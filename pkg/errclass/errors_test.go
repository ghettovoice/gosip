package errclass_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"testing"

	"github.com/ghettovoice/gosip/pkg/errclass"
)

var errTest = errors.New("qwerty")

type behavioralError struct {
	closed    bool
	timeout   bool
	temporary bool
	canceled  bool
	grammar   bool
}

func (behavioralError) Error() string     { return "behavioral error" }
func (e behavioralError) Closed() bool    { return e.closed }
func (e behavioralError) Timeout() bool   { return e.timeout }
func (e behavioralError) Temporary() bool { return e.temporary }
func (e behavioralError) Canceled() bool  { return e.canceled }
func (e behavioralError) Grammar() bool   { return e.grammar }

func TestClass_Error(t *testing.T) {
	t.Parallel()

	if got := errclass.ClassTimeout.Error(); got != "timeout" {
		t.Fatalf("ClassTimeout.Error() = %q, want %q", got, "timeout")
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	if err := errclass.Classify(nil, errclass.ClassNotFound); err != nil {
		t.Fatalf("Classify(nil) = %v, want nil", err)
	}

	err := errclass.Classify(errTest, errclass.ClassNotFound, errclass.ClassConflict, errclass.ClassTimeout)
	if err.Error() != errTest.Error() {
		t.Fatalf("Classify().Error() = %q, want %q", err.Error(), errTest.Error())
	}
	if !errors.Is(err, errTest) {
		t.Fatalf("errors.Is(Classify(), errTest) = false, want true")
	}

	for _, class := range []errclass.Class{
		errclass.ClassNotFound,
		errclass.ClassConflict,
		errclass.ClassTimeout,
	} {
		if !errors.Is(err, class) {
			t.Errorf("errors.Is(Classify(), %q) = false, want true", class)
		}
	}
	if errors.Is(err, errclass.ClassClosed) {
		t.Errorf("errors.Is(Classify(), ClassClosed) = true, want false")
	}

	classified, ok := errors.AsType[*errclass.Error](err)
	if !ok {
		t.Fatalf("errors.AsType[*errclass.Error](Classify()) ok = false, want true")
	}
	if !classified.Timeout() {
		t.Errorf("Classify().Timeout() = false, want true")
	}
	if classified.Closed() || classified.Temporary() || classified.Canceled() {
		t.Errorf("Classify() reported an unexpected behavioral class")
	}

	classes := classified.Classes()
	if len(classes) != 3 {
		t.Fatalf("len(Classify().Classes()) = %d, want 3", len(classes))
	}
}

func TestClassify_Nested(t *testing.T) {
	t.Parallel()

	err := errclass.Classify(errTest, errclass.ClassNotFound)
	err = fmt.Errorf("context: %w", err)
	err = errclass.Classify(err, errclass.ClassConflict)

	if !errors.Is(err, errTest) {
		t.Fatalf("errors.Is(err, errTest) = false, want true")
	}
	if !errclass.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, want true")
	}
	if !errclass.IsConflict(err) {
		t.Fatalf("IsConflict(err) = false, want true")
	}
}

func TestError_NilReceiver(t *testing.T) {
	t.Parallel()

	var err *errclass.Error
	if got := err.Error(); got != "<nil>" {
		t.Fatalf("(*Error)(nil).Error() = %q, want %q", got, "<nil>")
	}
	if err.Unwrap() != nil {
		t.Fatalf("(*Error)(nil).Unwrap() = %v, want nil", err.Unwrap())
	}
	if err.Classes() != nil {
		t.Fatalf("(*Error)(nil).Classes() = %v, want nil", err.Classes())
	}
	if err.Is(errclass.ClassClosed) || err.Closed() || err.Timeout() || err.Temporary() || err.Canceled() {
		t.Fatalf("nil *Error reported a classification")
	}
}

func TestBehavioralHelpers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		is   func(error) bool
	}{
		{"temporary class", errclass.Classify(errTest, errclass.ClassTemporary), errclass.IsTemporary},
		{"temporary interface", behavioralError{temporary: true}, errclass.IsTemporary},
		{"timeout class", errclass.Classify(errTest, errclass.ClassTimeout), errclass.IsTimeout},
		{"timeout interface", behavioralError{timeout: true}, errclass.IsTimeout},
		{"closed class", errclass.Classify(errTest, errclass.ClassClosed), errclass.IsClosed},
		{"closed interface", behavioralError{closed: true}, errclass.IsClosed},
		{"canceled class", errclass.Classify(errTest, errclass.ClassCanceled), errclass.IsCanceled},
		{"canceled interface", behavioralError{canceled: true}, errclass.IsCanceled},
		{"grammar interface", behavioralError{grammar: true}, errclass.IsGrammar},
		{"not found class", errclass.Classify(errTest, errclass.ClassNotFound), errclass.IsNotFound},
		{"conflict class", errclass.Classify(errTest, errclass.ClassConflict), errclass.IsConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := errors.Join(behavioralError{}, fmt.Errorf("wrapped: %w", tt.err))
			if !tt.is(err) {
				t.Fatalf("helper(%v) = false, want true", err)
			}
		})
	}
}

func TestBehavioralHelpers_False(t *testing.T) {
	t.Parallel()

	for name, is := range map[string]func(error) bool{
		"temporary": errclass.IsTemporary,
		"timeout":   errclass.IsTimeout,
		"closed":    errclass.IsClosed,
		"canceled":  errclass.IsCanceled,
		"grammar":   errclass.IsGrammar,
		"not found": errclass.IsNotFound,
		"conflict":  errclass.IsConflict,
		"deadline":  errclass.IsDeadline,
		"network":   errclass.IsNetwork,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if is(nil) {
				t.Fatalf("helper(nil) = true, want false")
			}
			if is(behavioralError{}) {
				t.Fatalf("helper(unclassified error) = true, want false")
			}
		})
	}
}

func TestIsClosed_StandardErrors(t *testing.T) {
	t.Parallel()

	for _, err := range []error{net.ErrClosed, io.ErrClosedPipe, io.EOF, os.ErrClosed} {
		if !errclass.IsClosed(fmt.Errorf("wrapped: %w", err)) {
			t.Errorf("IsClosed(%v) = false, want true", err)
		}
	}
}

func TestIsCanceled(t *testing.T) {
	t.Parallel()

	if !errclass.IsCanceled(fmt.Errorf("wrapped: %w", context.Canceled)) {
		t.Fatalf("IsCanceled(context.Canceled) = false, want true")
	}
}

func TestIsDeadline(t *testing.T) {
	t.Parallel()

	for _, err := range []error{context.DeadlineExceeded, os.ErrDeadlineExceeded} {
		if !errclass.IsDeadline(fmt.Errorf("wrapped: %w", err)) {
			t.Errorf("IsDeadline(%v) = false, want true", err)
		}
	}
}

func TestIsNetwork(t *testing.T) {
	t.Parallel()

	err := &net.OpError{Op: "dial", Net: "udp", Err: errTest}
	if !errclass.IsNetwork(fmt.Errorf("wrapped: %w", err)) {
		t.Fatalf("IsNetwork(*net.OpError) = false, want true")
	}
}
