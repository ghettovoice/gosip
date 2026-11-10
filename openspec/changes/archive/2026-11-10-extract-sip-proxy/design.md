# Design

## Context

See `proposal.md` for motivation and scope. Relevant implementation constraints are:

- `sip/proxy.go` currently stores `*Element`, registers itself in `NewProxy`, and
  owns an unbind function. Its upstream request and rejection-send indexes use
  transaction keys without an element qualifier.
- `ProxyRequest` already serializes decisions through its own queue and captures
  a cancellation-detached admission context. `ProxyRestoreBindings` explicitly
  supplies transactions and custom deliveries, but not an element.
- Private `Request.forwarded` and `Response.preserveToTag` are consumed by raw
  preparation helpers. `EnsureRequestVia` additionally calls the proxy-private
  `isPrxBranch`, so moving files alone would leave a reverse core dependency.
- Both INVITE and non-INVITE client/server checkpoints explicitly carry
  `ForwardedRequest` or `PreserveResponseToTag`. This is not only an INVITE concern.
- `MessageMetadata.Clone` copies the container, not arbitrary stored values.
  JSON decoding produces `map[string]any` values rather than reconstructing Go
  structs. Proxy-owned metadata must work within those existing semantics.
- `Element.UseMiddleware` injects an element into interceptor/callback contexts.
  `Element.Shutdown` invokes middleware shutdown hooks, and `Element.Close`
  calls registered middleware implementing `io.Closer`.
- `StatsRecorder` depends on proxy events and private core helpers, including
  request-time metadata for Timestamp response delay. It must move with the
  extraction rather than introduce `sip -> proxy` imports.

## Goals / Non-Goals

**Goals:**

- Express the boundary using existing concrete SIP types and middleware seams.
- Keep the element on each operation/coordinator, never on the proxy instance.
- Preserve protocol behavior, serialization meanings, and observation counting
  while adding explicit correlation on envelopes.
- Make migration source-breaking and clear rather than maintain a cyclic facade.

**Non-Goals:**

- An element interface, first-element binding, a new general middleware lifecycle
  protocol, or independent lifetimes for elements sharing one proxy instance.
- A permanent stateless request store, new wire correlation headers, distributed
  tracing infrastructure, or correlation used as routing/security authority.
- Changes to core message `LogValue`, slog handlers/helpers, recorder metric policy,
  RFC 4320 defaults, forking policy, timers, or response-selection rules.

## Decisions

### 1. Package direction and public naming

Use `sip/proxy -> sip` and `sip/stats -> sip + sip/proxy`; the core imports neither.
Keep `proxy.Proxy`; remove redundant prefixes from its surrounding public API:

| Existing API | New API |
| --- | --- |
| `sip.NewProxy` | `proxy.New` |
| `sip.ProxyRoute`, `sip.ProxyOptions` | `proxy.Route`, `proxy.Options` |
| `sip.ProxyRequest` | `proxy.Request` |
| `sip.ProxyTarget`, `sip.ProxyTargetPlan` | `proxy.Target`, `proxy.TargetPlan` |
| `sip.ProxyBoundDelivery`, `sip.ProxyDeliveryRef` | `proxy.BoundDelivery`, `proxy.DeliveryRef` |
| `sip.ProxySelection`, `sip.ProxyBranchOutcome` | `proxy.Selection`, `proxy.BranchOutcome` |
| `sip.ProxyEvent`, `sip.ProxyEventKind` | `proxy.Event`, `proxy.EventKind` |
| `sip.ProxyEventHandler`, `HandleProxyEvent` | `proxy.EventHandler`, `HandleEvent` |
| `sip.ProxyEvent*` | `proxy.EventKind*` |
| `sip.ProxyRequestOutcome`, `sip.ProxyOutcome*` | `proxy.RequestOutcome`, `proxy.Outcome*` |
| `sip.ProxyRequestSnapshot` | `proxy.RequestSnapshot` |
| `sip.ProxyTargetSnapshot`, `sip.ProxyAttemptSnapshot` | `proxy.TargetSnapshot`, `proxy.AttemptSnapshot` |
| `sip.ProxyRequestSnapshotHandler`, `HandleProxyRequestSnapshot` | `proxy.RequestSnapshotHandler`, `HandleRequestSnapshot` |
| `sip.ProxyRestoreBindings` | `proxy.RestoreBindings` |
| `sip.ErrClassProxy` | `proxy.ErrClass` |
| `sip.ErrProxyClosed`, `sip.ErrProxyRequestStarted` | `proxy.ErrClosed`, `proxy.ErrRequestStarted` |
| `sip.ErrInvalidProxySnapshot`, `sip.ErrProxySnapshotUnsupported` | `proxy.ErrInvalidSnapshot`, `proxy.ErrSnapshotUnsupported` |
| `sip.StatsRecorder`, `sip.StatsReport` | `stats.Recorder`, `stats.Report` |
| `sip.TransportStats`, `sip.TransactionStats`, `sip.ProxyStats` | `stats.Transport`, `stats.Transaction`, `stats.Proxy` |

Function adapters and error constructors follow the same rule.
Event-kind constants use the `EventKind` prefix (for example,
`proxy.EventKindRequestHandled`); their numeric values and string forms are
unchanged.
Keep `ForwardMode` and its serialized values. Keep error-class value `"proxy"`,
existing sentinel messages, snapshot JSON names, and statistics report JSON names.
`stats.Report` retains `Transports`, `Transactions`, and `Proxy` fields.

Alternatives: keeping everything in `sip` does not establish a boundary; an
internal implementation with public aliases retains coupling and risks import
cycles. A facade in `sip` is intentionally not provided.

### 2. Construction and registration are separate

The constructor is `New(routes []Route, opts ...Options) (*Proxy, error)`. It copies
and validates configuration using the existing rules, but accepts no element and
installs no middleware. Fallback keys are created in the relevant element scope,
not as construction-time attachment work.

Applications register with `unbind := elm.UseMiddleware(prx)`. Remove `Proxy.elm`
and `Proxy.unbind`; there is no constructor-based alternative registration path.
Use concrete `*sip.Element` wherever an element is required, not a large interface.

### 3. Operation ownership and element-qualified indexes

At a claimed interception boundary, obtain a non-nil element using
`sip.ElementFromContext`. Missing context fails with classified
`proxy.ErrMissingElement` before any proxy state installation or traffic. Unmatched
requests and closed-proxy pass-through do not acquire a hidden default element.

Pass the element explicitly through synchronous stateless processing. Add an
immutable `elm *sip.Element` field to each stateful `proxy.Request`; capture its
stable context as `context.WithoutCancel(sip.ContextWithElement(ctx, elm))`.
Callbacks, drains, timers, request sends, cancellation, error reporting, response
correlation, and cleanup use that field, never a proxy-global element or whichever
transient callback happened to wake the drain.

Use a guarded `map[*sip.Element]*elementState` for request indexes, pending
rejection-send records/order, and the fallback stateless key. Each element state
keeps the existing key-specific synchronization and bounded pending-send policy.
Allocate scopes only for actual processing/restoration, not for empty lookups.
Fallback keys remain stable for the proxy lifetime; configured `Options.StatelessKey`
keeps its existing application-controlled meaning. Do not include trace IDs in
stateless branch derivation or add a per-request stateless history store.

The public lookup becomes:

```go
func (prx *Proxy) LookupRequest(elm *sip.Element, key sip.ServerTransactionKey) (*Request, bool)
```

Nil elements and absent entries return `(nil, false)`. Duplicate admission,
CANCEL/ACK lookup, restore conflicts, and rejection-send correlation use the same
element-qualified namespace. Do not identify elements by transport addresses or
mutable configured instance IDs.

### 4. Lifecycle remains per proxy instance

Keep global, idempotent `Shutdown`/`Close` semantics: seal admission, detach global
subscriptions, retire each request under that request's element context, stop
owned timers/callbacks, and clear every element scope. Never close borrowed
transports or the elements themselves. Preserve admission-versus-shutdown races
and execute callbacks/transaction effects outside proxy index locks.

Registration detachment belongs to the caller/element. Applications retire active
proxy work before unbinding because the outbound response interceptor is required
to settle actual send results. Unbind does not itself call proxy shutdown.

Sharing allows isolated concurrent processing but does not imply independent
ownership: shutting down one registered element can invoke the shared proxy's
instance-wide lifecycle hooks. Document this explicitly; applications needing
independent lifetimes create separate proxies. Do not add reference-counted
attachment, a special registration wrapper, or new Element lifecycle hooks here.
Element scopes are retained until proxy shutdown; dynamic attachment churn is not
being redesigned in this change.

### 5. Restore and observer contexts are explicit

Add `Element *sip.Element` to `RestoreBindings`, alongside the existing
`ServerTransaction`, `ClientTransactions`, and `Deliveries` fields. Reject nil
before staging subscriptions or indexes. This binding wins over an element in the
caller's context; remaining context values are preserved.

Keep existing dormant install, rollback-only-staged behavior, and explicit
`Request.Start(ctx)`. Reapply the bound element when starting restored transactions
and for all resumed work. Supplying compatible transactions/resources for that
element remains the application's explicit rebinding responsibility; retain the
existing relationship/mode validation rather than infer ownership from a key.

Global event/snapshot subscriptions continue to observe every element. At each
handler invocation, stamp the processing element into context. This includes
admission, early rejection, stateless response, drain/timer effects, lifecycle
cleanup, and restored activation. Neither `Event` nor `RequestSnapshot` gains an
element field, including a `json:"-"` runtime field.

### 6. Core provenance is envelope metadata, not proxy policy

Use core-owned keys `sip.forwarded`, `sip.preserve_to_tag`, and
`sip.forwarded_via_branch`; do not place them inside `sip.proxy`. The last key
explicitly identifies the local top Via branch whose transport/sent-by may be
finalized. These are public, local preparation conventions, not authenticated
statements about a remote sender.

Provide the following typed envelope operations (setters do not return errors):

| Receiver | Operations |
| --- | --- |
| `MessageEnvelope[T]` | `MarkForwarded()` and `Forwarded() bool` |
| `ResponseEnvelope` | `SetPreserveToTag(bool)` and `PreservesToTag() bool` |
| `RequestEnvelope` | `SetForwardedViaBranch(string)` and `ForwardedViaBranch() string` |
| `RequestEnvelope` | `PrepareVia(TransportProto, Addr)` and `EnsureFromTag()` |
| `ResponseEnvelope` | `EnsureToTag(string)` |

Absent/wrong-typed boolean metadata reads as false. The forwarding marker works
on both request and response envelopes; response tag preservation also honors its
specific flag. Empty local-branch designation means no local Via may be rewritten.

Keep raw `EnsureRequestVia`, `EnsureRequestFromTag`, and `EnsureResponseToTag`
signatures as endpoint-oriented message helpers. Move provenance checks into the
new envelope operations and route every element, client-transaction, attempt, and
transport preparation path through those operations. Do not call a locking
envelope method inside that envelope's `WithMessage` callback.

For a forwarded request, `PrepareVia` never invents a hop/branch and updates a top
Via only when its branch equals the explicit local designation. Remove the
core's call to `isPrxBranch`: only `sip/proxy` understands its wire namespaces.
The proxy clears inherited local-hop designation on fresh branch copies, then
sets it when inserting its own Via. Preserve the current pre-wire
UDP-to-reliable branch-reuse rule and fresh identity on ordinary address retries.

Carry provenance through CANCEL envelope creation and cached response sends;
new envelopes must not lose it merely because the underlying message was copied.
Retain `ForwardedRequest`/`PreserveResponseToTag` checkpoint fields as compatibility
mirrors for both INVITE and non-INVITE transactions. On restore, a true legacy
field establishes the corresponding metadata; a false/absent legacy field does
not clear a present marker. Trusted legacy forwarded checkpoints can recover the
local designation from their saved outbound top Via. Keep version and JSON field
meanings unchanged; do not remove legacy fields in this change.

Alternative: exporting private message flags leaves provenance outside the
universal metadata mechanism and fails to address core knowledge of proxy branches.

### 7. Narrow semantic core APIs replace private accesses

Expose these operations using existing concrete types and conventions:

```go
func (elm *Element) NewCancelClientTransaction(ctx context.Context, invTx ClientTransaction, opts ...ClientTransactionOptions) (ClientTransaction, error)
func (elm *Element) LookupRequestAddrs(ctx context.Context, uri *URI, opts ...LookupMessageAddrsOptions) iter.Seq[ResolvedAddr]
func (elm *Element) ReportError(ctx context.Context, err error)
func ClearTransactionBinding(meta *MessageMetadata)
func CleanRequestURI(uri *URI) *URI
func (res *ResponseEnvelope) RequestTime() (time.Time, bool)
```

The CANCEL and lookup wrappers initialize/bind the element and obey existing
operation/lifecycle admission, including lazy iterator execution. `ReportError`
preserves current error-handler filtering and callback leases. Initialization
stays inside `sip`; the proxy no longer calls `ensureInit` or accesses `srvLctr`
and `txm` directly.

`ClearTransactionBinding` removes only the core transaction key/type metadata.
`CleanRequestURI` preserves the current helper's cleaning/ownership semantics.
`RequestTime` exposes the existing response request-time metadata used by statistics;
keep its Timestamp/RTT policy unchanged. Keep small numeric statistics helpers
private to `stats`. Do not export raw managers or a list of private metadata keys.

Keep clone convenience wrappers private to `proxy`, implemented with public
`Clone` methods. `adjustReqRejection` already lives in a proxy file and moves with
it; it needs no core export. Use `sip.ProtoVer20()` instead of private constants.

### 8. One correlation value and one identity source

Introduce the following public data model in `sip/proxy`:

```go
const MetadataKey = "sip.proxy"

type Stage string

const (
    StageClaim    Stage = "claim"
    StageForward  Stage = "forward"
    StageResponse Stage = "response"
    StageUpstream Stage = "upstream"
)

type Metadata struct {
    RequestID        string      `json:"request_id,omitempty"`
    RelatedRequestID string      `json:"related_request_id,omitempty"`
    TargetID         string      `json:"target_id,omitempty"`
    AttemptID        string      `json:"attempt_id,omitempty"`
    RouteName        string      `json:"route_name,omitempty"`
    Mode             ForwardMode `json:"mode"`
    Stage            Stage       `json:"stage"`
}

func MetadataFrom(meta *sip.MessageMetadata) (Metadata, bool)
func SetMetadata(meta *sip.MessageMetadata, value Metadata)
```

Store `Metadata` by value and replace it as a whole. Typed reads return copies and
accept the stored value or a JSON-decoded map; decode maps into the declared type
and validate stage/mode without mutating the source map. Wrong-typed/malformed
values return false. Nil metadata containers are safe for reads and no-op writes.
Do not introduce a core JSON type registry or promise deep cloning of arbitrary
application metadata. Immutable scalar fields avoid library-created aliasing.

Mint the operation identity once after route selection and before claimed work.
Carry it into either the lightweight observation operation or the admitted request
coordinator. A duplicate uses the existing owner's identity without new handling
observations. Stateful promotion transfers the same identity and accepted routing
state; effective mode becomes stateful on its copies, not on configured routes.
Do not use `RelatedRequestID` as a substitute for promotion identity continuity.

Stamp each envelope at the boundary where the information is known:

| Envelope | Stage | Attribution |
| --- | --- | --- |
| Claimed inbound/working request | claim | request ID, route, effective mode |
| Prepared downstream request | forward | same request ID plus target/physical attempt |
| Attributed downstream response | response | owning request/target/attempt |
| Upstream response copy | upstream | request plus an actual known source, if any |

Use the existing request, target, and attempt identity generators and checkpoint
fields, not a parallel ID system. Stamp before the corresponding send/callback;
metadata for immediate responses must not depend on the initial send returning.
Generated local responses and aggregated results do not invent a single winning
attempt. Repeated/additional eligible INVITE 2xx retain their real source and do
not reopen completion or scheduling.

A distinct ACK/CANCEL operation gets its own lightweight identity and uses
`RelatedRequestID` only when a live relationship is known. Annotating it does not
create a response coordinator, extra observation events, or new retention.
Downstream CANCEL builders preserve forwarding provenance and receive appropriate
proxy attribution before starting/sending. On a new traversal, replace prior
`sip.proxy` stamps; keep only explicitly known relationships, not a history array.
The old `sip.proxy_route` key is retired. Keep `sip.proxy_finalized` separate as an
internal processing guard and reset it on every fresh branch copy.

For stateless responses, mode and stage can be known while original request,
route, target, and attempt IDs are not. Leave them empty. Metadata is not sent in
SIP messages and cannot supply cross-network or cross-process correlation by
itself. Existing Via ownership rules remain authoritative.

### 9. Correlation and provenance survive recovery without replay

Keep supported snapshot versions and existing observation fields. Envelope metadata
survives JSON naturally; typed access handles decoded maps. Checkpoint RequestID,
route, effective mode, and attempt records are authoritative when reconstructing
missing older metadata, rather than accepting stale message stamps as authority.
Preserve known related-request links. Rebuild attempt allocation past saved
attempt IDs so new attempts cannot collide after restart.

Metadata restoration occurs only on owned dormant copies, before publication.
It does not emit events, resolve targets, send traffic, or replay preprocessing.
The first newly committed event continues the saved sequence, and existing
first-forward/completion flags retain their current meaning.

### 10. Logging uses the metadata type, not a handler framework

Implement `Metadata.LogValue() slog.Value` as a group with the same field names as
JSON, mode's readable name, and the stage string. Omit empty IDs and unknown route
names; include known mode and stage. Proxy logs attach `slog.Any("proxy", md)` when
correlation exists. Logs before identity is available need not fabricate metadata.
Do not dump arbitrary message metadata or authentication material. Core
`MessageEnvelope.LogValue` and `pkg/log` remain unchanged. Handler wrappers,
filters, and end-application rendering helpers are explicitly deferred.

### 11. Statistics relocation preserves behavior

Move the recorder, reports, tests, and statistics example into `sip/stats`, retaining
the zero-value API, explicit `prx.BindEventHandler(recorder)`, and
`elm.UseMiddleware(recorder)`. Implement `proxy.EventHandler` using `HandleEvent`.
Preserve counter definitions, concurrency behavior, Timestamp processing, report
JSON, and the distinction between successful sends and eventual SIP outcomes.
Keep counters outside snapshots; do not reconstruct historical totals on restore.

## Risks / Trade-offs

- Source incompatibility -> Provide a complete API/import migration table and
  update examples, docs, and project rules; do not claim absence of external users.
- Incomplete private-access inventory -> Include core branch preparation and
  statistics request-time access, not only the three private Element accesses.
- Equal keys on different elements -> Test request, CANCEL, rejection-send, lookup,
  and restore namespaces with colliding keys and interleaved callbacks.
- Shared lifecycle closes other processing -> Document global ownership; use
  separate proxy instances for independent element lifetimes.
- Inherited or mutable metadata -> Store scalar values, replace whole stamps,
  validate decoded representations, and reestablish authoritative checkpoint data.
- Provenance lost in a lower send/checkpoint path -> Cover element, transaction,
  transport, CANCEL, cached response, and all four transaction restore families.
- Context drift during asynchronous work -> Reapply each request's immutable
  element at effects and observer dispatch, including during instance-wide cleanup.
- Stateless trace gaps -> Document partial correlation rather than changing branch
  derivation, storing history, or inventing request/attempt identities.
- Premature unbinding loses send observation -> Retire work before detachment and
  test the documented close-then-unbind sequence.

## Migration Plan

1. Add core metadata/provenance preparation and narrow semantic APIs with focused
   regression tests, retaining compatibility checkpoint mirrors.
2. Move proxy and statistics together as one source migration so no temporary
   `sip -> proxy` dependency or compatibility aliases are introduced.
3. Apply request-scoped element ownership, explicit restore/lookup, canonical
   observer contexts, and metadata/identity stamping using the existing coordinator.
4. Move white-box tests with their owning package; keep core-only tests in `sip`.
   Black-box integration tests can import the new packages without cycles. Reuse
   `internal/testutil` doubles; do not export internals just to relocate tests.
5. Update `docs/proxy.md`, recovery/statistics examples, package comments, import
   references, and `.agents/rules/proj_guide.md` references to the moved APIs.
6. Run targeted tests during each step, then one final full race/vet test gate and
   a fresh full lint pass. No build/test execution is required for this planning-only
   workflow, and implementation begins only after a new apply request.

There is no deployment or persistence migration service. Existing supported
checkpoint JSON remains readable through the compatibility adapters. Applications
migrate imports, explicit registration, restore bindings, and qualified lookup
alongside the library update. Rollback means reverting the implementation and its
consumer migration together; older library versions need not understand newly
added correlation metadata beyond their existing arbitrary-metadata behavior.

## Verification Strategy

- Extend transparent-preparation tests for untagged transit messages, designated
  versus foreign Via hops, Max-Forwards zero, SIPS, and UDP size switching.
- Test provenance clone/JSON round trips and legacy/new-metadata checkpoints for
  both INVITE/non-INVITE client and server transactions.
- Test constructor side-effect freedom, missing-element errors, equal keys across
  two real elements, rejection-send interleaving, explicit lookup/restore, and
  global close/admission races using existing doubles and `testing/synctest`.
- Inspect observer context for admission, rejection, stateless response, queued
  drain, Timer C, cleanup, and restored activation, including conflicting transient
  caller contexts. Assert no element field is added to events or snapshots.
- Compare metadata, event IDs, and snapshots across forks, immediate responses,
  retries, promotion, ACK/CANCEL, local/aggregated responses, and recovery. Assert
  honest empty stateless associations and unchanged observation counts.
- Exercise typed metadata reads with native values, decoded maps, absent/malformed
  values, and clone replacement; resolve `slog.LogValuer` using an ordinary slog
  handler without adding production handler helpers.
- Retain recorder report/JSON and concurrency assertions and compile/run migrated
  examples through the normal Go test/vet gate.
