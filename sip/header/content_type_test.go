package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Content-Type", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Content-Type: ", &header.Any{Name: "Content-Type"}, nil),
			Entry(nil,
				"Content-Type: application/sdp;\r\n\tcharset=UTF-8",
				&header.ContentType{
					Type:    "application",
					Subtype: "sdp",
					Params:  make(header.Values).Set("charset", "UTF-8"),
				},
				nil,
			),
			Entry(nil,
				"c: application/sdp;\r\n\tcharset=UTF-8",
				&header.ContentType{
					Type:    "application",
					Subtype: "sdp",
					Params:  make(header.Values).Set("charset", "UTF-8"),
				},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (*header.ContentType)(nil), ""),
			Entry(nil, &header.ContentType{}, "Content-Type: /"),
			Entry(nil,
				&header.ContentType{
					Type:    "application",
					Subtype: "sdp",
					Params:  make(header.Values).Set("charset", "UTF-8"),
				},
				"Content-Type: application/sdp;charset=UTF-8",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, (*header.ContentType)(nil), nil, false),
			Entry(nil, (*header.ContentType)(nil), (*header.ContentType)(nil), true),
			Entry(nil, &header.ContentType{}, (*header.ContentType)(nil), false),
			Entry(nil, &header.ContentType{}, &header.ContentType{}, true),
			Entry(nil,
				&header.ContentType{},
				&header.ContentType{Type: "application", Subtype: "sdp"},
				false,
			),
			Entry(nil,
				&header.ContentType{
					Type:    "TEXT",
					Subtype: "PLAIN",
					Params:  make(header.Values).Set("charset", "UTF-8"),
				},
				header.ContentType{
					Type:    "TEXT",
					Subtype: "PLAIN",
					Params:  make(header.Values).Set("charset", "UTF-8"),
				},
				true,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (*header.ContentType)(nil), false),
			Entry(nil, &header.ContentType{}, false),
			Entry(nil, &header.ContentType{Type: "text"}, false),
			Entry(nil, &header.ContentType{Subtype: "plain"}, false),
			Entry(nil, &header.ContentType{Type: "text", Subtype: "plain"}, true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 *header.ContentType) {
				Expect(reflect.ValueOf(hdr2).Pointer()).ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
				if hdr1.Params != nil {
					Expect(reflect.ValueOf(hdr2.Params).Pointer()).ToNot(Equal(reflect.ValueOf(hdr1.Params).Pointer()))
				}
			},
			Entry(nil, (*header.ContentType)(nil)),
			Entry(nil, &header.ContentType{}),
			Entry(nil, &header.ContentType{Type: "text", Subtype: "plain"}),
			Entry(nil, &header.ContentType{
				Type:    "text",
				Subtype: "plain",
				Params:  make(header.Values).Set("charset", "UTF-8"),
			}),
			// endregion
		)
	})
})
