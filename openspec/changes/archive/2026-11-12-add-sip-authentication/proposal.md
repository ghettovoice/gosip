# Proposal

## Why

Gosip already represents SIP Digest and Bearer headers, but applications must implement credential verification and challenge responses themselves. Reusable authentication contracts will let applications connect basic implementations to today's proxy hooks and future user-agent helpers without coupling the SIP core to a concrete mechanism.

## What Changes

- Add a client authentication interface and a generic server authentication interface in `sip`; server success returns the application-defined principal `P` directly, without an `AuthResult` wrapper.
- Add `DigestClient`, `BearerClient`, `DigestServer[P]`, and `BearerServer[P]` in `sip/auth`, with explicit construction and application-supplied credential/token callbacks.
- Support 401/WWW-Authenticate/Authorization and 407/Proxy-Authenticate/Proxy-Authorization without sending messages or managing transactions.
- Provide Digest SHA-256 and explicitly enabled legacy MD5 with `qop=auth`, bounded in-memory nonce/replay tracking, and expired-nonce challenges.
- Delegate Bearer token acquisition and validation to the application; require a configured trusted HTTPS authorization-server URI for challenge-based operation.
- Build on the existing RFC 8760 grammar/parser, make Digest credential validation algorithm-aware, and preserve the original credential URI text for digest verification.
- Document explicit principal handling, authentication-versus-authorization boundaries, proxy-hook composition, and deployment/security limitations.

Automatic retry, new `Element` methods, user-agent implementation, OAuth/JWT implementations, persistent/distributed nonce storage, and general authorization policy are out of scope.

## Capabilities

### New Capabilities

- `sip-authentication`: Pluggable client/server SIP authentication, typed application principals, basic Digest/Bearer mechanisms, challenge handling, and authentication-specific wire compatibility.

### Modified Capabilities

None. Existing proxy hook signatures, transaction ownership, ACK/CANCEL handling, and downstream challenge aggregation remain unchanged.

## Impact

- New public contracts and shared authentication error conditions in `sip`; new implementations in `sip/auth`.
- Focused changes to `sip/header/authorization.go` and header/parser regression tests. The RFC 3261 ABNF source and generated parser already include the RFC 8760 updates and do not need to be reimplemented in this change.
- New authentication documentation and examples; update the architecture description to distinguish optional authentication mechanics from application policy.
- No new module dependencies, middleware registration, network clients, or changes to the proxy processing pipeline.
- This change contains planning artifacts only; implementation requires a separate request after design review.
