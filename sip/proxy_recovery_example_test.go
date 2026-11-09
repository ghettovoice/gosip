package sip_test

import (
	"context"
	"fmt"

	"github.com/ghettovoice/gosip/sip"
)

// reqCheckpoint is the application-coordinated checkpoint of one
// proxied request: the orchestration snapshot captured through
// [sip.ProxyRequest.Snapshot] pairs with the upstream and downstream
// transaction snapshots captured separately through the transaction
// snapshotter contracts. The application owns the capture order and any
// persistence of these independent records — together they are not an
// atomic durable checkpoint.
type reqCheckpoint struct {
	req  *sip.ProxyRequestSnapshot
	srv  *sip.ServerTransactionSnapshot
	clns map[sip.ClientTransactionKey]*sip.ClientTransactionSnapshot
}

// rebindDeliveryFlows resolves every delivery reference carried by the
// checkpoint's unfinished targets into the application's existing bound
// transports. A reference that cannot be rebound surfaces as an error:
// a restored request never resolves or dials a replacement flow itself.
func rebindDeliveryFlows(
	ctx context.Context,
	snap *sip.ProxyRequestSnapshot,
	rebind func(context.Context, sip.ProxyDeliveryRef) (sip.ProxyBoundDelivery, error),
) (map[sip.ProxyDeliveryRef]sip.ProxyBoundDelivery, error) {
	unfinished := make(map[string]bool, len(snap.TargetStates))
	for _, st := range snap.TargetStates {
		unfinished[st.TargetID] = !st.Completed
	}
	flows := make(map[sip.ProxyDeliveryRef]sip.ProxyBoundDelivery)
	for _, group := range snap.TargetPlan.Groups {
		for _, trgt := range group {
			if trgt.Delivery == (sip.ProxyDeliveryRef{}) || !unfinished[trgt.ID] {
				continue
			}
			bound, err := rebind(ctx, trgt.Delivery)
			if err != nil {
				return nil, err
			}
			flows[trgt.Delivery] = bound
		}
	}
	return flows, nil
}

// ExampleProxy_RestoreRequest composes the application-owned recovery of
// one proxied request. Capture and restore are separate halves: before
// shutdown the application snapshots the request coordinator and each
// transaction independently; after restart it rebuilds dormant
// transaction bindings and rebound delivery flows, installs the request
// dormant through [sip.Proxy.RestoreRequest], and activates it with the
// once-only [sip.ProxyRequest.Start] only after the restore has
// installed the coordinator subscriptions and the upstream index entry.
func ExampleProxy_RestoreRequest() {
	// capture is the live-side half: the request coordinator and each of
	// its transactions are snapshotted independently — the application
	// owns the order of these separate captures and any persistence of
	// the resulting records.
	capture := func(
		rq *sip.ProxyRequest,
		srvTx sip.ServerTransaction,
		clnTxs map[sip.ClientTransactionKey]sip.ClientTransaction,
	) (*reqCheckpoint, error) {
		reqSnap, err := rq.Snapshot()
		if err != nil {
			return nil, err
		}
		srvSnapper, ok := srvTx.(sip.ServerTransactionSnapshotter)
		if !ok {
			return nil, fmt.Errorf("upstream transaction %v cannot snapshot", srvTx.Key())
		}
		srvSnap, err := srvSnapper.Snapshot()
		if err != nil {
			return nil, err
		}
		clnSnaps := make(map[sip.ClientTransactionKey]*sip.ClientTransactionSnapshot, len(clnTxs))
		for key, tx := range clnTxs {
			clnSnapper, ok := tx.(sip.ClientTransactionSnapshotter)
			if !ok {
				return nil, fmt.Errorf("client transaction %v cannot snapshot", key)
			}
			clnSnap, err := clnSnapper.Snapshot()
			if err != nil {
				return nil, err
			}
			clnSnaps[key] = clnSnap
		}
		return &reqCheckpoint{req: reqSnap, srv: srvSnap, clns: clnSnaps}, nil
	}

	// restore is the restart-side half: it rebinds the delivery flows
	// the unfinished targets still need, recreates the dormant
	// transaction bindings — the upstream server transaction and every
	// live downstream attempt keyed by its recorded client transaction
	// key (attempts already settled at capture need no binding) — then
	// installs the request dormant and activates it.
	restore := func(
		ctx context.Context,
		prx *sip.Proxy,
		ckpt *reqCheckpoint,
		upTp sip.ServerTransport,
		downTp sip.ClientTransport,
		rebind func(context.Context, sip.ProxyDeliveryRef) (sip.ProxyBoundDelivery, error),
	) (*sip.ProxyRequest, error) {
		// Observers bind before activation: a bound handler observes
		// future committed revisions only, so nothing the resumed
		// request publishes is missed. The returned unbind is idempotent
		// and lives with the binding, not the request.
		unbind := prx.BindSnapshotHandler(sip.ProxyRequestSnapshotHandlerFunc(
			func(context.Context, *sip.ProxyRequestSnapshot) {
				// Persist or forward the new revision.
			},
		))
		defer unbind()

		// A missing required flow fails the restore explicitly instead
		// of resolving or dialing a replacement.
		flows, err := rebindDeliveryFlows(ctx, ckpt.req, rebind)
		if err != nil {
			return nil, err
		}

		srvTx, err := sip.RestoreServerTransaction(ckpt.srv, upTp)
		if err != nil {
			return nil, err
		}
		clnTxs := make(map[sip.ClientTransactionKey]sip.ClientTransaction, len(ckpt.clns))
		for key, clnSnap := range ckpt.clns {
			tx, err := sip.RestoreClientTransaction(clnSnap, downTp)
			if err != nil {
				return nil, err
			}
			clnTxs[key] = tx
		}

		// RestoreRequest validates the checkpoint against the bindings
		// and installs the request dormant — no sends, no callbacks, and
		// no snapshot revisions until activation.
		rq, err := prx.RestoreRequest(ctx, ckpt.req, sip.ProxyRestoreBindings{
			ServerTransaction:  srvTx,
			ClientTransactions: clnTxs,
			Deliveries:         flows,
		})
		if err != nil {
			return nil, err
		}

		// Start activates once-only: it starts the supplied dormant
		// transactions — whose subscriptions were installed at
		// restore — then resumes the preserved deadlines, pending work,
		// and target scheduling without replaying completed decisions.
		if err := rq.Start(ctx); err != nil {
			return nil, err
		}
		return rq, nil
	}

	// The application wires both halves into its shutdown and startup
	// paths; an example has no live proxy, so they stay inert here.
	fmt.Println("recovery halves:", len([]any{capture, restore}))

	// Output:
	// recovery halves: 2
}
