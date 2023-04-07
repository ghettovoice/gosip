package header_test

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Priority", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Priority: ", &header.Any{Name: "Priority"}, nil),
			Entry(nil, "Priority: non-urgent", header.Priority("non-urgent"), nil),
			Entry(nil, "Priority: aaa-bbb-ccc", header.Priority("aaa-bbb-ccc"), nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.Priority(""), "Priority: "),
			Entry(nil, header.Priority("non-urgent"), "Priority: non-urgent"),
			Entry(nil, header.Priority("AAA-bbb-CCC"), "Priority: AAA-bbb-CCC"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.Priority(""), nil, false),
			Entry(nil, header.Priority(""), header.Priority(""), true),
			Entry(nil, header.Priority("non-urgent"), header.Priority(""), false),
			Entry(nil, header.Priority("non-urgent"), header.Priority("non-urgent"), true),
			Entry(nil, header.Priority("non-urgent"), header.Priority("NON-URGENT"), true),
			Entry(nil, header.Priority("non-urgent"), header.Priority("aaa-bbb-ccc"), false),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.Priority(""), false),
			Entry(nil, header.Priority("non-urgent"), true),
			Entry(nil, header.Priority("aaa-bbb-ccc"), true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.Priority) {},
			Entry(nil, header.Priority("")),
			Entry(nil, header.Priority("non-urgent")),
			Entry(nil, header.Priority("aaa-bbb-ccc")),
			// endregion
		)
	})
})
