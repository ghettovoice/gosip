package timeutil_test

import (
	"encoding/json"
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
		t.Errorf("Expected duration %v, got %v", duration, timer.Duration())
	}

	if timer.State() != timeutil.TimerStateRunning {
		t.Errorf("Expected state %v, got %v", timeutil.TimerStateRunning, timer.State())
	}

	if timer.StartTime().IsZero() {
		t.Error("Expected non-zero start time")
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
		t.Errorf("Expected elapsed time >= 10ms, got %v", elapsed)
	}

	// Test elapsed after stopping
	timer.Stop()
	elapsedAfterStop := timer.Elapsed()
	if elapsedAfterStop < 10*time.Millisecond {
		t.Errorf("Expected elapsed time after stop >= 10ms, got %v", elapsedAfterStop)
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
		t.Errorf("Expected time left <= 90ms, got %v", left)
	}

	// Test time left after stopping
	timer.Stop()
	leftAfterStop := timer.Left()
	if leftAfterStop != 0 {
		t.Errorf("Expected time left after stop to be 0, got %v", leftAfterStop)
	}
}

func TestTimer_Expired(t *testing.T) {
	t.Parallel()

	duration := 10 * time.Millisecond
	timer := timeutil.NewTimer(duration)

	// Should not be expired initially
	if timer.Expired() {
		t.Error("Timer should not be expired initially")
	}

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Update state to check expiration
	timer.UpdateState()

	if !timer.Expired() {
		t.Error("Timer should be expired after duration")
	}

	if timer.State() != timeutil.TimerStateExpired {
		t.Errorf("Expected state %v, got %v", timeutil.TimerStateExpired, timer.State())
	}
}

func TestTimer_Stop(t *testing.T) {
	t.Parallel()

	duration := 100 * time.Millisecond
	timer := timeutil.NewTimer(duration)

	timer.Stop()

	if timer.State() != timeutil.TimerStateStopped {
		t.Errorf("Expected state %v, got %v", timeutil.TimerStateStopped, timer.State())
	}

	if timer.StopTime().IsZero() {
		t.Error("Expected stop time to be set")
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
		t.Errorf("Expected duration %v, got %v", newDuration, timer.Duration())
	}

	if timer.State() != timeutil.TimerStateRunning {
		t.Errorf("Expected state %v, got %v", timeutil.TimerStateRunning, timer.State())
	}

	if !timer.StopTime().IsZero() {
		t.Error("Expected stop time to be nil after reset")
	}
}

func TestTimer_SerializeDeserialize(t *testing.T) {
	t.Parallel()

	duration := 100 * time.Millisecond
	timer := timeutil.NewTimer(duration)

	// Serialize to JSON
	data, err := timer.ToJSON()
	if err != nil {
		t.Fatalf("Failed to serialize timer: %v", err)
	}

	// Deserialize from JSON
	restoredTimer, err := timeutil.FromJSON(data)
	if err != nil {
		t.Fatalf("Failed to deserialize timer: %v", err)
	}

	// Check basic properties
	if restoredTimer.Duration() != timer.Duration() {
		t.Errorf("Expected duration %v, got %v", timer.Duration(), restoredTimer.Duration())
	}

	if restoredTimer.StartTime().Unix() != timer.StartTime().Unix() {
		t.Errorf("Expected start time %v, got %v", timer.StartTime(), restoredTimer.StartTime())
	}
}

func TestTimer_SerializeDeserialize_Expired(t *testing.T) {
	t.Parallel()

	duration := 10 * time.Millisecond
	timer := timeutil.NewTimer(duration)

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)
	timer.UpdateState()

	// Serialize to JSON
	data, err := timer.ToJSON()
	if err != nil {
		t.Fatalf("Failed to serialize timer: %v", err)
	}

	// Deserialize from JSON
	restoredTimer, err := timeutil.FromJSON(data)
	if err != nil {
		t.Fatalf("Failed to deserialize timer: %v", err)
	}

	// Should be expired after unmarshaling and state update
	if !restoredTimer.Expired() {
		t.Error("Restored timer should be expired")
	}

	if restoredTimer.State() != timeutil.TimerStateExpired {
		t.Errorf("Expected state %v, got %v", timeutil.TimerStateExpired, restoredTimer.State())
	}
}

func TestTimer_MarshalUnmarshalJSON(t *testing.T) {
	t.Parallel()

	duration := 50 * time.Millisecond
	timer := timeutil.NewTimer(duration)

	// Test custom JSON marshaling
	data, err := json.Marshal(timer)
	if err != nil {
		t.Fatalf("Failed to marshal timer: %v", err)
	}

	var restoredTimer timeutil.SerializableTimer
	err = json.Unmarshal(data, &restoredTimer)
	if err != nil {
		t.Fatalf("Failed to unmarshal timer: %v", err)
	}

	if restoredTimer.Duration() != timer.Duration() {
		t.Errorf("Expected duration %v, got %v", timer.Duration(), restoredTimer.Duration())
	}
}

func TestNewTimerWithStartTime(t *testing.T) {
	t.Parallel()

	startTime := time.Now().Add(-50 * time.Millisecond)
	duration := 100 * time.Millisecond
	timer := timeutil.FromTime(startTime, duration)

	if timer.StartTime().Unix() != startTime.Unix() {
		t.Errorf("Expected start time %v, got %v", startTime, timer.StartTime())
	}

	if timer.Duration() != duration {
		t.Errorf("Expected duration %v, got %v", duration, timer.Duration())
	}

	// Should have some elapsed time
	elapsed := timer.Elapsed()
	if elapsed < 50*time.Millisecond {
		t.Errorf("Expected elapsed time >= 50ms, got %v", elapsed)
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
		fmt.Println("Timer expired!")
	} else {
		fmt.Println("Timer is still running")
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
		fmt.Println("callback: Restored timer expired!")
	})

	if restoredTimer.Expired() {
		// Timer expired while serialized, handle appropriately
		fmt.Println("Restored timer expired!")
	}

	time.Sleep(10 * time.Millisecond)

	// Output:
	// Timer is still running
	// Restored timer expired!
	// callback: Restored timer expired!
}

func TestNewTimerWithFunc(t *testing.T) {
	t.Parallel()

	// Test timer expiration with callback
	duration := 10 * time.Millisecond
	var callbackExecuted int32 // atomic int32

	timer := timeutil.AfterFunc(duration, func() {
		atomic.StoreInt32(&callbackExecuted, 1)
	})

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)
	timer.UpdateState()

	// Give callback time to execute
	time.Sleep(10 * time.Millisecond)

	if atomic.LoadInt32(&callbackExecuted) == 0 {
		t.Error("Callback should have been executed")
	}

	if !timer.Expired() {
		t.Error("Timer should be expired")
	}
}

func TestTimer_AfterFunc_Expired(t *testing.T) {
	t.Parallel()

	// Test setting callback on already expired timer
	duration := 10 * time.Millisecond
	var callbackExecuted int32 // atomic int32

	timer := timeutil.NewTimer(duration)

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)
	timer.UpdateState()

	// Set callback after expiration
	timer.SetCallback(func() {
		atomic.StoreInt32(&callbackExecuted, 1)
	})

	// Give callback time to execute
	time.Sleep(10 * time.Millisecond)

	if atomic.LoadInt32(&callbackExecuted) == 0 {
		t.Error("Callback should have been executed immediately for already expired timer")
	}
}

func TestTimer_AfterFunc_Stopped(t *testing.T) {
	t.Parallel()

	// Test that stopped timers don't execute callbacks
	duration := 100 * time.Millisecond
	var callbackExecuted int32 // atomic int32

	timer := timeutil.NewTimer(duration)
	timer.SetCallback(func() {
		atomic.StoreInt32(&callbackExecuted, 1)
	})

	// Stop timer before expiration
	timer.Stop()

	// Wait past original expiration time
	time.Sleep(150 * time.Millisecond)

	if atomic.LoadInt32(&callbackExecuted) != 0 {
		t.Error("Callback should not have been executed for stopped timer")
	}

	if timer.State() != timeutil.TimerStateStopped {
		t.Errorf("Expected state %v, got %v", timeutil.TimerStateStopped, timer.State())
	}
}

func TestTimer_AfterFunc_WithSerialization(t *testing.T) {
	t.Parallel()

	// Test callback execution after serialization/deserialization
	duration := 10 * time.Millisecond

	timer := timeutil.NewTimer(duration)
	timer.SetCallback(func() {
		t.Log("Original timer callback executed")
	})

	// Serialize timer
	data, err := timer.ToJSON()
	if err != nil {
		t.Fatalf("Failed to serialize timer: %v", err)
	}

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Restore timer
	restoredTimer, err := timeutil.FromJSON(data)
	if err != nil {
		t.Fatalf("Failed to deserialize timer: %v", err)
	}

	// Set callback on restored timer
	var restoredCallbackExecuted int32 // atomic int32
	restoredTimer.SetCallback(func() {
		atomic.StoreInt32(&restoredCallbackExecuted, 1)
	})

	// Update state to trigger callback if expired
	restoredTimer.UpdateState()

	// Give callback time to execute
	time.Sleep(10 * time.Millisecond)

	if atomic.LoadInt32(&restoredCallbackExecuted) == 0 {
		t.Error("Restored timer callback should have been executed")
	}

	if !restoredTimer.Expired() {
		t.Error("Restored timer should be expired")
	}
}

func TestTimer_AfterFunc_WithSerialization_NoExtraUpdate(t *testing.T) {
	t.Parallel()

	// Test that FromJSON() automatically calls UpdateState() and triggers callbacks
	// if callback is set before unmarshaling
	duration := 10 * time.Millisecond

	timer := timeutil.NewTimer(duration)

	// Serialize timer
	data, err := timer.ToJSON()
	if err != nil {
		t.Fatalf("Failed to serialize timer: %v", err)
	}

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Restore timer with custom unmarshaling that sets callback first
	var restoredTimer timeutil.SerializableTimer
	if err := json.Unmarshal(data, &restoredTimer); err != nil {
		t.Fatalf("Failed to deserialize timer: %v", err)
	}

	// Set callback after unmarshaling but before checking state
	var callbackExecuted int32 // atomic int32
	restoredTimer.SetCallback(func() {
		atomic.StoreInt32(&callbackExecuted, 1)
	})

	// Call UpdateState() to trigger callback since timer was expired when unmarshaled
	// but callback was set after unmarshaling
	restoredTimer.UpdateState()

	// Give callback time to execute
	time.Sleep(10 * time.Millisecond)

	if atomic.LoadInt32(&callbackExecuted) == 0 {
		t.Error("Callback should have been executed")
	}

	if !restoredTimer.Expired() {
		t.Error("Timer should be expired")
	}
}

func TestTimer_Reset_ClearsCallback(t *testing.T) {
	t.Parallel()

	// Test that Reset clears callback state
	duration := 100 * time.Millisecond
	var callbackExecuted int32 // atomic int32

	timer := timeutil.NewTimer(duration)
	timer.SetCallback(func() {
		atomic.StoreInt32(&callbackExecuted, 1)
	})

	// Reset timer before expiration
	timer.Reset(200 * time.Millisecond)

	// Wait past original expiration time
	time.Sleep(150 * time.Millisecond)

	if atomic.LoadInt32(&callbackExecuted) != 0 {
		t.Error("Original callback should not have been executed after reset")
	}

	if timer.State() != timeutil.TimerStateRunning {
		t.Errorf("Expected state %v, got %v", timeutil.TimerStateRunning, timer.State())
	}
}

func TestTimer_AutoExecution(t *testing.T) {
	t.Parallel()

	// Test that the real timer automatically executes callbacks
	// without requiring manual UpdateState() calls
	duration := 50 * time.Millisecond
	var callbackExecuted int32 // atomic int32

	timer := timeutil.AfterFunc(duration, func() {
		atomic.StoreInt32(&callbackExecuted, 1)
	})

	// Wait for timer to expire naturally
	time.Sleep(duration + 20*time.Millisecond)

	// Check that callback was executed automatically
	if atomic.LoadInt32(&callbackExecuted) == 0 {
		t.Error("Callback should have been executed automatically by real timer")
	}

	if !timer.Expired() {
		t.Error("Timer should be expired")
	}
}

func TestTimer_AutoExecution_WithAfterFunc(t *testing.T) {
	t.Parallel()

	// Test that AfterFunc also starts the real timer automatically
	duration := 50 * time.Millisecond
	var callbackExecuted int32 // atomic int32

	timer := timeutil.NewTimer(duration)

	// Set callback after creation
	timer.SetCallback(func() {
		atomic.StoreInt32(&callbackExecuted, 1)
	})

	// Wait for timer to expire naturally
	time.Sleep(duration + 20*time.Millisecond)

	// Check that callback was executed automatically
	if atomic.LoadInt32(&callbackExecuted) == 0 {
		t.Error("Callback should have been executed automatically by real timer")
	}

	if !timer.Expired() {
		t.Error("Timer should be expired")
	}
}

func TestTimer_StopPreventsAutoExecution(t *testing.T) {
	t.Parallel()

	// Test that stopping the timer prevents automatic callback execution
	duration := 50 * time.Millisecond
	var callbackExecuted int32 // atomic int32

	timer := timeutil.AfterFunc(duration, func() {
		atomic.StoreInt32(&callbackExecuted, 1)
	})

	// Stop timer before expiration
	time.Sleep(10 * time.Millisecond)
	timer.Stop()

	// Wait past original expiration time
	time.Sleep(duration + 20*time.Millisecond)

	// Check that callback was NOT executed
	if atomic.LoadInt32(&callbackExecuted) != 0 {
		t.Error("Callback should not have been executed for stopped timer")
	}

	if timer.State() != timeutil.TimerStateStopped {
		t.Errorf("Expected state %v, got %v", timeutil.TimerStateStopped, timer.State())
	}
}

func TestTimer_ResetRestartsRealTimer(t *testing.T) {
	t.Parallel()

	// Ensure Reset restarts underlying timer and executes callback after new duration
	initialDuration := 200 * time.Millisecond
	var callbackCount int32

	timer := timeutil.AfterFunc(initialDuration, func() {
		atomic.AddInt32(&callbackCount, 1)
	})

	// Reset to a shorter duration before the original one fires
	time.Sleep(50 * time.Millisecond)
	newDuration := 100 * time.Millisecond
	timer.Reset(newDuration)

	// Wait long enough for the reset timer to fire
	time.Sleep(newDuration + 50*time.Millisecond)

	if got := atomic.LoadInt32(&callbackCount); got != 1 {
		t.Fatalf("expected callback to run once after reset, got %d", got)
	}

	if !timer.Expired() {
		t.Error("timer should be expired after reset duration elapsed")
	}
}
