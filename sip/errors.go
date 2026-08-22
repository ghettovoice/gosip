package sip

import (
	"fmt"
	"log/slog"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

// Common errors.
const (
	ErrActionNotAllowed Error = "action not allowed"
	ErrNoAddress        Error = "no address resolved"
)

// ClassError identifies a broad classification of an error.
type ClassError uint32

// Error classes.
const (
	ErrClassMessage ClassError = 1 << iota
	ErrClassTransport
	ErrClassTransaction
	ErrClassElement
	ErrClassParser
	ErrClassProxy
	ErrClassClosed
	ErrClassTimeout
	ErrClassTemporary
	ErrClassCanceled
	ErrClassInvalidState
	ErrClassNotFound
	ErrClassConflict
)

// Error returns the name of a single error class.
func (c ClassError) Error() string { return c.String() }

// String returns the name of a single error class.
func (c ClassError) String() string {
	switch c {
	case ErrClassMessage:
		return "message"
	case ErrClassTransport:
		return "transport"
	case ErrClassTransaction:
		return "transaction"
	case ErrClassElement:
		return "element"
	case ErrClassParser:
		return "parser"
	case ErrClassProxy:
		return "proxy"
	case ErrClassClosed:
		return "closed"
	case ErrClassTimeout:
		return "timeout"
	case ErrClassTemporary:
		return "temporary"
	case ErrClassCanceled:
		return "canceled"
	case ErrClassInvalidState:
		return "invalid state"
	case ErrClassNotFound:
		return "not found"
	case ErrClassConflict:
		return "conflict"
	default:
		return "multiple or unknown"
	}
}

func matchClassError(classes ClassError, target error) bool {
	var class ClassError
	return errors.As(target, &class) && class != 0 && classes&class == class
}

func errClasses(message string) ClassError {
	switch message {
	case string(ErrInvalidMessage),
		string(ErrEntityTooLarge),
		string(ErrMessageTooLarge),
		string(ErrMethodNotAllowed),
		string(ErrMessageNotMatched),
		string(ErrUnhandledMessage):
		return ErrClassMessage

	case string(ErrNoTransport):
		return ErrClassTransport | ErrClassNotFound
	case string(ErrTransportManagerClosed):
		return ErrClassTransport | ErrClassClosed

	case string(ErrActionNotAllowed):
		return ErrClassTransaction | ErrClassInvalidState
	case string(ErrTransactionNotFound):
		return ErrClassTransaction | ErrClassNotFound
	case string(ErrTransactionTimedOut):
		return ErrClassTransaction | ErrClassTimeout
	case string(ErrDuplicateTransaction):
		return ErrClassTransaction | ErrClassConflict
	case string(ErrTransactionManagerClosed):
		return ErrClassTransaction | ErrClassClosed

	case string(ErrElementClosed):
		return ErrClassElement | ErrClassClosed
	case string(ErrNoAddress):
		return ErrClassElement | ErrClassNotFound

	case string(ErrForwardContextDuplicate):
		return ErrClassProxy | ErrClassConflict
	case string(ErrForwardContextNotFound):
		return ErrClassProxy | ErrClassNotFound
	case string(errUnsupURIScheme),
		string(errToManyHops),
		string(errUnsupOption),
		string(errAuthRequired),
		string(errNoFwdTargets):
		return ErrClassProxy

	default:
		return 0
	}
}

// IsClosedError reports whether err describes a closed resource.
func IsClosedError(err error) bool {
	return errors.Is(err, ErrClassClosed) || errors.IsClosedError(err)
}

// IsTimeoutError reports whether err describes a timeout.
func IsTimeoutError(err error) bool {
	return errors.Is(err, ErrClassTimeout) || errors.IsTimeoutError(err)
}

// IsTemporaryError reports whether err is temporary.
func IsTemporaryError(err error) bool {
	return errors.Is(err, ErrClassTemporary) || errors.IsTemporaryError(err)
}

// IsCanceledError reports whether err was caused by cancellation.
func IsCanceledError(err error) bool {
	return errors.Is(err, ErrClassCanceled) || errors.IsCanceledError(err)
}

// Error is a string-based SIP sentinel error.
type Error string

func (e Error) Error() string { return string(e) }

// Classes returns the classifications assigned to the sentinel error.
func (e Error) Classes() ClassError { return errClasses(string(e)) }

// Is reports whether the sentinel error belongs to target class.
func (e Error) Is(target error) bool { return matchClassError(e.Classes(), target) }

// Closed reports whether the sentinel error describes a closed resource.
func (e Error) Closed() bool { return e.Classes()&ErrClassClosed != 0 }

// Timeout reports whether the sentinel error describes a timeout.
func (e Error) Timeout() bool { return e.Classes()&ErrClassTimeout != 0 }

// Temporary reports whether the sentinel error is temporary.
func (e Error) Temporary() bool { return e.Classes()&ErrClassTemporary != 0 }

// Canceled reports whether the sentinel error was caused by cancellation.
func (e Error) Canceled() bool { return e.Classes()&ErrClassCanceled != 0 }

type RequestRejectedError struct {
	cause       error
	resStatus   ResponseStatus
	respondOpts RespondOptions
	logLevel    slog.Level
}

func NewRequestRejectedError(
	cause error,
	logLevel slog.Level,
	resStatus ResponseStatus,
	respondOpts ...RespondOptions,
) *RequestRejectedError {
	return &RequestRejectedError{
		cause:       cause,
		resStatus:   resStatus,
		respondOpts: util.LastSliceElemOr(respondOpts, RespondOptions{}),
		logLevel:    logLevel,
	}
}

func (e *RequestRejectedError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("request rejected: %v", e.cause)
}

func (e *RequestRejectedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (*RequestRejectedError) Rejected() bool { return true }

func (e *RequestRejectedError) ResponseStatus() ResponseStatus {
	if e == nil {
		return 0
	}
	return e.resStatus
}

func (e *RequestRejectedError) RespondOptions() RespondOptions {
	if e == nil {
		return RespondOptions{}
	}
	return e.respondOpts
}

func (e *RequestRejectedError) LogLevel() slog.Level {
	if e == nil {
		return 0
	}
	return e.logLevel
}

type ResponseRejectedError struct {
	cause    error
	logLevel slog.Level
}

func NewResponseRejectedError(cause error, logLevel slog.Level) *ResponseRejectedError {
	return &ResponseRejectedError{
		cause:    cause,
		logLevel: logLevel,
	}
}

func (e *ResponseRejectedError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("response rejected: %v", e.cause)
}

func (e *ResponseRejectedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (*ResponseRejectedError) Rejected() bool { return true }

func (e *ResponseRejectedError) LogLevel() slog.Level {
	if e == nil {
		return 0
	}
	return e.logLevel
}
