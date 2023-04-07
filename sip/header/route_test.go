package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/uri"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Route", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Route: ", &header.Any{Name: "Route"}, nil),
			Entry(nil, "Route: abc", &header.Any{Name: "Route", Value: "abc"}, nil),
			Entry(nil,
				"Route: <sip:foo@bar;lr>;k=v,\r\n\t<sip:baz@qux>, <sip:quux@quuz>;a=b",
				header.Route{
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
			Entry(nil, header.Route(nil), ""),
			Entry(nil, header.Route{}, "Route: "),
			Entry(nil,
				header.Route{
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
				"Route: <sip:foo@bar;lr>;k=v, <sip:baz@qux>, <sip:quux@quuz>;a=b",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.Route(nil), nil, false),
			Entry(nil, header.Route(nil), header.Route(nil), true),
			Entry(nil, header.Route{}, header.Route(nil), true),
			Entry(nil, header.Route{}, header.Route{}, true),
			Entry(nil,
				header.Route{
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
				header.Route{
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
				header.Route{
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
				header.Route{
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
			Entry(nil, header.Route(nil), false),
			Entry(nil, header.Route{}, false),
			Entry(nil,
				header.Route{
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
			Entry(nil, header.Route{{URI: (*uri.SIP)(nil)}}, false),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.Route) {
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
			Entry(nil, header.Route(nil)),
			Entry(nil, header.Route{}),
			Entry(nil,
				header.Route{
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
