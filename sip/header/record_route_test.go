package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/uri"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Record-Route", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Record-Route: ", &header.Any{Name: "Record-Route"}, nil),
			Entry(nil, "Record-Route: abc", &header.Any{Name: "Record-Route", Value: "abc"}, nil),
			Entry(nil,
				"Record-Route: <sip:foo@bar;lr>;k=v,\r\n\t<sip:baz@qux>, <sip:quux@quuz>;a=b",
				header.RecordRoute{
					{
						URI: &uri.SIP{
							User:   uri.User("foo"),
							Addr:   uri.Host("bar"),
							Params: make(header.Values).Set("lr", ""),
						},
						Params: make(header.Values).Set("k", "v"),
					},
					{URI: &uri.SIP{User: uri.User("baz"), Addr: uri.Host("qux")}},
					{
						URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
						Params: make(header.Values).Set("a", "b"),
					},
				},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.RecordRoute(nil), ""),
			Entry(nil, header.RecordRoute{}, "Record-Route: "),
			Entry(nil,
				header.RecordRoute{
					{
						URI: &uri.SIP{
							User:   uri.User("foo"),
							Addr:   uri.Host("bar"),
							Params: make(header.Values).Set("lr", ""),
						},
						Params: make(header.Values).Set("k", "v"),
					},
					{URI: &uri.SIP{User: uri.User("baz"), Addr: uri.Host("qux")}},
					{
						URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
						Params: make(header.Values).Set("a", "b"),
					},
				},
				"Record-Route: <sip:foo@bar;lr>;k=v, <sip:baz@qux>, <sip:quux@quuz>;a=b",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.RecordRoute(nil), nil, false),
			Entry(nil, header.RecordRoute(nil), header.RecordRoute(nil), true),
			Entry(nil, header.RecordRoute{}, header.RecordRoute(nil), true),
			Entry(nil, header.RecordRoute{}, header.RecordRoute{}, true),
			Entry(nil,
				header.RecordRoute{
					{
						URI: &uri.SIP{
							User:   uri.User("foo"),
							Addr:   uri.Host("bar"),
							Params: make(header.Values).Set("lr", ""),
						},
						Params: make(header.Values).Set("k", "v"),
					},
					{URI: &uri.SIP{User: uri.User("baz"), Addr: uri.Host("qux")}},
					{
						URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
						Params: make(header.Values).Set("a", "b"),
					},
				},
				header.RecordRoute{
					{
						URI: &uri.SIP{
							User:   uri.User("foo"),
							Addr:   uri.Host("bar"),
							Params: make(header.Values).Set("lr", ""),
						},
						Params: make(header.Values).Set("a", "b"),
					},
					{URI: &uri.SIP{User: uri.User("baz"), Addr: uri.Host("qux")}},
					{
						URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
						Params: make(header.Values).Set("k", "v"),
					},
				},
				true,
			),
			Entry(nil,
				header.RecordRoute{
					{
						URI: &uri.SIP{
							User:   uri.User("foo"),
							Addr:   uri.Host("bar"),
							Params: make(header.Values).Set("lr", ""),
						},
						Params: make(header.Values).Set("k", "v"),
					},
					{URI: &uri.SIP{User: uri.User("baz"), Addr: uri.Host("qux")}},
					{
						URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
						Params: make(header.Values).Set("a", "b"),
					},
				},
				header.RecordRoute{
					{URI: &uri.SIP{User: uri.User("baz"), Addr: uri.Host("qux")}},
					{
						URI: &uri.SIP{
							User:   uri.User("foo"),
							Addr:   uri.Host("bar"),
							Params: make(header.Values).Set("lr", ""),
						},
						Params: make(header.Values).Set("k", "v"),
					},
					{
						URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
						Params: make(header.Values).Set("a", "b"),
					},
				},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.RecordRoute(nil), false),
			Entry(nil, header.RecordRoute{}, false),
			Entry(nil,
				header.RecordRoute{
					{
						URI: &uri.SIP{
							User:   uri.User("foo"),
							Addr:   uri.Host("bar"),
							Params: make(header.Values).Set("lr", ""),
						},
						Params: make(header.Values).Set("k", "v"),
					},
					{URI: &uri.SIP{User: uri.User("baz"), Addr: uri.Host("qux")}},
					{
						URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
						Params: make(header.Values).Set("a", "b"),
					},
				},
				true,
			),
			Entry(nil, header.RecordRoute{{URI: (*uri.SIP)(nil)}}, false),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.RecordRoute) {
				if len(hdr1) > 0 {
					Expect(reflect.ValueOf(hdr2).Pointer()).ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
					for i := range hdr1 {
						if hdr1[i].URI == nil || reflect.ValueOf(hdr1[i].URI).IsNil() {
							Expect(hdr2[i].URI).To(BeNil())
						} else {
							Expect(reflect.ValueOf(hdr2[i].URI).Pointer()).ToNot(Equal(reflect.ValueOf(hdr1[i].URI).Pointer()))
						}
						if hdr1[i].Params == nil {
							Expect(hdr2[i].Params).To(BeNil())
						} else {
							Expect(reflect.ValueOf(hdr2[i].Params).Pointer()).ToNot(Equal(reflect.ValueOf(hdr1[i].Params).Pointer()))
						}
					}
				}
			},
			Entry(nil, header.RecordRoute(nil)),
			Entry(nil, header.RecordRoute{}),
			Entry(nil,
				header.RecordRoute{
					{
						URI: &uri.SIP{
							User:   uri.User("foo"),
							Addr:   uri.Host("bar"),
							Params: make(header.Values).Set("lr", ""),
						},
						Params: make(header.Values).Set("k", "v"),
					},
					{URI: &uri.SIP{User: uri.User("baz"), Addr: uri.Host("qux")}},
					{
						URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
						Params: make(header.Values).Set("a", "b"),
					},
				},
			),
			// endregion
		)
	})
})
