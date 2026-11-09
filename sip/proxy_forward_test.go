package sip_test

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip"
)

// fwdStatuses returns the wire-observed response statuses in send order.
func fwdStatuses(ress []*sip.ResponseEnvelope) []int {
	sts := make([]int, 0, len(ress))
	for _, res := range ress {
		sts = append(sts, int(res.Status()))
	}
	return sts
}

// fwdClnTx loads the registered downstream client transaction of a
// captured outbound request.
func fwdClnTx(tb testing.TB, elm *sip.Element, outReq *sip.RequestEnvelope) sip.ClientTransaction {
	tb.Helper()

	key, err := sip.MakeClientTransactionKey(outReq)
	if err != nil {
		tb.Fatalf("sip.MakeClientTransactionKey(outReq) error = %v, want nil", err)
	}
	tx, ok := elm.LoadClientTransaction(key)
	if !ok {
		tb.Fatal("forwarding client transaction not registered")
	}
	return tx
}

// fwdSrvTxBehavior reports the non-INVITE behavior profile of the server
// transaction admitted for req.
func fwdSrvTxBehavior(tb testing.TB, elm *sip.Element, req *sip.RequestEnvelope) sip.NonInviteServerTransactionBehavior {
	tb.Helper()

	key, err := sip.MakeServerTransactionKey(req)
	if err != nil {
		tb.Fatalf("sip.MakeServerTransactionKey(req) error = %v, want nil", err)
	}
	tx, ok := elm.LoadServerTransaction(key)
	if !ok {
		tb.Fatal("admitted server transaction not registered")
	}
	provider, ok := tx.(sip.NonInviteServerTransactionBehaviorProvider)
	if !ok {
		tb.Fatalf("server transaction %T has no non-INVITE behavior provider", tx)
	}
	return provider.NonInviteServerTransactionBehavior()
}

// fwdIntercept admits one inbound request through the proxy interceptor.
func fwdIntercept(tb testing.TB, prx *sip.Proxy, req *sip.RequestEnvelope) {
	tb.Helper()

	next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil })
	if err := prx.InterceptInboundRequest(tb.Context(), next, req); err != nil {
		tb.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
	}
}

func fwdLitPlan() sip.ProxyTargetPlan {
	return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
		{
			{
				ID: "A",
				URI: &sip.URI{
					User: sip.MakeUserInfo("alice"),
					Addr: sip.MakeHostPortAddr("10.0.0.9", 5060),
				},
			},
		},
	}}
}

func TestProxy_StatefulForward(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	origBody := []byte("v=0\r\no=- 1 1 IN IP4 10.0.0.5\r\ns=-\r\n")

	fwdRoute := func(name string, txOpts sip.ServerTransactionOptions) *sip.ProxyRoute {
		return &sip.ProxyRoute{
			Name:                     name,
			ForwardMode:              sip.ForwardModeStateful,
			ServerTransactionOptions: txOpts,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
				return fwdLitPlan(), nil
			},
		}
	}
	fwdOpts := sip.ProxyOptions{ServedDomains: []string{"alice.voip.com"}}

	t.Run("two hundred forwarded upstream with body intact", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			elm, prx, tp := newConfiguredTestProxy(t, fwdRoute("forward-ok", sip.ServerTransactionOptions{}), fwdOpts)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".forward-ok", laddr, raddr)
			req.WithMessage(func(msg *sip.Request) { msg.Body = origBody })
			fwdIntercept(t, prx, req)
			synctest.Wait()

			sent := testutil.WaitResult(t, tp.sendReqCalls, "outbound request send").req
			clnTx := fwdClnTx(t, elm, sent)

			var resToTag string
			res := newInRes(t, sent, sip.ResponseStatusOK)
			res.WithMessage(func(msg *sip.Response) {
				msg.Body = origBody
				if to, ok := msg.Headers.To(); ok {
					resToTag, _ = to.Tag()
				}
			})
			if err := clnTx.RecvResponse(ctx, res); err != nil {
				t.Fatalf("clnTx.RecvResponse(200) error = %v, want nil", err)
			}
			synctest.Wait()

			upstream := drainPrxRess(tp)
			if diff := cmp.Diff([]int{200}, fwdStatuses(upstream)); diff != "" {
				t.Fatalf("responses (-want +got): %s", diff)
			}

			fwd := upstream[0]
			var gotBody []byte
			fwd.WithMessage(func(msg *sip.Response) { gotBody = msg.Body })
			if diff := cmp.Diff(origBody, gotBody); diff != "" {
				t.Fatalf("body (-want +got): %s", diff)
			}

			// Exactly one Via was removed from the forwarded copy and the
			// downstream dialog tag crossed upstream verbatim.
			var (
				viaCnt int
				toTag  string
			)
			fwd.WithMessage(func(msg *sip.Response) {
				for range msg.Headers.Vias() {
					viaCnt++
				}
				if to, ok := msg.Headers.To(); ok {
					toTag, _ = to.Tag()
				}
			})
			var inVias int
			req.WithMessage(func(msg *sip.Request) {
				for range msg.Headers.Vias() {
					inVias++
				}
			})
			if viaCnt != inVias {
				t.Fatalf("forwarded response Via count = %d, want %d (one proxy hop removed)", viaCnt, inVias)
			}
			if toTag != resToTag {
				t.Fatalf("forwarded response To tag = %q, want %q", toTag, resToTag)
			}
		})
	})

	t.Run("server transaction options pass through unchanged", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			suffix string
			opts   sip.ServerTransactionOptions
			want   sip.NonInviteServerTransactionBehavior
		}{
			{
				"legacy default",
				"legacy",
				sip.ServerTransactionOptions{},
				sip.NonInviteServerTransactionBehaviorLegacy,
			},
			{
				"explicit RFC 4320",
				"rfc4320",
				sip.ServerTransactionOptions{
					NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320,
				},
				sip.NonInviteServerTransactionBehaviorRFC4320,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				synctest.Test(t, func(t *testing.T) {
					elm, prx, _ := newConfiguredTestProxy(t, fwdRoute("opts-"+tt.suffix, tt.opts), fwdOpts)

					// The upstream transaction of an admitted non-INVITE
					// request receives the route options unchanged through
					// the factory seam.
					req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".opts-"+tt.suffix, laddr, raddr)
					fwdIntercept(t, prx, req)
					synctest.Wait()
					if got := fwdSrvTxBehavior(t, elm, req); got != tt.want {
						t.Fatalf("non-INVITE behavior = %v, want %v", got, tt.want)
					}

					// A matching CANCEL reuses the same seam: its own
					// non-INVITE server transaction inherits identical
					// options. The full matching-CANCEL flow lands later.
					inv := newInInviteReq(t, "UDP", sip.MagicCookie+".opts-"+tt.suffix+"-c", laddr, raddr)
					fwdIntercept(t, prx, inv)
					synctest.Wait()
					cancelReq, err := sip.NewCancelRequestEnvelope(inv)
					if err != nil {
						t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
					}
					fwdIntercept(t, prx, cancelReq)
					synctest.Wait()
					if got := fwdSrvTxBehavior(t, elm, cancelReq); got != tt.want {
						t.Fatalf("matching CANCEL non-INVITE behavior = %v, want %v", got, tt.want)
					}
				})
			})
		}
	})

	t.Run("route miss passes the request to the next receiver", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			// The only configured route claims requests for "bob"; the
			// inbound request targets "alice" and matches nothing.
			route := fwdRoute("miss", sip.ServerTransactionOptions{})
			route.MatchRequest = func(_ context.Context, req *sip.RequestEnvelope) bool {
				u, ok := req.URI().(*sip.URI)
				return ok && u.User.Username() == "bob"
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, fwdOpts)

			var nexted atomic.Int32
			req := newInInviteReq(t, "UDP", sip.MagicCookie+".forward-miss", laddr, raddr)
			next := sip.RequestReceiverFunc(func(_ context.Context, got *sip.RequestEnvelope) error {
				if got != req {
					t.Error("next receiver received a different request envelope")
				}
				nexted.Add(1)
				return nil
			})

			if err := prx.InterceptInboundRequest(ctx, next, req); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			if got := nexted.Load(); got != 1 {
				t.Fatalf("next receiver calls=%d, want 1", got)
			}
			if v, ok := req.Metadata().Get("sip.proxy_route"); ok {
				t.Fatalf("route metadata = (%v, %t), want absent", v, ok)
			}
			if got := countAllTxs(elm); got != 0 {
				t.Fatalf("transactions=%d, want 0 (no proxy-owned transactions)", got)
			}
			if got := len(drainPrxReqs(tp)); got != 0 {
				t.Fatalf("outbound sends=%d, want 0", got)
			}
			if got := len(drainPrxRess(tp)); got != 0 {
				t.Fatalf("upstream responses=%d, want 0", got)
			}
		})
	})
}

func TestProxy_ResponseSendOutcome(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		laddr := netip.MustParseAddrPort("192.168.1.100:5060")
		raddr := netip.MustParseAddrPort("10.0.0.5:5060")
		injErr := errors.ErrorWrap("upstream write failed")

		route := &sip.ProxyRoute{
			Name:        "send-outcome",
			ForwardMode: sip.ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
				return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
					{
						{
							ID:  "A",
							URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr("10.0.0.9", 5060)},
						},
						{
							ID:  "B",
							URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr("10.0.0.10", 5060)},
						},
					},
				}}, nil
			},
		}
		elm, prx, tp := newConfiguredTestProxy(t, route, sip.ProxyOptions{
			ServedDomains: []string{"alice.voip.com"},
		})

		var errCalls atomic.Int32
		var lastErr atomic.Value
		elm.BindErrorHandler(sip.ErrorHandlerFunc(func(_ context.Context, err error) {
			errCalls.Add(1)
			lastErr.Store(err)
		}))

		// The first upstream response write fails at the transport; later
		// writes succeed.
		tp.sendResHook = func(_ *sip.ResponseEnvelope, idx int) error {
			if idx == 0 {
				return injErr
			}
			return nil
		}

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".send-outcome", laddr, raddr)
		fwdIntercept(t, prx, req)
		synctest.Wait()

		sends := drainPrxReqs(tp)
		if got := len(sends); got != 2 {
			t.Fatalf("outbound request sends=%d, want 2", got)
		}
		txA := fwdClnTx(t, elm, sends[0])
		txB := fwdClnTx(t, elm, sends[1])

		// Branch A's success forwards but the wire write fails: the
		// transaction-level submission still completes, and the real
		// transport failure is reported exactly once through the element
		// error handlers.
		if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusOK)); err != nil {
			t.Fatalf("txA.RecvResponse(200) error = %v, want nil", err)
		}
		synctest.Wait()
		if got := errCalls.Load(); got != 1 {
			t.Fatalf("element error reports=%d, want 1", got)
		}
		if err, _ := lastErr.Load().(error); !errors.Is(err, injErr) {
			t.Fatalf("reported error=%v, want %v", err, injErr)
		}
		if got := len(drainPrxRess(tp)); got != 0 {
			t.Fatalf("recorded forwarded responses after failed write=%d, want 0", got)
		}

		// The unrelated pending branch was not cancelled: its success still
		// forwards and reaches the wire.
		if err := txB.RecvResponse(ctx, newInRes(t, sends[1], sip.ResponseStatusOK)); err != nil {
			t.Fatalf("txB.RecvResponse(200) error = %v, want nil", err)
		}
		synctest.Wait()
		if got := errCalls.Load(); got != 1 {
			t.Fatalf("element error reports=%d, want 1", got)
		}
		if diff := cmp.Diff([]int{200}, fwdStatuses(drainPrxRess(tp))); diff != "" {
			t.Fatalf("responses (-want +got): %s", diff)
		}
	})
}

func TestProxy_TransparentPreparationTransport(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	origBody := []byte("forwarded body")

	t.Run("forwarded response keeps dialog tag and body on both send paths", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := &sip.ProxyRoute{
				Name:        "preparation",
				ForwardMode: sip.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
					return fwdLitPlan(), nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, sip.ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			})

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".preparation", laddr, raddr)
			fwdIntercept(t, prx, req)
			synctest.Wait()

			sent := testutil.WaitResult(t, tp.sendReqCalls, "outbound request send").req
			clnTx := fwdClnTx(t, elm, sent)

			var downTag string
			res := newInRes(t, sent, sip.ResponseStatusOK)
			res.WithMessage(func(msg *sip.Response) {
				msg.Body = origBody
				if to, ok := msg.Headers.To(); ok {
					downTag, _ = to.Tag()
				}
			})
			if err := clnTx.RecvResponse(ctx, res); err != nil {
				t.Fatalf("clnTx.RecvResponse(200) error = %v, want nil", err)
			}
			synctest.Wait()

			// Transport send path: the forwarded copy crossed the outbound
			// interceptor chain and the wire endpoint untouched.
			fwd := testutil.WaitResult(t, tp.sendResCalls, "upstream response send").res
			assertTagAndBody := func(env *sip.ResponseEnvelope, what string) {
				t.Helper()
				var (
					toTag string
					body  []byte
				)
				env.WithMessage(func(msg *sip.Response) {
					if to, ok := msg.Headers.To(); ok {
						toTag, _ = to.Tag()
					}
					body = msg.Body
				})
				if toTag != downTag {
					t.Fatalf("%s To tag = %q, want %q", what, toTag, downTag)
				}
				if diff := cmp.Diff(origBody, body); diff != "" {
					t.Fatalf("%s body (-want +got): %s", what, diff)
				}
			}
			assertTagAndBody(fwd, "transport send")

			// Element send path: re-sending the same forwarded copy through
			// the element outbound response helper keeps tag and body too.
			var viaElm *sip.ResponseEnvelope
			sender := sip.ResponseSenderFunc(func(_ context.Context, res *sip.ResponseEnvelope, _ ...sip.SendResponseOptions) error {
				viaElm = res
				return nil
			})
			if err := elm.InterceptOutboundResponse(ctx, sender, fwd); err != nil {
				t.Fatalf("elm.InterceptOutboundResponse() error = %v, want nil", err)
			}
			assertTagAndBody(viaElm, "element send")

			// Control: an endpoint-constructed response still gets a
			// generated tag on the element path.
			local := newInRes(t, sent, sip.ResponseStatusOK)
			if err := elm.InterceptOutboundResponse(ctx, sender, local); err != nil {
				t.Fatalf("elm.InterceptOutboundResponse(local) error = %v, want nil", err)
			}
			var localTag string
			local.WithMessage(func(msg *sip.Response) {
				if to, ok := msg.Headers.To(); ok {
					localTag, _ = to.Tag()
				}
			})
			if localTag == "" || localTag == downTag {
				t.Fatalf("endpoint response To tag = %q, want a fresh generated tag", localTag)
			}
		})
	})

	t.Run("sips downgrade performs zero request sends", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			loc := &attemptLocator{
				log:     &attemptActionLog{},
				lookups: &atomic.Int32{},
				candidates: map[string][]attemptCandidate{
					"10.0.0.60": {udpAttemptCandidate("sips-hop", "10.0.0.60:5060")},
				},
			}
			route := &sip.ProxyRoute{
				Name:        "sips-downgrade",
				ForwardMode: sip.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
					return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
						{
							{
								ID:  "A",
								URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr("10.0.0.60", 5060), Secured: true},
							},
						},
					}}, nil
				},
			}
			_, prx, tp := newAttemptTestProxy(t, route, sip.ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			}, loc)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".sips-downgrade", laddr, raddr)
			fwdIntercept(t, prx, req)
			synctest.Wait()

			if got := len(drainPrxReqs(tp)); got != 0 {
				t.Fatalf("outbound request sends on SIPS downgrade=%d, want 0", got)
			}
		})
	})
}
