package sip

import (
	"context"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

// RequestSender is an interface for sending requests.
type RequestSender interface {
	// SendRequest sends the request to the remote address specified in the envelope.
	//
	// Context can be used to cancel the request sending process through the deadline.
	SendRequest(ctx context.Context, req *RequestEnvelope, opts ...SendRequestOptions) error
}

type RequestSenderFunc func(ctx context.Context, req *RequestEnvelope, opts ...SendRequestOptions) error

func (fn RequestSenderFunc) SendRequest(
	ctx context.Context,
	req *RequestEnvelope,
	opts ...SendRequestOptions,
) error {
	return errors.Wrap(fn(ctx, req, opts...))
}

// SendRequestOptions are options for sending a request.
type SendRequestOptions struct {
	// RenderOptions are options for rendering the request.
	RenderOptions RenderOptions `json:"render_options,omitzero"`
	// LookupOptions are options for looking up the target address.
	LookupOptions LookupMessageAddrsOptions `json:"lookup_options,omitzero"`
	// TODO: options for multicast
}

type ResponseSender interface {
	// SendResponse sends the response to a remote address resolved with steps
	// defined in RFC 3261 Section 18.2.2. and RFC 3263 Section 5.
	//
	// Context can be used to cancel the response sending process through the deadline.
	SendResponse(ctx context.Context, res *ResponseEnvelope, opts ...SendResponseOptions) error
}

type ResponseSenderFunc func(ctx context.Context, res *ResponseEnvelope, opts ...SendResponseOptions) error

func (fn ResponseSenderFunc) SendResponse(
	ctx context.Context,
	res *ResponseEnvelope,
	opts ...SendResponseOptions,
) error {
	return errors.Wrap(fn(ctx, res, opts...))
}

// SendResponseOptions are options for sending a response.
type SendResponseOptions struct {
	// RenderOptions are options for rendering the response.
	RenderOptions RenderOptions `json:"render_options,omitzero"`
	// LookupOptions are options for looking up the target address.
	LookupOptions LookupMessageAddrsOptions `json:"lookup_options,omitzero"`
}

type Responder interface {
	Respond(ctx context.Context, req *RequestEnvelope, sts ResponseStatus, opts ...RespondOptions) error
}

type ResponderFunc func(ctx context.Context, req *RequestEnvelope, sts ResponseStatus, opts ...RespondOptions) error

func (fn ResponderFunc) Respond(
	ctx context.Context,
	req *RequestEnvelope,
	sts ResponseStatus,
	opts ...RespondOptions,
) error {
	return errors.Wrap(fn(ctx, req, sts, opts...))
}

// RespondOptions are options for respond helper functions.
type RespondOptions struct {
	// ResponseOptions are options for creating the response.
	ResponseOptions ResponseOptions `json:"response_options,omitzero"`
	// SendOptions are options for sending the response.
	SendOptions SendResponseOptions `json:"send_options,omitzero"`
}

func Respond(
	ctx context.Context,
	req *RequestEnvelope,
	sts ResponseStatus,
	sndr ResponseSender,
	opts ...RespondOptions,
) error {
	resOpts := util.LastSliceElemOr(opts, RespondOptions{})

	res, err := req.NewResponse(sts, resOpts.ResponseOptions)
	if err != nil {
		return errors.Wrap(err)
	}

	return errors.Wrap(sndr.SendResponse(ctx, res, resOpts.SendOptions))
}
