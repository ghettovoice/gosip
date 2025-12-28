package sip

import (
	"context"
	"iter"
	"log/slog"
	"reflect"
	"sync/atomic"

	"braces.dev/errtrace"
	"github.com/qmuntal/stateless"

	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/types"
)

const (
	ErrTransactionNotFound         Error = "transaction not found"
	ErrTransactionNotMatched       Error = "transaction not matched"
	ErrTransactionActionNotAllowed Error = "transaction action not allowed"
	ErrTransactionTimedOut         Error = "transaction timed out"
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
	// OnStateChanged registers a callback to be called when the transaction state changes.
	OnStateChanged(cb TransactionStateHandler) (cancel func())
	// OnError registers a callback to be called when the transaction encounters an transport or timeout error.
	OnError(cb TransactionErrorHandler) (cancel func())
	// Terminate forces the transaction to terminate.
	// This is used for internal error recovery when a transaction needs to be
	// cleaned up due to internal errors (e.g., failed to store in transaction store).
	Terminate(ctx context.Context) error
}

type TransactionStateHandler = func(ctx context.Context, tx Transaction, from, to TransactionState)

type TransactionErrorHandler = func(ctx context.Context, tx Transaction, err error)

type baseTransact struct {
	ctx       context.Context
	cancelCtx context.CancelFunc
	typ       TransactionType
	impl      transactImpl
	fsm       *stateless.StateMachine
	state     atomic.Value // TransactionState
	log       *slog.Logger

	onStateChanged types.CallbackManager[TransactionStateHandler]
	pendingStates  types.Deque[stateless.Transition]

	onErr       types.CallbackManager[TransactionErrorHandler]
	pendingErrs types.Deque[error]
}

type transactImpl any

func newBaseTransact(ctx context.Context, typ TransactionType, impl transactImpl, log *slog.Logger) *baseTransact {
	ctx, cancelCtx := context.WithCancel(ctx)
	return &baseTransact{
		ctx:       ctx,
		cancelCtx: cancelCtx,
		typ:       typ,
		impl:      impl,
		log:       log,
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

// OnStateChanged registers a callback to be called when the transaction state changes.
//
// The callback will be called with the transaction state before and after the change.
// The callback can be canceled by calling the returned cancel function.
// Multiple callbacks can be registered.
//
// The callback will be called with the transaction's context, see [Transaction.Context].
// The transaction can be retrieved from the context using [TransactionFromContext].
func (tx *baseTransact) OnStateChanged(fn TransactionStateHandler) (cancel func()) {
	cancel = tx.onStateChanged.Add(fn)
	tx.deliverPendingStates()
	return cancel
}

func (tx *baseTransact) deliverPendingStates() {
	transitions := tx.pendingStates.Drain()
	if len(transitions) == 0 {
		return
	}

	tx.onStateChanged.Range(func(fn TransactionStateHandler) {
		for _, tr := range transitions {
			fn(tx.ctx, tx.impl.(Transaction), tr.Source.(TransactionState), tr.Destination.(TransactionState))
		}
	})
}

func (tx *baseTransact) passStateTransition(tr stateless.Transition) {
	tx.pendingStates.Append(tr)
	if tx.onStateChanged.Len() > 0 {
		tx.deliverPendingStates()
	}
}

// OnError registers a callback to be called when the transaction encounters an error.
// The error can be a transport error (usually [net.Error]) or a [ErrTransactionTimedOut].
//
// The callback will be called with the error.
// The callback can be canceled by calling the returned cancel function.
// Multiple callbacks can be registered.
//
// The callback will be called with the transaction's context, see [Transaction.Context].
// The transaction can be retrieved from the context using [TransactionFromContext].
func (tx *baseTransact) OnError(fn TransactionErrorHandler) (cancel func()) {
	cancel = tx.onErr.Add(fn)
	tx.deliverPendingErrs()
	return cancel
}

func (tx *baseTransact) deliverPendingErrs() {
	errs := tx.pendingErrs.Drain()
	if len(errs) == 0 {
		return
	}

	tx.onErr.Range(func(fn TransactionErrorHandler) {
		for _, err := range errs {
			fn(tx.ctx, tx.impl.(Transaction), errtrace.Wrap(err))
		}
	})
}

func (tx *baseTransact) passErr(err error) {
	tx.pendingErrs.Append(errtrace.Wrap(err))
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

	tx.fsm.SetTriggerParameters(txEvtTranspErr, reflect.TypeOf((*error)(nil)).Elem())

	tx.fsm.OnTransitioned(func(_ context.Context, transition stateless.Transition) {
		tx.log.LogAttrs(tx.ctx, slog.LevelDebug,
			"transaction state changed",
			slog.Any("transaction", tx.impl),
			slog.Any("from", transition.Source),
			slog.Any("to", transition.Destination),
		)

		tx.passStateTransition(transition)

		if transition.Destination == TransactionStateTerminated {
			tx.cancelCtx()
		}
	})

	tx.fsm.OnUnhandledTrigger(func(_ context.Context, state stateless.State, trigger stateless.Trigger, _ []string) error {
		return errtrace.Wrap(ErrTransactionActionNotAllowed)
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

	tx.passErr(errtrace.Wrap(err))
	return nil
}

func (tx *baseTransact) actTimedOut(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction timed out", slog.Any("transaction", tx.impl))

	tx.passErr(errtrace.Wrap(ErrTransactionTimedOut))
	return nil
}

//nolint:unparam
func (tx *baseTransact) actTerminated(context.Context, ...any) error {
	tx.log.LogAttrs(tx.ctx, slog.LevelDebug, "transaction terminated", slog.Any("transaction", tx.impl))

	return nil
}

// Terminate forces the transaction to terminate.
// This is used for internal error recovery when a transaction needs to be
// cleaned up due to internal errors (e.g., failed to store in transaction store).
// The method triggers the FSM transition to Terminated state, which will
// properly stop all timers and release resources.
func (tx *baseTransact) Terminate(ctx context.Context) error {
	if tx.State() == TransactionStateTerminated {
		return nil
	}
	return errtrace.Wrap(tx.fsm.FireCtx(ctx, txEvtTerminate))
}

// TransactionStore is an interface for a generic transaction store.
type TransactionStore[K comparable, T Transaction] interface {
	// Load loads a transaction by its key.
	Load(ctx context.Context, key K) (T, error)
	// Store stores a transaction.
	Store(ctx context.Context, key K, tx T) error
	// Delete deletes a transaction by its key.
	Delete(ctx context.Context, key K) error
	// All returns all transactions.
	All(ctx context.Context) (iter.Seq2[K, T], error)
}

// MemoryTransactionStore implements TransactionStore using in-memory storage.
type MemoryTransactionStore[K comparable, T Transaction] struct {
	txs *syncutil.ShardMap[K, T]
	kmu syncutil.KeyMutex[K]
}

// NewMemoryTransactionStore creates a new in-memory transaction store.
func NewMemoryTransactionStore[K comparable, T Transaction]() *MemoryTransactionStore[K, T] {
	return &MemoryTransactionStore[K, T]{
		txs: syncutil.NewShardMap[K, T](),
	}
}

// Load loads a transaction by its key.
func (s *MemoryTransactionStore[K, T]) Load(_ context.Context, key K) (T, error) {
	unlock := s.kmu.Lock(key)
	defer unlock()

	tx, ok := s.txs.Get(key)
	if !ok {
		return tx, errtrace.Wrap(ErrTransactionNotFound)
	}
	return tx, nil
}

// Store stores a new one if it does not exist.
func (s *MemoryTransactionStore[K, T]) Store(_ context.Context, key K, tx T) error {
	unlock := s.kmu.Lock(key)
	defer unlock()

	if _, ok := s.txs.Get(key); ok {
		return nil
	}
	s.txs.Set(key, tx)
	return nil
}

// Delete deletes a transaction by its key.
func (s *MemoryTransactionStore[K, T]) Delete(_ context.Context, key K) error {
	unlock := s.kmu.Lock(key)
	defer unlock()

	if _, ok := s.txs.Del(key); !ok {
		return errtrace.Wrap(ErrTransactionNotFound)
	}
	return nil
}

func (s *MemoryTransactionStore[K, T]) All(_ context.Context) (iter.Seq2[K, T], error) {
	return s.txs.Items(), nil
}
