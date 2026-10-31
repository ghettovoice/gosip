package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestPriority_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Priority
		want string
	}{
		{"zero", header.Priority(""), "Priority: "},
		{"full", header.Priority("non-urgent"), "Priority: non-urgent"},
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

func TestPriority_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     header.Priority
		wantRes string
		wantErr error
	}{
		{"zero", header.Priority(""), "Priority: ", nil},
		{"full", header.Priority("non-urgent"), "Priority: non-urgent", nil},
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

func TestPriority_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Priority
		val  any
		want bool
	}{
		{"zero to nil", header.Priority(""), nil, false},
		{"zero to nil ptr", header.Priority(""), (*header.Priority)(nil), false},
		{"zero to zero", header.Priority(""), header.Priority(""), true},
		{"not match 1", header.Priority("non-urgent"), header.Priority(""), false},
		{"not match 2", header.Priority("non-urgent"), header.Priority("aaa-bbb-ccc"), false},
		{"match", header.Priority("non-urgent"), header.Priority("NON-URGENT"), true},
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

func TestPriority_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Priority
		want bool
	}{
		{"zero", header.Priority(""), false},
		{"valid", header.Priority("non-urgent"), true},
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

func TestPriority_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Priority
	}{
		{"zero", header.Priority("")},
		{"full", header.Priority("non-urgent")},
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
