// Package errclass provides error classification helpers and predefined error classes.
package errclass

import "errors"

// Class identifies a broad classification of an error.
//
//nolint:errname
type Class string

// Common error classes.
//
//nolint:errname
const (
	ClassClosed    Class = "closed"
	ClassTimeout   Class = "timeout"
	ClassTemporary Class = "temporary"
	ClassCanceled  Class = "canceled"
	ClassNotFound  Class = "not found"
	ClassConflict  Class = "conflict"
)

// Error returns the name of a single error class.
func (c Class) Error() string { return string(c) }

// Classes is a map of error classes.
type Classes = map[Class]bool

// Error is an error with additional classification information.
type Error struct {
	base    error
	classes Classes
}

// Classify creates an Error with the given base error and classes.
func Classify(base error, class Class, otherClasses ...Class) error {
	if base == nil {
		return nil
	}

	err := &Error{
		base:    base,
		classes: Classes{class: true},
	}
	for _, class := range otherClasses {
		err.classes[class] = true
	}
	return err
}

func (e *Error) Error() string {
	if e == nil || e.base == nil {
		return "<nil>"
	}
	return e.base.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.base
}

// Classes returns the error classes for this error.
func (e *Error) Classes() Classes {
	if e == nil {
		return nil
	}
	return e.classes
}

// Is reports whether the target error is classified as belonging to this error class.
func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}

	var cls Class
	return errors.As(target, &cls) && cls != "" && e.classes[cls]
}

// Closed reports whether the sentinel error describes a closed resource.
func (e *Error) Closed() bool { return e != nil && e.classes[ClassClosed] }

// Timeout reports whether the sentinel error describes a timeout.
func (e *Error) Timeout() bool { return e != nil && e.classes[ClassTimeout] }

// Temporary reports whether the sentinel error is temporary.
func (e *Error) Temporary() bool { return e != nil && e.classes[ClassTemporary] }

// Canceled reports whether the sentinel error was caused by cancellation.
func (e *Error) Canceled() bool { return e != nil && e.classes[ClassCanceled] }
