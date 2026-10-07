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
	*serverTransact

	tmrJ atomic.Pointer[timeutil.Timer]
}

var _ ServerTransaction = (*NonInviteServerTransaction)(nil)

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

	o := util.LastSliceElemOr(opts, ServerTransactionOptions{})

	tx := new(NonInviteServerTransaction)
	srvTx, err := newServerTransact(TransactionTypeServerNonInvite, tx, req, tp, o)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	tx.serverTransact = srvTx
	if err := tx.initFSM(TransactionStateTrying); err != nil {
		return nil, errors.Wrap(err)
	}

	return tx, nil
}

// Start activates the non-INVITE server transaction.
// For a new transaction it runs the initial trying action.
// For a restored transaction it only activates the preserved timers without
// replaying entry actions.
// It must be called exactly once after the transaction is created or restored.
func (tx *NonInviteServerTransaction) Start(ctx context.Context) error {
	if !tx.admitStart(ctx) {
		return errors.Wrap(NewTransactionActionNotAllowedError())
	}
	defer tx.finishStart()
	if tx.isTerminated() {
		return errors.Wrap(NewTransactionActionNotAllowedError())
	}

	var err error
	if tx.restored {
		tx.activateRestored(ctx)
	} else {
		err = tx.actTrying(ctx)
		tx.activate(ctx)
	}

	return errors.Wrap(err)
}

func (tx *NonInviteServerTransaction) activateRestored(ctx context.Context) {
	tx.lcMu.Lock()

	if tmr := tx.tmrJ.Load(); tmr != nil && tmr.Expired() &&
		tx.State() == TransactionStateCompleted && !tx.isTerminated() {
		tx.lcMu.Unlock()
		tx.onTimerJ(ctx, tmr)
		tx.activate(ctx)
		return
	}

	if !tx.isTerminated() {
		tx.armTmrLocked(&tx.tmrJ, tx.tmrJ.Load(), []TransactionState{TransactionStateCompleted},
			func(tmr *timeutil.Timer) { tx.onTimerJ(ctx, tmr) })
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

const txEvtTimerJ = "timer_J"

func (tx *NonInviteServerTransaction) initFSM(start TransactionState) error {
	if err := tx.serverTransact.initFSM(start); err != nil {
		return errors.Wrap(err)
	}

	// TODO: RFC 6026 Section 8.8: the server transaction SHOULD
	// inform the TU that a transport failure has occurred,
	// and MUST remain in the current state.
	// Not clear should this be applied to also non-INVITE transaction...

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
		Permit(txEvtSend2xx, TransactionStateCompleted).
		Permit(txEvtSend300699, TransactionStateCompleted).
		Permit(txEvtTranspErr, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateCompleted).
		OnEntry(tx.actCompleted).
		OnEntryFrom(txEvtSend2xx, tx.actSendRes).
		OnEntryFrom(txEvtSend300699, tx.actSendRes).
		InternalTransition(txEvtRecvReq, tx.actResendRes).
		InternalTransition(txEvtSend2xx, tx.actNoop).
		InternalTransition(txEvtSend300699, tx.actNoop).
		Permit(txEvtTimerJ, TransactionStateTerminated).
		Permit(txEvtTranspErr, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateTerminated).
		OnEntry(tx.actTerminated).
		OnEntryFrom(txEvtTranspErr, tx.actTranspErr).
		OnEntryFrom(txEvtTerminate, tx.actTermErr).
		InternalTransition(txEvtTerminate, tx.actNoop)

	return nil
}

//nolint:unparam
func (tx *NonInviteServerTransaction) actTrying(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction trying", slog.Any("transaction", tx))

	return nil
}

func (tx *NonInviteServerTransaction) actCompleted(ctx context.Context, args ...any) error {
	_ = tx.serverTransact.actCompleted(ctx, args...)

	var timeJ time.Duration
	if !tx.tp.Metadata().Reliable() {
		timeJ = tx.timing.TimeJ()
	}
	tmr := timeutil.NewTimer(timeJ)
	if tx.armTmr(&tx.tmrJ, tmr, []TransactionState{TransactionStateCompleted},
		func(tmr *timeutil.Timer) { tx.onTimerJ(ctx, tmr) }) {
		tx.log.LogAttrs(
			ctx, slog.LevelDebug, "timer J started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	return nil
}

func (tx *NonInviteServerTransaction) onTimerJ(ctx context.Context, tmr *timeutil.Timer) {
	defer tx.saveSnapshot()
	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer J expired", slog.Any("transaction", tx))

	tx.clearTmr(&tx.tmrJ, tmr)
	tx.fireTmrTrigger(ctx, txEvtTimerJ)
}

func (tx *NonInviteServerTransaction) actTerminated(ctx context.Context, args ...any) error {
	_ = tx.serverTransact.actTerminated(ctx, args...)

	if tx.stopTmr(&tx.tmrJ) {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer J stopped", slog.Any("transaction", tx))
	}

	return nil
}

func (tx *NonInviteServerTransaction) takeSnapshot() *ServerTransactionSnapshot {
	return &ServerTransactionSnapshot{
		Time:         time.Now(),
		Type:         tx.Type(),
		State:        tx.State(),
		Key:          tx.Key(),
		Request:      tx.Request(),
		LastResponse: tx.LastResponse(),
		SendOptions:  tx.sendOpts.Load().(SendResponseOptions), //nolint:forcetypeassert
		Timing:       tx.timing,
		TimerJ:       tx.tmrJ.Load().Snapshot(),
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

	o := util.LastSliceElemOr(opts, ServerTransactionOptions{})
	o.Timing = snap.Timing

	req := snap.Request.Clone().(*RequestEnvelope) //nolint:forcetypeassert

	tx := new(NonInviteServerTransaction)
	srvTx, err := newServerTransact(TransactionTypeServerNonInvite, tx, req, tp, o)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	tx.serverTransact = srvTx
	if snap.LastResponse != nil {
		tx.lastRes.Store(snap.LastResponse.Clone().(*ResponseEnvelope)) //nolint:forcetypeassert
	}
	tx.sendOpts.Store(snap.SendOptions)
	if err := tx.initFSM(snap.State); err != nil {
		return nil, errors.Wrap(err)
	}
	tx.restored = true

	if err := tx.restoreTimers(snap); err != nil {
		return nil, errors.Wrap(err)
	}
	tx.saveSnapshot()

	return tx, nil
}

func (tx *NonInviteServerTransaction) restoreTimers(snap *ServerTransactionSnapshot) error {
	if snap.TimerJ == nil {
		return nil
	}

	restored, err := timeutil.RestoreTimer(snap.TimerJ)
	if err != nil {
		return errors.Wrap(err)
	}
	tx.tmrJ.Store(restored)

	return nil
}
