package sip

import (
	"context"
	"fmt"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

// ProxyEventKind classifies a [ProxyEvent] observation record.
type ProxyEventKind int

const (
	// ProxyEventRequestHandled indicates an inbound request claimed by a
	// proxy route entered the forwarding pipeline.
	ProxyEventRequestHandled ProxyEventKind = iota
	// ProxyEventForwardAttemptFinished indicates a downstream forward
	// attempt of a proxied request produced its decision.
	ProxyEventForwardAttemptFinished
	// ProxyEventRequestForwarded indicates an outbound request copy was
	// emitted toward a target.
	ProxyEventRequestForwarded
	// ProxyEventResponseForwarded indicates a response was relayed toward
	// the prior hop.
	ProxyEventResponseForwarded
	// ProxyEventRequestCompleted indicates a proxied request reached its
	// committed outcome.
	ProxyEventRequestCompleted
)

func (k ProxyEventKind) String() string {
	switch k {
	case ProxyEventRequestHandled:
		return "request_handled"
	case ProxyEventForwardAttemptFinished:
		return "forward_attempt_finished"
	case ProxyEventRequestForwarded:
		return "request_forwarded"
	case ProxyEventResponseForwarded:
		return "response_forwarded"
	case ProxyEventRequestCompleted:
		return "request_completed"
	default:
		return fmt.Sprintf("unknown(%d)", int(k))
	}
}

// ProxyEvent is a detached observation record of one proxy forwarding
// boundary.
//
// An event carries only correlation and result data copied out of the
// pipeline at the boundary: it never shares a mutable envelope,
// transaction, or coordinator reference with the state that produced it.
// Correlation fields are populated only when the association is actually
// known — a standalone stateless response does not invent a request or
// route association — and fields irrelevant to the event kind keep their
// zero value. RequestID is correlation data for joining the events of one
// request, not a metric label.
type ProxyEvent struct {
	// Kind classifies the observed boundary.
	Kind ProxyEventKind
	// Time is the wall-clock time the event was produced.
	Time time.Time
	// RequestID is the correlation identifier of the owning proxy request,
	// empty when the event has no request association.
	RequestID string
	// Sequence is the emission order of the event within its request,
	// zero when the event has no request association.
	Sequence uint64
	// RouteName is the name of the claiming [ProxyRoute], empty when the
	// event has no route association.
	RouteName string
	// Mode is the forward mode of the claiming route.
	Mode ForwardMode
	// Method is the SIP request method the event relates to.
	Method RequestMethod
	// TargetID is the logical target the event relates to, empty when the
	// event has no target association.
	TargetID string
	// AttemptID is the attempt identifier the event relates to, empty when
	// the event has no attempt association.
	AttemptID string
	// StatusCode is the SIP status code of the related response, zero when
	// the event carries no response.
	StatusCode int
	// Outcome is the committed request outcome carried by completion
	// events.
	Outcome ProxyRequestOutcome
	// Error is the detached failure classification of the event, nil when
	// the event reports no failure.
	Error error
}

// ProxyEventHandler is a callback invoked synchronously when the proxy
// emits a [ProxyEvent].
//
// Handlers run on the emitting goroutine at the drain/effect boundary,
// outside the request coordinator, transaction, and registry locks, so
// they may inspect public proxy state and bind or unbind handlers
// reentrantly without deadlock. Each handler receives its own copy of the
// detached event value. Handlers must stay short: a blocking handler
// delays every later observer of the same dispatch, and no handler can
// veto the delivery to the others. A handler bound mid-dispatch first
// observes the next event, and unbinding does not wait on the in-flight
// dispatch.
type ProxyEventHandler interface {
	HandleProxyEvent(ctx context.Context, event ProxyEvent)
}

// ProxyEventHandlerFunc is a function adapter that implements
// [ProxyEventHandler].
type ProxyEventHandlerFunc func(ctx context.Context, event ProxyEvent)

func (f ProxyEventHandlerFunc) HandleProxyEvent(ctx context.Context, event ProxyEvent) {
	f(ctx, event)
}

// BindEventHandler binds the callback invoked on each [ProxyEvent] this
// proxy emits, see [ProxyEventHandler] for the dispatch contract.
//
// Binding observes future events only, nothing is replayed. The callback
// can be unbound by calling the returned function; unbind is idempotent,
// may be called reentrantly from inside the handler, and does not wait on
// an in-flight dispatch that already captured the handler. A nil handler
// or a bind made once the proxy lifecycle ended returns a noop unbind;
// [Proxy.Shutdown] and [Proxy.Close] detach every tracked subscription.
func (prx *Proxy) BindEventHandler(h ProxyEventHandler) (unbind func()) {
	if h == nil {
		return noop
	}
	return prx.trackSub(func() func() { return prx.onEvent.Add(h) })
}

// emitEvt is the drain/effect dispatch point of the observation
// pipeline: it delivers the detached event synchronously to every handler
// bound at dispatch start. It runs outside the coordinator, transaction,
// and registry locks — callers emit at the boundary where the producing
// effect already committed — and spawns no per-observer goroutine. No
// observer holds a routing veto.
func (prx *Proxy) emitEvt(ctx context.Context, event ProxyEvent) {
	for h := range prx.onEvent.All() {
		h.HandleProxyEvent(ctx, event)
	}
}

// errPrxObserved marks an error whose operation already emitted every
// observation event it will ever produce. The pipeline-exit observer
// skips its own handled/completed records for it so one operation never
// counts twice.
const errPrxObserved errors.Error = "proxy operation observed"

// prxOperation is the observation cursor of one lightweight operation — a
// stateless traversal or an early matched rejection — that owns no
// request coordinator. It mints the operation's correlation ID and its
// committed dispatch sequence: events of one operation share the ID and
// count 1, 2, 3... in emission order, while the ID itself is transient
// and retained only for the operation's own pending response send.
type prxOperation struct {
	id     string
	seq    uint64
	route  *ProxyRoute
	method RequestMethod
}

func newPrxOperation(route *ProxyRoute, method RequestMethod) *prxOperation {
	return &prxOperation{
		id:     util.RandString(24),
		route:  route,
		method: method,
	}
}

// emit stamps the operation's committed sequence and correlation on the
// detached event and dispatches it synchronously.
func (op *prxOperation) emit(ctx context.Context, prx *Proxy, ev ProxyEvent) {
	op.seq++
	ev.RequestID = op.id
	ev.Sequence = op.seq
	ev.Time = time.Now()
	ev.Method = op.method
	if op.route != nil {
		ev.RouteName = op.route.Name
		ev.Mode = op.route.ForwardMode
	}
	prx.emitEvt(ctx, ev)
}

// emitObs stamps the coordinator-owned correlation and the committed
// dispatch sequence on the detached event and dispatches it synchronously
// at the drain/effect boundary — outside the coordinator guard. The
// sequence is allocated under the coordinator guard so concurrent
// producers (the admission goroutine and the drain owner) keep one
// committed order. A restored coordinator continues the checkpointed
// sequence under the original request identity instead of minting a new
// one, so post-activation events keep their historical correlation.
func (rq *ProxyRequest) emitObs(ctx context.Context, ev ProxyEvent) {
	rq.mu.Lock()
	rq.evSeq++
	ev.Sequence = rq.evSeq
	ev.RequestID = rq.id
	rq.mu.Unlock()

	ev.Time = time.Now()
	ev.Method = rq.req.Method()
	if rq.route != nil {
		ev.RouteName = rq.route.Name
		ev.Mode = rq.route.ForwardMode
	}
	rq.prx.emitEvt(ctx, ev)
}

// nextAttemptID allocates a fresh attempt correlation ID for targetID:
// every physical send generation of one logical target gets its own ID,
// so a DNS retry or a size-switch replacement never aliases its
// predecessor in an observation.
func (rq *ProxyRequest) nextAttemptID(targetID string) string {
	rq.mu.Lock()
	defer rq.mu.Unlock()
	rq.attSeq++
	return fmt.Sprintf("%s#%d", targetID, rq.attSeq)
}

// obsAttemptSettled emits the committed outcome of one initial send:
// exactly one ForwardAttemptFinished event — on success additionally
// carrying the settled attempt's correlation — and, for the first
// successful initial send of the request, the one-shot RequestForwarded
// event. It runs on the drain goroutine outside the coordinator guard,
// right where the send's actual result committed.
func (rq *ProxyRequest) obsAttemptSettled(
	ctx context.Context,
	targetID string,
	attID string,
	err error,
) {
	rq.emitObs(ctx, ProxyEvent{
		Kind:      ProxyEventForwardAttemptFinished,
		TargetID:  targetID,
		AttemptID: attID,
		Error:     err,
	})
	if err != nil {
		return
	}

	rq.mu.Lock()
	first := !rq.firstFwd
	rq.firstFwd = true
	rq.mu.Unlock()
	if !first {
		return
	}
	rq.emitObs(ctx, ProxyEvent{
		Kind:      ProxyEventRequestForwarded,
		TargetID:  targetID,
		AttemptID: attID,
	})
}

// obsCompletionEffectLocked returns the effect emitting the one-shot
// completion observation of the just-committed outcome. It runs under the
// coordinator guard right after the terminal transition committed: the
// outcome and its carried data are copied out here so the effect emits a
// detached record at the drain boundary. Committing the observation mark
// here lets a snapshot record that the completion event already belongs
// to history, so a restore never replays it.
func (rq *ProxyRequest) obsCompletionEffectLocked() prxEffect {
	rq.evDone = true

	var sts ResponseStatus
	switch {
	case rq.outcome == ProxyOutcomeResponse && rq.final != nil:
		sts = rq.final.Status()
	case rq.outcome == ProxyOutcomeCancelled:
		// Cancellation intent only ever lands on an INVITE coordinator,
		// whose terminal answer is the generated 487 termination response.
		sts = ResponseStatusRequestTerminated
	case rq.outcome == ProxyOutcomeTimeout &&
		rq.req.Method().Equal(RequestMethodInvite):
		sts = ResponseStatusRequestTimeout
	}
	outcome := rq.outcome
	var err error
	switch outcome {
	case ProxyOutcomeRejected, ProxyOutcomeTimeout,
		ProxyOutcomeSendFailure, ProxyOutcomeAbandoned:
		err = rq.lastErr
	case ProxyOutcomeResponse, ProxyOutcomeCancelled, ProxyOutcomeSent:
		// Non-error outcomes carry no error detail.
	}
	return func(ctx context.Context) {
		rq.emitObs(ctx, ProxyEvent{
			Kind:       ProxyEventRequestCompleted,
			Outcome:    outcome,
			StatusCode: int(sts),
			Error:      err,
		})
	}
}

// attemptCorrelation resolves the observation correlation — target ID and
// attempt ID — of the attempt that delivered key, when the attempt record
// is still known. A response whose producing attempt already retired, or
// a proxy-generated response, carries no correlation.
func (rq *ProxyRequest) attemptCorrelation(key ClientTransactionKey) (targetID, attID string) {
	rq.mu.Lock()
	defer rq.mu.Unlock()
	if att := rq.attempts[key]; att != nil {
		return att.targetID, att.attID
	}
	return "", ""
}

// prxRejectionSend is the pending observation record of one answered
// rejection: it correlates the answering response's actual send with the
// rejected lightweight operation that produced it. The record lives only
// until the first matching send or the bounded registry evicts it.
type prxRejectionSend struct {
	op  *prxOperation
	sts ResponseStatus
}

// prxMaxRejectedSends bounds the pending rejection-send registry: a
// rejection record only bridges an interceptor return and its transport
// answer, so a small FIFO cap covers every in-flight rejection without
// retaining history.
const prxMaxRejectedSends = 128

// rejSendKey normalizes a server transaction key so the response that
// answers a rejected request matches the request's own key: the response
// carries no Request-URI and gains a generated To tag the request did
// not have, while every other field — Via, CSeq, Call-ID, From tag — is
// copied verbatim.
func rejSendKey(key ServerTransactionKey) ServerTransactionKey {
	key.ToTag = ""
	key.URI = ""
	return key
}

// trackRejectedSend registers the pending observation record of one
// answered rejection, keyed by the normalized transaction identity of the
// request being rejected. The oldest pending record is evicted past the
// bound.
func (prx *Proxy) trackRejectedSend(
	key ServerTransactionKey,
	op *prxOperation,
	sts ResponseStatus,
) {
	if prx.closed.Load() {
		return
	}
	key = rejSendKey(key)
	prx.rejMu.Lock()
	defer prx.rejMu.Unlock()
	if prx.rejSends == nil {
		prx.rejSends = make(map[ServerTransactionKey]*prxRejectionSend)
	}
	if _, dup := prx.rejSends[key]; !dup {
		prx.rejOrd = append(prx.rejOrd, key)
	}
	prx.rejSends[key] = &prxRejectionSend{op: op, sts: sts}
	for len(prx.rejOrd) > prxMaxRejectedSends {
		old := prx.rejOrd[0]
		prx.rejOrd = prx.rejOrd[1:]
		delete(prx.rejSends, old)
	}
}

// takeRejectedSend consumes the pending rejection record matching res by
// normalized transaction identity and expected status. A response of the
// wrong status — for example a transaction's automatic 100 Trying — never
// consumes the record.
func (prx *Proxy) takeRejectedSend(res *ResponseEnvelope) (*prxRejectionSend, bool) {
	key, err := MakeServerTransactionKey(res)
	if err != nil {
		return nil, false
	}
	key = rejSendKey(key)
	prx.rejMu.Lock()
	defer prx.rejMu.Unlock()
	rec, ok := prx.rejSends[key]
	if !ok || rec.sts != res.Status() {
		return nil, false
	}
	delete(prx.rejSends, key)
	return rec, true
}

// observedReqResult applies the pipeline-exit result mapping and emits the
// rejection observation of a claimed operation that exits with an error:
// one Handled record plus one Completed(Rejected) record, and a pending
// send record so the answering response — when the transport actually
// sends it — is observed as a forwarded response. Consumed requests and
// operations that already emitted their own observation return silently.
func (prx *Proxy) observedReqResult(
	ctx context.Context,
	route *ProxyRoute,
	req *RequestEnvelope,
	err error,
) error {
	res := prxReqResult(req, err)
	if res == nil || errors.Is(res, errPrxObserved) {
		return res
	}
	prx.observeRejectedOp(ctx, route, req, res)
	return res
}

// observeRejectedOp emits the operation-level observation of a matched
// request rejected before any forwarding machinery owned it: Handled at
// sequence 1, Completed(Rejected) at sequence 2, and the pending record
// that lets the answering send close the operation's event stream.
func (prx *Proxy) observeRejectedOp(
	ctx context.Context,
	route *ProxyRoute,
	req *RequestEnvelope,
	err error,
) {
	op := newPrxOperation(route, req.Method())
	op.emit(ctx, prx, ProxyEvent{Kind: ProxyEventRequestHandled})
	sts := ResponseStatusServerInternalError
	if rej, ok := errors.AsType[*RequestRejectedError](err); ok &&
		rej.ResponseStatus().IsValid() {
		sts = rej.ResponseStatus()
	}
	op.emit(ctx, prx, ProxyEvent{
		Kind:       ProxyEventRequestCompleted,
		Outcome:    ProxyOutcomeRejected,
		StatusCode: int(sts),
		Error:      err,
	})
	if key, keyErr := MakeServerTransactionKey(req); keyErr == nil {
		prx.trackRejectedSend(key, op, sts)
	}
}
