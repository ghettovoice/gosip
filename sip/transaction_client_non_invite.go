package sip

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/internal/timeutil"
)

type NonInviteClientTransaction struct {
	*clientTransact

	tmrE atomic.Pointer[timeutil.SerializableTimer]
	tmrF atomic.Pointer[timeutil.SerializableTimer]
	tmrK atomic.Pointer[timeutil.SerializableTimer]
}

func NewNonInviteClientTransaction(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	tp ClientTransport,
	opts *ClientTransactionOptions,
) (*NonInviteClientTransaction, error) {
	if err := req.Validate(); err != nil {
		return nil, errtrace.Wrap(NewInvalidArgumentError(err))
	}
	if mtd := req.Method(); mtd.Equal(RequestMethodInvite) || mtd.Equal(RequestMethodAck) {
		return nil, errtrace.Wrap(NewInvalidArgumentError(ErrMethodNotAllowed))
	}

	tx := new(NonInviteClientTransaction)
	clnTx, err := newClientTransact(TransactionTypeClientNonInvite, tx, req, tp, opts)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	tx.clientTransact = clnTx

	ctx = ContextWithTransaction(ctx, tx)

	if err := tx.initFSM(TransactionStateTrying); err != nil {
		return nil, errtrace.Wrap(err)
	}
	if err := tx.actTrying(ctx); err != nil {
		return nil, errtrace.Wrap(err)
	}
	return tx, nil
}

const (
	txEvtTimerE = "timer_e"
	txEvtTimerF = "timer_f"
	txEvtTimerK = "timer_k"
)

func (tx *NonInviteClientTransaction) initFSM(start TransactionState) error {
	if err := tx.clientTransact.initFSM(start); err != nil {
		return errtrace.Wrap(err)
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
		InternalTransition(txEvtTerminate, tx.actNoop)

	return nil
}

func (tx *NonInviteClientTransaction) actTrying(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction trying", slog.Any("transaction", tx))

	if err := tx.sendReq(ctx, tx.req); err != nil {
		return errtrace.Wrap(err)
	}

	if !tx.tp.Reliable() {
		tmr := timeutil.AfterFunc(tx.timings.TimeE(), tx.timerEHdlr(ctx))
		tx.tmrE.Store(tmr)

		tx.log.LogAttrs(ctx, slog.LevelDebug,
			"timer E started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	tmr := timeutil.AfterFunc(tx.timings.TimeF(), tx.timerFHdlr(ctx))
	tx.tmrF.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug,
		"timer F started",
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
			panic(fmt.Errorf("fire %q in state %q: %w", txEvtTimerE, tx.State(), err))
		}

		if tmr := tx.tmrE.Load(); tmr != nil {
			var dur time.Duration
			if tx.State() == TransactionStateTrying {
				dur = min(2*tmr.Duration(), tx.timings.T2())
			} else {
				dur = tx.timings.T2()
			}
			tmr.Reset(dur)

			tx.log.LogAttrs(ctx, slog.LevelDebug,
				"timer E reset",
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
			panic(fmt.Errorf("fire %q in state %q: %w", txEvtTimerF, tx.State(), err))
		}
	}
}

func (tx *NonInviteClientTransaction) actCompleted(ctx context.Context, args ...any) error {
	tx.clientTransact.actCompleted(ctx, args...) //nolint:errcheck

	if tmr := tx.tmrE.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer E stopped", slog.Any("transaction", tx))
	}
	if tmr := tx.tmrF.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer F stopped", slog.Any("transaction", tx))
	}

	tmr := timeutil.AfterFunc(tx.timings.TimeK(), tx.timerKHdlr(ctx))
	tx.tmrK.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug,
		"timer K started",
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
			panic(fmt.Errorf("fire %q in state %q: %w", txEvtTimerK, tx.State(), err))
		}
	}
}

func (tx *NonInviteClientTransaction) actTerminated(ctx context.Context, args ...any) error {
	tx.clientTransact.actTerminated(ctx, args...) //nolint:errcheck

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
		Type:         tx.typ,
		State:        tx.State(),
		Key:          tx.key,
		Request:      tx.req,
		LastResponse: tx.LastResponse(),
		SendOptions:  cloneSendReqOpts(tx.sendOpts),
		Timings:      tx.timings,
		TimerE:       tx.tmrE.Load().Snapshot(),
		TimerF:       tx.tmrF.Load().Snapshot(),
		TimerK:       tx.tmrK.Load().Snapshot(),
	}
}

func RestoreNonInviteClientTransaction(
	ctx context.Context,
	snap *ClientTransactionSnapshot,
	tp ClientTransport,
	opts *ClientTransactionOptions,
) (*NonInviteClientTransaction, error) {
	if !snap.IsValid() || snap.Type != TransactionTypeClientNonInvite {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid snapshot"))
	}

	var restoreOpts ClientTransactionOptions
	if opts != nil {
		restoreOpts = *opts
	}
	restoreOpts.Key = snap.Key
	restoreOpts.SendOptions = cloneSendReqOpts(snap.SendOptions)
	restoreOpts.Timings = snap.Timings

	tx := new(NonInviteClientTransaction)
	clnTx, err := newClientTransact(TransactionTypeClientNonInvite, tx, snap.Request, tp, &restoreOpts)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	tx.clientTransact = clnTx

	ctx = ContextWithTransaction(ctx, tx)

	if snap.LastResponse != nil {
		tx.lastRes.Store(snap.LastResponse)
	}

	if err := tx.initFSM(snap.State); err != nil {
		return nil, errtrace.Wrap(err)
	}

	tx.restoreTimers(ctx, snap)

	return tx, nil
}

func (tx *NonInviteClientTransaction) restoreTimers(ctx context.Context, snap *ClientTransactionSnapshot) {
	if tmr := snap.TimerE; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerEHdlr(ctx))
		tx.tmrE.Store(restored)
	}

	if tmr := snap.TimerF; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerFHdlr(ctx))
		tx.tmrF.Store(restored)
	}

	if tmr := snap.TimerK; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerKHdlr(ctx))
		tx.tmrK.Store(restored)
	}
}
