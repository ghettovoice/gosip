package sip

import (
	"context"
	"iter"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/log"
)

// Transaction manager errors.
const (
	ErrDuplicateTransaction     Error = "duplicate transaction"
	ErrTransactionManagerClosed Error = "transaction manager closed"
)

// TransactionManager is responsible for matching incoming messages to corresponding transactions
// and creating new transactions.
type TransactionManager struct {
	// ServerTransactionFactory is the server transaction factory.
	// If nil, a [NewServerTransaction] is used.
	ServerTransactionFactory ServerTransactionFactory
	// ServerTransactionStore is the server transaction store.
	// If nil, a [NewMemoryServerTransactionStore] is used.
	ServerTransactionStore ServerTransactionStore
	// ClientTransactionFactory is the client transaction factory.
	// If nil, a [NewClientTransaction] is used.
	ClientTransactionFactory ClientTransactionFactory
	// ClientTransactionStore is the client transaction store.
	// If nil, a [NewMemoryClientTransactionStore] is used.
	ClientTransactionStore ClientTransactionStore
	// StaleTransactionTimeout is the timeout for stale transactions.
	// Client INVITE transaction in proceeding, server INVITE transaction in proceeding
	// and non-INVITE transaction in trying/proceeding states after this timeout are considered stale
	// and will be terminated to prevent memory leaks.
	// If 0, 5 minutes is used. If negative, stale transactions are never terminated.
	StaleTransactionTimeout time.Duration
	// Logger is the logger.
	// If nil, the [log.Default] is used.
	Logger *slog.Logger

	NoopMessageInterceptor

	state atomic.Uint32

	clnTxHdlrs types.CallbackManager[ClientTransactionHandler]
	srvTxHdlrs types.CallbackManager[ServerTransactionHandler]

	closeOnce sync.Once
	closeErr  error

	srvTxsStoreOnce sync.Once
	srvTxsStoreVal  ServerTransactionStore
	clnTxsStoreOnce sync.Once
	clnTxsStoreVal  ClientTransactionStore
}

var _ MessageInterceptor = (*TransactionManager)(nil)

const (
	txmStateRunning uint32 = iota
	txmStateClosing
	txmStateClosed
)

func (txm *TransactionManager) srvTxsStore() ServerTransactionStore {
	if txm.ServerTransactionStore != nil {
		return txm.ServerTransactionStore
	}

	txm.srvTxsStoreOnce.Do(func() {
		txm.srvTxsStoreVal = NewMemoryServerTransactionStore()
	})
	return txm.srvTxsStoreVal
}

func (txm *TransactionManager) srvTxFactory() ServerTransactionFactory {
	if txm.ServerTransactionFactory == nil {
		return ServerTransactionFactoryFunc(NewServerTransaction)
	}
	return txm.ServerTransactionFactory
}

func (txm *TransactionManager) clnTxsStore() ClientTransactionStore {
	if txm.ClientTransactionStore != nil {
		return txm.ClientTransactionStore
	}

	txm.clnTxsStoreOnce.Do(func() {
		txm.clnTxsStoreVal = NewMemoryClientTransactionStore()
	})
	return txm.clnTxsStoreVal
}

func (txm *TransactionManager) clnTxFactory() ClientTransactionFactory {
	if txm.ClientTransactionFactory == nil {
		return ClientTransactionFactoryFunc(NewClientTransaction)
	}
	return txm.ClientTransactionFactory
}

const defStaleTxTimeout = 5 * time.Minute

func (txm *TransactionManager) staleTxTimeout() time.Duration {
	if txm.StaleTransactionTimeout == 0 {
		return defStaleTxTimeout
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
) error {
	if txm.state.Load() >= txmStateClosing {
		return errors.Wrap(ErrTransactionManagerClosed)
	}

	tx, err := txm.srvTxsStore().MatchMessage(ctx, req)
	if err == nil {
		// transaction found, pass request to it
		if err = tx.RecvRequest(ctx, req); err != nil {
			return errors.Wrap(NewRequestRejectedError(
				err,
				slog.LevelWarn,
				ResponseStatusServerInternalError,
			))
		}
		// request is consumed by the server transaction,
		// stop the receivers chain
		return nil
	}

	if !errors.Is(err, ErrTransactionNotFound) {
		lvl := slog.LevelError
		if IsMessageError(err) {
			lvl = slog.LevelWarn
		}

		txm.log().Log(ctx, lvl, "failed to match inbound request to transaction",
			slog.Any("error", err),
			slog.Any("request", req),
		)
	}

	// new request - pass to the next receiver
	if err = next.RecvRequest(ctx, req); err != nil {
		// The next receiver may have created a server transaction. If it returns a RejectRequestError,
		// we must interrupt the error chain from propagating back to the transport and instead send
		// the response through the created transaction to ensure proper stateful handling.
		if rejectErr, ok := errors.AsType[*RequestRejectedError](err); ok {
			if key, txErr := ServerTransactionKeyFromMessage(req); txErr == nil {
				if tx, txErr = txm.LoadServerTransaction(ctx, key); txErr == nil {
					sts := ResponseStatusServerInternalError
					if rejectErr.ResponseStatus().IsValid() {
						sts = rejectErr.ResponseStatus()
					}

					if txErr = Respond(ctx, req, sts, tx, rejectErr.RespondOptions()); txErr == nil {
						return nil
					}

					txm.log().LogAttrs(ctx, slog.LevelError, "failed to respond via server transaction",
						slog.Any("error", txErr),
						slog.Any("request", req),
						slog.Any("transaction", tx),
					)
				}
			}
		}
	}

	return errors.Wrap(err)
}

func (txm *TransactionManager) InterceptInboundResponse(
	ctx context.Context,
	next ResponseReceiver,
	res *ResponseEnvelope,
) error {
	if txm.state.Load() >= txmStateClosing {
		return errors.Wrap(ErrTransactionManagerClosed)
	}

	tx, err := txm.clnTxsStore().MatchMessage(ctx, res)
	if err == nil {
		if err := tx.RecvResponse(ctx, res); err != nil {
			return errors.Wrap(NewResponseRejectedError(err, slog.LevelWarn))
		}
		// response is consumed by the client transaction,
		// stop the receivers chain
		return nil
	}

	if !errors.Is(err, ErrTransactionNotFound) {
		lvl := slog.LevelError
		if IsMessageError(err) {
			lvl = slog.LevelWarn
		}

		txm.log().Log(ctx, lvl, "failed to match inbound response to transaction",
			slog.Any("error", err),
			slog.Any("response", res),
		)
	}

	// pass non-matched response to the next receiver
	return errors.Wrap(next.RecvResponse(ctx, res))
}

func (txm *TransactionManager) Close(ctx context.Context) error {
	txm.closeOnce.Do(func() {
		txm.state.Store(txmStateClosing)
		txm.closeErr = txm.close(ctx)
		txm.state.Store(txmStateClosed)

		txm.log().LogAttrs(ctx, slog.LevelDebug, "transaction manager closed")
	})
	return errors.Wrap(txm.closeErr)
}

func (txm *TransactionManager) close(ctx context.Context) error {
	var errs []error
	if txs, err := txm.clnTxsStore().LoadAll(ctx); err == nil {
		for tx := range txs {
			if err := tx.Terminate(ctx, errors.Wrap(ErrTransactionManagerClosed)); err != nil {
				errs = append(errs, errors.Errorf("terminate client transaction %q: %w", tx.Key(), err))
			}
		}
	} else {
		errs = append(errs, errors.Errorf("load client transactions: %w", err))
	}

	if txs, err := txm.srvTxsStore().LoadAll(ctx); err == nil {
		for tx := range txs {
			if err := tx.Terminate(ctx, errors.Wrap(ErrTransactionManagerClosed)); err != nil {
				errs = append(errs, errors.Errorf("terminate server transaction %q: %w", tx.Key(), err))
			}
		}
	} else {
		errs = append(errs, errors.Errorf("load server transactions: %w", err))
	}

	if len(errs) == 0 {
		return nil
	}
	return errors.JoinPrefixWrap("transaction manager close errors:", errs...)
}

func (txm *TransactionManager) NewClientTransaction(
	ctx context.Context,
	req *RequestEnvelope,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (tx ClientTransaction, err error) {
	if txm.state.Load() >= txmStateClosing {
		return nil, errors.Wrap(ErrTransactionManagerClosed)
	}

	tx, err = txm.clnTxFactory().NewClientTransaction(ctx, req, tp, opts...)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	defer func() {
		if err != nil {
			if err := tx.Terminate(ctx, errors.Wrap(err)); err != nil {
				txm.log().LogAttrs(ctx, slog.LevelError, "failed to terminate client transaction",
					slog.Any("transaction", tx),
					slog.Any("error", err),
				)
			}
		}
	}()

	if err = txm.clnTxsStore().Store(ctx, tx); err != nil {
		return nil, errors.Wrap(err)
	}

	tx.BindStateHandler(txm.clnTxStateHdlr(tx))

	for h := range txm.clnTxHdlrs.All() {
		h.HandleClientTransaction(ctx, tx)
	}

	if err = tx.Start(ctx); err != nil {
		return nil, errors.Wrap(err)
	}

	return tx, nil
}

func (txm *TransactionManager) clnTxStateHdlr(tx ClientTransaction) TransactionStateHandlerFunc {
	var staleTmr *time.Timer

	return func(ctx context.Context, _, to TransactionState) {
		if tx.Type() == TransactionTypeClientInvite && txm.staleTxTimeout() > 0 {
			if to == TransactionStateProceeding {
				staleTmr = time.AfterFunc(txm.staleTxTimeout(), func() {
					if err := tx.Terminate(ctx, errors.Wrap(ErrTransactionTimedOut)); err != nil {
						txm.log().LogAttrs(ctx, slog.LevelError, "failed to terminate client transaction",
							slog.Any("transaction", tx),
							slog.Any("error", err),
						)
					}
				})
			} else if staleTmr != nil {
				staleTmr.Stop()
			}
		}

		if to == TransactionStateTerminated {
			if err := txm.clnTxsStore().Delete(ctx, tx); err != nil && !errors.Is(err, ErrTransactionNotFound) {
				txm.log().LogAttrs(ctx, slog.LevelError, "failed to delete client transaction from store",
					slog.Any("transaction", tx),
					slog.Any("error", err),
				)
			}
		}
	}
}

func (txm *TransactionManager) LoadClientTransaction(
	ctx context.Context,
	key ClientTransactionKey,
) (ClientTransaction, error) {
	return errors.Wrap2(txm.clnTxsStore().Load(ctx, key))
}

func (txm *TransactionManager) AllClientTransactions(ctx context.Context) (iter.Seq[ClientTransaction], error) {
	return errors.Wrap2(txm.clnTxsStore().LoadAll(ctx))
}

func (txm *TransactionManager) getOrAddClnTx(
	ctx context.Context,
	invTx ClientTransaction,
	cncReq *RequestEnvelope,
	key ClientTransactionKey,
	opts ...ClientTransactionOptions,
) (ClientTransaction, error) {
	if tx, err := txm.LoadClientTransaction(ctx, key); err == nil {
		return tx, nil
	} else if !errors.Is(err, ErrTransactionNotFound) {
		return nil, errors.Wrap(err)
	}

	tx, err := txm.NewClientTransaction(ctx, cncReq, invTx.Transport(), opts...)
	if err == nil {
		return tx, nil
	}
	if !errors.Is(err, ErrDuplicateTransaction) {
		return nil, errors.Wrap(err)
	}

	return errors.Wrap2(txm.LoadClientTransaction(ctx, key))
}

func (txm *TransactionManager) CancelClientTransaction(
	ctx context.Context,
	invTx ClientTransaction,
	opts ...ClientTransactionOptions,
) (ClientTransaction, error) {
	if txm.state.Load() >= txmStateClosing {
		return nil, errors.Wrap(ErrTransactionManagerClosed)
	}

	if invTx.Type() != TransactionTypeClientInvite {
		return nil, errors.Wrap(ErrActionNotAllowed)
	}

	cncReq, err := NewCancelRequestEnvelope(invTx.Request())
	if err != nil {
		return nil, errors.Wrap(err)
	}

	key, err := ClientTransactionKeyFromMessage(cncReq)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	if tx, err := txm.LoadClientTransaction(ctx, key); err == nil {
		return tx, nil
	} else if !errors.Is(err, ErrTransactionNotFound) {
		return nil, errors.Wrap(err)
	}

	if invTx.State() > TransactionStateProceeding {
		return nil, errors.Wrap(ErrActionNotAllowed)
	}

	if invTx.State() == TransactionStateProceeding {
		return errors.Wrap2(txm.getOrAddClnTx(ctx, invTx, cncReq, key, opts...))
	}

	firstOf := syncutil.NewFirstOf[error]()
	firstOf.AddCancel(invTx.BindStateHandler(TransactionStateHandlerFunc(
		func(_ context.Context, _, to TransactionState) {
			if to == TransactionStateProceeding {
				firstOf.Resolve(nil)
			} else {
				firstOf.Resolve(errors.Wrap(ErrActionNotAllowed))
			}
		},
	)))

	switch invTx.State() {
	case TransactionStateCalling:
	case TransactionStateProceeding:
		firstOf.Resolve(nil)
	default:
		firstOf.Resolve(errors.Wrap(ErrActionNotAllowed))
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
			return nil, errors.Wrap(ErrActionNotAllowed)
		}

		return errors.Wrap2(txm.getOrAddClnTx(ctx, invTx, cncReq, key, opts...))
	}
}

func (txm *TransactionManager) NewServerTransaction(
	ctx context.Context,
	req *RequestEnvelope,
	tp ServerTransport,
	opts ...ServerTransactionOptions,
) (tx ServerTransaction, err error) {
	if txm.state.Load() >= txmStateClosing {
		return nil, errors.Wrap(ErrTransactionManagerClosed)
	}

	tx, err = txm.srvTxFactory().NewServerTransaction(ctx, req, tp, opts...)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	defer func() {
		if err != nil {
			if err := tx.Terminate(ctx, errors.Wrap(err)); err != nil {
				txm.log().LogAttrs(ctx, slog.LevelError, "failed to terminate server transaction",
					slog.Any("transaction", tx),
					slog.Any("error", err),
				)
			}
		}
	}()

	if err := txm.srvTxsStore().Store(ctx, tx); err != nil {
		return nil, errors.Wrap(err)
	}

	tx.BindStateHandler(txm.srvTxStateHdlr(tx))

	for h := range txm.srvTxHdlrs.All() {
		h.HandleServerTransaction(ctx, tx)
	}

	if err = tx.Start(ctx); err != nil {
		return nil, errors.Wrap(err)
	}

	return tx, nil
}

func (txm *TransactionManager) srvTxStateHdlr(tx ServerTransaction) TransactionStateHandlerFunc {
	var staleTmr *time.Timer

	return func(ctx context.Context, _, to TransactionState) {
		if (to == TransactionStateTrying || to == TransactionStateProceeding) && txm.staleTxTimeout() > 0 {
			staleTmr = time.AfterFunc(txm.staleTxTimeout(), func() {
				if err := tx.Terminate(ctx, errors.Wrap(ErrTransactionTimedOut)); err != nil {
					txm.log().LogAttrs(ctx, slog.LevelError, "failed to terminate server transaction",
						slog.Any("transaction", tx),
						slog.Any("error", err),
					)
				}
			})
		} else if staleTmr != nil {
			staleTmr.Stop()
		}

		if to == TransactionStateTerminated {
			if err := txm.srvTxsStore().Delete(ctx, tx); err != nil && !errors.Is(err, ErrTransactionNotFound) {
				txm.log().LogAttrs(ctx, slog.LevelError, "failed to delete server transaction from store",
					slog.Any("transaction", tx),
					slog.Any("error", err),
				)
			}
		}
	}
}

func (txm *TransactionManager) LoadServerTransaction(
	ctx context.Context,
	key ServerTransactionKey,
) (ServerTransaction, error) {
	return errors.Wrap2(txm.srvTxsStore().Load(ctx, key))
}

func (txm *TransactionManager) AllServerTransactions(ctx context.Context) (iter.Seq[ServerTransaction], error) {
	return errors.Wrap2(txm.srvTxsStore().LoadAll(ctx))
}

type ClientTransactionHandler interface {
	HandleClientTransaction(ctx context.Context, tx ClientTransaction)
}

type ClientTransactionHandlerFunc func(ctx context.Context, tx ClientTransaction)

func (f ClientTransactionHandlerFunc) HandleClientTransaction(ctx context.Context, tx ClientTransaction) {
	f(ctx, tx)
}

func (txm *TransactionManager) BindClientTransactionHandler(handler ClientTransactionHandler) (unbind func()) {
	if handler == nil || txm.state.Load() >= txmStateClosed {
		return func() {}
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
	if handler == nil || txm.state.Load() >= txmStateClosed {
		return func() {}
	}
	return txm.srvTxHdlrs.Add(handler)
}

type TransactionHandler interface {
	ClientTransactionHandler
	ServerTransactionHandler
}

func (txm *TransactionManager) BindTransactionHandler(handler TransactionHandler) (unbind func()) {
	if handler == nil || txm.state.Load() >= txmStateClosed {
		return func() {}
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
