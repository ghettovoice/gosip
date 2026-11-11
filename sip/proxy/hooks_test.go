package proxy_test

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
	"github.com/ghettovoice/gosip/sip/proxy"
)

func TestProxy_Hooks(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	t.Run("rejection carries status", func(t *testing.T) {
		t.Parallel()

		var authorized atomic.Int32
		route := &proxy.Route{
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
		elm, prx, _ := newTestProxy(t, route)

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".hook-reject", laddr, raddr)
		err := prx.InterceptInboundRequest(
			sip.ContextWithElement(t.Context(), elm),
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
			t.Fatalf("route.AuthorizeRequest called %d times, want 1", got)
		}
	})

	t.Run("duplicate resolver target IDs rejected", func(t *testing.T) {
		t.Parallel()

		route := &proxy.Route{
			Name: "dup-ids",
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{
					{
						{ID: "t", URI: &sip.URI{Addr: sip.MakeHostAddr("198.51.100.1")}},
						{ID: "t", URI: &sip.URI{Addr: sip.MakeHostAddr("198.51.100.2")}},
					},
				}}, nil
			},
		}
		elm, prx, _ := newConfiguredTestProxy(t, route, proxy.Options{
			ServedDomains: []string{"alice.voip.com"},
		})

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".hook-dup", laddr, raddr)
		if err := prx.InterceptInboundRequest(
			sip.ContextWithElement(t.Context(), elm),
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

			route := &proxy.Route{
				Name:        "copies",
				ForwardMode: proxy.ForwardModeStateless,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return proxy.TargetPlan{Groups: [][]proxy.Target{
						{
							{ID: "t1", URI: &sip.URI{Addr: sip.MakeHostAddr("198.51.100.1")}},
							{ID: "t2", URI: &sip.URI{Addr: sip.MakeHostAddr("198.51.100.2")}},
						},
					}}, nil
				},
				AdjustOutboundRequest: func(
					_ context.Context,
					inReq, outReq *sip.RequestEnvelope,
					trgt proxy.Target,
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
			elm, prx, _ := newConfiguredTestProxy(t, route, proxy.Options{
				StatelessKey:  []byte("hook-copies-key"),
				ServedDomains: []string{"alice.voip.com"},
			})

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".hook-copies", laddr, raddr)
			if err := prx.InterceptInboundRequest(
				sip.ContextWithElement(t.Context(), elm),
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
			gotSel     proxy.Selection
			prepReq    *sip.RequestEnvelope
			selectCall atomic.Int32
		)
		route := &proxy.Route{
			Name: "fields",
			PreprocessRoute: func(_ context.Context, req *sip.RequestEnvelope) error {
				prepReq = req
				return nil
			},
			SelectTargets: func(_ context.Context, sel proxy.Selection) ([]string, error) {
				selectCall.Add(1)
				gotSel = sel
				return []string{"a"}, nil
			},
		}

		elm := &sip.Element{}
		t.Cleanup(func() { elm.Close() })
		if _, err := proxy.New([]proxy.Route{*route}); err != nil {
			t.Fatalf("proxy.New() error = %v, want nil", err)
		}

		sel := proxy.Selection{
			RemainingGroups: [][]proxy.Target{
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

func TestProxy_FinalResponseResolver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		resolve func(context.Context, proxy.FinalResponseSelection) (int, error)
		want    []int
	}{
		{
			name: "custom selects second arrival",
			resolve: func(ctx context.Context, selection proxy.FinalResponseSelection) (int, error) {
				return 1, nil
			},
			want: []int{486},
		},
		{
			name: "nil uses default",
			want: []int{480},
		},
		{
			name:    "explicit default delegation",
			resolve: proxy.DefaultFinalResponseResolver,
			want:    []int{480},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				var (
					gotSelection proxy.FinalResponseSelection
					calls        int
					resolver     func(context.Context, proxy.FinalResponseSelection) (int, error)
				)
				if tt.resolve != nil {
					resolver = func(ctx context.Context, selection proxy.FinalResponseSelection) (int, error) {
						calls++
						gotSelection = selection
						return tt.resolve(ctx, selection)
					}
				}
				route := &proxy.Route{
					Name:                 "final-resolver",
					ForwardMode:          proxy.ForwardModeStateful,
					ResolveFinalResponse: resolver,
				}
				req, branches, upstream := runFinalResponseResolver(t, route, [2]sip.ResponseStatus{480, 486}, nil)
				if diff := cmp.Diff(tt.want, fwdStatuses(upstream)); diff != "" {
					t.Fatalf("forwarded statuses (-want +got):\n%s", diff)
				}
				if tt.resolve == nil {
					return
				}
				if calls != 1 {
					t.Fatalf("route.ResolveFinalResponse called %d times, want 1", calls)
				}
				if gotSelection.Request == nil {
					t.Fatal("proxy.FinalResponseSelection.Request = nil, want the inbound request")
				}
				if got, want := gotSelection.Request.String(), req.String(); got != want {
					t.Errorf("proxy.FinalResponseSelection.Request = %q, want %q", got, want)
				}
				claim, ok := proxy.MetadataFrom(req.Metadata())
				if !ok || claim.RequestID == "" {
					t.Fatalf("inbound proxy.MetadataFrom() = (%+v, %v), want a claimed request identity", claim, ok)
				}
				requestMD, ok := proxy.MetadataFrom(gotSelection.Request.Metadata())
				if !ok {
					t.Fatal("proxy.FinalResponseSelection.Request metadata missing")
				}
				if diff := cmp.Diff(claim, requestMD); diff != "" {
					t.Errorf("resolver request metadata (-want +got):\n%s", diff)
				}
				if len(gotSelection.Candidates) != 2 {
					t.Fatalf("proxy.FinalResponseSelection.Candidates length = %d, want 2", len(gotSelection.Candidates))
				}
				if diff := cmp.Diff([]int{480, 486}, fwdStatuses(gotSelection.Candidates)); diff != "" {
					t.Errorf("arrival-ordered candidate statuses (-want +got):\n%s", diff)
				}
				var attemptIDs [2]string
				for i, targetID := range []string{"A", "B"} {
					branchMD, ok := proxy.MetadataFrom(branches[i].Metadata())
					if !ok || branchMD.RequestID != claim.RequestID || branchMD.TargetID != targetID ||
						branchMD.AttemptID == "" || branchMD.Stage != proxy.StageForward {
						t.Fatalf("branch %s metadata = (%+v, %v), want its request, target, and attempt identity",
							targetID, branchMD, ok)
					}
					attemptIDs[i] = branchMD.AttemptID
					candidateMD, ok := proxy.MetadataFrom(gotSelection.Candidates[i].Metadata())
					if !ok {
						t.Fatalf("candidate %d proxy.MetadataFrom() ok = false, want true", i)
					}
					want := proxy.Metadata{
						RequestID: claim.RequestID,
						TargetID:  targetID,
						AttemptID: branchMD.AttemptID,
						RouteName: "final-resolver",
						Mode:      proxy.ForwardModeStateful,
						Stage:     proxy.StageResponse,
					}
					if diff := cmp.Diff(want, candidateMD); diff != "" {
						t.Errorf("candidate %d metadata (-want +got):\n%s", i, diff)
					}
				}
				if attemptIDs[0] == attemptIDs[1] {
					t.Errorf("fork attempt IDs = %q/%q, want distinct branch identities", attemptIDs[0], attemptIDs[1])
				}
			})
		})
	}
}

func runFinalResponseResolver(
	t *testing.T,
	route *proxy.Route,
	statuses [2]sip.ResponseStatus,
	prepare func(*sip.ResponseEnvelope),
) (*sip.RequestEnvelope, [2]*sip.RequestEnvelope, []*sip.ResponseEnvelope) {
	t.Helper()

	route.ResolveRequestTargets = func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
		return obsLitPlan("A", "B"), nil
	}
	elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{ServedDomains: []string{"alice.voip.com"}})
	req := newInInviteReq(t, "UDP", sip.MagicCookie+".final-resolver",
		netip.MustParseAddrPort("192.168.1.100:5060"), netip.MustParseAddrPort("10.0.0.5:5060"))
	fwdIntercept(t, elm, prx, req)
	synctest.Wait()

	sends := drainReqs(tp)
	if len(sends) != 2 {
		t.Fatalf("outbound request sends = %d, want 2", len(sends))
	}
	var branches [2]*sip.RequestEnvelope
	for _, out := range sends {
		uri, ok := out.URI().(*sip.URI)
		if !ok {
			t.Fatalf("outbound Request-URI type = %T, want *sip.URI", out.URI())
		}
		var index int
		switch uri.Addr.Host() {
		case "10.0.0.9":
			index = 0
		case "10.0.0.10":
			index = 1
		default:
			t.Fatalf("outbound target host = %q, want 10.0.0.9 or 10.0.0.10", uri.Addr.Host())
		}
		if branches[index] != nil {
			t.Fatalf("duplicate outbound branch for host %q", uri.Addr.Host())
		}
		branches[index] = out
	}
	for i, branch := range branches {
		if branch == nil {
			t.Fatalf("outbound branch %d missing", i)
		}
		res := newInRes(t, branch, statuses[i])
		if prepare != nil {
			prepare(res)
		}
		if err := fwdClnTx(t, elm, branch).RecvResponse(t.Context(), res); err != nil {
			t.Fatalf("clnTx.RecvResponse(%d) error = %v, want nil", statuses[i], err)
		}
		synctest.Wait()
		if i == 0 {
			if got := len(drainRess(tp)); got != 0 {
				t.Fatalf("upstream sends with an active sibling = %d, want 0", got)
			}
		}
	}
	return req, branches, drainRess(tp)
}
