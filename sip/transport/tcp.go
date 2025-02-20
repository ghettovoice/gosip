package transport

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"

	"github.com/ghettovoice/gosip/sip"
)

const (
	TCPDefaultPort uint16 = 5060

	tcpProto   sip.TransportProto = "TCP"
	tcpNetwork string             = "tcp"
)

// TCP implements the TCP SIP transport.
type TCP struct {
	reliableBase
}

func NewTCP(opts *Options) *TCP {
	tp := new(TCP)
	tp.proto = tcpProto
	tp.streamed, tp.secured = true, false
	tp.listen, tp.dial = tp.listenTCP, tp.dialTCP
	if opts != nil {
		tp.opts = *opts
	}
	tp.opts.Log = tp.opts.log().With("transport", tp)
	return tp
}

func (tp *TCP) listenTCP(ctx context.Context, addr netip.AddrPort, _ ...any) (net.Listener, error) {
	return tp.opts.netListen(ctx, tcpNetwork, addr)
}

func (tp *TCP) dialTCP(ctx context.Context, addr netip.AddrPort, _ ...any) (net.Conn, error) {
	return tp.opts.netDial(ctx, tcpNetwork, addr)
}

func (tp *TCP) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", fmt.Sprintf("%T", tp)),
		slog.String("ptr", fmt.Sprintf("%p", tp)),
		slog.Any("proto", tp.proto),
	)
}

var tcpMetadata = sip.TransportMetadata{
	Proto:       tcpProto,
	Network:     tcpNetwork,
	DefaultPort: TCPDefaultPort,
	IsReliable:  true,
	IsSecured:   false,
	Factory:     &Factory{Proto: tcpProto},
}

func TCPMetadata() sip.TransportMetadata { return tcpMetadata }
