package grammar_test

import (
	"bytes"
	"testing"

	"github.com/ghettovoice/gosip/internal/grammar"
)

func TestQuote(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		str  string
		want string
	}{
		{"empty", "", `""`},
		{"no quote", "abc", `"abc"`},
		{"with quote", `"ab"c"`, `"\"ab\"c\""`},
		{"with backslash quote", `ab\"c`, `"ab\\\"c"`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got, want := grammar.Quote(c.str), c.want; got != want {
				t.Errorf("grammar.Quote(%q) = %q, want %q", c.str, got, want)
			}
		})
	}
}

func TestUnquote(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		str  string
		want string
	}{
		{"empty", "", ""},
		{"empty quote", `""`, ""},
		{"no quote", "abc", "abc"},
		{"with quote", `"abc"`, "abc"},
		{"with backslash quote", `"\"ab\"c\\\""`, `"ab"c\"`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got, want := grammar.Unquote(c.str), c.want; got != want {
				t.Errorf("grammar.Unquote(%q) = %q, want %q", c.str, got, want)
			}
		})
	}
}

func TestIsTelNum(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		str  string
		want bool
	}{
		{"", "", false},
		{"", "abc", true},
		{"", "abc-11", true},
		{"", "abc-zz", false},
		{"", "123", true},
		{"", "123-0f-#*", true},
		{"", "123-0f-#*!", false},
		{"", "(123)33-55", true},
		{"", "(123) 33 55", false},
		{"", "+55(123)33-55", true},
		{"", "+55(abc)33-55", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got, want := grammar.IsTelNum(c.str), c.want; got != want {
				t.Errorf("grammar.IsTelNum(%q) = %v, want %v", c.str, got, want)
			}
		})
	}
}

func TestIsGlobTelNum(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		str  string
		want bool
	}{
		{"", "", false},
		{"", "123-44-55", false},
		{"", "+123-44-55", true},
		{"", "+1(123)-44-55", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got, want := grammar.IsGlobTelNum(c.str), c.want; got != want {
				t.Errorf("grammar.IsGlobTelNum(%q) = %v, want %v", c.str, got, want)
			}
		})
	}
}

func BenchmarkCleanTelNum(b *testing.B) {
	cases := []struct {
		name string
		in   any
		out  any
	}{
		{"string", "+7(333)444-55-66", "+73334445566"},
		{"bytes", []byte("+7(333)444-55-66"), []byte("+73334445566")},
	}

	b.ResetTimer()
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ResetTimer()
			for b.Loop() {
				switch in := c.in.(type) {
				case string:
					want, _ := c.out.(string)
					if got := grammar.CleanTelNum(in); got != want {
						b.Errorf("grammar.CleanTelNum(%q) = %q, want %q", in, got, want)
					}
				case []byte:
					want, _ := c.out.([]byte)
					if got := grammar.CleanTelNum(in); !bytes.Equal(got, want) {
						b.Errorf("grammar.CleanTelNum(%q) = %q, want %q", in, got, want)
					}
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
	for b.Loop() {
		if _, err := grammar.ParseRequest(str); err != nil {
			b.Errorf("grammar.ParseRequest() error = %v, want nil", err)
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
	for b.Loop() {
		if _, err := grammar.ParseResponse(str); err != nil {
			b.Errorf("grammar.ParseResponse() error = %v, want nil", err)
		}
	}
}
