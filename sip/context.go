package sip

import (
	"context"

	"github.com/ghettovoice/gosip/internal/util"
)

type (
	transpCtxKey   struct{}
	transactCtxKey struct{}
	elemCtxKey     struct{}
)

// ContextWithElement returns a context carrying the current SIP element.
// Passing nil masks an element inherited from the parent context.
func ContextWithElement(ctx context.Context, elm *Element) context.Context {
	if elm == nil {
		return context.WithValue(ctx, elemCtxKey{}, struct{}{})
	}
	if current, ok := ElementFromContext(ctx); ok && current == elm {
		return ctx
	}
	return context.WithValue(ctx, elemCtxKey{}, elm)
}

// ElementFromContext returns the current SIP element, if present.
func ElementFromContext(ctx context.Context) (*Element, bool) {
	elm, ok := ctx.Value(elemCtxKey{}).(*Element)
	return elm, ok && elm != nil
}

// ContextWithTransport returns a context carrying the current SIP transport.
// Passing a nil transport masks any transport inherited from the parent.
func ContextWithTransport(ctx context.Context, tp Transport) context.Context {
	if util.IsNil(tp) {
		return context.WithValue(ctx, transpCtxKey{}, struct{}{})
	}
	return context.WithValue(ctx, transpCtxKey{}, tp)
}

// TransportFromContext returns the current SIP transport and reports whether
// one is present.
func TransportFromContext(ctx context.Context) (Transport, bool) {
	value, ok := ctx.Value(transpCtxKey{}).(Transport)
	if !ok || util.IsNil(value) {
		return nil, false
	}
	return value, true
}

// ContextWithTransaction returns a context carrying the current SIP transaction.
// The transaction slot holds only the current transaction, so a nested
// transaction shadows the parent without changing it.
// Passing a nil transaction masks any transaction inherited from the parent.
// If the transaction's transport also implements [Transport], it becomes the
// current transport. A narrow [ClientTransport] or [ServerTransport] that does
// not implement [Transport] leaves any inherited transport unchanged.
// A callback can retrieve the current value with [TransactionFromContext] or
// a typed getter, for example:
//
//	tx, ok := ServerTransactionFromContext(ctx)
func ContextWithTransaction(ctx context.Context, tx Transaction) context.Context {
	if util.IsNil(tx) {
		return context.WithValue(ctx, transactCtxKey{}, struct{}{})
	}

	ctx = context.WithValue(ctx, transactCtxKey{}, tx)

	var tp Transport
	switch tx := tx.(type) {
	case ClientTransaction:
		tp, _ = tx.Transport().(Transport)
	case ServerTransaction:
		tp, _ = tx.Transport().(Transport)
	}
	if !util.IsNil(tp) {
		ctx = ContextWithTransport(ctx, tp)
	}

	return ctx
}

// TransactionFromContext returns the current SIP transaction and reports
// whether one is present.
func TransactionFromContext(ctx context.Context) (Transaction, bool) {
	value, ok := ctx.Value(transactCtxKey{}).(Transaction)
	if !ok || util.IsNil(value) {
		return nil, false
	}
	return value, true
}

// ClientTransactionFromContext returns the current SIP client transaction and
// reports whether one is present.
func ClientTransactionFromContext(ctx context.Context) (ClientTransaction, bool) {
	tx, ok := TransactionFromContext(ctx)
	if !ok {
		return nil, false
	}
	clientTx, ok := tx.(ClientTransaction)
	if !ok || util.IsNil(clientTx) {
		return nil, false
	}
	return clientTx, true
}

// ServerTransactionFromContext returns the current SIP server transaction and
// reports whether one is present.
func ServerTransactionFromContext(ctx context.Context) (ServerTransaction, bool) {
	tx, ok := TransactionFromContext(ctx)
	if !ok {
		return nil, false
	}
	serverTx, ok := tx.(ServerTransaction)
	if !ok || util.IsNil(serverTx) {
		return nil, false
	}
	return serverTx, true
}
