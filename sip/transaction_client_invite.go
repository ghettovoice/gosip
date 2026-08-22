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
	"github.com/ghettovoice/gosip/sip/header"
)

// InviteClientTransaction represents a SIP client transaction for INVITE requests.
// It implements the client transaction FSM defined in RFC 3261 section 17.1.1
// and patches from RFC 6026.
type InviteClientTransaction struct {
	*clientTransact

	tmrA atomic.Pointer[timeutil.Timer]
	tmrB atomic.Pointer[timeutil.Timer]
	tmrD atomic.Pointer[timeutil.Timer]
	tmrM atomic.Pointer[timeutil.Timer]

	ack atomic.Pointer[RequestEnvelope]
}

var _ ClientTransaction = (*InviteClientTransaction)(nil)

// NewInviteClientTransaction creates an initialized invite client transaction.
//
// Request expected to be a valid SIP request with INVITE method.
// Transport expected to be a non-nil client transport.
// Options are optional and can be nil, in which case default options will be used.
func NewInviteClientTransaction(
	_ context.Context,
	req *RequestEnvelope,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (*InviteClientTransaction, error) {
	if err := req.Validate(); err != nil {
		return nil, errors.Wrap(err)
	}
	if !req.Method().Equal(RequestMethodInvite) {
		return nil, errors.Wrap(ErrMethodNotAllowed)
	}

	o := util.LastSliceElemOr(opts, ClientTransactionOptions{})

	tx := new(InviteClientTransaction)
	clnTx, err := newClientTransact(TransactionTypeClientInvite, tx, req, tp, o)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	tx.clientTransact = clnTx
	if err := tx.initFSM(TransactionStateCalling); err != nil {
		return nil, errors.Wrap(err)
	}

	return tx, nil
}

// Start starts the invite client transaction.
func (tx *InviteClientTransaction) Start(ctx context.Context) error {
	if !tx.started.CompareAndSwap(false, true) || tx.State() != TransactionStateCalling {
		return errors.Wrap(ErrActionNotAllowed)
	}
	return errors.Wrap(tx.actCalling(ctx))
}

func (tx *InviteClientTransaction) LogValue() slog.Value {
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
	txEvtTimerA = "timer_a"
	txEvtTimerB = "timer_b"
	txEvtTimerD = "timer_d"
	txEvtTimerM = "timer_m"
)

func (tx *InviteClientTransaction) initFSM(start TransactionState) error {
	if err := tx.clientTransact.initFSM(start); err != nil {
		return errors.Wrap(err)
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
		OnEntryFrom(txEvtTerminate, tx.actTermErr).
		InternalTransition(txEvtTerminate, tx.actNoop)

	return nil
}

func (tx *InviteClientTransaction) actPassResSendAck(ctx context.Context, args ...any) error {
	_ = tx.actPassRes(ctx, args...)
	_ = tx.actSendAck(ctx, args...)
	return nil
}

func (tx *InviteClientTransaction) actSendAck(ctx context.Context, _ ...any) error {
	ack := tx.getAck()

	tx.log.LogAttrs(ctx, slog.LevelDebug, "send request",
		slog.Any("transaction", tx.impl),
		slog.Any("request", ack),
	)

	_ = tx.sendReq(ctx, ack)
	return nil
}

func (tx *InviteClientTransaction) getAck() *RequestEnvelope {
	if r := tx.ack.Load(); r != nil {
		return r
	}

	msg := &Request{
		Method:  RequestMethodAck,
		Proto:   protoVer20,
		Headers: make(Headers),
	}

	tx.req.WithMessage(func(req *Request) {
		msg.URI = req.URI.Clone()

		hop, _ := req.Headers.FirstVia()
		msg.Headers.Set(header.Via{hop.Clone()})

		from, _ := req.Headers.From()
		msg.Headers.Set(from.Clone())

		tx.lastRes.Load().WithMessage(func(res *Response) {
			to, _ := res.Headers.To()
			msg.Headers.Set(to.Clone())
		})

		cid, _ := req.Headers.CallID()
		msg.Headers.Set(cid.Clone())

		cseq, _ := req.Headers.CSeq()
		cseq = cseq.Clone().(*header.CSeq) //nolint:forcetypeassert
		cseq.Method = RequestMethodAck
		msg.Headers.Set(cseq)

		if routes := req.Headers.Get("Route"); len(routes) > 0 {
			for _, h := range routes {
				msg.Headers.Append(h.Clone())
			}
		}
	})
	EnsureRequestMaxForwards(msg)
	EnsureMessageContentLength(msg)

	req := NewRequestEnvelope(msg).
		SetTransport(tx.req.Transport()).
		SetLocalAddr(tx.req.LocalAddr()).
		SetRemoteAddr(tx.req.RemoteAddr())
	tx.ack.Store(req)
	return req
}

func (tx *InviteClientTransaction) actCalling(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction calling", slog.Any("transaction", tx))

	if err := tx.sendReq(ctx, tx.req); err != nil {
		return errors.Wrap(err)
	}

	if !tx.tp.Metadata().Reliable() {
		tmr := timeutil.AfterFunc(tx.timing.TimeA(), tx.timerAHdlr(ctx))
		tx.tmrA.Store(tmr)

		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	tmr := timeutil.AfterFunc(tx.timing.TimeB(), tx.timerBHdlr(ctx))
	tx.tmrB.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B started",
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
			panic(errors.Wrap(newTxTriggerErr(txEvtTimerA, tx.State(), err)))
		}

		if tmr := tx.tmrA.Load(); tmr != nil {
			tmr.Reset(2 * tmr.Duration())

			tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A reset",
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
			panic(errors.Wrap(newTxTriggerErr(txEvtTimerB, tx.State(), err)))
		}
	}
}

func (tx *InviteClientTransaction) actProceeding(ctx context.Context, args ...any) error {
	_ = tx.clientTransact.actProceeding(ctx, args...)

	if tmr := tx.tmrA.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrB.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B stopped", slog.Any("transaction", tx))
	}

	return nil
}

func (tx *InviteClientTransaction) actCompleted(ctx context.Context, args ...any) error {
	_ = tx.clientTransact.actCompleted(ctx, args...)

	if tmr := tx.tmrA.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrB.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B stopped", slog.Any("transaction", tx))
	}

	var timeD time.Duration
	if !tx.tp.Metadata().Reliable() {
		timeD = tx.timing.timeD()
	}
	tmr := timeutil.AfterFunc(timeD, tx.timerDHdlr(ctx))
	tx.tmrD.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer D started",
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
			panic(errors.Wrap(newTxTriggerErr(txEvtTimerD, tx.State(), err)))
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

	tmr := timeutil.AfterFunc(tx.timing.TimeM(), tx.timerMHdlr(ctx))
	tx.tmrM.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer M started",
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
			panic(errors.Wrap(newTxTriggerErr(txEvtTimerM, tx.State(), err)))
		}
	}
}

func (tx *InviteClientTransaction) actTerminated(ctx context.Context, args ...any) error {
	_ = tx.clientTransact.actTerminated(ctx, args...)

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
		Type:         tx.Type(),
		State:        tx.State(),
		Key:          tx.Key(),
		Request:      tx.Request(),
		LastResponse: tx.LastResponse(),
		SendOptions:  tx.sendOpts,
		Timing:       tx.timing,
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
	opts ...ClientTransactionOptions,
) (*InviteClientTransaction, error) {
	if !snap.IsValid() || snap.Type != TransactionTypeClientInvite {
		return nil, errors.ErrorWrap("invalid snapshot")
	}

	o := util.LastSliceElemOr(opts, ClientTransactionOptions{})
	o.SendOptions = snap.SendOptions
	o.Timing = snap.Timing

	tx := new(InviteClientTransaction)
	clnTx, err := newClientTransact(TransactionTypeClientInvite, tx, snap.Request, tp, o)
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

func (tx *InviteClientTransaction) restoreTimers(ctx context.Context, snap *ClientTransactionSnapshot) error {
	if tmr := snap.TimerA; tmr != nil {
		restored, err := timeutil.RestoreTimer(tmr)
		if err != nil {
			return errors.Wrap(err)
		}

		restored.SetCallback(tx.timerAHdlr(ctx))
		tx.tmrA.Store(restored)
	}

	if tmr := snap.TimerB; tmr != nil {
		restored, err := timeutil.RestoreTimer(tmr)
		if err != nil {
			return errors.Wrap(err)
		}

		restored.SetCallback(tx.timerBHdlr(ctx))
		tx.tmrB.Store(restored)
	}

	if tmr := snap.TimerD; tmr != nil {
		restored, err := timeutil.RestoreTimer(tmr)
		if err != nil {
			return errors.Wrap(err)
		}

		restored.SetCallback(tx.timerDHdlr(ctx))
		tx.tmrD.Store(restored)
	}

	if tmr := snap.TimerM; tmr != nil {
		restored, err := timeutil.RestoreTimer(tmr)
		if err != nil {
			return errors.Wrap(err)
		}

		restored.SetCallback(tx.timerMHdlr(ctx))
		tx.tmrM.Store(restored)
	}

	return nil
}
