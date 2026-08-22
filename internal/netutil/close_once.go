package netutil

import (
	"net"
	"sync"

	"github.com/ghettovoice/gosip/internal/errors"
)

type closeOnceListener struct {
	net.Listener
	closeOnce sync.Once
	closeErr  error
}

// NewCloseOnceListener wraps net.Listener and ensures it is closed only once.
func NewCloseOnceListener(ls net.Listener) net.Listener {
	if _, ok := AsListener[*closeOnceListener](ls); ok {
		return ls
	}
	return &closeOnceListener{Listener: ls}
}

func (l *closeOnceListener) Close() error {
	l.closeOnce.Do(func() { l.closeErr = l.Listener.Close() })
	return errors.Wrap(l.closeErr)
}

func (l *closeOnceListener) Unwrap() net.Listener {
	if l == nil {
		return nil
	}
	return l.Listener
}

type closeOnceConn struct {
	net.Conn
	closeOnce sync.Once
	closeErr  error
}

// NewCloseOnceConn wraps net.Conn and ensures it is closed only once.
func NewCloseOnceConn(c net.Conn) net.Conn {
	if _, ok := AsConn[*closeOnceConn](c); ok {
		return c
	}
	return &closeOnceConn{Conn: c}
}

func (c *closeOnceConn) Close() error {
	c.closeOnce.Do(func() { c.closeErr = c.Conn.Close() })
	return errors.Wrap(c.closeErr)
}

func (c *closeOnceConn) Unwrap() net.Conn {
	if c == nil {
		return nil
	}
	return c.Conn
}

type closeOncePacketConn struct {
	net.PacketConn
	closeOnce sync.Once
	closeErr  error
}

// NewCloseOncePacketConn wraps net.PacketConn and ensures it is closed only once.
func NewCloseOncePacketConn(c net.PacketConn) net.PacketConn {
	if _, ok := AsPacketConn[*closeOncePacketConn](c); ok {
		return c
	}
	return &closeOncePacketConn{PacketConn: c}
}

func (c *closeOncePacketConn) Close() error {
	c.closeOnce.Do(func() { c.closeErr = c.PacketConn.Close() })
	return errors.Wrap(c.closeErr)
}

func (c *closeOncePacketConn) Unwrap() net.PacketConn {
	if c == nil {
		return nil
	}
	return c.PacketConn
}
