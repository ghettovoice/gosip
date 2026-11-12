package auth

import (
	"log/slog"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
)

// ServerOptions configures the shared protection space and authentication
// leg of a server authenticator.
type ServerOptions struct {
	// Realm is the non-empty, case-sensitive protection space this
	// instance issues challenges for and accepts credentials from.
	Realm string
	// Proxy selects the authentication leg: false reads Authorization and
	// challenges with 401/WWW-Authenticate; true reads
	// Proxy-Authorization and challenges with 407/Proxy-Authenticate.
	Proxy bool
}

// authRejection builds the shared challenge rejection: a Debug
// level *sip.RequestRejectedError carrying the leg's status and typed
// challenge headers. The caller supplies the fully built cause — callers
// inspect it with errors.Is/As — and headers for the configured family.
// It never sends a response.
func authRejection(proxy bool, cause error, hdrs sip.Headers) error {
	status := sip.ResponseStatusUnauthorized
	if proxy {
		status = sip.ResponseStatusProxyAuthenticationRequired
	}
	return errors.Wrap(sip.NewRequestRejectedError(
		cause, slog.LevelDebug, status,
		sip.RespondOptions{ResponseOptions: sip.ResponseOptions{Headers: hdrs}},
	))
}
