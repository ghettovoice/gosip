# UserAgent Design

- Date: 2026-11-17
- Status: proposed, awaiting approval
- Scope: new packages `sip/dialog` and `sip/useragent`, and their integration
  with `sip.Element`
- Source discussion: [future-architecture.md](future-architecture.md)
  sections 3, 7, and 8; this document turns the recorded direction into a
  concrete design. It does not finalize anything else recorded there.

All type, function, and field names in this document are illustrative; the
builder chooses final names within the repo naming rules.

## 1. Intent and why

gosip today stops at protocol mechanics: messages, transports, transactions,
and the `Element` composition root. Applications can build a proxy or a
redirect server, but there is no endpoint role. The endpoint stack closes that
gap: the UserAgent (UA) combines outgoing client operations (UAC) with incoming
server handling (UAS), a reusable dialog manager handles in-dialog dispatch,
and the UA's session layer provides high-level session operations.

The why is recorded in future-architecture.md: gosip is a composable framework
where roles attach as components, main components come before SIP extensions,
and the UA is the host layer for most of the planned extensions (100rel,
Replaces, tdialog, session timers, outbound). Its internal boundaries decide
whether those extensions land cleanly later, which is why this design cares
about seams more than about feature count.

## 2. Goals

1. An endpoint role component: attachable to `Element` via `UseComponent`,
   implementing the optional component interfaces, with one type exposing
   both the inbound hooks and the public operation API.
2. RFC 3261 UAC/UAS core: outgoing operations (MESSAGE, OPTIONS, REGISTER,
   INVITE with forking), incoming request handling, dialogs, in-dialog
   requests, sessions, and BYE.
3. A high-level operation API: op handles with event callbacks set at
   creation, `First`/`Wait` convenience methods, and cancellation mapped to
   CANCEL.
4. A separate dialog host package (`sip/dialog`) with an optional `Element`
   component for dialog lookup and dispatch, usable without the UA.
5. Session as the primary public abstraction, fully implemented and managed
   by the UA, with offer/answer generation behind an SPI and media entirely
   outside.
6. Replaceable policies: authentication, redirect following, disposition of
   extra forked dialogs; hooks for application handling of in-dialog methods.
7. Architectural readiness for the extension list (100rel, Replaces, tdialog,
   timer, outbound, GRUU, RFC 3840) without implementing any of them now.

## 3. Non-goals

- Media: no RTP, no SDP parsing or generation in gosip. Message bodies are
  opaque bytes passed to and from application code.
- SIP extensions: not in this design. Only the seams they will need.
- B2BUA as a deliverable. It remains a possible later composition over two
  dialog sets.
- Registrar (the server side of REGISTER), location service, proxy behavior.
- SUBSCRIBE/NOTIFY, PUBLISH, REFER operations in the initial phases. The op
  pattern is method-agnostic and they can be added later.
- Service-Route consumption (RFC 3608): arrives with the Path extension
  group.
- Live attach/detach of the UA while stateful work is in flight.

## 4. Anti-goals

- Burying dialog state inside the UA package: dialog-level extensions would
  then depend on the whole UA, and extracting the layer later would be a
  breaking change.
- Rushing a universal public `Client` abstraction. The layering
  operation-over-transactions stays internal to the UA (future-architecture
  section 3).
- Policy defaults that cannot be replaced or disabled.
- Privileged access: `sip/useragent` and `sip/dialog` are written against the
  same public surface an external consumer gets.
- Any automatic commitment to a media engine.

## 5. Constraints

- Component model: explicit construction and explicit `UseComponent`
  registration; the component borrows the element and never starts, shuts
  down, or closes it; one instance may serve several elements with
  unambiguous per-request element binding (future-architecture section 1).
- Transactions: existing FSMs absorb retransmissions and handle CANCEL and
  non-2xx ACK internally; ACK for 2xx responses to INVITE is end-to-end and
  becomes dialog-layer responsibility; `Element.Close` already sends
  best-effort CANCELs for proceeding INVITE client transactions.
- Send path: `Element.SendRequestStateful` provides RFC 3263 candidate
  resolution, failover, and transaction confirmation; operations build on it.
- Authentication: `sip.ClientAuthenticator` supplies credentials only; the
  operation is the exchange owner that correlates the challenge and produces
  the retry request with fresh branch and incremented CSeq. Everything works
  without an authenticator attached.
- Redirect following (3xx contacts): bounded by depth, total attempts, loop
  detection, allowed targets, a shared deadline, and trust evaluation before
  credentials are recomputed for a new target. Credentials are never carried
  blindly to another target.
- Capabilities: attached, enabled, advertised, and negotiated are four
  different things; the UA advertises only what is both supported and
  configured (future-architecture section 8).
- Sequencing: main components before SIP extensions.
- Repo conventions: naming rules, `internal/errors` plus `errclass` error
  taxonomy, testing style, and the `task` lint/test gates documented in
  `.agents/rules/` and `.devin/rules/`.

## 6. Approaches considered

### 6.1 Session semantics: what is a session?

| Option | Description | Verdict |
| --- | --- | --- |
| A. SIP dialog only | `Invite` returns a dialog; bodies are opaque and the application negotiates | rejected: session management is a stated goal |
| B. Dialog plus SDP offer/answer | the UA owns typed SDP negotiation state | rejected: drags SDP parsing/generation into gosip |
| C. Managed session with offer/answer behind an SPI | the session knows when an offer or answer must appear; a pluggable provider decides the content | **picked** |

Picked as the middle ground, closer to B: the Session abstraction and its
management are fully implemented in the UA, while offer/answer generation and
media stay outside.

### 6.2 Decomposition: where the dialog layer lives

- A. Single package `sip/useragent` with dialogs as an internal layer.
  Fastest start, but every dialog-level extension would depend on the whole
  UA, and extracting the layer later is a breaking refactor.
- B. Two layers: `sip/dialog` as the dialog host, `sip/useragent` on top.
  **Picked.**
- C. Dialogs in core `sip` next to transactions. Rejected: the criterion is
  who consumes the feature, not which RFC defines it. Transports and
  transactions serve every role; dialog state serves only UA-like roles
  (UA, later B2BUA). Core would also balloon.

### 6.3 Operation API shape

- A. Blocking call: `sess, err := ua.Invite(...)`. Simple, but hides forking,
  early dialogs, and additional confirmed sessions.
- B. Event-only op: subscribe after creation. Full visibility, but there is a
  subscription race against the first responses, and ceremony for simple
  calls.
- C. Hybrid op handle: callbacks set in the options at creation (no race),
  `First`/`Wait` convenience, `Cancel`. **Picked.** Covers both "just make
  the call" and per-fork control for B2BUA-style applications.

### 6.4 Implementation order

- A. Operation skeleton on simple methods first (MESSAGE/OPTIONS), designed
  INVITE-shaped, then dialog core, then the INVITE vertical, then
  registration. **Picked.**
- B. Dialog core first. Solid foundation first, but long without user value
  and the contract is designed without a live consumer.
- C. INVITE vertical first. Core value first, but the hardest part (forking,
  CANCEL races, late 2xx, ACK ownership) with the least infrastructure.

Option A carries an explicit caveat: the op pattern must be designed for
INVITE from day one (op handles, creation-time event callbacks,
`First`/`Wait`), with MESSAGE merely as its first consumer.

## 7. Design

### 7.1 Package layout

```text
sip/dialog       Passive Dialog core and optional Manager component.
sip/useragent    UserAgent component: operations, endpoint policy, sessions.
```

Import direction is strictly downward: core `sip` never imports `dialog`;
`dialog` never imports `useragent`. Extensions pick their host layer (100rel
and Replaces target `dialog`; outbound targets transport plus the UA
registration hooks).

`Dialog` itself is never registered with `UseComponent`. In phase 3 the
optional `dialog.Manager` is a separate component implementing
`sip.InboundRequestInterceptor`. The UA uses the same public manager API
as a standalone consumer. `SessionManager` starts as an internal UA
consumer, not as another `Element` interceptor.

### 7.2 `sip/dialog`: scope

The passive phase 2 dialog core implements RFC 3261 section 12:

- `DialogID` (Call-ID, local tag, remote tag) and matching of incoming
  requests and responses to dialogs.
- Dialog FSM: early, confirmed, terminated, with the confirmed-on-2xx and
  early-dialog tracking rules.
- Route set, remote target, and CSeq tracking for both directions.
- Building ordinary in-dialog requests; the caller dispatches them through
  the Element send paths.
- ACK for 2xx responses to INVITE, built from the dialog state.
- The host contract for dialog-level extensions: typed read-only views,
  events, and explicit actions, never exported mutable state. The exact hook
  set is left to the builder; 100rel (RSeq/RAck state, PRACK correlation)
  and Replaces (matching by the Replaces header, early versus confirmed
  rules) are the pressure tests.

What `dialog` deliberately does not do: forking policy, authentication,
redirect handling, offer/answer, registration. Those are UA concerns.

#### Phase 3: Dialog manager

- Register each early or confirmed branch and its handler explicitly. The
  manager owns lookup and removal, keyed by receiving `Element` and
  role-relative `dialog.ID` (Call-ID and tags). It can conclude and remove
  a branch without sending BYE; the caller decides when to end it.
- The existing transaction layer gets the request first. The manager
  claims a request only if it finds an owned dialog, checks ordinary
  request CSeq through `Dialog.ValidateRequest`, and hands the matched
  dialog and request to the branch handler. A stale CSeq on an owned
  dialog gets the RFC 3261 500 response; retransmission deduplication
  stays with the transaction layer.
- ACK for an INVITE 2xx uses `Dialog.MatchACK` and notifies the owner of
  the UAS INVITE exchange without creating a transaction, advancing CSeq,
  or sending a response. CANCEL and non-2xx ACK stay with the transaction
  path. The manager does not intercept INVITE responses.
- Requests without a matching dialog pass to the next component unchanged,
  including requests with a To tag. A proxy on the same Element may own
  them. Return 481 only when the endpoint has established ownership, never
  solely because lookup missed. Branch handlers run outside registry locks.

### 7.3 `sip/useragent`: component shape

UserAgent construction and attachment (the manager is registered separately):

```go
ua, err := useragent.New(useragent.Options{
    AOR:            aor,   // required, the local identity
    Contact:        contact, // optional; derived when unset
    Authenticator:  auth,    // optional, sip.ClientAuthenticator
    RedirectPolicy: policy,  // optional, bounded 3xx follower
    Negotiator:     neg,     // optional, default offer/answer SPI
    OnInvite:       func(ctx context.Context, inv *useragent.IncomingInvite) {},
    OnMessage:      func(ctx context.Context, msg *useragent.IncomingMessage) {},
})
elm.UseComponent(ua)
```

- `New` is pure: it validates and copies configuration and binds nothing;
  registration is the explicit `UseComponent` call. This follows the
  `sip/proxy` and `sip/redirector` construction pattern.
- The type implements `MessageInterceptor` (claiming inbound requests that
  belong to it; responses to operation transactions arrive through the client
  transactions' response handlers), `Capabilities()` (advertising handled
  methods), `Shutdown(ctx)`, and `io.Closer`.
- In phase 3, explicitly register the shared `dialog.Manager` before the
  UA on each Element. The UA registers branches with that manager and
  receives requests for them through branch handlers. Its own interceptor
  claims initial and standalone requests it is configured to serve and
  passes other traffic to the next interceptor. It does not duplicate the
  manager's dialog lookup. Standalone consumers can register the manager
  without registering a UA.
- `Shutdown(ctx)` cancels in-flight operations, drains UA-owned sessions
  with best-effort BYE, and stops the registration manager if active.
  Keep dialog dispatch available during that drain; `Element.Shutdown`
  runs hooks in registration order, so the dialog manager must not clear
  the registry in an earlier shutdown hook. Unregister remaining entries
  during final cleanup without sending BYE.

### 7.4 Operations (UAC)

```go
op, err := ua.Message(ctx, target, useragent.MessageOperationOptions{
    Body:        []byte("hello"),
    ContentType: "text/plain",
})
res, err := op.Wait(ctx) // final response envelope, or error
```

- Every operation returns an op handle created with its options; event
  callbacks are set in those options, so there is no subscribe-after-start
  race. `Wait(ctx)` resolves with the final outcome; `Cancel(ctx)` maps to
  CANCEL where the method allows it.
- Context cancellation aborts the operation: outstanding client transactions
  are canceled, partial state is cleaned up.
- One logical operation may chain several transactions: the initial request,
  an authentication retry after 401/407 (fresh branch, incremented CSeq, new
  credentials from the authenticator), and redirect hops within the
  redirect policy limits. DNS and transport failover stay inside the
  element send path and are never conflated with these logical retries.
- Operations send statefully through `Element.SendRequestStateful`;
  stateless sending remains an element-level escape hatch.

INVITE additionally exposes forking:

```go
op, err := ua.Invite(ctx, target, useragent.InviteOptions{
    Negotiator: myNeg,                          // optional; falls back to UA default
    OnDialog:   func(ev useragent.DialogEvent) {},   // early dialogs, provisionals
    OnSession:  func(ev useragent.SessionEvent) {},  // every confirmed session
})
sess, err := op.First(ctx) // first confirmed session, ACK already sent
err = op.Wait(ctx)         // operation complete: every fork resolved
```

- One INVITE operation tracks N early dialogs and, legally, up to N
  confirmed sessions (each 2xx creates a separate dialog).
- Disposition of extra forks (my call, a policy option): once `First`
  confirms, the op CANCELs remaining early dialogs and sends BYE for late
  confirmations; a "keep all" option disables this for applications that
  want every confirmed session.
- The UA retains each prepared 2xx ACK for the lifetime of the INVITE
  response observer, keyed by INVITE attempt and `dialog.ID`. Retransmitted
  2xx responses replay it even after the manager has removed the dialog.

### 7.5 Incoming handling (UAS)

```go
// ua.OnInvite(...) or Options.OnInvite, receiving *useragent.IncomingInvite:
inv.Ringing(ctx, opts...)               // 180, optionally with a body
inv.OnCancel(func() {})                 // caller sent CANCEL
sess, err := inv.Accept(ctx, opts...)   // 2xx; body via SPI or options
inv.Decline(ctx, useragent.DeclineBusy) // 486 and similar
```

- `Accept` creates or confirms the branch and associates it with a
  session. Make the branch routable before the establishing response can
  be observed by the peer. The UA's UAS INVITE exchange owns 2xx
  retransmissions and stops them when the manager reports a matched ACK.
- CANCEL after `Accept` produces the transaction-level 481 from the
  transaction layer (RFC 3261 section 13.3.1.4). The symmetric caller-side
  race (a late 2xx racing our CANCEL) is covered by the extra-fork
  disposition in 7.4.
- OnMessage and OnOptions handlers receive the request plus a responder
  that can send any status. OPTIONS is auto-answered from advertised
  capabilities unless OnOptions is set (my call).
- The UA's internal SessionManager receives matched requests from the
  dialog manager and dispatches by session:
  - BYE terminates the session and raises OnClosed.
  - re-INVITE and UPDATE run through the offer/answer SPI and raise
    OnUpdated (phase 4).
  - other methods reach a per-session hook; the default response follows
    RFC 3261 section 8.2.1: 501 for methods unknown to the stack, 405 for
    recognized methods with no handler (builder settles the exact table).

### 7.6 Sessions and the offer/answer SPI

```go
// Body is an opaque message body with its content type.
type Body struct {
    Data        []byte
    ContentType string
}

type OfferAnswerNegotiator interface {
    // MakeOffer is called when the session must attach an offer
    // (INVITE, re-INVITE, UPDATE).
    MakeOffer(ctx context.Context) (Body, error)
    // MakeAnswer is called when a remote offer arrives and a response or
    // update must carry the answer.
    MakeAnswer(ctx context.Context, offer Body) (Body, error)
    // OnAnswer validates a remote answer to our offer.
    OnAnswer(ctx context.Context, answer Body) error
}
```

- `SessionManager` owns the association between the dialog branch and its
  session, including closure and application callbacks. It does not
  perform SIP dialog lookup or intercept Element traffic directly.
- The session layer knows when the protocol requires an offer or answer;
  the provider decides the content. Bodies stay opaque to gosip.
- A pass-through negotiator ships as the default: it carries application
  bodies unchanged and requires the app to supply offer/answer content per
  operation (my call).
- Glare (simultaneous re-INVITE) is protocol correctness, not policy: the
  session layer implements RFC 3261 section 14.1 behavior (491 handling
  with backoff).
- Session public surface: `Close` (BYE, waits for the final with a timeout),
  `Request` (in-dialog custom method), `Local`/`Remote` views, `OnClosed`/
  `OnUpdated` events. The `Dialog` stays reachable underneath for advanced
  use.

### 7.7 Registration manager

- `ua.Register(ctx, opts)` is a one-shot REGISTER operation (op handle,
  auth and redirect policies included).
- `useragent.RegistrationManager` optionally drives it: refreshes at a
  configurable fraction of the expiry (default half, my call), retries with
  backoff, optional deregistration on shutdown.
- No Service-Route consumption, instance-id, or reg-id in these phases;
  those arrive with the Path and outbound extensions.

### 7.8 Extension readiness (seams, not implementations)

Where each planned extension plugs in:

- 100rel (RFC 3262): reliable provisional state lives with the INVITE
  operation and early dialogs in `sip/dialog`; PRACK runs as in-dialog
  transactions; offers in reliable 1xx surface at the negotiator SPI.
- Replaces (RFC 3891): dialog matching by header in `sip/dialog`; early
  versus confirmed rules; an authorization hook; teardown coordination on
  the accepting side.
- tdialog (RFC 4538): dialog existence query on the `sip/dialog` host.
- timer (RFC 4028): session refresh rides the phase 4 update machinery,
  with refresher-role policy as a replaceable piece.
- outbound (RFC 5626): flow runtime stays transport-level per
  future-architecture section 8; the UA side needs registration-manager
  hooks (instance-id, reg-id) later.
- GRUU (RFC 5627): contact parameters on REGISTER plus dialog usage; the
  registrar side is separate.
- RFC 3840: feature-tag advertisement via UA options; follows the
  capabilities four-way distinction.

### 7.9 Errors

- Domain classes `useragent.ErrClass` and `dialog.ErrClass`, following the
  `proxy.ErrClass` precedent.
- Sentinels as `errors.Error` constants per the project guide, with classes
  for operation outcomes (canceled, declined, authentication failed,
  redirect limit reached, and similar). The exact taxonomy is the
  builder's, within those rules.

### 7.10 Testing

- Black-box tests in `useragent_test`/`dialog_test` packages, two elements
  over loopback using the existing `siptest` utilities.
- Table-driven cases; race scenarios (forking, CANCEL versus 2xx, glare,
  late 2xx) with deterministic timing injection.
- Dialog snapshot/restore is a later concern, not part of these phases.

## 8. Phases

1. **Operation skeleton (MESSAGE/OPTIONS).** The useragent component, the
   op handle pattern (INVITE-shaped), Message/Options operations with
   authentication retry and redirect policy, OnMessage/OnOptions handling,
   OPTIONS auto-answer. Exit: end-to-end MESSAGE between two elements,
   including auth-retry and redirect tests.
2. **`sip/dialog` core.** IDs and matching, FSM, route set, CSeq, in-dialog
   requests, 2xx ACK. Exit: dialog tests against the transaction layer;
   the extension hook surface pressure-checked against 100rel and
   Replaces on paper.
3. **INVITE vertical and component composition.** Add the optional
   `dialog.Manager` for inbound dialog dispatch and registry lifecycle.
   Compose it with InviteOp (OnDialog/OnSession/First/Wait/Cancel), OnInvite
   (Ringing/Accept/Decline/OnCancel), and the UA's internal SessionManager.
   Include BYE in both directions, the SPI and pass-through negotiator,
   fork disposition, UAS 2xx retransmissions and late-2xx ACK replay.
   Exit: standalone dialog dispatch without a UA, fallthrough on a lookup
   miss, and a two-element UA call flow with ACK/BYE, fork, decline,
   CANCEL and late-2xx coverage.
4. **Session updates.** re-INVITE/UPDATE through the SPI, OnUpdated, glare
   per RFC 3261 section 14.1, update-failure handling.
5. **Registration.** The REGISTER op and the RegistrationManager (refresh,
   backoff, deregistration).

Phases 1-3 carry the core value; 4-5 follow. Each phase lands through the
repo's plan-first workflow with its own task breakdown.

## 9. Left to the builder

- Final type, function, and field names; the shape of the options structs
  (follow the `New(..., opts ...Options)` pattern of proxy and redirector);
  event payload types.
- Dialog FSM granularity and any timers beyond the transaction timers.
- The dialog-level host contract surface (views, events, actions) validated
  against 100rel, Replaces, and tdialog needs.
- Forking details: per-fork provisional correlation, CANCEL scope on First,
  late-2xx bookkeeping.
- The 405 versus 501 response table for in-dialog methods.
- Error sentinels and classes per phase.
- The concurrency contract per public interface: state scope, ownership,
  callback reentrancy, deadlines, and cleanup, following the
  future-architecture section 7 minimums.
- The test plan per phase, including deterministic timing for race cases.
- Whether `Session.Close` waits for the BYE final (leaning yes, with a
  timeout; settle while building).

## 10. Calls made without the user, review these

1. `OfferAnswerNegotiator` operates on opaque bytes plus content type; a
   pass-through negotiator is the default.
2. Extra-fork disposition after `First`: CANCEL remaining early dialogs,
   BYE late confirmations; a "keep all" option disables it.
3. OPTIONS is auto-answered from advertised capabilities unless an
   OnOptions handler is set.
4. Registration refresh defaults to half the expiry.
5. Package names `sip/useragent` and `sip/dialog`; the role type is
   `UserAgent` (distinct from the `User-Agent` header,
   `sip/header.UserAgent`).
6. Operations always send statefully; stateless sending stays an
   element-level escape hatch.
7. The UA-level UAS shape (OnInvite/OnMessage handlers, responder objects)
   was proposed during the discussion and not objected to; treat as mine.
8. The RegistrationManager is a separate helper type in `sip/useragent`,
   not a mode of the component itself.
