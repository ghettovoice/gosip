package sip

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/pkg/log"
)

// Proxy is a SIP proxy middleware for an [Element].
// It claims inbound requests matching its routes and forwards them according
// to the route configuration.
type Proxy struct {
	elm         *Element
	routes      []ProxyRoute
	opts        ProxyOptions
	selfAddrs   []Addr
	domainAddrs []Addr
	unbind      func()

	// slFallbackKey is the instance-local random key reserved for the
	// stateless sends a stateful-only proxy must still perform without a
	// transaction: the mandatory unmatched-CANCEL path of RFC 3261
	// Section 16.10 and the end-to-end ACK. It is generated only when no
	// [ProxyOptions.StatelessKey] is configured and never authorizes an
	// explicitly stateless route.
	slFallbackKey []byte

	// closed commits the lifecycle end: admission is sealed before any
	// detach work runs, so no new request claim, request or restore
	// registration, or observer bind can succeed afterwards.
	closed atomic.Bool

	// closeOnce runs the shared one-shot shutdown exactly once for both
	// [Proxy.Shutdown] and [Proxy.Close].
	closeOnce sync.Once

	// subMu guards the observer and snapshot subscription unbinds tracked
	// for lifecycle release. It is never held while an unbind or a
	// dispatched callback runs.
	subMu  sync.Mutex
	subSeq uint64
	subs   map[uint64]func()

	// reqsMu guards srvRqs, the index of owned requests by their upstream
	// server transaction key. It only protects index lookups and updates;
	// request decisions stay serialized inside each request's own drain.
	reqsMu sync.Mutex
	srvRqs map[ServerTransactionKey]*ProxyRequest

	// onSnap manages the request snapshot subscriptions of
	// [Proxy.BindSnapshotHandler].
	onSnap shared.CallbackManager[ProxyRequestSnapshotHandler]

	// onEvent manages the observation event subscriptions of
	// [Proxy.BindEventHandler].
	onEvent shared.CallbackManager[ProxyEventHandler]

	// rejSends correlates answered rejections of coordinator-less
	// operations — an early matched rejection or a failed stateless
	// traversal — with their pending upstream response sends, keyed by the
	// normalized server transaction identity. It only bridges an
	// interceptor return and the transport's answering send; the FIFO
	// order bounds it to a few in-flight records, never history.
	rejMu    sync.Mutex
	rejSends map[ServerTransactionKey]*prxRejectionSend
	rejOrd   []ServerTransactionKey
}

var (
	_ InboundRequestInterceptor   = (*Proxy)(nil)
	_ InboundResponseInterceptor  = (*Proxy)(nil)
	_ OutboundResponseInterceptor = (*Proxy)(nil)
)

// NewProxy creates a [Proxy] middleware bound to the element.
//
// Routes and options are validated and copied: later changes to the caller's
// slices, maps, and URIs do not affect the running proxy, and a failed
// construction installs no middleware. The constructor rejects a nil element,
// duplicate route names, unsupported forward modes or non-INVITE server
// transaction behaviors, and out-of-range Timer C values.
func NewProxy(elm *Element, routes []ProxyRoute, opts ...ProxyOptions) (*Proxy, error) {
	if elm == nil {
		return nil, errors.ErrorWrap("nil element")
	}

	o := util.LastSliceElem(opts, ProxyOptions{}).clone()
	if err := o.validate(); err != nil {
		return nil, errors.Wrap(err)
	}
	o.TimerC = o.timeC()

	ownedRoutes := slices.Clone(routes)
	names := make(map[string]struct{}, len(ownedRoutes))
	for i := range ownedRoutes {
		r := &ownedRoutes[i]
		if err := r.validate(); err != nil {
			return nil, errors.Wrap(err)
		}
		if r.ForwardMode == ForwardModeStateless && len(o.StatelessKey) == 0 {
			return nil, errors.ErrorfWrap(
				"stateless proxy route %q requires a configured StatelessKey", r.Name,
			)
		}
		if _, dup := names[r.Name]; dup {
			return nil, errors.ErrorfWrap("duplicate proxy route name %q", r.Name)
		}
		names[r.Name] = struct{}{}
	}

	prx := &Proxy{
		elm:         elm,
		routes:      ownedRoutes,
		opts:        o,
		selfAddrs:   prxIdentityAddrs(o.LocalIdentities),
		domainAddrs: prxDomainAddrs(o.ServedDomains),
		srvRqs:      make(map[ServerTransactionKey]*ProxyRequest),
	}
	if len(o.StatelessKey) == 0 {
		prx.slFallbackKey = []byte(util.RandString(32))
	}
	prx.unbind = elm.UseMiddleware(prx)
	return prx, nil
}

func prxIdentityAddrs(ids []*URI) []Addr {
	addrs := make([]Addr, 0, len(ids))
	for _, u := range ids {
		addrs = append(addrs, u.Addr)
	}
	return addrs
}

func prxDomainAddrs(domains []string) []Addr {
	addrs := make([]Addr, 0, len(domains))
	for _, d := range domains {
		addrs = append(addrs, MakeHostAddr(d))
	}
	return addrs
}

func (prx *Proxy) LogValue() slog.Value {
	if prx == nil {
		return slog.Value{}
	}

	return slog.GroupValue(
		slog.String("ptr", fmt.Sprintf("%p", prx)),
	)
}

func (prx *Proxy) logger(ctx context.Context) *slog.Logger {
	return log.LoggerFromValues(ctx, prx.elm)
}

const prxRouteMetaKey = "sip.proxy_route"

func (prx *Proxy) InterceptInboundRequest(
	ctx context.Context,
	next RequestReceiver,
	req *RequestEnvelope,
) error {
	if prx.closed.Load() {
		// A sealed proxy claims nothing: the request continues down the
		// receiver chain as if no route matched.
		return errors.Wrap(next.RecvRequest(ctx, req))
	}

	route := prx.shouldForwardReq(ctx, req)
	if route == nil {
		return errors.Wrap(next.RecvRequest(ctx, req))
	}

	req.Metadata().Set(prxRouteMetaKey, route.Name)

	prx.logger(ctx).With(
		slog.Any("inbound_request", req),
		slog.Any("proxy_route", route),
	).DebugContext(ctx, "proxy route resolved")

	// For all new inbound requests, including any with unknown methods,
	// an element intending to proxy the request MUST:
	// 	1. Validate the request (Section 16.3);
	// 	2. Initialize server transaction if stateful forward required;
	// 	3. Authorize the request (Section 16.3, step 6);
	// 	4. Preprocess routing information (Section 16.4);
	// 	5. Determine target(s) for the request (Section 16.5);
	// 	6. Forward the request to each target (Section 16.6);
	// 	7. Process all responses (Section 16.7).

	if err := prx.validateReq(ctx, route, req); err != nil {
		return prx.observedReqResult(ctx, route, req, err)
	}

	// A CANCEL is matched against the live response contexts before any
	// location resolution: a matching CANCEL is acknowledged on its own
	// transaction and cancels the pending branches of the related request,
	// while an unmatched one takes the deterministic stateless forwarding
	// of Section 16.10.
	if req.Method().Equal(RequestMethodCancel) {
		return prx.observedReqResult(ctx, route, req, prx.handleCancel(ctx, route, req))
	}

	// An ACK never gains a response context: a non-2xx ACK is dispatched
	// to the matching INVITE server transaction and absorbed by its
	// retransmission handling, while an end-to-end ACK is routed through
	// the same prepared-target path without a transaction or a provisional
	// generation.
	if req.Method().Equal(RequestMethodAck) {
		return prx.observedReqResult(ctx, route, req, prx.forwardAck(ctx, route, req))
	}

	// Stateful forwarding is driven by the per-request coordinator: the
	// remaining pipeline stages run inside admission — server transaction
	// admission and duplicate handling, authorization, routing preprocess
	// and target resolution — and inside the coordinator drain: selection,
	// outbound copy preparation, transport finalization and the attempt
	// send.
	if route.ForwardMode == ForwardModeStateful {
		return prx.observedReqResult(ctx, route, req, prx.admitStatefulReq(ctx, route, req))
	}

	// Stateless forwarding prepares a deterministic destination and branch.
	// Pre-send promotion to stateful admission continues from the accepted
	// working request and complete target plan without repeating
	// authorization, routing preprocessing, or target resolution.
	if err := prx.authorizeReq(ctx, route, req); err != nil {
		return prx.observedReqResult(ctx, route, req, err)
	}

	wReq, err := prx.preprocessRouting(ctx, route, req)
	if err != nil {
		return prx.observedReqResult(ctx, route, req, err)
	}

	plan, err := prx.resolveTargets(ctx, route, wReq)
	if err != nil {
		return prx.observedReqResult(ctx, route, req, err)
	}

	return prx.observedReqResult(ctx, route, req, prx.forwardStatelessReq(ctx, route, req, wReq, plan))
}

// prxStepResult maps an internal request-consumption signal to a silent nil
// result and wraps every other failure for the interceptor caller.
func prxStepResult(err error) error {
	switch {
	case err == nil, errors.Is(err, errPrxReqConsumed):
		return nil
	default:
		return errors.Wrap(err)
	}
}

// prxReqResult applies the method response rules at the pipeline exit for
// every rejection source — an ACK is never answered and a CANCEL is never
// authentication-challenged — then maps the internal request-consumption
// signal and wraps every other failure for the interceptor caller.
// RFC 3261 Sections 9.2 and 16.3.
func prxReqResult(req *RequestEnvelope, err error) error {
	return prxStepResult(adjustReqRejection(req, err))
}

func (prx *Proxy) shouldForwardReq(ctx context.Context, req *RequestEnvelope) *ProxyRoute {
	for i := range prx.routes {
		r := &prx.routes[i]
		if r.MatchRequest == nil || r.MatchRequest(ctx, req) {
			return r
		}
	}

	return nil
}

const (
	errUnsupURIScheme errors.Error = "unsupported URI scheme"
	errTooManyHops    errors.Error = "too many hops"
	errUnsupOption    errors.Error = "unsupported option"
	errNoFwdTargets   errors.Error = "no forward targets"
	errPrxReqConsumed errors.Error = "proxy request consumed"
)

func (prx *Proxy) processReqStrictRouting(env *RequestEnvelope, req *Request) {
	ru, ok := req.URI.(*URI)
	if !ok {
		return
	}

	// If the Request-URI of the request contains a value this proxy previously
	// placed into a Record-Route header field (see Section 16.6 item 4),
	// the proxy MUST replace the Request-URI in the request with the last
	// value from the Route header field, and remove that value from the
	// Route header field.
	if !prx.isSelfAddr(ru.Addr, env.Transport().DefaultPort) {
		return
	}

	hop, ok := req.Headers.LastRoute()
	if !ok {
		return
	}

	hu, ok := hop.URI.(*URI)
	if !ok {
		return
	}

	req.URI = hu.Clone().(*URI) //nolint:forcetypeassert // the concrete type is fixed by construction
	req.Headers.PopLastRoute()
}

func (prx *Proxy) processReqURIMAddr(env *RequestEnvelope, req *Request) {
	ru, ok := req.URI.(*URI)
	if !ok {
		return
	}

	// If the Request-URI contains a maddr parameter, the proxy MUST check
	// to see if its value is in the set of addresses or domains the proxy
	// is configured to be responsible for.  If the Request-URI has a maddr
	// parameter with a value the proxy is responsible for, and the request
	// was received using the port and transport indicated (explicitly or by
	// default) in the Request-URI, the proxy MUST strip the maddr and any
	// non-default port or transport parameter and continue processing as if
	// those values had not been present in the request.
	maddr, ok := ru.MAddr()
	if !ok || !maddr.IsValid() || !prx.isResponsibleAddr(maddr) {
		return
	}

	ruTp := UDPMetadata()
	if ru.Secured {
		ruTp = TLSMetadata()
	}

	if v, ok := ru.Transport(); ok && v.IsValid() {
		if m, ok := prx.elm.TransportMetadataByProto(v); ok && m.IsValid() {
			ruTp = m
		}
	}

	ruPort := ruTp.DefaultPort
	if v, ok := ru.Addr.Port(); ok && v > 0 {
		ruPort = v
	}

	if !env.Transport().Proto.Equal(ruTp.Proto) || env.LocalAddr().Port() != ruPort {
		return
	}

	ru.Addr = MakeHostAddr(ru.Addr.Host())
	ru.Params.Delete("maddr").Delete("transport")
}

func (prx *Proxy) processReqFirstRoute(env *RequestEnvelope, req *Request) {
	// If the first value in the Route header field indicates this proxy,
	// the proxy MUST remove that value from the request.
	hop, ok := req.Headers.FirstRoute()
	if !ok {
		return
	}

	hu, ok := hop.URI.(*URI)
	if !ok || !prx.isSelfAddr(hu.Addr, env.Transport().DefaultPort) {
		return
	}

	req.Headers.PopFirstRoute()
}

// isSelfAddr reports whether the address names one of the proxy's own
// advertised identities, for example a URI previously placed into a
// Record-Route or Route header field value.
func (prx *Proxy) isSelfAddr(addr Addr, defPort uint16) bool {
	return matchPrxAddr(prx.selfAddrs, addr, defPort)
}

// isResponsibleAddr reports whether the address is one the proxy is
// responsible for: either its own identity or a configured served domain.
func (prx *Proxy) isResponsibleAddr(addr Addr) bool {
	return prx.isSelfAddr(addr, 0) || prx.isServedAddr(addr)
}

func matchPrxAddr(addrs []Addr, addr Addr, defPort uint16) bool {
	if defPort > 0 {
		addr = addr.WithPortIfMissing(defPort)
	} else {
		addr = addr.WithoutPort()
	}

	for _, laddr := range addrs {
		if defPort > 0 {
			laddr = laddr.WithPortIfMissing(defPort)
		} else {
			laddr = laddr.WithoutPort()
		}

		if laddr.Equal(addr) {
			return true
		}
	}

	return false
}

// singlePrxTarget reports whether the plan contains exactly one logical
// target across all groups. Target identity is independent of URI equality.
func singlePrxTarget(plan ProxyTargetPlan) (ProxyTarget, bool) {
	var target ProxyTarget
	found := false
	for _, group := range plan.Groups {
		for _, trgt := range group {
			if found {
				return ProxyTarget{}, false
			}
			target, found = trgt, true
		}
	}
	return target, found
}

// forwardStatelessReq enforces one logical target and one bound destination.
// RFC 3261 Section 16.6.
//
// Multi-target plans promote intact before any preparation or observation;
// ACK and unmatched CANCEL fail routing instead. Single-target destination
// failures may also require pre-send promotion. Once a send is attempted,
// neither promotion nor alternate-destination retry is permitted.
func (prx *Proxy) forwardStatelessReq(
	ctx context.Context,
	route *ProxyRoute,
	req *RequestEnvelope,
	work *RequestEnvelope,
	plan ProxyTargetPlan,
) error {
	trgt, single := singlePrxTarget(plan)
	if !single && plan.hasTargets() {
		err := prx.statelessPromoteErr(req, errors.PrefixWrap(
			errNoStatelessDest, "multiple logical targets",
		))
		if !errors.Is(err, errPrxStatelessPromote) {
			return errors.Wrap(err)
		}
		prepared, err := newPrxPreparedRouting(req, work, plan)
		if err != nil {
			return errors.Wrap(err)
		}
		return errors.Wrap(prx.promoteStatefulReq(ctx, route, prepared))
	}

	var (
		out        *RequestEnvelope
		bound      ProxyBoundDelivery
		err        error
		trgtErrs   []error
		failedAtts []ProxyEvent
	)
	if single {
		out, bound, err = prx.prepareStatelessFwd(ctx, route, prxRequestInput{original: req, working: work}, trgt)
		if errors.Is(err, errPrxStatelessPromote) {
			// The winning stateful admission owns the operation's
			// observations, so the stateless path emits nothing.
			prepared, err := newPrxPreparedRouting(req, work, plan)
			if err != nil {
				return errors.Wrap(err)
			}
			return errors.Wrap(prx.promoteStatefulReq(ctx, route, prepared))
		}
		if err != nil {
			trgtErrs = append(trgtErrs, err)
			// Buffer a failed preparation until Handled owns sequence 1.
			failedAtts = append(failedAtts, ProxyEvent{
				Kind:      ProxyEventForwardAttemptFinished,
				TargetID:  trgt.ID,
				AttemptID: trgt.ID,
				Error:     errors.Wrap(&RequestAttemptError{Cause: err}),
			})
		}
	}

	op := newPrxOperation(route, req.Method())
	prx.observeStatelessPrepared(ctx, op, failedAtts)

	if !single || err != nil {
		// If the target set remains empty after applying all of the above,
		// the proxy MUST return an error response, which SHOULD be the 480
		// (Temporarily Unavailable) response.
		rej := &RequestRejectedError{
			cause:  errors.JoinPrefixWrap("stateless forward errors:", append([]error{errNoFwdTargets}, trgtErrs...)...),
			resSts: ResponseStatusTemporarilyUnavailable,
			logLvl: slog.LevelDebug,
		}
		op.emit(ctx, prx, ProxyEvent{
			Kind:       ProxyEventRequestCompleted,
			Outcome:    ProxyOutcomeRejected,
			StatusCode: int(ResponseStatusTemporarilyUnavailable),
			Error:      errors.Wrap(rej),
		})
		if key, err := MakeServerTransactionKey(req); err == nil {
			prx.trackRejectedSend(key, op, ResponseStatusTemporarilyUnavailable)
		}
		return errors.JoinWrap(errPrxObserved, errors.Wrap(rej))
	}

	err = bound.Transport.SendRequest(ctx, out)
	op.emit(ctx, prx, ProxyEvent{
		Kind:      ProxyEventForwardAttemptFinished,
		TargetID:  trgt.ID,
		AttemptID: trgt.ID,
		Error:     err,
	})
	if err == nil {
		op.emit(ctx, prx, ProxyEvent{
			Kind:      ProxyEventRequestForwarded,
			TargetID:  trgt.ID,
			AttemptID: trgt.ID,
		})
		op.emit(ctx, prx, ProxyEvent{
			Kind:    ProxyEventRequestCompleted,
			Outcome: ProxyOutcomeSent,
		})
		return nil
	}

	err = errors.JoinPrefixWrap("stateless forward errors:", err)
	op.emit(ctx, prx, ProxyEvent{
		Kind:    ProxyEventRequestCompleted,
		Outcome: ProxyOutcomeSendFailure,
		Error:   err,
	})
	// The transport answers the failure with a generated 500: the pending
	// record lets the answering send close the operation's event stream.
	if key, keyErr := MakeServerTransactionKey(req); keyErr == nil {
		prx.trackRejectedSend(key, op, ResponseStatusServerInternalError)
	}
	return errors.JoinWrap(errPrxObserved, err)
}

// observeStatelessPrepared starts observations only after all pre-send
// promotion decisions are past. Buffered failures retain target order and
// follow Handled, before any completion or wire send.
func (prx *Proxy) observeStatelessPrepared(ctx context.Context, op *prxOperation, failedAtts []ProxyEvent) {
	op.emit(ctx, prx, ProxyEvent{Kind: ProxyEventRequestHandled})
	for _, ev := range failedAtts {
		op.emit(ctx, prx, ev)
	}
}

// newSrvTx is the single factory seam for the route-owned
// upstream server transaction: the route's ServerTransactionOptions pass
// through unchanged — the proxy never overrides the application's
// transaction behavior selection — and the transaction is never started
// inside the factory. The matching-CANCEL admission path reuses the same
// seam so a related CANCEL transaction inherits identical options.
func (prx *Proxy) newSrvTx(
	ctx context.Context,
	route *ProxyRoute,
	req *RequestEnvelope,
	tp ServerTransport,
) (ServerTransaction, error) {
	return errors.Wrap2(prx.elm.NewServerTransaction(ctx, req, tp, route.ServerTransactionOptions))
}

// InterceptOutboundResponse intercepts outbound responses on the transport
// send path.
//
// A proxy-initiated upstream send is correlated through a private one-shot
// context token minted by [ProxyRequest.sendUpstream]: the interceptor calls
// the next sender, settles the token with the real wire result, and enqueues
// it for the owning request coordinator. No observer work is dispatched
// inside the transaction action — the enqueue is the only coordinator touch,
// and the real result is returned to the transport unchanged. Sends without
// a token pass through.
func (prx *Proxy) InterceptOutboundResponse(
	ctx context.Context,
	next ResponseSender,
	res *ResponseEnvelope,
	opts ...SendResponseOptions,
) error {
	send, tok := prxSendFromCtx(ctx)
	switch {
	case tok:
		err := next.SendResponse(ctx, res, opts...)
		send.settle(ctx, err)
		return errors.Wrap(err)
	default:
		// A coordinator-less rejection answers through the transport
		// respond path — never inside a transaction action: the pending
		// record closes the operation's event stream with the real wire
		// result.
		rec, recOK := prx.takeRejectedSend(res)
		err := next.SendResponse(ctx, res, opts...)
		if recOK && err == nil {
			rec.op.emit(ctx, prx, ProxyEvent{
				Kind:       ProxyEventResponseForwarded,
				StatusCode: int(res.Status()),
			})
		}
		return errors.Wrap(err)
	}
}

// InterceptInboundResponse intercepts inbound responses on the transport
// receive path.
//
// Ownership is decided by the proxy-namespace mark embedded in the top
// Via branch — never by a localhost Via guess. A response authentically
// owned by this instance's stateless path ([Proxy.shouldForwardRes]) is
// relayed to the prior hop recorded in the Via stack by
// [Proxy.forwardStatelessRes]. A response carrying the stateful mark
// belongs to a client transaction of the stateful path: the transaction
// manager claims it ahead of this middleware while the transaction is
// live, and a stray response that outlived its transaction is discarded
// (RFC 4320, RFC 6026). A claimed Via that fails sent-by validation is
// consumed rather than relayed. A response outside the proxy namespace
// is foreign and continues down the receiver chain.
//
// No transaction is created and no locally generated provisional
// response is emitted on this path.
func (prx *Proxy) InterceptInboundResponse(
	ctx context.Context,
	next ResponseReceiver,
	res *ResponseEnvelope,
) error {
	if prx.closed.Load() {
		return errors.Wrap(next.RecvResponse(ctx, res))
	}

	mark, _, owned := prxResMark(res)
	switch {
	case owned && mark == prxBranchStatelessMark:
		if !prx.shouldForwardRes(ctx, res) {
			// A stateless claim whose Via fails sent-by validation is
			// consumed, never relayed.
			return nil
		}
		return errors.Wrap(prx.forwardStatelessRes(ctx, res))
	case owned && mark == prxBranchStatefulMark:
		if prx.hasLiveClnTx(res) {
			// Owned by a live client transaction: the stateless relay
			// never steals it, and the rest of the chain still observes
			// it.
			return errors.Wrap(next.RecvResponse(ctx, res))
		}
		// A stray stateful response that outlived its transaction is
		// discarded (RFC 4320/6026).
		return nil
	default:
		return errors.Wrap(next.RecvResponse(ctx, res))
	}
}

// ErrProxyClosed reports an operation attempted on a proxy whose lifecycle
// already ended: a new request claim, a restore install, or an observer
// bind.
const ErrProxyClosed errors.Error = "proxy closed"

// NewProxyClosedError creates an [ErrProxyClosed] error classified as a
// closed proxy error.
func NewProxyClosedError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrProxyClosed, args...),
		ErrClassProxy,
		errclass.ClassClosed,
	)
}

// Shutdown gracefully shuts the proxy down.
//
// It is the optional element middleware shutdown hook: the owning element
// invokes it before middleware and transport closure, and a direct call is
// equivalent to [Proxy.Close]. Shutdown seals admission first — new inbound
// requests pass through unclaimed and no new coordinator, restore, or
// observer subscription can register — then detaches the middleware
// registration and every tracked subscription, retires the owned request
// coordinators, and releases the request indexes. Coordinator retirement
// drops queued scheduling, disarms owned timers, detaches staged
// transaction subscriptions, and terminates owned transactions; borrowed
// transports, application storage, and transactions the proxy does not own
// are never closed.
//
// No callback, application bind, or transaction effect runs under a lock,
// and Shutdown never waits on an in-flight dispatch that already captured
// an observer, so a reentrant unbind — or a reentrant Shutdown — inside a
// callback is safe. Only the first call runs; concurrent and repeated
// calls share its result.
func (prx *Proxy) Shutdown(ctx context.Context) error {
	prx.closeOnce.Do(func() {
		// The lifecycle work must finish even on a canceled caller
		// context: it keeps the context values but drops cancellation.
		prx.shutdown(context.WithoutCancel(ctx))
	})
	return nil
}

// Close releases every resource owned by the proxy. It is equivalent to
// [Proxy.Shutdown] and idempotent: repeated calls share the first result.
// Close also serves as the middleware close hook of the owning element.
func (prx *Proxy) Close() error {
	return prx.Shutdown(context.Background())
}

// shutdown is the shared one-shot lifecycle path of [Proxy.Shutdown] and
// [Proxy.Close]. It runs every detach and termination outside the index
// and subscription guards.
func (prx *Proxy) shutdown(ctx context.Context) {
	// Seal admission before any detach work: a request claim, a request
	// or restore registration, or an observer bind that lost the race is
	// refused rather than orphaned past the release snapshot.
	prx.closed.Store(true)

	prx.subMu.Lock()
	subs := prx.subs
	prx.subs = nil
	prx.subMu.Unlock()

	prx.reqsMu.Lock()
	rqs := make([]*ProxyRequest, 0, len(prx.srvRqs))
	for key, rq := range prx.srvRqs {
		rqs = append(rqs, rq)
		delete(prx.srvRqs, key)
	}
	prx.reqsMu.Unlock()

	prx.rejMu.Lock()
	prx.rejSends = nil
	prx.rejOrd = nil
	prx.rejMu.Unlock()

	// The middleware registration detaches outside every lock, so no new
	// inbound or transaction work reaches the proxy while its request
	// coordinators retire.
	if prx.unbind != nil {
		prx.unbind()
		prx.unbind = nil
	}
	for _, unbind := range subs {
		unbind()
	}
	for _, rq := range rqs {
		rq.shutdown(ctx)
	}
}

// trackSub registers one observer subscription unbind for lifecycle
// release and returns the public unbind. The callback registration runs
// under the subscription guard, so a bind racing the seal either commits
// before the release snapshot or is refused with a noop unbind. The
// returned unbind is idempotent and never waits on an in-flight dispatch
// that already captured the handler.
func (prx *Proxy) trackSub(add func() (unbind func())) func() {
	prx.subMu.Lock()
	defer prx.subMu.Unlock()
	if prx.closed.Load() {
		return noop
	}

	inner := add()
	id := prx.subSeq
	prx.subSeq++
	if prx.subs == nil {
		prx.subs = make(map[uint64]func())
	}
	var once sync.Once
	prx.subs[id] = func() {
		once.Do(func() {
			inner()
			prx.subMu.Lock()
			delete(prx.subs, id)
			prx.subMu.Unlock()
		})
	}
	return prx.subs[id]
}
