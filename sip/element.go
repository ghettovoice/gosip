package sip

import (
	"context"
	"log/slog"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip/header"
)

// Element setups basic inbound/outbound message pipeline and
// provides common SIP element message processing.
type Element struct {
	NoopMessageInterceptor
	name string
	tpm  *TransportManager
	txm  *TransactionManager
	log  *slog.Logger
}

// ElementOptions configures an [Element].
// All fields are optional.
type ElementOptions struct {
	*TransactionManagerOptions
	// Logger is the logger used by the element.
	// If nil, the [log.Default] is used.
	Logger *slog.Logger
}

func (o *ElementOptions) txmOpts() *TransactionManagerOptions {
	if o == nil {
		return nil
	}
	return o.TransactionManagerOptions
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

	if tp == nil {
		return nil, errors.NewInvalidArgumentErrorWrap("nil transport")
	}

	tpm := new(TransportManager)
	if err := tpm.TrackTransport(tp, true); err != nil {
		_ = tpm.Close()
		return nil, errors.Wrap(err)
	}

	var txm *TransactionManager
	if txmOpts := opts.txmOpts(); txmOpts != nil {
		if txmOpts.Logger == nil {
			txmOpts.Logger = opts.log()
		}

		txm = NewTransactionManager(txmOpts)
	}

	elm := &Element{
		name: name,
		tpm:  tpm,
		txm:  txm,
		log:  opts.log(),
	}
	elm.log = elm.log.With(slog.Any("element", elm))

	tpm.UseInterceptor(txm)
	tpm.UseInterceptor(elm)

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
		slog.Any("default_transport", elm.tpm.GetDefaultTransport()),
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

	return errors.Wrap(next.SendRequest(ctx, req, opts))
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

	return errors.Wrap(next.SendResponse(ctx, res, opts))
}

func (elm *Element) Close() error {
	if elm == nil {
		return nil
	}

	return errors.JoinWrap(
		elm.txm.Close(),
		elm.tpm.Close(),
	)
}

func (elm *Element) SendRequest(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	opts *SendRequestOptions,
) error {
	return elm.tpm.SendRequest(ctx, req, opts)
}

func (elm *Element) SendResponse(
	ctx context.Context,
	res *OutboundResponseEnvelope,
	opts *SendResponseOptions,
) error {
	return elm.tpm.SendResponse(ctx, res, opts)
}

func (*Element) RequestStateful(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	opts *ClientTransactionOptions,
) (ClientTransaction, error) {
	panic("not implemented")
}

func (*Element) RespondStateful(
	ctx context.Context,
	res *OutboundResponseEnvelope,
	opts *ServerTransactionOptions,
) error {
	panic("not implemented")
}
