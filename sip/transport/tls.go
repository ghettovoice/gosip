package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/netip"

	"github.com/ghettovoice/gosip/sip"
)

const (
	TLSDefaultPort uint16 = 5061

	tlsProto   sip.TransportProto = "TLS"
	tlsNetwork string             = "tcp"
)

// TLS implements the TLS SIP transport.
type TLS struct {
	TCP
}

func NewTLS(opts *Options) *TLS {
	tp := new(TLS)
	tp.proto = tlsProto
	tp.streamed, tp.secured = true, true
	tp.listen, tp.dial = tp.listenTLS, tp.dialTLS
	if opts != nil {
		tp.opts = *opts
	}
	tp.opts.Log = tp.opts.log().With("transport", tp)
	return tp
}

func (tp *TLS) listenTLS(ctx context.Context, addr netip.AddrPort, opts ...any) (net.Listener, error) {
	ls, err := tp.listenTCP(ctx, addr, opts...)
	if err != nil {
		return nil, err
	}
	return tls.NewListener(ls, tp.opts.TLSConfigSrv), nil
}

func (tp *TLS) dialTLS(ctx context.Context, addr netip.AddrPort, opts ...any) (net.Conn, error) {
	c, err := tp.dialTCP(ctx, addr, opts...)
	if err != nil {
		return nil, err
	}
	return tls.Client(c, tp.opts.TLSConfigCln), nil
}

func (tp *TLS) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", fmt.Sprintf("%T", tp)),
		slog.String("ptr", fmt.Sprintf("%p", tp)),
		slog.Any("proto", tp.proto),
	)
}

var tlsMetadata sip.TransportMetadata = sip.TransportMetadata{
	Proto:      tlsProto,
	IsReliable: true,
	IsSecured:  true,
	Factory:    &Factory{Proto: tlsProto},
}

func TLSMetadata() sip.TransportMetadata { return tlsMetadata }
