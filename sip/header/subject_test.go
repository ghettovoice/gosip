package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestSubject_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Subject
		want string
	}{
		{"zero", header.Subject(""), "Subject: "},
		{"full", header.Subject("Tech Support"), "Subject: Tech Support"},
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

func TestSubject_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     header.Subject
		wantRes string
		wantErr error
	}{
		{"zero", header.Subject(""), "Subject: ", nil},
		{"full", header.Subject("Tech Support"), "Subject: Tech Support", nil},
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

func TestSubject_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Subject
		val  any
		want bool
	}{
		{"zero to nil", header.Subject(""), nil, false},
		{"zero to nil ptr", header.Subject(""), (*header.Subject)(nil), false},
		{"zero to zero", header.Subject(""), header.Subject(""), true},
		{"not match 1", header.Subject("Tech Support"), header.Subject(""), false},
		{"not match 2", header.Subject("Tech Support"), header.Subject("tech support"), false},
		{"match", header.Subject("Tech Support"), header.Subject("Tech Support"), true},
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

func TestSubject_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Subject
		want bool
	}{
		{"zero", header.Subject(""), true},
		{"valid", header.Subject("Tech Support"), true},
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

func TestSubject_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Subject
	}{
		{"zero", header.Subject("")},
		{"full", header.Subject("Tech Support")},
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
