package proxy_test

import (
	"context"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/proxy"
)

func TestProxy_StatefulAdmission(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		laddr := netip.MustParseAddrPort("192.168.1.100:5060")
		raddr := netip.MustParseAddrPort("10.0.0.5:5060")

		var authorized, resolved atomic.Int32
		route := &proxy.Route{
			Name:        "test",
			ForwardMode: proxy.ForwardModeStateful,
			AuthorizeRequest: func(context.Context, *sip.RequestEnvelope) error {
				authorized.Add(1)
				return nil
			},
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				resolved.Add(1)
				return proxy.TargetPlan{Groups: [][]proxy.Target{
					{
						{
							ID: "A",
							URI: &sip.URI{
								User: sip.MakeUserInfo("alice"),
								Addr: sip.MakeHostPortAddr("10.0.0.9", 5060),
							},
						},
					},
				}}, nil
			},
		}

		elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
			ServedDomains: []string{"alice.voip.com"},
		})

		// A downstream response delivered inside sip.RequestAttempt.DoStateful —
		// before the outer send call returns — must still reach the request
		// coordinator: admission installs ownership and event subscriptions
		// before the transaction starts.
		var insideRecv atomic.Int32
		tp.sendReqHook = func(call sendReqCall, _ int) error {
			key, err := sip.MakeClientTransactionKey(call.req)
			if err != nil {
				return err
			}
			tx, ok := elm.LoadClientTransaction(key)
			if !ok {
				return errors.ErrorWrap("client transaction not registered at send")
			}
			res := newInRes(t, call.req, sip.ResponseStatusRinging)
			if err := tx.RecvResponse(call.ctx, res); err != nil {
				return err
			}
			insideRecv.Add(1)
			return nil
		}

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".proxy-admit", laddr, raddr)
		next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
			return nil
		})

		// Concurrent duplicate admissions collapse into a single owned request:
		// authorization and target resolution run exactly once.
		const dups = 4
		errs := make([]error, dups)
		var wg sync.WaitGroup
		for i := range dups {
			wg.Go(func() {
				dup, _ := req.Clone().(*sip.RequestEnvelope)
				errs[i] = prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), next, dup)
			})
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("prx.InterceptInboundRequest() [%d] error = %v, want nil", i, err)
			}
		}
		synctest.Wait()

		if got := authorized.Load(); got != 1 {
			t.Fatalf("authorizations=%d, want 1", got)
		}
		if got := resolved.Load(); got != 1 {
			t.Fatalf("resolutions=%d, want 1", got)
		}
		if got := insideRecv.Load(); got != 1 {
			t.Fatalf("in-send response deliveries=%d, want 1", got)
		}

		sends := drainReqs(tp)
		if got := len(sends); got != 1 {
			t.Fatalf("outbound request sends=%d, want 1", got)
		}
		outReq := sends[0]

		key, err := sip.MakeClientTransactionKey(outReq)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey(outReq) error = %v, want nil", err)
		}
		clnTx, ok := elm.LoadClientTransaction(key)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}

		// The in-send response was captured by the admission-time
		// subscription: a handler bound now observes no replayed event.
		var probe atomic.Int32
		clnTx.BindResponseHandler(sip.InboundResponseHandlerFunc(
			func(context.Context, *sip.ResponseEnvelope) { probe.Add(1) },
		))
		if got := probe.Load(); got != 0 {
			t.Fatalf("replayed in-send responses=%d, want 0 (captured at admission)", got)
		}

		// Reentrant transaction callbacks must not deadlock the coordinator.
		if err := clnTx.RecvResponse(ctx, newInRes(t, outReq, sip.ResponseStatusOK)); err != nil {
			t.Fatalf("clnTx.RecvResponse() error = %v, want nil", err)
		}
		synctest.Wait()
		if got := probe.Load(); got != 1 {
			t.Fatalf("live responses=%d, want 1", got)
		}

		// A transaction carrying only an application-controlled claim record is
		// not claimed: ownership is proven by the private admission token, not
		// by request metadata.
		frnReq := newInInviteReq(t, "UDP", sip.MagicCookie+".proxy-foreign", laddr, raddr)
		proxy.SetMetadata(frnReq.Metadata(), proxy.Metadata{
			RequestID: "foreign-claim",
			RouteName: route.Name,
			Mode:      proxy.ForwardModeStateful,
			Stage:     proxy.StageClaim,
		})
		frnTx, err := elm.NewServerTransaction(ctx, frnReq, tp)
		if err != nil {
			t.Fatalf("elm.NewServerTransaction() error = %v, want nil", err)
		}
		if err := frnTx.Start(ctx); err != nil {
			t.Fatalf("frnTx.Start() error = %v, want nil", err)
		}
		if err := frnTx.SendResponse(ctx, newInRes(t, frnReq, sip.ResponseStatusOK)); err != nil {
			t.Fatalf("frnTx.SendResponse() error = %v, want nil", err)
		}
		synctest.Wait()

		if got := len(drainReqs(tp)); got != 0 {
			t.Fatalf("outbound sends after unclaimed transaction=%d, want 0", got)
		}
		// The owned request's forwarded 200 and the foreign transaction's
		// own response may both appear; the transaction's automatic 100
		// Trying is tolerated but never a coordinator effect.
		var final200 int
		for _, res := range drainRess(tp) {
			if res.Status() == sip.ResponseStatusOK {
				final200++
			}
		}
		if final200 != 2 {
			t.Fatalf("final responses upstream=%d, want 2 (forwarded + foreign transaction)", final200)
		}
	})
}

func TestProxy_ForkScheduling(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	forkHosts := map[string]string{
		"A": "10.0.0.9",
		"B": "10.0.0.10",
		"C": "10.0.0.11",
	}
	forkPlan := func(groups ...[]string) proxy.TargetPlan {
		var plan proxy.TargetPlan
		for _, group := range groups {
			targets := make([]proxy.Target, 0, len(group))
			for _, id := range group {
				targets = append(targets, proxy.Target{
					ID: id,
					URI: &sip.URI{
						User: sip.MakeUserInfo("alice"),
						Addr: sip.MakeHostPortAddr(forkHosts[id], 5060),
					},
				})
			}
			plan.Groups = append(plan.Groups, targets)
		}
		return plan
	}
	// targetID maps an outbound branch copy back to its plan target via the
	// target URI host; unknown hosts return an empty ID.
	targetID := func(req *sip.RequestEnvelope) string {
		uri, ok := req.URI().(*sip.URI)
		if !ok {
			return ""
		}
		for id, host := range forkHosts {
			if uri.Addr.Host() == host {
				return id
			}
		}
		return ""
	}
	// inviteSends drops the ACK copies an INVITE client transaction emits on
	// a non-2xx final response, keeping only branch starts.
	inviteSends := func(reqs []*sip.RequestEnvelope) []*sip.RequestEnvelope {
		var out []*sip.RequestEnvelope
		for _, req := range reqs {
			if req.Method().Equal(sip.RequestMethodInvite) {
				out = append(out, req)
			}
		}
		return out
	}
	clnTxFor := func(tb testing.TB, elm *sip.Element, outReq *sip.RequestEnvelope) sip.ClientTransaction {
		tb.Helper()
		key, err := sip.MakeClientTransactionKey(outReq)
		if err != nil {
			tb.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		tx, ok := elm.LoadClientTransaction(key)
		if !ok {
			tb.Fatal("forwarding client transaction not registered")
		}
		return tx
	}
	intercept := func(tb testing.TB, ctx context.Context, elm *sip.Element, prx *proxy.Proxy, branch string) {
		tb.Helper()
		req := newInInviteReq(t, "UDP", branch, laddr, raddr)
		next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
			return nil
		})
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), next, req); err != nil {
			tb.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}
	}
	forkRoute := func(name string, plan proxy.TargetPlan, sel func(context.Context, proxy.Selection) ([]string, error)) *proxy.Route {
		return &proxy.Route{
			Name:        name,
			ForwardMode: proxy.ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return plan, nil
			},
			SelectTargets: sel,
		}
	}
	forkOpts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}

	t.Run("parallel group settles before the next group starts", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			elm, prx, tp := newConfiguredTestProxy(t, forkRoute(
				"fork-groups",
				forkPlan([]string{"A", "B"}, []string{"C"}),
				nil,
			), forkOpts)
			var cStarted atomic.Bool
			tp.sendReqHook = func(call sendReqCall, _ int) error {
				if targetID(call.req) == "C" {
					cStarted.Store(true)
				}
				return nil
			}

			intercept(t, ctx, elm, prx, sip.MagicCookie+".fork-groups")
			synctest.Wait()

			// Every member of the current group starts without waiting for a
			// sibling final response.
			sends := inviteSends(drainReqs(tp))
			if got := len(sends); got != 2 {
				t.Fatalf("initial outbound sends=%d, want 2", got)
			}
			started := []string{targetID(sends[0]), targetID(sends[1])}
			if diff := cmp.Diff([]string{"A", "B"}, started); diff != "" {
				t.Fatalf("targets (-want +got): %s", diff)
			}
			if cStarted.Load() {
				t.Fatal("next group started before continuation permitted")
			}

			// A provisional response settles nothing and opens no next group.
			txA := clnTxFor(t, elm, sends[0])
			if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("clnTx.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()
			if got := len(inviteSends(drainReqs(tp))); got != 0 {
				t.Fatalf("outbound sends after provisional=%d, want 0", got)
			}
			if cStarted.Load() {
				t.Fatal("next group started while both siblings stayed active")
			}

			// One final response leaves the sibling active: the next group is
			// still gated on the whole group settling.
			if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusNotFound)); err != nil {
				t.Fatalf("clnTx.RecvResponse(404) error = %v, want nil", err)
			}
			synctest.Wait()
			if cStarted.Load() {
				t.Fatal("next group started while a sibling branch stayed active")
			}

			// The last sibling's final response opens the next group.
			txB := clnTxFor(t, elm, sends[1])
			if err := txB.RecvResponse(ctx, newInRes(t, sends[1], sip.ResponseStatusBusyHere)); err != nil {
				t.Fatalf("clnTx.RecvResponse(486) error = %v, want nil", err)
			}
			synctest.Wait()

			nextSends := inviteSends(drainReqs(tp))
			if got := len(nextSends); got != 1 {
				t.Fatalf("next-group outbound sends=%d, want 1", got)
			}
			if got := targetID(nextSends[0]); got != "C" {
				t.Fatalf("next-group target=%q, want %q", got, "C")
			}
			if !cStarted.Load() {
				t.Fatal("next group never started after the group settled")
			}

			// A settled plan never restarts a completed target.
			txC := clnTxFor(t, elm, nextSends[0])
			if err := txC.RecvResponse(ctx, newInRes(t, nextSends[0], sip.ResponseStatusBusyHere)); err != nil {
				t.Fatalf("clnTx.RecvResponse(486) error = %v, want nil", err)
			}
			synctest.Wait()
			if got := len(inviteSends(drainReqs(tp))); got != 0 {
				t.Fatalf("outbound sends after plan settled=%d, want 0", got)
			}
		})
	})

	t.Run("success closes scheduling", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			elm, prx, tp := newConfiguredTestProxy(t, forkRoute(
				"fork-success",
				forkPlan([]string{"A"}, []string{"B"}),
				nil,
			), forkOpts)

			intercept(t, ctx, elm, prx, sip.MagicCookie+".fork-success")
			synctest.Wait()

			sends := inviteSends(drainReqs(tp))
			if got := len(sends); got != 1 {
				t.Fatalf("initial outbound sends=%d, want 1", got)
			}

			txA := clnTxFor(t, elm, sends[0])
			if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusOK)); err != nil {
				t.Fatalf("clnTx.RecvResponse(200) error = %v, want nil", err)
			}
			synctest.Wait()
			if got := len(inviteSends(drainReqs(tp))); got != 0 {
				t.Fatalf("outbound sends after success=%d, want 0 (scheduling closed)", got)
			}
		})
	})

	t.Run("received 6xx closes scheduling", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			elm, prx, tp := newConfiguredTestProxy(t, forkRoute(
				"fork-6xx",
				forkPlan([]string{"A"}, []string{"B"}),
				nil,
			), forkOpts)

			intercept(t, ctx, elm, prx, sip.MagicCookie+".fork-6xx")
			synctest.Wait()

			sends := inviteSends(drainReqs(tp))
			if got := len(sends); got != 1 {
				t.Fatalf("initial outbound sends=%d, want 1", got)
			}

			txA := clnTxFor(t, elm, sends[0])
			if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusBusyEverywhere)); err != nil {
				t.Fatalf("clnTx.RecvResponse(600) error = %v, want nil", err)
			}
			synctest.Wait()
			if got := len(inviteSends(drainReqs(tp))); got != 0 {
				t.Fatalf("outbound sends after 6xx=%d, want 0 (scheduling closed)", got)
			}
		})
	})

	t.Run("empty selection waits then declines", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			selCh := make(chan proxy.Selection, 8)
			elm, prx, tp := newConfiguredTestProxy(t, forkRoute(
				"fork-decline",
				forkPlan([]string{"A", "B"}, []string{"C"}),
				func(_ context.Context, sel proxy.Selection) ([]string, error) {
					selCh <- sel
					if len(sel.CompletedOutcomes) == 0 {
						return []string{"A", "B"}, nil
					}
					return nil, nil
				},
			), forkOpts)

			intercept(t, ctx, elm, prx, sip.MagicCookie+".fork-decline")
			synctest.Wait()

			sends := inviteSends(drainReqs(tp))
			if got := len(sends); got != 2 {
				t.Fatalf("initial outbound sends=%d, want 2", got)
			}

			// With one sibling settled and one still active, an empty
			// selection means wait: no new branch starts.
			txA := clnTxFor(t, elm, sends[0])
			if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusNotFound)); err != nil {
				t.Fatalf("clnTx.RecvResponse(404) error = %v, want nil", err)
			}
			synctest.Wait()
			if got := len(inviteSends(drainReqs(tp))); got != 0 {
				t.Fatalf("outbound sends while a sibling stayed active=%d, want 0", got)
			}

			// Once no branch remains active, a repeated empty selection
			// declines the remaining targets and completes scheduling.
			txB := clnTxFor(t, elm, sends[1])
			if err := txB.RecvResponse(ctx, newInRes(t, sends[1], sip.ResponseStatusBusyHere)); err != nil {
				t.Fatalf("clnTx.RecvResponse(486) error = %v, want nil", err)
			}
			synctest.Wait()
			if got := len(inviteSends(drainReqs(tp))); got != 0 {
				t.Fatalf("outbound sends after decline=%d, want 0", got)
			}

			var sels []proxy.Selection
			for len(selCh) > 0 {
				sels = append(sels, <-selCh)
			}
			if got := len(sels); got != 3 {
				t.Fatalf("selection hook calls=%d, want 3", got)
			}
			if diff := cmp.Diff([]string{"B"}, sels[1].ActiveTargetIDs); diff != "" {
				t.Fatalf("second selection active targets (-want +got): %s", diff)
			}
			if got := len(sels[2].ActiveTargetIDs); got != 0 {
				t.Fatalf("third selection active targets=%d, want 0", got)
			}
			if got := len(sels[2].RemainingGroups); got != 1 {
				t.Fatalf("third selection remaining groups=%d, want 1", got)
			}
		})
	})

	t.Run("invalid selection abandons the request", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			elm, prx, tp := newConfiguredTestProxy(t, forkRoute(
				"fork-invalid",
				forkPlan([]string{"A"}, []string{"B"}),
				func(context.Context, proxy.Selection) ([]string, error) {
					return []string{"ghost"}, nil
				},
			), forkOpts)

			intercept(t, ctx, elm, prx, sip.MagicCookie+".fork-invalid")
			synctest.Wait()
			if got := len(inviteSends(drainReqs(tp))); got != 0 {
				t.Fatalf("outbound sends after invalid selection=%d, want 0", got)
			}
		})
	})
}

func TestProxy_RestoreStart(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	inviteSends := func(reqs []*sip.RequestEnvelope) []*sip.RequestEnvelope {
		var out []*sip.RequestEnvelope
		for _, req := range reqs {
			if req.Method().Equal(sip.RequestMethodInvite) {
				out = append(out, req)
			}
		}
		return out
	}
	cancelSends := func(reqs []*sip.RequestEnvelope) []*sip.RequestEnvelope {
		var out []*sip.RequestEnvelope
		for _, req := range reqs {
			if req.Method().Equal(sip.RequestMethodCancel) {
				out = append(out, req)
			}
		}
		return out
	}

	t.Run("activation is once-only and replays no sends", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)
			elm, prx, tp := newConfiguredTestProxy(t, f.route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			bindings := f.dormantBindings(t, elm, tp)
			clnB := bindings.ClientTransactions[f.keyB]
			rq, err := prx.RestoreRequest(sip.ContextWithElement(ctx, elm), f.snap, bindings)
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}

			if err := rq.Start(ctx); err != nil {
				t.Fatalf("first rq.Start(): %v", err)
			}
			if err := rq.Start(ctx); !errors.Is(err, proxy.ErrRequestStarted) {
				t.Fatalf("second rq.Start() error = %v, want %v", err, proxy.ErrRequestStarted)
			}
			synctest.Wait()

			// Activation replays nothing: no initial send, no response, and
			// no new branch is emitted while target B stays live.
			if got := len(drainReqs(tp)); got != 0 {
				t.Fatalf("replayed initial sends=%d, want 0", got)
			}
			if got := len(drainRess(tp)); got != 0 {
				t.Fatalf("replayed responses=%d, want 0", got)
			}

			// The subscriptions were installed before the supplied
			// transactions started: a response on the restored attempt is
			// attributed to the coordinator.
			if err := clnB.RecvResponse(ctx, newInRes(t, clnB.Request(), sip.ResponseStatusBusyHere)); err != nil {
				t.Fatalf("clnB.RecvResponse(486) error = %v, want nil", err)
			}
			synctest.Wait()

			// Completed groups do not rerun and the empty selection declines
			// the unstarted target C: the committed 404 final crosses
			// upstream, and no new branch send happens.
			if got := len(inviteSends(drainReqs(tp))); got != 0 {
				t.Fatalf("new branch sends=%d, want 0 (completed group must not rerun)", got)
			}
			var final int
			for _, res := range drainRess(tp) {
				if res.Status() == sip.ResponseStatusNotFound {
					final++
				}
			}
			if final != 1 {
				t.Fatalf("forwarded finals upstream=%d, want 1 (committed 404)", final)
			}
		})
	})

	t.Run("overdue timer C applies the expiry policy immediately", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)
			snap := f.snap.Clone()
			// The preserved absolute deadline is already in the past: the
			// expiry policy applies at activation rather than after a fresh
			// Timer C interval.
			attemptOfTarget(snap, "B").TimerCDeadline = snap.Time.Add(-time.Second)

			elm, prx, tp := newConfiguredTestProxy(t, f.route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
			})
			rq, err := prx.RestoreRequest(sip.ContextWithElement(ctx, elm), snap, f.dormantBindings(t, elm, tp))
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}
			if err := rq.Start(ctx); err != nil {
				t.Fatalf("first rq.Start(): %v", err)
			}
			synctest.Wait()

			// The expired deadline settled target B as a timeout without a
			// fresh interval: no branch resend happened, and the committed
			// 404 final crossed upstream once every target settled.
			if got := len(inviteSends(drainReqs(tp))); got != 0 {
				t.Fatalf("new branch sends=%d, want 0", got)
			}
			var final int
			for _, res := range drainRess(tp) {
				if res.Status() == sip.ResponseStatusNotFound {
					final++
				}
			}
			if final != 1 {
				t.Fatalf("forwarded finals upstream=%d, want 1 (committed 404)", final)
			}
			rsnap, err := rq.Snapshot()
			if err != nil {
				t.Fatalf("rq.Snapshot() error = %v, want nil", err)
			}
			if !rsnap.Completed {
				t.Fatal("restored request did not complete after the expired deadline")
			}
			stB := targetStateOf(rsnap, "B")
			if stB == nil || !stB.Completed {
				t.Fatalf("target B=%+v, want settled by the expired deadline", stB)
			}
		})
	})

	t.Run("recorded cancellation resumes only the deferred work", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)
			snap := f.snap.Clone()
			snap.Cancelled = true
			attemptOfTarget(snap, "B").DeferredCancel = true

			elm, prx, tp := newConfiguredTestProxy(t, f.route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
			})
			bindings := f.dormantBindings(t, elm, tp)
			clnB := bindings.ClientTransactions[f.keyB]
			rq, err := prx.RestoreRequest(sip.ContextWithElement(ctx, elm), snap, bindings)
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}
			if err := rq.Start(ctx); err != nil {
				t.Fatalf("first rq.Start(): %v", err)
			}
			synctest.Wait()

			// The restored attempt is still Calling: its deferred CANCEL
			// stays deferred until a provisional response makes it legal.
			if got := len(drainReqs(tp)); got != 0 {
				t.Fatalf("sends before the deferred point=%d, want 0", got)
			}

			if err := clnB.RecvResponse(ctx, newInRes(t, clnB.Request(), sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("clnB.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()

			if got := len(cancelSends(drainReqs(tp))); got != 1 {
				t.Fatalf("deferred CANCEL sends=%d, want 1", got)
			}
			var prov int
			for _, res := range drainRess(tp) {
				if res.Status() == sip.ResponseStatusRinging {
					prov++
				}
			}
			if prov != 1 {
				t.Fatalf("forwarded provisionals upstream=%d, want 1 (180)", prov)
			}
		})
	})

	t.Run("initiated cancellation is not re-issued", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)
			snap := f.snap.Clone()
			snap.Cancelled = true
			attemptOfTarget(snap, "B").Cancelled = true

			elm, prx, tp := newConfiguredTestProxy(t, f.route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
			})
			bindings := f.dormantBindings(t, elm, tp)
			clnB := bindings.ClientTransactions[f.keyB]
			rq, err := prx.RestoreRequest(sip.ContextWithElement(ctx, elm), snap, bindings)
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}
			if err := rq.Start(ctx); err != nil {
				t.Fatalf("first rq.Start(): %v", err)
			}
			if err := clnB.RecvResponse(ctx, newInRes(t, clnB.Request(), sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("clnB.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()

			// The branch cancellation was already initiated at checkpoint
			// time: the provisional forwards upstream but no second CANCEL
			// is emitted.
			if got := len(cancelSends(drainReqs(tp))); got != 0 {
				t.Fatalf("re-issued CANCEL sends=%d, want 0", got)
			}
			var prov int
			for _, res := range drainRess(tp) {
				if res.Status() == sip.ResponseStatusRinging {
					prov++
				}
			}
			if prov != 1 {
				t.Fatalf("forwarded provisionals upstream=%d, want 1 (180)", prov)
			}
		})
	})

	t.Run("committed direction keeps scheduling closed", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)
			snap := f.snap.Clone()
			// A recorded global-failure candidate marks the committed
			// direction: scheduling stays closed after the restore.
			attA := attemptOfTarget(snap, "A")
			res600 := newInRes(t, attA.Request, sip.ResponseStatusBusyEverywhere)
			attA.Response = res600
			snap.Candidates = []*sip.ResponseEnvelope{res600}

			// A selector that would start target C if scheduling reopened:
			// the restored direction must keep it unreachable.
			route := *f.route
			route.SelectTargets = func(context.Context, proxy.Selection) ([]string, error) {
				return []string{"C"}, nil
			}
			elm, prx, tp := newConfiguredTestProxy(t, &route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
			})
			bindings := f.dormantBindings(t, elm, tp)
			clnB := bindings.ClientTransactions[f.keyB]
			rq, err := prx.RestoreRequest(sip.ContextWithElement(ctx, elm), snap, bindings)
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}
			if err := rq.Start(ctx); err != nil {
				t.Fatalf("first rq.Start(): %v", err)
			}
			if err := clnB.RecvResponse(ctx, newInRes(t, clnB.Request(), sip.ResponseStatusBusyHere)); err != nil {
				t.Fatalf("clnB.RecvResponse(486) error = %v, want nil", err)
			}
			synctest.Wait()

			if got := len(inviteSends(drainReqs(tp))); got != 0 {
				t.Fatalf("new branch sends=%d, want 0 (scheduling stayed closed)", got)
			}
			var final int
			for _, res := range drainRess(tp) {
				if res.Status() == sip.ResponseStatusBusyEverywhere {
					final++
				}
			}
			if final != 1 {
				t.Fatalf("forwarded finals upstream=%d, want 1 (committed 600)", final)
			}
		})
	})

	t.Run("terminal request stays inspect-only", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)
			snap := f.snap.Clone()
			snap.Completed = true
			snap.Outcome = proxy.OutcomeResponse
			snap.FinalStatus = sip.ResponseStatusNotFound
			index := 0
			snap.FinalCandidateIndex = &index
			snap.FinalForwarded = true
			stB := targetStateOf(snap, "B")
			stB.Completed = true
			stB.TimerCDeadline = time.Time{}
			stC := targetStateOf(snap, "C")
			stC.Completed = true
			stC.Declined = true
			attB := attemptOfTarget(snap, "B")
			attB.Completed = true
			attB.Response = newInRes(t, attB.Request, sip.ResponseStatusBusyHere)
			attB.TimerCDeadline = time.Time{}

			srvSnap := f.srvSnap.Clone()
			srvSnap.State = sip.TransactionStateTerminated
			srvSnap.Timer1xx = nil

			elm, prx, tp := newConfiguredTestProxy(t, f.route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
			})
			srvTx, err := sip.RestoreServerTransaction(srvSnap, tp)
			if err != nil {
				t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
			}
			rq, err := prx.RestoreRequest(sip.ContextWithElement(ctx, elm), snap, proxy.RestoreBindings{
				Element:           elm,
				ServerTransaction: srvTx,
			})
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}
			if err := rq.Start(ctx); err != nil {
				t.Fatalf("first rq.Start(): %v", err)
			}
			if err := rq.Start(ctx); !errors.Is(err, proxy.ErrRequestStarted) {
				t.Fatalf("second rq.Start() error = %v, want %v", err, proxy.ErrRequestStarted)
			}
			synctest.Wait()

			if got := len(drainReqs(tp)) + len(drainRess(tp)); got != 0 {
				t.Fatalf("sends after terminal activation=%d, want 0", got)
			}
			rsnap, err := rq.Snapshot()
			if err != nil {
				t.Fatalf("rq.Snapshot() error = %v, want nil", err)
			}
			if !rsnap.Completed || rsnap.Outcome != proxy.OutcomeResponse {
				t.Fatalf("restored terminal state=(completed=%t, %v), want (true, response)", rsnap.Completed, rsnap.Outcome)
			}
		})
	})

	t.Run("pending address retry resumes the deferred failover", func(t *testing.T) {
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
			route := attemptRoute("rest-retry", attemptPlan(
				[]proxy.Target{attemptURITarget("A", "10.0.0.9")},
			))
			opts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}
			elm1, prx1, tp1 := newAttemptTestProxy(t, route, opts, loc)
			tp1.sendReqHook = attemptRecSend(log, names)

			admitAttemptReq(ctx, t, elm1, prx1, newInInviteReq(t, "UDP", sip.MagicCookie+".rest-retry", laddr, raddr))
			synctest.Wait()
			if got := len(log.get()); got != 2 {
				t.Fatalf("actions before capture=%v, want [resolve:first send:first@UDP]", log.get())
			}

			srvKey, err := sip.MakeServerTransactionKey(newInInviteReq(t, "UDP", sip.MagicCookie+".rest-retry", laddr, raddr))
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			rq1, ok := prx1.LookupRequest(elm1, srvKey)
			if !ok {
				t.Fatal("prx1.LookupRequest(elm1, ) found no owned request")
			}
			snap, err := rq1.Snapshot()
			if err != nil {
				t.Fatalf("rq1.Snapshot() error = %v, want nil", err)
			}
			srvTx1, ok := elm1.LoadServerTransaction(srvKey)
			if !ok {
				t.Fatal("upstream transaction not registered")
			}
			srvSnapper, ok := srvTx1.(sip.ServerTransactionSnapshotter)
			if !ok {
				t.Fatal("upstream transaction does not implement sip.ServerTransactionSnapshotter")
			}
			srvSnap := mustSrvTxSnapshot(t, srvSnapper)

			// The capture is rewritten to the boundary the application
			// checkpointed: the first attempt produced a retryable 503 and
			// the runner owned the failover decision while no live attempt
			// was in flight.
			stA := targetStateOf(snap, "A")
			stA.PendingAddressRetry = true
			attA := attemptOfTarget(snap, "A")
			attA.Completed = true
			attA.State = sip.TransactionStateCompleted
			attA.Response = newInRes(t, attA.Request, sip.ResponseStatusServiceUnavailable)
			attA.TimerCDeadline = time.Time{}

			elm2, prx2, tp2 := newAttemptTestProxy(t, route, opts, loc)
			tp2.sendReqHook = attemptRecSend(log, names)
			srvTx2, err := sip.RestoreServerTransaction(srvSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
			}
			rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, proxy.RestoreBindings{
				Element:           elm2,
				ServerTransaction: srvTx2,
			})
			if err != nil {
				t.Fatalf("prx2.RestoreRequest() error = %v, want nil", err)
			}
			if err := rq2.Start(ctx); err != nil {
				t.Fatalf("first rq2.Start(): %v", err)
			}
			synctest.Wait()

			// The rebuilt runner skipped the already attempted candidate and
			// sent the failover attempt to the next one only.
			want := []string{
				"resolve:first", "send:first@UDP",
				"resolve:first", "resolve:second", "send:second@UDP",
			}
			if diff := cmp.Diff(want, log.get()); diff != "" {
				t.Fatalf("attempt order (-want +got): %s", diff)
			}
			sends := attemptInviteSends(drainReqs(tp2))
			if got := len(sends); got != 1 {
				t.Fatalf("outbound sends after activation=%d, want 1", got)
			}
			if got := attemptSendNamer(sendReqCall{req: sends[0]}, names); got != "second" {
				t.Fatalf("resumed attempt candidate=%q, want %q", got, "second")
			}

			// The preserved generation deadline bounds the resumed attempt.
			rsnap, err := rq2.Snapshot()
			if err != nil {
				t.Fatalf("rq2.Snapshot() error = %v, want nil", err)
			}
			if att := attemptOfTarget(rsnap, "A"); att == nil || att.TimerCDeadline.IsZero() {
				t.Fatal("resumed attempt lost the preserved generation deadline")
			}

			txA2 := attemptClnTx(t, elm2, sends[0])
			if err := txA2.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusBusyHere)); err != nil {
				t.Fatalf("clnTx.RecvResponse(486) error = %v, want nil", err)
			}
			synctest.Wait()
			var final int
			for _, res := range drainRess(tp2) {
				if res.Status() == sip.ResponseStatusBusyHere {
					final++
				}
			}
			if final != 1 {
				t.Fatalf("forwarded finals upstream=%d, want 1 (486)", final)
			}
		})
	})

	t.Run("newly eligible 2xx after activation still forwards", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)
			elm, prx, tp := newConfiguredTestProxy(t, f.route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			bindings := f.dormantBindings(t, elm, tp)
			clnB := bindings.ClientTransactions[f.keyB]
			rq, err := prx.RestoreRequest(sip.ContextWithElement(ctx, elm), f.snap, bindings)
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}
			if err := rq.Start(ctx); err != nil {
				t.Fatalf("first rq.Start(): %v", err)
			}
			if err := clnB.RecvResponse(ctx, newInRes(t, clnB.Request(), sip.ResponseStatusOK)); err != nil {
				t.Fatalf("clnB.RecvResponse(200) error = %v, want nil", err)
			}
			synctest.Wait()

			if got := len(inviteSends(drainReqs(tp))); got != 0 {
				t.Fatalf("new branch sends=%d, want 0", got)
			}
			var okRes int
			for _, res := range drainRess(tp) {
				if res.Status() == sip.ResponseStatusOK {
					okRes++
				}
			}
			if okRes != 1 {
				t.Fatalf("forwarded successes upstream=%d, want 1", okRes)
			}
		})
	})
}
