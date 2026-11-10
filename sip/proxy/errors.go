package proxy

import (
	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/pkg/errclass"
)

// ErrClass is the error class assigned to proxy errors.
const ErrClass errclass.Class = "proxy"

// IsError reports whether err is classified as a proxy error.
func IsError(err error) bool {
	return errors.Is(err, ErrClass)
}

// ErrMissingElement reports a claimed proxy operation invoked without a
// processing element in the context: a matched inbound request or an
// owned inbound response, or a restore with no element binding.
const ErrMissingElement errors.Error = "missing processing element"

// NewMissingElementError creates an [ErrMissingElement] error classified
// as a proxy error.
func NewMissingElementError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrMissingElement, args...),
		ErrClass,
	)
}

// ErrClosed reports an operation attempted on a proxy whose lifecycle
// already ended: a new request claim, a restore install, or an observer
// bind.
const ErrClosed errors.Error = "proxy closed"

// NewClosedError creates an [ErrClosed] error classified as a
// closed proxy error.
func NewClosedError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrClosed, args...),
		ErrClass,
		errclass.ClassClosed,
	)
}

// ErrRequestStarted reports a [Request.Start] call on a request
// that is already active: a repeated activation of a restored request, or
// a request that was never dormant.
const ErrRequestStarted errors.Error = "proxy request already started"

// NewRequestStartedError creates an [ErrRequestStarted] error
// classified as a proxy error.
func NewRequestStartedError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrRequestStarted, args...),
		ErrClass,
		errclass.ClassConflict,
	)
}

// ErrInvalidSnapshot reports a [RequestSnapshot] and
// [RestoreBindings] set that fails [Proxy.RestoreRequest]
// validation: an unsupported snapshot version, a route or mode
// mismatch, malformed or duplicate progress records, conflicting
// transaction key or request relationships, incompatible final or
// cancellation state, invalid deadlines, or missing and mismatched
// runtime bindings.
const ErrInvalidSnapshot errors.Error = "invalid proxy request snapshot"

// NewInvalidSnapshotError creates an [ErrInvalidSnapshot] error
// classified as a proxy error.
func NewInvalidSnapshotError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrInvalidSnapshot, args...),
		ErrClass,
	)
}

// ErrSnapshotUnsupported reports a [RequestSnapshot] capture
// the request cannot support: an upstream transaction without the
// [sip.ServerTransactionSnapshotter] contract, a non-INVITE transaction
// without the [sip.NonInviteServerTransactionBehaviorProvider] contract,
// or an opaque metadata value that cannot be serialized.
const ErrSnapshotUnsupported errors.Error = "proxy request snapshot unsupported"

// NewSnapshotUnsupportedError creates an [ErrSnapshotUnsupported]
// error classified as a proxy error.
func NewSnapshotUnsupportedError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrSnapshotUnsupported, args...),
		ErrClass,
	)
}

// noop is an empty unbind function returned for refused subscriptions.
var noop = func() {}

// errServiceUnavail classifies an attempt failure that answered with a
// 503 Service Unavailable: the runner may advance to another target.
const errServiceUnavail errors.Error = "service unavailable"
