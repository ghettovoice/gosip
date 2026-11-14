# sip-authentication Specification

## Purpose

Provide reusable client and server SIP authentication mechanisms that applications can connect to proxy and user-agent processing while retaining control of identity, authorization, token services, and SIP exchanges.

## Requirements

### Requirement: Independent authentication contracts

The library SHALL expose a non-generic client authenticator and a server authenticator parameterized by application principal type `P` in `sip`. Basic Digest and Bearer implementations for both sides SHALL reside in `sip/auth`. Core contracts SHALL NOT depend on concrete implementations.

#### Scenario: Interchangeable server mechanisms

- **WHEN** an application supplies Digest or Bearer authentication for the same principal type
- **THEN** either implementation satisfies the same server contract without type assertions or a mechanism dependency in `sip`.

#### Scenario: Application-defined implementation

- **WHEN** an application provides its own implementation of a client or server contract
- **THEN** callers can use it without constructing a built-in mechanism or attaching components.

### Requirement: Direct typed principal return

Successful server authentication SHALL return `P` directly with a nil error. Failure SHALL return the zero value of `P` and a non-nil error. The library SHALL NOT require an authentication result wrapper, infer identity from SIP identity headers, or implicitly store the principal in context or message metadata.

#### Scenario: Value principal

- **WHEN** a valid credential resolves to an application-defined struct principal
- **THEN** authentication returns that struct without converting it to a pointer, string identifier, or `any`.

#### Scenario: Failed proof does not expose principal

- **WHEN** a Digest lookup finds an account but the proof is invalid
- **THEN** the result contains only the zero principal and an error, even if the request's From header claims that account.

### Requirement: Explicit construction and configuration

Basic authenticators SHALL validate required callbacks and realm/trust configuration at construction, copy mutable options, and reject invalid capacities, durations, or algorithms. Construction SHALL NOT register components, send messages, contact external services, or start background processing. Uninitialized instances SHALL fail rather than panic or authenticate.

#### Scenario: Invalid server configuration

- **WHEN** a server lacks its required lookup/validator or has a negative nonce TTL/capacity
- **THEN** construction fails without installing authentication or creating a runtime process.

#### Scenario: Configuration ownership

- **WHEN** the application mutates its source algorithm slice or AS URI after construction
- **THEN** the authenticator's policy remains unchanged.

#### Scenario: Nonce capacity cannot hold one challenge set

- **WHEN** a Digest server is configured with fewer nonce records than enabled challenge algorithms
- **THEN** construction fails rather than issuing a response whose preferred nonce has already been evicted.

### Requirement: Authentication has no SIP exchange ownership

Authenticators SHALL NOT send requests/responses, create transactions, retry exchanges, or change CSeq, Via, routing, bodies, or dialog state. The application or a future higher-level helper SHALL own response correlation, retry limits, and subsequent sending.

#### Scenario: Client applies a challenge

- **WHEN** a client authenticator succeeds on an application-owned retry request
- **THEN** only the applicable authentication headers change and no SIP message is sent.

#### Scenario: Server requests authentication

- **WHEN** server authentication needs another exchange
- **THEN** it returns a rejection describing a challenge without sending a response.

### Requirement: Server challenge status and credential scope

A basic server SHALL read Authorization and challenge with 401/WWW-Authenticate by default. Explicit proxy mode SHALL instead read Proxy-Authorization and challenge with 407/Proxy-Authenticate. Challenges SHALL be inspectable as `sip.RequestRejectedError` and `sip.ErrAuthenticationRequired` through wrapping.

#### Scenario: UAS challenge

- **WHEN** a default-mode server receives no acceptable Authorization credential
- **THEN** it returns a 401 rejection with its typed WWW-Authenticate challenge, ignoring Proxy-Authorization as a substitute.

#### Scenario: Proxy challenge

- **WHEN** a proxy-mode server receives no acceptable Proxy-Authorization credential
- **THEN** it returns a 407 rejection with its typed Proxy-Authenticate challenge, ignoring Authorization as a substitute.

### Requirement: Authentication failures are distinct from operational errors

Callbacks SHALL use `sip.ErrInvalidCredentials` for explicit negative account/token results. Such results and invalid proofs SHALL produce authentication challenges. Backend failures, malformed backend HA1, cancellation, and deadlines SHALL preserve their error identities without becoming 401/407 challenges or returning a principal.

#### Scenario: Invalid or absent account

- **WHEN** an account lookup explicitly reports invalid credentials or a known account supplies a bad proof
- **THEN** the public challenge does not reveal which account condition occurred.

#### Scenario: Token validator outage

- **WHEN** token validation returns an operational error rather than invalid credentials
- **THEN** callers can inspect the original failure and receive no authentication-required rejection.

### Requirement: ACK and CANCEL are not challenged

Basic authenticators SHALL reject direct attempts to begin authentication for ACK or CANCEL with a non-challenge method error. They SHALL NOT return a principal, allocate a server nonce, or add credentials for these calls. Existing caller-owned ACK/CANCEL protocol handling SHALL remain unchanged.

#### Scenario: Server called for CANCEL

- **WHEN** a server authenticator is invoked directly for CANCEL
- **THEN** it returns a method error without a 401/407 rejection or a new challenge nonce.

#### Scenario: Client called for ACK

- **WHEN** a client authenticator is invoked directly for ACK
- **THEN** it returns a method error and leaves the request unchanged.

### Requirement: Client challenge selection is realm-scoped

A client SHALL accept only initialized request/response inputs with a 401 or 407 response. It SHALL independently inspect both challenge header families, select at most one supported challenge for its configured case-sensitive realm per family, and ignore unrelated schemes/realms. No compatible challenge SHALL yield `sip.ErrUnsupportedChallenge` without provider calls or request mutation.

#### Scenario: Aggregated challenge families

- **WHEN** a 401 or 407 contains applicable challenges in both WWW-Authenticate and Proxy-Authenticate
- **THEN** the client can produce both Authorization and Proxy-Authorization in one call.

#### Scenario: Different realm

- **WHEN** all challenges belong to another realm, including one differing only in case
- **THEN** no credentials are requested from the provider or applied to the request.

#### Scenario: Same realm on different authentication legs

- **WHEN** matching WWW-Authenticate and Proxy-Authenticate challenges use the same realm
- **THEN** each provider invocation explicitly identifies its UAS/proxy leg so the application can choose different credentials.

#### Scenario: Non-challenge response

- **WHEN** a caller supplies a 200 response, nil envelope, or uninitialized request
- **THEN** the call returns an ordinary input error without modifying headers or calling providers.

#### Scenario: Missing transaction headers and malformed challenge

- **WHEN** a request has a valid method and Request-URI but lacks transaction headers, and the response contains malformed followed by compatible challenges
- **THEN** the request is an acceptable authentication input and malformed challenges are skipped without provider calls for them.

### Requirement: Client header application is atomic and isolated

Clients SHALL prepare all selected credentials before editing the request. Any provider/validation error SHALL leave the request unchanged. Successful updates SHALL preserve the received response, other protection spaces, other schemes, and unrelated message data. Callback input mutation SHALL NOT edit the source messages.

#### Scenario: Second selected provider fails

- **WHEN** both challenge families are selected but credential preparation for one fails
- **THEN** neither family's prepared headers are committed.

#### Scenario: Existing credentials for another realm

- **WHEN** Digest credentials are applied for one realm to a request containing credentials for another realm
- **THEN** only the selected realm/mechanism's credentials are replaced.

### Requirement: Digest algorithm and qop policy is explicit

Basic Digest SHALL support SHA-256 and explicitly enabled MD5, with SHA-256 as the sole default. Servers SHALL advertise enabled algorithms in preference order with distinct nonces and `qop=auth`. Clients SHALL prefer compatible SHA-256 over MD5 and retain wire order within an algorithm. Unsupported algorithms/qop SHALL NOT implicitly enable a weaker algorithm.

#### Scenario: Secure default rejects MD5

- **WHEN** a default client receives only an MD5 challenge, including an omitted algorithm
- **THEN** it reports no supported challenge rather than silently computing MD5.

#### Scenario: Explicit legacy compatibility

- **WHEN** MD5 is explicitly enabled and a compatible MD5 challenge is selected
- **THEN** client and server can complete a qop=auth exchange using MD5.

#### Scenario: Stronger challenge follows legacy challenge

- **WHEN** MD5 and SHA-256 are enabled and compatible MD5 is advertised before SHA-256
- **THEN** the client selects SHA-256 regardless of allowlist order.

#### Scenario: Missing and unsupported qop

- **WHEN** a supported Digest challenge omits qop or offers only auth-int
- **THEN** the client uses explicit qop=auth for the omitted case and skips the auth-int-only case; servers reject proofs without the issued qop=auth/count fields.

### Requirement: Digest proof verification uses authenticated inputs

Digest SHALL compute the response from the exact method, digest URI text, realm, username, issued nonce, eight-digit nonce count, cnonce, and selected algorithm using qop=auth. Comparisons SHALL use validated fixed-size digest bytes and constant-time equality. Realm/username SHALL be case-sensitive; incomplete, malformed, or challenge-inconsistent proofs SHALL NOT authenticate.

#### Scenario: Independent digest vectors

- **WHEN** client generation or server verification is exercised against independently specified MD5 and SHA-256 vectors
- **THEN** responses match the expected values without deriving expectations from the implementation under test.

#### Scenario: Changed method or algorithm

- **WHEN** a valid proof is reused with a changed method, algorithm, realm, or signed URI text
- **THEN** it does not authenticate under the original challenge.

#### Scenario: Opaque challenge parameter

- **WHEN** a selected Digest challenge includes opaque
- **THEN** the client copies that value verbatim into the resulting credentials in either header family without changing the digest formula; an absent opaque is not invented.

### Requirement: Digest wire values are lossless and algorithm-aware

Digest header parsing SHALL accept RFC 8760 digest syntax beyond MD5, including a parseable empty pre-challenge placeholder. Credential validation SHALL check hexadecimal syntax and algorithm-dependent lengths without blocking structurally valid extension algorithms. The original digest URI value SHALL survive parsing, cloning, rendering, and JSON header round trips when its typed URI is unchanged.

#### Scenario: SHA-256 credential round trip

- **WHEN** a 64-digit SHA-256 Authorization or Proxy-Authorization value is parsed and rendered
- **THEN** it remains a typed Digest credential accepted by header validation.

#### Scenario: Noncanonical signed URI spelling

- **WHEN** a valid external proof signs a URI whose parameter order or escaping differs from canonical rendering
- **THEN** verification hashes the original credential URI value, not a reconstructed normalization.

#### Scenario: Placeholder is not proof

- **WHEN** an empty response placeholder parses successfully
- **THEN** a server does not accept it as successful authentication.

### Requirement: Digest target acceptance is explicit

A basic Digest server SHALL use the existing parsed URI Equal operation to compare the credential URI and current request URI by default. An explicit application URI validator SHALL be able to accept a legitimate forwarded target instead. URI acceptance SHALL be separate from hashing the original signed URI and SHALL NOT grant blanket acceptance of unrelated targets.

#### Scenario: Foreign signed target

- **WHEN** an otherwise valid proof names a target not equivalent to the request URI and no override accepts it
- **THEN** the server challenges instead of returning a principal.

#### Scenario: Permitted forwarded target

- **WHEN** an explicit application validator accepts a forwarded credential target and the proof is otherwise valid
- **THEN** the server can authenticate despite the Request-URI having changed in forwarding.

#### Scenario: Equivalent differently spelled SIP target

- **WHEN** the existing URI equality operation considers differently spelled credential and request URIs equivalent
- **THEN** the default target policy accepts that equivalence while digest verification still uses the original signed URI text.

### Requirement: Digest nonce origin and expiry are verified

A Digest server SHALL accept only its own retained, unexpired nonce with matching realm and algorithm. Nonces and cnonces SHALL contain at least 128 bits of cryptographic randomness. A recognized expired nonce with an otherwise valid proof SHALL produce a fresh stale=true challenge; unknown, evicted, or bad-proof nonces SHALL NOT be treated as authenticated stale credentials.

#### Scenario: Expired valid proof

- **WHEN** a retained nonce expires before an otherwise valid proof is committed
- **THEN** no principal is returned and a fresh challenge indicates stale=true.

#### Scenario: Invented or evicted nonce

- **WHEN** credentials present a nonce the server no longer retains or never issued
- **THEN** authentication fails with a normal fresh challenge and does not recreate the missing nonce record.

### Requirement: Digest replay commitment is atomic

Accepted nonce counts SHALL strictly increase per issued nonce, with no reset on cnonce changes. Only a valid proof SHALL bind a nonce to an authenticated username or advance replay state. Validation SHALL recheck nonce availability and expiry at commitment. Count overflow, reuse, regression, or an incompatible username binding SHALL NOT authenticate.

#### Scenario: Concurrent duplicate proof

- **WHEN** two invocations race to authenticate the same valid nonce/count proof
- **THEN** at most one returns a principal.

#### Scenario: Forged high count

- **WHEN** a bad proof presents a higher count than an outstanding legitimate proof
- **THEN** it does not consume that count or prevent the lower legitimate proof from authenticating.

#### Scenario: Out-of-order legitimate request

- **WHEN** a valid proof arrives after a greater count has already committed for the nonce
- **THEN** it is challenged again rather than reopening the replay window.

#### Scenario: Client count starts and exhausts

- **WHEN** a fresh client nonce session is used or an existing count is exhausted
- **THEN** the fresh session starts at 00000001 and exhaustion returns an ordinary error without wrapping the count or modifying request headers.

### Requirement: Digest state is bounded and instance-local

Client and server nonce state SHALL have finite configurable limits and SHALL NOT require background cleanup goroutines. Default server TTL/capacity SHALL be five minutes/4096 records; default client capacity SHALL be 1024 sessions. Eviction/restart SHALL require a fresh challenge when existing state cannot verify a proof, never authorize by forgetting replay history.

#### Scenario: Capacity pressure

- **WHEN** new challenges or client nonce sessions exceed configured capacity
- **THEN** retained state remains bounded and removed state cannot make an old proof succeed.

#### Scenario: Restart or another server instance

- **WHEN** a request uses a nonce issued by an instance whose state is unavailable
- **THEN** it is challenged again rather than accepted without origin/replay validation.

### Requirement: Bearer challenge trust is explicit

Bearer servers SHALL issue challenges containing a configured HTTPS authz_server and realm, with optional scope. HTTPS AS URLs SHALL be absolute with a host and no userinfo/fragment. Clients SHALL require matching realms and byte-identical rendered AS URIs, without additional normalization, before invoking the provider. Authenticators SHALL NOT contact a challenged URL, discover an AS, or follow redirects automatically.

#### Scenario: Untrusted authorization server

- **WHEN** a Bearer challenge advertises a different AS URI, a missing AS, or a non-HTTPS AS
- **THEN** the client does not invoke its provider or apply a token.

#### Scenario: Trusted challenge

- **WHEN** realm and trusted AS match
- **THEN** the token provider receives detached challenge/request data, including any scope and error fields.

#### Scenario: Different spelling of an AS URL

- **WHEN** a challenge AS URI renders differently from the trusted configuration, even due to an explicit default port or trailing slash
- **THEN** it is not an implicit trusted alias and the token provider is not called.

### Requirement: Bearer validation returns only verified principals

A Bearer server SHALL delegate each candidate token to the configured validator and return only a successfully validated `P`. Explicit invalid/expired tokens SHALL result in a challenge with invalid_token; missing tokens SHALL be challenged without that error field. Token format, issuer, audience, lifetime, scope, and protection policy SHALL remain the application's responsibility.

#### Scenario: Opaque reference token

- **WHEN** an opaque non-JWT token is accepted by the application validator
- **THEN** the server returns its typed principal without requiring JWT parsing.

#### Scenario: Multiple credential candidates

- **WHEN** one token is explicitly invalid but another token in the selected header family validates
- **THEN** the valid token can authenticate; an operational validator failure instead stops processing without a principal.

### Requirement: Bearer application preserves unknown ownership

The client SHALL reject empty or syntactically invalid provider tokens, including header-injection characters. Applying a token SHALL add it to the applicable header family unless an identical Bearer token is already present. Other tokens SHALL be preserved because the credential syntax has no realm; the authenticator SHALL NOT claim automatic stale-token pruning.

#### Scenario: Sequential Bearer challenges

- **WHEN** a request already carries another proxy's Bearer token
- **THEN** applying the selected token preserves the existing value and adds only the new credential.

#### Scenario: Repeated identical token

- **WHEN** the same token is applied again to the same header family
- **THEN** another identical Bearer credential is not added.

#### Scenario: Provider token contains CRLF

- **WHEN** the provider returns a token containing a newline or other invalid token character
- **THEN** the call fails and the request remains unchanged.

### Requirement: Callbacks preserve concurrency and cancellation boundaries

Authenticators SHALL support concurrent calls on distinct requests, invoke application callbacks outside message/state locks, and propagate caller context. Cancellation observed before header/replay commitment SHALL prevent that commitment. Callers SHALL exclusively own mutable client request inputs during authentication; no background goroutine SHALL be used to hide an uncooperative callback.

#### Scenario: Reentrant inspection

- **WHEN** a provider or validator inspects original envelopes or handles another request through the same authenticator
- **THEN** library-held message or nonce locks do not deadlock it.

#### Scenario: Cancellation during callback

- **WHEN** the caller cancels while a callback runs and the callback later returns
- **THEN** authentication returns cancellation without applying credentials, advancing replay state, or returning a principal.

### Requirement: Integration and security boundaries are documented

Documentation SHALL show explicit client invocation and a proxy hook that handles typed `P` without changing hook signatures. It SHALL distinguish authentication from authorization and document replay/replica/reordering limits, token protection, trusted AS policy, and sensitive message logging. Library-authored error messages and examples SHALL NOT embed secrets or automatically log arbitrary principals; applications remain responsible for safe callback error text.

#### Scenario: Application-owned authorization

- **WHEN** authentication succeeds but the application denies an action
- **THEN** the application can reject the request independently of the mechanism's successful principal result.

#### Scenario: Existing proxy behavior

- **WHEN** an application installs authentication through its ordinary authorization hook
- **THEN** existing transaction retransmission ownership, ACK/CANCEL rules, response sending, and challenge aggregation remain unchanged.
