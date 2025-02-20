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
	dial   func(context.Context, netip.AddrPort, ...any) (net.Conn, error)

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

func (tp *reliableBase) ListenAndServe(ctx context.Context, addr netip.AddrPort, opts ...any) error {
	if tp.closing.Load() {
		return ErrTransportClosed
	}

	ls, err := tp.listen(ctx, addr, opts...)
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

	return tp.Serve(ls)
}

func (tp *reliableBase) Serve(ls net.Listener) error {
	ls = newCloseOnceListener(ls)
	defer ls.Close()

	if tp.closing.Load() {
		return ErrTransportClosed
	}

	tp.trackListener(ls)
	defer tp.untrackListener(ls)

	logger := tp.opts.log().With(sip.LocalAddrField, ls.Addr())
	var tempDelay time.Duration
	for {
		c, err := ls.Accept()
		if err != nil {
			if tp.closing.Load() {
				return ErrTransportClosed
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
		tp.trackConn(rc, rc.rmtAddrPort())
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
	rc.connIdleTracker.conn = rc
	return rc
}

func (tp *reliableBase) GetOrDial(ctx context.Context, addr netip.AddrPort, opts ...any) (sip.RequestWriter, error) {
	c, err := tp.getOrDial(ctx, addr, opts...)
	if err != nil {
		return nil, err
	}
	return newReliableRequestWriter(tp, c), nil
}

func (tp *reliableBase) getOrDial(ctx context.Context, addr netip.AddrPort, opts ...any) (*reliableConn, error) {
	if tp.closing.Load() {
		return nil, ErrTransportClosed
	}

	if addr.Port() == 0 {
		addr = netip.AddrPortFrom(addr.Addr(), DefaultPort(tp.proto))
	}

	// first, try to get by remote address
	for c := range tp.conns.AllKey(addr) {
		return c.(*reliableConn), nil //nolint:forcetypeassert
	}

	// or dial new
	c, err := tp.dial(ctx, addr, opts...)
	if err != nil {
		return nil, err
	}
	rc := tp.newConn(c)
	tp.trackConn(rc, rc.rmtAddrPort())
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

	connReader
	connIdleTracker
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
		return c.serveStream(c, c.opts.parser(), c.onInMsg, c.opts.log())
	}
	return c.servePacket(c, c.opts.parser(), c.onInMsg, c.opts.log())
}

func (c *reliableConn) onInMsg(ctx context.Context, msg sip.Message) error {
	if req, ok := msg.(*sip.Request); ok {
		ctx = context.WithValue(ctx, responderKey{}, newReliableResponseWriter(c.tp, c, req))
	}
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

	c.opts.log().Info("message sent", "message", msg, "dump", bb)

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
	c.connIdleTracker.updateIdleTTL(c.opts.connIdleTTL(), c.opts.log())
}

func (c *reliableConn) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", fmt.Sprintf("%T", c)),
		slog.String("ptr", fmt.Sprintf("%p", c)),
		slog.Any("local_addr", c.LocalAddr()),
		slog.Any("remote_addr", c.RemoteAddr()),
	)
}

type reliableRequestWriter struct {
	tp   *reliableBase
	conn atomic.Pointer[reliableConn]
}

func newReliableRequestWriter(tp *reliableBase, c *reliableConn) *reliableRequestWriter {
	w := &reliableRequestWriter{
		tp: tp,
	}
	w.conn.Store(c)
	return w
}

func (w *reliableRequestWriter) RemoteAddr() netip.AddrPort { return w.conn.Load().rmtAddrPort() }

func (w *reliableRequestWriter) WriteRequest(ctx context.Context, req *sip.Request, opts ...any) error {
	c := w.conn.Load()
	if c == nil {
		panic(errors.New("connection is nil"))
	}
	return c.WriteMessage(ctx, req, opts...)
}

type reliableResponseWriter struct {
	tp           *reliableBase
	conn         atomic.Pointer[reliableConn]
	addrResolver remoteAddrResolver
	responseBuilder
}

func newReliableResponseWriter(tp *reliableBase, c *reliableConn, req *sip.Request) *reliableResponseWriter {
	w := new(reliableResponseWriter)
	w.conn.Store(c)
	w.addrResolver.dns = tp.opts.netResolver()
	w.req = req
	w.hdrs = make(sip.Headers)
	return w
}

// Write sends the response via opened connection.
// If sending fails due to network error, it resolves new destinations
// according to RFC 3261 Section 18.2.2, RFC 3263 Section 5.
func (w *reliableResponseWriter) Write(ctx context.Context, sts sip.ResponseStatus, opts ...any) error {
	res, err := w.buildResponse(sts, opts...)
	if err != nil {
		return err
	}

	// First, try to send the response via opened connection.
	if err = w.conn.Load().WriteMessage(ctx, res, opts); err == nil {
		return nil
	}

	// If it fails due to network error,
	// then resolve a list of alternative addresses each one until success or all fail.
	if !isNetError(err) {
		return err
	}

	errs := []error{err}
	for addr := range w.addrResolver.ResponseRemoteAddrs(res) {
		c, err := w.tp.getOrDial(ctx, addr)
		if err != nil {
			errs = append(errs, err)
			if errors.Is(err, ErrTransportClosed) {
				break
			}
			continue
		}
		w.conn.Swap(c).Close()

		if err = c.WriteMessage(ctx, res, opts...); err == nil {
			return nil
		}

		errs = append(errs, err)
		if !isNetError(err) {
			break
		}
	}
	return errors.Join(errs...)
}
