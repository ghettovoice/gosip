package proxy

import (
	"context"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// lcCountingTransp wraps the internal stub transport with a Close counter so
// lifecycle tests can prove the proxy never closes a borrowed transport.
type lcCountingTransp struct {
	*internalTransp

	closeCalls atomic.Int32
}

func (tp *lcCountingTransp) Close() error {
	tp.closeCalls.Add(1)
	return nil
}

func newLifecycleTestProxy(
	tb testing.TB,
	route Route,
	opts Options,
	elems ...*sip.Element,
) (*sip.Element, *Proxy, *lcCountingTransp) {
	tb.Helper()

	tp := &lcCountingTransp{internalTransp: newInternalTransp()}

	elm := &sip.Element{}
	if len(elems) > 0 && elems[len(elems)-1] != nil {
		elm = elems[len(elems)-1]
	}
	tb.Cleanup(func() { elm.Close() })

	if err := elm.TrackTransport(tp); err != nil {
		tb.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	prx, err := New([]Route{route}, opts)
	if err != nil {
		tb.Fatalf("proxy.New() error = %v, want nil", err)
	}

	elm.UseComponent(prx)

	return elm, prx, tp
}

// lcTarget builds a literal-IP plan target.
func lcTarget(id, host string) Target {
	return Target{
		ID:  id,
		URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr(host, 5060)},
	}
}

// lcGroups builds a target plan from ordered ID/host groups.
func lcGroups(groups ...[]Target) TargetPlan {
	return TargetPlan{Groups: groups}
}

func lcRoute(name string, plan TargetPlan) Route {
	return Route{
		Name:        name,
		ForwardMode: ForwardModeStateful,
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
			return plan, nil
		},
	}
}

// newInternalInfoReq is the non-INVITE sibling of newInternalInviteReq.
func newInternalInfoReq(branch string) *sip.RequestEnvelope {
	req := newInternalInviteReq(branch)
	req.WithMessage(func(r *sip.Request) {
		r.Method = sip.RequestMethodInfo
		if cseq, ok := r.Headers.CSeq(); ok {
			r.Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: sip.RequestMethodInfo})
		}
	})
	return req
}

// drainInternalReqs empties the stub transport's recorded request sends.
func drainInternalReqs(tp *internalTransp) []*sip.RequestEnvelope {
	var reqs []*sip.RequestEnvelope
	for {
		select {
		case req := <-tp.sendReqCalls:
			reqs = append(reqs, req)
		default:
			return reqs
		}
	}
}

func countInternalReqs(tp *internalTransp) int { return len(drainInternalReqs(tp)) }

// countInternalRes drains and counts the stub transport's recorded response
// sends.
func countInternalRes(tp *internalTransp) int {
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

// lcProbeComponent is a component carrying only the optional element shutdown hook,
// used to observe the proxy shutdown ordering inside sip.Element.Shutdown.
type lcProbeComponent struct {
	onShutdown func(ctx context.Context)
}

func (c *lcProbeComponent) Shutdown(ctx context.Context) error {
	if c.onShutdown != nil {
		c.onShutdown(ctx)
	}
	return nil
}

// lcCountingComponent is an element component lifecycle spy counting the optional
// element Shutdown and Close hook calls, so lifecycle tests can prove the
// proxy never drives the owning element's component lifecycle itself.
type lcCountingComponent struct {
	shutdownCalls atomic.Int32
	closeCalls    atomic.Int32
}

func (c *lcCountingComponent) Shutdown(context.Context) error {
	c.shutdownCalls.Add(1)
	return nil
}

func (c *lcCountingComponent) Close() error {
	c.closeCalls.Add(1)
	return nil
}

// lcSharedSide is one element side of a shared-proxy lifecycle fixture: its
// own element, its own counting transport, and the proxy registration
// unbind.
type lcSharedSide struct {
	elm    *sip.Element
	tp     *lcCountingTransp
	unbind func()
}

// newSharedLifecycleProxy binds one proxy to two fresh elements, each
// tracking its own counting transport.
func newSharedLifecycleProxy(
	tb testing.TB,
	route Route,
	opts Options,
) (*Proxy, []lcSharedSide) {
	tb.Helper()

	prx, err := New([]Route{route}, opts)
	if err != nil {
		tb.Fatalf("proxy.New() error = %v, want nil", err)
	}

	sides := make([]lcSharedSide, 0, 2)
	for range 2 {
		tp := &lcCountingTransp{internalTransp: newInternalTransp()}
		elm := &sip.Element{}
		tb.Cleanup(func() { elm.Close() })
		if err := elm.TrackTransport(tp); err != nil {
			tb.Fatalf("elm.TrackTransport() error = %v, want nil", err)
		}
		sides = append(sides, lcSharedSide{elm: elm, tp: tp, unbind: elm.UseComponent(prx)})
	}
	return prx, sides
}

// lcLiveTxCount counts the transactions still registered on the element.
func lcLiveTxCount(elm *sip.Element) int {
	cnt := 0
	for range elm.AllServerTransactions() {
		cnt++
	}
	for range elm.AllClientTransactions() {
		cnt++
	}
	return cnt
}

// lcRecordTermElm returns a transaction state handler that records the
// element carried by the terminating transition's callback context.
func lcRecordTermElm(dst *atomic.Pointer[sip.Element]) sip.TransactionStateHandlerFunc {
	return func(cbCtx context.Context, _, to sip.TransactionState) {
		if to != sip.TransactionStateTerminated {
			return
		}
		if cbElm, ok := sip.ElementFromContext(cbCtx); ok {
			dst.Store(cbElm)
		}
	}
}

func TestProxy_Close(t *testing.T) {
	t.Parallel()

	t.Run("seals admission and retires owned work", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := lcRoute("lc-seal", lcGroups(
				[]Target{lcTarget("A", "10.0.0.9")},
				[]Target{lcTarget("C", "10.0.0.11")},
			))
			elm, prx, tp := newLifecycleTestProxy(t, route, Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			req := newInternalInviteReq(sip.MagicCookie + ".lc-seal")
			rq := admitInternalReq(t, elm, prx, req)
			synctest.Wait()

			sends := drainInternalReqs(tp.internalTransp)
			if got := len(sends); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}

			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			srvTx, ok := elm.LoadServerTransaction(srvKey)
			if !ok {
				t.Fatal("upstream transaction not registered")
			}
			clnKey, err := sip.MakeClientTransactionKey(sends[0])
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
			if got := countInternalReqs(tp.internalTransp); got != 0 {
				t.Fatalf("outbound sends after close=%d, want 0 (scheduled group C must not start)", got)
			}

			// Owned transactions terminated and request indexes released.
			if got := srvTx.State(); got != sip.TransactionStateTerminated {
				t.Fatalf("upstream transaction state=%v, want %v", got, sip.TransactionStateTerminated)
			}
			if got := clnTx.State(); got != sip.TransactionStateTerminated {
				t.Fatalf("client transaction state=%v, want %v", got, sip.TransactionStateTerminated)
			}
			if _, ok := elm.LoadServerTransaction(srvKey); ok {
				t.Fatal("upstream transaction still registered after close")
			}
			if _, ok := elm.LoadClientTransaction(clnKey); ok {
				t.Fatal("client transaction still registered after close")
			}
			if _, ok := prx.LookupRequest(elm, srvKey); ok {
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
				sip.ContextWithElement(ctx, elm),
				sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
					nexted.Add(1)
					return nil
				}),
				newInternalInviteReq(sip.MagicCookie+".lc-seal-late"),
			)
			if err != nil {
				t.Fatalf("post-close InterceptInboundRequest() error = %v, want nil", err)
			}
			if got := nexted.Load(); got != 1 {
				t.Fatalf("post-close next calls=%d, want 1", got)
			}
			if got := countInternalReqs(tp.internalTransp); got != 0 {
				t.Fatalf("post-close outbound sends=%d, want 0", got)
			}
			if got := srvReqCount(prx, elm); got != 0 {
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
				[]Target{lcTarget("A", "10.0.0.9")},
			))
			elm, prx, tp := newLifecycleTestProxy(t, route, Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			rq := admitInternalReq(t, elm, prx, newInternalInviteReq(sip.MagicCookie+".lc-adm"))
			synctest.Wait()
			sends := drainInternalReqs(tp.internalTransp)
			if got := len(sends); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}

			// Stage a still-pending client-transaction admission: binds are
			// staged in rq.unbinds but no commitTxAdmissions ever ran, so the
			// transaction lives only in rq.pendingAdms — the pre-seal window
			// between admission and send-result commit.
			out := sends[0].Clone().(*sip.RequestEnvelope)
			out.WithMessage(func(r *sip.Request) {
				if via, ok := r.Headers.FirstVia(); ok && via != nil {
					via.Params.Set("branch", sip.GenerateBranch(0))
				}
			})
			pendTx, err := elm.NewClientTransaction(ctx, out, tp)
			if err != nil {
				t.Fatalf("elm.NewClientTransaction() error = %v, want nil", err)
			}
			rq.mu.Lock()
			gen := rq.targets["A"].gen
			rq.mu.Unlock()
			rq.attachClnTx(pendTx, &txOwner{rq: rq, targetID: "A", gen: gen})
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

			if got := pendTx.State(); got != sip.TransactionStateTerminated {
				t.Fatalf("pending admission transaction state=%v, want %v",
					got, sip.TransactionStateTerminated)
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
				[]Target{lcTarget("A", "10.0.0.9")},
			))
			route.ServerTransactionOptions.NonInviteServerTransactionBehavior = sip.NonInviteServerTransactionBehaviorRFC4320
			elm, prx, tp := newLifecycleTestProxy(t, route, Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			req := newInternalInfoReq(sip.MagicCookie + ".lc-trying")
			admitInternalReq(t, elm, prx, req)
			synctest.Wait()
			if got := countInternalReqs(tp.internalTransp); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}

			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			srvTx, ok := elm.LoadServerTransaction(srvKey)
			if !ok {
				t.Fatal("upstream transaction not registered")
			}
			snapper, ok := srvTx.(sip.ServerTransactionSnapshotter)
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
			if got := srvTx.State(); got != sip.TransactionStateTerminated {
				t.Fatalf("upstream transaction state=%v, want %v", got, sip.TransactionStateTerminated)
			}

			// Past the delayed Trying deadline nothing may be emitted: the
			// transaction termination stopped the pending Trying timer.
			time.Sleep(5 * time.Second)
			synctest.Wait()
			if got := countInternalRes(tp.internalTransp); got != 0 {
				t.Fatalf("upstream response sends after close=%d, want 0", got)
			}
		})
	})

	t.Run("borrowed resources and unrelated transactions stay open", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			dlv := &lcCountingTransp{internalTransp: newInternalTransp()}
			route := lcRoute("lc-borrow", lcGroups(
				[]Target{{
					ID:       "A",
					URI:      &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr("10.0.0.9", 5060)},
					Delivery: DeliveryRef{Kind: "gw", Key: "x"},
				}},
			))
			elm, prx, tp := newLifecycleTestProxy(t, route, Options{
				ServedDomains: []string{"alice.voip.com"},
				ResolveDelivery: func(context.Context, DeliveryRef) (BoundDelivery, error) {
					return BoundDelivery{
						Transport:  dlv,
						LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
						RemoteAddr: netip.MustParseAddrPort("10.0.0.9:5060"),
					}, nil
				},
			})
			compSpy := &lcCountingComponent{}
			elm.UseComponent(compSpy)

			// A foreign transaction owned by no proxy request must never be
			// touched by the proxy lifecycle.
			frnTx, err := elm.NewServerTransaction(ctx, newInternalInviteReq(sip.MagicCookie+".lc-foreign"), tp)
			if err != nil {
				t.Fatalf("elm.NewServerTransaction() error = %v, want nil", err)
			}
			if err := frnTx.Start(ctx); err != nil {
				t.Fatalf("frnTx.Start() error = %v, want nil", err)
			}

			rq := admitInternalReq(t, elm, prx, newInternalInviteReq(sip.MagicCookie+".lc-borrow"))
			synctest.Wait()
			if got := countInternalReqs(dlv.internalTransp); got != 1 {
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
			if got := frnTx.State(); got == sip.TransactionStateTerminated {
				t.Fatal("closed unrelated transaction")
			}
			if !rq.closed {
				t.Fatal("request coordinator not sealed by close")
			}
			// The borrowed element lifecycle stays untouched: the proxy
			// close runs no element shutdown or close hooks and the element
			// still admits work.
			if got := compSpy.shutdownCalls.Load(); got != 0 {
				t.Fatal("proxy close ran element shutdown hooks")
			}
			if got := compSpy.closeCalls.Load(); got != 0 {
				t.Fatal("proxy close closed an element component")
			}
			if err := elm.TrackTransport(newInternalTransp()); err != nil {
				t.Fatalf("element unusable after proxy close: %v", err)
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
				[]Target{lcTarget("A", "10.0.0.9")},
			))
			_, prx, tp := newLifecycleTestProxy(t, route, Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			var h1Calls, h2Calls atomic.Int32
			prx.BindEventHandler(EventHandlerFunc(func(ctx context.Context, _ Event) {
				h1Calls.Add(1)
				// Close must not wait on the in-flight captured dispatch:
				// reentrant unbind and close inside a handler are safe.
				if err := prx.Close(); err != nil {
					t.Errorf("prx.Close() inside dispatch error = %v, want nil", err)
				}
			}))
			prx.BindEventHandler(EventHandlerFunc(func(context.Context, Event) {
				h2Calls.Add(1)
			}))
			snapCnt := atomic.Int32{}
			prx.BindSnapshotHandler(RequestSnapshotHandlerFunc(func(context.Context, *RequestSnapshot) {
				snapCnt.Add(1)
			}))

			prx.emitEvt(t.Context(), Event{Kind: EventKindRequestHandled, Time: time.Now()})
			if got := h1Calls.Load() + h2Calls.Load(); got != 2 {
				t.Fatalf("captured dispatch delivered=%d, want 2 (captured dispatch may finish)", got)
			}

			// The detach is committed: a later dispatch reaches nobody.
			prx.emitEvt(t.Context(), Event{Kind: EventKindRequestForwarded, Time: time.Now()})
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
				[]Target{lcTarget("A", "10.0.0.9")},
			))
			_, prx, _ := newLifecycleTestProxy(t, route, Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			h := EventHandlerFunc(func(context.Context, Event) {})
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
						RequestSnapshotHandlerFunc(func(context.Context, *RequestSnapshot) {}),
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
				[]Target{lcTarget("A", "10.0.0.9")},
			))
			_, prx, _ := newLifecycleTestProxy(t, route, Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			if err := prx.Close(); err != nil {
				t.Fatalf("prx.Close() error = %v, want nil", err)
			}

			unbind := prx.BindEventHandler(EventHandlerFunc(func(context.Context, Event) {
				t.Error("event handler bound after close received a dispatch")
			}))
			unbind()
			prx.emitEvt(ctx, Event{Kind: EventKindRequestHandled, Time: time.Now()})

			if _, err := prx.RestoreRequest(ctx, nil, RestoreBindings{}); !errors.Is(err, ErrClosed) {
				t.Fatalf("prx.RestoreRequest() error = %v, want %v", err, ErrClosed)
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
				[]Target{lcTarget("A", "10.0.0.9")},
			))
			elm := &sip.Element{}
			_, prx, tp := newLifecycleTestProxy(t, route, Options{
				ServedDomains: []string{"alice.voip.com"},
			}, elm)

			var sawProxyClosed, sawTransportOpen atomic.Bool
			elm.UseComponent(&lcProbeComponent{onShutdown: func(context.Context) {
				sawProxyClosed.Store(prx.closed.Load())
				sawTransportOpen.Store(tp.closeCalls.Load() == 0)
			}})

			rq := admitInternalReq(t, elm, prx, newInternalInviteReq(sip.MagicCookie+".lc-elem"))
			synctest.Wait()

			if err := elm.Shutdown(ctx); err != nil {
				t.Fatalf("elm.Shutdown() error = %v, want nil", err)
			}

			if !prx.closed.Load() {
				t.Fatal("element shutdown did not route through proxy shutdown")
			}
			if !sawProxyClosed.Load() {
				t.Fatal("later component hook observed the proxy still open")
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

	t.Run("admission races close", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := lcRoute("lc-race-close", lcGroups(
				[]Target{lcTarget("A", "10.0.0.9")},
			))
			prx, sides := newSharedLifecycleProxy(t, route, Options{
				ServedDomains: []string{"alice.voip.com"},
			})
			a, b := sides[0], sides[1]

			// One admitted request per element anchors the per-element
			// retirement proof: each upstream transaction must observe its
			// own element in the callback context even though the shutdown
			// context carries the other element.
			reqA := newInternalInviteReq(sip.MagicCookie + ".lc-race-a")
			reqB := newInternalInviteReq(sip.MagicCookie + ".lc-race-b")
			rqA := admitInternalReq(t, a.elm, prx, reqA)
			rqB := admitInternalReq(t, b.elm, prx, reqB)
			synctest.Wait()
			drainInternalReqs(a.tp.internalTransp)
			drainInternalReqs(b.tp.internalTransp)

			srvKeyA, err := sip.MakeServerTransactionKey(reqA)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			srvTxA, ok := a.elm.LoadServerTransaction(srvKeyA)
			if !ok {
				t.Fatal("element A upstream transaction not registered")
			}
			srvKeyB, err := sip.MakeServerTransactionKey(reqB)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			srvTxB, ok := b.elm.LoadServerTransaction(srvKeyB)
			if !ok {
				t.Fatal("element B upstream transaction not registered")
			}
			var termElmA, termElmB atomic.Pointer[sip.Element]
			srvTxA.BindStateHandler(lcRecordTermElm(&termElmA))
			srvTxB.BindStateHandler(lcRecordTermElm(&termElmB))

			var nexted atomic.Int32
			next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
				nexted.Add(1)
				return nil
			})
			var wg sync.WaitGroup
			for i, side := range []lcSharedSide{a, b} {
				for j := range 8 {
					wg.Go(func() {
						req := newInternalInviteReq(
							sip.MagicCookie + ".lc-race-" + strconv.Itoa(i) + "-" + strconv.Itoa(j),
						)
						// A racing claim either retires with the lifecycle
						// or passes through unclaimed; both results are
						// valid and must never deadlock or leak.
						_ = prx.InterceptInboundRequest(
							sip.ContextWithElement(ctx, side.elm), next, req,
						)
					})
				}
			}
			// The conflicting context element is deliberate: one
			// instance-wide shutdown retires the requests of every
			// element, each under the element that claimed it.
			if err := prx.Shutdown(sip.ContextWithElement(ctx, a.elm)); err != nil {
				t.Fatalf("prx.Shutdown() error = %v, want nil", err)
			}
			wg.Wait()
			synctest.Wait()

			if got := termElmA.Load(); got != a.elm {
				t.Errorf("element A upstream retirement element = %v, want %v", got, a.elm)
			}
			if got := termElmB.Load(); got != b.elm {
				t.Errorf("element B upstream retirement element = %v, want %v", got, b.elm)
			}
			if !rqA.closed || !rqB.closed {
				t.Fatal("admitted request coordinators not sealed by close")
			}
			for _, side := range []lcSharedSide{a, b} {
				if got := lcLiveTxCount(side.elm); got != 0 {
					t.Fatalf("live transactions on element %p = %d, want 0", side.elm, got)
				}
			}
			if got := len(prx.elms); got != 0 {
				t.Fatalf("element scopes after close = %d, want 0", got)
			}

			// Post-close matching requests pass to the next receiver
			// unclaimed on every element.
			for _, side := range []lcSharedSide{a, b} {
				var lateNexted atomic.Int32
				if err := prx.InterceptInboundRequest(
					sip.ContextWithElement(ctx, side.elm),
					sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
						lateNexted.Add(1)
						return nil
					}),
					newInternalInviteReq(sip.MagicCookie+".lc-race-late"),
				); err != nil {
					t.Fatalf("post-close InterceptInboundRequest() error = %v, want nil", err)
				}
				if got := lateNexted.Load(); got != 1 {
					t.Fatalf("post-close next calls on element %p = %d, want 1", side.elm, got)
				}
			}
		})
	})

	t.Run("blocked sends release", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := lcRoute("lc-blocked", lcGroups(
				[]Target{lcTarget("A", "10.0.0.9")},
			))
			prx, sides := newSharedLifecycleProxy(t, route, Options{
				ServedDomains: []string{"alice.voip.com"},
			})
			a, b := sides[0], sides[1]

			sendStarted := make(chan *sip.RequestEnvelope, 1)
			sendRelease := make(chan struct{})
			sendDone := make(chan struct{}, 1)
			a.tp.sendReqHook = func(_ context.Context, out *sip.RequestEnvelope) error {
				select {
				case sendStarted <- out:
				default:
				}
				<-sendRelease
				sendDone <- struct{}{}
				return nil
			}

			req := newInternalInviteReq(sip.MagicCookie + ".lc-blocked")
			rq := admitInternalReq(t, a.elm, prx, req)
			out := <-sendStarted // the branch send is blocked inside the client transaction action

			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			srvTx, ok := a.elm.LoadServerTransaction(srvKey)
			if !ok {
				t.Fatal("upstream transaction not registered")
			}
			clnKey, err := sip.MakeClientTransactionKey(out)
			if err != nil {
				t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
			}
			clnTx, ok := a.elm.LoadClientTransaction(clnKey)
			if !ok {
				t.Fatal("forwarding client transaction not registered")
			}
			var srvElm, clnElm atomic.Pointer[sip.Element]
			srvTx.BindStateHandler(lcRecordTermElm(&srvElm))
			clnTx.BindStateHandler(lcRecordTermElm(&clnElm))

			// Shutdown under a conflicting element context must not wait
			// on the send still blocked inside the transaction action:
			// releasing it lets the queued termination settle without a
			// deadlock, and every transaction callback still observes the
			// request's own element.
			if err := prx.Shutdown(sip.ContextWithElement(ctx, b.elm)); err != nil {
				t.Fatalf("prx.Shutdown() error = %v, want nil", err)
			}
			close(sendRelease)
			<-sendDone
			synctest.Wait()

			if got := srvElm.Load(); got != a.elm {
				t.Errorf("upstream transaction callback element = %v, want %v", got, a.elm)
			}
			if got := clnElm.Load(); got != a.elm {
				t.Errorf("client transaction callback element = %v, want %v", got, a.elm)
			}
			if got := srvTx.State(); got != sip.TransactionStateTerminated {
				t.Fatalf("upstream transaction state = %v, want %v", got, sip.TransactionStateTerminated)
			}
			if got := clnTx.State(); got != sip.TransactionStateTerminated {
				t.Fatalf("client transaction state = %v, want %v", got, sip.TransactionStateTerminated)
			}
			if !rq.closed {
				t.Fatal("request coordinator not sealed by shutdown")
			}
			if got := a.tp.closeCalls.Load(); got != 0 {
				t.Fatal("closed borrowed transport")
			}
		})
	})

	t.Run("unbind is not shutdown", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			route := lcRoute("lc-unbind", lcGroups(
				[]Target{lcTarget("A", "10.0.0.9")},
			))
			prx, sides := newSharedLifecycleProxy(t, route, Options{
				ServedDomains: []string{"alice.voip.com"},
			})
			a, b := sides[0], sides[1]

			// Detaching one registration is never a shutdown path: it is
			// idempotent and leaves the proxy accepting claims on every
			// other registration.
			a.unbind()
			a.unbind()
			if prx.closed.Load() {
				t.Fatal("registration unbind sealed the proxy lifecycle")
			}

			admitInternalReq(t, b.elm, prx, newInternalInviteReq(sip.MagicCookie+".lc-unbind"))
			synctest.Wait()
			if got := countInternalReqs(b.tp.internalTransp); got != 1 {
				t.Fatalf("element B outbound sends = %d, want 1", got)
			}
			if got := srvReqCount(prx, b.elm); got != 1 {
				t.Fatalf("element B request index size = %d, want 1", got)
			}
		})
	})

	t.Run("close then unbind", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := lcRoute("lc-close-unbind", lcGroups(
				[]Target{lcTarget("A", "10.0.0.9")},
			))
			prx, sides := newSharedLifecycleProxy(t, route, Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			if err := prx.Close(); err != nil {
				t.Fatalf("prx.Close() error = %v, want nil", err)
			}
			// The registration unbind after the lifecycle end is a safe
			// detach, never a second shutdown: repeated calls on both
			// registrations stay idempotent.
			for range 2 {
				for _, side := range sides {
					side.unbind()
				}
			}
			if err := prx.Close(); err != nil {
				t.Fatalf("second prx.Close() error = %v, want nil", err)
			}
			if err := prx.Shutdown(ctx); err != nil {
				t.Fatalf("prx.Shutdown() after unbinds error = %v, want nil", err)
			}
		})
	})

	t.Run("shared instance one lifecycle", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := lcRoute("lc-shared-lc", lcGroups(
				[]Target{lcTarget("A", "10.0.0.9")},
			))
			prx, sides := newSharedLifecycleProxy(t, route, Options{
				ServedDomains: []string{"alice.voip.com"},
			})
			a, b := sides[0], sides[1]

			rqB := admitInternalReq(t, b.elm, prx, newInternalInviteReq(sip.MagicCookie+".lc-shared-lc"))
			synctest.Wait()

			// Element A's component shutdown drives the whole instance:
			// one proxy registered on several elements has a single
			// lifecycle and seals admission on every element.
			if err := a.elm.Shutdown(ctx); err != nil {
				t.Fatalf("elmA.Shutdown() error = %v, want nil", err)
			}
			synctest.Wait()

			if !prx.closed.Load() {
				t.Fatal("element shutdown did not seal the shared proxy")
			}
			if !rqB.closed {
				t.Fatal("element B request coordinator not retired by the shared lifecycle")
			}
			if got := len(prx.elms); got != 0 {
				t.Fatalf("element scopes after shutdown = %d, want 0", got)
			}

			// A post-seal matching request on element B passes to the next
			// receiver unclaimed; element B itself is untouched.
			var nexted atomic.Int32
			if err := prx.InterceptInboundRequest(
				sip.ContextWithElement(ctx, b.elm),
				sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
					nexted.Add(1)
					return nil
				}),
				newInternalInviteReq(sip.MagicCookie+".lc-shared-late"),
			); err != nil {
				t.Fatalf("post-shutdown InterceptInboundRequest() error = %v, want nil", err)
			}
			if got := nexted.Load(); got != 1 {
				t.Fatalf("post-shutdown next calls on element B = %d, want 1", got)
			}
			if got := a.tp.closeCalls.Load(); got != 1 {
				t.Fatalf("element A transport close calls = %d, want 1 (closed by the element, not the proxy)", got)
			}
			if got := b.tp.closeCalls.Load(); got != 0 {
				t.Fatalf("element B transport close calls = %d, want 0 (element B lifecycle is independent)", got)
			}
		})
	})
}
