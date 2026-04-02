package header_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestContact_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  header.Contact
		opts *header.RenderOptions
		want string
	}{
		{"nil", nil, nil, ""},
		{"empty", header.Contact{}, &header.RenderOptions{Compact: true}, "m: *"},
		{"empty elem", header.Contact{{}}, nil, "Contact: <>"},
		{
			"full",
			header.Contact{
				{
					DisplayName: "Mr. Watson",
					URI: &uri.SIP{
						User: uri.User("watson"),
						Addr: uri.AddrFromHost("worcester.bell-telephone.com"),
					},
					Params: make(header.Values).Set("expires", "3600"),
				},
				{
					URI: &uri.Tel{
						Number: "+123",
						Params: make(types.Values).Set("ext", "555"),
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Render(c.opts); got != c.want {
				t.Errorf("hdr.Render(opts) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestContact_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
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
						User: uri.User("watson"),
						Addr: uri.AddrFromHost("worcester.bell-telephone.com"),
					},
					Params: make(header.Values).Set("expires", "3600"),
				},
				{
					URI: &uri.Tel{
						Number: "+123",
						Params: make(types.Values).Set("ext", "555"),
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

func TestContact_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.String(); got != c.want {
				t.Errorf("hdr.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestContact_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
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
					User: uri.User("watson"),
					Addr: uri.AddrFromHost("worcester.bell-telephone.com"),
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
						User: uri.User("watson"),
						Addr: uri.AddrFromHost("worcester.bell-telephone.com"),
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
						User: uri.User("watson"),
						Addr: uri.AddrFromHost("worcester.bell-telephone.com"),
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
						User: uri.User("watson"),
						Addr: uri.AddrFromHost("worcester.bell-telephone.com"),
					},
					Params: make(header.Values).Set("q", "1"),
				},
			},
			header.Contact{
				{
					DisplayName: "Mr. Watson",
					URI: &uri.SIP{
						User: uri.User("watson"),
						Addr: uri.AddrFromHost("worcester.bell-telephone.com"),
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
						User: uri.User("watson"),
						Addr: uri.AddrFromHost("worcester.bell-telephone.com"),
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
						User: uri.User("watson"),
						Addr: uri.AddrFromHost("worcester.bell-telephone.com"),
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.Equal(c.val); got != c.want {
				t.Errorf("hdr.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestContact_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.hdr.IsValid(); got != c.want {
				t.Errorf("hdr.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestContact_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
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

func TestContact_MarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
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
						User: uri.User("alice"),
						Addr: uri.AddrFromHost("example.com"),
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
					URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.AddrFromHost("example.com")},
					Params: make(header.Values).Set("q", "0.7"),
				},
				{
					URI:    &uri.Tel{Number: "+123", Params: make(types.Values).Set("ext", "555")},
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

func TestContact_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
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
						User: uri.User("alice"),
						Addr: uri.AddrFromHost("example.com"),
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
					URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.AddrFromHost("example.com")},
					Params: make(header.Values).Set("q", "0.7"),
				},
				{
					URI:    &uri.Tel{Number: "+123", Params: make(types.Values).Set("ext", "555")},
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got header.Contact
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

func TestContact_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
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
						User: uri.User("alice"),
						Addr: uri.AddrFromHost("example.com"),
					},
					Params: make(header.Values).Set("expires", "3600"),
				},
			},
		},
		{
			"multiple uris",
			header.Contact{
				{
					URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.AddrFromHost("example.com")},
					Params: make(header.Values).Set("q", "0.7"),
				},
				{
					URI:    &uri.Tel{Number: "+123", Params: make(types.Values).Set("ext", "555")},
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

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(c.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			var got header.Contact
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}
