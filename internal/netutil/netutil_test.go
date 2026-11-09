package netutil_test

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/netutil"
)

// Shared connection wrappers for unwrapping and decorator tests.
type testConnWrapper struct {
	net.Conn
	wrapped net.Conn
}

func (t *testConnWrapper) Unwrap() net.Conn {
	return t.wrapped
}

type testPktConnWrapper struct {
	net.PacketConn
	wrapped net.PacketConn
}

func (t *testPktConnWrapper) Unwrap() net.PacketConn {
	return t.wrapped
}

// Shared connection stubs.
type stubConn struct {
	net.Conn
	closed bool
}

func (m *stubConn) Close() error {
	m.closed = true
	return nil
}

func (*stubConn) Read(b []byte) (n int, err error) {
	return 0, nil
}

func (*stubConn) Write(b []byte) (n int, err error) {
	return len(b), nil
}

func (*stubConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
}

func (*stubConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8081}
}

func (*stubConn) SetDeadline(t time.Time) error {
	return nil
}

func (*stubConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (*stubConn) SetWriteDeadline(t time.Time) error {
	return nil
}

type stubPktConn struct {
	net.PacketConn
	closed bool
}

func (m *stubPktConn) Close() error {
	m.closed = true
	return nil
}

func (*stubPktConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	return 0, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}, nil
}

func (*stubPktConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return len(p), nil
}

func (*stubPktConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
}

func (*stubPktConn) SetDeadline(t time.Time) error {
	return nil
}

func (*stubPktConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (*stubPktConn) SetWriteDeadline(t time.Time) error {
	return nil
}

// ============================================================================
// Tests for As* functions (from netutil.go)
// ============================================================================

func TestAsConn(t *testing.T) {
	t.Parallel()

	t.Run("direct type match", func(t *testing.T) {
		t.Parallel()

		mock := &stubConn{}

		got, found := netutil.AsConn[*stubConn](mock)
		if !found {
			t.Errorf("netutil.AsConn[*stubConn](mock) found = false, want true")
		}
		if got != mock {
			t.Errorf("netutil.AsConn[*stubConn](mock) = %p, want %p", got, mock)
		}
	})

	t.Run("wrapped connection", func(t *testing.T) {
		t.Parallel()

		inner := &stubConn{}
		wrapped := &testConnWrapper{Conn: inner, wrapped: inner}

		got, found := netutil.AsConn[*stubConn](wrapped)
		if !found {
			t.Errorf("netutil.AsConn[*stubConn](wrapped) found = false, want true")
		}
		if got != inner {
			t.Errorf("netutil.AsConn[*stubConn](wrapped) = %p, want %p", got, inner)
		}
	})

	t.Run("multiple levels of wrapping", func(t *testing.T) {
		t.Parallel()

		deepInner := &stubConn{}
		middle := &testConnWrapper{Conn: deepInner, wrapped: deepInner}
		outer := &testConnWrapper{Conn: middle, wrapped: middle}

		got, found := netutil.AsConn[*stubConn](outer)
		if !found {
			t.Errorf("netutil.AsConn[*stubConn](outer) found = false, want true")
		}
		if got != deepInner {
			t.Errorf("netutil.AsConn[*stubConn](outer) = %p, want %p", got, deepInner)
		}
	})

	t.Run("non-existent type", func(t *testing.T) {
		t.Parallel()

		mock := &stubConn{}

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

		got, found := netutil.AsConn[*stubConn](nilConn)
		if found {
			t.Errorf("netutil.AsConn[*stubConn](nil) found = true, want false")
		}
		if got != nil {
			t.Errorf("netutil.AsConn[*stubConn](nil) = %v, want nil", got)
		}
	})

	t.Run("connection without Unwrap", func(t *testing.T) {
		t.Parallel()

		noUnwrap := &stubConn{}

		got, found := netutil.AsConn[*testConnWrapper](noUnwrap)
		if found {
			t.Errorf("netutil.AsConn[*testConnWrapper](noUnwrap) found = true, want false")
		}
		if got != nil {
			t.Errorf("netutil.AsConn[*testConnWrapper](noUnwrap) = %v, want nil", got)
		}
	})
}

func TestAsPacketConn(t *testing.T) {
	t.Parallel()

	t.Run("direct type match", func(t *testing.T) {
		t.Parallel()

		mock := &stubPktConn{}

		got, found := netutil.AsPacketConn[*stubPktConn](mock)
		if !found {
			t.Errorf("netutil.AsPacketConn[*stubPktConn](mock) found = false, want true")
		}
		if got != mock {
			t.Errorf("netutil.AsPacketConn[*stubPktConn](mock) = %p, want %p", got, mock)
		}
	})

	t.Run("wrapped connection", func(t *testing.T) {
		t.Parallel()

		inner := &stubPktConn{}
		wrapped := &testPktConnWrapper{PacketConn: inner, wrapped: inner}

		got, found := netutil.AsPacketConn[*stubPktConn](wrapped)
		if !found {
			t.Errorf("netutil.AsPacketConn[*stubPktConn](wrapped) found = false, want true")
		}
		if got != inner {
			t.Errorf("netutil.AsPacketConn[*stubPktConn](wrapped) = %p, want %p", got, inner)
		}
	})

	t.Run("multiple levels of wrapping", func(t *testing.T) {
		t.Parallel()

		deepInner := &stubPktConn{}
		middle := &testPktConnWrapper{PacketConn: deepInner, wrapped: deepInner}
		outer := &testPktConnWrapper{PacketConn: middle, wrapped: middle}

		got, found := netutil.AsPacketConn[*stubPktConn](outer)
		if !found {
			t.Errorf("netutil.AsPacketConn[*stubPktConn](outer) found = false, want true")
		}
		if got != deepInner {
			t.Errorf("netutil.AsPacketConn[*stubPktConn](outer) = %p, want %p", got, deepInner)
		}
	})

	t.Run("non-existent type", func(t *testing.T) {
		t.Parallel()

		mock := &stubPktConn{}

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

		got, found := netutil.AsPacketConn[*stubPktConn](nilConn)
		if found {
			t.Errorf("netutil.AsPacketConn[*stubPktConn](nil) found = true, want false")
		}
		if got != nil {
			t.Errorf("netutil.AsPacketConn[*stubPktConn](nil) = %v, want nil", got)
		}
	})

	t.Run("packet connection without Unwrap", func(t *testing.T) {
		t.Parallel()

		noUnwrap := &stubPktConn{}

		got, found := netutil.AsPacketConn[*testPktConnWrapper](noUnwrap)
		if found {
			t.Errorf("netutil.AsPacketConn[*testPktConnWrapper](noUnwrap) found = true, want false")
		}
		if got != nil {
			t.Errorf("netutil.AsPacketConn[*testPktConnWrapper](noUnwrap) = %v, want nil", got)
		}
	})
}

func TestAsConn_RealWrappers(t *testing.T) {
	t.Parallel()

	t.Run("find CloseOnceConn", func(t *testing.T) {
		t.Parallel()

		orig := &stubConn{}
		closeOnce := netutil.NewCloseOnceConn(orig)
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

		orig := &stubConn{}
		closeOnce := netutil.NewCloseOnceConn(orig)
		autoClose := netutil.NewAutoCloseConn(closeOnce, time.Minute)

		got, found := netutil.AsConn[*stubConn](autoClose)
		if !found {
			t.Errorf("netutil.AsConn[*stubConn](autoClose) found = false, want true")
		}
		if got != orig {
			t.Errorf("netutil.AsConn[*stubConn](autoClose) = %p, want %p", got, orig)
		}
	})
}

func TestAsPacketConn_RealWrappers(t *testing.T) {
	t.Parallel()

	t.Run("find CloseOncePacketConn", func(t *testing.T) {
		t.Parallel()

		orig := &stubPktConn{}
		ctxConn := netutil.NewContextPacketConn(orig)
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

		orig := &stubPktConn{}
		closeOnce := netutil.NewCloseOncePacketConn(orig)

		got, found := netutil.AsPacketConn[*stubPktConn](closeOnce)
		if !found {
			t.Errorf("netutil.AsPacketConn[*stubPktConn](closeOnce) found = false, want true")
		}
		if got != orig {
			t.Errorf("netutil.AsPacketConn[*stubPktConn](closeOnce) = %p, want %p", got, orig)
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
		t.Fatalf("ParseAddrPort() error = %+v, want nil", err)
	}

	t.Run("UDP network", func(t *testing.T) {
		t.Parallel()

		got := netutil.AddrPortToNetAddr("udp", addrPort)
		want := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
		if diff := cmp.Diff(got, want); diff != "" {
			t.Errorf("netutil.AddrPortToNetAddr(udp, addrPort) mismatch\ndiff (-got +want):\n%v", diff)
		}
	})

	t.Run("TCP network", func(t *testing.T) {
		t.Parallel()

		got := netutil.AddrPortToNetAddr("tcp", addrPort)
		want := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
		if diff := cmp.Diff(got, want); diff != "" {
			t.Errorf("netutil.AddrPortToNetAddr(tcp, addrPort) mismatch\ndiff (-got +want):\n%v", diff)
		}
	})

	t.Run("IP network", func(t *testing.T) {
		t.Parallel()

		got := netutil.AddrPortToNetAddr("ip", addrPort)
		want := &net.IPAddr{IP: net.IPv4(127, 0, 0, 1)}
		if diff := cmp.Diff(got, want); diff != "" {
			t.Errorf("netutil.AddrPortToNetAddr(ip, addrPort) mismatch\ndiff (-got +want):\n%v", diff)
		}
	})

	t.Run("unknown network", func(t *testing.T) {
		t.Parallel()

		got := netutil.AddrPortToNetAddr("unknown", addrPort)
		// netAddr is not exported, so we test via interface methods
		if got.Network() != "unknown" || got.String() != "127.0.0.1:8080" {
			t.Errorf(
				"netutil.AddrPortToNetAddr(unknown, addrPort) = %s://%s, want unknown://127.0.0.1:8080",
				got.Network(),
				got.String(),
			)
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
			t.Fatalf("netip.ParseAddrPort() error = %+v, want nil", err)
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
			t.Fatalf("netip.ParseAddrPort() error = %+v, want nil", err)
		}

		got := netutil.UnmapAddrPort(addrPort)
		want, err := netip.ParseAddrPort("127.0.0.1:8080")
		if err != nil {
			t.Fatalf("netip.ParseAddrPort() error = %+v, want nil", err)
		}
		if got != want {
			t.Errorf("netutil.UnmapAddrPort(IPv4-mapped IPv6) = %v, want %v", got, want)
		}
	})

	t.Run("IPv6 address", func(t *testing.T) {
		t.Parallel()

		addrPort, err := netip.ParseAddrPort("[::1]:8080")
		if err != nil {
			t.Fatalf("netip.ParseAddrPort() error = %v, want nil", err)
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
		name    string
		tpNet   string
		connNet string
		want    bool
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

			got := netutil.IsNetworkCompatible(tt.tpNet, tt.connNet)
			if got != tt.want {
				t.Errorf("netutil.IsNetworkCompatible(%q, %q) = %v, want %v",
					tt.tpNet,
					tt.connNet,
					got,
					tt.want,
				)
			}
		})
	}
}
