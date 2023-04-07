package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Require", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Require: ", &header.Any{Name: "Require"}, nil),
			Entry(nil, "Require: 100rel, Foo, Bar", header.Require{"100rel", "Foo", "Bar"}, nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.Require(nil), ""),
			Entry(nil, header.Require{}, "Require: "),
			Entry(nil, header.Require{"100rel", "Foo", "Bar"}, "Require: 100rel, Foo, Bar"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.Require(nil), nil, false),
			Entry(nil, header.Require(nil), header.Require(nil), true),
			Entry(nil, header.Require{}, header.Require(nil), true),
			Entry(nil, header.Require{}, header.Require{}, true),
			Entry(nil, header.Require{"100rel", "Foo", "Bar"}, header.Require{}, false),
			Entry(nil, header.Require{"100rel", "Foo", "Bar"}, header.Require{"100rel", "foo", "bar"}, true),
			Entry(nil, header.Require{"100rel", "foo", "bar"}, header.Require{"bar", "foo", "100rel"}, false),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.Require(nil), false),
			Entry(nil, header.Require{}, false),
			Entry(nil, header.Require{"100rel", "Foo", "Bar"}, true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.Require) {
				if len(hdr1) > 0 {
					Expect(reflect.ValueOf(hdr2).Pointer()).ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
				}
			},
			Entry(nil, header.Require(nil)),
			Entry(nil, header.Require{}),
			Entry(nil, header.Require{"100rel", "Foo", "Bar"}),
			// endregion
		)
	})
})
