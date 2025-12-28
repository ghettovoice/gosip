package sip

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
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
)

const msgSendTimeout = time.Minute

var (
	zeroTime     time.Time
	zeroAddrPort netip.AddrPort
)

type baseTransp struct {
	ctx        context.Context
	cancCtx    context.CancelFunc
	impl       transpImpl
	meta       TransportMetadata
	laddr      netip.AddrPort
	sentByHost string
	dns        DNSResolver
	log        *slog.Logger

	srvOnce sync.Once
	srvErr  error
	onReq   types.CallbackManager[TransportRequestHandler]
	onRes   types.CallbackManager[TransportResponseHandler]
}

type transpImpl interface {
	writeTo(
		ctx context.Context,
		bb *bytes.Buffer,
		raddr netip.AddrPort,
		opts *transpWriteOpts,
	) (netip.AddrPort, error)
	serve() error
	close() error
}

type transpWriteOpts struct {
	noDialConn bool
}

//nolint:revive
func newBaseTransp(
	ctx context.Context,
	impl transpImpl,
	md TransportMetadata,
	laddr netip.AddrPort,
	sentByHost string,
	dns DNSResolver,
	logger *slog.Logger,
) *baseTransp {
	ctx, cancel := context.WithCancel(ctx)
	tp := &baseTransp{
		ctx:        ctx,
		cancCtx:    cancel,
		impl:       impl,
		meta:       md,
		laddr:      laddr,
		sentByHost: sentByHost,
		dns:        dns,
		log:        logger.With("transport", impl),
	}
	return tp
}

// Proto returns the transport protocol.
func (tp *baseTransp) Proto() TransportProto { return tp.meta.Proto }

// Network returns the transport network.
func (tp *baseTransp) Network() string { return tp.meta.Network }

// LocalAddr returns the transport local address.
func (tp *baseTransp) LocalAddr() netip.AddrPort { return tp.laddr }

// Reliable returns whether the transport is reliable or not.
func (tp *baseTransp) Reliable() bool { return tp.meta.Reliable }

// Secured returns whether the transport is secured or not.
func (tp *baseTransp) Secured() bool { return tp.meta.Secured }

// Streamed returns whether the transport is streamed or not.
func (tp *baseTransp) Streamed() bool { return tp.meta.Streamed }

// DefaultPort returns the transport default port.
// It is used to build remote addresses when no port is specified,
// or during DNS lookup to resolve the message destination.
func (tp *baseTransp) DefaultPort() uint16 { return tp.meta.DefaultPort }

// LogValue builds a [slog.Value] for the transport.
func (tp *baseTransp) LogValue() slog.Value {
	if tp == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.Any("proto", tp.meta.Proto),
		slog.Any("network", tp.meta.Network),
		slog.Any("local_addr", tp.laddr),
	)
}

// Logger returns the transport logger.
func (tp *baseTransp) Logger() *slog.Logger {
	if tp == nil {
		return nil
	}
	return tp.log
}

// SendRequestOptions sends a request to the specified remote address.
//
// Context can be used to cancel the request sending process through the deadline.
//
// The request must have the [header.Via] header with at least one non-zero [header.ViaHop] element,
// this element can have zero (or stub) transport and address fields, they will be filled by the transport.
// See RFC 3261 Section 18.1.1 for details.
//
// Options are optional and can be nil.
func (tp *baseTransp) SendRequest(ctx context.Context, req *OutboundRequest, opts *SendRequestOptions) error {
	// fail fast if transport is already closed
	if tp.isClosed() {
		return errtrace.Wrap(ErrTransportClosed)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.timeout())
		defer cancel()
	}

	req.SetLocalAddr(tp.laddr)

	raddr := req.RemoteAddr()
	if raddr.Port() == 0 {
		raddr = netip.AddrPortFrom(raddr.Addr(), tp.meta.DefaultPort)
	}
	req.SetRemoteAddr(raddr)
	if !raddr.IsValid() {
		return errtrace.Wrap(NewInvalidArgumentError("invalid remote address"))
	}

	req.UpdateMessage(func(msg *Request) {
		if via, ok := msg.Headers.FirstVia(); ok {
			via.Transport = tp.meta.Proto
			via.Addr = header.HostPort(tp.sentByHost, tp.laddr.Port())
		}
		if tp.meta.Streamed && !msg.Headers.Has("Content-Length") {
			msg.Headers.Set(header.ContentLength(len(msg.Body)))
		}
	})

	if err := req.Validate(); err != nil {
		return errtrace.Wrap(NewInvalidArgumentError(err))
	}

	bb := util.GetBytesBuffer()
	defer util.FreeBytesBuffer(bb)
	if _, err := req.RenderTo(bb, opts.rendOpts()); err != nil {
		return errtrace.Wrap(err)
	}

	if !tp.meta.Reliable && uint(bb.Len()) > MTU-200 {
		// this is a very unlikely case, but we should still check
		// selecting the correct transport must be done in the upper layer
		// TODO: do we need to try render in compact form automatically?
		return errtrace.Wrap(NewInvalidArgumentError(ErrMessageTooLarge))
	}

	laddr, err := tp.impl.writeTo(ctx, bb, raddr, nil)
	if err != nil {
		return errtrace.Wrap(tp.resolveWriteErr(ctx, err))
	}
	req.SetLocalAddr(laddr)
	return nil
}

// SendResponseOptions sends a response to a remote address resolved with steps
// defined in RFC 3261 Section 18.2.2. and RFC 3263 Section 5.
//
// Context can be used to cancel the response sending process through the deadline.
// Options are optional and can be nil.
func (tp *baseTransp) SendResponse(ctx context.Context, res *OutboundResponse, opts *SendResponseOptions) error {
	return errtrace.Wrap(tp.sendRes(ctx, res, opts))
}

const errNoResTargets Error = "no response targets resolved"

func (tp *baseTransp) sendRes(ctx context.Context, res *OutboundResponse, opts *SendResponseOptions) error {
	// fail fast if transport is already closed
	if tp.isClosed() {
		return errtrace.Wrap(ErrTransportClosed)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.timeout())
		defer cancel()
	}

	res.SetLocalAddr(tp.laddr)

	var via header.ViaHop
	res.UpdateMessage(func(msg *Response) {
		if tp.meta.Streamed && !msg.Headers.Has("Content-Length") {
			msg.Headers.Set(header.ContentLength(len(msg.Body)))
		}

		if hop, ok := msg.Headers.FirstVia(); ok {
			via = hop.Clone()
		}
	})

	if err := res.Validate(); err != nil {
		return errtrace.Wrap(NewInvalidArgumentError(err))
	}

	if !res.Transport().Equal(tp.meta.Proto) {
		return errtrace.Wrap(NewInvalidArgumentError(
			"transport mismatch: got %q, want %q",
			res.Transport(),
			tp.meta.Proto,
		))
	}

	bb := util.GetBytesBuffer()
	defer util.FreeBytesBuffer(bb)
	if _, err := res.RenderTo(bb, opts.rendOpts()); err != nil {
		return errtrace.Wrap(err)
	}

	if raddr := res.RemoteAddr(); tp.meta.Reliable && raddr.IsValid() {
		// If the "sent-protocol" is a reliable transport protocol such as
		// TCP or SCTP, or TLS over those, the response MUST be sent using
		// the existing connection to the source of the original request
		// that created the transaction, if that connection is still open.
		laddr, err := tp.impl.writeTo(ctx, bb, raddr, &transpWriteOpts{noDialConn: true})
		if err == nil {
			res.SetLocalAddr(laddr)
			return nil
		}
		if err := tp.resolveWriteErr(ctx, err); err != nil {
			if errors.Is(err, ErrTransportClosed) {
				return errtrace.Wrap(err)
			}
		}
	}

	// finally fallback to address resolving procedure defined in RFC 3261 Section 18.2.2. and RFC 3263 Section 5.
	var errs []error
	for _, raddr := range ResponseAddrs(ctx, via, tp.meta, tp.dns) {
		res.SetRemoteAddr(raddr)
		laddr, err := tp.impl.writeTo(ctx, bb, raddr, nil)
		if err == nil {
			res.SetLocalAddr(laddr)
			return nil
		}
		if err := tp.resolveWriteErr(ctx, err); err != nil {
			if errors.Is(err, ErrTransportClosed) {
				return errtrace.Wrap(err)
			}
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return errtrace.Wrap(errNoResTargets)
	}
	return errtrace.Wrap(errorutil.JoinPrefix("all response targets failed:", errs...))
}

func (tp *baseTransp) resolveWriteErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, net.ErrClosed) || tp.ctx.Err() != nil {
		return ErrTransportClosed //errtrace:skip
	}
	if ctx.Err() != nil {
		return ctx.Err() //errtrace:skip
	}
	return err //errtrace:skip
}

// OnRequest registers a request callback.
//
// Multiple callbacks are allowed, they will be called in the order they were registered.
// If all callbacks return fails with error, the request will be automatically rejected with failure response.
//
// Context passed to the callback is canceled when the transport is closed.
// Transport can be retrieved from the context using [TransportFromContext].
func (tp *baseTransp) OnRequest(fn TransportRequestHandler) (cancel func()) {
	return tp.onReq.Add(fn)
}

// OnResponse registers a response callback.
//
// Multiple callbacks are allowed, they will be called in the order they were registered.
// If all callbacks return fails with error, the response will be silently discarded.
//
// Context passed to the callback is canceled when the transport is closed.
// Transport can be retrieved from the context using [TransportFromContext].
func (tp *baseTransp) OnResponse(fn TransportResponseHandler) (cancel func()) {
	return tp.onRes.Add(fn)
}

// Serve starts serving the listener and blocks until the transport is closed.
// Repeated calls return the same terminal error, typically [ErrTransportClosed] after [Transport.Close].
func (tp *baseTransp) Serve() error {
	tp.srvOnce.Do(func() {
		tp.srvErr = tp.impl.serve()
	})
	return errtrace.Wrap(tp.srvErr)
}

// Close closes the transport and underlying listener with all connections.
// It returns any error returned from closing the listener.
func (tp *baseTransp) Close() error {
	tp.cancCtx()
	return errtrace.Wrap(tp.impl.close())
}

func (tp *baseTransp) isClosed() bool {
	select {
	case <-tp.ctx.Done():
		return true
	default:
		return false
	}
}

//nolint:gocognit
func (tp *baseTransp) readMsgs(msgs iter.Seq2[Message, error]) error {
	// ctx := log.ContextWithLogger(tp.ctx, tp.log)
	ctx := tp.ctx

	var tempDelay time.Duration
	for msg, err := range msgs {
		if err != nil {
			var perr *ParseError
			if !errors.As(err, &perr) {
				// conn read errors
				if errorutil.IsTemporaryErr(err) {
					// retry after delay on temp conn errors
					if tempDelay == 0 {
						tempDelay = 5 * time.Millisecond
					} else {
						tempDelay *= 2
					}
					if v := time.Minute; tempDelay > v {
						tempDelay = v
					}

					tp.log.LogAttrs(ctx, slog.LevelDebug,
						"failed to read message due to the temporary error, continue serving after delay...",
						slog.Any("error", err),
						slog.Duration("delay", tempDelay),
					)

					tmr := time.NewTimer(tempDelay)
					select {
					case <-tp.ctx.Done():
						tmr.Stop()
						return errtrace.Wrap(ErrTransportClosed)
					case <-tmr.C:
					}
					continue
				}
				return errtrace.Wrap(err)
			}

			// pass messages with parse errors (incomplete message, missing Content-Length, other ErrInvalidMessage or Grammar errors),
			// they will be discarded below
			msg = perr.Msg
		}

		if msg != nil {
			switch msg := msg.(type) {
			case *InboundRequest:
				if tp.recvReqSafe(ctx, msg, err) {
					return nil
				}
			case *InboundResponse:
				if tp.recvResSafe(ctx, msg, err) {
					return nil
				}
			default:
				tp.log.LogAttrs(ctx, slog.LevelWarn,
					"silently discard inbound message due to unsupported message type",
					slog.Any("error", newUnexpectMsgTypeErr(msg)),
					slog.Any("message", msg),
				)
			}
		}

		if tp.shouldStopReadMsgs(err) {
			return errtrace.Wrap(err)
		}
	}
	return nil
}

func (tp *baseTransp) recvReqSafe(ctx context.Context, req *InboundRequest, err error) (stop bool) {
	defer func() {
		if r := recover(); r != nil {
			tp.log.LogAttrs(ctx, slog.LevelError, "panic in inbound request handler",
				slog.Any("panic", r),
				slog.String("stack", string(debug.Stack())),
				slog.Any("request", req),
			)

			func() {
				defer func() {
					if r2 := recover(); r2 != nil {
						tp.log.LogAttrs(ctx, slog.LevelError, "panic while responding to inbound request handler panic",
							slog.Any("panic", r2),
							slog.String("stack", string(debug.Stack())),
							slog.Any("request", req),
						)
					}
				}()

				respondStateless(ctx, tp.impl.(ServerTransport), req, ResponseStatusServerInternalError)
			}()

			stop = tp.meta.Reliable
		}
	}()

	tp.recvReq(ctx, req, err)
	return false
}

func (tp *baseTransp) recvReq(ctx context.Context, req *InboundRequest, err error) {
	ctx = context.WithValue(ctx, srvTranspCtxKey, tp.impl)

	// try to setup Via params even first to allow correct response routing in case of any failure
	if via, ok := req.msg.Headers.FirstVia(); ok && via != nil && via.IsValid() {
		// RFC 3261 Section 18.2.1.
		if via.Addr.IP() == nil || !via.Addr.IP().Equal(req.rmtAddr.Addr().AsSlice()) {
			if via.Params == nil {
				via.Params = make(header.Values)
			}
			via.Params.Set("received", req.rmtAddr.Addr().String())
		}
		// RFC 3581 Section 4.
		if via.Params.Has("rport") {
			via.Params.Set("rport", strconv.Itoa(int(req.rmtAddr.Port())))
		}
	}

	if err != nil {
		// we faced some errors during parsing of the request,
		// but if the parsed request contains mandatory headers,
		// then we can respond to it with a proper error response.
		var sts ResponseStatus
		switch {
		case errors.Is(err, ErrEntityTooLarge):
			sts = ResponseStatusRequestEntityTooLarge
		case errors.Is(err, ErrMessageTooLarge):
			sts = ResponseStatusMessageTooLarge
		case errors.Is(err, ErrInvalidMessage) || errorutil.IsGrammarErr(err):
			sts = ResponseStatusBadRequest
		default:
			sts = ResponseStatusServerInternalError
		}

		tp.log.LogAttrs(ctx, slog.LevelDebug, "discarding inbound request due to parse error",
			slog.Any("error", err),
			slog.Any("request", req),
		)
		respondStateless(ctx, tp.impl.(ServerTransport), req, sts)
		return
	}

	if err := req.Validate(); err != nil {
		tp.log.LogAttrs(ctx, slog.LevelDebug, "discarding inbound request due to validation error",
			slog.Any("error", err),
			slog.Any("request", req),
		)
		respondStateless(ctx, tp.impl.(ServerTransport), req, ResponseStatusBadRequest)
		return
	}

	if v, ok := tp.impl.(interface {
		recvReq(ctx context.Context, req *InboundRequest)
	}); ok {
		v.recvReq(ctx, req)
		return
	}

	var handled bool
	tp.onReq.Range(func(fn TransportRequestHandler) {
		handled = true
		fn(ctx, tp.impl.(ServerTransport), req)
	})
	if handled {
		return
	}

	tp.log.LogAttrs(ctx, slog.LevelWarn, "discarding inbound request due to missing request handlers",
		slog.Any("request", req),
	)
	respondStateless(ctx, tp.impl.(ServerTransport), req, ResponseStatusServiceUnavailable)
}

func (tp *baseTransp) recvResSafe(ctx context.Context, res *InboundResponse, err error) (stop bool) {
	defer func() {
		if r := recover(); r != nil {
			tp.log.LogAttrs(ctx, slog.LevelError, "panic in inbound response handler",
				slog.Any("panic", r),
				slog.String("stack", string(debug.Stack())),
				slog.Any("response", res),
			)
			stop = tp.meta.Reliable
		}
	}()

	tp.recvRes(ctx, res, err)
	return false
}

func (tp *baseTransp) recvRes(ctx context.Context, res *InboundResponse, err error) {
	ctx = context.WithValue(ctx, clnTranspCtxKey, tp.impl)

	if err != nil {
		tp.log.LogAttrs(ctx, slog.LevelDebug, "silently discard inbound response due to parse error",
			slog.Any("error", err),
			slog.Any("response", res),
		)
		return
	}

	if err := res.Validate(); err != nil {
		tp.log.LogAttrs(ctx, slog.LevelDebug, "silently discard inbound response due to validation error",
			slog.Any("error", err),
			slog.Any("response", res),
		)
		return
	}

	// RFC 3261 Section 18.1.2.
	via, _ := res.msg.Headers.FirstVia()
	if !util.EqFold(via.Addr.Host(), tp.sentByHost) {
		tp.log.LogAttrs(ctx, slog.LevelDebug, "silently discard inbound response due to host mismatch",
			slog.String("via_host", via.Addr.Host()),
			slog.String("transport_host", tp.sentByHost),
			slog.Any("response", res),
		)
		return
	}

	if v, ok := tp.impl.(interface {
		recvRes(ctx context.Context, res *InboundResponse)
	}); ok {
		v.recvRes(ctx, res)
		return
	}

	var handled bool
	tp.onRes.Range(func(fn TransportResponseHandler) {
		handled = true
		fn(ctx, tp.impl.(ClientTransport), res)
	})
	if handled {
		return
	}

	tp.log.LogAttrs(ctx, slog.LevelWarn, "silently discard inbound response due to missing response handlers",
		slog.Any("response", res),
	)
}

func (tp *baseTransp) shouldStopReadMsgs(err error) bool {
	if err == nil || !tp.meta.Streamed {
		return false
	}
	if errors.Is(err, ErrMessageTooLarge) || errors.Is(err, ErrEntityTooLarge) {
		return true
	}

	var perr *ParseError
	if !errors.As(err, &perr) {
		return false
	}
	if perr.State == ParseStateStart && len(perr.Data) == 0 {
		// don't stop on keep-alive CRLF (empty start line)
		return false
	}
	return errors.Is(err, ErrInvalidMessage) || errorutil.IsGrammarErr(err)
}

func wrapInMsg(msg Message, laddr, raddr netip.AddrPort) Message {
	switch m := msg.(type) {
	case *Request:
		return NewInboundRequest(m, laddr, raddr)
	case *Response:
		return NewInboundResponse(m, laddr, raddr)
	default:
		return msg
	}
}

func packetMsgs(conn net.PacketConn, prs Parser, readTimeout time.Duration) iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		conn = &readDeadlinePacketConn{conn, readTimeout}
		laddr := netip.MustParseAddrPort(conn.LocalAddr().String())

		buf := make([]byte, MaxMsgSize)
		for {
			num, rmtAddr, err := conn.ReadFrom(buf)
			if err != nil {
				if yield(nil, errtrace.Wrap(err)) {
					continue
				}
				return
			}

			raddr := netip.MustParseAddrPort(rmtAddr.String())
			msg, err := prs.ParsePacket(buf[:num])
			if err == nil {
				msg = wrapInMsg(msg, laddr, raddr)
			} else {
				var perr *ParseError
				// skip any empty buffer and parse errors without message
				if !errors.As(err, &perr) || perr.Msg == nil {
					continue
				}

				perr.Msg = wrapInMsg(perr.Msg, laddr, raddr)
			}

			if !yield(msg, errtrace.Wrap(err)) {
				return
			}
		}
	}
}

func streamMsgs(conn net.Conn, prs Parser, readTimeout time.Duration) iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		laddr := netip.MustParseAddrPort(conn.LocalAddr().String())
		raddr := netip.MustParseAddrPort(conn.RemoteAddr().String())

		rd := &io.LimitedReader{
			R: &readDeadlineConn{conn, readTimeout},
			N: int64(MaxMsgSize),
		}
		sp := prs.ParseStream(rd)
		for msg, err := range sp.Messages() {
			if err == nil {
				rd.N = int64(MaxMsgSize)

				msg = wrapInMsg(msg, laddr, raddr)
			} else {
				isTooLong := rd.N <= 0
				rd.N = int64(MaxMsgSize)

				var perr *ParseError
				if !errors.As(err, &perr) {
					// failed on reading of message start line
					if isTooLong {
						err = ErrMessageTooLarge
					}
					if yield(nil, errtrace.Wrap(err)) {
						continue
					}
					return
				}

				if perr.Msg == nil {
					// failed on parsing of message start line
					if yield(nil, errtrace.Wrap(err)) {
						continue
					}
					return
				}

				// failed at reading/parsing of message headers or body
				if isTooLong {
					err = fmt.Errorf("%w: %w", err, ErrMessageTooLarge)
				}

				perr.Msg = wrapInMsg(perr.Msg, laddr, raddr)
			}

			if !yield(msg, errtrace.Wrap(err)) {
				return
			}
		}
	}
}
