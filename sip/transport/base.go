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
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/ghettovoice/gosip/internal/iterutils"
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

type connReader struct{}

type rmtAddrKey struct{}

type loggerKey struct{}

type parseErrKey struct{}

type responderKey struct{}

func (*connReader) servePacket(ac any, p sip.Parser, onMsgFn func(context.Context, sip.Message) error, logger *slog.Logger) error {
	pc, isPktConn := ac.(net.PacketConn)
	c, isConn := ac.(net.Conn)
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

		msg, err := p.ParsePacket(buf[:num])
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
			msgLogger.Warn("inbound message parsing error", "data", log.StringValue(buf[:num]), "message", msg, "error", err)
			msgCtx = context.WithValue(msgCtx, parseErrKey{}, err)
		}

		msgLogger.Info("message received", "message", msg, "dump", log.CalcValue(func() any { return msg.Render() }))

		msgCtx = context.WithValue(msgCtx, loggerKey{}, msgLogger)
		if err = onMsgFn(msgCtx, msg); err != nil {
			msgLogger.Warn("failed to accept inbound message", "error", err)
		}
	}
}

func (*connReader) serveStream(c net.Conn, p sip.Parser, onMsgFn func(context.Context, sip.Message) error, logger *slog.Logger) error {
	ctx := context.Background()
	r := &io.LimitedReader{R: c, N: int64(sip.MaxMsgSize)}
	sp := p.ParseStream(r)
	for msg, err := range sp.Messages() {
		msgCtx := ctx
		msgLogger := logger

		if err != nil {
			isTooLong := (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) && r.N <= 0
			isGramErr := utils.IsGrammarErr(err)
			if !(isTooLong || isGramErr) {
				return err
			}

			// If the error is due to exceeding the message size limit, we should continue parsing the connection.
			// Also, any grammar errors should not break the parsing loop.
			r.N = int64(sip.MaxMsgSize)
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
					var (
						perr *sip.ParseError
						data []byte
					)
					if errors.As(err, &perr) {
						data = perr.Buf
					}
					msgLogger.Warn("inbound message parsing error", "data", log.StringValue(data), "message", msg, "error", err)
				}
				msgCtx = context.WithValue(msgCtx, parseErrKey{}, err)
			}
		}

		r.N = int64(sip.MaxMsgSize)

		msgLogger.Info("message received", "message", msg, "dump", log.CalcValue(func() any { return msg.Render() }))

		msgCtx = context.WithValue(msgCtx, loggerKey{}, msgLogger)
		if err = onMsgFn(msgCtx, msg); err != nil {
			msgLogger.Warn("failed to accept inbound message", "error", err)
		}
	}
	return io.EOF
}

type connIdleTracker struct {
	conn io.Closer
	tmr  atomic.Pointer[time.Timer]
}

func (c *connIdleTracker) updateIdleTTL(ttl time.Duration, logger *slog.Logger) {
	if ttl <= 0 {
		return
	}
	if tmr := c.tmr.Load(); tmr == nil {
		c.tmr.Store(time.AfterFunc(ttl, func() { c.conn.Close() }))
	} else if !tmr.Reset(ttl) {
		tmr.Stop()
	}
	logger.Debug("connection TTL updated", "ttl", ttl)
}

func (c *connIdleTracker) stopIdleTTL() {
	if tmr := c.tmr.Load(); tmr != nil {
		tmr.Stop()
	}
}

type messageHandler struct {
	onInReqFn  atomic.Value // func(context.Context, *Request, ResponseWriter) error
	onInResFn  atomic.Value // func(context.Context, *Response) error
	onOutReqFn atomic.Value // func(context.Context, *Request) error
	onOutResFn atomic.Value // func(context.Context, *Response) error

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

func (mh *messageHandler) OnInboundRequest(fn func(context.Context, *sip.Request, sip.ResponseWriter) error) {
	mh.onInReqFn.Store(fn)
}

func (mh *messageHandler) OnInboundResponse(fn func(context.Context, *sip.Response) error) {
	mh.onInResFn.Store(fn)
}

func (mh *messageHandler) OnOutboundRequest(fn func(context.Context, *sip.Request) error) {
	mh.onOutReqFn.Store(fn)
}

func (mh *messageHandler) OnOutboundResponse(fn func(context.Context, *sip.Response) error) {
	mh.onOutResFn.Store(fn)
}

type baseTransp interface {
	Proto() sip.TransportProto
	Streamed() bool
	options() *Options
	listenPorts(prepend []uint16) iter.Seq[uint16]
}

type baseConn interface {
	fromListener() bool
	locAddrPort() netip.AddrPort
}

//nolint:gocognit
func (mh *messageHandler) onInMsg(ctx context.Context, tp baseTransp, c baseConn, msg sip.Message) (accepted bool) {
	var logger *slog.Logger
	if l, k := ctx.Value(loggerKey{}).(*slog.Logger); k && l != nil {
		logger = l
	} else {
		logger = log.Noop
	}

	laddr := c.locAddrPort()
	var raddr netip.AddrPort
	if rc, k := c.(interface{ rmtAddrPort() netip.AddrPort }); k {
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

	_, viaHop := iterutils.IterFirst2(sip.GetMessageHeaders(msg).ViaHops())
	now := time.Now().UTC()

	switch m := msg.(type) {
	case *sip.Request:
		mh.inReqNum.Add(1)
		defer func() {
			if !accepted {
				mh.inReqRejectNum.Add(1)
			}
		}()

		rspd, ok := ctx.Value(responderKey{}).(sip.ResponseWriter)
		if !ok {
			panic(errMissResWrt)
		}

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
			logger.Info("discarding invalid inbound request")
			return false
		}

		if prsErr, ok := ctx.Value(parseErrKey{}).(error); ok {
			// We faced some errors during parsing of the request, but the parsed request contains mandatory headers,
			// then we can respond to it.
			logger.Info("discarding inbound request due to parsing error")

			sts := sip.ResponseStatusBadRequest
			if errors.Is(prsErr, sip.ErrMessageTooLarge) {
				sts = sip.ResponseStatusMessageTooLarge
			}
			resCtx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			if err := rspd.Write(resCtx, sts); err != nil {
				logger.Warn(
					fmt.Sprintf(`failed to respond "%d %s" to invalid inbound request`, sts, sip.ResponseStatusReason(sts)),
					"error", err,
				)
			}
			return false
		}

		if fn, ok := mh.onInReqFn.Load().(func(context.Context, *sip.Request, sip.ResponseWriter) error); ok && fn != nil {
			if err := fn(ctx, m, rspd); err != nil {
				logger.Warn("failed to accept inbound request", "error", err)
				return false
			}
			logger.Debug("inbound request accepted")
		} else {
			logger.Warn("discarding inbound request, because no inbound request handler is set")
		}
	case *sip.Response:
		mh.inResNum.Add(1)
		defer func() {
			if !accepted {
				mh.inResRejectNum.Add(1)
			}
		}()

		m.Metadata[sip.ResponseTstampField] = now

		logger = logger.With("response", m)

		if !m.IsValid() {
			logger.Info("discarding invalid inbound response")
			return false
		}
		if _, ok := ctx.Value(parseErrKey{}).(error); ok {
			logger.Info("discarding inbound response due to parsing error")
			return false
		}
		// RFC 3261 Section 18.1.2.
		if stringutils.LCase(viaHop.Addr.Host()) != stringutils.LCase(tp.options().sentByHost()) {
			logger.Info(fmt.Sprintf(`discarding inbound response due to Via's "sent-by" mismatch with transport's host = %q`, tp.options().sentByHost()))
			return false
		}

		if hdrs := m.Headers.Get("Timestamp"); len(hdrs) > 0 {
			if ts, ok := hdrs[0].(*header.Timestamp); ok && !ts.ReqTime.IsZero() && now.After(ts.ReqTime) {
				n := mh.rttMeas.Add(1)
				rtt := uint64(now.Sub(ts.ReqTime) - ts.ResDelay)
				mh.rtt.Store((mh.rtt.Load()*(n-1) + rtt) / n)
			}
		}

		if fn, ok := mh.onInResFn.Load().(func(context.Context, *sip.Response) error); ok && fn != nil {
			if err := fn(ctx, m); err != nil {
				logger.Warn("failed to accept inbound message", "error", err)
				return false
			}
			logger.Debug("inbound message accepted")
		} else {
			logger.Warn("discarding inbound message, because no inbound message handler is set")
		}
	}
	return true
}

//nolint:gocognit
func (mh *messageHandler) onOutMsg(ctx context.Context, tp baseTransp, c baseConn, msg sip.Message) (bb *bytes.Buffer, err error) {
	laddr := c.locAddrPort()
	var raddr netip.AddrPort
	if rc, ok := c.(interface{ rmtAddrPort() netip.AddrPort }); ok {
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
		mh.outReqNum.Add(1)
		defer func() {
			if err != nil {
				mh.outReqRejectNum.Add(1)
			}
		}()

		m.Metadata[sip.RequestTstampField] = now

		// RFC 3261 Section 18.1.1.
		// the transport user must prepend Via with zero Addr and fill other fields by itself
		_, viaHop := iterutils.IterFirst2(m.Headers.ViaHops())
		if viaHop == nil || !viaHop.Addr.IsZero() {
			return nil, sip.ErrInvalidMessage
		}

		viaHop.Transport = tp.Proto()

		var locPorts []uint16
		if c.fromListener() {
			locPorts = []uint16{laddr.Port()}
		}
		viaHop.Addr = tp.options().sentByBuild(tp.options().sentByHost(), tp.listenPorts(locPorts))

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

		if fn, ok := mh.onOutReqFn.Load().(func(context.Context, *sip.Request) error); ok && fn != nil {
			if err = fn(ctx, m); err != nil {
				return nil, err
			}
		}
	case *sip.Response:
		mh.outResNum.Add(1)
		defer func() {
			if err != nil {
				mh.outResRejectNum.Add(1)
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

		if fn, ok := mh.onOutResFn.Load().(func(context.Context, *sip.Response) error); ok && fn != nil {
			if err = fn(ctx, m); err != nil {
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

type remoteAddrResolver struct {
	dns *net.Resolver
}

// ResponseRemoteAddrs returns a list of remote addresses resolving them step by step,
// according to RFC 3261 Section 18.2.2, RFC 3263 Section 5 and RFC 3581 Section 4.
//
//nolint:gocognit
func (ar *remoteAddrResolver) ResponseRemoteAddrs(res *sip.Response) iter.Seq[netip.AddrPort] {
	return func(yield func(netip.AddrPort) bool) {
		_, viaHop := iterutils.IterFirst2(res.Headers.ViaHops())
		if viaHop == nil || !viaHop.IsValid() {
			return
		}

		if !IsReliable(viaHop.Transport) {
			// RFC 3261 Section 18.2.2, bullet 2.
			if maddr := viaHop.Params.Last("maddr"); maddr != "" {
				if addr, err := netip.ParseAddr(maddr); err == nil {
					var port uint16
					if p, ok := viaHop.Addr.Port(); ok && p > 0 {
						port = p
					} else {
						port = DefaultPort(viaHop.Transport)
					}
					yield(netip.AddrPortFrom(addr, port))
					// no fallback to RFC 3263 Section 5 is defined for "maddr" case,
					// so we stop here.
					return
				}
			}
		}

		// RFC 3261 Section 18.2.2, bullet 1 and 3.
		if recv := viaHop.Params.Last("received"); recv != "" {
			if addr, err := netip.ParseAddr(recv); err == nil {
				var port uint16
				if !IsReliable(viaHop.Transport) {
					// RFC 3581 Section 4.
					if rport := viaHop.Params.Last("rport"); rport != "" {
						if p, err := strconv.ParseUint(rport, 10, 16); err == nil {
							port = uint16(p)
						}
					}
				}
				if port == 0 {
					if p, ok := viaHop.Addr.Port(); ok && p > 0 {
						port = p
					} else {
						port = DefaultPort(viaHop.Transport)
					}
				}
				if !yield(netip.AddrPortFrom(addr, port)) {
					return
				}
			}
		}

		// RFC 3261 Section 18.2.2, bullet 4, i.e. fallback to RFC 3263 Section 5.
		if viaHop.Addr.IP() != nil {
			if addr, ok := netip.AddrFromSlice(viaHop.Addr.IP()); ok {
				var port uint16
				if p, ok := viaHop.Addr.Port(); ok && p > 0 {
					port = p
				} else {
					port = DefaultPort(viaHop.Transport)
				}
				if !yield(netip.AddrPortFrom(addr, port)) {
					return
				}
			}
		} else {
			if port, ok := viaHop.Addr.Port(); ok && port > 0 {
				if ips, err := ar.dns.LookupIP(context.TODO(), "ip", viaHop.Addr.Host()); err == nil {
					for _, ip := range ips {
						if addr, ok := netip.AddrFromSlice(ip); ok {
							if !yield(netip.AddrPortFrom(addr, port)) {
								return
							}
						}
					}
				}
			} else {
				serv := "sip"
				if IsSecured(viaHop.Transport) {
					serv = "sips"
				}

				if _, srvs, err := ar.dns.LookupSRV(context.TODO(), serv, Network(viaHop.Transport), viaHop.Addr.Host()); err == nil {
					srvs = slices.SortedFunc(slices.Values(srvs), func(e1, e2 *net.SRV) int {
						switch {
						case e1.Priority < e2.Priority:
							return -1
						case e1.Priority > e2.Priority:
							return 1
						case e1.Weight > e2.Weight:
							return -1
						case e1.Weight < e2.Weight:
							return 1
						default:
							return strings.Compare(e1.Target, e2.Target)
						}
					})
					for _, srv := range srvs {
						if ips, err := ar.dns.LookupIP(context.TODO(), "ip", srv.Target); err == nil {
							for _, ip := range ips {
								if addr, ok := netip.AddrFromSlice(ip); ok {
									if !yield(netip.AddrPortFrom(addr, srv.Port)) {
										return
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

type responseBuilder struct {
	req  *sip.Request
	hdrs sip.Headers
	tag  string
}

func (b *responseBuilder) Headers() sip.Headers { return b.hdrs }

func (b *responseBuilder) SetTag(tag string) { b.tag = tag }

func (b *responseBuilder) buildResponse(sts sip.ResponseStatus, opts ...any) (*sip.Response, error) {
	var (
		reason  string
		body    []byte
		cntType *header.ContentType
	)
	for _, opt := range opts {
		switch v := opt.(type) {
		case string:
			reason = v
		case []byte:
			body = v
		case header.MIMEType:
			ct := header.ContentType(v)
			cntType = &ct
		}
	}

	res := sip.NewResponse(b.req, sts, reason)
	if to := res.Headers.To(); to != nil && b.tag != "" {
		to.Params.Set("tag", b.tag)
	}
	for n, hs := range res.Headers {
		switch n.ToCanonic() {
		case "Via", "From", "To", "Call-ID", "CSeq", "Timestamp":
			continue
		default:
			for _, h := range hs {
				res.Headers.Append(h)
			}
		}
	}
	if body != nil {
		res.Body = body
		if !cntType.IsValid() {
			mt := mimetype.Detect(body)
			ct, _ := header.Parse("Content-Type: "+mt.String(), nil)
			cntType, _ = ct.(*header.ContentType)
		}
		res.Headers.Set(cntType)
	}

	return res, nil
}
