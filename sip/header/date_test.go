package header_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Date", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Date: abc", &header.Any{Name: "Date", Value: "abc"}, nil),
			Entry(nil,
				"Date: Sat, 13 Nov 2010 23:29:00 GMT",
				&header.Date{time.Date(2010, 11, 13, 23, 29, 00, 0, time.UTC)},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (*header.Date)(nil), ""),
			Entry(nil, &header.Date{}, "Date: Mon, 01 Jan 0001 00:00:00 GMT"),
			Entry(nil,
				&header.Date{time.Date(2010, 11, 13, 23, 29, 00, 0, time.UTC)},
				"Date: Sat, 13 Nov 2010 23:29:00 GMT",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, (*header.Date)(nil), nil, false),
			Entry(nil, (*header.Date)(nil), (*header.Date)(nil), true),
			Entry(nil, &header.Date{}, (*header.Date)(nil), false),
			Entry(nil,
				&header.Date{time.Date(2010, 11, 13, 23, 29, 00, 0, time.UTC)},
				header.Date{time.Date(2010, 11, 13, 23, 29, 00, 0, time.UTC)},
				true,
			),
			Entry(nil,
				&header.Date{time.Date(2019, 4, 13, 23, 29, 00, 0, time.UTC)},
				&header.Date{time.Date(2010, 11, 13, 23, 29, 00, 0, time.UTC)},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (*header.Date)(nil), false),
			Entry(nil, &header.Date{}, false),
			Entry(nil, &header.Date{time.Now()}, true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 *header.Date) {},
			Entry(nil, (*header.Date)(nil)),
			Entry(nil, &header.Date{time.Now()}),
			// endregion
		)
	})
})
