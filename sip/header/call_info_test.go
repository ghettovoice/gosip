package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/uri"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Call-Info", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Call-Info:", &header.Any{Name: "Call-Info"}, nil),
			Entry(nil,
				"Call-Info: <http://www.example.com/alice/photo.jpg> ;purpose=icon,\r\n"+
					"\t<http://www.example.com/alice/> ;purpose=info",
				header.CallInfo{
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/photo.jpg"},
						Params: make(header.Values).Set("purpose", "icon"),
					},
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/"},
						Params: make(header.Values).Set("purpose", "info"),
					},
				},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (header.CallInfo)(nil), ""),
			Entry(nil, header.CallInfo{}, "Call-Info: "),
			Entry(nil,
				header.CallInfo{
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/photo.jpg"},
						Params: make(header.Values).Set("purpose", "icon"),
					},
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/"},
						Params: make(header.Values).Set("purpose", "info"),
					},
				},
				"Call-Info: <http://www.example.com/alice/photo.jpg>;purpose=icon, <http://www.example.com/alice/>;purpose=info",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, (header.CallInfo)(nil), nil, false),
			Entry(nil, (header.CallInfo)(nil), (header.CallInfo)(nil), true),
			Entry(nil, header.CallInfo{}, (header.CallInfo)(nil), true),
			Entry(nil, header.CallInfo{}, header.CallInfo{}, true),
			Entry(nil,
				header.CallInfo{
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/photo.jpg"},
						Params: make(header.Values).Set("purpose", "icon"),
					},
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/"},
						Params: make(header.Values).Set("purpose", "info"),
					},
				},
				header.CallInfo{},
				false,
			),
			Entry(nil,
				header.CallInfo{
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/photo.jpg"},
						Params: make(header.Values).Set("purpose", "icon"),
					},
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/"},
						Params: make(header.Values).Set("purpose", "info"),
					},
				},
				header.CallInfo{
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/"},
						Params: make(header.Values).Set("purpose", "info"),
					},
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/photo.jpg"},
						Params: make(header.Values).Set("purpose", "icon"),
					},
				},
				false,
			),
			Entry(nil,
				header.CallInfo{
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/photo.jpg"},
						Params: make(header.Values).Set("purpose", "icon"),
					},
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/"},
						Params: make(header.Values).Set("purpose", "info"),
					},
				},
				header.CallInfo{
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/photo.jpg"},
						Params: make(header.Values).Set("purpose", "icon"),
					},
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/"},
						Params: make(header.Values).Set("purpose", "info"),
					},
				},
				true,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (header.CallInfo)(nil), false),
			Entry(nil, header.CallInfo{}, false),
			Entry(nil,
				header.CallInfo{
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/photo.jpg"},
						Params: make(header.Values).Set("purpose", "icon"),
					},
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/"},
						Params: make(header.Values).Set("purpose", "info"),
					},
				},
				true,
			),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.CallInfo) {
				if len(hdr1) > 0 {
					Expect(reflect.ValueOf(hdr2).Pointer()).
						ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
					for i := 0; i < len(hdr1); i++ {
						if hdr1[i].URI == nil {
							Expect(hdr2[i].URI).To(BeNil())
						} else {
							Expect(reflect.ValueOf(hdr2[i].URI).Pointer()).
								ToNot(Equal(reflect.ValueOf(hdr1[i].URI).Pointer()))
						}
						if hdr1[i].Params == nil {
							Expect(hdr2[i].Params).To(BeNil())
						} else {
							Expect(reflect.ValueOf(hdr2[i].Params).Pointer()).
								ToNot(Equal(reflect.ValueOf(hdr1[i].Params).Pointer()))
						}
					}
				}
			},
			Entry(nil, (header.CallInfo)(nil)),
			Entry(nil, header.CallInfo{}),
			Entry(nil,
				header.CallInfo{
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/photo.jpg"},
						Params: make(header.Values).Set("purpose", "icon"),
					},
					{
						URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/"},
						Params: make(header.Values).Set("purpose", "info"),
					},
				},
			),
			// endregion
		)
	})
})
