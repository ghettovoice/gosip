package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/uri"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Reply-To", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Reply-To: ", &header.Any{Name: "Reply-To"}, nil),
			Entry(nil,
				"Reply-To: sip:alice@127.0.0.1;tag=a48s",
				&header.ReplyTo{
					URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("127.0.0.1")},
					Params: make(header.Values).Set("tag", "a48s"),
				},
				nil,
			),
			Entry(nil,
				"Reply-To: sips:alice@127.0.0.1;tag=a48s",
				&header.ReplyTo{
					URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("127.0.0.1"), Secured: true},
					Params: make(header.Values).Set("tag", "a48s"),
				},
				nil,
			),
			Entry(nil,
				"Reply-To: https://example.org/username?tag=a48s",
				&header.ReplyTo{
					URI:    &uri.Any{Scheme: "https", Host: "example.org", Path: "/username"},
					Params: make(header.Values).Set("tag", "a48s"),
				},
				nil,
			),
			Entry(nil,
				"Reply-To: \"A. G. Bell\" <sip:agb@bell-telephone.com>\r\n\t;tag=a48s",
				&header.ReplyTo{
					DisplayName: "A. G. Bell",
					URI:         &uri.SIP{User: uri.User("agb"), Addr: uri.Host("bell-telephone.com")},
					Params:      make(header.Values).Set("tag", "a48s"),
				},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (*header.ReplyTo)(nil), ""),
			Entry(nil, &header.ReplyTo{}, "Reply-To: <>"),
			Entry(nil,
				&header.ReplyTo{
					DisplayName: "A. G. Bell",
					URI: &uri.SIP{
						User:   uri.User("agb"),
						Addr:   uri.Host("bell-telephone.com"),
						Params: make(header.Values).Set("transport", "udp"),
					},
					Params: make(header.Values).Set("tag", "a48s"),
				},
				"Reply-To: \"A. G. Bell\" <sip:agb@bell-telephone.com;transport=udp>;tag=a48s",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, (*header.ReplyTo)(nil), nil, false),
			Entry(nil, (*header.ReplyTo)(nil), (*header.ReplyTo)(nil), true),
			Entry(nil, &header.ReplyTo{}, (*header.ReplyTo)(nil), false),
			Entry(nil,
				&header.ReplyTo{
					DisplayName: "A. G. Bell",
					URI: &uri.SIP{
						User:   uri.User("agb"),
						Addr:   uri.Host("bell-telephone.com"),
						Params: make(header.Values).Set("transport", "udp"),
					},
					Params: make(header.Values).Set("tag", "a48s"),
				},
				header.ReplyTo{
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
				&header.ReplyTo{
					DisplayName: "A. G. Bell",
					URI: &uri.SIP{
						User:   uri.User("agb"),
						Addr:   uri.Host("bell-telephone.com"),
						Params: make(header.Values).Set("transport", "udp"),
					},
					Params: make(header.Values).Set("tag", "a48s"),
				},
				&header.ReplyTo{
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
				&header.ReplyTo{
					DisplayName: "A. G. Bell",
					URI: &uri.SIP{
						User:   uri.User("agb"),
						Addr:   uri.Host("bell-telephone.com"),
						Params: make(header.Values).Set("transport", "udp"),
					},
					Params: make(header.Values).Set("tag", "a48s").Set("x", "def"),
				},
				&header.ReplyTo{
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
			Entry(nil, (*header.ReplyTo)(nil), false),
			Entry(nil, &header.ReplyTo{}, false),
			Entry(nil,
				&header.ReplyTo{
					URI: &uri.SIP{Addr: uri.Host("bell-telephone.com")},
				},
				true,
			),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 *header.ReplyTo) {
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
			Entry(nil, (*header.ReplyTo)(nil)),
			Entry(nil,
				&header.ReplyTo{
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
