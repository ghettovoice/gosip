package netutil_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/netutil"
)

type testListenerForDecorators struct {
	net.Listener
	wrapped net.Listener
}

func (t *testListenerForDecorators) Unwrap() net.Listener {
	return t.wrapped
}

type testConnForDecorators struct {
	net.Conn
	wrapped net.Conn
}

func (t *testConnForDecorators) Unwrap() net.Conn {
	return t.wrapped
}

type testPacketConnForDecorators struct {
	net.PacketConn
	wrapped net.PacketConn
}

func (t *testPacketConnForDecorators) Unwrap() net.PacketConn {
	return t.wrapped
}

type mockListenerForDecorators struct {
	net.Listener
	closed bool
}

func (m *mockListenerForDecorators) Close() error {
	m.closed = true
	return nil
}

func (*mockListenerForDecorators) Accept() (net.Conn, error) {
	return &mockConnForDecorators{}, nil
}

func (*mockListenerForDecorators) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
}

type mockConnForDecorators struct {
	net.Conn
	closed bool
}

func (m *mockConnForDecorators) Close() error {
	m.closed = true
	return nil
}

func (*mockConnForDecorators) Read(b []byte) (n int, err error) {
	return 0, nil
}

func (*mockConnForDecorators) Write(b []byte) (n int, err error) {
	return len(b), nil
}

func (*mockConnForDecorators) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
}

func (*mockConnForDecorators) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8081}
}

func (*mockConnForDecorators) SetDeadline(t time.Time) error {
	return nil
}

func (*mockConnForDecorators) SetReadDeadline(t time.Time) error {
	return nil
}

func (*mockConnForDecorators) SetWriteDeadline(t time.Time) error {
	return nil
}

type mockPacketConnForDecorators struct {
	net.PacketConn
	closed bool
}

func (m *mockPacketConnForDecorators) Close() error {
	m.closed = true
	return nil
}

func (*mockPacketConnForDecorators) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	return 0, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}, nil
}

func (*mockPacketConnForDecorators) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return len(p), nil
}

func (*mockPacketConnForDecorators) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
}

func (*mockPacketConnForDecorators) SetDeadline(t time.Time) error {
	return nil
}

func (*mockPacketConnForDecorators) SetReadDeadline(t time.Time) error {
	return nil
}

func (*mockPacketConnForDecorators) SetWriteDeadline(t time.Time) error {
	return nil
}

func TestWrapListener(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	original := &mockListenerForDecorators{}

	t.Run("empty chain", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapListener()

		got := chain(ctx, original)
		if got != original {
			t.Errorf("netutil.WrapListener() = %T, want %T", got, original)
		}
	})

	t.Run("single decorator", func(t *testing.T) {
		t.Parallel()

		decorator := func(ctx context.Context, ls net.Listener) net.Listener {
			return &testListenerForDecorators{Listener: ls, wrapped: ls}
		}
		chain := netutil.WrapListener(decorator)

		got := chain(ctx, original)
		if _, ok := got.(*testListenerForDecorators); !ok {
			t.Errorf("netutil.WrapListener(decorator) = %T, want *testListenerForDecorators", got)
		}
	})

	t.Run("multiple decorators applied in order", func(t *testing.T) {
		t.Parallel()

		var callOrder []int

		decorator1 := func(ctx context.Context, ls net.Listener) net.Listener {
			callOrder = append(callOrder, 1)
			return &testListenerForDecorators{Listener: ls, wrapped: ls}
		}
		decorator2 := func(ctx context.Context, ls net.Listener) net.Listener {
			callOrder = append(callOrder, 2)
			return &testListenerForDecorators{Listener: ls, wrapped: ls}
		}

		chain := netutil.WrapListener(decorator1, decorator2)
		result := chain(ctx, original)

		wantOrder := []int{2, 1}
		if diff := cmp.Diff(callOrder, wantOrder); diff != "" {
			t.Errorf("decorator call order mismatch\ndiff (-got +want):\n%v", diff)
		}

		if tl, ok := result.(*testListenerForDecorators); ok {
			// The first decorator (decorator1) should be the innermost
			// So tl.wrapped should be the result of decorator2, which wraps the result of decorator1
			if innerTL, ok := tl.wrapped.(*testListenerForDecorators); ok {
				if innerTL.wrapped != original {
					t.Error("decorators not applied in correct order")
				}
			} else {
				t.Error("expected inner testListenerForDecorators")
			}
		} else {
			t.Errorf("netutil.WrapListener() = %T, want *testListenerForDecorators", result)
		}
	})

	t.Run("nil decorators are skipped", func(t *testing.T) {
		t.Parallel()

		decorator := func(ctx context.Context, ls net.Listener) net.Listener {
			return &testListenerForDecorators{Listener: ls, wrapped: ls}
		}
		chain := netutil.WrapListener(nil, decorator, nil)

		got := chain(ctx, original)
		if _, ok := got.(*testListenerForDecorators); !ok {
			t.Errorf("netutil.WrapListener(nil, decorator, nil) = %T, want *testListenerForDecorators", got)
		}
	})

	t.Run("nil decorators are skipped with multiple decorators", func(t *testing.T) {
		t.Parallel()

		decorator := func(ctx context.Context, ls net.Listener) net.Listener {
			return &testListenerForDecorators{Listener: ls, wrapped: ls}
		}
		chain := netutil.WrapListener(nil, decorator, nil)

		got := chain(ctx, original)
		if _, ok := got.(*testListenerForDecorators); !ok {
			t.Errorf("netutil.WrapListener(nil, decorator, nil) = %T, want *testListenerForDecorators", got)
		}
	})
}

func TestWrapConn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	original := &mockConnForDecorators{}

	t.Run("empty chain", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapConn()

		got := chain(ctx, original)
		if got != net.Conn(original) {
			t.Errorf("netutil.WrapConn() = %T, want %T", got, original)
		}
	})

	t.Run("single decorator", func(t *testing.T) {
		t.Parallel()

		decorator := func(ctx context.Context, conn net.Conn) net.Conn {
			return &testConnForDecorators{Conn: conn, wrapped: conn}
		}
		chain := netutil.WrapConn(decorator)

		got := chain(ctx, original)
		if _, ok := got.(*testConnForDecorators); !ok {
			t.Errorf("netutil.WrapConn(decorator) = %T, want *testConnForDecorators", got)
		}
	})

	t.Run("multiple decorators applied in reverse order", func(t *testing.T) {
		t.Parallel()

		var callOrder []int

		decorator1 := func(ctx context.Context, conn net.Conn) net.Conn {
			callOrder = append(callOrder, 1)
			return &testConnForDecorators{Conn: conn, wrapped: conn}
		}
		decorator2 := func(ctx context.Context, conn net.Conn) net.Conn {
			callOrder = append(callOrder, 2)
			return &testConnForDecorators{Conn: conn, wrapped: conn}
		}

		chain := netutil.WrapConn(decorator1, decorator2)
		result := chain(ctx, original)

		wantOrder := []int{2, 1}
		if diff := cmp.Diff(callOrder, wantOrder); diff != "" {
			t.Errorf("decorator call order mismatch\ndiff (-got +want):\n%v", diff)
		}

		if tc, ok := result.(*testConnForDecorators); ok {
			// decorator2 should be outermost, decorator1 should be inner
			inner := tc.wrapped
			if innerTC, ok := inner.(*testConnForDecorators); ok {
				if innerTC.wrapped != net.Conn(original) {
					t.Error("decorators not applied in correct reverse order")
				}
			} else {
				t.Errorf("inner wrapper = %T, want *testConnForDecorators", inner)
			}
		} else {
			t.Errorf("netutil.WrapConn() = %T, want *testConnForDecorators", result)
		}
	})

	t.Run("nil decorators are skipped", func(t *testing.T) {
		t.Parallel()

		decorator := func(ctx context.Context, conn net.Conn) net.Conn {
			return &testConnForDecorators{Conn: conn, wrapped: conn}
		}
		chain := netutil.WrapConn(nil, decorator, nil)

		got := chain(ctx, original)
		if _, ok := got.(*testConnForDecorators); !ok {
			t.Errorf("netutil.WrapConn(nil, decorator, nil) = %T, want *testConnForDecorators", got)
		}
	})

	t.Run("nil decorators are skipped with multiple decorators", func(t *testing.T) {
		t.Parallel()

		decorator := func(ctx context.Context, conn net.Conn) net.Conn {
			return &testConnForDecorators{Conn: conn, wrapped: conn}
		}
		chain := netutil.WrapConn(nil, decorator, nil)

		got := chain(ctx, original)
		if _, ok := got.(*testConnForDecorators); !ok {
			t.Errorf("netutil.WrapConn(nil, decorator, nil) = %T, want *testConnForDecorators", got)
		}
	})

	t.Run("nil decorators are skipped with multiple decorators", func(t *testing.T) {
		t.Parallel()

		decorator := func(ctx context.Context, conn net.Conn) net.Conn {
			return &testConnForDecorators{Conn: conn, wrapped: conn}
		}
		chain := netutil.WrapConn(nil, decorator, nil)

		got := chain(ctx, original)
		if _, ok := got.(*testConnForDecorators); !ok {
			t.Errorf("netutil.WrapConn(nil, decorator, nil) = %T, want *testConnForDecorators", got)
		}
	})
}

func TestWrapPacketConn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	original := &mockPacketConnForDecorators{}

	t.Run("empty chain", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapPacketConn()

		got := chain(ctx, original)
		if got != net.PacketConn(original) {
			t.Errorf("netutil.WrapPacketConn() = %T, want %T", got, original)
		}
	})

	t.Run("single decorator", func(t *testing.T) {
		t.Parallel()

		decorator := func(ctx context.Context, conn net.PacketConn) net.PacketConn {
			return &testPacketConnForDecorators{PacketConn: conn, wrapped: conn}
		}
		chain := netutil.WrapPacketConn(decorator)

		got := chain(ctx, original)
		if _, ok := got.(*testPacketConnForDecorators); !ok {
			t.Errorf("netutil.WrapPacketConn(decorator) = %T, want *testPacketConnForDecorators", got)
		}
	})

	t.Run("multiple decorators applied in reverse order", func(t *testing.T) {
		t.Parallel()

		var callOrder []int

		decorator1 := func(ctx context.Context, conn net.PacketConn) net.PacketConn {
			callOrder = append(callOrder, 1)
			return &testPacketConnForDecorators{PacketConn: conn, wrapped: conn}
		}
		decorator2 := func(ctx context.Context, conn net.PacketConn) net.PacketConn {
			callOrder = append(callOrder, 2)
			return &testPacketConnForDecorators{PacketConn: conn, wrapped: conn}
		}

		chain := netutil.WrapPacketConn(decorator1, decorator2)
		result := chain(ctx, original)

		wantOrder := []int{2, 1}
		if diff := cmp.Diff(callOrder, wantOrder); diff != "" {
			t.Errorf("decorator call order mismatch\ndiff (-got +want):\n%v", diff)
		}

		if tpc, ok := result.(*testPacketConnForDecorators); ok {
			// decorator2 should be outermost, decorator1 should be inner
			inner := tpc.wrapped
			if innerTPC, ok := inner.(*testPacketConnForDecorators); ok {
				if innerTPC.wrapped != net.PacketConn(original) {
					t.Error("decorators not applied in correct reverse order")
				}
			} else {
				t.Errorf("inner wrapper = %T, want *testPacketConnForDecorators", inner)
			}
		} else {
			t.Errorf("netutil.WrapPacketConn() = %T, want *testPacketConnForDecorators", result)
		}
	})

	t.Run("nil decorators are skipped", func(t *testing.T) {
		t.Parallel()

		decorator := func(ctx context.Context, conn net.PacketConn) net.PacketConn {
			return &testPacketConnForDecorators{PacketConn: conn, wrapped: conn}
		}
		chain := netutil.WrapPacketConn(nil, decorator, nil)

		got := chain(ctx, original)
		if _, ok := got.(*testPacketConnForDecorators); !ok {
			t.Errorf("netutil.WrapPacketConn(nil, decorator, nil) = %T, want *testPacketConnForDecorators", got)
		}
	})

	t.Run("nil decorators are skipped with multiple decorators", func(t *testing.T) {
		t.Parallel()

		decorator := func(ctx context.Context, conn net.PacketConn) net.PacketConn {
			return &testPacketConnForDecorators{PacketConn: conn, wrapped: conn}
		}
		chain := netutil.WrapPacketConn(nil, decorator, nil)

		got := chain(ctx, original)
		if _, ok := got.(*testPacketConnForDecorators); !ok {
			t.Errorf("netutil.WrapPacketConn(nil, decorator, nil) = %T, want *testPacketConnForDecorators", got)
		}
	})

	t.Run("nil decorators are skipped with multiple decorators", func(t *testing.T) {
		t.Parallel()

		decorator := func(ctx context.Context, conn net.PacketConn) net.PacketConn {
			return &testPacketConnForDecorators{PacketConn: conn, wrapped: conn}
		}
		chain := netutil.WrapPacketConn(nil, decorator, nil)

		got := chain(ctx, original)
		if _, ok := got.(*testPacketConnForDecorators); !ok {
			t.Errorf("netutil.WrapPacketConn(nil, decorator, nil) = %T, want *testPacketConnForDecorators", got)
		}
	})
}
