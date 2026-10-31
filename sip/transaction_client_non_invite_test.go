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
	"github.com/ghettovoice/gosip/sip"
)

func TestNonInviteClientTransaction_LifecycleUnreliable(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig

		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("22.22.22.22:5070")

		tp := newStubClientTransport(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".client-noninvite", local, remote)

		tx, err := sip.NewNonInviteClientTransaction(req, tp, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)

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
		// the firing lock at the moment RecvResponse returns, so use waitForTransactState.
		waitForTransactState(t, tx, sip.TransactionStateProceeding, 100*time.Millisecond)

		assertResponseStatus(t, resCh, sip.ResponseStatusRinging)

		tp.drainSendReqs()

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

		tp.drainSendReqs()
		tp.ensureNoSendReq(t)

		waitForTransactState(t, tx, sip.TransactionStateTerminated, timing.TimeK()+200*time.Millisecond)
		tp.ensureNoSendReq(t)
	})
}

func TestNonInviteClientTransaction_RoundTripSnapshot(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig

		remote := netip.MustParseAddrPort("66.66.66.66:5080")
		local := netip.MustParseAddrPort("22.22.22.22:5071")

		origTP := newStubClientTransport(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".client-noninvite-snapshot", local, remote)

		tx, err := sip.NewNonInviteClientTransaction(req, origTP, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction(req, tp, opts) error = %v, want nil", err)
		}
		startClientTransaction(t, tx)

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

		restored, err := sip.RestoreNonInviteClientTransaction(&snapCopy, restoredTP, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.RestoreNonInviteClientTransaction(snap, tp, opts) error = %v, want nil", err)
		}

		startClientTransaction(t, restored)

		if got, want := restored.State(), sip.TransactionStateCompleted; got != want {
			t.Fatalf("restored.State() = %q, want %q", got, want)
		}

		if got, want := restored.Key(), tx.Key(); !got.Equal(want) {
			t.Fatalf("restored.Key() = %v, want %v", got, want)
		}

		if res := restored.LastResponse(); res.Status() != sip.ResponseStatusOK {
			t.Fatalf("restored.LastResponse().Status() = %v, want %v", res.Status(), sip.ResponseStatusOK)
		}

		waitForTransactState(t, restored, sip.TransactionStateTerminated, timing.TimeK()+200*time.Millisecond)
		restoredTP.ensureNoSendReq(t)
	})
}

func TestNonInviteClientTransaction_Terminate_FromTrying(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("22.22.22.22:5070")

		tp := newStubClientTransport(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-trying", local, remote)

		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)

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

		tp := newStubClientTransport(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-proceeding", local, remote)

		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
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

func TestNonInviteClientTransaction_Terminate_FromCompleted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("22.22.22.22:5070")

		tp := newStubClientTransport(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-completed", local, remote)

		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)

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

		tp := newStubClientTransport(true)
		req := newOutNonInviteReq(t, "TCP", sip.MagicCookie+".client-reliable-timer-k", local, remote)

		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)

		tp.waitSendReq(t)
		if err := tx.RecvResponse(t.Context(), newInRes(t, req, sip.ResponseStatusOK)); err != nil {
			t.Fatalf("tx.RecvResponse(200) error = %v, want nil", err)
		}

		if state := tx.State(); state == sip.TransactionStateCompleted {
			snap := mustClientSnapshot(t, tx)
			if snap.TimerK != nil && snap.TimerK.Duration != 0 {
				t.Fatalf("Timer K = %+v, want duration 0", snap.TimerK)
			}
		} else if state != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q or %q", state, sip.TransactionStateCompleted, sip.TransactionStateTerminated)
		}

		waitForTransactState(t, tx, sip.TransactionStateTerminated, time.Second)
	})
}

func TestNonInviteClientTransaction_PreparedRecvWaitsForStart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
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

		startClientTransaction(t, tx)
		if call := tp.waitSendReq(t); call.req.Method() != sip.RequestMethodInfo {
			t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInfo)
		}

		if err := <-errCh; err != nil {
			t.Fatalf("tx.RecvResponse() error = %v, want nil", err)
		}
		waitForTransactState(t, tx, sip.TransactionStateCompleted, 100*time.Millisecond)
	})
}

func TestNonInviteClientTransaction_RestoreExpiredTimerF(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".restore-timer-f", local, remote)

		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)
		tp.waitSendReq(t)

		snap := mustClientSnapshot(t, tx)
		if snap == nil || snap.TimerE == nil || snap.TimerF == nil {
			t.Fatalf("mustClientSnapshot(t, tx) = %+v, want running timers E and F", snap)
		}

		time.Sleep(timing.TimeF() + 100*time.Millisecond)

		restoredTP := newStubClientTransport(false)
		restored, err := sip.RestoreNonInviteClientTransaction(snap, restoredTP)
		if err != nil {
			t.Fatalf("sip.RestoreNonInviteClientTransaction() error = %v, want nil", err)
		}
		if got := restored.State(); got != sip.TransactionStateTrying {
			t.Fatalf("restored.State() = %q, want %q", got, sip.TransactionStateTrying)
		}

		time.Sleep(timing.TimeF())
		restoredTP.ensureNoSendReq(t)

		startClientTransaction(t, restored)

		restoredTP.ensureNoSendReq(t)
		waitForTransactState(t, restored, sip.TransactionStateTerminated, 100*time.Millisecond)
	})
}

func TestNonInviteClientTransaction_RepeatStart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".repeat-start", local, remote)

		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}

		startClientTransaction(t, tx)
		tp.waitSendReq(t)

		if err := tx.Start(ctx); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("second tx.Start() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}
	})
}

func TestNonInviteClientTransaction_TerminateDuringInitialSend(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
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
		waitForTransactState(t, tx, sip.TransactionStateTerminated, 100*time.Millisecond)

		snap := mustClientSnapshot(t, tx)
		if snap.TimerE != nil || snap.TimerF != nil || snap.TimerK != nil {
			t.Fatalf("terminated transaction timers = %+v, want all nil", snap)
		}
		tp.drainSendReqs()
		tp.ensureNoSendReq(t)
	})
}

func TestNonInviteClientTransaction_RestoreStoppedTimerK(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubClientTransport(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".restore-stopped-k", local, remote)

		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)
		tp.waitSendReq(t)
		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusNotFound)); err != nil {
			t.Fatalf("tx.RecvResponse(404) error = %v, want nil", err)
		}
		waitForTransactState(t, tx, sip.TransactionStateCompleted, 100*time.Millisecond)

		snap := mustClientSnapshot(t, tx)
		if snap == nil || snap.TimerK == nil {
			t.Fatalf("mustClientSnapshot(t, tx) = %+v, want running timer K", snap)
		}
		snap.TimerK = &timeutil.TimerSnapshot{State: timeutil.TimerStateStopped, StartTime: time.Now()}

		restoredTP := newStubClientTransport(false)
		restored, err := sip.RestoreNonInviteClientTransaction(snap, restoredTP)
		if err != nil {
			t.Fatalf("sip.RestoreNonInviteClientTransaction() error = %v, want nil", err)
		}

		startClientTransaction(t, restored)
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

		tp := newStubClientTransport(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".restore-invalid", local, remote)

		tx, err := sip.NewNonInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)
		tp.waitSendReq(t)

		base := mustClientSnapshot(t, tx)
		if base == nil {
			t.Fatal("mustClientSnapshot(t, tx) = nil, want snapshot")
		}

		tests := []struct {
			name   string
			mutate func(snap *sip.ClientTransactionSnapshot)
		}{
			{name: "type mismatch", mutate: func(snap *sip.ClientTransactionSnapshot) {
				snap.Type = sip.TransactionTypeClientInvite
			}},
			{name: "wrong state for type", mutate: func(snap *sip.ClientTransactionSnapshot) {
				snap.State = sip.TransactionStateConfirmed
			}},
			{name: "key mismatch", mutate: func(snap *sip.ClientTransactionSnapshot) {
				snap.Key.Branch = "z9hG4bK.other"
			}},
			{name: "foreign active timer", mutate: func(snap *sip.ClientTransactionSnapshot) {
				snap.TimerD = snap.TimerE
			}},
			{name: "invalid timer", mutate: func(snap *sip.ClientTransactionSnapshot) {
				snap.TimerF = &timeutil.TimerSnapshot{State: timeutil.TimerStateInvalid}
			}},
			{name: "malformed stopped foreign timer", mutate: func(snap *sip.ClientTransactionSnapshot) {
				snap.TimerD = &timeutil.TimerSnapshot{State: timeutil.TimerStateStopped}
			}},
		}

		for _, tt := range tests {
			snap := *base
			tt.mutate(&snap)

			restoredTP := newStubClientTransport(false)
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

		tp := newStubClientTransport(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".snap-ntf", local, remote)

		tx, err := sip.NewNonInviteClientTransaction(req, tp, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}

		rec := &clnSnapRecorder{}

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

		startClientTransaction(t, tx)
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

		waitForTransactState(t, tx, sip.TransactionStateTerminated, timing.TimeK()+200*time.Millisecond)
		if got := rec.len(); got != 4 {
			t.Fatalf("snapshot notifications after timer K = %d, want 4", got)
		}
		if ev := rec.at(3); ev.State != sip.TransactionStateTerminated ||
			ev.TimerE != nil || ev.TimerF != nil || ev.TimerK != nil {
			t.Fatalf("terminal snapshot = %+v, want Terminated with all timers cleaned", ev)
		}

		// every published snapshot must be restorable
		for i := range rec.len() {
			if _, err := sip.RestoreNonInviteClientTransaction(rec.at(i), newStubClientTransport(false)); err != nil {
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

		tp := newStubClientTransport(false)
		req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".snap-tmr-e", local, remote)

		tx, err := sip.NewNonInviteClientTransaction(req, tp, sip.ClientTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)
		tp.waitSendReq(t)

		rec := &clnSnapRecorder{}
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
		if ev.TimerE == nil || ev.TimerE.State != timeutil.TimerStateRunning ||
			ev.TimerE.Duration != 2*timing.TimeE() {
			t.Fatalf("snapshot TimerE = %+v, want running with duration %v", ev.TimerE, 2*timing.TimeE())
		}
	})
}
