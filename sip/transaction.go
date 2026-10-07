package sip

import (
	"context"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/timeutil"
	"github.com/qmuntal/stateless"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
)

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
	// Started reports whether Start has been called.
	Started() bool
	// Start starts the transaction.
	// It must be called exactly once after the transaction is registered.
	Start(ctx context.Context) error
	// Terminate forces the transaction to terminate immediately switching it
	// to the [TransactionStateTerminated] state.
	Terminate(ctx context.Context, cause error) error
	// BindStartHandler binds a callback invoked when the transaction starts.
	BindStartHandler(fn TransactionStartHandler) (unbind func())
	// BindStateHandler binds a callback to be called when the transaction state changes.
	// The callback can be unbound by calling the returned cancel function.
	BindStateHandler(fn TransactionStateHandler) (unbind func())
	// BindErrorHandler binds a callback to be called when the transaction encounters an transport or timeout error.
	// The callback can be unbound by calling the returned cancel function.
	BindErrorHandler(fn ErrorHandler) (unbind func())
}

type TransactionStateHandler interface {
	HandleTransactionState(ctx context.Context, from, to TransactionState)
}

type TransactionStateHandlerFunc func(ctx context.Context, from, to TransactionState)

func (f TransactionStateHandlerFunc) HandleTransactionState(ctx context.Context, from, to TransactionState) {
	f(ctx, from, to)
}

type TransactionStartHandler interface {
	HandleTransactionStart(ctx context.Context)
}

type TransactionStartHandlerFunc func(ctx context.Context)

func (f TransactionStartHandlerFunc) HandleTransactionStart(ctx context.Context) {
	f(ctx)
}

type baseTransact struct {
	typ   TransactionType
	impl  transactImpl
	fsm   *stateless.StateMachine
	state atomic.Value // TransactionState
	log   *slog.Logger

	lcMu          sync.Mutex
	started       bool
	starting      bool
	restored      bool
	activated     chan struct{}
	activateOnce  sync.Once
	terminated    chan struct{}
	terminateOnce sync.Once

	onStart types.CallbackManager[TransactionStartHandler]

	onStateChanged types.CallbackManager[TransactionStateHandler]
	pendingStates  types.Queue[pendingState]

	onErr       types.CallbackManager[ErrorHandler]
	pendingErrs types.Queue[pendingError]
	lastErr     atomic.Value // error
}

type transactImpl interface {
	Transaction
	saveSnapshotLocked()
}

type pendingState struct {
	ctx        context.Context
	transition stateless.Transition
}

type pendingError struct {
	ctx context.Context
	err error
}

func newBaseTransact(typ TransactionType, impl transactImpl, logger *slog.Logger) *baseTransact {
	return &baseTransact{
		typ:        typ,
		impl:       impl,
		log:        logger,
		activated:  make(chan struct{}),
		terminated: make(chan struct{}),
	}
}

// Type returns the transaction type.
func (tx *baseTransact) Type() TransactionType { return tx.typ }

// State returns the current state of the transaction.
func (tx *baseTransact) State() TransactionState {
	return tx.state.Load().(TransactionState) //nolint:forcetypeassert
}

func (tx *baseTransact) Logger() *slog.Logger { return tx.log }

func (tx *baseTransact) Started() bool {
	tx.lcMu.Lock()
	defer tx.lcMu.Unlock()
	return tx.started
}

func (tx *baseTransact) BindStartHandler(fn TransactionStartHandler) (unbind func()) {
	return tx.onStart.Add(fn)
}

func (tx *baseTransact) admitStart(ctx context.Context) bool {
	tx.lcMu.Lock()
	ok := !tx.started && !tx.isTerminated() && tx.State() != TransactionStateTerminated
	if ok {
		tx.started = true
		tx.starting = true
	}
	callbacks := slices.Collect(tx.onStart.All())
	tx.lcMu.Unlock()

	if ok {
		for _, fn := range callbacks {
			fn.HandleTransactionStart(ctx)
		}
	}
	return ok
}

func (tx *baseTransact) finishStart() {
	tx.lcMu.Lock()
	tx.starting = false
	tx.saveSnapshotLocked()
	tx.lcMu.Unlock()
}

func (tx *baseTransact) saveSnapshot() {
	tx.lcMu.Lock()
	tx.saveSnapshotLocked()
	tx.lcMu.Unlock()
}

func (tx *baseTransact) saveSnapshotLocked() {
	tx.impl.saveSnapshotLocked()
}

func (tx *baseTransact) activate(context.Context) {
	tx.lcMu.Lock()
	tx.activateLocked()
	tx.lcMu.Unlock()
}

func (tx *baseTransact) activateLocked() {
	if !tx.started || tx.isTerminated() || tx.State() == TransactionStateTerminated {
		return
	}
	tx.activateOnce.Do(func() { close(tx.activated) })
}

func (tx *baseTransact) isActive() bool {
	select {
	case <-tx.activated:
		return !tx.isTerminated()
	default:
		return false
	}
}

func (tx *baseTransact) waitActive(ctx context.Context) error {
	select {
	case <-tx.activated:
		return nil
	case <-tx.terminated:
		return errors.Wrap(NewTransactionActionNotAllowedError())
	case <-ctx.Done():
		return errors.Wrap(ctx.Err())
	}
}

func (tx *baseTransact) armTmr(
	slot *atomic.Pointer[timeutil.Timer],
	tmr *timeutil.Timer,
	allowed []TransactionState,
	fire func(tmr *timeutil.Timer),
) bool {
	tx.lcMu.Lock()
	defer tx.lcMu.Unlock()
	return tx.armTmrLocked(slot, tmr, allowed, fire)
}

func (tx *baseTransact) armTmrLocked(
	slot *atomic.Pointer[timeutil.Timer],
	tmr *timeutil.Timer,
	allowed []TransactionState,
	fire func(tmr *timeutil.Timer),
) bool {
	if tmr == nil || tx.isTerminated() || !isTxStateIn(tx.State(), allowed...) {
		return false
	}

	slot.Store(tmr)
	if st := tmr.State(); st == timeutil.TimerStateRunning || st == timeutil.TimerStateExpired {
		tmr.SetCallback(tx.guardTmr(slot, tmr, allowed, func() { fire(tmr) }))
	}
	return true
}

func (tx *baseTransact) guardTmr(
	slot *atomic.Pointer[timeutil.Timer],
	tmr *timeutil.Timer,
	allowed []TransactionState,
	fire func(),
) func() {
	return func() {
		tx.lcMu.Lock()
		ok := slot.Load() == tmr && !tx.isTerminated() && isTxStateIn(tx.State(), allowed...)
		tx.lcMu.Unlock()
		if !ok {
			return
		}
		fire()
	}
}

func (tx *baseTransact) stopTmr(slot *atomic.Pointer[timeutil.Timer]) bool {
	tx.lcMu.Lock()
	tmr := slot.Swap(nil)
	tx.lcMu.Unlock()
	return tmr != nil && tmr.Stop()
}

func (tx *baseTransact) clearTmr(slot *atomic.Pointer[timeutil.Timer], tmr *timeutil.Timer) {
	tx.lcMu.Lock()
	slot.CompareAndSwap(tmr, nil)
	tx.lcMu.Unlock()
}

func (tx *baseTransact) resetTmr(
	slot *atomic.Pointer[timeutil.Timer],
	tmr *timeutil.Timer,
	d time.Duration,
	allowed ...TransactionState,
) bool {
	tx.lcMu.Lock()
	defer tx.lcMu.Unlock()
	if tmr == nil || slot.Load() != tmr || tx.isTerminated() || !isTxStateIn(tx.State(), allowed...) {
		return false
	}
	tmr.Reset(d)
	return true
}

func (tx *baseTransact) fireTmrTrigger(ctx context.Context, trigger stateless.Trigger) {
	if err := tx.fsm.FireCtx(ctx, trigger); err != nil {
		if errors.Is(err, ErrTransactionActionNotAllowed) {
			return
		}
		panic(errors.Wrap(newTxTriggerErr(trigger, tx.State(), err)))
	}
}

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
			fn.HandleTransactionState(
				v.ctx,
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
			tx.lcMu.Lock()
			tx.state.Store(state)
			if state == TransactionStateTerminated {
				tx.terminateLocked()
			}
			tx.lcMu.Unlock()
			return nil
		},
		stateless.FiringQueued,
	)

	tx.fsm.SetTriggerParameters(txEvtTranspErr, reflect.TypeFor[error]())
	tx.fsm.SetTriggerParameters(txEvtTerminate, reflect.TypeFor[error]())

	tx.fsm.OnTransitioned(func(ctx context.Context, transition stateless.Transition) {
		tx.log.LogAttrs(
			ctx, slog.LevelDebug, "transaction state changed",
			slog.Any("transaction", tx.impl),
			slog.Any("from", transition.Source),
			slog.Any("to", transition.Destination),
		)

		tx.passStateTransition(ctx, transition)
	})

	tx.fsm.OnUnhandledTrigger(func(_ context.Context, state stateless.State, trigger stateless.Trigger, _ []string) error {
		return errors.Wrap(newTxTriggerErr(trigger, state, NewTransactionActionNotAllowedError()))
	})

	if start == TransactionStateTerminated {
		tx.terminateOnce.Do(func() { close(tx.terminated) })
	}

	return nil
}

func (*baseTransact) actNoop(context.Context, ...any) error { return nil }

func (tx *baseTransact) actTranspErr(ctx context.Context, args ...any) error {
	err := args[0].(error) //nolint:forcetypeassert

	tx.log.LogAttrs(
		ctx, slog.LevelDebug, "transport error occurred",
		slog.Any("transaction", tx.impl),
		slog.Any("error", err),
	)

	tx.passErr(ctx, errors.Wrap(err))
	return nil
}

func (tx *baseTransact) actTimedOut(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction timed out", slog.Any("transaction", tx.impl))

	tx.passErr(ctx, errors.Wrap(NewTransactionTimedOutError()))
	return nil
}

func (tx *baseTransact) actTermErr(ctx context.Context, args ...any) error {
	err := args[0].(error) //nolint:forcetypeassert

	tx.log.LogAttrs(
		ctx, slog.LevelDebug, "transaction terminated forcefully",
		slog.Any("transaction", tx.impl),
		slog.Any("error", err),
	)

	tx.passErr(ctx, errors.Wrap(err))
	return nil
}

//nolint:unparam
func (tx *baseTransact) actTerminated(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction terminated", slog.Any("transaction", tx.impl))

	tx.terminate(ctx)
	return nil
}

func (tx *baseTransact) terminate(context.Context) {
	tx.lcMu.Lock()
	tx.terminateLocked()
	tx.lcMu.Unlock()
}

func (tx *baseTransact) terminateLocked() {
	tx.terminateOnce.Do(func() { close(tx.terminated) })
}

func (tx *baseTransact) isTerminated() bool {
	select {
	case <-tx.terminated:
		return true
	default:
		return false
	}
}

// Terminate forces the transaction to terminate immediately switching it
// to the [TransactionStateTerminated] state.
// If cause is nil, it will be set to generic "undefined termination cause" error.
func (tx *baseTransact) Terminate(ctx context.Context, cause error) error {
	defer tx.saveSnapshot()
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

func isTxStateIn(state TransactionState, allowed ...TransactionState) bool {
	return slices.Contains(allowed, state)
}

func validateTxTmrSnap(tmr *timeutil.TimerSnapshot, state TransactionState, allowed ...TransactionState) error {
	if tmr == nil {
		return nil
	}
	if err := tmr.Validate(); err != nil {
		return errors.Wrap(err)
	}
	if tmr.State != timeutil.TimerStateRunning {
		return nil
	}
	if !isTxStateIn(state, allowed...) {
		return errors.Wrap(NewInvalidTransactionSnapshotError("active timer in wrong state"))
	}
	return nil
}
