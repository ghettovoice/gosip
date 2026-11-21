# Design

## Context

See [proposal.md](proposal.md) for motivation. Phase 2 follows
[the UA design](../../../docs/useragent-design.md). The existing INVITE
client transaction delivers the stream of 2xx responses while Accepted
(Timer M); it handles ACK for 3xx-6xx itself. The RFC 3261 ACK for a 2xx
bypasses the server INVITE transaction. Phase 1's `LastResponse()`-based
operation loop is not an INVITE integration seam.

## Goals / Non-Goals

**Goals:** Establish a public dialog contract that an independent consumer
can use without hooks into transaction internals. Prove this boundary
against actual transaction delivery and the wire format.

**Non-Goals:** No component registration, dialog discovery across elements,
implicit send or teardown, offer/answer, transaction changes, or new
processor interfaces for extensions. Support RFC 3261 tagged dialog
establishment; adding RFC 2543 tagless-dialog compatibility is out of scope
and does not change legacy behavior in the core transaction package.

## Decisions

### Keep `sip/dialog` passive and own one dialog per object

The package imports `sip`, not `sip/useragent`. Its public surface consists
of role-specific establishment from the initial INVITE plus response,
matchable identity, a detached view, explicit confirmation and termination,
request matching/sequence validation, and builders for ordinary requests
and 2xx ACK. It does not register interceptors or retain the transactions or
Element. Consumers own any lookup map; a consumer operating on multiple
Elements scopes lookup by Element as well as by the dialog key.

Use one serialization point per dialog for state transitions and counters.
Copy the required message fields on input; return owned envelopes and
views on output. Complete validation before mutating state. The package
runs no timers, callbacks, I/O, or background workers under or outside
that lock. The caller owns context, transport and transaction lifetime,
serialization of wire dispatch, and removal from lookup. The package
serializes CSeq allocation, not sends performed outside it. Failed
construction does not allocate a number; an allocated CSeq is not reused
if the caller abandons the request or sending fails.

Keeping this in `sip/useragent` would couple external consumers to the
component and its operation policy. A global registry or automatic
transaction attachment would assume ownership of dispatch and lifecycle
before the INVITE vertical can validate those choices.

### Use role-specific establishment and the Element routing pipeline

Derive identity, URI, route and sequence baselines per RFC 3261 sections
12 and 13.2.2.4. Validate correlation to the initial INVITE, including
Call-ID, originating tag, and CSeq method and number. Require one usable
SIP or SIPS Contact in the peer message that supplies the remote target.
Keep SIP URI scheme and parameters; never infer a target from transport
metadata. On early-to-confirmed UAC promotion, replace only the route set
from the final response, even if empty, as specified in
[RFC 3261 section 13.2.2.4](https://www.rfc-editor.org/rfc/rfc3261.html#section-13.2.2.4).
Keep the remote target and both CSeq values. A late provisional response
or a retransmitted 2xx for a confirmed dialog does not change its state.
The UAS consumer supplies its tagged establishing response with a stable
local tag, its own Contact, and the INVITE's Record-Route values in wire
order; owning response generation remains outside the package.

Produce a fresh endpoint envelope with the remote target as Request-URI
and the logical route set as Route, without inherited address or cached
resolution metadata. `Element.ResolveRequestTarget` selects the next hop
and performs strict-routing rewrites exactly once. Send ordinary requests
such as BYE with `Element.SendRequestStateful`; the core does not choose
application responses, perform target refresh, or decide when a BYE is
appropriate. Ordinary construction excludes ACK, CANCEL, and target-refresh
INVITE/UPDATE, rather than silently offering incomplete method semantics.
Inbound CSeq validation reports an out-of-order result to the caller; the
caller owns the RFC 3261 500 response for a stale ordinary request and the
transaction manager continues to own retransmission deduplication.

### Separate 2xx ACK construction from operation policy

Build an ACK for the matching confirmed UAC dialog and the actual sent
INVITE. Use a fresh envelope with the remote target as Request-URI and
the dialog route set as Route, so Element performs any strict-route
rewrite just as for an ordinary request. Copy the INVITE's Authorization
and Proxy-Authorization headers, if any, without calling the authenticator
or putting credentials on other in-dialog requests. Permit caller-supplied
opaque ACK body and content type without classifying offers or answers.
A matching incoming UAS ACK is recognized by dialog identity and the
initial INVITE CSeq; it does not go through the ordinary CSeq update path
and does not receive a response.

The caller sends the ACK through `Element.SendRequest` (no client
transaction). Retain the prepared ACK separately from the dialog, keyed by
fork and INVITE attempt, and send it on each repeated 2xx. The consumer can
keep a pristine clone for retry or reuse the successfully resolved envelope
on that same Element; both respect `Element`'s routing ownership. Retire
the ACK with the corresponding response observer according to the
exchange lifetime, not when the dialog ends. Do not include the copied
credential values in diagnostic logs. Phase 2 proves this contract
in a test consumer; Phase 3 owns production observer handoff, pending
forks, late 2xx after CANCEL, and UAS retransmission while waiting for ACK.

An automatic ACK/termination policy in the core would need to decide when
and how to answer a remote offer and which forks to retain. Deferring that
policy avoids a second operation layer. Keep the public surface to views
and actions; use early dialog identity and request construction to check a
future 100rel integration, and role-relative lookup with explicit lifecycle
to check a future Replaces integration. Neither requires a new hook now.

## Risks / Trade-offs

- Treating `LastResponse()` as a stream can lose forked finals. In the
  test consumer, create the client transaction with
  `Element.NewClientTransaction` (which registers it), bind the response
  handler before `Start`, and process every delivered response. Do not
  add a new core callback to make the fixture work.
- Double strict-routing rewrite or accidental routing to the INVITE's
  peer address can send BYE to the wrong hop. Use fresh envelopes and
  assert Request-URI, Route values, and selected next hop against Element.
- Equal or reused CSeq values have method-specific meanings. Keep ACK and
  CANCEL out of ordinary accounting and validate successful allocation,
  stale peer values, and overflow independently.
- Publishing a building block leaves callers responsible for exchange
  policy and send ordering. Document that distinction rather than
  promising a complete UA or session API in this package.

## Migration Plan

None. The new package is opt-in; existing packages and applications retain
their behavior without adopting it.
