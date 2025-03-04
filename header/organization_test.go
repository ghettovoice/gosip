package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestOrganization_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Organization
		want string
	}{
		{"zero", header.Organization(""), "Organization: "},
		{"full", header.Organization("Boxes by Bob"), "Organization: Boxes by Bob"},
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

func TestOrganization_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.Organization
		wantRes string
		wantErr error
	}{
		{"zero", header.Organization(""), "Organization: ", nil},
		{"full", header.Organization("Boxes by Bob"), "Organization: Boxes by Bob", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			_, err := c.hdr.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("hdr.RenderTo(sb, nil) error = %v, want %q\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}
			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestOrganization_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Organization
		val  any
		want bool
	}{
		{"zero to nil", header.Organization(""), nil, false},
		{"zero to nil ptr", header.Organization(""), (*header.Organization)(nil), false},
		{"zero to zero", header.Organization(""), header.Organization(""), true},
		{"not match 1", header.Organization("Boxes by Bob"), header.Organization(""), false},
		{"not match 2", header.Organization("Boxes by Bob"), header.Organization("BOXES By Bob"), false},
		{"match", header.Organization("Boxes by Bob"), header.Organization("Boxes by Bob"), true},
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

func TestOrganization_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Organization
		want bool
	}{
		{"zero", header.Organization(""), true},
		{"valid", header.Organization("Boxes by Bob"), true},
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

func TestOrganization_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Organization
	}{
		{"zero", header.Organization("")},
		{"full", header.Organization("Boxes by Bob")},
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
