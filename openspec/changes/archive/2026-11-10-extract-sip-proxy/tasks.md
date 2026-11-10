# Tasks

All tasks remain unchecked during proposal creation. Implementation starts only
on a new apply request. Use the project Go/testing/error conventions, reuse
`internal/testutil`, and keep each task's regression tests and documentation with
its implementation. Package extraction and statistics relocation are one
coordinated source migration, not separate compatibility releases.

## 1. Core forwarding provenance and public seams

- [x] 1.1 Add metadata-backed `MarkForwarded`/`Forwarded`, response To-tag preservation, and local-Via designation accessors on envelopes; verify false/absent/wrong-type values, independent clone replacement, and request/response JSON round trips in core message tests.
- [x] 1.2 Add provenance-aware envelope preparation and route element, transaction, attempt, and transport send preparation through it, removing private flags and the core dependency on `isPrxBranch`; verify `TestProxy_TransparentPreparation` plus designated-versus-upstream Via, missing tags, Max-Forwards zero, SIPS, and pre-wire UDP size-switch cases without nested envelope locks.
- [x] 1.3 Carry provenance through CANCEL construction and cached-response sends while retaining legacy transaction snapshot fields as compatibility mirrors; verify both INVITE/non-INVITE client/server restore families with legacy-only, metadata-only, and combined checkpoint fixtures and assert false legacy fields do not erase metadata.
- [x] 1.4 Expose `Element.NewCancelClientTransaction`, `LookupRequestAddrs`, and `ReportError` with the design's signatures and existing lifecycle/context rules; verify deferred CANCEL, cancelled/closed operations, lazy lookup admission, error filtering, and callback context in focused Element tests.
- [x] 1.5 Add `ClearTransactionBinding`, `CleanRequestURI`, and response `RequestTime` access using the existing semantics; verify only transaction metadata is removed, URI cleaning preserves ownership, and response request-time extraction supports unchanged Timestamp-delay behavior.
- [x] 1.6 Document core metadata keys, typed helpers, endpoint-versus-transit preparation, local-Via designation, and legacy checkpoint handling in their public comments; verify comments and runnable examples agree with the new tests and contain no proxy namespace dependency.

## 2. Proxy/statistics package extraction and processing ownership

- [x] 2.1 Move proxy implementation and its owned tests into `sip/proxy`, statistics into `sip/stats`, and apply the design's complete public rename table, including handler methods, errors, adapters, and constants; verify package imports compile without `sip -> proxy/stats` edges or compatibility re-exports and preserve serialized enum/error-class values.
- [x] 2.2 Implement `proxy.New(routes, opts)` without an element or automatic attachment and update test setup to explicit `UseMiddleware`; verify construction/validation installs no bindings, copied configuration remains isolated, and missing-element claimed calls fail inspectably before state or traffic while unmatched calls still pass through.
- [x] 2.3 Bind each processing operation and coordinator to its concrete context element, thread it through private helpers, and scope request/rejection indexes and fallback keys by element; verify concurrent equal-key requests on two real elements, independent CANCEL/ACK association, interleaved rejected-response sends, stable per-element fallback keys, and explicit-key isolation.
- [x] 2.4 Change public lookup to `LookupRequest(element, key)` and implement global proxy lifecycle without an owned unbind; verify nil/absent lookup, close-versus-admission races, cleanup under each request's element, post-close pass-through, close-then-unbind idempotency, and no closure of borrowed resources in lifecycle tests.
- [x] 2.5 Complete `stats.Recorder` integration using public core APIs and `proxy.EventHandler.HandleEvent`; verify the existing report, Timestamp/RTT, counter-concurrency, three-fork counting, explicit-subscription, and JSON-shape assertions in `sip/stats` tests without changing metric policy.
- [x] 2.6 Move white-box tests with their owning code, keep core-only tests in `sip`, and adjust black-box integration imports/shared doubles without exporting test-only internals; verify targeted proxy/statistics tests and package examples compile without test import cycles.
- [x] 2.7 Update proxy/stats package comments, constructor/statistics examples, `docs/proxy.md`, and project guidance for new imports, API names, explicit registration, and shared lifecycle ownership; verify documented report fields remain `Transports`, `Transactions`, and `Proxy` and examples pass their package Go test/vet checks.

## 3. Explicit restoration and observer element context

- [x] 3.1 Add required `RestoreBindings.Element`, make it authoritative over caller context, and install restored requests in the matching element namespace; verify nil rejection occurs before staging and two dormant restores with equal keys on different elements remain independently discoverable, retaining existing rollback/mode/binding checks.
- [x] 3.2 Reapply each request's bound element on every event and snapshot dispatch and when starting/restoring asynchronous work; verify admission, rejection, stateless response, drain, Timer C, cleanup, and restored activation with empty/conflicting transient contexts using `ElementFromContext` assertions and no new element fields in payloads.
- [x] 3.3 Update recovery comments, examples, and `docs/proxy.md` for explicit element rebinding, element-qualified lookup, and context-only observer access; verify dormant restore produces no effects, activation uses the bound element, and existing deadline/preprocessing/observation-replay regression tests still pass.

## 4. Serializable correlation and message attribution

- [x] 4.1 Add `proxy.Metadata`, string stages, `MetadataKey`, `MetadataFrom`, and `SetMetadata` exactly as designed; verify native values, JSON-decoded maps, valid partial stateless values, nil/absent/malformed inputs, whole-value replacement, and snapshot/sibling isolation in focused metadata tests.
- [x] 4.2 Unify operation/coordinator identity allocation with the existing observation/checkpoint IDs and preserve one identity through promotion; verify inbound/working/outbound metadata, events, and snapshots agree, inbound, working, and outgoing envelopes reflect effective stateful mode without mutating configured routes, and duplicate/transaction retransmission counts remain unchanged.
- [x] 4.3 Stamp downstream request attempts, attributed inbound responses, and upstream copies at their real processing boundaries; verify fork isolation, distinct physical retry IDs, immediate responses during send, local/aggregated responses without invented winners, and repeated/additional INVITE 2xx without repeated completion.
- [x] 4.4 Add known ACK/CANCEL related-request links and downstream CANCEL attribution without extra coordinators, events, or history; verify matched and unmatched method paths retain existing branch/response rules and source request metadata is not overwritten by the child operation.
- [x] 4.5 Keep stateless metadata partial and replace previous-traversal stamps; retire `sip.proxy_route` while retaining/resetting the separate finalize guard; verify unknown response IDs/routes remain empty, no request store appears, deterministic wire branches stay unchanged, and metadata never bypasses Via ownership checks.
- [x] 4.6 Restore/reconstruct message metadata from authoritative saved request/attempt identities on owned dormant copies and advance attempt allocation past saved IDs; verify full proxy-plus-transaction JSON restore, older supported snapshots without metadata, continued sequence/related IDs, distinct new attempts, and no replayed sends or observations.
- [x] 4.7 Document metadata fields, stages, partial stateless correlation, loop-back replacement, and the distinction from a complete event trace in package comments and `docs/proxy.md`; verify documented JSON/accessor examples match tests and make no cross-network tracing or authentication promise.

## 5. Metadata-based structured proxy logs

- [x] 5.1 Implement `Metadata.LogValue` with stable field names, readable mode/stage, and omitted unknown IDs; verify grouped output through ordinary slog JSON/text handlers, including partial stateless values and no arbitrary application metadata or authentication material.
- [x] 5.2 Use `slog.Any("proxy", metadata)` in proxy-owned logs when correlation is available; verify representative admission/attempt/response logs carry the matching stamp and document this usage without modifying core `MessageEnvelope.LogValue`, `pkg/log`, or introducing handler helpers.

## 6. Final integration verification

- [x] 6.1 Run one fresh full `task --force test` gate after targeted group checks; verify race detection, vet, migrated examples, and all existing SIP/proxy/recovery regression tests pass with the Taskfile defaults.
- [x] 6.2 Run a fresh `task --force lint` and inspect the complete implementation diff/import graph; verify no moved API references remain outside intentional migration documentation, no `sip -> proxy/stats` dependency or production slog handler was added, checkpoint/report JSON compatibility is covered, and lint reports zero issues.
