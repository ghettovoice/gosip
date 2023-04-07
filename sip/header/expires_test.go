package header_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Expires", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Expires: abc", &header.Any{Name: "Expires", Value: "abc"}, nil),
			Entry(nil, "Expires: 0", &header.Expires{}, nil),
			Entry(nil, "Expires: 3600", &header.Expires{3600 * time.Second}, nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (*header.Expires)(nil), ""),
			Entry(nil, &header.Expires{}, "Expires: 0"),
			Entry(nil, &header.Expires{3600 * time.Second}, "Expires: 3600"),
			Entry(nil, &header.Expires{3600500 * time.Millisecond}, "Expires: 3600"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, (*header.Expires)(nil), nil, false),
			Entry(nil, (*header.Expires)(nil), (*header.Expires)(nil), true),
			Entry(nil, &header.Expires{}, (*header.Expires)(nil), false),
			Entry(nil, &header.Expires{3600 * time.Second}, &header.Expires{10 * time.Second}, false),
			Entry(nil, &header.Expires{3600 * time.Second}, &header.Expires{3600 * time.Second}, true),
			Entry(nil, &header.Expires{3600 * time.Second}, &header.Expires{3600500 * time.Millisecond}, true),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (*header.Expires)(nil), false),
			Entry(nil, &header.Expires{}, true),
			Entry(nil, &header.Expires{60 * time.Second}, true),
			Entry(nil, &header.Expires{60500 * time.Millisecond}, true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 *header.Expires) {},
			Entry(nil, (*header.Expires)(nil)),
			Entry(nil, &header.Expires{3600 * time.Millisecond}),
			// endregion
		)
	})
})
