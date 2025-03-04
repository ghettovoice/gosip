package sip

import (
	"bytes"
	"context"
	"errors"
	"iter"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/dns"
	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/log"
)

// ReliableTransportOptions contains transport options.
type ReliableTransportOptions struct {
	// DefaultPort is a default well-known port of the transport.
	// It is used to build remote addresses when no port is specified,
	// or during DNS lookup to resolve the message destination.
	// Default is 5060.
	DefaultPort uint16
	// Secured indicates whether the transport is secured.
	// Default is false.
	Secured bool
	// Streamed indicates whether the transport reads messages as a stream or as packets.
	// Set to true for protocols like TCP, false for framed protocols like SCTP.
	// Default is false.
	Streamed bool
	// Parser is a parser used to parse inbound SIP messages.
	// If nil, [DefaultParser] is used.
	Parser Parser
	// SentByHost is a host used to build the Via's "sent-by" field.
	// If empty, "127.0.0.1" is used.
	SentByHost string
	// ConnIdleTTL is the maximum duration a reliable connection may be idle before it is closed.
	// Idle timer resets every time a new message is received or sent.
	// If the TTL is set to -1, then no idle timer is used, connections will stay open until transport shutdown.
	// If the TTL is set to 0, then the value of defTimingCfg.TimeC() is used, which is 5m by default.
	ConnIdleTTL time.Duration
	// ConnDialer is a connection dialer used to dial connections for reliable transports.
	// If nil, [DefaultConnDialer] is used.
	ConnDialer ConnDialer
	// Log is a logger used to log transport events, warnings and errors.
	// If nil, [log.Default] is used.
	Log *slog.Logger
	// DNSResolver is a DNS resolver used to resolve the message destination.
	// If nil, [dns.DefaultResolver] is used.
	DNSResolver DNSResolver
}

func (o *ReliableTransportOptions) defPort() uint16 {
	if o == nil || o.DefaultPort == 0 {
		return 5060
	}
	return o.DefaultPort
}

func (o *ReliableTransportOptions) secured() bool {
	if o == nil {
		return false
	}
	return o.Secured
}

func (o *ReliableTransportOptions) streamed() bool {
	if o == nil {
		return false
	}
	return o.Streamed
}

func (o *ReliableTransportOptions) parser() Parser {
	if o == nil || o.Parser == nil {
		return DefaultParser()
	}
	return o.Parser
}

func (o *ReliableTransportOptions) sentByHost() string {
	if o == nil || o.SentByHost == "" {
		return "127.0.0.1"
	}
	return o.SentByHost
}

func (o *ReliableTransportOptions) connIdleTTL() time.Duration {
	if o == nil || o.ConnIdleTTL == 0 {
		return defTimingCfg.TimeC()
	}
	return o.ConnIdleTTL
}

func (o *ReliableTransportOptions) connDialer() ConnDialer {
	if o == nil || o.ConnDialer == nil {
		return DefaultConnDialer()
	}
	return o.ConnDialer
}

func (o *ReliableTransportOptions) log() *slog.Logger {
	if o == nil || o.Log == nil {
		return log.Default()
	}
	return o.Log
}

func (o *ReliableTransportOptions) dnsResolver() DNSResolver {
	if o == nil || o.DNSResolver == nil {
		return dns.DefaultResolver()
	}
	return o.DNSResolver
}

// ReliableTransport implements [Transport] interface based on a reliable network protocol.
type ReliableTransport struct {
	*baseTransp
	lsnr        net.Listener
	parser      Parser
	connDialer  ConnDialer
	connIdleTTL time.Duration
	connTracker
	connSrvWg sync.WaitGroup
}

// NewReliableTransport creates a new [ReliableTransport].
// Transport protocol and listener are required arguments.
// Options are optional, default options are used if nil.
func NewReliableTransport(
	proto TransportProto,
	ls net.Listener,
	opts *ReliableTransportOptions,
) (*ReliableTransport, error) {
	if !proto.IsValid() {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid protocol"))
	}
	if ls == nil {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid listener"))
	}

	tp := new(ReliableTransport)
	tp.baseTransp = newBaseTransp(
		context.Background(),
		tp,
		TransportMetadata{
			Proto:       proto,
			Network:     ls.Addr().Network(),
			Reliable:    true,
			Secured:     opts.secured(),
			Streamed:    opts.streamed(),
			DefaultPort: opts.defPort(),
		},
		netip.MustParseAddrPort(ls.Addr().String()),
		opts.sentByHost(),
		opts.dnsResolver(),
		opts.log(),
	)
	tp.lsnr = newCloseOnceListener(newLogListener(ls, tp.log))
	tp.parser = opts.parser()
	tp.connDialer = opts.connDialer()
	tp.connIdleTTL = opts.connIdleTTL()
	return tp, nil
}

func (tp *ReliableTransport) close() error {
	err := tp.lsnr.Close()
	for c := range tp.allConns() {
		c.Close()
	}
	tp.connSrvWg.Wait()
	return errtrace.Wrap(err)
}

const errNoConn Error = "no connection found"

func (tp *ReliableTransport) writeTo(
	ctx context.Context,
	buf *bytes.Buffer,
	raddr netip.AddrPort,
	opts *transpWriteOpts,
) (netip.AddrPort, error) {
	var conn net.Conn
	if opts != nil && opts.noDialConn {
		var ok bool
		conn, ok = tp.getConn(raddr)
		if !ok {
			return zeroAddrPort, errtrace.Wrap(errNoConn)
		}
	} else {
		var err error
		conn, err = tp.getOrDialConn(raddr, func(raddr netip.AddrPort) (net.Conn, error) {
			c, e := tp.connDialer.DialConn(ctx, tp.meta.Network, raddr)
			if e != nil {
				return nil, errtrace.Wrap(e)
			}
			return tp.initConn(c), nil
		})
		if err != nil {
			return zeroAddrPort, errtrace.Wrap(err)
		}
	}

	if d, ok := ctx.Deadline(); ok {
		if err := conn.SetWriteDeadline(d); err != nil {
			return zeroAddrPort, errtrace.Wrap(err)
		}
		defer conn.SetWriteDeadline(zeroTime)
	}
	if _, err := conn.Write(buf.Bytes()); err != nil {
		return zeroAddrPort, errtrace.Wrap(err)
	}
	return netip.MustParseAddrPort(conn.LocalAddr().String()), nil
}

func (tp *ReliableTransport) serve() error {
	defer tp.lsnr.Close()

	tp.log.LogAttrs(tp.ctx, slog.LevelDebug, "begin serving the listener", slog.Any("listener", tp.lsnr))
	defer tp.log.LogAttrs(tp.ctx, slog.LevelDebug, "serving the listener finished", slog.Any("listener", tp.lsnr))

	var tempDelay time.Duration
	for {
		conn, err := tp.lsnr.Accept()
		if err != nil {
			if errorutil.IsTemporaryErr(err) {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if v := time.Minute; tempDelay > v {
					tempDelay = v
				}

				tp.log.LogAttrs(tp.ctx, slog.LevelDebug,
					"failed to accept connection due to the temporary error, continue serving after delay...",
					slog.Any("error", err),
					slog.Duration("delay", tempDelay),
				)

				tmr := time.NewTimer(tempDelay)
				select {
				case <-tp.ctx.Done():
					tmr.Stop()
					return errtrace.Wrap(ErrTransportClosed)
				case <-tmr.C:
				}
				continue
			}

			select {
			case <-tp.ctx.Done():
				return errtrace.Wrap(ErrTransportClosed)
			default:
				return errtrace.Wrap(err)
			}
		}

		tp.trackConn(tp.initConn(conn))
	}
}

func (tp *ReliableTransport) initConn(conn net.Conn) net.Conn {
	conn = newAutoCloseConn(
		newCloseOnceConn(
			newLogConn(conn, tp.log),
		),
		tp.connIdleTTL,
	)
	tp.connSrvWg.Go(func() {
		if err := tp.serveConn(conn); err != nil && !errors.Is(err, ErrTransportClosed) {
			tp.log.LogAttrs(tp.ctx, slog.LevelWarn, "failed to serve the connection",
				slog.Any("connection", conn),
				slog.Any("error", err),
			)
		}
	})
	return conn
}

func (tp *ReliableTransport) serveConn(conn net.Conn) error {
	defer func() {
		tp.untrackConn(conn)
		conn.Close()
	}()

	tp.log.LogAttrs(tp.ctx, slog.LevelDebug, "begin serving the connection", slog.Any("connection", conn))
	defer tp.log.LogAttrs(tp.ctx, slog.LevelDebug, "serving the connection finished", slog.Any("connection", conn))

	var msgs iter.Seq2[Message, error]
	if tp.meta.Streamed {
		msgs = streamMsgs(conn, tp.parser, time.Minute)
	} else {
		msgs = packetMsgs(&packetConn{conn}, tp.parser, time.Minute)
	}

	err := tp.readMsgs(msgs)
	select {
	case <-tp.ctx.Done():
		return errtrace.Wrap(ErrTransportClosed)
	default:
		return errtrace.Wrap(err)
	}
}

type connTracker struct {
	mu    sync.RWMutex
	conns map[netip.AddrPort]net.Conn
}

func (trk *connTracker) trackConn(c net.Conn) {
	raddr := netip.MustParseAddrPort(c.RemoteAddr().String())

	trk.mu.Lock()
	if trk.conns == nil {
		trk.conns = make(map[netip.AddrPort]net.Conn)
	}
	trk.conns[raddr] = c
	trk.mu.Unlock()
}

func (trk *connTracker) untrackConn(c net.Conn) {
	raddr := netip.MustParseAddrPort(c.RemoteAddr().String())

	trk.mu.Lock()
	delete(trk.conns, raddr)
	trk.mu.Unlock()
}

func (trk *connTracker) getConn(raddr netip.AddrPort) (net.Conn, bool) {
	trk.mu.RLock()
	defer trk.mu.RUnlock()

	conn, ok := trk.conns[raddr]
	if !ok {
		return nil, false
	}
	return conn, true
}

func (trk *connTracker) getOrDialConn(raddr netip.AddrPort, dialConn func(netip.AddrPort) (net.Conn, error)) (net.Conn, error) {
	trk.mu.Lock()
	defer trk.mu.Unlock()

	c, ok := trk.conns[raddr]
	if !ok {
		var err error
		c, err = dialConn(raddr)
		if err != nil {
			return nil, errtrace.Wrap(err)
		}

		if trk.conns == nil {
			trk.conns = make(map[netip.AddrPort]net.Conn)
		}
		trk.conns[raddr] = c
	}
	return c, nil
}

func (trk *connTracker) allConns() iter.Seq[net.Conn] {
	return func(yield func(net.Conn) bool) {
		trk.mu.RLock()
		defer trk.mu.RUnlock()

		for _, c := range trk.conns {
			if !yield(c) {
				return
			}
		}
	}
}
