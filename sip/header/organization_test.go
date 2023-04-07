package header_test

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Organization", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Organization:", header.Organization(""), nil),
			Entry(nil, "Organization: ", header.Organization(""), nil),
			Entry(nil, "Organization: Boxes by Bob", header.Organization("Boxes by Bob"), nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.Organization(""), "Organization: "),
			Entry(nil, header.Organization("Boxes by Bob"), "Organization: Boxes by Bob"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.Organization(""), nil, false),
			Entry(nil, header.Organization("Boxes by Bob"), header.Organization(""), false),
			Entry(nil, header.Organization("Boxes by Bob"), header.Organization("Boxes by Bob"), true),
			Entry(nil, header.Organization("Boxes by Bob"), header.Organization("Boxes By Bob"), false),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.Organization(""), true),
			Entry(nil, header.Organization("Boxes by Bob"), true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.Organization) {},
			Entry(nil, header.Organization("")),
			Entry(nil, header.Organization("Boxes by Bob")),
			// endregion
		)
	})
})
