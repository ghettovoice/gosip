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

	"github.com/ghettovoice/gosip/sip"
)

// unreliableBase is a base for connection-less transports such as UDP.
// It is also always packet-oriented.
type unreliableBase struct {
	opts           Options
	proto          sip.TransportProto
	secured        bool
	addrPortToAddr func(netip.AddrPort) net.Addr
	listen         func(context.Context, netip.AddrPort, ...any) (net.PacketConn, error)
	dial           func(context.Context, netip.AddrPort, ...any) (net.PacketConn, error)

	listenerTracker
	connTracker
	messageHandler

	closing atomic.Bool
}

func (tp *unreliableBase) Proto() sip.TransportProto { return tp.proto }

func (*unreliableBase) Reliable() bool { return false }

func (tp *unreliableBase) Secured() bool { return tp.secured }

func (*unreliableBase) Streamed() bool { return false }

func (tp *unreliableBase) Shutdown() error {
	tp.closing.Store(true)

	var errs []error
	if err := tp.listenerTracker.closeAll(); err != nil {
		errs = append(errs, fmt.Errorf("close listeners: %w", err))
	}
	if err := tp.connTracker.closeAll(); err != nil {
		errs = append(errs, fmt.Errorf("close connections: %w", err))
	}
	return errors.Join(errs...)
}

func (tp *unreliableBase) ListenAndServe(ctx context.Context, addr netip.AddrPort, opts ...any) error {
	if tp.closing.Load() {
		return ErrTransportClosed
	}

	ls, err := tp.listen(ctx, addr, opts...)
	if err != nil {
		return err
	}

	ls = newCloseOncePacketConn(ls)
	defer ls.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		ls.Close()
	}()

	return tp.Serve(ls)
}

func (tp *unreliableBase) Serve(ls net.PacketConn) error {
	ls = newCloseOncePacketConn(ls)
	defer ls.Close()

	if tp.closing.Load() {
		return ErrTransportClosed
	}

	c := tp.newConn(ls, netip.AddrPort{})
	c.opts.ConnIdleTTL = -1 // infinite for listener packet connections
	c.fromLs = true

	tp.trackListener(c)
	defer tp.untrackListener(c)

	err := c.serve()
	if tp.closing.Load() {
		err = ErrTransportClosed
	}
	return err
}

func (tp *unreliableBase) newConn(c net.PacketConn, raddr netip.AddrPort) *unreliableConn {
	uc := &unreliableConn{
		PacketConn: newLogPacketConn(newCloseOncePacketConn(c), tp.opts.log()),
		opts:       tp.opts,
		tp:         tp,
		keyAddr:    raddr,
	}
	uc.opts.Log = uc.opts.log().With("connection", uc)
	uc.connIdleTracker.conn = uc
	return uc
}

func (tp *unreliableBase) GetOrDial(ctx context.Context, addr netip.AddrPort, opts ...any) (sip.RequestWriter, error) {
	c, err := tp.getOrDial(ctx, addr, opts...)
	if err != nil {
		return nil, err
	}
	return newUnreliableRequestWriter(tp, c, addr), nil
}

func (tp *unreliableBase) getOrDial(ctx context.Context, addr netip.AddrPort, opts ...any) (*unreliableConn, error) {
	if tp.closing.Load() {
		return nil, ErrTransportClosed
	}

	if addr.Port() == 0 {
		addr = netip.AddrPortFrom(addr.Addr(), DefaultPort(tp.proto))
	}

	// first, try to get by remote address
	for c := range tp.conns.AllKey(addr) {
		return c.(*unreliableConn), nil //nolint:forcetypeassert
	}

	// or try to get one of packet listener connections
	var uc *unreliableConn
	tp.lss.Range(func(c, _ any) bool {
		uc = c.(*unreliableConn) //nolint:forcetypeassert
		return false
	})
	if uc != nil {
		return uc, nil
	}

	// or make a new
	c, err := tp.dial(ctx, addr, opts...)
	if err != nil {
		return nil, err
	}
	uc = tp.newConn(c, addr)
	tp.trackConn(uc, uc.keyAddr)
	go uc.serve() //nolint:errcheck
	return uc, nil
}

func (tp *unreliableBase) Stats() sip.TransportReport {
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

func (tp *unreliableBase) trackListener(ls any) {
	tp.listenerTracker.trackListener(ls)
	tp.opts.log().Debug("listener is tracked", "listener", ls)
}

func (tp *unreliableBase) untrackListener(ls any) {
	tp.listenerTracker.untrackListener(ls)
	tp.opts.log().Debug("listener is untracked", "listener", ls)
}

func (tp *unreliableBase) trackConn(c any, key netip.AddrPort, keys ...netip.AddrPort) {
	tp.connTracker.trackConn(c, key, keys...)
	if tp.opts.log().Enabled(context.Background(), slog.LevelDebug) {
		ks := make([]netip.AddrPort, 1+len(keys))
		ks[0] = key
		copy(ks[1:], keys)
		tp.opts.log().Debug("connection is tracked", "keys", ks, "connection", c)
	}
}

func (tp *unreliableBase) untrackConn(c any, key netip.AddrPort, keys ...netip.AddrPort) {
	tp.connTracker.untrackConn(c, key, keys...)
	if tp.opts.log().Enabled(context.Background(), slog.LevelDebug) {
		ks := make([]netip.AddrPort, 1+len(keys))
		ks[0] = key
		copy(ks[1:], keys)
		tp.opts.log().Debug("connection is untracked", "keys", ks, "connection", c)
	}
}

func (tp *unreliableBase) options() *Options { return &tp.opts }

type unreliableConn struct {
	net.PacketConn
	opts Options
	tp   *unreliableBase
	// keyAddr is non-zero only for outbound connections.
	// It is used to track such connections also by remote address.
	keyAddr netip.AddrPort
	// fromLs is true if the connection local addr is the same as the listener
	fromLs bool

	connReader
	connIdleTracker
}

func (c *unreliableConn) fromListener() bool { return c.fromLs }

func (c *unreliableConn) locAddrPort() netip.AddrPort {
	return netip.MustParseAddrPort(c.LocalAddr().String())
}

func (c *unreliableConn) Close() error {
	c.stopIdleTTL()
	return c.PacketConn.Close()
}

func (c *unreliableConn) serve() error {
	defer func() {
		if err := recover(); err != nil {
			c.opts.log().Error("panic occurred while serving the connection", "error", err)
		}
		c.Close()
		if !c.fromLs {
			c.tp.untrackConn(c, c.keyAddr)
		}
	}()

	c.updateTTL()
	return c.servePacket(c, c.opts.parser(), c.onInMsg, c.opts.log())
}

func (c *unreliableConn) onInMsg(ctx context.Context, msg sip.Message) error {
	if req, ok := msg.(*sip.Request); ok {
		ctx = context.WithValue(ctx, responderKey{}, newUnreliableResponseWriter(c.tp, c, req))
	}
	if c.tp.onInMsg(ctx, c.tp, c, msg) {
		c.updateTTL()
	}
	return nil
}

func (c *unreliableConn) WriteMessage(ctx context.Context, msg sip.Message, raddr netip.AddrPort, opts ...any) error {
	bb, err := c.onOutMsg(ctx, msg, raddr)
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

	if err = c.writeMsg(ctx, bb, raddr); err != nil {
		return err
	}

	c.opts.log().Info("message sent", "message", msg, "remote_addr", raddr, "dump", bb)

	c.updateTTL()
	return nil
}

func (c *unreliableConn) writeMsg(ctx context.Context, bb *bytes.Buffer, raddr netip.AddrPort) error {
	if d, ok := ctx.Deadline(); ok {
		if err := c.SetWriteDeadline(d); err != nil {
			return err
		}
		defer c.SetWriteDeadline(noDeadline)
	}
	// TODO if address is multicast, set the TTL equal to "ttl" param from top Via or 1.
	_, err := c.WriteTo(bb.Bytes(), c.tp.addrPortToAddr(raddr))
	return err
}

func (c *unreliableConn) onOutMsg(ctx context.Context, msg sip.Message, raddr netip.AddrPort) (*bytes.Buffer, error) {
	ctx = context.WithValue(ctx, rmtAddrKey{}, raddr)
	ctx = context.WithValue(ctx, loggerKey{}, c.opts.log().With(sip.RemoteAddrField, raddr))
	return c.tp.onOutMsg(ctx, c.tp, c, msg)
}

func (c *unreliableConn) updateTTL() {
	c.connIdleTracker.updateIdleTTL(c.opts.connIdleTTL(), c.opts.log())
}

func (c *unreliableConn) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", fmt.Sprintf("%T", c)),
		slog.String("ptr", fmt.Sprintf("%p", c)),
		slog.Any("local_addr", c.LocalAddr()),
	)
}

type unreliableRequestWriter struct {
	tp    *unreliableBase
	conn  atomic.Pointer[unreliableConn]
	raddr netip.AddrPort
}

func newUnreliableRequestWriter(tp *unreliableBase, c *unreliableConn, raddr netip.AddrPort) *unreliableRequestWriter {
	w := &unreliableRequestWriter{
		tp:    tp,
		raddr: raddr,
	}
	w.conn.Store(c)
	return w
}

func (w *unreliableRequestWriter) RemoteAddr() netip.AddrPort { return w.raddr }

func (w *unreliableRequestWriter) WriteRequest(ctx context.Context, req *sip.Request, opts ...any) error {
	c := w.conn.Load()
	if c == nil {
		panic(errors.New("connection is nil"))
	}
	return c.WriteMessage(ctx, req, w.raddr, opts...)
}

type unreliableResponseWriter struct {
	tp           *unreliableBase
	conn         atomic.Pointer[unreliableConn]
	addrResolver remoteAddrResolver // TODO pass from options
	responseBuilder
}

func newUnreliableResponseWriter(tp *unreliableBase, c *unreliableConn, req *sip.Request) *unreliableResponseWriter {
	w := new(unreliableResponseWriter)
	w.tp = tp
	w.conn.Store(c)
	w.addrResolver.dns = tp.opts.netResolver()
	w.req = req
	w.hdrs = make(sip.Headers)
	return w
}

// Write sends the response via opened connection.
// If sending fails due to network error, it resolves new destinations
// according to RFC 3261 Section 18.2.2, RFC 3263 Section 5.
func (w *unreliableResponseWriter) Write(ctx context.Context, sts sip.ResponseStatus, opts ...any) error {
	res, err := w.buildResponse(sts, opts...)
	if err != nil {
		return err
	}

	var errs []error //nolint:prealloc
	for addr := range w.addrResolver.ResponseRemoteAddrs(res) {
		err := w.conn.Load().WriteMessage(ctx, res, addr, opts...)
		if err == nil {
			return nil
		}

		errs = append(errs, err)
		if !isNetError(err) {
			break
		}
		// TODO maybe need to check certain syscall error codes to detect local/remote issues.
		// If the error related to the local socket, then need to make new connection.
		// https://man7.org/linux/man-pages/man2/sendto.2.html
		//      - syscall.EINVAL - connection socket is in invalid state -> need to make new
		//      - *net.OpError wrapping error:
		//            - syscall error - need to detect that the problem
		//              with remote side (invalid address, not reachable address, port, etc) -> go to the next address;
		//            - net.ErrClosed - connection closed -> create new and re-try with the same address.
	}
	return errors.Join(errs...)
}
