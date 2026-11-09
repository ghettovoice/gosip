package transport

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/timeutil"

	"github.com/ghettovoice/gosip/sip"
)

type stubIdleConn struct {
	net.Conn
	laddr, raddr net.Addr
	closes       atomic.Int32
}

func (c *stubIdleConn) Close() error {
	c.closes.Add(1)
	return nil
}

func (c *stubIdleConn) LocalAddr() net.Addr  { return c.laddr }
func (c *stubIdleConn) RemoteAddr() net.Addr { return c.raddr }

func TestConn_Close_DisposesIdleWatchdog(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		base := &stubIdleConn{
			laddr: &net.TCPAddr{IP: net.IPv4(11, 11, 11, 11), Port: 5070},
			raddr: &net.TCPAddr{IP: net.IPv4(55, 55, 55, 55), Port: 5060},
		}
		c, err := NewConn(base, sip.TCPMetadata(), ConnOptions{IdleTimeout: -time.Second})
		if err != nil {
			t.Fatalf("transport.NewConn() error = %v, want nil", err)
		}

		c.idleTmr.Close()
		var fired atomic.Int32
		wd := timeutil.NewWatchdog(time.Second, func() { fired.Add(1) })
		c.idleTmr = wd
		wd.Start()

		if err := c.Close(); err != nil {
			t.Fatalf("c.Close() error = %v, want nil", err)
		}
		c.resetIdleTmr()

		time.Sleep(2 * time.Second)
		if got := fired.Load(); got != 0 {
			t.Fatalf("idle watchdog fired %d times after close, want 0", got)
		}
	})
}

func TestConnOptions_Timeouts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts ConnOptions
		wantRead,
		wantWrite,
		wantIdle time.Duration
	}{
		{
			"zero defaults",
			ConnOptions{},
			time.Second,
			10 * time.Second,
			5*time.Minute + 32*time.Second,
		},
		{
			"positive",
			ConnOptions{
				ReadTimeout:  2 * time.Second,
				WriteTimeout: 3 * time.Second,
				IdleTimeout:  4 * time.Second,
			},
			2 * time.Second, 3 * time.Second,
			4 * time.Second,
		},
		{
			"negative",
			ConnOptions{
				ReadTimeout:  -time.Second,
				WriteTimeout: -time.Second,
				IdleTimeout:  -time.Second,
			},
			-time.Second,
			-time.Second,
			-time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.opts.readTimeout(); got != tt.wantRead {
				t.Errorf("opts.readTimeout() = %v, want %v", got, tt.wantRead)
			}
			if got := tt.opts.writeTimeout(); got != tt.wantWrite {
				t.Errorf("opts.writeTimeout() = %v, want %v", got, tt.wantWrite)
			}
			if got := tt.opts.idleTimeout(); got != tt.wantIdle {
				t.Errorf("opts.idleTimeout() = %v, want %v", got, tt.wantIdle)
			}
		})
	}
}

// stubDeadlineConn is a net.Conn test double that records deadline setter calls.
type stubDeadlineConn struct {
	laddr, raddr net.Addr
	readDL       []time.Time
	writeDL      []time.Time
}

func (*stubDeadlineConn) Read(b []byte) (int, error)  { return len(b), nil }
func (*stubDeadlineConn) Write(b []byte) (int, error) { return len(b), nil }
func (*stubDeadlineConn) Close() error                { return nil }
func (c *stubDeadlineConn) LocalAddr() net.Addr       { return c.laddr }
func (c *stubDeadlineConn) RemoteAddr() net.Addr      { return c.raddr }

func (c *stubDeadlineConn) SetDeadline(t time.Time) error {
	c.readDL = append(c.readDL, t)
	c.writeDL = append(c.writeDL, t)
	return nil
}

func (c *stubDeadlineConn) SetReadDeadline(t time.Time) error {
	c.readDL = append(c.readDL, t)
	return nil
}

func (c *stubDeadlineConn) SetWriteDeadline(t time.Time) error {
	c.writeDL = append(c.writeDL, t)
	return nil
}

// stubDeadlinePktConn is a net.PacketConn test double that records deadline
// setter calls.
type stubDeadlinePktConn struct {
	laddr, raddr net.Addr
	readDL       []time.Time
	writeDL      []time.Time
}

func (c *stubDeadlinePktConn) ReadFrom(b []byte) (int, net.Addr, error) {
	return len(b), c.raddr, nil
}

func (*stubDeadlinePktConn) WriteTo(b []byte, _ net.Addr) (int, error) { return len(b), nil }
func (*stubDeadlinePktConn) Close() error                              { return nil }
func (c *stubDeadlinePktConn) LocalAddr() net.Addr                     { return c.laddr }

func (c *stubDeadlinePktConn) SetDeadline(t time.Time) error {
	c.readDL = append(c.readDL, t)
	c.writeDL = append(c.writeDL, t)
	return nil
}

func (c *stubDeadlinePktConn) SetReadDeadline(t time.Time) error {
	c.readDL = append(c.readDL, t)
	return nil
}

func (c *stubDeadlinePktConn) SetWriteDeadline(t time.Time) error {
	c.writeDL = append(c.writeDL, t)
	return nil
}

// checkDeadlineCalls asserts the deadline setter call pattern recorded on a
// fake conn: when wantSet is false no calls are expected; otherwise exactly one
// set plus a zero-time reset. The set deadline must exactly match wantDeadline.
func checkDeadlineCalls(
	t *testing.T,
	op string,
	calls []time.Time,
	wantDeadline time.Time,
	wantSet bool,
) {
	t.Helper()

	if !wantSet {
		if len(calls) != 0 {
			t.Fatalf("%s deadline calls = %v, want none", op, calls)
		}
		return
	}
	if len(calls) != 2 {
		t.Fatalf("%s deadline calls = %v, want [set, zero reset]", op, calls)
	}
	if !calls[0].Equal(wantDeadline) {
		t.Fatalf("%s deadline = %v, want %v", op, calls[0], wantDeadline)
	}
	if !calls[1].IsZero() {
		t.Fatalf("%s deadline reset call = %v, want zero time", op, calls[1])
	}
}

func TestConn_ReadWriteTimeouts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		readTO,
		writeTO,
		callerDeadline time.Duration
		effReadTO,
		effWriteTO time.Duration
		wantDL bool // whether conn deadline setters are expected to be called
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
				base := &stubDeadlineConn{
					laddr: &net.TCPAddr{IP: net.IPv4(11, 11, 11, 11), Port: 5070},
					raddr: &net.TCPAddr{IP: net.IPv4(55, 55, 55, 55), Port: 5060},
				}
				c, err := NewConn(base, sip.TCPMetadata(), ConnOptions{
					ReadTimeout:  tt.readTO,
					WriteTimeout: tt.writeTO,
					IdleTimeout:  -time.Second,
				})
				if err != nil {
					t.Fatalf("transport.NewConn() error = %v, want nil", err)
				}
				t.Cleanup(func() { _ = c.Close() })

				ctx := t.Context()
				var callerDL time.Time
				if tt.callerDeadline > 0 {
					callerDL = time.Now().Add(tt.callerDeadline)
					var cancel context.CancelFunc
					ctx, cancel = context.WithDeadline(ctx, callerDL)
					defer cancel()
				}

				buf := make([]byte, 64)
				before := time.Now()
				wantReadDL := before.Add(tt.effReadTO)
				if !callerDL.IsZero() {
					wantReadDL = callerDL
				}

				if n, err := c.Read(ctx, buf); err != nil {
					t.Fatalf("c.Read() error = %v, want nil", err)
				} else if n != len(buf) {
					t.Fatalf("c.Read() = %d, want %d", n, len(buf))
				}
				checkDeadlineCalls(t, "read", base.readDL, wantReadDL, tt.wantDL)

				before = time.Now()
				wantWriteDL := before.Add(tt.effWriteTO)
				if !callerDL.IsZero() {
					wantWriteDL = callerDL
				}

				if n, err := c.Write(ctx, buf); err != nil {
					t.Fatalf("c.Write() error = %v, want nil", err)
				} else if n != len(buf) {
					t.Fatalf("c.Write() = %d, want %d", n, len(buf))
				}
				checkDeadlineCalls(t, "write", base.writeDL, wantWriteDL, tt.wantDL)
			})
		})
	}
}
