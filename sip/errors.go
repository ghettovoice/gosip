package sip

import "github.com/ghettovoice/gosip/internal/errorutil"

// Common errors.
const (
	ErrInvalidArgument        = errorutil.ErrInvalidArgument
	ErrActionNotAllowed Error = "action not allowed"
)

// Transaction errors.
const (
	ErrTransactionNotFound      Error = "transaction not found"
	ErrTransactionTimedOut      Error = "transaction timed out"
	ErrTransactionManagerClosed Error = "transaction manager closed"
)

// Transport errors.
const (
	// ErrTransportClosed is returned when attempting to use a closed transport.
	ErrTransportClosed Error = "transport closed"
	// ErrNoTarget is returned when no target for the message is resolved.
	ErrNoTarget Error = "no target resolved"
	// ErrUnhandledMessage is returned when the message wasn't handled by any receiver or sender.
	ErrUnhandledMessage Error = "unhandled message"
	ErrNoTransport      Error = "no transport resolved"

	errNoConn Error = "no connection found"
)

// Message errors.
const (
	ErrInvalidMessage    Error = "invalid message"
	ErrEntityTooLarge    Error = "entity too large"
	ErrMessageTooLarge   Error = "message too large"
	ErrMethodNotAllowed  Error = "request method not allowed"
	ErrMessageNotMatched Error = "message not matched"

	errMissHdrs Error = "missing mandatory headers"
)

// Error represents a SIP error.
// See [errorutil.Error].
type Error = errorutil.Error

// NewInvalidArgumentError creates a new error with [ErrInvalidArgument] or
// wraps provided error with [ErrInvalidArgument].
func NewInvalidArgumentError(args ...any) error {
	return errorutil.NewInvalidArgumentError(args...) //errtrace:skip
}
