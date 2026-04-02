package errors_test

import (
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
)

func TestJoin(t *testing.T) {
	t.Parallel()

	t.Run("no errors", func(t *testing.T) {
		t.Parallel()

		err := errors.Join()
		if err != nil {
			t.Fatalf("errors.Join() = %v, want nil", err)
		}
	})

	t.Run("single error", func(t *testing.T) {
		t.Parallel()

		base := errors.New("boom")

		err := errors.Join(base)
		if err == nil {
			t.Fatal("errors.Join() = nil, want error")
		}

		assertErrorIs(t, err, base)
		assertErrorContains(t, err, "boom")
	})

	t.Run("multiple errors", func(t *testing.T) {
		t.Parallel()

		err1 := errors.New("one")
		err2 := errors.New("two")

		err := errors.Join(err1, err2)
		if err == nil {
			t.Fatal("errors.Join() = nil, want error")
		}

		assertErrorIs(t, err, err1)
		assertErrorIs(t, err, err2)

		const want = "\n  - one\n  - two"
		if err.Error() != want {
			t.Fatalf("errors.Join() = %q, want %q", err.Error(), want)
		}

		unwrapped := unwrapErrors(t, err)
		if len(unwrapped) != 2 {
			t.Fatalf("errors.Join() unwrap length = %d, want 2", len(unwrapped))
		}

		if !errors.Is(unwrapped[0], err1) || !errors.Is(unwrapped[1], err2) {
			t.Fatalf("errors.Join() unwrap = %#v, want [%v %v]", unwrapped, err1, err2)
		}
	})

	t.Run("multiline error", func(t *testing.T) {
		t.Parallel()

		err1 := errors.New("line1\nline2")
		err2 := errors.New("two")

		err := errors.Join(err1, err2)
		if err == nil {
			t.Fatal("errors.Join() = nil, want error")
		}

		const want = "\n  - line1\n    line2\n  - two"
		if err.Error() != want {
			t.Fatalf("errors.Join() = %q, want %q", err.Error(), want)
		}
	})
}

func TestJoinPrefix(t *testing.T) {
	t.Parallel()

	t.Run("no errors", func(t *testing.T) {
		t.Parallel()

		err := errors.JoinPrefix("root")
		if err != nil {
			t.Fatalf("errors.JoinPrefix() = %v, want nil", err)
		}
	})

	t.Run("single error", func(t *testing.T) {
		t.Parallel()

		base := errors.New("boom")

		err := errors.JoinPrefix("root:", base)
		if err == nil {
			t.Fatal("errors.JoinPrefix() = nil, want error")
		}

		assertErrorIs(t, err, base)
		assertErrorContains(t, err, "root: boom")
	})

	t.Run("multiple errors", func(t *testing.T) {
		t.Parallel()

		err1 := errors.New("one")
		err2 := errors.New("two")

		err := errors.JoinPrefix("root:", err1, err2)
		if err == nil {
			t.Fatal("errors.JoinPrefix() = nil, want error")
		}

		assertErrorIs(t, err, err1)
		assertErrorIs(t, err, err2)

		const want = "root:\n  - one\n  - two"
		if err.Error() != want {
			t.Fatalf("errors.JoinPrefix() = %q, want %q", err.Error(), want)
		}

		unwrapped := unwrapErrors(t, err)
		if len(unwrapped) != 2 {
			t.Fatalf("errors.JoinPrefix() unwrap length = %d, want 2", len(unwrapped))
		}

		if !errors.Is(unwrapped[0], err1) || !errors.Is(unwrapped[1], err2) {
			t.Fatalf("errors.JoinPrefix() unwrap = %#v, want [%v %v]", unwrapped, err1, err2)
		}
	})
}
