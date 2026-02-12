package sip

import "context"

// Handler type aliases.
type (
	ErrorHandler = func(ctx context.Context, err error)

	InboundResponseHandler  = func(ctx context.Context, res *InboundResponseEnvelope)
	InboundRequestHandler   = func(ctx context.Context, req *InboundRequestEnvelope)
	OutboundRequestHandler  = func(ctx context.Context, req *OutboundRequestEnvelope)
	OutboundResponseHandler = func(ctx context.Context, res *OutboundResponseEnvelope)

	TransactionStateHandler  = func(ctx context.Context, from, to TransactionState)
	ClientTransactionHandler = func(ctx context.Context, tx ClientTransaction)
	ServerTransactionHandler = func(ctx context.Context, tx ServerTransaction)
)

// Handler interfaces.
type (
	TransactionInitHandlerRegistry interface {
		OnNewClientTransaction(fn ClientTransactionHandler) (unbind func())
		OnNewServerTransaction(fn ServerTransactionHandler) (unbind func())
	}
)
