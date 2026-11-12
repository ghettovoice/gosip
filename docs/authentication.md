# SIP Authentication

gosip provides reusable client and server authentication contracts so
applications can plug basic mechanisms into proxy and future user-agent
processing while keeping ownership of identity, credentials, tokens, and SIP
exchanges.

## Contracts

The consumer-owned interfaces live in package `sip` so future core helpers can
consume them without depending on a concrete mechanism:

- `sip.ClientAuthenticator` — applies credential headers to an
  application-owned retry request using the challenges in a received 401/407
  response.
- `sip.ServerAuthenticator[P]` — authenticates an inbound request and returns
  the application-defined principal `P` directly. A nil error is the only
  success signal; failures return the zero value of `P` plus a non-nil error.

Basic Digest and Bearer mechanisms live in the optional `sip/auth` package.
Applications may equally supply their own implementations; no middleware or
account model is required to satisfy a contract.

Shared error conditions declared in `sip`:

- `sip.ErrAuthenticationRequired` — discoverable cause of a 401/407 challenge
  returned as a `*sip.RequestRejectedError`.
- `sip.ErrInvalidCredentials` — an application's explicit negative account or
  token result; it never means an operational backend failure.
- `sip.ErrUnsupportedChallenge` — the client cannot answer any applicable
  challenge under its configured mechanism, realm, trust, or algorithm policy.

## Ownership boundaries

- Authenticators never send or resend messages, manage transactions, or own
  CSeq/Via/routing changes. Callers own correlation between a challenge
  response and the retry request, retry limits, and subsequent sending.
- The 401/WWW-Authenticate + Authorization and 407/Proxy-Authenticate +
  Proxy-Authorization header families are handled independently.
- The principal is not inferred from `From` or other identity headers and is
  not stored in context or message metadata; consuming it is an explicit
  application decision.
- Authentication is not authorization: a successful `P` does not grant any
  action. Applications apply their own policy and may still reject the
  request.
- ACK and CANCEL are not new authentication exchanges; the existing proxy
  dispatch for them is unchanged.

## Digest headers

`header.DigestCredentials` already parses the RFC 8760 wire grammar,
including SHA-256/SHA-512-256 responses, extension algorithm tokens, and the
empty pre-challenge response placeholder.

- `IsValid` is structural, not computational. Known algorithms require their
  digest size (32 hex digits for MD5 variants, 64 for SHA-256/SHA-512-256
  variants); unknown algorithm tokens accept any even-length hexadecimal
  value of at least 32 digits so custom consumers can parse them. An empty
  response and a syntactically fine unknown-algorithm response are both
  parseable, but neither is a proof a basic mechanism can verify.
- `URIText` returns the Request-URI exactly as it appeared in the parsed
  header — the bytes the digest response signs. It stays stable across
  clone, render, and JSON round trips while the typed `URI` is unchanged.
  Editing or replacing `URI` invalidates the snapshot and `URIText` returns
  the current typed rendering; callers must recalculate the response after
  changing signed fields.
- Parsed `URI` equality remains the target-acceptance primitive; the
  raw-text cache is not a second identity or a URI normalizer.

## Digest client

`sip/auth.NewDigestClient` implements `sip.ClientAuthenticator` for
RFC 7616 Digest over SIP. It applies Authorization/Proxy-Authorization
credentials to a caller-owned retry request; it never sends, retries,
correlates, or owns CSeq/Via/routing changes.

- `Realm` fixes the protection space the instance answers (case-sensitive);
  challenges for other realms are ignored. `Credentials` receives detached
  request/challenge clones plus a `proxy` flag — false for the
  WWW-Authenticate/Authorization leg, true for the Proxy-Authenticate/
  Proxy-Authorization leg — so the same realm on different legs may select
  different accounts.
- `Algorithms` is an allowlist, not a preference order. The default is
  SHA-256 only; legacy MD5 must be listed explicitly. Selection prefers
  SHA-256 over MD5 regardless of list or challenge order — a local policy
  choice permitted by RFC 8760, not protection against an attacker who
  strips stronger challenges.
- A challenge without a `qop` directive is answered as `qop=auth`; a qop
  list containing only `auth-int` is not supported. Digest `opaque` is
  echoed verbatim and never enters the digest formula.
- Nonce-count sessions are bounded (default 1024 per leg+nonce, LRU), held
  in memory only, and scoped by authentication leg and server nonce. Counts
  start at `00000001`, increment atomically, never wrap, and may leave gaps
  after aborted exchanges.
- Authentication is all-or-nothing per call: every selected leg is prepared
  first and headers are committed once, so a provider error, an empty
  username, or cancellation leaves the request unchanged. A request with a
  valid method and Request-URI suffices — transaction headers are not
  required — while ACK and CANCEL are rejected as ordinary
  method-not-allowed errors.

## Digest server

`sip/auth.NewDigestServer` implements `sip.ServerAuthenticator` for
RFC 7616 Digest over SIP. It verifies Authorization/Proxy-Authorization
credentials on the caller's request and returns the application-defined
principal `P` directly — the zero `P` on every failure. It never sends a
response, mutates the request, authorizes the resulting action, or treats
request fields such as From as identity.

- `ServerOptions.Realm` fixes the case-sensitive protection space;
  `Proxy` selects the leg (false: `Authorization` in, 401 +
  `WWW-Authenticate` out; true: `Proxy-Authorization` in, 407 +
  `Proxy-Authenticate` out). `Algorithms` is an ordered allowlist
  advertised as one challenge per algorithm — SHA-256 only by default,
  MD5 only when listed explicitly. Prefer leaving MD5 disabled: it is a
  legacy-interop fallback and enabling it also answers omitted-algorithm
  credentials.
- `LookupHA1(ctx, username, realm, algorithm)` returns the account's HA1
  plus its principal. HA1 is the exact lowercase, algorithm-sized hex of
  `H(username:realm:password)` — compute it with `sip/auth.DigestHA1` or
  store it precomputed so passwords never leave the account store. Return
  `sip.ErrInvalidCredentials` for a missing or unacceptable account: it
  stays discoverable via `errors.Is` on the challenge while still
  producing a 401/407. Any other error is an ordinary operational failure
  — surfaced to the caller, never translated into a challenge — as are
  malformed (wrong size/case/non-hex) HA1 values.
- `ValidateURI` is optional and replaces the default target policy, which
  accepts a credential URI that `Equal`s the Request-URI — equivalent
  parsed URIs pass even when the signed spelling differs, while foreign
  targets are rejected. Supply it when the application legitimately
  forwards or rewrites targets: it receives detached request/URI clones,
  returns `sip.ErrInvalidCredentials` to deny a candidate, and any other
  error to abort authentication as an operational failure.
- Issued nonces are kept in bounded in-memory state: five-minute TTL and
  4096 oldest-evicted records by default, one distinct nonce per
  advertised algorithm. State is local to the instance — it does not span
  restarts or replicas and provides no distributed replay protection, so
  after a restart or on another replica the same credentials are simply
  re-challenged.
- A valid proof against a known-but-expired nonce gets a `stale=true`
  challenge; a bad proof, an unknown nonce, or an evicted record gets an
  ordinary fresh challenge — never stale. Accepted nonce counts must be
  positive, strictly increasing, and fit in 32 bits; reusing, regressing,
  or overflowing the count is re-challenged, and changing `cnonce` does
  not reset it. A forged high-count proof never advances the accepted
  count, so it cannot block a later legitimate request. Proof snapshots
  and failed attempts consume nothing; a presented nonce binds to the
  first successfully authenticated username and rejects other accounts
  afterwards.
- Retransmissions are a SIP-layer concern: re-invoking authentication
  with an already-consumed proof is a replay and gets re-challenged, so
  applications should not re-run `AuthenticateRequest` on retransmitted
  requests. The returned principal belongs to the application — store or
  wrap it per the exchange's own policy.
- Errors and challenges never expose usernames, passwords, HA1 values, or
  proofs. Proof comparison is constant-time over the fixed-size digest,
  but the package makes no whole-request constant-time claim — the
  account lookup itself is application code with its own timing.

## Bearer

`sip/auth.NewBearerClient` and `sip/auth.NewBearerServer` implement RFC 8898
Bearer over SIP for one protection space. Tokens stay opaque: no JWT
parsing, no OAuth flows, no fetching the authorization-server URL.

**Client.** `BearerClientOptions.Realm` is matched exactly (case-sensitive)
and `AuthzServer` must render as an absolute HTTPS URL — host required, no
userinfo, no fragment. A challenge is answered only when its `authz_server`
renders byte-identically to the configured value: `https://as.example:443/t`,
`https://as.example/t/`, and `https://as.example/t` are three different trust
roots, and a different spelling needs explicit configuration rather than
implicit aliasing. The client retains only the validated rendered string
(the server retains an independent URI copy for its challenges), so
mutating the caller's URI after construction changes nothing. `Token` receives detached
request/challenge clones plus a `proxy` flag (false = WWW-Authenticate/
Authorization, true = Proxy-Authenticate/Proxy-Authorization) and sees the
challenge's `scope`/`error` parameters forwarded uninterpreted. Both legs
are prepared before a single commit, so a provider error, an invalid
returned token, or cancellation leaves the request unchanged. Returned
tokens must be non-empty visible ASCII without spaces, control bytes, or
non-ASCII content. Credentials carry no realm on the wire, so the client
only appends a byte-identical-absent token per family and never claims,
reorders, or prunes existing tokens — stale-token lifetime belongs to the
exchange owner.

**Server.** `BearerServerOptions` embeds `ServerOptions` (same realm/leg
rules as Digest) plus the advertised `AuthzServer`, an optional verbatim
`Scope`, and `ValidateToken(ctx, request, token)`. Each syntactically
acceptable token reaches the validator once with a fresh detached request
clone. `sip.ErrInvalidCredentials` rejects the candidate — the challenge
then carries `error="invalid_token"` and a later candidate may still
succeed — while any other error is an ordinary operational failure that
stops processing immediately. Issuer, audience, expiry, scope acceptance,
and decryption are all the validator's job; reference tokens that are not
JWTs are fully supported. A missing-token challenge advertises realm, AS,
and scope without an error parameter.

**Token protection.** Bearer credentials are only as protected as the path
they travel. TLS protects each hop, not end-to-end — a token visible to an
intermediary is usable by it. Where that is not acceptable, deployments
need encrypted tokens or an equivalent authorized-path guarantee per
RFC 8898, and applications should scope and expire tokens accordingly.
Neither mechanism logs or returns token material, and the same discipline
applies to application callbacks.

## Composition example

A proxy application installs authentication inside its existing authorization
hook. The hook signature is unchanged, and a challenge rejection flows through
the ordinary `RequestRejectedError` response path:

```go
route := &proxy.Route{
    AuthorizeRequest: func(ctx context.Context, req *sip.RequestEnvelope) error {
        // ACK and CANCEL are never authenticated; they follow the
        // application's own method rules.
        if req.Method().Equal(sip.RequestMethodAck) || req.Method().Equal(sip.RequestMethodCancel) {
            return nil
        }
        user, err := serverAuthn.AuthenticateRequest(ctx, req)
        if err != nil {
            return err // may be a 401/407 challenge rejection
        }
        // Apply application authorization policy with the typed principal.
        if !policy.Allows(user, req) {
            return sip.NewRequestRejectedError(errForbidden, slog.LevelWarn, sip.ResponseStatusForbidden)
        }
        return nil
    },
}
```

The corresponding client flow obtains a 401/407, applies credentials to an
exclusively owned retry request, and leaves sending to the exchange owner.
`sip/auth.NewDigestClient` and `sip/auth.NewDigestServer` above are the
concrete mechanism constructors behind this sketch.

## Runnable examples

`sip/auth/example_test.go` contains runnable examples for the public
contracts:

- `ExampleDigestClient_AuthenticateRequest` — applies stored credentials to a
  request after the caller received a 401 challenge; sending the retry stays
  application-owned.
- `ExampleDigestServer_AuthenticateRequest` — a full challenge round trip
  between `NewDigestServer` and `NewDigestClient` returning the application's
  principal.
- `ExampleBearerServer_AuthenticateRequest` — a `proxy.Route.AuthorizeRequest`
  closure where authentication and the application's own authorization
  decision are independent steps.

Run them with `go test ./sip/auth -run '^Example' -count=1`.

## Security notes

- Digest and Bearer are not transport protection. Deployments should protect
  Digest traffic and ensure Bearer tokens are only visible to authorized SIP
  elements (TLS plus token protection as appropriate under RFC 8898).
- Bearer challenges carry an authorization-server URI per RFC 8898; clients
  must only act on a trusted configured AS and never fetch or follow a
  challenged URL.
- In-memory Digest nonce tracking does not span restarts or replicas and uses
  strict nonce-count replay checks; reordered requests may be re-challenged.
- Authentication errors and examples must not embed usernames, passwords,
  HA1 values, tokens, digest responses, or arbitrary principals. Applications
  keep the same discipline for errors returned by their own callbacks and for
  SIP message logging.
