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

func TestProxyAuthenticate_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ProxyAuthenticate
		want string
	}{
		{"nil", nil, ""},
		{"zero", &header.ProxyAuthenticate{}, "Proxy-Authenticate: "},
		{
			"digest",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.DigestChallenge{
					Realm: "atlanta.com",
					Domain: []uri.URI{
						&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
						&uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
						&uri.Any{URL: url.URL{Path: "/a/b/c"}},
					},
					QOP:       []string{"auth", "auth-int"},
					Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
					Stale:     true,
					Algorithm: "MD5",
					Opaque:    "qwerty",
					Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"Proxy-Authenticate: Digest algorithm=MD5, nonce=\"f84f1cec41e6cbe5aea9c8e88d359\", " +
				"opaque=\"qwerty\", qop=\"auth,auth-int\", realm=\"atlanta.com\", stale=true, " +
				"domain=\"sip:ss1.carrier.com http://example.com /a/b/c\", p1=abc, p2=\"a b c\"",
		},
		{
			"bearer",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.BearerChallenge{
					Realm:       "atlanta.com",
					Scope:       "abc",
					AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
					Error:       "qwerty",
					Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"Proxy-Authenticate: Bearer error=\"qwerty\", realm=\"atlanta.com\", scope=\"abc\", " +
				"authz_server=\"http://example.com\", p1=abc, p2=\"a b c\"",
		},
		{
			"custom",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"Proxy-Authenticate: Custom p1=abc, p2=\"a b c\"",
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

func TestProxyAuthenticate_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.ProxyAuthenticate
		wantRes string
		wantErr error
	}{
		{"nil", nil, "", nil},
		{"zero", &header.ProxyAuthenticate{}, "Proxy-Authenticate: ", nil},
		{
			"custom",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"Proxy-Authenticate: Custom p1=abc, p2=\"a b c\"",
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

func TestProxyAuthenticate_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ProxyAuthenticate
		want string
	}{
		{"nil", (*header.ProxyAuthenticate)(nil), ""},
		{"zero", &header.ProxyAuthenticate{}, ""},
		{
			"custom",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
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

func TestProxyAuthenticate_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ProxyAuthenticate
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.ProxyAuthenticate)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.ProxyAuthenticate)(nil), (*header.ProxyAuthenticate)(nil), true},
		{"zero ptr to nil ptr", &header.ProxyAuthenticate{}, (*header.ProxyAuthenticate)(nil), false},
		{"zero to zero", &header.ProxyAuthenticate{}, header.ProxyAuthenticate{}, true},
		{
			"not match 1",
			&header.ProxyAuthenticate{},
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "Qwerty",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			false,
		},
		{
			"not match 2",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.DigestChallenge{
					Realm: "atlanta.com",
					Domain: []uri.URI{
						&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
						&uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
						&uri.Any{URL: url.URL{Path: "/a/b/c"}},
					},
					QOP:       []string{"auth", "auth-int"},
					Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
					Stale:     true,
					Algorithm: "MD5",
					Opaque:    "qwerty",
					Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			&header.ProxyAuthenticate{
				AuthChallenge: &header.BearerChallenge{
					Realm:       "atlanta.com",
					Scope:       "abc",
					AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
					Error:       "qwerty",
					Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			false,
		},
		{
			"match",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
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

func TestProxyAuthenticate_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ProxyAuthenticate
		want bool
	}{
		{"nil", (*header.ProxyAuthenticate)(nil), false},
		{"zero", &header.ProxyAuthenticate{}, false},
		{
			"invalid 1",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.DigestChallenge{Realm: "ATLANTA.com"},
			},
			false,
		},
		{"invalid 2", &header.ProxyAuthenticate{AuthChallenge: &header.BearerChallenge{}}, false},
		{"invalid 3", &header.ProxyAuthenticate{AuthChallenge: (*header.AnyChallenge)(nil)}, false},
		{
			"valid",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
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

func TestProxyAuthenticate_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.ProxyAuthenticate
	}{
		{"nil", nil},
		{"zero", &header.ProxyAuthenticate{}},
		{
			"digest",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.DigestChallenge{
					Realm: "ATLANTA.com",
					Domain: []uri.URI{
						&uri.SIP{Addr: uri.Host("SS1.CARRIER.COM")},
						&uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
						&uri.Any{URL: url.URL{Path: "/a/b/c"}},
					},
					QOP:       []string{"auth", "auth-int"},
					Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
					Stale:     true,
					Algorithm: "md5",
					Opaque:    "qwerty",
					Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
		},
		{
			"bearer",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.BearerChallenge{
					Realm:       "atlanta.com",
					Scope:       "abc",
					AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
					Error:       "qwerty",
					Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
		},
		{
			"custom",
			&header.ProxyAuthenticate{
				AuthChallenge: &header.AnyChallenge{
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
