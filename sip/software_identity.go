package sip

import (
	"context"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip/header"
)

// SoftwareIdentity is an element component that stamps the configured
// software identity onto outbound endpoint messages. It appends
// User-Agent to outbound requests and Server to outbound responses, only
// when the corresponding field is non-empty and the header is absent —
// an existing header, even an empty one, is never overwritten. Envelopes
// marked with [MessageEnvelope.MarkForwarded] pass through unchanged.
//
// Registration is explicit through [Element.UseComponent]; the zero value
// is a no-op interceptor.
type SoftwareIdentity struct {
	UserAgent string
	Server    string
}

var (
	_ OutboundRequestInterceptor  = SoftwareIdentity{}
	_ OutboundResponseInterceptor = SoftwareIdentity{}
)

func (id SoftwareIdentity) InterceptOutboundRequest(
	ctx context.Context,
	next RequestSender,
	req *RequestEnvelope,
	opts ...SendRequestOptions,
) error {
	if !req.Forwarded() && id.UserAgent != "" {
		req.WithMessage(func(r *Request) {
			if r.Headers == nil {
				r.Headers = make(Headers)
			}
			if !r.Headers.Has("User-Agent") {
				r.Headers.Append(header.UserAgent(id.UserAgent))
			}
		})
	}
	return errors.Wrap(next.SendRequest(ctx, req, opts...))
}

func (id SoftwareIdentity) InterceptOutboundResponse(
	ctx context.Context,
	next ResponseSender,
	res *ResponseEnvelope,
	opts ...SendResponseOptions,
) error {
	if !res.Forwarded() && id.Server != "" {
		res.WithMessage(func(r *Response) {
			if r.Headers == nil {
				r.Headers = make(Headers)
			}
			if !r.Headers.Has("Server") {
				r.Headers.Append(header.Server(id.Server))
			}
		})
	}
	return errors.Wrap(next.SendResponse(ctx, res, opts...))
}
