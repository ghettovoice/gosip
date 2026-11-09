package netutil

import (
	"net"

	"github.com/ghettovoice/gosip/internal/errors"
)

type pktConnAdapter struct {
	net.Conn
}

func NewPacketConnAdapter(c net.Conn) net.Conn {
	if _, ok := AsConn[*pktConnAdapter](c); ok {
		return c
	}
	return &pktConnAdapter{Conn: c}
}

func (c *pktConnAdapter) ReadFrom(b []byte) (int, net.Addr, error) {
	switch conn := c.Conn.(type) {
	case net.PacketConn:
		return errors.Wrap3(conn.ReadFrom(b))
	default:
		n, err := conn.Read(b)
		return n, conn.RemoteAddr(), errors.Wrap(err)
	}
}

func (c *pktConnAdapter) WriteTo(b []byte, addr net.Addr) (int, error) {
	switch conn := c.Conn.(type) {
	case net.PacketConn:
		return errors.Wrap2(conn.WriteTo(b, addr))
	default:
		n, err := conn.Write(b)
		return n, errors.Wrap(err)
	}
}

func (c *pktConnAdapter) Unwrap() net.Conn {
	if c == nil {
		return nil
	}
	return c.Conn
}
