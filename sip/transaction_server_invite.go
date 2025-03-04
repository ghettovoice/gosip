package sip

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sync/atomic"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/internal/timeutil"
	"github.com/ghettovoice/gosip/internal/types"
)

// InviteServerTransaction represents an invite server transaction.
// It implements the server transaction state machine defined in RFC 3261 section 17.2. plus patches from RFC 6026.
type InviteServerTransaction struct {
	*serverTransact

	tmr1xx atomic.Pointer[timeutil.SerializableTimer]
	tmrG   atomic.Pointer[timeutil.SerializableTimer]
	tmrH   atomic.Pointer[timeutil.SerializableTimer]
	tmrI   atomic.Pointer[timeutil.SerializableTimer]
	tmrL   atomic.Pointer[timeutil.SerializableTimer]

	onAck       types.CallbackManager[RequestHandler]
	pendingAcks types.Deque[*InboundRequest]
}

// NewInviteServerTransaction creates a new invite server transaction and starts its state machine.
//
// Request expected to be a valid SIP request with INVITE method.
// Transport expected to be a non-nil server transport.
// Options are optional and can be nil, in which case default options will be used.
// Transaction key will be filled from the request automatically if not specified in the options.
func NewInviteServerTransaction(
	req *InboundRequest,
	tp ServerTransport,
	opts *ServerTransactionOptions,
) (*InviteServerTransaction, error) {
	if err := req.Validate(); err != nil {
		return nil, errtrace.Wrap(NewInvalidArgumentError(err))
	}
	if !req.Method().Equal(RequestMethodInvite) {
		return nil, errtrace.Wrap(NewInvalidArgumentError(ErrMethodNotAllowed))
	}

	tx := new(InviteServerTransaction)
	srvTx, err := newServerTransact(TransactionTypeServerInvite, tx, req, tp, opts)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	tx.serverTransact = srvTx

	if err := tx.initFSM(TransactionStateProceeding); err != nil {
		return nil, errtrace.Wrap(err)
	}
	if err := tx.actProceeding(tx.ctx); err != nil {
		return nil, errtrace.Wrap(err)
	}
	return tx, nil
}

const (
	txEvtRecvAck  = "recv_ack"
	txEvtTimer1xx = "timer_1xx"
	txEvtTimerG   = "timer_g"
	txEvtTimerH   = "timer_h"
	txEvtTimerI   = "timer_i"
	txEvtTimerL   = "timer_l"
)

func (tx *InviteServerTransaction) initFSM(start TransactionState) error {
	if err := tx.serverTransact.initFSM(start); err != nil {
		return errtrace.Wrap(err)
	}

	tx.fsm.SetTriggerParameters(txEvtRecvAck, reflect.TypeOf((*InboundRequest)(nil)))

	tx.fsm.Configure(TransactionStateProceeding).
		InternalTransition(txEvtRecvReq, tx.actResendRes).
		InternalTransition(txEvtSend1xx, tx.actSendRes).
		InternalTransition(txEvtTimer1xx, tx.actSend100).
		InternalTransition(txEvtTranspErr, tx.actTranspErr).
		Permit(txEvtSend2xx, TransactionStateAccepted).
		Permit(txEvtSend300699, TransactionStateCompleted).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateAccepted).
		OnEntry(tx.actAccepted).
		OnEntryFrom(txEvtSend2xx, tx.actSendRes).
		InternalTransition(txEvtRecvReq, tx.actNoop).
		InternalTransition(txEvtRecvAck, tx.actPassAck).
		InternalTransition(txEvtSend2xx, tx.actSendRes).
		InternalTransition(txEvtTranspErr, tx.actTranspErr).
		Permit(txEvtTimerL, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateCompleted).
		OnEntry(tx.actCompleted).
		OnEntryFrom(txEvtSend300699, tx.actSendRes).
		InternalTransition(txEvtRecvReq, tx.actResendRes).
		InternalTransition(txEvtTimerG, tx.actResendRes).
		InternalTransition(txEvtTranspErr, tx.actTranspErr).
		Permit(txEvtRecvAck, TransactionStateConfirmed).
		Permit(txEvtTimerH, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateConfirmed).
		OnEntry(tx.actConfirmed).
		InternalTransition(txEvtRecvReq, tx.actNoop).
		InternalTransition(txEvtRecvAck, tx.actNoop).
		Permit(txEvtTimerI, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateTerminated).
		OnEntry(tx.actTerminated).
		OnEntryFrom(txEvtTimerH, tx.actTimedOut)

	return nil
}

func (tx *InviteServerTransaction) actSend100(ctx context.Context, _ ...any) error {
	res, err := tx.req.NewResponse(ResponseStatusTrying, nil)
	if err != nil {
		// Request is always valid, so this should never happen.
		panic(fmt.Errorf("create auto %q response: %w", ResponseStatusTrying, err))
	}

	tx.log.LogAttrs(ctx, slog.LevelDebug, "send response", slog.Any("transaction", tx), slog.Any("response", res))

	tx.sendRes(ctx, res, nil) //nolint:errcheck
	return nil
}

func (tx *InviteServerTransaction) actSendRes(ctx context.Context, args ...any) error {
	if tmr := tx.tmr1xx.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "1xx timer stopped", slog.Any("transaction", tx))
	}
	return errtrace.Wrap(tx.serverTransact.actSendRes(ctx, args...))
}

func (tx *InviteServerTransaction) actPassAck(ctx context.Context, args ...any) error {
	ack := args[0].(*InboundRequest) //nolint:forcetypeassert

	tx.log.LogAttrs(ctx, slog.LevelDebug, "pass ACK", slog.Any("transaction", tx), slog.Any("ack", ack))

	tx.pendingAcks.Append(ack)
	if tx.onAck.Len() > 0 {
		tx.deliverPendingAcks()
	}
	return nil
}

func (tx *InviteServerTransaction) deliverPendingAcks() {
	acks := tx.pendingAcks.Drain()
	if len(acks) == 0 {
		return
	}

	tx.onAck.Range(func(fn RequestHandler) {
		for _, ack := range acks {
			fn(tx.ctx, ack)
		}
	})
}

//nolint:unparam
func (tx *InviteServerTransaction) actProceeding(ctx context.Context, args ...any) error {
	tx.serverTransact.actProceeding(ctx, args...) //nolint:errcheck

	tmr := timeutil.AfterFunc(tx.timings.Time100(), tx.onTimer1xx)
	tx.tmr1xx.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug,
		"1xx timer started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *InviteServerTransaction) onTimer1xx() {
	tx.log.LogAttrs(tx.ctx, slog.LevelDebug, "1xx timer expired", slog.Any("transaction", tx))

	tx.tmr1xx.Store(nil)

	if tx.State() != TransactionStateProceeding {
		return
	}

	if err := tx.fsm.FireCtx(tx.ctx, txEvtTimer1xx); err != nil {
		panic(fmt.Errorf("fire %q in state %q: %w", txEvtTimer1xx, tx.State(), err))
	}
}

func (tx *InviteServerTransaction) actAccepted(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction accepted", slog.Any("transaction", tx))

	tmr := timeutil.AfterFunc(tx.timings.TimeL(), tx.onTimerL)
	tx.tmrL.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug,
		"timer L started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *InviteServerTransaction) onTimerL() {
	tx.log.LogAttrs(tx.ctx, slog.LevelDebug, "timer L expired", slog.Any("transaction", tx))

	tx.tmrL.Store(nil)

	if tx.State() != TransactionStateAccepted {
		return
	}

	if err := tx.fsm.FireCtx(tx.ctx, txEvtTimerL); err != nil {
		panic(fmt.Errorf("fire %q in state %q: %w", txEvtTimerL, tx.State(), err))
	}
}

func (tx *InviteServerTransaction) actCompleted(ctx context.Context, args ...any) error {
	tx.serverTransact.actCompleted(ctx, args...) //nolint:errcheck

	if !IsReliableTransport(tx.tp) {
		tmr := timeutil.AfterFunc(tx.timings.TimeG(), tx.onTimerG)
		tx.tmrG.Store(tmr)

		tx.log.LogAttrs(ctx, slog.LevelDebug,
			"timer G started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	tmr := timeutil.AfterFunc(tx.timings.TimeH(), tx.onTimerH)
	tx.tmrH.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug,
		"timer H started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *InviteServerTransaction) onTimerH() {
	tx.log.LogAttrs(tx.ctx, slog.LevelDebug, "timer H expired", slog.Any("transaction", tx))

	tx.tmrH.Store(nil)

	if tx.State() != TransactionStateCompleted {
		return
	}

	if err := tx.fsm.FireCtx(tx.ctx, txEvtTimerH); err != nil {
		panic(fmt.Errorf("fire %q in state %q: %w", txEvtTimerH, tx.State(), err))
	}
}

func (tx *InviteServerTransaction) onTimerG() {
	tx.log.LogAttrs(tx.ctx, slog.LevelDebug, "timer G expired", slog.Any("transaction", tx))

	if tx.State() != TransactionStateCompleted {
		tx.tmrG.Store(nil)
		return
	}

	if err := tx.fsm.FireCtx(tx.ctx, txEvtTimerG); err != nil {
		panic(fmt.Errorf("fire %q in state %q: %w", txEvtTimerG, tx.State(), err))
	}

	if tmr := tx.tmrG.Load(); tmr != nil {
		tmr.Reset(min(2*tmr.Duration(), tx.timings.T2()))

		tx.log.LogAttrs(tx.ctx, slog.LevelDebug,
			"timer G reset",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}
}

func (tx *InviteServerTransaction) actConfirmed(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction confirmed", slog.Any("transaction", tx))

	if tmr := tx.tmrH.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer H stopped", slog.Any("transaction", tx))
	}
	if tmr := tx.tmrG.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer G stopped", slog.Any("transaction", tx))
	}

	var timeI time.Duration
	if !IsReliableTransport(tx.tp) {
		timeI = tx.timings.TimeI()
	}
	tmr := timeutil.AfterFunc(timeI, tx.onTimerI)
	tx.tmrI.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug,
		"timer I started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *InviteServerTransaction) onTimerI() {
	tx.log.LogAttrs(tx.ctx, slog.LevelDebug, "timer I expired", slog.Any("transaction", tx))

	tx.tmrI.Store(nil)

	if tx.State() != TransactionStateConfirmed {
		return
	}

	if err := tx.fsm.FireCtx(tx.ctx, txEvtTimerI); err != nil {
		panic(fmt.Errorf("fire %q in state %q: %w", txEvtTimerI, tx.State(), err))
	}
}

func (tx *InviteServerTransaction) actTerminated(ctx context.Context, args ...any) error {
	tx.serverTransact.actTerminated(ctx, args...) //nolint:errcheck

	// timer G can be active after transition to here by timer H
	if tmr := tx.tmrG.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer G stopped", slog.Any("transaction", tx))
	}
	if tmr := tx.tmrH.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer H stopped", slog.Any("transaction", tx))
	}
	if tmr := tx.tmrI.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer I stopped", slog.Any("transaction", tx))
	}
	if tmr := tx.tmrL.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer L stopped", slog.Any("transaction", tx))
	}

	return nil
}

func (tx *InviteServerTransaction) adjustKeys(txKey, reqKey *ServerTransactionKey, req *InboundRequest) {
	if !IsRFC3261Branch(txKey.Branch) && req.Method().Equal(RequestMethodAck) {
		to, _ := req.Headers().To()
		reqKey.ToTag, _ = to.Tag()

		if res := tx.LastResponse(); res != nil {
			to, _ := res.Headers().To()
			txKey.ToTag, _ = to.Tag()
		}
	}
}

func (tx *InviteServerTransaction) recvReq(ctx context.Context, req *InboundRequest) error {
	if req.Method().Equal(RequestMethodAck) {
		return errtrace.Wrap(tx.fsm.FireCtx(ctx, txEvtRecvAck, req))
	}
	return errtrace.Wrap(tx.serverTransact.recvReq(ctx, req))
}

// OnAck registers a callback to be called when the transaction receives an 2xx ACK.
//
// 2xx ACK can be matched to the INVITE transaction only by RFC 2543 matching rules,
// so this callback here only for backward compatibility with old clients.
// 2xx ACK from RFC 3261 always goes outside of the INVITE transaction.
//
// The callback will be called with the transaction's context, see [Transaction.Context].
// The transaction can be retrieved from the context using [TransactionFromContext].
//
// The callback can be canceled by calling the returned cancel function.
// Multiple callbacks can be registered.
func (tx *InviteServerTransaction) OnAck(fn RequestHandler) (cancel func()) {
	cancel = tx.onAck.Add(fn)
	tx.deliverPendingAcks()
	return cancel
}

func (tx *InviteServerTransaction) takeSnapshot() *ServerTransactionSnapshot {
	return &ServerTransactionSnapshot{
		Time:         time.Now(),
		Type:         tx.typ,
		State:        tx.State(),
		Key:          tx.key,
		Request:      tx.req,
		LastResponse: tx.LastResponse(),
		SendOptions:  cloneSendResOpts(tx.sendOpts.Load()),
		Timings:      tx.timings,
		Timer1xx:     tx.tmr1xx.Load().Snapshot(),
		TimerG:       tx.tmrG.Load().Snapshot(),
		TimerH:       tx.tmrH.Load().Snapshot(),
		TimerI:       tx.tmrI.Load().Snapshot(),
		TimerL:       tx.tmrL.Load().Snapshot(),
	}
}

// RestoreInviteServerTransaction restores an invite server transaction from a snapshot.
//
// The snapshot contains the serialized state of the transaction.
// Transport is required to send responses.
// Options are optional and can be nil. The key field from options is ignored
// and the key from the snapshot will be used instead.
//
// After restoration, the transaction FSM will be in the state specified in the snapshot.
// Timers will be restored and their callbacks will be reconnected to the FSM.
// If a timer has already expired according to the snapshot, it will not be restarted.
func RestoreInviteServerTransaction(
	snap *ServerTransactionSnapshot,
	tp ServerTransport,
	opts *ServerTransactionOptions,
) (*InviteServerTransaction, error) {
	if !snap.IsValid() || snap.Type != TransactionTypeServerInvite {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid snapshot"))
	}

	var restoreOpts ServerTransactionOptions
	if opts != nil {
		restoreOpts = *opts
	}
	restoreOpts.Key = snap.Key
	restoreOpts.Timings = snap.Timings

	tx := new(InviteServerTransaction)
	srvTx, err := newServerTransact(TransactionTypeServerInvite, tx, snap.Request, tp, &restoreOpts)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	tx.serverTransact = srvTx

	if snap.LastResponse != nil {
		tx.lastRes.Store(snap.LastResponse)
	}

	if snap.SendOptions != nil {
		tx.sendOpts.Store(cloneSendResOpts(snap.SendOptions))
	}

	if err := tx.initFSM(snap.State); err != nil {
		return nil, errtrace.Wrap(err)
	}

	tx.restoreTimers(snap)

	return tx, nil
}

// restoreTimers restores transaction timers from the snapshot and reconnects their callbacks.
func (tx *InviteServerTransaction) restoreTimers(snap *ServerTransactionSnapshot) {
	if tmr := snap.Timer1xx; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.onTimer1xx)
		tx.tmr1xx.Store(restored)
	}

	if tmr := snap.TimerG; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.onTimerG)
		tx.tmrG.Store(restored)
	}

	if tmr := snap.TimerH; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.onTimerH)
		tx.tmrH.Store(restored)
	}

	if tmr := snap.TimerI; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.onTimerI)
		tx.tmrI.Store(restored)
	}

	if tmr := snap.TimerL; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.onTimerL)
		tx.tmrL.Store(restored)
	}
}
