package header_test

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("MIME-Version", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "MIME-Version: ", &header.Any{Name: "MIME-Version"}, nil),
			Entry(nil, "MIME-Version: 1.5", header.MIMEVersion("1.5"), nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.MIMEVersion(""), "MIME-Version: "),
			Entry(nil, header.MIMEVersion("1.5"), "MIME-Version: 1.5"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.MIMEVersion(""), nil, false),
			Entry(nil, header.MIMEVersion(""), header.MIMEVersion(""), true),
			Entry(nil, header.MIMEVersion("1.5"), header.MIMEVersion("1.5"), true),
			Entry(nil, header.MIMEVersion("1.5"), header.MIMEVersion("2.0"), false),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.MIMEVersion(""), false),
			Entry(nil, header.MIMEVersion("1.5"), true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.MIMEVersion) {},
			Entry(nil, header.MIMEVersion("1.5")),
			// endregion
		)
	})
})
