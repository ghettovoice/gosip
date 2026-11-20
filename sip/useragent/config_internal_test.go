package useragent

import (
	"context"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
)

func TestNewCopiesAndNormalizes(t *testing.T) {
	t.Parallel()

	aor, err := sip.ParseURI("sip:alice@example.com")
	if err != nil {
		t.Fatalf("sip.ParseURI() error = %v, want nil", err)
	}
	contact, err := sip.ParseURI("sip:alice@192.0.2.1")
	if err != nil {
		t.Fatalf("sip.ParseURI() error = %v, want nil", err)
	}
	allow := func(context.Context, *sip.RequestEnvelope, *sip.URI) (bool, error) {
		return true, nil
	}
	policy := &RedirectPolicy{AllowTarget: allow}

	ua, err := New(Options{
		AOR:            aor,
		Contact:        contact,
		RedirectPolicy: policy,
	})
	if err != nil {
		t.Fatalf("useragent.New() error = %v, want nil", err)
	}
	p := ua.opts.RedirectPolicy
	if p == nil || p.MaxDepth != 5 || p.MaxAttempts != 10 || p.Timeout != 32*time.Second {
		t.Fatalf("policy defaults = %+v, want depth 5, attempts 10, timeout 32s", p)
	}
	if ua.opts.MaxAuthAttempts != 3 {
		t.Fatalf("ua.opts.MaxAuthAttempts = %d, want 3", ua.opts.MaxAuthAttempts)
	}
	if ua.opts.AOR == aor || ua.opts.Contact == contact || ua.opts.RedirectPolicy == policy {
		t.Fatal("useragent.New kept a caller-owned pointer")
	}

	// Later caller mutation leaves the stored copies unchanged.
	aor.User = sip.MakeUserInfo("eve")
	contact.Addr = sip.MakeHostAddr("attacker.example.com")
	policy.MaxDepth = 1

	if got := ua.opts.AOR.User.Username(); got != "alice" {
		t.Fatalf("ua.opts.AOR.User.Username() = %q, want %q", got, "alice")
	}
	if got := ua.opts.Contact.Addr.Host(); got != "192.0.2.1" {
		t.Fatalf("ua.opts.Contact.Addr.Host() = %q, want %q", got, "192.0.2.1")
	}
	if ua.opts.RedirectPolicy.MaxDepth != 5 {
		t.Fatalf("ua.opts.RedirectPolicy.MaxDepth = %d, want 5", ua.opts.RedirectPolicy.MaxDepth)
	}
}
