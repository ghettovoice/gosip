package sip_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/sip"
)

func TestNonInviteServerTransaction_LifecycleUnrelTransp(t *testing.T) {
	t.Parallel()

	t1 := 5 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, 2*t1)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("33.33.33.33:5070")

	tp := newStubTransportExt("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5070), false)
	req := newInNonInviteReq(t, tp.Proto(), sip.MagicCookie+".unreliable", local, remote)

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

	tp := newStubTransportExt("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5070), false)
	req := newInNonInviteReq(t, tp.Proto(), sip.MagicCookie+".mismatch-callid", local, remote)

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

	tp := newStubTransportExt("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5070), false)
	req := newInNonInviteReq(t, tp.Proto(), sip.MagicCookie+".mismatch-via", local, remote)

	tx, err := sip.NewNonInviteServerTransaction(t.Context(), req, tp, &sip.ServerTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
	}

	res, err := req.NewResponse(sip.ResponseStatusRinging, nil)
	if err != nil {
		t.Fatalf("req.NewResponse(180, nil) error = %v, want nil", err)
	}
	res.AccessMessage(func(r *sip.Response) {
		if via, ok := r.Headers.FirstVia(); ok && via != nil {
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

	tp := newStubTransportExt("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5070), false)
	req := newInNonInviteReq(t, tp.Proto(), sip.MagicCookie+".mismatch-cseq", local, remote)

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

	tp := newStubTransportExt("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5070), false)
	sendErr := errors.New("transport test error")
	tp.setSendResHook(func(_ sendResCall, idx int) error {
		if idx >= 1 {
			return errtrace.Wrap(sendErr)
		}
		return nil
	})

	req := newInNonInviteReq(t, tp.Proto(), sip.MagicCookie+".transp-err", local, remote)

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

	origTP := newStubTransportExt("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5070), false)
	req := newInNonInviteReq(t, origTP.Proto(), sip.MagicCookie+".snapshot", local, remote)

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

	restoredTP := newStubTransportExt(origTP.Proto(), origTP.Network(), origTP.LocalAddr(), origTP.Reliable())
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

	tp := newStubTransportExt("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5070), false)
	req := newInNonInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-trying", local, remote)

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

	tp := newStubTransportExt("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5070), false)
	req := newInNonInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-proceeding", local, remote)

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

	tp := newStubTransportExt("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5070), false)
	req := newInNonInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-completed", local, remote)

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

	tp := newStubTransportExt("UDP", "udp", netip.AddrPortFrom(netip.IPv4Unspecified(), 5070), false)
	req := newInNonInviteReq(t, tp.Proto(), sip.MagicCookie+".terminate-idempotent", local, remote)

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
