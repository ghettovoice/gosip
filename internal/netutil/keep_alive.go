package netutil

import (
	"context"
	"net"
	"net/netip"

	"github.com/ghettovoice/gosip/internal/errors"
)

// STUNKeepAlivePktConn is a wrapper around [net.PacketConn]
// that implements STUN keep-alive functionality described in RFC 5626.
type STUNKeepAlivePktConn struct {
	net.PacketConn
}

func (c *STUNKeepAlivePktConn) ReadFrom(p []byte) (int, net.Addr, error) {
	// TODO: read and detect STUN binding request
	// if it is a binding request (ping), send STUN binding response (pong)
	// otherwise, return the read data
	return errors.Wrap3(c.PacketConn.ReadFrom(p))
}

func (*STUNKeepAlivePktConn) Ping(ctx context.Context, addr netip.AddrPort) error {
	// TODO: implement me
	panic("not implemented")
}

func (*STUNKeepAlivePktConn) OnPing(fn func(ctx context.Context, addr netip.AddrPort)) (unbind func()) {
	// TODO: implement me
	panic("not implemented")
}

func (c *STUNKeepAlivePktConn) Unwrap() net.PacketConn {
	if c == nil {
		return nil
	}
	return c.PacketConn
}
