package sip

type ClientTransaction struct {
	req *Request
}

func NewClientTransaction(req *Request) *ClientTransaction {
	return &ClientTransaction{req: req}
}
