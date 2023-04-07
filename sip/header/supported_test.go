package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Supported", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Supported: ", header.Supported{}, nil),
			Entry(nil, "Supported: 100rel, Foo, Bar", header.Supported{"100rel", "Foo", "Bar"}, nil),
			Entry(nil, "k: 100rel, Foo, Bar", header.Supported{"100rel", "Foo", "Bar"}, nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.Supported(nil), ""),
			Entry(nil, header.Supported{}, "Supported: "),
			Entry(nil, header.Supported{"100rel", "Foo", "Bar"}, "Supported: 100rel, Foo, Bar"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.Supported(nil), nil, false),
			Entry(nil, header.Supported(nil), header.Supported(nil), true),
			Entry(nil, header.Supported{}, header.Supported(nil), true),
			Entry(nil, header.Supported{}, header.Supported{}, true),
			Entry(nil, header.Supported{"100rel", "Foo", "Bar"}, header.Supported{}, false),
			Entry(nil, header.Supported{"100rel", "Foo", "Bar"}, header.Supported{"100rel", "foo", "bar"}, true),
			Entry(nil, header.Supported{"100rel", "foo", "bar"}, header.Supported{"bar", "foo", "100rel"}, false),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.Supported(nil), false),
			Entry(nil, header.Supported{}, true),
			Entry(nil, header.Supported{"100rel", "Foo", "Bar"}, true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.Supported) {
				if len(hdr1) > 0 {
					Expect(reflect.ValueOf(hdr2).Pointer()).ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
				}
			},
			Entry(nil, header.Supported(nil)),
			Entry(nil, header.Supported{}),
			Entry(nil, header.Supported{"100rel", "Foo", "Bar"}),
			// endregion
		)
	})
})
