package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestSupported_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Supported
		want string
	}{
		{"nil", nil, ""},
		{"empty", header.Supported{}, "Supported: "},
		{"empty elem", header.Supported{""}, "Supported: "},
		{"full", header.Supported{"100rel", "Foo", "Bar"}, "Supported: 100rel, Foo, Bar"},
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

func TestSupported_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.Supported
		wantRes string
		wantErr error
	}{
		{"nil", nil, "", nil},
		{"empty", header.Supported{}, "Supported: ", nil},
		{"full", header.Supported{"100rel", "Foo", "Bar"}, "Supported: 100rel, Foo, Bar", nil},
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

func TestSupported_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Supported
		want string
	}{
		{"nil", nil, ""},
		{"empty", header.Supported{}, ""},
		{"full", header.Supported{"100rel", "Foo", "Bar"}, "100rel, Foo, Bar"},
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

func TestSupported_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Supported
		val  any
		want bool
	}{
		{"nil ptr to nil", nil, nil, false},
		{"nil ptr to nil ptr", nil, header.Supported(nil), true},
		{"zero ptr to nil ptr", header.Supported{}, header.Supported(nil), true},
		{"zero to zero", header.Supported{}, header.Supported{}, true},
		{"zero to zero ptr", header.Supported{}, &header.Supported{}, true},
		{"zero to nil ptr", header.Supported{}, (*header.Supported)(nil), false},
		{"not match 1", header.Supported{"100rel"}, header.Supported{}, false},
		{"not match 2", header.Supported{"100rel", "foo"}, header.Supported{"foo", "100rel"}, false},
		{"match", header.Supported{"100rel", "FOO"}, header.Supported{"100rel", "foo"}, true},
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

func TestSupported_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Supported
		want bool
	}{
		{"nil", nil, false},
		{"empty", header.Supported{}, true},
		{"valid", header.Supported{"100rel", "abc"}, true},
		{"invalid", header.Supported{"a b c"}, false},
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

func TestSupported_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Supported
	}{
		{"nil", nil},
		{"empty", header.Supported{}},
		{"full", header.Supported{"100rel", "foo", "bar"}},
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
