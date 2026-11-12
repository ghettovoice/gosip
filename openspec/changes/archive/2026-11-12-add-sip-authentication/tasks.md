# Tasks

Planning only. Review the updated OpenSpec change first; prepare the detailed implementation plan after its approval. All implementation tasks remain unchecked until the plan is agreed and implementation is explicitly requested.

## 1. Public contracts and error boundaries

- [x] 1.1 Add `sip.ClientAuthenticator` and `sip.ServerAuthenticator[P]` in `sip/auth.go` with the design's exact method signatures and ownership/zero-result contracts; verify compile-time examples using both a struct and pointer principal and an application-defined implementation.
- [x] 1.2 Add shared `ErrAuthenticationRequired`, `ErrInvalidCredentials`, and `ErrUnsupportedChallenge` conditions using repository error conventions; verify wrapped/joined inspection and the distinction between a challenge rejection and an ordinary callback failure in focused `sip` error tests.
- [x] 1.3 Add the `sip/auth` package documentation and the `docs/authentication.md` contract/ownership section, and update `docs/architecture.md` to describe optional authentication mechanics without giving the core ownership of policy; verify dependency direction and explicitly documented absence of automatic sending, retry, identity storage, and authorization.

## 2. Digest header and parser compatibility

- [x] 2.1 Add regression coverage for the existing RFC 8760 grammar/parser with MD5, SHA-256, empty-placeholder, and unknown-algorithm parsing cases for both credential header families; verify with the grammar and `sip/header` package tests without repeating the already completed ABNF edit or parser generation.
- [x] 2.2 Replace MD5-only `DigestCredentials.IsValid` length checking with algorithm-aware hexadecimal validation while retaining extension-algorithm syntax; verify omitted-algorithm MD5 semantics, known MD5/SHA variants and case-insensitive algorithm names, valid 32/64-digit cases, length mismatches, non-hex input, and rejection of an empty authentication proof. Cover existing `Authorization.IsValid`/`ProxyAuthorization.IsValid` delegation and render/clone/equality/JSON behavior without rewriting methods that already work.
- [x] 2.3 Add lossless `DigestCredentials.URIText()` with the private original-text/rendered-snapshot behavior from the design and use it for credential rendering; verify noncanonical parameter order and escaping through parse, clone, render, and JSON, plus programmatic credentials and typed-URI mutation fallback.
- [x] 2.4 Document response-size and signed-URI behavior in header godoc and `docs/authentication.md`; verify existing MD5 header snapshots remain valid and `go test -race -vet=all ./sip/header ./internal/grammar/rfc3261` passes.

## 3. Digest calculation and client

- [x] 3.1 Add the supported algorithm resolver, qop=auth calculation, fixed-size digest validation/comparison, and exported `auth.DigestHA1`; verify published independent MD5 and SHA-256 vectors, including the expected HA1/HA2/response values, rather than calculating test expectations with the helper under test.
- [x] 3.2 Add `NewDigestClient` and `DigestClientOptions` with realm-scoped detached credential callbacks (including the proxy-leg flag), SHA-256-only default, explicit MD5 policy, and bounded nonce sessions; verify constructor errors/copying, omitted-algorithm MD5 semantics, SHA-256 preference even when MD5 is advertised/listed first, qop selection, initial count 00000001, concurrent counter reservation, capacity eviction, and exhaustion without wrap.
- [x] 3.3 Implement header-only `DigestClient.AuthenticateRequest` over both challenge families with stronger-first selection, verbatim opaque echo, and prepare-before-commit semantics; verify aggregated 401/407, same-realm/different-leg credential choice, preservation of other realms/schemes and non-auth fields, response immutability, no provider call for malformed/unsupported challenges, and unchanged request on any provider error.
- [x] 3.4 Document Digest client configuration, credential scoping, algorithm defaults, opaque handling, and caller-owned sending in godoc and `docs/authentication.md`; verify client tests cover valid method/URI inputs without transaction headers, ACK/CANCEL, nil/uninitialized/non-challenge inputs, reentrant detached callbacks, and cancellation before header commitment.

## 4. Digest server and replay state

- [x] 4.1 Add `ServerOptions`, `DigestServerOptions[P]`, and `NewDigestServer[P]`, including HA1 lookup, URI acceptance policy, nonce TTL/capacity defaults, and capacity sufficient for a complete algorithm challenge set; verify configuration/zero-value failures and typed principal behavior.
- [x] 4.2 Implement bounded instance-local nonce issuance, expiry, username binding, and atomic post-verification count commitment without a cleanup goroutine; verify unknown/evicted/wrong-algorithm nonces, count zero/reuse/regression/overflow, and that a bad high-count proof never consumes legitimate replay state.
- [x] 4.3 Implement 401/407 challenge rejections with ordered algorithms and distinct nonces; verify no-header/bad-proof/unknown-account outcomes, retained valid expired proofs producing stale=true, unknown or pruned nonce producing a normal challenge, and no principal on failure.
- [x] 4.4 Implement verification using original credential URI text, method/realm/algorithm/qop checks, the existing URI Equal operation for default target acceptance, and explicit forwarded-target validation; verify signed noncanonical URIs, equivalent differently spelled targets, accepted/rejected forwarding, constant-size proof checks, case-sensitive identities, malformed backend HA1, and operational-error preservation.
- [x] 4.5 Add deterministic expiry and concurrency tests using `testing/synctest` and coordinated callbacks; verify at most one success for duplicate proofs, expiry/eviction while lookup runs, cancellation before replay commitment, and documented strict-count rejection of reordered requests.
- [x] 4.6 Document HA1 lookup, nonce defaults, strict count/retransmission semantics, restart/replica limitations, and application ownership of a successful `P`; verify the Digest server tests pass under race detection and that documentation matches the externally observable nonce policy.

## 5. Bearer client and server

- [x] 5.1 Add `BearerClientOptions`, `BearerServerOptions[P]`, and constructors with copied trusted HTTPS AS configuration and required callbacks; verify missing/invalid trust configuration, absolute HTTPS URL with host and no userinfo/fragment, zero-value safety, detached inputs, and no automatic AS network access.
- [x] 5.2 Implement realm/AS-scoped Bearer client challenge handling with a proxy-leg callback flag and token syntax checks; verify both challenge families, same-realm leg distinction, scope/error forwarding, byte-identical rendered AS matching with no implicit URL aliases, atomic provider failures, and empty/whitespace/CRLF token rejection.
- [x] 5.3 Implement Bearer credential application without guessing ownership of existing tokens; verify preserving another service's tokens and other schemes, deduplicating an identical token in the same family, and leaving CSeq/Via/body/routing and the response unchanged.
- [x] 5.4 Implement typed Bearer server validation over candidates in the configured header family; verify opaque non-JWT token success, invalid-token continuation to a valid candidate, fail-closed operational errors, missing-versus-invalid challenge fields, and zero principal on failure.
- [x] 5.5 Document trust, token protection, validator responsibilities, no JWT/OAuth implementation, and deliberate absence of stale-token pruning; verify examples never log tokens/principals and Bearer tests cover ACK/CANCEL, reentrant callbacks, and cancellation before successful return.

## 6. Integration and final verification

- [x] 6.1 Add runnable client and generic server/proxy-hook examples that consume `P` explicitly; verify examples compile/run without a live OAuth server, automatic retry, or principal metadata serialization.
- [x] 6.2 Add focused integration cases composing real authenticators with the existing proxy authorization hook; verify 407 challenges use the existing response path, operational failures are not disguised as 407, and stateful retransmissions do not repeat authentication. Preserve existing ACK/CANCEL, stateless, and challenge-aggregation behavior without changing hook signatures.
- [x] 6.3 Run one final full `task test` pass with repository race/vet/timeout defaults, followed by `golangci-lint run ./...`; verify successful exits, fix issues only in changed code, and review generated/formatting diffs for unrelated edits.
- [x] 6.4 Run `openspec validate add-sip-authentication --strict` and check every specification scenario against implementation evidence; verify the public API, docs, tests, and completed task marks agree before requesting archive.

## Workflow follow-up

- Obtain design approval before starting implementation. OpenSpec artifact completion does not imply that any implementation task is complete.
- Main-spec synchronization, archival, commits, and integration remain separate explicit workflow actions.
