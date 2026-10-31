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

	tests := []struct {
		name string
		uri  *uri.Any
		want string
	}{
		{"nil", (*uri.Any)(nil), ""},
		{"empty", &uri.Any{}, ""},
		{"scheme", &uri.Any{URL: url.URL{Scheme: "qwe"}}, "qwe:"},
		{"path", &uri.Any{Path: "qwe/abc.wav"}, "qwe/abc.wav"},
		{"scheme and host", &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}}, "http://example.com"},
		{"scheme and opaque", &uri.Any{URL: url.URL{Scheme: "ftp", Opaque: "example.com/a/b/c"}}, "ftp:example.com/a/b/c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.uri.Render(); got != tt.want {
				t.Errorf("uri.Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAny_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		uri     *uri.Any
		wantRes string
		wantErr error
	}{
		{"nil", (*uri.Any)(nil), "", nil},
		{"scheme and host", &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}}, "http://example.com", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			_, err := tt.uri.RenderTo(&sb)

			if diff := cmp.Diff(err, tt.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("uri.RenderTo(&sb) error = %v, want %v\ndiff (-got +want):\n%v", err, tt.wantErr, diff)
			}

			if got := sb.String(); got != tt.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, tt.wantRes)
			}
		})
	}
}

func TestAny_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		uri  *uri.Any
		want string
	}{
		{"nil", (*uri.Any)(nil), ""},
		{"empty", &uri.Any{}, ""},
		{"scheme and host", &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}}, "http://example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.uri.String(); got != tt.want {
				t.Errorf("uri.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAny_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
			&uri.Any{URL: url.URL{Scheme: "ftp", Host: "example.com"}},
			"ftp://example.com",
			false,
		},
		{
			"case insensitive",
			&uri.Any{URL: url.URL{Scheme: "http", Host: "example.com", Path: "/A/B/C"}},
			uri.Any{URL: url.URL{Scheme: "HTTP", Host: "EXAMPLE.COM", Path: "/a/b/c"}},
			true,
		},
		{
			"different paths",
			&uri.Any{URL: url.URL{Scheme: "http", Host: "example.com", Path: "/c/b/a"}},
			&uri.Any{URL: url.URL{Scheme: "http", Host: "example.com", Path: "/a/b/c"}},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.uri.Equal(tt.val); got != tt.want {
				t.Errorf("uri.Equal(val) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAny_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		uri  *uri.Any
		want bool
	}{
		{"nil", (*uri.Any)(nil), false},
		{"zero", &uri.Any{}, false},
		{"valid", &uri.Any{URL: url.URL{Scheme: "ftp", Host: "example.com"}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.uri.IsValid(); got != tt.want {
				t.Errorf("uri.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAny_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		uri  *uri.Any
	}{
		{"nil", nil},
		{"zero", &uri.Any{}},
		{
			"valid with user",
			&uri.Any{URL: url.URL{Scheme: "https", User: url.User("root"), Host: "example.com"}},
		},
		{
			"valid with user and password",
			&uri.Any{URL: url.URL{Scheme: "https", User: url.UserPassword("root", "qwe"), Host: "example.com"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.uri.Clone()

			if tt.uri == nil {
				if got != nil {
					t.Errorf("uri.Clone() = %+v, want nil", got)
				}
				return
			}

			if diff := cmp.Diff(got, tt.uri, cmp.AllowUnexported(uri.Any{})); diff != "" {
				t.Errorf("uri.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.uri, diff)
			}
		})
	}
}

func TestAny_RoundTripText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		uri     *uri.Any
		want    *uri.Any
		wantErr bool
	}{
		{"nil", (*uri.Any)(nil), &uri.Any{}, false},
		{"zero", &uri.Any{}, &uri.Any{}, false},
		{
			"opaque",
			&uri.Any{URL: url.URL{Scheme: "mailto", Opaque: "user@example.com"}},
			&uri.Any{URL: url.URL{Scheme: "mailto", Opaque: "user@example.com"}},
			false,
		},
		{
			"full",
			&uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b", RawQuery: "q=1"}},
			&uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b", RawQuery: "q=1"}},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			text, err := tt.uri.MarshalText()
			if err != nil {
				t.Fatalf("uri.MarshalText() error = %v, want nil", err)
			}

			var got uri.Any
			err = got.UnmarshalText(text)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("got.UnmarshalText(text) error = nil, want error")
				}
				return
			}

			if err != nil {
				t.Fatalf("got.UnmarshalText(text) error = %v, want nil", err)
			}

			if diff := cmp.Diff(&got, tt.want, cmp.AllowUnexported(uri.Any{})); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%s", &got, tt.want, diff)
			}
		})
	}
}
