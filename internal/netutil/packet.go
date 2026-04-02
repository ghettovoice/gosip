package netutil

import (
	"net"

	"github.com/ghettovoice/gosip/internal/errors"
)

type packetConnAdapter struct {
	net.Conn
}

func NewPacketConnAdapter(c net.Conn) net.Conn {
	if _, ok := AsConn[*packetConnAdapter](c); ok {
		return c
	}
	return &packetConnAdapter{Conn: c}
}

func (c *packetConnAdapter) ReadFrom(b []byte) (int, net.Addr, error) {
	switch conn := c.Conn.(type) {
	case net.PacketConn:
		return errors.Wrap3(conn.ReadFrom(b))
	default:
		n, err := conn.Read(b)
		return n, conn.RemoteAddr(), errors.Wrap(err)
	}
}

func (c *packetConnAdapter) WriteTo(b []byte, addr net.Addr) (int, error) {
	switch conn := c.Conn.(type) {
	case net.PacketConn:
		return errors.Wrap2(conn.WriteTo(b, addr))
	default:
		n, err := conn.Write(b)
		return n, errors.Wrap(err)
	}
}

func (c *packetConnAdapter) Unwrap() net.Conn {
	return c.Conn
}
