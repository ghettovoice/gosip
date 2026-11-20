# Design

## Context

The approved UserAgent design (`docs/useragent-design.md`) defines the UA in
five vertical slices and leaves this change with slice one: the operation
skeleton. Everything below implements phase 1 of that document; its decisions
about session semantics, dialog decomposition, and phasing are not restated
here.

Existing seams this design builds on:

- Component model: `Element.UseComponent` with optional interfaces
  (`MessageInterceptor`, `Capabilities()`, `Shutdown(ctx)`, `io.Closer`);
  `sip/redirector` is the reference for claim-and-respond components.
- Send path: `Element.SendRequestStateful` resolves RFC 3263 candidates,
  waits for each candidate's final outcome, and returns the live client
  transaction; the final response is available via `ClientTransaction.LastResponse()`.
  DNS/transport failover stays inside the element and is not a logical retry.
- Authentication: `sip.ClientAuthenticator.AuthenticateRequest(ctx, req, res)`
  mutates `req` with credentials answering the challenge in `res`; the caller
  owns correlation and retries (the op layer is that owner).
- Endpoint message preparation: the element stamps From tag, Via, and
  Max-Forwards on unmarked requests; the UA must not mark its requests as
  forwarded.

## Goals / Non-Goals

**Goals:**

- An operation engine that MESSAGE and OPTIONS exercise now and INVITE
  extends later without redesign (op handles, creation-time callbacks,
  `Wait`/`Cancel`, single resolution).
- Retry chaining owned by the op layer: authentication and redirect policies
  as replaceable, optional pieces.
- A component that follows the redirector's construction/claim/respond
  pattern and the repo's option-struct conventions.

**Non-Goals:**

- Dialogs, INVITE, sessions, offer/answer, registration, SIP extensions
  (later slices of the upstream design).
- Any change to core `sip`, `sip/auth`, or other components.
- Live attach/detach while operations are in flight.

## Decisions

### D1: Package layout and public surface

One package, `sip/useragent`, split by file role: component (type
`UserAgent`, config `Options`), operation engine (op state machine and
registry), per-method operations and their `*OperationOptions` structs, incoming
handlers (`IncomingMessage` with responder control), retry policies
(authentication, redirect), and errors.

Public naming follows the repo conventions (`*Options` suffix, full words):

```go
ua, err := useragent.New(useragent.Options{
    AOR:            aor,
    Contact:        contact,
    Authenticator:  auth,    // optional sip.ClientAuthenticator
    RedirectPolicy: policy,  // optional
    OnMessage:      handler, // optional
    OnOptions:      handler, // optional
})
elm.UseComponent(ua)

op, err := ua.Message(ctx, target, useragent.MessageOperationOptions{
    Body:        body,
    ContentType: "text/plain",
})
res, err := op.Wait(ctx)
```

The component config type is `Options`; per-operation option structs use the
`*OperationOptions` suffix (`MessageOperationOptions`, `OptionsOperationOptions`) to avoid the
`Options.Options` collision. Operation handles are `*MessageOperation`,
`*OptionsOperation` with `Wait`, `Cancel`, and `Done`-style access.

### D2: Operation engine

Each operation runs a small state machine on its own goroutine, started by
the operation constructor: `build request → send → classify final response →
retry or resolve`. The outcome is stored exactly once; `Wait` observes it and
returns the same result on repeated calls. The UA keeps an op registry
(behind a mutex) so shutdown can cancel in-flight operations and drain.

Callbacks live in the operation options at creation, so there is no
subscribe-after-start race. Progress callbacks (for later INVITE use) are
part of the engine shape now, exercised only by the final/progress events
MESSAGE and OPTIONS produce.

Alternatives considered: running the state machine lazily inside `Wait`
(rejected: ops would have two owners and cancel-on-context-drop would be
harder to guarantee); channels-only API (rejected: no convenience path, and
the repo prefers handler binding).

### D3: Send integration and retry request identity

The engine sends through `Element.SendRequestStateful` and reads
`ClientTransaction.LastResponse()` for classification. On each logical retry
(auth or redirect), the UA clones the sent request envelope, increments CSeq,
sets a fresh Via branch, and re-sends through the same path. Call-ID and
From tag stay stable so retries belong to one logical exchange. The element
owns RFC 3263 failover for 503; if it exhausts the candidates, the engine
propagates the send error. The op context cancels the whole chain:
`SendRequestStateful` and the engine loop observe it, and non-INVITE methods
never emit CANCEL.

### D4: Authentication retry

On a 401/407 final response with an authenticator configured, the engine
applies `AuthenticateRequest(ctx, retryReq, challengeRes)` to the cloned
request and resends. Attempts are bounded by a configurable limit; repeated
challenges beyond it fail with an authentication-limited error. Without an
authenticator the challenge response is the final outcome. The engine never
lets the authenticator own the request: it always hands it a fresh retry
clone.

### D5: Redirect policy

Redirect following is a policy object with explicit limits: maximum depth and
total attempts, a visited-target set for loop detection, a shared deadline,
and an allowed-targets check. On a 300, 301, or 302 final response the policy
extracts the next contact, validates it against every limit, and the engine
rewrites the request URI on a retry clone. Credentials are recomputed only
after the new target passes the policy. Other 3xx responses, or any 3xx
without a configured policy, resolve without a redirect retry.

### D6: Incoming handling

The component implements the inbound request interceptor. It claims a request
when a handler is registered for the method, or for OPTIONS when auto-answer
is enabled and no handler is registered. On claim it creates a server
transaction via `Element.NewServerTransaction` and hands the application an
`IncomingMessage` carrying request plus responder control, following the
redirector's transaction/respond pattern; retransmissions are absorbed by the
server transaction. Unclaimed requests pass to the next interceptor.
OPTIONS auto-answer responds 200 with the advertised method capabilities.
Shutdown and Close release retained responders and their transaction
observers without waiting for remote peers or closing the element.

### D7: Capabilities and errors

`Capabilities()` advertises exactly the methods the UA is configured to
handle (MESSAGE with an `OnMessage` handler, OPTIONS with auto-answer or an
`OnOptions` handler). Errors follow the project guide: a `useragent.ErrClass`
domain class, `errors.Error` sentinels for operation outcomes
(authentication-limited, redirect-loop, operation canceled), classified with
`errclass` common classes so callers can inspect conditions without message
matching.

### D8: Testing

Black-box tests in `useragent_test` use loopback UDP elements set up by
`siptest.NewLoopbackUDPElement`, shared with redirector tests. Table-driven
cases cover claim rules, outcomes, auth retry chains, and redirect limits.
Transaction `TimingConfig` provides deterministic timing, and the standard
`task test` gate runs with the race detector.

## Risks / Trade-offs

- [Engine depends on `SendRequestStateful` waiting semantics] → the engine
  treats the element API as its only seam and integration tests pin the
  behavior; if element confirmation changes, op classification still reads
  `LastResponse()` and the tx error state, not internals.
- [OPTIONS auto-answer can conflict with a coexisting proxy on the same
  element] → claim rules are documented; component registration order decides
  conflicts exactly as it does today for proxy versus redirector.
- [Op goroutine lifecycle leaks on misuse] → the registry drains on
  shutdown, `Wait`/context cancellation always releases, and race tests
  cover the cancel-shutdown paths.
- [Retry identity mistakes (CSeq, branch, tags) are protocol-visible] →
  table-driven tests assert wire-level request properties across retries.

## Migration Plan

No production code in existing components changes, so there are no
consumers to migrate. The redirector integration tests use the shared
loopback UDP fixture described in D8. To roll back the change, remove the
user-agent package and its guide (`docs/useragent.md`) and revert the
redirector test fixture change and shared helper.

## Open Questions

- Default values for the auth attempt limit and redirect depth/attempt caps
  (spec requires configurability and bounds only; defaults tuned during
  implementation).
- Whether `Wait` also exposes the provisional responses it observed or only
  the final (deferred to INVITE, where provisionals matter; no change to
  this slice either way).
