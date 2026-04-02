package netutil

import (
	"context"
	"net"
	"net/netip"

	"github.com/ghettovoice/gosip/internal/errors"
)

// STUNKeepAlivePacketConn is a wrapper around [net.PacketConn]
// that implements STUN keep-alive functionality described in RFC 5626.
type STUNKeepAlivePacketConn struct {
	net.PacketConn
}

func (c *STUNKeepAlivePacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	// TODO: read and detect STUN binding request
	// if it is a binding request (ping), send STUN binding response (pong)
	// otherwise, return the read data
	return errors.Wrap3(c.PacketConn.ReadFrom(p))
}

func (*STUNKeepAlivePacketConn) Ping(ctx context.Context, addr netip.AddrPort) error {
	// TODO: implement me
	panic("not implemented")
}

func (*STUNKeepAlivePacketConn) OnPing(fn func(ctx context.Context, addr netip.AddrPort)) (unbind func()) {
	// TODO: implement me
	panic("not implemented")
}

func (c *STUNKeepAlivePacketConn) Unwrap() net.PacketConn {
	return c.PacketConn
}
