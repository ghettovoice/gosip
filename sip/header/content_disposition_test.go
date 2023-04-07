package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Content-Disposition", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Content-Disposition: ", &header.Any{Name: "Content-Disposition"}, nil),
			Entry(nil,
				"Content-Disposition: session ;\r\n\thandling=optional",
				&header.ContentDisposition{
					Type:   "session",
					Params: make(header.Values).Set("handling", "optional"),
				},
				nil,
			),
			Entry(nil,
				"Content-Disposition: custom ;\r\n\thandling=optional ; param=\"Hello world!\"",
				&header.ContentDisposition{
					Type: "custom",
					Params: make(header.Values).
						Set("handling", "optional").
						Set("param", `"Hello world!"`),
				},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (*header.ContentDisposition)(nil), ""),
			Entry(nil, &header.ContentDisposition{}, "Content-Disposition: "),
			Entry(nil,
				&header.ContentDisposition{
					Type: "Session",
					Params: make(header.Values).
						Set("handling", "optional").
						Set("param", `"Hello world!"`),
				},
				"Content-Disposition: Session;handling=optional;param=\"Hello world!\"",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, (*header.ContentDisposition)(nil), nil, false),
			Entry(nil, (*header.ContentDisposition)(nil), (*header.ContentDisposition)(nil), true),
			Entry(nil, &header.ContentDisposition{}, (*header.ContentDisposition)(nil), false),
			Entry(nil, &header.ContentDisposition{}, header.ContentDisposition{}, true),
			Entry(nil,
				&header.ContentDisposition{
					Type: "SESSION",
					Params: make(header.Values).
						Set("handling", "optional"),
				},
				header.ContentDisposition{
					Type: "session",
					Params: make(header.Values).
						Set("handling", "optional").
						Set("param", `"Hello world!"`),
				},
				true,
			),
			Entry(nil,
				&header.ContentDisposition{
					Type:   "session",
					Params: make(header.Values).Set("handling", "optional"),
				},
				&header.ContentDisposition{
					Type:   "session",
					Params: make(header.Values).Set("handling", "required"),
				},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (*header.ContentDisposition)(nil), false),
			Entry(nil, &header.ContentDisposition{}, false),
			Entry(nil,
				&header.ContentDisposition{
					Type:   "icon",
					Params: make(header.Values).Set("handling", "optional"),
				},
				true,
			),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 *header.ContentDisposition) {
				Expect(reflect.ValueOf(hdr2).Pointer()).ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
			},
			Entry(nil, (*header.ContentDisposition)(nil)),
			Entry(nil,
				&header.ContentDisposition{
					Type:   "icon",
					Params: make(header.Values).Set("handling", "optional"),
				},
			),
			// endregion
		)
	})
})
