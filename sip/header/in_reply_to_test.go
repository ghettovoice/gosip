package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("In-Reply-To", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "In-Reply-To: ", &header.Any{Name: "In-Reply-To"}, nil),
			Entry(nil,
				"In-Reply-To: 70710@saturn.bell-tel.com, 17320@saturn.bell-tel.com",
				header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.InReplyTo(nil), ""),
			Entry(nil, header.InReplyTo{}, "In-Reply-To: "),
			Entry(nil,
				header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"},
				"In-Reply-To: 70710@saturn.bell-tel.com, 17320@saturn.bell-tel.com",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.InReplyTo(nil), nil, false),
			Entry(nil, header.InReplyTo(nil), header.InReplyTo(nil), true),
			Entry(nil, header.InReplyTo{}, header.InReplyTo(nil), true),
			Entry(nil,
				header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"},
				header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"},
				true,
			),
			Entry(nil,
				header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"},
				header.InReplyTo{"17320@saturn.bell-tel.com", "70710@saturn.bell-tel.com"},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.InReplyTo(nil), false),
			Entry(nil, header.InReplyTo{}, false),
			Entry(nil, header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"}, true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.InReplyTo) {
				if len(hdr1) == 0 {
					return
				}

				Expect(reflect.ValueOf(hdr2).Pointer()).
					ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
			},
			Entry(nil, header.InReplyTo(nil)),
			Entry(nil, header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"}),
			// endregion
		)
	})
})
