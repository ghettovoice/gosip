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
				tp := newStubServerTransport(false)
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
				startServerTransaction(t, tx)

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
					beforeErr := mustServerSnapshot(t, tx)
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

				snap := mustServerSnapshot(t, tx)
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

				recovered := mustServerSnapshot(t, tx)
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
				waitForTransactState(t, tx, sip.TransactionStateTerminated, timeSkew)
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

			restoredTP := newStubServerTransport(false)
			if _, err := sip.RestoreNonInviteServerTransaction(&snap, restoredTP); err == nil {
				t.Fatalf("sip.RestoreNonInviteServerTransaction(%s) error = nil, want error", tt.name)
			}
			restoredTP.ensureNoSendRes(t)
		}
	})
}

func TestNonInviteServerTransaction_SnapshotNotifications(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var timing sip.TimingConfig

		ctx := t.Context()
		remote := netip.MustParseAddrPort("55.55.55.55:5060")
		local := netip.MustParseAddrPort("33.33.33.33:5060")

		tp := newStubServerTransport(false)
		req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".snap-ntf", local, remote)

		tx, err := sip.NewNonInviteServerTransaction(req, tp, sip.ServerTransactionOptions{Timing: timing})
		if err != nil {
			t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
		}

		rec := &srvSnapRecorder{}

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

		startServerTransaction(t, tx)

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

		waitForTransactState(t, tx, sip.TransactionStateTerminated, timing.TimeJ()+200*time.Millisecond)
		if got := rec.len(); got != 4 {
			t.Fatalf("snapshot notifications after timer J = %d, want 4", got)
		}
		if ev := rec.at(3); ev.State != sip.TransactionStateTerminated || ev.TimerJ != nil {
			t.Fatalf("terminal snapshot = %+v, want Terminated with all timers cleaned", ev)
		}

		// every published snapshot must be restorable
		for i := range rec.len() {
			if _, err := sip.RestoreNonInviteServerTransaction(rec.at(i), newStubServerTransport(false)); err != nil {
				t.Fatalf("sip.RestoreNonInviteServerTransaction(snapshot[%d]) error = %v, want nil", i, err)
			}
		}
	})
}
