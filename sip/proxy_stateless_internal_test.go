package sip

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip/header"
)

func newStatelessTestProxy(tb testing.TB, key []byte) *Proxy {
	tb.Helper()

	elm := &Element{}
	tb.Cleanup(func() { _ = elm.Close() })

	return &Proxy{
		elm:  elm,
		opts: ProxyOptions{StatelessKey: key},
	}
}

func statelessTestURI() *URI {
	return &URI{User: MakeUserInfo("alice"), Addr: MakeHostAddr("alice.example.com")}
}

func statelessTestTarget() ProxyTarget {
	return ProxyTarget{
		ID:  "t1",
		URI: &URI{User: MakeUserInfo("alice"), Addr: MakeHostPortAddr("198.51.100.10", 5060)},
	}
}

func TestProxy_stateless_single_target(t *testing.T) {
	t.Parallel()

	a := statelessTestTarget()
	a.Attributes = map[string]string{"role": "primary"}
	a.Delivery = ProxyDeliveryRef{Kind: "binding", Key: "A"}
	b := statelessTestTarget()
	b.ID = "t2"
	b.Attributes = map[string]string{"role": "backup"}
	b.Delivery = ProxyDeliveryRef{Kind: "binding", Key: "B"}
	other := statelessTestTarget()
	other.ID = "other"
	other.URI.Addr = MakeHostPortAddr("198.51.100.11", 5060)
	tests := []struct {
		name string
		plan ProxyTargetPlan
		want ProxyTarget
		ok   bool
	}{
		{name: "zero targets"},
		{name: "empty groups", plan: ProxyTargetPlan{Groups: [][]ProxyTarget{{}, {}}}},
		{name: "one target", plan: ProxyTargetPlan{Groups: [][]ProxyTarget{{a}}}, want: a, ok: true},
		{name: "empty surrounding groups", plan: ProxyTargetPlan{Groups: [][]ProxyTarget{{}, {a}, {}}}, want: a, ok: true},
		{name: "same group", plan: ProxyTargetPlan{Groups: [][]ProxyTarget{{a, other}}}},
		{name: "cross group", plan: ProxyTargetPlan{Groups: [][]ProxyTarget{{a}, {}, {other}}}},
		{name: "same uri distinct ids", plan: ProxyTargetPlan{Groups: [][]ProxyTarget{{a, b}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			before, err := clonePrxTargetPlan(tt.plan)
			if err != nil {
				t.Fatalf("clonePrxTargetPlan() error = %v, want nil", err)
			}
			got, ok := singlePrxTarget(tt.plan)
			if ok != tt.ok {
				t.Errorf("singlePrxTarget() ok = %v, want %v", ok, tt.ok)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("singlePrxTarget() target (-want +got): %s", diff)
			}
			if diff := cmp.Diff(before, tt.plan); diff != "" {
				t.Errorf("singlePrxTarget() mutated plan (-want +got): %s", diff)
			}
		})
	}
}

// statelessRelatedOf derives a related-method fixture (CANCEL or a
// non-routed ACK) from an INVITE envelope: the top Via, From tag, Call-ID,
// CSeq number, and Request-URI are preserved while the method and the CSeq
// method change. A non-empty toTag installs the acknowledged response's tag
// on the To header.
func statelessRelatedOf(inv *RequestEnvelope, method RequestMethod, toTag string) *RequestEnvelope {
	env := cloneReqEnvelope(inv)
	env.WithMessage(func(r *Request) {
		r.Method = method
		if cseq, ok := r.Headers.CSeq(); ok {
			r.Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: method})
		}
		if toTag != "" {
			if to, ok := r.Headers.To(); ok {
				if to.Params == nil {
					to.Params = make(Values)
				}
				to.Params.Set("tag", toTag)
			}
		}
	})
	return env
}

func statelessLegacyVia(env *RequestEnvelope) {
	env.WithMessage(func(r *Request) {
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
	invite := newValidationReq(RequestMethodInvite, statelessTestURI())
	assertValidStatelessBranch := func(tb testing.TB, branch string) {
		tb.Helper()

		if !IsRFC3261Branch(branch) {
			tb.Fatalf("prx.statelessBranch() = %q is not a valid RFC 3261 branch", branch)
		}
		if !strings.HasPrefix(branch, MagicCookie+"."+prxBranchStatelessMark+".") {
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

		cancel, err := NewCancelRequestEnvelope(invite)
		if err != nil {
			t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
		}
		ack := statelessRelatedOf(invite, RequestMethodAck, "down-tag-1")

		branchInvite := prx.statelessBranch(invite, target)
		branchCancel := prx.statelessBranch(cancel, target)
		branchAck := prx.statelessBranch(ack, target)

		if branchInvite != branchCancel || branchInvite != branchAck {
			t.Fatalf("related methods changed branch: invite=%q cancel=%q ack=%q", branchInvite, branchCancel, branchAck)
		}
	})

	t.Run("to tag normalized", func(t *testing.T) {
		t.Parallel()

		tagged := statelessRelatedOf(invite, RequestMethodInvite, "down-tag-2")

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
		routedAck := statelessRelatedOf(invite, RequestMethodAck, "down-tag-3")
		routedAck.WithMessage(func(r *Request) {
			if via, ok := r.Headers.FirstVia(); ok {
				via.Params.Set("branch", MagicCookie+".validate-ack")
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
		legacyCancel := statelessRelatedOf(legacyInvite, RequestMethodCancel, "")
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
		other.URI = &URI{User: MakeUserInfo("alice"), Addr: MakeHostPortAddr("198.51.100.20", 5060)}

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

		in := newValidationReq(RequestMethodInvite, statelessTestURI())
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
func statelessPolicyPlan(trgt ProxyTarget) ProxyTargetPlan {
	return ProxyTargetPlan{Groups: [][]ProxyTarget{{trgt}}}
}

func statelessHostPolicyTarget() ProxyTarget {
	return ProxyTarget{
		ID:  "h1",
		URI: &URI{User: MakeUserInfo("alice"), Addr: MakeHostAddr("alice.example.org")},
	}
}

// TestProxy_StatelessSendPolicy exercises the private send-policy path of
// stateless forwarding: the promotion decision per method, the promotion
// marker returned before any wire send, and the instance-local fallback key
// reserved for the mandatory unmatched-CANCEL path of a stateful-only proxy.
func TestProxy_StatelessSendPolicy(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	key := []byte("stateless-policy-key")
	slRoute := ProxyRoute{Name: "sl", ForwardMode: ForwardModeStateless}
	stRoute := ProxyRoute{Name: "st", ForwardMode: ForwardModeStateful}

	t.Run("promotion eligibility", func(t *testing.T) {
		t.Parallel()

		invite := newInternalInviteReq(MagicCookie + ".sls-policy-1")
		ack := statelessRelatedOf(invite, RequestMethodAck, "tag-1")
		cancel := statelessRelatedOf(invite, RequestMethodCancel, "")

		prx := newStatelessTestProxy(t, key)
		if !prx.statelessPromotable(invite) {
			t.Fatal("INVITE is not promote-eligible, want eligible")
		}
		if prx.statelessPromotable(ack) {
			t.Fatal("ACK is promote-eligible, want never promoted")
		}
		if prx.statelessPromotable(cancel) {
			t.Fatal("unmatched CANCEL is promote-eligible, want stateless-only")
		}
	})

	t.Run("matched cancel promotable", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			route := ProxyRoute{
				Name:        "st",
				ForwardMode: ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
					return newInternalLitPlan(), nil
				},
			}
			_, prx, _ := newInternalTestProxy(t, route, ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			})

			invite := newInternalInviteReq(MagicCookie + ".sls-policy-2")
			admitInternalReq(t, prx, invite)
			synctest.Wait()

			cancel := statelessRelatedOf(invite, RequestMethodCancel, "")
			if !prx.statelessPromotable(cancel) {
				t.Fatal("matched CANCEL is not promote-eligible, want eligible")
			}
		})
	})

	t.Run("promotion marker", func(t *testing.T) {
		t.Parallel()

		_, prx, tp := newInternalTestProxy(t, slRoute, ProxyOptions{StatelessKey: key})

		invite := newInternalInviteReq(MagicCookie + ".sls-policy-3")
		err := prx.forwardStateless(ctx, &slRoute, invite, statelessHostPolicyTarget())
		if err == nil {
			t.Fatal("prx.forwardStateless() error = nil, want destination failure")
		}
		if !errors.Is(err, errPrxStatelessPromote) {
			t.Fatalf("prx.forwardStateless() error = %v, want %v", err, errPrxStatelessPromote)
		}
		if !errors.Is(err, errNoStatelessDest) {
			t.Fatalf("prx.forwardStateless() error = %v, want %v", err, errNoStatelessDest)
		}

		ack := statelessRelatedOf(invite, RequestMethodAck, "tag-1")
		err = prx.forwardStateless(ctx, &slRoute, ack, statelessHostPolicyTarget())
		if err == nil {
			t.Fatal("prx.forwardStateless() ACK error = nil, want routing failure")
		}
		if errors.Is(err, errPrxStatelessPromote) {
			t.Fatalf("prx.forwardStateless() ACK error = %v, want no promotion marker", err)
		}

		cancel := statelessRelatedOf(invite, RequestMethodCancel, "")
		err = prx.forwardStateless(ctx, &slRoute, cancel, statelessHostPolicyTarget())
		if err == nil {
			t.Fatal("prx.forwardStateless() CANCEL error = nil, want routing failure")
		}
		if errors.Is(err, errPrxStatelessPromote) {
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
		_, prx, tp := newInternalTestProxy(t, stRoute, ProxyOptions{})

		invite := newInternalInviteReq(MagicCookie + ".sls-policy-4")
		cancel := statelessRelatedOf(invite, RequestMethodCancel, "")

		err := prx.forwardStatelessReq(ctx, &stRoute, cancel, cancel, statelessPolicyPlan(statelessTestTarget()))
		if err != nil {
			t.Fatalf("prx.forwardStatelessReq() error = %v, want nil", err)
		}

		sent := testutil.WaitResult(t, tp.sendReqCalls, "stateless CANCEL send")
		var branch string
		sent.WithMessage(func(r *Request) {
			if via, ok := r.Headers.FirstVia(); ok {
				branch, _ = via.Branch()
			}
		})
		if !strings.HasPrefix(branch, MagicCookie+"."+prxBranchStatelessMark+".") {
			t.Fatalf("top via branch = %q, want proxy-stateless mark", branch)
		}
	})

	t.Run("fallback key scoped to cancel", func(t *testing.T) {
		t.Parallel()

		_, prx, _ := newInternalTestProxy(t, stRoute, ProxyOptions{})
		invite := newInternalInviteReq(MagicCookie + ".sls-policy-5")
		if got := prx.statelessBranchToken(invite, statelessTestTarget()); got != "" {
			t.Fatalf("prx.statelessBranchToken() INVITE = %q, want empty (no fallback for response-eligible)", got)
		}
		cancel := statelessRelatedOf(invite, RequestMethodCancel, "")
		if got := prx.statelessBranchToken(cancel, statelessTestTarget()); got == "" {
			t.Fatal("prx.statelessBranchToken() CANCEL = empty, want fallback-key token")
		}

		// Instance-local: two keyless proxies derive distinct CANCEL tokens.
		_, other, _ := newInternalTestProxy(t, stRoute, ProxyOptions{})
		if got := other.statelessBranchToken(cancel, statelessTestTarget()); got == "" {
			t.Fatal("sibling prx.statelessBranchToken() CANCEL = empty, want fallback-key token")
		} else if got == prx.statelessBranchToken(cancel, statelessTestTarget()) {
			t.Fatal("instance-local fallback keys collided across proxies")
		}
	})
}
