package uri_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/internal/grammar"
	"github.com/ghettovoice/gosip/sip/uri"
)

var _ = Describe("URI", Label("sip", "uri"), func() {
	Describe("SIP", func() {
		assertURIParsing(
			// region
			Entry(nil, "", nil, grammar.ErrEmptyInput),
			Entry(nil, "sip:example.com", &uri.SIP{Addr: uri.Host("example.com")}, nil),
			Entry(nil, "sips:example.com", &uri.SIP{Secured: true, Addr: uri.Host("example.com")}, nil),
			// Entry("notsip:example.com", "notsip:example.com", nil, "invalid argument: malformed source"),
			// Entry("sip:", "sip:", nil, "invalid argument: malformed source"),
			Entry(nil, "sip:localhost", &uri.SIP{Addr: uri.Host("localhost")}, nil),
			Entry(nil, "sip:example.COM", &uri.SIP{Addr: uri.Host("example.COM")}, nil),
			Entry(nil, "sip:example#.com", &uri.SIP{}, grammar.ErrMalformedInput),
			Entry(nil, "sip:example-a.com", &uri.SIP{Addr: uri.Host("example-a.com")}, nil),
			Entry(nil, "sip::5060", nil, grammar.ErrMalformedInput),
			Entry(nil, "sip:example.com:5060", &uri.SIP{Addr: uri.HostPort("example.com", 5060)}, nil),
			Entry(nil, "sip:127.0.0.1", &uri.SIP{Addr: uri.Host("127.0.0.1")}, nil),
			Entry(nil, "sip:127.0.0.1:5060", &uri.SIP{Addr: uri.HostPort("127.0.0.1", 5060)}, nil),
			Entry(nil, "sip:127.0.0.1:?priority=urgent", nil, grammar.ErrMalformedInput),
			Entry(nil, "sip:[2001:db8::9:1]", &uri.SIP{Addr: uri.Host("2001:db8::9:1")}, nil),
			Entry(nil, "sip:[2001:db8::9:1]:5060", &uri.SIP{Addr: uri.HostPort("2001:db8::9:1", 5060)}, nil),
			Entry(nil,
				"sip:admin@example.com:5060",
				&uri.SIP{User: uri.User("admin"), Addr: uri.HostPort("example.com", 5060)},
				nil,
			),
			Entry(nil,
				"sip:%40dmin@example.com",
				&uri.SIP{User: uri.User("@dmin"), Addr: uri.Host("example.com")},
				nil,
			),
			Entry(nil,
				"sip:admin:@example.com",
				&uri.SIP{User: uri.UserPassword("admin", ""), Addr: uri.Host("example.com")},
				nil,
			),
			Entry(nil,
				"sip:admin:qw3rty!+@example.com",
				&uri.SIP{User: uri.UserPassword("admin", "qw3rty!+"), Addr: uri.Host("example.com")},
				nil,
			),
			Entry(nil,
				"sip:admin;field=value@example.com",
				&uri.SIP{User: uri.User("admin;field=value"), Addr: uri.Host("example.com")},
				nil,
			),
			Entry(nil, "sip::passwd@example.com", nil, grammar.ErrMalformedInput),
			Entry(nil,
				"sip:admin@example.com;transport=tcp",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Params: make(uri.Values).Set("transport", "tcp"),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com;transport=wss",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Params: make(uri.Values).Set("transport", "wss"),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com;transport=any",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Params: make(uri.Values).Set("transport", "any"),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com;TranspOrt=UDP",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Params: make(uri.Values).Set("transport", "UDP"),
				},
				nil,
			),
			Entry(nil,
				"sip:+1-222-333;field=qwerty@example.com;user=phone",
				&uri.SIP{User: uri.User("+1-222-333;field=qwerty"), Addr: uri.Host("example.com"),
					Params: make(uri.Values).Set("user", "phone"),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com;user=any",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Params: make(uri.Values).Set("user", "any"),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com;method=INVITE",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Params: make(uri.Values).Set("method", "INVITE"),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com;method=refer",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Params: make(uri.Values).Set("method", "refer"),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com;ttl=50",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Params: make(uri.Values).Set("ttl", "50"),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com;maddr=example.com",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Params: make(uri.Values).Set("maddr", "example.com"),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com;maddr=127.0.0.1",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Params: make(uri.Values).Set("maddr", "127.0.0.1"),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com;lr",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Params: make(uri.Values).Set("lr", ""),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com;foo=bar",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Params: make(uri.Values).Set("foo", "bar"),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com;foo%3D=b%40r",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Params: make(uri.Values).Set("foo=", "b@r"),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com;foo%3D=b%40r;baz",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Params: make(uri.Values).Set("foo=", "b@r").Set("baz", ""),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com;transport=wss;user=phone;lr;foo%3D=b%40r;transport=TCP;bAz=%E4%B8%96%E7%95%8C",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Params: make(uri.Values).
						Append("transport", "wss").
						Append("user", "phone").
						Append("lr", "").
						Append("foo=", "b@r").
						Append("transport", "TCP").
						Append("baz", "世界"),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com?subject=hello%20world",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Headers: make(uri.Values).Append("subject", "hello world"),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com?subject=hello%20world&to=admin%40example.com&body=QWERTY&to=root%40example.org",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Headers: make(uri.Values).
						Append("subject", "hello world").
						Append("to", "admin@example.com").
						Append("body", "QWERTY").
						Append("to", "root@example.org"),
				},
				nil,
			),
			Entry(nil,
				"sip:admin@example.com?=hello%20world&to=admin%40example.com",
				nil,
				grammar.ErrMalformedInput,
			),
			Entry(nil,
				"sip:admin@example.com?%20=hello%20world&To=admin%40example.com&priority=",
				&uri.SIP{User: uri.User("admin"), Addr: uri.Host("example.com"),
					Headers: make(uri.Values).
						Append(" ", "hello world").
						Append("to", "admin@example.com").
						Append("priority", ""),
				},
				nil,
			),
			// endregion
		)

		assertURIRendering(
			// region
			Entry(nil, (*uri.SIP)(nil), ""),
			Entry(nil, &uri.SIP{}, "sip:"),
			Entry(nil, &uri.SIP{Addr: uri.Host("example.com")}, "sip:example.com"),
			Entry(nil, &uri.SIP{Secured: true}, "sips:"),
			Entry(nil,
				&uri.SIP{Secured: true, Addr: uri.HostPort("example.com", 5060)},
				"sips:example.com:5060",
			),
			Entry(nil,
				&uri.SIP{Addr: uri.Host("example.com"), User: uri.User("root")},
				"sip:root@example.com",
			),
			Entry(nil,
				&uri.SIP{Addr: uri.Host("example.com"), User: uri.UserPassword("root", "")},
				"sip:root:@example.com",
			),
			Entry(nil,
				&uri.SIP{
					Addr: uri.Host("example.com"),
					User: uri.UserPassword("root@;field=123", "p@sswd;qwe"),
				},
				"sip:root%40;field=123:p%40sswd%3Bqwe@example.com",
			),
			Entry(nil,
				&uri.SIP{
					Addr: uri.Host("example.com"),
					Params: make(uri.Values).
						Append("Transport", "wss").
						Append("method", "INVITE").
						Append("fOo[ 世 ]", "界 !").
						Append("b@z", "").
						Append("method", "UPDATE"),
				},
				"sip:example.com;b%40z;foo[%20%E4%B8%96%20]=%E7%95%8C%20!;method=UPDATE;transport=wss",
			),
			Entry(nil,
				&uri.SIP{
					User: uri.UserPassword("root", ""),
					Addr: uri.Host("example.com"),
					Params: make(uri.Values).
						Append("transport", "UDP").
						Append("lr", ""),
					Headers: make(uri.Values).
						Append("Subject", "Hello world!").
						Append("priority", "emergency").
						Append("x-hE@DER", "").
						Append("priority", "URGENT"),
				},
				"sip:root:@example.com;lr;transport=UDP?priority=emergency&priority=URGENT&subject=Hello%20world!&x-he%40der=",
			),
			// endregion
		)

		assertURIComparing(
			// region
			Entry(nil, (*uri.SIP)(nil), nil, false),
			Entry(nil, (*uri.SIP)(nil), (*uri.SIP)(nil), true),
			Entry(nil, &uri.SIP{}, (*uri.SIP)(nil), false),
			Entry(nil, &uri.SIP{}, &uri.SIP{}, true),
			Entry(nil, &uri.SIP{}, uri.SIP{}, true),
			Entry(nil,
				&uri.SIP{Addr: uri.Host("example.com"), User: uri.User("root")},
				&uri.SIP{Addr: uri.Host("example.com"), User: uri.User("root")},
				true,
			),
			Entry(nil,
				&uri.SIP{Addr: uri.Host("example.com"), User: uri.User("root")},
				uri.SIP{Addr: uri.Host("example.com"), User: uri.User("root")},
				true,
			),
			Entry(nil,
				&uri.SIP{Addr: uri.Host("example.com"), User: uri.User("root")},
				&uri.SIP{Addr: uri.Host("EXAMPLE.COM"), User: uri.User("root")},
				true,
			),
			Entry(nil,
				&uri.SIP{Addr: uri.Host("example.com"), User: uri.User("root")},
				&uri.SIP{Secured: true, Addr: uri.Host("example.com"), User: uri.User("root")},
				false,
			),
			Entry(nil,
				&uri.SIP{Addr: uri.Host("example.com"), User: uri.User("root")},
				&uri.SIP{Addr: uri.Host("example.com")},
				false,
			),
			Entry(nil,
				&uri.SIP{
					User:   uri.User("root"),
					Addr:   uri.Host("example.com"),
					Params: make(uri.Values).Set("foo", "bar"),
				},
				&uri.SIP{
					User: uri.User("root"),
					Addr: uri.Host("example.com"),
				},
				true,
			),
			Entry(nil,
				&uri.SIP{
					User:   uri.User("root"),
					Addr:   uri.Host("example.com"),
					Params: make(uri.Values).Set("foo", "bar"),
				},
				&uri.SIP{
					User:   uri.User("root"),
					Addr:   uri.Host("example.com"),
					Params: make(uri.Values).Set("baz", ""),
				},
				true,
			),
			Entry(nil,
				&uri.SIP{
					User:   uri.User("root"),
					Addr:   uri.Host("example.com"),
					Params: make(uri.Values).Set("foo", "bar"),
				},
				&uri.SIP{
					User:   uri.User("root"),
					Addr:   uri.Host("example.com"),
					Params: make(uri.Values).Set("FOO", "BAR"),
				},
				true,
			),
			Entry(nil,
				&uri.SIP{
					User:   uri.User("root"),
					Addr:   uri.Host("example.com"),
					Params: make(uri.Values).Set("foo", "bar"),
				},
				&uri.SIP{
					User:   uri.User("root"),
					Addr:   uri.Host("example.com"),
					Params: make(uri.Values).Set("foo", "baz"),
				},
				false,
			),
			Entry(nil,
				&uri.SIP{
					User:   uri.User("root"),
					Addr:   uri.Host("example.com"),
					Params: make(uri.Values).Set("transport", "UDP").Set("lr", ""),
				},
				&uri.SIP{
					User:   uri.User("root"),
					Addr:   uri.Host("example.com"),
					Params: make(uri.Values).Set("lr", ""),
				},
				false,
			),
			Entry(nil,
				&uri.SIP{
					User:   uri.User("root"),
					Addr:   uri.Host("example.com"),
					Params: make(uri.Values).Set("transport", "UDP"),
				},
				&uri.SIP{
					User:   uri.User("root"),
					Addr:   uri.Host("example.com"),
					Params: make(uri.Values).Set("transport", "UDP").Set("lr", ""),
				},
				false,
			),
			Entry(nil,
				&uri.SIP{
					User:   uri.User("root"),
					Addr:   uri.Host("example.com"),
					Params: make(uri.Values).Set("transport", "udp").Set("lr", ""),
				},
				&uri.SIP{
					User:   uri.User("root"),
					Addr:   uri.Host("EXAMPLE.com"),
					Params: make(uri.Values).Set("transport", "UDP").Set("lr", ""),
				},
				true,
			),
			Entry(nil,
				&uri.SIP{
					User: uri.User("root"),
					Addr: uri.Host("example.com"),
				},
				&uri.SIP{
					User:   uri.User("root"),
					Addr:   uri.Host("EXAMPLE.com"),
					Params: make(uri.Values).Set("transport", "UDP").Set("lr", ""),
				},
				false,
			),
			Entry(nil,
				&uri.SIP{
					User:   uri.User("root"),
					Addr:   uri.Host("example.com"),
					Params: make(uri.Values).Set("transport", "udp").Set("lr", ""),
				},
				&uri.SIP{
					User: uri.User("root"),
					Addr: uri.Host("EXAMPLE.com"),
				},
				false,
			),
			Entry(nil,
				&uri.SIP{
					User:    uri.User("root"),
					Addr:    uri.Host("example.com"),
					Params:  make(uri.Values).Set("transport", "udp").Set("lr", ""),
					Headers: make(uri.Values).Set("priority", "urgent"),
				},
				&uri.SIP{
					User:   uri.User("root"),
					Addr:   uri.Host("example.com"),
					Params: make(uri.Values).Set("transport", "udp").Set("lr", ""),
				},
				false,
			),
			Entry(nil,
				&uri.SIP{
					User:    uri.User("root"),
					Addr:    uri.Host("example.com"),
					Params:  make(uri.Values).Set("transport", "udp").Set("lr", ""),
					Headers: make(uri.Values).Set("priority", "urgent"),
				},
				&uri.SIP{
					User:    uri.User("root"),
					Addr:    uri.Host("example.com"),
					Params:  make(uri.Values).Set("transport", "udp").Set("lr", ""),
					Headers: make(uri.Values).Set("priority", "emergency"),
				},
				false,
			),
			Entry(nil,
				&uri.SIP{
					User:    uri.User("root"),
					Addr:    uri.Host("example.com"),
					Params:  make(uri.Values).Set("transport", "udp").Set("lr", ""),
					Headers: make(uri.Values).Set("priority", "urgent"),
				},
				&uri.SIP{
					User:    uri.User("root"),
					Addr:    uri.Host("example.com"),
					Params:  make(uri.Values).Set("transport", "udp").Set("lr", ""),
					Headers: make(uri.Values).Set("subject", "Hello world!"),
				},
				false,
			),
			Entry(nil,
				&uri.SIP{
					User:    uri.User("root"),
					Addr:    uri.Host("example.com"),
					Params:  make(uri.Values).Set("transport", "udp").Set("lr", ""),
					Headers: make(uri.Values).Set("priority", "urgent").Set("subject", "Hello World!"),
				},
				&uri.SIP{
					User:    uri.User("root"),
					Addr:    uri.Host("example.com"),
					Params:  make(uri.Values).Set("transport", "udp").Set("lr", ""),
					Headers: make(uri.Values).Set("Priority", "URGENT").Set("subject", "hello world!"),
				},
				true,
			),
			// endregion
		)

		assertURIValidating(
			// region
			Entry(nil, &uri.SIP{}, false),
			Entry(nil, &uri.SIP{User: uri.User("root")}, false),
			Entry(nil, &uri.SIP{Addr: uri.Host("example.com")}, true),
			Entry(nil, &uri.SIP{Addr: uri.Host("")}, false),
			Entry(nil, &uri.SIP{User: uri.User("root"), Addr: uri.Host("example.com")}, true),
			Entry(nil, &uri.SIP{User: uri.User(""), Addr: uri.Host("example.com")}, true),
			// endregion
		)

		DescribeTable("cloning", Label("cloning"),
			// region
			func(u1 *uri.SIP) {
				u2 := u1.Clone()
				if u1 == nil {
					Expect(u2).To(BeNil(), "assert cloned URI is nil")
				} else {
					u2 := u2.(*uri.SIP)
					Expect(u2).To(Equal(u1), "assert cloned URI is equal to the original URI")
					Expect(reflect.ValueOf(u2).Pointer()).
						ToNot(Equal(reflect.ValueOf(u1).Pointer()), "assert cloned URI pointer is different than the original")
					if u1.Params != nil {
						Expect(reflect.ValueOf(u2.Params).Pointer()).
							ToNot(Equal(reflect.ValueOf(u1.Params).Pointer()), "assert cloned Params field pointer is different than the original")
					}
					if u1.Headers != nil {
						Expect(reflect.ValueOf(u2.Headers).Pointer()).
							ToNot(Equal(reflect.ValueOf(u1.Headers).Pointer()), "assert cloned Headers field pointer is different than the original")
					}
				}
			},
			EntryDescription("%#v"),
			Entry(nil, (*uri.SIP)(nil)),
			Entry(nil, &uri.SIP{
				User:    uri.User("root"),
				Addr:    uri.HostPort("example.com", 5060),
				Params:  make(uri.Values).Set("transport", "udp"),
				Headers: make(uri.Values).Set("priority", "urgent"),
			}),
			// endregion
		)
	})

	Describe("UserInfo", func() {
		DescribeTable("initializing",
			// region
			func(usr, pwd string, pwdSet bool) {
				var ui uri.UserInfo
				if pwdSet {
					ui = uri.UserPassword(usr, pwd)
				} else {
					ui = uri.User(usr)
				}
				Expect(ui.Username()).To(Equal(usr), "assert username = %s", usr)
				pw, ok := ui.Password()
				Expect(pw).To(Equal(pwd), "assert password = %s", pwd)
				Expect(ok).To(Equal(pwdSet), "assert password set = %v", pwdSet)
			},
			EntryDescription(`with username = %q, password = %q, password set = %v`),
			// region entries
			Entry(nil, "root", "", false),
			Entry(nil, "root", "qwerty", true),
			Entry(nil, "root", "", true),
			Entry(nil, "", "qwerty", true),
			// endregion
			// endregion
		)

		DescribeTable("rendering", Label("rendering"),
			// region
			func(ui uri.UserInfo, expect string) {
				Expect(ui.String()).To(Equal(expect))
			},
			EntryDescription("%#[1]v"),
			Entry(nil, uri.UserInfo{}, ""),
			Entry(nil, uri.User(""), ""),
			Entry(nil, uri.UserPassword("", "qwerty"), ":qwerty"),
			Entry(nil, uri.User("root?"), "root?"),
			Entry(nil, uri.User("root@"), "root%40"),
			Entry(nil, uri.UserPassword("root", ""), "root:"),
			Entry(nil, uri.UserPassword("root", " "), "root:%20"),
			Entry(nil, uri.UserPassword("Root", " @QWE"), "Root:%20%40QWE"),
			// endregion
		)

		DescribeTable("comparing", Label("comparing"),
			// region
			func(ui uri.UserInfo, v any, expect bool) {
				Expect(ui.Equal(v)).To(Equal(expect))
			},
			EntryDescription("%#[1]v with value = %#[2]v"),
			Entry(nil, uri.UserInfo{}, nil, false),
			Entry(nil, uri.UserInfo{}, (*uri.UserInfo)(nil), false),
			Entry(nil, uri.UserInfo{}, uri.UserInfo{}, true),
			Entry(nil, uri.UserInfo{}, &uri.UserInfo{}, true),
			Entry(nil, uri.User("root"), uri.UserInfo{}, false),
			Entry(nil, uri.UserPassword("root", ""), uri.User("root"), false),
			Entry(nil,
				uri.UserPassword("root", "qwerty"),
				uri.UserPassword("root", "qwerty"),
				true,
			),
			Entry(nil,
				uri.UserPassword("root", "qwerty"),
				uri.UserPassword("ROOT", "qwerty"),
				false,
			),
			Entry(nil,
				uri.UserPassword("root", "qwerty"),
				uri.UserPassword("root", "QWERTY"),
				false,
			),
			Entry(nil,
				uri.UserPassword("root", "qwerty"),
				uri.UserPassword("root", "qwerty"),
				true,
			),
			// endregion
		)

		DescribeTable("validating", Label("validating"),
			// region
			func(ui uri.UserInfo, expect bool) {
				Expect(ui.IsValid()).To(Equal(expect))
			},
			EntryDescription("%[1]q"),
			Entry(nil, uri.UserInfo{}, false),
			Entry(nil, uri.User("root"), true),
			Entry(nil, uri.User("r@ot"), true),
			Entry(nil, uri.UserPassword("root", ""), true),
			Entry(nil, uri.UserPassword("root", "qwerty"), true),
			// endregion
		)
	})
})
