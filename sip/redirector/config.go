package redirector

import (
	"context"
	"log/slog"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// Route represents a redirection routing rule.
// A route is an immutable value owned by the redirector after [New] returns.
type Route struct {
	// Name is the route identifier reported in errors and logs.
	Name string
	// MatchRequest reports whether the route claims the inbound request.
	// A nil matcher accepts every request at the route's position; the first
	// matching route wins.
	MatchRequest func(context.Context, *sip.RequestEnvelope) bool
	// AuthorizeRequest authorizes the inbound request before contact
	// resolution. A nil authorizer allows the request; an error rejects
	// it with 403 Forbidden and the resolver is not invoked.
	AuthorizeRequest func(context.Context, *sip.RequestEnvelope) error
	// ResolveContacts resolves the redirection contacts carried into the
	// response Contact header. It is required: [New] rejects a route
	// without it.
	ResolveContacts func(context.Context, *sip.RequestEnvelope) (header.Contact, error)
	// SelectStatus selects the response status from the request and the
	// resolved contacts. A nil selector uses the constant [Route.Status].
	SelectStatus func(context.Context, *sip.RequestEnvelope, header.Contact) sip.ResponseStatus
	// Status is the constant redirect status used when SelectStatus is nil.
	// The zero value means 302 (Moved Temporarily); a non-zero value must
	// be one of 300, 301, or 302.
	Status sip.ResponseStatus
}

// validate reports whether the route is a usable redirector route.
// A route without a contact resolver, or with a non-zero constant Status
// outside the supported redirect set, is rejected.
func (r *Route) validate() error {
	if r.ResolveContacts == nil {
		return errclass.Classify(
			errors.ErrorfWrap("invalid redirector route %q: nil contact resolver", r.Name),
			sip.ErrClassElement,
		)
	}
	if r.Status != 0 && !redirectStatusSupported(r.Status) {
		return errclass.Classify(
			errors.ErrorfWrap("invalid redirector route %q: unsupported status %d", r.Name, r.Status),
			sip.ErrClassElement,
		)
	}
	return nil
}

// redirectStatusSupported reports whether sts names a redirect response
// status the redirector may produce: 300 Multiple Choices, 301 Moved
// Permanently, or 302 Moved Temporarily.
func redirectStatusSupported(sts sip.ResponseStatus) bool {
	switch sts {
	case sip.ResponseStatusMultipleChoices,
		sip.ResponseStatusMovedPermanently,
		sip.ResponseStatusMovedTemporarily:
		return true
	default:
		return false
	}
}

// Options configures a [Redirector].
type Options struct {
	// Logger is the fallback logger used when the processing context
	// carries none. Nil selects the package default logger.
	Logger *slog.Logger
}
