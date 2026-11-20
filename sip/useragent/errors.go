package useragent

import (
	"context"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/pkg/errclass"
)

// ErrClass is the error class assigned to user agent errors.
const ErrClass errclass.Class = "useragent"

// IsError reports whether err is classified as a user agent error.
func IsError(err error) bool {
	return errors.Is(err, ErrClass)
}

// ErrInvalidConfig reports a [New] call whose options fail validation: a
// missing or invalid address of record, an invalid contact, a negative
// authentication-attempt bound, or an unusable redirect policy.
const ErrInvalidConfig errors.Error = "invalid useragent options"

// NewInvalidConfigError creates an [ErrInvalidConfig] error classified
// as a user agent error.
func NewInvalidConfigError(args ...any) error {
	return errclass.Classify(
		errors.GetCaller().Prefix(ErrInvalidConfig, args...),
		ErrClass,
	)
}

// ErrInvalidRequest reports a user agent operation invoked with a request
// that fails validation before it can be sent.
const ErrInvalidRequest errors.Error = "invalid useragent request"

// NewInvalidRequestError creates an [ErrInvalidRequest] error classified
// as a user agent error.
func NewInvalidRequestError(args ...any) error {
	return errclass.Classify(
		errors.GetCaller().Prefix(ErrInvalidRequest, args...),
		ErrClass,
	)
}

// ErrMissingElement reports a user agent operation invoked without a
// processing element in the operation context.
const ErrMissingElement errors.Error = "missing element"

// NewMissingElementError creates an [ErrMissingElement] error classified
// as a user agent error.
func NewMissingElementError() error {
	return errclass.Classify(
		errors.GetCaller().Prefix(ErrMissingElement),
		ErrClass,
	)
}

// ErrClosed reports an operation attempted on a user agent whose lifecycle
// already ended.
const ErrClosed errors.Error = "useragent closed"

// NewClosedError creates an [ErrClosed] error classified as a closed user
// agent error.
func NewClosedError() error {
	return errclass.Classify(
		errors.GetCaller().Prefix(ErrClosed),
		ErrClass,
		errclass.ClassClosed,
	)
}

// ErrCanceled reports a user agent operation ended by context cancellation
// or deadline. The cancel cause stays attached to the constructed error,
// so a deadline cause keeps matching [context.DeadlineExceeded] instead of
// collapsing into plain cancellation.
const ErrCanceled errors.Error = "useragent operation canceled"

// NewCanceledError creates an [ErrCanceled] error classified as a user
// agent error and as canceled — or as a timeout when cause is a deadline.
// A nil cause still yields a live [ErrCanceled] error, never a typed nil.
func NewCanceledError(cause error) error {
	class := errclass.ClassCanceled
	if errors.Is(cause, context.DeadlineExceeded) {
		class = errclass.ClassTimeout
	}
	return errclass.Classify(
		errors.GetCaller().Prefix(ErrCanceled, cause),
		ErrClass,
		class,
	)
}

// ErrAuthenticationLimited reports a 401/407 challenge chain that reached
// the configured authentication-attempt bound.
const ErrAuthenticationLimited errors.Error = "authentication attempt limit reached"

// NewAuthenticationLimitedError creates an [ErrAuthenticationLimited]
// error classified as a user agent error.
func NewAuthenticationLimitedError(args ...any) error {
	return errclass.Classify(
		errors.GetCaller().Prefix(ErrAuthenticationLimited, args...),
		ErrClass,
	)
}

// ErrRedirectLoop reports a redirect chain that returned to an already
// visited target.
const ErrRedirectLoop errors.Error = "redirect loop detected"

// NewRedirectLoopError creates an [ErrRedirectLoop] error classified
// as a user agent error.
func NewRedirectLoopError(args ...any) error {
	return errclass.Classify(
		errors.GetCaller().Prefix(ErrRedirectLoop, args...),
		ErrClass,
	)
}

// ErrResponseFinal reports a second final response attempted on an inbound
// request that was already answered.
const ErrResponseFinal errors.Error = "final response already sent"

// NewResponseFinalError creates an [ErrResponseFinal] error classified
// as a conflicting user agent error.
func NewResponseFinalError() error {
	return errclass.Classify(
		errors.GetCaller().Prefix(ErrResponseFinal),
		ErrClass,
		errclass.ClassConflict,
	)
}

// ErrCSeqExhausted reports a retry that cannot allocate a fresh CSeq: the
// sequence reached its maximum value.
const ErrCSeqExhausted errors.Error = "cseq sequence exhausted"

// NewCSeqExhaustedError creates an [ErrCSeqExhausted] error classified
// as a user agent error.
func NewCSeqExhaustedError() error {
	return errclass.Classify(
		errors.GetCaller().Prefix(ErrCSeqExhausted),
		ErrClass,
	)
}

// ErrNoFinalResponse reports an operation outcome asked for a final
// response when the exchange produced none.
const ErrNoFinalResponse errors.Error = "no final response"

// NewNoFinalResponseError creates an [ErrNoFinalResponse] error classified
// as a user agent error.
func NewNoFinalResponseError() error {
	return errclass.Classify(
		errors.GetCaller().Prefix(ErrNoFinalResponse),
		ErrClass,
	)
}
