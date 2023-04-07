package header_test

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Subject", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Subject:", header.Subject(""), nil),
			Entry(nil, "Subject: ", header.Subject(""), nil),
			Entry(nil, "Subject: Tech Support", header.Subject("Tech Support"), nil),
			Entry(nil, "s: Tech Support", header.Subject("Tech Support"), nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.Subject(""), "Subject: "),
			Entry(nil, header.Subject("Tech Support"), "Subject: Tech Support"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.Subject(""), nil, false),
			Entry(nil, header.Subject("Tech Support"), header.Subject(""), false),
			Entry(nil, header.Subject("Tech Support"), header.Subject("Tech Support"), true),
			Entry(nil, header.Subject("Tech Support"), header.Subject("tech support"), false),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.Subject(""), true),
			Entry(nil, header.Subject("Tech Support"), true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.Subject) {},
			Entry(nil, header.Subject("")),
			Entry(nil, header.Subject("Tech Support")),
			// endregion
		)
	})
})
