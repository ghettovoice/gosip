# SIP Proxy

The `github.com/ghettovoice/gosip/sip/proxy` package provides `Proxy`, an
inbound middleware for a `sip.Element` that claims inbound requests matching
configured routes and forwards them according to each route's configuration.
The core `sip` package does not depend on `sip/proxy`.

## Constructing a Proxy

```go
prx, err := proxy.New(routes, opts)
unbind := elm.UseMiddleware(prx)
```

- `routes` is a `[]proxy.Route` slice evaluated in order; the first
  matching route wins.
- `opts` is an optional `proxy.Options` value.

`proxy.New` validates and copies the routes and options: later changes to the
caller's slices, maps, and URIs do not affect the running proxy, and a failed
construction installs no middleware. Construction fails on duplicate route
names, unsupported forward modes or non-INVITE server transaction behaviors,
and out-of-range Timer C values.

The constructor binds no element: an explicit `elm.UseMiddleware(prx)`
registration attaches the proxy to an element's processing chain, and one
instance may be registered on several elements at once. The registration's
`unbind` detaches the middleware from that element only — it is never a
shutdown path.

## Migrating from the `sip` package API

The proxy and statistics APIs moved out of the core `sip` package into
`sip/proxy` and `sip/stats`. The core imports neither package, and no facade
or aliases remain in `sip`. Map existing code with this table:

| Previous API | Current API |
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

Function adapters and error constructors follow the same
rule. Event-kind constants use the `EventKind` prefix (for example,
`proxy.EventKindRequestHandled`); their numeric values and string forms are
unchanged. `ForwardMode` and its serialized values, the error-class value
`"proxy"`, existing sentinel messages, snapshot JSON names, and statistics
report JSON names are unchanged. `stats.Report` retains its `Transports`,
`Transactions`, and `Proxy` fields.

Registration also changed: the removed constructor accepted an element and
self-registered. `proxy.New` is pure — construct the proxy, then attach it
with `elm.UseMiddleware(prx)`; see the lifecycle section for the required
retire-before-unbind ordering.

## Proxy lifecycle

One proxy instance has one lifecycle covering every element it is
registered on. `prx.Shutdown(ctx)` — or the equivalent `prx.Close()` — is
idempotent and instance-wide: it seals admission on all elements at once so
new matching requests pass through unclaimed, detaches the proxy's own
subscriptions, retires each owned request under the element that claimed
it, and clears every element scope. The owning element invokes `Shutdown`
as its optional middleware hook, so shutting down any one element — for
example by closing it — shuts the whole proxy down. Shutdown never closes
borrowed resources: transports, elements, sibling middleware, and
transactions the proxy does not own are all left running. Applications
that need independent lifetimes — for example draining one element while
another keeps processing — must build an independent proxy instance per
lifetime.

Registration detachment is the caller's responsibility and follows one
ordering rule: retire active proxy work before calling `unbind`. The
registration's outbound response interceptor is required to settle the
sends of in-flight requests, so run `prx.Shutdown(ctx)`/`prx.Close()` —
or close an element, which invokes the same hook — first, and only then
call `unbind`. Unbind itself never invokes proxy shutdown.

## Routes

A `proxy.Route` is an immutable routing rule owned by the proxy after
`proxy.New` returns.

| Field | Meaning |
| --- | --- |
| `Name` | Route identifier reported in request metadata, events, and snapshots. Must be unique within one proxy. |
| `ForwardMode` | `proxy.ForwardModeStateful` (zero value) forwards through an upstream server transaction and downstream client transaction attempts. `proxy.ForwardModeStateless` forwards without transactions and requires `Options.StatelessKey`. |
| `MatchRequest` | Reports whether the route claims the inbound request. Nil accepts every request at the route's position. |
| `RecordRoute` | Requests insertion of a Record-Route header field value into forwarded requests. |
| `ServerTransactionOptions` | Passed unchanged to the upstream server transaction created for a stateful request. |
| `ClientTransactionOptions` | Configures downstream client transaction attempts. |

### Non-INVITE transaction behavior

`Route.ServerTransactionOptions.NonInviteServerTransactionBehavior` selects the response
and timer profile of the upstream non-INVITE server transaction the route
creates for a stateful request. The option is local to the transaction this
proxy creates: it is never negotiated with or advertised to peer elements,
and it has no effect on INVITE transactions.

The zero value `sip.NonInviteServerTransactionBehaviorLegacy` is the default: RFC 4320
handling is off unless a route opts in explicitly with
`sip.NonInviteServerTransactionBehaviorRFC4320`, which rejects provisional responses other
than 100 Trying, rejects 408 Request Timeout, and sends an automatic 100
Trying once the delayed-Trying deadline passes on unreliable transports.
Full RFC 4320 behavior in a deployment requires this explicit enablement —
or equivalent guarantees enforced by the application — at every element
that terminates the non-INVITE server transaction; enabling it on a single
route or proxy does not provide it end to end.

The option travels through a single factory seam: the proxy passes the
route's `ServerTransactionOptions` unchanged to every upstream server
transaction it creates, including a matching CANCEL transaction, and never
overrides the application's selection.

## Stateful forwarding

A stateful route owns the request through an upstream server transaction
and downstream client transaction attempts. Downstream responses are
processed on owned copies: exactly one Via hop is removed, the forwarded
dialog tag and body cross upstream verbatim, and the response is sent only
through the original upstream transaction — a missing or unusable
transaction discards the response rather than fabricating a replacement.
After a committed final decision, additionally received eligible INVITE
2xx responses on retained transactions are still forwarded upstream, and
the request completion is not repeated. Non-INVITE suppression guards
(non-100 provisionals, 408) apply in either transaction behavior mode.

### Route hooks

All hooks are optional; a nil hook selects the documented default.

| Hook | Default when nil | Purpose |
| --- | --- | --- |
| `ResolveRequestTargets` | RFC 3261 predetermined-target rules still run; without a predetermined target the request is rejected with the documented no-contact rejection. | Resolves the `proxy.TargetPlan` for the request. |
| `AuthorizeRequest` | Forwarding is allowed. | Authorizes the inbound request; returning `sip.RequestRejectedError` produces the carried SIP rejection. |
| `PreprocessRoute` | Standard routing preprocessing is unchanged. | Inspects or rewrites routing information on an owned working copy. Cannot bypass local-identity, security, or loop rules. |
| `AdjustOutboundRequest` | The outbound copy is used as constructed. | Customizes one outbound branch copy before final invariant checks and Via insertion. |
| `SelectTargets` | The first remaining plan group is the next selection. | Chooses the unstarted target IDs to attempt next; returned IDs must name unstarted targets exactly once. |

Authorization, route preprocessing, and logical target resolution belong
to one inbound routing operation. Pre-send promotion continues that same
operation without replaying those hooks or consuming local routing again.
Separate stateless inbound operations may each have run policy before
competing for stateful admission; only the winner owns the coordinator.
Subsequent retransmissions matching its upstream transaction are consumed
by the transaction manager without re-entering route hooks.

Execution hooks have narrower scopes: `AdjustOutboundRequest` runs for
each per-target preparation (including a pre-send stateless preparation
that later requires promotion), `SelectTargets` runs per selection cycle,
and address resolution/delivery work follows the attempt lifecycle.
Promotion does not make these hooks globally once-only.

### Secure forwarding

A SIPS Request-URI on the original incoming request requires a SIPS
Request-URI and secured transport on every downstream send. This constraint
is retained independently of the resolved contact or mutable working URI,
including after recovery. Preprocessing, outbound adjustment, strict-route
rewriting, and delivery binding cannot weaken it. An unsupported secure
delivery is rejected before sending; the proxy does not automatically
upgrade a SIP contact or guess TLS availability. A security violation does
not trigger stateless-to-stateful promotion.

A SIP request received over TLS does not by itself acquire a SIPS
Request-URI requirement. The RFC 3261 secure Record-Route rules are a
separate decision: the incoming transport and outgoing next hop can require
a reachable configured SIPS Record-Route identity even when the forwarded
Request-URI is SIP. Record-Route selection retains the accepted working
request's direction and transport view.

## Stateless forwarding

A route with `ForwardMode: proxy.ForwardModeStateless` forwards requests
without creating transactions. Stateless forwarding is a contract, not a
relaxed mode:

- **A `StatelessKey` is required.** `proxy.New` rejects an explicit
  stateless route configured without `Options.StatelessKey`. The key
  is a stable application secret that authenticates the deterministic Via
  branch derivation (`statelessBranch`): because a stateless proxy cannot
  distinguish a retransmission from the original, the branch is a keyed
  HMAC over message fields that are invariant on retransmission — the
  target identity, Request-URI, Route set, Call-ID, From tag, CSeq
  sequence, and top Via. The method and To tag are excluded so a related
  CANCEL and a non-routed ACK derive the same branch as the request they
  relate to. Every proxy instance that must emit identical stateless
  branches shares the key; deploy it like a signing key. The same
  normalization applies to the complete Via branch a stateless send emits:
  a related CANCEL and a non-routed ACK carrying the associated request's
  upstream transaction identity select the same destination and complete
  Via branch — the derivation is To-tag invariant. An ACK with a different
  transaction identity, such as an end-to-end ACK for a 2xx, may carry a
  different branch.
- **Exactly one logical target and one destination.** A genuine stateless
  operation accepts exactly one target across all plan groups, ignoring
  empty groups. Distinct target IDs count separately even when their URIs
  are equal. That target commits to one destination — a final remote
  address plus a bound client transport — selected before the first wire
  send. A literal-IP next hop uses the built-in deterministic selection and
  never touches DNS; a hostname or otherwise custom next hop is reachable
  statelessly only through
  `Options.ResolveStatelessDestination`, whose result must be stable
  across retransmissions. Ordinary mutable or weighted DNS ordering is not
  a determinism guarantee. A send failure is reported as-is: there is no
  post-send failover to an alternate destination.
- **Promotion happens before the first send.** An ordinary response-eligible
  request with multiple logical targets promotes the complete plan before
  observations, target adjustment, binding, or branch preparation. Stateful
  scheduling starts same-group targets together and respects ordered groups;
  no target is truncated or deduplicated by URI. When the deterministic
  destination invariants cannot be established — no resolver for a hostname
  target, a resolver error, an unusable bound delivery — a response-eligible
  request is promoted to stateful forwarding before anything is emitted, and the
  promoted send carries the stateful Via ownership mark. Promotion transfers
  independently owned original and accepted working requests plus the
  complete target plan, including group order and attributes. The original
  retains upstream transaction identity; branches continue from the working
  state without replaying authorization, preprocessing, logical resolution,
  or local-route consumption. The stored route remains stateless; only the
  admitted request's route copy and saved mode are stateful.
- **CANCEL and ACK stay stateless when they must.** An unmatched CANCEL
  follows the mandatory stateless forwarding of RFC 3261 Section 16.10 —
  it fails routing rather than being promoted — and an end-to-end ACK is
  never promoted because it has no response context. A multi-target plan
  fails before delivery effects for either method, with no transaction,
  independent response context, upstream response, or authentication challenge.
  This also applies to their mandatory transaction-free paths on stateful
  routes; matching stateful CANCEL handling is unchanged. A stateful-only proxy
  derives an instance-local fallback key solely for these mandatory paths;
  the fallback never authorizes an explicitly configured stateless route.
- **Response ownership requires both a branch mark and local sent-by.** An inbound
  response is forwarded statelessly only when its top Via carries the
  proxy-stateless branch mark *and* its sent-by matches this element's
  transport advertised address. When a receiving transport is available,
  its protocol and sent-by must match that top Via. The
  proxy removes exactly that one Via and routes the copy by the next Via.
  A stateful-marked response with no live transaction is discarded, and a
  response outside the proxy branch namespace passes to the next receiver.
  A claimed response with an invalid or foreign sent-by, or no remaining
  Via, is discarded. These routing checks do not authenticate a remote peer.
- **Outbound is not supported.** The proxy implements no RFC 5626
  Outbound, keep-alive, or token handling and never advertises the
  `outbound` option tag.

See `ExampleProxy_stateless` and `ExampleProxy_statelessDestination` in
`sip/proxy/example_test.go` for compile-checked configurations.

## Target plan

`proxy.TargetPlan` is an ordered, eagerly resolved location-target plan.
`Groups` holds the ordered logical-target groups: each group is attempted
before the next group is considered.
This ordering is preserved when a multi-target stateless route promotes.
One target with empty surrounding groups still executes statelessly; a
plan with no actual targets retains the existing no-contact rejection
(480 for response-eligible requests).

Each `proxy.Target` is a single logical forwarding target:

| Field | Meaning |
| --- | --- |
| `ID` | Identifier unique within the plan. An empty ID is replaced with a deterministic `group.target` position ID; duplicate IDs are rejected. |
| `URI` | The target contact URI. |
| `Routes` | Per-target route set applied to the outbound copy. |
| `Delivery` | Optional reference to an application-defined delivery binding; the zero value means normal next-hop resolution. |
| `Attributes` | Extension state carried as string values. |

## Delivery binding

A target may carry a `proxy.DeliveryRef{Kind, Key}` naming an
application-defined delivery resource. The zero value selects ordinary
next-hop resolution and needs no resolver.

`Options.ResolveDelivery` binds a nonzero reference to a
`proxy.BoundDelivery{Transport, LocalAddr, RemoteAddr}` whose sender is
already bound to the required delivery semantics. A nonzero reference without
a configured resolver is an explicit failure: no ordinary send is attempted.
The binding cannot override the original request's SIPS constraint or the
outgoing secure-next-hop requirement. An insecure bound transport is rejected
before its sender runs, without ordinary delivery fallback or implicit
redial. These binding seams do not implement RFC 5626 Outbound.

## Proxy options

`proxy.Options` configures the proxy. The proxy owns an independent copy
of all values after `proxy.New` returns.

| Field | Meaning |
| --- | --- |
| `LocalIdentities` | Configured routing identities used for self-route recognition and Record-Route generation; not the transport's Via sent-by ownership list. |
| `ServedDomains` | Domains the proxy's location service is responsible for; requests addressed to a served domain consult the route target resolver. |
| `TimerC` | Bounds the lifetime of each proxied INVITE attempt. Zero selects a five-minute default; explicit values must be greater than three minutes. |
| `StatelessKey` | Application-supplied stable secret/namespace required for explicit stateless routes. |
| `DetectLoops` | Enables routing-fingerprint comparison across all locally owned usable Vias; foreign proxy markers alone are not loop evidence. |
| `RecordRouteURI` | Supplies the advertised Record-Route URI for a forwarded request; nil selects a configured local identity. |
| `ResolveDelivery` | Binds a custom delivery reference to an existing transport resource; nil accepts only ordinary next-hop delivery. |
| `ResolveStatelessDestination` | Resolves an application-guaranteed stable stateless destination for a target. |

### Local Via ownership and loop detection

Routing identities, transport advertised addresses, and branch markers have
different roles. `LocalIdentities` configures self-routing and Record-Route;
it does not establish ownership of an old Via. A syntactically valid Via
is locally owned only when this element has a transport for the protocol
recorded on that Via and that transport's `MatchSentBy` accepts its sent-by.
The returning request may arrive over a different transport; request-loop
ownership still uses the old Via's protocol.

With `DetectLoops` enabled, the proxy checks every owned Via with a usable
proxy-stateful or proxy-stateless branch and compares its embedded routing
fingerprint using that mark's normalization. An unchanged owned routing
fingerprint rejects a true loop with 482 (ACK remains response-free).
Owned Vias with only changed routing fingerprints remain allowed spirals.
A foreign Via or an earlier changed-route owned Via cannot hide a deeper
owned match.

Independent proxies share the branch format and may produce equal routing
fingerprints. A foreign proxy's Via is accepted as prior-hop routing data,
not treated as local-loop evidence. Neither a shared marker, a matching
fingerprint, nor these sent-by ownership checks authenticate the remote peer.

## Recovery

A stateful request's orchestration state can be checkpointed and restored
by the application. Recovery is explicit: a snapshot carries no storage
state, live sockets, transaction handles, or callbacks, and a restore
continues only what the application binds back.

### Capturing

`rq.Snapshot()` returns a detached, serializable
`proxy.RequestSnapshot` of one owned request — route name, mode,
upstream transaction key, target plan progress, attempt records,
response context, cancellation intent, and armed absolute deadlines.
It carries both the original request as admitted (the transaction-matching
identity) and the post-preprocess working request — the validated output of
`PreprocessRoute` plus deterministic local-route consumption that later
target groups and address failover continue from. Snapshot version 1 retains
these as independently owned values, including for a promoted request whose
named route is configured stateless.
`prx.LookupRequest(elm, srvKey)` locates the coordinator of an upstream
server transaction key inside that element's processing namespace — a nil
or unseen element reports no association — and `prx.BindSnapshotHandler(h)`
subscribes to future committed revisions; each handler receives an
independent clone and runs outside the coordinator locks.
Neither the event nor the snapshot payload serializes the element: the
processing element is context-only observation data that
`BindEventHandler` and `BindSnapshotHandler` callbacks read from the
delivered context through `sip.ElementFromContext`.

Transaction state is captured separately through the optional
`sip.ServerTransactionSnapshotter` and
`sip.ClientTransactionSnapshotter` contracts. A request whose upstream
transaction cannot snapshot — or whose opaque metadata is not
JSON-serializable — fails capture with
`proxy.ErrSnapshotUnsupported`; ordinary forwarding is unaffected.

The application owns checkpoint coordination: proxy and transaction
snapshots are independent captures, and ordering them into one consistent
durable checkpoint is the application's responsibility. Together they do
not form an atomic checkpoint with each other or with network effects,
and restoration cannot recreate exactly-once send effects or live
sockets.

### Restoring

On the recovery side the application recreates dormant transactions with
`sip.RestoreServerTransaction` and `sip.RestoreClientTransaction` and
supplies them — plus the processing element and every required delivery
flow — through `proxy.RestoreBindings`:

- `Element`: the processing element the restored request continues under.
  Required and authoritative: it overrides an element carried by the
  caller's context while the context's other values survive, and a nil
  element fails with `proxy.ErrMissingElement` before anything is
  staged — the context is never consulted for an element.
- `ServerTransaction`: the dormant upstream transaction whose key and
  initial request must match the snapshot.
- `ClientTransactions`: the dormant downstream transactions of live
  attempts, keyed by `sip.ClientTransactionKey`. Attempts already settled
  at capture need no binding; their recorded outcomes are restored
  instead.
- `Deliveries`: a `map[proxy.DeliveryRef]proxy.BoundDelivery`
  rebinding every nonzero delivery reference carried by an unfinished
  target to an existing bound transport and remote address. A missing
  required flow fails validation explicitly — it is never resolved or
  dialed implicitly.

Every runtime binding is explicit: transactions are matched to recorded
attempts by their keys but are never inferred or recreated from a key,
and the element comes only from `bindings.Element` — never from the
caller's context or a transaction key.

`prx.RestoreRequest(ctx, snap, bindings)` validates the snapshot and
every binding — version, route, plan coverage, transaction keys and
requests, both saved request messages' structure and method compatibility,
metadata on both saved request envelopes, deadlines, and cancellation and
outcome consistency — before claiming transaction ownership or installing
subscriptions, indexes, or timers. Missing or malformed saved request state
returns `proxy.ErrInvalidSnapshot`, preserving the message validation
cause; unsupported opaque metadata returns
`proxy.ErrSnapshotUnsupported` without silently dropping values.
Direct caller-supplied snapshots are checked too, even though capture
normally guarantees serializable metadata.

Only a stateful-mode record can restore: a
stateless-mode checkpoint has no owned coordinator and is rejected, and a
version-1 record without `working_request` is rejected without migration
or reconstruction. Valid version-1 snapshots are unchanged: the original
request remains the upstream transaction-matching identity, while the
saved working request is adopted as the coordinator's working copy, so
target preparation resumes from the post-preprocess envelope without
rerunning `PreprocessRoute` or local-route consumption.
The saved complete plan also avoids replaying authorization or logical
target resolution. Observation correlation and sequence progress resume
from the checkpoint without replaying historical handled, attempt,
forwarded, or completion observations; this is not a durable exactly-once
guarantee for application hooks or network effects.
A successful restore installs the coordinator, its event subscriptions,
and the upstream index entry dormant: no sends, no callbacks, no
snapshot revisions, and no timers run until activation. A failed install
rolls back only its own staged registrations; the supplied transactions
and any existing index entry are untouched.

### Activating

`rq.Start(ctx)` activates a dormant restored request exactly once; a
repeated call fails with `proxy.ErrRequestStarted`. The supplied
transactions start only after the subscriptions installed at restore are
in place, then the preserved state resumes: absolute Timer C deadlines
re-arm (an expired deadline applies the timeout policy immediately),
live targets rebuild their deferred address work, recorded cancellation
resumes only its permitted steps, and completed targets never rerun. A
request completed at capture is inspect-only: activation emits nothing.

Bind application observers before `Start` so no new revision is missed —
binding observes future revisions only and never replays the last one.
The `unbind` function returned by `BindSnapshotHandler` is idempotent and
safe to call reentrantly, including from inside the handler; its lifetime
belongs to the binding, not to any single request.

A restored non-INVITE request keeps the behavior mode saved in its
checkpoint — the saved mode wins over current route creation defaults,
and a binding reporting a different mode fails validation before
activation.

See `ExampleProxy_RestoreRequest` in
`sip/proxy/recovery_example_test.go` for the compile-checked composition.

## Message metadata

Every request or response copy the proxy processes carries a serializable
`proxy.Metadata` stamp under the `proxy.MetadataKey` (`"sip.proxy"`) key of
its `sip.MessageMetadata`. The stamp survives envelope cloning and JSON
serialization — snapshot checkpoints restore it — while never appearing
in the rendered SIP message.

`Metadata.Stage` names the processing boundary the stamp describes:

| Stage | Boundary |
| --- | --- |
| `proxy.StageClaim` (`"claim"`) | Route-claim phase of an inbound request: records the minted request identity, the claiming route, and the effective mode; target and attempt stay empty. |
| `proxy.StageForward` (`"forward"`) | One owned downstream request copy: carries the request identity plus the target and physical-attempt identities of that send. |
| `proxy.StageResponse` (`"response"`) | Downstream response processing: an attributed response carries the owning request, target, and attempt identities. |
| `proxy.StageUpstream` (`"upstream"`) | Upstream-facing processing of a request attempt: carries the request identity plus the single-attempt source only when one attempt provably owns the copy. |

The remaining fields correlate the stamp. `RequestID` is the proxy request
identity minted at claim; events, errors, and outcomes of the same request
join through the shared `RequestID`, and `Event.Sequence` orders the
request's events. `RelatedRequestID` links a matched CANCEL or a forwarded
ACK to the live parent request it relates to, recorded only when the
relationship is really known. `TargetID` and `AttemptID` name the logical
plan target and the concrete physical attempt; `RouteName` and `Mode`
record the claiming route and its effective forward mode.

Correlation fields are populated only when the stamped boundary actually
knows them — unknown fields stay empty rather than being invented. A local
or aggregated upstream response keeps target and attempt empty because no
single attempt owns it, and a stateless response without recoverable
provenance stamps only mode and stage: its JSON form is the partial
`{"mode":1,"stage":"response"}` record.

Serialization and access follow these rules:

- `Mode` serializes as the numeric `proxy.ForwardMode` (`0` stateful, `1`
  stateless): the field is a `uint`, and JSON keeps the numeric form. The
  readable `stateful`/`stateless` names are the `ForwardMode.String()`
  rendering used in logs, not the JSON contract — a decoded map carrying a
  string mode is rejected.
- `proxy.SetMetadata` stores the whole value: stamps are written and
  replaced atomically, never merged or patched per field. A message
  looping back through the proxy is stamped with the fresh traversal's
  claim record only — the stamp describes the current boundary, not a
  history of traversals.
- `proxy.MetadataFrom` accepts the stored `proxy.Metadata` or the
  `map[string]any` a JSON decode restores. It validates mode and stage,
  rejects malformed known fields, ignores unknown fields so newer
  producers stay readable, and never mutates the source map.
- `proxy.Metadata` implements `slog.LogValuer`: a correlation-aware proxy
  log record attaches the boundary's stamp once as `slog.Any("proxy", md)`,
  rendering a single `proxy` group — request, related, target, attempt,
  route, mode, and stage fields, with empty correlation fields omitted.
  It is never split into separate correlation attributes, and arbitrary
  application metadata stored on the envelope is never logged.

The stamp is local, non-authenticated observation data with explicit
non-goals: it is not cross-network transport (metadata lives on the
in-process envelope, never on the wire), not a wire identifier (Via branch
values and transaction keys remain the wire identities), not
authentication (a decoded value asserts only what the producing
application stored), and not routing authority (claim, loop, and Via
ownership checks never consult it).

See `ExampleMetadataFrom` in `sip/proxy/metadata_example_test.go`.

## Example

A minimal proxy with one route that claims every request addressed to a
served domain and resolves a static literal-IP target plan:

```go
elm := &sip.Element{}
defer elm.Close()

prx, err := proxy.New([]proxy.Route{
 {
  Name: "static-location",
  ResolveRequestTargets: func(ctx context.Context, req *sip.RequestEnvelope) (proxy.TargetPlan, error) {
   return proxy.TargetPlan{Groups: [][]proxy.Target{
    {{
     ID: "alice-desk",
     URI: &sip.URI{
      User: sip.MakeUserInfo("alice"),
      Addr: sip.MakeHostPortAddr("192.0.2.10", 5060),
     },
    }},
   }}, nil
  },
 },
}, proxy.Options{
 ServedDomains: []string{"example.com"},
})
if err != nil {
 // handle error
}

// Registration is explicit; retire the proxy before unbinding.
unbind := elm.UseMiddleware(prx)
defer func() {
 _ = prx.Close()
 unbind()
}()
```

See `sip/proxy/example_test.go` for the compile-checked version.
