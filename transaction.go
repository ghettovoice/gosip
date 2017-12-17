package gosip

import (
	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
)

type TransactionLayer interface {
	log.LocalLogger
	core.Cancellable
	core.Awaiting
	Messages() <-chan *core.IncomingMessage
	Errors() <-chan error
	Send(addr string, msg core.Message) error
}

type transactionLayer struct {
	logger log.Logger
}
