package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestMaxForwards_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.MaxForwards
		want string
	}{
		{"zero", header.MaxForwards(0), "Max-Forwards: 0"},
		{"full", header.MaxForwards(123), "Max-Forwards: 123"},
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

func TestMaxForwards_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     header.MaxForwards
		wantRes string
		wantErr error
	}{
		{"zero", header.MaxForwards(0), "Max-Forwards: 0", nil},
		{"full", header.MaxForwards(123), "Max-Forwards: 123", nil},
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

func TestMaxForwards_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.MaxForwards
		val  any
		want bool
	}{
		{"zero to nil", header.MaxForwards(0), nil, false},
		{"zero to nil ptr", header.MaxForwards(0), (*header.MaxForwards)(nil), false},
		{"zero to zero", header.MaxForwards(0), header.MaxForwards(0), true},
		{"not match 1", header.MaxForwards(123), header.MaxForwards(0), false},
		{"not match 2", header.MaxForwards(123), header.MaxForwards(456), false},
		{"match", header.MaxForwards(123), header.MaxForwards(123), true},
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

func TestMaxForwards_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.MaxForwards
		want bool
	}{
		{"zero", header.MaxForwards(0), true},
		{"full", header.MaxForwards(123), true},
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

func TestMaxForwards_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.MaxForwards
	}{
		{"zero", header.MaxForwards(0)},
		{"full", header.MaxForwards(123)},
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
