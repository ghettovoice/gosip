package sip_test

import (
	"context"
	stdErrors "errors"
	"fmt"
	"io"
	"testing"

	"github.com/ghettovoice/gosip/sip"
)

func TestClassError_UsesErrorTree(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		err   error
		class sip.ClassError
		want  bool
	}{
		{
			name:  "wrapped message",
			err:   fmt.Errorf("parse request: %w", sip.ErrInvalidMessage),
			class: sip.ErrClassMessage,
			want:  true,
		},
		// {
		// 	name:  "closed transport",
		// 	err:   sip.ErrConnClosed,
		// 	class: sip.ErrClassTransport,
		// 	want:  true,
		// },
		// {
		// 	name:  "closed resource",
		// 	err:   sip.ErrConnClosed,
		// 	class: sip.ErrClassClosed,
		// 	want:  true,
		// },
		{
			name:  "transaction timeout",
			err:   sip.ErrTransactionTimedOut,
			class: sip.ErrClassTransaction | sip.ErrClassTimeout,
			want:  true,
		},
		{
			name:  "parser",
			err:   &sip.ParseError{Err: stdErrors.New("parse failed")},
			class: sip.ErrClassParser,
			want:  true,
		},
		// {
		// 	name: "joined errors",
		// 	err: stdErrors.Join(
		// 		sip.ErrInvalidMessage,
		// 		sip.ErrConnClosed,
		// 	),
		// 	class: sip.ErrClassTransport,
		// 	want:  true,
		// },
		{
			name:  "unrelated class",
			err:   sip.ErrInvalidMessage,
			class: sip.ErrClassTransport,
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := stdErrors.Is(tt.err, tt.class)
			if got != tt.want {
				t.Errorf("errors.Is(%v, %v) = %v, want %v", tt.err, tt.class, got, tt.want)
			}
		})
	}
}

func TestErrorHelpers_ClassAndStandardErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fn   func(error) bool
		err  error
		want bool
	}{
		// {name: "closed sentinel", fn: sip.IsClosedError, err: sip.ErrConnClosed, want: true},
		{name: "closed EOF", fn: sip.IsClosedError, err: io.EOF, want: true},
		{name: "timeout sentinel", fn: sip.IsTimeoutError, err: sip.ErrTransactionTimedOut, want: true},
		{name: "canceled context", fn: sip.IsCanceledError, err: context.Canceled, want: true},
		{name: "temporary error", fn: sip.IsTemporaryError, err: temporaryError{}, want: true},
		{name: "ordinary error", fn: sip.IsTemporaryError, err: stdErrors.New("ordinary"), want: false},
		{
			name: "joined timeout after non-timeout sentinel",
			fn:   sip.IsTimeoutError,
			err:  stdErrors.Join(sip.ErrInvalidMessage, behaviorError{timeout: true}),
			want: true,
		},
		{
			name: "joined temporary after non-temporary sentinel",
			fn:   sip.IsTemporaryError,
			err:  stdErrors.Join(sip.ErrInvalidMessage, behaviorError{temporary: true}),
			want: true,
		},
		{
			name: "joined closed after non-closed sentinel",
			fn:   sip.IsClosedError,
			err:  stdErrors.Join(sip.ErrInvalidMessage, behaviorError{closed: true}),
			want: true,
		},
		{
			name: "joined canceled after non-canceled sentinel",
			fn:   sip.IsCanceledError,
			err:  stdErrors.Join(sip.ErrInvalidMessage, behaviorError{canceled: true}),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.fn(tt.err)
			if got != tt.want {
				t.Errorf("error helper(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestErrorInterfaces_ParseErrorDelegatesBehavior(t *testing.T) {
	t.Parallel()

	parseErr := &sip.ParseError{Err: behaviorError{
		timeout:   true,
		temporary: true,
		canceled:  true,
	}}

	var timeout interface{ Timeout() bool } = parseErr
	if !timeout.Timeout() {
		t.Errorf("ParseError.Timeout() = false, want true")
	}

	var temporary interface{ Temporary() bool } = parseErr
	if !temporary.Temporary() {
		t.Errorf("ParseError.Temporary() = false, want true")
	}

	var canceled interface{ Canceled() bool } = parseErr
	if !canceled.Canceled() {
		t.Errorf("ParseError.Canceled() = false, want true")
	}

	// var closed interface{ Closed() bool } = sip.ErrConnClosed
	// if !closed.Closed() {
	// 	t.Errorf("ClosedError.Closed() = false, want true")
	// }

	var sentinelTimeout interface{ Timeout() bool } = sip.ErrTransactionTimedOut
	if !sentinelTimeout.Timeout() {
		t.Errorf("Error.Timeout() = false, want true")
	}
}

type behaviorError struct {
	closed    bool
	timeout   bool
	temporary bool
	canceled  bool
}

func (behaviorError) Error() string     { return "behavior error" }
func (e behaviorError) Closed() bool    { return e.closed }
func (e behaviorError) Timeout() bool   { return e.timeout }
func (e behaviorError) Temporary() bool { return e.temporary }
func (e behaviorError) Canceled() bool  { return e.canceled }

type temporaryError struct{}

func (temporaryError) Error() string   { return "temporary error" }
func (temporaryError) Temporary() bool { return true }
