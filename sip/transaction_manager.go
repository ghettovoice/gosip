package sip

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/log"
)

// TransactionManagerOptions are the options for a [TransactionManager].
type TransactionManagerOptions struct {
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
}

func (o *TransactionManagerOptions) srvTxFactory() ServerTransactionFactory {
	if o == nil || o.ServerTransactionFactory == nil {
		return ServerTransactionFactoryFunc(NewServerTransaction)
	}
	return o.ServerTransactionFactory
}

func (o *TransactionManagerOptions) srvTxStore() ServerTransactionStore {
	if o == nil || o.ServerTransactionStore == nil {
		return NewMemoryServerTransactionStore()
	}
	return o.ServerTransactionStore
}

func (o *TransactionManagerOptions) clnTxFactory() ClientTransactionFactory {
	if o == nil || o.ClientTransactionFactory == nil {
		return ClientTransactionFactoryFunc(NewClientTransaction)
	}
	return o.ClientTransactionFactory
}

func (o *TransactionManagerOptions) clnTxStore() ClientTransactionStore {
	if o == nil || o.ClientTransactionStore == nil {
		return NewMemoryClientTransactionStore()
	}
	return o.ClientTransactionStore
}

func (o *TransactionManagerOptions) staleTxTimeout() time.Duration {
	if o == nil || o.StaleTransactionTimeout == 0 {
		return 5 * time.Minute
	}
	return o.StaleTransactionTimeout
}

func (o *TransactionManagerOptions) log() *slog.Logger {
	if o == nil || o.Logger == nil {
		return log.Default()
	}
	return o.Logger
}

// TransactionManager is responsible for matching incoming messages to corresponding transactions
// and creating new transactions.
type TransactionManager struct {
	noopMessageInterceptor
	srvTxsStore    ServerTransactionStore
	srvTxFactory   ServerTransactionFactory
	clnTxsStore    ClientTransactionStore
	clnTxFactory   ClientTransactionFactory
	staleTxTimeout time.Duration
	log            *slog.Logger

	onNewClnTx types.CallbackManager[ClientTransactionHandler]
	onNewSrvTx types.CallbackManager[ServerTransactionHandler]

	closing   atomic.Bool
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

// NewTransactionManager creates a new [TransactionManager].
// Options are optional, if nil, default values are used (see [TransactionManagerOptions]).
func NewTransactionManager(opts *TransactionManagerOptions) *TransactionManager {
	return &TransactionManager{
		srvTxsStore:    opts.srvTxStore(),
		srvTxFactory:   opts.srvTxFactory(),
		clnTxsStore:    opts.clnTxStore(),
		clnTxFactory:   opts.clnTxFactory(),
		staleTxTimeout: opts.staleTxTimeout(),
		log:            opts.log(),
	}
}

// InboundRequestInterceptor returns an interceptor for inbound requests.
func (txm *TransactionManager) InboundRequestInterceptor() InboundRequestInterceptor {
	return InboundRequestInterceptorFunc(txm.interceptInboundRequest)
}

func (txm *TransactionManager) interceptInboundRequest(
	ctx context.Context,
	req *InboundRequestEnvelope,
	next RequestReceiver,
) error {
	tx, err := txm.srvTxsStore.LookupMatched(ctx, req)
	if err != nil {
		if errors.Is(err, ErrInvalidArgument) {
			return errtrace.Wrap(NewRejectRequestError(
				err,
				ResponseStatusBadRequest,
				slog.LevelDebug,
			))
		}
		if !errors.Is(err, ErrTransactionNotFound) {
			return errtrace.Wrap(NewRejectRequestError(
				err,
				ResponseStatusServerInternalError,
				slog.LevelWarn,
			))
		}

		if txm.closing.Load() {
			return errtrace.Wrap(NewRejectRequestError(
				ErrTransactionManagerClosed,
				ResponseStatusServiceUnavailable,
				slog.LevelDebug,
			))
		}

		return errtrace.Wrap(next.RecvRequest(ctx, req))
	}

	if err := tx.RecvRequest(ctx, req); err != nil {
		if errors.Is(err, ErrMessageNotMatched) {
			return errtrace.Wrap(NewRejectRequestError(
				err,
				ResponseStatusCallTransactionDoesNotExist,
				slog.LevelDebug,
			))
		}
		return errtrace.Wrap(NewRejectRequestError(
			err,
			ResponseStatusServerInternalError,
			slog.LevelWarn,
		))
	}
	return nil
}

// InboundResponseInterceptor returns an interceptor for inbound responses.
func (txm *TransactionManager) InboundResponseInterceptor() InboundResponseInterceptor {
	return InboundResponseInterceptorFunc(txm.interceptInboundResponse)
}

func (txm *TransactionManager) interceptInboundResponse(
	ctx context.Context,
	res *InboundResponseEnvelope,
	next ResponseReceiver,
) error {
	tx, err := txm.clnTxsStore.LookupMatched(ctx, res)
	if err != nil {
		if errors.Is(err, ErrInvalidArgument) {
			return errtrace.Wrap(NewRejectResponseError(err, slog.LevelDebug))
		}
		if !errors.Is(err, ErrTransactionNotFound) {
			return errtrace.Wrap(NewRejectResponseError(err, slog.LevelWarn))
		}

		if txm.closing.Load() {
			return errtrace.Wrap(NewRejectResponseError(ErrTransactionManagerClosed, slog.LevelDebug))
		}

		return errtrace.Wrap(next.RecvResponse(ctx, res))
	}

	if err := tx.RecvResponse(ctx, res); err != nil {
		if errors.Is(err, ErrMessageNotMatched) {
			return errtrace.Wrap(NewRejectResponseError(err, slog.LevelDebug))
		}
		return errtrace.Wrap(NewRejectResponseError(err, slog.LevelWarn))
	}
	return nil
}

func (txm *TransactionManager) Close(ctx context.Context) error {
	txm.closeOnce.Do(func() {
		txm.closing.Store(true)
		txm.closeErr = txm.close(ctx)
	})
	return errtrace.Wrap(txm.closeErr)
}

func (txm *TransactionManager) close(ctx context.Context) error {
	if txm.closed.Load() {
		return nil
	}

	var errs []error
	if txs, err := txm.clnTxsStore.All(ctx); err == nil {
		for tx := range txs {
			if err := tx.Terminate(ctx); err != nil {
				errs = append(errs, fmt.Errorf("terminate client transaction %q: %w", tx.Key(), err))
			}
		}
	} else {
		errs = append(errs, fmt.Errorf("load client transactions: %w", err))
	}

	if txs, err := txm.srvTxsStore.All(ctx); err == nil {
		for tx := range txs {
			if err := tx.Terminate(ctx); err != nil {
				errs = append(errs, fmt.Errorf("terminate server transaction %q: %w", tx.Key(), err))
			}
		}
	} else {
		errs = append(errs, fmt.Errorf("load server transactions: %w", err))
	}

	txm.closed.Store(true)

	if len(errs) == 0 {
		return nil
	}
	return errtrace.Wrap(errorutil.JoinPrefix("failed to close transaction manager:", errs...))
}

func (txm *TransactionManager) NewClientTransaction(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	tp ClientTransport,
	opts *ClientTransactionOptions,
) (ClientTransaction, error) {
	if txm.closing.Load() {
		return nil, errtrace.Wrap(ErrTransactionManagerClosed)
	}

	tx, err := txm.clnTxFactory.NewClientTransaction(ctx, req, tp, opts)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	if err = txm.clnTxsStore.Store(ctx, tx); err != nil {
		tx.Terminate(ctx) //nolint:errcheck
		return nil, errtrace.Wrap(err)
	}
	tx.OnStateChanged(txm.clnTxStateHdlr(tx))
	for fn := range txm.onNewClnTx.All() {
		fn(ctx, tx)
	}
	return tx, nil
}

func (txm *TransactionManager) clnTxStateHdlr(tx ClientTransaction) TransactionStateHandler {
	var staleTmr *time.Timer
	return func(ctx context.Context, _, to TransactionState) {
		if tx.Type() == TransactionTypeClientInvite && txm.staleTxTimeout > 0 {
			if to == TransactionStateProceeding {
				staleTmr = time.AfterFunc(txm.staleTxTimeout, func() {
					tx.Terminate(ctx) //nolint:errcheck
				})
			} else if staleTmr != nil {
				staleTmr.Stop()
			}
		}

		if to == TransactionStateTerminated {
			if err := txm.clnTxsStore.Delete(ctx, tx); err != nil && !errors.Is(err, ErrTransactionNotFound) {
				txm.log.LogAttrs(ctx, slog.LevelError,
					"failed to delete client transaction from store",
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
	return errtrace.Wrap2(txm.clnTxsStore.Load(ctx, key))
}

// OnNewClientTransaction binds a callback to be called when a client transaction is created.
// The callback can be unbound by calling the returned unbind function.
func (txm *TransactionManager) OnNewClientTransaction(fn ClientTransactionHandler) (unbind func()) {
	return txm.onNewClnTx.Add(fn)
}

func (txm *TransactionManager) NewServerTransaction(
	ctx context.Context,
	req *InboundRequestEnvelope,
	tp ServerTransport,
	opts *ServerTransactionOptions,
) (ServerTransaction, error) {
	if txm.closing.Load() {
		return nil, errtrace.Wrap(ErrTransactionManagerClosed)
	}

	tx, err := txm.srvTxFactory.NewServerTransaction(ctx, req, tp, opts)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	if err = txm.srvTxsStore.Store(ctx, tx); err != nil {
		tx.Terminate(ctx) //nolint:errcheck
		return nil, errtrace.Wrap(err)
	}
	tx.OnStateChanged(txm.srvTxStateHdlr(tx))
	for fn := range txm.onNewSrvTx.All() {
		fn(ctx, tx)
	}
	return tx, nil
}

func (txm *TransactionManager) srvTxStateHdlr(tx ServerTransaction) TransactionStateHandler {
	var staleTmr *time.Timer
	return func(ctx context.Context, _, to TransactionState) {
		if (to == TransactionStateTrying || to == TransactionStateProceeding) && txm.staleTxTimeout > 0 {
			staleTmr = time.AfterFunc(txm.staleTxTimeout, func() {
				tx.Terminate(ctx) //nolint:errcheck
			})
		} else if staleTmr != nil {
			staleTmr.Stop()
		}

		if to == TransactionStateTerminated {
			if err := txm.srvTxsStore.Delete(ctx, tx); err != nil && !errors.Is(err, ErrTransactionNotFound) {
				txm.log.LogAttrs(ctx, slog.LevelError,
					"failed to delete server transaction from store",
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
	return errtrace.Wrap2(txm.srvTxsStore.Load(ctx, key))
}

// OnNewServerTransaction binds a callback to be called when a server transaction is created.
// The callback can be unbound by calling the returned unbind function.
func (txm *TransactionManager) OnNewServerTransaction(fn ServerTransactionHandler) (unbind func()) {
	return txm.onNewSrvTx.Add(fn)
}
