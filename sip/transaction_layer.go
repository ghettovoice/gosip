package sip

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/log"
)

const ErrTransactionLayerClosed Error = "transaction layer closed"

// TransactionLayerOptions are the options for a [TransactionLayer].
type TransactionLayerOptions struct {
	// ServerTransactionFactory is the server transaction factory.
	// If nil, a [DefaultServerTransactionFactory] is used.
	ServerTransactionFactory ServerTransactionFactory
	// ServerTransactionStore is the server transaction store.
	// If nil, a [NewMemoryServerTransactionStore] is used.
	ServerTransactionStore ServerTransactionStore
	// ClientTransactionFactory is the client transaction factory.
	// If nil, a [DefaultClientTransactionFactory] is used.
	ClientTransactionFactory ClientTransactionFactory
	// ClientTransactionStore is the client transaction store.
	// If nil, a [NewMemoryClientTransactionStore] is used.
	ClientTransactionStore ClientTransactionStore
	// Log is the logger.
	// If nil, the [log.Default] is used.
	Log *slog.Logger
}

func (o *TransactionLayerOptions) srvTxFctr() ServerTransactionFactory {
	if o == nil || o.ServerTransactionFactory == nil {
		return DefaultServerTransactionFactory()
	}
	return o.ServerTransactionFactory
}

func (o *TransactionLayerOptions) srvTxStore() ServerTransactionStore {
	if o == nil || o.ServerTransactionStore == nil {
		return NewMemoryServerTransactionStore()
	}
	return o.ServerTransactionStore
}

func (o *TransactionLayerOptions) clnTxFctr() ClientTransactionFactory {
	if o == nil || o.ClientTransactionFactory == nil {
		return DefaultClientTransactionFactory()
	}
	return o.ClientTransactionFactory
}

func (o *TransactionLayerOptions) clnTxStore() ClientTransactionStore {
	if o == nil || o.ClientTransactionStore == nil {
		return NewMemoryClientTransactionStore()
	}
	return o.ClientTransactionStore
}

func (o *TransactionLayerOptions) log() *slog.Logger {
	if o == nil || o.Log == nil {
		return log.Default()
	}
	return o.Log
}

// TransactionLayer is responsible for matching incoming messages to corresponding transactions.
//
// Transaction layer catches all inbound messages from the transport and works as a wrapper around it.
// The UA or proxy core should subscribe to the transaction layer events to receive inbound requests.
// Inbound messages that match the existing transactions are passed to the transaction for processing.
// Non-matched inbound requests are passed to the core for processing,
// non-matched inbound responses are silently discarded.
type TransactionLayer struct {
	tp Transport
	cancOnReq,
	cancOnRes func()
	srvTxsStore ServerTransactionStore
	srvTxFctr   ServerTransactionFactory
	clnTxsStore ClientTransactionStore
	clnTxFctr   ClientTransactionFactory
	log         *slog.Logger

	closing   atomic.Bool
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error

	onReq types.CallbackManager[RequestHandler]
}

// NewTransactionLayer creates a new [TransactionLayer].
// Transport is required argument and expected to be non-nil.
// Options are optional, if nil, default values are used (see [TransactionLayerOptions]).
func NewTransactionLayer(tp Transport, opts *TransactionLayerOptions) (*TransactionLayer, error) {
	if tp == nil {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid transport"))
	}

	txl := &TransactionLayer{
		tp:          tp,
		srvTxsStore: opts.srvTxStore(),
		srvTxFctr:   opts.srvTxFctr(),
		clnTxsStore: opts.clnTxStore(),
		clnTxFctr:   opts.clnTxFctr(),
		log:         opts.log(),
	}
	txl.cancOnReq = tp.OnRequest(txl.recvReq)
	txl.cancOnRes = tp.OnResponse(txl.recvRes)
	return txl, nil
}

func (txl *TransactionLayer) recvReq(ctx context.Context, req *InboundRequest) {
	tp, ok := ServerTransportFromContext(ctx)
	if !ok {
		tp = txl.tp
	}

	var txKey ServerTransactionKey
	if err := txKey.FillFromMessage(req); err != nil {
		txl.log.LogAttrs(ctx, slog.LevelWarn,
			"discarding inbound request due to transaction key error",
			slog.Any("request", req),
			slog.Any("error", err),
		)
		respondStateless(ctx, tp, req, ResponseStatusBadRequest)
		return
	}

	tx, err := txl.srvTxsStore.Load(ctx, txKey)
	if err != nil {
		if errors.Is(err, ErrTransactionNotFound) {
			if txl.closing.Load() {
				respondStateless(ctx, tp, req, ResponseStatusServiceUnavailable)
				return
			}
			// transaction was not found, pass request to the core
			// ctx = context.WithValue(ctx, transactLayerCtxKey, txl)
			var handled bool
			txl.onReq.Range(func(fn RequestHandler) {
				handled = true
				fn(ctx, req)
			})
			if !handled {
				txl.log.LogAttrs(ctx, slog.LevelWarn,
					"discarding inbound request due to missing transaction layer request handlers",
					slog.Any("request", req),
				)
				respondStateless(ctx, tp, req, ResponseStatusServiceUnavailable)
			}
			return
		}

		txl.log.LogAttrs(ctx, slog.LevelWarn,
			"discarding inbound request due to transaction load error",
			slog.Any("request", req),
			slog.Any("error", err),
		)
		respondStateless(ctx, txl.tp, req, ResponseStatusServerInternalError)
		return
	}

	if err := tx.RecvRequest(ctx, req); err != nil {
		if errors.Is(err, ErrTransactionNotMatched) {
			txl.log.LogAttrs(ctx, slog.LevelDebug,
				"discarding inbound request due to transaction mismatch",
				slog.Any("request", req),
				slog.Any("transaction", tx),
				slog.Any("error", err),
			)
			if txl.closing.Load() {
				respondStateless(ctx, tp, req, ResponseStatusServiceUnavailable)
				return
			}
			respondStateless(ctx, txl.tp, req, types.ResponseStatusCallTransactionDoesNotExist)
			return
		}

		txl.log.LogAttrs(ctx, slog.LevelWarn,
			"discarding inbound request due to transaction receive error",
			slog.Any("request", req),
			slog.Any("transaction", tx),
			slog.Any("error", err),
		)
		respondStateless(ctx, tp, req, ResponseStatusServerInternalError)
		return
	}
}

func (txl *TransactionLayer) recvRes(ctx context.Context, res *InboundResponse) {
	var txKey ClientTransactionKey
	if err := txKey.FillFromMessage(res); err != nil {
		txl.log.LogAttrs(ctx, slog.LevelWarn,
			"silently discard inbound response due to transaction key error",
			slog.Any("response", res),
			slog.Any("error", err),
		)
		return
	}

	tx, err := txl.clnTxsStore.Load(ctx, txKey)
	if err != nil {
		if errors.Is(err, ErrTransactionNotFound) {
			txl.log.LogAttrs(ctx, slog.LevelDebug,
				"silently discard inbound response due to missing corresponding transaction",
				slog.Any("response", res),
				slog.Any("error", err),
			)
		} else {
			txl.log.LogAttrs(ctx, slog.LevelWarn,
				"silently discard inbound response due to transaction load error",
				slog.Any("response", res),
				slog.Any("error", err),
			)
		}
		return
	}

	if err := tx.RecvResponse(ctx, res); err != nil {
		txl.log.LogAttrs(ctx, slog.LevelWarn,
			"silently discard inbound response due to transaction receive error",
			slog.Any("response", res),
			slog.Any("error", err),
		)
	}
}

func (txl *TransactionLayer) Close(ctx context.Context) error {
	txl.closing.Store(true)
	txl.closeOnce.Do(func() {
		txl.closeErr = txl.close(ctx)
	})
	return errtrace.Wrap(txl.closeErr)
}

func (txl *TransactionLayer) close(ctx context.Context) error {
	if txl.closed.Load() {
		return nil
	}

	var errs []error
	if txs, err := txl.clnTxsStore.All(ctx); err == nil {
		for key, tx := range txs {
			if err := tx.Terminate(ctx); err != nil {
				errs = append(errs, fmt.Errorf("terminate client transaction %q: %w", key, err))
			}
		}
	} else {
		errs = append(errs, fmt.Errorf("load client transactions: %w", err))
	}

	if txs, err := txl.srvTxsStore.All(ctx); err == nil {
		for key, tx := range txs {
			if err := tx.Terminate(ctx); err != nil {
				errs = append(errs, fmt.Errorf("terminate server transaction %q: %w", key, err))
			}
		}
	} else {
		errs = append(errs, fmt.Errorf("load server transactions: %w", err))
	}

	if txl.cancOnReq != nil {
		txl.cancOnReq()
	}
	if txl.cancOnRes != nil {
		txl.cancOnRes()
	}

	txl.closed.Store(true)

	if len(errs) == 0 {
		return nil
	}
	return errtrace.Wrap(errorutil.JoinPrefix("failed to close transaction layer:", errs...))
}

// OnRequest registers a callback to be called when a request not matched to any transaction is received.
func (txl *TransactionLayer) OnRequest(fn RequestHandler) (cancel func()) {
	return txl.onReq.Add(fn)
}

// func (txl *TransactionLayer) TrackClientTransaction(ctx context.Context, tx ClientTransaction) error {
// 	key, ok := GetClientTransactionKey(tx)
// 	if !ok {
// 		return errtrace.Wrap(NewInvalidArgumentError("invalid transaction"))
// 	}
// 	return txl.clnTxsStore.Store(ctx, key, tx)
// }

// func (txl *TransactionLayer) TrackServerTransaction(ctx context.Context, tx ServerTransaction) error {
// 	key, ok := GetServerTransactionKey(tx)
// 	if !ok {
// 		return errtrace.Wrap(NewInvalidArgumentError("invalid transaction"))
// 	}
// 	return txl.srvTxsStore.Store(ctx, key, tx)
// }

func (txl *TransactionLayer) NewClientTransaction(
	ctx context.Context,
	req *OutboundRequest,
	tp ClientTransport,
	opts *ClientTransactionOptions,
) (ClientTransaction, error) {
	if txl.closing.Load() {
		return nil, errtrace.Wrap(ErrTransactionLayerClosed)
	}
	tx, err := txl.clnTxFctr.NewClientTransaction(ctx, req, tp, opts)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	key, _ := GetClientTransactionKey(tx)
	if err = txl.clnTxsStore.Store(ctx, key, tx); err != nil {
		tx.Terminate(ctx) //nolint:errcheck
		return nil, errtrace.Wrap(err)
	}
	tx.OnStateChanged(func(ctx context.Context, _, to TransactionState) {
		if to == TransactionStateTerminated {
			if err := txl.clnTxsStore.Delete(ctx, key); err != nil && !errors.Is(err, ErrTransactionNotFound) {
				txl.log.LogAttrs(ctx, slog.LevelError, "failed to delete client transaction from store",
					slog.Any("transaction", tx),
					slog.Any("error", err),
				)
			}
		}
	})
	return tx, nil
}

func (txl *TransactionLayer) NewServerTransaction(
	ctx context.Context,
	req *InboundRequest,
	tp ServerTransport,
	opts *ServerTransactionOptions,
) (ServerTransaction, error) {
	if txl.closing.Load() {
		return nil, errtrace.Wrap(ErrTransactionLayerClosed)
	}
	tx, err := txl.srvTxFctr.NewServerTransaction(ctx, req, tp, opts)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	key, _ := GetServerTransactionKey(tx)
	if err = txl.srvTxsStore.Store(ctx, key, tx); err != nil {
		tx.Terminate(ctx) //nolint:errcheck
		return nil, errtrace.Wrap(err)
	}
	tx.OnStateChanged(func(ctx context.Context, _, to TransactionState) {
		if to == TransactionStateTerminated {
			if err := txl.srvTxsStore.Delete(ctx, key); err != nil && !errors.Is(err, ErrTransactionNotFound) {
				txl.log.LogAttrs(ctx, slog.LevelError, "failed to delete server transaction from store",
					slog.Any("transaction", tx),
					slog.Any("error", err),
				)
			}
		}
	})
	return tx, nil
}

// const transactLayerCtxKey types.ContextKey = "transaction_layer"

// // TransactionLayerFromContext returns the transaction layer from the given context.
// func TransactionLayerFromContext(ctx context.Context) (*TransactionLayer, bool) {
// 	txl, ok := ctx.Value(transactLayerCtxKey).(*TransactionLayer)
// 	return txl, ok
// }
