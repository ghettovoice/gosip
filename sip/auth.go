package sip

import (
	"context"

	"github.com/ghettovoice/gosip/internal/errors"
)

// ClientAuthenticator applies authentication to an application-owned request
// using the challenges carried by a received 401/407 response.
//
// Implementations only prepare and apply credential headers; they do not send
// or resend messages, manage transactions or retries, or correlate the
// request with the challenge response. The caller must exclusively own the
// supplied request for the duration of the call.
//
// The optional sip/auth package provides basic implementations; applications
// may also supply their own conformance.
type ClientAuthenticator interface {
	// AuthenticateRequest updates req with credentials answering the
	// challenges in res.
	AuthenticateRequest(ctx context.Context, req *RequestEnvelope, res *ResponseEnvelope) error
}

// ServerAuthenticator authenticates an inbound request and returns the
// application-defined principal P on success.
//
// A nil error is the only success signal: on failure implementations return
// the zero value of P together with a non-nil error, typically a
// [RequestRejectedError] carrying a 401/407 challenge. The principal is not
// inferred from message identity headers and is not stored in context or
// message metadata by the contract.
type ServerAuthenticator[P any] interface {
	// AuthenticateRequest authenticates req and returns the resolved
	// principal.
	AuthenticateRequest(ctx context.Context, req *RequestEnvelope) (P, error)
}

// Authentication error conditions shared by authentication contracts and
// mechanisms. They never carry credentials, proofs, or principal data.
const (
	// ErrAuthenticationRequired reports that the server requires another
	// authentication exchange. It is the discoverable cause of a challenge
	// [RequestRejectedError].
	ErrAuthenticationRequired errors.Error = "authentication required"
	// ErrInvalidCredentials reports an application's explicit negative
	// account or token result, distinct from operational lookup failures.
	ErrInvalidCredentials errors.Error = "invalid credentials" //nolint:gosec // public sentinel label, not a credential
	// ErrUnsupportedChallenge reports that a client cannot answer any
	// applicable challenge under its configured mechanism, trust, or
	// algorithm policy.
	ErrUnsupportedChallenge errors.Error = "unsupported authentication challenge"
)
