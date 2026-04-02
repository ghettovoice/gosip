package sip

import (
	"context"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
)

// InboundRequestInterceptor intercepts inbound requests before they reach the receiver.
type InboundRequestInterceptor interface {
	InterceptInboundRequest(ctx context.Context, req *InboundRequestEnvelope, next RequestReceiver) error
}

type InboundRequestInterceptorFunc func(
	ctx context.Context,
	req *InboundRequestEnvelope,
	next RequestReceiver,
) error

func (fn InboundRequestInterceptorFunc) InterceptInboundRequest(
	ctx context.Context,
	req *InboundRequestEnvelope,
	next RequestReceiver,
) error {
	return errors.Wrap(fn(ctx, req, next))
}

// InboundResponseInterceptor intercepts inbound responses before they reach the receiver.
type InboundResponseInterceptor interface {
	InterceptInboundResponse(ctx context.Context, res *InboundResponseEnvelope, next ResponseReceiver) error
}

type InboundResponseInterceptorFunc func(
	ctx context.Context,
	res *InboundResponseEnvelope,
	next ResponseReceiver,
) error

func (fn InboundResponseInterceptorFunc) InterceptInboundResponse(
	ctx context.Context,
	res *InboundResponseEnvelope,
	next ResponseReceiver,
) error {
	return errors.Wrap(fn(ctx, res, next))
}

// OutboundRequestInterceptor intercepts outbound requests before they are sent.
type OutboundRequestInterceptor interface {
	InterceptOutboundRequest(
		ctx context.Context,
		req *OutboundRequestEnvelope,
		opts *SendRequestOptions,
		next RequestSender,
	) error
}

type OutboundRequestInterceptorFunc func(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	opts *SendRequestOptions,
	next RequestSender,
) error

func (fn OutboundRequestInterceptorFunc) InterceptOutboundRequest(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	opts *SendRequestOptions,
	next RequestSender,
) error {
	return errors.Wrap(fn(ctx, req, opts, next))
}

// OutboundResponseInterceptor intercepts outbound responses before they are sent.
type OutboundResponseInterceptor interface {
	InterceptOutboundResponse(
		ctx context.Context,
		res *OutboundResponseEnvelope,
		opts *SendResponseOptions,
		next ResponseSender,
	) error
}

type OutboundResponseInterceptorFunc func(
	ctx context.Context,
	res *OutboundResponseEnvelope,
	opts *SendResponseOptions,
	next ResponseSender,
) error

func (fn OutboundResponseInterceptorFunc) InterceptOutboundResponse(
	ctx context.Context,
	res *OutboundResponseEnvelope,
	opts *SendResponseOptions,
	next ResponseSender,
) error {
	return errors.Wrap(fn(ctx, res, opts, next))
}

// MessageInterceptor provides optional inbound/outbound interceptors.
type MessageInterceptor interface {
	InboundRequestInterceptor() InboundRequestInterceptor
	InboundResponseInterceptor() InboundResponseInterceptor
	OutboundRequestInterceptor() OutboundRequestInterceptor
	OutboundResponseInterceptor() OutboundResponseInterceptor
}

type StdMessageInterceptor struct {
	InboundRequest   InboundRequestInterceptor
	InboundResponse  InboundResponseInterceptor
	OutboundRequest  OutboundRequestInterceptor
	OutboundResponse OutboundResponseInterceptor
}

func (f StdMessageInterceptor) InboundRequestInterceptor() InboundRequestInterceptor {
	return f.InboundRequest
}

func (f StdMessageInterceptor) InboundResponseInterceptor() InboundResponseInterceptor {
	return f.InboundResponse
}

func (f StdMessageInterceptor) OutboundRequestInterceptor() OutboundRequestInterceptor {
	return f.OutboundRequest
}

func (f StdMessageInterceptor) OutboundResponseInterceptor() OutboundResponseInterceptor {
	return f.OutboundResponse
}

type NoopMessageInterceptor struct{}

func (NoopMessageInterceptor) InboundRequestInterceptor() InboundRequestInterceptor     { return nil }
func (NoopMessageInterceptor) InboundResponseInterceptor() InboundResponseInterceptor   { return nil }
func (NoopMessageInterceptor) OutboundRequestInterceptor() OutboundRequestInterceptor   { return nil }
func (NoopMessageInterceptor) OutboundResponseInterceptor() OutboundResponseInterceptor { return nil }

// InterceptInboundRequest builds a request receiver pipeline in FIFO order.
func InterceptInboundRequest(interceptors []InboundRequestInterceptor, final RequestReceiver) RequestReceiver {
	if final == nil {
		return nil
	}

	receiver := final
	for i := len(interceptors) - 1; i >= 0; i-- {
		interceptor := interceptors[i]
		if interceptor == nil {
			continue
		}

		next := receiver
		receiver = RequestReceiverFunc(func(ctx context.Context, req *InboundRequestEnvelope) error {
			return errors.Wrap(interceptor.InterceptInboundRequest(ctx, req, next))
		})
	}

	return receiver
}

// InterceptInboundResponse builds a response receiver pipeline in FIFO order.
func InterceptInboundResponse(interceptors []InboundResponseInterceptor, final ResponseReceiver) ResponseReceiver {
	if final == nil {
		return nil
	}

	receiver := final
	for i := len(interceptors) - 1; i >= 0; i-- {
		interceptor := interceptors[i]
		if interceptor == nil {
			continue
		}

		next := receiver
		receiver = ResponseReceiverFunc(func(ctx context.Context, res *InboundResponseEnvelope) error {
			return errors.Wrap(interceptor.InterceptInboundResponse(ctx, res, next))
		})
	}

	return receiver
}

// InterceptOutboundRequest builds a request sender pipeline in LIFO order.
func InterceptOutboundRequest(interceptors []OutboundRequestInterceptor, final RequestSender) RequestSender {
	if final == nil {
		return nil
	}

	sender := final
	for i := range interceptors {
		interceptor := interceptors[i]
		if interceptor == nil {
			continue
		}

		next := sender
		sender = RequestSenderFunc(
			func(ctx context.Context, req *OutboundRequestEnvelope, opts *SendRequestOptions) error {
				return errors.Wrap(interceptor.InterceptOutboundRequest(ctx, req, opts, next))
			},
		)
	}

	return sender
}

// InterceptOutboundResponse builds a response sender pipeline in LIFO order.
func InterceptOutboundResponse(interceptors []OutboundResponseInterceptor, final ResponseSender) ResponseSender {
	if final == nil {
		return nil
	}

	sender := final
	for i := range interceptors {
		interceptor := interceptors[i]
		if interceptor == nil {
			continue
		}

		next := sender
		sender = ResponseSenderFunc(
			func(ctx context.Context, res *OutboundResponseEnvelope, opts *SendResponseOptions) error {
				return errors.Wrap(interceptor.InterceptOutboundResponse(ctx, res, opts, next))
			},
		)
	}

	return sender
}

type InboundRequestInterceptorChain interface {
	// UseInboundRequestInterceptor adds interceptor for inbound requests.
	// The interceptor can be removed by calling the returned unbind function.
	UseInboundRequestInterceptor(interceptor InboundRequestInterceptor) (unbind func())
}

type InboundResponseInterceptorChain interface {
	// UseInboundResponseInterceptor adds interceptor for inbound responses.
	// The interceptor can be removed by calling the returned unbind function.
	UseInboundResponseInterceptor(interceptor InboundResponseInterceptor) (unbind func())
}

type OutboundRequestInterceptorChain interface {
	// UseOutboundRequestInterceptor adds interceptor for outbound requests.
	// The interceptor can be removed by calling the returned unbind function.
	UseOutboundRequestInterceptor(interceptor OutboundRequestInterceptor) (unbind func())
}

type OutboundResponseInterceptorChain interface {
	// UseOutboundResponseInterceptor adds interceptor for outbound responses.
	// The interceptor can be removed by calling the returned unbind function.
	UseOutboundResponseInterceptor(interceptor OutboundResponseInterceptor) (unbind func())
}

type MessageInterceptorChain interface {
	InboundRequestInterceptorChain
	InboundResponseInterceptorChain
	OutboundRequestInterceptorChain
	OutboundResponseInterceptorChain
	// UseInterceptor adds all non-nil interceptors from the provided object.
	// The interceptor can be removed by calling the returned unbind function.
	UseInterceptor(interceptor MessageInterceptor) (unbind func())
}

type baseMessageInterceptorChain struct {
	inReqInts  types.CallbackManager[InboundRequestInterceptor]
	inResInts  types.CallbackManager[InboundResponseInterceptor]
	outReqInts types.CallbackManager[OutboundRequestInterceptor]
	outResInts types.CallbackManager[OutboundResponseInterceptor]
}

func (ch *baseMessageInterceptorChain) UseInboundRequestInterceptor(interceptor InboundRequestInterceptor) (unbind func()) {
	return ch.inReqInts.Add(interceptor)
}

func (ch *baseMessageInterceptorChain) UseInboundResponseInterceptor(interceptor InboundResponseInterceptor) (unbind func()) {
	return ch.inResInts.Add(interceptor)
}

func (ch *baseMessageInterceptorChain) UseOutboundRequestInterceptor(interceptor OutboundRequestInterceptor) (unbind func()) {
	return ch.outReqInts.Add(interceptor)
}

func (ch *baseMessageInterceptorChain) UseOutboundResponseInterceptor(interceptor OutboundResponseInterceptor) (unbind func()) {
	return ch.outResInts.Add(interceptor)
}

func (ch *baseMessageInterceptorChain) UseInterceptor(interceptor MessageInterceptor) (unbind func()) {
	if interceptor == nil {
		return func() {}
	}

	var unbinds []func()
	if inbound := interceptor.InboundRequestInterceptor(); inbound != nil {
		unbinds = append(unbinds, ch.UseInboundRequestInterceptor(inbound))
	}

	if inbound := interceptor.InboundResponseInterceptor(); inbound != nil {
		unbinds = append(unbinds, ch.UseInboundResponseInterceptor(inbound))
	}

	if outbound := interceptor.OutboundRequestInterceptor(); outbound != nil {
		unbinds = append(unbinds, ch.UseOutboundRequestInterceptor(outbound))
	}

	if outbound := interceptor.OutboundResponseInterceptor(); outbound != nil {
		unbinds = append(unbinds, ch.UseOutboundResponseInterceptor(outbound))
	}

	return func() {
		for _, fn := range unbinds {
			fn()
		}
	}
}
