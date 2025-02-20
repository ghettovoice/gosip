package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/uri"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Alert-Info", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Alert-Info:", &header.Any{Name: "Alert-Info"}, nil),
			Entry(nil,
				"Alert-Info:\r\n\t<https://example.com/a/b/c>;baz;foo=bar,\r\n\t\t<https://example.com/x/y/z>",
				header.AlertInfo{
					{
						URI:    &uri.Any{Scheme: "https", Host: "example.com", Path: "/a/b/c"},
						Params: make(header.Values).Set("foo", "bar").Set("baz", ""),
					},
					{
						URI: &uri.Any{Scheme: "https", Host: "example.com", Path: "/x/y/z"},
					},
				},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (header.AlertInfo)(nil), ""),
			Entry(nil, header.AlertInfo{}, "Alert-Info: "),
			Entry(nil,
				header.AlertInfo{
					{
						URI:    &uri.Any{Scheme: "https", Host: "example.com", Path: "/a/b/c"},
						Params: make(header.Values).Set("foo", "bar").Set("baz", ""),
					},
					{
						URI: &uri.Any{Scheme: "https", Host: "example.com", Path: "/x/y/z"},
					},
				},
				"Alert-Info: <https://example.com/a/b/c>;baz;foo=bar, <https://example.com/x/y/z>",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.AlertInfo(nil), nil, false),
			Entry(nil, header.AlertInfo(nil), header.AlertInfo(nil), true),
			Entry(nil, header.AlertInfo{}, header.AlertInfo(nil), true),
			Entry(nil, header.AlertInfo{}, header.AlertInfo{}, true),
			Entry(nil,
				header.AlertInfo{
					{URI: &uri.Any{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
				},
				header.AlertInfo{},
				false,
			),
			Entry(nil,
				header.AlertInfo{
					{
						URI:    &uri.Any{Scheme: "https", Host: "abc.com", Path: "/a/b/c"},
						Params: make(header.Values).Set("field1", `"QWERTY"`),
					},
					{
						URI:    &uri.Any{Scheme: "https", Host: "asd.com", Path: "/a/b/c"},
						Params: make(header.Values).Set("field2", "asd"),
					},
				},
				header.AlertInfo{
					{
						URI:    &uri.Any{Scheme: "HTTPS", Host: "ABC.com", Path: "/a/b/c"},
						Params: make(header.Values).Set("field1", `"QWERTY"`),
					},
					{
						URI: &uri.Any{Scheme: "https", Host: "ASD.COM", Path: "/a/b/c"},
						Params: make(header.Values).
							Append("field2", "zxc").
							Append("field2", "ASD"),
					},
				},
				true,
			),
			Entry(nil,
				header.AlertInfo{
					{
						URI:    &uri.Any{Scheme: "https", Host: "abc.com", Path: "/a/b/c"},
						Params: make(header.Values).Set("field1", `"QWERTY"`),
					},
					{
						URI:    &uri.Any{Scheme: "https", Host: "asd.com", Path: "/a/b/c"},
						Params: make(header.Values).Set("field2", "asd"),
					},
				},
				header.AlertInfo{
					{
						URI:    &uri.Any{Scheme: "https", Host: "ASD.COM", Path: "/a/b/c"},
						Params: make(header.Values).Set("field2", "asd"),
					},
					{
						URI:    &uri.Any{Scheme: "HTTPS", Host: "ABC.com", Path: "/a/b/c"},
						Params: make(header.Values).Set("field1", `"QWERTY"`),
					},
				},
				false,
			),
			Entry(nil,
				header.AlertInfo{
					{
						URI:    &uri.Any{Scheme: "https", Host: "abc.com", Path: "/a/b/c"},
						Params: make(header.Values).Set("field1", `"QWERTY"`),
					},
					{
						URI:    &uri.Any{Scheme: "https", Host: "asd.com", Path: "/a/b/c"},
						Params: make(header.Values).Set("field2", "asd"),
					},
				},
				header.AlertInfo{
					{
						URI:    &uri.Any{Scheme: "HTTPS", Host: "ABC.com", Path: "/a/b/c"},
						Params: make(header.Values).Set("field1", `"qwerty"`),
					},
					{
						URI:    &uri.Any{Scheme: "https", Host: "ASD.COM", Path: "/a/b/c"},
						Params: make(header.Values).Set("field2", "asd"),
					},
				},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (header.AlertInfo)(nil), false),
			Entry(nil, header.AlertInfo{}, false),
			Entry(nil, header.AlertInfo{{}}, false),
			Entry(nil, header.AlertInfo{
				{
					URI: &uri.Any{Scheme: "https", Host: "example.com", Path: "/a/b/c"},
				},
			}, true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.AlertInfo) {
				if len(hdr1) > 0 {
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
				}
			},
			Entry(nil, (header.AlertInfo)(nil)),
			Entry(nil, header.AlertInfo{}),
			Entry(nil,
				header.AlertInfo{
					{
						URI:    &uri.Any{Scheme: "https", Host: "example.com", Path: "/a/b/c"},
						Params: make(header.Values).Set("foo", "bar").Set("baz", ""),
					},
					{
						URI: &uri.Any{Scheme: "https", Host: "example.com", Path: "/x/y/z"},
					},
				},
			),
			// endregion
		)
	})
})
