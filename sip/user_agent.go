package sip

import "context"

type CancelHandler func(ctx context.Context, req *InboundRequest)

type ErrorHandler func(ctx context.Context, err error)

type UserAgentServer interface {
	Request() *InboundRequest
	Respond(ctx context.Context, sts ResponseStatus, opts *RespondOptions) error
	OnCancel(fn CancelHandler) (cancel func())
	OnError(fn ErrorHandler) (cancel func())
}

type RespondOptions struct {
	ResponseOptions *ResponseOptions
	SendOptions     *SendResponseOptions
}

func (o *RespondOptions) resOpts() *ResponseOptions {
	if o == nil {
		return nil
	}
	return o.ResponseOptions
}

func (o *RespondOptions) sendOpts() *SendResponseOptions {
	if o == nil {
		return nil
	}
	return o.SendOptions
}

type UserAgentClient interface {
	Request() *OutboundRequest
	Cancel(ctx context.Context, opts *SendRequestOptions) error
	OnResponse(fn TransportResponseHandler) (cancel func())
	OnError(fn ErrorHandler) (cancel func())
}
