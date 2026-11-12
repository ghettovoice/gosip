package auth

import (
	"context"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

// BearerClientOptions configures a Bearer client authenticator for one
// protection space and one trusted authorization server.
type BearerClientOptions struct {
	// Realm is the non-empty, case-sensitive protection space the instance
	// answers challenges for.
	Realm string
	// AuthzServer is the trusted authorization-server URI: it must render
	// as an absolute HTTPS URL with a host and no userinfo or fragment.
	// Challenged AS URIs are compared byte-for-byte against its rendered
	// form — a different spelling, including a default port or a trailing
	// slash, is a different trust root.
	AuthzServer uri.URI
	// Token returns the token for a challenge. It receives detached clones
	// of the request and challenge plus the leg flag — false for
	// WWW-Authenticate/Authorization, true for Proxy-Authenticate/
	// Proxy-Authorization. Challenge Scope and Error parameters are
	// forwarded inside the cloned challenge; the callback decides their
	// meaning. Token acquisition is application-owned — it may contact the
	// application's configured trusted AS, but an untrusted challenge URI
	// must never be treated as permission for arbitrary network access.
	// The library itself performs no AS network access, ever. The callback
	// must not log the token.
	Token func(
		ctx context.Context,
		req *sip.Request,
		chal *header.BearerChallenge,
		proxy bool,
	) (string, error)
}

// BearerClient implements sip.ClientAuthenticator for RFC 8898 Bearer over
// SIP. It answers only challenges for the configured realm and trusted AS
// and applies provider tokens as Bearer credentials; it never sends,
// retries, fetches the authorization-server URL, or interprets tokens.
// Bearer credentials carry no realm, so previously applied tokens are never
// claimed or pruned — the exchange owner manages stale-token lifetime.
// Tokens are only as protected as the path they travel: TLS is
// hop-by-hop, so deployments needing end-to-end protection require
// encrypted tokens or an equivalent authorized-path guarantee per
// RFC 8898.
type BearerClient struct {
	realm    string
	authzSrv string
	token    func(context.Context, *sip.Request, *header.BearerChallenge, bool) (string, error)
}

// NewBearerClient validates the configuration and returns a ready client.
// It performs no callback, sends nothing, and starts no goroutine.
func NewBearerClient(opts BearerClientOptions) (*BearerClient, error) {
	if opts.Realm == "" {
		return nil, errors.ErrorWrap("missing bearer realm")
	}
	if opts.Token == nil {
		return nil, errors.ErrorWrap("missing token callback")
	}
	rendered, _, err := validateAuthzServer(opts.AuthzServer)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return &BearerClient{realm: opts.Realm, authzSrv: rendered, token: opts.Token}, nil
}

// AuthenticateRequest applies Bearer credentials for trusted challenges on
// either authentication leg of a 401/407 response. Both legs are prepared
// first — each provider sees detached request/challenge clones plus its leg
// flag — and committed once, so any failure or cancellation leaves the
// request unchanged. A challenge matches only when it is a structurally
// valid Bearer challenge for the exact realm whose rendered AS URI is
// byte-identical to the configured trusted URI; scope and error values
// are forwarded to the provider uninterpreted.
func (c *BearerClient) AuthenticateRequest(ctx context.Context, req *sip.RequestEnvelope, res *sip.ResponseEnvelope) error {
	if c == nil || c.token == nil {
		return errors.ErrorWrap("uninitialized bearer client")
	}
	reqMsg, resMsg, err := clientMsgs(ctx, req, res)
	if err != nil {
		return errors.Wrap(err)
	}

	type leg struct {
		proxy bool
		token string
	}
	var legs []leg
	for _, proxy := range []bool{false, true} {
		cln := c.selectChal(resMsg.Headers, proxy)
		if cln == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return errors.Wrap(err)
		}
		reqClone, _ := reqMsg.Clone().(*sip.Request)
		clnClone, _ := cln.Clone().(*header.BearerChallenge)
		token, err := c.token(ctx, reqClone, clnClone, proxy)
		if err != nil {
			return errors.Wrap(err)
		}
		if err := ctx.Err(); err != nil {
			return errors.Wrap(err)
		}
		if !isBearerToken(token) {
			return errors.ErrorWrap("provider returned an invalid bearer token")
		}
		legs = append(legs, leg{proxy: proxy, token: token})
	}
	if len(legs) == 0 {
		return errors.Wrap(sip.ErrUnsupportedChallenge)
	}
	return errors.Wrap(withReqCommit(ctx, req, func(msg *sip.Request) {
		for _, l := range legs {
			applyBearerCreds(&msg.Headers, l.proxy, l.token)
		}
	}))
}

// selectChal returns the first wire-order structurally valid Bearer
// challenge in the family that names the exact configured realm and the
// byte-identical trusted AS. Digest, other-scheme, and untrusted values
// are skipped silently.
func (c *BearerClient) selectChal(hdrs sip.Headers, proxy bool) *header.BearerChallenge {
	seq := hdrs.AuthenticateChallenges
	if proxy {
		seq = hdrs.ProxyAuthenticateChallenges
	}
	for chal := range seq() {
		cln, ok := chal.(*header.BearerChallenge)
		if !ok || cln == nil || !cln.IsValid() {
			continue
		}
		if cln.Realm != c.realm {
			continue
		}
		rendered, err := renderURI(cln.AuthzServer)
		if err != nil || rendered != c.authzSrv {
			continue
		}
		return cln
	}
	return nil
}

// applyBearerCreds appends a Bearer credential to the selected
// family unless a byte-identical token already exists there. Every other
// Bearer token, scheme, realm, and header value keeps its position: the
// grammar carries no realm, so existing tokens are never claimed or
// pruned.
func applyBearerCreds(hdrs *sip.Headers, proxy bool, token string) {
	name := sip.HeaderName("Authorization")
	if proxy {
		name = "Proxy-Authorization"
	}
	for _, hdr := range hdrs.Get(name) {
		var creds header.AuthCredentials
		switch h := hdr.(type) {
		case *header.Authorization:
			if !proxy && h != nil {
				creds = h.AuthCredentials
			}
		case *header.ProxyAuthorization:
			if proxy && h != nil {
				creds = h.AuthCredentials
			}
		}
		if bc, ok := creds.(*header.BearerCredentials); ok && bc != nil && bc.Token == token {
			return
		}
	}
	if *hdrs == nil {
		*hdrs = make(sip.Headers)
	}
	creds := &header.BearerCredentials{Token: token}
	if proxy {
		*hdrs = hdrs.Append(&header.ProxyAuthorization{AuthCredentials: creds})
	} else {
		*hdrs = hdrs.Append(&header.Authorization{AuthCredentials: creds})
	}
}
