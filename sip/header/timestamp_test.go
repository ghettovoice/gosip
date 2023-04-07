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
		assertHeaderParsing(
			// region
			Entry(nil, "Timestamp: ", &header.Any{Name: "Timestamp"}, nil),
			Entry(nil, "Timestamp: 0.543", &header.Timestamp{ReqTstamp: 543 * time.Millisecond}, nil),
			Entry(nil,
				"Timestamp: 0.543 5.32575",
				&header.Timestamp{ReqTstamp: 543 * time.Millisecond, ResDelay: 5325750 * time.Microsecond},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (*header.Timestamp)(nil), ""),
			Entry(nil, &header.Timestamp{}, "Timestamp: 0"),
			Entry(nil, &header.Timestamp{ReqTstamp: 543 * time.Millisecond}, "Timestamp: 0.543"),
			Entry(nil,
				&header.Timestamp{ReqTstamp: 543 * time.Millisecond, ResDelay: 5325750 * time.Microsecond},
				"Timestamp: 0.543 5.32575",
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
				&header.Timestamp{ReqTstamp: 543 * time.Millisecond, ResDelay: 5325750 * time.Microsecond},
				header.Timestamp{ReqTstamp: 543 * time.Millisecond, ResDelay: 5325750 * time.Microsecond},
				true,
			),
			Entry(nil,
				&header.Timestamp{ReqTstamp: 543 * time.Millisecond, ResDelay: 5325750 * time.Microsecond},
				&header.Timestamp{ReqTstamp: 543 * time.Millisecond, ResDelay: 5325751 * time.Microsecond},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (*header.Timestamp)(nil), false),
			Entry(nil, &header.Timestamp{}, true),
			Entry(nil, &header.Timestamp{ReqTstamp: 543 * time.Millisecond}, true),
			Entry(nil, &header.Timestamp{ReqTstamp: 543 * time.Millisecond, ResDelay: 5325750 * time.Microsecond}, true),
			Entry(nil, &header.Timestamp{ReqTstamp: -time.Nanosecond}, false),
			Entry(nil, &header.Timestamp{ReqTstamp: time.Second, ResDelay: -time.Nanosecond}, false),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 *header.Timestamp) {
				Expect(reflect.ValueOf(hdr2).Pointer()).ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
			},
			Entry(nil, (*header.Timestamp)(nil)),
			Entry(nil, &header.Timestamp{}),
			Entry(nil, &header.Timestamp{ReqTstamp: 543 * time.Millisecond}),
			Entry(nil, &header.Timestamp{ReqTstamp: 543 * time.Millisecond, ResDelay: 5325750 * time.Microsecond}),
			// endregion
		)
	})
})
