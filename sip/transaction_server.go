package sip

import (
	"context"
)

type ServerTransaction struct {
	// fsm *fsm.FSM
	// tp  Transport
	req *Request
}

func NewServerTransaction(req *Request) (*ServerTransaction, error) {
	if !req.IsValid() {
		return nil, ErrInvalidMessage
	}

	return &ServerTransaction{
		req: req,
	}, nil
}

func (*ServerTransaction) Match(_ *Request) bool {
	// TODO implement me
	panic("not implemented")
}

func (tx *ServerTransaction) Receive(_ context.Context, req *Request) error {
	if !tx.Match(req) {
		return ErrMismatchedTransaction
	}
	// TODO implement me
	panic("not implemented")
}
