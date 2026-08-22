package sip_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
)

func TestInviteClientTransaction_Start(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".client-start", local, remote)

		tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		select {
		case call := <-tp.sendReqCalls:
			t.Fatalf("unexpected initial send with method %v", call.req.Method())
		default:
		}

		if err := tx.Start(t.Context()); err != nil {
			t.Fatalf("tx.Start() error = %v, want nil", err)
		}

		if call := tp.waitSendReq(t); call.req.Method() != sip.RequestMethodInvite {
			t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
		}

		if err := tx.Start(t.Context()); !errors.Is(err, sip.ErrActionNotAllowed) {
			t.Fatalf("tx.Start() error = %v, want %v", err, sip.ErrActionNotAllowed)
		}

		if err := tx.Terminate(t.Context(), errors.New("test cleanup")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}
	})
}

func TestInviteClientTransaction_Accepted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig

		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		// Use an unreliable transport to keep timer A enabled and avoid retransmit races in tests.
		tp := newStubClientTransport(false)

		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".client-accepted", local, remote)

		tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)

		call := tp.waitSendReq(t)
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

		resCh := make(chan *sip.ResponseEnvelope, 3)
		tx.BindResponseHandler(sip.InboundResponseHandlerFunc(func(_ context.Context, res *sip.ResponseEnvelope) {
			resCh <- res
		}))

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
		}

		// FiringQueued mode may defer the actual state transition if timer A goroutine
		// holds the firing lock at the moment RecvResponse returns, so use waitForTransactState.
		waitForTransactState(t, tx, sip.TransactionStateProceeding, 100*time.Millisecond)

		assertResponseStatus(t, resCh, sip.ResponseStatusRinging)
		tp.drainSendReqs()

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
		tp.drainSendReqs()

		// additional 2xx keeps transaction accepted and delivers to TU
		secondOK := ok.Clone().(*sip.ResponseEnvelope) //nolint:forcetypeassert
		if err := tx.RecvResponse(ctx, secondOK); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 200 repeat) error = %v, want nil", err)
		}

		assertResponseStatus(t, resCh, sip.ResponseStatusOK)

		waitForTransactState(t, tx, sip.TransactionStateTerminated, timing.TimeM()+100*time.Millisecond)

		tp.ensureNoSendReq(t)
	})
}

func TestInviteClientTransaction_Rejected(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)

		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".client-rejected", local, remote)

		tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)

		call := tp.waitSendReq(t)
		if call.req.Method() != sip.RequestMethodInvite {
			t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
		}

		ctx := t.Context()

		resCh := make(chan *sip.ResponseEnvelope, 2)
		tx.BindResponseHandler(sip.InboundResponseHandlerFunc(func(_ context.Context, res *sip.ResponseEnvelope) {
			resCh <- res
		}))

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
		}

		assertResponseStatus(t, resCh, sip.ResponseStatusRinging)
		tp.drainSendReqs()

		decline := newInRes(t, req, sip.ResponseStatusDecline)
		if err := tx.RecvResponse(ctx, decline); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 603) error = %v, want nil", err)
		}

		assertResponseStatus(t, resCh, sip.ResponseStatusDecline)

		if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
			t.Fatalf("tx.State() = %q, want %q", got, want)
		}

		ackCall := tp.waitSendReq(t)
		if ackCall.req.Method() != sip.RequestMethodAck {
			t.Fatalf("sent %v, want ACK", ackCall.req.Method())
		}

		// Retransmitted final response should trigger another ACK send.
		secondDecline := decline.Clone().(*sip.ResponseEnvelope) //nolint:forcetypeassert
		if err := tx.RecvResponse(ctx, secondDecline); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 603 retransmit) error = %v, want nil", err)
		}

		retransAck := tp.waitSendReq(t)
		if retransAck.req.Method() != sip.RequestMethodAck {
			t.Fatalf("sent %v, want ACK retransmit", retransAck.req.Method())
		}

		waitForTransactState(t, tx, sip.TransactionStateTerminated, sip.TimeD+200*time.Millisecond)
		tp.ensureNoSendReq(t)
	})
}

func TestInviteClientTransaction_Timeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig

		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)

		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".client-timeout", local, remote)

		tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)

		call := tp.waitSendReq(t)
		if call.req.Method() != sip.RequestMethodInvite {
			t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
		}

		resCh := make(chan *sip.ResponseEnvelope, 1)
		tx.BindResponseHandler(sip.InboundResponseHandlerFunc(func(_ context.Context, res *sip.ResponseEnvelope) {
			resCh <- res
		}))

		timeout := timing.TimeB() + 200*time.Millisecond
		waitForTransactState(t, tx, sip.TransactionStateTerminated, timeout)

		select {
		case res := <-resCh:
			t.Fatalf("unexpected response %v", res.Status())
		default:
		}

		if res := tx.LastResponse(); res != nil {
			t.Fatalf("tx.LastResponse() = %v, want nil", res.Status())
		}

		tp.drainSendReqs()
		tp.ensureNoSendReq(t)
	})
}

func TestInviteClientTransaction_RoundTripSnapshot(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		origTP := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".client-snapshot", local, remote)

		tx, err := sip.NewInviteClientTransaction(t.Context(), req, origTP)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)

		initial := origTP.waitSendReq(t)
		if initial.req.Method() != sip.RequestMethodInvite {
			t.Fatalf("initial send method = %q, want %q", initial.req.Method(), sip.RequestMethodInvite)
		}

		origTP.drainSendReqs()

		ctx := t.Context()

		decline := newInRes(t, req, sip.ResponseStatusDecline)
		if err := tx.RecvResponse(ctx, decline); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 603) error = %v, want nil", err)
		}

		ackCall := origTP.waitSendReq(t)
		if ackCall.req.Method() != sip.RequestMethodAck {
			t.Fatalf("sent %v, want ACK", ackCall.req.Method())
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

		restoredTP := newStubClientTransport(false)

		restored, err := sip.RestoreInviteClientTransaction(t.Context(), &snapCopy, restoredTP)
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

		retransmit := decline.Clone().(*sip.ResponseEnvelope) //nolint:forcetypeassert
		if err := restored.RecvResponse(ctx, retransmit); err != nil {
			t.Fatalf("restored.RecvResponse(ctx, 603) error = %v, want nil", err)
		}

		ack := restoredTP.waitSendReq(t)
		if ack.req.Method() != sip.RequestMethodAck {
			t.Fatalf("sent %v, want ACK retransmit", ack.req.Method())
		}

		waitForTransactState(t, restored, sip.TransactionStateTerminated, sip.TimeD+200*time.Millisecond)
	})
}

func TestInviteClientTransaction_Terminate_FromCalling(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".terminate-calling", local, remote)

		tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)

		tp.waitSendReq(t)

		if got := tx.State(); got != sip.TransactionStateCalling {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateCalling)
		}

		stateCh := make(chan sip.TransactionState, 1)
		tx.BindStateHandler(sip.TransactionStateHandlerFunc(func(_ context.Context, _, to sip.TransactionState) {
			if to == sip.TransactionStateTerminated {
				stateCh <- to
			}
		}))

		ctx := t.Context()
		if err := tx.Terminate(ctx, errors.New("test error")); err != nil {
			t.Fatalf("tx.Terminate() error = %v", err)
		}

		select {
		case <-stateCh:
		case <-time.After(5 * time.Second):
			t.Fatal("BindStateHandler callback wait timeout")
		}

		if got := tx.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}

		tp.ensureNoSendReq(t)
	})
}

func TestInviteClientTransaction_Terminate_FromProceeding(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".terminate-proceeding", local, remote)

		tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)

		tp.waitSendReq(t)
		ctx := t.Context()

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.RecvResponse(180) error = %v, want nil", err)
		}

		tp.drainSendReqs()

		if got := tx.State(); got != sip.TransactionStateProceeding {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateProceeding)
		}

		if err := tx.Terminate(ctx, errors.New("test error")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		if got := tx.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}

		tp.ensureNoSendReq(t)
	})
}

func TestInviteClientTransaction_Terminate_FromAccepted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".terminate-accepted", local, remote)

		tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction(INVITE, tp, opts) error = %v, want nil", err)
		}
		startClientTransaction(t, tx)

		tp.waitSendReq(t)
		ctx := t.Context()

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
			t.Fatalf("tx.RecvResponse(200) error = %v, want nil", err)
		}

		tp.drainSendReqs()

		if got := tx.State(); got != sip.TransactionStateAccepted {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateAccepted)
		}

		if err := tx.Terminate(ctx, errors.New("test error")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		if got := tx.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}

		tp.ensureNoSendReq(t)
	})
}

func TestInviteClientTransaction_Terminate_FromCompleted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".terminate-completed", local, remote)

		tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)

		tp.waitSendReq(t)
		ctx := t.Context()

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusDecline)); err != nil {
			t.Fatalf("tx.RecvResponse(603) error = %v, want nil", err)
		}

		tp.waitSendReq(t)
		tp.drainSendReqs()

		if got := tx.State(); got != sip.TransactionStateCompleted {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateCompleted)
		}

		if err := tx.Terminate(ctx, errors.New("test error")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		if got := tx.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}

		tp.ensureNoSendReq(t)
	})
}

func TestInviteClientTransaction_Terminate_Idempotent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".terminate-idempotent", local, remote)

		tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)

		tp.waitSendReq(t)
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

func TestInviteClientTransaction_ReliableTimerD(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(true)
		req := newOutInviteReq(t, "TCP", sip.MagicCookie+".client-reliable-timer-d", local, remote)

		tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)

		tp.waitSendReq(t)
		if err := tx.RecvResponse(t.Context(), newInRes(t, req, sip.ResponseStatusDecline)); err != nil {
			t.Fatalf("tx.RecvResponse(603) error = %v, want nil", err)
		}

		tp.waitSendReq(t)
		if state := tx.State(); state == sip.TransactionStateCompleted {
			snap := tx.Snapshot()
			if snap.TimerD != nil && snap.TimerD.Duration != 0 {
				t.Fatalf("Timer D = %+v, want duration 0", snap.TimerD)
			}
		} else if state != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q or %q", state, sip.TransactionStateCompleted, sip.TransactionStateTerminated)
		}

		waitForTransactState(t, tx, sip.TransactionStateTerminated, time.Second)
	})
}
