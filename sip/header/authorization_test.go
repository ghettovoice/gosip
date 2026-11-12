package header_test

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

// renderSpyURI is a uri.URI test double that records render options and can
// fail rendering; DigestCredentials.RenderTo must propagate both faithfully.
type renderSpyURI struct {
	text     string
	err      error
	lastOpts []shared.RenderOptions
}

func (u *renderSpyURI) Scheme() string { return "sip" }

func (u *renderSpyURI) RenderTo(w io.Writer, opts ...shared.RenderOptions) (int, error) {
	u.lastOpts = opts
	if u.err != nil {
		return 0, u.err
	}
	return fmt.Fprint(w, u.text)
}

func (u *renderSpyURI) Clone() uri.URI {
	clone := *u
	return &clone
}

func (u *renderSpyURI) IsValid() bool { return u.text != "" }

func (u *renderSpyURI) Equal(val any) bool {
	other, ok := val.(*renderSpyURI)
	return ok && u.text == other.text
}

func TestAuthorization_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
					URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.Render(); got != tt.want {
				t.Errorf("hdr.Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuthorization_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

func TestAuthorization_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.String(); got != tt.want {
				t.Errorf("hdr.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuthorization_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
					URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hdr.Equal(tt.val); got != tt.want {
				t.Errorf("hdr.Equal(val) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuthorization_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
		{
			"valid sha-256 digest",
			&header.Authorization{
				AuthCredentials: &header.DigestCredentials{
					Username:  "root",
					Realm:     "example.com",
					Nonce:     "qwerty",
					URI:       &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
					Algorithm: "SHA-256",
					Response:  "753927fa0e85d155564e2e272a28d1802ca10daf4496794697cf8db5856cb6c1",
				},
			},
			true,
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

func TestAuthorization_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
					URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.hdr.Clone()
			if tt.hdr == nil {
				if got != nil {
					t.Errorf("hdr.Clone() = %+v, want nil", got)
				}
				return
			}
			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Errorf("hdr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}

func TestAuthorization_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  *header.Authorization
		want string
	}{
		{"nil", nil, "null"},
		{"zero", &header.Authorization{}, `{"name":"Authorization","value":""}`},
		{
			"digest",
			&header.Authorization{
				AuthCredentials: &header.DigestCredentials{
					Username:   "root",
					Realm:      "example.com",
					Nonce:      "qwerty",
					URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
					Response:   "587245234b3434cc3412213e5f113a54",
					Algorithm:  "MD5",
					CNonce:     "1q2w3e",
					Opaque:     "zxc",
					QOP:        "auth",
					NonceCount: 5,
					Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			`{"name":"Authorization","value":"Digest algorithm=MD5, cnonce=\"1q2w3e\", nc=00000005, nonce=\"qwerty\", opaque=\"zxc\", qop=auth, realm=\"example.com\", response=\"587245234b3434cc3412213e5f113a54\", username=\"root\", uri=\"sip:example.com\", p1=abc, p2=\"a b c\""}`,
		},
		{
			"bearer",
			&header.Authorization{
				AuthCredentials: &header.BearerCredentials{Token: "QweRTY123"},
			},
			`{"name":"Authorization","value":"Bearer QweRTY123"}`,
		},
		{
			"custom",
			&header.Authorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			`{"name":"Authorization","value":"Custom p1=abc, p2=\"a b c\""}`,
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

func TestAuthorization_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    *header.Authorization
		wantErr bool
	}{
		{"null", "null", nil, false},
		{"empty object", `{}`, nil, true},
		{"empty name", `{"value":"Bearer QweRTY123"}`, nil, true},
		{"empty value", `{"name":"Authorization","value":""}`, &header.Authorization{}, false},
		{"invalid json", `{"name":"Authorization","value":`, &header.Authorization{}, true},
		{"wrong header", `{"name":"From","value":"\"Alice\" <sip:alice@example.com>"}`, &header.Authorization{}, true},
		{
			"digest",
			`{"name":"Authorization","value":"Digest algorithm=MD5, cnonce=\"1q2w3e\", nc=00000005, nonce=\"qwerty\", opaque=\"zxc\", qop=auth, realm=\"example.com\", response=\"587245234b3434cc3412213e5f113a54\", username=\"root\", uri=\"sip:example.com\", p1=abc, p2=\"a b c\""}`,
			&header.Authorization{
				AuthCredentials: &header.DigestCredentials{
					Username:   "root",
					Realm:      "example.com",
					Nonce:      "qwerty",
					URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
					Response:   "587245234b3434cc3412213e5f113a54",
					Algorithm:  "MD5",
					CNonce:     "1q2w3e",
					Opaque:     "zxc",
					QOP:        "auth",
					NonceCount: 5,
					Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			false,
		},
		{
			"bearer",
			`{"name":"Authorization","value":"Bearer QweRTY123"}`,
			&header.Authorization{
				AuthCredentials: &header.BearerCredentials{Token: "QweRTY123"},
			},
			false,
		},
		{
			"custom",
			`{"name":"Authorization","value":"Custom p1=abc, p2=\"a b c\""}`,
			&header.Authorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got *header.Authorization
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

func TestAuthorization_RoundTripJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
					URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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
		{"bearer", &header.Authorization{AuthCredentials: &header.BearerCredentials{Token: "QweRTY123"}}},
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tt.hdr)
			if err != nil {
				t.Fatalf("json.Marshal(hdr) error = %v, want nil", err)
			}

			var got *header.Authorization
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(data, got) error = %v, want nil", err)
			}
			if diff := cmp.Diff(got, tt.hdr); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.hdr, diff)
			}
		})
	}
}

func TestDigestCredentials_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.crd.Render(); got != tt.want {
				t.Errorf("crd.Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDigestCredentials_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			_, err := tt.crd.RenderTo(&sb)
			if diff := cmp.Diff(err, tt.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("crd.RenderTo(&sb) error = %v, want %v\ndiff (-got +want):\n%v", err, tt.wantErr, diff)
			}
			if got := sb.String(); got != tt.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, tt.wantRes)
			}
		})
	}
}

func TestDigestCredentials_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.crd.String(); got != tt.want {
				t.Errorf("crd.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDigestCredentials_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("localhost")},
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.COM")},
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.crd.Equal(tt.val); got != tt.want {
				t.Errorf("crd.Equal(val) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDigestCredentials_IsValid(t *testing.T) {
	t.Parallel()

	const (
		resp32       = "587245234b3434cc3412213e5f113a54"
		resp64       = "753927fa0e85d155564e2e272a28d1802ca10daf4496794697cf8db5856cb6c1"
		resp64Upper  = "753927FA0E85D155564E2E272A28D1802CA10DAF4496794697CF8DB5856CB6C1"
		resp40       = "0123456789abcdef0123456789abcdef01234567"
		resp16       = "0123456789abcdef"
		resp33       = "0123456789abcdef0123456789abcdef012"
		resp32NonHex = "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
	)
	mkDigest := func(algorithm, response string) *header.DigestCredentials {
		return &header.DigestCredentials{
			Username:  "root",
			Realm:     "example.com",
			Nonce:     "qwerty",
			URI:       &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
			Algorithm: algorithm,
			Response:  response,
		}
	}

	tests := []struct {
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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

		// RFC 8760 algorithm-aware response lengths. Algorithm names are
		// case-insensitive; an omitted algorithm means MD5. Unknown
		// algorithm tokens keep even-length hexadecimal syntax of at least
		// 32 digits: structural validity is not computational support.
		{"md5", mkDigest("MD5", resp32), true},
		{"md5-sess", mkDigest("MD5-sess", resp32), true},
		{"omitted algorithm is md5", mkDigest("", resp32), true},
		{"sha-256", mkDigest("SHA-256", resp64), true},
		{"sha-256-sess", mkDigest("SHA-256-sess", resp64), true},
		{"sha-512-256", mkDigest("SHA-512-256", resp64), true},
		{"sha-512-256-sess", mkDigest("SHA-512-256-sess", resp64), true},
		{"algorithm name case-insensitive", mkDigest("Sha-256", resp64), true},
		{"uppercase hexadecimal", mkDigest("SHA-256", resp64Upper), true},
		{"extension algorithm 40 digits", mkDigest("CustomHash", resp40), true},
		{"extension algorithm 64 digits", mkDigest("CustomHash", resp64), true},

		{"empty response placeholder is not proof", mkDigest("SHA-256", ""), false},
		{"md5 length mismatch", mkDigest("MD5", resp64), false},
		{"sha-256 length mismatch", mkDigest("SHA-256", resp32), false},
		{"non-hex of valid length", mkDigest("MD5", resp32NonHex), false},
		{"odd extension response", mkDigest("CustomHash", resp33), false},
		{"short extension response", mkDigest("CustomHash", resp16), false},
		{"non-token algorithm", mkDigest("SHA@256", resp64), false},
		{
			"missing URI",
			&header.DigestCredentials{
				Username: "root", Realm: "example.com", Nonce: "qwerty",
				Algorithm: "SHA-256", Response: resp64,
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.crd.IsValid(); got != tt.want {
				t.Errorf("crd.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDigestCredentials_URIText(t *testing.T) {
	t.Parallel()

	mkDigestHeader := func(hdrName, uriText string) string {
		return hdrName + `: Digest username="root", realm="example.com", nonce="qwerty",` +
			` uri="` + uriText + `", response="587245234b3434cc3412213e5f113a54"`
	}
	parseDigest := func(t *testing.T, hdrName, uriText string) (header.Header, *header.DigestCredentials) {
		t.Helper()

		input := mkDigestHeader(hdrName, uriText)
		got, err := header.Parse(input)
		if err != nil {
			t.Fatalf("header.Parse(%q) error = %v, want nil", input, err)
		}
		var crd *header.DigestCredentials
		switch hdr := got.(type) {
		case *header.Authorization:
			crd, _ = hdr.AuthCredentials.(*header.DigestCredentials)
		case *header.ProxyAuthorization:
			crd, _ = hdr.AuthCredentials.(*header.DigestCredentials)
		}
		if crd == nil {
			t.Fatalf("header.Parse(%q) credentials = %T, want *header.DigestCredentials", input, got)
		}
		return got, crd
	}

	// The digest response signs the raw Request-URI text, so parsed
	// credentials keep the wire spelling even when it differs from the
	// typed URI rendering (canonical parameter order, normalized escapes).
	// A typed URI edit drops the snapshot and falls back to its rendering.
	t.Run("parsed URI text", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			hdrName string
			uriText string
			render  string
		}{
			{"noncanonical param order", "Authorization", "sip:alice@example.com;z=1;a=2", "sip:alice@example.com;a=2;z=1"},
			{"escaped user", "Authorization", "sip:%61lice@example.com", "sip:alice@example.com"},
			{"canonical", "Authorization", "sip:alice@example.com", "sip:alice@example.com"},
			{"noncanonical param order", "Proxy-Authorization", "sip:alice@example.com;z=1;a=2", "sip:alice@example.com;a=2;z=1"},
			{"escaped user", "Proxy-Authorization", "sip:%61lice@example.com", "sip:alice@example.com"},
		}

		for _, tt := range tests {
			t.Run(tt.hdrName+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				hdr, crd := parseDigest(t, tt.hdrName, tt.uriText)

				if got := crd.URIText(); got != tt.uriText {
					t.Errorf("crd.URIText() = %q, want %q", got, tt.uriText)
				}
				if got := shared.Render(crd.URI); got != tt.render {
					t.Errorf("typed URI render = %q, want %q", got, tt.render)
				}
				if rendered := shared.Render(hdr); !strings.Contains(rendered, `uri="`+tt.uriText+`"`) {
					t.Errorf("hdr render = %q, want it to contain signed URI %q", rendered, tt.uriText)
				}
			})
		}
	})

	t.Run("programmatic credentials", func(t *testing.T) {
		t.Parallel()

		crd := &header.DigestCredentials{
			URI: &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("example.com")},
		}
		if got, want := crd.URIText(), shared.Render(crd.URI); got != want {
			t.Errorf("crd.URIText() = %q, want %q", got, want)
		}
	})

	t.Run("nil", func(t *testing.T) {
		t.Parallel()

		if got := (*header.DigestCredentials)(nil).URIText(); got != "" {
			t.Errorf("nil crd.URIText() = %q, want %q", got, "")
		}
		if got := (&header.DigestCredentials{}).URIText(); got != "" {
			t.Errorf("nil URI crd.URIText() = %q, want %q", got, "")
		}
	})

	t.Run("clone keeps and typed edit replaces", func(t *testing.T) {
		t.Parallel()

		_, crd := parseDigest(t, "Authorization", "sip:alice@example.com;z=1;a=2")

		clone, ok := crd.Clone().(*header.DigestCredentials)
		if !ok {
			t.Fatalf("crd.Clone() = %T, want *header.DigestCredentials", crd.Clone())
		}
		if got, want := clone.URIText(), crd.URIText(); got != want {
			t.Fatalf("clone.URIText() = %q, want %q", got, want)
		}

		// Mutating the cloned typed URI falls back to its rendering and
		// leaves the original snapshot intact.
		clone.URI.(*uri.SIP).Addr = uri.MakeHostAddr("example.org")
		if got, want := clone.URIText(), shared.Render(clone.URI); got != want {
			t.Errorf("mutated clone.URIText() = %q, want %q", got, want)
		}
		if got, want := crd.URIText(), "sip:alice@example.com;z=1;a=2"; got != want {
			t.Errorf("crd.URIText() = %q, want %q", got, want)
		}

		// Replacing the typed URI falls back too.
		crd.URI = &uri.SIP{Addr: uri.MakeHostAddr("example.net")}
		if got, want := crd.URIText(), "sip:example.net"; got != want {
			t.Errorf("replaced URI crd.URIText() = %q, want %q", got, want)
		}
	})

	t.Run("JSON round trip", func(t *testing.T) {
		t.Parallel()

		hdr, _ := parseDigest(t, "Authorization", "sip:alice@example.com;z=1;a=2")

		data, err := hdr.(*header.Authorization).MarshalJSON()
		if err != nil {
			t.Fatalf("hdr.MarshalJSON() error = %v, want nil", err)
		}
		var out header.Authorization
		if err := out.UnmarshalJSON(data); err != nil {
			t.Fatalf("out.UnmarshalJSON(%s) error = %v, want nil", data, err)
		}
		outCrd, ok := out.AuthCredentials.(*header.DigestCredentials)
		if !ok {
			t.Fatalf("out.AuthCredentials = %T, want *header.DigestCredentials", out.AuthCredentials)
		}
		if got, want := outCrd.URIText(), "sip:alice@example.com;z=1;a=2"; got != want {
			t.Errorf("outCrd.URIText() = %q, want %q", got, want)
		}
	})

	t.Run("render options and errors propagate", func(t *testing.T) {
		t.Parallel()

		const renderErr errors.Error = "digest URI render failed"
		crd := &header.DigestCredentials{
			Username: "root",
			Realm:    "example.com",
			Nonce:    "qwerty",
			URI:      &renderSpyURI{text: "sip:example.com", err: renderErr},
			Response: "587245234b3434cc3412213e5f113a54",
		}

		var sb strings.Builder
		if _, err := crd.RenderTo(&sb); !errors.Is(err, renderErr) {
			t.Fatalf("crd.RenderTo() error = %v, want %v", err, renderErr)
		}

		spy := &renderSpyURI{text: "sip:example.com"}
		crd.URI = spy
		if _, err := crd.RenderTo(&sb, header.RenderOptions{Compact: true}); err != nil {
			t.Fatalf("crd.RenderTo() error = %v, want nil", err)
		}
		if len(spy.lastOpts) == 0 || !spy.lastOpts[len(spy.lastOpts)-1].Compact {
			t.Fatalf("URI render options = %+v, want Compact forwarded", spy.lastOpts)
		}
		if got := sb.String(); !strings.Contains(got, `uri="sip:example.com"`) {
			t.Errorf("rendered credentials = %q, want it to contain %q", got, `uri="sip:example.com"`)
		}
	})
}

func TestDigestCredentials_RoundTrip(t *testing.T) {
	t.Parallel()

	const (
		resp64 = "753927fa0e85d155564e2e272a28d1802ca10daf4496794697cf8db5856cb6c1"
		resp40 = "0123456789abcdef0123456789abcdef01234567"
	)
	tests := []struct {
		name      string
		algorithm string
		response  string
		uriText   string
	}{
		{"sha-256", "SHA-256", resp64, "sip:alice@example.com;z=1;a=2"},
		{"sha-512-256", "SHA-512-256", resp64, "sip:%61lice@example.com;z=1;a=2"},
		{"extension", "CustomHash", resp40, "sip:%61lice@example.com;z=1;a=2"},
	}

	for _, hdrName := range []string{"Authorization", "Proxy-Authorization"} {
		for _, tt := range tests {
			t.Run(hdrName+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				input := hdrName + `: Digest username="root", realm="example.com", nonce="qwerty",` +
					` uri="` + tt.uriText + `", response="` + tt.response + `", algorithm=` + tt.algorithm

				got, err := header.Parse(input)
				if err != nil {
					t.Fatalf("header.Parse(%q) error = %v, want nil", input, err)
				}

				var crd *header.DigestCredentials
				var roundTrip func(t *testing.T) *header.DigestCredentials
				switch hdr := got.(type) {
				case *header.Authorization:
					crd, _ = hdr.AuthCredentials.(*header.DigestCredentials)
					roundTrip = func(t *testing.T) *header.DigestCredentials {
						t.Helper()

						data, err := hdr.MarshalJSON()
						if err != nil {
							t.Fatalf("hdr.MarshalJSON() error = %v, want nil", err)
						}
						var out header.Authorization
						if err := out.UnmarshalJSON(data); err != nil {
							t.Fatalf("out.UnmarshalJSON(%s) error = %v, want nil", data, err)
						}
						outCrd, _ := out.AuthCredentials.(*header.DigestCredentials)
						return outCrd
					}
				case *header.ProxyAuthorization:
					crd, _ = hdr.AuthCredentials.(*header.DigestCredentials)
					roundTrip = func(t *testing.T) *header.DigestCredentials {
						t.Helper()

						data, err := hdr.MarshalJSON()
						if err != nil {
							t.Fatalf("hdr.MarshalJSON() error = %v, want nil", err)
						}
						var out header.ProxyAuthorization
						if err := out.UnmarshalJSON(data); err != nil {
							t.Fatalf("out.UnmarshalJSON(%s) error = %v, want nil", data, err)
						}
						outCrd, _ := out.AuthCredentials.(*header.DigestCredentials)
						return outCrd
					}
				}
				if crd == nil {
					t.Fatalf("header.Parse(%q) credentials = %T, want *header.DigestCredentials", input, got)
				}

				if crd.Algorithm != tt.algorithm || crd.Response != tt.response {
					t.Fatalf(
						"digest algorithm/response = %q/%q, want %q/%q",
						crd.Algorithm, crd.Response, tt.algorithm, tt.response,
					)
				}
				if got := crd.URIText(); got != tt.uriText {
					t.Errorf("crd.URIText() = %q, want %q", got, tt.uriText)
				}
				if !crd.IsValid() {
					t.Errorf("crd.IsValid() = false, want true")
				}

				// Semantic Equal ignores private raw-text metadata; the
				// clone's own snapshot stays independent after a typed edit.
				clone, ok := crd.Clone().(*header.DigestCredentials)
				if !ok {
					t.Fatalf("crd.Clone() = %T, want *header.DigestCredentials", crd.Clone())
				}
				if !clone.Equal(crd) {
					t.Fatalf("clone.Equal(crd) = false, want true")
				}
				clone.URI.(*uri.SIP).Addr = uri.MakeHostAddr("example.org")
				if got := crd.URIText(); got != tt.uriText {
					t.Errorf("crd.URIText() = %q after clone edit, want %q", got, tt.uriText)
				}

				// Render and re-parse: the signed URI spelling survives.
				reparsed, err := header.Parse(shared.Render(got))
				if err != nil {
					t.Fatalf("header.Parse(rendered) error = %v, want nil", err)
				}
				var reparsedCrd *header.DigestCredentials
				switch hdr := reparsed.(type) {
				case *header.Authorization:
					reparsedCrd, _ = hdr.AuthCredentials.(*header.DigestCredentials)
				case *header.ProxyAuthorization:
					reparsedCrd, _ = hdr.AuthCredentials.(*header.DigestCredentials)
				}
				if reparsedCrd == nil {
					t.Fatalf("reparsed credentials = %T, want *header.DigestCredentials", reparsed)
				}
				if got := reparsedCrd.URIText(); got != tt.uriText {
					t.Errorf("reparsedCrd.URIText() = %q, want %q", got, tt.uriText)
				}

				if outCrd := roundTrip(t); outCrd == nil {
					t.Fatalf("JSON round trip credentials = %T, want *header.DigestCredentials", outCrd)
				} else if got := outCrd.URIText(); got != tt.uriText {
					t.Errorf("outCrd.URIText() = %q, want %q", got, tt.uriText)
				}
			})
		}
	}
}

func TestDigestCredentials_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
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
				URI:        &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.crd.Clone()
			if tt.crd == nil {
				if got != nil {
					t.Errorf("crd.Clone() = %+v, want nil", got)
				}
				return
			}
			if diff := cmp.Diff(got, tt.crd); diff != "" {
				t.Errorf("crd.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.crd, diff)
			}
		})
	}
}

func TestBearerCredentials_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		crd  *header.BearerCredentials
		want string
	}{
		{"nil", (*header.BearerCredentials)(nil), ""},
		{"zero", &header.BearerCredentials{}, "Bearer "},
		{"full", &header.BearerCredentials{Token: "qwerty"}, "Bearer qwerty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.crd.Render(); got != tt.want {
				t.Errorf("crd.Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBearerCredentials_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		crd     *header.BearerCredentials
		wantRes string
		wantErr error
	}{
		{"nil", (*header.BearerCredentials)(nil), "", nil},
		{"zero", &header.BearerCredentials{}, "Bearer ", nil},
		{"full", &header.BearerCredentials{Token: "qwerty"}, "Bearer qwerty", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			_, err := tt.crd.RenderTo(&sb)
			if diff := cmp.Diff(err, tt.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("crd.RenderTo(&sb) error = %v, want %v\ndiff (-got +want):\n%v", err, tt.wantErr, diff)
			}
			if got := sb.String(); got != tt.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, tt.wantRes)
			}
		})
	}
}

func TestBearerCredentials_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		crd  *header.BearerCredentials
		want string
	}{
		{"nil", (*header.BearerCredentials)(nil), ""},
		{"zero", &header.BearerCredentials{}, "Bearer "},
		{"full", &header.BearerCredentials{Token: "qwerty"}, "Bearer qwerty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.crd.String(); got != tt.want {
				t.Errorf("crd.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBearerCredentials_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.crd.Equal(tt.val); got != tt.want {
				t.Errorf("crd.Equal(val) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBearerCredentials_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		crd  *header.BearerCredentials
		want bool
	}{
		{"nil", (*header.BearerCredentials)(nil), false},
		{"zero", &header.BearerCredentials{}, false},
		{"invalid", &header.BearerCredentials{Token: ""}, false},
		{"valid", &header.BearerCredentials{Token: "qwerty"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.crd.IsValid(); got != tt.want {
				t.Errorf("crd.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBearerCredentials_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		crd  *header.BearerCredentials
	}{
		{"nil", nil},
		{"zero", &header.BearerCredentials{}},
		{"full", &header.BearerCredentials{Token: "qwerty"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.crd.Clone()
			if tt.crd == nil {
				if got != nil {
					t.Errorf("crd.Clone() = %+v, want nil", got)
				}
				return
			}
			if diff := cmp.Diff(got, tt.crd); diff != "" {
				t.Errorf("crd.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.crd, diff)
			}
		})
	}
}

func TestAnyCredentials_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.crd.Render(); got != tt.want {
				t.Errorf("crd.Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAnyCredentials_RenderTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			_, err := tt.crd.RenderTo(&sb)
			if diff := cmp.Diff(err, tt.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("crd.RenderTo(&sb) error = %v, want %v\ndiff (-got +want):\n%v", err, tt.wantErr, diff)
			}
			if got := sb.String(); got != tt.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, tt.wantRes)
			}
		})
	}
}

func TestAnyCredentials_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.crd.String(); got != tt.want {
				t.Errorf("crd.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAnyCredentials_Equal(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.crd.Equal(tt.val); got != tt.want {
				t.Errorf("crd.Equal(val) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAnyCredentials_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.crd.IsValid(); got != tt.want {
				t.Errorf("crd.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAnyCredentials_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		crd  *header.AnyCredentials
	}{
		{"nil", nil},
		{"zero", &header.AnyCredentials{}},
		{"full", &header.AnyCredentials{Scheme: "qwerty", Params: make(header.Values).Set("p1", "abc")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.crd.Clone()
			if tt.crd == nil {
				if got != nil {
					t.Errorf("crd.Clone() = %+v, want nil", got)
				}
				return
			}
			if diff := cmp.Diff(got, tt.crd); diff != "" {
				t.Errorf("crd.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, tt.crd, diff)
			}
		})
	}
}
