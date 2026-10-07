package transport

import (
	"net"
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
		ctx := t.Context()
		base := &idleSeamConn{
			laddr: &net.TCPAddr{IP: net.IPv4(11, 11, 11, 11), Port: 5070},
			raddr: &net.TCPAddr{IP: net.IPv4(55, 55, 55, 55), Port: 5060},
		}
		c, err := NewConnection(ctx, base, sip.TCPMetadata(), ConnectionOptions{IdleTimeout: -time.Second})
		if err != nil {
			t.Fatalf("NewConnection() error = %v, want nil", err)
		}

		c.idleTmr.Close()
		var fired atomic.Int32
		wd := timeutil.NewWatchdog(time.Second, func() { fired.Add(1) })
		c.idleTmr = wd
		wd.Start()

		if err := c.Close(ctx); err != nil {
			t.Fatalf("c.Close() error = %v, want nil", err)
		}
		c.resetIdleTmr()

		time.Sleep(2 * time.Second)
		if got := fired.Load(); got != 0 {
			t.Fatalf("idle watchdog fired %d times after close, want 0", got)
		}
	})
}
