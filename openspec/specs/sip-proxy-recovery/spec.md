# sip-proxy-recovery Specification

## Purpose

Allow applications to capture and restore SIP proxy request orchestration explicitly while retaining ownership of persistence, transaction checkpoints, and transport resources.

## Requirements

### Requirement: Explicit versioned request snapshots

The proxy SHALL expose versioned snapshots of stable per-request orchestration state without performing storage I/O. Snapshots SHALL preserve the original and post-preprocess working requests separately and identify their route, upstream transaction, downstream attempts, target-plan progress, response decisions, cancellation intent, and proxy-owned timer deadlines.

#### Scenario: Capture an active fork

- **WHEN** an application captures a request with completed, active, and unstarted targets
- **THEN** the snapshot distinguishes those targets and preserves the data needed to continue the existing plan without resolving contacts again.

### Requirement: Snapshot value isolation

Snapshots and callback observations SHALL be independent of subsequent proxy mutations. Applications SHALL be able to serialize protocol-owned state without serializing callbacks, locks, sockets, or transport objects. Unsupported application metadata SHALL have a documented explicit handling policy rather than silently losing required routing state.

#### Scenario: Caller mutates a snapshot

- **WHEN** an application changes a returned target, response, or metadata container
- **THEN** the live proxy and previously returned independent snapshots remain unchanged.

#### Scenario: Caller mutates a working request snapshot

- **WHEN** an application changes the saved working request in a returned snapshot
- **THEN** the live working request, the saved original request, and independently captured snapshots remain unchanged.

### Requirement: Application-owned checkpoint coordination

The proxy SHALL document how its orchestration snapshot pairs with separately captured transaction snapshots and runtime configuration. It SHALL NOT claim that independent snapshots or network effects form an atomic durable checkpoint. Custom transactions without snapshot support SHALL remain usable for forwarding and fail snapshot requests explicitly when required support is absent.

#### Scenario: Independent persistence

- **WHEN** an application subscribes to proxy and transaction snapshot publications
- **THEN** storage ordering and a consistent checkpoint across those publications remain explicit application responsibilities.

#### Scenario: Non-snapshotting custom transaction

- **WHEN** a required custom transaction does not provide the snapshot observations needed for recovery
- **THEN** ordinary forwarding remains available but recovery capture fails with an inspectable unsupported-operation error.

### Requirement: Restore validates transaction bindings

Restoration SHALL require an explicit non-nil processing element, compatible upstream and downstream transaction bindings, and the named route configuration. It SHALL reject unsupported versions, invalid or missing working requests, malformed plans, duplicate target identities, invalid deadlines, missing bindings, incompatible behavior selections, and incompatible transaction relationships without silently recreating missing transactions.

#### Scenario: Missing downstream transaction

- **WHEN** a snapshot references an attempt for which no required transaction binding is supplied
- **THEN** restoration fails before activating timers or sending network traffic and leaves unrelated live state unchanged.

#### Scenario: Conflicting request

- **WHEN** the upstream request or a downstream binding does not match the snapshot's recorded relationship
- **THEN** restoration returns a validation error rather than attaching the snapshot to a different request.

#### Scenario: Version one snapshot lacks the working request

- **WHEN** a version-1 orchestration snapshot omits its required saved working request
- **THEN** restoration fails before installing state or producing effects, without reconstructing that request or replaying application preprocessing.

#### Scenario: Missing explicit element

- **WHEN** an application restores a request without supplying a processing element, even if its caller context contains one
- **THEN** restoration fails inspectably before claiming bindings, installing subscriptions or indexes, arming timers, or producing network effects.

#### Scenario: Explicit binding overrides caller context

- **WHEN** valid restoration supplies one element in its bindings and a different element in its caller context
- **THEN** the restored request and all subsequent work use the explicitly bound element rather than the caller-context element or a previously used element.

### Requirement: Restore validates saved request values

Restoration SHALL validate the saved working request's message structure and method compatibility with the original request, and SHALL apply the supported-metadata policy to both saved request envelopes. These checks SHALL complete before claiming transaction bindings or installing subscriptions, indexes, or timers. Validation SHALL NOT replay application preprocessing or local-route consumption, reconstruct working state, or produce network effects.

#### Scenario: Malformed saved working request

- **WHEN** a snapshot contains a working request whose method matches the original request but whose required headers, Request-URI, or SIP protocol are invalid
- **THEN** restoration returns an inspectable snapshot-validation error before claiming bindings or installing state, leaving the supplied transactions and unrelated live state unchanged.

#### Scenario: Unsupported metadata at restoration

- **WHEN** an application supplies an otherwise valid restore snapshot with unsupported opaque metadata in its original or working request
- **THEN** restoration returns an inspectable unsupported-operation error before claiming bindings or installing state, without silently dropping metadata or replaying preprocessing.

### Requirement: Restore preserves saved transaction behavior

Proxy checkpoints SHALL retain the effective non-INVITE server behavior and require compatible transaction bindings. Current creation defaults SHALL NOT silently change the saved mode. Mode changes for new transactions SHALL NOT upgrade existing restored requests.

#### Scenario: Restored Legacy request with new creation defaults

- **WHEN** a Legacy request checkpoint and matching Legacy bindings are restored under configuration that enables RFC 4320 for new transactions
- **THEN** restoration preserves the saved Legacy behavior rather than enabling the option.

#### Scenario: Binding has a different behavior mode

- **WHEN** a supplied non-INVITE server binding has a different mode from the request checkpoint
- **THEN** restoration fails before activation instead of silently accepting incompatible behavior.

### Requirement: Dormant restore before activation

Restoration SHALL prepare request state and attach its transaction relationships without starting timers, resolving new contacts, replaying sends, or emitting response events. An explicit activation step SHALL resume preserved work after all required callbacks and indexes are installed.

#### Scenario: Inspect a dormant restoration

- **WHEN** a valid request snapshot and compatible dormant transactions are restored but not activated
- **THEN** no initial request, response, CANCEL, or timer action is emitted.

### Requirement: Resume preserved progress and deadlines

Activation SHALL preserve the post-preprocess working request, absolute timer deadlines, completed targets, forwarded-final decisions, and cancellation intent. It SHALL NOT restart a full timeout or repeat completed groups. Protocol-required retransmissions SHALL remain the responsibility of the corresponding restored transactions.

#### Scenario: Restore an expired Timer C

- **WHEN** a restored active attempt has a Timer C deadline in the past
- **THEN** activation applies the expiry policy immediately rather than granting a new full Timer C interval.

#### Scenario: Restore cancellation

- **WHEN** the snapshot records cancellation and a deferred downstream CANCEL
- **THEN** activation starts no new targets and resumes only the permitted cancellation and remaining transaction processing.

#### Scenario: Restore after forwarding a success

- **WHEN** a snapshot records a previously forwarded INVITE 2xx
- **THEN** activation does not replay that forwarding decision, while subsequently received eligible 2xx responses can still be forwarded.

#### Scenario: Preprocessing survives a serialized checkpoint

- **WHEN** application preprocessing changes routing before an active request is captured, serialized to JSON, decoded, restored, and activated, and the next sequential group becomes eligible
- **THEN** that group's outbound request preserves the saved preprocessing changes without rerunning the application hook, resolving contacts again, or repeating local-route consumption.

### Requirement: Delivery references do not recreate live flows

Snapshots SHALL retain serializable delivery references rather than live network handles. Restoration SHALL require the application to rebind custom delivery resources, with an explicit failure when a required resource is absent. It SHALL NOT infer that a connection survived a process restart from an address tuple alone.

#### Scenario: Missing bound flow after restart

- **WHEN** a restored target requires an existing flow that cannot be rebound
- **THEN** the binding fails explicitly without silently opening a new connection or substituting ordinary DNS delivery.

### Requirement: Snapshot subscription and lifecycle

Snapshot observation SHALL have explicit bind/unbind semantics and ordered per-request revisions. User callbacks SHALL run outside proxy mutation locks and receive the request's processing element through context only. Shutdown SHALL detach observations and release recovery-related resources without closing application-owned storage or transports. Serialized snapshots SHALL NOT contain runtime element references.

#### Scenario: Unbind during concurrent processing

- **WHEN** an application unbinds a snapshot observer while requests are progressing
- **THEN** unbinding is idempotent and does not deadlock with snapshot capture or proxy shutdown.

#### Scenario: Snapshot notification after restored activation

- **WHEN** a restored request publishes a new snapshot revision from resumed work or an expired timer
- **THEN** the observer obtains the explicitly rebound element from its callback context without an element field in the snapshot.

### Requirement: Recovery does not replay observations

Restoration and activation SHALL NOT re-emit handling or send/completion observations for recorded historical work. Newly occurring actions SHALL remain observable. Checkpoints SHALL preserve required observation decisions without serializing subscribers or recorder totals, and SHALL NOT promise atomic or exactly-once external metrics across crashes.

#### Scenario: Restore an already forwarded request

- **WHEN** a saved request records a successful initial forward or a committed completion
- **THEN** restoring and activating it does not repeat those observations or reconstruct historical recorder counters.

#### Scenario: New response after activation

- **WHEN** a newly received eligible INVITE 2xx is forwarded after activation
- **THEN** the new response send is observed without replaying the prior request-handled, initial-forward, or completion events.

### Requirement: Element-qualified request lookup

Public request lookup SHALL require the processing element together with the upstream transaction key. Equal keys on different elements SHALL resolve independently, including requests installed dormant by restoration. A lookup without an element or without a matching entry SHALL return no request without mutating proxy state.

#### Scenario: Equal restored upstream keys

- **WHEN** two compatible snapshots with equal upstream keys are restored into different elements of one proxy
- **THEN** each element-qualified lookup returns only its own request and neither restore conflicts merely because the keys are equal.

#### Scenario: Lookup without an element

- **WHEN** an application queries a request with a nil element
- **THEN** lookup returns no request instead of choosing an arbitrary matching element.

### Requirement: Recovered message correlation

Recovery SHALL preserve saved request identity, observation sequence, known related-request links, and attempt attribution across envelope JSON round trips. Authoritative checkpoint identities SHALL determine reconstructed metadata when older snapshots lack it. New attempt IDs SHALL NOT alias historical attempts, and rebuilding metadata SHALL NOT replay sends or historical observations.

#### Scenario: Next attempt after JSON restoration

- **WHEN** a serialized request and its transaction checkpoints are decoded, explicitly rebound, activated, and continue to a new address attempt or target
- **THEN** messages and new events retain the saved request ID, continue the saved event sequence, and use an attempt ID distinct from saved attempts.

#### Scenario: Existing snapshot without proxy metadata

- **WHEN** a valid supported snapshot has observation identities but no new `sip.proxy` metadata
- **THEN** restoration derives only known message correlation from the saved orchestration state without minting a replacement request ID or replaying historical events.

#### Scenario: Dormant restoration remains side-effect free

- **WHEN** metadata is restored or reconstructed on a dormant request
- **THEN** no sends, timers, contact resolution, or observer notifications occur until the existing explicit activation boundary.
