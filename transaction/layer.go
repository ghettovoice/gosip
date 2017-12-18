package transaction

import (
	"fmt"
	"strings"
	"sync"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/transport"
)

// Layer serves client and server transactions.
type Layer interface {
	log.LocalLogger
	core.Cancellable
	core.Awaiting
	String() string
	Send(addr string, msg core.Message) error
}

type layer struct {
	logger       log.LocalLogger
	tpl          transport.Layer
	msgs         chan *IncomingMessage
	errs         chan error
	terrs        chan error
	done         chan struct{}
	canceled     chan struct{}
	transactions *transactionStore
}

func NewLayer(tpl transport.Layer) Layer {
	txl := &layer{
		tpl:          tpl,
		msgs:         make(chan *IncomingMessage),
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

func (txl *layer) Messages() <-chan *IncomingMessage {
	return txl.msgs
}

func (txl *layer) Errors() <-chan error {
	return txl.errs
}

func (txl *layer) Send(addr string, msg core.Message) error {
	txl.Log().Debugf("%s sends %s", txl, msg.Short())

	var (
		tx  Tx
		err error
	)

	switch msg := msg.(type) {
	case core.Response:
		tx, err = txl.getServerTx(msg)
		if err != nil {
			return err
		}
		return tx.(ServerTx).Respond(msg)
	case core.Request:
		tx, err = NewClientTx(msg, addr, txl.tpl, txl.msgs, txl.terrs)
		if err != nil {
			return err
		}
		txl.transactions.put(tx.Key(), tx)
		tx.Init()
		return nil
	default:
		return &core.UnsupportedMessageError{
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
			txl.transactions.drop(tx.Key())
		}
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
			go func(incomingMsg *transport.IncomingMessage) {
				defer wg.Done()
				txl.Log().Infof("%s received %s", txl, incomingMsg)

				switch incomingMsg.Message.(type) {
				case core.Request:
					txl.handleRequest(incomingMsg)
				case core.Response:
					txl.handleResponse(incomingMsg)
				default:
					txl.Log().Errorf("%s received unsupported message %s", txl, incomingMsg)
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
		close(txl.errs)
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

func (txl *layer) handleRequest(incomingReq *transport.IncomingMessage) {
	// todo error handling!
	// try to match to existent tx
	if tx, err := txl.getServerTx(incomingReq.Message); err == nil {
		if err := tx.Receive(incomingReq); err != nil {
			txl.Log().Error(err)
		}
		return
	}
	// or create new one
	txl.Log().Debugf("%s creates new server transaction for %s", txl, incomingReq)
	dest := incomingReq.RAddr
	tx, err := NewServerTx(incomingReq.Message.(core.Request), dest, txl.tpl, txl.msgs, txl.terrs)
	if err != nil {
		txl.Log().Error(err)
		return
	}
	// put tx to store, to match retransmitting requests later
	txl.transactions.put(tx.Key(), tx)
	tx.Init()
}

func (txl *layer) handleResponse(incomingRes *transport.IncomingMessage) {
	tx, err := txl.getClientTx(incomingRes.Message)
	if err != nil {
		txl.Log().Warn(err)
		// RFC 3261 - 17.1.1.2.
		// Not matched responses should be passed directly to the UA
		txl.msgs <- &IncomingMessage{
			incomingRes.Message,
			incomingRes.Network,
			incomingRes.LAddr,
			incomingRes.RAddr,
			nil,
		}
		return
	}
	tx.Receive(incomingRes)
}

// RFC 17.1.3.
func (txl *layer) getClientTx(msg core.Message) (ClientTx, error) {
	txl.Log().Debugf("%s searches client transaction for %s", txl, msg.Short())

	key, err := makeClientTxKey(msg)
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
		return tx, nil
	default:
		return nil, fmt.Errorf("%s failed to match %s to client transaction: found %s is not a client transaction",
			txl, msg.Short(), tx)
	}
}

// RFC 17.2.3.
func (txl *layer) getServerTx(msg core.Message) (ServerTx, error) {
	txl.Log().Debugf("%s searches server transaction for %s", txl, msg.Short())

	key, err := makeServerTxKey(msg)
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
		return tx, nil
	default:
		return nil, fmt.Errorf("%s failed to match %s to server transaction: found %s is not server transaction",
			txl, msg.Short(), tx)
	}
}

// makeServerTxKey creates server commonTx key for matching retransmitting requests - RFC 3261 17.2.3.
func makeServerTxKey(msg core.Message) (TxKey, error) {
	var sep = "$"

	firstViaHop, ok := msg.ViaHop()
	if !ok {
		return "", fmt.Errorf("'Via' header not found or empty in %s", msg.Short())
	}

	cseq, ok := msg.CSeq()
	if !ok {
		return "", fmt.Errorf("'CSeq' header not found in %s", msg.Short())
	}
	method := cseq.MethodName
	if method == core.ACK {
		method = core.INVITE
	}

	var isRFC3261 bool
	branch, ok := firstViaHop.Params.Get("branch")
	if ok && branch.String() != "" &&
		strings.HasPrefix(branch.String(), core.RFC3261BranchMagicCookie) &&
		strings.TrimPrefix(branch.String(), core.RFC3261BranchMagicCookie) != "" {

		isRFC3261 = true
	} else {
		isRFC3261 = false
	}

	// RFC 3261 compliant
	if isRFC3261 {
		return TxKey(strings.Join([]string{
			branch.String(),               // branch
			firstViaHop.Host,              // sent-by Host
			fmt.Sprint(*firstViaHop.Port), // sent-by Port
			string(method),                // request Method
		}, sep)), nil
	}
	// RFC 2543 compliant
	from, ok := msg.From()
	if !ok {
		return "", fmt.Errorf("'From' header not found in %s", msg.Short())
	}
	fromTag, ok := from.Params.Get("tag")
	if !ok {
		return "", fmt.Errorf("'tag' param not found in 'From' header of %s", msg.Short())
	}
	callId, ok := msg.CallID()
	if !ok {
		return "", fmt.Errorf("'Call-ID' header not found in %s", msg.Short())
	}

	return TxKey(strings.Join([]string{
		// TODO: how to match core.Response in Send method to server tx? currently disabled
		// msg.Recipient().String(), // request-uri
		fromTag.String(),       // from tag
		callId.String(),        // Call-ID
		string(method),         // cseq method
		fmt.Sprint(cseq.SeqNo), // cseq num
		firstViaHop.String(),   // top Via
	}, sep)), nil
}

// makeClientTxKey creates client commonTx key for matching responses - RFC 3261 17.1.3.
func makeClientTxKey(msg core.Message) (TxKey, error) {
	var sep = "$"

	cseq, ok := msg.CSeq()
	if !ok {
		return "", fmt.Errorf("'CSeq' header not found in %s", msg.Short())
	}
	method := cseq.MethodName
	if method == core.ACK {
		method = core.INVITE
	}

	firstViaHop, ok := msg.ViaHop()
	if !ok {
		return "", fmt.Errorf("'Via' header not found or empty in %s", msg.Short())
	}

	branch, ok := firstViaHop.Params.Get("branch")
	if !ok || len(branch.String()) == 0 ||
		!strings.HasPrefix(branch.String(), core.RFC3261BranchMagicCookie) ||
		len(strings.TrimPrefix(branch.String(), core.RFC3261BranchMagicCookie)) == 0 {
		return "", fmt.Errorf("'branch' not found or empty in 'Via' header of %s", msg.Short())
	}

	return TxKey(strings.Join([]string{
		branch.String(),
		string(method),
	}, sep)), nil
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
	for key := range store.transactions {
		if tx, ok := store.get(key); ok {
			all = append(all, tx)
		}
	}

	return all
}
