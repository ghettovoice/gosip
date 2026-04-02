package errors_test

import (
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
)

func TestWrap(t *testing.T) {
	t.Parallel()

	base := errors.Error("boom")

	err := errors.Wrap(base)
	if err == nil {
		t.Fatal("errors.Wrap() = nil, want error")
	}

	assertErrorIs(t, err, base)
}

func TestWrap2(t *testing.T) {
	t.Parallel()

	t.Run("nil error", func(t *testing.T) {
		t.Parallel()

		const want = 5

		got, err := errors.Wrap2(want, nil)
		if err != nil {
			t.Fatalf("errors.Wrap2() error = %v, want nil", err)
		}

		if got != want {
			t.Fatalf("errors.Wrap2() = %d, want %d", got, want)
		}
	})

	t.Run("with error", func(t *testing.T) {
		t.Parallel()

		const want = 7

		base := errors.Error("boom")

		got, err := errors.Wrap2(want, base)
		if got != want {
			t.Fatalf("errors.Wrap2() = %d, want %d", got, want)
		}

		if err == nil {
			t.Fatal("errors.Wrap2() error = nil, want error")
		}

		assertErrorIs(t, err, base)
	})
}

func TestWrap3(t *testing.T) {
	t.Parallel()

	t.Run("nil error", func(t *testing.T) {
		t.Parallel()

		const (
			want1 = "one"
			want2 = "two"
		)

		got1, got2, err := errors.Wrap3(want1, want2, nil)
		if err != nil {
			t.Fatalf("errors.Wrap3() error = %v, want nil", err)
		}

		if got1 != want1 || got2 != want2 {
			t.Fatalf("errors.Wrap3() = (%q, %q), want (%q, %q)", got1, got2, want1, want2)
		}
	})

	t.Run("with error", func(t *testing.T) {
		t.Parallel()

		const (
			want1 = "one"
			want2 = "two"
		)

		base := errors.Error("boom")

		got1, got2, err := errors.Wrap3(want1, want2, base)
		if got1 != want1 || got2 != want2 {
			t.Fatalf("errors.Wrap3() = (%q, %q), want (%q, %q)", got1, got2, want1, want2)
		}

		if err == nil {
			t.Fatal("errors.Wrap3() error = nil, want error")
		}

		assertErrorIs(t, err, base)
	})
}
