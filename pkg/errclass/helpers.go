package errclass

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
)

func traverseErr(err error, predicate func(error) bool) bool {
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
			if traverseErr(nested, predicate) {
				return true
			}
		}
	case interface{ Unwrap() error }:
		return traverseErr(err.Unwrap(), predicate)
	}
	return false
}

// IsTemporary returns true if the error is temporary.
func IsTemporary(err error) bool {
	return errors.Is(err, ClassTemporary) ||
		traverseErr(err, func(err error) bool {
			e, ok := err.(interface{ Temporary() bool })
			return ok && e.Temporary()
		})
}

// IsTimeout returns true if the error is a timeout error.
func IsTimeout(err error) bool {
	return errors.Is(err, ClassTimeout) ||
		traverseErr(err, func(err error) bool {
			e, ok := err.(interface{ Timeout() bool })
			return ok && e.Timeout()
		})
}

// IsGrammar returns true if the error is a grammar error.
func IsGrammar(err error) bool {
	return traverseErr(err, func(err error) bool {
		e, ok := err.(interface{ Grammar() bool })
		return ok && e.Grammar()
	})
}

// IsNetwork returns true if the error is a network error.
func IsNetwork(err error) bool {
	var e *net.OpError
	return errors.As(err, &e)
}

// IsClosed returns true if the error is a closed error.
func IsClosed(err error) bool {
	return errors.Is(err, ClassClosed) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, os.ErrClosed) ||
		traverseErr(err, func(err error) bool {
			e, ok := err.(interface{ Closed() bool })
			return ok && e.Closed()
		})
}

// IsCanceled returns true if the error is a canceled error.
func IsCanceled(err error) bool {
	return errors.Is(err, ClassCanceled) ||
		errors.Is(err, context.Canceled) ||
		traverseErr(err, func(err error) bool {
			e, ok := err.(interface{ Canceled() bool })
			return ok && e.Canceled()
		})
}

// IsDeadline returns true if the error is a deadline error.
func IsDeadline(err error) bool {
	return errors.Is(err, os.ErrDeadlineExceeded) ||
		errors.Is(err, context.DeadlineExceeded)
}

func IsNotFound(err error) bool { return errors.Is(err, ClassNotFound) }

func IsConflict(err error) bool { return errors.Is(err, ClassConflict) }
