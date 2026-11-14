package proxy_test

import (
	"context"
	"iter"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/proxy"
)

func TestProxy_foreign_via_ownership(t *testing.T) {
	t.Parallel()

	for _, mode := range []proxy.ForwardMode{proxy.ForwardModeStateful, proxy.ForwardModeStateless} {
		for _, detect := range []bool{false, true} {
			name := mode.String() + "/disabled"
			if detect {
				name = mode.String() + "/enabled"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				synctest.Test(t, func(t *testing.T) {
					a := netip.MustParseAddrPort("192.0.2.10:5060")
					b := netip.MustParseAddrPort("192.0.2.20:5060")
					dest := netip.MustParseAddrPort("198.51.100.10:5060")
					newProxy := func(local, remote netip.AddrPort, key string) (*sip.Element, *proxy.Proxy, *sentByTransp) {
						tp := &sentByTransp{
							stubTransp: newStubTransp(),
							sentBy: []sip.Addr{
								sip.MakeHostPortAddr(local.Addr().String(), local.Port()),
							},
						}
						elm := &sip.Element{}
						t.Cleanup(func() { _ = elm.Close() })
						if err := elm.TrackTransport(tp); err != nil {
							t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
						}
						target := statelessLitTarget("same-target", dest.Addr().String())
						target.Delivery = proxy.DeliveryRef{Kind: "test", Key: "bound"}
						route := statelessPlanRoute(statelessPlan(target))
						route.ForwardMode = mode
						prx, err := proxy.New([]proxy.Route{*route}, proxy.Options{
							DetectLoops:     detect,
							StatelessKey:    []byte(key),
							LocalIdentities: []*sip.URI{{Addr: tp.sentBy[0]}},
							ServedDomains:   []string{"alice.voip.com", dest.Addr().String()},
							ResolveDelivery: func(context.Context, proxy.DeliveryRef) (proxy.BoundDelivery, error) {
								return proxy.BoundDelivery{Transport: tp, LocalAddr: local, RemoteAddr: remote}, nil
							},
						})
						if err != nil {
							t.Fatalf("proxy.New() error = %v, want nil", err)
						}
						elm.UseComponent(prx)
						return elm, prx, tp
					}
					elmA, prxA, tpA := newProxy(a, b, "independent-proxy-a-key")
					elmB, prxB, tpB := newProxy(b, dest, "independent-proxy-b-key")
					next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
						t.Error("next receiver called, want claimed request")
						return nil
					})
					in := newInNonInviteReq(t, "UDP", sip.MagicCookie+".foreign-owner", a,
						netip.MustParseAddrPort("203.0.113.5:5060"))
					if err := prxA.InterceptInboundRequest(sip.ContextWithElement(t.Context(), elmA), next, in); err != nil {
						t.Fatalf("A first traversal error = %v, want nil", err)
					}
					sentA := testutil.WaitResult(t, tpA.sendReqCalls, "A emitted request").req
					viaA, ok := sentA.Message().Headers.FirstVia()
					if !ok || !tpA.MatchSentBy(viaA.Addr) || tpB.MatchSentBy(viaA.Addr) {
						t.Fatal("A emitted Via must belong only to A's tracked transport")
					}
					inB := sip.NewRequestEnvelope(sentA.Message().Clone().(*sip.Request)).
						SetTransport(sip.UDPMetadata()).SetLocalAddr(b).SetRemoteAddr(a)
					if err := prxB.InterceptInboundRequest(sip.ContextWithElement(t.Context(), elmB), next, inB); err != nil {
						t.Fatalf("B first traversal with foreign Via error = %v, want nil", err)
					}
					sentB := testutil.WaitResult(t, tpB.sendReqCalls, "B emitted request").req
					viaB, ok := sentB.Message().Headers.FirstVia()
					if !ok || !tpB.MatchSentBy(viaB.Addr) || tpA.MatchSentBy(viaB.Addr) {
						t.Fatal("B emitted Via must belong only to B's tracked transport")
					}
					// The private companion checks these against the fingerprint
					// functions; equal wire components also pin this public fixture.
					fpA := strings.Split(statelessTopBranch(t, sentA), ".")[2]
					fpB := strings.Split(statelessTopBranch(t, sentB), ".")[2]
					if fpA != fpB {
						t.Fatalf("emitted routing fingerprints A=%q B=%q, want equal", fpA, fpB)
					}
					loop := sip.NewRequestEnvelope(sentB.Message().Clone().(*sip.Request)).
						SetTransport(sip.UDPMetadata()).SetLocalAddr(b).SetRemoteAddr(dest)
					err := prxB.InterceptInboundRequest(sip.ContextWithElement(t.Context(), elmB), next, loop)
					if !detect {
						if err != nil {
							t.Fatalf("B returning request error = %v, want nil with detection disabled", err)
						}
						testutil.WaitResult(t, tpB.sendReqCalls, "B disabled loop control")
						return
					}
					var rej *sip.RequestRejectedError
					if !errors.As(err, &rej) || rej.ResponseStatus() != sip.ResponseStatusLoopDetected {
						t.Fatalf("B returning request error = %v, want rejection 482", err)
					}
					synctest.Wait()
					if got := len(drainReqs(tpB.stubTransp)); got != 0 {
						t.Fatalf("B loop downstream sends = %d, want 0", got)
					}
				})
			})
		}
	}
}

// TestProxy_StatelessDispatch pins the stateless route invariants that stay
// observable on the public pipeline: a claimed request is consumed without a
// server transaction, an upstream SIP response, or a next-receiver dispatch.
func TestProxy_StatelessDispatch(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	var nexted, authorized atomic.Int32
	route := &proxy.Route{
		Name:        "stateless",
		ForwardMode: proxy.ForwardModeStateless,
		AuthorizeRequest: func(context.Context, *sip.RequestEnvelope) error {
			authorized.Add(1)
			return nil
		},
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
			return proxy.TargetPlan{Groups: [][]proxy.Target{
				{{
					ID:  "t1",
					URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr("198.51.100.10", 5060)},
				}},
			}}, nil
		},
	}
	elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
		StatelessKey:  []byte("stateless-dispatch-key"),
		ServedDomains: []string{"alice.voip.com"},
	})

	next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
		nexted.Add(1)
		return nil
	})

	req := newInInviteReq(t, "UDP", sip.MagicCookie+".sls-dispatch", laddr, raddr)
	key, err := sip.MakeServerTransactionKey(req)
	if err != nil {
		t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
	}

	if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), next, req); err != nil {
		t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
	}

	if got := nexted.Load(); got != 0 {
		t.Fatalf("next receiver called %d times, want 0 (route claimed the request)", got)
	}
	if got := authorized.Load(); got != 1 {
		t.Fatalf("route.AuthorizeRequest() called %d times, want 1", got)
	}
	if _, ok := elm.LoadServerTransaction(key); ok {
		t.Fatal("stateless route created a server transaction, want none")
	}
	if got := tp.countSendRes(); got != 0 {
		t.Fatalf("upstream responses = %d, want 0 (no provisional on the stateless path)", got)
	}

	// A retransmitted request is another transaction-free stateless
	// operation.
	if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), next, req); err != nil {
		t.Fatalf("retransmission prx.InterceptInboundRequest() error = %v, want nil", err)
	}
	if _, ok := elm.LoadServerTransaction(key); ok {
		t.Fatal("retransmission created a server transaction, want none")
	}
}

// statelessBoundClnTransp is a bound client-transport stub for the stateless
// destination tests: it records every send and reports an injected failure
// without ever dialing another destination.
type statelessBoundClnTransp struct {
	sendReqCalls chan sendReqCall
	sendErr      error
	sends        atomic.Int32
}

func newStatelessBoundClnTransp() *statelessBoundClnTransp {
	return &statelessBoundClnTransp{sendReqCalls: make(chan sendReqCall, 8)}
}

func (*statelessBoundClnTransp) Metadata() sip.TransportMetadata { return sip.UDPMetadata() }

func (tp *statelessBoundClnTransp) SendRequest(
	ctx context.Context,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	tp.sends.Add(1)
	if tp.sendErr != nil {
		return errors.Wrap(tp.sendErr)
	}
	call := sendReqCall{ctx: ctx, req: req}
	if len(opts) > 0 {
		call.opts = opts[len(opts)-1]
	}
	tp.sendReqCalls <- call
	return nil
}

// statelessEmptyLocator resolves no next-hop candidates, so a promoted
// request can never emit a wire send inside the test.
type statelessEmptyLocator struct{}

func (statelessEmptyLocator) LookupRequestAddrs(
	context.Context, *sip.URI, ...sip.LookupMessageAddrsOptions,
) iter.Seq[sip.ResolvedAddr] {
	return func(func(sip.ResolvedAddr) bool) {}
}

// statelessFixedLocator emits exactly one UDP candidate, so a promoted
// request resolves a stateful attempt without touching DNS.
type statelessFixedLocator struct {
	addr netip.AddrPort
}

func (l statelessFixedLocator) LookupRequestAddrs(
	context.Context, *sip.URI, ...sip.LookupMessageAddrsOptions,
) iter.Seq[sip.ResolvedAddr] {
	return func(yield func(sip.ResolvedAddr) bool) {
		yield(sip.ResolvedAddr{Transport: "UDP", Addr: l.addr})
	}
}

func statelessHostTarget() proxy.Target {
	return proxy.Target{
		ID:  "h1",
		URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.example.org")},
	}
}

func statelessLitTarget(id, host string) proxy.Target {
	return proxy.Target{
		ID:  id,
		URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr(host, 5060)},
	}
}

func statelessPlan(trgt proxy.Target) proxy.TargetPlan {
	return proxy.TargetPlan{Groups: [][]proxy.Target{{trgt}}}
}

func statelessPlanRoute(plan proxy.TargetPlan) *proxy.Route {
	return &proxy.Route{
		Name:        "stateless",
		ForwardMode: proxy.ForwardModeStateless,
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
			return plan, nil
		},
	}
}

func statelessTopBranch(tb testing.TB, req *sip.RequestEnvelope) string {
	tb.Helper()

	var branch string
	req.WithMessage(func(r *sip.Request) {
		if via, ok := r.Headers.FirstVia(); ok {
			branch, _ = via.Branch()
		}
	})
	if branch == "" {
		tb.Fatal("sent request has no top Via branch")
	}
	return branch
}

func countAllTxs(elm *sip.Element) int {
	return len(slices.Collect(elm.AllServerTransactions())) +
		len(slices.Collect(elm.AllClientTransactions()))
}

// cardinalityCounts counts factory calls as well as retained transactions:
// a transaction that terminates immediately must not escape the assertions.
type cardinalityCounts struct {
	authorized, preprocessed, resolved, adjusted atomic.Int32
	clients, servers, lookups, bindings, stable  atomic.Int32
}

func (c *cardinalityCounts) element() *sip.Element {
	return &sip.Element{
		ServerLocator: &attemptLocator{
			log: &attemptActionLog{}, lookups: &c.lookups,
			candidates: map[string][]attemptCandidate{
				"198.51.100.10": {{addr: sip.ResolvedAddr{Transport: "UDP", Addr: netip.MustParseAddrPort("198.51.100.10:5060")}}},
				"198.51.100.11": {{addr: sip.ResolvedAddr{Transport: "UDP", Addr: netip.MustParseAddrPort("198.51.100.11:5060")}}},
			},
		},
		ClientTransactionFactory: sip.ClientTransactionFactoryFunc(
			func(req *sip.RequestEnvelope, tp sip.ClientTransport, opts ...sip.ClientTransactionOptions) (sip.ClientTransaction, error) {
				c.clients.Add(1)
				return errors.Wrap2(sip.NewClientTransaction(req, tp, opts...))
			},
		),
		ServerTransactionFactory: sip.ServerTransactionFactoryFunc(
			func(req *sip.RequestEnvelope, tp sip.ServerTransport, opts ...sip.ServerTransactionOptions) (sip.ServerTransaction, error) {
				c.servers.Add(1)
				return errors.Wrap2(sip.NewServerTransaction(req, tp, opts...))
			},
		),
	}
}

func cardinalityPlan(name string) proxy.TargetPlan {
	a := statelessLitTarget("A", "198.51.100.10")
	b := statelessLitTarget("B", "198.51.100.11")
	a.Attributes = map[string]string{"role": "primary"}
	b.Attributes = map[string]string{"role": "backup"}
	switch name {
	case "one target":
		return statelessPlan(a)
	case "empty surrounding groups":
		return proxy.TargetPlan{Groups: [][]proxy.Target{{}, {a}, {}}}
	case "same group":
		return proxy.TargetPlan{Groups: [][]proxy.Target{{a, b}}}
	case "sequential groups":
		return proxy.TargetPlan{Groups: [][]proxy.Target{{a}, {b}}}
	case "same uri distinct ids":
		b.URI = statelessLitTarget("B", "198.51.100.10").URI
		a.Delivery = proxy.DeliveryRef{Kind: "binding", Key: "A"}
		b.Delivery = proxy.DeliveryRef{Kind: "binding", Key: "B"}
		return proxy.TargetPlan{Groups: [][]proxy.Target{{a, b}}}
	default:
		return proxy.TargetPlan{Groups: [][]proxy.Target{{}, {}}}
	}
}

func TestProxy_stateless_cardinality(t *testing.T) {
	t.Parallel()

	for _, method := range []sip.RequestMethod{sip.RequestMethodInvite, sip.RequestMethodInfo} {
		t.Run(strings.ToLower(string(method)), func(t *testing.T) {
			t.Parallel()
			for _, name := range []string{
				"one target", "empty surrounding groups", "same group",
				"sequential groups", "same uri distinct ids", "empty plan",
			} {
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					synctest.Test(t, func(t *testing.T) {
						testCardinality(t, method, name)
					})
				})
			}
		})
	}
}

func testCardinality(t *testing.T, method sip.RequestMethod, name string) {
	t.Helper()
	c := &cardinalityCounts{}
	plan := cardinalityPlan(name)
	multi := name == "same group" || name == "sequential groups" || name == "same uri distinct ids"
	route := statelessPlanRoute(plan)
	route.AuthorizeRequest = func(context.Context, *sip.RequestEnvelope) error {
		c.authorized.Add(1)
		return nil
	}
	route.PreprocessRoute = func(_ context.Context, req *sip.RequestEnvelope) error {
		c.preprocessed.Add(1)
		req.WithMessage(func(r *sip.Request) {
			r.Headers.Append(&header.Any{Name: "X-Cardinality", Value: "accepted"})
		})
		return nil
	}
	route.ResolveRequestTargets = func(_ context.Context, req *sip.RequestEnvelope) (proxy.TargetPlan, error) {
		c.resolved.Add(1)
		if !strings.Contains(req.Message().Render(), "X-Cardinality: accepted") {
			t.Error("resolver input lost accepted preprocessing")
		}
		return plan, nil
	}
	route.AdjustOutboundRequest = func(_ context.Context, _, out *sip.RequestEnvelope, _ proxy.Target) error {
		c.adjusted.Add(1)
		if multi && c.servers.Load() != 1 {
			t.Error("target adjusted before stateful admission, want cardinality preflight first")
		}
		if !strings.Contains(out.Message().Render(), "X-Cardinality: accepted") {
			t.Error("outbound request lost accepted preprocessing")
		}
		return nil
	}
	var tp *stubTransp
	elm, prx, transport := newConfiguredTestProxy(t, route, proxy.Options{
		StatelessKey: []byte("stateless-cardinality-key"), ServedDomains: []string{"alice.voip.com"},
		ResolveDelivery: func(_ context.Context, ref proxy.DeliveryRef) (proxy.BoundDelivery, error) {
			c.bindings.Add(1)
			if ref.Kind != "binding" || (ref.Key != "A" && ref.Key != "B") {
				t.Errorf("delivery reference = %+v, want retained A or B binding", ref)
			}
			return proxy.BoundDelivery{Transport: tp, RemoteAddr: netip.MustParseAddrPort("198.51.100.10:5060")}, nil
		},
	}, c.element())
	tp = transport
	rec := &evtRecorder{}
	prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))
	req := newInInviteReq(t,
		"UDP", sip.MagicCookie+".cardinality",
		netip.MustParseAddrPort("192.168.1.100:5060"), netip.MustParseAddrPort("10.0.0.5:5060"),
	)
	if method == sip.RequestMethodInfo {
		req = newInNonInviteReq(t,
			"UDP", sip.MagicCookie+".cardinality",
			req.LocalAddr(), req.RemoteAddr(),
		)
	}
	original := req.Message().Render()
	key, err := sip.MakeServerTransactionKey(req)
	if err != nil {
		t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
	}
	next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
		t.Error("next receiver called, want claimed request")
		return nil
	})
	err = prx.InterceptInboundRequest(sip.ContextWithElement(t.Context(), elm), next, req)
	synctest.Wait()
	if name == "empty plan" {
		rej, ok := errors.AsType[*sip.RequestRejectedError](err)
		if !ok || rej.ResponseStatus() != sip.ResponseStatusTemporarilyUnavailable {
			t.Fatalf("empty plan error = %v, want 480 rejection", err)
		}
	} else if err != nil {
		t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
	}
	sends := drainReqs(tp)
	wantInitial := 1
	if multi && name != "sequential groups" {
		wantInitial = 2
	} else if name == "empty plan" {
		wantInitial = 0
	}
	if len(sends) != wantInitial {
		t.Fatalf("initial downstream sends = %d, want %d", len(sends), wantInitial)
	}
	if !multi {
		if got := countAllTxs(elm); got != 0 {
			t.Fatalf("stateless transactions = %d, want 0", got)
		}
		if _, ok := prx.LookupRequest(elm, key); ok {
			t.Fatal("stateless request retained a coordinator, want none")
		}
		if got := tp.countSendRes(); got != 0 {
			t.Fatalf("upstream responses = %d, want 0 (no stateless provisional)", got)
		}
		if c.clients.Load() != 0 || c.servers.Load() != 0 || c.adjusted.Load() != int32(wantInitial) {
			t.Fatalf(
				"stateless execution = (client %d, server %d, adjust %d), want (0, 0, %d)",
				c.clients.Load(),
				c.servers.Load(),
				c.adjusted.Load(),
				wantInitial,
			)
		}
		if c.lookups.Load() != int32(wantInitial) || c.bindings.Load() != 0 {
			t.Errorf(
				"stateless delivery calls = (locator %d, binding %d), want (%d, 0)",
				c.lookups.Load(),
				c.bindings.Load(),
				wantInitial,
			)
		}
		for _, out := range sends {
			if !strings.Contains(statelessTopBranch(t, out), ".proxy-stateless.") {
				t.Fatal("one-target send lost stateless ownership")
			}
		}
	} else {
		responses := testCardinalityPromoted(t, elm, prx, tp, req, sends, name, c)
		assertCardinalityEvents(t, rec, responses)
	}
	if got := []int32{c.authorized.Load(), c.preprocessed.Load(), c.resolved.Load()}; !slices.Equal(got, []int32{1, 1, 1}) {
		t.Errorf("routing hook calls = %v, want [1 1 1]", got)
	}
	if got := req.Message().Render(); got != original {
		t.Error("original request mutated during forwarding")
	}
	if diff := cmp.Diff(cardinalityPlan(name), plan); diff != "" {
		t.Errorf("application plan (-want +got): %s", diff)
	}
}

func testCardinalityPromoted(
	t *testing.T, elm *sip.Element, prx *proxy.Proxy, tp *stubTransp,
	req *sip.RequestEnvelope, sends []*sip.RequestEnvelope, name string, c *cardinalityCounts,
) int {
	t.Helper()
	plan := cardinalityPlan(name)
	key, err := sip.MakeServerTransactionKey(req)
	if err != nil {
		t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
	}
	rq, ok := prx.LookupRequest(elm, key)
	if !ok {
		t.Fatal("multi-target request has no coordinator, want intact stateful promotion")
	}
	srv, ok := elm.LoadServerTransaction(key)
	if !ok || srv.Request().Message().Render() != req.Message().Render() {
		t.Fatal("promoted server transaction lost original identity")
	}
	snap, err := rq.Snapshot()
	if err != nil {
		t.Fatalf("rq.Snapshot() error = %v, want nil", err)
	}
	if diff := cmp.Diff(plan, snap.TargetPlan); diff != "" {
		t.Fatalf("admitted complete plan (-want +got): %s", diff)
	}
	if got := countAllTxs(elm); got != 1+len(sends) {
		t.Fatalf("initial transactions = %d, want %d", got, 1+len(sends))
	}
	if c.clients.Load() != int32(len(sends)) || c.servers.Load() != 1 || c.adjusted.Load() != int32(len(sends)) {
		t.Fatalf(
			"initial execution = (client %d, server %d, adjust %d), want (%d, 1, %d)",
			c.clients.Load(),
			c.servers.Load(),
			c.adjusted.Load(),
			len(sends),
			len(sends),
		)
	}
	var settled, acks int
	for len(sends) > 0 {
		out := sends[0]
		sends = sends[1:]
		if !strings.Contains(statelessTopBranch(t, out), ".proxy-stateful.") {
			t.Fatal("promoted send lost stateful ownership")
		}
		if name == "sequential groups" && !out.URI().Equal(plan.Groups[settled][0].URI) {
			t.Fatal("sequential send changed accepted group order")
		}
		clientKey, err := sip.MakeClientTransactionKey(out)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		tx, ok := elm.LoadClientTransaction(clientKey)
		if !ok {
			t.Fatal("downstream send has no client transaction")
		}
		if err := tx.RecvResponse(t.Context(), newInRes(t, out, sip.ResponseStatusBusyHere)); err != nil {
			t.Fatalf("tx.RecvResponse() error = %v, want nil", err)
		}
		settled++
		synctest.Wait()
		for _, extra := range drainReqs(tp) {
			if extra.Method() == sip.RequestMethodAck {
				acks++
			} else {
				sends = append(sends, extra)
			}
		}
	}
	wantAcks := 0
	if req.Method() == sip.RequestMethodInvite {
		wantAcks = 2
	}
	if settled != 2 || acks != wantAcks || c.clients.Load() != 2 || c.servers.Load() != 1 || c.adjusted.Load() != 2 {
		t.Fatalf(
			"execution = (settled %d, ACK %d, client %d, server %d, adjust %d), want (2, %d, 2, 1, 2)",
			settled,
			acks,
			c.clients.Load(),
			c.servers.Load(),
			c.adjusted.Load(),
			wantAcks,
		)
	}
	wantBindings, wantLookups := int32(0), int32(2)
	if name == "same uri distinct ids" {
		wantBindings, wantLookups = 2, 0
	}
	if c.bindings.Load() != wantBindings || c.lookups.Load() != wantLookups {
		t.Errorf(
			"delivery calls = (binding %d, locator %d), want (%d, %d)",
			c.bindings.Load(),
			c.lookups.Load(),
			wantBindings,
			wantLookups,
		)
	}
	upstream := drainRess(tp)
	if len(upstream) == 0 || upstream[len(upstream)-1].Status() != sip.ResponseStatusBusyHere {
		t.Fatal("promoted operation did not forward its final upstream response")
	}
	return len(upstream)
}

func assertCardinalityEvents(t *testing.T, rec *evtRecorder, responses int) {
	t.Helper()
	wantCounts := map[proxy.EventKind]int{
		proxy.EventKindRequestHandled: 1, proxy.EventKindRequestForwarded: 1,
		proxy.EventKindRequestCompleted: 1, proxy.EventKindForwardAttemptFinished: 2,
		proxy.EventKindResponseForwarded: responses,
	}
	if diff := cmp.Diff(wantCounts, rec.counts()); diff != "" {
		t.Fatalf("promoted observation counts (-want +got): %s", diff)
	}
	events := rec.get()
	for i, ev := range events {
		if ev.Mode != proxy.ForwardModeStateful || ev.RequestID == "" ||
			ev.RequestID != events[0].RequestID || ev.Sequence != uint64(i+1) ||
			ev.Error != nil {
			t.Errorf("event %d = %+v, want one ordered stateful stream", i, ev)
		}
	}
	ids := make(map[string]bool)
	for _, ev := range rec.kindSeqs(proxy.EventKindForwardAttemptFinished) {
		ids[ev.TargetID] = true
	}
	if !ids["A"] || !ids["B"] {
		t.Errorf("attempt target IDs = %v, want both A and B", ids)
	}
}

func TestProxy_stateless_cardinality_related_methods(t *testing.T) {
	t.Parallel()

	for _, mode := range []proxy.ForwardMode{proxy.ForwardModeStateless, proxy.ForwardModeStateful} {
		t.Run(mode.String(), func(t *testing.T) {
			t.Parallel()
			for _, method := range []sip.RequestMethod{sip.RequestMethodAck, sip.RequestMethodCancel} {
				t.Run(strings.ToLower(string(method)), func(t *testing.T) {
					t.Parallel()
					for _, name := range []string{"one target", "same group", "sequential groups"} {
						t.Run(name, func(t *testing.T) {
							t.Parallel()
							synctest.Test(t, func(t *testing.T) {
								testCardinalityRelated(t, mode, method, name)
							})
						})
					}
				})
			}
		})
	}
}

func testCardinalityRelated(t *testing.T, mode proxy.ForwardMode, method sip.RequestMethod, name string) {
	t.Helper()
	c := &cardinalityCounts{}
	route := statelessPlanRoute(cardinalityPlan(name))
	route.ForwardMode = mode
	route.AuthorizeRequest = func(context.Context, *sip.RequestEnvelope) error {
		c.authorized.Add(1)
		return nil
	}
	route.AdjustOutboundRequest = func(context.Context, *sip.RequestEnvelope, *sip.RequestEnvelope, proxy.Target) error {
		c.adjusted.Add(1)
		return nil
	}
	route.ResolveRequestTargets = func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
		c.resolved.Add(1)
		return cardinalityPlan(name), nil
	}
	var tp *stubTransp
	elm, prx, transport := newConfiguredTestProxy(t, route, proxy.Options{
		StatelessKey: []byte("related-cardinality-key"), ServedDomains: []string{"alice.voip.com"},
		ResolveDelivery: func(context.Context, proxy.DeliveryRef) (proxy.BoundDelivery, error) {
			c.bindings.Add(1)
			return proxy.BoundDelivery{Transport: tp, RemoteAddr: netip.MustParseAddrPort("198.51.100.10:5060")}, nil
		},
		ResolveStatelessDestination: func(context.Context, *sip.RequestEnvelope, proxy.Target) (proxy.BoundDelivery, error) {
			c.stable.Add(1)
			return proxy.BoundDelivery{Transport: tp, RemoteAddr: netip.MustParseAddrPort("198.51.100.10:5060")}, nil
		},
	}, c.element())
	tp = transport
	rec := &evtRecorder{}
	prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))
	invite := newInInviteReq(t, "UDP", sip.MagicCookie+".related-cardinality",
		netip.MustParseAddrPort("192.168.1.100:5060"), netip.MustParseAddrPort("10.0.0.5:5060"))
	req, err := sip.NewCancelRequestEnvelope(invite)
	if err != nil {
		t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
	}
	if method == sip.RequestMethodAck {
		req = newInAckReq(t, invite, newInRes(t, invite, sip.ResponseStatusOK))
	}
	key, err := sip.MakeServerTransactionKey(req)
	if err != nil {
		t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
	}
	next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
		t.Error("next receiver called, want claimed related method")
		return nil
	})
	err = prx.InterceptInboundRequest(sip.ContextWithElement(t.Context(), elm), next, req)
	synctest.Wait()
	wantSends := 0
	if name == "one target" {
		wantSends = 1
	}
	if got := len(drainReqs(tp)); got != wantSends {
		t.Errorf("all downstream sends = %d, want %d", got, wantSends)
	}
	if got := countAllTxs(elm); got != 0 || c.clients.Load() != 0 || c.servers.Load() != 0 {
		t.Errorf("transactions = (retained %d, client %d, server %d), want all zero",
			got,
			c.clients.Load(),
			c.servers.Load(),
		)
	}
	inviteKey, keyErr := sip.MakeServerTransactionKey(invite)
	if keyErr != nil {
		t.Fatalf("sip.MakeServerTransactionKey(INVITE) error = %v, want nil", keyErr)
	}
	for _, lookup := range []sip.ServerTransactionKey{key, inviteKey} {
		if _, ok := prx.LookupRequest(elm, lookup); ok {
			t.Error("related method retained a coordinator, want none")
		}
	}
	if got := tp.countSendRes(); got != 0 {
		t.Errorf("upstream responses/challenges = %d, want 0", got)
	}
	if c.adjusted.Load() != int32(wantSends) || c.stable.Load() != 0 ||
		c.bindings.Load() != 0 || c.lookups.Load() != int32(wantSends) {
		t.Errorf("delivery callbacks = (adjust %d, stable %d, binding %d, locator %d), want (%d, 0, 0, %d)",
			c.adjusted.Load(),
			c.stable.Load(),
			c.bindings.Load(),
			c.lookups.Load(),
			wantSends,
			wantSends,
		)
	}
	if name == "one target" {
		if err != nil {
			t.Errorf("one-target error = %v, want nil", err)
		}
	} else {
		complete := rec.kindSeqs(proxy.EventKindRequestCompleted)
		if len(complete) != 1 || complete[0].Outcome != proxy.OutcomeRejected || complete[0].Error == nil {
			t.Errorf("routing result = %v, completions = %+v, want inspectable rejection", err, complete)
		}
		if got := rec.counts(); got[proxy.EventKindRequestHandled] != 1 ||
			got[proxy.EventKindForwardAttemptFinished] != 0 ||
			got[proxy.EventKindRequestForwarded] != 0 {
			t.Errorf("related-method observations = %v, want one rejected operation without an attempt or forward", got)
		}
	}
	if c.authorized.Load() != 1 || c.resolved.Load() != 1 {
		t.Errorf("route hook calls = (auth %d, resolve %d), want (1, 1)", c.authorized.Load(), c.resolved.Load())
	}
}

// promotionFixture keeps route-level and execution-level hook counts
// separate. Its two-group plan promotes before target preparation; the
// accepted working request also retains a hostname next hop.
type promotionFixture struct {
	authorized, preprocessed, resolved atomic.Int32
	adjusted, selected                 atomic.Int32
	plan                               proxy.TargetPlan
	route                              *proxy.Route
	opts                               proxy.Options
}

func newPromotionFixture() *promotionFixture {
	f := &promotionFixture{
		plan: proxy.TargetPlan{Groups: [][]proxy.Target{
			{{ID: "A", URI: statelessHostTarget().URI, Attributes: map[string]string{"role": "primary"}}},
			{{ID: "B", URI: snapTargetURI("10.0.0.10"), Attributes: map[string]string{"role": "backup"}}},
		}},
		opts: proxy.Options{
			StatelessKey:    []byte("promotion-preserves-routing-key"),
			ServedDomains:   []string{"alice.voip.com"},
			LocalIdentities: []*sip.URI{{Addr: sip.MakeHostPortAddr("192.168.1.100", 5060)}},
		},
	}
	f.route = &proxy.Route{
		Name:        "promotion",
		ForwardMode: proxy.ForwardModeStateless,
		AuthorizeRequest: func(context.Context, *sip.RequestEnvelope) error {
			f.authorized.Add(1)
			return nil
		},
		PreprocessRoute: func(_ context.Context, work *sip.RequestEnvelope) error {
			f.preprocessed.Add(1)
			work.WithMessage(func(r *sip.Request) {
				r.Headers.Append(&header.Any{Name: "X-Prepared", Value: "accepted"})
				for _, host := range []string{"192.168.1.100", "next.example.org"} {
					r.Headers.AppendRoute(header.RouteHop{URI: &sip.URI{
						Addr:   sip.MakeHostPortAddr(host, 5060),
						Params: make(sip.Values).Set("lr", ""),
					}})
				}
			})
			return nil
		},
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
			if f.resolved.Add(1) > 1 {
				// A replay must not silently choose a new logical destination.
				return statelessPlan(statelessLitTarget("replayed", "10.0.0.99")), nil
			}
			return f.plan, nil
		},
		AdjustOutboundRequest: func(context.Context, *sip.RequestEnvelope, *sip.RequestEnvelope, proxy.Target) error {
			f.adjusted.Add(1)
			return nil
		},
		SelectTargets: func(_ context.Context, sel proxy.Selection) ([]string, error) {
			f.selected.Add(1)
			var ids []string
			for _, group := range sel.RemainingGroups {
				for _, target := range group {
					ids = append(ids, target.ID)
				}
			}
			return ids, nil
		},
	}
	return f
}

func assertPromotionWorking(tb testing.TB, req *sip.RequestEnvelope) {
	tb.Helper()
	if got := strings.Count(strings.ToLower(req.Message().Render()), "x-prepared: accepted"); got != 1 {
		tb.Fatalf("preprocessing header count = %d, want 1", got)
	}
	var hosts []string
	req.WithMessage(func(r *sip.Request) {
		for hop := range r.Headers.Routes() {
			if u, ok := hop.URI.(*sip.URI); ok {
				hosts = append(hosts, u.Addr.Host())
			}
		}
	})
	if !slices.Equal(hosts, []string{"next.example.org"}) {
		tb.Fatalf("remaining Route hosts = %v, want [next.example.org]", hosts)
	}
}

func TestProxy_promotion_preserves_routing(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		f := newPromotionFixture()
		planBefore := newPromotionFixture().plan
		elm, prx, tp := newConfiguredTestProxy(t, f.route, f.opts, &sip.Element{
			ServerLocator: statelessFixedLocator{addr: netip.MustParseAddrPort("198.51.100.77:5060")},
		})
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".promotion-routing",
			netip.MustParseAddrPort("192.168.1.100:5060"), netip.MustParseAddrPort("10.0.0.5:5060"))
		before := req.Message().Render()
		fwdIntercept(t, elm, prx, req)
		synctest.Wait()
		if got := []int32{f.authorized.Load(), f.preprocessed.Load(), f.resolved.Load()}; !slices.Equal(got, []int32{1, 1, 1}) {
			t.Fatalf("route hook calls = %v, want [1 1 1]", got)
		}
		sends := drainReqs(tp)
		if len(sends) != 1 {
			t.Fatalf("initial sends = %d, want 1", len(sends))
		}
		assertPromotionWorking(t, sends[0])
		if branch := statelessTopBranch(t, sends[0]); !strings.Contains(branch, ".proxy-stateful.") {
			t.Fatalf("promoted branch = %q, want stateful ownership", branch)
		}
		srvKey, err := sip.MakeServerTransactionKey(req)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		rq, ok := prx.LookupRequest(elm, srvKey)
		if !ok {
			t.Fatal("promoted request not indexed by original upstream key")
		}
		srvTx, ok := elm.LoadServerTransaction(srvKey)
		if !ok || srvTx.Key() != srvKey || srvTx.Request().Message().Render() != before {
			t.Fatal("server transaction did not retain original request identity")
		}
		snap, err := rq.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		if diff := cmp.Diff(planBefore, snap.TargetPlan); diff != "" {
			t.Fatalf("accepted plan (-want +got): %s", diff)
		}
		clnKey, err := sip.MakeClientTransactionKey(sends[0])
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx, ok := elm.LoadClientTransaction(clnKey)
		if !ok {
			t.Fatal("promoted client transaction not registered")
		}
		if err := clnTx.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusBusyHere)); err != nil {
			t.Fatalf("clnTx.RecvResponse() error = %v, want nil", err)
		}
		synctest.Wait()
		var invites []*sip.RequestEnvelope
		for _, out := range drainReqs(tp) {
			if out.Method().Equal(sip.RequestMethodInvite) {
				invites = append(invites, out)
			}
		}
		if len(invites) != 1 || !invites[0].URI().Equal(planBefore.Groups[1][0].URI) {
			t.Fatalf("group B sends = %v, want one saved backup target", invites)
		}
		assertPromotionWorking(t, invites[0])
		if got := []int32{f.authorized.Load(), f.preprocessed.Load(), f.resolved.Load()}; !slices.Equal(got, []int32{1, 1, 1}) {
			t.Fatalf("route hook calls = %v, want [1 1 1]", got)
		}
		// Cardinality promotes before preparation, so only two stateful targets;
		// selection runs per scheduling cycle, not once per inbound request.
		if f.adjusted.Load() != 2 || f.selected.Load() < 2 {
			t.Fatalf("execution hooks = (adjust %d, select %d), want (2, >=2)", f.adjusted.Load(), f.selected.Load())
		}
		if req.Message().Render() != before {
			t.Fatal("promotion mutated the application's original request")
		}
		if diff := cmp.Diff(planBefore, f.plan); diff != "" {
			t.Fatalf("application plan after handoff (-want +got): %s", diff)
		}
	})
}

// TestProxy_sips_forwarding catches security lost during contact resolution,
// application rewrites, or delivery binding through the public send paths.
func TestProxy_sips_forwarding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sipInput   bool
		sipTarget  bool
		adjust     bool
		strict     bool
		preprocess bool
		custom     bool
		stable     bool
		secureTp   bool
		inboundTLS bool
		wantSend   bool
	}{
		{name: "sip contact", sipTarget: true},
		{name: "adjusted uri", adjust: true},
		{name: "strict route", strict: true, secureTp: true},
		{name: "preprocessed uri", preprocess: true, sipTarget: true},
		{name: "custom udp", custom: true},
		{name: "stable resolver udp", stable: true},
		{name: "secure control", secureTp: true, wantSend: true},
		{name: "sip control", sipInput: true, sipTarget: true, wantSend: true},
		{
			name: "sip over tls control", sipInput: true, sipTarget: true,
			secureTp: true, inboundTLS: true, wantSend: true,
		},
	}
	modes := []struct {
		name string
		mode proxy.ForwardMode
	}{
		{name: "stateful", mode: proxy.ForwardModeStateful},
		{name: "stateless", mode: proxy.ForwardModeStateless},
	}
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()

			for _, tt := range tests {
				if tt.stable && mode.mode != proxy.ForwardModeStateless {
					continue // The stable destination resolver is stateless-only.
				}
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()

					synctest.Test(t, func(t *testing.T) {
						ctx := t.Context()
						laddr := netip.MustParseAddrPort("192.0.2.1:5060")
						upstream := netip.MustParseAddrPort("192.0.2.5:5060")
						downstream := netip.MustParseAddrPort("198.51.100.10:5060")
						trgt := statelessLitTarget("t1", "198.51.100.10")
						trgt.URI.Secured = !tt.sipTarget
						if tt.stable {
							trgt.URI.Addr = sip.MakeHostAddr("contact.example.org")
						}
						if tt.strict {
							trgt.Routes = []header.RouteHop{{
								URI: &sip.URI{Addr: sip.MakeHostPortAddr("198.51.100.10", 5060)},
							}}
						}
						if tt.custom {
							trgt.Delivery = proxy.DeliveryRef{Kind: "flow", Key: "secure-input"}
						}
						targetBefore := trgt.URI.String()
						routesBefore := make([]string, 0, len(trgt.Routes))
						for _, hop := range trgt.Routes {
							routesBefore = append(routesBefore, hop.String())
						}

						var resolved, boundCalls, stableCalls, lookups atomic.Int32
						route := &proxy.Route{
							Name: "sips", ForwardMode: mode.mode,
							ResolveRequestTargets: func(_ context.Context, req *sip.RequestEnvelope) (proxy.TargetPlan, error) {
								resolved.Add(1)
								if tt.preprocess && req.URI().Scheme() != "sip" {
									t.Errorf("resolver URI scheme = %q, want sip", req.URI().Scheme())
								}
								return statelessPlan(trgt), nil
							},
						}
						if tt.preprocess {
							route.PreprocessRoute = func(_ context.Context, req *sip.RequestEnvelope) error {
								req.WithMessage(func(r *sip.Request) {
									r.URI = &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.voip.com")}
								})
								return nil
							}
						}
						if tt.adjust {
							route.AdjustOutboundRequest = func(_ context.Context, _, out *sip.RequestEnvelope, _ proxy.Target) error {
								out.WithMessage(func(r *sip.Request) {
									r.URI = &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr("198.51.100.10", 5060)}
								})
								return nil
							}
						}

						proto := sip.TransportProto("UDP")
						if tt.secureTp {
							proto = "TLS"
						}
						loc := &attemptLocator{
							log: &attemptActionLog{}, lookups: &lookups,
							candidates: map[string][]attemptCandidate{
								"198.51.100.10":       {{name: "only", addr: sip.ResolvedAddr{Transport: proto, Addr: downstream}}},
								"contact.example.org": {{name: "fallback", addr: sip.ResolvedAddr{Transport: "TLS", Addr: downstream}}},
							},
						}
						bound := newStatelessBoundClnTransp()
						elm, prx, udp := newConfiguredTestProxy(t, route, proxy.Options{
							StatelessKey: []byte("sips-forwarding-key"), ServedDomains: []string{"alice.voip.com"},
							LocalIdentities: []*sip.URI{
								{Addr: sip.MakeHostPortAddr("proxy.example.org", 5061), Secured: true},
							},
							ResolveDelivery: func(_ context.Context, ref proxy.DeliveryRef) (proxy.BoundDelivery, error) {
								boundCalls.Add(1)
								if ref != trgt.Delivery || !tt.custom {
									t.Errorf("delivery reference = %+v, want %+v for custom case", ref, trgt.Delivery)
								}
								return proxy.BoundDelivery{Transport: bound, RemoteAddr: downstream}, nil
							},
							ResolveStatelessDestination: func(_ context.Context, req *sip.RequestEnvelope, target proxy.Target) (proxy.BoundDelivery, error) {
								stableCalls.Add(1)
								if req.URI().Scheme() != "sips" || target.ID != "t1" || !tt.stable {
									t.Errorf("stable resolver input = %s, target = %+v, want SIPS t1", req.URI(), target)
								}
								return proxy.BoundDelivery{Transport: bound, RemoteAddr: downstream}, nil
							},
						}, &sip.Element{ServerLocator: loc})
						tls := newStubTransp()
						tls.md = sip.TLSMetadata()
						tls.reliable = true
						if err := elm.TrackTransport(tls); err != nil {
							t.Fatalf("elm.TrackTransport(TLS) error = %v, want nil", err)
						}
						rec := &evtRecorder{}
						prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))
						inProto := sip.TransportProto("UDP")
						if tt.inboundTLS {
							inProto = "TLS"
						}
						req := newInInviteReq(t, inProto, sip.MagicCookie+".sips", laddr, upstream)
						req.WithMessage(func(r *sip.Request) {
							r.URI = &sip.URI{
								User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.voip.com"), Secured: !tt.sipInput,
							}
						})
						originalBefore := req.Message().Render()
						next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
							t.Error("next receiver called, want claimed request")
							return nil
						})
						err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), next, req)
						synctest.Wait()

						sent := append(drainReqs(udp), drainReqs(tls)...)
						for len(bound.sendReqCalls) > 0 {
							sent = append(sent, (<-bound.sendReqCalls).req)
						}
						wantSends := 0
						if tt.wantSend {
							wantSends = 1
						}
						if len(sent) != wantSends {
							t.Errorf("all downstream sends = %d, want %d", len(sent), wantSends)
						}
						counts := rec.counts()
						if tt.wantSend {
							if err != nil {
								t.Errorf("prx.InterceptInboundRequest() error = %v, want nil", err)
							}
							if counts[proxy.EventKindRequestForwarded] != 1 {
								t.Errorf("forwarded events = %d, want 1", counts[proxy.EventKindRequestForwarded])
							}
							for _, sent := range sent {
								if sent.RemoteAddr() != downstream {
									t.Errorf("forwarded destination = %v, want %v", sent.RemoteAddr(), downstream)
								}
								if !tt.sipInput {
									if got := sent.URI().Scheme(); got != "sips" {
										t.Errorf("forwarded URI scheme = %q, want sips", got)
									}
									if !sent.Transport().Secured() {
										t.Errorf("forwarded transport = %+v, want secured", sent.Transport())
									}
								} else if got := sent.URI().Scheme(); got != "sip" {
									t.Errorf("forwarded URI scheme = %q, want sip", got)
								}
								if got := sent.Transport().Proto; got != proto {
									t.Errorf("forwarded transport protocol = %q, want %q", got, proto)
								}
							}
						} else {
							attempts := rec.kindSeqs(proxy.EventKindForwardAttemptFinished)
							if len(attempts) != 1 || attempts[0].Error == nil {
								t.Errorf("attempt events = %+v, want one inspectable pre-send failure (returned %v)", attempts, err)
							}
							if counts[proxy.EventKindRequestForwarded] != 0 {
								t.Errorf("forwarded events = %d, want 0", counts[proxy.EventKindRequestForwarded])
							}
							if got := bound.sends.Load(); got != 0 {
								t.Errorf("bound send attempts = %d, want 0", got)
							}
						}
						if got := resolved.Load(); got != 1 {
							t.Errorf("target resolver calls = %d, want 1", got)
						}
						if tt.custom && boundCalls.Load() != 1 {
							t.Errorf("delivery resolver calls = %d, want 1", boundCalls.Load())
						}
						if tt.stable {
							if got := stableCalls.Load(); got != 1 {
								t.Errorf("stable resolver calls = %d, want 1", got)
							}
							if got := countAllTxs(elm); got != 0 {
								t.Errorf("transactions = %d, want 0 (no security-repair promotion)", got)
							}
						}
						if (tt.custom || tt.stable) && lookups.Load() != 0 {
							t.Errorf("ordinary candidate lookups = %d, want 0 (no fallback)", lookups.Load())
						}
						if got := req.Message().Render(); got != originalBefore {
							t.Errorf("original request changed:\n%s", got)
						}
						if got := trgt.URI.String(); got != targetBefore {
							t.Errorf("target URI = %s, want unchanged %s", got, targetBefore)
						}
						for i, hop := range trgt.Routes {
							if got := hop.String(); got != routesBefore[i] {
								t.Errorf("target route %d = %s, want unchanged %s", i, got, routesBefore[i])
							}
						}
					})
				})
			}
		})
	}
}

// TestProxy_StatelessDestination pins the one-destination determinism of the
// stateless send path through the request interceptor: a claimed request is
// sent exactly once through the resolved bound transport without creating a
// transaction, undecidable invariants promote response-eligible requests to
// stateful before the first send, and non-promotable methods stay stateless
// or fail routing.
func TestProxy_StatelessDestination(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	key := []byte("stateless-destination-key")
	served := []string{"alice.voip.com"}
	t.Run("literal destination single send", func(t *testing.T) {
		t.Parallel()

		trgt := statelessLitTarget("t1", "198.51.100.10")
		route := statelessPlanRoute(statelessPlan(trgt))
		elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
			StatelessKey:    key,
			ServedDomains:   served,
			LocalIdentities: []*sip.URI{{Addr: sip.MakeHostAddr("pxy.example.net")}},
		})

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".sls-dest-literal", laddr, raddr)
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, req); err != nil {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}

		sent := testutil.WaitResult(t, tp.sendReqCalls, "stateless outbound request send")
		if destinations := len(drainReqs(tp)); destinations != 0 {
			t.Fatalf("extra destinations=%d, want 0 more", destinations)
		}
		if got, want := sent.req.RemoteAddr(), netip.MustParseAddrPort("198.51.100.10:5060"); got != want {
			t.Fatalf("sent destination = %v, want %v", got, want)
		}
		branch := statelessTopBranch(t, sent.req)
		if !strings.HasPrefix(branch, sip.MagicCookie+".proxy-stateless.") {
			t.Fatalf("top via branch = %q, want proxy-stateless mark", branch)
		}
		if got := countAllTxs(elm); got != 0 {
			t.Fatalf("transactions=%d, want 0", got)
		}

		// A retransmission is another deterministic send with the same
		// branch and destination.
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, req); err != nil {
			t.Fatalf("retransmission prx.InterceptInboundRequest() error = %v, want nil", err)
		}
		retrans := testutil.WaitResult(t, tp.sendReqCalls, "stateless retransmission send")
		if got := statelessTopBranch(t, retrans.req); got != branch {
			t.Fatalf("retransmission branch = %q, want identical %q", got, branch)
		}
		if got := countAllTxs(elm); got != 0 {
			t.Fatalf("transactions=%d, want 0", got)
		}
	})

	t.Run("hostname without resolver promotes", func(t *testing.T) {
		t.Parallel()

		route := statelessPlanRoute(statelessPlan(statelessHostTarget()))
		elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
			StatelessKey:  key,
			ServedDomains: served,
		}, &sip.Element{ServerLocator: statelessEmptyLocator{}})

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".sls-dest-host", laddr, raddr)
		txKey, err := sip.MakeServerTransactionKey(req)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}

		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, req); err != nil {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}

		if _, ok := elm.LoadServerTransaction(txKey); !ok {
			t.Fatal("request was not promoted to a stateful admission")
		}
		if got := len(drainReqs(tp)); got != 0 {
			t.Fatalf("stateless sends=%d, want 0 (promotion happens before the first send)", got)
		}
	})

	t.Run("promoted via marks stateful", func(t *testing.T) {
		t.Parallel()

		route := statelessPlanRoute(statelessPlan(statelessHostTarget()))
		elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
			StatelessKey:  key,
			ServedDomains: served,
		}, &sip.Element{ServerLocator: statelessFixedLocator{
			addr: netip.MustParseAddrPort("198.51.100.33:5060"),
		}})

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".sls-promote", laddr, raddr)
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, req); err != nil {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}

		// The promoted send is owned by the stateful client transaction:
		// the proxy-stateless mark is reserved for genuine stateless sends
		// because the inbound response path attributes ownership by it.
		sent := testutil.WaitResult(t, tp.sendReqCalls, "promoted stateful send")
		if branch := statelessTopBranch(t, sent.req); !strings.HasPrefix(branch, sip.MagicCookie+".proxy-stateful.") {
			t.Fatalf("promoted top via branch = %q, want proxy-stateful mark", branch)
		}
	})

	t.Run("stable resolver honored", func(t *testing.T) {
		t.Parallel()

		bound := newStatelessBoundClnTransp()
		var resolved atomic.Int32
		route := statelessPlanRoute(statelessPlan(statelessHostTarget()))
		elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
			StatelessKey:  key,
			ServedDomains: served,
			ResolveStatelessDestination: func(
				_ context.Context,
				req *sip.RequestEnvelope,
				trgt proxy.Target,
			) (proxy.BoundDelivery, error) {
				resolved.Add(1)
				if req == nil || trgt.ID != "h1" {
					t.Errorf("opts.ResolveStatelessDestination() got req=%v target=%v, want request + h1", req, trgt)
				}
				return proxy.BoundDelivery{
					Transport:  bound,
					RemoteAddr: netip.MustParseAddrPort("198.51.100.77:5060"),
				}, nil
			},
		})

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".sls-dest-rslv", laddr, raddr)
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, req); err != nil {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}

		sent := testutil.WaitResult(t, bound.sendReqCalls, "bound stateless send")
		if got, want := sent.req.RemoteAddr(), netip.MustParseAddrPort("198.51.100.77:5060"); got != want {
			t.Fatalf("bound destination = %v, want %v", got, want)
		}
		if got := resolved.Load(); got != 1 {
			t.Fatalf("opts.ResolveStatelessDestination() calls = %d, want 1", got)
		}
		if got := len(drainReqs(tp)); got != 0 {
			t.Fatalf("element transport sends=%d, want 0 (bound transport owns the send)", got)
		}
		if got := countAllTxs(elm); got != 0 {
			t.Fatalf("transactions=%d, want 0", got)
		}
	})

	t.Run("send error never retries alternate", func(t *testing.T) {
		t.Parallel()

		bound := newStatelessBoundClnTransp()
		bound.sendErr = errors.ErrorWrap("send broken")
		var resolved atomic.Int32
		route := statelessPlanRoute(statelessPlan(statelessHostTarget()))
		elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
			StatelessKey:  key,
			ServedDomains: served,
			ResolveStatelessDestination: func(context.Context, *sip.RequestEnvelope, proxy.Target) (proxy.BoundDelivery, error) {
				resolved.Add(1)
				return proxy.BoundDelivery{
					Transport:  bound,
					RemoteAddr: netip.MustParseAddrPort("198.51.100.77:5060"),
				}, nil
			},
		})

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".sls-dest-fail", laddr, raddr)
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, req); err == nil {
			t.Fatal("prx.InterceptInboundRequest() error = nil, want send failure")
		}
		if got := bound.sends.Load(); got != 1 {
			t.Fatalf("send attempts=%d, want 1 (no alternate-destination retry)", got)
		}
		if got := resolved.Load(); got != 1 {
			t.Fatalf("opts.ResolveStatelessDestination() calls = %d, want 1", got)
		}
		if got := len(drainReqs(tp)); got != 0 {
			t.Fatalf("element transport sends=%d, want 0", got)
		}
	})

	t.Run("ack never promoted", func(t *testing.T) {
		t.Parallel()

		route := statelessPlanRoute(statelessPlan(statelessHostTarget()))
		elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
			StatelessKey:  key,
			ServedDomains: served,
		})

		invite := newInInviteReq(t, "UDP", sip.MagicCookie+".sls-dest-ack", laddr, raddr)
		ack := newInAckReq(t, invite, newInRes(t, invite, sip.ResponseStatusRequestTerminated))

		// An unroutable ACK is consumed silently: it never gains a
		// transaction and never reaches a destination.
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, ack); err != nil {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil (silent consumption)", err)
		}
		if got := countAllTxs(elm); got != 0 {
			t.Fatalf("transactions=%d, want 0 (ACK is never promoted)", got)
		}
		if got := len(drainReqs(tp)); got != 0 {
			t.Fatalf("sends=%d, want 0", got)
		}
	})

	t.Run("unmatched cancel stays stateless", func(t *testing.T) {
		t.Parallel()

		route := statelessPlanRoute(statelessPlan(statelessLitTarget("t1", "198.51.100.10")))
		elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
			StatelessKey:  key,
			ServedDomains: served,
		})

		invite := newInInviteReq(t, "UDP", sip.MagicCookie+".sls-dest-cnc", laddr, raddr)
		cancel, err := sip.NewCancelRequestEnvelope(invite)
		if err != nil {
			t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
		}

		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, cancel); err != nil {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}

		sent := testutil.WaitResult(t, tp.sendReqCalls, "stateless CANCEL send")
		if !sent.req.Method().Equal(sip.RequestMethodCancel) {
			t.Fatalf("sent method = %v, want CANCEL", sent.req.Method())
		}
		if branch := statelessTopBranch(t, sent.req); !strings.HasPrefix(branch, sip.MagicCookie+".proxy-stateless.") {
			t.Fatalf("top via branch = %q, want proxy-stateless mark", branch)
		}
		if got := countAllTxs(elm); got != 0 {
			t.Fatalf("transactions=%d, want 0", got)
		}
	})

	t.Run("unmatched cancel fails routing", func(t *testing.T) {
		t.Parallel()

		route := statelessPlanRoute(statelessPlan(statelessHostTarget()))
		elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
			StatelessKey:  key,
			ServedDomains: served,
		})

		invite := newInInviteReq(t, "UDP", sip.MagicCookie+".sls-dest-cncf", laddr, raddr)
		cancel, err := sip.NewCancelRequestEnvelope(invite)
		if err != nil {
			t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
		}

		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, cancel); err == nil {
			t.Fatal("prx.InterceptInboundRequest() error = nil, want routing failure")
		}
		if got := len(drainReqs(tp)); got != 0 {
			t.Fatalf("sends=%d, want 0", got)
		}
		if got := countAllTxs(elm); got != 0 {
			t.Fatalf("transactions=%d, want 0 (unmatched CANCEL is never promoted)", got)
		}
	})

	t.Run("missing bound flow no dial", func(t *testing.T) {
		t.Parallel()

		bound := newStatelessBoundClnTransp()
		route := statelessPlanRoute(statelessPlan(statelessHostTarget()))
		elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
			StatelessKey:  key,
			ServedDomains: served,
			ResolveStatelessDestination: func(context.Context, *sip.RequestEnvelope, proxy.Target) (proxy.BoundDelivery, error) {
				return proxy.BoundDelivery{Transport: bound}, nil
			},
		})

		invite := newInInviteReq(t, "UDP", sip.MagicCookie+".sls-dest-noflow", laddr, raddr)
		cancel, err := sip.NewCancelRequestEnvelope(invite)
		if err != nil {
			t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
		}

		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, cancel); err == nil {
			t.Fatal("prx.InterceptInboundRequest() error = nil, want bound-flow failure")
		}
		if got := bound.sends.Load(); got != 0 {
			t.Fatalf("bound transport sends=%d, want 0 (missing flow fails without dial)", got)
		}
		if got := len(drainReqs(tp)); got != 0 {
			t.Fatalf("element transport sends=%d, want 0", got)
		}
		if got := countAllTxs(elm); got != 0 {
			t.Fatalf("transactions=%d, want 0", got)
		}
	})
}

// statelessSentByRejecter wraps a stub transport and rejects every sent-by
// check, simulating a claimed-path response whose top Via does not name
// this element.
type statelessSentByRejecter struct{ *stubTransp }

func (statelessSentByRejecter) MatchSentBy(sip.Addr) bool { return false }

// resViaCount counts the Via hops of res.
func resViaCount(res *sip.ResponseEnvelope) (n int) {
	res.WithMessage(func(r *sip.Response) {
		for range r.Headers.Vias() {
			n++
		}
	})
	return n
}

// resTopVia returns a clone of the top Via hop of res.
func resTopVia(tb testing.TB, res *sip.ResponseEnvelope) header.ViaHop {
	tb.Helper()

	var via header.ViaHop
	res.WithMessage(func(r *sip.Response) {
		if v, ok := r.Headers.FirstVia(); ok && v != nil {
			via = v.Clone()
		}
	})
	if via.IsZero() {
		tb.Fatal("response has no top Via")
	}
	return via
}

// prependMarkedVia inserts a fabricated proxy-namespace Via hop carrying
// mark at the top of res, simulating a claimed-path inbound response.
func prependMarkedVia(res *sip.ResponseEnvelope, mark string) {
	res.WithMessage(func(r *sip.Response) {
		r.Headers.PrependVia(header.ViaHop{
			Proto:     sip.ProtoVer20(),
			Transport: "UDP",
			Addr:      sip.MakeHostPortAddr("192.168.1.100", 5060),
			Params: make(sip.Values).Set(
				"branch",
				sip.MagicCookie+"."+mark+"."+strings.Repeat("ab", 16)+".late",
			),
		})
	})
}

// TestProxy_ResponseOwnership pins the inbound-response ownership
// classification: only a response whose top Via is authentically this
// proxy instance's stateless Via is relayed — after removing exactly that
// Via toward the next recorded hop — while stateful-owned strays, invalid
// claims, and responses with nothing left to route are consumed silently
// and foreign traffic passes to the next receiver.
func TestProxy_ResponseOwnership(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	key := []byte("response-ownership-key")
	served := []string{"alice.voip.com"}
	newOwnershipProxy := func(tb testing.TB) (*sip.Element, *proxy.Proxy, *stubTransp) {
		tb.Helper()

		return newConfiguredTestProxy(tb,
			statelessPlanRoute(statelessPlan(statelessLitTarget("t1", "198.51.100.10"))),
			proxy.Options{StatelessKey: key, ServedDomains: served},
		)
	}
	// emitStateless sends one inbound INVITE through the stateless route
	// and returns the emitted outbound copy: its top Via is this proxy's
	// stateless-marked hop that an inbound response must carry to be owned.
	emitStateless := func(tb testing.TB, elm *sip.Element, prx *proxy.Proxy, tp *stubTransp, branch string) *sip.RequestEnvelope {
		tb.Helper()

		req := newInInviteReq(tb, "UDP", branch, laddr, raddr)
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, req); err != nil {
			tb.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}
		return testutil.WaitResult(tb, tp.sendReqCalls, "stateless outbound send").req
	}

	t.Run("stateless owned forwarded", func(t *testing.T) {
		t.Parallel()

		var nextCalls atomic.Int32
		next := sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error {
			nextCalls.Add(1)
			return nil
		})
		elm, prx, tp := newOwnershipProxy(t)

		out := emitStateless(t, elm, prx, tp, sip.MagicCookie+".own-fwd")
		res := newInRes(t, out, sip.ResponseStatusNotFound)

		if err := prx.InterceptInboundResponse(sip.ContextWithElement(ctx, elm), next, res); err != nil {
			t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
		}

		sent := testutil.WaitResult(t, tp.sendResCalls, "stateless response send").res
		if got := resViaCount(sent); got != 1 {
			t.Fatalf("forwarded vias=%d, want 1 (exactly the proxy hop removed)", got)
		}
		top := resTopVia(t, sent)
		if branch, _ := top.Branch(); branch != sip.MagicCookie+".own-fwd" {
			t.Fatalf("forwarded top via branch=%q, want client branch %q", branch, sip.MagicCookie+".own-fwd")
		}
		if got, want := top.Addr, sip.MakeHostPortAddr(raddr.Addr().String(), raddr.Port()); !got.Equal(want) {
			t.Fatalf("forwarded top via sent-by=%v, want %v", got, want)
		}
		if got := sent.Status(); !got.Equal(sip.ResponseStatusNotFound) {
			t.Fatalf("forwarded status=%v, want %v", got, sip.ResponseStatusNotFound)
		}
		if got := nextCalls.Load(); got != 0 {
			t.Fatalf("next calls=%d, want 0 (owned response consumed by the proxy)", got)
		}
		if got := resViaCount(res); got != 2 {
			t.Fatalf("inbound response vias=%d, want 2 (envelope never mutated)", got)
		}
	})

	t.Run("invite provisional forwarded", func(t *testing.T) {
		t.Parallel()

		var nextCalls atomic.Int32
		next := sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error {
			nextCalls.Add(1)
			return nil
		})
		elm, prx, tp := newOwnershipProxy(t)

		out := emitStateless(t, elm, prx, tp, sip.MagicCookie+".own-180")
		res := newInRes(t, out, sip.ResponseStatusRinging)

		if err := prx.InterceptInboundResponse(sip.ContextWithElement(ctx, elm), next, res); err != nil {
			t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
		}

		sent := testutil.WaitResult(t, tp.sendResCalls, "forwarded provisional").res
		if got := sent.Status(); !got.Equal(sip.ResponseStatusRinging) {
			t.Fatalf("forwarded status=%v, want %v", got, sip.ResponseStatusRinging)
		}
		if got := resViaCount(sent); got != 1 {
			t.Fatalf("forwarded vias=%d, want 1", got)
		}
		if got := nextCalls.Load(); got != 0 {
			t.Fatalf("next calls=%d, want 0", got)
		}
	})

	t.Run("foreign response passes next", func(t *testing.T) {
		t.Parallel()

		var nextCalls atomic.Int32
		next := sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error {
			nextCalls.Add(1)
			return nil
		})
		elm, prx, tp := newOwnershipProxy(t)

		// A response whose top Via is outside the proxy namespace is
		// foreign: it continues down the receiver chain untouched.
		in := newInInviteReq(t, "UDP", sip.MagicCookie+".foreign", laddr, raddr)
		res := newInRes(t, in, sip.ResponseStatusBusyHere)

		if err := prx.InterceptInboundResponse(sip.ContextWithElement(ctx, elm), next, res); err != nil {
			t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
		}
		if got := nextCalls.Load(); got != 1 {
			t.Fatalf("foreign next calls=%d, want 1", got)
		}
		if got := tp.countSendRes(); got != 0 {
			t.Fatal("foreign response reached transport")
		}
	})

	t.Run("stateful stray discarded", func(t *testing.T) {
		t.Parallel()

		var nextCalls atomic.Int32
		next := sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error {
			nextCalls.Add(1)
			return nil
		})
		elm, prx, tp := newOwnershipProxy(t)

		in := newInInviteReq(t, "UDP", sip.MagicCookie+".stray", laddr, raddr)
		for _, sts := range []sip.ResponseStatus{
			sip.ResponseStatusBusyHere,
			// A late 2xx outliving its transaction is discarded the same
			// way (RFC 4320/6026): it can never take the stateless relay.
			sip.ResponseStatusOK,
		} {
			res := newInRes(t, in, sts)
			prependMarkedVia(res, "proxy-stateful")

			if err := prx.InterceptInboundResponse(sip.ContextWithElement(ctx, elm), next, res); err != nil {
				t.Fatalf("prx.InterceptInboundResponse(%v) error = %v, want nil", sts, err)
			}
		}

		if got := nextCalls.Load(); got != 0 {
			t.Fatalf("next calls=%d, want 0", got)
		}
		if got := tp.countSendRes(); got != 0 {
			t.Fatal("discarded response reached transport")
		}
	})

	t.Run("claimed invalid via discarded", func(t *testing.T) {
		t.Parallel()

		var nextCalls atomic.Int32
		next := sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error {
			nextCalls.Add(1)
			return nil
		})
		elm, prx, tp := newOwnershipProxy(t)
		out := emitStateless(t, elm, prx, tp, sip.MagicCookie+".own-invalid")

		// The stateless mark is claimed but the receiving transport does
		// not own the Via sent-by: the response is consumed, not relayed.
		res := newInRes(t, out, sip.ResponseStatusNotFound)
		rejCtx := sip.ContextWithTransport(ctx, statelessSentByRejecter{tp})
		if err := prx.InterceptInboundResponse(sip.ContextWithElement(rejCtx, elm), next, res); err != nil {
			t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
		}

		// A syntactically invalid claimed Via is consumed the same way.
		in := newInInviteReq(t, "UDP", sip.MagicCookie+".broken", laddr, raddr)
		res2 := newInRes(t, in, sip.ResponseStatusBusyHere)
		res2.WithMessage(func(r *sip.Response) {
			r.Headers.PrependVia(header.ViaHop{
				Proto:     sip.ProtoVer20(),
				Transport: "UDP",
				Params: make(sip.Values).Set(
					"branch",
					sip.MagicCookie+".proxy-stateless."+strings.Repeat("ab", 16)+".broken",
				),
			})
		})
		if err := prx.InterceptInboundResponse(sip.ContextWithElement(ctx, elm), next, res2); err != nil {
			t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
		}

		if got := nextCalls.Load(); got != 0 {
			t.Fatalf("next calls=%d, want 0", got)
		}
		if got := tp.countSendRes(); got != 0 {
			t.Fatal("discarded response reached transport")
		}
	})

	t.Run("no remaining via not forwarded", func(t *testing.T) {
		t.Parallel()

		var nextCalls atomic.Int32
		next := sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error {
			nextCalls.Add(1)
			return nil
		})
		elm, prx, tp := newOwnershipProxy(t)

		// A response carrying only this proxy's stateless Via — for
		// example a locally generated CANCEL answer whose operation never
		// crossed downstream — has no recorded prior hop and is consumed.
		in := newInInviteReq(t, "UDP", sip.MagicCookie+".cancel-local", laddr, raddr)
		res := newInRes(t, in, sip.ResponseStatusOK)
		res.WithMessage(func(r *sip.Response) {
			r.Headers.Set(header.Via{{
				Proto:     sip.ProtoVer20(),
				Transport: "UDP",
				Addr:      sip.MakeHostPortAddr("192.168.1.100", 5060),
				Params: make(sip.Values).Set(
					"branch",
					sip.MagicCookie+".proxy-stateless."+strings.Repeat("ab", 16)+".local",
				),
			}})
			r.Headers.Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodCancel})
		})

		if err := prx.InterceptInboundResponse(sip.ContextWithElement(ctx, elm), next, res); err != nil {
			t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
		}
		if got := nextCalls.Load(); got != 0 {
			t.Fatalf("next calls=%d, want 0", got)
		}
		if got := tp.countSendRes(); got != 0 {
			t.Fatal("response with no remaining Via reached transport")
		}
	})

	t.Run("invalid next via consumed", func(t *testing.T) {
		t.Parallel()

		var nextCalls atomic.Int32
		next := sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error {
			nextCalls.Add(1)
			return nil
		})
		elm, prx, tp := newOwnershipProxy(t)
		out := emitStateless(t, elm, prx, tp, sip.MagicCookie+".own-badnext")

		// The recorded prior hop is unroutable: the owned response is
		// consumed rather than emitted toward a broken destination.
		res := newInRes(t, out, sip.ResponseStatusNotFound)
		res.WithMessage(func(r *sip.Response) {
			if via, ok := r.Headers.LastVia(); ok && via != nil {
				via.Addr = sip.Addr{}
			}
		})

		if err := prx.InterceptInboundResponse(sip.ContextWithElement(ctx, elm), next, res); err != nil {
			t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
		}
		if got := nextCalls.Load(); got != 0 {
			t.Fatalf("next calls=%d, want 0", got)
		}
		if got := tp.countSendRes(); got != 0 {
			t.Fatal("unrouteable response reached transport")
		}
	})

	t.Run("stateful traffic not stolen", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			var nextCalls atomic.Int32
			next := sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error {
				nextCalls.Add(1)
				return nil
			})

			tp := newStubTransp()
			elm := &sip.Element{}
			t.Cleanup(func() { elm.Close() })
			if err := elm.TrackTransport(tp); err != nil {
				t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
			}

			// One element owns both forwarding modes: the stateful route
			// claims requests for user "st", the stateless route takes
			// everything else.
			prx, err := proxy.New([]proxy.Route{
				{
					Name:        "stateful",
					ForwardMode: proxy.ForwardModeStateful,
					MatchRequest: func(_ context.Context, req *sip.RequestEnvelope) bool {
						u, ok := req.URI().(*sip.URI)
						return ok && u.User.Username() == "st"
					},
					ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
						return statelessPlan(proxy.Target{
							ID:  "st1",
							URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr("10.0.0.9", 5060)},
						}), nil
					},
				},
				*statelessPlanRoute(statelessPlan(statelessLitTarget("t1", "198.51.100.10"))),
			}, proxy.Options{StatelessKey: key, ServedDomains: served})
			if err != nil {
				t.Fatalf("proxy.New() error = %v, want nil", err)
			}
			elm.UseComponent(prx)

			// A stateful admission leaves a live client transaction whose
			// outbound Via carries the proxy-stateful mark.
			stReq := newInInviteReq(t, "UDP", sip.MagicCookie+".mixed-st", laddr, raddr)
			stReq.WithMessage(func(r *sip.Request) {
				r.URI = &sip.URI{User: sip.MakeUserInfo("st"), Addr: sip.MakeHostAddr("alice.voip.com")}
			})
			if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, stReq); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			stOut := testutil.WaitResult(t, tp.sendReqCalls, "stateful outbound send").req
			key, err := sip.MakeClientTransactionKey(stOut)
			if err != nil {
				t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
			}
			if _, ok := elm.LoadClientTransaction(key); !ok {
				t.Fatal("stateful admission produced no live client transaction")
			}
			drainRess(tp) // admission may have emitted an upstream provisional

			// The live transaction's response is never stolen by the
			// stateless relay: it continues down the receiver chain.
			stRes := newInRes(t, stOut, sip.ResponseStatusBusyHere)
			if err := prx.InterceptInboundResponse(sip.ContextWithElement(ctx, elm), next, stRes); err != nil {
				t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
			}
			if got := nextCalls.Load(); got != 1 {
				t.Fatalf("stateful response next calls=%d, want 1", got)
			}
			if got := tp.countSendRes(); got != 0 {
				t.Fatal("stateful response forwarded through the stateless path")
			}

			// A stateless response on the same element still relays.
			slOut := emitStateless(t, elm, prx, tp, sip.MagicCookie+".mixed-sl")
			slRes := newInRes(t, slOut, sip.ResponseStatusNotFound)
			if err := prx.InterceptInboundResponse(sip.ContextWithElement(ctx, elm), next, slRes); err != nil {
				t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
			}
			sent := testutil.WaitResult(t, tp.sendResCalls, "stateless response send").res
			if got := resViaCount(sent); got != 1 {
				t.Fatalf("forwarded vias=%d, want 1", got)
			}
			if got := nextCalls.Load(); got != 1 {
				t.Fatalf("next calls=%d, want 1", got)
			}
		})
	})
}

// TestProxy_StatelessForwarding_RelatedMethodBranch pins the complete
// stateless Via branch normalization for related methods: an INVITE, its
// exact retransmission, a related CANCEL, and a non-routed ACK carrying
// the acknowledged response's To tag all emit the identical complete top
// Via branch and commit to the identical destination without a
// transaction or request coordinator, for magic-cookie and legacy inputs.
// An ACK with a distinct upstream transaction identity may
// carry a different branch, and the stateless ownership mark keeps
// loop/spiral detection consistent on a returning request.
func TestProxy_StatelessForwarding_RelatedMethodBranch(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	key := []byte("related-method-branch-key")
	served := []string{"alice.voip.com"}
	newBranchProxy := func(tb testing.TB, opts proxy.Options) (*sip.Element, *proxy.Proxy, *stubTransp) {
		tb.Helper()
		return newConfiguredTestProxy(tb,
			statelessPlanRoute(statelessPlan(statelessLitTarget("t1", "198.51.100.10"))),
			opts,
		)
	}

	for _, family := range []struct {
		name         string
		branchPrefix string
	}{
		{"magic cookie", sip.MagicCookie + ".sls"},
		{"legacy", "legacy.sls"},
	} {
		t.Run(family.name, func(t *testing.T) {
			t.Parallel()

			t.Run("related methods share the complete branch", func(t *testing.T) {
				t.Parallel()

				synctest.Test(t, func(t *testing.T) {
					ctx := t.Context()
					elm, prx, tp := newBranchProxy(t, proxy.Options{
						StatelessKey:  key,
						ServedDomains: served,
					})

					invite := newInInviteReq(t, "UDP", family.branchPrefix+".rel-inv", laddr, raddr)
					to, ok := invite.Message().Headers.To()
					if !ok {
						t.Fatal("INVITE has no To header")
					}
					if tag, ok := to.Tag(); ok || tag != "" {
						t.Fatalf("INVITE To tag = (%q, %v), want (empty, false)", tag, ok)
					}
					cancel, err := sip.NewCancelRequestEnvelope(invite)
					if err != nil {
						t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
					}

					// A non-routed ACK keeps the upstream transaction identity but
					// carries the acknowledged response's To tag.
					res := newInRes(t, invite, sip.ResponseStatusRequestTerminated)
					res.WithMessage(func(r *sip.Response) {
						to, ok := r.Headers.To()
						if !ok {
							t.Fatal("487 response has no To header")
						}
						if to.Params == nil {
							to.Params = make(sip.Values)
						}
						to.Params.Set("tag", "related-ack-tag")
					})
					ack := newInAckReq(t, invite, res)
					ackTo, ok := ack.Message().Headers.To()
					if !ok {
						t.Fatal("ACK has no To header")
					}
					if tag, ok := ackTo.Tag(); !ok || tag != "related-ack-tag" {
						t.Fatalf("ACK To tag = (%q, %v), want (related-ack-tag, true)", tag, ok)
					}
					if got, want := statelessTopBranch(t, ack), statelessTopBranch(t, invite); got != want {
						t.Fatalf("ACK upstream branch = %q, want INVITE branch %q", got, want)
					}

					var wantBranch string
					for _, incoming := range []struct {
						name string
						req  *sip.RequestEnvelope
					}{
						{"invite", invite},
						{"retransmission", invite},
						{"cancel", cancel},
						{"ack", ack},
					} {
						txKey, err := sip.MakeServerTransactionKey(incoming.req)
						if err != nil {
							t.Fatalf("%s sip.MakeServerTransactionKey() error = %v, want nil", incoming.name, err)
						}
						if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, incoming.req); err != nil {
							t.Fatalf("%s prx.InterceptInboundRequest() error = %v, want nil", incoming.name, err)
						}
						sent := testutil.WaitResult(t, tp.sendReqCalls, "stateless "+incoming.name+" send").req
						branch := statelessTopBranch(t, sent)
						if wantBranch == "" {
							wantBranch = branch
							if !strings.HasPrefix(wantBranch, sip.MagicCookie+".proxy-stateless.") {
								t.Fatalf("top via branch = %q, want proxy-stateless mark", wantBranch)
							}
						} else if branch != wantBranch {
							t.Fatalf("%s top via branch = %q, want identical %q", incoming.name, branch, wantBranch)
						}
						if got := sent.RemoteAddr(); got != netip.MustParseAddrPort("198.51.100.10:5060") {
							t.Fatalf("destination = %v, want 198.51.100.10:5060", got)
						}
						if got := sent.Transport().Proto; !got.Equal(sip.TransportProto("UDP")) {
							t.Fatalf("destination transport = %q, want UDP", got)
						}
						if got := countAllTxs(elm); got != 0 {
							t.Fatalf("transactions = %d, want 0", got)
						}
						if req, ok := prx.LookupRequest(elm, txKey); req != nil || ok {
							t.Fatalf("%s prx.LookupRequest(elm, ) = (%v, %v), want (nil, false)", incoming.name, req, ok)
						}
						if got := tp.countSendRes(); got != 0 {
							t.Fatalf("upstream responses = %d, want 0 (no provisional on the stateless path)", got)
						}
					}
				})
			})

			t.Run("distinct transaction identity", func(t *testing.T) {
				t.Parallel()

				synctest.Test(t, func(t *testing.T) {
					ctx := t.Context()
					elm, prx, tp := newBranchProxy(t, proxy.Options{
						StatelessKey:  key,
						ServedDomains: served,
					})

					invite := newInInviteReq(t, "UDP", family.branchPrefix+".dist-inv", laddr, raddr)
					if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, invite); err != nil {
						t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
					}
					invBranch := statelessTopBranch(t, testutil.WaitResult(t, tp.sendReqCalls, "stateless INVITE send").req)
					if got := countAllTxs(elm); got != 0 {
						t.Fatalf("transactions = %d, want 0", got)
					}

					// Choose a distinct upstream identity in both families: the
					// shared ACK helper only changes magic-cookie branches.
					ack := newInAckReq(t, invite, newInRes(t, invite, sip.ResponseStatusOK))
					ack.WithMessage(func(r *sip.Request) {
						via, ok := r.Headers.FirstVia()
						if !ok {
							t.Fatal("ACK has no top Via")
						}
						via.Params.Set("branch", family.branchPrefix+".distinct-ack")
					})
					if got, inv := statelessTopBranch(t, ack), statelessTopBranch(t, invite); got == inv {
						t.Fatalf("ACK upstream branch = %q, want different from INVITE", got)
					}
					if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, ack); err != nil {
						t.Fatalf("ACK prx.InterceptInboundRequest() error = %v, want nil", err)
					}
					ackBranch := statelessTopBranch(t, testutil.WaitResult(t, tp.sendReqCalls, "stateless ACK send").req)
					if ackBranch == invBranch {
						t.Fatalf("distinct upstream transaction kept branch %q", ackBranch)
					}
					if got := countAllTxs(elm); got != 0 {
						t.Fatalf("transactions = %d, want 0", got)
					}
				})
			})

			t.Run("loop and spiral with the stateless mark", func(t *testing.T) {
				t.Parallel()

				synctest.Test(t, func(t *testing.T) {
					ctx := t.Context()
					elm, prx, tp := newBranchProxy(t, proxy.Options{
						DetectLoops:   true,
						StatelessKey:  key,
						ServedDomains: served,
					})

					invite := newInInviteReq(t, "UDP", family.branchPrefix+".loop-inv", laddr, raddr)
					if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, invite); err != nil {
						t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
					}
					out := testutil.WaitResult(t, tp.sendReqCalls, "stateless INVITE send").req
					if got := countAllTxs(elm); got != 0 {
						t.Fatalf("transactions = %d, want 0", got)
					}

					// The emitted message returning unchanged is a loop: the
					// embedded stateless fingerprint reproduces itself. The
					// fed-back request is rebuilt from the wire message because
					// envelope metadata never survives a network round-trip.
					loop := sip.NewRequestEnvelope(out.Message().Clone().(*sip.Request)).
						SetTransport(sip.UDPMetadata()).
						SetLocalAddr(laddr).
						SetRemoteAddr(raddr)
					err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, loop)
					if err == nil {
						t.Fatal("prx.InterceptInboundRequest() error = nil, want loop rejection")
					}
					var rej *sip.RequestRejectedError
					if !errors.As(err, &rej) {
						t.Fatalf("prx.InterceptInboundRequest() error = %v, want *sip.RequestRejectedError", err)
					}
					if got, want := rej.ResponseStatus(), sip.ResponseStatusLoopDetected; got != want {
						t.Fatalf("rejection status = %v, want %v", got, want)
					}
					if got := len(drainReqs(tp)); got != 0 {
						t.Fatalf("sends=%d, want 0 (a loop is never forwarded)", got)
					}
					if got := countAllTxs(elm); got != 0 {
						t.Fatalf("transactions = %d, want 0", got)
					}

					// A copy returning with changed routing data is a spiral: the
					// added next-hop Route rewrites the fingerprint, and the
					// request is forwarded to the new hop.
					spiral := sip.NewRequestEnvelope(out.Message().Clone().(*sip.Request)).
						SetTransport(sip.UDPMetadata()).
						SetLocalAddr(laddr).
						SetRemoteAddr(raddr)
					spiral.WithMessage(func(r *sip.Request) {
						r.Headers.AppendRoute(header.RouteHop{URI: &sip.URI{
							Addr:   sip.MakeHostPortAddr("192.0.2.55", 5060),
							Params: make(sip.Values).Set("lr", ""),
						}})
					})
					if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, spiral); err != nil {
						t.Fatalf("spiral prx.InterceptInboundRequest() error = %v, want nil", err)
					}
					sent := testutil.WaitResult(t, tp.sendReqCalls, "spiral forward send").req
					if got, want := sent.RemoteAddr(), netip.MustParseAddrPort("192.0.2.55:5060"); got != want {
						t.Fatalf("spiral destination = %v, want %v", got, want)
					}
					if got := countAllTxs(elm); got != 0 {
						t.Fatalf("transactions = %d, want 0", got)
					}
				})
			})
		})
	}
}

// TestProxy_StatelessMetadata pins the honest stateless correlation
// contract: a claimed inbound request keeps its genuinely known claim
// record, the downstream copy adds the genuinely known single-target
// correlation at the forward stage, and an unattributed stateless
// response carries mode and stage alone — never fabricated request,
// target, attempt, or route identities. Every fresh traversal replaces a
// prior sip.proxy value wholesale, forged metadata never bypasses Via
// ownership, no request-history store exists to recover IDs from, and
// correlation IDs stay out of the deterministic wire branch.
func TestProxy_StatelessMetadata(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	key := []byte("stateless-metadata-key")
	served := []string{"alice.voip.com"}

	newSlsProxy := func(tb testing.TB, routeName string) (*sip.Element, *proxy.Proxy, *stubTransp) {
		tb.Helper()
		route := statelessPlanRoute(statelessPlan(statelessLitTarget("t1", "198.51.100.10")))
		route.Name = routeName
		return newConfiguredTestProxy(tb, route, proxy.Options{
			StatelessKey:  key,
			ServedDomains: served,
		})
	}
	// emitSls sends one inbound INVITE through the stateless route and
	// returns the emitted outbound copy.
	emitSls := func(
		tb testing.TB,
		elm *sip.Element,
		prx *proxy.Proxy,
		tp *stubTransp,
		branch string,
	) *sip.RequestEnvelope {
		tb.Helper()
		req := newInInviteReq(tb, "UDP", branch, laddr, raddr)
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, req); err != nil {
			tb.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}
		return testutil.WaitResult(tb, tp.sendReqCalls, "stateless outbound send").req
	}
	reqStamp := func(tb testing.TB, env *sip.RequestEnvelope) proxy.Metadata {
		tb.Helper()
		md, ok := proxy.MetadataFrom(env.Metadata())
		if !ok {
			tb.Fatalf("proxy.MetadataFrom() = (%+v, %v), want a valid proxy stamp", md, ok)
		}
		return md
	}
	resStamp := func(tb testing.TB, env *sip.ResponseEnvelope) proxy.Metadata {
		tb.Helper()
		md, ok := proxy.MetadataFrom(env.Metadata())
		if !ok {
			tb.Fatalf("proxy.MetadataFrom() = (%+v, %v), want a valid proxy stamp", md, ok)
		}
		return md
	}

	t.Run("known inbound claim", func(t *testing.T) {
		t.Parallel()

		elm, prx, tp := newSlsProxy(t, "slmd-claim")
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".sls-md-claim", laddr, raddr)
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, req); err != nil {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}

		in := reqStamp(t, req)
		if in.RequestID == "" || in.Stage != proxy.StageClaim ||
			in.RouteName != "slmd-claim" || in.Mode != proxy.ForwardModeStateless {
			t.Fatalf("inbound claim stamp = %+v, want nonempty RequestID/RouteName at claim in stateless mode", in)
		}
		if in.TargetID != "" || in.AttemptID != "" {
			t.Fatalf("inbound claim stamp = %+v, want empty target/attempt (no hop is known yet)", in)
		}
		if got := len(drainReqs(tp)); got != 1 {
			t.Fatalf("outbound sends=%d, want 1", got)
		}
	})

	t.Run("known downstream forward", func(t *testing.T) {
		t.Parallel()

		elm, prx, tp := newSlsProxy(t, "slmd-fwd")
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".sls-md-fwd", laddr, raddr)
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, req); err != nil {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}

		in := reqStamp(t, req)
		sends := drainReqs(tp)
		if len(sends) != 1 {
			t.Fatalf("outbound sends=%d, want 1", len(sends))
		}
		out := reqStamp(t, sends[0])
		if out.RequestID == "" || out.RequestID != in.RequestID {
			t.Errorf("outbound stamp = %+v, want the claim's request ID %q", out, in.RequestID)
		}
		// The single deterministic send is the target's only attempt, so
		// it shares the target ID the observation events already carry.
		if out.TargetID != "t1" || out.AttemptID != "t1" {
			t.Errorf("outbound stamp = %+v, want target/attempt t1/t1", out)
		}
		if out.Stage != proxy.StageForward || out.RouteName != "slmd-fwd" || out.Mode != proxy.ForwardModeStateless {
			t.Errorf("outbound stamp = %+v, want route slmd-fwd at stage forward in stateless mode", out)
		}
	})

	t.Run("roundtrip stripped response", func(t *testing.T) {
		t.Parallel()

		elm, prx, tp := newSlsProxy(t, "slmd-res")
		out := emitSls(t, elm, prx, tp, sip.MagicCookie+".sls-md-res")

		// A stale or forged value riding the inbound envelope is replaced
		// whole by the honest record: the relay keeps no request history,
		// so mode and stage are all it genuinely knows.
		res := newInRes(t, out, sip.ResponseStatusNotFound)
		proxy.SetMetadata(res.Metadata(), proxy.Metadata{
			RequestID:        "forged-req",
			RelatedRequestID: "forged-rel",
			TargetID:         "forged-t",
			AttemptID:        "forged-a",
			RouteName:        "forged-route",
			Mode:             proxy.ForwardModeStateful,
			Stage:            proxy.StageClaim,
		})
		var nextCalls atomic.Int32
		next := sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error {
			nextCalls.Add(1)
			return nil
		})
		if err := prx.InterceptInboundResponse(sip.ContextWithElement(ctx, elm), next, res); err != nil {
			t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
		}

		wantRes := proxy.Metadata{Mode: proxy.ForwardModeStateless, Stage: proxy.StageResponse}
		if diff := cmp.Diff(wantRes, resStamp(t, res)); diff != "" {
			t.Errorf("inbound response stamp mismatch (-want +got):\n%s", diff)
		}
		sent := testutil.WaitResult(t, tp.sendResCalls, "stateless response send").res
		wantUp := proxy.Metadata{Mode: proxy.ForwardModeStateless, Stage: proxy.StageUpstream}
		if diff := cmp.Diff(wantUp, resStamp(t, sent)); diff != "" {
			t.Errorf("upstream copy stamp mismatch (-want +got):\n%s", diff)
		}
		if got := nextCalls.Load(); got != 0 {
			t.Fatalf("next calls=%d, want 0 (owned response consumed by the proxy)", got)
		}
	})

	t.Run("fresh loopback", func(t *testing.T) {
		t.Parallel()

		elm, prx, tp := newSlsProxy(t, "slmd-loop")
		out := emitSls(t, elm, prx, tp, sip.MagicCookie+".sls-md-loop")
		first := reqStamp(t, out)

		// The emitted message traversing the proxy again is a fresh
		// traversal: the new claim replaces the prior sip.proxy value
		// wholesale rather than merging its forward record.
		loop := sip.NewRequestEnvelope(out.Message().Clone().(*sip.Request)).
			SetTransport(sip.UDPMetadata()).
			SetLocalAddr(laddr).
			SetRemoteAddr(raddr)
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, loop); err != nil {
			t.Fatalf("loopback prx.InterceptInboundRequest() error = %v, want nil", err)
		}
		re := reqStamp(t, loop)
		if re.RequestID == "" || re.RequestID == first.RequestID ||
			re.Stage != proxy.StageClaim || re.RouteName != "slmd-loop" ||
			re.Mode != proxy.ForwardModeStateless {
			t.Fatalf("re-traversed claim stamp = %+v, want a fresh claim record of route slmd-loop", re)
		}
		if re.TargetID != "" || re.AttemptID != "" || re.RelatedRequestID != "" {
			t.Fatalf("re-traversed claim stamp = %+v, want the prior forward record replaced, not merged", re)
		}
	})

	t.Run("retransmit equal branch", func(t *testing.T) {
		t.Parallel()

		elm, prx, tp := newSlsProxy(t, "slmd-branch")
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".sls-md-branch", laddr, raddr)
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, req); err != nil {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}
		sent1 := testutil.WaitResult(t, tp.sendReqCalls, "first stateless send").req
		md1 := reqStamp(t, req)

		// A retransmission mints a distinct operation identity, but the
		// correlation ID never enters the wire branch or the destination.
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, req); err != nil {
			t.Fatalf("retransmission prx.InterceptInboundRequest() error = %v, want nil", err)
		}
		sent2 := testutil.WaitResult(t, tp.sendReqCalls, "stateless retransmission send").req
		md2 := reqStamp(t, req)

		if md1.RequestID == "" || md1.RequestID == md2.RequestID {
			t.Fatalf("operation request IDs %q vs %q, want distinct minted identities", md1.RequestID, md2.RequestID)
		}
		if b1, b2 := statelessTopBranch(t, sent1), statelessTopBranch(t, sent2); b1 != b2 {
			t.Fatalf("retransmission branches %q vs %q, want byte-identical", b1, b2)
		}
		if got, want := sent1.RemoteAddr(), sent2.RemoteAddr(); got != want {
			t.Fatalf("retransmission destinations %v vs %v, want identical", got, want)
		}
	})

	t.Run("forged metadata foreign via", func(t *testing.T) {
		t.Parallel()

		elm, prx, tp := newSlsProxy(t, "slmd-forge")
		forged := proxy.Metadata{
			RequestID: "forged-req",
			TargetID:  "forged-t",
			AttemptID: "forged-a",
			RouteName: "slmd-forge",
			Mode:      proxy.ForwardModeStateless,
			Stage:     proxy.StageResponse,
		}

		var nextCalls atomic.Int32
		next := sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error {
			nextCalls.Add(1)
			return nil
		})

		// A response outside the proxy namespace is foreign no matter
		// what its metadata asserts: it continues down the chain and the
		// forged value is left alone.
		foreign := newInRes(t,
			newInInviteReq(t, "UDP", sip.MagicCookie+".sls-md-frn", laddr, raddr),
			sip.ResponseStatusNotFound,
		)
		proxy.SetMetadata(foreign.Metadata(), forged)
		if err := prx.InterceptInboundResponse(sip.ContextWithElement(ctx, elm), next, foreign); err != nil {
			t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
		}
		if got := nextCalls.Load(); got != 1 {
			t.Fatalf("foreign response next calls=%d, want 1", got)
		}
		if md, ok := proxy.MetadataFrom(foreign.Metadata()); !ok || md != forged {
			t.Fatalf("foreign response stamp = (%+v, %v), want the forged value untouched %+v", md, ok, forged)
		}

		// A claimed-marked Via whose sent-by is not this element's is
		// consumed without relay or attribution either: forged metadata
		// can neither pass Via ownership nor claim an identity.
		out := emitSls(t, elm, prx, tp, sip.MagicCookie+".sls-md-own")
		claimed := newInRes(t, out, sip.ResponseStatusBusyHere)
		proxy.SetMetadata(claimed.Metadata(), forged)
		rejCtx := sip.ContextWithTransport(ctx, statelessSentByRejecter{tp})
		if err := prx.InterceptInboundResponse(sip.ContextWithElement(rejCtx, elm), next, claimed); err != nil {
			t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
		}
		if got := nextCalls.Load(); got != 1 {
			t.Fatalf("claimed-invalid response next calls=%d, want 1 (consumed, never chained)", got)
		}
		if got := tp.countSendRes(); got != 0 {
			t.Fatal("forged metadata bypassed Via ownership into a relay send")
		}
	})

	t.Run("stale route no leak", func(t *testing.T) {
		t.Parallel()

		elm, prx, _ := newSlsProxy(t, "slmd-route")
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".sls-md-stale", laddr, raddr)
		proxy.SetMetadata(req.Metadata(), proxy.Metadata{
			RequestID: "stale-id",
			RouteName: "stale-route",
			Mode:      proxy.ForwardModeStateless,
			Stage:     proxy.StageResponse,
		})
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, req); err != nil {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}

		md := reqStamp(t, req)
		if md.RouteName != "slmd-route" || md.RequestID == "stale-id" || md.RequestID == "" {
			t.Fatalf("claim stamp = %+v, want the stale record replaced by a fresh claim of route slmd-route", md)
		}
	})

	t.Run("no history store", func(t *testing.T) {
		t.Parallel()

		elm, prx, tp := newSlsProxy(t, "slmd-store")
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".sls-md-store", laddr, raddr)
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, req); err != nil {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}
		out := testutil.WaitResult(t, tp.sendReqCalls, "stateless outbound send").req

		res := newInRes(t, out, sip.ResponseStatusNotFound)
		next := sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error { return nil })
		if err := prx.InterceptInboundResponse(sip.ContextWithElement(ctx, elm), next, res); err != nil {
			t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
		}
		testutil.WaitResult(t, tp.sendResCalls, "stateless response send")

		// The full stateless roundtrip installs nothing recoverable: no
		// transaction, no request index entry, and no history store the
		// response attribution could have recovered IDs from.
		key, err := sip.MakeServerTransactionKey(req)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		if rq, ok := prx.LookupRequest(elm, key); ok {
			t.Fatalf("prx.LookupRequest() = (%v, %v), want no entry after a stateless traversal", rq, ok)
		}
		if got := countAllTxs(elm); got != 0 {
			t.Fatalf("transactions=%d, want 0", got)
		}
		// Internal inspection: the only request index the proxy keeps —
		// the per-element owned server-request map — gains no entry from
		// a stateless traversal.
		elms := reflect.ValueOf(prx).Elem().FieldByName("elms")
		if elms.IsValid() && elms.Kind() == reflect.Map {
			if es := elms.MapIndex(reflect.ValueOf(elm)); es.IsValid() {
				if idx := es.Elem().FieldByName("srvRqs"); idx.IsValid() && idx.Len() != 0 {
					t.Fatalf("request index entries=%d, want 0 (no stateless history store)", idx.Len())
				}
			}
		}
	})
}
