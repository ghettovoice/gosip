package proxy_test

import (
	"context"
	"log/slog"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/proxy"
)

// resCSeqMethod returns the CSeq method carried by res, used to attribute a
// sent response to its request context.
func resCSeqMethod(tb testing.TB, res *sip.ResponseEnvelope) sip.RequestMethod {
	tb.Helper()

	var mtd sip.RequestMethod
	res.WithMessage(func(r *sip.Response) {
		if cseq, ok := r.Headers.CSeq(); ok {
			mtd = cseq.Method
		}
	})
	return mtd
}

// resStatuses returns the statuses of the sent responses that belong to the
// request context of mtd, in arrival order.
func resStatuses(
	tb testing.TB,
	ress []*sip.ResponseEnvelope,
	mtd sip.RequestMethod,
) []int {
	tb.Helper()

	var out []int
	for _, res := range ress {
		if resCSeqMethod(tb, res).Equal(mtd) {
			out = append(out, int(res.Status()))
		}
	}
	return out
}

// countReqMethod returns the number of sent requests carrying mtd.
func countReqMethod(reqs []*sip.RequestEnvelope, mtd sip.RequestMethod) int {
	var cnt int
	for _, req := range reqs {
		if req != nil && req.Method().Equal(mtd) {
			cnt++
		}
	}
	return cnt
}

// waitCancelSend waits for the next sent request to carry the CANCEL
// method, skipping branch ACKs a settled INVITE transaction emits on a
// non-2xx final response.
func waitCancelSend(tb testing.TB, tp *stubTransp) sendReqCall {
	tb.Helper()

	for {
		call := testutil.WaitResult(tb, tp.sendReqCalls, "downstream CANCEL")
		if call.req != nil && call.req.Method().Equal(sip.RequestMethodCancel) {
			return call
		}
	}
}

// cancelClnTxKey returns the client transaction key a downstream CANCEL
// built from the outbound branch copy outReq would take.
func cancelClnTxKey(tb testing.TB, outReq *sip.RequestEnvelope) sip.ClientTransactionKey {
	tb.Helper()

	cnc, err := sip.NewCancelRequestEnvelope(outReq)
	if err != nil {
		tb.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
	}
	key, err := sip.MakeClientTransactionKey(cnc)
	if err != nil {
		tb.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
	}
	return key
}

// cancelFromTag reports whether the From header of req carries a tag.
func cancelFromTag(tb testing.TB, req *sip.RequestEnvelope) (tag string, ok bool) {
	tb.Helper()

	req.WithMessage(func(r *sip.Request) {
		if from, found := r.Headers.From(); found {
			tag, ok = from.Tag()
		}
	})
	return tag, ok
}

func TestProxy_FinalResponseResolver_BypassCancel(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		route := &proxy.Route{
			Name:        "resolver-cancel-no-finals",
			ForwardMode: proxy.ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{
					{statelessLitTarget("A", "10.0.0.9")},
					{statelessLitTarget("B", "10.0.0.10")},
				}}, nil
			},
			ResolveFinalResponse: func(context.Context, proxy.FinalResponseSelection) (int, error) {
				calls.Add(1)
				return -1, errors.ErrorWrap("unexpected cancellation resolver invocation")
			},
		}
		elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{ServedDomains: []string{"alice.voip.com"}})
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".resolver-cancel-no-finals",
			netip.MustParseAddrPort("192.168.1.100:5060"), netip.MustParseAddrPort("10.0.0.5:5060"))
		fwdIntercept(t, elm, prx, req)
		synctest.Wait()
		key, err := sip.MakeServerTransactionKey(req)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		rq, ok := prx.LookupRequest(elm, key)
		if !ok {
			t.Fatal("proxy.LookupRequest() missing admitted INVITE")
		}
		sends := drainReqs(tp)
		if len(sends) != 1 {
			t.Fatalf("proxy initial sends = %d, want 1", len(sends))
		}
		tx := fwdClnTx(t, elm, sends[0])
		cancel, err := sip.NewCancelRequestEnvelope(req)
		if err != nil {
			t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
		}
		fwdIntercept(t, elm, prx, cancel)
		synctest.Wait()
		responses := drainRess(tp)
		if diff := cmp.Diff([]int{200}, resStatuses(t, responses, sip.RequestMethodCancel)); diff != "" {
			t.Errorf("proxy CANCEL wire statuses (-want +got):\n%s", diff)
		}
		if got := len(resStatuses(t, responses, sip.RequestMethodInvite)); got != 0 {
			t.Errorf("proxy INVITE responses before branch exhaustion = %d, want 0", got)
		}
		if got := len(drainReqs(tp)); got != 0 {
			t.Errorf("proxy sends before provisional = %d, want 0 (CANCEL deferred, scheduling closed)", got)
		}

		// Termination, not a downstream 487, leaves no candidate for the resolver.
		if err := tx.Terminate(t.Context(), sip.NewTransactionTimedOutError()); err != nil {
			t.Fatalf("clnTx.Terminate() error = %v, want nil", err)
		}
		synctest.Wait()
		responses = drainRess(tp)
		if diff := cmp.Diff([]int{487}, resStatuses(t, responses, sip.RequestMethodInvite)); diff != "" {
			t.Errorf("proxy INVITE wire statuses (-want +got):\n%s", diff)
		}
		if got := len(responses); got != 1 {
			t.Errorf("proxy generated responses = %d, want only the INVITE 487", got)
		}
		if got := len(drainReqs(tp)); got != 0 {
			t.Errorf("proxy late sends = %d, want 0 (no retry or backup target)", got)
		}
		snap, err := rq.Snapshot()
		if err != nil {
			t.Fatalf("proxy.Request.Snapshot() error = %v, want nil", err)
		}
		if !snap.Completed || !snap.Cancelled || snap.Outcome != proxy.OutcomeCancelled || !snap.FinalForwarded {
			t.Errorf("proxy cancelled completed/cancelled/outcome/forwarded = %t/%t/%v/%t, want true/true/cancelled/true",
				snap.Completed, snap.Cancelled, snap.Outcome, snap.FinalForwarded)
		}
		if got := len(snap.Candidates); got != 0 {
			t.Errorf("proxy cancelled candidates = %d, want 0", got)
		}
		if got := calls.Load(); got != 0 {
			t.Errorf("route.ResolveFinalResponse calls = %d, want 0", got)
		}
	})
}

func TestProxy_Cancel(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	served := []string{"alice.voip.com"}
	silentNext := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
		return nil
	})

	// intercept feeds req through the proxy request pipeline.
	intercept := func(tb testing.TB, ctx context.Context, elm *sip.Element, prx *proxy.Proxy, req *sip.RequestEnvelope) {
		tb.Helper()

		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, req); err != nil {
			tb.Fatalf("prx.InterceptInboundRequest(%v) error = %v, want nil", req.Method(), err)
		}
	}

	// branchTx returns the downstream client transaction of one sent
	// branch copy.
	branchTx := func(tb testing.TB, elm *sip.Element, outReq *sip.RequestEnvelope) sip.ClientTransaction {
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

	t.Run("matched cancel acknowledged and branches cancelled", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			var cancelSendCount, authorized atomic.Int32
			route := &proxy.Route{
				Name:        "cancel-matched",
				ForwardMode: proxy.ForwardModeStateful,
				ServerTransactionOptions: sip.ServerTransactionOptions{
					NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320,
				},
				AuthorizeRequest: func(context.Context, *sip.RequestEnvelope) error {
					authorized.Add(1)
					return nil
				},
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return proxy.TargetPlan{Groups: [][]proxy.Target{
						{statelessLitTarget("A", "10.0.0.9")},
						{statelessLitTarget("B", "10.0.0.10")},
					}}, nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{ServedDomains: served})
			tp.sendReqHook = func(call sendReqCall, _ int) error {
				if call.req != nil && call.req.Method().Equal(sip.RequestMethodCancel) {
					cancelSendCount.Add(1)
				}
				return nil
			}

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".cancel-matched", laddr, raddr)
			intercept(t, ctx, elm, prx, req)
			synctest.Wait()

			sends := drainReqs(tp)
			if got := len(sends); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}
			txA := branchTx(t, elm, sends[0])

			cancel, err := sip.NewCancelRequestEnvelope(req)
			if err != nil {
				t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
			}
			intercept(t, ctx, elm, prx, cancel)
			synctest.Wait()

			// The matching CANCEL is acknowledged on its own server
			// transaction; the original request context is never answered
			// for the CANCEL.
			cancelResponses := resStatuses(t, drainRess(tp), sip.RequestMethodCancel)
			if diff := cmp.Diff([]int{200}, cancelResponses); diff != "" {
				t.Fatalf("cancel responses (-want +got): %s", diff)
			}
			if got := authorized.Load(); got != 1 {
				t.Fatalf("authorizations=%d, want 1 (matching CANCEL is never re-authorized)", got)
			}

			cncKey, err := sip.MakeServerTransactionKey(cancel)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey(cancel) error = %v, want nil", err)
			}
			cncTx, ok := elm.LoadServerTransaction(cncKey)
			if !ok {
				t.Fatal("cancel server transaction not registered")
			}
			if got := cncTx.Type(); got != sip.TransactionTypeServerNonInvite {
				t.Fatalf("cancel transaction type = %v, want %v", got, sip.TransactionTypeServerNonInvite)
			}
			provider, ok := cncTx.(sip.NonInviteServerTransactionBehaviorProvider)
			if !ok {
				t.Fatal("cancel transaction does not implement sip.NonInviteServerTransactionBehaviorProvider")
			}
			if got := provider.NonInviteServerTransactionBehavior(); got != sip.NonInviteServerTransactionBehaviorRFC4320 {
				t.Fatalf("cancel transaction behavior = %v, want %v (route options pass through)",
					got,
					sip.NonInviteServerTransactionBehaviorRFC4320,
				)
			}

			// The branch saw no provisional yet: the downstream CANCEL is
			// deferred, and no new target may start once scheduling closed.
			if got := cancelSendCount.Load(); got != 0 {
				t.Fatalf("cancelled before provisional, sends=%d, want 0", got)
			}
			if got := len(drainReqs(tp)); got != 0 {
				t.Fatalf("outbound sends after cancel=%d, want 0 (scheduling closed)", got)
			}

			if err := txA.RecvResponse(t.Context(), newInRes(t, sends[0], sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("txA.RecvResponse: %v", err)
			}
			call := waitCancelSend(t, tp)
			if !call.req.Method().Equal(sip.RequestMethodCancel) {
				t.Fatalf("downstream cancel method = %v, want CANCEL", call.req.Method())
			}
			if got := cancelSendCount.Load(); got != 1 {
				t.Fatalf("cancelled after provisional, sends=%d, want 1", got)
			}
			synctest.Wait()

			// The downstream UAS answers the cancelled branch with 487: it
			// is the committed final forwarded upstream on the original
			// transaction.
			if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusRequestTerminated)); err != nil {
				t.Fatalf("txA.RecvResponse(487): %v", err)
			}
			synctest.Wait()

			upstreamFinals := resStatuses(t, drainRess(tp), sip.RequestMethodInvite)
			if diff := cmp.Diff([]int{180, 487}, upstreamFinals); diff != "" {
				t.Fatalf("upstream responses (-want +got): %s", diff)
			}
			// The second target never started; the only remaining sends are
			// the branch ACKs emitted for the non-2xx final.
			if got := countReqMethod(drainReqs(tp), sip.RequestMethodInvite); got != 0 {
				t.Fatalf("late INVITE sends=%d, want 0", got)
			}
		})
	})

	t.Run("final-first race never cancels", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			var cancelSendCount atomic.Int32
			route := &proxy.Route{
				Name:        "cancel-final",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return proxy.TargetPlan{Groups: [][]proxy.Target{{
						statelessLitTarget("A", "10.0.0.9"),
						statelessLitTarget("B", "10.0.0.10"),
					}}}, nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{ServedDomains: served})
			tp.sendReqHook = func(call sendReqCall, _ int) error {
				if call.req != nil && call.req.Method().Equal(sip.RequestMethodCancel) {
					cancelSendCount.Add(1)
				}
				return nil
			}

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".cancel-final", laddr, raddr)
			intercept(t, ctx, elm, prx, req)
			synctest.Wait()

			sends := drainReqs(tp)
			if got := len(sends); got != 2 {
				t.Fatalf("outbound sends=%d, want 2", got)
			}
			txA := branchTx(t, elm, sends[0])
			txB := branchTx(t, elm, sends[1])

			// Branch A settles on a final response while B is still calling.
			if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusNotFound)); err != nil {
				t.Fatalf("txA.RecvResponse(404): %v", err)
			}
			synctest.Wait()

			cancel, err := sip.NewCancelRequestEnvelope(req)
			if err != nil {
				t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
			}
			intercept(t, ctx, elm, prx, cancel)
			synctest.Wait()

			cancelResponses := resStatuses(t, drainRess(tp), sip.RequestMethodCancel)
			if diff := cmp.Diff([]int{200}, cancelResponses); diff != "" {
				t.Fatalf("cancel responses (-want +got): %s", diff)
			}
			// The settled branch A is never cancelled; B is still calling
			// and defers its CANCEL to the first provisional.
			if got := cancelSendCount.Load(); got != 0 {
				t.Fatalf("cancel sends=%d, want 0 (settled branch is never cancelled)", got)
			}

			if err := txB.RecvResponse(ctx, newInRes(t, sends[1], sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("txB.RecvResponse(180): %v", err)
			}
			waitCancelSend(t, tp)
			if got := cancelSendCount.Load(); got != 1 {
				t.Fatalf("cancel sends=%d, want 1 (only the live branch)", got)
			}

			// Exactly one downstream CANCEL transaction exists; the settled
			// branch key never produced one.
			keyA := cancelClnTxKey(t, sends[0])
			if _, ok := elm.LoadClientTransaction(keyA); ok {
				t.Fatal("settled branch produced a CANCEL transaction")
			}
			keyB := cancelClnTxKey(t, sends[1])
			if _, ok := elm.LoadClientTransaction(keyB); !ok {
				t.Fatal("live branch has no CANCEL transaction")
			}
			synctest.Wait()
		})
	})

	t.Run("duplicate cancel absorbed by its own transaction", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			var cancelSendCount atomic.Int32
			route := &proxy.Route{
				Name:        "cancel-dup",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return proxy.TargetPlan{Groups: [][]proxy.Target{{
						statelessLitTarget("A", "10.0.0.9"),
					}}}, nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{ServedDomains: served})
			tp.sendReqHook = func(call sendReqCall, _ int) error {
				if call.req != nil && call.req.Method().Equal(sip.RequestMethodCancel) {
					cancelSendCount.Add(1)
				}
				return nil
			}

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".cancel-dup", laddr, raddr)
			intercept(t, ctx, elm, prx, req)
			synctest.Wait()
			drainReqs(tp)

			cancel, err := sip.NewCancelRequestEnvelope(req)
			if err != nil {
				t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
			}
			cncKey, err := sip.MakeServerTransactionKey(cancel)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey(cancel) error = %v, want nil", err)
			}

			intercept(t, ctx, elm, prx, cancel)
			synctest.Wait()
			cncTx, ok := elm.LoadServerTransaction(cncKey)
			if !ok {
				t.Fatal("cancel server transaction not registered")
			}

			// A retransmitted CANCEL maps to the same transaction and is
			// absorbed: no second transaction, no second cancellation pass.
			intercept(t, ctx, elm, prx, cancel.Clone().(*sip.RequestEnvelope))
			synctest.Wait()
			again, ok := elm.LoadServerTransaction(cncKey)
			if !ok || again != cncTx {
				t.Fatalf("cancel transaction after duplicate = (%v, %t), want the same %v", again, ok, cncTx)
			}
			if got := cancelSendCount.Load(); got != 0 {
				t.Fatalf("downstream cancel sends=%d, want 0 (duplicate absorbed)", got)
			}
		})
	})

	t.Run("unmatched cancel forwards statelessly", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		route := &proxy.Route{
			Name:        "cancel-unmatched",
			ForwardMode: proxy.ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{{
					statelessLitTarget("A", "10.0.0.9"),
				}}}, nil
			},
		}
		elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{ServedDomains: served})

		// A CANCEL for an INVITE the proxy never admitted takes the
		// Section 16.10 deterministic stateless path: forwarded like any
		// other request, never answered 481, never challenged, and never
		// given a transaction or response context.
		unseen := newInInviteReq(t, "UDP", sip.MagicCookie+".cancel-unseen", laddr, raddr)
		cancel, err := sip.NewCancelRequestEnvelope(unseen)
		if err != nil {
			t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
		}
		intercept(t, ctx, elm, prx, cancel)

		sends := drainReqs(tp)
		if got := len(sends); got != 1 {
			t.Fatalf("stateless sends=%d, want 1", got)
		}
		if !sends[0].Method().Equal(sip.RequestMethodCancel) {
			t.Fatalf("sent method = %v, want CANCEL", sends[0].Method())
		}
		if branch := statelessTopBranch(t, sends[0]); !strings.HasPrefix(branch, sip.MagicCookie+".proxy-stateless.") {
			t.Fatalf("top via branch = %q, want proxy-stateless mark", branch)
		}
		if got := countAllTxs(elm); got != 0 {
			t.Fatalf("transactions=%d, want 0 (no response context)", got)
		}
		if got := len(drainRess(tp)); got != 0 {
			t.Fatalf("responses=%d, want 0 (never a 481)", got)
		}
	})

	t.Run("unmatched cancel never challenged", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()

		var authorized atomic.Int32
		route := &proxy.Route{
			Name:        "cancel-unchallenged",
			ForwardMode: proxy.ForwardModeStateful,
			AuthorizeRequest: func(context.Context, *sip.RequestEnvelope) error {
				authorized.Add(1)
				return errors.Wrap(sip.NewRequestRejectedError(
					errors.ErrorWrap("unauthorized"),
					slog.LevelWarn,
					sip.ResponseStatusProxyAuthenticationRequired,
				))
			},
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{{
					statelessLitTarget("A", "10.0.0.9"),
				}}}, nil
			},
		}
		elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{ServedDomains: served})

		unseen := newInInviteReq(t, "UDP", sip.MagicCookie+".cancel-unchal", laddr, raddr)
		cancel, err := sip.NewCancelRequestEnvelope(unseen)
		if err != nil {
			t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
		}
		intercept(t, ctx, elm, prx, cancel)

		if got := authorized.Load(); got != 1 {
			t.Fatalf("authorizations=%d, want 1", got)
		}
		if got := len(drainRess(tp)); got != 0 {
			t.Fatalf("responses=%d, want 0 (a CANCEL is never challenged)", got)
		}
		if got := len(drainReqs(tp)); got != 0 {
			t.Fatalf("sends=%d, want 0", got)
		}
		if got := countAllTxs(elm); got != 0 {
			t.Fatalf("transactions=%d, want 0", got)
		}
	})

	t.Run("tagless from invite cancelled downstream", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			var cancelSendCount atomic.Int32
			route := &proxy.Route{
				Name:        "cancel-tagless",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return proxy.TargetPlan{Groups: [][]proxy.Target{{
						statelessLitTarget("A", "10.0.0.9"),
					}}}, nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{ServedDomains: served})
			tp.sendReqHook = func(call sendReqCall, _ int) error {
				if call.req != nil && call.req.Method().Equal(sip.RequestMethodCancel) {
					cancelSendCount.Add(1)
				}
				return nil
			}

			// RFC 9.1: the CANCEL shares the INVITE's From header field,
			// including a missing tag.
			req := newInInviteReq(t, "UDP", sip.MagicCookie+".cancel-tagless", laddr, raddr)
			req.WithMessage(func(r *sip.Request) {
				if from, ok := r.Headers.From(); ok {
					from.Params.Delete("tag")
				}
			})
			intercept(t, ctx, elm, prx, req)
			synctest.Wait()

			sends := drainReqs(tp)
			if got := len(sends); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}
			if tag, ok := cancelFromTag(t, sends[0]); ok {
				t.Fatalf("forwarded INVITE From tag = %q, want tagless", tag)
			}
			txA := branchTx(t, elm, sends[0])

			cancel, err := sip.NewCancelRequestEnvelope(req)
			if err != nil {
				t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
			}
			intercept(t, ctx, elm, prx, cancel)
			synctest.Wait()

			if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("txA.RecvResponse(180): %v", err)
			}
			call := waitCancelSend(t, tp)
			if got := cancelSendCount.Load(); got != 1 {
				t.Fatalf("cancel sends=%d, want 1 (tagless From builds fine)", got)
			}
			if tag, ok := cancelFromTag(t, call.req); ok {
				t.Fatalf("downstream CANCEL From tag = %q, want tagless (shared with INVITE)", tag)
			}
			synctest.Wait()
		})
	})
}
