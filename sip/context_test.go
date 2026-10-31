package sip_test

import (
	"context"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
)

type contextTestKey struct{}

func TestElementContext_EmptyShadowAndMask(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithDeadline(
		context.WithValue(t.Context(), contextTestKey{}, "original"),
		time.Now().Add(time.Minute),
	)
	defer cancel()

	if got, ok := sip.ElementFromContext(parent); ok || got != nil {
		t.Fatalf("sip.ElementFromContext(parent) = (%v, %t), want (nil, false)", got, ok)
	}

	first, second := &sip.Element{}, &sip.Element{}
	firstCtx := sip.ContextWithElement(parent, first)
	secondCtx := sip.ContextWithElement(firstCtx, second)
	if got, ok := sip.ElementFromContext(secondCtx); !ok || got != second {
		t.Fatalf("sip.ElementFromContext(secondCtx) = (%v, %t), want %v", got, ok, second)
	}
	if got, ok := sip.ElementFromContext(firstCtx); !ok || got != first {
		t.Fatalf("sip.ElementFromContext(firstCtx) = (%v, %t), want %v", got, ok, first)
	}
	if got := secondCtx.Value(contextTestKey{}); got != "original" {
		t.Fatalf("secondCtx.Value(contextTestKey{}) = %v, want original", got)
	}
	if got, ok := secondCtx.Deadline(); !ok {
		t.Fatal("secondCtx.Deadline() reports no inherited deadline")
	} else if want, _ := parent.Deadline(); !got.Equal(want) {
		t.Fatalf("secondCtx.Deadline() = %v, want %v", got, want)
	}

	masked := sip.ContextWithElement(secondCtx, nil)
	if got, ok := sip.ElementFromContext(masked); ok || got != nil {
		t.Fatalf("sip.ElementFromContext(masked) = (%v, %t), want (nil, false)", got, ok)
	}
	var typedNil *sip.Element
	masked = sip.ContextWithElement(firstCtx, typedNil)
	if got, ok := sip.ElementFromContext(masked); ok || got != nil {
		t.Fatalf("sip.ElementFromContext(typed nil) = (%v, %t), want (nil, false)", got, ok)
	}

	cancel()
	if !errors.Is(secondCtx.Err(), context.Canceled) || !errors.Is(masked.Err(), context.Canceled) {
		t.Fatalf("cancellation not inherited: second = %v, masked = %v", secondCtx.Err(), masked.Err())
	}
}

func assertPropagationContext(
	t *testing.T,
	ctx context.Context,
	tx sip.Transaction,
	tp sip.Transport,
	value string,
) {
	t.Helper()
	if got, ok := sip.TransactionFromContext(ctx); !ok || got != tx {
		t.Fatalf("sip.TransactionFromContext(ctx) = (%v, %t), want %v", got, ok, tx)
	}
	if got, ok := sip.TransportFromContext(ctx); !ok || got != tp {
		t.Fatalf("sip.TransportFromContext(ctx) = (%v, %t), want %v", got, ok, tp)
	}
	if got := ctx.Value(contextTestKey{}); got != value {
		t.Fatalf("ctx.Value(contextTestKey{}) = %v, want %q", got, value)
	}
}

func TestContextHelpers_EmptyAndShadowedValues(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithDeadline(context.WithValue(t.Context(), contextTestKey{}, "parent"), time.Now().Add(time.Minute))
	defer cancel()

	if got, ok := sip.TransportFromContext(ctx); ok || got != nil {
		t.Fatalf("sip.TransportFromContext(empty) = (%v, %t), want (nil, false)", got, ok)
	}
	if got, ok := sip.TransactionFromContext(ctx); ok || got != nil {
		t.Fatalf("sip.TransactionFromContext(empty) = (%v, %t), want (nil, false)", got, ok)
	}
	if got, ok := sip.ClientTransactionFromContext(ctx); ok || got != nil {
		t.Fatalf("sip.ClientTransactionFromContext(empty) = (%v, %t), want (nil, false)", got, ok)
	}
	if got, ok := sip.ServerTransactionFromContext(ctx); ok || got != nil {
		t.Fatalf("sip.ServerTransactionFromContext(empty) = (%v, %t), want (nil, false)", got, ok)
	}

	tp := newProxyStubTransport()
	parent := sip.ContextWithTransport(ctx, tp)
	serverTx := newCustomServerTx(t, sip.MagicCookie+".context-server", sip.TransactionStateProceeding)
	serverTx.tp = tp
	serverCtx := sip.ContextWithTransaction(parent, serverTx)
	clientTx := newCustomClientTx(t, sip.MagicCookie+".context-client", sip.TransactionStateCalling)
	clientTx.tp = newStubClientTransport(false)
	clientCtx := sip.ContextWithTransaction(serverCtx, clientTx)

	if got, ok := sip.TransactionFromContext(clientCtx); !ok || got != clientTx {
		t.Fatalf("sip.TransactionFromContext(clientCtx) = (%v, %t), want current client transaction", got, ok)
	}
	if got, ok := sip.ClientTransactionFromContext(clientCtx); !ok || got != clientTx {
		t.Fatalf("sip.ClientTransactionFromContext(clientCtx) = (%v, %t), want current client transaction", got, ok)
	}
	if got, ok := sip.ServerTransactionFromContext(clientCtx); ok || got != nil {
		t.Fatalf("sip.ServerTransactionFromContext(clientCtx) = (%v, %t), want (nil, false)", got, ok)
	}
	if got, ok := sip.TransportFromContext(clientCtx); !ok || got != tp {
		t.Fatalf("sip.TransportFromContext(clientCtx) = (%v, %t), want inherited full transport", got, ok)
	}
	if got, ok := sip.ServerTransactionFromContext(serverCtx); !ok || got != serverTx {
		t.Fatalf("sip.ServerTransactionFromContext(serverCtx) = (%v, %t), want parent server transaction", got, ok)
	}
	if got, ok := sip.TransportFromContext(parent); !ok || got != tp {
		t.Fatalf("sip.TransportFromContext(parent) = (%v, %t), want parent transport", got, ok)
	}
	if got := clientCtx.Value(contextTestKey{}); got != "parent" {
		t.Fatalf("clientCtx.Value(contextTestKey{}) = %v, want parent value", got)
	}
	deadline, ok := clientCtx.Deadline()
	if !ok {
		t.Fatal("clientCtx.Deadline() reports no inherited deadline")
	}
	parentDeadline, _ := ctx.Deadline()
	if !deadline.Equal(parentDeadline) {
		t.Fatalf("clientCtx.Deadline() = %v, want %v", deadline, parentDeadline)
	}
	cancel()
	if clientCtx.Err() == nil || ctx.Err() == nil {
		t.Fatalf("cancellation not preserved: child error = %v, parent error = %v", clientCtx.Err(), ctx.Err())
	}
}

func TestContextHelpers_NilMasksAndNarrowTransport(t *testing.T) {
	t.Parallel()

	fullTransport := newProxyStubTransport()
	ctx := sip.ContextWithTransport(t.Context(), fullTransport)
	ctx = sip.ContextWithTransport(ctx, nil)
	if got, ok := sip.TransportFromContext(ctx); ok || got != nil {
		t.Fatalf("sip.TransportFromContext(masked) = (%v, %t), want (nil, false)", got, ok)
	}

	var typedNilTransport *proxyStubTransport
	ctx = sip.ContextWithTransport(t.Context(), fullTransport)
	ctx = sip.ContextWithTransport(ctx, typedNilTransport)
	if got, ok := sip.TransportFromContext(ctx); ok || got != nil {
		t.Fatalf("sip.TransportFromContext(typed nil masked) = (%v, %t), want (nil, false)", got, ok)
	}

	var typedNilClient *stubClientTransaction
	inheritedTx := &stubClientTransaction{}
	ctx = sip.ContextWithTransaction(t.Context(), inheritedTx)
	ctx = sip.ContextWithTransaction(ctx, typedNilClient)
	if got, ok := sip.TransactionFromContext(ctx); ok || got != nil {
		t.Fatalf("sip.TransactionFromContext(typed nil masked) = (%v, %t), want (nil, false)", got, ok)
	}
	if got, ok := sip.ClientTransactionFromContext(ctx); ok || got != nil {
		t.Fatalf("sip.ClientTransactionFromContext(typed nil masked) = (%v, %t), want (nil, false)", got, ok)
	}

	ctx = sip.ContextWithTransaction(ctx, nil)
	if got, ok := sip.TransactionFromContext(ctx); ok || got != nil {
		t.Fatalf("sip.TransactionFromContext(nil masked) = (%v, %t), want (nil, false)", got, ok)
	}

	narrow := newStubClientTransport(false)
	tx := newCustomClientTx(t, sip.MagicCookie+".context-narrow", sip.TransactionStateCalling)
	tx.tp = narrow
	ctx = sip.ContextWithTransaction(t.Context(), tx)
	if got, ok := sip.TransportFromContext(ctx); ok || got != nil {
		t.Fatalf("sip.TransportFromContext(narrow transport) = (%v, %t), want (nil, false)", got, ok)
	}

	ctx = sip.ContextWithTransaction(sip.ContextWithTransport(t.Context(), fullTransport), tx)
	if got, ok := sip.TransportFromContext(ctx); !ok || got != fullTransport {
		t.Fatalf("sip.TransportFromContext(narrow with inherited transport) = (%v, %t), want inherited full transport", got, ok)
	}
}
