package timeutil

import (
	"sync"
	"time"
)

// InactivityTimer executes a callback after the configured period without a reset.
// Reset and Stop are safe to call concurrently.
type InactivityTimer struct {
	mu       sync.Mutex
	timeout  time.Duration
	deadline time.Time
	timer    *time.Timer
	callback func()
	stopped  bool
}

// NewInactivityTimer creates an inactivity timer. The timer starts when Reset is called.
func NewInactivityTimer(timeout time.Duration, callback func()) *InactivityTimer {
	return &InactivityTimer{
		timeout:  timeout,
		callback: callback,
	}
}

// Reset restarts the timer from the current time.
func (t *InactivityTimer) Reset() {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.stopped || t.timeout <= 0 {
		return
	}

	t.deadline = time.Now().Add(t.timeout)
	if t.timer == nil {
		t.timer = time.AfterFunc(t.timeout, t.expire)
		return
	}

	t.timer.Reset(t.timeout)
}

// Stop permanently stops the timer. Subsequent Reset calls are ignored.
func (t *InactivityTimer) Stop() {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.stopped {
		return
	}

	t.stopped = true
	t.deadline = time.Time{}
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}

func (t *InactivityTimer) expire() {
	t.mu.Lock()
	if t.stopped || t.timer == nil {
		t.mu.Unlock()
		return
	}

	if remaining := time.Until(t.deadline); remaining > 0 {
		t.timer.Reset(remaining)
		t.mu.Unlock()
		return
	}

	t.stopped = true
	t.timer = nil
	callback := t.callback
	t.mu.Unlock()

	if callback != nil {
		callback()
	}
}
