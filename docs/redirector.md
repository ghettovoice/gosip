# SIP Redirector

The `github.com/ghettovoice/gosip/sip/redirector` package provides
`Redirector`, an element component for a `sip.Element` that claims inbound
requests matching configured routes and answers them: a redirection
response carrying the route's resolved contacts on success, or a failure
response. The core `sip` package does not depend on `sip/redirector`.

## Role boundary

A redirect server *answers* requests with redirection; it never forwards
them. The component's whole pipeline is:

```text
inbound request -> match -> authorize -> resolve contacts -> 3xx response
```

- **It does not follow remote redirects.** Acting on received 3xx
  contacts is client-side (UAC) policy; a redirect server produces
  redirection responses and has no opinion about how the recipient uses
  them. Likewise, a proxy recursing on a downstream 3xx is a different
  feature, not this component's job.
- **It is not "a proxy with sending disabled".** There are no downstream
  client branches at all: no target plans, no Via insertion on forwarded
  copies, no upstream response handling, no response-context ranking.
- **It claims only inside the inbound interceptor chain.** A route match
  consumes the request — it never reaches the next receiver. There is no
  explicit entry point for handing an already received request to the
  component.

A request the redirector does not claim — no matching route, an ACK, or
any request after the component is closed — continues down the receiver
chain unchanged. An entirely unclaimed request ends at the transport-level
final receiver, answered 503 Service Unavailable.

## Constructing a Redirector

```go
rdr, err := redirector.New(routes, opts)
unbind := elm.UseComponent(rdr)
```

- `routes` is a `[]redirector.Route` slice evaluated in order; the first
  matching route wins.
- `opts` is an optional variadic `redirector.Options` value: omitted, the
  zero options apply; when several values are supplied the last whole
  value wins — fields are not merged.

`redirector.New` validates and copies the routes: later changes to the
caller's slice do not affect the running redirector, and a failed
construction installs no components. Construction fails on a route
without a contact resolver or with a non-zero constant status outside
{300, 301, 302}.

The constructor binds no element: an explicit `elm.UseComponent(rdr)`
registration attaches the redirector to an element's processing chain,
and one instance may be registered on several elements at once. The
registration's `unbind` detaches the component from that element only —
it is never a shutdown path.

## Redirector lifecycle

`rdr.Close()` is one-shot and idempotent: concurrent and repeated calls
share the first call's effect. It seals claiming on every element the
instance serves — after Close, a request that would match a route is no
longer claimed and passes through unchanged.

The redirector owns no goroutines, subscriptions, or request state, so
Close only stores the seal. The owning element invokes Close as its
optional `io.Closer` component hook, so closing an element closes the
redirector too. Unbind itself never invokes Close; detach through the
`unbind` returned by `UseComponent`.

## Routes

A `redirector.Route` is an immutable routing rule owned by the redirector
after `redirector.New` returns.

| Field | Meaning |
| --- | --- |
| `Name` | Route identifier reported in errors and logs. |
| `MatchRequest` | Reports whether the route claims the inbound request. Nil accepts every request at the route's position. |
| `AuthorizeRequest` | Authorizes the inbound request before contact resolution. Nil allows the request; an error rejects it with 403 Forbidden and the resolver is not invoked. |
| `ResolveContacts` | Resolves the redirection contacts carried into the response Contact header. Required: `redirector.New` rejects a route without it. The returned contacts, including `q` and `expires` parameters, are carried verbatim in resolved order. |
| `SelectStatus` | Selects the response status from the request and the resolved contacts. Nil selects the constant `Status`. |
| `Status` | Constant redirect status used when `SelectStatus` is nil. The zero value means 302 Moved Temporarily; a non-zero value must be one of 300, 301, or 302. |

All hooks are optional except `ResolveContacts`; a nil hook selects the
documented default. Note that a nil `MatchRequest` matches *every* request
at the route's position, including CANCEL — scope broad predicates
narrowly or exclude CANCEL explicitly if that is not intended.

## Status policy

The supported redirect set is exactly three statuses, with their ordinary
RFC 3261 meanings:

- **300 Multiple Choices** — the resolved contacts are alternatives the
  client chooses among; natural when resolution produces several
  contacts, for example with different `q` values.
- **301 Moved Permanently** — the user is no longer reachable at the
  Request-URI; the contacts name the new address to record for future
  requests.
- **302 Moved Temporarily** — the contacts are valid for this attempt
  only; the Request-URI stays authoritative for later requests.

The default is the constant 302: a route with neither `Status` nor
`SelectStatus` answers 302, and an explicit `Status` must be one of the
three — anything else fails construction.

`SelectStatus` is the per-request alternative: it receives the request
and the resolved contacts and runs only when resolution produced at
least one contact — never for authorization failures, empty resolutions,
or resolver errors. A returned status outside {300, 301, 302} is an
invalid selection answered 500 Server Internal Error without a Contact
header. The component does not interpret the statuses further — the
client that follows the redirect owns that policy.

## Failure mapping

| Condition | Response |
| --- | --- |
| `AuthorizeRequest` returns an error | 403 Forbidden; the resolver is not invoked |
| `ResolveContacts` returns no contacts | 404 Not Found |
| `ResolveContacts` returns an error | 500 Server Internal Error |
| `SelectStatus` returns an unsupported status | 500 Server Internal Error |
| CANCEL matches a route | 481 Call/Transaction Does Not Exist |

Failure responses carry no Contact header and are sent through the same
stateful path as redirect responses. A claimed request dispatched without
a processing element in its context — which cannot happen inside a real
`UseComponent` chain, where the element adapter injects it — fails as an
ordinary interceptor error classified `sip.ErrClassElement` with the
`redirector.ErrMissingElement` sentinel.

## Transaction notes

Every response goes through `Element.RespondStateful`, which loads or
creates the request's server transaction and sends through it. To-tag
generation and RFC 3261 Section 18.2.2 response routing — including
RFC 3581 `rport` symmetric routing — come from the element's outbound
chain; the component contains no transaction logic of its own.
Consequences, all provided by the existing layers:

- **Retransmission absorption.** A retransmitted request matched to its
  live server transaction is absorbed by the transaction layer: the
  stored response is resent and no route callback runs again. After the
  transaction terminates on its standard timers, a further
  retransmission re-runs the pipeline — the same window any stateful
  element has — so application resolvers should be deterministic for a
  given request.
- **ACK for 3xx.** The ACK confirming a non-2xx final response to an
  INVITE is absorbed by the INVITE server transaction before components
  run. The redirector never claims an ACK: a route-matched ACK passes
  through to the next receiver and is never answered by a redirection
  response.
- **Late CANCEL.** The redirector answers synchronously and never leaves
  a request pending, so every CANCEL matching a route is late by
  definition: the original request already has a final response. A
  claimed CANCEL is answered 481 on its own server transaction — without
  resolving contacts — so the transaction layer absorbs the CANCEL's
  retransmissions the same way. A CANCEL no route matches passes through
  unclaimed.

## Redirector options

`redirector.Options` configures the redirector.

| Field | Meaning |
| --- | --- |
| `Logger` | Fallback logger used when the processing context carries none; nil selects the package default. The processing element's logger from the request context wins over it. |

## Multiple elements

One Redirector instance may be registered on several elements. The
component keeps no per-request or per-element state — only the copied
route slice, the fallback logger, and the closed flag — and resolves the
processing element per request from the interception context through
`sip.ElementFromContext`. A request is always answered through the
element that received it, and `Close` seals claiming on every element at
once.

## Example

A minimal redirector with one route that claims every request and
resolves a single static contact — the same construction the runnable
`ExampleNew` in `sip/redirector/example_test.go` registers on an element
with a loopback UDP transport and listener before answering a raw
INVITE:

```go
rdr, err := redirector.New([]redirector.Route{
    {
        Name: "static-location",
        // A nil matcher accepts every request at this route's
        // position, so the route resolves one fixed contact for
        // whatever it claims.
        ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
            return header.Contact{
                {
                    URI: &sip.URI{
                        User: sip.MakeUserInfo("alice"),
                        Addr: sip.MakeHostPortAddr("192.0.2.10", 5060),
                    },
                },
            }, nil
        },
    },
})
if err != nil {
    fmt.Println("error:", err)
    return
}

unbind := elm.UseComponent(rdr)
defer func() {
    _ = rdr.Close()
    unbind()
}()
```

See `sip/redirector/example_test.go` for the complete compile-checked
version, including the element wiring and the wire exchange: it prints
`302`.
