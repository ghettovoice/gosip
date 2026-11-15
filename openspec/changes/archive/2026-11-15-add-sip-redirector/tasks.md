# Tasks

Planning only. Review the updated OpenSpec change first; prepare the detailed implementation plan after design approval. All implementation tasks remain unchecked until the plan is agreed and implementation is explicitly requested.

## 1. Component skeleton and configuration

- [x] 1.1 Add the `sip/redirector` package with `Route`, `Options`, `Redirector`, and `New`, including resolver-required and constant-status validation ({300, 301, 302}, zero meaning 302) and route-slice copying; verify with construction tests that invalid routes are rejected, defaults apply, and mutating the source configuration after construction has no effect.
- [x] 1.2 Implement `InterceptInboundRequest` claiming: closed-component pass-through, first-matching-route wins, no-match pass-through, ACK never claimed, and claimed requests not reaching the next receiver; verify with black-box interceptor tests over a stub receiver chain.
- [x] 1.3 Implement the one-shot `io.Closer` sealing; verify a closed component passes every request through unchanged and that repeated `Close` calls are safe.

## 2. Redirect response pipeline

- [x] 2.1 Implement the authorize-resolve-select-respond pipeline with the failure mappings (authorize error 403 without invoking the resolver, no contacts 404, resolver operational error 500); verify each mapping sends its status through `RespondStateful` and consumes the request.
- [x] 2.2 Implement 3xx response construction: contacts carried verbatim into the Contact header with `q` and `expires` parameters in resolved order, constant default 302, and `SelectStatus` receiving the request plus resolved contacts; verify per-request status selection, count-based 300 selection, and unsupported callback status producing 500 without a Contact header.
- [x] 2.3 Verify stateful sending end-to-end on an element: the response carries a To-tag, follows RFC 3261 section 18.2.2 response routing, and is observable on the wire in an element-level integration test.

## 3. CANCEL and retransmission behavior

- [x] 3.1 Implement matched-CANCEL claiming with 481 through `RespondStateful`; verify the CANCEL does not reach the next receiver, the 481 uses the CANCEL's own server transaction, and retransmissions of that CANCEL are absorbed.
- [x] 3.2 Verify redirect-response retransmission handling: a retransmitted original request while its server transaction is live resends the stored 3xx without re-invoking route callbacks; verify with a resolver invocation counter.

## 4. Integration, examples, and documentation

- [x] 4.1 Verify one component instance registered on two concurrently running elements answers each request through the element that received it; cover the cross-element integration in a black-box test.
- [x] 4.2 Add a runnable example wiring the redirector to an element with a contact resolver; verify the example runs and produces a 302 response with contacts.
- [x] 4.3 Add the `docs/redirector.md` user guide (role boundary, route configuration, status policy, CANCEL/ACK behavior, failure mapping) and update `docs/architecture.md` and `docs/future-architecture.md` status; verify the documented snippets compile as written.

## 5. Final verification

- [x] 5.1 Run one full `task test` pass with repository race/vet/timeout defaults followed by `golangci-lint run ./...`; verify successful exits, fix issues only in changed code, and review diffs for unrelated edits.
- [x] 5.2 Run `openspec validate add-sip-redirector --strict` and check every specification scenario against implementation evidence; verify the public API, docs, tests, and completed task marks agree before requesting archive.

## Workflow follow-up

- Obtain design approval before starting implementation. OpenSpec artifact completion does not imply that any implementation task is complete.
- Main-spec synchronization, archival, commits, and integration remain separate explicit workflow actions.
