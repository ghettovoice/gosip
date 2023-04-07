package header_test

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Content-Length", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Content-Length: ", &header.Any{Name: "Content-Length"}, nil),
			Entry(nil, "Content-Length: abc", &header.Any{Name: "Content-Length", Value: "abc"}, nil),
			Entry(nil, "Content-Length: 123", header.ContentLength(123), nil),
			Entry(nil, "l: 123", header.ContentLength(123), nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.ContentLength(0), "Content-Length: 0"),
			Entry(nil, header.ContentLength(123), "Content-Length: 123"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.ContentLength(0), nil, false),
			Entry(nil, header.ContentLength(0), header.ContentLength(0), true),
			Entry(nil, header.ContentLength(123), header.ContentLength(123), true),
			Entry(nil, header.ContentLength(123), header.ContentLength(456), false),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.ContentLength(0), true),
			Entry(nil, header.ContentLength(123), true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.ContentLength) {},
			Entry(nil, header.ContentLength(0)),
			Entry(nil, header.ContentLength(123)),
			// endregion
		)
	})
})
