package header_test

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Content-Encoding", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Content-Encoding: ", &header.Any{Name: "Content-Encoding"}, nil),
			Entry(nil, "Content-Encoding: gzip, QWE", header.ContentEncoding{"gzip", "QWE"}, nil),
			Entry(nil, "e: gzip, QWE", header.ContentEncoding{"gzip", "QWE"}, nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.ContentEncoding(nil), ""),
			Entry(nil, header.ContentEncoding{}, "Content-Encoding: "),
			Entry(nil, header.ContentEncoding{"qwe", "ZIP", "tar"}, "Content-Encoding: qwe, ZIP, tar"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.ContentEncoding(nil), nil, false),
			Entry(nil, header.ContentEncoding(nil), header.ContentEncoding(nil), true),
			Entry(nil, header.ContentEncoding{}, header.ContentEncoding(nil), true),
			Entry(nil, header.ContentEncoding{}, header.ContentEncoding{}, true),
			Entry(nil, header.ContentEncoding{"identity"}, header.ContentEncoding{}, false),
			Entry(nil, header.ContentEncoding{"gzip", "TAR"}, header.ContentEncoding{"gzip", "tar"}, true),
			Entry(nil, header.ContentEncoding{"tar", "gzip"}, header.ContentEncoding{"gzip", "tar"}, false),
			Entry(nil, header.ContentEncoding{"tar"}, header.ContentEncoding{"tar", "qwe"}, false),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.ContentEncoding(nil), false),
			Entry(nil, header.ContentEncoding{}, false),
			Entry(nil, header.ContentEncoding{"gzip", "QWE"}, true),
			Entry(nil, header.ContentEncoding{"t a r"}, false),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.ContentEncoding) {},
			Entry(nil, header.ContentEncoding(nil)),
			Entry(nil, header.ContentEncoding{}),
			Entry(nil, header.ContentEncoding{"gzip", "tar"}),
			// endregion
		)
	})
})
