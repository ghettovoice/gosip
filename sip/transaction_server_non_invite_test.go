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
	"github.com/ghettovoice/gosip/sip/header"
)

func TestNonInviteServerTransaction_LifecycleUnrelTransp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig

		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5070")

		tp := newStubServerTransport(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".unreliable", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp, sip.ServerTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		ctx := t.Context()

		if err := tx.Respond(ctx, sip.ResponseStatusRinging); err != nil {
			t.Fatalf("tx.Respond(ctx, 180, nil) error = %v, want nil", err)
		}

		call := tp.waitSendRes(t)
		if call.res.Status() != sip.ResponseStatusRinging {
			t.Fatalf("provisional response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusRinging)
		}

		if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
			t.Fatalf("tx.State() = %q, want %q", got, want)
		}

		if err := tx.RecvRequest(ctx, req); err != nil {
			t.Fatalf("tx.RecvRequest(ctx, request) error = %v, want nil", err)
		}

		call = tp.waitSendRes(t)
		if call.res.Status() != sip.ResponseStatusRinging {
			t.Fatalf("retransmit provisional mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusRinging)
		}

		if err := tx.Respond(ctx, sip.ResponseStatusCallIsBeingForwarded); err != nil {
			t.Fatalf("tx.Respond(ctx, 181, nil) error = %v, want nil", err)
		}

		call = tp.waitSendRes(t)
		if call.res.Status() != sip.ResponseStatusCallIsBeingForwarded {
			t.Fatalf("updated provisional mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusCallIsBeingForwarded)
		}

		if err := tx.Respond(ctx, sip.ResponseStatusOK); err != nil {
			t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
		}

		call = tp.waitSendRes(t)
		if call.res.Status() != sip.ResponseStatusOK {
			t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
		}

		if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
			t.Fatalf("tx.State() = %q, want %q", got, want)
		}

		if err := tx.RecvRequest(ctx, req); err != nil {
			t.Fatalf("tx.RecvRequest(ctx, request) error = %v, want nil", err)
		}

		call = tp.waitSendRes(t)
		if call.res.Status() != sip.ResponseStatusOK {
			t.Fatalf("retransmit final mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
		}

		if err := tx.Respond(ctx, sip.ResponseStatusTrying); err == nil {
			t.Fatal("tx.Respond(ctx, 100, nil) error = nil, want error")
		} else if !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("unexpected error: got %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}

		if err := tx.Respond(ctx, sip.ResponseStatusOK); err != nil {
			t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
		}

		tp.ensureNoSendRes(t)

		waitForTransactState(t, tx, sip.TransactionStateTerminated, timing.TimeJ()+200*time.Millisecond)

		tp.ensureNoSendRes(t)
	})
}

func TestNonInviteServerTransaction_SendResponseMismatchCallID(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5070")

		tp := newStubServerTransport(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".mismatch-callid", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		res, err := req.NewResponse(sip.ResponseStatusRinging)
		if err != nil {
			t.Fatalf("req.NewResponse(180, nil) error = %v, want nil", err)
		}

		res.WithMessage(func(r *sip.Response) {
			r.Headers.Set(header.CallID("call-mismatch@localhost"))
		})

		ctx := t.Context()
		if err := tx.SendResponse(ctx, res); err == nil {
			t.Fatal("tx.SendResponse(ctx, res, nil) error = nil, want error")
		} else if !errors.Is(err, sip.ErrMessageNotMatched) {
			t.Fatalf("unexpected error: got %v, want wrapped %v", err, sip.ErrMessageNotMatched)
		}
	})
}

func TestNonInviteServerTransaction_SendResponseMismatchVia(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5070")

		tp := newStubServerTransport(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".mismatch-via", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		res, err := req.NewResponse(sip.ResponseStatusRinging)
		if err != nil {
			t.Fatalf("req.NewResponse(180, nil) error = %v, want nil", err)
		}

		res.WithMessage(func(r *sip.Response) {
			if via, ok := r.Headers.FirstVia(); ok && via != nil {
				via.Params.Set("branch", sip.MagicCookie+".other-branch")
			}
		})

		ctx := t.Context()
		if err := tx.SendResponse(ctx, res); err == nil {
			t.Fatal("tx.SendResponse(ctx, res, nil) error = nil, want error")
		} else if !errors.Is(err, sip.ErrMessageNotMatched) {
			t.Fatalf("unexpected error: got %v, want wrapped %v", err, sip.ErrMessageNotMatched)
		}
	})
}

func TestNonInviteServerTransaction_SendResponseMismatchCSeqMethod(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5070")

		tp := newStubServerTransport(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".mismatch-cseq", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		res, err := req.NewResponse(sip.ResponseStatusRinging)
		if err != nil {
			t.Fatalf("req.NewResponse(180, nil) error = %v, want nil", err)
		}

		res.WithMessage(func(r *sip.Response) {
			r.Headers.Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInvite})
		})

		ctx := t.Context()
		if err := tx.SendResponse(ctx, res); err == nil {
			t.Fatal("tx.SendResponse(ctx, res, nil) error = nil, want error")
		} else if !errors.Is(err, sip.ErrMessageNotMatched) {
			t.Fatalf("unexpected error: got %v, want wrapped %v", err, sip.ErrMessageNotMatched)
		}
	})
}

func TestNonInviteServerTransaction_ProceedingTranspErr(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5070")

		tp := newStubServerTransport(false)
		sendErr := errors.New("transport test error")
		tp.setSendResHook(func(_ sendResCall, idx int) error {
			if idx >= 1 {
				return sendErr
			}
			return nil
		})

		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".transp-err", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		ctx := t.Context()

		if err := tx.Respond(ctx, sip.ResponseStatusRinging); err != nil {
			t.Fatalf("tx.Respond(ctx, 180, nil) error = %v, want nil", err)
		}

		call := tp.waitSendRes(t)
		if call.res.Status() != sip.ResponseStatusRinging {
			t.Fatalf("provisional response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusRinging)
		}

		if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
			t.Fatalf("tx.State() = %q, want %q", got, want)
		}

		errCh := make(chan error, 1)
		tx.BindErrorHandler(sip.ErrorHandlerFunc(func(ctx context.Context, err error) {
			select {
			case errCh <- err:
			default:
			}
		}))

		if err := tx.Respond(ctx, sip.ResponseStatusCallIsBeingForwarded); err != nil {
			t.Fatalf("tx.Respond(ctx, 181, nil) error = %v, want nil", err)
		}

		select {
		case err := <-errCh:
			if !errors.Is(err, sendErr) {
				t.Fatalf("transport error mismatch: got %v, want wrapped %v", err, sendErr)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("expected transport error callback")
		}

		waitForTransactState(t, tx, sip.TransactionStateTerminated, 200*time.Millisecond)

		tp.ensureNoSendRes(t)
	})
}

func TestNonInviteServerTransaction_RoundTripSnapshot(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig

		remote := netip.MustParseAddrPort("44.44.44.44:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		origTP := newStubServerTransport(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".snapshot", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, origTP, sip.ServerTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		ctx := t.Context()
		if err := tx.Respond(ctx, sip.ResponseStatusOK); err != nil {
			t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
		}

		call := origTP.waitSendRes(t)
		if call.res.Status() != sip.ResponseStatusOK {
			t.Fatalf("final response mismatch: got %v, want %v", call.res.Status(), sip.ResponseStatusOK)
		}

		if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
			t.Fatalf("tx.State() = %q, want %q", got, want)
		}

		snap := mustServerSnapshot(t, tx)
		if snap == nil || snap.TimerJ == nil {
			t.Fatalf("mustServerSnapshot(t, tx).TimerJ = %v, want non-nil", snap.TimerJ)
		}

		restoredTP := newStubServerTransport(origTP.reliable)

		restored, err := sip.RestoreNonInviteServerTransaction(snap, restoredTP, sip.ServerTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.RestoreNonInviteServerTransaction() error = %v, want nil", err)
		}

		startServerTransaction(t, restored)

		if got, want := restored.State(), sip.TransactionStateCompleted; got != want {
			t.Fatalf("restored.State() = %q, want %q", got, want)
		}

		if got, want := restored.Key(), tx.Key(); got != want {
			t.Fatalf("restored.Key() = %v, want %v", got, want)
		}

		if res := restored.LastResponse(); res.Status() != sip.ResponseStatusOK {
			t.Fatalf("restored.LastResponse().Status() = %v, want %v", res.Status(), sip.ResponseStatusOK)
		}

		waitForTransactState(t, restored, sip.TransactionStateTerminated, timing.TimeJ()+200*time.Millisecond)
	})
}

func TestNonInviteServerTransaction_Terminate_FromTrying(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5070")

		tp := newStubServerTransport(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-trying", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

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
		if err := tx.Terminate(ctx, nil); err != nil {
			t.Fatalf("tx.Terminate() error = %v", err)
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

func TestNonInviteServerTransaction_Terminate_FromProceeding(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5070")

		tp := newStubServerTransport(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-proceeding", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		ctx := t.Context()
		if err := tx.Respond(ctx, sip.ResponseStatusRinging); err != nil {
			t.Fatalf("tx.Respond(ctx, 180, nil) error = %v, want nil", err)
		}

		tp.drainSendRess()

		if got := tx.State(); got != sip.TransactionStateProceeding {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateProceeding)
		}

		if err := tx.Terminate(ctx, nil); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		if got := tx.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}

		tp.ensureNoSendRes(t)
	})
}

func TestNonInviteServerTransaction_Terminate_FromCompleted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5070")

		tp := newStubServerTransport(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-completed", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		ctx := t.Context()
		if err := tx.Respond(ctx, sip.ResponseStatusOK); err != nil {
			t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
		}

		tp.drainSendRess()

		if got := tx.State(); got != sip.TransactionStateCompleted {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateCompleted)
		}

		if err := tx.Terminate(ctx, nil); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		if got := tx.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}

		tp.ensureNoSendRes(t)
	})
}

func TestNonInviteServerTransaction_Terminate_Idempotent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5070")

		tp := newStubServerTransport(true)
		req := newInNonInviteReq(t, "TCP", sip.MagicCookie+".terminate-idempotent", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)

		ctx := t.Context()

		if err := tx.Terminate(ctx, nil); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		if err := tx.Terminate(ctx, nil); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}

		if got := tx.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}
	})
}

func TestNonInviteServerTransaction_SendResponseBeforeStart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubServerTransport(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".respond-before-start", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}

		if err := tx.SendResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("tx.SendResponse() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}
		if err := tx.Respond(ctx, sip.ResponseStatusOK); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("tx.Respond() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}
		tp.ensureNoSendRes(t)

		startServerTransaction(t, tx)
		if err := tx.SendResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
			t.Fatalf("tx.SendResponse() error = %v, want nil", err)
		}
		if call := tp.waitSendRes(t); call.res.Status() != sip.ResponseStatusOK {
			t.Fatalf("sent response status = %v, want %v", call.res.Status(), sip.ResponseStatusOK)
		}
	})
}

func TestNonInviteServerTransaction_RepeatStart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubServerTransport(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".repeat-start", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}

		startServerTransaction(t, tx)

		if err := tx.Start(ctx); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("second tx.Start() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}

		if err := tx.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}
	})
}

func TestRestoreNonInviteServerTransaction_InvalidSnapshot(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubServerTransport(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".restore-invalid-ni", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)
		if err := tx.SendResponse(t.Context(), newInRes(t, req, sip.ResponseStatusOK)); err != nil {
			t.Fatalf("tx.SendResponse() error = %v, want nil", err)
		}
		waitForTransactState(t, tx, sip.TransactionStateCompleted, 100*time.Millisecond)

		base := mustServerSnapshot(t, tx)
		if base == nil {
			t.Fatal("mustServerSnapshot(t, tx) = nil, want snapshot")
		}

		testCases := []struct {
			name   string
			mutate func(snap *sip.ServerTransactionSnapshot)
		}{
			{name: "type mismatch", mutate: func(snap *sip.ServerTransactionSnapshot) {
				snap.Type = sip.TransactionTypeServerInvite
			}},
			{name: "invalid state", mutate: func(snap *sip.ServerTransactionSnapshot) {
				snap.State = 0
			}},
			{name: "wrong state for type", mutate: func(snap *sip.ServerTransactionSnapshot) {
				snap.State = sip.TransactionStateConfirmed
			}},
			{name: "key mismatch", mutate: func(snap *sip.ServerTransactionSnapshot) {
				snap.Key.Branch = "z9hG4bK.other"
			}},
			{name: "foreign active timer", mutate: func(snap *sip.ServerTransactionSnapshot) {
				snap.TimerG = snap.TimerJ
			}},
			{name: "malformed stopped foreign timer", mutate: func(snap *sip.ServerTransactionSnapshot) {
				snap.TimerG = &timeutil.TimerSnapshot{State: timeutil.TimerStateStopped}
			}},
			{name: "active timer in wrong state", mutate: func(snap *sip.ServerTransactionSnapshot) {
				snap.State = sip.TransactionStateProceeding
			}},
		}

		for _, c := range testCases {
			snap := *base
			c.mutate(&snap)

			restoredTP := newStubServerTransport(false)
			if _, err := sip.RestoreNonInviteServerTransaction(&snap, restoredTP); err == nil {
				t.Fatalf("sip.RestoreNonInviteServerTransaction(%s) error = nil, want error", c.name)
			}
			restoredTP.ensureNoSendRes(t)
		}
	})
}
