package sip

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/timeutil"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

// NonInviteServerTransaction represents a non-invite server transaction.
type NonInviteServerTransaction struct {
	*baseSrvTransact

	tmrJ      atomic.Pointer[timeutil.Timer]
	tmrTrying atomic.Pointer[timeutil.Timer]

	// behavior is the response-policy profile frozen at
	// construction, see [ServerTransactionOptions.NonInviteServerTransactionBehavior].
	behavior NonInviteServerTransactionBehavior
	// tryingDeadline is the absolute RFC 4320 delayed-Trying eligibility
	// deadline shared by response validation and the delayed Trying timer.
	// It is assigned at Start before activation and read-only afterwards.
	tryingDeadline time.Time
}

var (
	_ ServerTransaction                          = (*NonInviteServerTransaction)(nil)
	_ ServerTransactionSnapshotter               = (*NonInviteServerTransaction)(nil)
	_ NonInviteServerTransactionBehaviorProvider = (*NonInviteServerTransaction)(nil)
)

// NewNonInviteServerTransaction creates an initialized non-INVITE server transaction.
//
// Request expected to be a valid SIP request with any method except INVITE or ACK.
// Transport expected to be a non-nil server transport.
// Options are optional and can be nil, in which case default options will be used.
// Transaction key will be filled from the request automatically if not specified in the options.
func NewNonInviteServerTransaction(
	req *RequestEnvelope,
	tp ServerTransport,
	opts ...ServerTransactionOptions,
) (*NonInviteServerTransaction, error) {
	if err := req.Validate(); err != nil {
		return nil, errors.Wrap(err)
	}

	if mtd := req.Method(); mtd.Equal(RequestMethodInvite) || mtd.Equal(RequestMethodAck) {
		return nil, errors.Wrap(NewRequestMethodNotAllowedError())
	}

	o := util.LastSliceElem(opts, ServerTransactionOptions{})
	if !o.NonInviteServerTransactionBehavior.supported() {
		return nil, errors.ErrorfWrap("invalid non-INVITE server transaction behavior %d", o.NonInviteServerTransactionBehavior)
	}

	tx := new(NonInviteServerTransaction)
	tx.behavior = o.NonInviteServerTransactionBehavior
	base, err := newBaseSrvTransact(TransactionTypeServerNonInvite, tx, req, tp, o)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	tx.baseSrvTransact = base
	if err := tx.initFSM(TransactionStateTrying); err != nil {
		return nil, errors.Wrap(err)
	}

	return tx, nil
}

// NonInviteServerTransactionBehavior returns the behavior profile selected at construction.
// The value is frozen before Start and stays readable while the transaction
// is dormant, including a pre-Start transaction restored from a snapshot.
func (tx *NonInviteServerTransaction) NonInviteServerTransactionBehavior() NonInviteServerTransactionBehavior {
	if tx == nil {
		return NonInviteServerTransactionBehaviorLegacy
	}
	return tx.behavior
}

// Start activates the non-INVITE server transaction.
// For a new transaction it runs the initial trying action.
// For a restored transaction it only activates the preserved timers without
// replaying entry actions.
// It must be called exactly once after the transaction is created or restored.
func (tx *NonInviteServerTransaction) Start(ctx context.Context) error {
	ctx = ContextWithTransaction(ctx, tx.impl)

	if !tx.admitStart(ctx) {
		return errors.Wrap(NewTransactionActionNotAllowedError())
	}

	defer tx.finishStart(ctx)

	if tx.isTerminated() {
		return errors.Wrap(NewTransactionActionNotAllowedError())
	}

	if tx.behavior == NonInviteServerTransactionBehaviorRFC4320 && tx.tryingDeadline.IsZero() {
		delay, derr := tx.timing.nonInviteTryingDelay()
		if derr != nil {
			return errors.Wrap(derr)
		}
		// The deadline is assigned before activation so serialized response
		// validation observes it; it stays read-only afterwards. A restored
		// transaction already carries the absolute deadline preserved by its
		// snapshot, so only a new transaction computes a fresh delay.
		tx.tryingDeadline = time.Now().Add(delay)
	}

	var err error
	if tx.restored {
		tx.activateRestored(ctx)
	} else {
		err = tx.actTrying(ctx)
		tx.activate(ctx)
	}
	if err == nil {
		// The timer is armed after activation so an immediately eligible
		// deadline (T1 >= T2) cannot dispatch a 100 before the transaction
		// is active.
		tx.armTmrTrying(ctx)
	}

	return errors.Wrap(err)
}

func (tx *NonInviteServerTransaction) activateRestored(ctx context.Context) {
	tx.lcMu.Lock()

	if tmr := tx.tmrJ.Load(); tmr != nil && tmr.Expired() &&
		tx.State() == TransactionStateCompleted && !tx.isTerminated() {
		tx.lcMu.Unlock()
		tx.onTmrJ(ctx, tmr)
		tx.activate(ctx)
		return
	}

	if !tx.isTerminated() {
		tx.armTmrLocked(
			&tx.tmrJ, tx.tmrJ.Load(),
			[]TransactionState{TransactionStateCompleted},
			func(tmr *timeutil.Timer) { tx.onTmrJ(ctx, tmr) },
		)
	}
	tx.activateLocked()
	tx.lcMu.Unlock()
}

func (tx *NonInviteServerTransaction) LogValue() slog.Value {
	if tx == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.String("ptr", fmt.Sprintf("%p", tx)),
		slog.Any("key", tx.key),
		slog.Any("type", tx.typ),
		slog.Any("state", tx.State()),
	)
}

const txEvtTmrJ = "timer_J"

func (tx *NonInviteServerTransaction) initFSM(start TransactionState) error {
	if err := tx.baseSrvTransact.initFSM(start); err != nil {
		return errors.Wrap(err)
	}

	// RFC 6026 Section 8.8 replaces RFC 3261 Section 17.2.4's transport-error
	// rule. Apply it to non-INVITE transactions despite the unchanged Figure 8:
	// retain the current state to absorb retransmissions and reuse the cached
	// response. Final responses enter Completed and start Timer J regardless of
	// send success; the transport error must not restart Timer J.

	tx.fsm.Configure(TransactionStateTrying).
		InternalTransition(txEvtRecvReq, tx.actNoop).
		Permit(txEvtSend1xx, TransactionStateProceeding).
		Permit(txEvtSend2xx, TransactionStateCompleted).
		Permit(txEvtSend300699, TransactionStateCompleted).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateProceeding).
		OnEntry(tx.actProceeding).
		OnEntryFrom(txEvtSend1xx, tx.actSendRes).
		InternalTransition(txEvtRecvReq, tx.actResendRes).
		InternalTransition(txEvtSend1xx, tx.actSendRes).
		InternalTransition(txEvtTpErr, tx.actTranspErr).
		Permit(txEvtSend2xx, TransactionStateCompleted).
		Permit(txEvtSend300699, TransactionStateCompleted).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateCompleted).
		OnEntry(tx.actCompleted).
		OnEntryFrom(txEvtSend2xx, tx.actSendRes).
		OnEntryFrom(txEvtSend300699, tx.actSendRes).
		InternalTransition(txEvtRecvReq, tx.actResendRes).
		InternalTransition(txEvtSend2xx, tx.actNoop).
		InternalTransition(txEvtSend300699, tx.actNoop).
		InternalTransition(txEvtTpErr, tx.actTranspErr).
		Permit(txEvtTmrJ, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateTerminated).
		OnEntry(tx.actTerminated).
		OnEntryFrom(txEvtTerminate, tx.actTermErr).
		InternalTransition(txEvtTerminate, tx.actNoop)

	return nil
}

//nolint:unparam // uniform signature with the other FSM actions
func (tx *NonInviteServerTransaction) actTrying(ctx context.Context, _ ...any) error {
	tx.log.DebugContext(ctx, "transaction trying", slog.Any("transaction", tx))

	return nil
}

func (tx *NonInviteServerTransaction) actCompleted(ctx context.Context, args ...any) error {
	_ = tx.baseSrvTransact.actCompleted(ctx, args...)

	var timeJ time.Duration
	if !tx.tp.Metadata().Reliable() {
		timeJ = tx.timing.TimeJ()
	}
	tmr := timeutil.NewTimer(timeJ)
	if tx.armTmr(
		&tx.tmrJ, tmr,
		[]TransactionState{TransactionStateCompleted},
		func(tmr *timeutil.Timer) { tx.onTmrJ(ctx, tmr) },
	) {
		tx.log.DebugContext(ctx, "timer J started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	return nil
}

func (tx *NonInviteServerTransaction) onTmrJ(ctx context.Context, tmr *timeutil.Timer) {
	tx.beginSnapshotUpdate()
	defer tx.finishSnapshotUpdate(ctx)

	tx.log.DebugContext(ctx, "timer J expired", slog.Any("transaction", tx))

	tx.clearTmr(&tx.tmrJ, tmr)
	tx.fireTmrTrigger(ctx, txEvtTmrJ)
}

// actSendRes stops the pending delayed Trying timer: any sent response, an
// explicit earlier 100 on reliable transport or a final response, cancels
// the pending automatic Trying.
func (tx *NonInviteServerTransaction) actSendRes(ctx context.Context, args ...any) error {
	if tx.stopTmr(&tx.tmrTrying) {
		tx.log.DebugContext(ctx, "delayed trying timer stopped", slog.Any("transaction", tx))
	}
	return errors.Wrap(tx.baseSrvTransact.actSendRes(ctx, args...))
}

func (tx *NonInviteServerTransaction) actTerminated(ctx context.Context, args ...any) error {
	_ = tx.baseSrvTransact.actTerminated(ctx, args...)

	if tx.stopTmr(&tx.tmrTrying) {
		tx.log.DebugContext(ctx, "delayed trying timer stopped", slog.Any("transaction", tx))
	}

	if tx.stopTmr(&tx.tmrJ) {
		tx.log.DebugContext(ctx, "timer J stopped", slog.Any("transaction", tx))
	}

	return nil
}

func (tx *NonInviteServerTransaction) takeSnapshot() *ServerTransactionSnapshot {
	lastRes := tx.LastResponse()
	return &ServerTransactionSnapshot{
		Time:                               time.Now(),
		Type:                               tx.Type(),
		State:                              tx.State(),
		Key:                                tx.Key(),
		Request:                            tx.Request(),
		LastResponse:                       lastRes,
		SendOptions:                        tx.sendOpts.Load().(SendResponseOptions), //nolint:forcetypeassert // the concrete type is fixed by construction
		PreserveResponseToTag:              resPreservesToTag(lastRes),
		Timing:                             tx.timing,
		NonInviteServerTransactionBehavior: tx.behavior,
		TimerJ:                             tx.tmrJ.Load().Snapshot(),
		TimerTrying:                        tx.tmrTrying.Load().Snapshot(),
	}
}

// RestoreNonInviteServerTransaction restores a dormant non-INVITE server
// transaction from a snapshot.
//
// The snapshot contains the serialized state of the transaction; its request
// and last response are cloned, so the caller keeps ownership of the snapshot.
// Transport is required to send responses.
// Options are optional and can be nil; timing and send options are taken from
// the snapshot.
//
// The restored transaction is suspended and not activated: timers are restored
// without callbacks, no entry actions or sends are replayed, and no state or
// response events are emitted. [Start] activates the preserved timer deadlines.
// A terminated snapshot can be rehydrated for inspection only, it can be
// neither started nor registered.
func RestoreNonInviteServerTransaction(
	snap *ServerTransactionSnapshot,
	tp ServerTransport,
	opts ...ServerTransactionOptions,
) (*NonInviteServerTransaction, error) {
	// Timers are restored without callbacks and activated by Start.
	if err := snap.validate(TransactionTypeServerNonInvite); err != nil {
		return nil, errors.Wrap(err)
	}

	o := util.LastSliceElem(opts, ServerTransactionOptions{})
	o.Timing = snap.Timing

	req := snap.Request.Clone().(*RequestEnvelope) //nolint:forcetypeassert // the concrete type is fixed by construction

	tx := new(NonInviteServerTransaction)
	// The saved mode wins over the creation options, like Timing: a snapshot
	// without the field restores as Legacy.
	tx.behavior = snap.NonInviteServerTransactionBehavior
	base, err := newBaseSrvTransact(TransactionTypeServerNonInvite, tx, req, tp, o)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	tx.baseSrvTransact = base
	if snap.LastResponse != nil {
		lastRes := snap.LastResponse.Clone().(*ResponseEnvelope) //nolint:forcetypeassert // the concrete type is fixed by construction
		// Merge the legacy preservation scalar into envelope metadata before
		// Start can resend the cached response; false never clears provenance.
		setResPreserveToTag(lastRes, snap.PreserveResponseToTag)
		tx.lastRes.Store(lastRes)
	}
	tx.sendOpts.Store(snap.SendOptions)
	if err := tx.initFSM(snap.State); err != nil {
		return nil, errors.Wrap(err)
	}
	tx.restored = true

	if err := tx.restoreTmrs(snap); err != nil {
		return nil, errors.Wrap(err)
	}
	tx.saveSnapshot()

	return tx, nil
}

func (tx *NonInviteServerTransaction) restoreTmrs(snap *ServerTransactionSnapshot) error {
	for dst, tmrSnap := range map[*atomic.Pointer[timeutil.Timer]]*timeutil.TimerSnapshot{
		&tx.tmrJ:      snap.TimerJ,
		&tx.tmrTrying: snap.TimerTrying,
	} {
		if tmrSnap == nil {
			continue
		}

		restored, err := timeutil.RestoreTimer(tmrSnap)
		if err != nil {
			return errors.Wrap(err)
		}
		dst.Store(restored)
	}

	if snap.TimerTrying != nil {
		// Preserve the absolute delayed-Trying deadline so Start does not
		// restart a fresh delay for the restored transaction.
		tx.tryingDeadline = snap.TimerTrying.StartTime.Add(snap.TimerTrying.Duration)
	}

	return nil
}

// validateNonInviteRes enforces the RFC 4320 response restrictions when
// the RFC 4320 behavior profile is selected: no provisional responses other
// than 100 Trying, no 408 Request Timeout, and no early 100 Trying over
// unreliable transport (reliable transport permits an explicit earlier 100).
// Final responses are unaffected.
//
// It runs inside the transaction lifecycle serialization before the response
// reaches the FSM transition, so a rejected response is neither sent nor
// mutates transaction state. It is a no-op for INVITE transactions and for
// the Legacy profile.
func (tx *baseSrvTransact) validateNonInviteRes(sts ResponseStatus, reliable bool, now time.Time) error {
	ntx, ok := tx.impl.(*NonInviteServerTransaction)
	if !ok || ntx.behavior != NonInviteServerTransactionBehaviorRFC4320 {
		return nil
	}

	switch {
	case sts == ResponseStatusTrying && !reliable && now.Before(ntx.tryingDeadline):
		// RFC 4320 section 2: over unreliable transport a 100 Trying is
		// suppressed until client Timer E backoff would reach T2.
		return errors.Wrap(NewTransactionActionNotAllowedError("early %q response over unreliable transport", sts))
	case sts != ResponseStatusTrying && sts.IsProvisional():
		// RFC 4320 section 2: a non-INVITE UAS must not send provisional
		// responses other than 100 Trying.
		return errors.Wrap(NewTransactionActionNotAllowedError("%q provisional response not allowed", sts))
	case sts == ResponseStatusRequestTimeout:
		// RFC 4320 section 2: a non-INVITE UAS must not send a 408 Request
		// Timeout response.
		return errors.Wrap(NewTransactionActionNotAllowedError("%q response not allowed", sts))
	}
	return nil
}

// armTmrTrying arms the pending RFC 4320 delayed Trying timer at the
// eligibility deadline computed by [NonInviteServerTransaction.Start]. It is
// a no-op in the Legacy behavior profile and once a response has been sent:
// an explicit earlier 100 on reliable transport or any sent response cancels
// the pending automatic Trying.
func (tx *NonInviteServerTransaction) armTmrTrying(ctx context.Context) {
	if tx.behavior != NonInviteServerTransactionBehaviorRFC4320 || tx.lastRes.Load() != nil {
		return
	}

	// A restored transaction reuses the timer preserved by its snapshot,
	// including an already-expired one that fires immediately. A new
	// transaction is armed from the shared absolute deadline rather than
	// recomputing the delay; time.Until saturates at the maximum Duration,
	// so a far-future deadline cannot wrap into an immediate fire.
	tmr := tx.tmrTrying.Load()
	if tmr == nil {
		tmr = timeutil.NewTimer(time.Until(tx.tryingDeadline))
	}
	if tx.armTmr(
		&tx.tmrTrying, tmr,
		[]TransactionState{TransactionStateTrying},
		func(tmr *timeutil.Timer) { tx.onTmrTrying(ctx, tmr) },
	) {
		tx.log.DebugContext(ctx, "delayed trying timer started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}
}

func (tx *NonInviteServerTransaction) onTmrTrying(ctx context.Context, tmr *timeutil.Timer) {
	tx.beginSnapshotUpdate()
	defer tx.finishSnapshotUpdate(ctx)

	tx.log.DebugContext(ctx, "delayed trying timer expired", slog.Any("transaction", tx))

	tx.clearTmr(&tx.tmrTrying, tmr)

	res, err := tx.req.NewResponse(ResponseStatusTrying)
	if err != nil {
		// Request is always valid, so this should never happen.
		panic(errors.ErrorfWrap("create auto %q response: %w", ResponseStatusTrying, err))
	}

	// The 100 is dispatched through the same serialized lifecycle as
	// application sends, so validation and the FSM transition make a queued
	// Trying unhandled after a racing final response instead of emitting a
	// post-final 100. A not-allowed rejection means the response is simply
	// no longer eligible and is skipped.
	if err := tx.SendResponse(ctx, res); err != nil && !errors.Is(err, ErrTransactionActionNotAllowed) {
		panic(errors.Wrap(err))
	}
}

// NonInviteServerTransactionBehaviorProvider is an optional interface implemented by the
// built-in non-INVITE server transaction to expose its selected behavior
// profile, see [ServerTransactionOptions.NonInviteServerTransactionBehavior].
//
// The reported mode is frozen before the transaction starts and stays
// readable while the transaction is dormant, including a transaction restored
// from a snapshot that [ServerTransactionSnapshotter.Snapshot] cannot inspect
// yet because it refuses nonterminal dormant transactions.
//
// It is intentionally not a part of [ServerTransaction], so custom
// transaction implementations are not required to support it; use a type
// assertion to detect it. Custom bindings without the provider cannot recover
// a non-INVITE mode and fail explicitly unsupported; ordinary custom
// forwarding is unaffected.
type NonInviteServerTransactionBehaviorProvider interface {
	// NonInviteServerTransactionBehavior returns the frozen non-INVITE behavior profile.
	NonInviteServerTransactionBehavior() NonInviteServerTransactionBehavior
}

// NonInviteServerTransactionBehavior selects the behavior profile of a non-INVITE server
// transaction.
type NonInviteServerTransactionBehavior int

const (
	// NonInviteServerTransactionBehaviorLegacy preserves the existing non-INVITE transaction
	// validation, response, and timer behavior. It is the zero/default profile.
	NonInviteServerTransactionBehaviorLegacy NonInviteServerTransactionBehavior = iota
	// NonInviteServerTransactionBehaviorRFC4320 explicitly enables the RFC 4320 actions for
	// non-INVITE server transactions: the restricted provisional/408 response
	// policy and the automatic delayed Trying response.
	NonInviteServerTransactionBehaviorRFC4320
)

// supported reports whether the value names a selectable behavior profile.
func (b NonInviteServerTransactionBehavior) supported() bool {
	switch b {
	case NonInviteServerTransactionBehaviorLegacy, NonInviteServerTransactionBehaviorRFC4320:
		return true
	default:
		return false
	}
}
