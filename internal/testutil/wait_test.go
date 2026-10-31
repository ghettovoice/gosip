package testutil_test

import (
	"testing"
	"testing/synctest"

	"github.com/ghettovoice/gosip/internal/testutil"
)

func TestWaitResult_Receive(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ch := make(chan string)
		go func() { ch <- "result" }()

		if got, want := testutil.WaitResult(t, ch, "result"), "result"; got != want {
			t.Errorf("testutil.WaitResult() = %q, want %q", got, want)
		}
	})
}

func TestWaitResult_ClosedChannel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ch := make(chan string)
		close(ch)

		if got, want := testutil.WaitResult(t, ch, "closed channel"), ""; got != want {
			t.Errorf("testutil.WaitResult() = %q, want zero value %q", got, want)
		}
	})
}

func TestWaitClosed_ClosedChannel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ch := make(chan struct{})
		go close(ch)

		testutil.WaitClosed(t, ch, "channel closure")
	})
}
