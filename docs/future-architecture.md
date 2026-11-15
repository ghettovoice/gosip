# Future Architecture Directions

This document records an architecture discussion about where gosip could go
next: desired directions, proposed boundaries, and open questions. It is
**not** a specification or a finalized design. Nothing here commits to
package names, type or function signatures, component ordering, or an
implementation plan.

For the architecture as it exists today see
[architecture.md](architecture.md); the proxy component has its own guide
in [proxy.md](proxy.md) and the authentication contracts in
[authentication.md](authentication.md).

Status of the pieces discussed below:

- **Exists today**: `sip/proxy`, `sip/stats`, `sip/auth`, and the
  `sip.ClientAuthenticator` / `sip.ServerAuthenticator[P]` contracts.
- **Future work**: `UserAgent`, `Redirector`, `Registrar`, and an explicit
  forwarding entry on the proxy (`ForwardRequest`). None of these are
  implemented.
- `Registrar` is recorded only as an unexplored future feature; it has not
  been designed or discussed beyond its name.
- **Agreed framework direction**: gosip is intended to be a composable SIP
  framework, not a fixed stack. Public integration contracts should let
  end users supply their own implementations, including from external Go
  modules, without forking gosip. No such contract is finalized here.
- **Agreed sequencing**: the main components (`UserAgent`, `Registrar`,
  and peers) are developed **before** SIP extensions; extensions stay in
  view now only to avoid architectural dead ends, not to start
  implementation. The first future extension group is 100rel, Path,
  outbound, and Replaces (see section 8); ordering within the group and
  among component slices is undecided.

## 1. Shared boundaries and terminology

### Element scope

`sip.Element` remains the composition root for transports, routing,
transactions, and component wiring. Role-specific operation policies (how a
UA retries an authenticated request, how a redirect server picks response
statuses, how a registrar expires bindings) do not move into `Element`.
There is no plan for a global retry middleware on `Element`.

### Component model

Higher-level roles attach as components via `Element.UseComponent` and
implement whichever optional interfaces they need: message interceptors on
the four transport chains, transaction handlers, `Capabilities()`,
`Shutdown(ctx)`, `io.Closer`. A desired default from the discussion: one
component may both hook inbound processing *and* expose a public
explicit-operation API on the same type; no wrapper objects are required to
separate the two surfaces.

Terminology: "SIP extension" is reserved for protocol extensions (new
methods, headers, option tags). Attachable units are called "components",
not extensions.

### Construction and ownership (proposed defaults)

The following defaults are recommendations from the discussion to validate
in the next design round, not finalized decisions:

- Components are constructed explicitly and attached explicitly with
  `UseComponent`; constructors do not self-register.
- A component receives or references a configured `Element` rather than
  creating one implicitly.
- A component that borrows an `Element` does not start, shut down, or close
  it; element lifecycle stays with the element's owner.

When one component instance serves several elements, the processing element
for any given request or operation must be unambiguous, and per-element
state must stay isolated.

## 2. Proxy: explicit forwarding entry (`ForwardRequest`)

Today `sip/proxy` claims inbound requests through route `MatchRequest`
predicates inside the interceptor chain. That is sufficient for simple
matching, but it does not cover orchestration outside the chain: an
application that already received a request and applied its own policy may
want to hand the request to proxy forwarding, and a hybrid UAS/proxy may
forward only some calls.

The desired direction is an explicit entry point, tentatively
`Proxy.ForwardRequest`, that hands an already received request into
proxy-owned forwarding. It is a handoff of an existing inbound request, not
request origination and not a bare "send this on the wire" helper. There is
no final signature.

Design intent: the middleware path (inbound claim) and the explicit entry
share **one** processing path, so behavior stays identical across:

- request validation, authorization, and security checks;
- ACK/CANCEL handling;
- transaction admission and retransmission deduplication;
- upstream response ownership;
- stateful vs stateless forwarding rules, snapshots, and event observation.

Acceptance of the call by the method must not be conflated with completion
of forwarding, which is asynchronous.

Open decisions:

- whether forwarding is driven by the configured routes or by a route or
  targets supplied with the call;
- how the processing `Element` is bound or provided for the call;
- whether the caller receives a handle, events, or a result object;
- cancellation and context lifetime of the handoff;
- who produces local rejection responses for requests that arrive outside
  the normal interception and transaction-manager path.

## 3. UserAgent (UAS + UAC)

A `UserAgent` would be an optional role component combining incoming
server-side (UAS) handling with outgoing client-side (UAC) operations,
dialog and session management, and public role-specific methods such as
`Invite`, `Register`, or `Message` (names illustrative only).

### Transactions vs operations vs dialogs vs sessions

A single logical outgoing operation can span multiple transactions (initial
request, authentication retry, redirect retry). The recommendation from the
discussion is to keep that layering internal and not rush a universal
public `Client` abstraction; this is a recommendation, not an approved API
contract.

Four distinct scopes matter:

- **transaction** -- one request and its responses (the existing layer);
- **operation** -- a logical user action that may chain transactions;
- **dialog** -- a SIP dialog; one INVITE can fork into several early or
  confirmed dialogs, so an `Invite` operation is not 1:1 with a dialog;
- **session** -- the higher-level session abstraction associated with
  dialogs; its relationship to SDP and media is still open.

Design concerns to address (open, not committed):

- provisional responses and early dialog tracking;
- CANCEL races and late 2xx arrival;
- ACK ownership differences between non-2xx and 2xx INVITE finals;
- in-dialog requests: CSeq ordering, target/routing, and BYE handling.

Unchosen boundaries: the SDP offer-answer model, the session abstraction,
and how far RTP/media integration goes. The UA design makes no automatic
commitment to a media engine.

### Authentication and redirect policies

These are optional policies; required SIP correctness (transactions, ACK,
CANCEL, dialog state) is not optional and must work without them.

- **Authentication** should reuse `sip.ClientAuthenticator` (see
  [authentication.md](authentication.md)). The authenticator supplies
  credentials only; the exchange owner correlates the challenge response
  and produces the retry request/transaction with fresh CSeq/Via where
  required.
- **Redirect** (3xx contact handling) needs explicit limits: depth, total
  attempts, loop detection, allowed targets, a shared deadline across
  retries, and trust evaluation before credentials are recomputed for a new
  target. Credentials are never carried blindly to another target.
- DNS/transport failover already exists inside `Element`'s send path; it is
  a different concern from logical authentication or redirect retries, and
  the layers must not be conflated.
- A UAC following 3xx contacts and a proxy recursing on 3xx are different
  policies, not the same feature.

The whole UA is not a prerequisite for useful work: vertical slices (for
example REGISTER-only or MESSAGE-only) are viable, and their order remains
open. Per the agreed sequencing, main components come before SIP
extensions; that means extensions are not immediate implementation
pilots, not that every UA feature must exist before extension work may
start.

## 4. Redirector

A `Redirector` would be a small, independent SIP redirect server component:

```text
inbound request -> match -> authorize -> resolve contacts -> 3xx response
```

It answers with redirection instead of forwarding: it does not follow
remote redirects itself, and it is not "a proxy with sending disabled";
there are no downstream client branches at all.

Desired properties:

- contacts in the response may carry `q` values and `expires` parameters;
- 300, 301, and 302 have distinct semantics; supported statuses, defaults,
  and whether selection is configured or per-request remain open;
- server transaction behavior, retransmissions, and ACK/CANCEL handling
  must remain correct under the existing transaction layer.

Open: the exact callback shape for contact resolution, and the mapping of
policy failures to status codes and error values. No public
`RedirectRequest`-style explicit entry has been designed, and none is
obligatory.

## 5. Registrar

`Registrar` is a **placeholder name only**: recorded as a future feature at
the user's request and not designed or discussed beyond this section.

Intended role: handle incoming REGISTER requests and manage the binding
between an address-of-record and its registered contacts.

Explicitly out of scope for it: *outgoing* REGISTER is the user agent's
registration-client responsibility, not this server's.

Everything else is an open question rather than a designed solution:
authentication and authorization of registrations, expiration semantics,
persistence, concurrency, replicas, and lifecycle contracts.

## 6. Additional directions (new proposals, unconfirmed)

The items in this section came up as new proposals during the discussion.
They are recorded for completeness and have **not** been agreed.

### Location service

A shared storage/lookup boundary for address-of-record to contact bindings:

- the registrar updates bindings;
- the proxy resolves forwarding targets from it;
- the redirector resolves contacts from it.

This is a *logical* SIP location/contact lookup and must not be confused
with `Element`'s RFC 3263 DNS/network destination resolution, which stays
at the transport layer. Whether it becomes a component or a public
interface is undecided; do not assume `proxy.TargetPlan` is the shared
lookup contract, because its branch scheduling and delivery concerns are
proxy-specific. Any common interface is still to be designed.

### B2BUA

A back-to-back user agent could eventually be composed from two UA legs
with explicit, independent control over each leg's dialogs and sessions. It
is not a proxy mode and is not an agreed near-term feature.

Dialog and session state abstractions are part of the UA design space; they
are not automatically separate components registered on an element.

## 7. Framework and public integration contracts

gosip aims to be a composable SIP framework, not a fixed stack. Public
contracts should distinguish two surfaces:

- **application API**: the operations and options an application calls;
- **integration SPI** (service-provider interface): the contracts a
  consumer implements to supply or replace framework behavior, such as
  custom transports, resolvers, authenticators, or policy objects.

The rule of thumb from the discussion: core and built-in implementations
should be written against the same public capabilities an external
consumer gets; implementing an equivalent feature must not require
privileged access to private or internal APIs. Existing seams -- the
transport interfaces, transaction factories,
`sip.ClientAuthenticator` / `sip.ServerAuthenticator[P]`, and the
`Element` component model -- are examples of this shape, not a complete
solution.

Two integration mechanisms are expected, at host level:

- **supplement**: add behavior through typed hooks, policies, or stateful
  protocol handlers;
- **replace**: substitute a mechanism through an explicit strategy,
  factory, or service interface.

Replacement should not be forced through clever interception chains or a
universal event bus. Interfaces stay consumer-owned in their host layers
(element, component, registrar, ...): optional implementation packages
depend on the host or core, and hosts never import concrete
implementations. Exact package and interface boundaries are undecided.

A SIP extension is a protocol feature and may span several role adapters
(UA, registrar, proxy, transport); it is not necessarily one
`ElementComponent` or one middleware, and there should be no giant
mandatory all-roles interface. Typed host-level integration lets a state
owner offer read-only views or events plus explicit actions, instead of
exporting mutable internal fields or offering only after-the-fact
observation.

Any integration contract must define, at minimum:

- state scope: element, component, operation, dialog, registration, or
  flow;
- ownership and concurrency/serialization guarantees;
- callback reentrancy, deadlines, and cancellation;
- timers, deterministic execution order, and cleanup/drain behavior.

An optional feature does not promise live attach/detach while stateful
work is in flight. Third-party implementability -- an external module can
realize an equivalent feature -- is the eventual validation criterion for
these contracts. There is no implementation or test plan yet, and the
goal excludes a generic plugin or DI framework and speculative hooks for
every RFC.

## 8. SIP extensions (future work)

Extensions come after the main components (see the status notes). The
first future group, in no committed order:

| Extension | RFC | Primary hosts | Scope notes |
| --- | --- | --- | --- |
| 100rel | [RFC 3262](https://www.rfc-editor.org/rfc/rfc3262) | UA (INVITE operations) | reliable 1xx (except 100 Trying); PRACK transactions; RSeq/RAck state; timers; early-dialog and offer-answer interaction |
| Path | [RFC 3327](https://www.rfc-editor.org/rfc/rfc3327) | Registrar, proxies | registrar stores the path with the binding; proxies on the registration path collect it; the home proxy routes along it |
| outbound | [RFC 5626](https://www.rfc-editor.org/rfc/rfc5626) | UA, Registrar, edge proxy, transport | UA flow registration and recovery; registrar instance-id/reg-id bindings; edge-proxy token and exact-flow routing; transport keepalive and liveness |
| Replaces | [RFC 3891](https://www.rfc-editor.org/rfc/rfc3891) | UA | match an existing INVITE-created dialog and coordinate authorized replacement by a new INVITE |

100rel and outbound carry the highest importance in this group. Later
candidates are recorded, not scheduled: tdialog
([RFC 4538](https://www.rfc-editor.org/rfc/rfc4538)) -- target-dialog
knowledge used to authorize a new dialog-creating request outside that
dialog (it is not replacement, and proxy behavior is unchanged); GRUU
([RFC 5627](https://www.rfc-editor.org/rfc/rfc5627)) -- a UA obtaining and
using a globally routable URI, the registrar issuing it, and the
location/proxy path resolving the instance-specific target.

Guardrails from the discussion:

- **100rel** is UA-level protocol state, not just adding headers and not
  necessarily a replacement for the base transaction FSMs: reliable
  provisional responses exclude 100 Trying, and PRACK runs its own
  transaction.
- **Replaces** matches on Call-ID and tags, needs early vs confirmed
  handling (including the early-only case), authorization (the
  identifiers alone are not permission), and coordination between
  accepting the new INVITE and tearing down the old dialog; failure to
  accept the new INVITE must leave the old dialog unchanged. Replaces
  alone is not attended transfer -- REFER/NOTIFY are separate work.
- **Path** is independently useful: it applies to registration routing,
  not per-dialog Record-Route and not the UA's outbound Service-Route. It
  is an important foundation for the server side of outbound topologies
  with edge proxies, not a blanket mandatory local dependency -- an
  outbound UA may register with a remote registrar/edge proxy.
- **outbound** needs neutral flow mechanisms that may live in the
  transport runtime, with the outbound protocol policy optional outside
  it: live flow identity and lifecycle, exact-flow delivery, and
  keepalive plumbing -- not just an address tuple or a SIP message
  interceptor. Selecting a dead existing flow must not silently open a
  replacement connection under the same flow identity. There is no final
  flow interface design.
- **Capabilities** must distinguish four things: an implementation being
  attached/available, the local enabled policy, the role- and
  message-appropriate Supported/Require/Proxy-Require advertisement, and
  the actual negotiated per-operation/per-dialog use. Aggregated
  `Element` option tags alone are insufficient when several roles
  coexist -- a globally enabled 100rel implementation must not be
  advertised as proxy feature support. Local dependency and
  configuration validation are separate from remote negotiation:
  advertise only behavior that is both supported and configured.

Optional dependency sketch (illustrative, no implementation ordering
mandate within the group):

- the UA/dialog foundation serves 100rel and Replaces;
- registrar/location bindings plus proxy routing serve Path;
- Path, the transport flow runtime, and UA registration inform outbound
  (edge-proxy topologies are conditional).

## 9. Open next design decisions

The next round of discussion should settle, in no particular order:

- status reporting: how components expose operation state and outcomes;
- ownership: which component owns a request, transaction, or dialog at each
  point, and how that is represented;
- operation results: handles vs events vs result objects for explicit
  entries such as `ForwardRequest`;
- dispatch: how inbound messages are routed between coexisting components;
- session scope: where dialog/session state lives inside the UA and who may
  touch it;
- SPI stability: versioning and compatibility expectations for the
  integration contracts;
- minimum host contracts: the smallest interface surface each host must
  offer, and safe composition plus capability negotiation when several
  roles coexist.

Composition sketch (illustrative only, not settled ownership
relationships):

```text
            +--------------------------------------------------+
            |          Element (transports, transactions)      |
            +--------------------------------------------------+
              |          |             |             |
        +----------+ +-----------+ +------------+ +------------+
        |  Proxy   | | UserAgent | | Redirector | | Registrar  |
        +----------+ +-----------+ +------------+ +------------+
```

The components are peers on one element, not a mandatory serial pipeline:
each attaches independently and none implies another.
