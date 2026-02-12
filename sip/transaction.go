package sip

import (
	"context"
	"log/slog"
	"reflect"
	"sync/atomic"

	"braces.dev/errtrace"
	"github.com/qmuntal/stateless"

	"github.com/ghettovoice/gosip/internal/types"
)

type TransactionState string

// Transaction states.
const (
	TransactionStateTrying     TransactionState = "trying"
	TransactionStateCalling    TransactionState = "calling"
	TransactionStateProceeding TransactionState = "proceeding"
	TransactionStateAccepted   TransactionState = "accepted"
	TransactionStateCompleted  TransactionState = "completed"
	TransactionStateConfirmed  TransactionState = "confirmed"
	TransactionStateTerminated TransactionState = "terminated"
)

type TransactionType string

// Transaction types.
const (
	TransactionTypeClientInvite    TransactionType = "client_invite"
	TransactionTypeClientNonInvite TransactionType = "client_non_invite"
	TransactionTypeServerInvite    TransactionType = "server_invite"
	TransactionTypeServerNonInvite TransactionType = "server_non_invite"
)

// Transaction is a generic SIP transaction.
type Transaction interface {
	// Type returns the transaction type.
	Type() TransactionType
	// State returns the current state of the transaction.
	State() TransactionState
	// MatchMessage checks whether the message matches the transaction.
	MatchMessage(msg Message) bool
	// OnStateChanged binds a callback to be called when the transaction state changes.
	// The callback can be unbound by calling the returned cancel function.
	OnStateChanged(fn TransactionStateHandler) (unbind func())
	// OnError binds a callback to be called when the transaction encounters an transport or timeout error.
	// The callback can be unbound by calling the returned cancel function.
	OnError(fn ErrorHandler) (unbind func())
	// Terminate forces the transaction to terminate immediately switching it
	// to the [TransactionStateTerminated] state.
	Terminate(ctx context.Context) error
}

const transactCtxKey types.ContextKey = "transaction"

func ContextWithTransaction(ctx context.Context, tx Transaction) context.Context {
	return context.WithValue(ctx, transactCtxKey, tx)
}

func TransactionFromContext(ctx context.Context) (Transaction, bool) {
	tx, ok := ctx.Value(transactCtxKey).(Transaction)
	return tx, ok
}

type baseTransact struct {
	typ   TransactionType
	impl  transactImpl
	fsm   *stateless.StateMachine
	state atomic.Value // TransactionState
	log   *slog.Logger

	onStateChanged types.CallbackManager[TransactionStateHandler]
	pendingStates  types.Deque[pendingState]

	onErr       types.CallbackManager[ErrorHandler]
	pendingErrs types.Deque[pendingError]
}

type transactImpl Transaction

type pendingState struct {
	ctx        context.Context
	transition stateless.Transition
}

type pendingError struct {
	ctx context.Context
	err error
}

func newBaseTransact(typ TransactionType, impl transactImpl, log *slog.Logger) *baseTransact {
	return &baseTransact{
		typ:  typ,
		impl: impl,
		log:  log,
	}
}

// Type returns the transaction type.
func (tx *baseTransact) Type() TransactionType {
	if tx == nil {
		return ""
	}
	return tx.typ
}

// State returns the current state of the transaction.
func (tx *baseTransact) State() TransactionState {
	if tx == nil {
		return ""
	}
	return tx.state.Load().(TransactionState) //nolint:forcetypeassert
}

// OnStateChanged binds the callback to be called when the transaction state changes.
//
// The callback can be unbound by calling the returned cancel function.
// Multiple callbacks are allowed, they will be called in the order they were registered.
// Context passed to the callback is canceled when the transaction is terminated.
func (tx *baseTransact) OnStateChanged(fn TransactionStateHandler) (unbind func()) {
	defer tx.deliverPendingStates()
	return tx.onStateChanged.Add(fn)
}

func (tx *baseTransact) deliverPendingStates() {
	states := tx.pendingStates.Drain()
	if len(states) == 0 {
		return
	}

	for fn := range tx.onStateChanged.All() {
		for _, e := range states {
			fn(e.ctx, e.transition.Source.(TransactionState), e.transition.Destination.(TransactionState)) //nolint:forcetypeassert
		}
	}
}

func (tx *baseTransact) passStateTransition(ctx context.Context, tr stateless.Transition) {
	tx.pendingStates.Append(pendingState{ctx, tr})
	if tx.onStateChanged.Len() > 0 {
		tx.deliverPendingStates()
	}
}

// OnError binds the callback to be called when the transaction encounters an error.
// The error can be a transport error (usually [net.Error]) or a [ErrTransactionTimedOut].
//
// The callback can be unbound by calling the returned cancel function.
// Multiple callbacks are allowed, they will be called in the order they were registered.
func (tx *baseTransact) OnError(fn ErrorHandler) (unbind func()) {
	defer tx.deliverPendingErrs()
	return tx.onErr.Add(fn)
}

func (tx *baseTransact) deliverPendingErrs() {
	errs := tx.pendingErrs.Drain()
	if len(errs) == 0 {
		return
	}

	for fn := range tx.onErr.All() {
		for _, e := range errs {
			fn(e.ctx, errtrace.Wrap(e.err))
		}
	}
}

func (tx *baseTransact) passErr(ctx context.Context, err error) {
	tx.pendingErrs.Append(pendingError{ctx, errtrace.Wrap(err)})
	if tx.onErr.Len() > 0 {
		tx.deliverPendingErrs()
	}
}

const (
	txEvtTranspErr = "transp_err"
	txEvtTerminate = "terminate"
)

//nolint:unparam
func (tx *baseTransact) initFSM(start TransactionState) error {
	tx.state.Store(start)
	tx.fsm = stateless.NewStateMachineWithExternalStorage(
		func(context.Context) (stateless.State, error) {
			return tx.state.Load(), nil
		},
		func(_ context.Context, state stateless.State) error {
			tx.state.Store(state)
			return nil
		},
		stateless.FiringQueued,
	)

	tx.fsm.SetTriggerParameters(txEvtTranspErr, reflect.TypeFor[error]())

	tx.fsm.OnTransitioned(func(ctx context.Context, transition stateless.Transition) {
		tx.log.LogAttrs(ctx, slog.LevelDebug,
			"transaction state changed",
			slog.Any("transaction", tx.impl),
			slog.Any("from", transition.Source),
			slog.Any("to", transition.Destination),
		)

		tx.passStateTransition(ctx, transition)
	})

	tx.fsm.OnUnhandledTrigger(func(context.Context, stateless.State, stateless.Trigger, []string) error {
		return errtrace.Wrap(ErrActionNotAllowed)
	})

	return nil
}

func (*baseTransact) actNoop(context.Context, ...any) error { return nil }

func (tx *baseTransact) actTranspErr(ctx context.Context, args ...any) error {
	err := args[0].(error) //nolint:forcetypeassert

	tx.log.LogAttrs(ctx, slog.LevelDebug,
		"transport error occurred",
		slog.Any("transaction", tx.impl),
		slog.Any("error", err),
	)

	tx.passErr(ctx, errtrace.Wrap(err))
	return nil
}

func (tx *baseTransact) actTimedOut(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction timed out", slog.Any("transaction", tx.impl))

	tx.passErr(ctx, errtrace.Wrap(ErrTransactionTimedOut))
	return nil
}

//nolint:unparam
func (tx *baseTransact) actTerminated(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction terminated", slog.Any("transaction", tx.impl))

	return nil
}

// Terminate forces the transaction to terminate immediately switching it
// to the [TransactionStateTerminated] state.
func (tx *baseTransact) Terminate(ctx context.Context) error {
	if tx.State() == TransactionStateTerminated {
		return nil
	}
	return errtrace.Wrap(tx.fsm.FireCtx(ContextWithTransaction(ctx, tx.impl), txEvtTerminate))
}

func SendRequestStateful(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	txf ClientTransactionFactory,
	opts *SendRequestOptions,
) (ClientTransaction, error) {
	// TODO: implement
	panic("not implemented")
}

func RespondStateful(
	ctx context.Context,
	txf ServerTransactionFactory,
	tp ServerTransport,
	req *InboundRequestEnvelope,
	sts ResponseStatus,
	opts *RespondOptions,
) (ServerTransaction, error) {
	// TODO: review this later
	res, err := req.NewResponse(sts, opts.resOpts())
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	tx, err := txf.NewServerTransaction(ctx, req, tp, nil)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	if err := tx.SendResponse(ctx, res, opts.sendOpts()); err != nil {
		return nil, errtrace.Wrap(err)
	}
	return tx, nil
}
