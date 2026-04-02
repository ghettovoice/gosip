package errors_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
)

type temporaryError struct {
	temporary bool
}

func (temporaryError) Error() string     { return "temporary" }
func (e temporaryError) Temporary() bool { return e.temporary }

type timeoutError struct {
	timeout bool
}

func (timeoutError) Error() string   { return "timeout" }
func (e timeoutError) Timeout() bool { return e.timeout }

type grammarError struct {
	grammar bool
}

func (grammarError) Error() string   { return "grammar" }
func (e grammarError) Grammar() bool { return e.grammar }

type unwrapAll interface {
	Unwrap() []error
}

func assertErrorContains(tb testing.TB, err error, want string) {
	tb.Helper()

	if err == nil {
		tb.Fatal("expected non-nil error")
	}

	if !strings.Contains(err.Error(), want) {
		tb.Fatalf("expected error text %q contains %q", err.Error(), want)
	}
}

func assertErrorIs(tb testing.TB, err, target error) {
	tb.Helper()

	if !errors.Is(err, target) {
		tb.Fatalf("expected error %v to match target %v", err, target)
	}
}

func unwrapErrors(tb testing.TB, err error) []error {
	tb.Helper()

	var u unwrapAll
	if !errors.As(err, &u) {
		tb.Fatalf("expected error %v to support Unwrap() []error", err)
	}

	return u.Unwrap()
}

func TestSentinel_Error(t *testing.T) {
	t.Parallel()

	const want = "boom"

	got := errors.Error(want).Error()
	if got != want {
		t.Errorf("err.Error() = %q, want %q", got, want)
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	const msg = "boom"

	err := errors.New(msg)
	if err == nil {
		t.Fatal("errors.New() = nil, want error")
	}

	assertErrorContains(t, err, msg)
	assertErrorIs(t, err, errors.Error(msg))
}

func TestErrorf(t *testing.T) {
	t.Parallel()

	err := errors.Errorf("value %d", 3)
	if err == nil {
		t.Fatal("errors.Errorf() = nil, want error")
	}

	assertErrorContains(t, err, "value 3")
}

func TestPrefix(t *testing.T) {
	t.Parallel()

	sentinel := errors.Error("sentinel")
	base := errors.New("base")
	prefixed := fmt.Errorf("%w: detail", sentinel)

	tests := []struct {
		name         string
		args         []any
		wantMessage  string
		wantIsBase   bool
		wantIsPrefix bool
	}{
		{
			name:         "no args",
			wantMessage:  "sentinel",
			wantIsPrefix: true,
		},
		{
			name:         "error arg",
			args:         []any{base},
			wantMessage:  "sentinel: base",
			wantIsBase:   true,
			wantIsPrefix: true,
		},
		{
			name:         "error arg already prefixed",
			args:         []any{prefixed},
			wantMessage:  "sentinel: detail",
			wantIsPrefix: true,
		},
		{
			name:         "string arg",
			args:         []any{"detail"},
			wantMessage:  "sentinel: detail",
			wantIsPrefix: true,
		},
		{
			name:         "format args",
			args:         []any{"detail %d", 2},
			wantMessage:  "sentinel: detail 2",
			wantIsPrefix: true,
		},
		{
			name:         "default arg",
			args:         []any{123},
			wantMessage:  "sentinel",
			wantIsPrefix: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := errors.Prefix(sentinel, tt.args...)
			if err == nil {
				t.Fatal("errors.Prefix() = nil, want error")
			}

			assertErrorContains(t, err, tt.wantMessage)

			if tt.wantIsPrefix {
				assertErrorIs(t, err, sentinel)
			}

			if tt.wantIsBase {
				assertErrorIs(t, err, base)
			} else if errors.Is(err, base) {
				t.Fatalf("errors.Prefix() unexpectedly matched base error")
			}
		})
	}
}
