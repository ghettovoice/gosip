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
	if l == nil {
		return nil
	}

	l.closeOnce.Do(func() {
		if err := l.Listener.Close(); err != nil {
			l.closeErr = err
		}
	})

	return errors.Wrap(l.closeErr)
}

func (l *closeOnceListener) Unwrap() net.Listener {
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
	if c == nil {
		return nil
	}

	c.closeOnce.Do(func() {
		if err := c.Conn.Close(); err != nil {
			c.closeErr = err
		}
	})

	return errors.Wrap(c.closeErr)
}

func (c *closeOnceConn) Unwrap() net.Conn {
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
	if c == nil {
		return nil
	}

	c.closeOnce.Do(func() {
		if err := c.PacketConn.Close(); err != nil {
			c.closeErr = err
		}
	})

	return errors.Wrap(c.closeErr)
}

func (c *closeOncePacketConn) Unwrap() net.PacketConn {
	return c.PacketConn
}
