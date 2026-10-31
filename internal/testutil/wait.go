// Package testutil provides reusable helpers for tests.
package testutil

import (
	"testing"
	"time"
)

const resultWaitTimeout = 5 * time.Second

// WaitResult receives a channel value with a timeout to prevent a test from
// hanging indefinitely.
func WaitResult[T any](tb testing.TB, ch <-chan T, what string) T {
	tb.Helper()

	timer := time.NewTimer(resultWaitTimeout)
	defer timer.Stop()

	select {
	case result := <-ch:
		return result
	case <-timer.C:
		tb.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

// WaitClosed waits for a channel to either produce a value or be closed.
func WaitClosed(tb testing.TB, ch <-chan struct{}, what string) {
	tb.Helper()
	_ = WaitResult(tb, ch, what)
}
