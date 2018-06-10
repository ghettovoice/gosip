package transaction

import (
	"fmt"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/transport"
)

// Layer serves client and server transactions.
type Layer interface {
	log.LocalLogger
	Cancel()
	Done() <-chan struct{}
	String() string
	Send(msg sip.Message) (Tx, error)
	Transport() transport.Layer
	Messages() <-chan TxMessage
	Errors() <-chan error
}

type layer struct {
	logger       log.LocalLogger
	tpl          transport.Layer
	msgs         chan TxMessage
	errs         chan error
	terrs        chan error
	done         chan struct{}
	canceled     chan struct{}
	transactions *transactionStore
}

func NewLayer(tpl transport.Layer) Layer {
	txl := &layer{
		logger:       log.NewSafeLocalLogger(),
		tpl:          tpl,
		msgs:         make(chan TxMessage),
		errs:         make(chan error),
		terrs:        make(chan error),
		done:         make(chan struct{}),
		canceled:     make(chan struct{}),
		transactions: newTransactionStore(),
	}
	go txl.listenMessages()
	go txl.serveTransactions()
	return txl
}

func (txl *layer) String() string {
	var addr string
	if txl == nil {
		addr = "<nil>"
	} else {
		addr = fmt.Sprintf("%p", txl)
	}

	return fmt.Sprintf("TransactionLayer %s", addr)
}

func (txl *layer) Log() log.Logger {
	return txl.logger.Log()
}

func (txl *layer) SetLog(logger log.Logger) {
	txl.logger.SetLog(logger.WithFields(map[string]interface{}{
		"tx-layer": txl.String(),
	}))
}

func (txl *layer) Cancel() {
	select {
	case <-txl.canceled:
	default:
		txl.Log().Debugf("cancel %s", txl)
		close(txl.canceled)
	}
}

func (txl *layer) Done() <-chan struct{} {
	return txl.done
}

func (txl *layer) Messages() <-chan TxMessage {
	return txl.msgs
}

func (txl *layer) Errors() <-chan error {
	return txl.errs
}

func (txl *layer) Transport() transport.Layer {
	return txl.tpl
}

func (txl *layer) Send(msg sip.Message) (Tx, error) {
	txl.Log().Debugf("%s sends %s", txl, msg.Short())

	var tx Tx
	var err error
	switch msg := msg.(type) {
	case sip.Response:
		tx, err = txl.getServerTx(msg)
		if err != nil {
			return nil, err
		}
		err = tx.(ServerTx).Respond(msg)
		if err != nil {
			return nil, err
		}
		return tx, nil
	case sip.Request:
		tx, err = NewClientTx(msg, txl.tpl, txl.msgs, txl.terrs)
		tx.SetLog(txl.Log())
		if err != nil {
			return nil, err
		}
		txl.transactions.put(tx.Key(), tx)
		tx.Init()
		return tx, nil
	default:
		return nil, &sip.UnsupportedMessageError{
			fmt.Errorf("%s got unsupported message %s", txl, msg.Short()),
			msg.String(),
		}
	}
}

func (txl *layer) listenMessages() {
	wg := new(sync.WaitGroup)
	defer func() {
		txl.Log().Infof("%s stops listen messages routine", txl)
		// wait for message handlers
		wg.Wait()
		// drop all transactions
		for _, tx := range txl.transactions.all() {
			tx.Terminate()
			txl.transactions.drop(tx.Key())
		}
		// todo bloody patch, remove after refactoring
		time.Sleep(time.Second)
		close(txl.terrs)
	}()
	txl.Log().Infof("%s starts listen messages routine", txl)

	for {
		select {
		case <-txl.canceled:
			txl.Log().Warnf("%s received cancel signal", txl)
			return
		case msg, ok := <-txl.tpl.Messages():
			if !ok {
				return
			}
			// start handle goroutine
			wg.Add(1)
			go func(msg sip.Message) {
				defer wg.Done()
				txl.Log().Infof("%s received %s", txl, msg.Short())

				switch msg := msg.(type) {
				case sip.Request:
					txl.handleRequest(msg)
				case sip.Response:
					txl.handleResponse(msg)
				default:
					txl.Log().Errorf("%s received unsupported message %s", txl, msg.Short())
					// todo pass up error?
				}
			}(msg)
		}
	}
}

func (txl *layer) serveTransactions() {
	defer func() {
		txl.Log().Infof("%s stops listen messages routine", txl)
		close(txl.msgs)
		//close(txl.errs)
		close(txl.done)
	}()
	txl.Log().Infof("%s starts serve transactions routine", txl)

	for {
		select {
		case err, ok := <-txl.terrs:
			if !ok {
				return
			}
			// all errors from Tx should be wrapped to TxError
			terr, ok := err.(TxError)
			if !ok {
				continue
			}

			txl.transactions.drop(terr.Key())
			// transaction terminated or timed out
			if terr.Terminated() || terr.Timeout() {
				continue
			}

			txl.errs <- terr.InitialError()
		}
	}
}

func (txl *layer) handleRequest(req sip.Request) {
	// todo error handling!
	// try to match to existent tx
	if tx, err := txl.getServerTx(req); err == nil {
		if err := tx.Receive(req); err != nil {
			txl.Log().Error(err)
		}
		return
	}
	// or create new one
	txl.Log().Debugf("%s creates new server transaction for %s", txl, req.Short())
	tx, err := NewServerTx(req, txl.tpl, txl.msgs, txl.terrs)
	tx.SetLog(txl.Log())
	if err != nil {
		txl.Log().Error(err)
		return
	}
	// put tx to store, to match retransmitting requests later
	txl.transactions.put(tx.Key(), tx)
	tx.Init()
}

func (txl *layer) handleResponse(res sip.Response) {
	tx, err := txl.getClientTx(res)
	if err != nil {
		txl.Log().Warn(err)
		// RFC 3261 - 17.1.1.2.
		// Not matched responses should be passed directly to the UA
		txl.msgs <- &txMessage{res, nil}
		return
	}
	tx.Receive(res)
}

// RFC 17.1.3.
func (txl *layer) getClientTx(msg sip.Message) (ClientTx, error) {
	txl.Log().Debugf("%s searches client transaction for %s", txl, msg.Short())

	key, err := MakeClientTxKey(msg)
	if err != nil {
		return nil, fmt.Errorf("%s failed to match %s to client transaction: %s", txl, msg.Short(), err)
	}

	tx, ok := txl.transactions.get(key)
	if !ok {
		return nil, fmt.Errorf("%s failed to match %s to client transaction: transaction with key %s not found",
			txl, msg.Short(), key)
	}

	switch tx := tx.(type) {
	case ClientTx:
		tx.Log().Debugf("%s found %s for %s", txl, tx, msg.Short())
		return tx, nil
	default:
		return nil, fmt.Errorf("%s failed to match %s to client transaction: found %s is not a client transaction",
			txl, msg.Short(), tx)
	}
}

// RFC 17.2.3.
func (txl *layer) getServerTx(msg sip.Message) (ServerTx, error) {
	txl.Log().Debugf("%s searches server transaction for %s", txl, msg.Short())

	key, err := MakeServerTxKey(msg)
	if err != nil {
		return nil, fmt.Errorf("%s failed to match %s to server transaction: %s", txl, msg.Short(), err)
	}

	tx, ok := txl.transactions.get(key)
	if !ok {
		return nil, fmt.Errorf("%s failed to match %s to server transaction: transaction with key %s not found",
			txl, msg.Short(), key)
	}

	switch tx := tx.(type) {
	case ServerTx:
		tx.Log().Debugf("%s found %s for %s", txl, tx, msg.Short())
		return tx, nil
	default:
		return nil, fmt.Errorf("%s failed to match %s to server transaction: found %s is not server transaction",
			txl, msg.Short(), tx)
	}
}

type transactionStore struct {
	mu           *sync.RWMutex
	transactions map[TxKey]Tx
}

func newTransactionStore() *transactionStore {
	return &transactionStore{
		mu:           new(sync.RWMutex),
		transactions: make(map[TxKey]Tx),
	}
}

func (store *transactionStore) put(key TxKey, tx Tx) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.transactions[key] = tx
}

func (store *transactionStore) get(key TxKey) (Tx, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	tx, ok := store.transactions[key]
	return tx, ok
}

func (store *transactionStore) drop(key TxKey) bool {
	if _, ok := store.get(key); !ok {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.transactions, key)
	return true
}

func (store *transactionStore) all() []Tx {
	all := make([]Tx, 0)
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, tx := range store.transactions {
		all = append(all, tx)
	}

	return all
}
