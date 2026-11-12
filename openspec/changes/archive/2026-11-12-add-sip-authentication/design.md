# Design

## Context

See [proposal.md](proposal.md) for motivation and scope.

The current code provides:

- Typed Digest/Bearer credentials and challenges in `sip/header`, and iterators over both authentication header families in `sip.Headers`.
- An application-owned `proxy.Route.AuthorizeRequest(context.Context, *sip.RequestEnvelope) error` hook.
- `sip.RequestRejectedError`, which carries a suggested SIP status and response headers without sending a response.
- Message envelopes that clone protocol data for readers; `WithMessage` takes an exclusive message lock.
- A Go 1.27 minimum toolchain.

There are three relevant wire constraints:

1. The `request-digest` and `algorithm` rules in `internal/grammar/rfc3261/rules.abnf` and the generated parser already include the RFC 8760 updates. `DigestCredentials.IsValid` still assumes a 32-character MD5 response; credential-level validation remains to be updated.
2. `uri.SIP` renders URI parameters in sorted order. Re-rendering a parsed credential URI is therefore not a lossless way to recover the string used in Digest A2.
3. SIP Bearer challenges use RFC 8898, including an HTTPS `authz_server`; they are not just HTTP Bearer challenges with a realm.

Existing proxy requirements already cover authentication hooks, response transport, retransmission ownership, challenge aggregation, and ACK/CANCEL handling. This change adds a separate `sip-authentication` capability, not another proxy pipeline.

## Goals / Non-Goals

**Goals:**

- Keep consumer-owned contracts in `sip` and optional implementations in `sip/auth`.
- Return an application-defined server principal directly, preserving its Go type.
- Make authentication usable without an `Element`, transport, proxy, or user agent.
- Separate successful authentication, protocol challenges, unsupported client challenges, and operational failures.
- Keep shared mechanism state bounded, concurrency-safe, and independent of background goroutines.

**Non-Goals:**

- Sending, resending, waiting for responses, CSeq/Via management, INVITE ACK generation, or automatic authentication middleware.
- Inferring application identity from `From`, implementing permissions, or silently storing a principal in context/message metadata.
- OAuth flows, JWT/JWE implementation, token refresh scheduling, or authorization-server discovery.
- Digest `auth-int`, `-sess` algorithms, SHA-512-256 computation, userhash, mutual authentication, Authentication-Info, or legacy no-qop proof generation.
- A public nonce-store abstraction, distributed replay protection, or persistence.

## Decisions

### 1. Contracts in sip, mechanisms in sip/auth

Proposed public contracts in `sip/auth.go`:

```go
type ClientAuthenticator interface {
    AuthenticateRequest(
        ctx context.Context,
        req *RequestEnvelope,
        res *ResponseEnvelope,
    ) error
}

type ServerAuthenticator[P any] interface {
    AuthenticateRequest(
        ctx context.Context,
        req *RequestEnvelope,
    ) (P, error)
}
```

The client receives an application-owned retry request and the received challenge response. It only applies authentication headers. The server inspects an inbound request and returns the authenticated principal or an error.

The two interfaces deliberately have different signatures. Neither embeds the other. No `AuthResult[P]`, common user model, or principal constraint is introduced. `P` can be a value, pointer, interface, or application wrapper. Success is determined solely by a nil error; on failure implementations return the zero value of `P`.

Implementations and constructors:

- `auth.DigestClient`, `auth.NewDigestClient(DigestClientOptions) (*DigestClient, error)`.
- `auth.BearerClient`, `auth.NewBearerClient(BearerClientOptions) (*BearerClient, error)`.
- `auth.DigestServer[P]`, `auth.NewDigestServer[P](DigestServerOptions[P]) (*DigestServer[P], error)`.
- `auth.BearerServer[P]`, `auth.NewBearerServer[P](BearerServerOptions[P]) (*BearerServer[P], error)`.

Options use ordinary function fields, following existing callback conventions. Constructors validate required fields and copy mutable configuration. Constructed instances are safe for concurrent calls on different requests. Their zero values fail with an ordinary configuration error rather than panic or authenticate.

**Alternatives:** Keeping interfaces in `sip/auth` would require future `sip` consumers to redeclare compatible contracts or depend on their own implementation package. Returning `any` loses principal type safety. A result wrapper with `Subject` duplicates identity information the library does not consume. Combining client and server interfaces forces applications to implement unused operations.

### 2. Explicit callback configuration, not an account/token framework

All four basic implementations require a non-empty, case-sensitive `Realm`. One instance handles one configured realm. Digest usernames are also case-sensitive. Authentication comparisons do not reuse the header model's case-folded realm equality.

Shared `auth.ServerOptions` contains:

- `Realm string`.
- `Proxy bool`: false uses 401 plus WWW-Authenticate and reads Authorization; true uses 407 plus Proxy-Authenticate and reads Proxy-Authorization.

`DigestServerOptions[P]` and `BearerServerOptions[P]` embed `ServerOptions`. The 401 zero/default is a design proposal, not an existing proxy behavior; a proxy application explicitly sets `Proxy: true`.

`DigestClientOptions` contains:

- `Realm string`.
- `Algorithms []string`: an allowlist; nil defaults to SHA-256 only, and explicit configuration can allow MD5. The client prefers SHA-256 over MD5 independently of slice/challenge order.
- `MaxNonces int`: zero selects a 1024-entry client nonce-state limit.
- `Credentials func(ctx context.Context, req *sip.Request, challenge *header.DigestChallenge, proxy bool) (username string, password string, err error)`.

The request and challenge passed to providers are detached inputs. Their mutation does not edit the original request or response, or change the selected challenge used for calculation. The callback can choose credentials using the request target and local trust policy. `proxy` is false for WWW-Authenticate and true for Proxy-Authenticate, so the same realm on the two legs does not force the same account or token.

`DigestServerOptions[P]` contains:

- `Algorithms []string`: nil defaults to SHA-256 only; order is challenge preference.
- `NonceTTL time.Duration`: zero selects five minutes.
- `MaxNonces int`: zero selects 4096 issued nonce records.
- `LookupHA1 func(ctx context.Context, username, realm, algorithm string) (ha1 string, principal P, err error)`.
- Optional `ValidateURI func(context.Context, *sip.Request, uri.URI) error`.

The HA1 callback avoids requiring the server to retrieve plaintext passwords. Export `auth.DigestHA1(username, realm, password, algorithm string) (string, error)` for applications that need to compute HA1 from a password. It returns the lowercase hexadecimal H(username:realm:password) for a supported algorithm. An empty password is not intrinsically invalid.

`BearerClientOptions` contains:

- `Realm string`, a configured trusted `AuthzServer uri.URI`, and `Token func(ctx context.Context, req *sip.Request, challenge *header.BearerChallenge, proxy bool) (string, error)`.

`BearerServerOptions[P]` contains:

- A configured HTTPS `AuthzServer uri.URI`, optional `Scope string`, and `ValidateToken func(context.Context, *sip.Request, string) (P, error)`.

Server validation receives a detached request so the application can check token audience and its binding to the service/request. The library does not parse unverified claims or treat a token's non-empty spelling as authentication.

Missing callbacks, invalid/non-HTTPS AS URIs, empty realms, negative TTL/capacity, unsupported algorithms, duplicate algorithm entries, and explicitly empty algorithm lists fail construction. HTTPS AS validation requires an absolute URL with a non-empty host and no userinfo or fragment, not merely a URI with an https scheme token. The server nonce capacity must fit at least one complete set of enabled algorithm challenges, so issuing a response does not evict that response's own preferred nonce. Algorithm names are case-insensitive and normalized to their canonical spelling. URI/options slices are copied. There is no default credential source or accept-all validator.

**Alternatives:** Fixed password/token fields are simpler for a demo but do not provide target-aware credential selection or typed account lookup. A general provider/store hierarchy adds interfaces without a current consumer. Callbacks keep the base implementations small and leave storage, secrets, and network policy with the application.

### 3. Reuse SIP rejection errors for challenges

Add shared error conditions in `sip` so future core consumers do not import `sip/auth`:

- `ErrAuthenticationRequired`: the server requires another authentication exchange.
- `ErrInvalidCredentials`: the application's explicit negative account/token lookup result.
- `ErrUnsupportedChallenge`: the client cannot handle a matching challenge under its configured mechanism/trust/algorithm policy.

A server challenge is a wrapped `*sip.RequestRejectedError` with level Debug, the configured 401/407, and typed challenge headers in `RespondOptions.ResponseOptions.Headers`. Its cause matches `ErrAuthenticationRequired`; when supplied by a callback, `ErrInvalidCredentials` remains discoverable too. Unknown users and invalid passwords receive equivalent public challenges. Library-authored error messages do not include a username, password, HA1, token, digest response, or request dump. Applications must also keep their own callback errors free of secrets; preserving a callback's cause is not a claim that the library can sanitize that cause's text.

Callbacks use `ErrInvalidCredentials` for an absent account or invalid/expired/unacceptable token. Other errors, including cancellation, deadlines, malformed HA1 returned by the backend, and storage/introspection failures, remain ordinary inspectable errors. They are not translated into 401/407 or invalid-credential challenges. Invalid configured/client input is also an ordinary error, not a challenge to send.

When several relevant credential headers exist, a server can accept any valid one. It skips explicit invalid-credential candidates but fails closed on an operational callback error rather than hiding it behind another candidate. Server failures never return a partial principal.

ACK and CANCEL are not new authentication exchanges. Both client and server implementations reject direct attempts to authenticate these methods with an ordinary `sip.NewRequestMethodNotAllowedError`, without challenge headers or nonce allocation. Existing proxy dispatch already bypasses ordinary authorization for these methods and remains unchanged.

**Alternatives:** A new challenge-error hierarchy would duplicate `RequestRejectedError` and need another adapter. Returning challenges as successful values would require an additional result wrapper and ambiguous success handling.

### 4. Client application is header-only, atomic, and realm-scoped

`AuthenticateRequest` accepts only non-nil envelopes containing non-nil messages and a 401 or 407 response. An initialized request has a non-empty syntactically valid method and a valid Request-URI. It need not have Via, CSeq, Call-ID, or a non-nil header map: full `Request.Validate` is not the authentication precondition. Invalid inputs produce ordinary errors; nil/structurally invalid challenge values are skipped without provider calls.

An aggregated 401/407 can contain both WWW-Authenticate and Proxy-Authenticate. Inspect each family independently, selecting at most one supported challenge for the configured realm in each family. Digest prefers SHA-256 over MD5 among compatible enabled candidates and preserves wire order among candidates using the same algorithm. This is explicit local policy under RFC 8760 Section 2.4. Unrelated or unsupported schemes/realms are ignored.

Bearer additionally compares the challenge AS URI's rendered representation byte-for-byte with the copied configured HTTPS URI's rendered representation. It adds no auth-specific normalization of host case, default ports, path segments, percent encoding, or trailing slashes, and follows no redirects. A different spelling requires explicit trust configuration rather than being treated as an implicit alias.

The client prepares all selected credentials before editing the request. A provider error or invalid returned token leaves the request unchanged. If neither family has a compatible challenge, return `ErrUnsupportedChallenge` and do not call a provider.

On success, update Authorization for a selected WWW challenge and Proxy-Authorization for a selected proxy challenge. Replace only credentials owned by this mechanism and protection space, preserving other realms, schemes, header families, bodies, routing fields, and unrelated headers. For Bearer, whose credential syntax has no realm, see Decision 8's ownership rule.

No Call-ID/CSeq correlation between the supplied response and retry request is enforced here: the future exchange owner must establish that association before invoking the mechanism. The caller must exclusively own the retry request during the call.

**Alternatives:** Blind `Headers.Set` would erase other realms' credentials and break sequential proxy challenges. Selecting a header family solely from the status would mishandle aggregated 401/407 challenges. Cloning/retrying complete transactions is a separate, explicitly deferred layer.

### 5. Digest is qop=auth with SHA-256 by default

The base implementations compute MD5 and SHA-256, but MD5 is available only when explicitly enabled. No silent downgrade occurs. A server advertises one challenge per enabled algorithm, in configured preference order, each with a distinct issued nonce and `qop="auth"`.

For a supported challenge with no qop parameter, the client uses implicit `auth` as described by RFC 8760 and emits qop, cnonce, and nc. A challenge offering only unsupported qop values is skipped. The server issues and accepts only explicit `qop=auth` proofs; it does not accept legacy no-qop proofs without nonce counts.

Use the RFC Digest calculation:

```text
HA1 = H(username ":" realm ":" password)
HA2 = H(method ":" digest-uri)
response = H(HA1 ":" nonce ":" nc ":" cnonce ":" qop ":" HA2)
```

Method spelling and the digest URI value are hashed without case folding or URI normalization. Responses and HA1 are validated as algorithm-sized lowercase hexadecimal values and compared as fixed-size decoded bytes using constant-time comparison. `nc` is an eight-digit hexadecimal value in the range 1..ffffffff; cnonce must be non-empty. Realm, nonce, algorithm, and qop must match the issued challenge.

The client copies the selected challenge's opaque value verbatim into credentials, without interpretation or including it in the digest formula. An absent opaque remains absent. The basic server does not generate opaque values; its nonce record already carries the necessary challenge state.

Use `crypto/rand` for server nonces and client cnonces, with at least 128 random bits each. The client maintains a bounded per-nonce counter shared across concurrent requests, scoped to the header family and realm. A fresh session starts at `00000001`. It never wraps the counter; exhaustion is an ordinary non-challenge error with no request mutation, not `ErrUnsupportedChallenge`. Expiry/eviction may require a new challenge, never reuse an already accepted count as a successful proof.

An omitted algorithm means MD5 according to the protocol, not SHA-256. It is therefore accepted only by an explicitly MD5-enabled client/server policy.

**Alternatives:** MD5-only is interoperable with older devices but an unsuitable new security default. Selecting the first wire-supported algorithm follows server preference, but would choose MD5 ahead of an available SHA-256 challenge when both are enabled. The basic client instead has a stronger-first local policy. Implementing every RFC 7616 feature would grow a basic mechanism into a much larger framework. The header parser remains extensible even when the base authenticators reject an unsupported computation.

### 6. Bounded stateful nonce validation and replay commitment

`DigestServer[P]` owns a bounded in-memory store; it is not exported as another extension interface. Each record includes its realm, algorithm, expiry, optional authenticated username binding, and highest accepted nonce count.

Authentication snapshots a record, validates credentials outside the state lock, and atomically rechecks the record, expiry, username binding, and count before returning a principal. Only a valid proof can bind a nonce to a username or advance its count. A lost concurrent race, removed/expired nonce, count zero/reuse/regression, or wrong algorithm cannot succeed.

Counts must strictly increase per issued nonce. Changing cnonce does not reset the count. Two simultaneous copies of one proof cannot both succeed. A later-arriving lower-count request can be challenged again; the initial implementation does not provide an out-of-order replay window.

Recognized expired nonces with an otherwise valid proof yield a fresh challenge with `stale=true`. An unknown/evicted nonce or a bad proof gets a normal fresh challenge, not a stale assertion. Expired records can be pruned opportunistically; after removal their nonces are simply unknown. Eviction never recreates an issued record on credential presentation.

Capacity pressure evicts records rather than allocating unbounded state. The client also bounds nonce sessions, using fresh cnonce/state after eviction; the server can challenge an old nonce whose reset count no longer advances. No cleanup goroutine, `Start`, `Close`, or lifecycle registration is needed.

**Alternatives:** Accepting any client-supplied nonce permits fabricated proofs and replay. A signed stateless nonce alone does not enforce nonce-count replay protection. A public/distributed store API and sliding replay window are deferred until a deployment needs them.

### 7. Preserve the signed URI without changing general URI semantics

The RFC 8760 `request-digest` and `algorithm` rules are already present in `internal/grammar/rfc3261/rules.abnf` and the generated `rules.go`. Treat them as the existing baseline rather than repeating the grammar edit or generation. Add regression tests for MD5/SHA-256, unsupported extension algorithms, and the empty pre-challenge placeholder in both credential header families.

Update `DigestCredentials.IsValid` to validate hexadecimal syntax and algorithm-dependent lengths rather than assuming MD5 for every algorithm. Known MD5 variants use 32 digits; known SHA-256/SHA-512-256 variants use 64. Unknown algorithm tokens retain structurally valid, even-sized hexadecimal values of at least 32 digits for custom consumers. An empty placeholder can parse but is not an authentication proof.

The header-method work is separate from the completed grammar update. Verify that `Authorization.IsValid` and `ProxyAuthorization.IsValid` expose the corrected credential validation through their existing delegation. Cover rendering, cloning, equality, and JSON round trips for the expanded Digest values; do not duplicate algorithm checks in wrappers or rewrite methods that already handle these values correctly.

Add `(*header.DigestCredentials).URIText() string`, returning the unquoted digest URI value. Parsing stores the exact Request-URI node text and a private snapshot of its typed URI rendering. If the current typed `URI` renders like that snapshot, `URIText` returns the original text; otherwise it returns the current URI rendering. For programmatically constructed credentials, it returns the typed URI rendering directly.

Credential rendering uses `URIText`, and cloning/JSON header round trips preserve that representation. This avoids exposing two independently editable public URI fields and avoids changing the URI package's canonical rendering rules. A caller changing a signed URI must recalculate the response.

The server hashes `URIText`, not a normalized URI. Separately, its default target policy uses the existing parsed credential URI's `Equal` operation against the current request URI. In particular, SIP/SIPS use `uri.SIP.Equal` and its established RFC 3261 comparison rules; auth introduces no second URI normalization/equality implementation and does not compare raw signed strings for target acceptance. `ValidateURI`, when supplied, replaces that policy and must explicitly accept only direct/forwarded targets for which the application is willing to authenticate. It returns `ErrInvalidCredentials` to reject a target and an ordinary error for an operational failure. This is the controlled allowance for forwarded targets in RFC 8760 Section 2.6, not blanket acceptance of a foreign signed URI.

**Alternatives:** Re-rendering a URI before hashing rejects valid external clients with different parameter order/escaping. Comparing only the current Request-URI breaks legitimate forwarding. Public raw and typed URI fields with undefined precedence would make edits unsafe.

### 8. Bearer remains an opaque, application-validated credential

The server advertises its configured realm, HTTPS AS URI, and optional scope using `header.BearerChallenge`. Invalid/expired tokens add `error="invalid_token"`; an initial missing-token challenge has no error parameter. Different realms/services must be distinguished by token validation, not by a realm field invented on BearerCredentials.

The server checks all Bearer credentials in the configured header family, passing each token and detached request to `ValidateToken`. A valid callback result returns `P`; an explicit invalid result can continue to another candidate; an operational error stops processing. Token signature, audience, issuer, expiry, scope acceptance, and decryption remain the validator's responsibility.

The client calls `Token` only after the realm and trusted AS match. It never fetches the challenge URL, follows redirects, or sends a refresh token. A returned token must be non-empty and match the Bearer token grammar, excluding whitespace/CRLF injection.

Because a Bearer credential contains no realm, an existing value cannot safely be assigned to the current realm by parsing alone. The client adds its provider-returned token unless an identical Bearer token is already present, preserving other Bearer tokens and other schemes. It does not blindly replace every Bearer header. This operation is idempotent for the same token but does not promise old-token pruning; a future exchange owner can manage that with explicit protection-space ownership.

**Alternatives:** Treating every token as a JWT rejects legitimate reference tokens. Blindly using a challenged AS URL risks token disclosure and unintended outbound requests. Overwriting all Bearer credentials can break another proxy's authentication.

### 9. Keep principal ownership and protocol orchestration with the caller

A proxy application closes over its `sip.ServerAuthenticator[*User]`, calls it from `Route.AuthorizeRequest`, handles the returned principal explicitly, applies its own authorization policy, and returns the error. No generic result adapter that discards the principal is added. No implicit serialization of `P` into message metadata occurs.

All providers/validators run without envelope or nonce locks held. Cancellation is checked before callbacks and before committing headers or replay state; callback contexts preserve caller cancellation/deadlines. Providers must themselves honor context. The authenticators do not launch goroutines to disguise a blocking callback.

Documentation must explain that stateful transaction retransmissions are handled before the proxy hook, whereas re-invoking the authenticator directly on a proof is a replay. Stateless routes do not acquire a hidden transaction/cache; applications needing retransmission reuse choose the existing stateful path.

Future `sip` exchange helpers can consume these interfaces and own retries, challenge negotiation across mechanisms/realms, and principal propagation. None are introduced by this change.

## Risks / Trade-offs

- [MD5 is obsolete] -> SHA-256-only default; MD5 requires explicit opt-in and a documented compatibility warning.
- [An attacker can remove stronger challenges when weak fallback is allowed] -> Prefer SHA-256 when present, do not claim this detects challenge stripping, and use a trusted protected transport or disable MD5.
- [Authentication is not transport security] -> No send/protection guarantee is implied. Applications must protect Digest traffic and ensure Bearer tokens are visible only to authorized SIP elements, using TLS plus encrypted tokens or equivalent trusted-path protection as appropriate under RFC 8898.
- [Application validator mistakes remain security-sensitive] -> No built-in accept-all behavior; document issuer/audience/expiry/scope checks, trusted AS selection, and token protection requirements.
- [Equivalent challenges do not guarantee equal account-lookup timing] -> Constant-time digest comparison is not a whole-request timing guarantee. Applications must control lookup behavior, rate limiting, and transport exposure; this initial mechanism does not promise timing-resistant account lookup.
- [In-memory nonces do not span restarts/replicas] -> Restart/eviction produces a new challenge; deployments need instance affinity or a custom server authenticator for distributed verification.
- [Strict nonce counts can reject reordered valid requests] -> Document the policy and test fail-closed behavior; a replay window is a separate future capability.
- [Bearer credentials lack wire realm ownership] -> Preserve other tokens; do not claim automatic stale-token replacement or unrestricted multi-realm negotiation.
- [Existing stack logs can render SIP credentials] -> New auth errors/examples must not log secrets or arbitrary principals. This change does not claim to retrofit stack-wide SIP-message log redaction; deployments must configure safe message logging.
- [Proposed API and defaults are not yet approved] -> Keep all tasks unchecked and do not implement until the user reviews this design.

## Migration Plan

This is an opt-in addition. Existing applications keep their current hooks and no authentication is attached automatically.

Implementation first adds contracts and wire regression coverage, then mechanisms, then documentation/examples and verification. Existing MD5 header renderings remain valid; newly supported digest lengths and original URI spelling must survive parse/render and JSON paths.

Rollback consists of removing an application's configured authenticator and restoring its previous hook/provider; the library does not own external state or migrate account storage. Removing authentication must not be presented as a safe production rollback without an equivalent application access policy.

## Approval Boundary

The user has agreed on separate client/server mechanisms, contracts consumable by future helpers, stateful Digest nonce tracking, generics for the server, and direct `P` return.

The exact client signature, constructor/callback shapes (including the proxy-leg flag), SHA-256-only/stronger-first client policy, explicit MD5 opt-in, qop subset, 401 default with `Proxy: true` for 407, nonce defaults, HA1 lookup, trusted-AS configuration/equality, and Bearer header-preservation rule are concrete proposals in this document. They are ready for review, not claims of earlier user approval.

First review and approve the updated OpenSpec change. Prepare the detailed implementation plan only after that approval, and begin code changes only after the plan is agreed.

There are no deferred questions that must be answered by the implementer to choose the architecture.

## References

- [SIP authentication, RFC 3261 Section 22](https://www.rfc-editor.org/rfc/rfc3261.html#section-22).
- [SIP Digest updates, RFC 8760](https://www.rfc-editor.org/rfc/rfc8760.html).
- [Digest calculations, RFC 7616](https://www.rfc-editor.org/rfc/rfc7616.html).
- [SIP Bearer procedures and token protection, RFC 8898](https://www.rfc-editor.org/rfc/rfc8898.html).
