package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Accept-Language", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Accept-Language:", header.AcceptLanguage{}, nil),
			Entry(nil, "Accept-Language: *", header.AcceptLanguage{{Lang: "*"}}, nil),
			Entry(nil,
				"Accept-Language:\r\n"+
					"\tda,\r\n"+
					"\ten-gb;q=0.8, en;q=0.7",
				header.AcceptLanguage{
					{Lang: "da"},
					{Lang: "en-gb", Params: make(header.Values).Set("q", "0.8")},
					{Lang: "en", Params: make(header.Values).Set("q", "0.7")},
				},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.AcceptLanguage(nil), ""),
			Entry(nil, header.AcceptLanguage{}, "Accept-Language: "),
			Entry(nil, header.AcceptLanguage{{}}, "Accept-Language: "),
			Entry(nil, header.AcceptLanguage{{Lang: "*"}}, "Accept-Language: *"),
			Entry(nil, header.AcceptLanguage{{Lang: "en"}}, "Accept-Language: en"),
			Entry(nil,
				header.AcceptLanguage{{Lang: "en"}, {Lang: "fr"}},
				"Accept-Language: en, fr",
			),
			Entry(nil,
				header.AcceptLanguage{
					{
						Lang: "en",
						Params: make(header.Values).
							Set("a", "123").
							Set("q", "0.9"),
					},
					{Lang: "de"},
				},
				"Accept-Language: en;q=0.9;a=123, de",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.AcceptLanguage{}, nil, false),
			Entry(nil, header.AcceptLanguage{}, header.AcceptLanguage(nil), true),
			Entry(nil, header.AcceptLanguage(nil), header.AcceptLanguage(nil), true),
			Entry(nil, header.AcceptLanguage{}, header.AcceptLanguage{}, true),
			Entry(nil,
				header.AcceptLanguage{},
				header.AcceptLanguage{{Lang: "en"}},
				false,
			),
			Entry(nil,
				header.AcceptLanguage{{Lang: "en"}},
				header.AcceptLanguage{{Lang: "EN"}},
				true,
			),
			Entry(nil,
				header.AcceptLanguage{{Lang: "en"}},
				header.AcceptLanguage{{Lang: "fr"}},
				false,
			),
			Entry(nil,
				header.AcceptLanguage{
					{
						Lang:   "en",
						Params: header.Values{"foo": {"bar"}},
					},
				},
				header.AcceptLanguage{
					{
						Lang: "en",
					},
				},
				true,
			),
			Entry(nil,
				header.AcceptLanguage{
					{
						Lang:   "en",
						Params: header.Values{"foo": {"bar"}},
					},
				},
				header.AcceptLanguage{
					{
						Lang:   "en",
						Params: header.Values{"foo": {"BAR"}},
					},
				},
				true,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.AcceptLanguage{}, true),
			Entry(nil, header.AcceptLanguage{{}}, false),
			Entry(nil,
				header.AcceptLanguage{
					{
						Lang: "en",
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
				header.AcceptLanguage{
					{
						Lang:   "*",
						Params: header.Values{"q": {"0.5"}},
					},
				},
				true,
			),
			Entry(nil,
				header.AcceptLanguage{
					{
						Lang:   "en",
						Params: header.Values{"f i e l d": {"123"}},
					},
				},
				false,
			),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.AcceptLanguage) {
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
			Entry(nil, header.AcceptLanguage(nil)),
			Entry(nil, header.AcceptLanguage{}),
			Entry(nil,
				header.AcceptLanguage{
					{
						Lang:   "en",
						Params: header.Values{"q": {"0.7"}},
					},
					{
						Lang:   "fr",
						Params: header.Values{"q": {"0.5"}},
					},
				},
			),
			// endregion
		)
	})
})
