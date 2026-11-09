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
	//nolint:err113 // this is a error constructor
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

	switch arg := args[0].(type) {
	case error:
		if errors.Is(arg, sentinel) {
			return arg //errtrace:skip
		}
		return fmt.Errorf("%w: %w", sentinel, arg) //errtrace:skip
	case string:
		if len(args) == 1 {
			return fmt.Errorf("%w: %s", sentinel, arg) //errtrace:skip
		}
		return fmt.Errorf("%w: %s", sentinel, fmt.Sprintf(arg, args[1:]...)) //errtrace:skip
	default:
		return sentinel //errtrace:skip
	}
}
