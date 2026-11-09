package sip

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip/header"
)

func newPrxHookReq(ruri *URI) *RequestEnvelope {
	return NewRequestEnvelope(&Request{
		Method:  RequestMethodInvite,
		URI:     ruri,
		Proto:   protoVer20,
		Headers: make(Headers),
	})
}

func TestProxy_HookWrappers(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	domains := prxDomainAddrs([]string{"alice.voip.com"})

	t.Run("authorize request", func(t *testing.T) {
		t.Parallel()

		prx := &Proxy{domainAddrs: domains}
		req := newPrxHookReq(&URI{Addr: MakeHostAddr("alice.voip.com")})

		t.Run("nil authorizer allows", func(t *testing.T) {
			t.Parallel()

			if err := prx.authorizeReq(ctx, &ProxyRoute{}, req); err != nil {
				t.Fatalf("prx.authorizeReq() error = %v, want nil", err)
			}
			if err := prx.authorizeReq(ctx, nil, req); err != nil {
				t.Fatalf("prx.authorizeReq() error = %v, want nil for nil route", err)
			}
		})

		t.Run("rejection carries status", func(t *testing.T) {
			t.Parallel()

			cause := errors.ErrorWrap("policy denied")
			route := &ProxyRoute{
				AuthorizeRequest: func(context.Context, *RequestEnvelope) error {
					return NewRequestRejectedError(cause, slog.LevelWarn, ResponseStatusForbidden)
				},
			}

			err := prx.authorizeReq(ctx, route, req)
			if err == nil {
				t.Fatal("prx.authorizeReq() error = nil, want rejection")
			}

			var rej *RequestRejectedError
			if !errors.As(err, &rej) {
				t.Fatalf("prx.authorizeReq() error = %v, want *RequestRejectedError", err)
			}
			if got, want := rej.ResponseStatus(), ResponseStatusForbidden; got != want {
				t.Fatalf("rejection status = %v, want %v", got, want)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("prx.authorizeReq() error = %v, want cause %v", err, cause)
			}
		})

		t.Run("ordinary error propagates", func(t *testing.T) {
			t.Parallel()

			sentErr := errors.ErrorWrap("authorization backend down")
			route := &ProxyRoute{
				AuthorizeRequest: func(context.Context, *RequestEnvelope) error { return sentErr },
			}

			err := prx.authorizeReq(ctx, route, req)
			if !errors.Is(err, sentErr) {
				t.Fatalf("prx.authorizeReq() error = %v, want %v", err, sentErr)
			}

			var rej *RequestRejectedError
			if errors.As(err, &rej) {
				t.Fatalf("prx.authorizeReq() error = %v, ordinary error became a SIP rejection", err)
			}
		})
	})

	t.Run("resolve targets", func(t *testing.T) {
		t.Parallel()

		prx := &Proxy{domainAddrs: domains}

		t.Run("maddr short circuits resolver", func(t *testing.T) {
			t.Parallel()

			var resolved bool
			route := &ProxyRoute{
				ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
					resolved = true
					return ProxyTargetPlan{}, nil
				},
			}
			req := newPrxHookReq(&URI{
				Addr:   MakeHostAddr("alice.voip.com"),
				Params: make(Values).Set("maddr", "224.0.2.1"),
			})

			plan, err := prx.resolveTargets(ctx, route, req)
			if err != nil {
				t.Fatalf("prx.resolveTargets() error = %v, want nil", err)
			}
			if resolved {
				t.Fatal("route.ResolveRequestTargets called for a predetermined maddr target")
			}
			if got, want := len(plan.Groups), 1; got != want {
				t.Fatalf("len(plan.Groups) = %d, want %d", got, want)
			}
			if got, want := len(plan.Groups[0]), 1; got != want {
				t.Fatalf("len(plan.Groups[0]) = %d, want %d", got, want)
			}
			if got := plan.Groups[0][0].URI; !got.Equal(req.URI()) {
				t.Fatalf("target URI = %v, want Request-URI %v", got, req.URI())
			}
		})

		t.Run("foreign domain short circuits resolver", func(t *testing.T) {
			t.Parallel()

			var resolved bool
			route := &ProxyRoute{
				ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
					resolved = true
					return ProxyTargetPlan{}, nil
				},
			}
			req := newPrxHookReq(&URI{Addr: MakeHostAddr("bob.example.net")})

			plan, err := prx.resolveTargets(ctx, route, req)
			if err != nil {
				t.Fatalf("prx.resolveTargets() error = %v, want nil", err)
			}
			if resolved {
				t.Fatal("route.ResolveRequestTargets called for a foreign-domain Request-URI")
			}
			if got, want := len(plan.Groups), 1; got != want {
				t.Fatalf("len(plan.Groups) = %d, want %d", got, want)
			}
			if got := plan.Groups[0][0].URI; !got.Equal(req.URI()) {
				t.Fatalf("target URI = %v, want Request-URI %v", got, req.URI())
			}
			if got, want := plan.Groups[0][0].ID, "1.1"; got != want {
				t.Fatalf("target ID = %q, want generated %q", got, want)
			}
		})

		t.Run("nil resolver rejects no contact", func(t *testing.T) {
			t.Parallel()

			req := newPrxHookReq(&URI{Addr: MakeHostAddr("alice.voip.com")})

			_, err := prx.resolveTargets(ctx, &ProxyRoute{}, req)
			if err == nil {
				t.Fatal("prx.resolveTargets() error = nil, want no-contact rejection")
			}
			if !errors.Is(err, errNoFwdTargets) {
				t.Fatalf("prx.resolveTargets() error = %v, want %v", err, errNoFwdTargets)
			}

			var rej *RequestRejectedError
			if !errors.As(err, &rej) {
				t.Fatalf("prx.resolveTargets() error = %v, want *RequestRejectedError", err)
			}
			if got, want := rej.ResponseStatus(), ResponseStatusTemporarilyUnavailable; got != want {
				t.Fatalf("rejection status = %v, want %v", got, want)
			}
		})

		t.Run("resolver plan is cloned", func(t *testing.T) {
			t.Parallel()

			srcPlan := ProxyTargetPlan{Groups: [][]ProxyTarget{
				{
					{
						URI:        &URI{Addr: MakeHostAddr("a.example.com")},
						Attributes: map[string]string{"k": "original"},
					},
				},
			}}
			route := &ProxyRoute{
				ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
					return srcPlan, nil
				},
			}
			req := newPrxHookReq(&URI{Addr: MakeHostAddr("alice.voip.com")})

			plan, err := prx.resolveTargets(ctx, route, req)
			if err != nil {
				t.Fatalf("prx.resolveTargets() error = %v, want nil", err)
			}
			if got, want := plan.Groups[0][0].ID, "1.1"; got != want {
				t.Fatalf("target ID = %q, want generated %q", got, want)
			}

			// Mutating the resolver's returned plan must not corrupt the
			// stored plan.
			srcPlan.Groups[0][0].Attributes["k"] = "mutated"
			srcPlan.Groups[0][0].URI.User = MakeUserInfo("mutated")

			if got := plan.Groups[0][0].Attributes["k"]; got != "original" {
				t.Fatalf("stored attributes mutated: %q, want %q", got, "original")
			}
			if got := plan.Groups[0][0].URI.User.String(); got != "" {
				t.Fatalf("stored URI user mutated: %q, want empty", got)
			}
		})

		t.Run("resolver error propagates", func(t *testing.T) {
			t.Parallel()

			sentErr := errors.ErrorWrap("location lookup failed")
			route := &ProxyRoute{
				ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
					return ProxyTargetPlan{}, sentErr
				},
			}
			req := newPrxHookReq(&URI{Addr: MakeHostAddr("alice.voip.com")})

			if _, err := prx.resolveTargets(ctx, route, req); !errors.Is(err, sentErr) {
				t.Fatalf("prx.resolveTargets() error = %v, want %v", err, sentErr)
			}
		})
	})

	t.Run("select targets", func(t *testing.T) {
		t.Parallel()

		prx := &Proxy{}
		selection := ProxySelection{
			RemainingGroups: [][]ProxyTarget{
				{{ID: "a"}, {ID: "b"}},
				{{ID: "c"}},
			},
		}

		t.Run("nil selector uses ordered groups", func(t *testing.T) {
			t.Parallel()

			ids, err := prx.selectTargets(ctx, &ProxyRoute{}, selection)
			if err != nil {
				t.Fatalf("prx.selectTargets() error = %v, want nil", err)
			}
			if diff := cmp.Diff([]string{"a", "b"}, ids); diff != "" {
				t.Fatalf("selected IDs (-want +got):\n%s", diff)
			}
		})

		t.Run("nil selector empty plan", func(t *testing.T) {
			t.Parallel()

			ids, err := prx.selectTargets(ctx, &ProxyRoute{}, ProxySelection{})
			if err != nil {
				t.Fatalf("prx.selectTargets() error = %v, want nil", err)
			}
			if got := len(ids); got != 0 {
				t.Fatalf("selected IDs = %v, want empty", ids)
			}
		})

		t.Run("hook selection passes through", func(t *testing.T) {
			t.Parallel()

			var gotSel ProxySelection
			route := &ProxyRoute{
				SelectTargets: func(_ context.Context, sel ProxySelection) ([]string, error) {
					gotSel = sel
					return []string{"c", "a"}, nil
				},
			}

			ids, err := prx.selectTargets(ctx, route, selection)
			if err != nil {
				t.Fatalf("prx.selectTargets() error = %v, want nil", err)
			}
			if diff := cmp.Diff([]string{"c", "a"}, ids); diff != "" {
				t.Fatalf("selected IDs (-want +got):\n%s", diff)
			}
			if got, want := len(gotSel.RemainingGroups), 2; got != want {
				t.Fatalf("selection groups = %d, want %d", got, want)
			}
		})

		t.Run("rejects invalid IDs", func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name      string
				selection ProxySelection
				ids       []string
				wantErr   error
			}{
				{
					name:      "unknown target",
					selection: selection,
					ids:       []string{"z"},
					wantErr:   errUnknownPrxTargetID,
				},
				{
					name:      "duplicate selection",
					selection: selection,
					ids:       []string{"a", "a"},
					wantErr:   errDuplicatePrxTargetID,
				},
				{
					name: "already started target",
					selection: ProxySelection{
						RemainingGroups: selection.RemainingGroups,
						ActiveTargetIDs: []string{"d"},
					},
					ids:     []string{"d"},
					wantErr: errActivePrxTargetID,
				},
				{
					name: "finished target",
					selection: ProxySelection{
						RemainingGroups:   selection.RemainingGroups,
						CompletedOutcomes: []ProxyBranchOutcome{{TargetID: "c", Completed: true}},
					},
					ids:     []string{"c"},
					wantErr: errFinishedPrxTargetID,
				},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()

					route := &ProxyRoute{
						SelectTargets: func(context.Context, ProxySelection) ([]string, error) {
							return tt.ids, nil
						},
					}

					if _, err := prx.selectTargets(ctx, route, tt.selection); !errors.Is(err, tt.wantErr) {
						t.Fatalf("prx.selectTargets() error = %v, want %v", err, tt.wantErr)
					}
				})
			}
		})

		t.Run("empty selection semantics", func(t *testing.T) {
			t.Parallel()

			route := &ProxyRoute{
				SelectTargets: func(context.Context, ProxySelection) ([]string, error) {
					return nil, nil
				},
			}

			// Empty while attempts are active means wait for their progress.
			ids, err := prx.selectTargets(ctx, route, ProxySelection{
				RemainingGroups: selection.RemainingGroups,
				ActiveTargetIDs: []string{"a"},
			})
			if err != nil {
				t.Fatalf("prx.selectTargets() error = %v, want nil (wait)", err)
			}
			if got := len(ids); got != 0 {
				t.Fatalf("selected IDs = %v, want empty (wait)", ids)
			}

			// Empty without active attempts declines the remaining targets.
			ids, err = prx.selectTargets(ctx, route, ProxySelection{
				RemainingGroups: selection.RemainingGroups,
			})
			if err != nil {
				t.Fatalf("prx.selectTargets() error = %v, want nil (decline)", err)
			}
			if got := len(ids); got != 0 {
				t.Fatalf("selected IDs = %v, want empty (decline)", ids)
			}
		})

		t.Run("hook error propagates", func(t *testing.T) {
			t.Parallel()

			sentErr := errors.ErrorWrap("selector failed")
			route := &ProxyRoute{
				SelectTargets: func(context.Context, ProxySelection) ([]string, error) {
					return nil, sentErr
				},
			}

			if _, err := prx.selectTargets(ctx, route, selection); !errors.Is(err, sentErr) {
				t.Fatalf("prx.selectTargets() error = %v, want %v", err, sentErr)
			}
		})
	})

	t.Run("bind delivery", func(t *testing.T) {
		t.Parallel()

		t.Run("ordinary ref needs no resolver", func(t *testing.T) {
			t.Parallel()

			prx := &Proxy{}
			bound, err := prx.bindDelivery(ctx, ProxyDeliveryRef{})
			if err != nil {
				t.Fatalf("prx.bindDelivery() error = %v, want nil", err)
			}
			if bound != (ProxyBoundDelivery{}) {
				t.Fatalf("bound delivery = %+v, want zero for ordinary delivery", bound)
			}
		})

		t.Run("custom ref without resolver fails", func(t *testing.T) {
			t.Parallel()

			prx := &Proxy{}
			_, err := prx.bindDelivery(ctx, ProxyDeliveryRef{Kind: "flow", Key: "f-1"})
			if err == nil {
				t.Fatal("prx.bindDelivery() error = nil, want missing-resolver failure")
			}
			if !errors.Is(err, errNoDeliveryResolver) {
				t.Fatalf("prx.bindDelivery() error = %v, want %v", err, errNoDeliveryResolver)
			}
		})

		t.Run("resolver binds ref", func(t *testing.T) {
			t.Parallel()

			var gotRef ProxyDeliveryRef
			want := ProxyBoundDelivery{
				LocalAddr:  netip.MustParseAddrPort("192.0.2.10:5060"),
				RemoteAddr: netip.MustParseAddrPort("198.51.100.20:5060"),
			}
			prx := &Proxy{
				opts: ProxyOptions{
					ResolveDelivery: func(_ context.Context, ref ProxyDeliveryRef) (ProxyBoundDelivery, error) {
						gotRef = ref
						return want, nil
					},
				},
			}

			ref := ProxyDeliveryRef{Kind: "flow", Key: "f-1"}
			bound, err := prx.bindDelivery(ctx, ref)
			if err != nil {
				t.Fatalf("prx.bindDelivery() error = %v, want nil", err)
			}
			if bound != want {
				t.Fatalf("bound delivery = %+v, want %+v", bound, want)
			}
			if gotRef != ref {
				t.Fatalf("resolver ref = %+v, want %+v", gotRef, ref)
			}
		})

		t.Run("resolver error propagates", func(t *testing.T) {
			t.Parallel()

			sentErr := errors.ErrorWrap("flow not found")
			prx := &Proxy{
				opts: ProxyOptions{
					ResolveDelivery: func(context.Context, ProxyDeliveryRef) (ProxyBoundDelivery, error) {
						return ProxyBoundDelivery{}, sentErr
					},
				},
			}

			_, err := prx.bindDelivery(ctx, ProxyDeliveryRef{Kind: "flow", Key: "gone"})
			if !errors.Is(err, sentErr) {
				t.Fatalf("prx.bindDelivery() error = %v, want %v", err, sentErr)
			}
		})
	})
}

func TestProxy_Hooks(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	prx := &Proxy{domainAddrs: prxDomainAddrs([]string{"alice.voip.com"})}

	req := newPrxHookReq(&URI{Addr: MakeHostAddr("alice.voip.com")})
	req.WithMessage(func(r *Request) {
		r.Headers.Append(header.Route{
			{
				URI:    &URI{Addr: MakeHostAddr("proxy.example.com")},
				Params: make(Values).Set("lr", ""),
			},
		})
	})

	var (
		calls         []string
		sawLocalRoute bool
	)
	route := &ProxyRoute{
		AuthorizeRequest: func(context.Context, *RequestEnvelope) error {
			calls = append(calls, "authorize")
			return nil
		},
		PreprocessRoute: func(_ context.Context, work *RequestEnvelope) error {
			calls = append(calls, "inspect-route")
			work.WithMessage(func(r *Request) {
				_, sawLocalRoute = r.Headers.FirstRoute()
				// The working copy is owned: consuming the local Route here
				// must not leak into the inbound request.
				r.Headers.PopFirstRoute()
			})
			return nil
		},
		ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
			calls = append(calls, "resolve")
			return ProxyTargetPlan{Groups: [][]ProxyTarget{
				{{URI: &URI{Addr: MakeHostAddr("alice-1.example.net")}}},
			}}, nil
		},
		AdjustOutboundRequest: func(_ context.Context, inReq, outReq *RequestEnvelope, _ ProxyTarget) error {
			calls = append(calls, "adjust")
			if outReq == inReq {
				t.Error("route.AdjustOutboundRequest() outbound copy aliases the inbound request")
			}
			outReq.WithMessage(func(r *Request) {
				if ru, ok := r.URI.(*URI); ok {
					ru.User = MakeUserInfo("mutated")
				}
			})
			return nil
		},
	}

	if err := prx.authorizeReq(ctx, route, req); err != nil {
		t.Fatalf("prx.authorizeReq() error = %v, want nil", err)
	}

	// The route preprocess hook observes routing headers on an owned working
	// copy before the core consumes a local Route value.
	work := cloneReqEnvelope(req)
	if err := route.PreprocessRoute(ctx, work); err != nil {
		t.Fatalf("route.PreprocessRoute() error = %v, want nil", err)
	}
	if !sawLocalRoute {
		t.Error("route.PreprocessRoute did not observe the local Route header")
	}
	req.WithMessage(func(r *Request) {
		if _, ok := r.Headers.FirstRoute(); !ok {
			t.Error("hook mutation leaked: inbound request lost its Route header")
		}
	})

	plan, err := prx.resolveTargets(ctx, route, req)
	if err != nil {
		t.Fatalf("prx.resolveTargets() error = %v, want nil", err)
	}

	for _, group := range plan.Groups {
		for _, trgt := range group {
			// Each target gets an independent outbound copy.
			outReq := cloneReqEnvelope(req)
			if err := route.AdjustOutboundRequest(ctx, req, outReq, trgt); err != nil {
				t.Fatalf("route.AdjustOutboundRequest() error = %v, want nil", err)
			}
		}
	}

	want := []string{"authorize", "inspect-route", "resolve", "adjust"}
	if diff := cmp.Diff(want, calls); diff != "" {
		t.Fatalf("hook order (-want +got):\n%s", diff)
	}
}
