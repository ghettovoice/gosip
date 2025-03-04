package uri_test

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/uri"
)

func TestParse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   any
		wantURI uri.URI
		wantErr error
	}{
		{"empty input", "", nil, grammar.ErrEmptyInput},

		{"any malformed", []byte{0x7f}, nil, grammar.ErrMalformedInput},
		{"any as path", "abc", &uri.Any{Path: "abc"}, nil},
		{"any as path with slashes", "/a/b/c", &uri.Any{Path: "/a/b/c"}, nil},
		{"any with scheme and host", "http://localhost", &uri.Any{Scheme: "http", Host: "localhost"}, nil},
		{
			"any with host and path",
			"http://localhost/abc",
			&uri.Any{Scheme: "http", Host: "localhost", Path: "/abc"},
			nil,
		},
		{
			"any as opaque",
			"urn:service:sos?a=1&b=2",
			&uri.Any{Scheme: "urn", Opaque: "service:sos", RawQuery: "a=1&b=2"},
			nil,
		},
		{"any as bytes", []byte("http://localhost"), &uri.Any{Scheme: "http", Host: "localhost"}, nil},

		{"tel", "tel:+1(22)333-44-55", &uri.Tel{Number: "+1(22)333-44-55"}, nil},
		{
			"tel with params",
			"tel:+1(22)333-44-55;ext=55;ISUB=qwe;Field1;field2=vAl%20%22",
			&uri.Tel{
				Number: "+1(22)333-44-55",
				Params: uri.Values{
					"ext":    []string{"55"},
					"isub":   []string{"qwe"},
					"field1": []string{""},
					"field2": []string{`vAl "`},
				},
			},
			nil,
		},
		{
			"tel with local number",
			"tel:1122;phone-context=+765;field1;isub=qwe;field2=v%40l;field1=abc",
			&uri.Tel{
				Number: "1122",
				Params: uri.Values{
					"phone-context": []string{"+765"},
					"field1":        []string{"", "abc"},
					"isub":          []string{"qwe"},
					"field2":        []string{"v@l"},
				},
			},
			nil,
		},
		{"tel with spaces", "tel:+1 (22) 333-44-55", nil, grammar.ErrMalformedInput},
		{"tel with invalid param", "tel:+1(22)333-44-55;fi%20ld=qwe", nil, grammar.ErrMalformedInput},

		{"sip with host only", "sip:EXAMPLE-abc.qwe.com", &uri.SIP{Addr: uri.Host("EXAMPLE-abc.qwe.com")}, nil},
		{"sips with host only", "sips:example.com", &uri.SIP{Secured: true, Addr: uri.Host("example.com")}, nil},
		{"sip with invalid host", "sip:example#.com", &uri.SIP{}, grammar.ErrMalformedInput},
		{"sip with host and port", "sip:example.com:5060", &uri.SIP{Addr: uri.HostPort("example.com", 5060)}, nil},
		{"sip with empty host", "sip::5060", nil, grammar.ErrMalformedInput},
		{"sip with IPv4 and port", "sip:127.0.0.1:5060", &uri.SIP{Addr: uri.HostPort("127.0.0.1", 5060)}, nil},
		{"sip with IPv6 and port", "sip:[2001:db8::9:1]:5060", &uri.SIP{Addr: uri.HostPort("2001:db8::9:1", 5060)}, nil},
		{"sip with missed port", "sip:127.0.0.1:?priority=urgent", nil, grammar.ErrMalformedInput},
		{
			"sip with user info",
			"sip:admin@example.com:5060",
			&uri.SIP{User: uri.User("admin"), Addr: uri.HostPort("example.com", 5060)},
			nil,
		},
		{
			"sip with user special chars",
			"sip:%40dmin@example.com",
			&uri.SIP{User: uri.User("@dmin"), Addr: uri.Host("example.com")},
			nil,
		},
		{
			"sip with user and empty password",
			"sip:admin:@example.com",
			&uri.SIP{User: uri.UserPassword("admin", ""), Addr: uri.Host("example.com")},
			nil,
		},
		{
			"sip with user and password",
			"sip:admin:qw3rty!+@example.com",
			&uri.SIP{User: uri.UserPassword("admin", "qw3rty!+"), Addr: uri.Host("example.com")},
			nil,
		},
		{
			"sip with user encoded params",
			"sip:admin;field=value@example.com",
			&uri.SIP{User: uri.User("admin;field=value"), Addr: uri.Host("example.com")},
			nil,
		},
		{"sip with invalid user", "sip::passwd@example.com", nil, grammar.ErrMalformedInput},
		{
			"sip with uri params",
			"sip:admin@example.com;Transport=TCP;user=any;method=INVITE;maddr=127.0.0.1;ttl=50;lr;foo=bar;method=refer;foo%3D=b%40r",
			&uri.SIP{
				User: uri.User("admin"),
				Addr: uri.Host("example.com"),
				Params: make(uri.Values).
					Append("transport", "TCP").
					Append("user", "any").
					Append("method", "INVITE").
					Append("maddr", "127.0.0.1").
					Append("ttl", "50").
					Append("lr", "").
					Append("foo", "bar").
					Append("method", "refer").
					Append("foo=", "b@r"),
			},
			nil,
		},
		{
			"sip with phone number",
			"sip:+1-222-333;field=qwerty@example.com;user=phone",
			&uri.SIP{
				User:   uri.User("+1-222-333;field=qwerty"),
				Addr:   uri.Host("example.com"),
				Params: make(uri.Values).Set("user", "phone"),
			},
			nil,
		},
		{"sip with invalid uri params", "sip:admin@example.com?=hello", nil, grammar.ErrMalformedInput},
		{
			"sip with headers",
			"sip:admin@example.com?subject=hello%20world&to=admin%40example.com&body=QWERTY&to=root%40example.org",
			&uri.SIP{
				User: uri.User("admin"),
				Addr: uri.Host("example.com"),
				Headers: make(uri.Values).
					Append("subject", "hello world").
					Append("to", "admin@example.com").
					Append("body", "QWERTY").
					Append("to", "root@example.org"),
			},
			nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var (
				got    uri.URI
				gotErr error
			)
			switch in := c.input.(type) {
			case string:
				got, gotErr = uri.Parse(in)
			case []byte:
				got, gotErr = uri.Parse(in)
			}
			if c.wantErr == nil {
				if gotErr != nil {
					t.Fatalf("sip.Parse(%q) error = %v, want nil", fmt.Sprintf("%v", c.input), gotErr)
				}
				if diff := cmp.Diff(got, c.wantURI); diff != "" {
					t.Errorf("uri.Parse(%q) = %+v, want %+v\ndiff (-got +want):\n%v",
						fmt.Sprintf("%v", c.input), got, c.wantURI, diff,
					)
				}
			} else {
				if diff := cmp.Diff(gotErr, c.wantErr, cmpopts.EquateErrors()); diff != "" {
					t.Errorf("uri.Parse(%q) error = %v, want %v\ndiff (-got +want):\n%v",
						fmt.Sprintf("%v", c.input), gotErr, c.wantErr, diff,
					)
				}
			}
		})
	}
}

func TestGetScheme(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  uri.URI
		want string
	}{
		{"nil", nil, ""},
		{"sip", &uri.SIP{}, "sip"},
		{"sips", &uri.SIP{Secured: true}, "sips"},
		{"tel", &uri.Tel{}, "tel"},
		{"any", &uri.Any{Scheme: "http"}, "http"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got, want := uri.GetScheme(c.uri), c.want; got != want {
				t.Errorf("uri.GetScheme(%+v) = %q, want %q", c.uri, got, want)
			}
		})
	}
}

func TestGetAddr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  uri.URI
		want string
	}{
		{"nil", nil, ""},
		{"sip", &uri.SIP{Addr: uri.HostPort("example.com", 5060)}, "example.com:5060"},
		{"tel", &uri.Tel{Number: "+123"}, "+123"},
		{"any", &uri.Any{Scheme: "http", Host: "example.com", Path: "/abc"}, "example.com/abc"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got, want := uri.GetAddr(c.uri), c.want; got != want {
				t.Errorf("uri.GetAddr(%+v) = %q, want %q", c.uri, got, want)
			}
		})
	}
}

func TestGetParams(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  uri.URI
		want uri.Values
	}{
		{"nil", nil, nil},
		{"sip", &uri.SIP{Params: make(uri.Values).Set("a", "1")}, make(uri.Values).Set("a", "1")},
		{"tel", &uri.Tel{Params: make(uri.Values).Set("a", "1")}, make(uri.Values).Set("a", "1")},
		{"any", &uri.Any{RawQuery: "a=1&b=2"}, make(uri.Values).Set("a", "1").Set("b", "2")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := uri.GetParams(c.uri)
			if diff := cmp.Diff(got, c.want); diff != "" {
				t.Errorf("uri.GetParams(%+v) = %+v, want %+v\ndiff (-got +want):\n%v", c.uri, got, c.want, diff)
			}
		})
	}
}
