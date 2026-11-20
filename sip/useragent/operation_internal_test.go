package useragent

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// opTestContactURI is the contact injected by outcome-mutation checks.
var opTestContactURI = &sip.URI{
	User: sip.MakeUserInfo("alice"),
	Addr: sip.MakeHostAddr("alice.test"),
}

// newOpTestRes builds a minimal 200 OK response envelope for operation
// outcome tests. The Call-ID header keeps the cloned header map non-nil, so
// envelope clones can be mutated through WithMessage.
func newOpTestRes() *sip.ResponseEnvelope {
	return sip.NewResponseEnvelope(&sip.Response{
		Proto:   sip.ProtoVer20(),
		Status:  sip.ResponseStatusOK,
		Headers: make(sip.Headers).Set(header.CallID("op-test@alice.test")),
	})
}

// waitOpChan fails the test when ch does not close within a bounded wait.
func waitOpChan(tb testing.TB, ch <-chan struct{}, name string) {
	tb.Helper()

	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		tb.Fatalf("%s not closed within 5s", name)
	}
}

func TestOperation_Wait(t *testing.T) {
	t.Parallel()

	res200 := newOpTestRes()
	op := newOperation(t.Context(), OperationOptions{}, func(context.Context) (*sip.ResponseEnvelope, error) {
		return res200, nil
	})

	// Two sequential Wait calls resolve the same outcome through independent
	// clones.
	got1, err := op.Wait(t.Context())
	if err != nil {
		t.Fatalf("op.Wait() error = %v, want nil", err)
	}
	got1 = got1.WithMessage(func(res *sip.Response) {
		res.Headers.Set(header.Contact{{URI: opTestContactURI}})
	})
	got2, err := op.Wait(t.Context())
	if err != nil {
		t.Fatalf("op.Wait() error = %v, want nil", err)
	}
	if n := len(slices.Collect(got2.Headers().Contacts())); n != 0 {
		t.Fatalf("mutated Wait result leaked into stored outcome: %d contacts", n)
	}
	if got := got2.Status(); got != sip.ResponseStatusOK {
		t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
	}
	if got1 == got2 || got1 == res200 || got2 == res200 {
		t.Fatal("op.Wait() returned a shared envelope instance")
	}

	// Concurrent Wait calls each resolve an independent clone.
	type waitOut struct {
		res *sip.ResponseEnvelope
		err error
	}
	outs := make(chan waitOut, 2)
	go func() {
		res, err := op.Wait(t.Context())
		outs <- waitOut{res, err}
	}()
	go func() {
		res, err := op.Wait(t.Context())
		outs <- waitOut{res, err}
	}()
	out1, out2 := <-outs, <-outs
	if out1.err != nil || out2.err != nil {
		t.Fatalf("concurrent op.Wait() errors = %v, %v, want nil", out1.err, out2.err)
	}
	if out1.res == out2.res {
		t.Fatal("concurrent op.Wait() returned a shared envelope instance")
	}
	out1.res = out1.res.WithMessage(func(res *sip.Response) {
		res.Headers.Set(header.Contact{{URI: opTestContactURI}})
	})
	if n := len(slices.Collect(out2.res.Headers().Contacts())); n != 0 {
		t.Fatalf("mutated concurrent Wait result leaked into another clone: %d contacts", n)
	}
	got3, err := op.Wait(t.Context())
	if err != nil {
		t.Fatalf("op.Wait() error = %v, want nil", err)
	}
	if n := len(slices.Collect(got3.Headers().Contacts())); n != 0 {
		t.Fatalf("mutated concurrent Wait result leaked into stored outcome: %d contacts", n)
	}
}

func TestOperation_Cancel(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	op := newOperation(t.Context(), OperationOptions{}, func(ctx context.Context) (*sip.ResponseEnvelope, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	waitOpChan(t, started, "runner start")

	if err := op.Cancel(t.Context()); err != nil {
		t.Fatalf("op.Cancel() error = %v, want nil", err)
	}
	res, err := op.Wait(t.Context())
	if !errors.Is(err, ErrCanceled) || !errors.Is(err, context.Canceled) || !errclass.IsCanceled(err) {
		t.Fatalf("op.Wait() error = %v, want %v canceled outcome", err, ErrCanceled)
	}
	if res != nil {
		t.Fatalf("op.Wait() response = %v, want nil", res)
	}

	// Cancel is idempotent: a repeated call shares the first call's effect.
	if err := op.Cancel(t.Context()); err != nil {
		t.Fatalf("second op.Cancel() error = %v, want nil", err)
	}
}

func TestOperation_CancelAfterComplete(t *testing.T) {
	t.Parallel()

	res200 := newOpTestRes()
	op := newOperation(t.Context(), OperationOptions{}, func(context.Context) (*sip.ResponseEnvelope, error) {
		return res200, nil
	})

	got1, err := op.Wait(t.Context())
	if err != nil {
		t.Fatalf("op.Wait() error = %v, want nil", err)
	}
	if got := got1.Status(); got != sip.ResponseStatusOK {
		t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
	}

	if err := op.Cancel(t.Context()); err != nil {
		t.Fatalf("op.Cancel() error = %v, want nil", err)
	}

	// A late cancel never rewrites a published outcome.
	got2, err := op.Wait(t.Context())
	if err != nil {
		t.Fatalf("op.Wait() after cancel error = %v, want nil", err)
	}
	if got := got2.Status(); got != sip.ResponseStatusOK {
		t.Fatalf("op.Wait() after cancel status = %v, want %v", got, sip.ResponseStatusOK)
	}
}

func TestOperation_CancelWinsBeforePublish(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	barrier := make(chan struct{})
	res200 := newOpTestRes()
	op := newOperation(t.Context(), OperationOptions{}, func(context.Context) (*sip.ResponseEnvelope, error) {
		close(started)
		<-barrier
		return res200, nil
	})
	waitOpChan(t, started, "runner start")

	// Cancellation observed before the terminal commit beats a late runner
	// success.
	if err := op.Cancel(t.Context()); err != nil {
		t.Fatalf("op.Cancel() error = %v, want nil", err)
	}
	close(barrier)

	res, err := op.Wait(t.Context())
	if !errors.Is(err, ErrCanceled) || !errors.Is(err, context.Canceled) || !errclass.IsCanceled(err) {
		t.Fatalf("op.Wait() error = %v, want %v canceled outcome", err, ErrCanceled)
	}
	if res != nil {
		t.Fatalf("op.Wait() response = %v, want nil (late runner response suppressed)", res)
	}
}

func TestOperation_WaitContextCancel(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	barrier := make(chan struct{})
	res200 := newOpTestRes()
	op := newOperation(t.Context(), OperationOptions{}, func(context.Context) (*sip.ResponseEnvelope, error) {
		close(started)
		<-barrier
		return res200, nil
	})
	waitOpChan(t, started, "runner start")

	// A canceled waiter resolves promptly without touching the operation.
	canceledCtx, stop := context.WithCancel(t.Context())
	stop()
	res, err := op.Wait(canceledCtx)
	if !errors.Is(err, context.Canceled) || res != nil {
		t.Fatalf("op.Wait(canceled ctx) = %v, %v, want nil response + context.Canceled", res, err)
	}

	// The canceled waiter did not kill the runner: the real outcome still
	// publishes for later waiters.
	close(barrier)
	got, err := op.Wait(t.Context())
	if err != nil {
		t.Fatalf("op.Wait() error = %v, want nil", err)
	}
	if got := got.Status(); got != sip.ResponseStatusOK {
		t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
	}
}

func TestOperation_CallbackOnce(t *testing.T) {
	t.Parallel()

	res200 := newOpTestRes()
	cbResults := make(chan Result, 1)
	var cbCalls atomic.Int32
	op := newOperation(
		t.Context(),
		OperationOptions{
			OnComplete: func(_ context.Context, res Result) {
				cbCalls.Add(1)
				// Publication precedes the callback: mutating the delivered
				// clone must not change the stored outcome.
				if res.Response != nil {
					res.Response = res.Response.WithMessage(func(msg *sip.Response) {
						msg.Headers.Set(header.Contact{{URI: opTestContactURI}})
					})
				}
				cbResults <- res
			},
		},
		func(context.Context) (*sip.ResponseEnvelope, error) {
			return res200, nil
		},
	)

	var cbResult Result
	select {
	case cbResult = <-cbResults:
	case <-time.After(5 * time.Second):
		t.Fatal("OnComplete not invoked within 5s")
	}
	if cbResult.Err != nil {
		t.Fatalf("OnComplete result error = %v, want nil", cbResult.Err)
	}
	if cbResult.Response == nil || cbResult.Response.Status() != sip.ResponseStatusOK {
		t.Fatalf("OnComplete result response = %v, want 200 OK", cbResult.Response)
	}
	if n := len(slices.Collect(cbResult.Response.Headers().Contacts())); n != 1 {
		t.Fatalf("callback mutation missing on its own clone: %d contacts, want 1", n)
	}

	// done is closed before the callback runs, so the result is already
	// observable when OnComplete fires.
	select {
	case <-op.Done():
	default:
		t.Fatal("op.Done() not closed before OnComplete invocation")
	}

	// The stored outcome is untouched by the callback mutation.
	got, err := op.Wait(t.Context())
	if err != nil {
		t.Fatalf("op.Wait() error = %v, want nil", err)
	}
	if n := len(slices.Collect(got.Headers().Contacts())); n != 0 {
		t.Fatalf("callback mutation leaked into stored outcome: %d contacts", n)
	}

	// The callback fires exactly once for the operation lifetime.
	waitOpChan(t, op.exited, "worker exit")
	if n := cbCalls.Load(); n != 1 {
		t.Fatalf("OnComplete invoked %d times, want 1", n)
	}
}

func TestOperation_PreCanceled(t *testing.T) {
	t.Parallel()

	var runnerCalls atomic.Int32
	res200 := newOpTestRes()

	// Pre-canceled construction: the runner must never run.
	canceledCtx, stop := context.WithCancel(t.Context())
	stop()
	op := newOperation(canceledCtx, OperationOptions{}, func(context.Context) (*sip.ResponseEnvelope, error) {
		runnerCalls.Add(1)
		return res200.Clone().(*sip.ResponseEnvelope), nil
	})
	if n := runnerCalls.Load(); n != 0 {
		t.Fatalf("runner invoked %d times on pre-canceled ctx", n)
	}
	res, err := op.Wait(t.Context())
	if !errors.Is(err, ErrCanceled) || !errors.Is(err, context.Canceled) || res != nil {
		t.Fatalf("op.Wait() = %v, %v, want nil response + canceled outcome", res, err)
	}

	// The worker owns its lifecycle end even when Wait is never called:
	// done and exited both close and no goroutine is stranded.
	op2 := newOperation(canceledCtx, OperationOptions{}, func(context.Context) (*sip.ResponseEnvelope, error) {
		runnerCalls.Add(1)
		return res200, nil
	})
	waitOpChan(t, op2.Done(), "op2 done")
	waitOpChan(t, op2.exited, "op2 exited")
	if n := runnerCalls.Load(); n != 0 {
		t.Fatalf("runner invoked %d times on pre-canceled ctx", n)
	}
}
