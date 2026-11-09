# Design

## Context

The library is in active development and has no established external user base. Design the new proxy API directly rather than preserve the unfinished proxy surface.

See `proposal.md` for motivation and scope. The three delta specs define externally observable behavior; this document chooses implementation boundaries and public contracts.

Observed integration constraints:

- `sip/proxy.go` currently mixes route configuration with target channels/iterators and a mutable forward context. Its response path and branch generator are stubs; the stateless send is absent.
- `Element` installs the transaction manager ahead of application middleware. Matching responses are consumed there, so stateful proxy response handling must subscribe to transaction events rather than rely on an inbound response interceptor.
- `SendRequestStateful` waits for an attempt's final outcome. `ProduceRequestAttempts` and `RequestAttempt.DoStateful` instead expose address attempts and initial transaction start, suitable for independent branch progress.
- Transaction admission handlers run before Start and are the place to install response, state, and error subscriptions. Callbacks can be synchronous/reentrant.
- INVITE transactions already implement RFC 6026 Accepted states and timers L/M. Non-INVITE server transactions currently have only Timer J and no delayed Trying timer.
- `EnsureRequestMaxForwards` currently replaces an existing zero, and generic outbound response preparation supplies missing To tags. Forwarded messages require a distinct preparation path so these endpoint conveniences cannot corrupt proxy transformations.
- `AcquireConnOptions.Dial=false` supports existing-connection lookup, but an address lookup is not an identity for a particular transport-flow generation.

## Goals / Non-Goals

**Goals:** Keep the proxy as an `Element` middleware and transaction user; make ordinary forwarding usable without optional callbacks; use concrete immutable values for configuration and routing data; expose small optional function hooks only at application-policy boundaries; make owned state and shutdown explicit.

**Non-Goals:** Do not create a second SIP transaction engine, universal plugin framework, dialog/B2BUA layer, storage backend, or complete Outbound implementation. Recovery does not include restoring sockets or an atomic cross-component persistence transaction.

## Decisions

### 1. Shared routing with separate execution paths

Keep `NewProxy(elm *Element, routes []ProxyRoute, opts ...ProxyOptions) (*Proxy, error)` and `Proxy` middleware ownership. Redesign the data and callback fields rather than retaining an adapter around the incomplete forward-context model.

Use stateful as the zero/default forwarding mode; stateless is explicit. Reject invalid configured modes and conflicting named routes at construction. A nil match function matches all requests at that route's position; first match wins. Nil authorization allows forwarding. Nil target resolution uses the RFC predetermined target when available and otherwise yields the documented no-contact outcome. Server/client transaction options become ordinary value options with zero defaults instead of mandatory callbacks.

Stateful forwarding and RFC 4320 behavior are separate choices. Add `ServerTransactionOptions.NonInviteServerTransactionBehavior` with zero-value `NonInviteServerTransactionBehaviorLegacy` and explicit `NonInviteServerTransactionBehaviorRFC4320`. Legacy retains existing low-level behavior. `ProxyRoute.ServerTransactionOptions` is passed through unchanged; the proxy never enables RFC 4320 implicitly, including for matching CANCEL. There is no second proxy-level automatic enablement flag. INVITE/client transaction behavior is unaffected.

Separate served domains from self-routing identities. `ProxyOptions` carries `ServedDomains`, `LocalIdentities`, optional `RecordRouteURI` selection, and the timing/stateless/delivery hooks below. Responsible-domain lookup does not infer authority from every host interface. Self-route recognition checks the configured identity and proxy-generated URI information, not merely that Request-URI names a local IP.

Record-Route is disabled unless requested by the route or required by protocol. When needed, use a configured advertised identity suitable for the selected direction/transport; `RecordRouteURI(ctx, inReq, outReq)` can supply a deployment-specific URI. Reject an unusable configuration instead of choosing `LocalAddrs[0]`. Preserve strict/loose routing, maddr rules, SIPS constraints, and the original incoming request for transaction matching and loop fingerprints.

Alternative rejected: one forwarding loop controlled by a stateful boolean. Stateless ownership, deterministic delivery, and response processing are materially different.

### 2. Concrete target plan and focused policy hooks

Use these public data contracts (field/type spellings are the planned API, not implementation code):

| Contract | Shape and meaning |
| --- | --- |
| `ProxyTargetPlan` | `Groups [][]ProxyTarget`; an owned, ordered, eagerly resolved location-target plan |
| `ProxyTarget` | `ID string`, `URI *URI`, `Routes []header.RouteHop`, `Delivery ProxyDeliveryRef`, `Attributes map[string]string` |
| `ProxyDeliveryRef` | `Kind string`, `Key string`; zero value means normal next-hop resolution; nonzero is an application-defined stable binding reference |
| `ProxyBoundDelivery` | `Transport ClientTransport`, `LocalAddr netip.AddrPort`, `RemoteAddr netip.AddrPort`; the sender is already bound to the required delivery semantics |
| `ProxySelection` | Read-only copies of the request, remaining target groups, active target IDs, and completed `ProxyBranchOutcome` values |
| `ProxyBranchOutcome` | Target ID, attempt key, optional final response, local error classification, and completion/cancellation state |

Application hooks are function fields, not mandatory multi-method implementations:

- `ProxyRoute.ResolveRequestTargets(ctx, req) (ProxyTargetPlan, error)` resolves logical contacts once. Preserve RFC predetermined-target processing before consulting it.
- `ProxyRoute.AuthorizeRequest(ctx, req) error` uses the existing `NewRequestRejectedError` contract for SIP rejection; authentication middleware can implement application policy. ACK/CANCEL exceptions remain core rules.
- `ProxyRoute.AdjustOutboundRequest(ctx, inReq, outReq, target) error` customizes a branch before final invariant checks and Via insertion.
- `ProxyRoute.SelectTargets(ctx, selection) ([]string, error)` optionally chooses unstarted target IDs. Nil uses ordered groups. Unknown, duplicate, or already-started IDs are errors. An empty selection while branches remain active means wait; with no active branches it declines remaining targets and completes selection.
- `ProxyOptions.ResolveDelivery(ctx, ref) (ProxyBoundDelivery, error)` binds custom delivery references. Nil accepts only ordinary delivery.

Clone plans, URI/header values, maps, and callback views at ownership boundaries. Reject duplicate explicit target IDs; assign deterministic plan-position IDs when omitted. Do not deduplicate distinct targets by URI alone. Attribute values are strings so extension state has predictable snapshot encoding. They are not automatically interpreted as Outbound registration data.

Treat pre-send stateless-to-stateful promotion as continuation of the same inbound operation. Invoke authorization, route preprocessing, and logical target resolution at most once per operation; transfer the validated working request and owned plan to stateful admission while retaining the original request for transaction matching. Do not repeat local-route consumption or discard preprocessing output. Per-target adjustment, selection cycles, and address/delivery attempts retain their existing scopes.

Core protocol guards always override policy: no new branches after accepted cancellation, a forwarded final response, or received 6xx; no weakened security or invalid response generation. A selection hook cannot resurrect a finished target. This supports future instance-aware branch replacement without making the core interpret reg-id now.

Alternative rejected: resumable arbitrary iterators/channels and opaque `any` target state. They complicate cancellation, ownership, and serialization without improving the normal case.

### 3. Per-request coordinator and branch/attempt distinction

A private coordinator owns one upstream server transaction, the immutable plan, target progress, active client attempts, accumulated responses, forwarded-final state, cancellation intent, and Timer C deadlines. A logical target can have several sequential address attempts; each attempt has a distinct client transaction/branch. Keep target completion separate from transaction retention through timers D/K/L/M.

Serialize request decisions through a per-request event queue with one drain owner. Transaction callbacks only enqueue detached event data; they must not wait for proxy work or perform nested transaction calls while transaction internals are locked. Transitioning an idle queue to nonempty schedules a short-lived asynchronous drain owner; do not execute a drain with transaction effects inline in the enqueueing callback. Compute transitions under the coordinator guard, execute transport/transaction and application effects outside it, and feed completion events back through the queue. Publish stable snapshots outside mutation locks, after their state transition is committed. Do not add a permanently running goroutine per idle request or a global serialization lock.

Install association and event subscriptions at transaction admission before Start. Use a private context ownership token to identify proxy-created transactions in `HandleClientTransaction`/`HandleServerTransaction`; do not correlate by an application-controlled route name alone. Register the request context before starting branches so an immediate response cannot outrun association. Preserve duplicate-admission behavior and route transaction-local ACK/retransmissions through the manager.

Default scheduling starts every selected member of the current group without waiting for another member's final response. Progress to the next group when current logical targets resolve and continuation remains legal. DNS failover belongs to each target's attempt runner; it does not advance the location-service group. The default negative continuation policy tries remaining groups; success, cancellation, and global failure close scheduling.

Alternative rejected: holding a request mutex across SendRequest/Start or waiting in `SendRequestStateful`. Both obstruct parallelism and risk reentrant deadlocks.

### 4. Sending and message preparation boundaries

For ordinary delivery, consume `ProduceRequestAttempts` lazily and start attempts through `DoStateful`; handle subsequent responses/timeouts/503 retry decisions in the proxy-owned attempt runner. Reuse existing lookup and request-attempt error classification instead of copying the whole high-level sender. Never collect a lazy DNS sequence merely to discover its fallback list.

For custom delivery, bind `ProxyDeliveryRef` once for the selected attempt, apply its explicit transport and local/remote addresses, then use the existing transaction factory/registration/start APIs with that bound `ClientTransport`. The binding's sender must preserve element observation/preparation semantics while enforcing the chosen resource. A failed binding has no implicit DNS or dial fallback. Rebind resources explicitly during recovery.

Prepare a cloned request in protocol order: target URI cleanup, hop count, per-target Route additions, optional application adjustment, strict-routing rewrite, secure next-hop selection, advertised Record-Route, and Via/Content-Length. Validate hook output before network effects. Resolve any transport-dependent identity after selecting the transport and before its send; retain the original request separately.

Preserve the incoming SIPS requirement independently of the mutable target URI throughout target resolution, Route/Request-URI rewrites, and outbound adjustment. A SIP contact or custom binding must not erase it. Validate the final request and selected transport against that requirement before sending; reject unsupported secure delivery without silently downgrading the URI or transport.

Make `EnsureRequestMaxForwards` fill only an absent header. Introduce a private forwarded-message marker/preparation distinction for endpoint-only defaults, notably To-tag insertion; it is set by the core, stripped/rebuilt at clone/restore boundaries, and is not a wire header. Forwarding a 1xx/2xx never manufactures a dialog tag or changes a body. Locally generated responses still use endpoint response construction.

### 5. Stateful response, cancellation, and timers

Feed response events from client transactions to their coordinator. Clone before removing the top Via. A response with no remaining Via belongs to the locally generated operation (for example a CANCEL) and is not an upstream response to the original request.

Use RFC 3261 Section 16.7 as amended by RFC 4320/6026: forward eligible INVITE 101-199 and all eligible INVITE 2xx; retain state after first success; send at most one final for non-INVITE. Do not immediately forward 6xx: close scheduling, cancel pending attempts, and preserve the race in which another branch returns INVITE 2xx. Choose a negative final only after eligible attempts settle under RFC rules; do not wait for a completed transaction's cleanup timer merely to decide that its logical target has a final result.

Separate logical settlement from response-forwarding eligibility. A settled attempt, completed target, or committed request outcome must not discard a newly received eligible INVITE 2xx while its owned client transaction and original usable upstream server transaction remain available. This includes exact retransmissions and additional successes on the same client transaction. Forwarding such a response does not reopen selection, append another logical branch outcome, restart Timer C, or repeat request completion or initial-send observations; each successful new upstream response send remains observable. Preserve abandonment, ownership, no-remaining-Via, and upstream-lifetime guards. Existing RFC 6026 transaction FSMs already deliver and accept these responses; do not replace their lifetime handling with proxy-level message-content deduplication.

Independent proxy guards always suppress downstream non-INVITE non-100 provisional responses and 408, require the original usable upstream transaction, and retain RFC 6026 protections, regardless of the server transaction behavior option. This setting is not a switch that disables response-context correctness.

Default negative choice prefers 6xx, otherwise the lowest response class, with 401/407/415/420/484 preferred within 4xx and deterministic arrival-order ties. Aggregate all 401/407 challenges if choosing an authentication response. Do not recurse on 3xx; preserve its Contacts. Map a lone downstream 503 to 500 by default. Handle transport failures as 503 outcomes and exhausted/no-response attempts as timeout outcomes; non-INVITE suppression remains an explicit final-send guard.

Send upstream through the held, original server transaction. Do not use `Element.SendResponseStateful` to recover an absent transaction; it can create a replacement. If that transaction is absent/unusable, discard the response. Failure of a response write does not itself cancel unrelated pending branches.

Process matching CANCEL before ordinary location resolution: create/use its own non-INVITE server transaction, answer 200, mark cancellation intent, stop new targets, and cancel eligible pending attempts. Use the existing CANCEL transaction builder only after a provisional has arrived; retain deferred intent otherwise. A CANCEL without a response context follows Section 16.10 stateless forwarding, not an automatic 481. Do not challenge CANCEL or respond to ACK. ACK for non-2xx stays within INVITE transactions; end-to-end ACK is routed and sent without creating an ACK transaction.

`ProxyOptions.TimerC` defaults to five minutes and uses the default for zero and rejects negative values or positive values at or below three minutes. Initial arming and 101-199 refresh use this duration, independent of T1. On expiry use terminate-on-expiry as the default policy: cancel an attempt that has a provisional response, otherwise settle it as a timeout. Keep Timer C state until the associated operation is safely settled. Transaction cleanup timers remain transaction-owned.

Alternative rejected: first-final-response-wins or blanket cancellation on any I/O error. Those lose valid forked successes and conflate upstream failure with downstream failure.

### 6. Opt-in RFC 4320 server transaction behavior

Use the built-in `NonInviteServerTransactionBehavior` option rather than arbitrary callbacks that patch the FSM. `NonInviteServerTransactionBehaviorLegacy` is zero and preserves existing validation, sending, and timer behavior. `NonInviteServerTransactionBehaviorRFC4320` explicitly enables the new response policy and automatic delayed Trying. Validate unknown mode values for non-INVITE creation and freeze the mode before Start; do not change it on an active transaction. Legacy does not roll back existing RFC 6026 fixes.

The proxy passes its route's `ServerTransactionOptions` unchanged to server transaction creation/factories, including matching CANCEL, and does not force this option on. Custom factories are responsible for honoring the selected behavior contract. The ordinary forwarding default remains stateful but the RFC 4320 option is disabled by default at both levels. Applications can explicitly enable it without replacing the proxy or its transaction engine.

Only in RFC4320 mode, reject non-100 1xx, too-early unreliable-transport 100, and non-INVITE 408 before FSM transitions, without sending or changing state. In Legacy mode do not introduce these new low-level rejection rules or the automatic Trying timer. Proxy response filtering remains independent and enforced in either mode.

For enabled mode add a dedicated delayed-Trying timer. Accumulate effective Timer E intervals until backoff first reaches T2 (default 0.5 + 1 + 2 seconds), not INVITE Time100 or T2 alone. Preserve effective T1/T2 defaulting; if T1 already reaches/exceeds T2, use an immediate eligible deadline, and reject overflow/incoherent derived timing explicitly. Arm at Start only if no response has been sent. A valid initial response stops it; retransmissions still reuse cached responses. Serialize timer dispatch and final-response sends so no 100 can follow a final response. Reliable transports permit an explicit earlier 100 and otherwise use the same automatic deadline. Client Timer F never restarts because of this feature or receipt of 100.

Extend `ServerTransactionSnapshot` with the selected `NonInviteServerTransactionBehavior` and the optional pending Trying timer. A missing behavior field in an older snapshot means Legacy and requires no new Trying deadline; restore it without implicit enablement or replay. An RFC4320 snapshot in an active unanswered state must retain the exact pending deadline; a missing required deadline is a malformed enabled-mode snapshot, not grounds to guess a fresh delay. Already-responded or terminal snapshots do not need that pending timer. Restore preserves the saved mode even when current creation options differ; logging/runtime configuration cannot implicitly upgrade the saved protocol behavior.

Keep the actual saved non-INVITE server behavior in the proxy checkpoint relationship and validate supplied transaction bindings against it. Current route creation defaults do not override the mode of a compatible saved request. Changing the mode for newly created transactions does not change active or restored transactions.

This is a local compatibility setting, not a SIP option tag or peer-negotiated feature. Document that full RFC 4320 behavior requires explicit opt-in or an application layer providing equivalent guarantees; default-off is not a full-conformance claim. RFC 4321 remains analysis and regression guidance. Do not invent a peer deadline/server Timer F, reset client lifetime, or add cross-request blacklisting. A proxy request finishing without a permitted final response still retires its owned upstream transaction silently.

Alternatives rejected: unconditional activation changes legacy observable behavior; a generic FSM-injection callback exposes timer/lifecycle races; implementing duplicate timing exclusively in proxy leaves other transaction users without a reusable opt-in mode.

### 7. Stateless determinism and response-path ownership

`ProxyOptions.StatelessKey` is an application-supplied stable secret/namespace required for explicit stateless routes. Use a keyed SHA-256-derived token with a valid SIP branch encoding, a distinguishable stateless/stateful ownership marker, and sufficient uniqueness. Do not use a random branch or random per-send Record-Route value in stateless forwarding. Normalize related CANCEL/non-routed ACK inputs as required; tests cover magic-cookie and legacy inputs, including To-tag differences. A proxy with only stateful routes may generate an instance-local fallback key for the mandatory unmatched-CANCEL path; that key does not make it eligible for an explicitly configured stateless route.

Apply related-method normalization to every component of the emitted stateless Via branch, including the embedded routing fingerprint, not only its keyed token. A response-added To tag must not change the branch of a non-routed ACK retaining the associated INVITE's upstream transaction identity. An end-to-end ACK with a different upstream transaction identity is not required to reuse that branch. Keep stateful fingerprint semantics intact and use consistent stateless normalization in loop comparisons so true loops and changed-route spirals remain distinguishable. Maintain committed forwarding regressions for both magic-cookie and legacy inputs, comparing complete emitted Via branches and destinations for INVITE, retransmission, CANCEL, and non-routed ACK with a response-added To tag. Include no-transaction and loop/spiral assertions; helper-token equality alone is insufficient.

A genuine stateless operation accepts only one logical target and one final bound destination. A plan containing multiple targets, whether in one group or across groups, requires ordinary response-eligible requests to be promoted before any downstream send; preserve the complete owned plan and its group order. ACK and unmatched CANCEL cannot be promoted and must fail routing without sending when the plan cannot be forwarded safely as one target. Do not silently truncate the plan, deduplicate distinct bindings, or send to several targets statelessly.

Stateless destination choice must cover the final address and transport, not only a Contact URI. Literal-IP ordinary destinations can use built-in deterministic selection. An optional `ResolveStatelessDestination(ctx, req, target) (ProxyBoundDelivery, error)` supports application-guaranteed stable hostname/custom resolution. Ordinary mutable or weighted DNS ordering is not a determinism guarantee: promote ordinary response-eligible requests, excluding unmatched CANCEL, to stateful handling before the first send when invariants cannot be established. Configuration changes require application-managed overlap/quiescence or stateful handling during the transaction window. ACK is never promoted into an ACK transaction; impossible safe forwarding returns an error without a SIP response.

This change does not implement automatic post-send stateless-to-stateful conversion. Once a request has been emitted statelessly, a send failure is reported without trying an alternate destination; retaining correct response ownership for an ambiguously delivered earlier attempt would otherwise require additional state. Promotion happens before the first wire send. Unmatched CANCEL remains on the stateless path and uses the configured deterministic resolver; inability to route it safely is a routing failure, not an invented 481 or a new independent response context. A stateless path emits no 100 and has no per-request context. Related-method destination stability is part of the resolver contract, not something an arbitrary user callback can be assumed to satisfy.

Establish proxy-local Via ownership before using a branch fingerprint as evidence of a loop. Shared branch-format markers alone do not prove ownership. A Via emitted by an independent proxy is not a local-loop witness, even when routing fingerprints match. For an owned Via, unchanged routing detects a loop and changed routing remains an allowed spiral.

Stateful responses arrive through transaction handlers. The inbound response interceptor handles only explicitly recognized stateless ownership, validates top Via sent-by, removes one Via, and routes by the next Via. A stateful marker with no live transaction is discarded, including late 2xx. Unrelated messages pass to the next receiver so a proxy does not steal another middleware's traffic. Malformed/foreign responses that belong to this proxy's claimed path are discarded according to the RFC rules.

Alternative rejected: treating every response with a local Via as stateless. That defeats the RFC 4320/6026 late-response protections when both modes share an element.

### 8. Snapshot/restore is explicit composition, not a store

Expose `ProxyRequestSnapshot` as a versioned value and `ProxyRequest.Snapshot() (*ProxyRequestSnapshot, error)` as the stable capture operation. `Proxy.BindSnapshotHandler` publishes future per-request revisions and returns idempotent unbind; it does not replay old publications. Provide request lookup by upstream key so applications can capture an active request directly.

Snapshot data contains the route name/configuration identity, original request, post-preprocess working request, owned target plan and attributes, target progress, each attempt's key and target association, collected candidate responses, forwarded-final decision, cancellation/deferred-CANCEL intent, and absolute Timer C deadlines. Snapshot-owned application extension data is string-valued; required delivery information belongs in explicit serializable references. Message metadata follows existing envelope clone rules for ordinary forwarding, but snapshot encoding rejects unsupported opaque values rather than silently dropping required state. Record the effective non-INVITE server behavior so supplied checkpoint bindings can be checked without applying current creation defaults. Preserve committed observation decisions and sequence progress as described in Section 11, without storing observer callbacks or recorder counters.

Capture `OriginalRequest` and `WorkingRequest *RequestEnvelope` (JSON field `working_request`) as independent owned values. The original remains the upstream transaction-matching request. The working value preserves the validated output of `PreprocessRoute` followed by core local-route consumption, before per-target preparation; it is the source for later groups and address failover. Clone and validate serializable metadata on both values at snapshot boundaries. Restore adopts the saved working value without invoking `PreprocessRoute` or consuming local routing a second time; reconstructing it from the original cannot recover arbitrary application hook output.

Keep orchestration snapshot version 1 and extend its current layout in place: there are no consumers requiring compatibility with the earlier incomplete layout. Require a valid `WorkingRequest` on every request snapshot; a missing value fails restoration with `ErrInvalidProxySnapshot` before installing subscriptions, indexes, or timers. Do not add migration, guess a working value, or replay hooks to accept incomplete v1 records. This decision does not alter transaction snapshot formats or the older non-INVITE transaction snapshot default-to-Legacy rules in Section 6.

Validate the saved `WorkingRequest`'s message structure, including required headers, Request-URI, and SIP protocol, and its method compatibility with `OriginalRequest` before claiming supplied transactions or installing subscriptions, indexes, or timers. Check metadata serializability on both saved request envelopes at restore as well as capture. Missing or malformed working state returns `ErrInvalidProxySnapshot`; unsupported opaque metadata returns `ErrProxySnapshotUnsupported`. Validation must not replay `PreprocessRoute`, consume local routing, reconstruct the working request, or produce network effects.

Do not duplicate transaction snapshots inside the proxy snapshot. The application captures transaction snapshots using the existing snapshotter APIs and persists a consistent set with the orchestration revision. Document the ordering/causality requirement: independently captured latest values are not automatically an atomic checkpoint. Track consumed transaction observations and reject an unavailable/unstable recovery capture rather than publishing a partial branch-start decision. Custom transactions lacking required snapshot observations still forward normally but cannot produce a recoverable capture.

`Proxy.RestoreRequest(ctx, snapshot, bindings) (*ProxyRequest, error)` accepts explicit `ProxyRestoreBindings` containing the required dormant server/client transaction objects and resolved custom delivery resources. Validate versions, route identity, target uniqueness, key/request relationships, final/cancellation state compatibility, incompatible behavior selections, and deadlines before installing anything. Terminal historical attempts need stored outcomes, not live transaction bindings. Fail rather than fabricate missing transactions or claim arbitrary checkpoint consistency.

Install the coordinator, subscriptions, and indexes without starting supplied transactions. `ProxyRequest.Start(ctx)` explicitly activates a restored request and its supplied dormant transactions after all associations exist; activation ownership transfers to the request handle. Enforce once-only activation and rollback only registrations/resources created by the failed restore. Preserve expired deadlines and do not replay completed target decisions or initial network sends. Normal transaction retransmissions after activation remain legal.

Live flow handles never enter snapshots. Restore uses the application's binding resolver; an absent exact flow is an explicit failure, never an instruction to dial a replacement. An old address tuple alone cannot establish that a flow survived restart.

Alternative rejected: automatic `ForwardContextStore.LoadAll` on construction, or promising exactly-once network effects from snapshots. Both hide application-owned consistency and transport-liveness decisions.

Alternatives rejected for working-state recovery: rerunning application preprocessing can change routing or repeat side effects; replaying only core routing loses hook output; replacing the original with the working request breaks upstream binding validation. A new orchestration version or compatibility adapter adds no value before there are consumers of the current v1 layout.

### 9. Outbound-ready seams without premature Outbound support

The target/delivery split and selection hook are sufficient extension seams for a later RFC 5626 change: an Outbound resolver can retain AOR/instance/reg-id identity and Path-derived route vectors; a selector can admit only one contact per instance and replace a 430-failed branch; a bound sender can enforce the existing logical flow. An edge-proxy route hook can inspect a local Route before the core consumes it; supply this as `ProxyRoute.PreprocessRoute(ctx, req) error`, invoked on an owned working copy between validation and local Route removal, then revalidate routing-relevant output.

This hook does not implement token parsing or bypass local-identity, security, or loop rules. The future Outbound module will own token integrity, incoming/outgoing flow classification, Path/ob and Record-Route behavior, 430/439 policy, invalidation, and keep-alives. Do not advertise `outbound` or add STUN/CRLF parsing in this change. Do not silently make a missing custom flow into ordinary destination lookup.

Alternative rejected: hard-coding TCP connection pointers or RFC 5626 registration fields throughout the proxy core. That couples routing to a particular transport and implements an incomplete extension under a misleading capability flag.

### 10. Lifecycle, file boundaries, and verification

Keep private implementation split by responsibility under `sip/`: routing/branch formatting, stateful coordination and attempt scheduling, stateless execution, response selection, and snapshots. Do not introduce a new public package hierarchy solely for this feature. Reuse existing error constructors/classification, transport metadata, callback helpers, and transaction timing machinery.

Retain the middleware unbind function; expose idempotent proxy close/shutdown behavior compatible with `Element` ownership. Seal admission, stop new scheduling, drain/terminate owned work, unbind handlers, and release indexes/timers. Never close borrowed transports, application storage, or unrelated transactions. Report operational errors through existing element/error-handler conventions without logging credentials or raw flow tokens.

Use external-package behavior tests in `<source>_test.go` and private-state tests in `<source>_internal_test.go`; extend shared stubs under the existing test conventions rather than duplicating a second transport harness. Use `testing/synctest` for timer and race schedules, `cmp.Diff` for structural assertions, and existing classification predicates for errors. Cover every spec scenario, including immediate callbacks, late responses, duplicate admission, shutdown, and dormant activation. Add loopback UDP/TCP coverage for actual serialized Via/Route/body behavior without external DNS/network services. Include event-order/reentrancy, multi-observer, counting, and restore-no-replay tests for proxy observations.

Run narrow SIP/transport test selections while implementing. Use a single final `task check` gate (tests with race/vet, configured lint/format checks, vulnerability checks); do not run Go verification merely to create these planning artifacts.

### 11. Observer events and proxy statistics

Keep observation separate from authorization, target selection, and request preparation. Add `ProxyEventHandler.HandleProxyEvent(ctx context.Context, event ProxyEvent)` without an error return, a `ProxyEventHandlerFunc` adapter, and `Proxy.BindEventHandler(handler ProxyEventHandler) (unbind func())`. Multiple independent observers can subscribe; subscriptions observe future events only. Unbind is idempotent and does not wait on an already captured/in-flight dispatch. No observer is required for forwarding. Reuse the existing callback manager rather than introduce an event-bus dependency or a background goroutine per observer.

`ProxyEvent` is a detached typed value with `Kind ProxyEventKind`, `Time time.Time`, `RequestID string`, `Sequence uint64`, `RouteName string`, `Mode ForwardMode`, `Method RequestMethod`, optional `TargetID`/`AttemptID` strings, `StatusCode int`, `Outcome ProxyRequestOutcome`, and an optional classified `Error error`. Do not expose mutable request/context/transaction objects or raw credentials/flow tokens. Request/route association and sequence are present only when actually known; standalone stateless response events must not invent them. Request IDs are correlation data, not automatic metric labels.

| Event kind | Observation boundary |
| --- | --- |
| `ProxyEventRequestHandled` | A matching route claims a new operation; unmatched traffic emits nothing. Stateful admission/retransmission deduplication is retained. |
| `ProxyEventForwardAttemptFinished` | A proxy-initiated delivery attempt's initial send operation settles, once per attempt, with success or a classified error. Pre-send delivery failures are failed attempts too. Automatic transaction retransmissions do not emit this event. |
| `ProxyEventRequestForwarded` | The first successful initial request send for a logical stateful request, or for the current stateless operation. Additional forks and retries do not repeat the stateful event. |
| `ProxyEventResponseForwarded` | A proxy-initiated upstream response send returns success. Dropped responses and failed writes emit no success event. |
| `ProxyEventRequestCompleted` | The proxy commits its request/operation outcome once, independently of retained transaction/context cleanup. Additional eligible INVITE 2xx can still generate response-forward events, not another completion. |

A successful send means the send operation returned nil, not peer receipt or SIP 2xx. Emit attempt success/failure only after its actual initial outcome is known, not merely on branch allocation or transaction registration. Preserve one observation across immediate/reentrant send callbacks. Use `ProxyRequestOutcome` values `Response`, `Rejected`, `Timeout`, `Cancelled`, `SendFailure`, `Abandoned`, and `Sent` (the last for successful stateless forwarding without knowledge of its downstream SIP outcome). Include a final status when known. Accepted cancellation alone is not completion: a racing INVITE success still determines the actual outcome. For stateful processing, completion is the committed final response decision or silent terminal outcome, not every branch result or eventual cleanup timer. Early rejection without a usable transaction identity is an individual operation, not a promise to deduplicate malformed traffic.

Dispatch committed detached observations outside coordinator, transaction, and callback-registry locks through the current request drain/effect boundary. Calls are synchronous to that dispatch boundary, not inline from locked transaction callbacks. Observers must be short and avoid blocking I/O; offloading is application-owned. Per-request sequence records committed dispatch order, not a universal phase order across immediate responses/send-return races. There is no global ordering between concurrent requests. An observer can inspect public state or unbind without deadlocking, and changing its event value cannot alter another observer or protocol processing. Observer return values cannot veto or rewrite routing. Shutdown detaches subscriptions and releases only proxy-owned resources.

Stateless observation uses only current-operation data and never adds a deduplication cache, a retained request context, or transactions. Retransmitted stateless input is another observable forwarding operation. ACK and unmatched CANCEL remain on their prescribed paths; observation must not allocate an ACK transaction or an independent CANCEL response context. A standalone stateless response can report available method/status/branch correlation but has no fabricated original request or route association.

`StatsRecorder` implements `ProxyEventHandler`; bind it explicitly with `proxy.BindEventHandler(recorder)` and retain the unbind function. Existing Element middleware binding continues to collect raw transport/transaction statistics independently. Add `StatsReport.Proxy ProxyStats` with aggregate cumulative `uint64` counters `RequestsHandled`, `RequestsForwarded`, `ForwardAttemptsSucceeded`, `ForwardAttemptsFailed`, `ResponsesForwarded`, `RequestsCompleted`, `RequestsRejected`, `RequestsTimedOut`, and `RequestsCancelled`. Increment the first six from their corresponding event kinds (attempts split by success/error); increment the final three only from completion outcomes Rejected, Timeout, and Cancelled. Use race-safe counters and no automatic per-request/target label maps. These are observed totals for the recorder's subscription lifetime, not active-request gauges or persistent lifetime totals. Three successful forks of one stateful request yield one handled request, one forwarded request, and three successful attempts, not three handled requests. A negative SIP final is not an initial-send failure.

Restore must not replay RequestHandled or historical send/completion observations. Keep the committed first-forward/completion decisions, observed initial attempt outcomes, and per-request sequence progress in the orchestration checkpoint where needed to suppress replay; do not serialize handlers, recorder counters, contexts, or arbitrary error objects. After activation emit only newly occurring observations, such as an eligible newly forwarded INVITE 2xx. Observations, wire effects, snapshots, and external metrics are not an atomic durable transaction: do not claim exactly-once metrics across crashes or automatic reconstruction of historical counters.

Alternative rejected: policy callbacks with side effects for counting, or a single undifferentiated sent-message counter. They couple observation to routing and cannot distinguish one forked request from its attempts and transport retransmissions.

## Risks / Trade-offs

- [Callbacks race with Start or shutdown] -> Admission-time association, nonblocking event ingestion, effects outside mutation locks, and deterministic reentrancy tests.
- [Non-INVITE fixed-lifetime races remain] -> Apply RFC 4320, preserve Timer F, and document RFC 4321 limitations instead of promising reliable delayed negative aggregation.
- [Reference-based recovery requires coordinated checkpoints] -> Explicit bindings, validation, stable revisions, and documentation that capture is not a distributed atomic transaction.
- [Logical settlement drops protocol-required INVITE successes] -> Keep response-forwarding eligibility separate from scheduling/completion and test repeated 2xx both before and after logical completion.
- [Stateless helper tokens hide changes in full branches] -> Compare complete emitted Via branches for related methods and retain loop/spiral regressions.
- [Recovery loses application preprocessing] -> Save the post-preprocess working request independently, require it in the current v1 layout, and test JSON round-trip continuation without hook replay or repeated local-route consumption.
- [Old snapshots lack a behavior field] -> Interpret absence as Legacy and keep saved behavior; reject only enabled-mode snapshots that lack required pending timer state.
- [Stateless DNS/configuration instability] -> Explicit deterministic resolver contract, stable key/identity, and stateful promotion rather than implicit random retries.
- [Future Outbound could expand the API] -> Keep serializable references and pre-consumption route/selection/delivery seams; add Outbound behavior only in a separately specified change.
- [Default-off mistaken for full RFC 4320 conformance] -> Document explicit enablement or equivalent application guarantees and show both default and enabled configurations.
- [Observation counts confused with delivery or durable totals] -> Define send-success, request/attempt scopes, stateless operation counting, and recovery boundaries explicitly; keep recorders independent of protocol decisions.

## References

- RFC 3261 Sections 16-18: <https://www.rfc-editor.org/rfc/rfc3261>
- RFC 4320 Section 4: <https://datatracker.ietf.org/doc/html/rfc4320#section-4>
- RFC 4321 Sections 1.1-1.6: <https://datatracker.ietf.org/doc/html/rfc4321#section-1>
- RFC 6026 Sections 7.3 and 8: <https://www.rfc-editor.org/rfc/rfc6026>
- RFC 5626 Sections 5 and 7 (extension constraints only): <https://datatracker.ietf.org/doc/html/rfc5626>
