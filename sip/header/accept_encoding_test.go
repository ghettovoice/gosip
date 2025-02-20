package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Accept-Encoding", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Accept-Encoding:", header.AcceptEncoding{}, nil),
			Entry(nil, "Accept-Encoding: *", header.AcceptEncoding{{Encoding: "*"}}, nil),
			Entry(nil,
				"Accept-Encoding: gzip;q=0.5;foo=bar, deflate;foo",
				header.AcceptEncoding{
					{Encoding: "gzip", Params: make(header.Values).Set("q", "0.5").Set("foo", "bar")},
					{Encoding: "deflate", Params: make(header.Values).Set("foo", "")},
				},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.AcceptEncoding(nil), ""),
			Entry(nil, header.AcceptEncoding{}, "Accept-Encoding: "),
			Entry(nil, header.AcceptEncoding{{}}, "Accept-Encoding: "),
			Entry(nil, header.AcceptEncoding{{Encoding: "*"}}, "Accept-Encoding: *"),
			Entry(nil, header.AcceptEncoding{{Encoding: "gzip"}}, "Accept-Encoding: gzip"),
			Entry(nil,
				header.AcceptEncoding{{Encoding: "gzip"}, {Encoding: "compress"}},
				"Accept-Encoding: gzip, compress",
			),
			Entry(nil,
				header.AcceptEncoding{
					{
						Encoding: "gzip",
						Params: make(header.Values).
							Set("a", "123").
							Set("q", "0.9"),
					},
					{Encoding: "deflate"},
				},
				"Accept-Encoding: gzip;q=0.9;a=123, deflate",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.AcceptEncoding{}, nil, false),
			Entry(nil, header.AcceptEncoding(nil), header.AcceptEncoding(nil), true),
			Entry(nil, header.AcceptEncoding{}, header.AcceptEncoding(nil), true),
			Entry(nil, header.AcceptEncoding{}, header.AcceptEncoding{}, true),
			Entry(nil,
				header.AcceptEncoding{},
				header.AcceptEncoding{{Encoding: "gzip"}},
				false,
			),
			Entry(nil,
				header.AcceptEncoding{{Encoding: "gzip"}},
				header.AcceptEncoding{{Encoding: "GZIP"}},
				true,
			),
			Entry(nil,
				header.AcceptEncoding{{Encoding: "gzip"}},
				header.AcceptEncoding{{Encoding: "compress"}},
				false,
			),
			Entry(nil,
				header.AcceptEncoding{
					{
						Encoding: "gzip",
						Params:   header.Values{"foo": {"bar"}},
					},
				},
				header.AcceptEncoding{
					{
						Encoding: "gzip",
					},
				},
				true,
			),
			Entry(nil,
				header.AcceptEncoding{
					{
						Encoding: "gzip",
						Params:   header.Values{"foo": {"bar"}},
					},
				},
				header.AcceptEncoding{
					{
						Encoding: "gzip",
						Params:   header.Values{"foo": {"BAR"}},
					},
				},
				true,
			),
			Entry(nil,
				header.AcceptEncoding{
					{
						Encoding: "gzip",
						Params:   header.Values{"foo": {"bar"}},
					},
				},
				header.AcceptEncoding{
					{
						Encoding: "gzip",
						Params:   header.Values{"foo": {"qwe"}},
					},
				},
				false,
			),
			Entry(nil,
				header.AcceptEncoding{
					{
						Encoding: "gzip",
						Params:   header.Values{"foo": {`"bar"`}},
					},
				},
				header.AcceptEncoding{
					{
						Encoding: "gzip",
						Params:   header.Values{"foo": {`"BAR"`}},
					},
				},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.AcceptEncoding{}, true),
			Entry(nil, header.AcceptEncoding{{}}, false),
			Entry(nil,
				header.AcceptEncoding{
					{
						Encoding: "gzip",
						Params: header.Values{
							"q":   {"0.7"},
							"foo": {"a_b-c"},
							"bar": {`"A B C"`},
						},
					},
				},
				true,
			),
			Entry(nil,
				header.AcceptEncoding{
					{
						Encoding: "*",
						Params:   header.Values{"q": {"0.5"}},
					},
				},
				true,
			),
			Entry(nil,
				header.AcceptEncoding{
					{
						Encoding: "gzip",
						Params:   header.Values{"f i e l d": {"123"}},
					},
				},
				false,
			),
			Entry(nil,
				header.AcceptEncoding{
					{
						Encoding: "gzip",
						Params:   header.Values{"field": {" a b c "}},
					},
				},
				false,
			),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.AcceptEncoding) {
				if len(hdr1) > 0 {
					Expect(reflect.ValueOf(hdr2).Pointer()).
						ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
					for i := range hdr1 {
						if hdr1[i].Params == nil {
							Expect(hdr2[i].Params).To(BeNil())
						} else {
							Expect(reflect.ValueOf(hdr2[i].Params).Pointer()).
								ToNot(Equal(reflect.ValueOf(hdr1[i].Params).Pointer()))
						}
					}
				}
			},
			Entry(nil, header.AcceptEncoding(nil)),
			Entry(nil, header.AcceptEncoding{}),
			Entry(nil,
				header.AcceptEncoding{
					{
						Encoding: "gzip",
						Params:   header.Values{"q": {"0.7"}},
					},
					{
						Encoding: "compress",
						Params:   header.Values{"q": {"0.5"}},
					},
				},
			),
			// endregion
		)
	})
})
