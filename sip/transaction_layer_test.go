package sip_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
)

func TestTransactionLayer_Close_Idempotent(t *testing.T) {
	t.Parallel()

	tp := newStubTransport(sip.TransportProto("UDP"), 5060)

	txl, err := sip.NewTransactionLayer(tp, nil)
	if err != nil {
		t.Fatalf("NewTransactionLayer() error = %v", err)
	}

	ctx := t.Context()

	// First close should succeed
	if err := txl.Close(ctx); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	// Second close should also succeed (idempotent)
	if err := txl.Close(ctx); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestTransactionLayer_Close_RejectsNewClientTransaction(t *testing.T) {
	t.Parallel()

	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubTransport(sip.TransportProto("UDP"), 5060)
	laddr := tp.LocalAddr()

	txl, err := sip.NewTransactionLayer(tp, nil)
	if err != nil {
		t.Fatalf("NewTransactionLayer() error = %v", err)
	}

	ctx := t.Context()

	// Close the layer
	if err := txl.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Attempt to create new client transaction should fail
	req := newOutInviteReq(t, sip.TransportProto("UDP"), "", laddr, raddr)
	_, err = txl.NewClientTransaction(ctx, req, tp, nil)
	if !errors.Is(err, sip.ErrTransactionLayerClosed) {
		t.Fatalf("NewClientTransaction() error = %v, want %v", err, sip.ErrTransactionLayerClosed)
	}
}

func TestTransactionLayer_Close_RejectsNewServerTransaction(t *testing.T) {
	t.Parallel()

	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubTransport(sip.TransportProto("UDP"), 5060)
	laddr := tp.LocalAddr()

	txl, err := sip.NewTransactionLayer(tp, nil)
	if err != nil {
		t.Fatalf("NewTransactionLayer() error = %v", err)
	}

	ctx := t.Context()

	// Close the layer
	if err := txl.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Attempt to create new server transaction should fail
	req := newInInviteReq(t, sip.TransportProto("UDP"), "", laddr, raddr)
	_, err = txl.NewServerTransaction(ctx, req, tp, nil)
	if !errors.Is(err, sip.ErrTransactionLayerClosed) {
		t.Fatalf("NewServerTransaction() error = %v, want %v", err, sip.ErrTransactionLayerClosed)
	}
}

func TestTransactionLayer_Close_TerminatesActiveTransactions(t *testing.T) {
	t.Parallel()

	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubTransport(sip.TransportProto("UDP"), 5060)
	laddr := tp.LocalAddr()

	txl, err := sip.NewTransactionLayer(tp, nil)
	if err != nil {
		t.Fatalf("NewTransactionLayer() error = %v", err)
	}

	ctx := t.Context()

	// Create a client transaction
	clnReq := newOutInviteReq(t, sip.TransportProto("UDP"), "", laddr, raddr)
	clnTx, err := txl.NewClientTransaction(ctx, clnReq, tp, nil)
	if err != nil {
		t.Fatalf("NewClientTransaction() error = %v", err)
	}

	// Create a server transaction (use different branch to avoid conflict)
	srvReq := newInInviteReq(t, sip.TransportProto("UDP"), sip.MagicCookie+".srv-branch", laddr, raddr)
	srvTx, err := txl.NewServerTransaction(ctx, srvReq, tp, nil)
	if err != nil {
		t.Fatalf("NewServerTransaction() error = %v", err)
	}

	// Close the layer - should terminate all transactions
	if err := txl.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Verify transactions are terminated
	waitForTransactState(t, clnTx, sip.TransactionStateTerminated, 100*time.Millisecond)
	waitForTransactState(t, srvTx, sip.TransactionStateTerminated, 100*time.Millisecond)
}

func TestTransactionLayer_Close_UnmatchedACKSilentlyDiscarded(t *testing.T) {
	t.Parallel()

	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubTransport(sip.TransportProto("UDP"), 5060)
	laddr := tp.LocalAddr()

	txl, err := sip.NewTransactionLayer(tp, nil)
	if err != nil {
		t.Fatalf("NewTransactionLayer() error = %v", err)
	}

	ctx := t.Context()

	// Close the layer
	if err := txl.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Create an ACK request (unmatched) using existing helper
	inviteReq := newInInviteReq(t, sip.TransportProto("UDP"), "", laddr, raddr)
	res, err := inviteReq.NewResponse(sip.ResponseStatusOK, nil)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	ackReq := newInAckReq(t, inviteReq, res)

	// Send unmatched ACK request - should be silently discarded (no 503)
	tp.triggerRequest(ctx, ackReq)

	// Ensure no response is sent
	if got := tp.responseCount(); got != 0 {
		t.Fatalf("unexpected sends on transport: got %d, want 0", got)
	}
}

func TestTransactionLayer_Close_UnmatchedResponseSilentlyDiscarded(t *testing.T) {
	t.Parallel()

	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubTransport(sip.TransportProto("UDP"), 5060)
	laddr := tp.LocalAddr()

	txl, err := sip.NewTransactionLayer(tp, nil)
	if err != nil {
		t.Fatalf("NewTransactionLayer() error = %v", err)
	}

	ctx := t.Context()

	// Close the layer
	if err := txl.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Create an unmatched response using existing helper
	req := newOutInviteReq(t, sip.TransportProto("UDP"), "", laddr, raddr)
	res := newInRes(t, req, sip.ResponseStatusOK)

	// Send unmatched response - should be silently discarded
	tp.triggerResponse(ctx, res)

	// Ensure no response is sent (and no panic)
	if got := tp.responseCount(); got != 0 {
		t.Fatalf("unexpected sends on transport: got %d, want 0", got)
	}
}

func TestTransactionLayer_Close_WithContextTimeout(t *testing.T) {
	t.Parallel()

	tp := newStubTransport(sip.TransportProto("UDP"), 5060)

	txl, err := sip.NewTransactionLayer(tp, nil)
	if err != nil {
		t.Fatalf("NewTransactionLayer() error = %v", err)
	}

	// Close with timeout context
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := txl.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
