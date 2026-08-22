package sip

import (
	"context"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"

	"github.com/qmuntal/stateless"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
)

// Transaction errors.
const (
	ErrTransactionNotFound Error = "transaction not found"
	ErrTransactionTimedOut Error = "transaction timed out"
)

// IsTransactionError reports whether err belongs to the transaction error class.
func IsTransactionError(err error) bool {
	return errors.Is(err, ErrClassTransaction)
}

type TransactionState uint

const (
	TransactionStateTrying TransactionState = iota + 1
	TransactionStateCalling
	TransactionStateProceeding
	TransactionStateAccepted
	TransactionStateCompleted
	TransactionStateConfirmed
	TransactionStateTerminated
)

var txStateNames = map[TransactionState]string{
	TransactionStateTrying:     "trying",
	TransactionStateCalling:    "calling",
	TransactionStateProceeding: "proceeding",
	TransactionStateAccepted:   "accepted",
	TransactionStateCompleted:  "completed",
	TransactionStateConfirmed:  "confirmed",
	TransactionStateTerminated: "terminated",
}

func TransactionStateFromString(s string) TransactionState {
	for state, name := range txStateNames {
		if strings.EqualFold(name, s) {
			return state
		}
	}
	return 0
}

func (s TransactionState) String() string {
	if str, ok := txStateNames[s]; ok {
		return str
	}
	return "invalid"
}

func (s TransactionState) IsValid() bool {
	_, ok := txStateNames[s]
	return ok
}

func (s TransactionState) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

func (s TransactionState) AppendText(b []byte) ([]byte, error) {
	return append(b, s.String()...), nil
}

func (s *TransactionState) UnmarshalText(data []byte) error {
	*s = TransactionStateFromString(string(data))
	return nil
}

type TransactionType uint

const (
	TransactionTypeClientInvite TransactionType = iota + 1
	TransactionTypeClientNonInvite
	TransactionTypeServerInvite
	TransactionTypeServerNonInvite
)

var txTypeNames = map[TransactionType]string{
	TransactionTypeClientInvite:    "client_invite",
	TransactionTypeClientNonInvite: "client_non_invite",
	TransactionTypeServerInvite:    "server_invite",
	TransactionTypeServerNonInvite: "server_non_invite",
}

func TransactionTypeFromString(s string) TransactionType {
	for typ, name := range txTypeNames {
		if strings.EqualFold(name, s) {
			return typ
		}
	}
	return 0
}

func (t TransactionType) String() string {
	if str, ok := txTypeNames[t]; ok {
		return str
	}
	return "invalid"
}

func (t TransactionType) IsValid() bool {
	_, ok := txTypeNames[t]
	return ok
}

func (t TransactionType) MarshalText() ([]byte, error) {
	return []byte(t.String()), nil
}

func (t TransactionType) AppendText(b []byte) ([]byte, error) {
	return append(b, t.String()...), nil
}

func (t *TransactionType) UnmarshalText(data []byte) error {
	*t = TransactionTypeFromString(string(data))
	return nil
}

// Transaction is a generic SIP transaction.
type Transaction interface {
	// Type returns the transaction type.
	Type() TransactionType
	// State returns the current state of the transaction.
	State() TransactionState
	// MatchMessage checks whether the message matches the transaction.
	MatchMessage(msg Message) bool
	// LastError returns the last error that occurred in the transaction.
	LastError() error
	// BindStateHandler binds a callback to be called when the transaction state changes.
	// The callback can be unbound by calling the returned cancel function.
	BindStateHandler(fn TransactionStateHandler) (unbind func())
	// BindErrorHandler binds a callback to be called when the transaction encounters an transport or timeout error.
	// The callback can be unbound by calling the returned cancel function.
	BindErrorHandler(fn ErrorHandler) (unbind func())
	// Terminate forces the transaction to terminate immediately switching it
	// to the [TransactionStateTerminated] state.
	Terminate(ctx context.Context, cause error) error
}

type TransactionStateHandler interface {
	HandleTransactionState(ctx context.Context, from, to TransactionState)
}

type TransactionStateHandlerFunc func(ctx context.Context, from, to TransactionState)

func (f TransactionStateHandlerFunc) HandleTransactionState(ctx context.Context, from, to TransactionState) {
	f(ctx, from, to)
}

type baseTransact struct {
	typ   TransactionType
	impl  transactImpl
	fsm   *stateless.StateMachine
	state atomic.Value // TransactionState
	log   *slog.Logger

	onStateChanged types.CallbackManager[TransactionStateHandler]
	pendingStates  types.Queue[pendingState]

	onErr       types.CallbackManager[ErrorHandler]
	pendingErrs types.Queue[pendingError]
	lastErr     atomic.Value // error
}

type transactImpl = Transaction

type pendingState struct {
	ctx        context.Context
	transition stateless.Transition
}

type pendingError struct {
	ctx context.Context
	err error
}

func newBaseTransact(typ TransactionType, impl transactImpl, logger *slog.Logger) *baseTransact {
	return &baseTransact{typ: typ, impl: impl, log: logger}
}

// Type returns the transaction type.
func (tx *baseTransact) Type() TransactionType { return tx.typ }

// State returns the current state of the transaction.
func (tx *baseTransact) State() TransactionState {
	return tx.state.Load().(TransactionState) //nolint:forcetypeassert
}

func (tx *baseTransact) Logger() *slog.Logger { return tx.log }

// BindStateHandler binds the callback to be called when the transaction state changes.
//
// The callback can be unbound by calling the returned cancel function.
// Multiple callbacks are allowed, they will be called in the order they were registered.
// Context passed to the callback is canceled when the transaction is terminated.
func (tx *baseTransact) BindStateHandler(fn TransactionStateHandler) (unbind func()) {
	defer tx.deliverPendingStates()
	return tx.onStateChanged.Add(fn)
}

func (tx *baseTransact) deliverPendingStates() {
	states := tx.pendingStates.Drain()
	if len(states) == 0 {
		return
	}

	for _, v := range states {
		for fn := range tx.onStateChanged.All() {
			//nolint:forcetypeassert
			fn.HandleTransactionState(v.ctx,
				v.transition.Source.(TransactionState),
				v.transition.Destination.(TransactionState),
			)
		}
	}
}

func (tx *baseTransact) passStateTransition(ctx context.Context, tr stateless.Transition) {
	tx.pendingStates.Push(pendingState{ctx, tr})
	if tx.onStateChanged.Len() > 0 {
		tx.deliverPendingStates()
	}
}

// BindErrorHandler binds the callback to be called when the transaction encounters an error.
// The error can be a transport error (usually [net.Error]) or a [ErrTransactionTimedOut].
//
// The callback can be unbound by calling the returned cancel function.
// Multiple callbacks are allowed, they will be called in the order they were registered.
func (tx *baseTransact) BindErrorHandler(fn ErrorHandler) (unbind func()) {
	defer tx.deliverPendingErrs()
	return tx.onErr.Add(fn)
}

func (tx *baseTransact) deliverPendingErrs() {
	errs := tx.pendingErrs.Drain()
	if len(errs) == 0 {
		return
	}

	for _, v := range errs {
		for fn := range tx.onErr.All() {
			fn.HandleError(v.ctx, errors.Wrap(v.err))
		}
	}
}

func (tx *baseTransact) passErr(ctx context.Context, err error) {
	tx.lastErr.Store(err)

	tx.pendingErrs.Push(pendingError{ctx, errors.Wrap(err)})
	if tx.onErr.Len() > 0 {
		tx.deliverPendingErrs()
	}
}

func (tx *baseTransact) LastError() error {
	if err, ok := tx.lastErr.Load().(error); ok {
		return errors.Wrap(err)
	}
	return nil
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
	tx.fsm.SetTriggerParameters(txEvtTerminate, reflect.TypeFor[error]())

	tx.fsm.OnTransitioned(func(ctx context.Context, transition stateless.Transition) {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction state changed",
			slog.Any("transaction", tx.impl),
			slog.Any("from", transition.Source),
			slog.Any("to", transition.Destination),
		)

		tx.passStateTransition(ctx, transition)
	})

	tx.fsm.OnUnhandledTrigger(func(_ context.Context, state stateless.State, trigger stateless.Trigger, _ []string) error {
		return errors.Wrap(newTxTriggerErr(trigger, state, ErrActionNotAllowed))
	})

	return nil
}

func (*baseTransact) actNoop(context.Context, ...any) error { return nil }

func (tx *baseTransact) actTranspErr(ctx context.Context, args ...any) error {
	err := args[0].(error) //nolint:forcetypeassert

	tx.log.LogAttrs(ctx, slog.LevelDebug, "transport error occurred",
		slog.Any("transaction", tx.impl),
		slog.Any("error", err),
	)

	tx.passErr(ctx, errors.Wrap(err))
	return nil
}

func (tx *baseTransact) actTimedOut(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction timed out", slog.Any("transaction", tx.impl))

	tx.passErr(ctx, errors.Wrap(ErrTransactionTimedOut))
	return nil
}

func (tx *baseTransact) actTermErr(ctx context.Context, args ...any) error {
	err := args[0].(error) //nolint:forcetypeassert

	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction terminated forcefully",
		slog.Any("transaction", tx.impl),
		slog.Any("error", err),
	)

	tx.passErr(ctx, errors.Wrap(err))
	return nil
}

//nolint:unparam
func (tx *baseTransact) actTerminated(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction terminated", slog.Any("transaction", tx.impl))
	return nil
}

// Terminate forces the transaction to terminate immediately switching it
// to the [TransactionStateTerminated] state.
// If cause is nil, it will be set to generic "undefined termination cause" error.
func (tx *baseTransact) Terminate(ctx context.Context, cause error) error {
	if tx.State() == TransactionStateTerminated {
		return nil
	}

	if cause == nil {
		cause = errors.New("undefined termination cause")
	}
	return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtTerminate, errors.Wrap(cause)))
}

const (
	txKeyMetaKey  = "sip.transaction_key"
	txTypeMetaKey = "sip.transaction_type"
)

func newTxTriggerErr(trigger stateless.Trigger, state stateless.State, err error) error {
	return errors.Errorf("trigger %q in state %q: %w", trigger, state, err)
}
