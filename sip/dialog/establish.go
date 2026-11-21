package dialog

import (
	"math"
	"strings"
	"unicode"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// NewUAC establishes a caller-side dialog from the sent initial INVITE and
// the dialog-establishing response to it: a tagged provisional response
// creates an early dialog and a valid tagged 2xx a confirmed one. The
// dialog owns copies of the parties taken from the response From and To,
// and the local sequence is seeded with the INVITE's CSeq number.
//
// It returns [ErrInvalidMessage] for a malformed INVITE or response,
// [ErrNotEstablishing] for a response that cannot establish a dialog
// (100 Trying, an untagged provisional, or an unsuccessful final
// response), and [ErrMismatch] for a well-formed response that does not
// correlate to the INVITE attempt.
func NewUAC(invite *sip.Request, res *sip.Response) (*Dialog, error) {
	inv, err := validInvite(invite)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	est, err := validEstablishing(inv, res)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	state := StateConfirmed
	if est.early {
		state = StateEarly
	}
	return &Dialog{
		id:      ID{CallID: inv.callID, LocalTag: inv.fromTag, RemoteTag: est.toTag},
		role:    RoleUAC,
		loc:     est.from.Clone(),
		rmt:     est.to.Clone(),
		target:  est.target,
		routes:  est.routes,
		cseqNum: inv.cseqNum,
		key:     inv.key,
		state:   state,
		locCSeq: inv.cseqNum,
	}, nil
}

// NewUAS establishes a callee-side dialog from the received initial INVITE
// and the endpoint's tagged establishing response to it. The dialog owns
// copies of the parties taken from the request To and From, with the
// response's To tag installed on the local party, and the remote sequence
// is seeded with the INVITE's CSeq number. Error semantics match [NewUAC].
func NewUAS(invite *sip.Request, res *sip.Response) (*Dialog, error) {
	inv, err := validInvite(invite)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	est, err := validEstablishing(inv, res)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	loc := inv.to
	if loc.Params == nil {
		loc.Params = make(header.Values)
	}
	loc.Params.Set("tag", est.toTag)

	state := StateConfirmed
	if est.early {
		state = StateEarly
	}
	return &Dialog{
		id:      ID{CallID: inv.callID, LocalTag: est.toTag, RemoteTag: inv.fromTag},
		role:    RoleUAS,
		loc:     loc,
		rmt:     inv.from,
		target:  inv.target,
		routes:  inv.routes,
		cseqNum: inv.cseqNum,
		key:     inv.key,
		state:   state,
		rmtCSeq: inv.cseqNum,
	}, nil
}

// invitePeer carries the validated, owned fields extracted from the
// initial INVITE.
type invitePeer struct {
	callID  string
	fromTag string
	cseqNum uint
	key     sip.ClientTransactionKey
	from    header.NameAddr
	to      header.NameAddr
	target  *sip.URI
	routes  []header.RouteHop
}

// validInvite validates the initial INVITE and returns owned copies of the
// fields a dialog needs. The request must be an exact INVITE that opens a
// new dialog: a single well-typed From, To, Call-ID, CSeq, and Contact,
// a tagged From, an untagged To, and one usable SIP or SIPS Contact URI.
func validInvite(invite *sip.Request) (invitePeer, error) {
	var peer invitePeer
	if invite == nil {
		return peer, newInvalidMsgErr("nil request")
	}
	if invite.Method != sip.RequestMethodInvite {
		return peer, newInvalidMsgErr("non-INVITE request method %q", invite.Method)
	}
	callID, err := singleCallID(invite.Headers)
	if err != nil {
		return peer, errors.Wrap(err)
	}
	from, err := singleFrom(invite.Headers)
	if err != nil {
		return peer, errors.Wrap(err)
	}
	to, err := singleTo(invite.Headers)
	if err != nil {
		return peer, errors.Wrap(err)
	}
	fromTag, present, err := tagParam(from.Params)
	if err != nil {
		return peer, errors.Wrap(err)
	}
	if !present {
		return peer, newInvalidMsgErr("missing From tag")
	}
	_, present, err = tagParam(to.Params)
	if err != nil {
		return peer, errors.Wrap(err)
	}
	if present {
		return peer, newInvalidMsgErr("initial INVITE To carries a tag")
	}
	cseq, err := singleCSeq(invite.Headers)
	if err != nil {
		return peer, errors.Wrap(err)
	}
	if via, ok := invite.Headers.FirstVia(); !ok || via == nil {
		return peer, newInvalidMsgErr("missing usable Via hop")
	}
	key, err := sip.MakeClientTransactionKey(invite)
	if err != nil {
		return peer, newInvalidMsgErr(err)
	}
	target, err := singleContact(invite.Headers)
	if err != nil {
		return peer, errors.Wrap(err)
	}
	routes, err := recRouteSet(invite.Headers, false)
	if err != nil {
		return peer, errors.Wrap(err)
	}

	return invitePeer{
		callID:  callID,
		fromTag: fromTag,
		cseqNum: cseq.SeqNum,
		key:     key,
		from:    header.NameAddr(*from).Clone(),
		to:      header.NameAddr(*to).Clone(),
		target:  target,
		routes:  routes,
	}, nil
}

// establishing carries the validated fields extracted from the
// dialog-establishing response. The party views borrow the response's
// header storage; consumers that retain them must clone.
type establishing struct {
	callID  string
	fromTag string
	toTag   string
	cseqNum uint
	key     sip.ClientTransactionKey
	from    header.NameAddr // response From: UAC local party.
	to      header.NameAddr // response To: UAC remote party.
	target  *sip.URI        // response Contact: UAC remote target.
	routes  []header.RouteHop
	early   bool
}

// validEstablishing validates that res is a dialog-establishing response
// to the INVITE described by inv: the response is fully validated, then
// its Call-ID, origin tag, CSeq number, and transaction attempt must
// equal the INVITE's. The To tag stays free because it defines the new
// fork's identity.
func validEstablishing(inv invitePeer, res *sip.Response) (establishing, error) {
	est, err := validRes(res)
	if err != nil {
		return est, errors.Wrap(err)
	}
	if est.callID != inv.callID ||
		est.fromTag != inv.fromTag ||
		est.cseqNum != inv.cseqNum ||
		!est.key.Equal(inv.key) {
		return est, errors.PrefixWrap(ErrMismatch, "response does not match the initial INVITE")
	}
	est.routes, err = recRouteSet(res.Headers, true)
	if err != nil {
		return est, errors.Wrap(err)
	}
	return est, nil
}

// validRes validates res as a candidate for establishing or
// confirming a dialog. The status is checked first: 100 Trying, an
// untagged provisional, or any non-2xx final response reports
// [ErrNotEstablishing]; a malformed candidate reports [ErrInvalidMessage].
func validRes(res *sip.Response) (establishing, error) {
	var est establishing
	if res == nil {
		return est, newInvalidMsgErr("nil response")
	}
	switch {
	case !res.Status.IsValid():
		return est, newInvalidMsgErr("invalid status %v", res.Status)
	case res.Status.Equal(sip.ResponseStatusTrying):
		return est, errors.PrefixWrap(ErrNotEstablishing, "status %v", res.Status)
	case res.Status.IsProvisional():
		if !toTagged(res.Headers) {
			return est, errors.PrefixWrap(ErrNotEstablishing, "untagged provisional response")
		}
	case !res.Status.IsSuccessful():
		return est, errors.PrefixWrap(ErrNotEstablishing, "status %v", res.Status)
	}

	callID, err := singleCallID(res.Headers)
	if err != nil {
		return est, errors.Wrap(err)
	}
	from, err := singleFrom(res.Headers)
	if err != nil {
		return est, errors.Wrap(err)
	}
	to, err := singleTo(res.Headers)
	if err != nil {
		return est, errors.Wrap(err)
	}
	fromTag, present, err := tagParam(from.Params)
	if err != nil {
		return est, errors.Wrap(err)
	}
	if !present {
		return est, newInvalidMsgErr("missing From tag")
	}
	toTag, present, err := tagParam(to.Params)
	if err != nil {
		return est, errors.Wrap(err)
	}
	if !present {
		return est, newInvalidMsgErr("missing To tag")
	}
	cseq, err := singleCSeq(res.Headers)
	if err != nil {
		return est, errors.Wrap(err)
	}
	if via, ok := res.Headers.FirstVia(); !ok || via == nil {
		return est, newInvalidMsgErr("missing usable Via hop")
	}
	key, err := sip.MakeClientTransactionKey(res)
	if err != nil {
		return est, newInvalidMsgErr(err)
	}
	target, err := singleContact(res.Headers)
	if err != nil {
		return est, errors.Wrap(err)
	}

	return establishing{
		callID:  callID,
		fromTag: fromTag,
		toTag:   toTag,
		cseqNum: cseq.SeqNum,
		key:     key,
		from:    header.NameAddr(*from),
		to:      header.NameAddr(*to),
		target:  target,
		early:   res.Status.IsProvisional(),
	}, nil
}

// singleFrom returns the message's sole well-typed From header.
func singleFrom(hdrs sip.Headers) (*header.From, error) {
	hs := hdrs.Get("From")
	if len(hs) != 1 {
		return nil, newInvalidMsgErr("From header count %d, want 1", len(hs))
	}
	from, ok := hs[0].(*header.From)
	if !ok || from == nil {
		return nil, newInvalidMsgErr("invalid From header type %T", hs[0])
	}
	return from, nil
}

// singleTo returns the message's sole well-typed To header.
func singleTo(hdrs sip.Headers) (*header.To, error) {
	hs := hdrs.Get("To")
	if len(hs) != 1 {
		return nil, newInvalidMsgErr("To header count %d, want 1", len(hs))
	}
	to, ok := hs[0].(*header.To)
	if !ok || to == nil {
		return nil, newInvalidMsgErr("invalid To header type %T", hs[0])
	}
	return to, nil
}

// singleCallID returns the message's sole Call-ID value, which must be
// non-empty and free of whitespace and control characters.
func singleCallID(hdrs sip.Headers) (string, error) {
	hs := hdrs.Get("Call-ID")
	if len(hs) != 1 {
		return "", newInvalidMsgErr("Call-ID header count %d, want 1", len(hs))
	}
	var callID string
	switch cid := hs[0].(type) {
	case header.CallID:
		callID = string(cid)
	case *header.CallID:
		if cid != nil {
			callID = string(*cid)
		}
	default:
		return "", newInvalidMsgErr("invalid Call-ID header type %T", hs[0])
	}
	if callID == "" {
		return "", newInvalidMsgErr("empty Call-ID")
	}
	if strings.IndexFunc(callID, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return "", newInvalidMsgErr("Call-ID contains whitespace or control characters")
	}
	return callID, nil
}

// singleCSeq returns the message's sole INVITE CSeq header with a sequence
// number in the initial-exchange range 1..math.MaxInt32.
func singleCSeq(hdrs sip.Headers) (*header.CSeq, error) {
	hs := hdrs.Get("CSeq")
	if len(hs) != 1 {
		return nil, newInvalidMsgErr("CSeq header count %d, want 1", len(hs))
	}
	cseq, ok := hs[0].(*header.CSeq)
	if !ok || cseq == nil {
		return nil, newInvalidMsgErr("invalid CSeq header type %T", hs[0])
	}
	if cseq.Method != sip.RequestMethodInvite {
		return nil, newInvalidMsgErr("non-INVITE CSeq method %q", cseq.Method)
	}
	if cseq.SeqNum == 0 || cseq.SeqNum > math.MaxInt32 {
		return nil, newInvalidMsgErr("CSeq number %d out of range", cseq.SeqNum)
	}
	return cseq, nil
}

// tagParam returns a party header's tag parameter. A present tag must be a
// single non-empty token value; an absent tag is not an error.
func tagParam(params header.Values) (string, bool, error) {
	vals, present := params["tag"]
	if !present {
		return "", false, nil
	}
	if len(vals) != 1 {
		return "", true, newInvalidMsgErr("tag parameter count %d, want 1", len(vals))
	}
	tag := vals[0]
	if tag == "" {
		return "", true, newInvalidMsgErr("empty tag parameter")
	}
	if !grammar.IsToken(tag) {
		return "", true, newInvalidMsgErr("non-token tag %q", tag)
	}
	return tag, true, nil
}

// toTagged reports whether any To header entry carries a tag parameter,
// used to classify provisional responses before full field validation.
func toTagged(hdrs sip.Headers) bool {
	for _, h := range hdrs.Get("To") {
		to, ok := h.(*header.To)
		if ok && to != nil && len(to.Params["tag"]) > 0 {
			return true
		}
	}
	return false
}

// singleContact returns an owned clone of the message's sole usable SIP or
// SIPS Contact URI, counted across every typed Contact header entry.
func singleContact(hdrs sip.Headers) (*sip.URI, error) {
	var target *sip.URI
	for _, h := range hdrs.Get("Contact") {
		var addrs []header.ContactAddr
		switch contact := h.(type) {
		case header.Contact:
			addrs = contact
		case *header.Contact:
			if contact != nil {
				addrs = *contact
			}
		default:
			return nil, newInvalidMsgErr("invalid Contact header type %T", h)
		}
		if len(addrs) == 0 {
			return nil, newInvalidMsgErr("empty or wildcard Contact header")
		}
		for i := range addrs {
			u, ok := addrs[i].URI.(*sip.URI)
			if !ok || u == nil || !u.IsValid() {
				return nil, newInvalidMsgErr("unusable Contact URI")
			}
			if target != nil {
				return nil, newInvalidMsgErr("multiple Contact URIs")
			}
			target = u
		}
	}
	if target == nil {
		return nil, newInvalidMsgErr("missing Contact header")
	}
	cloned, _ := target.Clone().(*sip.URI)
	return cloned, nil
}
