# SIP User Agent

The `github.com/ghettovoice/gosip/sip/useragent` package provides
`UserAgent`, an element component for a `sip.Element` that adds the
endpoint roles: outgoing request operations (the UAC side) and incoming
request handling (the UAS side). The core `sip` package does not depend
on `sip/useragent`.

This slice covers the operation skeleton: MESSAGE and OPTIONS
operations, inbound MESSAGE/OPTIONS handling, authentication retry, and
bounded redirect following. INVITE, dialogs, sessions, registration,
and offer/answer negotiation are explicit non-goals — they belong to
later slices of the upstream design in
[useragent-design.md](useragent-design.md), which is a proposal, not a
description of the shipped surface.

## Role boundary

A user agent is an *endpoint*: it starts request exchanges of its own
and answers requests addressed to it. It never forwards — routing,
branching, and record-routing belong to `sip/proxy`, and answering with
redirection belongs to `sip/redirector`.

```text
outgoing: Message/Options -> operation -> send -> auth retry / redirect -> outcome
incoming: inbound request -> claim -> server transaction -> handler respond
```

- **Operations are per-request exchanges, not sessions.** An operation
  resolves with the first final response (after any authentication
  retries and redirect hops); nothing persists dialog state.
- **Claiming stays inside the inbound interceptor chain.** A claimed
  request is dispatched to a handler through a per-request server
  transaction; an unclaimed request passes to the next interceptor
  unchanged and may end at the transport-level final receiver.
- **Cancellation is local.** All operations in this slice are non-INVITE,
  and a canceled operation context ends the exchange with a canceled
  outcome — it never becomes a wire CANCEL, because there is none to
  send for these methods.

## Constructing a UserAgent

```go
ua, err := useragent.New(useragent.Options{
    AOR:       aor,
    OnMessage: onMessage,
})
unbind := elm.UseComponent(ua)
```

`useragent.New` accepts optional variadic `useragent.Options` values:
omitted, the zero options apply; when several values are supplied the
last whole value wins — fields are not merged. The constructor validates
and copies the options: later changes to the caller's URIs or redirect
policy do not affect the running user agent, and the constructor binds
no element.

`useragent.Options` configures the user agent.

| Field | Meaning |
| --- | --- |
| `AOR` | Local identity URI carried into outgoing requests. Required; must be a valid SIP URI. |
| `Contact` | URI carried in the Contact header of outgoing OPTIONS requests — the only place a contact is stamped; a MESSAGE request never carries Contact and inbound-request responses carry none. Nil emits no Contact header. |
| `Authenticator` | `sip.ClientAuthenticator` answering 401/407 challenges. Nil disables authentication retry: a challenge response is the final outcome. |
| `RedirectPolicy` | `*useragent.RedirectPolicy` bounding automatic 3xx following. Nil disables redirect following: a redirect response is the final outcome. |
| `MaxAuthAttempts` | Bound on authentication retries per operation. Zero selects the default of 3; negative is invalid. |
| `DisableOptionsAutoAnswer` | Turns off the automatic 200 capabilities answer to inbound OPTIONS requests no handler claims. |
| `OnMessage` | `useragent.RequestHandler` for inbound MESSAGE requests. Nil means MESSAGE is not claimed and passes to the next interceptor. |
| `OnOptions` | `useragent.RequestHandler` for inbound OPTIONS, overriding the auto-answer. Nil falls back to the auto-answer unless it is disabled. |
| `Logger` | Logger handed to the exchange's client transactions; nil lets each transaction inherit the element's logger. |

`useragent.RedirectPolicy` bounds redirect following by an operation.
Every limit is explicit: a chain never exceeds the configured depth,
total sends, or shared deadline.

| Field | Meaning |
| --- | --- |
| `MaxDepth` | Bound on followed redirect hops. Zero selects the default of 5; negative is invalid. |
| `MaxAttempts` | Bound on total logical sends — the initial request, every authentication retry, and every redirect hop. Zero selects the default of 10; negative is invalid. Element DNS failover inside one send never counts. |
| `Timeout` | Single shared deadline bounding the whole chain, created once at operation entry and never refreshed between hops. Zero selects the default of 32 s; negative is invalid. |
| `AllowTarget` | Decides whether a candidate target may be followed. Required: `New` rejects a policy without it. It runs before any send to the candidate and before any credential computation for it, and receives detached clones. |

## Element binding

`elm.UseComponent(ua)` returns an unbind `func()`, not an error. The
registration stays application-owned: calling the unbind detaches the
component from that one element and is never a shutdown path. `UseComponent` never
returns nil: a registration rejected because the element is already
closing yields a non-nil no-op unbind — the component stays
caller-owned and is never registered.

Outgoing operations resolve their sending element from the operation
context, not from a fixed field:

```go
op, err := ua.Message(sip.ContextWithElement(ctx, elm), target)
```

An operation context without an element fails synchronously with
`useragent.ErrMissingElement`. One `UserAgent` instance may serve
several elements at once: each operation runs on the element its
context carries, and each inbound claim dispatches on the element that
received the request.

Close semantics follow the shared instance. `ua.Shutdown(ctx)` seals the
lifecycle, cancels every admitted operation, and drains the handler and
operation workers — completion callbacks included — until they finish
or the context is done. `ua.Close()` is the non-graceful seal: it
cancels in-flight operations and returns without the drain. Both are
idempotent and instance-wide. Closing an attached element invokes these
component hooks, so shutting down one element seals the shared user
agent and cancels its operations while the other elements sharing the
instance stay open. Both hooks cancel and release retained inbound
responders and detach their transaction observers. Neither hook closes the
borrowed elements or calls the unbind the application holds.

## Operations

```go
op, err := ua.Message(ctx, target, useragent.MessageOperationOptions{Body: []byte("ping")})
res, err := op.Wait(waitCtx)
```

`ua.Message` and `ua.Options` start an exchange toward `target` and
return a handle — `*MessageOperation` or `*OptionsOperation` — whose embedded
operation exposes `Wait`, `Cancel`, and `Done`. Options is parameterless
beyond the shared `OperationOptions`; `MessageOperationOptions` adds `Body`
(copied before the operation starts and sent verbatim) and
`ContentType` (validated as a MIME media type, `text/plain` by default).
The target scheme is not restricted: non-SIP URIs are left to the
element's URI converter for routing.

The synchronous gate runs before any worker starts: a done context
reports `ErrCanceled`, a missing element `ErrMissingElement`, a
zero-value or sealed user agent `ErrClosed`, and a nil, typed-nil, or
invalid target — or a malformed content type — `ErrInvalidRequest`.

- `Wait(waitCtx)` blocks until the operation publishes its terminal
  outcome or `waitCtx` is done, whichever comes first. A waiter's
  deadline fails only that waiter — it never cancels the operation —
  and a publication simultaneous with the waiter error still wins.
  Repeated and concurrent `Wait` calls resolve the same outcome through
  independent response clones.
- `Cancel` requests the operation to stop; it is idempotent and never
  blocks. Cancellation observed before the outcome commits produces the
  canceled outcome; a cancel arriving after publication changes nothing.
- `Done` closes when the terminal outcome is published — for every
  outcome, including cancellation — so it can drive a `select`.

`OperationOptions` binds callbacks at operation creation, so there is no
subscribe-after-start race.

| Field | Meaning |
| --- | --- |
| `Timing` | `sip.TimingConfig` for the exchange's transactions. |
| `OnProgress` | Reports provisional responses. |
| `OnComplete` | Reports the terminal outcome. |

- `OnProgress` receives only `101`–`199` responses — a bare `100 Trying`
  is never delivered — each as a deep clone, and it is suppressed once
  the operation context is done. The callback runs inside the send path:
  do not call `Wait` on the same operation (the outcome cannot publish
  while the callback blocks the send) and do not call `Shutdown` on the
  user agent (it drains in-flight callbacks and would wait on itself).
  Calling `Cancel` or `Close` inside a callback is allowed — `Close`
  seals and returns without draining.
- `OnComplete` fires exactly once for every terminal outcome — success,
  failure, or cancellation — after the outcome is published, so the
  callback may safely call `Wait` or observe `Done`.

## Outcomes and errors

An ordinary final response — success *or failure* — resolves the
operation with that response and a nil error: a 404 produced by the peer
is an outcome, not a Go error. `Wait` resolves an error for send-path
failures (a transport error, or the 503 that ends the element's
candidate confirmation), authentication-limit and redirect failures,
cancellation, and shutdown. A canceled operation reports an
`ErrCanceled` error that also matches `context.Canceled` or
`context.DeadlineExceeded` according to its cause.

Classify errors — never string-match. Every user agent error carries
`useragent.ErrClass` (`"useragent"`), so `useragent.IsError` recognizes
the domain; `errors.Is` matches the sentinels; and the `pkg/errclass`
predicates (`IsClosed`, `IsCanceled`, `IsTimeout`, `IsConflict`) match
the broader categories through wrapped and joined errors.

| Sentinel | Condition |
| --- | --- |
| `ErrInvalidConfig` | `New` options failed validation. |
| `ErrCanceled` | Operation context canceled or deadline exceeded; classified canceled or, for a deadline cause, timeout. |
| `ErrAuthenticationLimited` | A 401/407 challenge chain reached `MaxAuthAttempts`. |
| `ErrRedirectLoop` | A redirect chain returned to an already visited target. |
| `ErrClosed` | Operation attempted on a sealed user agent, or `Respond` after shutdown; classified `errclass.ClassClosed`. |
| `ErrMissingElement` | Operation context carries no processing element. |
| `ErrInvalidRequest` | Request or response failed validation before any send. |
| `ErrResponseFinal` | A second final response attempted on an answered inbound request; classified `errclass.ClassConflict`. |
| `ErrCSeqExhausted` | A retry could not allocate a fresh CSeq: the sequence reached its maximum value. |
| `ErrNoFinalResponse` | The exchange produced no final response. |

## Authentication retry

With a configured `Authenticator`, a 401/407 final response starts a
retry chain instead of resolving the operation:

- Each retry is a fresh send: the retried request is a clone of the
  actually sent request with the CSeq sequence number incremented and a
  fresh RFC 3261 top-Via branch — a new transaction — while the Call-ID,
  From tag, and To identity stay stable.
- The authenticator mutates only the retry clone and observes the
  challenge as a detached copy, so the transaction's stored request is
  never altered.
- The retry budget is operation-wide: `MaxAuthAttempts` counts all
  retries of the operation, and a redirect chain shares the same bound —
  plus the policy's shared `MaxAttempts` send budget, which gates
  retries exactly like hops.
- Exhausting the attempts bound fails the operation with
  `ErrAuthenticationLimited`; the outcome still carries the last
  challenge response.
- A challenge with no authenticator configured resolves as an ordinary
  final response.

## Redirect following

With a configured `RedirectPolicy`, a 300, 301, or 302 final response
starts a depth-first chain over its surviving Contact header addresses;
any other 3xx resolves the response directly.

- Candidate extraction drops wildcard, non-SIP/SIPS, invalid, and
  expired contacts, orders the survivors by `q` value (stable within
  equal values; a malformed or out-of-range `q` discards the contact),
  and cleans each contact to a request URI. A secured operation — one
  whose target is `sips` — never follows a downgrade to plain `sip`.
- `AllowTarget` gates every candidate: it runs before any send to the
  candidate and before any credential computation for it, and it
  receives detached clones so a callback cannot alter the candidate or
  the sent request.
- Credentials never cross targets: a retargeted retry replaces the
  request URI and deletes the Authorization and Proxy-Authorization
  families, so a new target's first send is always unauthenticated and a
  fresh challenge starts a new retry leg under the same budgets.
- A visited-target set detects loops and fails the chain with
  `ErrRedirectLoop`. A chain stopped by the send budget, the depth
  bound, a denied gate, or a redirect without approved candidates
  resolves with the most recent redirect response — never a synthetic
  error.
- A non-3xx final failure falls back to the redirect's pending sibling
  contacts; a 6xx global failure ends the chain.

## Inbound handling

The user agent claims requests inside the element's inbound interceptor
chain: MESSAGE when `OnMessage` is configured, OPTIONS when `OnOptions`
is configured or — when it is nil and the auto-answer is not disabled —
through the automatic capabilities answer. Every other request passes to
the next interceptor unchanged, and an entirely unclaimed request ends
at the transport-level final receiver answered 503.

A claimed request is dispatched to the handler as an `IncomingMessage`
bound to a freshly registered server transaction, so retransmissions are
absorbed by the transaction match and never reach the handler twice.

- `in.Request()` returns an immutable snapshot of the inbound request.
- `in.Respond(ctx, status, opts...)` sends through the request's server
  transaction. A provisional (1xx) response does not commit the
  responder; a final (2xx–6xx) response commits it once its send
  succeeded, and later calls fail with `ErrResponseFinal`. A failed send
  leaves the gate open, and a sealed user agent reports `ErrClosed`,
  which outranks even a committed final.
- The responder may be retained: the handler can return without
  answering and respond later — the handle stays live until a final
  response commits it, the transaction terminates, or the user agent
  shuts down.
- A handler error is reported as a 500 rejection that the transaction
  manager's deferred responder answers once through the registered
  server transaction; a `*sip.RequestRejectedError` answers through the
  same path with its own status and respond options — send options
  included.
- The OPTIONS auto-answer returns 200 advertising the user agent's *own*
  configured capabilities — the element's aggregated set is never
  leaked — and sets Allow, Supported, and Accept only when non-empty.
  `OnOptions` overrides the auto-answer entirely, and
  `DisableOptionsAutoAnswer` turns it off.

## Protocol limits

- A MESSAGE request never carries a Contact header; that is a package
  contract, not an RFC 3428 prohibition.
- A successful response to a MESSAGE request carries no body and no
  Contact header; `Respond` enforces this with `ErrInvalidRequest`
  before anything is sent.
- An OPTIONS request carries a Contact header only when a contact is
  configured.
- Operation cancellation is local: for the non-INVITE methods of this
  slice it never turns into a wire CANCEL — there is none to send.

## Example

The runnable `ExampleUserAgent_Message` in
`sip/useragent/example_test.go` wires two real elements on loopback
UDP — the same track-transport/listen/serve shape a production
application uses — binds a user agent on each, and sends a MESSAGE from
one to the other; the peer's `OnMessage` handler answers `200 OK` and
the example prints the resolved status:

```go
op, err := uaAlice.Message(
    sip.ContextWithElement(opCtx, elmAlice),
    target,
    useragent.MessageOperationOptions{Body: []byte("ping")},
)
if err != nil {
    fmt.Println("error:", err)
    return
}
res, err := op.Wait(opCtx)
if err != nil {
    fmt.Println("error:", err)
    return
}
fmt.Println(int(res.Status()))
```

See the example source for the complete compile-checked version,
including element construction, cleanup, and the `// Output: 200`
assertion.
