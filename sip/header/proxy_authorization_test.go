package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/uri"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Proxy-Authorization", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Proxy-Authorization: qwerty", &header.Any{Name: "Proxy-Authorization", Value: "qwerty"}, nil),
			Entry(nil,
				"Proxy-Authorization: Digest username=\"root\", realm=\"example.com\", nonce=\"qwerty\",\r\n"+
					"\turi=\"sip:example.com\", response=\"587245234b3434cc3412213e5f113a54\", algorithm=MD5,\r\n"+
					"\tcnonce=\"1q2w3e\", opaque=\"zxc\", qop=auth, nc=00000005, p1=abc, p2=\"a b c\"",
				&header.ProxyAuthorization{
					AuthCredentials: &header.DigestAuthCredentials{
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
				nil,
			),
			Entry(nil,
				"Proxy-Authorization: Bearer QweRTY123",
				&header.ProxyAuthorization{
					AuthCredentials: &header.BearerAuthCredentials{
						Token: "QweRTY123",
					},
				},
				nil,
			),
			Entry(nil,
				"Proxy-Authorization: Custom p1=abc, p2=\"a b c\"",
				&header.ProxyAuthorization{
					AuthCredentials: &header.GenericAuthCredentials{
						Scheme: "Custom",
						Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
					},
				},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (*header.ProxyAuthorization)(nil), ""),
			Entry(nil, &header.ProxyAuthorization{}, "Proxy-Authorization: "),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.DigestAuthCredentials{
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
				"Proxy-Authorization: Digest algorithm=MD5, cnonce=\"1q2w3e\", nc=00000005, nonce=\"qwerty\", opaque=\"zxc\", "+
					"qop=auth, realm=\"example.com\", response=\"587245234b3434cc3412213e5f113a54\", username=\"root\", "+
					"uri=\"sip:example.com\", p1=abc, p2=\"a b c\"",
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.BearerAuthCredentials{
						Token: "QweRTY123",
					},
				},
				"Proxy-Authorization: Bearer QweRTY123",
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.GenericAuthCredentials{
						Scheme: "Custom",
						Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
					},
				},
				"Proxy-Authorization: Custom p1=abc, p2=\"a b c\"",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, (*header.ProxyAuthorization)(nil), nil, false),
			Entry(nil, (*header.ProxyAuthorization)(nil), (*header.ProxyAuthorization)(nil), true),
			Entry(nil, &header.ProxyAuthorization{}, (*header.ProxyAuthorization)(nil), false),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.DigestAuthCredentials{
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
				header.ProxyAuthorization{
					AuthCredentials: &header.DigestAuthCredentials{
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
				},
				true,
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.DigestAuthCredentials{
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
					AuthCredentials: &header.DigestAuthCredentials{
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
				},
				false,
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.DigestAuthCredentials{
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
					AuthCredentials: (*header.DigestAuthCredentials)(nil),
				},
				false,
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.DigestAuthCredentials{
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
					AuthCredentials: &header.BearerAuthCredentials{
						Token: "QwertY",
					},
				},
				false,
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.BearerAuthCredentials{
						Token: "QWERTY",
					},
				},
				&header.ProxyAuthorization{
					AuthCredentials: &header.BearerAuthCredentials{
						Token: "qwerty",
					},
				},
				false,
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.BearerAuthCredentials{
						Token: "QwertY",
					},
				},
				&header.ProxyAuthorization{
					AuthCredentials: &header.BearerAuthCredentials{
						Token: "QwertY",
					},
				},
				true,
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.GenericAuthCredentials{
						Scheme: "custom",
						Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
					},
				},
				&header.ProxyAuthorization{
					AuthCredentials: &header.GenericAuthCredentials{
						Scheme: "Custom",
						Params: make(header.Values).Set("p1", "ABC").Set("p2", `"a b c"`),
					},
				},
				true,
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.GenericAuthCredentials{
						Scheme: "Custom",
						Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
					},
				},
				&header.ProxyAuthorization{
					AuthCredentials: &header.GenericAuthCredentials{
						Scheme: "Qwerty",
						Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
					},
				},
				false,
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.GenericAuthCredentials{
						Scheme: "Custom",
						Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
					},
				},
				&header.ProxyAuthorization{
					AuthCredentials: &header.GenericAuthCredentials{
						Scheme: "Custom",
						Params: make(header.Values).Set("p1", "abc").Set("p2", `"zxc"`),
					},
				},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (*header.ProxyAuthorization)(nil), false),
			Entry(nil, &header.ProxyAuthorization{}, false),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.DigestAuthCredentials{
						Username: "root",
						Response: "587245234b3434cc3412213e5f113a54",
					},
				},
				false,
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.DigestAuthCredentials{
						Username:  "root",
						Realm:     "example.com",
						Nonce:     "qwerty",
						URI:       &uri.SIP{Addr: uri.Host("example.com")},
						Response:  "587245234b3434cc3412213e5f113a54",
						Algorithm: "MD5",
						QOP:       "auth",
					},
				},
				true,
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.BearerAuthCredentials{},
				},
				false,
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.BearerAuthCredentials{Token: "QwertY"},
				},
				true,
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.GenericAuthCredentials{},
				},
				false,
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.GenericAuthCredentials{
						Scheme: "Custom",
						Params: make(header.Values),
					},
				},
				false,
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.GenericAuthCredentials{
						Scheme: "Custom",
						Params: make(header.Values).Set("p1", "abc"),
					},
				},
				true,
			),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 *header.ProxyAuthorization) {
				Expect(reflect.ValueOf(hdr2).Pointer()).ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
				switch crd1 := hdr1.AuthCredentials.(type) {
				case *header.DigestAuthCredentials:
					crd2 := hdr2.AuthCredentials.(*header.DigestAuthCredentials)
					if crd1 == nil || reflect.ValueOf(crd1).IsNil() {
						Expect(crd2).To(BeNil())
					} else {
						Expect(reflect.ValueOf(crd2).Pointer()).
							ToNot(Equal(reflect.ValueOf(crd1).Pointer()))
						if crd1.URI == nil || reflect.ValueOf(crd1.URI).IsNil() {
							Expect(crd2.URI).To(BeNil())
						} else {
							Expect(reflect.ValueOf(crd2.URI).Pointer()).
								ToNot(Equal(reflect.ValueOf(crd1.URI).Pointer()))
						}
						if crd1.Params == nil {
							Expect(crd2.Params).To(BeNil())
						} else {
							Expect(reflect.ValueOf(crd2.Params).Pointer()).
								ToNot(Equal(reflect.ValueOf(crd1.Params).Pointer()))
						}
					}
				case *header.BearerAuthCredentials:
					crd2 := hdr2.AuthCredentials.(*header.BearerAuthCredentials)
					if crd1 == nil || reflect.ValueOf(crd1).IsNil() {
						Expect(crd2).To(BeNil())
					} else {
						Expect(reflect.ValueOf(crd2).Pointer()).ToNot(Equal(reflect.ValueOf(crd1).Pointer()))
					}
				case *header.GenericAuthCredentials:
					crd2 := hdr2.AuthCredentials.(*header.GenericAuthCredentials)
					if crd1 == nil || reflect.ValueOf(crd1).IsNil() {
						Expect(crd2).To(BeNil())
					} else {
						Expect(reflect.ValueOf(crd2).Pointer()).ToNot(Equal(reflect.ValueOf(crd1).Pointer()))
						if crd1.Params == nil {
							Expect(crd2.Params).To(BeNil())
						} else {
							Expect(reflect.ValueOf(crd2.Params).Pointer()).
								ToNot(Equal(reflect.ValueOf(crd1.Params).Pointer()))
						}
					}
				}
			},
			Entry(nil, (*header.ProxyAuthorization)(nil)),
			Entry(nil, &header.ProxyAuthorization{}),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.DigestAuthCredentials{
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
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.BearerAuthCredentials{
						Token: "QweRTY123",
					},
				},
			),
			Entry(nil,
				&header.ProxyAuthorization{
					AuthCredentials: &header.GenericAuthCredentials{
						Scheme: "Custom",
						Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
					},
				},
			),
			// endregion
		)
	})
})
