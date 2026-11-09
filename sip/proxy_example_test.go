package sip_test

import (
	"context"
	"fmt"
	"net/netip"
	"testing"

	"github.com/ghettovoice/gosip/sip"
)

// ExampleNewProxy configures a proxy with a single route that claims every
// request addressed to a served domain and resolves a static literal-IP
// target plan. ServerTransactionOptions stay at their zero value, so the
// upstream non-INVITE transactions keep the default-off RFC 4320 behavior.
func ExampleNewProxy() {
	elm := &sip.Element{}
	defer func() { _ = elm.Close() }()

	prx, err := sip.NewProxy(elm, []sip.ProxyRoute{
		{
			Name: "static-location",
			// All other route hooks stay nil: a nil matcher accepts every
			// request at this route's position, a nil authorizer allows
			// forwarding, and a nil selector follows the ordered plan groups.
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
				// One ordered group with a single target; groups are
				// attempted in order and targets within a group together.
				return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
					{{
						ID: "alice-desk",
						URI: &sip.URI{
							User: sip.MakeUserInfo("alice"),
							Addr: sip.MakeHostPortAddr("192.0.2.10", 5060),
						},
					}},
				}}, nil
			},
		},
	}, sip.ProxyOptions{
		ServedDomains: []string{"example.com"},
		TimerC:        0, // zero selects the five-minute default
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("proxy ready:", prx != nil)

	// Output:
	// proxy ready: true
}

// ExampleProxy_stateless configures a route that forwards requests without
// transactions. An explicit stable StatelessKey is required: NewProxy
// rejects a stateless route configured without one, because the key
// authenticates the deterministic branch derivation that lets the proxy
// emit an identical request on every retransmission and recognize its own
// responses afterwards.
//
// Stateless forwarding commits to exactly one destination — a final remote
// address and a bound client transport — selected before the first send. A
// literal-IP target like the one below uses the built-in deterministic
// selection; a hostname or otherwise custom target is reachable statelessly
// only through ProxyOptions.ResolveStatelessDestination, because ordinary
// mutable or weighted DNS ordering is not a determinism guarantee. When the
// invariants cannot be established a response-eligible request is promoted
// to stateful forwarding before the first send; after the send there is no
// failover to an alternate destination. RFC 5626 Outbound is not supported:
// the proxy never advertises the outbound option tag.
func ExampleProxy_stateless() {
	elm := &sip.Element{}
	defer func() { _ = elm.Close() }()

	prx, err := sip.NewProxy(elm, []sip.ProxyRoute{
		{
			Name:        "stateless-location",
			ForwardMode: sip.ForwardModeStateless,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
				// A literal-IP next hop resolves deterministically without
				// DNS and never consults ResolveStatelessDestination.
				return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
					{{
						ID: "alice-desk",
						URI: &sip.URI{
							User: sip.MakeUserInfo("alice"),
							Addr: sip.MakeHostPortAddr("192.0.2.10", 5060),
						},
					}},
				}}, nil
			},
		},
	}, sip.ProxyOptions{
		// The key is a stable application secret shared by every proxy
		// instance that must derive identical stateless branches; deploy it
		// like a signing key.
		StatelessKey: []byte("site-specific-stable-secret"),
		LocalIdentities: []*sip.URI{
			{Addr: sip.MakeHostAddr("proxy.example.com")},
		},
		ServedDomains: []string{"example.com"},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("proxy ready:", prx != nil)

	// Output:
	// proxy ready: true
}

// ExampleProxy_statelessDestination shows the
// ProxyOptions.ResolveStatelessDestination callback contract a hostname or
// otherwise custom stateless target needs. The callback must return exactly
// one stable destination — a bound client transport plus the final remote
// address — identical for every retransmission of the request; a result
// drawn from ordinary DNS resolution order is not stable enough. A nil
// transport or an invalid remote address is rejected, and an error promotes
// a response-eligible request to stateful forwarding before the first send.
func ExampleProxy_statelessDestination() {
	elm := &sip.Element{}
	defer func() { _ = elm.Close() }()

	prx, err := sip.NewProxy(elm, []sip.ProxyRoute{
		{
			Name:        "stateless-location",
			ForwardMode: sip.ForwardModeStateless,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
				// A hostname target reaches the wire statelessly only
				// through the resolver configured below.
				return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
					{{
						ID: "alice-desk",
						URI: &sip.URI{
							User: sip.MakeUserInfo("alice"),
							Addr: sip.MakeHostPortAddr("phone.example.net", 5060),
						},
					}},
				}}, nil
			},
		},
	}, sip.ProxyOptions{
		StatelessKey:  []byte("site-specific-stable-secret"),
		ServedDomains: []string{"example.com"},
		ResolveStatelessDestination: func(
			_ context.Context, _ *sip.RequestEnvelope, target sip.ProxyTarget,
		) (sip.ProxyBoundDelivery, error) {
			// In a real deployment the transport and the remote address come
			// from the application's stable uplink assignment, not from a
			// fresh DNS lookup.
			tp, ok := elm.TransportByProto(sip.UDPMetadata().Proto)
			if !ok {
				return sip.ProxyBoundDelivery{}, fmt.Errorf("no UDP transport for target %s", target.ID)
			}
			return sip.ProxyBoundDelivery{
				Transport:  tp,
				RemoteAddr: netip.MustParseAddrPort("192.0.2.10:5060"),
			}, nil
		},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("proxy ready:", prx != nil)

	// Output:
	// proxy ready: true
}

// ExampleNewProxy_rfc4320 configures a route whose upstream non-INVITE
// server transactions explicitly opt in to RFC 4320 behavior. The option is
// local to the route's transaction: it is off by default and never
// negotiated with peers.
func ExampleNewProxy_rfc4320() {
	elm := &sip.Element{}
	defer func() { _ = elm.Close() }()

	prx, err := sip.NewProxy(elm, []sip.ProxyRoute{
		{
			Name: "static-location",
			// Opt in to RFC 4320 non-INVITE handling on the upstream server
			// transaction: provisional responses other than 100 Trying and
			// 408 are rejected, and a 100 Trying is sent automatically once
			// the delayed-Trying deadline passes on unreliable transports.
			ServerTransactionOptions: sip.ServerTransactionOptions{
				NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320,
			},
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
				return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
					{{
						ID: "alice-desk",
						URI: &sip.URI{
							User: sip.MakeUserInfo("alice"),
							Addr: sip.MakeHostPortAddr("192.0.2.10", 5060),
						},
					}},
				}}, nil
			},
		},
	}, sip.ProxyOptions{
		ServedDomains: []string{"example.com"},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("proxy ready:", prx != nil)

	// Output:
	// proxy ready: true
}

// TestProxy_StatelessConfiguration asserts the construction-time stateless
// contract: an explicit stateless route is accepted only with a configured
// StatelessKey.
func TestProxy_StatelessConfiguration(t *testing.T) {
	t.Parallel()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	statelessRoutes := []sip.ProxyRoute{{
		Name:        "stateless",
		ForwardMode: sip.ForwardModeStateless,
	}}
	key := []byte("stateless-config-key")

	if _, err := sip.NewProxy(elm, statelessRoutes, sip.ProxyOptions{StatelessKey: key}); err != nil {
		t.Fatalf("stateless config: %v", err)
	}
	if _, err := sip.NewProxy(elm, statelessRoutes, sip.ProxyOptions{StatelessKey: nil}); err == nil {
		t.Fatal("explicit stateless route accepted without a key")
	}
}
