package sip

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/sip/header"
)

// ForwardMode selects how the proxy forwards requests claimed by a route.
type ForwardMode uint

const (
	// ForwardModeStateful forwards the request through an upstream server
	// transaction and downstream client transaction attempts.
	// It is the zero/default forwarding mode.
	ForwardModeStateful ForwardMode = iota
	// ForwardModeStateless forwards the request without transactions.
	// Stateless routes require an explicit [ProxyOptions.StatelessKey].
	ForwardModeStateless
)

// ForwardModeFromString converts a mode name to [ForwardMode].
// Unknown names resolve to the stateful default.
func ForwardModeFromString(s string) ForwardMode {
	switch util.ToLower(s) {
	case "stateless":
		return ForwardModeStateless
	case "stateful":
		fallthrough
	default:
		return ForwardModeStateful
	}
}

func (m ForwardMode) String() string {
	switch m {
	case ForwardModeStateless:
		return "stateless"
	case ForwardModeStateful:
		fallthrough
	default:
		return "stateful"
	}
}

func (m ForwardMode) supported() bool {
	switch m {
	case ForwardModeStateful, ForwardModeStateless:
		return true
	default:
		return false
	}
}

// ProxyRoute represents a routing rule for the proxy.
// A route is an immutable value owned by the proxy after [NewProxy] returns.
type ProxyRoute struct {
	// Name is the route identifier reported in request metadata, events, and
	// snapshots. Route names must be unique within one proxy.
	Name string
	// ForwardMode selects stateful or stateless forwarding.
	// The zero value is [ForwardModeStateful].
	ForwardMode ForwardMode
	// MatchRequest reports whether the route claims the inbound request.
	// A nil matcher accepts every request at the route's position; the first
	// matching route wins.
	MatchRequest func(context.Context, *RequestEnvelope) bool
	// RecordRoute requests insertion of a Record-Route header field value into
	// forwarded requests.
	RecordRoute bool
	// ServerTransactionOptions are passed unchanged to the upstream server
	// transaction created for a stateful request.
	ServerTransactionOptions ServerTransactionOptions
	// ClientTransactionOptions configure downstream client transaction attempts.
	ClientTransactionOptions ClientTransactionOptions

	// Hook fields below.

	// ResolveRequestTargets resolves the location target plan for the request.
	// A nil resolver relies on the RFC predetermined target and otherwise
	// produces the documented no-contact rejection.
	ResolveRequestTargets func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error)
	// AuthorizeRequest authorizes the inbound request.
	// A nil authorizer allows forwarding; returning [RequestRejectedError]
	// produces the carried SIP rejection.
	AuthorizeRequest func(context.Context, *RequestEnvelope) error
	// PreprocessRoute inspects or rewrites routing information on an owned
	// working copy before the proxy consumes a local Route header field
	// value. A nil hook leaves the standard routing preprocess unchanged.
	// It cannot bypass local-identity, security, or loop rules.
	PreprocessRoute func(context.Context, *RequestEnvelope) error
	// AdjustOutboundRequest customizes one outbound branch copy before final
	// invariant checks and Via insertion.
	AdjustOutboundRequest func(context.Context, *RequestEnvelope, *RequestEnvelope, ProxyTarget) error
	// SelectTargets optionally chooses the unstarted target IDs to attempt
	// next. A nil selector uses the ordered plan groups. Returned IDs must
	// name unstarted targets exactly once. An empty result waits while
	// attempts are active and declines the remaining targets otherwise.
	SelectTargets func(context.Context, ProxySelection) ([]string, error)
}

func (r *ProxyRoute) LogValue() slog.Value {
	if r == nil {
		return slog.Value{}
	}
	return slog.StringValue(r.Name)
}

func (r *ProxyRoute) validate() error {
	if !r.ForwardMode.supported() {
		return errors.ErrorfWrap("invalid proxy route %q forward mode %d", r.Name, r.ForwardMode)
	}
	if b := r.ServerTransactionOptions.NonInviteServerTransactionBehavior; !b.supported() {
		return errors.ErrorfWrap(
			"invalid proxy route %q non-INVITE server transaction behavior %d",
			r.Name, b,
		)
	}
	return nil
}

// ProxyOptions configures a [Proxy].
// The proxy owns an independent copy of all values after [NewProxy] returns.
type ProxyOptions struct {
	// LocalIdentities are the advertised SIP URIs owned by this proxy.
	// They are used for self-route recognition and Record-Route generation.
	LocalIdentities []*URI
	// ServedDomains are the domains the proxy's location service is
	// responsible for. Requests addressed to a served domain consult the route
	// target resolver instead of forwarding to the Request-URI.
	ServedDomains []string
	// TimerC bounds the lifetime of each proxied INVITE attempt.
	// Zero selects a five minute default; positive values must be greater
	// than three minutes.
	TimerC time.Duration
	// StatelessKey is the application-supplied stable secret/namespace required
	// for explicit stateless routes. [NewProxy] rejects a stateless route
	// configured without it.
	StatelessKey []byte
	// DetectLoops enables optional loop detection.
	DetectLoops bool
	// RecordRouteURI optionally supplies the advertised Record-Route URI for a
	// forwarded request. Nil selects a configured local identity.
	RecordRouteURI func(context.Context, *RequestEnvelope, *RequestEnvelope) (*URI, error)
	// ResolveDelivery binds a custom delivery reference to an existing
	// transport resource. Nil accepts only ordinary next-hop delivery.
	ResolveDelivery func(context.Context, ProxyDeliveryRef) (ProxyBoundDelivery, error)
	// ResolveStatelessDestination optionally resolves an application-guaranteed
	// stable stateless destination for a target.
	//
	// Stateless forwarding commits to exactly one destination — a final
	// remote address and a bound client transport — selected before the
	// first wire send. A literal-IP next hop uses the built-in deterministic
	// selection and never consults this hook; a hostname or otherwise custom
	// next hop is reachable statelessly only through it. The returned
	// delivery must be stable across retransmissions of the request —
	// ordinary mutable or weighted DNS ordering is not a determinism
	// guarantee — and a send failure never retries an alternate destination.
	// A nil hook, a hook error, or an unusable bound delivery means the
	// invariants cannot be established: a response-eligible request is then
	// promoted to stateful forwarding before the first send, while an ACK or
	// an unmatched CANCEL stays on its mandatory stateless path and fails
	// routing instead.
	ResolveStatelessDestination func(context.Context, *RequestEnvelope, ProxyTarget) (ProxyBoundDelivery, error)
}

const (
	// defPrxTimeC is the Timer C duration used when [ProxyOptions.TimerC]
	// is zero.
	defPrxTimeC = 5 * time.Minute
	// minPrxTimeC is the exclusive lower bound for an explicit Timer C.
	// RFC 3261 Section 16.6 requires Timer C to be larger than 3 minutes.
	minPrxTimeC = 3 * time.Minute
)

func (o ProxyOptions) validate() error {
	for _, u := range o.LocalIdentities {
		if u == nil {
			return errors.ErrorWrap("nil proxy local identity URI")
		}
	}
	if o.TimerC < 0 || (o.TimerC > 0 && o.TimerC <= minPrxTimeC) {
		return errors.ErrorfWrap("invalid proxy timer C %v", o.TimerC)
	}
	return nil
}

func (o ProxyOptions) timeC() time.Duration {
	if o.TimerC == 0 {
		return defPrxTimeC
	}
	return o.TimerC
}

// clone returns a copy of the options owning independent slices and URIs.
func (o ProxyOptions) clone() ProxyOptions {
	o.LocalIdentities = slices.Clone(o.LocalIdentities)
	for i, u := range o.LocalIdentities {
		if u != nil {
			o.LocalIdentities[i] = u.Clone().(*URI) //nolint:forcetypeassert // the concrete type is fixed by construction
		}
	}
	o.ServedDomains = slices.Clone(o.ServedDomains)
	o.StatelessKey = slices.Clone(o.StatelessKey)
	return o
}

// ProxyTargetPlan is an owned, ordered, eagerly resolved location-target plan.
// Each group is attempted before the next group is considered.
type ProxyTargetPlan struct {
	// Groups are the ordered logical-target groups.
	Groups [][]ProxyTarget `json:"groups"`
}

// hasTargets reports whether the plan holds at least one logical target.
func (p ProxyTargetPlan) hasTargets() bool {
	for _, group := range p.Groups {
		if len(group) > 0 {
			return true
		}
	}
	return false
}

// ProxyTarget is a single logical forwarding target.
// A target has an identity independent of its URI so distinct bindings may
// share a contact URI.
type ProxyTarget struct {
	// ID is the target identifier unique within the plan.
	// An empty ID is replaced with a deterministic "group.target" position ID.
	ID string `json:"id"`
	// URI is the target contact URI.
	URI *URI `json:"uri"`
	// Routes is the per-target route set applied to the outbound copy.
	Routes []header.RouteHop `json:"routes,omitempty"`
	// Delivery references an application-defined delivery binding.
	// The zero value means normal next-hop resolution.
	Delivery ProxyDeliveryRef `json:"delivery,omitzero"`
	// Attributes carry extension state as string values.
	Attributes map[string]string `json:"attributes,omitempty"`
}

// ProxyDeliveryRef is a stable reference to an application-defined delivery
// binding. The zero value means normal next-hop resolution.
type ProxyDeliveryRef struct {
	// Kind classifies the referenced delivery resource.
	Kind string `json:"kind"`
	// Key identifies the resource within its kind.
	Key string `json:"key"`
}

// ProxyBoundDelivery is a resolved delivery binding whose sender is already
// bound to the required delivery semantics.
type ProxyBoundDelivery struct {
	// Transport is the bound client transport.
	Transport ClientTransport
	// LocalAddr is the bound local address.
	LocalAddr netip.AddrPort
	// RemoteAddr is the bound remote address.
	RemoteAddr netip.AddrPort
}

// ProxyBranchOutcome describes the settled result of one downstream attempt.
type ProxyBranchOutcome struct {
	// TargetID is the logical target of the attempt.
	TargetID string
	// AttemptKey is the client transaction key of the attempt.
	AttemptKey ClientTransactionKey
	// Response is the optional final downstream response.
	Response *ResponseEnvelope
	// Error is the local error classification when the attempt failed locally.
	Error error
	// Completed reports whether the attempt finished.
	Completed bool
	// Cancelled reports whether the attempt was cancelled.
	Cancelled bool
}

// ProxySelection is a read-only view of a request's remaining target selection
// state passed to the route selection hook.
type ProxySelection struct {
	// Request is the inbound request being forwarded.
	Request *RequestEnvelope
	// RemainingGroups holds the unstarted targets of the earliest still-open
	// plan group — at most one group, since groups progress strictly in
	// order. Later groups stay hidden until every earlier target resolves, so
	// a selection hook must name only the offered IDs; any other result is
	// rejected as unknown.
	RemainingGroups [][]ProxyTarget
	// ActiveTargetIDs are the identifiers of started unfinished targets.
	ActiveTargetIDs []string
	// CompletedOutcomes are the settled attempt outcomes.
	CompletedOutcomes []ProxyBranchOutcome
}

// ProxyRequestOutcome classifies the committed result of a proxied request.
type ProxyRequestOutcome int

const (
	// ProxyOutcomeResponse indicates a final response decision was committed.
	ProxyOutcomeResponse ProxyRequestOutcome = iota
	// ProxyOutcomeRejected indicates a local rejection.
	ProxyOutcomeRejected
	// ProxyOutcomeTimeout indicates the request timed out without a usable response.
	ProxyOutcomeTimeout
	// ProxyOutcomeCancelled indicates the request was cancelled.
	ProxyOutcomeCancelled
	// ProxyOutcomeSendFailure indicates every attempt failed to send.
	ProxyOutcomeSendFailure
	// ProxyOutcomeAbandoned indicates the request was abandoned before completion.
	ProxyOutcomeAbandoned
	// ProxyOutcomeSent indicates a successful stateless send without knowledge
	// of the downstream SIP outcome.
	ProxyOutcomeSent
)

func (o ProxyRequestOutcome) String() string {
	switch o {
	case ProxyOutcomeResponse:
		return "response"
	case ProxyOutcomeRejected:
		return "rejected"
	case ProxyOutcomeTimeout:
		return "timeout"
	case ProxyOutcomeCancelled:
		return "cancelled"
	case ProxyOutcomeSendFailure:
		return "send_failure"
	case ProxyOutcomeAbandoned:
		return "abandoned"
	case ProxyOutcomeSent:
		return "sent"
	default:
		return fmt.Sprintf("unknown(%d)", int(o))
	}
}

// clonePrxTargetPlan returns an owned copy of the resolved target plan.
//
// Targets without an explicit ID receive a deterministic 1-based
// "group-index.target-index" identifier. Duplicate final IDs, including an
// explicit ID colliding with a generated one, are rejected. Distinct targets
// are never deduplicated by URI.
func clonePrxTargetPlan(plan ProxyTargetPlan) (ProxyTargetPlan, error) {
	var cloned ProxyTargetPlan
	if plan.Groups != nil {
		cloned.Groups = make([][]ProxyTarget, len(plan.Groups))
	}

	ids := make(map[string]struct{})
	for gi, group := range plan.Groups {
		if group == nil {
			continue
		}

		cGroup := make([]ProxyTarget, len(group))
		for ti, trgt := range group {
			ctrgt := trgt
			if ctrgt.URI != nil {
				ctrgt.URI = ctrgt.URI.Clone().(*URI) //nolint:forcetypeassert // the concrete type is fixed by construction
			}
			if trgt.Routes != nil {
				ctrgt.Routes = make([]header.RouteHop, len(trgt.Routes))
				for i, hop := range trgt.Routes {
					ctrgt.Routes[i] = hop.Clone()
				}
			}
			ctrgt.Attributes = maps.Clone(trgt.Attributes)

			if ctrgt.ID == "" {
				ctrgt.ID = fmt.Sprintf("%d.%d", gi+1, ti+1)
			}
			if _, dup := ids[ctrgt.ID]; dup {
				return ProxyTargetPlan{}, errors.ErrorfWrap("duplicate proxy target ID %q", ctrgt.ID)
			}
			ids[ctrgt.ID] = struct{}{}

			cGroup[ti] = ctrgt
		}
		cloned.Groups[gi] = cGroup
	}

	return cloned, nil
}

const (
	errUnknownPrxTargetID   errors.Error = "unknown proxy target ID"
	errDuplicatePrxTargetID errors.Error = "duplicate proxy target ID"
	errActivePrxTargetID    errors.Error = "proxy target already active"
	errFinishedPrxTargetID  errors.Error = "proxy target already finished"
	errNoDeliveryResolver   errors.Error = "no proxy delivery resolver"
)

// Proxy request snapshot errors.
const (
	// ErrProxySnapshotUnsupported reports a [ProxyRequestSnapshot] capture
	// the request cannot support: an upstream transaction without the
	// [ServerTransactionSnapshotter] contract, a non-INVITE transaction
	// without the [NonInviteServerTransactionBehaviorProvider] contract, or an opaque
	// metadata value that cannot be serialized.
	ErrProxySnapshotUnsupported errors.Error = "proxy request snapshot unsupported"
	// errPrxSnapshotUnstable reports a capture attempted outside a
	// committed ownership boundary — before the plan is committed or while
	// a client-transaction admission is pending — where the branch decision
	// is still partial.
	errPrxSnapshotUnstable errors.Error = "unstable proxy request state"
)

// NewProxySnapshotUnsupportedError creates an [ErrProxySnapshotUnsupported]
// error classified as a proxy error.
func NewProxySnapshotUnsupportedError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrProxySnapshotUnsupported, args...),
		ErrClassProxy,
	)
}

// authorizeReq runs the route's authorization hook.
//
// A nil authorizer allows forwarding. The hook reports a SIP rejection with
// [NewRequestRejectedError]; any other error fails the request without a
// suggested response.
func (*Proxy) authorizeReq(ctx context.Context, route *ProxyRoute, req *RequestEnvelope) error {
	if route == nil || route.AuthorizeRequest == nil {
		return nil
	}
	return errors.Wrap(route.AuthorizeRequest(ctx, req))
}

// resolveTargets resolves the forwarding target plan for the request.
//
// RFC 3261 Section 16.5 predetermined-target rules run before the route
// resolver: a Request-URI "maddr" parameter or a Request-URI domain the
// proxy is not responsible for yields the Request-URI as the only target.
// Without a predetermined target, a nil resolver or a resolved plan with no
// targets produces the documented no-contact rejection. Every resolved plan
// is normalized by [clonePrxTargetPlan], so the stored plan is independent
// of the resolver's returned value.
func (prx *Proxy) resolveTargets(
	ctx context.Context,
	route *ProxyRoute,
	req *RequestEnvelope,
) (ProxyTargetPlan, error) {
	var (
		plan     ProxyTargetPlan
		resolved bool
	)
	req.WithMessage(func(r *Request) {
		ru, ok := r.URI.(*URI)
		if !ok {
			return
		}

		// If the Request-URI of the request contains an maddr parameter, the
		// Request-URI MUST be placed into the target set as the only target
		// URI, and the proxy MUST proceed to Section 16.6.
		if addr, ok := ru.MAddr(); ok && addr.IsValid() {
			//nolint:forcetypeassert // the concrete type is fixed by construction
			plan = ProxyTargetPlan{Groups: [][]ProxyTarget{
				{{URI: ru.Clone().(*URI)}},
			}}
			resolved = true
			return
		}

		// If the domain of the Request-URI indicates a domain this element is
		// not responsible for, the Request-URI MUST be placed into the target
		// set as the only target, and the element MUST proceed to the task of
		// Request Forwarding (Section 16.6).
		if !prx.isResponsibleAddr(ru.Addr) {
			//nolint:forcetypeassert // the concrete type is fixed by construction
			plan = ProxyTargetPlan{Groups: [][]ProxyTarget{
				{{URI: ru.Clone().(*URI)}},
			}}
			resolved = true
		}
	})

	if !resolved {
		if route == nil || route.ResolveRequestTargets == nil {
			// If the target set remains empty after applying all of the above, the
			// proxy MUST return an error response, which SHOULD be the 480
			// (Temporarily Unavailable) response.
			return ProxyTargetPlan{}, errors.Wrap(NewRequestRejectedError(
				errNoFwdTargets,
				slog.LevelDebug,
				ResponseStatusTemporarilyUnavailable,
			))
		}

		var err error
		plan, err = route.ResolveRequestTargets(ctx, req)
		if err != nil {
			return ProxyTargetPlan{}, errors.Wrap(err)
		}
	}

	cloned, err := clonePrxTargetPlan(plan)
	if err != nil {
		return ProxyTargetPlan{}, errors.Wrap(err)
	}

	if !cloned.hasTargets() {
		// If the target set remains empty after applying all of the above, the
		// proxy MUST return an error response, which SHOULD be the 480
		// (Temporarily Unavailable) response.
		return ProxyTargetPlan{}, errors.Wrap(NewRequestRejectedError(
			errNoFwdTargets,
			slog.LevelDebug,
			ResponseStatusTemporarilyUnavailable,
		))
	}

	return cloned, nil
}

// selectTargets runs the route's target-selection hook and validates its
// result against the unstarted targets.
//
// A nil selector uses the ordered plan groups: the first remaining group is
// the next selection. A configured hook must return unstarted target IDs
// with no duplicates; unknown, already-started, finished, and repeated IDs
// are rejected, so a hook cannot resurrect a finished target. An empty
// result is legal: with active targets it means wait for their progress,
// without them it declines the remaining targets and completes selection.
// The caller distinguishes the two cases through
// [ProxySelection.ActiveTargetIDs].
func (*Proxy) selectTargets(
	ctx context.Context,
	route *ProxyRoute,
	selection ProxySelection,
) ([]string, error) {
	if route == nil || route.SelectTargets == nil {
		var ids []string
		if len(selection.RemainingGroups) > 0 {
			for _, trgt := range selection.RemainingGroups[0] {
				ids = append(ids, trgt.ID)
			}
		}
		return ids, nil
	}

	ids, err := route.SelectTargets(ctx, selection)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	unstarted := make(map[string]struct{})
	for _, group := range selection.RemainingGroups {
		for _, trgt := range group {
			unstarted[trgt.ID] = struct{}{}
		}
	}
	active := make(map[string]struct{}, len(selection.ActiveTargetIDs))
	for _, id := range selection.ActiveTargetIDs {
		active[id] = struct{}{}
	}
	finished := make(map[string]struct{}, len(selection.CompletedOutcomes))
	for _, oc := range selection.CompletedOutcomes {
		finished[oc.TargetID] = struct{}{}
	}

	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := active[id]; ok {
			return nil, errors.PrefixWrap(errActivePrxTargetID, "ID %q", id)
		}
		if _, ok := finished[id]; ok {
			return nil, errors.PrefixWrap(errFinishedPrxTargetID, "ID %q", id)
		}
		if _, ok := unstarted[id]; !ok {
			return nil, errors.PrefixWrap(errUnknownPrxTargetID, "ID %q", id)
		}
		if _, dup := seen[id]; dup {
			return nil, errors.PrefixWrap(errDuplicatePrxTargetID, "ID %q", id)
		}
		seen[id] = struct{}{}
	}

	return ids, nil
}

// bindDelivery resolves a target delivery reference to a bound transport.
//
// The zero [ProxyDeliveryRef] selects ordinary next-hop resolution and needs
// no resolver. Without a configured [ProxyOptions.ResolveDelivery], a
// nonzero reference is an explicit failure: no ordinary send is attempted
// for an unresolvable custom binding.
func (prx *Proxy) bindDelivery(ctx context.Context, ref ProxyDeliveryRef) (ProxyBoundDelivery, error) {
	if ref == (ProxyDeliveryRef{}) {
		return ProxyBoundDelivery{}, nil
	}

	if prx.opts.ResolveDelivery == nil {
		return ProxyBoundDelivery{}, errors.PrefixWrap(
			errNoDeliveryResolver,
			"kind %q key %q", ref.Kind, ref.Key,
		)
	}

	return errors.Wrap2(prx.opts.ResolveDelivery(ctx, ref))
}
