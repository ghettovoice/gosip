package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestUserAgent_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.UserAgent
		want string
	}{
		{"zero", header.UserAgent(""), "User-Agent: "},
		{"full", header.UserAgent("abc/v2 (DEF)"), "User-Agent: abc/v2 (DEF)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.Render(); got != tt.want {
				t.Errorf("hdr.Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUserAgent_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     header.UserAgent
		wantRes string
		wantErr error
	}{
		{"zero", header.UserAgent(""), "User-Agent: ", nil},
		{"full", header.UserAgent("abc/v2 (DEF)"), "User-Agent: abc/v2 (DEF)", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder

			_, err := tt.hdr.RenderTo(&sb)
			if diff := cmp.Diff(err, tt.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("hdr.RenderTo(&sb) error = %v, want %v\ndiff (-got +want):\n%v", err, tt.wantErr, diff)
			}

			if got := sb.String(); got != tt.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, tt.wantRes)
			}
		})
	}
}

func TestUserAgent_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.UserAgent
		val  any
		want bool
	}{
		{"zero to nil", header.UserAgent(""), nil, false},
		{"zero to nil ptr", header.UserAgent(""), (*header.UserAgent)(nil), false},
		{"zero to zero", header.UserAgent(""), header.UserAgent(""), true},
		{"not match 1", header.UserAgent("abc/v2 (DEF)"), header.UserAgent(""), false},
		{"not match 2", header.UserAgent("abc/v2 (DEF)"), header.UserAgent("abc/v2 (def)"), false},
		{"match", header.UserAgent("abc/v2 (DEF)"), header.UserAgent("abc/v2 (DEF)"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.Equal(tt.val); got != tt.want {
				t.Errorf("hdr.Equal(val) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUserAgent_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.UserAgent
		want bool
	}{
		{"zero", header.UserAgent(""), false},
		{"valid", header.UserAgent("abc/v2 (DEF)"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.IsValid(); got != tt.want {
				t.Errorf("hdr.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUserAgent_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.UserAgent
	}{
		{"zero", header.UserAgent("")},
		{"full", header.UserAgent("abc/v2 (DEF)")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.Clone(); got != tt.hdr {
				t.Errorf("hdr.Clone() = %+v, want %+v", got, tt.hdr)
			}
		})
	}
}
