package txs

import (
	"fmt"
	"strings"
	"sync"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/transp"
)

type Layer interface {
	log.LocalLogger
	core.Cancellable
	core.Awaiting
	String() string
	Send(addr string, msg core.Message) error
}

type layer struct {
	logger       log.LocalLogger
	tpl          transp.Layer
	msgs         chan *IncomingMessage
	errs         chan error
	terrs        chan error
	done         chan struct{}
	canceled     chan struct{}
	transactions *transactionStore
}

func NewLayer(tpl transp.Layer) Layer {
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
	return nil
}

func (txl *layer) serveTransactions() {
	defer func() {
		txl.Log().Infof("%s stops listen messages routine", txl)
		// wait for transactions
		txs := txl.transactions.all()
		wg := new(sync.WaitGroup)
		wg.Add(len(txs))
		for _, tx := range txs {
			go func(tx Transaction) {
				defer wg.Done()
				<-tx.Done()
			}(tx)
		}
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
			terr, ok := err.(TransactionError)
			if !ok {
				continue
			}

			txl.transactions.drop(terr.Key())
			if terr.Terminated() { // transaction terminated
				continue
			}

			txl.errs <- terr.InitialError()
		}
	}
}

func (txl *layer) listenMessages() {
	wg := new(sync.WaitGroup)
	defer func() {
		txl.Log().Infof("%s stops listen messages routine", txl)
		wg.Wait()
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
			wg.Add(1)
			go func(incomingMsg *transp.IncomingMessage) {
				defer wg.Done()
				txl.Log().Infof("%s received %s", txl, incomingMsg)

				switch incomingMsg.Msg.(type) {
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

func (txl *layer) handleRequest(incomingReq *transp.IncomingMessage) {
	// todo error handling!
	req := incomingReq.Msg.(core.Request)
	tx, err := txl.getServerTx(req)
	if err != nil {
		txl.Log().Debugf("%s creates new server transaction for %s", txl, incomingReq)
		dest := incomingReq.RAddr
		tx = NewServerTransaction(req, dest, txl.tpl, txl.msgs, txl.terrs, txl.canceled)
		// RFC 3261 8.2.6.1
		// UASs SHOULD NOT issue a provisional response for a non-INVITE request.
		// Rather, UASs SHOULD generate a final response to a non-INVITE request as soon as possible.
		if req.IsInvite() {
			// Send a 100 Trying immediately.
			// Technically we shouldn't do this if we trust the user to do it within 200ms,
			// but I'm not sure how to handle that situation right now.
			// Explicitly don't do this for ACKs; 2xx ACKs are their own transaction but
			// don't engender a provisional response - we just pass them up to the user
			// to handle at the dialog scope.
			txl.sendPresumptiveTrying(tx)
		}

		// put tx to store, to match retransmitting requests later
		// todo check RFC for ACK
		txl.putServerTx(tx)
	}

	err = tx.Receive(incomingReq)
	if err != nil {
		txl.Log().Error(err)
	}
}

func (txl *layer) sendPresumptiveTrying(tx ServerTransaction) {
	tx.Log().Infof("%s sends '100 Trying' auto response on %s", txl, tx)
	// Pretend the user sent us a 100 to send.
	if err := tx.Trying(); err != nil {
		tx.Log().Error(err)
	}
}

func (txl *layer) handleResponse(incomingRes *transp.IncomingMessage) {
	res := incomingRes.Msg.(core.Response)
	tx, err := txl.getClientTx(res)
	if err != nil {
		txl.Log().Warn(err)
		// RFC 3261 - 17.1.1.2.
		// Not matched responses should be passed directly to the UA
		txl.msgs <- &IncomingMessage{incomingRes, nil}
		return
	}
	tx.Receive(incomingRes)
}

// RFC 17.1.3.
func (txl *layer) getClientTx(res core.Response) (ClientTransaction, error) {
	txl.Log().Debugf("%s searches client transaction by %s", txl, res.Short())

	key, err := makeClientTxKey(res)
	if err != nil {
		return nil, fmt.Errorf("%s failed to match %s to client transaction: %s", txl, res.Short(), err)
	}

	tx, ok := txl.transactions.get(key)
	if !ok {
		return nil, fmt.Errorf("%s failed to match %s to client transaction: transaction with key %s not found",
			txl, res.Short(), key)
	}

	switch tx := tx.(type) {
	case ClientTransaction:
		return tx, nil
	default:
		return nil, fmt.Errorf("%s failed to match %s to client transaction: found %s is not a client transaction",
			txl, res.Short(), tx)
	}
}

func (txl *layer) putClientTx(tx ClientTransaction) error {
	txl.Log().Debugf("%s puts %s to store", txl, tx)

	key, err := makeClientTxKey(tx.Origin())
	if err != nil {
		return fmt.Errorf("%s failed to put %s: %s", txl, tx, err)
	}

	txl.transactions.put(key, tx)

	return nil
}

func (txl *layer) dropClientTx(tx ClientTransaction) error {
	txl.Log().Debugf("%s drops %s from store", txl, tx)

	key, err := makeClientTxKey(tx.Origin())
	if err != nil {
		return fmt.Errorf("%s failed to drop %s: %s", txl, tx, err)
	}

	txl.transactions.drop(key)

	return nil
}

// RFC 17.2.3.
func (txl *layer) getServerTx(req core.Request) (ServerTransaction, error) {
	txl.Log().Debugf("%s searches server transaction by %s", txl, req.Short())

	key, err := makeServerTxKey(req)
	if err != nil {
		return nil, fmt.Errorf("%s failed to match %s to server transaction: %s", txl, req.Short(), err)
	}

	tx, ok := txl.transactions.get(key)
	if !ok {
		return nil, fmt.Errorf("%s failed to match %s to server transaction: transaction with key %s not found",
			txl, req.Short(), key)
	}

	switch tx := tx.(type) {
	case ServerTransaction:
		return tx, nil
	default:
		return nil, fmt.Errorf("%s failed to match %s to server transaction: found %s is not server transaction",
			txl, req.Short(), tx)
	}
}

func (txl *layer) putServerTx(tx ServerTransaction) error {
	txl.Log().Debugf("%s puts %s to store", txl, tx)

	key, err := makeServerTxKey(tx.Origin())
	if err != nil {
		return fmt.Errorf("%s failed to put %s: %s", txl, tx, err)
	}

	txl.transactions.put(key, tx)

	return nil
}

func (txl *layer) dropServerTx(tx ServerTransaction) error {
	txl.Log().Debugf("%s drops %s from store", txl, tx)

	key, err := makeServerTxKey(tx.Origin())
	if err != nil {
		return fmt.Errorf("%s failed to drop %s: %s", txl, tx, err)
	}

	txl.transactions.drop(key)

	return nil
}

// makeServerTxKey creates server transaction key for matching retransmitting requests - RFC 3261 17.2.3.
func makeServerTxKey(req core.Request) (TransactionKey, error) {
	var sep = "$"

	firstViaHop, ok := req.ViaHop()
	if !ok {
		return "", fmt.Errorf("'Via' header not found or empty in %s", req.Short())
	}

	cseq, ok := req.CSeq()
	if !ok {
		return "", fmt.Errorf("'CSeq' header not found in %s", req.Short())
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
		return TransactionKey(strings.Join([]string{
			branch.String(),
			firstViaHop.Host,              // branch
			fmt.Sprint(*firstViaHop.Port), // sent-by
			string(method),                // origin method
		}, sep)), nil
	}
	// RFC 2543 compliant
	from, ok := req.From()
	if !ok {
		return "", fmt.Errorf("'From' header not found in %s", req.Short())
	}
	fromTag, ok := from.Params.Get("tag")
	if !ok {
		return "", fmt.Errorf("'tag' param not found in 'From' header of %s", req.Short())
	}
	callId, ok := req.CallId()
	if !ok {
		return "", fmt.Errorf("'Call-Id' header not found in %s", req.Short())
	}

	return TransactionKey(strings.Join([]string{
		req.Recipient().String(), // request-uri
		fromTag.String(),         // from tag
		callId.String(),          // call-id
		string(method),           // cseq method
		fmt.Sprint(cseq.SeqNo),   // cseq num
		firstViaHop.String(),     // top Via
	}, sep)), nil
}

// makeClientTxKey creates client transaction key for matching responses - RFC 3261 17.1.3.
func makeClientTxKey(msg core.Message) (TransactionKey, error) {
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

	return TransactionKey(strings.Join([]string{
		branch.String(),
		string(method),
	}, sep)), nil
}

type transactionStore struct {
	mu           *sync.RWMutex
	transactions map[TransactionKey]Transaction
}

func newTransactionStore() *transactionStore {
	return &transactionStore{
		mu:           new(sync.RWMutex),
		transactions: make(map[TransactionKey]Transaction),
	}
}

func (store *transactionStore) put(key TransactionKey, tx Transaction) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.transactions[key] = tx
}

func (store *transactionStore) get(key TransactionKey) (Transaction, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	tx, ok := store.transactions[key]
	return tx, ok
}

func (store *transactionStore) drop(key TransactionKey) bool {
	if _, ok := store.get(key); !ok {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.transactions, key)
	return true
}

func (store *transactionStore) all() []Transaction {
	all := make([]Transaction, 0)
	for key := range store.transactions {
		if tx, ok := store.get(key); ok {
			all = append(all, tx)
		}
	}

	return all
}
