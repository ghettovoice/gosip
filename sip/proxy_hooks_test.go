package sip_test

import (
	"context"
	"log/slog"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
)

func TestProxy_Hooks(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	t.Run("rejection carries status", func(t *testing.T) {
		t.Parallel()

		var authorized atomic.Int32
		route := &sip.ProxyRoute{
			Name: "reject",
			AuthorizeRequest: func(context.Context, *sip.RequestEnvelope) error {
				authorized.Add(1)
				return sip.NewRequestRejectedError(
					errors.ErrorWrap("policy denied"),
					slog.LevelWarn,
					sip.ResponseStatusForbidden,
				)
			},
		}
		_, prx, _ := newTestProxy(t, route)

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".hook-reject", laddr, raddr)
		err := prx.InterceptInboundRequest(
			t.Context(),
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil }),
			req,
		)
		if err == nil {
			t.Fatal("prx.InterceptInboundRequest() error = nil, want rejection")
		}

		var rej *sip.RequestRejectedError
		if !errors.As(err, &rej) {
			t.Fatalf("error = %v, want *sip.RequestRejectedError", err)
		}
		if got, want := rej.ResponseStatus(), sip.ResponseStatusForbidden; got != want {
			t.Fatalf("rejection status = %v, want %v", got, want)
		}
		if got := authorized.Load(); got != 1 {
			t.Fatalf("AuthorizeRequest called %d times, want 1", got)
		}
	})

	t.Run("duplicate resolver target IDs rejected", func(t *testing.T) {
		t.Parallel()

		route := &sip.ProxyRoute{
			Name: "dup-ids",
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
				return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
					{
						{ID: "t", URI: &sip.URI{Addr: sip.MakeHostAddr("198.51.100.1")}},
						{ID: "t", URI: &sip.URI{Addr: sip.MakeHostAddr("198.51.100.2")}},
					},
				}}, nil
			},
		}
		_, prx, _ := newConfiguredTestProxy(t, route, sip.ProxyOptions{
			ServedDomains: []string{"alice.voip.com"},
		})

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".hook-dup", laddr, raddr)
		if err := prx.InterceptInboundRequest(
			t.Context(),
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil }),
			req,
		); err == nil {
			t.Fatal("prx.InterceptInboundRequest() error = nil, want duplicate-ID rejection")
		}
	})

	t.Run("outbound copies are independent", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			type adjustCall struct {
				TargetID string
				RURIUser string
				MaxFwd   uint
			}
			var calls []adjustCall

			route := &sip.ProxyRoute{
				Name:        "copies",
				ForwardMode: sip.ForwardModeStateless,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
					return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
						{
							{ID: "t1", URI: &sip.URI{Addr: sip.MakeHostAddr("198.51.100.1")}},
							{ID: "t2", URI: &sip.URI{Addr: sip.MakeHostAddr("198.51.100.2")}},
						},
					}}, nil
				},
				AdjustOutboundRequest: func(
					_ context.Context,
					inReq, outReq *sip.RequestEnvelope,
					trgt sip.ProxyTarget,
				) error {
					if outReq == inReq {
						t.Error("route.AdjustOutboundRequest() outbound copy aliases the inbound request")
					}

					call := adjustCall{TargetID: trgt.ID}
					outReq.WithMessage(func(r *sip.Request) {
						if v, ok := r.Headers.MaxForwards(); ok {
							call.MaxFwd = uint(v)
						}
					})
					if ru, ok := outReq.URI().(*sip.URI); ok {
						call.RURIUser = ru.User.String()
					}
					calls = append(calls, call)

					// Mutate this branch copy: the next target must observe a
					// fresh copy without it.
					outReq.WithMessage(func(r *sip.Request) {
						if ru, ok := r.URI.(*sip.URI); ok {
							ru.User = sip.MakeUserInfo("mutated-" + trgt.ID)
						}
					})
					return nil
				},
			}
			_, prx, _ := newConfiguredTestProxy(t, route, sip.ProxyOptions{
				StatelessKey:  []byte("hook-copies-key"),
				ServedDomains: []string{"alice.voip.com"},
			})

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".hook-copies", laddr, raddr)
			if err := prx.InterceptInboundRequest(
				t.Context(),
				sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil }),
				req,
			); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			want := []adjustCall{
				{TargetID: "t1", RURIUser: "", MaxFwd: uint(sip.DefaultMaxForwards) - 1},
				{TargetID: "t2", RURIUser: "", MaxFwd: uint(sip.DefaultMaxForwards) - 1},
			}
			if diff := cmp.Diff(want, calls); diff != "" {
				t.Fatalf("adjust calls (-want +got):\n%s", diff)
			}

			// The inbound request keeps its routing data: outbound mutations live
			// on the per-target copies only.
			var (
				inUser string
				inMF   uint
			)
			req.WithMessage(func(r *sip.Request) {
				if ru, ok := r.URI.(*sip.URI); ok {
					inUser = ru.User.String()
				}
				if v, ok := r.Headers.MaxForwards(); ok {
					inMF = uint(v)
				}
			})
			if got, want := inUser, "alice"; got != want {
				t.Fatalf("inbound Request-URI user = %q, want %q", got, want)
			}
			if got, want := inMF, uint(sip.DefaultMaxForwards); got != want {
				t.Fatalf("inbound Max-Forwards = %d, want %d", got, want)
			}
		})
	})

	t.Run("selection and preprocess fields", func(t *testing.T) {
		t.Parallel()

		var (
			gotSel     sip.ProxySelection
			prepReq    *sip.RequestEnvelope
			selectCall atomic.Int32
		)
		route := &sip.ProxyRoute{
			Name: "fields",
			PreprocessRoute: func(_ context.Context, req *sip.RequestEnvelope) error {
				prepReq = req
				return nil
			},
			SelectTargets: func(_ context.Context, sel sip.ProxySelection) ([]string, error) {
				selectCall.Add(1)
				gotSel = sel
				return []string{"a"}, nil
			},
		}

		elm := &sip.Element{}
		t.Cleanup(func() { elm.Close() })
		if _, err := sip.NewProxy(elm, []sip.ProxyRoute{*route}); err != nil {
			t.Fatalf("sip.NewProxy() error = %v, want nil", err)
		}

		sel := sip.ProxySelection{
			RemainingGroups: [][]sip.ProxyTarget{
				{{ID: "a"}, {ID: "b"}},
			},
			ActiveTargetIDs: []string{"x"},
		}
		ids, err := route.SelectTargets(t.Context(), sel)
		if err != nil {
			t.Fatalf("route.SelectTargets() error = %v, want nil", err)
		}
		if diff := cmp.Diff([]string{"a"}, ids); diff != "" {
			t.Fatalf("selected IDs (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(sel.ActiveTargetIDs, gotSel.ActiveTargetIDs); diff != "" {
			t.Fatalf("selection active IDs (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(sel.RemainingGroups, gotSel.RemainingGroups); diff != "" {
			t.Fatalf("selection groups (-want +got):\n%s", diff)
		}

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".hook-fields", laddr, raddr)
		if err := route.PreprocessRoute(t.Context(), req); err != nil {
			t.Fatalf("route.PreprocessRoute() error = %v, want nil", err)
		}
		if prepReq != req {
			t.Fatal("route.PreprocessRoute() received a different request than supplied")
		}
	})
}
