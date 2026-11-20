package useragent_test

import (
	"context"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/useragent"
)

func TestNew(t *testing.T) {
	t.Parallel()

	aor, err := sip.ParseURI("sip:alice@example.com")
	if err != nil {
		t.Fatalf("sip.ParseURI() error = %v, want nil", err)
	}
	allowTarget := func(context.Context, *sip.RequestEnvelope, *sip.URI) (bool, error) {
		return true, nil
	}
	var nilContact *sip.URI

	tests := []struct {
		name    string
		opts    []useragent.Options
		wantErr bool
	}{
		{
			name: "valid address of record",
			opts: []useragent.Options{{AOR: aor}},
		},
		{
			name:    "no options",
			opts:    nil,
			wantErr: true,
		},
		{
			name:    "nil address of record",
			opts:    []useragent.Options{{}},
			wantErr: true,
		},
		{
			name:    "invalid address of record",
			opts:    []useragent.Options{{AOR: &sip.URI{}}},
			wantErr: true,
		},
		{
			name: "invalid contact",
			opts: []useragent.Options{{
				AOR:     aor,
				Contact: &sip.URI{},
			}},
			wantErr: true,
		},
		{
			name: "negative max auth attempts",
			opts: []useragent.Options{{
				AOR:             aor,
				MaxAuthAttempts: -1,
			}},
			wantErr: true,
		},
		{
			name: "redirect policy without target check",
			opts: []useragent.Options{{
				AOR:            aor,
				RedirectPolicy: &useragent.RedirectPolicy{},
			}},
			wantErr: true,
		},
		{
			name: "redirect policy negative max depth",
			opts: []useragent.Options{{
				AOR:            aor,
				RedirectPolicy: &useragent.RedirectPolicy{AllowTarget: allowTarget, MaxDepth: -1},
			}},
			wantErr: true,
		},
		{
			name: "redirect policy negative max attempts",
			opts: []useragent.Options{{
				AOR:            aor,
				RedirectPolicy: &useragent.RedirectPolicy{AllowTarget: allowTarget, MaxAttempts: -1},
			}},
			wantErr: true,
		},
		{
			name: "redirect policy negative timeout",
			opts: []useragent.Options{{
				AOR:            aor,
				RedirectPolicy: &useragent.RedirectPolicy{AllowTarget: allowTarget, Timeout: -time.Second},
			}},
			wantErr: true,
		},
		{
			name: "typed nil contact is unset",
			opts: []useragent.Options{{
				AOR:     aor,
				Contact: nilContact,
			}},
		},
		{
			name:    "trailing empty options lose address of record",
			opts:    []useragent.Options{{AOR: aor}, {}},
			wantErr: true,
		},
		{
			name: "trailing options with address of record",
			opts: []useragent.Options{{}, {AOR: aor}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ua, err := useragent.New(tt.opts...)
			if tt.wantErr {
				if err == nil {
					t.Fatal("useragent.New() error = nil, want error")
				}
				if ua != nil {
					t.Fatalf("useragent.New() = %v, want nil user agent on error", ua)
				}
				if !errors.Is(err, useragent.ErrInvalidConfig) {
					t.Fatalf("useragent.New() error = %v, want %v", err, useragent.ErrInvalidConfig)
				}
				if !useragent.IsError(err) {
					t.Fatalf("useragent.New() error = %v, want a useragent-classified error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("useragent.New() error = %v, want nil", err)
			}
			if ua == nil {
				t.Fatal("useragent.New() = nil, want non-nil user agent")
			}
		})
	}

	// New binds nothing: a fresh element still advertises no methods.
	ua, err := useragent.New(useragent.Options{AOR: aor})
	if err != nil {
		t.Fatalf("useragent.New() error = %v, want nil", err)
	}
	if ua == nil {
		t.Fatal("useragent.New() = nil, want non-nil user agent")
	}
	elm := &sip.Element{}
	if caps := elm.Capabilities(); len(caps.Methods) != 0 {
		t.Fatalf("elm.Capabilities().Methods = %v, want empty after New", caps.Methods)
	}
}
