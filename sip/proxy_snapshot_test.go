package sip_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
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

func targetStateOf(snap *sip.ProxyRequestSnapshot, id string) *sip.ProxyTargetSnapshot {
	for i := range snap.TargetStates {
		if snap.TargetStates[i].TargetID == id {
			return &snap.TargetStates[i]
		}
	}
	return nil
}

func attemptOfTarget(snap *sip.ProxyRequestSnapshot, id string) *sip.ProxyAttemptSnapshot {
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
			route := &sip.ProxyRoute{
				Name:        "snap",
				ForwardMode: sip.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
					return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
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
								Delivery: sip.ProxyDeliveryRef{Kind: "gw", Key: "eu-1"},
							},
						},
					}}, nil
				},
				SelectTargets: func(context.Context, sip.ProxySelection) ([]string, error) {
					if selCalls.Add(1) == 1 {
						return []string{"A", "B"}, nil
					}
					// While a branch stays active an empty selection waits:
					// the next group never starts and target C stays
					// unstarted.
					return nil, nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, sip.ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			})

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".snap-states", laddr, raddr)
			if err := prx.InterceptInboundRequest(ctx, noopNext, req); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			sends := make(map[string]*sip.RequestEnvelope)
			for _, out := range drainPrxReqs(tp) {
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
			rq, ok := prx.LookupRequest(srvKey)
			if !ok || rq == nil {
				t.Fatal("prx.LookupRequest() found no owned request")
			}

			snap, err := rq.Snapshot()
			if err != nil {
				t.Fatalf("rq.Snapshot() error = %v, want nil", err)
			}
			if snap.Version != 1 {
				t.Fatalf("snapshot version=%d, want 1", snap.Version)
			}
			if snap.RouteName != "snap" || snap.Mode != sip.ForwardModeStateful {
				t.Fatalf("snapshot route=(%q, %v), want (%q, %v)", snap.RouteName, snap.Mode, "snap", sip.ForwardModeStateful)
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
			if got := snap.TargetPlan.Groups[1][0].Delivery; got != (sip.ProxyDeliveryRef{Kind: "gw", Key: "eu-1"}) {
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
			route := &sip.ProxyRoute{
				Name:        "snap-clone",
				ForwardMode: sip.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
					return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
						{{
							ID:         "A",
							URI:        snapTargetURI(targetHosts["A"]),
							Attributes: map[string]string{"role": "primary"},
						}},
					}}, nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, sip.ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			})

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".snap-clone", laddr, raddr)
			if err := prx.InterceptInboundRequest(ctx, noopNext, req); err != nil {
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
			rq, ok := prx.LookupRequest(srvKey)
			if !ok {
				t.Fatal("prx.LookupRequest() found no owned request")
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

		ref := sip.ProxyDeliveryRef{Kind: "gw", Key: "eu-1"}
		data, err := json.Marshal(ref)
		if err != nil {
			t.Fatalf("json.Marshal(ProxyDeliveryRef) error = %v, want nil", err)
		}
		var decoded sip.ProxyDeliveryRef
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("json.Unmarshal(ProxyDeliveryRef) error = %v, want nil", err)
		}
		if diff := cmp.Diff(ref, decoded); diff != "" {
			t.Fatalf("delivery ref round-trip (-want +got): %s", diff)
		}
	})

	t.Run("unsupported opaque metadata fails capture", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := &sip.ProxyRoute{
				Name:        "snap-meta",
				ForwardMode: sip.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
					return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
						{{ID: "A", URI: snapTargetURI(targetHosts["A"])}},
					}}, nil
				},
			}
			_, prx, _ := newConfiguredTestProxy(t, route, sip.ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			})

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".snap-meta", laddr, raddr)
			req.Metadata().Set("opaque", make(chan int))
			if err := prx.InterceptInboundRequest(ctx, noopNext, req); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			rq, ok := prx.LookupRequest(srvKey)
			if !ok {
				t.Fatal("prx.LookupRequest() found no owned request")
			}
			if _, err := rq.Snapshot(); !errors.Is(err, sip.ErrProxySnapshotUnsupported) {
				t.Fatalf("rq.Snapshot() error = %v, want %v", err, sip.ErrProxySnapshotUnsupported)
			}
		})
	})

	t.Run("custom transaction without snapshotter forwards but fails capture", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := &sip.ProxyRoute{
				Name:        "snap-nosnap",
				ForwardMode: sip.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
					return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
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
			_, prx, tp := newConfiguredTestProxy(t, route, sip.ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			}, elm)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".snap-nosnap", laddr, raddr)
			if err := prx.InterceptInboundRequest(ctx, noopNext, req); err != nil {
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
			for _, res := range drainPrxRess(tp) {
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
			rq, ok := prx.LookupRequest(srvKey)
			if !ok {
				t.Fatal("prx.LookupRequest() found no owned request")
			}
			if _, err := rq.Snapshot(); !errors.Is(err, sip.ErrProxySnapshotUnsupported) {
				t.Fatalf("rq.Snapshot() error = %v, want %v", err, sip.ErrProxySnapshotUnsupported)
			}
		})
	})

	t.Run("revisions are future-only and unbind is reentrant", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := &sip.ProxyRoute{
				Name:        "snap-rev",
				ForwardMode: sip.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
					return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
						{{ID: "A", URI: snapTargetURI(targetHosts["A"])}},
					}}, nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, sip.ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			})

			var mu sync.Mutex
			var got1 []*sip.ProxyRequestSnapshot
			unbind1 := prx.BindSnapshotHandler(sip.ProxyRequestSnapshotHandlerFunc(
				func(_ context.Context, snap *sip.ProxyRequestSnapshot) {
					mu.Lock()
					got1 = append(got1, snap)
					mu.Unlock()
				},
			))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".snap-rev", laddr, raddr)
			if err := prx.InterceptInboundRequest(ctx, noopNext, req); err != nil {
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
			rq, ok := prx.LookupRequest(srvKey)
			if !ok {
				t.Fatal("prx.LookupRequest() found no owned request")
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
			var got2 []*sip.ProxyRequestSnapshot
			unbind2 := prx.BindSnapshotHandler(sip.ProxyRequestSnapshotHandlerFunc(
				func(_ context.Context, snap *sip.ProxyRequestSnapshot) {
					got2Mx.Lock()
					got2 = append(got2, snap)
					got2Mx.Unlock()
				},
			))

			// A self-unbinding handler proves unbind is reentrant: it removes
			// itself inside the callback and observes exactly one revision.
			var got3 atomic.Int32
			var unbind3 func()
			unbind3 = prx.BindSnapshotHandler(sip.ProxyRequestSnapshotHandlerFunc(
				func(_ context.Context, _ *sip.ProxyRequestSnapshot) {
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
	snap       *sip.ProxyRequestSnapshot
	srvSnap    *sip.ServerTransactionSnapshot
	foreignSrv *sip.ServerTransactionSnapshot
	clnSnap    *sip.ClientTransactionSnapshot
	keyB       sip.ClientTransactionKey
	srvKey     sip.ServerTransactionKey
	delRef     sip.ProxyDeliveryRef
	route      *sip.ProxyRoute
	raddr      netip.AddrPort
}

// newRestoreInviteFixture admits an INVITE on a first proxy, settles
// target A, and captures the request and transaction snapshots used by
// every dormant-restore subtest.
func newRestoreInviteFixture(t *testing.T) *restoreInviteFixture {
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
	delRef := sip.ProxyDeliveryRef{Kind: "gw", Key: "eu-1"}

	var selCalls atomic.Int32
	route := &sip.ProxyRoute{
		Name:        "rest",
		ForwardMode: sip.ForwardModeStateful,
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
			return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
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
		SelectTargets: func(context.Context, sip.ProxySelection) ([]string, error) {
			if selCalls.Add(1) == 1 {
				return []string{"A", "B"}, nil
			}
			// The next group never starts while a branch is active.
			return nil, nil
		},
	}
	elm, prx, tp := newConfiguredTestProxy(t, route, sip.ProxyOptions{
		ServedDomains: []string{"alice.voip.com"},
	})

	ctx := t.Context()
	req := newInInviteReq(t, "UDP", sip.MagicCookie+".rest-invite", laddr, raddr)
	if err := prx.InterceptInboundRequest(ctx, noopNext, req); err != nil {
		t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
	}
	synctest.Wait()

	sends := make(map[string]*sip.RequestEnvelope)
	for _, out := range drainPrxReqs(tp) {
		if out.Method().Equal(sip.RequestMethodInvite) {
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
	rq, ok := prx.LookupRequest(srvKey)
	if !ok {
		t.Fatal("prx.LookupRequest() found no owned request")
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
	if err := prx.InterceptInboundRequest(ctx, noopNext, foreign); err != nil {
		t.Fatalf("prx.InterceptInboundRequest(foreign) error = %v, want nil", err)
	}
	synctest.Wait()
	drainPrxReqs(tp)
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
		t.Fatal("upstream transaction does not implement ServerTransactionSnapshotter")
	}
	foreignSnapper, ok := foreignTx.(sip.ServerTransactionSnapshotter)
	if !ok {
		t.Fatal("foreign upstream transaction does not implement ServerTransactionSnapshotter")
	}
	clnSnapper, ok := txB.(sip.ClientTransactionSnapshotter)
	if !ok {
		t.Fatal("client transaction of target B does not implement ClientTransactionSnapshotter")
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
// against the restore-side transport.
func (f *restoreInviteFixture) dormantBindings(tb testing.TB, tp *prxStubTransp) sip.ProxyRestoreBindings {
	tb.Helper()

	srvTx, err := sip.RestoreServerTransaction(f.srvSnap, tp)
	if err != nil {
		tb.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
	}
	clnTx, err := sip.RestoreClientTransaction(f.clnSnap, tp)
	if err != nil {
		tb.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
	}
	return sip.ProxyRestoreBindings{
		ServerTransaction:  srvTx,
		ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{f.keyB: clnTx},
		Deliveries: map[sip.ProxyDeliveryRef]sip.ProxyBoundDelivery{
			f.delRef: {Transport: tp, RemoteAddr: f.raddr},
		},
	}
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
			_, prx, tp := newConfiguredTestProxy(t, f.route, sip.ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			})

			var callbacks atomic.Int32
			unbind := prx.BindSnapshotHandler(sip.ProxyRequestSnapshotHandlerFunc(
				func(context.Context, *sip.ProxyRequestSnapshot) { callbacks.Add(1) },
			))
			defer unbind()

			rq, err := prx.RestoreRequest(ctx, f.snap, f.dormantBindings(t, tp))
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}
			if rq == nil {
				t.Fatal("prx.RestoreRequest() returned no request")
			}
			if got, ok := prx.LookupRequest(f.srvKey); !ok || got != rq {
				t.Fatal("restored request not indexed by its upstream key")
			}
			synctest.Wait()

			if sends := len(drainPrxReqs(tp)); sends != 0 {
				t.Fatalf("requests sent while dormant=%d, want 0", sends)
			}
			if sends := len(drainPrxRess(tp)); sends != 0 {
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
			_, prx, tp := newConfiguredTestProxy(t, f.route, sip.ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
				ResolveDelivery: func(context.Context, sip.ProxyDeliveryRef) (sip.ProxyBoundDelivery, error) {
					resolverCalls.Add(1)
					return sip.ProxyBoundDelivery{}, errors.ErrorWrap("unexpected resolver call")
				},
			})

			for _, tt := range []struct {
				name  string
				snap  func() *sip.ProxyRequestSnapshot
				binds func() sip.ProxyRestoreBindings
			}{
				{
					name: "unsupported version",
					snap: func() *sip.ProxyRequestSnapshot {
						s := f.snap.Clone()
						s.Version = 99
						return s
					},
				},
				{
					name: "unknown route",
					snap: func() *sip.ProxyRequestSnapshot {
						s := f.snap.Clone()
						s.RouteName = "ghost"
						return s
					},
				},
				{
					name: "mode mismatch",
					snap: func() *sip.ProxyRequestSnapshot {
						s := f.snap.Clone()
						s.Mode = sip.ForwardModeStateless
						return s
					},
				},
				{
					name: "duplicate target progress",
					snap: func() *sip.ProxyRequestSnapshot {
						s := f.snap.Clone()
						s.TargetStates = append(s.TargetStates, s.TargetStates[0])
						return s
					},
				},
				{
					name: "duplicate plan target id",
					snap: func() *sip.ProxyRequestSnapshot {
						s := f.snap.Clone()
						s.TargetPlan.Groups[0] = append(s.TargetPlan.Groups[0], s.TargetPlan.Groups[0][0])
						return s
					},
				},
				{
					name: "invalid deadline on settled attempt",
					snap: func() *sip.ProxyRequestSnapshot {
						s := f.snap.Clone()
						attA := attemptOfTarget(s, "A")
						attA.TimerCDeadline = s.Time
						return s
					},
				},
				{
					name: "missing upstream binding",
					binds: func() sip.ProxyRestoreBindings {
						b := f.dormantBindings(t, tp)
						b.ServerTransaction = nil
						return b
					},
				},
				{
					name: "missing client binding",
					binds: func() sip.ProxyRestoreBindings {
						b := f.dormantBindings(t, tp)
						delete(b.ClientTransactions, f.keyB)
						return b
					},
				},
				{
					name: "conflicting upstream request",
					binds: func() sip.ProxyRestoreBindings {
						b := f.dormantBindings(t, tp)
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
					binds: func() sip.ProxyRestoreBindings {
						b := f.dormantBindings(t, tp)
						attA := attemptOfTarget(f.snap, "A")
						b.ClientTransactions[attA.Key] = b.ClientTransactions[f.keyB]
						return b
					},
				},
				{
					name: "unknown client binding",
					binds: func() sip.ProxyRestoreBindings {
						b := f.dormantBindings(t, tp)
						b.ClientTransactions[sip.ClientTransactionKey{
							Branch: sip.MagicCookie + ".rest-unknown", Method: "INVITE",
						}] = b.ClientTransactions[f.keyB]
						return b
					},
				},
				{
					name: "cancelled outcome without intent",
					snap: func() *sip.ProxyRequestSnapshot {
						s := f.snap.Clone()
						s.Completed = true
						s.Outcome = sip.ProxyOutcomeCancelled
						return s
					},
				},
				{
					name: "forwarded final without candidate",
					snap: func() *sip.ProxyRequestSnapshot {
						s := f.snap.Clone()
						s.FinalForwarded = true
						s.Candidates = nil
						return s
					},
				},
				{
					name: "final status mismatch",
					snap: func() *sip.ProxyRequestSnapshot {
						s := f.snap.Clone()
						s.Completed = true
						s.Outcome = sip.ProxyOutcomeResponse
						s.FinalStatus = sip.ResponseStatusOK
						return s
					},
				},
				{
					name: "missing required flow",
					binds: func() sip.ProxyRestoreBindings {
						b := f.dormantBindings(t, tp)
						b.Deliveries = nil
						return b
					},
				},
			} {
				snap := f.snap
				if tt.snap != nil {
					snap = tt.snap()
				}
				bindings := f.dormantBindings(t, tp)
				if tt.binds != nil {
					bindings = tt.binds()
				}
				if _, err := prx.RestoreRequest(ctx, snap, bindings); err == nil {
					t.Fatalf("incompatible bindings accepted: %s", tt.name)
				}
			}

			// No rejection ever resolved a replacement flow or sent
			// traffic: validation fails before any install.
			if got := resolverCalls.Load(); got != 0 {
				t.Fatalf("ResolveDelivery calls=%d, want 0", got)
			}
			synctest.Wait()
			if sends := len(drainPrxReqs(tp)); sends != 0 {
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

			route := &sip.ProxyRoute{
				Name:        "rest-ni",
				ForwardMode: sip.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
					return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
						{{ID: "A", URI: snapTargetURI("10.0.0.9")}},
					}}, nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, sip.ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			})

			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".rest-info", laddr, raddr)
			if err := prx.InterceptInboundRequest(ctx, noopNext, req); err != nil {
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

			rq, ok := prx.LookupRequest(srvKey)
			if !ok {
				t.Fatal("prx.LookupRequest() found no owned request")
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
				t.Fatal("upstream transaction does not implement ServerTransactionSnapshotter")
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
				t.Fatal("forwarding client transaction does not implement ClientTransactionSnapshotter")
			}
			clnSnap := mustClnTxSnapshot(t, clnSnapper)

			// The restore side enables RFC4320 for NEW transactions: the
			// saved Legacy checkpoint still binds, the dormant binding's
			// own saved mode wins over creation defaults.
			rfcRoute := &sip.ProxyRoute{
				Name:        "rest-ni",
				ForwardMode: sip.ForwardModeStateful,
				ServerTransactionOptions: sip.ServerTransactionOptions{
					NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320,
				},
			}
			_, prx2, tp2 := newConfiguredTestProxy(t, rfcRoute, sip.ProxyOptions{})

			legacyTx, err := sip.RestoreServerTransaction(srvSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
			}
			prvd, ok := legacyTx.(sip.NonInviteServerTransactionBehaviorProvider)
			if !ok {
				t.Fatal("dormant non-INVITE transaction does not implement NonInviteServerTransactionBehaviorProvider")
			}
			if got := prvd.NonInviteServerTransactionBehavior(); got != sip.NonInviteServerTransactionBehaviorLegacy {
				t.Fatalf("dormant binding mode=%v, want Legacy", got)
			}
			cln2, err := sip.RestoreClientTransaction(clnSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
			}
			bindings := sip.ProxyRestoreBindings{
				ServerTransaction:  legacyTx,
				ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{clnKey: cln2},
			}
			if _, err := prx2.RestoreRequest(ctx, snap, bindings); err != nil {
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
			if _, err := prx2.RestoreRequest(ctx, snap, bindings); !errors.Is(err, sip.ErrInvalidProxySnapshot) {
				t.Fatalf("prx.RestoreRequest() error = %v, want %v", err, sip.ErrInvalidProxySnapshot)
			}

			// A binding that cannot report its mode fails explicitly
			// unsupported.
			bindings.ServerTransaction = &noSnapSrvTransact{legacyTx}
			if _, err := prx2.RestoreRequest(ctx, snap, bindings); !errors.Is(err, sip.ErrProxySnapshotUnsupported) {
				t.Fatalf("prx.RestoreRequest() error = %v, want %v", err, sip.ErrProxySnapshotUnsupported)
			}

			// Cancellation is INVITE-only: it can never land on a
			// non-INVITE checkpoint.
			bad := snap.Clone()
			bad.Cancelled = true
			bindings.ServerTransaction = legacyTx
			if _, err := prx2.RestoreRequest(ctx, bad, bindings); err == nil {
				t.Fatal("cancelled non-INVITE checkpoint accepted")
			}
		})
	})

	t.Run("failed install rolls back only its own staged registrations", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreInviteFixture(t)
			_, prx, tp := newConfiguredTestProxy(t, f.route, sip.ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
			})

			var callbacks atomic.Int32
			unbind := prx.BindSnapshotHandler(sip.ProxyRequestSnapshotHandlerFunc(
				func(context.Context, *sip.ProxyRequestSnapshot) { callbacks.Add(1) },
			))
			defer unbind()

			rq1, err := prx.RestoreRequest(ctx, f.snap, f.dormantBindings(t, tp))
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}

			// A second restore of the same upstream identity stages its
			// own bindings, then fails on the index commit: only the
			// staged registrations roll back, the existing entry and the
			// borrowed transactions are untouched.
			bindings2 := f.dormantBindings(t, tp)
			srv2 := bindings2.ServerTransaction
			if _, err := prx.RestoreRequest(ctx, f.snap, bindings2); err == nil {
				t.Fatal("duplicate upstream restore accepted")
			}
			if got, ok := prx.LookupRequest(f.srvKey); !ok || got != rq1 {
				t.Fatal("existing request index entry disturbed by a failed install")
			}
			if srv2.Started() {
				t.Fatal("borrowed transaction started by a failed install")
			}
			if got := srv2.State(); got == sip.TransactionStateTerminated {
				t.Fatal("borrowed transaction terminated by a failed install")
			}
			synctest.Wait()
			if sends := len(drainPrxReqs(tp)) + len(drainPrxRess(tp)); sends != 0 {
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
		_, prx, tp := newConfiguredTestProxy(t, f.route, sip.ProxyOptions{
			ServedDomains: []string{"alice.voip.com"},
			ResolveDelivery: func(context.Context, sip.ProxyDeliveryRef) (sip.ProxyBoundDelivery, error) {
				dialCalls.Add(1)
				return sip.ProxyBoundDelivery{}, errors.ErrorWrap("unexpected replacement dial")
			},
		})

		bindingsMissingFlow := f.dormantBindings(t, tp)
		delete(bindingsMissingFlow.Deliveries, f.delRef)

		_, err := prx.RestoreRequest(ctx, f.snap, bindingsMissingFlow)
		if err == nil {
			t.Fatal("missing bound flow accepted")
		}
		if !errors.Is(err, sip.ErrInvalidProxySnapshot) {
			t.Fatalf("prx.RestoreRequest() error = %v, want %v", err, sip.ErrInvalidProxySnapshot)
		}
		if got := dialCalls.Load(); got != 0 {
			t.Fatalf("dial calls=%d, want 0", got)
		}
		synctest.Wait()
		if sends := len(drainPrxReqs(tp)) + len(drainPrxRess(tp)); sends != 0 {
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
		route := &sip.ProxyRoute{
			Name:        "cache",
			ForwardMode: sip.ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
				return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
					{{
						ID:  "A",
						URI: snapTargetURI("10.0.0.9"),
					}},
				}}, nil
			},
		}
		opts := sip.ProxyOptions{ServedDomains: []string{"alice.voip.com"}}
		elm1, prx1, tp1 := newConfiguredTestProxy(t, route, opts)

		// The inbound request carries no From tag: the forwarded copy must
		// keep it verbatim through the checkpoint round-trip.
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".cache-rest", laddr, raddr)
		req.WithMessage(func(r *sip.Request) {
			if from, ok := r.Headers.From(); ok && from != nil {
				from.Params.Delete("tag")
			}
		})
		if err := prx1.InterceptInboundRequest(ctx, noopNext, req); err != nil {
			t.Fatalf("prx1.InterceptInboundRequest() error = %v, want nil", err)
		}
		synctest.Wait()

		sends := drainPrxReqs(tp1)
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
		fwd := drainPrxRess(tp1)
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
		rq1, ok := prx1.LookupRequest(srvKey)
		if !ok {
			t.Fatal("prx1.LookupRequest() found no owned request")
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
			t.Fatal("upstream transaction does not implement ServerTransactionSnapshotter")
		}
		srvSnap := mustSrvTxSnapshot(t, srvSnapper)
		if !srvSnap.PreserveResponseToTag {
			t.Fatal("server checkpoint lost PreserveResponseToTag for the forwarded tagless 180")
		}
		clnSnapper, ok := clnTx1.(sip.ClientTransactionSnapshotter)
		if !ok {
			t.Fatal("forwarding client transaction does not implement ClientTransactionSnapshotter")
		}
		clnSnap := mustClnTxSnapshot(t, clnSnapper)

		// JSON round-trip every application-coordinated checkpoint.
		var (
			rqJSON  sip.ProxyRequestSnapshot
			srvJSON sip.ServerTransactionSnapshot
			clnJSON sip.ClientTransactionSnapshot
		)
		raw, err := json.Marshal(snap)
		if err != nil {
			t.Fatalf("json.Marshal(snap) error = %v, want nil", err)
		}
		if err := json.Unmarshal(raw, &rqJSON); err != nil {
			t.Fatalf("json.Unmarshal(ProxyRequestSnapshot) error = %v, want nil", err)
		}
		raw, err = json.Marshal(srvSnap)
		if err != nil {
			t.Fatalf("json.Marshal(srvSnap) error = %v, want nil", err)
		}
		if err := json.Unmarshal(raw, &srvJSON); err != nil {
			t.Fatalf("json.Unmarshal(ServerTransactionSnapshot) error = %v, want nil", err)
		}
		raw, err = json.Marshal(clnSnap)
		if err != nil {
			t.Fatalf("json.Marshal(clnSnap) error = %v, want nil", err)
		}
		if err := json.Unmarshal(raw, &clnJSON); err != nil {
			t.Fatalf("json.Unmarshal(ClientTransactionSnapshot) error = %v, want nil", err)
		}
		if !srvJSON.PreserveResponseToTag {
			t.Fatal("JSON round-trip lost PreserveResponseToTag")
		}

		_, prx2, tp2 := newConfiguredTestProxy(t, route, opts)
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
		clnTx2.Request().WithMessage(func(r *sip.Request) {
			sip.EnsureRequestFromTag(r)
			from, _ := r.Headers.From()
			if tag, _ := from.Tag(); tag != "" {
				t.Fatalf("restored forwarded request gained invented From tag %q", tag)
			}
		})

		rq2, err := prx2.RestoreRequest(ctx, &rqJSON, sip.ProxyRestoreBindings{
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
		if got := len(drainPrxReqs(tp2)) + len(drainPrxRess(tp2)); got != 0 {
			t.Fatalf("dormant send count=%d, want 0", got)
		}

		// An upstream INVITE retransmission receives exactly the cached
		// tagless 180: no To tag may be invented for the re-sent copy.
		dup, ok := rqJSON.OriginalRequest.Clone().(*sip.RequestEnvelope)
		if !ok {
			t.Fatal("OriginalRequest.Clone() is not a *sip.RequestEnvelope")
		}
		if err := srvTx2.RecvRequest(ctx, dup); err != nil {
			t.Fatalf("srvTx2.RecvRequest() error = %v, want nil", err)
		}
		synctest.Wait()
		resent := drainPrxRess(tp2)
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
		if got := len(drainPrxReqs(tp2)); got != 0 {
			t.Fatalf("outbound request sends=%d, want 0", got)
		}

		// Settling the restored attempt retires the rebuilt lazy address
		// runner together with the target.
		if err := clnTx2.RecvResponse(ctx, newInRes(t, clnTx2.Request(), sip.ResponseStatusBusyHere)); err != nil {
			t.Fatalf("clnTx2.RecvResponse(486) error = %v, want nil", err)
		}
		synctest.Wait()
		drainPrxRess(tp2)
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
		f := newPrxPromotionFixture()
		loc := statelessFixedLocator{addr: netip.MustParseAddrPort("198.51.100.77:5060")}
		elm1, prx1, tp1 := newConfiguredTestProxy(t, f.route, f.opts, &sip.Element{ServerLocator: loc})
		in := newInInviteReq(t, "UDP", sip.MagicCookie+".promotion-snapshot",
			netip.MustParseAddrPort("192.168.1.100:5060"), netip.MustParseAddrPort("10.0.0.5:5060"))
		original := in.Message().Render()
		fwdIntercept(t, prx1, in)
		synctest.Wait()
		sends := drainPrxReqs(tp1)
		if len(sends) != 1 {
			t.Fatalf("initial sends = %d, want 1", len(sends))
		}
		srvKey, err := sip.MakeServerTransactionKey(in)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		rq1, ok := prx1.LookupRequest(srvKey)
		if !ok {
			t.Fatal("promoted request not indexed by original key")
		}
		snap, err := rq1.Snapshot()
		if err != nil {
			t.Fatalf("rq1.Snapshot() error = %v, want nil", err)
		}
		if snap.Version != 1 || snap.Mode != sip.ForwardModeStateful || f.route.ForwardMode != sip.ForwardModeStateless {
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
		assertPrxPromotionWorking(t, snap.WorkingRequest)
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
			Request *sip.ProxyRequestSnapshot      `json:"request"`
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
		_, prx2, tp2 := newConfiguredTestProxy(t, f.route, f.opts, &sip.Element{ServerLocator: loc})
		rec := &prxEvtRecorder{}
		prx2.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))
		srvTx2, err := sip.RestoreServerTransaction(wire.Server, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		clnTx2, err := sip.RestoreClientTransaction(wire.Client, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
		}
		rq2, err := prx2.RestoreRequest(ctx, saved, sip.ProxyRestoreBindings{
			ServerTransaction:  srvTx2,
			ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{clnKey: clnTx2},
		})
		if err != nil {
			t.Fatalf("prx2.RestoreRequest() error = %v, want nil", err)
		}
		if got, ok := prx2.LookupRequest(srvKey); !ok || got != rq2 {
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
		if len(rec.get()) != 0 || len(drainPrxReqs(tp2))+len(drainPrxRess(tp2)) != 0 {
			t.Fatal("dormant restore emitted effects or historical observations")
		}
		if err := rq2.Start(ctx); err != nil {
			t.Fatalf("rq2.Start() error = %v, want nil", err)
		}
		synctest.Wait()
		if len(rec.get()) != 0 || len(drainPrxReqs(tp2))+len(drainPrxRess(tp2)) != 0 {
			t.Fatal("activation replayed historical sends or observations")
		}
		if err := clnTx2.RecvResponse(ctx, newInRes(t, clnTx2.Request(), sip.ResponseStatusBusyHere)); err != nil {
			t.Fatalf("clnTx2.RecvResponse() error = %v, want nil", err)
		}
		synctest.Wait()
		var failover []*sip.RequestEnvelope
		for _, out := range drainPrxReqs(tp2) {
			if out.Method().Equal(sip.RequestMethodInvite) {
				failover = append(failover, out)
			}
		}
		if len(failover) != 1 || !failover[0].URI().Equal(f.plan.Groups[1][0].URI) {
			t.Fatalf("restored group B sends = %v, want saved backup URI", failover)
		}
		assertPrxPromotionWorking(t, failover[0])
		after, err := rq2.Snapshot()
		if err != nil {
			t.Fatalf("rq2.Snapshot() error = %v, want nil", err)
		}
		if after.OriginalRequest.Message().Render() != original || after.Mode != sip.ForwardModeStateful {
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
				ev.Mode != sip.ForwardModeStateful || ev.Kind == sip.ProxyEventRequestHandled ||
				ev.Kind == sip.ProxyEventRequestForwarded || ev.Kind == sip.ProxyEventRequestCompleted {
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
		route := &sip.ProxyRoute{
			Name:        "work-rt",
			ForwardMode: sip.ForwardModeStateful,
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
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
				return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
					{{ID: "A", URI: snapTargetURI("10.0.0.9")}},
					{{ID: "B", URI: snapTargetURI("10.0.0.10")}},
				}}, nil
			},
		}
		opts := sip.ProxyOptions{ServedDomains: []string{"alice.voip.com"}}
		elm1, prx1, tp1 := newConfiguredTestProxy(t, route, opts)

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".work-rt", laddr, raddr)
		if err := prx1.InterceptInboundRequest(ctx, noopNext, req); err != nil {
			t.Fatalf("prx1.InterceptInboundRequest() error = %v, want nil", err)
		}
		synctest.Wait()

		sends := drainPrxReqs(tp1)
		if got := len(sends); got != 1 {
			t.Fatalf("initial outbound sends=%d, want 1", got)
		}
		assertReqRouteHop(t, sends[0], "192.0.2.77")
		if got := hookCalls.Load(); got != 1 {
			t.Fatalf("PreprocessRoute calls=%d, want 1", got)
		}

		srvKey, err := sip.MakeServerTransactionKey(req)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		rq1, ok := prx1.LookupRequest(srvKey)
		if !ok {
			t.Fatal("prx1.LookupRequest() found no owned request")
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
			t.Fatal("upstream transaction does not implement ServerTransactionSnapshotter")
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
			t.Fatal("forwarding client transaction does not implement ClientTransactionSnapshotter")
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
		var rqJSON sip.ProxyRequestSnapshot
		if err := json.Unmarshal(raw, &rqJSON); err != nil {
			t.Fatalf("json.Unmarshal(ProxyRequestSnapshot) error = %v, want nil", err)
		}

		_, prx2, tp2 := newConfiguredTestProxy(t, route, opts)
		srvTx2, err := sip.RestoreServerTransaction(srvSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		clnTx2, err := sip.RestoreClientTransaction(clnSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
		}
		rq2, err := prx2.RestoreRequest(ctx, &rqJSON, sip.ProxyRestoreBindings{
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
		if got := len(drainPrxReqs(tp2)) + len(drainPrxRess(tp2)); got != 0 {
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
		for _, out := range drainPrxReqs(tp2) {
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
			t.Fatalf("PreprocessRoute calls after restore=%d, want 1 (the hook is never replayed)", got)
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

		route := &sip.ProxyRoute{
			Name:        "work-miss",
			ForwardMode: sip.ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
				return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
					{{ID: "A", URI: snapTargetURI("10.0.0.9")}},
				}}, nil
			},
		}
		opts := sip.ProxyOptions{ServedDomains: []string{"alice.voip.com"}}
		elm1, prx1, tp1 := newConfiguredTestProxy(t, route, opts)

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".work-miss", laddr, raddr)
		if err := prx1.InterceptInboundRequest(ctx, noopNext, req); err != nil {
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
		rq1, ok := prx1.LookupRequest(srvKey)
		if !ok {
			t.Fatal("prx1.LookupRequest() found no owned request")
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
			t.Fatal("upstream transaction does not implement ServerTransactionSnapshotter")
		}
		srvSnap := mustSrvTxSnapshot(t, srvSnapper)
		clnSnapper, ok := clnTx1.(sip.ClientTransactionSnapshotter)
		if !ok {
			t.Fatal("forwarding client transaction does not implement ClientTransactionSnapshotter")
		}
		clnSnap := mustClnTxSnapshot(t, clnSnapper)

		// A wire checkpoint missing the working request decodes with a
		// nil field; the version-1 contract rejects it.
		raw, err := json.Marshal(snap)
		if err != nil {
			t.Fatalf("json.Marshal(snap) error = %v, want nil", err)
		}
		var rqJSON sip.ProxyRequestSnapshot
		if err := json.Unmarshal(raw, &rqJSON); err != nil {
			t.Fatalf("json.Unmarshal(ProxyRequestSnapshot) error = %v, want nil", err)
		}
		rqJSON.WorkingRequest = nil

		_, prx2, tp2 := newConfiguredTestProxy(t, route, opts)
		srvTx2, err := sip.RestoreServerTransaction(srvSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		clnTx2, err := sip.RestoreClientTransaction(clnSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
		}
		_, err = prx2.RestoreRequest(ctx, &rqJSON, sip.ProxyRestoreBindings{
			ServerTransaction:  srvTx2,
			ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{clnKey: clnTx2},
		})
		if !errors.Is(err, sip.ErrInvalidProxySnapshot) {
			t.Fatalf("prx2.RestoreRequest() error = %v, want %v", err, sip.ErrInvalidProxySnapshot)
		}
		if got, ok := prx2.LookupRequest(srvKey); ok || got != nil {
			t.Fatal("rejected restore installed an upstream index entry")
		}
		synctest.Wait()
		if sends := len(drainPrxReqs(tp2)) + len(drainPrxRess(tp2)); sends != 0 {
			t.Fatalf("sends by a rejected restore=%d, want 0", sends)
		}
	})
}

func TestProxy_RestoreRequest_RequestValueValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		mutate                 func(*sip.ProxyRequestSnapshot)
		invalidMessage         bool
		invalidOriginalMessage bool
		wantErr                error
		wantCause              error
	}{
		{
			name: "missing via",
			mutate: func(snap *sip.ProxyRequestSnapshot) {
				snap.WorkingRequest.WithMessage(func(r *sip.Request) { r.Headers.Delete("Via") })
			},
			invalidMessage: true,
			wantErr:        sip.ErrInvalidProxySnapshot,
			wantCause:      sip.ErrInvalidMessage,
		},
		{
			name: "invalid request uri",
			mutate: func(snap *sip.ProxyRequestSnapshot) {
				snap.WorkingRequest.WithMessage(func(r *sip.Request) { r.URI = nil })
			},
			invalidMessage: true,
			wantErr:        sip.ErrInvalidProxySnapshot,
			wantCause:      sip.ErrInvalidMessage,
		},
		{
			name: "invalid protocol",
			mutate: func(snap *sip.ProxyRequestSnapshot) {
				snap.WorkingRequest.WithMessage(func(r *sip.Request) { r.Proto = sip.ProtoInfo{} })
			},
			invalidMessage: true,
			wantErr:        sip.ErrInvalidProxySnapshot,
			wantCause:      sip.ErrInvalidMessage,
		},
		{
			name: "method mismatch",
			mutate: func(snap *sip.ProxyRequestSnapshot) {
				snap.WorkingRequest.WithMessage(func(r *sip.Request) {
					r.Method = sip.RequestMethodInfo
					cseq, _ := r.Headers.CSeq()
					cseq.Method = sip.RequestMethodInfo
				})
			},
			wantErr: sip.ErrInvalidProxySnapshot,
		},
		{
			name: "opaque original metadata",
			mutate: func(snap *sip.ProxyRequestSnapshot) {
				snap.OriginalRequest.Metadata().Set("restore.opaque", make(chan int))
			},
			wantErr: sip.ErrProxySnapshotUnsupported,
		},
		{
			name: "opaque working metadata",
			mutate: func(snap *sip.ProxyRequestSnapshot) {
				snap.WorkingRequest.Metadata().Set("restore.opaque", make(chan int))
			},
			wantErr: sip.ErrProxySnapshotUnsupported,
		},
		{
			name: "serializable metadata",
			mutate: func(snap *sip.ProxyRequestSnapshot) {
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
			mutate: func(snap *sip.ProxyRequestSnapshot) {
				snap.WorkingRequest = &sip.RequestEnvelope{}
			},
			invalidMessage: true,
			wantErr:        sip.ErrInvalidProxySnapshot,
		},
		{
			name: "missing original via",
			mutate: func(snap *sip.ProxyRequestSnapshot) {
				snap.OriginalRequest.WithMessage(func(r *sip.Request) { r.Headers.Delete("Via") })
			},
			invalidOriginalMessage: true,
			wantErr:                sip.ErrInvalidProxySnapshot,
			wantCause:              sip.ErrInvalidMessage,
		},
		{
			name: "invalid original request uri",
			mutate: func(snap *sip.ProxyRequestSnapshot) {
				snap.OriginalRequest.WithMessage(func(r *sip.Request) { r.URI = nil })
			},
			invalidOriginalMessage: true,
			wantErr:                sip.ErrInvalidProxySnapshot,
			wantCause:              sip.ErrInvalidMessage,
		},
		{
			name: "invalid original protocol",
			mutate: func(snap *sip.ProxyRequestSnapshot) {
				snap.OriginalRequest.WithMessage(func(r *sip.Request) { r.Proto = sip.ProtoInfo{} })
			},
			invalidOriginalMessage: true,
			wantErr:                sip.ErrInvalidProxySnapshot,
			wantCause:              sip.ErrInvalidMessage,
		},
		{
			name: "empty original envelope",
			mutate: func(snap *sip.ProxyRequestSnapshot) {
				snap.OriginalRequest = &sip.RequestEnvelope{}
			},
			invalidOriginalMessage: true,
			wantErr:                sip.ErrInvalidProxySnapshot,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				var hookCalls, resolveCalls atomic.Int32
				route := &sip.ProxyRoute{
					Name:        "restore-values",
					ForwardMode: sip.ForwardModeStateful,
					PreprocessRoute: func(context.Context, *sip.RequestEnvelope) error {
						hookCalls.Add(1)
						return nil
					},
					ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
						resolveCalls.Add(1)
						return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
							{{ID: "A", URI: snapTargetURI("10.0.0.9")}},
						}}, nil
					},
				}
				opts := sip.ProxyOptions{ServedDomains: []string{"alice.voip.com"}}
				elm1, prx1, tp1 := newConfiguredTestProxy(t, route, opts)
				req := newInInviteReq(t, "UDP", sip.MagicCookie+".restore-values",
					netip.MustParseAddrPort("192.168.1.100:5060"), netip.MustParseAddrPort("10.0.0.5:5060"))
				noopNext := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil })
				if err := prx1.InterceptInboundRequest(ctx, noopNext, req); err != nil {
					t.Fatalf("prx1.InterceptInboundRequest() error = %v, want nil", err)
				}
				synctest.Wait()
				if got := hookCalls.Load(); got != 1 {
					t.Fatalf("source PreprocessRoute calls = %d, want 1", got)
				}
				if got := resolveCalls.Load(); got != 1 {
					t.Fatalf("source ResolveRequestTargets calls = %d, want 1", got)
				}
				sends := drainPrxReqs(tp1)
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
				rq1, ok := prx1.LookupRequest(srvKey)
				if !ok {
					t.Fatal("prx1.LookupRequest() found no owned request")
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
					t.Fatal("upstream transaction does not implement ServerTransactionSnapshotter")
				}
				srvSnap := mustSrvTxSnapshot(t, srvSnapper)
				clnSnapper, ok := clnTx1.(sip.ClientTransactionSnapshotter)
				if !ok {
					t.Fatal("forwarding client transaction does not implement ClientTransactionSnapshotter")
				}
				clnSnap := mustClnTxSnapshot(t, clnSnapper)

				raw, err := json.Marshal(snap)
				if err != nil {
					t.Fatalf("json.Marshal(snap) error = %v, want nil", err)
				}
				snap = new(sip.ProxyRequestSnapshot)
				if err := json.Unmarshal(raw, snap); err != nil {
					t.Fatalf("json.Unmarshal(ProxyRequestSnapshot) error = %v, want nil", err)
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

				_, prx, tp := newConfiguredTestProxy(t, route, opts)
				srvTx, err := sip.RestoreServerTransaction(srvSnap, tp)
				if err != nil {
					t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
				}
				clnTx, err := sip.RestoreClientTransaction(clnSnap, tp)
				if err != nil {
					t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
				}
				bindings := sip.ProxyRestoreBindings{
					ServerTransaction:  srvTx,
					ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{clnKey: clnTx},
				}
				var events, revisions atomic.Int32
				unbindEvents := prx.BindEventHandler(sip.ProxyEventHandlerFunc(
					func(context.Context, sip.ProxyEvent) { events.Add(1) },
				))
				defer unbindEvents()
				unbindSnapshots := prx.BindSnapshotHandler(sip.ProxyRequestSnapshotHandlerFunc(
					func(context.Context, *sip.ProxyRequestSnapshot) { revisions.Add(1) },
				))
				defer unbindSnapshots()

				rq, err := prx.RestoreRequest(t.Context(), snap, bindings)
				if tt.wantErr != nil {
					if rq != nil || !errors.Is(err, tt.wantErr) {
						t.Fatalf("prx.RestoreRequest() = (%v, %v), want (nil, %v)", rq, err, tt.wantErr)
					}
					if !errors.Is(err, sip.ErrClassProxy) {
						t.Fatalf("prx.RestoreRequest() error = %v, want proxy classification", err)
					}
					if tt.wantCause != nil && !errors.Is(err, tt.wantCause) {
						t.Fatalf("prx.RestoreRequest() error = %v, want cause %v", err, tt.wantCause)
					}
					if got, ok := prx.LookupRequest(snap.UpstreamKey); ok || got != nil {
						t.Fatal("rejected restore installed an upstream index entry")
					}
				} else {
					if rq == nil || err != nil {
						t.Fatalf("prx.RestoreRequest() = (%v, %v), want a dormant request and nil", rq, err)
					}
					if got, ok := prx.LookupRequest(snap.UpstreamKey); !ok || got != rq {
						t.Fatal("valid restore did not install its upstream index entry")
					}
				}
				synctest.Wait()
				if got := len(drainPrxReqs(tp)) + len(drainPrxRess(tp)); got != 0 {
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
