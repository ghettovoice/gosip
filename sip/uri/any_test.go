package uri_test

import (
	"fmt"
	"net/url"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/uri"
)

var _ = Describe("URI", Label("sip", "uri"), func() {
	Describe("Any", func() {
		assertURIParsing(
			// region
			Entry(nil, "/a/b/c", &uri.Any{Path: "/a/b/c"}, nil),
			Entry(nil, "http://localhost", &uri.Any{Scheme: "http", Host: "localhost"}, nil),
			Entry(nil,
				"http://example.com/qwe/rty.wav",
				&uri.Any{Scheme: "http", Host: "example.com", Path: "/qwe/rty.wav"},
				nil,
			),
			Entry(nil,
				"urn:service:sos?a=1&b=2",
				&uri.Any{Scheme: "urn", Opaque: "service:sos", RawQuery: "a=1&b=2"},
				nil,
			),
			// endregion
		)

		assertURIRendering(
			// region
			Entry(nil, (*uri.Any)(nil), ""),
			Entry(nil, &uri.Any{}, ""),
			Entry(nil, &uri.Any{Scheme: "qwe"}, "qwe:"),
			Entry(nil, &uri.Any{Path: "qwe/abc.wav"}, "qwe/abc.wav"),
			Entry(nil, &uri.Any{Scheme: "http", Host: "example.com"}, "http://example.com"),
			Entry(nil, &uri.Any{Scheme: "ftp", Opaque: "example.com/a/b/c"}, "ftp:example.com/a/b/c"),
			// endregion
		)

		assertURIComparing(
			// region
			Entry(nil, (*uri.Any)(nil), nil, false),
			Entry(nil, (*uri.Any)(nil), (*uri.Any)(nil), true),
			Entry(nil, &uri.Any{}, (*uri.Any)(nil), false),
			Entry(nil, (*uri.Any)(nil), &uri.Tel{}, false),
			Entry(nil,
				&uri.Any{Scheme: "http", Host: "example.com"},
				uri.Any{Scheme: "HTTP", Host: "EXAMPLE.COM"},
				true,
			),
			// endregion
		)

		assertURIValidating(
			// region
			Entry(nil, &uri.Any{}, false),
			Entry(nil, &uri.Any{Scheme: "ftp", Host: "example.com"}, true),
			// endregion
		)

		DescribeTable("cloning", Label("cloning"),
			// region
			func(u1 *uri.Any) {
				u2 := u1.Clone()
				if u1 == nil {
					Expect(u2).To(BeNil(), "assert cloned URI is nil")
				} else {
					u2, ok := u2.(*uri.Any)
					Expect(ok).To(BeTrue(), fmt.Sprintf("assert cloned URI is of type %T", u1))
					Expect(u2).To(Equal(u1), "assert cloned URI is equal to the original URI")
					Expect(reflect.ValueOf(u2).Pointer()).
						ToNot(Equal(reflect.ValueOf(u1).Pointer()), "assert cloned URI pointer is different than the original")
					if u1.User != nil {
						Expect(reflect.ValueOf(u2.User).Pointer()).
							ToNot(Equal(reflect.ValueOf(u1.User).Pointer()), "assert cloned User field pointer is different than the original")
					}
				}
			},
			EntryDescription("%#v"),
			Entry(nil, (*uri.Any)(nil)),
			Entry(nil, &uri.Any{Scheme: "http", Host: "example.com"}),
			Entry(nil, &uri.Any{Scheme: "https", User: url.User("root"), Host: "example.com"}),
			Entry(nil, &uri.Any{Scheme: "https", User: url.UserPassword("root", "qwe"), Host: "example.com"}),
			// endregion
		)
	})
})
