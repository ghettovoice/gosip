package proxy_test

import (
	"context"
	"iter"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/proxy"
)

// attemptActionLog records one ordered log of lazy-resolve and send actions
// shared between the locator and transport stubs of a subtest.
type attemptActionLog struct {
	mu      sync.Mutex
	actions []string
}

func (l *attemptActionLog) add(action string) {
	l.mu.Lock()
	l.actions = append(l.actions, action)
	l.mu.Unlock()
}

func (l *attemptActionLog) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.actions...)
}

// attemptCandidate names one lazy next-hop candidate of a target.
type attemptCandidate struct {
	name string
	addr sip.ResolvedAddr
}

// attemptLocator resolves a request target URI into the ordered candidate
// list configured for its host. Resolution is lazy: each candidate emits one
// "resolve" action only when the caller pulls it, and lookups counts the
// pulled candidates.
type attemptLocator struct {
	log        *attemptActionLog
	lookups    *atomic.Int32
	candidates map[string][]attemptCandidate
}

func (l *attemptLocator) LookupRequestAddrs(
	_ context.Context,
	uri *sip.URI,
	_ ...sip.LookupMessageAddrsOptions,
) iter.Seq[sip.ResolvedAddr] {
	return func(yield func(sip.ResolvedAddr) bool) {
		for _, cand := range l.candidates[uri.Addr.Host()] {
			l.lookups.Add(1)
			l.log.add("resolve:" + cand.name)
			if !yield(cand.addr) {
				return
			}
		}
	}
}

func udpAttemptCandidate(name, hostport string) attemptCandidate {
	return attemptCandidate{
		name: name,
		addr: sip.ResolvedAddr{
			Transport: "UDP",
			Addr:      netip.MustParseAddrPort(hostport),
		},
	}
}

func attemptPlan(groups ...[]proxy.Target) proxy.TargetPlan {
	var plan proxy.TargetPlan
	for _, group := range groups {
		plan.Groups = append(plan.Groups, append([]proxy.Target(nil), group...))
	}
	return plan
}

func attemptURITarget(id, host string) proxy.Target {
	return proxy.Target{
		ID: id,
		URI: &sip.URI{
			User: sip.MakeUserInfo("alice"),
			Addr: sip.MakeHostPortAddr(host, 5060),
		},
	}
}

func attemptRoute(name string, plan proxy.TargetPlan) *proxy.Route {
	return &proxy.Route{
		Name:        name,
		ForwardMode: proxy.ForwardModeStateful,
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
			return plan, nil
		},
	}
}

func newAttemptTestProxy(
	tb testing.TB,
	route *proxy.Route,
	opts proxy.Options,
	loc *attemptLocator,
) (*sip.Element, *proxy.Proxy, *stubTransp) {
	tb.Helper()
	return newConfiguredTestProxy(tb, route, opts, &sip.Element{ServerLocator: loc})
}

// attemptSendNamer maps an outbound request to its candidate name through the
// per-subtest remote-address table.
func attemptSendNamer(call sendReqCall, names map[string]string) string {
	addr := call.req.RemoteAddr().Addr().String()
	if name, ok := names[addr]; ok {
		return name
	}
	return addr
}

// attemptInviteSends drops the ACK copies an INVITE client transaction emits
// on a non-2xx final response, keeping only branch starts.
func attemptInviteSends(reqs []*sip.RequestEnvelope) []*sip.RequestEnvelope {
	var out []*sip.RequestEnvelope
	for _, req := range reqs {
		if req.Method().Equal(sip.RequestMethodInvite) {
			out = append(out, req)
		}
	}
	return out
}

// attemptRecSend returns a sendReqHook that records "send:<name>@<proto>"
// actions for INVITE sends only; retransmitted ACKs are ignored.
func attemptRecSend(
	log *attemptActionLog,
	names map[string]string,
) func(sendReqCall, int) error {
	return func(call sendReqCall, _ int) error {
		if !call.req.Method().Equal(sip.RequestMethodInvite) {
			return nil
		}
		name := attemptSendNamer(call, names)
		log.add("send:" + name + "@" + string(call.req.Transport().Proto))
		return nil
	}
}

func attemptTopBranch(tb testing.TB, req *sip.RequestEnvelope) string {
	tb.Helper()
	var branch string
	req.WithMessage(func(r *sip.Request) {
		if via, ok := r.Headers.FirstVia(); ok {
			branch, _ = via.Branch()
		}
	})
	return branch
}

func attemptClnTx(
	tb testing.TB,
	elm *sip.Element,
	req *sip.RequestEnvelope,
) sip.ClientTransaction {
	tb.Helper()
	key, err := sip.MakeClientTransactionKey(req)
	if err != nil {
		tb.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
	}
	tx, ok := elm.LoadClientTransaction(key)
	if !ok {
		tb.Fatal("forwarding client transaction not registered")
	}
	return tx
}

func admitAttemptReq(ctx context.Context, tb testing.TB, elm *sip.Element, prx *proxy.Proxy, req *sip.RequestEnvelope) {
	tb.Helper()
	next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
		return nil
	})
	if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), next, req); err != nil {
		tb.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
	}
}

func TestProxy_AttemptFailover(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	prxOpts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}

	newInbound := func(tb testing.TB, branch string) *sip.RequestEnvelope {
		tb.Helper()
		return newInInviteReq(tb, "UDP", branch, laddr, raddr)
	}

	t.Run("successful initial send pulls no further candidate", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			log := &attemptActionLog{}
			var lookups atomic.Int32
			loc := &attemptLocator{
				log:     log,
				lookups: &lookups,
				candidates: map[string][]attemptCandidate{
					"10.0.0.9": {
						udpAttemptCandidate("first", "10.0.0.9:5060"),
						udpAttemptCandidate("second", "10.0.0.10:5060"),
					},
				},
			}
			names := map[string]string{"10.0.0.9": "first", "10.0.0.10": "second"}
			elm, prx, tp := newAttemptTestProxy(t,
				attemptRoute("lazy", attemptPlan(
					[]proxy.Target{attemptURITarget("A", "10.0.0.9")},
				)),
				prxOpts, loc,
			)
			tp.sendReqHook = attemptRecSend(log, names)

			admitAttemptReq(ctx, t, elm, prx, newInbound(t, sip.MagicCookie+".lazy"))
			synctest.Wait()

			want := []string{"resolve:first", "send:first@UDP"}
			if diff := cmp.Diff(want, log.get()); diff != "" {
				t.Fatalf("attempt order (-want +got): %s", diff)
			}
			if got := lookups.Load(); got != 1 {
				t.Fatalf("lookups after successful send=%d, want 1", got)
			}

			// A final non-retryable response settles the target without
			// pulling the second candidate either.
			sends := drainReqs(tp)
			if got := len(sends); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}
			tx := attemptClnTx(t, elm, sends[0])
			if err := tx.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusNotFound)); err != nil {
				t.Fatalf("clnTx.RecvResponse(404) error = %v, want nil", err)
			}
			synctest.Wait()

			if got := lookups.Load(); got != 1 {
				t.Fatalf("lookups after final response=%d, want 1", got)
			}
			if diff := cmp.Diff(want, log.get()); diff != "" {
				t.Fatalf("attempt order after final response (-want +got): %s", diff)
			}
		})
	})

	t.Run("initial error and timeout advance through candidates", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			log := &attemptActionLog{}
			var lookups atomic.Int32
			loc := &attemptLocator{
				log:     log,
				lookups: &lookups,
				candidates: map[string][]attemptCandidate{
					"10.0.0.9": {
						udpAttemptCandidate("first", "10.0.0.9:5060"),
						udpAttemptCandidate("second", "10.0.0.10:5060"),
						udpAttemptCandidate("third", "10.0.0.11:5060"),
						udpAttemptCandidate("fourth", "10.0.0.12:5060"),
					},
				},
			}
			names := map[string]string{
				"10.0.0.9":  "first",
				"10.0.0.10": "second",
				"10.0.0.11": "third",
				"10.0.0.12": "fourth",
			}
			elm, prx, tp := newAttemptTestProxy(t,
				attemptRoute("retry", attemptPlan(
					[]proxy.Target{attemptURITarget("A", "10.0.0.9")},
				)),
				prxOpts, loc,
			)

			var sentMu sync.Mutex
			var sentReqs []*sip.RequestEnvelope
			tp.sendReqHook = func(call sendReqCall, _ int) error {
				if !call.req.Method().Equal(sip.RequestMethodInvite) {
					return nil
				}
				name := attemptSendNamer(call, names)
				log.add("send:" + name + "@UDP")
				sentMu.Lock()
				sentReqs = append(sentReqs, call.req)
				sentMu.Unlock()
				switch name {
				case "first":
					return &net.OpError{Op: "write", Net: "udp", Err: errors.ErrorWrap("refused")}
				case "second":
					// A context error surfacing from inside the transport
					// machinery stays retryable, matching the element's own
					// request-attempt classification.
					return context.DeadlineExceeded
				default:
					return nil
				}
			}

			admitAttemptReq(ctx, t, elm, prx, newInbound(t, sip.MagicCookie+".retry"))
			synctest.Wait()

			want := make([]string, 0, 8)
			want = append(want,
				"resolve:first", "send:first@UDP",
				"resolve:second", "send:second@UDP",
				"resolve:third", "send:third@UDP",
			)
			if diff := cmp.Diff(want, log.get()); diff != "" {
				t.Fatalf("attempt order (-want +got): %s", diff)
			}
			if got := lookups.Load(); got != 3 {
				t.Fatalf("lookups after retryable send errors=%d, want 3 (lazy)", got)
			}

			// A transaction timeout without any response advances again.
			sentMu.Lock()
			req3 := sentReqs[2]
			sentMu.Unlock()
			tx3 := attemptClnTx(t, elm, req3)
			if err := tx3.Terminate(ctx, sip.NewTransactionTimedOutError()); err != nil {
				t.Fatalf("clnTx.Terminate() error = %v, want nil", err)
			}
			synctest.Wait()

			want = append(want, "resolve:fourth", "send:fourth@UDP")
			if diff := cmp.Diff(want, log.get()); diff != "" {
				t.Fatalf("attempt order after timeout (-want +got): %s", diff)
			}
			if got := lookups.Load(); got != 4 {
				t.Fatalf("lookups after timeout=%d, want 4", got)
			}

			// Every retry got a unique proxy-namespace client branch.
			sentMu.Lock()
			branches := make([]string, 0, len(sentReqs))
			for _, req := range sentReqs {
				branches = append(branches, attemptTopBranch(t, req))
			}
			sentMu.Unlock()
			if got := len(branches); got != 4 {
				t.Fatalf("recorded branch sends=%d, want 4", got)
			}
			seen := make(map[string]struct{}, len(branches))
			for i, b := range branches {
				if !strings.HasPrefix(b, sip.MagicCookie+".proxy-stateful.") {
					t.Fatalf("branch[%d]=%q, want a proxy-stateful namespace branch", i, b)
				}
				if _, dup := seen[b]; dup {
					t.Fatalf("branch[%d]=%q repeats an earlier attempt branch", i, b)
				}
				seen[b] = struct{}{}
			}

			// A timeout after a provisional response does not advance.
			sentMu.Lock()
			req4 := sentReqs[3]
			sentMu.Unlock()
			tx4 := attemptClnTx(t, elm, req4)
			if err := tx4.RecvResponse(ctx, newInRes(t, req4, sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("clnTx.RecvResponse(180) error = %v, want nil", err)
			}
			if err := tx4.Terminate(ctx, sip.NewTransactionTimedOutError()); err != nil {
				t.Fatalf("clnTx.Terminate() error = %v, want nil", err)
			}
			synctest.Wait()
			if diff := cmp.Diff(want, log.get()); diff != "" {
				t.Fatalf("attempt order after proceeding timeout (-want +got): %s", diff)
			}
		})
	})

	t.Run("candidate-scoped finalize failure advances", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			log := &attemptActionLog{}
			var lookups atomic.Int32
			loc := &attemptLocator{
				log:     log,
				lookups: &lookups,
				candidates: map[string][]attemptCandidate{
					"10.0.0.9": {
						// SCTP has no tracked transport: finalizing this
						// candidate fails before any send and the runner
						// advances to the next candidate.
						{
							name: "first",
							addr: sip.ResolvedAddr{
								Transport: "SCTP",
								Addr:      netip.MustParseAddrPort("10.0.0.9:5060"),
							},
						},
						udpAttemptCandidate("second", "10.0.0.10:5060"),
					},
				},
			}
			names := map[string]string{"10.0.0.10": "second"}
			elm, prx, tp := newAttemptTestProxy(t,
				attemptRoute("no-tp", attemptPlan(
					[]proxy.Target{attemptURITarget("A", "10.0.0.9")},
				)),
				prxOpts, loc,
			)
			tp.sendReqHook = attemptRecSend(log, names)

			admitAttemptReq(ctx, t, elm, prx, newInbound(t, sip.MagicCookie+".no-tp"))
			synctest.Wait()

			want := []string{
				"resolve:first",
				"resolve:second", "send:second@UDP",
			}
			if diff := cmp.Diff(want, log.get()); diff != "" {
				t.Fatalf("attempt order (-want +got): %s", diff)
			}
			if got := lookups.Load(); got != 2 {
				t.Fatalf("lookups=%d, want 2", got)
			}
		})
	})

	t.Run("policy-scoped finalize failure aborts the runner", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			log := &attemptActionLog{}
			var lookups atomic.Int32
			loc := &attemptLocator{
				log:     log,
				lookups: &lookups,
				candidates: map[string][]attemptCandidate{
					"10.0.0.9": {
						udpAttemptCandidate("a-first", "10.0.0.9:5060"),
						udpAttemptCandidate("a-second", "10.0.0.10:5060"),
					},
					"10.0.0.30": {
						udpAttemptCandidate("c-first", "10.0.0.30:5060"),
					},
				},
			}
			names := map[string]string{
				"10.0.0.9":  "a-first",
				"10.0.0.10": "a-second",
				"10.0.0.30": "c-first",
			}
			// A SIPS next hop can never downgrade to an unsecured transport:
			// the failure is policy-scoped, so the runner aborts instead of
			// pulling the second candidate, and the next group proceeds.
			sipsTrgt := attemptURITarget("A", "10.0.0.9")
			sipsTrgt.URI.Secured = true
			elm, prx, tp := newAttemptTestProxy(t,
				attemptRoute("sips-abort", attemptPlan(
					[]proxy.Target{sipsTrgt},
					[]proxy.Target{attemptURITarget("C", "10.0.0.30")},
				)),
				prxOpts, loc,
			)
			tp.sendReqHook = attemptRecSend(log, names)

			admitAttemptReq(ctx, t, elm, prx, newInbound(t, sip.MagicCookie+".sips-abort"))
			synctest.Wait()

			want := []string{
				"resolve:a-first",
				"resolve:c-first", "send:c-first@UDP",
			}
			if diff := cmp.Diff(want, log.get()); diff != "" {
				t.Fatalf("attempt order (-want +got): %s", diff)
			}
			if got := lookups.Load(); got != 2 {
				t.Fatalf("lookups=%d, want 2 (aborted after the policy failure)", got)
			}
		})
	})

	t.Run("503 advances through candidates then the next target", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			log := &attemptActionLog{}
			var lookups atomic.Int32
			loc := &attemptLocator{
				log:     log,
				lookups: &lookups,
				candidates: map[string][]attemptCandidate{
					"10.0.0.9": {
						udpAttemptCandidate("a-first", "10.0.0.9:5060"),
						udpAttemptCandidate("a-second", "10.0.0.10:5060"),
					},
					"10.0.0.20": {
						udpAttemptCandidate("b-first", "10.0.0.20:5060"),
					},
				},
			}
			names := map[string]string{
				"10.0.0.9":  "a-first",
				"10.0.0.10": "a-second",
				"10.0.0.20": "b-first",
			}
			elm, prx, tp := newAttemptTestProxy(t,
				attemptRoute("failover", attemptPlan(
					[]proxy.Target{attemptURITarget("A", "10.0.0.9")},
					[]proxy.Target{attemptURITarget("B", "10.0.0.20")},
				)),
				prxOpts, loc,
			)
			tp.sendReqHook = attemptRecSend(log, names)

			admitAttemptReq(ctx, t, elm, prx, newInbound(t, sip.MagicCookie+".failover"))
			synctest.Wait()

			want := make([]string, 0, 6)
			want = append(want, "resolve:a-first", "send:a-first@UDP")
			if diff := cmp.Diff(want, log.get()); diff != "" {
				t.Fatalf("attempt order (-want +got): %s", diff)
			}

			sends := attemptInviteSends(drainReqs(tp))
			if got := len(sends); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}
			txA1 := attemptClnTx(t, elm, sends[0])
			if err := txA1.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusServiceUnavailable)); err != nil {
				t.Fatalf("clnTx.RecvResponse(503) error = %v, want nil", err)
			}
			synctest.Wait()

			want = append(want, "resolve:a-second", "send:a-second@UDP")
			if diff := cmp.Diff(want, log.get()); diff != "" {
				t.Fatalf("attempt order after first 503 (-want +got): %s", diff)
			}
			sends = attemptInviteSends(drainReqs(tp))
			if got := len(sends); got != 1 {
				t.Fatalf("outbound sends after first 503=%d, want 1", got)
			}

			txA2 := attemptClnTx(t, elm, sends[0])
			if err := txA2.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusServiceUnavailable)); err != nil {
				t.Fatalf("clnTx.RecvResponse(503) error = %v, want nil", err)
			}
			synctest.Wait()

			want = append(want, "resolve:b-first", "send:b-first@UDP")
			if diff := cmp.Diff(want, log.get()); diff != "" {
				t.Fatalf("attempt order after second 503 (-want +got): %s", diff)
			}
			if got := lookups.Load(); got != 3 {
				t.Fatalf("lookups=%d, want 3", got)
			}
		})
	})

	t.Run("size switch reuses branch and the discarded generation cannot settle", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			log := &attemptActionLog{}
			var lookups atomic.Int32
			loc := &attemptLocator{
				log:     log,
				lookups: &lookups,
				candidates: map[string][]attemptCandidate{
					"10.0.0.9": {
						udpAttemptCandidate("a-first", "10.0.0.9:5060"),
					},
					"10.0.0.30": {
						udpAttemptCandidate("c-first", "10.0.0.30:5060"),
					},
				},
			}
			names := map[string]string{
				"10.0.0.9":  "a-first",
				"10.0.0.30": "c-first",
			}
			elm, prx, tp := newAttemptTestProxy(t,
				attemptRoute("size-switch", attemptPlan(
					[]proxy.Target{attemptURITarget("A", "10.0.0.9")},
					[]proxy.Target{attemptURITarget("C", "10.0.0.30")},
				)),
				prxOpts, loc,
			)

			tcp := newStubTransp()
			tcp.md = sip.TCPMetadata()
			if err := elm.TrackTransport(tcp); err != nil {
				t.Fatalf("elm.TrackTransport(tcp) error = %v, want nil", err)
			}

			var sentMu sync.Mutex
			var udpSends, tcpSends []*sip.RequestEnvelope
			tcp.sendReqHook = func(call sendReqCall, _ int) error {
				if !call.req.Method().Equal(sip.RequestMethodInvite) {
					return nil
				}
				log.add("send:" + attemptSendNamer(call, names) + "@TCP")
				sentMu.Lock()
				tcpSends = append(tcpSends, call.req)
				sentMu.Unlock()
				return nil
			}

			var tooLarge atomic.Bool
			tooLarge.Store(true)
			tp.sendReqHook = func(call sendReqCall, _ int) error {
				if !call.req.Method().Equal(sip.RequestMethodInvite) {
					return nil
				}
				log.add("send:" + attemptSendNamer(call, names) + "@UDP")
				sentMu.Lock()
				udpSends = append(udpSends, call.req)
				sentMu.Unlock()
				if tooLarge.CompareAndSwap(true, false) {
					return sip.ErrMessageTooLarge
				}
				return nil
			}

			admitAttemptReq(ctx, t, elm, prx, newInbound(t, sip.MagicCookie+".size-switch"))
			synctest.Wait()

			want := make([]string, 0, 5)
			want = append(want,
				"resolve:a-first",
				"send:a-first@UDP",
				"send:a-first@TCP",
			)
			if diff := cmp.Diff(want, log.get()); diff != "" {
				t.Fatalf("attempt order (-want +got): %s", diff)
			}

			// The local size fallback keeps the same logical send: both
			// physical transactions share one proxy branch.
			if got := len(udpSends); got != 1 {
				t.Fatalf("udp sends=%d, want 1", got)
			}
			if got := len(tcpSends); got != 1 {
				t.Fatalf("tcp sends=%d, want 1", got)
			}
			udpBranch := attemptTopBranch(t, udpSends[0])
			tcpBranch := attemptTopBranch(t, tcpSends[0])
			if udpBranch != tcpBranch {
				t.Fatalf("size-switch branch=%q -> %q, want reused", udpBranch, tcpBranch)
			}
			if !strings.HasPrefix(tcpBranch, sip.MagicCookie+".proxy-stateful.") {
				t.Fatalf("size-switch branch=%q, want a proxy-stateful namespace branch", tcpBranch)
			}

			// The discarded UDP transaction's termination must not settle the
			// target: the next group stays gated on the live transaction.
			if got := len(log.get()); got != len(want) {
				t.Fatalf("actions after size switch=%d, want %d (no early next-group start)", got, len(want))
			}

			txTCP := attemptClnTx(t, elm, tcpSends[0])
			if err := txTCP.RecvResponse(ctx, newInRes(t, tcpSends[0], sip.ResponseStatusNotFound)); err != nil {
				t.Fatalf("clnTx.RecvResponse(404) error = %v, want nil", err)
			}
			synctest.Wait()

			want = append(want, "resolve:c-first", "send:c-first@UDP")
			if diff := cmp.Diff(want, log.get()); diff != "" {
				t.Fatalf("attempt order after A settled (-want +got): %s", diff)
			}
		})
	})

	t.Run("final 503 delivered inside the initial send advances after commit", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			log := &attemptActionLog{}
			var lookups atomic.Int32
			loc := &attemptLocator{
				log:     log,
				lookups: &lookups,
				candidates: map[string][]attemptCandidate{
					"10.0.0.9": {
						udpAttemptCandidate("first", "10.0.0.9:5060"),
						udpAttemptCandidate("second", "10.0.0.10:5060"),
					},
				},
			}
			names := map[string]string{"10.0.0.9": "first", "10.0.0.10": "second"}
			elm, prx, tp := newAttemptTestProxy(t,
				attemptRoute("in-send", attemptPlan(
					[]proxy.Target{attemptURITarget("A", "10.0.0.9")},
				)),
				prxOpts, loc,
			)

			tp.sendReqHook = func(call sendReqCall, _ int) error {
				if !call.req.Method().Equal(sip.RequestMethodInvite) {
					return nil
				}
				name := attemptSendNamer(call, names)
				log.add("send:" + name + "@UDP")
				if name != "first" {
					return nil
				}
				// Deliver the 503 while the outer send is still in flight.
				tx := attemptClnTx(t, elm, call.req)
				return tx.RecvResponse(call.ctx, newInRes(t, call.req, sip.ResponseStatusServiceUnavailable))
			}

			admitAttemptReq(ctx, t, elm, prx, newInbound(t, sip.MagicCookie+".in-send"))
			synctest.Wait()

			// The in-send final outcome is observed only once the outer send
			// result is committed: the second candidate starts afterwards.
			want := []string{
				"resolve:first", "send:first@UDP",
				"resolve:second", "send:second@UDP",
			}
			if diff := cmp.Diff(want, log.get()); diff != "" {
				t.Fatalf("attempt order (-want +got): %s", diff)
			}
			if got := lookups.Load(); got != 2 {
				t.Fatalf("lookups=%d, want 2", got)
			}
		})
	})

	t.Run("bound delivery failure performs no DNS or dial", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			log := &attemptActionLog{}
			var lookups atomic.Int32
			loc := &attemptLocator{
				log:     log,
				lookups: &lookups,
				candidates: map[string][]attemptCandidate{
					"10.0.0.9": {
						udpAttemptCandidate("a-first", "10.0.0.9:5060"),
					},
					"10.0.0.30": {
						udpAttemptCandidate("c-first", "10.0.0.30:5060"),
					},
				},
			}
			names := map[string]string{
				"10.0.0.9":  "a-first",
				"10.0.0.30": "c-first",
			}
			boundTrgt := attemptURITarget("A", "10.0.0.9")
			boundTrgt.Delivery = proxy.DeliveryRef{Kind: "flow", Key: "gone"}
			elm, prx, tp := newAttemptTestProxy(t,
				attemptRoute("bound-fail", attemptPlan(
					[]proxy.Target{boundTrgt},
					[]proxy.Target{attemptURITarget("C", "10.0.0.30")},
				)),
				prxOpts, loc,
			)
			tp.sendReqHook = attemptRecSend(log, names)

			admitAttemptReq(ctx, t, elm, prx, newInbound(t, sip.MagicCookie+".bound-fail"))
			synctest.Wait()

			// The unresolvable delivery ref fails the target explicitly: no
			// DNS resolution and no bound-transport send ever ran.
			want := []string{"resolve:c-first", "send:c-first@UDP"}
			if diff := cmp.Diff(want, log.get()); diff != "" {
				t.Fatalf("attempt order (-want +got): %s", diff)
			}
			if got := lookups.Load(); got != 1 {
				t.Fatalf("lookups=%d, want 1 (bound target never resolved)", got)
			}
		})
	})

	t.Run("bound delivery sends through the resolved transport", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			log := &attemptActionLog{}
			var lookups atomic.Int32
			loc := &attemptLocator{
				log:        log,
				lookups:    &lookups,
				candidates: make(map[string][]attemptCandidate),
			}

			boundLocal := netip.MustParseAddrPort("192.168.1.200:5060")
			boundRemote := netip.MustParseAddrPort("10.0.0.77:5060")

			var boundTp *stubTransp
			var resolved atomic.Int32
			opts := prxOpts
			opts.ResolveDelivery = func(_ context.Context, ref proxy.DeliveryRef) (proxy.BoundDelivery, error) {
				resolved.Add(1)
				if ref != (proxy.DeliveryRef{Kind: "flow", Key: "f-1"}) {
					return proxy.BoundDelivery{}, errors.ErrorWrap("unknown delivery ref")
				}
				return proxy.BoundDelivery{
					Transport:  boundTp,
					LocalAddr:  boundLocal,
					RemoteAddr: boundRemote,
				}, nil
			}

			boundTrgt := attemptURITarget("A", "10.0.0.9")
			boundTrgt.Delivery = proxy.DeliveryRef{Kind: "flow", Key: "f-1"}
			elm, prx, tp := newAttemptTestProxy(t,
				attemptRoute("bound-ok", attemptPlan(
					[]proxy.Target{boundTrgt},
				)),
				opts, loc,
			)
			boundTp = tp
			_ = elm

			tp.sendReqHook = func(call sendReqCall, _ int) error {
				if call.req.Method().Equal(sip.RequestMethodInvite) {
					log.add("send:bound")
				}
				return nil
			}

			admitAttemptReq(ctx, t, elm, prx, newInbound(t, sip.MagicCookie+".bound-ok"))
			synctest.Wait()

			want := []string{"send:bound"}
			if diff := cmp.Diff(want, log.get()); diff != "" {
				t.Fatalf("attempt order (-want +got): %s", diff)
			}
			if got := resolved.Load(); got != 1 {
				t.Fatalf("ResolveDelivery calls=%d, want 1", got)
			}
			if got := lookups.Load(); got != 0 {
				t.Fatalf("lookups=%d, want 0 (no DNS for bound delivery)", got)
			}

			sends := drainReqs(tp)
			if got := len(sends); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}
			if got := sends[0].RemoteAddr(); got != boundRemote {
				t.Fatalf("bound send remote addr=%v, want %v", got, boundRemote)
			}
			if got := sends[0].LocalAddr(); got != boundLocal {
				t.Fatalf("bound send local addr=%v, want %v", got, boundLocal)
			}
			if b := attemptTopBranch(t, sends[0]); !strings.HasPrefix(b, sip.MagicCookie+".proxy-stateful.") {
				t.Fatalf("bound send branch=%q, want a proxy-stateful namespace branch", b)
			}
		})
	})
}
