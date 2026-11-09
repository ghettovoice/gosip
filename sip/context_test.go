package sip_test

import (
	"context"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
)

type testCtxKey struct{}

func TestElementContext_EmptyShadowAndMask(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithDeadline(
		context.WithValue(t.Context(), testCtxKey{}, "original"),
		time.Now().Add(time.Minute),
	)
	t.Cleanup(cancel)

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
	if got := secondCtx.Value(testCtxKey{}); got != "original" {
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

func assertPropagationCtx(
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
	if got := ctx.Value(testCtxKey{}); got != value {
		t.Fatalf("ctx.Value(contextTestKey{}) = %v, want %q", got, value)
	}
}

func TestContextHelpers_EmptyAndShadowedValues(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithDeadline(
		context.WithValue(t.Context(), testCtxKey{}, "parent"),
		time.Now().Add(time.Minute),
	)
	t.Cleanup(cancel)

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

	tp := newPrxStubTransp()
	parent := sip.ContextWithTransport(ctx, tp)
	srvTx := newCustomSrvTransact(t, sip.MagicCookie+".context-server", sip.TransactionStateProceeding)
	srvTx.tp = tp
	srvTxCtx := sip.ContextWithTransaction(parent, srvTx)
	clnTx := newCustomClnTransact(t, sip.MagicCookie+".context-client", sip.TransactionStateCalling)
	clnTx.tp = newStubClnTransp(false)
	clnTxCtx := sip.ContextWithTransaction(srvTxCtx, clnTx)

	if got, ok := sip.TransactionFromContext(clnTxCtx); !ok || got != clnTx {
		t.Fatalf("sip.TransactionFromContext(clnTxCtx) = (%v, %t), want current client transaction", got, ok)
	}
	if got, ok := sip.ClientTransactionFromContext(clnTxCtx); !ok || got != clnTx {
		t.Fatalf("sip.ClientTransactionFromContext(clnTxCtx) = (%v, %t), want current client transaction", got, ok)
	}
	if got, ok := sip.ServerTransactionFromContext(clnTxCtx); ok || got != nil {
		t.Fatalf("sip.ServerTransactionFromContext(clnTxCtx) = (%v, %t), want (nil, false)", got, ok)
	}
	if got, ok := sip.TransportFromContext(clnTxCtx); !ok || got != tp {
		t.Fatalf("sip.TransportFromContext(clnTxCtx) = (%v, %t), want inherited full transport", got, ok)
	}
	if got, ok := sip.ServerTransactionFromContext(srvTxCtx); !ok || got != srvTx {
		t.Fatalf("sip.ServerTransactionFromContext(srvTxCtx) = (%v, %t), want parent server transaction", got, ok)
	}
	if got, ok := sip.TransportFromContext(parent); !ok || got != tp {
		t.Fatalf("sip.TransportFromContext(parent) = (%v, %t), want parent transport", got, ok)
	}
	if got := clnTxCtx.Value(testCtxKey{}); got != "parent" {
		t.Fatalf("clnTxCtx.Value(testCtxKey{}) = %v, want parent value", got)
	}
	deadline, ok := clnTxCtx.Deadline()
	if !ok {
		t.Fatal("clnTxCtx.Deadline() reports no inherited deadline")
	}
	parentDeadline, _ := ctx.Deadline()
	if !deadline.Equal(parentDeadline) {
		t.Fatalf("clnTxCtx.Deadline() = %v, want %v", deadline, parentDeadline)
	}

	cancel()

	if clnTxCtx.Err() == nil || ctx.Err() == nil {
		t.Fatalf("cancellation not preserved: child error = %v, parent error = %v", clnTxCtx.Err(), ctx.Err())
	}
}

func TestContextHelpers_NilMasksAndNarrowTransport(t *testing.T) {
	t.Parallel()

	fullTransport := newPrxStubTransp()
	ctx := sip.ContextWithTransport(t.Context(), fullTransport)
	ctx = sip.ContextWithTransport(ctx, nil)
	if got, ok := sip.TransportFromContext(ctx); ok || got != nil {
		t.Fatalf("sip.TransportFromContext(masked) = (%v, %t), want (nil, false)", got, ok)
	}

	var typedNilTransport *prxStubTransp
	ctx = sip.ContextWithTransport(t.Context(), fullTransport)
	ctx = sip.ContextWithTransport(ctx, typedNilTransport)
	if got, ok := sip.TransportFromContext(ctx); ok || got != nil {
		t.Fatalf("sip.TransportFromContext(typed nil masked) = (%v, %t), want (nil, false)", got, ok)
	}

	var typedNilClient *stubClnTransact
	inheritedTx := &stubClnTransact{}
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

	narrow := newStubClnTransp(false)
	tx := newCustomClnTransact(t, sip.MagicCookie+".context-narrow", sip.TransactionStateCalling)
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
