package sip_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/timeutil"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

func TestInviteClientTransaction_BindErrorHandler_Context(t *testing.T) {
	t.Parallel()

	localAddr := netip.MustParseAddrPort("11.11.11.11:5070")
	remoteAddr := netip.MustParseAddrPort("55.55.55.55:5060")
	tp := newPrxStubTransp()
	const sendErr errors.Error = "request send failed"
	tp.sendReqHook = func(sendReqCall, int) error {
		return errors.Wrap(sendErr)
	}
	req := newOutInviteReq(t, "UDP", sip.MagicCookie+".error-context", localAddr, remoteAddr)
	tx, err := sip.NewInviteClientTransaction(req, tp)
	if err != nil {
		t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
	}
	registerTxCleanup(t, tx)

	errorCtxs := make(chan context.Context, 1)
	startCtx := context.WithValue(t.Context(), testCtxKey{}, "error")
	if err := tx.Start(startCtx); !errors.Is(err, sendErr) {
		t.Fatalf("tx.Start() error = %v, want %v", err, sendErr)
	}
	tx.BindErrorHandler(sip.ErrorHandlerFunc(func(ctx context.Context, _ error) {
		errorCtxs <- ctx
	}))
	assertPropagationCtx(t, testutil.WaitResult(t, errorCtxs, "client transport error context"), tx, tp, "error")
}

func TestInviteClientTransaction_BindResponseHandler_ReplayContext(t *testing.T) {
	t.Parallel()

	localAddr := netip.MustParseAddrPort("11.11.11.11:5070")
	remoteAddr := netip.MustParseAddrPort("55.55.55.55:5060")
	tp := newPrxStubTransp()
	req := newOutInviteReq(t, "UDP", sip.MagicCookie+".response-replay-context", localAddr, remoteAddr)
	tx, err := sip.NewInviteClientTransaction(req, tp)
	if err != nil {
		t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
	}
	registerTxCleanup(t, tx)
	if err := tx.Start(t.Context()); err != nil {
		t.Fatalf("tx.Start() error = %v, want nil", err)
	}
	_ = tp.waitSendReq(t)

	responseCtx := context.WithValue(t.Context(), testCtxKey{}, "response-replay")
	if err := tx.RecvResponse(responseCtx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
		t.Fatalf("tx.RecvResponse(180) error = %v, want nil", err)
	}
	responseCtxs := make(chan context.Context, 1)
	tx.BindResponseHandler(sip.InboundResponseHandlerFunc(func(ctx context.Context, _ *sip.ResponseEnvelope) {
		responseCtxs <- ctx
	}))
	assertPropagationCtx(t,
		testutil.WaitResult(t, responseCtxs, "response replay callback context"),
		tx, tp, "response-replay",
	)

	if err := tx.Terminate(t.Context(), nil); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}
}

func TestInviteClientTransaction_Snapshot_DuringStart(t *testing.T) {
	t.Parallel()

	local := netip.MustParseAddrPort("0.0.0.0:5060")
	remote := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubClnTransp(false)
	tx, err := sip.NewInviteClientTransaction(
		newOutInviteReq(t, "UDP", sip.MagicCookie+".snapshot-starting", local, remote),
		tp,
	)
	if err != nil {
		t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
	}
	if _, err := tx.Snapshot(); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
		t.Fatalf("tx.Snapshot() before Start error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
	}

	var snapshotErr error
	tx.BindStartHandler(sip.TransactionStartHandlerFunc(
		func(context.Context) { _, snapshotErr = tx.Snapshot() },
	))
	if err := tx.Start(t.Context()); err != nil {
		t.Fatalf("tx.Start() error = %v, want nil", err)
	}
	if !errors.Is(snapshotErr, sip.ErrTransactionActionNotAllowed) {
		t.Fatalf("tx.Snapshot() during Start error = %v, want %v", snapshotErr, sip.ErrTransactionActionNotAllowed)
	}
	tp.waitSendReq(t)
	mustClnTxSnapshot(t, tx)
}

func TestInviteClientTransaction_Start_ReentrantCallback(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		tp := newStubClnTransp(false)
		tx, err := sip.NewInviteClientTransaction(
			newOutInviteReq(t,
				"UDP", sip.MagicCookie+".lc-reentrant",
				netip.MustParseAddrPort("11.11.11.11:5070"),
				netip.MustParseAddrPort("55.55.55.55:5060"),
			),
			tp,
		)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		var starts atomic.Int32
		tx.BindStartHandler(sip.TransactionStartHandlerFunc(
			func(hdlrCtx context.Context) {
				_ = tx.Started()
				_ = tx.State()
				if err := tx.Start(hdlrCtx); err != nil {
					starts.Add(1)
				}
			},
		))

		if err := tx.Start(ctx); err != nil {
			t.Fatalf("tx.Start() error = %v, want nil", err)
		}
		if starts.Load() == 0 {
			t.Fatal("reentrant tx.Start() never called from start callback")
		}
	})
}

func TestInviteClientTransaction_Start_ConcurrentTerminate(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	laddr := netip.MustParseAddrPort("11.11.11.11:5070")
	raddr := netip.MustParseAddrPort("55.55.55.55:5060")

	for i := range 20 {
		tp := newStubClnTransp(false)
		tx, err := sip.NewInviteClientTransaction(
			newOutInviteReq(t, "UDP", sip.MagicCookie+".lc-race"+strconv.Itoa(i), laddr, raddr),
			tp,
		)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		var wg sync.WaitGroup
		wg.Go(func() { _ = tx.Start(ctx) })
		wg.Go(func() { _ = tx.Terminate(ctx, errors.ErrorWrap("race")) })
		wg.Wait()

		if got := tx.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("iter %d: tx.State() = %v, want %v", i, got, sip.TransactionStateTerminated)
		}
	}
}

func TestInviteClientTransaction_Start(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
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

func TestInviteClientTransaction_Snapshot_ReturnsLastStableState(t *testing.T) {
	t.Parallel()

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")
	tp := newStubClnTransp(false)
	req := newOutInviteReq(t, "UDP", sip.MagicCookie+".snapshot-stable", local, remote)

	tx, err := sip.NewInviteClientTransaction(req, tp)
	if err != nil {
		t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
	}

	startClnTx(t, tx)
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

	during := mustClnTxSnapshot(t, tx)
	if during.State != sip.TransactionStateCalling || during.LastResponse != nil ||
		during.TimerA == nil || during.TimerB == nil {
		t.Fatalf("snapshot during transition = %+v, want last stable Calling snapshot", during)
	}

	close(release)

	if err := <-errCh; err != nil {
		t.Fatalf("tx.RecvResponse(180) error = %v, want nil", err)
	}

	after := mustClnTxSnapshot(t, tx)
	if after.State != sip.TransactionStateProceeding || after.LastResponse == nil ||
		after.TimerA != nil || after.TimerB != nil {
		t.Fatalf("snapshot after transition = %+v, want stable Proceeding snapshot", after)
	}

	during.State = sip.TransactionStateTerminated
	during.TimerA.Duration = 0
	unchanged := mustClnTxSnapshot(t, tx)
	if unchanged.State != sip.TransactionStateProceeding {
		t.Fatalf("snapshot state after caller mutation = %v, want %v", unchanged.State, sip.TransactionStateProceeding)
	}
}

func TestInviteClientTransaction_Snapshot_UpdatesAfterTimerReset(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".snapshot-reset", local, remote)

		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		startClnTx(t, tx)
		tp.waitSendReq(t)

		before := mustClnTxSnapshot(t, tx)
		if before.TimerA == nil {
			t.Fatal("initial snapshot TimerA = nil, want running timer")
		}

		time.Sleep(before.TimerA.Duration + time.Millisecond)
		tp.waitSendReq(t)

		after := mustClnTxSnapshot(t, tx)
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
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".client-accepted", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		startClnTx(t, tx)

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

		resCh := make(chan *sip.ResponseEnvelope, 3)
		tx.BindResponseHandler(sip.InboundResponseHandlerFunc(func(_ context.Context, res *sip.ResponseEnvelope) {
			resCh <- res
		}))

		ctx := t.Context()

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
		}

		// FiringQueued mode may defer the actual state transition if timer A goroutine
		// holds the firing lock at the moment RecvResponse returns, so use waitForTxState.
		waitForTxState(t, tx, sip.TransactionStateProceeding, 100*time.Millisecond)
		assertResStatus(t, resCh, sip.ResponseStatusRinging)
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

		assertResStatus(t, resCh, sip.ResponseStatusOK)
		tp.drainSendReqs()

		// additional 2xx keeps transaction accepted and delivers to TU
		secondOK := ok.Clone().(*sip.ResponseEnvelope)
		if err := tx.RecvResponse(ctx, secondOK); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 200 repeat) error = %v, want nil", err)
		}

		assertResStatus(t, resCh, sip.ResponseStatusOK)
		waitForTxState(t, tx, sip.TransactionStateTerminated, timing.TimeM()+100*time.Millisecond)
		tp.ensureNoSendReq(t)
	})
}

func TestInviteClientTransaction_Rejected(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".client-rejected", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)

		call := tp.waitSendReq(t)
		if call.req.Method() != sip.RequestMethodInvite {
			t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
		}

		resCh := make(chan *sip.ResponseEnvelope, 2)
		tx.BindResponseHandler(sip.InboundResponseHandlerFunc(func(_ context.Context, res *sip.ResponseEnvelope) {
			resCh <- res
		}))

		ctx := t.Context()

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
		}

		assertResStatus(t, resCh, sip.ResponseStatusRinging)
		tp.drainSendReqs()

		decline := newInRes(t, req, sip.ResponseStatusDecline)
		if err := tx.RecvResponse(ctx, decline); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 603) error = %v, want nil", err)
		}

		assertResStatus(t, resCh, sip.ResponseStatusDecline)

		if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
			t.Fatalf("tx.State() = %q, want %q", got, want)
		}

		ackCall := tp.waitSendReq(t)
		if ackCall.req.Method() != sip.RequestMethodAck {
			t.Fatalf("sent %v, want ACK", ackCall.req.Method())
		}

		// Retransmitted final response should trigger another ACK send.
		secondDecline := decline.Clone().(*sip.ResponseEnvelope)
		if err := tx.RecvResponse(ctx, secondDecline); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 603 retransmit) error = %v, want nil", err)
		}

		retransAck := tp.waitSendReq(t)
		if retransAck.req.Method() != sip.RequestMethodAck {
			t.Fatalf("sent %v, want ACK retransmit", retransAck.req.Method())
		}

		waitForTxState(t, tx, sip.TransactionStateTerminated, sip.TimeD+200*time.Millisecond)
		tp.ensureNoSendReq(t)
	})
}

func TestInviteClientTransaction_Timeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".client-timeout", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)

		call := tp.waitSendReq(t)
		if call.req.Method() != sip.RequestMethodInvite {
			t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
		}

		resCh := make(chan *sip.ResponseEnvelope, 1)
		tx.BindResponseHandler(sip.InboundResponseHandlerFunc(func(_ context.Context, res *sip.ResponseEnvelope) {
			resCh <- res
		}))

		timeout := timing.TimeB() + 200*time.Millisecond
		waitForTxState(t, tx, sip.TransactionStateTerminated, timeout)

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
		origTP := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".client-snapshot", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, origTP)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)

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

		snap := mustClnTxSnapshot(t, tx)
		if snap == nil {
			t.Fatal("mustClnTxSnapshot(t, tx) = nil, want snapshot")
		}

		data, err := json.Marshal(snap)
		if err != nil {
			t.Fatalf("json.Marshal(snapshot) error = %v, want nil", err)
		}

		var snapCopy sip.ClientTransactionSnapshot
		if err := json.Unmarshal(data, &snapCopy); err != nil {
			t.Fatalf("json.Unmarshal(snapshot) error = %v, want nil", err)
		}

		restoredTP := newStubClnTransp(false)
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

		startClnTx(t, restored)

		retransmit := decline.Clone().(*sip.ResponseEnvelope)
		if err := restored.RecvResponse(ctx, retransmit); err != nil {
			t.Fatalf("restored.RecvResponse(ctx, 603) error = %v, want nil", err)
		}

		ack := restoredTP.waitSendReq(t)
		if ack.req.Method() != sip.RequestMethodAck {
			t.Fatalf("sent %v, want ACK retransmit", ack.req.Method())
		}

		waitForTxState(t, restored, sip.TransactionStateTerminated, sip.TimeD+200*time.Millisecond)
	})
}

func TestInviteClientTransaction_Terminate_FromCalling(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".terminate-calling", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)
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
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".terminate-proceeding", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)
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
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".terminate-accepted", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction(INVITE, tp, opts) error = %v, want nil", err)
		}
		startClnTx(t, tx)
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
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".terminate-completed", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)
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
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".terminate-idempotent", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)
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
		tp := newStubClnTransp(true)
		req := newOutInviteReq(t, "TCP", sip.MagicCookie+".client-reliable-timer-d", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)
		tp.waitSendReq(t)

		if err := tx.RecvResponse(t.Context(), newInRes(t, req, sip.ResponseStatusDecline)); err != nil {
			t.Fatalf("tx.RecvResponse(603) error = %v, want nil", err)
		}

		tp.waitSendReq(t)

		if state := tx.State(); state == sip.TransactionStateCompleted {
			snap := mustClnTxSnapshot(t, tx)
			if snap.TimerD != nil && snap.TimerD.Duration != 0 {
				t.Fatalf("Timer D = %+v, want duration 0", snap.TimerD)
			}
		} else if state != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q or %q", state, sip.TransactionStateCompleted, sip.TransactionStateTerminated)
		}

		waitForTxState(t, tx, sip.TransactionStateTerminated, time.Second)
	})
}

func TestInviteClientTransaction_RecvResponse_WaitsForStart(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
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

		startClnTx(t, tx)

		if call := tp.waitSendReq(t); call.req.Method() != sip.RequestMethodInvite {
			t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
		}
		if err := <-errCh; err != nil {
			t.Fatalf("tx.RecvResponse() error = %v, want nil", err)
		}

		waitForTxState(t, tx, sip.TransactionStateProceeding, 100*time.Millisecond)
	})
}

func TestInviteClientTransaction_RecvResponse_CanceledBeforeStart(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
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

func TestInviteClientTransaction_RecvResponse_ReleasedByTerminateBeforeStart(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
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

func TestInviteClientTransaction_Start_AfterTerminate(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
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

func TestRestoreInviteClientTransaction_ExpiredTimerB(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".restore-timer-b", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		startClnTx(t, tx)
		tp.waitSendReq(t)

		snap := mustClnTxSnapshot(t, tx)
		if snap == nil || snap.TimerA == nil || snap.TimerB == nil {
			t.Fatalf("mustClnTxSnapshot(t, tx) = %+v, want running timers A and B", snap)
		}

		time.Sleep(timing.TimeB() + 100*time.Millisecond)

		restoredTP := newStubClnTransp(false)
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

		startClnTx(t, restored)
		restoredTP.ensureNoSendReq(t)
		waitForTxState(t, restored, sip.TransactionStateTerminated, 100*time.Millisecond)
	})
}

func TestRestoreInviteClientTransaction_TerminatedSnapshot(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".restore-terminated", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)
		tp.waitSendReq(t)

		if err := tx.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		snap := mustClnTxSnapshot(t, tx)
		if snap == nil || snap.State != sip.TransactionStateTerminated {
			t.Fatalf("mustClnTxSnapshot(t, tx) = %+v, want terminated state", snap)
		}

		restored, err := sip.RestoreInviteClientTransaction(snap, newStubClnTransp(false))
		if err != nil {
			t.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
		}
		if got := restored.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("restored.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}
		if restored.Started() {
			t.Fatal("restored.Started() = true, want false")
		}
		if got := mustClnTxSnapshot(t, restored).State; got != sip.TransactionStateTerminated {
			t.Fatalf("restored.Snapshot().State = %q, want %q", got, sip.TransactionStateTerminated)
		}
		if err := restored.Start(ctx); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("restored.Start() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}
	})
}

func TestRestoreInviteClientTransaction_InvalidSnapshot(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".restore-invalid", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)
		tp.waitSendReq(t)

		base := mustClnTxSnapshot(t, tx)
		if base == nil {
			t.Fatal("mustClnTxSnapshot(t, tx) = nil, want snapshot")
		}

		tests := []struct {
			name   string
			mutate func(snap *sip.ClientTransactionSnapshot)
		}{
			{
				"type mismatch",
				func(snap *sip.ClientTransactionSnapshot) {
					snap.Type = sip.TransactionTypeClientNonInvite
				},
			},
			{
				"invalid state",
				func(snap *sip.ClientTransactionSnapshot) {
					snap.State = 0
				},
			},
			{
				"wrong state for type",
				func(snap *sip.ClientTransactionSnapshot) {
					snap.State = sip.TransactionStateConfirmed
				},
			},
			{
				"key mismatch",
				func(snap *sip.ClientTransactionSnapshot) {
					snap.Key.Branch = "z9hG4bK.other"
				},
			},
			{
				"proceeding without response",
				func(snap *sip.ClientTransactionSnapshot) {
					snap.State = sip.TransactionStateProceeding
				},
			},
			{
				"unexpected last response",
				func(snap *sip.ClientTransactionSnapshot) {
					snap.LastResponse = newInRes(t, snap.Request, sip.ResponseStatusRinging)
				},
			},
			{
				"foreign active timer",
				func(snap *sip.ClientTransactionSnapshot) {
					snap.TimerF = snap.TimerA
				},
			},
			{
				"invalid timer",
				func(snap *sip.ClientTransactionSnapshot) {
					snap.TimerA = &timeutil.TimerSnapshot{State: timeutil.TimerStateInvalid}
				},
			},
			{
				"active timer in wrong state",
				func(snap *sip.ClientTransactionSnapshot) {
					snap.State = sip.TransactionStateProceeding
					snap.LastResponse = newInRes(t, snap.Request, sip.ResponseStatusRinging)
				},
			},
			{
				"malformed stopped foreign timer",
				func(snap *sip.ClientTransactionSnapshot) {
					snap.TimerF = &timeutil.TimerSnapshot{State: timeutil.TimerStateStopped}
				},
			},
		}

		for _, tt := range tests {
			snap := *base
			tt.mutate(&snap)

			restoredTP := newStubClnTransp(false)
			if _, err := sip.RestoreInviteClientTransaction(&snap, restoredTP); err == nil {
				t.Fatalf("sip.RestoreInviteClientTransaction(%s) error = nil, want error", tt.name)
			}
			restoredTP.ensureNoSendReq(t)
		}
	})
}

func TestInviteClientTransaction_Start_ConcurrentStartsAndTerminate(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
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

		waitForTxState(t, tx, sip.TransactionStateTerminated, 100*time.Millisecond)
	})
}

func TestInviteClientTransaction_Terminate_DuringInitialSend(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
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

		waitForTxState(t, tx, sip.TransactionStateTerminated, 100*time.Millisecond)

		snap := mustClnTxSnapshot(t, tx)
		if snap.TimerA != nil || snap.TimerB != nil || snap.TimerD != nil || snap.TimerM != nil {
			t.Fatalf("terminated transaction timers = %+v, want all nil", snap)
		}

		tp.drainSendReqs()
		tp.ensureNoSendReq(t)
	})
}

func TestRestoreInviteClientTransaction_StartWithPendingResponse(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".restore-pending", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)
		tp.waitSendReq(t)

		snap := mustClnTxSnapshot(t, tx)
		if snap == nil || snap.TimerA == nil || snap.TimerB == nil {
			t.Fatalf("mustClnTxSnapshot(t, tx) = %+v, want running timers A and B", snap)
		}

		restoredTP := newStubClnTransp(false)
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

		startClnTx(t, restored)
		restoredTP.ensureNoSendReq(t)

		if err := <-recvCh; err != nil {
			t.Fatalf("restored.RecvResponse() error = %v, want nil", err)
		}

		waitForTxState(t, restored, sip.TransactionStateProceeding, 100*time.Millisecond)
	})
}

func TestRestoreInviteClientTransaction_ReceivedViaResponse(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".restore-via", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)
		tp.waitSendReq(t)

		res := newInRes(t, req, sip.ResponseStatusRinging)
		if err := tx.RecvResponse(t.Context(), res); err != nil {
			t.Fatalf("tx.RecvResponse(180) error = %v, want nil", err)
		}

		waitForTxState(t, tx, sip.TransactionStateProceeding, 100*time.Millisecond)

		base := mustClnTxSnapshot(t, tx)
		if base == nil || base.LastResponse == nil {
			t.Fatalf("mustClnTxSnapshot(t, tx) = %+v, want proceeding snapshot with last response", base)
		}

		snap := *base
		snap.LastResponse.WithMessage(func(r *sip.Response) {
			if via, ok := r.Headers.FirstVia(); ok {
				via.Params.Set("received", "10.0.0.5")
				via.Params.Set("rport", "5060")
			}
		})

		if _, err := sip.RestoreInviteClientTransaction(&snap, newStubClnTransp(false)); err != nil {
			t.Fatalf("sip.RestoreInviteClientTransaction(received/rport via) error = %v, want nil", err)
		}

		tests := []struct {
			name   string
			mutate func(r *sip.Response)
		}{
			{
				"wrong branch",
				func(r *sip.Response) {
					if via, ok := r.Headers.FirstVia(); ok {
						via.Params.Set("branch", sip.MagicCookie+".other")
					}
				},
			},
			{
				"wrong call-id",
				func(r *sip.Response) {
					r.Headers.Set(header.CallID("other-call@bob.voip.com"))
				},
			},
			{
				"wrong cseq",
				func(r *sip.Response) {
					if cseq, ok := r.Headers.CSeq(); ok {
						r.Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum + 1, Method: cseq.Method})
					}
				},
			},
		}

		for _, tt := range tests {
			snap := *base
			snap.LastResponse = base.LastResponse.Clone().(*sip.ResponseEnvelope)
			snap.LastResponse.WithMessage(tt.mutate)

			if _, err := sip.RestoreInviteClientTransaction(&snap, newStubClnTransp(false)); err == nil {
				t.Fatalf("sip.RestoreInviteClientTransaction(%s) error = nil, want error", tt.name)
			}
		}
	})
}

func TestInviteClientTransaction_SnapshotNotifications(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".snap-ntf", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		rec := &clnTxSnapRecorder{}
		// reentrant Snapshot/Started calls from a handler must not deadlock
		var snapErr error
		var startedFlag bool
		tx.BindSnapshotHandler(sip.ClientTransactionSnapshotHandlerFunc(func(hdlrCtx context.Context, snap *sip.ClientTransactionSnapshot) {
			rec.handler()(hdlrCtx, snap)
			_, snapErr = tx.Snapshot()
			startedFlag = tx.Started()
		}))
		if got := rec.len(); got != 0 {
			t.Fatalf("snapshot notifications before Start = %d, want 0", got)
		}

		startClnTx(t, tx)
		tp.waitSendReq(t)

		if got := rec.len(); got != 1 {
			t.Fatalf("snapshot notifications after Start = %d, want 1", got)
		}
		if snapErr != nil {
			t.Fatalf("reentrant tx.Snapshot() error = %v, want nil", snapErr)
		}
		if !startedFlag {
			t.Fatal("reentrant tx.Started() = false, want true")
		}
		if ev := rec.at(0); ev.State != sip.TransactionStateCalling ||
			ev.TimerA == nil || ev.TimerB == nil || ev.LastResponse != nil {
			t.Fatalf("first snapshot = %+v, want Calling with timers A/B", ev)
		}

		ctx := t.Context()
		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
		}
		if got := rec.len(); got != 2 {
			t.Fatalf("snapshot notifications after 180 = %d, want 2", got)
		}
		if ev := rec.at(1); ev.State != sip.TransactionStateProceeding ||
			ev.LastResponse == nil || ev.LastResponse.Status() != sip.ResponseStatusRinging ||
			ev.TimerA != nil || ev.TimerB != nil {
			t.Fatalf("second snapshot = %+v, want Proceeding with 180 and stopped timers A/B", ev)
		}

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusBusyHere)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 486) error = %v, want nil", err)
		}
		if got := rec.len(); got != 3 {
			t.Fatalf("snapshot notifications after 486 = %d, want 3", got)
		}
		if ev := rec.at(2); ev.State != sip.TransactionStateCompleted ||
			ev.LastResponse == nil || ev.LastResponse.Status() != sip.ResponseStatusBusyHere ||
			ev.TimerD == nil {
			t.Fatalf("third snapshot = %+v, want Completed with 486 and timer D", ev)
		}

		waitForTxState(t, tx, sip.TransactionStateTerminated, sip.TimeD+200*time.Millisecond)

		if got := rec.len(); got != 4 {
			t.Fatalf("snapshot notifications after timer D = %d, want 4", got)
		}
		if ev := rec.at(3); ev.State != sip.TransactionStateTerminated ||
			ev.TimerA != nil || ev.TimerB != nil || ev.TimerD != nil || ev.TimerM != nil {
			t.Fatalf("terminal snapshot = %+v, want Terminated with all timers cleaned", ev)
		}

		// every published snapshot must be restorable
		for i := range rec.len() {
			if _, err := sip.RestoreInviteClientTransaction(rec.at(i), newStubClnTransp(false)); err != nil {
				t.Fatalf("sip.RestoreInviteClientTransaction(snapshot[%d]) error = %v, want nil", i, err)
			}
		}
	})
}

func TestInviteClientTransaction_SnapshotNotification_TimerA(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".snap-tmr-a", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)
		tp.waitSendReq(t)

		rec := &clnTxSnapRecorder{}
		tx.BindSnapshotHandler(rec.handler())

		time.Sleep(timing.TimeA() + 10*time.Millisecond)
		tp.waitSendReq(t)

		if got := rec.len(); got != 1 {
			t.Fatalf("snapshot notifications after timer A = %d, want 1", got)
		}

		ev := rec.at(0)
		if ev.State != sip.TransactionStateCalling {
			t.Fatalf("snapshot state after timer A = %q, want %q", ev.State, sip.TransactionStateCalling)
		}
		if ev.TimerA == nil || ev.TimerA.State != timeutil.TimerStateRunning ||
			ev.TimerA.Duration != 2*timing.TimeA() {
			t.Fatalf("snapshot TimerA = %+v, want running with duration %v", ev.TimerA, 2*timing.TimeA())
		}
	})
}

func TestInviteClientTransaction_SnapshotHandler_CloneIsolation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".snap-iso", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		// observer 1 mutates every notification it receives: state, timers,
		// and nested request/response messages
		tx.BindSnapshotHandler(sip.ClientTransactionSnapshotHandlerFunc(func(_ context.Context, snap *sip.ClientTransactionSnapshot) {
			snap.State = sip.TransactionStateTerminated
			if snap.TimerA != nil {
				snap.TimerA.State = timeutil.TimerStateStopped
				snap.TimerA.Duration = 0
			}
			snap.Request.WithMessage(func(r *sip.Request) {
				r.Method = sip.RequestMethodAck
				r.Headers.Delete("Call-ID")
			})
			if snap.LastResponse != nil {
				snap.LastResponse.WithMessage(func(r *sip.Response) {
					r.Status = sip.ResponseStatusOK
				})
			}
		}))

		second := &clnTxSnapRecorder{}
		tx.BindSnapshotHandler(second.handler())

		startClnTx(t, tx)
		tp.waitSendReq(t)

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
		}
		if got := second.len(); got != 2 {
			t.Fatalf("observer 2 notifications = %d, want 2", got)
		}
		if ev := second.at(0); ev.State != sip.TransactionStateCalling ||
			ev.TimerA == nil || ev.TimerA.State != timeutil.TimerStateRunning ||
			ev.Request.Method() != sip.RequestMethodInvite {
			t.Fatalf("observer 2 snapshot[0] = %+v, want Calling with running timer A and INVITE request", ev)
		}
		if ev := second.at(1); ev.State != sip.TransactionStateProceeding ||
			ev.LastResponse == nil || ev.LastResponse.Status() != sip.ResponseStatusRinging {
			t.Fatalf("observer 2 snapshot[1] = %+v, want Proceeding with 180", ev)
		}

		// the stored stable snapshot must not be affected either
		stable := mustClnTxSnapshot(t, tx)
		if stable.State != sip.TransactionStateProceeding ||
			stable.LastResponse == nil || stable.LastResponse.Status() != sip.ResponseStatusRinging ||
			stable.Request.Method() != sip.RequestMethodInvite {
			t.Fatalf("stored snapshot = %+v, want Proceeding with 180 and INVITE request", stable)
		}
	})
}

func TestInviteClientTransaction_SnapshotHandler_Unbind(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".snap-unbind", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		// nil handler returns a no-op unbind
		tx.BindSnapshotHandler(nil)()

		first := &clnTxSnapRecorder{}
		unbindFirst := tx.BindSnapshotHandler(first.handler())

		startClnTx(t, tx)
		tp.waitSendReq(t)

		// late binding receives future publications only (no replay)
		late := &clnTxSnapRecorder{}
		unbindLate := tx.BindSnapshotHandler(late.handler())
		if got := late.len(); got != 0 {
			t.Fatalf("late-bound notifications = %d, want 0", got)
		}

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
		}
		if got := first.len(); got != 2 {
			t.Fatalf("observer 1 notifications = %d, want 2", got)
		}
		if got := late.len(); got != 1 {
			t.Fatalf("late-bound notifications = %d, want 1", got)
		}

		// unbind is idempotent
		unbindLate()
		unbindLate()

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusBusyHere)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 486) error = %v, want nil", err)
		}
		if got := late.len(); got != 1 {
			t.Fatalf("unbound notifications = %d, want 1", got)
		}
		if got := first.len(); got != 3 {
			t.Fatalf("observer 1 notifications = %d, want 3", got)
		}

		unbindFirst()

		if err := tx.Terminate(ctx, errors.ErrorWrap("test cleanup")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}
		if got := first.len(); got != 3 {
			t.Fatalf("observer 1 notifications after unbind = %d, want 3", got)
		}
	})
}

func TestInviteClientTransaction_SnapshotNotification_FIFO(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".snap-fifo", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		first := &clnTxSnapRecorder{}
		second := &clnTxSnapRecorder{}

		var terminateOnce sync.Once
		tx.BindSnapshotHandler(sip.ClientTransactionSnapshotHandlerFunc(func(hdlrCtx context.Context, snap *sip.ClientTransactionSnapshot) {
			first.handler()(hdlrCtx, snap)
			if snap.State == sip.TransactionStateProceeding {
				// reentrant terminate queues a terminal publication behind the
				// in-flight notification, so observer 2 still sees Proceeding first
				terminateOnce.Do(func() {
					if err := tx.Terminate(hdlrCtx, errors.ErrorWrap("terminate from observer")); err != nil {
						t.Errorf("tx.Terminate() in handler error = %v, want nil", err)
					}
				})
			}
		}))
		tx.BindSnapshotHandler(second.handler())

		startClnTx(t, tx)
		tp.waitSendReq(t)

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
		}

		want := []sip.TransactionState{
			sip.TransactionStateCalling,
			sip.TransactionStateProceeding,
			sip.TransactionStateTerminated,
		}
		if got := first.states(); !slices.Equal(got, want) {
			t.Fatalf("observer 1 states = %v, want %v", got, want)
		}
		if got := second.states(); !slices.Equal(got, want) {
			t.Fatalf("observer 2 states = %v, want %v", got, want)
		}
	})
}

func TestInviteClientTransaction_SnapshotNotification_QueuedWhileDraining(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".snap-queue", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		rec := &clnTxSnapRecorder{}
		entered := make(chan struct{})
		release := make(chan struct{})
		var blockOnce sync.Once
		tx.BindSnapshotHandler(sip.ClientTransactionSnapshotHandlerFunc(func(hdlrCtx context.Context, snap *sip.ClientTransactionSnapshot) {
			blockOnce.Do(func() {
				close(entered)
				<-release
			})
			rec.handler()(hdlrCtx, snap)
		}))

		startCh := make(chan error, 1)
		go func() { startCh <- tx.Start(ctx) }()

		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("first snapshot handler not reached")
		}

		// a concurrent update publishes while the drain is blocked: the
		// notification is queued, not delivered concurrently
		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
		}
		if got := rec.len(); got != 0 {
			t.Fatalf("delivered notifications while callback blocked = %d, want 0", got)
		}

		close(release)

		if err := <-startCh; err != nil {
			t.Fatalf("tx.Start() error = %v, want nil", err)
		}

		if got := rec.len(); got != 2 {
			t.Fatalf("snapshot notifications after release = %d, want 2", got)
		}
		if ev := rec.at(0); ev.State != sip.TransactionStateCalling {
			t.Fatalf("snapshot[0].State = %q, want %q", ev.State, sip.TransactionStateCalling)
		}
		if ev := rec.at(1); ev.State != sip.TransactionStateProceeding {
			t.Fatalf("snapshot[1].State = %q, want %q", ev.State, sip.TransactionStateProceeding)
		}
	})
}

func TestInviteClientTransaction_SnapshotNotification_CoalescesDuringOperation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".snap-coalesce", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		rec := &clnTxSnapRecorder{}
		tx.BindSnapshotHandler(rec.handler())

		entered := make(chan struct{})
		release := make(chan struct{})
		secondRes := make(chan struct{})
		var calls atomic.Int32
		tx.BindResponseHandler(sip.InboundResponseHandlerFunc(func(context.Context, *sip.ResponseEnvelope) {
			if calls.Add(1) == 1 {
				close(entered)
				<-release
			} else {
				close(secondRes)
			}
		}))

		startClnTx(t, tx)
		tp.waitSendReq(t)
		if got := rec.len(); got != 1 {
			t.Fatalf("snapshot notifications after Start = %d, want 1", got)
		}

		errCh := make(chan error, 2)
		go func() { errCh <- tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)) }()

		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("response handler not reached")
		}

		// concurrent response while the first response handler is paused: the
		// pending trigger must not produce a partial publication
		go func() { errCh <- tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusBusyHere)) }()

		time.Sleep(50 * time.Millisecond)

		if got := rec.len(); got != 1 {
			t.Fatalf("snapshot notifications during paused response handler = %d, want 1", got)
		}
		if snap := mustClnTxSnapshot(t, tx); snap.State != sip.TransactionStateCalling ||
			snap.LastResponse != nil || snap.TimerA == nil || snap.TimerB == nil {
			t.Fatalf("stable snapshot during paused response handler = %+v, want Calling with timers A/B", snap)
		}

		close(release)

		select {
		case <-secondRes:
		case <-time.After(5 * time.Second):
			t.Fatal("queued response not processed")
		}
		for range 2 {
			if err := <-errCh; err != nil {
				t.Fatalf("tx.RecvResponse() error = %v, want nil", err)
			}
		}

		// the overlapping operations coalesce into a single completed publication
		if got := rec.len(); got != 2 {
			t.Fatalf("snapshot notifications after release = %d, want 2", got)
		}
		if ev := rec.at(1); ev.State != sip.TransactionStateCompleted ||
			ev.LastResponse == nil || ev.LastResponse.Status() != sip.ResponseStatusBusyHere {
			t.Fatalf("coalesced snapshot = %+v, want Completed with 486", ev)
		}
	})
}

func TestInviteClientTransaction_SnapshotNotification_TerminateInResponseCallback(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".snap-term", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		rec := &clnTxSnapRecorder{}
		tx.BindSnapshotHandler(rec.handler())

		var terminateOnce sync.Once
		tx.BindResponseHandler(sip.InboundResponseHandlerFunc(func(hdlrCtx context.Context, _ *sip.ResponseEnvelope) {
			terminateOnce.Do(func() {
				if err := tx.Terminate(hdlrCtx, errors.ErrorWrap("terminate from response handler")); err != nil {
					t.Errorf("tx.Terminate() in response handler error = %v, want nil", err)
				}
			})
		}))

		startClnTx(t, tx)
		tp.waitSendReq(t)

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
		}

		// the reentrant terminate must not leak an intermediate proceeding publication
		want := []sip.TransactionState{sip.TransactionStateCalling, sip.TransactionStateTerminated}
		if got := rec.states(); !slices.Equal(got, want) {
			t.Fatalf("snapshot states = %v, want %v", got, want)
		}
		if ev := rec.at(1); ev.TimerA != nil || ev.TimerB != nil || ev.TimerD != nil || ev.TimerM != nil {
			t.Fatalf("terminal snapshot = %+v, want all timers cleaned", ev)
		}
	})
}

func TestInviteClientTransaction_SnapshotNotification_RestoredExpiredTimer(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".snap-expired", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		startClnTx(t, tx)
		tp.waitSendReq(t)

		ctx := t.Context()
		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusBusyHere)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 486) error = %v, want nil", err)
		}

		snap := mustClnTxSnapshot(t, tx)
		if snap == nil || snap.TimerD == nil {
			t.Fatalf("mustClnTxSnapshot(t, tx) = %+v, want completed snapshot with timer D", snap)
		}

		rtp := newStubClnTransp(false)
		restored, err := sip.RestoreInviteClientTransaction(snap, rtp, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
		}

		rec := &clnTxSnapRecorder{}
		restored.BindSnapshotHandler(rec.handler())

		// expire timer D while the restored transaction is still dormant
		time.Sleep(sip.TimeD + 100*time.Millisecond)
		startClnTx(t, restored)

		// the expired timer must produce exactly one terminal publication
		want := []sip.TransactionState{sip.TransactionStateTerminated}
		if got := rec.states(); !slices.Equal(got, want) {
			t.Fatalf("restored snapshot states = %v, want %v", got, want)
		}
	})
}

func TestInviteClientTransaction_SnapshotNotification_TerminateNoop(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".snap-term-noop", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		// an observer that terminates on every publication: terminating an
		// already-terminated transaction must be an observation no-op,
		// otherwise the terminal publication loops forever
		rec := &clnTxSnapRecorder{}
		var calls atomic.Int32
		tx.BindSnapshotHandler(sip.ClientTransactionSnapshotHandlerFunc(func(hdlrCtx context.Context, snap *sip.ClientTransactionSnapshot) {
			rec.handler()(hdlrCtx, snap)
			if calls.Add(1) >= 3 {
				t.Error("snapshot handler called more than twice: no-op Terminate must not republish")
				return
			}
			if err := tx.Terminate(hdlrCtx, errors.ErrorWrap("terminate from observer")); err != nil {
				t.Errorf("tx.Terminate() in handler error = %v, want nil", err)
			}
		}))

		startClnTx(t, tx)
		tp.waitSendReq(t)

		want := []sip.TransactionState{sip.TransactionStateCalling, sip.TransactionStateTerminated}
		if got := rec.states(); !slices.Equal(got, want) {
			t.Fatalf("snapshot states = %v, want %v", got, want)
		}

		before, err := json.Marshal(mustClnTxSnapshot(t, tx))
		if err != nil {
			t.Fatalf("json.Marshal(snapshot) error = %v, want nil", err)
		}

		// repeated external Terminate adds no event and no new stable snapshot
		for range 2 {
			if err := tx.Terminate(ctx, errors.ErrorWrap("again")); err != nil {
				t.Fatalf("tx.Terminate() error = %v, want nil", err)
			}
		}
		if got := rec.len(); got != 2 {
			t.Fatalf("snapshot notifications after repeated Terminate = %d, want 2", got)
		}

		after, err := json.Marshal(mustClnTxSnapshot(t, tx))
		if err != nil {
			t.Fatalf("json.Marshal(snapshot) error = %v, want nil", err)
		}
		if !slices.Equal(before, after) {
			t.Fatal("stored snapshot changed after no-op Terminate")
		}
	})
}

func TestInviteClientTransaction_SnapshotNotification_RecoversAfterPanic(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".snap-panic", local, remote)
		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		// a panicking observer propagates to the caller but must not leave
		// the notification drain or lifecycle lock in a broken state
		badUnbind := tx.BindSnapshotHandler(sip.ClientTransactionSnapshotHandlerFunc(
			func(context.Context, *sip.ClientTransactionSnapshot) {
				panic("observer panic")
			},
		))

		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Error("tx.Start() panic = nil, want observer panic")
				}
			}()
			_ = tx.Start(ctx)
		}()
		badUnbind()

		if !tx.Started() {
			t.Fatal("tx.Started() = false after recovered panic, want true")
		}
		if _, err := tx.Snapshot(); err != nil {
			t.Fatalf("tx.Snapshot() error = %v, want nil", err)
		}

		rec := &clnTxSnapRecorder{}
		tx.BindSnapshotHandler(rec.handler())

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
		}
		if got := rec.len(); got != 1 {
			t.Fatalf("snapshot notifications after panic recovery = %d, want 1", got)
		}
		if ev := rec.at(0); ev.State != sip.TransactionStateProceeding {
			t.Fatalf("snapshot state = %q, want %q", ev.State, sip.TransactionStateProceeding)
		}
	})
}

func TestRestoreInviteClientTransaction_TimerContext(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		localAddr := netip.MustParseAddrPort("11.11.11.11:5070")
		remoteAddr := netip.MustParseAddrPort("55.55.55.55:5060")
		tp := newPrxStubTransp()
		timing := sip.TimingConfig{T1: 5 * time.Millisecond}
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".restored-context", localAddr, remoteAddr)
		tx, err := sip.NewInviteClientTransaction(req, tp, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		registerTxCleanup(t, tx)
		if err := tx.Start(context.WithValue(t.Context(), testCtxKey{}, "original")); err != nil {
			t.Fatalf("tx.Start() error = %v, want nil", err)
		}
		_ = tp.waitSendReq(t)

		snapshot, err := tx.Snapshot()
		if err != nil {
			t.Fatalf("tx.Snapshot() error = %v, want nil", err)
		}
		if err := tx.Terminate(t.Context(), nil); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		restored, err := sip.RestoreInviteClientTransaction(
			snapshot, tp,
			sip.ClientTransactionOptions{Timing: timing},
		)
		if err != nil {
			t.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
		}
		registerTxCleanup(t, restored)
		if err := restored.Start(context.WithValue(t.Context(), testCtxKey{}, "restored")); err != nil {
			t.Fatalf("restored.Start() error = %v, want nil", err)
		}

		time.Sleep(timing.TimeA() + time.Millisecond)
		assertPropagationCtx(t, tp.waitSendReq(t).ctx, restored, tp, "restored")
		if err := restored.Terminate(t.Context(), nil); err != nil {
			t.Fatalf("restored.Terminate() error = %v, want nil", err)
		}
	})
}
