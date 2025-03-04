package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestContentEncoding_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentEncoding
		opts *header.RenderOptions
		want string
	}{
		{"nil", header.ContentEncoding(nil), nil, ""},
		{"empty", header.ContentEncoding{}, nil, "Content-Encoding: "},
		{"empty elem", header.ContentEncoding{""}, nil, "Content-Encoding: "},
		{"full", header.ContentEncoding{"qwe", "ZIP", "tar"}, nil, "Content-Encoding: qwe, ZIP, tar"},
		{"compact", header.ContentEncoding{"qwe", "ZIP", "tar"}, &header.RenderOptions{Compact: true}, "e: qwe, ZIP, tar"},
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

func TestContentEncoding_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.ContentEncoding
		wantRes string
		wantErr error
	}{
		{"nil", header.ContentEncoding(nil), "", nil},
		{"empty", header.ContentEncoding{}, "Content-Encoding: ", nil},
		{"full", header.ContentEncoding{"qwe", "ZIP", "tar"}, "Content-Encoding: qwe, ZIP, tar", nil},
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

func TestContentEncoding_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentEncoding
		want string
	}{
		{"nil", header.ContentEncoding(nil), ""},
		{"empty", header.ContentEncoding{}, ""},
		{"full", header.ContentEncoding{"qwe", "ZIP", "tar"}, "qwe, ZIP, tar"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.String(); got != c.want {
				t.Errorf("hdr.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestContentEncoding_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentEncoding
		val  any
		want bool
	}{
		{"nil ptr to nil", header.ContentEncoding(nil), nil, false},
		{"nil ptr to nil ptr", header.ContentEncoding(nil), header.ContentEncoding(nil), true},
		{"zero ptr to nil ptr", header.ContentEncoding{}, header.ContentEncoding(nil), true},
		{"zero to zero", header.ContentEncoding{}, header.ContentEncoding{}, true},
		{"zero to zero ptr", header.ContentEncoding{}, &header.ContentEncoding{}, true},
		{"zero to nil ptr", header.ContentEncoding{}, (*header.ContentEncoding)(nil), false},
		{"not match 1", header.ContentEncoding{"qwe"}, header.ContentEncoding{}, false},
		{"not match 2", header.ContentEncoding{"qwe", "ZIP"}, header.ContentEncoding{"ZIP", "qwe"}, false},
		{"match", header.ContentEncoding{"qwe", "ZIP"}, header.ContentEncoding{"QWE", "zip"}, true},
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

func TestContentEncoding_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentEncoding
		want bool
	}{
		{"nil", header.ContentEncoding(nil), false},
		{"empty", header.ContentEncoding{}, false},
		{"valid", header.ContentEncoding{"gzip", "QWE"}, true},
		{"invalid", header.ContentEncoding{"t a r"}, false},
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

func TestContentEncoding_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ContentEncoding
	}{
		{"nil", nil},
		{"empty", header.ContentEncoding{}},
		{"full", header.ContentEncoding{"gzip", "QWE"}},
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
