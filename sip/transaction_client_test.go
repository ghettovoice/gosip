package sip_test

import (
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
)

func assertResponseStatus(tb testing.TB, resCh <-chan *sip.InboundResponseEnvelope, want sip.ResponseStatus) {
	tb.Helper()

	select {
	case res := <-resCh:
		if res.Status() != want {
			tb.Fatalf("res.Status = %v, want %v", res.Status(), want)
		}
	case <-time.After(100 * time.Millisecond):
		tb.Fatalf("response wait timeout, want %v", want)
	}
}
