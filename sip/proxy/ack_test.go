package proxy_test

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/proxy"
)

// countAckTxs reports how many admitted transactions — upstream
// or downstream — were created for an ACK request. An ACK never gains a
// transaction of its own on either side of the proxy.
func countAckTxs(elm *sip.Element) int {
	var n int
	for tx := range elm.AllServerTransactions() {
		if req := tx.Request(); req != nil && req.Method().Equal(sip.RequestMethodAck) {
			n++
		}
	}
	for tx := range elm.AllClientTransactions() {
		if req := tx.Request(); req != nil && req.Method().Equal(sip.RequestMethodAck) {
			n++
		}
	}
	return n
}

// admitInviteAndSettle intercepts one INVITE to a single-target stateful
// route, waits for the branch send, and injects the downstream final
// response that the coordinator forwards upstream. It returns the
// upstream server transaction.
func admitInviteAndSettle(
	tb testing.TB,
	prx *proxy.Proxy,
	elm *sip.Element,
	tp *stubTransp,
	next sip.RequestReceiver,
	invite *sip.RequestEnvelope,
	sts sip.ResponseStatus,
) sip.ServerTransaction {
	tb.Helper()

	ctx := tb.Context()
	if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), next, invite); err != nil {
		tb.Fatalf("prx.InterceptInboundRequest(INVITE) error = %v, want nil", err)
	}
	synctest.Wait()

	outReq := testutil.WaitResult(tb, tp.sendReqCalls, "outbound INVITE").req
	clnKey, err := sip.MakeClientTransactionKey(outReq)
	if err != nil {
		tb.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
	}
	clnTx, ok := elm.LoadClientTransaction(clnKey)
	if !ok {
		tb.Fatal("forwarding client transaction not registered")
	}

	invKey, err := sip.MakeServerTransactionKey(invite)
	if err != nil {
		tb.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
	}
	srvTx, ok := elm.LoadServerTransaction(invKey)
	if !ok {
		tb.Fatal("upstream server transaction not registered")
	}

	if err := clnTx.RecvResponse(ctx, newInRes(tb, outReq, sts)); err != nil {
		tb.Fatalf("clnTx.RecvResponse(%v) error = %v, want nil", sts, err)
	}
	synctest.Wait()

	return srvTx
}

// TestProxy_ACK pins the ACK dispatch: a non-2xx ACK is absorbed by the
// matching INVITE server transaction, an end-to-end ACK routes through
// the prepared-target path without a transaction or a provisional
// generation, and an unroutable ACK fails with an error and never a SIP
// response.
func TestProxy_ACK(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	served := []string{"alice.voip.com"}
	silentNext := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
		return nil
	})
	ackRoute := func(name string) *proxy.Route {
		return &proxy.Route{
			Name:        name,
			ForwardMode: proxy.ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{{
					statelessLitTarget("A", "10.0.0.9"),
				}}}, nil
			},
		}
	}

	t.Run("non-2xx ack absorbed by the invite transaction", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			elm, prx, tp := newConfiguredTestProxy(t, ackRoute("ack-absorbed"), proxy.Options{ServedDomains: served})

			invite := newInInviteReq(t, "UDP", sip.MagicCookie+".ack-absorbed", laddr, raddr)
			srvTx := admitInviteAndSettle(t, prx, elm, tp, silentNext, invite, sip.ResponseStatusNotFound)

			if got := srvTx.State(); got != sip.TransactionStateCompleted {
				t.Fatalf("invite transaction state = %v, want %v", got, sip.TransactionStateCompleted)
			}
			drainReqs(tp)
			drainRess(tp)

			// The ACK shares the INVITE's top Via branch and CSeq number —
			// the server transaction identity — so it is hop-by-hop: the
			// matching transaction absorbs it, confirmed synchronously.
			ack := newInAckReq(t, invite, newInRes(t, invite, sip.ResponseStatusNotFound))
			if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, ack); err != nil {
				t.Fatalf("prx.InterceptInboundRequest(ACK) error = %v, want nil", err)
			}
			if got := srvTx.State(); got != sip.TransactionStateConfirmed {
				t.Fatalf("invite transaction state = %v, want %v", got, sip.TransactionStateConfirmed)
			}
			if got := countAckTxs(elm); got != 0 {
				t.Fatalf("ACK transactions=%d, want 0", got)
			}
			if len(tp.sendResCalls) != 0 {
				t.Fatal("ACK received a SIP response")
			}
			if got := len(tp.sendReqCalls); got != 0 {
				t.Fatalf("hop-by-hop ACK forwarded downstream, sends=%d, want 0", got)
			}
		})
	})

	t.Run("end-to-end ack routed without a transaction", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			elm, prx, tp := newConfiguredTestProxy(t, ackRoute("ack-e2e"), proxy.Options{ServedDomains: served})

			invite := newInInviteReq(t, "UDP", sip.MagicCookie+".ack-e2e", laddr, raddr)
			admitInviteAndSettle(t, prx, elm, tp, silentNext, invite, sip.ResponseStatusOK)
			drainReqs(tp)
			drainRess(tp)

			// The ACK for a 2xx is a new upstream transaction — a fresh
			// top Via branch — so it routes through the same prepared-target
			// path as any other request and is sent once, without a
			// transaction or a provisional generation.
			ack := newInAckReq(t, invite, newInRes(t, invite, sip.ResponseStatusOK))
			if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, ack); err != nil {
				t.Fatalf("prx.InterceptInboundRequest(ACK) error = %v, want nil", err)
			}

			sent := testutil.WaitResult(t, tp.sendReqCalls, "forwarded end-to-end ACK").req
			if !sent.Method().Equal(sip.RequestMethodAck) {
				t.Fatalf("sent method = %v, want ACK", sent.Method())
			}
			if got, want := sent.RemoteAddr(), netip.MustParseAddrPort("10.0.0.9:5060"); got != want {
				t.Fatalf("sent destination = %v, want %v", got, want)
			}
			if branch := statelessTopBranch(t, sent); !strings.HasPrefix(branch, sip.MagicCookie+".proxy-stateless.") {
				t.Fatalf("top via branch = %q, want proxy-stateless mark", branch)
			}
			if got := countAckTxs(elm); got != 0 {
				t.Fatalf("ACK transactions=%d, want 0", got)
			}
			if len(tp.sendResCalls) != 0 {
				t.Fatal("ACK received a SIP response")
			}
		})
	})

	t.Run("unroutable ack errors with no response", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()

		route := ackRoute("ack-unroutable")
		route.ResolveRequestTargets = func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
			return proxy.TargetPlan{}, errors.ErrorWrap("no contacts")
		}
		elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{ServedDomains: served})

		// An end-to-end ACK whose routing cannot be satisfied fails through
		// the pipeline exit: the error is reported, a SIP response is never
		// sent, and no transaction or wire send is produced.
		invite := newInInviteReq(t, "UDP", sip.MagicCookie+".ack-unroutable", laddr, raddr)
		ack := newInAckReq(t, invite, newInRes(t, invite, sip.ResponseStatusOK))
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, ack); err == nil {
			t.Fatal("prx.InterceptInboundRequest(ACK) error = nil, want routing failure")
		}
		if len(tp.sendResCalls) != 0 {
			t.Fatal("ACK received a SIP response")
		}
		if got := len(tp.sendReqCalls); got != 0 {
			t.Fatalf("unroutable ACK sends=%d, want 0", got)
		}
		if got := countAckTxs(elm); got != 0 {
			t.Fatalf("ACK transactions=%d, want 0", got)
		}
	})
}
