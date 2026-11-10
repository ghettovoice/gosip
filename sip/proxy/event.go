package proxy

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

// EventKind classifies a [Event] observation record.
type EventKind int

const (
	// EventKindRequestHandled indicates an inbound request claimed by a
	// proxy route entered the forwarding pipeline.
	EventKindRequestHandled EventKind = iota
	// EventKindForwardAttemptFinished indicates a downstream forward
	// attempt of a proxied request produced its decision.
	EventKindForwardAttemptFinished
	// EventKindRequestForwarded indicates an outbound request copy was
	// emitted toward a target.
	EventKindRequestForwarded
	// EventKindResponseForwarded indicates a response was relayed toward
	// the prior hop.
	EventKindResponseForwarded
	// EventKindRequestCompleted indicates a proxied request reached its
	// committed outcome.
	EventKindRequestCompleted
)

func (k EventKind) String() string {
	switch k {
	case EventKindRequestHandled:
		return "request_handled"
	case EventKindForwardAttemptFinished:
		return "forward_attempt_finished"
	case EventKindRequestForwarded:
		return "request_forwarded"
	case EventKindResponseForwarded:
		return "response_forwarded"
	case EventKindRequestCompleted:
		return "request_completed"
	default:
		return fmt.Sprintf("unknown(%d)", int(k))
	}
}

// Event is a detached observation record of one proxy forwarding
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
type Event struct {
	// Kind classifies the observed boundary.
	Kind EventKind
	// Time is the wall-clock time the event was produced.
	Time time.Time
	// RequestID is the correlation identifier of the owning proxy request,
	// empty when the event has no request association.
	RequestID string
	// Sequence is the emission order of the event within its request,
	// zero when the event has no request association.
	Sequence uint64
	// RouteName is the name of the claiming [Route], empty when the
	// event has no route association.
	RouteName string
	// Mode is the forward mode of the claiming route.
	Mode ForwardMode
	// Method is the SIP request method the event relates to.
	Method sip.RequestMethod
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
	Outcome RequestOutcome
	// Error is the detached failure classification of the event, nil when
	// the event reports no failure.
	Error error
}

// EventHandler is a callback invoked synchronously when the proxy
// emits a [Event].
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
type EventHandler interface {
	HandleEvent(ctx context.Context, event Event)
}

// EventHandlerFunc is a function adapter that implements
// [EventHandler].
type EventHandlerFunc func(ctx context.Context, event Event)

func (f EventHandlerFunc) HandleEvent(ctx context.Context, event Event) {
	f(ctx, event)
}

// BindEventHandler binds the callback invoked on each [Event] this
// proxy emits, see [EventHandler] for the dispatch contract.
//
// Binding observes future events only, nothing is replayed. The callback
// can be unbound by calling the returned function; unbind is idempotent,
// may be called reentrantly from inside the handler, and does not wait on
// an in-flight dispatch that already captured the handler. A nil handler
// or a bind made once the proxy lifecycle ended returns a noop unbind;
// [Proxy.Shutdown] and [Proxy.Close] detach every tracked subscription.
func (prx *Proxy) BindEventHandler(h EventHandler) (unbind func()) {
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
func (prx *Proxy) emitEvt(ctx context.Context, event Event) {
	for h := range prx.onEvent.All() {
		h.HandleEvent(ctx, event)
	}
}

// errObserved marks an error whose operation already emitted every
// observation event it will ever produce. The pipeline-exit observer
// skips its own handled/completed records for it so one operation never
// counts twice.
const errObserved errors.Error = "proxy operation observed"

// operation is the observation cursor of one lightweight operation — a
// stateless traversal or an early matched rejection — that owns no
// request coordinator. It adopts the correlation ID minted at the
// request's claim and owns its committed dispatch sequence: events of
// one operation share the ID and count 1, 2, 3... in emission order,
// while the ID itself is transient and retained only for the operation's
// own pending response send.
type operation struct {
	id     string
	seq    uint64
	elm    *sip.Element
	route  *Route
	method sip.RequestMethod
}

func newOperation(elm *sip.Element, route *Route, method sip.RequestMethod, requestID string) *operation {
	if requestID == "" {
		// A claimed request always carries the minted claim identity; an
		// operation created outside the claim pipeline still gets a
		// distinct correlation for its event stream.
		requestID = util.RandString(24)
	}
	return &operation{
		id:     requestID,
		elm:    elm,
		route:  route,
		method: method,
	}
}

// emit stamps the operation's committed sequence and correlation on the
// detached event and dispatches it synchronously. Handlers observe the
// operation's own immutable element, never an element a transient
// effect context — such as the later answering send's — happened to carry.
func (op *operation) emit(ctx context.Context, prx *Proxy, ev Event) {
	op.seq++
	ev.RequestID = op.id
	ev.Sequence = op.seq
	ev.Time = time.Now()
	ev.Method = op.method
	if op.route != nil {
		ev.RouteName = op.route.Name
		ev.Mode = op.route.ForwardMode
	}
	prx.emitEvt(sip.ContextWithElement(ctx, op.elm), ev)
}

// requestID returns the coordinator's observation correlation identity:
// the operation ID minted once at the request's claim — or restored from
// its checkpoint — shared with the envelope claim stamps.
func (rq *Request) requestID() string {
	rq.mu.Lock()
	defer rq.mu.Unlock()
	return rq.id
}

// emitObs stamps the coordinator-owned correlation and the committed
// dispatch sequence on the detached event and dispatches it synchronously
// at the drain/effect boundary — outside the coordinator guard. The
// sequence is allocated under the coordinator guard so concurrent
// producers (the admission goroutine and the drain owner) keep one
// committed order. A restored coordinator continues the checkpointed
// sequence under the original request identity instead of minting a new
// one, so post-activation events keep their historical correlation.
func (rq *Request) emitObs(ctx context.Context, ev Event) {
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
	rq.prx.emitEvt(sip.ContextWithElement(ctx, rq.elm), ev)
}

// nextAttemptID allocates a fresh attempt correlation ID for targetID:
// every physical send generation of one logical target gets its own ID,
// so a DNS retry or a size-switch replacement never aliases its
// predecessor in an observation.
func (rq *Request) nextAttemptID(targetID string) string {
	rq.mu.Lock()
	defer rq.mu.Unlock()
	rq.attSeq++
	return fmt.Sprintf("%s#%d", targetID, rq.attSeq)
}

// savedAttemptSeq parses the numeric counter suffix of a saved attempt
// correlation ID. Attempt IDs are minted as "<targetID>#<n>" and the
// target ID itself may contain '#', so only the LAST '#' delimits the
// counter; a missing or non-numeric suffix reports zero.
func savedAttemptSeq(attID string) uint64 {
	_, suffix, found := strings.CutLast(attID, "#")
	if !found {
		return 0
	}
	n, err := strconv.ParseUint(suffix, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// stampRestoredClaim rebuilds the claim-stage stamp of one owned inbound
// request copy under the restored identity and the effective route:
// checkpoint authority replaces a missing or stale record, while a
// related link the copy already knows stays intact. A nil envelope is a
// no-op.
func (rq *Request) stampRestoredClaim(env *sip.RequestEnvelope) {
	if env == nil {
		return
	}
	md := rq.stageMeta(StageClaim, "", "")
	if cur, ok := MetadataFrom(env.Metadata()); ok {
		md.RelatedRequestID = cur.RelatedRequestID
	}
	stampClaim(env, md)
}

// rebuildRestoredMetadata reconstructs the [Metadata] stamps of the owned
// dormant copies a restored request adopted from its checkpoint — before
// the request is published. The checkpoint is the only authority: the
// saved request identity, the claiming route's effective mode, and the
// saved attempt records replace any missing or stale stamps, while the
// saved source envelopes themselves are never mutated.
//
// It returns the resolved attempt correlation IDs keyed by client
// transaction key for the binding install.
func (rq *Request) rebuildRestoredMetadata(
	snap *RequestSnapshot,
) map[sip.ClientTransactionKey]string {
	rq.resumeRestoredAttSeq(snap)
	attIDs := rq.resolveRestoredAttIDs(snap)

	rq.stampRestoredClaim(rq.req)
	rq.stampRestoredClaim(rq.work)

	// A stored candidate keeps the attribution of the attempt record that
	// provably owns it; an unattributed aggregate never gains a winner.
	for _, res := range rq.finals {
		targetID, attID := rq.restoredResCorrelation(snap, res, attIDs)
		stampRes(res, rq.stageMeta(StageResponse, targetID, attID))
	}
	// A settled attempt's recorded outcome response carries its own
	// attempt attribution.
	for i := range rq.outcomes {
		oc := &rq.outcomes[i]
		if oc.Response != nil {
			stampRes(oc.Response, rq.stageMeta(StageResponse, oc.TargetID, attIDs[oc.AttemptKey]))
		}
	}
	return attIDs
}

// resumeRestoredAttSeq advances the attempt counter past every saved
// attempt ID's LAST '#' numeric suffix — a target ID may contain '#'
// itself — so a post-activation attempt can never alias a recorded one,
// and a record without a usable saved ID mints above all of them.
func (rq *Request) resumeRestoredAttSeq(snap *RequestSnapshot) {
	var attSeq uint64
	noteAttSeq := func(meta *sip.MessageMetadata) {
		if md, ok := MetadataFrom(meta); ok {
			if n := savedAttemptSeq(md.AttemptID); n > attSeq {
				attSeq = n
			}
		}
	}
	for i := range snap.Attempts {
		att := &snap.Attempts[i]
		if att.Request != nil {
			noteAttSeq(att.Request.Metadata())
		}
		if att.Response != nil {
			noteAttSeq(att.Response.Metadata())
		}
	}
	for _, res := range snap.Candidates {
		if res != nil {
			noteAttSeq(res.Metadata())
		}
	}

	rq.mu.Lock()
	if attSeq > rq.attSeq {
		rq.attSeq = attSeq
	}
	rq.mu.Unlock()
}

// resolveRestoredAttIDs maps every saved attempt record to its attempt
// correlation ID: a usable saved forward-stage ID is authoritative, while
// a record without one mints above every saved suffix.
func (rq *Request) resolveRestoredAttIDs(
	snap *RequestSnapshot,
) map[sip.ClientTransactionKey]string {
	attIDs := make(map[sip.ClientTransactionKey]string, len(snap.Attempts))
	for i := range snap.Attempts {
		att := &snap.Attempts[i]
		var attID string
		if att.Request != nil {
			if md, ok := MetadataFrom(att.Request.Metadata()); ok && md.Stage == StageForward {
				attID = md.AttemptID
			}
		}
		if attID == "" {
			attID = rq.nextAttemptID(att.TargetID)
		}
		attIDs[att.Key] = attID
	}
	return attIDs
}

// restoredResCorrelation resolves the target/attempt correlation a stored
// response-context candidate provably owns: exactly one saved attempt
// record must hold an equal stored response — an ambiguous or absent
// match leaves the aggregate unattributed rather than inventing a winner.
func (rq *Request) restoredResCorrelation(
	snap *RequestSnapshot,
	res *sip.ResponseEnvelope,
	attIDs map[sip.ClientTransactionKey]string,
) (targetID, attID string) {
	for i := range snap.Attempts {
		att := &snap.Attempts[i]
		if att.Response == nil || !att.Response.Equal(res) {
			continue
		}
		if targetID != "" {
			return "", ""
		}
		targetID, attID = att.TargetID, attIDs[att.Key]
	}
	return targetID, attID
}

// obsAttemptSettled emits the committed outcome of one initial send:
// exactly one ForwardAttemptFinished event — on success additionally
// carrying the settled attempt's correlation — and, for the first
// successful initial send of the request, the one-shot RequestForwarded
// event. It runs on the drain goroutine outside the coordinator guard,
// right where the send's actual result committed.
func (rq *Request) obsAttemptSettled(
	ctx context.Context,
	targetID string,
	attID string,
	err error,
) {
	rq.emitObs(ctx, Event{
		Kind:      EventKindForwardAttemptFinished,
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
	rq.emitObs(ctx, Event{
		Kind:      EventKindRequestForwarded,
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
func (rq *Request) obsCompletionEffectLocked() effect {
	rq.evDone = true

	var sts sip.ResponseStatus
	switch {
	case rq.outcome == OutcomeResponse && rq.final != nil:
		sts = rq.final.Status()
	case rq.outcome == OutcomeCancelled:
		// Cancellation intent only ever lands on an INVITE coordinator,
		// whose terminal answer is the generated 487 termination response.
		sts = sip.ResponseStatusRequestTerminated
	case rq.outcome == OutcomeTimeout &&
		rq.req.Method().Equal(sip.RequestMethodInvite):
		sts = sip.ResponseStatusRequestTimeout
	}
	outcome := rq.outcome
	var err error
	switch outcome {
	case OutcomeRejected, OutcomeTimeout,
		OutcomeSendFailure, OutcomeAbandoned:
		err = rq.lastErr
	case OutcomeResponse, OutcomeCancelled, OutcomeSent:
		// Non-error outcomes carry no error detail.
	}
	return func(ctx context.Context) {
		rq.emitObs(ctx, Event{
			Kind:       EventKindRequestCompleted,
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
func (rq *Request) attemptCorrelation(key sip.ClientTransactionKey) (targetID, attID string) {
	rq.mu.Lock()
	defer rq.mu.Unlock()
	if att := rq.attempts[key]; att != nil {
		return att.targetID, att.attID
	}
	return "", ""
}

// rejectionSend is the pending observation record of one answered
// rejection: it correlates the answering response's actual send with the
// rejected lightweight operation that produced it. The record lives only
// until the first matching send or the bounded registry evicts it.
type rejectionSend struct {
	op  *operation
	sts sip.ResponseStatus
}

// maxRejectedSends bounds the pending rejection-send registry: a
// rejection record only bridges an interceptor return and its transport
// answer, so a small FIFO cap covers every in-flight rejection without
// retaining history.
const maxRejectedSends = 128

// rejSendKey normalizes a server transaction key so the response that
// answers a rejected request matches the request's own key: the response
// carries no Request-URI and gains a generated To tag the request did
// not have, while every other field — Via, CSeq, Call-ID, From tag — is
// copied verbatim.
func rejSendKey(key sip.ServerTransactionKey) sip.ServerTransactionKey {
	key.ToTag = ""
	key.URI = ""
	return key
}

// trackRejectedSend registers the pending observation record of one
// answered rejection under elm's namespace, keyed by the normalized
// transaction identity of the request being rejected. The oldest pending
// record is evicted past the bound.
func (prx *Proxy) trackRejectedSend(
	elm *sip.Element,
	key sip.ServerTransactionKey,
	op *operation,
	sts sip.ResponseStatus,
) {
	if prx.closed.Load() {
		return
	}
	es := prx.getElemState(elm, true)
	if es == nil {
		return
	}
	key = rejSendKey(key)
	es.rejMu.Lock()
	defer es.rejMu.Unlock()
	if es.rejSends == nil {
		es.rejSends = make(map[sip.ServerTransactionKey]*rejectionSend)
	}
	if _, dup := es.rejSends[key]; !dup {
		es.rejOrd = append(es.rejOrd, key)
	}
	es.rejSends[key] = &rejectionSend{op: op, sts: sts}
	for len(es.rejOrd) > maxRejectedSends {
		old := es.rejOrd[0]
		es.rejOrd = es.rejOrd[1:]
		delete(es.rejSends, old)
	}
}

// takeRejectedSend consumes the pending rejection record matching res by
// normalized transaction identity and expected status inside elm's
// namespace. A nil or unseen element never consumes a record; a response
// of the wrong status — for example a transaction's automatic 100
// Trying — never consumes one either.
func (prx *Proxy) takeRejectedSend(elm *sip.Element, res *sip.ResponseEnvelope) (*rejectionSend, bool) {
	key, err := sip.MakeServerTransactionKey(res)
	if err != nil {
		return nil, false
	}
	es := prx.getElemState(elm, false)
	if es == nil {
		return nil, false
	}
	key = rejSendKey(key)
	es.rejMu.Lock()
	defer es.rejMu.Unlock()
	rec, ok := es.rejSends[key]
	if !ok || rec.sts != res.Status() {
		return nil, false
	}
	delete(es.rejSends, key)
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
	elm *sip.Element,
	route *Route,
	req *sip.RequestEnvelope,
	err error,
) error {
	res := reqResult(req, err)
	if res == nil || errors.Is(res, errObserved) {
		return res
	}
	prx.observeRejectedOp(ctx, elm, route, req, res)
	return res
}

// observeRejectedOp emits the operation-level observation of a matched
// request rejected before any forwarding machinery owned it: Handled at
// sequence 1, Completed(Rejected) at sequence 2, and the pending record
// that lets the answering send close the operation's event stream.
func (prx *Proxy) observeRejectedOp(
	ctx context.Context,
	elm *sip.Element,
	route *Route,
	req *sip.RequestEnvelope,
	err error,
) {
	op := newOperation(elm, route, req.Method(), claimID(req))
	op.emit(ctx, prx, Event{Kind: EventKindRequestHandled})
	sts := sip.ResponseStatusServerInternalError
	if rej, ok := errors.AsType[*sip.RequestRejectedError](err); ok &&
		rej.ResponseStatus().IsValid() {
		sts = rej.ResponseStatus()
	}
	op.emit(ctx, prx, Event{
		Kind:       EventKindRequestCompleted,
		Outcome:    OutcomeRejected,
		StatusCode: int(sts),
		Error:      err,
	})
	if key, keyErr := sip.MakeServerTransactionKey(req); keyErr == nil {
		prx.trackRejectedSend(elm, key, op, sts)
	}
}
