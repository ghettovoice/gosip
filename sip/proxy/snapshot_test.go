package proxy_test

import (
	"context"
	"encoding/json"
	"maps"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/proxy"
)

// noSnapSrvTransact wraps a built-in server transaction while hiding its optional
// snapshot interfaces: forwarding through it works exactly like the wrapped
// transaction, but it implements neither [sip.ServerTransactionSnapshotter]
// nor [sip.NonInviteServerTransactionBehaviorProvider].
type noSnapSrvTransact struct{ sip.ServerTransaction }

func snapTargetURI(host string) *sip.URI {
	return &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr(host, 5060)}
}

func snapCmpOpts() []cmp.Option {
	return []cmp.Option{
		cmpopts.EquateComparable(sip.ResolvedAddr{}, netip.AddrPort{}),
		cmpopts.EquateEmpty(),
	}
}

func targetStateOf(snap *proxy.RequestSnapshot, id string) *proxy.TargetSnapshot {
	for i := range snap.TargetStates {
		if snap.TargetStates[i].TargetID == id {
			return &snap.TargetStates[i]
		}
	}
	return nil
}

func attemptOfTarget(snap *proxy.RequestSnapshot, id string) *proxy.AttemptSnapshot {
	for i := range snap.Attempts {
		if snap.Attempts[i].TargetID == id {
			return &snap.Attempts[i]
		}
	}
	return nil
}

func TestProxy_Snapshot(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	targetHosts := map[string]string{"A": "10.0.0.9", "B": "10.0.0.10", "C": "10.0.0.11"}
	targetOf := func(req *sip.RequestEnvelope) string {
		u, ok := req.URI().(*sip.URI)
		if !ok {
			return ""
		}
		for id, host := range targetHosts {
			if u.Addr.Host() == host {
				return id
			}
		}
		return ""
	}
	noopNext := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
		return nil
	})

	t.Run("captures completed, active, and unstarted targets", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			var selCalls atomic.Int32
			route := &proxy.Route{
				Name:        "snap",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return proxy.TargetPlan{Groups: [][]proxy.Target{
						{
							{
								ID:         "A",
								URI:        snapTargetURI(targetHosts["A"]),
								Attributes: map[string]string{"role": "primary"},
							},
							{ID: "B", URI: snapTargetURI(targetHosts["B"])},
						},
						{
							{
								ID:       "C",
								URI:      snapTargetURI(targetHosts["C"]),
								Delivery: proxy.DeliveryRef{Kind: "gw", Key: "eu-1"},
							},
						},
					}}, nil
				},
				SelectTargets: func(context.Context, proxy.Selection) ([]string, error) {
					if selCalls.Add(1) == 1 {
						return []string{"A", "B"}, nil
					}
					// While a branch stays active an empty selection waits:
					// the next group never starts and target C stays
					// unstarted.
					return nil, nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".snap-states", laddr, raddr)
			if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), noopNext, req); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			sends := make(map[string]*sip.RequestEnvelope)
			for _, out := range drainReqs(tp) {
				if out.Method().Equal(sip.RequestMethodInvite) {
					sends[targetOf(out)] = out
				}
			}
			if len(sends) != 2 || sends["A"] == nil || sends["B"] == nil {
				t.Fatalf("outbound branch sends=%d, want targets A and B", len(sends))
			}

			// Settle A with a final response; B stays active, C unstarted.
			keyA, err := sip.MakeClientTransactionKey(sends["A"])
			if err != nil {
				t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
			}
			txA, ok := elm.LoadClientTransaction(keyA)
			if !ok {
				t.Fatal("forwarding client transaction of target A not registered")
			}
			if err := txA.RecvResponse(ctx, newInRes(t, sends["A"], sip.ResponseStatusNotFound)); err != nil {
				t.Fatalf("clnTx.RecvResponse(404) error = %v, want nil", err)
			}
			synctest.Wait()

			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			rq, ok := prx.LookupRequest(elm, srvKey)
			if !ok || rq == nil {
				t.Fatal("prx.LookupRequest(elm, srvKey) found no owned request")
			}

			snap, err := rq.Snapshot()
			if err != nil {
				t.Fatalf("rq.Snapshot() error = %v, want nil", err)
			}
			if snap.Version != 1 {
				t.Fatalf("snapshot version=%d, want 1", snap.Version)
			}
			if snap.RouteName != "snap" || snap.Mode != proxy.ForwardModeStateful {
				t.Fatalf("snapshot route=(%q, %v), want (%q, %v)", snap.RouteName, snap.Mode, "snap", proxy.ForwardModeStateful)
			}
			if !snap.UpstreamKey.Equal(srvKey) {
				t.Fatalf("snapshot upstream key=%v, want %v", snap.UpstreamKey, srvKey)
			}
			if snap.OriginalRequest == nil || !snap.OriginalRequest.Method().Equal(sip.RequestMethodInvite) {
				t.Fatal("snapshot original request missing or not INVITE")
			}
			if snap.Completed || snap.FinalForwarded || snap.Cancelled {
				t.Fatalf("snapshot flags=(completed=%t, forwarded=%t, cancelled=%t), want all false",
					snap.Completed, snap.FinalForwarded, snap.Cancelled)
			}
			if snap.RequestID == "" || snap.Sequence == 0 {
				t.Fatalf("snapshot correlation=(%q, %d), want a minted request ID and a nonzero sequence",
					snap.RequestID, snap.Sequence)
			}
			if !snap.FirstForwardCommitted || snap.CompletionCommitted {
				t.Fatalf(
					"snapshot observation flags=(forward=%t, completion=%t), "+
						"want a committed forward and an unfinished completion",
					snap.FirstForwardCommitted,
					snap.CompletionCommitted,
				)
			}
			if got := len(snap.TargetPlan.Groups); got != 2 {
				t.Fatalf("snapshot plan groups=%d, want 2", got)
			}
			if got := snap.TargetPlan.Groups[1][0].Delivery; got != (proxy.DeliveryRef{Kind: "gw", Key: "eu-1"}) {
				t.Fatalf("snapshot plan delivery=%+v, want gw/eu-1", got)
			}

			stA := targetStateOf(snap, "A")
			if stA == nil || !stA.Started || !stA.Completed || stA.Declined {
				t.Fatalf("target A state=%+v, want started and completed", stA)
			}
			stB := targetStateOf(snap, "B")
			if stB == nil || !stB.Started || stB.Completed || stB.Declined {
				t.Fatalf("target B state=%+v, want started and active", stB)
			}
			stC := targetStateOf(snap, "C")
			if stC == nil || stC.Started || stC.Completed || stC.Declined {
				t.Fatalf("target C state=%+v, want unstarted", stC)
			}
			if got := len(stA.AttemptedAddresses); got != 1 {
				t.Fatalf("target A attempted addresses=%d, want 1", got)
			}

			attA := attemptOfTarget(snap, "A")
			if attA == nil || !attA.Completed || attA.Key.IsZero() {
				t.Fatalf("attempt A=%+v, want a completed attempt with a key", attA)
			}
			if !attA.InitialSendSettled || !attA.InitialSendSucceeded ||
				attA.InitialSendErrorClass != "" {
				t.Fatalf(
					"attempt A initial send=(settled=%t, succeeded=%t, class=%q), want a settled success",
					attA.InitialSendSettled,
					attA.InitialSendSucceeded,
					attA.InitialSendErrorClass,
				)
			}
			if attA.Response == nil || attA.Response.Status() != sip.ResponseStatusNotFound {
				t.Fatalf("attempt A response=%v, want 404", attA.Response)
			}
			if attA.State != sip.TransactionStateCompleted {
				t.Fatalf("attempt A state=%v, want %v", attA.State, sip.TransactionStateCompleted)
			}
			if !attA.TimerCDeadline.IsZero() {
				t.Fatal("settled attempt A kept an armed Timer C deadline")
			}
			attB := attemptOfTarget(snap, "B")
			if attB == nil || attB.Completed || attB.Cancelled || attB.DeferredCancel {
				t.Fatalf("attempt B=%+v, want an active uncancelled attempt", attB)
			}
			if attB.State != sip.TransactionStateCalling {
				t.Fatalf("attempt B state=%v, want %v", attB.State, sip.TransactionStateCalling)
			}
			if attB.TimerCDeadline.IsZero() {
				t.Fatal("active INVITE attempt B has no Timer C deadline")
			}
			if attB.Key.Equal(attA.Key) {
				t.Fatal("attempts A and B share one client transaction key")
			}

			if got := len(snap.Candidates); got != 1 {
				t.Fatalf("response context candidates=%d, want 1", got)
			}
			if got := snap.Candidates[0].Status(); got != sip.ResponseStatusNotFound {
				t.Fatalf("candidate status=%v, want 404", got)
			}
		})
	})

	t.Run("returned snapshots are detached clones", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := &proxy.Route{
				Name:        "snap-clone",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return proxy.TargetPlan{Groups: [][]proxy.Target{
						{{
							ID:         "A",
							URI:        snapTargetURI(targetHosts["A"]),
							Attributes: map[string]string{"role": "primary"},
						}},
					}}, nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".snap-clone", laddr, raddr)
			if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), noopNext, req); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			outReq := testutil.WaitResult(t, tp.sendReqCalls, "outbound branch send").req
			key, err := sip.MakeClientTransactionKey(outReq)
			if err != nil {
				t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
			}
			clnTx, ok := elm.LoadClientTransaction(key)
			if !ok {
				t.Fatal("forwarding client transaction not registered")
			}
			if err := clnTx.RecvResponse(ctx, newInRes(t, outReq, sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("clnTx.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()

			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			rq, ok := prx.LookupRequest(elm, srvKey)
			if !ok {
				t.Fatal("prx.LookupRequest(elm, ) found no owned request")
			}

			first, err := rq.Snapshot()
			if err != nil {
				t.Fatalf("rq.Snapshot() error = %v, want nil", err)
			}
			before, err := rq.Snapshot()
			if err != nil {
				t.Fatalf("rq.Snapshot() error = %v, want nil", err)
			}

			// Mutate every cloned container of the first snapshot: target
			// URI, attributes map, message, envelope metadata, attempted
			// addresses, and attempt request/response envelopes.
			mut := first.TargetPlan.Groups[0][0]
			mut.URI.Addr = sip.MakeHostAddr("evil.example")
			mut.Attributes["role"] = "evil"
			first.OriginalRequest.WithMessage(func(r *sip.Request) {
				r.Method = sip.RequestMethodCancel
			})
			first.OriginalRequest.Metadata().Set("taint", true)
			first.TargetStates[0].AttemptedAddresses[0].Addr = netip.MustParseAddrPort("1.1.1.1:1")
			first.Attempts[0].Request.WithMessage(func(r *sip.Request) {
				r.Method = sip.RequestMethodBye
			})
			first.Attempts[0].Response.WithMessage(func(m *sip.Response) {
				m.Status = sip.ResponseStatusBusyEverywhere
			})

			second, err := rq.Snapshot()
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if diff := cmp.Diff(before, second, snapCmpOpts()...); diff != "" {
				t.Fatalf("snapshot isolation (-want +got): %s", diff)
			}

			// The mutation is real: it changed the first snapshot only.
			if got := first.TargetPlan.Groups[0][0].URI.Addr.Host(); got != "evil.example" {
				t.Fatalf("mutated target URI host=%q, want %q", got, "evil.example")
			}
			if got := second.TargetPlan.Groups[0][0].URI.Addr.Host(); got != targetHosts["A"] {
				t.Fatalf("second snapshot target URI host=%q, want %q", got, targetHosts["A"])
			}
		})
	})

	t.Run("delivery references serialize", func(t *testing.T) {
		t.Parallel()

		ref := proxy.DeliveryRef{Kind: "gw", Key: "eu-1"}
		data, err := json.Marshal(ref)
		if err != nil {
			t.Fatalf("json.Marshal(proxy.DeliveryRef) error = %v, want nil", err)
		}
		var decoded proxy.DeliveryRef
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("json.Unmarshal(proxy.DeliveryRef) error = %v, want nil", err)
		}
		if diff := cmp.Diff(ref, decoded); diff != "" {
			t.Fatalf("delivery ref round-trip (-want +got): %s", diff)
		}
	})

	t.Run("unsupported opaque metadata fails capture", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := &proxy.Route{
				Name:        "snap-meta",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return proxy.TargetPlan{Groups: [][]proxy.Target{
						{{ID: "A", URI: snapTargetURI(targetHosts["A"])}},
					}}, nil
				},
			}
			elm, prx, _ := newConfiguredTestProxy(t, route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".snap-meta", laddr, raddr)
			req.Metadata().Set("opaque", make(chan int))
			if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), noopNext, req); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			rq, ok := prx.LookupRequest(elm, srvKey)
			if !ok {
				t.Fatal("prx.LookupRequest(elm, ) found no owned request")
			}
			if _, err := rq.Snapshot(); !errors.Is(err, proxy.ErrSnapshotUnsupported) {
				t.Fatalf("rq.Snapshot() error = %v, want %v", err, proxy.ErrSnapshotUnsupported)
			}
		})
	})

	t.Run("custom transaction without snapshotter forwards but fails capture", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := &proxy.Route{
				Name:        "snap-nosnap",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return proxy.TargetPlan{Groups: [][]proxy.Target{
						{{ID: "A", URI: snapTargetURI(targetHosts["A"])}},
					}}, nil
				},
			}
			elm := &sip.Element{
				ServerTransactionFactory: sip.ServerTransactionFactoryFunc(
					func(req *sip.RequestEnvelope, tp sip.ServerTransport, opts ...sip.ServerTransactionOptions) (sip.ServerTransaction, error) {
						tx, err := sip.NewServerTransaction(req, tp, opts...)
						if err != nil {
							return nil, errors.Wrap(err)
						}
						return &noSnapSrvTransact{tx}, nil
					},
				),
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
			}, elm)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".snap-nosnap", laddr, raddr)
			if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), noopNext, req); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			// Ordinary forwarding through the custom transaction works: the
			// downstream branch is established and its final response crosses
			// upstream.
			outReq := testutil.WaitResult(t, tp.sendReqCalls, "outbound branch send").req
			key, err := sip.MakeClientTransactionKey(outReq)
			if err != nil {
				t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
			}
			clnTx, ok := elm.LoadClientTransaction(key)
			if !ok {
				t.Fatal("forwarding client transaction not registered")
			}
			if err := clnTx.RecvResponse(ctx, newInRes(t, outReq, sip.ResponseStatusOK)); err != nil {
				t.Fatalf("clnTx.RecvResponse(200) error = %v, want nil", err)
			}
			synctest.Wait()

			var fwd200 bool
			for _, res := range drainRess(tp) {
				if res.Status() == sip.ResponseStatusOK {
					fwd200 = true
				}
			}
			if !fwd200 {
				t.Fatal("downstream 200 was never forwarded upstream")
			}

			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			rq, ok := prx.LookupRequest(elm, srvKey)
			if !ok {
				t.Fatal("prx.LookupRequest(elm, ) found no owned request")
			}
			if _, err := rq.Snapshot(); !errors.Is(err, proxy.ErrSnapshotUnsupported) {
				t.Fatalf("rq.Snapshot() error = %v, want %v", err, proxy.ErrSnapshotUnsupported)
			}
		})
	})

	t.Run("revisions are future-only and unbind is reentrant", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := &proxy.Route{
				Name:        "snap-rev",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return proxy.TargetPlan{Groups: [][]proxy.Target{
						{{ID: "A", URI: snapTargetURI(targetHosts["A"])}},
					}}, nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			var mu sync.Mutex
			var got1 []*proxy.RequestSnapshot
			unbind1 := prx.BindSnapshotHandler(proxy.RequestSnapshotHandlerFunc(
				func(_ context.Context, snap *proxy.RequestSnapshot) {
					mu.Lock()
					got1 = append(got1, snap)
					mu.Unlock()
				},
			))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".snap-rev", laddr, raddr)
			if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), noopNext, req); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			outReq := testutil.WaitResult(t, tp.sendReqCalls, "outbound branch send").req
			key, err := sip.MakeClientTransactionKey(outReq)
			if err != nil {
				t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
			}
			clnTx, ok := elm.LoadClientTransaction(key)
			if !ok {
				t.Fatal("forwarding client transaction not registered")
			}
			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			rq, ok := prx.LookupRequest(elm, srvKey)
			if !ok {
				t.Fatal("prx.LookupRequest(elm, ) found no owned request")
			}

			mu.Lock()
			pubs := len(got1)
			mu.Unlock()
			if pubs == 0 {
				t.Fatal("no snapshot revisions published")
			}

			// A handler bound now observes only revisions after the bind.
			cur, err := rq.Snapshot()
			if err != nil {
				t.Fatalf("rq.Snapshot() error = %v, want nil", err)
			}
			var got2Mx sync.Mutex
			var got2 []*proxy.RequestSnapshot
			unbind2 := prx.BindSnapshotHandler(proxy.RequestSnapshotHandlerFunc(
				func(_ context.Context, snap *proxy.RequestSnapshot) {
					got2Mx.Lock()
					got2 = append(got2, snap)
					got2Mx.Unlock()
				},
			))

			// A self-unbinding handler proves unbind is reentrant: it removes
			// itself inside the callback and observes exactly one revision.
			var got3 atomic.Int32
			var unbind3 func()
			unbind3 = prx.BindSnapshotHandler(proxy.RequestSnapshotHandlerFunc(
				func(_ context.Context, _ *proxy.RequestSnapshot) {
					got3.Add(1)
					unbind3()
				},
			))

			if err := clnTx.RecvResponse(ctx, newInRes(t, outReq, sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("clnTx.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()

			got2Mx.Lock()
			if len(got2) == 0 {
				t.Fatal("late-bound handler observed no revision")
			} else if got2[0].Revision <= cur.Revision {
				t.Fatalf("late-bound handler first revision=%d, want > %d", got2[0].Revision, cur.Revision)
			}
			got2Mx.Unlock()
			if got := got3.Load(); got != 1 {
				t.Fatalf("self-unbinding handler deliveries=%d, want 1", got)
			}

			// Unbinding twice is idempotent; after unbind no further
			// revisions are delivered.
			unbind2()
			unbind2()
			got2Mx.Lock()
			frozen := len(got2)
			got2Mx.Unlock()

			if err := clnTx.RecvResponse(ctx, newInRes(t, outReq, sip.ResponseStatusNotFound)); err != nil {
				t.Fatalf("clnTx.RecvResponse(404) error = %v, want nil", err)
			}
			synctest.Wait()
			got2Mx.Lock()
			if got := len(got2); got != frozen {
				t.Fatalf("unbound handler deliveries=%d, want %d", got, frozen)
			}
			got2Mx.Unlock()
			if got := got3.Load(); got != 1 {
				t.Fatalf("self-unbinding handler deliveries=%d, want 1", got)
			}
			mu.Lock()
			if got := len(got1); got <= pubs {
				t.Fatalf("bound handler revisions=%d, want > %d", got, pubs)
			}
			mu.Unlock()

			unbind1()
			if unbind := prx.BindSnapshotHandler(nil); unbind == nil {
				t.Fatal("nil handler bind returned nil unbind")
			} else {
				unbind()
			}
		})
	})
}

// restoreInviteFixture is a captured in-flight INVITE fork: target A
// settled with a 404, target B still active in Calling, and target C
// unstarted and carrying a custom delivery reference. The paired
// transaction snapshots feed the dormant runtime bindings.
type restoreInviteFixture struct {
	snap       *proxy.RequestSnapshot
	srvSnap    *sip.ServerTransactionSnapshot
	foreignSrv *sip.ServerTransactionSnapshot
	clnSnap    *sip.ClientTransactionSnapshot
	keyB       sip.ClientTransactionKey
	srvKey     sip.ServerTransactionKey
	delRef     proxy.DeliveryRef
	route      *proxy.Route
	raddr      netip.AddrPort
}

// newRestoreInviteFixture admits an INVITE on a first proxy, settles
// target A, and captures the request and transaction snapshots used by
// every dormant-restore subtest.
func newRestoreInviteFixture(t *testing.T) *restoreInviteFixture {
	t.Helper()
	return newRestoreFixture(t, sip.RequestMethodInvite)
}

// newRestoreFixture is newRestoreInviteFixture parameterized by the inbound
// method so restored retransmission timers cover both the INVITE Timer A and
// the non-INVITE Timer E sends.
func newRestoreFixture(t *testing.T, method sip.RequestMethod) *restoreInviteFixture {
	t.Helper()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	targetHosts := map[string]string{"A": "10.0.0.9", "B": "10.0.0.10", "C": "10.0.0.11"}
	targetOf := func(req *sip.RequestEnvelope) string {
		u, ok := req.URI().(*sip.URI)
		if !ok {
			return ""
		}
		for id, host := range targetHosts {
			if u.Addr.Host() == host {
				return id
			}
		}
		return ""
	}
	noopNext := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
		return nil
	})
	delRef := proxy.DeliveryRef{Kind: "gw", Key: "eu-1"}

	var selCalls atomic.Int32
	route := &proxy.Route{
		Name:        "rest",
		ForwardMode: proxy.ForwardModeStateful,
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
			return proxy.TargetPlan{Groups: [][]proxy.Target{
				{
					{ID: "A", URI: snapTargetURI(targetHosts["A"])},
					{ID: "B", URI: snapTargetURI(targetHosts["B"])},
				},
				{{
					ID:       "C",
					URI:      snapTargetURI(targetHosts["C"]),
					Delivery: delRef,
				}},
			}}, nil
		},
		SelectTargets: func(context.Context, proxy.Selection) ([]string, error) {
			if selCalls.Add(1) == 1 {
				return []string{"A", "B"}, nil
			}
			// The next group never starts while a branch is active.
			return nil, nil
		},
	}
	elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
		ServedDomains: []string{"alice.voip.com"},
	})

	ctx := t.Context()
	req := newInInviteReq(t, "UDP", sip.MagicCookie+".rest-invite", laddr, raddr)
	if !method.Equal(sip.RequestMethodInvite) {
		req = markReqNonInvite(req)
	}
	if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), noopNext, req); err != nil {
		t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
	}
	synctest.Wait()

	sends := make(map[string]*sip.RequestEnvelope)
	for _, out := range drainReqs(tp) {
		if out.Method().Equal(method) {
			if id := targetOf(out); id != "" {
				sends[id] = out
			}
		}
	}
	if sends["A"] == nil || sends["B"] == nil {
		t.Fatal("outbound branch sends for targets A and B not recorded")
	}

	keyA, err := sip.MakeClientTransactionKey(sends["A"])
	if err != nil {
		t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
	}
	txA, ok := elm.LoadClientTransaction(keyA)
	if !ok {
		t.Fatal("client transaction of target A not registered")
	}
	if err := txA.RecvResponse(ctx, newInRes(t, sends["A"], sip.ResponseStatusNotFound)); err != nil {
		t.Fatalf("clnTx.RecvResponse(404) error = %v, want nil", err)
	}
	synctest.Wait()

	srvKey, err := sip.MakeServerTransactionKey(req)
	if err != nil {
		t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
	}
	rq, ok := prx.LookupRequest(elm, srvKey)
	if !ok {
		t.Fatal("prx.LookupRequest(elm, ) found no owned request")
	}
	snap, err := rq.Snapshot()
	if err != nil {
		t.Fatalf("rq.Snapshot() error = %v, want nil", err)
	}

	srvTx, ok := elm.LoadServerTransaction(srvKey)
	if !ok {
		t.Fatal("upstream transaction not registered")
	}
	keyB, err := sip.MakeClientTransactionKey(sends["B"])
	if err != nil {
		t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
	}
	txB, ok := elm.LoadClientTransaction(keyB)
	if !ok {
		t.Fatal("client transaction of target B not registered")
	}

	// A second request provides a genuinely different dormant upstream
	// transaction for the conflicting-binding check. Its branch sends
	// only pollute the stub channel and are drained away.
	foreign := newInInviteReq(t, "UDP", sip.MagicCookie+".rest-foreign", laddr, raddr)
	if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), noopNext, foreign); err != nil {
		t.Fatalf("prx.InterceptInboundRequest(foreign) error = %v, want nil", err)
	}
	synctest.Wait()
	drainReqs(tp)
	foreignKey, err := sip.MakeServerTransactionKey(foreign)
	if err != nil {
		t.Fatalf("sip.MakeServerTransactionKey(foreign) error = %v, want nil", err)
	}
	foreignTx, ok := elm.LoadServerTransaction(foreignKey)
	if !ok {
		t.Fatal("foreign upstream transaction not registered")
	}

	srvSnapper, ok := srvTx.(sip.ServerTransactionSnapshotter)
	if !ok {
		t.Fatal("upstream transaction does not implement sip.ServerTransactionSnapshotter")
	}
	foreignSnapper, ok := foreignTx.(sip.ServerTransactionSnapshotter)
	if !ok {
		t.Fatal("foreign upstream transaction does not implement sip.ServerTransactionSnapshotter")
	}
	clnSnapper, ok := txB.(sip.ClientTransactionSnapshotter)
	if !ok {
		t.Fatal("client transaction of target B does not implement sip.ClientTransactionSnapshotter")
	}

	return &restoreInviteFixture{
		snap:       snap,
		srvSnap:    mustSrvTxSnapshot(t, srvSnapper),
		foreignSrv: mustSrvTxSnapshot(t, foreignSnapper),
		clnSnap:    mustClnTxSnapshot(t, clnSnapper),
		keyB:       keyB,
		srvKey:     srvKey,
		delRef:     delRef,
		route:      route,
		raddr:      raddr,
	}
}

// dormantBindings builds fresh dormant runtime bindings of the fixture
// against the restore-side element and transport.
func (f *restoreInviteFixture) dormantBindings(
	tb testing.TB,
	elm *sip.Element,
	tp *stubTransp,
) proxy.RestoreBindings {
	tb.Helper()

	srvTx, err := sip.RestoreServerTransaction(f.srvSnap, tp)
	if err != nil {
		tb.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
	}
	clnTx, err := sip.RestoreClientTransaction(f.clnSnap, tp)
	if err != nil {
		tb.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
	}
	return proxy.RestoreBindings{
		Element:            elm,
		ServerTransaction:  srvTx,
		ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{f.keyB: clnTx},
		Deliveries: map[proxy.DeliveryRef]proxy.BoundDelivery{
			f.delRef: {Transport: tp, RemoteAddr: f.raddr},
		},
	}
}

func TestProxy_RestoreRequest_RestoredRetransmissionMetadata(t *testing.T) {
	t.Parallel()

	t.Run("invite timer A", func(t *testing.T) {
		t.Parallel()
		testRestoredRetransmissionMetadata(t, sip.RequestMethodInvite)
	})
	t.Run("non-invite timer E", func(t *testing.T) {
		t.Parallel()
		testRestoredRetransmissionMetadata(t, sip.RequestMethodInfo)
	})
}

// testRestoredRetransmissionMetadata verifies that a client transaction
// supplied dormant at restore retransmits its own request envelope with the
// rebuilt attempt correlation: the ownership token installed at Start lets
// the outbound interceptor replace the stripped checkpoint stamp, while the
// transaction key, provenance, and attempt accounting stay unchanged.
func testRestoredRetransmissionMetadata(t *testing.T, method sip.RequestMethod) {
	t.Helper()

	synctest.Test(t, func(t *testing.T) {
		f := newRestoreFixture(t, method)
		snap := f.snap.Clone()
		snap.OriginalRequest.Metadata().Delete(proxy.MetadataKey)
		snap.WorkingRequest.Metadata().Delete(proxy.MetadataKey)
		for i := range snap.Attempts {
			if snap.Attempts[i].Request != nil {
				snap.Attempts[i].Request.Metadata().Delete(proxy.MetadataKey)
			}
			if snap.Attempts[i].Response != nil {
				snap.Attempts[i].Response.Metadata().Delete(proxy.MetadataKey)
			}
		}
		for _, res := range snap.Candidates {
			res.Metadata().Delete(proxy.MetadataKey)
		}
		f.clnSnap.Request.Metadata().Delete(proxy.MetadataKey)
		rec := &evtRecorder{}
		elm, prx, tp := newConfiguredTestProxy(t, f.route, proxy.Options{
			ServedDomains: []string{"alice.voip.com"},
		})
		prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))
		rq, err := prx.RestoreRequest(t.Context(), snap, f.dormantBindings(t, elm, tp))
		if err != nil {
			t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
		}
		rebuilt, err := rq.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		attB := attemptOfTarget(rebuilt, "B")
		if attB == nil || attB.Request == nil {
			t.Fatal("rebuilt snapshot has no target B attempt request")
		}
		want, ok := proxy.MetadataFrom(attB.Request.Metadata())
		if !ok {
			t.Fatal("rebuilt snapshot has no metadata")
		}
		if want.RequestID != snap.RequestID || want.TargetID != "B" || want.AttemptID == "" ||
			want.RouteName != "rest" || want.Mode != proxy.ForwardModeStateful ||
			want.Stage != proxy.StageForward {
			t.Fatalf("rebuilt forward stamp = %+v, want saved request ID %q, target B, nonempty attempt ID, route rest, stateful, stage forward", want, snap.RequestID)
		}
		if !attB.Request.Forwarded() || attB.Request.ForwardedViaBranch() == "" {
			t.Fatalf("rebuilt attempt request forwarded=(%v, %q), want a marked forwarded request", attB.Request.Forwarded(), attB.Request.ForwardedViaBranch())
		}
		if got := len(rec.get()); got != 0 {
			t.Fatalf("events while dormant = %d, want 0", got)
		}
		if got := len(drainReqs(tp)); got != 0 {
			t.Fatalf("sends while dormant = %d, want 0", got)
		}

		if err := rq.Start(t.Context()); err != nil {
			t.Fatalf("rq.Start() error = %v, want nil", err)
		}
		synctest.Wait()
		before := rec.get()
		time.Sleep(time.Second)
		synctest.Wait()

		var seen int
		for _, req := range drainReqs(tp) {
			if !req.Method().Equal(method) {
				continue
			}
			seen++
			if key, keyErr := sip.MakeClientTransactionKey(req); keyErr != nil || !key.Equal(f.keyB) {
				t.Errorf("retransmission key = (%v, %v), want the restored attempt transaction key %v", key, keyErr, f.keyB)
			}
			if !req.Forwarded() || req.ForwardedViaBranch() != attB.Request.ForwardedViaBranch() {
				t.Errorf("retransmission forwarded=(%v, %q), want marked with the attempt branch %q", req.Forwarded(), req.ForwardedViaBranch(), attB.Request.ForwardedViaBranch())
			}
			if got, want := req.Render(), attB.Request.Render(); got != want {
				t.Errorf("retransmission render = %q, want the attempt request wire form %q", got, want)
			}
			got, ok := proxy.MetadataFrom(req.Metadata())
			if !ok {
				t.Error("retransmission carries no proxy metadata, want the rebuilt forward stamp")
				continue
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("retransmission metadata diff (-want +got):\n%s", diff)
			}
		}
		if seen == 0 {
			t.Fatal("no restored retransmission recorded")
		}

		after := rec.get()
		if len(after) != len(before) {
			t.Errorf("events after retransmission = %d, want unchanged %d", len(after), len(before))
		}
		if len(before) > 0 && len(after) >= len(before) &&
			after[len(before)-1].Sequence != before[len(before)-1].Sequence {
			t.Errorf("event sequence after retransmission shifted to %d, want unchanged %d",
				after[len(before)-1].Sequence, before[len(before)-1].Sequence)
		}

		snapAfter, err := rq.Snapshot()
		if err != nil {
			t.Fatalf("post-start rq.Snapshot() error = %v, want nil", err)
		}
		if len(snapAfter.Attempts) != len(rebuilt.Attempts) {
			t.Errorf("attempts after retransmission = %d, want unchanged %d (no new logical attempt)", len(snapAfter.Attempts), len(rebuilt.Attempts))
		}
	})
}

func TestProxy_RestoreDormant(t *testing.T) {
	t.Parallel()

	noopNext := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
		return nil
	})

	t.Run("valid restore installs dormant work with zero effects", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)
			elm, prx, tp := newConfiguredTestProxy(t, f.route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			var callbacks atomic.Int32
			unbind := prx.BindSnapshotHandler(proxy.RequestSnapshotHandlerFunc(
				func(context.Context, *proxy.RequestSnapshot) { callbacks.Add(1) },
			))
			defer unbind()

			rq, err := prx.RestoreRequest(sip.ContextWithElement(ctx, elm), f.snap, f.dormantBindings(t, elm, tp))
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}
			if rq == nil {
				t.Fatal("prx.RestoreRequest() returned no request")
			}
			if got, ok := prx.LookupRequest(elm, f.srvKey); !ok || got != rq {
				t.Fatal("restored request not indexed by its upstream key")
			}
			synctest.Wait()

			if sends := len(drainReqs(tp)); sends != 0 {
				t.Fatalf("requests sent while dormant=%d, want 0", sends)
			}
			if sends := len(drainRess(tp)); sends != 0 {
				t.Fatalf("responses sent while dormant=%d, want 0", sends)
			}
			if got := callbacks.Load(); got != 0 {
				t.Fatalf("snapshot callbacks while dormant=%d, want 0", got)
			}

			// A dormant restoration stays inspectable through the ordinary
			// request snapshot contract.
			rsnap, err := rq.Snapshot()
			if err != nil {
				t.Fatalf("rq.Snapshot() error = %v, want nil", err)
			}
			attB := attemptOfTarget(rsnap, "B")
			if attB == nil || attB.Completed || attB.State != sip.TransactionStateCalling {
				t.Fatalf("restored attempt B=%+v, want an active calling attempt", attB)
			}
			stC := targetStateOf(rsnap, "C")
			if stC == nil || stC.Started || stC.Completed {
				t.Fatalf("restored target C=%+v, want unstarted", stC)
			}
		})
	})

	t.Run("incompatible snapshots and bindings reject before install", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)
			var resolverCalls atomic.Int32
			elm, prx, tp := newConfiguredTestProxy(t, f.route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
				ResolveDelivery: func(context.Context, proxy.DeliveryRef) (proxy.BoundDelivery, error) {
					resolverCalls.Add(1)
					return proxy.BoundDelivery{}, errors.ErrorWrap("unexpected resolver call")
				},
			})

			for _, tt := range []struct {
				name  string
				snap  func() *proxy.RequestSnapshot
				binds func() proxy.RestoreBindings
			}{
				{
					name: "unsupported version",
					snap: func() *proxy.RequestSnapshot {
						s := f.snap.Clone()
						s.Version = 99
						return s
					},
				},
				{
					name: "unknown route",
					snap: func() *proxy.RequestSnapshot {
						s := f.snap.Clone()
						s.RouteName = "ghost"
						return s
					},
				},
				{
					name: "mode mismatch",
					snap: func() *proxy.RequestSnapshot {
						s := f.snap.Clone()
						s.Mode = proxy.ForwardModeStateless
						return s
					},
				},
				{
					name: "duplicate target progress",
					snap: func() *proxy.RequestSnapshot {
						s := f.snap.Clone()
						s.TargetStates = append(s.TargetStates, s.TargetStates[0])
						return s
					},
				},
				{
					name: "duplicate plan target id",
					snap: func() *proxy.RequestSnapshot {
						s := f.snap.Clone()
						s.TargetPlan.Groups[0] = append(s.TargetPlan.Groups[0], s.TargetPlan.Groups[0][0])
						return s
					},
				},
				{
					name: "invalid deadline on settled attempt",
					snap: func() *proxy.RequestSnapshot {
						s := f.snap.Clone()
						attA := attemptOfTarget(s, "A")
						attA.TimerCDeadline = s.Time
						return s
					},
				},
				{
					name: "missing upstream binding",
					binds: func() proxy.RestoreBindings {
						b := f.dormantBindings(t, elm, tp)
						b.ServerTransaction = nil
						return b
					},
				},
				{
					name: "missing client binding",
					binds: func() proxy.RestoreBindings {
						b := f.dormantBindings(t, elm, tp)
						delete(b.ClientTransactions, f.keyB)
						return b
					},
				},
				{
					name: "conflicting upstream request",
					binds: func() proxy.RestoreBindings {
						b := f.dormantBindings(t, elm, tp)
						frnTx, err := sip.RestoreServerTransaction(f.foreignSrv, tp)
						if err != nil {
							t.Fatalf("sip.RestoreServerTransaction(foreign) error = %v, want nil", err)
						}
						b.ServerTransaction = frnTx
						return b
					},
				},
				{
					name: "client binding key mismatch",
					binds: func() proxy.RestoreBindings {
						b := f.dormantBindings(t, elm, tp)
						attA := attemptOfTarget(f.snap, "A")
						b.ClientTransactions[attA.Key] = b.ClientTransactions[f.keyB]
						return b
					},
				},
				{
					name: "unknown client binding",
					binds: func() proxy.RestoreBindings {
						b := f.dormantBindings(t, elm, tp)
						b.ClientTransactions[sip.ClientTransactionKey{
							Branch: sip.MagicCookie + ".rest-unknown", Method: "INVITE",
						}] = b.ClientTransactions[f.keyB]
						return b
					},
				},
				{
					name: "cancelled outcome without intent",
					snap: func() *proxy.RequestSnapshot {
						s := f.snap.Clone()
						s.Completed = true
						s.Outcome = proxy.OutcomeCancelled
						return s
					},
				},
				{
					name: "forwarded final without candidate",
					snap: func() *proxy.RequestSnapshot {
						s := f.snap.Clone()
						s.FinalForwarded = true
						s.Candidates = nil
						return s
					},
				},
				{
					name: "final status mismatch",
					snap: func() *proxy.RequestSnapshot {
						s := f.snap.Clone()
						s.Completed = true
						s.Outcome = proxy.OutcomeResponse
						s.FinalStatus = sip.ResponseStatusOK
						return s
					},
				},
				{
					name: "missing required flow",
					binds: func() proxy.RestoreBindings {
						b := f.dormantBindings(t, elm, tp)
						b.Deliveries = nil
						return b
					},
				},
			} {
				snap := f.snap
				if tt.snap != nil {
					snap = tt.snap()
				}
				bindings := f.dormantBindings(t, elm, tp)
				if tt.binds != nil {
					bindings = tt.binds()
				}
				if _, err := prx.RestoreRequest(sip.ContextWithElement(ctx, elm), snap, bindings); err == nil {
					t.Fatalf("incompatible bindings accepted: %s", tt.name)
				}
			}

			// No rejection ever resolved a replacement flow or sent
			// traffic: validation fails before any install.
			if got := resolverCalls.Load(); got != 0 {
				t.Fatalf("opts.ResolveDelivery calls=%d, want 0", got)
			}
			synctest.Wait()
			if sends := len(drainReqs(tp)); sends != 0 {
				t.Fatalf("requests sent by rejected restores=%d, want 0", sends)
			}
		})
	})

	t.Run("saved dormant non-INVITE mode wins over creation defaults", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			laddr := netip.MustParseAddrPort("192.168.1.100:5060")
			raddr := netip.MustParseAddrPort("10.0.0.5:5060")

			route := &proxy.Route{
				Name:        "rest-ni",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return proxy.TargetPlan{Groups: [][]proxy.Target{
						{{ID: "A", URI: snapTargetURI("10.0.0.9")}},
					}}, nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".rest-info", laddr, raddr)
			if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), noopNext, req); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			srvTx, ok := elm.LoadServerTransaction(srvKey)
			if !ok {
				t.Fatal("upstream transaction not registered")
			}
			// Move the transaction to Proceeding so an RFC4320-mode
			// mutation restores without its pending Trying deadline.
			if err := srvTx.SendResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("srvTx.SendResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()

			rq, ok := prx.LookupRequest(elm, srvKey)
			if !ok {
				t.Fatal("prx.LookupRequest(elm, ) found no owned request")
			}
			snap, err := rq.Snapshot()
			if err != nil {
				t.Fatalf("rq.Snapshot() error = %v, want nil", err)
			}
			if snap.NonInviteServerTransactionBehavior != sip.NonInviteServerTransactionBehaviorLegacy {
				t.Fatalf("saved non-INVITE behavior=%v, want Legacy", snap.NonInviteServerTransactionBehavior)
			}
			srvSnapper, ok := srvTx.(sip.ServerTransactionSnapshotter)
			if !ok {
				t.Fatal("upstream transaction does not implement sip.ServerTransactionSnapshotter")
			}
			srvSnap := mustSrvTxSnapshot(t, srvSnapper)

			outReq := testutil.WaitResult(t, tp.sendReqCalls, "outbound branch send").req
			clnKey, err := sip.MakeClientTransactionKey(outReq)
			if err != nil {
				t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
			}
			clnTx, ok := elm.LoadClientTransaction(clnKey)
			if !ok {
				t.Fatal("forwarding client transaction not registered")
			}
			clnSnapper, ok := clnTx.(sip.ClientTransactionSnapshotter)
			if !ok {
				t.Fatal("forwarding client transaction does not implement sip.ClientTransactionSnapshotter")
			}
			clnSnap := mustClnTxSnapshot(t, clnSnapper)

			// The restore side enables RFC4320 for NEW transactions: the
			// saved Legacy checkpoint still binds, the dormant binding's
			// own saved mode wins over creation defaults.
			rfcRoute := &proxy.Route{
				Name:        "rest-ni",
				ForwardMode: proxy.ForwardModeStateful,
				ServerTransactionOptions: sip.ServerTransactionOptions{
					NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320,
				},
			}
			elm2, prx2, tp2 := newConfiguredTestProxy(t, rfcRoute, proxy.Options{})

			legacyTx, err := sip.RestoreServerTransaction(srvSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
			}
			prvd, ok := legacyTx.(sip.NonInviteServerTransactionBehaviorProvider)
			if !ok {
				t.Fatal("dormant non-INVITE transaction does not implement sip.NonInviteServerTransactionBehaviorProvider")
			}
			if got := prvd.NonInviteServerTransactionBehavior(); got != sip.NonInviteServerTransactionBehaviorLegacy {
				t.Fatalf("dormant binding mode=%v, want Legacy", got)
			}
			cln2, err := sip.RestoreClientTransaction(clnSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
			}
			bindings := proxy.RestoreBindings{
				Element:            elm2,
				ServerTransaction:  legacyTx,
				ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{clnKey: cln2},
			}
			if _, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, bindings); err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil (saved mode wins)", err)
			}

			// A binding whose dormant mode disagrees with the checkpoint
			// rejects before activation.
			mut := srvSnap.Clone()
			mut.NonInviteServerTransactionBehavior = sip.NonInviteServerTransactionBehaviorRFC4320
			rfcTx, err := sip.RestoreServerTransaction(mut, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreServerTransaction(mutated) error = %v, want nil", err)
			}
			bindings.ServerTransaction = rfcTx
			if _, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, bindings); !errors.Is(err, proxy.ErrInvalidSnapshot) {
				t.Fatalf("prx.RestoreRequest() error = %v, want %v", err, proxy.ErrInvalidSnapshot)
			}

			// A binding that cannot report its mode fails explicitly
			// unsupported.
			bindings.ServerTransaction = &noSnapSrvTransact{legacyTx}
			if _, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, bindings); !errors.Is(err, proxy.ErrSnapshotUnsupported) {
				t.Fatalf("prx.RestoreRequest() error = %v, want %v", err, proxy.ErrSnapshotUnsupported)
			}

			// Cancellation is INVITE-only: it can never land on a
			// non-INVITE checkpoint.
			bad := snap.Clone()
			bad.Cancelled = true
			bindings.ServerTransaction = legacyTx
			if _, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), bad, bindings); err == nil {
				t.Fatal("cancelled non-INVITE checkpoint accepted")
			}
		})
	})

	t.Run("failed install rolls back only its own staged registrations", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)
			elm, prx, tp := newConfiguredTestProxy(t, f.route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
			})

			var callbacks atomic.Int32
			unbind := prx.BindSnapshotHandler(proxy.RequestSnapshotHandlerFunc(
				func(context.Context, *proxy.RequestSnapshot) { callbacks.Add(1) },
			))
			defer unbind()

			rq1, err := prx.RestoreRequest(sip.ContextWithElement(ctx, elm), f.snap, f.dormantBindings(t, elm, tp))
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}

			// A second restore of the same upstream identity stages its
			// own bindings, then fails on the index commit: only the
			// staged registrations roll back, the existing entry and the
			// borrowed transactions are untouched.
			bindings2 := f.dormantBindings(t, elm, tp)
			srv2 := bindings2.ServerTransaction
			if _, err := prx.RestoreRequest(sip.ContextWithElement(ctx, elm), f.snap, bindings2); err == nil {
				t.Fatal("duplicate upstream restore accepted")
			}
			if got, ok := prx.LookupRequest(elm, f.srvKey); !ok || got != rq1 {
				t.Fatal("existing request index entry disturbed by a failed install")
			}
			if srv2.Started() {
				t.Fatal("borrowed transaction started by a failed install")
			}
			if got := srv2.State(); got == sip.TransactionStateTerminated {
				t.Fatal("borrowed transaction terminated by a failed install")
			}
			synctest.Wait()
			if sends := len(drainReqs(tp)) + len(drainRess(tp)); sends != 0 {
				t.Fatalf("sends by a failed install=%d, want 0", sends)
			}
			if got := callbacks.Load(); got != 0 {
				t.Fatalf("snapshot callbacks after a failed install=%d, want 0", got)
			}
		})
	})
}

// TestProxy_RestoreMissingFlow is the focused companion of the dormant
// restore validation cases: an unfinished target whose required delivery
// reference has no supplied binding fails the restore before anything
// resolves or dials a replacement flow.
func TestProxy_RestoreMissingFlow(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		f := newRestoreInviteFixture(t)

		var dialCalls atomic.Int32
		elm, prx, tp := newConfiguredTestProxy(t, f.route, proxy.Options{
			ServedDomains: []string{"alice.voip.com"},
			ResolveDelivery: func(context.Context, proxy.DeliveryRef) (proxy.BoundDelivery, error) {
				dialCalls.Add(1)
				return proxy.BoundDelivery{}, errors.ErrorWrap("unexpected replacement dial")
			},
		})

		bindingsMissingFlow := f.dormantBindings(t, elm, tp)
		delete(bindingsMissingFlow.Deliveries, f.delRef)

		_, err := prx.RestoreRequest(sip.ContextWithElement(ctx, elm), f.snap, bindingsMissingFlow)
		if err == nil {
			t.Fatal("missing bound flow accepted")
		}
		if !errors.Is(err, proxy.ErrInvalidSnapshot) {
			t.Fatalf("prx.RestoreRequest() error = %v, want %v", err, proxy.ErrInvalidSnapshot)
		}
		if got := dialCalls.Load(); got != 0 {
			t.Fatalf("dial calls=%d, want 0", got)
		}
		synctest.Wait()
		if sends := len(drainReqs(tp)) + len(drainRess(tp)); sends != 0 {
			t.Fatalf("sends by a missing-flow restore=%d, want 0", sends)
		}
	})
}

func TestProxy_RestoreCachedResponse(t *testing.T) {
	t.Parallel()

	noopNext := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil })

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		laddr := netip.MustParseAddrPort("192.168.1.100:5060")
		raddr := netip.MustParseAddrPort("10.0.0.5:5060")
		route := &proxy.Route{
			Name:        "cache",
			ForwardMode: proxy.ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{
					{{
						ID:  "A",
						URI: snapTargetURI("10.0.0.9"),
					}},
				}}, nil
			},
		}
		opts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}
		elm1, prx1, tp1 := newConfiguredTestProxy(t, route, opts)

		// The inbound request carries no From tag: the forwarded copy must
		// keep it verbatim through the checkpoint round-trip.
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".cache-rest", laddr, raddr)
		req.WithMessage(func(r *sip.Request) {
			if from, ok := r.Headers.From(); ok && from != nil {
				from.Params.Delete("tag")
			}
		})
		if err := prx1.InterceptInboundRequest(sip.ContextWithElement(ctx, elm1), noopNext, req); err != nil {
			t.Fatalf("prx1.InterceptInboundRequest() error = %v, want nil", err)
		}
		synctest.Wait()

		sends := drainReqs(tp1)
		if got := len(sends); got != 1 {
			t.Fatalf("initial outbound sends=%d, want 1", got)
		}
		outReq := sends[0]
		clnKey, err := sip.MakeClientTransactionKey(outReq)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx1, ok := elm1.LoadClientTransaction(clnKey)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}

		// A tagless downstream provisional is forwarded verbatim upstream:
		// NewResponse invents a To tag by default, so the checkpoint
		// response strips it to record a genuinely tagless provisional.
		ringRes := newInRes(t, outReq, sip.ResponseStatusRinging)
		ringRes.WithMessage(func(res *sip.Response) {
			if to, ok := res.Headers.To(); ok && to != nil {
				to.Params.Delete("tag")
			}
		})
		if err := clnTx1.RecvResponse(ctx, ringRes); err != nil {
			t.Fatalf("clnTx1.RecvResponse(180) error = %v, want nil", err)
		}
		synctest.Wait()
		fwd := drainRess(tp1)
		if got := len(fwd); got != 1 {
			t.Fatalf("forwarded provisionals upstream=%d, want 1", got)
		}
		fwd[0].WithMessage(func(res *sip.Response) {
			to, _ := res.Headers.To()
			if tag, _ := to.Tag(); tag != "" {
				t.Fatalf("forwarded 180 To tag=%q, want tagless", tag)
			}
		})

		srvKey, err := sip.MakeServerTransactionKey(req)
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
		if !srvSnap.PreserveResponseToTag {
			t.Fatal("server checkpoint lost srvSnap.PreserveResponseToTag for the forwarded tagless 180")
		}
		clnSnapper, ok := clnTx1.(sip.ClientTransactionSnapshotter)
		if !ok {
			t.Fatal("forwarding client transaction does not implement sip.ClientTransactionSnapshotter")
		}
		clnSnap := mustClnTxSnapshot(t, clnSnapper)

		// JSON round-trip every application-coordinated checkpoint.
		var (
			rqJSON  proxy.RequestSnapshot
			srvJSON sip.ServerTransactionSnapshot
			clnJSON sip.ClientTransactionSnapshot
		)
		raw, err := json.Marshal(snap)
		if err != nil {
			t.Fatalf("json.Marshal(snap) error = %v, want nil", err)
		}
		if err := json.Unmarshal(raw, &rqJSON); err != nil {
			t.Fatalf("json.Unmarshal(proxy.RequestSnapshot) error = %v, want nil", err)
		}
		raw, err = json.Marshal(srvSnap)
		if err != nil {
			t.Fatalf("json.Marshal(srvSnap) error = %v, want nil", err)
		}
		if err := json.Unmarshal(raw, &srvJSON); err != nil {
			t.Fatalf("json.Unmarshal(sip.ServerTransactionSnapshot) error = %v, want nil", err)
		}
		raw, err = json.Marshal(clnSnap)
		if err != nil {
			t.Fatalf("json.Marshal(clnSnap) error = %v, want nil", err)
		}
		if err := json.Unmarshal(raw, &clnJSON); err != nil {
			t.Fatalf("json.Unmarshal(sip.ClientTransactionSnapshot) error = %v, want nil", err)
		}
		if !srvJSON.PreserveResponseToTag {
			t.Fatal("JSON round-trip lost srvJSON.PreserveResponseToTag")
		}

		elm2, prx2, tp2 := newConfiguredTestProxy(t, route, opts)
		srvTx2, err := sip.RestoreServerTransaction(&srvJSON, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		clnTx2, err := sip.RestoreClientTransaction(&clnJSON, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
		}

		// The forwarded marker survived the JSON path: the endpoint
		// convenience must not manufacture a From tag on the restored
		// outbound request retransmission.
		restoredReq := clnTx2.Request()
		restoredReq.EnsureFromTag()
		restoredReq.WithMessage(func(r *sip.Request) {
			from, _ := r.Headers.From()
			if tag, _ := from.Tag(); tag != "" {
				t.Fatalf("restored forwarded request gained invented From tag %q", tag)
			}
		})

		rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), &rqJSON, proxy.RestoreBindings{
			Element:            elm2,
			ServerTransaction:  srvTx2,
			ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{clnKey: clnTx2},
		})
		if err != nil {
			t.Fatalf("prx2.RestoreRequest() error = %v, want nil", err)
		}
		if err := rq2.Start(ctx); err != nil {
			t.Fatalf("rq2.Start() error = %v, want nil", err)
		}
		synctest.Wait()
		if got := len(drainReqs(tp2)) + len(drainRess(tp2)); got != 0 {
			t.Fatalf("dormant send count=%d, want 0", got)
		}

		// An upstream INVITE retransmission receives exactly the cached
		// tagless 180: no To tag may be invented for the re-sent copy.
		dup, ok := rqJSON.OriginalRequest.Clone().(*sip.RequestEnvelope)
		if !ok {
			t.Fatal("rqJSON.OriginalRequest.Clone() is not a *sip.RequestEnvelope")
		}
		if err := srvTx2.RecvRequest(ctx, dup); err != nil {
			t.Fatalf("srvTx2.RecvRequest() error = %v, want nil", err)
		}
		synctest.Wait()
		resent := drainRess(tp2)
		if got := len(resent); got != 1 {
			t.Fatalf("cached retransmissions=%d, want 1", got)
		}
		resent[0].WithMessage(func(res *sip.Response) {
			if res.Status != sip.ResponseStatusRinging {
				t.Fatalf("cached response status=%v, want 180", res.Status)
			}
			to, _ := res.Headers.To()
			if tag, _ := to.Tag(); tag != "" {
				t.Fatalf("re-sent 180 To tag=%q, want tagless", tag)
			}
		})
		if got := len(drainReqs(tp2)); got != 0 {
			t.Fatalf("outbound request sends=%d, want 0", got)
		}

		// Settling the restored attempt retires the rebuilt lazy address
		// runner together with the target.
		if err := clnTx2.RecvResponse(ctx, newInRes(t, clnTx2.Request(), sip.ResponseStatusBusyHere)); err != nil {
			t.Fatalf("clnTx2.RecvResponse(486) error = %v, want nil", err)
		}
		synctest.Wait()
		drainRess(tp2)
	})
}

// assertReqRouteHop reports a test failure when the envelope carries no
// loose Route hop whose URI names the given host.
func assertReqRouteHop(tb testing.TB, env *sip.RequestEnvelope, host string) {
	tb.Helper()
	var found bool
	env.WithMessage(func(r *sip.Request) {
		for hop := range r.Headers.Routes() {
			u, ok := hop.URI.(*sip.URI)
			if ok && u.Addr.Host() == host && u.Params.Has("lr") {
				found = true
			}
		}
	})
	if !found {
		tb.Fatalf("request carries no loose Route hop toward %s", host)
	}
}

func TestProxy_promotion_snapshot_continuation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		f := newPromotionFixture()
		loc := statelessFixedLocator{addr: netip.MustParseAddrPort("198.51.100.77:5060")}
		elm1, prx1, tp1 := newConfiguredTestProxy(t, f.route, f.opts, &sip.Element{ServerLocator: loc})
		in := newInInviteReq(t, "UDP", sip.MagicCookie+".promotion-snapshot",
			netip.MustParseAddrPort("192.168.1.100:5060"), netip.MustParseAddrPort("10.0.0.5:5060"))
		original := in.Message().Render()
		fwdIntercept(t, elm1, prx1, in)
		synctest.Wait()
		sends := drainReqs(tp1)
		if len(sends) != 1 {
			t.Fatalf("initial sends = %d, want 1", len(sends))
		}
		srvKey, err := sip.MakeServerTransactionKey(in)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		rq1, ok := prx1.LookupRequest(elm1, srvKey)
		if !ok {
			t.Fatal("promoted request not indexed by original key")
		}
		snap, err := rq1.Snapshot()
		if err != nil {
			t.Fatalf("rq1.Snapshot() error = %v, want nil", err)
		}
		if snap.Version != 1 || snap.Mode != proxy.ForwardModeStateful || f.route.ForwardMode != proxy.ForwardModeStateless {
			t.Fatalf("snapshot version/mode = %d/%v, configured mode = %v, want 1/stateful/stateless",
				snap.Version, snap.Mode, f.route.ForwardMode)
		}
		if got := []int32{f.authorized.Load(), f.preprocessed.Load(), f.resolved.Load()}; got[0] != 1 || got[1] != 1 || got[2] != 1 {
			t.Errorf("route hook calls = %v, want [1 1 1]", got)
		}
		a, b := targetStateOf(snap, "A"), targetStateOf(snap, "B")
		if a == nil || !a.Started || a.Completed || b == nil || b.Started {
			t.Fatalf("checkpoint progress A=%+v B=%+v, want live A and unstarted B from accepted plan", a, b)
		}
		assertPromotionWorking(t, snap.WorkingRequest)
		if snap.OriginalRequest.Message().Render() != original || snap.OriginalRequest == snap.WorkingRequest {
			t.Fatal("snapshot did not independently retain original and working requests")
		}
		srvTx1, ok := elm1.LoadServerTransaction(srvKey)
		if !ok {
			t.Fatal("server transaction missing")
		}
		srvSnap := mustSrvTxSnapshot(t, srvTx1.(sip.ServerTransactionSnapshotter))
		clnKey, err := sip.MakeClientTransactionKey(sends[0])
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx1, ok := elm1.LoadClientTransaction(clnKey)
		if !ok {
			t.Fatal("client transaction missing")
		}
		clnSnap := mustClnTxSnapshot(t, clnTx1.(sip.ClientTransactionSnapshotter))
		// Round-trip all three checkpoints, not just the orchestration value.
		wire := struct {
			Request *proxy.RequestSnapshot         `json:"request"`
			Server  *sip.ServerTransactionSnapshot `json:"server"`
			Client  *sip.ClientTransactionSnapshot `json:"client"`
		}{snap, srvSnap, clnSnap}
		raw, err := json.Marshal(wire)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v, want nil", err)
		}
		wire.Request, wire.Server, wire.Client = nil, nil, nil
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatalf("json.Unmarshal() error = %v, want nil", err)
		}
		saved := wire.Request
		elm2, prx2, tp2 := newConfiguredTestProxy(t, f.route, f.opts, &sip.Element{ServerLocator: loc})
		rec := &evtRecorder{}
		prx2.BindEventHandler(proxy.EventHandlerFunc(rec.record))
		srvTx2, err := sip.RestoreServerTransaction(wire.Server, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		clnTx2, err := sip.RestoreClientTransaction(wire.Client, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
		}
		rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), saved, proxy.RestoreBindings{
			Element:            elm2,
			ServerTransaction:  srvTx2,
			ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{clnKey: clnTx2},
		})
		if err != nil {
			t.Fatalf("prx2.RestoreRequest() error = %v, want nil", err)
		}
		if got, ok := prx2.LookupRequest(elm2, srvKey); !ok || got != rq2 {
			t.Fatal("restored promotion lost original upstream key")
		}
		// Mutating the supplied checkpoint must not affect either adopted
		// request, its sibling, or the saved complete plan.
		saved.OriginalRequest.WithMessage(func(r *sip.Request) { r.URI = snapTargetURI("changed-original") })
		if saved.WorkingRequest.Message().Render() != snap.WorkingRequest.Message().Render() {
			t.Fatal("snapshot working request aliases its original sibling")
		}
		saved.WorkingRequest.WithMessage(func(r *sip.Request) { r.Headers.PopFirstRoute() })
		saved.TargetPlan.Groups[1][0].Attributes["role"] = "mutated"
		synctest.Wait()
		if len(rec.get()) != 0 || len(drainReqs(tp2))+len(drainRess(tp2)) != 0 {
			t.Fatal("dormant restore emitted effects or historical observations")
		}
		if err := rq2.Start(ctx); err != nil {
			t.Fatalf("rq2.Start() error = %v, want nil", err)
		}
		synctest.Wait()
		if len(rec.get()) != 0 || len(drainReqs(tp2))+len(drainRess(tp2)) != 0 {
			t.Fatal("activation replayed historical sends or observations")
		}
		if err := clnTx2.RecvResponse(ctx, newInRes(t, clnTx2.Request(), sip.ResponseStatusBusyHere)); err != nil {
			t.Fatalf("clnTx2.RecvResponse() error = %v, want nil", err)
		}
		synctest.Wait()
		var failover []*sip.RequestEnvelope
		for _, out := range drainReqs(tp2) {
			if out.Method().Equal(sip.RequestMethodInvite) {
				failover = append(failover, out)
			}
		}
		if len(failover) != 1 || !failover[0].URI().Equal(f.plan.Groups[1][0].URI) {
			t.Fatalf("restored group B sends = %v, want saved backup URI", failover)
		}
		assertPromotionWorking(t, failover[0])
		after, err := rq2.Snapshot()
		if err != nil {
			t.Fatalf("rq2.Snapshot() error = %v, want nil", err)
		}
		if after.OriginalRequest.Message().Render() != original || after.Mode != proxy.ForwardModeStateful {
			t.Fatal("restored continuation lost original request or promoted mode")
		}
		if diff := cmp.Diff(f.plan, after.TargetPlan); diff != "" {
			t.Fatalf("restored accepted plan (-want +got): %s", diff)
		}
		if got := []int32{f.authorized.Load(), f.preprocessed.Load(), f.resolved.Load()}; got[0] != 1 || got[1] != 1 || got[2] != 1 {
			t.Fatalf("route hook calls after restore = %v, want [1 1 1]", got)
		}
		events := rec.get()
		if len(events) == 0 {
			t.Fatal("restored group B emitted no new observations")
		}
		sequence := snap.Sequence
		for _, ev := range events {
			if ev.RequestID != snap.RequestID || ev.Sequence <= snap.Sequence ||
				ev.Sequence != sequence+1 ||
				ev.Mode != proxy.ForwardModeStateful || ev.Kind == proxy.EventKindRequestHandled ||
				ev.Kind == proxy.EventKindRequestForwarded || ev.Kind == proxy.EventKindRequestCompleted {
				t.Fatalf("restored event = %+v, want only new continuation observations", ev)
			}
			sequence = ev.Sequence
		}
	})
}

// TestProxy_RestoreRequest_WorkingRequestRoundTrip proves the checkpoint
// carries the post-preprocess working request: a route's PreprocessRoute
// mutation survives the JSON round-trip into every later target group,
// and the application hook is never replayed on the restore side.
func TestProxy_RestoreRequest_WorkingRequestRoundTrip(t *testing.T) {
	t.Parallel()

	noopNext := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
		return nil
	})

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		laddr := netip.MustParseAddrPort("192.168.1.100:5060")
		raddr := netip.MustParseAddrPort("10.0.0.5:5060")

		var hookCalls atomic.Int32
		route := &proxy.Route{
			Name:        "work-rt",
			ForwardMode: proxy.ForwardModeStateful,
			PreprocessRoute: func(_ context.Context, work *sip.RequestEnvelope) error {
				hookCalls.Add(1)
				work.WithMessage(func(r *sip.Request) {
					r.Headers.AppendRoute(header.RouteHop{
						URI: &sip.URI{
							Addr:   sip.MakeHostPortAddr("192.0.2.77", 5060),
							Params: make(sip.Values).Set("lr", ""),
						},
					})
				})
				return nil
			},
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{
					{{ID: "A", URI: snapTargetURI("10.0.0.9")}},
					{{ID: "B", URI: snapTargetURI("10.0.0.10")}},
				}}, nil
			},
		}
		opts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}
		elm1, prx1, tp1 := newConfiguredTestProxy(t, route, opts)

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".work-rt", laddr, raddr)
		if err := prx1.InterceptInboundRequest(sip.ContextWithElement(ctx, elm1), noopNext, req); err != nil {
			t.Fatalf("prx1.InterceptInboundRequest() error = %v, want nil", err)
		}
		synctest.Wait()

		sends := drainReqs(tp1)
		if got := len(sends); got != 1 {
			t.Fatalf("initial outbound sends=%d, want 1", got)
		}
		assertReqRouteHop(t, sends[0], "192.0.2.77")
		if got := hookCalls.Load(); got != 1 {
			t.Fatalf("opts.PreprocessRoute calls=%d, want 1", got)
		}

		srvKey, err := sip.MakeServerTransactionKey(req)
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
		clnKey, err := sip.MakeClientTransactionKey(sends[0])
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx1, ok := elm1.LoadClientTransaction(clnKey)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}
		clnSnapper, ok := clnTx1.(sip.ClientTransactionSnapshotter)
		if !ok {
			t.Fatal("forwarding client transaction does not implement sip.ClientTransactionSnapshotter")
		}
		clnSnap := mustClnTxSnapshot(t, clnSnapper)

		// The durable checkpoint is a wire record: the working request
		// must be part of it, and the restore must run from the decoded
		// copy.
		raw, err := json.Marshal(snap)
		if err != nil {
			t.Fatalf("json.Marshal(snap) error = %v, want nil", err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatalf("json.Unmarshal(snapshot fields) error = %v, want nil", err)
		}
		if _, ok := fields["working_request"]; !ok {
			t.Fatal("marshaled snapshot carries no working_request field")
		}
		var rqJSON proxy.RequestSnapshot
		if err := json.Unmarshal(raw, &rqJSON); err != nil {
			t.Fatalf("json.Unmarshal(proxy.RequestSnapshot) error = %v, want nil", err)
		}

		elm2, prx2, tp2 := newConfiguredTestProxy(t, route, opts)
		srvTx2, err := sip.RestoreServerTransaction(srvSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		clnTx2, err := sip.RestoreClientTransaction(clnSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
		}
		rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), &rqJSON, proxy.RestoreBindings{
			Element:            elm2,
			ServerTransaction:  srvTx2,
			ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{clnKey: clnTx2},
		})
		if err != nil {
			t.Fatalf("prx2.RestoreRequest() error = %v, want nil", err)
		}
		if err := rq2.Start(ctx); err != nil {
			t.Fatalf("rq2.Start() error = %v, want nil", err)
		}
		synctest.Wait()
		if got := len(drainReqs(tp2)) + len(drainRess(tp2)); got != 0 {
			t.Fatalf("sends at restored activation=%d, want 0", got)
		}

		// Settling group A's live attempt fails the request over into
		// group B: its outbound copy must still continue from the
		// post-preprocess working request.
		if err := clnTx2.RecvResponse(ctx, newInRes(t, clnTx2.Request(), sip.ResponseStatusBusyHere)); err != nil {
			t.Fatalf("clnTx2.RecvResponse(486) error = %v, want nil", err)
		}
		synctest.Wait()

		// The settled attempt's transaction also emits its non-2xx ACK;
		// the failover INVITE to group B is the send that continues the
		// orchestration.
		var failover *sip.RequestEnvelope
		var invites int
		for _, out := range drainReqs(tp2) {
			if out.Method().Equal(sip.RequestMethodInvite) {
				invites++
				failover = out
			}
		}
		if invites != 1 || failover == nil {
			t.Fatalf("failover INVITE sends=%d, want 1", invites)
		}
		u, ok := failover.URI().(*sip.URI)
		if !ok || u.Addr.Host() != "10.0.0.10" {
			t.Fatalf("failover request URI=%v, want host 10.0.0.10", failover.URI())
		}
		assertReqRouteHop(t, failover, "192.0.2.77")
		if got := hookCalls.Load(); got != 1 {
			t.Fatalf("opts.PreprocessRoute calls after restore=%d, want 1 (the hook is never replayed)", got)
		}
	})
}

// TestProxy_RestoreRequest_MissingWorkingRequestRejected proves a
// version-1 checkpoint without the working request is rejected before
// anything is installed.
func TestProxy_RestoreRequest_MissingWorkingRequestRejected(t *testing.T) {
	t.Parallel()

	noopNext := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
		return nil
	})

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		laddr := netip.MustParseAddrPort("192.168.1.100:5060")
		raddr := netip.MustParseAddrPort("10.0.0.5:5060")

		route := &proxy.Route{
			Name:        "work-miss",
			ForwardMode: proxy.ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{
					{{ID: "A", URI: snapTargetURI("10.0.0.9")}},
				}}, nil
			},
		}
		opts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}
		elm1, prx1, tp1 := newConfiguredTestProxy(t, route, opts)

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".work-miss", laddr, raddr)
		if err := prx1.InterceptInboundRequest(sip.ContextWithElement(ctx, elm1), noopNext, req); err != nil {
			t.Fatalf("prx1.InterceptInboundRequest() error = %v, want nil", err)
		}
		synctest.Wait()

		outReq := testutil.WaitResult(t, tp1.sendReqCalls, "outbound branch send").req
		clnKey, err := sip.MakeClientTransactionKey(outReq)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx1, ok := elm1.LoadClientTransaction(clnKey)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}
		srvKey, err := sip.MakeServerTransactionKey(req)
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
		clnSnapper, ok := clnTx1.(sip.ClientTransactionSnapshotter)
		if !ok {
			t.Fatal("forwarding client transaction does not implement sip.ClientTransactionSnapshotter")
		}
		clnSnap := mustClnTxSnapshot(t, clnSnapper)

		// A wire checkpoint missing the working request decodes with a
		// nil field; the version-1 contract rejects it.
		raw, err := json.Marshal(snap)
		if err != nil {
			t.Fatalf("json.Marshal(snap) error = %v, want nil", err)
		}
		var rqJSON proxy.RequestSnapshot
		if err := json.Unmarshal(raw, &rqJSON); err != nil {
			t.Fatalf("json.Unmarshal(proxy.RequestSnapshot) error = %v, want nil", err)
		}
		rqJSON.WorkingRequest = nil

		elm2, prx2, tp2 := newConfiguredTestProxy(t, route, opts)
		srvTx2, err := sip.RestoreServerTransaction(srvSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		clnTx2, err := sip.RestoreClientTransaction(clnSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
		}
		_, err = prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), &rqJSON, proxy.RestoreBindings{
			Element:            elm2,
			ServerTransaction:  srvTx2,
			ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{clnKey: clnTx2},
		})
		if !errors.Is(err, proxy.ErrInvalidSnapshot) {
			t.Fatalf("prx2.RestoreRequest() error = %v, want %v", err, proxy.ErrInvalidSnapshot)
		}
		if got, ok := prx2.LookupRequest(elm2, srvKey); ok || got != nil {
			t.Fatal("rejected restore installed an upstream index entry")
		}
		synctest.Wait()
		if sends := len(drainReqs(tp2)) + len(drainRess(tp2)); sends != 0 {
			t.Fatalf("sends by a rejected restore=%d, want 0", sends)
		}
	})
}

func TestProxy_RestoreRequest_RequestValueValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		mutate                 func(*proxy.RequestSnapshot)
		invalidMessage         bool
		invalidOriginalMessage bool
		wantErr                error
		wantCause              error
	}{
		{
			name: "missing via",
			mutate: func(snap *proxy.RequestSnapshot) {
				snap.WorkingRequest.WithMessage(func(r *sip.Request) { r.Headers.Delete("Via") })
			},
			invalidMessage: true,
			wantErr:        proxy.ErrInvalidSnapshot,
			wantCause:      sip.ErrInvalidMessage,
		},
		{
			name: "invalid request uri",
			mutate: func(snap *proxy.RequestSnapshot) {
				snap.WorkingRequest.WithMessage(func(r *sip.Request) { r.URI = nil })
			},
			invalidMessage: true,
			wantErr:        proxy.ErrInvalidSnapshot,
			wantCause:      sip.ErrInvalidMessage,
		},
		{
			name: "invalid protocol",
			mutate: func(snap *proxy.RequestSnapshot) {
				snap.WorkingRequest.WithMessage(func(r *sip.Request) { r.Proto = sip.ProtoInfo{} })
			},
			invalidMessage: true,
			wantErr:        proxy.ErrInvalidSnapshot,
			wantCause:      sip.ErrInvalidMessage,
		},
		{
			name: "method mismatch",
			mutate: func(snap *proxy.RequestSnapshot) {
				snap.WorkingRequest.WithMessage(func(r *sip.Request) {
					r.Method = sip.RequestMethodInfo
					cseq, _ := r.Headers.CSeq()
					cseq.Method = sip.RequestMethodInfo
				})
			},
			wantErr: proxy.ErrInvalidSnapshot,
		},
		{
			name: "opaque original metadata",
			mutate: func(snap *proxy.RequestSnapshot) {
				snap.OriginalRequest.Metadata().Set("restore.opaque", make(chan int))
			},
			wantErr: proxy.ErrSnapshotUnsupported,
		},
		{
			name: "opaque working metadata",
			mutate: func(snap *proxy.RequestSnapshot) {
				snap.WorkingRequest.Metadata().Set("restore.opaque", make(chan int))
			},
			wantErr: proxy.ErrSnapshotUnsupported,
		},
		{
			name: "serializable metadata",
			mutate: func(snap *proxy.RequestSnapshot) {
				snap.OriginalRequest.Metadata().Set("restore.json", map[string]any{
					"route": "original", "values": []any{"alice", true, float64(1)},
				})
				snap.WorkingRequest.Metadata().Set("restore.json", map[string]any{
					"route": "working", "values": []any{"bob", false, float64(2)},
				})
			},
		},
		{
			name: "empty working envelope",
			mutate: func(snap *proxy.RequestSnapshot) {
				snap.WorkingRequest = &sip.RequestEnvelope{}
			},
			invalidMessage: true,
			wantErr:        proxy.ErrInvalidSnapshot,
		},
		{
			name: "missing original via",
			mutate: func(snap *proxy.RequestSnapshot) {
				snap.OriginalRequest.WithMessage(func(r *sip.Request) { r.Headers.Delete("Via") })
			},
			invalidOriginalMessage: true,
			wantErr:                proxy.ErrInvalidSnapshot,
			wantCause:              sip.ErrInvalidMessage,
		},
		{
			name: "invalid original request uri",
			mutate: func(snap *proxy.RequestSnapshot) {
				snap.OriginalRequest.WithMessage(func(r *sip.Request) { r.URI = nil })
			},
			invalidOriginalMessage: true,
			wantErr:                proxy.ErrInvalidSnapshot,
			wantCause:              sip.ErrInvalidMessage,
		},
		{
			name: "invalid original protocol",
			mutate: func(snap *proxy.RequestSnapshot) {
				snap.OriginalRequest.WithMessage(func(r *sip.Request) { r.Proto = sip.ProtoInfo{} })
			},
			invalidOriginalMessage: true,
			wantErr:                proxy.ErrInvalidSnapshot,
			wantCause:              sip.ErrInvalidMessage,
		},
		{
			name: "empty original envelope",
			mutate: func(snap *proxy.RequestSnapshot) {
				snap.OriginalRequest = &sip.RequestEnvelope{}
			},
			invalidOriginalMessage: true,
			wantErr:                proxy.ErrInvalidSnapshot,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				var hookCalls, resolveCalls atomic.Int32
				route := &proxy.Route{
					Name:        "restore-values",
					ForwardMode: proxy.ForwardModeStateful,
					PreprocessRoute: func(context.Context, *sip.RequestEnvelope) error {
						hookCalls.Add(1)
						return nil
					},
					ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
						resolveCalls.Add(1)
						return proxy.TargetPlan{Groups: [][]proxy.Target{
							{{ID: "A", URI: snapTargetURI("10.0.0.9")}},
						}}, nil
					},
				}
				opts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}
				elm1, prx1, tp1 := newConfiguredTestProxy(t, route, opts)
				req := newInInviteReq(t,
					"UDP", sip.MagicCookie+".restore-values",
					netip.MustParseAddrPort("192.168.1.100:5060"), netip.MustParseAddrPort("10.0.0.5:5060"),
				)
				noopNext := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil })
				if err := prx1.InterceptInboundRequest(sip.ContextWithElement(ctx, elm1), noopNext, req); err != nil {
					t.Fatalf("prx1.InterceptInboundRequest() error = %v, want nil", err)
				}
				synctest.Wait()
				if got := hookCalls.Load(); got != 1 {
					t.Fatalf("source opts.PreprocessRoute calls = %d, want 1", got)
				}
				if got := resolveCalls.Load(); got != 1 {
					t.Fatalf("source ResolveRequestTargets calls = %d, want 1", got)
				}
				sends := drainReqs(tp1)
				if len(sends) != 1 {
					t.Fatalf("source request sends = %d, want 1", len(sends))
				}
				clnKey, err := sip.MakeClientTransactionKey(sends[0])
				if err != nil {
					t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
				}
				clnTx1, ok := elm1.LoadClientTransaction(clnKey)
				if !ok {
					t.Fatal("forwarding client transaction not registered")
				}
				srvKey, err := sip.MakeServerTransactionKey(req)
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
				clnSnapper, ok := clnTx1.(sip.ClientTransactionSnapshotter)
				if !ok {
					t.Fatal("forwarding client transaction does not implement sip.ClientTransactionSnapshotter")
				}
				clnSnap := mustClnTxSnapshot(t, clnSnapper)

				raw, err := json.Marshal(snap)
				if err != nil {
					t.Fatalf("json.Marshal(snap) error = %v, want nil", err)
				}
				snap = new(proxy.RequestSnapshot)
				if err := json.Unmarshal(raw, snap); err != nil {
					t.Fatalf("json.Unmarshal(proxy.RequestSnapshot) error = %v, want nil", err)
				}
				// Mutate only after decoding so restore, not JSON encoding,
				// must reject directly supplied malformed or opaque values.
				tt.mutate(snap)
				if err := snap.WorkingRequest.Validate(); (err != nil) != tt.invalidMessage {
					t.Fatalf("snap.WorkingRequest.Validate() error = %v, want invalid = %t", err, tt.invalidMessage)
				}
				if err := snap.OriginalRequest.Validate(); (err != nil) != tt.invalidOriginalMessage {
					t.Fatalf("snap.OriginalRequest.Validate() error = %v, want invalid = %t", err, tt.invalidOriginalMessage)
				}

				elm, prx, tp := newConfiguredTestProxy(t, route, opts)
				srvTx, err := sip.RestoreServerTransaction(srvSnap, tp)
				if err != nil {
					t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
				}
				clnTx, err := sip.RestoreClientTransaction(clnSnap, tp)
				if err != nil {
					t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
				}
				bindings := proxy.RestoreBindings{
					Element:            elm,
					ServerTransaction:  srvTx,
					ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{clnKey: clnTx},
				}
				var events, revisions atomic.Int32
				unbindEvents := prx.BindEventHandler(proxy.EventHandlerFunc(
					func(context.Context, proxy.Event) { events.Add(1) },
				))
				defer unbindEvents()
				unbindSnapshots := prx.BindSnapshotHandler(proxy.RequestSnapshotHandlerFunc(
					func(context.Context, *proxy.RequestSnapshot) { revisions.Add(1) },
				))
				defer unbindSnapshots()

				rq, err := prx.RestoreRequest(sip.ContextWithElement(t.Context(), elm), snap, bindings)
				if tt.wantErr != nil {
					if rq != nil || !errors.Is(err, tt.wantErr) {
						t.Fatalf("prx.RestoreRequest() = (%v, %v), want (nil, %v)", rq, err, tt.wantErr)
					}
					if !errors.Is(err, proxy.ErrClass) {
						t.Fatalf("prx.RestoreRequest() error = %v, want proxy classification", err)
					}
					if tt.wantCause != nil && !errors.Is(err, tt.wantCause) {
						t.Fatalf("prx.RestoreRequest() error = %v, want cause %v", err, tt.wantCause)
					}
					if got, ok := prx.LookupRequest(elm, snap.UpstreamKey); ok || got != nil {
						t.Fatal("rejected restore installed an upstream index entry")
					}
				} else {
					if rq == nil || err != nil {
						t.Fatalf("prx.RestoreRequest() = (%v, %v), want a dormant request and nil", rq, err)
					}
					if got, ok := prx.LookupRequest(elm, snap.UpstreamKey); !ok || got != rq {
						t.Fatal("valid restore did not install its upstream index entry")
					}
				}
				synctest.Wait()
				if got := len(drainReqs(tp)) + len(drainRess(tp)); got != 0 {
					t.Fatalf("restore sends = %d, want 0", got)
				}
				if srvTx.Started() || clnTx.Started() {
					t.Fatal("restore started a supplied dormant transaction")
				}
				if got := events.Load() + revisions.Load(); got != 0 {
					t.Fatalf("restore observations = %d, want 0", got)
				}
				if got := hookCalls.Load(); got != 1 {
					t.Fatalf("PreprocessRoute calls after restore = %d, want 1", got)
				}
				if got := resolveCalls.Load(); got != 1 {
					t.Fatalf("ResolveRequestTargets calls after restore = %d, want 1", got)
				}
			})
		})
	}
}

// TestProxy_MetadataRecovery covers the restore-time rebuild of the
// sip.proxy metadata on the owned dormant copies: the checkpoint
// authority — the saved request identity, the claiming route's effective
// mode, and the saved attempt records — replaces missing or stale stamps,
// the attempt counter resumes past every saved suffix, and the dormant
// install emits no sends, timers, resolver calls, observer notifications,
// or preprocessing replays before Start.
func TestProxy_MetadataRecovery(t *testing.T) {
	t.Parallel()

	opts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}

	// wantClaim is the rebuilt claim-stage stamp every inbound owned copy
	// carries: the saved request identity under the effective route.
	wantClaim := func(snap *proxy.RequestSnapshot) proxy.Metadata {
		return proxy.Metadata{
			RequestID: snap.RequestID,
			RouteName: "rest",
			Mode:      proxy.ForwardModeStateful,
			Stage:     proxy.StageClaim,
		}
	}
	reqMD := func(tb testing.TB, env *sip.RequestEnvelope) proxy.Metadata {
		tb.Helper()
		md, ok := proxy.MetadataFrom(env.Metadata())
		if !ok {
			tb.Fatalf("request envelope carries no valid sip.proxy metadata")
		}
		return md
	}
	resMD := func(tb testing.TB, env *sip.ResponseEnvelope) proxy.Metadata {
		tb.Helper()
		md, ok := proxy.MetadataFrom(env.Metadata())
		if !ok {
			tb.Fatalf("response envelope carries no valid sip.proxy metadata")
		}
		return md
	}
	// assertContinuation proves the restored observation stream continues
	// the checkpoint: every post-activation event carries the saved
	// request identity and a strictly increasing sequence, while the
	// committed one-shot records — handled, first-forward — never replay.
	assertContinuation := func(t *testing.T, snap *proxy.RequestSnapshot, rec *evtRecorder, wantCompleted int) {
		t.Helper()
		seq := snap.Sequence
		for _, ev := range rec.get() {
			if ev.RequestID != snap.RequestID {
				t.Errorf("event %+v carries request ID %q, want %q", ev, ev.RequestID, snap.RequestID)
			}
			if ev.Sequence != seq+1 {
				t.Errorf("event %+v sequence = %d, want %d", ev, ev.Sequence, seq+1)
			}
			seq = ev.Sequence
		}
		counts := rec.counts()
		if counts[proxy.EventKindRequestHandled] != 0 {
			t.Errorf("handled observations=%d, want 0 (history is never replayed)", counts[proxy.EventKindRequestHandled])
		}
		if counts[proxy.EventKindRequestForwarded] != 0 {
			t.Errorf("first-forward observations=%d, want 0 (history is never replayed)", counts[proxy.EventKindRequestForwarded])
		}
		if counts[proxy.EventKindRequestCompleted] != wantCompleted {
			t.Errorf("completion observations=%d, want %d", counts[proxy.EventKindRequestCompleted], wantCompleted)
		}
	}

	t.Run("full json restore", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)

			// The saved attempt records carry their own forward-stage
			// attribution, the authority the rebuild must reproduce.
			attA := reqMD(t, attemptOfTarget(f.snap, "A").Request)
			attB := reqMD(t, attemptOfTarget(f.snap, "B").Request)
			if attA.Stage != proxy.StageForward || attA.AttemptID == "" ||
				attB.Stage != proxy.StageForward || attB.AttemptID == "" {
				t.Fatalf("saved attempt stamps = (%+v, %+v), want forward-stage attribution", attA, attB)
			}

			// Every checkpoint family round-trips through the wire encoding.
			var rqJSON proxy.RequestSnapshot
			raw, err := json.Marshal(f.snap)
			if err != nil {
				t.Fatalf("json.Marshal(snap) error = %v, want nil", err)
			}
			if err := json.Unmarshal(raw, &rqJSON); err != nil {
				t.Fatalf("json.Unmarshal(proxy.RequestSnapshot) error = %v, want nil", err)
			}
			var srvJSON sip.ServerTransactionSnapshot
			raw, err = json.Marshal(f.srvSnap)
			if err != nil {
				t.Fatalf("json.Marshal(srvSnap) error = %v, want nil", err)
			}
			if err := json.Unmarshal(raw, &srvJSON); err != nil {
				t.Fatalf("json.Unmarshal(sip.ServerTransactionSnapshot) error = %v, want nil", err)
			}
			var clnJSON sip.ClientTransactionSnapshot
			raw, err = json.Marshal(f.clnSnap)
			if err != nil {
				t.Fatalf("json.Marshal(clnSnap) error = %v, want nil", err)
			}
			if err := json.Unmarshal(raw, &clnJSON); err != nil {
				t.Fatalf("json.Unmarshal(sip.ClientTransactionSnapshot) error = %v, want nil", err)
			}

			elm2, prx2, tp2 := newConfiguredTestProxy(t, f.route, opts)
			rec := &evtRecorder{}
			prx2.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			srvTx2, err := sip.RestoreServerTransaction(&srvJSON, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
			}
			clnTx2, err := sip.RestoreClientTransaction(&clnJSON, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
			}
			rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), &rqJSON, proxy.RestoreBindings{
				Element:            elm2,
				ServerTransaction:  srvTx2,
				ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{f.keyB: clnTx2},
				Deliveries: map[proxy.DeliveryRef]proxy.BoundDelivery{
					f.delRef: {Transport: tp2, RemoteAddr: f.raddr},
				},
			})
			if err != nil {
				t.Fatalf("prx2.RestoreRequest() error = %v, want nil", err)
			}
			if err := rq2.Start(ctx); err != nil {
				t.Fatalf("rq2.Start() error = %v, want nil", err)
			}
			synctest.Wait()

			// The re-snapshot exposes the rebuilt stamps of every owned
			// dormant copy: inbound copies claim under the saved identity,
			// the attempt request keeps its recorded forward attribution,
			// and the attributable response context keeps its owning
			// attempt record's correlation.
			rsnap, err := rq2.Snapshot()
			if err != nil {
				t.Fatalf("rq2.Snapshot() error = %v, want nil", err)
			}
			for name, env := range map[string]*sip.RequestEnvelope{
				"original": rsnap.OriginalRequest,
				"working":  rsnap.WorkingRequest,
			} {
				if diff := cmp.Diff(wantClaim(f.snap), reqMD(t, env)); diff != "" {
					t.Errorf("restored %s claim stamp (-want +got): %s", name, diff)
				}
			}
			attB2 := attemptOfTarget(rsnap, "B")
			if attB2 == nil {
				t.Fatal("restored snapshot has no attempt record for target B")
			}
			wantFwd := proxy.Metadata{
				RequestID: f.snap.RequestID,
				TargetID:  "B",
				AttemptID: attB.AttemptID,
				RouteName: "rest",
				Mode:      proxy.ForwardModeStateful,
				Stage:     proxy.StageForward,
			}
			if diff := cmp.Diff(wantFwd, reqMD(t, attB2.Request)); diff != "" {
				t.Errorf("restored attempt forward stamp (-want +got): %s", diff)
			}
			if got := len(rsnap.Candidates); got != 1 {
				t.Fatalf("restored response candidates=%d, want 1", got)
			}
			wantCand := proxy.Metadata{
				RequestID: f.snap.RequestID,
				TargetID:  "A",
				AttemptID: attA.AttemptID,
				RouteName: "rest",
				Mode:      proxy.ForwardModeStateful,
				Stage:     proxy.StageResponse,
			}
			if diff := cmp.Diff(wantCand, resMD(t, rsnap.Candidates[0])); diff != "" {
				t.Errorf("restored candidate response stamp (-want +got): %s", diff)
			}

			// A response on the restored attempt crosses upstream with the
			// rebuilt attempt correlation.
			if err := clnTx2.RecvResponse(ctx, newInRes(t, clnTx2.Request(), sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("clnTx2.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()
			prov := drainRess(tp2)
			if got := len(prov); got != 1 {
				t.Fatalf("forwarded provisionals upstream=%d, want 1", got)
			}
			wantUp := proxy.Metadata{
				RequestID: f.snap.RequestID,
				TargetID:  "B",
				AttemptID: attB.AttemptID,
				RouteName: "rest",
				Mode:      proxy.ForwardModeStateful,
				Stage:     proxy.StageUpstream,
			}
			if diff := cmp.Diff(wantUp, resMD(t, prov[0])); diff != "" {
				t.Errorf("forwarded provisional stamp (-want +got): %s", diff)
			}

			// Settling the restored attempt commits the recorded 404: the
			// forwarded final is an unattributed aggregate and invents no
			// winning attempt.
			if err := clnTx2.RecvResponse(ctx, newInRes(t, clnTx2.Request(), sip.ResponseStatusBusyHere)); err != nil {
				t.Fatalf("clnTx2.RecvResponse(486) error = %v, want nil", err)
			}
			synctest.Wait()
			var final *sip.ResponseEnvelope
			for _, res := range drainRess(tp2) {
				if res.Status() == sip.ResponseStatusNotFound {
					final = res
				}
			}
			if final == nil {
				t.Fatal("committed 404 never crossed upstream")
			}
			wantFinal := proxy.Metadata{
				RequestID: f.snap.RequestID,
				RouteName: "rest",
				Mode:      proxy.ForwardModeStateful,
				Stage:     proxy.StageUpstream,
			}
			if diff := cmp.Diff(wantFinal, resMD(t, final)); diff != "" {
				t.Errorf("aggregated final stamp (-want +got): %s", diff)
			}
			assertContinuation(t, f.snap, rec, 1)
		})
	})

	t.Run("older v1 without metadata", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)

			// A supported v1 checkpoint captured before the metadata
			// contract carries no sip.proxy stamps at all.
			snap := f.snap.Clone()
			strip := func(meta *sip.MessageMetadata) {
				if meta != nil {
					meta.Delete(proxy.MetadataKey)
				}
			}
			strip(snap.OriginalRequest.Metadata())
			strip(snap.WorkingRequest.Metadata())
			for i := range snap.Attempts {
				if snap.Attempts[i].Request != nil {
					strip(snap.Attempts[i].Request.Metadata())
				}
				if snap.Attempts[i].Response != nil {
					strip(snap.Attempts[i].Response.Metadata())
				}
			}
			for _, res := range snap.Candidates {
				strip(res.Metadata())
			}

			elm2, prx2, tp2 := newConfiguredTestProxy(t, f.route, opts)
			rec := &evtRecorder{}
			prx2.BindEventHandler(proxy.EventHandlerFunc(rec.record))
			bindings := f.dormantBindings(t, elm2, tp2)
			clnB := bindings.ClientTransactions[f.keyB]
			rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, bindings)
			if err != nil {
				t.Fatalf("prx2.RestoreRequest() error = %v, want nil", err)
			}
			if err := rq2.Start(ctx); err != nil {
				t.Fatalf("rq2.Start() error = %v, want nil", err)
			}
			synctest.Wait()

			// Correlation is rebuilt from the checkpoint authority: the
			// saved request ID is adopted, never replaced by a fresh one.
			rsnap, err := rq2.Snapshot()
			if err != nil {
				t.Fatalf("rq2.Snapshot() error = %v, want nil", err)
			}
			if rsnap.RequestID != f.snap.RequestID {
				t.Fatalf("restored request ID = %q, want the saved %q", rsnap.RequestID, f.snap.RequestID)
			}
			for name, env := range map[string]*sip.RequestEnvelope{
				"original": rsnap.OriginalRequest,
				"working":  rsnap.WorkingRequest,
			} {
				if diff := cmp.Diff(wantClaim(f.snap), reqMD(t, env)); diff != "" {
					t.Errorf("rebuilt %s claim stamp (-want +got): %s", name, diff)
				}
			}
			attB2 := attemptOfTarget(rsnap, "B")
			if attB2 == nil {
				t.Fatal("restored snapshot has no attempt record for target B")
			}
			md := reqMD(t, attB2.Request)
			if md.Stage != proxy.StageForward || md.RequestID != f.snap.RequestID ||
				md.RouteName != "rest" || md.Mode != proxy.ForwardModeStateful ||
				md.TargetID != "B" || !strings.HasPrefix(md.AttemptID, "B#") {
				t.Errorf("rebuilt attempt stamp = %+v, want forward-stage attribution of target B under %q",
					md, f.snap.RequestID)
			}
			cand := resMD(t, rsnap.Candidates[0])
			if cand.Stage != proxy.StageResponse || cand.RequestID != f.snap.RequestID ||
				cand.TargetID != "A" || cand.AttemptID == "" {
				t.Errorf("rebuilt candidate stamp = %+v, want response-stage attribution of target A", cand)
			}

			if err := clnB.RecvResponse(ctx, newInRes(t, clnB.Request(), sip.ResponseStatusBusyHere)); err != nil {
				t.Fatalf("clnB.RecvResponse(486) error = %v, want nil", err)
			}
			synctest.Wait()
			drainReqs(tp2)
			drainRess(tp2)
			assertContinuation(t, f.snap, rec, 1)
		})
	})

	t.Run("stale stamps ignored", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)

			// Every owned copy carries a valid but wrong stamp: the
			// checkpoint authority, not the envelope, is trusted.
			snap := f.snap.Clone()
			stale := proxy.Metadata{
				RequestID: "stale-req",
				TargetID:  "stale-target",
				AttemptID: "stale#1",
				RouteName: "stale-route",
				Mode:      proxy.ForwardModeStateful,
				Stage:     proxy.StageUpstream,
			}
			proxy.SetMetadata(snap.OriginalRequest.Metadata(), stale)
			proxy.SetMetadata(snap.WorkingRequest.Metadata(), stale)
			for i := range snap.Attempts {
				if snap.Attempts[i].Request != nil {
					proxy.SetMetadata(snap.Attempts[i].Request.Metadata(), stale)
				}
				if snap.Attempts[i].Response != nil {
					proxy.SetMetadata(snap.Attempts[i].Response.Metadata(), stale)
				}
			}
			for _, res := range snap.Candidates {
				proxy.SetMetadata(res.Metadata(), stale)
			}

			elm2, prx2, tp2 := newConfiguredTestProxy(t, f.route, opts)
			rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, f.dormantBindings(t, elm2, tp2))
			if err != nil {
				t.Fatalf("prx2.RestoreRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			rsnap, err := rq2.Snapshot()
			if err != nil {
				t.Fatalf("rq2.Snapshot() error = %v, want nil", err)
			}
			for name, env := range map[string]*sip.RequestEnvelope{
				"original": rsnap.OriginalRequest,
				"working":  rsnap.WorkingRequest,
			} {
				if diff := cmp.Diff(wantClaim(f.snap), reqMD(t, env)); diff != "" {
					t.Errorf("corrected %s claim stamp (-want +got): %s", name, diff)
				}
			}
			attB2 := attemptOfTarget(rsnap, "B")
			if attB2 == nil {
				t.Fatal("restored snapshot has no attempt record for target B")
			}
			md := reqMD(t, attB2.Request)
			if md.Stage != proxy.StageForward || md.RequestID != f.snap.RequestID ||
				md.RouteName != "rest" || md.Mode != proxy.ForwardModeStateful || md.TargetID != "B" {
				t.Errorf("corrected attempt stamp = %+v, want forward-stage attribution of target B under %q",
					md,
					f.snap.RequestID,
				)
			}
			if strings.HasPrefix(md.AttemptID, "stale") {
				t.Errorf("corrected attempt ID = %q, want a rebuilt attempt correlation", md.AttemptID)
			}
			cand := resMD(t, rsnap.Candidates[0])
			if cand.Stage != proxy.StageResponse || cand.RequestID != f.snap.RequestID ||
				cand.RouteName != "rest" || cand.Mode != proxy.ForwardModeStateful {
				t.Errorf("corrected candidate stamp = %+v, want response-stage record under %q",
					cand,
					f.snap.RequestID,
				)
			}
		})
	})

	t.Run("attempt suffix past hash", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)

			// The saved target ID itself contains '#': only the LAST '#'
			// delimits the attempt counter, so the recorded attempt
			// fork#east#12 leaves the counter at 12.
			snap := f.snap.Clone()
			snap.TargetPlan.Groups[0][1].ID = "fork#east"
			targetStateOf(snap, "B").TargetID = "fork#east"
			attB := attemptOfTarget(snap, "B")
			attB.TargetID = "fork#east"
			proxy.SetMetadata(attB.Request.Metadata(), proxy.Metadata{
				RequestID: snap.RequestID,
				TargetID:  "fork#east",
				AttemptID: "fork#east#12",
				RouteName: "rest",
				Mode:      proxy.ForwardModeStateful,
				Stage:     proxy.StageForward,
			})

			loc := &attemptLocator{
				log:     &attemptActionLog{},
				lookups: &atomic.Int32{},
				candidates: map[string][]attemptCandidate{
					"10.0.0.10": {
						udpAttemptCandidate("tried", "10.0.0.10:5060"),
						udpAttemptCandidate("retry", "10.0.0.10:5061"),
					},
				},
			}
			elm2, prx2, tp2 := newAttemptTestProxy(t, f.route, opts, loc)
			bindings := f.dormantBindings(t, elm2, tp2)
			clnB := bindings.ClientTransactions[f.keyB]
			rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, bindings)
			if err != nil {
				t.Fatalf("prx2.RestoreRequest() error = %v, want nil", err)
			}
			if err := rq2.Start(ctx); err != nil {
				t.Fatalf("rq2.Start() error = %v, want nil", err)
			}
			synctest.Wait()

			// A retryable 503 advances the rebuilt address runner: the new
			// physical attempt mints above every saved suffix, never
			// aliasing fork#east#12 or restarting at fork#east#1.
			if err := clnB.RecvResponse(ctx, newInRes(t, clnB.Request(), sip.ResponseStatusServiceUnavailable)); err != nil {
				t.Fatalf("clnB.RecvResponse(503) error = %v, want nil", err)
			}
			synctest.Wait()

			var retryCopy *sip.RequestEnvelope
			for _, out := range drainReqs(tp2) {
				if out.Method().Equal(sip.RequestMethodInvite) {
					retryCopy = out
				}
			}
			if retryCopy == nil {
				t.Fatal("retryable 503 produced no new attempt send")
			}
			md, ok := proxy.MetadataFrom(retryCopy.Metadata())
			if !ok {
				t.Fatal("retry copy missing sip.proxy metadata")
			}
			if md.TargetID != "fork#east" {
				t.Errorf("TargetID = %q, want fork#east", md.TargetID)
			}
			suffix, found := strings.CutPrefix(md.AttemptID, "fork#east#")
			n, err := strconv.ParseUint(suffix, 10, 64)
			if !found || err != nil || n <= 12 {
				t.Errorf("AttemptID = %q, want fork#east#<n> with n > 12", md.AttemptID)
			}
		})
	})

	t.Run("dormant zero effects", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)

			var resolverCalls, preprocessCalls atomic.Int32
			route := *f.route
			route.PreprocessRoute = func(context.Context, *sip.RequestEnvelope) error {
				preprocessCalls.Add(1)
				return nil
			}
			elm2, prx2, tp2 := newConfiguredTestProxy(t, &route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
				ResolveDelivery: func(context.Context, proxy.DeliveryRef) (proxy.BoundDelivery, error) {
					resolverCalls.Add(1)
					return proxy.BoundDelivery{}, errors.ErrorWrap("unexpected resolver call")
				},
			})

			var events, revisions atomic.Int32
			unbindEvents := prx2.BindEventHandler(proxy.EventHandlerFunc(
				func(context.Context, proxy.Event) { events.Add(1) },
			))
			defer unbindEvents()
			unbindSnapshots := prx2.BindSnapshotHandler(proxy.RequestSnapshotHandlerFunc(
				func(context.Context, *proxy.RequestSnapshot) { revisions.Add(1) },
			))
			defer unbindSnapshots()

			if _, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), f.snap, f.dormantBindings(t, elm2, tp2)); err != nil {
				t.Fatalf("prx2.RestoreRequest() error = %v, want nil", err)
			}
			// Deadline re-arming belongs to activation: advancing past
			// every preserved Timer C bound while dormant still emits
			// nothing.
			time.Sleep(time.Minute)
			synctest.Wait()

			if got := len(drainReqs(tp2)) + len(drainRess(tp2)); got != 0 {
				t.Fatalf("sends while dormant=%d, want 0", got)
			}
			if got := events.Load() + revisions.Load(); got != 0 {
				t.Fatalf("observer notifications while dormant=%d, want 0", got)
			}
			if got := resolverCalls.Load(); got != 0 {
				t.Fatalf("resolver calls while dormant=%d, want 0", got)
			}
			if got := preprocessCalls.Load(); got != 0 {
				t.Fatalf("preprocess replays while dormant=%d, want 0", got)
			}
		})
	})

	t.Run("source envelopes unchanged", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)
			snap := f.snap.Clone()

			// The checkpoint's own envelopes are borrowed read-only:
			// reconstruction stamps only the coordinator's owned copies.
			metaOf := func(m *sip.MessageMetadata) map[string]any {
				return maps.Collect(m.All())
			}
			reqEnvs := make([]*sip.RequestEnvelope, 0, 2+len(snap.Attempts))
			reqEnvs = append(reqEnvs, snap.OriginalRequest, snap.WorkingRequest)
			for i := range snap.Attempts {
				reqEnvs = append(reqEnvs, snap.Attempts[i].Request)
			}
			resEnvs := make([]*sip.ResponseEnvelope, 0, len(snap.Attempts)+len(snap.Candidates))
			for i := range snap.Attempts {
				resEnvs = append(resEnvs, snap.Attempts[i].Response)
			}
			resEnvs = append(resEnvs, snap.Candidates...)

			beforeReq := make(map[*sip.RequestEnvelope]map[string]any, len(reqEnvs))
			for _, env := range reqEnvs {
				if env != nil {
					beforeReq[env] = metaOf(env.Metadata())
				}
			}
			beforeRes := make(map[*sip.ResponseEnvelope]map[string]any, len(resEnvs))
			for _, env := range resEnvs {
				if env != nil {
					beforeRes[env] = metaOf(env.Metadata())
				}
			}

			elm2, prx2, tp2 := newConfiguredTestProxy(t, f.route, opts)
			rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, f.dormantBindings(t, elm2, tp2))
			if err != nil {
				t.Fatalf("prx2.RestoreRequest() error = %v, want nil", err)
			}
			if err := rq2.Start(ctx); err != nil {
				t.Fatalf("rq2.Start() error = %v, want nil", err)
			}
			synctest.Wait()

			for env, want := range beforeReq {
				if diff := cmp.Diff(want, metaOf(env.Metadata()), cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("source request envelope metadata mutated (-before +after): %s", diff)
				}
			}
			for env, want := range beforeRes {
				if diff := cmp.Diff(want, metaOf(env.Metadata()), cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("source response envelope metadata mutated (-before +after): %s", diff)
				}
			}
		})
	})
}
