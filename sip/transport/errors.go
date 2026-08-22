package transport

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"

	"github.com/ghettovoice/gosip/sip"
)

const (
	ErrNetworkClosed          Error = "network closed"
	ErrTransportClosed        Error = "transport closed"
	ErrListenerTracked        Error = "listener already tracked"
	ErrListenerServing        Error = "listener already serving"
	ErrConnectionTracked      Error = "connection already tracked"
	ErrBrokenConnectionStream Error = "broken connection stream"
	ErrConnectionNotFound     Error = "connection not found"
)

type Error string

func (e Error) Error() string { return string(e) }

// Classes returns the classifications assigned to the sentinel error.
func (e Error) Classes() sip.ClassError { return errClasses(string(e)) }

// Is reports whether the sentinel error belongs to target class.
func (e Error) Is(target error) bool {
	if matchClassError(e.Classes(), target) {
		return true
	}

	isStdClosed := target == net.ErrClosed || target == io.ErrClosedPipe ||
		target == io.EOF || target == os.ErrClosed
	if isStdClosed && matchClassError(e.Classes(), sip.ErrClassClosed) {
		return true
	}

	return false
}

// Closed reports whether the sentinel error describes a closed resource.
func (e Error) Closed() bool { return e.Classes()&sip.ErrClassClosed != 0 }

// Timeout reports whether the sentinel error describes a timeout.
func (e Error) Timeout() bool { return e.Classes()&sip.ErrClassTimeout != 0 }

// Temporary reports whether the sentinel error is temporary.
func (e Error) Temporary() bool { return e.Classes()&sip.ErrClassTemporary != 0 }

// Canceled reports whether the sentinel error was caused by cancellation.
func (e Error) Canceled() bool { return e.Classes()&sip.ErrClassCanceled != 0 }

func errClasses(message string) sip.ClassError {
	switch message {
	case string(ErrConnectionNotFound):
		return sip.ErrClassTransport | sip.ErrClassNotFound
	case string(ErrBrokenConnectionStream):
		return sip.ErrClassTransport
	case string(ErrConnectionTracked), string(ErrListenerTracked), string(ErrListenerServing):
		return sip.ErrClassTransport | sip.ErrClassConflict
	case string(ErrTransportClosed), string(ErrNetworkClosed):
		return sip.ErrClassTransport | sip.ErrClassClosed

	default:
		return 0
	}
}

func matchClassError(classes sip.ClassError, target error) bool {
	var class sip.ClassError
	return errors.As(target, &class) && class != 0 && classes&class == class
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
