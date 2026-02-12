package sip

import (
	"context"
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
	return fn(ctx, req, next) //errtrace:skip
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
	return fn(ctx, res, next) //errtrace:skip
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
	return fn(ctx, req, opts, next) //errtrace:skip
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
	return fn(ctx, res, opts, next) //errtrace:skip
}

// MessageInterceptor provides optional inbound/outbound interceptors.
type MessageInterceptor interface {
	InboundRequestInterceptor() InboundRequestInterceptor
	InboundResponseInterceptor() InboundResponseInterceptor
	OutboundRequestInterceptor() OutboundRequestInterceptor
	OutboundResponseInterceptor() OutboundResponseInterceptor
}

// MessageInterceptorAdapter is a helper to create an [MessageInterceptor] from functions.
type MessageInterceptorAdapter struct {
	InboundRequest   InboundRequestInterceptor
	InboundResponse  InboundResponseInterceptor
	OutboundRequest  OutboundRequestInterceptor
	OutboundResponse OutboundResponseInterceptor
}

func (f MessageInterceptorAdapter) InboundRequestInterceptor() InboundRequestInterceptor {
	return f.InboundRequest
}

func (f MessageInterceptorAdapter) InboundResponseInterceptor() InboundResponseInterceptor {
	return f.InboundResponse
}

func (f MessageInterceptorAdapter) OutboundRequestInterceptor() OutboundRequestInterceptor {
	return f.OutboundRequest
}

func (f MessageInterceptorAdapter) OutboundResponseInterceptor() OutboundResponseInterceptor {
	return f.OutboundResponse
}

type noopMessageInterceptor struct{}

type NoopMessageInterceptor = noopMessageInterceptor

func (noopMessageInterceptor) InboundRequestInterceptor() InboundRequestInterceptor { return nil }

func (noopMessageInterceptor) InboundResponseInterceptor() InboundResponseInterceptor { return nil }

func (noopMessageInterceptor) OutboundRequestInterceptor() OutboundRequestInterceptor { return nil }

func (noopMessageInterceptor) OutboundResponseInterceptor() OutboundResponseInterceptor { return nil }

type MessageInterceptorChain interface {
	UseInterceptor(interceptor MessageInterceptor) (unbind func())
}

// ChainInboundRequest builds a request receiver pipeline in FIFO order.
func ChainInboundRequest(
	interceptors []InboundRequestInterceptor,
	final RequestReceiver,
) RequestReceiver {
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
			return interceptor.InterceptInboundRequest(ctx, req, next) //errtrace:skip
		})
	}
	return receiver
}

// ChainInboundResponse builds a response receiver pipeline in FIFO order.
func ChainInboundResponse(
	interceptors []InboundResponseInterceptor,
	final ResponseReceiver,
) ResponseReceiver {
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
			return interceptor.InterceptInboundResponse(ctx, res, next) //errtrace:skip
		})
	}
	return receiver
}

// ChainOutboundRequest builds a request sender pipeline in LIFO order.
func ChainOutboundRequest(
	interceptors []OutboundRequestInterceptor,
	final RequestSender,
) RequestSender {
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
				return interceptor.InterceptOutboundRequest(ctx, req, opts, next) //errtrace:skip
			},
		)
	}
	return sender
}

// ChainOutboundResponse builds a response sender pipeline in LIFO order.
func ChainOutboundResponse(
	interceptors []OutboundResponseInterceptor,
	final ResponseSender,
) ResponseSender {
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
				return interceptor.InterceptOutboundResponse(ctx, res, opts, next) //errtrace:skip
			},
		)
	}
	return sender
}
