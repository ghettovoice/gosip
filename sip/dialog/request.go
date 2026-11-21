package dialog

import (
	"bytes"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// RequestOptions carries the optional payload of an ordinary in-dialog
// request built by [Dialog.NewRequest]. When several values are passed,
// the last whole value wins.
type RequestOptions struct {
	// Body is the request body. It is cloned by the builder.
	Body []byte
	// ContentType describes Body and is required for a nonempty body.
	ContentType *header.ContentType
}

// NewRequest builds an ordinary in-dialog request and wraps it in a fresh
// envelope carrying no transport, address, forwarding, or resolution
// state. The Request-URI is the dialog remote target, From and To are
// exact detached copies of the dialog parties, and the dialog route set
// is appended as Route fields in order.
//
// A successful build consumes the next local CSeq number even if the
// request is never dispatched; the number is not rolled back. Callers
// dispatching from a shared dialog must serialize sends so numbers leave
// the endpoint in order.
//
// It returns [ErrInvalidMessage] for an invalid method token or options,
// [ErrUnsupportedMethod] for ACK, CANCEL, INVITE, or UPDATE in any case,
// [ErrTerminated] on a terminated dialog, and [ErrCSeqExhausted] when the
// local sequence counter cannot advance.
func (d *Dialog) NewRequest(method sip.RequestMethod, opts ...RequestOptions) (*sip.RequestEnvelope, error) {
	if !method.IsValid() {
		return nil, errors.Wrap(newInvalidMsgErr("invalid request method %q", method))
	}
	if mtd := method.Canonic(); mtd == sip.RequestMethodAck ||
		mtd == sip.RequestMethodCancel ||
		mtd == sip.RequestMethodInvite ||
		mtd == sip.RequestMethodUpdate {
		return nil, errors.PrefixWrap(ErrUnsupportedMethod, "method %q", method)
	}
	req, err := d.prepareReq(method, util.LastSliceElem(opts, RequestOptions{}))
	if err != nil {
		return nil, errors.Wrap(err)
	}

	d.mu.Lock()
	if d.state == StateTerminated {
		d.mu.Unlock()
		return nil, errors.PrefixWrap(ErrTerminated)
	}
	seq, err := d.nextLocCSeqLocked()
	if err != nil {
		d.mu.Unlock()
		return nil, err
	}
	routes := d.routes
	d.mu.Unlock()

	return finishReq(req, seq, routes), nil
}

// prepareReq validates the method and options and builds the request
// message detached from the dialog's stored state: fresh endpoint
// headers, the remote target as Request-URI (sanitized by the core on a
// copy), exact cloned parties as From/To, and a fresh Via branch. It runs
// before the state lock so nothing is committed when it fails.
func (d *Dialog) prepareReq(method sip.RequestMethod, opts RequestOptions) (*sip.Request, error) {
	if !method.IsValid() {
		return nil, newInvalidMsgErr("invalid request method %q", method)
	}
	if opts.ContentType != nil && !opts.ContentType.IsValid() {
		return nil, newInvalidMsgErr("invalid Content-Type")
	}
	if len(opts.Body) > 0 && opts.ContentType == nil {
		return nil, newInvalidMsgErr("body without Content-Type")
	}
	if d.target == nil {
		return nil, newInvalidMsgErr("missing remote target")
	}

	req, err := sip.NewRequest(method, d.target, d.loc.URI, d.rmt.URI, sip.RequestOptions{
		CallID: d.id.CallID,
		SeqNum: 1,
		Body:   bytes.Clone(opts.Body),
	})
	if err != nil {
		return nil, newInvalidMsgErr(err)
	}
	from := header.From(d.loc.Clone())
	to := header.To(d.rmt.Clone())
	req.Headers.Set(&from).Set(&to)
	if opts.ContentType != nil {
		req.Headers.Set(opts.ContentType.Clone())
	}
	return req, nil
}

// finishReq installs the chosen CSeq number and the route snapshot on
// the prepared request and wraps it in a fresh envelope. The number is
// not necessarily an allocated local one — an ACK carries the
// acknowledged INVITE's number. It runs after any state commit and
// cannot fail.
func finishReq(req *sip.Request, seq uint, routes []header.RouteHop) *sip.RequestEnvelope {
	req.Headers.Set(&header.CSeq{SeqNum: seq, Method: req.Method})
	if len(routes) > 0 {
		req.Headers.Append(header.Route(cloneRouteSet(routes)))
	}
	return sip.NewRequestEnvelope(req)
}
