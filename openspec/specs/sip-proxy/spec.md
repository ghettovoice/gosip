# sip-proxy Specification

## Purpose

Provide an embeddable SIP proxy that routes requests and responses correctly while applications retain control of routing policy, delivery, and deployment.

## Requirements

### Requirement: Middleware integration and useful defaults

The library SHALL offer proxy middleware in `sip/proxy`, constructed without an element or automatic attachment and explicitly registered through the element middleware API. Stateful forwarding SHALL remain the default. Unmatched messages SHALL continue to the next receiver. Optional callbacks SHALL have documented defaults rather than causing nil-callback panics.

#### Scenario: Minimal stateful forwarding

- **WHEN** an application configures a matching route and reachable target without optional callbacks and explicitly registers the proxy on an element
- **THEN** the proxy forwards the request statefully and returns downstream responses upstream.

#### Scenario: Unmatched request

- **WHEN** no configured route matches an inbound request
- **THEN** the proxy passes the request to the next receiver without creating proxy-owned transactions.

#### Scenario: Construction does not attach

- **WHEN** an application constructs a valid proxy without registering it
- **THEN** construction requires no element and installs no middleware, transactions, timers, or transport bindings.

#### Scenario: Invalid configuration has no attachment effects

- **WHEN** proxy construction rejects invalid routes or options
- **THEN** no element or middleware registration has been modified.

### Requirement: Server transaction behavior is explicit

The proxy SHALL pass application-selected server transaction behavior unchanged and SHALL NOT implicitly enable RFC 4320. Default configuration SHALL leave that option disabled. Documentation SHALL distinguish optional transaction behavior from independent proxy guards and full RFC 4320 conformance.

#### Scenario: Default proxy leaves the option disabled

- **WHEN** a stateful non-INVITE request uses default server transaction options
- **THEN** the proxy does not turn on RFC 4320 or introduce its automatic Trying timer.

#### Scenario: Application explicitly enables the option

- **WHEN** a route explicitly selects RFC 4320 server transaction behavior
- **THEN** the proxy passes that selection to its non-INVITE server transaction creation without overriding it.

#### Scenario: Default is not a full conformance claim

- **WHEN** documentation describes the default-off configuration
- **THEN** it states that complete RFC 4320 behavior requires explicit enablement or equivalent application-layer guarantees.

### Requirement: Proxy request validation

The proxy SHALL apply RFC 3261 Section 16.3 validation without rejecting unknown methods or unrelated extension headers. Invalid routing-relevant syntax, exhausted Max-Forwards, and unsupported Proxy-Require options SHALL produce the applicable protocol outcome, subject to ACK and CANCEL response rules.

#### Scenario: Unsupported required extension

- **WHEN** a response-eligible request requires an unsupported proxy option
- **THEN** the proxy returns 420 with all unsupported option tags in Unsupported.

#### Scenario: Unknown method and extension header

- **WHEN** an otherwise valid request uses an unknown method and an unrelated extension header
- **THEN** the proxy forwards it without removing that header merely because it is unknown.

#### Scenario: Exhausted hop count

- **WHEN** a non-ACK request arrives with Max-Forwards zero
- **THEN** it is not forwarded and receives 483, except that OPTIONS can be answered locally under the documented policy.

### Requirement: Route preprocessing and target resolution

The proxy SHALL implement strict-router recovery, local topmost Route removal, and local maddr processing under RFC 3261 Sections 16.4-16.5. It SHALL distinguish owned routing identities from domains served by a location service and preserve non-local routes.

#### Scenario: Strict-router recovery

- **WHEN** the Request-URI is a recognizable URI previously supplied by this proxy and a final Route value holds the original target
- **THEN** the proxy restores that target to Request-URI and removes only the consumed Route value.

#### Scenario: Local domain without contacts

- **WHEN** location resolution for a served domain returns an empty target plan
- **THEN** the proxy returns 480 for a response-eligible request without blocking on target enumeration.

### Requirement: Per-target request transformation

Forwarding SHALL clone the inbound request, apply the target URI and route set, perform strict/loose next-hop processing, decrement an existing Max-Forwards exactly once, and add the proxy Via. Unrelated headers and bodies SHALL be preserved. A missing Max-Forwards SHALL be supplied without resetting a legitimate outgoing zero.

#### Scenario: Last permitted hop

- **WHEN** an accepted request has Max-Forwards one
- **THEN** its outbound copy has Max-Forwards zero and downstream sending helpers do not replace zero with a default.

#### Scenario: Strict next hop

- **WHEN** the first outbound Route lacks lr
- **THEN** the proxy moves that URI to Request-URI and appends the prior Request-URI to the end of Route.

#### Scenario: Independent fork copies

- **WHEN** a request is forwarded to two targets
- **THEN** target-specific changes do not mutate the inbound request, another branch, or application-owned configuration.

### Requirement: Secure routing and advertised identity

The proxy SHALL honor SIPS transport requirements and RFC 3261 Record-Route rules. Advertised identities SHALL be chosen for the applicable direction and transport rather than arbitrarily selecting a host interface. Unsupported secure delivery SHALL fail without silently downgrading security.

#### Scenario: Record-Route for a secure route

- **WHEN** forwarding requires a SIPS Record-Route value
- **THEN** the inserted value is a reachable configured identity with lr and the required secure scheme.

#### Scenario: SIPS survives contact resolution

- **WHEN** a SIPS request resolves to a SIP contact
- **THEN** the proxy neither emits a downgraded SIP Request-URI nor selects insecure transport; if secure forwarding cannot be established, routing fails before any downstream send.

#### Scenario: Application adjustment cannot weaken SIPS

- **WHEN** outbound adjustment or a custom delivery binding selects an insecure URI or transport for an incoming SIPS request
- **THEN** the proxy rejects that insecure forwarding before any downstream send rather than accepting the downgrade.

### Requirement: Stateful request and attempt ownership

Each new stateful request SHALL have one upstream server transaction and associated downstream attempt transactions. Retransmissions SHALL reuse existing transaction handling without repeating authorization or target resolution. Delivery attempts SHALL be correlated before responses can be processed.

#### Scenario: Immediate response during send

- **WHEN** a downstream response arrives before the initial send operation returns
- **THEN** it is attributed to the correct request and branch and processed without loss or deadlock.

#### Scenario: Concurrent duplicate requests

- **WHEN** retransmissions race with admission of the original request
- **THEN** only one proxy request is admitted and existing transaction handling absorbs the duplicates.

### Requirement: Parallel and sequential forking

Stateful forwarding SHALL support ordered groups of logical targets, concurrent progress within a group, and sequential continuation between groups. Each network retry SHALL be distinguished from selecting another logical target. A target SHALL NOT be repeated solely because its group is revisited.

#### Scenario: Parallel group followed by fallback

- **WHEN** the first group contains A and B and the next group contains C
- **THEN** A and B start without waiting for one another's final responses, and C starts only when continuation is permitted after the first group resolves.

#### Scenario: Cancellation or global failure stops scheduling

- **WHEN** cancellation is accepted, an INVITE success is forwarded, or a 6xx is received
- **THEN** no new target branches are started for that request.

### Requirement: Immediate and aggregated responses

The proxy SHALL apply RFC 3261 Section 16.7 with RFC 6026 updates. It SHALL forward eligible INVITE provisional responses other than 100, forward INVITE 2xx including additional and repeated successes, and forward at most one final response for a non-INVITE request. Non-INVITE response suppression SHALL remain enforced independently of the optional transaction behavior.

#### Scenario: Multiple INVITE successes

- **WHEN** distinct branches return 200 and a successful response is retransmitted while the required transactions remain available
- **THEN** every such INVITE 2xx is forwarded, including a success racing with cancellation.

#### Scenario: Success after logical completion

- **WHEN** an eligible INVITE 2xx arrives on a retained owned client transaction after its attempt or request has logically completed, while the original upstream transaction remains usable
- **THEN** the response is forwarded without reopening scheduling or repeating branch outcomes, initial-forward observations, or request completion.

#### Scenario: Global failure races with success

- **WHEN** one branch returns 6xx while another remains pending
- **THEN** the proxy stops new branches and initiates cancellation of pending branches without immediately forwarding the 6xx ahead of a possible INVITE 2xx.

#### Scenario: Disabled option does not weaken proxy response guards

- **WHEN** RFC 4320 transaction behavior is disabled and a downstream non-INVITE response is 180 or 408
- **THEN** the proxy does not forward that response and still enforces original-upstream-transaction and late-response rules.

### Requirement: Best negative response and authentication aggregation

When final response selection is required, the proxy SHALL enforce mandatory RFC 3261 Section 16.7 constraints and authentication-header aggregation. The default selection SHALL preserve current response preferences. The proxy SHALL preserve forwarded dialog tags, retain the existing lone-503-to-500 transformation, and use 408 only where the method permits it.

#### Scenario: Combined authentication challenges

- **WHEN** a selected 401 or 407 follows other 401 and 407 responses in the same response context
- **THEN** its WWW-Authenticate and Proxy-Authenticate values include the challenges from those responses without rewriting them.

#### Scenario: Only service unavailable

- **WHEN** the only usable downstream final result is 503 and no policy establishes general proxy unavailability
- **THEN** the proxy returns 500 rather than forwarding 503 upstream.

#### Scenario: INVITE attempts produce no final response

- **WHEN** all INVITE attempts finish without a forwardable final response
- **THEN** the proxy produces 408 through the existing upstream transaction.

#### Scenario: Custom selection retains authentication processing

- **WHEN** an application selects a 407 from a response context containing both 401 and 407 challenges
- **THEN** the proxy aggregates the authentication challenges before forwarding without requiring the callback to modify responses.

#### Scenario: Custom selection does not bypass lone-503 handling

- **WHEN** a custom callback selects the only candidate and that response is 503
- **THEN** the proxy applies the same 500 transformation as the default path without changing the stored 503 candidate.

### Requirement: CANCEL handling

A matching stateful CANCEL SHALL receive its own transaction response of 200 and cancel pending branches of the associated request without creating a separate response context. A CANCEL without a matching response context SHALL be forwarded statelessly under RFC 3261 Section 16.10, not automatically rejected with 481.

#### Scenario: CANCEL before a branch provisional response

- **WHEN** matching cancellation arrives before a downstream INVITE has received a provisional response
- **THEN** the proxy acknowledges the CANCEL upstream, prevents new target branches, and defers downstream CANCEL until it is permitted or the branch finishes.

#### Scenario: Unknown CANCEL

- **WHEN** no associated response context exists
- **THEN** the proxy applies the stateless CANCEL forwarding rules without authenticating it through a new challenge exchange.

### Requirement: ACK handling

The proxy SHALL leave non-2xx ACK handling to the matching INVITE transactions and route end-to-end ACK without creating an ACK transaction. It SHALL NOT send a SIP response to an ACK.

#### Scenario: ACK for a successful INVITE

- **WHEN** an end-to-end ACK follows the dialog route set
- **THEN** it is forwarded according to routing without creating a non-INVITE client transaction.

### Requirement: Timer C policy

Each proxied INVITE attempt SHALL have Timer C greater than three minutes initially and after 101-199 provisional responses. Timer C SHALL be independent of small test or deployment T1 values. Expiry SHALL follow RFC 3261 Section 16.8 with documented reset/cancellation policy.

#### Scenario: Trying does not refresh Timer C

- **WHEN** a branch receives 100 and later 180
- **THEN** 100 does not refresh Timer C, while 180 resets it to a value greater than three minutes.

#### Scenario: Expiry with and without a provisional response

- **WHEN** Timer C expires under the terminate-on-expiry policy
- **THEN** an attempt with a provisional response is cancelled as permitted, while an attempt without a provisional response is handled as a timeout result.

### Requirement: Transport failures

The proxy SHALL handle request transport errors as downstream 503 outcomes under RFC 3261 Section 16.9 while applying the applicable next-address retry rules. Failure to transmit an upstream response SHALL NOT by itself cancel all other branches.

#### Scenario: Upstream response write failure

- **WHEN** forwarding a response fails at the transport
- **THEN** the response failure is reported without turning it into global cancellation of the request.

### Requirement: Deterministic stateless forwarding

Stateless forwarding SHALL select exactly one destination, use retransmission-invariant transformations and branch values, and create no transaction or provisional response. Related CANCEL and non-routed ACK with the associated request's upstream transaction identity SHALL select its destination and complete Via branch. ACK with a different transaction identity need not reuse that branch. Unsafe alternate-destination retry SHALL require stateful promotion.

#### Scenario: Retransmission and related methods

- **WHEN** an INVITE, its retransmission, its CANCEL, and its non-routed ACK retaining the associated transaction identity traverse a stateless route with either magic-cookie or legacy upstream Via inputs, including a response-added To tag on that ACK
- **THEN** each input family preserves the same destination and complete emitted Via branch across its related methods without creating transactions or a per-request store.

#### Scenario: Routing configuration changes

- **WHEN** mutable resolution or a configuration change cannot preserve stateless routing invariants within the transaction window
- **THEN** affected requests use stateful handling or an explicit safe failure policy instead of silently changing the stateless destination.

#### Scenario: Multi-target plan on an ordinary request

- **WHEN** an ordinary response-eligible request on a stateless route resolves to multiple logical targets in one group or across groups
- **THEN** the proxy promotes it before the first downstream send, retains the complete plan, and applies stateful group scheduling instead of stateless fan-out or silent truncation.

#### Scenario: Multi-target plan on ACK or unmatched CANCEL

- **WHEN** an ACK or unmatched CANCEL resolves to multiple logical targets
- **THEN** routing fails without a downstream send or stateful promotion, and forwarding creates no ACK transaction or independent CANCEL response context.

### Requirement: Pre-send promotion preserves routing work

Pre-send promotion SHALL continue the same inbound operation and preserve its accepted routing result. It SHALL NOT repeat authorization, route preprocessing, local-route consumption, or logical target resolution. Promotion SHALL retain the complete target plan and application preprocessing changes without changing the original request's transaction identity.

#### Scenario: Promotion does not replay application hooks

- **WHEN** a request has passed authorization and route preprocessing and resolved its targets, but cannot establish stateless delivery
- **THEN** stateful promotion uses the accepted result without invoking those application hooks again within that inbound operation.

#### Scenario: Promotion preserves preprocessing and plan

- **WHEN** preprocessing rewrites the working request and core processing consumes local routing before promotion
- **THEN** stateful branches use the accepted working state and complete target plan without repeating local-route consumption or replacing the original transaction identity.

### Requirement: Response forwarding ownership

Stateless responses SHALL be forwarded only when their topmost Via belongs to this proxy, after removing exactly that Via. Unmatched responses from stateful forwarding SHALL be discarded under RFC 4320/6026, not mistaken for stateless traffic. Stateful forwarding SHALL also require the original usable upstream transaction.

#### Scenario: Late stateful response

- **WHEN** a stateful INVITE or non-INVITE response has no matching downstream transaction, or its upstream transaction is no longer usable
- **THEN** it is not forwarded and no replacement upstream transaction is created.

#### Scenario: Stateless response

- **WHEN** a response belongs to this proxy's stateless forwarding path and has a remaining Via
- **THEN** it is forwarded using that Via without changing its body or unrelated headers.

### Requirement: Focused application extension points

Applications SHALL be able to supply target resolution, route preparation, target-selection policy, and delivery binding without replacing protocol processing. Targets SHALL have identity independent of URI, per-target route sets, and serializable delivery references. These seams SHALL NOT advertise RFC 5626 support by themselves.

#### Scenario: Distinct bindings share a URI

- **WHEN** two application targets have distinct identities or delivery references but equal Contact URIs
- **THEN** the proxy preserves the distinct bindings rather than unconditionally deduplicating by URI.

#### Scenario: Inspect a local route before consumption

- **WHEN** an application route-preprocessing hook handles a local topmost Route carrying extension information
- **THEN** it can inspect that value before core removal, while the core still validates the resulting routing and security invariants.

#### Scenario: Bound delivery is unavailable

- **WHEN** a custom delivery reference cannot bind to its required existing transport resource
- **THEN** delivery fails explicitly without silently substituting DNS resolution or opening a new connection.

### Requirement: Optional loop detection

Configured loop detection SHALL distinguish a true loop from a changed-route spiral rather than rejecting every request whose Via names this proxy. Only a Via owned by this proxy SHALL be used as a local-loop witness; a shared branch-format marker alone SHALL NOT prove ownership.

#### Scenario: Spiral is not a loop

- **WHEN** a request returns through this proxy with routing-relevant information changed
- **THEN** loop detection does not reject it merely because a prior Via names this proxy.

#### Scenario: Foreign proxy Via is not a local loop

- **WHEN** loop detection is enabled and a valid request first traverses this proxy with a Via from an independent proxy, sharing the branch format and routing fingerprint but not proxy identity
- **THEN** the proxy continues normal routing rather than rejecting that foreign Via as a local loop.

#### Scenario: Owned Via with unchanged routing

- **WHEN** loop detection is enabled and a request returns with a Via owned by this proxy and unchanged routing-relevant information, including when foreign Via entries precede the owned Via
- **THEN** the proxy recognizes the true loop under the applicable method response rules.

### Requirement: Safe middleware lifecycle

Explicit middleware detachment SHALL stop new deliveries through that registration. Proxy shutdown and close SHALL seal instance-wide admission and release owned timers, callbacks, indexes, and subscriptions without closing unrelated application resources. Applications SHALL retire active proxy work before removing its required interception bindings; unbinding SHALL NOT implicitly close the proxy. Repeated shutdown and close SHALL be safe.

#### Scenario: Shutdown with active requests

- **WHEN** the owning element shuts down while branches and callbacks are active
- **THEN** proxy-owned work is drained or terminated without deadlock, leaked callbacks, or closure of unrelated transports.

#### Scenario: Explicit detachment after retirement

- **WHEN** an application closes the proxy and invokes the registration's unbind function
- **THEN** new requests are not claimed, cleanup is idempotent, and unrelated middleware remains registered.

#### Scenario: Shared instance has one lifecycle

- **WHEN** a proxy used by multiple elements is shut down directly or through an owning element's middleware lifecycle
- **THEN** that proxy instance stops admission on every element and retires each request using its own processing element.

### Requirement: Optional proxy observation subscriptions

Applications SHALL be able to subscribe multiple typed, observation-only handlers and unbind them idempotently. Subscriptions SHALL receive only future events outside internal locks, without mutable protocol state or routing vetoes. Forwarding SHALL require no observer, and shutdown SHALL detach subscriptions. Every handler invocation SHALL carry the processing element in its context; event bodies SHALL NOT duplicate the element reference.

#### Scenario: Independent observers

- **WHEN** statistics and audit observers subscribe to the same proxy
- **THEN** both receive detached observations without changing routing, and unbinding one does not disable the other.

#### Scenario: Reentrant observer

- **WHEN** an observer inspects public state or unbinds itself during dispatch
- **THEN** it does not deadlock, while already captured or in-flight delivery can finish.

#### Scenario: Asynchronous event carries its processing element

- **WHEN** a request emits an event from admission, a transaction callback, the coordinator drain, a timer, or cleanup
- **THEN** the handler retrieves that request's element from context even if the triggering callback had no element or a different element in its context.

#### Scenario: Event-kind naming preserves compatibility

- **WHEN** an application consumes proxy observations through `proxy.EventKind`
- **THEN** the public constants use the `EventKind` prefix while retaining
  their existing numeric values and string representations.

### Requirement: Forward observations distinguish requests and attempts

The proxy SHALL expose request handling, initial attempt outcomes, first successful request forwarding, successful upstream response forwarding, and completion. Success SHALL mean a successful send return, not peer receipt or SIP acceptance. Attempt observations SHALL identify their target/attempt when known and SHALL NOT repeat for automatic transaction retransmissions.

#### Scenario: Successful send precedes a negative SIP result

- **WHEN** the initial downstream send succeeds and the peer subsequently returns 403
- **THEN** the attempt remains a send success and the final SIP status is observed separately.

#### Scenario: Failed send is not a successful forward

- **WHEN** an initial delivery attempt fails before or during its send
- **THEN** observers receive a failed attempt, not a successful request-forward event for that attempt.

#### Scenario: Immediate response observations during send

- **WHEN** a response callback runs before the initial send returns
- **THEN** attempt observations are based on the actual send outcome and are emitted once without requiring a fixed phase order across response and send-return observations.

### Requirement: Logical completion is observed once

A stateful request SHALL produce one completion observation when its final response decision or silent terminal outcome is committed. Retained transaction cleanup and additional eligible INVITE 2xx SHALL NOT repeat completion. Observations SHALL distinguish local rejection, response outcome, timeout, cancellation, send failure, and abandonment.

#### Scenario: Multiple successes retain one request outcome

- **WHEN** additional eligible INVITE 2xx arrive after the first success commits the request outcome
- **THEN** their successful upstream sends remain observable but the request completion is not counted again.

#### Scenario: Cancellation races with success

- **WHEN** accepted cancellation races with a forwarded INVITE 2xx
- **THEN** cancellation intent alone does not prematurely count a cancelled completion instead of the actual successful response outcome.

### Requirement: Observation respects forwarding mode

Stateful admission SHALL avoid counting handled retransmissions as new logical requests. Stateless observation SHALL count current forwarding operations without retaining request history or promising retransmission deduplication. Unknown request/route associations on stateless responses SHALL remain absent rather than be fabricated.

#### Scenario: Stateful retransmission

- **WHEN** a retransmission is absorbed by an existing stateful transaction
- **THEN** it does not create another handled-request or initial-forward observation.

#### Scenario: Stateless retransmission

- **WHEN** the same request traverses stateless forwarding twice
- **THEN** both operations are observable without adding a request store or transaction for deduplication.

### Requirement: Event-derived proxy statistics

The statistics API SHALL reside in `sip/stats` with a zero-value `stats.Recorder` and `stats.Report`. Explicit proxy-event subscription SHALL report cumulative handled/forwarded requests, successful/failed attempts, forwarded responses, completions, local rejections, timeouts, and cancellations separately from transport and transaction statistics. Concurrent updates and report reads SHALL remain safe; totals SHALL describe observations, not guaranteed peer delivery or persistent history.

#### Scenario: Three successful forks

- **WHEN** one stateful request is admitted and its three target attempts each send successfully
- **THEN** proxy statistics increase handled requests by one, forwarded requests by one, and successful attempts by three without duplicating request-level counts.

#### Scenario: Explicit recorder attachment

- **WHEN** an application binds its statistics recorder to proxy events alongside its existing Element middleware binding
- **THEN** the report includes proxy counters while existing transport and transaction statistics retain their own meanings.

#### Scenario: Package migration preserves reports

- **WHEN** an application uses the relocated recorder and report types
- **THEN** report field names, JSON representations, counter meanings, and explicit subscription behavior remain unchanged.

### Requirement: Processing element isolation

Every claimed processing operation SHALL use the concrete element supplied by its inbound context. A shared proxy SHALL isolate request lookup, duplicate admission, CANCEL/ACK association, rejection-response correlation, transport selection, and fallback stateless identity by element. A claimed operation with no processing element SHALL fail inspectably before installing state or sending traffic.

#### Scenario: Equal transaction keys on different elements

- **WHEN** two elements concurrently process requests with equal upstream transaction keys through the same proxy
- **THEN** each request has its own coordinator and transactions, and neither request is treated as the other's retransmission or CANCEL target.

#### Scenario: Equal rejected requests on different elements

- **WHEN** equal-key requests are rejected on two elements and their answering sends interleave
- **THEN** rejection-send correlation and observer context remain associated with the correct element.

#### Scenario: Missing processing element

- **WHEN** a matching request enters the proxy directly without an element in context
- **THEN** processing fails with an inspectable error and does not attach state to a previously used element.

### Requirement: Unified serializable proxy metadata

The proxy SHALL expose one serializable metadata value under `sip.proxy` containing known request, related-request, target, and attempt IDs, route name, effective forwarding mode, and processing stage. Typed reads SHALL support both library-stored values and their JSON-decoded representation. Reading or replacing one envelope's metadata SHALL NOT mutate another envelope or a published snapshot.

#### Scenario: Metadata survives JSON round trip

- **WHEN** a stamped envelope is serialized to JSON and decoded
- **THEN** typed access returns the same known correlation fields without requiring an application type-registration hook.

#### Scenario: Independent fork metadata

- **WHEN** two outbound copies belong to different targets and one copy's metadata is replaced
- **THEN** their shared request identity remains consistent and the sibling, source request, and previously published snapshots remain unchanged.

#### Scenario: Malformed metadata is not trusted

- **WHEN** typed access encounters an absent, wrong-typed, or invalid correlation value
- **THEN** it reports no usable metadata without panicking, fabricating identifiers, or changing protocol routing.

### Requirement: Message stages and attempt attribution

Stamped messages SHALL distinguish claimed inbound requests, downstream request copies, correlated inbound responses, and upstream responses. Outbound requests and stateful downstream responses SHALL identify the applicable target and physical attempt when known. Local or aggregated upstream responses SHALL omit any single-attempt attribution that is not known. Metadata SHALL NOT change SIP headers, bodies, or response-selection policy.

#### Scenario: Downstream response and upstream copy

- **WHEN** a stateful branch response is attributed and an owned copy is forwarded upstream
- **THEN** both envelopes carry the request identity and known attempt attribution, with distinct response and upstream stages.

#### Scenario: Address retry

- **WHEN** a logical target starts a new physical delivery attempt
- **THEN** the request and target identities remain stable while the attempt identity changes consistently with attempt observations.

#### Scenario: Local final response

- **WHEN** the proxy generates a local final response without a single downstream source
- **THEN** its upstream metadata identifies the request without inventing a winning target or attempt.

### Requirement: Correlation uses observation identity

Metadata, events, and snapshots for one operation SHALL use the same request identity. Pre-send promotion SHALL preserve that identity and change metadata to the effective mode without replaying handled observations. Transaction retransmissions SHALL NOT allocate new logical attempt identities. A distinct related ACK or CANCEL SHALL record a related request ID only when the proxy knows the relationship.

#### Scenario: Stateless-to-stateful promotion

- **WHEN** accepted stateless routing is promoted before sending
- **THEN** inbound, working, and outgoing envelopes and subsequent events retain one request identity and describe the effective stateful mode.

#### Scenario: Matched CANCEL correlation

- **WHEN** a CANCEL is associated with a live request coordinator
- **THEN** its metadata links to that request without creating a separate response context or adding observations merely to populate metadata.

#### Scenario: Retransmission on a stateful transaction

- **WHEN** an existing transaction absorbs a retransmission
- **THEN** existing request and attempt correlation remains authoritative and no new logical work is counted.

### Requirement: Stateless correlation is honest and bounded

Stateless metadata SHALL describe the current operation without retaining request history. A response with no known original request, route, target, or attempt association SHALL leave those fields absent. Metadata SHALL NOT become a wire identifier, routing authority, or authentication mechanism, and SHALL NOT weaken existing Via ownership checks.

#### Scenario: Stateless response after a network round trip

- **WHEN** a stateless response arrives with owned Via routing but no recoverable request metadata
- **THEN** it carries only known mode and stage information, without a fabricated original request or attempt ID or a lookup store introduced for tracing.

#### Scenario: Stateless retransmissions

- **WHEN** a stateless request is processed again as a new inbound operation
- **THEN** its metadata can have a new operation identity while its protocol-required destination and complete Via branch remain retransmission-invariant.

### Requirement: Metadata provides optional structured logging

The metadata type SHALL provide a structured slog value with stable correlation field names and omitted unknown identifiers. Proxy-owned logs with known correlation SHALL attach that metadata as one structured value. This SHALL require neither new slog handlers nor changes to core message logging, and SHALL NOT log arbitrary application metadata or authentication material.

#### Scenario: Structured proxy log

- **WHEN** a proxy log record is emitted with known request or attempt correlation
- **THEN** an ordinary slog handler receives one grouped metadata value matching the envelope's correlation and stage.

#### Scenario: Partial metadata log

- **WHEN** metadata has no related request, target, or attempt association
- **THEN** its structured value omits those unknown identifiers instead of rendering misleading placeholders.

### Requirement: Route-specific final response resolution

Applications SHALL be able to configure an ordinary function field `Route.ResolveFinalResponse` without a named callback type or interface. It SHALL receive `context.Context` and `FinalResponseSelection`, containing the request and arrival-ordered candidates, and return an integer candidate index and an error. A nil function SHALL use the exported `DefaultFinalResponseResolver` with the same signature.

#### Scenario: Select a different negative response

- **WHEN** a route receives 480 followed by 486 and its callback returns index 1 without an error
- **THEN** the proxy selects the 486 candidate instead of the default first-arrival choice.

#### Scenario: Explicitly delegate to the default

- **WHEN** a custom callback delegates an unchanged selection to `DefaultFinalResponseResolver`
- **THEN** the resulting candidate and upstream response match the nil-callback path.

#### Scenario: Preserve response attribution for policy inspection

- **WHEN** a callback inspects a candidate that has known proxy target and attempt metadata
- **THEN** the selection input retains that attribution without inventing identity for an unknown source.

### Requirement: Reusable default response selection

The exported default resolver SHALL return an index into its original input without modifying it. It SHALL ignore nil and provisional entries, prefer success over 6xx, then 6xx over other classes, otherwise the lowest class, prefer 401/407/415/420/484 within 4xx, and retain input order on ties. With no selectable final response, it SHALL return index -1 and an error.

#### Scenario: Default preferences and ties

- **WHEN** default resolution receives 486 followed by 401
- **THEN** it returns index 1; for 480 followed by 486 it returns index 0.

#### Scenario: Index refers to the original slice

- **WHEN** the exported default resolver receives a nil entry, a 180, and a 404 in that order
- **THEN** it returns index 2 rather than an index into a filtered copy.

#### Scenario: Successful candidate remains strongest

- **WHEN** the exported default resolver receives 600 followed by 200
- **THEN** it returns index 1 without performing sends or response rewriting.

#### Scenario: No usable candidate

- **WHEN** the exported default resolver receives an empty input or only nil and provisional entries
- **THEN** it returns index -1 and a non-nil error without panicking.

### Requirement: Resolution does not replace protocol decisions

Custom resolution SHALL apply only to a settled stateful response context with negative final candidates and no successful candidate. It SHALL NOT control immediate response forwarding, target scheduling, cancellation, or local no-response outcomes. If any candidate is 6xx, a successful selection SHALL name a 6xx candidate. Other class and 4xx preferences SHALL be application policy.

#### Scenario: Deliberate departure from default preferences

- **WHEN** a custom callback selects 404 over 401, or 486 over 302, without a successful or 6xx candidate
- **THEN** the proxy accepts that selection as application policy rather than treating a recommendation-level preference as mandatory.

#### Scenario: Mandatory global-failure precedence

- **WHEN** a callback selects 486 from a context also containing 600
- **THEN** the proxy rejects the selection rather than forwarding 486.

#### Scenario: Success bypasses custom resolution

- **WHEN** a response context contains a successful response
- **THEN** the proxy preserves its existing success path without calling the custom resolver or replacing success with a negative candidate.

#### Scenario: No response and method-specific suppression

- **WHEN** attempts finish without a usable candidate, including a non-INVITE context whose only received final is 408
- **THEN** no custom resolver is called and existing timeout, cancellation, send-failure, and method-specific suppression rules remain effective.

#### Scenario: Stateless forwarding and stateful promotion

- **WHEN** a request remains stateless
- **THEN** its route callback is not called; if the request is promoted before sending, the resulting stateful request uses the same route's callback when selection is required.

### Requirement: Resolution input isolation and coordinator safety

The proxy SHALL call custom resolution outside internal locks, at most once for an eligible live decision, with the owning processing element in context. Input protocol messages, slices, and metadata containers SHALL be detached from live state; opaque application metadata values SHALL retain the existing envelope copy semantics. Callback results SHALL NOT overwrite a superseding success or terminal decision.

#### Scenario: Reentrant inspection

- **WHEN** a callback looks up its request or attempts to capture a snapshot
- **THEN** it does not deadlock; capture during an uncommitted resolution reports an unstable snapshot rather than a partial terminal decision.

#### Scenario: Callback changes its detached inputs

- **WHEN** a callback changes a candidate's SIP status or headers, the request's protocol fields, or metadata container entries
- **THEN** those changes do not alter the proxy-owned request, candidates, or published snapshots, and the returned index still refers to the originally offered order.

#### Scenario: Late success during resolution

- **WHEN** an eligible INVITE 2xx arrives while negative response resolution is outstanding
- **THEN** the proxy forwards that success through its existing owned transactions and prevents the stale callback result from committing or sending a conflicting negative outcome.

#### Scenario: Shutdown during resolution

- **WHEN** the request is retired while its callback is outstanding
- **THEN** the callback result produces no new final send, scheduling, or duplicate completion.

### Requirement: Resolution failures are explicit

A callback error, an out-of-range index, or a mandatory-precedence violation SHALL cause an inspectable resolution failure, not an implicit default choice or timeout. Absent a superseding decision, it SHALL abandon the request once and use the existing upstream failure path with status 500 if the original upstream transaction is usable. Returned error causes SHALL be preserved.

#### Scenario: Invalid candidate index

- **WHEN** a callback returns a negative index or an index equal to the number of offered candidates without an error
- **THEN** the proxy records an invalid selection and does not forward a default candidate or panic.

#### Scenario: Callback error is not a SIP rejection policy

- **WHEN** a callback returns an error, including an error carrying another SIP rejection status
- **THEN** the request follows the resolution-failure path with status 500 rather than using the error to select a different SIP result, and the underlying cause remains inspectable.

#### Scenario: Original upstream transaction is unavailable

- **WHEN** resolution fails after the original upstream transaction becomes unavailable
- **THEN** the request records the failure without creating a replacement transaction or sending a fabricated response.
