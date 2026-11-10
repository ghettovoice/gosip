# Spec Delta

## MODIFIED Requirements

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

### Requirement: Snapshot subscription and lifecycle

Snapshot observation SHALL have explicit bind/unbind semantics and ordered per-request revisions. User callbacks SHALL run outside proxy mutation locks and receive the request's processing element through context only. Shutdown SHALL detach observations and release recovery-related resources without closing application-owned storage or transports. Serialized snapshots SHALL NOT contain runtime element references.

#### Scenario: Unbind during concurrent processing

- **WHEN** an application unbinds a snapshot observer while requests are progressing
- **THEN** unbinding is idempotent and does not deadlock with snapshot capture or proxy shutdown.

#### Scenario: Snapshot notification after restored activation

- **WHEN** a restored request publishes a new snapshot revision from resumed work or an expired timer
- **THEN** the observer obtains the explicitly rebound element from its callback context without an element field in the snapshot.

## ADDED Requirements

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
