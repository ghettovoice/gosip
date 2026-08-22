package sip

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/timeutil"
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
	_ context.Context,
	req *RequestEnvelope,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (*NonInviteClientTransaction, error) {
	if err := req.Validate(); err != nil {
		return nil, errors.Wrap(err)
	}
	if mtd := req.Method(); mtd.Equal(RequestMethodInvite) || mtd.Equal(RequestMethodAck) {
		return nil, errors.Wrap(ErrMethodNotAllowed)
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

// Start starts the non-INVITE client transaction.
func (tx *NonInviteClientTransaction) Start(ctx context.Context) error {
	if !tx.started.CompareAndSwap(false, true) || tx.State() != TransactionStateTrying {
		return errors.Wrap(ErrActionNotAllowed)
	}
	return errors.Wrap(tx.actTrying(ctx))
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

	if !tx.tp.Metadata().Reliable() {
		tmr := timeutil.AfterFunc(tx.timing.TimeE(), tx.timerEHdlr(ctx))
		tx.tmrE.Store(tmr)

		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer E started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	tmr := timeutil.AfterFunc(tx.timing.TimeF(), tx.timerFHdlr(ctx))
	tx.tmrF.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer F started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *NonInviteClientTransaction) timerEHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer E expired", slog.Any("transaction", tx))

		if tx.State() != TransactionStateTrying && tx.State() != TransactionStateProceeding {
			tx.tmrE.Store(nil)
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerE); err != nil {
			panic(errors.Wrap(newTxTriggerErr(txEvtTimerE, tx.State(), err)))
		}

		if tmr := tx.tmrE.Load(); tmr != nil {
			var dur time.Duration
			if tx.State() == TransactionStateTrying {
				dur = min(2*tmr.Duration(), tx.timing.t2())
			} else {
				dur = tx.timing.t2()
			}
			tmr.Reset(dur)

			tx.log.LogAttrs(ctx, slog.LevelDebug, "timer E reset",
				slog.Any("transaction", tx),
				slog.Time("expires_at", time.Now().Add(tmr.Left())),
			)
		}
	}
}

func (tx *NonInviteClientTransaction) timerFHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer F expired", slog.Any("transaction", tx))

		tx.tmrF.Store(nil)

		if tx.State() != TransactionStateTrying && tx.State() != TransactionStateProceeding {
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerF); err != nil {
			panic(errors.Wrap(newTxTriggerErr(txEvtTimerF, tx.State(), err)))
		}
	}
}

func (tx *NonInviteClientTransaction) actCompleted(ctx context.Context, args ...any) error {
	_ = tx.clientTransact.actCompleted(ctx, args...)

	if tmr := tx.tmrE.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer E stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrF.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer F stopped", slog.Any("transaction", tx))
	}

	var timeK time.Duration
	if !tx.tp.Metadata().Reliable() {
		timeK = tx.timing.TimeK()
	}
	tmr := timeutil.AfterFunc(timeK, tx.timerKHdlr(ctx))
	tx.tmrK.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer K started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *NonInviteClientTransaction) timerKHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer K expired", slog.Any("transaction", tx))

		tx.tmrK.Store(nil)

		if tx.State() != TransactionStateCompleted {
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerK); err != nil {
			panic(errors.Wrap(newTxTriggerErr(txEvtTimerK, tx.State(), err)))
		}
	}
}

func (tx *NonInviteClientTransaction) actTerminated(ctx context.Context, args ...any) error {
	_ = tx.clientTransact.actTerminated(ctx, args...)

	if tmr := tx.tmrE.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer E stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrF.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer F stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrK.Swap(nil); tmr != nil && tmr.Stop() {
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
	ctx context.Context,
	snap *ClientTransactionSnapshot,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (*NonInviteClientTransaction, error) {
	if !snap.IsValid() || snap.Type != TransactionTypeClientNonInvite {
		return nil, errors.ErrorWrap("invalid snapshot")
	}

	o := util.LastSliceElemOr(opts, ClientTransactionOptions{})
	o.SendOptions = snap.SendOptions
	o.Timing = snap.Timing

	tx := new(NonInviteClientTransaction)
	clnTx, err := newClientTransact(TransactionTypeClientNonInvite, tx, snap.Request, tp, o)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	tx.clientTransact = clnTx
	if snap.LastResponse != nil {
		tx.lastRes.Store(snap.LastResponse)
	}
	if err := tx.initFSM(snap.State); err != nil {
		return nil, errors.Wrap(err)
	}
	tx.started.Store(true)

	if err := tx.restoreTimers(ctx, snap); err != nil {
		_ = tx.Terminate(ctx, errors.Wrap(err))
		return nil, errors.Wrap(err)
	}

	return tx, nil
}

func (tx *NonInviteClientTransaction) restoreTimers(ctx context.Context, snap *ClientTransactionSnapshot) error {
	if tmr := snap.TimerE; tmr != nil {
		restored, err := timeutil.RestoreTimer(tmr)
		if err != nil {
			return errors.Wrap(err)
		}

		restored.SetCallback(tx.timerEHdlr(ctx))
		tx.tmrE.Store(restored)
	}

	if tmr := snap.TimerF; tmr != nil {
		restored, err := timeutil.RestoreTimer(tmr)
		if err != nil {
			return errors.Wrap(err)
		}

		restored.SetCallback(tx.timerFHdlr(ctx))
		tx.tmrF.Store(restored)
	}

	if tmr := snap.TimerK; tmr != nil {
		restored, err := timeutil.RestoreTimer(tmr)
		if err != nil {
			return errors.Wrap(err)
		}

		restored.SetCallback(tx.timerKHdlr(ctx))
		tx.tmrK.Store(restored)
	}

	return nil
}
