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

func TestWWWAuthenticate_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.WWWAuthenticate
		want string
	}{
		{"nil", (*header.WWWAuthenticate)(nil), ""},
		{"zero", &header.WWWAuthenticate{}, "WWW-Authenticate: "},
		{
			"digest",
			&header.WWWAuthenticate{
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
			"WWW-Authenticate: Digest algorithm=MD5, nonce=\"f84f1cec41e6cbe5aea9c8e88d359\", " +
				"opaque=\"qwerty\", qop=\"auth,auth-int\", realm=\"atlanta.com\", stale=true, " +
				"domain=\"sip:ss1.carrier.com http://example.com /a/b/c\", p1=abc, p2=\"a b c\"",
		},
		{
			"bearer",
			&header.WWWAuthenticate{
				AuthChallenge: &header.BearerChallenge{
					Realm:       "atlanta.com",
					Scope:       "abc",
					AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
					Error:       "qwerty",
					Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"WWW-Authenticate: Bearer error=\"qwerty\", realm=\"atlanta.com\", scope=\"abc\", " +
				"authz_server=\"http://example.com\", p1=abc, p2=\"a b c\"",
		},
		{
			"custom",
			&header.WWWAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"WWW-Authenticate: Custom p1=abc, p2=\"a b c\"",
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

func TestWWWAuthenticate_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		hdr     *header.WWWAuthenticate
		wantRes string
		wantErr error
	}{
		{"nil", (*header.WWWAuthenticate)(nil), "", nil},
		{"zero", &header.WWWAuthenticate{}, "WWW-Authenticate: ", nil},
		{
			"custom",
			&header.WWWAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			"WWW-Authenticate: Custom p1=abc, p2=\"a b c\"",
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

func TestWWWAuthenticate_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.WWWAuthenticate
		want string
	}{
		{"nil", (*header.WWWAuthenticate)(nil), ""},
		{"zero", &header.WWWAuthenticate{}, ""},
		{
			"custom",
			&header.WWWAuthenticate{
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

func TestWWWAuthenticate_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.WWWAuthenticate
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.WWWAuthenticate)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.WWWAuthenticate)(nil), (*header.WWWAuthenticate)(nil), true},
		{"zero ptr to nil ptr", &header.WWWAuthenticate{}, (*header.WWWAuthenticate)(nil), false},
		{"zero to zero", &header.WWWAuthenticate{}, header.WWWAuthenticate{}, true},
		{
			"not match 1",
			&header.WWWAuthenticate{},
			&header.WWWAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "Qwerty",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			false,
		},
		{
			"not match 2",
			&header.WWWAuthenticate{
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
			&header.WWWAuthenticate{
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
			&header.WWWAuthenticate{
				AuthChallenge: &header.AnyChallenge{
					Scheme: "custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			&header.WWWAuthenticate{
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

func TestWWWAuthenticate_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.WWWAuthenticate
		want bool
	}{
		{"nil", (*header.WWWAuthenticate)(nil), false},
		{"zero", &header.WWWAuthenticate{}, false},
		{
			"invalid 1",
			&header.WWWAuthenticate{
				AuthChallenge: &header.DigestChallenge{Realm: "ATLANTA.com"},
			},
			false,
		},
		{"invalid 2", &header.WWWAuthenticate{AuthChallenge: &header.BearerChallenge{}}, false},
		{"invalid 3", &header.WWWAuthenticate{AuthChallenge: (*header.AnyChallenge)(nil)}, false},
		{
			"valid",
			&header.WWWAuthenticate{
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

func TestWWWAuthenticate_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  *header.WWWAuthenticate
	}{
		{"nil", (*header.WWWAuthenticate)(nil)},
		{"zero", &header.WWWAuthenticate{}},
		{
			"digest",
			&header.WWWAuthenticate{
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
			&header.WWWAuthenticate{
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
			&header.WWWAuthenticate{
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

func TestDigestChallenge_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cln  *header.DigestChallenge
		want string
	}{
		{"nil", (*header.DigestChallenge)(nil), ""},
		{"zero", &header.DigestChallenge{}, "Digest "},
		{
			"full",
			&header.DigestChallenge{
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
			"Digest algorithm=MD5, nonce=\"f84f1cec41e6cbe5aea9c8e88d359\", " +
				"opaque=\"qwerty\", qop=\"auth,auth-int\", realm=\"atlanta.com\", stale=true, " +
				"domain=\"sip:ss1.carrier.com http://example.com /a/b/c\", p1=abc, p2=\"a b c\"",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.cln.Render(nil); got != c.want {
				t.Errorf("cln.Render(nil) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDigestChallenge_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		cln     *header.DigestChallenge
		wantRes string
		wantErr error
	}{
		{"nil", (*header.DigestChallenge)(nil), "", nil},
		{"zero", &header.DigestChallenge{}, "Digest ", nil},
		{
			"full",
			&header.DigestChallenge{
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
			"Digest algorithm=MD5, nonce=\"f84f1cec41e6cbe5aea9c8e88d359\", " +
				"opaque=\"qwerty\", qop=\"auth,auth-int\", realm=\"atlanta.com\", stale=true, " +
				"domain=\"sip:ss1.carrier.com http://example.com /a/b/c\", p1=abc, p2=\"a b c\"",
			nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			_, err := c.cln.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("cln.RenderTo(sb, nil) error = %v, want %q\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}
			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestDigestChallenge_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cln  *header.DigestChallenge
		want string
	}{
		{"nil", (*header.DigestChallenge)(nil), ""},
		{"zero", &header.DigestChallenge{}, "Digest "},
		{
			"full",
			&header.DigestChallenge{
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
			"Digest algorithm=MD5, nonce=\"f84f1cec41e6cbe5aea9c8e88d359\", " +
				"opaque=\"qwerty\", qop=\"auth,auth-int\", realm=\"atlanta.com\", stale=true, " +
				"domain=\"sip:ss1.carrier.com http://example.com /a/b/c\", p1=abc, p2=\"a b c\"",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.cln.String(); got != c.want {
				t.Errorf("cln.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDigestChallenge_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cln  *header.DigestChallenge
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.DigestChallenge)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.DigestChallenge)(nil), (*header.DigestChallenge)(nil), true},
		{"zero ptr to nil ptr", &header.DigestChallenge{}, (*header.DigestChallenge)(nil), false},
		{"zero ptr to zero", &header.DigestChallenge{}, header.DigestChallenge{}, true},
		{
			"not match 1",
			&header.DigestChallenge{
				Realm: "atlanta.com",
				Domain: []uri.URI{
					&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
				},
				QOP:       []string{"auth", "auth-int"},
				Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
				Stale:     true,
				Algorithm: "MD5",
				Opaque:    "qwerty",
			},
			&header.DigestChallenge{
				Realm: "atlanta.com",
				Domain: []uri.URI{
					&uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
				},
				QOP:       []string{"auth", "auth-int"},
				Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
				Stale:     true,
				Algorithm: "MD5",
				Opaque:    "qwerty",
			},
			false,
		},
		{
			"not match 2",
			&header.DigestChallenge{
				Realm: "atlanta.com",
				Domain: []uri.URI{
					&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
				},
				QOP:       []string{"auth", "auth-int"},
				Nonce:     "F84F1CEC41E6CBE5AEA9C8E88D359",
				Stale:     true,
				Algorithm: "MD5",
				Opaque:    "qwerty",
			},
			&header.DigestChallenge{
				Realm: "atlanta.com",
				Domain: []uri.URI{
					&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
				},
				QOP:       []string{"auth", "auth-int"},
				Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
				Stale:     true,
				Algorithm: "MD5",
				Opaque:    "qwerty",
			},
			false,
		},
		{
			"not match 3",
			&header.DigestChallenge{
				Realm: "atlanta.com",
				Domain: []uri.URI{
					&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
				},
				QOP:       []string{"auth", "auth-int"},
				Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
				Stale:     true,
				Algorithm: "MD5",
				Opaque:    "qwerty",
			},
			&header.DigestChallenge{
				Realm: "atlanta.com",
				Domain: []uri.URI{
					&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
				},
				QOP:       []string{"auth", "auth-int"},
				Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
				Stale:     true,
				Algorithm: "MD5",
				Opaque:    "QWERTY",
			},
			false,
		},
		{
			"not match 4",
			&header.DigestChallenge{
				Realm: "atlanta.com",
				Domain: []uri.URI{
					&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
				},
				QOP:       []string{"auth"},
				Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
				Stale:     true,
				Algorithm: "MD5",
				Opaque:    "qwerty",
			},
			&header.DigestChallenge{
				Realm: "atlanta.com",
				Domain: []uri.URI{
					&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
				},
				QOP:       []string{"auth", "auth-int"},
				Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
				Stale:     true,
				Algorithm: "MD5",
				Opaque:    "qwerty",
			},
			false,
		},
		{
			"not match 5",
			&header.DigestChallenge{
				Realm: "atlanta.com",
				Domain: []uri.URI{
					&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
				},
				QOP:       []string{"auth"},
				Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
				Stale:     false,
				Algorithm: "MD5",
				Opaque:    "qwerty",
			},
			&header.DigestChallenge{
				Realm: "atlanta.com",
				Domain: []uri.URI{
					&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
				},
				QOP:       []string{"auth"},
				Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
				Stale:     true,
				Algorithm: "MD5",
				Opaque:    "qwerty",
			},
			false,
		},
		{
			"match",
			&header.DigestChallenge{
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
				Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`).Set("p3", "def"),
			},
			&header.DigestChallenge{
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
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.cln.Equal(c.val); got != c.want {
				t.Errorf("cln.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestDigestChallenge_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cln  *header.DigestChallenge
		want bool
	}{
		{"nil", (*header.DigestChallenge)(nil), false},
		{"zero", &header.DigestChallenge{}, false},
		{"invalid 1", &header.DigestChallenge{Realm: "example.com"}, false},
		{
			"invalid 2",
			&header.DigestChallenge{
				Realm:     "example.com",
				Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
				Algorithm: "m d 5",
			},
			false,
		},
		{
			"invalid 3",
			&header.DigestChallenge{
				Realm:     "example.com",
				Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
				Algorithm: "md5",
				QOP:       []string{"au t h"},
			},
			false,
		},
		{
			"invalid 4",
			&header.DigestChallenge{
				Realm: "example.com",
				Nonce: "f84f1cec41e6cbe5aea9c8e88d359",
				Domain: []uri.URI{
					&uri.Any{},
				},
			},
			false,
		},
		{"valid", &header.DigestChallenge{Realm: "ATLANTA.com", Nonce: "f84f1cec41e6cbe5aea9c8e88d359"}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.cln.IsValid(); got != c.want {
				t.Errorf("cln.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestDigestChallenge_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cln  *header.DigestChallenge
		want any
	}{
		{"nil", (*header.DigestChallenge)(nil), nil},
		{"zero", &header.DigestChallenge{}, &header.DigestChallenge{}},
		{
			"full",
			&header.DigestChallenge{
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
			&header.DigestChallenge{
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
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.cln.Clone()
			if c.cln == nil {
				if got != nil {
					t.Errorf("cln.Clone() = %+v, want nil", got)
				}
				return
			}
			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Errorf("cln.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.want, diff)
			}
		})
	}
}

func TestBearerChallenge_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cln  *header.BearerChallenge
		want string
	}{
		{"nil", (*header.BearerChallenge)(nil), ""},
		{"zero", &header.BearerChallenge{}, "Bearer "},
		{
			"full",
			&header.BearerChallenge{
				Realm:       "atlanta.com",
				Scope:       "abc",
				AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
				Error:       "qwerty",
				Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			"Bearer error=\"qwerty\", realm=\"atlanta.com\", scope=\"abc\", " +
				"authz_server=\"http://example.com\", p1=abc, p2=\"a b c\"",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.cln.Render(nil); got != c.want {
				t.Errorf("cln.Render(nil) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBearerChallenge_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		cln     *header.BearerChallenge
		wantRes string
		wantErr error
	}{
		{"nil", (*header.BearerChallenge)(nil), "", nil},
		{"zero", &header.BearerChallenge{}, "Bearer ", nil},
		{
			"full",
			&header.BearerChallenge{
				Realm:       "atlanta.com",
				Scope:       "abc",
				AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
				Error:       "qwerty",
				Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			"Bearer error=\"qwerty\", realm=\"atlanta.com\", scope=\"abc\", " +
				"authz_server=\"http://example.com\", p1=abc, p2=\"a b c\"",
			nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			_, err := c.cln.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("cln.RenderTo(sb, nil) error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}
			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestBearerChallenge_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cln  *header.BearerChallenge
		want string
	}{
		{"nil", (*header.BearerChallenge)(nil), ""},
		{"zero", &header.BearerChallenge{}, "Bearer "},
		{
			"full",
			&header.BearerChallenge{
				Realm:       "atlanta.com",
				Scope:       "abc",
				AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
				Error:       "qwerty",
				Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			"Bearer error=\"qwerty\", realm=\"atlanta.com\", scope=\"abc\", " +
				"authz_server=\"http://example.com\", p1=abc, p2=\"a b c\"",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.cln.String(); got != c.want {
				t.Errorf("cln.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBearerChallenge_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cln  *header.BearerChallenge
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.BearerChallenge)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.BearerChallenge)(nil), (*header.BearerChallenge)(nil), true},
		{"zero ptr to nil ptr", &header.BearerChallenge{}, (*header.BearerChallenge)(nil), false},
		{"zero ptr to zero", &header.BearerChallenge{}, header.BearerChallenge{}, true},
		{
			"not match 1",
			&header.BearerChallenge{Realm: "example.com"},
			&header.BearerChallenge{Realm: "local"},
			false,
		},
		{
			"not match 2",
			&header.BearerChallenge{Realm: "atlanta.com", Scope: "abc"},
			&header.BearerChallenge{Realm: "atlanta.com", Scope: "ABC"},
			false,
		},
		{
			"not match 3",
			&header.BearerChallenge{Realm: "atlanta.com", Scope: "abc", Error: "qwerty"},
			&header.BearerChallenge{Realm: "atlanta.com", Scope: "abc", Error: "QWERTY"},
			false,
		},
		{
			"not match 4",
			&header.BearerChallenge{
				Realm:       "atlanta.com",
				Scope:       "abc",
				AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
				Error:       "qwerty",
			},
			&header.BearerChallenge{
				Realm:       "ATLANTA.COM",
				Scope:       "abc",
				AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "localhost", Path: "/a/b/c"}},
				Error:       "qwerty",
			},
			false,
		},
		{
			"match",
			&header.BearerChallenge{
				Realm:       "atlanta.com",
				Scope:       "abc",
				AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
				Error:       "qwerty",
				Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			&header.BearerChallenge{
				Realm:       "ATLANTA.COM",
				Scope:       "abc",
				AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
				Error:       "qwerty",
				Params:      make(header.Values).Set("p1", "abc").Set("p3", "asd"),
			},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.cln.Equal(c.val); got != c.want {
				t.Errorf("cln.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBearerChallenge_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cln  *header.BearerChallenge
		want bool
	}{
		{"nil", (*header.BearerChallenge)(nil), false},
		{"zero", &header.BearerChallenge{}, false},
		{"invalid", &header.BearerChallenge{Realm: "example.com"}, false},
		{"valid", &header.BearerChallenge{AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}}}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.cln.IsValid(); got != c.want {
				t.Errorf("cln.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBearerChallenge_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cln  *header.BearerChallenge
	}{
		{"nil", (*header.BearerChallenge)(nil)},
		{"zero", &header.BearerChallenge{}},
		{
			"full",
			&header.BearerChallenge{
				Realm:       "atlanta.com",
				Scope:       "abc",
				AuthzServer: &uri.Any{URL: url.URL{Scheme: "http", Host: "example.com"}},
				Error:       "qwerty",
				Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.cln.Clone()
			if c.cln == nil {
				if got != nil {
					t.Errorf("cln.Clone() = %+v, want nil", got)
				}
				return
			}
			if diff := cmp.Diff(got, c.cln); diff != "" {
				t.Errorf("cln.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.cln, diff)
			}
		})
	}
}

func TestAnyChallenge_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cln  *header.AnyChallenge
		want string
	}{
		{"nil", (*header.AnyChallenge)(nil), ""},
		{"zero", &header.AnyChallenge{}, " "},
		{
			"full",
			&header.AnyChallenge{
				Scheme: "Custom",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			"Custom p1=abc, p2=\"a b c\"",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.cln.Render(nil); got != c.want {
				t.Errorf("cln.Render(nil) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAnyChallenge_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		cln     *header.AnyChallenge
		wantRes string
		wantErr error
	}{
		{"nil", (*header.AnyChallenge)(nil), "", nil},
		{"zero", &header.AnyChallenge{}, " ", nil},
		{
			"full",
			&header.AnyChallenge{
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
			_, err := c.cln.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("cln.RenderTo(sb, nil) error = %v, want %q\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}
			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestAnyChallenge_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cln  *header.AnyChallenge
		want string
	}{
		{"nil", (*header.AnyChallenge)(nil), ""},
		{"zero", &header.AnyChallenge{}, " "},
		{
			"full",
			&header.AnyChallenge{
				Scheme: "Custom",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			"Custom p1=abc, p2=\"a b c\"",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.cln.String(); got != c.want {
				t.Errorf("cln.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAnyChallenge_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cln  *header.AnyChallenge
		val  any
		want bool
	}{
		{"nil ptr to nil", (*header.AnyChallenge)(nil), nil, false},
		{"nil ptr to nil ptr", (*header.AnyChallenge)(nil), (*header.AnyChallenge)(nil), true},
		{"zero ptr to nil ptr", &header.AnyChallenge{}, (*header.AnyChallenge)(nil), false},
		{"zero ptr to zero", &header.AnyChallenge{}, header.AnyChallenge{}, true},
		{
			"not match 1",
			&header.AnyChallenge{
				Scheme: "Custom",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			&header.AnyChallenge{
				Scheme: "Qwerty",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			false,
		},
		{
			"not match 2",
			&header.AnyChallenge{
				Scheme: "Custom",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			&header.AnyChallenge{
				Scheme: "Custom",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"zxc"`),
			},
			false,
		},
		{
			"match",
			&header.AnyChallenge{
				Scheme: "custom",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			},
			&header.AnyChallenge{
				Scheme: "Custom",
				Params: make(header.Values).Set("p1", "ABC").Set("p2", `"a b c"`),
			},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.cln.Equal(c.val); got != c.want {
				t.Errorf("cln.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAnyChallenge_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cln  *header.AnyChallenge
		want bool
	}{
		{"nil", (*header.AnyChallenge)(nil), false},
		{"zero", &header.AnyChallenge{}, false},
		{"invalid 1", &header.AnyChallenge{Scheme: ""}, false},
		{"invalid 2", &header.AnyChallenge{Scheme: "qwerty"}, false},
		{"valid", &header.AnyChallenge{Scheme: "qwerty", Params: make(header.Values).Set("p1", "abc")}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.cln.IsValid(); got != c.want {
				t.Errorf("cln.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAnyChallenge_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cln  *header.AnyChallenge
	}{
		{"nil", (*header.AnyChallenge)(nil)},
		{"zero", &header.AnyChallenge{}},
		{
			"full",
			&header.AnyChallenge{Scheme: "qwerty", Params: make(header.Values).Set("p1", "abc")},
		},
	}

	cmpOpts := []cmp.Option{
		cmp.AllowUnexported(header.AnyChallenge{}),
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.cln.Clone()
			if c.cln == nil {
				if got != nil {
					t.Errorf("cln.Clone() = %+v, want nil", got)
				}
				return
			}
			if diff := cmp.Diff(got, c.cln, cmpOpts...); diff != "" {
				t.Errorf("cln.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.cln, diff)
			}
		})
	}
}
