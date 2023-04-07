package header_test

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Server", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Server:", &header.Any{Name: "Server"}, nil),
			Entry(nil, "Server: ", &header.Any{Name: "Server"}, nil),
			Entry(nil, "Server: abc/v2 (DEF)", header.Server("abc/v2 (DEF)"), nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.Server(""), "Server: "),
			Entry(nil, header.Server("abc/v2 (DEF)"), "Server: abc/v2 (DEF)"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.Server(""), nil, false),
			Entry(nil, header.Server("abc/v2 (DEF)"), header.Server(""), false),
			Entry(nil, header.Server("abc/v2 (DEF)"), header.Server("abc/v2 (DEF)"), true),
			Entry(nil, header.Server("abc/v2 (DEF)"), header.Server("abc/v2 (def)"), false),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.Server(""), false),
			Entry(nil, header.Server("a"), true),
			Entry(nil, header.Server("abc/v2 (DEF)"), true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.Server) {},
			Entry(nil, header.Server("")),
			Entry(nil, header.Server("abc/v2 (DEF)")),
			// endregion
		)
	})
})
