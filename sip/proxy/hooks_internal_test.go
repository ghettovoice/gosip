package proxy

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"testing/synctest"
	"time"

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

func admitFinalResolverFork(
	tb testing.TB,
	route Route,
	req *sip.RequestEnvelope,
) (*sip.Element, *internalTransp, *Request) {
	tb.Helper()

	plan := newInternalLitPlan()
	plan.Groups[0] = append(plan.Groups[0], Target{
		ID: "B",
		URI: &sip.URI{
			User: sip.MakeUserInfo("alice"),
			Addr: sip.MakeHostPortAddr("10.0.0.10", 5060),
		},
	})
	route.ResolveRequestTargets = func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
		return plan, nil
	}
	route.ServerTransactionOptions.Timing.Time100 = time.Hour
	elm, prx, tp := newInternalTestProxy(tb, route, Options{ServedDomains: []string{"alice.voip.com"}})
	rq := admitInternalReq(tb, elm, prx, req)
	synctest.Wait()
	return elm, tp, rq
}

func finalResolverTarget(tb testing.TB, elm *sip.Element, rq *Request, id string) sip.ClientTransaction {
	tb.Helper()

	rq.mu.Lock()
	key := rq.targets[id].txKey
	rq.mu.Unlock()
	tx, ok := elm.LoadClientTransaction(key)
	if !ok {
		tb.Fatalf("client transaction for target %q not registered", id)
	}
	return tx
}

func TestProxy_FinalResponseResolver_InputIsolation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		opaque := &struct{ Value int }{Value: 42}
		req := newInternalInviteReq(sip.MagicCookie + ".resolver-isolation")
		req.Metadata().Set("opaque", opaque).Set("entry", "original")
		wantRequest := req.Render()
		var elm *sip.Element
		var rq *Request
		var calls int
		route := Route{
			Name: "resolver-isolation",
			ResolveFinalResponse: func(ctx context.Context, selection FinalResponseSelection) (int, error) {
				calls++
				if got, ok := sip.ElementFromContext(ctx); !ok || got != elm {
					return -1, errors.ErrorWrap("resolver context lost owning element")
				}
				if _, err := rq.Snapshot(); !errors.Is(err, errSnapshotUnstable) {
					return -1, errors.ErrorfWrap("pending snapshot error = %v, want unstable", err)
				}
				if got, _ := selection.Request.Metadata().Get("opaque"); got != opaque {
					return -1, errors.ErrorWrap("request opaque metadata identity changed")
				}
				selection.Request.WithMessage(func(msg *sip.Request) {
					msg.Method = sip.RequestMethodOptions
					msg.URI = &sip.URI{Addr: sip.MakeHostAddr("mutated.example.com")}
					msg.Proto = sip.ProtoInfo{}
					msg.Headers.Set(header.CallID("mutated-request"))
				})
				selection.Request.Metadata().Clear().Set("replacement", true)
				for _, candidate := range selection.Candidates {
					if got, _ := candidate.Metadata().Get("opaque"); got != opaque {
						return -1, errors.ErrorWrap("candidate opaque metadata identity changed")
					}
					candidate.WithMessage(func(msg *sip.Response) {
						msg.Status = sip.ResponseStatusOK
						msg.Reason = sip.ResponseStatusOK.Reason()
						msg.Headers.Set(header.CallID("mutated-response"))
						if to, ok := msg.Headers.To(); ok {
							to.Params.Set("tag", "mutated-tag")
						}
						msg.Body[0] = 'X'
					})
					candidate.Metadata().Set("entry", "mutated").Delete("opaque")
				}
				selection.Candidates[0], selection.Candidates[1] = selection.Candidates[1], selection.Candidates[0]
				selection.Candidates[1] = nil
				return 1, nil
			},
		}
		var tp *internalTransp
		elm, tp, rq = admitFinalResolverFork(t, route, req)
		txA := finalResolverTarget(t, elm, rq, "A")
		txB := finalResolverTarget(t, elm, rq, "B")
		first := newSelectCandidate(t, txA.Request(), sip.ResponseStatusTemporarilyUnavailable)
		second := newSelectCandidate(t, txB.Request(), sip.ResponseStatusBusyHere)
		for _, res := range []*sip.ResponseEnvelope{first, second} {
			res.Metadata().Set("opaque", opaque).Set("entry", "original")
			res.WithMessage(func(msg *sip.Response) {
				if to, ok := msg.Headers.To(); ok {
					to.Params.Set("tag", "original-tag")
				}
				msg.Body = []byte("original body")
			})
		}
		wantCandidates := []string{first.Render(), second.Render()}
		wantWire := cloneResEnvelope(second)
		wantWire.WithMessage(func(msg *sip.Response) { msg.Headers.PopFirstVia() })
		if err := txA.RecvResponse(t.Context(), first); err != nil {
			t.Fatalf("txA.RecvResponse() error = %v, want nil", err)
		}
		synctest.Wait()
		snap, err := rq.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		before, err := json.Marshal(snap)
		if err != nil {
			t.Fatalf("json.Marshal(snapshot) error = %v, want nil", err)
		}
		if err := txB.RecvResponse(t.Context(), second); err != nil {
			t.Fatalf("txB.RecvResponse() error = %v, want nil", err)
		}
		synctest.Wait()

		sent := drainInternalRess(tp)
		if got := resStatusList(sent); cmp.Diff([]int{486}, got) != "" {
			t.Fatalf("upstream statuses = %v, want [486]", got)
		}
		if diff := cmp.Diff(wantWire.Render(), sent[0].Render()); diff != "" {
			t.Errorf("selected wire content (-want +got):\n%s", diff)
		}
		forwardedHeaders := sent[0].Headers()
		gotCallID, _ := forwardedHeaders.CallID()
		wantCallID, _ := req.Headers().CallID()
		if count := len(forwardedHeaders.Get("Call-ID")); count != 1 || gotCallID != wantCallID {
			t.Errorf("forwarded Call-ID = (%d, %q), want (1, %q)", count, gotCallID, wantCallID)
		}
		if calls != 1 {
			t.Errorf("resolver calls = %d, want 1", calls)
		}
		if diff := cmp.Diff(wantRequest, req.Render()); diff != "" {
			t.Errorf("admitted request mutation (-want +got):\n%s", diff)
		}
		if got, _ := req.Metadata().Get("opaque"); got != opaque || req.Metadata().Has("replacement") {
			t.Error("resolver metadata replacements leaked into admitted request")
		}
		if got, _ := req.Metadata().Get("entry"); got != "original" {
			t.Errorf("admitted request metadata entry = %v, want original", got)
		}
		rq.mu.Lock()
		gotCandidates := make([]string, len(rq.finals))
		for i, candidate := range rq.finals {
			gotCandidates[i] = candidate.Render()
			if got, _ := candidate.Metadata().Get("entry"); got != "original" {
				t.Errorf("candidate %d metadata entry = %v, want original", i, got)
			}
			if got, _ := candidate.Metadata().Get("opaque"); got != opaque {
				t.Errorf("candidate %d opaque metadata = %v, want original pointer", i, got)
			}
		}
		index, final := rq.finalResIndex, rq.final
		rq.mu.Unlock()
		if diff := cmp.Diff(wantCandidates, gotCandidates); diff != "" {
			t.Errorf("stored candidates (-want +got):\n%s", diff)
		}
		if index != 1 || final == nil || final.Status() != sip.ResponseStatusBusyHere {
			t.Errorf("committed selection = (%d, %v), want original index 1 / 486", index, final)
		}
		after, err := json.Marshal(snap)
		if err != nil {
			t.Fatalf("json.Marshal(snapshot) error = %v, want nil", err)
		}
		if diff := cmp.Diff(string(before), string(after)); diff != "" {
			t.Errorf("earlier snapshot mutation (-before +after):\n%s", diff)
		}
	})
}

func TestProxy_FinalResponseResolver_OrdinaryError(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		cause := errors.ErrorWrap("resolver unavailable")
		route := Route{
			Name: "resolver-error",
			ResolveFinalResponse: func(context.Context, FinalResponseSelection) (int, error) {
				return 1, errors.Wrap(cause)
			},
		}
		req := newInternalInviteReq(sip.MagicCookie + ".resolver-error")
		elm, tp, rq := admitFinalResolverFork(t, route, req)
		prx := rq.prx
		var completions []Event
		defer prx.BindEventHandler(EventHandlerFunc(func(_ context.Context, ev Event) {
			if ev.Kind == EventKindRequestCompleted {
				completions = append(completions, ev)
			}
		}))()
		for i, id := range []string{"A", "B"} {
			tx := finalResolverTarget(t, elm, rq, id)
			status := []sip.ResponseStatus{480, 486}[i]
			if err := tx.RecvResponse(t.Context(), newInternalInRes(t, tx.Request(), status)); err != nil {
				t.Fatalf("tx.RecvResponse() error = %v, want nil", err)
			}
			synctest.Wait()
		}
		rq.mu.Lock()
		done, outcome, lastErr, index, final := rq.done, rq.outcome, rq.lastErr, rq.finalResIndex, rq.final
		rq.mu.Unlock()
		if !done || outcome != OutcomeAbandoned || final != nil || index != -1 {
			t.Errorf("request = (done=%t, %v, final=%v, index=%d), want abandoned without selection",
				done, outcome, final, index)
		}
		if !errors.Is(lastErr, cause) || !errors.Is(lastErr, ErrClass) {
			t.Errorf("last error = %v, want preserved cause and proxy class", lastErr)
		}
		if len(completions) != 1 || completions[0].Outcome != OutcomeAbandoned {
			t.Errorf("completions = %+v, want one abandoned completion", completions)
		}
		if got := resStatusList(drainInternalRess(tp)); cmp.Diff([]int{500}, got) != "" {
			t.Errorf("upstream statuses = %v, want [500] without default fallback", got)
		}
	})
}

func TestProxy_FinalResponseResolver_Failure(t *testing.T) {
	t.Parallel()

	const cause errors.Error = "final response policy failed"
	tests := []struct {
		name     string
		statuses [2]sip.ResponseStatus
		index    int
		atEnd    bool
		cause    error
		typed    bool
	}{
		{name: "negative index", statuses: [2]sip.ResponseStatus{480, 486}, index: -1},
		{name: "index at candidate count", statuses: [2]sip.ResponseStatus{480, 486}, atEnd: true},
		{name: "486 cannot override 600", statuses: [2]sip.ResponseStatus{600, 486}, index: 1},
		{name: "callback error", statuses: [2]sip.ResponseStatus{480, 486}, index: 1, cause: cause},
		{name: "typed callback rejection 403", statuses: [2]sip.ResponseStatus{480, 486}, index: 1, cause: cause, typed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, upstream := range []string{"usable", "nil", "unavailable", "terminated"} {
				t.Run(upstream, func(t *testing.T) {
					t.Parallel()

					synctest.Test(t, func(t *testing.T) {
						callbackErr := tt.cause
						if tt.typed {
							callbackErr = sip.NewRequestRejectedError(tt.cause, slog.LevelWarn, sip.ResponseStatusForbidden)
						}
						var calls int
						route := Route{
							Name: "resolver-failure",
							ResolveFinalResponse: func(_ context.Context, selection FinalResponseSelection) (int, error) {
								calls++
								if tt.atEnd {
									return len(selection.Candidates), nil
								}
								return tt.index, errors.Wrap(callbackErr)
							},
						}
						req := newInternalInviteReq(sip.MagicCookie + ".resolver-failure")
						elm, tp, rq := admitFinalResolverFork(t, route, req)
						prx := rq.prx
						rq.mu.Lock()
						original, key := rq.srvTx, rq.srvKey
						rq.mu.Unlock()
						if tx, ok := elm.LoadServerTransaction(key); !ok || tx != original || original.Key() != key {
							t.Fatal("original upstream transaction pointer/key not registered")
						}
						var replacements []sip.ServerTransaction
						defer elm.UseMiddleware(sip.ServerTransactionHandlerFunc(func(_ context.Context, tx sip.ServerTransaction) {
							replacements = append(replacements, tx)
						}))()
						var completions []Event
						defer prx.BindEventHandler(EventHandlerFunc(func(_ context.Context, ev Event) {
							if ev.Kind == EventKindRequestCompleted {
								completions = append(completions, ev)
							}
						}))()
						switch upstream {
						case "nil":
							rq.mu.Lock()
							rq.srvTx = nil
							rq.mu.Unlock()
						case "unavailable":
							rq.mu.Lock()
							rq.srvGone = true
							rq.mu.Unlock()
						case "terminated":
							if err := original.Terminate(t.Context(), nil); err != nil {
								t.Fatalf("original.Terminate() error = %v, want nil", err)
							}
							synctest.Wait()
						}
						for i, id := range []string{"A", "B"} {
							tx := finalResolverTarget(t, elm, rq, id)
							if err := tx.RecvResponse(t.Context(), newInternalInRes(t, tx.Request(), tt.statuses[i])); err != nil {
								t.Fatalf("tx.RecvResponse() error = %v, want nil", err)
							}
							synctest.Wait()
						}

						rq.mu.Lock()
						done, outcome, lastErr, index, final := rq.done, rq.outcome, rq.lastErr, rq.finalResIndex, rq.final
						held, heldKey := rq.srvTx, rq.srvKey
						rq.mu.Unlock()
						if !done || outcome != OutcomeAbandoned || index != -1 || final != nil {
							t.Errorf("request = (done=%t, %v, index=%d, final=%v), want abandoned without selection",
								done, outcome, index, final)
						}
						wantCause := tt.cause
						if wantCause == nil {
							wantCause = errInvalidFinalResIndex
						}
						if !errors.Is(lastErr, wantCause) || !errors.Is(lastErr, ErrClass) {
							t.Errorf("recorded error = %v, want cause %v and proxy class", lastErr, wantCause)
						}
						if callbackErr != nil && !errors.Is(lastErr, callbackErr) {
							t.Errorf("recorded error = %v, want callback error %v", lastErr, callbackErr)
						}
						if rej, ok := errors.AsType[*sip.RequestRejectedError](lastErr); !ok {
							t.Errorf("recorded error = %v, want outer SIP rejection", lastErr)
						} else {
							if got := rej.ResponseStatus(); got != sip.ResponseStatusServerInternalError {
								t.Errorf("outer rejection status = %v, want 500", got)
							}
							if got := rej.LogLevel(); got != slog.LevelError {
								t.Errorf("outer rejection log level = %v, want ERROR", got)
							}
							if diff := cmp.Diff(sip.RespondOptions{}, rej.RespondOptions()); diff != "" {
								t.Errorf("outer rejection options (-want +got):\n%s", diff)
							}
						}
						if calls != 1 {
							t.Errorf("resolver calls = %d, want 1", calls)
						}
						if len(completions) != 1 {
							t.Fatalf("completions = %+v, want one abandoned completion", completions)
						}
						if completions[0].Outcome != OutcomeAbandoned || !errors.Is(completions[0].Error, wantCause) {
							t.Errorf("completion = %+v, want abandoned with cause %v", completions[0], wantCause)
						}
						if heldKey != key || upstream != "nil" && held != original || upstream == "nil" && held != nil {
							t.Error("failure changed original upstream transaction pointer/key")
						}
						if len(replacements) != 0 {
							t.Errorf("replacement server admissions = %d, want 0", len(replacements))
						}
						for tx := range elm.AllServerTransactions() {
							if tx != original {
								t.Errorf("registered server transaction = %v, want only original", tx)
							}
						}
						sent := drainInternalRess(tp)
						if upstream == "usable" {
							if got := resStatusList(sent); cmp.Diff([]int{500}, got) != "" {
								t.Errorf("upstream statuses = %v, want [500] without default fallback", got)
							}
							if len(sent) == 1 {
								if last := original.LastResponse(); last == nil || last.Render() != sent[0].Render() {
									t.Error("original transaction did not retain the upstream response")
								}
								if got, err := sip.MakeServerTransactionKey(sent[0]); err != nil || got != key {
									t.Errorf("upstream response key = (%v, %v), want (%v, nil)", got, err, key)
								}
							}
						} else if len(sent) != 0 || original.LastResponse() != nil {
							t.Errorf("unusable upstream sends = %d, last response = %v, want 0 and nil",
								len(sent), original.LastResponse())
						}
					})
				})
			}
		})
	}
}

func TestProxy_FinalResponseResolver_GlobalFailure(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls int
		route := Route{
			Name: "resolver-global-failure",
			ResolveFinalResponse: func(_ context.Context, selection FinalResponseSelection) (int, error) {
				calls++
				if got := resStatusList(selection.Candidates); cmp.Diff([]int{604, 600}, got) != "" {
					return -1, errors.ErrorfWrap("candidate statuses = %v, want [604 600]", got)
				}
				return 1, nil
			},
		}
		req := newInternalInviteReq(sip.MagicCookie + ".resolver-global-failure")
		elm, tp, rq := admitFinalResolverFork(t, route, req)
		prx := rq.prx
		var completions []Event
		defer prx.BindEventHandler(EventHandlerFunc(func(_ context.Context, ev Event) {
			if ev.Kind == EventKindRequestCompleted {
				completions = append(completions, ev)
			}
		}))()
		for i, id := range []string{"A", "B"} {
			tx := finalResolverTarget(t, elm, rq, id)
			status := [2]sip.ResponseStatus{604, 600}[i]
			if err := tx.RecvResponse(t.Context(), newInternalInRes(t, tx.Request(), status)); err != nil {
				t.Fatalf("tx.RecvResponse() error = %v, want nil", err)
			}
			synctest.Wait()
		}
		if got := resStatusList(drainInternalRess(tp)); cmp.Diff([]int{600}, got) != "" {
			t.Errorf("upstream statuses = %v, want [600]", got)
		}
		if calls != 1 {
			t.Errorf("resolver calls = %d, want 1", calls)
		}
		if len(completions) != 1 || completions[0].Outcome != OutcomeResponse || completions[0].StatusCode != 600 {
			t.Errorf("completions = %+v, want one response completion with status 600", completions)
		}
		rq.mu.Lock()
		index := rq.finalResIndex
		rq.mu.Unlock()
		if index != 1 {
			t.Errorf("committed index = %d, want 1", index)
		}
	})
}

func TestProxy_FinalResponseResolver_RetainedSuccess(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		var calls int
		route := Route{
			Name: "resolver-retained-success",
			ResolveFinalResponse: func(context.Context, FinalResponseSelection) (int, error) {
				calls++
				close(entered)
				<-release
				return 1, nil
			},
		}
		req := newInternalInviteReq(sip.MagicCookie + ".resolver-retained-success")
		elm, tp, rq := admitFinalResolverFork(t, route, req)
		for i, id := range []string{"A", "B"} {
			tx := finalResolverTarget(t, elm, rq, id)
			status := []sip.ResponseStatus{480, 486}[i]
			if err := tx.RecvResponse(t.Context(), newInternalInRes(t, tx.Request(), status)); err != nil {
				t.Fatalf("tx.RecvResponse() error = %v, want nil", err)
			}
			synctest.Wait()
		}
		<-entered
		rq.enqueue(t.Context(), workEvent{kind: evtSchedule})
		rq.enqueue(t.Context(), workEvent{kind: evtSchedule})
		tx := finalResolverTarget(t, elm, rq, "A")
		success := newInternalInRes(t, tx.Request(), sip.ResponseStatusOK)
		rq.mu.Lock()
		gen := rq.attempts[tx.Key()].gen
		rq.mu.Unlock()
		// The retained attempt can still deliver a success after its negative
		// settlement; enqueue at the existing detached attribution boundary.
		rq.enqueue(t.Context(), workEvent{kind: evtClnRes, key: tx.Key(), gen: gen, res: success})
		unblock()
		synctest.Wait()

		rq.mu.Lock()
		done, outcome, index, final := rq.done, rq.outcome, rq.finalResIndex, rq.final
		rq.mu.Unlock()
		if !done || outcome != OutcomeResponse || index != 2 || final != success {
			t.Errorf("committed selection = (done=%t, %v, index=%d, final=%v), want retained success at 2",
				done, outcome, index, final)
		}
		if calls != 1 {
			t.Errorf("resolver calls = %d, want 1", calls)
		}
		if got := resStatusList(drainInternalRess(tp)); cmp.Diff([]int{200}, got) != "" {
			t.Errorf("upstream statuses = %v, want [200] with original-envelope dedup", got)
		}
	})
}
