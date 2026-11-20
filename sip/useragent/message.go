package useragent

import (
	"bytes"
	"context"
	"mime"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

// MessageOperationOptions configures a [UserAgent.Message] operation.
type MessageOperationOptions struct {
	// OperationOptions carries the shared per-operation timing and the
	// progress and completion callbacks bound at creation.
	OperationOptions
	// Body is the opaque MESSAGE payload, copied before the operation
	// starts and sent verbatim.
	Body []byte
	// ContentType is the MIME content type of Body. An empty value selects
	// the text/plain default. The value is validated with
	// [mime.ParseMediaType] before the operation starts, and media-type
	// parameters are preserved in the built Content-Type header.
	ContentType string
}

// MessageOperation is the handle of an in-flight [UserAgent.Message] operation.
// The embedded operation exposes Wait, Cancel, and Done.
type MessageOperation struct{ *operation }

// Message starts a MESSAGE operation toward target on the element bound
// in ctx by [sip.ContextWithElement].
//
// The synchronous gate runs before any worker starts: a done context
// reports [ErrCanceled], a missing element [ErrMissingElement], and a
// zero-value or never-initialized user agent [ErrClosed]. The target is
// validated and cloned — nil, typed-nil, or invalid targets report
// [ErrInvalidRequest] — but its scheme is not restricted: non-SIP URIs are
// left to the element's URIConverter for routing. A malformed ContentType
// also reports [ErrInvalidRequest]. The built request never carries a
// Contact header by package contract and always carries Content-Type when
// a body is present, defaulting to text/plain.
//
// The returned handle resolves through [operation.Wait]: an ordinary final
// response — success or failure — resolves the response outcome, while a
// send failure, cancellation, or shutdown resolves the classified error.
func (ua *UserAgent) Message(
	ctx context.Context,
	target sip.AnyURI,
	opts ...MessageOperationOptions,
) (*MessageOperation, error) {
	// A pre-canceled caller never reaches a worker.
	if err := ctx.Err(); err != nil {
		return nil, errors.Wrap(NewCanceledError(err))
	}
	elm, ok := sip.ElementFromContext(ctx)
	if !ok {
		return nil, errors.Wrap(NewMissingElementError())
	}
	if ua.opts.AOR == nil {
		return nil, errors.Wrap(NewClosedError())
	}

	o := util.LastSliceElem(opts, MessageOperationOptions{})

	// The URI scheme is deliberately not checked: the element's
	// URIConverter owns non-SIP target conversion.
	if util.IsNil(target) || !target.IsValid() {
		return nil, errors.Wrap(NewInvalidRequestError("invalid target URI %q", target))
	}
	target = target.Clone()
	body := bytes.Clone(o.Body)

	if o.ContentType != "" {
		if _, _, err := mime.ParseMediaType(o.ContentType); err != nil {
			return nil, errors.Wrap(NewInvalidRequestError("invalid content type %q", o.ContentType))
		}
	}

	req, err := ua.buildReq(sip.RequestMethodMessage, target, body, o.ContentType)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	op, err := ua.startOp(ctx, o.OperationOptions, func(opCtx context.Context) (*sip.ResponseEnvelope, error) {
		return ua.runOp(opCtx, elm, req, o.OperationOptions)
	})
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return &MessageOperation{op}, nil
}
