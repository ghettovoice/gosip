package header_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestAuthorization_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Authorization
		want string
	}{
		{"nil", (*header.Authorization)(nil), ""},
		{"zero", &header.Authorization{}, "Authorization: "},
		{
			"digest",
			&header.Authorization{
				AuthCredentials: &header.DigestCredentials{
					Username:   "root",
					Realm:      "example.com",
					Nonce:      "qwerty",
					URI:        &uri.SIP{Addr: uri.Host("example.com")},
					Response:   "587245234b3434cc3412213e5f113a54",
					Algorithm:  "MD5",
					CNonce:     "1q2w3e",
					Opaque:     "zxc",
					QOP:        "auth",
					NonceCount: 5,
					Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"Authorization: Digest algorithm=MD5, cnonce=\"1q2w3e\", nc=00000005, nonce=\"qwerty\", opaque=\"zxc\", " +
				"qop=auth, realm=\"example.com\", response=\"587245234b3434cc3412213e5f113a54\", username=\"root\", " +
				"uri=\"sip:example.com\", p1=abc, p2=\"a b c\"",
		},
		{
			"bearer",
			&header.Authorization{
				AuthCredentials: &header.BearerCredentials{Token: "QweRTY123"},
			},
			"Authorization: Bearer QweRTY123",
		},
		{
			"custom",
			&header.Authorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"Authorization: Custom p1=abc, p2=\"a b c\"",
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

func TestAuthorization_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.Authorization
		wantRes string
		wantErr error
	}{
		{"nil", (*header.Authorization)(nil), "", nil},
		{"zero", &header.Authorization{}, "Authorization: ", nil},
		{
			"custom",
			&header.Authorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"Authorization: Custom p1=abc, p2=\"a b c\"",
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

func TestAuthorization_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Authorization
		want string
	}{
		{"nil", (*header.Authorization)(nil), ""},
		{"zero", &header.Authorization{}, ""},
		{
			"custom",
			&header.Authorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"Custom p1=abc, p2=\"a b c\"",
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

func TestAuthorization_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Authorization
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.Authorization)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.Authorization)(nil), (*header.Authorization)(nil), true},
		{"zero ptr to nil ptr", &header.Authorization{}, (*header.Authorization)(nil), false},
		{"zero to zero", &header.Authorization{}, header.Authorization{}, true},
		{
			"not match 1",
			&header.Authorization{},
			&header.Authorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Qwerty",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			false,
		},
		{
			"not match 2",
			&header.Authorization{
				AuthCredentials: &header.DigestCredentials{
					Username:   "root",
					Realm:      "example.com",
					Nonce:      "qwerty",
					URI:        &uri.SIP{Addr: uri.Host("example.com")},
					Response:   "587245234b3434cc3412213e5f113a54",
					Algorithm:  "MD5",
					CNonce:     "1q2w3e",
					Opaque:     "zxc",
					QOP:        "auth",
					NonceCount: 5,
					Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			&header.Authorization{
				AuthCredentials: &header.BearerCredentials{
					Token: "QwertY",
				},
			},
			false,
		},
		{
			"match",
			&header.Authorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			&header.Authorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "ABC").Set("p2", `"a b c"`),
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

func TestAuthorization_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Authorization
		want bool
	}{
		{"nil", (*header.Authorization)(nil), false},
		{"zero", &header.Authorization{}, false},
		{
			"invalid 1",
			&header.Authorization{
				AuthCredentials: &header.DigestCredentials{
					Username: "root",
					Response: "587245234b3434cc3412213e5f113a54",
				},
			},
			false,
		},
		{"invalid 2", &header.Authorization{AuthCredentials: &header.BearerCredentials{}}, false},
		{"invalid 3", &header.Authorization{AuthCredentials: (*header.AnyCredentials)(nil)}, false},
		{
			"valid",
			&header.Authorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc"),
				},
			},
			true,
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

func TestAuthorization_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Authorization
	}{
		{"nil", nil},
		{"zero", &header.Authorization{}},
		{
			"digest",
			&header.Authorization{
				AuthCredentials: &header.DigestCredentials{
					Username:   "root",
					Realm:      "example.com",
					Nonce:      "qwerty",
					URI:        &uri.SIP{Addr: uri.Host("example.com")},
					Response:   "587245234b3434cc3412213e5f113a54",
					Algorithm:  "MD5",
					CNonce:     "1q2w3e",
					Opaque:     "zxc",
					QOP:        "auth",
					NonceCount: 5,
					Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
		},
		{
			"bearer",
			&header.Authorization{
				AuthCredentials: &header.BearerCredentials{
					Token: "QweRTY123",
				},
			},
		},
		{
			"custom",
			&header.Authorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.hdr.Clone()
			if c.hdr == nil {
				if got != nil {
					t.Errorf("hdr.Clone() = %+v, want nil", got)
				}
				return
			}
			if diff := cmp.Diff(got, c.hdr); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.hdr, diff)
			}
		})
	}
}

//nolint:gocognit
func TestAuthorization_MarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.Authorization
		want    map[string]any
		wantErr error
	}{
		{
			name: "nil",
			hdr:  (*header.Authorization)(nil),
		},
		{
			name: "zero",
			hdr:  &header.Authorization{},
			want: map[string]any{
				"name":  "Authorization",
				"value": "",
			},
		},
		{
			name: "digest",
			hdr: &header.Authorization{
				AuthCredentials: &header.DigestCredentials{
					Username:   "root",
					Realm:      "example.com",
					Nonce:      "qwerty",
					URI:        &uri.SIP{Addr: uri.Host("example.com")},
					Response:   "587245234b3434cc3412213e5f113a54",
					Algorithm:  "MD5",
					CNonce:     "1q2w3e",
					Opaque:     "zxc",
					QOP:        "auth",
					NonceCount: 5,
					Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			want: map[string]any{
				"name":  "Authorization",
				"value": "Digest algorithm=MD5, cnonce=\"1q2w3e\", nc=00000005, nonce=\"qwerty\", opaque=\"zxc\", qop=auth, realm=\"example.com\", response=\"587245234b3434cc3412213e5f113a54\", username=\"root\", uri=\"sip:example.com\", p1=abc, p2=\"a b c\"",
			},
		},
		{
			name: "bearer",
			hdr: &header.Authorization{
				AuthCredentials: &header.BearerCredentials{Token: "QweRTY123"},
			},
			want: map[string]any{
				"name":  "Authorization",
				"value": "Bearer QweRTY123",
			},
		},
		{
			name: "custom",
			hdr: &header.Authorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			want: map[string]any{
				"name":  "Authorization",
				"value": "Custom p1=abc, p2=\"a b c\"",
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

			if c.hdr == nil {
				if string(got) != "null" {
					t.Errorf("json.Marshal(nil) = %s, want \"null\"", string(got))
				}
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

func TestAuthorization_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    *header.Authorization
		wantErr bool
	}{
		{
			name:    "null",
			data:    "null",
			want:    &header.Authorization{},
			wantErr: false,
		},
		{
			name:    "zero",
			data:    `{"name":"Authorization","value":""}`,
			want:    &header.Authorization{},
			wantErr: true,
		},
		{
			name: "digest",
			data: `{"name":"Authorization","value":"Digest algorithm=MD5, cnonce=\"1q2w3e\", nc=00000005, nonce=\"qwerty\", opaque=\"zxc\", qop=auth, realm=\"example.com\", response=\"587245234b3434cc3412213e5f113a54\", username=\"root\", uri=\"sip:example.com\", p1=abc, p2=\"a b c\""}`,
			want: &header.Authorization{
				AuthCredentials: &header.DigestCredentials{
					Username:   "root",
					Realm:      "example.com",
					Nonce:      "qwerty",
					URI:        &uri.SIP{Addr: uri.Host("example.com")},
					Response:   "587245234b3434cc3412213e5f113a54",
					Algorithm:  "MD5",
					CNonce:     "1q2w3e",
					Opaque:     "zxc",
					QOP:        "auth",
					NonceCount: 5,
					Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
		},
		{
			name: "bearer",
			data: `{"name":"Authorization","value":"Bearer QweRTY123"}`,
			want: &header.Authorization{
				AuthCredentials: &header.BearerCredentials{Token: "QweRTY123"},
			},
		},
		{
			name: "custom",
			data: `{"name":"Authorization","value":"Custom p1=abc, p2=\"a b c\""}`,
			want: &header.Authorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
		},
		{
			name:    "invalid_json",
			data:    `{"name":"Authorization","value":`,
			want:    &header.Authorization{},
			wantErr: true,
		},
		{
			name:    "wrong_header",
			data:    `{"name":"From","value":"\"Alice\" <sip:alice@example.com>"}`,
			want:    &header.Authorization{},
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got header.Authorization
			err := json.Unmarshal([]byte(c.data), &got)
			if (err != nil) != c.wantErr {
				t.Errorf("json.Unmarshal(data, got) error = %v, want %v", err, c.wantErr)
				return
			}
			if err != nil {
				return
			}

			if diff := cmp.Diff(&got, c.want); diff != "" {
				t.Errorf("unmarshal mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestAuthorization_RoundTripJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.Authorization
	}{
		{
			name: "nil",
			hdr:  (*header.Authorization)(nil),
		},
		{
			name: "digest",
			hdr: &header.Authorization{
				AuthCredentials: &header.DigestCredentials{
					Username:   "root",
					Realm:      "example.com",
					Nonce:      "qwerty",
					URI:        &uri.SIP{Addr: uri.Host("example.com")},
					Response:   "587245234b3434cc3412213e5f113a54",
					Algorithm:  "MD5",
					CNonce:     "1q2w3e",
					Opaque:     "zxc",
					QOP:        "auth",
					NonceCount: 5,
					Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
		},
		{
			name: "bearer",
			hdr: &header.Authorization{
				AuthCredentials: &header.BearerCredentials{Token: "QweRTY123"},
			},
		},
		{
			name: "custom",
			hdr: &header.Authorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
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

			var got header.Authorization
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}

			var want *header.Authorization
			if c.hdr != nil {
				want = c.hdr
			} else {
				want = &header.Authorization{}
			}

			if diff := cmp.Diff(&got, want); diff != "" {
				t.Errorf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", &got, want, diff)
			}
		})
	}
}

func TestDigestCredentials_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		crd  *header.DigestCredentials
		want string
	}{
		{"nil", (*header.DigestCredentials)(nil), ""},
		{"zero", &header.DigestCredentials{}, "Digest "},
		{
			"full",
			&header.DigestCredentials{
				Username:   "root",
				Realm:      "example.com",
				Nonce:      "qwerty",
				URI:        &uri.SIP{Addr: uri.Host("example.com")},
				Response:   "587245234b3434cc3412213e5f113a54",
				Algorithm:  "MD5",
				CNonce:     "1q2w3e",
				Opaque:     "zxc",
				QOP:        "auth",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			"Digest algorithm=MD5, cnonce=\"1q2w3e\", nc=00000005, nonce=\"qwerty\", opaque=\"zxc\", " +
				"qop=auth, realm=\"example.com\", response=\"587245234b3434cc3412213e5f113a54\", username=\"root\", " +
				"uri=\"sip:example.com\", p1=abc, p2=\"a b c\"",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.crd.Render(nil); got != c.want {
				t.Errorf("crd.Render(nil) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDigestCredentials_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		crd     *header.DigestCredentials
		wantRes string
		wantErr error
	}{
		{"nil", (*header.DigestCredentials)(nil), "", nil},
		{"zero", &header.DigestCredentials{}, "Digest ", nil},
		{
			"full",
			&header.DigestCredentials{
				Username:   "root",
				Realm:      "example.com",
				Nonce:      "qwerty",
				URI:        &uri.SIP{Addr: uri.Host("example.com")},
				Response:   "587245234b3434cc3412213e5f113a54",
				Algorithm:  "MD5",
				CNonce:     "1q2w3e",
				Opaque:     "zxc",
				QOP:        "auth",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			"Digest algorithm=MD5, cnonce=\"1q2w3e\", nc=00000005, nonce=\"qwerty\", opaque=\"zxc\", " +
				"qop=auth, realm=\"example.com\", response=\"587245234b3434cc3412213e5f113a54\", username=\"root\", " +
				"uri=\"sip:example.com\", p1=abc, p2=\"a b c\"",
			nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			_, err := c.crd.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("crd.RenderTo(sb, nil) error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}
			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestDigestCredentials_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		crd  *header.DigestCredentials
		want string
	}{
		{"nil", (*header.DigestCredentials)(nil), ""},
		{"zero", &header.DigestCredentials{}, "Digest "},
		{
			"full",
			&header.DigestCredentials{
				Username:   "root",
				Realm:      "example.com",
				Nonce:      "qwerty",
				URI:        &uri.SIP{Addr: uri.Host("example.com")},
				Response:   "587245234b3434cc3412213e5f113a54",
				Algorithm:  "MD5",
				CNonce:     "1q2w3e",
				Opaque:     "zxc",
				QOP:        "auth",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			"Digest algorithm=MD5, cnonce=\"1q2w3e\", nc=00000005, nonce=\"qwerty\", opaque=\"zxc\", " +
				"qop=auth, realm=\"example.com\", response=\"587245234b3434cc3412213e5f113a54\", username=\"root\", " +
				"uri=\"sip:example.com\", p1=abc, p2=\"a b c\"",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.crd.String(); got != c.want {
				t.Errorf("crd.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDigestCredentials_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		crd  *header.DigestCredentials
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.DigestCredentials)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.DigestCredentials)(nil), (*header.DigestCredentials)(nil), true},
		{"zero ptr to nil ptr", &header.DigestCredentials{}, (*header.DigestCredentials)(nil), false},
		{"zero ptr to zero", &header.DigestCredentials{}, header.DigestCredentials{}, true},
		{
			"not match 1",
			&header.DigestCredentials{
				Username:   "root",
				Realm:      "example.com",
				Nonce:      "qwerty",
				URI:        &uri.SIP{Addr: uri.Host("example.com")},
				Response:   "587245234b3434cc3412213e5f113a54",
				Algorithm:  "MD5",
				CNonce:     "1q2w3e",
				Opaque:     "zxc",
				QOP:        "auth",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			&header.DigestCredentials{
				Username:   "ROOT",
				Realm:      "example.com",
				Nonce:      "qwerty",
				URI:        &uri.SIP{Addr: uri.Host("example.com")},
				Response:   "587245234b3434cc3412213e5f113a54",
				Algorithm:  "MD5",
				CNonce:     "1q2w3e",
				Opaque:     "zxc",
				QOP:        "auth",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			false,
		},
		{
			"not match 2",
			&header.DigestCredentials{
				Username:   "root",
				Realm:      "example.com",
				Nonce:      "qwerty",
				URI:        &uri.SIP{Addr: uri.Host("example.com")},
				Response:   "587245234b3434cc3412213e5f113a54",
				Algorithm:  "MD5",
				CNonce:     "1q2w3e",
				Opaque:     "zxc",
				QOP:        "auth",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			&header.DigestCredentials{
				Username:   "root",
				Realm:      "example.com",
				Nonce:      "QWERTY",
				URI:        &uri.SIP{Addr: uri.Host("example.com")},
				Response:   "587245234b3434cc3412213e5f113a54",
				Algorithm:  "MD5",
				CNonce:     "1q2w3e",
				Opaque:     "zxc",
				QOP:        "auth",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			false,
		},
		{
			"not match 3",
			&header.DigestCredentials{
				Username:   "root",
				Realm:      "example.com",
				Nonce:      "qwerty",
				URI:        &uri.SIP{Addr: uri.Host("example.com")},
				Response:   "ABC123QWE",
				Algorithm:  "MD5",
				CNonce:     "1q2w3e",
				Opaque:     "zxc",
				QOP:        "auth",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			&header.DigestCredentials{
				Username:   "root",
				Realm:      "example.com",
				Nonce:      "qwerty",
				URI:        &uri.SIP{Addr: uri.Host("example.com")},
				Response:   "abc123qwe",
				Algorithm:  "MD5",
				CNonce:     "1q2w3e",
				Opaque:     "zxc",
				QOP:        "auth",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			false,
		},
		{
			"not match 4",
			&header.DigestCredentials{
				Username:   "root",
				Realm:      "example.com",
				Nonce:      "qwerty",
				URI:        &uri.SIP{Addr: uri.Host("example.com")},
				Response:   "587245234b3434cc3412213e5f113a54",
				Algorithm:  "MD5",
				CNonce:     "1q2w3e",
				Opaque:     "zxc",
				QOP:        "auth",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			&header.DigestCredentials{
				Username:   "root",
				Realm:      "example.com",
				Nonce:      "qwerty",
				URI:        &uri.SIP{Addr: uri.Host("example.com")},
				Response:   "587245234b3434cc3412213e5f113a54",
				Algorithm:  "MD5",
				CNonce:     "1Q2W3E",
				Opaque:     "zxc",
				QOP:        "auth",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			false,
		},
		{
			"not match 5",
			&header.DigestCredentials{
				Username:   "root",
				Realm:      "example.com",
				Nonce:      "qwerty",
				URI:        &uri.SIP{Addr: uri.Host("localhost")},
				Response:   "587245234b3434cc3412213e5f113a54",
				Algorithm:  "MD5",
				CNonce:     "1q2w3e",
				Opaque:     "zxc",
				QOP:        "auth",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			&header.DigestCredentials{
				Username:   "root",
				Realm:      "example.com",
				Nonce:      "qwerty",
				URI:        &uri.SIP{Addr: uri.Host("example.com")},
				Response:   "587245234b3434cc3412213e5f113a54",
				Algorithm:  "MD5",
				CNonce:     "1q2w3e",
				Opaque:     "zxc",
				QOP:        "auth",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			false,
		},
		{
			"match",
			&header.DigestCredentials{
				Username:   "root",
				Realm:      "Example.COM",
				Nonce:      "qwerty",
				URI:        &uri.SIP{Addr: uri.Host("example.COM")},
				Response:   "587245234b3434cc3412213e5f113a54",
				Algorithm:  "MD5",
				CNonce:     "1q2w3e",
				Opaque:     "zxc",
				QOP:        "auth",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`).Set("p3", "def"),
			},
			&header.DigestCredentials{
				Username:   "root",
				Realm:      "example.com",
				Nonce:      "qwerty",
				URI:        &uri.SIP{Addr: uri.Host("example.com")},
				Response:   "587245234b3434cc3412213e5f113a54",
				Algorithm:  "md5",
				CNonce:     "1q2w3e",
				Opaque:     "zxc",
				QOP:        "AUTH",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "ABC").Set("p2", `"a b c"`),
			},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.crd.Equal(c.val); got != c.want {
				t.Errorf("crd.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestDigestCredentials_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		crd  *header.DigestCredentials
		want bool
	}{
		{"nil", (*header.DigestCredentials)(nil), false},
		{"zero", &header.DigestCredentials{}, false},
		{"invalid 1", &header.DigestCredentials{Username: "root"}, false},
		{"invalid 2", &header.DigestCredentials{Username: "root", Realm: "example.com"}, false},
		{"invalid 3", &header.DigestCredentials{Username: "root", Realm: "example.com", Response: "123"}, false},
		{
			"valid",
			&header.DigestCredentials{
				Username:   "root",
				Realm:      "example.com",
				Nonce:      "qwerty",
				URI:        &uri.SIP{Addr: uri.Host("example.com")},
				Response:   "587245234b3434cc3412213e5f113a54",
				Algorithm:  "md5",
				CNonce:     "1q2w3e",
				Opaque:     "zxc",
				QOP:        "auth",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "ABC").Set("p2", `"a b c"`),
			},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.crd.IsValid(); got != c.want {
				t.Errorf("crd.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestDigestCredentials_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		crd  *header.DigestCredentials
	}{
		{"nil", nil},
		{"zero", &header.DigestCredentials{}},
		{
			"full",
			&header.DigestCredentials{
				Username:   "root",
				Realm:      "example.com",
				Nonce:      "qwerty",
				URI:        &uri.SIP{Addr: uri.Host("example.com")},
				Response:   "587245234b3434cc3412213e5f113a54",
				Algorithm:  "md5",
				CNonce:     "1q2w3e",
				Opaque:     "zxc",
				QOP:        "auth",
				NonceCount: 5,
				Params:     make(header.Values).Set("p1", "ABC").Set("p2", `"a b c"`),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.crd.Clone()
			if c.crd == nil {
				if got != nil {
					t.Errorf("crd.Clone() = %+v, want nil", got)
				}
				return
			}
			if diff := cmp.Diff(got, c.crd); diff != "" {
				t.Errorf("crd.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.crd, diff)
			}
		})
	}
}

func TestBearerCredentials_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		crd  *header.BearerCredentials
		want string
	}{
		{"nil", (*header.BearerCredentials)(nil), ""},
		{"zero", &header.BearerCredentials{}, "Bearer "},
		{"full", &header.BearerCredentials{Token: "qwerty"}, "Bearer qwerty"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.crd.Render(nil); got != c.want {
				t.Errorf("crd.Render(nil) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBearerCredentials_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		crd     *header.BearerCredentials
		wantRes string
		wantErr error
	}{
		{"nil", (*header.BearerCredentials)(nil), "", nil},
		{"zero", &header.BearerCredentials{}, "Bearer ", nil},
		{"full", &header.BearerCredentials{Token: "qwerty"}, "Bearer qwerty", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			_, err := c.crd.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("crd.RenderTo(sb, nil) error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}
			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestBearerCredentials_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		crd  *header.BearerCredentials
		want string
	}{
		{"nil", (*header.BearerCredentials)(nil), ""},
		{"zero", &header.BearerCredentials{}, "Bearer "},
		{"full", &header.BearerCredentials{Token: "qwerty"}, "Bearer qwerty"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.crd.String(); got != c.want {
				t.Errorf("crd.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBearerCredentials_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		crd  *header.BearerCredentials
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.BearerCredentials)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.BearerCredentials)(nil), (*header.BearerCredentials)(nil), true},
		{"zero ptr to nil ptr", &header.BearerCredentials{}, (*header.BearerCredentials)(nil), false},
		{"zero ptr to zero", &header.BearerCredentials{}, header.BearerCredentials{}, true},
		{"not match 1", &header.BearerCredentials{Token: "qwerty"}, header.BearerCredentials{}, false},
		{"not match 2", &header.BearerCredentials{Token: "qwerty"}, &header.BearerCredentials{Token: "QWERTY"}, false},
		{"match", &header.BearerCredentials{Token: "qwerty"}, &header.BearerCredentials{Token: "qwerty"}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.crd.Equal(c.val); got != c.want {
				t.Errorf("crd.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBearerCredentials_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		crd  *header.BearerCredentials
		want bool
	}{
		{"nil", (*header.BearerCredentials)(nil), false},
		{"zero", &header.BearerCredentials{}, false},
		{"invalid", &header.BearerCredentials{Token: ""}, false},
		{"valid", &header.BearerCredentials{Token: "qwerty"}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.crd.IsValid(); got != c.want {
				t.Errorf("crd.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBearerCredentials_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		crd  *header.BearerCredentials
	}{
		{"nil", nil},
		{"zero", &header.BearerCredentials{}},
		{"full", &header.BearerCredentials{Token: "qwerty"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.crd.Clone()
			if c.crd == nil {
				if got != nil {
					t.Errorf("crd.Clone() = %+v, want nil", got)
				}
				return
			}
			if diff := cmp.Diff(got, c.crd); diff != "" {
				t.Errorf("crd.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.crd, diff)
			}
		})
	}
}

func TestAnyCredentials_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		crd  *header.AnyCredentials
		want string
	}{
		{"nil", (*header.AnyCredentials)(nil), ""},
		{"zero", &header.AnyCredentials{}, " "},
		{
			"full",
			&header.AnyCredentials{
				Scheme: "Custom",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			"Custom p1=abc, p2=\"a b c\"",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.crd.Render(nil); got != c.want {
				t.Errorf("crd.Render(nil) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAnyCredentials_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		crd     *header.AnyCredentials
		wantRes string
		wantErr error
	}{
		{"nil", (*header.AnyCredentials)(nil), "", nil},
		{"zero", &header.AnyCredentials{}, " ", nil},
		{
			"full",
			&header.AnyCredentials{
				Scheme: "Custom",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			"Custom p1=abc, p2=\"a b c\"",
			nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			_, err := c.crd.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("crd.RenderTo(sb, nil) error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}
			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestAnyCredentials_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		crd  *header.AnyCredentials
		want string
	}{
		{"nil", (*header.AnyCredentials)(nil), ""},
		{"zero", &header.AnyCredentials{}, " "},
		{
			"full",
			&header.AnyCredentials{
				Scheme: "Custom",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			"Custom p1=abc, p2=\"a b c\"",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.crd.String(); got != c.want {
				t.Errorf("crd.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAnyCredentials_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		crd  *header.AnyCredentials
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.AnyCredentials)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.AnyCredentials)(nil), (*header.AnyCredentials)(nil), true},
		{"zero ptr to nil ptr", &header.AnyCredentials{}, (*header.AnyCredentials)(nil), false},
		{"zero ptr to zero", &header.AnyCredentials{}, header.AnyCredentials{}, true},
		{
			"not match 1",
			&header.AnyCredentials{
				Scheme: "Custom",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			&header.AnyCredentials{
				Scheme: "Qwerty",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			false,
		},
		{
			"not match 2",
			&header.AnyCredentials{
				Scheme: "Custom",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			&header.AnyCredentials{
				Scheme: "Custom",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"zxc"`),
			},
			false,
		},
		{
			"match",
			&header.AnyCredentials{
				Scheme: "custom",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			&header.AnyCredentials{
				Scheme: "Custom",
				Params: make(header.Values).Set("p1", "ABC").Set("p2", `"a b c"`),
			},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.crd.Equal(c.val); got != c.want {
				t.Errorf("crd.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAnyCredentials_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		crd  *header.AnyCredentials
		want bool
	}{
		{"nil", (*header.AnyCredentials)(nil), false},
		{"zero", &header.AnyCredentials{}, false},
		{"invalid 1", &header.AnyCredentials{Scheme: ""}, false},
		{"invalid 2", &header.AnyCredentials{Scheme: "qwerty"}, false},
		{"valid", &header.AnyCredentials{Scheme: "qwerty", Params: make(header.Values).Set("p1", "abc")}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.crd.IsValid(); got != c.want {
				t.Errorf("crd.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAnyCredentials_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		crd  *header.AnyCredentials
	}{
		{"nil", nil},
		{"zero", &header.AnyCredentials{}},
		{"full", &header.AnyCredentials{Scheme: "qwerty", Params: make(header.Values).Set("p1", "abc")}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.crd.Clone()
			if c.crd == nil {
				if got != nil {
					t.Errorf("crd.Clone() = %+v, want nil", got)
				}
				return
			}
			if diff := cmp.Diff(got, c.crd); diff != "" {
				t.Errorf("crd.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.crd, diff)
			}
		})
	}
}
