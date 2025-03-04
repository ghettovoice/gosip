package header_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestExpires_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Expires
		want string
	}{
		{"nil", nil, ""},
		{"zero", &header.Expires{}, "Expires: 0"},
		{"full", &header.Expires{Duration: 123 * time.Second}, "Expires: 123"},
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

func TestExpires_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.Expires
		wantRes string
		wantErr error
	}{
		{"nil", nil, "", nil},
		{"zero", &header.Expires{}, "Expires: 0", nil},
		{"full", &header.Expires{Duration: 3600 * time.Second}, "Expires: 3600", nil},
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

func TestExpires_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Expires
		val  any
		want bool
	}{
		{"nil to nil", nil, nil, false},
		{"nil to zero", nil, &header.Expires{}, false},
		{"nil to nil ptr", nil, (*header.Expires)(nil), true},
		{"zero to nil", &header.Expires{}, nil, false},
		{"zero to nil ptr", &header.Expires{}, (*header.Expires)(nil), false},
		{"zero to zero", &header.Expires{}, &header.Expires{}, true},
		{"not match 1", &header.Expires{Duration: 123}, &header.Expires{}, false},
		{"not match 2", &header.Expires{Duration: 123}, &header.Expires{Duration: 456}, false},
		{"match", &header.Expires{Duration: 123}, &header.Expires{Duration: 123}, true},
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

func TestExpires_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Expires
		want bool
	}{
		{"nil", nil, false},
		{"zero", &header.Expires{}, true},
		{"full", &header.Expires{Duration: 123 * time.Second}, true},
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

func TestExpires_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Expires
	}{
		{"nil", nil},
		{"zero", &header.Expires{}},
		{"full", &header.Expires{Duration: 123 * time.Second}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.hdr.Clone()
			if c.hdr == nil {
				if got != nil {
					t.Errorf("hdr.Clone() = %+v, want nil", got)
				}
				return
			}
			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}
