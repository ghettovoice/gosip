package timeutil

import (
	"encoding/json"
	"sync"
	"time"

	"braces.dev/errtrace"
)

// TimerState represents the current state of a serializable timer.
type TimerState string

const (
	// TimerStateRunning indicates the timer is currently running.
	TimerStateRunning TimerState = "running"
	// TimerStateStopped indicates the timer was stopped before expiration.
	TimerStateStopped TimerState = "stopped"
	// TimerStateExpired indicates the timer has expired.
	TimerStateExpired TimerState = "expired"
)

// SerializableTimer represents a timer that can be serialized to/from JSON.
// It tracks the start time, duration, and current state and can export/import
// a lightweight [TimerSnapshot] for storage. Runtime-only fields such as
// callbacks and the underlying [time.Timer] are intentionally excluded from
// the snapshot and must be reattached manually after restoration.
// SerializableTimer automatically manages a real time.Timer for callback
// execution while it is running.
type SerializableTimer struct {
	// startTime is the timestamp when the timer was started.
	startTime time.Time
	// duration is the total duration the timer should run.
	duration time.Duration
	// state is the current state of the timer.
	state TimerState
	// stopTime is the timestamp when the timer was stopped (if applicable).
	stopTime time.Time

	// callback is the function to execute when the timer expires.
	// This field is not serialized.
	callback func()
	// callbackExecuted tracks whether the callback has been executed.
	// This field is not serialized.
	callbackExecuted bool
	// mu protects concurrent access to all mutable fields.
	mu sync.Mutex
	// realTimer is the actual time.Timer that runs in the background.
	// This field is not serialized.
	realTimer *time.Timer
}

// NewTimer creates a new SerializableTimer with the given duration.
// The timer is started immediately.
func NewTimer(duration time.Duration) *SerializableTimer {
	now := time.Now()
	return &SerializableTimer{
		startTime: now,
		duration:  duration,
		state:     TimerStateRunning,
	}
}

// AfterFunc creates a new SerializableTimer with the given duration and callback.
// The timer is started immediately and the callback will be executed when it expires.
func AfterFunc(duration time.Duration, f func()) *SerializableTimer {
	timer := NewTimer(duration)
	timer.SetCallback(f)
	return timer
}

// FromTime creates a new SerializableTimer with the given start time and duration.
// This is useful for recreating timers from serialized data.
// Unlike FromJSON, this does not automatically call UpdateState(). You should call
// UpdateState() after creating the timer to check expiration and trigger callbacks.
func FromTime(startTime time.Time, duration time.Duration) *SerializableTimer {
	return &SerializableTimer{
		startTime: startTime,
		duration:  duration,
		state:     TimerStateRunning,
	}
}

// State returns the current timer state in a thread-safe manner.
func (t *SerializableTimer) State() TimerState {
	if t == nil {
		return ""
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// StartTime returns the timer's start time.
func (t *SerializableTimer) StartTime() time.Time {
	if t == nil {
		return time.Time{}
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	return t.startTime
}

// Duration returns the timer's duration.
func (t *SerializableTimer) Duration() time.Duration {
	if t == nil {
		return 0
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	return t.duration
}

// StopTime returns the timer's stop time (nil if not stopped).
func (t *SerializableTimer) StopTime() time.Time {
	if t == nil {
		return time.Time{}
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopTime
}

// Elapsed returns the time elapsed since the timer started.
func (t *SerializableTimer) Elapsed() time.Duration {
	if t == nil {
		return 0
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	return t.elapsedUnsafe()
}

// elapsedUnsafe computes the elapsed duration without locking.
// Caller must hold the mutex.
func (t *SerializableTimer) elapsedUnsafe() time.Duration {
	switch t.state {
	case TimerStateRunning:
		return time.Since(t.startTime)
	case TimerStateStopped, TimerStateExpired:
		if !t.stopTime.IsZero() {
			return t.stopTime.Sub(t.startTime)
		}
		return t.duration
	}
	return t.duration
}

// Left returns the time remaining until the timer expires.
// Returns 0 if the timer is expired or stopped.
func (t *SerializableTimer) Left() time.Duration {
	if t == nil {
		return 0
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.state == TimerStateStopped {
		return 0
	}
	elapsed := t.elapsedUnsafe()
	left := t.duration - elapsed
	if left < 0 {
		return 0
	}
	return left
}

// Expired returns true if the timer has expired.
func (t *SerializableTimer) Expired() bool {
	if t == nil {
		return false
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	return t.expiredUnsafe()
}

// expiredUnsafe returns true if the timer has expired without acquiring the mutex.
// Caller must hold the mutex.
func (t *SerializableTimer) expiredUnsafe() bool {
	if t.state == TimerStateExpired {
		return true
	}
	if t.state == TimerStateStopped {
		return false
	}
	// For running timers, check if elapsed time exceeds duration
	return time.Since(t.startTime) >= t.duration
}

// Stop stops the timer and updates its state.
// If the timer is stopped, the callback will not be executed.
func (t *SerializableTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.state != TimerStateRunning {
		return false
	}

	t.stopTime = time.Now()
	t.state = TimerStateStopped
	// Clear callback since timer was stopped
	t.callback = nil

	// Stop the real timer if it exists
	if t.realTimer != nil {
		t.realTimer.Stop()
		t.realTimer = nil
	}
	return true
}

// SetCallback sets a function to be executed when the timer expires.
// Similar to time.AfterFunc, the function is called in its own goroutine.
// If the timer has already expired, the function will be executed immediately.
// If the timer is stopped, the function will not be executed.
// This method automatically starts a real time.Timer to handle callback execution.
func (t *SerializableTimer) SetCallback(f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.callback = f

	// Check if timer has already expired (unsafe version to avoid deadlock)
	if t.expiredUnsafe() && !t.callbackExecuted {
		t.callbackExecuted = true
		go f()
		return
	}

	// If timer is still running, start/replace the real timer
	if t.state == TimerStateRunning {
		// Stop any existing real timer
		if t.realTimer != nil {
			t.realTimer.Stop()
		}

		// Calculate remaining time
		remaining := t.duration - time.Since(t.startTime)
		if remaining <= 0 {
			remaining = 1 // Minimal time to trigger immediate execution
		}

		// Start new real timer
		t.realTimer = time.AfterFunc(remaining, func() {
			t.mu.Lock()
			defer t.mu.Unlock()

			// Mark as expired and execute callback
			if t.state == TimerStateRunning && !t.callbackExecuted {
				t.state = TimerStateExpired
				t.stopTime = time.Now()
				t.callbackExecuted = true
				// Copy callback to avoid race with marshaling
				callback := t.callback
				if callback != nil {
					go callback()
				}
			}
		})
	}
}

// UpdateState updates the timer's state based on the current time.
// This should be called after unmarshaling to check if the timer has expired.
func (t *SerializableTimer) UpdateState() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.updateStateUnsafe()
}

// updateStateUnsafe updates the timer's state without acquiring the mutex.
// Caller must hold the mutex.
func (t *SerializableTimer) updateStateUnsafe() {
	wasRunning := t.state == TimerStateRunning
	// Use direct calculation instead of Elapsed() to avoid deadlock
	isExpired := time.Since(t.startTime) >= t.duration

	if wasRunning && isExpired {
		t.state = TimerStateExpired

		// Execute callback if set and not already executed
		if t.callback != nil && !t.callbackExecuted {
			t.callbackExecuted = true
			go t.callback()
		}
	} else if t.state == TimerStateExpired && isExpired && t.callback != nil && !t.callbackExecuted {
		// Handle case where timer is already expired but callback wasn't executed
		// This can happen in race conditions or when UpdateState is called multiple times
		t.callbackExecuted = true
		go t.callback()
	}
}

// Reset resets the timer with a new duration, starting from now.
// The callback is preserved - if one was set, it will execute when the new duration expires.
// To clear the callback, call Stop() first.
func (t *SerializableTimer) Reset(duration time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	t.startTime = now
	t.duration = duration
	t.state = TimerStateRunning
	t.stopTime = time.Time{}
	t.callbackExecuted = false

	// Stop existing real timer
	if t.realTimer != nil {
		t.realTimer.Stop()
		t.realTimer = nil
	}

	// If there's a callback, restart the real timer
	if t.callback != nil {
		t.realTimer = time.AfterFunc(duration, func() {
			t.mu.Lock()
			defer t.mu.Unlock()

			// Mark as expired and execute callback
			if t.state == TimerStateRunning && !t.callbackExecuted {
				t.state = TimerStateExpired
				t.stopTime = time.Now()
				t.callbackExecuted = true
				// Copy callback to avoid race with marshaling
				callback := t.callback
				if callback != nil {
					go callback()
				}
			}
		})
	}
}

var jsonNull = []byte("null")

// TimerSnapshot represents a serializable view of a timer.
// Only deterministic fields are included so that the snapshot can be safely
// persisted or transferred between goroutines or processes.
type TimerSnapshot struct {
	StartTime time.Time     `json:"start_time"`
	Duration  time.Duration `json:"duration"`
	State     TimerState    `json:"state"`
	StopTime  time.Time     `json:"stop_time,omitzero"`
}

// Snapshot returns an immutable representation of the timer state.
// The returned snapshot can be serialized directly or passed to [RestoreTimer]
// to recreate a timer instance with the same timing metadata.
func (t *SerializableTimer) Snapshot() *TimerSnapshot {
	if t == nil {
		return nil
	}

	t.mu.Lock()
	snap := t.snapshotUnsafe()
	t.mu.Unlock()

	return &snap
}

func (t *SerializableTimer) snapshotUnsafe() TimerSnapshot {
	// keep timer state up to date before exporting
	t.updateStateUnsafe()

	return TimerSnapshot{
		StartTime: t.startTime,
		Duration:  t.duration,
		State:     t.state,
		StopTime:  t.stopTime,
	}
}

func (t *SerializableTimer) restoreUnsafe(snap *TimerSnapshot) {
	defer t.updateStateUnsafe()

	if t.realTimer != nil {
		t.realTimer.Stop()
		t.realTimer = nil
	}

	if snap == nil {
		// Reset timer fields to zero values.
		t.startTime = time.Time{}
		t.duration = 0
		t.state = ""
		t.stopTime = time.Time{}
		t.callback = nil
		t.callbackExecuted = false
		return
	}

	t.startTime = snap.StartTime
	t.duration = snap.Duration
	t.state = snap.State
	t.stopTime = snap.StopTime
	// reset runtime-only fields
	t.callback = nil
	t.callbackExecuted = false
}

// MarshalJSON implements json.Marshaler.
func (t *SerializableTimer) MarshalJSON() ([]byte, error) {
	if t == nil {
		return jsonNull, nil
	}

	t.mu.Lock()
	snap := t.snapshotUnsafe()
	t.mu.Unlock()

	return errtrace.Wrap2(json.Marshal(&snap))
}

// UnmarshalJSON implements json.Unmarshaler.
func (t *SerializableTimer) UnmarshalJSON(data []byte) error {
	// Unmarshal into a snapshot
	var snap TimerSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return errtrace.Wrap(err)
	}

	t.mu.Lock()
	t.restoreUnsafe(&snap)
	t.mu.Unlock()

	return nil
}

// ToJSON serializes the timer to a JSON string.
func (t *SerializableTimer) ToJSON() ([]byte, error) {
	return errtrace.Wrap2(json.Marshal(t))
}

// FromJSON deserializes a timer from a JSON string.
func FromJSON(data []byte) (*SerializableTimer, error) {
	snap := new(TimerSnapshot)
	if err := json.Unmarshal(data, snap); err != nil {
		return nil, errtrace.Wrap(err)
	}

	timer := new(SerializableTimer)
	timer.restoreUnsafe(snap)
	return timer, nil
}

// SnapshotTimer safely snapshots the provided timer.
func SnapshotTimer(t *SerializableTimer) *TimerSnapshot {
	if t == nil {
		return nil
	}
	return t.Snapshot()
}

// RestoreTimer recreates a SerializableTimer from its snapshot.
// Callback-related fields are left nil; callers should reattach callbacks or
// restart timers using [SetCallback] / [Reset] as appropriate after restoration.
func RestoreTimer(snap *TimerSnapshot) *SerializableTimer {
	if snap == nil {
		return nil
	}

	timer := new(SerializableTimer)
	timer.restoreUnsafe(snap)
	return timer
}
