# Design

## Context

See [proposal.md](proposal.md) for motivation and scope.

The current stack already provides every primitive the component needs:

- Inbound request interceptor chain `txm -> element -> components -> final receiver`; a component claims a request by not calling `next`. Unclaimed requests end at the transport-level final receiver with a 503.
- `TransactionManager.InterceptInboundRequest` absorbs retransmissions and non-2xx ACKs through live server transactions before any component runs, and answers `*sip.RequestRejectedError` statefully when a downstream receiver created a transaction.
- `Element.RespondStateful` creates or loads the server transaction for a request and sends the response through it; To-tag generation and RFC 3261 §18.2.2 response routing are handled by the element's outbound chain and the transaction.
- `header.Contact` (`ContactAddr = NameAddr`) carries `q` and `expires` as URI parameters.
- `sip/proxy` is the inbound-component precedent: `Proxy.InterceptInboundRequest` claims route-matched requests and dispatches CANCEL/ACK before its pipeline.

Agreed with the maintainer during design discussion: status selection is a per-route callback that also receives the resolved contacts, with a constant-status default of 302; policy failures map to 403/404/500; matched CANCELs are answered 481; ACK is never claimed. `docs/future-architecture.md` section 4 records the role boundary: a redirect server answers with redirection, it does not follow remote redirects and is not a proxy with sending disabled.

## Goals / Non-Goals

**Goals:**

- A small independent component built only from public `sip` primitives, per the framework principle that built-ins get no privileged access.
- Zero component-level transaction logic: no retransmission handling, no ACK/CANCEL transaction machinery, no timers.
- One stateless instance usable on several concurrently running elements.
- Behavior parity across the middleware path only — there is no explicit second entry point.

**Non-Goals:**

- Following remote 3xx contacts (UAC policy), proxy recursion on 3xx, or any downstream client branch.
- A location-service component or shared lookup contract; the resolver callback is the only contact source.
- 305/380 statuses, contact selection or sorting beyond verbatim passthrough, observation events, snapshots, or recovery.
- Any change to `sip` core, `sip/proxy`, or the transaction layer.

## Decisions

### 1. Package and component shape

New package `sip/redirector` with:

```go
type Redirector struct { /* routes, logger, closed flag */ }

func New(routes []Route, opts ...Options) (*Redirector, error)
```

`Redirector` implements `sip.InboundRequestInterceptor` for claiming and `io.Closer` for lifecycle. It does not implement `Capabilities` (a redirect server advertises no new methods or option tags) and has no `Shutdown` hook (no in-flight state to drain; `Element.Close` invokes the closer after unbinding). `New` validates and copies configuration and binds nothing; registration is the explicit `elm.UseComponent(rdr)` whose unbind the application keeps. `Close` is one-shot and seals claiming: after it, every request passes through unchanged, mirroring the sealed-proxy behavior.

Options follow the `proxy.New` convention: `opts` is variadic, an omitted value selects the zero `Options`, and when several values are supplied the last whole value wins — option fields are not merged.

**Alternatives:** A redirect mode inside `sip/proxy` is rejected by the recorded role boundary ("not a proxy with sending disabled"). Library helpers without an interceptor lose claiming and transactional correctness. An explicit `RedirectRequest`-style entry is not obligatory per the future-architecture discussion and is omitted until a consumer needs orchestration outside the chain.

### 2. Route model

```go
type Route struct {
    Name string
    // MatchRequest reports whether the route claims the request.
    // A nil matcher accepts every request at the route's position;
    // the first matching route wins.
    MatchRequest func(context.Context, *sip.RequestEnvelope) bool
    // AuthorizeRequest authorizes the inbound request before contact
    // resolution. A nil authorizer allows the request; any error
    // produces the 403 mapping.
    AuthorizeRequest func(context.Context, *sip.RequestEnvelope) error
    // ResolveContacts resolves the redirection contacts for the response.
    // Required; the returned contacts are carried verbatim into the
    // response Contact header, including q and expires parameters.
    ResolveContacts func(context.Context, *sip.RequestEnvelope) (header.Contact, error)
    // SelectStatus selects the response status from the request and the
    // resolved contacts. A nil selector uses the constant Status.
    SelectStatus func(context.Context, *sip.RequestEnvelope, header.Contact) sip.ResponseStatus
    // Status is the constant status used when SelectStatus is nil.
    // The zero value is 302.
    Status sip.ResponseStatus
}
```

Field naming follows `proxy.Route` (`Name`, `MatchRequest`, `AuthorizeRequest`); `AuthorizeRequest` replaces the discussed `Authorize` for consistency with the proxy hook. `Options` carries a `Logger *slog.Logger` used as the fallback logger; the processing element's logger from the interception context wins, as in the proxy.

`New` rejects a route without `ResolveContacts` or with a constant `Status` outside {300, 301, 302}, and copies the route slice so later application-side mutation has no effect.

**Alternatives:** A `ContactResolver` interface would add a type without a second implementation; function fields match the repo's callback conventions. Selecting the status before resolution would make count-based 300-versus-302 decisions impossible, which is the main reason the callback receives the contacts.

### 3. Pipeline and consumption semantics

`InterceptInboundRequest` runs, in order:

1. **Closed** — pass to `next`.
2. **Route match** — first matching route wins; no match passes to `next`.
3. **ACK** — pass to `next`; never claimed. ACKs for the 3xx finals are absorbed by the transaction layer before components run (txm is outermost), and `Request.NewResponse` itself rejects ACK, so claiming would be incorrect on both layers.
4. **CANCEL** — claim and answer 481 through `RespondStateful` (see decision 5).
5. **AuthorizeRequest** — an error maps to 403.
6. **ResolveContacts** — empty result maps to 404; an error maps to 500.
7. **SelectStatus** (callback or constant) — an unsupported status maps to 500.
8. **Respond** — `Element.RespondStateful` with the Contact header in `RespondStatefulOptions.ResponseOptions.Headers`.

The element comes from `sip.ElementFromContext(ctx)`; its absence is an ordinary interceptor error (the element adapter always injects it in real chains). A successfully sent response consumes the request (return nil). A response-send failure is returned as an ordinary interceptor error for the stack's existing handling; the component logs it at warn level and does not retry.

**Alternatives:** Returning `*sip.RequestRejectedError` and letting the txm deferred handler respond requires creating the server transaction separately first and splits the response path in two; responding directly through `RespondStateful` keeps one uniform stateful path for redirect and failure responses.

### 4. Status policy

The supported set is {300, 301, 302} with distinct documented semantics (300 multiple choices, 301 permanent, 302 temporary). The default is 302. The constant is validated at construction; a callback result is validated at runtime and maps to 500, because a callback can depend on request data. The component does not interpret the statuses further — the client that follows the redirect owns that policy.

### 5. CANCEL and ACK

The redirector answers synchronously and never leaves a request pending, so every CANCEL reaching it for a matched route is late by definition: the original transaction has already produced a final response. It claims such CANCELs and answers 481 (RFC 3261 §9.2, analogous to the stateful-proxy unmatched-CANCEL rule in §16.10), sent through `RespondStateful` so the CANCEL's own server transaction absorbs its retransmissions. CANCEL carries the same Request-URI as the request it cancels, so ordinary route predicates match it without CANCEL-specific matching. No answered-request tracking exists or is needed.

ACK is never claimed (decision 3). Both choices keep the component stateless.

### 6. Response construction and transaction reuse

All responses go through `Element.RespondStateful`, which loads or creates the server transaction for the request and sends through it. Consequences, all provided by existing layers: retransmissions of the original request are absorbed by the transaction's cached response without re-invoking route callbacks; To-tag is ensured by the element's outbound response interceptor; response destination follows RFC 3261 §18.2.2 symmetric routing. After the transaction terminates (per its standard timers), a further retransmission would re-run the pipeline; this is the standard stateful-element window and matches proxy behavior. Application resolvers should be deterministic for a given request.

### 7. Failure mapping

| Condition | Response |
| --- | --- |
| AuthorizeRequest error | 403 |
| Resolver returns no contacts | 404 |
| Resolver operational error | 500 |
| Unsupported selected status | 500 |

All are sent through the same `RespondStateful` path and consume the request. Authorization failure skips contact resolution entirely.

### 8. Lifecycle and multi-element service

State is only the immutable route slice, the fallback logger, and an atomic closed flag. No goroutines, subscriptions, per-element maps, or request state. `Close` is a one-shot `io.Closer` invoked by `Element.Close`; unbinding happens through the `UseComponent` unbind the application holds. One instance on several elements resolves the processing element per request from the context, so responses always leave through the receiving element.

## Risks / Trade-offs

- [Match-all routes claim every CANCEL on the element and answer 481] -> Document that broad predicates should exclude CANCEL or scope narrowly; this claiming property is the same one the proxy has and belongs to route design.
- [Retransmission after transaction termination re-runs resolution and may produce different contacts] -> Standard SIP timer window, identical to the proxy; documented expectation that resolvers are deterministic per request.
- [SelectStatus couples status choice to resolved contacts] -> Deliberate: enables 300-on-multiple selection; documented as the callback contract.
- [Response-send failure produces an interceptor error and no SIP response] -> Matches existing stack conventions (transport logs and discards); the component adds a warn log and never retries.

## Migration Plan

New package only; no existing behavior changes. Rollback is unbinding the component and removing the package.

## Open Questions

- Whether `sip/redirector` needs its own error-domain class (like `proxy.ErrClass`) for errclass consumers. Deferred until a consumer needs classification; current errors use ordinary classified construction.
