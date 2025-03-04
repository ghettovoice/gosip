package sip_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
)

func TestInviteClientTransaction_Accepted(t *testing.T) {
	t.Parallel()

	// Use a slightly bigger T1 so timer A does not fire before we inject responses on slower/-race runs.
	t1 := 20 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	// Use an unreliable transport to keep timer A enabled and avoid retransmit races in tests.
	tp := newStubClientTransport("UDP", "udp", local, false)

	req := newOutInviteReq(t, tp.Proto(), sip.MagicCookie+".client-accepted", local, remote)

	tx, err := sip.NewInviteClientTransaction(req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewClientInviteTransaction(INVITE, tp, opts) error = %v, want nil", err)
	}

	call := tp.waitSend(t, 100*time.Millisecond)
	if call.req.Method() != sip.RequestMethodInvite {
		t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
	}
	if call.req.RemoteAddr() != remote {
		t.Fatalf("initial send remote addr = %v, want %v", call.req.RemoteAddr(), remote)
	}

	if got, want := tx.State(), sip.TransactionStateCalling; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	ctx := t.Context()

	resCh := make(chan *sip.InboundResponse, 3)
	tx.OnResponse(func(_ context.Context, res *sip.InboundResponse) {
		resCh <- res
	})

	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
	}

	if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}
	assertResponseStatus(t, resCh, sip.ResponseStatusRinging)
	tp.drainSends()

	ok := newInRes(t, req, sip.ResponseStatusOK)
	if err := tx.RecvResponse(ctx, ok); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 200) error = %v, want nil", err)
	}
	if err := tx.RecvResponse(ctx, ok); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 200) error = %v, want nil", err)
	}

	if got, want := tx.State(), sip.TransactionStateAccepted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}
	assertResponseStatus(t, resCh, sip.ResponseStatusOK)
	tp.drainSends()

	// additional 2xx keeps transaction accepted and delivers to TU
	secondOK := ok.Clone().(*sip.InboundResponse) //nolint:forcetypeassert
	if err := tx.RecvResponse(ctx, secondOK); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 200 repeat) error = %v, want nil", err)
	}
	assertResponseStatus(t, resCh, sip.ResponseStatusOK)

	waitForTransactState(t, tx, sip.TransactionStateTerminated, timings.TimeM()+100*time.Millisecond)

	tp.ensureNoSend(t, 100*time.Millisecond)
}

func TestInviteClientTransaction_Rejected(t *testing.T) {
	t.Parallel()

	t1 := 20 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	tp := newStubClientTransport("UDP", "udp", local, false)

	req := newOutInviteReq(t, tp.Proto(), sip.MagicCookie+".client-rejected", local, remote)

	tx, err := sip.NewInviteClientTransaction(req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewClientInviteTransaction(INVITE, tp, opts) error = %v, want nil", err)
	}

	call := tp.waitSend(t, 100*time.Millisecond)
	if call.req.Method() != sip.RequestMethodInvite {
		t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
	}

	ctx := t.Context()

	resCh := make(chan *sip.InboundResponse, 2)
	tx.OnResponse(func(_ context.Context, res *sip.InboundResponse) {
		resCh <- res
	})

	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
	}
	assertResponseStatus(t, resCh, sip.ResponseStatusRinging)
	tp.drainSends()

	decline := newInRes(t, req, sip.ResponseStatusDecline)
	if err := tx.RecvResponse(ctx, decline); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 603) error = %v, want nil", err)
	}

	assertResponseStatus(t, resCh, sip.ResponseStatusDecline)

	if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	ackCall := tp.waitSend(t, 100*time.Millisecond)
	if ackCall.req.Method() != sip.RequestMethodAck {
		t.Fatalf("expected ACK send, got %v", ackCall.req.Method())
	}

	// Retransmitted final response should trigger another ACK send.
	secondDecline := decline.Clone().(*sip.InboundResponse) //nolint:forcetypeassert
	if err := tx.RecvResponse(ctx, secondDecline); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 603 retransmit) error = %v, want nil", err)
	}
	retransAck := tp.waitSend(t, 100*time.Millisecond)
	if retransAck.req.Method() != sip.RequestMethodAck {
		t.Fatalf("expected ACK retransmit, got %v", retransAck.req.Method())
	}

	waitForTransactState(t, tx, sip.TransactionStateTerminated, timings.TimeD()+200*time.Millisecond)
	tp.ensureNoSend(t, 100*time.Millisecond)
}

func TestInviteClientTransaction_Timeout(t *testing.T) {
	t.Parallel()

	t1 := 5 * time.Millisecond
	timings := sip.NewTimings(t1, 4*t1, 6*t1, 32*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	tp := newStubClientTransport("UDP", "udp", local, false)

	req := newOutInviteReq(t, tp.Proto(), sip.MagicCookie+".client-timeout", local, remote)

	tx, err := sip.NewInviteClientTransaction(req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewClientInviteTransaction(INVITE, tp, opts) error = %v, want nil", err)
	}

	call := tp.waitSend(t, 100*time.Millisecond)
	if call.req.Method() != sip.RequestMethodInvite {
		t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
	}

	resCh := make(chan *sip.InboundResponse, 1)
	tx.OnResponse(func(_ context.Context, res *sip.InboundResponse) {
		resCh <- res
	})

	timeout := timings.TimeB() + 200*time.Millisecond
	waitForTransactState(t, tx, sip.TransactionStateTerminated, timeout)

	select {
	case res := <-resCh:
		t.Fatalf("did not expect response, got %v", res.Status())
	default:
	}

	if res := tx.LastResponse(); res != nil {
		t.Fatalf("LastResponse() = %v, want nil", res.Status())
	}

	tp.drainSends()
	tp.ensureNoSend(t, 50*time.Millisecond)
}

func TestInviteClientTransaction_RoundTripSnapshot(t *testing.T) {
	t.Parallel()

	t1 := 20 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, 2*t1)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	origTP := newStubClientTransport("TCP", "tcp", local, true)
	req := newOutInviteReq(t, origTP.Proto(), sip.MagicCookie+".client-snapshot", local, remote)

	tx, err := sip.NewInviteClientTransaction(req, origTP, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewClientInviteTransaction(INVITE, tp, opts) error = %v, want nil", err)
	}

	initial := origTP.waitSend(t, 100*time.Millisecond)
	if initial.req.Method() != sip.RequestMethodInvite {
		t.Fatalf("initial send method = %q, want %q", initial.req.Method(), sip.RequestMethodInvite)
	}
	origTP.drainSends()

	ctx := t.Context()
	decline := newInRes(t, req, sip.ResponseStatusDecline)
	if err := tx.RecvResponse(ctx, decline); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 603) error = %v, want nil", err)
	}
	ackCall := origTP.waitSend(t, 100*time.Millisecond)
	if ackCall.req.Method() != sip.RequestMethodAck {
		t.Fatalf("expected ACK send, got %v", ackCall.req.Method())
	}

	snap := tx.Snapshot()
	if snap == nil {
		t.Fatal("tx.Snapshot() = nil, want snapshot")
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("json.Marshal(snapshot) error = %v, want nil", err)
	}

	var snapCopy sip.ClientTransactionSnapshot
	if err := json.Unmarshal(data, &snapCopy); err != nil {
		t.Fatalf("json.Unmarshal(snapshot) error = %v, want nil", err)
	}

	restoredTP := newStubClientTransport("TCP", "tcp", local, true)
	restored, err := sip.RestoreInviteClientTransaction(&snapCopy, restoredTP, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.RestoreInviteClientTransaction(snap, tp, opts) error = %v, want nil", err)
	}

	if got, want := restored.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("restored.State() = %q, want %q", got, want)
	}
	if got, want := restored.Key(), tx.Key(); !got.Equal(want) {
		t.Fatalf("restored.Key() = %v, want %v", got, want)
	}
	if res := restored.LastResponse(); res.Status() != sip.ResponseStatusDecline {
		t.Fatalf("restored.LastResponse().Status() = %v, want %v", res.Status(), sip.ResponseStatusDecline)
	}

	retransmit := decline.Clone().(*sip.InboundResponse) //nolint:forcetypeassert
	if err := restored.RecvResponse(ctx, retransmit); err != nil {
		t.Fatalf("restored.RecvResponse(ctx, 603) error = %v, want nil", err)
	}
	ack := restoredTP.waitSend(t, 100*time.Millisecond)
	if ack.req.Method() != sip.RequestMethodAck {
		t.Fatalf("expected ACK retransmit, got %v", ack.req.Method())
	}

	waitForTransactState(t, restored, sip.TransactionStateTerminated, timings.TimeD()+200*time.Millisecond)
}

func TestInviteClientTransaction_Terminate_FromCalling(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	tp := newStubClientTransport("UDP", "udp", local, false)
	req := newOutInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-calling", local, remote)

	tx, err := sip.NewInviteClientTransaction(req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("NewInviteClientTransaction error = %v", err)
	}

	tp.waitSend(t, 100*time.Millisecond)

	if got := tx.State(); got != sip.TransactionStateCalling {
		t.Fatalf("State() = %q, want %q", got, sip.TransactionStateCalling)
	}

	stateCh := make(chan sip.TransactionState, 1)
	tx.OnStateChanged(func(_ context.Context, _, to sip.TransactionState) {
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

func TestInviteClientTransaction_Terminate_FromProceeding(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	tp := newStubClientTransport("UDP", "udp", local, false)
	req := newOutInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-proceeding", local, remote)

	tx, err := sip.NewInviteClientTransaction(req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("NewInviteClientTransaction error = %v", err)
	}

	tp.waitSend(t, 100*time.Millisecond)
	ctx := t.Context()

	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
		t.Fatalf("RecvResponse(180) error = %v", err)
	}
	tp.drainSends()

	if got := tx.State(); got != sip.TransactionStateProceeding {
		t.Fatalf("State() = %q, want %q", got, sip.TransactionStateProceeding)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("Terminate() error = %v", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("State() after Terminate = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSend(t, 2*t1)
}

func TestInviteClientTransaction_Terminate_FromAccepted(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	tp := newStubClientTransport("UDP", "udp", local, false)
	req := newOutInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-accepted", local, remote)

	tx, err := sip.NewInviteClientTransaction(req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("NewInviteClientTransaction error = %v", err)
	}

	tp.waitSend(t, 100*time.Millisecond)
	ctx := t.Context()

	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
		t.Fatalf("RecvResponse(200) error = %v", err)
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

func TestInviteClientTransaction_Terminate_FromCompleted(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	tp := newStubClientTransport("UDP", "udp", local, false)
	req := newOutInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-completed", local, remote)

	tx, err := sip.NewInviteClientTransaction(req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("NewInviteClientTransaction error = %v", err)
	}

	tp.waitSend(t, 100*time.Millisecond)
	ctx := t.Context()

	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusDecline)); err != nil {
		t.Fatalf("RecvResponse(603) error = %v", err)
	}
	tp.waitSend(t, 100*time.Millisecond)
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

func TestInviteClientTransaction_Terminate_Idempotent(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	tp := newStubClientTransport("UDP", "udp", local, false)
	req := newOutInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-idempotent", local, remote)

	tx, err := sip.NewInviteClientTransaction(req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("NewInviteClientTransaction error = %v", err)
	}

	tp.waitSend(t, 100*time.Millisecond)
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
