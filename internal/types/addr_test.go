package types_test

import (
	"net"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/types"
)

func TestHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		host string
	}{
		{"empty", ""},
		{"domain", "ExAmplE.COM"},
		{"IPv4", "192.168.0.1"},
		{"IPv6", "2001:db8::9:1"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			addr := types.Host(c.host)
			if got, want := addr.Host(), c.host; got != want {
				t.Errorf("addr.Host() = %q, want %q", got, want)
			}
			if want := net.ParseIP(c.host); want != nil {
				if got := addr.IP(); !got.Equal(want) {
					t.Errorf("addr.IP() = %v, want %v", got, want)
				}
			}
			if got, ok := addr.Port(); ok {
				t.Errorf("addr.Port() = (%v, %v), want (0, false)", got, ok)
			}
		})
	}
}

func TestHostPort(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		host string
		port uint16
	}{
		{"empty", "", 0},
		{"domain", "example.com", 5060},
		{"IPv4", "192.168.0.1", 5060},
		{"IPv6", "2001:db8::9:1", 5060},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			addr := types.HostPort(c.host, c.port)
			if got, want := addr.Host(), c.host; got != want {
				t.Errorf("addr.Host() = %q, want %q", got, want)
			}
			if want := net.ParseIP(c.host); want != nil {
				if got := addr.IP(); !got.Equal(want) {
					t.Errorf("addr.IP() = %v, want %v", got, want)
				}
			}
			if got, ok := addr.Port(); !ok || got != c.port {
				t.Errorf("addr.Port() = (%v, %v), want (%v, true)", got, ok, c.port)
			}
		})
	}
}

func TestAddr_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr types.Addr
		want string
	}{
		{"zero", types.Addr{}, ""},
		{"empty host", types.Host(""), ""},
		{"empty host with port", types.HostPort("", 5060), ":5060"},
		{"space host with port", types.HostPort(" ", 5060), " :5060"},
		{"domain", types.Host("example.com"), "example.com"},
		{"domain with port", types.HostPort("example.com", 5060), "example.com:5060"},
		{"domain with zero port", types.HostPort("example.com", 0), "example.com:0"},
		{"IPv4", types.Host("192.168.0.1"), "192.168.0.1"},
		{"IPv4 with port", types.HostPort("192.168.0.1", 5060), "192.168.0.1:5060"},
		{"IPv6", types.Host("2001:db8::9:1"), "[2001:db8::9:1]"},
		{"IPv6 with port", types.HostPort("2001:db8::9:1", 5060), "[2001:db8::9:1]:5060"},
		{"IPv6 with zero port", types.HostPort("2001:db8::9:1", 0), "[2001:db8::9:1]:0"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got, want := c.addr.String(), c.want; got != want {
				t.Errorf("addr.String() = %q, want %q", got, want)
			}
		})
	}
}

func TestAddr_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr types.Addr
		val  any
		want bool
	}{
		{"", types.Addr{}, nil, false},
		{"", types.Addr{}, types.Addr{}, true},
		{"", types.Addr{}, (*types.Addr)(nil), false},
		{"", types.Host("example.com"), types.Addr{}, false},
		{"", types.HostPort("example.com", 0), types.Host("example.com"), false},
		{"", types.HostPort("example.com", 5060), types.HostPort("EXAMPLE.COM", 5060), true},
		{"", types.HostPort("192.0.2.128", 5060), types.HostPort("192.0.2.128", 5060), true},
		{
			"",
			types.HostPort("192.0.2.128", 5060),
			func() *types.Addr {
				addr := types.HostPort("192.0.2.128", 5060)
				return &addr
			}(),
			true,
		},
		{"", types.HostPort("192.0.2.128", 5060), types.HostPort("::ffff:192.0.2.128", 5060), true},
		{"", types.HostPort("2001:db8::9:1", 5060), types.HostPort("2001:db8::9:01", 5060), true},
		{"", types.HostPort("localhost", 5060), types.HostPort("127.0.0.1", 5060), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got, want := c.addr.Equal(c.val), c.want; got != want {
				t.Errorf("addr.Equal(val) = %v, want %v", got, want)
			}
		})
	}
}

func TestAddr_IsValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr types.Addr
		want bool
	}{
		{"zero", types.Addr{}, false},
		{"empty host", types.HostPort("", 5060), false},
		{"host only", types.Host("example.com"), true},
		{"host with zero port", types.HostPort("example.com", 0), true},
		{"host with port", types.HostPort("example.com", 999), true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got, want := c.addr.IsValid(), c.want; got != want {
				t.Errorf("addr.IsValid() = %v, want %v", got, want)
			}
		})
	}
}

func TestAddr_IsZero(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr types.Addr
		want bool
	}{
		{"", types.Addr{}, true},
		{"", types.Host(""), true},
		{"", types.HostPort("", 0), false},
		{"", types.Host("example.com"), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got, want := c.addr.IsZero(), c.want; got != want {
				t.Errorf("addr.IsZero() = %v, want %v", got, want)
			}
		})
	}
}

func TestAddr_Clone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr types.Addr
	}{
		{"", types.Addr{}},
		{"", types.HostPort("", 5060)},
		{"", types.Host("example.com")},
		{"", types.HostPort("192.168.0.1", 555)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := c.addr.Clone()
			if diff := cmp.Diff(got, c.addr, cmp.AllowUnexported(types.Addr{})); diff != "" {
				t.Errorf("addr.Clone() = %+v, want %+v\ndiff (-got +want):\n%v", got, c.addr, diff)
			}
		})
	}
}

func TestAddr_RoundTripText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr types.Addr
	}{
		{"host", types.Host("example.com")},
		{"host_port", types.HostPort("example.com", 5060)},
		{"ipv4", types.HostPort("192.168.0.1", 5060)},
		{"ipv6", types.Host("2001:db8::9:1")},
		{"ipv6_port", types.HostPort("2001:db8::9:1", 5060)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			text, err := c.addr.MarshalText()
			if err != nil {
				t.Fatalf("addr.MarshalText() error = %v, want nil", err)
			}

			var got types.Addr
			if err := got.UnmarshalText(text); err != nil {
				t.Fatalf("addr.UnmarshalText(text) error = %v, want nil", err)
			}

			if diff := cmp.Diff(got, c.addr, cmp.AllowUnexported(types.Addr{})); diff != "" {
				t.Errorf("round-trip mismatch: got = %+v, want = %+v\ndiff (-got +want):\n%v", got, c.addr, diff)
			}
		})
	}
}

func TestAddr_UnmarshalTextError(t *testing.T) {
	t.Parallel()

	var addr types.Addr
	if err := addr.UnmarshalText([]byte("://bad")); err == nil {
		t.Fatal("addr.UnmarshalText(\"://bad\") error = nil, want error")
	}

	if diff := cmp.Diff(addr, types.Addr{}, cmp.AllowUnexported(types.Addr{})); diff != "" {
		t.Errorf("addr.UnmarshalText(\"://bad\") wrote %+v, want types.Addr{}\ndiff (-got +want):\n%v", addr, diff)
	}
}
