package sip

import (
	"context"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip/header"
)

// lcCountingTransp wraps the internal stub transport with a Close counter so
// lifecycle tests can prove the proxy never closes a borrowed transport.
type lcCountingTransp struct {
	*prxInternalTransp

	closeCalls atomic.Int32
}

func (tp *lcCountingTransp) Close() error {
	tp.closeCalls.Add(1)
	return nil
}

func newLifecycleTestProxy(
	tb testing.TB,
	route ProxyRoute,
	opts ProxyOptions,
	elems ...*Element,
) (*Element, *Proxy, *lcCountingTransp) {
	tb.Helper()

	tp := &lcCountingTransp{prxInternalTransp: newPrxInternalTransp()}

	elm := &Element{}
	if len(elems) > 0 && elems[len(elems)-1] != nil {
		elm = elems[len(elems)-1]
	}
	tb.Cleanup(func() { elm.Close() })

	if err := elm.TrackTransport(tp); err != nil {
		tb.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	prx, err := NewProxy(elm, []ProxyRoute{route}, opts)
	if err != nil {
		tb.Fatalf("NewProxy() error = %v, want nil", err)
	}

	return elm, prx, tp
}

// lcTarget builds a literal-IP plan target.
func lcTarget(id, host string) ProxyTarget {
	return ProxyTarget{
		ID:  id,
		URI: &URI{User: MakeUserInfo("alice"), Addr: MakeHostPortAddr(host, 5060)},
	}
}

// lcGroups builds a target plan from ordered ID/host groups.
func lcGroups(groups ...[]ProxyTarget) ProxyTargetPlan {
	return ProxyTargetPlan{Groups: groups}
}

func lcRoute(name string, plan ProxyTargetPlan) ProxyRoute {
	return ProxyRoute{
		Name:        name,
		ForwardMode: ForwardModeStateful,
		ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
			return plan, nil
		},
	}
}

// newInternalInfoReq is the non-INVITE sibling of newInternalInviteReq.
func newInternalInfoReq(branch string) *RequestEnvelope {
	req := newInternalInviteReq(branch)
	req.WithMessage(func(r *Request) {
		r.Method = RequestMethodInfo
		if cseq, ok := r.Headers.CSeq(); ok {
			r.Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: RequestMethodInfo})
		}
	})
	return req
}

// drainInternalReqs empties the stub transport's recorded request sends.
func drainInternalReqs(tp *prxInternalTransp) []*RequestEnvelope {
	var reqs []*RequestEnvelope
	for {
		select {
		case req := <-tp.sendReqCalls:
			reqs = append(reqs, req)
		default:
			return reqs
		}
	}
}

func countInternalReqs(tp *prxInternalTransp) int { return len(drainInternalReqs(tp)) }

// countInternalRes drains and counts the stub transport's recorded response
// sends.
func countInternalRes(tp *prxInternalTransp) int {
	cnt := 0
	for {
		select {
		case <-tp.sendResCalls:
			cnt++
		default:
			return cnt
		}
	}
}

// lcProbeMW is a middleware carrying only the optional element shutdown hook,
// used to observe the proxy shutdown ordering inside Element.Shutdown.
type lcProbeMW struct {
	onShutdown func(ctx context.Context)
}

func (mw *lcProbeMW) Shutdown(ctx context.Context) error {
	if mw.onShutdown != nil {
		mw.onShutdown(ctx)
	}
	return nil
}

func TestProxy_Close(t *testing.T) {
	t.Parallel()

	t.Run("seals admission and retires owned work", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := lcRoute("lc-seal", lcGroups(
				[]ProxyTarget{lcTarget("A", "10.0.0.9")},
				[]ProxyTarget{lcTarget("C", "10.0.0.11")},
			))
			elm, prx, tp := newLifecycleTestProxy(t, route, ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			})

			req := newInternalInviteReq(MagicCookie + ".lc-seal")
			rq := admitInternalReq(t, prx, req)
			synctest.Wait()

			sends := drainInternalReqs(tp.prxInternalTransp)
			if got := len(sends); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}

			srvKey, err := MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			srvTx, ok := elm.LoadServerTransaction(srvKey)
			if !ok {
				t.Fatal("upstream transaction not registered")
			}
			clnKey, err := MakeClientTransactionKey(sends[0])
			if err != nil {
				t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
			}
			clnTx, ok := elm.LoadClientTransaction(clnKey)
			if !ok {
				t.Fatal("forwarding client transaction not registered")
			}

			rq.mu.Lock()
			armed := rq.targets["A"].tmrC != nil
			rq.mu.Unlock()
			if !armed {
				t.Fatal("INVITE attempt has no armed Timer C")
			}

			if err := prx.Close(); err != nil {
				t.Fatalf("prx.Close() error = %v, want nil", err)
			}
			synctest.Wait()

			// The sealed admission never lets the scheduled group C start.
			if got := countInternalReqs(tp.prxInternalTransp); got != 0 {
				t.Fatalf("outbound sends after close=%d, want 0 (scheduled group C must not start)", got)
			}

			// Owned transactions terminated and request indexes released.
			if got := srvTx.State(); got != TransactionStateTerminated {
				t.Fatalf("upstream transaction state=%v, want %v", got, TransactionStateTerminated)
			}
			if got := clnTx.State(); got != TransactionStateTerminated {
				t.Fatalf("client transaction state=%v, want %v", got, TransactionStateTerminated)
			}
			if _, ok := elm.LoadServerTransaction(srvKey); ok {
				t.Fatal("upstream transaction still registered after close")
			}
			if _, ok := elm.LoadClientTransaction(clnKey); ok {
				t.Fatal("client transaction still registered after close")
			}
			if _, ok := prx.LookupRequest(srvKey); ok {
				t.Fatal("request index still resolves after close")
			}

			rq.mu.Lock()
			closed := rq.closed
			tmrC := rq.targets["A"].tmrC
			deadline := rq.targets["A"].timeC
			unbinds := len(rq.unbinds)
			rq.mu.Unlock()
			if !closed {
				t.Fatal("request coordinator not sealed by close")
			}
			if tmrC != nil || !deadline.IsZero() {
				t.Fatal("Timer C still armed after close")
			}
			if unbinds != 0 {
				t.Fatalf("staged transaction unbinds=%d, want 0 (released)", unbinds)
			}

			// A new inbound request passes through unclaimed.
			var nexted atomic.Int32
			err = prx.InterceptInboundRequest(
				ctx,
				RequestReceiverFunc(func(context.Context, *RequestEnvelope) error {
					nexted.Add(1)
					return nil
				}),
				newInternalInviteReq(MagicCookie+".lc-seal-late"),
			)
			if err != nil {
				t.Fatalf("post-close InterceptInboundRequest() error = %v, want nil", err)
			}
			if got := nexted.Load(); got != 1 {
				t.Fatalf("post-close next calls=%d, want 1", got)
			}
			if got := countInternalReqs(tp.prxInternalTransp); got != 0 {
				t.Fatalf("post-close outbound sends=%d, want 0", got)
			}
			if got := len(prx.srvRqs); got != 0 {
				t.Fatalf("request index size=%d, want 0", got)
			}

			if got := tp.closeCalls.Load(); got != 0 {
				t.Fatal("closed borrowed transport")
			}
			if err := prx.Close(); err != nil {
				t.Fatalf("second Close: %v", err)
			}
		})
	})

	t.Run("pending client admission terminates too", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := lcRoute("lc-adm", lcGroups(
				[]ProxyTarget{lcTarget("A", "10.0.0.9")},
			))
			elm, prx, tp := newLifecycleTestProxy(t, route, ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			})

			rq := admitInternalReq(t, prx, newInternalInviteReq(MagicCookie+".lc-adm"))
			synctest.Wait()
			sends := drainInternalReqs(tp.prxInternalTransp)
			if got := len(sends); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}

			// Stage a still-pending client-transaction admission: binds are
			// staged in rq.unbinds but no commitTxAdmissions ever ran, so the
			// transaction lives only in rq.pendingAdms — the pre-seal window
			// between admission and send-result commit.
			out := sends[0].Clone().(*RequestEnvelope)
			out.WithMessage(func(r *Request) {
				if via, ok := r.Headers.FirstVia(); ok && via != nil {
					via.Params.Set("branch", GenerateBranch(0))
				}
			})
			pendTx, err := elm.NewClientTransaction(ctx, out, tp)
			if err != nil {
				t.Fatalf("elm.NewClientTransaction() error = %v, want nil", err)
			}
			rq.mu.Lock()
			gen := rq.targets["A"].gen
			rq.mu.Unlock()
			rq.attachClnTx(pendTx, &prxTxOwner{rq: rq, targetID: "A", gen: gen})
			rq.mu.Lock()
			pending := len(rq.pendingAdms)
			rq.mu.Unlock()
			if pending != 1 {
				t.Fatalf("pending admissions=%d, want 1", pending)
			}
			if err := pendTx.Start(ctx); err != nil {
				t.Fatalf("pendTx.Start() error = %v, want nil", err)
			}
			pendKey := pendTx.Key()
			if _, ok := elm.LoadClientTransaction(pendKey); !ok {
				t.Fatal("pending admission transaction not registered")
			}

			if err := prx.Close(); err != nil {
				t.Fatalf("prx.Close() error = %v, want nil", err)
			}
			synctest.Wait()

			if got := pendTx.State(); got != TransactionStateTerminated {
				t.Fatalf("pending admission transaction state=%v, want %v",
					got, TransactionStateTerminated)
			}
			if _, ok := elm.LoadClientTransaction(pendKey); ok {
				t.Fatal("pending admission transaction still registered after close")
			}
			if err := prx.Close(); err != nil {
				t.Fatalf("second Close: %v", err)
			}
		})
	})

	t.Run("pending trying stops via transaction termination", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			route := lcRoute("lc-trying", lcGroups(
				[]ProxyTarget{lcTarget("A", "10.0.0.9")},
			))
			route.ServerTransactionOptions.NonInviteServerTransactionBehavior = NonInviteServerTransactionBehaviorRFC4320
			elm, prx, tp := newLifecycleTestProxy(t, route, ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			})

			req := newInternalInfoReq(MagicCookie + ".lc-trying")
			admitInternalReq(t, prx, req)
			synctest.Wait()
			if got := countInternalReqs(tp.prxInternalTransp); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}

			srvKey, err := MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			srvTx, ok := elm.LoadServerTransaction(srvKey)
			if !ok {
				t.Fatal("upstream transaction not registered")
			}
			snapper, ok := srvTx.(ServerTransactionSnapshotter)
			if !ok {
				t.Fatal("upstream transaction has no snapshotter")
			}
			snap, err := snapper.Snapshot()
			if err != nil {
				t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
			}
			if snap.TimerTrying == nil {
				t.Fatal("no pending delayed Trying deadline before close")
			}

			if err := prx.Close(); err != nil {
				t.Fatalf("prx.Close() error = %v, want nil", err)
			}
			if got := srvTx.State(); got != TransactionStateTerminated {
				t.Fatalf("upstream transaction state=%v, want %v", got, TransactionStateTerminated)
			}

			// Past the delayed Trying deadline nothing may be emitted: the
			// transaction termination stopped the pending Trying timer.
			time.Sleep(5 * time.Second)
			synctest.Wait()
			if got := countInternalRes(tp.prxInternalTransp); got != 0 {
				t.Fatalf("upstream response sends after close=%d, want 0", got)
			}
		})
	})

	t.Run("borrowed resources and unrelated transactions stay open", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			dlv := &lcCountingTransp{prxInternalTransp: newPrxInternalTransp()}
			route := lcRoute("lc-borrow", lcGroups(
				[]ProxyTarget{{
					ID:       "A",
					URI:      &URI{User: MakeUserInfo("alice"), Addr: MakeHostPortAddr("10.0.0.9", 5060)},
					Delivery: ProxyDeliveryRef{Kind: "gw", Key: "x"},
				}},
			))
			elm, prx, tp := newLifecycleTestProxy(t, route, ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
				ResolveDelivery: func(context.Context, ProxyDeliveryRef) (ProxyBoundDelivery, error) {
					return ProxyBoundDelivery{
						Transport:  dlv,
						LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
						RemoteAddr: netip.MustParseAddrPort("10.0.0.9:5060"),
					}, nil
				},
			})

			// A foreign transaction owned by no proxy request must never be
			// touched by the proxy lifecycle.
			frnTx, err := elm.NewServerTransaction(ctx, newInternalInviteReq(MagicCookie+".lc-foreign"), tp)
			if err != nil {
				t.Fatalf("elm.NewServerTransaction() error = %v, want nil", err)
			}
			if err := frnTx.Start(ctx); err != nil {
				t.Fatalf("frnTx.Start() error = %v, want nil", err)
			}

			rq := admitInternalReq(t, prx, newInternalInviteReq(MagicCookie+".lc-borrow"))
			synctest.Wait()
			if got := countInternalReqs(dlv.prxInternalTransp); got != 1 {
				t.Fatalf("bound delivery sends=%d, want 1", got)
			}

			if err := prx.Close(); err != nil {
				t.Fatalf("prx.Close() error = %v, want nil", err)
			}
			synctest.Wait()

			if got := tp.closeCalls.Load(); got != 0 {
				t.Fatal("closed borrowed transport")
			}
			if got := dlv.closeCalls.Load(); got != 0 {
				t.Fatal("closed bound delivery transport")
			}
			if got := frnTx.State(); got == TransactionStateTerminated {
				t.Fatal("closed unrelated transaction")
			}
			if !rq.closed {
				t.Fatal("request coordinator not sealed by close")
			}
			if err := frnTx.Terminate(ctx, nil); err != nil {
				t.Fatalf("frnTx.Terminate() error = %v, want nil", err)
			}
		})
	})

	t.Run("reentrant close finishes the captured dispatch and detaches", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			route := lcRoute("lc-dispatch", lcGroups(
				[]ProxyTarget{lcTarget("A", "10.0.0.9")},
			))
			_, prx, tp := newLifecycleTestProxy(t, route, ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			})

			var h1Calls, h2Calls atomic.Int32
			prx.BindEventHandler(ProxyEventHandlerFunc(func(ctx context.Context, _ ProxyEvent) {
				h1Calls.Add(1)
				// Close must not wait on the in-flight captured dispatch:
				// reentrant unbind and close inside a handler are safe.
				if err := prx.Close(); err != nil {
					t.Errorf("prx.Close() inside dispatch error = %v, want nil", err)
				}
			}))
			prx.BindEventHandler(ProxyEventHandlerFunc(func(context.Context, ProxyEvent) {
				h2Calls.Add(1)
			}))
			snapCnt := atomic.Int32{}
			prx.BindSnapshotHandler(ProxyRequestSnapshotHandlerFunc(func(context.Context, *ProxyRequestSnapshot) {
				snapCnt.Add(1)
			}))

			prx.emitEvt(t.Context(), ProxyEvent{Kind: ProxyEventRequestHandled, Time: time.Now()})
			if got := h1Calls.Load() + h2Calls.Load(); got != 2 {
				t.Fatalf("captured dispatch delivered=%d, want 2 (captured dispatch may finish)", got)
			}

			// The detach is committed: a later dispatch reaches nobody.
			prx.emitEvt(t.Context(), ProxyEvent{Kind: ProxyEventRequestForwarded, Time: time.Now()})
			if got := h1Calls.Load() + h2Calls.Load(); got != 2 {
				t.Fatalf("post-close dispatch delivered=%d, want 2 total (subscriptions detached)", got)
			}
			if got := prx.onEvent.Len() + prx.onSnap.Len(); got != 0 {
				t.Fatalf("subscriptions after close=%d, want 0", got)
			}

			if err := prx.Close(); err != nil {
				t.Fatalf("second Close: %v", err)
			}
			if got := tp.closeCalls.Load(); got != 0 {
				t.Fatal("closed borrowed transport")
			}
		})
	})

	t.Run("concurrent binds, unbinds and close do not deadlock", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			route := lcRoute("lc-race", lcGroups(
				[]ProxyTarget{lcTarget("A", "10.0.0.9")},
			))
			_, prx, _ := newLifecycleTestProxy(t, route, ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			})

			h := ProxyEventHandlerFunc(func(context.Context, ProxyEvent) {})
			var wg sync.WaitGroup
			for range 4 {
				wg.Go(func() {
					for range 25 {
						unbind := prx.BindEventHandler(h)
						unbind()
						unbind()
					}
				})
			}
			wg.Go(func() {
				for range 25 {
					unbind := prx.BindSnapshotHandler(
						ProxyRequestSnapshotHandlerFunc(func(context.Context, *ProxyRequestSnapshot) {}),
					)
					unbind()
				}
			})
			done := make(chan struct{})
			go func() {
				defer close(done)
				if err := prx.Close(); err != nil {
					t.Errorf("prx.Close() error = %v, want nil", err)
				}
			}()
			wg.Wait()
			<-done

			if err := prx.Close(); err != nil {
				t.Fatalf("second Close: %v", err)
			}
		})
	})

	t.Run("closed proxy rejects new subscriptions and restores", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := lcRoute("lc-late", lcGroups(
				[]ProxyTarget{lcTarget("A", "10.0.0.9")},
			))
			_, prx, _ := newLifecycleTestProxy(t, route, ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			})

			if err := prx.Close(); err != nil {
				t.Fatalf("prx.Close() error = %v, want nil", err)
			}

			unbind := prx.BindEventHandler(ProxyEventHandlerFunc(func(context.Context, ProxyEvent) {
				t.Error("event handler bound after close received a dispatch")
			}))
			unbind()
			prx.emitEvt(ctx, ProxyEvent{Kind: ProxyEventRequestHandled, Time: time.Now()})

			if _, err := prx.RestoreRequest(ctx, nil, ProxyRestoreBindings{}); !errors.Is(err, ErrProxyClosed) {
				t.Fatalf("prx.RestoreRequest() error = %v, want %v", err, ErrProxyClosed)
			}
			if err := prx.Close(); err != nil {
				t.Fatalf("second Close: %v", err)
			}
		})
	})

	t.Run("element shutdown routes through proxy shutdown first", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := lcRoute("lc-elem", lcGroups(
				[]ProxyTarget{lcTarget("A", "10.0.0.9")},
			))
			elm := &Element{}
			_, prx, tp := newLifecycleTestProxy(t, route, ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			}, elm)

			var sawProxyClosed, sawTransportOpen atomic.Bool
			elm.UseMiddleware(&lcProbeMW{onShutdown: func(context.Context) {
				sawProxyClosed.Store(prx.closed.Load())
				sawTransportOpen.Store(tp.closeCalls.Load() == 0)
			}})

			rq := admitInternalReq(t, prx, newInternalInviteReq(MagicCookie+".lc-elem"))
			synctest.Wait()

			if err := elm.Shutdown(ctx); err != nil {
				t.Fatalf("elm.Shutdown() error = %v, want nil", err)
			}

			if !prx.closed.Load() {
				t.Fatal("element shutdown did not route through proxy shutdown")
			}
			if !sawProxyClosed.Load() {
				t.Fatal("later middleware hook observed the proxy still open")
			}
			if !sawTransportOpen.Load() {
				t.Fatal("transport closed before the proxy shutdown hook ran")
			}
			if !rq.closed {
				t.Fatal("request coordinator not sealed by element shutdown")
			}
			if got := tp.closeCalls.Load(); got != 1 {
				t.Fatalf("transport close calls=%d, want 1 (closed by the element, not the proxy)", got)
			}
			if err := prx.Close(); err != nil {
				t.Fatalf("second Close: %v", err)
			}
		})
	})
}
