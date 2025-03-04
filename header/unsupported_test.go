package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestUnsupported_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Unsupported
		want string
	}{
		{"nil", header.Unsupported(nil), ""},
		{"empty", header.Unsupported{}, "Unsupported: "},
		{"empty elem", header.Unsupported{""}, "Unsupported: "},
		{"full", header.Unsupported{"100rel", "Foo", "Bar"}, "Unsupported: 100rel, Foo, Bar"},
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

func TestUnsupported_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.Unsupported
		wantRes string
		wantErr error
	}{
		{"nil", header.Unsupported(nil), "", nil},
		{"empty", header.Unsupported{}, "Unsupported: ", nil},
		{"full", header.Unsupported{"100rel", "Foo", "Bar"}, "Unsupported: 100rel, Foo, Bar", nil},
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
				t.Errorf("hdr.RenderTo() wrote %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestUnsupported_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Unsupported
		want string
	}{
		{"nil", header.Unsupported(nil), ""},
		{"empty", header.Unsupported{}, ""},
		{"full", header.Unsupported{"100rel", "Foo", "Bar"}, "100rel, Foo, Bar"},
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

func TestUnsupported_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Unsupported
		val  any
		want bool
	}{
		{"nil ptr to nil", header.Unsupported(nil), nil, false},
		{"nil ptr to nil ptr", header.Unsupported(nil), header.Unsupported(nil), true},
		{"zero ptr to nil ptr", header.Unsupported{}, header.Unsupported(nil), true},
		{"zero to zero", header.Unsupported{}, header.Unsupported{}, true},
		{"zero to zero ptr", header.Unsupported{}, &header.Unsupported{}, true},
		{"zero to nil ptr", header.Unsupported{}, (*header.Unsupported)(nil), false},
		{"not match 1", header.Unsupported{"100rel"}, header.Unsupported{}, false},
		{"not match 2", header.Unsupported{"100rel", "foo"}, header.Unsupported{"foo", "100rel"}, false},
		{"match", header.Unsupported{"100rel", "FOO"}, header.Unsupported{"100rel", "foo"}, true},
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

func TestUnsupported_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Unsupported
		want bool
	}{
		{"nil", header.Unsupported(nil), false},
		{"empty", header.Unsupported{}, false},
		{"valid", header.Unsupported{"100rel", "abc"}, true},
		{"invalid", header.Unsupported{"a b c"}, false},
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

func TestUnsupported_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Unsupported
	}{
		{"nil", header.Unsupported(nil)},
		{"empty", header.Unsupported{}},
		{"full", header.Unsupported{"100rel", "foo", "bar"}},
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
