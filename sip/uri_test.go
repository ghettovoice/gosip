package sip_test

import (
	"testing"

	"github.com/ghettovoice/gosip/sip"
)

func TestCleanRequestURI(t *testing.T) {
	t.Parallel()

	u, err := sip.ParseURI("sip:alice@example.com;transport=tcp;method=INVITE?Subject=test&Priority=urgent")
	if err != nil {
		t.Fatalf("sip.ParseURI() error = %v, want nil", err)
	}

	// Snapshot the input before cleaning: the helper must never mutate it.
	before := u.String()

	got := sip.CleanRequestURI(u)
	if got == nil {
		t.Fatal("sip.CleanRequestURI(u) = nil, want non-nil")
	}
	if got == u {
		t.Fatal("sip.CleanRequestURI(u) returned the input URI, want a clone")
	}

	want := "sip:alice@example.com;transport=tcp"
	if got.String() != want {
		t.Errorf("got.String() = %q, want %q", got.String(), want)
	}
	if got.Params.Has("method") {
		t.Error("got.Params.Has(\"method\") = true, want false")
	}
	if len(got.Headers) != 0 {
		t.Errorf("len(got.Headers) = %d, want 0", len(got.Headers))
	}

	// The input URI must stay unchanged: the helper always clones.
	if u.String() != before {
		t.Errorf("u.String() = %q, want %q (input mutated)", u.String(), before)
	}
}
