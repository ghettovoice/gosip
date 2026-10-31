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

func TestContact_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Contact
		opts []header.RenderOptions
		want string
	}{
		{"nil", nil, nil, ""},
		{"empty", header.Contact{}, []header.RenderOptions{{Compact: true}}, "m: *"},
		{"empty elem", header.Contact{{}}, nil, "Contact: <>"},
		{
			"full",
			header.Contact{
				{
					DisplayName: "Mr. Watson",
					URI: &uri.SIP{
						User: uri.MakeUserInfo("watson"),
						Addr: uri.MakeHostAddr("worcester.bell-telephone.com"),
					},
					Params: make(header.Values).Set("expires", "3600"),
				},
				{
					URI: &uri.Tel{
						Number: "+123",
						Params: make(header.Values).Set("ext", "555"),
					},
					Params: make(header.Values).Set("q", "0.7"),
				},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "mailto", Opaque: "watson@bell-telephone.com"}},
					Params: make(header.Values).Set("q", "0.1"),
				},
			},
			nil,
			"Contact: \"Mr. Watson\" <sip:watson@worcester.bell-telephone.com>;expires=3600, " +
				"<tel:+123;ext=555>;q=0.7, <mailto:watson@bell-telephone.com>;q=0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.Render(tt.opts...); got != tt.want {
				t.Errorf("hdr.Render(opts...) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContact_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hdr     header.Contact
		wantRes string
		wantErr error
	}{
		{"nil", header.Contact(nil), "", nil},
		{"empty", header.Contact{}, "Contact: *", nil},
		{
			"full",
			header.Contact{
				{
					DisplayName: "Mr. Watson",
					URI: &uri.SIP{
						User: uri.MakeUserInfo("watson"),
						Addr: uri.MakeHostAddr("worcester.bell-telephone.com"),
					},
					Params: make(header.Values).Set("expires", "3600"),
				},
				{
					URI: &uri.Tel{
						Number: "+123",
						Params: make(header.Values).Set("ext", "555"),
					},
					Params: make(header.Values).Set("q", "0.7"),
				},
				{
					URI:    &uri.Any{URL: url.URL{Scheme: "mailto", Opaque: "watson@bell-telephone.com"}},
					Params: make(header.Values).Set("q", "0.1"),
				},
			},
			"Contact: \"Mr. Watson\" <sip:watson@worcester.bell-telephone.com>;expires=3600, " +
				"<tel:+123;ext=555>;q=0.7, <mailto:watson@bell-telephone.com>;q=0.1",
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

func TestContact_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Contact
		want string
	}{
		{"nil", header.Contact(nil), ""},
		{"empty", header.Contact{}, "*"},
		{
			"full",
			header.Contact{
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

func TestContact_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Contact
		val  any
		want bool
	}{
		{"nil ptr to nil", header.Contact(nil), nil, false},
		{"nil ptr to nil ptr", header.Contact(nil), header.Contact(nil), true},
		{"zero ptr to nil ptr", header.Contact{}, header.Contact(nil), true},
		{"zero to zero", header.Contact{}, header.Contact{}, true},
		{"zero to zero ptr", header.Contact{}, &header.Contact{}, true},
		{"zero to nil ptr", header.Contact{}, (*header.Contact)(nil), false},
		{
			"not match 1",
			header.Contact{{
				DisplayName: "Mr. Watson",
				URI: &uri.SIP{
					User: uri.MakeUserInfo("watson"),
					Addr: uri.MakeHostAddr("worcester.bell-telephone.com"),
				},
				Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
			}},
			header.Contact{{
				DisplayName: "Mr. Watson",
				URI:         &uri.Any{URL: url.URL{Scheme: "mailto", Opaque: "watson@bell-telephone.com"}},
				Params:      make(header.Values).Set("q", "0.1"),
			}},
			false,
		},
		{
			"not match 2",
			header.Contact{
				{
					DisplayName: "Mr. Watson",
					URI: &uri.SIP{
						User: uri.MakeUserInfo("watson"),
						Addr: uri.MakeHostAddr("worcester.bell-telephone.com"),
					},
					Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
				},
				{
					DisplayName: "Mr. Watson",
					URI:         &uri.Any{URL: url.URL{Scheme: "mailto", Opaque: "watson@bell-telephone.com"}},
					Params:      make(header.Values).Set("q", "0.1"),
				},
			},
			header.Contact{
				{
					DisplayName: "Mr. Watson",
					URI:         &uri.Any{URL: url.URL{Scheme: "mailto", Opaque: "watson@bell-telephone.com"}},
					Params:      make(header.Values).Set("q", "0.1"),
				},
				{
					DisplayName: "Mr. Watson",
					URI: &uri.SIP{
						User: uri.MakeUserInfo("watson"),
						Addr: uri.MakeHostAddr("worcester.bell-telephone.com"),
					},
					Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
				},
			},
			false,
		},
		{
			"not match 3",
			header.Contact{
				{
					DisplayName: "Mr. Watson",
					URI: &uri.SIP{
						User: uri.MakeUserInfo("watson"),
						Addr: uri.MakeHostAddr("worcester.bell-telephone.com"),
					},
					Params: make(header.Values).Set("q", "1"),
				},
			},
			header.Contact{
				{
					DisplayName: "Mr. Watson",
					URI: &uri.SIP{
						User: uri.MakeUserInfo("watson"),
						Addr: uri.MakeHostAddr("worcester.bell-telephone.com"),
					},
					Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
				},
			},
			false,
		},
		{
			"match",
			header.Contact{
				{
					DisplayName: "Mr. Watson",
					URI: &uri.SIP{
						User: uri.MakeUserInfo("watson"),
						Addr: uri.MakeHostAddr("worcester.bell-telephone.com"),
					},
					Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
				},
				{
					DisplayName: "Mr. Watson",
					URI:         &uri.Any{URL: url.URL{Scheme: "mailto", Opaque: "watson@bell-telephone.com"}},
					Params:      make(header.Values).Set("q", "0.1"),
				},
			},
			header.Contact{
				{
					DisplayName: "Mr. Watson",
					URI: &uri.SIP{
						User: uri.MakeUserInfo("watson"),
						Addr: uri.MakeHostAddr("worcester.bell-telephone.com"),
					},
					Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
				},
				{
					DisplayName: "Mr. Watson",
					URI:         &uri.Any{URL: url.URL{Scheme: "mailto", Opaque: "watson@bell-telephone.com"}},
					Params:      make(header.Values).Set("q", "0.1").Set("a", "aaa"),
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

func TestContact_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Contact
		want bool
	}{
		{"nil", header.Contact(nil), false},
		{"empty", header.Contact{}, true},
		{
			"valid",
			header.Contact{{
				URI: &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
			}},
			true,
		},
		{"invalid 1", header.Contact{{URI: (*uri.Any)(nil)}}, false},
		{"invalid 2", header.Contact{{URI: &uri.Any{}}}, false},
		{
			"invalid 3",
			header.Contact{{
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

func TestContact_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Contact
	}{
		{"nil", nil},
		{"empty", header.Contact{}},
		{
			"full",
			header.Contact{{
				URI:    &uri.Any{URL: url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
				Params: header.Values{"expires": {"3600"}},
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

func TestContact_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Contact
		want string
	}{
		{"nil", nil, "null"},
		{"empty", header.Contact{}, `{"name":"Contact","value":"*"}`},
		{
			"single sip",
			header.Contact{
				{
					DisplayName: "Alice",
					URI: &uri.SIP{
						User: uri.MakeUserInfo("alice"),
						Addr: uri.MakeHostAddr("example.com"),
					},
					Params: make(header.Values).Set("expires", "3600"),
				},
			},
			`{"name":"Contact","value":"\"Alice\" \u003csip:alice@example.com\u003e;expires=3600"}`,
		},
		{
			"multiple uris",
			header.Contact{
				{
					URI:    &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("example.com")},
					Params: make(header.Values).Set("q", "0.7"),
				},
				{
					URI:    &uri.Tel{Number: "+123", Params: make(header.Values).Set("ext", "555")},
					Params: make(header.Values).Set("q", "0.3"),
				},
			},
			`{"name":"Contact","value":"\u003csip:alice@example.com\u003e;q=0.7, \u003ctel:+123;ext=555\u003e;q=0.3"}`,
		},
		{
			"any uri",
			header.Contact{
				{
					URI: &uri.Any{URL: url.URL{Scheme: "mailto", Opaque: "alice@example.com"}},
				},
			},
			`{"name":"Contact","value":"\u003cmailto:alice@example.com\u003e"}`,
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

func TestContact_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    header.Contact
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"*"}`, nil, true},
		{"empty value", `{"name":"Contact","value":""}`, header.Contact{}, false},
		{"asterisk", `{"name":"Contact","value":"*"}`, header.Contact{}, false},
		{"invalid json", `{"name":"Contact","value":`, nil, true},
		{"wrong header", `{"name":"From","value":"\"Alice\" <sip:alice@example.com>"}`, nil, true},
		{
			"single sip",
			`{"name":"Contact","value":"\"Alice\" <sip:alice@example.com>;expires=3600"}`,
			header.Contact{
				{
					DisplayName: "Alice",
					URI: &uri.SIP{
						User: uri.MakeUserInfo("alice"),
						Addr: uri.MakeHostAddr("example.com"),
					},
					Params: make(header.Values).Set("expires", "3600"),
				},
			},
			false,
		},
		{
			"multiple uris",
			`{"name":"Contact","value":"<sip:alice@example.com>;q=0.7, <tel:+123;ext=555>;q=0.3"}`,
			header.Contact{
				{
					URI:    &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("example.com")},
					Params: make(header.Values).Set("q", "0.7"),
				},
				{
					URI:    &uri.Tel{Number: "+123", Params: make(header.Values).Set("ext", "555")},
					Params: make(header.Values).Set("q", "0.3"),
				},
			},
			false,
		},
		{
			"any uri",
			`{"name":"Contact","value":"<mailto:alice@example.com>"}`,
			header.Contact{
				{
					URI: &uri.Any{URL: url.URL{Scheme: "mailto", Opaque: "alice@example.com"}},
				},
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got header.Contact
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

func TestContact_RoundTripJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Contact
	}{
		{"nil", nil},
		{"empty", header.Contact{}},
		{
			"single sip",
			header.Contact{
				{
					DisplayName: "Alice",
					URI: &uri.SIP{
						User: uri.MakeUserInfo("alice"),
						Addr: uri.MakeHostAddr("example.com"),
					},
					Params: make(header.Values).Set("expires", "3600"),
				},
			},
		},
		{
			"multiple uris",
			header.Contact{
				{
					URI:    &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("example.com")},
					Params: make(header.Values).Set("q", "0.7"),
				},
				{
					URI:    &uri.Tel{Number: "+123", Params: make(header.Values).Set("ext", "555")},
					Params: make(header.Values).Set("q", "0.3"),
				},
			},
		},
		{
			"any uri",
			header.Contact{
				{
					URI: &uri.Any{URL: url.URL{Scheme: "mailto", Opaque: "alice@example.com"}},
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

			var got header.Contact
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}
