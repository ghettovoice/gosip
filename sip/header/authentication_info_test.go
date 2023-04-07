package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Authentication-Info", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Authentication-Info:", &header.Any{Name: "Authentication-Info"}, nil),
			Entry(nil, `Authentication-Info: qwe="rty"`, &header.Any{Name: "Authentication-Info", Value: `qwe="rty"`}, nil),
			Entry(nil,
				"Authentication-Info:\r\n"+
					"\tnextnonce=\"qwe\",\r\n"+
					"\tqop=auth,\r\n"+
					"\trspauth=\"a0b5\",\r\n"+
					"\tcnonce=\"rty\",\r\n"+
					"\tnc=00000003\r\n",
				&header.AuthenticationInfo{
					NextNonce:  "qwe",
					QOP:        "auth",
					RspAuth:    "a0b5",
					CNonce:     "rty",
					NonceCount: 3,
				},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (*header.AuthenticationInfo)(nil), ""),
			Entry(nil, &header.AuthenticationInfo{}, "Authentication-Info: "),
			Entry(nil,
				&header.AuthenticationInfo{
					NextNonce:  "qwerty",
					QOP:        "auth-int",
					RspAuth:    "abcdef",
					CNonce:     "1q2w3e",
					NonceCount: 5,
				},
				`Authentication-Info: cnonce="1q2w3e", nc=00000005, nextnonce="qwerty", qop=auth-int, rspauth="abcdef"`,
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, (*header.AuthenticationInfo)(nil), (*header.AuthenticationInfo)(nil), true),
			Entry(nil, (*header.AuthenticationInfo)(nil), nil, false),
			Entry(nil, &header.AuthenticationInfo{}, (*header.AuthenticationInfo)(nil), false),
			Entry(nil,
				&header.AuthenticationInfo{
					NextNonce:  "qwerty",
					QOP:        "auth",
					RspAuth:    "abcdef",
					CNonce:     "1q2w3e",
					NonceCount: 5,
				},
				header.AuthenticationInfo{
					NextNonce:  "qwerty",
					QOP:        "AUTH",
					RspAuth:    "abcdef",
					CNonce:     "1q2w3e",
					NonceCount: 5,
				},
				true,
			),
			Entry(nil,
				&header.AuthenticationInfo{
					NextNonce:  "qwerty",
					QOP:        "auth",
					RspAuth:    "abcdef",
					CNonce:     "1q2w3e",
					NonceCount: 5,
				},
				&header.AuthenticationInfo{
					NextNonce:  "QWERTY",
					QOP:        "auth",
					RspAuth:    "abcdef",
					CNonce:     "1q2w3e",
					NonceCount: 5,
				},
				false,
			),
			Entry(nil,
				&header.AuthenticationInfo{
					NextNonce:  "qwerty",
					QOP:        "auth",
					RspAuth:    "abcdef",
					CNonce:     "1q2w3e",
					NonceCount: 5,
				},
				&header.AuthenticationInfo{
					NextNonce:  "qwerty",
					QOP:        "auth",
					RspAuth:    "abcdef",
					CNonce:     "1q2w3e",
					NonceCount: 10,
				},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (*header.AuthenticationInfo)(nil), false),
			Entry(nil, &header.AuthenticationInfo{}, false),
			Entry(nil, &header.AuthenticationInfo{QOP: "auth"}, false),
			Entry(nil,
				&header.AuthenticationInfo{
					NextNonce: "qwerty",
					QOP:       "auth",
				},
				true,
			),
			Entry(nil,
				&header.AuthenticationInfo{
					NextNonce: "qwerty",
					QOP:       "a u t h",
				},
				false,
			),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 *header.AuthenticationInfo) {
				Expect(reflect.ValueOf(hdr2).Pointer()).
					ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
			},
			Entry(nil, (*header.AuthenticationInfo)(nil)),
			Entry(nil, &header.AuthenticationInfo{}),
			Entry(nil,
				&header.AuthenticationInfo{
					NextNonce:  "qwerty",
					QOP:        "auth",
					RspAuth:    "abcdef",
					CNonce:     "1q2w3e",
					NonceCount: 5,
				},
			),
			// endregion
		)
	})
})
