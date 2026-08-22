package sip

import (
	"context"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofrs/uuid/v5"

	"github.com/ghettovoice/gosip/dns"
	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip/header"
)

// Element errors.
const (
	ErrElementClosed Error = "element closed"

	errServiceUnavail Error = "service unavailable"
)

type ElementID = uuid.UUID

func NextElementID() ElementID { return uuid.Must(uuid.NewV4()) }

// Element is the composition root for SIP transport, routing, and transaction processing.
//
// It provides the protocol-level mechanics shared by user agents, proxies, dialogs,
// and other higher-level components; it does not own their authentication or business response policy.
type Element struct {
	id      ElementID
	tpm     *TransportManager
	txm     *TransactionManager
	srvLctr RemoteServerLocator
	uriConv URIConverter
	log     *slog.Logger

	state atomic.Uint32

	closeOnce sync.Once
	closeErr  error

	mws syncutil.OrderedList[ElementMiddleware]

	baseCpb ElementCapabilities
	curCpb  atomic.Value // ElementCapabilities
}

const (
	elmStateRunning uint32 = iota
	elmStateClosing
	elmStateClosed
)

// ElementOptions configures an [Element].
// All fields are optional.
type ElementOptions struct {
	InstanceID ElementID
	// Capabilities is the initial capabilities of the element.
	// Empty by default.
	Capabilities ElementCapabilities
	// ServerLocator is used to locate remote servers for request routing.
	// If nil, a new [RemoteElementLocator] is created with [ElementOptions.DNSResolver]
	// and current transport metadata.
	ServerLocator RemoteServerLocator
	// DNSResolver is used only to create new [RemoteElementLocator] when [ElementOptions.ServerLocator] is nil.
	// If nil, [dns.DefaultResolver] is used.
	DNSResolver DNSResolver
	// URIConverter is used to convert URIs to SIP URIs.
	// If nil, [DefaultURIConverter] is used.
	URIConverter URIConverter
	// Logger is the logger used by the element.
	// If nil, the [log.Default] is used.
	Logger *slog.Logger
	// ServerTransactionFactory is the server transaction factory.
	// If nil, a [NewServerTransaction] is used.
	ServerTransactionFactory ServerTransactionFactory
	// ServerTransactionStore is the server transaction store.
	// If nil, a [NewMemoryServerTransactionStore] is used.
	ServerTransactionStore ServerTransactionStore
	// ClientTransactionFactory is the client transaction factory.
	// If nil, a [NewClientTransaction] is used.
	ClientTransactionFactory ClientTransactionFactory
	// ClientTransactionStore is the client transaction store.
	// If nil, a [NewMemoryClientTransactionStore] is used.
	ClientTransactionStore ClientTransactionStore
	// StaleTransactionTimeout is the timeout for stale transactions.
	// Client INVITE transaction in proceeding, server INVITE transaction in proceeding
	// and non-INVITE transaction in trying/proceeding states after this timeout
	// are considered stale and will be terminated to prevent memory leaks.
	// If 0, 5 minutes is used. If negative, stale transactions are never terminated.
	StaleTransactionTimeout time.Duration
}

func (o ElementOptions) instID() ElementID {
	if o.InstanceID.IsZero() {
		return NextElementID()
	}
	return o.InstanceID
}

func (o ElementOptions) dnsRslvr() DNSResolver {
	if o.DNSResolver == nil {
		return dns.DefaultResolver()
	}
	return o.DNSResolver
}

func (o ElementOptions) srvLctr(tpm *TransportManager) RemoteServerLocator {
	if o.ServerLocator != nil {
		return o.ServerLocator
	}
	return &RemoteElementLocator{o.dnsRslvr(), tpm}
}

func (o ElementOptions) uriConv() URIConverter {
	if o.URIConverter == nil {
		return defURIConverter
	}
	return o.URIConverter
}

func (o ElementOptions) log() *slog.Logger {
	if o.Logger == nil {
		return log.Default()
	}
	return o.Logger
}

// NewElement creates a new base SIP [Element].
func NewElement(opts ...ElementOptions) (*Element, error) {
	elmOpts := util.LastSliceElemOr(opts, ElementOptions{})

	elm := &Element{
		id:      elmOpts.instID(),
		baseCpb: elmOpts.Capabilities,
		uriConv: elmOpts.uriConv(),
	}
	elm.curCpb.Store(elm.baseCpb)
	elm.log = elmOpts.log().With(slog.Any("element", elm))
	elm.tpm = &TransportManager{Logger: elm.log}
	elm.txm = &TransactionManager{
		ServerTransactionFactory: elmOpts.ServerTransactionFactory,
		ServerTransactionStore:   elmOpts.ServerTransactionStore,
		ClientTransactionFactory: elmOpts.ClientTransactionFactory,
		ClientTransactionStore:   elmOpts.ClientTransactionStore,
		StaleTransactionTimeout:  elmOpts.StaleTransactionTimeout,
		Logger:                   elm.log,
	}
	elm.srvLctr = elmOpts.srvLctr(elm.tpm)

	elm.tpm.UseMessageInterceptor(elm.txm)
	elm.tpm.UseMessageInterceptor(elm)

	return elm, nil
}

func (elm *Element) ID() ElementID { return elm.id }

func (elm *Element) Logger() *slog.Logger { return elm.log }

func (elm *Element) LogValue() slog.Value {
	if elm == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.String("ptr", fmt.Sprintf("%p", elm)),
		slog.Any("id", elm.id),
	)
}

func (*Element) InterceptOutboundRequest(
	ctx context.Context,
	next RequestSender,
	req *RequestEnvelope,
	opts ...SendRequestOptions,
) error {
	req.WithMessage(func(r *Request) {
		EnsureRequestVia(r, req.Transport().Proto, Addr{})
		EnsureRequestFromTag(r)
		EnsureRequestMaxForwards(r)
		EnsureMessageContentLength(r)
	})

	if tp := req.Transport(); !tp.Reliable() && tp.MTU > 0 {
		sendOpts := util.LastSliceElemOr(opts, SendRequestOptions{})
		isLarge := uint(len(req.Render(sendOpts.RenderOptions))) > tp.MTU-200

		if isLarge && !sendOpts.RenderOptions.Compact {
			sendOpts.RenderOptions.Compact = true
			isLarge = uint(len(req.Render(sendOpts.RenderOptions))) > tp.MTU-200
			if !isLarge {
				opts = append(opts, sendOpts)
			}
		}

		if isLarge && !req.Metadata().Has(forceUnrelMetaKey) {
			return errors.Wrap(ErrMessageTooLarge)
		}
	}

	return errors.Wrap(next.SendRequest(ctx, req, opts...))
}

func (elm *Element) InterceptOutboundResponse(
	ctx context.Context,
	next ResponseSender,
	res *ResponseEnvelope,
	opts ...SendResponseOptions,
) error {
	res.WithMessage(func(r *Response) {
		EnsureResponseToTag(r, GenerateStableToTag(r, elm.id[:]))
		EnsureMessageContentLength(r)
	})

	return errors.Wrap(next.SendResponse(ctx, res, opts...))
}

func (*Element) InterceptInboundRequest(ctx context.Context, next RequestReceiver, req *RequestEnvelope) error {
	return errors.Wrap(next.RecvRequest(ctx, req))
}

func (*Element) InterceptInboundResponse(ctx context.Context, next ResponseReceiver, res *ResponseEnvelope) error {
	return errors.Wrap(next.RecvResponse(ctx, res))
}

// Close closes the element, transport manager and transaction manager.
func (elm *Element) Close(ctx context.Context) error {
	elm.closeOnce.Do(func() {
		elm.state.Store(elmStateClosing)
		elm.closeErr = elm.close(ctx)
		elm.state.Store(elmStateClosed)

		elm.log.LogAttrs(ctx, slog.LevelDebug, "element closed")
	})
	return errors.Wrap(elm.closeErr)
}

func (elm *Element) close(ctx context.Context) error {
	for mw := range elm.mws.All() {
		var closeErr error
		switch v := mw.(type) {
		case closer:
			closeErr = v.Close(ctx)
		case io.Closer:
			closeErr = v.Close()
		}
		if closeErr != nil {
			elm.log.LogAttrs(ctx, slog.LevelWarn, "failed to close middleware",
				slog.Any("error", closeErr),
				slog.Any("middleware", mw),
			)
		}
	}

	if txs, err := elm.txm.AllClientTransactions(ctx); err == nil {
		for tx := range txs {
			if tx.Type() == TransactionTypeClientInvite && tx.State() == TransactionStateProceeding {
				if cnc, err := NewCancelRequestEnvelope(tx.Request()); err == nil {
					if err = elm.SendRequest(ctx, cnc); err != nil {
						elm.log.LogAttrs(ctx, slog.LevelWarn, "failed to cancel transaction",
							slog.Any("error", err),
							slog.Any("transaction", tx),
						)
					}
				}
			}
		}
	}

	return errors.JoinPrefix("element close errors:", elm.txm.Close(ctx), elm.tpm.Close(ctx))
}

func (elm *Element) TransportManager() *TransportManager { return elm.tpm }

func (elm *Element) TrackTransport(tp Transport) error {
	if elm.state.Load() >= elmStateClosing {
		return errors.Wrap(ErrElementClosed)
	}
	return errors.Wrap(elm.tpm.TrackTransport(tp))
}

func (elm *Element) UntrackTransport(tp Transport) error {
	if elm.state.Load() >= elmStateClosing {
		return errors.Wrap(ErrElementClosed)
	}
	return errors.Wrap(elm.tpm.UntrackTransport(tp))
}

func (elm *Element) TransportByProto(proto TransportProto) (Transport, bool) {
	return elm.tpm.TransportByProto(proto)
}

func (elm *Element) AllTransports() iter.Seq[Transport] {
	return elm.tpm.AllTransports()
}

func (elm *Element) TransportMetadataByProto(proto TransportProto) (TransportMetadata, bool) {
	return elm.tpm.TransportMetadataByProto(proto)
}

func (elm *Element) TransportMetadataByNAPTRService(service string) (TransportMetadata, bool) {
	return elm.tpm.TransportMetadataByNAPTRService(service)
}

func (elm *Element) AllTransportMetadata() iter.Seq[TransportMetadata] {
	return elm.tpm.AllTransportMetadata()
}

func (elm *Element) TransportFromRequest(req *RequestEnvelope) (Transport, bool) {
	return elm.tpm.TransportFromRequest(req)
}

func (elm *Element) TransportFromResponse(res *ResponseEnvelope) (Transport, bool) {
	return elm.tpm.TransportFromResponse(res)
}

func (elm *Element) Listen(ctx context.Context, proto TransportProto, addr string) (TransportListener, error) {
	if elm.state.Load() >= elmStateClosing {
		return nil, errors.Wrap(ErrElementClosed)
	}
	return errors.Wrap2(elm.tpm.Listen(ctx, proto, addr))
}

func (elm *Element) MatchSentBy(sentBy Addr) bool {
	return elm.tpm.MatchSentBy(sentBy)
}

func (elm *Element) TransactionManager() *TransactionManager { return elm.txm }

func (elm *Element) NewClientTransaction(
	ctx context.Context,
	req *RequestEnvelope,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (ClientTransaction, error) {
	if elm.state.Load() >= elmStateClosing {
		return nil, errors.Wrap(ErrElementClosed)
	}

	txOpts := util.LastSliceElemOr(opts, ClientTransactionOptions{})
	if txOpts.Logger == nil {
		txOpts.Logger = elm.log
	} else {
		txOpts.Logger = txOpts.Logger.With(slog.Any("element", elm))
	}

	return errors.Wrap2(elm.txm.NewClientTransaction(ctx, req, tp, txOpts))
}

func (elm *Element) LoadClientTransaction(ctx context.Context, key ClientTransactionKey) (ClientTransaction, error) {
	return errors.Wrap2(elm.txm.LoadClientTransaction(ctx, key))
}

func (elm *Element) AllClientTransactions(ctx context.Context) (iter.Seq[ClientTransaction], error) {
	return errors.Wrap2(elm.txm.AllClientTransactions(ctx))
}

func (elm *Element) NewServerTransaction(
	ctx context.Context,
	req *RequestEnvelope,
	tp ServerTransport,
	opts ...ServerTransactionOptions,
) (ServerTransaction, error) {
	if elm.state.Load() >= elmStateClosing {
		return nil, errors.Wrap(ErrElementClosed)
	}

	txOpts := util.LastSliceElemOr(opts, ServerTransactionOptions{})
	if txOpts.Logger == nil {
		txOpts.Logger = elm.log
	} else {
		txOpts.Logger = txOpts.Logger.With(slog.Any("element", elm))
	}

	return errors.Wrap2(elm.txm.NewServerTransaction(ctx, req, tp, txOpts))
}

func (elm *Element) LoadServerTransaction(ctx context.Context, key ServerTransactionKey) (ServerTransaction, error) {
	return errors.Wrap2(elm.txm.LoadServerTransaction(ctx, key))
}

func (elm *Element) AllServerTransactions(ctx context.Context) (iter.Seq[ServerTransaction], error) {
	return errors.Wrap2(elm.txm.AllServerTransactions(ctx))
}

// RequestAttempt binds one cloned request to one resolved next-hop candidate.
//
// Attempts are produced by [Element.ProduceRequestAttempts] and are executed
// by [RequestAttempt.DoStateless] or [RequestAttempt.DoStateful].
// The exported request and address let a higher level adjust an attempt before sending;
// the [Element] owns only the private state used to coordinate branch generation
// and size fallbacks.
type RequestAttempt struct {
	// Addr is the concrete transport and network destination for this attempt.
	Addr ResolvedAddr
	// Request is the request copy prepared for this attempt.
	Request *RequestEnvelope

	elm         *Element
	sharedState *reqAttemptsSharedState
	forceUnrel  bool
}

// IsRFC2543Fallback reports whether this attempt is the compatibility retry
// that deliberately sends an oversized request over an unreliable transport.
func (ra *RequestAttempt) IsRFC2543Fallback() bool { return ra.forceUnrel }

const forceUnrelMetaKey = "sip.force_unreliable"

// prepareReq applies the selected transport and destination to the attempt.
//
// A normal DNS/transaction retry receives a fresh branch.
// The only branch reuse is the local UDP-to-reliable size switch, which happens
// before the request has been successfully delivered and is still the same logical send.
func (ra *RequestAttempt) prepareReq(tpMeta TransportMetadata, reuseBranch bool) *RequestAttempt {
	if ra.forceUnrel && !tpMeta.Reliable() {
		ra.Request.Metadata().Set(forceUnrelMetaKey, true)
	} else {
		ra.Request.Metadata().Delete(forceUnrelMetaKey)
	}

	ra.Request.
		SetTransport(tpMeta).
		SetRemoteAddr(ra.Addr.Addr).
		WithMessage(func(r *Request) {
			// ensure that each attempt sends request with unique branch
			EnsureRequestVia(r, tpMeta.Proto, Addr{})
			via, _ := r.Headers.FirstVia()
			currBranch, _ := via.Branch()
			if !reuseBranch && currBranch == ra.sharedState.prevBranch {
				currBranch = GenerateBranch(0)
				via.Params.Set("branch", currBranch)
			}
			ra.sharedState.prevBranch = currBranch
		})

	return ra
}

// DoStateless sends this attempt without creating a SIP transaction.
//
// It may switch a direct, non-service-record UDP candidate to a matching reliable
// transport when the message is too large. Service-record candidates are deferred
// so the next RFC 3263 candidate is tried first.
func (ra *RequestAttempt) DoStateless(ctx context.Context, opts ...SendRequestOptions) error {
	tp, ok := ra.elm.TransportByProto(ra.Addr.Transport)
	if !ok {
		return errors.Wrap(ErrNoTransport)
	}

	sendOpts := util.LastSliceElemOr(opts, SendRequestOptions{})

	err := ra.prepareReq(tp.Metadata(), false).sendStateless(ctx, tp, sendOpts)
	if err != nil && !ra.forceUnrel && !tp.Metadata().Reliable() &&
		(errors.Is(err, ErrEntityTooLarge) || errors.Is(err, ErrMessageTooLarge)) {
		if ra.Addr.FromDNS {
			// defer to fallback, re-try will be on next reliable attempt
			ra.sharedState.unrelFallback = append(ra.sharedState.unrelFallback, ra.Addr)
		} else {
			// re-try with switch to reliable transport
			for newTp := range ra.elm.AllTransports() {
				if newTp.Metadata().Reliable() && newTp.Metadata().Secured() == tp.Metadata().Secured() {
					newAddr := ra.Addr
					newAddr.Transport = newTp.Metadata().Proto

					err = ra.prepareReq(newTp.Metadata(), true).sendStateless(ctx, newTp, sendOpts)
					if err == nil {
						ra.Addr = newAddr
					} else if IsTransportError(err) {
						ra.sharedState.unrelFallback = append(ra.sharedState.unrelFallback, ra.Addr)
					}
					break
				}
			}
		}
	}

	return errors.Wrap(err)
}

func (ra *RequestAttempt) sendStateless(ctx context.Context, tp ClientTransport, opts SendRequestOptions) error {
	return errors.Wrap(tp.SendRequest(ctx, ra.Request, opts))
}

// DoStateful creates and starts a client transaction for this attempt.
//
// A successful return means the initial request was accepted by the selected
// transport; response policy and the remainder of the transaction lifecycle
// belong to the caller of the sending API.
func (ra *RequestAttempt) DoStateful(ctx context.Context, opts ...ClientTransactionOptions) (ClientTransaction, error) {
	tp, ok := ra.elm.TransportByProto(ra.Addr.Transport)
	if !ok {
		return nil, errors.Wrap(ErrNoTransport)
	}

	txOpts := util.LastSliceElemOr(opts, ClientTransactionOptions{})

	tx, err := ra.prepareReq(tp.Metadata(), false).sendStateful(ctx, tp, txOpts)
	if err != nil && !ra.forceUnrel && !tp.Metadata().Reliable() &&
		(errors.Is(err, ErrEntityTooLarge) || errors.Is(err, ErrMessageTooLarge)) {
		if ra.Addr.FromDNS {
			// defer to fallback, re-try will be on next reliable attempt
			ra.sharedState.unrelFallback = append(ra.sharedState.unrelFallback, ra.Addr)
		} else {
			// re-try with switch to reliable transport
			for newTp := range ra.elm.AllTransports() {
				if newTp.Metadata().Reliable() && newTp.Metadata().Secured() == tp.Metadata().Secured() {
					newAddr := ra.Addr
					newAddr.Transport = newTp.Metadata().Proto

					tx, err = ra.prepareReq(newTp.Metadata(), true).sendStateful(ctx, newTp, txOpts)
					if err == nil {
						ra.Addr = newAddr
					} else if IsTransportError(err) {
						ra.sharedState.unrelFallback = append(ra.sharedState.unrelFallback, ra.Addr)
					}
					break
				}
			}
		}
	}

	return tx, errors.Wrap(err)
}

func (ra *RequestAttempt) sendStateful(ctx context.Context, tp Transport, opts ClientTransactionOptions) (ClientTransaction, error) {
	tx, err := ra.elm.NewClientTransaction(ctx, ra.Request.Clone().(*RequestEnvelope), tp, opts) //nolint:forcetypeassert
	if err != nil {
		return nil, errors.Wrap(err)
	}

	ra.Request.UpdateFrom(tx.Request())
	return tx, nil
}

// reqAttemptsSharedState is shared by the lazy attempt sequence.
//
// A primary attempt can append an unreliable size fallback while it is being executed;
// the fallback is emitted only after all primary candidates have been tried.
type reqAttemptsSharedState struct {
	unrelFallback []ResolvedAddr
	prevBranch    string
}

// RequestAttemptError describes a failed attempt together with the request and
// transaction state available to the caller.
type RequestAttemptError struct {
	// Cause is the transport, transaction, timeout, or response-classification error.
	Cause error
	// Addr is the candidate used by the failed attempt.
	Addr ResolvedAddr
	// Request is the exact request copy used by the attempt.
	Request *RequestEnvelope
	// Transaction is the client transaction, when one was started.
	Transaction ClientTransaction
	// CancelTransaction is the transaction created while cancelling the request.
	CancelTransaction ClientTransaction
	// Terminal prevents the request runner from trying another candidate.
	Terminal bool
}

func (e *RequestAttemptError) Error() string {
	if e == nil || e.Cause == nil {
		return sNilTag
	}
	return fmt.Sprintf("send request to %q: %v", e.Addr, e.Cause)
}

func (e *RequestAttemptError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

const lookupTargetMetaKey = "sip.lookup_target"

// ResolveRequestTarget resolves the next-hop target following RFC 3261
// Section 8.1.2.
//
// If the request has no pre-resolved remote address, it applies strict/loose
// Route processing before DNS resolution and may mutate the request to perform
// the RFC 2543 strict-router transformation.
//
// A valid remote address is an explicit contract that the caller has already
// performed all required route-set transformations and selected the next hop.
// The resolved target is cached in request metadata because all attempts for
// one send operation use the same routing decision.
func (elm *Element) ResolveRequestTarget(ctx context.Context, req *RequestEnvelope) (*URI, error) {
	if v, ok := req.Metadata().Get(lookupTargetMetaKey); ok {
		if trgt, ok := v.(*URI); ok {
			return trgt, nil
		}
	}

	if addr := req.RemoteAddr(); addr.IsValid() {
		trgt := &URI{
			Addr: AddrFromIPPort(addr.Addr().AsSlice(), addr.Port()),
		}
		if tp := req.Transport(); tp.IsValid() {
			trgt.Params = make(Values).Set("transport", string(tp.Proto))
		}
		req.WithMessage(func(r *Request) {
			if u, ok := r.URI.(*URI); ok {
				trgt.Secured = u.Secured
			}
		})
		req.Metadata().Set(lookupTargetMetaKey, trgt)
		return trgt, nil
	}

	var (
		trgt *URI
		err  error
	)
	req.WithMessage(func(r *Request) {
		var secured bool

		reqURI := r.URI
		if u, ok := reqURI.(*URI); ok {
			trgt = u.Clone().(*URI) //nolint:forcetypeassert
			secured = u.Secured
		}

		route, ok := r.Headers.FirstRoute()
		if !ok || !route.IsValid() {
			if trgt == nil {
				trgt, err = elm.uriConv.ConvertURI(ctx, reqURI.Clone())
				if err != nil {
					err = errors.Wrap(err)
					return
				}
			}
			return
		}

		var routeURI *URI
		routeURI, err = elm.uriConv.ConvertURI(ctx, route.URI)
		if err != nil {
			err = errors.Wrap(err)
			return
		}

		trgt = routeURI.Clone().(*URI) //nolint:forcetypeassert
		if secured {
			trgt.Secured = secured
		}

		if !routeURI.LR() {
			r.Headers.
				AppendRoute(header.RouteHop{URI: reqURI.Clone()}).
				PopFirstRoute()
			r.URI = cleanReqURI(routeURI)
		}
	})
	if err != nil {
		return nil, errors.Wrap(err)
	}

	req.Metadata().Set(lookupTargetMetaKey, trgt)
	return trgt, nil
}

// ProduceRequestAttempts lazily produces ordered request attempts for the
// resolved next-hop candidates.
//
// Each attempt owns a clone of req, so routing, transport metadata, and branch
// changes do not leak between candidates.
//
// The sequence first emits DNS candidates.
// If an executed attempt discovers a size-related unreliable-transport fallback,
// that candidate is appended only after all primary candidates have been consumed.
// Consequently callers should execute attempts as they iterate them rather than
// collect the sequence before sending.
func (elm *Element) ProduceRequestAttempts(
	ctx context.Context,
	req *RequestEnvelope,
	opts ...LookupMessageAddrsOptions,
) (iter.Seq[*RequestAttempt], error) {
	trgt, err := elm.ResolveRequestTarget(ctx, req)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	// Produce attempts based on DNS lookup procedure.
	// This is the common path for most outbound requests.
	return func(yield func(*RequestAttempt) bool) {
		lookupOpts := util.LastSliceElemOr(opts, LookupMessageAddrsOptions{})
		sharedState := &reqAttemptsSharedState{}

		for addr := range elm.srvLctr.LookupRequestAddrs(ctx, trgt, lookupOpts) {
			req := req.Clone().(*RequestEnvelope) //nolint:forcetypeassert
			req.SetRemoteAddr(addr.Addr)
			if tp, ok := elm.TransportMetadataByProto(addr.Transport); ok {
				req.SetTransport(tp)
			}

			if !yield(&RequestAttempt{
				Request:     req,
				Addr:        addr,
				elm:         elm,
				sharedState: sharedState,
			}) {
				return
			}
		}

		for _, addr := range sharedState.unrelFallback {
			req := req.Clone().(*RequestEnvelope) //nolint:forcetypeassert
			req.SetRemoteAddr(addr.Addr)
			if tp, ok := elm.TransportMetadataByProto(addr.Transport); ok {
				req.SetTransport(tp)
			}

			if !yield(&RequestAttempt{
				Request:     req,
				Addr:        addr,
				elm:         elm,
				sharedState: sharedState,
				forceUnrel:  true,
			}) {
				return
			}
		}
	}, nil
}

// runReqAttempts is the common execution loop for stateless and stateful
// request sending.
//
// It owns candidate iteration and retry classification, but delegates the actual
// send and attempt-specific policy to its callbacks.
func (elm *Element) runReqAttempts(
	ctx context.Context,
	req *RequestEnvelope,
	lookupOpts LookupMessageAddrsOptions,
	beforeSend func(context.Context, *RequestAttempt) bool,
	send func(context.Context, *RequestAttempt) error,
	onError func(context.Context, *RequestAttempt, error) bool,
	useFallback bool,
) error {
	attempts, err := elm.ProduceRequestAttempts(ctx, req, lookupOpts)
	if err != nil {
		return errors.Wrap(err)
	}

	var errs []error
	for attempt := range attempts {
		if !useFallback && attempt.forceUnrel {
			continue
		}

		if beforeSend != nil && !beforeSend(ctx, attempt) {
			continue
		}

		err := send(ctx, attempt)
		if err == nil {
			return nil
		}

		errs = append(errs, err)

		if errors.Is(err, ctx.Err()) ||
			errors.Is(err, ErrElementClosed) ||
			errors.Is(err, ErrTransportManagerClosed) ||
			errors.Is(err, ErrTransactionManagerClosed) ||
			(IsMessageError(err) &&
				!errors.Is(err, ErrEntityTooLarge) &&
				!errors.Is(err, ErrMessageTooLarge)) {
			break
		}

		if err, ok := errors.AsType[*RequestAttemptError](err); ok && err.Terminal {
			break
		}

		if onError != nil && !onError(ctx, attempt, errors.Wrap(err)) {
			break
		}
	}

	if len(errs) == 0 {
		return errors.Wrap(ErrNoAddress)
	}
	return errors.JoinPrefixWrap("send request errors:", errs...)
}

// SendRequest implements the [RequestSender] contract for [Element].
//
// It resolves the request target but sends only through the first candidate;
// callers that need DNS candidate failover should use [Element.SendRequestStateless].
// The request is updated with the successfully prepared attempt before returning.
func (elm *Element) SendRequest(ctx context.Context, req *RequestEnvelope, opts ...SendRequestOptions) error {
	if elm.state.Load() >= elmStateClosed {
		return errors.Wrap(ErrElementClosed)
	}

	sendOpts := util.LastSliceElemOr(opts, SendRequestOptions{})
	sendOpts.LookupOptions.StableDNSRecordsOrder = true

	attempts, err := elm.ProduceRequestAttempts(ctx, req, sendOpts.LookupOptions)
	if err != nil {
		return errors.Wrap(err)
	}

	attempt, ok := util.SeqFirst(attempts)
	if !ok {
		return errors.Wrap(ErrNoAddress)
	}

	if err := attempt.DoStateless(ctx, sendOpts); err != nil {
		return errors.Wrap(err)
	}

	req.UpdateFrom(attempt.Request)

	return nil
}

type SendRequestStatelessOptions struct {
	// SendOptions are applied to every candidate attempt.
	SendOptions SendRequestOptions
	// BeforeAttempt can modify or reject a candidate before it is sent.
	// A false result skips that candidate without treating it as a failure.
	BeforeAttempt func(context.Context, *RequestAttempt) bool
	// OnAttemptError observes a failed candidate. Returning false stops the
	// DNS failover loop; nil means that retry classification remains automatic.
	OnAttemptError func(context.Context, *RequestAttempt, error) bool
	// UseRFC2543Fallback permits retrying an oversized request over an
	// unreliable transport after reliable candidates have been exhausted.
	UseRFC2543Fallback bool
}

// SendRequestStateless sends a request without a transaction while trying the
// ordered DNS candidates.
//
// Stateless sending can retry local transport/write failures, but it cannot detect
// SIP responses or transaction timeouts; those require [Element.SendRequestStateful]
// and a [ClientTransaction].
func (elm *Element) SendRequestStateless(
	ctx context.Context,
	req *RequestEnvelope,
	opts ...SendRequestStatelessOptions,
) error {
	if elm.state.Load() >= elmStateClosed {
		return errors.Wrap(ErrElementClosed)
	}

	sendOpts := util.LastSliceElemOr(opts, SendRequestStatelessOptions{})
	sendOpts.SendOptions.LookupOptions.StableDNSRecordsOrder = true

	var successAttempt *RequestAttempt
	err := elm.runReqAttempts(ctx, req,
		sendOpts.SendOptions.LookupOptions,
		sendOpts.BeforeAttempt,
		func(ctx context.Context, attempt *RequestAttempt) error {
			if err := attempt.DoStateless(ctx, sendOpts.SendOptions); err != nil {
				return errors.Wrap(&RequestAttemptError{
					Cause:   err,
					Addr:    attempt.Addr,
					Request: attempt.Request.Clone().(*RequestEnvelope), //nolint:forcetypeassert
				})
			}

			successAttempt = attempt

			return nil
		},
		sendOpts.OnAttemptError,
		sendOpts.UseRFC2543Fallback,
	)
	if err != nil {
		return errors.Wrap(err)
	}

	req.UpdateFrom(successAttempt.Request)

	return nil
}

// SendResponse sends a SIP response statelessly using the element's transport manager.
func (elm *Element) SendResponse(ctx context.Context, res *ResponseEnvelope, opts ...SendResponseOptions) error {
	if elm.state.Load() >= elmStateClosed {
		return errors.Wrap(ErrElementClosed)
	}

	sendOpts := util.LastSliceElemOr(opts, SendResponseOptions{})
	sendOpts.LookupOptions.StableDNSRecordsOrder = true

	return errors.Wrap(elm.tpm.SendResponse(ctx, res, sendOpts))
}

// Respond sends a SIP response statelessly using the element's transport manager.
func (elm *Element) Respond(ctx context.Context, req *RequestEnvelope, sts ResponseStatus, opts ...RespondOptions) error {
	if elm.state.Load() >= elmStateClosed {
		return errors.Wrap(ErrElementClosed)
	}

	resOpts := util.LastSliceElemOr(opts, RespondOptions{})
	if resOpts.ResponseOptions.LocalTag == "" {
		req.WithMessage(func(r *Request) {
			resOpts.ResponseOptions.LocalTag = GenerateStableToTag(r, elm.id[:])
		})
	}

	return errors.Wrap(Respond(ctx, req, sts, elm, resOpts))
}

type SendRequestStatefulOptions struct {
	// SendOptions are applied to every transaction attempt.
	SendOptions SendRequestOptions
	// Timing configures the client transaction.
	// If zero, [DefaultTimings] is used.
	Timing TimingConfig
	// Logger configures the client transaction logger.
	// If nil, the [Element] logger is inherited.
	Logger *slog.Logger
	// BeforeAttempt can modify or reject a candidate before its transaction is created.
	// A false result skips that candidate.
	BeforeAttempt func(context.Context, *RequestAttempt) bool
	// OnAttemptProgress observes provisional 101-199 responses while [Element]
	// confirms the current direction.
	// The callback is informational; it does not transfer transaction ownership
	// before confirmation completes.
	OnAttemptProgress func(context.Context, *RequestAttempt, *ResponseEnvelope)
	// OnAttemptError observes a failed candidate.
	// Returning false stops DNS failover; nil leaves the built-in RFC 3263
	// classification in control.
	OnAttemptError func(context.Context, *RequestAttempt, error) bool
	// UseRFC2543Fallback permits retrying an oversized request over an
	// unreliable transport after reliable candidates have been exhausted.
	UseRFC2543Fallback bool
}

// SendRequestStateful resolves and tries request candidates using client transactions.
//
// [Element] keeps an attempt alive until the current direction is confirmed
// according to RFC 3263 policy: a successful final transaction state
// ends confirmation, while 503, transport failure, or a timeout before any
// response may advance to the next candidate.
// Once this method returns, the caller owns the transaction's application-level
// response policy.
func (elm *Element) SendRequestStateful(
	ctx context.Context,
	req *RequestEnvelope,
	opts ...SendRequestStatefulOptions,
) (ClientTransaction, error) {
	if elm.state.Load() >= elmStateClosing {
		return nil, errors.Wrap(ErrElementClosed)
	}

	sendOpts := util.LastSliceElemOr(opts, SendRequestStatefulOptions{})

	var successTx ClientTransaction
	err := elm.runReqAttempts(ctx, req, sendOpts.SendOptions.LookupOptions,
		sendOpts.BeforeAttempt,
		func(ctx context.Context, attempt *RequestAttempt) error {
			tx, err := attempt.DoStateful(ctx, ClientTransactionOptions{
				SendOptions: sendOpts.SendOptions,
				Timing:      sendOpts.Timing,
				Logger:      sendOpts.Logger,
			})
			if err != nil {
				return errors.Wrap(&RequestAttemptError{
					Cause:   err,
					Addr:    attempt.Addr,
					Request: attempt.Request.Clone().(*RequestEnvelope), //nolint:forcetypeassert
				})
			}

			if err := elm.confirmClientTx(ctx, attempt, tx, sendOpts.OnAttemptProgress); err != nil {
				return errors.Wrap(err)
			}

			successTx = tx

			return nil
		},
		sendOpts.OnAttemptError,
		sendOpts.UseRFC2543Fallback,
	)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return successTx, nil
}

// confirmClientTx decides whether the current candidate is usable before the
// request runner commits to it.
//
// The method intentionally waits for the transaction's accepted/completed state
// instead of returning on a provisional response: RFC 3263 treats a 503 as an
// attempt failure, and the caller must give the current server a chance to
// produce that final result before trying another DNS candidate.
//
// The confirmation handler is removed before returning.
// The transaction itself remains alive and continues its normal SIP FSM; only
// application-specific response handling is left to the caller.
func (elm *Element) confirmClientTx(
	ctx context.Context,
	attempt *RequestAttempt,
	tx ClientTransaction,
	onProgress func(context.Context, *RequestAttempt, *ResponseEnvelope),
) error {
	unbindResHndlr := tx.BindResponseHandler(InboundResponseHandlerFunc(
		func(ctx context.Context, res *ResponseEnvelope) {
			if onProgress != nil {
				if sts := res.Status(); ResponseStatusTrying < sts && sts < types.ResponseStatusOK {
					onProgress(ctx, attempt, res)
				}
			}
		},
	))
	defer unbindResHndlr()

	// RFC 3263 Section 4.3: keep the attempt until a final transaction
	// outcome or a transport/timeout error is available for classification.
	// A provisional response is reported through OnAttemptProgress but does not
	// complete confirmation because a later 503 still fails this attempt.
	// firstOf must be fully assigned before binding any handler,
	// since tx.Bind*Handler may invoke the handler synchronously
	// (e.g. to deliver an already pending error or state transition).
	firstOf := syncutil.NewFirstOf[error]()
	firstOf.
		AddCancel(tx.BindErrorHandler(ErrorHandlerFunc(
			func(_ context.Context, err error) {
				unbindResHndlr()
				firstOf.Resolve(errors.Wrap(err))
			},
		))).
		AddCancel(tx.BindStateHandler(TransactionStateHandlerFunc(
			func(_ context.Context, _, to TransactionState) {
				//nolint:exhaustive
				switch to {
				case TransactionStateAccepted, TransactionStateCompleted:
					unbindResHndlr()
					firstOf.Resolve(nil)
				}
			},
		)))

	select {
	case err := <-firstOf.Chan():
		if err != nil {
			return errors.Wrap(&RequestAttemptError{
				Cause:       err,
				Addr:        attempt.Addr,
				Request:     tx.Request(),
				Transaction: tx,
				Terminal: !errors.Is(err, ErrTransactionTimedOut) && !IsTransportError(err) ||
					errors.Is(err, ErrTransactionTimedOut) && tx.State() >= TransactionStateProceeding,
			})
		}
	case <-ctx.Done():
		unbindResHndlr()
		firstOf.Resolve(errors.Wrap(ctx.Err()))

		if err := <-firstOf.Chan(); err != nil {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()

			cncTx, cncErr := elm.txm.CancelClientTransaction(ctx, tx)
			if cncErr != nil && !errors.Is(err, ErrActionNotAllowed) && !errors.Is(err, ErrTransactionManagerClosed) {
				elm.log.LogAttrs(ctx, slog.LevelWarn, "failed to cancel transaction",
					slog.Any("transaction", tx),
					slog.Any("error", err),
				)
			}

			return errors.Wrap(&RequestAttemptError{
				Cause:             err,
				Addr:              attempt.Addr,
				Request:           tx.Request(),
				Transaction:       tx,
				CancelTransaction: cncTx,
				Terminal:          true,
			})
		}
	}

	switch res := tx.LastResponse(); res.Status() {
	case ResponseStatusServiceUnavailable:
		return errors.Wrap(&RequestAttemptError{
			Cause:       errServiceUnavail,
			Addr:        attempt.Addr,
			Request:     tx.Request(),
			Transaction: tx,
		})
	default:
		return nil
	}
}

type SendResponseStatefulOptions struct {
	// SendOptions are options for sending the response.
	SendOptions SendResponseOptions
	// Timing is the SIP timing config that will be used with the transaction.
	// If zero, [DefaultTimings] will be used.
	Timing TimingConfig
	// Logger is the logger that will be used with the transaction.
	// If nil, the [log.Default] will be used.
	Logger *slog.Logger
}

// SendResponseStateful sends a SIP response statefully using the element's
// transport manager and transaction manager.
func (elm *Element) SendResponseStateful(
	ctx context.Context,
	req *RequestEnvelope,
	res *ResponseEnvelope,
	opts ...SendResponseStatefulOptions,
) (ServerTransaction, error) {
	if elm.state.Load() >= elmStateClosing {
		return nil, errors.Wrap(ErrElementClosed)
	}

	txKey, err := ServerTransactionKeyFromMessage(req)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	sendOpts := util.LastSliceElemOr(opts, SendResponseStatefulOptions{})

	tx, err := elm.LoadServerTransaction(ctx, txKey)
	if err != nil {
		if !errors.Is(err, ErrTransactionNotFound) {
			return nil, errors.Wrap(err)
		}

		tp, ok := elm.TransportFromRequest(req)
		if !ok {
			return nil, errors.Wrap(ErrNoTransport)
		}

		tx, err = elm.NewServerTransaction(ctx, req, tp, ServerTransactionOptions{
			Timing: sendOpts.Timing,
			Logger: sendOpts.Logger,
		})
		if err != nil {
			return nil, errors.Wrap(err)
		}
	}

	if err := tx.SendResponse(ctx, res, sendOpts.SendOptions); err != nil {
		if err := tx.Terminate(ctx, errors.Wrap(err)); err != nil {
			elm.log.LogAttrs(ctx, slog.LevelWarn, "failed to terminate transaction",
				slog.Any("transaction", tx),
				slog.Any("error", err),
			)
		}
		return nil, errors.Wrap(err)
	}

	return tx, nil
}

type RespondStatefulOptions struct {
	ResponseOptions ResponseOptions
	SendOptions     SendResponseStatefulOptions
}

// RespondStateful sends a SIP response statefully using the element's
// transport manager and transaction manager.
func (elm *Element) RespondStateful(
	ctx context.Context,
	req *RequestEnvelope,
	sts ResponseStatus,
	opts ...RespondStatefulOptions,
) (ServerTransaction, error) {
	if elm.state.Load() >= elmStateClosing {
		return nil, errors.Wrap(ErrElementClosed)
	}

	resOpts := util.LastSliceElemOr(opts, RespondStatefulOptions{})

	res, err := req.NewResponse(sts, resOpts.ResponseOptions)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return errors.Wrap2(elm.SendResponseStateful(ctx, req, res, resOpts.SendOptions))
}
