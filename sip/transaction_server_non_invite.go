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

// NonInviteServerTransaction represents a non-invite server transaction.
type NonInviteServerTransaction struct {
	*serverTransact

	tmrJ atomic.Pointer[timeutil.SerializableTimer]
}

// NewNonInviteServerTransaction creates a new non-invite server transaction and starts its state machine.
//
// Context does not affect the transaction lifecycle, it can be used to
// pass additional information to the transaction.
// Request expected to be a valid SIP request with any method except INVITE or ACK.
// Transport expected to be a non-nil server transport.
// Options are optional and can be nil, in which case default options will be used.
// Transaction key will be filled from the request automatically if not specified in the options.
func NewNonInviteServerTransaction(
	ctx context.Context,
	req *InboundRequestEnvelope,
	tp ServerTransport,
	opts *ServerTransactionOptions,
) (*NonInviteServerTransaction, error) {
	if err := req.Validate(); err != nil {
		return nil, errtrace.Wrap(NewInvalidArgumentError(err))
	}
	if mtd := req.Method(); mtd.Equal(RequestMethodInvite) || mtd.Equal(RequestMethodAck) {
		return nil, errtrace.Wrap(NewInvalidArgumentError(ErrMethodNotAllowed))
	}

	tx := new(NonInviteServerTransaction)
	srvTx, err := newServerTransact(TransactionTypeServerNonInvite, tx, req, tp, opts)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	tx.serverTransact = srvTx

	ctx = ContextWithTransaction(ctx, tx)

	if err := tx.initFSM(TransactionStateTrying); err != nil {
		return nil, errtrace.Wrap(err)
	}
	if err := tx.actTrying(ctx); err != nil {
		return nil, errtrace.Wrap(err)
	}
	return tx, nil
}

const txEvtTimerJ = "timer_J"

func (tx *NonInviteServerTransaction) initFSM(start TransactionState) error {
	if err := tx.serverTransact.initFSM(start); err != nil {
		return errtrace.Wrap(err)
	}

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
		InternalTransition(txEvtTerminate, tx.actNoop)

	return nil
}

//nolint:unparam
func (tx *NonInviteServerTransaction) actTrying(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction trying", slog.Any("transaction", tx))

	return nil
}

func (tx *NonInviteServerTransaction) actCompleted(ctx context.Context, args ...any) error {
	tx.serverTransact.actCompleted(ctx, args...) //nolint:errcheck

	var timeJ time.Duration
	if !tx.tp.Reliable() {
		timeJ = tx.timings.TimeJ()
	}
	tmr := timeutil.AfterFunc(timeJ, tx.timerJHdlr(ctx))
	tx.tmrJ.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug,
		"timer J started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *NonInviteServerTransaction) timerJHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer J expired", slog.Any("transaction", tx))

		tx.tmrJ.Store(nil)

		if tx.State() != TransactionStateCompleted {
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerJ); err != nil {
			panic(fmt.Errorf("fire %q in state %q: %w", txEvtTimerJ, tx.State(), err))
		}
	}
}

func (tx *NonInviteServerTransaction) actTerminated(ctx context.Context, args ...any) error {
	tx.serverTransact.actTerminated(ctx, args...) //nolint:errcheck

	if tmr := tx.tmrJ.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer J stopped", slog.Any("transaction", tx))
	}

	return nil
}

func (tx *NonInviteServerTransaction) takeSnapshot() *ServerTransactionSnapshot {
	return &ServerTransactionSnapshot{
		Time:         time.Now(),
		Type:         tx.typ,
		State:        tx.State(),
		Key:          tx.key,
		Request:      tx.req,
		LastResponse: tx.LastResponse(),
		SendOptions:  cloneSendResOpts(tx.sendOpts.Load()),
		Timings:      tx.timings,
		TimerJ:       tx.tmrJ.Load().Snapshot(),
	}
}

// RestoreNonInviteServerTransaction restores a non-invite server transaction from a snapshot.
//
// Context does not affect the transaction lifecycle, it can be used to
// pass additional information to the transaction.
// The snapshot contains the serialized state of the transaction.
// Transport is required to send responses.
// Options are optional and can be nil. The key field from options is ignored
// and the key from the snapshot will be used instead.
//
// After restoration, the transaction FSM will be in the state specified in the snapshot.
// Timer J will be restored and its callback reconnected to the FSM.
// If the timer has already expired according to the snapshot, it will not be restarted.
func RestoreNonInviteServerTransaction(
	ctx context.Context,
	snap *ServerTransactionSnapshot,
	tp ServerTransport,
	opts *ServerTransactionOptions,
) (*NonInviteServerTransaction, error) {
	if !snap.IsValid() || snap.Type != TransactionTypeServerNonInvite {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid snapshot"))
	}

	var restoreOpts ServerTransactionOptions
	if opts != nil {
		restoreOpts = *opts
	}
	restoreOpts.Key = snap.Key
	restoreOpts.Timings = snap.Timings

	tx := new(NonInviteServerTransaction)
	srvTx, err := newServerTransact(TransactionTypeServerNonInvite, tx, snap.Request, tp, &restoreOpts)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	tx.serverTransact = srvTx

	ctx = ContextWithTransaction(ctx, tx)

	if snap.LastResponse != nil {
		tx.lastRes.Store(snap.LastResponse)
	}
	if snap.SendOptions != nil {
		tx.sendOpts.Store(cloneSendResOpts(snap.SendOptions))
	}

	if err := tx.initFSM(snap.State); err != nil {
		return nil, errtrace.Wrap(err)
	}

	tx.restoreTimers(ctx, snap)

	return tx, nil
}

func (tx *NonInviteServerTransaction) restoreTimers(ctx context.Context, snap *ServerTransactionSnapshot) {
	if tmr := snap.TimerJ; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerJHdlr(ctx))
		tx.tmrJ.Store(restored)
	}
}
