package proxy

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
	"github.com/ghettovoice/gosip/pkg/log"
	"github.com/ghettovoice/gosip/sip"
)

// Proxy is a SIP proxy component for an [sip.Element].
// It claims inbound requests matching its routes and forwards them according
// to the route configuration.
type Proxy struct {
	routes      []Route
	opts        Options
	selfAddrs   []sip.Addr
	domainAddrs []sip.Addr

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

	// reqsMu guards elms, the per-element processing scopes of
	// [elemState] keyed by the *sip.Element pointer. It only protects
	// scope map lookups and creation; index and rejection state stay
	// serialized inside each scope's own guards, and request decisions
	// inside each request's own drain. A created scope lives until global
	// shutdown.
	reqsMu sync.Mutex
	elms   map[*sip.Element]*elemState

	// onSnap manages the request snapshot subscriptions of
	// [Proxy.BindSnapshotHandler].
	onSnap shared.CallbackManager[RequestSnapshotHandler]

	// onEvent manages the observation event subscriptions of
	// [Proxy.BindEventHandler].
	onEvent shared.CallbackManager[EventHandler]
}

var (
	_ sip.InboundRequestInterceptor   = (*Proxy)(nil)
	_ sip.InboundResponseInterceptor  = (*Proxy)(nil)
	_ sip.OutboundResponseInterceptor = (*Proxy)(nil)
)

// New creates a [Proxy] component.
//
// Routes and options are validated and copied: later changes to the caller's
// slices, maps, and URIs do not affect the running proxy, and a failed
// construction installs nothing. The constructor has no element and performs
// no attachment work: an explicit [sip.Element.UseComponent] registration
// binds the proxy to an element's processing chain, and every claimed
// interception resolves its processing element from the interception context.
// The constructor rejects duplicate route names, unsupported forward modes or
// non-INVITE server transaction behaviors, and out-of-range Timer C values.
func New(routes []Route, opts ...Options) (*Proxy, error) {
	o := util.LastSliceElem(opts, Options{}).clone()
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
		routes:      ownedRoutes,
		opts:        o,
		selfAddrs:   identityAddrs(o.LocalIdentities),
		domainAddrs: domainAddrs(o.ServedDomains),
	}
	return prx, nil
}

func identityAddrs(ids []*sip.URI) []sip.Addr {
	addrs := make([]sip.Addr, 0, len(ids))
	for _, u := range ids {
		addrs = append(addrs, u.Addr)
	}
	return addrs
}

func domainAddrs(domains []string) []sip.Addr {
	addrs := make([]sip.Addr, 0, len(domains))
	for _, d := range domains {
		addrs = append(addrs, sip.MakeHostAddr(d))
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

// logger resolves the processing logger from the canonical processing
// context: the element installed by the interception or request context wins,
// then any context-carried logger, then the package default.
func (prx *Proxy) logger(ctx context.Context) *slog.Logger {
	if elm, ok := sip.ElementFromContext(ctx); ok {
		return log.LoggerFromValues(ctx, elm)
	}
	return log.LoggerFromValues(ctx)
}

func (prx *Proxy) InterceptInboundRequest(
	ctx context.Context,
	next sip.RequestReceiver,
	req *sip.RequestEnvelope,
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

	// The claim binds the processing element carried by the interception
	// context — installed by the owning element's component interceptors — before
	// any metadata, index state, or traffic is produced.
	elm, ok := sip.ElementFromContext(ctx)
	if !ok {
		return errors.Wrap(NewMissingElementError())
	}

	// The operation identity is minted once at the claim, before any
	// claimed work or rejection outcome: the stateful coordinator and
	// the lightweight operation paths adopt it unchanged, and the claim
	// stamp carries it into every cloned working and outbound copy. The
	// whole-value stamp also replaces any proxy metadata a previous
	// traversal left on the envelope.
	reqID := util.RandString(24)
	md := Metadata{
		RequestID: reqID,
		RouteName: route.Name,
		Mode:      route.ForwardMode,
		Stage:     StageClaim,
	}
	stampClaim(req, md)

	prx.logger(ctx).DebugContext(ctx, "proxy route resolved",
		slog.Any("inbound_request", req),
		slog.Any("proxy_route", route),
	)

	// For all new inbound requests, including any with unknown methods,
	// an element intending to proxy the request MUST:
	// 	1. Validate the request (Section 16.3);
	// 	2. Initialize server transaction if stateful forward required;
	// 	3. Authorize the request (Section 16.3, step 6);
	// 	4. Preprocess routing information (Section 16.4);
	// 	5. Determine target(s) for the request (Section 16.5);
	// 	6. Forward the request to each target (Section 16.6);
	// 	7. Process all responses (Section 16.7).

	if err := prx.validateReq(ctx, elm, route, req); err != nil {
		return prx.observedReqResult(ctx, elm, route, req, err)
	}

	// A CANCEL is matched against the live response contexts before any
	// location resolution: a matching CANCEL is acknowledged on its own
	// transaction and cancels the pending branches of the related request,
	// while an unmatched one takes the deterministic stateless forwarding
	// of Section 16.10.
	if req.Method().Equal(sip.RequestMethodCancel) {
		return prx.observedReqResult(ctx, elm, route, req, prx.handleCancel(ctx, elm, route, req))
	}

	// An ACK never gains a response context: a non-2xx ACK is dispatched
	// to the matching INVITE server transaction and absorbed by its
	// retransmission handling, while an end-to-end ACK is routed through
	// the same prepared-target path without a transaction or a provisional
	// generation.
	if req.Method().Equal(sip.RequestMethodAck) {
		return prx.observedReqResult(ctx, elm, route, req, prx.forwardAck(ctx, elm, route, req))
	}

	// Stateful forwarding is driven by the per-request coordinator: the
	// remaining pipeline stages run inside admission — server transaction
	// admission and duplicate handling, authorization, routing preprocess
	// and target resolution — and inside the coordinator drain: selection,
	// outbound copy preparation, transport finalization and the attempt
	// send.
	if route.ForwardMode == ForwardModeStateful {
		return prx.observedReqResult(ctx, elm, route, req, prx.admitStatefulReq(ctx, elm, route, req))
	}

	// Stateless forwarding prepares a deterministic destination and branch.
	// Pre-send promotion to stateful admission continues from the accepted
	// working request and complete target plan without repeating
	// authorization, routing preprocessing, or target resolution.
	if err := prx.authorizeReq(ctx, route, req); err != nil {
		return prx.observedReqResult(ctx, elm, route, req, err)
	}

	wReq, err := prx.preprocessRouting(ctx, elm, route, req)
	if err != nil {
		return prx.observedReqResult(ctx, elm, route, req, err)
	}

	plan, err := prx.resolveTargets(ctx, route, wReq)
	if err != nil {
		return prx.observedReqResult(ctx, elm, route, req, err)
	}

	return prx.observedReqResult(ctx, elm, route, req, prx.forwardStatelessReq(ctx, elm, route, req, wReq, plan))
}

// stepResult maps an internal request-consumption signal to a silent nil
// result and wraps every other failure for the interceptor caller.
func stepResult(err error) error {
	switch {
	case err == nil, errors.Is(err, errReqConsumed):
		return nil
	default:
		return errors.Wrap(err)
	}
}

// reqResult applies the method response rules at the pipeline exit for
// every rejection source — an ACK is never answered and a CANCEL is never
// authentication-challenged — then maps the internal request-consumption
// signal and wraps every other failure for the interceptor caller.
// RFC 3261 Sections 9.2 and 16.3.
func reqResult(req *sip.RequestEnvelope, err error) error {
	return stepResult(adjustReqRejection(req, err))
}

func (prx *Proxy) shouldForwardReq(ctx context.Context, req *sip.RequestEnvelope) *Route {
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
	errReqConsumed    errors.Error = "proxy request consumed"
)

func (prx *Proxy) processReqStrictRouting(env *sip.RequestEnvelope, req *sip.Request) {
	ru, ok := req.URI.(*sip.URI)
	if !ok {
		return
	}

	// If the Request-URI of the request contains a value this proxy previously
	// placed into a Record-Route header field (see Section 16.6 item 4),
	// the proxy MUST replace the Request-URI in the request with the last
	// value from the Route header field, and remove that value from the
	// Route header field.
	if !prx.IsSelfAddr(ru.Addr, env.Transport().DefaultPort) {
		return
	}

	hop, ok := req.Headers.LastRoute()
	if !ok {
		return
	}

	hu, ok := hop.URI.(*sip.URI)
	if !ok {
		return
	}

	req.URI = hu.Clone().(*sip.URI) //nolint:forcetypeassert // the concrete type is fixed by construction
	req.Headers.PopLastRoute()
}

func (prx *Proxy) processReqURIMAddr(elm *sip.Element, env *sip.RequestEnvelope, req *sip.Request) {
	ru, ok := req.URI.(*sip.URI)
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
	if !ok || !maddr.IsValid() || !prx.IsResponsibleAddr(maddr) {
		return
	}

	ruTp := sip.UDPMetadata()
	if ru.Secured {
		ruTp = sip.TLSMetadata()
	}

	if v, ok := ru.Transport(); ok && v.IsValid() {
		if m, ok := elm.TransportMetadataByProto(v); ok && m.IsValid() {
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

	ru.Addr = sip.MakeHostAddr(ru.Addr.Host())
	ru.Params.Delete("maddr").Delete("transport")
}

func (prx *Proxy) processReqFirstRoute(env *sip.RequestEnvelope, req *sip.Request) {
	// If the first value in the Route header field indicates this proxy,
	// the proxy MUST remove that value from the request.
	hop, ok := req.Headers.FirstRoute()
	if !ok {
		return
	}

	hu, ok := hop.URI.(*sip.URI)
	if !ok || !prx.IsSelfAddr(hu.Addr, env.Transport().DefaultPort) {
		return
	}

	req.Headers.PopFirstRoute()
}

// IsSelfAddr reports whether the address names one of the proxy's own
// advertised identities configured through [Options.LocalIdentities], for
// example a URI previously placed into a Record-Route or Route header field
// value. A configured served domain is not a local routing identity; query
// it with [Proxy.IsServedAddr] instead.
//
// A positive defaultPort fills a missing port on both sides of the
// comparison; explicit ports remain authoritative. A zero defaultPort
// ignores ports entirely.
func (prx *Proxy) IsSelfAddr(addr sip.Addr, defaultPort uint16) bool {
	return matchAddr(prx.selfAddrs, addr, defaultPort)
}

// IsResponsibleAddr reports whether the address is one the proxy is
// responsible for: either its own identity ([Proxy.IsSelfAddr]) or a
// configured served domain ([Proxy.IsServedAddr]). Ports are ignored.
func (prx *Proxy) IsResponsibleAddr(addr sip.Addr) bool {
	return prx.IsSelfAddr(addr, 0) || prx.IsServedAddr(addr)
}

func matchAddr(addrs []sip.Addr, addr sip.Addr, defPort uint16) bool {
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

// singleTarget reports whether the plan contains exactly one logical
// target across all groups. Target identity is independent of URI equality.
func singleTarget(plan TargetPlan) (Target, bool) {
	var target Target
	found := false
	for _, group := range plan.Groups {
		for _, trgt := range group {
			if found {
				return Target{}, false
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
	elm *sip.Element,
	route *Route,
	req *sip.RequestEnvelope,
	work *sip.RequestEnvelope,
	plan TargetPlan,
) error {
	trgt, single := singleTarget(plan)
	if !single && plan.hasTargets() {
		err := prx.statelessPromoteErr(elm, req, errors.PrefixWrap(
			errNoStatelessDest, "multiple logical targets",
		))
		if !errors.Is(err, errStatelessPromote) {
			return errors.Wrap(err)
		}
		prepared, err := newPreparedRouting(req, work, plan)
		if err != nil {
			return errors.Wrap(err)
		}
		return prx.promoteStatefulResult(ctx, elm, route, req, prepared)
	}

	var (
		out        *sip.RequestEnvelope
		bound      BoundDelivery
		err        error
		trgtErrs   []error
		failedAtts []Event
	)
	if single {
		out, bound, err = prx.prepareStatelessFwd(ctx, elm, route, requestInput{original: req, working: work}, trgt)
		if errors.Is(err, errStatelessPromote) {
			// The winning stateful admission owns the operation's
			// observations, so the stateless path emits nothing.
			prepared, err := newPreparedRouting(req, work, plan)
			if err != nil {
				return errors.Wrap(err)
			}
			return prx.promoteStatefulResult(ctx, elm, route, req, prepared)
		}
		if err != nil {
			trgtErrs = append(trgtErrs, err)
			// Buffer a failed preparation until Handled owns sequence 1.
			failedAtts = append(failedAtts, Event{
				Kind:      EventKindForwardAttemptFinished,
				TargetID:  trgt.ID,
				AttemptID: trgt.ID,
				Error:     errors.Wrap(&sip.RequestAttemptError{Cause: err}),
			})
		}
	}

	op := newOperation(elm, route, req.Method(), claimID(req))
	prx.observeStatelessPrepared(ctx, op, failedAtts)

	if !single || err != nil {
		// If the target set remains empty after applying all of the above,
		// the proxy MUST return an error response, which SHOULD be the 480
		// (Temporarily Unavailable) response.
		rej := sip.NewRequestRejectedError(
			errors.JoinPrefixWrap("stateless forward errors:", append([]error{errNoFwdTargets}, trgtErrs...)...),
			slog.LevelDebug,
			sip.ResponseStatusTemporarilyUnavailable,
		)
		op.emit(ctx, prx, Event{
			Kind:       EventKindRequestCompleted,
			Outcome:    OutcomeRejected,
			StatusCode: int(sip.ResponseStatusTemporarilyUnavailable),
			Error:      errors.Wrap(rej),
		})
		if key, err := sip.MakeServerTransactionKey(req); err == nil {
			prx.trackRejectedSend(elm, key, op, sip.ResponseStatusTemporarilyUnavailable)
		}
		return errors.JoinWrap(errObserved, errors.Wrap(rej))
	}

	err = bound.Transport.SendRequest(ctx, out)
	op.emit(ctx, prx, Event{
		Kind:      EventKindForwardAttemptFinished,
		TargetID:  trgt.ID,
		AttemptID: trgt.ID,
		Error:     err,
	})
	if err == nil {
		op.emit(ctx, prx, Event{
			Kind:      EventKindRequestForwarded,
			TargetID:  trgt.ID,
			AttemptID: trgt.ID,
		})
		op.emit(ctx, prx, Event{
			Kind:    EventKindRequestCompleted,
			Outcome: OutcomeSent,
		})
		return nil
	}

	err = errors.JoinPrefixWrap("stateless forward errors:", err)
	op.emit(ctx, prx, Event{
		Kind:    EventKindRequestCompleted,
		Outcome: OutcomeSendFailure,
		Error:   err,
	})
	// The transport answers the failure with a generated 500: the pending
	// record lets the answering send close the operation's event stream.
	if key, keyErr := sip.MakeServerTransactionKey(req); keyErr == nil {
		prx.trackRejectedSend(elm, key, op, sip.ResponseStatusServerInternalError)
	}
	return errors.JoinWrap(errObserved, err)
}

// observeStatelessPrepared starts observations only after all pre-send
// promotion decisions are past. Buffered failures retain target order and
// follow Handled, before any completion or wire send.
func (prx *Proxy) observeStatelessPrepared(ctx context.Context, op *operation, failedAtts []Event) {
	op.emit(ctx, prx, Event{Kind: EventKindRequestHandled})
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
	elm *sip.Element,
	route *Route,
	req *sip.RequestEnvelope,
	tp sip.ServerTransport,
) (sip.ServerTransaction, error) {
	return errors.Wrap2(elm.NewServerTransaction(ctx, req, tp, route.ServerTransactionOptions))
}

// InterceptOutboundResponse intercepts outbound responses on the transport
// send path.
//
// A proxy-initiated upstream send is correlated through a private one-shot
// context token minted by [Request.sendUpstream]: the interceptor calls
// the next sender, settles the token with the real wire result, and enqueues
// it for the owning request coordinator. No observer work is dispatched
// inside the transaction action — the enqueue is the only coordinator touch,
// and the real result is returned to the transport unchanged. Sends without
// a token pass through.
func (prx *Proxy) InterceptOutboundResponse(
	ctx context.Context,
	next sip.ResponseSender,
	res *sip.ResponseEnvelope,
	opts ...sip.SendResponseOptions,
) error {
	send, tok := sendFromCtx(ctx)
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
		elm, _ := sip.ElementFromContext(ctx)
		rec, recOK := prx.takeRejectedSend(elm, res)
		err := next.SendResponse(ctx, res, opts...)
		if recOK && err == nil {
			rec.op.emit(ctx, prx, Event{
				Kind:       EventKindResponseForwarded,
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
// manager claims it ahead of this component while the transaction is
// live, and a stray response that outlived its transaction is discarded
// (RFC 4320, RFC 6026). A claimed Via that fails sent-by validation is
// consumed rather than relayed. A response outside the proxy namespace
// is foreign and continues down the receiver chain.
//
// No transaction is created and no locally generated provisional
// response is emitted on this path.
func (prx *Proxy) InterceptInboundResponse(
	ctx context.Context,
	next sip.ResponseReceiver,
	res *sip.ResponseEnvelope,
) error {
	if prx.closed.Load() {
		return errors.Wrap(next.RecvResponse(ctx, res))
	}

	mark, _, owned := resMark(res)
	if !owned {
		// A response outside the proxy namespace is foreign and continues
		// down the receiver chain.
		return errors.Wrap(next.RecvResponse(ctx, res))
	}

	elm, ok := sip.ElementFromContext(ctx)
	if !ok {
		return errors.Wrap(NewMissingElementError())
	}

	switch mark {
	case branchStatelessMark:
		if !prx.shouldForwardRes(ctx, elm, res) {
			// A stateless claim whose Via fails sent-by validation is
			// consumed, never relayed.
			return nil
		}
		return errors.Wrap(prx.forwardStatelessRes(ctx, elm, res))
	case branchStatefulMark:
		if prx.hasLiveClnTx(elm, res) {
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

// Shutdown gracefully shuts the proxy down.
//
// It is the optional element component shutdown hook: the owning element
// invokes it before component and transport closure, and a direct call is
// equivalent to [Proxy.Close]. The lifecycle is instance-wide: a proxy
// registered on several elements has one shutdown that seals admission on
// every element. Shutdown seals admission first — new inbound requests
// pass through unclaimed and no new coordinator, restore, or observer
// subscription can register — then releases every tracked subscription,
// retires each owned request coordinator under the element that claimed
// it, and clears every element scope so no later lookup or registration
// can resurrect one. The component registration itself belongs to the
// caller: it is detached by the unbind returned from
// [sip.Element.UseComponent], never by shutdown. Coordinator retirement
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
// Close also serves as the component close hook of the owning element.
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
	var rqs []*Request
	for _, es := range prx.elms {
		es.reqsMu.Lock()
		for key, rq := range es.srvRqs {
			rqs = append(rqs, rq)
			delete(es.srvRqs, key)
		}
		es.reqsMu.Unlock()

		es.rejMu.Lock()
		es.rejSends = nil
		es.rejOrd = nil
		es.rejMu.Unlock()
	}
	// Every element scope is released with the instance: the sealed create
	// path of getElemState keeps a racing admission from resurrecting
	// one, and a lookup reports no association for a cleared namespace.
	clear(prx.elms)
	prx.reqsMu.Unlock()

	// The component registration detachment belongs to the caller's
	// explicit [sip.Element.UseComponent] unbind; shutdown releases only
	// the subscriptions the proxy itself tracked.
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

// cloneReqEnvelope returns a deep copy of req, or nil for a nil envelope.
func cloneReqEnvelope(req *sip.RequestEnvelope) *sip.RequestEnvelope {
	if req == nil {
		return nil
	}
	return req.Clone().(*sip.RequestEnvelope) //nolint:forcetypeassert // the concrete type is fixed by construction
}

// cloneResEnvelope returns a deep copy of res, or nil for a nil envelope.
func cloneResEnvelope(res *sip.ResponseEnvelope) *sip.ResponseEnvelope {
	if res == nil {
		return nil
	}
	return res.Clone().(*sip.ResponseEnvelope) //nolint:forcetypeassert // the concrete type is fixed by construction
}
