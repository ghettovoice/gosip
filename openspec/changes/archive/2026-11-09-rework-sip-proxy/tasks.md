# Tasks

Sections 1-12 retain the recorded completion status of earlier implementation passes. Their checkmarks do not establish that later verification findings are resolved. Section 13 tracks V2-V5 and D1 from whole-change verification, together with their regressions and fresh final checks; these follow-up tasks remain incomplete until their implementation and verification are finished.

## 1. Public contracts and configuration

- [x] 1.1 Replace mutable proxy route execution fields and forward-store contracts with the target-plan, target identity, delivery-reference, selection, and request-handle contracts from design.md; verify constructor/API tests cover nil defaults, stateful default, invalid modes, duplicate route/target identities, immutable configuration copies, default-off non-INVITE behavior, and unsupported behavior selections.
- [x] 1.2 Add focused optional resolver, authorization, request/route preparation, selection, and delivery-binding hooks with protocol guardrails; verify callback validation/error classification tests and API documentation describe every default and callback ownership rule.
- [x] 1.3 Document the new proxy configuration and target-plan contracts with compile-checked usage examples; verify documentation matches the new API and clearly states that RFC 4320 is opt-in and server transaction options are passed through unchanged.

## 2. Routing and transparent message preparation

- [x] 2.1 Implement RFC 3261 validation and separate served-domain/self-identity matching; verify tests for unknown methods/headers, unsupported Proxy-Require, OPTIONS/Max-Forwards policy, empty target plans, and inspectable rejection errors.
- [x] 2.2 Implement strict-router recovery, local Route removal, maddr handling, per-target routes, and strict/loose outbound next-hop rewriting; verify table-driven URI/header comparisons and a route hook observing extension data before local Route consumption.
- [x] 2.3 Correct missing-only Max-Forwards defaulting and forwarded-response preparation so endpoint defaults do not reset outgoing zero or invent dialog tags; verify request/response helper regressions for 1-to-0 forwarding, preserved To tags, bodies, and independent branch copies.
- [x] 2.4 Implement advertised Record-Route selection, SIPS protections, and optional loop/spiral fingerprints; verify configured-identity, lr, transport/security, true-loop, changed-route spiral, and hook-output validation tests; document identity configuration and failure behavior.

## 3. RFC 4320 non-INVITE transactions

- [x] 3.1 Add the non-INVITE server behavior option with Legacy as zero/default and explicit RFC4320 mode; apply non-100 provisional, premature unreliable-100, and 408 send restrictions only when enabled; verify both modes, invalid selections, unchanged INVITE/client behavior, zero sends/state mutation on enabled-mode rejection, and later valid final-response capability.
- [x] 3.2 Add the enabled-mode effective-T1/T2 delayed-Trying calculation and timer; verify synctest cases for its default 3.5-second deadline, custom timing, overflow/edge handling, early reliable 100, and a final response racing the timer, plus no new automatic Trying timer in Legacy mode.
- [x] 3.3 Extend server snapshots with the selected behavior and enabled-mode pending Trying deadline; verify saved-mode restoration despite changed creation defaults, old snapshots defaulting to Legacy, overdue enabled activation, no response replay, timer cleanup, and rejection only when enabled mode lacks required pending timer state.
- [x] 3.4 Document opt-in/non-negotiated RFC 4320 behavior, default-off compatibility, and mode-preserving recovery; verify client regressions show 100 does not restart Timer F, Timer E behavior is retained, and both Legacy and RFC4320 server/client regression selections pass.

## 4. Stateful coordination and attempt scheduling

- [x] 4.1 Introduce per-request event coordination with asynchronous nonblocking callback ingestion and admission-time transaction association; verify immediate responses inside Start/send, callback reentrancy, concurrent duplicate admission, and no repeated authorization/resolution.
- [x] 4.2 Implement default parallel groups and sequential continuation with distinct logical-target/address-attempt state; verify A/B progress independently, C waits for permitted continuation, completed targets do not repeat, and invalid custom selections fail safely.
- [x] 4.3 Integrate lazy ordinary request attempts and bound custom senders without waiting in SendRequestStateful; verify fake-locator tests cover transport/timeout/503 next-address handling, distinct attempt branches, delayed fallback candidates, and no implicit DNS/dial after a failed custom binding.
- [x] 4.4 Add a minimal runnable stateful middleware example and group/policy usage documentation; verify compile-checked examples use optional-hook defaults and the stateful behavior tests demonstrate real request/response forwarding; include default-off usage and an explicit opt-in example, and verify the proxy preserves selected transaction options including matching CANCEL rather than enabling them implicitly.

## 5. Response processing and aggregation

- [x] 5.1 Implement transaction-event response processing and one-Via removal without mutating cached downstream messages; verify provisional 100 suppression, INVITE 101-199 forwarding, locally generated CANCEL responses staying local, body/tag preservation, and repeated/multiple INVITE 2xx forwarding.
- [x] 5.2 Implement negative response precedence, 401/407 challenge aggregation, preserved 3xx Contacts without recursion, and lone-503-to-500 policy; verify mixed-class, duplicate-challenge, 6xx precedence, and deterministic tie-selection tables.
- [x] 5.3 Enforce original-upstream transaction use and non-INVITE 408/late-response suppression; verify no replacement transaction is created, all-timeout non-INVITE completes silently, INVITE no-response produces 408, and fast 401 plus a silent branch follows RFC 4321 lifetime limitations, and its independent non-INVITE 408/late guards hold in both behavior modes.
- [x] 5.4 Preserve RFC 6026 Accepted transaction lifetime and transport-error behavior; verify 6xx-versus-2xx races, additional 2xx after cancellation, and upstream response-write failure without global downstream cancellation; document the response-selection and late-response rules.

## 6. CANCEL, ACK, and Timer C

- [x] 6.1 Implement matching-CANCEL acknowledgement and pending/deferred branch cancellation using existing transaction builders; verify CANCEL before provisional, after provisional, after a final, duplicate CANCEL, and unmatched stateless CANCEL without a new response context or automatic 481.
- [x] 6.2 Integrate non-2xx transaction ACK and end-to-end 2xx ACK routing; verify ACK never creates a client/server ACK transaction or receives a SIP response, and CANCEL is not challenged through a new authentication exchange.
- [x] 6.3 Add independent proxy Timer C configuration and attempt deadlines with a five-minute default and greater-than-three-minute validation; verify 100 does not refresh it, 101-199 does, small T1 cannot shorten it, and expiry chooses the correct cancellation/timeout behavior; document cancellation and expiry policy.

## 7. Stateless execution and ownership

- [x] 7.1 Implement keyed deterministic branch generation, one-destination selection, and retransmission-invariant Route/Record-Route transformations; verify magic-cookie and legacy INVITE/retransmission/CANCEL/non-routed-ACK fixtures, including To-tag differences and distinct proxy namespaces.
- [x] 7.2 Implement explicit stateless key/resolver configuration, ordinary pre-send promotion when safe stateless guarantees are unavailable, and no automatic post-send alternate retry; verify stable literal-IP/custom destinations, mutable-resolution/configuration handling, failed writes, and ACK/unmatched-CANCEL exceptions.
- [x] 7.3 Implement stateless response classification and topmost Via validation using explicit ownership markers; verify mixed stateful/stateless traffic on one element, foreign/no-remaining-Via responses, unknown stateful responses, and no transactions or provisional responses on the stateless path.
- [x] 7.4 Add compile-checked stateless usage documentation and deterministic-resolver obligations; verify examples state key/configuration lifetime requirements and do not advertise Outbound support or pretend ordinary weighted DNS is deterministic.

## 8. Explicit snapshot and restore composition

- [x] 8.1 Implement versioned isolated orchestration snapshots, stable per-request revisions, request lookup, and future-only snapshot subscriptions; verify clone isolation, unsupported opaque metadata, non-snapshotting custom transactions, reentrant observation, idempotent unbind tests, and saved non-INVITE behavior in snapshot values.
- [x] 8.2 Implement validated dormant restore with explicit transaction/delivery bindings and once-only activation; verify invalid versions, duplicate IDs, missing/mismatched bindings, no initial sends/events while dormant, and failed-restore cleanup that preserves unrelated registrations/resources, mismatched-behavior binding validation, and preservation of the saved mode when creation defaults changed.
- [x] 8.3 Resume preserved plan progress, final/cancellation decisions, and absolute Timer C deadlines; verify unstarted versus completed groups, overdue timers, deferred CANCEL, additional 2xx after activation, and no replay of completed forwarding decisions.
- [x] 8.4 Document application-owned consistent checkpoints and binding/activation order with compile-checked examples; verify a missing exact-flow binding fails without redial and neither snapshots nor docs promise atomic persistence or socket restoration; document saved-mode recovery and that complete RFC 4320 behavior requires explicit activation or equivalent application guarantees.

## 9. Proxy observation and statistics

- [x] 9.1 Add the typed proxy event/value/outcome contracts, handler/function adapter, and future-only bind/unbind API; verify zero-observer forwarding, multiple observers, value isolation, self-unbind/public-state inspection, concurrent subscription changes, and shutdown without internal-lock callbacks.
- [x] 9.2 Emit the five observation kinds at committed request, attempt-send, upstream-send, and outcome boundaries; verify successful versus failed initial sends, immediate callbacks, three-way forking, DNS retries, local rejection, timeout, cancellation-versus-2xx, multiple INVITE 2xx, and transaction retransmissions without duplicate logical-request/attempt counts. Verify stateless operations and standalone responses require no retained context or fabricated associations, and ACK/unmatched-CANCEL behavior is unchanged.
- [x] 9.3 Integrate StatsRecorder through explicit proxy subscription and add aggregate ProxyStats to StatsReport; verify exact counter deltas for the three-successful-forks case, failure/retry and terminal outcomes, separation from raw transport/transaction counts, concurrent updates/report reads, and compile-checked bind/unbind usage examples.
- [x] 9.4 Preserve committed observation decisions and sequence progress across snapshots without storing subscribers or counters; verify dormant restore emits nothing, activation does not replay historical events, new post-activation response sends remain observable, and documentation states subscription-lifetime/stateless-operation counting and the absence of durable exactly-once guarantees.

## 10. Lifecycle and cross-component acceptance

- [x] 10.1 Integrate proxy close/shutdown and middleware detachment with Element ownership; verify concurrent admission/callback/shutdown cases drain or terminate only owned work, release timers/indexes, and leave borrowed transports and storage open; document ownership.
- [x] 10.2 Add loopback UDP/TCP acceptance coverage for both forwarding modes, both non-INVITE server behavior modes, forking, cancellation, serialized routing headers, late non-INVITE suppression, and restore composition; verify tests use local/fake DNS and existing shared test infrastructure rather than external services.
- [x] 10.3 Run focused affected-package tests while working and a single final `task check` gate after configured formatting; verify tests/race/vet, lint, and vulnerability checks pass, every delta-spec scenario has coverage, API examples compile, docs carry explicit/conditional RFC 4320 conformance claims rather than claiming it for default-off, proxy event/statistics contracts are covered, and RFC 5626 implementation remains explicitly outside this change.

## 11. Verification follow-up

- [x] 11.1 Fix W1 by separating INVITE 2xx forwarding eligibility from logical settlement; verify exact retransmissions and additional successes on a retained client transaction before and after request completion, one-shot branch/request outcomes and initial-send observations, per-send response observations, and unchanged abandonment/upstream-lifetime guards; update response-handling documentation.
- [x] 11.2 Fix W2 by normalizing every component of the emitted stateless branch for related methods; verify full outbound Via branches and destinations for magic-cookie and legacy INVITE/retransmission/CANCEL/non-routed-ACK fixtures with To-tag differences, no client transactions, and unchanged loop/spiral detection; document the distinction from ACKs with a different transaction identity.
- [x] 11.3 Fix W3 by extending orchestration snapshot v1 in place with an independently cloned, required WorkingRequest; verify JSON round-trip continuation preserves preprocessing and local-route consumption without hook replay, original-request binding checks remain intact, opaque metadata and invalid/missing working requests fail safely, and snapshot mutation cannot affect live state; update recovery documentation and examples without changing transaction snapshot compatibility.
- [x] 11.4 Run a fresh final task check gate with atomic coverage, race/vet, configured lint, and vulnerability analysis, obtaining network permission where required; verify W1-W3 regression coverage and compile-checked examples, then run strict OpenSpec validation and git diff --check; record skipped or blocked checks explicitly rather than treating them as passing.

## 12. Restore validation and legacy coverage

- [x] 12.1 Fix V1 by validating the saved WorkingRequest's message structure and method compatibility and checking supported metadata on both saved request envelopes before claiming transaction bindings or installing subscriptions, indexes, or timers; add negative tests for missing Via, invalid Request-URI/SIP protocol, method mismatch, and unsupported metadata independently on the original and working requests; verify ErrInvalidProxySnapshot or ErrProxySnapshotUnsupported as appropriate, no installed state or sends, unchanged dormant bindings and unrelated live state, and valid-v1 continuation without hook replay, repeated local-route consumption, or transaction-snapshot compatibility changes.
- [x] 12.2 Address S1 with committed black-box forwarding regressions for both magic-cookie and legacy INVITE/retransmission/CANCEL/non-routed-ACK inputs with To-tag differences; assert complete emitted Via branches, destination/transport relationships, no transaction creation, and loop/spiral behavior; make the distinct-ACK fixture's different input identity explicit instead of relying on magic-cookie-specific helper behavior.
- [x] 12.3 Run focused V1/S1 regressions and existing W1-W3/recovery examples, then a fresh final task check gate with atomic coverage, race/vet, configured lint, and vulnerability analysis, obtaining network permission where required; run strict OpenSpec validation and staged/unstaged git diff --check; record skipped or blocked checks explicitly.

## 13. Whole-change verification follow-up

- [x] 13.1 Fix V2 by retaining incoming SIPS constraints through target/Route rewrites, outbound adjustment, and ordinary/custom delivery finalization; add committed stateful/stateless tests for SIP contacts and insecure adjusted/bound delivery, asserting preserved secure URI/transport or inspectable rejection before downstream send plus a passing SIP control; preserve Record-Route identity rules and update security/delivery documentation.

- [x] 13.2 Fix V5 by handing the accepted original/working requests and owned plan into pre-send stateful admission without replaying authorization, preprocessing, local-route consumption, or contact resolution; add hostname-driven promotion tests for hook invocation counts, retained rewrites/plan, original transaction matching, early-response association, duplicate handling, stateful ownership markers, snapshot v1 continuation, and one-shot request observations; keep per-target/attempt hook scopes and update promotion documentation.

- [x] 13.3 Fix V3 after 13.2 by enforcing one logical target and one bound destination before stateless sending; add tests for same-group and cross-group multi-target promotion retaining the complete plan and stateful scheduling, one-target zero-transaction controls, and ACK/unmatched-CANCEL routing failures without sends or forbidden transaction/context creation; preserve no post-send retry and related-method branch/destination rules, and update stateless policy documentation.

- [x] 13.4 Fix V4 by establishing proxy-local Via ownership before loop-fingerprint comparison; add enabled/disabled controls and two-independent-proxy tests in both forwarding modes with distinct identities/keys, shared branch formats, and matching routing fingerprints; retain owned true-loop detection behind foreign Vias, changed-route spiral acceptance, magic-cookie/legacy related-method normalization, and response ownership; use accurate sent-by doubles and update loop/identity documentation.

- [x] 13.5 Address D1 by updating obsolete comments about the transitional stateless pipeline, "Plan 3" branch work, and restored local-route replay; align them with the implemented promotion/ownership paths and adoption of saved WorkingRequest; verify a comment-only diff without behavior changes or unrelated formatting churn.

- [x] 13.6 Run focused V2-V5 regressions, existing W1-W3/V1/S1 and recovery/observation regressions, and compile-checked examples, then one fresh final task check gate with race/vet, atomic coverage, configured lint, and vulnerability analysis, obtaining network permission where required; run strict OpenSpec validation and staged/unstaged git diff --check, compare approved artifact/task coverage, and record actual outcomes plus any blocked or skipped checks without treating previous passing evidence as a new gate.
