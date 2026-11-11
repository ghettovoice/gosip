# Tasks

## 1. Reusable default selection and proxy finalization

- [x] 1.1 Add table-driven tests in `sip/proxy/response_internal_test.go` for default resolver indices, including `[nil, 180, 404] -> 2`, `[600, 200] -> 1`, `[486, 401] -> 1`, `[480, 486] -> 0`, preferred-4xx ties, and empty/non-final-only input returning `-1` with an error; verify the new tests fail before implementing the exported resolver.
- [x] 1.2 Add `FinalResponseSelection` in `config.go` and `DefaultFinalResponseResolver(context.Context, FinalResponseSelection) (int, error)` in `response.go`, extracting the existing ranking without duplicating it; verify the new index tests and existing `TestProxy_SelectFinalResponse` pass.
- [x] 1.3 Separate private response finalization from ranking while preserving original-envelope identity when no rewrite is needed; verify existing and added tests for lone 503 mapping, multiple 503 candidates, authentication aggregation, To-tag preservation, and unchanged stored candidates.
- [x] 1.4 Document the standalone resolver's index, no-candidate error, ordering, and non-mutating behavior with its public API; add an external-package usage test or runnable example that compiles and checks a literal expected index.

## 2. Route callback and coordinator commitment

- [x] 2.1 Add route-level regression tests in `hooks_test.go` and `response_test.go` for nil/default equivalence, custom `[480, 486] -> 1`, explicit delegation, selection of 486 over 302, 404 over 401, preserved candidate metadata, and custom 401/407 aggregation; verify these expose the missing route extension before implementation.
- [x] 2.2 Add the ordinary `Route.ResolveFinalResponse` function field and integrate pending-resolution effects/result events in `stateful.go`; validate results against original candidates and retain the selected source index; verify the route tests pass and callback protocol-field/container mutations cannot change the selected original response.
- [x] 2.3 Add and pass failure-path tests for negative/out-of-range indices, selecting 486 despite 600, returned errors including `sip.RequestRejectedError`, preserved causes, a single abandoned completion, and unavailable upstream transactions; verify failure produces 500 only through the original usable transaction with no silent default or replacement transaction.
- [x] 2.4 Add and pass channel-synchronized tests for reentrant lookup/snapshot capture, duplicate finish events, late INVITE 2xx during a blocked resolver, stale successful/error callback results, and shutdown before callback return; verify no deadlock, no conflicting negative send, and no duplicate completion under `go test -race -vet=all -timeout=1m ./sip/proxy`.
- [x] 2.5 Add and pass bypass/regression cases for INVITE successes and additional 2xx, existing successful non-INVITE outcomes, no candidates, cancellation without candidates, excluded non-INVITE 408, stateless forwarding, and pre-send promotion retaining the route callback; verify the existing scheduling, cancellation, and response-ownership tests still pass.
- [x] 2.6 Update route/API documentation and add a runnable example showing an inline callback that delegates to `DefaultFinalResponseResolver`; document read-only detached inputs, shared opaque metadata values, mandatory versus recommended preferences, explicit failure behavior, and the unchanged 503 handling; verify the example compiles/runs in the proxy package tests.

## 3. Durable candidate identity in the current snapshot format

- [x] 3.1 Add failing tests in `snapshot_test.go` and `snapshot_internal_test.go` for restoring the second of two 486 candidates with distinct tags/headers after JSON round trip, including restoration with a changed or error-returning callback that must not run.
- [x] 3.2 Extend the current version-1 snapshot layout in place with independently cloned `FinalCandidateIndex *int` and reconstruction from the saved index in `snapshot.go`; remove default reranking from validation/installation; verify exact selected content, custom cross-class decisions, lone-503 mapping, and authentication aggregation survive recovery without replaying sends or completion observations.
- [x] 3.3 Add and pass table-driven restore validation cases for missing, negative, out-of-range, extraneous, nil/provisional, method-suppressed, and success/6xx-conflicting selections, plus processed-status mismatches; verify every rejection happens before claiming bindings, invoking policy, or producing network effects.
- [x] 3.4 Keep the snapshot version at 1 without a compatibility branch; verify captures retain that version, completed response checkpoints without the required index are rejected without inferred/default reconstruction or callback invocation, and snapshot versions other than 1 remain unsupported.
- [x] 3.5 Integrate unstable-capture handling for pending resolution and test independent index-pointer cloning, unfinished snapshots without selection, all-settled uncommitted restoration, and successful-outcome index capture; verify new recovery cases and existing snapshot/lifecycle tests pass under race detection.
- [x] 3.6 Update recovery documentation and examples for the selected index, the in-place version-1 layout change without old-layout compatibility, and application-owned checkpoint ordering; verify examples compile and the documented fields match the serialized JSON tests.

## 4. Integration verification

- [x] 4.1 Run `go test -race -vet=all -timeout=1m -covermode=atomic -coverprofile=cover.out ./...` once as the final test gate; verify all packages pass and report any unrelated baseline failures explicitly.
- [x] 4.2 Run the configured formatting checks and `golangci-lint run ./...` after all implementation changes; verify zero issues without weakening configuration and review formatter edits for unrelated changes.
- [x] 4.3 Review the final diff against both delta specs and the design, then run `openspec validate add-proxy-final-response-resolver --strict`; verify every scenario has test coverage and no transaction-layer, dependency, stateless-policy, or unrelated configuration changes were introduced.
