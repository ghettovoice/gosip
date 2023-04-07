package header_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Min-Expires", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Min-Expires: abc", &header.Any{Name: "Min-Expires", Value: "abc"}, nil),
			Entry(nil, "Min-Expires: 0", &header.MinExpires{}, nil),
			Entry(nil, "Min-Expires: 3600", &header.MinExpires{3600 * time.Second}, nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (*header.MinExpires)(nil), ""),
			Entry(nil, &header.MinExpires{}, "Min-Expires: 0"),
			Entry(nil, &header.MinExpires{3600 * time.Second}, "Min-Expires: 3600"),
			Entry(nil, &header.MinExpires{3600500 * time.Millisecond}, "Min-Expires: 3600"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, (*header.MinExpires)(nil), nil, false),
			Entry(nil, (*header.MinExpires)(nil), (*header.MinExpires)(nil), true),
			Entry(nil, &header.MinExpires{}, (*header.MinExpires)(nil), false),
			Entry(nil, &header.MinExpires{3600 * time.Second}, &header.MinExpires{10 * time.Second}, false),
			Entry(nil, &header.MinExpires{3600 * time.Second}, &header.MinExpires{3600 * time.Second}, true),
			Entry(nil, &header.MinExpires{3600 * time.Second}, &header.MinExpires{3600500 * time.Millisecond}, true),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (*header.MinExpires)(nil), false),
			Entry(nil, &header.MinExpires{}, true),
			Entry(nil, &header.MinExpires{60 * time.Second}, true),
			Entry(nil, &header.MinExpires{60500 * time.Millisecond}, true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 *header.MinExpires) {},
			Entry(nil, (*header.MinExpires)(nil)),
			Entry(nil, &header.MinExpires{3600 * time.Millisecond}),
			// endregion
		)
	})
})
