package sip

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/timeutil"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/internal/util"
)

// InviteServerTransaction represents an invite server transaction.
// It implements the server transaction FSM defined in RFC 3261 section 17.2.1
// and patches from RFC 6026.
type InviteServerTransaction struct {
	*baseSrvTransact

	tmr1xx atomic.Pointer[timeutil.Timer]
	tmrG   atomic.Pointer[timeutil.Timer]
	tmrH   atomic.Pointer[timeutil.Timer]
	tmrI   atomic.Pointer[timeutil.Timer]
	tmrL   atomic.Pointer[timeutil.Timer]

	onAck       shared.CallbackManager[InboundRequestHandler]
	pendingAcks shared.Queue[pendingTxAck]
}

var (
	_ ServerTransaction            = (*InviteServerTransaction)(nil)
	_ ServerTransactionSnapshotter = (*InviteServerTransaction)(nil)
)

type InboundRequestHandler interface {
	HandleInboundRequest(ctx context.Context, req *RequestEnvelope)
}

type InboundRequestHandlerFunc func(ctx context.Context, req *RequestEnvelope)

func (f InboundRequestHandlerFunc) HandleInboundRequest(ctx context.Context, req *RequestEnvelope) {
	f(ctx, req)
}

type pendingTxAck struct {
	ctx context.Context //nolint:containedctx // the pending handler needs the originating ctx
	ack *RequestEnvelope
}

// NewInviteServerTransaction creates an initialized invite server transaction.
//
// Request expected to be a valid SIP request with INVITE method.
// Transport expected to be a non-nil server transport.
// Options are optional and can be nil, in which case default options will be used.
// Transaction key will be filled from the request automatically if not specified in the options.
func NewInviteServerTransaction(
	req *RequestEnvelope,
	tp ServerTransport,
	opts ...ServerTransactionOptions,
) (*InviteServerTransaction, error) {
	if err := req.Validate(); err != nil {
		return nil, errors.Wrap(err)
	}

	if !req.Method().Equal(RequestMethodInvite) {
		return nil, errors.Wrap(NewRequestMethodNotAllowedError())
	}

	o := util.LastSliceElem(opts, ServerTransactionOptions{})

	tx := new(InviteServerTransaction)
	base, err := newBaseSrvTransact(TransactionTypeServerInvite, tx, req, tp, o)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	tx.baseSrvTransact = base
	if err := tx.initFSM(TransactionStateProceeding); err != nil {
		return nil, errors.Wrap(err)
	}

	return tx, nil
}

// Start activates the invite server transaction.
// For a new transaction it runs the initial proceeding action (100 Trying timer).
// For a restored transaction it only activates the preserved timers without
// replaying entry actions.
// It must be called exactly once after the transaction is created or restored.
func (tx *InviteServerTransaction) Start(ctx context.Context) error {
	ctx = ContextWithTransaction(ctx, tx.impl)

	if !tx.admitStart(ctx) {
		return errors.Wrap(NewTransactionActionNotAllowedError())
	}

	defer tx.finishStart(ctx)

	if tx.isTerminated() {
		return errors.Wrap(NewTransactionActionNotAllowedError())
	}

	var err error
	if tx.restored {
		tx.activateRestored(ctx)
	} else {
		err = tx.actProceeding(ctx)
		tx.activate(ctx)
	}

	return errors.Wrap(err)
}

func (tx *InviteServerTransaction) activateRestored(ctx context.Context) {
	tx.lcMu.Lock()

	if tmr := tx.tmrH.Load(); tmr != nil && tmr.Expired() &&
		tx.State() == TransactionStateCompleted && !tx.isTerminated() {
		tx.lcMu.Unlock()
		tx.onTmrH(ctx, tmr)
		tx.activate(ctx)
		return
	}
	if tmr := tx.tmrI.Load(); tmr != nil && tmr.Expired() &&
		tx.State() == TransactionStateConfirmed && !tx.isTerminated() {
		tx.lcMu.Unlock()
		tx.onTmrI(ctx, tmr)
		tx.activate(ctx)
		return
	}
	if tmr := tx.tmrL.Load(); tmr != nil && tmr.Expired() &&
		tx.State() == TransactionStateAccepted && !tx.isTerminated() {
		tx.lcMu.Unlock()
		tx.onTmrL(ctx, tmr)
		tx.activate(ctx)
		return
	}

	if !tx.isTerminated() {
		tx.armTmrLocked(
			&tx.tmr1xx, tx.tmr1xx.Load(),
			[]TransactionState{TransactionStateProceeding},
			func(tmr *timeutil.Timer) { tx.onTmr1xx(ctx, tmr) },
		)
		tx.armTmrLocked(
			&tx.tmrG, tx.tmrG.Load(),
			[]TransactionState{TransactionStateCompleted},
			func(tmr *timeutil.Timer) { tx.onTmrG(ctx, tmr) },
		)
		tx.armTmrLocked(
			&tx.tmrH, tx.tmrH.Load(),
			[]TransactionState{TransactionStateCompleted},
			func(tmr *timeutil.Timer) { tx.onTmrH(ctx, tmr) },
		)
		tx.armTmrLocked(
			&tx.tmrI, tx.tmrI.Load(),
			[]TransactionState{TransactionStateConfirmed},
			func(tmr *timeutil.Timer) { tx.onTmrI(ctx, tmr) },
		)
		tx.armTmrLocked(
			&tx.tmrL, tx.tmrL.Load(),
			[]TransactionState{TransactionStateAccepted},
			func(tmr *timeutil.Timer) { tx.onTmrL(ctx, tmr) },
		)
	}
	tx.activateLocked()
	tx.lcMu.Unlock()
}

func (tx *InviteServerTransaction) LogValue() slog.Value {
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
	txEvtRecvAck = "recv_ack"
	txEvtTmr1xx  = "timer_1xx"
	txEvtTmrG    = "timer_g"
	txEvtTmrH    = "timer_h"
	txEvtTmrI    = "timer_i"
	txEvtTmrL    = "timer_l"
)

func (tx *InviteServerTransaction) initFSM(start TransactionState) error {
	if err := tx.baseSrvTransact.initFSM(start); err != nil {
		return errors.Wrap(err)
	}

	tx.fsm.SetTriggerParameters(txEvtRecvAck, reflect.TypeFor[*RequestEnvelope]())

	tx.fsm.Configure(TransactionStateProceeding).
		InternalTransition(txEvtRecvReq, tx.actResendRes).
		InternalTransition(txEvtSend1xx, tx.actSendRes).
		InternalTransition(txEvtTmr1xx, tx.actSend100).
		InternalTransition(txEvtTpErr, tx.actTranspErr).
		Permit(txEvtSend2xx, TransactionStateAccepted).
		Permit(txEvtSend300699, TransactionStateCompleted).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateAccepted).
		OnEntry(tx.actAccepted).
		OnEntryFrom(txEvtSend2xx, tx.actSendRes).
		InternalTransition(txEvtRecvReq, tx.actNoop).
		InternalTransition(txEvtRecvAck, tx.actPassAck).
		InternalTransition(txEvtSend2xx, tx.actSendRes).
		InternalTransition(txEvtTpErr, tx.actTranspErr).
		Permit(txEvtTmrL, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateCompleted).
		OnEntry(tx.actCompleted).
		OnEntryFrom(txEvtSend300699, tx.actSendRes).
		InternalTransition(txEvtRecvReq, tx.actResendRes).
		InternalTransition(txEvtTmrG, tx.actResendRes).
		InternalTransition(txEvtTpErr, tx.actTranspErr).
		Permit(txEvtRecvAck, TransactionStateConfirmed).
		Permit(txEvtTmrH, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateConfirmed).
		OnEntry(tx.actConfirmed).
		InternalTransition(txEvtRecvReq, tx.actNoop).
		InternalTransition(txEvtRecvAck, tx.actNoop).
		Permit(txEvtTmrI, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateTerminated).
		OnEntry(tx.actTerminated).
		OnEntryFrom(txEvtTmrH, tx.actTimedOut).
		OnEntryFrom(txEvtTerminate, tx.actTermErr).
		InternalTransition(txEvtTerminate, tx.actNoop)

	return nil
}

func (tx *InviteServerTransaction) actSend100(ctx context.Context, _ ...any) error {
	res, err := tx.req.NewResponse(ResponseStatusTrying)
	if err != nil {
		// Request is always valid, so this should never happen.
		panic(errors.ErrorfWrap("create auto %q response: %w", ResponseStatusTrying, err))
	}

	tx.log.DebugContext(
		ctx, "send response",
		slog.Any("transaction", tx),
		slog.Any("response", res),
	)

	_ = tx.sendRes(ctx, res, SendResponseOptions{})
	return nil
}

func (tx *InviteServerTransaction) actSendRes(ctx context.Context, args ...any) error {
	if tx.stopTmr(&tx.tmr1xx) {
		tx.log.DebugContext(ctx, "1xx timer stopped", slog.Any("transaction", tx))
	}
	return errors.Wrap(tx.baseSrvTransact.actSendRes(ctx, args...))
}

func (tx *InviteServerTransaction) actPassAck(ctx context.Context, args ...any) error {
	ack := args[0].(*RequestEnvelope) //nolint:forcetypeassert // the concrete type is fixed by construction

	tx.log.DebugContext(ctx, "pass ACK",
		slog.Any("transaction", tx),
		slog.Any("ack", ack),
	)

	tx.pendingAcks.Push(pendingTxAck{ctx, ack})
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

	for _, v := range acks {
		for fn := range tx.onAck.All() {
			fn.HandleInboundRequest(v.ctx, v.ack)
		}
	}
}

//nolint:unparam // uniform signature with the other FSM actions
func (tx *InviteServerTransaction) actProceeding(ctx context.Context, args ...any) error {
	_ = tx.baseSrvTransact.actProceeding(ctx, args...)

	tmr := timeutil.NewTimer(tx.timing.time100())
	if tx.armTmr(
		&tx.tmr1xx, tmr,
		[]TransactionState{TransactionStateProceeding},
		func(tmr *timeutil.Timer) { tx.onTmr1xx(ctx, tmr) },
	) {
		tx.log.DebugContext(ctx, "1xx timer started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	return nil
}

func (tx *InviteServerTransaction) onTmr1xx(ctx context.Context, tmr *timeutil.Timer) {
	tx.beginSnapshotUpdate()
	defer tx.finishSnapshotUpdate(ctx)

	tx.log.DebugContext(ctx, "1xx timer expired", slog.Any("transaction", tx))

	tx.clearTmr(&tx.tmr1xx, tmr)
	tx.fireTmrTrigger(ctx, txEvtTmr1xx)
}

func (tx *InviteServerTransaction) actAccepted(ctx context.Context, _ ...any) error {
	tx.log.DebugContext(ctx, "transaction accepted", slog.Any("transaction", tx))

	tmr := timeutil.NewTimer(tx.timing.TimeL())
	if tx.armTmr(
		&tx.tmrL, tmr,
		[]TransactionState{TransactionStateAccepted},
		func(tmr *timeutil.Timer) { tx.onTmrL(ctx, tmr) },
	) {
		tx.log.DebugContext(ctx, "timer L started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	return nil
}

func (tx *InviteServerTransaction) onTmrL(ctx context.Context, tmr *timeutil.Timer) {
	tx.beginSnapshotUpdate()
	defer tx.finishSnapshotUpdate(ctx)

	tx.log.DebugContext(ctx, "timer L expired", slog.Any("transaction", tx))

	tx.clearTmr(&tx.tmrL, tmr)
	tx.fireTmrTrigger(ctx, txEvtTmrL)
}

func (tx *InviteServerTransaction) actCompleted(ctx context.Context, args ...any) error {
	_ = tx.baseSrvTransact.actCompleted(ctx, args...)

	if !tx.tp.Metadata().Reliable() {
		tmr := timeutil.NewTimer(tx.timing.TimeG())
		if tx.armTmr(
			&tx.tmrG, tmr,
			[]TransactionState{TransactionStateCompleted},
			func(tmr *timeutil.Timer) { tx.onTmrG(ctx, tmr) },
		) {
			tx.log.DebugContext(ctx, "timer G started",
				slog.Any("transaction", tx),
				slog.Time("expires_at", time.Now().Add(tmr.Left())),
			)
		}
	}

	tmr := timeutil.NewTimer(tx.timing.TimeH())
	if tx.armTmr(
		&tx.tmrH, tmr,
		[]TransactionState{TransactionStateCompleted},
		func(tmr *timeutil.Timer) { tx.onTmrH(ctx, tmr) },
	) {
		tx.log.DebugContext(ctx, "timer H started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	return nil
}

func (tx *InviteServerTransaction) onTmrG(ctx context.Context, tmr *timeutil.Timer) {
	tx.beginSnapshotUpdate()
	defer tx.finishSnapshotUpdate(ctx)

	tx.log.DebugContext(ctx, "timer G expired", slog.Any("transaction", tx))

	tx.fireTmrTrigger(ctx, txEvtTmrG)

	if tx.resetTmr(&tx.tmrG, tmr, min(2*tmr.Duration(), tx.timing.t2()), TransactionStateCompleted) {
		tx.log.DebugContext(ctx, "timer G reset",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}
}

func (tx *InviteServerTransaction) onTmrH(ctx context.Context, tmr *timeutil.Timer) {
	tx.beginSnapshotUpdate()
	defer tx.finishSnapshotUpdate(ctx)

	tx.log.DebugContext(ctx, "timer H expired", slog.Any("transaction", tx))

	tx.clearTmr(&tx.tmrH, tmr)
	tx.fireTmrTrigger(ctx, txEvtTmrH)
}

func (tx *InviteServerTransaction) actConfirmed(ctx context.Context, _ ...any) error {
	tx.log.DebugContext(ctx, "transaction confirmed", slog.Any("transaction", tx))

	if tx.stopTmr(&tx.tmrH) {
		tx.log.DebugContext(ctx, "timer H stopped", slog.Any("transaction", tx))
	}

	if tx.stopTmr(&tx.tmrG) {
		tx.log.DebugContext(ctx, "timer G stopped", slog.Any("transaction", tx))
	}

	var timeI time.Duration
	if !tx.tp.Metadata().Reliable() {
		timeI = tx.timing.TimeI()
	}
	tmr := timeutil.NewTimer(timeI)
	if tx.armTmr(
		&tx.tmrI, tmr,
		[]TransactionState{TransactionStateConfirmed},
		func(tmr *timeutil.Timer) { tx.onTmrI(ctx, tmr) },
	) {
		tx.log.DebugContext(ctx, "timer I started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	return nil
}

func (tx *InviteServerTransaction) onTmrI(ctx context.Context, tmr *timeutil.Timer) {
	tx.beginSnapshotUpdate()
	defer tx.finishSnapshotUpdate(ctx)

	tx.log.DebugContext(ctx, "timer I expired", slog.Any("transaction", tx))

	tx.clearTmr(&tx.tmrI, tmr)
	tx.fireTmrTrigger(ctx, txEvtTmrI)
}

func (tx *InviteServerTransaction) actTerminated(ctx context.Context, args ...any) error {
	_ = tx.baseSrvTransact.actTerminated(ctx, args...)

	if tx.stopTmr(&tx.tmr1xx) {
		tx.log.DebugContext(ctx, "1xx timer stopped", slog.Any("transaction", tx))
	}

	// timer G can be active after transition to here by timer H
	if tx.stopTmr(&tx.tmrG) {
		tx.log.DebugContext(ctx, "timer G stopped", slog.Any("transaction", tx))
	}

	if tx.stopTmr(&tx.tmrH) {
		tx.log.DebugContext(ctx, "timer H stopped", slog.Any("transaction", tx))
	}

	if tx.stopTmr(&tx.tmrI) {
		tx.log.DebugContext(ctx, "timer I stopped", slog.Any("transaction", tx))
	}

	if tx.stopTmr(&tx.tmrL) {
		tx.log.DebugContext(ctx, "timer L stopped", slog.Any("transaction", tx))
	}

	return nil
}

func (tx *InviteServerTransaction) adjustTxKeys(txKey, reqKey *ServerTransactionKey, reqMtd RequestMethod) {
	if reqMtd.Equal(RequestMethodAck) {
		reqKey.Method = string(RequestMethodInvite)
	}

	// set tx key To tag to last response To tag for RFC 2543 matching of ACK on initial INVITE
	if !IsRFC3261Branch(txKey.Branch) && reqMtd.Equal(RequestMethodAck) && txKey.ToTag == "" {
		if res := tx.lastRes.Load(); res != nil {
			res.WithMessage(func(r *Response) {
				to, _ := r.Headers.To()
				txKey.ToTag, _ = to.Tag()
			})
		}
	}
}

func (tx *InviteServerTransaction) recvReq(ctx context.Context, req *RequestEnvelope) error {
	if req.Method().Equal(RequestMethodAck) {
		return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtRecvAck, req))
	}
	return errors.Wrap(tx.baseSrvTransact.recvReq(ctx, req))
}

// BindAckHandler binds the callback to be called when the transaction receives an 2xx ACK.
//
// 2xx ACK can be matched to the INVITE transaction only by RFC 2543 matching rules,
// so this callback here only for backward compatibility with old clients.
// 2xx ACK from RFC 3261 always goes outside of the INVITE transaction.
//
// The callback can be unbound by calling the returned cancel function.
// Multiple callbacks are allowed, they will be called in the order they were registered.
// Context passed to the callback will be the context passed to the [InviteServerTransaction.RecvRequest] method.
func (tx *InviteServerTransaction) BindAckHandler(fn InboundRequestHandler) (unbind func()) {
	defer tx.deliverPendingAcks()
	return tx.onAck.Add(fn)
}

func (tx *InviteServerTransaction) takeSnapshot() *ServerTransactionSnapshot {
	lastRes := tx.LastResponse()
	return &ServerTransactionSnapshot{
		Time:                  time.Now(),
		Type:                  tx.Type(),
		State:                 tx.State(),
		Key:                   tx.Key(),
		Request:               tx.Request(),
		LastResponse:          lastRes,
		SendOptions:           tx.sendOpts.Load().(SendResponseOptions), //nolint:forcetypeassert // the concrete type is fixed by construction
		PreserveResponseToTag: resPreservesToTag(lastRes),
		Timing:                tx.timing,
		Timer1xx:              tx.tmr1xx.Load().Snapshot(),
		TimerG:                tx.tmrG.Load().Snapshot(),
		TimerH:                tx.tmrH.Load().Snapshot(),
		TimerI:                tx.tmrI.Load().Snapshot(),
		TimerL:                tx.tmrL.Load().Snapshot(),
	}
}

// RestoreInviteServerTransaction restores a dormant invite server transaction
// from a snapshot.
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
func RestoreInviteServerTransaction(
	snap *ServerTransactionSnapshot,
	tp ServerTransport,
	opts ...ServerTransactionOptions,
) (*InviteServerTransaction, error) {
	// Timers are restored without callbacks and activated by Start.
	if err := snap.validate(TransactionTypeServerInvite); err != nil {
		return nil, errors.Wrap(err)
	}

	o := util.LastSliceElem(opts, ServerTransactionOptions{})
	o.Timing = snap.Timing

	req := snap.Request.Clone().(*RequestEnvelope) //nolint:forcetypeassert // the concrete type is fixed by construction

	tx := new(InviteServerTransaction)
	base, err := newBaseSrvTransact(TransactionTypeServerInvite, tx, req, tp, o)
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

func (tx *InviteServerTransaction) restoreTmrs(snap *ServerTransactionSnapshot) error {
	for dst, tmrSnap := range map[*atomic.Pointer[timeutil.Timer]]*timeutil.TimerSnapshot{
		&tx.tmr1xx: snap.Timer1xx,
		&tx.tmrG:   snap.TimerG,
		&tx.tmrH:   snap.TimerH,
		&tx.tmrI:   snap.TimerI,
		&tx.tmrL:   snap.TimerL,
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
