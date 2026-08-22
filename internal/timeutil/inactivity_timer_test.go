package timeutil_test

import (
	"sync"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/timeutil"
)

func TestInactivityTimer_ExpiresAfterReset(t *testing.T) {
	t.Parallel()

	expired := make(chan struct{})
	timer := timeutil.NewInactivityTimer(10*time.Millisecond, func() { close(expired) })
	defer timer.Stop()

	timer.Reset()

	select {
	case <-expired:
	case <-time.After(time.Second):
		t.Fatal("timer did not expire")
	}
}

func TestInactivityTimer_ResetDelaysExpiration(t *testing.T) {
	t.Parallel()

	expired := make(chan struct{})
	timer := timeutil.NewInactivityTimer(30*time.Millisecond, func() { close(expired) })
	defer timer.Stop()

	timer.Reset()
	time.Sleep(15 * time.Millisecond)
	timer.Reset()

	select {
	case <-expired:
		t.Fatal("timer expired before reset duration elapsed")
	case <-time.After(10 * time.Millisecond):
	}

	select {
	case <-expired:
	case <-time.After(time.Second):
		t.Fatal("timer did not expire after reset")
	}
}

func TestInactivityTimer_StopPreventsExpiration(t *testing.T) {
	t.Parallel()

	expired := make(chan struct{})
	timer := timeutil.NewInactivityTimer(10*time.Millisecond, func() { close(expired) })
	timer.Reset()
	timer.Stop()
	timer.Reset()

	select {
	case <-expired:
		t.Fatal("stopped timer expired")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestInactivityTimer_ConcurrentReset(t *testing.T) {
	t.Parallel()

	var (
		mu      sync.Mutex
		expires int
		expired = make(chan struct{})
		timer   = timeutil.NewInactivityTimer(20*time.Millisecond, func() {
			mu.Lock()
			expires++
			mu.Unlock()
			close(expired)
		})
	)
	defer timer.Stop()

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for range 20 {
				timer.Reset()
			}
		})
	}
	wg.Wait()

	select {
	case <-expired:
	case <-time.After(time.Second):
		t.Fatal("timer did not expire after concurrent resets")
	}

	mu.Lock()
	defer mu.Unlock()
	if expires != 1 {
		t.Fatalf("expiration callbacks = %d, want 1", expires)
	}
}
