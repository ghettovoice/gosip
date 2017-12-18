package transaction

import (
	"fmt"

	"github.com/discoviking/fsm"
	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/transport"
)

type TxKey string

func (key TxKey) String() string {
	return string(key)
}

// Tx is an common SIP transaction
type Tx interface {
	log.LocalLogger
	Init()
	Key() TxKey
	Origin() core.Request
	// Receive receives message from transport layer.
	Receive(msg *transport.IncomingMessage) error
	Destination() string
	String() string
}

type commonTx struct {
	logger   log.LocalLogger
	key      TxKey
	fsm      *fsm.FSM
	origin   core.Request
	dest     string
	tpl      transport.Layer
	lastResp core.Response
	msgs     chan<- *IncomingMessage
	errs     chan<- error
	lastErr  error
}

func (tx *commonTx) String() string {
	if tx == nil {
		return "Tx <nil>"
	}

	return fmt.Sprintf("Tx %p [%s]", tx, tx.Origin().Short())
}

func (tx *commonTx) Log() log.Logger {
	return tx.logger.Log()
}

func (tx *commonTx) SetLog(logger log.Logger) {
	tx.logger.SetLog(logger.WithFields(map[string]interface{}{
		"tx": tx.String(),
	}))
}

func (tx *commonTx) Origin() core.Request {
	return tx.origin
}

func (tx *commonTx) Destination() string {
	return tx.dest
}

func (tx *commonTx) Key() TxKey {
	return tx.key
}
