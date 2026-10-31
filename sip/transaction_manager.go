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
	// StaleTransactionTimeout limits the time spent continuously in states
	// without an FSM termination timer: client/server INVITE in Proceeding
	// and server non-INVITE in Trying/Proceeding.
	// Expired transactions are forcibly terminated to prevent memory leaks;
	// no SIP response is generated. State changes within these states do not
	// restart the timeout. This is a cleanup safeguard, not an application
	// response deadline.
	// If zero, 1 hour is used. If negative, automatic stale cleanup is disabled.
	StaleTransactionTimeout time.Duration
	// TransactionStartTimeout limits how long a registered transaction may remain unstarted.
	// If 0, 1 minute is used. If negative, unstarted transactions are never terminated
	// automatically.
	TransactionStartTimeout time.Duration
	// Logger is the logger.
	// If nil, the [log.Default] is used.
	Logger *slog.Logger

	NoopMessageInterceptor

	lcMu      sync.Mutex
	state     uint32
	closeOnce sync.Once
	closeErr  error

	clnTxs     syncutil.ShardMap[ClientTransactionKey, *clientTransactRegistration]
	clnTxHdlrs types.CallbackManager[ClientTransactionHandler]

	srvTxs     syncutil.ShardMap[ServerTransactionKey, *serverTransactRegistration]
	srvTxHdlrs types.CallbackManager[ServerTransactionHandler]
	mergedReqs syncutil.ShardMap[mergedRequestKey, *serverTransactRegistration]
}

var _ MessageInterceptor = (*TransactionManager)(nil)

const (
	txmStateRunning uint32 = iota
	txmStateClosing
	txmStateClosed
)

type clientTransactRegistration struct {
	transactRegistration[ClientTransaction]
	txKey ClientTransactionKey
}

type serverTransactRegistration struct {
	transactRegistration[ServerTransaction]
	txKey     ServerTransactionKey
	mergedKey mergedRequestKey
}

type transactRegistration[T Transaction] struct {
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

func (r *transactRegistration[T]) addUnbind(fn func()) {
	r.mu.Lock()
	if !r.closed {
		r.unbinds = append(r.unbinds, fn)
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	fn()
}

func (r *transactRegistration[T]) reject() {
	r.mu.Lock()
	r.rejected = true
	r.mu.Unlock()
}

func (r *transactRegistration[T]) noteCtx(ctx context.Context) {
	r.mu.Lock()
	r.lastCtx = ctx
	r.mu.Unlock()
}

func (r *transactRegistration[T]) notifyCtxOr(ctx context.Context) context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastCtx == nil {
		return ctx
	}
	return r.lastCtx
}

func (r *transactRegistration[T]) isUsable() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.admitted && !r.closed
}

func (r *transactRegistration[T]) close() {
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

func (r *transactRegistration[T]) watchStart(
	ctx context.Context,
	timeout time.Duration,
	onTimeout func(context.Context, Transaction),
) {
	if timeout <= 0 || !r.isUsable() {
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
		r.expireStartWatcher(ctx, watcher, onTimeout)
	})
	r.startWatcher = watcher
	watcher.Start()
	r.mu.Unlock()

	if r.tx.Started() {
		r.stopStartWatcher()
	}
}

func (r *transactRegistration[T]) stopStartWatcher() {
	r.mu.Lock()
	if r.startWatcher != nil {
		r.startWatcher.Close()
		r.startWatcher = nil
	}
	r.mu.Unlock()
}

func (r *transactRegistration[T]) expireStartWatcher(
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

func (r *transactRegistration[T]) watchStale(
	ctx context.Context,
	timeout time.Duration,
	onTimeout func(context.Context, Transaction),
) {
	if timeout <= 0 || !r.isUsable() {
		return
	}

	if !isStaleTx(r.tx) {
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
		r.expireStaleWatcher(ctx, watcher, onTimeout)
	})
	r.staleWatcher = watcher
	watcher.Start()
}

func (r *transactRegistration[T]) expireStaleWatcher(
	ctx context.Context,
	watcher *timeutil.Watchdog,
	onTimeout func(context.Context, Transaction),
) {
	stale := isStaleTx(r.tx)

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
		return time.Hour
	}
	return txm.StaleTransactionTimeout
}

func (txm *TransactionManager) txStartTimeout() time.Duration {
	if txm.TransactionStartTimeout == 0 {
		return time.Minute
	}
	return txm.TransactionStartTimeout
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
			err := tx.RecvRequest(ContextWithTransaction(ctx, tx), req)
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
			if respondErr := Respond(
				ContextWithTransaction(ctx, tx),
				req,
				sts,
				tx,
				rejectErr.RespondOptions(),
			); respondErr != nil {
				txm.log().WarnContext(
					ctx, "failed to respond via server transaction",
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
			if err = tx.RecvResponse(ContextWithTransaction(ctx, tx), res); err == nil {
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

func (txm *TransactionManager) Close() error {
	txm.closeOnce.Do(func() {
		txm.lcMu.Lock()
		txm.state = txmStateClosing
		txm.lcMu.Unlock()

		txm.closeErr = txm.close()

		txm.lcMu.Lock()
		txm.state = txmStateClosed
		txm.lcMu.Unlock()

		txm.log().Debug("transaction manager closed")
	})
	return errors.Wrap(txm.closeErr)
}

func (txm *TransactionManager) close() error {
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
			ctx := ContextWithTransaction(context.Background(), rec.tx)
			if err := rec.tx.Terminate(ctx, errors.Wrap(NewTransactionManagerClosedError())); err != nil {
				collectErr(errors.Errorf("terminate client transaction %q: %w", rec.txKey, err))
			}
		})
	}

	for _, rec := range txm.srvTxs.All() {
		rec.drop()
		run(func() {
			ctx := ContextWithTransaction(context.Background(), rec.tx)
			if err := rec.tx.Terminate(ctx, errors.Wrap(NewTransactionManagerClosedError())); err != nil {
				collectErr(errors.Errorf("terminate server transaction %q: %w", rec.txKey, err))
			}
		})
	}

	wg.Wait()

	return errors.JoinPrefixWrap("transaction manager close errors:", errs...)
}

func isStaleTx(tx Transaction) bool {
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

func (txm *TransactionManager) checkRunning() error {
	txm.lcMu.Lock()
	defer txm.lcMu.Unlock()

	if txm.state >= txmStateClosing {
		return errors.Wrap(NewTransactionManagerClosedError())
	}
	return nil
}

func (*TransactionManager) checkClnTx(tx ClientTransaction) error {
	if util.IsNil(tx) {
		return errors.ErrorWrap("nil transaction")
	}
	if typ := tx.Type(); typ != TransactionTypeClientInvite && typ != TransactionTypeClientNonInvite {
		return errors.Wrap(NewTransactionActionNotAllowedError())
	}
	if !tx.Key().IsValid() {
		return errors.Wrap(NewInvalidTransactionKeyError())
	}
	if tx.State() == TransactionStateTerminated {
		return errors.Wrap(NewTransactionActionNotAllowedError())
	}

	return nil
}

// RegisterClientTransaction admits a client transaction into the manager
// runtime indexes.
//
// It covers new, restored and custom [ClientTransaction] implementations.
// The transaction is validated and published to the load indexes first, then
// handlers bound via [TransactionManager.BindClientTransactionHandler] are
// notified exactly once outside the manager and registration locks, so a
// handler may read the transaction back through
// [TransactionManager.LoadClientTransaction], register other transactions or
// close the manager. Nil, invalid, started, terminated, duplicate and
// closed-manager registrations are rejected without notification.
//
// Successful return means the transaction was admitted; a handler closing
// the manager does not turn the call into an error, even though the admitted
// transaction is already terminated.
// Registration does not start the transaction: [Transaction.Start] must be
// called separately after all dependencies are connected.
func (txm *TransactionManager) RegisterClientTransaction(ctx context.Context, tx ClientTransaction) error {
	if err := txm.checkClnTx(tx); err != nil {
		return errors.Wrap(err)
	}
	ctx = ContextWithTransaction(ctx, tx)
	txKey := tx.Key()

	rec := &clientTransactRegistration{txKey: txKey}
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

	for h := range txm.clnTxHdlrs.All() {
		h.HandleClientTransaction(ctx, tx)
	}

	if tx.State() == TransactionStateTerminated {
		rec.drop()
	} else {
		rec.watchStart(rec.notifyCtxOr(ctx), txm.txStartTimeout(), txm.handleTxTimeout)
	}

	return nil
}

func (txm *TransactionManager) dropClnTx(rec *clientTransactRegistration) {
	rec.close()
	txm.clnTxs.CompareAndDelete(rec.txKey, func(actual *clientTransactRegistration) bool {
		return actual == rec
	})
}

func (txm *TransactionManager) clnTxStateHdlr(rec *clientTransactRegistration) TransactionStateHandlerFunc {
	return func(ctx context.Context, _, to TransactionState) {
		rec.noteCtx(ctx)
		if to == TransactionStateTerminated {
			rec.reject()
			rec.drop()
			return
		}

		rec.watchStale(ctx, txm.staleTxTimeout(), txm.handleTxTimeout)
	}
}

func (txm *TransactionManager) clnTxStartHdlr(rec *clientTransactRegistration) TransactionStartHandlerFunc {
	return func(ctx context.Context) {
		rec.noteCtx(ctx)
		rec.reject()
		rec.stopStartWatcher()
		rec.watchStale(ctx, txm.staleTxTimeout(), txm.handleTxTimeout)
	}
}

func (txm *TransactionManager) handleTxTimeout(ctx context.Context, tx Transaction) {
	ctx = ContextWithTransaction(ctx, tx)
	if err := tx.Terminate(ctx, errors.Wrap(NewTransactionTimedOutError())); err != nil {
		txm.log().ErrorContext(
			ctx, "failed to terminate timed out transaction",
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

	if util.IsNil(invTx) {
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
	if util.IsNil(tx) {
		return errors.ErrorWrap("nil transaction")
	}
	if typ := tx.Type(); typ != TransactionTypeServerInvite && typ != TransactionTypeServerNonInvite {
		return errors.Wrap(NewTransactionActionNotAllowedError())
	}
	if !tx.Key().IsValid() {
		return errors.Wrap(NewInvalidTransactionKeyError())
	}
	if tx.State() == TransactionStateTerminated {
		return errors.Wrap(NewTransactionActionNotAllowedError())
	}

	return nil
}

// RegisterServerTransaction admits a server transaction into the manager
// runtime indexes.
//
// It covers new, restored and custom [ServerTransaction] implementations.
// The transaction is validated and published to the load and merged-request
// indexes first, then handlers bound via
// [TransactionManager.BindServerTransactionHandler] are notified exactly once
// outside the manager and registration locks, so a handler may read the
// transaction back through [TransactionManager.LoadServerTransaction] or
// [TransactionManager.LookupMergedRequest], register other transactions or
// close the manager. Nil, invalid, started, terminated, duplicate and
// closed-manager registrations are rejected without notification.
//
// Successful return means the transaction was admitted; a handler closing
// the manager does not turn the call into an error, even though the admitted
// transaction is already terminated.
// Registration does not start the transaction: [Transaction.Start] must be
// called separately after all dependencies are connected.
func (txm *TransactionManager) RegisterServerTransaction(ctx context.Context, tx ServerTransaction) error {
	if err := txm.checkSrvTx(tx); err != nil {
		return errors.Wrap(err)
	}
	ctx = ContextWithTransaction(ctx, tx)

	mergedKey, err := makeMergedRequestKey(tx.Request())
	if err != nil {
		return errors.Wrap(err)
	}
	txKey := tx.Key()

	rec := &serverTransactRegistration{txKey: txKey, mergedKey: mergedKey}
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

	for h := range txm.srvTxHdlrs.All() {
		h.HandleServerTransaction(ctx, tx)
	}

	if tx.State() == TransactionStateTerminated {
		rec.drop()
	} else {
		rec.watchStart(rec.notifyCtxOr(ctx), txm.txStartTimeout(), txm.handleTxTimeout)
	}

	return nil
}

func (txm *TransactionManager) dropSrvTx(rec *serverTransactRegistration) {
	rec.close()
	if deleted := txm.srvTxs.CompareAndDelete(rec.txKey, func(actual *serverTransactRegistration) bool {
		return actual == rec
	}); deleted {
		txm.mergedReqs.CompareAndDelete(rec.mergedKey, func(actual *serverTransactRegistration) bool {
			return actual == rec
		})
	}
}

func (txm *TransactionManager) srvTxStateHdlr(rec *serverTransactRegistration) TransactionStateHandlerFunc {
	return func(ctx context.Context, _, to TransactionState) {
		rec.noteCtx(ctx)
		if to == TransactionStateTerminated {
			rec.reject()
			rec.drop()
			return
		}

		rec.watchStale(ctx, txm.staleTxTimeout(), txm.handleTxTimeout)
	}
}

func (txm *TransactionManager) srvTxStartHdlr(rec *serverTransactRegistration) TransactionStartHandlerFunc {
	return func(ctx context.Context) {
		rec.noteCtx(ctx)
		rec.reject()
		rec.stopStartWatcher()
		rec.watchStale(ctx, txm.staleTxTimeout(), txm.handleTxTimeout)
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

// ClientTransactionHandler is notified when a client transaction is admitted
// by the transaction manager.
//
// It is invoked exactly once per successful
// [TransactionManager.RegisterClientTransaction], after the transaction is
// published to the manager indexes and before its start watchdog is armed.
// The notification covers new, restored and custom transaction
// implementations; rejected registrations are not notified.
// The handler runs outside the manager locks and may look the transaction
// up, register other transactions or close the manager.
// The notification does not start the transaction.
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

// ServerTransactionHandler is notified when a server transaction is admitted
// by the transaction manager.
//
// It is invoked exactly once per successful
// [TransactionManager.RegisterServerTransaction], after the transaction is
// published to the manager indexes and before its start watchdog is armed.
// The notification covers new, restored and custom transaction
// implementations; rejected registrations are not notified.
// The handler runs outside the manager locks and may look the transaction
// up, register other transactions or close the manager.
// The notification does not start the transaction.
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
