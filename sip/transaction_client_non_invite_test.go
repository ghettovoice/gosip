package sip_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
)

func TestNonInviteClientTransaction_LifecycleUnreliable(t *testing.T) {
	t.Parallel()

	t1 := 20 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, 2*t1)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("22.22.22.22:5070")

	tp := newStubClientTransport("UDP", "udp", local, false)
	req := newOutNonInviteReq(t, tp.Proto(), sip.MagicCookie+".client-noninvite", local, remote)

	tx, err := sip.NewNonInviteClientTransaction(req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteClientTransaction(req, tp, opts) error = %v, want nil", err)
	}

	call := tp.waitSend(t, 100*time.Millisecond)
	if call.req.Method() != sip.RequestMethodInfo {
		t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInfo)
	}
	if call.req.RemoteAddr() != remote {
		t.Fatalf("initial send remote addr = %v, want %v", call.req.RemoteAddr(), remote)
	}

	if got, want := tx.State(), sip.TransactionStateTrying; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	// Timer E should retransmit the request while waiting for a response on unreliable transports.
	retrans := tp.waitSend(t, timings.TimeE()+50*time.Millisecond)
	if retrans.req.Method() != sip.RequestMethodInfo {
		t.Fatalf("retransmit method = %q, want %q", retrans.req.Method(), sip.RequestMethodInfo)
	}

	resCh := make(chan *sip.InboundResponse, 2)
	tx.OnResponse(func(_ context.Context, res *sip.InboundResponse) {
		resCh <- res
	})

	ctx := t.Context()
	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
	}
	if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}
	assertResponseStatus(t, resCh, sip.ResponseStatusRinging)

	tp.drainSends()

	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 200) error = %v, want nil", err)
	}

	if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}
	assertResponseStatus(t, resCh, sip.ResponseStatusOK)

	if res := tx.LastResponse(); res.Status() != sip.ResponseStatusOK {
		t.Fatalf("tx.LastResponse().Status() = %v, want %v", res.Status(), sip.ResponseStatusOK)
	}

	tp.drainSends()
	tp.ensureNoSend(t, timings.TimeE()+20*time.Millisecond)

	waitForTransactState(t, tx, sip.TransactionStateTerminated, timings.TimeK()+200*time.Millisecond)
	tp.ensureNoSend(t, 2*timings.TimeE())
}

func TestNonInviteClientTransaction_RoundTripSnapshot(t *testing.T) {
	t.Parallel()

	t1 := 10 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, 2*t1)

	remote := netip.MustParseAddrPort("66.66.66.66:5080")
	local := netip.MustParseAddrPort("22.22.22.22:5071")

	origTP := newStubClientTransport("TCP", "tcp", local, true)
	req := newOutNonInviteReq(t, origTP.Proto(), sip.MagicCookie+".client-noninvite-snapshot", local, remote)

	tx, err := sip.NewNonInviteClientTransaction(req, origTP, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteClientTransaction(req, tp, opts) error = %v, want nil", err)
	}

	call := origTP.waitSend(t, 100*time.Millisecond)
	if call.req.Method() != sip.RequestMethodInfo {
		t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInfo)
	}

	ctx := t.Context()
	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 200) error = %v, want nil", err)
	}

	if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
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
	restored, err := sip.RestoreNonInviteClientTransaction(&snapCopy, restoredTP, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.RestoreNonInviteClientTransaction(snap, tp, opts) error = %v, want nil", err)
	}

	if got, want := restored.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("restored.State() = %q, want %q", got, want)
	}
	if got, want := restored.Key(), tx.Key(); !got.Equal(want) {
		t.Fatalf("restored.Key() = %v, want %v", got, want)
	}
	if res := restored.LastResponse(); res.Status() != sip.ResponseStatusOK {
		t.Fatalf("restored.LastResponse().Status() = %v, want %v", res.Status(), sip.ResponseStatusOK)
	}

	waitForTransactState(t, restored, sip.TransactionStateTerminated, timings.TimeK()+200*time.Millisecond)
	restoredTP.ensureNoSend(t, 100*time.Millisecond)
}

func TestNonInviteClientTransaction_Terminate_FromTrying(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("22.22.22.22:5070")

	tp := newStubClientTransport("UDP", "udp", local, false)
	req := newOutNonInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-trying", local, remote)

	tx, err := sip.NewNonInviteClientTransaction(req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("NewNonInviteClientTransaction error = %v", err)
	}

	tp.waitSend(t, 100*time.Millisecond)

	if got := tx.State(); got != sip.TransactionStateTrying {
		t.Fatalf("State() = %q, want %q", got, sip.TransactionStateTrying)
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

func TestNonInviteClientTransaction_Terminate_FromProceeding(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("22.22.22.22:5070")

	tp := newStubClientTransport("UDP", "udp", local, false)
	req := newOutNonInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-proceeding", local, remote)

	tx, err := sip.NewNonInviteClientTransaction(req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("NewNonInviteClientTransaction error = %v", err)
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

func TestNonInviteClientTransaction_Terminate_FromCompleted(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("22.22.22.22:5070")

	tp := newStubClientTransport("UDP", "udp", local, false)
	req := newOutNonInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-completed", local, remote)

	tx, err := sip.NewNonInviteClientTransaction(req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("NewNonInviteClientTransaction error = %v", err)
	}

	tp.waitSend(t, 100*time.Millisecond)
	ctx := t.Context()

	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
		t.Fatalf("RecvResponse(200) error = %v", err)
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
