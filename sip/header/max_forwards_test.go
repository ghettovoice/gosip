package header_test

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Max-Forwards", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Max-Forwards: ", &header.Any{Name: "Max-Forwards"}, nil),
			Entry(nil, "Max-Forwards: 0", header.MaxForwards(0), nil),
			Entry(nil, "Max-Forwards: 10", header.MaxForwards(10), nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.MaxForwards(0), "Max-Forwards: 0"),
			Entry(nil, header.MaxForwards(10), "Max-Forwards: 10"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.MaxForwards(0), nil, false),
			Entry(nil, header.MaxForwards(0), header.MaxForwards(0), true),
			Entry(nil, header.MaxForwards(10), header.MaxForwards(10), true),
			Entry(nil, header.MaxForwards(0), header.MaxForwards(10), false),
			Entry(nil, header.MaxForwards(10), header.MaxForwards(0), false),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.MaxForwards(0), true),
			Entry(nil, header.MaxForwards(10), true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.MaxForwards) {},
			Entry(nil, header.MaxForwards(0)),
			Entry(nil, header.MaxForwards(10)),
			// endregion
		)
	})
})
