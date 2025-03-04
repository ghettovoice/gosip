package header_test

import (
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	"braces.dev/errtrace"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/uri"
)

func TestCanonicName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		out  header.Name
	}{
		{"", "call-id", "Call-ID"},
		{"", "cALL-id", "Call-ID"},
		{"", "Call-Id", "Call-ID"},
		{"", "i", "Call-ID"},
		{"", "Call-ID", "Call-ID"},
		{"", "cseq", "CSeq"},
		{"", "Cseq", "CSeq"},
		{"", "x-custom-header", "X-Custom-Header"},
		{"", "l", "Content-Length"},
		{"", "mime-version", "MIME-Version"},
		{"", "www-authenticate", "WWW-Authenticate"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got, want := header.CanonicName(c.in), c.out; got != want {
				t.Errorf("header.CanonicName(%q) = %q, want %q", c.in, got, want)
			}
		})
	}
}

func TestParse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		src     any
		hdrPrs  map[string]header.Parser
		wantHdr header.Header
		wantErr error
	}{
		{"empty string", "", nil, nil, grammar.ErrEmptyInput},
		{"empty bytes", []byte{}, nil, nil, grammar.ErrEmptyInput},
		{"trash", "qwerty", nil, nil, grammar.ErrMalformedInput},
		{"trash bytes", []byte("qwerty"), nil, nil, grammar.ErrMalformedInput},

		{"accept-encoding 1", "Accept-Encoding:", nil, header.AcceptEncoding{}, nil},
		{"accept-encoding 2", "Accept-Encoding: *", nil, header.AcceptEncoding{{Encoding: "*"}}, nil},
		{
			"accept-encoding 3",
			"Accept-Encoding: gzip;q=0.5;foo=bar, deflate;foo",
			nil,
			header.AcceptEncoding{
				{Encoding: "gzip", Params: make(header.Values).Set("q", "0.5").Set("foo", "bar")},
				{Encoding: "deflate", Params: make(header.Values).Set("foo", "")},
			},
			nil,
		},

		{"accept-language 1", "Accept-Language:", nil, header.AcceptLanguage{}, nil},
		{"accept-language 2", "Accept-Language: *", nil, header.AcceptLanguage{{Lang: "*"}}, nil},
		{
			"accept-language 3",
			"Accept-Language:\r\n" +
				"\tda,\r\n" +
				"\ten-gb;q=0.8, en;q=0.7",
			nil,
			header.AcceptLanguage{
				{Lang: "da"},
				{Lang: "en-gb", Params: make(header.Values).Set("q", "0.8")},
				{Lang: "en", Params: make(header.Values).Set("q", "0.7")},
			},
			nil,
		},

		{"accept 1", "Accept:", nil, header.Accept{}, nil},
		{
			"accept 2",
			"Accept: */*",
			nil,
			header.Accept{
				{MIMEType: header.MIMEType{Type: "*", Subtype: "*"}},
			},
			nil,
		},
		{
			"accept 3",
			"Accept: text/*;charset=utf-8;q=0.8;foo, application/json;q=0.5",
			nil,
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
					MIMEType: header.MIMEType{Type: "application", Subtype: "json"},
					Params:   make(header.Values).Append("q", "0.5"),
				},
			},
			nil,
		},
		{
			"accept 4",
			"Accept: text/plain;foo",
			nil,
			header.Accept{
				{
					MIMEType: header.MIMEType{Type: "text", Subtype: "plain"},
					Params:   make(header.Values).Append("foo", ""),
				},
			},
			nil,
		},
		{
			"accept 5",
			"Accept: text/plain;foo=bar",
			nil,
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
		},
		{"accept 6", "Accept: text", nil, &header.Any{Name: "Accept", Value: "text"}, nil},

		{"alert-info 1", "Alert-Info:", nil, &header.Any{Name: "Alert-Info"}, nil},
		{
			"alert-info 2",
			"Alert-Info:\r\n\t<https://example.com/a/b/c>;baz;foo=bar,\r\n\t\t<https://example.com/x/y/z>",
			nil,
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
		},

		{"allow 1", "Allow:", nil, header.Allow{}, nil},
		{"allow 2", "Allow:\r\n\tINVITE,   ACK,\r\n\tABC", nil, header.Allow{"INVITE", "ACK", "ABC"}, nil},

		{"any 1", "Abc-Xyz", nil, nil, grammar.ErrMalformedInput},
		{"any 2", "Abc-Xyz:", nil, &header.Any{Name: "Abc-Xyz"}, nil},
		{"any 3", "Abc-Xyz: abc", nil, &header.Any{Name: "Abc-Xyz", Value: "abc"}, nil},
		{"any 4", "Abc-Xyz: abc\r\n\tqwe", nil, &header.Any{Name: "Abc-Xyz", Value: "abc\r\n\tqwe"}, nil},

		{"authentication-info 1", "Authentication-Info:", nil, &header.Any{Name: "Authentication-Info"}, nil},
		{
			"authentication-info 2",
			`Authentication-Info: qwe="rty"`,
			nil,
			&header.Any{Name: "Authentication-Info", Value: `qwe="rty"`},
			nil,
		},
		{
			"authentication-info 3",
			"Authentication-Info:\r\n" +
				"\tnextnonce=\"qwe\",\r\n" +
				"\tqop=auth,\r\n" +
				"\trspauth=\"a0b5\",\r\n" +
				"\tcnonce=\"rty\",\r\n" +
				"\tnc=00000003\r\n",
			nil,
			&header.AuthenticationInfo{
				NextNonce:  "qwe",
				QOP:        "auth",
				RspAuth:    "a0b5",
				CNonce:     "rty",
				NonceCount: 3,
			},
			nil,
		},

		{"authorization 1", "Authorization: qwerty", nil, &header.Any{Name: "Authorization", Value: "qwerty"}, nil},
		{
			"authorization 2",
			"Authorization: Digest username=\"root\", realm=\"example.com\", nonce=\"qwerty\",\r\n" +
				"\turi=\"sip:example.com\", response=\"587245234b3434cc3412213e5f113a54\", algorithm=MD5,\r\n" +
				"\tcnonce=\"1q2w3e\", opaque=\"zxc\", qop=auth, nc=00000005, p1=abc, p2=\"a b c\"",
			nil,
			&header.Authorization{
				AuthCredentials: &header.DigestCredentials{
					Username:   "root",
					Realm:      "example.com",
					Nonce:      "qwerty",
					URI:        &uri.SIP{Addr: uri.Host("example.com")},
					Response:   "587245234b3434cc3412213e5f113a54",
					Algorithm:  "MD5",
					CNonce:     "1q2w3e",
					Opaque:     "zxc",
					QOP:        "auth",
					NonceCount: 5,
					Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			nil,
		},
		{
			"authorization 3",
			"Authorization: Bearer QweRTY123",
			nil,
			&header.Authorization{
				AuthCredentials: &header.BearerCredentials{
					Token: "QweRTY123",
				},
			},
			nil,
		},
		{
			"authorization 4",
			"Authorization: Custom p1=abc, p2=\"a b c\"",
			nil,
			&header.Authorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			nil,
		},

		{"call-id 1", "Call-ID: ", nil, &header.Any{Name: "Call-ID"}, nil},
		{"call-id 2", "Call-ID: qweRTY", nil, header.CallID("qweRTY"), nil},
		{"call-id 3", "Call-Id: qweRTY", nil, header.CallID("qweRTY"), nil},
		{"call-id 4", "i: qweRTY", nil, header.CallID("qweRTY"), nil},

		{"call-info 1", "Call-Info:", nil, &header.Any{Name: "Call-Info"}, nil},
		{
			"call-info 2",
			"Call-Info: <http://www.example.com/alice/photo.jpg> ;purpose=icon,\r\n" +
				"\t<http://www.example.com/alice/> ;purpose=info",
			nil,
			header.CallInfo{
				{
					URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/photo.jpg"},
					Params: make(header.Values).Set("purpose", "icon"),
				},
				{
					URI:    &uri.Any{Scheme: "http", Host: "www.example.com", Path: "/alice/"},
					Params: make(header.Values).Set("purpose", "info"),
				},
			},
			nil,
		},

		{"contact 1", "Contact:", nil, &header.Any{Name: "Contact"}, nil},
		{"contact 2", "Contact: *", nil, header.Contact{}, nil},
		{
			"contact 3",
			"Contact: sips:alice@127.0.0.1;tag=a48s",
			nil,
			header.Contact{{
				URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("127.0.0.1"), Secured: true},
				Params: make(header.Values).Set("tag", "a48s"),
			}},
			nil,
		},
		{
			"contact 4",
			"Contact: tel:+123;tag=a48s",
			nil,
			header.Contact{{
				URI:    &uri.Tel{Number: "+123"},
				Params: make(header.Values).Set("tag", "a48s"),
			}},
			nil,
		},
		{
			"contact 5",
			"Contact: \"A. G. Bell\" <sip:agb@bell-telephone.com;param=val>\r\n\t;tag=a48s",
			nil,
			header.Contact{{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("param", "val"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			}},
			nil,
		},
		{
			"contact 6",
			"Contact: \"Mr. Watson\" <sip:watson@worcester.bell-telephone.com>\r\n" +
				"\t;q=0.7; expires=3600,\r\n" +
				"\t\"Mr. Watson\" <mailto:watson@bell-telephone.com> ;q=0.1",
			nil,
			header.Contact{
				{
					DisplayName: "Mr. Watson",
					URI: &uri.SIP{
						User: uri.User("watson"),
						Addr: uri.Host("worcester.bell-telephone.com"),
					},
					Params: make(header.Values).Set("q", "0.7").Set("expires", "3600"),
				},
				{
					DisplayName: "Mr. Watson",
					URI:         &uri.Any{Scheme: "mailto", Opaque: "watson@bell-telephone.com"},
					Params:      make(header.Values).Set("q", "0.1"),
				},
			},
			nil,
		},
		{
			"contact 7",
			"m: <sips:bob@192.0.2.4;transport=UDP>;expires=60",
			nil,
			header.Contact{{
				URI: &uri.SIP{
					Secured: true,
					User:    uri.User("bob"),
					Addr:    uri.Host("192.0.2.4"),
					Params:  make(header.Values).Set("transport", "UDP"),
				},
				Params: make(header.Values).Set("expires", "60"),
			}},
			nil,
		},

		{"content-disposition 1", "Content-Disposition: ", nil, &header.Any{Name: "Content-Disposition"}, nil},
		{
			"content-disposition 2",
			"Content-Disposition: session ;\r\n\thandling=optional",
			nil,
			&header.ContentDisposition{
				Type:   "session",
				Params: make(header.Values).Set("handling", "optional"),
			},
			nil,
		},
		{
			"content-disposition 3",
			"Content-Disposition: custom ;\r\n\thandling=optional ; param=\"Hello world!\"",
			nil,
			&header.ContentDisposition{
				Type: "custom",
				Params: make(header.Values).
					Set("handling", "optional").
					Set("param", `"Hello world!"`),
			},
			nil,
		},

		{"content-encoding 1", "Content-Encoding: ", nil, &header.Any{Name: "Content-Encoding"}, nil},
		{"content-encoding 2", "Content-Encoding: gzip, QWE", nil, header.ContentEncoding{"gzip", "QWE"}, nil},
		{"content-encoding 3", "e: gzip, QWE", nil, header.ContentEncoding{"gzip", "QWE"}, nil},

		{"content-language 1", "Content-Language: ", nil, &header.Any{Name: "Content-Language"}, nil},
		{"content-language 2", "Content-Language: en, ru-RU", nil, header.ContentLanguage{"en", "ru-RU"}, nil},

		{"content-length 1", "Content-Length: ", nil, &header.Any{Name: "Content-Length"}, nil},
		{"content-length 2", "Content-Length: abc", nil, &header.Any{Name: "Content-Length", Value: "abc"}, nil},
		{"content-length 3", "Content-Length: 123", nil, header.ContentLength(123), nil},
		{"content-length 4", "l: 123", nil, header.ContentLength(123), nil},

		{"content-type 1", "Content-Type: ", nil, &header.Any{Name: "Content-Type"}, nil},
		{
			"content-type 2",
			"Content-Type: application/sdp;\r\n\tcharset=UTF-8",
			nil,
			&header.ContentType{
				Type:    "application",
				Subtype: "sdp",
				Params:  make(header.Values).Set("charset", "UTF-8"),
			},
			nil,
		},
		{
			"content-type 3",
			"c: application/sdp;\r\n\tcharset=UTF-8;q=0.5;foo=bar",
			nil,
			&header.ContentType{
				Type:    "application",
				Subtype: "sdp",
				Params:  make(header.Values).Set("charset", "UTF-8").Set("q", "0.5").Set("foo", "bar"),
			},
			nil,
		},

		{"cseq 1", "CSeq: ", nil, &header.Any{Name: "CSeq"}, nil},
		{"cseq 2", "CSeq: 4711 INVITE", nil, &header.CSeq{SeqNum: 4711, Method: "INVITE"}, nil},
		{"cseq 3", "Cseq: 4711 INVITE", nil, &header.CSeq{SeqNum: 4711, Method: "INVITE"}, nil},
		{"cseq 4", "CSeq: 33 CUSTOM", nil, &header.CSeq{SeqNum: 33, Method: "CUSTOM"}, nil},

		{
			"custom 1",
			"X-Custom: abc\r\n\tqwe",
			map[string]header.Parser{
				"x-custom": func(name string, value []byte) header.Header {
					return &customHeader{Name: name, Value: value}
				},
			},
			&customHeader{Name: "X-Custom", Value: []byte("abc\r\n\tqwe")},
			nil,
		},

		{"date 1", "Date: ", nil, &header.Any{Name: "Date"}, nil},
		{
			"date 2",
			"Date: Sat, 13 Nov 2010 23:29:00 GMT",
			nil,
			&header.Date{Time: time.Date(2010, 11, 13, 23, 29, 0, 0, time.UTC)},
			nil,
		},

		{"error-info 1", "Error-Info: ", nil, &header.Any{Name: "Error-Info"}, nil},
		{"error-info 2", "Error-Info: abc", nil, &header.Any{Name: "Error-Info", Value: "abc"}, nil},
		{
			"error-info 3",
			"Error-Info: <sip:not-in-service-recording@atlanta.com;p1=abc>;p2=zzz,\r\n" +
				"\t<http://example.org/qwerty>",
			nil,
			header.ErrorInfo{
				{
					URI: &uri.SIP{
						User:   uri.User("not-in-service-recording"),
						Addr:   uri.Host("atlanta.com"),
						Params: make(header.Values).Set("p1", "abc"),
					},
					Params: make(header.Values).Set("p2", "zzz"),
				},
				{
					URI: &uri.Any{Scheme: "http", Host: "example.org", Path: "/qwerty"},
				},
			},
			nil,
		},

		{"expires 1", "Expires: ", nil, &header.Any{Name: "Expires"}, nil},
		{"expires 2", "Expires: abc", nil, &header.Any{Name: "Expires", Value: "abc"}, nil},
		{"expires 3", "Expires: 0", nil, &header.Expires{}, nil},
		{"expires 4", "Expires: 3600", nil, &header.Expires{Duration: 3600 * time.Second}, nil},

		{"from 1", "From: ", nil, &header.Any{Name: "From"}, nil},
		{
			"from 2",
			"From: sip:alice@127.0.0.1;tag=a48s",
			nil,
			&header.From{
				URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("127.0.0.1")},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			nil,
		},
		{
			"from 3",
			"From: sips:alice@127.0.0.1;tag=a48s",
			nil,
			&header.From{
				URI:    &uri.SIP{Secured: true, User: uri.User("alice"), Addr: uri.Host("127.0.0.1")},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			nil,
		},
		{
			"from 4",
			"From: https://example.org/username?tag=a48s",
			nil,
			&header.From{
				URI:    &uri.Any{Scheme: "https", Host: "example.org", Path: "/username"},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			nil,
		},
		{
			"from 5",
			"From: \"A. G. Bell\" <sip:agb@bell-telephone.com;transport=udp>\r\n\t;tag=a48s",
			nil,
			&header.From{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("transport", "udp"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			nil,
		},
		{
			"from 6",
			"f: Anonymous <https://example.org/username>;tag=hyh8",
			nil,
			&header.From{
				DisplayName: "Anonymous",
				URI:         &uri.Any{Scheme: "https", Host: "example.org", Path: "/username"},
				Params:      make(header.Values).Set("tag", "hyh8"),
			},
			nil,
		},

		{"in-reply-to 1", "In-Reply-To: ", nil, &header.Any{Name: "In-Reply-To"}, nil},
		{
			"in-reply-to 2",
			"In-Reply-To: 70710@saturn.bell-tel.com, 17320@saturn.bell-tel.com",
			nil,
			header.InReplyTo{"70710@saturn.bell-tel.com", "17320@saturn.bell-tel.com"},
			nil,
		},

		{"max-forwards 1", "Max-Forwards: ", nil, &header.Any{Name: "Max-Forwards"}, nil},
		{"max-forwards 2", "Max-Forwards: 0", nil, header.MaxForwards(0), nil},
		{"max-forwards 3", "Max-Forwards: 10", nil, header.MaxForwards(10), nil},

		{"mime-version 1", "MIME-Version: ", nil, &header.Any{Name: "MIME-Version"}, nil},
		{"mime-version 2", "MIME-Version: 1.5", nil, header.MIMEVersion("1.5"), nil},

		{"min-expires 1", "Min-Expires: abc", nil, &header.Any{Name: "Min-Expires", Value: "abc"}, nil},
		{"min-expires 2", "Min-Expires: 0", nil, &header.MinExpires{}, nil},
		{"min-expires 3", "Min-Expires: 3600", nil, &header.MinExpires{Duration: 3600 * time.Second}, nil},

		{"organization 1", "Organization:", nil, header.Organization(""), nil},
		{"organization 2", "Organization: ", nil, header.Organization(""), nil},
		{"organization 3", "Organization: Boxes by Bob", nil, header.Organization("Boxes by Bob"), nil},

		{"priority 1", "Priority: ", nil, &header.Any{Name: "Priority"}, nil},
		{"priority 2", "Priority: non-urgent", nil, header.Priority("non-urgent"), nil},
		{"priority 3", "Priority: aaa-bbb-ccc", nil, header.Priority("aaa-bbb-ccc"), nil},

		{"proxy-authenticate 1", "Proxy-Authenticate: ", nil, &header.Any{Name: "Proxy-Authenticate"}, nil},
		{"proxy-authenticate 2", "Proxy-Authenticate: Digest", nil, &header.Any{Name: "Proxy-Authenticate", Value: "Digest"}, nil},
		{
			"proxy-authenticate 3",
			"Proxy-Authenticate: Digest realm=\"atlanta.com\",\r\n" +
				"\tdomain=\"sip:ss1.carrier.com http://example.com /a/b/c\", qop=\"auth,auth-int\",\r\n" +
				"\tnonce=\"f84f1cec41e6cbe5aea9c8e88d359\",\r\n" +
				"\topaque=\"\", stale=true, algorithm=MD5,\r\n" +
				"\tp1=abc, p2=\"a b c\"",
			nil,
			&header.ProxyAuthenticate{AuthChallenge: &header.DigestChallenge{
				Realm: "atlanta.com",
				Domain: []uri.URI{
					&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
					&uri.Any{Scheme: "http", Host: "example.com"},
					&uri.Any{Path: "/a/b/c"},
				},
				QOP:       []string{"auth", "auth-int"},
				Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
				Stale:     true,
				Algorithm: "MD5",
				Opaque:    "",
				Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			}},
			nil,
		},
		{
			"proxy-authenticate 4",
			"Proxy-Authenticate: Bearer realm=\"atlanta.com\",\r\n" +
				"\tscope=\"abc\", authz_server=\"http://example.com\", error=\"qwerty\",\r\n" +
				"\tp1=abc, p2=\"a b c\"",
			nil,
			&header.ProxyAuthenticate{AuthChallenge: &header.BearerChallenge{
				Realm:       "atlanta.com",
				Scope:       "abc",
				AuthzServer: &uri.Any{Scheme: "http", Host: "example.com"},
				Error:       "qwerty",
				Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			}},
			nil,
		},
		{
			"proxy-authenticate 5",
			"Proxy-Authenticate: Custom p1=abc, p2=\"a b c\"",
			nil,
			&header.ProxyAuthenticate{AuthChallenge: &header.AnyChallenge{
				Scheme: "Custom",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			}},
			nil,
		},

		{"proxy-authorization 1", "Proxy-Authorization: qwerty", nil, &header.Any{Name: "Proxy-Authorization", Value: "qwerty"}, nil},
		{
			"proxy-authorization 2",
			"Proxy-Authorization: Digest username=\"root\", realm=\"example.com\", nonce=\"qwerty\",\r\n" +
				"\turi=\"sip:example.com\", response=\"587245234b3434cc3412213e5f113a54\", algorithm=MD5,\r\n" +
				"\tcnonce=\"1q2w3e\", opaque=\"zxc\", qop=auth, nc=00000005, p1=abc, p2=\"a b c\"",
			nil,
			&header.ProxyAuthorization{
				AuthCredentials: &header.DigestCredentials{
					Username:   "root",
					Realm:      "example.com",
					Nonce:      "qwerty",
					URI:        &uri.SIP{Addr: uri.Host("example.com")},
					Response:   "587245234b3434cc3412213e5f113a54",
					Algorithm:  "MD5",
					CNonce:     "1q2w3e",
					Opaque:     "zxc",
					QOP:        "auth",
					NonceCount: 5,
					Params:     make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			nil,
		},
		{
			"proxy-authorization 3",
			"Proxy-Authorization: Bearer QweRTY123",
			nil,
			&header.ProxyAuthorization{
				AuthCredentials: &header.BearerCredentials{
					Token: "QweRTY123",
				},
			},
			nil,
		},
		{
			"proxy-authorization 4",
			"Proxy-Authorization: Custom p1=abc, p2=\"a b c\"",
			nil,
			&header.ProxyAuthorization{
				AuthCredentials: &header.AnyCredentials{
					Scheme: "Custom",
					Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
				},
			},
			nil,
		},

		{"proxy-require 1", "Proxy-Require: ", nil, &header.Any{Name: "Proxy-Require"}, nil},
		{"proxy-require 2", "Proxy-Require: 100rel, Foo, Bar", nil, header.ProxyRequire{"100rel", "Foo", "Bar"}, nil},

		{"record-route 1", "Record-Route: ", nil, &header.Any{Name: "Record-Route"}, nil},
		{"record-route 2", "Record-Route: abc", nil, &header.Any{Name: "Record-Route", Value: "abc"}, nil},
		{
			"record-route 3",
			"Record-Route: <sip:foo@bar;lr>;k=v,\r\n\t<sip:baz@qux>, <sip:quux@quuz>;a=b",
			nil,
			header.RecordRoute{
				{
					URI: &uri.SIP{
						User:   uri.User("foo"),
						Addr:   uri.Host("bar"),
						Params: make(header.Values).Set("lr", ""),
					},
					Params: make(header.Values).Set("k", "v"),
				},
				{URI: &uri.SIP{User: uri.User("baz"), Addr: uri.Host("qux")}},
				{
					URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
					Params: make(header.Values).Set("a", "b"),
				},
			},
			nil,
		},

		{"reply-to 1", "Reply-To: ", nil, &header.Any{Name: "Reply-To"}, nil},
		{
			"reply-to 2",
			"Reply-To: sip:alice@127.0.0.1;tag=a48s",
			nil,
			&header.ReplyTo{
				URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("127.0.0.1")},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			nil,
		},
		{
			"reply-to 3",
			"Reply-To: sips:alice@127.0.0.1;tag=a48s",
			nil,
			&header.ReplyTo{
				URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("127.0.0.1"), Secured: true},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			nil,
		},
		{
			"reply-to 4",
			"Reply-To: https://example.org/username?tag=a48s",
			nil,
			&header.ReplyTo{
				URI:    &uri.Any{Scheme: "https", Host: "example.org", Path: "/username"},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			nil,
		},
		{
			"reply-to 5",
			"Reply-To: \"A. G. Bell\" <sip:agb@bell-telephone.com>\r\n\t;tag=a48s",
			nil,
			&header.ReplyTo{
				DisplayName: "A. G. Bell",
				URI:         &uri.SIP{User: uri.User("agb"), Addr: uri.Host("bell-telephone.com")},
				Params:      make(header.Values).Set("tag", "a48s"),
			},
			nil,
		},

		{"require 1", "Require: ", nil, &header.Any{Name: "Require"}, nil},
		{"require 2", "Require: 100rel, Foo, Bar", nil, header.Require{"100rel", "Foo", "Bar"}, nil},

		{"retry-after 1", "Retry-After: ", nil, &header.Any{Name: "Retry-After"}, nil},
		{"retry-after 2", "Retry-After: abc", nil, &header.Any{Name: "Retry-After", Value: "abc"}, nil},
		{
			"retry-after 3",
			"Retry-After: 120\r\n\t( I'm in a meeting ) ;duration=60",
			nil,
			&header.RetryAfter{
				Delay:   120 * time.Second,
				Comment: "I'm in a meeting",
				Params:  make(header.Values).Set("duration", "60"),
			},
			nil,
		},

		{"route 1", "Route: ", nil, &header.Any{Name: "Route"}, nil},
		{"route 2", "Route: abc", nil, &header.Any{Name: "Route", Value: "abc"}, nil},
		{
			"route 3",
			"Route: <sip:foo@bar;lr>;k=v,\r\n\t<sip:baz@qux>, <sip:quux@quuz>;a=b",
			nil,
			header.Route{
				{
					URI: &uri.SIP{
						User:   uri.User("foo"),
						Addr:   uri.Host("bar"),
						Params: make(header.Values).Set("lr", ""),
					},
					Params: make(header.Values).Set("k", "v"),
				},
				{URI: &uri.SIP{User: uri.User("baz"), Addr: uri.Host("qux")}},
				{
					URI:    &uri.SIP{User: uri.User("quux"), Addr: uri.Host("quuz")},
					Params: make(header.Values).Set("a", "b"),
				},
			},
			nil,
		},

		{"server 1", "Server:", nil, &header.Any{Name: "Server"}, nil},
		{"server 2", "Server: ", nil, &header.Any{Name: "Server"}, nil},
		{"server 3", "Server: abc/v2 (DEF)", nil, header.Server("abc/v2 (DEF)"), nil},

		{"subject 1", "Subject:", nil, header.Subject(""), nil},
		{"subject 2", "Subject: ", nil, header.Subject(""), nil},
		{"subject 3", "Subject: Tech Support", nil, header.Subject("Tech Support"), nil},
		{"subject 4", "s: Tech Support", nil, header.Subject("Tech Support"), nil},

		{"supported 1", "Supported: ", nil, header.Supported{}, nil},
		{"supported 2", "Supported: 100rel, Foo, Bar", nil, header.Supported{"100rel", "Foo", "Bar"}, nil},
		{"supported 3", "k: 100rel, Foo, Bar", nil, header.Supported{"100rel", "Foo", "Bar"}, nil},

		{"timestamp 1", "Timestamp: ", nil, &header.Any{Name: "Timestamp"}, nil},
		{"timestamp 2", "Timestamp: 0.543", nil, &header.Timestamp{RequestTime: time.Unix(0, 543*1e6).UTC()}, nil},
		{
			"timestamp 3",
			"Timestamp: 946729845.350 5.32575",
			nil,
			&header.Timestamp{
				RequestTime:   time.Date(2000, time.January, 1, 12, 30, 45, 350*1e6, time.UTC),
				ResponseDelay: 5325750 * time.Microsecond,
			},
			nil,
		},

		{"to 1", "To: ", nil, &header.Any{Name: "To"}, nil},
		{
			"to 2",
			"To: sip:alice@127.0.0.1;tag=a48s",
			nil,
			&header.To{
				URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("127.0.0.1")},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			nil,
		},
		{
			"to 3",
			"To: sips:alice@127.0.0.1;tag=a48s",
			nil,
			&header.To{
				URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("127.0.0.1"), Secured: true},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			nil,
		},
		{
			"to 4",
			"To: https://example.org/username?tag=a48s",
			nil,
			&header.To{
				URI:    &uri.Any{Scheme: "https", Host: "example.org", Path: "/username"},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			nil,
		},
		{
			"to 5",
			"To: \"A. G. Bell\" <sip:agb@bell-telephone.com;param=val>\r\n\t;tag=a48s",
			nil,
			&header.To{
				DisplayName: "A. G. Bell",
				URI: &uri.SIP{
					User:   uri.User("agb"),
					Addr:   uri.Host("bell-telephone.com"),
					Params: make(header.Values).Set("param", "val"),
				},
				Params: make(header.Values).Set("tag", "a48s"),
			},
			nil,
		},
		{
			"to 6",
			"t: Anonymous <https://example.org/username>;tag=hyh8",
			nil,
			&header.To{
				DisplayName: "Anonymous",
				URI:         &uri.Any{Scheme: "https", Host: "example.org", Path: "/username"},
				Params:      make(header.Values).Set("tag", "hyh8"),
			},
			nil,
		},

		{"unsupported 1", "Unsupported: ", nil, &header.Any{Name: "Unsupported"}, nil},
		{"unsupported 2", "Unsupported: 100rel, Foo, Bar", nil, header.Unsupported{"100rel", "Foo", "Bar"}, nil},

		{"user-agent 1", "User-Agent:", nil, &header.Any{Name: "User-Agent"}, nil},
		{"user-agent 2", "User-Agent: ", nil, &header.Any{Name: "User-Agent"}, nil},
		{"user-agent 3", "User-Agent: abc/v2 (DEF)", nil, header.UserAgent("abc/v2 (DEF)"), nil},

		{"via 1", "Via:", nil, &header.Any{Name: "Via"}, nil},
		{"via 2", "Via: ", nil, &header.Any{Name: "Via"}, nil},
		{"via 3", "Via: abc", nil, &header.Any{Name: "Via", Value: "abc"}, nil},
		{
			"via 4",
			"Via: SIP / 2.0 / UDP     erlang.bell-telephone.com:5060;received=192.0.2.207;branch=z9hG4bK87asdks7,\r\n" +
				"\tSIP/2.0/UDP first.example.com: 4000;ttl=16\r\n" +
				"\t;maddr=224.2.0.1 ;branch=z9hG4bKa7c6a8dlze.1",
			nil,
			header.Via{
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "UDP",
					Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
					Params: make(header.Values).
						Set("received", "192.0.2.207").
						Set("branch", "z9hG4bK87asdks7"),
				},
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "UDP",
					Addr:      header.HostPort("first.example.com", 4000),
					Params: make(header.Values).
						Set("ttl", "16").
						Set("maddr", "224.2.0.1").
						Set("branch", "z9hG4bKa7c6a8dlze.1"),
				},
			},
			nil,
		},
		{
			"via 5",
			"Via: SIP/2.0/UDP erlang.bell-telephone.com:5060;branch=z9hG4bK87asdks7;rport",
			nil,
			header.Via{
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "UDP",
					Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
					Params: make(header.Values).
						Set("branch", "z9hG4bK87asdks7").
						Set("rport", ""),
				},
			},
			nil,
		},
		{
			"via 6",
			"Via: SIP/2.0/UDP erlang.bell-telephone.com:5060;branch=z9hG4bK87asdks7;rport=123",
			nil,
			header.Via{
				{
					Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
					Transport: "UDP",
					Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
					Params: make(header.Values).
						Set("branch", "z9hG4bK87asdks7").
						Set("rport", "123"),
				},
			},
			nil,
		},

		{"warning 1", "Warning: ", nil, &header.Any{Name: "Warning"}, nil},
		{"warning 2", "Warning: qwerty", nil, &header.Any{Name: "Warning", Value: "qwerty"}, nil},
		{"warning 3", "Warning: 307 isi.edu", nil, &header.Any{Name: "Warning", Value: "307 isi.edu"}, nil},
		{
			"warning 4",
			"Warning: 307 isi.edu \"\"",
			nil,
			header.Warning{
				{
					Code:  307,
					Agent: "isi.edu",
					Text:  "",
				},
			},
			nil,
		},
		{
			"warning 5",
			"Warning: 307 isi.edu \"Session parameter 'foo' not understood\",\r\n" +
				"\t301 isi.edu \"Incompatible network address type 'E.164'\"",
			nil,
			header.Warning{
				{
					Code:  307,
					Agent: "isi.edu",
					Text:  "Session parameter 'foo' not understood",
				},
				{
					Code:  301,
					Agent: "isi.edu",
					Text:  "Incompatible network address type 'E.164'",
				},
			},
			nil,
		},

		{"www-authenticate 1", "WWW-Authenticate: ", nil, &header.Any{Name: "WWW-Authenticate"}, nil},
		{"www-authenticate 2", "WWW-Authenticate: Digest", nil, &header.Any{Name: "WWW-Authenticate", Value: "Digest"}, nil},
		{
			"www-authenticate 3",
			"WWW-Authenticate: Digest realm=\"atlanta.com\",\r\n" +
				"\tdomain=\"sip:ss1.carrier.com http://example.com /a/b/c\", qop=\"auth,auth-int\",\r\n" +
				"\tnonce=\"f84f1cec41e6cbe5aea9c8e88d359\",\r\n" +
				"\topaque=\"\", stale=true, algorithm=MD5,\r\n" +
				"\tp1=abc, p2=\"a b c\"",
			nil,
			&header.WWWAuthenticate{AuthChallenge: &header.DigestChallenge{
				Realm: "atlanta.com",
				Domain: []uri.URI{
					&uri.SIP{Addr: uri.Host("ss1.carrier.com")},
					&uri.Any{Scheme: "http", Host: "example.com"},
					&uri.Any{Path: "/a/b/c"},
				},
				QOP:       []string{"auth", "auth-int"},
				Nonce:     "f84f1cec41e6cbe5aea9c8e88d359",
				Stale:     true,
				Algorithm: "MD5",
				Opaque:    "",
				Params:    make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			}},
			nil,
		},
		{
			"www-authenticate 4",
			"WWW-Authenticate: Bearer realm=\"atlanta.com\",\r\n" +
				"\tscope=\"abc\", authz_server=\"http://example.com\", error=\"qwerty\",\r\n" +
				"\tp1=abc, p2=\"a b c\"",
			nil,
			&header.WWWAuthenticate{AuthChallenge: &header.BearerChallenge{
				Realm:       "atlanta.com",
				Scope:       "abc",
				AuthzServer: &uri.Any{Scheme: "http", Host: "example.com"},
				Error:       "qwerty",
				Params:      make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			}},
			nil,
		},
		{
			"www-authenticate 5",
			"WWW-Authenticate: Custom p1=abc, p2=\"a b c\"",
			nil,
			&header.WWWAuthenticate{AuthChallenge: &header.AnyChallenge{
				Scheme: "Custom",
				Params: make(header.Values).Set("p1", "abc").Set("p2", `"a b c"`),
			}},
			nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			for n, p := range c.hdrPrs {
				header.RegisterParser(n, p)
			}
			defer func() {
				for n := range c.hdrPrs {
					header.UnregisterParser(n)
				}
			}()

			var (
				gotHdr header.Header
				gotErr error
			)
			switch src := c.src.(type) {
			case string:
				gotHdr, gotErr = header.Parse(src)
			case []byte:
				gotHdr, gotErr = header.Parse(src)
			}
			if c.wantErr == nil {
				if diff := cmp.Diff(gotHdr, c.wantHdr, cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("header.Parse(%q) = %+v, want %+v\ndiff (-got +want):\n%v",
						fmt.Sprintf("%v", c.src), gotHdr, c.wantHdr, diff,
					)
				}
				if gotErr != nil {
					t.Errorf("header.Parse(%q) error = %v, want nil", fmt.Sprintf("%v", c.src), gotErr)
				}
			} else {
				if diff := cmp.Diff(gotErr, c.wantErr, cmpopts.EquateErrors()); diff != "" {
					t.Errorf("header.Parse(%q) error = %v, want %q\ndiff (-got +want):\n%v",
						fmt.Sprintf("%v", c.src), gotErr, c.wantErr, diff,
					)
				}
			}
		})
	}
}

type customHeader struct {
	Name  string
	Value []byte
}

func (h *customHeader) CanonicName() header.Name { return header.Name(h.Name) }

func (h *customHeader) CompactName() header.Name { return header.Name(h.Name) }

func (h *customHeader) RenderValue() string {
	return string(h.Value)
}

func (h *customHeader) Render(*header.RenderOptions) string {
	return h.RenderValue()
}

func (h *customHeader) RenderTo(w io.Writer, _ *header.RenderOptions) (int, error) {
	return errtrace.Wrap2(w.Write([]byte(h.RenderValue())))
}

func (h *customHeader) String() string { return string(h.Value) }

func (h *customHeader) Clone() header.Header { return &customHeader{Name: h.Name, Value: h.Value} }

func (h *customHeader) IsValid() bool { return h != nil && h.Name != "" }

func (h *customHeader) Equal(val any) bool {
	return reflect.DeepEqual(h, val)
}
