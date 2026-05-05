package sip

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/netutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/log"
)

type Conn interface {
	Metadata() TransportMetadata
	LocalAddr() netip.AddrPort
	RemoteAddr() netip.AddrPort
	Messages(ctx context.Context) iter.Seq2[Message, error]
	WriteMessage(ctx context.Context, msg Message, addr netip.AddrPort, opts *RenderOptions) error
	Close() error
}

const connCtxKey types.ContextKey = "connection"

func ContextWithConn(ctx context.Context, conn Conn) context.Context {
	if c, ok := ConnFromContext(ctx); ok && c == conn {
		return ctx
	}
	return context.WithValue(ctx, connCtxKey, conn)
}

func ConnFromContext(ctx context.Context) (Conn, bool) {
	c, ok := ctx.Value(connCtxKey).(Conn)
	return c, ok
}

type ConnOptions struct {
	TTL    time.Duration
	Parser Parser
	Logger *slog.Logger
}

func (o *ConnOptions) ttl() time.Duration {
	if o == nil || o.TTL == 0 {
		return defTimingCfg.TimeC()
	}
	return o.TTL
}

func (o *ConnOptions) parser() Parser {
	if o == nil || o.Parser == nil {
		return DefaultParser()
	}
	return o.Parser
}

func (o *ConnOptions) logger() *slog.Logger {
	if o == nil || o.Logger == nil {
		return log.Default()
	}
	return o.Logger
}

func NewConn(ctx context.Context, base any, meta TransportMetadata, opts *ConnOptions) (Conn, error) {
	if v, ok := base.(interface{ RemoteAddr() net.Addr }); ok && v.RemoteAddr() != nil {
		// connected socket
		c, ok := base.(net.Conn)
		if !ok {
			return nil, errors.NewInvalidArgumentErrorWrap("unexpected connection type %T", base)
		}

		return errors.Wrap2(newConn(ctx, c, meta, opts))
	}

	c, ok := base.(net.PacketConn)
	if !ok {
		return nil, errors.NewInvalidArgumentErrorWrap("unexpected connection type %T", base)
	}

	return errors.Wrap2(newPacketConn(ctx, c, meta, opts))
}

type connBase struct {
	laddr, raddr netip.AddrPort
	meta         TransportMetadata
	prs          Parser
	log          *slog.Logger

	closeOnce sync.Once
	closeErr  error
	closed    chan struct{}
}

func (cb *connBase) isClosed() bool {
	select {
	case <-cb.closed:
		return true
	default:
		return false
	}
}

func (cb *connBase) wrapInMsg(msg Message, laddr, raddr netip.AddrPort) Message {
	switch m := msg.(type) {
	case *Request:
		return util.Must2(NewInboundRequestEnvelope(m, cb.meta.Proto, laddr, raddr))
	case *Response:
		return util.Must2(NewInboundResponseEnvelope(m, cb.meta.Proto, laddr, raddr))
	default:
		// should never happen with lib Parser implementation
		panic(newUnexpectMsgTypeErrWrap(msg))
	}
}

type readPacketConn interface {
	LocalAddr() netip.AddrPort
	ReadFrom(ctx context.Context, b []byte) (int, netip.AddrPort, error)
}

type streamToPacketAdapter struct {
	readStreamConn
}

func (a *streamToPacketAdapter) ReadFrom(ctx context.Context, buf []byte) (int, netip.AddrPort, error) {
	n, err := a.Read(ctx, buf)
	if err != nil {
		return 0, netip.AddrPort{}, errors.Wrap(err)
	}

	return n, a.RemoteAddr(), nil
}

//nolint:gocognit
func (cb *connBase) packetMsgs(
	ctx context.Context,
	conn readPacketConn,
	onKeepAlive func(ctx context.Context, kaType uint8, raddr netip.AddrPort),
) iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		var (
			rdrDelay    time.Duration
			rdrDelayTmr *time.Timer
		)
		defer func() {
			if rdrDelayTmr != nil {
				rdrDelayTmr.Stop()
			}
		}()

		buf := make([]byte, MaxMessageSize)
		for {
			num, raddr, err := conn.ReadFrom(ctx, buf)
			if err != nil {
				// conn read error
				if err = cb.continueOnTempReadErr(
					ctx, errors.Wrap(err),
					conn.LocalAddr(), netip.AddrPort{},
					&rdrDelay, &rdrDelayTmr,
				); err == nil {
					// continue on temp, read deadline error
					continue
				}

				// stop on final error
				yield(nil, errors.Wrap(err))

				return
			}

			rdrDelay = 0

			switch {
			// TODO: implement STUN multiplexing for RFC 5626
			case bytes.Equal(buf[:num], crlf2x):
				if onKeepAlive != nil {
					onKeepAlive(ctx, keepAlivePingCRLF, raddr)
				}
				continue
			case bytes.Equal(buf[:num], crlf):
				if onKeepAlive != nil {
					onKeepAlive(ctx, keepAlivePongCRLF, raddr)
				}
				continue
			}

			msg, err := cb.prs.ParsePacket(buf[:num])
			if err != nil {
				perr, ok := errors.AsType[*ParseError](err)
				if !ok || perr.Msg == nil {
					// skip any empty buffer and parse errors without message
					continue
				}

				perr.Msg = cb.wrapInMsg(perr.Msg, conn.LocalAddr(), raddr)
			}

			if msg != nil {
				msg = cb.wrapInMsg(msg, conn.LocalAddr(), raddr)
			}

			if !yield(msg, errors.Wrap(err)) {
				return
			}

			select {
			case <-ctx.Done():
				yield(nil, errors.Wrap(ctx.Err()))
				return
			default:
			}
		}
	}
}

type readStreamConn interface {
	LocalAddr() netip.AddrPort
	RemoteAddr() netip.AddrPort
	Read(ctx context.Context, b []byte) (int, error)
}

type streamConnReader struct {
	ctx context.Context
	readStreamConn
}

func (r *streamConnReader) Read(b []byte) (int, error) {
	return errors.Wrap2(r.readStreamConn.Read(r.ctx, b))
}

//nolint:gocognit
func (cb *connBase) streamMsgs(
	ctx context.Context,
	conn readStreamConn,
	onKeepAlive func(ctx context.Context, kaType uint8),
) iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		kas := &crlfKeepAliveState{
			recvPing: func() {
				if onKeepAlive != nil {
					onKeepAlive(ctx, keepAlivePingCRLF)
				}
			},
			recvPong: func() {
				if onKeepAlive != nil {
					onKeepAlive(ctx, keepAlivePongCRLF)
				}
			},
		}

		var (
			rdrDelay    time.Duration
			rdrDelayTmr *time.Timer
		)
		defer func() {
			if rdrDelayTmr != nil {
				rdrDelayTmr.Stop()
			}
		}()

		rd := &io.LimitedReader{
			R: &streamConnReader{ctx, conn},
			N: int64(MaxMessageSize),
		}

		sp := cb.prs.ParseStream(rd)
		for msg, err := range sp.Messages() {
			if err != nil {
				isTooLong := rd.N <= 0
				rd.N = int64(MaxMessageSize)

				perr, ok := errors.AsType[*ParseError](err)
				if !ok {
					// failed on reading conn before message start
					kas.reset()

					if isTooLong {
						err = ErrMessageTooLarge
					}

					if err = cb.continueOnTempReadErr(
						ctx, errors.Wrap(err),
						conn.LocalAddr(), conn.RemoteAddr(),
						&rdrDelay, &rdrDelayTmr,
					); err == nil {
						// continue on temp, read deadline error
						continue
					}

					// stop iterator on final read errors
					yield(nil, errors.Wrap(err))

					return
				}

				rdrDelay = 0

				if perr.Msg == nil {
					// failed on parsing of message start line
					if errors.Is(perr.Err, grammar.ErrEmptyInput) {
						// got CRLF
						kas.crlf()
						continue
					}

					kas.reset()

					if !yield(nil, errors.Wrap(err)) {
						return
					}

					continue
				}

				// failed at reading/parsing of message headers or body
				kas.reset()

				if isTooLong {
					err = errors.Errorf("%w: %w", err, ErrMessageTooLarge)
				}

				perr.Msg = cb.wrapInMsg(perr.Msg, conn.LocalAddr(), conn.RemoteAddr())
			}

			if msg != nil {
				rdrDelay = 0

				kas.reset()

				rd.N = int64(MaxMessageSize)

				msg = cb.wrapInMsg(msg, conn.LocalAddr(), conn.RemoteAddr())
			}

			if !yield(msg, errors.Wrap(err)) {
				return
			}

			select {
			case <-ctx.Done():
				yield(nil, errors.Wrap(ctx.Err()))
				return
			default:
			}
		}
	}
}

func (cb *connBase) continueOnTempReadErr(
	ctx context.Context,
	err error,
	laddr, raddr netip.AddrPort,
	rdrDelay *time.Duration,
	rdrDelayTmr **time.Timer,
) error {
	if !errors.IsTemporaryErr(err) {
		*rdrDelay = 0

		if *rdrDelayTmr != nil {
			(*rdrDelayTmr).Stop()
		}

		return errors.Wrap(err)
	}

	// retry after delay on temp conn errors
	if errors.Is(err, os.ErrDeadlineExceeded) {
		// our read deadline, no need for exponetial rise
		*rdrDelay = time.Millisecond
	} else {
		// network read error
		if *rdrDelay == 0 {
			*rdrDelay = 5 * time.Millisecond
		} else {
			*rdrDelay *= 2
		}

		if v := time.Minute; *rdrDelay > v {
			*rdrDelay = v
		}

		attrs := append(make([]slog.Attr, 0, 4),
			slog.Any("error", err),
			slog.Duration("delay", *rdrDelay),
			slog.Any("local_addr", laddr),
		)
		if raddr.IsValid() {
			attrs = append(attrs, slog.Any("remote_addr", raddr))
		}

		cb.log.LogAttrs(ctx, slog.LevelDebug,
			"failed to read connection due to the temporary error, continue reading after delay...",
			attrs...,
		)
	}

	if *rdrDelayTmr == nil {
		*rdrDelayTmr = time.NewTimer(*rdrDelay)
	} else {
		(*rdrDelayTmr).Reset(*rdrDelay)
	}

	select {
	case <-cb.closed:
		*rdrDelay = 0

		if *rdrDelayTmr != nil {
			(*rdrDelayTmr).Stop()
		}

		return errors.Wrap(net.ErrClosed)
	case <-ctx.Done():
		*rdrDelay = 0

		if *rdrDelayTmr != nil {
			(*rdrDelayTmr).Stop()
		}

		return errors.Wrap(ctx.Err())
	case <-(*rdrDelayTmr).C:
		return nil
	}
}

const (
	keepAlivePingCRLF uint8 = iota + 1
	keepAlivePongCRLF
)

type crlfKeepAliveState struct {
	crlfNum    atomic.Uint32
	pingWndTmr *time.Timer
	recvPing   func()
	recvPong   func()
}

func (ka *crlfKeepAliveState) crlf() {
	switch ka.crlfNum.Add(1) {
	case 1:
		ka.pingWndTmr = time.AfterFunc(100*time.Millisecond, func() {
			if ka.crlfNum.CompareAndSwap(1, 0) {
				ka.recvPong()
			}
		})
	case 2:
		ka.recvPing()
		ka.crlfNum.Store(0)

		if ka.pingWndTmr != nil {
			ka.pingWndTmr.Stop()
		}
	}
}

func (ka *crlfKeepAliveState) reset() {
	if ka.crlfNum.CompareAndSwap(1, 0) {
		ka.recvPong()

		if ka.pingWndTmr != nil {
			ka.pingWndTmr.Stop()
		}
	}
}

func (cb *connBase) String() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	sb.WriteString(cb.meta.Network)
	sb.WriteRune(' ')
	sb.WriteString(cb.laddr.String())

	if cb.raddr.IsValid() {
		sb.WriteRune(' ')
		sb.WriteString(cb.raddr.String())
	}

	return sb.String()
}

func (cb *connBase) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		f.Write([]byte(cb.String())) //nolint:errcheck
		return
	case 'q':
		f.Write([]byte(strconv.Quote(cb.String()))) //nolint:errcheck
		return
	default:
		type (
			hideMethods connBase
			connBase    hideMethods
		)

		fmt.Fprintf(f, fmt.FormatString(f, verb), (*connBase)(cb))

		return
	}
}

type packetConn struct {
	connBase
	origConn net.PacketConn
	conn     netutil.ContextPacketConn
}

func newPacketConn(ctx context.Context, base net.PacketConn, meta TransportMetadata, opts *ConnOptions) (*packetConn, error) {
	if base == nil {
		return nil, errors.NewInvalidArgumentErrorWrap("nil connection")
	}

	if v, ok := base.(interface{ RemoteAddr() net.Addr }); ok && v.RemoteAddr() != nil {
		return nil, errors.NewInvalidArgumentErrorWrap("connected socket not allowed here")
	}

	if !meta.IsValid() {
		return nil, errors.NewInvalidArgumentErrorWrap("invalid metadata %q", meta)
	}

	if v1, v2 := meta.Network, base.LocalAddr().Network(); !util.EqFold(v1, v2) {
		return nil, errors.NewInvalidArgumentErrorWrap(
			"metadata and connection networks mismatch: %q != %q",
			v1, v2,
		)
	}

	meta.Flags &^= TransportFlagReliable | TransportFlagStreamed
	c := &packetConn{
		connBase: connBase{
			meta:   meta.Canonic(),
			laddr:  netutil.UnmapAddrPort(netip.MustParseAddrPort(base.LocalAddr().String())),
			prs:    opts.parser(),
			closed: make(chan struct{}),
		},
		origConn: base,
	}
	c.log = opts.logger().With(slog.Any("connection", c))
	//nolint:forcetypeassert
	c.conn = netutil.WrapPacketConn([]netutil.PacketConnDecorator{
		netutil.NewContextPacketConnDecorator(),
		netutil.NewCloseOncePacketConnDecorator(),
		netutil.NewLogPacketConnDecorator(c.log, slog.LevelDebug),
	}...)(ctx, base).(netutil.ContextPacketConn)

	return c, nil
}

func (c *packetConn) LocalAddr() netip.AddrPort {
	if c == nil {
		return netip.AddrPort{}
	}
	return c.laddr
}

func (c *packetConn) listenAddr() netip.AddrPort {
	return c.LocalAddr()
}

func (*packetConn) RemoteAddr() netip.AddrPort {
	return netip.AddrPort{}
}

func (c *packetConn) Metadata() TransportMetadata {
	if c == nil {
		return TransportMetadata{}
	}
	return c.meta
}

func (c *packetConn) LogValue() slog.Value {
	if c == nil {
		return slog.Value{}
	}

	return slog.GroupValue(
		slog.Any("proto", c.meta.Proto),
		slog.Any("network", c.meta.Network),
		slog.Any("local_addr", c.laddr),
	)
}

func (c *packetConn) Logger() *slog.Logger {
	if c == nil {
		return nil
	}
	return c.log
}

func (c *packetConn) Close() error {
	if c == nil {
		return nil
	}

	c.closeOnce.Do(func() {
		c.closeErr = c.conn.Close()
		close(c.closed)
	})

	return errors.Wrap(c.closeErr)
}

func (c *packetConn) WriteTo(ctx context.Context, b []byte, addr netip.AddrPort) (int, error) {
	if c.isClosed() {
		return 0, errors.Wrap(net.ErrClosed)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, ConnWriteTimeout)
		defer cancel()
	}

	return errors.Wrap2(c.conn.WriteToContext(ctx, b, netutil.AddrPortToNetAddr(c.meta.Network, addr)))
}

func (c *packetConn) ReadFrom(ctx context.Context, b []byte) (int, netip.AddrPort, error) {
	if c.isClosed() {
		return 0, netip.AddrPort{}, errors.Wrap(net.ErrClosed)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, ConnReadTimeout)
		defer cancel()
	}

	n, addr, err := c.conn.ReadFromContext(ctx, b)
	if err != nil {
		return 0, netip.AddrPort{}, errors.Wrap(err)
	}

	return n, netutil.UnmapAddrPort(netip.MustParseAddrPort(addr.String())), nil
}

func (c *packetConn) Messages(ctx context.Context) iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		go func() {
			select {
			case <-ctx.Done():
				c.Close()
			case <-c.closed:
			}
		}()

		for msg, err := range c.packetMsgs(ctx, c, c.recvKeepAliveCRLF) {
			if !yield(msg, errors.Wrap(err)) {
				break
			}
		}
	}
}

func (c *packetConn) recvKeepAliveCRLF(ctx context.Context, kaType uint8, raddr netip.AddrPort) {
	if c.isClosed() {
		return
	}

	switch kaType {
	case keepAlivePingCRLF:
		// connection-less transports like UDP should ping/pong via STUN multiplexing
		// be be liberal on possible double CRLF pings and send CRLF pongs
		c.WriteTo(ctx, crlf, raddr) //nolint:errcheck
	case keepAlivePongCRLF:
		// nothing to do, ignore
	}
}

func (c *packetConn) WriteMessage(ctx context.Context, msg Message, addr netip.AddrPort, opts *RenderOptions) error {
	if c.isClosed() {
		return errors.Wrap(net.ErrClosed)
	}

	if err := msg.Validate(); err != nil {
		return errors.NewInvalidArgumentErrorWrap(err)
	}

	bb := util.GetBytesBuffer()
	defer util.FreeBytesBuffer(bb)

	if _, err := msg.RenderTo(bb, opts); err != nil {
		return errors.Wrap(err)
	}

	// if uint(bb.Len()) > MTU-200 {
	// 	// this is a very unlikely case, but we should still check
	// 	// selecting the correct transport must be done in the upper layer
	// 	// TODO: do we need to try render in compact form automatically?
	// 	return errors.NewInvalidArgumentErrorWrap(ErrMessageTooLarge)
	// }

	if _, err := c.WriteTo(ctx, bb.Bytes(), addr); err != nil {
		return errors.Wrap(err)
	}

	return nil
}

type netPacketConnAdapter struct {
	ctx context.Context
	*packetConn
}

func (c *netPacketConnAdapter) LocalAddr() net.Addr {
	return netutil.AddrPortToNetAddr(c.meta.Network, c.laddr)
}

func (c *netPacketConnAdapter) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	n, a, err := c.packetConn.ReadFrom(c.ctx, p)
	return n, netutil.AddrPortToNetAddr(c.meta.Network, a), errors.Wrap(err)
}

func (c *netPacketConnAdapter) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return errors.Wrap2(c.packetConn.WriteTo(c.ctx, p, netip.MustParseAddrPort(addr.String())))
}

func (c *netPacketConnAdapter) Close() error {
	if c == nil {
		return nil
	}
	return errors.Wrap(c.packetConn.Close())
}

func (c *netPacketConnAdapter) SetDeadline(t time.Time) error {
	return errors.Wrap(c.conn.SetDeadline(t))
}

func (c *netPacketConnAdapter) SetReadDeadline(t time.Time) error {
	return errors.Wrap(c.conn.SetReadDeadline(t))
}

func (c *netPacketConnAdapter) SetWriteDeadline(t time.Time) error {
	return errors.Wrap(c.conn.SetWriteDeadline(t))
}

func (c *netPacketConnAdapter) Unwrap() net.PacketConn {
	return c.conn
}

func (c *packetConn) AsNetPacketConn(ctx context.Context) net.PacketConn {
	if c == nil {
		return nil
	}
	return &netPacketConnAdapter{ctx, c}
}

type conn struct {
	connBase
	origConn net.Conn
	conn     netutil.ContextConn
	// sip level TTL based on successful read/write messages or keep-alive pings
	ttl    time.Duration
	ttlTmr atomic.Pointer[time.Timer]
}

func newConn(ctx context.Context, base net.Conn, meta TransportMetadata, opts *ConnOptions) (*conn, error) {
	if base == nil {
		return nil, errors.NewInvalidArgumentErrorWrap("nil connection")
	}

	if base.RemoteAddr() == nil {
		return nil, errors.NewInvalidArgumentErrorWrap("unconnected socket not allowed here")
	}

	if !meta.IsValid() {
		return nil, errors.NewInvalidArgumentErrorWrap("invalid metadata %q", meta)
	}

	if v1, v2 := meta.Network, base.LocalAddr().Network(); !util.EqFold(v1, v2) {
		return nil, errors.NewInvalidArgumentErrorWrap(
			"metadata and connection networks mismatch: %q != %q",
			v1, v2,
		)
	}

	c := &conn{
		connBase: connBase{
			meta:   meta.Canonic(),
			laddr:  netutil.UnmapAddrPort(netip.MustParseAddrPort(base.LocalAddr().String())),
			raddr:  netutil.UnmapAddrPort(netip.MustParseAddrPort(base.RemoteAddr().String())),
			prs:    opts.parser(),
			closed: make(chan struct{}),
		},
		origConn: base,
		ttl:      opts.ttl(),
	}
	c.log = opts.logger().With(slog.Any("connection", c))
	//nolint:forcetypeassert
	c.conn = netutil.WrapConn([]netutil.ConnDecorator{
		netutil.NewContextConnDecorator(),
		netutil.NewAutoCloseConnDecorator(c.ttl), // connection level TTL, based on any successful read/write operations
		netutil.NewCloseOnceConnDecorator(),
		netutil.NewLogConnDecorator(c.log, slog.LevelDebug),
	}...)(ctx, base).(netutil.ContextConn)
	c.resetTTL()

	return c, nil
}

func (c *conn) resetTTL() {
	if c.ttl <= 0 {
		return
	}

	if tmr := c.ttlTmr.Load(); tmr == nil {
		c.ttlTmr.Store(time.AfterFunc(c.ttl, func() { c.Close() }))
	} else if !tmr.Reset(c.ttl) {
		// timer was already expired
		tmr.Stop()
	}
}

func (c *conn) LocalAddr() netip.AddrPort {
	if c == nil {
		return netip.AddrPort{}
	}
	return c.laddr
}

func (c *conn) RemoteAddr() netip.AddrPort {
	if c == nil {
		return netip.AddrPort{}
	}
	return c.raddr
}

func (c *conn) Metadata() TransportMetadata {
	if c == nil {
		return TransportMetadata{}
	}
	return c.meta
}

func (c *conn) LogValue() slog.Value {
	if c == nil {
		return slog.Value{}
	}

	return slog.GroupValue(
		slog.Any("proto", c.meta.Proto),
		slog.Any("network", c.meta.Network),
		slog.Any("local_addr", c.laddr),
		slog.Any("remote_addr", c.raddr),
	)
}

func (c *conn) Logger() *slog.Logger {
	if c == nil {
		return nil
	}
	return c.log
}

func (c *conn) Close() error {
	if c == nil {
		return nil
	}

	if tmr := c.ttlTmr.Swap(nil); tmr != nil {
		tmr.Stop()
	}

	c.closeOnce.Do(func() {
		c.closeErr = c.conn.Close()
		close(c.closed)
	})

	return errors.Wrap(c.closeErr)
}

func (c *conn) Write(ctx context.Context, buf []byte) (int, error) {
	if c.isClosed() {
		return 0, errors.Wrap(net.ErrClosed)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, ConnWriteTimeout)
		defer cancel()
	}

	n, err := c.conn.WriteContext(ctx, buf)
	if err != nil {
		return 0, errors.Wrap(err)
	}

	c.resetTTL() // reset TTL on our successful write

	return n, nil
}

func (c *conn) Read(ctx context.Context, buf []byte) (int, error) {
	if c.isClosed() {
		return 0, errors.Wrap(net.ErrClosed)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, ConnReadTimeout)
		defer cancel()
	}

	n, err := c.conn.ReadContext(ctx, buf)
	if err != nil {
		return 0, errors.Wrap(err)
	}

	return n, nil
}

func (c *conn) Messages(ctx context.Context) iter.Seq2[Message, error] {
	var msgs iter.Seq2[Message, error]
	if c.meta.Streamed() {
		msgs = c.streamMsgs(ctx, c, c.recvKeepAlive)
	} else {
		msgs = c.packetMsgs(ctx, &streamToPacketAdapter{c},
			func(ctx context.Context, kaType uint8, raddr netip.AddrPort) {
				c.recvKeepAlive(ctx, kaType)
			},
		)
	}

	return func(yield func(Message, error) bool) {
		go func() {
			select {
			case <-ctx.Done():
				c.Close()
			case <-c.closed:
			}
		}()

		for msg, err := range msgs {
			if msg != nil {
				c.resetTTL() // reset TTL when we received a message, ignoring any incoming trash
			}

			if !yield(msg, errors.Wrap(err)) {
				break
			}
		}
	}
}

func (c *conn) recvKeepAlive(ctx context.Context, kaType uint8) {
	if c.isClosed() {
		return
	}

	switch kaType {
	case keepAlivePingCRLF:
		c.resetTTL()

		if _, err := c.Write(ctx, crlf); err != nil {
			c.log.LogAttrs(ctx, slog.LevelWarn, "failed to send CRLF pong", slog.Any("error", err))
		}
		// TODO: fire event or callback for upper layer
	case keepAlivePongCRLF:
		c.resetTTL()
		// TODO: confirm running ping
	}
}

func (c *conn) WriteMessage(ctx context.Context, msg Message, addr netip.AddrPort, opts *RenderOptions) error {
	if c.isClosed() {
		return errors.Wrap(net.ErrClosed)
	}

	addr = netutil.UnmapAddrPort(addr)
	if addr != c.raddr {
		return errors.NewInvalidArgumentErrorWrap("can't write to %q", addr)
	}

	if err := msg.Validate(); err != nil {
		return errors.NewInvalidArgumentErrorWrap(err)
	}

	bb := util.GetBytesBuffer()
	defer util.FreeBytesBuffer(bb)

	if _, err := msg.RenderTo(bb, opts); err != nil {
		return errors.Wrap(err)
	}

	if _, err := c.Write(ctx, bb.Bytes()); err != nil {
		return errors.Wrap(err)
	}

	return nil
}

func (c *conn) VerifyHost(host string) error {
	if !c.meta.Secured() {
		return nil
	}

	vc, ok := netutil.AsConn[interface{ VerifyHostname(h string) error }](c.conn)
	if !ok {
		// TODO: do we need to force hostname verification for any secured connection?
		//       maybe return some specific error
		return nil
	}

	return errors.Wrap(vc.VerifyHostname(host))
}

type netConnAdapter struct {
	ctx context.Context
	*conn
}

func (c *netConnAdapter) LocalAddr() net.Addr {
	return netutil.AddrPortToNetAddr(c.meta.Network, c.laddr)
}

func (c *netConnAdapter) RemoteAddr() net.Addr {
	return netutil.AddrPortToNetAddr(c.meta.Network, c.raddr)
}

func (c *netConnAdapter) Read(b []byte) (int, error) {
	return errors.Wrap2(c.conn.Read(c.ctx, b))
}

func (c *netConnAdapter) Write(b []byte) (int, error) {
	return errors.Wrap2(c.conn.Write(c.ctx, b))
}

func (c *netConnAdapter) Close() error {
	if c == nil {
		return nil
	}
	return errors.Wrap(c.conn.Close())
}

func (c *netConnAdapter) SetDeadline(t time.Time) error {
	return errors.Wrap(c.conn.conn.SetDeadline(t))
}

func (c *netConnAdapter) SetReadDeadline(t time.Time) error {
	return errors.Wrap(c.conn.conn.SetReadDeadline(t))
}

func (c *netConnAdapter) SetWriteDeadline(t time.Time) error {
	return errors.Wrap(c.conn.conn.SetWriteDeadline(t))
}

func (c *netConnAdapter) Unwrap() net.Conn {
	return c.conn.conn
}

func (c *conn) AsNetConn(ctx context.Context) net.Conn {
	if c == nil {
		return nil
	}
	return &netConnAdapter{ctx, c}
}

type connListener struct {
	net.Listener
	origLis net.Listener
	addr    netip.AddrPort
	closed  atomic.Bool
}

func newConnListener(ctx context.Context, base net.Listener, logger *slog.Logger) *connListener {
	l := &connListener{
		origLis: base,
		addr:    netutil.UnmapAddrPort(netip.MustParseAddrPort(base.Addr().String())),
	}
	l.Listener = netutil.WrapListener([]netutil.ListenerDecorator{
		netutil.NewCloseOnceListenerDecorator(),
		netutil.NewLogListenerDecorator(logger.With(slog.Any("listener", l)), slog.LevelDebug),
	}...)(ctx, base)

	return l
}

func (l *connListener) Close() error {
	if l == nil {
		return nil
	}

	l.closed.Store(true)

	return errors.Wrap(l.Listener.Close())
}

func (l *connListener) listenAddr() netip.AddrPort {
	if l == nil {
		return netip.AddrPort{}
	}
	return l.addr
}

func (l *connListener) isClosed() bool {
	return l.closed.Load()
}

func (l *connListener) String() string {
	if l == nil {
		return sNilTag
	}
	return fmt.Sprintf("%s %s", l.Addr().Network(), l.addr)
}

func (l *connListener) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		f.Write([]byte(l.String())) //nolint:errcheck
		return
	case 'q':
		f.Write([]byte(strconv.Quote(l.String()))) //nolint:errcheck
		return
	default:
		type (
			hideMethods  connListener
			connListener hideMethods
		)

		fmt.Fprintf(f, fmt.FormatString(f, verb), (*connListener)(l))

		return
	}
}

func (l *connListener) Unwrap() net.Listener {
	if l == nil {
		return nil
	}
	return l.Listener
}
