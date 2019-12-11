package sip

import "github.com/ghettovoice/gosip/log"

type Transaction interface {
	log.Loggable

	Origin() Request
	String() string
	Errors() <-chan error
	Done() <-chan bool
}

type ServerTransaction interface {
	Transaction
	Respond(res Response) error
	Acks() <-chan Request
	Cancels() <-chan Request
}

type ClientTransaction interface {
	Transaction
	Responses() <-chan Response
}
