package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestOrganization_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Organization
		want string
	}{
		{"zero", header.Organization(""), "Organization: "},
		{"full", header.Organization("Boxes by Bob"), "Organization: Boxes by Bob"},
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

func TestOrganization_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     header.Organization
		wantRes string
		wantErr error
	}{
		{"zero", header.Organization(""), "Organization: ", nil},
		{"full", header.Organization("Boxes by Bob"), "Organization: Boxes by Bob", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder

			_, err := tt.hdr.RenderTo(&sb)
			if diff := cmp.Diff(err, tt.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("hdr.RenderTo(&sb) error = %v, want %q\ndiff (-got +want):\n%v", err, tt.wantErr, diff)
			}

			if got := sb.String(); got != tt.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, tt.wantRes)
			}
		})
	}
}

func TestOrganization_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.Equal(tt.val); got != tt.want {
				t.Errorf("hdr.Equal(val) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOrganization_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Organization
		want bool
	}{
		{"zero", header.Organization(""), true},
		{"valid", header.Organization("Boxes by Bob"), true},
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

func TestOrganization_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Organization
	}{
		{"zero", header.Organization("")},
		{"full", header.Organization("Boxes by Bob")},
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
