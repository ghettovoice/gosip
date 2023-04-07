package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Any", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "X-Custom", nil, grammar.ErrMalformedInput),
			Entry(nil, "X-Custom:", &header.Any{Name: "X-Custom"}, nil),
			Entry(nil, "X-Custom: abc", &header.Any{Name: "X-Custom", Value: "abc"}, nil),
			Entry(nil, "X-Custom: abc\r\n\tqwe", &header.Any{Name: "X-Custom", Value: "abc\r\n\tqwe"}, nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (*header.Any)(nil), ""),
			Entry(nil, &header.Any{}, ": "),
			Entry(nil, &header.Any{Name: "x-custom"}, "X-Custom: "),
			Entry(nil, &header.Any{Name: "x-custom", Value: "abc"}, "X-Custom: abc"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, (*header.Any)(nil), nil, false),
			Entry(nil, (*header.Any)(nil), (*header.Any)(nil), true),
			Entry(nil, &header.Any{}, (*header.Any)(nil), false),
			Entry(nil, &header.Any{}, &header.Any{}, true),
			Entry(nil, &header.Any{Name: "x-custom"}, &header.Any{}, false),
			Entry(nil, &header.Any{Name: "x-custom", Value: "abc"}, &header.Any{}, false),
			Entry(nil,
				&header.Any{Name: "x-custom", Value: "abc"},
				header.Any{Name: "X-CUSTOM", Value: "abc"},
				true,
			),
			Entry(nil,
				&header.Any{Name: "X-Custom", Value: "abc"},
				&header.Any{Name: "X-Custom", Value: "ABC"},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (*header.Any)(nil), false),
			Entry(nil, &header.Any{}, false),
			Entry(nil, &header.Any{Name: "x-custom"}, true),
			Entry(nil, &header.Any{Name: "a b c"}, false),
			Entry(nil, &header.Any{Name: "a-b-c"}, true),
			Entry(nil, &header.Any{Name: "x-custom", Value: "abc"}, true),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 *header.Any) {
				Expect(reflect.ValueOf(hdr2).Pointer()).ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
			},
			Entry(nil, (*header.Any)(nil)),
			Entry(nil, &header.Any{}),
			Entry(nil, &header.Any{Name: "x-custom", Value: "abc"}),
			// endregion
		)
	})
})
