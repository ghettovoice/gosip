package proxy

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
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// bindCountingSrvTransact wraps a borrowed upstream transaction and records the
// balance of handler binds the proxy staged on it, so a rejected or
// rolled-back restore can be checked for leaked subscriptions.
type bindCountingSrvTransact struct {
	*sip.InviteServerTransaction
	binds   atomic.Int32
	unbinds atomic.Int32
}

func (tx *bindCountingSrvTransact) BindStateHandler(fn sip.TransactionStateHandler) func() {
	tx.binds.Add(1)
	inner := tx.InviteServerTransaction.BindStateHandler(fn)
	return func() { tx.unbinds.Add(1); inner() }
}

func (tx *bindCountingSrvTransact) BindErrorHandler(fn sip.ErrorHandler) func() {
	tx.binds.Add(1)
	inner := tx.InviteServerTransaction.BindErrorHandler(fn)
	return func() { tx.unbinds.Add(1); inner() }
}

// bindBalance reports staged binds lacking a matching unbind.
func (tx *bindCountingSrvTransact) bindBalance() int32 { return tx.binds.Load() - tx.unbinds.Load() }

// bindCountingClnTransact is the downstream counterpart of [bindCountingSrvTransact].
type bindCountingClnTransact struct {
	*sip.InviteClientTransaction
	binds   atomic.Int32
	unbinds atomic.Int32
}

func (tx *bindCountingClnTransact) BindStateHandler(fn sip.TransactionStateHandler) func() {
	tx.binds.Add(1)
	inner := tx.InviteClientTransaction.BindStateHandler(fn)
	return func() { tx.unbinds.Add(1); inner() }
}

func (tx *bindCountingClnTransact) BindErrorHandler(fn sip.ErrorHandler) func() {
	tx.binds.Add(1)
	inner := tx.InviteClientTransaction.BindErrorHandler(fn)
	return func() { tx.unbinds.Add(1); inner() }
}

func (tx *bindCountingClnTransact) BindResponseHandler(fn sip.InboundResponseHandler) func() {
	tx.binds.Add(1)
	inner := tx.InviteClientTransaction.BindResponseHandler(fn)
	return func() { tx.unbinds.Add(1); inner() }
}

// bindBalance reports staged binds lacking a matching unbind.
func (tx *bindCountingClnTransact) bindBalance() int32 { return tx.binds.Load() - tx.unbinds.Load() }

func TestProxyRequestSnapshot_UnstableTransition(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		route := Route{
			Name:        "snap-unstable",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		elm, prx, _ := newInternalTestProxy(t, route, Options{
			ServedDomains: []string{"alice.voip.com"},
		})

		// A coordinator before the committed plan has no stable ownership
		// boundary to capture.
		rq := newReq(ctx, elm, prx, &route, newInternalInviteReq(sip.MagicCookie+".snap-unstable"), "snap-unstable")
		if _, err := rq.Snapshot(); !errors.Is(err, errSnapshotUnstable) {
			t.Fatalf("rq.Snapshot() error = %v, want %v", err, errSnapshotUnstable)
		}

		// Once admitted, capture works.
		rq2 := admitInternalReq(t, elm, prx, newInternalInviteReq(sip.MagicCookie+".snap-admitted"))
		synctest.Wait()
		if _, err := rq2.Snapshot(); err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}

		// A pending client-transaction admission is a partial branch
		// decision: capture and publication are refused while it is
		// unresolved.
		rq2.mu.Lock()
		rq2.pendingAdms[42] = &admission{targetID: "A", gen: 42}
		rq2.mu.Unlock()

		if _, err := rq2.Snapshot(); !errors.Is(err, errSnapshotUnstable) {
			t.Fatalf("rq.Snapshot() error = %v, want %v", err, errSnapshotUnstable)
		}

		var delivered atomic.Int32
		unbind := prx.BindSnapshotHandler(RequestSnapshotHandlerFunc(
			func(context.Context, *RequestSnapshot) { delivered.Add(1) },
		))
		rq2.enqueue(ctx, workEvent{kind: evtSrvErr, err: errors.ErrorWrap("test")})
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
		route := Route{
			Name:        "snap-pub",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		elm, prx, tp := newInternalTestProxy(t, route, Options{
			ServedDomains: []string{"alice.voip.com"},
		})

		var revs []uint64
		unbind := prx.BindSnapshotHandler(RequestSnapshotHandlerFunc(
			func(_ context.Context, snap *RequestSnapshot) {
				revs = append(revs, snap.Revision)
			},
		))
		defer unbind()

		rq := admitInternalReq(t, elm, prx, newInternalInviteReq(sip.MagicCookie+".snap-pub"))
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
		key, err := sip.MakeClientTransactionKey(outReq)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx, ok := elm.LoadClientTransaction(key)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}

		// A committed downstream response publishes a higher revision.
		if err := clnTx.RecvResponse(ctx, newInternalInRes(t, outReq, sip.ResponseStatusRinging)); err != nil {
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

	snap := &RequestSnapshot{
		Version:   reqSnapshotVer,
		Revision:  7,
		RouteName: "r",
		TargetPlan: TargetPlan{Groups: [][]Target{
			{{
				ID:         "A",
				URI:        &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr("10.0.0.9", 5060)},
				Attributes: map[string]string{"k": "v"},
			}},
		}},
		TargetStates: []TargetSnapshot{
			{TargetID: "A", Started: true, AttemptedAddresses: []sip.ResolvedAddr{{Transport: "UDP"}}},
		},
		WorkingRequest: newInternalInviteReq(sip.MagicCookie + ".snap-clone-work"),
	}
	clone := snap.Clone()
	clone.TargetPlan.Groups[0][0].Attributes["k"] = "evil"
	clone.TargetStates[0].AttemptedAddresses = nil
	clone.WorkingRequest.WithMessage(func(r *sip.Request) {
		r.Headers.AppendRoute(header.RouteHop{URI: &sip.URI{Addr: sip.MakeHostAddr("evil.example")}})
	})
	if snap.TargetPlan.Groups[0][0].Attributes["k"] != "v" {
		t.Fatal("mutating the clone changed the original snapshot")
	}
	if len(snap.TargetStates[0].AttemptedAddresses) != 1 {
		t.Fatal("mutating the clone changed the original snapshot")
	}
	snap.WorkingRequest.WithMessage(func(r *sip.Request) {
		if _, ok := r.Headers.FirstRoute(); ok {
			t.Fatal("mutating the clone changed the original snapshot")
		}
	})
}

func TestProxy_RestoreRequest_DormantInstall(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		route := Route{
			Name:        "rest",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		opts := Options{ServedDomains: []string{"alice.voip.com"}}
		elm1, prx1, tp1 := newInternalTestProxy(t, route, opts)

		inReq := newInternalInviteReq(sip.MagicCookie + ".rest-install")
		rq1 := admitInternalReq(t, elm1, prx1, inReq)
		synctest.Wait()

		outReq := testutil.WaitResult(t, tp1.sendReqCalls, "outbound request send")
		clnKey, err := sip.MakeClientTransactionKey(outReq)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx1, ok := elm1.LoadClientTransaction(clnKey)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}
		srvKey, err := sip.MakeServerTransactionKey(inReq)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		snap, err := rq1.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		srvSnapper, ok := rq1.srvTx.(sip.ServerTransactionSnapshotter)
		if !ok {
			t.Fatal("upstream transaction does not implement sip.ServerTransactionSnapshotter")
		}
		srvSnap, err := srvSnapper.Snapshot()
		if err != nil {
			t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
		}
		clnSnapper, ok := clnTx1.(sip.ClientTransactionSnapshotter)
		if !ok {
			t.Fatal("client transaction does not implement sip.ClientTransactionSnapshotter")
		}
		clnSnap, err := clnSnapper.Snapshot()
		if err != nil {
			t.Fatalf("clnTx.Snapshot() error = %v, want nil", err)
		}

		elm2, prx2, tp2 := newInternalTestProxy(t, route, opts)
		srvTx, err := sip.RestoreServerTransaction(srvSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		clnTx, err := sip.RestoreClientTransaction(clnSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
		}

		rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, RestoreBindings{
			Element:            elm2,
			ServerTransaction:  srvTx,
			ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{clnKey: clnTx},
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
		if got, ok := prx2.loadSrvReq(elm2, srvKey); !ok || got != rq2 {
			t.Fatal("restored request not indexed by its upstream key")
		}

		// The dormant callback queue cannot drain: a queued event waits
		// for activation without scheduling a drain owner.
		rq2.enqueue(ctx, workEvent{kind: evtSrvErr, err: errors.ErrorWrap("test")})
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
		mutate  func(*RequestSnapshot)
		wantErr error
	}{
		{
			"missing via",
			func(snap *RequestSnapshot) {
				snap.WorkingRequest.WithMessage(func(r *sip.Request) { r.Headers.Delete("Via") })
			},
			ErrInvalidSnapshot,
		},
		{
			"opaque original metadata",
			func(snap *RequestSnapshot) {
				snap.OriginalRequest.Metadata().Set("restore.opaque", make(chan int))
			},
			ErrSnapshotUnsupported,
		},
		{
			"opaque working metadata",
			func(snap *RequestSnapshot) {
				snap.WorkingRequest.Metadata().Set("restore.opaque", make(chan int))
			},
			ErrSnapshotUnsupported,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				route := Route{
					Name:        "restore-values",
					ForwardMode: ForwardModeStateful,
					ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
						return newInternalLitPlan(), nil
					},
				}
				opts := Options{ServedDomains: []string{"alice.voip.com"}}
				elm1, prx1, tp1 := newInternalTestProxy(t, route, opts)
				rq1 := admitInternalReq(t, elm1, prx1, newInternalInviteReq(sip.MagicCookie+".restore-values"))
				synctest.Wait()
				outReq := testutil.WaitResult(t, tp1.sendReqCalls, "outbound request send")
				clnKey, err := sip.MakeClientTransactionKey(outReq)
				if err != nil {
					t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
				}
				clnTx1, ok := elm1.LoadClientTransaction(clnKey)
				if !ok {
					t.Fatal("forwarding client transaction not registered")
				}
				snap, err := rq1.Snapshot()
				if err != nil {
					t.Fatalf("rq1.Snapshot() error = %v, want nil", err)
				}
				srvSnap, err := rq1.srvTx.(sip.ServerTransactionSnapshotter).Snapshot()
				if err != nil {
					t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
				}
				clnSnap, err := clnTx1.(sip.ClientTransactionSnapshotter).Snapshot()
				if err != nil {
					t.Fatalf("clnTx.Snapshot() error = %v, want nil", err)
				}

				elm, prx, tp := newInternalTestProxy(t, route, opts)
				controlReq := newInternalInviteReq(sip.MagicCookie + ".restore-values-control")
				control := admitInternalReq(t, elm, prx, controlReq)
				synctest.Wait()
				controlKey, err := sip.MakeServerTransactionKey(controlReq)
				if err != nil {
					t.Fatalf("sip.MakeServerTransactionKey(control) error = %v, want nil", err)
				}
				if got, ok := prx.loadSrvReq(elm, controlKey); !ok || got != control {
					t.Fatal("control request not indexed before rejection")
				}
				if got := len(drainInternalReqs(tp)); got != 1 {
					t.Fatalf("control request sends = %d, want 1", got)
				}
				drainInternalRess(tp)

				srvTx, err := sip.RestoreInviteServerTransaction(srvSnap, tp)
				if err != nil {
					t.Fatalf("sip.RestoreInviteServerTransaction() error = %v, want nil", err)
				}
				clnTx, err := sip.RestoreInviteClientTransaction(clnSnap, tp)
				if err != nil {
					t.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
				}
				srv := &bindCountingSrvTransact{InviteServerTransaction: srvTx}
				cln := &bindCountingClnTransact{InviteClientTransaction: clnTx}
				bindings := RestoreBindings{
					Element:            elm,
					ServerTransaction:  srv,
					ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{clnKey: cln},
				}
				var observations atomic.Int32
				unbindEvents := prx.BindEventHandler(EventHandlerFunc(
					func(context.Context, Event) { observations.Add(1) },
				))
				defer unbindEvents()
				unbindSnapshots := prx.BindSnapshotHandler(RequestSnapshotHandlerFunc(
					func(context.Context, *RequestSnapshot) { observations.Add(1) },
				))
				defer unbindSnapshots()

				valid := snap.Clone()
				tt.mutate(snap)
				rq, err := prx.RestoreRequest(sip.ContextWithElement(ctx, elm), snap, bindings)
				if rq != nil || !errors.Is(err, tt.wantErr) {
					t.Fatalf("prx.RestoreRequest() = (%v, %v), want (nil, %v)", rq, err, tt.wantErr)
				}
				synctest.Wait()
				if got := srv.bindBalance(); got != 0 {
					t.Fatalf("upstream staged binds after rejection = %d, want 0", got)
				}
				if got := cln.bindBalance(); got != 0 {
					t.Fatalf("downstream staged binds after rejection = %d, want 0", got)
				}
				if srv.Started() || cln.Started() {
					t.Fatal("rejected restore started a borrowed transaction")
				}
				if srv.State() == sip.TransactionStateTerminated ||
					cln.State() == sip.TransactionStateTerminated {
					t.Fatal("rejected restore terminated a borrowed transaction")
				}
				if got, ok := prx.loadSrvReq(elm, snap.UpstreamKey); ok || got != nil {
					t.Fatal("rejected restore installed a candidate upstream index entry")
				}
				if got, ok := prx.loadSrvReq(elm, controlKey); !ok || got != control {
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
				rq, err = prx.RestoreRequest(sip.ContextWithElement(ctx, elm), snap, bindings)
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
				if got, ok := prx.loadSrvReq(elm, snap.UpstreamKey); !ok || got != rq {
					t.Fatal("repaired restore did not index its coordinator")
				}
				if srv.Started() || cln.Started() ||
					srv.State() == sip.TransactionStateTerminated ||
					cln.State() == sip.TransactionStateTerminated {
					t.Fatal("repaired dormant restore started or terminated a borrowed transaction")
				}
				if got, ok := prx.loadSrvReq(elm, controlKey); !ok || got != control {
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
		route := Route{
			Name:        "rest-rb",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		opts := Options{ServedDomains: []string{"alice.voip.com"}}
		elm1, prx1, tp1 := newInternalTestProxy(t, route, opts)

		inReq := newInternalInviteReq(sip.MagicCookie + ".rest-rollback")
		rq1 := admitInternalReq(t, elm1, prx1, inReq)
		synctest.Wait()

		outReq := testutil.WaitResult(t, tp1.sendReqCalls, "outbound request send")
		clnKey, err := sip.MakeClientTransactionKey(outReq)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx1, ok := elm1.LoadClientTransaction(clnKey)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}
		srvKey, err := sip.MakeServerTransactionKey(inReq)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		snap, err := rq1.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		srvSnap, err := rq1.srvTx.(sip.ServerTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
		}
		clnSnap, err := clnTx1.(sip.ClientTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("clnTx.Snapshot() error = %v, want nil", err)
		}

		elm2, prx2, tp2 := newInternalTestProxy(t, route, opts)
		mkBindings := func() RestoreBindings {
			srvTx, err := sip.RestoreInviteServerTransaction(srvSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreInviteServerTransaction() error = %v, want nil", err)
			}
			clnTx, err := sip.RestoreInviteClientTransaction(clnSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
			}
			return RestoreBindings{
				Element:           elm2,
				ServerTransaction: &bindCountingSrvTransact{InviteServerTransaction: srvTx},
				ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{
					clnKey: &bindCountingClnTransact{InviteClientTransaction: clnTx},
				},
			}
		}

		rqFirst, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, mkBindings())
		if err != nil {
			t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
		}

		second := mkBindings()
		srv2 := second.ServerTransaction.(*bindCountingSrvTransact)
		cln2 := second.ClientTransactions[clnKey].(*bindCountingClnTransact)
		if _, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, second); err == nil {
			t.Fatal("duplicate upstream restore accepted")
		}

		if got, ok := prx2.loadSrvReq(elm2, srvKey); !ok || got != rqFirst {
			t.Fatal("failed install disturbed the existing index entry")
		}
		if got := srv2.bindBalance(); got != 0 {
			t.Fatalf("upstream staged binds=%d, want 0 (staged unbinds rolled back)", got)
		}
		if got := cln2.bindBalance(); got != 0 {
			t.Fatalf("downstream staged binds=%d, want 0 (staged unbinds rolled back)", got)
		}
		if srv2.Started() || cln2.Started() {
			t.Fatal("borrowed transaction started by a failed install")
		}
		if srv2.State() == sip.TransactionStateTerminated ||
			cln2.State() == sip.TransactionStateTerminated {
			t.Fatal("borrowed transaction terminated by a failed install")
		}
	})
}

func TestProxy_RestoreRequest_CompletedAttemptBindingIsolation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		route := Route{
			Name:        "rest-iso",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		opts := Options{ServedDomains: []string{"alice.voip.com"}}
		elm1, prx1, tp1 := newInternalTestProxy(t, route, opts)

		inReq := newInternalInviteReq(sip.MagicCookie + ".rest-iso")
		rq1 := admitInternalReq(t, elm1, prx1, inReq)
		synctest.Wait()
		testutil.WaitResult(t, tp1.sendReqCalls, "outbound request send")

		snap, err := rq1.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		srvSnap, err := rq1.srvTx.(sip.ServerTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
		}

		// Two real client transactions stand in for the failover history of
		// target A: the settled attempt committed a 503, the live sibling
		// is still Proceeding on its provisional response.
		out1 := newInternalInviteReq(sip.MagicCookie + ".rest-iso-a1")
		cln1, err := elm1.NewClientTransaction(ctx, out1, tp1)
		if err != nil {
			t.Fatalf("elm.NewClientTransaction() error = %v, want nil", err)
		}
		if err := cln1.Start(ctx); err != nil {
			t.Fatalf("cln1.Start() error = %v, want nil", err)
		}
		res503 := newInternalInRes(t, cln1.Request(), sip.ResponseStatusServiceUnavailable)
		if err := cln1.RecvResponse(ctx, res503); err != nil {
			t.Fatalf("cln1.RecvResponse(503) error = %v, want nil", err)
		}
		out2 := newInternalInviteReq(sip.MagicCookie + ".rest-iso-a2")
		cln2, err := elm1.NewClientTransaction(ctx, out2, tp1)
		if err != nil {
			t.Fatalf("elm.NewClientTransaction() error = %v, want nil", err)
		}
		if err := cln2.Start(ctx); err != nil {
			t.Fatalf("cln2.Start() error = %v, want nil", err)
		}
		res180 := newInternalInRes(t, cln2.Request(), sip.ResponseStatusRinging)
		if err := cln2.RecvResponse(ctx, res180); err != nil {
			t.Fatalf("cln2.RecvResponse(180) error = %v, want nil", err)
		}
		synctest.Wait()

		clnSnap1, err := cln1.(sip.ClientTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("cln1.Snapshot() error = %v, want nil", err)
		}
		clnSnap2, err := cln2.(sip.ClientTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("cln2.Snapshot() error = %v, want nil", err)
		}

		deadline := snap.Time.Add(time.Hour)
		snap.TargetStates = []TargetSnapshot{{
			TargetID:           "A",
			Started:            true,
			AttemptedAddresses: []sip.ResolvedAddr{{Transport: "UDP", Addr: netip.MustParseAddrPort("10.0.0.9:5060")}},
		}}
		snap.Attempts = []AttemptSnapshot{
			{
				Key:                  clnSnap1.Key,
				TargetID:             "A",
				Request:              clnSnap1.Request,
				Addr:                 sip.ResolvedAddr{Transport: "UDP", Addr: netip.MustParseAddrPort("10.0.0.9:5060")},
				State:                sip.TransactionStateCompleted,
				Response:             res503,
				Completed:            true,
				InitialSendSettled:   true,
				InitialSendSucceeded: true,
			},
			{
				Key:                  clnSnap2.Key,
				TargetID:             "A",
				Request:              clnSnap2.Request,
				Addr:                 sip.ResolvedAddr{Transport: "UDP", Addr: netip.MustParseAddrPort("10.0.0.10:5060")},
				State:                sip.TransactionStateProceeding,
				Response:             res180,
				TimerCDeadline:       deadline,
				InitialSendSettled:   true,
				InitialSendSucceeded: true,
			},
		}

		elm2, prx2, tp2 := newInternalTestProxy(t, route, opts)
		srvTx, err := sip.RestoreServerTransaction(srvSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		settledTx, err := sip.RestoreClientTransaction(clnSnap1, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction(settled) error = %v, want nil", err)
		}
		liveTx, err := sip.RestoreClientTransaction(clnSnap2, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction(live) error = %v, want nil", err)
		}

		rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, RestoreBindings{
			Element:           elm2,
			ServerTransaction: srvTx,
			ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{
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
		if st.lastRes == nil || st.lastRes.Status() != sip.ResponseStatusRinging {
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
		slRoute := Route{
			Name:        "sl-rest",
			ForwardMode: ForwardModeStateless,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		opts := Options{
			StatelessKey:  []byte("promoted-restore-key"),
			ServedDomains: []string{"alice.voip.com"},
		}
		elm1, prx1, tp1 := newInternalTestProxy(t, slRoute, opts)

		inReq := newInternalInviteReq(sip.MagicCookie + ".sl-rest")
		prepared, err := newPreparedRouting(inReq, inReq, newInternalLitPlan())
		if err != nil {
			t.Fatalf("newPreparedRouting() error = %v, want nil", err)
		}
		if err := prx1.promoteStatefulReq(ctx, elm1, &slRoute, prepared); err != nil {
			t.Fatalf("prx.promoteStatefulReq() error = %v, want nil", err)
		}
		synctest.Wait()

		outReq := testutil.WaitResult(t, tp1.sendReqCalls, "outbound request send")
		clnKey, err := sip.MakeClientTransactionKey(outReq)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx1, ok := elm1.LoadClientTransaction(clnKey)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}
		srvKey, err := sip.MakeServerTransactionKey(inReq)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		rq1, ok := prx1.loadSrvReq(elm1, srvKey)
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
		srvSnap, err := rq1.srvTx.(sip.ServerTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
		}
		clnSnap, err := clnTx1.(sip.ClientTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("clnTx.Snapshot() error = %v, want nil", err)
		}

		// The restore-side proxy still carries the stateless configuration:
		// the promoted snapshot must round-trip against it.
		elm2, prx2, tp2 := newInternalTestProxy(t, slRoute, opts)
		srvTx, err := sip.RestoreServerTransaction(srvSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		clnTx, err := sip.RestoreClientTransaction(clnSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
		}

		rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, RestoreBindings{
			Element:            elm2,
			ServerTransaction:  srvTx,
			ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{clnKey: clnTx},
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
		if got, ok := prx2.loadSrvReq(elm2, srvKey); !ok || got != rq2 {
			t.Fatal("restored request not indexed by its upstream key")
		}
	})
}

// restoreElmFixture is the captured in-flight INVITE checkpoint of the
// element-binding restore cases: the request snapshot plus the dormant
// transaction snapshots its runtime bindings are restored from.
type restoreElmFixture struct {
	snap    *RequestSnapshot
	srvSnap *sip.ServerTransactionSnapshot
	clnSnap *sip.ClientTransactionSnapshot
	srvKey  sip.ServerTransactionKey
	clnKey  sip.ClientTransactionKey
}

// newRestoreElmFixture admits an in-flight INVITE on a throwaway source
// proxy and captures the request and transaction checkpoints.
func newRestoreElmFixture(tb testing.TB, route Route, opts Options) *restoreElmFixture {
	tb.Helper()

	elm, prx, tp := newInternalTestProxy(tb, route, opts)
	inReq := newInternalInviteReq(sip.MagicCookie + ".rest-elm")
	rq := admitInternalReq(tb, elm, prx, inReq)
	synctest.Wait()

	outReq := testutil.WaitResult(tb, tp.sendReqCalls, "outbound request send")
	clnKey, err := sip.MakeClientTransactionKey(outReq)
	if err != nil {
		tb.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
	}
	clnTx, ok := elm.LoadClientTransaction(clnKey)
	if !ok {
		tb.Fatal("forwarding client transaction not registered")
	}
	srvKey, err := sip.MakeServerTransactionKey(inReq)
	if err != nil {
		tb.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
	}
	snap, err := rq.Snapshot()
	if err != nil {
		tb.Fatalf("rq.Snapshot() error = %v, want nil", err)
	}
	srvSnap, err := rq.srvTx.(sip.ServerTransactionSnapshotter).Snapshot()
	if err != nil {
		tb.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
	}
	clnSnap, err := clnTx.(sip.ClientTransactionSnapshotter).Snapshot()
	if err != nil {
		tb.Fatalf("clnTx.Snapshot() error = %v, want nil", err)
	}
	return &restoreElmFixture{
		snap:    snap,
		srvSnap: srvSnap,
		clnSnap: clnSnap,
		srvKey:  srvKey,
		clnKey:  clnKey,
	}
}

// dormantBindings builds fresh dormant transaction bindings of the
// fixture against tp for elm, wrapped in subscription-counting spies.
func (f *restoreElmFixture) dormantBindings(
	tb testing.TB,
	elm *sip.Element,
	tp *internalTransp,
) (*bindCountingSrvTransact, *bindCountingClnTransact, RestoreBindings) {
	tb.Helper()

	srvTx, err := sip.RestoreInviteServerTransaction(f.srvSnap, tp)
	if err != nil {
		tb.Fatalf("sip.RestoreInviteServerTransaction() error = %v, want nil", err)
	}
	clnTx, err := sip.RestoreInviteClientTransaction(f.clnSnap, tp)
	if err != nil {
		tb.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
	}
	srv := &bindCountingSrvTransact{InviteServerTransaction: srvTx}
	cln := &bindCountingClnTransact{InviteClientTransaction: clnTx}
	return srv, cln, RestoreBindings{
		Element:            elm,
		ServerTransaction:  srv,
		ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{f.clnKey: cln},
	}
}

// trackRestoreElm attaches a second element with its own stub transport to
// prx so one proxy serves two processing namespaces.
func trackRestoreElm(tb testing.TB, prx *Proxy) (*sip.Element, *internalTransp) {
	tb.Helper()

	elm := &sip.Element{}
	tb.Cleanup(func() { elm.Close() })
	tp := newInternalTransp()
	if err := elm.TrackTransport(tp); err != nil {
		tb.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}
	elm.UseMiddleware(prx)
	return elm, tp
}

// ctxMarkKey is the unrelated caller-context value a restore must
// preserve while it overrides the caller's element.
type ctxMarkKey struct{}

// TestProxy_RestoreElementBinding pins the explicit element contract of
// [Proxy.RestoreRequest]: [RestoreBindings.Element] is required and
// authoritative — a restore never resolves its element from the caller's
// context and never infers ownership from a transaction key.
func TestProxy_RestoreElementBinding(t *testing.T) {
	t.Parallel()

	route := Route{
		Name:        "rest-elm",
		ForwardMode: ForwardModeStateful,
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
			return newInternalLitPlan(), nil
		},
	}
	opts := Options{ServedDomains: []string{"alice.voip.com"}}

	t.Run("nil element rejects before staging even with a ctx element", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreElmFixture(t, route, opts)
			elmA, prx, tpA := newInternalTestProxy(t, route, opts)
			elmB, _ := trackRestoreElm(t, prx)

			srv, cln, bindings := f.dormantBindings(t, nil, tpA)
			var observations atomic.Int32
			unbindEvents := prx.BindEventHandler(EventHandlerFunc(
				func(context.Context, Event) { observations.Add(1) },
			))
			defer unbindEvents()
			unbindSnaps := prx.BindSnapshotHandler(RequestSnapshotHandlerFunc(
				func(context.Context, *RequestSnapshot) { observations.Add(1) },
			))
			defer unbindSnaps()

			// The caller context carries a valid element the restore must
			// not adopt: only the explicit binding can name the namespace.
			rq, err := prx.RestoreRequest(sip.ContextWithElement(ctx, elmB), f.snap, bindings)
			if rq != nil || err == nil {
				t.Fatalf("prx.RestoreRequest() = (%v, %v), want (nil, %v)", rq, err, ErrMissingElement)
			}
			if !errors.Is(err, ErrMissingElement) {
				t.Fatalf("prx.RestoreRequest() error = %v, want %v", err, ErrMissingElement)
			}
			if !IsError(err) {
				t.Fatalf("prx.RestoreRequest() error = %v, want proxy classification", err)
			}
			synctest.Wait()

			// The rejection precedes every staged effect: no subscription
			// staged, no borrowed transaction claimed or moved, no element
			// scope or index entry allocated, and no send, timer, or
			// observation produced.
			if got := srv.bindBalance(); got != 0 {
				t.Fatalf("upstream staged binds=%d, want 0", got)
			}
			if got := cln.bindBalance(); got != 0 {
				t.Fatalf("downstream staged binds=%d, want 0", got)
			}
			if srv.Started() || cln.Started() {
				t.Fatal("rejected restore started a borrowed transaction")
			}
			if srv.State() == sip.TransactionStateTerminated ||
				cln.State() == sip.TransactionStateTerminated {
				t.Fatal("rejected restore terminated a borrowed transaction")
			}
			if es := prx.getElemState(elmA, false); es != nil {
				t.Fatal("rejected restore allocated the candidate element scope")
			}
			if es := prx.getElemState(elmB, false); es != nil {
				t.Fatal("rejected restore allocated the caller-context element scope")
			}
			if got := len(drainInternalReqs(tpA)) + len(drainInternalRess(tpA)); got != 0 {
				t.Fatalf("rejected restore sends=%d, want 0", got)
			}
			if got := observations.Load(); got != 0 {
				t.Fatalf("rejected restore observations=%d, want 0", got)
			}
		})
	})

	t.Run("explicit binding overrides the caller context element", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreElmFixture(t, route, opts)
			elmA, prx, tpA := newInternalTestProxy(t, route, opts)
			elmB, _ := trackRestoreElm(t, prx)

			_, _, bindings := f.dormantBindings(t, elmA, tpA)
			ctxB := context.WithValue(sip.ContextWithElement(ctx, elmB), ctxMarkKey{}, "caller")
			rq, err := prx.RestoreRequest(ctxB, f.snap, bindings)
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}

			if got, ok := prx.LookupRequest(elmA, f.srvKey); !ok || got != rq {
				t.Fatal("restored request not indexed under the bound element")
			}
			if got, ok := prx.LookupRequest(elmB, f.srvKey); ok || got != nil {
				t.Fatal("restored request resolved under the caller-context element")
			}
			if got, ok := sip.ElementFromContext(rq.ctx); !ok || got != elmA {
				t.Fatalf("restored request ctx element=(%v, %t), want the bound element", got, ok)
			}
			if got := rq.ctx.Value(ctxMarkKey{}); got != "caller" {
				t.Fatalf("restored request ctx marker=%v, want the preserved caller value", got)
			}
		})
	})

	t.Run("equal keys under two elements resolve independently", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreElmFixture(t, route, opts)
			elmA, prx, tpA := newInternalTestProxy(t, route, opts)
			elmB, tpB := trackRestoreElm(t, prx)

			_, _, bindingsA := f.dormantBindings(t, elmA, tpA)
			_, _, bindingsB := f.dormantBindings(t, elmB, tpB)
			rqA, err := prx.RestoreRequest(ctx, f.snap, bindingsA)
			if err != nil {
				t.Fatalf("prx.RestoreRequest(elmA) error = %v, want nil", err)
			}
			rqB, err := prx.RestoreRequest(ctx, f.snap, bindingsB)
			if err != nil {
				t.Fatalf("prx.RestoreRequest(elmB) error = %v, want nil", err)
			}
			if rqA == rqB {
				t.Fatal("equal upstream keys restored to one coordinator")
			}
			if got, ok := prx.LookupRequest(elmA, f.srvKey); !ok || got != rqA {
				t.Fatal("restore under element A not indexed by its upstream key")
			}
			if got, ok := prx.LookupRequest(elmB, f.srvKey); !ok || got != rqB {
				t.Fatal("restore under element B not indexed by its upstream key")
			}
		})
	})

	t.Run("equal key under the same element conflicts", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreElmFixture(t, route, opts)
			elmA, prx, tpA := newInternalTestProxy(t, route, opts)

			_, _, first := f.dormantBindings(t, elmA, tpA)
			rq1, err := prx.RestoreRequest(ctx, f.snap, first)
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}

			srv2, cln2, second := f.dormantBindings(t, elmA, tpA)
			rq2, err := prx.RestoreRequest(ctx, f.snap, second)
			if rq2 != nil || !errors.Is(err, ErrInvalidSnapshot) {
				t.Fatalf("duplicate restore = (%v, %v), want (nil, %v)", rq2, err, ErrInvalidSnapshot)
			}
			if got, ok := prx.LookupRequest(elmA, f.srvKey); !ok || got != rq1 {
				t.Fatal("conflicting restore disturbed the existing index entry")
			}
			if got := srv2.bindBalance(); got != 0 {
				t.Fatalf("upstream staged binds=%d, want 0", got)
			}
			if got := cln2.bindBalance(); got != 0 {
				t.Fatalf("downstream staged binds=%d, want 0", got)
			}
			if srv2.Started() || cln2.Started() {
				t.Fatal("conflicting restore started a borrowed transaction")
			}
			if got := len(drainInternalReqs(tpA)) + len(drainInternalRess(tpA)); got != 0 {
				t.Fatalf("conflicting restore sends=%d, want 0", got)
			}
		})
	})

	t.Run("a key owned on another element implies no ownership", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreElmFixture(t, route, opts)
			elmA, prx, tpA := newInternalTestProxy(t, route, opts)
			elmB, tpB := trackRestoreElm(t, prx)

			// A live request on element B already owns the upstream key the
			// snapshot carries: the restore bound to element A must neither
			// attach there nor disturb the live association.
			live := admitInternalReq(t, elmB, prx, newInternalInviteReq(sip.MagicCookie+".rest-elm"))
			synctest.Wait()
			drainInternalReqs(tpB)
			if got, ok := prx.LookupRequest(elmB, f.srvKey); !ok || got != live {
				t.Fatal("live request on element B not indexed by its upstream key")
			}

			_, _, bindings := f.dormantBindings(t, elmA, tpA)
			rq, err := prx.RestoreRequest(ctx, f.snap, bindings)
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}
			if rq == live {
				t.Fatal("restore attached to the live request of another element")
			}
			if got, ok := prx.LookupRequest(elmA, f.srvKey); !ok || got != rq {
				t.Fatal("restored request not indexed under the bound element")
			}
			if got, ok := prx.LookupRequest(elmB, f.srvKey); !ok || got != live {
				t.Fatal("restore disturbed the live association of the other element")
			}
		})
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
		{"timeout", sip.NewTransactionTimedOutError(), "timeout"},
		{"closed", sip.NewTransactionManagerClosedError(), "closed"},
		{"transport", sip.NewNoTransportError(), "transport"},
		{"message", sip.NewInvalidMessageError(), "message"},
		{"transaction", sip.NewTransactionNotFoundError(), "transaction"},
		{"element closed", sip.NewElementClosedError(), "closed"},
		{"element", errclass.Classify(errors.ErrorWrap("x"), sip.ErrClassElement), "element"},
		{"other", errors.ErrorWrap("misc"), "error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := attemptErrClass(tt.err); got != tt.want {
				t.Fatalf("attemptErrClass(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}
