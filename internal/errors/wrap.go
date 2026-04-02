package errors

import (
	"errors"

	"braces.dev/errtrace"
)

// Wrap adds information about the program counter of the caller to the error.
// See [errtrace.Wrap].
func Wrap(err error) error {
	if err == nil {
		return nil
	}
	return GetCaller().Wrap(err) //errtrace:skip
}

// Wrap2 is used to [Wrap] the last error return when returning 2 values.
// See [errtrace.Wrap2].
func Wrap2[T any](v T, err error) (T, error) {
	if err == nil {
		return v, nil
	}
	return v, GetCaller().Wrap(err) //errtrace:skip
}

// Wrap3 is used to [Wrap] the last error return when returning 3 values.
// See [errtrace.Wrap3].
func Wrap3[T1, T2 any](v1 T1, v2 T2, err error) (T1, T2, error) {
	if err == nil {
		return v1, v2, nil
	}
	return v1, v2, GetCaller().Wrap(err) //errtrace:skip
}

// Unwrap returns the result of calling the Unwrap method on err,
// if err's type contains an Unwrap method returning error. Otherwise, Unwrap returns nil.
// See [errors.Unwrap].
func Unwrap(err error) error {
	return errors.Unwrap(err) //errtrace:skip
}

// As finds the first error in err's tree that matches target, and if one is found, sets
// target to that error value and returns true. Otherwise, it returns false.
// See [errors.As].
func As(err error, target any) bool { return errors.As(err, target) }

// AsType finds the first error in err's tree that matches the type E, and
// if one is found, returns that error value and true. Otherwise, it
// returns the zero value of E and false.
// See [errors.AsType].
func AsType[E error](err error) (E, bool) { return errors.AsType[E](err) }

// Is reports whether any error in err's tree matches target.
// The target must be comparable.
// See [errors.Is].
func Is(err, target error) bool { return errors.Is(err, target) }

type Caller struct{ errtrace.Caller }

func GetCaller() Caller {
	return Caller{errtrace.GetCaller()}
}

func (c Caller) Wrap(err error) error {
	if err == nil {
		return nil
	}
	return c.Caller.Wrap(err) //errtrace:skip
}

func (c Caller) Error(msg string) error {
	return c.Wrap(Error(msg)) //errtrace:skip
}

func (c Caller) Errorf(format string, args ...any) error {
	return c.Wrap(Errorf(format, args...)) //errtrace:skip
}

func (c Caller) Prefix(sentinel error, args ...any) error {
	return c.Wrap(Prefix(sentinel, args...)) //errtrace:skip
}

func (c Caller) Join(errs ...error) error {
	return c.Wrap(Join(errs...)) //errtrace:skip
}

func (c Caller) JoinPrefix(prefix string, errs ...error) error {
	return c.Wrap(JoinPrefix(prefix, errs...)) //errtrace:skip
}

// ErrorWrap returns an error with the given message and caller location.
// It is similar to calling [Error] then [Wrap].
func ErrorWrap(msg string) error {
	return GetCaller().Error(msg) //errtrace:skip
}

// ErrorfWrap returns an error with the given format string and arguments and caller location.
// It is similar to calling [Errorf] then [Wrap].
func ErrorfWrap(format string, args ...any) error {
	return GetCaller().Errorf(format, args...) //errtrace:skip
}

// PrefixWrap returns an error with the given sentinel error and arguments and caller location.
// It is similar to calling [Prefix] then [Wrap].
func PrefixWrap(sentinel error, args ...any) error {
	return GetCaller().Prefix(sentinel, args...) //errtrace:skip
}

// JoinWrap returns an error with the given errors joined and caller location.
// It is similar to calling [Join] then [Wrap].
func JoinWrap(errs ...error) error {
	return GetCaller().Join(errs...) //errtrace:skip
}

// JoinPrefixWrap returns an error with the given prefix and errors joined and caller location.
// It is similar to calling [JoinPrefix] then [Wrap].
func JoinPrefixWrap(prefix string, errs ...error) error {
	return GetCaller().JoinPrefix(prefix, errs...) //errtrace:skip
}
