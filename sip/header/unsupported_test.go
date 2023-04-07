package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Unsupported", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Unsupported: ", &header.Any{Name: "Unsupported"}, nil),
			Entry(nil, "Unsupported: 100rel, Foo, Bar", header.Unsupported{"100rel", "Foo", "Bar"}, nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.Unsupported(nil), ""),
			Entry(nil, header.Unsupported{}, "Unsupported: "),
			Entry(nil, header.Unsupported{"100rel", "Foo", "Bar"}, "Unsupported: 100rel, Foo, Bar"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.Unsupported(nil), nil, false),
			Entry(nil, header.Unsupported(nil), header.Unsupported(nil), true),
			Entry(nil, header.Unsupported{}, header.Unsupported(nil), true),
			Entry(nil, header.Unsupported{}, header.Unsupported{}, true),
			Entry(nil, header.Unsupported{"100rel", "Foo", "Bar"}, header.Unsupported{}, false),
			Entry(nil, header.Unsupported{"100rel", "Foo", "Bar"}, header.Unsupported{"100rel", "foo", "bar"}, true),
			Entry(nil, header.Unsupported{"100rel", "foo", "bar"}, header.Unsupported{"bar", "foo", "100rel"}, false),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.Unsupported(nil), false),
			Entry(nil, header.Unsupported{}, false),
			Entry(nil, header.Unsupported{"100rel", "Foo", "Bar"}, true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.Unsupported) {
				if len(hdr1) > 0 {
					Expect(reflect.ValueOf(hdr2).Pointer()).ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
				}
			},
			Entry(nil, header.Unsupported(nil)),
			Entry(nil, header.Unsupported{}),
			Entry(nil, header.Unsupported{"100rel", "Foo", "Bar"}),
			// endregion
		)
	})
})
