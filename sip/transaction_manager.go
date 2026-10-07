package sip

import (
	"context"
	"iter"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/ghettovoice/timeutil"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/pkg/log"
)

// TransactionManager is responsible for matching incoming messages to corresponding transactions
// and creating new transactions.
type TransactionManager struct {
	// ServerTransactionFactory is the server transaction factory.
	// If nil, a [NewServerTransaction] is used.
	ServerTransactionFactory ServerTransactionFactory
	// ClientTransactionFactory is the client transaction factory.
	// If nil, a [NewClientTransaction] is used.
	ClientTransactionFactory ClientTransactionFactory
	// StaleTransactionTimeout is the timeout for stale transactions.
	// Client INVITE transaction in proceeding, server INVITE transaction in proceeding
	// and non-INVITE transaction in trying/proceeding states after this timeout are considered stale
	// and will be terminated to prevent memory leaks.
	// If 0, 5 minutes is used. If negative, stale transactions are never terminated.
	StaleTransactionTimeout time.Duration
	// TransactionStartTimeout limits how long a registered transaction may remain unstarted.
	// If nonpositive, unstarted transactions are never terminated automatically.
	TransactionStartTimeout time.Duration
	// Logger is the logger.
	// If nil, the [log.Default] is used.
	Logger *slog.Logger

	NoopMessageInterceptor

	lcMu      sync.Mutex
	state     uint32
	closeOnce sync.Once
	closeErr  error

	clnTxs     syncutil.ShardMap[ClientTransactionKey, *clientTxRegistration]
	clnTxHdlrs types.CallbackManager[ClientTransactionHandler]

	srvTxs     syncutil.ShardMap[ServerTransactionKey, *serverTxRegistration]
	srvTxHdlrs types.CallbackManager[ServerTransactionHandler]
	mergedReqs syncutil.ShardMap[mergedRequestKey, *serverTxRegistration]
}

type clientTxRegistration struct {
	transactionRegistration[ClientTransaction]
	txKey ClientTransactionKey
}

type serverTxRegistration struct {
	transactionRegistration[ServerTransaction]
	txKey     ServerTransactionKey
	mergedKey mergedRequestKey
}

type transactionRegistration[T Transaction] struct {
	tx           T
	startWatcher *timeutil.Watchdog
	staleWatcher *timeutil.Watchdog
	drop         func()

	mu       sync.Mutex
	rejected bool
	admitted bool
	closed   bool
	lastCtx  context.Context
	unbinds  []func()
}

func (r *transactionRegistration[T]) addUnbind(fn func()) {
	r.mu.Lock()
	if !r.closed {
		r.unbinds = append(r.unbinds, fn)
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	fn()
}

func (r *transactionRegistration[T]) reject() {
	r.mu.Lock()
	r.rejected = true
	r.mu.Unlock()
}

func (r *transactionRegistration[T]) noteCtx(ctx context.Context) {
	r.mu.Lock()
	r.lastCtx = ctx
	r.mu.Unlock()
}

func (r *transactionRegistration[T]) notifyCtxOr(ctx context.Context) context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastCtx == nil {
		return ctx
	}
	return r.lastCtx
}

func (r *transactionRegistration[T]) usable() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.admitted && !r.closed
}

func (r *transactionRegistration[T]) close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	if r.startWatcher != nil {
		r.startWatcher.Close()
		r.startWatcher = nil
	}
	if r.staleWatcher != nil {
		r.staleWatcher.Close()
		r.staleWatcher = nil
	}
	unbinds := r.unbinds
	r.unbinds = nil
	r.mu.Unlock()

	for _, fn := range unbinds {
		fn()
	}
}

func (r *transactionRegistration[T]) watchStart(
	ctx context.Context,
	timeout time.Duration,
	onTimeout func(context.Context, Transaction),
) {
	if timeout <= 0 || !r.usable() {
		return
	}

	if r.tx.Started() {
		return
	}

	r.mu.Lock()
	if !r.admitted || r.closed || r.startWatcher != nil {
		r.mu.Unlock()
		return
	}

	var watcher *timeutil.Watchdog
	watcher = timeutil.NewWatchdog(timeout, func() {
		r.expireStartWatchdog(ctx, watcher, onTimeout)
	})
	r.startWatcher = watcher
	watcher.Start()
	r.mu.Unlock()

	if r.tx.Started() {
		r.stopStartWatchdog()
	}
}

func (r *transactionRegistration[T]) stopStartWatchdog() {
	r.mu.Lock()
	if r.startWatcher != nil {
		r.startWatcher.Close()
		r.startWatcher = nil
	}
	r.mu.Unlock()
}

func (r *transactionRegistration[T]) expireStartWatchdog(
	ctx context.Context,
	watcher *timeutil.Watchdog,
	onTimeout func(context.Context, Transaction),
) {
	started := r.tx.Started()

	r.mu.Lock()
	if !r.admitted || r.closed || r.startWatcher != watcher {
		r.mu.Unlock()
		return
	}
	r.startWatcher = nil
	watcher.Close()
	r.mu.Unlock()

	if !started {
		onTimeout(ctx, r.tx)
	}
}

func (r *transactionRegistration[T]) watchStale(
	ctx context.Context,
	timeout time.Duration,
	onTimeout func(context.Context, Transaction),
) {
	if timeout <= 0 || !r.usable() {
		return
	}

	if !isStaleTransaction(r.tx) {
		r.mu.Lock()
		if r.staleWatcher != nil {
			r.staleWatcher.Close()
			r.staleWatcher = nil
		}
		r.mu.Unlock()
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.admitted || r.closed || r.staleWatcher != nil {
		return
	}

	var watcher *timeutil.Watchdog
	watcher = timeutil.NewWatchdog(timeout, func() {
		r.expireStaleWatchdog(ctx, watcher, onTimeout)
	})
	r.staleWatcher = watcher
	watcher.Start()
}

func (r *transactionRegistration[T]) expireStaleWatchdog(
	ctx context.Context,
	watcher *timeutil.Watchdog,
	onTimeout func(context.Context, Transaction),
) {
	stale := isStaleTransaction(r.tx)

	r.mu.Lock()
	if !r.admitted || r.closed || r.staleWatcher != watcher {
		r.mu.Unlock()
		return
	}
	r.staleWatcher = nil
	watcher.Close()
	r.mu.Unlock()

	if !stale {
		return
	}

	onTimeout(ctx, r.tx)
}

type mergedRequestKey struct {
	fromTag,
	callID,
	method string
	seqNum uint
}

func makeMergedRequestKey(msg Message) (mergedRequestKey, error) {
	if err := msg.Validate(); err != nil {
		return mergedRequestKey{}, errors.Wrap(err)
	}

	hdrs, ok := GetMessageHeaders(msg)
	if !ok {
		return mergedRequestKey{}, errors.Wrap(newUnexpectMsgTypeErr(msg))
	}

	var k mergedRequestKey

	from, _ := hdrs.From()
	k.fromTag, _ = from.Tag()

	callID, _ := hdrs.CallID()
	k.callID = string(callID)

	cseq, _ := hdrs.CSeq()
	k.seqNum = cseq.SeqNum
	k.method = string(cseq.Method.ToUpper())

	return k, nil
}

var _ MessageInterceptor = (*TransactionManager)(nil)

const (
	txmStateRunning uint32 = iota
	txmStateClosing
	txmStateClosed
)

func (txm *TransactionManager) srvTxFactory() ServerTransactionFactory {
	if txm.ServerTransactionFactory == nil {
		return ServerTransactionFactoryFunc(NewServerTransaction)
	}
	return txm.ServerTransactionFactory
}

func (txm *TransactionManager) clnTxFactory() ClientTransactionFactory {
	if txm.ClientTransactionFactory == nil {
		return ClientTransactionFactoryFunc(NewClientTransaction)
	}
	return txm.ClientTransactionFactory
}

func (txm *TransactionManager) staleTxTimeout() time.Duration {
	if txm.StaleTransactionTimeout == 0 {
		return 5 * time.Minute
	}
	return txm.StaleTransactionTimeout
}

func (txm *TransactionManager) log() *slog.Logger {
	if txm.Logger == nil {
		return log.Default()
	}
	return txm.Logger
}

func (txm *TransactionManager) InterceptInboundRequest(
	ctx context.Context,
	next RequestReceiver,
	req *RequestEnvelope,
) (finErr error) {
	if txKey, err := MakeServerTransactionKey(req); err == nil {
		if tx, ok := txm.LoadServerTransaction(txKey); ok {
			// transaction found, pass request to it
			err := tx.RecvRequest(ctx, req)
			if err == nil {
				// request is consumed by the server transaction,
				// stop the receivers chain
				return nil
			}

			if !errors.Is(err, ErrMessageNotMatched) {
				// some unexpected error occurred
				return errors.Wrap(&RequestRejectedError{
					cause:  err,
					resSts: ResponseStatusServerInternalError,
					logLvl: slog.LevelWarn,
				})
			}

			// fallback to next receiver
		}

		defer func() {
			if finErr == nil {
				return
			}

			// The next receiver may have created a server transaction.
			// If it returns a RejectRequestError, we must interrupt the error chain
			// from propagating back to the transport and instead send the response
			// through the created transaction to ensure proper stateful handling.
			rejectErr, ok := errors.AsType[*RequestRejectedError](finErr)
			if !ok {
				return
			}

			tx, ok := txm.LoadServerTransaction(txKey)
			if !ok {
				return
			}

			sts := ResponseStatusServerInternalError
			if rejectErr.ResponseStatus().IsValid() {
				sts = rejectErr.ResponseStatus()
			}
			if respondErr := Respond(ctx, req, sts, tx, rejectErr.RespondOptions()); respondErr != nil {
				txm.log().LogAttrs(
					ctx, slog.LevelError, "failed to respond via server transaction",
					slog.Any("error", respondErr),
					slog.Any("request", req),
					slog.Any("transaction", tx),
				)
				return
			}

			finErr = nil
		}()
	}

	// new request - pass to the next receiver
	return errors.Wrap(next.RecvRequest(ctx, req))
}

func (txm *TransactionManager) InterceptInboundResponse(
	ctx context.Context,
	next ResponseReceiver,
	res *ResponseEnvelope,
) error {
	if txKey, err := MakeClientTransactionKey(res); err == nil {
		if tx, ok := txm.LoadClientTransaction(txKey); ok {
			// transaction found, pass response to it
			if err = tx.RecvResponse(ctx, res); err == nil {
				// response is consumed by the client transaction,
				// stop the receivers chain
				return nil
			}

			if !errors.Is(err, ErrMessageNotMatched) {
				// some unexpected error occurred
				return errors.Wrap(&ResponseRejectedError{err, slog.LevelWarn})
			}

			// fallback to next receiver
		}
	}

	// pass non-matched response to the next receiver
	return errors.Wrap(next.RecvResponse(ctx, res))
}

func (txm *TransactionManager) Close(ctx context.Context) error {
	txm.closeOnce.Do(func() {
		txm.lcMu.Lock()
		txm.state = txmStateClosing
		txm.lcMu.Unlock()

		txm.closeErr = txm.close(ctx)

		txm.lcMu.Lock()
		txm.state = txmStateClosed
		txm.lcMu.Unlock()

		txm.log().LogAttrs(ctx, slog.LevelDebug, "transaction manager closed")
	})
	return errors.Wrap(txm.closeErr)
}

func (txm *TransactionManager) close(ctx context.Context) error {
	var (
		wg     sync.WaitGroup
		errsMu sync.Mutex
		errs   []error
	)

	collectErr := func(err error) {
		if err == nil {
			return
		}

		errsMu.Lock()
		errs = append(errs, err)
		errsMu.Unlock()
	}

	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	run := func(fn func()) {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			fn()
		})
	}

	for _, rec := range txm.clnTxs.All() {
		rec.drop()
		run(func() {
			if err := rec.tx.Terminate(ctx, errors.Wrap(NewTransactionManagerClosedError())); err != nil {
				collectErr(errors.Errorf("terminate client transaction %q: %w", rec.txKey, err))
			}
		})
	}

	for _, rec := range txm.srvTxs.All() {
		rec.drop()
		run(func() {
			if err := rec.tx.Terminate(ctx, errors.Wrap(NewTransactionManagerClosedError())); err != nil {
				collectErr(errors.Errorf("terminate server transaction %q: %w", rec.txKey, err))
			}
		})
	}

	wg.Wait()

	return errors.JoinPrefixWrap("transaction manager close errors:", errs...)
}

func isStaleTransaction(tx Transaction) bool {
	if !tx.Started() {
		return false
	}

	switch tx.Type() {
	case TransactionTypeClientInvite, TransactionTypeServerInvite:
		return tx.State() == TransactionStateProceeding
	case TransactionTypeServerNonInvite:
		return tx.State() == TransactionStateTrying || tx.State() == TransactionStateProceeding
	default:
		return false
	}
}

func isNilTransaction(tx Transaction) bool {
	return util.IsNil(tx)
}

func (txm *TransactionManager) checkRunning() error {
	txm.lcMu.Lock()
	defer txm.lcMu.Unlock()

	if txm.state >= txmStateClosing {
		return errors.Wrap(NewTransactionManagerClosedError())
	}
	return nil
}

func (*TransactionManager) checkClnTx(tx ClientTransaction) error {
	if isNilTransaction(tx) {
		return errors.ErrorWrap("nil transaction")
	}
	if typ := tx.Type(); typ != TransactionTypeClientInvite && typ != TransactionTypeClientNonInvite {
		return errors.Wrap(NewTransactionActionNotAllowedError())
	}
	if !tx.Key().IsValid() {
		return errors.Wrap(NewInvalidMessageError("invalid transaction key"))
	}
	if tx.State() == TransactionStateTerminated {
		return errors.Wrap(NewTransactionActionNotAllowedError())
	}

	return nil
}

func (txm *TransactionManager) RegisterClientTransaction(ctx context.Context, tx ClientTransaction) error {
	if err := txm.checkClnTx(tx); err != nil {
		return errors.Wrap(err)
	}
	txKey := tx.Key()

	rec := &clientTxRegistration{txKey: txKey}
	rec.tx = tx
	rec.drop = func() { txm.dropClnTx(rec) }
	rec.addUnbind(tx.BindStateHandler(txm.clnTxStateHdlr(rec)))
	rec.addUnbind(tx.BindStartHandler(txm.clnTxStartHdlr(rec)))

	candidate := tx.State() != TransactionStateTerminated && !tx.Started()

	err := func() error {
		txm.lcMu.Lock()
		defer txm.lcMu.Unlock()
		rec.mu.Lock()
		defer rec.mu.Unlock()

		if txm.state >= txmStateClosing {
			return errors.Wrap(NewTransactionManagerClosedError())
		}
		if _, loaded := txm.clnTxs.Load(txKey); loaded {
			return errors.Wrap(NewTransactionDuplicateError())
		}
		if rec.closed || rec.rejected || !candidate {
			return errors.Wrap(NewTransactionActionNotAllowedError())
		}
		rec.admitted = true
		txm.clnTxs.Store(txKey, rec)
		return nil
	}()
	if err != nil {
		rec.close()
		return errors.Wrap(err)
	}

	if tx.State() == TransactionStateTerminated {
		rec.drop()
	} else {
		rec.watchStart(rec.notifyCtxOr(ctx), txm.TransactionStartTimeout, txm.handleTransactionTimeout)
	}

	return nil
}

func (txm *TransactionManager) dropClnTx(rec *clientTxRegistration) {
	rec.close()
	txm.clnTxs.CompareAndDelete(rec.txKey, func(actual *clientTxRegistration) bool {
		return actual == rec
	})
}

func (txm *TransactionManager) clnTxStateHdlr(rec *clientTxRegistration) TransactionStateHandlerFunc {
	return func(ctx context.Context, _, to TransactionState) {
		rec.noteCtx(ctx)
		if to == TransactionStateTerminated {
			rec.reject()
			rec.drop()
			return
		}

		rec.watchStale(ctx, txm.staleTxTimeout(), txm.handleTransactionTimeout)
	}
}

func (txm *TransactionManager) clnTxStartHdlr(rec *clientTxRegistration) TransactionStartHandlerFunc {
	return func(ctx context.Context) {
		rec.noteCtx(ctx)
		rec.reject()
		rec.stopStartWatchdog()
		rec.watchStale(ctx, txm.staleTxTimeout(), txm.handleTransactionTimeout)
	}
}

func (txm *TransactionManager) handleTransactionTimeout(ctx context.Context, tx Transaction) {
	if err := tx.Terminate(ctx, errors.Wrap(NewTransactionTimedOutError())); err != nil {
		txm.log().LogAttrs(
			ctx, slog.LevelError, "failed to terminate timed out transaction",
			slog.Any("transaction", tx),
			slog.Any("error", err),
		)
	}
}

func (txm *TransactionManager) NewClientTransaction(
	ctx context.Context,
	req *RequestEnvelope,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (ClientTransaction, error) {
	if err := txm.checkRunning(); err != nil {
		return nil, errors.Wrap(err)
	}

	tx, err := txm.clnTxFactory().NewClientTransaction(req, tp, opts...)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	if err := txm.RegisterClientTransaction(ctx, tx); err != nil {
		return nil, errors.Wrap(err)
	}

	for h := range txm.clnTxHdlrs.All() {
		h.HandleClientTransaction(ctx, tx)
	}

	txm.lcMu.Lock()
	closed := txm.state >= txmStateClosing
	txm.lcMu.Unlock()

	if closed {
		return nil, errors.Wrap(NewTransactionManagerClosedError())
	}

	return tx, nil
}

func (txm *TransactionManager) LoadClientTransaction(key ClientTransactionKey) (ClientTransaction, bool) {
	rec, ok := txm.clnTxs.Load(key)
	if !ok {
		return nil, false
	}
	return rec.tx, true
}

func (txm *TransactionManager) AllClientTransactions() iter.Seq[ClientTransaction] {
	return func(yield func(ClientTransaction) bool) {
		for _, rec := range txm.clnTxs.All() {
			if !yield(rec.tx) {
				return
			}
		}
	}
}

func (txm *TransactionManager) NewCancelClientTransaction(
	ctx context.Context,
	invTx ClientTransaction,
	opts ...ClientTransactionOptions,
) (ClientTransaction, error) {
	if err := txm.checkRunning(); err != nil {
		return nil, errors.Wrap(err)
	}

	if isNilTransaction(invTx) {
		return nil, errors.ErrorWrap("nil transaction")
	}

	if invTx.Type() != TransactionTypeClientInvite {
		return nil, errors.Wrap(NewTransactionActionNotAllowedError())
	}

	cncReq, err := NewCancelRequestEnvelope(invTx.Request())
	if err != nil {
		return nil, errors.Wrap(err)
	}

	cncKey, err := MakeClientTransactionKey(cncReq)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	if _, ok := txm.LoadClientTransaction(cncKey); ok {
		return nil, errors.Wrap(NewTransactionDuplicateError())
	}

	if !invTx.Started() {
		return nil, errors.Wrap(NewTransactionActionNotAllowedError())
	}

	if invTx.State() > TransactionStateProceeding {
		return nil, errors.Wrap(NewTransactionActionNotAllowedError())
	}

	if invTx.State() == TransactionStateProceeding {
		return errors.Wrap2(txm.NewClientTransaction(ctx, cncReq, invTx.Transport(), opts...))
	}

	firstOf := syncutil.NewFirstOf[error]()
	firstOf.AddCancel(invTx.BindStateHandler(TransactionStateHandlerFunc(
		func(_ context.Context, _, to TransactionState) {
			if to == TransactionStateProceeding {
				firstOf.Resolve(nil)
			} else {
				firstOf.Resolve(errors.Wrap(NewTransactionActionNotAllowedError()))
			}
		},
	)))

	switch invTx.State() {
	case TransactionStateCalling:
	case TransactionStateProceeding:
		firstOf.Resolve(nil)
	default:
		firstOf.Resolve(errors.Wrap(NewTransactionActionNotAllowedError()))
	}

	select {
	case <-ctx.Done():
		firstOf.Resolve(errors.Wrap(ctx.Err()))
		return nil, errors.Wrap(<-firstOf.Chan())
	case err := <-firstOf.Chan():
		if err != nil {
			return nil, errors.Wrap(err)
		}

		if invTx.State() != TransactionStateProceeding {
			return nil, errors.Wrap(NewTransactionActionNotAllowedError())
		}

		return errors.Wrap2(txm.NewClientTransaction(ctx, cncReq, invTx.Transport(), opts...))
	}
}

func (*TransactionManager) checkSrvTx(tx ServerTransaction) error {
	if isNilTransaction(tx) {
		return errors.ErrorWrap("nil transaction")
	}
	if typ := tx.Type(); typ != TransactionTypeServerInvite && typ != TransactionTypeServerNonInvite {
		return errors.Wrap(NewTransactionActionNotAllowedError())
	}
	if !tx.Key().IsValid() {
		return errors.Wrap(NewInvalidMessageError("invalid transaction key"))
	}
	if tx.State() == TransactionStateTerminated {
		return errors.Wrap(NewTransactionActionNotAllowedError())
	}

	return nil
}

func (txm *TransactionManager) RegisterServerTransaction(ctx context.Context, tx ServerTransaction) error {
	if err := txm.checkSrvTx(tx); err != nil {
		return errors.Wrap(err)
	}

	mergedKey, err := makeMergedRequestKey(tx.Request())
	if err != nil {
		return errors.Wrap(err)
	}
	txKey := tx.Key()

	rec := &serverTxRegistration{txKey: txKey, mergedKey: mergedKey}
	rec.tx = tx
	rec.drop = func() { txm.dropSrvTx(rec) }
	rec.addUnbind(tx.BindStateHandler(txm.srvTxStateHdlr(rec)))
	rec.addUnbind(tx.BindStartHandler(txm.srvTxStartHdlr(rec)))

	candidate := tx.State() != TransactionStateTerminated && !tx.Started()

	err = func() error {
		txm.lcMu.Lock()
		defer txm.lcMu.Unlock()
		rec.mu.Lock()
		defer rec.mu.Unlock()

		if txm.state >= txmStateClosing {
			return errors.Wrap(NewTransactionManagerClosedError())
		}
		if _, loaded := txm.srvTxs.Load(txKey); loaded {
			return errors.Wrap(NewTransactionDuplicateError())
		}
		if rec.closed || rec.rejected || !candidate {
			return errors.Wrap(NewTransactionActionNotAllowedError())
		}
		rec.admitted = true
		txm.srvTxs.Store(txKey, rec)
		txm.mergedReqs.Store(mergedKey, rec)
		return nil
	}()
	if err != nil {
		rec.close()
		return errors.Wrap(err)
	}

	if tx.State() == TransactionStateTerminated {
		rec.drop()
	} else {
		rec.watchStart(rec.notifyCtxOr(ctx), txm.TransactionStartTimeout, txm.handleTransactionTimeout)
	}

	return nil
}

func (txm *TransactionManager) dropSrvTx(rec *serverTxRegistration) {
	rec.close()
	if deleted := txm.srvTxs.CompareAndDelete(rec.txKey, func(actual *serverTxRegistration) bool {
		return actual == rec
	}); deleted {
		txm.mergedReqs.CompareAndDelete(rec.mergedKey, func(actual *serverTxRegistration) bool {
			return actual == rec
		})
	}
}

func (txm *TransactionManager) srvTxStateHdlr(rec *serverTxRegistration) TransactionStateHandlerFunc {
	return func(ctx context.Context, _, to TransactionState) {
		rec.noteCtx(ctx)
		if to == TransactionStateTerminated {
			rec.reject()
			rec.drop()
			return
		}

		rec.watchStale(ctx, txm.staleTxTimeout(), txm.handleTransactionTimeout)
	}
}

func (txm *TransactionManager) srvTxStartHdlr(rec *serverTxRegistration) TransactionStartHandlerFunc {
	return func(ctx context.Context) {
		rec.noteCtx(ctx)
		rec.reject()
		rec.stopStartWatchdog()
		rec.watchStale(ctx, txm.staleTxTimeout(), txm.handleTransactionTimeout)
	}
}

func (txm *TransactionManager) NewServerTransaction(
	ctx context.Context,
	req *RequestEnvelope,
	tp ServerTransport,
	opts ...ServerTransactionOptions,
) (ServerTransaction, error) {
	if err := txm.checkRunning(); err != nil {
		return nil, errors.Wrap(err)
	}

	tx, err := txm.srvTxFactory().NewServerTransaction(req, tp, opts...)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	if err := txm.RegisterServerTransaction(ctx, tx); err != nil {
		return nil, errors.Wrap(err)
	}

	for h := range txm.srvTxHdlrs.All() {
		h.HandleServerTransaction(ctx, tx)
	}

	txm.lcMu.Lock()
	closed := txm.state >= txmStateClosing
	txm.lcMu.Unlock()

	if closed {
		return nil, errors.Wrap(NewTransactionManagerClosedError())
	}

	return tx, nil
}

func (txm *TransactionManager) LoadServerTransaction(key ServerTransactionKey) (ServerTransaction, bool) {
	isAck := util.EqFold(key.Method, string(RequestMethodAck))
	if isAck {
		key.Method = string(RequestMethodInvite)
	}

	// match inbound RFC 3261/2345 request retransmits
	if rec, ok := txm.srvTxs.Load(key); ok {
		return rec.tx, true
	}

	if IsRFC3261Branch(key.Branch) || !isAck {
		return nil, false
	}

	key.ToTag = ""
	rec, ok := txm.srvTxs.Load(key)
	if !ok {
		return nil, false
	}
	return rec.tx, true
}

// LookupMergedRequest looks up a merged request by the given message.
// It returns the merged request key and a boolean indicating whether the request was found.
// RFC 3261 Section 8.2.2.2.
func (txm *TransactionManager) LookupMergedRequest(req Message) (ServerTransactionKey, bool) {
	// TODO: check that req is actually a some request implementation
	key, err := makeMergedRequestKey(req)
	if err != nil {
		return ServerTransactionKey{}, false
	}

	rec, ok := txm.mergedReqs.Load(key)
	if !ok {
		return ServerTransactionKey{}, false
	}
	return rec.txKey, true
}

func (txm *TransactionManager) AllServerTransactions() iter.Seq[ServerTransaction] {
	return func(yield func(ServerTransaction) bool) {
		for _, rec := range txm.srvTxs.All() {
			if !yield(rec.tx) {
				return
			}
		}
	}
}

type ClientTransactionHandler interface {
	HandleClientTransaction(ctx context.Context, tx ClientTransaction)
}

type ClientTransactionHandlerFunc func(ctx context.Context, tx ClientTransaction)

func (f ClientTransactionHandlerFunc) HandleClientTransaction(ctx context.Context, tx ClientTransaction) {
	f(ctx, tx)
}

func (txm *TransactionManager) BindClientTransactionHandler(handler ClientTransactionHandler) (unbind func()) {
	txm.lcMu.Lock()
	defer txm.lcMu.Unlock()

	if handler == nil || txm.state >= txmStateClosing {
		return noop
	}

	return txm.clnTxHdlrs.Add(handler)
}

type ServerTransactionHandler interface {
	HandleServerTransaction(ctx context.Context, tx ServerTransaction)
}

type ServerTransactionHandlerFunc func(ctx context.Context, tx ServerTransaction)

func (f ServerTransactionHandlerFunc) HandleServerTransaction(ctx context.Context, tx ServerTransaction) {
	f(ctx, tx)
}

func (txm *TransactionManager) BindServerTransactionHandler(handler ServerTransactionHandler) (unbind func()) {
	txm.lcMu.Lock()
	defer txm.lcMu.Unlock()

	if handler == nil || txm.state >= txmStateClosing {
		return noop
	}

	return txm.srvTxHdlrs.Add(handler)
}

type TransactionHandler interface {
	ClientTransactionHandler
	ServerTransactionHandler
}

func (txm *TransactionManager) BindTransactionHandler(handler TransactionHandler) (unbind func()) {
	txm.lcMu.Lock()
	defer txm.lcMu.Unlock()

	if handler == nil || txm.state >= txmStateClosing {
		return noop
	}

	unbinds := []func(){
		txm.clnTxHdlrs.Add(handler),
		txm.srvTxHdlrs.Add(handler),
	}

	return func() {
		for _, fn := range unbinds {
			fn()
		}
	}
}
