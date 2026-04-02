package errors_test

import (
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
			name: "other error",
			err:  errors.New("other"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := errors.IsTemporaryErr(tt.err)
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
			name: "other error",
			err:  errors.New("other"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := errors.IsTimeoutErr(tt.err)
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
			name: "other error",
			err:  errors.New("other"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := errors.IsGrammarErr(tt.err)
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
