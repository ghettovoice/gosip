package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestMIMEVersion_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.MIMEVersion
		want string
	}{
		{"zero", header.MIMEVersion(""), "MIME-Version: "},
		{"full", header.MIMEVersion("1.5"), "MIME-Version: 1.5"},
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

func TestMIMEVersion_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.MIMEVersion
		wantRes string
		wantErr error
	}{
		{"zero", header.MIMEVersion(""), "MIME-Version: ", nil},
		{"full", header.MIMEVersion("1.5"), "MIME-Version: 1.5", nil},
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

func TestMIMEVersion_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.MIMEVersion
		val  any
		want bool
	}{
		{"zero to nil", header.MIMEVersion(""), nil, false},
		{"zero to nil ptr", header.MIMEVersion(""), (*header.MIMEVersion)(nil), false},
		{"zero to zero", header.MIMEVersion(""), header.MIMEVersion(""), true},
		{"not match 1", header.MIMEVersion("1.5"), header.MIMEVersion(""), false},
		{"not match 2", header.MIMEVersion("1.5"), header.MIMEVersion("2.0"), false},
		{"match", header.MIMEVersion("1.5"), header.MIMEVersion("1.5"), true},
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

func TestMIMEVersion_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.MIMEVersion
		want bool
	}{
		{"zero", header.MIMEVersion(""), false},
		{"invalid", header.MIMEVersion("1.5 abc"), false},
		{"valid", header.MIMEVersion("1.5"), true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.hdr.IsValid(); got != c.want {
				t.Errorf("hdr.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestMIMEVersion_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.MIMEVersion
	}{
		{"zero", header.MIMEVersion("")},
		{"full", header.MIMEVersion("1.5")},
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
