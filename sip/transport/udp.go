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
	UDPDefaultPort uint16 = 5060

	udpProto   sip.TransportProto = "UDP"
	udpNetwork string             = "udp"
)

// UDP implements the UDP SIP transport.
// TODO multicast listen/write support.
type UDP struct {
	unreliableBase
}

func NewUDP(opts *Options) *UDP {
	tp := new(UDP)
	tp.proto = udpProto
	tp.secured = false
	tp.listen, tp.dial = tp.listenUDP, tp.dialUDP
	if opts != nil {
		tp.opts = *opts
	}
	tp.opts.Log = tp.opts.log().With("transport", tp)
	return tp
}

func (tp *UDP) listenUDP(ctx context.Context, laddr netip.AddrPort, _ ...any) (net.PacketConn, error) {
	return tp.opts.netListenPacket(ctx, udpNetwork, laddr)
}

func (tp *UDP) dialUDP(ctx context.Context, laddr, _ netip.AddrPort, _ ...any) (net.PacketConn, error) {
	if !laddr.IsValid() {
		laddr = unspecAddrPort
	}
	return tp.opts.netListenPacket(ctx, udpNetwork, laddr)
}

func (tp *UDP) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", fmt.Sprintf("%T", tp)),
		slog.String("ptr", fmt.Sprintf("%p", tp)),
		slog.Any("proto", tp.proto),
	)
}

var udpMetadata = sip.TransportMetadata{
	Proto:       udpProto,
	Network:     udpNetwork,
	DefaultPort: UDPDefaultPort,
	IsReliable:  false,
	IsSecured:   false,
	Factory:     &Factory{Proto: udpProto},
}

func UDPMetadata() sip.TransportMetadata { return udpMetadata }
