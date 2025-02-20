package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/uri"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Error-Info", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Error-Info: ", &header.Any{Name: "Error-Info"}, nil),
			Entry(nil, "Error-Info: abc", &header.Any{Name: "Error-Info", Value: "abc"}, nil),
			Entry(nil,
				"Error-Info: <sip:not-in-service-recording@atlanta.com;p1=abc>;p2=zzz,\r\n"+
					"\t<http://example.org/qwerty>",
				header.ErrorInfo{
					{
						URI: &uri.SIP{
							User:   uri.User("not-in-service-recording"),
							Addr:   uri.Host("atlanta.com"),
							Params: make(header.Values).Set("p1", "abc"),
						},
						Params: make(header.Values).Set("p2", "zzz"),
					},
					{
						URI: &uri.Any{Scheme: "http", Host: "example.org", Path: "/qwerty"},
					},
				},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.ErrorInfo(nil), ""),
			Entry(nil, header.ErrorInfo{}, "Error-Info: "),
			Entry(nil,
				header.ErrorInfo{
					{
						URI: &uri.SIP{
							User:   uri.User("not-in-service-recording"),
							Addr:   uri.Host("atlanta.com"),
							Params: make(header.Values).Set("p1", "abc"),
						},
						Params: make(header.Values).Set("p2", "zzz"),
					},
					{
						URI: &uri.Any{Scheme: "http", Host: "example.org", Path: "/qwerty"},
					},
				},
				"Error-Info: <sip:not-in-service-recording@atlanta.com;p1=abc>;p2=zzz, <http://example.org/qwerty>",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.ErrorInfo(nil), nil, false),
			Entry(nil, header.ErrorInfo(nil), header.ErrorInfo(nil), true),
			Entry(nil, header.ErrorInfo{}, header.ErrorInfo(nil), true),
			Entry(nil,
				header.ErrorInfo{
					{
						URI: &uri.SIP{
							User:   uri.User("not-in-service-recording"),
							Addr:   uri.Host("atlanta.com"),
							Params: make(header.Values).Set("p1", "abc"),
						},
						Params: make(header.Values).Set("p2", "zzz"),
					},
					{
						URI: &uri.Any{Scheme: "http", Host: "example.org", Path: "/qwerty"},
					},
				},
				header.ErrorInfo{
					{
						URI: &uri.SIP{
							User:   uri.User("not-in-service-recording"),
							Addr:   uri.Host("atlanta.com"),
							Params: make(header.Values).Set("p1", "abc"),
						},
						Params: make(header.Values).Set("p2", "zzz"),
					},
					{
						URI: &uri.Any{Scheme: "http", Host: "example.org", Path: "/qwerty"},
					},
				},
				true,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.ErrorInfo(nil), false),
			Entry(nil, header.ErrorInfo{}, false),
			Entry(nil, header.ErrorInfo{{URI: (*uri.SIP)(nil)}}, false),
			Entry(nil,
				header.ErrorInfo{
					{
						URI: &uri.SIP{
							User:   uri.User("not-in-service-recording"),
							Addr:   uri.Host("atlanta.com"),
							Params: make(header.Values).Set("p1", "abc"),
						},
						Params: make(header.Values).Set("p2", "zzz"),
					},
					{
						URI: &uri.Any{Scheme: "http", Host: "example.org", Path: "/qwerty"},
					},
				},
				true,
			),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.ErrorInfo) {
				if len(hdr1) > 0 {
					Expect(reflect.ValueOf(hdr2).Pointer()).
						ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
					for i := range hdr1 {
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
			Entry(nil, header.ErrorInfo(nil)),
			Entry(nil, header.ErrorInfo{}),
			Entry(nil,
				header.ErrorInfo{
					{
						URI: &uri.SIP{
							User:   uri.User("not-in-service-recording"),
							Addr:   uri.Host("atlanta.com"),
							Params: make(header.Values).Set("p1", "abc"),
						},
						Params: make(header.Values).Set("p2", "zzz"),
					},
					{
						URI: &uri.Any{Scheme: "http", Host: "example.org", Path: "/qwerty"},
					},
				},
			),
			// endregion
		)
	})
})
