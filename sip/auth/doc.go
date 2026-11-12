// Package auth provides optional SIP authentication mechanisms built on the
// consumer-owned [github.com/ghettovoice/gosip/sip.ClientAuthenticator] and
// [github.com/ghettovoice/gosip/sip.ServerAuthenticator] contracts.
//
// The package contains the basic Digest and Bearer implementations shared by
// user agents and proxies — DigestClient/DigestServer cover RFC 7616
// Digest and BearerClient/BearerServer cover RFC 8898 Bearer, each on both
// the client and server sides. Applications keep ownership of
// credentials, tokens, account stores, authorization policy, and SIP
// exchanges: an authenticator prepares credential headers or returns a
// typed principal and never sends messages, retries exchanges, registers
// middleware, or stores identity in context or message metadata.
package auth
