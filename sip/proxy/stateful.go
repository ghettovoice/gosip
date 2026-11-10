package proxy

import (
	"context"
	"iter"
	"log/slog"
	"maps"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/pkg/log"
	"github.com/ghettovoice/gosip/sip"
)

// txOwnerCtxKey is the private context key carrying the transaction
// ownership token. The token marks transactions created by this proxy: it is
// installed into the admission context before the transaction factory runs so
// [Proxy.HandleClientTransaction] and [Proxy.HandleServerTransaction] can
// claim the admitted transaction before Start. It is unforgeable outside this
// package; envelope metadata such as the recorded [Metadata] is
// application-controlled and never proves ownership.
type txOwnerCtxKey struct{}

// txOwner is the private ownership token for a proxy-created
// transaction. A server-side token (srv) claims the upstream transaction of a
// request coordinator; a client-side token additionally carries the target ID
// and attempt generation of the runner the transaction belongs to, so
// callbacks can be correlated independently of the transaction key. The
// token also collects the pending client-transaction admissions created
// under it — at most two per send, when the pre-wire size switch retries on
// a reliable transport — so the runner can commit exactly one of them once
// the outer send result is known.
type txOwner struct {
	rq       *Request
	targetID string
	gen      uint64
	srv      bool
	adms     []*admission
}

// ctxWithTxOwner returns a context carrying the ownership token.
func ctxWithTxOwner(ctx context.Context, owner *txOwner) context.Context {
	return context.WithValue(ctx, txOwnerCtxKey{}, owner)
}

// txOwnerFromCtx returns the ownership token carried by ctx.
func txOwnerFromCtx(ctx context.Context) (*txOwner, bool) {
	owner, ok := ctx.Value(txOwnerCtxKey{}).(*txOwner)
	return owner, ok && owner != nil
}

// workEvtKind identifies one unit of coordinator work.
type workEvtKind uint8

const (
	// evtSchedule runs target selection: the current selection snapshot is
	// computed under the coordinator guard and the selection hook runs as an
	// outside effect feeding evtSelection back through the queue.
	evtSchedule workEvtKind = iota + 1
	// evtSelection applies a validated selection result.
	evtSelection
	// evtAttemptErr reports a failed initial send of one attempt
	// generation.
	evtAttemptErr
	// evtClnRes carries a detached downstream response copy.
	evtClnRes
	// evtClnState carries a client transaction state transition.
	evtClnState
	// evtClnErr carries a client transaction error report.
	evtClnErr
	// evtSrvState carries a server transaction state transition.
	evtSrvState
	// evtSrvErr carries a server transaction error report.
	evtSrvErr
	// evtFinalRes signals that the committed final response is
	// ready for the forwarding decision.
	evtFinalRes
	// evtSrvSend carries the actual wire result of a proxy-initiated
	// upstream response send, correlated by the one-shot send token.
	evtSrvSend
	// evtCancel records an accepted upstream CANCEL on the request:
	// cancellation intent is installed, scheduling closes, and every
	// eligible pending attempt is cancelled through the deferred builder.
	evtCancel
	// evtTmrC carries the expiry of one attempt generation's armed
	// Timer C deadline.
	evtTmrC
	// evtStart queues the activation resume of a dormant restored
	// request: preserved deadlines re-arm, live targets rebuild their lazy
	// address runners, deferred retry and cancellation work resumes, and
	// scheduling continues from the restored progress.
	evtStart
)

// workEvent is one detached unit of coordinator work. Transaction
// callbacks only copy data into an event and enqueue it; all payloads are
// owned copies, never live protocol objects shared with a transaction.
type workEvent struct {
	kind     workEvtKind
	targetID string
	attID    string
	// relID carries the related child identity of an accepted CANCEL —
	// the child's own claim identity — so the downstream CANCELs the
	// cancellation spawns share its attribution.
	relID string
	gen   uint64
	key   sip.ClientTransactionKey
	ids   []string
	res   *sip.ResponseEnvelope
	err   error
	from  sip.TransactionState
	to    sip.TransactionState
}

// effect is one transport/transaction or application effect computed by
// the drain under the coordinator guard and executed outside it.
type effect func(ctx context.Context)

// attempt records one committed downstream client transaction together
// with the admission generation it was created for. Correlation by admission
// generation plus key prevents events from a replaced or failed physical
// transaction from being attributed to its successor. Once the attempt
// produced its decision — a settle or a failover advance — settled events
// only release the attempt record without changing target state again.
type attempt struct {
	targetID string
	attID    string
	gen      uint64
	tx       sip.ClientTransaction
	// reqCopy is the owned attempt request copy adopted at restore: the
	// live path always clones the transaction's own request, while a
	// restored attempt keeps the checkpoint's request so its rebuilt
	// forward stamp survives into later snapshots.
	reqCopy *sip.RequestEnvelope
	addr    sip.ResolvedAddr
	lastErr error
	sawRes  bool
	settled bool

	// cancelled records that a downstream CANCEL was already initiated for
	// this attempt — immediately for a transaction able to carry one, or
	// deferred to its first provisional response — so a repeated
	// cancellation pass never emits a second one.
	cancelled bool

	// sendSettled and sendOK record the already committed observation of
	// the attempt's initial send, and sendErrCls its failure
	// classification. A committed attempt record implies a successful send
	// observation — a failed initial send discards its admission and
	// leaves no record — so a live attempt only ever carries (true, true,
	// ""); the fields exist so a restored checkpoint can keep its stored
	// values intact.
	sendSettled bool
	sendOK      bool
	sendErrCls  string
}

// targetState tracks one logical plan target: whether it was started,
// its current attempt generation and client transaction, the lazy
// address-attempt runner, the recorded attempted addresses, the latest
// response seen, its settled state, and its Timer C deadline.
type targetState struct {
	target  Target
	started bool
	done    bool
	gen     uint64
	txKey   sip.ClientTransactionKey
	runner  *attemptRunner
	lastRes *sip.ResponseEnvelope

	// timeC is the armed absolute Timer C deadline of the current attempt
	// generation; tmrC is its runtime timer reporting the expiry through the
	// coordinator event queue. The pair is armed once per generation, is
	// re-armed by 101-199 provisional responses, and is released only when
	// the attempt generation is truly settled or abandoned — an initiated
	// cancellation keeps it armed so a cancelled branch that never answers
	// stays bounded.
	timeC time.Time
	tmrC  *time.Timer

	// cancelling records that cancellation was initiated for the target —
	// a received 6xx (and later an accepted upstream CANCEL) marks every
	// still-active target so no retryable outcome spawns a new branch.
	// Cancellation never settles the branch: a racing INVITE 2xx must
	// still reach the response context.
	cancelling bool

	// attemptAddrs is the ordered history of executed candidate addresses,
	// recorded for request snapshots.
	attemptAddrs []sip.ResolvedAddr
}

// Request is the per-request coordinator handle. It owns the upstream
// server transaction, the immutable resolved target plan, per-target and
// per-attempt progress, accumulated branch responses, the forwarded-final
// decision, the cancellation intent, and the Timer C deadlines.
//
// Every decision is serialized through a private event queue with a single
// asynchronous drain owner: transaction callbacks only copy data into events
// and enqueue them, never wait for coordinator work and never call back into
// a transaction while transaction internals may be held. An idle-to-nonempty
// transition schedules a short-lived drain goroutine, so an idle request
// holds no permanent goroutine and there is no proxy-wide decision lock.
//
// [Request.Snapshot] captures the committed state as a detached
// [RequestSnapshot]; every committed drain transition also publishes a
// new revision through [Proxy.BindSnapshotHandler] outside the coordinator
// guard.
type Request struct {
	prx    *Proxy
	route  *Route
	req    *sip.RequestEnvelope
	work   *sip.RequestEnvelope
	srvTx  sip.ServerTransaction
	srvKey sip.ServerTransactionKey
	plan   TargetPlan

	// elm is the immutable processing element the request was claimed or
	// restored under. Transaction factories, lookups, error reporting, and
	// cancellation always use this element — never whichever element a
	// transient callback context happened to carry.
	elm *sip.Element

	// ctx is the stable coordinator context captured from the admission
	// (transport serve) context without its cancellation: transaction
	// callback contexts are transient, so the drain must not inherit a
	// cancellable ctx from whichever enqueue happened to wake it.
	ctx context.Context //nolint:containedctx // stable coordinator context

	mu       sync.Mutex
	queue    []workEvent
	draining bool

	begun   bool
	done    bool
	outcome RequestOutcome
	lastErr error
	srvGone bool
	srvErr  error
	final   *sip.ResponseEnvelope

	// closed marks the coordinator retired by the proxy lifecycle end:
	// queued and later work events are dropped, and transaction
	// subscriptions staged afterwards detach immediately instead of
	// accumulating past the released coordinator.
	closed bool

	// finals accumulates the attributed downstream final responses in
	// arrival order — the RFC 3261 Section 16.7 response context the final
	// selection ranks once every eligible attempt settles. A declined
	// branch contributes nothing to it; an attempt that ended without a
	// response contributes only its error outcome.
	finals []*sip.ResponseEnvelope

	targets     map[string]*targetState
	attempts    map[sip.ClientTransactionKey]*attempt
	pendingAdms map[uint64]*admission
	outcomes    []BranchOutcome
	genSeq      uint64
	unbinds     []func()

	// cancelIntent records that cancellation was requested for the request;
	// no new branch may start once it is set. CANCEL processing is layered on
	// top of the intent by the cancellation task.
	cancelIntent bool

	// cncID records the lightweight child identity of the accepted CANCEL
	// that initiated cancellation, so the downstream CANCELs it spawns
	// share its attribution. It stays empty for a cancellation the
	// coordinator initiated itself — a received 6xx — where each
	// downstream CANCEL mints its own lightweight identity instead.
	cncID string

	// schedClosed records that target selection is closed: once a final
	// downstream response commits the request direction — a success or a
	// global failure — no new branch may start. Unstarted targets settle as
	// declined when scheduling closes.
	schedClosed bool

	// dormant marks a coordinator installed by [Proxy.RestoreRequest] before
	// its explicit activation: queued events accumulate without scheduling a
	// drain owner, so a dormant request emits no sends, no transaction
	// effects, and no snapshot revisions until activation.
	dormant bool

	// deliveries carries the explicit custom delivery bindings installed by
	// [Proxy.RestoreRequest]: a rebound flow replaces the delivery resolver
	// for the restored request, and a missing required flow is an explicit
	// failure, never a DNS or dial fallback. Nil means ordinary
	// [Options.ResolveDelivery] resolution.
	deliveries map[DeliveryRef]BoundDelivery

	// sentRes records the pointer identities of downstream responses whose
	// upstream send was already initiated, so a response is never forwarded
	// twice through the same identity.
	sentRes map[*sip.ResponseEnvelope]struct{}
	// finalForwarded records that a final response was actually written to
	// the upstream transport. Only the intercepted wire result may commit
	// it — a nil transaction-level send return alone never does.
	finalForwarded bool
	// sendErr holds the last reported upstream response-send failure. It is
	// bookkeeping only: a write failure never cancels unrelated pending
	// branches.
	sendErr error

	// snapRev is the last committed snapshot revision of the request and
	// snapTime its commit time. Revisions increase monotonically on every
	// published drain transition; a transition that fails the committed
	// state check — a partial branch decision — publishes nothing.
	snapRev  uint64
	snapTime time.Time

	// id is the observation correlation identifier of the request, minted
	// at admission — or restored from the checkpoint so post-activation
	// events keep the original correlation; evSeq is its committed event
	// dispatch sequence; firstFwd marks the already committed first
	// successful initial send; evDone marks the already committed
	// completion observation; attSeq allocates distinct attempt
	// correlation IDs.
	id       string
	evSeq    uint64
	firstFwd bool
	evDone   bool
	attSeq   uint64
}

var (
	_ sip.TransactionHandler         = (*Proxy)(nil)
	_ sip.OutboundRequestInterceptor = (*Proxy)(nil)
)

// newReq creates the coordinator for one stateful admission.
// The coordinator exists before the upstream transaction is created so the
// admission-time ownership token can already point at it. The admission ctx
// is captured without cancellation as the coordinator's stable context.
// requestID is the operation identity minted once at the request's claim —
// or restored from a checkpoint — and is adopted as supplied, never
// re-minted: the coordinator, its observation stream, and the envelope
// claim stamps share one identity.
func newReq(
	ctx context.Context,
	elm *sip.Element,
	prx *Proxy,
	route *Route,
	req *sip.RequestEnvelope,
	requestID string,
) *Request {
	return &Request{
		prx:         prx,
		elm:         elm,
		route:       route,
		req:         req,
		ctx:         context.WithoutCancel(sip.ContextWithElement(ctx, elm)),
		targets:     make(map[string]*targetState),
		attempts:    make(map[sip.ClientTransactionKey]*attempt),
		pendingAdms: make(map[uint64]*admission),
		id:          requestID,
	}
}

// begin commits the resolved plan to the coordinator and schedules the first
// selection cycle on the event queue.
func (rq *Request) begin(ctx context.Context, req *sip.RequestEnvelope, plan TargetPlan) {
	rq.mu.Lock()
	if rq.begun {
		rq.mu.Unlock()
		return
	}
	rq.begun = true
	rq.work = req
	rq.plan = plan
	for _, group := range plan.Groups {
		for _, trgt := range group {
			rq.targets[trgt.ID] = &targetState{target: trgt}
		}
	}
	rq.mu.Unlock()

	rq.enqueue(ctx, workEvent{kind: evtSchedule})
}

// enqueue appends one detached event to the request queue. It never blocks:
// an idle-to-nonempty transition schedules a single asynchronous drain owner,
// so callbacks return immediately and all coordinator decisions happen in the
// drain. The drain runs under the coordinator's stable admission context, not
// the enqueuing callback's transient ctx. A dormant restored request only
// accumulates events: its queue cannot drain until activation.
func (rq *Request) enqueue(ctx context.Context, event workEvent) {
	rq.mu.Lock()
	if rq.closed {
		rq.mu.Unlock()
		return
	}
	rq.queue = append(rq.queue, event)
	if rq.draining || rq.dormant {
		rq.mu.Unlock()
		return
	}
	rq.draining = true
	drainCtx := rq.ctx //nolint:contextcheck // drain intentionally uses the request lifecycle ctx, not the caller ctx
	rq.mu.Unlock()

	if drainCtx == nil {
		drainCtx = context.WithoutCancel(ctx)
	}
	go rq.drain(drainCtx)
}

// queueLocked appends a follow-up event while the drain is processing under
// the coordinator guard. The current drain iteration picks it up.
func (rq *Request) queueLocked(event workEvent) {
	rq.queue = append(rq.queue, event)
}

// drain is the single-owner asynchronous decision path of the coordinator.
// It pops queued events one at a time, computes state transitions under the
// coordinator guard, and executes the resulting transport/transaction and
// application effects outside the guard. It retires as soon as the queue is
// idle, leaving no permanent goroutine behind.
func (rq *Request) drain(ctx context.Context) {
	// Every effect and observer notification the drain runs observes the
	// request's own element, never an element a transient enqueue or
	// callback context happened to carry.
	ctx = sip.ContextWithElement(ctx, rq.elm)

	defer func() {
		// A panicking effect must neither take down the process nor pin the
		// drain owner flag: release it and log the panic, matching the
		// recover-and-log convention used for inbound message processing in
		// the transport layer, so a later enqueue can schedule a fresh drain.
		if p := recover(); p != nil {
			rq.mu.Lock()
			rq.draining = false
			// A drain panic is request-scoped, not a stage boundary: the
			// request's recorded claim stamp identifies the coordinator on
			// the line, with the canonical claim record as fallback when
			// the envelope somehow lost it.
			md, ok := MetadataFrom(rq.req.Metadata())
			if !ok {
				md = rq.stageMetaLocked(StageClaim, "", "")
			}
			rq.mu.Unlock()

			rq.prx.logger(ctx).ErrorContext(ctx, "panic occurred in proxy request drain",
				slog.Any("proxy", md),
				slog.Any("error", p),
				slog.Any("stack", log.StringValue(debug.Stack())),
			)
		}
	}()

	for {
		rq.mu.Lock()
		if len(rq.queue) == 0 {
			rq.draining = false
			rq.mu.Unlock()
			return
		}
		ev := rq.queue[0]
		rq.queue[0] = workEvent{}
		rq.queue = rq.queue[1:]
		effects := rq.applyEvtLocked(ctx, ev)
		rq.mu.Unlock()

		for _, ef := range effects {
			ef(ctx)
		}

		rq.publishSnapshot(ctx)
	}
}

// applyEvtLocked computes the state transition for one queued event and
// returns the effects to execute outside the coordinator guard.
//
// Events of a still-pending client-transaction admission are buffered on the
// admission itself: nothing may observe a success or failure before the
// outer send operation's return is known and the admission is committed or
// discarded.
func (rq *Request) applyEvtLocked(ctx context.Context, ev workEvent) []effect {
	if rq.closed {
		return nil
	}
	if adm, ok := rq.pendingAdms[ev.gen]; ok {
		adm.events = append(adm.events, ev)
		return nil
	}

	switch ev.kind {
	case evtSchedule:
		return rq.applyScheduleLocked(ctx)
	case evtSelection:
		return rq.applySelectionLocked(ctx, ev)
	case evtAttemptErr:
		return rq.applyAttemptErrLocked(ctx, ev)
	case evtClnRes:
		return rq.applyClnResLocked(ctx, ev)
	case evtClnState:
		return rq.applyClnStateLocked(ctx, ev)
	case evtClnErr:
		return rq.applyClnErrLocked(ctx, ev)
	case evtSrvState:
		return rq.applySrvStateLocked(ctx, ev)
	case evtSrvErr:
		rq.srvErr = ev.err
		return nil
	case evtFinalRes:
		return rq.applyFinalResLocked(ctx)
	case evtSrvSend:
		return rq.applySrvSendLocked(ctx, ev)
	case evtCancel:
		return rq.applyCancelLocked(ev)
	case evtTmrC:
		return rq.applyTmrCLocked(ctx, ev)
	case evtStart:
		return rq.applyStartLocked(ctx)
	default:
		return nil
	}
}

// applyStartLocked computes the activation continuation of a dormant
// restored request once [Request.Start] queues it. Everything it does
// continues from the checkpoint instead of replaying it: preserved
// absolute Timer C deadlines re-arm as runtime timers — an expired
// deadline fires immediately rather than after a fresh interval — live
// targets rebuild the lazy address runners a checkpoint cannot carry, a
// pending address retry resumes its deferred failover, a recorded
// cancellation resumes only the work still legally deferred, and target
// scheduling continues from the restored progress. Completed groups and
// settled targets are never touched again.
func (rq *Request) applyStartLocked(ctx context.Context) []effect {
	if rq.done || !rq.begun {
		// A terminal restored request is inspect-only: its decisions are
		// already committed and no deferred work may resume.
		return nil
	}

	var effects []effect
	for _, id := range slices.Sorted(maps.Keys(rq.targets)) {
		st := rq.targets[id]
		if !st.started || st.done {
			continue
		}
		if !st.timeC.IsZero() && st.tmrC == nil {
			rq.armRestoredTmrCLocked(ctx, st)
		}
		if rq.cancelIntent || st.cancelling || st.runner != nil || st.target.Delivery != (DeliveryRef{}) {
			continue
		}
		// A pending address retry owns a deferred failover: the rebuilt
		// runner advances to the next candidate. A live attempt only
		// rebuilds the runner a future retryable outcome may still need.
		targetID, gen, resume := id, st.gen, false
		if att := rq.attempts[st.txKey]; att == nil || att.settled {
			resume = true
		}
		effects = append(effects, func(ctx context.Context) {
			if rq.rebuildAttemptRunner(ctx, targetID, gen) && resume {
				rq.advanceAttempt(ctx, targetID, gen)
			}
		})
	}
	// A recorded cancellation resumes only the work still legally
	// deferred: the upstream CANCEL intent cancels every still-active
	// target, while a recorded per-target cancellation — a committed
	// global-failure direction — owes only the deferred downstream
	// CANCELs the checkpoint could not emit. The supplied transactions
	// are started by now, so a CANCEL on a Proceeding branch is legal.
	if rq.cancelIntent {
		effects = append(effects, rq.cancelPendingAttemptsLocked()...)
	} else {
		effects = append(effects, rq.cancelMarkedAttemptsLocked()...)
	}
	rq.queueLocked(workEvent{kind: evtSchedule})
	return effects
}

// rebuildAttemptRunner reopens the lazy next-hop candidate iterator of one
// restored live target. The checkpoint carried the target's attempted
// candidate addresses and settled attempt errors but no live iterator, so
// the rebuilt runner skips every already executed candidate and keeps the
// recorded failover errors: contacts are never re-resolved, and no
// completed decision replays. A request-preparation or producer failure
// settles the target through the ordinary attempt-error event. It reports
// whether the rebuilt runner was installed — a deferred failover may only
// resume on an installed runner.
func (rq *Request) rebuildAttemptRunner(ctx context.Context, targetID string, gen uint64) bool {
	rq.mu.Lock()
	st := rq.targets[targetID]
	if st == nil || !st.started || st.done ||
		st.gen != gen || st.runner != nil ||
		st.target.Delivery != (DeliveryRef{}) ||
		rq.done || rq.cancelIntent {
		rq.mu.Unlock()
		return false
	}
	var (
		prx    = rq.prx
		route  = rq.route
		input  = requestInput{original: rq.req, working: rq.work}
		target = st.target
		tried  = slices.Clone(st.attemptAddrs)
		errs   []error
	)
	for i := range rq.outcomes {
		if oc := &rq.outcomes[i]; oc.TargetID == targetID && oc.Error != nil {
			errs = append(errs, oc.Error)
		}
	}
	rq.mu.Unlock()

	outReq, err := prx.prepareTargetReq(ctx, route, input, target)
	if err != nil {
		err = errors.Wrap(err)
		rq.obsAttemptSettled(ctx, targetID, rq.nextAttemptID(targetID), err)
		rq.failAttempt(ctx, targetID, gen, err)
		return false
	}
	attempts, err := rq.elm.ProduceRequestAttempts(ctx, outReq)
	if err != nil {
		err = errors.Wrap(err)
		rq.obsAttemptSettled(ctx, targetID, rq.nextAttemptID(targetID), err)
		rq.failAttempt(ctx, targetID, gen, err)
		return false
	}

	next, stop := iter.Pull(attempts)
	rn := &attemptRunner{targetID: targetID, gen: gen, stopFn: stop, errs: errs}
	rn.nextFn = func() (*sip.RequestAttempt, bool) {
		for {
			attempt, ok := next()
			if !ok {
				return nil, false
			}
			// The checkpoint already executed these candidates: the rebuilt
			// runner never repeats them.
			if attempt == nil || slices.Contains(tried, attempt.Addr) {
				continue
			}
			return attempt, true
		}
	}

	rq.mu.Lock()
	if st = rq.targets[targetID]; st == nil || st.done || st.gen != gen || st.runner != nil || rq.done {
		rq.mu.Unlock()
		rn.stop()
		return false
	}
	st.runner = rn
	rq.mu.Unlock()
	return true
}

// selectionLocked builds the read-only selection view of the unstarted
// groups, active target IDs, and settled outcomes.
//
// Groups are strictly sequential: only the earliest group that still holds
// an unsettled target may supply the next selection, so a later group stays
// hidden until every earlier target resolves.
func (rq *Request) selectionLocked() Selection {
	sel := Selection{Request: rq.work}
	for _, group := range rq.plan.Groups {
		var rem []Target
		open := false
		for _, trgt := range group {
			st := rq.targets[trgt.ID]
			if st == nil || !st.done {
				open = true
			}
			if st == nil || (!st.started && !st.done) {
				rem = append(rem, trgt)
			}
		}
		if len(rem) > 0 {
			sel.RemainingGroups = append(sel.RemainingGroups, rem)
		}
		if open {
			break
		}
	}
	for id, st := range rq.targets {
		if st.started && !st.done {
			sel.ActiveTargetIDs = append(sel.ActiveTargetIDs, id)
		}
	}
	slices.Sort(sel.ActiveTargetIDs)
	sel.CompletedOutcomes = slices.Clone(rq.outcomes)
	return sel
}

// applyScheduleLocked decides whether target selection may run now and, if
// so, defers the selection hook call to an outside effect.
//
// A cancellation or a committed direction that landed before the plan was
// committed leaves targets created after the close unsettled: scheduling
// closes again so they decline and the request can still complete.
func (rq *Request) applyScheduleLocked(context.Context) []effect {
	if rq.done || !rq.begun {
		return nil
	}
	if rq.cancelIntent || rq.schedClosed {
		rq.closeSchedulingLocked()
		return rq.finishLocked()
	}
	sel := rq.selectionLocked()
	if len(sel.RemainingGroups) == 0 && len(sel.ActiveTargetIDs) == 0 {
		return rq.finishLocked()
	}

	route := rq.route
	return []effect{func(ctx context.Context) {
		ids, err := rq.prx.selectTargets(ctx, route, sel)
		rq.enqueue(ctx, workEvent{kind: evtSelection, ids: ids, err: err})
	}}
}

// applySelectionLocked validates the selection result and emits one start
// effect per selected unstarted target. An empty selection waits while
// attempts are active and declines the remaining targets otherwise.
func (rq *Request) applySelectionLocked(ctx context.Context, ev workEvent) []effect {
	if rq.done || rq.cancelIntent || rq.schedClosed {
		return nil
	}
	if ev.err != nil {
		rq.prx.logger(ctx).WarnContext(ctx, "proxy target selection failed",
			slog.Any("proxy", rq.stageMetaLocked(StageForward, "", "")),
			slog.Any("error", ev.err),
		)
		return rq.failLocked(ev.err)
	}
	if len(ev.ids) == 0 {
		if len(rq.activeTargetIDsLocked()) == 0 {
			for _, st := range rq.targets {
				if !st.started {
					st.done = true
				}
			}
			return rq.finishLocked()
		}
		return nil
	}

	invite := rq.req.Method().Equal(sip.RequestMethodInvite)
	var effects []effect
	for _, id := range ev.ids {
		st := rq.targets[id]
		if st == nil || st.started || st.done {
			continue
		}
		st.started = true
		st.gen = rq.nextGenLocked()
		if invite {
			rq.armTmrCLocked(ctx, st)
		}
		gen := st.gen
		effects = append(effects, func(ctx context.Context) {
			rq.startAttempt(ctx, id, gen)
		})
	}
	return effects
}

// applyAttemptErrLocked settles an attempt whose initial send failed and
// schedules continuation.
func (rq *Request) applyAttemptErrLocked(_ context.Context, ev workEvent) []effect {
	st := rq.targets[ev.targetID]
	if st == nil || st.done || st.gen != ev.gen {
		return nil
	}
	st.done = true
	disarmTmrCLocked(st)
	rq.outcomes = append(rq.outcomes, BranchOutcome{
		TargetID:  ev.targetID,
		Error:     ev.err,
		Completed: true,
	})
	rq.lastErr = ev.err
	rq.queueLocked(workEvent{kind: evtSchedule})
	return append(detachAttemptRunnerLocked(st), rq.finishLocked()...)
}

// req returns the committed transaction's request copy, when known. A
// restored attempt owns the checkpoint's adopted copy; a live attempt
// clones its transaction's request.
func (att *attempt) req() *sip.RequestEnvelope {
	if att.reqCopy != nil {
		return att.reqCopy
	}
	if att.tx == nil {
		return nil
	}
	return att.tx.Request()
}

// applyClnResLocked attributes a detached downstream response to its
// attempt and settles the logical target on a final response. A 503 final
// response is a retryable outcome: the target's address runner advances to
// the next candidate instead of settling.
//
// A response arriving after the request completed — for example after an
// internal abandonment — is a stale discard: the attempt still settles so
// its later transaction termination only releases the record, and nothing
// is forwarded or recorded. An eligible INVITE success is the exception:
// it stays forwardable while the owned transactions remain usable, even on
// a settled attempt, a finished target, or a completed request.
func (rq *Request) applyClnResLocked(_ context.Context, ev workEvent) []effect {
	att := rq.attempts[ev.key]
	if att == nil || att.gen != ev.gen {
		return nil
	}

	// The attribution boundary stamps the detached inbound copy once with
	// the owning request and the attempt that delivered it: the record
	// stays on every stored candidate and forwarded clone until an
	// upstream send stamps its own stage.
	stampRes(ev.res, rq.stageMetaLocked(StageResponse, att.targetID, att.attID))

	// Every attributed response feeds the response-processing path as an
	// outside-guard effect; the minimal engine decides eligibility itself.
	fwd := rq.processResEffect(ev.key, ev.res)

	// An eligible INVITE 2xx stays forwardable after logical settlement:
	// RFC 3261 Section 16.7 forwards every 2xx while the owned transactions
	// remain usable, including exact retransmissions and successes racing
	// completion. Only the forwarding effect runs; processRes keeps the
	// abandonment, eligibility, and dedup guards, and no scheduling, branch
	// outcome, Timer C, completion, or first-forward decision reopens.
	st := rq.targets[att.targetID]
	if rq.inviteSuccessAfterSettleLocked(att, st, ev.res) {
		if !att.settled {
			settleAttemptLocked(att, st)
		}
		return []effect{fwd}
	}
	// A response on an already settled attempt is a hard discard: only the
	// eligible INVITE 2xx above bypasses settlement.
	if att.settled || st == nil {
		return nil
	}
	if rq.done {
		settleAttemptLocked(att, st)
		return nil
	}
	if st.done {
		return nil
	}

	if !ev.res.Status().IsFinal() {
		att.sawRes = true
		st.lastRes = ev.res
		// A 101-199 provisional proves the downstream branch is working on
		// the request and re-arms its Timer C; a 100 Trying never refreshes
		// the deadline.
		if !ev.res.Status().Equal(sip.ResponseStatusTrying) {
			rq.rearmTmrCLocked(st)
		}
		effects := []effect{fwd}
		// A provisional moves the branch transaction to Proceeding: the
		// first one is the earliest point a deferred branch cancellation
		// may legally emit its downstream CANCEL. The initiated CANCEL
		// keeps Timer C armed — cancellation never settles the branch, so
		// a cancelled branch that never answers its final response is
		// still bounded by the deadline.
		if st.cancelling && !att.cancelled {
			att.cancelled = true
			if tx := att.tx; tx != nil && tx.Type() == sip.TransactionTypeClientInvite {
				targetID, attID := att.targetID, att.attID
				effects = append(effects, func(ctx context.Context) {
					rq.cancelAttemptTx(ctx, tx, targetID, attID)
				})
			}
		}
		return effects
	}

	// Every attributed final joins the request's response context in
	// arrival order, including the retryable 503s the address runner
	// consumes: the context spans all attempts of a logical target, so the
	// final selection sees every response that ever arrived. A downstream
	// non-INVITE 408 is the exception: it records its branch outcome but
	// can never cross upstream, so it must not shadow a forwardable
	// sibling final in the selection.
	if rq.req.Method().Equal(sip.RequestMethodInvite) || !ev.res.Status().Equal(sip.ResponseStatusRequestTimeout) {
		rq.finals = append(rq.finals, ev.res)
	}

	if ev.res.Status() == sip.ResponseStatusServiceUnavailable && st.runner != nil && !st.cancelling {
		att.settled = true
		att.sawRes = true
		st.lastRes = ev.res
		return append(
			rq.advanceAttemptEffectLocked(st, att, errors.Wrap(&sip.RequestAttemptError{
				Cause:       errServiceUnavail,
				Addr:        att.addr,
				Request:     att.req(),
				Transaction: att.tx,
			})),
			fwd,
		)
	}

	att.settled = true
	st.done = true
	disarmTmrCLocked(st)
	rq.outcomes = append(rq.outcomes, BranchOutcome{
		TargetID:   att.targetID,
		AttemptKey: ev.key,
		Response:   ev.res,
		Completed:  true,
	})
	var effects []effect
	// A success commits the request direction and a global failure ends it:
	// in both cases RFC 3261 Section 16.7 forbids starting new branches, so
	// selection closes and every unstarted target settles as declined. The
	// 6xx additionally initiates cancellation of the pending branches while
	// the response context is kept until the committed decision — a racing
	// INVITE 2xx must still be able to win.
	switch sts := ev.res.Status(); {
	case sts.IsGlobalFailure():
		rq.closeSchedulingLocked()
		effects = rq.cancelPendingAttemptsLocked()
	case sts.IsSuccessful():
		rq.closeSchedulingLocked()
	}
	rq.queueLocked(workEvent{kind: evtSchedule})
	effects = append(effects, detachAttemptRunnerLocked(st)...)
	effects = append(effects, fwd)
	return append(effects, rq.finishLocked()...)
}

// processResEffect returns the outside-guard effect that feeds one
// detached downstream response into the response-processing path; the
// minimal engine decides eligibility itself.
func (rq *Request) processResEffect(key sip.ClientTransactionKey, res *sip.ResponseEnvelope) effect {
	return effect(func(ctx context.Context) {
		if err := rq.processRes(ctx, key, res); err != nil {
			// The failed upstream send owns this boundary's stamp: the
			// response copy crossed upstream under the same stage and
			// attempt correlation.
			targetID, attID := rq.attemptCorrelation(key)
			rq.prx.logger(ctx).DebugContext(ctx, "proxy response forwarding failed",
				slog.Any("proxy", rq.stageMeta(StageUpstream, targetID, attID)),
				slog.Any("error", err),
			)
		}
	})
}

// inviteSuccessAfterSettleLocked reports whether the detached response is
// an INVITE success that arrived after its attempt, its target, or the
// whole request already settled — the case that stays forwardable while
// the owned transactions remain usable.
func (rq *Request) inviteSuccessAfterSettleLocked(att *attempt, st *targetState, res *sip.ResponseEnvelope) bool {
	return rq.req.Method().Equal(sip.RequestMethodInvite) &&
		res != nil && res.Status().IsSuccessful() &&
		(att.settled || rq.done || (st != nil && st.done))
}

// settleAttemptLocked closes an attempt record and its logical target
// without recording a branch outcome: the attempt's later transaction
// termination only releases the record, and no scheduling or completion
// bookkeeping reopens.
func settleAttemptLocked(att *attempt, st *targetState) {
	att.settled = true
	att.sawRes = true
	if st != nil {
		st.done = true
		disarmTmrCLocked(st)
	}
}

// cancelPendingAttemptsLocked initiates cancellation of every still-active
// target, per RFC 3261 Section 16.7: a received 6xx cancels pending client
// transactions. Every unsettled INVITE attempt whose transaction can
// legally carry a CANCEL — it has reached the Proceeding state — receives
// one through the existing transaction builder; an attempt still in
// Calling defers its CANCEL to its first provisional response, and a
// non-INVITE attempt has no cancellation primitive at all and settles on
// its own events. Cancellation marks the target so no retryable outcome
// spawns a new branch; it never settles the branch itself — its armed
// Timer C stays so a cancelled branch that never answers still settles on
// the deadline — so a racing INVITE 2xx can still win.
func (rq *Request) cancelPendingAttemptsLocked() []effect {
	for _, st := range rq.targets {
		if st.started && !st.done {
			st.cancelling = true
		}
	}
	return rq.cancelMarkedAttemptsLocked()
}

// cancelMarkedAttemptsLocked emits the deferred downstream CANCEL of
// every still-eligible attempt whose target is already under
// cancellation: an attempt whose transaction can legally carry a CANCEL —
// it has reached the Proceeding state — receives one through the existing
// transaction builder, an attempt still in Calling defers its CANCEL to
// its first provisional response, and a non-INVITE attempt has no
// cancellation primitive at all and settles on its own events. The
// attempt's cancellation mark is committed before the send so a repeated
// pass never emits a second CANCEL.
func (rq *Request) cancelMarkedAttemptsLocked() []effect {
	var effects []effect
	for _, att := range rq.attempts {
		if att.settled || att.cancelled || att.tx == nil ||
			att.tx.Type() != sip.TransactionTypeClientInvite ||
			!att.tx.Started() ||
			att.tx.State() != sip.TransactionStateProceeding {
			continue
		}
		st := rq.targets[att.targetID]
		if st == nil || !st.cancelling {
			continue
		}
		att.cancelled = true
		tx, targetID, attID := att.tx, att.targetID, att.attID
		effects = append(effects, func(ctx context.Context) {
			rq.cancelAttemptTx(ctx, tx, targetID, attID)
		})
	}
	return effects
}

// applyCancelLocked records an accepted upstream CANCEL: no new branch may
// start, unstarted targets settle as declined, and every still-eligible
// attempt is cancelled through the deferred builder — an attempt still in
// Calling receives its downstream CANCEL on its first provisional
// response, an attempt that already produced its decision is never
// touched. Cancellation never settles a branch: a racing INVITE 2xx must
// still reach the response context.
func (rq *Request) applyCancelLocked(ev workEvent) []effect {
	if rq.done || rq.cancelIntent {
		return nil
	}
	rq.cancelIntent = true
	rq.cncID = ev.relID
	rq.closeSchedulingLocked()
	return append(rq.cancelPendingAttemptsLocked(), rq.finishLocked()...)
}

// cancelAttemptTx emits the downstream CANCEL of one cancelled attempt as
// an outside-guard effect. The transaction state is rechecked at effect
// time and again inside the builder: a branch that already moved past
// Proceeding — for example a racing success — must not receive a late
// CANCEL. A failed cancellation is logged and left to the branch's own
// settlement; it never disturbs the request.
func (rq *Request) cancelAttemptTx(ctx context.Context, tx sip.ClientTransaction, targetID, attID string) {
	if tx.Type() != sip.TransactionTypeClientInvite ||
		tx.State() != sip.TransactionStateProceeding {
		return
	}

	// The downstream CANCEL is built through the same core provenance
	// copy the transaction factory would make — the INVITE top Via and
	// the forwarding marks — and the child attribution is stamped on the
	// envelope before it ever reaches a transaction: a transaction's
	// stored request is immutable once admitted. The same stamp
	// correlates the cancellation's own log lines.
	md := rq.cancelReqStamp(targetID, attID)
	cncReq, err := sip.NewCancelRequestEnvelope(tx.Request())
	if err != nil {
		rq.prx.logger(ctx).WarnContext(ctx, "proxy branch cancellation failed",
			slog.Any("proxy", md),
			slog.Any("transaction", tx),
			slog.Any("error", err),
		)
		return
	}
	stampForward(cncReq, md)

	cncTx, err := rq.elm.NewClientTransaction(ctx, cncReq, tx.Transport())
	switch {
	case err == nil:
	case errors.Is(err, sip.ErrTransactionActionNotAllowed),
		errors.Is(err, sip.ErrTransactionManagerClosed),
		errors.Is(err, sip.ErrTransactionDuplicate):
		// The branch already settled, the manager is closing, or a
		// CANCEL for this branch exists already — nothing left to do.
		return
	default:
		rq.prx.logger(ctx).WarnContext(ctx, "proxy branch cancellation failed",
			slog.Any("proxy", md),
			slog.Any("transaction", tx),
			slog.Any("error", err),
		)
		return
	}
	if err := cncTx.Start(sip.ContextWithTransaction(ctx, cncTx)); err != nil {
		rq.prx.logger(ctx).WarnContext(ctx, "proxy branch cancellation transaction start failed",
			slog.Any("proxy", md),
			slog.Any("transaction", cncTx),
			slog.Any("error", err),
		)
	}
}

// cancelReqStamp builds the child attribution stamp of one downstream
// CANCEL before its transaction starts: the accepted CANCEL's own
// lightweight identity when one initiated this request's cancellation,
// otherwise a fresh one — always linked to the parent request and the
// cancelled attempt known at build time. The caller stamps it on the
// CANCEL envelope and attaches it to the cancellation's log lines; the
// core provenance copy keeps its forwarding marks untouched.
func (rq *Request) cancelReqStamp(targetID, attID string) Metadata {
	rq.mu.Lock()
	md := rq.stageMetaLocked(StageForward, targetID, attID)
	md.RelatedRequestID = md.RequestID
	md.RequestID = rq.cncID
	rq.mu.Unlock()

	if md.RequestID == "" {
		md.RequestID = util.RandString(24)
	}
	return md
}

// applyClnStateLocked tracks client transaction termination: a transaction
// that ends without a final response settles its attempt with the recorded
// transaction error, or advances the target's address runner when the
// outcome is retryable.
func (rq *Request) applyClnStateLocked(_ context.Context, ev workEvent) []effect {
	att := rq.attempts[ev.key]
	if att == nil || att.gen != ev.gen {
		return nil
	}
	if ev.to != sip.TransactionStateTerminated {
		return nil
	}
	delete(rq.attempts, ev.key)

	if att.settled {
		return nil
	}
	st := rq.targets[att.targetID]
	if st == nil || st.done {
		return nil
	}
	att.settled = true

	err := att.lastErr
	if err == nil {
		err = sip.NewTransactionTimedOutError()
	}
	// A target under cancellation may not spawn a new branch: the outcome
	// settles it instead of advancing the address runner.
	if st.runner != nil && !st.cancelling && attemptOutcomeRetryable(att, err) {
		return rq.advanceAttemptEffectLocked(st, att, errors.Wrap(&sip.RequestAttemptError{
			Cause:       err,
			Addr:        att.addr,
			Request:     att.req(),
			Transaction: att.tx,
		}))
	}

	st.done = true
	disarmTmrCLocked(st)
	rq.outcomes = append(rq.outcomes, BranchOutcome{
		TargetID:   att.targetID,
		AttemptKey: ev.key,
		Error:      err,
		Completed:  true,
	})
	rq.lastErr = err
	rq.queueLocked(workEvent{kind: evtSchedule})
	return append(detachAttemptRunnerLocked(st), rq.finishLocked()...)
}

// advanceAttemptEffectLocked records a retryable attempt outcome on the
// target's runner and returns the effect that pulls the next lazy
// next-hop candidate.
func (rq *Request) advanceAttemptEffectLocked(st *targetState, att *attempt, err error) []effect {
	st.runner.errs = append(st.runner.errs, err)
	targetID := att.targetID
	gen := st.gen
	return []effect{func(ctx context.Context) {
		rq.advanceAttempt(ctx, targetID, gen)
	}}
}

// detachAttemptRunnerLocked releases the target's lazy candidate iterator.
// The iterator stop runs as an outside-guard effect because its cleanup
// belongs to the producer, never to the coordinator guard.
func detachAttemptRunnerLocked(st *targetState) []effect {
	rn := st.runner
	st.runner = nil
	if rn == nil {
		return nil
	}
	return []effect{func(context.Context) { rn.stop() }}
}

// applyClnErrLocked records a client transaction error report; termination
// settles the attempt.
func (rq *Request) applyClnErrLocked(_ context.Context, ev workEvent) []effect {
	if att := rq.attempts[ev.key]; att != nil && att.gen == ev.gen {
		att.lastErr = ev.err
	}
	return nil
}

// applyFinalResLocked emits the forwarding decision for the committed
// final response. A downstream final decision runs inside processRes
// outside the guard, so the send never happens under the coordinator mutex.
//
// An INVITE exhausted without a usable final is answered by a generated
// response sent through the original upstream transaction: a 408 for the
// local-timeout outcome, a 487 for the cancelled outcome — a request with
// an accepted upstream CANCEL and no forwardable final is answered like a
// terminated request, per RFC 3261 Section 9.2. The response is built
// from the inbound request, whose own Via stack it keeps verbatim, so it
// skips the downstream Via removal entirely. A non-INVITE exhaustion never
// manufactures a 408 — the local timeout outcome is the recorded result and
// owned state retires silently.
func (rq *Request) applyFinalResLocked(context.Context) []effect {
	res := rq.final
	if res != nil {
		// The committed final answers upstream without a single-attempt
		// source: the stamp keeps the request identity only.
		md := rq.stageMetaLocked(StageUpstream, "", "")
		return []effect{func(ctx context.Context) {
			if err := rq.processRes(ctx, sip.ClientTransactionKey{}, res); err != nil {
				rq.prx.logger(ctx).DebugContext(ctx, "proxy final response forwarding failed",
					slog.Any("proxy", md),
					slog.Any("error", err),
				)
			}
		}}
	}

	var genSts sip.ResponseStatus
	switch {
	case rq.outcome == OutcomeCancelled:
		// Cancellation intent only ever lands on an INVITE coordinator, so
		// the generated response is always the 487 termination response.
		genSts = sip.ResponseStatusRequestTerminated
	case rq.outcome == OutcomeTimeout &&
		rq.req.Method().Equal(sip.RequestMethodInvite):
		genSts = sip.ResponseStatusRequestTimeout
	default:
		return nil
	}
	req := rq.req
	md := rq.stageMetaLocked(StageUpstream, "", "")
	return []effect{func(ctx context.Context) {
		res, err := req.NewResponse(genSts)
		if err != nil {
			rq.prx.logger(ctx).DebugContext(ctx, "proxy generated response build failed",
				slog.Any("proxy", md),
				slog.Any("error", err),
			)
			return
		}
		if err := rq.sendUpstream(ctx, res, "", ""); err != nil {
			rq.prx.logger(ctx).DebugContext(ctx, "proxy generated response forwarding failed",
				slog.Any("proxy", md),
				slog.Any("error", err),
			)
		}
	}}
}

// applySrvSendLocked records the actual wire result of a proxy-initiated
// upstream response send. Only a successful intercepted write commits the
// forwarded-final decision; a failure is recorded once and reported through
// the element error handlers — it never cancels unrelated pending branches.
func (rq *Request) applySrvSendLocked(_ context.Context, ev workEvent) []effect {
	if ev.err != nil {
		rq.sendErr = ev.err
		err := ev.err
		return []effect{func(ctx context.Context) {
			rq.elm.ReportError(ctx, err)
		}}
	}
	if ev.res != nil && ev.res.Status().IsFinal() {
		rq.finalForwarded = true
	}
	var sts int
	if ev.res != nil {
		sts = int(ev.res.Status())
	}
	return []effect{func(ctx context.Context) {
		rq.emitObs(ctx, Event{
			Kind:       EventKindResponseForwarded,
			StatusCode: sts,
			TargetID:   ev.targetID,
			AttemptID:  ev.attID,
		})
	}}
}

// applySrvStateLocked tracks the upstream transaction lifecycle; once it is
// gone the request index entry is released.
func (rq *Request) applySrvStateLocked(_ context.Context, ev workEvent) []effect {
	if ev.to != sip.TransactionStateTerminated {
		return nil
	}
	rq.srvGone = true
	if rq.srvKey.IsValid() {
		rq.prx.unregisterSrvReq(rq.srvKey, rq)
	}
	return nil
}

// activeTargetIDsLocked returns the IDs of started unfinished targets.
func (rq *Request) activeTargetIDsLocked() []string {
	var ids []string
	for id, st := range rq.targets {
		if st.started && !st.done {
			ids = append(ids, id)
		}
	}
	return ids
}

// nextGenLocked allocates a new admission generation.
func (rq *Request) nextGenLocked() uint64 {
	rq.genSeq++
	return rq.genSeq
}

// closeSchedulingLocked ends target selection: no new branch may start once
// a final downstream response commits the request direction. Unstarted
// targets settle as declined so request completion does not wait on
// branches that will never run; already started attempts keep settling
// through their own events.
func (rq *Request) closeSchedulingLocked() {
	rq.schedClosed = true
	for _, st := range rq.targets {
		if !st.started {
			st.done = true
		}
	}
}

// failLocked completes the request with an error when it cannot proceed
// and returns the abandon effects. Every still-open target settles without
// a recorded outcome, pending client-transaction admissions and lazy
// address runners are released, and a live upstream transaction is answered
// with the mapped rejection before being terminated: an application
// failure — for example a selection hook error or a selection contract
// violation — is answered coherently instead of leaving the caller to its
// own transaction timeout, and the fully done request forwards nothing
// more. A request without a live upstream transaction retires silently;
// an index entry that can no longer be released by a transaction lifecycle
// event is unregistered eagerly.
func (rq *Request) failLocked(err error) []effect {
	if rq.done {
		return nil
	}
	rq.done = true
	rq.lastErr = err
	rq.outcome = OutcomeAbandoned

	var effects []effect
	for _, st := range rq.targets {
		st.done = true
		disarmTmrCLocked(st)
		effects = append(effects, detachAttemptRunnerLocked(st)...)
	}
	clear(rq.pendingAdms)

	tx := rq.srvTx
	if tx == nil || rq.srvGone {
		if rq.srvKey.IsValid() {
			effects = append(effects, func(context.Context) {
				rq.prx.unregisterSrvReq(rq.srvKey, rq)
			})
		}
		return append(effects, rq.obsCompletionEffectLocked())
	}

	req := rq.req
	return append(effects,
		rq.obsCompletionEffectLocked(),
		func(ctx context.Context) {
			rq.rejectUpstream(ctx, req, err, tx)
		},
	)
}

// finishLocked completes the request once every plan target is settled and
// commits the outcome decision.
//
// A committed downstream final queues the forwarding decision; an INVITE
// exhausted without a usable final queues the same event so the proxy
// answers with a generated 408 through the original upstream transaction.
// When no permitted final response will ever be sent — a non-INVITE
// exhaustion or any other no-response outcome — the owned upstream
// transaction is retired silently instead of being abandoned to stale
// transaction cleanup.
func (rq *Request) finishLocked() []effect {
	if rq.done || !rq.begun || len(rq.targets) == 0 {
		return nil
	}
	for _, st := range rq.targets {
		if !st.done {
			return nil
		}
	}
	rq.done = true
	rq.outcome = rq.chooseOutcomeLocked()

	switch {
	case rq.outcome == OutcomeResponse && rq.final != nil:
		rq.queueLocked(workEvent{kind: evtFinalRes})
	case rq.outcome == OutcomeCancelled:
		// Cancellation intent only ever lands on the INVITE coordinator a
		// CANCEL matched, so no method check is needed here.
		rq.queueLocked(workEvent{kind: evtFinalRes})
	case rq.outcome == OutcomeTimeout &&
		rq.req.Method().Equal(sip.RequestMethodInvite):
		rq.queueLocked(workEvent{kind: evtFinalRes})
	default:
		if tx := rq.srvTx; tx != nil {
			md := rq.stageMetaLocked(StageUpstream, "", "")
			return []effect{
				rq.obsCompletionEffectLocked(),
				func(ctx context.Context) {
					if err := tx.Terminate(ctx, nil); err != nil {
						rq.prx.logger(ctx).DebugContext(ctx, "proxy upstream transaction retirement failed",
							slog.Any("proxy", md),
							slog.Any("error", err),
						)
					}
				},
			}
		}
	}
	return []effect{rq.obsCompletionEffectLocked()}
}

// chooseOutcomeLocked derives the committed outcome once every plan target
// is settled. The accumulated response context is ranked by the RFC 3261
// Section 16.7 selection and aggregation policy; declined branches and
// attempts that ended without a response contribute no candidate, so a lone
// received 503 still maps to a generated 500. Only when the context holds
// no usable final does the error classification run: a timeout outcome wins
// over other send failures — a downstream non-INVITE 408 counts as one,
// since it reports a downstream timeout rather than a forwardable final —
// and an all-declined request — silence for the upstream caller —
// classifies as a timeout.
func (rq *Request) chooseOutcomeLocked() RequestOutcome {
	if final, err := selectFinalRes(rq.finals); err == nil {
		rq.final = final
		return OutcomeResponse
	}
	// An accepted CANCEL ends the request without a usable final: the
	// original INVITE is answered by a generated 487 rather than the
	// local-timeout 408.
	if rq.cancelIntent {
		return OutcomeCancelled
	}
	invite := rq.req.Method().Equal(sip.RequestMethodInvite)
	for i := range rq.outcomes {
		oc := &rq.outcomes[i]
		if errors.Is(oc.Error, sip.ErrTransactionTimedOut) ||
			(!invite && oc.Response != nil &&
				oc.Response.Status().Equal(sip.ResponseStatusRequestTimeout)) {
			return OutcomeTimeout
		}
	}
	if len(rq.outcomes) > 0 {
		return OutcomeSendFailure
	}
	return OutcomeTimeout
}

// attachSrvTx claims the admitted upstream server transaction and installs
// the event subscriptions. It runs inside transaction admission, before
// Start, so no transaction event can outrun the association.
func (rq *Request) attachSrvTx(tx sip.ServerTransaction) {
	key := tx.Key()
	rq.stageSrvTx(tx)

	rq.prx.registerSrvReq(key, rq)
}

// stageSrvTx claims a server transaction and installs the event
// subscriptions without committing the request index. Restored requests
// bind through this seam before the guarded index commit, so a failed
// install can roll back only its own staged registrations.
func (rq *Request) stageSrvTx(tx sip.ServerTransaction) {
	key := tx.Key()
	unbinds := []func(){
		tx.BindStateHandler(sip.TransactionStateHandlerFunc(
			func(ctx context.Context, from, to sip.TransactionState) {
				rq.enqueue(ctx, workEvent{kind: evtSrvState, from: from, to: to})
			},
		)),
		tx.BindErrorHandler(sip.ErrorHandlerFunc(
			func(ctx context.Context, err error) {
				rq.enqueue(ctx, workEvent{kind: evtSrvErr, err: err})
			},
		)),
	}

	rq.mu.Lock()
	if rq.closed {
		// A retired coordinator never accumulates staged subscriptions:
		// they detach immediately instead of leaking past the shutdown.
		rq.mu.Unlock()
		for _, unbind := range unbinds {
			unbind()
		}
		return
	}
	rq.srvTx = tx
	rq.srvKey = key
	if tx.State() == sip.TransactionStateTerminated {
		rq.srvGone = true
	}
	rq.unbinds = append(rq.unbinds, unbinds...)
	rq.mu.Unlock()
}

// attachClnTx claims the admitted downstream client transaction and
// installs the event subscriptions. It runs inside transaction admission,
// before Start, so a response received inside the initial send is captured
// and attributed instead of racing the association.
//
// The admission gets its own generation and starts as pending: its events
// are buffered until the attempt runner learns the outer send result and
// commits exactly one admission. A discarded physical transaction — for
// example the failed UDP side of a local size switch that shares the branch
// key — keeps its own generation and can never settle the succeeding
// transaction.
func (rq *Request) attachClnTx(tx sip.ClientTransaction, owner *txOwner) {
	key := tx.Key()

	rq.mu.Lock()
	if rq.closed {
		rq.mu.Unlock()
		return
	}
	admGen := rq.nextGenLocked()
	adm := &admission{
		targetID: owner.targetID,
		runGen:   owner.gen,
		gen:      admGen,
		key:      key,
		tx:       tx,
	}
	owner.adms = append(owner.adms, adm)
	rq.pendingAdms[admGen] = adm
	rq.mu.Unlock()

	unbinds := []func(){
		tx.BindResponseHandler(sip.InboundResponseHandlerFunc(
			func(ctx context.Context, res *sip.ResponseEnvelope) {
				rq.enqueue(ctx, workEvent{
					kind: evtClnRes,
					gen:  admGen,
					key:  key,
					res:  res.Clone().(*sip.ResponseEnvelope), //nolint:forcetypeassert // the concrete type is fixed by construction
				})
			},
		)),
		tx.BindStateHandler(sip.TransactionStateHandlerFunc(
			func(ctx context.Context, from, to sip.TransactionState) {
				rq.enqueue(ctx, workEvent{
					kind: evtClnState,
					gen:  admGen,
					key:  key,
					from: from,
					to:   to,
				})
			},
		)),
		tx.BindErrorHandler(sip.ErrorHandlerFunc(
			func(ctx context.Context, err error) {
				rq.enqueue(ctx, workEvent{
					kind: evtClnErr,
					gen:  admGen,
					key:  key,
					err:  err,
				})
			},
		)),
	}

	rq.mu.Lock()
	if rq.closed {
		// A retired coordinator never accumulates staged subscriptions:
		// the fresh binds detach immediately and the staged admission is
		// dropped so the runner can never commit it.
		delete(rq.pendingAdms, adm.gen)
		owner.adms = slices.DeleteFunc(owner.adms, func(a *admission) bool {
			return a == adm
		})
		rq.mu.Unlock()
		for _, unbind := range unbinds {
			unbind()
		}
		return
	}
	rq.unbinds = append(rq.unbinds, unbinds...)
	rq.mu.Unlock()
}

// preparedRouting transfers an accepted routing result across the pre-send
// promotion boundary, retaining upstream identity separately from working state.
type preparedRouting struct {
	input requestInput
	plan  TargetPlan
}

func newPreparedRouting(
	original, working *sip.RequestEnvelope,
	plan TargetPlan,
) (preparedRouting, error) {
	if original == nil || original.MessageEnvelope == nil || working == nil || working.MessageEnvelope == nil {
		return preparedRouting{}, errors.Wrap(sip.NewInvalidMessageError("missing prepared routing request"))
	}
	input := requestInput{
		original: cloneReqEnvelope(original),
		working:  cloneReqEnvelope(working),
	}
	if err := input.original.Validate(); err != nil {
		return preparedRouting{}, errors.Wrap(err)
	}
	if err := input.working.Validate(); err != nil {
		return preparedRouting{}, errors.Wrap(err)
	}
	owned, err := cloneTargetPlan(plan)
	if err != nil {
		return preparedRouting{}, errors.Wrap(err)
	}
	if !owned.hasTargets() {
		return preparedRouting{}, errors.Wrap(errNoFwdTargets)
	}
	return preparedRouting{input: input, plan: owned}, nil
}

// admitStatefulReq owns one matched stateful request through admission:
// it creates and claims the upstream server transaction, starts it, runs
// authorization, routing preprocess and target resolution exactly once, then
// hands the owned plan to the request coordinator.
//
// A duplicate request maps to the already admitted transaction and is
// consumed by transaction-local retransmission rules, so concurrent
// admissions never repeat authorization or resolution.
func (prx *Proxy) admitStatefulReq(
	ctx context.Context,
	elm *sip.Element,
	route *Route,
	req *sip.RequestEnvelope,
) error {
	return errors.Wrap(prx.admitStatefulRouting(ctx, elm, route, req, nil))
}

// admitStatefulRouting shares transaction ownership, duplicate handling and
// admission-before-Start for ordinary routing and accepted-result promotion.
// Only a winning nil-prepared admission runs application routing policy.
func (prx *Proxy) admitStatefulRouting(
	ctx context.Context,
	elm *sip.Element,
	route *Route,
	original *sip.RequestEnvelope,
	prepared *preparedRouting,
) error {
	req := original
	if prepared != nil {
		req = prepared.input.original
		if req == nil || prepared.input.working == nil || !prepared.plan.hasTargets() {
			return errors.Wrap(sip.NewInvalidMessageError("incomplete prepared routing"))
		}
	}
	tp := prx.srvTpOf(elm, req)
	if tp == nil {
		return errors.Wrap(sip.NewRequestRejectedError(
			sip.NewNoTransportError(),
			slog.LevelError,
			sip.ResponseStatusServerInternalError,
		))
	}

	// The coordinator exists before the transaction: the ownership token in
	// the admission context lets HandleServerTransaction claim the registered
	// transaction and install its subscriptions before Start. The claim
	// stamp carries the operation identity minted once at the claim; a
	// direct admission without it — a restore or an internal probe — still
	// gives the coordinator a usable correlation.
	rqID := claimID(req)
	if rqID == "" {
		rqID = util.RandString(24)
	}
	rq := newReq(ctx, elm, prx, route, req, rqID)
	ownerCtx := ctxWithTxOwner(ctx, &txOwner{rq: rq, srv: true})

	srvTx, err := prx.newSrvTx(ownerCtx, elm, route, req, tp)
	if errors.Is(err, sip.ErrTransactionDuplicate) {
		return errors.Wrap(prx.consumeDupReq(ctx, elm, req, err))
	}
	if err != nil {
		return errors.Wrap(sip.NewRequestRejectedError(err, slog.LevelError, sip.ResponseStatusServerInternalError))
	}

	ctx = sip.ContextWithTransaction(ctx, srvTx)
	if err := srvTx.Start(ctx); err != nil {
		return errors.Wrap(sip.NewRequestRejectedError(err, slog.LevelError, sip.ResponseStatusServerInternalError))
	}

	// The lifecycle seal may have landed while the admission ran: the
	// request index already refused the registration, so the staged
	// coordinator retires here and the sealed claim answers coherently
	// instead of leaving a live transaction behind.
	if prx.closed.Load() {
		rq.shutdown(ctx)
		return errors.Wrap(sip.NewRequestRejectedError(
			NewClosedError(),
			slog.LevelDebug,
			sip.ResponseStatusServiceUnavailable,
		))
	}

	var (
		wReq *sip.RequestEnvelope
		plan TargetPlan
	)
	if prepared != nil {
		wReq, plan = prepared.input.working, prepared.plan
	} else {
		wReq, plan, err = prx.prepareStatefulRouting(ctx, elm, route, req)
		if err != nil {
			return errors.Wrap(err)
		}
	}

	// The admission is won and owned: the handled observation is committed
	// before the coordinator schedules its first selection cycle so it
	// always occupies sequence 1 of the request's event stream.
	rq.emitObs(ctx, Event{Kind: EventKindRequestHandled})
	rq.begin(ctx, wReq, plan)
	return nil
}

// consumeDupReq completes one lost admission race: the retransmitted
// request is delivered to the already admitted owner transaction, whose
// request identity rewrites the claim stamp before consumption — the
// duplicate emits no observation of its own. A transaction key that
// cannot be rebuilt, or an upstream transaction already gone, reports
// the admission error unchanged. An owner retired from the request index
// between the transaction lookup and the request lookup leaves the
// retransmission's own claim record: no live identity exists to join.
func (prx *Proxy) consumeDupReq(
	ctx context.Context,
	elm *sip.Element,
	req *sip.RequestEnvelope,
	dupErr error,
) error {
	key, keyErr := sip.MakeServerTransactionKey(req)
	if keyErr != nil {
		return errors.Wrap(dupErr)
	}
	existTx, ok := elm.LoadServerTransaction(key)
	if !ok {
		return errors.Wrap(dupErr)
	}

	if owner, ok := prx.loadSrvReq(elm, key); ok {
		// The absorbed retransmission joins the live owner's effective
		// claim — its request identity and mode — rather than keeping a
		// claim record minted under this traversal's configured route.
		stampClaim(req, owner.stageMeta(StageClaim, "", ""))
	}
	if err := existTx.RecvRequest(sip.ContextWithTransaction(ctx, existTx), req); err != nil {
		return errors.Wrap(err)
	}
	return errors.Wrap(errReqConsumed)
}

// prepareStatefulRouting runs routing policy only after ordinary admission
// wins. Promotion already owns this result and never enters this path.
func (prx *Proxy) prepareStatefulRouting(
	ctx context.Context,
	elm *sip.Element,
	route *Route,
	req *sip.RequestEnvelope,
) (*sip.RequestEnvelope, TargetPlan, error) {
	if err := prx.authorizeReq(ctx, route, req); err != nil {
		return nil, TargetPlan{}, errors.Wrap(err)
	}
	work, err := prx.preprocessRouting(ctx, elm, route, req)
	if err != nil {
		return nil, TargetPlan{}, errors.Wrap(err)
	}
	plan, err := prx.resolveTargets(ctx, route, work)
	if err != nil {
		return nil, TargetPlan{}, errors.Wrap(err)
	}
	return work, plan, nil
}

// srvTpOf picks the server transport for an upstream transaction
// serving req: the transport bound to the request's inbound protocol wins,
// otherwise any tracked transport.
func (prx *Proxy) srvTpOf(elm *sip.Element, req *sip.RequestEnvelope) sip.ServerTransport {
	tp, ok := elm.TransportByProto(req.Transport().Proto)
	if !ok {
		for tp = range elm.AllTransports() {
			break
		}
	}
	return tp
}

// promoteStatefulReq is the pre-send promotion seam of the stateless
// path: a response-eligible request whose deterministic stateless
// invariants cannot be established continues through stateful admission
// before any wire send. The accepted working request and complete plan
// bypass routing policy replay; the original retains transaction identity.
//
// The admission runs under a stateful-mode copy of the route: the
// promoted request is owned by the stateful pipeline and sent through
// client transactions, so every Via it emits must carry the stateful
// ownership mark. The stateless mark is reserved for genuine stateless
// sends — the inbound response path attributes ownership by it — and the
// stored route is never mutated.
func (prx *Proxy) promoteStatefulReq(
	ctx context.Context,
	elm *sip.Element,
	route *Route,
	prepared preparedRouting,
) error {
	if route != nil && route.ForwardMode != ForwardModeStateful {
		st := *route
		st.ForwardMode = ForwardModeStateful
		route = &st
		// The owned promotion copies record the effective mode they
		// continue under: the claiming route name and the operation
		// identity are unchanged, so both copies always carry the same
		// promotion record — an unstamped copy still gets one under a
		// shared minted identity. The app-facing envelope is reconciled
		// to this authoritative claim by the caller once admission wins.
		rqID := claimID(prepared.input.original)
		if rqID == "" {
			rqID = util.RandString(24)
		}
		for _, env := range []*sip.RequestEnvelope{prepared.input.original, prepared.input.working} {
			md, ok := MetadataFrom(env.Metadata())
			if !ok {
				md = Metadata{RouteName: route.Name, Stage: StageClaim}
			}
			md.RequestID = rqID
			md.Mode = ForwardModeStateful
			stampClaim(env, md)
		}
	}
	return errors.Wrap(prx.admitStatefulRouting(ctx, elm, route, prepared.input.original, &prepared))
}

// promoteStatefulResult completes a pre-send promotion for the
// application-facing req: on a winning admission its envelope joins the
// effective claim the owned promotion copies received, and on a lost
// admission race — when the already admitted owner consumed the promoted
// admission of a retransmission that re-traversed the stateless path — it
// joins the winning owner's effective claim, so no stale configured-mode
// record survives either reconciliation. An owner retired from the
// request index leaves this traversal's own claim record: no live
// identity exists to join.
func (prx *Proxy) promoteStatefulResult(
	ctx context.Context,
	elm *sip.Element,
	route *Route,
	req *sip.RequestEnvelope,
	prepared preparedRouting,
) error {
	err := prx.promoteStatefulReq(ctx, elm, route, prepared)
	if !errors.Is(err, errReqConsumed) {
		if err == nil {
			// The promotion was admitted: the app-facing envelope joins
			// the same effective claim the owned promotion copies
			// received, so its inbound record carries the stateful mode
			// the request actually continues under.
			if md, ok := MetadataFrom(prepared.input.original.Metadata()); ok {
				stampClaim(req, md)
			}
		}
		return errors.Wrap(err)
	}
	if key, keyErr := sip.MakeServerTransactionKey(req); keyErr == nil {
		if owner, ok := prx.loadSrvReq(elm, key); ok {
			// A live owner absorbed the promoted admission: the duplicate
			// inherits the owner's effective claim, not the stateless
			// record this traversal minted before losing the race.
			stampClaim(req, owner.stageMeta(StageClaim, "", ""))
		}
	}
	return errors.Wrap(err)
}

// HandleServerTransaction claims the server transactions this proxy created.
//
// The admission seam runs before transaction Start: when the ownership token
// proves the transaction belongs to a request of this proxy, the coordinator
// association and event subscriptions are installed immediately. Transactions
// without the token — or carrying only an application-controlled route name —
// are ignored.
func (prx *Proxy) HandleServerTransaction(ctx context.Context, tx sip.ServerTransaction) {
	owner, ok := txOwnerFromCtx(ctx)
	if !ok || !owner.srv || owner.rq == nil || owner.rq.prx != prx {
		return
	}
	owner.rq.attachSrvTx(tx)
}

// HandleClientTransaction claims the client transactions this proxy created.
//
// Like [Proxy.HandleServerTransaction] it runs inside admission before Start:
// the transaction is registered into the request context first, so a response
// received inside the initial send is captured and attributed to the attempt
// generation that created the transaction.
func (prx *Proxy) HandleClientTransaction(ctx context.Context, tx sip.ClientTransaction) {
	owner, ok := txOwnerFromCtx(ctx)
	if !ok || owner.srv || owner.rq == nil || owner.rq.prx != prx {
		return
	}
	owner.rq.attachClnTx(tx, owner)
}

// InterceptOutboundRequest rewrites the correlation stamp of an outbound
// request only when the private ownership token proves the send belongs to
// one of this proxy's committed attempts: the bound element must match the
// coordinator's, the request's canonical client transaction key must resolve
// to an attempt record carrying the token's target and admission generation,
// and the coordinator must still be live. The attempt's authoritative forward
// stamp then replaces whatever the envelope carried — a restored transaction
// retransmits its own checkpoint-era request, whose stamp is the record the
// rebuild already committed. Every other send delegates unchanged: no
// request identity, route name, or attempt correlation is ever minted here,
// and an ACK or CANCEL key can never inherit an INVITE attempt's stamp.
func (prx *Proxy) InterceptOutboundRequest(
	ctx context.Context,
	next sip.RequestSender,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	owner, ok := txOwnerFromCtx(ctx)
	if !ok || owner.srv || owner.rq == nil || owner.rq.prx != prx {
		return errors.Wrap(next.SendRequest(ctx, req, opts...))
	}

	rq := owner.rq
	var md Metadata
	stamp := false
	rq.mu.Lock()
	if !rq.closed && rq.elm != nil {
		if elm, ok := sip.ElementFromContext(ctx); ok && elm == rq.elm {
			if key, err := sip.MakeClientTransactionKey(req); err == nil {
				if rec := rq.attempts[key]; rec != nil && rec.targetID == owner.targetID && rec.gen == owner.gen {
					md = rq.stageMetaLocked(StageForward, rec.targetID, rec.attID)
					stamp = true
				}
			}
		}
	}
	rq.mu.Unlock()

	if stamp {
		stampForward(req, md)
	}
	return errors.Wrap(next.SendRequest(ctx, req, opts...))
}

// registerSrvReq indexes an owned request by its upstream transaction
// key inside the request's element namespace. A registration racing the
// lifecycle seal is refused and reports false: the caller's post-admission
// seal check retires the coordinator instead of leaving it indexed past
// the close snapshot.
func (prx *Proxy) registerSrvReq(key sip.ServerTransactionKey, rq *Request) bool {
	es := prx.getElemState(rq.elm, true)
	if es == nil {
		return false
	}
	es.reqsMu.Lock()
	defer es.reqsMu.Unlock()
	if prx.closed.Load() {
		return false
	}
	es.srvRqs[key] = rq
	return true
}

// unregisterSrvReq removes the request index entry from the request's
// element namespace if it still names this coordinator.
func (prx *Proxy) unregisterSrvReq(key sip.ServerTransactionKey, rq *Request) {
	es := prx.getElemState(rq.elm, false)
	if es == nil {
		return
	}
	es.reqsMu.Lock()
	if cur, ok := es.srvRqs[key]; ok && cur == rq {
		delete(es.srvRqs, key)
	}
	es.reqsMu.Unlock()
}

// loadSrvReq returns the coordinator associated with an upstream
// transaction key inside the element's namespace. A nil or unseen element
// reports no association and never allocates a scope.
func (prx *Proxy) loadSrvReq(elm *sip.Element, key sip.ServerTransactionKey) (*Request, bool) {
	es := prx.getElemState(elm, false)
	if es == nil {
		return nil, false
	}
	es.reqsMu.Lock()
	defer es.reqsMu.Unlock()
	rq, ok := es.srvRqs[key]
	return rq, ok
}

// shutdown retires the coordinator at the proxy lifecycle end. Queued
// decisions and every later enqueue are dropped, armed Timer C deadlines
// disarm, lazy address runners release, the staged transaction
// subscriptions detach, and every still-live owned transaction
// terminates — which also stops its transaction-owned timers, including a
// pending Trying. Every unbind and transaction effect runs outside the
// coordinator guard, so an in-flight drain or observer dispatch already
// past the guard finishes undisturbed and a reentrant unbind cannot
// deadlock.
func (rq *Request) shutdown(ctx context.Context) {
	// Retirement effects run under the request's own processing element,
	// never under whichever element the lifecycle context happened to
	// carry: one instance-wide shutdown retires requests claimed on
	// different elements, and transaction callbacks must still resolve
	// the element that claimed them.
	ctx = sip.ContextWithElement(ctx, rq.elm)

	rq.mu.Lock()
	if rq.closed {
		rq.mu.Unlock()
		return
	}
	rq.closed = true
	rq.queue = nil

	rq.disarmAllTmrsLocked()
	var (
		stops   []func()
		clnTxs  []sip.ClientTransaction
		srvTx   = rq.srvTx
		srvGone = rq.srvGone
		srvKey  = rq.srvKey
		unbinds = rq.unbinds
		srvMD   = rq.stageMetaLocked(StageUpstream, "", "")
	)
	rq.unbinds = nil
	for _, st := range rq.targets {
		if rn := st.runner; rn != nil {
			st.runner = nil
			stops = append(stops, rn.stop)
		}
	}
	// Every collected branch transaction keeps the forward-stage stamp its
	// send boundary owned, so a retirement failure stays correlated to the
	// attempt — or, for a still-pending admission, to its target.
	var txMDs []Metadata
	for _, att := range rq.attempts {
		if att.tx != nil {
			clnTxs = append(clnTxs, att.tx)
			txMDs = append(txMDs, rq.stageMetaLocked(StageForward, att.targetID, att.attID))
		}
	}
	// A still-pending client-transaction admission owns its transaction the
	// same way a committed attempt does: it must terminate here instead of
	// lingering in the element manager until its own timeout.
	for _, adm := range rq.pendingAdms {
		if adm.tx != nil {
			clnTxs = append(clnTxs, adm.tx)
			txMDs = append(txMDs, rq.stageMetaLocked(StageForward, adm.targetID, ""))
		}
	}
	clear(rq.pendingAdms)
	rq.mu.Unlock()

	for _, stop := range stops {
		stop()
	}
	for _, unbind := range unbinds {
		unbind()
	}
	for i, tx := range clnTxs {
		if err := tx.Terminate(ctx, nil); err != nil {
			rq.prx.logger(ctx).DebugContext(ctx, "proxy branch transaction retirement failed",
				slog.Any("proxy", txMDs[i]),
				slog.Any("error", err),
			)
		}
	}
	if srvTx != nil && !srvGone {
		if err := srvTx.Terminate(ctx, nil); err != nil {
			rq.prx.logger(ctx).DebugContext(ctx, "proxy upstream transaction retirement failed",
				slog.Any("proxy", srvMD),
				slog.Any("error", err),
			)
		}
	}
	// The transaction subscriptions already detached, so no lifecycle
	// event can release the index entry: it is released eagerly here.
	if srvKey.IsValid() {
		rq.prx.unregisterSrvReq(srvKey, rq)
	}
}
