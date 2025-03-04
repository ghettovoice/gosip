package header_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/internal/types"
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
		{"nil", header.Contact(nil), nil, ""},
		{"empty", header.Contact{}, &header.RenderOptions{Compact: true}, "m: *"},
		{"empty elem", header.Contact{{}}, nil, "Contact: <>"},
		{
			"full",
			header.Contact{
				{
					DisplayName: "Mr. Watson",
					URI: &uri.SIP{
						User: uri.User("watson"),
						Addr: uri.Host("worcester.bell-telephone.com"),
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
					URI:    &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
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
						Addr: uri.Host("worcester.bell-telephone.com"),
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
					URI:    &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
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
				{URI: &uri.Any{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
				{URI: &uri.Any{Scheme: "https", Host: "example.com", Path: "/x/y/z"}},
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
					Addr: uri.Host("worcester.bell-telephone.com"),
				},
				Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
			}},
			header.Contact{{
				DisplayName: "Mr. Watson",
				URI:         &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
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
						Addr: uri.Host("worcester.bell-telephone.com"),
					},
					Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
				},
				{
					DisplayName: "Mr. Watson",
					URI:         &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
					Params:      make(header.Values).Set("q", "0.1"),
				},
			},
			header.Contact{
				{
					DisplayName: "Mr. Watson",
					URI:         &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
					Params:      make(header.Values).Set("q", "0.1"),
				},
				{
					DisplayName: "Mr. Watson",
					URI: &uri.SIP{
						User: uri.User("watson"),
						Addr: uri.Host("worcester.bell-telephone.com"),
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
						Addr: uri.Host("worcester.bell-telephone.com"),
					},
					Params: make(header.Values).Set("q", "1"),
				},
			},
			header.Contact{
				{
					DisplayName: "Mr. Watson",
					URI: &uri.SIP{
						User: uri.User("watson"),
						Addr: uri.Host("worcester.bell-telephone.com"),
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
						Addr: uri.Host("worcester.bell-telephone.com"),
					},
					Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
				},
				{
					DisplayName: "Mr. Watson",
					URI:         &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
					Params:      make(header.Values).Set("q", "0.1"),
				},
			},
			header.Contact{
				{
					DisplayName: "Mr. Watson",
					URI: &uri.SIP{
						User: uri.User("watson"),
						Addr: uri.Host("worcester.bell-telephone.com"),
					},
					Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
				},
				{
					DisplayName: "Mr. Watson",
					URI:         &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
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
				URI: &uri.Any{Scheme: "https", Host: "example.com", Path: "/a/b/c"},
			}},
			true,
		},
		{"invalid 1", header.Contact{{URI: (*uri.Any)(nil)}}, false},
		{"invalid 2", header.Contact{{URI: &uri.Any{}}}, false},
		{
			"invalid 3",
			header.Contact{{
				URI:    &uri.Any{Scheme: "https", Host: "example.com"},
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
				URI:    &uri.Any{Scheme: "https", Host: "example.com", Path: "/a/b/c"},
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
		name    string
		hdr     header.Contact
		want    map[string]any
		wantErr error
	}{
		{
			name: "nil",
			hdr:  header.Contact(nil),
			want: map[string]any{
				"name":  "Contact",
				"value": "",
			},
		},
		{
			name: "empty",
			hdr:  header.Contact{},
			want: map[string]any{
				"name":  "Contact",
				"value": "*",
			},
		},
		{
			name: "single_sip",
			hdr: header.Contact{
				{
					DisplayName: "Alice",
					URI: &uri.SIP{
						User: uri.User("alice"),
						Addr: uri.Host("example.com"),
					},
					Params: make(header.Values).Set("expires", "3600"),
				},
			},
			want: map[string]any{
				"name":  "Contact",
				"value": "\"Alice\" <sip:alice@example.com>;expires=3600",
			},
		},
		{
			name: "multiple_uris",
			hdr: header.Contact{
				{
					URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("example.com")},
					Params: make(header.Values).Set("q", "0.7"),
				},
				{
					URI:    &uri.Tel{Number: "+123", Params: make(types.Values).Set("ext", "555")},
					Params: make(header.Values).Set("q", "0.3"),
				},
			},
			want: map[string]any{
				"name":  "Contact",
				"value": "<sip:alice@example.com>;q=0.7, <tel:+123;ext=555>;q=0.3",
			},
		},
		{
			name: "any_uri",
			hdr: header.Contact{
				{
					URI: &uri.Any{Scheme: "mailto", Opaque: "alice@example.com"},
				},
			},
			want: map[string]any{
				"name":  "Contact",
				"value": "<mailto:alice@example.com>",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(c.hdr)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("json.Marshal(hdr) error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
				return
			}

			var gotMap map[string]any
			if err := json.Unmarshal(got, &gotMap); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			if c.want != nil {
				if name, ok := gotMap["name"].(string); !ok || name != c.want["name"] {
					t.Errorf("got[\"name\"] = %v, want %v", gotMap["name"], c.want["name"])
				}
				if wantValue, ok := c.want["value"]; ok {
					if value, ok := gotMap["value"].(string); !ok || value != wantValue {
						t.Errorf("got[\"value\"] = %v, want %v", gotMap["value"], wantValue)
					}
				}
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
		{
			name:    "null",
			data:    "null",
			want:    header.Contact(nil),
			wantErr: false,
		},
		{
			name: "empty",
			data: `{"name":"Contact","value":"*"}`,
			want: header.Contact{},
		},
		{
			name: "single_sip",
			data: `{"name":"Contact","value":"\"Alice\" <sip:alice@example.com>;expires=3600"}`,
			want: header.Contact{
				{
					DisplayName: "Alice",
					URI: &uri.SIP{
						User: uri.User("alice"),
						Addr: uri.Host("example.com"),
					},
					Params: make(header.Values).Set("expires", "3600"),
				},
			},
		},
		{
			name: "multiple_uris",
			data: `{"name":"Contact","value":"<sip:alice@example.com>;q=0.7, <tel:+123;ext=555>;q=0.3"}`,
			want: header.Contact{
				{
					URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("example.com")},
					Params: make(header.Values).Set("q", "0.7"),
				},
				{
					URI:    &uri.Tel{Number: "+123", Params: make(types.Values).Set("ext", "555")},
					Params: make(header.Values).Set("q", "0.3"),
				},
			},
		},
		{
			name: "any_uri",
			data: `{"name":"Contact","value":"<mailto:alice@example.com>"}`,
			want: header.Contact{
				{
					URI: &uri.Any{Scheme: "mailto", Opaque: "alice@example.com"},
				},
			},
		},
		{
			name:    "invalid_json",
			data:    `{"name":"Contact","value":`,
			want:    header.Contact(nil),
			wantErr: true,
		},
		{
			name:    "wrong_header",
			data:    `{"name":"From","value":"\"Alice\" <sip:alice@example.com>"}`,
			want:    header.Contact(nil),
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got header.Contact
			err := json.Unmarshal([]byte(c.data), &got)
			if (err != nil) != c.wantErr {
				t.Errorf("json.Unmarshal(data, got) error = %v, want %v", err, c.wantErr)
				return
			}
			if err != nil {
				return
			}

			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Errorf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
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
		{
			name: "empty",
			hdr:  header.Contact{},
		},
		{
			name: "single_sip",
			hdr: header.Contact{
				{
					DisplayName: "Alice",
					URI: &uri.SIP{
						User: uri.User("alice"),
						Addr: uri.Host("example.com"),
					},
					Params: make(header.Values).Set("expires", "3600"),
				},
			},
		},
		{
			name: "multiple_uris",
			hdr: header.Contact{
				{
					URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("example.com")},
					Params: make(header.Values).Set("q", "0.7"),
				},
				{
					URI:    &uri.Tel{Number: "+123", Params: make(types.Values).Set("ext", "555")},
					Params: make(header.Values).Set("q", "0.3"),
				},
			},
		},
		{
			name: "any_uri",
			hdr: header.Contact{
				{
					URI: &uri.Any{Scheme: "mailto", Opaque: "alice@example.com"},
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
				t.Errorf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}
