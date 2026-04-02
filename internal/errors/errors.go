// Package errors provides error handling utilities for the gosip library.
// It includes custom error types, error wrapping functions, and utilities
// for working with multiple errors. The package can be used as a full replacement
// of the std errors package.
//
// Wrapping helpers are based on braces.dev/errtrace.
package errors

import (
	"errors"
	"fmt"
)

// Error is a string type that implements the error interface.
type Error string

func (s Error) Error() string { return string(s) }

// New returns an error with the supplied text.
func New(msg string) error {
	return Error(msg) //errtrace:skip
}

// Errorf formats according to a format specifier and returns the string as a
// value that satisfies error.
func Errorf(format string, args ...any) error {
	return fmt.Errorf(format, args...) //errtrace:skip
}

// Prefix prepends an error with a sentinel error and adds caller information.
// It supports multiple argument patterns:
//   - No args: returns sentinel
//   - error arg: prefix with sentinel (unless already prefixed)
//   - string arg: formats as message with sentinel prefix
//   - string + args: formats with Sprintf then prefixes with sentinel
func Prefix(sentinel error, args ...any) error {
	if sentinel == nil {
		return nil
	}

	if len(args) == 0 {
		return sentinel //errtrace:skip
	}

	switch v := args[0].(type) {
	case error:
		if errors.Is(v, sentinel) {
			return v //errtrace:skip
		}
		return fmt.Errorf("%w: %w", sentinel, v) //errtrace:skip
	case string:
		if len(args) == 1 {
			return fmt.Errorf("%w: %s", sentinel, v) //errtrace:skip
		}
		return fmt.Errorf("%w: %s", sentinel, fmt.Sprintf(v, args[1:]...)) //errtrace:skip
	default:
		return sentinel //errtrace:skip
	}
}

const (
	// ErrInvalidArgument is a sentinel error for invalid arguments.
	ErrInvalidArgument Error = "invalid argument"
	ErrNilReceiver     Error = "nil receiver"
)

// NewInvalidArgumentError returns an error prefixed with [ErrInvalidArgument].
func NewInvalidArgumentError(args ...any) error {
	return Prefix(ErrInvalidArgument, args...) //errtrace:skip
}

// NewInvalidArgumentErrorWrap returns an error prefixed with [ErrInvalidArgument] and caller location.
// It is similar to calling [NewInvalidArgumentError] then [Wrap].
func NewInvalidArgumentErrorWrap(args ...any) error {
	return GetCaller().Prefix(ErrInvalidArgument, args...) //errtrace:skip
}

// NewNilReceiverError returns an error prefixed with [ErrNilReceiver] and caller location.
func NewNilReceiverError(args ...any) error {
	return Prefix(ErrNilReceiver, args...) //errtrace:skip
}

// NewNilReceiverErrorWrap returns an error prefixed with [ErrNilReceiver] and caller location.
// It is similar to calling [NewNilReceiverError] then [Wrap].
func NewNilReceiverErrorWrap(args ...any) error {
	return GetCaller().Prefix(ErrNilReceiver, args...) //errtrace:skip
}
