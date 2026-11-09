package sip_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/timeutil"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip"
)

func TestNonInviteClientTransaction_BindStateHandler_ReplayContext(t *testing.T) {
	t.Parallel()

	localAddr := netip.MustParseAddrPort("11.11.11.11:5070")
	remoteAddr := netip.MustParseAddrPort("55.55.55.55:5060")
	tp := newPrxStubTransp()
	req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".state-replay-context", localAddr, remoteAddr)
	tx, err := sip.NewNonInviteClientTransaction(req, tp)
	if err != nil {
		t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
	}
	registerTxCleanup(t, tx)
	if err := tx.Start(t.Context()); err != nil {
		t.Fatalf("tx.Start() error = %v, want nil", err)
	}
	_ = tp.waitSendReq(t)
	if err := tx.Terminate(context.WithValue(t.Context(), testCtxKey{}, "state-replay"), nil); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	stateCtxs := make(chan context.Context, 1)
	tx.BindStateHandler(sip.TransactionStateHandlerFunc(func(ctx context.Context, _, _ sip.TransactionState) {
		stateCtxs <- ctx
	}))
	assertPropagationCtx(t,
		testutil.WaitResult(t, stateCtxs, "state replay callback context"),
		tx, tp, "state-replay",
	)
}

func TestNonInviteClientTransaction_LifecycleUnreliable(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("22.22.22.22:5070")
		tp := newStubClnTransp(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".client-noninvite", local, remote)
		tx, err := sip.NewNonInviteClientTransaction(req, tp, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)

		call := tp.waitSendReq(t)
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
		retrans := tp.waitSendReq(t)
		if retrans.req.Method() != sip.RequestMethodInfo {
			t.Fatalf("retransmit method = %q, want %q", retrans.req.Method(), sip.RequestMethodInfo)
		}

		resCh := make(chan *sip.ResponseEnvelope, 2)
		tx.BindResponseHandler(sip.InboundResponseHandlerFunc(func(_ context.Context, res *sip.ResponseEnvelope) {
			resCh <- res
		}))

		ctx := t.Context()
		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
		}

		// FiringQueued mode may defer the state transition if timer E goroutine holds
		// the firing lock at the moment RecvResponse returns, so use waitForTxState.
		waitForTxState(t, tx, sip.TransactionStateProceeding, 100*time.Millisecond)
		assertResStatus(t, resCh, sip.ResponseStatusRinging)
		tp.drainSendReqs()

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 200) error = %v, want nil", err)
		}

		if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
			t.Fatalf("tx.State() = %q, want %q", got, want)
		}

		assertResStatus(t, resCh, sip.ResponseStatusOK)

		if res := tx.LastResponse(); res.Status() != sip.ResponseStatusOK {
			t.Fatalf("tx.LastResponse().Status() = %v, want %v", res.Status(), sip.ResponseStatusOK)
		}

		tp.drainSendReqs()
		tp.ensureNoSendReq(t)

		waitForTxState(t, tx, sip.TransactionStateTerminated, timing.TimeK()+200*time.Millisecond)
		tp.ensureNoSendReq(t)
	})
}

func TestNonInviteClientTransaction_TryingPreservesTimerF(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("22.22.22.22:5070")
		tp := newStubClnTransp(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".trying-timer-f", local, remote)
		tx, err := sip.NewNonInviteClientTransaction(req, tp, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)
		tp.waitSendReq(t)

		snapBefore := mustClnTxSnapshot(t, tx)
		if snapBefore == nil || snapBefore.TimerE == nil || snapBefore.TimerF == nil {
			t.Fatalf("mustClnTxSnapshot(t, tx) = %+v, want running timers E and F", snapBefore)
		}
		tmrEDeadline := snapBefore.TimerE.StartTime.Add(snapBefore.TimerE.Duration)
		tmrFDeadline := snapBefore.TimerF.StartTime.Add(snapBefore.TimerF.Duration)

		ctx := t.Context()
		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusTrying)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 100) error = %v, want nil", err)
		}
		waitForTxState(t, tx, sip.TransactionStateProceeding, 100*time.Millisecond)

		snapAfter := mustClnTxSnapshot(t, tx)
		if snapAfter == nil || snapAfter.TimerE == nil || snapAfter.TimerF == nil {
			t.Fatalf("mustClnTxSnapshot(t, tx) after 100 = %+v, want running timers E and F", snapAfter)
		}
		if got := snapAfter.TimerF.StartTime.Add(snapAfter.TimerF.Duration); !got.Equal(tmrFDeadline) {
			t.Fatalf("TimerF deadline after 100 = %v, want unchanged %v", got, tmrFDeadline)
		}
		if got := snapAfter.TimerE.StartTime.Add(snapAfter.TimerE.Duration); !got.Equal(tmrEDeadline) {
			t.Fatalf("TimerE deadline after 100 = %v, want unchanged %v", got, tmrEDeadline)
		}

		// Timer E retransmission behavior stays as before: the request is
		// resent when the original deadline passes in Proceeding.
		time.Sleep(time.Until(tmrEDeadline))
		retrans := tp.waitSendReq(t)
		if retrans.req.Method() != sip.RequestMethodInfo {
			t.Fatalf("timer E retransmission method = %q, want %q", retrans.req.Method(), sip.RequestMethodInfo)
		}
	})
}

func TestNonInviteClientTransaction_RoundTripSnapshot(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig
		remote := netip.MustParseAddrPort("66.66.66.66:5080")
		local := netip.MustParseAddrPort("22.22.22.22:5071")
		origTP := newStubClnTransp(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".client-noninvite-snapshot", local, remote)
		tx, err := sip.NewNonInviteClientTransaction(req, origTP, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction(req, tp, opts) error = %v, want nil", err)
		}
		startClnTx(t, tx)

		call := origTP.waitSendReq(t)
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
		restored, err := sip.RestoreNonInviteClientTransaction(&snapCopy, restoredTP, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.RestoreNonInviteClientTransaction(snap, tp, opts) error = %v, want nil", err)
		}

		startClnTx(t, restored)

		if got, want := restored.State(), sip.TransactionStateCompleted; got != want {
			t.Fatalf("restored.State() = %q, want %q", got, want)
		}

		if got, want := restored.Key(), tx.Key(); !got.Equal(want) {
			t.Fatalf("restored.Key() = %v, want %v", got, want)
		}

		if res := restored.LastResponse(); res.Status() != sip.ResponseStatusOK {
			t.Fatalf("restored.LastResponse().Status() = %v, want %v", res.Status(), sip.ResponseStatusOK)
		}

		waitForTxState(t, restored, sip.TransactionStateTerminated, timing.TimeK()+200*time.Millisecond)
		restoredTP.ensureNoSendReq(t)
	})
}

func TestNonInviteClientTransaction_Terminate_FromTrying(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("22.22.22.22:5070")
		tp := newStubClnTransp(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-trying", local, remote)
		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)

		tp.waitSendReq(t)

		if got := tx.State(); got != sip.TransactionStateTrying {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTrying)
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
			t.Fatal("BindStateHandler callback wait timeout")
		}

		if got := tx.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}

		tp.ensureNoSendReq(t)
	})
}

func TestNonInviteClientTransaction_Terminate_FromProceeding(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("22.22.22.22:5070")
		tp := newStubClnTransp(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-proceeding", local, remote)
		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
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

func TestNonInviteClientTransaction_Terminate_FromCompleted(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("22.22.22.22:5070")
		tp := newStubClnTransp(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-completed", local, remote)
		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)

		tp.waitSendReq(t)
		ctx := t.Context()

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
			t.Fatalf("tx.RecvResponse(200) error = %v, want nil", err)
		}

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

func TestNonInviteClientTransaction_ReliableTimerK(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("22.22.22.22:5070")
		tp := newStubClnTransp(true)
		req := newOutNonInviteReq(t, "TCP", sip.MagicCookie+".client-reliable-timer-k", local, remote)
		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)

		tp.waitSendReq(t)
		if err := tx.RecvResponse(t.Context(), newInRes(t, req, sip.ResponseStatusOK)); err != nil {
			t.Fatalf("tx.RecvResponse(200) error = %v, want nil", err)
		}

		if state := tx.State(); state == sip.TransactionStateCompleted {
			snap := mustClnTxSnapshot(t, tx)
			if snap.TimerK != nil && snap.TimerK.Duration != 0 {
				t.Fatalf("Timer K = %+v, want duration 0", snap.TimerK)
			}
		} else if state != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q or %q", state, sip.TransactionStateCompleted, sip.TransactionStateTerminated)
		}

		waitForTxState(t, tx, sip.TransactionStateTerminated, time.Second)
	})
}

func TestNonInviteClientTransaction_RecvResponse_WaitsForStart(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".prepared-recv", local, remote)
		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}

		tp.ensureNoSendReq(t)

		if _, err := tx.Snapshot(); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("tx.Snapshot() before Start error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}

		res := newInRes(t, req, sip.ResponseStatusOK)
		errCh := make(chan error, 1)
		go func() { errCh <- tx.RecvResponse(ctx, res) }()

		time.Sleep(100 * time.Millisecond)
		select {
		case err := <-errCh:
			t.Fatalf("tx.RecvResponse() = %v before Start, want blocked", err)
		default:
		}

		startClnTx(t, tx)
		if call := tp.waitSendReq(t); call.req.Method() != sip.RequestMethodInfo {
			t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInfo)
		}

		if err := <-errCh; err != nil {
			t.Fatalf("tx.RecvResponse() error = %v, want nil", err)
		}
		waitForTxState(t, tx, sip.TransactionStateCompleted, 100*time.Millisecond)
	})
}

func TestRestoreNonInviteClientTransaction_ExpiredTimerF(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".restore-timer-f", local, remote)
		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)
		tp.waitSendReq(t)

		snap := mustClnTxSnapshot(t, tx)
		if snap == nil || snap.TimerE == nil || snap.TimerF == nil {
			t.Fatalf("mustClnTxSnapshot(t, tx) = %+v, want running timers E and F", snap)
		}

		time.Sleep(timing.TimeF() + 100*time.Millisecond)

		restoredTP := newStubClnTransp(false)
		restored, err := sip.RestoreNonInviteClientTransaction(snap, restoredTP)
		if err != nil {
			t.Fatalf("sip.RestoreNonInviteClientTransaction() error = %v, want nil", err)
		}
		if got := restored.State(); got != sip.TransactionStateTrying {
			t.Fatalf("restored.State() = %q, want %q", got, sip.TransactionStateTrying)
		}

		time.Sleep(timing.TimeF())
		restoredTP.ensureNoSendReq(t)

		startClnTx(t, restored)

		restoredTP.ensureNoSendReq(t)
		waitForTxState(t, restored, sip.TransactionStateTerminated, 100*time.Millisecond)
	})
}

func TestNonInviteClientTransaction_Start_Repeated(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".repeat-start", local, remote)
		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}

		startClnTx(t, tx)
		tp.waitSendReq(t)

		if err := tx.Start(ctx); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("second tx.Start() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}
	})
}

func TestNonInviteClientTransaction_Terminate_DuringInitialSend(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".term-during-send", local, remote)

		entered := make(chan struct{})
		release := make(chan struct{})
		tp.sendReqHook = func(sendReqCall, int) error {
			close(entered)
			<-release
			return nil
		}

		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
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
		if snap.TimerE != nil || snap.TimerF != nil || snap.TimerK != nil {
			t.Fatalf("terminated transaction timers = %+v, want all nil", snap)
		}

		tp.drainSendReqs()
		tp.ensureNoSendReq(t)
	})
}

func TestRestoreNonInviteClientTransaction_StoppedTimerK(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".restore-stopped-k", local, remote)
		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)
		tp.waitSendReq(t)
		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusNotFound)); err != nil {
			t.Fatalf("tx.RecvResponse(404) error = %v, want nil", err)
		}
		waitForTxState(t, tx, sip.TransactionStateCompleted, 100*time.Millisecond)

		snap := mustClnTxSnapshot(t, tx)
		if snap == nil || snap.TimerK == nil {
			t.Fatalf("mustClnTxSnapshot(t, tx) = %+v, want running timer K", snap)
		}
		snap.TimerK = &timeutil.TimerSnapshot{State: timeutil.TimerStateStopped, StartTime: time.Now()}

		restoredTP := newStubClnTransp(false)
		restored, err := sip.RestoreNonInviteClientTransaction(snap, restoredTP)
		if err != nil {
			t.Fatalf("sip.RestoreNonInviteClientTransaction() error = %v, want nil", err)
		}

		startClnTx(t, restored)
		restoredTP.ensureNoSendReq(t)

		time.Sleep(2 * timing.TimeK())
		if got := restored.State(); got != sip.TransactionStateCompleted {
			t.Fatalf("restored.State() = %q, want %q (stopped timer K must not be revived)", got, sip.TransactionStateCompleted)
		}
	})
}

func TestRestoreNonInviteClientTransaction_InvalidSnapshot(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".restore-invalid", local, remote)
		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
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
					snap.Type = sip.TransactionTypeClientInvite
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
				"foreign active timer",
				func(snap *sip.ClientTransactionSnapshot) {
					snap.TimerD = snap.TimerE
				},
			},
			{
				"invalid timer",
				func(snap *sip.ClientTransactionSnapshot) {
					snap.TimerF = &timeutil.TimerSnapshot{State: timeutil.TimerStateInvalid}
				},
			},
			{
				"malformed stopped foreign timer",
				func(snap *sip.ClientTransactionSnapshot) {
					snap.TimerD = &timeutil.TimerSnapshot{State: timeutil.TimerStateStopped}
				},
			},
		}

		for _, tt := range tests {
			snap := *base
			tt.mutate(&snap)

			restoredTP := newStubClnTransp(false)
			if _, err := sip.RestoreNonInviteClientTransaction(&snap, restoredTP); err == nil {
				t.Fatalf("sip.RestoreNonInviteClientTransaction(%s) error = nil, want error", tt.name)
			}
			restoredTP.ensureNoSendReq(t)
		}
	})
}

func TestNonInviteClientTransaction_SnapshotNotifications(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".snap-ntf", local, remote)
		tx, err := sip.NewNonInviteClientTransaction(req, tp, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
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
		if ev := rec.at(0); ev.State != sip.TransactionStateTrying ||
			ev.TimerE == nil || ev.TimerF == nil || ev.LastResponse != nil {
			t.Fatalf("first snapshot = %+v, want Trying with timers E/F", ev)
		}

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
		}
		if got := rec.len(); got != 2 {
			t.Fatalf("snapshot notifications after 180 = %d, want 2", got)
		}
		if ev := rec.at(1); ev.State != sip.TransactionStateProceeding ||
			ev.LastResponse == nil || ev.LastResponse.Status() != sip.ResponseStatusRinging ||
			ev.TimerE == nil || ev.TimerF == nil {
			t.Fatalf("second snapshot = %+v, want Proceeding with 180 and timers E/F", ev)
		}

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
			t.Fatalf("tx.RecvResponse(ctx, 200) error = %v, want nil", err)
		}
		if got := rec.len(); got != 3 {
			t.Fatalf("snapshot notifications after 200 = %d, want 3", got)
		}
		if ev := rec.at(2); ev.State != sip.TransactionStateCompleted ||
			ev.LastResponse == nil || ev.LastResponse.Status() != sip.ResponseStatusOK ||
			ev.TimerK == nil || ev.TimerE != nil || ev.TimerF != nil {
			t.Fatalf("third snapshot = %+v, want Completed with 200 and timer K", ev)
		}

		waitForTxState(t, tx, sip.TransactionStateTerminated, timing.TimeK()+200*time.Millisecond)
		if got := rec.len(); got != 4 {
			t.Fatalf("snapshot notifications after timer K = %d, want 4", got)
		}
		if ev := rec.at(3); ev.State != sip.TransactionStateTerminated ||
			ev.TimerE != nil || ev.TimerF != nil || ev.TimerK != nil {
			t.Fatalf("terminal snapshot = %+v, want Terminated with all timers cleaned", ev)
		}

		// every published snapshot must be restorable
		for i := range rec.len() {
			if _, err := sip.RestoreNonInviteClientTransaction(rec.at(i), newStubClnTransp(false)); err != nil {
				t.Fatalf("sip.RestoreNonInviteClientTransaction(snapshot[%d]) error = %v, want nil", i, err)
			}
		}
	})
}

func TestNonInviteClientTransaction_SnapshotNotification_TimerE(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")
		tp := newStubClnTransp(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".snap-tmr-e", local, remote)
		tx, err := sip.NewNonInviteClientTransaction(req, tp, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClnTx(t, tx)
		tp.waitSendReq(t)

		rec := &clnTxSnapRecorder{}
		tx.BindSnapshotHandler(rec.handler())

		time.Sleep(timing.TimeE() + 10*time.Millisecond)
		tp.waitSendReq(t)

		if got := rec.len(); got != 1 {
			t.Fatalf("snapshot notifications after timer E = %d, want 1", got)
		}
		ev := rec.at(0)
		if ev.State != sip.TransactionStateTrying {
			t.Fatalf("snapshot state after timer E = %q, want %q", ev.State, sip.TransactionStateTrying)
		}
		if ev.TimerE == nil || ev.TimerE.State != timeutil.TimerStateRunning || ev.TimerE.Duration != 2*timing.TimeE() {
			t.Fatalf("snapshot TimerE = %+v, want running with duration %v", ev.TimerE, 2*timing.TimeE())
		}
	})
}
