package netutil

import (
	"net"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
)

var zeroTime time.Time

type readDeadlinePacketConn struct {
	net.PacketConn
	timeout time.Duration
}

// NewReadDeadlinePacketConn returns a wrapper around [net.PacketConn] that sets a read deadline on the connection.
func NewReadDeadlinePacketConn(conn net.PacketConn, timeout time.Duration) net.PacketConn {
	if c, ok := AsPacketConn[*readDeadlinePacketConn](conn); ok {
		return c
	}
	return &readDeadlinePacketConn{PacketConn: conn, timeout: timeout}
}

func (c *readDeadlinePacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	if c.timeout > 0 {
		if err := c.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
			return 0, nil, errors.Wrap(err)
		}
		defer c.SetReadDeadline(zeroTime) //nolint:errcheck
	}

	return errors.Wrap3(c.PacketConn.ReadFrom(b))
}

func (c *readDeadlinePacketConn) Unwrap() net.PacketConn {
	return c.PacketConn
}

type writeDeadlinePacketConn struct {
	net.PacketConn
	timeout time.Duration
}

// NewWriteDeadlinePacketConn wraps [net.PacketConn] with a write deadline.
func NewWriteDeadlinePacketConn(conn net.PacketConn, timeout time.Duration) net.PacketConn {
	if c, ok := AsPacketConn[*writeDeadlinePacketConn](conn); ok {
		return c
	}
	return &writeDeadlinePacketConn{PacketConn: conn, timeout: timeout}
}

func (c *writeDeadlinePacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	if c.timeout > 0 {
		if err := c.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
			return 0, errors.Wrap(err)
		}
		defer c.SetWriteDeadline(zeroTime) //nolint:errcheck
	}

	return errors.Wrap2(c.PacketConn.WriteTo(b, addr))
}

func (c *writeDeadlinePacketConn) Unwrap() net.PacketConn {
	return c.PacketConn
}

type readDeadlineConn struct {
	net.Conn
	timeout time.Duration
}

func NewReadDeadlineConn(conn net.Conn, timeout time.Duration) net.Conn {
	if c, ok := AsConn[*readDeadlineConn](conn); ok {
		return c
	}
	return &readDeadlineConn{Conn: conn, timeout: timeout}
}

func (c *readDeadlineConn) Read(b []byte) (int, error) {
	if c.timeout > 0 {
		if err := c.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
			return 0, errors.Wrap(err)
		}
		defer c.SetReadDeadline(zeroTime) //nolint:errcheck
	}

	return errors.Wrap2(c.Conn.Read(b))
}

func (c *readDeadlineConn) Unwrap() net.Conn {
	return c.Conn
}

type writeDeadlineConn struct {
	net.Conn
	timeout time.Duration
}

func NewWriteDeadlineConn(conn net.Conn, timeout time.Duration) net.Conn {
	if c, ok := AsConn[*writeDeadlineConn](conn); ok {
		return c
	}
	return &writeDeadlineConn{Conn: conn, timeout: timeout}
}

func (c *writeDeadlineConn) Write(b []byte) (int, error) {
	if c.timeout > 0 {
		if err := c.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
			return 0, errors.Wrap(err)
		}
		defer c.SetWriteDeadline(zeroTime) //nolint:errcheck
	}

	return errors.Wrap2(c.Conn.Write(b))
}

func (c *writeDeadlineConn) Unwrap() net.Conn {
	return c.Conn
}
