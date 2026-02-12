package sip

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/internal/timeutil"
)

// InviteClientTransaction represents a SIP client transaction for INVITE requests.
// It implements the client transaction FSM defined in RFC 3261 section 17.1.1
// and patches from RFC 6026.
type InviteClientTransaction struct {
	*clientTransact

	tmrA atomic.Pointer[timeutil.SerializableTimer]
	tmrB atomic.Pointer[timeutil.SerializableTimer]
	tmrD atomic.Pointer[timeutil.SerializableTimer]
	tmrM atomic.Pointer[timeutil.SerializableTimer]

	ack atomic.Pointer[OutboundRequestEnvelope]
}

// NewInviteClientTransaction creates a new invite client transaction and starts its state machine.
//
// Context does not affect the transaction lifecycle, it is passed to the initial FSM actions and transitions.
// Request expected to be a valid SIP request with INVITE method.
// Transport expected to be a non-nil client transport.
// Options are optional and can be nil, in which case default options will be used.
func NewInviteClientTransaction(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	tp ClientTransport,
	opts *ClientTransactionOptions,
) (*InviteClientTransaction, error) {
	if err := req.Validate(); err != nil {
		return nil, errtrace.Wrap(NewInvalidArgumentError(err))
	}
	if !req.Method().Equal(RequestMethodInvite) {
		return nil, errtrace.Wrap(NewInvalidArgumentError(ErrMethodNotAllowed))
	}

	tx := new(InviteClientTransaction)
	clnTx, err := newClientTransact(TransactionTypeClientInvite, tx, req, tp, opts)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	tx.clientTransact = clnTx

	ctx = ContextWithTransaction(ctx, tx)

	if err := tx.initFSM(TransactionStateCalling); err != nil {
		return nil, errtrace.Wrap(err)
	}
	if err := tx.actCalling(ctx); err != nil {
		return nil, errtrace.Wrap(err)
	}
	return tx, nil
}

const (
	txEvtTimerA = "timer_a"
	txEvtTimerB = "timer_b"
	txEvtTimerD = "timer_d"
	txEvtTimerM = "timer_m"
)

func (tx *InviteClientTransaction) initFSM(start TransactionState) error {
	if err := tx.clientTransact.initFSM(start); err != nil {
		return errtrace.Wrap(err)
	}

	tx.fsm.Configure(TransactionStateCalling).
		InternalTransition(txEvtTimerA, tx.actSendReq).
		Permit(txEvtRecv1xx, TransactionStateProceeding).
		Permit(txEvtRecv2xx, TransactionStateAccepted).
		Permit(txEvtRecv300699, TransactionStateCompleted).
		Permit(txEvtTimerB, TransactionStateTerminated).
		Permit(txEvtTranspErr, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateProceeding).
		OnEntry(tx.actProceeding).
		OnEntryFrom(txEvtRecv1xx, tx.actPassRes).
		InternalTransition(txEvtRecv1xx, tx.actPassRes).
		Permit(txEvtRecv2xx, TransactionStateAccepted).
		Permit(txEvtRecv300699, TransactionStateCompleted).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateCompleted).
		OnEntry(tx.actCompleted).
		OnEntryFrom(txEvtRecv300699, tx.actPassResSendAck).
		InternalTransition(txEvtRecv300699, tx.actSendAck).
		Permit(txEvtTimerD, TransactionStateTerminated).
		Permit(txEvtTranspErr, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateAccepted).
		OnEntry(tx.actAccepted).
		OnEntryFrom(txEvtRecv2xx, tx.actPassRes).
		InternalTransition(txEvtRecv2xx, tx.actPassRes).
		Permit(txEvtTimerM, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateTerminated).
		OnEntry(tx.actTerminated).
		OnEntryFrom(txEvtTimerB, tx.actTimedOut).
		OnEntryFrom(txEvtTranspErr, tx.actTranspErr).
		InternalTransition(txEvtTerminate, tx.actNoop)

	return nil
}

func (tx *InviteClientTransaction) actPassResSendAck(ctx context.Context, args ...any) error {
	tx.actPassRes(ctx, args...) //nolint:errcheck
	tx.actSendAck(ctx, args...) //nolint:errcheck
	return nil
}

func (tx *InviteClientTransaction) actSendAck(ctx context.Context, _ ...any) error {
	ack := tx.ack.Load()
	if ack == nil {
		ack = tx.req.Clone().(*OutboundRequestEnvelope) //nolint:forcetypeassert
		ack.message().Method = RequestMethodAck

		via, _ := ack.message().Headers.FirstVia()
		ack.message().Headers.Set(header.Via{*via})

		cseq, _ := ack.message().Headers.CSeq()
		cseq.Method = RequestMethodAck

		to, _ := tx.LastResponse().Headers().To()
		ack.message().Headers.Set(to)

		ack.message().Headers.Set(header.MaxForwards(70))

		tx.ack.Store(ack)
	}

	tx.log.LogAttrs(ctx, slog.LevelDebug, "send request",
		slog.Any("transaction", tx.impl),
		slog.Any("request", ack),
	)

	tx.sendReq(ctx, ack) //nolint:errcheck
	return nil
}

func (tx *InviteClientTransaction) actCalling(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction calling", slog.Any("transaction", tx))

	if err := tx.sendReq(ctx, tx.req); err != nil {
		return errtrace.Wrap(err)
	}

	if !tx.tp.Reliable() {
		tmr := timeutil.AfterFunc(tx.timings.TimeA(), tx.timerAHdlr(ctx))
		tx.tmrA.Store(tmr)

		tx.log.LogAttrs(ctx, slog.LevelDebug,
			"timer A started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	tmr := timeutil.AfterFunc(tx.timings.TimeB(), tx.timerBHdlr(ctx))
	tx.tmrB.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug,
		"timer B started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *InviteClientTransaction) timerAHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A expired", slog.Any("transaction", tx))

		if tx.State() != TransactionStateCalling {
			tx.tmrA.Store(nil)
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerA); err != nil {
			panic(fmt.Errorf("fire %q in state %q: %w", txEvtTimerA, tx.State(), err))
		}

		if tmr := tx.tmrA.Load(); tmr != nil {
			tmr.Reset(2 * tmr.Duration())

			tx.log.LogAttrs(ctx, slog.LevelDebug,
				"timer A reset",
				slog.Any("transaction", tx),
				slog.Time("expires_at", time.Now().Add(tmr.Left())),
			)
		}
	}
}

func (tx *InviteClientTransaction) timerBHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B expired", slog.Any("transaction", tx))

		tx.tmrB.Store(nil)

		if tx.State() != TransactionStateCalling {
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerB); err != nil {
			panic(fmt.Errorf("fire %q in state %q: %w", txEvtTimerB, tx.State(), err))
		}
	}
}

func (tx *InviteClientTransaction) actProceeding(ctx context.Context, args ...any) error {
	tx.clientTransact.actProceeding(ctx, args...) //nolint:errcheck

	if tmr := tx.tmrA.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A stopped", slog.Any("transaction", tx))
	}
	if tmr := tx.tmrB.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B stopped", slog.Any("transaction", tx))
	}

	return nil
}

func (tx *InviteClientTransaction) actCompleted(ctx context.Context, args ...any) error {
	tx.clientTransact.actCompleted(ctx, args...) //nolint:errcheck

	if tmr := tx.tmrA.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A stopped", slog.Any("transaction", tx))
	}
	if tmr := tx.tmrB.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B stopped", slog.Any("transaction", tx))
	}

	tmr := timeutil.AfterFunc(tx.timings.TimeD(), tx.timerDHdlr(ctx))
	tx.tmrD.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug,
		"timer D started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *InviteClientTransaction) timerDHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer D expired", slog.Any("transaction", tx))

		tx.tmrD.Store(nil)

		if tx.State() != TransactionStateCompleted {
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerD); err != nil {
			panic(fmt.Errorf("fire %q in state %q: %w", txEvtTimerD, tx.State(), err))
		}
	}
}

func (tx *InviteClientTransaction) actAccepted(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction accepted", slog.Any("transaction", tx))

	if tmr := tx.tmrA.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A stopped", slog.Any("transaction", tx))
	}
	if tmr := tx.tmrB.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B stopped", slog.Any("transaction", tx))
	}

	tmr := timeutil.AfterFunc(tx.timings.TimeM(), tx.timerMHdlr(ctx))
	tx.tmrM.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug,
		"timer M started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *InviteClientTransaction) timerMHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer M expired", slog.Any("transaction", tx))

		tx.tmrM.Store(nil)

		if tx.State() != TransactionStateAccepted {
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerM); err != nil {
			panic(fmt.Errorf("fire %q in state %q: %w", txEvtTimerM, tx.State(), err))
		}
	}
}

func (tx *InviteClientTransaction) actTerminated(ctx context.Context, args ...any) error {
	tx.clientTransact.actTerminated(ctx, args...) //nolint:errcheck

	if tmr := tx.tmrA.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A stopped", slog.Any("transaction", tx))
	}
	if tmr := tx.tmrB.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B stopped", slog.Any("transaction", tx))
	}
	if tmr := tx.tmrD.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer D stopped", slog.Any("transaction", tx))
	}
	if tmr := tx.tmrM.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer M stopped", slog.Any("transaction", tx))
	}

	return nil
}

func (tx *InviteClientTransaction) takeSnapshot() *ClientTransactionSnapshot {
	return &ClientTransactionSnapshot{
		Time:         time.Now(),
		Type:         tx.typ,
		State:        tx.State(),
		Key:          tx.key,
		Request:      tx.req,
		LastResponse: tx.LastResponse(),
		SendOptions:  cloneSendReqOpts(tx.sendOpts),
		Timings:      tx.timings,
		TimerA:       tx.tmrA.Load().Snapshot(),
		TimerB:       tx.tmrB.Load().Snapshot(),
		TimerD:       tx.tmrD.Load().Snapshot(),
		TimerM:       tx.tmrM.Load().Snapshot(),
	}
}

func RestoreInviteClientTransaction(
	ctx context.Context,
	snap *ClientTransactionSnapshot,
	tp ClientTransport,
	opts *ClientTransactionOptions,
) (*InviteClientTransaction, error) {
	if !snap.IsValid() || snap.Type != TransactionTypeClientInvite {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid snapshot"))
	}

	var restoreOpts ClientTransactionOptions
	if opts != nil {
		restoreOpts = *opts
	}
	restoreOpts.Key = snap.Key
	restoreOpts.SendOptions = cloneSendReqOpts(snap.SendOptions)
	restoreOpts.Timings = snap.Timings

	tx := new(InviteClientTransaction)
	clnTx, err := newClientTransact(TransactionTypeClientInvite, tx, snap.Request, tp, &restoreOpts)
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

func (tx *InviteClientTransaction) restoreTimers(ctx context.Context, snap *ClientTransactionSnapshot) {
	if tmr := snap.TimerA; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerAHdlr(ctx))
		tx.tmrA.Store(restored)
	}

	if tmr := snap.TimerB; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerBHdlr(ctx))
		tx.tmrB.Store(restored)
	}

	if tmr := snap.TimerD; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerDHdlr(ctx))
		tx.tmrD.Store(restored)
	}

	if tmr := snap.TimerM; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerMHdlr(ctx))
		tx.tmrM.Store(restored)
	}
}
