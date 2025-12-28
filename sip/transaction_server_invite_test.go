package sip_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/sip"
)

func TestInviteServerTransaction_AutoTrying(t *testing.T) {
	t.Parallel()

	timings := sip.NewTimings(0, 0, 0, 0, 10*time.Millisecond)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5060), false)

	req := newInInviteReq(t, tp.Proto(), sip.MagicCookie+".auto-trying", local, remote)

	tx, err := sip.NewInviteServerTransaction(req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction(INVITE, tp, opts) error = %v, want nil", err)
	}

	call := tp.waitSend(t, 2*timings.TimeG())
	if call.res.Status() != sip.ResponseStatusTrying {
		t.Fatalf("unexpected auto response status: got %v, want %v", call.res.Status(), sip.ResponseStatusTrying)
	}

	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusRinging, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 180, nil) error = %v, want nil", err)
	}

	call = tp.waitSend(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusRinging {
		t.Fatalf("unexpected ringing status: got %v, want %v", call.res.Status(), sip.ResponseStatusRinging)
	}

	tp.ensureNoSend(t, 100*time.Millisecond)

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

	tp := newStubServerTransport("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5060), false)

	req := newInInviteReq(t, tp.Proto(), sip.MagicCookie+".timed-out", local, remote)

	tx, err := sip.NewInviteServerTransaction(req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction(INVITE, tp, opts) error = %v, want nil", err)
	}

	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusBusyHere, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 486, nil) error = %v, want nil", err)
	}

	call := tp.waitSend(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusBusyHere {
		t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
	}

	if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	errCh := make(chan error, 1)
	tx.OnError(func(ctx context.Context, _ sip.Transaction, err error) {
		select {
		case errCh <- err:
		default:
		}
	})

	deadline := time.NewTimer(timings.TimeH() + timings.TimeG())
	defer deadline.Stop()

	for i := range 10 {
		select {
		case call := <-tp.sendCh():
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

	tp.ensureNoSend(t, 2*timings.TimeG())
}

func TestInviteServerTransaction_Confirmed(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5060), false)

	req := newInInviteReq(t, tp.Proto(), sip.MagicCookie+".confirmed", local, remote)

	tx, err := sip.NewInviteServerTransaction(req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction(INVITE, tp, opts) error = %v, want nil", err)
	}

	// send 486 final response -> transition to completed
	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusBusyHere, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 486, nil) error = %v, want nil", err)
	}

	call := tp.waitSend(t, 100*time.Millisecond)
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

	call = tp.waitSend(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusBusyHere {
		t.Fatalf("resend response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
	}

	// resend 486 final response on timer G
	// select used cause timer G can already fired first time (usually when tests running with -race flag)
	select {
	case call := <-tp.sendCh():
		if call.res.Status() != sip.ResponseStatusBusyHere {
			t.Fatalf("resend response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
		}
	default:
		call := tp.waitSend(t, timings.TimeG())
		if call.res.Status() != sip.ResponseStatusBusyHere {
			t.Fatalf("resend response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
		}
	}

	// receive ACK -> transition to confirmed
	ack := newInAckReq(t, req, tx.LastResponse())
	if err := tx.RecvRequest(ctx, ack); err != nil {
		t.Fatalf("tx.RecvRequest(ctx, ACK) error = %v, want nil", err)
	}

	//nolint:exhaustive
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

	tp.ensureNoSend(t, 100*time.Millisecond)
}

func TestInviteServerTransaction_ConfirmedRelTransp(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport("TCP", "tcp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5060), true)

	req := newInInviteReq(t, tp.Proto(), sip.MagicCookie+".confirmed", local, remote)

	tx, err := sip.NewInviteServerTransaction(req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction(INVITE, tp, opts) error = %v, want nil", err)
	}

	// send 486 final response -> transition to completed
	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusBusyHere, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 486, nil) error = %v, want nil", err)
	}

	call := tp.waitSend(t, 100*time.Millisecond)
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

	call = tp.waitSend(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusBusyHere {
		t.Fatalf("resend response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
	}

	// timer G not started for reliable transport
	tp.ensureNoSend(t, 2*timings.T2())

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

	tp.ensureNoSend(t, 100*time.Millisecond)
}

func TestInviteServerTransaction_Accepted(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5060), false)

	req := newInInviteReq(t, tp.Proto(), sip.MagicCookie+".accepted", local, remote)

	tx, err := sip.NewInviteServerTransaction(req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction(INVITE, tp, opts) error = %v, want nil", err)
	}

	// send 200 final response -> transition to accepted
	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
	}

	call := tp.waitSend(t, 100*time.Millisecond)
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

	tp.ensureNoSend(t, timings.T2())

	// resend 2xx from TU
	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
	}

	call = tp.waitSend(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusOK {
		t.Fatalf("resend response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
	}

	// RFC3261 2xx ACK does not match transaction
	if err := tx.RecvRequest(ctx, newInAckReq(t, req, tx.LastResponse())); err == nil {
		t.Fatal("tx.RecvRequest(ctx, ACK) error = nil, want error")
	}

	waitForTransactState(t, tx, sip.TransactionStateTerminated, timings.TimeL()+100*time.Millisecond)

	tp.ensureNoSend(t, 100*time.Millisecond)
}

func TestInviteServerTransaction_AcceptedRFC2543(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5060), false)

	req := newInInviteReq(t, tp.Proto(), "rfc2543.accepted", local, remote)

	tx, err := sip.NewInviteServerTransaction(req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction(INVITE, tp, opts) error = %v, want nil", err)
	}

	// send 200 final response -> transition to accepted
	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
	}

	call := tp.waitSend(t, 100*time.Millisecond)
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

	tp.ensureNoSend(t, timings.T2())

	// resend 2xx from TU
	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
	}

	call = tp.waitSend(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusOK {
		t.Fatalf("resend response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
	}

	ackCh := make(chan *sip.InboundRequest, 1)
	tx.OnAck(func(ctx context.Context, _ sip.ServerTransaction, ack *sip.InboundRequest) {
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

	tp.ensureNoSend(t, 100*time.Millisecond)
}

func TestInviteServerTransaction_AcceptedTranspErr(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5060), false)

	sendErr := errors.New("transport test error")
	tp.setSendHook(func(_ sendResCall, idx int) error {
		if idx >= 1 {
			return errtrace.Wrap(sendErr)
		}
		return nil
	})

	req := newInInviteReq(t, tp.Proto(), sip.MagicCookie+".accepted-transp-err", local, remote)

	tx, err := sip.NewInviteServerTransaction(req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteServerTransaction(INVITE, tp, opts) error = %v, want nil", err)
	}

	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
	}

	call := tp.waitSend(t, 100*time.Millisecond)
	if call.res.Status() != sip.ResponseStatusOK {
		t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
	}

	if got, want := tx.State(), sip.TransactionStateAccepted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	errCh := make(chan error, 1)
	tx.OnError(func(ctx context.Context, _ sip.Transaction, err error) {
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

	tp.ensureNoSend(t, 100*time.Millisecond)
}

func TestInviteServerTransaction_RoundTripSnapshot(t *testing.T) {
	t.Parallel()

	t1 := 5 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, 2*t1)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	origTP := newStubServerTransport("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5060), false)
	req := newInInviteReq(t, origTP.Proto(), sip.MagicCookie+".snapshot", local, remote)

	tx, err := sip.NewInviteServerTransaction(req, origTP, &sip.ServerTransactionOptions{Timings: timings})
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

	call := origTP.waitSend(t, 200*time.Millisecond)
	if call.res.Status() == sip.ResponseStatusTrying {
		call = origTP.waitSend(t, 200*time.Millisecond)
	}
	if call.res.Status() != sip.ResponseStatusBusyHere {
		t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
	}

	restoredTP := newStubServerTransport(origTP.Proto(), origTP.Network(), origTP.LocalAddr(), origTP.Reliable())
	restored, err := sip.RestoreInviteServerTransaction(snap, restoredTP, &sip.ServerTransactionOptions{Timings: timings})
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

	retransmit := restoredTP.waitSend(t, 200*time.Millisecond)
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

	tp := newStubServerTransport("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5060), false)
	req := newInInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-proceeding", local, remote)

	tx, err := sip.NewInviteServerTransaction(req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("NewInviteServerTransaction error = %v", err)
	}

	tp.drainSends()

	if got := tx.State(); got != sip.TransactionStateProceeding {
		t.Fatalf("State() = %q, want %q", got, sip.TransactionStateProceeding)
	}

	stateCh := make(chan sip.TransactionState, 1)
	tx.OnStateChanged(func(_ context.Context, _ sip.Transaction, _, to sip.TransactionState) {
		if to == sip.TransactionStateTerminated {
			stateCh <- to
		}
	})

	ctx := t.Context()
	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("Terminate() error = %v", err)
	}

	select {
	case <-stateCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnStateChanged not called for Terminated")
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("State() after Terminate = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSend(t, 2*t1)
}

func TestInviteServerTransaction_Terminate_FromAccepted(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5060), false)
	req := newInInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-accepted", local, remote)

	tx, err := sip.NewInviteServerTransaction(req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("NewInviteServerTransaction error = %v", err)
	}

	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("Respond(200) error = %v", err)
	}
	tp.drainSends()

	if got := tx.State(); got != sip.TransactionStateAccepted {
		t.Fatalf("State() = %q, want %q", got, sip.TransactionStateAccepted)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("Terminate() error = %v", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("State() after Terminate = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSend(t, 2*t1)
}

func TestInviteServerTransaction_Terminate_FromCompleted(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5060), false)
	req := newInInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-completed", local, remote)

	tx, err := sip.NewInviteServerTransaction(req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("NewInviteServerTransaction error = %v", err)
	}

	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusBusyHere, nil); err != nil {
		t.Fatalf("Respond(486) error = %v", err)
	}
	tp.drainSends()

	if got := tx.State(); got != sip.TransactionStateCompleted {
		t.Fatalf("State() = %q, want %q", got, sip.TransactionStateCompleted)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("Terminate() error = %v", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("State() after Terminate = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSend(t, 2*t1)
}

func TestInviteServerTransaction_Terminate_FromConfirmed(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5060), false)
	req := newInInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-confirmed", local, remote)

	tx, err := sip.NewInviteServerTransaction(req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("NewInviteServerTransaction error = %v", err)
	}

	ctx := t.Context()
	if err := tx.Respond(ctx, sip.ResponseStatusBusyHere, nil); err != nil {
		t.Fatalf("Respond(486) error = %v", err)
	}
	tp.drainSends()

	ack := newInAckReq(t, req, tx.LastResponse())
	if err := tx.RecvRequest(ctx, ack); err != nil {
		t.Fatalf("RecvRequest(ACK) error = %v", err)
	}

	if got := tx.State(); got != sip.TransactionStateConfirmed {
		t.Fatalf("State() = %q, want %q", got, sip.TransactionStateConfirmed)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("Terminate() error = %v", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("State() after Terminate = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSend(t, 2*t1)
}

func TestInviteServerTransaction_Terminate_Idempotent(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5060")

	tp := newStubServerTransport("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5060), false)
	req := newInInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-idempotent", local, remote)

	tx, err := sip.NewInviteServerTransaction(req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("NewInviteServerTransaction error = %v", err)
	}

	tp.drainSends()
	ctx := t.Context()

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("first Terminate() error = %v", err)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("second Terminate() error = %v", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("State() = %q, want %q", got, sip.TransactionStateTerminated)
	}
}
