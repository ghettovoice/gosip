package sip_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/timeutil"
	"github.com/google/go-cmp/cmp"

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

		tp := newStubSrvTransp(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".unreliable", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp, sip.ServerTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startSrvTx(t, tx)

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

		waitForTxState(t, tx, sip.TransactionStateTerminated, timing.TimeJ()+200*time.Millisecond)

		tp.ensureNoSendRes(t)
	})
}

func TestNonInviteServerTransaction_SendResponse_MismatchCallID(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5070")

		tp := newStubSrvTransp(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".mismatch-callid", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startSrvTx(t, tx)

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

func TestNonInviteServerTransaction_SendResponse_MismatchVia(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5070")

		tp := newStubSrvTransp(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".mismatch-via", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startSrvTx(t, tx)

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

func TestNonInviteServerTransaction_SendResponse_MismatchCSeqMethod(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5070")

		tp := newStubSrvTransp(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".mismatch-cseq", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startSrvTx(t, tx)

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

		tp := newStubSrvTransp(false)
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
		startSrvTx(t, tx)

		stateCh := make(chan struct{}, 4)
		tx.BindStateHandler(sip.TransactionStateHandlerFunc(func(
			context.Context,
			sip.TransactionState,
			sip.TransactionState,
		) {
			stateCh <- struct{}{}
		}))

		errCh := make(chan error, 4)
		tx.BindErrorHandler(sip.ErrorHandlerFunc(func(_ context.Context, err error) {
			errCh <- err
		}))

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
		if got, want := len(stateCh), 1; got != want {
			t.Fatalf("state handler calls after provisional response = %d, want %d", got, want)
		}

		if err := tx.Respond(ctx, sip.ResponseStatusCallIsBeingForwarded); err != nil {
			t.Fatalf("tx.Respond(ctx, 181, nil) error = %v, want nil", err)
		}

		assertTransportError := func() {
			t.Helper()
			select {
			case got := <-errCh:
				if !errors.Is(got, sendErr) {
					t.Fatalf("transport error = %v, want wrapped %v", got, sendErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("expected transport error callback")
			}
			select {
			case got := <-errCh:
				t.Fatalf("transport error callback received more than once: %v", got)
			default:
			}
		}

		assertTransportError()

		if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
			t.Fatalf("tx.State() after failed provisional response = %q, want %q", got, want)
		}
		if lastRes := tx.LastResponse(); lastRes == nil {
			t.Fatal("tx.LastResponse() after failed provisional response = nil")
		} else if got, want := lastRes.Status(), sip.ResponseStatusCallIsBeingForwarded; got != want {
			t.Fatalf("tx.LastResponse().Status() after failed provisional response = %v, want %v", got, want)
		}
		if got, want := len(stateCh), 1; got != want {
			t.Fatalf("state handler calls after transport error = %d, want %d", got, want)
		}
		if lastErr := tx.LastError(); !errors.Is(lastErr, sendErr) {
			t.Fatalf("tx.LastError() = %v, want wrapped %v", lastErr, sendErr)
		}

		if err := tx.RecvRequest(ctx, req); err != nil {
			t.Fatalf("tx.RecvRequest(ctx, request) error = %v, want nil", err)
		}
		assertTransportError()

		if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
			t.Fatalf("tx.State() after failed provisional retransmission = %q, want %q", got, want)
		}
		if lastRes := tx.LastResponse(); lastRes == nil {
			t.Fatal("tx.LastResponse() after failed provisional retransmission = nil")
		} else if got, want := lastRes.Status(), sip.ResponseStatusCallIsBeingForwarded; got != want {
			t.Fatalf("tx.LastResponse().Status() after failed provisional retransmission = %v, want %v", got, want)
		}
		if got, want := len(stateCh), 1; got != want {
			t.Fatalf("state handler calls after failed retransmission = %d, want %d", got, want)
		}
		if lastErr := tx.LastError(); !errors.Is(lastErr, sendErr) {
			t.Fatalf("tx.LastError() after failed retransmission = %v, want wrapped %v", lastErr, sendErr)
		}

		tp.setSendResHook(nil)
		if err := tx.RecvRequest(ctx, req); err != nil {
			t.Fatalf("tx.RecvRequest(ctx, request) after transport recovery error = %v, want nil", err)
		}
		call = tp.waitSendRes(t)
		if got, want := call.res.Status(), sip.ResponseStatusCallIsBeingForwarded; got != want {
			t.Fatalf("retransmit provisional response = %v, want %v", got, want)
		}

		if err := tx.Respond(ctx, sip.ResponseStatusOK); err != nil {
			t.Fatalf("tx.Respond(ctx, 200, nil) error = %v, want nil", err)
		}
		call = tp.waitSendRes(t)
		if got, want := call.res.Status(), sip.ResponseStatusOK; got != want {
			t.Fatalf("final response = %v, want %v", got, want)
		}
		if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
			t.Fatalf("tx.State() after final response = %q, want %q", got, want)
		}
		if got, want := len(stateCh), 2; got != want {
			t.Fatalf("state handler calls after final response = %d, want %d", got, want)
		}
	})
}

func TestNonInviteServerTransaction_CompletedTranspErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		status             sip.ResponseStatus
		fromProceeding     bool
		failRetransmission bool
	}{
		{
			name:   "failed 2xx final response from trying",
			status: sip.ResponseStatusOK,
		},
		{
			name:           "failed 3xx-6xx final response from proceeding",
			status:         sip.ResponseStatusBadRequest,
			fromProceeding: true,
		},
		{
			name:               "failed 2xx final retransmission",
			status:             sip.ResponseStatusAccepted,
			failRetransmission: true,
		},
		{
			name:               "failed 3xx-6xx final retransmission",
			status:             sip.ResponseStatusBadRequest,
			failRetransmission: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				const (
					t1       = 10 * time.Millisecond
					timeSkew = time.Millisecond
				)
				timing := sip.TimingConfig{T1: t1}

				remote := netip.MustParseAddrPort("55.55.55.55:5060")
				local := netip.MustParseAddrPort("33.33.33.33:5070")
				tp := newStubSrvTransp(false)
				sendErr := errors.New("transport test error")

				failAt := 0
				if tt.fromProceeding || tt.failRetransmission {
					failAt = 1
				}
				tp.setSendResHook(func(_ sendResCall, idx int) error {
					if idx == failAt {
						return sendErr
					}
					return nil
				})

				req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".transp-err-completed", local, remote)
				tx, err := sip.NewNonInviteServerTransaction(
					req,
					tp,
					sip.ServerTransactionOptions{Timing: timing},
				)
				if err != nil {
					t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
				}
				startSrvTx(t, tx)

				stateCh := make(chan struct{}, 4)
				tx.BindStateHandler(sip.TransactionStateHandlerFunc(func(
					context.Context,
					sip.TransactionState,
					sip.TransactionState,
				) {
					stateCh <- struct{}{}
				}))

				errCh := make(chan error, 4)
				tx.BindErrorHandler(sip.ErrorHandlerFunc(func(_ context.Context, err error) {
					errCh <- err
				}))

				ctx := t.Context()
				if tt.fromProceeding {
					if err := tx.Respond(ctx, sip.ResponseStatusRinging); err != nil {
						t.Fatalf("tx.Respond(ctx, 180, nil) error = %v, want nil", err)
					}
					call := tp.waitSendRes(t)
					if got, want := call.res.Status(), sip.ResponseStatusRinging; got != want {
						t.Fatalf("provisional response = %v, want %v", got, want)
					}
				}

				if tt.failRetransmission {
					if err := tx.Respond(ctx, tt.status); err != nil {
						t.Fatalf("tx.Respond(ctx, %v, nil) error = %v, want nil", tt.status, err)
					}
					call := tp.waitSendRes(t)
					if got, want := call.res.Status(), tt.status; got != want {
						t.Fatalf("final response = %v, want %v", got, want)
					}
				} else if err := tx.Respond(ctx, tt.status); err != nil {
					t.Fatalf("tx.Respond(ctx, %v, nil) error = %v, want nil", tt.status, err)
				}

				wantStateEvents := 1
				if tt.fromProceeding {
					wantStateEvents++
				}
				if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
					t.Fatalf("tx.State() after final response = %q, want %q", got, want)
				}
				if got := len(stateCh); got != wantStateEvents {
					t.Fatalf("state handler calls after final response = %d, want %d", got, wantStateEvents)
				}

				var timerJDeadline time.Time
				if tt.failRetransmission {
					beforeErr := mustSrvTxSnapshot(t, tx)
					if beforeErr.TimerJ == nil {
						t.Fatal("TimerJ before retransmission error = nil, want non-nil")
					}
					timerJDeadline = beforeErr.TimerJ.StartTime.Add(beforeErr.TimerJ.Duration)
					time.Sleep(timing.TimeJ() / 2)

					if err := tx.RecvRequest(ctx, req); err != nil {
						t.Fatalf("tx.RecvRequest(ctx, request) error = %v, want nil", err)
					}
				}

				assertTransportError := func() {
					t.Helper()
					select {
					case got := <-errCh:
						if !errors.Is(got, sendErr) {
							t.Fatalf("transport error = %v, want wrapped %v", got, sendErr)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("expected transport error callback")
					}
					select {
					case got := <-errCh:
						t.Fatalf("transport error callback received more than once: %v", got)
					default:
					}
				}
				assertTransportError()

				if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
					t.Fatalf("tx.State() after transport error = %q, want %q", got, want)
				}
				if got := len(stateCh); got != wantStateEvents {
					t.Fatalf("state handler calls after transport error = %d, want %d", got, wantStateEvents)
				}
				if lastErr := tx.LastError(); !errors.Is(lastErr, sendErr) {
					t.Fatalf("tx.LastError() = %v, want wrapped %v", lastErr, sendErr)
				}
				if lastRes := tx.LastResponse(); lastRes == nil {
					t.Fatal("tx.LastResponse() = nil after transport error")
				} else if got, want := lastRes.Status(), tt.status; got != want {
					t.Fatalf("tx.LastResponse().Status() = %v, want %v", got, want)
				}

				snap := mustSrvTxSnapshot(t, tx)
				if snap.TimerJ == nil {
					t.Fatal("TimerJ after transport error = nil, want non-nil")
				}
				if got, want := snap.TimerJ.State, timeutil.TimerStateRunning; got != want {
					t.Fatalf("TimerJ.State after transport error = %q, want %q", got, want)
				}
				if !tt.failRetransmission {
					timerJDeadline = snap.TimerJ.StartTime.Add(snap.TimerJ.Duration)
				} else if got := snap.TimerJ.StartTime.Add(snap.TimerJ.Duration); !got.Equal(timerJDeadline) {
					t.Fatalf("TimerJ deadline after retransmission error = %v, want unchanged %v", got, timerJDeadline)
				}

				tp.setSendResHook(nil)
				if err := tx.RecvRequest(ctx, req); err != nil {
					t.Fatalf("tx.RecvRequest(ctx, request) after transport recovery error = %v, want nil", err)
				}
				call := tp.waitSendRes(t)
				if got, want := call.res.Status(), tt.status; got != want {
					t.Fatalf("retransmitted response = %v, want cached final %v", got, want)
				}
				if lastRes := tx.LastResponse(); lastRes == nil {
					t.Fatal("tx.LastResponse() = nil after recovery")
				} else if got, want := lastRes.Status(), tt.status; got != want {
					t.Fatalf("tx.LastResponse().Status() after recovery = %v, want %v", got, want)
				}
				if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
					t.Fatalf("tx.State() after recovered retransmission = %q, want %q", got, want)
				}
				if got := len(stateCh); got != wantStateEvents {
					t.Fatalf("state handler calls after recovered retransmission = %d, want %d", got, wantStateEvents)
				}

				recovered := mustSrvTxSnapshot(t, tx)
				if recovered.TimerJ == nil {
					t.Fatal("TimerJ after recovered retransmission = nil, want non-nil")
				}
				if got := recovered.TimerJ.StartTime.Add(recovered.TimerJ.Duration); !got.Equal(timerJDeadline) {
					t.Fatalf("TimerJ deadline after recovered retransmission = %v, want unchanged %v", got, timerJDeadline)
				}

				remaining := time.Until(timerJDeadline)
				if remaining <= 0 {
					t.Fatalf("TimerJ deadline %v already passed at %v", timerJDeadline, time.Now())
				}
				time.Sleep(remaining + timeSkew)
				waitForTxState(t, tx, sip.TransactionStateTerminated, timeSkew)
				if got, want := len(stateCh), wantStateEvents+1; got != want {
					t.Fatalf("state handler calls after TimerJ = %d, want %d", got, want)
				}
			})
		})
	}
}

func TestNonInviteServerTransaction_RoundTripSnapshot(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig

		remote := netip.MustParseAddrPort("44.44.44.44:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		origTP := newStubSrvTransp(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".snapshot", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, origTP, sip.ServerTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startSrvTx(t, tx)

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

		snap := mustSrvTxSnapshot(t, tx)
		if snap == nil || snap.TimerJ == nil {
			t.Fatalf("mustSrvTxSnapshot(t, tx).TimerJ = %v, want non-nil", snap.TimerJ)
		}

		restoredTP := newStubSrvTransp(origTP.reliable)

		restored, err := sip.RestoreNonInviteServerTransaction(snap, restoredTP, sip.ServerTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.RestoreNonInviteServerTransaction() error = %v, want nil", err)
		}

		startSrvTx(t, restored)

		if got, want := restored.State(), sip.TransactionStateCompleted; got != want {
			t.Fatalf("restored.State() = %q, want %q", got, want)
		}

		if got, want := restored.Key(), tx.Key(); got != want {
			t.Fatalf("restored.Key() = %v, want %v", got, want)
		}

		if res := restored.LastResponse(); res.Status() != sip.ResponseStatusOK {
			t.Fatalf("restored.LastResponse().Status() = %v, want %v", res.Status(), sip.ResponseStatusOK)
		}

		waitForTxState(t, restored, sip.TransactionStateTerminated, timing.TimeJ()+200*time.Millisecond)
	})
}

func TestNonInviteServerTransaction_Terminate_FromTrying(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5070")

		tp := newStubSrvTransp(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-trying", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startSrvTx(t, tx)

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

		tp := newStubSrvTransp(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-proceeding", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startSrvTx(t, tx)

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

		tp := newStubSrvTransp(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-completed", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startSrvTx(t, tx)

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

		tp := newStubSrvTransp(true)
		req := newInNonInviteReq(t, "TCP", sip.MagicCookie+".terminate-idempotent", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startSrvTx(t, tx)

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

func TestNonInviteServerTransaction_SendResponse_BeforeStart(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubSrvTransp(false)
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

		startSrvTx(t, tx)
		if err := tx.SendResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
			t.Fatalf("tx.SendResponse() error = %v, want nil", err)
		}
		if call := tp.waitSendRes(t); call.res.Status() != sip.ResponseStatusOK {
			t.Fatalf("sent response status = %v, want %v", call.res.Status(), sip.ResponseStatusOK)
		}
	})
}

func TestNonInviteServerTransaction_Start_Repeated(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("11.11.11.11:5070")

		tp := newStubSrvTransp(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".repeat-start", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}

		startSrvTx(t, tx)

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

		tp := newStubSrvTransp(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".restore-invalid-ni", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		startSrvTx(t, tx)
		if err := tx.SendResponse(t.Context(), newInRes(t, req, sip.ResponseStatusOK)); err != nil {
			t.Fatalf("tx.SendResponse() error = %v, want nil", err)
		}
		waitForTxState(t, tx, sip.TransactionStateCompleted, 100*time.Millisecond)

		base := mustSrvTxSnapshot(t, tx)
		if base == nil {
			t.Fatal("mustSrvTxSnapshot(t, tx) = nil, want snapshot")
		}

		tests := []struct {
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

		for _, tt := range tests {
			snap := *base
			tt.mutate(&snap)

			restoredTP := newStubSrvTransp(false)
			if _, err := sip.RestoreNonInviteServerTransaction(&snap, restoredTP); err == nil {
				t.Fatalf("sip.RestoreNonInviteServerTransaction(%s) error = nil, want error", tt.name)
			}
			restoredTP.ensureNoSendRes(t)
		}
	})
}

func TestNonInviteServerTransaction_ResponsePolicy(t *testing.T) {
	t.Parallel()

	assertBehavior := func(t *testing.T, tx *sip.NonInviteServerTransaction, want sip.NonInviteServerTransactionBehavior) {
		t.Helper()
		p, ok := any(tx).(sip.NonInviteServerTransactionBehaviorProvider)
		if !ok {
			t.Fatal("non-INVITE transaction does not implement sip.NonInviteServerTransactionBehaviorProvider")
		}
		if got := p.NonInviteServerTransactionBehavior(); got != want {
			t.Fatalf("NonInviteServerTransactionBehaviorProvider.NonInviteServerTransactionBehavior() = %v, want %v", got, want)
		}
	}

	t.Run("legacy keeps provisional and 408 sends", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			remote := netip.MustParseAddrPort("55.55.55.55:5060")
			local := netip.MustParseAddrPort("33.33.33.33:5070")

			tp := newStubSrvTransp(false)
			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".policy-legacy", local, remote)

			tx, err := sip.NewNonInviteServerTransaction(req, tp,
				sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorLegacy})
			if err != nil {
				t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
			}
			assertBehavior(t, tx, sip.NonInviteServerTransactionBehaviorLegacy)
			startSrvTx(t, tx)

			ctx := t.Context()
			if err := tx.SendResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("tx.SendResponse(ctx, 180) error = %v, want nil", err)
			}
			if call := tp.waitSendRes(t); call.res.Status() != sip.ResponseStatusRinging {
				t.Fatalf("sent response status = %v, want %v", call.res.Status(), sip.ResponseStatusRinging)
			}
			if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
				t.Fatalf("tx.State() = %q, want %q", got, want)
			}

			if err := tx.SendResponse(ctx, newInRes(t, req, sip.ResponseStatusRequestTimeout)); err != nil {
				t.Fatalf("tx.SendResponse(ctx, 408) error = %v, want nil", err)
			}
			if call := tp.waitSendRes(t); call.res.Status() != sip.ResponseStatusRequestTimeout {
				t.Fatalf("sent response status = %v, want %v", call.res.Status(), sip.ResponseStatusRequestTimeout)
			}
			if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
				t.Fatalf("tx.State() = %q, want %q", got, want)
			}
		})
	})

	t.Run("rfc4320 rejects restricted responses", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			remote := netip.MustParseAddrPort("55.55.55.55:5060")
			local := netip.MustParseAddrPort("33.33.33.33:5070")

			tp := newStubSrvTransp(false)
			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".policy-rfc4320", local, remote)

			tx, err := sip.NewNonInviteServerTransaction(req, tp,
				sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320})
			if err != nil {
				t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
			}
			assertBehavior(t, tx, sip.NonInviteServerTransactionBehaviorRFC4320)
			startSrvTx(t, tx)

			ctx := t.Context()
			assertRejected := func(sts sip.ResponseStatus) {
				t.Helper()

				before := tx.State()
				err := tx.SendResponse(ctx, newInRes(t, req, sts))
				if err == nil || tx.State() != before {
					t.Fatalf("tx.SendResponse(ctx, %v) changed transaction: err = %v, state = %q",
						sts, err, tx.State())
				}
				if !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
					t.Fatalf("tx.SendResponse(ctx, %v) error = %v, want %v",
						sts, err, sip.ErrTransactionActionNotAllowed)
				}
				if len(tp.sendResCalls) != 0 {
					t.Fatalf("rejected %v response reached transport", sts)
				}
				if res := tx.LastResponse(); res != nil {
					t.Fatalf("rejected %v response cached as last response %v", sts, res.Status())
				}
			}

			// RFC 4320: no 101-199 provisional, no 408, and no early 100 over
			// unreliable transport.
			assertRejected(sip.ResponseStatusTrying)
			assertRejected(sip.ResponseStatusRinging)
			assertRejected(sip.ResponseStatusRequestTimeout)

			// The automatic delayed Trying is dispatched once the deadline
			// passes even over unreliable transport.
			time.Sleep(4 * time.Second)
			if call := tp.waitSendRes(t); call.res.Status() != sip.ResponseStatusTrying {
				t.Fatalf("automatic response status = %v, want %v", call.res.Status(), sip.ResponseStatusTrying)
			}
			if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
				t.Fatalf("tx.State() = %q, want %q", got, want)
			}

			// A 100 Trying sent after the delayed-Trying deadline is eligible
			// even over unreliable transport.
			if err := tx.SendResponse(ctx, newInRes(t, req, sip.ResponseStatusTrying)); err != nil {
				t.Fatalf("tx.SendResponse(ctx, 100) after deadline error = %v, want nil", err)
			}
			if call := tp.waitSendRes(t); call.res.Status() != sip.ResponseStatusTrying {
				t.Fatalf("sent response status = %v, want %v", call.res.Status(), sip.ResponseStatusTrying)
			}
			if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
				t.Fatalf("tx.State() = %q, want %q", got, want)
			}

			// A valid final response is unaffected.
			if err := tx.SendResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
				t.Fatalf("tx.SendResponse(ctx, 200) error = %v, want nil", err)
			}
			if call := tp.waitSendRes(t); call.res.Status() != sip.ResponseStatusOK {
				t.Fatalf("sent response status = %v, want %v", call.res.Status(), sip.ResponseStatusOK)
			}
			if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
				t.Fatalf("tx.State() = %q, want %q", got, want)
			}
		})
	})

	t.Run("rfc4320 permits early 100 on reliable transport", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			remote := netip.MustParseAddrPort("55.55.55.55:5060")
			local := netip.MustParseAddrPort("33.33.33.33:5070")

			tp := newStubSrvTransp(true)
			req := newInNonInviteReq(t, "TCP", sip.MagicCookie+".policy-rfc4320-tcp", local, remote)

			tx, err := sip.NewNonInviteServerTransaction(req, tp,
				sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320})
			if err != nil {
				t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
			}
			startSrvTx(t, tx)

			ctx := t.Context()
			if err := tx.SendResponse(ctx, newInRes(t, req, sip.ResponseStatusTrying)); err != nil {
				t.Fatalf("tx.SendResponse(ctx, 100) error = %v, want nil", err)
			}
			if call := tp.waitSendRes(t); call.res.Status() != sip.ResponseStatusTrying {
				t.Fatalf("sent response status = %v, want %v", call.res.Status(), sip.ResponseStatusTrying)
			}
			if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
				t.Fatalf("tx.State() = %q, want %q", got, want)
			}
		})
	})

	t.Run("invalid behavior rejected at construction", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			remote := netip.MustParseAddrPort("55.55.55.55:5060")
			local := netip.MustParseAddrPort("33.33.33.33:5070")

			tp := newStubSrvTransp(false)
			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".policy-invalid", local, remote)

			if _, err := sip.NewNonInviteServerTransaction(req, tp,
				sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehavior(0xff)}); err == nil {
				t.Fatal("sip.NewNonInviteServerTransaction() with invalid behavior error = nil, want error")
			}
		})
	})

	t.Run("provider reports mode pre-start and on dormant restore", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			remote := netip.MustParseAddrPort("55.55.55.55:5060")
			local := netip.MustParseAddrPort("33.33.33.33:5070")

			tp := newStubSrvTransp(false)
			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".policy-provider", local, remote)

			tx, err := sip.NewNonInviteServerTransaction(req, tp,
				sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320})
			if err != nil {
				t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
			}

			// The selected mode is readable before Start.
			assertBehavior(t, tx, sip.NonInviteServerTransactionBehaviorRFC4320)
			startSrvTx(t, tx)

			if err := tx.SendResponse(t.Context(), newInRes(t, req, sip.ResponseStatusOK)); err != nil {
				t.Fatalf("tx.SendResponse(ctx, 200) error = %v, want nil", err)
			}
			snap := mustSrvTxSnapshot(t, tx)

			restoredTP := newStubSrvTransp(false)
			restored, err := sip.RestoreNonInviteServerTransaction(snap, restoredTP,
				sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320})
			if err != nil {
				t.Fatalf("sip.RestoreNonInviteServerTransaction() error = %v, want nil", err)
			}

			// A dormant restored transaction reports its mode without Snapshot,
			// which refuses nonterminal dormant transactions.
			assertBehavior(t, restored, sip.NonInviteServerTransactionBehaviorRFC4320)
		})
	})

	t.Run("shared option does not alter invite transactions", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			remote := netip.MustParseAddrPort("55.55.55.55:5060")
			local := netip.MustParseAddrPort("33.33.33.33:5070")

			tp := newStubSrvTransp(false)
			req := newInInviteReq(t, "UDP", sip.MagicCookie+".policy-invite", local, remote)

			tx, err := sip.NewInviteServerTransaction(req, tp,
				sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320})
			if err != nil {
				t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
			}
			startSrvTx(t, tx)

			if err := tx.SendResponse(t.Context(), newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("tx.SendResponse(ctx, 180) on INVITE error = %v, want nil", err)
			}
			if call := tp.waitSendRes(t); call.res.Status() != sip.ResponseStatusRinging {
				t.Fatalf("sent response status = %v, want %v", call.res.Status(), sip.ResponseStatusRinging)
			}
		})
	})
}

func TestNonInviteServerTransaction_DelayedTrying(t *testing.T) {
	t.Parallel()

	t.Run("default unreliable timing sends one 100 at the deadline", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			remote := netip.MustParseAddrPort("55.55.55.55:5060")
			local := netip.MustParseAddrPort("33.33.33.33:5070")

			tp := newStubSrvTransp(false)
			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".delayed-trying", local, remote)

			tx, err := sip.NewNonInviteServerTransaction(req, tp,
				sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320})
			if err != nil {
				t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
			}
			startSrvTx(t, tx)

			// The default backoff sums to 3.5s: nothing is sent before the
			// deadline, not even an implicit provisional response.
			time.Sleep(3500*time.Millisecond - time.Nanosecond)
			if got := len(tp.sendResCalls); got != 0 {
				t.Fatalf("sent responses before the deadline = %d, want 0", got)
			}
			if got, want := tx.State(), sip.TransactionStateTrying; got != want {
				t.Fatalf("tx.State() = %q, want %q", got, want)
			}

			// The timer fires at the deadline and dispatches exactly one 100.
			time.Sleep(time.Millisecond)
			call := tp.waitSendRes(t)
			if got, want := call.res.Status(), sip.ResponseStatusTrying; got != want {
				t.Fatalf("sent response status = %v, want %v", got, want)
			}
			if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
				t.Fatalf("tx.State() = %q, want %q", got, want)
			}

			// A retransmitted request reuses the cached 100; the timer does
			// not re-fire or manufacture a new provisional response.
			if err := tx.RecvRequest(t.Context(), req); err != nil {
				t.Fatalf("tx.RecvRequest(ctx, request) error = %v, want nil", err)
			}
			call = tp.waitSendRes(t)
			if got, want := call.res.Status(), sip.ResponseStatusTrying; got != want {
				t.Fatalf("retransmitted response status = %v, want %v", got, want)
			}

			time.Sleep(time.Second)
			if got := len(tp.sendResCalls); got != 0 {
				t.Fatalf("extra responses after the cached retransmission = %d, want 0", got)
			}
		})
	})

	t.Run("non-default timing derives the deadline from backoff", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			remote := netip.MustParseAddrPort("55.55.55.55:5060")
			local := netip.MustParseAddrPort("33.33.33.33:5070")

			tp := newStubSrvTransp(false)
			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".delayed-trying-timing", local, remote)

			// T1=1s, T2=4s accumulates 1s + 2s, so Trying is eligible at 3s.
			tx, err := sip.NewNonInviteServerTransaction(req, tp, sip.ServerTransactionOptions{
				Timing:                             sip.TimingConfig{T1: time.Second, T2: 4 * time.Second},
				NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320,
			})
			if err != nil {
				t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
			}
			startSrvTx(t, tx)

			time.Sleep(3*time.Second - time.Nanosecond)
			if got := len(tp.sendResCalls); got != 0 {
				t.Fatalf("sent responses before the deadline = %d, want 0", got)
			}

			time.Sleep(time.Millisecond)
			call := tp.waitSendRes(t)
			if got, want := call.res.Status(), sip.ResponseStatusTrying; got != want {
				t.Fatalf("sent response status = %v, want %v", got, want)
			}
			if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
				t.Fatalf("tx.State() = %q, want %q", got, want)
			}
		})
	})

	t.Run("t1 reaching t2 makes trying immediately eligible", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			remote := netip.MustParseAddrPort("55.55.55.55:5060")
			local := netip.MustParseAddrPort("33.33.33.33:5070")

			tp := newStubSrvTransp(false)
			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".delayed-trying-instant", local, remote)

			tx, err := sip.NewNonInviteServerTransaction(req, tp, sip.ServerTransactionOptions{
				Timing:                             sip.TimingConfig{T1: 4 * time.Second, T2: 4 * time.Second},
				NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320,
			})
			if err != nil {
				t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
			}
			startSrvTx(t, tx)

			call := tp.waitSendRes(t)
			if got, want := call.res.Status(), sip.ResponseStatusTrying; got != want {
				t.Fatalf("sent response status = %v, want %v", got, want)
			}
			if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
				t.Fatalf("tx.State() = %q, want %q", got, want)
			}
		})
	})

	t.Run("reliable explicit 100 suppresses the duplicate automatic one", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			remote := netip.MustParseAddrPort("55.55.55.55:5060")
			local := netip.MustParseAddrPort("33.33.33.33:5070")

			tp := newStubSrvTransp(true)
			req := newInNonInviteReq(t, "TCP", sip.MagicCookie+".delayed-trying-tcp", local, remote)

			tx, err := sip.NewNonInviteServerTransaction(req, tp,
				sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320})
			if err != nil {
				t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
			}
			startSrvTx(t, tx)

			// Reliable transport permits an explicit 100 before the deadline;
			// the automatic timer then emits no duplicate initial response.
			if err := tx.SendResponse(t.Context(), newInRes(t, req, sip.ResponseStatusTrying)); err != nil {
				t.Fatalf("tx.SendResponse(ctx, 100) error = %v, want nil", err)
			}
			call := tp.waitSendRes(t)
			if got, want := call.res.Status(), sip.ResponseStatusTrying; got != want {
				t.Fatalf("sent response status = %v, want %v", got, want)
			}
			if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
				t.Fatalf("tx.State() = %q, want %q", got, want)
			}

			time.Sleep(4 * time.Second)
			if got := len(tp.sendResCalls); got != 0 {
				t.Fatalf("extra responses past the deadline = %d, want 0", got)
			}
		})
	})

	t.Run("rejected early 100 does not suppress the automatic one", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			remote := netip.MustParseAddrPort("55.55.55.55:5060")
			local := netip.MustParseAddrPort("33.33.33.33:5070")

			tp := newStubSrvTransp(false)
			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".delayed-trying-rejected", local, remote)

			tx, err := sip.NewNonInviteServerTransaction(req, tp,
				sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320})
			if err != nil {
				t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
			}
			startSrvTx(t, tx)

			// An explicit 100 on unreliable transport before the deadline
			// fails without transmission or suppressing the automatic one.
			if err := tx.Respond(t.Context(), sip.ResponseStatusTrying); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
				t.Fatalf("tx.Respond(ctx, 100) error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
			}
			if got := len(tp.sendResCalls); got != 0 {
				t.Fatalf("sent responses after rejected 100 = %d, want 0", got)
			}

			time.Sleep(3500*time.Millisecond + time.Millisecond)
			call := tp.waitSendRes(t)
			if got, want := call.res.Status(), sip.ResponseStatusTrying; got != want {
				t.Fatalf("sent response status = %v, want %v", got, want)
			}
			if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
				t.Fatalf("tx.State() = %q, want %q", got, want)
			}
		})
	})

	t.Run("final response prevents a post-final 100", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			remote := netip.MustParseAddrPort("55.55.55.55:5060")
			local := netip.MustParseAddrPort("33.33.33.33:5070")

			tp := newStubSrvTransp(false)
			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".delayed-trying-final", local, remote)

			tx, err := sip.NewNonInviteServerTransaction(req, tp,
				sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320})
			if err != nil {
				t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
			}
			startSrvTx(t, tx)

			// A final response sent just before the deadline wins the race;
			// the expired timer never emits a provisional after the final.
			time.Sleep(3500*time.Millisecond - time.Millisecond)
			if err := tx.Respond(t.Context(), sip.ResponseStatusOK); err != nil {
				t.Fatalf("tx.Respond(ctx, 200) error = %v, want nil", err)
			}
			call := tp.waitSendRes(t)
			if got, want := call.res.Status(), sip.ResponseStatusOK; got != want {
				t.Fatalf("sent response status = %v, want %v", got, want)
			}
			if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
				t.Fatalf("tx.State() = %q, want %q", got, want)
			}

			time.Sleep(10 * time.Millisecond)
			if got := len(tp.sendResCalls); got != 0 {
				t.Fatalf("responses sent after the final one = %d, want 0", got)
			}
		})
	})

	t.Run("legacy mode arms no timer", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			remote := netip.MustParseAddrPort("55.55.55.55:5060")
			local := netip.MustParseAddrPort("33.33.33.33:5070")

			tp := newStubSrvTransp(false)
			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".delayed-trying-legacy", local, remote)

			tx, err := sip.NewNonInviteServerTransaction(req, tp)
			if err != nil {
				t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
			}
			startSrvTx(t, tx)

			time.Sleep(4 * time.Second)
			if got := len(tp.sendResCalls); got != 0 {
				t.Fatalf("sent responses in legacy mode = %d, want 0", got)
			}
			if got, want := tx.State(), sip.TransactionStateTrying; got != want {
				t.Fatalf("tx.State() = %q, want %q", got, want)
			}
		})
	})

	t.Run("termination stops the timer", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			remote := netip.MustParseAddrPort("55.55.55.55:5060")
			local := netip.MustParseAddrPort("33.33.33.33:5070")

			tp := newStubSrvTransp(false)
			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".delayed-trying-terminate", local, remote)

			tx, err := sip.NewNonInviteServerTransaction(req, tp,
				sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320})
			if err != nil {
				t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
			}
			startSrvTx(t, tx)

			if err := tx.Terminate(t.Context(), nil); err != nil {
				t.Fatalf("tx.Terminate() error = %v, want nil", err)
			}

			time.Sleep(4 * time.Second)
			if got := len(tp.sendResCalls); got != 0 {
				t.Fatalf("sent responses after termination = %d, want 0", got)
			}
		})
	})
}

func TestNonInviteServerTransaction_RecvRequestPreservesTimers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		behavior sip.NonInviteServerTransactionBehavior
	}{
		{name: "legacy", behavior: sip.NonInviteServerTransactionBehaviorLegacy},
		{name: "rfc4320", behavior: sip.NonInviteServerTransactionBehaviorRFC4320},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				remote := netip.MustParseAddrPort("55.55.55.55:5060")
				local := netip.MustParseAddrPort("33.33.33.33:5070")

				tp := newStubSrvTransp(false)
				req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".retransmit-timers-"+tt.name, local, remote)

				tx, err := sip.NewNonInviteServerTransaction(req, tp,
					sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: tt.behavior})
				if err != nil {
					t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
				}
				startSrvTx(t, tx)

				ctx := t.Context()

				// In Trying a request retransmission is absorbed without a
				// resend and without restarting the pending Trying deadline.
				trying := mustSrvTxSnapshot(t, tx)
				var tryingDeadline time.Time
				switch tt.behavior {
				case sip.NonInviteServerTransactionBehaviorRFC4320:
					if trying.TimerTrying == nil {
						t.Fatal("snapshot TimerTrying = nil, want pending trying deadline")
					}
					tryingDeadline = trying.TimerTrying.StartTime.Add(trying.TimerTrying.Duration)
				default:
					if trying.TimerTrying != nil {
						t.Fatalf("snapshot TimerTrying = %+v, want nil", trying.TimerTrying)
					}
				}

				if err := tx.RecvRequest(ctx, req); err != nil {
					t.Fatalf("tx.RecvRequest(ctx, request) error = %v, want nil", err)
				}
				tp.ensureNoSendRes(t)

				retrying := mustSrvTxSnapshot(t, tx)
				if got, want := retrying.State, sip.TransactionStateTrying; got != want {
					t.Fatalf("snapshot state after retransmission = %q, want %q", got, want)
				}
				switch tt.behavior {
				case sip.NonInviteServerTransactionBehaviorRFC4320:
					if retrying.TimerTrying == nil {
						t.Fatal("snapshot TimerTrying after retransmission = nil, want pending trying deadline")
					}
					if got := retrying.TimerTrying.StartTime.Add(retrying.TimerTrying.Duration); !got.Equal(tryingDeadline) {
						t.Fatalf("TimerTrying deadline after retransmission = %v, want unchanged %v",
							got, tryingDeadline)
					}
				default:
					if retrying.TimerTrying != nil {
						t.Fatalf("snapshot TimerTrying after retransmission = %+v, want nil", retrying.TimerTrying)
					}
				}

				// In Completed a request retransmission resends the cached
				// final response without restarting Timer J.
				if err := tx.Respond(ctx, sip.ResponseStatusOK); err != nil {
					t.Fatalf("tx.Respond(ctx, 200) error = %v, want nil", err)
				}
				call := tp.waitSendRes(t)
				if got, want := call.res.Status(), sip.ResponseStatusOK; got != want {
					t.Fatalf("final response status = %v, want %v", got, want)
				}

				completed := mustSrvTxSnapshot(t, tx)
				if completed.TimerJ == nil {
					t.Fatal("snapshot TimerJ = nil, want armed")
				}
				tmrJDeadline := completed.TimerJ.StartTime.Add(completed.TimerJ.Duration)

				if err := tx.RecvRequest(ctx, req); err != nil {
					t.Fatalf("tx.RecvRequest(ctx, request) error = %v, want nil", err)
				}
				call = tp.waitSendRes(t)
				if got, want := call.res.Status(), sip.ResponseStatusOK; got != want {
					t.Fatalf("retransmitted response status = %v, want cached final %v", got, want)
				}

				resend := mustSrvTxSnapshot(t, tx)
				if resend.TimerJ == nil {
					t.Fatal("snapshot TimerJ after retransmission = nil, want armed")
				}
				if got := resend.TimerJ.StartTime.Add(resend.TimerJ.Duration); !got.Equal(tmrJDeadline) {
					t.Fatalf("TimerJ deadline after retransmission = %v, want unchanged %v", got, tmrJDeadline)
				}
			})
		})
	}
}

func TestNonInviteServerTransaction_SnapshotNotifications(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig

		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5060")

		tp := newStubSrvTransp(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".snap-ntf", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp, sip.ServerTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}

		rec := &srvTxSnapRecorder{}

		// reentrant Snapshot/Started calls from a handler must not deadlock
		var snapErr error
		var startedFlag bool
		tx.BindSnapshotHandler(sip.ServerTransactionSnapshotHandlerFunc(func(hdlrCtx context.Context, snap *sip.ServerTransactionSnapshot) {
			rec.handler()(hdlrCtx, snap)
			_, snapErr = tx.Snapshot()
			startedFlag = tx.Started()
		}))

		if got := rec.len(); got != 0 {
			t.Fatalf("snapshot notifications before Start = %d, want 0", got)
		}

		startSrvTx(t, tx)

		if got := rec.len(); got != 1 {
			t.Fatalf("snapshot notifications after Start = %d, want 1", got)
		}
		if snapErr != nil {
			t.Fatalf("reentrant tx.Snapshot() error = %v, want nil", snapErr)
		}
		if !startedFlag {
			t.Fatal("reentrant tx.Started() = false, want true")
		}
		if ev := rec.at(0); ev.State != sip.TransactionStateTrying || ev.TimerJ != nil || ev.LastResponse != nil {
			t.Fatalf("first snapshot = %+v, want Trying without timers", ev)
		}

		if err := tx.Respond(ctx, sip.ResponseStatusRinging); err != nil {
			t.Fatalf("tx.Respond(ctx, 180) error = %v, want nil", err)
		}
		if got := rec.len(); got != 2 {
			t.Fatalf("snapshot notifications after 180 = %d, want 2", got)
		}
		if ev := rec.at(1); ev.State != sip.TransactionStateProceeding ||
			ev.LastResponse == nil || ev.LastResponse.Status() != sip.ResponseStatusRinging {
			t.Fatalf("second snapshot = %+v, want Proceeding with 180", ev)
		}

		if err := tx.Respond(ctx, sip.ResponseStatusOK); err != nil {
			t.Fatalf("tx.Respond(ctx, 200) error = %v, want nil", err)
		}
		if got := rec.len(); got != 3 {
			t.Fatalf("snapshot notifications after 200 = %d, want 3", got)
		}
		if ev := rec.at(2); ev.State != sip.TransactionStateCompleted ||
			ev.LastResponse == nil || ev.LastResponse.Status() != sip.ResponseStatusOK ||
			ev.TimerJ == nil {
			t.Fatalf("third snapshot = %+v, want Completed with 200 and timer J", ev)
		}

		waitForTxState(t, tx, sip.TransactionStateTerminated, timing.TimeJ()+200*time.Millisecond)
		if got := rec.len(); got != 4 {
			t.Fatalf("snapshot notifications after timer J = %d, want 4", got)
		}
		if ev := rec.at(3); ev.State != sip.TransactionStateTerminated || ev.TimerJ != nil {
			t.Fatalf("terminal snapshot = %+v, want Terminated with all timers cleaned", ev)
		}

		// every published snapshot must be restorable
		for i := range rec.len() {
			if _, err := sip.RestoreNonInviteServerTransaction(rec.at(i), newStubSrvTransp(false)); err != nil {
				t.Fatalf("sip.RestoreNonInviteServerTransaction(snapshot[%d]) error = %v, want nil", i, err)
			}
		}
	})
}

// nonInviteSrvTxBehaviorOf reads the frozen non-INVITE profile through the
// optional provider interface; it is the only mode check allowed on a
// dormant restored transaction because Snapshot refuses nonterminal
// dormant transactions.
func nonInviteSrvTxBehaviorOf(tb testing.TB, tx sip.ServerTransaction) sip.NonInviteServerTransactionBehavior {
	tb.Helper()

	p, ok := tx.(sip.NonInviteServerTransactionBehaviorProvider)
	if !ok {
		tb.Fatal("server transaction does not implement sip.NonInviteServerTransactionBehaviorProvider")
	}
	return p.NonInviteServerTransactionBehavior()
}

func TestNonInviteServerTransaction_RestoreTrying(t *testing.T) {
	t.Parallel()

	newRFC4320Tx := func(t *testing.T, branch string, tp *stubSrvTransp) (*sip.NonInviteServerTransaction, *sip.RequestEnvelope) {
		t.Helper()

		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5070")
		req := newInNonInviteReq(t, "UDP", branch, local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp,
			sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320})
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}
		return tx, req
	}

	t.Run("json round-trip preserves trying deadline and mode", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			tp := newStubSrvTransp(false)
			tx, _ := newRFC4320Tx(t, sip.MagicCookie+".snap-trying-json", tp)

			startedAt := time.Now()
			startSrvTx(t, tx)

			snap := mustSrvTxSnapshot(t, tx)
			if got, want := snap.NonInviteServerTransactionBehavior, sip.NonInviteServerTransactionBehaviorRFC4320; got != want {
				t.Fatalf("snapshot NonInviteServerTransactionBehavior = %v, want %v", got, want)
			}
			if snap.TimerTrying == nil {
				t.Fatal("snapshot TimerTrying = nil, want pending trying deadline")
			}
			if got, want := snap.TimerTrying.State, timeutil.TimerStateRunning; got != want {
				t.Fatalf("snapshot TimerTrying.State = %q, want %q", got, want)
			}

			// The deadline is the absolute delayed-Trying eligibility instant
			// computed at Start (default backoff 0.5s + 1s + 2s = 3.5s).
			deadline := snap.TimerTrying.StartTime.Add(snap.TimerTrying.Duration)
			if want := startedAt.Add(3500 * time.Millisecond); !deadline.Equal(want) {
				t.Fatalf("trying deadline = %v, want %v", deadline, want)
			}

			// Clone is an independent copy: mutating its timer must not leak
			// into the stored snapshot.
			clone := snap.Clone()
			clone.TimerTrying.Duration = time.Second
			if snap.TimerTrying.Duration == time.Second {
				t.Fatal("mutating the clone changed the stored TimerTrying")
			}
			if diff := cmp.Diff(snap, snap.Clone()); diff != "" {
				t.Fatalf("snapshot clone mismatch (-want +got):\n%s", diff)
			}

			data, err := json.Marshal(snap)
			if err != nil {
				t.Fatalf("json.Marshal(snapshot) error = %v, want nil", err)
			}

			var decoded sip.ServerTransactionSnapshot
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("json.Unmarshal(snapshot) error = %v, want nil", err)
			}
			if diff := cmp.Diff(snap.TimerTrying, decoded.TimerTrying); diff != "" {
				t.Fatalf("decoded TimerTrying mismatch (-want +got):\n%s", diff)
			}
			if got, want := decoded.NonInviteServerTransactionBehavior, sip.NonInviteServerTransactionBehaviorRFC4320; got != want {
				t.Fatalf("decoded NonInviteServerTransactionBehavior = %v, want %v", got, want)
			}

			// A serialized clone and the decoded snapshot must produce the
			// same wire form as the original. The decoded form is compared as
			// parsed JSON because decoding turns typed metadata values into
			// maps whose members re-marshal in sorted order.
			cloneData, err := json.Marshal(snap.Clone())
			if err != nil {
				t.Fatalf("json.Marshal(clone) error = %v, want nil", err)
			}
			if !slices.Equal(cloneData, data) {
				t.Fatal("json.Marshal(clone) differs from the original snapshot encoding")
			}

			roundData, err := json.Marshal(&decoded)
			if err != nil {
				t.Fatalf("json.Marshal(decoded) error = %v, want nil", err)
			}
			var origForm, roundForm map[string]any
			if err := json.Unmarshal(data, &origForm); err != nil {
				t.Fatalf("json.Unmarshal(original) error = %v, want nil", err)
			}
			if err := json.Unmarshal(roundData, &roundForm); err != nil {
				t.Fatalf("json.Unmarshal(round-trip) error = %v, want nil", err)
			}
			if diff := cmp.Diff(origForm, roundForm); diff != "" {
				t.Fatalf("json round-trip changed the snapshot (-want +got):\n%s", diff)
			}

			if err := tx.Terminate(t.Context(), nil); err != nil {
				t.Fatalf("tx.Terminate() error = %v, want nil", err)
			}
		})
	})

	t.Run("saved mode and absolute deadline win over creation options", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			origTP := newStubSrvTransp(false)
			tx, _ := newRFC4320Tx(t, sip.MagicCookie+".snap-trying-deadline", origTP)
			startSrvTx(t, tx)

			snap := mustSrvTxSnapshot(t, tx)
			if snap.TimerTrying == nil {
				t.Fatal("snapshot TimerTrying = nil, want pending trying deadline")
			}
			deadline := snap.TimerTrying.StartTime.Add(snap.TimerTrying.Duration)

			if err := tx.Terminate(ctx, nil); err != nil {
				t.Fatalf("tx.Terminate() error = %v, want nil", err)
			}

			// Part of the backoff elapses while the snapshot is dormant.
			time.Sleep(time.Second)

			restoredTP := newStubSrvTransp(false)
			restored, err := sip.RestoreNonInviteServerTransaction(snap, restoredTP,
				sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorLegacy})
			if err != nil {
				t.Fatalf("sip.RestoreNonInviteServerTransaction() error = %v, want nil", err)
			}

			// The saved RFC4320 mode wins over the Legacy creation option,
			// readable on the dormant transaction through the provider.
			if got, want := nonInviteSrvTxBehaviorOf(t, restored), sip.NonInviteServerTransactionBehaviorRFC4320; got != want {
				t.Fatalf("restored NonInviteServerTransactionBehavior = %v, want %v", got, want)
			}

			// Dormant restore emits no events and no sends.
			probe := &restoreEvtProbe{}
			probe.bind(t, restored)
			var startCalls int32
			restored.BindStartHandler(sip.TransactionStartHandlerFunc(func(context.Context) {
				startCalls++
			}))
			time.Sleep(500 * time.Millisecond)
			probe.assertQuiet(t, "while dormant")
			if got := len(restoredTP.sendResCalls); got != 0 {
				t.Fatalf("sent responses while dormant = %d, want 0", got)
			}
			if got := startCalls; got != 0 {
				t.Fatalf("start handler calls while dormant = %d, want 0", got)
			}
			if got, want := restored.State(), sip.TransactionStateTrying; got != want {
				t.Fatalf("restored.State() = %q, want %q", got, want)
			}

			startSrvTx(t, restored)

			// Activation publishes the same absolute deadline and the saved
			// mode; it is not recomputed from a fresh delay.
			rsnap, err := restored.Snapshot()
			if err != nil {
				t.Fatalf("restored.Snapshot() error = %v, want nil", err)
			}
			if got, want := rsnap.NonInviteServerTransactionBehavior, sip.NonInviteServerTransactionBehaviorRFC4320; got != want {
				t.Fatalf("restored snapshot NonInviteServerTransactionBehavior = %v, want %v", got, want)
			}
			if rsnap.TimerTrying == nil {
				t.Fatal("restored snapshot TimerTrying = nil, want pending trying deadline")
			}
			if got := rsnap.TimerTrying.StartTime.Add(rsnap.TimerTrying.Duration); !got.Equal(deadline) {
				t.Fatalf("restored trying deadline = %v, want unchanged %v", got, deadline)
			}

			// Nothing is sent before the preserved deadline; a recomputed
			// deadline would have shifted it by the dormant time.
			time.Sleep(time.Until(deadline) - time.Millisecond)
			if got := len(restoredTP.sendResCalls); got != 0 {
				t.Fatalf("sent responses before the preserved deadline = %d, want 0", got)
			}

			time.Sleep(2 * time.Millisecond)
			call := restoredTP.waitSendRes(t)
			if got, want := call.res.Status(), sip.ResponseStatusTrying; got != want {
				t.Fatalf("sent response status = %v, want %v", got, want)
			}
			if got, want := restored.State(), sip.TransactionStateProceeding; got != want {
				t.Fatalf("restored.State() = %q, want %q", got, want)
			}
			if got := len(restoredTP.sendResCalls); got != 0 {
				t.Fatalf("extra responses after the delayed 100 = %d, want 0", got)
			}
		})
	})

	t.Run("overdue deadline dispatches 100 immediately on activation", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			origTP := newStubSrvTransp(false)
			tx, _ := newRFC4320Tx(t, sip.MagicCookie+".snap-trying-overdue", origTP)
			startSrvTx(t, tx)

			snap := mustSrvTxSnapshot(t, tx)
			if snap.TimerTrying == nil {
				t.Fatal("snapshot TimerTrying = nil, want pending trying deadline")
			}
			deadline := snap.TimerTrying.StartTime.Add(snap.TimerTrying.Duration)

			if err := tx.Terminate(ctx, nil); err != nil {
				t.Fatalf("tx.Terminate() error = %v, want nil", err)
			}

			// The deadline passes while the snapshot is dormant.
			time.Sleep(time.Until(deadline) + time.Second)

			restoredTP := newStubSrvTransp(false)
			restored, err := sip.RestoreNonInviteServerTransaction(snap, restoredTP)
			if err != nil {
				t.Fatalf("sip.RestoreNonInviteServerTransaction() error = %v, want nil", err)
			}
			if got, want := nonInviteSrvTxBehaviorOf(t, restored), sip.NonInviteServerTransactionBehaviorRFC4320; got != want {
				t.Fatalf("restored NonInviteServerTransactionBehavior = %v, want %v", got, want)
			}

			// Still dormant: the overdue timer is restored without a callback,
			// so nothing fires before Start.
			time.Sleep(500 * time.Millisecond)
			if got := len(restoredTP.sendResCalls); got != 0 {
				t.Fatalf("sent responses while dormant = %d, want 0", got)
			}

			startSrvTx(t, restored)

			call := restoredTP.waitSendRes(t)
			if got, want := call.res.Status(), sip.ResponseStatusTrying; got != want {
				t.Fatalf("sent response status = %v, want %v", got, want)
			}
			if got, want := restored.State(), sip.TransactionStateProceeding; got != want {
				t.Fatalf("restored.State() = %q, want %q", got, want)
			}
		})
	})

	t.Run("sent responses are never replayed", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			origTP := newStubSrvTransp(false)
			tx, req := newRFC4320Tx(t, sip.MagicCookie+".snap-trying-replay", origTP)
			startSrvTx(t, tx)

			// The automatic delayed 100 is sent at the deadline; the snapshot
			// then carries the answered mode without a pending timer.
			time.Sleep(3500*time.Millisecond + time.Millisecond)
			call := origTP.waitSendRes(t)
			if got, want := call.res.Status(), sip.ResponseStatusTrying; got != want {
				t.Fatalf("automatic response status = %v, want %v", got, want)
			}

			snap := mustSrvTxSnapshot(t, tx)
			if got, want := snap.NonInviteServerTransactionBehavior, sip.NonInviteServerTransactionBehaviorRFC4320; got != want {
				t.Fatalf("snapshot NonInviteServerTransactionBehavior = %v, want %v", got, want)
			}
			if snap.TimerTrying != nil {
				t.Fatalf("snapshot TimerTrying = %+v, want nil after the 100", snap.TimerTrying)
			}
			if res := snap.LastResponse; res == nil || res.Status() != sip.ResponseStatusTrying {
				t.Fatalf("snapshot LastResponse = %v, want 100 Trying", res)
			}

			restoredTP := newStubSrvTransp(false)
			restored, err := sip.RestoreNonInviteServerTransaction(snap, restoredTP)
			if err != nil {
				t.Fatalf("sip.RestoreNonInviteServerTransaction() error = %v, want nil", err)
			}
			if got, want := nonInviteSrvTxBehaviorOf(t, restored), sip.NonInviteServerTransactionBehaviorRFC4320; got != want {
				t.Fatalf("restored NonInviteServerTransactionBehavior = %v, want %v", got, want)
			}

			startSrvTx(t, restored)
			if got, want := restored.State(), sip.TransactionStateProceeding; got != want {
				t.Fatalf("restored.State() = %q, want %q", got, want)
			}
			if res := restored.LastResponse(); res == nil || res.Status() != sip.ResponseStatusTrying {
				t.Fatalf("restored.LastResponse() = %v, want cached 100 Trying", res)
			}
			restoredTP.ensureNoSendRes(t)

			// A retransmission reuses the cached 100; that is retransmission,
			// not a replay of the restore.
			if err := restored.RecvRequest(ctx, req.Clone().(*sip.RequestEnvelope)); err != nil {
				t.Fatalf("restored.RecvRequest() error = %v, want nil", err)
			}
			call = restoredTP.waitSendRes(t)
			if got, want := call.res.Status(), sip.ResponseStatusTrying; got != want {
				t.Fatalf("retransmitted response status = %v, want %v", got, want)
			}

			// The answered final response is never replayed either, and the
			// completed mode is not downgraded by completion.
			if err := tx.SendResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
				t.Fatalf("tx.SendResponse(ctx, 200) error = %v, want nil", err)
			}
			call = origTP.waitSendRes(t)
			if got, want := call.res.Status(), sip.ResponseStatusOK; got != want {
				t.Fatalf("final response status = %v, want %v", got, want)
			}

			finalSnap := mustSrvTxSnapshot(t, tx)
			if got, want := finalSnap.NonInviteServerTransactionBehavior, sip.NonInviteServerTransactionBehaviorRFC4320; got != want {
				t.Fatalf("completed snapshot NonInviteServerTransactionBehavior = %v, want %v", got, want)
			}
			if finalSnap.TimerTrying != nil {
				t.Fatalf("completed snapshot TimerTrying = %+v, want nil", finalSnap.TimerTrying)
			}

			finalTP := newStubSrvTransp(false)
			finalRestored, err := sip.RestoreNonInviteServerTransaction(finalSnap, finalTP)
			if err != nil {
				t.Fatalf("sip.RestoreNonInviteServerTransaction() error = %v, want nil", err)
			}
			if got, want := nonInviteSrvTxBehaviorOf(t, finalRestored), sip.NonInviteServerTransactionBehaviorRFC4320; got != want {
				t.Fatalf("restored NonInviteServerTransactionBehavior = %v, want %v", got, want)
			}

			startSrvTx(t, finalRestored)
			if got, want := finalRestored.State(), sip.TransactionStateCompleted; got != want {
				t.Fatalf("final restored.State() = %q, want %q", got, want)
			}
			finalTP.ensureNoSendRes(t)
		})
	})

	t.Run("missing mode field restores as legacy", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			remote := netip.MustParseAddrPort("55.55.55.55:5060")
			local := netip.MustParseAddrPort("33.33.33.33:5070")

			// An old snapshot has no mode field at all: a legacy transaction
			// snapshot in Trying is indistinguishable from it.
			tp := newStubSrvTransp(false)
			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".snap-legacy", local, remote)
			tx, err := sip.NewNonInviteServerTransaction(req, tp)
			if err != nil {
				t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
			}
			startSrvTx(t, tx)
			snap := mustSrvTxSnapshot(t, tx)

			data, err := json.Marshal(snap)
			if err != nil {
				t.Fatalf("json.Marshal(snapshot) error = %v, want nil", err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatalf("json.Unmarshal(snapshot) error = %v, want nil", err)
			}
			if _, ok := fields["non_invite_server_transaction_behavior"]; ok {
				t.Fatal("legacy snapshot must not serialize non_invite_server_transaction_behavior")
			}
			if _, ok := fields["timer_trying"]; ok {
				t.Fatal("legacy snapshot must not serialize timer_trying")
			}

			if err := tx.Terminate(ctx, nil); err != nil {
				t.Fatalf("tx.Terminate() error = %v, want nil", err)
			}

			// The missing field restores as Legacy even when the creation
			// options select RFC4320: no implicit enablement, no replay.
			restoredTP := newStubSrvTransp(false)
			restored, err := sip.RestoreNonInviteServerTransaction(snap, restoredTP,
				sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320})
			if err != nil {
				t.Fatalf("sip.RestoreNonInviteServerTransaction() error = %v, want nil", err)
			}
			if got, want := nonInviteSrvTxBehaviorOf(t, restored), sip.NonInviteServerTransactionBehaviorLegacy; got != want {
				t.Fatalf("restored NonInviteServerTransactionBehavior = %v, want %v", got, want)
			}

			startSrvTx(t, restored)

			restoredSnap, err := restored.Snapshot()
			if err != nil {
				t.Fatalf("restored.Snapshot() error = %v, want nil", err)
			}
			if got, want := restoredSnap.NonInviteServerTransactionBehavior, sip.NonInviteServerTransactionBehaviorLegacy; got != want {
				t.Fatalf("behavior=%v, want %v", got, want)
			}

			// No automatic delayed Trying is armed for the legacy mode.
			time.Sleep(4 * time.Second)
			if got := len(restoredTP.sendResCalls); got != 0 {
				t.Fatalf("sent responses in restored legacy mode = %d, want 0", got)
			}
			if got, want := restored.State(), sip.TransactionStateTrying; got != want {
				t.Fatalf("restored.State() = %q, want %q", got, want)
			}

			// The same applies at the wire level: stripping the field from an
			// RFC4320 snapshot restores as Legacy.
			rfcTP := newStubSrvTransp(false)
			rfcTx, rfcReq := newRFC4320Tx(t, sip.MagicCookie+".snap-strip-mode", rfcTP)
			startSrvTx(t, rfcTx)
			if err := rfcTx.SendResponse(ctx, newInRes(t, rfcReq, sip.ResponseStatusOK)); err != nil {
				t.Fatalf("tx.SendResponse(ctx, 200) error = %v, want nil", err)
			}
			rfcTP.waitSendRes(t)

			rfcData, err := json.Marshal(mustSrvTxSnapshot(t, rfcTx))
			if err != nil {
				t.Fatalf("json.Marshal(snapshot) error = %v, want nil", err)
			}
			var rfcFields map[string]json.RawMessage
			if err := json.Unmarshal(rfcData, &rfcFields); err != nil {
				t.Fatalf("json.Unmarshal(snapshot) error = %v, want nil", err)
			}
			delete(rfcFields, "non_invite_server_transaction_behavior")
			strippedData, err := json.Marshal(rfcFields)
			if err != nil {
				t.Fatalf("json.Marshal(stripped) error = %v, want nil", err)
			}
			var stripped sip.ServerTransactionSnapshot
			if err := json.Unmarshal(strippedData, &stripped); err != nil {
				t.Fatalf("json.Unmarshal(stripped) error = %v, want nil", err)
			}

			strippedRestored, err := sip.RestoreNonInviteServerTransaction(&stripped, newStubSrvTransp(false))
			if err != nil {
				t.Fatalf("sip.RestoreNonInviteServerTransaction() error = %v, want nil", err)
			}
			if got, want := nonInviteSrvTxBehaviorOf(t, strippedRestored), sip.NonInviteServerTransactionBehaviorLegacy; got != want {
				t.Fatalf("restored NonInviteServerTransactionBehavior = %v, want %v", got, want)
			}
		})
	})

	t.Run("malformed snapshots are rejected without registering", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			origTP := newStubSrvTransp(false)
			tx, _ := newRFC4320Tx(t, sip.MagicCookie+".snap-trying-invalid", origTP)
			startSrvTx(t, tx)

			base := mustSrvTxSnapshot(t, tx)
			if base.TimerTrying == nil {
				t.Fatal("snapshot TimerTrying = nil, want pending trying deadline")
			}
			if got, want := base.NonInviteServerTransactionBehavior, sip.NonInviteServerTransactionBehaviorRFC4320; got != want {
				t.Fatalf("snapshot NonInviteServerTransactionBehavior = %v, want %v", got, want)
			}

			tests := []struct {
				name       string
				mutate     func(snap *sip.ServerTransactionSnapshot)
				wantSentin error
			}{
				{
					"invalid mode",
					func(snap *sip.ServerTransactionSnapshot) {
						snap.NonInviteServerTransactionBehavior = sip.NonInviteServerTransactionBehavior(0xff)
					},
					sip.ErrInvalidTransactionSnapshot,
				},
				{
					"rfc4320 trying without timer",
					func(snap *sip.ServerTransactionSnapshot) {
						snap.TimerTrying = nil
					},
					sip.ErrInvalidTransactionSnapshot,
				},
				{
					"trying timer running in wrong state",
					func(snap *sip.ServerTransactionSnapshot) {
						snap.State = sip.TransactionStateProceeding
					},
					sip.ErrInvalidTransactionSnapshot,
				},
				{
					"malformed trying timer",
					func(snap *sip.ServerTransactionSnapshot) {
						snap.TimerTrying = &timeutil.TimerSnapshot{State: timeutil.TimerStateStopped}
					},
					nil,
				},
			}

			for _, tt := range tests {
				snap := *base
				tt.mutate(&snap)

				restoredTP := newStubSrvTransp(false)
				restored, err := sip.RestoreNonInviteServerTransaction(&snap, restoredTP)
				if err == nil {
					t.Fatalf("sip.RestoreNonInviteServerTransaction(%s) error = nil, want error", tt.name)
				}
				if restored != nil {
					t.Fatalf("sip.RestoreNonInviteServerTransaction(%s) = %v, want nil transaction", tt.name, restored)
				}
				if tt.wantSentin != nil && !errors.Is(err, tt.wantSentin) {
					t.Fatalf("sip.RestoreNonInviteServerTransaction(%s) error = %v, want %v", tt.name, err, tt.wantSentin)
				}
				// A rejected snapshot produces no transaction to register or
				// start, and nothing reaches the transport.
				restoredTP.ensureNoSendRes(t)
			}
		})
	})

	t.Run("terminal snapshots restore without starting", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			origTP := newStubSrvTransp(false)
			tx, _ := newRFC4320Tx(t, sip.MagicCookie+".snap-trying-terminated", origTP)
			startSrvTx(t, tx)
			if err := tx.Terminate(ctx, nil); err != nil {
				t.Fatalf("tx.Terminate() error = %v, want nil", err)
			}

			// Terminated in Trying: no response, no pending timer, but the
			// mode is still captured - completion never downgrades it.
			snap := mustSrvTxSnapshot(t, tx)
			if got, want := snap.NonInviteServerTransactionBehavior, sip.NonInviteServerTransactionBehaviorRFC4320; got != want {
				t.Fatalf("terminated snapshot NonInviteServerTransactionBehavior = %v, want %v", got, want)
			}
			if snap.TimerTrying != nil {
				t.Fatalf("terminated snapshot TimerTrying = %+v, want nil", snap.TimerTrying)
			}

			restoredTP := newStubSrvTransp(false)
			restored, err := sip.RestoreNonInviteServerTransaction(snap, restoredTP)
			if err != nil {
				t.Fatalf("sip.RestoreNonInviteServerTransaction() error = %v, want nil", err)
			}
			if got, want := restored.State(), sip.TransactionStateTerminated; got != want {
				t.Fatalf("restored.State() = %q, want %q", got, want)
			}
			if got, want := nonInviteSrvTxBehaviorOf(t, restored), sip.NonInviteServerTransactionBehaviorRFC4320; got != want {
				t.Fatalf("restored NonInviteServerTransactionBehavior = %v, want %v", got, want)
			}

			// A terminated snapshot rehydrates for inspection only.
			if err := restored.Start(ctx); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
				t.Fatalf("restored.Start() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
			}
			restoredTP.ensureNoSendRes(t)

			// A legacy terminated snapshot restores the same way.
			remote := netip.MustParseAddrPort("55.55.55.55:5060")
			local := netip.MustParseAddrPort("33.33.33.33:5070")
			legacyTP := newStubSrvTransp(false)
			legacyTx, err := sip.NewNonInviteServerTransaction(
				newInNonInviteReq(t, "UDP", sip.MagicCookie+".snap-legacy-terminated", local, remote),
				legacyTP,
			)
			if err != nil {
				t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
			}
			startSrvTx(t, legacyTx)
			if err := legacyTx.Terminate(ctx, nil); err != nil {
				t.Fatalf("legacyTx.Terminate() error = %v, want nil", err)
			}

			legacyRestored, err := sip.RestoreNonInviteServerTransaction(
				mustSrvTxSnapshot(t, legacyTx),
				newStubSrvTransp(false),
			)
			if err != nil {
				t.Fatalf("sip.RestoreNonInviteServerTransaction() error = %v, want nil", err)
			}
			if got, want := nonInviteSrvTxBehaviorOf(t, legacyRestored), sip.NonInviteServerTransactionBehaviorLegacy; got != want {
				t.Fatalf("restored NonInviteServerTransactionBehavior = %v, want %v", got, want)
			}
		})
	})
}
