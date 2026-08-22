package errors_test

import (
	"context"
	"net"
	"syscall"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
)

func TestIsTemporaryErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "temporary",
			err:  temporaryError{temporary: true},
			want: true,
		},
		{
			name: "not temporary",
			err:  temporaryError{temporary: false},
			want: false,
		},
		{
			name: "joined after not temporary",
			err: errors.Join(
				temporaryError{temporary: false},
				temporaryError{temporary: true},
			),
			want: true,
		},
		{
			name: "other error",
			err:  errors.New("other"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := errors.IsTemporaryError(tt.err)
			if got != tt.want {
				t.Errorf("errors.IsTemporaryErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsTimeoutErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "timeout",
			err:  timeoutError{timeout: true},
			want: true,
		},
		{
			name: "not timeout",
			err:  timeoutError{timeout: false},
			want: false,
		},
		{
			name: "joined after not timeout",
			err: errors.Join(
				timeoutError{timeout: false},
				timeoutError{timeout: true},
			),
			want: true,
		},
		{
			name: "other error",
			err:  errors.New("other"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := errors.IsTimeoutError(tt.err)
			if got != tt.want {
				t.Errorf("errors.IsTimeoutErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsGrammarErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "grammar",
			err:  grammarError{grammar: true},
			want: true,
		},
		{
			name: "not grammar",
			err:  grammarError{grammar: false},
			want: false,
		},
		{
			name: "joined after not grammar",
			err: errors.Join(
				grammarError{grammar: false},
				grammarError{grammar: true},
			),
			want: true,
		},
		{
			name: "other error",
			err:  errors.New("other"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := errors.IsGrammarError(tt.err)
			if got != tt.want {
				t.Errorf("errors.IsGrammarErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsNetError(t *testing.T) {
	t.Parallel()

	opErr := &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "syscall error",
			err:  syscall.EINVAL,
			want: true,
		},
		{
			name: "op error",
			err:  opErr,
			want: true,
		},
		{
			name: "other error",
			err:  errors.New("other"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := errors.IsNetError(tt.err)
			if got != tt.want {
				t.Errorf("errors.IsNetError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsClosedErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "closed", err: propertyError{closed: true}, want: true},
		{name: "not closed", err: propertyError{}, want: false},
		{
			name: "joined after not closed",
			err: errors.Join(
				propertyError{},
				propertyError{closed: true},
			),
			want: true,
		},
		{name: "network closed", err: net.ErrClosed, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := errors.IsClosedError(tt.err)
			if got != tt.want {
				t.Errorf("errors.IsClosedError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsCanceledErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "canceled", err: propertyError{canceled: true}, want: true},
		{name: "not canceled", err: propertyError{}, want: false},
		{
			name: "joined after not canceled",
			err: errors.Join(
				propertyError{},
				propertyError{canceled: true},
			),
			want: true,
		},
		{name: "context canceled", err: context.Canceled, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := errors.IsCanceledError(tt.err)
			if got != tt.want {
				t.Errorf("errors.IsCanceledError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

type propertyError struct {
	closed   bool
	canceled bool
}

func (propertyError) Error() string    { return "property error" }
func (e propertyError) Closed() bool   { return e.closed }
func (e propertyError) Canceled() bool { return e.canceled }
