# timeutil Package

The `timeutil` package provides a serializable timer implementation designed for use with SIP transactions and other time-sensitive operations that need to be persisted and restored.

## Features

- **Serializable**: Can be marshaled to/from JSON for persistence
- **State-aware**: Tracks timer state (running, stopped, expired)
- **Expiration handling**: Properly handles timers that expire during serialization
- **AfterFunc support**: Similar to `time.AfterFunc`, executes callbacks when timer expires
- **Automatic execution**: Real `time.Timer` runs in background, no manual `UpdateState()` needed
- **Thread-safe**: All operations are safe for concurrent use
- **Flexible**: Supports custom start times for restoration scenarios
- **Snapshot support**: Exports lightweight `TimerSnapshot` structures for persistence outside JSON

## Timer States

- `TimerStateRunning`: Timer is currently active and counting down
- `TimerStateStopped`: Timer was manually stopped before expiration
- `TimerStateExpired`: Timer has naturally expired

## Basic Usage

```go
package main

import (
    "fmt"
    "time"
    "github.com/ghettovoice/gosip/internal/timeutil"
)

func main() {
    // Create a new timer that expires after 5 seconds
    timer := timeutil.NewTimer(5 * time.Second)
    
    // Check timer state
    if timer.Expired() {
        fmt.Println("Timer has expired")
        // Apply cleanup actions, send timeout response, etc.
    }
    
    // Get remaining time
    left := timer.Left()
    fmt.Printf("Time remaining: %v\n", left)
    
    // Get elapsed time
    elapsed := timer.Elapsed()
    fmt.Printf("Time elapsed: %v\n", elapsed)
}
```

## AfterFunc Usage

The timer supports `AfterFunc` functionality similar to `time.AfterFunc` but with automatic execution:

```go
// Create timer with callback - executes automatically
timer := timeutil.AfterFunc(5*time.Second, func() {
    fmt.Println("Timer expired!")
    // Handle timeout: send 408 response, cleanup resources, etc.
})

// Or set callback after creation - also executes automatically
timer := timeutil.NewTimer(5 * time.Second)
timer.SetCallback(func() {
    fmt.Println("Timer expired!")
})

// No manual UpdateState() needed - real timer runs in background
// Timer will automatically execute callback after 5 seconds

// For restored timers, SetCallback automatically detects expiration
restoredTimer, _ := timeutil.FromJSON(data)
restoredTimer.SetCallback(func() {
    fmt.Println("Restored timer expired!")
})
// No UpdateState() needed - SetCallback handles expired timers automatically
```

## Using FromTime

For advanced scenarios where you need to recreate a timer with a specific start time:

```go
// Recreate timer from saved start time and duration
savedStartTime := time.Now().Add(-2 * time.Second)
timer := timeutil.FromTime(savedStartTime, 5*time.Second)

// Important: Call UpdateState() to check if already expired
timer.UpdateState()

if timer.Expired() {
    fmt.Println("Timer already expired")
}

// If setting a callback, UpdateState() will trigger it if expired
timer.SetCallback(func() {
    fmt.Println("Callback executed!")
})
timer.UpdateState() // Triggers callback immediately if expired
```

## Serialization Example

```go
// Create and configure a timer
timer := timeutil.NewTimer(10 * time.Second)

// Serialize to JSON (uses the same representation as TimerSnapshot)
data, err := timer.ToJSON()
if err != nil {
    panic(err)
}

// Store `data` in database, send over network, etc.

// Later, restore the timer
restoredTimer, err := timeutil.FromJSON(data)
if err != nil {
    panic(err)
}

// State is automatically updated during unmarshaling
// Only call UpdateState() if you need to re-check after some time
if restoredTimer.Expired() {
    fmt.Println("Timer expired while serialized")
    // Handle timeout scenario appropriately
}

// Callbacks are not serialized; reattach them after restoration if needed
restoredTimer.SetCallback(func() {
    fmt.Println("Restored timer callback!")
})
```

## Snapshot & JSON Format

The serialized timer uses the following JSON structure:

```json
{
  "start_time": "2025-11-03T12:54:05.184256+03:00",
  "duration": 5000000000,
  "state": "running",
  "stop_time": "2025-11-03T12:54:07.184256+03:00"
}
```

- `start_time`: ISO 8601 timestamp when timer started
- `duration`: Duration in nanoseconds (Go's native format)
- `state`: Current timer state
- `stop_time`: Timestamp when stopped (optional, only present for stopped timers)

Snapshots and JSON share the same schema, so you can either persist raw `TimerSnapshot`
structures (via `Snapshot()` / `SnapshotTimer()`) or the JSON representation. Snapshots are
useful when you want tighter control over the storage format (e.g., gob/Protobuf), while JSON
is helpful for logging or network interchange. In either case, use `RestoreTimer()` to create a
fresh `SerializableTimer` instance and then reattach callbacks as needed.

## Key Methods

### Creation

- `NewTimer(duration)` - Create and start timer immediately
- `AfterFunc(duration, callback)` - Create timer with callback (package function)
- `FromTime(startTime, duration)` - Create with specific start time

### Callback Management

- `SetCallback(callback)` - Set function to execute when timer expires  
- `UpdateState()` - Update state and trigger callbacks if expired (note: automatically called during JSON unmarshaling)

### State Queries

- `State()` - Get current timer state (thread-safe)
- `StartTime()` - Get timer's start time
- `Duration()` - Get timer's duration  
- `StopTime()` - Get timer's stop time (nil if not stopped)
- `Expired()` - Check if timer has expired
- `Elapsed()` - Get time elapsed since start
- `Left()` - Get time remaining until expiration

### Control

- `Stop()` - Manually stop the timer
- `Reset(duration)` - Reset timer with new duration

### Serialization

- `ToJSON()` / `FromJSON()` - Direct JSON serialization
- Implements `json.Marshaler` and `json.Unmarshaler` interfaces
- `Snapshot()` / `SnapshotTimer()` - Capture immutable state without JSON
- `RestoreTimer()` - Create a new timer from a snapshot

## Important Notes

1. **Automatic execution**: Real `time.Timer` runs in background - no manual `UpdateState()` needed for normal operation
2. **`UpdateState()` is automatically called during JSON unmarshaling** - state is updated when you call `FromJSON()`
3. **`SetCallback()` automatically checks for expiration** - no need to call `UpdateState()` after setting a callback
4. **Call `UpdateState()` manually when**:
   - After `FromTime()` to check expiration and trigger callbacks
   - After setting a callback on a timer created with `FromTime()`
   - Re-checking expiration after time has passed
5. The timer automatically updates its state during marshaling to ensure accuracy
6. `Left()` returns 0 for stopped or expired timers
7. `Elapsed()` returns the full duration for expired timers
8. All time operations use `time.Time` for precision and timezone handling
9. Callbacks are executed in separate goroutines, similar to `time.AfterFunc`
10. If a timer is stopped, the callback will not be executed and the real timer is stopped
11. `Reset()` does not clear callbacks - it restarts the real timer with the new duration if a callback exists

## Thread Safety

The `SerializableTimer` is fully thread-safe. All methods use internal mutexes to protect concurrent access to timer state. You can safely call any method from multiple goroutines without external synchronization.
