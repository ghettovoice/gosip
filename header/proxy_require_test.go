package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
)

func TestProxyRequire_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ProxyRequire
		want string
	}{
		{"nil", nil, ""},
		{"empty", header.ProxyRequire{}, "Proxy-Require: "},
		{"empty elem", header.ProxyRequire{""}, "Proxy-Require: "},
		{"full", header.ProxyRequire{"100rel", "Foo", "Bar"}, "Proxy-Require: 100rel, Foo, Bar"},
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

func TestProxyRequire_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.ProxyRequire
		wantRes string
		wantErr error
	}{
		{"nil", nil, "", nil},
		{"empty", header.ProxyRequire{}, "Proxy-Require: ", nil},
		{"full", header.ProxyRequire{"100rel", "Foo", "Bar"}, "Proxy-Require: 100rel, Foo, Bar", nil},
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

func TestProxyRequire_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ProxyRequire
		want string
	}{
		{"nil", nil, ""},
		{"empty", header.ProxyRequire{}, ""},
		{"full", header.ProxyRequire{"100rel", "Foo", "Bar"}, "100rel, Foo, Bar"},
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

func TestProxyRequire_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ProxyRequire
		val  any
		want bool
	}{
		{"nil ptr to nil", nil, nil, false},
		{"nil ptr to nil ptr", nil, header.ProxyRequire(nil), true},
		{"zero ptr to nil ptr", header.ProxyRequire{}, header.ProxyRequire(nil), true},
		{"zero to zero", header.ProxyRequire{}, header.ProxyRequire{}, true},
		{"zero to zero ptr", header.ProxyRequire{}, &header.ProxyRequire{}, true},
		{"zero to nil ptr", header.ProxyRequire{}, (*header.ProxyRequire)(nil), false},
		{"not match 1", header.ProxyRequire{"100rel"}, header.ProxyRequire{}, false},
		{"not match 2", header.ProxyRequire{"100rel", "foo"}, header.ProxyRequire{"foo", "100rel"}, false},
		{"match", header.ProxyRequire{"100rel", "FOO"}, header.ProxyRequire{"100rel", "foo"}, true},
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

func TestProxyRequire_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ProxyRequire
		want bool
	}{
		{"nil", header.ProxyRequire(nil), false},
		{"empty", header.ProxyRequire{}, false},
		{"valid", header.ProxyRequire{"100rel", "abc"}, true},
		{"invalid", header.ProxyRequire{"a b c"}, false},
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

func TestProxyRequire_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ProxyRequire
	}{
		{"nil", nil},
		{"empty", header.ProxyRequire{}},
		{"full", header.ProxyRequire{"100rel", "foo", "bar"}},
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
