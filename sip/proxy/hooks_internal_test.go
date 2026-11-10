package proxy

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

func newHookReq(ruri *sip.URI) *sip.RequestEnvelope {
	return sip.NewRequestEnvelope(&sip.Request{
		Method:  sip.RequestMethodInvite,
		URI:     ruri,
		Proto:   sip.ProtoVer20(),
		Headers: make(sip.Headers),
	})
}

func TestProxy_HookWrappers(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	domains := domainAddrs([]string{"alice.voip.com"})

	t.Run("authorize request", func(t *testing.T) {
		t.Parallel()

		prx := &Proxy{domainAddrs: domains}
		req := newHookReq(&sip.URI{Addr: sip.MakeHostAddr("alice.voip.com")})

		t.Run("nil authorizer allows", func(t *testing.T) {
			t.Parallel()

			if err := prx.authorizeReq(ctx, &Route{}, req); err != nil {
				t.Fatalf("prx.authorizeReq() error = %v, want nil", err)
			}
			if err := prx.authorizeReq(ctx, nil, req); err != nil {
				t.Fatalf("prx.authorizeReq() error = %v, want nil for nil route", err)
			}
		})

		t.Run("rejection carries status", func(t *testing.T) {
			t.Parallel()

			cause := errors.ErrorWrap("policy denied")
			route := &Route{
				AuthorizeRequest: func(context.Context, *sip.RequestEnvelope) error {
					return sip.NewRequestRejectedError(cause, slog.LevelWarn, sip.ResponseStatusForbidden)
				},
			}

			err := prx.authorizeReq(ctx, route, req)
			if err == nil {
				t.Fatal("prx.authorizeReq() error = nil, want rejection")
			}

			var rej *sip.RequestRejectedError
			if !errors.As(err, &rej) {
				t.Fatalf("prx.authorizeReq() error = %v, want *sip.RequestRejectedError", err)
			}
			if got, want := rej.ResponseStatus(), sip.ResponseStatusForbidden; got != want {
				t.Fatalf("rejection status = %v, want %v", got, want)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("prx.authorizeReq() error = %v, want cause %v", err, cause)
			}
		})

		t.Run("ordinary error propagates", func(t *testing.T) {
			t.Parallel()

			sentErr := errors.ErrorWrap("authorization backend down")
			route := &Route{
				AuthorizeRequest: func(context.Context, *sip.RequestEnvelope) error { return sentErr },
			}

			err := prx.authorizeReq(ctx, route, req)
			if !errors.Is(err, sentErr) {
				t.Fatalf("prx.authorizeReq() error = %v, want %v", err, sentErr)
			}

			var rej *sip.RequestRejectedError
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
			route := &Route{
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
					resolved = true
					return TargetPlan{}, nil
				},
			}
			req := newHookReq(&sip.URI{
				Addr:   sip.MakeHostAddr("alice.voip.com"),
				Params: make(sip.Values).Set("maddr", "224.0.2.1"),
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
			route := &Route{
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
					resolved = true
					return TargetPlan{}, nil
				},
			}
			req := newHookReq(&sip.URI{Addr: sip.MakeHostAddr("bob.example.net")})

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

			req := newHookReq(&sip.URI{Addr: sip.MakeHostAddr("alice.voip.com")})

			_, err := prx.resolveTargets(ctx, &Route{}, req)
			if err == nil {
				t.Fatal("prx.resolveTargets() error = nil, want no-contact rejection")
			}
			if !errors.Is(err, errNoFwdTargets) {
				t.Fatalf("prx.resolveTargets() error = %v, want %v", err, errNoFwdTargets)
			}

			var rej *sip.RequestRejectedError
			if !errors.As(err, &rej) {
				t.Fatalf("prx.resolveTargets() error = %v, want *sip.RequestRejectedError", err)
			}
			if got, want := rej.ResponseStatus(), sip.ResponseStatusTemporarilyUnavailable; got != want {
				t.Fatalf("rejection status = %v, want %v", got, want)
			}
		})

		t.Run("resolver plan is cloned", func(t *testing.T) {
			t.Parallel()

			srcPlan := TargetPlan{Groups: [][]Target{
				{
					{
						URI:        &sip.URI{Addr: sip.MakeHostAddr("a.example.com")},
						Attributes: map[string]string{"k": "original"},
					},
				},
			}}
			route := &Route{
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
					return srcPlan, nil
				},
			}
			req := newHookReq(&sip.URI{Addr: sip.MakeHostAddr("alice.voip.com")})

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
			srcPlan.Groups[0][0].URI.User = sip.MakeUserInfo("mutated")

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
			route := &Route{
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
					return TargetPlan{}, sentErr
				},
			}
			req := newHookReq(&sip.URI{Addr: sip.MakeHostAddr("alice.voip.com")})

			if _, err := prx.resolveTargets(ctx, route, req); !errors.Is(err, sentErr) {
				t.Fatalf("prx.resolveTargets() error = %v, want %v", err, sentErr)
			}
		})
	})

	t.Run("select targets", func(t *testing.T) {
		t.Parallel()

		prx := &Proxy{}
		selection := Selection{
			RemainingGroups: [][]Target{
				{{ID: "a"}, {ID: "b"}},
				{{ID: "c"}},
			},
		}

		t.Run("nil selector uses ordered groups", func(t *testing.T) {
			t.Parallel()

			ids, err := prx.selectTargets(ctx, &Route{}, selection)
			if err != nil {
				t.Fatalf("prx.selectTargets() error = %v, want nil", err)
			}
			if diff := cmp.Diff([]string{"a", "b"}, ids); diff != "" {
				t.Fatalf("selected IDs (-want +got):\n%s", diff)
			}
		})

		t.Run("nil selector empty plan", func(t *testing.T) {
			t.Parallel()

			ids, err := prx.selectTargets(ctx, &Route{}, Selection{})
			if err != nil {
				t.Fatalf("prx.selectTargets() error = %v, want nil", err)
			}
			if got := len(ids); got != 0 {
				t.Fatalf("selected IDs = %v, want empty", ids)
			}
		})

		t.Run("hook selection passes through", func(t *testing.T) {
			t.Parallel()

			var gotSel Selection
			route := &Route{
				SelectTargets: func(_ context.Context, sel Selection) ([]string, error) {
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
				selection Selection
				ids       []string
				wantErr   error
			}{
				{
					name:      "unknown target",
					selection: selection,
					ids:       []string{"z"},
					wantErr:   errUnknownTargetID,
				},
				{
					name:      "duplicate selection",
					selection: selection,
					ids:       []string{"a", "a"},
					wantErr:   errDuplicateTargetID,
				},
				{
					name: "already started target",
					selection: Selection{
						RemainingGroups: selection.RemainingGroups,
						ActiveTargetIDs: []string{"d"},
					},
					ids:     []string{"d"},
					wantErr: errActiveTargetID,
				},
				{
					name: "finished target",
					selection: Selection{
						RemainingGroups:   selection.RemainingGroups,
						CompletedOutcomes: []BranchOutcome{{TargetID: "c", Completed: true}},
					},
					ids:     []string{"c"},
					wantErr: errFinishedTargetID,
				},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()

					route := &Route{
						SelectTargets: func(context.Context, Selection) ([]string, error) {
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

			route := &Route{
				SelectTargets: func(context.Context, Selection) ([]string, error) {
					return nil, nil
				},
			}

			// Empty while attempts are active means wait for their progress.
			ids, err := prx.selectTargets(ctx, route, Selection{
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
			ids, err = prx.selectTargets(ctx, route, Selection{
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
			route := &Route{
				SelectTargets: func(context.Context, Selection) ([]string, error) {
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
			bound, err := prx.bindDelivery(ctx, DeliveryRef{})
			if err != nil {
				t.Fatalf("prx.bindDelivery() error = %v, want nil", err)
			}
			if bound != (BoundDelivery{}) {
				t.Fatalf("bound delivery = %+v, want zero for ordinary delivery", bound)
			}
		})

		t.Run("custom ref without resolver fails", func(t *testing.T) {
			t.Parallel()

			prx := &Proxy{}
			_, err := prx.bindDelivery(ctx, DeliveryRef{Kind: "flow", Key: "f-1"})
			if err == nil {
				t.Fatal("prx.bindDelivery() error = nil, want missing-resolver failure")
			}
			if !errors.Is(err, errNoDeliveryResolver) {
				t.Fatalf("prx.bindDelivery() error = %v, want %v", err, errNoDeliveryResolver)
			}
		})

		t.Run("resolver binds ref", func(t *testing.T) {
			t.Parallel()

			var gotRef DeliveryRef
			want := BoundDelivery{
				LocalAddr:  netip.MustParseAddrPort("192.0.2.10:5060"),
				RemoteAddr: netip.MustParseAddrPort("198.51.100.20:5060"),
			}
			prx := &Proxy{
				opts: Options{
					ResolveDelivery: func(_ context.Context, ref DeliveryRef) (BoundDelivery, error) {
						gotRef = ref
						return want, nil
					},
				},
			}

			ref := DeliveryRef{Kind: "flow", Key: "f-1"}
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
				opts: Options{
					ResolveDelivery: func(context.Context, DeliveryRef) (BoundDelivery, error) {
						return BoundDelivery{}, sentErr
					},
				},
			}

			_, err := prx.bindDelivery(ctx, DeliveryRef{Kind: "flow", Key: "gone"})
			if !errors.Is(err, sentErr) {
				t.Fatalf("prx.bindDelivery() error = %v, want %v", err, sentErr)
			}
		})
	})
}

func TestProxy_Hooks(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	prx := &Proxy{domainAddrs: domainAddrs([]string{"alice.voip.com"})}

	req := newHookReq(&sip.URI{Addr: sip.MakeHostAddr("alice.voip.com")})
	req.WithMessage(func(r *sip.Request) {
		r.Headers.Append(header.Route{
			{
				URI:    &sip.URI{Addr: sip.MakeHostAddr("proxy.example.com")},
				Params: make(sip.Values).Set("lr", ""),
			},
		})
	})

	var (
		calls         []string
		sawLocalRoute bool
	)
	route := &Route{
		AuthorizeRequest: func(context.Context, *sip.RequestEnvelope) error {
			calls = append(calls, "authorize")
			return nil
		},
		PreprocessRoute: func(_ context.Context, work *sip.RequestEnvelope) error {
			calls = append(calls, "inspect-route")
			work.WithMessage(func(r *sip.Request) {
				_, sawLocalRoute = r.Headers.FirstRoute()
				// The working copy is owned: consuming the local Route here
				// must not leak into the inbound request.
				r.Headers.PopFirstRoute()
			})
			return nil
		},
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
			calls = append(calls, "resolve")
			return TargetPlan{Groups: [][]Target{
				{{URI: &sip.URI{Addr: sip.MakeHostAddr("alice-1.example.net")}}},
			}}, nil
		},
		AdjustOutboundRequest: func(_ context.Context, inReq, outReq *sip.RequestEnvelope, _ Target) error {
			calls = append(calls, "adjust")
			if outReq == inReq {
				t.Error("route.AdjustOutboundRequest() outbound copy aliases the inbound request")
			}
			outReq.WithMessage(func(r *sip.Request) {
				if ru, ok := r.URI.(*sip.URI); ok {
					ru.User = sip.MakeUserInfo("mutated")
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
	req.WithMessage(func(r *sip.Request) {
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
