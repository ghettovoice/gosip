package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Proxy-Require", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Proxy-Require: ", &header.Any{Name: "Proxy-Require"}, nil),
			Entry(nil, "Proxy-Require: 100rel, Foo, Bar", header.ProxyRequire{"100rel", "Foo", "Bar"}, nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.ProxyRequire(nil), ""),
			Entry(nil, header.ProxyRequire{}, "Proxy-Require: "),
			Entry(nil, header.ProxyRequire{"100rel", "Foo", "Bar"}, "Proxy-Require: 100rel, Foo, Bar"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.ProxyRequire(nil), nil, false),
			Entry(nil, header.ProxyRequire(nil), header.ProxyRequire(nil), true),
			Entry(nil, header.ProxyRequire{}, header.ProxyRequire(nil), true),
			Entry(nil, header.ProxyRequire{}, header.ProxyRequire{}, true),
			Entry(nil, header.ProxyRequire{"100rel", "Foo", "Bar"}, header.ProxyRequire{}, false),
			Entry(nil, header.ProxyRequire{"100rel", "Foo", "Bar"}, header.ProxyRequire{"100rel", "foo", "bar"}, true),
			Entry(nil, header.ProxyRequire{"100rel", "foo", "bar"}, header.ProxyRequire{"bar", "foo", "100rel"}, false),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.ProxyRequire(nil), false),
			Entry(nil, header.ProxyRequire{}, false),
			Entry(nil, header.ProxyRequire{"100rel", "Foo", "Bar"}, true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.ProxyRequire) {
				if len(hdr1) > 0 {
					Expect(reflect.ValueOf(hdr2).Pointer()).ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
				}
			},
			Entry(nil, header.ProxyRequire(nil)),
			Entry(nil, header.ProxyRequire{}),
			Entry(nil, header.ProxyRequire{"100rel", "Foo", "Bar"}),
			// endregion
		)
	})
})
