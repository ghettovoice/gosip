package header_test

import (
	"reflect"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Timestamp", func() {
		reqTstamp := time.Date(2000, time.January, 1, 12, 30, 45, 350*1e6, time.UTC)

		assertHeaderParsing(
			// region
			Entry(nil, "Timestamp: ", &header.Any{Name: "Timestamp"}, nil),
			Entry(nil, "Timestamp: 0.543", &header.Timestamp{ReqTime: time.Unix(0, 543*1e6).UTC()}, nil),
			Entry(nil,
				"Timestamp: 946729845.350 5.32575",
				&header.Timestamp{ReqTime: reqTstamp, ResDelay: 5325750 * time.Microsecond},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (*header.Timestamp)(nil), ""),
			Entry(nil, &header.Timestamp{}, "Timestamp: 0"),
			Entry(nil, &header.Timestamp{ReqTime: reqTstamp}, "Timestamp: 946729845.350"),
			Entry(nil,
				&header.Timestamp{ReqTime: time.Unix(0, 543*1e6).UTC(), ResDelay: 5325750 * time.Microsecond},
				"Timestamp: 0.543 5.326",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, (*header.Timestamp)(nil), nil, false),
			Entry(nil, (*header.Timestamp)(nil), (*header.Timestamp)(nil), true),
			Entry(nil, &header.Timestamp{}, (*header.Timestamp)(nil), false),
			Entry(nil, &header.Timestamp{}, &header.Timestamp{}, true),
			Entry(nil,
				&header.Timestamp{ReqTime: time.Unix(0, 543*1e6).UTC(), ResDelay: 5325750 * time.Microsecond},
				header.Timestamp{ReqTime: time.Unix(0, 543*1e6).UTC(), ResDelay: 5325750 * time.Microsecond},
				true,
			),
			Entry(nil,
				&header.Timestamp{ReqTime: reqTstamp, ResDelay: 5325750 * time.Microsecond},
				&header.Timestamp{ReqTime: reqTstamp, ResDelay: 5325751 * time.Microsecond},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (*header.Timestamp)(nil), false),
			Entry(nil, &header.Timestamp{}, false),
			Entry(nil, &header.Timestamp{ReqTime: time.Time{}}, false),
			Entry(nil, &header.Timestamp{ReqTime: time.Unix(0, 543*1e6).UTC()}, true),
			Entry(nil, &header.Timestamp{ReqTime: time.Now().UTC(), ResDelay: 5325750 * time.Microsecond}, true),
			Entry(nil, &header.Timestamp{ReqTime: time.Unix(1100, 543*1e6).UTC(), ResDelay: -time.Nanosecond}, false),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 *header.Timestamp) {
				Expect(reflect.ValueOf(hdr2).Pointer()).ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
			},
			Entry(nil, (*header.Timestamp)(nil)),
			Entry(nil, &header.Timestamp{}),
			Entry(nil, &header.Timestamp{ReqTime: time.Unix(0, 543*1e6).UTC()}),
			Entry(nil, &header.Timestamp{ReqTime: reqTstamp, ResDelay: 5325750 * time.Microsecond}),
			// endregion
		)
	})
})
