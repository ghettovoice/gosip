package sip_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

type sendReqCall struct {
	ctx  context.Context
	req  *sip.RequestEnvelope
	opts sip.SendRequestOptions
}

type stubClientTransport struct {
	reliable bool

	sendReqCalls chan sendReqCall

	mu          sync.Mutex
	sendReqHook func(sendReqCall, int) error
	sendReqCnt  int
}

func newStubClientTransport(reliable bool) *stubClientTransport {
	return &stubClientTransport{
		reliable:     reliable,
		sendReqCalls: make(chan sendReqCall, 64),
	}
}

func (tp *stubClientTransport) Metadata() sip.TransportMetadata {
	if tp == nil {
		return sip.TransportMetadata{}
	}

	var flags sip.TransportFlags

	flags.SetReliable(tp.reliable)

	return sip.TransportMetadata{Flags: flags}
}

func (tp *stubClientTransport) SendRequest(
	ctx context.Context,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	var reqCopy *sip.RequestEnvelope
	if req != nil {
		reqCopy = req.Clone().(*sip.RequestEnvelope) //nolint:forcetypeassert
	}

	call := sendReqCall{
		ctx:  ctx,
		req:  reqCopy,
		opts: util.LastSliceElemOr(opts, sip.SendRequestOptions{}),
	}

	tp.mu.Lock()
	idx := tp.sendReqCnt
	tp.sendReqCnt++
	hook := tp.sendReqHook
	tp.mu.Unlock()

	if hook != nil {
		if err := hook(call, idx); err != nil {
			return err
		}
	}

	tp.sendReqCalls <- call

	return nil
}

func (tp *stubClientTransport) waitSendReq(tb testing.TB) sendReqCall {
	tb.Helper()

	tmr := time.NewTimer(5 * time.Second)
	defer tmr.Stop()

	select {
	case call := <-tp.sendReqCalls:
		return call
	case <-tmr.C:
		tb.Fatalf("timed out waiting for request send call")
		return sendReqCall{}
	}
}

func (tp *stubClientTransport) ensureNoSendReq(tb testing.TB) {
	tb.Helper()

	tmr := time.NewTimer(1 * time.Second)
	defer tmr.Stop()

	select {
	case call := <-tp.sendReqCalls:
		var mtd sip.RequestMethod
		if call.req != nil {
			mtd = call.req.Method()
		}

		tb.Fatalf("unexpected request send call with method %v", mtd)
	case <-tmr.C:
	}
}

func (tp *stubClientTransport) drainSendReqs() {
	for {
		select {
		case <-tp.sendReqCalls:
		default:
			return
		}
	}
}

func startClientTransaction(tb testing.TB, tx sip.ClientTransaction) {
	tb.Helper()

	if err := tx.Start(tb.Context()); err != nil {
		tb.Fatalf("tx.Start() error = %v, want nil", err)
	}
}

func assertResponseStatus(tb testing.TB, resCh <-chan *sip.ResponseEnvelope, want sip.ResponseStatus) {
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

func TestClientTransactionKey_RoundTripBinary(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		key  sip.ClientTransactionKey
	}{
		{
			name: "standard",
			key: sip.ClientTransactionKey{
				Branch: "z9hG4bK-123",
				Method: "INVITE",
			},
		},
		{
			name: "lowercase method",
			key: sip.ClientTransactionKey{
				Branch: "z9hG4bK-abc",
				Method: "invite",
			},
		},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			original := c.key

			data, err := original.MarshalBinary()
			if err != nil {
				t.Fatalf("key.MarshalBinary() error = %v", err)
			}

			if len(data) == 0 {
				t.Fatalf("key.MarshalBinary() = %v, want non-empty", data)
			}

			var restored sip.ClientTransactionKey
			if err := restored.UnmarshalBinary(data); err != nil {
				t.Fatalf("new.UnmarshalBinary(data) error = %v, want nil", err)
			}

			// Compare with canonical form (method uppercased)
			if !original.Canonic().Equal(&restored) {
				t.Fatalf("round-trip mismatch: got %+v, want %+v", restored, original.Canonic())
			}
		})
	}
}

func TestClientTransactionKey_UnmarshalBinary_Invalid(t *testing.T) {
	t.Parallel()

	var key sip.ClientTransactionKey
	if err := key.UnmarshalBinary([]byte{0x03}); err == nil {
		t.Fatalf("key.UnmarshalBinary([]byte{0x03}) = nil, want error")
	}
}
