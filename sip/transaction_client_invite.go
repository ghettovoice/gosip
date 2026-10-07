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
	req *RequestEnvelope,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (*InviteClientTransaction, error) {
	if err := req.Validate(); err != nil {
		return nil, errors.Wrap(err)
	}
	if !req.Method().Equal(RequestMethodInvite) {
		return nil, errors.Wrap(NewRequestMethodNotAllowedError())
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

// Start activates the invite client transaction.
// For a new transaction it sends the initial request and starts timers.
// For a restored transaction it only activates the preserved timers without
// replaying the initial send or entry actions.
// It must be called exactly once after the transaction is created or restored.
func (tx *InviteClientTransaction) Start(ctx context.Context) error {
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
			err = tx.actCalling(ctx)
		} else {
			err = errors.Wrap(NewTransactionActionNotAllowedError())
		}
	}

	return errors.Wrap(err)
}

func (tx *InviteClientTransaction) activateRestored(ctx context.Context) {
	tx.lcMu.Lock()

	if tmr := tx.tmrB.Load(); tmr != nil && tmr.Expired() &&
		tx.State() == TransactionStateCalling && !tx.isTerminated() {
		tx.lcMu.Unlock()
		tx.onTimerB(ctx, tmr)
		tx.activate(ctx)
		return
	}
	if tmr := tx.tmrD.Load(); tmr != nil && tmr.Expired() &&
		tx.State() == TransactionStateCompleted && !tx.isTerminated() {
		tx.lcMu.Unlock()
		tx.onTimerD(ctx, tmr)
		tx.activate(ctx)
		return
	}
	if tmr := tx.tmrM.Load(); tmr != nil && tmr.Expired() &&
		tx.State() == TransactionStateAccepted && !tx.isTerminated() {
		tx.lcMu.Unlock()
		tx.onTimerM(ctx, tmr)
		tx.activate(ctx)
		return
	}

	if !tx.isTerminated() {
		tx.armTmrLocked(&tx.tmrA, tx.tmrA.Load(), []TransactionState{TransactionStateCalling},
			func(tmr *timeutil.Timer) { tx.onTimerA(ctx, tmr) })
		tx.armTmrLocked(&tx.tmrB, tx.tmrB.Load(), []TransactionState{TransactionStateCalling},
			func(tmr *timeutil.Timer) { tx.onTimerB(ctx, tmr) })
		tx.armTmrLocked(&tx.tmrD, tx.tmrD.Load(), []TransactionState{TransactionStateCompleted},
			func(tmr *timeutil.Timer) { tx.onTimerD(ctx, tmr) })
		tx.armTmrLocked(&tx.tmrM, tx.tmrM.Load(), []TransactionState{TransactionStateAccepted},
			func(tmr *timeutil.Timer) { tx.onTimerM(ctx, tmr) })
	}
	tx.activateLocked()
	tx.lcMu.Unlock()
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

	tx.log.LogAttrs(
		ctx, slog.LevelDebug, "send request",
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
		tmr := timeutil.NewTimer(tx.timing.TimeA())
		if tx.armTmr(&tx.tmrA, tmr, []TransactionState{TransactionStateCalling},
			func(tmr *timeutil.Timer) { tx.onTimerA(ctx, tmr) }) {
			tx.log.LogAttrs(
				ctx, slog.LevelDebug, "timer A started",
				slog.Any("transaction", tx),
				slog.Time("expires_at", time.Now().Add(tmr.Left())),
			)
		}
	}

	tmr := timeutil.NewTimer(tx.timing.TimeB())
	if tx.armTmr(&tx.tmrB, tmr, []TransactionState{TransactionStateCalling},
		func(tmr *timeutil.Timer) { tx.onTimerB(ctx, tmr) }) {
		tx.log.LogAttrs(
			ctx, slog.LevelDebug, "timer B started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	return nil
}

func (tx *InviteClientTransaction) onTimerA(ctx context.Context, tmr *timeutil.Timer) {
	defer tx.saveSnapshot()
	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A expired", slog.Any("transaction", tx))

	tx.fireTmrTrigger(ctx, txEvtTimerA)

	if tx.resetTmr(&tx.tmrA, tmr, 2*tmr.Duration(), TransactionStateCalling) {
		tx.log.LogAttrs(
			ctx, slog.LevelDebug, "timer A reset",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}
}

func (tx *InviteClientTransaction) onTimerB(ctx context.Context, tmr *timeutil.Timer) {
	defer tx.saveSnapshot()
	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B expired", slog.Any("transaction", tx))

	tx.clearTmr(&tx.tmrB, tmr)
	tx.fireTmrTrigger(ctx, txEvtTimerB)
}

func (tx *InviteClientTransaction) actProceeding(ctx context.Context, args ...any) error {
	_ = tx.clientTransact.actProceeding(ctx, args...)

	if tx.stopTmr(&tx.tmrA) {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A stopped", slog.Any("transaction", tx))
	}

	if tx.stopTmr(&tx.tmrB) {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B stopped", slog.Any("transaction", tx))
	}

	return nil
}

func (tx *InviteClientTransaction) actCompleted(ctx context.Context, args ...any) error {
	_ = tx.clientTransact.actCompleted(ctx, args...)

	if tx.stopTmr(&tx.tmrA) {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A stopped", slog.Any("transaction", tx))
	}

	if tx.stopTmr(&tx.tmrB) {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B stopped", slog.Any("transaction", tx))
	}

	var timeD time.Duration
	if !tx.tp.Metadata().Reliable() {
		timeD = tx.timing.timeD()
	}
	tmr := timeutil.NewTimer(timeD)
	if tx.armTmr(&tx.tmrD, tmr, []TransactionState{TransactionStateCompleted},
		func(tmr *timeutil.Timer) { tx.onTimerD(ctx, tmr) }) {
		tx.log.LogAttrs(
			ctx, slog.LevelDebug, "timer D started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	return nil
}

func (tx *InviteClientTransaction) onTimerD(ctx context.Context, tmr *timeutil.Timer) {
	defer tx.saveSnapshot()
	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer D expired", slog.Any("transaction", tx))

	tx.clearTmr(&tx.tmrD, tmr)
	tx.fireTmrTrigger(ctx, txEvtTimerD)
}

func (tx *InviteClientTransaction) actAccepted(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction accepted", slog.Any("transaction", tx))

	if tx.stopTmr(&tx.tmrA) {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A stopped", slog.Any("transaction", tx))
	}

	if tx.stopTmr(&tx.tmrB) {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B stopped", slog.Any("transaction", tx))
	}

	tmr := timeutil.NewTimer(tx.timing.TimeM())
	if tx.armTmr(&tx.tmrM, tmr, []TransactionState{TransactionStateAccepted},
		func(tmr *timeutil.Timer) { tx.onTimerM(ctx, tmr) }) {
		tx.log.LogAttrs(
			ctx, slog.LevelDebug, "timer M started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	return nil
}

func (tx *InviteClientTransaction) onTimerM(ctx context.Context, tmr *timeutil.Timer) {
	defer tx.saveSnapshot()
	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer M expired", slog.Any("transaction", tx))

	tx.clearTmr(&tx.tmrM, tmr)
	tx.fireTmrTrigger(ctx, txEvtTimerM)
}

func (tx *InviteClientTransaction) actTerminated(ctx context.Context, args ...any) error {
	_ = tx.clientTransact.actTerminated(ctx, args...)

	if tx.stopTmr(&tx.tmrA) {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A stopped", slog.Any("transaction", tx))
	}

	if tx.stopTmr(&tx.tmrB) {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B stopped", slog.Any("transaction", tx))
	}

	if tx.stopTmr(&tx.tmrD) {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer D stopped", slog.Any("transaction", tx))
	}

	if tx.stopTmr(&tx.tmrM) {
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
	snap *ClientTransactionSnapshot,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (*InviteClientTransaction, error) {
	// Timers are restored without callbacks and activated by Start.
	if err := snap.validate(TransactionTypeClientInvite); err != nil {
		return nil, errors.Wrap(err)
	}

	o := util.LastSliceElemOr(opts, ClientTransactionOptions{})
	o.SendOptions = snap.SendOptions
	o.Timing = snap.Timing

	req := snap.Request.Clone().(*RequestEnvelope) //nolint:forcetypeassert

	tx := new(InviteClientTransaction)
	clnTx, err := newClientTransact(TransactionTypeClientInvite, tx, req, tp, o)
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

func (tx *InviteClientTransaction) restoreTimers(snap *ClientTransactionSnapshot) error {
	for dst, tmrSnap := range map[*atomic.Pointer[timeutil.Timer]]*timeutil.TimerSnapshot{
		&tx.tmrA: snap.TimerA,
		&tx.tmrB: snap.TimerB,
		&tx.tmrD: snap.TimerD,
		&tx.tmrM: snap.TimerM,
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
