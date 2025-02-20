package transport

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/transport/internal/ws"
)

const (
	WSSDefaultPort uint16 = 443

	wssProto   sip.TransportProto = "WSS"
	wssNetwork string             = "tcp"
)

// WSS implements the secured WebSocket SIP transport.
type WSS struct {
	TLS
	wsDlr *ws.Dialer
}

func NewWSS(opts *Options) *WSS {
	tp := new(WSS)
	tp.proto = wssProto
	tp.streamed, tp.secured = true, true
	tp.listen, tp.dial = tp.listenWSS, tp.dialWSS
	if opts != nil {
		tp.opts = *opts
	}
	tp.opts.Log = tp.opts.log().With("transport", tp)
	tp.wsDlr = ws.NewDialer(tp.opts.WSConfig)
	return tp
}

func (tp *WSS) listenWSS(ctx context.Context, addr netip.AddrPort, opts ...any) (net.Listener, error) {
	ls, err := tp.listenTLS(ctx, addr, opts...)
	if err != nil {
		return nil, err
	}
	return ws.NewListener(ls, tp.opts.WSConfig), nil
}

func (tp *WSS) dialWSS(ctx context.Context, addr netip.AddrPort, opts ...any) (c net.Conn, err error) {
	c, err = tp.dialTLS(ctx, addr, opts...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			c.Close()
		}
	}()
	return tp.wsDlr.Upgrade(c, &url.URL{Scheme: "wss", Host: addr.String()})
}

func (tp *WSS) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", fmt.Sprintf("%T", tp)),
		slog.String("ptr", fmt.Sprintf("%p", tp)),
		slog.Any("proto", tp.proto),
	)
}

var wssMetadata = sip.TransportMetadata{
	Proto:      wssProto,
	Network:    wssNetwork,
	IsReliable: true,
	IsSecured:  true,
	Factory:    &Factory{Proto: wssProto},
}

func WSSMetadata() sip.TransportMetadata { return wssMetadata }
