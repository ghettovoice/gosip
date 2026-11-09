package netutil

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
)

type ContextPacketConn interface {
	net.PacketConn
	WriteToContext(ctx context.Context, b []byte, addr net.Addr) (int, error)
	ReadFromContext(ctx context.Context, b []byte) (int, net.Addr, error)
}

type ctxPktConn struct {
	net.PacketConn
	wrMu, rdMu sync.Mutex
}

func NewContextPacketConn(conn net.PacketConn) net.PacketConn {
	if _, ok := AsPacketConn[ContextPacketConn](conn); ok {
		return conn
	}
	return &ctxPktConn{PacketConn: conn}
}

func (c *ctxPktConn) WriteToContext(ctx context.Context, b []byte, addr net.Addr) (int, error) {
	c.wrMu.Lock()
	defer c.wrMu.Unlock()

	if d, ok := ctx.Deadline(); ok {
		if err := c.SetWriteDeadline(d); err != nil {
			return 0, errors.Wrap(err)
		}
		defer c.SetWriteDeadline(time.Time{})
	}

	return errors.Wrap2(c.WriteTo(b, addr))
}

func (c *ctxPktConn) ReadFromContext(ctx context.Context, b []byte) (int, net.Addr, error) {
	c.rdMu.Lock()
	defer c.rdMu.Unlock()

	if d, ok := ctx.Deadline(); ok {
		if err := c.SetReadDeadline(d); err != nil {
			return 0, nil, errors.Wrap(err)
		}
		defer c.SetReadDeadline(time.Time{})
	}

	return errors.Wrap3(c.ReadFrom(b))
}

func (c *ctxPktConn) Unwrap() net.PacketConn {
	if c == nil {
		return nil
	}
	return c.PacketConn
}

type ContextConn interface {
	net.Conn
	WriteContext(ctx context.Context, b []byte) (int, error)
	ReadContext(ctx context.Context, b []byte) (int, error)
}

type ctxConn struct {
	net.Conn
	wrMu, rdMu sync.Mutex
}

func NewContextConn(conn net.Conn) net.Conn {
	if _, ok := AsConn[ContextConn](conn); ok {
		return conn
	}
	return &ctxConn{Conn: conn}
}

func (c *ctxConn) WriteContext(ctx context.Context, b []byte) (int, error) {
	c.wrMu.Lock()
	defer c.wrMu.Unlock()

	if d, ok := ctx.Deadline(); ok {
		if err := c.SetWriteDeadline(d); err != nil {
			return 0, errors.Wrap(err)
		}
		defer c.SetWriteDeadline(time.Time{})
	}

	return errors.Wrap2(c.Write(b))
}

func (c *ctxConn) ReadContext(ctx context.Context, b []byte) (int, error) {
	c.rdMu.Lock()
	defer c.rdMu.Unlock()

	if d, ok := ctx.Deadline(); ok {
		if err := c.SetReadDeadline(d); err != nil {
			return 0, errors.Wrap(err)
		}
		defer c.SetReadDeadline(time.Time{})
	}

	return errors.Wrap2(c.Read(b))
}

func (c *ctxConn) Unwrap() net.Conn {
	if c == nil {
		return nil
	}
	return c.Conn
}
