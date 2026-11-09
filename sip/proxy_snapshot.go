package sip

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/pkg/errclass"
)

// prxReqSnapshotVer is the format version emitted by
// [ProxyRequest.Snapshot]. Version 1 is the initial orchestration layout.
const prxReqSnapshotVer uint32 = 1

// ProxyRequestSnapshot is a detached, serializable view of one proxied
// request's orchestration state.
//
// The snapshot stores orchestration references and classified outcomes only:
// it never embeds transaction snapshots — the upstream and downstream
// transaction snapshots are captured separately through the transaction
// snapshot contracts — and it never carries live transaction handles,
// callbacks, iterators, transports, or arbitrary error objects.
//
// Every captured message envelope, URI, map, and slice is a detached clone:
// mutating a returned snapshot or a clone delivered to a snapshot handler
// affects neither the live request nor any other capture. Opaque
// application metadata values keep the existing message-envelope clone
// semantics (metadata containers are copied, values are shared), and a
// capture fails with [ErrProxySnapshotUnsupported] when a stored metadata
// value is not JSON-serializable.
type ProxyRequestSnapshot struct {
	// Version is the snapshot format version; version 1 is the initial
	// orchestration layout.
	Version uint32 `json:"version"`
	// Revision is the last committed snapshot revision of the request.
	// Revisions increase monotonically on every published transition.
	Revision uint64 `json:"revision"`
	// Time is the commit time of the recorded revision.
	Time time.Time `json:"time"`
	// RouteName is the name of the route that claimed the request.
	RouteName string `json:"route_name"`
	// Mode is the forwarding mode the request was admitted under.
	Mode ForwardMode `json:"mode"`
	// UpstreamKey is the server transaction key of the owned upstream
	// transaction; its snapshot is captured separately.
	UpstreamKey ServerTransactionKey `json:"upstream_key"`
	// OriginalRequest is the inbound request as admitted.
	OriginalRequest *RequestEnvelope `json:"original_request"`
	// WorkingRequest is the validated post-PreprocessRoute plus
	// local-route-consumption envelope that later groups and address
	// failover continue from; it is captured separately from the
	// transaction-matching original.
	WorkingRequest *RequestEnvelope `json:"working_request"`
	// TargetPlan is the committed immutable target plan.
	TargetPlan ProxyTargetPlan `json:"target_plan"`
	// TargetStates are the per-target progress records sorted by target ID.
	TargetStates []ProxyTargetSnapshot `json:"target_states"`
	// Attempts are the per-attempt records sorted by transaction key.
	Attempts []ProxyAttemptSnapshot `json:"attempts"`
	// Candidates is the response context — the attributed downstream final
	// responses in arrival order.
	Candidates []*ResponseEnvelope `json:"candidates"`
	// FinalForwarded reports whether a final response was actually written
	// to the upstream transport.
	FinalForwarded bool `json:"final_forwarded"`
	// Completed reports whether the request reached its terminal decision.
	Completed bool `json:"completed"`
	// Outcome is the committed request outcome; it is meaningful only when
	// Completed is true.
	Outcome ProxyRequestOutcome `json:"outcome"`
	// FinalStatus is the status of the committed final response, zero when
	// no final decision exists.
	FinalStatus ResponseStatus `json:"final_status,omitempty"`
	// Cancelled reports whether cancellation was requested for the request.
	Cancelled bool `json:"cancelled"`
	// NonInviteServerTransactionBehavior is the frozen behavior profile of the upstream
	// non-INVITE server transaction; it is the legacy zero value for INVITE
	// requests.
	NonInviteServerTransactionBehavior NonInviteServerTransactionBehavior `json:"non_invite_server_transaction_behavior,omitempty"`
	// RequestID is the observation correlation identifier minted at
	// admission. Restoring it keeps post-activation events under the
	// original request identity instead of minting a new one.
	RequestID string `json:"request_id"`
	// Sequence is the committed event dispatch sequence of the request's
	// observation stream: restored events continue past the stored value.
	Sequence uint64 `json:"sequence"`
	// FirstForwardCommitted reports whether the one-shot first successful
	// initial-send observation already committed, so a restore never
	// replays it.
	FirstForwardCommitted bool `json:"first_forward_committed"`
	// CompletionCommitted reports whether the completion observation
	// already committed with the terminal decision, so a restore never
	// replays it. It can never be set for an unfinished request.
	CompletionCommitted bool `json:"completion_committed"`
}

// Clone returns an independent copy of the snapshot: message envelopes,
// URIs, maps and slices are deep-copied so mutations of the clone cannot
// affect the original.
func (snap *ProxyRequestSnapshot) Clone() *ProxyRequestSnapshot {
	if snap == nil {
		return nil
	}

	clone := *snap
	clone.OriginalRequest = cloneReqEnvelope(snap.OriginalRequest)
	clone.WorkingRequest = cloneReqEnvelope(snap.WorkingRequest)
	// Stored plans are always normalized by construction, so the cloning
	// normalization cannot fail here.
	clone.TargetPlan, _ = clonePrxTargetPlan(snap.TargetPlan)
	clone.TargetStates = slices.Clone(snap.TargetStates)
	for i := range clone.TargetStates {
		clone.TargetStates[i].AttemptedAddresses = slices.Clone(snap.TargetStates[i].AttemptedAddresses)
	}
	clone.Attempts = slices.Clone(snap.Attempts)
	for i := range clone.Attempts {
		clone.Attempts[i].Request = cloneReqEnvelope(snap.Attempts[i].Request)
		clone.Attempts[i].Response = cloneResEnvelope(snap.Attempts[i].Response)
	}
	clone.Candidates = make([]*ResponseEnvelope, len(snap.Candidates))
	for i, res := range snap.Candidates {
		clone.Candidates[i] = cloneResEnvelope(res)
	}
	return &clone
}

// ProxyTargetSnapshot is the detached progress record of one plan target.
type ProxyTargetSnapshot struct {
	// TargetID is the plan target identifier.
	TargetID string `json:"target_id"`
	// Started reports whether the target was selected and its first attempt
	// generation started.
	Started bool `json:"started"`
	// Completed reports whether the target settled.
	Completed bool `json:"completed"`
	// Declined reports whether the target settled without ever starting —
	// for example when scheduling closed before its group was reached.
	Declined bool `json:"declined"`
	// PendingAddressRetry reports whether the target's address runner still
	// owns a failover decision while no live attempt is in flight.
	PendingAddressRetry bool `json:"pending_address_retry"`
	// Cancelling reports whether the target is under branch cancellation —
	// recorded from an accepted upstream CANCEL or a committed
	// global-failure direction; a cancelled branch never spawns a new
	// attempt.
	Cancelling bool `json:"cancelling"`
	// TimerCDeadline is the armed absolute Timer C deadline bounding the
	// target's attempt generation; zero when no deadline is armed. It
	// duplicates the live attempt's recorded deadline so a pending address
	// retry — a generation without a live attempt — still preserves the
	// bound that owns it.
	TimerCDeadline time.Time `json:"timer_c_deadline,omitzero"`
	// AttemptedAddresses is the ordered history of executed candidate
	// addresses.
	AttemptedAddresses []ResolvedAddr `json:"attempted_addresses"`
}

// ProxyAttemptSnapshot is the detached record of one committed downstream
// client transaction attempt.
type ProxyAttemptSnapshot struct {
	// Key is the client transaction key correlating the attempt; the
	// transaction's own snapshot is captured separately.
	Key ClientTransactionKey `json:"key"`
	// TargetID is the logical target the attempt belongs to.
	TargetID string `json:"target_id"`
	// Request is the outbound request copy sent by the attempt.
	Request *RequestEnvelope `json:"request,omitempty"`
	// Addr is the resolved destination the attempt was sent to.
	Addr ResolvedAddr `json:"addr,omitzero"`
	// State is the transaction state observed at capture time.
	State TransactionState `json:"state"`
	// Response is the latest response attributed to the attempt.
	Response *ResponseEnvelope `json:"response,omitempty"`
	// ErrorClass is the string classification of the attempt's local
	// failure — a stable class name, never an arbitrary error value.
	ErrorClass string `json:"error_class,omitempty"`
	// Completed reports whether the attempt produced its decision.
	Completed bool `json:"completed"`
	// Cancelled reports whether a downstream CANCEL was already initiated.
	Cancelled bool `json:"cancelled"`
	// DeferredCancel reports whether the attempt is under cancellation but
	// its downstream CANCEL is deferred to its first provisional response.
	DeferredCancel bool `json:"deferred_cancel"`
	// TimerCDeadline is the armed absolute Timer C deadline of the attempt
	// generation; zero when no deadline is armed.
	TimerCDeadline time.Time `json:"timer_c_deadline,omitzero"`
	// InitialSendSettled reports whether the attempt's initial-send
	// observation already committed. A committed attempt record always
	// carries it: a failed initial send discards its admission and leaves
	// no record at all.
	InitialSendSettled bool `json:"initial_send_settled"`
	// InitialSendSucceeded reports whether the observed initial send
	// succeeded; meaningful only when InitialSendSettled is true.
	InitialSendSucceeded bool `json:"initial_send_succeeded,omitempty"`
	// InitialSendErrorClass is the string classification of the observed
	// initial-send failure — a stable class name, never an arbitrary
	// error object; empty for a successful send.
	InitialSendErrorClass string `json:"initial_send_error_class,omitempty"`
}

// ProxyRequestSnapshotHandler is a callback invoked when a proxy request
// publishes a new committed snapshot revision.
//
// Notifications reflect committed coordinator state at ownership
// boundaries: a revision is published only after the triggering drain
// transition fully applied, so a partial branch decision — for example a
// still-pending client-transaction admission — is never published. Each
// handler receives an independent [ProxyRequestSnapshot.Clone], and
// handlers are invoked outside the coordinator mutation locks, so they may
// reentrantly bind and unbind.
type ProxyRequestSnapshotHandler interface {
	HandleProxyRequestSnapshot(ctx context.Context, snapshot *ProxyRequestSnapshot)
}

// ProxyRequestSnapshotHandlerFunc is a function adapter that implements
// [ProxyRequestSnapshotHandler].
type ProxyRequestSnapshotHandlerFunc func(ctx context.Context, snapshot *ProxyRequestSnapshot)

func (f ProxyRequestSnapshotHandlerFunc) HandleProxyRequestSnapshot(
	ctx context.Context,
	snapshot *ProxyRequestSnapshot,
) {
	f(ctx, snapshot)
}

// BindSnapshotHandler binds the callback invoked on each new published
// [ProxyRequestSnapshot] revision of any request owned by this proxy, see
// [ProxyRequestSnapshotHandler] for the publication contract.
//
// Binding observes future publications only, the last published revision is
// not replayed. The callback can be unbound by calling the returned
// function, unbind is idempotent and may be called reentrantly from inside
// the handler. A nil handler or a bind made once the proxy lifecycle ended
// returns a noop unbind; [Proxy.Shutdown] and [Proxy.Close] detach every
// tracked subscription.
func (prx *Proxy) BindSnapshotHandler(h ProxyRequestSnapshotHandler) (unbind func()) {
	if h == nil {
		return noop
	}
	return prx.trackSub(func() func() { return prx.onSnap.Add(h) })
}

// LookupRequest returns the request coordinator associated with the given
// upstream server transaction key.
func (prx *Proxy) LookupRequest(key ServerTransactionKey) (*ProxyRequest, bool) {
	return prx.loadSrvReq(key)
}

// Snapshot returns a detached [ProxyRequestSnapshot] of the committed
// coordinator state.
//
// The capture runs only at an ownership boundary: while a branch decision
// is still partial — before the plan is committed or while a
// client-transaction admission is pending — it fails with
// [errProxySnapshotUnstable] instead of capturing a partial decision. A
// request whose upstream transaction does not implement
// [ServerTransactionSnapshotter] — or whose non-INVITE transaction cannot
// report its frozen behavior through [NonInviteServerTransactionBehaviorProvider] — fails
// with [ErrProxySnapshotUnsupported]: such a request forwards normally but
// its snapshot could never be restored. Opaque metadata values that are not
// JSON-serializable fail the capture the same way.
func (rq *ProxyRequest) Snapshot() (*ProxyRequestSnapshot, error) {
	rq.mu.Lock()
	defer rq.mu.Unlock()
	return errors.Wrap2(rq.buildSnapshotLocked())
}

// buildSnapshotLocked builds the version-1 snapshot value under the
// coordinator guard. It stamps the current committed revision; callers
// publishing a new revision overwrite Revision and Time under the same
// guard hold.
func (rq *ProxyRequest) buildSnapshotLocked() (*ProxyRequestSnapshot, error) {
	// A snapshot may only be captured at a committed ownership boundary:
	// before the plan is committed, or while a client-transaction admission
	// is still pending, the branch decision is partial and nothing may be
	// captured or published.
	if !rq.begun || len(rq.pendingAdms) > 0 || rq.srvTx == nil || rq.work == nil {
		return nil, errors.Wrap(errPrxSnapshotUnstable)
	}
	// The upstream transaction must support its own snapshot capture: the
	// transaction snapshot is persisted separately, and a request snapshot
	// whose upstream transaction can never be captured is unrestorable.
	if _, ok := rq.srvTx.(ServerTransactionSnapshotter); !ok {
		return nil, errors.Wrap(NewProxySnapshotUnsupportedError("upstream transaction %v", rq.srvTx.Key()))
	}
	var niBehavior NonInviteServerTransactionBehavior
	if !rq.req.Method().Equal(RequestMethodInvite) {
		prvd, ok := rq.srvTx.(NonInviteServerTransactionBehaviorProvider)
		if !ok {
			return nil, errors.Wrap(NewProxySnapshotUnsupportedError("non-INVITE behavior of upstream transaction %v", rq.srvTx.Key()))
		}
		niBehavior = prvd.NonInviteServerTransactionBehavior()
	}

	snap := &ProxyRequestSnapshot{
		Version:                            prxReqSnapshotVer,
		Revision:                           rq.snapRev,
		Time:                               rq.snapTime,
		UpstreamKey:                        rq.srvKey,
		OriginalRequest:                    cloneReqEnvelope(rq.req),
		WorkingRequest:                     cloneReqEnvelope(rq.work),
		FinalForwarded:                     rq.finalForwarded,
		Completed:                          rq.done,
		Outcome:                            rq.outcome,
		Cancelled:                          rq.cancelIntent,
		NonInviteServerTransactionBehavior: niBehavior,
		RequestID:                          rq.id,
		Sequence:                           rq.evSeq,
		FirstForwardCommitted:              rq.firstFwd,
		CompletionCommitted:                rq.evDone,
	}
	if rq.route != nil {
		snap.RouteName = rq.route.Name
		snap.Mode = rq.route.ForwardMode
	}
	if rq.final != nil {
		snap.FinalStatus = rq.final.Status()
	}
	if err := prxSnapshotMetaErr("original request", snap.OriginalRequest.Metadata()); err != nil {
		return nil, err
	}
	if err := prxSnapshotMetaErr("working request", snap.WorkingRequest.Metadata()); err != nil {
		return nil, err
	}
	cands, err := rq.snapshotCandidatesLocked()
	if err != nil {
		return nil, err
	}
	snap.Candidates = cands

	plan, err := clonePrxTargetPlan(rq.plan)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	snap.TargetPlan = plan
	snap.TargetStates = rq.snapshotTargetStatesLocked()

	atts, err := rq.snapshotAttemptsLocked()
	if err != nil {
		return nil, err
	}
	snap.Attempts = atts

	return snap, nil
}

// snapshotCandidatesLocked clones the stored final-response candidates and
// rejects unsupported application metadata on each copy.
func (rq *ProxyRequest) snapshotCandidatesLocked() ([]*ResponseEnvelope, error) {
	var cands []*ResponseEnvelope
	for _, res := range rq.finals {
		clone := cloneResEnvelope(res)
		if err := prxSnapshotMetaErr("candidate response", clone.Metadata()); err != nil {
			return nil, err
		}
		cands = append(cands, clone)
	}
	return cands, nil
}

// snapshotTargetStatesLocked builds the per-target progress records sorted
// by target ID.
func (rq *ProxyRequest) snapshotTargetStatesLocked() []ProxyTargetSnapshot {
	states := make([]ProxyTargetSnapshot, 0, len(rq.targets))
	for id, st := range rq.targets {
		ts := ProxyTargetSnapshot{
			TargetID:           id,
			Started:            st.started,
			Completed:          st.done,
			Declined:           !st.started && st.done,
			Cancelling:         st.cancelling,
			AttemptedAddresses: slices.Clone(st.attemptAddrs),
		}
		if st.started && !st.done {
			ts.TimerCDeadline = st.timeC
			if st.runner != nil {
				if att := rq.attempts[st.txKey]; att == nil || att.settled {
					ts.PendingAddressRetry = true
				}
			}
		}
		states = append(states, ts)
	}
	slices.SortFunc(states, func(a, b ProxyTargetSnapshot) int {
		return strings.Compare(a.TargetID, b.TargetID)
	})
	return states
}

// snapshotAttemptsLocked builds the per-attempt records sorted by
// transaction key and rejects unsupported application metadata on each
// stored message copy.
func (rq *ProxyRequest) snapshotAttemptsLocked() ([]ProxyAttemptSnapshot, error) {
	atts := make([]ProxyAttemptSnapshot, 0, len(rq.attempts))
	for key, att := range rq.attempts {
		as := ProxyAttemptSnapshot{
			Key:                   key,
			TargetID:              att.targetID,
			Addr:                  att.addr,
			ErrorClass:            prxAttemptErrClass(att.lastErr),
			Completed:             att.settled,
			Cancelled:             att.cancelled,
			InitialSendSettled:    att.sendSettled,
			InitialSendSucceeded:  att.sendOK,
			InitialSendErrorClass: att.sendErrCls,
		}
		if st := rq.targets[att.targetID]; st != nil && st.txKey.Equal(key) {
			as.DeferredCancel = st.cancelling && !att.cancelled
			as.TimerCDeadline = st.timeC
		}
		if att.tx != nil {
			as.State = att.tx.State()
			as.Request = cloneReqEnvelope(att.req())
			as.Response = cloneResEnvelope(att.tx.LastResponse())
		}
		if as.Request != nil {
			if err := prxSnapshotMetaErr("attempt request", as.Request.Metadata()); err != nil {
				return nil, err
			}
		}
		if as.Response != nil {
			if err := prxSnapshotMetaErr("attempt response", as.Response.Metadata()); err != nil {
				return nil, err
			}
		}
		atts = append(atts, as)
	}
	slices.SortFunc(atts, func(a, b ProxyAttemptSnapshot) int {
		if c := strings.Compare(a.Key.Branch, b.Key.Branch); c != 0 {
			return c
		}
		return strings.Compare(a.Key.Method, b.Key.Method)
	})
	return atts, nil
}

// publishSnapshot emits one snapshot revision for the last committed drain
// transition. It runs outside the coordinator guard — snapshot delivery is
// an outside effect like every other observer notification — and emits
// nothing while the coordinator is mid-transition: a pending
// client-transaction admission means the branch decision is still partial.
// Recipients get independent [ProxyRequestSnapshot.Clone] copies.
func (rq *ProxyRequest) publishSnapshot(ctx context.Context) {
	snap, ok := rq.publishedSnapshot()
	if !ok {
		return
	}
	for h := range rq.prx.onSnap.All() {
		h.HandleProxyRequestSnapshot(ctx, snap.Clone())
	}
}

// publishedSnapshot commits a new snapshot revision under the coordinator
// guard and returns its detached value, or false when the coordinator is
// mid-transition. The deferred unlock also keeps the drain recoverable when
// a transaction accessor panics under the guard.
func (rq *ProxyRequest) publishedSnapshot() (*ProxyRequestSnapshot, bool) {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	snap, err := rq.buildSnapshotLocked()
	if err != nil {
		return nil, false
	}
	rq.snapRev++
	rq.snapTime = time.Now()
	snap.Revision = rq.snapRev
	snap.Time = rq.snapTime
	return snap, true
}

// prxSnapshotReqErr checks a saved request's presence and structure while
// preserving the snapshot classification and the validation cause.
func prxSnapshotReqErr(kind string, req *RequestEnvelope) error {
	if req == nil {
		return errors.Wrap(NewInvalidProxySnapshotError("missing %s", kind))
	}
	if err := req.Validate(); err != nil {
		return errors.JoinWrap(
			NewInvalidProxySnapshotError("invalid %s", kind),
			err,
		)
	}
	return nil
}

// prxSnapshotMetaErr reports the first opaque metadata value that is
// not JSON-serializable: a request snapshot is only capturable when every
// stored metadata value can be serialized.
func prxSnapshotMetaErr(what string, meta *MessageMetadata) error {
	for k, v := range meta.All() {
		if _, err := json.Marshal(v); err != nil {
			return errors.Wrap(NewProxySnapshotUnsupportedError("%s metadata key %q", what, k))
		}
	}
	return nil
}

// ErrProxyRequestStarted reports a [ProxyRequest.Start] call on a request
// that is already active: a repeated activation of a restored request, or
// a request that was never dormant.
const ErrProxyRequestStarted errors.Error = "proxy request already started"

// NewProxyRequestStartedError creates an [ErrProxyRequestStarted] error
// classified as a proxy error.
func NewProxyRequestStartedError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrProxyRequestStarted, args...),
		ErrClassProxy,
		errclass.ClassConflict,
	)
}

// ErrInvalidProxySnapshot reports a [ProxyRequestSnapshot] and
// [ProxyRestoreBindings] set that fails [Proxy.RestoreRequest]
// validation: an unsupported snapshot version, a route or mode
// mismatch, malformed or duplicate progress records, conflicting
// transaction key or request relationships, incompatible final or
// cancellation state, invalid deadlines, or missing and mismatched
// runtime bindings.
const ErrInvalidProxySnapshot errors.Error = "invalid proxy request snapshot"

// NewInvalidProxySnapshotError creates an [ErrInvalidProxySnapshot] error
// classified as a proxy error.
func NewInvalidProxySnapshotError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrInvalidProxySnapshot, args...),
		ErrClassProxy,
	)
}

// ProxyRestoreBindings carries the explicit runtime bindings installed by
// [Proxy.RestoreRequest] for one restored request.
//
// Every supplied transaction must be dormant: created through
// [RestoreServerTransaction] or [RestoreClientTransaction] and never
// started or terminated. Restoration claims the transactions through the
// same ownership-token admission seam the transaction-manager callback
// path uses, installs the event subscriptions, and indexes the request —
// without starting the supplied transactions, so a dormant request emits
// no sends and no callbacks until its explicit activation.
type ProxyRestoreBindings struct {
	// ServerTransaction is the dormant upstream server transaction the
	// request continues on. It is required, and its key and initial
	// request must match the snapshot's upstream identity.
	ServerTransaction ServerTransaction
	// ClientTransactions are the dormant downstream client transactions
	// of the request's live attempts, keyed by client transaction key.
	// Attempts already settled at capture need no binding: their stored
	// outcomes are restored instead of live transactions.
	ClientTransactions map[ClientTransactionKey]ClientTransaction
	// Deliveries are the rebound custom delivery resources of the
	// request's unresolved delivery references, keyed by
	// [ProxyDeliveryRef]. A nonzero reference carried by an unfinished
	// target requires a binding; an absent required flow is an explicit
	// failure, never an instruction to dial a replacement.
	Deliveries map[ProxyDeliveryRef]ProxyBoundDelivery
}

// prxRestoreStage is the validated, pre-commit restore plan of one
// request: the named route, the normalized target plan, the per-target
// progress records, and the verified runtime bindings. Validation and
// staging run without the proxy index guard so every consistency check
// fails before anything is installed.
type prxRestoreStage struct {
	prx        *Proxy
	route      *ProxyRoute
	srvTx      ServerTransaction
	plan       ProxyTargetPlan
	states     map[string]*ProxyTargetSnapshot
	clients    map[ClientTransactionKey]ClientTransaction
	deliveries map[ProxyDeliveryRef]ProxyBoundDelivery
}

// RestoreRequest validates a captured [ProxyRequestSnapshot] against its
// explicit [ProxyRestoreBindings] and installs the request coordinator
// dormant.
//
// Validation rejects an unsupported snapshot version, an unknown or
// mode-mismatched route, malformed or duplicate progress records,
// conflicting transaction key or request relationships, incompatible
// final or cancellation state, invalid deadlines, and missing or
// mismatched bindings — before anything is installed or activated.
// Terminal historical attempts are restored from their stored outcomes
// and never require live transaction bindings; an unfinished target
// carrying a nonzero [ProxyDeliveryRef] requires its rebound flow and
// fails without resolving or dialing a replacement.
//
// The saved non-INVITE server behavior of the checkpoint is verified
// against the supplied dormant binding through
// [NonInviteServerTransactionBehaviorProvider] — never through the snapshotter contract,
// which a dormant transaction cannot serve before termination. The saved
// mode wins over current route creation defaults; a binding lacking the
// provider fails with [ErrProxySnapshotUnsupported] for the recoverable
// mode check while remaining usable for ordinary forwarding.
//
// A successful restore installs the coordinator, its event
// subscriptions, and the upstream index entry without starting the
// supplied transactions: the dormant request emits no sends, no
// callbacks, and no snapshot revisions until its explicit activation.
// A failed install rolls back only the registrations staged by this
// call and leaves the supplied transactions and the existing index
// untouched.
func (prx *Proxy) RestoreRequest(
	ctx context.Context,
	snap *ProxyRequestSnapshot,
	bindings ProxyRestoreBindings,
) (*ProxyRequest, error) {
	if prx.closed.Load() {
		return nil, errors.Wrap(NewProxyClosedError())
	}
	stage, err := prx.validatePrxRestore(snap, bindings)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	rq := stage.install(ctx, snap)
	if err := prx.commitRestoredReq(rq); err != nil {
		rq.rollbackStagedBinds()
		return nil, errors.Wrap(err)
	}
	return rq, nil
}

// routeByName returns the configured route carrying name.
func (prx *Proxy) routeByName(name string) *ProxyRoute {
	for i := range prx.routes {
		if prx.routes[i].Name == name {
			return &prx.routes[i]
		}
	}
	return nil
}

// Start activates a dormant request installed by [Proxy.RestoreRequest]:
// activation ownership transfers to the request handle.
//
// Start is once-only — a repeated call, or a call on a request that was
// never dormant, returns [ErrProxyRequestStarted]. The supplied dormant
// transactions are started only after the coordinator associations and
// event subscriptions installed at restore are already in place, so no
// transaction event can outrun its attribution; a terminated supplied
// transaction is skipped — a terminal restored request is inspect-only
// and emits nothing. Activation replays no initial send, no completed
// decision, and no resolved contact: the queued activation event re-arms
// preserved absolute deadlines, rebuilds the live targets' lazy address
// runners, resumes a pending address retry's deferred failover, resumes
// the recorded cancellation's still-deferred work, and continues target
// scheduling from the restored progress — all through the ordinary
// coordinator queue, where completed groups and targets can never rerun.
func (rq *ProxyRequest) Start(ctx context.Context) error {
	rq.mu.Lock()
	if rq.closed {
		rq.mu.Unlock()
		return errors.Wrap(NewProxyClosedError())
	}
	if !rq.dormant {
		rq.mu.Unlock()
		return errors.Wrap(NewProxyRequestStartedError())
	}
	rq.dormant = false

	srvTx := rq.srvTx
	txs := make([]ClientTransaction, 0, len(rq.attempts))
	for _, att := range rq.attempts {
		if att.tx != nil {
			txs = append(txs, att.tx)
		}
	}
	rq.mu.Unlock()

	slices.SortFunc(txs, func(a, b ClientTransaction) int {
		if c := strings.Compare(a.Key().Branch, b.Key().Branch); c != 0 {
			return c
		}
		return strings.Compare(a.Key().Method, b.Key().Method)
	})

	// Every coordinator subscription was bound at restore time, before any
	// transaction may emit an event: a dormant binding that can still move
	// is started now and queues its first events while the request is
	// already active.
	var errs []error
	if srvTx != nil && srvTx.State() != TransactionStateTerminated && !srvTx.Started() {
		if err := srvTx.Start(ContextWithTransaction(ctx, srvTx)); err != nil {
			errs = append(errs, err)
		}
	}
	for _, tx := range txs {
		if tx.Started() || tx.State() == TransactionStateTerminated {
			continue
		}
		if err := tx.Start(ContextWithTransaction(ctx, tx)); err != nil {
			errs = append(errs, err)
		}
	}

	// The activation resume is queued after the dormant history it must
	// follow: events accumulated while dormant apply first, then the
	// activation event rebuilds the deferred runtime work.
	rq.enqueue(ctx, prxWorkEvent{kind: prxEvtStart})
	return errors.JoinPrefixWrap("start restored transactions:", errs...)
}

// validatePrxRestore runs every consistency check of
// [Proxy.RestoreRequest] and returns the staged install plan. Nothing is
// installed or subscribed here: a rejection leaves no trace on the proxy
// or the supplied bindings.
func (prx *Proxy) validatePrxRestore(
	snap *ProxyRequestSnapshot,
	bindings ProxyRestoreBindings,
) (*prxRestoreStage, error) {
	if snap == nil {
		return nil, errors.Wrap(NewInvalidProxySnapshotError())
	}
	if snap.Version != prxReqSnapshotVer {
		return nil, errors.Wrap(NewInvalidProxySnapshotError("unsupported version %d", snap.Version))
	}
	// A coordinator only exists on the stateful path: a stateless-mode
	// checkpoint can never describe an owned upstream/downstream request.
	if snap.Mode != ForwardModeStateful {
		return nil, errors.Wrap(NewInvalidProxySnapshotError("unexpected forward mode %v", snap.Mode))
	}
	route := prx.routeByName(snap.RouteName)
	if route == nil {
		return nil, errors.Wrap(NewInvalidProxySnapshotError("unknown proxy route %q", snap.RouteName))
	}
	// A request promoted out of a stateless route ran under a stateful-mode
	// route copy: its snapshot records the owning route name with the actual
	// stateful processing mode rather than the stored configuration, so the
	// name-plus-mode pair is itself the promotion record. The restored
	// coordinator continues under the same stateful copy — the request stays
	// owned by the named route while its sends keep the stateful ownership
	// mark.
	if route.ForwardMode != snap.Mode {
		promoted := *route
		promoted.ForwardMode = snap.Mode
		route = &promoted
	}

	if err := prxSnapshotReqErr("original request", snap.OriginalRequest); err != nil {
		return nil, errors.Wrap(err)
	}
	method := snap.OriginalRequest.Method()
	if method.Equal(RequestMethodAck) || method.Equal(RequestMethodCancel) {
		return nil, errors.Wrap(NewInvalidProxySnapshotError("unexpected request method %v", method))
	}
	if err := prxSnapshotReqErr("working request", snap.WorkingRequest); err != nil {
		return nil, errors.Wrap(err)
	}
	if !snap.WorkingRequest.Method().Equal(method) {
		return nil, errors.Wrap(NewInvalidProxySnapshotError("working request method does not match the original request"))
	}
	invite := method.Equal(RequestMethodInvite)
	origKey, err := MakeServerTransactionKey(snap.OriginalRequest)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if !snap.UpstreamKey.Equal(origKey) {
		return nil, errors.Wrap(NewInvalidProxySnapshotError("upstream key does not match the original request"))
	}
	// A committed dispatch sequence without its correlation identity is a
	// corrupt checkpoint: restored events must continue the recorded
	// stream under the original request ID.
	if snap.Sequence > 0 && snap.RequestID == "" {
		return nil, errors.Wrap(NewInvalidProxySnapshotError("event sequence without request correlation"))
	}
	if err := prxSnapshotMetaErr("original request", snap.OriginalRequest.Metadata()); err != nil {
		return nil, errors.Wrap(err)
	}
	if err := prxSnapshotMetaErr("working request", snap.WorkingRequest.Metadata()); err != nil {
		return nil, errors.Wrap(err)
	}

	plan, err := clonePrxTargetPlan(snap.TargetPlan)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if !plan.hasTargets() {
		return nil, errors.Wrap(NewInvalidProxySnapshotError("empty target plan"))
	}

	states, err := validatePrxRestoreTargets(invite, plan, snap.TargetStates)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if err := validatePrxRestoreAttempts(invite, snap, states); err != nil {
		return nil, errors.Wrap(err)
	}
	if err := validatePrxRestoreOutcome(invite, snap); err != nil {
		return nil, errors.Wrap(err)
	}
	if err := validatePrxRestoreServer(invite, snap, bindings.ServerTransaction); err != nil {
		return nil, errors.Wrap(err)
	}
	clients, err := validatePrxRestoreClients(invite, snap, bindings.ClientTransactions)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	deliveries, err := validatePrxRestoreDeliveries(plan, states, bindings.Deliveries)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return &prxRestoreStage{
		prx:        prx,
		route:      route,
		srvTx:      bindings.ServerTransaction,
		plan:       plan,
		states:     states,
		clients:    clients,
		deliveries: deliveries,
	}, nil
}

// validatePrxRestoreTargets checks the per-target progress records form
// an exact cover of the normalized plan and returns them keyed by target
// ID.
//
//nolint:gocognit // linear chain of plan validation checks
func validatePrxRestoreTargets(
	invite bool,
	plan ProxyTargetPlan,
	raw []ProxyTargetSnapshot,
) (map[string]*ProxyTargetSnapshot, error) {
	states := make(map[string]*ProxyTargetSnapshot, len(raw))
	var planTargets int
	for _, group := range plan.Groups {
		planTargets += len(group)
	}
	if len(raw) != planTargets {
		return nil, errors.Wrap(NewInvalidProxySnapshotError("target states do not cover the plan"))
	}
	for i := range raw {
		ts := &raw[i]
		if _, ok := states[ts.TargetID]; ok {
			return nil, errors.Wrap(NewInvalidProxySnapshotError("duplicate proxy target ID %q", ts.TargetID))
		}
		states[ts.TargetID] = ts
		if ts.Declined && ts.Started {
			return nil, errors.Wrap(NewInvalidProxySnapshotError("target %q is both started and declined", ts.TargetID))
		}
		if ts.PendingAddressRetry && (!ts.Started || ts.Completed) {
			return nil, errors.Wrap(NewInvalidProxySnapshotError("target %q has an invalid pending retry", ts.TargetID))
		}
		if ts.Cancelling && (!ts.Started || ts.Completed) {
			return nil, errors.Wrap(NewInvalidProxySnapshotError("target %q has an invalid cancellation mark", ts.TargetID))
		}
		if !ts.TimerCDeadline.IsZero() && (!invite || !ts.Started || ts.Completed) {
			return nil, errors.Wrap(NewInvalidProxySnapshotError("target %q carries an invalid Timer C deadline", ts.TargetID))
		}
	}
	for _, group := range plan.Groups {
		for _, trgt := range group {
			if states[trgt.ID] == nil {
				return nil, errors.Wrap(NewInvalidProxySnapshotError("target %q has no progress record", trgt.ID))
			}
		}
	}
	return states, nil
}

// validatePrxRestoreAttempts checks the attempt records: unique valid
// keys belonging to started plan targets, consistent transaction-key and
// request relationships, stored outcomes for settled attempts, and
// cancel/deadline flag compatibility.
func validatePrxRestoreAttempts(
	invite bool,
	snap *ProxyRequestSnapshot,
	states map[string]*ProxyTargetSnapshot,
) error {
	// A downstream CANCEL is initiated by an accepted upstream CANCEL or a
	// committed global-failure direction — the recorded cancellation marks
	// stay valid in both cases.
	committedDir := snap.Completed
	for _, res := range snap.Candidates {
		if res != nil && res.Status().IsGlobalFailure() {
			committedDir = true
			break
		}
	}
	seen := make(map[ClientTransactionKey]struct{}, len(snap.Attempts))
	for i := range snap.Attempts {
		if err := validatePrxRestoreAttempt(
			invite, snap, committedDir, &snap.Attempts[i], states, seen,
		); err != nil {
			return err
		}
	}
	return nil
}

// validatePrxRestoreAttempt checks one recorded attempt: its transaction
// key is unique and valid, it belongs to a started target, its stored
// request reproduces the key, its cancellation marks agree with the
// request-level direction, and its settled outcome, initial-send
// observation, and Timer C deadline are internally consistent.
func validatePrxRestoreAttempt(
	invite bool,
	snap *ProxyRequestSnapshot,
	committedDir bool,
	att *ProxyAttemptSnapshot,
	states map[string]*ProxyTargetSnapshot,
	seen map[ClientTransactionKey]struct{},
) error {
	if !att.Key.IsValid() {
		return errors.Wrap(NewInvalidProxySnapshotError("invalid attempt transaction key %v", att.Key))
	}
	if _, dup := seen[att.Key]; dup {
		return errors.Wrap(NewInvalidProxySnapshotError("duplicate attempt transaction key %v", att.Key))
	}
	seen[att.Key] = struct{}{}

	st := states[att.TargetID]
	if st == nil || !st.Started {
		return errors.Wrap(NewInvalidProxySnapshotError("attempt %v belongs to an unstarted target %q", att.Key, att.TargetID))
	}
	if !att.State.IsValid() || att.State == TransactionStateTerminated {
		return errors.Wrap(NewInvalidProxySnapshotError("invalid attempt %v transaction state %v", att.Key, att.State))
	}
	if att.Request == nil {
		return errors.Wrap(NewInvalidProxySnapshotError("attempt %v misses its request", att.Key))
	}
	reqKey, err := MakeClientTransactionKey(att.Request)
	if err != nil {
		return errors.Wrap(err)
	}
	if !reqKey.Equal(att.Key) {
		return errors.Wrap(NewInvalidProxySnapshotError("attempt %v request does not match its key", att.Key))
	}
	if att.Cancelled && !snap.Cancelled && !committedDir {
		return errors.Wrap(NewInvalidProxySnapshotError("attempt %v is cancelled without request cancellation", att.Key))
	}
	if att.DeferredCancel && ((!snap.Cancelled && !committedDir) || att.Cancelled) {
		return errors.Wrap(NewInvalidProxySnapshotError("attempt %v has an invalid deferred cancellation", att.Key))
	}
	if att.Completed && att.Response == nil && att.ErrorClass == "" {
		return errors.Wrap(NewInvalidProxySnapshotError("settled attempt %v has no stored outcome", att.Key))
	}
	// A committed attempt record implies its initial-send observation
	// already committed: a failed send discards its admission and
	// leaves no record. The stored outcome must be a coherent
	// success-or-class pair.
	if !att.InitialSendSettled {
		return errors.Wrap(NewInvalidProxySnapshotError("attempt %v has no settled initial send", att.Key))
	}
	if att.InitialSendSucceeded == (att.InitialSendErrorClass != "") {
		return errors.Wrap(NewInvalidProxySnapshotError("attempt %v has an inconsistent initial send outcome", att.Key))
	}
	// A live attempt may legitimately carry an armed deadline even
	// under initiated cancellation: a cancelled branch keeps its
	// Timer C bound until it truly settles.
	if !att.TimerCDeadline.IsZero() && (!invite || att.Completed) {
		return errors.Wrap(NewInvalidProxySnapshotError("attempt %v carries an invalid Timer C deadline", att.Key))
	}
	return nil
}

// validatePrxRestoreOutcome checks the committed decision fields: the
// final/cancellation state, the final status, the forwarded-final flag,
// and the saved non-INVITE behavior must agree with each other and with
// the request method.
func validatePrxRestoreOutcome(invite bool, snap *ProxyRequestSnapshot) error {
	if snap.Cancelled && !invite {
		return errors.Wrap(NewInvalidProxySnapshotError("cancellation recorded for a non-INVITE request"))
	}
	if snap.CompletionCommitted && !snap.Completed {
		return errors.Wrap(NewInvalidProxySnapshotError("completion committed for an unfinished request"))
	}
	for i, res := range snap.Candidates {
		if res == nil {
			return errors.Wrap(NewInvalidProxySnapshotError("nil response context candidate %d", i))
		}
	}
	if err := validatePrxRestoreOutcomeState(snap); err != nil {
		return err
	}
	if err := validatePrxRestoreFinalStatus(snap); err != nil {
		return err
	}
	if invite {
		if snap.NonInviteServerTransactionBehavior != NonInviteServerTransactionBehaviorLegacy {
			return errors.Wrap(NewInvalidProxySnapshotError("non-INVITE behavior recorded for an INVITE request"))
		}
	} else if !snap.NonInviteServerTransactionBehavior.supported() {
		return errors.Wrap(NewInvalidProxySnapshotError("unsupported non-INVITE behavior %d", snap.NonInviteServerTransactionBehavior))
	}
	return nil
}

// validatePrxRestoreOutcomeState checks that the recorded outcome is one
// a request in the snapshot's completion state can commit, and that
// cancellation intent accompanies a cancelled outcome.
func validatePrxRestoreOutcomeState(snap *ProxyRequestSnapshot) error {
	if snap.Completed {
		switch snap.Outcome {
		case ProxyOutcomeResponse, ProxyOutcomeRejected, ProxyOutcomeTimeout,
			ProxyOutcomeCancelled, ProxyOutcomeSendFailure, ProxyOutcomeAbandoned:
		default:
			return errors.Wrap(NewInvalidProxySnapshotError("unexpected outcome %v", snap.Outcome))
		}
		if snap.Outcome == ProxyOutcomeCancelled && !snap.Cancelled {
			return errors.Wrap(NewInvalidProxySnapshotError("cancelled outcome without cancellation intent"))
		}
		return nil
	}
	if snap.Outcome != ProxyOutcomeResponse {
		return errors.Wrap(NewInvalidProxySnapshotError("uncommitted outcome %v", snap.Outcome))
	}
	return nil
}

// validatePrxRestoreFinalStatus checks that a recorded final status is a
// valid final response, that it matches the stored response context on a
// committed response outcome, and that a forwarded final names a final
// candidate or a committed request decision.
func validatePrxRestoreFinalStatus(snap *ProxyRequestSnapshot) error {
	if snap.FinalStatus != 0 && (!snap.FinalStatus.IsValid() || !snap.FinalStatus.IsFinal()) {
		return errors.Wrap(NewInvalidProxySnapshotError("invalid final status %v", snap.FinalStatus))
	}
	if snap.Outcome == ProxyOutcomeResponse && snap.Completed {
		final, err := selectFinalRes(snap.Candidates)
		if err != nil {
			return errors.Wrap(NewInvalidProxySnapshotError("response outcome without a selectable final"))
		}
		if !final.Status().Equal(snap.FinalStatus) {
			return errors.Wrap(NewInvalidProxySnapshotError("final status %v does not match the response context", snap.FinalStatus))
		}
	} else if snap.FinalStatus != 0 {
		// A recorded final status only exists on a committed response
		// outcome: an unfinished request or any other outcome never sets it.
		return errors.Wrap(NewInvalidProxySnapshotError("final status recorded for outcome %v", snap.Outcome))
	}
	if snap.FinalForwarded {
		// The forwarded final either came from the response context or was
		// generated by the request's own terminal decision — a cancelled or
		// timed-out request can commit a forwarded 487/408 without any
		// downstream candidate. An unfinished request must name the final
		// candidate it forwarded.
		forwarded := snap.Completed
		for _, res := range snap.Candidates {
			if res != nil && res.Status().IsFinal() {
				forwarded = true
				break
			}
		}
		if !forwarded {
			return errors.Wrap(NewInvalidProxySnapshotError("forwarded final without a final candidate"))
		}
	}
	return nil
}

// validatePrxRestoreServer checks the required dormant upstream server
// transaction binding: its type, key, initial request and lifecycle state
// must match the snapshot, and a non-INVITE binding must reproduce the
// saved behavior through [NonInviteServerTransactionBehaviorProvider] — never through the
// snapshotter contract a dormant transaction cannot serve.
func validatePrxRestoreServer(
	invite bool,
	snap *ProxyRequestSnapshot,
	tx ServerTransaction,
) error {
	if util.IsNil(tx) {
		return errors.Wrap(NewInvalidProxySnapshotError("missing upstream server transaction binding"))
	}
	wantType := TransactionTypeServerNonInvite
	if invite {
		wantType = TransactionTypeServerInvite
	}
	if tx.Type() != wantType {
		return errors.Wrap(NewInvalidProxySnapshotError("unexpected upstream transaction type %v", tx.Type()))
	}
	if !tx.Key().Equal(snap.UpstreamKey) {
		return errors.Wrap(NewInvalidProxySnapshotError("upstream transaction key does not match the snapshot"))
	}
	srvReq := tx.Request()
	if srvReq == nil {
		return errors.Wrap(NewInvalidProxySnapshotError("upstream transaction misses its request"))
	}
	txKey, err := MakeServerTransactionKey(srvReq)
	if err != nil {
		return errors.Wrap(err)
	}
	if !txKey.Equal(snap.UpstreamKey) || !srvReq.Equal(snap.OriginalRequest) {
		return errors.Wrap(NewInvalidProxySnapshotError("upstream transaction request does not match the snapshot"))
	}
	if tx.Started() {
		return errors.Wrap(NewInvalidProxySnapshotError("upstream transaction is not dormant"))
	}
	if tx.State() == TransactionStateTerminated && !snap.Completed {
		return errors.Wrap(NewInvalidProxySnapshotError("upstream transaction terminated for an unfinished request"))
	}
	if invite {
		return nil
	}
	prvd, ok := tx.(NonInviteServerTransactionBehaviorProvider)
	if !ok {
		return errors.Wrap(NewProxySnapshotUnsupportedError("non-INVITE behavior of upstream transaction %v", tx.Key()))
	}
	if prvd.NonInviteServerTransactionBehavior() != snap.NonInviteServerTransactionBehavior {
		return errors.Wrap(NewInvalidProxySnapshotError(
			"upstream transaction behavior %v does not match the saved %v",
			prvd.NonInviteServerTransactionBehavior(),
			snap.NonInviteServerTransactionBehavior,
		))
	}
	return nil
}

// validatePrxRestoreClients checks the dormant downstream client
// transaction bindings: every live attempt requires a binding matching
// its recorded key, request, type, and transaction state; every supplied
// binding must name a recorded attempt; settled historical attempts are
// restored from their stored outcomes and accept an optional matching
// binding.
func validatePrxRestoreClients(
	invite bool,
	snap *ProxyRequestSnapshot,
	bindings map[ClientTransactionKey]ClientTransaction,
) (map[ClientTransactionKey]ClientTransaction, error) {
	wantType := TransactionTypeClientNonInvite
	if invite {
		wantType = TransactionTypeClientInvite
	}
	for i := range snap.Attempts {
		att := &snap.Attempts[i]
		if att.Completed {
			continue
		}
		if _, ok := bindings[att.Key]; !ok {
			return nil, errors.Wrap(NewInvalidProxySnapshotError("missing client transaction binding %v", att.Key))
		}
	}
	for key, tx := range bindings {
		if err := validatePrxRestoreClnBinding(wantType, snap, key, tx); err != nil {
			return nil, err
		}
	}
	return bindings, nil
}

// validatePrxRestoreClnBinding checks one supplied downstream binding
// against the snapshot: it must name a recorded attempt, be a non-nil
// dormant transaction of the matching type and key, and reproduce the
// attempt's request and transaction state.
func validatePrxRestoreClnBinding(
	wantType TransactionType,
	snap *ProxyRequestSnapshot,
	key ClientTransactionKey,
	tx ClientTransaction,
) error {
	var att *ProxyAttemptSnapshot
	for i := range snap.Attempts {
		if snap.Attempts[i].Key.Equal(key) {
			att = &snap.Attempts[i]
			break
		}
	}
	if att == nil {
		return errors.Wrap(NewInvalidProxySnapshotError("unexpected client transaction binding %v", key))
	}
	if util.IsNil(tx) {
		return errors.Wrap(NewInvalidProxySnapshotError("nil client transaction binding %v", key))
	}
	if tx.Type() != wantType {
		return errors.Wrap(NewInvalidProxySnapshotError("unexpected client transaction type %v", tx.Type()))
	}
	if !tx.Key().Equal(key) {
		return errors.Wrap(NewInvalidProxySnapshotError("client transaction key does not match the binding %v", key))
	}
	clnReq := tx.Request()
	if clnReq == nil {
		return errors.Wrap(NewInvalidProxySnapshotError("client transaction %v misses its request", key))
	}
	txKey, err := MakeClientTransactionKey(clnReq)
	if err != nil {
		return errors.Wrap(err)
	}
	if !txKey.Equal(key) || !clnReq.Equal(att.Request) {
		return errors.Wrap(NewInvalidProxySnapshotError("client transaction request does not match the snapshot %v", key))
	}
	if tx.Started() {
		return errors.Wrap(NewInvalidProxySnapshotError("client transaction %v is not dormant", key))
	}
	if tx.State() != att.State {
		// A settled attempt's binding may legally have terminated after
		// its checkpoint: termination adds no decision, so the saved
		// Completed state stays compatible with a terminated binding.
		if !att.Completed || tx.State() != TransactionStateTerminated {
			return errors.Wrap(NewInvalidProxySnapshotError(
				"client transaction %v state %v does not match the saved %v",
				key, tx.State(),
				att.State,
			))
		}
	}
	return nil
}

// validatePrxRestoreDeliveries checks the rebound custom delivery
// resources: every nonzero delivery reference carried by an unfinished
// target requires an explicit binding, every supplied reference must
// name a plan reference, and every bound delivery must be usable — a
// bound transport and a valid remote address. A missing required flow is
// an explicit failure, never a resolver call or an implicit dial.
func validatePrxRestoreDeliveries(
	plan ProxyTargetPlan,
	states map[string]*ProxyTargetSnapshot,
	bindings map[ProxyDeliveryRef]ProxyBoundDelivery,
) (map[ProxyDeliveryRef]ProxyBoundDelivery, error) {
	refs := make(map[ProxyDeliveryRef]bool)
	for _, group := range plan.Groups {
		for _, trgt := range group {
			if trgt.Delivery == (ProxyDeliveryRef{}) {
				continue
			}
			st := states[trgt.ID]
			refs[trgt.Delivery] = refs[trgt.Delivery] || st == nil || !st.Completed
		}
	}
	for ref := range bindings {
		if _, ok := refs[ref]; !ok || ref == (ProxyDeliveryRef{}) {
			return nil, errors.Wrap(NewInvalidProxySnapshotError("unexpected delivery binding %q/%q", ref.Kind, ref.Key))
		}
	}
	for ref, required := range refs {
		if required {
			if _, ok := bindings[ref]; !ok {
				return nil, errors.Wrap(NewInvalidProxySnapshotError("missing delivery binding %q/%q", ref.Kind, ref.Key))
			}
		}
	}
	for ref, bound := range bindings {
		if util.IsNil(bound.Transport) {
			return nil, errors.Wrap(NewInvalidProxySnapshotError(
				"delivery binding %q/%q has no bound transport",
				ref.Kind,
				ref.Key,
			))
		}
		if !bound.RemoteAddr.IsValid() {
			return nil, errors.Wrap(NewInvalidProxySnapshotError(
				"delivery binding %q/%q has no bound remote address",
				ref.Kind,
				ref.Key,
			))
		}
	}
	return bindings, nil
}

// install builds the dormant coordinator of a validated restore: the
// committed orchestration state is populated, the rebound deliveries are
// installed, and the supplied transactions are claimed through the
// ownership-token admission seam before the index commit — all outside
// the proxy index guard, so a transaction callback may reenter safely and
// a later commit failure can roll back only the staged registrations.
func (s *prxRestoreStage) install(ctx context.Context, snap *ProxyRequestSnapshot) *ProxyRequest {
	rq := newPrxReq(ctx, s.prx, s.route, cloneReqEnvelope(snap.OriginalRequest))

	rq.mu.Lock()
	rq.dormant = true
	rq.begun = true
	// The saved working envelope is adopted as-is: it already carries the
	// validated post-PreprocessRoute plus local-route-consumption state of
	// the original admission, so neither the application hook nor the
	// deterministic Section 16.4 consumption is replayed here. The
	// snapshot's OriginalRequest itself keeps the pre-preprocess identity
	// the upstream transaction validation compares.
	rq.work = cloneReqEnvelope(snap.WorkingRequest)
	rq.plan = s.plan
	rq.snapRev = snap.Revision
	rq.snapTime = snap.Time
	// The restored correlation keeps the original request identity and
	// dispatch sequence so post-activation events continue the recorded
	// stream instead of minting a new identity; the committed one-shot
	// decisions are restored with it so no historical observation can
	// re-fire.
	if snap.RequestID != "" {
		rq.id = snap.RequestID
	}
	rq.evSeq = snap.Sequence
	rq.firstFwd = snap.FirstForwardCommitted
	rq.evDone = snap.CompletionCommitted
	rq.cancelIntent = snap.Cancelled
	rq.schedClosed = snap.Cancelled || snap.Completed
	rq.done = snap.Completed
	rq.finalForwarded = snap.FinalForwarded
	if snap.Completed {
		rq.outcome = snap.Outcome
	}
	for _, res := range snap.Candidates {
		rq.finals = append(rq.finals, cloneResEnvelope(res))
		// Format v1 has no dedicated scheduling-close field, so activation
		// derives it: a recorded direction commitment — a received success
		// or global failure in the response context — closed scheduling at
		// capture time the same way [ProxyRequest.closeSchedulingLocked]
		// does on a live event.
		if sts := res.Status(); sts.IsSuccessful() || sts.IsGlobalFailure() {
			rq.schedClosed = true
		}
	}
	if snap.Completed && snap.Outcome == ProxyOutcomeResponse {
		// Validation already proved the selection commits.
		rq.final, _ = selectFinalRes(rq.finals)
	}
	// The explicit bindings never alias the caller's map: a later caller
	// mutation must not rewrite the coordinator's restored flows.
	rq.deliveries = maps.Clone(s.deliveries)
	for _, group := range s.plan.Groups {
		for _, trgt := range group {
			ts := s.states[trgt.ID]
			st := &prxTargetState{
				target:       trgt,
				started:      ts.Started,
				done:         ts.Completed,
				cancelling:   ts.Cancelling || (snap.Cancelled && ts.Started && !ts.Completed),
				attemptAddrs: slices.Clone(ts.AttemptedAddresses),
			}
			if st.started && !st.done {
				st.gen = rq.nextGenLocked()
				// The recorded generation deadline is re-armed as a runtime
				// timer at activation; a live attempt's own deadline
				// supersedes it on install.
				st.timeC = ts.TimerCDeadline
			}
			rq.targets[trgt.ID] = st
		}
	}
	for i := range snap.Attempts {
		att := &snap.Attempts[i]
		if att.Completed {
			rq.outcomes = append(rq.outcomes, ProxyBranchOutcome{
				TargetID:   att.TargetID,
				AttemptKey: att.Key,
				Response:   cloneResEnvelope(att.Response),
				Error:      prxRestoreOutcomeErr(att.ErrorClass),
				Completed:  true,
				Cancelled:  att.Cancelled,
			})
		}
	}
	rq.mu.Unlock()

	for i := range snap.Attempts {
		att := &snap.Attempts[i]
		tx := s.clients[att.Key]
		if tx == nil {
			continue
		}
		rq.installRestoredClnTx(ctx, att, tx)
	}

	rq.stageSrvTx(s.srvTx)
	return rq
}

// installRestoredClnTx claims one supplied dormant client transaction
// for its restored attempt: the binding carries a minted ownership token
// through the same admission seam the transaction-manager callback path
// uses, then commits the admission immediately so no transaction event
// can outrun the association. Queued events cannot drain while the
// request is dormant.
func (rq *ProxyRequest) installRestoredClnTx(
	ctx context.Context,
	att *ProxyAttemptSnapshot,
	tx ClientTransaction,
) {
	rq.mu.Lock()
	st := rq.targets[att.TargetID]
	var runGen uint64
	if st != nil {
		if att.Completed {
			// A settled historical attempt must never claim the target's
			// live generation: its admission correlates under a fresh
			// generation that cannot match st.gen, so committing it leaves
			// the live attempt's txKey attribution untouched.
			runGen = rq.nextGenLocked()
		} else {
			runGen = st.gen
		}
	}
	rq.mu.Unlock()

	owner := &prxTxOwner{rq: rq, targetID: att.TargetID, gen: runGen}
	rq.attachClnTx(tx, owner)
	rq.commitTxAdmissions(ctx, owner, tx, att.Addr)

	rq.mu.Lock()
	defer rq.mu.Unlock()
	if rec := rq.attempts[att.Key]; rec != nil {
		rec.settled = att.Completed
		rec.cancelled = att.Cancelled
		rec.sawRes = att.Response != nil
		rec.lastErr = prxRestoreOutcomeErr(att.ErrorClass)
		rec.sendSettled = att.InitialSendSettled
		rec.sendOK = att.InitialSendSucceeded
		rec.sendErrCls = att.InitialSendErrorClass
	}
	if st != nil && !att.Completed && st.txKey.Equal(att.Key) && !st.done {
		st.lastRes = cloneResEnvelope(att.Response)
		if att.DeferredCancel || att.Cancelled {
			st.cancelling = true
		}
		st.timeC = att.TimerCDeadline
	}
}

// commitRestoredReq commits the upstream index entry of a staged
// dormant restore under the proxy index guard: a key already owned by
// another request conflicts and leaves the existing entry untouched. A
// request whose upstream transaction is already gone keeps no index
// entry, matching the ordinary lifecycle release.
func (prx *Proxy) commitRestoredReq(rq *ProxyRequest) error {
	rq.mu.Lock()
	key, gone := rq.srvKey, rq.srvGone
	rq.mu.Unlock()
	if !key.IsValid() || gone {
		return nil
	}

	prx.reqsMu.Lock()
	defer prx.reqsMu.Unlock()
	if prx.closed.Load() {
		return errors.Wrap(NewProxyClosedError())
	}
	if cur, ok := prx.srvRqs[key]; ok && cur != rq {
		return errors.Wrap(NewInvalidProxySnapshotError("upstream key %v already bound", key))
	}
	prx.srvRqs[key] = rq
	return nil
}

// rollbackStagedBinds releases only the registrations staged by a failed
// restore install. The supplied transactions and any pre-existing index
// entries are borrowed resources and are left untouched.
func (rq *ProxyRequest) rollbackStagedBinds() {
	for _, unbind := range rq.unbinds {
		unbind()
	}
}

// errRestoredPrxAttempt marks a local attempt outcome reconstructed
// from a snapshot's stored error classification.
const errRestoredPrxAttempt errors.Error = "restored proxy attempt outcome"

// prxRestoreOutcomeErr reconstructs the representative error of a
// settled attempt's stored classification: a recorded timeout keeps the
// timeout sentinel so the outcome ranking still applies, other classes
// keep their domain classification. An empty class has no outcome error.
func prxRestoreOutcomeErr(class string) error {
	switch class {
	case "":
		return nil
	case string(errclass.ClassTimeout):
		return errors.Wrap(NewTransactionTimedOutError())
	default:
		return errclass.Classify(
			errors.Prefix(errRestoredPrxAttempt, "class %q", class),
			errclass.Class(class),
		)
	}
}

// bindAttemptDelivery resolves one target delivery reference for an
// attempt send. A restored request answers only from its explicit
// restored bindings: a missing flow is an explicit failure, never a
// resolver call or an implicit dial. Ordinary requests delegate to the
// configured delivery resolver.
func (rq *ProxyRequest) bindAttemptDelivery(
	ctx context.Context,
	ref ProxyDeliveryRef,
) (ProxyBoundDelivery, error) {
	if rq.deliveries != nil {
		bound, ok := rq.deliveries[ref]
		if !ok {
			return ProxyBoundDelivery{}, errors.PrefixWrap(
				errNoDeliveryResolver,
				"restored kind %q key %q",
				ref.Kind,
				ref.Key,
			)
		}
		return bound, nil
	}
	return errors.Wrap2(rq.prx.bindDelivery(ctx, ref))
}

// prxAttemptErrClass records an attempt's local failure as a stable
// string classification: snapshots never carry arbitrary error objects.
func prxAttemptErrClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errclass.IsCanceled(err):
		return string(errclass.ClassCanceled)
	case errclass.IsTimeout(err):
		return string(errclass.ClassTimeout)
	case errclass.IsClosed(err):
		return string(errclass.ClassClosed)
	case IsTransportError(err):
		return string(ErrClassTransport)
	case IsMessageError(err):
		return string(ErrClassMessage)
	case IsTransactionError(err):
		return string(ErrClassTransaction)
	case IsElementError(err):
		return string(ErrClassElement)
	default:
		return "error"
	}
}
