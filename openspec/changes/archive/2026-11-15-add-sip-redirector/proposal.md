# Proposal

## Why

Gosip ships proxy, stats, and authentication components, but a redirect server role still requires each application to hand-roll inbound claiming plus stateful 3xx responses on top of raw `Element` primitives. The redirector is the smallest future component recorded in `docs/future-architecture.md` (section 4), and delivering it exercises the agreed component model on a second inbound role after the proxy.

## What Changes

- Add `sip/redirector`, a small independent SIP redirect server component attached with `Element.UseComponent` via `sip.InboundRequestInterceptor`: `inbound request -> match -> authorize -> resolve contacts -> 3xx response`.
- Route model with application callbacks: `MatchRequest` predicate, optional `Authorize`, required `ResolveContacts`, and optional `SelectStatus`; a nil `SelectStatus` uses the route's constant status, defaulting to 302.
- Redirect responses use only 300, 301, and 302, sent statefully through the existing server transaction machinery; resolved contacts carry their `q` and `expires` parameters verbatim.
- Map policy failures to responses: authorization failure 403, no resolved contacts 404, operational resolver failure or invalid selected status 500.
- Claim CANCEL requests matching a route and answer 481; never claim ACK (absorbed by the transaction layer).
- One stateless instance may serve several elements; construction is explicit and pure, with no self-registration and no new element lifecycle ownership.

Out of scope: following remote 3xx contacts (a UAC policy), proxy recursion on 3xx, a location-service component or shared lookup contract, 305/380 statuses, contact selection or re-try on the client side, and any change to proxy, core, or transaction behavior.

## Capabilities

### New Capabilities

- `sip-redirector`: Inbound route claiming, authorization and contact-resolution callbacks, 3xx response generation with status selection, policy-failure status mapping, CANCEL/ACK behavior, and transaction-layer correctness for redirect responses.

### Modified Capabilities

None. Existing proxy, transaction, transport, and authentication requirements remain unchanged.

## Impact

- New public package `sip/redirector` with black-box and internal tests, plus a runnable example.
- Documentation: new user guide for the redirector component and updates to the architecture description (including `docs/future-architecture.md` status).
- No changes to `sip` core, `sip/proxy`, `sip/stats`, `sip/auth`, the transaction layer, or existing public contracts; no new module dependencies.
- This change contains planning artifacts only; implementation requires a separate request after design review.
