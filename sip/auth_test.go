package sip_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

type principal struct {
	ID string
}

type valueAuthenticator struct{}

func (valueAuthenticator) AuthenticateRequest(_ context.Context, _ *sip.RequestEnvelope) (principal, error) {
	return principal{ID: "account-1"}, nil
}

type pointerAuthenticator struct{}

func (pointerAuthenticator) AuthenticateRequest(_ context.Context, _ *sip.RequestEnvelope) (*principal, error) {
	return &principal{ID: "account-1"}, nil
}

type applicationClient struct{}

func (applicationClient) AuthenticateRequest(_ context.Context, _ *sip.RequestEnvelope, _ *sip.ResponseEnvelope) error {
	return nil
}

var (
	_ sip.ServerAuthenticator[principal]  = valueAuthenticator{}
	_ sip.ServerAuthenticator[*principal] = pointerAuthenticator{}
	_ sip.ClientAuthenticator             = applicationClient{}
)

func TestAuthenticationContracts(t *testing.T) {
	t.Parallel()

	got, err := valueAuthenticator{}.AuthenticateRequest(t.Context(), nil)
	if err != nil || got.ID != "account-1" {
		t.Fatalf("valueAuthenticator.AuthenticateRequest() = %+v, %v, want principal{ID: \"account-1\"}, nil", got, err)
	}

	gotPtr, err := pointerAuthenticator{}.AuthenticateRequest(t.Context(), nil)
	if err != nil || gotPtr == nil || gotPtr.ID != "account-1" {
		t.Fatalf("pointerAuthenticator.AuthenticateRequest() = %+v, %v, want &principal{ID: \"account-1\"}, nil", gotPtr, err)
	}

	if err := (applicationClient{}).AuthenticateRequest(t.Context(), nil, nil); err != nil {
		t.Fatalf("applicationClient.AuthenticateRequest() error = %v, want nil", err)
	}
}

const errBackendUnavailable errors.Error = "backend unavailable"

func TestAuthenticationErrors(t *testing.T) {
	t.Parallel()

	challenge := &header.WWWAuthenticate{
		AuthChallenge: &header.DigestChallenge{Realm: "example.com", Nonce: "qwerty"},
	}
	resOpts := sip.RespondOptions{
		ResponseOptions: sip.ResponseOptions{
			Headers: make(sip.Headers).Append(challenge),
		},
	}
	rejection := sip.NewRequestRejectedError(
		errors.Join(sip.ErrAuthenticationRequired, sip.ErrInvalidCredentials),
		slog.LevelDebug,
		sip.ResponseStatusUnauthorized,
		resOpts,
	)

	wrapped := errors.Wrap(errors.Wrap(rejection))
	if !errors.Is(wrapped, sip.ErrAuthenticationRequired) {
		t.Fatal("lost challenge condition")
	}
	if !errors.Is(wrapped, sip.ErrInvalidCredentials) {
		t.Fatal("lost negative lookup condition")
	}

	var rejErr *sip.RequestRejectedError
	if !errors.As(wrapped, &rejErr) {
		t.Fatal("lost SIP rejection")
	}
	if rejErr.ResponseStatus() != sip.ResponseStatusUnauthorized {
		t.Fatalf("rejection status = %v, want %v", rejErr.ResponseStatus(), sip.ResponseStatusUnauthorized)
	}
	gotChallenges := rejErr.RespondOptions().ResponseOptions.Headers.Get("WWW-Authenticate")
	if len(gotChallenges) != 1 || !gotChallenges[0].Equal(challenge) {
		t.Fatalf("challenge headers = %+v, want %v", gotChallenges, challenge)
	}

	if errors.Is(wrapped, errBackendUnavailable) {
		t.Fatal("authentication challenge masked an unrelated condition")
	}
	if errors.Is(errBackendUnavailable, sip.ErrAuthenticationRequired) || errors.Is(errBackendUnavailable, sip.ErrInvalidCredentials) {
		t.Fatal("backend failure became an authentication condition")
	}
}
