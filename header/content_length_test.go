package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestContentLength_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentLength
		opts *header.RenderOptions
		want string
	}{
		{"zero", header.ContentLength(0), nil, "Content-Length: 0"},
		{"full", header.ContentLength(123), nil, "Content-Length: 123"},
		{"compact", header.ContentLength(123), &header.RenderOptions{Compact: true}, "l: 123"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Render(c.opts); got != c.want {
				t.Errorf("hdr.Render(opts) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestContentLength_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.ContentLength
		wantRes string
		wantErr error
	}{
		{"zero", header.ContentLength(0), "Content-Length: 0", nil},
		{"full", header.ContentLength(123), "Content-Length: 123", nil},
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

func TestContentLength_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentLength
		val  any
		want bool
	}{
		{"zero to nil", header.ContentLength(0), nil, false},
		{"zero to nil ptr", header.ContentLength(0), (*header.ContentLength)(nil), false},
		{"zero to zero", header.ContentLength(0), header.ContentLength(0), true},
		{"not match 1", header.ContentLength(123), header.ContentLength(0), false},
		{"not match 2", header.ContentLength(123), header.ContentLength(456), false},
		{"match", header.ContentLength(123), header.ContentLength(123), true},
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

func TestContentLength_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentLength
		want bool
	}{
		{"zero", header.ContentLength(0), true},
		{"full", header.ContentLength(123), true},
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

func TestContentLength_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentLength
	}{
		{"zero", header.ContentLength(0)},
		{"full", header.ContentLength(123)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.hdr.Clone()
			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}
