# Proposal

## Why

The current SIP proxy middleware is an incomplete forwarding skeleton: response
forwarding and branch generation are missing, target iteration can fail or block, and the sending path cannot coordinate parallel branches. gosip needs a usable proxy foundation for a future framework: correct protocol defaults, small idiomatic extension points, and application-owned routing and persistence.

## What Changes

- Replace the proxy execution core with shared RFC 3261 routing and separate transaction-stateful and stateless execution paths.
- Support parallel and sequential target groups, per-attempt client transactions, response aggregation, CANCEL/ACK handling, Timer C, and lifecycle cleanup. Preserve RFC 6026 INVITE transaction behavior.
- Add opt-in RFC 4320 behavior through non-INVITE server transaction options, disabled by default in both transactions and proxy configuration. Preserve independent proxy response/lifecycle guards; use RFC 4321 for regression scenarios, not as a separate normative standard.
- Redesign `ProxyRoute`, target resolution, and forward-context APIs to separate immutable configuration, serializable target plans, per-request orchestration, and transport delivery. Retain `Proxy` as `Element` middleware and reuse existing transport and transaction infrastructure.
- Replace automatic store-oriented recovery with explicit, versioned snapshot/restore APIs. Applications own storage, serialization scheduling, and restart orchestration.
- Provide working defaults and optional focused hooks for application policy. Protocol invariants remain enforced even when applications customize routing or request preparation.
- Anticipate RFC 5626 through independent target identity, per-target route sets, delivery references, and branch-selection/delivery seams. Do not implement or advertise SIP Outbound support in this change.
- Expose optional typed proxy observation events and integrate them with StatsRecorder. Separate logical requests, forwarding attempts, response sends, and terminal outcomes without coupling protocol processing to a metrics backend.

Out of scope: B2BUA or dialog ownership, registrar/location-service implementations, built-in databases, automatic store recovery, automatic 3xx recursion, RFC 5626 flow-token codecs and registration rules, STUN/CRLF keep-alives, and destination blacklisting across unrelated transactions.

## Capabilities

### New Capabilities

- `sip-proxy`: Stateful and stateless RFC 3261 proxy middleware, routing, forking, response handling, application policy, lifecycle, delivery extensibility, observation events, and proxy statistics.
- `sip-non-invite-transactions`: Opt-in RFC 4320 response restrictions and delayed Trying behavior, with legacy-compatible defaults and mode-preserving snapshot/restore.
- `sip-proxy-recovery`: Explicit snapshots and dormant restoration of proxy request state and its transaction relationships, with application-managed persistence.

### Modified Capabilities

None. The project currently has no durable OpenSpec capability specifications.

## Impact

- Primary implementation areas: `sip/proxy.go`, new focused proxy files under `sip/`, and their public behavior/internal tests.
- Supporting areas: non-INVITE server transactions, transaction snapshots, timing helpers, and narrowly scoped `Element` integration where required. Avoid unrelated transport/transaction API changes.
- Extend `sip/stats.go`, its tests, and `StatsReport` with event-derived proxy statistics; preserve existing transport and transaction statistics.
- Existing `Element` transaction handlers and `ProduceRequestAttempts`/`RequestAttempt` APIs are integration points; the high-level `SendRequestStateful` confirmation loop is not the fork scheduler.
- The library is in active development; the new proxy API can replace unfinished contracts directly. Provide documentation and usage examples for the resulting API.
- No new runtime dependencies or external services are planned.
- Protocol basis: RFC 3261 Sections 16-18, normative updates in RFC 4320 and RFC 6026, analysis in RFC 4321, and future extension constraints from RFC 5626 Sections 5 and 7.
- Server transaction snapshots retain the selected non-INVITE behavior; older snapshots without the new field restore in Legacy mode. Default-off behavior alone does not establish full RFC 4320 compliance.
