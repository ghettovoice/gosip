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
	opts    Options
	proto   sip.TransportProto
	secured bool
	listen  func(context.Context, netip.AddrPort, ...any) (net.PacketConn, error)
	dial    func(context.Context, netip.AddrPort, netip.AddrPort, ...any) (net.PacketConn, error)

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

func (tp *unreliableBase) ListenAndServe(ctx context.Context, laddr netip.AddrPort, opts ...any) (err error) {
	if tp.closing.Load() {
		return sip.ErrTransportClosed
	}

	var ls net.PacketConn
	ls, err = tp.listen(ctx, laddr, opts...)
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
	defer func() {
		if !errors.Is(err, sip.ErrTransportClosed) {
			if e := ctx.Err(); e != nil {
				err = ctx.Err()
			}
		}
	}()

	return tp.Serve(ls)
}

func (tp *unreliableBase) Serve(ls net.PacketConn) error {
	ls = newCloseOncePacketConn(ls)
	defer ls.Close()

	if tp.closing.Load() {
		return sip.ErrTransportClosed
	}

	c := tp.newConn(ls, zeroAddrPort)
	c.opts.ConnIdleTTL = -1 // infinite for listener packet connections
	c.fromLs = true

	tp.trackListener(c)
	defer tp.untrackListener(c)

	err := c.serve()
	if tp.closing.Load() {
		err = sip.ErrTransportClosed
	}
	return err
}

func (tp *unreliableBase) newConn(c net.PacketConn, raddr netip.AddrPort) *unreliableConn {
	uc := &unreliableConn{
		PacketConn: newLogPacketConn(newCloseOncePacketConn(c), tp.opts.log()),
		opts:       tp.opts,
		tp:         tp,
		rmtAddr:    raddr,
	}
	uc.opts.Log = uc.opts.log().With("connection", uc)
	uc.idleTracker.expired = func() { uc.Close() }
	return uc
}

func (tp *unreliableBase) SendRequest(ctx context.Context, req *sip.Request, raddr netip.AddrPort, opts ...any) error {
	c, err := tp.getOrDial(ctx, zeroAddrPort, raddr, opts...)
	if err != nil {
		return err
	}
	return c.WriteMessage(ctx, req, raddr, opts...)
}

func (tp *unreliableBase) SendResponse(ctx context.Context, res *sip.Response, laddr netip.AddrPort, opts ...any) error {
	c, err := tp.getOrDial(ctx, laddr, zeroAddrPort, opts...)
	if err != nil {
		return err
	}

	var errs []error
	for raddr := range tp.opts.respAddrResolver().ResponseAddrs(res) {
		if err = c.WriteMessage(ctx, res, raddr, opts...); err == nil {
			return nil
		} else if !isNetError(err) {
			errs = append(errs, err)
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
	if len(errs) == 0 {
		return ErrNoAddrResolved
	}
	return errors.Join(errs...)
}

func (tp *unreliableBase) getOrDial(ctx context.Context, laddr, raddr netip.AddrPort, opts ...any) (*unreliableConn, error) {
	if tp.closing.Load() {
		return nil, sip.ErrTransportClosed
	}

	if raddr.IsValid() && raddr.Port() == 0 {
		raddr = netip.AddrPortFrom(raddr.Addr(), DefaultPort(tp.proto))
	}

	// first, try to get by local addr (there could be connections only in no listener case)
	if laddr.IsValid() {
		for c := range tp.conns.AllKey(laddr) {
			return c.(*unreliableConn), nil //nolint:forcetypeassert
		}
	}
	// or try to get by remote address
	for c := range tp.conns.AllKey(raddr) {
		return c.(*unreliableConn), nil //nolint:forcetypeassert
	}

	// or try to get one of the listener connections (common case)
	var uc *unreliableConn
	tp.lss.Range(func(v, _ any) bool {
		c := v.(*unreliableConn) //nolint:forcetypeassert
		if !laddr.IsValid() || c.locAddrPort() == laddr {
			uc = c
			return false
		}
		return true
	})
	if uc != nil {
		return uc, nil
	}

	// or make a new temporary connection (no listener case)
	c, err := tp.dial(ctx, laddr, raddr, opts...)
	if err != nil {
		return nil, err
	}
	uc = tp.newConn(c, raddr)
	tp.trackConn(uc, uc.locAddrPort(), uc.rmtAddr)
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
	// rmtAddr is non-zero only for outbound connections in no listener case.
	// It is used to track such connections also by remote address.
	rmtAddr netip.AddrPort
	// fromLs is true if the connection local addr is the same as the listener
	fromLs bool

	idleTracker
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
			c.tp.untrackConn(c, c.locAddrPort(), c.rmtAddr)
		}
	}()

	c.updateTTL()
	return servePacket(c, c.opts.parser(), c.onInMsg, c.opts.log())
}

func (c *unreliableConn) onInMsg(ctx context.Context, msg sip.Message) error {
	if c.tp.onInMsg(ctx, c.tp, c, msg) {
		c.updateTTL()
	}
	return nil
}

func (c *unreliableConn) WriteMessage(ctx context.Context, msg sip.Message, raddr netip.AddrPort, _ ...any) error {
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

	c.opts.log().Info("message sent", "message", msg, "remote_addr", raddr)

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
	_, err := c.WriteTo(bb.Bytes(), addrPortToNetAddr(Network(c.tp.proto), raddr))
	return err
}

func (c *unreliableConn) onOutMsg(ctx context.Context, msg sip.Message, raddr netip.AddrPort) (*bytes.Buffer, error) {
	ctx = context.WithValue(ctx, rmtAddrKey{}, raddr)
	ctx = context.WithValue(ctx, loggerKey{}, c.opts.log().With(sip.RemoteAddrField, raddr))
	return c.tp.onOutMsg(ctx, c.tp, c, msg)
}

func (c *unreliableConn) updateTTL() {
	c.idleTracker.updateIdleTTL(c.opts.connIdleTTL(), c.opts.log())
}

func (c *unreliableConn) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("type", fmt.Sprintf("%T", c)),
		slog.String("ptr", fmt.Sprintf("%p", c)),
		slog.Any("local_addr", c.LocalAddr()),
	)
}
