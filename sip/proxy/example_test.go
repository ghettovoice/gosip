package proxy_test

import (
	"context"
	"fmt"
	"net/netip"
	"testing"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/proxy"
)

func ExampleDefaultFinalResponseResolver() {
	from := &sip.URI{
		User: sip.MakeUserInfo("alice"),
		Addr: sip.MakeHostAddr("a.example.com"),
	}
	to := &sip.URI{
		User: sip.MakeUserInfo("bob"),
		Addr: sip.MakeHostAddr("b.example.com"),
	}
	msg, err := sip.NewRequest(sip.RequestMethodInvite, to, from, to)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	req := sip.NewRequestEnvelope(msg)
	unavailable, err := req.NewResponse(sip.ResponseStatusTemporarilyUnavailable)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	busy, err := req.NewResponse(sip.ResponseStatusBusyHere)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	index, err := proxy.DefaultFinalResponseResolver(context.Background(), proxy.FinalResponseSelection{
		Request:    req,
		Candidates: []*sip.ResponseEnvelope{unavailable, busy},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(index)

	// Output:
	// 0
}

// ExampleRoute_finalResponseResolver prefers 486 while preserving mandatory
// success/6xx precedence and explicitly delegating all other cases.
func ExampleRoute_finalResponseResolver() {
	route := proxy.Route{
		ResolveFinalResponse: func(ctx context.Context, selection proxy.FinalResponseSelection) (int, error) {
			// Successes belong to the core; a 6xx must beat other negatives.
			for _, candidate := range selection.Candidates {
				if candidate == nil {
					continue
				}
				if status := candidate.Status(); status.IsSuccessful() || status.IsGlobalFailure() {
					return proxy.DefaultFinalResponseResolver(ctx, selection)
				}
			}
			for i, candidate := range selection.Candidates {
				if candidate != nil && candidate.Status().Equal(sip.ResponseStatusBusyHere) {
					return i, nil
				}
			}
			return proxy.DefaultFinalResponseResolver(ctx, selection)
		},
	}

	from := &sip.URI{
		User: sip.MakeUserInfo("alice"),
		Addr: sip.MakeHostAddr("a.example.com"),
	}
	to := &sip.URI{
		User: sip.MakeUserInfo("bob"),
		Addr: sip.MakeHostAddr("b.example.com"),
	}
	msg, err := sip.NewRequest(sip.RequestMethodInvite, to, from, to)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	req := sip.NewRequestEnvelope(msg)
	unavailable, err := req.NewResponse(sip.ResponseStatusTemporarilyUnavailable)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	busy, err := req.NewResponse(sip.ResponseStatusBusyHere)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	// This runnable example calls the field directly, without starting a proxy.
	index, err := route.ResolveFinalResponse(context.Background(), proxy.FinalResponseSelection{
		Request:    req,
		Candidates: []*sip.ResponseEnvelope{unavailable, busy},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(index)

	// Output:
	// 1
}

// ExampleRoute_targetSelector composes a custom target-selection policy on
// top of the default: while attempts are active it offers only the first ID
// of the offered group, otherwise it delegates to the default first-group
// policy.
//
// Calling the hook directly demonstrates the policy only: the coordinator
// supplies at most one open group per selection and validates the returned
// IDs against unstarted, unfinished targets.
func ExampleRoute_targetSelector() {
	route := proxy.Route{
		SelectTargets: func(ctx context.Context, selection proxy.Selection) ([]string, error) {
			if len(selection.ActiveTargetIDs) > 0 &&
				len(selection.RemainingGroups) > 0 &&
				len(selection.RemainingGroups[0]) > 0 {
				return []string{selection.RemainingGroups[0][0].ID}, nil
			}
			return proxy.DefaultTargetSelector(ctx, selection)
		},
	}

	// This runnable example calls the field directly, without starting a proxy.
	ids, err := route.SelectTargets(context.Background(), proxy.Selection{
		RemainingGroups: [][]proxy.Target{{{ID: "a"}, {ID: "b"}}},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(ids)

	ids, err = route.SelectTargets(context.Background(), proxy.Selection{
		RemainingGroups: [][]proxy.Target{{{ID: "a"}, {ID: "b"}}},
		ActiveTargetIDs: []string{"running"},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(ids)

	// Output:
	// [a b]
	// [a]
}

// ExampleNew configures a proxy with a single route that claims every
// request addressed to a served domain and resolves a static literal-IP
// target plan. sip.ServerTransactionOptions stay at their zero value, so the
// upstream non-INVITE transactions keep the default-off RFC 4320 behavior.
//
// Construction is pure: proxy.New installs nothing. An explicit
// elm.UseMiddleware registration attaches the proxy to the element's
// processing chain, and the returned unbind detaches only that
// registration — it is never a shutdown path.
func ExampleNew() {
	elm := &sip.Element{}
	defer func() { _ = elm.Close() }()

	prx, err := proxy.New([]proxy.Route{
		{
			Name: "static-location",
			// All other route hooks stay nil: a nil matcher accepts every
			// request at this route's position, a nil authorizer allows
			// forwarding, and a nil selector follows the ordered plan groups.
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				// One ordered group with a single target; groups are
				// attempted in order and targets within a group together.
				return proxy.TargetPlan{Groups: [][]proxy.Target{
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
	}, proxy.Options{
		ServedDomains: []string{"example.com"},
		TimerC:        0, // zero selects the five-minute default
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	unbind := elm.UseMiddleware(prx)
	defer func() {
		// Retire owned proxy work before detaching the registration: the
		// outbound response interceptor must still be installed to settle
		// the sends of in-flight requests, so the instance-wide Close
		// runs before unbind.
		_ = prx.Close()
		unbind()
	}()
	fmt.Println("proxy ready:", prx != nil)

	// Output:
	// proxy ready: true
}

// ExampleProxy_stateless configures a route that forwards requests without
// transactions. An explicit stable StatelessKey is required: proxy.New
// rejects a stateless route configured without one, because the key
// authenticates the deterministic branch derivation that lets the proxy
// emit an identical request on every retransmission and recognize its own
// responses afterwards.
//
// Stateless forwarding commits to exactly one destination — a final remote
// address and a bound client transport — selected before the first send. A
// literal-IP target like the one below uses the built-in deterministic
// selection; a hostname or otherwise custom target is reachable statelessly
// only through proxy.Options.ResolveStatelessDestination, because ordinary
// mutable or weighted DNS ordering is not a determinism guarantee. When the
// invariants cannot be established a response-eligible request is promoted
// to stateful forwarding before the first send; after the send there is no
// failover to an alternate destination. RFC 5626 Outbound is not supported:
// the proxy never advertises the outbound option tag.
func ExampleProxy_stateless() {
	elm := &sip.Element{}
	defer func() { _ = elm.Close() }()

	prx, err := proxy.New([]proxy.Route{
		{
			Name:        "stateless-location",
			ForwardMode: proxy.ForwardModeStateless,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				// A literal-IP next hop resolves deterministically without
				// DNS and never consults ResolveStatelessDestination.
				return proxy.TargetPlan{Groups: [][]proxy.Target{
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
	}, proxy.Options{
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

	// Same explicit registration as ExampleNew: retire the shared proxy
	// lifecycle before detaching the middleware registration.
	unbind := elm.UseMiddleware(prx)
	defer func() {
		_ = prx.Close()
		unbind()
	}()
	fmt.Println("proxy ready:", prx != nil)

	// Output:
	// proxy ready: true
}

// ExampleProxy_statelessDestination shows the
// proxy.Options.ResolveStatelessDestination callback contract a hostname or
// otherwise custom stateless target needs. The callback must return exactly
// one stable destination — a bound client transport plus the final remote
// address — identical for every retransmission of the request; a result
// drawn from ordinary DNS resolution order is not stable enough. A nil
// transport or an invalid remote address is rejected, and an error promotes
// a response-eligible request to stateful forwarding before the first send.
func ExampleProxy_statelessDestination() {
	elm := &sip.Element{}
	defer func() { _ = elm.Close() }()

	prx, err := proxy.New([]proxy.Route{
		{
			Name:        "stateless-location",
			ForwardMode: proxy.ForwardModeStateless,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				// A hostname target reaches the wire statelessly only
				// through the resolver configured below.
				return proxy.TargetPlan{Groups: [][]proxy.Target{
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
	}, proxy.Options{
		StatelessKey:  []byte("site-specific-stable-secret"),
		ServedDomains: []string{"example.com"},
		ResolveStatelessDestination: func(
			_ context.Context, _ *sip.RequestEnvelope, target proxy.Target,
		) (proxy.BoundDelivery, error) {
			// In a real deployment the transport and the remote address come
			// from the application's stable uplink assignment, not from a
			// fresh DNS lookup.
			tp, ok := elm.TransportByProto(sip.UDPMetadata().Proto)
			if !ok {
				return proxy.BoundDelivery{}, fmt.Errorf("no UDP transport for target %s", target.ID)
			}
			return proxy.BoundDelivery{
				Transport:  tp,
				RemoteAddr: netip.MustParseAddrPort("192.0.2.10:5060"),
			}, nil
		},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	unbind := elm.UseMiddleware(prx)
	defer func() {
		_ = prx.Close()
		unbind()
	}()
	fmt.Println("proxy ready:", prx != nil)

	// Output:
	// proxy ready: true
}

// ExampleNew_rfc4320 configures a route whose upstream non-INVITE
// server transactions explicitly opt in to RFC 4320 behavior. The option is
// local to the route's transaction: it is off by default and never
// negotiated with peers.
func ExampleNew_rfc4320() {
	elm := &sip.Element{}
	defer func() { _ = elm.Close() }()

	prx, err := proxy.New([]proxy.Route{
		{
			Name: "static-location",
			// Opt in to RFC 4320 non-INVITE handling on the upstream server
			// transaction: provisional responses other than 100 Trying and
			// 408 are rejected, and a 100 Trying is sent automatically once
			// the delayed-Trying deadline passes on unreliable transports.
			ServerTransactionOptions: sip.ServerTransactionOptions{
				NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320,
			},
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{
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
	}, proxy.Options{
		ServedDomains: []string{"example.com"},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	unbind := elm.UseMiddleware(prx)
	defer func() {
		_ = prx.Close()
		unbind()
	}()
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

	statelessRoutes := []proxy.Route{{
		Name:        "stateless",
		ForwardMode: proxy.ForwardModeStateless,
	}}
	key := []byte("stateless-config-key")

	if _, err := proxy.New(statelessRoutes, proxy.Options{StatelessKey: key}); err != nil {
		t.Fatalf("stateless config: %v", err)
	}
	if _, err := proxy.New(statelessRoutes, proxy.Options{StatelessKey: nil}); err == nil {
		t.Fatal("explicit stateless route accepted without a key")
	}
}
