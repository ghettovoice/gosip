package useragent

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

// RequestHandler handles an inbound request claimed by the user agent. The
// processing element and the responder controls arrive through the
// [IncomingMessage]; a non-nil error reports a handler failure back to the
// dispatching element.
type RequestHandler func(context.Context, *IncomingMessage) error

// IncomingMessage carries a claimed inbound request to its
// [RequestHandler] and owns the responder lifecycle for it: the handler
// may answer through [IncomingMessage.Respond] or retain the handle and
// respond after it returns, until a final response commits the handle,
// the transaction terminates, or the owning user agent shuts down.
type IncomingMessage struct {
	// ua is the owning user agent and elm the element that received the
	// request; both are resolved per claim by the inbound dispatch.
	ua  *UserAgent
	elm *sip.Element
	req *sip.RequestEnvelope

	// ctx is the inbound scope handed to the handler; it is canceled on
	// user-agent shutdown, pre-final transaction termination, and handle
	// release.
	ctx    context.Context //nolint:containedctx // the inbound scope is owned by the handle for its retained-responder lifetime
	cancel context.CancelFunc

	// responseMu serializes every Respond call so provisional and final
	// responses can never interleave; final commits only under it, after
	// a successful send.
	responseMu sync.Mutex
	final      bool

	// sendErr records the transaction error observed while a response
	// send is in flight: transaction FSMs report transport failures only
	// through their error pipeline, never through the send result.
	sendErr atomic.Pointer[error]

	// unbind drops the transaction-termination observation bound by the
	// dispatch. released and unbind are protected by ua.mu: release marks
	// the handle and consumes the bound unbind under the same lock, so a
	// termination replay — or a shutdown — landing before the dispatch
	// publishes the unbind can never be lost; setUnbind instead runs a
	// late unbind immediately on a released handle.
	released    bool
	unbind      func()
	releaseOnce sync.Once
}

// Request returns an immutable snapshot of the inbound request: handlers
// never mutate the envelope still owned by the dispatching element.
func (in *IncomingMessage) Request() *sip.RequestEnvelope {
	return in.req.Clone().(*sip.RequestEnvelope) //nolint:forcetypeassert // Clone preserves the concrete type
}

// release drops the inbound registration exactly once. It unregisters
// the handle, unbinds termination observation, and cancels the inbound
// context — including on a successful final send, which is why the final
// flag outranks the canceled scope while the agent still runs.
func (in *IncomingMessage) release() {
	in.releaseOnce.Do(func() {
		ua := in.ua
		ua.mu.Lock()
		in.released = true
		unbind := in.unbind
		in.unbind = nil
		ua.mu.Unlock()

		in.cancel()
		// unbind runs outside the lock: it must never run under ua.mu.
		if unbind != nil {
			unbind()
		}

		// The registration stays in the map through cleanup so a
		// concurrent seal snapshot still sees the handle and serializes
		// on this Once; only then is the entry removed.
		ua.mu.Lock()
		delete(ua.inbounds, in)
		ua.mu.Unlock()
	})
}

// setUnbind publishes the dispatch's transaction-observation unbind. A
// bind can synchronously replay the Terminated state — or a shutdown can
// release the handle — before this call lands; on a released handle the
// late unbind runs immediately instead of being dropped.
func (in *IncomingMessage) setUnbind(unbind func()) {
	in.ua.mu.Lock()
	if !in.released {
		in.unbind = unbind
		in.ua.mu.Unlock()
		return
	}
	in.ua.mu.Unlock()
	unbind()
}

// Respond sends a SIP response to the inbound request through the server
// transaction the dispatch registered for it.
//
// A provisional (1xx) response does not commit the responder, so a final
// response may still follow. A final (2xx–6xx) response commits the
// responder once its send succeeded: further calls fail with
// [ErrResponseFinal] while the owning user agent runs, and a failed send
// leaves the gate open. A sealed user agent reports [ErrClosed], which
// outranks even a committed final. A 2xx response to a MESSAGE request
// must not carry a body or a Contact header; such a call fails with
// [ErrInvalidRequest] before anything is sent.
func (in *IncomingMessage) Respond(
	ctx context.Context,
	status sip.ResponseStatus,
	opts ...sip.ResponseOptions,
) error {
	// The agent's seal outranks a committed final: a retained responder
	// reports closed after shutdown even when its earlier final already
	// released the handle. Never hold ua.mu when acquiring responseMu:
	// a final response releases the handle while holding responseMu.
	if in.ua.isClosed() {
		return errors.Wrap(NewClosedError())
	}

	in.responseMu.Lock()
	defer in.responseMu.Unlock()

	// The final flag outranks the inbound scope check: the final release
	// cancels in.ctx, so a second final must report ErrResponseFinal,
	// not ErrClosed, while the agent still runs.
	if in.final {
		return errors.Wrap(NewResponseFinalError())
	}
	if in.ctx.Err() != nil {
		return errors.Wrap(NewClosedError())
	}
	if !status.IsValid() {
		return errors.Wrap(NewInvalidRequestError("invalid response status %d", status))
	}

	resOpts := util.LastSliceElem(opts, sip.ResponseOptions{})

	// RFC 3428 Section 7: a successful response to a MESSAGE request
	// carries no message body and no Contact header.
	if in.req.Method().Equal(sip.RequestMethodMessage) && status.IsSuccessful() {
		if len(resOpts.Body) > 0 {
			return errors.Wrap(NewInvalidRequestError("MESSAGE 2xx response must not carry a body"))
		}
		if len(resOpts.Headers.Get("Contact")) > 0 {
			return errors.Wrap(NewInvalidRequestError("MESSAGE 2xx response must not carry Contact header"))
		}
	}

	// RespondStateful reuses the transaction the dispatch registered for
	// this request, so the send runs through the transaction FSM and the
	// transaction replays the response for retransmissions. The FSM
	// reports a transport failure only through the transaction's error
	// pipeline, so observe it through the bound error handler.
	in.sendErr.Store(nil)
	_, err := in.elm.RespondStateful(
		ctx,
		in.req,
		status,
		sip.RespondStatefulOptions{ResponseOptions: resOpts},
	)
	if err != nil {
		return errors.Wrap(err)
	}
	if e := in.sendErr.Swap(nil); e != nil {
		return errors.Wrap(*e)
	}

	// A shutdown that landed during the send detached the send-error
	// observer through release, so a transport failure may already be
	// lost: report closed instead of committing success.
	if in.ua.isClosed() {
		return errors.Wrap(NewClosedError())
	}

	if status.IsFinal() {
		in.final = true
		in.release()
	}
	return nil
}

// claimTarget describes how a claimed inbound request is answered: the
// configured handler, or the automatic OPTIONS capabilities answer when
// no handler claims the method.
type claimTarget struct {
	handler  RequestHandler
	autoOpts bool
}

// InterceptInboundRequest intercepts an inbound request on the owning
// element's receiver chain.
//
// A sealed user agent — or one that never passed [New] — claims nothing,
// and an unclaimed request passes to the next interceptor unchanged. A
// claimed request needs the receiving element to dispatch against: when
// the context carries none the claim fails with [ErrMissingElement]
// before reaching next or the handler.
func (ua *UserAgent) InterceptInboundRequest(
	ctx context.Context,
	next sip.RequestReceiver,
	req *sip.RequestEnvelope,
) error {
	if ua.isClosed() || ua.opts.AOR == nil {
		return errors.Wrap(next.RecvRequest(ctx, req))
	}

	ct, claimed := ua.claim(req)
	if !claimed {
		return errors.Wrap(next.RecvRequest(ctx, req))
	}

	elm, ok := sip.ElementFromContext(ctx)
	if !ok {
		return errors.Wrap(NewMissingElementError())
	}
	return errors.Wrap(ua.dispatchInbound(ctx, elm, req, ct))
}

// claim reports whether the user agent claims req and, when it does, the
// target that answers it: the [Options.OnMessage] handler for MESSAGE,
// the [Options.OnOptions] handler for OPTIONS, or the auto answer for
// OPTIONS when it is enabled and no handler is registered. Every other
// method is left to the next interceptor.
func (ua *UserAgent) claim(req *sip.RequestEnvelope) (*claimTarget, bool) {
	switch {
	case ua.opts.OnMessage != nil && req.Method().Equal(sip.RequestMethodMessage):
		return &claimTarget{handler: ua.opts.OnMessage}, true
	case ua.opts.OnOptions != nil && req.Method().Equal(sip.RequestMethodOptions):
		return &claimTarget{handler: ua.opts.OnOptions}, true
	case !ua.opts.DisableOptionsAutoAnswer && req.Method().Equal(sip.RequestMethodOptions):
		return &claimTarget{autoOpts: true}, true
	}
	return nil, false
}

// dispatchInbound answers a claimed request through the resolved claim
// target: the configured handler, or the automatic OPTIONS capabilities
// answer. It registers a real server transaction for the request, admits
// the inbound handle into the lifecycle drain, and invokes the target
// with the transaction-bound inbound context before it returns —
// retransmissions are absorbed by the transaction match and never reach
// the target twice.
func (ua *UserAgent) dispatchInbound(
	ctx context.Context,
	elm *sip.Element,
	req *sip.RequestEnvelope,
	ct *claimTarget,
) error {
	tp, ok := elm.TransportFromRequest(req)
	if !ok {
		return errors.Wrap(NewInvalidRequestError("no transport for inbound request"))
	}

	// The server transaction is registered before the handler runs. A
	// duplicate registration means a retransmission slipped past the
	// manager's own match: it is absorbed here instead of redispatching
	// the handler.
	tx, err := elm.NewServerTransaction(ctx, req, tp, sip.ServerTransactionOptions{})
	switch {
	case err == nil:
	case errors.Is(err, sip.ErrTransactionDuplicate):
		return nil
	default:
		return errors.Wrap(err)
	}
	if err := tx.Start(sip.ContextWithTransaction(ctx, tx)); err != nil {
		return errors.Wrap(err)
	}

	in := &IncomingMessage{ua: ua, elm: elm, req: req}
	in.ctx, in.cancel = context.WithCancel(ctx)

	release, admitted := ua.trackInbound(in)
	if !admitted {
		in.cancel()
		return errors.Wrap(NewClosedError())
	}
	// The worker slot belongs to the handler invocation; a retained
	// handle never holds it past the handler's return.
	defer ua.workers.Done()

	// Transaction termination releases the handle when no final went
	// out; release is idempotent, so an already-final responder is a
	// no-op. Transaction errors are reported to the responder through
	// sendErr, so a failed send never commits the final gate. The handler
	// itself runs without any user-agent lock held.
	unbindState := tx.BindStateHandler(sip.TransactionStateHandlerFunc(
		func(_ context.Context, _, to sip.TransactionState) {
			if to == sip.TransactionStateTerminated {
				release()
			}
		},
	))
	unbindErr := tx.BindErrorHandler(sip.ErrorHandlerFunc(func(_ context.Context, err error) {
		in.sendErr.Store(&err)
	}))
	in.setUnbind(func() {
		unbindState()
		unbindErr()
	})

	// A transaction that reached Terminated between Start and these
	// binds fired its last transition before the state handler existed:
	// pending states are drained live once any handler is bound, so the
	// bound handler above never observes it. Check once after the
	// unbind is published and release the drain slot directly.
	if tx.State() == sip.TransactionStateTerminated {
		release()
	}

	if ct.autoOpts {
		return errors.Wrap(ua.answerOptions(ctx, in))
	}

	err = ct.handler(sip.ContextWithTransaction(in.ctx, tx), in) //nolint:contextcheck // the handler runs on the inbound-owned scope, not the caller ctx
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*sip.RequestRejectedError](err); !ok {
		// A generic handler failure becomes a 500 rejection so the same
		// single-answer path covers it.
		err = sip.NewRequestRejectedError(err, slog.LevelWarn, sip.ResponseStatusServerInternalError)
	}
	// The rejection propagates to the transaction manager's deferred
	// responder, which answers once through the server transaction this
	// dispatch registered — status, response options, and send options
	// included — and suppresses the transport's stateless answer. The
	// handler's error stays the rejection's cause, so the dispatching
	// element's chain still observes it.
	return errors.Wrap(err)
}

// answerOptions sends the automatic OPTIONS capabilities answer through
// the inbound handle's server transaction. The advertisement is the user
// agent's own configured capabilities — never the element's aggregated
// set — and only non-empty headers are set, so no bare Allow, Supported,
// or Accept header renders.
func (ua *UserAgent) answerOptions(ctx context.Context, in *IncomingMessage) error {
	caps := ua.Capabilities()
	hdrs := sip.Headers{}
	if h := caps.NewAllowHeader(); h != nil {
		hdrs.Set(h)
	}
	if h := caps.NewSupportedHeader(); h != nil {
		hdrs.Set(h)
	}
	if h := caps.NewAcceptHeader(); h != nil {
		hdrs.Set(h)
	}
	return errors.Wrap(in.Respond(ctx, sip.ResponseStatusOK, sip.ResponseOptions{Headers: hdrs}))
}
