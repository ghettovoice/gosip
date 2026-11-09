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

	"github.com/ghettovoice/timeutil"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/netutil"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/sip"
)

type ConnOrientedListener struct {
	baseLis  net.Listener
	tp       *ConnOrientedTransport
	addr     netip.AddrPort
	log      *slog.Logger
	connOpts ConnOptions

	closeOnce sync.Once
	closeErr  error
	closed    chan struct{}
	serving   atomic.Bool
}

var _ sip.TransportListener = (*ConnOrientedListener)(nil)

type ConnOrientedListenerOptions = ConnOptions

func NewConnOrientedListener(
	base net.Listener,
	tp *ConnOrientedTransport,
	opts ...ConnOrientedListenerOptions,
) (*ConnOrientedListener, error) {
	if base == nil {
		return nil, errors.ErrorWrap("nil listener")
	}

	lsOpts := util.LastSliceElem(opts, ConnOrientedListenerOptions{})

	ls := &ConnOrientedListener{
		tp:     tp,
		addr:   netutil.UnmapAddrPort(netip.MustParseAddrPort(base.Addr().String())),
		closed: make(chan struct{}),
	}
	ls.log = lsOpts.log().With(slog.Any("listener", ls))
	ls.baseLis = netutil.WrapListener(
		netutil.NewLogListenerDecorator(ls.log, slog.LevelDebug),
		netutil.NewCloseOnceListenerDecorator(),
	)(base)

	ls.connOpts = lsOpts
	ls.connOpts.Logger = ls.log
	return ls, nil
}

func (ls *ConnOrientedListener) Metadata() sip.TransportMetadata { return ls.tp.meta }
func (ls *ConnOrientedListener) LocalAddr() netip.AddrPort       { return ls.addr }

func (ls *ConnOrientedListener) LogValue() slog.Value {
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

func (ls *ConnOrientedListener) String() string {
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

func (ls *ConnOrientedListener) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		f.Write([]byte(ls.String()))
		return
	case 'q':
		f.Write([]byte(strconv.Quote(ls.String())))
		return
	default:
		type (
			hideMethods          ConnOrientedListener
			ConnOrientedListener hideMethods
		)
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*ConnOrientedListener)(ls))
		return
	}
}

func (ls *ConnOrientedListener) Accept(ctx context.Context) (*Conn, error) {
	netConn, err := ls.baseLis.Accept()
	if err != nil {
		if errors.Is(err, net.ErrClosed) {
			err = NewNetworkClosedError(err)
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
		return nil, errors.Wrap(NewConnTrackedError())
	}

	return conn.Conn, nil
}

func (ls *ConnOrientedListener) isClosed() bool {
	select {
	case <-ls.closed:
		return true
	default:
		return false
	}
}

func (ls *ConnOrientedListener) Close() error {
	ls.closeOnce.Do(func() {
		ls.closeErr = ls.baseLis.Close()
		if ls.closeErr != nil && errors.Is(ls.closeErr, net.ErrClosed) {
			ls.closeErr = NewNetworkClosedError(ls.closeErr)
		}
		close(ls.closed)
	})
	return errors.Wrap(ls.closeErr)
}

func (ls *ConnOrientedListener) Serve(ctx context.Context) error {
	if ls.isClosed() {
		return errors.Wrap(NewNetworkClosedError())
	}
	if !ls.serving.CompareAndSwap(false, true) {
		return errors.Wrap(NewListenerServingError())
	}
	defer ls.serving.Store(false)

	trLs, _ := ls.tp.lisMap.Load(ls.addr)
	if trLs != nil {
		defer ls.tp.untrackLis(ctx, trLs)
	}

	go func() {
		select {
		case <-ctx.Done():
			if trLs != nil && trLs.borrowed {
				return
			}

			if err := ls.Close(); err != nil {
				ls.log.WarnContext(
					ctx, "failed to close listener",
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
		if err == nil {
			err = NewNetworkClosedError()
		} else if !errors.Is(err, ErrNetworkClosed) {
			err = NewNetworkClosedError(err)
		}
		return errors.Wrap(err)
	default:
	}

	return errors.Wrap(err)
}

// acceptBackoff tracks the exponential backoff delay applied after temporary
// accept errors, along with its timer.
type acceptBackoff struct {
	delay time.Duration
	tmr   *time.Timer
}

func (b *acceptBackoff) reset() {
	b.delay = 0
	if b.tmr != nil {
		b.tmr.Stop()
	}
}

func (b *acceptBackoff) next() time.Duration {
	if b.delay == 0 {
		b.delay = initRetryDelay
	} else {
		b.delay *= 2
	}
	return min(b.delay, time.Minute)
}

func (b *acceptBackoff) wait() {
	if b.tmr == nil {
		b.tmr = time.NewTimer(b.delay)
	} else {
		b.tmr.Reset(b.delay)
	}
}

func (ls *ConnOrientedListener) serve(ctx context.Context) error {
	var bkoff acceptBackoff
	defer bkoff.reset()

	for {
		_, err := ls.Accept(ctx)
		if err == nil {
			bkoff.reset()
			continue
		}

		if errors.Is(err, ErrConnTracked) {
			bkoff.reset()
			continue
		}
		if !errclass.IsTemporary(err) {
			return errors.Wrap(err)
		}

		delay := bkoff.next()
		ls.log.DebugContext(
			ctx, "failed to accept connection due to the temporary error, continue accepting after delay...",
			slog.Any("error", err),
			slog.Duration("delay", delay),
			slog.Any("local_addr", ls.addr),
		)
		bkoff.wait()

		select {
		case <-ls.closed:
			return errors.Wrap(NewNetworkClosedError())
		case <-ctx.Done():
			return errors.Wrap(ctx.Err())
		case <-bkoff.tmr.C:
			bkoff.reset()
		}
	}
}

type netLisAdapter struct {
	*ConnOrientedListener
}

var _ net.Listener = (*netLisAdapter)(nil)

func (l *netLisAdapter) Addr() net.Addr {
	return netutil.AddrPortToNetAddr(l.Metadata().Network, l.addr)
}

func (l *netLisAdapter) Accept() (net.Conn, error) {
	conn, err := l.ConnOrientedListener.Accept(context.Background())
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return conn.AsNetConn(), nil
}

func (l *netLisAdapter) Close() error {
	return errors.Wrap(l.ConnOrientedListener.Close())
}

func (l *netLisAdapter) Unwrap() net.Listener {
	if l == nil {
		return nil
	}
	return l.baseLis
}

func (ls *ConnOrientedListener) AsNetListener() net.Listener {
	if ls == nil {
		return nil
	}
	return &netLisAdapter{ls}
}

type Conn struct {
	connBase
	origConn net.Conn
	ctxConn  netutil.ContextConn
	idleTmr  *timeutil.Watchdog
}

func NewConn(
	base net.Conn,
	meta sip.TransportMetadata,
	opts ...ConnOptions,
) (*Conn, error) {
	if base == nil {
		return nil, errors.ErrorWrap("nil connection")
	}
	if base.RemoteAddr() == nil {
		return nil, errors.ErrorWrap("unconnected socket not allowed here")
	}
	if !meta.IsValid() {
		return nil, errors.ErrorfWrap("invalid metadata %+v", meta)
	}
	if v1, v2 := meta.Network, base.LocalAddr().Network(); !util.EqualFold(v1, v2) {
		return nil, errors.ErrorfWrap("metadata and connection networks mismatch: %q != %q", v1, v2)
	}

	connOpts := util.LastSliceElem(opts, ConnOptions{})

	c := &Conn{
		meta:            meta.Canonic(),
		laddr:           netutil.UnmapAddrPort(netip.MustParseAddrPort(base.LocalAddr().String())),
		raddr:           netutil.UnmapAddrPort(netip.MustParseAddrPort(base.RemoteAddr().String())),
		parser:          connOpts.parser(),
		maxMsgReadSize:  connOpts.maxMsgReadSize(),
		maxMsgWriteSize: connOpts.maxMsgWriteSize(),
		readTimeout:     connOpts.readTimeout(),
		writeTimeout:    connOpts.writeTimeout(),
		idleTimeout:     connOpts.idleTimeout(),
		closed:          make(chan struct{}),
		origConn:        base,
	}
	c.log = connOpts.log().With(slog.Any("connection", c))
	//nolint:forcetypeassert // the concrete type is fixed by construction
	c.ctxConn = netutil.WrapConn(
		netutil.NewLogConnDecorator(c.log, slog.LevelDebug),
		netutil.NewCloseOnceConnDecorator(),
		netutil.NewContextConnDecorator(),
	)(base).(netutil.ContextConn)
	c.idleTmr = timeutil.NewWatchdog(c.idleTimeout, func() {
		if err := c.Close(); err != nil {
			c.log.Warn("failed to close connection",
				slog.Any("connection", c),
				slog.Any("error", err),
			)
		}
	})
	c.idleTmr.Start()
	return c, nil
}

func (c *Conn) resetIdleTmr() {
	if c.idleTmr != nil {
		c.idleTmr.Reset()
	}
}

func (c *Conn) LogValue() slog.Value {
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

func (c *Conn) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		f.Write([]byte(c.String()))
		return
	case 'q':
		f.Write([]byte(strconv.Quote(c.String())))
		return
	default:
		type (
			hideMethods Conn
			Conn        hideMethods
		)
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*Conn)(c))
		return
	}
}

func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.ctxConn.Close()
		if c.closeErr != nil && errors.Is(c.closeErr, net.ErrClosed) {
			c.closeErr = NewNetworkClosedError(c.closeErr)
		}
		close(c.closed)
	})
	if c.idleTmr != nil {
		c.idleTmr.Close()
	}
	return errors.Wrap(c.closeErr)
}

func (c *Conn) Write(ctx context.Context, buf []byte) (int, error) {
	if c.isClosed() {
		return 0, errors.Wrap(NewNetworkClosedError())
	}

	if _, ok := ctx.Deadline(); !ok && c.writeTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.writeTimeout)
		defer cancel()
	}

	n, err := c.ctxConn.WriteContext(ctx, buf)
	if err != nil {
		if errors.Is(err, net.ErrClosed) {
			err = NewNetworkClosedError(err)
		}
		return 0, errors.Wrap(err)
	}
	c.resetIdleTmr()
	return n, nil
}

func (c *Conn) Read(ctx context.Context, buf []byte) (int, error) {
	if c.isClosed() {
		return 0, errors.Wrap(NewNetworkClosedError())
	}

	if _, ok := ctx.Deadline(); !ok && c.readTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.readTimeout)
		defer cancel()
	}

	n, err := c.ctxConn.ReadContext(ctx, buf)
	if err != nil {
		if errors.Is(err, net.ErrClosed) {
			err = NewNetworkClosedError(err)
		}
		return 0, errors.Wrap(err)
	}
	return n, nil
}

func (c *Conn) Messages(ctx context.Context) iter.Seq2[sip.Message, error] {
	var msgs iter.Seq2[sip.Message, error]
	if c.meta.Streamed() {
		msgs = c.streamMsgs(ctx, c, c.recvKeepAlive)
	} else {
		msgs = c.packetMsgs(
			ctx, &streamToPacketAdapter{c},
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

func (c *Conn) recvKeepAlive(ctx context.Context, kaType uint8) {
	if c.isClosed() {
		return
	}

	switch kaType {
	case kaPingCRLF:
		c.resetIdleTmr()

		if _, err := c.Write(ctx, crlf); err != nil {
			c.log.WarnContext(ctx, "failed to send CRLF pong", slog.Any("error", err))
		}
		// TODO: fire event or callback for upper layer
	case kaPongCRLF:
		c.resetIdleTmr()
		// TODO: confirm running ping
	}
}

func (c *Conn) WriteMessage(
	ctx context.Context,
	msg sip.Message,
	addr netip.AddrPort,
	opts ...sip.RenderOptions,
) error {
	if c.isClosed() {
		return errors.Wrap(NewNetworkClosedError())
	}

	addr = netutil.UnmapAddrPort(addr)
	if addr.IsValid() && addr != c.raddr {
		return errors.Wrap(errclass.Classify(
			errors.Errorf("can't write to %q", addr),
			sip.ErrClassTransport,
		))
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
		return errors.Wrap(sip.NewMessageTooLargeError(c.maxMsgWriteSize))
	}

	if _, err := c.Write(ctx, bb.Bytes()); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

func (c *Conn) VerifyHost(host string) error {
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
	*Conn
}

var _ net.Conn = (*netConnAdapter)(nil)

func (c *netConnAdapter) LocalAddr() net.Addr {
	return netutil.AddrPortToNetAddr(c.meta.Network, c.laddr)
}

func (c *netConnAdapter) RemoteAddr() net.Addr {
	return netutil.AddrPortToNetAddr(c.meta.Network, c.raddr)
}

func (c *netConnAdapter) Read(b []byte) (int, error) {
	return errors.Wrap2(c.Conn.Read(context.Background(), b))
}

func (c *netConnAdapter) Write(b []byte) (int, error) {
	return errors.Wrap2(c.Conn.Write(context.Background(), b))
}

func (c *netConnAdapter) SetDeadline(t time.Time) error {
	err := c.ctxConn.SetDeadline(t)
	if err != nil && errors.Is(err, net.ErrClosed) {
		err = NewNetworkClosedError(err)
	}
	return errors.Wrap(err)
}

func (c *netConnAdapter) SetReadDeadline(t time.Time) error {
	err := c.ctxConn.SetReadDeadline(t)
	if err != nil && errors.Is(err, net.ErrClosed) {
		err = NewNetworkClosedError(err)
	}
	return errors.Wrap(err)
}

func (c *netConnAdapter) SetWriteDeadline(t time.Time) error {
	err := c.ctxConn.SetWriteDeadline(t)
	if err != nil && errors.Is(err, net.ErrClosed) {
		err = NewNetworkClosedError(err)
	}
	return errors.Wrap(err)
}

func (c *netConnAdapter) Close() error {
	return errors.Wrap(c.Conn.Close())
}

func (c *netConnAdapter) Unwrap() net.Conn {
	if c == nil {
		return nil
	}
	return c.ctxConn
}

func (c *Conn) AsNetConn() net.Conn {
	if c == nil {
		return nil
	}
	return &netConnAdapter{c}
}

type ConnOrientedTransport struct {
	transpBase[net.Listener]
}

var _ sip.Transport = (*ConnOrientedTransport)(nil)

type ConnOrientedTransportOptions = TransportOptions

func NewConnOrientedTransport(
	meta sip.TransportMetadata,
	opts ...ConnOrientedTransportOptions,
) (*ConnOrientedTransport, error) {
	if !meta.IsValid() {
		return nil, errors.ErrorfWrap("invalid metadata %+v", meta)
	}

	tpOpts := util.LastSliceElem(opts, ConnOrientedTransportOptions{})

	tp := new(ConnOrientedTransport)
	tp.init(tp, meta, tpOpts)
	return tp, nil
}

func (tp *ConnOrientedTransport) LogValue() slog.Value {
	if tp == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.String("ptr", fmt.Sprintf("%p", tp)),
		slog.Any("proto", tp.meta.Proto),
		slog.Any("network", tp.meta.Network),
	)
}

func (tp *ConnOrientedTransport) newListener(ctx context.Context, netLis net.Listener) (transpListener, error) {
	return errors.Wrap2(NewConnOrientedListener(netLis, tp, tp.connOpts))
}

// AcquireConn acquires a connection to the given remote address.
func (tp *ConnOrientedTransport) AcquireConn(
	ctx context.Context,
	raddr netip.AddrPort,
	opts ...AcquireConnOptions,
) (sip.TransportConn, error) {
	if tp.isClosing() {
		return nil, errors.Wrap(NewTransportClosedError())
	}

	acqOpts := util.LastSliceElem(opts, AcquireConnOptions{})

	// first try to search connected connection
	if conn, found := tp.findConn(raddr, acqOpts.locAddr(), acqOpts.Host); found {
		return conn.Conn, nil
	}

	if !acqOpts.Dial {
		return nil, errors.Wrap(NewConnNotFoundError())
	}

	conn, err := tp.dialConn(ctx, raddr, acqOpts)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return conn.Conn, nil
}
