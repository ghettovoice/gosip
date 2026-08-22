package timeutil

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
)

// TimerState represents the current state of a serializable timer.
type TimerState uint

const (
	TimerStateInvalid TimerState = iota
	TimerStateRunning
	TimerStateStopped
	TimerStateExpired
)

var tmrStateTags = map[TimerState]string{
	TimerStateRunning: "running",
	TimerStateStopped: "stopped",
	TimerStateExpired: "expired",
}

func TimerStateFromString(s string) TimerState {
	for state, tag := range tmrStateTags {
		if strings.EqualFold(tag, s) {
			return state
		}
	}
	return TimerStateInvalid
}

func (s TimerState) IsValid() bool {
	switch s {
	case TimerStateRunning, TimerStateStopped, TimerStateExpired:
		return true
	default:
		return false
	}
}

func (s TimerState) String() string {
	if tag, ok := tmrStateTags[s]; ok {
		return tag
	}
	return "invalid"
}

func (s TimerState) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

func (s TimerState) AppendText(b []byte) ([]byte, error) {
	return append(b, s.String()...), nil
}

func (s *TimerState) UnmarshalText(data []byte) error {
	*s = TimerStateFromString(string(data))
	return nil
}

// Timer represents a timer that can be serialized to/from JSON.
// It tracks the start time, duration, and current state and can export/import
// a lightweight [TimerSnapshot] for storage. Runtime-only fields such as
// callbacks and the underlying [time.Timer] are intentionally excluded from
// the snapshot and must be reattached manually after restoration.
// Timer automatically manages a real time.Timer for callback
// execution while it is running.
type Timer struct {
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
func NewTimer(duration time.Duration) *Timer {
	return &Timer{
		startTime: time.Now(),
		duration:  duration,
		state:     TimerStateRunning,
	}
}

// AfterFunc creates a new SerializableTimer with the given duration and callback.
// The timer is started immediately and the callback will be executed when it expires.
func AfterFunc(duration time.Duration, f func()) *Timer {
	timer := NewTimer(duration)
	timer.SetCallback(f)
	return timer
}

// FromTime creates a new SerializableTimer with the given start time and duration.
// This is useful for recreating timers from serialized data.
// Unlike FromJSON, this does not automatically call UpdateState(). You should call
// UpdateState() after creating the timer to check expiration and trigger callbacks.
func FromTime(startTime time.Time, duration time.Duration) *Timer {
	return &Timer{
		startTime: startTime,
		duration:  duration,
		state:     TimerStateRunning,
	}
}

// State returns the current timer state in a thread-safe manner.
func (t *Timer) State() TimerState {
	if t == nil {
		return 0
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.state
}

// StartTime returns the timer's start time.
func (t *Timer) StartTime() time.Time {
	if t == nil {
		return time.Time{}
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.startTime
}

// Duration returns the timer's duration.
func (t *Timer) Duration() time.Duration {
	if t == nil {
		return 0
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.duration
}

// StopTime returns the timer's stop time (zero value if not stopped).
func (t *Timer) StopTime() time.Time {
	if t == nil {
		return time.Time{}
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.stopTime
}

// Elapsed returns the time elapsed since the timer started.
func (t *Timer) Elapsed() time.Duration {
	if t == nil {
		return 0
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.elapsedUnsafe()
}

// elapsedUnsafe computes the elapsed duration without locking.
// Caller must hold the mutex.
func (t *Timer) elapsedUnsafe() time.Duration {
	switch t.state {
	case TimerStateRunning:
		return time.Since(t.startTime)
	case TimerStateStopped, TimerStateExpired:
		if !t.stopTime.IsZero() {
			return t.stopTime.Sub(t.startTime)
		}
		return t.duration
	default:
		return t.duration
	}
}

// Left returns the time remaining until the timer expires.
// Returns 0 if the timer is expired or stopped.
func (t *Timer) Left() time.Duration {
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
func (t *Timer) Expired() bool {
	if t == nil {
		return false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.expiredUnsafe()
}

// expiredUnsafe returns true if the timer has expired without acquiring the mutex.
// Caller must hold the mutex.
func (t *Timer) expiredUnsafe() bool {
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
func (t *Timer) Stop() bool {
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
func (t *Timer) SetCallback(f func()) {
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
		t.startRealTimer(t.duration - time.Since(t.startTime))
	}
}

// UpdateState updates the timer's state based on the current time.
// This is useful for timers created with [FromTime] or when the caller wants
// to manually re-check expiration after a period of inactivity.
func (t *Timer) UpdateState() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.updateStateUnsafe()
}

// updateStateUnsafe updates the timer's state without acquiring the mutex.
// Caller must hold the mutex.
func (t *Timer) updateStateUnsafe() {
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

// startRealTimer stops any existing real timer and starts a new one with the given remaining duration.
// Caller must hold the mutex.
func (t *Timer) startRealTimer(remaining time.Duration) {
	if t.realTimer != nil {
		t.realTimer.Stop()
	}

	if remaining <= 0 {
		remaining = 1 // Minimal time to trigger immediate execution
	}

	t.realTimer = time.AfterFunc(remaining, func() {
		t.mu.Lock()
		defer t.mu.Unlock()

		if t.state == TimerStateRunning && !t.callbackExecuted {
			t.state = TimerStateExpired
			t.stopTime = time.Now()
			t.callbackExecuted = true

			if callback := t.callback; callback != nil {
				go callback()
			}
		}
	})
}

// Reset resets the timer with a new duration, starting from now.
// The callback is preserved - if one was set, it will execute when the new duration expires.
// To clear the callback, call Stop() first.
func (t *Timer) Reset(duration time.Duration) {
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
		t.startRealTimer(duration)
	}
}

// TimerSnapshot represents a serializable view of a timer.
// Only deterministic fields are included so that the snapshot can be safely
// persisted or transferred between goroutines or processes.
type TimerSnapshot struct {
	State     TimerState    `json:"state"`
	StartTime time.Time     `json:"start_time"`
	Duration  time.Duration `json:"duration"`
	StopTime  time.Time     `json:"stop_time,omitzero"`
}

// IsValid reports whether the snapshot contains a known timer state.
func (s *TimerSnapshot) IsValid() bool { return s.Validate() == nil }

const ErrInvalidTimerSnapshot errors.Error = "invalid timer snapshot"

func newInvalidTimerSnapshotError(args ...any) error {
	return errors.Prefix(ErrInvalidTimerSnapshot, args...)
}

func (s *TimerSnapshot) Validate() error {
	if s == nil {
		return errors.ErrorWrap("nil snapshot")
	}

	if !s.State.IsValid() {
		return errors.Wrap(newInvalidTimerSnapshotError("invalid timer state %q", s.State))
	}

	if s.StartTime.IsZero() {
		return errors.Wrap(newInvalidTimerSnapshotError("start time is zero"))
	}

	if !s.StopTime.IsZero() && s.StopTime.Before(s.StartTime) {
		return errors.Wrap(newInvalidTimerSnapshotError("stop time %q is before start time %q", s.StopTime, s.StartTime))
	}

	return nil
}

// Snapshot returns a serializable copy of the timer state.
// The returned snapshot can be serialized directly or passed to [RestoreTimer]
// to recreate a timer instance with the same timing metadata.
func (t *Timer) Snapshot() *TimerSnapshot {
	if t == nil {
		return nil
	}

	t.mu.Lock()
	snap := t.snapshotUnsafe()
	t.mu.Unlock()

	return &snap
}

func (t *Timer) snapshotUnsafe() TimerSnapshot {
	// keep timer state up to date before exporting
	t.updateStateUnsafe()
	return TimerSnapshot{
		StartTime: t.startTime,
		Duration:  t.duration,
		State:     t.state,
		StopTime:  t.stopTime,
	}
}

func (t *Timer) restoreUnsafe(snap *TimerSnapshot) {
	defer t.updateStateUnsafe()

	if t.realTimer != nil {
		t.realTimer.Stop()
		t.realTimer = nil
	}

	t.startTime = snap.StartTime
	t.duration = snap.Duration
	t.state = snap.State
	t.stopTime = snap.StopTime
	// reset runtime-only fields
	t.callback = nil
	t.callbackExecuted = false
}

var jsonNull = []byte("null")

// MarshalJSON implements json.Marshaler.
func (t *Timer) MarshalJSON() ([]byte, error) {
	if t == nil {
		return jsonNull, nil
	}

	t.mu.Lock()
	snap := t.snapshotUnsafe()
	t.mu.Unlock()

	return errors.Wrap2(json.Marshal(&snap))
}

// UnmarshalJSON implements json.Unmarshaler.
func (t *Timer) UnmarshalJSON(data []byte) error {
	var snap *TimerSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return errors.Wrap(err)
	}

	if err := snap.Validate(); err != nil {
		return errors.Wrap(err)
	}

	t.mu.Lock()
	t.restoreUnsafe(snap)
	t.mu.Unlock()

	return nil
}

// ToJSON serializes the timer to a JSON string.
func (t *Timer) ToJSON() ([]byte, error) {
	return errors.Wrap2(json.Marshal(t))
}

// FromJSON deserializes a timer from a JSON string.
func FromJSON(data []byte) (*Timer, error) {
	var tmr *Timer
	if err := json.Unmarshal(data, &tmr); err != nil {
		return nil, errors.Wrap(err)
	}

	if tmr == nil {
		return nil, nil //nolint:nilnil
	}

	return tmr, nil
}

// SnapshotTimer safely snapshots the provided timer.
func SnapshotTimer(t *Timer) *TimerSnapshot {
	if t == nil {
		return nil
	}
	return t.Snapshot()
}

// RestoreTimer recreates a SerializableTimer from its snapshot.
// It returns an error if snap is nil or contains an invalid state.
// Callback-related fields are left nil; callers should reattach callbacks or
// restart timers using [SetCallback] / [Reset] as appropriate after restoration.
func RestoreTimer(snap *TimerSnapshot) (*Timer, error) {
	if err := snap.Validate(); err != nil {
		return nil, errors.Wrap(err)
	}

	timer := new(Timer)
	timer.restoreUnsafe(snap)
	return timer, nil
}
