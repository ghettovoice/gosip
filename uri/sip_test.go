package uri_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/uri"
)

func TestSIP_Render(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  *uri.SIP
		want string
	}{
		{"nil", (*uri.SIP)(nil), ""},
		{"zero", &uri.SIP{}, "sip:"},
		{"host and port", &uri.SIP{Addr: uri.HostPort("example.com", 5060)}, "sip:example.com:5060"},
		{"secured", &uri.SIP{Secured: true, Addr: uri.HostPort("example.com", 5060)}, "sips:example.com:5060"},
		{
			"user with empty password",
			&uri.SIP{Addr: uri.Host("example.com"), User: uri.UserPassword("root", "")},
			"sip:root:@example.com",
		},
		{
			"user with params encoded and password",
			&uri.SIP{
				Addr: uri.Host("example.com"),
				User: uri.UserPassword("root@;field=123", "p@sswd;qwe"),
			},
			"sip:root%40;field=123:p%40sswd%3Bqwe@example.com",
		},
		{
			"uri params and headers",
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
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.uri.Render(nil); got != c.want {
				t.Errorf("uri.Render(nil) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSIP_RenderTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		uri     *uri.SIP
		wantRes string
		wantErr error
	}{
		{"nil", (*uri.SIP)(nil), "", nil},
		{"zero", &uri.SIP{}, "sip:", nil},
		{"filled", &uri.SIP{Addr: uri.HostPort("example.com", 5060)}, "sip:example.com:5060", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			_, err := c.uri.RenderTo(&sb, nil)
			if diff := cmp.Diff(err, c.wantErr, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("uri.RenderTo(sb, nil) error = %v, want %v\ndiff (-got +want):\n%v", err, c.wantErr, diff)
			}
			if got := sb.String(); got != c.wantRes {
				t.Errorf("sb.String() = %q, want %q", got, c.wantRes)
			}
		})
	}
}

func TestSIP_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  *uri.SIP
		want string
	}{
		{"nil", (*uri.SIP)(nil), ""},
		{"zero", &uri.SIP{}, "sip:"},
		{"filled", &uri.SIP{Addr: uri.HostPort("example.com", 5060)}, "sip:example.com:5060"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.uri.String(); got != c.want {
				t.Errorf("uri.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSIP_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  *uri.SIP
		val  any
		want bool
	}{
		{"nil ptr to nil", (*uri.SIP)(nil), nil, false},
		{"nil ptr to nil ptr", (*uri.SIP)(nil), (*uri.SIP)(nil), true},
		{"zero ptr to nil ptr", &uri.SIP{}, (*uri.SIP)(nil), false},
		{"nil ptr to zero ptr", (*uri.SIP)(nil), &uri.SIP{}, false},
		{"zero ptr to zero ptr", &uri.SIP{}, &uri.SIP{}, true},
		{"zero ptr to zero val", &uri.SIP{}, uri.SIP{}, true},
		{
			"type mismatch",
			&uri.SIP{Addr: uri.HostPort("example.com", 5060)},
			"sip:example.com:5060",
			false,
		},
		{
			"secured to non-secured",
			&uri.SIP{Addr: uri.Host("example.com")},
			&uri.SIP{Secured: true, Addr: uri.Host("example.com")},
			false,
		},
		{
			"secured to secured",
			&uri.SIP{Secured: true, Addr: uri.Host("example.com")},
			&uri.SIP{Secured: true, Addr: uri.Host("example.com")},
			true,
		},
		{
			"addr match",
			&uri.SIP{Addr: uri.HostPort("example.com", 5060)},
			&uri.SIP{Addr: uri.HostPort("EXAMPLE.com", 5060)},
			true,
		},
		{
			"addr not match 1",
			&uri.SIP{Addr: uri.HostPort("example.com", 5060)},
			&uri.SIP{Addr: uri.HostPort("example.com", 5061)},
			false,
		},
		{
			"addr not match 2",
			&uri.SIP{Addr: uri.HostPort("example.com", 5060)},
			&uri.SIP{Addr: uri.Host("example.com")},
			false,
		},
		{
			"user match",
			&uri.SIP{Addr: uri.Host("example.com"), User: uri.UserPassword("root", "qwe")},
			&uri.SIP{Addr: uri.Host("example.com"), User: uri.UserPassword("root", "qwe")},
			true,
		},
		{
			"user not match 1",
			&uri.SIP{Addr: uri.Host("example.com"), User: uri.UserPassword("root", "")},
			&uri.SIP{Addr: uri.Host("example.com"), User: uri.User("root")},
			false,
		},
		{
			"user not match 2",
			&uri.SIP{Addr: uri.Host("example.com"), User: uri.User("root")},
			&uri.SIP{Addr: uri.Host("example.com")},
			false,
		},
		{
			"user not match 3",
			&uri.SIP{Addr: uri.Host("example.com"), User: uri.User("root")},
			&uri.SIP{Addr: uri.Host("example.com"), User: uri.User("ROOT")},
			false,
		},
		{
			"params match 1",
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
				Params: make(uri.Values).
					Set("FIELD1", "qwe").
					Set("FIELD2", "").
					Set("transport", "UDP").
					Set("lr", "").
					Set("FIELD3", "123"),
			},
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
				Params: make(uri.Values).
					Set("LR", "").
					Set("Transport", "udp").
					Set("field1", "QWE").
					Set("Field2", "").
					Set("field4", "abc"),
			},
			true,
		},
		{
			"params match 2",
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
				Params: make(uri.Values).
					Set("FIELD1", "qwe").
					Set("FIELD2", "").
					Set("FIELD3", "123"),
			},
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
			},
			true,
		},
		{
			"params match 3",
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
			},
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
				Params: make(uri.Values).
					Set("FIELD1", "qwe").
					Set("FIELD2", "").
					Set("FIELD3", "123"),
			},
			true,
		},
		{
			"params not match 1",
			&uri.SIP{
				User:   uri.User("root"),
				Addr:   uri.Host("example.com"),
				Params: make(uri.Values).Set("field1", "qwe"),
			},
			&uri.SIP{
				User:   uri.User("root"),
				Addr:   uri.Host("example.com"),
				Params: make(uri.Values).Set("field1", "xyz"),
			},
			false,
		},
		{
			"params not match 2",
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
				Params: make(uri.Values).
					Set("transport", "tcp").
					Set("lr", ""),
			},
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
				Params: make(uri.Values).
					Set("Transport", "udp").
					Set("method", "REGISTER"),
			},
			false,
		},
		{
			"params not match 3",
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
				Params: make(uri.Values).
					Set("transport", "tcp").
					Set("lr", ""),
			},
			&uri.SIP{
				User:   uri.User("root"),
				Addr:   uri.Host("example.com"),
				Params: make(uri.Values).Set("transport", "tcp"),
			},
			false,
		},
		{
			"params not match 4",
			&uri.SIP{
				User:   uri.User("root"),
				Addr:   uri.Host("example.com"),
				Params: make(uri.Values).Set("transport", "tcp"),
			},
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
				Params: make(uri.Values).
					Set("transport", "tcp").
					Set("lr", ""),
			},
			false,
		},
		{
			"headers match",
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
				Headers: make(uri.Values).
					Set("priority", "urgent").
					Set("subject", "Hello World!").
					Set("body", ""),
			},
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
				Headers: make(uri.Values).
					Set("Priority", "URGENT").
					Set("subject", "hello world!").
					Set("body", ""),
			},
			true,
		},
		{
			"headers not match 1",
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
				Headers: make(uri.Values).
					Set("to", "sip:root@example.com").
					Set("priority", "urgent").
					Set("body", "qqq"),
			},
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
				Headers: make(uri.Values).
					Set("priority", "urgent").
					Set("subject", "hello world!").
					Set("body", "qqq"),
			},
			false,
		},
		{
			"headers not match 2",
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
				Headers: make(uri.Values).
					Set("priority", "urgent").
					Set("to", "sip:root@example.com"),
			},
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
				Headers: make(uri.Values).
					Set("priority", "emergency").
					Set("subject", "hello world!"),
			},
			false,
		},
		{
			"headers not match 3",
			&uri.SIP{
				User: uri.User("root"),
				Addr: uri.Host("example.com"),
				Headers: make(uri.Values).
					Set("priority", "urgent").
					Set("subject", "hello world!"),
			},
			&uri.SIP{
				User:    uri.User("root"),
				Addr:    uri.Host("example.com"),
				Headers: make(uri.Values).Set("priority", "emergency"),
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.uri.Equal(c.val); got != c.want {
				t.Errorf("uri.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestSIP_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  *uri.SIP
		want bool
	}{
		{"nil", (*uri.SIP)(nil), false},
		{"zero", &uri.SIP{}, false},
		{"invalid addr", &uri.SIP{Addr: uri.Host("")}, false},
		{"valid", &uri.SIP{User: uri.UserPassword("root", "qwe"), Addr: uri.HostPort("example.com", 5060)}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.uri.IsValid(); got != c.want {
				t.Errorf("uri.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestSIP_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  *uri.SIP
	}{
		{"nil", (*uri.SIP)(nil)},
		{"zero", &uri.SIP{}},
		{
			"full",
			&uri.SIP{
				User:    uri.User("root"),
				Addr:    uri.HostPort("example.com", 5060),
				Params:  make(uri.Values).Set("transport", "udp"),
				Headers: make(uri.Values).Set("priority", "urgent"),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.uri.Clone()
			if c.uri == nil {
				if got != nil {
					t.Errorf("uri.Clone() = %+v, want nil", got)
				}
				return
			}
			if diff := cmp.Diff(got, c.uri, cmp.AllowUnexported(uri.SIP{})); diff != "" {
				t.Errorf("uri.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.uri, diff)
			}
		})
	}
}

func TestSIP_MarshalUnmarshalText_RoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		uri     *uri.SIP
		wantErr bool
	}{
		{"nil", (*uri.SIP)(nil), true},
		{"zero", &uri.SIP{}, true},
		{
			"secured",
			&uri.SIP{
				Secured: true,
				Addr:    uri.Host("example.com"),
				Params:  make(uri.Values).Set("transport", "tcp"),
			},
			false,
		},
		{
			"full",
			&uri.SIP{
				User: uri.UserPassword("root", "secret"),
				Addr: uri.HostPort("example.com", 5060),
				Params: make(uri.Values).
					Set("transport", "udp").
					Set("lr", ""),
				Headers: make(uri.Values).
					Append("Subject", "Hello").
					Append("Priority", "urgent"),
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			text, err := c.uri.MarshalText()
			if err != nil {
				t.Fatalf("uri.MarshalText() error = %v, want nil", err)
			}

			var got uri.SIP
			err = got.UnmarshalText(text)
			if c.wantErr {
				if err == nil {
					t.Fatalf("got.UnmarshalText(text) error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("got.UnmarshalText(text) error = %v, want nil", err)
			}

			if diff := cmp.Diff(&got, c.uri); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want %+v\ndiff (-got +want):\n%s", &got, c.uri, diff)
			}
		})
	}
}

func TestUser(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		user string
	}{
		{"empty", ""},
		{"username", "RooT;field1=1@23"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ui := uri.User(c.user)
			if got, want := ui.Username(), c.user; got != want {
				t.Errorf("ui.Username() = %q, want %q", got, want)
			}
			if got, ok := ui.Password(); ok || got != "" {
				t.Errorf("ui.Password() = (%q, %v), want (\"\", false)", got, ok)
			}
		})
	}
}

func TestUserPassword(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		user, passwd string
	}{
		{"empty", "", ""},
		{"empty password", "root", ""},
		{"username and password", "RooT;field1=1@23", "Qwerty!"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			ui := uri.UserPassword(c.user, c.passwd)
			if got := ui.Username(); got != c.user {
				t.Errorf("ui.Username() = %q, want %q", got, c.user)
			}

			if got, ok := ui.Password(); !ok || got != c.passwd {
				t.Errorf("ui.Password() = (%q, %v), want (%q, true)", got, ok, c.passwd)
			}
		})
	}
}

func TestUserInfo_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ui   uri.UserInfo
		want string
	}{
		{"empty", uri.UserInfo{}, ""},
		{"username", uri.User("root@;field1=1@23"), "root%40;field1=1%4023"},
		{"username with empty password", uri.UserPassword("root", ""), "root:"},
		{"empty username with password", uri.UserPassword("", "qwerty"), ":qwerty"},
		{"username and password", uri.UserPassword("root@;field1=1@23", "qwe@ "), "root%40;field1=1%4023:qwe%40%20"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.ui.String(); got != c.want {
				t.Errorf("ui.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestUserInfo_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ui   uri.UserInfo
		val  any
		want bool
	}{
		{"zero to nil", uri.UserInfo{}, nil, false},
		{"zero to nil pointer", uri.UserInfo{}, (*uri.UserInfo)(nil), false},
		{"zero to zero", uri.UserInfo{}, uri.UserInfo{}, true},
		{"zero to zero pointer", uri.UserInfo{}, &uri.UserInfo{}, true},
		{"zero to non-zero", uri.UserInfo{}, uri.User("root"), false},
		{
			"user and password match",
			uri.UserPassword("root", "qwerty"),
			uri.UserPassword("root", "qwerty"),
			true,
		},
		{
			"user and password not match 1",
			uri.UserPassword("root", "qwerty"),
			uri.UserPassword("ROOT", "qwerty"),
			false,
		},
		{
			"user and password not match 2",
			uri.UserPassword("root", "qwerty"),
			uri.UserPassword("root", "QWERTY"),
			false,
		},
		{
			"user and password not match 3",
			uri.User("root"),
			uri.UserPassword("root", "qwerty"),
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.ui.Equal(c.val); got != c.want {
				t.Errorf("ui.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestUserInfo_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ui   uri.UserInfo
		want bool
	}{
		{"zero", uri.UserInfo{}, false},
		{"user", uri.User("root"), true},
		{"user with password", uri.UserPassword("root", "qwerty"), true},
		{"user with empty password", uri.UserPassword("root", ""), true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.ui.IsValid(); got != c.want {
				t.Errorf("ui.IsValid() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestUserInfo_IsZero(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ui   uri.UserInfo
		want bool
	}{
		{"zero", uri.UserInfo{}, true},
		{"user", uri.User("root"), false},
		{"password", uri.UserPassword("", "qwerty"), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.ui.IsZero(); got != c.want {
				t.Errorf("ui.IsZero() = %v, want %v", got, c.want)
			}
		})
	}
}
