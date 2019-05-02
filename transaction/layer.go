package transaction

import (
	"fmt"
	"sync"

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
	Request(req sip.Request) (sip.ClientTransaction, error)
	Respond(res sip.Response) (sip.ServerTransaction, error)
	Transport() transport.Layer
	// Requests returns channel with new incoming server transactions.
	Requests() <-chan sip.ServerTransaction
	// ACKs on 2xx
	Acks() <-chan sip.Request
	// Responses returns channel with not matched responses.
	Responses() <-chan sip.Response
	Errors() <-chan error
}

type layer struct {
	logger       log.LocalLogger
	tpl          transport.Layer
	requests     chan sip.ServerTransaction
	acks         chan sip.Request
	responses    chan sip.Response
	errs         chan error
	done         chan struct{}
	canceled     chan struct{}
	transactions *transactionStore
	txWg         *sync.WaitGroup
	txWgLock     *sync.RWMutex
}

func NewLayer(tpl transport.Layer) Layer {
	txl := &layer{
		logger:       log.NewSafeLocalLogger(),
		tpl:          tpl,
		requests:     make(chan sip.ServerTransaction),
		acks:         make(chan sip.Request),
		responses:    make(chan sip.Response),
		errs:         make(chan error),
		done:         make(chan struct{}),
		canceled:     make(chan struct{}),
		transactions: newTransactionStore(),
		txWg:         new(sync.WaitGroup),
		txWgLock:     new(sync.RWMutex),
	}
	go txl.listenMessages()

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

func (txl *layer) Requests() <-chan sip.ServerTransaction {
	return txl.requests
}

func (txl *layer) Acks() <-chan sip.Request {
	return txl.acks
}

func (txl *layer) Responses() <-chan sip.Response {
	return txl.responses
}

func (txl *layer) Errors() <-chan error {
	return txl.errs
}

func (txl *layer) Transport() transport.Layer {
	return txl.tpl
}

func (txl *layer) Request(req sip.Request) (sip.ClientTransaction, error) {
	select {
	case <-txl.canceled:
		return nil, fmt.Errorf("%s is canceled", txl)
	default:
	}

	txl.Log().Debugf("%s sends %s", txl, req.Short())

	if req.IsAck() {
		return nil, fmt.Errorf("ack request must be sent directly through transport")
	}

	tx, err := NewClientTx(req, txl.tpl)
	if err != nil {
		return nil, err
	}

	txl.Log().Debugf("%s creates new %s", txl, tx)

	txl.transactions.put(tx.Key(), tx)

	tx.SetLog(txl.Log())

	err = tx.Init()
	if err != nil {
		return nil, err
	}

	txl.txWgLock.Lock()
	txl.txWg.Add(1)
	txl.txWgLock.Unlock()
	go txl.serveTransaction(tx)

	return tx, nil
}

func (txl *layer) Respond(res sip.Response) (sip.ServerTransaction, error) {
	select {
	case <-txl.canceled:
		return nil, fmt.Errorf("%s is canceled", txl)
	default:
	}

	txl.Log().Debugf("%s sends %s", txl, res.Short())

	tx, err := txl.getServerTx(res)
	if err != nil {
		return nil, err
	}

	err = tx.Respond(res)
	if err != nil {
		return nil, err
	}

	return tx, nil
}

func (txl *layer) listenMessages() {
	defer func() {
		txl.Log().Infof("%s stops listen messages routine", txl)

		txl.txWgLock.RLock()
		txl.txWg.Wait()
		txl.txWgLock.RUnlock()

		close(txl.requests)
		close(txl.responses)
		close(txl.errs)
		close(txl.done)
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

			go txl.handleMessage(msg)
		}
	}
}

func (txl *layer) serveTransaction(tx Tx) {
	defer func() {
		log.Debugf("%s deletes transaction %s", txl, tx)

		txl.transactions.drop(tx.Key())
		txl.txWg.Done()
	}()

	for {
		select {
		case <-txl.canceled:
			tx.Terminate()
			return
		case <-tx.Done():
			return
		}
	}
}

func (txl *layer) handleMessage(msg sip.Message) {
	select {
	case <-txl.canceled:
		return
	default:
	}

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
}

func (txl *layer) handleRequest(req sip.Request) {
	select {
	case <-txl.canceled:
		return
	default:
	}

	// try to match to existent tx: request retransmission, or ACKs on non-2xx, or CANCEL
	tx, err := txl.getServerTx(req)
	if err == nil {
		if err := tx.Receive(req); err != nil {
			txl.Log().Error(err)
		}

		return
	}
	// ACK on 2xx
	if req.IsAck() {
		select {
		case <-txl.canceled:
		case txl.acks <- req:
		}
		return
	}

	tx, err = NewServerTx(req, txl.tpl)
	if err != nil {
		txl.Log().Error(err)
		return
	}

	txl.Log().Debugf("%s creates new %s", txl, tx)
	// put tx to store, to match retransmitting requests later
	txl.transactions.put(tx.Key(), tx)

	if err := tx.Init(); err != nil {
		txl.Log().Error(err)
		return
	}

	tx.SetLog(txl.Log())

	txl.txWgLock.Lock()
	txl.txWg.Add(1)
	txl.txWgLock.Unlock()
	go txl.serveTransaction(tx)

	// pass up request
	txl.Log().Debugf("%s pass up %s", txl, req.Short())

	select {
	case <-txl.canceled:
	case txl.requests <- tx:
	}
}

func (txl *layer) handleResponse(res sip.Response) {
	select {
	case <-txl.canceled:
		return
	default:
	}

	tx, err := txl.getClientTx(res)
	if err != nil {
		txl.Log().Warn(err)
		// RFC 3261 - 17.1.1.2.
		// Not matched responses should be passed directly to the UA
		select {
		case <-txl.canceled:
		case txl.responses <- res:
		}
		return
	}

	if err := tx.Receive(res); err != nil {
		txl.Log().Error(err)
		return
	}
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
