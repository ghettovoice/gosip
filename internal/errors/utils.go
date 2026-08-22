package errors

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"syscall"
)

// IsTemporaryError returns true if the error is temporary.
func IsTemporaryError(err error) bool {
	return hasErrorProperty(err, func(err error) bool {
		e, ok := err.(interface{ Temporary() bool })
		return ok && e.Temporary()
	})
}

// IsTimeoutError returns true if the error is a timeout error.
func IsTimeoutError(err error) bool {
	return hasErrorProperty(err, func(err error) bool {
		e, ok := err.(interface{ Timeout() bool })
		return ok && e.Timeout()
	})
}

// IsGrammarError returns true if the error is a grammar error.
func IsGrammarError(err error) bool {
	return hasErrorProperty(err, func(err error) bool {
		e, ok := err.(interface{ Grammar() bool })
		return ok && e.Grammar()
	})
}

// IsNetError returns true if the error is a network error.
func IsNetError(err error) bool {
	var e *net.OpError
	return errors.Is(err, syscall.EINVAL) || errors.As(err, &e)
}

func IsClosedError(err error) bool {
	return errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, os.ErrClosed) ||
		hasErrorProperty(err, func(err error) bool {
			e, ok := err.(interface{ Closed() bool })
			return ok && e.Closed()
		})
}

func IsCanceledError(err error) bool {
	return errors.Is(err, context.Canceled) ||
		hasErrorProperty(err, func(err error) bool {
			e, ok := err.(interface{ Canceled() bool })
			return ok && e.Canceled()
		})
}

func IsDeadlineError(err error) bool {
	return errors.Is(err, os.ErrDeadlineExceeded) ||
		errors.Is(err, context.DeadlineExceeded)
}

func hasErrorProperty(err error, predicate func(error) bool) bool {
	if err == nil {
		return false
	}
	if predicate(err) {
		return true
	}

	// Inspect the current node before traversing children so false properties do not hide later matches.
	//nolint:errorlint // direct assertions are intentional for one-node-at-a-time tree traversal
	switch err := err.(type) {
	case interface{ Unwrap() []error }:
		for _, nested := range err.Unwrap() {
			if hasErrorProperty(nested, predicate) {
				return true
			}
		}
	case interface{ Unwrap() error }:
		return hasErrorProperty(err.Unwrap(), predicate)
	}

	return false
}
