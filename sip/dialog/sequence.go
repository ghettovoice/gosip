package dialog

import (
	"math"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// ValidateRequest accounts for the remote endpoint's CSeq number on an
// inbound ordinary in-dialog request. A first or greater number advances
// the remote baseline, an equal number changes nothing, and a lower
// number reports [ErrCSeqOutOfOrder] without
// mutating the dialog; the caller answers a stale request itself (a 500
// response per RFC 3261 Section 12.2.2). Transaction-level deduplication
// stays separate from this ordering check.
//
// It returns [ErrInvalidMessage] for a malformed request — a missing,
// duplicated, or untyped CSeq, a CSeq method that does not exactly match
// the request line, or a number outside 1..math.MaxUint32 — and
// [ErrMismatch] for a well-formed request carrying another dialog's
// Call-ID or tags. ACK and CANCEL in any case report
// [ErrUnsupportedMethod]: they share sequence numbers with the request
// they answer and never enter accounting. A valid matched request on a
// terminated dialog reports [ErrTerminated]. No target refresh or
// response generation happens here.
func (d *Dialog) ValidateRequest(req *sip.Request) error {
	if req == nil {
		return newInvalidMsgErr("nil request")
	}
	cseq, err := reqCSeq(req)
	if err != nil {
		return errors.Wrap(err)
	}
	callID, err := singleCallID(req.Headers)
	if err != nil {
		return errors.Wrap(err)
	}
	from, err := singleFrom(req.Headers)
	if err != nil {
		return errors.Wrap(err)
	}
	to, err := singleTo(req.Headers)
	if err != nil {
		return errors.Wrap(err)
	}
	fromTag, fromTagged, err := tagParam(from.Params)
	if err != nil {
		return errors.Wrap(err)
	}
	toTag, toTagged, err := tagParam(to.Params)
	if err != nil {
		return errors.Wrap(err)
	}
	// An inbound request in this dialog carries the remote party's tag
	// in From and the local tag in To; absent or foreign tags mean it is
	// well formed but belongs elsewhere.
	if callID != d.id.CallID || !fromTagged || !toTagged ||
		fromTag != d.id.RemoteTag || toTag != d.id.LocalTag {
		return errors.PrefixWrap(ErrMismatch, "request of another dialog")
	}
	if mtd := req.Method.Canonic(); mtd == sip.RequestMethodAck || mtd == sip.RequestMethodCancel {
		return errors.PrefixWrap(ErrUnsupportedMethod, "method %q", req.Method)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state == StateTerminated {
		return errors.PrefixWrap(ErrTerminated)
	}
	if cseq.SeqNum > d.rmtCSeq {
		d.rmtCSeq = cseq.SeqNum
		return nil
	}
	if cseq.SeqNum < d.rmtCSeq {
		return errors.PrefixWrap(ErrCSeqOutOfOrder, "remote CSeq %d below %d", cseq.SeqNum, d.rmtCSeq)
	}
	return nil
}

// nextLocCSeqLocked allocates the next local CSeq number and must be
// called with d.mu held. The counter is written only when a number is
// available and never wraps past math.MaxUint32 (RFC 3261 Section
// 12.2.1.1).
func (d *Dialog) nextLocCSeqLocked() (uint, error) {
	if d.locCSeq >= math.MaxUint32 {
		return 0, errors.PrefixWrap(ErrCSeqExhausted)
	}
	d.locCSeq++
	return d.locCSeq, nil
}

// reqCSeq returns the request's sole well-typed CSeq header,
// requiring a valid request-line method token that exactly matches the
// CSeq method and a number in 1..math.MaxUint32. It is the shared
// sequence check for inbound in-dialog requests, including the later ACK
// path; the initial INVITE keeps its own tighter bound in singleCSeq.
func reqCSeq(req *sip.Request) (*header.CSeq, error) {
	if !req.Method.IsValid() {
		return nil, newInvalidMsgErr("invalid request method %q", req.Method)
	}
	hs := req.Headers.Get("CSeq")
	if len(hs) != 1 {
		return nil, newInvalidMsgErr("CSeq header count %d, want 1", len(hs))
	}
	cseq, ok := hs[0].(*header.CSeq)
	if !ok || cseq == nil {
		return nil, newInvalidMsgErr("invalid CSeq header type %T", hs[0])
	}
	if cseq.Method != req.Method {
		return nil, newInvalidMsgErr("CSeq method %q, want %q", cseq.Method, req.Method)
	}
	if cseq.SeqNum == 0 || cseq.SeqNum > math.MaxUint32 {
		return nil, newInvalidMsgErr("CSeq number %d out of range", cseq.SeqNum)
	}
	return cseq, nil
}
