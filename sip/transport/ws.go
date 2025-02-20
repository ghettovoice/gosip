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
	WSDefaultPort uint16 = 80

	wsProto   sip.TransportProto = "WS"
	wsNetwork string             = "tcp"
)

// WS implements the WebSocket SIP transport.
// TODO websocket connection metadata: referer, upgrade headers and etc.
type WS struct {
	TCP
	wsDlr *ws.Dialer
}

func NewWS(opts *Options) *WS {
	tp := new(WS)
	tp.proto = wsProto
	tp.streamed, tp.secured = true, false
	tp.listen, tp.dial = tp.listenWS, tp.dialWS
	if opts != nil {
		tp.opts = *opts
	}
	tp.opts.Log = tp.opts.log().With("transport", tp)
	tp.wsDlr = ws.NewDialer(tp.opts.WSConfig)
	return tp
}

func (tp *WS) listenWS(ctx context.Context, addr netip.AddrPort, opts ...any) (net.Listener, error) {
	ls, err := tp.listenTCP(ctx, addr, opts...)
	if err != nil {
		return nil, err
	}
	return ws.NewListener(ls, tp.opts.WSConfig), nil
}

func (tp *WS) dialWS(ctx context.Context, addr netip.AddrPort, opts ...any) (c net.Conn, err error) {
	c, err = tp.dialTCP(ctx, addr, opts...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			c.Close()
		}
	}()
	return tp.wsDlr.Upgrade(c, &url.URL{Scheme: "ws", Host: addr.String()})
}

func (tp *WS) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", fmt.Sprintf("%T", tp)),
		slog.String("ptr", fmt.Sprintf("%p", tp)),
		slog.Any("proto", tp.proto),
	)
}

var wsMetadata = sip.TransportMetadata{
	Proto:      wsProto,
	Network:    wsNetwork,
	IsReliable: true,
	IsSecured:  false,
	Factory:    &Factory{Proto: wsProto},
}

func WSMetadata() sip.TransportMetadata { return wsMetadata }

type WSConfig = ws.Config
