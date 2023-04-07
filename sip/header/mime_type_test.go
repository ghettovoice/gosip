package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("MIMEType", func() {
		DescribeTable("rendering", Label("rendering"),
			// region
			func(mt header.MIMEType, expect string) {
				Expect(mt.String()).To(Equal(expect))
			},
			EntryDescription("%#[1]v"),
			Entry(nil, header.MIMEType{}, "/"),
			Entry(nil, header.MIMEType{Type: "application", Subtype: "*"}, "application/*"),
			Entry(nil,
				header.MIMEType{
					Type:    "TEXT",
					Subtype: "PLAIN",
					Params: make(header.Values).
						Append("foo", "123").
						Append("Charset", "UTF-8").
						Append("bar", `"QwertY"`),
				},
				`TEXT/PLAIN;bar="QwertY";charset=UTF-8;foo=123`,
			),
			// endregion
		)

		DescribeTable("comparing", Label("comparing"),
			// region
			func(mt header.MIMEType, v any, expect bool) {
				Expect(mt.Equal(v)).To(Equal(expect))
			},
			EntryDescription("%#[1]v with value = %#[2]v"),
			Entry(nil, header.MIMEType{}, nil, false),
			Entry(nil, header.MIMEType{}, (*header.MIMEType)(nil), false),
			Entry(nil, header.MIMEType{}, header.MIMEType{}, true),
			Entry(nil,
				header.MIMEType{Type: "text", Subtype: "plain"},
				&header.MIMEType{Type: "text", Subtype: "plain"},
				true,
			),
			Entry(nil,
				header.MIMEType{Type: "text", Subtype: ""},
				header.MIMEType{Type: "text", Subtype: "*"},
				false,
			),
			Entry(nil,
				header.MIMEType{
					Type:    "text",
					Subtype: "plain",
					Params:  make(header.Values).Set("charset", "utf-8"),
				},
				header.MIMEType{
					Type:    "TEXT",
					Subtype: "PLAIN",
					Params:  make(header.Values).Set("CHARSET", "UTF-8"),
				},
				true,
			),
			Entry(nil,
				header.MIMEType{
					Type:    "text",
					Subtype: "plain",
					Params:  make(header.Values).Set("charset", "utf-8"),
				},
				header.MIMEType{
					Type:    "text",
					Subtype: "plain",
					Params:  make(header.Values).Set("charset", "cp1251"),
				},
				false,
			),
			Entry(nil,
				header.MIMEType{
					Type:    "text",
					Subtype: "plain",
					Params:  make(header.Values).Set("foo", "bar"),
				},
				&header.MIMEType{
					Type:    "text",
					Subtype: "plain",
					Params:  make(header.Values).Set("charset", "cp1251"),
				},
				false,
			),
			Entry(nil,
				header.MIMEType{
					Type:    "text",
					Subtype: "plain",
				},
				header.MIMEType{
					Type:    "text",
					Subtype: "plain",
					Params:  make(header.Values).Set("charset", "cp1251"),
				},
				false,
			),
			// endregion
		)

		DescribeTable("validating", Label("validating"),
			// region
			func(mt header.MIMEType, expect bool) {
				Expect(mt.IsValid()).To(Equal(expect))
			},
			EntryDescription("%[1]q"),
			Entry(nil, header.MIMEType{}, false),
			Entry(nil, header.MIMEType{Type: "text"}, false),
			Entry(nil, header.MIMEType{Subtype: "plain"}, false),
			Entry(nil, header.MIMEType{Type: "text", Subtype: "plain"}, true),
			Entry(nil, header.MIMEType{Type: "text", Subtype: "*"}, true),
			Entry(nil, header.MIMEType{Type: "*", Subtype: "*"}, true),
			Entry(nil,
				header.MIMEType{
					Type:    "text",
					Subtype: "plain",
					Params:  make(header.Values).Append("F-O_O", "bar"),
				},
				true,
			),
			Entry(nil,
				header.MIMEType{
					Type:    "text",
					Subtype: "plain",
					Params:  make(header.Values).Append(" F - O_O ", "bar"),
				},
				false,
			),
			Entry(nil,
				header.MIMEType{
					Type:    "text",
					Subtype: "*",
					Params:  make(header.Values).Append("Foo", `" B a R "`),
				},
				true,
			),
			Entry(nil,
				header.MIMEType{
					Type:    "text",
					Subtype: "*",
					Params:  make(header.Values).Append("Foo", " B a R "),
				},
				false,
			),
			// endregion
		)

		DescribeTable("cloning", Label("cloning"),
			// region
			func(mt1 header.MIMEType) {
				mt2 := mt1.Clone()
				Expect(mt2).To(Equal(mt1), "assert cloned MIMEType is equal to the original MIMEType")
				if mt1.Params != nil {
					Expect(reflect.ValueOf(mt2.Params).Pointer()).ToNot(Equal(reflect.ValueOf(mt1.Params).Pointer()), "assert cloned Params pointer is different than the original")
				}
			},
			EntryDescription("%#v"),
			Entry(nil, header.MIMEType{}),
			Entry(nil, header.MIMEType{Type: "text", Subtype: "*"}),
			Entry(nil, header.MIMEType{Type: "text", Subtype: "*", Params: make(header.Values).Set("charset", "UTF-8")}),
			// endregion
		)
	})
})
