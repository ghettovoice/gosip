# Proposal

## Why

gosip has transports, transactions, a proxy, and a redirector, but no endpoint
role: an application cannot act as a SIP user agent. There is no way to issue
an outgoing request as a logical operation (with challenge retries and
redirect following) or to answer incoming requests as a UAS without hand
assembling transactions and response routing.

The approved UserAgent design (`docs/useragent-design.md`) sequences the UA in
vertical slices. This change delivers slice one: the operation skeleton,
deliberately INVITE-shaped but exercised through MESSAGE and OPTIONS, which
the later dialog and INVITE slices will build on without redesign.

## What Changes

- New package `sip/useragent` with a `UserAgent` element component: explicit
  construction, identity configuration (address of record, contact), inbound
  claim rules, capabilities advertisement, and shutdown behavior.
- An operation pattern for outgoing requests: op handles, event callbacks set
  at creation, `Wait`/`Cancel` semantics, context cancellation.
- `Message` and `Options` operations (UAC) that chain transactions: initial
  request, authentication retry after 401/407 with fresh CSeq and Via, and
  bounded redirect following of contacts in 300, 301, and 302 responses.
- Incoming handling (UAS): `OnMessage`/`OnOptions` handlers with responder
  control, plus automatic answering of OPTIONS from advertised capabilities.
- Stateful sending through the existing element send path; no changes to
  core `sip` behavior.

Not in this change (later slices of the design): dialogs, INVITE, sessions,
offer/answer, registration, and every SIP extension.

## Capabilities

### New Capabilities

- `sip-useragent`: endpoint user-agent operations — component attachment and
  claim rules, op-handle lifecycle for outgoing requests, MESSAGE/OPTIONS
  operations with challenge-retry and redirect policies, and incoming
  MESSAGE/OPTIONS handling.

### Modified Capabilities

None. `sip-authentication` already assigns exchange ownership to "the
application or a future higher-level helper"; this change implements that
consumer without altering the authenticator contracts.

## Impact

- New package `sip/useragent`; imports `sip` only (plus existing internal
  support packages). No new external dependencies.
- No production code changes to `sip` core, `sip/proxy`, `sip/redirector`,
  or `sip/auth`. The redirector integration tests now share
  `siptest.NewLoopbackUDPElement` with the user-agent tests.
- New user guide `docs/useragent.md` (operations slice), following the
  proxy/redirector doc convention.
- First release of the package: no existing consumers to migrate.
