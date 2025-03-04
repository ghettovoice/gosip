package uri_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/uri"
)

func TestAny_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  *uri.Any
		want string
	}{
		{"nil", (*uri.Any)(nil), ""},
		{"empty", &uri.Any{}, ""},
		{"scheme", &uri.Any{Scheme: "qwe"}, "qwe:"},
		{"path", &uri.Any{Path: "qwe/abc.wav"}, "qwe/abc.wav"},
		{"scheme and host", &uri.Any{Scheme: "http", Host: "example.com"}, "http://example.com"},
		{"scheme and opaque", &uri.Any{Scheme: "ftp", Opaque: "example.com/a/b/c"}, "ftp:example.com/a/b/c"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.uri.Render(nil)
			if got != c.want {
				t.Errorf("uri.Render(nil) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAny_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		uri     *uri.Any
		wantRes string
		wantErr error
	}{
		{"nil", (*uri.Any)(nil), "", nil},
		{"scheme and host", &uri.Any{Scheme: "http", Host: "example.com"}, "http://example.com", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			_, err := c.uri.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("uri.RenderTo(sb, nil) error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}
			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestAny_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  *uri.Any
		want string
	}{
		{"nil", (*uri.Any)(nil), ""},
		{"empty", &uri.Any{}, ""},
		{"scheme and host", &uri.Any{Scheme: "http", Host: "example.com"}, "http://example.com"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.uri.String(); got != c.want {
				t.Errorf("uri.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAny_Equal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		uri  *uri.Any
		val  any
		want bool
	}{
		{"nil ptr to nil", (*uri.Any)(nil), nil, false},
		{"nil ptr to nil ptr", (*uri.Any)(nil), (*uri.Any)(nil), true},
		{"zero ptr to nil ptr", &uri.Any{}, (*uri.Any)(nil), false},
		{"nil ptr to zero ptr", (*uri.Any)(nil), &uri.Any{}, false},
		{"zero ptr to zero ptr", &uri.Any{}, &uri.Any{}, true},
		{"zero ptr to zero val", &uri.Any{}, uri.Any{}, true},
		{
			"type mismatch",
			&uri.Any{Scheme: "ftp", Host: "example.com"},
			"ftp://example.com",
			false,
		},
		{
			"case insensitive",
			&uri.Any{Scheme: "http", Host: "example.com", Path: "/A/B/C"},
			uri.Any{Scheme: "HTTP", Host: "EXAMPLE.COM", Path: "/a/b/c"},
			true,
		},
		{
			"different paths",
			&uri.Any{Scheme: "http", Host: "example.com", Path: "/c/b/a"},
			&uri.Any{Scheme: "http", Host: "example.com", Path: "/a/b/c"},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.uri.Equal(c.val); got != c.want {
				t.Errorf("uri.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAny_IsValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		uri  *uri.Any
		want bool
	}{
		{"nil", (*uri.Any)(nil), false},
		{"zero", &uri.Any{}, false},
		{"valid", &uri.Any{Scheme: "ftp", Host: "example.com"}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.uri.IsValid(); got != c.want {
				t.Errorf("uri.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAny_Clone(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		uri  *uri.Any
	}{
		{"nil", nil},
		{"zero", &uri.Any{}},
		{
			"valid with user",
			&uri.Any{Scheme: "https", User: url.User("root"), Host: "example.com"},
		},
		{
			"valid with user and password",
			&uri.Any{Scheme: "https", User: url.UserPassword("root", "qwe"), Host: "example.com"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.uri.Clone()
			if c.uri == nil {
				if got != nil {
					t.Errorf("uri.Clone() = %+v, want nil", got)
				}
				return
			}
			if diff := cmp.Diff(got, c.uri, cmp.AllowUnexported(uri.Any{})); diff != "" {
				t.Errorf("uri.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.uri, diff)
			}
		})
	}
}

func TestAny_RoundTripText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		uri     *uri.Any
		wantErr bool
	}{
		{"nil", (*uri.Any)(nil), true},
		{"zero", &uri.Any{}, true},
		{"opaque", &uri.Any{Scheme: "mailto", Opaque: "user@example.com"}, false},
		{"full", &uri.Any{Scheme: "https", Host: "example.com", Path: "/a/b", RawQuery: "q=1"}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			text, err := c.uri.MarshalText()
			if err != nil {
				t.Fatalf("uri.MarshalText() error = %v, want nil", err)
			}

			var got uri.Any
			err = got.UnmarshalText(text)
			if c.wantErr {
				if err == nil {
					t.Fatalf("got.UnmarshalText(text) error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("got.UnmarshalText(text) error = %v, want nil", err)
			}

			if diff := cmp.Diff(&got, c.uri, cmp.AllowUnexported(uri.Any{})); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%s", &got, c.uri, diff)
			}
		})
	}
}
