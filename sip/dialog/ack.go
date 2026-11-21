package dialog

import (
	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

// NewACK builds the stateless ACK for a 2xx response that confirmed this
// dialog. The ACK reuses the acknowledged INVITE's CSeq number with
// method ACK, copies that INVITE's Authorization and Proxy-Authorization
// headers verbatim, and uses the dialog remote target and route set like
// an ordinary request. It allocates no CSeq number, creates no client
// transaction, and invokes no authenticator.
//
// The caller sends the envelope through [sip.Element.SendRequest], keeps
// it per fork/attempt until the response observation ends — a
// retransmitted 2xx is answered with the same prepared ACK — and must
// not log the copied credential values. An ACK is not rebuilt after
// termination.
//
// It returns [ErrInvalidMessage] for a malformed INVITE or response,
// [ErrNotEstablishing] for a non-2xx response, [ErrMismatch] for a
// well-formed exchange belonging to another fork or INVITE attempt,
// [ErrInvalidState] on an early or UAS dialog, and [ErrTerminated] after
// termination.
func (d *Dialog) NewACK(invite *sip.Request, res *sip.Response, opts ...RequestOptions) (*sip.RequestEnvelope, error) {
	if res == nil {
		return nil, newInvalidMsgErr("nil response")
	}
	if !res.Status.IsSuccessful() {
		return nil, errors.PrefixWrap(ErrNotEstablishing, "response status %v", res.Status)
	}
	inv, err := validInvite(invite)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	est, err := validEstablishing(inv, res)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	locTag, rmtTag := est.fromTag, est.toTag
	if d.role == RoleUAS {
		locTag, rmtTag = est.toTag, est.fromTag
	}
	if est.callID != d.id.CallID ||
		locTag != d.id.LocalTag || rmtTag != d.id.RemoteTag ||
		est.cseqNum != d.cseqNum || !est.key.Equal(d.key) {
		return nil, errors.PrefixWrap(ErrMismatch, "response of another dialog")
	}

	req, err := d.prepareReq(sip.RequestMethodAck, util.LastSliceElem(opts, RequestOptions{}))
	if err != nil {
		return nil, errors.Wrap(err)
	}
	req.Headers.CopyFrom(invite.Headers, "Authorization", "Proxy-Authorization")

	d.mu.Lock()
	if d.state == StateTerminated {
		d.mu.Unlock()
		return nil, errors.PrefixWrap(ErrTerminated)
	}
	if d.role != RoleUAC || d.state != StateConfirmed {
		d.mu.Unlock()
		return nil, errors.PrefixWrap(ErrInvalidState, "ACK requires a confirmed UAC dialog")
	}
	routes := d.routes
	d.mu.Unlock()

	return finishReq(req, d.cseqNum, routes), nil
}

// MatchACK reports whether req is the wire ACK for this dialog's initial
// INVITE 2xx: an exact ACK request line and CSeq method, the dialog's
// role-relative identity, and the original INVITE CSeq number. Only a
// confirmed UAS dialog recognizes an ACK; the check reads state and
// mutates nothing, in particular the remote CSeq baseline — an ACK
// carries the acknowledged request's number, not the next one.
func (d *Dialog) MatchACK(req *sip.Request) bool {
	if req == nil || req.Method != sip.RequestMethodAck {
		return false
	}
	cseq, err := reqCSeq(req)
	if err != nil || cseq.SeqNum != d.cseqNum {
		return false
	}
	id, err := IDFromRequest(req)
	if err != nil || id != d.id {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.role == RoleUAS && d.state == StateConfirmed
}
