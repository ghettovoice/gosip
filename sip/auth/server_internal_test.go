package auth

import (
	"log/slog"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

func TestAuthenticationRejection(t *testing.T) {
	t.Parallel()

	hdrs := make(sip.Headers).Set(&header.WWWAuthenticate{
		AuthChallenge: &header.DigestChallenge{
			Realm:     "example.com",
			Nonce:     "n1",
			Algorithm: "SHA-256",
			QOP:       []string{"auth"},
		},
	})

	t.Run("uas leg", func(t *testing.T) {
		t.Parallel()

		err := authRejection(false, sip.ErrAuthenticationRequired, hdrs)
		var rejection *sip.RequestRejectedError
		if !errors.As(err, &rejection) {
			t.Fatalf("error = %T, want *sip.RequestRejectedError", err)
		}
		if rejection.ResponseStatus() != sip.ResponseStatusUnauthorized {
			t.Fatalf("status = %d, want 401", rejection.ResponseStatus())
		}
		if rejection.LogLevel() != slog.LevelDebug {
			t.Fatalf("level = %v, want Debug", rejection.LogLevel())
		}
		got := rejection.RespondOptions().ResponseOptions.Headers
		if len(got.Get("WWW-Authenticate")) != 1 {
			t.Fatalf("challenge headers = %v, want one WWW-Authenticate", got)
		}
		if !errors.Is(err, sip.ErrAuthenticationRequired) {
			t.Fatalf("error = %v, want it to identify %v", err, sip.ErrAuthenticationRequired)
		}
	})

	t.Run("proxy leg", func(t *testing.T) {
		t.Parallel()

		err := authRejection(true, sip.ErrAuthenticationRequired, nil)
		var rejection *sip.RequestRejectedError
		if !errors.As(err, &rejection) {
			t.Fatalf("error = %T, want *sip.RequestRejectedError", err)
		}
		if rejection.ResponseStatus() != sip.ResponseStatusProxyAuthenticationRequired {
			t.Fatalf("status = %d, want 407", rejection.ResponseStatus())
		}
	})

	t.Run("negative cause stays discoverable", func(t *testing.T) {
		t.Parallel()

		cause := errors.Join(sip.ErrAuthenticationRequired, sip.ErrInvalidCredentials)
		err := authRejection(false, cause, hdrs)
		if !errors.Is(err, sip.ErrAuthenticationRequired) || !errors.Is(err, sip.ErrInvalidCredentials) {
			t.Fatalf("error = %v, want joined auth-required + invalid-credentials", err)
		}
	})
}
