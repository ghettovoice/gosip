package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Accept", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Accept:", header.Accept{}, nil),
			Entry(nil,
				"Accept: */*",
				header.Accept{
					{MIMEType: header.MIMEType{Type: "*", Subtype: "*"}},
				},
				nil,
			),
			Entry(nil,
				"Accept: text/*;charset=utf-8;q=0.8;foo, application/json;q=0.5",
				header.Accept{
					{
						MIMEType: header.MIMEType{
							Type:    "text",
							Subtype: "*",
							Params:  make(header.Values).Append("charset", "utf-8"),
						},
						Params: make(header.Values).Append("q", "0.8").Append("foo", ""),
					},
					{
						MIMEType: header.MIMEType{
							Type:    "application",
							Subtype: "json",
						},
						Params: make(header.Values).Append("q", "0.5"),
					},
				},
				nil,
			),
			Entry(nil,
				"Accept: text/plain;foo",
				header.Accept{
					{
						MIMEType: header.MIMEType{
							Type:    "text",
							Subtype: "plain",
						},
						Params: make(header.Values).Append("foo", ""),
					},
				},
				nil,
			),
			Entry(nil,
				"Accept: text/plain;foo=bar",
				header.Accept{
					{
						MIMEType: header.MIMEType{
							Type:    "text",
							Subtype: "plain",
							Params:  make(header.Values).Append("foo", "bar"),
						},
					},
				},
				nil,
			),
			Entry(nil, "Accept: text", &header.Any{Name: "Accept", Value: "text"}, nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (header.Accept)(nil), ""),
			Entry(nil, header.Accept{}, "Accept: "),
			Entry(nil, header.Accept{{}}, "Accept: /"),
			Entry(nil,
				header.Accept{
					{
						MIMEType: header.MIMEType{Type: "text", Subtype: "*"},
						Params: make(header.Values).
							Append("foo", "").
							Append("bar", "QwertY").
							Append("BAZ", `"zzzXXXyyy"`),
					},
				},
				`Accept: text/*;bar=QwertY;baz="zzzXXXyyy";foo`,
			),
			Entry(nil,
				header.Accept{
					{
						MIMEType: header.MIMEType{
							Type:    "text",
							Subtype: "*",
							Params:  make(header.Values).Append("charset", "UTF-8"),
						},
						Params: make(header.Values).
							Append("foo", "").
							Append("bar", "123"),
					},
				},
				"Accept: text/*;charset=UTF-8;q=1;bar=123;foo",
			),
			Entry(nil,
				header.Accept{
					{
						MIMEType: header.MIMEType{
							Type:    "text",
							Subtype: "plain",
							Params:  make(header.Values).Append("charset", "UTF-8"),
						},
						Params: make(header.Values).
							Append("foo", "").
							Append("bar", "123").
							Append("q", "0.5"),
					},
					{
						MIMEType: header.MIMEType{
							Type:    "text",
							Subtype: "csv",
							Params:  make(header.Values).Append("charset", "UTF-8"),
						},
					},
				},
				"Accept: text/plain;charset=UTF-8;q=0.5;bar=123;foo, text/csv;charset=UTF-8",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.Accept{}, nil, false),
			Entry(nil, header.Accept(nil), header.Accept(nil), true),
			Entry(nil, header.Accept{}, header.Accept(nil), true),
			Entry(nil, header.Accept{}, header.Accept{}, true),
			Entry(nil,
				header.Accept{},
				header.Accept{{MIMEType: header.MIMEType{Type: "text", Subtype: "*"}}},
				false,
			),
			Entry(nil,
				header.Accept{{MIMEType: header.MIMEType{Type: "text", Subtype: "*"}}},
				header.Accept{{MIMEType: header.MIMEType{Type: "text"}}},
				false,
			),
			Entry(nil,
				header.Accept{{MIMEType: header.MIMEType{Type: "text", Subtype: "plain"}}},
				header.Accept{{MIMEType: header.MIMEType{Type: "text", Subtype: "*"}}},
				false,
			),
			Entry(nil,
				header.Accept{
					{
						MIMEType: header.MIMEType{
							Type:    "text",
							Subtype: "*",
							Params:  make(header.Values).Set("charset", "UTF-8"),
						},
						Params: make(header.Values).
							Set("FOO", "BAR").
							Set("field", `"QwertY"`),
					},
				},
				header.Accept{
					{
						MIMEType: header.MIMEType{
							Type:    "text",
							Subtype: "*",
							Params:  make(header.Values).Set("CHARSET", "utf-8"),
						},
						Params: make(header.Values).
							Set("foo", "bar").
							Set("field", `"QwertY"`),
					},
				},
				true,
			),
			Entry(nil,
				header.Accept{
					{
						MIMEType: header.MIMEType{
							Type:    "text",
							Subtype: "*",
							Params:  make(header.Values).Set("charset", "UTF-8"),
						},
						Params: make(header.Values).
							Set("FOO", "BAR").
							Set("field", `"QwertY"`),
					},
				},
				header.Accept{
					{
						MIMEType: header.MIMEType{
							Type:    "text",
							Subtype: "*",
							Params:  make(header.Values).Set("CHARSET", "utf-8"),
						},
						Params: make(header.Values).
							Set("foo", "bar").
							Set("field", `"qwerty"`),
					},
				},
				false,
			),
			Entry(nil,
				header.Accept{
					{
						MIMEType: header.MIMEType{
							Type:    "text",
							Subtype: "*",
							Params:  make(header.Values).Set("charset", "UTF-8"),
						},
						Params: make(header.Values).Set("FOO", "BAR"),
					},
				},
				header.Accept{
					{
						MIMEType: header.MIMEType{
							Type:    "text",
							Subtype: "*",
							Params:  make(header.Values).Set("CHARSET", "utf-8"),
						},
						Params: make(header.Values).
							Set("foo", "bar").
							Set("field", `"QwertY"`),
					},
				},
				true,
			),
			Entry(nil,
				header.Accept{
					{
						MIMEType: header.MIMEType{
							Type:    "text",
							Subtype: "*",
							Params:  make(header.Values).Set("charset", "UTF-8"),
						},
						Params: make(header.Values).
							Set("FOO", "BAR").
							Set("field1", `"QwertY"`),
					},
				},
				header.Accept{
					{
						MIMEType: header.MIMEType{
							Type:    "text",
							Subtype: "*",
							Params:  make(header.Values).Set("CHARSET", "utf-8"),
						},
						Params: make(header.Values).
							Set("q", "0.7").
							Set("foo", "bar").
							Set("field2", `"QwertY"`),
					},
				},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (header.Accept)(nil), false),
			Entry(nil, header.Accept{}, true),
			Entry(nil,
				header.Accept{
					{
						MIMEType: header.MIMEType{
							Type:    "*",
							Subtype: "*",
							Params:  make(header.Values).Set("charset", "utf-8"),
						},
						Params: make(header.Values).Set("foo", `" b a r "`),
					},
				},
				true,
			),
			Entry(nil, header.Accept{{}}, false),
			Entry(nil,
				header.Accept{
					{
						MIMEType: header.MIMEType{Type: "*", Subtype: "*"},
						Params:   make(header.Values).Set(" f o o ", "bar"),
					},
				},
				false,
			),
			Entry(nil,
				header.Accept{
					{
						MIMEType: header.MIMEType{Type: "*", Subtype: "*"},
						Params:   make(header.Values).Set("foo", " b a r "),
					},
				},
				false,
			),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.Accept) {
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
			Entry(nil, header.Accept(nil)),
			Entry(nil, header.Accept{}),
			Entry(nil,
				header.Accept{
					{
						MIMEType: header.MIMEType{
							Type:    "text",
							Subtype: "plain",
							Params:  header.Values{"charset": {"utf-8"}},
						},
						Params: header.Values{
							"q":   {"0.7"},
							"foo": {"bar"},
						},
					},
					{
						MIMEType: header.MIMEType{
							Type:    "text",
							Subtype: "plain",
							Params:  header.Values{"charset": {"utf-8"}},
						},
						Params: header.Values{
							"foo": {"bar"},
						},
					},
				},
			),
			// endregion
		)
	})
})
