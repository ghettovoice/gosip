package sip

import (
	"fmt"
	"log/slog"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/pkg/errclass"
)

// SIP error classes.
const (
	ErrClassMessage     errclass.Class = "message"
	ErrClassTransport   errclass.Class = "transport"
	ErrClassTransaction errclass.Class = "transaction"
	ErrClassElement     errclass.Class = "element"
	ErrClassParser      errclass.Class = "parser"
	ErrClassProxy       errclass.Class = "proxy"
)

// Common SIP errors.
const (
	ErrNoAddress errors.Error = "no address resolved"
)

func NewNoAddressError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrNoAddress, args...),
		errclass.ClassNotFound,
	)
}

// func errClasses(message string) ClassError {
// 	switch message {
// 	case string(ErrInvalidMessage),
// 		string(ErrEntityTooLarge),
// 		string(ErrMessageTooLarge),
// 		string(ErrMethodNotAllowed),
// 		string(ErrMessageNotMatched),
// 		string(ErrUnhandledMessage):
// 		return ErrClassMessage

// 	case string(ErrNoTransport):
// 		return ErrClassTransport | ErrClassNotFound
// 	case string(ErrTransportManagerClosed):
// 		return ErrClassTransport | ErrClassClosed

// 	case string(ErrActionNotAllowed):
// 		return ErrClassTransaction | ErrClassInvalidState
// 	case string(ErrTransactionNotFound):
// 		return ErrClassTransaction | ErrClassNotFound
// 	case string(ErrTransactionTimedOut):
// 		return ErrClassTransaction | ErrClassTimeout
// 	case string(ErrDuplicateTransaction):
// 		return ErrClassTransaction | ErrClassConflict
// 	case string(ErrTransactionManagerClosed):
// 		return ErrClassTransaction | ErrClassClosed

// 	case string(ErrElementClosed):
// 		return ErrClassElement | ErrClassClosed
// 	case string(ErrNoAddress):
// 		return ErrClassElement | ErrClassNotFound

// 	case string(ErrForwardContextDuplicate):
// 		return ErrClassProxy | ErrClassConflict
// 	case string(ErrForwardContextNotFound):
// 		return ErrClassProxy | ErrClassNotFound
// 	case string(errUnsupURIScheme),
// 		string(errToManyHops),
// 		string(errUnsupOption),
// 		string(errAuthRequired),
// 		string(errNoFwdTargets):
// 		return ErrClassProxy

// 	default:
// 		return 0
// 	}
// }

type RequestRejectedError struct {
	cause   error
	resSts  ResponseStatus
	resOpts RespondOptions
	logLvl  slog.Level
}

// NewRequestRejectedError creates a request rejection error that also carries
// a suggested response status, respond options, and log level.
func NewRequestRejectedError(
	cause error,
	level slog.Level,
	status ResponseStatus,
	opts ...RespondOptions,
) *RequestRejectedError {
	return &RequestRejectedError{
		cause:   cause,
		logLvl:  level,
		resSts:  status,
		resOpts: util.LastSliceElemOr(opts, RespondOptions{}),
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
	return e.resSts
}

func (e *RequestRejectedError) RespondOptions() RespondOptions {
	if e == nil {
		return RespondOptions{}
	}
	return e.resOpts
}

func (e *RequestRejectedError) LogLevel() slog.Level {
	if e == nil {
		return 0
	}
	return e.logLvl
}

type ResponseRejectedError struct {
	cause  error
	logLvl slog.Level
}

// NewResponseRejectedError creates a response rejection error with the given cause and log level.
func NewResponseRejectedError(cause error, level slog.Level) *ResponseRejectedError {
	return &ResponseRejectedError{
		cause:  cause,
		logLvl: level,
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
	return e.logLvl
}

// Message errors.
const (
	ErrInvalidMessage          errors.Error = "invalid message"
	ErrMessageTooLarge         errors.Error = "message too large"
	ErrMessageBodyTooLarge     errors.Error = "message body too large"
	ErrRequestMethodNotAllowed errors.Error = "request method not allowed"
	ErrMessageNotMatched       errors.Error = "message not matched"
	ErrUnhandledMessage        errors.Error = "unhandled message"
)

// IsMessageError reports whether err belongs to the message error class.
func IsMessageError(err error) bool {
	return errors.Is(err, ErrClassMessage)
}

func NewInvalidMessageError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrInvalidMessage, args...),
		ErrClassMessage,
	)
}

func NewMessageTooLargeError(size uint) error {
	return errclass.Classify(
		errors.Prefix(ErrMessageTooLarge, "message exceeds max size %d", size),
		ErrClassMessage,
	)
}

func NewMessageBodyTooLargeError(size uint) error {
	return errclass.Classify(
		errors.Prefix(ErrMessageBodyTooLarge, "message body exceeds max size %d", size),
		ErrClassMessage,
	)
}

func NewRequestMethodNotAllowedError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrRequestMethodNotAllowed, args...),
		ErrClassMessage,
	)
}

func NewMessageNotMatched(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrMessageNotMatched, args...),
		ErrClassMessage,
	)
}

func NewUnhandledMessage(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrUnhandledMessage, args...),
		ErrClassMessage,
	)
}

// Transport errors.
const (
	ErrNoTransport            errors.Error = "no transport resolved"
	ErrTransportManagerClosed errors.Error = "transport manager closed"
)

// IsTransportError reports whether err belongs to the transport error class.
func IsTransportError(err error) bool {
	return errclass.IsNetwork(err) || errors.Is(err, ErrClassTransport)
}

func NewNoTransportError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrNoTransport, args...),
		ErrClassTransport,
		errclass.ClassNotFound,
	)
}

func NewTransportManagerClosedError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrTransportManagerClosed, args...),
		ErrClassTransport,
		errclass.ClassClosed,
	)
}

// Transaction errors.
const (
	ErrTransactionActionNotAllowed errors.Error = "transaction action not allowed"
	ErrTransactionNotFound         errors.Error = "transaction not found"
	ErrTransactionTimedOut         errors.Error = "transaction timed out"
	ErrTransactionDuplicate        errors.Error = "transaction duplicate"
	ErrInvalidTransactionSnapshot  errors.Error = "invalid transaction snapshot"
	ErrTransactionManagerClosed    errors.Error = "transaction manager closed"
)

// IsTransactionError reports whether err belongs to the transaction error class.
func IsTransactionError(err error) bool {
	return errors.Is(err, ErrClassTransaction)
}

func NewTransactionActionNotAllowedError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrTransactionActionNotAllowed, args...),
		ErrClassTransaction,
	)
}

func NewTransactionNotFoundError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrTransactionNotFound, args...),
		ErrClassTransaction,
		errclass.ClassNotFound,
	)
}

func NewTransactionTimedOutError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrTransactionTimedOut, args...),
		ErrClassTransaction,
		errclass.ClassTimeout,
	)
}

func NewTransactionDuplicateError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrTransactionDuplicate, args...),
		ErrClassTransaction,
		errclass.ClassConflict,
	)
}

func NewInvalidTransactionSnapshotError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrInvalidTransactionSnapshot, args...),
		ErrClassTransaction,
	)
}

func NewTransactionManagerClosedError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrTransactionManagerClosed, args...),
		ErrClassTransaction,
		errclass.ClassClosed,
	)
}

// Element errors.
const (
	ErrElementClosed errors.Error = "element closed"
)

func IsElementError(err error) bool {
	return errors.Is(err, ErrClassElement)
}

func NewElementClosedError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrElementClosed, args...),
		ErrClassElement,
		errclass.ClassClosed,
	)
}

// Proxy errors.
const (
	ErrForwardContextDuplicate errors.Error = "forward context duplicate"
	ErrForwardContextNotFound  errors.Error = "forward context not found"
)

func NewForwardContextDuplicateError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrForwardContextDuplicate, args...),
		ErrClassProxy,
		errclass.ClassConflict,
	)
}

func NewForwardContextNotFoundError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrForwardContextNotFound, args...),
		ErrClassProxy,
		errclass.ClassNotFound,
	)
}
