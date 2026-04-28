package sip

import (
	"context"
	"log/slog"
	"net/netip"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

// Element setups basic inbound/outbound message pipeline and
// provides common SIP element message processing.
type Element struct {
	name       string
	tpm        *TransportManager
	txm        *TransactionManager
	rmtSrvLctr RemoteServerLocator
	log        *slog.Logger
}

// ElementOptions configures an [Element].
// All fields are optional.
type ElementOptions struct {
	// RemoteServerLocator is used to lookup remote server transport and addresses for outbound request.
	// If nil, [DefaultRemoteElementLocator] is used.
	RemoteServerLocator RemoteServerLocator
	// TransactionOptions configures the transaction manager.
	// If nil, the element will be transaction stateless.
	TransactionOptions *TransactionManagerOptions
	// Logger is the logger used by the element.
	// If nil, the [log.Default] is used.
	Logger *slog.Logger
}

func (o *ElementOptions) rmtSrvLctr() RemoteServerLocator {
	if o == nil || o.RemoteServerLocator == nil {
		return defRmtElemLocator
	}
	return o.RemoteServerLocator
}

func (o *ElementOptions) txOpts() *TransactionManagerOptions {
	if o == nil {
		return nil
	}
	return o.TransactionOptions
}

func (o *ElementOptions) log() *slog.Logger {
	if o == nil || o.Logger == nil {
		return log.Default()
	}
	return o.Logger
}

// NewElement creates a new base SIP [Element].
//
// Name is the name of the element, used to add User-Agent/Server header where appropriate.
// Transport is the default transport to use for the element.
// Options are optional, default options are used if nil (see [ElementOptions]).
func NewElement(name string, tp Transport, opts *ElementOptions) (*Element, error) {
	if name == "" {
		return nil, errors.NewInvalidArgumentErrorWrap("empty name")
	}

	elm := &Element{
		name:       name,
		rmtSrvLctr: opts.rmtSrvLctr(),
	}
	elm.log = opts.log().With(slog.Any("element", elm))

	elm.tpm = NewTransportManager(&TransportManagerOptions{
		Logger: elm.log,
	})
	if err := elm.tpm.TrackTransport(tp); err != nil {
		_ = elm.Close()
		return nil, errors.ErrorfWrap("track default transport: %w", err)
	}

	if txOpts := opts.txOpts(); txOpts != nil {
		if txOpts.Logger == nil {
			txOpts.Logger = elm.log
		} else {
			txOpts.Logger = txOpts.Logger.With(slog.Any("element", elm))
		}

		elm.txm = NewTransactionManager(txOpts)
	}

	elm.tpm.UseInterceptor(elm.txm)
	elm.tpm.UseInterceptor(StdMessageInterceptor{
		OutboundRequest:  OutboundRequestInterceptorFunc(elm.interceptOutboundRequest),
		OutboundResponse: OutboundResponseInterceptorFunc(elm.interceptOutboundResponse),
	})

	return elm, nil
}

func (elm *Element) Name() string {
	if elm == nil {
		return ""
	}
	return elm.name
}

func (elm *Element) Logger() *slog.Logger {
	if elm == nil {
		return nil
	}
	return elm.log
}

func (elm *Element) LogValue() slog.Value {
	if elm == nil {
		return slog.Value{}
	}

	return slog.GroupValue(
		slog.Any("name", elm.name),
	)
}

func (elm *Element) TransportManager() *TransportManager {
	if elm == nil {
		return nil
	}
	return elm.tpm
}

func (elm *Element) TransactionManager() *TransactionManager {
	if elm == nil {
		return nil
	}
	return elm.txm
}

func (elm *Element) interceptOutboundRequest(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	opts *SendRequestOptions,
	next RequestSender,
) error {
	// TODO: append auto-headers, only self-generated requests, exclude forwarded requests
	req.AccessMessage(func(r *Request) {
		if r == nil || r.Headers == nil {
			return
		}

		if hdrs := r.Headers.Get("User-Agent"); len(hdrs) == 0 {
			r.Headers.Append(header.UserAgent(elm.name))
		}
	})

	return errors.Wrap(next.SendRequest(ctx, req, opts))
}

func (elm *Element) interceptOutboundResponse(
	ctx context.Context,
	res *OutboundResponseEnvelope,
	opts *SendResponseOptions,
	next ResponseSender,
) error {
	// TODO: append auto-headers, only self-generated responses, exclude forwarded responses
	res.AccessMessage(func(r *Response) {
		if r == nil || r.Headers == nil {
			return
		}

		if hdrs := r.Headers.Get("Server"); len(hdrs) == 0 {
			r.Headers.Append(header.Server(elm.name))
		}
	})

	return errors.Wrap(next.SendResponse(ctx, res, opts))
}

func (elm *Element) Close() error {
	if elm == nil {
		return nil
	}

	defer elm.log.Debug("element closed")

	return errors.JoinPrefixWrap("element close errors:", elm.txm.Close(), elm.tpm.Close())
}

func (*Element) resolveTargetURI(req *OutboundRequestEnvelope) *uri.SIP {
	var targetURI uri.URI
	req.AccessMessage(func(r *Request) {
		targetURI = r.URI.Clone()

		if route, ok := r.Headers.FirstRouteHop(); ok && route.IsValid() {
			if u, ok := route.URI.(*uri.SIP); ok && u.LR() {
				targetURI = route.URI.Clone()
			} else {
				targetURI = route.URI.Clone()
				reqURI := r.URI
				r.URI = route.URI.Clone()

				r.Headers.PopFirstRouteHop()
				r.Headers.AppendRouteHop(header.RouteHop{URI: reqURI})
			}
		}
	})

	if u, ok := targetURI.(*uri.SIP); ok {
		return u
	}
	// TODO: make injectable service to transform non-SIP URI to SIP URI
	//       at least for tel: URIs - predefined domain, some simple routing?
	return nil
}

func (elm *Element) SendRequest(ctx context.Context, req *OutboundRequestEnvelope, opts *SendRequestOptions) error {
	if req.Transport().IsValid() && req.RemoteAddr().IsValid() {
		// Transport and remote address are already set, send directly.
		return errors.Wrap(elm.tpm.SendRequest(ctx, req, opts))
	}

	// Default SIP element behavior - resolve target URI, lookup remote server,
	// then try to send to each resolved address until success.
	// RFC 3261 Section 8.1.2.
	targetURI := elm.resolveTargetURI(req)
	if !targetURI.IsValid() {
		return errors.Wrap(ErrNoDestAddressResolved)
	}

	var (
		errs               []error
		unrelFallbackProto TransportProto
		unrelFallbackAddr  netip.AddrPort
	)
	for proto, addr := range elm.rmtSrvLctr.LookupRequestAddrs(ctx, targetURI, elm.tpm) {
		// If request size is bigger than MTU - 200 bytes, it must be sent via reliable (congestion controlled) transport.
		// We remember unreliable transport as fallback.
		// RFC 3261 Section 18.1.1.
		if meta := elm.tpm.MetadataByProto(proto); !meta.Reliable() && len(req.Render(opts.rendOpts())) > int(MTU)-200 {
			unrelFallbackProto = proto
			unrelFallbackAddr = addr
			continue
		}

		req.SetTransport(proto)
		req.SetRemoteAddr(addr)

		err := elm.tpm.SendRequest(ctx, req, opts)
		if err == nil || errors.Is(err, ErrTransportManagerClosed) || errors.Is(err, ErrInvalidMessage) {
			return errors.Wrap(err)
		}

		errs = append(errs, errors.Errorf("send request via %q to %q: %w", proto, addr, err))
	}

	// Send request via unreliable transport as fallback if we failed to send it via some reliable transport.
	if unrelFallbackProto.IsValid() && unrelFallbackAddr.IsValid() {
		req.SetTransport(unrelFallbackProto)
		req.SetRemoteAddr(unrelFallbackAddr)

		err := elm.tpm.SendRequest(ctx, req, opts)
		if err == nil || errors.Is(err, ErrTransportManagerClosed) || errors.Is(err, ErrInvalidMessage) {
			return errors.Wrap(err)
		}

		errs = append(errs, errors.Errorf("send request via %q to %q: %w", unrelFallbackProto, unrelFallbackAddr, err))
	}

	if len(errs) == 0 {
		return errors.Wrap(ErrNoDestAddressResolved)
	}

	return errors.JoinWrap(errs...)
}

func (elm *Element) SendResponse(ctx context.Context, res *OutboundResponseEnvelope, opts *SendResponseOptions) error {
	return errors.Wrap(elm.tpm.SendResponse(ctx, res, opts))
}

func (elm *Element) Respond(ctx context.Context, req *InboundRequestEnvelope, sts ResponseStatus, opts *RespondOptions) error {
	return errors.Wrap(elm.tpm.Respond(ctx, req, sts, opts))
}

func (elm *Element) SendRequestStateful(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	opts *ClientTransactionOptions,
) (ClientTransaction, error) {
	if tp := elm.tpm.resolveReqTransp(req); tp != nil && req.RemoteAddr().IsValid() {
		// Transport and remote address are already set, send directly.
		return errors.Wrap2(elm.txm.NewClientTransaction(ctx, req, TransactionTransport{tp}, opts))
	}

	// Default SIP element behavior - resolve target URI, lookup remote server,
	// then try to send to each resolved address until success.
	// RFC 3261 Section 8.1.2.
	targetURI := elm.resolveTargetURI(req)
	if !targetURI.IsValid() {
		return nil, errors.Wrap(ErrNoDestAddressResolved)
	}

	switch {
	case opts == nil:
		opts = &ClientTransactionOptions{
			Logger: elm.log,
		}
	case opts.Logger == nil:
		opts.Logger = elm.log
	default:
		opts.Logger = opts.Logger.With(slog.Any("element", elm))
	}

	var (
		errs               []error
		unrelFallbackProto TransportProto
		unrelFallbackAddr  netip.AddrPort
	)
	for proto, addr := range elm.rmtSrvLctr.LookupRequestAddrs(ctx, targetURI, elm.tpm) {
		// If request size is bigger than MTU - 200 bytes, it must be sent via reliable (congestion controlled) transport.
		// We remember unreliable transport as fallback.
		// RFC 3261 Section 18.1.1.
		if meta := elm.tpm.MetadataByProto(proto); !meta.Reliable() && len(req.Render(opts.sendOpts().rendOpts())) > int(MTU)-200 {
			unrelFallbackProto = proto
			unrelFallbackAddr = addr
			continue
		}

		req.SetTransport(proto)
		req.SetRemoteAddr(addr)

		tp := elm.tpm.resolveReqTransp(req)
		if tp == nil {
			continue
		}

		tx, err := elm.txm.NewClientTransaction(ctx, req, TransactionTransport{tp}, opts)
		if err == nil || errors.Is(err, ErrTransactionManagerClosed) || errors.Is(err, ErrInvalidMessage) {
			return tx, errors.Wrap(err)
		}

		errs = append(errs, errors.Errorf("send request via %q to %q: %w", proto, addr, err))
	}

	// Send request via unreliable transport as fallback if we failed to send it via some reliable transport.
	if unrelFallbackProto.IsValid() && unrelFallbackAddr.IsValid() {
		req.SetTransport(unrelFallbackProto)
		req.SetRemoteAddr(unrelFallbackAddr)

		if tp := elm.tpm.resolveReqTransp(req); tp != nil {
			tx, err := elm.txm.NewClientTransaction(ctx, req, TransactionTransport{tp}, opts)
			if err == nil || errors.Is(err, ErrTransactionManagerClosed) || errors.Is(err, ErrInvalidMessage) {
				return tx, errors.Wrap(err)
			}

			errs = append(errs, errors.Errorf("send request via %q to %q: %w", unrelFallbackProto, unrelFallbackAddr, err))
		}
	}

	if len(errs) == 0 {
		return nil, errors.Wrap(ErrNoDestAddressResolved)
	}

	return nil, errors.JoinWrap(errs...)
}

type SendResponseStatefulOptions struct {
	SendOptions        *SendResponseOptions
	TransactionOptions *ServerTransactionOptions
}

func (o *SendResponseStatefulOptions) sendOpts() *SendResponseOptions {
	if o == nil {
		return nil
	}
	return o.SendOptions
}

func (o *SendResponseStatefulOptions) txOpts() *ServerTransactionOptions {
	if o == nil {
		return nil
	}
	return o.TransactionOptions
}

func (elm *Element) SendResponseStateful(
	ctx context.Context,
	req *InboundRequestEnvelope,
	res *OutboundResponseEnvelope,
	opts *SendResponseStatefulOptions,
) (ServerTransaction, error) {
	tp := elm.tpm.resolveResTransp(res)
	if tp == nil {
		return nil, errors.NewInvalidArgumentErrorWrap("no transport resolved for response")
	}

	switch {
	case opts == nil:
		opts = &SendResponseStatefulOptions{
			TransactionOptions: &ServerTransactionOptions{
				Logger: elm.log,
			},
		}
	case opts.TransactionOptions == nil:
		opts.TransactionOptions = &ServerTransactionOptions{
			Logger: elm.log,
		}
	case opts.TransactionOptions.Logger == nil:
		opts.TransactionOptions.Logger = elm.log
	default:
		opts.TransactionOptions.Logger = opts.TransactionOptions.Logger.With(slog.Any("element", elm))
	}

	tx, err := elm.txm.NewServerTransaction(ctx, req, TransactionTransport{tp}, opts.txOpts())
	if err != nil {
		return nil, errors.Wrap(err)
	}

	if err := tx.SendResponse(ctx, res, opts.sendOpts()); err != nil {
		tx.Terminate(ctx) //nolint:errcheck
		return nil, errors.Wrap(err)
	}

	return tx, nil
}

type RespondStatefulOptions struct {
	ResponseOptions    *ResponseOptions
	SendOptions        *SendResponseOptions
	TransactionOptions *ServerTransactionOptions
}

func (o *RespondStatefulOptions) resOpts() *ResponseOptions {
	if o == nil {
		return nil
	}
	return o.ResponseOptions
}

func (o *RespondStatefulOptions) sendOpts() *SendResponseOptions {
	if o == nil {
		return nil
	}
	return o.SendOptions
}

func (o *RespondStatefulOptions) txOpts() *ServerTransactionOptions {
	if o == nil {
		return nil
	}
	return o.TransactionOptions
}

func (o *RespondStatefulOptions) sendStatefulOpts() *SendResponseStatefulOptions {
	if o == nil && o.sendOpts() == nil && o.txOpts() == nil {
		return nil
	}

	return &SendResponseStatefulOptions{
		SendOptions:        o.sendOpts(),
		TransactionOptions: o.txOpts(),
	}
}

func (elm *Element) RespondStateful(
	ctx context.Context,
	req *InboundRequestEnvelope,
	sts ResponseStatus,
	opts *RespondStatefulOptions,
) (ServerTransaction, error) {
	res, err := req.NewResponse(sts, opts.resOpts())
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return errors.Wrap2(elm.SendResponseStateful(ctx, req, res, opts.sendStatefulOpts()))
}
