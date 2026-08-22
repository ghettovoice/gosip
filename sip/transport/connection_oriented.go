package transport

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/netutil"
	"github.com/ghettovoice/gosip/internal/timeutil"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

type ConnectionOrientedListener struct {
	baseLis  net.Listener
	tp       *ConnectionOrientedTransport
	addr     netip.AddrPort
	log      *slog.Logger
	connOpts ConnectionOptions

	closeOnce sync.Once
	closeErr  error
	closed    chan struct{}
	serving   atomic.Bool
}

var _ sip.TransportListener = (*ConnectionOrientedListener)(nil)

type ConnectionOrientedListenerOptions = ConnectionOptions

func NewConnectionOrientedListener(
	ctx context.Context,
	base net.Listener,
	tp *ConnectionOrientedTransport,
	opts ...ConnectionOrientedListenerOptions,
) (*ConnectionOrientedListener, error) {
	if base == nil {
		return nil, errors.ErrorWrap("nil listener")
	}

	lsOpts := util.LastSliceElemOr(opts, ConnectionOrientedListenerOptions{})

	ls := &ConnectionOrientedListener{
		tp:     tp,
		addr:   netutil.UnmapAddrPort(netip.MustParseAddrPort(base.Addr().String())),
		closed: make(chan struct{}),
	}
	ls.log = lsOpts.log().With(slog.Any("listener", ls))
	ls.baseLis = netutil.WrapListener([]netutil.ListenerDecorator{
		netutil.NewLogListenerDecorator(ls.log, slog.LevelDebug),
		netutil.NewCloseOnceListenerDecorator(),
	}...)(ctx, base)

	ls.connOpts = lsOpts
	ls.connOpts.Logger = ls.log
	return ls, nil
}

func (ls *ConnectionOrientedListener) Metadata() sip.TransportMetadata { return ls.tp.meta }
func (ls *ConnectionOrientedListener) LocalAddr() netip.AddrPort       { return ls.addr }

func (ls *ConnectionOrientedListener) LogValue() slog.Value {
	if ls == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.String("ptr", fmt.Sprintf("%p", ls)),
		slog.Any("proto", ls.tp.meta.Proto),
		slog.Any("network", ls.tp.meta.Network),
		slog.Any("local_addr", ls.addr),
	)
}

func (ls *ConnectionOrientedListener) String() string {
	if ls == nil {
		return "<nil>"
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	sb.WriteString(ls.tp.meta.Network)
	sb.WriteRune(':')
	sb.WriteString(ls.addr.String())
	return sb.String()
}

func (ls *ConnectionOrientedListener) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		f.Write([]byte(ls.String()))
		return
	case 'q':
		f.Write([]byte(strconv.Quote(ls.String())))
		return
	default:
		type (
			hideMethods                ConnectionOrientedListener
			ConnectionOrientedListener hideMethods
		)
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*ConnectionOrientedListener)(ls))
		return
	}
}

func (ls *ConnectionOrientedListener) Accept(ctx context.Context) (*Connection, error) {
	netConn, err := ls.baseLis.Accept()
	if err != nil {
		if errors.Is(err, net.ErrClosed) {
			err = errors.Errorf("%w: %w", err, ErrNetworkClosed)
		}
		return nil, errors.Wrap(err)
	}

	conn, done, err := ls.tp.serveConn(ctx, netConn, false)
	if err != nil {
		netConn.Close()
		return nil, errors.Wrap(err)
	}

	if done == nil {
		netConn.Close()
		return nil, errors.Wrap(ErrConnectionTracked)
	}

	return conn.Connection, nil
}

func (ls *ConnectionOrientedListener) isClosed() bool {
	select {
	case <-ls.closed:
		return true
	default:
		return false
	}
}

func (ls *ConnectionOrientedListener) Close(_ context.Context) error {
	ls.closeOnce.Do(func() {
		ls.closeErr = ls.baseLis.Close()
		if ls.closeErr != nil && errors.Is(ls.closeErr, net.ErrClosed) {
			ls.closeErr = errors.Errorf("%w: %w", ls.closeErr, ErrNetworkClosed)
		}
		close(ls.closed)
	})
	return errors.Wrap(ls.closeErr)
}

func (ls *ConnectionOrientedListener) Serve(ctx context.Context) error {
	if ls.isClosed() {
		return errors.Wrap(ErrNetworkClosed)
	}
	if !ls.serving.CompareAndSwap(false, true) {
		return errors.Wrap(ErrListenerServing)
	}
	defer ls.serving.Store(false)

	trLs, _ := ls.tp.lisMap.Load(ls.addr)
	if trLs != nil {
		defer ls.tp.untrackListener(ctx, trLs)
	}

	go func() {
		select {
		case <-ctx.Done():
			if trLs != nil && trLs.borrowed {
				return
			}

			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()

			if err := ls.Close(ctx); err != nil {
				ls.log.LogAttrs(ctx, slog.LevelWarn, "failed to close listener",
					slog.Any("listener", ls),
					slog.Any("error", err),
				)
			}
		case <-ls.closed:
		}
	}()

	err := ls.serve(ctx)

	select {
	case <-ctx.Done():
		if !errors.Is(err, ctx.Err()) {
			err = errors.Errorf("%w: %w", err, ctx.Err())
		}
		return errors.Wrap(err)
	default:
	}

	select {
	case <-ls.closed:
		if !errors.Is(err, ErrNetworkClosed) {
			err = errors.Errorf("%w: %w", err, ErrNetworkClosed)
		}
		return errors.Wrap(err)
	default:
	}

	return errors.Wrap(err)
}

func (ls *ConnectionOrientedListener) serve(ctx context.Context) error {
	var (
		accDelay    time.Duration
		accDelayTmr *time.Timer
	)
	defer func() {
		if accDelayTmr != nil {
			accDelayTmr.Stop()
		}
	}()

	resetAccDelay := func() {
		accDelay = 0

		if accDelayTmr != nil {
			accDelayTmr.Stop()
		}
	}

	for {
		if _, err := ls.Accept(ctx); err != nil {
			if errors.Is(err, ErrConnectionTracked) {
				resetAccDelay()
				continue
			}

			if !sip.IsTemporaryError(err) {
				return errors.Wrap(err)
			}

			if accDelay == 0 {
				accDelay = 5 * time.Millisecond
			} else {
				accDelay *= 2
			}
			if v := time.Minute; accDelay > v {
				accDelay = v
			}

			ls.log.LogAttrs(ctx, slog.LevelDebug,
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
			case <-ls.closed:
				return errors.Wrap(ErrNetworkClosed)
			case <-ctx.Done():
				return errors.Wrap(ctx.Err())
			case <-accDelayTmr.C:
				resetAccDelay()
				continue
			}
		}

		resetAccDelay()
	}
}

type netListenerAdapter struct {
	*ConnectionOrientedListener
}

var _ net.Listener = (*netListenerAdapter)(nil)

func (l *netListenerAdapter) Addr() net.Addr {
	return netutil.AddrPortToNetAddr(l.Metadata().Network, l.addr)
}

func (l *netListenerAdapter) Accept() (net.Conn, error) {
	conn, err := l.ConnectionOrientedListener.Accept(context.Background())
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return conn.AsNetConn(), nil
}

func (l *netListenerAdapter) Close() error {
	return errors.Wrap(l.ConnectionOrientedListener.Close(context.Background()))
}

func (l *netListenerAdapter) Unwrap() net.Listener {
	if l == nil {
		return nil
	}
	return l.baseLis
}

func (ls *ConnectionOrientedListener) AsNetListener() net.Listener {
	if ls == nil {
		return nil
	}
	return &netListenerAdapter{ls}
}

type Connection struct {
	connBase
	origConn net.Conn
	ctxConn  netutil.ContextConn
	idleTmr  *timeutil.InactivityTimer
}

func NewConnection(
	ctx context.Context,
	base net.Conn,
	meta sip.TransportMetadata,
	opts ...ConnectionOptions,
) (*Connection, error) {
	if base == nil {
		return nil, errors.ErrorWrap("nil connection")
	}
	if base.RemoteAddr() == nil {
		return nil, errors.ErrorWrap("unconnected socket not allowed here")
	}
	if !meta.IsValid() {
		return nil, errors.ErrorfWrap("invalid metadata %+v", meta)
	}
	if v1, v2 := meta.Network, base.LocalAddr().Network(); !util.EqFold(v1, v2) {
		return nil, errors.ErrorfWrap("metadata and connection networks mismatch: %q != %q", v1, v2)
	}

	connOpts := util.LastSliceElemOr(opts, ConnectionOptions{})

	c := &Connection{
		connBase: connBase{
			meta:            meta.Canonic(),
			laddr:           netutil.UnmapAddrPort(netip.MustParseAddrPort(base.LocalAddr().String())),
			raddr:           netutil.UnmapAddrPort(netip.MustParseAddrPort(base.RemoteAddr().String())),
			prsr:            connOpts.prsr(),
			maxMsgReadSize:  connOpts.maxMsgReadSize(),
			maxMsgWriteSize: connOpts.maxMsgWriteSize(),
			readTimeout:     connOpts.readTimeout(),
			writeTimeout:    connOpts.writeTimeout(),
			idleTimeout:     connOpts.idleTimeout(),
			closed:          make(chan struct{}),
		},
		origConn: base,
	}
	c.log = connOpts.log().With(slog.Any("connection", c))
	//nolint:forcetypeassert
	c.ctxConn = netutil.WrapConn([]netutil.ConnDecorator{
		netutil.NewLogConnDecorator(c.log, slog.LevelDebug),
		netutil.NewCloseOnceConnDecorator(),
		netutil.NewContextConnDecorator(),
	}...)(ctx, base).(netutil.ContextConn)
	c.idleTmr = timeutil.NewInactivityTimer(c.idleTimeout, func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()

		if err := c.Close(closeCtx); err != nil {
			c.log.LogAttrs(closeCtx, slog.LevelWarn, "failed to close connection",
				slog.Any("connection", c),
				slog.Any("error", err),
			)
		}
	})
	c.resetIdleTmr()
	return c, nil
}

func (c *Connection) resetIdleTmr() {
	if c.idleTmr != nil {
		c.idleTmr.Reset()
	}
}

func (c *Connection) LogValue() slog.Value {
	if c == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.String("ptr", fmt.Sprintf("%p", c)),
		slog.Any("proto", c.meta.Proto),
		slog.Any("network", c.meta.Network),
		slog.Any("local_addr", c.laddr),
		slog.Any("remote_addr", c.raddr),
	)
}

func (c *Connection) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		f.Write([]byte(c.String()))
		return
	case 'q':
		f.Write([]byte(strconv.Quote(c.String())))
		return
	default:
		type (
			hideMethods Connection
			Connection  hideMethods
		)
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*Connection)(c))
		return
	}
}

func (c *Connection) Close(_ context.Context) error {
	c.closeOnce.Do(func() {
		c.closeErr = c.ctxConn.Close()
		if c.closeErr != nil && errors.Is(c.closeErr, net.ErrClosed) {
			c.closeErr = errors.Errorf("%w: %w", c.closeErr, ErrNetworkClosed)
		}
		close(c.closed)
	})
	if c.idleTmr != nil {
		c.idleTmr.Stop()
	}
	return errors.Wrap(c.closeErr)
}

func (c *Connection) Write(ctx context.Context, buf []byte) (int, error) {
	if c.isClosed() {
		return 0, errors.Wrap(ErrNetworkClosed)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.writeTimeout)
		defer cancel()
	}

	n, err := c.ctxConn.WriteContext(ctx, buf)
	if err != nil {
		if errors.Is(err, net.ErrClosed) {
			err = errors.Errorf("%w: %w", err, ErrNetworkClosed)
		}
		return 0, errors.Wrap(err)
	}
	c.resetIdleTmr()
	return n, nil
}

func (c *Connection) Read(ctx context.Context, buf []byte) (int, error) {
	if c.isClosed() {
		return 0, errors.Wrap(ErrNetworkClosed)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.readTimeout)
		defer cancel()
	}

	n, err := c.ctxConn.ReadContext(ctx, buf)
	if err != nil {
		if errors.Is(err, net.ErrClosed) {
			err = errors.Errorf("%w: %w", err, ErrNetworkClosed)
		}
		return 0, errors.Wrap(err)
	}
	return n, nil
}

func (c *Connection) Messages(ctx context.Context) iter.Seq2[sip.Message, error] {
	var msgs iter.Seq2[sip.Message, error]
	if c.meta.Streamed() {
		msgs = c.streamMsgs(ctx, c, c.recvKeepAlive)
	} else {
		msgs = c.packetMsgs(ctx, &streamToPacketAdapter{c},
			func(ctx context.Context, kaType uint8, raddr netip.AddrPort) {
				c.recvKeepAlive(ctx, kaType)
			},
		)
	}

	return func(yield func(sip.Message, error) bool) {
		for msg, err := range msgs {
			if msg != nil {
				c.resetIdleTmr()
			}

			if !yield(msg, errors.Wrap(err)) {
				break
			}
		}
	}
}

func (c *Connection) recvKeepAlive(ctx context.Context, kaType uint8) {
	if c.isClosed() {
		return
	}

	switch kaType {
	case kaPingCRLF:
		c.resetIdleTmr()

		if _, err := c.Write(ctx, crlf); err != nil {
			c.log.LogAttrs(ctx, slog.LevelWarn, "failed to send CRLF pong", slog.Any("error", err))
		}
		// TODO: fire event or callback for upper layer
	case kaPongCRLF:
		c.resetIdleTmr()
		// TODO: confirm running ping
	}
}

func (c *Connection) WriteMessage(
	ctx context.Context,
	msg sip.Message,
	addr netip.AddrPort,
	opts ...sip.RenderOptions,
) error {
	if c.isClosed() {
		return errors.Wrap(ErrNetworkClosed)
	}

	addr = netutil.UnmapAddrPort(addr)
	if addr.IsValid() && addr != c.raddr {
		// TODO: add sentinel or classified error
		return errors.ErrorfWrap("can't write to %q", addr)
	}

	if err := msg.Validate(); err != nil {
		return errors.Wrap(err)
	}

	bb := util.GetBytesBuffer()
	defer util.FreeBytesBuffer(bb)

	if _, err := msg.RenderTo(bb, opts...); err != nil {
		return errors.Wrap(err)
	}
	if uint(bb.Len()) > c.maxMsgWriteSize {
		return errors.Wrap(sip.ErrMessageTooLarge)
	}

	if _, err := c.Write(ctx, bb.Bytes()); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

func (c *Connection) VerifyHost(host string) error {
	if !c.meta.Secured() {
		return nil
	}

	vc, ok := netutil.AsConn[interface{ VerifyHostname(h string) error }](c.ctxConn)
	if !ok {
		// TODO: do we need to force hostname verification for any secured connection?
		//       maybe return some specific error
		return nil
	}
	return errors.Wrap(vc.VerifyHostname(host))
}

type netConnAdapter struct {
	*Connection
}

var _ net.Conn = (*netConnAdapter)(nil)

func (c *netConnAdapter) LocalAddr() net.Addr {
	return netutil.AddrPortToNetAddr(c.meta.Network, c.laddr)
}

func (c *netConnAdapter) RemoteAddr() net.Addr {
	return netutil.AddrPortToNetAddr(c.meta.Network, c.raddr)
}

func (c *netConnAdapter) Read(b []byte) (int, error) {
	return errors.Wrap2(c.Connection.Read(context.Background(), b))
}

func (c *netConnAdapter) Write(b []byte) (int, error) {
	return errors.Wrap2(c.Connection.Write(context.Background(), b))
}

func (c *netConnAdapter) SetDeadline(t time.Time) error {
	err := c.ctxConn.SetDeadline(t)
	if err != nil && errors.Is(err, net.ErrClosed) {
		err = errors.Errorf("%w: %w", err, ErrNetworkClosed)
	}
	return errors.Wrap(err)
}

func (c *netConnAdapter) SetReadDeadline(t time.Time) error {
	err := c.ctxConn.SetReadDeadline(t)
	if err != nil && errors.Is(err, net.ErrClosed) {
		err = errors.Errorf("%w: %w", err, ErrNetworkClosed)
	}
	return errors.Wrap(err)
}

func (c *netConnAdapter) SetWriteDeadline(t time.Time) error {
	err := c.ctxConn.SetWriteDeadline(t)
	if err != nil && errors.Is(err, net.ErrClosed) {
		err = errors.Errorf("%w: %w", err, ErrNetworkClosed)
	}
	return errors.Wrap(err)
}

func (c *netConnAdapter) Close() error {
	return errors.Wrap(c.Connection.Close(context.Background()))
}

func (c *netConnAdapter) Unwrap() net.Conn {
	if c == nil {
		return nil
	}
	return c.ctxConn
}

func (c *Connection) AsNetConn() net.Conn {
	if c == nil {
		return nil
	}
	return &netConnAdapter{c}
}

type ConnectionOrientedTransport struct {
	transpBase[net.Listener]
}

var _ sip.Transport = (*ConnectionOrientedTransport)(nil)

type ConnectionOrientedTransportOptions = TransportOptions

func NewConnectionOrientedTransport(
	meta sip.TransportMetadata,
	opts ...ConnectionOrientedTransportOptions,
) (*ConnectionOrientedTransport, error) {
	if !meta.IsValid() {
		return nil, errors.ErrorfWrap("invalid metadata %+v", meta)
	}

	tpOpts := util.LastSliceElemOr(opts, ConnectionOrientedTransportOptions{})

	tp := new(ConnectionOrientedTransport)
	tp.init(tp, meta, tpOpts)
	return tp, nil
}

func (tp *ConnectionOrientedTransport) LogValue() slog.Value {
	if tp == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.String("ptr", fmt.Sprintf("%p", tp)),
		slog.Any("proto", tp.meta.Proto),
		slog.Any("network", tp.meta.Network),
	)
}

func (tp *ConnectionOrientedTransport) newListener(ctx context.Context, netLis net.Listener) (transpListener, error) {
	return errors.Wrap2(NewConnectionOrientedListener(ctx, netLis, tp, tp.connOpts))
}

// AcquireConnection acquires a connection to the given remote address.
func (tp *ConnectionOrientedTransport) AcquireConnection(
	ctx context.Context,
	raddr netip.AddrPort,
	opts ...AcquireConnectionOptions,
) (sip.TransportConnection, error) {
	if tp.isClosing() {
		return nil, errors.Wrap(ErrTransportClosed)
	}

	acqOpts := util.LastSliceElemOr(opts, AcquireConnectionOptions{})

	// first try to search connected connection
	if conn, found := tp.findConn(raddr, acqOpts.locAddr(), acqOpts.Host); found {
		return conn.Connection, nil
	}

	if !acqOpts.Dial {
		return nil, errors.Wrap(ErrConnectionNotFound)
	}

	conn, err := tp.dialConn(ctx, raddr)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return conn.Connection, nil
}
