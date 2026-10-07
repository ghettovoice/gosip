package sip_test

import (
	"context"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/timeutil"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
)

func TestInviteServerTransaction_AutoTrying(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5060")

		tp := newStubServerTransport(false)

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".auto-trying", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		call := tp.waitSendRes(t)
		if call.res.Status() != sip.ResponseStatusTrying {
			t.Fatalf("unexpected auto response status: got %v, want %v", call.res.Status(), sip.ResponseStatusTrying)
		}

		ctx := t.Context()
		if err := tx.Respond(ctx, sip.ResponseStatusRinging); err != nil {
			t.Fatalf("tx.Respond(ctx, 180, nil) error = %v, want nil", err)
		}

		call = tp.waitSendRes(t)
		if call.res.Status() != sip.ResponseStatusRinging {
			t.Fatalf("unexpected ringing status: got %v, want %v", call.res.Status(), sip.ResponseStatusRinging)
		}

		tp.ensureNoSendRes(t)

		if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
			t.Fatalf("tx.State() = %q, want %q", got, want)
		}
	})
}

func TestInviteServerTransaction_CompletedTimedOut(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig

		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5060")

		tp := newStubServerTransport(false)

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".timed-out", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, tp, sip.ServerTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		ctx := t.Context()
		if err := tx.Respond(ctx, sip.ResponseStatusBusyHere); err != nil {
			t.Fatalf("tx.Respond(ctx, 486, nil) error = %v, want nil", err)
		}

		call := tp.waitSendRes(t)
		if call.res.Status() != sip.ResponseStatusBusyHere {
			t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
		}

		if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
			t.Fatalf("tx.State() = %q, want %q", got, want)
		}

		errCh := make(chan error, 1)
		tx.BindErrorHandler(sip.ErrorHandlerFunc(func(ctx context.Context, err error) {
			select {
			case errCh <- err:
			default:
			}
		}))

		deadline := time.NewTimer(timing.TimeH() + timing.TimeG())
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

		waitForTransactState(t, tx, sip.TransactionStateTerminated, timing.TimeH()+2*timing.TimeG())

		select {
		case err := <-errCh:
			if !errors.Is(err, sip.ErrTransactionTimedOut) {
				t.Fatalf("transport error mismatch: got %v, want %v", err, sip.ErrTransactionTimedOut)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("expected transport error callback")
		}

		tp.ensureNoSendRes(t)
	})
}

func TestInviteServerTransaction_Confirmed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig

		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5060")

		tp := newStubServerTransport(false)

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".confirmed", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, tp, sip.ServerTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		// send 486 final response -> transition to completed
		ctx := t.Context()
		if err := tx.Respond(ctx, sip.ResponseStatusBusyHere); err != nil {
			t.Fatalf("tx.Respond(ctx, 486, nil) error = %v, want nil", err)
		}

		call := tp.waitSendRes(t)
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

		call = tp.waitSendRes(t)
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
			call := tp.waitSendRes(t)
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

		waitForTransactState(t, tx, sip.TransactionStateTerminated, timing.TimeI()+100*time.Millisecond)

		tp.ensureNoSendRes(t)
	})
}

func TestInviteServerTransaction_ConfirmedRelTransp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5060")

		tp := newStubServerTransport(true)

		req := newInInviteReq(t, "TCP", sip.MagicCookie+".confirmed", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		// send 486 final response -> transition to completed
		ctx := t.Context()
		if err := tx.Respond(ctx, sip.ResponseStatusBusyHere); err != nil {
			t.Fatalf("tx.Respond(ctx, 486, nil) error = %v, want nil", err)
		}

		call := tp.waitSendRes(t)
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

		call = tp.waitSendRes(t)
		if call.res.Status() != sip.ResponseStatusBusyHere {
			t.Fatalf("resend response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
		}

		// timer G not started for reliable transport
		tp.ensureNoSendRes(t)

		// receive ACK -> transition to confirmed
		ack := newInAckReq(t, req, tx.LastResponse())
		if err := tx.RecvRequest(ctx, ack); err != nil {
			t.Fatalf("tx.RecvRequest(ctx, ACK) error = %v, want nil", err)
		}

		// For reliable transport timer I fires with 0 delay (goroutine), so the transaction
		// may already be in terminated state by the time we read it. Accept both states.
		waitForTransactState(t, tx, sip.TransactionStateTerminated, 100*time.Millisecond)

		tp.ensureNoSendRes(t)
	})
}

func TestInviteServerTransaction_Accepted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig

		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5060")

		tp := newStubServerTransport(false)

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".accepted", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, tp, sip.ServerTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction(INVITE, tp, opts) error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		// send 200 final response -> transition to accepted
		ctx := t.Context()
		if err := tx.Respond(ctx, sip.ResponseStatusOK); err != nil {
			t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
		}

		call := tp.waitSendRes(t)
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

		tp.ensureNoSendRes(t)

		// resend 2xx from TU
		if err := tx.Respond(ctx, sip.ResponseStatusOK); err != nil {
			t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
		}

		call = tp.waitSendRes(t)
		if call.res.Status() != sip.ResponseStatusOK {
			t.Fatalf("resend response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
		}

		// RFC3261 2xx ACK does not match transaction
		if err := tx.RecvRequest(ctx, newInAckReq(t, req, tx.LastResponse())); err == nil {
			t.Fatal("tx.RecvRequest(ctx, ACK) error = nil, want error")
		}

		waitForTransactState(t, tx, sip.TransactionStateTerminated, timing.TimeL()+100*time.Millisecond)

		tp.ensureNoSendRes(t)
	})
}

func TestInviteServerTransaction_AcceptedRFC2543(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig

		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5060")

		tp := newStubServerTransport(false)

		req := newInInviteReq(t, "UDP", "rfc2543.accepted", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, tp, sip.ServerTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction(INVITE, tp, opts) error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		// send 200 final response -> transition to accepted
		ctx := t.Context()
		if err := tx.Respond(ctx, sip.ResponseStatusOK); err != nil {
			t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
		}

		call := tp.waitSendRes(t)
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

		tp.ensureNoSendRes(t)

		// resend 2xx from TU
		if err := tx.Respond(ctx, sip.ResponseStatusOK); err != nil {
			t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
		}

		call = tp.waitSendRes(t)
		if call.res.Status() != sip.ResponseStatusOK {
			t.Fatalf("resend response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
		}

		ackCh := make(chan *sip.RequestEnvelope, 1)
		tx.BindAckHandler(sip.InboundRequestHandlerFunc(func(ctx context.Context, ack *sip.RequestEnvelope) {
			select {
			case ackCh <- ack:
			default:
			}
		}))

		// RFC3261 2xx ACK must match transaction
		if err := tx.RecvRequest(ctx, newInAckReq(t, req, tx.LastResponse())); err != nil {
			t.Fatalf("tx.RecvRequest(ctx, ACK) error = %v, want nil", err)
		}

		select {
		case ack := <-ackCh:
			if ack.Method() != sip.RequestMethodAck {
				t.Fatalf("ACK method mismatch: got %v, want %v", ack.Method(), sip.RequestMethodAck)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("expected ACK callback")
		}

		waitForTransactState(t, tx, sip.TransactionStateTerminated, timing.TimeL()+100*time.Millisecond)

		tp.ensureNoSendRes(t)
	})
}

func TestInviteServerTransaction_AcceptedTranspErr(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig

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

		tx, err := sip.NewInviteServerTransaction(req, tp, sip.ServerTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction(INVITE, tp, opts) error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		ctx := t.Context()
		if err := tx.Respond(ctx, sip.ResponseStatusOK); err != nil {
			t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
		}

		call := tp.waitSendRes(t)
		if call.res.Status() != sip.ResponseStatusOK {
			t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
		}

		if got, want := tx.State(), sip.TransactionStateAccepted; got != want {
			t.Fatalf("tx.State() = %q, want %q", got, want)
		}

		errCh := make(chan error, 1)
		tx.BindErrorHandler(sip.ErrorHandlerFunc(func(ctx context.Context, err error) {
			select {
			case errCh <- err:
			default:
			}
		}))

		if err := tx.Respond(ctx, sip.ResponseStatusOK); err != nil {
			t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
		}

		select {
		case err := <-errCh:
			if !errors.Is(err, sendErr) {
				t.Fatalf("transport error mismatch: got %v, want %v", err, sendErr)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("expected transport error callback")
		}

		if got, want := tx.State(), sip.TransactionStateAccepted; got != want {
			t.Fatalf("tx.State() = %q, want %q", got, want)
		}

		waitForTransactState(t, tx, sip.TransactionStateTerminated, timing.TimeL()+100*time.Millisecond)

		tp.ensureNoSendRes(t)
	})
}

func TestInviteServerTransaction_RoundTripSnapshot(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig

		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5060")

		origTP := newStubServerTransport(false)
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".snapshot", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, origTP, sip.ServerTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction(INVITE, tp, opts) error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		ctx := t.Context()
		if err := tx.Respond(ctx, sip.ResponseStatusBusyHere); err != nil {
			t.Fatalf("tx.Respond(ctx, 486, nil) error = %v, want nil", err)
		}

		snap := mustServerSnapshot(t, tx)
		if snap == nil {
			t.Fatal("mustServerSnapshot(t, tx) = nil, want snapshot")
		}

		call := origTP.waitSendRes(t)
		if call.res.Status() == sip.ResponseStatusTrying {
			call = origTP.waitSendRes(t)
		}

		if call.res.Status() != sip.ResponseStatusBusyHere {
			t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusBusyHere)
		}

		restoredTP := newStubServerTransport(origTP.reliable)

		restored, err := sip.RestoreInviteServerTransaction(snap, restoredTP, sip.ServerTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.RestoreInviteServerTransaction(snap, tp, opts) error = %v, want nil", err)
		}

		startServerTransaction(t, restored)

		if got, want := restored.State(), sip.TransactionStateCompleted; got != want {
			t.Fatalf("restored.State() = %q, want %q", got, want)
		}

		if got, want := restored.Key(), tx.Key(); got != want {
			t.Fatalf("restored.Key() = %v, want %v", got, want)
		}

		if res := restored.LastResponse(); res.Status() != sip.ResponseStatusBusyHere {
			t.Fatalf("restored.LastResponse().Status() = %v, want %v", res.Status(), sip.ResponseStatusBusyHere)
		}

		retransmit := restoredTP.waitSendRes(t)
		if retransmit.res.Status() != sip.ResponseStatusBusyHere {
			t.Fatalf("timer G resend mismatch: got %v, want %v", retransmit.res.Status(), sip.ResponseStatusBusyHere)
		}

		waitForTransactState(t, restored, sip.TransactionStateTerminated, timing.TimeH()+200*time.Millisecond)
	})
}

func TestInviteServerTransaction_Terminate_FromProceeding(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5060")

		tp := newStubServerTransport(false)
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".terminate-proceeding", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		tp.drainSendRess()

		if got := tx.State(); got != sip.TransactionStateProceeding {
			t.Fatalf("State() = %q, want %q", got, sip.TransactionStateProceeding)
		}

		stateCh := make(chan sip.TransactionState, 1)
		tx.BindStateHandler(sip.TransactionStateHandlerFunc(func(_ context.Context, _, to sip.TransactionState) {
			if to == sip.TransactionStateTerminated {
				stateCh <- to
			}
		}))

		ctx := t.Context()
		if err := tx.Terminate(ctx, errors.New("test error")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		select {
		case <-stateCh:
		case <-time.After(5 * time.Second):
			t.Fatal("BindStateHandler callback timeout")
		}

		if got := tx.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}

		tp.ensureNoSendRes(t)
	})
}

func TestInviteServerTransaction_Terminate_FromAccepted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5060")

		tp := newStubServerTransport(false)
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".terminate-accepted", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		ctx := t.Context()
		if err := tx.Respond(ctx, sip.ResponseStatusOK); err != nil {
			t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
		}

		tp.drainSendRess()

		if got := tx.State(); got != sip.TransactionStateAccepted {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateAccepted)
		}

		if err := tx.Terminate(ctx, errors.New("test error")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		if got := tx.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}

		tp.ensureNoSendRes(t)
	})
}

func TestInviteServerTransaction_Terminate_FromCompleted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5060")

		tp := newStubServerTransport(false)
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".terminate-completed", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		ctx := t.Context()
		if err := tx.Respond(ctx, sip.ResponseStatusBusyHere); err != nil {
			t.Fatalf("tx.Respond(ctx, 486, nil) error = %v, want nil", err)
		}

		tp.drainSendRess()

		if got := tx.State(); got != sip.TransactionStateCompleted {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateCompleted)
		}

		if err := tx.Terminate(ctx, errors.New("test error")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		if got := tx.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}

		tp.ensureNoSendRes(t)
	})
}

func TestInviteServerTransaction_Terminate_FromConfirmed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5060")

		tp := newStubServerTransport(false)
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".terminate-confirmed", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		ctx := t.Context()
		if err := tx.Respond(ctx, sip.ResponseStatusBusyHere); err != nil {
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

		if err := tx.Terminate(ctx, errors.New("test error")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		if got := tx.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}

		tp.ensureNoSendRes(t)
	})
}

func TestInviteServerTransaction_Terminate_Idempotent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5060")

		tp := newStubServerTransport(false)
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".terminate-idempotent", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		tp.drainSendRess()

		ctx := t.Context()

		if err := tx.Terminate(ctx, errors.New("test error")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		if err := tx.Terminate(ctx, errors.New("test error")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		if got := tx.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}
	})
}

func TestInviteServerTransaction_SendResponseBeforeStart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubServerTransport(false)
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".respond-before-start", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}

		res := newInRes(t, req, sip.ResponseStatusTrying)
		if err := tx.SendResponse(ctx, res); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("tx.SendResponse() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}
		if err := tx.Respond(ctx, sip.ResponseStatusRinging); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("tx.Respond() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}
		tp.ensureNoSendRes(t)

		startServerTransaction(t, tx)
		if err := tx.SendResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.SendResponse() error = %v, want nil", err)
		}
		if call := tp.waitSendRes(t); call.res.Status() != sip.ResponseStatusRinging {
			t.Fatalf("sent response status = %v, want %v", call.res.Status(), sip.ResponseStatusRinging)
		}
	})
}

func TestInviteServerTransaction_PreparedRecvWaitsForStart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubServerTransport(false)
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".prepared-recv", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}

		otherReq := newInInviteReq(t, "UDP", sip.MagicCookie+".prepared-other", local, remote)
		if err := tx.RecvRequest(ctx, otherReq); !errors.Is(err, sip.ErrMessageNotMatched) {
			t.Fatalf("tx.RecvRequest(unmatched) error = %v, want %v", err, sip.ErrMessageNotMatched)
		}

		errCh := make(chan error, 1)
		go func() { errCh <- tx.RecvRequest(ctx, req.Clone().(*sip.RequestEnvelope)) }() //nolint:forcetypeassert

		time.Sleep(100 * time.Millisecond)
		select {
		case err := <-errCh:
			t.Fatalf("tx.RecvRequest() = %v before Start, want blocked", err)
		default:
		}

		startServerTransaction(t, tx)
		if err := <-errCh; err != nil {
			t.Fatalf("tx.RecvRequest() error = %v, want nil", err)
		}
	})
}

func TestInviteServerTransaction_RestoreExpiredTimerH(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubServerTransport(false)
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".restore-timer-h", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		if err := tx.SendResponse(ctx, newInRes(t, req, sip.ResponseStatusBusyHere)); err != nil {
			t.Fatalf("tx.SendResponse(486) error = %v, want nil", err)
		}
		tp.waitSendRes(t)
		if got := tx.State(); got != sip.TransactionStateCompleted {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateCompleted)
		}

		snap := mustServerSnapshot(t, tx)
		if snap == nil || snap.TimerG == nil || snap.TimerH == nil {
			t.Fatalf("mustServerSnapshot(t, tx) = %+v, want running timers G and H", snap)
		}

		time.Sleep(timing.TimeH() + 100*time.Millisecond)

		restoredTP := newStubServerTransport(false)
		restored, err := sip.RestoreInviteServerTransaction(snap, restoredTP)
		if err != nil {
			t.Fatalf("sip.RestoreInviteServerTransaction() error = %v, want nil", err)
		}
		if got := restored.State(); got != sip.TransactionStateCompleted {
			t.Fatalf("restored.State() = %q, want %q", got, sip.TransactionStateCompleted)
		}

		time.Sleep(timing.TimeH())
		restoredTP.ensureNoSendRes(t)

		startServerTransaction(t, restored)

		restoredTP.ensureNoSendRes(t)
		waitForTransactState(t, restored, sip.TransactionStateTerminated, 100*time.Millisecond)
	})
}

func TestRestoreServerTransaction_Dispatch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		if _, err := sip.RestoreServerTransaction(nil, newStubServerTransport(false)); err == nil {
			t.Fatal("sip.RestoreServerTransaction(nil, tp) error = nil, want error")
		}

		invReq := newInInviteReq(t, "UDP", sip.MagicCookie+".dispatch-inv", local, remote)
		invTx, err := sip.NewInviteServerTransaction(invReq, newStubServerTransport(false))
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		if err := invTx.Start(t.Context()); err != nil {
			t.Fatalf("invTx.Start() error = %v, want nil", err)
		}

		restored, err := sip.RestoreServerTransaction(mustServerSnapshot(t, invTx), newStubServerTransport(false))
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction(invite snap) error = %v, want nil", err)
		}
		if _, ok := restored.(*sip.InviteServerTransaction); !ok {
			t.Fatalf("sip.RestoreServerTransaction(invite snap) = %T, want *sip.InviteServerTransaction", restored)
		}

		niReq := newInNonInviteReq(t, "UDP", sip.MagicCookie+".dispatch-ni", local, remote)
		niTx, err := sip.NewNonInviteServerTransaction(niReq, newStubServerTransport(false))
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		if err := niTx.Start(t.Context()); err != nil {
			t.Fatalf("niTx.Start() error = %v, want nil", err)
		}

		restored, err = sip.RestoreServerTransaction(mustServerSnapshot(t, niTx), newStubServerTransport(false))
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction(non-invite snap) error = %v, want nil", err)
		}
		if _, ok := restored.(*sip.NonInviteServerTransaction); !ok {
			t.Fatalf("sip.RestoreServerTransaction(non-invite snap) = %T, want *sip.NonInviteServerTransaction", restored)
		}

		clnSnap := mustServerSnapshot(t, invTx)
		clnSnap.Type = sip.TransactionTypeClientInvite
		if _, err := sip.RestoreServerTransaction(clnSnap, newStubServerTransport(false)); err == nil {
			t.Fatal("sip.RestoreServerTransaction(client snap) error = nil, want error")
		}
	})
}

func TestRestoreInviteServerTransaction_InvalidSnapshot(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubServerTransport(false)
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".restore-invalid-srv", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		base := mustServerSnapshot(t, tx)
		if base == nil {
			t.Fatal("mustServerSnapshot(t, tx) = nil, want snapshot")
		}

		testCases := []struct {
			name   string
			mutate func(snap *sip.ServerTransactionSnapshot)
		}{
			{name: "type mismatch", mutate: func(snap *sip.ServerTransactionSnapshot) {
				snap.Type = sip.TransactionTypeServerNonInvite
			}},
			{name: "invalid state", mutate: func(snap *sip.ServerTransactionSnapshot) {
				snap.State = 0
			}},
			{name: "wrong state for type", mutate: func(snap *sip.ServerTransactionSnapshot) {
				snap.State = sip.TransactionStateCalling
			}},
			{name: "key mismatch", mutate: func(snap *sip.ServerTransactionSnapshot) {
				snap.Key.Branch = "z9hG4bK.other"
			}},
			{name: "foreign active timer", mutate: func(snap *sip.ServerTransactionSnapshot) {
				snap.TimerJ = snap.Timer1xx
			}},
			{name: "malformed stopped foreign timer", mutate: func(snap *sip.ServerTransactionSnapshot) {
				snap.TimerJ = &timeutil.TimerSnapshot{State: timeutil.TimerStateStopped}
			}},
			{name: "active timer in wrong state", mutate: func(snap *sip.ServerTransactionSnapshot) {
				snap.State = sip.TransactionStateAccepted
				snap.LastResponse = newInRes(t, snap.Request, sip.ResponseStatusOK)
			}},
		}

		for _, c := range testCases {
			snap := *base
			c.mutate(&snap)

			restoredTP := newStubServerTransport(false)
			if _, err := sip.RestoreInviteServerTransaction(&snap, restoredTP); err == nil {
				t.Fatalf("sip.RestoreInviteServerTransaction(%s) error = nil, want error", c.name)
			}
			restoredTP.ensureNoSendRes(t)
		}
	})
}

func TestInviteServerTransaction_ProvisionalSuppressesTimer100(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubServerTransport(false)
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".provisional-1xx", local, remote)

		tx, err := sip.NewInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}

		recvCh := make(chan error, 1)
		go func() { recvCh <- tx.RecvRequest(ctx, req.Clone().(*sip.RequestEnvelope)) }() //nolint:forcetypeassert

		time.Sleep(100 * time.Millisecond)
		select {
		case err := <-recvCh:
			t.Fatalf("tx.RecvRequest() = %v before Start, want blocked", err)
		default:
		}

		startServerTransaction(t, tx)
		if err := <-recvCh; err != nil {
			t.Fatalf("tx.RecvRequest() error = %v, want nil", err)
		}

		if err := tx.SendResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.SendResponse(180) error = %v, want nil", err)
		}
		if call := tp.waitSendRes(t); call.res.Status() != sip.ResponseStatusRinging {
			t.Fatalf("sent response status = %v, want %v", call.res.Status(), sip.ResponseStatusRinging)
		}
		if snap := mustServerSnapshot(t, tx); snap.Timer1xx != nil {
			t.Fatalf("snapshot Timer1xx = %+v after provisional response, want nil", snap.Timer1xx)
		}

		time.Sleep(10 * time.Second)
		tp.ensureNoSendRes(t)
	})
}
