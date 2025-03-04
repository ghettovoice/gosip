package sip_test

import (
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
)

func assertResponseStatus(tb testing.TB, resCh <-chan *sip.InboundResponse, want sip.ResponseStatus) {
	tb.Helper()

	select {
	case res := <-resCh:
		if res.Status() != want {
			tb.Fatalf("response status = %v, want %v", res.Status(), want)
		}
	case <-time.After(100 * time.Millisecond):
		tb.Fatalf("expected response with status %v", want)
	}
}
