package useragent

import (
	"context"
	"sync"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
)

// Result is the terminal outcome of an operation.
//
// Response carries the final response envelope on a completed exchange and
// is nil for failed or canceled outcomes. Err carries the terminal error;
// a canceled operation reports an [ErrCanceled] error that also matches
// [context.Canceled] or [context.DeadlineExceeded] according to its cause.
type Result struct {
	Response *sip.ResponseEnvelope
	Err      error
}

// OperationOptions configures a single operation.
//
// Callbacks are bound at operation creation, so there is no
// subscribe-after-start race. OnProgress reports intermediate events:
// in this slice it delivers each received provisional (101–199)
// response as a deep clone — never a bare 100 Trying — and is
// suppressed once the operation context is done; later slices emit
// further progress events through the same callback. OnComplete fires
// exactly once for every terminal outcome — success,
// failure, or cancellation — after the outcome is published, so the
// callback may safely call [operation.Wait] or observe [operation.Done].
// Callbacks never block waiters, and calling Cancel inside a callback is
// allowed.
type OperationOptions struct {
	Timing     sip.TimingConfig
	OnProgress func(context.Context, *sip.ResponseEnvelope)
	OnComplete func(context.Context, Result)
}

// operation is the engine handle of one in-flight user agent operation.
//
// The operation owns a derived context and a single worker goroutine that
// runs the exchange to its terminal outcome. done closes when the outcome
// is published; exited closes at worker exit, after the completion
// callback, so a registry drain observes the operation only once callbacks
// finished. result is guarded by mu, written exactly once, and never
// rewritten: cancellation observed before the commit beats a late runner
// response, while a cancel arriving after publication changes nothing.
type operation struct {
	// cancel stops the operation-owned context; it is idempotent and never
	// writes a result. done closes at result publication and exited at
	// worker exit. opts is the immutable creation-time configuration.
	cancel context.CancelFunc
	done   chan struct{}
	exited chan struct{}
	opts   OperationOptions

	// mu guards result, the single published terminal outcome.
	mu     sync.Mutex
	result Result
}

// newOperation creates an operation, derives its owned context from ctx,
// and spawns the worker that runs run to the terminal outcome. A
// pre-canceled ctx publishes a canceled outcome without ever invoking run.
func newOperation(
	ctx context.Context,
	opts OperationOptions,
	run func(context.Context) (*sip.ResponseEnvelope, error),
) *operation {
	opCtx, cancel := context.WithCancel(ctx)
	op := &operation{
		cancel: cancel,
		done:   make(chan struct{}),
		exited: make(chan struct{}),
		opts:   opts,
	}
	go op.work(opCtx, run)
	return op
}

// work runs the operation state machine and owns the worker lifecycle:
// pre-canceled check, runner invocation, single outcome commit, done
// publication, completion callback, then exit bookkeeping.
func (op *operation) work(opCtx context.Context, run func(context.Context) (*sip.ResponseEnvelope, error)) {
	defer func() {
		// Context resources are released and the exit is signaled only
		// after the stored outcome and the completion callback finished;
		// the deferred cancel never rewrites the published outcome.
		op.cancel()
		close(op.exited)
	}()

	var res *sip.ResponseEnvelope
	var rerr error
	if opCtx.Err() == nil {
		res, rerr = run(opCtx)
	}

	op.mu.Lock()
	if err := opCtx.Err(); err != nil {
		// Cancellation observed before the terminal commit suppresses
		// whatever the runner returned.
		op.result = Result{Err: NewCanceledError(err)}
	} else {
		op.result = Result{Response: cloneOpRes(res), Err: rerr}
	}
	published := op.result
	op.mu.Unlock()

	close(op.done)

	if op.opts.OnComplete != nil {
		op.opts.OnComplete(
			opCtx,
			Result{Response: cloneOpRes(published.Response), Err: published.Err},
		)
	}
}

// Wait blocks until the operation publishes its terminal outcome or ctx is
// done, whichever comes first. Repeated and concurrent calls resolve the
// same outcome through independent response clones. A done ctx only fails
// this waiter — it never touches the operation — and a publication
// simultaneous with the waiter error still wins.
func (op *operation) Wait(ctx context.Context) (*sip.ResponseEnvelope, error) {
	select {
	case <-op.done:
		return op.outcome()
	case <-ctx.Done():
		select {
		case <-op.done:
			return op.outcome()
		default:
		}
		return nil, errors.Wrap(ctx.Err())
	}
}

// outcome returns a deep copy of the published result: every waiter and
// callback receives an independent clone of the stored response.
func (op *operation) outcome() (*sip.ResponseEnvelope, error) {
	op.mu.Lock()
	defer op.mu.Unlock()

	return cloneOpRes(op.result.Response), errors.Wrap(op.result.Err)
}

// Cancel requests the operation to stop by canceling its owned context.
// It is idempotent, never blocks, and never writes a result directly: the
// worker commits the canceled outcome at its terminal step, and a cancel
// arriving after publication changes nothing. The ctx parameter is
// reserved for future cancel-command propagation.
func (op *operation) Cancel(_ context.Context) error {
	op.cancel()
	return nil
}

// Done returns a channel closed when the operation's terminal outcome is
// published. It closes for every terminal outcome, including cancellation.
func (op *operation) Done() <-chan struct{} {
	return op.done
}

// cloneOpRes returns a deep copy of res, preserving nil.
func cloneOpRes(res *sip.ResponseEnvelope) *sip.ResponseEnvelope {
	if res == nil {
		return nil
	}
	return res.Clone().(*sip.ResponseEnvelope) //nolint:forcetypeassert // Clone preserves the concrete type
}
