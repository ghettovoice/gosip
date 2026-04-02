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

type contextPacketConn struct {
	net.PacketConn
	wrMu, rdMu sync.Mutex
}

func NewContextPacketConn(conn net.PacketConn) net.PacketConn {
	if _, ok := AsPacketConn[ContextPacketConn](conn); ok {
		return conn
	}

	return &contextPacketConn{
		PacketConn: conn,
	}
}

func (c *contextPacketConn) WriteToContext(ctx context.Context, b []byte, addr net.Addr) (int, error) {
	c.wrMu.Lock()
	defer c.wrMu.Unlock()

	if d, ok := ctx.Deadline(); ok {
		if err := c.SetWriteDeadline(d); err != nil {
			return 0, errors.Wrap(err)
		}
		defer c.SetWriteDeadline(time.Time{}) //nolint:errcheck
	}

	return errors.Wrap2(c.WriteTo(b, addr))
}

func (c *contextPacketConn) ReadFromContext(ctx context.Context, b []byte) (int, net.Addr, error) {
	c.rdMu.Lock()
	defer c.rdMu.Unlock()

	if d, ok := ctx.Deadline(); ok {
		if err := c.SetReadDeadline(d); err != nil {
			return 0, nil, errors.Wrap(err)
		}
		defer c.SetReadDeadline(time.Time{}) //nolint:errcheck
	}

	return errors.Wrap3(c.ReadFrom(b))
}

type ContextConn interface {
	net.Conn
	WriteContext(ctx context.Context, b []byte) (int, error)
	ReadContext(ctx context.Context, b []byte) (int, error)
}

type contextConn struct {
	net.Conn
	wrMu, rdMu sync.Mutex
}

func NewContextConn(conn net.Conn) net.Conn {
	if _, ok := AsConn[ContextConn](conn); ok {
		return conn
	}

	return &contextConn{
		Conn: conn,
	}
}

func (c *contextConn) WriteContext(ctx context.Context, b []byte) (int, error) {
	c.wrMu.Lock()
	defer c.wrMu.Unlock()

	if d, ok := ctx.Deadline(); ok {
		if err := c.SetWriteDeadline(d); err != nil {
			return 0, errors.Wrap(err)
		}
		defer c.SetWriteDeadline(time.Time{}) //nolint:errcheck
	}

	return errors.Wrap2(c.Write(b))
}

func (c *contextConn) ReadContext(ctx context.Context, b []byte) (int, error) {
	c.rdMu.Lock()
	defer c.rdMu.Unlock()

	if d, ok := ctx.Deadline(); ok {
		if err := c.SetReadDeadline(d); err != nil {
			return 0, errors.Wrap(err)
		}
		defer c.SetReadDeadline(time.Time{}) //nolint:errcheck
	}

	return errors.Wrap2(c.Read(b))
}
