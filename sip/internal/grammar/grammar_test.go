package grammar_test

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grammar2 "github.com/ghettovoice/gosip/sip/internal/grammar"
)

var _ = Describe("Grammar", Label("grammar"), func() {
	DescribeTable("Quote()",
		// region
		func(str, expect string) {
			Expect(grammar2.Quote(str)).To(Equal(expect))
		},
		EntryDescription(`should convert "%s" to "%s"`),
		// region entries
		Entry(nil, "", `""`),
		Entry(nil, "abc", `"abc"`),
		Entry(nil, `"ab"c"`, `"\"ab\"c\""`),
		Entry(nil, `ab\"c`, `"ab\\\"c"`),
		// endregion
		// endregion
	)

	DescribeTable("Unquote()",
		// region
		func(str, expect string) {
			Expect(grammar2.Unquote(str)).To(Equal(expect))
		},
		EntryDescription(`should convert "%s" to "%s"`),
		// region entries
		Entry(nil, "", ""),
		Entry(nil, `""`, ""),
		Entry(nil, "abc", "abc"),
		Entry(nil, `"abc"`, "abc"),
		Entry(nil, `"\"ab\"c\""`, `"ab"c"`),
		Entry(nil, `"ab\\\"c"`, `ab\"c`),
		// endregion
		// endregion
	)

	DescribeTable("IsTelNum()",
		// region
		func(str string, expect bool) {
			Expect(grammar2.IsTelNum(str)).To(Equal(expect))
		},
		EntryDescription(`should return %[2]v for "%[1]s"`),
		// region entries
		Entry(nil, "", false),
		Entry(nil, "abc", true),
		Entry(nil, "abc-11", true),
		Entry(nil, "abc-zz", false),
		Entry(nil, "123", true),
		Entry(nil, "123-0f-#*", true),
		Entry(nil, "123-0f-#*!", false),
		Entry(nil, "(123)33-55", true),
		Entry(nil, "(123) 33 55", false),
		Entry(nil, "+55(123)33-55", true),
		Entry(nil, "+55(abc)33-55", false),
		// endregion
		// endregion
	)

	DescribeTable("IsGlobTelNum()",
		// region
		func(str string, expect bool) {
			Expect(grammar2.IsGlobTelNum(str)).To(Equal(expect))
		},
		EntryDescription(`should return %[2]v for "%[1]s"`),
		// region entries
		Entry(nil, "", false),
		Entry(nil, "123-44-55", false),
		Entry(nil, "+123-44-55", true),
		Entry(nil, "+1(123)-44-55", true),
		// endregion
		// endregion
	)
})

func BenchmarkCleanTelNum(b *testing.B) {
	cases := []struct{ in, out any }{
		{"+7(333)444-55-66", "+73334445566"},
		{[]byte("+7(333)444-55-66"), []byte("+73334445566")},
	}

	b.ResetTimer()
	for i, tc := range cases {
		b.Run(fmt.Sprintf("case_%d", i+1), func(b *testing.B) {
			g := NewGomegaWithT(b)
			b.ResetTimer()
			for range b.N {
				switch in := tc.in.(type) {
				case string:
					g.Expect(grammar2.CleanTelNum(in)).To(Equal(tc.out))
				case []byte:
					g.Expect(grammar2.CleanTelNum(in)).To(Equal(tc.out))
				}
			}
		})
	}
}

func BenchmarkParseRequest(b *testing.B) {
	str := "OPTIONS tel:+1-2-3;phone-context=b.example.com SIP/2.0\r\n" +
		"Route: <sip:192.168.0.10;lr>, <sip:192.168.0.11;lr>\r\n" +
		"Route: <sip:192.168.0.12;lr>\r\n" +
		"Via: SIP/2.0/UDP 192.168.100.110:5060;branch=aaa\r\n" +
		"Via: SIP/2.0/UDP 192.168.100.105:5060;branch=bbb, SIP/2.0/UDP 192.168.100.100:5060;branch=ccc\r\n" +
		"From: \"Alice\" <sip:alice@a.example.com>;tag=qwerty\r\n" +
		"To: <sip:bob@b.example.com;field1=val;field2>\r\n" +
		"CSeq: 100 OPTIONS\r\n" +
		"Call-ID: QwertY\r\n" +
		"Max-Forwards: 70\r\n" +
		"Accept: text/*;charset=utf-8\r\n" +
		"Accept-Encoding: gzip, identity\r\n" +
		"Accept-Language: ru, en;q=0.8\r\n" +
		"Content-Type: text/raw;charset=utf-8\r\n" +
		"Content-Length: 12\r\n" +
		"Content-Language: en-US, ru\r\n" +
		"Content-Encoding: identity\r\n" +
		"Date: Sat, 13 Nov 2010 23:29:00 GMT\r\n" +
		"Organization: SHIK Co\r\n" +
		"Priority: non-urgent\r\n" +
		"Require: tdialog, 100rel\r\n" +
		"Supported: tdialog, 100rel\r\n" +
		"Unsupported: foo\r\n" +
		"Subject: abc\r\n" +
		"User-Agent: go-voip v1.0\r\n" +
		"Timestamp: 0.05\r\n" +
		"X-Custom-Header: qwerty\r\n" +
		"\r\n" +
		"Hello world!"

	b.ResetTimer()
	for i := range b.N {
		if _, err := grammar2.ParseRequest(str); err != nil {
			b.Errorf("%d parse failed: %s", i, err)
		}
	}
}

func BenchmarkParseResponse(b *testing.B) {
	str := "SIP/2.0 200 Ok\r\n" +
		"Record-Route: <sip:192.168.0.10;lr>, <sip:192.168.0.11;lr>\r\n" +
		"Record-Route: <sip:192.168.0.12;lr>\r\n" +
		"Via: SIP/2.0/UDP 192.168.100.110:5060;branch=aaa\r\n" +
		"Via: SIP/2.0/UDP 192.168.100.105:5060;branch=bbb, SIP/2.0/UDP 192.168.100.100:5060;branch=ccc\r\n" +
		"From: \"Alice\" <sip:alice@a.example.com>;tag=qwerty\r\n" +
		"To: <sip:bob@b.example.com;field1=val;field2>\r\n" +
		"CSeq: 100 INVITE\r\n" +
		"Call-ID: QwertY\r\n" +
		"Max-Forwards: 70\r\n" +
		"Content-Type: text/raw;charset=utf-8\r\n" +
		"Content-Length: 12\r\n" +
		"Content-Language: en-US, ru\r\n" +
		"Content-Encoding: identity\r\n" +
		"Date: Sat, 13 Nov 2010 23:29:00 GMT\r\n" +
		"Organization: SHIK Co\r\n" +
		"Priority: non-urgent\r\n" +
		"Supported: tdialog, 100rel\r\n" +
		"Unsupported: foo\r\n" +
		"Server: go-voip v1.0\r\n" +
		"Timestamp: 0.05 5.5\r\n" +
		"X-Custom-Header: qwerty\r\n" +
		"\r\n" +
		"Hello world!"

	b.ResetTimer()
	for i := range b.N {
		if _, err := grammar2.ParseResponse(str); err != nil {
			b.Errorf("%d parse failed: %s", i, err)
		}
	}
}
