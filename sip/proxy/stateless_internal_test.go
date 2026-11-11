package proxy

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

func newStatelessTestProxy(tb testing.TB, key []byte) *Proxy {
	tb.Helper()

	return &Proxy{
		opts: Options{StatelessKey: key},
	}
}

func statelessTestURI() *sip.URI {
	return &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.example.com")}
}

func statelessTestTarget() Target {
	return Target{
		ID:  "t1",
		URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr("198.51.100.10", 5060)},
	}
}

func TestProxy_stateless_single_target(t *testing.T) {
	t.Parallel()

	a := statelessTestTarget()
	a.Attributes = map[string]string{"role": "primary"}
	a.Delivery = DeliveryRef{Kind: "binding", Key: "A"}
	b := statelessTestTarget()
	b.ID = "t2"
	b.Attributes = map[string]string{"role": "backup"}
	b.Delivery = DeliveryRef{Kind: "binding", Key: "B"}
	other := statelessTestTarget()
	other.ID = "other"
	other.URI.Addr = sip.MakeHostPortAddr("198.51.100.11", 5060)
	tests := []struct {
		name string
		plan TargetPlan
		want Target
		ok   bool
	}{
		{name: "zero targets"},
		{name: "empty groups", plan: TargetPlan{Groups: [][]Target{{}, {}}}},
		{name: "one target", plan: TargetPlan{Groups: [][]Target{{a}}}, want: a, ok: true},
		{name: "empty surrounding groups", plan: TargetPlan{Groups: [][]Target{{}, {a}, {}}}, want: a, ok: true},
		{name: "same group", plan: TargetPlan{Groups: [][]Target{{a, other}}}},
		{name: "cross group", plan: TargetPlan{Groups: [][]Target{{a}, {}, {other}}}},
		{name: "same uri distinct ids", plan: TargetPlan{Groups: [][]Target{{a, b}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			before, err := cloneTargetPlan(tt.plan)
			if err != nil {
				t.Fatalf("cloneTargetPlan() error = %v, want nil", err)
			}
			got, ok := singleTarget(tt.plan)
			if ok != tt.ok {
				t.Errorf("singleTarget() ok = %v, want %v", ok, tt.ok)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("singleTarget() target (-want +got): %s", diff)
			}
			if diff := cmp.Diff(before, tt.plan); diff != "" {
				t.Errorf("singleTarget() mutated plan (-want +got): %s", diff)
			}
		})
	}
}

// statelessRelatedOf derives a related-method fixture (CANCEL or a
// non-routed ACK) from an INVITE envelope: the top Via, From tag, Call-ID,
// CSeq number, and Request-URI are preserved while the method and the CSeq
// method change. A non-empty toTag installs the acknowledged response's tag
// on the To header.
func statelessRelatedOf(inv *sip.RequestEnvelope, method sip.RequestMethod, toTag string) *sip.RequestEnvelope {
	env := cloneReqEnvelope(inv)
	env.WithMessage(func(r *sip.Request) {
		r.Method = method
		if cseq, ok := r.Headers.CSeq(); ok {
			r.Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: method})
		}
		if toTag != "" {
			if to, ok := r.Headers.To(); ok {
				if to.Params == nil {
					to.Params = make(sip.Values)
				}
				to.Params.Set("tag", toTag)
			}
		}
	})
	return env
}

func statelessLegacyVia(env *sip.RequestEnvelope) {
	env.WithMessage(func(r *sip.Request) {
		if via, ok := r.Headers.FirstVia(); ok {
			via.Params.Delete("branch")
		}
	})
}

func TestProxy_StatelessBranch(t *testing.T) {
	t.Parallel()

	key := []byte("stateless-test-key")
	prx := newStatelessTestProxy(t, key)
	target := statelessTestTarget()
	invite := newValidationReq(sip.RequestMethodInvite, statelessTestURI())
	assertValidStatelessBranch := func(tb testing.TB, branch string) {
		tb.Helper()

		if !sip.IsRFC3261Branch(branch) {
			tb.Fatalf("prx.statelessBranch() = %q is not a valid RFC 3261 branch", branch)
		}
		if !strings.HasPrefix(branch, sip.MagicCookie+"."+branchStatelessMark+".") {
			tb.Fatalf("prx.statelessBranch() = %q misses the proxy-stateless ownership marker", branch)
		}
		token := branch[strings.LastIndex(branch, ".")+1:]
		if len(token) == 0 {
			tb.Fatalf("prx.statelessBranch() = %q has an empty keyed token", branch)
		}
	}

	t.Run("retransmission stable", func(t *testing.T) {
		t.Parallel()

		retrans := cloneReqEnvelope(invite)
		branchInvite := prx.statelessBranch(invite, target)
		branchRetrans := prx.statelessBranch(retrans, target)

		assertValidStatelessBranch(t, branchInvite)
		if branchInvite != branchRetrans {
			t.Fatalf("retransmission branch = %q, want identical %q", branchRetrans, branchInvite)
		}
	})

	t.Run("related methods share", func(t *testing.T) {
		t.Parallel()

		cancel, err := sip.NewCancelRequestEnvelope(invite)
		if err != nil {
			t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
		}
		ack := statelessRelatedOf(invite, sip.RequestMethodAck, "down-tag-1")

		branchInvite := prx.statelessBranch(invite, target)
		branchCancel := prx.statelessBranch(cancel, target)
		branchAck := prx.statelessBranch(ack, target)

		if branchInvite != branchCancel || branchInvite != branchAck {
			t.Fatalf("related methods changed branch: invite=%q cancel=%q ack=%q", branchInvite, branchCancel, branchAck)
		}
	})

	t.Run("to tag normalized", func(t *testing.T) {
		t.Parallel()

		tagged := statelessRelatedOf(invite, sip.RequestMethodInvite, "down-tag-2")

		branchInvite := prx.statelessBranch(invite, target)
		branchTagged := prx.statelessBranch(tagged, target)
		if branchInvite != branchTagged {
			t.Fatalf("To tag changed branch: untagged=%q tagged=%q", branchInvite, branchTagged)
		}
	})

	t.Run("distinct transaction changes branch", func(t *testing.T) {
		t.Parallel()

		// An end-to-end ACK for a 2xx is a new upstream transaction: its top
		// Via branch differs, so the derived branch may change.
		routedAck := statelessRelatedOf(invite, sip.RequestMethodAck, "down-tag-3")
		routedAck.WithMessage(func(r *sip.Request) {
			if via, ok := r.Headers.FirstVia(); ok {
				via.Params.Set("branch", sip.MagicCookie+".validate-ack")
			}
		})

		branchInvite := prx.statelessBranch(invite, target)
		branchRoutedAck := prx.statelessBranch(routedAck, target)
		if branchInvite == branchRoutedAck {
			t.Fatalf("distinct upstream transaction kept branch %q", branchRoutedAck)
		}
	})

	t.Run("magic cookie and legacy via", func(t *testing.T) {
		t.Parallel()

		cookieInvite := cloneReqEnvelope(invite)
		legacyInvite := cloneReqEnvelope(invite)
		statelessLegacyVia(legacyInvite)

		branchCookie := prx.statelessBranch(cookieInvite, target)
		branchLegacy := prx.statelessBranch(legacyInvite, target)

		assertValidStatelessBranch(t, branchCookie)
		assertValidStatelessBranch(t, branchLegacy)
		if branchCookie == branchLegacy {
			t.Fatalf("magic-cookie and legacy derivations collided at %q", branchCookie)
		}

		// Both derivations stay deterministic for retransmissions.
		legacyRetrans := cloneReqEnvelope(legacyInvite)
		if got := prx.statelessBranch(legacyRetrans, target); got != branchLegacy {
			t.Fatalf("legacy retransmission branch = %q, want %q", got, branchLegacy)
		}

		// Related methods still share the legacy branch: a legacy CANCEL
		// carries the same branch-less top Via and normalized identity.
		legacyCancel := statelessRelatedOf(legacyInvite, sip.RequestMethodCancel, "")
		if got := prx.statelessBranch(legacyCancel, target); got != branchLegacy {
			t.Fatalf("legacy cancel branch = %q, want %q", got, branchLegacy)
		}
	})

	t.Run("distinct key namespace", func(t *testing.T) {
		t.Parallel()

		other := newStatelessTestProxy(t, []byte("stateless-other-key"))

		branchKeyA := prx.statelessBranch(invite, target)
		branchKeyB := other.statelessBranch(invite, target)
		if branchKeyA == branchKeyB {
			t.Fatal("distinct stateless namespaces collided")
		}
	})

	t.Run("missing key rejected", func(t *testing.T) {
		t.Parallel()

		noKey := newStatelessTestProxy(t, nil)
		if got := noKey.statelessBranch(invite, target); got != "" {
			t.Fatalf("prx.statelessBranch() without a configured key = %q, want rejection", got)
		}
	})

	t.Run("target binds branch", func(t *testing.T) {
		t.Parallel()

		other := statelessTestTarget()
		other.ID = "t2"
		other.URI = &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr("198.51.100.20", 5060)}

		branchT1 := prx.statelessBranch(invite, target)
		branchT2 := prx.statelessBranch(invite, other)
		if branchT1 == branchT2 {
			t.Fatalf("distinct destinations collided at %q", branchT1)
		}
	})

	t.Run("deterministic across calls", func(t *testing.T) {
		t.Parallel()

		branch1 := prx.statelessBranch(invite, target)
		branch2 := prx.statelessBranch(invite, target)
		if branch1 != branch2 {
			t.Fatalf("prx.statelessBranch() not deterministic: %q vs %q", branch1, branch2)
		}

		// The same key on a sibling proxy reproduces the same branch: the
		// derivation depends only on the key and message, never on
		// instance-local randomness.
		sibling := newStatelessTestProxy(t, key)
		if got := sibling.statelessBranch(invite, target); got != branch1 {
			t.Fatalf("sibling proxy branch = %q, want %q", got, branch1)
		}
	})

	t.Run("inputs not mutated", func(t *testing.T) {
		t.Parallel()

		in := newValidationReq(sip.RequestMethodInvite, statelessTestURI())
		trgt := statelessTestTarget()
		wantReq := in.String()
		wantURI := trgt.URI.String()

		_ = prx.statelessBranch(in, trgt)

		if diff := cmp.Diff(wantReq, in.String()); diff != "" {
			t.Fatalf("inbound request mutated (-before +after):\n%s", diff)
		}
		if diff := cmp.Diff(wantURI, trgt.URI.String()); diff != "" {
			t.Fatalf("target URI mutated (-before +after):\n%s", diff)
		}
	})
}

// statelessPolicyPlan is a single-target plan used by the send-policy tests.
func statelessPolicyPlan(trgt Target) TargetPlan {
	return TargetPlan{Groups: [][]Target{{trgt}}}
}

func statelessHostPolicyTarget() Target {
	return Target{
		ID:  "h1",
		URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.example.org")},
	}
}

func TestProxy_FinalResponseResolver_StatelessPromotion(t *testing.T) {
	t.Parallel()

	t.Run("true stateless bypass", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			var calls atomic.Int32
			route := Route{
				Name:        "resolver-stateless",
				ForwardMode: ForwardModeStateless,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
					return statelessPolicyPlan(statelessTestTarget()), nil
				},
				ResolveFinalResponse: func(context.Context, FinalResponseSelection) (int, error) {
					calls.Add(1)
					return -1, errors.ErrorWrap("unexpected stateless resolver invocation")
				},
			}
			elm, prx, tp := newInternalTestProxy(t, route, Options{
				StatelessKey: []byte("resolver-stateless-key"), ServedDomains: []string{"alice.voip.com"},
			})
			var completions []RequestOutcome
			prx.BindEventHandler(EventHandlerFunc(func(_ context.Context, ev Event) {
				if ev.Kind == EventKindRequestCompleted {
					completions = append(completions, ev.Outcome)
				}
			}))
			req := newInternalInviteReq(sip.MagicCookie + ".resolver-stateless")
			ctx := sip.ContextWithElement(t.Context(), elm)
			if err := prx.InterceptInboundRequest(ctx,
				sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil }), req,
			); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()
			sends := drainInternalReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("stateless sends = %d, want 1", len(sends))
			}
			md, ok := MetadataFrom(sends[0].Metadata())
			if !ok || md.Mode != ForwardModeStateless {
				t.Errorf("stateless send metadata = (%+v, %t), want stateless", md, ok)
			}
			if got := lcLiveTxCount(elm); got != 0 {
				t.Fatalf("stateless transactions before response = %d, want 0", got)
			}
			if got := srvReqCount(prx, elm); got != 0 {
				t.Fatalf("stateless coordinators before response = %d, want 0", got)
			}
			res := newInternalInRes(t, sends[0], sip.ResponseStatusTemporarilyUnavailable)
			next := sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error {
				return errors.ErrorWrap("stateless response escaped proxy ownership")
			})
			if err := prx.InterceptInboundResponse(ctx, next, res); err != nil {
				t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
			}
			synctest.Wait()
			if diff := cmp.Diff([]int{480}, resStatusList(drainInternalRess(tp))); diff != "" {
				t.Errorf("stateless wire statuses (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]RequestOutcome{OutcomeSent}, completions); diff != "" {
				t.Errorf("stateless completion outcomes (-want +got):\n%s", diff)
			}
			if got := lcLiveTxCount(elm); got != 0 {
				t.Errorf("stateless transactions after response = %d, want 0", got)
			}
			if got := srvReqCount(prx, elm); got != 0 {
				t.Errorf("stateless coordinators after response = %d, want 0", got)
			}
			if got := calls.Load(); got != 0 {
				t.Errorf("route.ResolveFinalResponse calls = %d, want 0", got)
			}
		})
	})

	t.Run("pre-send forking retains configured callback", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			plan := newInternalLitPlan()
			plan.Groups[0] = append(plan.Groups[0], lcTarget("B", "10.0.0.10"))
			var resolved, calls atomic.Int32
			var selection FinalResponseSelection
			route := Route{
				Name:        "resolver-promotion",
				ForwardMode: ForwardModeStateless,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
					resolved.Add(1)
					return plan, nil
				},
				ResolveFinalResponse: func(_ context.Context, input FinalResponseSelection) (int, error) {
					calls.Add(1)
					selection = input
					return 1, nil
				},
			}
			elm, prx, tp := newInternalTestProxy(t, route, Options{
				StatelessKey: []byte("resolver-promotion-key"), ServedDomains: []string{"alice.voip.com"},
			})
			req := newInternalInviteReq(sip.MagicCookie + ".resolver-promotion")
			rq := admitInternalReq(t, elm, prx, req)
			synctest.Wait()
			sends := drainInternalReqs(tp)
			if len(sends) != 2 {
				t.Fatalf("promoted branch sends = %d, want 2", len(sends))
			}
			for _, out := range sends {
				md, ok := MetadataFrom(out.Metadata())
				if !ok || md.Mode != ForwardModeStateful || md.RouteName != route.Name || md.Stage != StageForward {
					t.Errorf("promoted send metadata = (%+v, %t), want stateful original route", md, ok)
				}
			}
			for i, id := range []string{"A", "B"} {
				tx := finalResolverTarget(t, elm, rq, id)
				if err := tx.RecvResponse(t.Context(), newInternalInRes(t, tx.Request(), []sip.ResponseStatus{480, 486}[i])); err != nil {
					t.Fatalf("promoted clnTx.RecvResponse() error = %v, want nil", err)
				}
				synctest.Wait()
				if i == 0 {
					if got := len(drainInternalRess(tp)); got != 0 {
						t.Errorf("promoted upstream sends with live sibling = %d, want 0", got)
					}
					if got := calls.Load(); got != 0 {
						t.Errorf("promoted resolver calls with live sibling = %d, want 0", got)
					}
				}
			}
			if got := resolved.Load(); got != 1 {
				t.Errorf("route.ResolveRequestTargets calls = %d, want 1 (no replay on promotion)", got)
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("configured route.ResolveFinalResponse calls = %d, want 1", got)
			}
			if diff := cmp.Diff([]int{480, 486}, resStatusList(selection.Candidates)); diff != "" {
				t.Errorf("promoted arrival-ordered candidates (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]int{486}, resStatusList(drainInternalRess(tp))); diff != "" {
				t.Errorf("promoted selected wire statuses (-want +got):\n%s", diff)
			}
			snap, err := rq.Snapshot()
			if err != nil {
				t.Fatalf("rq.Snapshot() error = %v, want nil", err)
			}
			if !snap.Completed || snap.Mode != ForwardModeStateful || snap.Outcome != OutcomeResponse || snap.FinalStatus != 486 {
				t.Errorf("promoted completed/mode/outcome/final = %t/%v/%v/%d, want true/stateful/response/486",
					snap.Completed, snap.Mode, snap.Outcome, snap.FinalStatus)
			}
		})
	})
}

// TestProxy_StatelessSendPolicy exercises the private send-policy path of
// stateless forwarding: the promotion decision per method, the promotion
// marker returned before any wire send, and the instance-local fallback key
// reserved for the mandatory unmatched-CANCEL path of a stateful-only proxy.
func TestProxy_StatelessSendPolicy(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	key := []byte("stateless-policy-key")
	slRoute := Route{Name: "sl", ForwardMode: ForwardModeStateless}
	stRoute := Route{Name: "st", ForwardMode: ForwardModeStateful}

	t.Run("promotion eligibility", func(t *testing.T) {
		t.Parallel()

		invite := newInternalInviteReq(sip.MagicCookie + ".sls-policy-1")
		ack := statelessRelatedOf(invite, sip.RequestMethodAck, "tag-1")
		cancel := statelessRelatedOf(invite, sip.RequestMethodCancel, "")

		prx := newStatelessTestProxy(t, key)
		if !prx.statelessPromotable(nil, invite) {
			t.Fatal("INVITE is not promote-eligible, want eligible")
		}
		if prx.statelessPromotable(nil, ack) {
			t.Fatal("ACK is promote-eligible, want never promoted")
		}
		if prx.statelessPromotable(nil, cancel) {
			t.Fatal("unmatched CANCEL is promote-eligible, want stateless-only")
		}
	})

	t.Run("matched cancel promotable", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			route := Route{
				Name:        "st",
				ForwardMode: ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
					return newInternalLitPlan(), nil
				},
			}
			elm, prx, _ := newInternalTestProxy(t, route, Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			invite := newInternalInviteReq(sip.MagicCookie + ".sls-policy-2")
			admitInternalReq(t, elm, prx, invite)
			synctest.Wait()

			cancel := statelessRelatedOf(invite, sip.RequestMethodCancel, "")
			if !prx.statelessPromotable(elm, cancel) {
				t.Fatal("matched CANCEL is not promote-eligible, want eligible")
			}
		})
	})

	t.Run("promotion marker", func(t *testing.T) {
		t.Parallel()

		elm, prx, tp := newInternalTestProxy(t, slRoute, Options{StatelessKey: key})

		invite := newInternalInviteReq(sip.MagicCookie + ".sls-policy-3")
		err := prx.forwardStateless(ctx, elm, &slRoute, invite, statelessHostPolicyTarget())
		if err == nil {
			t.Fatal("prx.forwardStateless() error = nil, want destination failure")
		}
		if !errors.Is(err, errStatelessPromote) {
			t.Fatalf("prx.forwardStateless() error = %v, want %v", err, errStatelessPromote)
		}
		if !errors.Is(err, errNoStatelessDest) {
			t.Fatalf("prx.forwardStateless() error = %v, want %v", err, errNoStatelessDest)
		}

		ack := statelessRelatedOf(invite, sip.RequestMethodAck, "tag-1")
		err = prx.forwardStateless(ctx, elm, &slRoute, ack, statelessHostPolicyTarget())
		if err == nil {
			t.Fatal("prx.forwardStateless() ACK error = nil, want routing failure")
		}
		if errors.Is(err, errStatelessPromote) {
			t.Fatalf("prx.forwardStateless() ACK error = %v, want no promotion marker", err)
		}

		cancel := statelessRelatedOf(invite, sip.RequestMethodCancel, "")
		err = prx.forwardStateless(ctx, elm, &slRoute, cancel, statelessHostPolicyTarget())
		if err == nil {
			t.Fatal("prx.forwardStateless() CANCEL error = nil, want routing failure")
		}
		if errors.Is(err, errStatelessPromote) {
			t.Fatalf("prx.forwardStateless() CANCEL error = %v, want no promotion marker", err)
		}

		if got := len(tp.sendReqCalls); got != 0 {
			t.Fatalf("sends=%d, want 0 (no wire emit on policy failures)", got)
		}
	})

	t.Run("keyless cancel fallback", func(t *testing.T) {
		t.Parallel()

		// A stateful-only proxy carries an instance-local fallback key used
		// solely by the mandatory unmatched-CANCEL stateless path.
		elm, prx, tp := newInternalTestProxy(t, stRoute, Options{})

		invite := newInternalInviteReq(sip.MagicCookie + ".sls-policy-4")
		cancel := statelessRelatedOf(invite, sip.RequestMethodCancel, "")

		err := prx.forwardStatelessReq(ctx, elm, &stRoute, cancel, cancel, statelessPolicyPlan(statelessTestTarget()))
		if err != nil {
			t.Fatalf("prx.forwardStatelessReq() error = %v, want nil", err)
		}

		sent := testutil.WaitResult(t, tp.sendReqCalls, "stateless CANCEL send")
		var branch string
		sent.WithMessage(func(r *sip.Request) {
			if via, ok := r.Headers.FirstVia(); ok {
				branch, _ = via.Branch()
			}
		})
		if !strings.HasPrefix(branch, sip.MagicCookie+"."+branchStatelessMark+".") {
			t.Fatalf("top via branch = %q, want proxy-stateless mark", branch)
		}
	})

	t.Run("fallback key scoped to cancel", func(t *testing.T) {
		t.Parallel()

		elm, prx, _ := newInternalTestProxy(t, stRoute, Options{})
		invite := newInternalInviteReq(sip.MagicCookie + ".sls-policy-5")
		if got := prx.statelessBranchToken(elm, invite, statelessTestTarget()); got != "" {
			t.Fatalf("prx.statelessBranchToken() INVITE = %q, want empty (no fallback for response-eligible)", got)
		}
		cancel := statelessRelatedOf(invite, sip.RequestMethodCancel, "")
		if got := prx.statelessBranchToken(elm, cancel, statelessTestTarget()); got == "" {
			t.Fatal("prx.statelessBranchToken() CANCEL = empty, want fallback-key token")
		}

		// Element-local: two elements of one keyless proxy derive distinct
		// CANCEL tokens, and a second proxy's element differs again.
		elmB, _, _ := newInternalTestProxy(t, stRoute, Options{})
		if got := prx.statelessBranchToken(elmB, cancel, statelessTestTarget()); got == "" {
			t.Fatal("sibling element prx.statelessBranchToken() CANCEL = empty, want fallback-key token")
		} else if got == prx.statelessBranchToken(elm, cancel, statelessTestTarget()) {
			t.Fatal("element-local fallback keys collided across elements")
		}
	})
}
