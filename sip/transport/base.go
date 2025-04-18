package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/gosip/internal/log"
	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

type listenerTracker struct {
	lss    sync.Map
	lssNum atomic.Int64
	lssWg  sync.WaitGroup
}

func (t *listenerTracker) trackListener(ls any) {
	t.lss.Store(ls, struct{}{})
	t.lssNum.Add(1)
	t.lssWg.Add(1)
}

func (t *listenerTracker) untrackListener(ls any) {
	t.lss.Delete(ls)
	t.lssNum.Add(-1)
	t.lssWg.Done()
}

func (t *listenerTracker) closeAll() error {
	var errs []error
	t.lss.Range(func(ls, _ any) bool {
		switch ls := ls.(type) {
		case net.Listener:
			errs = append(errs, ls.Close())
		case net.PacketConn:
			errs = append(errs, ls.Close())
		}
		return true
	})
	t.lssWg.Wait()
	return errors.Join(errs...)
}

func (t *listenerTracker) listenPorts(prepend []uint16) iter.Seq[uint16] {
	return func(yield func(uint16) bool) {
		passed := make(map[uint16]struct{}, len(prepend))
		for _, p := range prepend {
			passed[p] = struct{}{}
			if !yield(p) {
				return
			}
		}

		t.lss.Range(func(ls, _ any) bool {
			var addr net.Addr
			switch ls := ls.(type) {
			case net.Listener:
				addr = ls.Addr()
			case net.PacketConn:
				addr = ls.LocalAddr()
			default:
				return true
			}
			_, portStr, err := net.SplitHostPort(addr.String())
			if err != nil {
				return true
			}
			port, err := strconv.ParseUint(portStr, 10, 16)
			if err != nil {
				return true
			}
			if _, ok := passed[uint16(port)]; ok {
				return true
			}
			return yield(uint16(port))
		})
	}
}

type connTracker struct {
	conns    connPool
	connsNum atomic.Int64
	connsWg  sync.WaitGroup
}

func (t *connTracker) trackConn(c any, key netip.AddrPort, keys ...netip.AddrPort) {
	t.conns.Add(key, c)
	for _, k := range keys {
		t.conns.Add(k, c)
	}
	t.connsNum.Add(1)
	t.connsWg.Add(1)
}

func (t *connTracker) untrackConn(c any, key netip.AddrPort, keys ...netip.AddrPort) {
	t.conns.Del(key, c)
	for _, k := range keys {
		t.conns.Del(k, c)
	}
	t.connsNum.Add(-1)
	t.connsWg.Done()
}

func (t *connTracker) closeAll() error {
	var errs []error //nolint:prealloc
	for c := range t.conns.All() {
		errs = append(errs, c.(io.Closer).Close()) //nolint:forcetypeassert
	}
	t.connsWg.Wait()
	return errors.Join(errs...)
}

type rmtAddrKey struct{}

type loggerKey struct{}

type parseErrKey struct{}

func servePacket(anyConn any, prs sip.Parser, onMsgFn func(context.Context, sip.Message) error, logger *slog.Logger) error {
	pc, isPktConn := anyConn.(net.PacketConn)
	c, isConn := anyConn.(net.Conn)
	if !isPktConn && !isConn {
		return unexpectConnTypeError(c)
	}

	ctx := context.Background()
	buf := make([]byte, sip.MaxMsgSize)
	for {
		var (
			num   int
			raddr net.Addr
			err   error
		)
		if isPktConn {
			num, raddr, err = pc.ReadFrom(buf)
		} else {
			num, err = c.Read(buf)
		}
		if err != nil {
			return err
		}

		msgCtx := ctx
		if isPktConn {
			msgCtx = context.WithValue(msgCtx, rmtAddrKey{}, netip.MustParseAddrPort(raddr.String()))
		}
		msgLogger := logger.With("remote_addr", raddr)

		msg, err := prs.ParsePacket(buf[:num])
		if err != nil {
			// All errors from parser are considered continuable.
			if msg == nil {
				// It seems we get something that is not a SIP message
				// because we failed to parse even a start line.
				// So ignore any inbound trash and go further.
				continue
			}
			// If a message was partially read, log a warning.
			// It will be rejected later in onInMsg method.
			msgLogger.Warn("inbound message parsing error", "message", msg, "error", err)
			msgCtx = context.WithValue(msgCtx, parseErrKey{}, err)
		}

		msgLogger.Debug("message received", "message", msg)

		msgCtx = context.WithValue(msgCtx, loggerKey{}, msgLogger)
		if err = onMsgFn(msgCtx, msg); err != nil {
			msgLogger.Warn("failed to accept inbound message", "error", err)
		}
	}
}

func serveStream(conn net.Conn, prs sip.Parser, onMsgFn func(context.Context, sip.Message) error, logger *slog.Logger) error {
	ctx := context.Background()
	rd := &io.LimitedReader{R: conn, N: int64(sip.MaxMsgSize)}
	sp := prs.ParseStream(rd)
	for msg, err := range sp.Messages() {
		msgCtx := ctx
		msgLogger := logger

		if err != nil {
			isTooLong := (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) && rd.N <= 0
			isGramErr := utils.IsGrammarErr(err)
			if !(isTooLong || isGramErr) {
				return err
			}

			// If the error is due to exceeding the message size limit, we should continue parsing the connection.
			// Also, any grammar errors should not break the parsing loop.
			rd.N = int64(sip.MaxMsgSize)
			if msg == nil {
				continue
			}
			// If a message was partially read, log a warning.
			// It will be rejected later in onInMsg method.
			if isTooLong {
				err = sip.ErrMessageTooLarge
				if msgLogger.Enabled(msgCtx, slog.LevelWarn) {
					msgLogger.Warn(
						fmt.Sprintf(
							"failed to read inbound message due to exceeding the message size limit %d",
							sip.MaxMsgSize,
						),
						"message", msg,
						"error", err,
					)
				}
				msgCtx = context.WithValue(msgCtx, parseErrKey{}, err)
			} else {
				if msgLogger.Enabled(msgCtx, slog.LevelWarn) {
					msgLogger.Warn("inbound message parsing error", "message", msg, "error", err)
				}
				msgCtx = context.WithValue(msgCtx, parseErrKey{}, err)
			}
		}

		rd.N = int64(sip.MaxMsgSize)

		msgLogger.Debug("message received", "message", msg)

		msgCtx = context.WithValue(msgCtx, loggerKey{}, msgLogger)
		if err = onMsgFn(msgCtx, msg); err != nil {
			msgLogger.Warn("failed to accept inbound message", "error", err)
		}
	}
	return io.EOF
}

type idleTracker struct {
	expired func()
	tmr     atomic.Pointer[time.Timer]
}

func (t *idleTracker) updateIdleTTL(ttl time.Duration, logger *slog.Logger) {
	if ttl <= 0 {
		return
	}
	if tmr := t.tmr.Load(); tmr == nil {
		t.tmr.Store(time.AfterFunc(ttl, t.expired))
	} else if !tmr.Reset(ttl) {
		tmr.Stop()
	}
	logger.Debug("connection TTL updated", "ttl", ttl)
}

func (t *idleTracker) stopIdleTTL() {
	if tmr := t.tmr.Load(); tmr != nil {
		tmr.Stop()
	}
}

type messageHandler struct {
	inReqHdlr  atomic.Value // sip.RequestHandler
	inResHdlr  atomic.Value // sip.ResponseHandler
	outReqHdlr atomic.Value // sip.RequestHandler
	outResHdlr atomic.Value // sip.ResponseHandler

	inReqNum,
	inReqRejectNum,
	inResNum,
	inResRejectNum,
	outReqNum,
	outReqRejectNum,
	outResNum,
	outResRejectNum atomic.Uint64

	rtt,
	rttMeas atomic.Uint64
}

func (h *messageHandler) OnInboundRequest(hdlr sip.RequestHandler) { h.inReqHdlr.Store(hdlr) }

func (h *messageHandler) OnInboundResponse(hdlr sip.ResponseHandler) { h.inResHdlr.Store(hdlr) }

func (h *messageHandler) OnOutboundRequest(hdlr sip.RequestHandler) { h.outReqHdlr.Store(hdlr) }

func (h *messageHandler) OnOutboundResponse(hdlr sip.ResponseHandler) { h.outResHdlr.Store(hdlr) }

type baseTransp interface {
	Proto() sip.TransportProto
	Streamed() bool
	SendResponse(ctx context.Context, res *sip.Response, laddr netip.AddrPort, opts ...any) error
	options() *Options
	listenPorts(prepend []uint16) iter.Seq[uint16]
}

type baseConn interface {
	fromListener() bool
	locAddrPort() netip.AddrPort
}

//nolint:gocognit
func (h *messageHandler) onInMsg(ctx context.Context, tp baseTransp, conn baseConn, msg sip.Message) (accepted bool) {
	var logger *slog.Logger
	if l, k := ctx.Value(loggerKey{}).(*slog.Logger); k && l != nil {
		logger = l
	} else {
		logger = log.Noop
	}

	laddr := conn.locAddrPort()
	var raddr netip.AddrPort
	if rc, k := conn.(interface{ rmtAddrPort() netip.AddrPort }); k {
		// reliable transport is connection-oriented, so the connection always has a remote address
		raddr = rc.rmtAddrPort()
	} else {
		// unreliable transport is connection-less, so the remote address is in the context
		var ok bool
		if raddr, ok = ctx.Value(rmtAddrKey{}).(netip.AddrPort); !(ok && raddr.IsValid()) {
			panic(invalidRemoteAddrError(raddr))
		}
	}

	addMsgMdFields(msg,
		sip.TransportField, tp.Proto(),
		sip.LocalAddrField, laddr,
		sip.RemoteAddrField, raddr,
	)

	viaHop := sip.FirstHeaderElem[header.Via](sip.GetMessageHeaders(msg), "Via")
	now := time.Now().UTC()

	switch m := msg.(type) {
	case *sip.Request:
		h.inReqNum.Add(1)
		defer func() {
			if !accepted {
				h.inReqRejectNum.Add(1)
			}
		}()

		m.Metadata[sip.RequestTstampField] = now

		if viaHop != nil {
			// RFC 3261 Section 18.2.1.
			if viaHop.Addr.IP() == nil || !viaHop.Addr.IP().Equal(raddr.Addr().AsSlice()) {
				viaHop.Params.Set("received", raddr.Addr().String())
			}
			// RFC 3581 Section 4.
			if viaHop.Params.Has("rport") {
				viaHop.Params.Set("rport", strconv.Itoa(int(raddr.Port())))
			}
		}

		logger = logger.With("request", m)

		if !m.IsValid() {
			// If the request is invalid, i.e., missing mandatory headers, we can't respond.
			logger.Warn("discarding invalid inbound request")
			return false
		}

		if prsErr, ok := ctx.Value(parseErrKey{}).(error); ok {
			// We faced some errors during parsing of the request, but the parsed request contains mandatory headers,
			// then we can respond to it.
			logger.Warn("discarding inbound request due to parsing error")

			sts := sip.ResponseStatusBadRequest
			if errors.Is(prsErr, sip.ErrMessageTooLarge) {
				sts = sip.ResponseStatusMessageTooLarge
			}
			resCtx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			if err := tp.SendResponse(resCtx, sip.NewResponse(m, sts), laddr); err != nil {
				logger.Warn(
					fmt.Sprintf(
						`failed to respond "%d %s" to invalid inbound request`,
						sts,
						sts.Reason(),
					),
					"error", err,
				)
			}
			return false
		}

		if hdlr, ok := h.inReqHdlr.Load().(sip.RequestHandler); ok && hdlr != nil {
			if err := hdlr.HandleRequest(ctx, m); err != nil {
				logger.Warn("failed to accept inbound request", "error", err)
				return false
			}
			logger.Info("inbound request accepted")
		} else {
			logger.Warn("discarding inbound request, because no inbound request handler is set")
		}
	case *sip.Response:
		h.inResNum.Add(1)
		defer func() {
			if !accepted {
				h.inResRejectNum.Add(1)
			}
		}()

		m.Metadata[sip.ResponseTstampField] = now

		logger = logger.With("response", m)

		if !m.IsValid() {
			logger.Warn("discarding invalid inbound response")
			return false
		}
		if _, ok := ctx.Value(parseErrKey{}).(error); ok {
			logger.Warn("discarding inbound response due to parsing error")
			return false
		}
		// RFC 3261 Section 18.1.2.
		if stringutils.LCase(viaHop.Addr.Host()) != stringutils.LCase(tp.options().sentByHost()) {
			logger.Warn(fmt.Sprintf(`discarding inbound response due to Via's "sent-by" mismatch with transport's host = %q`, tp.options().sentByHost()))
			return false
		}

		if hdrs := m.Headers.Get("Timestamp"); len(hdrs) > 0 {
			if ts, ok := hdrs[0].(*header.Timestamp); ok && !ts.ReqTime.IsZero() && now.After(ts.ReqTime) {
				n := h.rttMeas.Add(1)
				rtt := uint64(now.Sub(ts.ReqTime) - ts.ResDelay)
				h.rtt.Store((h.rtt.Load()*(n-1) + rtt) / n)
			}
		}

		if hdlr, ok := h.inResHdlr.Load().(sip.ResponseHandler); ok && hdlr != nil {
			if err := hdlr.HandleResponse(ctx, m); err != nil {
				logger.Warn("failed to accept inbound message", "error", err)
				return false
			}
			logger.Info("inbound response accepted")
		} else {
			logger.Warn("discarding inbound message, because no inbound response handler is set")
		}
	}
	return true
}

//nolint:gocognit
func (h *messageHandler) onOutMsg(ctx context.Context, tp baseTransp, conn baseConn, msg sip.Message) (bb *bytes.Buffer, err error) {
	laddr := conn.locAddrPort()
	var raddr netip.AddrPort
	if rc, ok := conn.(interface{ rmtAddrPort() netip.AddrPort }); ok {
		// reliable transport is connection-oriented, so the connection always has a remote address
		raddr = rc.rmtAddrPort()
	} else {
		// unreliable transport is connection-less, so the remote address is in the context
		if raddr, ok = ctx.Value(rmtAddrKey{}).(netip.AddrPort); !(ok && raddr.IsValid()) {
			panic(invalidRemoteAddrError(raddr))
		}
	}

	addMsgMdFields(msg,
		sip.TransportField, tp.Proto(),
		sip.LocalAddrField, laddr,
		sip.RemoteAddrField, raddr,
	)

	now := time.Now().UTC()

	switch m := msg.(type) {
	case *sip.Request:
		h.outReqNum.Add(1)
		defer func() {
			if err != nil {
				h.outReqRejectNum.Add(1)
			}
		}()

		m.Metadata[sip.RequestTstampField] = now

		// RFC 3261 Section 18.1.1.
		// the transport user must prepend Via by itself
		viaHop := sip.FirstHeaderElem[header.Via](m.Headers, "Via")
		if viaHop == nil {
			return nil, sip.ErrInvalidMessage
		}

		viaHop.Transport = tp.Proto()

		var locPorts []uint16
		if conn.fromListener() {
			locPorts = []uint16{laddr.Port()}
		}
		viaHop.Addr = tp.options().sentByBuild(tp.options().sentByHost(), tp.listenPorts(locPorts))

		if raddr.Addr().IsMulticast() {
			viaHop.Params.Set("maddr", raddr.String()).Set("ttl", "1")
		}

		var ts *header.Timestamp
		if hdrs := m.Headers.Get("Timestamp"); len(hdrs) > 0 {
			var ok bool
			if ts, ok = hdrs[0].(*header.Timestamp); ok && ts.ReqTime.IsZero() {
				ts.ReqTime = now
				ts.ResDelay = 0
			} else {
				ts = &header.Timestamp{ReqTime: now}
			}
		} else {
			ts = &header.Timestamp{ReqTime: now}
		}
		m.Headers.Set(ts)

		if hdlr, ok := h.outReqHdlr.Load().(sip.RequestHandler); ok && hdlr != nil {
			if err = hdlr.HandleRequest(ctx, m); err != nil {
				return nil, err
			}
		}
	case *sip.Response:
		h.outResNum.Add(1)
		defer func() {
			if err != nil {
				h.outResRejectNum.Add(1)
			}
		}()

		// RFC 3261 Section 18.2.2.
		m.Metadata[sip.ResponseTstampField] = now

		if hdrs := m.Headers.Get("Timestamp"); len(hdrs) > 0 {
			if ts, ok := hdrs[0].(*header.Timestamp); ok {
				if reqTS, ok := m.Metadata[sip.RequestTstampField].(time.Time); ok && !reqTS.IsZero() {
					ts.ResDelay = now.Sub(reqTS)
				}
			}
		}

		if hdlr, ok := h.outResHdlr.Load().(sip.ResponseHandler); ok && hdlr != nil {
			if err = hdlr.HandleResponse(ctx, m); err != nil {
				return nil, err
			}
		}
	}

	if !utils.IsValid(msg) {
		return nil, sip.ErrInvalidMessage
	}

	if tp.Streamed() {
		sip.GetMessageHeaders(msg).Set(header.ContentLength(len(sip.GetMessageBody(msg))))
	}

	bb = newBytesBuf()
	defer func() {
		if err != nil {
			freeBytesBuf(bb)
			bb = nil
		}
	}()
	if err = msg.RenderTo(bb); err != nil {
		return bb, err
	}
	if _, ok := msg.(*sip.Request); ok && !tp.Streamed() && bb.Len() > sip.MTU-200 {
		// this is a very unlikely case, but we should still check
		// selecting the correct transport must be done in the upper layer
		return bb, sip.ErrMessageTooLarge
	}
	return bb, nil
}
