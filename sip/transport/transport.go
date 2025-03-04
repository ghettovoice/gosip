// Package transport provides a basic implementation of SIP transport.
//
// The following transports are supported:
//
// - [UDP]
// - [TCP]
// - [TLS]
// - [WS]
// - [WSS]
package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/ghettovoice/gosip/internal/iterutils"
	"github.com/ghettovoice/gosip/internal/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

var (
	ErrTransportClosed = errors.New("transport closed")

	noDeadline time.Time

	dfltNetLsCfg net.ListenConfig
	dfltNetDial  net.Dialer

	unspecAddrPort = netip.AddrPortFrom(netip.IPv4Unspecified(), 0)
)

// Options are used to configure SIP transport.
// The zero value is a valid configuration and provides default values for all fields.
type Options struct {
	Parser      sip.Parser
	Log         *slog.Logger
	SentByHost  string
	SentByBuild func(host string, listenPorts iter.Seq[uint16]) header.Addr
	// ConnIdleTTL is the maximum duration a connection may be idle before it is closed.
	// Idle timer resets every time a new message is received or sent successfully.
	// If the TTL is to -1, then no idle timer is used. Connections will stay open until transport shutdown.
	// Usually the value should be at least 3 minutes or Time C - the timeout of INVITE transaction on proxy.
	ConnIdleTTL time.Duration

	NetListen       NetListenFunc
	NetListenPacket NetListenPacketFunc
	NetDial         NetDialFunc
	NetResolver     *net.Resolver

	// TLSConfigSrv is a server-side TLS configuration.
	// Must be set for any secure transport such as TLS, WSS.
	TLSConfigSrv *tls.Config
	// TLSConfigCln is a client-side TLS configuration.
	// Must be set for any secure transport such as TLS, WSS.
	TLSConfigCln *tls.Config

	WSConfig *WSConfig
}

type NetListenFunc = func(ctx context.Context, network string, addr netip.AddrPort) (net.Listener, error)

type NetListenPacketFunc = func(ctx context.Context, network string, addr netip.AddrPort) (net.PacketConn, error)

type NetDialFunc = func(ctx context.Context, network string, addr netip.AddrPort) (net.Conn, error)

func (tp *Options) parser() sip.Parser {
	if tp.Parser == nil {
		return sip.DefaultParser()
	}
	return tp.Parser
}

func (tp *Options) sentByHost() string {
	if tp.SentByHost == "" {
		return "localhost"
	}
	return tp.SentByHost
}

func (tp *Options) sentByBuild(host string, listenPorts iter.Seq[uint16]) header.Addr {
	if tp.SentByBuild != nil {
		return tp.SentByBuild(host, listenPorts)
	}

	if port := iterutils.IterFirst(listenPorts); port > 0 {
		return header.HostPort(host, port)
	}
	return header.Host(host)
}

func (tp *Options) log() *slog.Logger {
	if tp.Log == nil {
		return log.Noop
	}
	return tp.Log
}

func (tp *Options) connIdleTTL() time.Duration {
	// if tp.ConnIdleTTL >= 0 && tp.ConnIdleTTL <= sip.TimeC() {
	// 	return sip.TimeC()
	// }
	return tp.ConnIdleTTL
}

func (tp *Options) netListen(ctx context.Context, network string, addr netip.AddrPort) (net.Listener, error) {
	if tp.NetListen != nil {
		return tp.NetListen(ctx, network, addr)
	}
	return netListen(ctx, network, addr)
}

func netListen(ctx context.Context, network string, addr netip.AddrPort) (net.Listener, error) {
	return dfltNetLsCfg.Listen(ctx, network, addr.String())
}

func (tp *Options) netListenPacket(ctx context.Context, network string, addr netip.AddrPort) (net.PacketConn, error) {
	if tp.NetListenPacket != nil {
		return tp.NetListenPacket(ctx, network, addr)
	}
	return netListenPacket(ctx, network, addr)
}

func netListenPacket(ctx context.Context, network string, addr netip.AddrPort) (net.PacketConn, error) {
	return dfltNetLsCfg.ListenPacket(ctx, network, addr.String())
}

func (tp *Options) netDial(ctx context.Context, network string, addr netip.AddrPort) (net.Conn, error) {
	if tp.NetDial != nil {
		return tp.NetDial(ctx, network, addr)
	}
	return netDial(ctx, network, addr)
}

func netDial(ctx context.Context, network string, addr netip.AddrPort) (net.Conn, error) {
	return dfltNetDial.DialContext(ctx, network, addr.String())
}

func (tp *Options) netResolver() *net.Resolver {
	if tp.NetResolver == nil {
		return net.DefaultResolver
	}
	return tp.NetResolver
}

type Factory struct {
	Proto sip.TransportProto
	*Options
}

func (f *Factory) NewTransport() (sip.Transport, error) {
	switch f.Proto.ToUpper() {
	case udpProto:
		return NewUDP(f.Options), nil
	case tcpProto:
		return NewTCP(f.Options), nil
	case tlsProto:
		return NewTLS(f.Options), nil
	case wsProto:
		return NewWS(f.Options), nil
	case wssProto:
		return NewWSS(f.Options), nil
	default:
		return nil, unknownProtoError(f.Proto)
	}
}

func unexpectConnTypeError(c any) error {
	return fmt.Errorf("unexpected connection type %T", c)
}

func invalidRemoteAddrError(addr netip.AddrPort) error {
	return fmt.Errorf("invalid remote address %q", addr)
}

func unknownProtoError(proto sip.TransportProto) error {
	return fmt.Errorf("unknown transport protocol %q", proto)
}

var errMissResWrt = errors.New("missing response writer")

func DefaultPort(p sip.TransportProto) uint16 {
	switch p.ToUpper() {
	case udpProto:
		return UDPDefaultPort
	case tcpProto:
		return TCPDefaultPort
	case tlsProto:
		return TLSDefaultPort
	case wsProto:
		return WSDefaultPort
	case wssProto:
		return WSSDefaultPort
	default:
		return 0
	}
}

func Network(p sip.TransportProto) string {
	switch p.ToUpper() {
	case tcpProto, tlsProto, wsProto, wssProto:
		return tcpNetwork
	case udpProto:
		return udpNetwork
	default:
		return ""
	}
}

func IsReliable(p sip.TransportProto) bool {
	switch p.ToUpper() {
	case tcpProto, tlsProto, wsProto, wssProto:
		return true
	default:
		return false
	}
}

func IsSecured(p sip.TransportProto) bool {
	switch p.ToUpper() {
	case tlsProto, wssProto:
		return true
	default:
		return false
	}
}

func IsStreamed(p sip.TransportProto) bool {
	switch p.ToUpper() {
	case tcpProto, tlsProto, wsProto, wssProto:
		return true
	default:
		return false
	}
}
