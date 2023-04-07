package grammar_test

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

var _ = Describe("Grammar", Label("grammar"), func() {
	DescribeTable("Escape()",
		// region
		func(str string, cb func(byte) bool, expect string) {
			Expect(grammar.Escape(str, cb)).To(Equal(expect))
		},
		EntryDescription(`should convert "%s" to "%[3]s"`),
		// region entries
		Entry(nil, "", nil, ""),
		Entry(nil, "abc-qwe!", nil, "abc-qwe!"),
		Entry(nil, "abc++qwe!", nil, "abc%2B%2Bqwe!"),
		Entry(nil, "abc++qwe!", func(c byte) bool { return c != '+' && !grammar.IsCharUnreserved(c) }, "abc++qwe!"),
		// endregion
		// endregion
	)

	DescribeTable("Unescape()",
		// region
		func(str, expect string) {
			Expect(grammar.Unescape(str)).To(Equal(expect))
		},
		EntryDescription(`should convert "%s" to "%s"`),
		// region entries
		Entry(nil, "", ""),
		Entry(nil, "abc", "abc"),
		Entry(nil, "abc%%", "abc%%"),
		Entry(nil, "abc%ax", "abc%ax"),
		Entry(nil, "abc%E4%b8%96", "abc世"),
		// endregion
		// endregion
	)
})

func BenchmarkEscape(b *testing.B) {
	cases := []struct{ in, out any }{
		{"abc++qwe!", "abc%2B%2Bqwe!"},
		{[]byte("abc++qwe!"), []byte("abc%2B%2Bqwe!")},
	}

	b.ResetTimer()
	for i, tc := range cases {
		b.Run(fmt.Sprintf("case_%d", i+1), func(b *testing.B) {
			g := NewGomegaWithT(b)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				switch in := tc.in.(type) {
				case string:
					g.Expect(grammar.Escape(in, nil)).To(Equal(tc.out))
				case []byte:
					g.Expect(grammar.Escape(in, nil)).To(Equal(tc.out))
				}
			}
		})
	}
}
