package header_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestCallInfo_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.CallInfo
		want string
	}{
		{"nil", header.CallInfo(nil), ""},
		{"empty", header.CallInfo{}, "Call-Info: "},
		{"empty elem", header.CallInfo{{}}, "Call-Info: <>"},
		{
			"full",
			header.CallInfo{
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
			"Call-Info: <https://example.com/a/b/c?foo=bar>;baz;foo=bar, <https://example.com/x/y/z>",
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

func TestCallInfo_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     header.CallInfo
		wantRes string
		wantErr error
	}{
		{"nil", header.CallInfo(nil), "", nil},
		{"empty", header.CallInfo{}, "Call-Info: ", nil},
		{
			"full",
			header.CallInfo{
				{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}}},
				{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/x/y/z"}}},
			},
			"Call-Info: <https://example.com/a/b/c>, <https://example.com/x/y/z>",
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

func TestCallInfo_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.CallInfo
		want string
	}{
		{"nil", header.CallInfo(nil), ""},
		{"empty", header.CallInfo{}, ""},
		{
			"full",
			header.CallInfo{
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

func TestCallInfo_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.CallInfo
		val  any
		want bool
	}{
		{"nil ptr to nil", header.CallInfo(nil), nil, false},
		{"nil ptr to nil ptr", header.CallInfo(nil), header.CallInfo(nil), true},
		{"zero ptr to nil ptr", header.CallInfo{}, header.CallInfo(nil), true},
		{"zero to zero", header.CallInfo{}, header.CallInfo{}, true},
		{"zero to zero ptr", header.CallInfo{}, &header.CallInfo{}, true},
		{"zero to nil ptr", header.CallInfo{}, (*header.CallInfo)(nil), false},
		{
			"not match 1",
			header.CallInfo{{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}}}},
			header.CallInfo{},
			false,
		},
		{
			"not match 2",
			header.CallInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field1", `"QWERTY"`),
				},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "asd.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field2", "asd"),
				},
			},
			header.CallInfo{
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
			header.CallInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field1", `"QWERTY"`),
				},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "asd.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field2", "asd"),
				},
			},
			header.CallInfo{
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
			header.CallInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("purpose", "qwe"),
				},
			},
			header.CallInfo{
				{
					URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
				},
			},
			false,
		},
		{
			"match",
			header.CallInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "abc.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field1", `"QWERTY"`),
				},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "asd.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("field2", "asd").Set("purpose", "qwe"),
				},
			},
			header.CallInfo{
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

func TestCallInfo_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.CallInfo
		want bool
	}{
		{"nil", header.CallInfo(nil), false},
		{"empty", header.CallInfo{}, false},
		{
			"valid",
			header.CallInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
					Params: header.Values{"purpose": {"qwe"}},
				},
			},
			true,
		},
		{"invalid 1", header.CallInfo{{URI: (*uri.Any)(nil)}}, false},
		{
			"invalid 2",
			header.CallInfo{{
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

func TestCallInfo_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.CallInfo
	}{
		{"nil", nil},
		{"empty", header.CallInfo{}},
		{
			"full",
			header.CallInfo{{
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

func TestCallInfo_MarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.CallInfo
		want string
	}{
		{"nil", nil, "null"},
		{"empty", header.CallInfo{}, `{"name":"Call-Info","value":""}`},
		{
			"single",
			header.CallInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("purpose", "icon"),
				},
			},
			`{"name":"Call-Info","value":"\u003chttps://example.com/a/b/c\u003e;purpose=icon"}`,
		},
		{
			"multiple",
			header.CallInfo{
				{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}}},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/x/y/z"}},
					Params: make(header.Values).Set("purpose", "card"),
				},
			},
			`{"name":"Call-Info","value":"\u003chttps://example.com/a/b/c\u003e, \u003chttps://example.com/x/y/z\u003e;purpose=card"}`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			if got := string(got); got != c.want {
				t.Fatalf("json.Marshal(hdr) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestCallInfo_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    header.CallInfo
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"<https://example.com/a/b/c>"}`, nil, true},
		{"empty value", `{"name":"Call-Info","value":""}`, header.CallInfo{}, false},
		{"invalid json", `{"name":"Call-Info","value":`, nil, true},
		{"wrong header", `{"name":"From","value":"<sip:alice@example.com>"}`, nil, true},
		{"invalid value", `{"name":"Call-Info","value":"https://example.com/a/b/c"}`, nil, true},
		{
			"single",
			`{"name":"Call-Info","value":"<https://example.com/a/b/c>;purpose=icon"}`,
			header.CallInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("purpose", "icon"),
				},
			},
			false,
		},
		{
			"multiple",
			`{"name":"Call-Info","value":"<https://example.com/a/b/c>, <https://example.com/x/y/z>;purpose=card"}`,
			header.CallInfo{
				{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}}},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/x/y/z"}},
					Params: make(header.Values).Set("purpose", "card"),
				},
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got header.CallInfo
			if err := json.Unmarshal([]byte(c.data), &got); err != nil {
				if !c.wantErr {
					t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
				}
				return
			}

			if c.wantErr {
				t.Fatal("json.Unmarshal(data, got) error = nil, want error")
			}

			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Fatalf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestCallInfo_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.CallInfo
	}{
		{"nil", nil},
		{"empty", header.CallInfo{}},
		{
			"single",
			header.CallInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
					Params: make(header.Values).Set("purpose", "icon"),
				},
			},
		},
		{
			"multiple",
			header.CallInfo{
				{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}}},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/x/y/z"}},
					Params: make(header.Values).Set("purpose", "card"),
				},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			var got header.CallInfo
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}
