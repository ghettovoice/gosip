package txs

import (
	"fmt"

	"github.com/discoviking/fsm"
	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/transp"
)

type TransactionKey string

type Transaction interface {
	log.LocalLogger
	core.Awaiting
	Key() TransactionKey
	Origin() core.Request
	Receive(msg *transp.IncomingMessage) error
	Destination() string
	IsInvite() bool
	IsAck() bool
	String() string
}

type transaction struct {
	logger   log.LocalLogger
	key      TransactionKey
	fsm      *fsm.FSM
	origin   core.Request
	dest     string
	tpl      transp.Layer
	lastResp core.Response
	msgs     chan<- *IncomingMessage
	errs     chan<- error
	cancel   <-chan struct{}
	done     chan struct{}
}

func (tx *transaction) String() string {
	if tx == nil {
		return "Transaction <nil>"
	}

	return fmt.Sprintf("Transaction %p [%s]", tx, tx.Origin().Short())
}

func (tx *transaction) Log() log.Logger {
	return tx.logger.Log()
}

func (tx *transaction) SetLog(logger log.Logger) {
	tx.logger.SetLog(logger.WithFields(map[string]interface{}{
		"tx": tx.String(),
	}))
}

func (tx *transaction) Origin() core.Request {
	return tx.origin
}

func (tx *transaction) Destination() string {
	return tx.dest
}

func (tx *transaction) IsInvite() bool {
	return tx.Origin().IsInvite()
}

func (tx *transaction) IsAck() bool {
	return tx.Origin().IsAck()
}

func (tx *transaction) Done() <-chan struct{} {
	return tx.done
}

func (tx *transaction) Key() TransactionKey {
	return tx.key
}
