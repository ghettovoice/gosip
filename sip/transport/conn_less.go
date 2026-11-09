package transport

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/netutil"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

type ConnLessListener struct {
	connBase
	baseConn netutil.ContextPacketConn
	tp       *ConnLessTransport
	serving  atomic.Bool
}

var (
	_ sip.TransportListener = (*ConnLessListener)(nil)
	_ sip.TransportConn     = (*ConnLessListener)(nil)
)

type ConnLessListenerOptions = ConnOptions

func NewConnLessListener(
	base net.PacketConn,
	tp *ConnLessTransport,
	opts ...ConnLessListenerOptions,
) (*ConnLessListener, error) {
	if base == nil {
		return nil, errors.ErrorWrap("nil connection")
	}
	if v, ok := base.(interface{ RemoteAddr() net.Addr }); ok && v.RemoteAddr() != nil {
		return nil, errors.ErrorWrap("connected socket not allowed here")
	}
	if v1, v2 := tp.meta.Network, base.LocalAddr().Network(); !util.EqualFold(v1, v2) {
		return nil, errors.ErrorfWrap("metadata and connection networks mismatch: %q != %q", v1, v2)
	}

	lsOpts := util.LastSliceElem(opts, ConnLessListenerOptions{})

	ls := &ConnLessListener{
		meta:            tp.meta,
		laddr:           netutil.UnmapAddrPort(netip.MustParseAddrPort(base.LocalAddr().String())),
		parser:          lsOpts.parser(),
		maxMsgReadSize:  lsOpts.maxMsgReadSize(),
		maxMsgWriteSize: lsOpts.maxMsgWriteSize(),
		readTimeout:     lsOpts.readTimeout(),
		writeTimeout:    lsOpts.writeTimeout(),
		closed:          make(chan struct{}),
		tp:              tp,
	}
	ls.log = lsOpts.log().With(slog.Any("listener", ls))
	//nolint:forcetypeassert // the concrete type is fixed by construction
	ls.baseConn = netutil.WrapPacketConn(
		netutil.NewLogPacketConnDecorator(ls.log, slog.LevelDebug),
		netutil.NewCloseOncePacketConnDecorator(),
		netutil.NewContextPacketConnDecorator(),
	)(base).(netutil.ContextPacketConn)
	return ls, nil
}

func (ls *ConnLessListener) LogValue() slog.Value {
	if ls == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.String("ptr", fmt.Sprintf("%p", ls)),
		slog.Any("proto", ls.meta.Proto),
		slog.Any("network", ls.meta.Network),
		slog.Any("local_addr", ls.laddr),
	)
}

func (ls *ConnLessListener) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		f.Write([]byte(ls.String()))
		return
	case 'q':
		f.Write([]byte(strconv.Quote(ls.String())))
		return
	default:
		type (
			hideMethods      ConnLessListener
			ConnLessListener hideMethods
		)
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*ConnLessListener)(ls))
		return
	}
}

func (ls *ConnLessListener) Close() error {
	ls.closeOnce.Do(func() {
		ls.closeErr = ls.baseConn.Close()
		if ls.closeErr != nil && errors.Is(ls.closeErr, net.ErrClosed) {
			ls.closeErr = NewNetworkClosedError(ls.closeErr)
		}
		close(ls.closed)
	})
	return errors.Wrap(ls.closeErr)
}

func (ls *ConnLessListener) Serve(ctx context.Context) error {
	if ls.isClosed() {
		return errors.Wrap(NewNetworkClosedError())
	}
	if !ls.serving.CompareAndSwap(false, true) {
		return errors.Wrap(NewListenerServingError())
	}
	defer ls.serving.Store(false)

	trLs, _ := ls.tp.lisMap.Load(ls.laddr)
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

	err := ls.tp.readMsgs(ctx, ls.Messages(ctx), false)

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

func (ls *ConnLessListener) WriteTo(ctx context.Context, b []byte, addr netip.AddrPort) (int, error) {
	if ls.isClosed() {
		return 0, errors.Wrap(NewNetworkClosedError())
	}

	if _, ok := ctx.Deadline(); !ok && ls.writeTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ls.writeTimeout)
		defer cancel()
	}

	n, err := ls.baseConn.WriteToContext(ctx, b, netutil.AddrPortToNetAddr(ls.meta.Network, addr))
	if err != nil {
		if errors.Is(err, net.ErrClosed) {
			err = NewNetworkClosedError(err)
		}
		return 0, errors.Wrap(err)
	}
	return n, nil
}

func (ls *ConnLessListener) ReadFrom(ctx context.Context, b []byte) (int, netip.AddrPort, error) {
	if ls.isClosed() {
		return 0, netip.AddrPort{}, errors.Wrap(NewNetworkClosedError())
	}

	if _, ok := ctx.Deadline(); !ok && ls.readTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ls.readTimeout)
		defer cancel()
	}

	n, addr, err := ls.baseConn.ReadFromContext(ctx, b)
	if err != nil {
		if errors.Is(err, net.ErrClosed) {
			err = NewNetworkClosedError(err)
		}
		return 0, netip.AddrPort{}, errors.Wrap(err)
	}
	return n, netutil.UnmapAddrPort(netip.MustParseAddrPort(addr.String())), nil
}

func (ls *ConnLessListener) Messages(ctx context.Context) iter.Seq2[sip.Message, error] {
	return func(yield func(sip.Message, error) bool) {
		for msg, err := range ls.packetMsgs(ctx, ls, ls.recvKeepAliveCRLF) {
			if !yield(msg, errors.Wrap(err)) {
				break
			}
		}
	}
}

func (ls *ConnLessListener) recvKeepAliveCRLF(ctx context.Context, kaType uint8, raddr netip.AddrPort) {
	if ls.isClosed() {
		return
	}

	switch kaType {
	case kaPingCRLF:
		// connection-less transports like UDP should ping/pong via STUN multiplexing
		// be be liberal on possible double CRLF pings and send CRLF pongs
		ls.WriteTo(ctx, crlf, raddr) //nolint:errcheck // the CRLF pong is best-effort
	case kaPongCRLF:
		// nothing to do, ignore
	}
}

func (ls *ConnLessListener) WriteMessage(
	ctx context.Context,
	msg sip.Message,
	addr netip.AddrPort,
	opts ...sip.RenderOptions,
) error {
	if ls.isClosed() {
		return errors.Wrap(NewNetworkClosedError())
	}
	if err := msg.Validate(); err != nil {
		return errors.Wrap(err)
	}

	bb := util.GetBytesBuffer()
	defer util.FreeBytesBuffer(bb)

	if _, err := msg.RenderTo(bb, opts...); err != nil {
		return errors.Wrap(err)
	}
	if uint(bb.Len()) > ls.maxMsgWriteSize {
		return errors.Wrap(sip.NewMessageTooLargeError(ls.maxMsgWriteSize))
	}

	if _, err := ls.WriteTo(ctx, bb.Bytes(), addr); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

type netPktConnAdapter struct {
	*ConnLessListener
}

var _ net.PacketConn = (*netPktConnAdapter)(nil)

func (c *netPktConnAdapter) LocalAddr() net.Addr {
	return netutil.AddrPortToNetAddr(c.meta.Network, c.laddr)
}

func (c *netPktConnAdapter) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	n, a, err := c.ConnLessListener.ReadFrom(context.Background(), p)
	return n, netutil.AddrPortToNetAddr(c.meta.Network, a), errors.Wrap(err)
}

func (c *netPktConnAdapter) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return errors.Wrap2(c.ConnLessListener.WriteTo(
		context.Background(),
		p,
		netip.MustParseAddrPort(addr.String()),
	))
}

func (c *netPktConnAdapter) SetDeadline(t time.Time) error {
	err := c.baseConn.SetDeadline(t)
	if err != nil && errors.Is(err, net.ErrClosed) {
		err = NewNetworkClosedError(err)
	}
	return errors.Wrap(err)
}

func (c *netPktConnAdapter) SetReadDeadline(t time.Time) error {
	err := c.baseConn.SetReadDeadline(t)
	if err != nil && errors.Is(err, net.ErrClosed) {
		err = NewNetworkClosedError(err)
	}
	return errors.Wrap(err)
}

func (c *netPktConnAdapter) SetWriteDeadline(t time.Time) error {
	err := c.baseConn.SetWriteDeadline(t)
	if err != nil && errors.Is(err, net.ErrClosed) {
		err = NewNetworkClosedError(err)
	}
	return errors.Wrap(err)
}

func (c *netPktConnAdapter) Close() error {
	return errors.Wrap(c.ConnLessListener.Close())
}

func (c *netPktConnAdapter) Unwrap() net.PacketConn {
	if c == nil {
		return nil
	}
	return c.baseConn
}

func (ls *ConnLessListener) AsNetPacketConn() net.PacketConn {
	if ls == nil {
		return nil
	}
	return &netPktConnAdapter{ls}
}

type ConnLessTransport struct {
	transpBase[net.PacketConn]
}

var _ sip.Transport = (*ConnLessTransport)(nil)

type ConnLessTransportOptions = TransportOptions

func NewConnLessTransport(
	meta sip.TransportMetadata,
	opts ...ConnLessTransportOptions,
) (*ConnLessTransport, error) {
	if !meta.IsValid() {
		return nil, errors.ErrorfWrap("invalid metadata %+v", meta)
	}

	meta.Flags &^= sip.TransportFlagReliable | sip.TransportFlagStreamed

	tpOpts := util.LastSliceElem(opts, ConnLessTransportOptions{})

	tp := new(ConnLessTransport)
	tp.init(tp, meta, tpOpts)
	return tp, nil
}

func (tp *ConnLessTransport) LogValue() slog.Value {
	if tp == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.String("ptr", fmt.Sprintf("%p", tp)),
		slog.Any("proto", tp.meta.Proto),
		slog.Any("network", tp.meta.Network),
	)
}

func (tp *ConnLessTransport) newListener(ctx context.Context, base net.PacketConn) (transpListener, error) {
	return errors.Wrap2(NewConnLessListener(base, tp, tp.connOpts))
}

// AcquireConn acquires a connection to the given remote address.
func (tp *ConnLessTransport) AcquireConn(
	ctx context.Context,
	raddr netip.AddrPort,
	opts ...AcquireConnOptions,
) (sip.TransportConn, error) {
	if tp.isClosing() {
		return nil, errors.Wrap(NewTransportClosedError())
	}

	acqOpts := util.LastSliceElem(opts, AcquireConnOptions{})
	laddr := acqOpts.locAddr()

	// first try to search connected connection
	if conn, found := tp.findConn(raddr, laddr, acqOpts.Host); found {
		return conn.Conn, nil
	}

	// fallback to listener
	if ls, ok := tp.lisMap.Load(laddr); ok && !ls.isClosed() {
		return ls.transpListener.(*ConnLessListener), nil //nolint:forcetypeassert // the concrete type is fixed by construction
	}

	for a, l := range tp.lisMap.All() {
		la := l.LocalAddr()
		if !l.isClosed() && (!laddr.IsValid() ||
			laddr.Addr().Is4() && la.Addr().Is4() ||
			laddr.Addr().Is6() && la.Addr().Is6()) {
			return l.transpListener.(*ConnLessListener), nil //nolint:forcetypeassert // the concrete type is fixed by construction
		}

		if l.isClosed() {
			tp.lisMap.CompareAndDelete(a, func(actual *trackedListener) bool {
				return actual == l
			})
		}
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
