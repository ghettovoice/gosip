package errclass_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ghettovoice/gosip/pkg/errclass"
)

var errTest = errors.New("qwerty")

func TestClass_Error(t *testing.T) {
	t.Parallel()

	if got := errclass.ClassTimeout.Error(); got != "timeout" {
		t.Fatalf("errclass.ClassTimeout.Error() = %q, want %q", got, "timeout")
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	if err := errclass.Classify(nil, errclass.ClassNotFound); err != nil {
		t.Fatalf("errclass.Classify(nil) = %v, want nil", err)
	}

	err := errclass.Classify(errTest, errclass.ClassNotFound, errclass.ClassConflict, errclass.ClassTimeout)
	if err.Error() != errTest.Error() {
		t.Fatalf("errclass.Classify().Error() = %q, want %q", err.Error(), errTest.Error())
	}
	if !errors.Is(err, errTest) {
		t.Fatalf("errors.Is(errclass.Classify(), errTest) = false, want true")
	}

	for _, class := range []errclass.Class{
		errclass.ClassNotFound,
		errclass.ClassConflict,
		errclass.ClassTimeout,
	} {
		if !errors.Is(err, class) {
			t.Errorf("errors.Is(errclass.Classify(), %q) = false, want true", class)
		}
	}
	if errors.Is(err, errclass.ClassClosed) {
		t.Errorf("errors.Is(errclass.Classify(), errclass.ClassClosed) = true, want false")
	}

	classified, ok := errors.AsType[*errclass.Error](err)
	if !ok {
		t.Fatalf("errors.AsType[*errclass.Error](errclass.Classify()) ok = false, want true")
	}
	if !classified.Timeout() {
		t.Errorf("errclass.Classify().Timeout() = false, want true")
	}
	if classified.Closed() || classified.Temporary() || classified.Canceled() {
		t.Errorf("errclass.Classify() reported an unexpected behavioral class")
	}

	classes := classified.Classes()
	if len(classes) != 3 {
		t.Fatalf("len(errclass.Classify().Classes()) = %d, want 3", len(classes))
	}
}

func TestClassify_Nested(t *testing.T) {
	t.Parallel()

	err := errclass.Classify(errTest, errclass.ClassNotFound)
	err = fmt.Errorf("context: %w", err)
	err = errclass.Classify(err, errclass.ClassConflict)

	if !errors.Is(err, errTest) {
		t.Fatalf("errors.Is(errclass.Classify(), errTest) = false, want true")
	}
	if !errclass.IsNotFound(err) {
		t.Fatalf("errclass.IsNotFound(errclass.Classify()) = false, want true")
	}
	if !errclass.IsConflict(err) {
		t.Fatalf("errclass.IsConflict(errclass.Classify()) = false, want true")
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
