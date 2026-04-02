package sip_test

import (
	"net/netip"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip"
)

func TestFlowToken_Equal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		tkn  sip.FlowToken
		val  any
		want bool
	}{
		{"zero to nil", sip.FlowToken{}, nil, false},
		{"zero to nil ptr", sip.FlowToken{}, (*sip.FlowToken)(nil), false},
		{"zero to zero", sip.FlowToken{}, sip.FlowToken{}, true},
		{
			"zero to non-zero",
			sip.FlowToken{},
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			false,
		},
		{
			"non-zero to zero",
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			sip.FlowToken{},
			false,
		},
		{
			"different type",
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			"string",
			false,
		},
		{
			"different type int",
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			123,
			false,
		},
		{
			"match value",
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			true,
		},
		{
			"match ptr",
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			&sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			true,
		},
		{
			"nil ptr comparison",
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			(*sip.FlowToken)(nil),
			false,
		},
		{
			"different transport",
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			sip.FlowToken{
				Transport:  "TCP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			false,
		},
		{
			"different local addr",
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.101:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			false,
		},
		{
			"different local port",
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5061"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			false,
		},
		{
			"different remote addr",
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.201:5060"),
			},
			false,
		},
		{
			"different remote port",
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5061"),
			},
			false,
		},
		{
			"IPv6 mapped addr canonicalization",
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("[::ffff:192.168.1.100]:5060"),
				RemoteAddr: netip.MustParseAddrPort("[::ffff:192.168.1.200]:5060"),
			},
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			true,
		},
		{
			"IPv6 canonicalization",
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("[::1]:5060"),
				RemoteAddr: netip.MustParseAddrPort("[::2]:5060"),
			},
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("[::1]:5060"),
				RemoteAddr: netip.MustParseAddrPort("[::2]:5060"),
			},
			true,
		},
		{
			"IPv4 vs IPv6",
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("[::1]:5060"),
				RemoteAddr: netip.MustParseAddrPort("[::2]:5060"),
			},
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := c.tkn.Equal(c.val); got != c.want {
				t.Errorf("tkn.Equal(val) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestFlowToken_RoundTripText_Valid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		tkn  sip.FlowToken
	}{
		{
			"UDP token",
			sip.FlowToken{
				Transport:  "UDP",
				LocalAddr:  netip.MustParseAddrPort("192.168.1.100:5060"),
				RemoteAddr: netip.MustParseAddrPort("192.168.1.200:5060"),
			},
		},
		{
			"TCP token",
			sip.FlowToken{
				Transport:  "TCP",
				LocalAddr:  netip.MustParseAddrPort("[::1]:5060"),
				RemoteAddr: netip.MustParseAddrPort("[::2]:5060"),
			},
		},
		{
			"TLS token",
			sip.FlowToken{
				Transport:  "TLS",
				LocalAddr:  netip.MustParseAddrPort("10.0.0.1:5061"),
				RemoteAddr: netip.MustParseAddrPort("10.0.0.2:5061"),
			},
		},
		{
			"WS token",
			sip.FlowToken{
				Transport:  "WS",
				LocalAddr:  netip.MustParseAddrPort("172.16.0.1:8080"),
				RemoteAddr: netip.MustParseAddrPort("172.16.0.2:8080"),
			},
		},
		{
			"WSS token",
			sip.FlowToken{
				Transport:  "WSS",
				LocalAddr:  netip.MustParseAddrPort("172.16.0.1:8443"),
				RemoteAddr: netip.MustParseAddrPort("172.16.0.2:8443"),
			},
		},
		{
			"SCTP token",
			sip.FlowToken{
				Transport:  "SCTP",
				LocalAddr:  netip.MustParseAddrPort("203.0.113.1:5000"),
				RemoteAddr: netip.MustParseAddrPort("203.0.113.2:5000"),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			// MarshalText
			data, err := c.tkn.MarshalText()
			if err != nil {
				t.Fatalf("token.MarshalText() error = %v, want nil", err)
			}

			if len(data) == 0 {
				t.Fatalf("token.MarshalText() = %v, want non-empty data", data)
			}

			// UnmarshalText
			var parsed sip.FlowToken
			if err := parsed.UnmarshalText(data); err != nil {
				t.Fatalf("parsed.UnmarshalText() error = %v, want nil", err)
			}

			// Verify round-trip
			if diff := cmp.Diff(parsed, c.tkn, cmpopts.EquateComparable(netip.AddrPort{})); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want = %+v\ndiff (-got +want):\n%s", parsed, c.tkn, diff)
			}

			// Verify string representation
			str := c.tkn.String()
			if str == "" {
				t.Fatalf("token.String() = %q, want non-empty string", str)
			}

			// Verify string can be parsed back
			var parsed2 sip.FlowToken
			if err := parsed2.UnmarshalText([]byte(str)); err != nil {
				t.Fatalf("parsed.UnmarshalText(string) error = %v, want nil", err)
			}

			if diff := cmp.Diff(parsed2, c.tkn, cmpopts.EquateComparable(netip.AddrPort{})); diff != "" {
				t.Fatalf("round-trip mismatch: got = %+v, want = %+v\ndiff (-got +want):\n%s", parsed2, c.tkn, diff)
			}
		})
	}
}

func TestFlowToken_RoundTripText_Zero(t *testing.T) {
	t.Parallel()

	var zero sip.FlowToken

	// MarshalText should return nil for zero token
	data, err := zero.MarshalText()
	if err != nil {
		t.Fatalf("token.MarshalText() error = %v, want nil", err)
	}

	if len(data) != 0 {
		t.Fatalf("token.MarshalText() = %v, want empty data", data)
	}

	// UnmarshalText with empty data should produce zero token
	var parsed sip.FlowToken
	if err := parsed.UnmarshalText(nil); err != nil {
		t.Fatalf("parsed.UnmarshalText(nil) error = %v, want nil", err)
	}

	if diff := cmp.Diff(parsed, zero, cmpopts.EquateComparable(netip.AddrPort{})); diff != "" {
		t.Fatalf("parsed.UnmarshalText(nil) = %+v, want = %+v\ndiff (-got +want):\n%s", parsed, zero, diff)
	}

	if err := parsed.UnmarshalText([]byte{}); err != nil {
		t.Fatalf("parsed.UnmarshalText([]byte{}) error = %v, want nil", err)
	}

	if diff := cmp.Diff(parsed, zero, cmpopts.EquateComparable(netip.AddrPort{})); diff != "" {
		t.Fatalf("UnmarshalText([]byte{}) = %+v, want = %+v\ndiff (-got +want):\n%s", parsed, zero, diff)
	}
}

func TestFlowToken_RoundTripText_Invalid(t *testing.T) {
	t.Parallel()

	invalidTokens := []struct {
		name string
		data []byte
	}{
		{"invalid base64", []byte("!!!invalid!!!")},
		{"too short", []byte("YWJj")}, // "abc" in base64
		{"invalid HMAC", []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaa")},
		{"invalid payload", []byte("aaaaaaaaaaaaaaaaYWJjZGVmZ2g=")}, // valid HMAC prefix, invalid payload
	}

	for _, c := range invalidTokens {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var parsed sip.FlowToken
			if err := parsed.UnmarshalText(c.data); err == nil {
				t.Fatalf("parsed.UnmarshalText() error = nil, want error")
			}
		})
	}
}
