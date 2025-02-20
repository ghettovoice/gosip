package sip

type ServerTransaction struct {
	req *Request
}

func NewServerTransaction(req *Request) *ServerTransaction {
	return &ServerTransaction{req: req}
}
