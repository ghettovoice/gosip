package sip

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/dns"
	"github.com/ghettovoice/gosip/log"
)

// UnreliableTransportOptions contains transport options.
type UnreliableTransportOptions struct {
	// DefaultPort is a default well-known port of the transport.
	// It is used to build remote addresses when no port is specified,
	// or during DNS lookup to resolve the message destination.
	// Default is 5060.
	DefaultPort uint16
	// Secured indicates whether the transport is secured.
	// Default is false.
	Secured bool
	// Parser is a parser used to parse inbound SIP messages.
	// If nil, [DefaultParser] is used.
	Parser Parser
	// SentByHost is a host used to build the Via's "sent-by" field.
	// If empty, "127.0.0.1" is used.
	SentByHost string
	// Log is a logger used to log transport events, warnings and errors.
	// If nil, [log.Default] is used.
	Log *slog.Logger
	// DNSResolver is a DNS resolver used to resolve the message destination.
	// If nil, [dns.DefaultResolver] is used.
	DNSResolver DNSResolver
}

func (o *UnreliableTransportOptions) defPort() uint16 {
	if o == nil || o.DefaultPort == 0 {
		return 5060
	}
	return o.DefaultPort
}

func (o *UnreliableTransportOptions) secured() bool {
	if o == nil {
		return false
	}
	return o.Secured
}

func (o *UnreliableTransportOptions) parser() Parser {
	if o == nil || o.Parser == nil {
		return DefaultParser()
	}
	return o.Parser
}

func (o *UnreliableTransportOptions) sentByHost() string {
	if o == nil || o.SentByHost == "" {
		return "127.0.0.1"
	}
	return o.SentByHost
}

func (o *UnreliableTransportOptions) log() *slog.Logger {
	if o == nil || o.Log == nil {
		return log.Default()
	}
	return o.Log
}

func (o *UnreliableTransportOptions) dnsResolver() DNSResolver {
	if o == nil || o.DNSResolver == nil {
		return dns.DefaultResolver()
	}
	return o.DNSResolver
}

// UnreliableTransport implements [Transport] interface based on an unreliable network protocol.
type UnreliableTransport struct {
	*baseTransp
	conn   net.PacketConn
	parser Parser
}

// NewUnreliableTransport creates a new [UnreliableTransport].
// Transport protocol and connection are required arguments.
// Options are optional, default options are used if nil.
func NewUnreliableTransport(
	proto TransportProto,
	conn net.PacketConn,
	opts *UnreliableTransportOptions,
) (*UnreliableTransport, error) {
	if !proto.IsValid() {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid protocol"))
	}
	if conn == nil {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid connection"))
	}

	tp := new(UnreliableTransport)
	tp.baseTransp = newBaseTransp(
		context.Background(),
		tp,
		TransportMetadata{
			Proto:       proto,
			Network:     conn.LocalAddr().Network(),
			Reliable:    false,
			Secured:     opts.secured(),
			Streamed:    false,
			DefaultPort: opts.defPort(),
		},
		netip.MustParseAddrPort(conn.LocalAddr().String()),
		opts.sentByHost(),
		opts.dnsResolver(),
		opts.log(),
	)
	tp.conn = newCloseOncePacketConn(newLogPacketConn(conn, tp.log))
	tp.parser = opts.parser()
	return tp, nil
}

func (tp *UnreliableTransport) close() error {
	return errtrace.Wrap(tp.conn.Close())
}

func (tp *UnreliableTransport) writeTo(
	ctx context.Context,
	buf *bytes.Buffer,
	raddr netip.AddrPort,
	_ *transpWriteOpts,
) (netip.AddrPort, error) {
	if d, ok := ctx.Deadline(); ok {
		if err := tp.conn.SetWriteDeadline(d); err != nil {
			return zeroAddrPort, errtrace.Wrap(err)
		}
		defer tp.conn.SetWriteDeadline(zeroTime)
	}
	if _, err := tp.conn.WriteTo(buf.Bytes(), addrPortToNetAddr(tp.meta.Network, raddr)); err != nil {
		return zeroAddrPort, errtrace.Wrap(err)
	}
	return tp.laddr, nil
}

func (tp *UnreliableTransport) serve() error {
	defer tp.conn.Close()

	tp.log.LogAttrs(tp.ctx, slog.LevelDebug, "begin serving the connection", slog.Any("connection", tp.conn))
	defer tp.log.LogAttrs(tp.ctx, slog.LevelDebug, "serving the connection finished", slog.Any("connection", tp.conn))

	err := tp.readMsgs(packetMsgs(tp.conn, tp.parser, time.Minute))
	select {
	case <-tp.ctx.Done():
		return errtrace.Wrap(ErrTransportClosed)
	default:
		return errtrace.Wrap(err)
	}
}
