package header_test

import (
	"reflect"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Retry-After", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Retry-After: ", &header.Any{Name: "Retry-After"}, nil),
			Entry(nil, "Retry-After: abc", &header.Any{Name: "Retry-After", Value: "abc"}, nil),
			Entry(nil,
				"Retry-After: 120(I'm in a meeting);duration=60",
				&header.RetryAfter{
					Delay:   120 * time.Second,
					Comment: "I'm in a meeting",
					Params:  make(header.Values).Set("duration", "60"),
				},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (*header.RetryAfter)(nil), ""),
			Entry(nil, &header.RetryAfter{}, "Retry-After: 0"),
			Entry(nil,
				&header.RetryAfter{
					Delay:   120 * time.Second,
					Comment: "I'm in a meeting",
					Params:  make(header.Values).Set("duration", "60"),
				},
				"Retry-After: 120 (I'm in a meeting);duration=60",
			),
			Entry(nil,
				&header.RetryAfter{
					Delay:  120 * time.Second,
					Params: make(header.Values).Set("duration", "60"),
				},
				"Retry-After: 120;duration=60",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, (*header.RetryAfter)(nil), nil, false),
			Entry(nil, (*header.RetryAfter)(nil), (*header.RetryAfter)(nil), true),
			Entry(nil, &header.RetryAfter{}, (*header.RetryAfter)(nil), false),
			Entry(nil, &header.RetryAfter{}, &header.RetryAfter{}, true),
			Entry(nil,
				&header.RetryAfter{
					Delay:   60 * time.Second,
					Comment: "I'm in a meeting",
					Params:  make(header.Values).Set("duration", "60").Set("x", "abc"),
				},
				header.RetryAfter{
					Delay:   60 * time.Second,
					Comment: "I'm in a meeting",
					Params:  make(header.Values).Set("duration", "60"),
				},
				true,
			),
			Entry(nil,
				&header.RetryAfter{
					Delay:   60 * time.Second,
					Comment: "I'm in a meeting",
				},
				header.RetryAfter{
					Delay:   60 * time.Second,
					Comment: "I'm in a MEETING",
				},
				false,
			),
			Entry(nil,
				&header.RetryAfter{
					Delay:   60 * time.Second,
					Comment: "I'm in a meeting",
				},
				header.RetryAfter{
					Delay:   120 * time.Second,
					Comment: "I'm in a meeting",
				},
				false,
			),
			Entry(nil,
				&header.RetryAfter{
					Delay:   60 * time.Second,
					Comment: "I'm in a meeting",
					Params:  make(header.Values).Set("duration", "60").Set("x", "abc"),
				},
				header.RetryAfter{
					Delay:   60 * time.Second,
					Comment: "I'm in a meeting",
					Params:  make(header.Values).Set("x", "abc"),
				},
				false,
			),
			Entry(nil,
				&header.RetryAfter{
					Delay:   60 * time.Second,
					Comment: "I'm in a meeting",
					Params:  make(header.Values).Set("duration", "60").Set("x", "abc"),
				},
				header.RetryAfter{
					Delay:   60 * time.Second,
					Comment: "I'm in a meeting",
					Params:  make(header.Values).Set("x", "abc").Set("x", "def"),
				},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (*header.RetryAfter)(nil), false),
			Entry(nil, &header.RetryAfter{}, true),
			Entry(nil, &header.RetryAfter{Delay: -time.Second}, false),
			Entry(nil,
				&header.RetryAfter{
					Delay:   60 * time.Second,
					Comment: "I'm in a meeting",
					Params:  make(header.Values).Set("duration", "60").Set("x", "abc"),
				},
				true,
			),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 *header.RetryAfter) {
				Expect(reflect.ValueOf(hdr2).Pointer()).
					ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
				if hdr1.Params == nil {
					Expect(hdr2.Params).To(BeNil())
				} else {
					Expect(reflect.ValueOf(hdr2.Params).Pointer()).
						ToNot(Equal(reflect.ValueOf(hdr1.Params).Pointer()))
				}
			},
			Entry(nil, (*header.RetryAfter)(nil)),
			Entry(nil,
				&header.RetryAfter{
					Delay:   60 * time.Second,
					Comment: "I'm in a meeting",
					Params:  make(header.Values).Set("duration", "60"),
				},
			),
			// endregion
		)
	})
})
