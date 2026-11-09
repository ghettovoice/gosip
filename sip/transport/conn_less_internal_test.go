package transport

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
)

func TestConnLessListener_ReadWriteTimeouts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		readTO,
		writeTO,
		callerDeadline time.Duration
		effReadTO,
		effWriteTO time.Duration
		wantDL bool
	}{
		{"zero defaults", 0, 0, 0, time.Second, 10 * time.Second, true},
		{"positive", 2 * time.Second, 3 * time.Second, 0, 2 * time.Second, 3 * time.Second, true},
		{
			"positive caller deadline earlier",
			2 * time.Second,
			3 * time.Second,
			time.Second,
			2 * time.Second,
			3 * time.Second,
			true,
		},
		{
			"positive caller deadline later",
			2 * time.Second,
			3 * time.Second,
			time.Hour,
			2 * time.Second,
			3 * time.Second,
			true,
		},
		{"negative disabled", -time.Second, -time.Second, 0, 0, 0, false},
		{"negative caller deadline", -time.Second, -time.Second, time.Hour, 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				tp, err := NewConnLessTransport(sip.UDPMetadata(), TransportOptions{
					PublicAddr: sip.MakeIPAddr(net.IPv4(127, 0, 0, 1).To4()),
				})
				if err != nil {
					t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
				}
				t.Cleanup(func() { _ = tp.Close() })

				base := &stubDeadlinePktConn{
					laddr: &net.UDPAddr{IP: net.IPv4(11, 11, 11, 11), Port: 5070},
					raddr: &net.UDPAddr{IP: net.IPv4(55, 55, 55, 55), Port: 5060},
				}
				ls, err := NewConnLessListener(base, tp, ConnOptions{
					ReadTimeout:  tt.readTO,
					WriteTimeout: tt.writeTO,
				})
				if err != nil {
					t.Fatalf("transport.NewConnLessListener() error = %v, want nil", err)
				}
				t.Cleanup(func() { _ = ls.Close() })

				ctx := t.Context()
				var callerDL time.Time
				if tt.callerDeadline > 0 {
					callerDL = time.Now().Add(tt.callerDeadline)
					var cancel context.CancelFunc
					ctx, cancel = context.WithDeadline(ctx, callerDL)
					defer cancel()
				}

				buf := make([]byte, 64)
				raddr := netip.MustParseAddrPort("55.55.55.55:5060")
				before := time.Now()
				wantReadDL := before.Add(tt.effReadTO)
				if !callerDL.IsZero() {
					wantReadDL = callerDL
				}

				if n, _, err := ls.ReadFrom(ctx, buf); err != nil {
					t.Fatalf("ls.ReadFrom() error = %v, want nil", err)
				} else if n != len(buf) {
					t.Fatalf("ls.ReadFrom() = %d, want %d", n, len(buf))
				}
				checkDeadlineCalls(t, "read", base.readDL, wantReadDL, tt.wantDL)

				before = time.Now()
				wantWriteDL := before.Add(tt.effWriteTO)
				if !callerDL.IsZero() {
					wantWriteDL = callerDL
				}

				if n, err := ls.WriteTo(ctx, buf, raddr); err != nil {
					t.Fatalf("ls.WriteTo() error = %v, want nil", err)
				} else if n != len(buf) {
					t.Fatalf("ls.WriteTo() = %d, want %d", n, len(buf))
				}
				checkDeadlineCalls(t, "write", base.writeDL, wantWriteDL, tt.wantDL)
			})
		})
	}
}

// A stale closed record must not resolve an exact local-address lookup; it
// is dropped and a live sibling still works as fallback.
func TestConnLessTransport_AcquireConn_SkipsClosedListener(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	tp, err := NewConnLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = tp.Close() })

	lis, err := tp.Listen(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tp.Listen() error = %v, want nil", err)
	}

	stale := lis.(*trackedListener)
	laddr := lis.LocalAddr()

	// Close the underlying listener behind the wrapper's back, leaving a
	// stale closed record in the registry like Serve's cancellation path
	// could.
	if err := stale.transpListener.Close(); err != nil {
		t.Fatalf("underlying lis.Close() error = %v, want nil", err)
	}
	if _, ok := tp.lisMap.Load(laddr); !ok {
		t.Fatal("stale record unexpectedly removed, test premise broken")
	}

	raddr := netip.MustParseAddrPort("127.0.0.1:50999")
	if conn, err := tp.AcquireConn(
		ctx,
		raddr,
		AcquireConnOptions{LocalAddr: laddr},
	); !errors.Is(err, ErrConnNotFound) {
		t.Fatalf("tp.AcquireConn() = (%v, %v), want %v", conn, err, ErrConnNotFound)
	}
	if _, ok := tp.lisMap.Load(laddr); ok {
		t.Error("stale closed record still tracked after lookup, want dropped")
	}

	// A live sibling on the same address family is picked up by the generic
	// fallback instead.
	lis2, err := tp.Listen(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sibling tp.Listen() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = lis2.Close() })

	conn, err := tp.AcquireConn(
		ctx,
		raddr,
		AcquireConnOptions{LocalAddr: laddr},
	)
	if err != nil {
		t.Fatalf("tp.AcquireConn() with live sibling error = %v, want nil", err)
	}
	if got := conn.LocalAddr(); got != lis2.LocalAddr() {
		t.Errorf("acquired conn.LocalAddr() = %v, want sibling %v", got, lis2.LocalAddr())
	}
}

func TestConnLessTransport_AcquireConn_ServeContextListenerFallback(t *testing.T) {
	t.Parallel()

	tp, err := NewConnLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", errors.Wrap(err))
	}
	t.Cleanup(func() { _ = tp.Close() })

	lis, err := tp.Listen(t.Context(), "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tp.Listen() error = %v, want nil", errors.Wrap(err))
	}

	var factoryCalls atomic.Int32
	conn, err := tp.AcquireConn(
		t.Context(),
		netip.MustParseAddrPort("127.0.0.1:5098"),
		AcquireConnOptions{
			LocalAddr: lis.LocalAddr(),
			Dial:      true,
			ServeContext: func() context.Context {
				factoryCalls.Add(1)
				return nil
			},
		},
	)
	if err != nil {
		t.Fatalf("tp.AcquireConn() error = %v, want nil", errors.Wrap(err))
	}
	if conn.LocalAddr() != lis.LocalAddr() {
		t.Fatalf("tp.AcquireConn().LocalAddr() = %v, want listener address %v",
			conn.LocalAddr(), lis.LocalAddr())
	}
	if got := factoryCalls.Load(); got != 0 {
		t.Errorf("opts.ServeContext() factory calls for listener fallback = %d, want 0", got)
	}
}
