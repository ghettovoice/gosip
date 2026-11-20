package useragent

import (
	"context"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/sip"
)

// newLifecycleTestUserAgent builds a minimal user agent for lifecycle
// tests: a valid address of record and no handlers.
func newLifecycleTestUserAgent(tb testing.TB) *UserAgent {
	tb.Helper()

	aor, err := sip.ParseURI("sip:alice@example.com")
	if err != nil {
		tb.Fatalf("sip.ParseURI() error = %v, want nil", err)
	}
	ua, err := New(Options{AOR: aor})
	if err != nil {
		tb.Fatalf("New() error = %v, want nil", err)
	}
	return ua
}

// run200 is the shared runner that resolves immediately with a fresh
// 200 OK response.
func run200(context.Context) (*sip.ResponseEnvelope, error) {
	return newOpTestRes(), nil
}

// runUntilCanceled is the shared runner that blocks until the operation
// context is done, so a seal's cancel is the only way it returns.
func runUntilCanceled(ctx context.Context) (*sip.ResponseEnvelope, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// stubCapsProvider is a component stub advertising a fixed method set; it
// proves a borrowed element still accepts registrations. A nil-check on
// the returned unbind alone is not enough: a closed element hands back a
// non-nil no-op unbind.
type stubCapsProvider struct {
	methods []sip.RequestMethod
}

func (s stubCapsProvider) Capabilities() sip.ElementCapabilities {
	return sip.ElementCapabilities{Methods: s.methods}
}

// opsLen reports the number of registered operations under the lifecycle
// lock.
func (ua *UserAgent) opsLen() int {
	ua.mu.Lock()
	defer ua.mu.Unlock()
	return len(ua.ops)
}

func TestUserAgent_Shutdown(t *testing.T) {
	t.Parallel()

	ua := newLifecycleTestUserAgent(t)

	started := make(chan struct{})
	op, err := ua.startOp(t.Context(), OperationOptions{}, func(ctx context.Context) (*sip.ResponseEnvelope, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("ua.startOp() error = %v, want nil", err)
	}
	waitOpChan(t, started, "runner start")

	if err := ua.Shutdown(t.Context()); err != nil {
		t.Fatalf("ua.Shutdown() error = %v, want nil", err)
	}

	// The drain canceled the runner and returned only after the worker
	// exited, so the canceled outcome is already published.
	res, err := op.Wait(t.Context())
	if !errors.Is(err, ErrCanceled) || !errors.Is(err, context.Canceled) || !errclass.IsCanceled(err) || res != nil {
		t.Fatalf("op.Wait() = %v, %v, want nil response + canceled outcome", res, err)
	}

	// Admission stays sealed after the drain.
	if _, err := ua.startOp(t.Context(), OperationOptions{}, run200); !errors.Is(err, ErrClosed) || !errclass.IsClosed(err) {
		t.Fatalf("ua.startOp() after shutdown error = %v, want ErrClosed", err)
	}

	// An instance that never passed New rejects operations instead of
	// panicking.
	var zero UserAgent
	if _, err := zero.startOp(t.Context(), OperationOptions{}, run200); !errors.Is(err, ErrClosed) {
		t.Fatalf("zero.startOp() error = %v, want ErrClosed", err)
	}
}

func TestUserAgent_ShutdownWaitsForOnComplete(t *testing.T) {
	t.Parallel()

	t.Run("drains held callback", func(t *testing.T) {
		t.Parallel()

		ua := newLifecycleTestUserAgent(t)

		entered := make(chan struct{})
		release := make(chan struct{})
		op, err := ua.startOp(t.Context(), OperationOptions{
			OnComplete: func(context.Context, Result) { close(entered); <-release },
		}, run200)
		if err != nil {
			t.Fatalf("ua.startOp() error = %v, want nil", err)
		}
		waitOpChan(t, entered, "OnComplete enter")

		shutdownDone := make(chan error, 1)
		go func() { shutdownDone <- ua.Shutdown(t.Context()) }()
		select {
		case err := <-shutdownDone:
			t.Fatalf("ua.Shutdown returned %v while OnComplete still held", err)
		default:
		}
		close(release)
		select {
		case err := <-shutdownDone:
			if err != nil {
				t.Fatalf("ua.Shutdown() error = %v, want nil", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("ua.Shutdown() did not return within 5s of OnComplete release")
		}
		if _, err := ua.startOp(t.Context(), OperationOptions{}, run200); !errors.Is(err, ErrClosed) {
			t.Fatalf("ua.startOp() after shutdown error = %v, want ErrClosed", err)
		}
		_ = op
	})

	t.Run("deadline releases the waiter", func(t *testing.T) {
		t.Parallel()

		ua := newLifecycleTestUserAgent(t)

		entered := make(chan struct{})
		release := make(chan struct{})
		op, err := ua.startOp(t.Context(), OperationOptions{
			OnComplete: func(context.Context, Result) { close(entered); <-release },
		}, run200)
		if err != nil {
			t.Fatalf("ua.startOp() error = %v, want nil", err)
		}
		waitOpChan(t, entered, "OnComplete enter")

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		if err := ua.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ua.Shutdown() error = %v, want context.DeadlineExceeded", err)
		}

		// The deadline released only this waiter: the worker stays held
		// until the callback is released, then exits and drains.
		close(release)
		waitOpChan(t, op.exited, "worker exit")
		if err := ua.Shutdown(t.Context()); err != nil {
			t.Fatalf("ua.Shutdown() after release error = %v, want nil", err)
		}
	})
}

func TestUserAgent_StartShutdownRace(t *testing.T) {
	t.Parallel()

	ua := newLifecycleTestUserAgent(t)

	const callers = 8
	gate := make(chan struct{})
	type startResult struct {
		op  *operation
		err error
	}
	results := make(chan startResult, callers)
	for range callers {
		go func() {
			<-gate
			op, err := ua.startOp(t.Context(), OperationOptions{}, runUntilCanceled)
			results <- startResult{op, err}
		}()
	}
	shutdownDone := make(chan error, 1)
	go func() {
		<-gate
		shutdownDone <- ua.Shutdown(t.Context())
	}()
	close(gate)

	admitted := make([]*operation, 0, callers)
	for range callers {
		out := <-results
		if out.err != nil {
			if !errors.Is(out.err, ErrClosed) || !errclass.IsClosed(out.err) {
				t.Fatalf("rejected ua.startOp() error = %v, want ErrClosed", out.err)
			}
			continue
		}
		admitted = append(admitted, out.op)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("ua.Shutdown() error = %v, want nil", err)
	}

	// Every admitted op was canceled by the drain and resolves its canceled
	// outcome; no admitted op is stranded.
	for i, op := range admitted {
		res, err := op.Wait(t.Context())
		if !errors.Is(err, ErrCanceled) || !errors.Is(err, context.Canceled) || res != nil {
			t.Fatalf("admitted op %d Wait() = %v, %v, want nil response + canceled outcome", i, res, err)
		}
	}
	if n := ua.opsLen(); n != 0 {
		t.Fatalf("ua.ops len after drain = %d, want 0", n)
	}
}

func TestUserAgent_Close(t *testing.T) {
	t.Parallel()

	ua := newLifecycleTestUserAgent(t)

	elm := new(sip.Element)
	t.Cleanup(func() { elm.Close() })
	if unbind := elm.UseComponent(ua); unbind == nil {
		t.Fatal("elm.UseComponent() = nil unbind, want non-nil")
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	op, err := ua.startOp(t.Context(), OperationOptions{
		OnComplete: func(context.Context, Result) { close(entered); <-release },
	}, runUntilCanceled)
	if err != nil {
		t.Fatalf("ua.startOp() error = %v, want nil", err)
	}

	// Close is the non-graceful seal: it cancels the in-flight op and
	// returns promptly — remote transaction lifetime and the still-held
	// worker callback never delay it.
	closeDone := make(chan error, 1)
	go func() { closeDone <- ua.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("ua.Close() error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ua.Close() did not return within 5s")
	}

	res, err := op.Wait(t.Context())
	if !errors.Is(err, ErrCanceled) || !errors.Is(err, context.Canceled) || res != nil {
		t.Fatalf("op.Wait() = %v, %v, want nil response + canceled outcome", res, err)
	}
	waitOpChan(t, entered, "OnComplete enter")

	// The borrowed element was not closed: it still accepts components and
	// aggregates their capabilities.
	if unbind := elm.UseComponent(stubCapsProvider{
		methods: []sip.RequestMethod{sip.RequestMethodInvite},
	}); unbind == nil {
		t.Fatal("elm.UseComponent() after ua.Close() = nil unbind, want non-nil")
	}
	if caps := elm.Capabilities(); !caps.SupportsMethod(sip.RequestMethodInvite) {
		t.Error("elm.Capabilities() does not support INVITE after ua.Close(), want supported")
	}

	// Release the held callback so the worker exits: the registry slot is
	// freed on worker exit with no Shutdown ever called.
	close(release)
	ua.mu.Lock()
	drained := ua.drained
	ua.mu.Unlock()
	waitOpChan(t, drained, "drained")
	if n := ua.opsLen(); n != 0 {
		t.Fatalf("ua.ops len after Close = %d, want 0", n)
	}
}

func TestUserAgent_ShutdownNoWaitRequired(t *testing.T) {
	t.Parallel()

	ua := newLifecycleTestUserAgent(t)

	op, err := ua.startOp(t.Context(), OperationOptions{}, run200)
	if err != nil {
		t.Fatalf("ua.startOp() error = %v, want nil", err)
	}

	// Wait is never called: the worker still publishes its outcome and the
	// releaser frees the registry slot on exit, so the drain completes.
	if err := ua.Shutdown(t.Context()); err != nil {
		t.Fatalf("ua.Shutdown() error = %v, want nil", err)
	}
	select {
	case <-op.Done():
	default:
		t.Fatal("op.Done() not closed after shutdown drain")
	}
	if n := ua.opsLen(); n != 0 {
		t.Fatalf("ua.ops len after drain = %d, want 0", n)
	}
}

func TestUserAgent_ShutdownTwice(t *testing.T) {
	t.Parallel()

	ua := newLifecycleTestUserAgent(t)

	if err := ua.Shutdown(t.Context()); err != nil {
		t.Fatalf("ua.Shutdown() error = %v, want nil", err)
	}
	ua.mu.Lock()
	first := ua.drained
	ua.mu.Unlock()
	if first == nil {
		t.Fatal("ua.drained = nil after first Shutdown, want the shared channel")
	}
	select {
	case <-first:
	default:
		t.Fatal("ua.drained not closed after the first Shutdown returned")
	}

	// The second call observes the same channel created at the first seal
	// instead of spawning another waiter goroutine.
	if err := ua.Shutdown(t.Context()); err != nil {
		t.Fatalf("second ua.Shutdown() error = %v, want nil", err)
	}
	ua.mu.Lock()
	second := ua.drained
	ua.mu.Unlock()
	if second != first {
		t.Fatal("second ua.Shutdown() replaced ua.drained, want the channel created once at seal")
	}
}

func TestUserAgent_ShutdownAfterClose(t *testing.T) {
	t.Parallel()

	ua := newLifecycleTestUserAgent(t)

	entered := make(chan struct{})
	release := make(chan struct{})
	op, err := ua.startOp(t.Context(), OperationOptions{
		OnComplete: func(context.Context, Result) { close(entered); <-release },
	}, run200)
	if err != nil {
		t.Fatalf("ua.startOp() error = %v, want nil", err)
	}
	waitOpChan(t, entered, "OnComplete enter")

	// Close seals and cancels without waiting, so the held callback
	// survives it.
	if err := ua.Close(); err != nil {
		t.Fatalf("ua.Close() error = %v, want nil", err)
	}

	// Shutdown after a prior Close still observes the shared drained
	// channel: it must stay pending while the admitted worker is held and
	// return once the worker exits.
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- ua.Shutdown(t.Context()) }()
	select {
	case err := <-shutdownDone:
		t.Fatalf("ua.Shutdown after Close returned %v while the worker still held", err)
	default:
	}
	close(release)
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("ua.Shutdown() after Close error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ua.Shutdown() after Close did not drain within 5s")
	}
	_ = op
}
