package timeutil_test

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/timeutil"
)

func TestNewTimer(t *testing.T) {
	t.Parallel()

	duration := 100 * time.Millisecond
	timer := timeutil.NewTimer(duration)

	if timer.Duration() != duration {
		t.Errorf("timer.Duration() = %v, want %v", timer.Duration(), duration)
	}

	if timer.State() != timeutil.TimerStateRunning {
		t.Errorf("timer.State() = %v, want %v", timer.State(), timeutil.TimerStateRunning)
	}

	if timer.StartTime().IsZero() {
		t.Errorf("timer.StartTime() = %v, want non-zero", timer.StartTime())
	}
}

func TestFromTime(t *testing.T) {
	t.Parallel()

	startTime := time.Now().Add(-50 * time.Millisecond)
	duration := 100 * time.Millisecond
	timer := timeutil.FromTime(startTime, duration)

	if timer.StartTime().Unix() != startTime.Unix() {
		t.Errorf("timer.StartTime().Unix() = %v, want %v", timer.StartTime().Unix(), startTime.Unix())
	}

	if timer.Duration() != duration {
		t.Errorf("timer.Duration() = %v, want %v", timer.Duration(), duration)
	}

	// Should have some elapsed time
	elapsed := timer.Elapsed()
	if elapsed < 50*time.Millisecond {
		t.Errorf("timer.Elapsed() = %v, want >= 50ms", elapsed)
	}
}

func TestAfterFunc(t *testing.T) {
	t.Parallel()

	// Test timer expiration with callback
	duration := 10 * time.Millisecond

	var callbackExecuted atomic.Int32

	timer := timeutil.AfterFunc(duration, func() { callbackExecuted.Store(1) })

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)
	timer.UpdateState()

	// Give callback time to execute
	time.Sleep(10 * time.Millisecond)

	if callbackExecuted.Load() == 0 {
		t.Error("callback must have been executed")
	}

	if !timer.Expired() {
		t.Errorf("timer.Expired() = %v, want true", timer.Expired())
	}
}

func TestAfterFunc_AutoExecution(t *testing.T) {
	t.Parallel()

	// Test that the real timer automatically executes callbacks
	// without requiring manual UpdateState() calls
	duration := 50 * time.Millisecond

	var callbackExecuted atomic.Int32

	timer := timeutil.AfterFunc(duration, func() { callbackExecuted.Store(1) })

	// Wait for timer to expire naturally
	time.Sleep(duration + 20*time.Millisecond)

	// Check that callback was executed automatically
	if callbackExecuted.Load() == 0 {
		t.Error("callback must have been executed automatically by real timer")
	}

	if !timer.Expired() {
		t.Errorf("timer.Expired() = %v, want true", timer.Expired())
	}
}

func TestTimer_Elapsed(t *testing.T) {
	t.Parallel()

	duration := 100 * time.Millisecond
	timer := timeutil.NewTimer(duration)

	// Test elapsed while running
	time.Sleep(10 * time.Millisecond)

	elapsed := timer.Elapsed()
	if elapsed < 10*time.Millisecond {
		t.Errorf("timer.Elapsed() = %v, want >= 10ms", elapsed)
	}

	// Test elapsed after stopping
	timer.Stop()

	elapsedAfterStop := timer.Elapsed()
	if elapsedAfterStop < 10*time.Millisecond {
		t.Errorf("timer.Elapsed() = %v, want >= 10ms", elapsedAfterStop)
	}
}

func TestTimer_Left(t *testing.T) {
	t.Parallel()

	duration := 100 * time.Millisecond
	timer := timeutil.NewTimer(duration)

	// Test time left while running
	time.Sleep(10 * time.Millisecond)

	left := timer.Left()
	if left > 90*time.Millisecond {
		t.Errorf("timer.Left() = %v, want <= 90ms", left)
	}

	// Test time left after stopping
	timer.Stop()

	leftAfterStop := timer.Left()
	if leftAfterStop != 0 {
		t.Errorf("timer.Left() after stop = %v, want 0", leftAfterStop)
	}
}

func TestTimer_Expired(t *testing.T) {
	t.Parallel()

	duration := 10 * time.Millisecond
	timer := timeutil.NewTimer(duration)

	// Should not be expired initially
	if timer.Expired() {
		t.Errorf("timer.Expired() = %v, want false", timer.Expired())
	}

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Update state to check expiration
	timer.UpdateState()

	if !timer.Expired() {
		t.Errorf("timer.Expired() = %v, want true", timer.Expired())
	}

	if timer.State() != timeutil.TimerStateExpired {
		t.Errorf("timer.State() = %v, want %v", timer.State(), timeutil.TimerStateExpired)
	}
}

func TestTimer_Stop(t *testing.T) {
	t.Parallel()

	duration := 100 * time.Millisecond
	timer := timeutil.NewTimer(duration)

	timer.Stop()

	if timer.State() != timeutil.TimerStateStopped {
		t.Errorf("timer.State() = %v, want %v", timer.State(), timeutil.TimerStateStopped)
	}

	if timer.StopTime().IsZero() {
		t.Errorf("timer.StopTime() = %v, want non-zero", timer.StopTime())
	}
}

func TestTimer_Stop_PreventsCallbackExecution(t *testing.T) {
	t.Parallel()

	// Test that stopping the timer prevents automatic callback execution
	duration := 50 * time.Millisecond

	var callbackExecuted atomic.Int32

	timer := timeutil.AfterFunc(duration, func() { callbackExecuted.Store(1) })

	// Stop timer before expiration
	time.Sleep(10 * time.Millisecond)
	timer.Stop()

	// Wait past original expiration time
	time.Sleep(duration + 20*time.Millisecond)

	// Check that callback was NOT executed
	if callbackExecuted.Load() != 0 {
		t.Error("callback must not have been executed for stopped timer")
	}

	if timer.State() != timeutil.TimerStateStopped {
		t.Errorf("timer.State() = %v, want %v", timer.State(), timeutil.TimerStateStopped)
	}
}

func TestTimer_Reset(t *testing.T) {
	t.Parallel()

	duration := 100 * time.Millisecond
	timer := timeutil.NewTimer(duration)

	time.Sleep(10 * time.Millisecond)

	newDuration := 200 * time.Millisecond
	timer.Reset(newDuration)

	if timer.Duration() != newDuration {
		t.Errorf("timer.Duration() = %v, want %v", timer.Duration(), newDuration)
	}

	if timer.State() != timeutil.TimerStateRunning {
		t.Errorf("timer.State() = %v, want %v", timer.State(), timeutil.TimerStateRunning)
	}

	if !timer.StopTime().IsZero() {
		t.Errorf("timer.StopTime() = %v, want zero", timer.StopTime())
	}
}

func TestTimer_Reset_RestartsRealTimer(t *testing.T) {
	t.Parallel()

	// Ensure Reset restarts underlying timer and executes callback after new duration
	initialDuration := 200 * time.Millisecond

	var callbackCount atomic.Int32

	timer := timeutil.AfterFunc(initialDuration, func() { callbackCount.Add(1) })

	// Reset to a shorter duration before the original one fires
	time.Sleep(50 * time.Millisecond)
	newDuration := 100 * time.Millisecond
	timer.Reset(newDuration)

	// Wait long enough for the reset timer to fire
	time.Sleep(newDuration + 50*time.Millisecond)

	if got := callbackCount.Load(); got != 1 {
		t.Fatalf("expected callback to run once after reset, got %d", got)
	}

	if !timer.Expired() {
		t.Errorf("timer.Expired() = %v, want true", timer.Expired())
	}
}

func TestTimer_Reset_DelaysCallback(t *testing.T) {
	t.Parallel()

	// Test that Reset delays callback execution
	duration := 100 * time.Millisecond

	var callbackExecuted atomic.Int32

	timer := timeutil.NewTimer(duration)
	timer.SetCallback(func() { callbackExecuted.Store(1) })

	// Reset timer before expiration
	timer.Reset(200 * time.Millisecond)

	// Wait past original expiration time
	time.Sleep(150 * time.Millisecond)

	if callbackExecuted.Load() != 0 {
		t.Error("callback must not have been executed after reset")
	}

	if timer.State() != timeutil.TimerStateRunning {
		t.Errorf("timer.State() = %v, want %v", timer.State(), timeutil.TimerStateRunning)
	}
}

func TestTimer_SetCallback_Expired(t *testing.T) {
	t.Parallel()

	// Test setting callback on already expired timer
	duration := 10 * time.Millisecond

	var callbackExecuted atomic.Int32

	timer := timeutil.NewTimer(duration)

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)
	timer.UpdateState()

	// Set callback after expiration
	timer.SetCallback(func() { callbackExecuted.Store(1) })

	// Give callback time to execute
	time.Sleep(10 * time.Millisecond)

	if callbackExecuted.Load() == 0 {
		t.Error("callback must have been executed immediately for already expired timer")
	}
}

func TestTimer_SetCallback_Stopped(t *testing.T) {
	t.Parallel()

	// Test that stopped timers don't execute callbacks
	duration := 100 * time.Millisecond

	var callbackExecuted atomic.Int32

	timer := timeutil.NewTimer(duration)
	timer.SetCallback(func() { callbackExecuted.Store(1) })

	// Stop timer before expiration
	timer.Stop()

	// Wait past original expiration time
	time.Sleep(150 * time.Millisecond)

	if callbackExecuted.Load() != 0 {
		t.Error("callback must not have been executed for stopped timer")
	}

	if timer.State() != timeutil.TimerStateStopped {
		t.Errorf("timer.State() = %v, want %v", timer.State(), timeutil.TimerStateStopped)
	}
}

func TestTimer_SetCallback_AutoExecution(t *testing.T) {
	t.Parallel()

	// Test that SetCallback also starts the real timer automatically
	duration := 50 * time.Millisecond

	var callbackExecuted atomic.Int32

	timer := timeutil.NewTimer(duration)

	// Set callback after creation
	timer.SetCallback(func() { callbackExecuted.Store(1) })

	// Wait for timer to expire naturally
	time.Sleep(duration + 20*time.Millisecond)

	// Check that callback was executed automatically
	if callbackExecuted.Load() == 0 {
		t.Error("callback must have been executed automatically by real timer")
	}

	if !timer.Expired() {
		t.Errorf("timer.Expired() = %v, want true", timer.Expired())
	}
}

func TestTimer_SetCallback_WithSerialization(t *testing.T) {
	t.Parallel()

	// Test callback execution after serialization/deserialization
	duration := 10 * time.Millisecond

	timer := timeutil.NewTimer(duration)
	timer.SetCallback(func() { t.Log("original timer callback executed") })

	// Serialize timer
	data, err := timer.ToJSON()
	if err != nil {
		t.Fatalf("failed to serialize timer: %v", err)
	}

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Restore timer
	restoredTimer, err := timeutil.FromJSON(data)
	if err != nil {
		t.Fatalf("failed to deserialize timer: %v", err)
	}

	// Set callback on restored timer
	var restoredCallbackExecuted atomic.Int32
	restoredTimer.SetCallback(func() { restoredCallbackExecuted.Store(1) })

	// Update state to trigger callback if expired
	restoredTimer.UpdateState()

	// Give callback time to execute
	time.Sleep(10 * time.Millisecond)

	if restoredCallbackExecuted.Load() == 0 {
		t.Error("restored timer callback must have been executed")
	}

	if !restoredTimer.Expired() {
		t.Errorf("restoredTimer.Expired() = %v, want true", restoredTimer.Expired())
	}
}

func TestTimer_SetCallback_WithSerialization_NoExtraUpdate(t *testing.T) {
	t.Parallel()

	// Test that FromJSON() restores expired state without requiring explicit UpdateState().
	duration := 10 * time.Millisecond

	timer := timeutil.NewTimer(duration)

	// Serialize timer
	data, err := timer.ToJSON()
	if err != nil {
		t.Fatalf("failed to serialize timer: %v", err)
	}

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Restore timer via helper that updates state during restoration.
	restoredTimer, err := timeutil.FromJSON(data)
	if err != nil {
		t.Fatalf("failed to deserialize timer: %v", err)
	}

	if restoredTimer.State() != timeutil.TimerStateExpired {
		t.Errorf("restoredTimer.State() = %v, want %v", restoredTimer.State(), timeutil.TimerStateExpired)
	}

	done := make(chan struct{}, 1)

	var callbackExecuted atomic.Int32
	restoredTimer.SetCallback(func() {
		callbackExecuted.Add(1)

		select {
		case done <- struct{}{}:
		default:
		}
	})

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("restored timer callback must have been executed without UpdateState")
	}

	if callbackExecuted.Load() != 1 {
		t.Errorf("callback execution count = %d, want 1", callbackExecuted.Load())
	}

	if !restoredTimer.Expired() {
		t.Errorf("restoredTimer.Expired() = %v, want true", restoredTimer.Expired())
	}
}

func TestTimer_RoundTripJSON(t *testing.T) {
	t.Parallel()

	duration := 100 * time.Millisecond
	timer := timeutil.NewTimer(duration)

	// Serialize to JSON
	data, err := timer.ToJSON()
	if err != nil {
		t.Fatalf("failed to serialize timer: %v", err)
	}

	// Deserialize from JSON
	restoredTimer, err := timeutil.FromJSON(data)
	if err != nil {
		t.Fatalf("failed to deserialize timer: %v", err)
	}

	// Check basic properties
	if restoredTimer.Duration() != timer.Duration() {
		t.Errorf("restoredTimer.Duration() = %v, want %v", restoredTimer.Duration(), timer.Duration())
	}

	if restoredTimer.StartTime().Unix() != timer.StartTime().Unix() {
		t.Errorf("restoredTimer.StartTime().Unix() = %v, want %v", restoredTimer.StartTime().Unix(), timer.StartTime().Unix())
	}
}

func TestTimer_RoundTripJSON_Expired(t *testing.T) {
	t.Parallel()

	duration := 10 * time.Millisecond
	timer := timeutil.NewTimer(duration)

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)
	timer.UpdateState()

	// Serialize to JSON
	data, err := timer.ToJSON()
	if err != nil {
		t.Fatalf("failed to serialize timer: %v", err)
	}

	// Deserialize from JSON
	restoredTimer, err := timeutil.FromJSON(data)
	if err != nil {
		t.Fatalf("failed to deserialize timer: %v", err)
	}

	// Should be expired after unmarshaling and state update
	if !restoredTimer.Expired() {
		t.Errorf("restoredTimer.Expired() = %v, want true", restoredTimer.Expired())
	}

	if restoredTimer.State() != timeutil.TimerStateExpired {
		t.Errorf("restoredTimer.State() = %v, want %v", restoredTimer.State(), timeutil.TimerStateExpired)
	}
}

func ExampleSerializableTimer() {
	// Create a new timer that expires after 3 seconds
	timer := timeutil.NewTimer(3 * time.Second)

	// Set a callback to execute when timer expires
	timer.SetCallback(func() {
		// Handle timeout: send response, cleanup, etc.
	})

	// Check timer state
	if timer.Expired() {
		// Timer has expired, apply additional actions
		fmt.Println("timer expired!")
	} else {
		fmt.Println("timer is still running")
	}

	// Get remaining time
	left := timer.Left()
	if left > 0 {
		// Timer is still running
	}

	// Serialize timer for persistence
	data, _ := timer.ToJSON()

	// Wait for expiration
	time.Sleep(3100 * time.Millisecond)

	// Later, restore the timer
	restoredTimer, _ := timeutil.FromJSON(data)

	// Set callback for restored timer
	restoredTimer.SetCallback(func() {
		fmt.Println("callback fired!")
	})

	if restoredTimer.Expired() {
		// Timer expired while serialized, handle appropriately
		fmt.Println("restored timer expired!")
	}

	time.Sleep(10 * time.Millisecond)

	// Output:
	// timer is still running
	// restored timer expired!
	// callback fired!
}
