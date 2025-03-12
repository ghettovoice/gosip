package sip

import (
	"context"
	"net/netip"

	"github.com/looplab/fsm"

	"github.com/ghettovoice/gosip/sip/header"
)

type ClientTransaction struct {
	fsm   *fsm.FSM
	tp    Transport
	req   *Request
	raddr netip.AddrPort
}

func NewClientTransaction(tp Transport, req *Request, raddr netip.AddrPort) (*ClientTransaction, error) {
	if !req.IsValid() {
		return nil, ErrInvalidMessage
	}

	tx := &ClientTransaction{
		tp:    tp,
		req:   req,
		raddr: raddr,
	}
	tx.initFSM()
	return tx, nil
}

func (tx *ClientTransaction) initFSM() {
	// TODO setup FSM
	if tx.req.Method == RequestMethodInvite {
		tx.fsm = fsm.NewFSM("calling", fsm.Events{}, fsm.Callbacks{})
	} else {
		tx.fsm = fsm.NewFSM("trying", fsm.Events{}, fsm.Callbacks{})
	}
}

func (tx *ClientTransaction) Match(res *Response) bool {
	if !res.IsValid() {
		return false
	}

	hop1 := FirstHeaderElem[header.Via](tx.req.Headers, "Via")
	cseq1 := FirstHeader[*header.CSeq](tx.req.Headers, "CSeq")
	hop2 := FirstHeaderElem[header.Via](res.Headers, "Via")
	cseq2 := FirstHeader[*header.CSeq](res.Headers, "CSeq")
	return hop1.Params.Last("branch") == hop2.Params.Last("branch") && cseq1.Equal(cseq2)
}

func (tx *ClientTransaction) Receive(ctx context.Context, res *Response) error {
	if !tx.Match(res) {
		return ErrMismatchedTransaction
	}

	var evt string
	switch {
	case res.Status < 200:
		evt = "progress"
	case 200 <= res.Status && res.Status < 300:
		evt = "success"
	default:
		evt = "failure"
	}

	return tx.fsm.Event(ctx, evt)
}
