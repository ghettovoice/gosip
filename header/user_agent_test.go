package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestUserAgent_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.UserAgent
		want string
	}{
		{"zero", header.UserAgent(""), "User-Agent: "},
		{"full", header.UserAgent("abc/v2 (DEF)"), "User-Agent: abc/v2 (DEF)"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Render(nil); got != c.want {
				t.Errorf("hdr.Render(nil) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestUserAgent_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.UserAgent
		wantRes string
		wantErr error
	}{
		{"zero", header.UserAgent(""), "User-Agent: ", nil},
		{"full", header.UserAgent("abc/v2 (DEF)"), "User-Agent: abc/v2 (DEF)", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			_, err := c.hdr.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("hdr.RenderTo(sb, nil) error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}
			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestUserAgent_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Equal(c.val); got != c.want {
				t.Errorf("hdr.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestUserAgent_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.UserAgent
		want bool
	}{
		{"zero", header.UserAgent(""), false},
		{"valid", header.UserAgent("abc/v2 (DEF)"), true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.IsValid(); got != c.want {
				t.Errorf("hdr.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestUserAgent_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.UserAgent
	}{
		{"zero", header.UserAgent("")},
		{"full", header.UserAgent("abc/v2 (DEF)")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Clone(); got != c.hdr {
				t.Errorf("hdr.Clone() = %+v, want %+v", got, c.hdr)
			}
		})
	}
}
