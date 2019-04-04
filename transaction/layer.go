package transaction

import (
	"context"
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
	Request(req sip.Request) (<-chan sip.Response, error)
	Respond(res sip.Response) (<-chan sip.Request, error)
	Transport() transport.Layer
	// Requests returns channel with new incoming server transactions.
	Requests() <-chan sip.Request
	// Responses returns channel with not matched responses.
	Responses() <-chan sip.Response
	Errors() <-chan error
}

type layer struct {
	logger       log.LocalLogger
	tpl          transport.Layer
	requests     chan sip.Request
	responses    chan sip.Response
	errs         chan error
	done         chan struct{}
	canceled     chan struct{}
	transactions *transactionStore
	txWg         *sync.WaitGroup
}

func NewLayer(tpl transport.Layer) Layer {
	ctx := context.Background()
	txl := &layer{
		logger:       log.NewSafeLocalLogger(),
		tpl:          tpl,
		requests:     make(chan sip.Request),
		responses:    make(chan sip.Response),
		errs:         make(chan error),
		done:         make(chan struct{}),
		canceled:     make(chan struct{}),
		transactions: newTransactionStore(),
		txWg:         new(sync.WaitGroup),
	}
	go txl.listenMessages(ctx)

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

func (txl *layer) Requests() <-chan sip.Request {
	return txl.requests
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

func (txl *layer) Request(req sip.Request) (<-chan sip.Response, error) {
	txl.Log().Debugf("%s sends %s", txl, req.Short())

	tx, err := NewClientTx(req, txl.tpl)
	tx.SetLog(txl.Log())
	if err != nil {
		return nil, err
	}

	err = tx.Init()
	if err != nil {
		return nil, err
	}

	txl.txWg.Add(1)
	go txl.serveTransaction(tx)
	txl.transactions.put(tx.Key(), tx)

	return tx.Responses(), nil
}

func (txl *layer) Respond(res sip.Response) (<-chan sip.Request, error) {
	txl.Log().Debugf("%s sends %s", txl, res.Short())

	tx, err := txl.getServerTx(res)
	if err != nil {
		return nil, err
	}

	err = tx.Respond(res)
	if err != nil {
		return nil, err
	}

	return tx.Ack(), nil
}

func (txl *layer) listenMessages(ctx context.Context) {
	defer func() {
		txl.Log().Infof("%s stops listen messages routine", txl)

		txl.txWg.Wait()
		<-time.After(time.Millisecond)

		close(txl.requests)
		close(txl.responses)
		close(txl.errs)
		close(txl.done)
	}()
	txl.Log().Infof("%s starts listen messages routine", txl)

	for {
		select {
		case <-ctx.Done():
			txl.Cancel()
		case <-txl.canceled:
			txl.Log().Warnf("%s received cancel signal", txl)
			return
		case msg, ok := <-txl.tpl.Messages():
			if !ok {
				return
			}
			// start handle goroutine
			go txl.handleMessage(msg)
		}
	}
}

func (txl *layer) serveTransaction(tx Tx) {
	defer txl.txWg.Done()
	defer func() {
		log.Debugf("%s deletes transaction %s", txl, tx)
		tx.Terminate()
		txl.transactions.drop(tx.Key())
	}()

	for {
		select {
		case <-txl.canceled:
			return
		case <-tx.Done():
			return
		case err := <-tx.Errors():
			select {
			case <-txl.canceled:
			case txl.errs <- err:
			}
		}
	}
}

func (txl *layer) handleMessage(msg sip.Message) {
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
	// try to match to existent tx: request retransmission or ACKs on non-2xx
	if tx, err := txl.getServerTx(req); err == nil {
		if err := tx.Receive(req); err != nil {
			txl.Log().Error(err)
		}

		return
	}

	// or create new one only for new requests except ACKs on 2xx
	if !req.IsAck() {
		txl.Log().Debugf("%s creates new server transaction for %s", txl, req.Short())
		tx, err := NewServerTx(req, txl.tpl)
		if err != nil {
			txl.Log().Error(err)
			return
		}

		tx.SetLog(txl.Log())
		// put tx to store, to match retransmitting requests later
		txl.transactions.put(tx.Key(), tx)
		txl.txWg.Add(1)
		go txl.serveTransaction(tx)

		if err := tx.Init(); err != nil {
			txl.Log().Error(err)
			return
		}
	}
	// pass up request
	txl.Log().Debugf("%s pass up %s", txl, req.Short())
	select {
	case <-txl.canceled:
	case txl.requests <- req:
	}
}

func (txl *layer) handleResponse(res sip.Response) {
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
	_ = tx.Receive(res)
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
