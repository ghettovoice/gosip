package header_test

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("User-Agent", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "User-Agent:", &header.Any{Name: "User-Agent"}, nil),
			Entry(nil, "User-Agent: ", &header.Any{Name: "User-Agent"}, nil),
			Entry(nil, "User-Agent: abc/v2 (DEF)", header.UserAgent("abc/v2 (DEF)"), nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.UserAgent(""), "User-Agent: "),
			Entry(nil, header.UserAgent("abc/v2 (DEF)"), "User-Agent: abc/v2 (DEF)"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.UserAgent(""), nil, false),
			Entry(nil, header.UserAgent("abc/v2 (DEF)"), header.UserAgent(""), false),
			Entry(nil, header.UserAgent("abc/v2 (DEF)"), header.UserAgent("abc/v2 (DEF)"), true),
			Entry(nil, header.UserAgent("abc/v2 (DEF)"), header.UserAgent("abc/v2 (def)"), false),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.UserAgent(""), false),
			Entry(nil, header.UserAgent("a"), true),
			Entry(nil, header.UserAgent("abc/v2 (DEF)"), true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.UserAgent) {},
			Entry(nil, header.UserAgent("")),
			Entry(nil, header.UserAgent("abc/v2 (DEF)")),
			// endregion
		)
	})
})
