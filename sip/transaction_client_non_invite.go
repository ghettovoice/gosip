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

// NonInviteClientTransaction represents a SIP client transaction for non-INVITE requests.
// It implements the client transaction FSM defined in RFC 3261 section 17.1.2.
type NonInviteClientTransaction struct {
	*clientTransact

	tmrE atomic.Pointer[timeutil.Timer]
	tmrF atomic.Pointer[timeutil.Timer]
	tmrK atomic.Pointer[timeutil.Timer]
}

var _ ClientTransaction = (*NonInviteClientTransaction)(nil)

// NewNonInviteClientTransaction creates an initialized non-INVITE client transaction.
func NewNonInviteClientTransaction(
	req *RequestEnvelope,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (*NonInviteClientTransaction, error) {
	if err := req.Validate(); err != nil {
		return nil, errors.Wrap(err)
	}
	if mtd := req.Method(); mtd.Equal(RequestMethodInvite) || mtd.Equal(RequestMethodAck) {
		return nil, errors.Wrap(NewRequestMethodNotAllowedError())
	}

	o := util.LastSliceElemOr(opts, ClientTransactionOptions{})

	tx := new(NonInviteClientTransaction)
	clnTx, err := newClientTransact(TransactionTypeClientNonInvite, tx, req, tp, o)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	tx.clientTransact = clnTx
	if err := tx.initFSM(TransactionStateTrying); err != nil {
		return nil, errors.Wrap(err)
	}

	return tx, nil
}

// Start activates the non-INVITE client transaction.
// For a new transaction it sends the initial request and starts timers.
// For a restored transaction it only activates the preserved timers without
// replaying the initial send or entry actions.
// It must be called exactly once after the transaction is created or restored.
func (tx *NonInviteClientTransaction) Start(ctx context.Context) error {
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
		tx.activate(ctx)
		if tx.isActive() {
			err = tx.actTrying(ctx)
		} else {
			err = errors.Wrap(NewTransactionActionNotAllowedError())
		}
	}

	return errors.Wrap(err)
}

func (tx *NonInviteClientTransaction) activateRestored(ctx context.Context) {
	tx.lcMu.Lock()

	states := []TransactionState{TransactionStateTrying, TransactionStateProceeding}
	if tmr := tx.tmrF.Load(); tmr != nil && tmr.Expired() &&
		isTxStateIn(tx.State(), states...) && !tx.isTerminated() {
		tx.lcMu.Unlock()
		tx.onTimerF(ctx, tmr)
		tx.activate(ctx)
		return
	}
	if tmr := tx.tmrK.Load(); tmr != nil && tmr.Expired() &&
		tx.State() == TransactionStateCompleted && !tx.isTerminated() {
		tx.lcMu.Unlock()
		tx.onTimerK(ctx, tmr)
		tx.activate(ctx)
		return
	}

	if !tx.isTerminated() {
		tx.armTmrLocked(&tx.tmrE, tx.tmrE.Load(), states,
			func(tmr *timeutil.Timer) { tx.onTimerE(ctx, tmr) })
		tx.armTmrLocked(&tx.tmrF, tx.tmrF.Load(), states,
			func(tmr *timeutil.Timer) { tx.onTimerF(ctx, tmr) })
		tx.armTmrLocked(&tx.tmrK, tx.tmrK.Load(), []TransactionState{TransactionStateCompleted},
			func(tmr *timeutil.Timer) { tx.onTimerK(ctx, tmr) })
	}
	tx.activateLocked()
	tx.lcMu.Unlock()
}

func (tx *NonInviteClientTransaction) LogValue() slog.Value {
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

const (
	txEvtTimerE = "timer_e"
	txEvtTimerF = "timer_f"
	txEvtTimerK = "timer_k"
)

func (tx *NonInviteClientTransaction) initFSM(start TransactionState) error {
	if err := tx.clientTransact.initFSM(start); err != nil {
		return errors.Wrap(err)
	}

	tx.fsm.Configure(TransactionStateTrying).
		InternalTransition(txEvtTimerE, tx.actSendReq).
		Permit(txEvtRecv1xx, TransactionStateProceeding).
		Permit(txEvtRecv2xx, TransactionStateCompleted).
		Permit(txEvtRecv300699, TransactionStateCompleted).
		Permit(txEvtTimerF, TransactionStateTerminated).
		Permit(txEvtTranspErr, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateProceeding).
		OnEntry(tx.actProceeding).
		OnEntryFrom(txEvtRecv1xx, tx.actPassRes).
		InternalTransition(txEvtTimerE, tx.actSendReq).
		InternalTransition(txEvtRecv1xx, tx.actPassRes).
		Permit(txEvtRecv2xx, TransactionStateCompleted).
		Permit(txEvtRecv300699, TransactionStateCompleted).
		Permit(txEvtTimerF, TransactionStateTerminated).
		Permit(txEvtTranspErr, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateCompleted).
		OnEntry(tx.actCompleted).
		OnEntryFrom(txEvtRecv2xx, tx.actPassRes).
		OnEntryFrom(txEvtRecv300699, tx.actPassRes).
		Permit(txEvtTimerK, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateTerminated).
		OnEntry(tx.actTerminated).
		OnEntryFrom(txEvtTimerF, tx.actTimedOut).
		OnEntryFrom(txEvtTranspErr, tx.actTranspErr).
		OnEntryFrom(txEvtTerminate, tx.actTermErr).
		InternalTransition(txEvtTerminate, tx.actNoop)

	return nil
}

func (tx *NonInviteClientTransaction) actTrying(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction trying", slog.Any("transaction", tx))

	if err := tx.sendReq(ctx, tx.req); err != nil {
		return errors.Wrap(err)
	}

	states := []TransactionState{TransactionStateTrying, TransactionStateProceeding}

	if !tx.tp.Metadata().Reliable() {
		tmr := timeutil.NewTimer(tx.timing.TimeE())
		if tx.armTmr(&tx.tmrE, tmr, states, func(tmr *timeutil.Timer) { tx.onTimerE(ctx, tmr) }) {
			tx.log.LogAttrs(
				ctx, slog.LevelDebug, "timer E started",
				slog.Any("transaction", tx),
				slog.Time("expires_at", time.Now().Add(tmr.Left())),
			)
		}
	}

	tmr := timeutil.NewTimer(tx.timing.TimeF())
	if tx.armTmr(&tx.tmrF, tmr, states, func(tmr *timeutil.Timer) { tx.onTimerF(ctx, tmr) }) {
		tx.log.LogAttrs(
			ctx, slog.LevelDebug, "timer F started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	return nil
}

func (tx *NonInviteClientTransaction) onTimerE(ctx context.Context, tmr *timeutil.Timer) {
	defer tx.saveSnapshot()
	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer E expired", slog.Any("transaction", tx))

	tx.fireTmrTrigger(ctx, txEvtTimerE)

	var dur time.Duration
	if tx.State() == TransactionStateTrying {
		dur = min(2*tmr.Duration(), tx.timing.t2())
	} else {
		dur = tx.timing.t2()
	}
	if tx.resetTmr(&tx.tmrE, tmr, dur, TransactionStateTrying, TransactionStateProceeding) {
		tx.log.LogAttrs(
			ctx, slog.LevelDebug, "timer E reset",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}
}

func (tx *NonInviteClientTransaction) onTimerF(ctx context.Context, tmr *timeutil.Timer) {
	defer tx.saveSnapshot()
	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer F expired", slog.Any("transaction", tx))

	tx.clearTmr(&tx.tmrF, tmr)
	tx.fireTmrTrigger(ctx, txEvtTimerF)
}

func (tx *NonInviteClientTransaction) actCompleted(ctx context.Context, args ...any) error {
	_ = tx.clientTransact.actCompleted(ctx, args...)

	if tx.stopTmr(&tx.tmrE) {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer E stopped", slog.Any("transaction", tx))
	}

	if tx.stopTmr(&tx.tmrF) {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer F stopped", slog.Any("transaction", tx))
	}

	var timeK time.Duration
	if !tx.tp.Metadata().Reliable() {
		timeK = tx.timing.TimeK()
	}
	tmr := timeutil.NewTimer(timeK)
	if tx.armTmr(&tx.tmrK, tmr, []TransactionState{TransactionStateCompleted},
		func(tmr *timeutil.Timer) { tx.onTimerK(ctx, tmr) }) {
		tx.log.LogAttrs(
			ctx, slog.LevelDebug, "timer K started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	return nil
}

func (tx *NonInviteClientTransaction) onTimerK(ctx context.Context, tmr *timeutil.Timer) {
	defer tx.saveSnapshot()
	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer K expired", slog.Any("transaction", tx))

	tx.clearTmr(&tx.tmrK, tmr)
	tx.fireTmrTrigger(ctx, txEvtTimerK)
}

func (tx *NonInviteClientTransaction) actTerminated(ctx context.Context, args ...any) error {
	_ = tx.clientTransact.actTerminated(ctx, args...)

	if tx.stopTmr(&tx.tmrE) {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer E stopped", slog.Any("transaction", tx))
	}

	if tx.stopTmr(&tx.tmrF) {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer F stopped", slog.Any("transaction", tx))
	}

	if tx.stopTmr(&tx.tmrK) {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer K stopped", slog.Any("transaction", tx))
	}

	return nil
}

func (tx *NonInviteClientTransaction) takeSnapshot() *ClientTransactionSnapshot {
	return &ClientTransactionSnapshot{
		Time:         time.Now(),
		Type:         tx.Type(),
		State:        tx.State(),
		Key:          tx.Key(),
		Request:      tx.Request(),
		LastResponse: tx.LastResponse(),
		SendOptions:  tx.sendOpts,
		Timing:       tx.timing,
		TimerE:       tx.tmrE.Load().Snapshot(),
		TimerF:       tx.tmrF.Load().Snapshot(),
		TimerK:       tx.tmrK.Load().Snapshot(),
	}
}

func RestoreNonInviteClientTransaction(
	snap *ClientTransactionSnapshot,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (*NonInviteClientTransaction, error) {
	// Timers are restored without callbacks and activated by Start.
	if err := snap.validate(TransactionTypeClientNonInvite); err != nil {
		return nil, errors.Wrap(err)
	}

	o := util.LastSliceElemOr(opts, ClientTransactionOptions{})
	o.SendOptions = snap.SendOptions
	o.Timing = snap.Timing

	req := snap.Request.Clone().(*RequestEnvelope) //nolint:forcetypeassert

	tx := new(NonInviteClientTransaction)
	clnTx, err := newClientTransact(TransactionTypeClientNonInvite, tx, req, tp, o)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	tx.clientTransact = clnTx
	if snap.LastResponse != nil {
		tx.lastRes.Store(snap.LastResponse.Clone().(*ResponseEnvelope)) //nolint:forcetypeassert
	}
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

func (tx *NonInviteClientTransaction) restoreTimers(snap *ClientTransactionSnapshot) error {
	for dst, tmrSnap := range map[*atomic.Pointer[timeutil.Timer]]*timeutil.TimerSnapshot{
		&tx.tmrE: snap.TimerE,
		&tx.tmrF: snap.TimerF,
		&tx.tmrK: snap.TimerK,
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

	return nil
}
