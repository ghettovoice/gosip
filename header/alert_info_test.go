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

func TestAlertInfo_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.AlertInfo
		want string
	}{
		{"nil", header.AlertInfo(nil), ""},
		{"empty", header.AlertInfo{}, "Alert-Info: "},
		{"empty elem", header.AlertInfo{{}}, "Alert-Info: <>"},
		{
			"full",
			header.AlertInfo{
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
			"Alert-Info: <https://example.com/a/b/c?foo=bar>;baz;foo=bar, <https://example.com/x/y/z>",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got, want := c.hdr.Render(nil), c.want; got != want {
				t.Errorf("hdr.Render() = %q, want %q", got, want)
			}
		})
	}
}

func TestAlertInfo_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.AlertInfo
		wantRes string
		wantErr error
	}{
		{"nil", header.AlertInfo(nil), "", nil},
		{"empty", header.AlertInfo{}, "Alert-Info: ", nil},
		{
			"full",
			header.AlertInfo{
				{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}}},
				{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/x/y/z"}}},
			},
			"Alert-Info: <https://example.com/a/b/c>, <https://example.com/x/y/z>",
			nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			_, err := c.hdr.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("hdr.RenderTo(sb) error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}
			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestAlertInfo_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.AlertInfo
		want string
	}{
		{"nil", header.AlertInfo(nil), ""},
		{"empty", header.AlertInfo{}, ""},
		{
			"full",
			header.AlertInfo{
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

func TestAlertInfo_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.AlertInfo
		val  any
		want bool
	}{
		{"nil ptr to nil", header.AlertInfo(nil), nil, false},
		{"nil ptr to nil ptr", header.AlertInfo(nil), header.AlertInfo(nil), true},
		{"zero ptr to nil ptr", header.AlertInfo{}, header.AlertInfo(nil), true},
		{"zero to zero", header.AlertInfo{}, header.AlertInfo{}, true},
		{"zero to zero ptr", header.AlertInfo{}, &header.AlertInfo{}, true},
		{"zero to nil ptr", header.AlertInfo{}, (*header.AlertInfo)(nil), false},
		{
			"not match 1",
			header.AlertInfo{{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}}}},
			header.AlertInfo{},
			false,
		},
		{
			"not match 2",
			header.AlertInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field1", `"QWERTY"`),
				},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "asd.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field2", "asd"),
				},
			},
			header.AlertInfo{
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
			header.AlertInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field1", `"QWERTY"`),
				},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "asd.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field2", "asd"),
				},
			},
			header.AlertInfo{
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
			header.AlertInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("purpose", "qwe"),
				},
			},
			header.AlertInfo{
				{
					URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
				},
			},
			false,
		},
		{
			"match",
			header.AlertInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field1", `"QWERTY"`),
				},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "asd.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field2", "asd").Set("purpose", "qwe"),
				},
			},
			header.AlertInfo{
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

func TestAlertInfo_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.AlertInfo
		want bool
	}{
		{"nil", header.AlertInfo(nil), false},
		{"empty", header.AlertInfo{}, false},
		{
			"valid",
			header.AlertInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
					Params: header.Values{"purpose": {"qwe"}},
				},
			},
			true,
		},
		{"invalid 1", header.AlertInfo{{URI: (*uri.Any)(nil)}}, false},
		{
			"invalid 2",
			header.AlertInfo{{
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

func TestAlertInfo_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.AlertInfo
	}{
		{"nil", header.AlertInfo(nil)},
		{"empty", header.AlertInfo{}},
		{
			"full",
			header.AlertInfo{{
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
