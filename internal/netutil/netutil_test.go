package netutil_test

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/netutil"
)

// Test types for As* functions.
type testConn struct {
	net.Conn
	wrapped net.Conn
}

func (t *testConn) Unwrap() net.Conn {
	return t.wrapped
}

type testPacketConn struct {
	net.PacketConn
	wrapped net.PacketConn
}

func (t *testPacketConn) Unwrap() net.PacketConn {
	return t.wrapped
}

// Mock implementations.
type mockConn struct {
	net.Conn
	closed bool
}

func (m *mockConn) Close() error {
	m.closed = true
	return nil
}

func (*mockConn) Read(b []byte) (n int, err error) {
	return 0, nil
}

func (*mockConn) Write(b []byte) (n int, err error) {
	return len(b), nil
}

func (*mockConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
}

func (*mockConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8081}
}

func (*mockConn) SetDeadline(t time.Time) error {
	return nil
}

func (*mockConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (*mockConn) SetWriteDeadline(t time.Time) error {
	return nil
}

type mockPacketConn struct {
	net.PacketConn
	closed bool
}

func (m *mockPacketConn) Close() error {
	m.closed = true
	return nil
}

func (*mockPacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	return 0, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}, nil
}

func (*mockPacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return len(p), nil
}

func (*mockPacketConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
}

func (*mockPacketConn) SetDeadline(t time.Time) error {
	return nil
}

func (*mockPacketConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (*mockPacketConn) SetWriteDeadline(t time.Time) error {
	return nil
}

// ============================================================================
// Tests for As* functions (from netutil.go)
// ============================================================================

func TestAsConn(t *testing.T) {
	t.Parallel()

	t.Run("direct type match", func(t *testing.T) {
		t.Parallel()

		mock := &mockConn{}

		got, found := netutil.AsConn[*mockConn](mock)
		if !found {
			t.Errorf("netutil.AsConn[*mockConn](mock) found = false, want true")
		}

		if got != mock {
			t.Errorf("netutil.AsConn[*mockConn](mock) = %p, want %p", got, mock)
		}
	})

	t.Run("wrapped connection", func(t *testing.T) {
		t.Parallel()

		inner := &mockConn{}
		wrapped := &testConn{Conn: inner, wrapped: inner}

		got, found := netutil.AsConn[*mockConn](wrapped)
		if !found {
			t.Errorf("netutil.AsConn[*mockConn](wrapped) found = false, want true")
		}

		if got != inner {
			t.Errorf("netutil.AsConn[*mockConn](wrapped) = %p, want %p", got, inner)
		}
	})

	t.Run("multiple levels of wrapping", func(t *testing.T) {
		t.Parallel()

		deepInner := &mockConn{}
		middle := &testConn{Conn: deepInner, wrapped: deepInner}
		outer := &testConn{Conn: middle, wrapped: middle}

		got, found := netutil.AsConn[*mockConn](outer)
		if !found {
			t.Errorf("netutil.AsConn[*mockConn](outer) found = false, want true")
		}

		if got != deepInner {
			t.Errorf("netutil.AsConn[*mockConn](outer) = %p, want %p", got, deepInner)
		}
	})

	t.Run("non-existent type", func(t *testing.T) {
		t.Parallel()

		mock := &mockConn{}

		got, found := netutil.AsConn[*net.TCPConn](mock)
		if found {
			t.Errorf("netutil.AsConn[*net.TCPConn](mock) found = true, want false")
		}

		if got != nil {
			t.Errorf("netutil.AsConn[*net.TCPConn](mock) = %v, want nil", got)
		}
	})

	t.Run("nil connection", func(t *testing.T) {
		t.Parallel()

		var nilConn net.Conn

		got, found := netutil.AsConn[*mockConn](nilConn)
		if found {
			t.Errorf("netutil.AsConn[*mockConn](nil) found = true, want false")
		}

		if got != nil {
			t.Errorf("netutil.AsConn[*mockConn](nil) = %v, want nil", got)
		}
	})

	t.Run("connection without Unwrap", func(t *testing.T) {
		t.Parallel()

		noUnwrap := &mockConn{}

		got, found := netutil.AsConn[*testConn](noUnwrap)
		if found {
			t.Errorf("netutil.AsConn[*testConn](noUnwrap) found = true, want false")
		}

		if got != nil {
			t.Errorf("netutil.AsConn[*testConn](noUnwrap) = %v, want nil", got)
		}
	})
}

func TestAsPacketConn(t *testing.T) {
	t.Parallel()

	t.Run("direct type match", func(t *testing.T) {
		t.Parallel()

		mock := &mockPacketConn{}

		got, found := netutil.AsPacketConn[*mockPacketConn](mock)
		if !found {
			t.Errorf("netutil.AsPacketConn[*mockPacketConn](mock) found = false, want true")
		}

		if got != mock {
			t.Errorf("netutil.AsPacketConn[*mockPacketConn](mock) = %p, want %p", got, mock)
		}
	})

	t.Run("wrapped connection", func(t *testing.T) {
		t.Parallel()

		inner := &mockPacketConn{}
		wrapped := &testPacketConn{PacketConn: inner, wrapped: inner}

		got, found := netutil.AsPacketConn[*mockPacketConn](wrapped)
		if !found {
			t.Errorf("netutil.AsPacketConn[*mockPacketConn](wrapped) found = false, want true")
		}

		if got != inner {
			t.Errorf("netutil.AsPacketConn[*mockPacketConn](wrapped) = %p, want %p", got, inner)
		}
	})

	t.Run("multiple levels of wrapping", func(t *testing.T) {
		t.Parallel()

		deepInner := &mockPacketConn{}
		middle := &testPacketConn{PacketConn: deepInner, wrapped: deepInner}
		outer := &testPacketConn{PacketConn: middle, wrapped: middle}

		got, found := netutil.AsPacketConn[*mockPacketConn](outer)
		if !found {
			t.Errorf("netutil.AsPacketConn[*mockPacketConn](outer) found = false, want true")
		}

		if got != deepInner {
			t.Errorf("netutil.AsPacketConn[*mockPacketConn](outer) = %p, want %p", got, deepInner)
		}
	})

	t.Run("non-existent type", func(t *testing.T) {
		t.Parallel()

		mock := &mockPacketConn{}

		got, found := netutil.AsPacketConn[*net.UDPConn](mock)
		if found {
			t.Errorf("netutil.AsPacketConn[*net.UDPConn](mock) found = true, want false")
		}

		if got != nil {
			t.Errorf("netutil.AsPacketConn[*net.UDPConn](mock) = %v, want nil", got)
		}
	})

	t.Run("nil packet connection", func(t *testing.T) {
		t.Parallel()

		var nilConn net.PacketConn

		got, found := netutil.AsPacketConn[*mockPacketConn](nilConn)
		if found {
			t.Errorf("netutil.AsPacketConn[*mockPacketConn](nil) found = true, want false")
		}

		if got != nil {
			t.Errorf("netutil.AsPacketConn[*mockPacketConn](nil) = %v, want nil", got)
		}
	})

	t.Run("packet connection without Unwrap", func(t *testing.T) {
		t.Parallel()

		noUnwrap := &mockPacketConn{}

		got, found := netutil.AsPacketConn[*testPacketConn](noUnwrap)
		if found {
			t.Errorf("netutil.AsPacketConn[*testPacketConn](noUnwrap) found = true, want false")
		}

		if got != nil {
			t.Errorf("netutil.AsPacketConn[*testPacketConn](noUnwrap) = %v, want nil", got)
		}
	})
}

func TestAsConn_RealWrappers(t *testing.T) {
	t.Parallel()

	t.Run("find CloseOnceConn", func(t *testing.T) {
		t.Parallel()

		original := &mockConn{}
		closeOnce := netutil.NewCloseOnceConn(original)
		ctxConn := netutil.NewContextConn(closeOnce)
		autoClose := netutil.NewAutoCloseConn(ctxConn, time.Minute)

		got, found := netutil.AsConn[netutil.ContextConn](autoClose)
		if !found {
			t.Errorf("netutil.AsConn[netutil.ContextConn](autoClose) found = false, want true")
		}

		if got != ctxConn {
			t.Errorf("netutil.AsConn[netutil.ContextConn](autoClose) = %p, want %p", got, ctxConn)
		}
	})

	t.Run("find original mock conn through wrappers", func(t *testing.T) {
		t.Parallel()

		original := &mockConn{}
		closeOnce := netutil.NewCloseOnceConn(original)
		autoClose := netutil.NewAutoCloseConn(closeOnce, time.Minute)

		got, found := netutil.AsConn[*mockConn](autoClose)
		if !found {
			t.Errorf("netutil.AsConn[*mockConn](autoClose) found = false, want true")
		}

		if got != original {
			t.Errorf("netutil.AsConn[*mockConn](autoClose) = %p, want %p", got, original)
		}
	})
}

func TestAsPacketConn_RealWrappers(t *testing.T) {
	t.Parallel()

	t.Run("find CloseOncePacketConn", func(t *testing.T) {
		t.Parallel()

		original := &mockPacketConn{}
		ctxConn := netutil.NewContextPacketConn(original)
		closeOnce := netutil.NewCloseOncePacketConn(ctxConn)

		got, found := netutil.AsPacketConn[netutil.ContextPacketConn](closeOnce)
		if !found {
			t.Errorf("netutil.AsPacketConn[netutil.ContextPacketConn](closeOnce) found = false, want true")
		}

		if got != ctxConn {
			t.Errorf("netutil.AsPacketConn[netutil.ContextPacketConn](closeOnce) = %p, want %p", got, ctxConn)
		}
	})

	t.Run("find original mock packet conn through wrapper", func(t *testing.T) {
		t.Parallel()

		original := &mockPacketConn{}
		closeOnce := netutil.NewCloseOncePacketConn(original)

		got, found := netutil.AsPacketConn[*mockPacketConn](closeOnce)
		if !found {
			t.Errorf("netutil.AsPacketConn[*mockPacketConn](closeOnce) found = false, want true")
		}

		if got != original {
			t.Errorf("netutil.AsPacketConn[*mockPacketConn](closeOnce) = %p, want %p", got, original)
		}
	})
}

// ============================================================================
// Tests for other functions (from netutil.go)
// ============================================================================

func TestAddrPortToNetAddr(t *testing.T) {
	t.Parallel()

	addrPort, err := netip.ParseAddrPort("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("ParseAddrPort() error = %v, want nil", err)
	}

	t.Run("UDP network", func(t *testing.T) {
		t.Parallel()

		got := netutil.AddrPortToNetAddr("udp", addrPort)

		want := &net.UDPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 8080,
		}
		if diff := cmp.Diff(got, want); diff != "" {
			t.Errorf("netutil.AddrPortToNetAddr(udp, addrPort) mismatch\ndiff (-got +want):\n%v", diff)
		}
	})

	t.Run("TCP network", func(t *testing.T) {
		t.Parallel()

		got := netutil.AddrPortToNetAddr("tcp", addrPort)

		want := &net.TCPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 8080,
		}
		if diff := cmp.Diff(got, want); diff != "" {
			t.Errorf("netutil.AddrPortToNetAddr(tcp, addrPort) mismatch\ndiff (-got +want):\n%v", diff)
		}
	})

	t.Run("IP network", func(t *testing.T) {
		t.Parallel()

		got := netutil.AddrPortToNetAddr("ip", addrPort)

		want := &net.IPAddr{
			IP: net.IPv4(127, 0, 0, 1),
		}
		if diff := cmp.Diff(got, want); diff != "" {
			t.Errorf("netutil.AddrPortToNetAddr(ip, addrPort) mismatch\ndiff (-got +want):\n%v", diff)
		}
	})

	t.Run("unknown network", func(t *testing.T) {
		t.Parallel()

		got := netutil.AddrPortToNetAddr("unknown", addrPort)
		// netAddr is not exported, so we test via interface methods
		if got.Network() != "unknown" || got.String() != "127.0.0.1:8080" {
			t.Errorf("netutil.AddrPortToNetAddr(unknown, addrPort) = %s://%s, want unknown://127.0.0.1:8080",
				got.Network(), got.String())
		}
	})

	t.Run("Unix network should panic", func(t *testing.T) {
		t.Parallel()

		defer func() {
			if r := recover(); r == nil {
				t.Errorf("netutil.AddrPortToNetAddr(unix, addrPort) should panic")
			}
		}()

		_ = netutil.AddrPortToNetAddr("unix", addrPort)
	})
}

func TestUnmapAddrPort(t *testing.T) {
	t.Parallel()

	t.Run("IPv4 address", func(t *testing.T) {
		t.Parallel()

		addrPort, err := netip.ParseAddrPort("127.0.0.1:8080")
		if err != nil {
			t.Fatalf("ParseAddrPort() error = %v, want nil", err)
		}

		got := netutil.UnmapAddrPort(addrPort)

		want := addrPort
		if got != want {
			t.Errorf("netutil.UnmapAddrPort(IPv4) = %v, want %v", got, want)
		}
	})

	t.Run("IPv4-mapped IPv6 address", func(t *testing.T) {
		t.Parallel()

		addrPort, err := netip.ParseAddrPort("[::ffff:127.0.0.1]:8080")
		if err != nil {
			t.Fatalf("ParseAddrPort() error = %v, want nil", err)
		}

		got := netutil.UnmapAddrPort(addrPort)

		want, err := netip.ParseAddrPort("127.0.0.1:8080")
		if err != nil {
			t.Fatalf("ParseAddrPort() error = %v, want nil", err)
		}

		if got != want {
			t.Errorf("netutil.UnmapAddrPort(IPv4-mapped IPv6) = %v, want %v", got, want)
		}
	})

	t.Run("IPv6 address", func(t *testing.T) {
		t.Parallel()

		addrPort, err := netip.ParseAddrPort("[::1]:8080")
		if err != nil {
			t.Fatalf("ParseAddrPort() error = %v, want nil", err)
		}

		got := netutil.UnmapAddrPort(addrPort)

		want := addrPort
		if got != want {
			t.Errorf("netutil.UnmapAddrPort(IPv6) = %v, want %v", got, want)
		}
	})
}

func TestIsNetworkCompatible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		transportNet string
		connNet      string
		want         bool
	}{
		// Exact matches
		{"udp-udp", "udp", "udp", true},
		{"udp4-udp4", "udp4", "udp4", true},
		{"udp6-udp6", "udp6", "udp6", true},
		{"tcp-tcp", "tcp", "tcp", true},
		{"tcp4-tcp4", "tcp4", "tcp4", true},
		{"tcp6-tcp6", "tcp6", "tcp6", true},

		// Generic transport with specific connections
		{"udp-udp4", "udp", "udp4", true},
		{"udp-udp6", "udp", "udp6", true},
		{"tcp-tcp4", "tcp", "tcp4", true},
		{"tcp-tcp6", "tcp", "tcp6", true},
		{"ip-ip4", "ip", "ip4", true},
		{"ip-ip6", "ip", "ip6", true},

		// Specific transport with generic connection
		{"udp4-udp", "udp4", "udp", true},
		{"udp6-udp", "udp6", "udp", true},
		{"tcp4-tcp", "tcp4", "tcp", true},
		{"tcp6-tcp", "tcp6", "tcp", true},
		{"ip4-ip", "ip4", "ip", true},
		{"ip6-ip", "ip6", "ip", true},

		// Incompatible combinations
		{"udp4-udp6", "udp4", "udp6", false},
		{"udp6-udp4", "udp6", "udp4", false},
		{"tcp4-tcp6", "tcp4", "tcp6", false},
		{"tcp6-tcp4", "tcp6", "tcp4", false},
		{"udp-tcp", "udp", "tcp", false},
		{"tcp-udp", "tcp", "udp", false},
		{"udp-unix", "udp", "unix", false},

		// Case insensitive
		{"UDP-udp4", "UDP", "udp4", true},
		{"UDP4-udp", "UDP4", "udp", true},
		{"Tcp-TCP6", "Tcp", "TCP6", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := netutil.IsNetworkCompatible(tt.transportNet, tt.connNet)
			if got != tt.want {
				t.Errorf("netutil.IsNetworkCompatible(%q, %q) = %v, want %v",
					tt.transportNet, tt.connNet, got, tt.want)
			}
		})
	}
}
