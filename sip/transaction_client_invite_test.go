package sip_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/timeutil"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

func TestInviteClientTransaction_Start(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".client-start", local, remote)

		tx, err := sip.NewInviteClientTransaction(req, tp)
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

		if err := tx.Start(t.Context()); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("tx.Start() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}

		if err := tx.Terminate(t.Context(), errors.New("test cleanup")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}
	})
}

func TestInviteClientTransaction_SnapshotReturnsLastStableState(t *testing.T) {
	t.Parallel()

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")
	tp := newStubClientTransport(false)
	req := newOutInviteReq(t, "UDP", sip.MagicCookie+".snapshot-stable", local, remote)

	tx, err := sip.NewInviteClientTransaction(req, tp)
	if err != nil {
		t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
	}
	startClientTransaction(t, tx)
	tp.waitSendReq(t)

	entered := make(chan struct{})
	release := make(chan struct{})
	tx.BindResponseHandler(sip.InboundResponseHandlerFunc(func(context.Context, *sip.ResponseEnvelope) {
		close(entered)
		<-release
	}))

	res := newInRes(t, req, sip.ResponseStatusRinging)
	errCh := make(chan error, 1)
	go func() { errCh <- tx.RecvResponse(t.Context(), res) }()
	<-entered

	during := mustClientSnapshot(t, tx)
	if during.State != sip.TransactionStateCalling || during.LastResponse != nil ||
		during.TimerA == nil || during.TimerB == nil {
		t.Fatalf("snapshot during transition = %+v, want last stable Calling snapshot", during)
	}

	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("tx.RecvResponse(180) error = %v, want nil", err)
	}

	after := mustClientSnapshot(t, tx)
	if after.State != sip.TransactionStateProceeding || after.LastResponse == nil ||
		after.TimerA != nil || after.TimerB != nil {
		t.Fatalf("snapshot after transition = %+v, want stable Proceeding snapshot", after)
	}

	during.State = sip.TransactionStateTerminated
	during.TimerA.Duration = 0
	unchanged := mustClientSnapshot(t, tx)
	if unchanged.State != sip.TransactionStateProceeding {
		t.Fatalf("snapshot state after caller mutation = %v, want %v", unchanged.State, sip.TransactionStateProceeding)
	}
}

func TestInviteClientTransaction_SnapshotUpdatesAfterTimerReset(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".snapshot-reset", local, remote)

		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)
		tp.waitSendReq(t)

		before := mustClientSnapshot(t, tx)
		if before.TimerA == nil {
			t.Fatal("initial snapshot TimerA = nil, want running timer")
		}
		time.Sleep(before.TimerA.Duration + time.Millisecond)
		tp.waitSendReq(t)

		after := mustClientSnapshot(t, tx)
		if after.TimerA == nil || after.TimerA.Duration != 2*before.TimerA.Duration {
			t.Fatalf("snapshot TimerA after reset = %+v, want duration %v", after.TimerA, 2*before.TimerA.Duration)
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

		tx, err := sip.NewInviteClientTransaction(req, tp, sip.ClientTransactionOptions{Timing: timing})
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

		tx, err := sip.NewInviteClientTransaction(req, tp)
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

		tx, err := sip.NewInviteClientTransaction(req, tp, sip.ClientTransactionOptions{Timing: timing})
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

		tx, err := sip.NewInviteClientTransaction(req, origTP)
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

		snap := mustClientSnapshot(t, tx)
		if snap == nil {
			t.Fatal("mustClientSnapshot(t, tx) = nil, want snapshot")
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

		restored, err := sip.RestoreInviteClientTransaction(&snapCopy, restoredTP)
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

		startClientTransaction(t, restored)

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

		tx, err := sip.NewInviteClientTransaction(req, tp)
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

		tx, err := sip.NewInviteClientTransaction(req, tp)
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

		tx, err := sip.NewInviteClientTransaction(req, tp)
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

		tx, err := sip.NewInviteClientTransaction(req, tp)
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

		tx, err := sip.NewInviteClientTransaction(req, tp)
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

		tx, err := sip.NewInviteClientTransaction(req, tp)
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
			snap := mustClientSnapshot(t, tx)
			if snap.TimerD != nil && snap.TimerD.Duration != 0 {
				t.Fatalf("Timer D = %+v, want duration 0", snap.TimerD)
			}
		} else if state != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q or %q", state, sip.TransactionStateCompleted, sip.TransactionStateTerminated)
		}

		waitForTransactState(t, tx, sip.TransactionStateTerminated, time.Second)
	})
}

func TestInviteClientTransaction_PreparedRecvWaitsForStart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".prepared-recv", local, remote)

		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		tp.ensureNoSendReq(t)
		if _, err := tx.Snapshot(); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("tx.Snapshot() before Start error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}

		otherReq := newOutInviteReq(t, "UDP", sip.MagicCookie+".prepared-other", local, remote)
		other := newInRes(t, otherReq, sip.ResponseStatusRinging)
		if err := tx.RecvResponse(ctx, other); !errors.Is(err, sip.ErrMessageNotMatched) {
			t.Fatalf("tx.RecvResponse(unmatched) error = %v, want %v", err, sip.ErrMessageNotMatched)
		}

		res := newInRes(t, req, sip.ResponseStatusRinging)
		errCh := make(chan error, 1)
		go func() { errCh <- tx.RecvResponse(ctx, res) }()

		time.Sleep(100 * time.Millisecond)
		select {
		case err := <-errCh:
			t.Fatalf("tx.RecvResponse() = %v before Start, want blocked", err)
		default:
		}

		startClientTransaction(t, tx)
		if call := tp.waitSendReq(t); call.req.Method() != sip.RequestMethodInvite {
			t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
		}

		if err := <-errCh; err != nil {
			t.Fatalf("tx.RecvResponse() error = %v, want nil", err)
		}
		waitForTransactState(t, tx, sip.TransactionStateProceeding, 100*time.Millisecond)
	})
}

func TestInviteClientTransaction_PreparedRecvCanceled(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".prepared-cancel", local, remote)

		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		ctx, cancel := context.WithCancel(t.Context())
		errCh := make(chan error, 1)
		go func() { errCh <- tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)) }()

		time.Sleep(100 * time.Millisecond)
		cancel()

		if err := <-errCh; !errors.Is(err, context.Canceled) {
			t.Fatalf("tx.RecvResponse() error = %v, want %v", err, context.Canceled)
		}
	})
}

func TestInviteClientTransaction_PreparedRecvReleasedByTerminate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".prepared-term", local, remote)

		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		errCh := make(chan error, 1)
		go func() { errCh <- tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)) }()

		time.Sleep(100 * time.Millisecond)
		if err := tx.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		if err := <-errCh; !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("tx.RecvResponse() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}
	})
}

func TestInviteClientTransaction_StartAfterTerminate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".start-after-term", local, remote)

		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		if err := tx.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}
		if got := tx.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}

		if err := tx.Start(ctx); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("tx.Start() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}

		if err := tx.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("second tx.Terminate() error = %v, want nil", err)
		}
	})
}

func TestInviteClientTransaction_RestoreExpiredTimerB(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".restore-timer-b", local, remote)

		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)
		tp.waitSendReq(t)

		snap := mustClientSnapshot(t, tx)
		if snap == nil || snap.TimerA == nil || snap.TimerB == nil {
			t.Fatalf("mustClientSnapshot(t, tx) = %+v, want running timers A and B", snap)
		}

		time.Sleep(timing.TimeB() + 100*time.Millisecond)

		restoredTP := newStubClientTransport(false)
		restored, err := sip.RestoreInviteClientTransaction(snap, restoredTP)
		if err != nil {
			t.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
		}
		if got := restored.State(); got != sip.TransactionStateCalling {
			t.Fatalf("restored.State() = %q, want %q", got, sip.TransactionStateCalling)
		}

		time.Sleep(timing.TimeB())
		restoredTP.ensureNoSendReq(t)
		if got := restored.State(); got != sip.TransactionStateCalling {
			t.Fatalf("restored.State() = %q, want %q", got, sip.TransactionStateCalling)
		}

		startClientTransaction(t, restored)

		restoredTP.ensureNoSendReq(t)
		waitForTransactState(t, restored, sip.TransactionStateTerminated, 100*time.Millisecond)
	})
}

func TestInviteClientTransaction_RestoreTerminatedSnapshot(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".restore-terminated", local, remote)

		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)
		tp.waitSendReq(t)

		if err := tx.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		snap := mustClientSnapshot(t, tx)
		if snap == nil || snap.State != sip.TransactionStateTerminated {
			t.Fatalf("mustClientSnapshot(t, tx) = %+v, want terminated state", snap)
		}

		restored, err := sip.RestoreInviteClientTransaction(snap, newStubClientTransport(false))
		if err != nil {
			t.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
		}
		if got := restored.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("restored.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}
		if restored.Started() {
			t.Fatal("restored.Started() = true, want false")
		}
		if got := mustClientSnapshot(t, restored).State; got != sip.TransactionStateTerminated {
			t.Fatalf("restored.Snapshot().State = %q, want %q", got, sip.TransactionStateTerminated)
		}

		if err := restored.Start(ctx); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("restored.Start() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}
	})
}

func TestRestoreClientTransaction_Dispatch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		if _, err := sip.RestoreClientTransaction(nil, newStubClientTransport(false)); err == nil {
			t.Fatal("sip.RestoreClientTransaction(nil, tp) error = nil, want error")
		}

		invReq := newOutInviteReq(t, "UDP", sip.MagicCookie+".dispatch-inv", local, remote)
		invTx, err := sip.NewInviteClientTransaction(invReq, newStubClientTransport(false))
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, invTx)

		restored, err := sip.RestoreClientTransaction(mustClientSnapshot(t, invTx), newStubClientTransport(false))
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction(invite snap) error = %v, want nil", err)
		}
		if _, ok := restored.(*sip.InviteClientTransaction); !ok {
			t.Fatalf("sip.RestoreClientTransaction(invite snap) = %T, want *sip.InviteClientTransaction", restored)
		}

		niReq := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".dispatch-ni", local, remote)
		niTx, err := sip.NewNonInviteClientTransaction(niReq, newStubClientTransport(false))
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, niTx)

		restored, err = sip.RestoreClientTransaction(mustClientSnapshot(t, niTx), newStubClientTransport(false))
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction(non-invite snap) error = %v, want nil", err)
		}
		if _, ok := restored.(*sip.NonInviteClientTransaction); !ok {
			t.Fatalf("sip.RestoreClientTransaction(non-invite snap) = %T, want *sip.NonInviteClientTransaction", restored)
		}

		srvSnap := mustClientSnapshot(t, invTx)
		srvSnap.Type = sip.TransactionTypeServerInvite
		if _, err := sip.RestoreClientTransaction(srvSnap, newStubClientTransport(false)); err == nil {
			t.Fatal("sip.RestoreClientTransaction(server snap) error = nil, want error")
		}
	})
}

func TestRestoreInviteClientTransaction_InvalidSnapshot(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".restore-invalid", local, remote)

		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)
		tp.waitSendReq(t)

		base := mustClientSnapshot(t, tx)
		if base == nil {
			t.Fatal("mustClientSnapshot(t, tx) = nil, want snapshot")
		}

		testCases := []struct {
			name   string
			mutate func(snap *sip.ClientTransactionSnapshot)
		}{
			{name: "type mismatch", mutate: func(snap *sip.ClientTransactionSnapshot) {
				snap.Type = sip.TransactionTypeClientNonInvite
			}},
			{name: "invalid state", mutate: func(snap *sip.ClientTransactionSnapshot) {
				snap.State = 0
			}},
			{name: "wrong state for type", mutate: func(snap *sip.ClientTransactionSnapshot) {
				snap.State = sip.TransactionStateConfirmed
			}},
			{name: "key mismatch", mutate: func(snap *sip.ClientTransactionSnapshot) {
				snap.Key.Branch = "z9hG4bK.other"
			}},
			{name: "proceeding without response", mutate: func(snap *sip.ClientTransactionSnapshot) {
				snap.State = sip.TransactionStateProceeding
			}},
			{name: "unexpected last response", mutate: func(snap *sip.ClientTransactionSnapshot) {
				snap.LastResponse = newInRes(t, snap.Request, sip.ResponseStatusRinging)
			}},
			{name: "foreign active timer", mutate: func(snap *sip.ClientTransactionSnapshot) {
				snap.TimerF = snap.TimerA
			}},
			{name: "invalid timer", mutate: func(snap *sip.ClientTransactionSnapshot) {
				snap.TimerA = &timeutil.TimerSnapshot{State: timeutil.TimerStateInvalid}
			}},
			{name: "active timer in wrong state", mutate: func(snap *sip.ClientTransactionSnapshot) {
				snap.State = sip.TransactionStateProceeding
				snap.LastResponse = newInRes(t, snap.Request, sip.ResponseStatusRinging)
			}},
			{name: "malformed stopped foreign timer", mutate: func(snap *sip.ClientTransactionSnapshot) {
				snap.TimerF = &timeutil.TimerSnapshot{State: timeutil.TimerStateStopped}
			}},
		}

		for _, c := range testCases {
			snap := *base
			c.mutate(&snap)

			restoredTP := newStubClientTransport(false)
			if _, err := sip.RestoreInviteClientTransaction(&snap, restoredTP); err == nil {
				t.Fatalf("sip.RestoreInviteClientTransaction(%s) error = nil, want error", c.name)
			}
			restoredTP.ensureNoSendReq(t)
		}
	})
}

func TestInviteClientTransaction_ConcurrentStartTerminate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".concurrent", local, remote)

		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() { _ = tx.Start(ctx) })
			wg.Go(func() { _ = tx.Terminate(ctx, errors.New("test cleanup")) })
		}
		wg.Wait()

		if got := tx.State(); got != sip.TransactionStateTerminated && got != sip.TransactionStateCalling {
			t.Fatalf("tx.State() = %q, want %q or %q", got, sip.TransactionStateCalling, sip.TransactionStateTerminated)
		}

		if err := tx.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}
		waitForTransactState(t, tx, sip.TransactionStateTerminated, 100*time.Millisecond)
	})
}

func TestInviteClientTransaction_TerminateDuringInitialSend(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".term-during-send", local, remote)

		entered := make(chan struct{})
		release := make(chan struct{})
		tp.sendReqHook = func(sendReqCall, int) error {
			close(entered)
			<-release
			return nil
		}

		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		startCh := make(chan error, 1)
		go func() { startCh <- tx.Start(ctx) }()

		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("initial send not reached")
		}

		if err := tx.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}
		close(release)

		if err := <-startCh; err != nil {
			t.Fatalf("tx.Start() error = %v, want nil", err)
		}
		waitForTransactState(t, tx, sip.TransactionStateTerminated, 100*time.Millisecond)

		snap := mustClientSnapshot(t, tx)
		if snap.TimerA != nil || snap.TimerB != nil || snap.TimerD != nil || snap.TimerM != nil {
			t.Fatalf("terminated transaction timers = %+v, want all nil", snap)
		}
		tp.drainSendReqs()
		tp.ensureNoSendReq(t)
	})
}

func TestInviteClientTransaction_RestoreStartWithPendingResponse(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".restore-pending", local, remote)

		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)
		tp.waitSendReq(t)

		snap := mustClientSnapshot(t, tx)
		if snap == nil || snap.TimerA == nil || snap.TimerB == nil {
			t.Fatalf("mustClientSnapshot(t, tx) = %+v, want running timers A and B", snap)
		}

		restoredTP := newStubClientTransport(false)
		restored, err := sip.RestoreInviteClientTransaction(snap, restoredTP)
		if err != nil {
			t.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
		}

		recvCh := make(chan error, 1)
		go func() { recvCh <- restored.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)) }()

		time.Sleep(100 * time.Millisecond)
		select {
		case err := <-recvCh:
			t.Fatalf("restored.RecvResponse() = %v before Start, want blocked", err)
		default:
		}

		startClientTransaction(t, restored)
		restoredTP.ensureNoSendReq(t)

		if err := <-recvCh; err != nil {
			t.Fatalf("restored.RecvResponse() error = %v, want nil", err)
		}
		waitForTransactState(t, restored, sip.TransactionStateProceeding, 100*time.Millisecond)
	})
}

func TestRestoreInviteClientTransaction_ReceivedViaResponse(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".restore-via", local, remote)

		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)
		tp.waitSendReq(t)

		res := newInRes(t, req, sip.ResponseStatusRinging)
		if err := tx.RecvResponse(t.Context(), res); err != nil {
			t.Fatalf("tx.RecvResponse(180) error = %v, want nil", err)
		}
		waitForTransactState(t, tx, sip.TransactionStateProceeding, 100*time.Millisecond)

		base := mustClientSnapshot(t, tx)
		if base == nil || base.LastResponse == nil {
			t.Fatalf("mustClientSnapshot(t, tx) = %+v, want proceeding snapshot with last response", base)
		}

		snap := *base
		snap.LastResponse.WithMessage(func(r *sip.Response) {
			if via, ok := r.Headers.FirstVia(); ok {
				via.Params.Set("received", "10.0.0.5")
				via.Params.Set("rport", "5060")
			}
		})

		if _, err := sip.RestoreInviteClientTransaction(&snap, newStubClientTransport(false)); err != nil {
			t.Fatalf("sip.RestoreInviteClientTransaction(received/rport via) error = %v, want nil", err)
		}

		testCases := []struct {
			name   string
			mutate func(r *sip.Response)
		}{
			{name: "wrong branch", mutate: func(r *sip.Response) {
				if via, ok := r.Headers.FirstVia(); ok {
					via.Params.Set("branch", sip.MagicCookie+".other")
				}
			}},
			{name: "wrong call-id", mutate: func(r *sip.Response) {
				r.Headers.Set(header.CallID("other-call@bob.voip.com"))
			}},
			{name: "wrong cseq", mutate: func(r *sip.Response) {
				if cseq, ok := r.Headers.CSeq(); ok {
					r.Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum + 1, Method: cseq.Method})
				}
			}},
		}

		for _, c := range testCases {
			snap := *base
			snap.LastResponse = base.LastResponse.Clone().(*sip.ResponseEnvelope) //nolint:forcetypeassert
			snap.LastResponse.WithMessage(c.mutate)

			if _, err := sip.RestoreInviteClientTransaction(&snap, newStubClientTransport(false)); err == nil {
				t.Fatalf("sip.RestoreInviteClientTransaction(%s) error = nil, want error", c.name)
			}
		}
	})
}
