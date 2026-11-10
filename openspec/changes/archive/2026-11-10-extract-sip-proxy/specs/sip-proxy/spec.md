# Spec Delta

## MODIFIED Requirements

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

Public constants of `proxy.EventKind` SHALL use the `EventKind` prefix:
`EventKindRequestHandled`, `EventKindForwardAttemptFinished`,
`EventKindRequestForwarded`, `EventKindResponseForwarded`, and
`EventKindRequestCompleted`. Their numeric values SHALL remain 0 through 4
in that order, and their existing `String()` representations SHALL remain
unchanged.

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

## ADDED Requirements

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
