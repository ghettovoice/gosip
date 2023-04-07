package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Allow", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Allow:", header.Allow{}, nil),
			Entry(nil,
				"Allow:\r\n\tINVITE,   ACK,\r\n\tABC",
				header.Allow{"INVITE", "ACK", "ABC"},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (header.Allow)(nil), ""),
			Entry(nil, header.Allow{}, "Allow: "),
			Entry(nil,
				header.Allow{"INVITE", "ACK", "ABC"},
				"Allow: INVITE, ACK, ABC",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, (header.Allow)(nil), nil, false),
			Entry(nil, (header.Allow)(nil), (header.Allow)(nil), true),
			Entry(nil, header.Allow{}, (header.Allow)(nil), true),
			Entry(nil, header.Allow{}, header.Allow{}, true),
			Entry(nil, header.Allow{"INVITE"}, header.Allow{}, false),
			Entry(nil,
				header.Allow{"INVITE", "BYE"},
				header.Allow{"INVITE", "BYE"},
				true,
			),
			Entry(nil,
				header.Allow{"INVITE", "BYE"},
				header.Allow{"BYE", "INVITE"},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (header.Allow)(nil), false),
			Entry(nil, header.Allow{}, true),
			Entry(nil, header.Allow{"INVITE", "abc"}, true),
			Entry(nil, header.Allow{"a b c"}, false),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.Allow) {
				if len(hdr1) > 0 {
					Expect(reflect.ValueOf(hdr2).Pointer()).ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
				}
			},
			Entry(nil, (header.Allow)(nil)),
			Entry(nil, header.Allow{}),
			Entry(nil, header.Allow{"INVITE", "ACK", "ABC"}),
			// endregion
		)
	})
})
