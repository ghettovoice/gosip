package uri_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/common"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
	"github.com/ghettovoice/gosip/sip/uri"
)

var _ = Describe("URI", Label("sip", "uri"), func() {
	Describe("Tel", func() {
		assertURIParsing(
			// region
			Entry(nil, "tel:+1(22)333-44-55", &uri.Tel{Number: "+1(22)333-44-55"}, nil),
			Entry(nil,
				"tel:+1(22)333-44-55;ext=55;ISUB=qwe;Field1;field2=vAl%20%22",
				&uri.Tel{
					Number: "+1(22)333-44-55",
					Params: uri.Values{
						"ext":    []string{"55"},
						"isub":   []string{"qwe"},
						"field1": []string{""},
						"field2": []string{`vAl "`},
					},
				},
				nil,
			),
			Entry(nil,
				"tel:1122;phone-context=+765;field1;isub=qwe;field2=v%40l;field1=abc",
				&uri.Tel{
					Number: "1122",
					Params: uri.Values{
						"phone-context": []string{"+765"},
						"field1":        []string{"", "abc"},
						"isub":          []string{"qwe"},
						"field2":        []string{"v@l"},
					},
				},
				nil,
			),
			Entry(nil, "tel:+1 (22) 333-44-55", nil, grammar.ErrMalformedInput),
			Entry(nil, "tel:+1(22)333-44-55;fi%20ld=qwe", nil, grammar.ErrMalformedInput),
			// endregion
		)

		assertURIRendering(
			// region
			Entry(nil, (*uri.Tel)(nil), ""),
			Entry(nil, &uri.Tel{}, "tel:"),
			Entry(nil, &uri.Tel{Params: uri.Values{"ext": []string{"123"}}}, "tel:;ext=123"),
			Entry(nil, &uri.Tel{Number: " ", Params: uri.Values{"ext": []string{"123"}}}, "tel:;ext=123"),
			Entry(nil, &uri.Tel{Number: "+1(222)333-44-55"}, "tel:+1(222)333-44-55"),
			Entry(nil, &uri.Tel{Number: "+1  (222)  333-44-55   "}, "tel:+1(222)333-44-55"),
			Entry(nil,
				&uri.Tel{
					Number: "+1  (222)  333-44-55",
					Params: uri.Values{
						"isub":    []string{"abc"},
						"Foo-Bar": []string{""},
						"ext":     []string{"111", "222"},
						"z":       []string{"2@;"},
						"a":       []string{"1"},
					},
				},
				"tel:+1(222)333-44-55;ext=222;isub=abc;a=1;foo-bar;z=2%40%3B",
			),
			Entry(nil,
				&uri.Tel{
					Number: "333-44-55",
					Params: uri.Values{
						"phone-context": []string{"+333", "+1(23)"},
						"Foo-Bar":       []string{"baz", ""},
						"ext":           []string{"123"},
						"z":             []string{"2@;"},
						"a":             []string{"1"},
					},
				},
				"tel:333-44-55;ext=123;phone-context=+1(23);a=1;foo-bar;z=2%40%3B",
			),
			// endregion
		)

		assertURIComparing(
			// region
			Entry(nil, (*uri.Tel)(nil), nil, false),
			Entry(nil, (*uri.Tel)(nil), (*uri.Tel)(nil), true),
			Entry(nil, &uri.Tel{}, (*uri.Tel)(nil), false),
			Entry(nil, (*uri.Tel)(nil), &uri.Tel{}, false),
			Entry(nil, &uri.Tel{Params: uri.Values{}}, uri.Tel{Number: " "}, true),
			Entry(nil, &uri.Tel{Number: "+1(123)33-55"}, &uri.Tel{}, false),
			Entry(nil, &uri.Tel{Number: "+1(123)33-55"}, &uri.Tel{Number: "+1 (123) 33-55  "}, true),
			Entry(nil, &uri.Tel{Number: "+1(123)33-55"}, &uri.Tel{Number: "1(123)33-55"}, false),
			Entry(nil,
				&uri.Tel{Number: "+1(123)33-55", Params: uri.Values{}},
				&uri.Tel{Number: "+1(123)33-55"},
				true,
			),
			Entry(nil,
				&uri.Tel{
					Number: "+1(123)33-55",
					Params: make(uri.Values).Set("BAR", "QWE").Set("foo", "").Set("ext", "12344"),
				},
				&uri.Tel{
					Number: "  +1 123 33 55  ",
					Params: make(uri.Values).Set("ext", "123-44").Set("bar", "qwe").Set("FOO", ""),
				},
				true,
			),
			Entry(nil,
				&uri.Tel{
					Number: "+1(123)33-55",
					Params: make(uri.Values).Set("foo", "").Set("ext", "12344"),
				},
				&uri.Tel{
					Number: "+1(123)33-55",
					Params: make(uri.Values).Set("ext", "12344").Set("bar", "qwe").Set("foo", ""),
				},
				false,
			),
			Entry(nil,
				&uri.Tel{
					Number: "+1(123)33-55",
					Params: make(uri.Values).Set("a", "qwe").Set("foo", "").Set("ext", "12344"),
				},
				&uri.Tel{
					Number: "+1(123)33-55",
					Params: make(uri.Values).Set("b", "qwe").Set("foo", "").Set("ext", "12344"),
				},
				false,
			),
			Entry(nil,
				&uri.Tel{
					Number: "+1(123)33-55",
					Params: make(uri.Values).Set("a", "qwe").Set("foo", "").Set("ext", "12344"),
				},
				&uri.Tel{
					Number: "+1(123)33-55",
					Params: make(uri.Values).Set("a", "abc").Set("foo", "").Set("ext", "12344"),
				},
				false,
			),
			// endregion
		)

		assertURIValidating(
			// region
			Entry(nil, &uri.Tel{}, false),
			Entry(nil, &uri.Tel{Number: "+123"}, true),
			Entry(nil, &uri.Tel{Number: "123"}, false),
			Entry(nil, &uri.Tel{Number: "123", Params: make(uri.Values).Set("phone-context", "+11")}, true),
			Entry(nil, &uri.Tel{Number: "+123", Params: make(uri.Values).Set("a b c", "111")}, false),
			// endregion
		)

		DescribeTable("cloning", Label("cloning"),
			// region
			func(u1 *uri.Tel) {
				u2 := u1.Clone()
				if u1 == nil {
					Expect(u2).To(BeNil(), "assert cloned TelURI is nil")
				} else {
					u2 := u2.(*uri.Tel)
					Expect(u2).To(Equal(u1), "assert cloned TelURI equal to the original URI")
					Expect(reflect.ValueOf(u2).Pointer()).
						ToNot(Equal(reflect.ValueOf(u1).Pointer()), "assert cloned TelURI pointer is different than the original")
					if u1.Params != nil {
						Expect(reflect.ValueOf(u2.Params).Pointer()).
							ToNot(Equal(reflect.ValueOf(u1.Params).Pointer()), "assert cloned Params pointer is different than the original")
					}
				}
			},
			EntryDescription("%#v"),
			Entry(nil, (*uri.Tel)(nil)),
			Entry(nil, &uri.Tel{Number: "+1(222)333-44-55"}),
			Entry(nil,
				&uri.Tel{
					Number: "+1(222)333-44-55",
					Params: uri.Values{
						"Foo": []string{""},
						"ext": []string{"123", "222"},
						"bar": []string{"ba  z", "fooooo"},
					},
				},
			),
			// endregion
		)

		DescribeTable("checking on global phone number",
			// region
			func(u *uri.Tel, expect bool) {
				Expect(u.IsGlob()).To(Equal(expect))
			},
			EntryDescription("%[1]q"),
			Entry(nil, &uri.Tel{}, false),
			Entry(nil, &uri.Tel{Number: "123456"}, false),
			Entry(nil, &uri.Tel{Number: "+123456"}, true),
			Entry(nil, &uri.Tel{Number: "+1(23)4-5-6"}, true),
			// endregion
		)

		DescribeTable("converting to SIP URI",
			// region
			func(tu *uri.Tel, su *uri.SIP) {
				Expect(tu.ToSIP()).To(Equal(su))
			},
			EntryDescription("%[1]q"),
			Entry(nil,
				&uri.Tel{},
				&uri.SIP{
					User:   uri.User(""),
					Addr:   common.Host(""),
					Params: make(uri.Values).Set("user", "phone"),
				},
			),
			Entry(nil,
				&uri.Tel{
					Number: "+123456",
					Params: make(uri.Values).
						Append("FOO", "BAR").
						Append("ext", "5-5-5").
						Append("baz", ""),
				},
				&uri.SIP{
					User:   uri.User("+123456;ext=555;baz;foo=bar"),
					Addr:   common.Host(""),
					Params: make(uri.Values).Set("user", "phone"),
				},
			),
			Entry(nil,
				&uri.Tel{
					Number: "123456",
					Params: make(uri.Values).
						Append("FOO", "BAR").
						Append("ext", "5-5-5").
						Append("baz", "").
						Append("phone-context", "+2(22)"),
				},
				&uri.SIP{
					User:   uri.User("123456;ext=555;phone-context=+222;baz;foo=bar"),
					Addr:   common.Host(""),
					Params: make(uri.Values).Set("user", "phone"),
				},
			),
			Entry(nil,
				&uri.Tel{
					Number: "123456",
					Params: make(uri.Values).
						Append("FOO", "BAR").
						Append("ext", "5-5-5").
						Append("baz", "").
						Append("phone-context", "voip.gw.net"),
				},
				&uri.SIP{
					User:   uri.User("123456;ext=555;baz;foo=bar"),
					Addr:   common.Host("voip.gw.net"),
					Params: make(uri.Values).Set("user", "phone"),
				},
			),
			// endregion
		)
	})
})
