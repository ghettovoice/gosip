package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestServer_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Server
		want string
	}{
		{"zero", header.Server(""), "Server: "},
		{"full", header.Server("abc/v2 (DEF)"), "Server: abc/v2 (DEF)"},
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

func TestServer_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.Server
		wantRes string
		wantErr error
	}{
		{"zero", header.Server(""), "Server: ", nil},
		{"full", header.Server("abc/v2 (DEF)"), "Server: abc/v2 (DEF)", nil},
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

func TestServer_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Server
		val  any
		want bool
	}{
		{"zero to nil", header.Server(""), nil, false},
		{"zero to nil ptr", header.Server(""), (*header.Server)(nil), false},
		{"zero to zero", header.Server(""), header.Server(""), true},
		{"not match 1", header.Server("abc/v2 (DEF)"), header.Server(""), false},
		{"not match 2", header.Server("abc/v2 (DEF)"), header.Server("abc/v2 (def)"), false},
		{"match", header.Server("abc/v2 (DEF)"), header.Server("abc/v2 (DEF)"), true},
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

func TestServer_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Server
		want bool
	}{
		{"zero", header.Server(""), false},
		{"valid", header.Server("abc/v2 (DEF)"), true},
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

func TestServer_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Server
	}{
		{"zero", header.Server("")},
		{"full", header.Server("abc/v2 (DEF)")},
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
