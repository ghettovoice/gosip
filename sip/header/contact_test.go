package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/uri"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Contact", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Contact:", &header.Any{Name: "Contact"}, nil),
			Entry(nil, "Contact: *", header.Contact{}, nil),
			Entry(nil,
				"Contact: sips:alice@127.0.0.1;tag=a48s",
				header.Contact{{
					URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("127.0.0.1"), Secured: true},
					Params: make(header.Values).Set("tag", "a48s"),
				}},
				nil,
			),
			Entry(nil,
				"Contact: https://example.org/username?tag=a48s",
				header.Contact{{
					URI:    &uri.Any{Scheme: "https", Host: "example.org", Path: "/username"},
					Params: make(header.Values).Set("tag", "a48s"),
				}},
				nil,
			),
			Entry(nil,
				"Contact: \"A. G. Bell\" <sip:agb@bell-telephone.com>\r\n\t;tag=a48s",
				header.Contact{{
					DisplayName: "A. G. Bell",
					URI:         &uri.SIP{User: uri.User("agb"), Addr: uri.Host("bell-telephone.com")},
					Params:      make(header.Values).Set("tag", "a48s"),
				}},
				nil,
			),
			Entry(nil,
				"Contact: \"Mr. Watson\" <sip:watson@worcester.bell-telephone.com>\r\n"+
					"\t;q=0.7; expires=3600,\r\n"+
					"\t\"Mr. Watson\" <mailto:watson@bell-telephone.com> ;q=0.1",
				header.Contact{
					{
						DisplayName: "Mr. Watson",
						URI: &uri.SIP{
							User: uri.User("watson"),
							Addr: uri.Host("worcester.bell-telephone.com"),
						},
						Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
					},
					{
						DisplayName: "Mr. Watson",
						URI:         &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
						Params:      make(header.Values).Set("q", "0.1"),
					},
				},
				nil,
			),
			Entry(nil,
				"m: <sips:bob@192.0.2.4;transport=UDP>;expires=60",
				header.Contact{
					{
						URI: &uri.SIP{
							Secured: true,
							User:    uri.User("bob"),
							Addr:    uri.Host("192.0.2.4"),
							Params:  make(header.Values).Set("transport", "UDP"),
						},
						Params: make(header.Values).Set("expires", "60"),
					},
				},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (header.Contact)(nil), ""),
			Entry(nil, header.Contact{}, "Contact: *"),
			Entry(nil,
				header.Contact{
					{
						DisplayName: "Mr. Watson",
						URI: &uri.SIP{
							User: uri.User("watson"),
							Addr: uri.Host("worcester.bell-telephone.com"),
						},
						Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
					},
					{
						DisplayName: "Mr. Watson",
						URI:         &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
						Params:      make(header.Values).Set("q", "0.1"),
					},
				},
				"Contact: \"Mr. Watson\" <sip:watson@worcester.bell-telephone.com>;q=0.7;expires=3600, "+
					"\"Mr. Watson\" <mailto:watson@bell-telephone.com>;q=0.1",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.Contact{}, nil, false),
			Entry(nil, header.Contact{}, header.Contact(nil), true),
			Entry(nil, header.Contact(nil), header.Contact(nil), true),
			Entry(nil, header.Contact{}, header.Contact{}, true),
			Entry(nil,
				header.Contact{
					{
						DisplayName: "Mr. Watson",
						URI: &uri.SIP{
							User: uri.User("watson"),
							Addr: uri.Host("worcester.bell-telephone.com"),
						},
						Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
					},
				},
				header.Contact{
					{
						DisplayName: "Mr. Watson",
						URI:         &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
						Params:      make(header.Values).Set("q", "0.1"),
					},
				},
				false,
			),
			Entry(nil,
				header.Contact{
					{
						DisplayName: "Mr. Watson",
						URI: &uri.SIP{
							User: uri.User("watson"),
							Addr: uri.Host("worcester.bell-telephone.com"),
						},
						Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
					},
					{
						DisplayName: "Mr. Watson",
						URI:         &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
						Params:      make(header.Values).Set("q", "0.1"),
					},
				},
				header.Contact{
					{
						DisplayName: "Mr. Watson",
						URI: &uri.SIP{
							User: uri.User("watson"),
							Addr: uri.Host("worcester.bell-telephone.com"),
						},
						Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
					},
					{
						DisplayName: "Mr. Watson",
						URI:         &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
						Params:      make(header.Values).Set("q", "0.1").Set("a", "aaa"),
					},
				},
				true,
			),
			Entry(nil,
				header.Contact{
					{
						DisplayName: "Mr. Watson",
						URI: &uri.SIP{
							User: uri.User("watson"),
							Addr: uri.Host("worcester.bell-telephone.com"),
						},
						Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
					},
					{
						DisplayName: "Mr. Watson",
						URI:         &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
						Params:      make(header.Values).Set("q", "0.1"),
					},
				},
				header.Contact{
					{
						DisplayName: "Mr. Watson",
						URI:         &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
						Params:      make(header.Values).Set("q", "0.1"),
					},
					{
						DisplayName: "Mr. Watson",
						URI: &uri.SIP{
							User: uri.User("watson"),
							Addr: uri.Host("worcester.bell-telephone.com"),
						},
						Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
					},
				},
				false,
			),
			Entry(nil,
				header.Contact{
					{
						DisplayName: "Mr. Watson",
						URI: &uri.SIP{
							User: uri.User("watson"),
							Addr: uri.Host("worcester.bell-telephone.com"),
						},
						Params: make(header.Values).Set("q", "1"),
					},
				},
				header.Contact{
					{
						DisplayName: "Mr. Watson",
						URI: &uri.SIP{
							User: uri.User("watson"),
							Addr: uri.Host("worcester.bell-telephone.com"),
						},
						Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
					},
				},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (header.Contact)(nil), false),
			Entry(nil, header.Contact{}, true),
			Entry(nil,
				header.Contact{
					{
						URI: (*uri.Any)(nil),
					},
				},
				false,
			),
			Entry(nil,
				header.Contact{
					{
						DisplayName: "Mr. Watson",
						URI: &uri.SIP{
							User: uri.User("watson"),
							Addr: uri.Host("worcester.bell-telephone.com"),
						},
						Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
					},
					{
						DisplayName: "Mr. Watson",
						URI:         &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
						Params:      make(header.Values).Set("q", "0.1"),
					},
				},
				true,
			),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.Contact) {
				if len(hdr1) == 0 {
					return
				}

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
			},
			Entry(nil, header.Contact(nil)),
			Entry(nil, header.Contact{}),
			Entry(nil,
				header.Contact{
					{
						DisplayName: "Mr. Watson",
						URI: &uri.SIP{
							User: uri.User("watson"),
							Addr: uri.Host("worcester.bell-telephone.com"),
						},
						Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
					},
					{
						DisplayName: "Mr. Watson",
						URI:         &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
						Params:      make(header.Values).Set("q", "0.1"),
					},
				},
			),
			// endregion
		)
	})
})
