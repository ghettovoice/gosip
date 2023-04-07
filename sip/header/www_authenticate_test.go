package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/uri"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("WWW-Authenticate", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "WWW-Authenticate: ", &header.Any{Name: "WWW-Authenticate"}, nil),
			Entry(nil, "WWW-Authenticate: Digest", &header.Any{Name: "WWW-Authenticate", Value: "Digest"}, nil),
			Entry(nil,
				"WWW-Authenticate: Digest realm=\"atlanta.com\",\r\n"+
					"\tdomain=\"sip:ss1.carrier.com http://example.com /a/b/c\", qop=\"auth,auth-int\",\r\n"+
					"\tnonce=\"f84f1cec41e6cbe5aea9c8e88d359\",\r\n"+
					"\topaque=\"\", stale=true, algorithm=MD5,\r\n"+
					"\tp1=abc, p2=\"a b c\"",
				&header.WWWAuthenticate{&header.DigestAuthChallenge{
					Realm: "atlanta.com",
					Domain: []uri.URI{
						&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
						&uri.Any{Scheme: "http", Host: "example.com"},
						&uri.Any{Path: "/a/b/c"},
					},
					QOP:       []string{"auth", "auth-int"},
					Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
					Stale:     true,
					Algorithm: "MD5",
					Opaque:    "",
					Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				}},
				nil,
			),
			Entry(nil,
				"WWW-Authenticate: Bearer realm=\"atlanta.com\",\r\n"+
					"\tscope=\"abc\", authz_server=\"http://example.com\", error=\"qwerty\",\r\n"+
					"\tp1=abc, p2=\"a b c\"",
				&header.WWWAuthenticate{&header.BearerAuthChallenge{
					Realm:       "atlanta.com",
					Scope:       "abc",
					AuthzServer: &uri.Any{Scheme: "http", Host: "example.com"},
					Error:       "qwerty",
					Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				}},
				nil,
			),
			Entry(nil,
				"WWW-Authenticate: Custom p1=abc, p2=\"a b c\"",
				&header.WWWAuthenticate{&header.GenericAuthChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				}},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (*header.WWWAuthenticate)(nil), ""),
			Entry(nil, &header.WWWAuthenticate{}, "WWW-Authenticate: "),
			Entry(nil,
				&header.WWWAuthenticate{&header.DigestAuthChallenge{
					Realm: "atlanta.com",
					Domain: []uri.URI{
						&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
						&uri.Any{Scheme: "http", Host: "example.com"},
						&uri.Any{Path: "/a/b/c"},
					},
					QOP:       []string{"auth", "auth-int"},
					Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
					Stale:     true,
					Algorithm: "MD5",
					Opaque:    "qwerty",
					Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				}},
				"WWW-Authenticate: Digest algorithm=MD5, nonce=\"f84f1cec41e6cbe5aea9c8e88d359\", "+
					"opaque=\"qwerty\", qop=\"auth,auth-int\", realm=\"atlanta.com\", stale=true, "+
					"domain=\"sip:ss1.carrier.com http://example.com /a/b/c\", p1=abc, p2=\"a b c\"",
			),
			Entry(nil,
				&header.WWWAuthenticate{&header.BearerAuthChallenge{
					Realm:       "atlanta.com",
					Scope:       "abc",
					AuthzServer: &uri.Any{Scheme: "http", Host: "example.com"},
					Error:       "qwerty",
					Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				}},
				"WWW-Authenticate: Bearer error=\"qwerty\", realm=\"atlanta.com\", scope=\"abc\", "+
					"authz_server=\"http://example.com\", p1=abc, p2=\"a b c\"",
			),
			Entry(nil,
				&header.WWWAuthenticate{&header.GenericAuthChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				}},
				"WWW-Authenticate: Custom p1=abc, p2=\"a b c\"",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, (*header.WWWAuthenticate)(nil), nil, false),
			Entry(nil, (*header.WWWAuthenticate)(nil), (*header.WWWAuthenticate)(nil), true),
			Entry(nil, &header.WWWAuthenticate{}, (*header.WWWAuthenticate)(nil), false),
			Entry(nil, &header.WWWAuthenticate{}, &header.WWWAuthenticate{}, true),
			Entry(nil,
				&header.WWWAuthenticate{(*header.DigestAuthChallenge)(nil)},
				&header.WWWAuthenticate{(*header.DigestAuthChallenge)(nil)},
				true,
			),
			Entry(nil,
				&header.WWWAuthenticate{(*header.DigestAuthChallenge)(nil)},
				&header.WWWAuthenticate{(*header.BearerAuthChallenge)(nil)},
				false,
			),
			Entry(nil,
				&header.WWWAuthenticate{&header.DigestAuthChallenge{
					Realm: "atlanta.com",
					Domain: []uri.URI{
						&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
						&uri.Any{Scheme: "http", Host: "example.com"},
						&uri.Any{Path: "/a/b/c"},
					},
					QOP:       []string{"auth", "auth-int"},
					Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
					Stale:     true,
					Algorithm: "MD5",
					Opaque:    "qwerty",
					Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				}},
				header.WWWAuthenticate{&header.DigestAuthChallenge{
					Realm: "ATLANTA.com",
					Domain: []uri.URI{
						&uri.SIP{Addr: uri.Host("SS1.CARRIER.COM")},
						&uri.Any{Scheme: "http", Host: "example.com"},
						&uri.Any{Path: "/a/b/c"},
					},
					QOP:       []string{"auth", "auth-int"},
					Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
					Stale:     true,
					Algorithm: "md5",
					Opaque:    "qwerty",
					Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				}},
				true,
			),
			Entry(nil,
				&header.WWWAuthenticate{&header.BearerAuthChallenge{
					Realm:       "atlanta.com",
					Scope:       "abc",
					AuthzServer: &uri.Any{Scheme: "http", Host: "example.com"},
					Error:       "qwerty",
					Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				}},
				&header.WWWAuthenticate{&header.BearerAuthChallenge{
					Realm:       "ATLANTA.COM",
					Scope:       "abc",
					AuthzServer: &uri.Any{Scheme: "http", Host: "example.com"},
					Error:       "qwerty",
					Params:      make(header.Values).Set("p1", "abc"),
				}},
				true,
			),
			Entry(nil,
				&header.WWWAuthenticate{&header.GenericAuthChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				}},
				&header.WWWAuthenticate{&header.GenericAuthChallenge{
					Scheme: "CUSTOM",
					Params: make(header.Values).Set("p1", "ABC").Set("p2", `"a b c"`),
				}},
				true,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (*header.WWWAuthenticate)(nil), false),
			Entry(nil, &header.WWWAuthenticate{}, false),
			Entry(nil, &header.WWWAuthenticate{&header.DigestAuthChallenge{}}, false),
			Entry(nil,
				&header.WWWAuthenticate{&header.DigestAuthChallenge{
					Realm: "atlanta.com",
					Domain: []uri.URI{
						&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
						&uri.Any{Scheme: "http", Host: "example.com"},
						&uri.Any{Path: "/a/b/c"},
					},
					QOP:       []string{"auth", "auth-int"},
					Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
					Stale:     true,
					Algorithm: "MD5",
					Opaque:    "qwerty",
					Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				}},
				true,
			),
			Entry(nil,
				&header.WWWAuthenticate{&header.BearerAuthChallenge{
					Realm:       "atlanta.com",
					Scope:       "abc",
					AuthzServer: &uri.Any{Scheme: "http", Host: "example.com"},
					Error:       "qwerty",
					Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				}},
				true,
			),
			Entry(nil,
				&header.WWWAuthenticate{&header.GenericAuthChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				}},
				true,
			),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 *header.WWWAuthenticate) {
				Expect(reflect.ValueOf(hdr2).Pointer()).ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
				switch cln1 := hdr1.AuthChallenge.(type) {
				case *header.DigestAuthChallenge:
					cln2 := hdr2.AuthChallenge.(*header.DigestAuthChallenge)
					if cln1 == nil || reflect.ValueOf(cln1).IsNil() {
						Expect(cln2).To(BeNil())
					} else {
						Expect(reflect.ValueOf(cln2).Pointer()).ToNot(Equal(reflect.ValueOf(cln1).Pointer()))
						if len(cln1.QOP) == 0 {
							Expect(cln2.QOP).To(HaveLen(0))
						} else {
							Expect(reflect.ValueOf(cln2.QOP).Pointer()).ToNot(Equal(reflect.ValueOf(cln1.QOP).Pointer()))
						}
						if len(cln1.Domain) == 0 {
							Expect(cln2.Domain).To(HaveLen(0))
						} else {
							Expect(reflect.ValueOf(cln2.Domain).Pointer()).ToNot(Equal(reflect.ValueOf(cln1.Domain).Pointer()))
							for i := range cln1.Domain {
								Expect(reflect.ValueOf(cln2.Domain[i]).Pointer()).ToNot(Equal(reflect.ValueOf(cln1.Domain[i]).Pointer()))
							}
						}
						if cln1.Params == nil {
							Expect(cln2.Params).To(BeNil())
						} else {
							Expect(reflect.ValueOf(cln2.Params).Pointer()).ToNot(Equal(reflect.ValueOf(cln1.Params).Pointer()))
						}
					}
				case *header.BearerAuthChallenge:
					cln2 := hdr2.AuthChallenge.(*header.BearerAuthChallenge)
					if cln1 == nil || reflect.ValueOf(cln1).IsNil() {
						Expect(cln2).To(BeNil())
					} else {
						Expect(reflect.ValueOf(cln2).Pointer()).ToNot(Equal(reflect.ValueOf(cln1).Pointer()))
						if cln1.AuthzServer == nil || reflect.ValueOf(cln1.AuthzServer).IsNil() {
							Expect(cln2.AuthzServer).ToNot(BeNil())
						} else {
							Expect(reflect.ValueOf(cln2.AuthzServer).Pointer()).ToNot(Equal(reflect.ValueOf(cln1.AuthzServer).Pointer()))
						}
						if cln1.Params == nil {
							Expect(cln2.Params).To(BeNil())
						} else {
							Expect(reflect.ValueOf(cln2.Params).Pointer()).ToNot(Equal(reflect.ValueOf(cln1.Params).Pointer()))
						}
					}
				case *header.GenericAuthChallenge:
					cln2 := hdr2.AuthChallenge.(*header.GenericAuthChallenge)
					if cln1 == nil || reflect.ValueOf(cln1).IsNil() {
						Expect(cln2).To(BeNil())
					} else {
						Expect(reflect.ValueOf(cln2).Pointer()).ToNot(Equal(reflect.ValueOf(cln1).Pointer()))
						if cln1.Params == nil {
							Expect(cln2.Params).To(BeNil())
						} else {
							Expect(reflect.ValueOf(cln2.Params).Pointer()).ToNot(Equal(reflect.ValueOf(cln1.Params).Pointer()))
						}
					}
				}
			},
			Entry(nil, (*header.WWWAuthenticate)(nil)),
			Entry(nil, &header.WWWAuthenticate{}),
			// Entry(nil, &header.WWWAuthenticate{(*header.DigestAuthChallenge)(nil)}),
			Entry(nil,
				&header.WWWAuthenticate{&header.DigestAuthChallenge{
					Realm: "atlanta.com",
					Domain: []uri.URI{
						&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
						&uri.Any{Scheme: "http", Host: "example.com"},
						&uri.Any{Path: "/a/b/c"},
					},
					QOP:       []string{"auth", "auth-int"},
					Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
					Stale:     true,
					Algorithm: "MD5",
					Opaque:    "qwerty",
					Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				}},
			),
			Entry(nil,
				&header.WWWAuthenticate{&header.BearerAuthChallenge{
					Realm:       "atlanta.com",
					Scope:       "abc",
					AuthzServer: &uri.Any{Scheme: "http", Host: "example.com"},
					Error:       "qwerty",
					Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				}},
			),
			Entry(nil,
				&header.WWWAuthenticate{&header.GenericAuthChallenge{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				}},
			),
			// endregion
		)
	})
})
