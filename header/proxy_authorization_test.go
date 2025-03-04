package header_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestProxyAuthorization_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ProxyAuthorization
		want string
	}{
		{"nil", nil, ""},
		{"zero", &header.ProxyAuthorization{}, "Proxy-Authorization: "},
		{
			"digest",
			&header.ProxyAuthorization{
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
			"Proxy-Authorization: Digest algorithm=MD5, cnonce=\"1q2w3e\", nc=00000005, nonce=\"qwerty\", opaque=\"zxc\", " +
				"qop=auth, realm=\"example.com\", response=\"587245234b3434cc3412213e5f113a54\", username=\"root\", " +
				"uri=\"sip:example.com\", p1=abc, p2=\"a b c\"",
		},
		{
			"bearer",
			&header.ProxyAuthorization{
				AuthCredentials: &header.BearerCredentials{Token: "QweRTY123"},
			},
			"Proxy-Authorization: Bearer QweRTY123",
		},
		{
			"custom",
			&header.ProxyAuthorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"Proxy-Authorization: Custom p1=abc, p2=\"a b c\"",
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

func TestProxyAuthorization_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.ProxyAuthorization
		wantRes string
		wantErr error
	}{
		{"nil", nil, "", nil},
		{"zero", &header.ProxyAuthorization{}, "Proxy-Authorization: ", nil},
		{
			"custom",
			&header.ProxyAuthorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"Proxy-Authorization: Custom p1=abc, p2=\"a b c\"",
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

func TestProxyAuthorization_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ProxyAuthorization
		want string
	}{
		{"nil", nil, ""},
		{"zero", &header.ProxyAuthorization{}, ""},
		{
			"custom",
			&header.ProxyAuthorization{
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

func TestProxyAuthorization_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ProxyAuthorization
		val  any
		want bool
	}{
		{"nil ptr to nil", nil, nil, false},
		{"nil ptr to nil ptr", nil, (*header.ProxyAuthorization)(nil), true},
		{"zero ptr to nil ptr", &header.ProxyAuthorization{}, (*header.ProxyAuthorization)(nil), false},
		{"zero to zero", &header.ProxyAuthorization{}, header.ProxyAuthorization{}, true},
		{
			"not match 1",
			&header.ProxyAuthorization{},
			&header.ProxyAuthorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Qwerty",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			false,
		},
		{
			"not match 2",
			&header.ProxyAuthorization{
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
			&header.ProxyAuthorization{
				AuthCredentials: &header.BearerCredentials{
					Token: "QwertY",
				},
			},
			false,
		},
		{
			"match",
			&header.ProxyAuthorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			&header.ProxyAuthorization{
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

func TestProxyAuthorization_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ProxyAuthorization
		want bool
	}{
		{"nil", nil, false},
		{"zero", &header.ProxyAuthorization{}, false},
		{
			"invalid 1",
			&header.ProxyAuthorization{
				AuthCredentials: &header.DigestCredentials{
					Username: "root",
					Response: "587245234b3434cc3412213e5f113a54",
				},
			},
			false,
		},
		{"invalid 2", &header.ProxyAuthorization{AuthCredentials: &header.BearerCredentials{}}, false},
		{"invalid 3", &header.ProxyAuthorization{AuthCredentials: (*header.AnyCredentials)(nil)}, false},
		{
			"valid",
			&header.ProxyAuthorization{
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

func TestProxyAuthorization_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ProxyAuthorization
	}{
		{"nil", nil},
		{"zero", &header.ProxyAuthorization{}},
		{
			"digest",
			&header.ProxyAuthorization{
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
			&header.ProxyAuthorization{
				AuthCredentials: &header.BearerCredentials{
					Token: "QweRTY123",
				},
			},
		},
		{
			"custom",
			&header.ProxyAuthorization{
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
