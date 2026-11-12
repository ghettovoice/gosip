package auth

import (
	"context"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

// BearerServerOptions configures a Bearer server authenticator for one
// protection space.
type BearerServerOptions[P any] struct {
	ServerOptions
	// AuthzServer is the authorization-server URI advertised in challenges;
	// it must render as an absolute HTTPS URL with a host and no userinfo
	// or fragment, and is retained as an independent copy.
	AuthzServer uri.URI
	// Scope, when non-empty, is advertised verbatim in challenges; scope
	// semantics stay the application's.
	Scope string
	// ValidateToken validates an opaque token against the request and
	// returns the principal. It receives a detached request clone. Return
	// sip.ErrInvalidCredentials for a missing, expired, or unacceptable
	// token — it produces an invalid_token challenge while another
	// candidate may still be tried; any other error is an ordinary
	// operational failure and stops processing. Issuer, audience, expiry,
	// scope, and decryption checks all stay here: the server treats the
	// token as opaque, including non-JWT reference tokens.
	ValidateToken func(ctx context.Context, req *sip.Request, token string) (P, error)
}

// BearerServer implements sip.ServerAuthenticator for RFC 8898 Bearer over
// SIP. It validates Authorization/Proxy-Authorization Bearer credentials on
// the caller's request and returns the validator's principal directly —
// the zero value of P on every failure. It never sends a response,
// mutates the request, parses tokens, authorizes the resulting action, or
// derives identity from request fields such as From. Tokens are only as
// protected as the path they travel: TLS is hop-by-hop, so deployments
// needing end-to-end protection require encrypted tokens or an equivalent
// authorized-path guarantee per RFC 8898.
type BearerServer[P any] struct {
	realm     string
	proxy     bool
	scope     string
	asURI     uri.URI
	validator func(context.Context, *sip.Request, string) (P, error)
}

// NewBearerServer validates the configuration and returns a ready server.
// It performs no callback, sends nothing, and starts no goroutine.
func NewBearerServer[P any](opts BearerServerOptions[P]) (*BearerServer[P], error) {
	if opts.Realm == "" {
		return nil, errors.ErrorWrap("missing bearer realm")
	}
	if opts.ValidateToken == nil {
		return nil, errors.ErrorWrap("missing token validator")
	}
	_, cloned, err := validateAuthzServer(opts.AuthzServer)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return &BearerServer[P]{
		realm:     opts.Realm,
		proxy:     opts.Proxy,
		scope:     opts.Scope,
		asURI:     cloned,
		validator: opts.ValidateToken,
	}, nil
}

// AuthenticateRequest validates Bearer credentials in the configured
// family. Each syntactically acceptable token reaches ValidateToken once
// with a fresh detached request clone; a sip.ErrInvalidCredentials result
// marks the challenge invalid_token and lets the next candidate run, any
// other error stops processing as an ordinary failure, and a nil error
// returns the principal after a final context check. Malformed tokens and
// non-Bearer credentials never reach the validator.
func (s *BearerServer[P]) AuthenticateRequest(ctx context.Context, req *sip.RequestEnvelope) (P, error) {
	var zero P
	if s == nil || s.validator == nil {
		return zero, errors.ErrorWrap("uninitialized bearer server")
	}
	reqMsg, err := serverMsg(ctx, req)
	if err != nil {
		return zero, errors.Wrap(err)
	}

	var negative bool
	seq := reqMsg.Headers.AuthorizationCredentials
	if s.proxy {
		seq = reqMsg.Headers.ProxyAuthorizationCredentials
	}
	for creds := range seq() {
		bc, ok := creds.(*header.BearerCredentials)
		if !ok || bc == nil {
			continue
		}
		if !isBearerToken(bc.Token) {
			negative = true
			continue
		}
		if err := ctx.Err(); err != nil {
			return zero, errors.Wrap(err)
		}
		detached, _ := reqMsg.Clone().(*sip.Request)
		principal, err := s.validator(ctx, detached, bc.Token)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return zero, errors.Wrap(ctxErr)
		}
		switch {
		case err == nil:
			return principal, nil
		case errors.Is(err, sip.ErrInvalidCredentials):
			negative = true
		default:
			return zero, errors.Wrap(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return zero, errors.Wrap(err)
	}

	return zero, errors.Wrap(s.chal(negative))
}

// chal builds the typed Bearer rejection: the configured realm and
// copied AS URI, the optional scope, and invalid_token only when a
// presented credential was explicitly rejected or malformed.
func (s *BearerServer[P]) chal(negative bool) error {
	cln := &header.BearerChallenge{
		Realm:       s.realm,
		Scope:       s.scope,
		AuthzServer: shared.Clone[uri.URI](s.asURI),
	}
	if negative {
		cln.Error = "invalid_token"
	}
	var hdrs sip.Headers
	if s.proxy {
		hdrs = make(sip.Headers).Set(&header.ProxyAuthenticate{AuthChallenge: cln})
	} else {
		hdrs = make(sip.Headers).Set(&header.WWWAuthenticate{AuthChallenge: cln})
	}
	cause := error(sip.ErrAuthenticationRequired)
	if negative {
		cause = errors.Join(sip.ErrAuthenticationRequired, sip.ErrInvalidCredentials)
	}
	return authRejection(s.proxy, cause, hdrs)
}
