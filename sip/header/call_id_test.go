package header_test

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Call-ID", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Call-ID:", &header.Any{Name: "Call-ID"}, nil),
			Entry(nil, "Call-ID: qweRTY", header.CallID("qweRTY"), nil),
			Entry(nil, "Call-Id: qweRTY", header.CallID("qweRTY"), nil),
			Entry(nil, "i: qweRTY", header.CallID("qweRTY"), nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.CallID(""), "Call-ID: "),
			Entry(nil, header.CallID("qweRTY"), "Call-ID: qweRTY"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.CallID(""), nil, false),
			Entry(nil, header.CallID(""), header.CallID(""), true),
			Entry(nil, header.CallID("qweRTY"), header.CallID("qweRTY"), true),
			Entry(nil, header.CallID("qweRTY"), header.CallID("qwerty"), false),
			Entry(nil, header.CallID("qweRTY"), "qweRTY", false),
			Entry(nil,
				header.CallID("qweRTY"),
				func() *header.CallID {
					hdr := header.CallID("qweRTY")
					return &hdr
				}(),
				true,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.CallID(""), false),
			Entry(nil, header.CallID("qweRTY"), true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.CallID) {},
			Entry(nil, header.CallID("")),
			Entry(nil, header.CallID("qweRTY")),
			// endregion
		)
	})
})
