package sip

import (
	"context"
	"log/slog"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/log"
)

// Element setups basic inbound/outbound message pipeline and
// provides common SIP element message processing.
type Element struct {
	noopMessageInterceptor
	name string
	log  *slog.Logger
}

type ElementOptions struct {
	// Logger is the logger used by the element.
	// If nil, the [log.Default] is used.
	Logger *slog.Logger
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
// Options are optional, default options are used if nil (see [ElementOptions]).
func NewElement(name string, opts *ElementOptions) (*Element, error) {
	if name == "" {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid name"))
	}

	elm := &Element{
		name: name,
		log:  opts.log(),
	}
	elm.log = elm.log.With("element", elm)
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
		return zeroSlogValue
	}
	return slog.GroupValue(
		slog.Any("name", elm.name),
	)
}

func (elm *Element) OutboundRequestInterceptor() OutboundRequestInterceptor {
	return OutboundRequestInterceptorFunc(elm.interceptOutboundRequest)
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
	return errtrace.Wrap(next.SendRequest(ctx, req, opts))
}

func (elm *Element) OutboundResponseInterceptor() OutboundResponseInterceptor {
	return OutboundResponseInterceptorFunc(elm.interceptOutboundResponse)
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
	return errtrace.Wrap(next.SendResponse(ctx, res, opts))
}
