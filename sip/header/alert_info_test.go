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

func TestAlertInfo_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got, want := tt.hdr.Render(), tt.want; got != want {
				t.Errorf("hdr.Render() = %q, want %q", got, want)
			}
		})
	}
}

func TestAlertInfo_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder

			_, err := tt.hdr.RenderTo(&sb)
			if diff := cmp.Diff(err, tt.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("hdr.RenderTo(&sb) error = %v, want %v\ndiff (-got +want):\n%v", err, tt.wantErr, diff)
			}

			if got := sb.String(); got != tt.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, tt.wantRes)
			}
		})
	}
}

func TestAlertInfo_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.String(); got != tt.want {
				t.Errorf("hdr.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAlertInfo_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.Equal(tt.val); got != tt.want {
				t.Errorf("hdr.Equal(val) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAlertInfo_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.IsValid(); got != tt.want {
				t.Errorf("hdr.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAlertInfo_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.hdr.Clone()
			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}

func TestAlertInfo_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.AlertInfo
		want string
	}{
		{"nil", nil, "null"},
		{"empty", header.AlertInfo{}, `{"name":"Alert-Info","value":""}`},
		{
			"single",
			header.AlertInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/tones/main"}},
					Params: make(header.Values).Set("purpose", "ring").Set("foo", "bar"),
				},
			},
			`{"name":"Alert-Info","value":"\u003chttps://example.com/tones/main\u003e;foo=bar;purpose=ring"}`,
		},
		{
			"multiple",
			header.AlertInfo{
				{
					URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/tones/main"}},
				},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/tones/alt"}},
					Params: make(header.Values).Set("purpose", "alternate"),
				},
			},
			`{"name":"Alert-Info","value":"\u003chttps://example.com/tones/main\u003e, \u003chttps://example.com/tones/alt\u003e;purpose=alternate"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tt.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			if got := string(got); got != tt.want {
				t.Fatalf("json.Marshal(hdr) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAlertInfo_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    header.AlertInfo
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", "{}", nil, true},
		{"empty name", `{"value":"<https://example.com/tones/main>"}`, nil, true},
		{"empty value", `{"name":"Alert-Info","value":""}`, header.AlertInfo{}, false},
		{"invalid json", `{"name":"Alert-Info","value":`, nil, true},
		{"wrong header", `{"name":"From","value":"<sip:alice@example.com>"}`, nil, true},
		{
			"single",
			`{"name":"Alert-Info","value":"<https://example.com/tones/main>;purpose=ring"}`,
			header.AlertInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/tones/main"}},
					Params: make(header.Values).Set("purpose", "ring"),
				},
			},
			false,
		},
		{
			"multiple",
			`{"name":"Alert-Info","value":"<https://example.com/tones/main>, <https://example.com/tones/alt>;purpose=alternate"}`,
			header.AlertInfo{
				{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/tones/main"}}},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/tones/alt"}},
					Params: make(header.Values).Set("purpose", "alternate"),
				},
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got header.AlertInfo
			if err := json.Unmarshal([]byte(tt.data), &got); err != nil {
				if !tt.wantErr {
					t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
				}
				return
			}

			if tt.wantErr {
				t.Fatal("json.Unmarshal(data, got) error = nil, want error")
			}

			if diff := cmp.Diff(got, tt.want); diff != "" {
				t.Fatalf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.want, diff)
			}
		})
	}
}

func TestAlertInfo_RoundTripJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.AlertInfo
	}{
		{"nil", nil},
		{"empty", header.AlertInfo{}},
		{
			"single",
			header.AlertInfo{
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/tones/main"}},
					Params: make(header.Values).Set("purpose", "ring"),
				},
			},
		},
		{
			"multiple",
			header.AlertInfo{
				{URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/tones/main"}}},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/tones/alt"}},
					Params: make(header.Values).Set("purpose", "alternate"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tt.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			var got header.AlertInfo
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}
