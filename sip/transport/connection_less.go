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

type ConnectionLessListener struct {
	connBase
	baseConn netutil.ContextPacketConn
	tp       *ConnectionLessTransport
	serving  atomic.Bool
}

var (
	_ sip.TransportListener   = (*ConnectionLessListener)(nil)
	_ sip.TransportConnection = (*ConnectionLessListener)(nil)
)

type ConnectionLessListenerOptions = ConnectionOptions

func NewConnectionLessListener(
	ctx context.Context,
	base net.PacketConn,
	tp *ConnectionLessTransport,
	opts ...ConnectionLessListenerOptions,
) (*ConnectionLessListener, error) {
	if base == nil {
		return nil, errors.ErrorWrap("nil connection")
	}
	if v, ok := base.(interface{ RemoteAddr() net.Addr }); ok && v.RemoteAddr() != nil {
		return nil, errors.ErrorWrap("connected socket not allowed here")
	}
	if v1, v2 := tp.meta.Network, base.LocalAddr().Network(); !util.EqFold(v1, v2) {
		return nil, errors.ErrorfWrap("metadata and connection networks mismatch: %q != %q", v1, v2)
	}

	lsOpts := util.LastSliceElemOr(opts, ConnectionLessListenerOptions{})

	ls := &ConnectionLessListener{
		connBase: connBase{
			meta:            tp.meta,
			laddr:           netutil.UnmapAddrPort(netip.MustParseAddrPort(base.LocalAddr().String())),
			prsr:            lsOpts.prsr(),
			maxMsgReadSize:  lsOpts.maxMsgReadSize(),
			maxMsgWriteSize: lsOpts.maxMsgWriteSize(),
			readTimeout:     lsOpts.readTimeout(),
			writeTimeout:    lsOpts.writeTimeout(),
			closed:          make(chan struct{}),
		},
		tp: tp,
	}
	ls.log = lsOpts.log().With(slog.Any("listener", ls))
	//nolint:forcetypeassert
	ls.baseConn = netutil.WrapPacketConn([]netutil.PacketConnDecorator{
		netutil.NewLogPacketConnDecorator(ls.log, slog.LevelDebug),
		netutil.NewCloseOncePacketConnDecorator(),
		netutil.NewContextPacketConnDecorator(),
	}...)(ctx, base).(netutil.ContextPacketConn)
	return ls, nil
}

func (ls *ConnectionLessListener) LogValue() slog.Value {
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

func (ls *ConnectionLessListener) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		f.Write([]byte(ls.String()))
		return
	case 'q':
		f.Write([]byte(strconv.Quote(ls.String())))
		return
	default:
		type (
			hideMethods            ConnectionLessListener
			ConnectionLessListener hideMethods
		)
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*ConnectionLessListener)(ls))
		return
	}
}

func (ls *ConnectionLessListener) Close(_ context.Context) error {
	ls.closeOnce.Do(func() {
		ls.closeErr = ls.baseConn.Close()
		if ls.closeErr != nil && errors.Is(ls.closeErr, net.ErrClosed) {
			ls.closeErr = errors.Errorf("%w: %w", ls.closeErr, ErrNetworkClosed)
		}
		close(ls.closed)
	})
	return errors.Wrap(ls.closeErr)
}

func (ls *ConnectionLessListener) Serve(ctx context.Context) error {
	if ls.isClosed() {
		return errors.Wrap(ErrNetworkClosed)
	}
	if !ls.serving.CompareAndSwap(false, true) {
		return errors.Wrap(ErrListenerServing)
	}
	defer ls.serving.Store(false)

	trLs, _ := ls.tp.lisMap.Load(ls.laddr)
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
			err = ErrNetworkClosed
		} else if !errors.Is(err, ErrNetworkClosed) {
			err = errors.Errorf("%w: %w", err, ErrNetworkClosed)
		}
		return errors.Wrap(err)
	default:
	}

	return errors.Wrap(err)
}

func (ls *ConnectionLessListener) WriteTo(ctx context.Context, b []byte, addr netip.AddrPort) (int, error) {
	if ls.isClosed() {
		return 0, errors.Wrap(ErrNetworkClosed)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ls.writeTimeout)
		defer cancel()
	}

	n, err := ls.baseConn.WriteToContext(ctx, b, netutil.AddrPortToNetAddr(ls.meta.Network, addr))
	if err != nil {
		if errors.Is(err, net.ErrClosed) {
			err = errors.Errorf("%w: %w", err, ErrNetworkClosed)
		}
		return 0, errors.Wrap(err)
	}
	return n, nil
}

func (ls *ConnectionLessListener) ReadFrom(ctx context.Context, b []byte) (int, netip.AddrPort, error) {
	if ls.isClosed() {
		return 0, netip.AddrPort{}, errors.Wrap(ErrNetworkClosed)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ls.readTimeout)
		defer cancel()
	}

	n, addr, err := ls.baseConn.ReadFromContext(ctx, b)
	if err != nil {
		if errors.Is(err, net.ErrClosed) {
			err = errors.Errorf("%w: %w", err, ErrNetworkClosed)
		}
		return 0, netip.AddrPort{}, errors.Wrap(err)
	}
	return n, netutil.UnmapAddrPort(netip.MustParseAddrPort(addr.String())), nil
}

func (ls *ConnectionLessListener) Messages(ctx context.Context) iter.Seq2[sip.Message, error] {
	return func(yield func(sip.Message, error) bool) {
		for msg, err := range ls.packetMsgs(ctx, ls, ls.recvKeepAliveCRLF) {
			if !yield(msg, errors.Wrap(err)) {
				break
			}
		}
	}
}

func (ls *ConnectionLessListener) recvKeepAliveCRLF(ctx context.Context, kaType uint8, raddr netip.AddrPort) {
	if ls.isClosed() {
		return
	}

	switch kaType {
	case kaPingCRLF:
		// connection-less transports like UDP should ping/pong via STUN multiplexing
		// be be liberal on possible double CRLF pings and send CRLF pongs
		ls.WriteTo(ctx, crlf, raddr) //nolint:errcheck
	case kaPongCRLF:
		// nothing to do, ignore
	}
}

func (ls *ConnectionLessListener) WriteMessage(
	ctx context.Context,
	msg sip.Message,
	addr netip.AddrPort,
	opts ...sip.RenderOptions,
) error {
	if ls.isClosed() {
		return errors.Wrap(ErrNetworkClosed)
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
		return errors.Wrap(sip.ErrMessageTooLarge)
	}

	if _, err := ls.WriteTo(ctx, bb.Bytes(), addr); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

type netPacketConnAdapter struct {
	*ConnectionLessListener
}

var _ net.PacketConn = (*netPacketConnAdapter)(nil)

func (c *netPacketConnAdapter) LocalAddr() net.Addr {
	return netutil.AddrPortToNetAddr(c.meta.Network, c.laddr)
}

func (c *netPacketConnAdapter) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	n, a, err := c.ConnectionLessListener.ReadFrom(context.Background(), p)
	return n, netutil.AddrPortToNetAddr(c.meta.Network, a), errors.Wrap(err)
}

func (c *netPacketConnAdapter) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return errors.Wrap2(c.ConnectionLessListener.WriteTo(
		context.Background(),
		p,
		netip.MustParseAddrPort(addr.String()),
	))
}

func (c *netPacketConnAdapter) SetDeadline(t time.Time) error {
	err := c.baseConn.SetDeadline(t)
	if err != nil && errors.Is(err, net.ErrClosed) {
		err = errors.Errorf("%w: %w", err, ErrNetworkClosed)
	}
	return errors.Wrap(err)
}

func (c *netPacketConnAdapter) SetReadDeadline(t time.Time) error {
	err := c.baseConn.SetReadDeadline(t)
	if err != nil && errors.Is(err, net.ErrClosed) {
		err = errors.Errorf("%w: %w", err, ErrNetworkClosed)
	}
	return errors.Wrap(err)
}

func (c *netPacketConnAdapter) SetWriteDeadline(t time.Time) error {
	err := c.baseConn.SetWriteDeadline(t)
	if err != nil && errors.Is(err, net.ErrClosed) {
		err = errors.Errorf("%w: %w", err, ErrNetworkClosed)
	}
	return errors.Wrap(err)
}

func (c *netPacketConnAdapter) Close() error {
	return errors.Wrap(c.ConnectionLessListener.Close(context.Background()))
}

func (c *netPacketConnAdapter) Unwrap() net.PacketConn {
	if c == nil {
		return nil
	}
	return c.baseConn
}

func (ls *ConnectionLessListener) AsNetPacketConn() net.PacketConn {
	if ls == nil {
		return nil
	}
	return &netPacketConnAdapter{ls}
}

type ConnectionLessTransport struct {
	transpBase[net.PacketConn]
}

var _ sip.Transport = (*ConnectionLessTransport)(nil)

type ConnectionLessTransportOptions = TransportOptions

func NewConnectionLessTransport(
	meta sip.TransportMetadata,
	opts ...ConnectionLessTransportOptions,
) (*ConnectionLessTransport, error) {
	if !meta.IsValid() {
		return nil, errors.ErrorfWrap("invalid metadata %+v", meta)
	}

	meta.Flags &^= sip.TransportFlagReliable | sip.TransportFlagStreamed

	tpOpts := util.LastSliceElemOr(opts, ConnectionLessTransportOptions{})

	tp := new(ConnectionLessTransport)
	tp.init(tp, meta, tpOpts)
	return tp, nil
}

func (tp *ConnectionLessTransport) LogValue() slog.Value {
	if tp == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.String("ptr", fmt.Sprintf("%p", tp)),
		slog.Any("proto", tp.meta.Proto),
		slog.Any("network", tp.meta.Network),
	)
}

func (tp *ConnectionLessTransport) newListener(ctx context.Context, base net.PacketConn) (transpListener, error) {
	return errors.Wrap2(NewConnectionLessListener(ctx, base, tp, tp.connOpts))
}

// AcquireConnection acquires a connection to the given remote address.
func (tp *ConnectionLessTransport) AcquireConnection(
	ctx context.Context,
	raddr netip.AddrPort,
	opts ...AcquireConnectionOptions,
) (sip.TransportConnection, error) {
	if tp.isClosing() {
		return nil, errors.Wrap(ErrTransportClosed)
	}

	acqOpts := util.LastSliceElemOr(opts, AcquireConnectionOptions{})
	laddr := acqOpts.locAddr()

	// first try to search connected connection
	if conn, found := tp.findConn(raddr, laddr, acqOpts.Host); found {
		return conn.Connection, nil
	}

	// fallback to listener
	if ls, ok := tp.lisMap.Load(laddr); ok {
		return ls.transpListener.(*ConnectionLessListener), nil //nolint:forcetypeassert
	}

	for a, l := range tp.lisMap.All() {
		la := l.LocalAddr()
		if !l.isClosed() && (!laddr.IsValid() ||
			laddr.Addr().Is4() && la.Addr().Is4() ||
			laddr.Addr().Is6() && la.Addr().Is6()) {
			return l.transpListener.(*ConnectionLessListener), nil //nolint:forcetypeassert
		}

		if l.isClosed() {
			tp.lisMap.Delete(a)
		}
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
