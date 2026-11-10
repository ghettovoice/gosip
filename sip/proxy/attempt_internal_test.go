package proxy

import (
	"iter"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/ghettovoice/gosip/sip"
)

func TestPrxAttemptRunner_Stop(t *testing.T) {
	t.Parallel()

	t.Run("concurrent stops", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			var cleanups atomic.Int32
			candidate := &sip.RequestAttempt{}
			next, stop := iter.Pull(func(yield func(*sip.RequestAttempt) bool) {
				defer cleanups.Add(1)
				yield(candidate)
			})
			rn := &attemptRunner{nextFn: next, stopFn: stop}
			defer rn.stop()

			if got, ok := rn.next(); got != candidate || !ok {
				t.Fatalf("rn.next() = (%p, %t), want (%p, true)", got, ok, candidate)
			}

			release := make(chan struct{})
			var stoppers sync.WaitGroup
			for range 16 {
				stoppers.Go(func() {
					<-release
					rn.stop()
				})
			}
			synctest.Wait()
			close(release)
			stoppers.Wait()

			if got := cleanups.Load(); got != 1 {
				t.Errorf("iterator cleanups = %d, want 1", got)
			}
			if got, ok := rn.next(); got != nil || ok {
				t.Errorf("rn.next() after stop = (%p, %t), want (nil, false)", got, ok)
			}
		})
	})

	t.Run("stop during pull", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			var cleanups atomic.Int32
			entered := make(chan struct{})
			release := make(chan struct{})
			// Release the producer even if an assertion ends the test early.
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			next, stop := iter.Pull(func(yield func(*sip.RequestAttempt) bool) {
				defer cleanups.Add(1)
				close(entered)
				<-release
				yield(&sip.RequestAttempt{})
			})
			rn := &attemptRunner{nextFn: next, stopFn: stop}
			type result struct {
				attempt *sip.RequestAttempt
				ok      bool
			}
			results := make(chan result, 1)
			go func() {
				attempt, ok := rn.next()
				results <- result{attempt: attempt, ok: ok}
			}()
			<-entered

			rn.stop()
			if got := cleanups.Load(); got != 0 {
				t.Errorf("iterator cleanups while pull blocked = %d, want 0", got)
			}
			if got, ok := rn.next(); got != nil || ok {
				t.Errorf("rn.next() after stop = (%p, %t), want (nil, false)", got, ok)
			}

			close(release)
			got := <-results
			if got.attempt != nil || got.ok {
				t.Errorf("active rn.next() after stop = (%p, %t), want (nil, false)", got.attempt, got.ok)
			}
			if got := cleanups.Load(); got != 1 {
				t.Errorf("iterator cleanups after pull returned = %d, want 1", got)
			}
			rn.stop()
			if got := cleanups.Load(); got != 1 {
				t.Errorf("iterator cleanups after repeated stop = %d, want 1", got)
			}
		})
	})

	t.Run("stop before pull", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			var entered atomic.Bool
			next, stop := iter.Pull(func(yield func(*sip.RequestAttempt) bool) {
				entered.Store(true)
				yield(&sip.RequestAttempt{})
			})
			rn := &attemptRunner{nextFn: next, stopFn: stop}

			rn.stop()
			if got, ok := rn.next(); got != nil || ok {
				t.Errorf("rn.next() after stop = (%p, %t), want (nil, false)", got, ok)
			}
			if entered.Load() {
				t.Error("iterator body entered after stop, want never entered")
			}
		})
	})
}
