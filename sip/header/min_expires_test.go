package header_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestMinExpires_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.MinExpires
		want string
	}{
		{"nil", nil, ""},
		{"zero", &header.MinExpires{}, "Min-Expires: 0"},
		{"full", &header.MinExpires{Duration: 123 * time.Second}, "Min-Expires: 123"},
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

func TestMinExpires_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     *header.MinExpires
		wantRes string
		wantErr error
	}{
		{"nil", nil, "", nil},
		{"zero", &header.MinExpires{}, "Min-Expires: 0", nil},
		{"full", &header.MinExpires{Duration: 3600 * time.Second}, "Min-Expires: 3600", nil},
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

func TestMinExpires_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.MinExpires
		val  any
		want bool
	}{
		{"nil to nil", nil, nil, false},
		{"nil to nil ptr", nil, (*header.MinExpires)(nil), true},
		{"zero to nil", &header.MinExpires{}, nil, false},
		{"zero to nil ptr", &header.MinExpires{}, (*header.MinExpires)(nil), false},
		{"zero to zero", &header.MinExpires{}, &header.MinExpires{}, true},
		{"not match 1", &header.MinExpires{Duration: 123 * time.Second}, &header.MinExpires{}, false},
		{"not match 2", &header.MinExpires{Duration: 123 * time.Second}, &header.MinExpires{Duration: 456 * time.Second}, false},
		{"match", &header.MinExpires{Duration: 123 * time.Second}, &header.MinExpires{Duration: 123 * time.Second}, true},
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

func TestMinExpires_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.MinExpires
		want bool
	}{
		{"nil", nil, false},
		{"zero", &header.MinExpires{}, true},
		{"full", &header.MinExpires{Duration: 123 * time.Second}, true},
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

func TestMinExpires_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.MinExpires
	}{
		{"nil", nil},
		{"zero", &header.MinExpires{}},
		{"full", &header.MinExpires{Duration: 123 * time.Second}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.hdr.Clone()
			if tt.hdr == nil {
				if got != nil {
					t.Errorf("hdr.Clone() = %+v, want nil", got)
				}
				return
			}

			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}
