package header_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestErrorInfo_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ErrorInfo
		want string
	}{
		{"nil", header.ErrorInfo(nil), ""},
		{"empty", header.ErrorInfo{}, "Error-Info: "},
		{"empty elem", header.ErrorInfo{{}}, "Error-Info: <>"},
		{
			"full",
			header.ErrorInfo{
				{
					URI: &uri.Any{
						URL: url.URL{
							Scheme:   "https",
							Host:     "example.com",
							Path:     "/a/b/c",
							RawQuery: "foo=bar",
						},
					},
					Params: make(header.Values).Set("foo", "bar").Set("baz", ""),
				},
				{
					URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/x/y/z"}},
				},
			},
			"Error-Info: <https://example.com/a/b/c?foo=bar>;baz;foo=bar, <https://example.com/x/y/z>",
		},
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

func TestErrorInfo_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.ErrorInfo
		wantRes string
		wantErr error
	}{
		{"nil", header.ErrorInfo(nil), "", nil},
		{"empty", header.ErrorInfo{}, "Error-Info: ", nil},
		{
			"full",
			header.ErrorInfo{
				{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}}},
				{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/x/y/z"}}},
			},
			"Error-Info: <https://example.com/a/b/c>, <https://example.com/x/y/z>",
			nil,
		},
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

func TestErrorInfo_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ErrorInfo
		want string
	}{
		{"nil", header.ErrorInfo(nil), ""},
		{"empty", header.ErrorInfo{}, ""},
		{
			"full",
			header.ErrorInfo{
				{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}}},
				{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/x/y/z"}}},
			},
			"<https://example.com/a/b/c>, <https://example.com/x/y/z>",
		},
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

func TestErrorInfo_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ErrorInfo
		val  any
		want bool
	}{
		{"nil ptr to nil", header.ErrorInfo(nil), nil, false},
		{"nil ptr to nil ptr", header.ErrorInfo(nil), header.ErrorInfo(nil), true},
		{"zero ptr to nil ptr", header.ErrorInfo{}, header.ErrorInfo(nil), true},
		{"zero to zero", header.ErrorInfo{}, header.ErrorInfo{}, true},
		{"zero to zero ptr", header.ErrorInfo{}, &header.ErrorInfo{}, true},
		{"zero to nil ptr", header.ErrorInfo{}, (*header.ErrorInfo)(nil), false},
		{
			"not match 1",
			header.ErrorInfo{{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}}}},
			header.ErrorInfo{},
			false,
		},
		{
			"not match 2",
			header.ErrorInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field1", `"QWERTY"`),
				},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "asd.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field2", "asd"),
				},
			},
			header.ErrorInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "asd.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field2", "asd"),
				},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field1", `"QWERTY"`),
				},
			},
			false,
		},
		{
			"not match 3",
			header.ErrorInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field1", `"QWERTY"`),
				},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "asd.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field2", "asd"),
				},
			},
			header.ErrorInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field1", `"qwerty"`),
				},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "asd.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field2", "asd"),
				},
			},
			false,
		},
		{
			"not match 4",
			header.ErrorInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("purpose", "qwe"),
				},
			},
			header.ErrorInfo{
				{
					URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
				},
			},
			false,
		},
		{
			"match",
			header.ErrorInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field1", `"QWERTY"`),
				},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "asd.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field2", "asd").Set("purpose", "qwe"),
				},
			},
			header.ErrorInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "HTTPS", Host: "ABC.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field1", `"qwerty"`).Append("field1", `"QWERTY"`),
				},
				{
					URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "ASD.COM", Path: "/a/b/c"}},
					Params: make(header.Values).
						Set("purpose", "qwe").
						Append("field1", "zxc").
						Append("field2", "ASD"),
				},
			},
			true,
		},
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

func TestErrorInfo_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ErrorInfo
		want bool
	}{
		{"nil", header.ErrorInfo(nil), false},
		{"empty", header.ErrorInfo{}, false},
		{
			"valid",
			header.ErrorInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
					Params: header.Values{"purpose": {"qwe"}},
				},
			},
			true,
		},
		{"invalid 1", header.ErrorInfo{{URI: (*uri.Any)(nil)}}, false},
		{
			"invalid 2",
			header.ErrorInfo{{
				URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com"}},
				Params: header.Values{"f i e l d": {"123"}},
			}},
			false,
		},
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

func TestErrorInfo_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.ErrorInfo
	}{
		{"nil", nil},
		{"empty", header.ErrorInfo{}},
		{
			"full",
			header.ErrorInfo{{
				URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
				Params: header.Values{"purpose": {"qwe"}},
			}},
		},
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
