package transport

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"net"
	"net/netip"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/gosip/dns"
	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/netutil"
	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

type TransportOptions struct {
	// PublicAddr is a template used to build the Via header's "sent-by" field
	// for outgoing requests.
	//
	// The address is normalized when the transport is created and finalized at
	// send time using the actual connection local address. The rules are:
	//
	//  1. Domain addresses with no port or a non-zero port are used as-is
	//     (for example, "example.com" or "example.com:5060").
	//  2. IP addresses with no port or a non-zero port are used as-is
	//     (for example, "192.0.2.1" or "192.0.2.1:5060").
	//  3. An empty [Addr] or an unspecified IP address (0.0.0.0 or ::) is replaced
	//     with the host IP resolved by [netutil.HostIP]; any explicit port
	//     is preserved.
	//  4. A port of 0 is a placeholder that is replaced at send time with the
	//     actual local port.
	//  5. If [netutil.HostIP] fails, or the host is empty, the host is resolved
	//     at send time from the connection local address. If the exact local
	//     address is not tracked as a listener, the transport falls back to a
	//     listener of the same IP family.
	//
	// The final sent-by is always determined by the transport during message sending.
	PublicAddr sip.Addr
	// Parser is a parser used to parse inbound SIP messages.
	// If nil, [sip.DefaultParser] is used.
	Parser sip.Parser
	// ClientLocator is used to locate remote clients for response routing.
	// If nil, a new [sip.RemoteElementLocator] is created with [TransportOptions.DNSResolver]
	// and current transport metadata.
	ClientLocator sip.RemoteClientLocator
	// DNSResolver is used only when [TransportOptions.ClientLocator] is nil to
	// create new [sip.RemoteElementLocator].
	// If nil, [dns.DefaultResolver] is used.
	DNSResolver sip.DNSResolver
	// Logger is a logger used to log transport events, warnings and errors.
	// If nil, [log.Default] is used.
	Logger *slog.Logger
	// ListenConfig is used to setup network listener: [net.PacketConn] or [net.Listener].
	// If nil, zero [NetListenConfig] is used.
	ListenConfig ListenConfig
	// ConnectionDialer is used to dial new [net.Conn] to a remote address.
	// If nil, zero [NetConnectionDialer] is used.
	ConnectionDialer ConnectionDialer
	// MaxMessageReadSize is the maximum buffer size for reading messages from the connection.
	// See [ConnectionOptions.MaxMessageReadSize].
	MaxMessageReadSize uint
	// MaxMessageWriteSize is the maximum size of the message to write to the connection.
	// See [ConnectionOptions.MaxMessageWriteSize].
	MaxMessageWriteSize uint
	// ConnectionReadTimeout is the timeout for reading from the connection.
	// See [ConnectionOptions.ReadTimeout].
	ConnectionReadTimeout time.Duration
	// ConnectionWriteTimeout is the timeout for writing to the connection.
	// See [ConnectionOptions.WriteTimeout].
	ConnectionWriteTimeout time.Duration
	// ConnectionIdleTimeout is the maximum duration a reliable connection may stay
	// idle before it is closed.
	// See [ConnectionOptions.IdleTimeout].
	ConnectionIdleTimeout time.Duration
	// HandleCustomMessage is called when a custom message is received.
	//
	// Custom message is the message that implements [sip.Message] interface
	// but not of [sip.Request] or [sip.Response] type.
	// Callback will be called for each such inbound message.
	// The [sip.Message] passed to the callback actually has value of type
	// sip.MessageEnvelope[sip.Message] with filled transport, local and remote
	// address data.
	HandleCustomMessage func(msg sip.Message)
}

func (o TransportOptions) pubAddr() sip.Addr {
	host := o.PublicAddr.Host()
	ip := o.PublicAddr.IP()

	if host != "" && ip == nil {
		// "domain[:port]" case
		return o.PublicAddr
	}

	// ip[:port] case
	if host == "" || ip.IsUnspecified() {
		// "0.0.0.0[:port]", ":port" case
		if v, err := netutil.HostIP(); err == nil {
			ip = v.To4()
		} else {
			// reset to use dynamically resolved based on source connection local addr
			host = ""
			ip = nil
		}
	}

	if port, ok := o.PublicAddr.Port(); ok {
		if ip == nil {
			return sip.AddrFromHostPort(host, port)
		}
		return sip.AddrFromIPPort(ip, port)
	}

	if ip == nil {
		return sip.AddrFromHost(host)
	}
	return sip.AddrFromIP(ip)
}

func (o TransportOptions) prsr() sip.Parser {
	if o.Parser == nil {
		return sip.DefaultParser()
	}
	return o.Parser
}

func (o TransportOptions) dnsRslvr() sip.DNSResolver {
	if o.DNSResolver == nil {
		return dns.DefaultResolver()
	}
	return o.DNSResolver
}

func (o TransportOptions) clnLctr(meta sip.TransportMetadata) sip.RemoteClientLocator {
	if o.ClientLocator != nil {
		return o.ClientLocator
	}
	return &sip.RemoteElementLocator{
		DNSResolver:      o.dnsRslvr(),
		MetadataProvider: &singleTranspMetaProvider{meta},
	}
}

func (o TransportOptions) log() *slog.Logger {
	if o.Logger == nil {
		return log.Default()
	}
	return o.Logger
}

func (o TransportOptions) lisConf() ListenConfig {
	if o.ListenConfig == nil {
		return defNetLisConf
	}
	return o.ListenConfig
}

func (o TransportOptions) connDlr() ConnectionDialer {
	if o.ConnectionDialer == nil {
		return defConnDlr
	}
	return o.ConnectionDialer
}

type ListenConfig interface {
	Listen(ctx context.Context, network, address string) (net.Listener, error)
	ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error)
}

type NetListenConfig = net.ListenConfig

var defNetLisConf = &NetListenConfig{}

type ConnectionDialer interface {
	Dial(ctx context.Context, network, address string) (net.Conn, error)
}

type NetConnectionDialer struct {
	net.Dialer
}

var defConnDlr = &NetConnectionDialer{}

func (d *NetConnectionDialer) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	return errors.Wrap2(d.DialContext(ctx, network, address))
}

type transpBase[L any] struct {
	impl    transpImpl[L]
	meta    sip.TransportMetadata
	pubAddr sip.Addr
	clnLctr sip.RemoteClientLocator
	log     *slog.Logger

	lisCfg   ListenConfig
	connDlr  ConnectionDialer
	connOpts ConnectionOptions

	closeOnce sync.Once
	closeErr  error
	closing   chan struct{}
	closed    atomic.Bool

	lisMap  listenerMap
	connMap connMap

	handleCustomMsg func(sip.Message)

	sip.StdMsgInterceptChain
}

type transpImpl[L any] interface {
	sip.Transport
	AcquireConnection(
		ctx context.Context,
		raddr netip.AddrPort,
		opts ...AcquireConnectionOptions,
	) (sip.TransportConnection, error)
	newListener(ctx context.Context, base L) (transpListener, error)
}

type transpListener interface {
	sip.TransportListener
	fmt.Stringer
	slog.LogValuer
	isClosed() bool
}

type trackedListener struct {
	transpListener
	// borrowed means the underlying listener was supplied by the caller through
	// ServeListener. Serve does not close it; Transport.Close does while it is tracked.
	borrowed bool
}

type trackedConn struct {
	*Connection
	// borrowed means the underlying connection was supplied by the caller through
	// ServeConn. Serve does not close it; Transport.Close does while it is tracked.
	borrowed bool
}

type (
	listenerMap = syncutil.RWMap[netip.AddrPort, *trackedListener]
	connBucket  = syncutil.RWMap[netip.AddrPort, *trackedConn]
	connMap     = syncutil.RWMap[netip.AddrPort, *connBucket]
)

func (tb *transpBase[L]) init(impl transpImpl[L], meta sip.TransportMetadata, opts TransportOptions) {
	tb.impl = impl
	tb.meta = meta.Canonic()
	tb.pubAddr = opts.pubAddr()
	tb.clnLctr = opts.clnLctr(tb.meta)
	tb.lisCfg = opts.lisConf()
	tb.connDlr = opts.connDlr()
	tb.log = opts.log().With(slog.Any("transport", impl))
	tb.handleCustomMsg = opts.HandleCustomMessage
	tb.closing = make(chan struct{})

	tb.connOpts.Parser = opts.prsr()
	tb.connOpts.Logger = tb.log
	tb.connOpts.MaxMessageReadSize = opts.MaxMessageReadSize
	tb.connOpts.MaxMessageWriteSize = opts.MaxMessageWriteSize
	tb.connOpts.ReadTimeout = opts.ConnectionReadTimeout
	tb.connOpts.WriteTimeout = opts.ConnectionWriteTimeout
	tb.connOpts.IdleTimeout = opts.ConnectionIdleTimeout
}

func (tb *transpBase[L]) Metadata() sip.TransportMetadata { return tb.meta }
func (tb *transpBase[L]) Logger() *slog.Logger            { return tb.log }

func (tb *transpBase[L]) isClosing() bool {
	select {
	case <-tb.closing:
		return true
	default:
		return false
	}
}

// Close closes the transport and all tracked listeners and connections.
//
// Borrowed resources supplied through ServeListener or ServeConn remain caller-owned
// when serving stops, but are closed here while they are still tracked.
func (tb *transpBase[L]) Close(ctx context.Context) error {
	tb.closeOnce.Do(func() {
		close(tb.closing)
		tb.closeErr = tb.close(ctx)
		tb.closed.Store(true)

		tb.log.LogAttrs(ctx, slog.LevelDebug, "transport closed")
	})
	return errors.Wrap(tb.closeErr)
}

func (tb *transpBase[L]) close(ctx context.Context) error {
	errs := make([]error, 0, tb.lisMap.Len()+tb.connMap.Len())

	for _, l := range tb.lisMap.All() {
		if err := l.Close(ctx); err != nil {
			errs = append(errs, errors.Errorf("close listener %q: %w", l, err))
		}
	}

	for _, conns := range tb.connMap.All() {
		for _, c := range conns.All() {
			if err := c.Close(ctx); err != nil {
				errs = append(errs, errors.Errorf("close connection %q: %w", c, err))
			}
		}
	}

	return errors.JoinPrefixWrap("transport close errors:", errs...)
}

func (tb *transpBase[L]) buildSentBy(laddr netip.AddrPort) sip.Addr {
	// tb.pubAddr can be here:
	// 1. domain[:port]
	// 2. ip[:port]
	// 3. [:port]
	if tb.pubAddr.IsValid() {
		// sent-by predefined and valid, use as is
		return tb.pubAddr
	}

	if _, ok := tb.lisMap.Load(laddr); !ok {
		// find matching listener by IP family, if laddr isn't a listener address
		for a, l := range tb.lisMap.All() {
			la := l.LocalAddr()
			if !l.isClosed() &&
				(la.Addr().Is4() && laddr.Addr().Is4() ||
					la.Addr().Is6() && laddr.Addr().Is6()) {
				laddr = la
				break
			}

			if l.isClosed() {
				tb.lisMap.Delete(a)
			}
		}
	}

	host := tb.pubAddr.Host()
	if host == "" {
		host = laddr.Addr().String()
	}

	port, portSet := tb.pubAddr.Port()
	if portSet && port == 0 {
		port = laddr.Port()
	}

	if port > 0 {
		return sip.AddrFromHostPort(host, port)
	}
	return sip.AddrFromHost(host)
}

func (tb *transpBase[L]) MatchSentBy(sentBy sip.Addr) bool {
	if tb.pubAddr.IsValid() {
		// Predefined sent-by case.
		return sentBy.Equal(tb.pubAddr)
	}

	// Host or port of the sent-by need to be replaced with the actual listener address.
	// So we need to check if the via address matches any of the listeners or connections.

	for _, l := range tb.lisMap.All() {
		if sentBy.Equal(tb.buildSentBy(l.LocalAddr())) {
			return true
		}
	}

	for _, conns := range tb.connMap.All() {
		for _, c := range conns.All() {
			if sentBy.Equal(tb.buildSentBy(c.LocalAddr())) {
				return true
			}
		}
	}

	return false
}

func (tb *transpBase[L]) ListenAddrs() iter.Seq[netip.AddrPort] {
	return func(yield func(netip.AddrPort) bool) {
		for a, l := range tb.lisMap.All() {
			if l.isClosed() {
				tb.lisMap.Delete(a)
				continue
			}

			if !yield(l.LocalAddr()) {
				return
			}
		}
	}
}

func (tb *transpBase[L]) Listen(ctx context.Context, addr string) (ls sip.TransportListener, err error) {
	if tb.isClosing() {
		return nil, errors.Wrap(ErrTransportClosed)
	}

	var (
		netLis   any
		lisFound bool
	)
	switch {
	case strings.HasPrefix(tb.meta.Network, "udp") || strings.HasPrefix(tb.meta.Network, "ip"):

		var ls net.PacketConn
		ls, err = tb.lisCfg.ListenPacket(ctx, tb.meta.Network, addr)
		defer func() {
			if ls != nil && (err != nil || lisFound) {
				_ = ls.Close()
			}
		}()

		netLis = ls
	case strings.HasPrefix(tb.meta.Network, "tcp"):

		var ls net.Listener
		ls, err = tb.lisCfg.Listen(ctx, tb.meta.Network, addr)
		defer func() {
			if ls != nil && (err != nil || lisFound) {
				_ = ls.Close()
			}
		}()

		netLis = ls
	default:
		return nil, errors.Errorf("unsupported network: %s", tb.meta.Network)
	}
	if err != nil {
		return nil, errors.Wrap(err)
	}

	ls, lisFound, err = tb.trackListener(ctx, netLis.(L), false) //nolint:forcetypeassert
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return ls, nil
}

// ServeListener serves a listener supplied by the caller and tracks it while it
// is serving.
//
// It does not close the supplied listener when serving stops or when ctx is cancelled.
// The caller must close it to interrupt a connection-oriented Accept.
// Transport.Close closes the listener if it is still tracked.
func (tb *transpBase[L]) ServeListener(ctx context.Context, netLis L) error {
	if tb.isClosing() {
		return errors.Wrap(ErrTransportClosed)
	}
	if util.IsNil(netLis) {
		return errors.ErrorWrap("nil listener")
	}

	ls, found, err := tb.trackListener(ctx, netLis, true)
	if err != nil {
		return errors.Wrap(err)
	}
	if found {
		return errors.Wrap(ErrListenerTracked)
	}
	defer tb.untrackListener(ctx, ls)

	err = ls.Serve(ctx)

	select {
	case <-ctx.Done():
		if err == nil {
			err = ctx.Err()
		} else if !errors.Is(err, ctx.Err()) {
			err = errors.Errorf("%w: %w", err, ctx.Err())
		}
		return errors.Wrap(err)
	default:
	}

	select {
	case <-tb.closing:
		if err == nil {
			err = ErrTransportClosed
		} else if !errors.Is(err, ErrTransportClosed) {
			err = errors.Errorf("%w: %w", err, ErrTransportClosed)
		}
		return errors.Wrap(err)
	default:
	}

	return errors.Wrap(err)
}

func (tb *transpBase[L]) trackListener(
	ctx context.Context,
	netLis L,
	external bool,
) (ls *trackedListener, found bool, err error) {
	if tb.isClosing() {
		return nil, false, errors.Wrap(ErrTransportClosed)
	}
	if util.IsNil(netLis) {
		return nil, false, errors.ErrorWrap("nil listener")
	}

	blAddr, ok := netutil.ListenAddr(netLis)
	if !ok {
		return nil, false, errors.ErrorfWrap("unexpected listener type %T", netLis)
	}

	if !netutil.IsNetworkCompatible(tb.meta.Network, blAddr.Network()) {
		return nil, false, errors.ErrorfWrap("incompatible listener network %q", blAddr.Network())
	}

	laddr := netutil.UnmapAddrPort(netip.MustParseAddrPort(blAddr.String()))

	if l, ok := tb.lisMap.Load(laddr); ok {
		if !l.isClosed() {
			return l, true, nil
		}

		tb.lisMap.Delete(laddr)
	}

	ls, found, err = tb.lisMap.LoadOrStoreFunc(laddr, func() (*trackedListener, error) {
		l, err := tb.impl.newListener(ctx, netLis)
		if err != nil {
			return nil, errors.Wrap(err)
		}
		return &trackedListener{l, external}, nil
	})
	if err != nil {
		return ls, false, errors.Wrap(err)
	}

	if !found {
		tb.log.LogAttrs(ctx, slog.LevelDebug, "listener tracked", slog.Any("listener", ls))
	}

	return ls, found, nil
}

func (tb *transpBase[L]) untrackListener(ctx context.Context, ls *trackedListener) {
	if !tb.lisMap.CompareAndDeleteFunc(ls.LocalAddr(), func(actual *trackedListener) bool {
		return actual == ls
	}) {
		return
	}

	tb.log.LogAttrs(ctx, slog.LevelDebug, "listener untracked", slog.Any("listener", ls))

	if ls.borrowed {
		return
	}
	if err := ls.Close(ctx); err != nil {
		tb.log.LogAttrs(ctx, slog.LevelWarn, "failed to close listener",
			slog.Any("listener", ls),
			slog.Any("error", err),
		)
	}
}

// ServeConn starts the reading loop for a connection supplied by the caller and
// tracks it while it is serving.
//
// It does not close the supplied connection when serving stops or when ctx is cancelled;
// the caller owns that resource. Transport.Close closes the connection if it is
// still tracked.
//
// If serving is stopped by context cancellation, ServeConn returns ctx.Err().
// If serving is stopped by calling conn.Close(), it returns [ErrNetworkClosed].
// If serving is stopped by transport close, it returns [ErrTransportClosed].
// In case of any other breaking read error, it returns the last read error.
func (tb *transpBase[L]) ServeConn(ctx context.Context, netConn net.Conn) error {
	if tb.isClosing() {
		return errors.Wrap(ErrTransportClosed)
	}
	if netConn == nil {
		return errors.ErrorWrap("nil connection")
	}

	_, done, err := tb.serveConn(ctx, netConn, true)
	if err != nil {
		return errors.Wrap(err)
	}
	if done == nil {
		return errors.Wrap(ErrConnectionTracked)
	}

	err = <-done

	select {
	case <-ctx.Done():
		if err == nil {
			err = ctx.Err()
		} else if !errors.Is(err, ctx.Err()) {
			err = errors.Errorf("%w: %w", err, ctx.Err())
		}
		return errors.Wrap(err)
	default:
	}

	select {
	case <-tb.closing:
		if err == nil {
			err = ErrTransportClosed
		} else if !errors.Is(err, ErrTransportClosed) {
			err = errors.Errorf("%w: %w", err, ErrTransportClosed)
		}
		return errors.Wrap(err)
	default:
	}

	return errors.Wrap(err)
}

func (tb *transpBase[L]) serveConn(
	ctx context.Context,
	netConn net.Conn,
	external bool,
) (*trackedConn, <-chan error, error) {
	conn, found, err := tb.trackConn(ctx, netConn, external)
	if err != nil {
		return nil, nil, errors.Wrap(err)
	}
	if found {
		return conn, nil, nil
	}

	go func() {
		select {
		case <-ctx.Done():
			if conn.borrowed {
				return
			}

			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()

			if err := conn.Close(ctx); err != nil {
				tb.log.LogAttrs(ctx, slog.LevelWarn, "failed to close connection",
					slog.Any("connection", conn),
					slog.Any("error", err),
				)
			}
		case <-tb.closing:
		}
	}()

	ch := make(chan error, 1)
	go func() {
		defer func() {
			tb.untrackConn(ctx, conn)
			close(ch)
		}()

		ch <- errors.Wrap(tb.readMsgs(ctx, conn.Messages(ctx), true))
	}()

	return conn, ch, nil
}

func (tb *transpBase[L]) trackConn(
	ctx context.Context,
	netConn net.Conn,
	external bool,
) (conn *trackedConn, found bool, err error) {
	if tb.isClosing() {
		return nil, false, errors.Wrap(ErrTransportClosed)
	}
	if netConn == nil {
		return nil, false, errors.ErrorWrap("nil connection")
	}
	if !netutil.IsNetworkCompatible(tb.meta.Network, netConn.LocalAddr().Network()) {
		return nil, false, errors.ErrorfWrap("incompatible connection network %q", netConn.LocalAddr().Network())
	}

	laddr := netutil.UnmapAddrPort(netip.MustParseAddrPort(netConn.LocalAddr().String()))
	raddr := netutil.UnmapAddrPort(netip.MustParseAddrPort(netConn.RemoteAddr().String()))

	conns, _, _ := tb.connMap.LoadOrStoreFunc(raddr, func() (*connBucket, error) { return &connBucket{}, nil })
	if found, ok := conns.Load(laddr); ok {
		if !found.isClosed() {
			return found, true, nil
		}

		conns.Delete(laddr)
	}

	conn, found, err = conns.LoadOrStoreFunc(laddr, func() (*trackedConn, error) {
		c, err := NewConnection(ctx, netConn, tb.meta, tb.connOpts)
		if err != nil {
			return nil, errors.Wrap(err)
		}
		return &trackedConn{c, external}, nil
	})
	if err != nil {
		tb.connMap.CompareAndDeleteFunc(raddr, func(v *connBucket) bool { return v.Len() == 0 })
		return nil, false, errors.Wrap(err)
	}

	if !found {
		tb.log.LogAttrs(ctx, slog.LevelDebug, "connection tracked", slog.Any("connection", conn))
	}

	return conn, found, nil
}

func (tb *transpBase[L]) untrackConn(ctx context.Context, conn *trackedConn) {
	conns, ok := tb.connMap.Load(conn.raddr)
	if !ok {
		return
	}
	if _, ok = conns.LoadAndDelete(conn.laddr); !ok {
		return
	}

	tb.connMap.CompareAndDeleteFunc(conn.raddr, func(v *connBucket) bool { return v.Len() == 0 })

	tb.log.LogAttrs(ctx, slog.LevelDebug, "connection untracked", slog.Any("connection", conn))

	if conn.borrowed {
		return
	}
	if err := conn.Close(ctx); err != nil {
		tb.log.LogAttrs(ctx, slog.LevelWarn, "failed to close connection",
			slog.Any("connection", conn),
			slog.Any("error", err),
		)
	}
}

func (tb *transpBase[L]) findConn(raddr, laddr netip.AddrPort, host string) (*trackedConn, bool) {
	if conns, ok := tb.connMap.Load(raddr); ok {
		if laddr.IsValid() {
			if c, ok := conns.Load(laddr); ok {
				if !c.isClosed() && c.VerifyHost(host) == nil {
					return c, true
				}

				if c.isClosed() {
					conns.Delete(laddr)
				}
			}
		} else {
			for a, c := range conns.All() {
				if !c.isClosed() && c.VerifyHost(host) == nil {
					return c, true
				}

				if c.isClosed() {
					conns.Delete(a)
				}
			}
		}
	}

	return nil, false
}

func (tb *transpBase[L]) dialConn(ctx context.Context, raddr netip.AddrPort) (*trackedConn, error) {
	netConn, err := tb.connDlr.Dial(ctx, tb.meta.Network, raddr.String())
	if err != nil {
		return nil, errors.Wrap(err)
	}

	conn, _, err := tb.serveConn(context.WithoutCancel(ctx), netConn, false)
	if err != nil {
		netConn.Close()
		return nil, errors.Wrap(err)
	}
	return conn, nil
}

func (tb *transpBase[L]) readMsgs(ctx context.Context, msgs iter.Seq2[sip.Message, error], stopOnPanic bool) error {
	for msg, err := range msgs {
		var perr *sip.ParseError
		if err != nil {
			var ok bool
			if perr, ok = errors.AsType[*sip.ParseError](err); !ok {
				// stop on:
				// - non-recoverable connection read error (net.Error, net.ErrClosed, etc...)
				// - context cancel
				return errors.Wrap(err)
			}

			// pass messages with parse errors, they will be discarded below
			msg = perr.Msg
		}

		if msg != nil {
			switch msg := msg.(type) {
			case *sip.RequestEnvelope:
				if err := tb.recvReqSafe(ctx, msg, errors.Wrap(err)); err != nil && stopOnPanic {
					// stop on panics only if not listener connection
					return errors.Wrap(err)
				}
			case *sip.ResponseEnvelope:
				if err := tb.recvResSafe(ctx, msg, errors.Wrap(err)); err != nil && stopOnPanic {
					// stop on panics only if not listener connection
					return errors.Wrap(err)
				}
			default:
				if tb.handleCustomMsg != nil {
					tb.handleCustomMsg(msg)
				} else {
					tb.log.LogAttrs(ctx, slog.LevelWarn, "unsupported inbound message received",
						slog.Any("message", msg),
					)
				}
			}
		}

		if perr != nil && tb.meta.Streamed() {
			// stop on broken stream
			return errors.PrefixWrap(ErrBrokenConnectionStream, err)
		}
	}

	return nil
}

func (tb *transpBase[L]) recvReqSafe(ctx context.Context, req *sip.RequestEnvelope, err error) (finErr error) {
	defer func() {
		if pe := recover(); pe != nil {
			tb.log.LogAttrs(ctx, slog.LevelError,
				"panic occurred while processing the inbound request",
				slog.Any("request", req),
				slog.Any("error", pe),
				slog.Any("stack", log.StringValue(debug.Stack())),
			)

			if v, ok := pe.(error); ok {
				finErr = errors.Wrap(v)
			} else {
				finErr = errors.ErrorfWrap("%v", pe)
			}

			func() {
				defer func() {
					if pe := recover(); pe != nil {
						tb.log.LogAttrs(ctx, slog.LevelError,
							"panic occurred while processing of the previous panic error",
							slog.Any("request", req),
							slog.Any("error", pe),
							slog.Any("stack", log.StringValue(debug.Stack())),
						)
					}
				}()

				tb.respondOrDiscard(ctx, req, sip.ResponseStatusServerInternalError)
			}()
		}
	}()

	if err := tb.recvReq(ctx, req, errors.Wrap(err)); err != nil {
		var resOpts sip.RespondOptions
		sts := sip.ResponseStatusServerInternalError
		lvl := slog.LevelDebug
		if e, ok := errors.AsType[RequestRejectedError](err); ok && e != nil {
			lvl = e.LogLevel()
			resOpts = e.RespondOptions()

			if e.ResponseStatus().IsValid() {
				sts = e.ResponseStatus()
			}
		}

		tb.log.LogAttrs(ctx, lvl,
			"rejecting the inbound request due to error",
			slog.Any("request", req),
			slog.Any("error", err),
		)

		tb.respondOrDiscard(ctx, req, sts, resOpts)
	}

	return nil
}

func (tb *transpBase[L]) recvReq(ctx context.Context, req *sip.RequestEnvelope, err error) error {
	req.WithMessage(func(r *sip.Request) {
		// try to setup Via params even first to allow correct response routing in case of any failure
		if via, ok := r.Headers.FirstVia(); ok && via != nil && via.IsValid() {
			raddr := req.RemoteAddr()
			// RFC 3261 Section 18.2.1.
			if via.Addr.IP() == nil || !via.Addr.IP().Equal(raddr.Addr().AsSlice()) {
				if via.Params == nil {
					via.Params = make(sip.Values)
				}
				via.Params.Set("received", raddr.Addr().String())
			}
			// RFC 3581 Section 4.
			if via.Params.Has("rport") {
				via.Params.Set("rport", strconv.Itoa(int(raddr.Port())))
			}
		}
	})

	if err != nil {
		// we faced some errors during parsing of the request,
		// but if the parsed request contains mandatory headers,
		// then we can respond to it with a proper error response.
		var sts sip.ResponseStatus
		switch {
		case errors.Is(err, sip.ErrEntityTooLarge):
			sts = sip.ResponseStatusRequestEntityTooLarge
		case errors.Is(err, sip.ErrMessageTooLarge):
			sts = sip.ResponseStatusMessageTooLarge
		case sip.IsMessageError(err) || errors.IsGrammarError(err):
			sts = sip.ResponseStatusBadRequest
		default:
			sts = sip.ResponseStatusServerInternalError
		}

		return errors.Wrap(&reqRejectedError{
			cause:     err,
			resStatus: sts,
			logLevel:  slog.LevelDebug,
		})
	}

	if err := req.Validate(); err != nil {
		return errors.Wrap(&reqRejectedError{
			cause:     err,
			resStatus: sip.ResponseStatusBadRequest,
			logLevel:  slog.LevelDebug,
		})
	}

	receiver := sip.InterceptInboundRequest(
		slices.Collect(util.SeqFilter(
			tb.InboundRequestInterceptors.All(),
			func(i sip.InboundRequestInterceptor) bool { return i != nil },
		)),
		sip.RequestReceiverFunc(func(ctx context.Context, req *sip.RequestEnvelope) error {
			return errors.Wrap(&reqRejectedError{
				cause:     sip.ErrUnhandledMessage,
				resStatus: sip.ResponseStatusServiceUnavailable,
				logLevel:  slog.LevelWarn,
			})
		}),
	)
	return errors.Wrap(receiver.RecvRequest(ctx, req))
}

func (tb *transpBase[L]) respondOrDiscard(
	ctx context.Context,
	req *sip.RequestEnvelope,
	sts sip.ResponseStatus,
	opts ...sip.RespondOptions,
) {
	if err := tb.Respond(ctx, req, sts, opts...); err != nil {
		lvl := slog.LevelWarn
		if sip.IsMessageError(err) ||
			errors.IsClosedError(err) ||
			errors.IsCanceledError(err) {
			lvl = slog.LevelDebug
		}

		tb.log.LogAttrs(ctx, lvl,
			"silently discard the inbound request due to respond failure",
			slog.Any("request", req),
			slog.Any("error", err),
		)
	}
}

func (tb *transpBase[L]) recvResSafe(ctx context.Context, res *sip.ResponseEnvelope, err error) (finErr error) {
	defer func() {
		if pe := recover(); pe != nil {
			tb.log.LogAttrs(ctx, slog.LevelError,
				"panic occurred while processing the inbound response",
				slog.Any("response", res),
				slog.Any("error", pe),
				slog.Any("stack", log.StringValue(debug.Stack())),
			)

			if v, ok := pe.(error); ok {
				finErr = errors.Wrap(v)
			} else {
				finErr = errors.ErrorfWrap("%v", pe)
			}
		}
	}()

	if err := tb.recvRes(ctx, res, errors.Wrap(err)); err != nil {
		lvl := slog.LevelDebug
		if e, ok := errors.AsType[ResponseRejectedError](err); ok && e != nil {
			lvl = e.LogLevel()
		}

		tb.log.LogAttrs(ctx, lvl,
			"silently discard the inbound response due to error",
			slog.Any("response", res),
			slog.Any("error", err),
		)
	}

	return nil
}

func (tb *transpBase[L]) recvRes(ctx context.Context, res *sip.ResponseEnvelope, err error) error {
	if err != nil {
		return errors.Wrap(&resRejectedError{
			cause:    err,
			logLevel: slog.LevelDebug,
		})
	}

	if err := res.Validate(); err != nil {
		return errors.Wrap(&resRejectedError{
			cause:    err,
			logLevel: slog.LevelDebug,
		})
	}

	// RFC 3261 Section 18.1.2.
	var via header.ViaHop
	res.WithMessage(func(r *sip.Response) {
		v, _ := r.Headers.FirstVia()
		via = v.Clone()
	})
	if !tb.MatchSentBy(via.Addr) {
		return errors.Wrap(&resRejectedError{
			cause:    errors.Errorf("Via sent-by address %q not matched", via.Addr),
			logLevel: slog.LevelDebug,
		})
	}

	receiver := sip.InterceptInboundResponse(
		slices.Collect(util.SeqFilter(
			tb.InboundResponseInterceptors.All(),
			func(i sip.InboundResponseInterceptor) bool { return i != nil },
		)),
		sip.ResponseReceiverFunc(func(ctx context.Context, res *sip.ResponseEnvelope) error {
			return errors.Wrap(&resRejectedError{
				cause:    sip.ErrUnhandledMessage,
				logLevel: slog.LevelWarn,
			})
		}),
	)
	return errors.Wrap(receiver.RecvResponse(ctx, res))
}

// SendRequest sends the request to the remote address specified in the req.
//
// Context can be used to cancel the request sending process through the deadline.
//
// The request must have the [header.Via] header with at least one [header.ViaHop] element,
// this element can have zero transport and address fields, they will be filled by the transport.
// In case of reliable transport, Content-Length header will be added automatically if it is missing.
//
// Options are optional, if nil is passed, default options are used (see [SendRequestOptions]).
func (tb *transpBase[L]) SendRequest(
	ctx context.Context,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	if tb.isClosing() {
		return errors.Wrap(ErrTransportClosed)
	}

	sender := sip.InterceptOutboundRequest(
		slices.Collect(util.SeqFilter(
			tb.OutboundRequestInterceptors.All(),
			func(i sip.OutboundRequestInterceptor) bool { return i != nil },
		)),
		sip.RequestSenderFunc(tb.sendRequest),
	)
	return errors.Wrap(sender.SendRequest(ctx, req, opts...))
}

func (tb *transpBase[L]) sendRequest(
	ctx context.Context,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	if tb.isClosing() {
		return errors.Wrap(ErrTransportClosed)
	}

	req.SetTransport(tb.meta)

	raddr := req.RemoteAddr()
	if raddr.Port() == 0 {
		raddr = netip.AddrPortFrom(raddr.Addr(), tb.meta.DefaultPort)
	}
	req.SetRemoteAddr(raddr)

	acqOpts := AcquireConnectionOptions{
		LocalAddr: req.LocalAddr(),
		Dial:      true,
	}
	if u, ok := req.URI().(*sip.URI); ok && u.Secured {
		acqOpts.Host = u.Addr.Host()
	}
	conn, err := tb.impl.AcquireConnection(ctx, raddr, acqOpts)
	if err != nil {
		return errors.Wrap(err)
	}

	req.SetLocalAddr(conn.LocalAddr()).
		WithMessage(func(r *sip.Request) {
			sip.EnsureRequestVia(r, tb.meta.Proto, tb.buildSentBy(req.LocalAddr()))
			sip.EnsureRequestViaMulticast(r, raddr.Addr())
			sip.EnsureRequestFromTag(r)
			if tb.meta.Streamed() {
				r.Headers.Set(header.ContentLength(len(r.Body)))
			}
		})
	if err := req.Validate(); err != nil {
		return errors.Wrap(err)
	}

	sendOpts := util.LastSliceElemOr(opts, sip.SendRequestOptions{})

	return errors.Wrap(conn.WriteMessage(ctx, req, raddr, sendOpts.RenderOptions))
}

// SendResponse sends the response to a remote address resolved with steps
// defined in RFC 3261 Section 18.2.2. and RFC 3263 Section 5.
//
// Context can be used to cancel the response sending process through the deadline.
//
// The topmost [header.ViaHop] transport must match the transport protocol.
// In case of reliable transport, Content-Length header will be added automatically if it is missing.
//
// Options are optional, if nil is passed, default options are used (see [SendResponseOptions]).
func (tb *transpBase[L]) SendResponse(
	ctx context.Context,
	res *sip.ResponseEnvelope,
	opts ...sip.SendResponseOptions,
) error {
	if tb.isClosing() {
		return errors.Wrap(ErrTransportClosed)
	}

	sender := sip.InterceptOutboundResponse(
		slices.Collect(util.SeqFilter(
			tb.OutboundResponseInterceptors.All(),
			func(i sip.OutboundResponseInterceptor) bool { return i != nil },
		)),
		sip.ResponseSenderFunc(tb.sendResponse),
	)
	return errors.Wrap(sender.SendResponse(ctx, res, opts...))
}

//nolint:gocognit
func (tb *transpBase[L]) sendResponse(
	ctx context.Context,
	res *sip.ResponseEnvelope,
	opts ...sip.SendResponseOptions,
) error {
	if tb.isClosing() {
		return errors.Wrap(ErrTransportClosed)
	}

	res.SetTransport(tb.meta)

	var via header.ViaHop
	res.WithMessage(func(r *sip.Response) {
		if v, ok := r.Headers.FirstVia(); ok && v != nil {
			via = v.Clone()
		}

		sip.EnsureResponseToTag(r, sip.GenerateStableToTag(r, []byte(tb.pubAddr.String())))
		if tb.meta.Streamed() {
			r.Headers.Set(header.ContentLength(len(r.Body)))
		}
	})
	if err := res.Validate(); err != nil {
		return errors.Wrap(err)
	}
	if !via.Transport.Equal(tb.meta.Proto) {
		return errors.ErrorfWrap("unexpected Via transport %q", via.Transport)
	}

	sendOpts := util.LastSliceElemOr(opts, sip.SendResponseOptions{})

	conn, err := tb.impl.AcquireConnection(ctx, res.RemoteAddr(), AcquireConnectionOptions{
		LocalAddr: res.LocalAddr(),
		// TODO: how to verify host of response on secured transport?
		// Host: ...,
	})
	if err == nil {
		res.SetLocalAddr(conn.LocalAddr())

		if conn.RemoteAddr().IsValid() {
			// connected socket or conn-oriented transport
			if err = conn.WriteMessage(ctx, res, res.RemoteAddr(), sendOpts.RenderOptions); err == nil {
				return nil
			}
		}
	}

	if errors.Is(err, ErrTransportClosed) || errors.Is(err, ctx.Err()) {
		return errors.Wrap(err)
	}

	// fallback to RFC 3261 Section 18.2.2. and RFC 3263 Section 5
	var errs []error
	for raddr := range tb.clnLctr.LookupResponseAddrs(ctx, via, sendOpts.LookupOptions) {
		var err error

		res.SetRemoteAddr(raddr.Addr)

		if conn == nil || conn.RemoteAddr().IsValid() && conn.RemoteAddr() != raddr.Addr {
			if conn, err = tb.impl.AcquireConnection(ctx, raddr.Addr, AcquireConnectionOptions{
				LocalAddr: res.LocalAddr(),
				Dial:      true,
				// TODO: how to verify host of response on secured transport?
				// Host: ...,
			}); err != nil {
				if errors.Is(err, ErrTransportClosed) || errors.Is(err, ctx.Err()) {
					return errors.Wrap(err)
				}

				errs = append(errs, errors.Errorf("acquire connection to %q: %w", raddr.Addr, err))
				continue
			}

			res.SetLocalAddr(conn.LocalAddr())
		}

		err = conn.WriteMessage(ctx, res, res.RemoteAddr(), sendOpts.RenderOptions)
		if err == nil || errors.IsClosedError(err) || errors.Is(err, ctx.Err()) {
			return errors.Wrap(err)
		}

		errs = append(errs, errors.Errorf("write response to %q: %w", raddr.Addr, err))
	}

	if len(errs) == 0 {
		return errors.Wrap(sip.ErrNoAddress)
	}
	return errors.JoinPrefixWrap("send response errors:", errs...)
}

func (tb *transpBase[L]) Respond(
	ctx context.Context,
	req *sip.RequestEnvelope,
	sts sip.ResponseStatus,
	opts ...sip.RespondOptions,
) error {
	if tb.isClosing() {
		return errors.Wrap(ErrTransportClosed)
	}

	resOpts := util.LastSliceElemOr(opts, sip.RespondOptions{})
	if resOpts.ResponseOptions.LocalTag == "" {
		req.WithMessage(func(r *sip.Request) {
			resOpts.ResponseOptions.LocalTag = sip.GenerateStableToTag(r, []byte(tb.pubAddr.String()))
		})
	}

	return errors.Wrap(sip.Respond(ctx, req, sts, tb, resOpts))
}
