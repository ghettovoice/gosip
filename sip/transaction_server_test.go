package sip_test

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

type sendResCall struct {
	ctx  context.Context
	res  *sip.OutboundResponseEnvelope
	opts *sip.SendResponseOptions
}

type stubServerTransport struct {
	reliable bool

	sendResCalls chan sendResCall

	mu          sync.Mutex
	sendResHook func(sendResCall, int) error
	sendResCnt  int
}

func newStubServerTransport(reliable bool) *stubServerTransport {
	return &stubServerTransport{
		reliable:     reliable,
		sendResCalls: make(chan sendResCall, 64),
	}
}

func (tp *stubServerTransport) Reliable() bool {
	if tp == nil {
		return false
	}
	return tp.reliable
}

func (tp *stubServerTransport) SendResponse(
	ctx context.Context,
	res *sip.OutboundResponseEnvelope,
	opts *sip.SendResponseOptions,
) error {
	call := sendResCall{
		ctx:  ctx,
		res:  res,
		opts: cloneSendResponseOptions(opts),
	}

	tp.mu.Lock()
	idx := tp.sendResCnt
	tp.sendResCnt++
	hook := tp.sendResHook
	tp.mu.Unlock()

	if hook != nil {
		if err := hook(call, idx); err != nil {
			return err
		}
	}

	tp.sendResCalls <- call

	return nil
}

func (tp *stubServerTransport) setSendResHook(hook func(sendResCall, int) error) {
	tp.mu.Lock()
	tp.sendResHook = hook
	tp.mu.Unlock()
}

func (tp *stubServerTransport) waitSendRes(tb testing.TB, timeout time.Duration) sendResCall {
	tb.Helper()

	tmr := time.NewTimer(timeout)
	defer tmr.Stop()

	select {
	case call := <-tp.sendResCalls:
		return call
	case <-tmr.C:
		tb.Fatalf("timed out waiting for response send call")
		return sendResCall{}
	}
}

func (tp *stubServerTransport) ensureNoSendRes(tb testing.TB, timeout time.Duration) {
	tb.Helper()

	tmr := time.NewTimer(timeout)
	defer tmr.Stop()

	select {
	case call := <-tp.sendResCalls:
		tb.Fatalf("unexpected response send call with status %v", call.res.Status())
	case <-tmr.C:
	}
}

func (tp *stubServerTransport) sendResChan() <-chan sendResCall {
	return tp.sendResCalls
}

func (tp *stubServerTransport) drainSendRess() {
	for {
		select {
		case <-tp.sendResCalls:
		default:
			return
		}
	}
}

func cloneSendResponseOptions(opts *sip.SendResponseOptions) *sip.SendResponseOptions {
	if opts == nil {
		return nil
	}

	copyOpts := *opts

	return &copyOpts
}

func TestServerTransactionKey_RoundTripBinary(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		key  sip.ServerTransactionKey
	}{
		{
			name: "rfc3261",
			key: sip.ServerTransactionKey{
				Branch: "z9hG4bK-123",
				SentBy: "Example.com:5060",
				Method: "INVITE",
			},
		},
		{
			name: "rfc2543",
			key: sip.ServerTransactionKey{
				Method:  "INVITE",
				URI:     "sip:user@example.com",
				FromTag: "from",
				ToTag:   "to",
				CallID:  "call",
				SeqNum:  42,
				Via:     "SIP/2.0/UDP example.com:5060",
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

			// t.Logf("hash: %x", data)

			var restored sip.ServerTransactionKey
			if err := restored.UnmarshalBinary(data); err != nil {
				t.Fatalf("new.UnmarshalBinary(data) error = %v, want nil", err)
			}

			if !original.Equal(&restored) {
				t.Fatalf("round-trip mismatch: got %+v, want %+v", restored, original)
			}
		})
	}
}

func TestServerTransactionKey_UnmarshalBinary_Invalid(t *testing.T) {
	t.Parallel()

	var key sip.ServerTransactionKey
	if err := key.UnmarshalBinary([]byte{0x03}); err == nil {
		t.Fatalf("key.UnmarshalBinary([]byte{0x03}) = nil, want error")
	}
}

func TestInviteServerTransaction_AutoTrying(t *testing.T) {
	t.Parallel()

	timings := sip.NewTimings(0, 0, 0, 0, 10*time.Millisecond)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport(false)

	req := newInInviteReq(t, "UDP", sip.MagicCookie+".auto-trying", local, remote)

	tx, err := sip.NewInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
	}

	call := tp.waitSendRes(t, 2*timings.TimeG())
	if call.res.Status() != sip.ResponseStatusTrying {
		t.Fatalf("unexpected auto response status: got %v, want %v", call.res.Status(), sip.ResponseStatusTrying)
	}

	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusRinging, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 180, nil) error = %v, want nil", err)
	}

	call = tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusRinging {
		t.Fatalf("unexpected ringing status: got %v, want %v", call.res.Status(), sip.ResponseStatusRinging)
	}

	tp.ensureNoSendRes(t, 100*time.Millisecond)

	if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}
}

func TestInviteServerTransaction_CompletedTimedOut(t *testing.T) {
	t.Parallel()

	t1 := 100 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport(false)

	req := newInInviteReq(t, "UDP", sip.MagicCookie+".timed-out", local, remote)

	tx, err := sip.NewInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
	}

	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusBusyHere, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 486, nil) error = %v, want nil", err)
	}

	call := tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusBusyHere {
		t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
	}

	if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	errCh := make(chan error, 1)
	tx.OnError(func(ctx context.Context, err error) {
		select {
		case errCh <- err:
		default:
		}
	})

	deadline := time.NewTimer(timings.TimeH() + timings.TimeG())
	defer deadline.Stop()

	for i := range 10 {
		select {
		case call := <-tp.sendResChan():
			if call.res.Status() != sip.ResponseStatusBusyHere {
				t.Fatalf("resend response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
			}
		case <-deadline.C:
			t.Fatalf("expected 10 retransmits before timer H, got %d", i)
		}
	}

	waitForTransactState(t, tx, sip.TransactionStateTerminated, timings.TimeH()+2*timings.TimeG())

	select {
	case err := <-errCh:
		if !errors.Is(err, sip.ErrTransactionTimedOut) {
			t.Fatalf("transport error mismatch: got %v, want %v", err, sip.ErrTransactionTimedOut)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected transport error callback")
	}

	tp.ensureNoSendRes(t, 2*timings.TimeG())
}

func TestInviteServerTransaction_Confirmed(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport(false)

	req := newInInviteReq(t, "UDP", sip.MagicCookie+".confirmed", local, remote)

	tx, err := sip.NewInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
	}

	// send 486 final response -> transition to completed
	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusBusyHere, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 486, nil) error = %v, want nil", err)
	}

	call := tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusBusyHere {
		t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
	}

	if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	// resend 486 final response on INVITE retransmit
	if err := tx.RecvRequest(ctx, req); err != nil {
		t.Fatalf("tx.RecvRequest(ctx, INVITE) error = %v, want nil", err)
	}

	call = tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusBusyHere {
		t.Fatalf("resend response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
	}

	// resend 486 final response on timer G
	// select used cause timer G can already fired first time (usually when tests running with -race flag)
	select {
	case call := <-tp.sendResChan():
		if call.res.Status() != sip.ResponseStatusBusyHere {
			t.Fatalf("resend response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
		}
	default:
		call := tp.waitSendRes(t, timings.TimeG())
		if call.res.Status() != sip.ResponseStatusBusyHere {
			t.Fatalf("resend response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
		}
	}

	// receive ACK -> transition to confirmed
	ack := newInAckReq(t, req, tx.LastResponse())
	if err := tx.RecvRequest(ctx, ack); err != nil {
		t.Fatalf("tx.RecvRequest(ctx, ACK) error = %v, want nil", err)
	}

	switch state := tx.State(); state {
	case sip.TransactionStateConfirmed:
		// expected path when timer I hasn't fired yet
	case sip.TransactionStateTerminated:
		// reliable transports set timer I to 0, so transaction may terminate immediately
	default:
		t.Fatalf("tx.State() = %q, want %q or %q", state, sip.TransactionStateConfirmed, sip.TransactionStateTerminated)
	}

	// no-op on INVITE, ACK retransmits
	if err := tx.RecvRequest(ctx, req); err != nil {
		t.Fatalf("tx.RecvRequest(ctx, INVITE) error = %v, want nil", err)
	}

	if err := tx.RecvRequest(ctx, ack); err != nil {
		t.Fatalf("tx.RecvRequest(ctx, ACK) error = %v, want nil", err)
	}

	waitForTransactState(t, tx, sip.TransactionStateTerminated, timings.TimeI()+100*time.Millisecond)

	tp.ensureNoSendRes(t, 100*time.Millisecond)
}

func TestInviteServerTransaction_ConfirmedRelTransp(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport(true)

	req := newInInviteReq(t, "TCP", sip.MagicCookie+".confirmed", local, remote)

	tx, err := sip.NewInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
	}

	// send 486 final response -> transition to completed
	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusBusyHere, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 486, nil) error = %v, want nil", err)
	}

	call := tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusBusyHere {
		t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
	}

	if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	// resend 486 final response on INVITE retransmit
	if err := tx.RecvRequest(ctx, req); err != nil {
		t.Fatalf("tx.RecvRequest(ctx, INVITE) error = %v, want nil", err)
	}

	call = tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusBusyHere {
		t.Fatalf("resend response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
	}

	// timer G not started for reliable transport
	tp.ensureNoSendRes(t, 2*timings.T2())

	// receive ACK -> transition to confirmed
	ack := newInAckReq(t, req, tx.LastResponse())
	if err := tx.RecvRequest(ctx, ack); err != nil {
		t.Fatalf("tx.RecvRequest(ctx, ACK) error = %v, want nil", err)
	}

	if got, want := tx.State(), sip.TransactionStateConfirmed; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	// timer I start with 0ms delay
	waitForTransactState(t, tx, sip.TransactionStateTerminated, 100*time.Millisecond)

	tp.ensureNoSendRes(t, 100*time.Millisecond)
}

func TestInviteServerTransaction_Accepted(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport(false)

	req := newInInviteReq(t, "UDP", sip.MagicCookie+".accepted", local, remote)

	tx, err := sip.NewInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction(INVITE, tp, opts) error = %v, want nil", err)
	}

	// send 200 final response -> transition to accepted
	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
	}

	call := tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusOK {
		t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
	}

	if got, want := tx.State(), sip.TransactionStateAccepted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	// no-op on INVITE retransmit
	if err := tx.RecvRequest(ctx, req); err != nil {
		t.Fatalf("tx.RecvRequest(ctx, INVITE) error = %v, want nil", err)
	}

	tp.ensureNoSendRes(t, timings.T2())

	// resend 2xx from TU
	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
	}

	call = tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusOK {
		t.Fatalf("resend response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
	}

	// RFC3261 2xx ACK does not match transaction
	if err := tx.RecvRequest(ctx, newInAckReq(t, req, tx.LastResponse())); err == nil {
		t.Fatal("tx.RecvRequest(ctx, ACK) error = nil, want error")
	}

	waitForTransactState(t, tx, sip.TransactionStateTerminated, timings.TimeL()+100*time.Millisecond)

	tp.ensureNoSendRes(t, 100*time.Millisecond)
}

func TestInviteServerTransaction_AcceptedRFC2543(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport(false)

	req := newInInviteReq(t, "UDP", "rfc2543.accepted", local, remote)

	tx, err := sip.NewInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction(INVITE, tp, opts) error = %v, want nil", err)
	}

	// send 200 final response -> transition to accepted
	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
	}

	call := tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusOK {
		t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
	}

	if got, want := tx.State(), sip.TransactionStateAccepted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	// no-op on INVITE retransmit
	if err := tx.RecvRequest(ctx, req); err != nil {
		t.Fatalf("tx.RecvRequest(ctx, INVITE) error = %v, want nil", err)
	}

	tp.ensureNoSendRes(t, timings.T2())

	// resend 2xx from TU
	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
	}

	call = tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusOK {
		t.Fatalf("resend response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
	}

	ackCh := make(chan *sip.InboundRequestEnvelope, 1)
	tx.OnAck(func(ctx context.Context, ack *sip.InboundRequestEnvelope) {
		select {
		case ackCh <- ack:
		default:
		}
	})

	// RFC3261 2xx ACK must match transaction
	if err := tx.RecvRequest(ctx, newInAckReq(t, req, tx.LastResponse())); err != nil {
		t.Fatalf("tx.RecvRequest(ctx, ACK) error = %v, want nil", err)
	}

	select {
	case ack := <-ackCh:
		if ack.Method() != sip.RequestMethodAck {
			t.Fatalf("ACK method mismatch: got %v, want %v", ack.Method(), sip.RequestMethodAck)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected ACK callback")
	}

	waitForTransactState(t, tx, sip.TransactionStateTerminated, timings.TimeL()+100*time.Millisecond)

	tp.ensureNoSendRes(t, 100*time.Millisecond)
}

func TestInviteServerTransaction_AcceptedTranspErr(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport(false)

	sendErr := errors.New("transport test error")
	tp.setSendResHook(func(_ sendResCall, idx int) error {
		if idx >= 1 {
			return sendErr
		}
		return nil
	})

	req := newInInviteReq(t, "UDP", sip.MagicCookie+".accepted-transp-err", local, remote)

	tx, err := sip.NewInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction(INVITE, tp, opts) error = %v, want nil", err)
	}

	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
	}

	call := tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusOK {
		t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
	}

	if got, want := tx.State(), sip.TransactionStateAccepted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	errCh := make(chan error, 1)
	tx.OnError(func(ctx context.Context, err error) {
		select {
		case errCh <- err:
		default:
		}
	})

	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, sendErr) {
			t.Fatalf("transport error mismatch: got %v, want %v", err, sendErr)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected transport error callback")
	}

	if got, want := tx.State(), sip.TransactionStateAccepted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	waitForTransactState(t, tx, sip.TransactionStateTerminated, timings.TimeL()+100*time.Millisecond)

	tp.ensureNoSendRes(t, 100*time.Millisecond)
}

func TestInviteServerTransaction_RoundTripSnapshot(t *testing.T) {
	t.Parallel()

	t1 := 5 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, 2*t1)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	origTP := newStubServerTransport(false)
	req := newInInviteReq(t, "UDP", sip.MagicCookie+".snapshot", local, remote)

	tx, err := sip.NewInviteServerTransaction(t.Context(), req, origTP, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction(INVITE, tp, opts) error = %v, want nil", err)
	}

	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusBusyHere, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 486, nil) error = %v, want nil", err)
	}

	snap := tx.Snapshot()
	if snap == nil {
		t.Fatal("tx.Snapshot() = nil, want snapshot")
	}

	call := origTP.waitSendRes(t, 200*time.Millisecond)
	if call.res.Status() == sip.ResponseStatusTrying {
		call = origTP.waitSendRes(t, 200*time.Millisecond)
	}

	if call.res.Status() != sip.ResponseStatusBusyHere {
		t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
	}

	restoredTP := newStubServerTransport(origTP.Reliable())

	restored, err := sip.RestoreInviteServerTransaction(t.Context(), snap, restoredTP, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.RestoreInviteServerTransaction(snap, tp, opts) error = %v, want nil", err)
	}

	if got, want := restored.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("restored.State() = %q, want %q", got, want)
	}

	if got, want := restored.Key(), tx.Key(); got != want {
		t.Fatalf("restored.Key() = %v, want %v", got, want)
	}

	if res := restored.LastResponse(); res.Status() != sip.ResponseStatusBusyHere {
		t.Fatalf("restored.LastResponse().Status() = %v, want %v", res.Status(), sip.ResponseStatusBusyHere)
	}

	retransmit := restoredTP.waitSendRes(t, 200*time.Millisecond)
	if retransmit.res.Status() != sip.ResponseStatusBusyHere {
		t.Fatalf("timer G resend mismatch: got %v, want %v", retransmit.res.Status(), sip.ResponseStatusBusyHere)
	}

	waitForTransactState(t, restored, sip.TransactionStateTerminated, timings.TimeH()+200*time.Millisecond)
}

func TestInviteServerTransaction_Terminate_FromProceeding(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport(false)
	req := newInInviteReq(t, "UDP", sip.MagicCookie+".terminate-proceeding", local, remote)

	tx, err := sip.NewInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
	}

	tp.drainSendRess()

	if got := tx.State(); got != sip.TransactionStateProceeding {
		t.Fatalf("State() = %q, want %q", got, sip.TransactionStateProceeding)
	}

	stateCh := make(chan sip.TransactionState, 1)
	tx.OnStateChanged(func(_ context.Context, _, to sip.TransactionState) {
		if to == sip.TransactionStateTerminated {
			stateCh <- to
		}
	})

	ctx := t.Context()
	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	select {
	case <-stateCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnStateChanged callback timeout")
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSendRes(t, 2*t1)
}

func TestInviteServerTransaction_Terminate_FromAccepted(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport(false)
	req := newInInviteReq(t, "UDP", sip.MagicCookie+".terminate-accepted", local, remote)

	tx, err := sip.NewInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
	}

	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
	}

	tp.drainSendRess()

	if got := tx.State(); got != sip.TransactionStateAccepted {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateAccepted)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSendRes(t, 2*t1)
}

func TestInviteServerTransaction_Terminate_FromCompleted(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport(false)
	req := newInInviteReq(t, "UDP", sip.MagicCookie+".terminate-completed", local, remote)

	tx, err := sip.NewInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
	}

	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusBusyHere, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 486, nil) error = %v, want nil", err)
	}

	tp.drainSendRess()

	if got := tx.State(); got != sip.TransactionStateCompleted {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateCompleted)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSendRes(t, 2*t1)
}

func TestInviteServerTransaction_Terminate_FromConfirmed(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport(false)
	req := newInInviteReq(t, "UDP", sip.MagicCookie+".terminate-confirmed", local, remote)

	tx, err := sip.NewInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
	}

	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusBusyHere, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 486, nil) error = %v, want nil", err)
	}

	tp.drainSendRess()

	ack := newInAckReq(t, req, tx.LastResponse())
	if err := tx.RecvRequest(ctx, ack); err != nil {
		t.Fatalf("tx.RecvRequest(ACK) error = %v, want nil", err)
	}

	if got := tx.State(); got != sip.TransactionStateConfirmed {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateConfirmed)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSendRes(t, 2*t1)
}

func TestInviteServerTransaction_Terminate_Idempotent(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport(false)
	req := newInInviteReq(t, "UDP", sip.MagicCookie+".terminate-idempotent", local, remote)

	tx, err := sip.NewInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
	}

	tp.drainSendRess()

	ctx := t.Context()

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}
}

func TestNonInviteServerTransaction_LifecycleUnrelTransp(t *testing.T) {
	t.Parallel()

	t1 := 5 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, 2*t1)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5070")

	tp := newStubServerTransport(false)
	req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".unreliable", local, remote)

	tx, err := sip.NewNonInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
	}

	ctx := t.Context()

	if err := tx.Respond(ctx, sip.ResponseStatusRinging, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 180, nil) error = %v, want nil", err)
	}

	call := tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusRinging {
		t.Fatalf("provisional response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusRinging)
	}

	if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	if err := tx.RecvRequest(ctx, req); err != nil {
		t.Fatalf("tx.RecvRequest(ctx, request) error = %v, want nil", err)
	}

	call = tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusRinging {
		t.Fatalf("retransmit provisional mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusRinging)
	}

	if err := tx.Respond(ctx, sip.ResponseStatusCallIsBeingForwarded, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 181, nil) error = %v, want nil", err)
	}

	call = tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusCallIsBeingForwarded {
		t.Fatalf("updated provisional mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusCallIsBeingForwarded)
	}

	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
	}

	call = tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusOK {
		t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
	}

	if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	if err := tx.RecvRequest(ctx, req); err != nil {
		t.Fatalf("tx.RecvRequest(ctx, request) error = %v, want nil", err)
	}

	call = tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusOK {
		t.Fatalf("retransmit final mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
	}

	if err := tx.Respond(ctx, sip.ResponseStatusTrying, nil); err == nil {
		t.Fatal("tx.Respond(ctx, 100, nil) error = nil, want error")
	} else if !errors.Is(err, sip.ErrActionNotAllowed) {
		t.Fatalf("unexpected error: got %v, want %v", err, sip.ErrActionNotAllowed)
	}

	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
	}

	tp.ensureNoSendRes(t, 100*time.Millisecond)

	waitForTransactState(t, tx, sip.TransactionStateTerminated, timings.TimeJ()+200*time.Millisecond)

	tp.ensureNoSendRes(t, 100*time.Millisecond)
}

func TestNonInviteServerTransaction_SendResponseMismatchCallID(t *testing.T) {
	t.Parallel()

	timings := sip.NewTimings(0, 0, 0, 0, 0)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5070")

	tp := newStubServerTransport(false)
	req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".mismatch-callid", local, remote)

	tx, err := sip.NewNonInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
	}

	res, err := req.NewResponse(sip.ResponseStatusRinging, nil)
	if err != nil {
		t.Fatalf("req.NewResponse(180, nil) error = %v, want nil", err)
	}

	res.AccessMessage(func(r *sip.Response) {
		r.Headers.Set(header.CallID("call-mismatch@localhost"))
	})

	ctx := t.Context()
	if err := tx.SendResponse(ctx, res, nil); err == nil {
		t.Fatal("tx.SendResponse(ctx, res, nil) error = nil, want error")
	} else if !errors.Is(err, sip.ErrInvalidArgument) {
		t.Fatalf("unexpected error: got %v, want wrapped %v", err, sip.ErrInvalidArgument)
	}
}

func TestNonInviteServerTransaction_SendResponseMismatchVia(t *testing.T) {
	t.Parallel()

	timings := sip.NewTimings(0, 0, 0, 0, 0)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5070")

	tp := newStubServerTransport(false)
	req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".mismatch-via", local, remote)

	tx, err := sip.NewNonInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
	}

	res, err := req.NewResponse(sip.ResponseStatusRinging, nil)
	if err != nil {
		t.Fatalf("req.NewResponse(180, nil) error = %v, want nil", err)
	}

	res.AccessMessage(func(r *sip.Response) {
		if via, ok := r.Headers.FirstViaHop(); ok && via != nil {
			via.Params.Set("branch", sip.MagicCookie+".other-branch")
		}
	})

	ctx := t.Context()
	if err := tx.SendResponse(ctx, res, nil); err == nil {
		t.Fatal("tx.SendResponse(ctx, res, nil) error = nil, want error")
	} else if !errors.Is(err, sip.ErrInvalidArgument) {
		t.Fatalf("unexpected error: got %v, want wrapped %v", err, sip.ErrInvalidArgument)
	}
}

func TestNonInviteServerTransaction_SendResponseMismatchCSeqMethod(t *testing.T) {
	t.Parallel()

	timings := sip.NewTimings(0, 0, 0, 0, 0)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5070")

	tp := newStubServerTransport(false)
	req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".mismatch-cseq", local, remote)

	tx, err := sip.NewNonInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
	}

	res, err := req.NewResponse(sip.ResponseStatusRinging, nil)
	if err != nil {
		t.Fatalf("req.NewResponse(180, nil) error = %v, want nil", err)
	}

	res.AccessMessage(func(r *sip.Response) {
		r.Headers.Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInvite})
	})

	ctx := t.Context()
	if err := tx.SendResponse(ctx, res, nil); err == nil {
		t.Fatal("tx.SendResponse(ctx, res, nil) error = nil, want error")
	} else if !errors.Is(err, sip.ErrInvalidArgument) {
		t.Fatalf("unexpected error: got %v, want wrapped %v", err, sip.ErrInvalidArgument)
	}
}

func TestNonInviteServerTransaction_ProceedingTranspErr(t *testing.T) {
	t.Parallel()

	t1 := 5 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, 2*t1)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5070")

	tp := newStubServerTransport(false)
	sendErr := errors.New("transport test error")
	tp.setSendResHook(func(_ sendResCall, idx int) error {
		if idx >= 1 {
			return sendErr
		}
		return nil
	})

	req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".transp-err", local, remote)

	tx, err := sip.NewNonInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
	}

	ctx := t.Context()

	if err := tx.Respond(ctx, sip.ResponseStatusRinging, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 180, nil) error = %v, want nil", err)
	}

	call := tp.waitSendRes(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusRinging {
		t.Fatalf("provisional response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusRinging)
	}

	if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	errCh := make(chan error, 1)
	tx.OnError(func(ctx context.Context, err error) {
		select {
		case errCh <- err:
		default:
		}
	})

	if err := tx.Respond(ctx, sip.ResponseStatusCallIsBeingForwarded, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 181, nil) error = %v, want nil", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, sendErr) {
			t.Fatalf("transport error mismatch: got %v, want wrapped %v", err, sendErr)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected transport error callback")
	}

	waitForTransactState(t, tx, sip.TransactionStateTerminated, 200*time.Millisecond)

	tp.ensureNoSendRes(t, 100*time.Millisecond)
}

func TestNonInviteServerTransaction_RoundTripSnapshot(t *testing.T) {
	t.Parallel()

	t1 := 5 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, 2*t1)

	remote := netip.MustParseAddrPort("44.44.44.44:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	origTP := newStubServerTransport(false)
	req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".snapshot", local, remote)

	tx, err := sip.NewNonInviteServerTransaction(t.Context(), req, origTP, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
	}

	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
	}

	call := origTP.waitSendRes(t, 50*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusOK {
		t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
	}

	if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	snap := tx.Snapshot()
	if snap == nil || snap.TimerJ == nil {
		t.Fatalf("tx.Snapshot().TimerJ = %v, want non-nil", snap.TimerJ)
	}

	restoredTP := newStubServerTransport(origTP.Reliable())

	restored, err := sip.RestoreNonInviteServerTransaction(t.Context(), snap, restoredTP, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.RestoreNonInviteServerTransaction() error = %v, want nil", err)
	}

	if got, want := restored.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("restored.State() = %q, want %q", got, want)
	}

	if got, want := restored.Key(), tx.Key(); got != want {
		t.Fatalf("restored.Key() = %v, want %v", got, want)
	}

	if res := restored.LastResponse(); res.Status() != sip.ResponseStatusOK {
		t.Fatalf("restored.LastResponse().Status() = %v, want %v", res.Status(), sip.ResponseStatusOK)
	}

	waitForTransactState(t, restored, sip.TransactionStateTerminated, timings.TimeJ()+200*time.Millisecond)
}

func TestNonInviteServerTransaction_Terminate_FromTrying(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5070")

	tp := newStubServerTransport(false)
	req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-trying", local, remote)

	tx, err := sip.NewNonInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
	}

	if got := tx.State(); got != sip.TransactionStateTrying {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTrying)
	}

	stateCh := make(chan sip.TransactionState, 1)
	tx.OnStateChanged(func(_ context.Context, _, to sip.TransactionState) {
		if to == sip.TransactionStateTerminated {
			stateCh <- to
		}
	})

	ctx := t.Context()
	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v", err)
	}

	select {
	case <-stateCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnStateChanged callback timeout")
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSendRes(t, 2*t1)
}

func TestNonInviteServerTransaction_Terminate_FromProceeding(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5070")

	tp := newStubServerTransport(false)
	req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-proceeding", local, remote)

	tx, err := sip.NewNonInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
	}

	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusRinging, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 180, nil) error = %v, want nil", err)
	}

	tp.drainSendRess()

	if got := tx.State(); got != sip.TransactionStateProceeding {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateProceeding)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSendRes(t, 2*t1)
}

func TestNonInviteServerTransaction_Terminate_FromCompleted(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5070")

	tp := newStubServerTransport(false)
	req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-completed", local, remote)

	tx, err := sip.NewNonInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
	}

	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
	}

	tp.drainSendRess()

	if got := tx.State(); got != sip.TransactionStateCompleted {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateCompleted)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSendRes(t, 2*t1)
}

func TestNonInviteServerTransaction_Terminate_Idempotent(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5070")

	tp := newStubServerTransport(true)
	req := newInNonInviteReq(t, "TCP", sip.MagicCookie+".terminate-idempotent", local, remote)

	tx, err := sip.NewNonInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
	}

	ctx := t.Context()

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}
}
