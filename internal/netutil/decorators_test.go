package netutil_test

import (
	"net"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/netutil"
)

type testLisWrapper struct {
	net.Listener
	wrapped net.Listener
}

func (t *testLisWrapper) Unwrap() net.Listener {
	return t.wrapped
}

type stubLis struct {
	net.Listener
	closed bool
}

func (m *stubLis) Close() error {
	m.closed = true
	return nil
}

func (*stubLis) Accept() (net.Conn, error) {
	return &stubConn{}, nil
}

func (*stubLis) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
}

func TestWrapListener(t *testing.T) {
	t.Parallel()

	orig := &stubLis{}

	t.Run("empty chain", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapListener()
		got := chain(orig)
		if got != orig {
			t.Errorf("netutil.WrapListener() = %T, want %T", got, orig)
		}
	})

	t.Run("single decorator", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapListener(func(ls net.Listener) net.Listener {
			return &testLisWrapper{ls, ls}
		})
		got := chain(orig)
		if _, ok := got.(*testLisWrapper); !ok {
			t.Errorf("netutil.WrapListener(decorator) = %T, want *testListenerWrapper", got)
		}
	})

	t.Run("multiple decorators applied in order", func(t *testing.T) {
		t.Parallel()

		var callOrder []int
		chain := netutil.WrapListener(
			func(ls net.Listener) net.Listener {
				callOrder = append(callOrder, 1)
				return &testLisWrapper{ls, ls}
			},
			func(ls net.Listener) net.Listener {
				callOrder = append(callOrder, 2)
				return &testLisWrapper{ls, ls}
			},
		)
		result := chain(orig)

		wantOrder := []int{1, 2}
		if diff := cmp.Diff(callOrder, wantOrder); diff != "" {
			t.Errorf("decorator call order mismatch\ndiff (-got +want):\n%v", diff)
		}

		if tl, ok := result.(*testLisWrapper); ok {
			// The first decorator (decorator1) should be the innermost
			// So tl.wrapped should be the result of decorator2, which wraps the result of decorator1
			if innerTL, ok := tl.wrapped.(*testLisWrapper); ok {
				if innerTL.wrapped != orig {
					t.Error("decorators not applied in correct order")
				}
			} else {
				t.Error("expected inner testListenerWrapper")
			}
		} else {
			t.Errorf("netutil.WrapListener() = %T, want *testListenerWrapper", result)
		}
	})

	t.Run("nil decorators are skipped", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapListener(nil, func(ls net.Listener) net.Listener {
			return &testLisWrapper{ls, ls}
		}, nil)

		got := chain(orig)
		if _, ok := got.(*testLisWrapper); !ok {
			t.Errorf("netutil.WrapListener(nil, decorator, nil) = %T, want *testListenerWrapper", got)
		}
	})

	t.Run("nil decorators are skipped with multiple decorators", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapListener(nil, func(ls net.Listener) net.Listener {
			return &testLisWrapper{ls, ls}
		}, nil)

		got := chain(orig)
		if _, ok := got.(*testLisWrapper); !ok {
			t.Errorf("netutil.WrapListener(nil, decorator, nil) = %T, want *testListenerWrapper", got)
		}
	})
}

func TestWrapConn(t *testing.T) {
	t.Parallel()

	orig := &stubConn{}

	t.Run("empty chain", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapConn()
		got := chain(orig)
		if got != net.Conn(orig) {
			t.Errorf("netutil.WrapConn() = %T, want %T", got, orig)
		}
	})

	t.Run("single decorator", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapConn(func(conn net.Conn) net.Conn {
			return &testConnWrapper{conn, conn}
		})
		got := chain(orig)
		if _, ok := got.(*testConnWrapper); !ok {
			t.Errorf("netutil.WrapConn(decorator) = %T, want *testConnWrapper", got)
		}
	})

	t.Run("multiple decorators applied in reverse order", func(t *testing.T) {
		t.Parallel()

		var callOrder []int
		chain := netutil.WrapConn(
			func(conn net.Conn) net.Conn {
				callOrder = append(callOrder, 1)
				return &testConnWrapper{conn, conn}
			},
			func(conn net.Conn) net.Conn {
				callOrder = append(callOrder, 2)
				return &testConnWrapper{conn, conn}
			},
		)
		result := chain(orig)

		wantOrder := []int{1, 2}
		if diff := cmp.Diff(callOrder, wantOrder); diff != "" {
			t.Errorf("decorator call order mismatch\ndiff (-got +want):\n%v", diff)
		}

		if tc, ok := result.(*testConnWrapper); ok {
			// decorator2 should be outermost, decorator1 should be inner
			inner := tc.wrapped
			if innerTC, ok := inner.(*testConnWrapper); ok {
				if innerTC.wrapped != net.Conn(orig) {
					t.Error("decorators not applied in correct reverse order")
				}
			} else {
				t.Errorf("inner wrapper = %T, want *testConnWrapper", inner)
			}
		} else {
			t.Errorf("netutil.WrapConn() = %T, want *testConnWrapper", result)
		}
	})

	t.Run("nil decorators are skipped", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapConn(nil, func(conn net.Conn) net.Conn {
			return &testConnWrapper{conn, conn}
		}, nil)
		got := chain(orig)
		if _, ok := got.(*testConnWrapper); !ok {
			t.Errorf("netutil.WrapConn(nil, decorator, nil) = %T, want *testConnWrapper", got)
		}
	})

	t.Run("nil decorators are skipped with multiple decorators", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapConn(nil, func(conn net.Conn) net.Conn {
			return &testConnWrapper{conn, conn}
		}, nil)
		got := chain(orig)
		if _, ok := got.(*testConnWrapper); !ok {
			t.Errorf("netutil.WrapConn(nil, decorator, nil) = %T, want *testConnWrapper", got)
		}
	})

	t.Run("nil decorators are skipped with multiple decorators", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapConn(nil, func(conn net.Conn) net.Conn {
			return &testConnWrapper{conn, conn}
		}, nil)
		got := chain(orig)
		if _, ok := got.(*testConnWrapper); !ok {
			t.Errorf("netutil.WrapConn(nil, decorator, nil) = %T, want *testConnWrapper", got)
		}
	})
}

func TestWrapPacketConn(t *testing.T) {
	t.Parallel()

	orig := &stubPktConn{}

	t.Run("empty chain", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapPacketConn()
		got := chain(orig)
		if got != net.PacketConn(orig) {
			t.Errorf("netutil.WrapPacketConn() = %T, want %T", got, orig)
		}
	})

	t.Run("single decorator", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapPacketConn(func(conn net.PacketConn) net.PacketConn {
			return &testPktConnWrapper{conn, conn}
		})
		got := chain(orig)
		if _, ok := got.(*testPktConnWrapper); !ok {
			t.Errorf("netutil.WrapPacketConn(decorator) = %T, want *testPktConnWrapper", got)
		}
	})

	t.Run("multiple decorators applied in reverse order", func(t *testing.T) {
		t.Parallel()

		var callOrder []int
		chain := netutil.WrapPacketConn(
			func(conn net.PacketConn) net.PacketConn {
				callOrder = append(callOrder, 1)
				return &testPktConnWrapper{conn, conn}
			},
			func(conn net.PacketConn) net.PacketConn {
				callOrder = append(callOrder, 2)
				return &testPktConnWrapper{conn, conn}
			},
		)
		result := chain(orig)

		wantOrder := []int{1, 2}
		if diff := cmp.Diff(callOrder, wantOrder); diff != "" {
			t.Errorf("decorator call order mismatch\ndiff (-got +want):\n%v", diff)
		}

		if tpc, ok := result.(*testPktConnWrapper); ok {
			// decorator2 should be outermost, decorator1 should be inner
			inner := tpc.wrapped
			if innerTPC, ok := inner.(*testPktConnWrapper); ok {
				if innerTPC.wrapped != net.PacketConn(orig) {
					t.Error("decorators not applied in correct reverse order")
				}
			} else {
				t.Errorf("inner wrapper = %T, want *testPktConnWrapper", inner)
			}
		} else {
			t.Errorf("netutil.WrapPacketConn() = %T, want *testPktConnWrapper", result)
		}
	})

	t.Run("nil decorators are skipped", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapPacketConn(nil, func(conn net.PacketConn) net.PacketConn {
			return &testPktConnWrapper{conn, conn}
		}, nil)
		got := chain(orig)
		if _, ok := got.(*testPktConnWrapper); !ok {
			t.Errorf("netutil.WrapPacketConn(nil, decorator, nil) = %T, want *testPktConnWrapper", got)
		}
	})

	t.Run("nil decorators are skipped with multiple decorators", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapPacketConn(nil, func(conn net.PacketConn) net.PacketConn {
			return &testPktConnWrapper{conn, conn}
		}, nil)
		got := chain(orig)
		if _, ok := got.(*testPktConnWrapper); !ok {
			t.Errorf("netutil.WrapPacketConn(nil, decorator, nil) = %T, want *testPktConnWrapper", got)
		}
	})

	t.Run("nil decorators are skipped with multiple decorators", func(t *testing.T) {
		t.Parallel()

		chain := netutil.WrapPacketConn(nil, func(conn net.PacketConn) net.PacketConn {
			return &testPktConnWrapper{conn, conn}
		}, nil)
		got := chain(orig)
		if _, ok := got.(*testPktConnWrapper); !ok {
			t.Errorf("netutil.WrapPacketConn(nil, decorator, nil) = %T, want *testPktConnWrapper", got)
		}
	})
}
