package sip

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/sip/header"
)

func TestProxyRequestSnapshot_UnstableTransition(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		route := ProxyRoute{
			Name:        "snap-unstable",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		_, prx, _ := newInternalTestProxy(t, route, ProxyOptions{
			ServedDomains: []string{"alice.voip.com"},
		})

		// A coordinator before the committed plan has no stable ownership
		// boundary to capture.
		rq := newPrxReq(ctx, prx, &route, newInternalInviteReq(MagicCookie+".snap-unstable"))
		if _, err := rq.Snapshot(); !errors.Is(err, errPrxSnapshotUnstable) {
			t.Fatalf("rq.Snapshot() error = %v, want %v", err, errPrxSnapshotUnstable)
		}

		// Once admitted, capture works.
		rq2 := admitInternalReq(t, prx, newInternalInviteReq(MagicCookie+".snap-admitted"))
		synctest.Wait()
		if _, err := rq2.Snapshot(); err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}

		// A pending client-transaction admission is a partial branch
		// decision: capture and publication are refused while it is
		// unresolved.
		rq2.mu.Lock()
		rq2.pendingAdms[42] = &prxAdmission{targetID: "A", gen: 42}
		rq2.mu.Unlock()

		if _, err := rq2.Snapshot(); !errors.Is(err, errPrxSnapshotUnstable) {
			t.Fatalf("rq.Snapshot() error = %v, want %v", err, errPrxSnapshotUnstable)
		}

		var delivered atomic.Int32
		unbind := prx.BindSnapshotHandler(ProxyRequestSnapshotHandlerFunc(
			func(context.Context, *ProxyRequestSnapshot) { delivered.Add(1) },
		))
		rq2.enqueue(ctx, prxWorkEvent{kind: prxEvtSrvErr, err: errors.ErrorWrap("test")})
		synctest.Wait()
		if got := delivered.Load(); got != 0 {
			t.Fatalf("snapshot revisions published during an unstable transition=%d, want 0", got)
		}
		unbind()

		rq2.mu.Lock()
		delete(rq2.pendingAdms, 42)
		rq2.mu.Unlock()

		if _, err := rq2.Snapshot(); err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
	})
}

func TestProxyRequestSnapshot_PublishedRevision(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		route := ProxyRoute{
			Name:        "snap-pub",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		elm, prx, tp := newInternalTestProxy(t, route, ProxyOptions{
			ServedDomains: []string{"alice.voip.com"},
		})

		var revs []uint64
		unbind := prx.BindSnapshotHandler(ProxyRequestSnapshotHandlerFunc(
			func(_ context.Context, snap *ProxyRequestSnapshot) {
				revs = append(revs, snap.Revision)
			},
		))
		defer unbind()

		rq := admitInternalReq(t, prx, newInternalInviteReq(MagicCookie+".snap-pub"))
		synctest.Wait()

		if len(revs) == 0 {
			t.Fatal("no snapshot revisions published")
		}
		for i := 1; i < len(revs); i++ {
			if revs[i] <= revs[i-1] {
				t.Fatalf("published revisions=%v, want strictly increasing", revs)
			}
		}

		outReq := testutil.WaitResult(t, tp.sendReqCalls, "outbound branch send")
		key, err := MakeClientTransactionKey(outReq)
		if err != nil {
			t.Fatalf("MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx, ok := elm.LoadClientTransaction(key)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}

		// A committed downstream response publishes a higher revision.
		if err := clnTx.RecvResponse(ctx, newInternalInRes(t, outReq, ResponseStatusRinging)); err != nil {
			t.Fatalf("clnTx.RecvResponse() error = %v, want nil", err)
		}
		synctest.Wait()

		if got, want := revs[len(revs)-1], revs[0]; got <= want {
			t.Fatalf("last published revision=%d, want > %d", got, want)
		}

		snap, err := rq.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		if snap.Revision != revs[len(revs)-1] {
			t.Fatalf("direct snapshot revision=%d, want last published %d", snap.Revision, revs[len(revs)-1])
		}
	})
}

func TestProxyRequestSnapshot_CloneIsIndependent(t *testing.T) {
	t.Parallel()

	snap := &ProxyRequestSnapshot{
		Version:   prxReqSnapshotVer,
		Revision:  7,
		RouteName: "r",
		TargetPlan: ProxyTargetPlan{Groups: [][]ProxyTarget{
			{{
				ID:         "A",
				URI:        &URI{User: MakeUserInfo("alice"), Addr: MakeHostPortAddr("10.0.0.9", 5060)},
				Attributes: map[string]string{"k": "v"},
			}},
		}},
		TargetStates: []ProxyTargetSnapshot{
			{TargetID: "A", Started: true, AttemptedAddresses: []ResolvedAddr{{Transport: "UDP"}}},
		},
		WorkingRequest: newInternalInviteReq(MagicCookie + ".snap-clone-work"),
	}
	clone := snap.Clone()
	clone.TargetPlan.Groups[0][0].Attributes["k"] = "evil"
	clone.TargetStates[0].AttemptedAddresses = nil
	clone.WorkingRequest.WithMessage(func(r *Request) {
		r.Headers.AppendRoute(header.RouteHop{URI: &URI{Addr: MakeHostAddr("evil.example")}})
	})
	if snap.TargetPlan.Groups[0][0].Attributes["k"] != "v" {
		t.Fatal("mutating the clone changed the original snapshot")
	}
	if len(snap.TargetStates[0].AttemptedAddresses) != 1 {
		t.Fatal("mutating the clone changed the original snapshot")
	}
	snap.WorkingRequest.WithMessage(func(r *Request) {
		if _, ok := r.Headers.FirstRoute(); ok {
			t.Fatal("mutating the clone changed the original snapshot")
		}
	})
}

func TestProxy_RestoreRequest_DormantInstall(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		route := ProxyRoute{
			Name:        "rest",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		opts := ProxyOptions{ServedDomains: []string{"alice.voip.com"}}
		elm1, prx1, tp1 := newInternalTestProxy(t, route, opts)

		inReq := newInternalInviteReq(MagicCookie + ".rest-install")
		rq1 := admitInternalReq(t, prx1, inReq)
		synctest.Wait()

		outReq := testutil.WaitResult(t, tp1.sendReqCalls, "outbound request send")
		clnKey, err := MakeClientTransactionKey(outReq)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx1, ok := elm1.LoadClientTransaction(clnKey)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}
		srvKey, err := MakeServerTransactionKey(inReq)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		snap, err := rq1.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		srvSnapper, ok := rq1.srvTx.(ServerTransactionSnapshotter)
		if !ok {
			t.Fatal("upstream transaction does not implement ServerTransactionSnapshotter")
		}
		srvSnap, err := srvSnapper.Snapshot()
		if err != nil {
			t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
		}
		clnSnapper, ok := clnTx1.(ClientTransactionSnapshotter)
		if !ok {
			t.Fatal("client transaction does not implement ClientTransactionSnapshotter")
		}
		clnSnap, err := clnSnapper.Snapshot()
		if err != nil {
			t.Fatalf("clnTx.Snapshot() error = %v, want nil", err)
		}

		_, prx2, tp2 := newInternalTestProxy(t, route, opts)
		srvTx, err := RestoreServerTransaction(srvSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		clnTx, err := RestoreClientTransaction(clnSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
		}

		rq2, err := prx2.RestoreRequest(ctx, snap, ProxyRestoreBindings{
			ServerTransaction:  srvTx,
			ClientTransactions: map[ClientTransactionKey]ClientTransaction{clnKey: clnTx},
		})
		if err != nil {
			t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
		}

		rq2.mu.Lock()
		dormant, begun := rq2.dormant, rq2.begun
		att := rq2.attempts[clnKey]
		st := rq2.targets["A"]
		pending := len(rq2.pendingAdms)
		unbinds := len(rq2.unbinds)
		srvHeld := rq2.srvTx
		timerC := st.timeC
		deliveries := rq2.deliveries
		rq2.mu.Unlock()

		if !dormant || !begun {
			t.Fatalf("restored request=(dormant=%t, begun=%t), want (true, true)", dormant, begun)
		}
		if srvHeld != srvTx {
			t.Fatal("upstream binding not installed on the coordinator")
		}
		if att == nil || att.tx != clnTx {
			t.Fatal("client transaction binding not installed on the attempt")
		}
		if att.gen == 0 || st.gen == 0 {
			t.Fatal("restored attempt and target carry no runtime generations")
		}
		if !st.txKey.Equal(clnKey) {
			t.Fatalf("target txKey=%v, want %v", st.txKey, clnKey)
		}
		if timerC.IsZero() {
			t.Fatal("live INVITE attempt lost its Timer C deadline")
		}
		if pending != 0 {
			t.Fatalf("pending admissions=%d, want 0", pending)
		}
		if unbinds != 5 {
			t.Fatalf("staged unbinds=%d, want 5 (2 upstream + 3 downstream)", unbinds)
		}
		if deliveries != nil {
			t.Fatalf("restored deliveries=%v, want nil for ordinary targets", deliveries)
		}
		if got, ok := prx2.loadSrvReq(srvKey); !ok || got != rq2 {
			t.Fatal("restored request not indexed by its upstream key")
		}

		// The dormant callback queue cannot drain: a queued event waits
		// for activation without scheduling a drain owner.
		rq2.enqueue(ctx, prxWorkEvent{kind: prxEvtSrvErr, err: errors.ErrorWrap("test")})
		synctest.Wait()
		rq2.mu.Lock()
		queued, draining := len(rq2.queue), rq2.draining
		srvErr := rq2.srvErr
		rq2.mu.Unlock()
		if queued != 1 || draining {
			t.Fatalf("dormant queue=(queued=%d, draining=%t), want (1, false)", queued, draining)
		}
		if srvErr != nil {
			t.Fatal("dormant queue drained a staged event")
		}
	})
}

func TestProxy_RestoreRequest_InvalidValuesLeaveBindingsUntouched(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*ProxyRequestSnapshot)
		wantErr error
	}{
		{
			name: "missing via",
			mutate: func(snap *ProxyRequestSnapshot) {
				snap.WorkingRequest.WithMessage(func(r *Request) { r.Headers.Delete("Via") })
			},
			wantErr: ErrInvalidProxySnapshot,
		},
		{
			name: "opaque original metadata",
			mutate: func(snap *ProxyRequestSnapshot) {
				snap.OriginalRequest.Metadata().Set("restore.opaque", make(chan int))
			},
			wantErr: ErrProxySnapshotUnsupported,
		},
		{
			name: "opaque working metadata",
			mutate: func(snap *ProxyRequestSnapshot) {
				snap.WorkingRequest.Metadata().Set("restore.opaque", make(chan int))
			},
			wantErr: ErrProxySnapshotUnsupported,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				route := ProxyRoute{
					Name:        "restore-values",
					ForwardMode: ForwardModeStateful,
					ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
						return newInternalLitPlan(), nil
					},
				}
				opts := ProxyOptions{ServedDomains: []string{"alice.voip.com"}}
				elm1, prx1, tp1 := newInternalTestProxy(t, route, opts)
				rq1 := admitInternalReq(t, prx1, newInternalInviteReq(MagicCookie+".restore-values"))
				synctest.Wait()
				outReq := testutil.WaitResult(t, tp1.sendReqCalls, "outbound request send")
				clnKey, err := MakeClientTransactionKey(outReq)
				if err != nil {
					t.Fatalf("MakeClientTransactionKey() error = %v, want nil", err)
				}
				clnTx1, ok := elm1.LoadClientTransaction(clnKey)
				if !ok {
					t.Fatal("forwarding client transaction not registered")
				}
				snap, err := rq1.Snapshot()
				if err != nil {
					t.Fatalf("rq1.Snapshot() error = %v, want nil", err)
				}
				srvSnap, err := rq1.srvTx.(ServerTransactionSnapshotter).Snapshot()
				if err != nil {
					t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
				}
				clnSnap, err := clnTx1.(ClientTransactionSnapshotter).Snapshot()
				if err != nil {
					t.Fatalf("clnTx.Snapshot() error = %v, want nil", err)
				}

				_, prx, tp := newInternalTestProxy(t, route, opts)
				controlReq := newInternalInviteReq(MagicCookie + ".restore-values-control")
				control := admitInternalReq(t, prx, controlReq)
				synctest.Wait()
				controlKey, err := MakeServerTransactionKey(controlReq)
				if err != nil {
					t.Fatalf("MakeServerTransactionKey(control) error = %v, want nil", err)
				}
				if got, ok := prx.loadSrvReq(controlKey); !ok || got != control {
					t.Fatal("control request not indexed before rejection")
				}
				if got := len(drainInternalReqs(tp)); got != 1 {
					t.Fatalf("control request sends = %d, want 1", got)
				}
				drainInternalRess(tp)

				srv, err := RestoreInviteServerTransaction(srvSnap, tp)
				if err != nil {
					t.Fatalf("RestoreInviteServerTransaction() error = %v, want nil", err)
				}
				cln, err := RestoreInviteClientTransaction(clnSnap, tp)
				if err != nil {
					t.Fatalf("RestoreInviteClientTransaction() error = %v, want nil", err)
				}
				bindings := ProxyRestoreBindings{
					ServerTransaction:  srv,
					ClientTransactions: map[ClientTransactionKey]ClientTransaction{clnKey: cln},
				}
				srvHandlers := srv.onStateChanged.Len() + srv.onErr.Len()
				clnHandlers := cln.onStateChanged.Len() + cln.onErr.Len() + cln.onRes.Len()
				var observations atomic.Int32
				unbindEvents := prx.BindEventHandler(ProxyEventHandlerFunc(
					func(context.Context, ProxyEvent) { observations.Add(1) },
				))
				defer unbindEvents()
				unbindSnapshots := prx.BindSnapshotHandler(ProxyRequestSnapshotHandlerFunc(
					func(context.Context, *ProxyRequestSnapshot) { observations.Add(1) },
				))
				defer unbindSnapshots()

				valid := snap.Clone()
				tt.mutate(snap)
				rq, err := prx.RestoreRequest(ctx, snap, bindings)
				if rq != nil || !errors.Is(err, tt.wantErr) {
					t.Fatalf("prx.RestoreRequest() = (%v, %v), want (nil, %v)", rq, err, tt.wantErr)
				}
				synctest.Wait()
				if got := srv.onStateChanged.Len() + srv.onErr.Len(); got != srvHandlers {
					t.Fatalf("upstream handlers after rejection = %d, want %d", got, srvHandlers)
				}
				if got := cln.onStateChanged.Len() + cln.onErr.Len() + cln.onRes.Len(); got != clnHandlers {
					t.Fatalf("downstream handlers after rejection = %d, want %d", got, clnHandlers)
				}
				if srv.started || cln.started {
					t.Fatal("rejected restore started a borrowed transaction")
				}
				if srv.isTerminated() || cln.isTerminated() {
					t.Fatal("rejected restore terminated a borrowed transaction")
				}
				if got, ok := prx.loadSrvReq(snap.UpstreamKey); ok || got != nil {
					t.Fatal("rejected restore installed a candidate upstream index entry")
				}
				if got, ok := prx.loadSrvReq(controlKey); !ok || got != control {
					t.Fatal("rejected restore disturbed the unrelated control request")
				}
				if got := len(drainInternalReqs(tp)) + len(drainInternalRess(tp)); got != 0 {
					t.Fatalf("rejected restore sends = %d, want 0", got)
				}
				if got := observations.Load(); got != 0 {
					t.Fatalf("rejected restore observations = %d, want 0", got)
				}

				// Repair only the saved values and reuse the exact borrowed
				// transactions: failed validation must not consume ownership.
				snap.OriginalRequest = valid.OriginalRequest
				snap.WorkingRequest = valid.WorkingRequest
				rq, err = prx.RestoreRequest(ctx, snap, bindings)
				if rq == nil || err != nil {
					t.Fatalf("prx.RestoreRequest(repaired) = (%v, %v), want a dormant request and nil", rq, err)
				}
				synctest.Wait()
				rq.mu.Lock()
				dormant := rq.dormant
				boundSrv := rq.srvTx
				boundCln := rq.attempts[clnKey].tx
				rq.mu.Unlock()
				if !dormant || boundSrv != srv || boundCln != cln {
					t.Fatal("repaired restore did not adopt the same bindings dormant")
				}
				if got, ok := prx.loadSrvReq(snap.UpstreamKey); !ok || got != rq {
					t.Fatal("repaired restore did not index its coordinator")
				}
				if srv.started || cln.started || srv.isTerminated() || cln.isTerminated() {
					t.Fatal("repaired dormant restore started or terminated a borrowed transaction")
				}
				if got, ok := prx.loadSrvReq(controlKey); !ok || got != control {
					t.Fatal("repaired restore disturbed the unrelated control request")
				}
				if got := len(drainInternalReqs(tp)) + len(drainInternalRess(tp)); got != 0 {
					t.Fatalf("repaired dormant restore sends = %d, want 0", got)
				}
				if got := observations.Load(); got != 0 {
					t.Fatalf("repaired dormant restore observations = %d, want 0", got)
				}
			})
		})
	}
}

func TestProxy_RestoreRequest_RollbackOnlyStaged(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		route := ProxyRoute{
			Name:        "rest-rb",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		opts := ProxyOptions{ServedDomains: []string{"alice.voip.com"}}
		elm1, prx1, tp1 := newInternalTestProxy(t, route, opts)

		inReq := newInternalInviteReq(MagicCookie + ".rest-rollback")
		rq1 := admitInternalReq(t, prx1, inReq)
		synctest.Wait()

		outReq := testutil.WaitResult(t, tp1.sendReqCalls, "outbound request send")
		clnKey, err := MakeClientTransactionKey(outReq)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx1, ok := elm1.LoadClientTransaction(clnKey)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}
		srvKey, err := MakeServerTransactionKey(inReq)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		snap, err := rq1.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		srvSnap, err := rq1.srvTx.(ServerTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
		}
		clnSnap, err := clnTx1.(ClientTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("clnTx.Snapshot() error = %v, want nil", err)
		}

		_, prx2, tp2 := newInternalTestProxy(t, route, opts)
		mkBindings := func() ProxyRestoreBindings {
			srvTx, err := RestoreInviteServerTransaction(srvSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreInviteServerTransaction() error = %v, want nil", err)
			}
			clnTx, err := RestoreInviteClientTransaction(clnSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
			}
			return ProxyRestoreBindings{
				ServerTransaction:  srvTx,
				ClientTransactions: map[ClientTransactionKey]ClientTransaction{clnKey: clnTx},
			}
		}

		rqFirst, err := prx2.RestoreRequest(ctx, snap, mkBindings())
		if err != nil {
			t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
		}

		second := mkBindings()
		srv2 := second.ServerTransaction.(*InviteServerTransaction)
		cln2 := second.ClientTransactions[clnKey].(*InviteClientTransaction)
		srv2Handlers := srv2.onStateChanged.Len() + srv2.onErr.Len()
		cln2Handlers := cln2.onStateChanged.Len() + cln2.onErr.Len() + cln2.onRes.Len()
		if _, err := prx2.RestoreRequest(ctx, snap, second); err == nil {
			t.Fatal("duplicate upstream restore accepted")
		}

		if got, ok := prx2.loadSrvReq(srvKey); !ok || got != rqFirst {
			t.Fatal("failed install disturbed the existing index entry")
		}
		if got := srv2.onStateChanged.Len() + srv2.onErr.Len(); got != srv2Handlers {
			t.Fatalf("upstream handlers=%d, want %d (staged unbinds rolled back)", got, srv2Handlers)
		}
		if got := cln2.onStateChanged.Len() + cln2.onErr.Len() + cln2.onRes.Len(); got != cln2Handlers {
			t.Fatalf("downstream handlers=%d, want %d (staged unbinds rolled back)", got, cln2Handlers)
		}
		if srv2.started || cln2.started {
			t.Fatal("borrowed transaction started by a failed install")
		}
		if srv2.isTerminated() || cln2.isTerminated() {
			t.Fatal("borrowed transaction terminated by a failed install")
		}
	})
}

func TestProxy_RestoreRequest_CompletedAttemptBindingIsolation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		route := ProxyRoute{
			Name:        "rest-iso",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		opts := ProxyOptions{ServedDomains: []string{"alice.voip.com"}}
		elm1, prx1, tp1 := newInternalTestProxy(t, route, opts)

		inReq := newInternalInviteReq(MagicCookie + ".rest-iso")
		rq1 := admitInternalReq(t, prx1, inReq)
		synctest.Wait()
		testutil.WaitResult(t, tp1.sendReqCalls, "outbound request send")

		snap, err := rq1.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		srvSnap, err := rq1.srvTx.(ServerTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
		}

		// Two real client transactions stand in for the failover history of
		// target A: the settled attempt committed a 503, the live sibling
		// is still Proceeding on its provisional response.
		out1 := newInternalInviteReq(MagicCookie + ".rest-iso-a1")
		cln1, err := elm1.NewClientTransaction(ctx, out1, tp1)
		if err != nil {
			t.Fatalf("elm.NewClientTransaction() error = %v, want nil", err)
		}
		if err := cln1.Start(ctx); err != nil {
			t.Fatalf("cln1.Start() error = %v, want nil", err)
		}
		res503 := newInternalInRes(t, cln1.Request(), ResponseStatusServiceUnavailable)
		if err := cln1.RecvResponse(ctx, res503); err != nil {
			t.Fatalf("cln1.RecvResponse(503) error = %v, want nil", err)
		}
		out2 := newInternalInviteReq(MagicCookie + ".rest-iso-a2")
		cln2, err := elm1.NewClientTransaction(ctx, out2, tp1)
		if err != nil {
			t.Fatalf("elm.NewClientTransaction() error = %v, want nil", err)
		}
		if err := cln2.Start(ctx); err != nil {
			t.Fatalf("cln2.Start() error = %v, want nil", err)
		}
		res180 := newInternalInRes(t, cln2.Request(), ResponseStatusRinging)
		if err := cln2.RecvResponse(ctx, res180); err != nil {
			t.Fatalf("cln2.RecvResponse(180) error = %v, want nil", err)
		}
		synctest.Wait()

		clnSnap1, err := cln1.(ClientTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("cln1.Snapshot() error = %v, want nil", err)
		}
		clnSnap2, err := cln2.(ClientTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("cln2.Snapshot() error = %v, want nil", err)
		}

		deadline := snap.Time.Add(time.Hour)
		snap.TargetStates = []ProxyTargetSnapshot{{
			TargetID:           "A",
			Started:            true,
			AttemptedAddresses: []ResolvedAddr{{Transport: "UDP", Addr: netip.MustParseAddrPort("10.0.0.9:5060")}},
		}}
		snap.Attempts = []ProxyAttemptSnapshot{
			{
				Key:                  clnSnap1.Key,
				TargetID:             "A",
				Request:              clnSnap1.Request,
				Addr:                 ResolvedAddr{Transport: "UDP", Addr: netip.MustParseAddrPort("10.0.0.9:5060")},
				State:                TransactionStateCompleted,
				Response:             res503,
				Completed:            true,
				InitialSendSettled:   true,
				InitialSendSucceeded: true,
			},
			{
				Key:                  clnSnap2.Key,
				TargetID:             "A",
				Request:              clnSnap2.Request,
				Addr:                 ResolvedAddr{Transport: "UDP", Addr: netip.MustParseAddrPort("10.0.0.10:5060")},
				State:                TransactionStateProceeding,
				Response:             res180,
				TimerCDeadline:       deadline,
				InitialSendSettled:   true,
				InitialSendSucceeded: true,
			},
		}

		_, prx2, tp2 := newInternalTestProxy(t, route, opts)
		srvTx, err := RestoreServerTransaction(srvSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		settledTx, err := RestoreClientTransaction(clnSnap1, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction(settled) error = %v, want nil", err)
		}
		liveTx, err := RestoreClientTransaction(clnSnap2, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction(live) error = %v, want nil", err)
		}

		rq2, err := prx2.RestoreRequest(ctx, snap, ProxyRestoreBindings{
			ServerTransaction: srvTx,
			ClientTransactions: map[ClientTransactionKey]ClientTransaction{
				clnSnap1.Key: settledTx,
				clnSnap2.Key: liveTx,
			},
		})
		if err != nil {
			t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
		}

		rq2.mu.Lock()
		defer rq2.mu.Unlock()
		st := rq2.targets["A"]
		settledAtt := rq2.attempts[clnSnap1.Key]
		liveAtt := rq2.attempts[clnSnap2.Key]
		if settledAtt == nil || settledAtt.tx != settledTx || !settledAtt.settled {
			t.Fatal("settled attempt record not installed")
		}
		if liveAtt == nil || liveAtt.tx != liveTx || liveAtt.settled {
			t.Fatal("live attempt record not installed")
		}
		if !st.txKey.Equal(clnSnap2.Key) {
			t.Fatalf("live target txKey=%v, want live sibling %v (settled binding must not reclaim it)", st.txKey, clnSnap2.Key)
		}
		if !st.timeC.Equal(deadline) {
			t.Fatalf("live target timerC=%v, want %v (settled binding must not disarm it)", st.timeC, deadline)
		}
		if st.lastRes == nil || st.lastRes.Status() != ResponseStatusRinging {
			t.Fatalf("live target lastRes=%v, want the sibling's 180", st.lastRes)
		}
		if len(rq2.outcomes) != 1 || !rq2.outcomes[0].AttemptKey.Equal(clnSnap1.Key) {
			t.Fatalf("restored outcomes=%+v, want the settled attempt's stored outcome", rq2.outcomes)
		}
	})
}

func TestProxy_RestoreRequest_PromotedMode(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		// A stateless route whose request was promoted to the stateful
		// pipeline: the snapshot records the owning route name with the
		// actual stateful processing mode.
		slRoute := ProxyRoute{
			Name:        "sl-rest",
			ForwardMode: ForwardModeStateless,
			ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		opts := ProxyOptions{
			StatelessKey:  []byte("promoted-restore-key"),
			ServedDomains: []string{"alice.voip.com"},
		}
		elm1, prx1, tp1 := newInternalTestProxy(t, slRoute, opts)

		inReq := newInternalInviteReq(MagicCookie + ".sl-rest")
		prepared, err := newPrxPreparedRouting(inReq, inReq, newInternalLitPlan())
		if err != nil {
			t.Fatalf("newPrxPreparedRouting() error = %v, want nil", err)
		}
		if err := prx1.promoteStatefulReq(ctx, &slRoute, prepared); err != nil {
			t.Fatalf("prx.promoteStatefulReq() error = %v, want nil", err)
		}
		synctest.Wait()

		outReq := testutil.WaitResult(t, tp1.sendReqCalls, "outbound request send")
		clnKey, err := MakeClientTransactionKey(outReq)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx1, ok := elm1.LoadClientTransaction(clnKey)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}
		srvKey, err := MakeServerTransactionKey(inReq)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		rq1, ok := prx1.loadSrvReq(srvKey)
		if !ok {
			t.Fatal("promoted request not indexed by its upstream key")
		}
		snap, err := rq1.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		if snap.RouteName != "sl-rest" || snap.Mode != ForwardModeStateful {
			t.Fatalf("promoted snapshot route=(%q, %v), want (%q, %v)",
				snap.RouteName,
				snap.Mode,
				"sl-rest",
				ForwardModeStateful,
			)
		}
		srvSnap, err := rq1.srvTx.(ServerTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
		}
		clnSnap, err := clnTx1.(ClientTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("clnTx.Snapshot() error = %v, want nil", err)
		}

		// The restore-side proxy still carries the stateless configuration:
		// the promoted snapshot must round-trip against it.
		_, prx2, tp2 := newInternalTestProxy(t, slRoute, opts)
		srvTx, err := RestoreServerTransaction(srvSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		clnTx, err := RestoreClientTransaction(clnSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
		}

		rq2, err := prx2.RestoreRequest(ctx, snap, ProxyRestoreBindings{
			ServerTransaction:  srvTx,
			ClientTransactions: map[ClientTransactionKey]ClientTransaction{clnKey: clnTx},
		})
		if err != nil {
			t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
		}

		rq2.mu.Lock()
		mode := rq2.route.ForwardMode
		rq2.mu.Unlock()
		if mode != ForwardModeStateful {
			t.Fatalf("restored request route mode=%v, want %v", mode, ForwardModeStateful)
		}
		if got := prx2.routes[0].ForwardMode; got != ForwardModeStateless {
			t.Fatalf("stored route mode=%v, want %v (restore never mutates configuration)", got, ForwardModeStateless)
		}
		if got, ok := prx2.loadSrvReq(srvKey); !ok || got != rq2 {
			t.Fatal("restored request not indexed by its upstream key")
		}
	})
}

func TestProxy_SnapshotErrorClass(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"canceled", errclass.Classify(errors.ErrorWrap("x"), errclass.ClassCanceled), "canceled"},
		{"timeout", NewTransactionTimedOutError(), "timeout"},
		{"closed", NewTransactionManagerClosedError(), "closed"},
		{"transport", NewNoTransportError(), "transport"},
		{"message", NewInvalidMessageError(), "message"},
		{"transaction", NewTransactionNotFoundError(), "transaction"},
		{"element closed", NewElementClosedError(), "closed"},
		{"element", errclass.Classify(errors.ErrorWrap("x"), ErrClassElement), "element"},
		{"other", errors.ErrorWrap("misc"), "error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := prxAttemptErrClass(tt.err); got != tt.want {
				t.Fatalf("prxAttemptErrClass(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}
