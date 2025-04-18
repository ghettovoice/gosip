package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip"
)

// reliableBase is a base for connection-oriented transport such as TCP.
// Reliable transport can be stream-oriented or packet-oriented.
type reliableBase struct {
	opts  Options
	proto sip.TransportProto
	streamed,
	secured bool
	listen func(context.Context, netip.AddrPort, ...any) (net.Listener, error)
	dial   func(context.Context, netip.AddrPort, netip.AddrPort, ...any) (net.Conn, error)

	listenerTracker
	connTracker
	messageHandler

	closing atomic.Bool
}

func (tp *reliableBase) Proto() sip.TransportProto { return tp.proto }

func (*reliableBase) Reliable() bool { return true }

func (tp *reliableBase) Secured() bool { return tp.secured }

func (tp *reliableBase) Streamed() bool { return tp.streamed }

func (tp *reliableBase) Shutdown() error {
	tp.closing.Store(true)

	errs := make([]error, 0, 2)
	if err := tp.listenerTracker.closeAll(); err != nil {
		errs = append(errs, fmt.Errorf("close listeners: %w", err))
	}
	if err := tp.connTracker.closeAll(); err != nil {
		errs = append(errs, fmt.Errorf("close connections: %w", err))
	}
	return errors.Join(errs...)
}

func (tp *reliableBase) ListenAndServe(ctx context.Context, laddr netip.AddrPort, opts ...any) (err error) {
	if tp.closing.Load() {
		return sip.ErrTransportClosed
	}

	var ls net.Listener
	ls, err = tp.listen(ctx, laddr, opts...)
	if err != nil {
		return err
	}

	ls = newCloseOnceListener(ls)
	defer ls.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		ls.Close()
	}()
	defer func() {
		if !errors.Is(err, sip.ErrTransportClosed) {
			if e := ctx.Err(); e != nil {
				err = ctx.Err()
			}
		}
	}()

	return tp.Serve(ls)
}

func (tp *reliableBase) Serve(ls net.Listener) error {
	ls = newCloseOnceListener(ls)
	defer ls.Close()

	if tp.closing.Load() {
		return sip.ErrTransportClosed
	}

	tp.trackListener(ls)
	defer tp.untrackListener(ls)

	logger := tp.opts.log().With(sip.LocalAddrField, ls.Addr())
	var tempDelay time.Duration
	for {
		c, err := ls.Accept()
		if err != nil {
			if tp.closing.Load() {
				return sip.ErrTransportClosed
			}
			if utils.IsTemporaryErr(err) {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if v := time.Second; tempDelay > v {
					tempDelay = v
				}
				logger.Warn(
					"failed to accept connection due to the temporary error; continue serving the listener...",
					"error", err,
					"retry_after", tempDelay,
				)
				time.Sleep(tempDelay)
				continue
			}
			return err
		}

		rc := tp.newConn(c)
		rc.fromLs = true
		tp.trackConn(rc, rc.locAddrPort(), rc.rmtAddrPort())
		go rc.serve() //nolint:errcheck
	}
}

func (tp *reliableBase) newConn(c net.Conn) *reliableConn {
	rc := &reliableConn{
		Conn: newLogConn(newCloseOnceConn(c), tp.opts.log()),
		opts: tp.opts,
		tp:   tp,
	}
	rc.opts.Log = rc.opts.log().With("connection", rc)
	rc.idleTracker.expired = func() { rc.Close() }
	return rc
}

func (tp *reliableBase) SendRequest(ctx context.Context, req *sip.Request, raddr netip.AddrPort, opts ...any) error {
	c, err := tp.getOrDial(ctx, zeroAddrPort, raddr, opts...)
	if err != nil {
		return err
	}
	return c.WriteMessage(ctx, req, opts...)
}

func (tp *reliableBase) SendResponse(ctx context.Context, res *sip.Response, laddr netip.AddrPort, opts ...any) error {
	if tp.closing.Load() {
		return sip.ErrTransportClosed
	}

	// First, try to send the response via opened connection matching local address.
	for v := range tp.conns.AllKey(laddr) {
		c := v.(*reliableConn) //nolint:forcetypeassert
		if err := c.WriteMessage(ctx, res, opts...); err == nil {
			return nil
		} else if !isNetError(err) {
			// If it fails due to network error,
			// then resolve a list of alternative addresses each one until success or all fail.
			return err
		}
		break
	}

	// fallback to dial a new connection
	var errs []error
	for raddr := range tp.opts.respAddrResolver().ResponseAddrs(res) {
		c, err := tp.getOrDial(ctx, laddr, raddr, opts...)
		if err != nil {
			errs = append(errs, err)
			if errors.Is(err, sip.ErrTransportClosed) {
				break
			}
			continue
		}

		if err = c.WriteMessage(ctx, res, opts...); err == nil {
			return nil
		} else if !isNetError(err) {
			errs = append(errs, err)
			break
		}
	}
	if len(errs) == 0 {
		return ErrNoAddrResolved
	}
	return errors.Join(errs...)
}

func (tp *reliableBase) getOrDial(ctx context.Context, laddr, raddr netip.AddrPort, opts ...any) (*reliableConn, error) {
	if tp.closing.Load() {
		return nil, sip.ErrTransportClosed
	}

	if raddr.IsValid() && raddr.Port() == 0 {
		raddr = netip.AddrPortFrom(raddr.Addr(), DefaultPort(tp.proto))
	}

	// first, try to get by local address
	if laddr.IsValid() {
		for c := range tp.conns.AllKey(laddr) {
			return c.(*reliableConn), nil //nolint:forcetypeassert
		}
	}
	// or try to get by remote address
	for c := range tp.conns.AllKey(raddr) {
		return c.(*reliableConn), nil //nolint:forcetypeassert
	}

	// or dial new
	c, err := tp.dial(ctx, laddr, raddr, opts...)
	if err != nil {
		return nil, err
	}
	rc := tp.newConn(c)
	tp.trackConn(rc, rc.locAddrPort(), rc.rmtAddrPort())
	go rc.serve() //nolint:errcheck
	return rc, nil
}

func (tp *reliableBase) Stats() sip.TransportReport {
	return sip.TransportReport{
		Proto:                     tp.proto,
		Listeners:                 uint32(tp.lssNum.Load()),
		Connections:               uint32(tp.connsNum.Load()),
		InboundRequests:           tp.inReqNum.Load(),
		InboundResponses:          tp.inResNum.Load(),
		InboundRequestsRejected:   tp.inReqRejectNum.Load(),
		InboundResponsesRejected:  tp.inResRejectNum.Load(),
		OutboundRequests:          tp.outReqNum.Load(),
		OutboundResponses:         tp.outResNum.Load(),
		OutboundRequestsRejected:  tp.outReqRejectNum.Load(),
		OutboundResponsesRejected: tp.outResRejectNum.Load(),
		MessageRTT:                time.Duration(tp.rtt.Load()),
		MessageRTTMeasurements:    tp.rttMeas.Load(),
	}
}

func (tp *reliableBase) trackListener(ls any) {
	tp.listenerTracker.trackListener(ls)
	tp.opts.log().Debug("listener is tracked", "listener", ls)
}

func (tp *reliableBase) untrackListener(ls any) {
	tp.listenerTracker.untrackListener(ls)
	tp.opts.log().Debug("listener is untracked", "listener", ls)
}

func (tp *reliableBase) trackConn(c any, key netip.AddrPort, keys ...netip.AddrPort) {
	tp.connTracker.trackConn(c, key, keys...)
	if tp.opts.log().Enabled(context.Background(), slog.LevelDebug) {
		ks := make([]netip.AddrPort, 1+len(keys))
		ks[0] = key
		copy(ks[1:], keys)
		tp.opts.log().Debug("connection is tracked", "keys", ks, "connection", c)
	}
}

func (tp *reliableBase) untrackConn(c any, key netip.AddrPort, keys ...netip.AddrPort) {
	tp.connTracker.untrackConn(c, key, keys...)
	if tp.opts.log().Enabled(context.Background(), slog.LevelDebug) {
		ks := make([]netip.AddrPort, 1+len(keys))
		ks[0] = key
		copy(ks[1:], keys)
		tp.opts.log().Debug("connection is untracked", "keys", ks, "connection", c)
	}
}

func (tp *reliableBase) options() *Options { return &tp.opts }

type reliableConn struct {
	net.Conn
	opts Options
	tp   *reliableBase
	// fromLs is true if the connection local addr is the same as the listener
	fromLs bool

	idleTracker
}

func (c *reliableConn) fromListener() bool { return c.fromLs }

func (c *reliableConn) locAddrPort() netip.AddrPort {
	return netip.MustParseAddrPort(c.LocalAddr().String())
}

func (c *reliableConn) rmtAddrPort() netip.AddrPort {
	return netip.MustParseAddrPort(c.RemoteAddr().String())
}

func (c *reliableConn) Close() error {
	c.stopIdleTTL()
	return c.Conn.Close()
}

func (c *reliableConn) serve() error {
	defer func() {
		if err := recover(); err != nil {
			c.opts.log().Error("panic occurred while serving the connection", "error", err)
		}
		c.Close()
		c.tp.untrackConn(c, c.locAddrPort(), c.rmtAddrPort())
	}()

	c.updateTTL()
	if c.tp.streamed {
		return serveStream(c, c.opts.parser(), c.onInMsg, c.opts.log())
	}
	return servePacket(c, c.opts.parser(), c.onInMsg, c.opts.log())
}

func (c *reliableConn) onInMsg(ctx context.Context, msg sip.Message) error {
	if c.tp.onInMsg(ctx, c.tp, c, msg) {
		c.updateTTL()
	}
	return nil
}

func (c *reliableConn) WriteMessage(ctx context.Context, msg sip.Message, _ ...any) error {
	bb, err := c.onOutMsg(ctx, msg)
	if err != nil {
		return err
	}
	defer freeBytesBuf(bb)

	defer func() {
		if err != nil {
			switch msg.(type) {
			case *sip.Request:
				c.tp.outReqRejectNum.Add(1)
			case *sip.Response:
				c.tp.outResRejectNum.Add(1)
			}
		}
	}()

	if err = c.writeMsg(ctx, bb); err != nil {
		return err
	}

	switch msg.(type) {
	case *sip.Request:
		c.opts.log().Info("outbound request sent", "request", msg)
	case *sip.Response:
		c.opts.log().Info("outbound response sent", "response", msg)
	}

	c.updateTTL()
	return nil
}

func (c *reliableConn) writeMsg(ctx context.Context, bb *bytes.Buffer) error {
	if d, ok := ctx.Deadline(); ok {
		if err := c.SetWriteDeadline(d); err != nil {
			return err
		}
		defer c.SetWriteDeadline(noDeadline)
	}
	_, err := c.Write(bb.Bytes())
	return err
}

func (c *reliableConn) onOutMsg(ctx context.Context, msg sip.Message) (*bytes.Buffer, error) {
	ctx = context.WithValue(ctx, loggerKey{}, c.opts.log())
	return c.tp.onOutMsg(ctx, c.tp, c, msg)
}

func (c *reliableConn) updateTTL() {
	c.idleTracker.updateIdleTTL(c.opts.connIdleTTL(), c.opts.log())
}

func (c *reliableConn) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", fmt.Sprintf("%T", c)),
		slog.String("ptr", fmt.Sprintf("%p", c)),
		slog.Any("local_addr", c.LocalAddr()),
		slog.Any("remote_addr", c.RemoteAddr()),
	)
}
