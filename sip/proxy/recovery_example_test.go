package proxy_test

import (
	"context"
	"fmt"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/proxy"
)

// reqCheckpoint is the application-coordinated checkpoint of one
// proxied request: the orchestration snapshot captured through
// [proxy.Request.Snapshot] pairs with the upstream and downstream
// transaction snapshots captured separately through the transaction
// snapshotter contracts. The application owns the capture order and any
// persistence of these independent records — together they are not an
// atomic durable checkpoint.
type reqCheckpoint struct {
	req  *proxy.RequestSnapshot
	srv  *sip.ServerTransactionSnapshot
	clns map[sip.ClientTransactionKey]*sip.ClientTransactionSnapshot
}

// rebindDeliveryFlows resolves every delivery reference carried by the
// checkpoint's unfinished targets into the application's existing bound
// transports. A reference that cannot be rebound surfaces as an error:
// a restored request never resolves or dials a replacement flow itself.
func rebindDeliveryFlows(
	ctx context.Context,
	snap *proxy.RequestSnapshot,
	rebind func(context.Context, proxy.DeliveryRef) (proxy.BoundDelivery, error),
) (map[proxy.DeliveryRef]proxy.BoundDelivery, error) {
	unfinished := make(map[string]bool, len(snap.TargetStates))
	for _, st := range snap.TargetStates {
		unfinished[st.TargetID] = !st.Completed
	}
	flows := make(map[proxy.DeliveryRef]proxy.BoundDelivery)
	for _, group := range snap.TargetPlan.Groups {
		for _, trgt := range group {
			if trgt.Delivery == (proxy.DeliveryRef{}) || !unfinished[trgt.ID] {
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
// dormant under the explicit element binding of
// [proxy.RestoreBindings.Element] through [proxy.Proxy.RestoreRequest],
// and activates it with the once-only [proxy.Request.Start] only after
// the restore has installed the coordinator subscriptions and the
// upstream index entry.
func ExampleProxy_RestoreRequest() {
	// capture is the live-side half: the request coordinator and each of
	// its transactions are snapshotted independently — the application
	// owns the order of these separate captures and any persistence of
	// the resulting records.
	capture := func(
		rq *proxy.Request,
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
	// installs the request dormant under the explicit element binding
	// and activates it.
	restore := func(
		ctx context.Context,
		prx *proxy.Proxy,
		elm *sip.Element,
		ckpt *reqCheckpoint,
		upTp sip.ServerTransport,
		downTp sip.ClientTransport,
		rebind func(context.Context, proxy.DeliveryRef) (proxy.BoundDelivery, error),
	) (*proxy.Request, error) {
		// Observers bind before activation: a bound handler observes
		// future committed revisions only, so nothing the resumed
		// request publishes is missed. The returned unbind is idempotent
		// and lives with the binding, not the request.
		revisions := map[*sip.Element][]*proxy.RequestSnapshot{}
		unbind := prx.BindSnapshotHandler(proxy.RequestSnapshotHandlerFunc(
			func(ctx context.Context, snap *proxy.RequestSnapshot) {
				// Persist or forward the new revision under the request's
				// element: the element is context-only observation
				// data — neither events nor snapshots serialize it.
				obsElm, _ := sip.ElementFromContext(ctx)
				revisions[obsElm] = append(revisions[obsElm], snap)
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
		// no snapshot revisions until activation. Element is required and
		// authoritative: it overrides the element carried by ctx while the
		// context's other values survive, a nil element fails with
		// proxy.ErrMissingElement before anything is staged, and ownership
		// is never inferred from the context or a transaction key.
		rq, err := prx.RestoreRequest(ctx, ckpt.req, proxy.RestoreBindings{
			Element:            elm,
			ServerTransaction:  srvTx,
			ClientTransactions: clnTxs,
			Deliveries:         flows,
		})
		if err != nil {
			return nil, err
		}

		// The dormant coordinator is already indexed under the bound
		// element: the lookup is element-qualified through the upstream
		// transaction key — the key alone never resolves a request.
		if _, ok := prx.LookupRequest(elm, srvTx.Key()); !ok {
			return nil, fmt.Errorf("restored request %v not indexed", srvTx.Key())
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
