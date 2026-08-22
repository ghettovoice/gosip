package syncutil_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ghettovoice/gosip/internal/syncutil"
)

func TestFirst_ResolveBeforeCancel(t *testing.T) {
	t.Parallel()

	var cancelled atomic.Int32

	first := syncutil.NewFirstOf[int]()
	first.Resolve(42).
		AddCancel(func() { cancelled.Add(1) }).
		AddCancel(func() { cancelled.Add(1) })

	if got := <-first.Chan(); got != 42 {
		t.Fatalf("Chan() = %d, want 42", got)
	}

	if got := cancelled.Load(); got != 2 {
		t.Fatalf("cancelled = %d, want 2", got)
	}
}

func TestFirst_CancelBeforeResolve(t *testing.T) {
	t.Parallel()

	var cancelled atomic.Int32

	first := syncutil.NewFirstOf[int]().
		AddCancel(func() { cancelled.Add(1) }).
		AddCancel(func() { cancelled.Add(1) }).
		Resolve(7)

	if got := <-first.Chan(); got != 7 {
		t.Fatalf("Chan() = %d, want 7", got)
	}

	if got := cancelled.Load(); got != 2 {
		t.Fatalf("cancelled = %d, want 2", got)
	}
}

func TestFirst_ConcurrentResolves(t *testing.T) {
	t.Parallel()

	for range 100 {
		var (
			cancelled atomic.Int32
			wg        sync.WaitGroup
		)

		first := syncutil.NewFirstOf[int]()

		wg.Add(2)
		go func() { defer wg.Done(); first.Resolve(1) }()
		go func() { defer wg.Done(); first.Resolve(2) }()

		first.AddCancel(func() { cancelled.Add(1) }).
			AddCancel(func() { cancelled.Add(1) })

		<-first.Chan()
		wg.Wait()

		if got := cancelled.Load(); got != 2 {
			t.Fatalf("cancelled = %d, want 2", got)
		}
	}
}

func TestFirst_ZeroValue(t *testing.T) {
	t.Parallel()

	first := syncutil.NewFirstOf[int]()
	first.Resolve(0).AddCancel(func() {})

	if got := <-first.Chan(); got != 0 {
		t.Fatalf("Chan() = %d, want 0", got)
	}
}
