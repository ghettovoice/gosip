package netutil

import (
	"net"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/timeutil"
)

type autoClosePacketConn struct {
	net.PacketConn
	tmr *timeutil.InactivityTimer
}

// NewAutoClosePacketConn wraps net.PacketConn and automatically closes it after a specified idle timeout.
// Any successful read or write operation resets the timer.
func NewAutoClosePacketConn(conn net.PacketConn, ttl time.Duration) net.PacketConn {
	if _, ok := AsPacketConn[*autoClosePacketConn](conn); ok {
		return conn
	}

	c := &autoClosePacketConn{PacketConn: conn}
	c.tmr = timeutil.NewInactivityTimer(ttl, func() { _ = c.Close() })
	c.resetTmr()
	return c
}

func (c *autoClosePacketConn) resetTmr() {
	if c.tmr != nil {
		c.tmr.Reset()
	}
}

func (c *autoClosePacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	n, err := c.PacketConn.WriteTo(p, addr)
	if err != nil {
		return n, errors.Wrap(err)
	}

	c.resetTmr()
	return n, nil
}

func (c *autoClosePacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, addr, err := c.PacketConn.ReadFrom(p)
	if err != nil {
		return n, addr, errors.Wrap(err)
	}

	c.resetTmr()
	return n, addr, nil
}

func (c *autoClosePacketConn) Close() error {
	if c.tmr != nil {
		c.tmr.Stop()
	}
	return errors.Wrap(c.PacketConn.Close())
}

func (c *autoClosePacketConn) Unwrap() net.PacketConn {
	if c == nil {
		return nil
	}
	return c.PacketConn
}

type autoCloseConn struct {
	net.Conn
	tmr *timeutil.InactivityTimer
}

// NewAutoCloseConn wraps net.Conn and automatically closes it after a specified idle timeout.
// Any successful read or write operation resets the timer.
func NewAutoCloseConn(conn net.Conn, ttl time.Duration) net.Conn {
	if _, ok := AsConn[*autoCloseConn](conn); ok {
		return conn
	}

	c := &autoCloseConn{Conn: conn}
	c.tmr = timeutil.NewInactivityTimer(ttl, func() { _ = c.Close() })
	c.resetTmr()
	return c
}

func (c *autoCloseConn) resetTmr() {
	if c.tmr != nil {
		c.tmr.Reset()
	}
}

func (c *autoCloseConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err != nil {
		return n, errors.Wrap(err)
	}

	c.resetTmr()
	return n, nil
}

func (c *autoCloseConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil {
		return n, errors.Wrap(err)
	}

	c.resetTmr()
	return n, nil
}

func (c *autoCloseConn) Close() error {
	if c.tmr != nil {
		c.tmr.Stop()
	}
	return errors.Wrap(c.Conn.Close())
}

func (c *autoCloseConn) Unwrap() net.Conn {
	if c == nil {
		return nil
	}
	return c.Conn
}
