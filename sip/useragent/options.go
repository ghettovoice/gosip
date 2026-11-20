package useragent

import (
	"context"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

// OptionsOperationOptions configures a [UserAgent.Options] operation.
type OptionsOperationOptions struct {
	// OperationOptions carries the shared per-operation timing and the
	// progress and completion callbacks bound at creation.
	OperationOptions
}

// OptionsOperation is the handle of an in-flight [UserAgent.Options] operation.
// The embedded operation exposes Wait, Cancel, and Done.
type OptionsOperation struct{ *operation }

// Options starts an OPTIONS operation toward target on the element bound
// in ctx by [sip.ContextWithElement].
//
// The synchronous gate runs before any worker starts: a done context
// reports [ErrCanceled], a missing element [ErrMissingElement], and a
// zero-value or never-initialized user agent [ErrClosed]. The target is
// validated and cloned — nil, typed-nil, or invalid targets report
// [ErrInvalidRequest] — but its scheme is not restricted: non-SIP URIs are
// left to the element's URIConverter for routing. The built request never
// carries a body, and it carries a Contact header only when the user
// agent has a configured contact.
//
// The returned handle resolves through [operation.Wait]: an ordinary final
// response — success or failure — resolves the response outcome, while a
// send failure, cancellation, or shutdown resolves the classified error.
func (ua *UserAgent) Options(
	ctx context.Context,
	target sip.AnyURI,
	opts ...OptionsOperationOptions,
) (*OptionsOperation, error) {
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

	o := util.LastSliceElem(opts, OptionsOperationOptions{})

	// The URI scheme is deliberately not checked: the element's
	// URIConverter owns non-SIP target conversion.
	if util.IsNil(target) || !target.IsValid() {
		return nil, errors.Wrap(NewInvalidRequestError("invalid target URI %q", target))
	}
	target = target.Clone()

	req, err := ua.buildReq(sip.RequestMethodOptions, target, nil, "")
	if err != nil {
		return nil, errors.Wrap(err)
	}

	op, err := ua.startOp(
		ctx,
		o.OperationOptions,
		func(opCtx context.Context) (*sip.ResponseEnvelope, error) {
			return ua.runOp(opCtx, elm, req, o.OperationOptions)
		},
	)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return &OptionsOperation{op}, nil
}
