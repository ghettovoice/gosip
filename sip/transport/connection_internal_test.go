package transport

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/timeutil"

	"github.com/ghettovoice/gosip/sip"
)

type idleSeamConn struct {
	net.Conn
	laddr, raddr net.Addr
	closes       atomic.Int32
}

func (c *idleSeamConn) Close() error {
	c.closes.Add(1)
	return nil
}

func (c *idleSeamConn) LocalAddr() net.Addr  { return c.laddr }
func (c *idleSeamConn) RemoteAddr() net.Addr { return c.raddr }

func TestConnection_IdleWatchdogDisposedOnClose(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		base := &idleSeamConn{
			laddr: &net.TCPAddr{IP: net.IPv4(11, 11, 11, 11), Port: 5070},
			raddr: &net.TCPAddr{IP: net.IPv4(55, 55, 55, 55), Port: 5060},
		}
		c, err := NewConnection(base, sip.TCPMetadata(), ConnectionOptions{IdleTimeout: -time.Second})
		if err != nil {
			t.Fatalf("NewConnection() error = %v, want nil", err)
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

func TestConnectionOptions_Timeouts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                          string
		opts                          ConnectionOptions
		wantRead, wantWrite, wantIdle time.Duration
	}{
		{
			"zero_defaults",
			ConnectionOptions{},
			time.Second, 10 * time.Second, 5*time.Minute + 32*time.Second,
		},
		{"positive", ConnectionOptions{
			ReadTimeout: 2 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 4 * time.Second,
		}, 2 * time.Second, 3 * time.Second, 4 * time.Second},
		{"negative", ConnectionOptions{
			ReadTimeout: -time.Second, WriteTimeout: -time.Second, IdleTimeout: -time.Second,
		}, -time.Second, -time.Second, -time.Second},
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

// deadlineRecConn is a net.Conn test double that records deadline setter calls.
type deadlineRecConn struct {
	laddr, raddr net.Addr
	readDL       []time.Time
	writeDL      []time.Time
}

func (*deadlineRecConn) Read(b []byte) (int, error)  { return len(b), nil }
func (*deadlineRecConn) Write(b []byte) (int, error) { return len(b), nil }
func (*deadlineRecConn) Close() error                { return nil }
func (c *deadlineRecConn) LocalAddr() net.Addr       { return c.laddr }
func (c *deadlineRecConn) RemoteAddr() net.Addr      { return c.raddr }

func (c *deadlineRecConn) SetDeadline(t time.Time) error {
	c.readDL = append(c.readDL, t)
	c.writeDL = append(c.writeDL, t)
	return nil
}

func (c *deadlineRecConn) SetReadDeadline(t time.Time) error {
	c.readDL = append(c.readDL, t)
	return nil
}

func (c *deadlineRecConn) SetWriteDeadline(t time.Time) error {
	c.writeDL = append(c.writeDL, t)
	return nil
}

// deadlineRecPacketConn is a net.PacketConn test double that records deadline
// setter calls.
type deadlineRecPacketConn struct {
	laddr, raddr net.Addr
	readDL       []time.Time
	writeDL      []time.Time
}

func (c *deadlineRecPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	return len(b), c.raddr, nil
}

func (*deadlineRecPacketConn) WriteTo(b []byte, _ net.Addr) (int, error) { return len(b), nil }
func (*deadlineRecPacketConn) Close() error                              { return nil }
func (c *deadlineRecPacketConn) LocalAddr() net.Addr                     { return c.laddr }

func (c *deadlineRecPacketConn) SetDeadline(t time.Time) error {
	c.readDL = append(c.readDL, t)
	c.writeDL = append(c.writeDL, t)
	return nil
}

func (c *deadlineRecPacketConn) SetReadDeadline(t time.Time) error {
	c.readDL = append(c.readDL, t)
	return nil
}

func (c *deadlineRecPacketConn) SetWriteDeadline(t time.Time) error {
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

func TestConnection_ReadWriteTimeouts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                            string
		readTO, writeTO, callerDeadline time.Duration
		effReadTO, effWriteTO           time.Duration
		wantDL                          bool // whether conn deadline setters are expected to be called
	}{
		{"zero_defaults", 0, 0, 0, time.Second, 10 * time.Second, true},
		{"positive", 2 * time.Second, 3 * time.Second, 0, 2 * time.Second, 3 * time.Second, true},
		{"positive_caller_deadline_earlier", 2 * time.Second, 3 * time.Second, time.Second, 2 * time.Second, 3 * time.Second, true},
		{"positive_caller_deadline_later", 2 * time.Second, 3 * time.Second, time.Hour, 2 * time.Second, 3 * time.Second, true},
		{"negative_disabled", -time.Second, -time.Second, 0, 0, 0, false},
		{"negative_caller_deadline", -time.Second, -time.Second, time.Hour, 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				base := &deadlineRecConn{
					laddr: &net.TCPAddr{IP: net.IPv4(11, 11, 11, 11), Port: 5070},
					raddr: &net.TCPAddr{IP: net.IPv4(55, 55, 55, 55), Port: 5060},
				}
				c, err := NewConnection(base, sip.TCPMetadata(), ConnectionOptions{
					ReadTimeout:  tt.readTO,
					WriteTimeout: tt.writeTO,
					IdleTimeout:  -time.Second,
				})
				if err != nil {
					t.Fatalf("NewConnection() error = %v, want nil", err)
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

func TestConnectionLessListener_ReadWriteTimeouts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                            string
		readTO, writeTO, callerDeadline time.Duration
		effReadTO, effWriteTO           time.Duration
		wantDL                          bool
	}{
		{"zero_defaults", 0, 0, 0, time.Second, 10 * time.Second, true},
		{"positive", 2 * time.Second, 3 * time.Second, 0, 2 * time.Second, 3 * time.Second, true},
		{"positive_caller_deadline_earlier", 2 * time.Second, 3 * time.Second, time.Second, 2 * time.Second, 3 * time.Second, true},
		{"positive_caller_deadline_later", 2 * time.Second, 3 * time.Second, time.Hour, 2 * time.Second, 3 * time.Second, true},
		{"negative_disabled", -time.Second, -time.Second, 0, 0, 0, false},
		{"negative_caller_deadline", -time.Second, -time.Second, time.Hour, 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				tp, err := NewConnectionLessTransport(sip.UDPMetadata(), TransportOptions{
					PublicAddr: sip.MakeIPAddr(net.IPv4(127, 0, 0, 1).To4()),
				})
				if err != nil {
					t.Fatalf("NewConnectionLessTransport() error = %v, want nil", err)
				}
				t.Cleanup(func() { _ = tp.Close() })

				base := &deadlineRecPacketConn{
					laddr: &net.UDPAddr{IP: net.IPv4(11, 11, 11, 11), Port: 5070},
					raddr: &net.UDPAddr{IP: net.IPv4(55, 55, 55, 55), Port: 5060},
				}
				ls, err := NewConnectionLessListener(base, tp, ConnectionOptions{
					ReadTimeout:  tt.readTO,
					WriteTimeout: tt.writeTO,
				})
				if err != nil {
					t.Fatalf("NewConnectionLessListener() error = %v, want nil", err)
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
