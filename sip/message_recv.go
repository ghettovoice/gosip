package sip

import (
	"context"
	"slices"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
)

// RequestReceiver is an interface for receiving requests.
type RequestReceiver interface {
	// RecvRequest receives a valid inbound request from the transport or downstream receiver.
	RecvRequest(ctx context.Context, req *RequestEnvelope) error
}

type RequestReceiverFunc func(ctx context.Context, req *RequestEnvelope) error

func (fn RequestReceiverFunc) RecvRequest(ctx context.Context, req *RequestEnvelope) error {
	return errors.Wrap(fn(ctx, req))
}

// ResponseReceiver is an interface for receiving responses.
type ResponseReceiver interface {
	// RecvResponse receives a valid inbound response from the transport or downstream receiver.
	RecvResponse(ctx context.Context, res *ResponseEnvelope) error
}

type ResponseReceiverFunc func(ctx context.Context, res *ResponseEnvelope) error

func (fn ResponseReceiverFunc) RecvResponse(ctx context.Context, res *ResponseEnvelope) error {
	return errors.Wrap(fn(ctx, res))
}

// InboundRequestInterceptor intercepts inbound requests before they reach the receiver.
type InboundRequestInterceptor interface {
	InterceptInboundRequest(ctx context.Context, next RequestReceiver, req *RequestEnvelope) error
}

type InboundRequestInterceptorFunc func(
	ctx context.Context,
	next RequestReceiver,
	req *RequestEnvelope,
) error

func (fn InboundRequestInterceptorFunc) InterceptInboundRequest(
	ctx context.Context,
	next RequestReceiver,
	req *RequestEnvelope,
) error {
	return errors.Wrap(fn(ctx, next, req))
}

// InboundResponseInterceptor intercepts inbound responses before they reach the receiver.
type InboundResponseInterceptor interface {
	InterceptInboundResponse(ctx context.Context, next ResponseReceiver, res *ResponseEnvelope) error
}

type InboundResponseInterceptorFunc func(
	ctx context.Context,
	next ResponseReceiver,
	res *ResponseEnvelope,
) error

func (fn InboundResponseInterceptorFunc) InterceptInboundResponse(
	ctx context.Context,
	next ResponseReceiver,
	res *ResponseEnvelope,
) error {
	return errors.Wrap(fn(ctx, next, res))
}

// OutboundRequestInterceptor intercepts outbound requests before they are sent.
type OutboundRequestInterceptor interface {
	InterceptOutboundRequest(
		ctx context.Context,
		next RequestSender,
		req *RequestEnvelope,
		opts ...SendRequestOptions,
	) error
}

type OutboundRequestInterceptorFunc func(
	ctx context.Context,
	next RequestSender,
	req *RequestEnvelope,
	opts ...SendRequestOptions,
) error

func (fn OutboundRequestInterceptorFunc) InterceptOutboundRequest(
	ctx context.Context,
	next RequestSender,
	req *RequestEnvelope,
	opts ...SendRequestOptions,
) error {
	return errors.Wrap(fn(ctx, next, req, opts...))
}

// OutboundResponseInterceptor intercepts outbound responses before they are sent.
type OutboundResponseInterceptor interface {
	InterceptOutboundResponse(
		ctx context.Context,
		next ResponseSender,
		res *ResponseEnvelope,
		opts ...SendResponseOptions,
	) error
}

type OutboundResponseInterceptorFunc func(
	ctx context.Context,
	next ResponseSender,
	res *ResponseEnvelope,
	opts ...SendResponseOptions,
) error

func (fn OutboundResponseInterceptorFunc) InterceptOutboundResponse(
	ctx context.Context,
	next ResponseSender,
	res *ResponseEnvelope,
	opts ...SendResponseOptions,
) error {
	return errors.Wrap(fn(ctx, next, res, opts...))
}

// MessageInterceptor provides optional inbound/outbound interceptors.
type MessageInterceptor interface {
	InboundRequestInterceptor
	InboundResponseInterceptor
	OutboundRequestInterceptor
	OutboundResponseInterceptor
}

type StdMessageInterceptor struct {
	InboundRequestInterceptor
	InboundResponseInterceptor
	OutboundRequestInterceptor
	OutboundResponseInterceptor
}

func (intcptr StdMessageInterceptor) InterceptInboundRequest(
	ctx context.Context,
	next RequestReceiver,
	req *RequestEnvelope,
) error {
	if intcptr.InboundRequestInterceptor == nil {
		return errors.Wrap(next.RecvRequest(ctx, req))
	}
	return errors.Wrap(intcptr.InboundRequestInterceptor.InterceptInboundRequest(ctx, next, req))
}

func (intcptr StdMessageInterceptor) InterceptInboundResponse(
	ctx context.Context,
	next ResponseReceiver,
	res *ResponseEnvelope,
) error {
	if intcptr.InboundResponseInterceptor == nil {
		return errors.Wrap(next.RecvResponse(ctx, res))
	}
	return errors.Wrap(intcptr.InboundResponseInterceptor.InterceptInboundResponse(ctx, next, res))
}

func (intcptr StdMessageInterceptor) InterceptOutboundRequest(
	ctx context.Context,
	next RequestSender,
	req *RequestEnvelope,
	opts ...SendRequestOptions,
) error {
	if intcptr.OutboundRequestInterceptor == nil {
		return errors.Wrap(next.SendRequest(ctx, req, opts...))
	}
	return errors.Wrap(intcptr.OutboundRequestInterceptor.InterceptOutboundRequest(ctx, next, req, opts...))
}

func (intcptr StdMessageInterceptor) InterceptOutboundResponse(
	ctx context.Context,
	next ResponseSender,
	res *ResponseEnvelope,
	opts ...SendResponseOptions,
) error {
	if intcptr.OutboundResponseInterceptor == nil {
		return errors.Wrap(next.SendResponse(ctx, res, opts...))
	}
	return errors.Wrap(intcptr.OutboundResponseInterceptor.InterceptOutboundResponse(ctx, next, res, opts...))
}

// NoopMessageInterceptor is a passthrough MessageInterceptor that delegates all calls to next.
// Embed it to satisfy MessageInterceptor while only overriding the methods you need.
type NoopMessageInterceptor struct{}

func (NoopMessageInterceptor) InterceptInboundRequest(
	ctx context.Context,
	next RequestReceiver,
	req *RequestEnvelope,
) error {
	return errors.Wrap(next.RecvRequest(ctx, req))
}

func (NoopMessageInterceptor) InterceptInboundResponse(
	ctx context.Context,
	next ResponseReceiver,
	res *ResponseEnvelope,
) error {
	return errors.Wrap(next.RecvResponse(ctx, res))
}

func (NoopMessageInterceptor) InterceptOutboundRequest(
	ctx context.Context,
	next RequestSender,
	req *RequestEnvelope, opts ...SendRequestOptions,
) error {
	return errors.Wrap(next.SendRequest(ctx, req, opts...))
}

func (NoopMessageInterceptor) InterceptOutboundResponse(
	ctx context.Context,
	next ResponseSender,
	res *ResponseEnvelope,
	opts ...SendResponseOptions,
) error {
	return errors.Wrap(next.SendResponse(ctx, res, opts...))
}

// InterceptInboundRequest builds a request receiver pipeline in FIFO order.
func InterceptInboundRequest(interceptors []InboundRequestInterceptor, final RequestReceiver) RequestReceiver {
	if final == nil {
		return nil
	}

	receiver := final
	for _, interceptor := range slices.Backward(interceptors) {
		if interceptor == nil {
			continue
		}

		next := receiver
		receiver = RequestReceiverFunc(func(ctx context.Context, req *RequestEnvelope) error {
			return errors.Wrap(interceptor.InterceptInboundRequest(ctx, next, req))
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
	for _, interceptor := range slices.Backward(interceptors) {
		if interceptor == nil {
			continue
		}

		next := receiver
		receiver = ResponseReceiverFunc(func(ctx context.Context, res *ResponseEnvelope) error {
			return errors.Wrap(interceptor.InterceptInboundResponse(ctx, next, res))
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
			func(ctx context.Context, req *RequestEnvelope, opts ...SendRequestOptions) error {
				return errors.Wrap(interceptor.InterceptOutboundRequest(ctx, next, req, opts...))
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
			func(ctx context.Context, res *ResponseEnvelope, opts ...SendResponseOptions) error {
				return errors.Wrap(interceptor.InterceptOutboundResponse(ctx, next, res, opts...))
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
	// UseMessageInterceptor adds all non-nil interceptors from the provided object.
	// The interceptor can be removed by calling the returned unbind function.
	UseMessageInterceptor(interceptor MessageInterceptor) (unbind func())
}

type StdMsgInterceptChain struct {
	InboundRequestInterceptors   types.CallbackManager[InboundRequestInterceptor]
	InboundResponseInterceptors  types.CallbackManager[InboundResponseInterceptor]
	OutboundRequestInterceptors  types.CallbackManager[OutboundRequestInterceptor]
	OutboundResponseInterceptors types.CallbackManager[OutboundResponseInterceptor]
}

func (ch *StdMsgInterceptChain) UseInboundRequestInterceptor(interceptor InboundRequestInterceptor) (unbind func()) {
	return ch.InboundRequestInterceptors.Add(interceptor)
}

func (ch *StdMsgInterceptChain) UseInboundResponseInterceptor(interceptor InboundResponseInterceptor) (unbind func()) {
	return ch.InboundResponseInterceptors.Add(interceptor)
}

func (ch *StdMsgInterceptChain) UseOutboundRequestInterceptor(interceptor OutboundRequestInterceptor) (unbind func()) {
	return ch.OutboundRequestInterceptors.Add(interceptor)
}

func (ch *StdMsgInterceptChain) UseOutboundResponseInterceptor(interceptor OutboundResponseInterceptor) (unbind func()) {
	return ch.OutboundResponseInterceptors.Add(interceptor)
}

func (ch *StdMsgInterceptChain) UseMessageInterceptor(interceptor MessageInterceptor) (unbind func()) {
	if interceptor == nil {
		return func() {}
	}

	unbinds := []func(){
		ch.UseInboundRequestInterceptor(interceptor),
		ch.UseInboundResponseInterceptor(interceptor),
		ch.UseOutboundRequestInterceptor(interceptor),
		ch.UseOutboundResponseInterceptor(interceptor),
	}

	return func() {
		for _, fn := range unbinds {
			fn()
		}
	}
}
