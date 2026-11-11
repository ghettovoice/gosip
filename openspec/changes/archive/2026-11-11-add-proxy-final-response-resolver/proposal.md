# Proposal

## Why

Applications need to customize which accumulated downstream final response a stateful proxy returns, using the policy freedom allowed by RFC 3261 Section 16.7. The current selection is internal and fixed, so applications cannot change its preferences or reuse its default behavior through a supported API.

## What Changes

- Add `Route.ResolveFinalResponse` as an ordinary function field, consistent with the existing route hooks; do not introduce a named callback type, interface, or strategy object.
- Add `FinalResponseSelection` carrying the request and arrival-ordered response candidates. The callback returns a candidate index and an error.
- Export `DefaultFinalResponseResolver` with the same signature. A nil route callback preserves current behavior, and custom callbacks can delegate to this function.
- Separate candidate ranking from proxy-owned response processing. Preserve successful-response handling, mandatory 6xx precedence, authentication aggregation, method-specific suppression, and the existing lone-503 transformation.
- Run custom resolution outside coordinator locks, validate its result, and handle policy failure explicitly rather than silently applying the default.
- Preserve the selected candidate in recovery checkpoints instead of recomputing custom decisions using the default policy.
- **BREAKING (snapshot layout):** Extend the current version-1 request snapshot format in place with explicit selection identity. Backward compatibility with the previous layout is not required; do not introduce another snapshot version, legacy reconstruction, or migration paths.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `sip-proxy`: Route-specific final-response selection with a reusable default and a clear boundary between application preferences and protocol invariants.
- `sip-proxy-recovery`: Durable selected-candidate identity and side-effect-free restoration of committed custom decisions within the updated current snapshot format.

## Impact

- Public API additions in `sip/proxy/config.go` and `sip/proxy/response.go`.
- Coordinator changes in `sip/proxy/stateful.go` to execute and commit resolution safely.
- Snapshot capture, cloning, validation, and installation changes in `sip/proxy/snapshot.go`.
- Focused response, hook, concurrency, and recovery tests, plus API usage documentation and an example.
- No changes to transaction-layer APIs, stateless response processing, persistence ownership, routing policy, or dependencies.
- Existing keyed route configuration and nil-hook behavior remain unchanged. As with any added public struct field, downstream unkeyed `Route` or `RequestSnapshot` literals require updating.

This change contains planning artifacts only; implementation requires a separate apply request.
