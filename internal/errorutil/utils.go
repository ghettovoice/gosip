package errorutil

import (
	"errors"
	"net"
	"syscall"
)

// IsTemporaryErr returns true if the error is temporary.
func IsTemporaryErr(err error) bool {
	var e interface{ Temporary() bool }
	return errors.As(err, &e) && e.Temporary()
}

// IsTimeoutErr returns true if the error is a timeout error.
func IsTimeoutErr(err error) bool {
	var e interface{ Timeout() bool }
	return errors.As(err, &e) && e.Timeout()
}

// IsGrammarErr returns true if the error is a grammar error.
func IsGrammarErr(err error) bool {
	var e interface{ Grammar() bool }
	return errors.As(err, &e) && e.Grammar()
}

// IsNetError returns true if the error is a network error.
func IsNetError(err error) bool {
	var e *net.OpError
	return errors.Is(err, syscall.EINVAL) || errors.As(err, &e)
}
