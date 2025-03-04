// Package timeutil provides SerializableTimer, a drop-in replacement for time.AfterFunc that
// can be snapshotted, serialized, and restored for long-lived workflows such as SIP transaction
// timeouts.
//
// A SerializableTimer keeps its runtime behaviour—automatic callback execution via a background
// time.Timer—while exposing deterministic state through TimerSnapshot. Snapshots can be marshaled
// to JSON, stored, and later passed to RestoreTimer to obtain a fresh timer instance. Callbacks
// are runtime-only; they must be reattached after restoration with SetCallback or Reset.
//
// Basic usage:
//
//	// Create timer with automatic callback execution.
//	timer := timeutil.AfterFunc(5*time.Second, func() {
//	    log.Println("Timer expired!")
//	})
//
//	// Persist the timer state.
//	snap := timer.Snapshot()
//	data, _ := json.Marshal(snap)
//
//	// Restore later and reattach callbacks.
//	var restoredSnap timeutil.TimerSnapshot
//	_ = json.Unmarshal(data, &restoredSnap)
//	restored := timeutil.RestoreTimer(&restoredSnap)
//	restored.SetCallback(func() {
//	    log.Println("Restored timer expired!")
//	})
//
// All timer operations are thread-safe and can be called concurrently from multiple goroutines.
package timeutil
