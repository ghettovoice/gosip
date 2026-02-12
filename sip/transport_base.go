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
	"slices"
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
	impl   transpImpl
	meta   TransportMetadata
	laddr  netip.AddrPort
	sentBy Addr
	dns    DNSResolver
	log    *slog.Logger

	srvOnce sync.Once
	srvErr  error

	closing chan struct{}
	clsOnce sync.Once
	clsErr  error

	inReqInts  types.CallbackManager[InboundRequestInterceptor]
	inResInts  types.CallbackManager[InboundResponseInterceptor]
	outReqInts types.CallbackManager[OutboundRequestInterceptor]
	outResInts types.CallbackManager[OutboundResponseInterceptor]
}

type transpImpl interface {
	Transport
	writeTo(
		ctx context.Context,
		bb *bytes.Buffer,
		raddr netip.AddrPort,
		opts *transpWriteOpts,
	) (netip.AddrPort, error)
	serve(ctx context.Context) error
	close(ctx context.Context) error
}

type transpWriteOpts struct {
	noDialConn bool
}

func newBaseTransp(
	impl transpImpl,
	md TransportMetadata,
	laddr netip.AddrPort,
	sentBy Addr,
	dns DNSResolver,
	logger *slog.Logger,
) *baseTransp {
	if sentBy.IsZero() {
		sentBy = HostPort(laddr.Addr().String(), laddr.Port())
	} else if _, ok := sentBy.Port(); ok {
		sentBy = HostPort(sentBy.Host(), laddr.Port())
	}

	tp := &baseTransp{
		impl:    impl,
		meta:    md,
		laddr:   laddr,
		sentBy:  sentBy,
		dns:     dns,
		log:     logger,
		closing: make(chan struct{}),
	}
	tp.log = tp.log.With("transport", tp)
	return tp
}

// Proto returns the transport protocol.
func (tp *baseTransp) Proto() TransportProto {
	if tp == nil {
		return ""
	}
	return tp.meta.Proto
}

// Network returns the transport network.
func (tp *baseTransp) Network() string {
	if tp == nil {
		return ""
	}
	return tp.meta.Network
}

// LocalAddr returns the transport local address.
func (tp *baseTransp) LocalAddr() netip.AddrPort {
	if tp == nil {
		return zeroAddrPort
	}
	return tp.laddr
}

// Reliable returns whether the transport is reliable or not.
func (tp *baseTransp) Reliable() bool {
	if tp == nil {
		return false
	}
	return tp.meta.Reliable
}

// Secured returns whether the transport is secured or not.
func (tp *baseTransp) Secured() bool {
	if tp == nil {
		return false
	}
	return tp.meta.Secured
}

// Streamed returns whether the transport is streamed or not.
func (tp *baseTransp) Streamed() bool {
	if tp == nil {
		return false
	}
	return tp.meta.Streamed
}

// DefaultPort returns the transport default port.
// The default port is used to build remote addresses when no port is specified or during DNS lookup.
func (tp *baseTransp) DefaultPort() uint16 {
	if tp == nil {
		return 0
	}
	return tp.meta.DefaultPort
}

// LogValue builds a [slog.Value] for the transport.
func (tp *baseTransp) LogValue() slog.Value {
	if tp == nil {
		return zeroSlogValue
	}
	return slog.GroupValue(
		slog.Any("proto", tp.meta.Proto),
		slog.Any("network", tp.meta.Network),
		slog.Any("local_addr", tp.laddr),
	)
}

// Logger returns the logger associated with the transport.
func (tp *baseTransp) Logger() *slog.Logger {
	if tp == nil {
		return nil
	}
	return tp.log
}

// SendRequest sends the request to the remote address specified in the req.
//
// Context can be used to cancel the request sending process through the deadline.
// If no deadline is specified on the context, the deadline is set to [SendRequestOptions.Timeout].
//
// The request must have the [header.Via] header with at least one [header.ViaHop] element,
// this element can have zero transport and address fields, they will be filled by the transport.
// In case of reliable transport, Content-Length header will be added automatically if it is missing.
//
// Options are optional, if nil is passed, default options are used (see [SendRequestOptions]).
func (tp *baseTransp) SendRequest(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	opts *SendRequestOptions,
) error {
	sender := ChainOutboundRequest(
		slices.Collect(
			util.SeqFilter(
				tp.outReqInts.All(),
				func(i OutboundRequestInterceptor) bool {
					return i != nil
				},
			),
		),
		RequestSenderFunc(tp.sendRequest),
	)
	return errtrace.Wrap(sender.SendRequest(ContextWithTransport(ctx, tp.impl), req, opts))
}

func (tp *baseTransp) sendRequest(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	opts *SendRequestOptions,
) error {
	if tp.isClosing() {
		return errtrace.Wrap(ErrTransportClosed)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.timeout())
		defer cancel()
	}

	req.SetTransport(tp.meta.Proto)
	req.SetLocalAddr(tp.laddr)

	raddr := req.RemoteAddr()
	if raddr.Port() == 0 {
		raddr = netip.AddrPortFrom(raddr.Addr(), tp.meta.DefaultPort)
	}
	req.SetRemoteAddr(raddr)
	if !raddr.IsValid() {
		return errtrace.Wrap(NewInvalidArgumentError("invalid remote address"))
	}

	req.AccessMessage(func(r *Request) {
		if r == nil || r.Headers == nil {
			return
		}

		if via, ok := r.Headers.FirstVia(); ok && via != nil {
			via.Transport = tp.meta.Proto
			via.Addr = tp.sentBy.Clone()
		}

		if tp.meta.Streamed {
			r.Headers.Set(header.ContentLength(len(r.Body)))
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

// SendResponse sends the response to a remote address resolved with steps
// defined in RFC 3261 Section 18.2.2. and RFC 3263 Section 5.
//
// Context can be used to cancel the response sending process through the deadline.
// If no deadline is specified on the context, the deadline is set to [SendResponseOptions.Timeout].
//
// The topmost [header.ViaHop] transport must match the transport protocol.
// In case of reliable transport, Content-Length header will be added automatically if it is missing.
//
// Options are optional, if nil is passed, default options are used (see [SendResponseOptions]).
func (tp *baseTransp) SendResponse(
	ctx context.Context,
	res *OutboundResponseEnvelope,
	opts *SendResponseOptions,
) error {
	sender := ChainOutboundResponse(
		slices.Collect(
			util.SeqFilter(
				tp.outResInts.All(),
				func(i OutboundResponseInterceptor) bool {
					return i != nil
				},
			),
		),
		ResponseSenderFunc(tp.sendResponse),
	)
	return errtrace.Wrap(sender.SendResponse(ContextWithTransport(ctx, tp.impl), res, opts))
}

//nolint:gocognit
func (tp *baseTransp) sendResponse(
	ctx context.Context,
	res *OutboundResponseEnvelope,
	opts *SendResponseOptions,
) error {
	if tp.isClosing() {
		return errtrace.Wrap(ErrTransportClosed)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.timeout())
		defer cancel()
	}

	res.SetTransport(tp.meta.Proto)
	res.SetLocalAddr(tp.laddr)

	var via header.ViaHop
	res.AccessMessage(func(r *Response) {
		if r == nil || r.Headers == nil {
			return
		}

		if hop, ok := r.Headers.FirstVia(); ok && hop != nil {
			via = hop.Clone()
		}

		if tp.meta.Streamed {
			r.Headers.Set(header.ContentLength(len(r.Body)))
		}
	})

	if err := res.Validate(); err != nil {
		return errtrace.Wrap(NewInvalidArgumentError(err))
	}

	if !via.Transport.Equal(tp.meta.Proto) {
		return errtrace.Wrap(NewInvalidArgumentError(
			"Via transport mismatch: got %q, want %q",
			via.Transport,
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

	// finally fallback to address resolving procedure defined in
	// RFC 3261 Section 18.2.2. and RFC 3263 Section 5.
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
		return errtrace.Wrap(ErrNoTarget)
	}
	return errtrace.Wrap(errorutil.JoinPrefix("all response targets failed:", errs...))
}

func (*baseTransp) resolveWriteErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, net.ErrClosed) {
		return ErrTransportClosed //errtrace:skip
	}
	if ctx.Err() != nil {
		return ctx.Err() //errtrace:skip
	}
	return err //errtrace:skip
}

func (tp *baseTransp) Respond(
	ctx context.Context,
	req *InboundRequestEnvelope,
	sts ResponseStatus,
	opts *RespondOptions,
) error {
	return errtrace.Wrap(RespondStateless(ctx, tp.impl, req, sts, opts))
}

// UseInboundRequestInterceptor adds interceptor for inbound requests.
// The interceptor can be removed by calling the returned cancel function.
//
// Context passed to the interceptor is a child of the context passed to [Serve] method.
func (tp *baseTransp) UseInboundRequestInterceptor(interceptor InboundRequestInterceptor) (unbind func()) {
	return tp.inReqInts.Add(interceptor)
}

// UseInboundResponseInterceptor adds interceptor for inbound responses.
// The interceptor can be removed by calling the returned cancel function.
//
// Context passed to the interceptor is a child of the context passed to [Serve] method.
func (tp *baseTransp) UseInboundResponseInterceptor(interceptor InboundResponseInterceptor) (unbind func()) {
	return tp.inResInts.Add(interceptor)
}

// UseOutboundRequestInterceptor adds interceptor for outbound requests.
// The interceptor can be removed by calling the returned cancel function.
//
// Context passed to the interceptor is a child of the context passed to [Serve] method.
func (tp *baseTransp) UseOutboundRequestInterceptor(interceptor OutboundRequestInterceptor) (unbind func()) {
	return tp.outReqInts.Add(interceptor)
}

// UseOutboundResponseInterceptor adds interceptor for outbound responses.
// The interceptor can be removed by calling the returned cancel function.
//
// Context passed to the interceptor is a child of the context passed to [Serve] method.
func (tp *baseTransp) UseOutboundResponseInterceptor(interceptor OutboundResponseInterceptor) (unbind func()) {
	return tp.outResInts.Add(interceptor)
}

// UseInterceptor adds all non-nil interceptors from the provided object.
// The interceptor can be removed by calling the returned cancel function.
//
// Context passed to the interceptor is a child of the context passed to [Serve] method.
func (tp *baseTransp) UseInterceptor(interceptor MessageInterceptor) (unbind func()) {
	if interceptor == nil {
		return func() {}
	}

	var unbinds []func()
	if inbound := interceptor.InboundRequestInterceptor(); inbound != nil {
		unbinds = append(unbinds, tp.UseInboundRequestInterceptor(inbound))
	}
	if inbound := interceptor.InboundResponseInterceptor(); inbound != nil {
		unbinds = append(unbinds, tp.UseInboundResponseInterceptor(inbound))
	}
	if outbound := interceptor.OutboundRequestInterceptor(); outbound != nil {
		unbinds = append(unbinds, tp.UseOutboundRequestInterceptor(outbound))
	}
	if outbound := interceptor.OutboundResponseInterceptor(); outbound != nil {
		unbinds = append(unbinds, tp.UseOutboundResponseInterceptor(outbound))
	}
	return func() {
		for _, fn := range unbinds {
			fn()
		}
	}
}

// Serve starts serving the listener and blocks until the transport is closed.
//
// Context is passed to the inbound message interceptors chain.
// Cancellation of the context does not stop the serve/read loop, use [Close] method instead.
func (tp *baseTransp) Serve(ctx context.Context) error {
	tp.srvOnce.Do(func() {
		tp.srvErr = tp.impl.serve(ContextWithTransport(ctx, tp.impl))
	})
	return errtrace.Wrap(tp.srvErr)
}

// Close closes the transport and underlying listener with all connections.
// It returns any error returned from closing the listener.
func (tp *baseTransp) Close(ctx context.Context) error {
	tp.clsOnce.Do(func() {
		close(tp.closing)
		tp.clsErr = tp.impl.close(ContextWithTransport(ctx, tp.impl))
	})
	return errtrace.Wrap(tp.clsErr)
}

func (tp *baseTransp) isClosing() bool {
	select {
	case <-tp.closing:
		return true
	default:
		return false
	}
}

//nolint:gocognit
func (tp *baseTransp) readMsgs(ctx context.Context, msgs iter.Seq2[Message, error]) error {
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
						"failed to read inbound message due to the temporary error, continue serving after delay...",
						slog.Any("error", err),
						slog.Duration("delay", tempDelay),
					)

					tmr := time.NewTimer(tempDelay)
					select {
					case <-tp.closing:
						tmr.Stop()
						return errtrace.Wrap(ErrTransportClosed)
					case <-tmr.C:
					}
					continue
				}
				return errtrace.Wrap(err)
			}

			// pass messages with parse errors (incomplete message, missing Content-Length,
			// other ErrInvalidMessage or Grammar errors), they will be discarded below
			msg = perr.Msg
		}

		if msg != nil {
			switch msg := msg.(type) {
			case *InboundRequestEnvelope:
				if tp.recvReqSafe(ctx, msg, errtrace.Wrap(err)) {
					return nil
				}
			case *InboundResponseEnvelope:
				if tp.recvResSafe(ctx, msg, errtrace.Wrap(err)) {
					return nil
				}
			default:
				tp.log.LogAttrs(ctx, slog.LevelWarn,
					"silently discard the inbound message due to unsupported message type",
					slog.Any("message", msg),
					slog.Any("error", newUnexpectMsgTypeErr(msg)),
				)
			}
		}

		if tp.shouldStopReadMsgs(err) {
			return errtrace.Wrap(err)
		}
	}
	return nil
}

func (tp *baseTransp) recvReqSafe(ctx context.Context, req *InboundRequestEnvelope, err error) (stop bool) {
	defer func() {
		if pe := recover(); pe != nil {
			tp.log.LogAttrs(ctx, slog.LevelError,
				"panic occurred while processing the inbound request",
				slog.Any("request", req),
				slog.Any("error", pe),
				slog.String("stack", string(debug.Stack())),
			)

			stop = tp.meta.Reliable

			func() {
				defer func() {
					if pe := recover(); pe != nil {
						tp.log.LogAttrs(ctx, slog.LevelError,
							"panic occurred while responding to the inbound request",
							slog.Any("request", req),
							slog.Any("error", pe),
							slog.String("stack", string(debug.Stack())),
						)

						stop = tp.meta.Reliable
					}
				}()

				tp.respond(ctx, req, ResponseStatusServerInternalError)
			}()
		}
	}()

	if err := tp.recvReq(ctx, req, errtrace.Wrap(err)); err != nil {
		var (
			rejectErr *rejectRequestError
			lvl       slog.Level
		)
		sts := ResponseStatusServerInternalError
		if errors.As(err, &rejectErr) {
			sts = rejectErr.sts
			lvl = rejectErr.lvl
		}

		tp.log.LogAttrs(ctx, lvl,
			"rejecting the inbound request due to error",
			slog.Any("request", req),
			slog.Any("error", err),
		)

		tp.respond(ctx, req, sts)
	}
	return false
}

func (tp *baseTransp) recvReq(ctx context.Context, req *InboundRequestEnvelope, err error) error {
	// try to setup Via params even first to allow correct response routing in case of any failure
	if via, ok := req.message().Headers.FirstVia(); ok && via != nil && via.IsValid() {
		// RFC 3261 Section 18.2.1.
		if via.Addr.IP() == nil || !via.Addr.IP().Equal(req.remoteAddr().Addr().AsSlice()) {
			if via.Params == nil {
				via.Params = make(Values)
			}
			via.Params.Set("received", req.remoteAddr().Addr().String())
		}
		// RFC 3581 Section 4.
		if via.Params.Has("rport") {
			via.Params.Set("rport", strconv.Itoa(int(req.remoteAddr().Port())))
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

		return errtrace.Wrap(NewRejectRequestError(err, sts, slog.LevelDebug))
	}

	if err := req.Validate(); err != nil {
		return errtrace.Wrap(NewRejectRequestError(err, ResponseStatusBadRequest, slog.LevelDebug))
	}

	receiver := ChainInboundRequest(
		slices.Collect(
			util.SeqFilter(
				tp.inReqInts.All(),
				func(i InboundRequestInterceptor) bool {
					return i != nil
				},
			),
		),
		RequestReceiverFunc(func(ctx context.Context, req *InboundRequestEnvelope) error {
			return errtrace.Wrap(NewRejectRequestError(
				ErrUnhandledMessage,
				ResponseStatusServiceUnavailable,
				slog.LevelWarn,
			))
		}),
	)
	return errtrace.Wrap(receiver.RecvRequest(ctx, req))
}

func (tp *baseTransp) respond(ctx context.Context, req *InboundRequestEnvelope, sts ResponseStatus) {
	if err := tp.Respond(ctx, req, sts, nil); err != nil {
		lvl := slog.LevelError
		if errors.Is(err, ErrInvalidArgument) {
			lvl = slog.LevelDebug
		}

		tp.log.LogAttrs(ctx, lvl,
			"silently discard the inbound request due to respond failure",
			slog.Any("request", req),
			slog.Any("error", err),
		)
	}
}

func (tp *baseTransp) recvResSafe(ctx context.Context, res *InboundResponseEnvelope, err error) (stop bool) {
	defer func() {
		if pe := recover(); pe != nil {
			tp.log.LogAttrs(ctx, slog.LevelError,
				"panic occurred while processing the inbound response",
				slog.Any("response", res),
				slog.Any("error", pe),
				slog.String("stack", string(debug.Stack())),
			)

			stop = tp.meta.Reliable
		}
	}()

	if err := tp.recvRes(ctx, res, errtrace.Wrap(err)); err != nil {
		var (
			rejectErr *rejectResponseError
			lvl       slog.Level
		)
		if errors.As(err, &rejectErr) {
			lvl = rejectErr.lvl
		}

		tp.log.LogAttrs(ctx, lvl,
			"silently discard the inbound response due to error",
			slog.Any("response", res),
			slog.Any("error", err),
		)
	}
	return false
}

func (tp *baseTransp) recvRes(ctx context.Context, res *InboundResponseEnvelope, err error) error {
	if err != nil {
		return errtrace.Wrap(NewRejectResponseError(err, slog.LevelDebug))
	}

	if err := res.Validate(); err != nil {
		return errtrace.Wrap(NewRejectResponseError(err, slog.LevelDebug))
	}

	// RFC 3261 Section 18.1.2.
	via, _ := res.message().Headers.FirstVia()
	if !via.Addr.Equal(tp.sentBy) {
		return errtrace.Wrap(NewRejectResponseError(
			errorutil.Errorf("Via sent-by mismatch: got %s, want %s", via.Addr, tp.sentBy),
			slog.LevelDebug,
		))
	}

	receiver := ChainInboundResponse(
		slices.Collect(
			util.SeqFilter(
				tp.inResInts.All(),
				func(i InboundResponseInterceptor) bool {
					return i != nil
				},
			),
		),
		ResponseReceiverFunc(func(ctx context.Context, res *InboundResponseEnvelope) error {
			return errtrace.Wrap(NewRejectResponseError(ErrUnhandledMessage, slog.LevelWarn))
		}),
	)
	return errtrace.Wrap(receiver.RecvResponse(ctx, res))
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

func wrapInMsg(msg Message, proto TransportProto, laddr, raddr netip.AddrPort) (Message, error) {
	switch m := msg.(type) {
	case *Request:
		return errtrace.Wrap2(NewInboundRequestEnvelope(m, proto, laddr, raddr))
	case *Response:
		return errtrace.Wrap2(NewInboundResponseEnvelope(m, proto, laddr, raddr))
	default:
		return msg, nil
	}
}

func packetMsgs(
	proto TransportProto,
	conn net.PacketConn,
	prs Parser,
	readTimeout time.Duration,
) iter.Seq2[Message, error] {
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
				m, e := wrapInMsg(msg, proto, laddr, raddr)
				if e != nil {
					// should never happen
					panic(e)
				}
				msg = m
			} else {
				var perr *ParseError
				// skip any empty buffer and parse errors without message
				if !errors.As(err, &perr) || perr.Msg == nil {
					continue
				}

				m, e := wrapInMsg(perr.Msg, proto, laddr, raddr)
				if e != nil {
					// should never happen
					panic(e)
				}
				perr.Msg = m
			}

			if !yield(msg, errtrace.Wrap(err)) {
				return
			}
		}
	}
}

//nolint:gocognit
func streamMsgs(
	proto TransportProto,
	conn net.Conn,
	prs Parser,
	readTimeout time.Duration,
) iter.Seq2[Message, error] {
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

				m, e := wrapInMsg(msg, proto, laddr, raddr)
				if e != nil {
					// should never happen
					panic(e)
				}
				msg = m
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

				m, e := wrapInMsg(perr.Msg, proto, laddr, raddr)
				if e != nil {
					// should never happen
					panic(e)
				}
				perr.Msg = m
			}

			if !yield(msg, errtrace.Wrap(err)) {
				return
			}
		}
	}
}
