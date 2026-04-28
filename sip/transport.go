package sip

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"math"
	"net"
	"net/netip"
	"runtime/debug"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/netutil"
	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

// Transport config variables.
var (
	// MTU is the maximum Transport Unit.
	// It is used to limit the size of the message that can be sent over the unreliable transport.
	MTU uint = 1500
	// MaxMessageSize is the maximum network message size.
	// It is used to limit read buffer size for the streamed transport.
	MaxMessageSize uint = math.MaxUint16

	// ConnWriteTimeout is the default timeout on connection write operation.
	ConnWriteTimeout = time.Minute
	// ConnReadTimeout is the default timeout on connection read operation.
	ConnReadTimeout = time.Minute
	// PongWaitTimeout is the timeout on pong response wait.
	PongWaitTimeout = 10 * time.Second
)

// Transport errors.
const (
	// ErrTransportClosed is returned when attempting to use a closed transport.
	ErrTransportClosed  errors.Error = "transport closed"
	ErrConnNotFound     errors.Error = "connection not found"
	ErrConnTracked      errors.Error = "connection already tracked"
	ErrBrokenConnStream errors.Error = "broken connection stream"
	ErrListenerTracked  errors.Error = "listener already tracked"
)

// TransportProto is a transport protocol name.
type TransportProto = types.TransportProto

// TransportFlags is a bitmask that represents transport capabilities.
type TransportFlags uint

const (
	TransportFlagReliable TransportFlags = 1 << iota
	TransportFlagSecured
	TransportFlagStreamed
)

func (f TransportFlags) Reliable() bool {
	return f&TransportFlagReliable != 0
}

func (f TransportFlags) Secured() bool {
	return f&TransportFlagSecured != 0
}

func (f TransportFlags) Streamed() bool {
	return f&TransportFlagStreamed != 0
}

type transpFlagsData struct {
	Reliable bool `json:"reliable"`
	Secured  bool `json:"secured"`
	Streamed bool `json:"streamed"`
}

func (f TransportFlags) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(json.Marshal(transpFlagsData{
		Reliable: f.Reliable(),
		Secured:  f.Secured(),
		Streamed: f.Streamed(),
	}))
}

func (f *TransportFlags) UnmarshalJSON(data []byte) error {
	if f == nil {
		return errors.NewInvalidArgumentErrorWrap("nil flags")
	}

	var dto transpFlagsData
	if err := json.Unmarshal(data, &dto); err != nil {
		*f = 0
		return errors.Wrap(err)
	}

	*f = 0
	if f.Reliable() {
		*f |= TransportFlagReliable
	}

	if f.Secured() {
		*f |= TransportFlagSecured
	}

	if f.Streamed() {
		*f |= TransportFlagStreamed
	}

	return nil
}

// TransportMetadata represents transport metadata.
type TransportMetadata struct {
	// Proto is the transport protocol.
	Proto TransportProto `json:"proto"`
	// Network is the network type.
	Network string `json:"network"`
	// DefaultPort is the default port for the transport.
	DefaultPort uint16 `json:"default_port"`
	// Flags is a set of transport flags.
	Flags TransportFlags `json:"flags"`
	// NAPTRService is the NAPTR service string for the transport.
	NAPTRService string `json:"naptr_service"`
	// Priority defines the transport selection order.
	// Lower value means higher priority (e.g. UDP=0, TCP=10, TLS=20).
	Priority int `json:"priority"`
}

func (d TransportMetadata) IsValid() bool {
	return d.Proto.IsValid() && d.Network != "" && d.DefaultPort > 0
}

func (d TransportMetadata) Reliable() bool {
	return d.Flags.Reliable()
}

func (d TransportMetadata) Secured() bool {
	return d.Flags.Secured()
}

func (d TransportMetadata) Streamed() bool {
	return d.Flags.Streamed()
}

func (d TransportMetadata) Canonic() TransportMetadata {
	d.Proto = d.Proto.Canonic()
	d.Network = util.LCase(d.Network)
	d.NAPTRService = util.UCase(d.NAPTRService)
	return d
}

var (
	udpTranspMeta = TransportMetadata{
		Proto:        "UDP",
		Network:      "udp",
		DefaultPort:  5060,
		NAPTRService: "SIP+D2U",
		Priority:     0,
	}
	tcpTranspMeta = TransportMetadata{
		Proto:        "TCP",
		Network:      "tcp",
		DefaultPort:  5060,
		Flags:        TransportFlagReliable | TransportFlagStreamed,
		NAPTRService: "SIP+D2T",
		Priority:     10,
	}
	tlsTranspMeta = TransportMetadata{
		Proto:        "TLS",
		Network:      "tcp",
		DefaultPort:  5061,
		Flags:        TransportFlagReliable | TransportFlagStreamed | TransportFlagSecured,
		NAPTRService: "SIPS+D2T",
		Priority:     20,
	}
	sctpTranspMeta = TransportMetadata{
		Proto:        "SCTP",
		Network:      "tcp",
		DefaultPort:  5060,
		Flags:        TransportFlagReliable,
		NAPTRService: "SIP+D2S",
		Priority:     30,
	}
	tlssctpTranspMeta = TransportMetadata{
		Proto:        "TLS-SCTP",
		Network:      "tcp",
		DefaultPort:  5061,
		Flags:        TransportFlagReliable | TransportFlagSecured,
		NAPTRService: "SIPS+D2S",
		Priority:     40,
	}
	wsTranspMeta = TransportMetadata{
		Proto:        "WS",
		Network:      "tcp",
		DefaultPort:  80,
		Flags:        TransportFlagReliable,
		NAPTRService: "SIP+D2W",
		Priority:     50,
	}
	wssTranspMeta = TransportMetadata{
		Proto:        "WSS",
		Network:      "tcp",
		DefaultPort:  443,
		Flags:        TransportFlagReliable | TransportFlagSecured,
		NAPTRService: "SIPS+D2W",
		Priority:     60,
	}
)

func UDPTransportMetadata() TransportMetadata     { return udpTranspMeta }
func TCPTransportMetadata() TransportMetadata     { return tcpTranspMeta }
func TLSTransportMetadata() TransportMetadata     { return tlsTranspMeta }
func SCTPTransportMetadata() TransportMetadata    { return sctpTranspMeta }
func TLSSCTPTransportMetadata() TransportMetadata { return tlssctpTranspMeta }
func WSTransportMetadata() TransportMetadata      { return wsTranspMeta }
func WSSTransportMetadata() TransportMetadata     { return wssTranspMeta }

// TransportMetadataProvider provides transport metadata for different protocols and NAPTR services.
type TransportMetadataProvider interface {
	MetadataByProto(proto TransportProto) TransportMetadata
	MetadataByNAPTRService(service string) TransportMetadata
	// AllMetadata returns a sequence of all registered transport metadata ordered by priority.
	AllMetadata() iter.Seq[TransportMetadata]
}

type singleTransportMetadataProvider struct {
	meta TransportMetadata
}

func (p *singleTransportMetadataProvider) MetadataByProto(proto TransportProto) TransportMetadata {
	if p.meta.Proto == proto {
		return p.meta
	}
	return TransportMetadata{}
}

func (p *singleTransportMetadataProvider) MetadataByNAPTRService(service string) TransportMetadata {
	if p.meta.NAPTRService == service {
		return p.meta
	}
	return TransportMetadata{}
}

func (p *singleTransportMetadataProvider) AllMetadata() iter.Seq[TransportMetadata] {
	return func(yield func(TransportMetadata) bool) {
		yield(p.meta)
	}
}

// Transport represents a combination of client and server transport functions.
type Transport interface {
	RequestSender
	ResponseSender
	MessageInterceptorChain
	// Metadata returns transport metadata.
	Metadata() TransportMetadata
	// ListenAndServe starts listening on the given address and serves incoming messages.
	ListenAndServe(ctx context.Context, addr string) error
	// Close closes the transport and releases underlying resources.
	Close() error
}

const transpCtxKey types.ContextKey = "transport"

func ContextWithTransport(ctx context.Context, tp Transport) context.Context {
	if t, ok := TransportFromContext(ctx); ok && t == tp {
		return ctx
	}
	return context.WithValue(ctx, transpCtxKey, tp)
}

func TransportFromContext(ctx context.Context) (Transport, bool) {
	tp, ok := ctx.Value(transpCtxKey).(Transport)
	return tp, ok
}

type TransportOptions struct {
	// SentBy is a "host[:port]" used as mask to build the Via's "sent-by" field.
	//
	// The sent-by field is formed according to the following rules:
	// 1. If a non-zero valid [Addr] is passed, it is used as is.
	// 2. If [Addr] is zero (empty), the IP address resolved with [netutil.GetHostIP] is used.
	// 3. If [Addr] has an empty host but a non-zero port,
	//    the host IP from [netutil.GetHostIP] is combined with the specified port.
	// 4. If [Addr] has a non-empty host but zero port,
	//    the host is used as is and port 0 signals to use the actual connection port.
	// 5. If [netutil.GetHostIP] fails to resolve the host IP,
	//    the address will be finalized during message sending using the actual connection IP and port.
	//
	// The final sent-by address is determined by the transport during message sending,
	// which can replace port 0 with the actual connection port and resolve unspecified addresses.
	SentBy Addr
	// Parser is a parser used to parse inbound SIP messages.
	// If nil, [DefaultParser] is used.
	Parser Parser
	// RemoteClientLocator is used to locate remote clients for response routing.
	// If nil, [DefaultRemoteElementLocator] is used.
	RemoteClientLocator RemoteClientLocator
	// Logger is a logger used to log transport events, warnings and errors.
	// If nil, [log.Default] is used.
	Logger *slog.Logger
	// ConnIdleTTL is the maximum duration a reliable connection may be idle before it is closed.
	//
	// Idle timer resets every time a new message is received or sent.
	// If the TTL is set to -1, then no idle timer is used, connections will stay opened until transport shutdown.
	// If the TTL is set to 0, then the value of [TimingConfig.TimeC] is used, which is 5m by default.
	ConnIdleTTL time.Duration
	// ConnDialer is used to dial new connection to a remote address.
	// If nil, [net.Dialer] is used.
	ConnDialer ConnDialer
}

func (o *TransportOptions) sentBy() Addr {
	if o != nil && o.SentBy.IsValid() && (o.SentBy.IP() == nil || !o.SentBy.IP().IsUnspecified()) {
		return o.SentBy
	}

	var (
		host    string
		ip      net.IP
		port    uint16
		hasPort bool
	)
	if o == nil || o.SentBy.Host() == "" || o.SentBy.IP() != nil && o.SentBy.IP().IsUnspecified() {
		if v, err := netutil.GetHostIP(); err == nil {
			ip = v.To4()
		}
	}

	if o == nil {
		hasPort = true
	} else if p, ok := o.SentBy.Port(); ok {
		port = p
		hasPort = ok
	}

	if hasPort {
		if ip == nil {
			return AddrFromHostPort(host, port)
		}
		return AddrFromIPPort(ip, port)
	}

	if ip == nil {
		return AddrFromHostPort(host, 0)
	}

	return AddrFromIPPort(ip, 0)
}

func (o *TransportOptions) parser() Parser {
	if o == nil || o.Parser == nil {
		return DefaultParser()
	}
	return o.Parser
}

func (o *TransportOptions) rmtClnLctr() RemoteClientLocator {
	if o == nil || o.RemoteClientLocator == nil {
		return DefaultRemoteElementLocator()
	}
	return o.RemoteClientLocator
}

func (o *TransportOptions) logger() *slog.Logger {
	if o == nil || o.Logger == nil {
		return log.Default()
	}
	return o.Logger
}

func (o *TransportOptions) connIdleTTL() time.Duration {
	if o == nil || o.ConnIdleTTL == 0 {
		return defTimingCfg.TimeC()
	}
	return o.ConnIdleTTL
}

func (o *TransportOptions) connDialer() ConnDialer {
	if o == nil || o.ConnDialer == nil {
		return &net.Dialer{}
	}
	return o.ConnDialer
}

// ConnDialer is used to init connected connections.
type ConnDialer interface {
	// Dial dials a connection to the remote address.
	DialContext(ctx context.Context, nt, addr string) (net.Conn, error)
}

// ConnDialerFunc is a [ConnDialer] implementation based on a function.
type ConnDialerFunc func(ctx context.Context, nt, addr string) (net.Conn, error)

func (f ConnDialerFunc) DialContext(ctx context.Context, nt, addr string) (net.Conn, error) {
	return errors.Wrap2(f(ctx, nt, addr))
}

type AcquireConnOptions struct {
	LocalAddr netip.AddrPort
	Dial      bool
	Host      string
}

func (o *AcquireConnOptions) locAddr() netip.AddrPort {
	if o == nil {
		return netip.AddrPort{}
	}
	return netutil.UnmapAddrPort(o.LocalAddr)
}

func (o *AcquireConnOptions) dial() bool {
	if o == nil {
		return false
	}
	return o.Dial
}

func (o *AcquireConnOptions) host() string {
	if o == nil {
		return ""
	}
	return o.Host
}

type transpBase[L listener, B any] struct {
	impl       transpImpl[L, B]
	meta       TransportMetadata
	sentBy     Addr
	prs        Parser
	rmtClnLctr RemoteClientLocator
	log        *slog.Logger
	connTTL    time.Duration
	connDialer ConnDialer

	closeOnce sync.Once
	closeErr  error
	closing   chan struct{}
	closed    atomic.Bool

	baseMessageInterceptorChain

	liss  lisMap[L]
	conns connMap
}

type transpImpl[L listener, B any] interface {
	Transport
	AcquireConn(ctx context.Context, raddr netip.AddrPort, opts *AcquireConnOptions) (Conn, error)
	newListener(ctx context.Context, base B) (L, error)
}

type listener interface {
	fmt.Stringer
	Close() error
	listenAddr() netip.AddrPort
	isClosed() bool
}

type (
	connBucket         = syncutil.RWMap[netip.AddrPort, *conn]
	connMap            = syncutil.RWMap[netip.AddrPort, *connBucket]
	lisMap[T listener] = syncutil.RWMap[netip.AddrPort, T]
)

func (tb *transpBase[L, B]) init(impl transpImpl[L, B], meta TransportMetadata, opts *TransportOptions) {
	tb.impl = impl
	tb.meta = meta.Canonic()
	tb.sentBy = opts.sentBy()
	tb.prs = opts.parser()
	tb.rmtClnLctr = opts.rmtClnLctr()
	tb.connTTL = opts.connIdleTTL()
	tb.connDialer = opts.connDialer()
	tb.closing = make(chan struct{})
	tb.log = opts.logger().With(slog.Any("transport", impl))
}

func (tb *transpBase[L, B]) Metadata() TransportMetadata {
	if tb == nil {
		return TransportMetadata{}
	}
	return tb.meta
}

func (tb *transpBase[L, B]) LogValue() slog.Value {
	if tb == nil {
		return slog.Value{}
	}

	return slog.GroupValue(
		slog.Any("proto", tb.meta.Proto),
		slog.Any("network", tb.meta.Network),
	)
}

func (tb *transpBase[L, B]) Logger() *slog.Logger {
	if tb == nil {
		return nil
	}
	return tb.log
}

func (tb *transpBase[L, B]) isClosing() bool {
	select {
	case <-tb.closing:
		return true
	default:
		return false
	}
}

func (tb *transpBase[L, B]) Close() error {
	if tb == nil {
		return nil
	}

	tb.closeOnce.Do(func() {
		close(tb.closing)
		tb.closeErr = tb.close()
		tb.closed.Store(true)

		tb.log.Debug("transport closed")
	})

	return errors.Wrap(tb.closeErr)
}

func (tb *transpBase[L, B]) close() error {
	errs := make([]error, 0, tb.liss.Len()+tb.conns.Len())

	for _, l := range tb.liss.All() {
		if err := l.Close(); err != nil {
			errs = append(errs, errors.Errorf("close listener %q: %w", l, err))
		}
	}

	for _, conns := range tb.conns.All() {
		for _, c := range conns.All() {
			if err := c.Close(); err != nil {
				errs = append(errs, errors.Errorf("close connection %q: %w", c, err))
			}
		}
	}

	return errors.JoinPrefixWrap("transport close errors:", errs...)
}

func (tb *transpBase[L, B]) makeSentBy(laddr netip.AddrPort) Addr {
	if tb.sentBy.IsValid() {
		return tb.sentBy
	}

	if _, ok := tb.liss.Load(laddr); !ok {
		for k, l := range tb.liss.All() {
			lsAddr := l.listenAddr()
			if !l.isClosed() &&
				(lsAddr.Addr().Is4() && laddr.Addr().Is4() ||
					lsAddr.Addr().Is6() && laddr.Addr().Is6()) {
				laddr = lsAddr
				break
			}

			if l.isClosed() {
				tb.liss.Delete(k)
			}
		}
	}

	var (
		host string
		port uint16
	)
	if h := tb.sentBy.Host(); h == "" {
		host = laddr.Addr().String()
	} else {
		host = h
	}

	if p, ok := tb.sentBy.Port(); ok {
		if p > 0 {
			port = p
		} else {
			port = laddr.Port()
		}
	}

	if port > 0 {
		return AddrFromHostPort(host, port)
	}

	return AddrFromHost(host)
}

func (tb *transpBase[L, B]) matchSentBy(viaAddr Addr, laddr netip.AddrPort) bool {
	sentBy := tb.makeSentBy(laddr)
	if viaAddr.Equal(sentBy) {
		return true
	}

	for _, l := range tb.liss.All() {
		sentBy = tb.makeSentBy(l.listenAddr())
		if viaAddr.Equal(sentBy) {
			return true
		}
	}

	return false
}

func (tb *transpBase[L, B]) trackListener(ctx context.Context, netLis B) (ls L, found bool, err error) {
	if tb.isClosing() {
		var zero L
		return zero, false, errors.Wrap(ErrTransportClosed)
	}

	blAddr, ok := netutil.ListenAddr(netLis)
	if !ok {
		var zero L
		return zero, false, errors.NewInvalidArgumentErrorWrap("unexpected listener type %T", netLis)
	}

	if !netutil.IsNetworkCompatible(tb.meta.Network, blAddr.Network()) {
		var zero L
		return zero, false, errors.NewInvalidArgumentErrorWrap("incompatible listener network %q", blAddr.Network())
	}

	laddr := netutil.UnmapAddrPort(netip.MustParseAddrPort(blAddr.String()))

	if l, ok := tb.liss.Load(laddr); ok {
		if !l.isClosed() {
			return l, true, nil
		}

		tb.liss.Delete(laddr)
	}

	ls, found, err = tb.liss.LoadOrStoreFunc(laddr, func() (L, error) {
		return errors.Wrap2(tb.impl.newListener(ctx, netLis))
	})
	if err != nil {
		return ls, false, errors.Wrap(err)
	}

	if !found {
		tb.log.LogAttrs(ctx, slog.LevelDebug, "listener tracked", slog.Any("listener", ls))
	}

	return ls, found, nil
}

func (tb *transpBase[L, B]) untrackListener(ctx context.Context, ls L) {
	if _, ok := tb.liss.LoadAndDelete(ls.listenAddr()); ok {
		ls.Close()

		tb.log.LogAttrs(ctx, slog.LevelDebug, "listener untracked", slog.Any("listener", ls))
	}
}

// ServeConn starts reading loop for the connected packet connection.
// Valid inbound messages will routed to appropriate message interceptor.
//
// Context is passed to inbound message interceptors and can be used to cancel serving of the connection.
func (tb *transpBase[L, B]) ServeConn(ctx context.Context, netConn net.Conn) error {
	_, done, err := tb.serveConn(ctx, netConn)
	if err != nil {
		return errors.Wrap(err)
	}

	if done == nil {
		return errors.Wrap(ErrConnTracked)
	}

	err = <-done
	select {
	case <-ctx.Done():
		if err == nil {
			err = ctx.Err()
		} else if !errors.Is(err, ctx.Err()) {
			err = errors.Errorf("%w: %w", err, ctx.Err())
		}
	case <-tb.closing:
		if err == nil {
			err = ErrTransportClosed
		} else if !errors.Is(err, ErrTransportClosed) {
			err = errors.Errorf("%w: %w", err, ErrTransportClosed)
		}
	default:
	}

	return errors.Wrap(err)
}

func (tb *transpBase[L, B]) serveConn(ctx context.Context, netConn net.Conn) (*conn, <-chan error, error) {
	if netConn == nil {
		return nil, nil, errors.NewInvalidArgumentErrorWrap("nil connection")
	}

	conn, found, err := tb.trackConn(ctx, netConn)
	if err != nil {
		return nil, nil, errors.Wrap(err)
	}

	if found {
		return conn, nil, nil
	}

	ctx = ContextWithTransport(ctx, tb.impl)
	ctx = ContextWithConn(ctx, conn)

	ch := make(chan error, 1)

	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-tb.closing:
		}
	}()

	go func() {
		defer func() {
			tb.untrackConn(ctx, conn)
			close(ch)
		}()

		ch <- errors.Wrap(tb.readMsgs(ctx, conn.Messages(ctx), true))
	}()

	return conn, ch, nil
}

func (tb *transpBase[L, B]) trackConn(ctx context.Context, netConn net.Conn) (c *conn, found bool, err error) {
	if tb.isClosing() {
		return nil, false, errors.Wrap(ErrTransportClosed)
	}

	if !netutil.IsNetworkCompatible(tb.meta.Network, netConn.LocalAddr().Network()) {
		return nil, false, errors.NewInvalidArgumentErrorWrap("incompatible connection network %q", netConn.LocalAddr().Network())
	}

	laddr := netutil.UnmapAddrPort(netip.MustParseAddrPort(netConn.LocalAddr().String()))
	raddr := netutil.UnmapAddrPort(netip.MustParseAddrPort(netConn.RemoteAddr().String()))

	conns, _, _ := tb.conns.LoadOrStoreFunc(raddr, func() (*connBucket, error) { return &connBucket{}, nil })
	if found, ok := conns.Load(laddr); ok {
		if !found.isClosed() {
			return found, true, nil
		}

		conns.Delete(laddr)
	}

	c, found, err = conns.LoadOrStoreFunc(laddr, func() (*conn, error) {
		return errors.Wrap2(newConn(ctx, netConn, tb.meta, &ConnOptions{
			TTL:    tb.connTTL,
			Parser: tb.prs,
			Logger: tb.log,
		}))
	})
	if err != nil {
		tb.conns.CompareAndDeleteFunc(raddr, func(v *connBucket) bool { return v.Len() == 0 })
		return nil, false, errors.Wrap(err)
	}

	if !found {
		tb.log.LogAttrs(ctx, slog.LevelDebug, "connection tracked", slog.Any("connection", c))
	}

	return c, found, nil
}

func (tb *transpBase[L, B]) untrackConn(ctx context.Context, conn *conn) {
	conns, ok := tb.conns.Load(conn.raddr)
	if !ok {
		return
	}

	if _, ok := conns.LoadAndDelete(conn.laddr); ok {
		conn.Close()
		tb.conns.CompareAndDeleteFunc(conn.raddr, func(v *connBucket) bool { return v.Len() == 0 })

		tb.log.LogAttrs(ctx, slog.LevelDebug, "connection untracked", slog.Any("connection", conn))
	}
}

func (tb *transpBase[L, B]) findConn(raddr, laddr netip.AddrPort, host string) (Conn, bool) {
	if conns, ok := tb.conns.Load(raddr); ok {
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
			for k, c := range conns.All() {
				if !c.isClosed() && c.VerifyHost(host) == nil {
					return c, true
				}

				if c.isClosed() {
					conns.Delete(k)
				}
			}
		}
	}

	return nil, false
}

func (tb *transpBase[L, B]) dialConn(ctx context.Context, raddr netip.AddrPort) (Conn, error) {
	netConn, err := tb.connDialer.DialContext(ctx, tb.meta.Network, raddr.String())
	if err != nil {
		return nil, errors.Wrap(err)
	}

	ctx = context.WithoutCancel(ctx)

	conn, _, err := tb.serveConn(ctx, netConn)
	if err != nil {
		netConn.Close()
		return nil, errors.Wrap(err)
	}

	return conn, nil
}

// SendRequest sends the request to the remote address specified in the req.
//
// Context can be used to cancel the request sending process through the deadline.
// If no deadline is specified on the context, the deadline is set to [SendRequestOptions.Timeout].
//
// The request must have the [header.Via] header with at least one [header.ViaHop] element,
// this element can have zero transport and address fields, they will be filled by the transport.
// In case of reliable transport, Content-Length header will be added automatically if it is missing.
//
// Options are optional, if nil is passed, default options are used (see [SendRequestOptions]).
func (tb *transpBase[L, B]) SendRequest(ctx context.Context, req *OutboundRequestEnvelope, opts *SendRequestOptions) error {
	if tb.isClosing() {
		return errors.Wrap(ErrTransportClosed)
	}

	ctx = ContextWithTransport(ctx, tb.impl)

	sender := InterceptOutboundRequest(
		slices.Collect(util.SeqFilter(tb.outReqInts.All(), func(i OutboundRequestInterceptor) bool { return i != nil })),
		RequestSenderFunc(tb.sendRequest),
	)

	return errors.Wrap(sender.SendRequest(ctx, req, opts))
}

func (tb *transpBase[L, B]) sendRequest(ctx context.Context, req *OutboundRequestEnvelope, opts *SendRequestOptions) error {
	if tb.isClosing() {
		return errors.Wrap(ErrTransportClosed)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, opts.timeout())
		defer cancel()
	}

	req.SetTransport(tb.meta.Proto)

	laddr := req.LocalAddr()

	raddr := req.RemoteAddr()
	if raddr.Port() == 0 {
		raddr = netip.AddrPortFrom(raddr.Addr(), tb.meta.DefaultPort)
	}

	req.SetRemoteAddr(raddr)

	if !raddr.IsValid() {
		return errors.NewInvalidArgumentErrorWrap("invalid remote address %q", raddr)
	}

	acqOpts := &AcquireConnOptions{
		LocalAddr: laddr,
		Dial:      true,
	}
	if u, ok := req.URI().(*uri.SIP); ok && u.Secured {
		acqOpts.Host = u.Addr.Host()
	}

	conn, err := tb.impl.AcquireConn(ctx, raddr, acqOpts)
	if err != nil {
		return errors.Wrap(err)
	}

	ctx = ContextWithConn(ctx, conn)
	laddr = conn.LocalAddr()
	req.SetLocalAddr(laddr)

	req.AccessMessage(func(r *Request) {
		if r == nil || r.Headers == nil {
			return
		}

		if hop, ok := r.Headers.FirstViaHop(); ok {
			hop.Transport = tb.meta.Proto

			hop.Addr = tb.makeSentBy(laddr)
			if branch, ok := hop.Branch(); !ok || !IsRFC3261Branch(branch) {
				if hop.Params == nil {
					hop.Params = make(types.Values)
				}

				hop.Params.Set("branch", tb.genStableBranch(laddr, raddr))
			}
		} else {
			r.Headers.Prepend(header.Via{{
				Proto:     protoVer20,
				Transport: tb.meta.Proto,
				Addr:      tb.makeSentBy(laddr),
				Params:    make(types.Values).Set("branch", tb.genStableBranch(laddr, raddr)),
			}})
		}

		if tb.meta.Streamed() {
			r.Headers.Set(header.ContentLength(len(r.Body)))
		}
	})

	if err := req.Validate(); err != nil {
		return errors.NewInvalidArgumentErrorWrap(err)
	}

	return errors.Wrap(conn.WriteMessage(ctx, req, raddr, opts.rendOpts()))
}

func (tb *transpBase[L, B]) genStableBranch(laddr, raddr netip.AddrPort) string {
	la, ra := laddr.String(), raddr.String()

	b := make([]byte, 0,
		util.SizePrefixedString(tb.meta.Proto)+
			util.SizePrefixedString(la)+
			util.SizePrefixedString(ra),
	)
	b = util.AppendPrefixedString(b, tb.meta.Proto)
	b = util.AppendPrefixedString(b, la)
	b = util.AppendPrefixedString(b, ra)

	return MagicCookie + "." + base64.RawURLEncoding.EncodeToString(b)
}

// SendResponse sends the response to a remote address resolved with steps
// defined in RFC 3261 Section 18.2.2. and RFC 3263 Section 5.
//
// Context can be used to cancel the response sending process through the deadline.
// If no deadline is specified on the context, the deadline is set to [SendResponseOptions.Timeout].
//
// The topmost [header.ViaHop] transport must match the transport protocol.
// In case of reliable transport, Content-Length header will be added automatically if it is missing.
//
// Options are optional, if nil is passed, default options are used (see [SendResponseOptions]).
func (tb *transpBase[L, B]) SendResponse(ctx context.Context, res *OutboundResponseEnvelope, opts *SendResponseOptions) error {
	if tb.isClosing() {
		return errors.Wrap(ErrTransportClosed)
	}

	ctx = ContextWithTransport(ctx, tb.impl)

	sender := InterceptOutboundResponse(
		slices.Collect(util.SeqFilter(tb.outResInts.All(), func(i OutboundResponseInterceptor) bool { return i != nil })),
		ResponseSenderFunc(tb.sendResponse),
	)

	return errors.Wrap(sender.SendResponse(ctx, res, opts))
}

//nolint:gocognit
func (tb *transpBase[L, B]) sendResponse(ctx context.Context, res *OutboundResponseEnvelope, opts *SendResponseOptions) error {
	if tb.isClosing() {
		return errors.Wrap(ErrTransportClosed)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, opts.timeout())
		defer cancel()
	}

	res.SetTransport(tb.meta.Proto)

	var via header.ViaHop
	res.AccessMessage(func(r *Response) {
		if r == nil || r.Headers == nil {
			return
		}

		if hop, ok := r.Headers.FirstViaHop(); ok {
			via = hop.Clone()
		}

		if to, ok := r.Headers.To(); ok {
			if tag, ok := to.Tag(); !ok || tag == "" {
				if to.Params == nil {
					to.Params = make(types.Values)
				}

				to.Params.Set("tag", genStableResTag(r.Headers))
			}
		}

		if tb.meta.Streamed() {
			r.Headers.Set(header.ContentLength(len(r.Body)))
		}
	})

	if err := res.Validate(); err != nil {
		return errors.NewInvalidArgumentErrorWrap(err)
	}

	if !via.Transport.Equal(tb.meta.Proto) {
		return errors.NewInvalidArgumentErrorWrap("unexpected Via transport %q", via.Transport)
	}

	conn, err := tb.impl.AcquireConn(ctx, res.RemoteAddr(), &AcquireConnOptions{
		LocalAddr: res.LocalAddr(),
		// TODO: how to verify host of response on secured transport?
		// Host: ...,
	})
	if err == nil {
		ctx = ContextWithConn(ctx, conn)
		res.SetLocalAddr(conn.LocalAddr())

		if conn.RemoteAddr().IsValid() {
			// connected socket or conn-oriented transport
			if err = conn.WriteMessage(ctx, res, res.RemoteAddr(), opts.rendOpts()); err == nil {
				return nil
			}
		}
	}

	if errors.Is(err, ErrTransportClosed) || errors.Is(err, ctx.Err()) {
		return errors.Wrap(err)
	}

	// fallback to RFC 3261 Section 18.2.2. and RFC 3263 Section 5
	var errs []error
	for _, raddr := range tb.rmtClnLctr.LookupResponseAddrs(ctx, via, &singleTransportMetadataProvider{tb.meta}) {
		var err error

		curCtx := ctx

		res.SetRemoteAddr(raddr)

		if conn == nil || conn.RemoteAddr().IsValid() && conn.RemoteAddr() != raddr {
			if conn, err = tb.impl.AcquireConn(curCtx, raddr, &AcquireConnOptions{
				LocalAddr: res.LocalAddr(),
				Dial:      true,
				// TODO: how to verify host of response on secured transport?
				// Host: ...,
			}); err != nil {
				if errors.Is(err, ErrTransportClosed) || errors.Is(err, curCtx.Err()) {
					return errors.Wrap(err)
				}

				errs = append(errs, errors.Errorf("acquire connection to %q: %w", raddr, err))

				continue
			}

			curCtx = ContextWithConn(curCtx, conn)
			res.SetLocalAddr(conn.LocalAddr())
		}

		err = conn.WriteMessage(curCtx, res, res.RemoteAddr(), opts.rendOpts())
		if err == nil || errors.Is(err, ErrTransportClosed) || errors.Is(err, curCtx.Err()) {
			return errors.Wrap(err)
		}

		errs = append(errs, errors.Errorf("write response to %q: %w", raddr, err))
	}

	if len(errs) == 0 {
		return errors.Wrap(ErrNoDestAddressResolved)
	}

	return errors.JoinPrefixWrap("send response errors:", errs...)
}

func (tb *transpBase[L, B]) Respond(ctx context.Context, req *InboundRequestEnvelope, sts ResponseStatus, opts *RespondOptions) error {
	if tb.isClosing() {
		return errors.Wrap(ErrTransportClosed)
	}
	return errors.Wrap(respondStateless(ctx, tb, req, sts, opts))
}

func respondStateless(
	ctx context.Context,
	sndr ResponseSender,
	req *InboundRequestEnvelope,
	sts ResponseStatus,
	opts *RespondOptions,
) error {
	resOpts := opts.resOpts()
	if resOpts == nil {
		resOpts = &ResponseOptions{}
	}

	if resOpts.LocalTag == "" {
		resOpts.LocalTag = genStableResTag(req.Headers())
	}

	res, err := req.NewResponse(sts, resOpts)
	if err != nil {
		return errors.Wrap(err)
	}

	return errors.Wrap(sndr.SendResponse(ctx, res, opts.sendOpts()))
}

func genStableResTag(hdrs Headers) string {
	if hdrs == nil {
		return ""
	}

	callID, _ := hdrs.CallID()

	var fromTag string
	if from, ok := hdrs.From(); ok && from != nil {
		if t, ok := from.Tag(); ok {
			fromTag = t
		}
	}

	buf := make([]byte, 0, len(callID)+len(fromTag))
	buf = append(buf, callID...)
	buf = append(buf, fromTag...)
	sum := sha256.Sum256(buf)

	return hex.EncodeToString(sum[:8])
}

func (tb *transpBase[L, B]) readMsgs(ctx context.Context, msgs iter.Seq2[Message, error], stopOnPanic bool) error {
	for msg, err := range msgs {
		var perr *ParseError
		if err != nil {
			var ok bool
			if perr, ok = errors.AsType[*ParseError](err); !ok {
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
			case *InboundRequestEnvelope:
				if err := tb.recvReqSafe(ctx, msg, errors.Wrap(err)); err != nil && stopOnPanic {
					// stop on panics only if not listener connection
					return errors.Wrap(err)
				}
			case *InboundResponseEnvelope:
				if err := tb.recvResSafe(ctx, msg, errors.Wrap(err)); err != nil && stopOnPanic {
					// stop on panics only if not listener connection
					return errors.Wrap(err)
				}
			}
		}

		if perr != nil && tb.meta.Streamed() {
			// stop on broken stream
			return errors.PrefixWrap(ErrBrokenConnStream, err)
		}
	}

	return nil
}

func (tb *transpBase[L, B]) recvReqSafe(ctx context.Context, req *InboundRequestEnvelope, err error) (finErr error) {
	defer func() {
		if pe := recover(); pe != nil {
			tb.log.LogAttrs(ctx, slog.LevelError, "panic occurred while processing the inbound request",
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
						tb.log.LogAttrs(ctx, slog.LevelError, "panic occurred while processing of the previous panic error",
							slog.Any("request", req),
							slog.Any("error", pe),
							slog.Any("stack", log.StringValue(debug.Stack())),
						)
					}
				}()

				tb.respondOrDiscard(ctx, req, ResponseStatusServerInternalError)
			}()
		}
	}()

	if err := tb.recvReq(ctx, req, errors.Wrap(err)); err != nil {
		sts := ResponseStatusServerInternalError

		lvl := slog.LevelWarn
		if e, ok := errors.AsType[RejectRequestError](err); ok && e != nil {
			sts = e.ResponseStatus()
			lvl = e.LogLevel()
		}

		tb.log.LogAttrs(ctx, lvl, "rejecting the inbound request due to error",
			slog.Any("request", req),
			slog.Any("error", err),
		)

		tb.respondOrDiscard(ctx, req, sts)
	}

	return nil
}

func (tb *transpBase[L, B]) recvReq(ctx context.Context, req *InboundRequestEnvelope, err error) error {
	// try to setup Via params even first to allow correct response routing in case of any failure
	if via, ok := req.msg.Headers.FirstViaHop(); ok && via != nil && via.IsValid() {
		raddr := req.RemoteAddr()
		// RFC 3261 Section 18.2.1.
		if via.Addr.IP() == nil || !via.Addr.IP().Equal(raddr.Addr().AsSlice()) {
			if via.Params == nil {
				via.Params = make(Values)
			}

			via.Params.Set("received", raddr.Addr().String())
		}
		// RFC 3581 Section 4.
		if via.Params.Has("rport") {
			via.Params.Set("rport", strconv.Itoa(int(raddr.Port())))
		}
	}

	if err != nil {
		// we faced some errors during parsing of the request,
		// but if the parsed request contains mandatory headers,
		// then we can respond to it with a proper error response.
		var sts ResponseStatus
		switch {
		case errors.Is(err, ErrEntityTooLarge):
			sts = ResponseStatusRequestEntityTooLarge
		case errors.Is(err, ErrMessageTooLarge):
			sts = ResponseStatusMessageTooLarge
		case errors.Is(err, ErrInvalidMessage) || errors.IsGrammarErr(err):
			sts = ResponseStatusBadRequest
		default:
			sts = ResponseStatusServerInternalError
		}

		return errors.Wrap(newRejectReqErr(err, sts, slog.LevelDebug))
	}

	if err := req.Validate(); err != nil {
		return errors.Wrap(newRejectReqErr(err, ResponseStatusBadRequest, slog.LevelDebug))
	}

	receiver := InterceptInboundRequest(
		slices.Collect(util.SeqFilter(tb.inReqInts.All(), func(i InboundRequestInterceptor) bool { return i != nil })),
		RequestReceiverFunc(func(ctx context.Context, req *InboundRequestEnvelope) error {
			return errors.Wrap(newRejectReqErr(ErrUnhandledMessage, ResponseStatusServiceUnavailable, slog.LevelWarn))
		}),
	)

	return errors.Wrap(receiver.RecvRequest(ctx, req))
}

func (tb *transpBase[L, B]) respondOrDiscard(ctx context.Context, req *InboundRequestEnvelope, sts ResponseStatus) {
	if err := tb.Respond(ctx, req, sts, nil); err != nil {
		lvl := slog.LevelWarn
		if errors.Is(err, ErrInvalidArgument) || errors.Is(err, ErrInvalidMessage) {
			lvl = slog.LevelDebug
		}

		tb.log.LogAttrs(ctx, lvl, "silently discard the inbound request due to respond failure",
			slog.Any("request", req),
			slog.Any("error", err),
		)
	}
}

func (tb *transpBase[L, B]) recvResSafe(ctx context.Context, res *InboundResponseEnvelope, err error) (finErr error) {
	defer func() {
		if pe := recover(); pe != nil {
			tb.log.LogAttrs(ctx, slog.LevelError, "panic occurred while processing the inbound response",
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
		lvl := slog.LevelWarn
		if e, ok := errors.AsType[RejectResponseError](err); ok && e != nil {
			lvl = e.LogLevel()
		}

		tb.log.LogAttrs(ctx, lvl, "silently discard the inbound response due to error",
			slog.Any("response", res),
			slog.Any("error", err),
		)
	}

	return nil
}

func (tb *transpBase[L, B]) recvRes(ctx context.Context, res *InboundResponseEnvelope, err error) error {
	if err != nil {
		return errors.Wrap(newRejectResErr(err, slog.LevelDebug))
	}

	if err := res.Validate(); err != nil {
		return errors.Wrap(newRejectResErr(err, slog.LevelDebug))
	}

	// RFC 3261 Section 18.1.2.
	via, _ := res.msg.Headers.FirstViaHop()
	if !tb.matchSentBy(via.Addr, res.LocalAddr()) {
		return errors.Wrap(newRejectResErr(errors.Errorf("Via sent-by address %q not matched", via.Addr), slog.LevelDebug))
	}

	receiver := InterceptInboundResponse(
		slices.Collect(util.SeqFilter(tb.inResInts.All(), func(i InboundResponseInterceptor) bool { return i != nil })),
		ResponseReceiverFunc(func(ctx context.Context, res *InboundResponseEnvelope) error {
			return errors.Wrap(newRejectResErr(ErrUnhandledMessage, slog.LevelWarn))
		}),
	)

	return errors.Wrap(receiver.RecvResponse(ctx, res))
}

type RejectRequestError interface {
	error
	ResponseStatus() ResponseStatus
	LogLevel() slog.Level
}

type rejectRequestError struct {
	err error
	sts ResponseStatus
	lvl slog.Level
}

func newRejectReqErr(err error, sts ResponseStatus, lvl slog.Level) error {
	if sts == 0 {
		sts = ResponseStatusServerInternalError
	}
	return &rejectRequestError{err, sts, lvl}
}

func (e *rejectRequestError) Error() string {
	if e == nil || e.err == nil {
		return sNilTag
	}
	return e.err.Error()
}

func (e *rejectRequestError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *rejectRequestError) ResponseStatus() ResponseStatus {
	if e == nil {
		return 0
	}
	return e.sts
}

func (e *rejectRequestError) LogLevel() slog.Level {
	if e == nil {
		return 0
	}
	return e.lvl
}

type RejectResponseError interface {
	error
	LogLevel() slog.Level
}

type rejectResponseError struct {
	err error
	lvl slog.Level
}

func newRejectResErr(err error, lvl slog.Level) error {
	return &rejectResponseError{err, lvl}
}

func (e *rejectResponseError) Error() string {
	if e == nil || e.err == nil {
		return sNilTag
	}
	return e.err.Error()
}

func (e *rejectResponseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *rejectResponseError) LogLevel() slog.Level {
	if e == nil {
		return 0
	}
	return e.lvl
}

type ConnlessTransport struct {
	transpBase[*packetConn, net.PacketConn]
	lisCfg PacketListenConfig
}

type ConnlessTransportOptions struct {
	TransportOptions
	// PacketListenConfig is used to init new [net.PacketConn] listener.
	// If nil, [net.ListenConfig] is used.
	PacketListenConfig PacketListenConfig
}

func (o *ConnlessTransportOptions) tpOpts() *TransportOptions {
	if o == nil {
		return nil
	}
	return &o.TransportOptions
}

func (o *ConnlessTransportOptions) pktLisConf() PacketListenConfig {
	if o == nil || o.PacketListenConfig == nil {
		return &net.ListenConfig{}
	}
	return o.PacketListenConfig
}

type PacketListenConfig interface {
	ListenPacket(ctx context.Context, nt, addr string) (net.PacketConn, error)
}

type PacketListenConfigFunc func(ctx context.Context, nt, addr string) (net.PacketConn, error)

func (f PacketListenConfigFunc) ListenPacket(ctx context.Context, nt, addr string) (net.PacketConn, error) {
	return errors.Wrap2(f(ctx, nt, addr))
}

func NewConnlessTransport(meta TransportMetadata, opts *ConnlessTransportOptions) (*ConnlessTransport, error) {
	if !meta.IsValid() {
		return nil, errors.NewInvalidArgumentErrorWrap("invalid metadata %q", meta)
	}

	meta.Flags &^= TransportFlagReliable | TransportFlagStreamed

	tp := new(ConnlessTransport)
	tp.init(tp, meta, opts.tpOpts())
	tp.lisCfg = opts.pktLisConf()

	return tp, nil
}

func (tp *ConnlessTransport) Metadata() TransportMetadata {
	if tp == nil {
		return TransportMetadata{}
	}
	return tp.transpBase.Metadata()
}

func (tp *ConnlessTransport) LogValue() slog.Value {
	if tp == nil {
		return slog.Value{}
	}
	return tp.transpBase.LogValue()
}

func (tp *ConnlessTransport) Logger() *slog.Logger {
	if tp == nil {
		return nil
	}
	return tp.transpBase.Logger()
}

func (tp *ConnlessTransport) Close() error {
	if tp == nil {
		return nil
	}
	return errors.Wrap(tp.transpBase.Close())
}

func (tp *ConnlessTransport) newListener(ctx context.Context, netLis net.PacketConn) (*packetConn, error) {
	return errors.Wrap2(newPacketConn(ctx, netLis, tp.meta, &ConnOptions{
		Parser: tp.prs,
		Logger: tp.log,
	}))
}

// ServeListener starts reading loop for the unconnected packet connection.
// Valid inbound messages will routed to appropriate message interceptor.
// ServeListener doesn't close the connection on return, it is responsibility of the caller.
//
// Context is passed to inbound message interceptors and can be used to cancel serving of the connection.
func (tp *ConnlessTransport) ServeListener(ctx context.Context, netLis net.PacketConn) error {
	if netLis == nil {
		return errors.NewInvalidArgumentErrorWrap("nil listener")
	}

	ls, found, err := tp.trackListener(ctx, netLis)
	if err != nil {
		return errors.Wrap(err)
	}

	if found {
		return errors.Wrap(ErrListenerTracked)
	}

	defer tp.untrackListener(ctx, ls)

	ctx = ContextWithTransport(ctx, tp)
	ctx = ContextWithConn(ctx, ls)

	go func() {
		select {
		case <-ctx.Done():
			ls.Close()
		case <-tp.closing:
		}
	}()

	err = tp.readMsgs(ctx, ls.Messages(ctx), false)
	select {
	case <-ctx.Done():
		if err == nil {
			err = ctx.Err()
		} else if !errors.Is(err, ctx.Err()) {
			err = errors.Errorf("%w: %w", err, ctx.Err())
		}
	case <-tp.closing:
		if err == nil {
			err = ErrTransportClosed
		} else if !errors.Is(err, ErrTransportClosed) {
			err = errors.Errorf("%w: %w", err, ErrTransportClosed)
		}
	default:
	}

	return errors.Wrap(err)
}

func (tp *ConnlessTransport) ListenAndServe(ctx context.Context, addr string) error {
	ls, err := tp.lisCfg.ListenPacket(ctx, tp.meta.Network, addr)
	if err != nil {
		return errors.Wrap(err)
	}

	return errors.Wrap(tp.ServeListener(ctx, ls))
}

func (tp *ConnlessTransport) AcquireConn(ctx context.Context, raddr netip.AddrPort, opts *AcquireConnOptions) (Conn, error) {
	if tp.isClosing() {
		return nil, errors.Wrap(ErrTransportClosed)
	}

	laddr := opts.locAddr()

	// first try to search connected connection
	if conn, found := tp.findConn(raddr, laddr, opts.host()); found {
		return conn, nil
	}

	// fallback to listener
	if l, ok := tp.liss.Load(laddr); ok {
		return l, nil
	}

	for k, l := range tp.liss.All() {
		if !l.isClosed() && (!laddr.IsValid() ||
			laddr.Addr().Is4() && l.laddr.Addr().Is4() ||
			laddr.Addr().Is6() && l.laddr.Addr().Is6()) {
			return l, nil
		}

		if l.isClosed() {
			tp.liss.Delete(k)
		}
	}

	if !opts.dial() {
		return nil, errors.Wrap(ErrConnNotFound)
	}

	return errors.Wrap2(tp.dialConn(ctx, raddr))
}

type ConnOrientedTransport struct {
	transpBase[*connListener, net.Listener]
	lisCfg ConnListenConfig
}

type ConnOrientedTransportOptions struct {
	TransportOptions
	// ConnListenConfig is used to init new [net.Listener] listener.
	// If nil, [net.ListenConfig] is used.
	ConnListenConfig ConnListenConfig
}

func (o *ConnOrientedTransportOptions) tpOpts() *TransportOptions {
	if o == nil {
		return nil
	}
	return &o.TransportOptions
}

func (o *ConnOrientedTransportOptions) connLisConf() ConnListenConfig {
	if o == nil || o.ConnListenConfig == nil {
		return &net.ListenConfig{}
	}
	return o.ConnListenConfig
}

type ConnListenConfig interface {
	Listen(ctx context.Context, nt, addr string) (net.Listener, error)
}

type ConnListenConfigFunc func(ctx context.Context, nt, addr string) (net.Listener, error)

func (f ConnListenConfigFunc) Listen(ctx context.Context, nt, addr string) (net.Listener, error) {
	return errors.Wrap2(f(ctx, nt, addr))
}

func NewConnOrientedTransport(meta TransportMetadata, opts *ConnOrientedTransportOptions) (*ConnOrientedTransport, error) {
	if !meta.IsValid() {
		return nil, errors.NewInvalidArgumentErrorWrap("invalid metadata %q", meta)
	}

	tp := new(ConnOrientedTransport)
	tp.init(tp, meta, opts.tpOpts())
	tp.lisCfg = opts.connLisConf()

	return tp, nil
}

func (tp *ConnOrientedTransport) Metadata() TransportMetadata {
	if tp == nil {
		return TransportMetadata{}
	}
	return tp.transpBase.Metadata()
}

func (tp *ConnOrientedTransport) LogValue() slog.Value {
	if tp == nil {
		return slog.Value{}
	}
	return tp.transpBase.LogValue()
}

func (tp *ConnOrientedTransport) Logger() *slog.Logger {
	if tp == nil {
		return nil
	}
	return tp.transpBase.Logger()
}

func (tp *ConnOrientedTransport) Close() error {
	if tp == nil {
		return nil
	}
	return errors.Wrap(tp.transpBase.Close())
}

func (tp *ConnOrientedTransport) newListener(ctx context.Context, netLis net.Listener) (*connListener, error) {
	return newConnListener(ctx, netLis, tp.log), nil
}

//nolint:gocognit
func (tp *ConnOrientedTransport) ServeListener(ctx context.Context, netLis net.Listener) error {
	if netLis == nil {
		return errors.NewInvalidArgumentErrorWrap("nil listener")
	}

	ls, found, err := tp.trackListener(ctx, netLis)
	if err != nil {
		return errors.Wrap(err)
	}

	if found {
		return errors.Wrap(ErrListenerTracked)
	}

	defer tp.untrackListener(ctx, ls)

	ctx = ContextWithTransport(ctx, tp)

	go func() {
		select {
		case <-ctx.Done():
			ls.Close()
		case <-tp.closing:
		}
	}()

	var (
		accDelay    time.Duration
		accDelayTmr *time.Timer
	)
	defer func() {
		if accDelayTmr != nil {
			accDelayTmr.Stop()
		}
	}()

	for {
		netConn, err := ls.Accept()
		if err != nil {
			if errors.IsTemporaryErr(err) {
				if accDelay == 0 {
					accDelay = 5 * time.Millisecond
				} else {
					accDelay *= 2
				}

				if v := time.Minute; accDelay > v {
					accDelay = v
				}

				tp.log.LogAttrs(ctx, slog.LevelDebug,
					"failed to accept connection due to the temporary error, continue accepting after delay...",
					slog.Any("error", err),
					slog.Duration("delay", accDelay),
					slog.Any("local_addr", ls.addr),
				)

				if accDelayTmr == nil {
					accDelayTmr = time.NewTimer(accDelay)
				} else {
					accDelayTmr.Reset(accDelay)
				}

				select {
				case <-tp.closing:
					return errors.Wrap(net.ErrClosed)
				case <-ctx.Done():
					return errors.Wrap(ctx.Err())
				case <-accDelayTmr.C:
					continue
				}
			}

			select {
			case <-ctx.Done():
				if !errors.Is(err, ctx.Err()) {
					err = errors.Errorf("%w: %w", err, ctx.Err())
				}
			case <-tp.closing:
				if !errors.Is(err, ErrTransportClosed) {
					err = errors.Errorf("%w: %w", err, ErrTransportClosed)
				}
			default:
			}

			return errors.Wrap(err)
		}

		accDelay = 0

		if accDelayTmr != nil {
			accDelayTmr.Stop()
		}

		conn, done, err := tp.serveConn(ctx, netConn)
		if err != nil {
			tp.log.LogAttrs(ctx, slog.LevelWarn, "failed to start serving the inbound connection",
				slog.Any("error", err),
				slog.Any("connection", netConn),
			)
			netConn.Close()

			continue
		}

		if done == nil {
			netConn.Close()
			tp.log.LogAttrs(ctx, slog.LevelDebug, "connection already tracked, reuse existing handler", slog.Any("connection", conn))
		}
	}
}

func (tp *ConnOrientedTransport) ListenAndServe(ctx context.Context, addr string) error {
	ls, err := tp.lisCfg.Listen(ctx, tp.meta.Network, addr)
	if err != nil {
		return errors.Wrap(err)
	}

	return errors.Wrap(tp.ServeListener(ctx, ls))
}

func (tp *ConnOrientedTransport) AcquireConn(ctx context.Context, raddr netip.AddrPort, opts *AcquireConnOptions) (Conn, error) {
	if tp.isClosing() {
		return nil, errors.Wrap(ErrTransportClosed)
	}

	// first try to search connected connection
	if conn, found := tp.findConn(raddr, opts.locAddr(), opts.host()); found {
		return conn, nil
	}

	if !opts.dial() {
		return nil, errors.Wrap(ErrConnNotFound)
	}

	return errors.Wrap2(tp.dialConn(ctx, raddr))
}
