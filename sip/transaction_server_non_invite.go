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
	_ context.Context,
	req *RequestEnvelope,
	tp ServerTransport,
	opts ...ServerTransactionOptions,
) (*NonInviteServerTransaction, error) {
	if err := req.Validate(); err != nil {
		return nil, errors.Wrap(err)
	}

	if mtd := req.Method(); mtd.Equal(RequestMethodInvite) || mtd.Equal(RequestMethodAck) {
		return nil, errors.Wrap(ErrMethodNotAllowed)
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

// Start starts the non-INVITE server transaction.
func (tx *NonInviteServerTransaction) Start(ctx context.Context) error {
	if !tx.started.CompareAndSwap(false, true) || tx.State() != TransactionStateTrying {
		return errors.Wrap(ErrActionNotAllowed)
	}
	return errors.Wrap(tx.actTrying(ctx))
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
	tmr := timeutil.AfterFunc(timeJ, tx.timerJHdlr(ctx))
	tx.tmrJ.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer J started",
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
			panic(errors.Wrap(newTxTriggerErr(txEvtTimerJ, tx.State(), err)))
		}
	}
}

func (tx *NonInviteServerTransaction) actTerminated(ctx context.Context, args ...any) error {
	_ = tx.serverTransact.actTerminated(ctx, args...)

	if tmr := tx.tmrJ.Swap(nil); tmr != nil && tmr.Stop() {
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
	opts ...ServerTransactionOptions,
) (*NonInviteServerTransaction, error) {
	if !snap.IsValid() || snap.Type != TransactionTypeServerNonInvite {
		return nil, errors.ErrorWrap("invalid snapshot")
	}

	o := util.LastSliceElemOr(opts, ServerTransactionOptions{})
	o.Timing = snap.Timing

	tx := new(NonInviteServerTransaction)
	srvTx, err := newServerTransact(TransactionTypeServerNonInvite, tx, snap.Request, tp, o)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	tx.serverTransact = srvTx
	if snap.LastResponse != nil {
		tx.lastRes.Store(snap.LastResponse)
	}
	tx.sendOpts.Store(snap.SendOptions)
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

func (tx *NonInviteServerTransaction) restoreTimers(ctx context.Context, snap *ServerTransactionSnapshot) error {
	if tmr := snap.TimerJ; tmr != nil {
		restored, err := timeutil.RestoreTimer(tmr)
		if err != nil {
			return errors.Wrap(err)
		}

		restored.SetCallback(tx.timerJHdlr(ctx))
		tx.tmrJ.Store(restored)
	}

	return nil
}
