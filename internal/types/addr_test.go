package types_test

import (
	"net"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/types"
)

func TestMakeHostAddr(t *testing.T) {
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

			addr := types.MakeHostAddr(c.host)
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

func TestMakeHostPortAddr(t *testing.T) {
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

			addr := types.MakeHostPortAddr(c.host, c.port)
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
		{"empty host", types.MakeHostAddr(""), ""},
		{"empty host with port", types.MakeHostPortAddr("", 5060), ":5060"},
		{"space host with port", types.MakeHostPortAddr(" ", 5060), " :5060"},
		{"domain", types.MakeHostAddr("example.com"), "example.com"},
		{"domain with port", types.MakeHostPortAddr("example.com", 5060), "example.com:5060"},
		{"domain with zero port", types.MakeHostPortAddr("example.com", 0), "example.com:0"},
		{"IPv4", types.MakeHostAddr("192.168.0.1"), "192.168.0.1"},
		{"IPv4 with port", types.MakeHostPortAddr("192.168.0.1", 5060), "192.168.0.1:5060"},
		{"IPv6", types.MakeHostAddr("2001:db8::9:1"), "[2001:db8::9:1]"},
		{"IPv6 with port", types.MakeHostPortAddr("2001:db8::9:1", 5060), "[2001:db8::9:1]:5060"},
		{"IPv6 with zero port", types.MakeHostPortAddr("2001:db8::9:1", 0), "[2001:db8::9:1]:0"},
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
		{"", types.MakeHostAddr("example.com"), types.Addr{}, false},
		{"", types.MakeHostPortAddr("example.com", 0), types.MakeHostAddr("example.com"), false},
		{"", types.MakeHostPortAddr("example.com", 5060), types.MakeHostPortAddr("EXAMPLE.COM", 5060), true},
		{"", types.MakeHostPortAddr("192.0.2.128", 5060), types.MakeHostPortAddr("192.0.2.128", 5060), true},
		{
			"",
			types.MakeHostPortAddr("192.0.2.128", 5060),
			func() *types.Addr {
				addr := types.MakeHostPortAddr("192.0.2.128", 5060)
				return &addr
			}(),
			true,
		},
		{"", types.MakeHostPortAddr("192.0.2.128", 5060), types.MakeHostPortAddr("::ffff:192.0.2.128", 5060), true},
		{"", types.MakeHostPortAddr("2001:db8::9:1", 5060), types.MakeHostPortAddr("2001:db8::9:01", 5060), true},
		{"", types.MakeHostPortAddr("localhost", 5060), types.MakeHostPortAddr("127.0.0.1", 5060), false},
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
		{"empty host", types.MakeHostPortAddr("", 5060), false},
		{"host only", types.MakeHostAddr("example.com"), true},
		{"host with zero port", types.MakeHostPortAddr("example.com", 0), false},
		{"host with port", types.MakeHostPortAddr("example.com", 999), true},
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
		{"", types.MakeHostAddr(""), true},
		{"", types.MakeHostPortAddr("", 0), false},
		{"", types.MakeHostAddr("example.com"), false},
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
		{"", types.MakeHostPortAddr("", 5060)},
		{"", types.MakeHostAddr("example.com")},
		{"", types.MakeHostPortAddr("192.168.0.1", 555)},
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

func TestAddr_MarshalText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr types.Addr
		want []byte
	}{
		{"zero", types.Addr{}, []byte("")},
		{"zero host", types.MakeHostAddr(""), []byte("")},
		{"zero host port", types.MakeHostPortAddr("", 0), []byte(":0")},
		{"host", types.MakeHostAddr("example.com"), []byte("example.com")},
		{"host port", types.MakeHostPortAddr("example.com", 5060), []byte("example.com:5060")},
		{"ipv4", types.MakeHostPortAddr("192.168.0.1", 5060), []byte("192.168.0.1:5060")},
		{"ipv6", types.MakeHostAddr("2001:db8::9:1"), []byte("[2001:db8::9:1]")},
		{"ipv6 port", types.MakeHostPortAddr("2001:db8::9:1", 5060), []byte("[2001:db8::9:1]:5060")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := c.addr.MarshalText()
			if err != nil {
				t.Fatalf("addr.MarshalText() error = %v, want nil", err)
			}

			if diff := cmp.Diff(c.want, got); diff != "" {
				t.Errorf("addr.MarshalText() = %q, want %q\ndiff (-got +want):\n%s", got, c.want, diff)
			}
		})
	}
}

func TestAddr_UnmarshalText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		text    []byte
		want    types.Addr
		wantErr bool
	}{
		{"zero", []byte(""), types.Addr{}, false},
		{"zero host port", []byte(":0"), types.MakeHostPortAddr("", 0), false},
		{"host", []byte("example.com"), types.MakeHostAddr("example.com"), false},
		{"host port", []byte("example.com:5060"), types.MakeHostPortAddr("example.com", 5060), false},
		{"ipv4", []byte("192.168.0.1:5060"), types.MakeHostPortAddr("192.168.0.1", 5060), false},
		{"ipv6", []byte("[2001:db8::9:1]"), types.MakeHostAddr("2001:db8::9:1"), false},
		{"ipv6 port", []byte("[2001:db8::9:1]:5060"), types.MakeHostPortAddr("2001:db8::9:1", 5060), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got types.Addr

			err := got.UnmarshalText(c.text)
			if (err != nil) != c.wantErr {
				t.Fatalf("addr.UnmarshalText(%q) error = %v, wantErr %v", c.text, err, c.wantErr)
			}

			if diff := cmp.Diff(got, c.want, cmp.AllowUnexported(types.Addr{})); diff != "" {
				t.Errorf("addr.UnmarshalText(%q) = %+v, want %+v\ndiff (-got +want):\n%v", c.text, got, c.want, diff)
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
		{"zero", types.Addr{}},
		{"zero host", types.MakeHostAddr("")},
		{"zero host port", types.MakeHostPortAddr("", 0)},
		{"host", types.MakeHostAddr("example.com")},
		{"host port", types.MakeHostPortAddr("example.com", 5060)},
		{"ipv4", types.MakeHostPortAddr("192.168.0.1", 5060)},
		{"ipv6", types.MakeHostAddr("2001:db8::9:1")},
		{"ipv6 port", types.MakeHostPortAddr("2001:db8::9:1", 5060)},
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
