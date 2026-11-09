package transport

import (
	"fmt"
	"log/slog"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/sip"
)

const (
	ErrNetworkClosed    errors.Error = "network closed"
	ErrTransportClosed  errors.Error = "transport closed"
	ErrListenerTracked  errors.Error = "listener already tracked"
	ErrListenerServing  errors.Error = "listener already serving"
	ErrConnTracked      errors.Error = "connection already tracked"
	ErrBrokenConnStream errors.Error = "broken connection stream"
	ErrConnNotFound     errors.Error = "connection not found"
)

func NewNetworkClosedError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrNetworkClosed, args...),
		errclass.ClassClosed,
		sip.ErrClassTransport,
	)
}

func NewTransportClosedError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrTransportClosed, args...),
		errclass.ClassClosed,
		sip.ErrClassTransport,
	)
}

func NewListenerTrackedError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrListenerTracked, args...),
		errclass.ClassConflict,
		sip.ErrClassTransport,
	)
}

func NewListenerServingError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrListenerServing, args...),
		errclass.ClassConflict,
		sip.ErrClassTransport,
	)
}

func NewConnTrackedError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrConnTracked, args...),
		errclass.ClassConflict,
		sip.ErrClassTransport,
	)
}

func NewBrokenConnStreamError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrBrokenConnStream, args...),
		sip.ErrClassTransport,
	)
}

func NewConnNotFoundError(args ...any) error {
	return errclass.Classify(
		errors.Prefix(ErrConnNotFound, args...),
		errclass.ClassNotFound,
		sip.ErrClassTransport,
	)
}

type RequestRejectedError interface {
	error
	Rejected() bool
	ResponseStatus() sip.ResponseStatus
	RespondOptions() sip.RespondOptions
	LogLevel() slog.Level
}

type reqRejectedError struct {
	cause       error
	resStatus   sip.ResponseStatus
	respondOpts sip.RespondOptions
	logLevel    slog.Level
}

func (e *reqRejectedError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("request rejected: %v", e.cause)
}

func (e *reqRejectedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (*reqRejectedError) Rejected() bool { return true }

func (e *reqRejectedError) ResponseStatus() sip.ResponseStatus {
	if e == nil {
		return 0
	}
	return e.resStatus
}

func (e *reqRejectedError) RespondOptions() sip.RespondOptions {
	if e == nil {
		return sip.RespondOptions{}
	}
	return e.respondOpts
}

func (e *reqRejectedError) LogLevel() slog.Level {
	if e == nil {
		return 0
	}
	return e.logLevel
}

type ResponseRejectedError interface {
	error
	Rejected() bool
	LogLevel() slog.Level
}

type resRejectedError struct {
	cause    error
	logLevel slog.Level
}

func (e *resRejectedError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("response rejected: %v", e.cause)
}

func (e *resRejectedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (*resRejectedError) Rejected() bool { return true }

func (e *resRejectedError) LogLevel() slog.Level {
	if e == nil {
		return 0
	}
	return e.logLevel
}
