package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/uri"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("From", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "From: ", &header.Any{Name: "From"}, nil),
			Entry(nil,
				"From: \"A. G. Bell\" <sip:agb@bell-telephone.com>\r\n\t;tag=a48s",
				&header.From{
					DisplayName: "A. G. Bell",
					URI:         &uri.SIP{User: uri.User("agb"), Addr: uri.Host("bell-telephone.com")},
					Params:      make(header.Values).Set("tag", "a48s"),
				},
				nil,
			),
			Entry(nil,
				"f: Anonymous <https://example.org/username>;tag=hyh8",
				&header.From{
					DisplayName: "Anonymous",
					URI:         &uri.Any{Scheme: "https", Host: "example.org", Path: "/username"},
					Params:      make(header.Values).Set("tag", "hyh8"),
				},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (*header.From)(nil), ""),
			Entry(nil, &header.From{}, "From: <>"),
			Entry(nil,
				&header.From{
					DisplayName: "A. G. Bell",
					URI: &uri.SIP{
						User:   uri.User("agb"),
						Addr:   uri.Host("bell-telephone.com"),
						Params: make(header.Values).Set("transport", "udp"),
					},
					Params: make(header.Values).Set("tag", "a48s"),
				},
				"From: \"A. G. Bell\" <sip:agb@bell-telephone.com;transport=udp>;tag=a48s",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, (*header.From)(nil), nil, false),
			Entry(nil, (*header.From)(nil), (*header.From)(nil), true),
			Entry(nil, &header.From{}, (*header.From)(nil), false),
			Entry(nil,
				&header.From{
					DisplayName: "A. G. Bell",
					URI: &uri.SIP{
						User:   uri.User("agb"),
						Addr:   uri.Host("bell-telephone.com"),
						Params: make(header.Values).Set("transport", "udp"),
					},
					Params: make(header.Values).Set("tag", "a48s"),
				},
				header.From{
					URI: &uri.SIP{
						User:   uri.User("agb"),
						Addr:   uri.Host("bell-telephone.com"),
						Params: make(header.Values).Set("transport", "udp"),
					},
					Params: make(header.Values).Set("tag", "a48s").Set("x", "abc"),
				},
				true,
			),
			Entry(nil,
				&header.From{
					DisplayName: "A. G. Bell",
					URI: &uri.SIP{
						User:   uri.User("agb"),
						Addr:   uri.Host("bell-telephone.com"),
						Params: make(header.Values).Set("transport", "udp"),
					},
					Params: make(header.Values).Set("tag", "a48s"),
				},
				&header.From{
					DisplayName: "A. G. Bell",
					URI: &uri.SIP{
						User: uri.User("AGB"),
						Addr: uri.Host("bell-telephone.com"),
					},
					Params: make(header.Values).Set("tag", "qwerty"),
				},
				false,
			),
			Entry(nil,
				&header.From{
					DisplayName: "A. G. Bell",
					URI: &uri.SIP{
						User:   uri.User("agb"),
						Addr:   uri.Host("bell-telephone.com"),
						Params: make(header.Values).Set("transport", "udp"),
					},
					Params: make(header.Values).Set("tag", "a48s").Set("x", "def"),
				},
				&header.From{
					URI: &uri.SIP{
						User:   uri.User("agb"),
						Addr:   uri.Host("bell-telephone.com"),
						Params: make(header.Values).Set("transport", "udp"),
					},
					Params: make(header.Values).Set("tag", "a48s").Set("x", "abc"),
				},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (*header.From)(nil), false),
			Entry(nil, &header.From{}, false),
			Entry(nil,
				&header.From{
					URI: &uri.SIP{Addr: uri.Host("bell-telephone.com")},
				},
				true,
			),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 *header.From) {
				Expect(reflect.ValueOf(hdr2).Pointer()).
					ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
				if hdr1.URI == nil {
					Expect(hdr2.URI).To(BeNil())
				} else {
					Expect(reflect.ValueOf(hdr2.URI).Pointer()).
						ToNot(Equal(reflect.ValueOf(hdr1.URI).Pointer()))
				}
				if hdr1.Params == nil {
					Expect(hdr2.Params).To(BeNil())
				} else {
					Expect(reflect.ValueOf(hdr2.Params).Pointer()).
						ToNot(Equal(reflect.ValueOf(hdr1.Params).Pointer()))
				}
			},
			Entry(nil, (*header.From)(nil)),
			Entry(nil,
				&header.From{
					DisplayName: "A. G. Bell",
					URI: &uri.SIP{
						User:   uri.User("agb"),
						Addr:   uri.Host("bell-telephone.com"),
						Params: make(header.Values).Set("transport", "udp"),
					},
					Params: make(header.Values).Set("tag", "a48s"),
				},
			),
			// endregion
		)
	})
})
