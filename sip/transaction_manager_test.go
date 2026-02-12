package sip_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

func TestTransactionManager_Close_Idempotent(t *testing.T) {
	t.Parallel()

	txm := sip.NewTransactionManager(nil)
	ctx := t.Context()

	// First close should succeed
	if err := txm.Close(ctx); err != nil {
		t.Fatalf("first txm.Close() error = %v, want nil", err)
	}

	// Second close should also succeed (idempotent)
	if err := txm.Close(ctx); err != nil {
		t.Fatalf("second txm.Close() error = %v, want nil", err)
	}
}

func TestTransactionManager_Close_RejectsNewClientTransaction(t *testing.T) {
	t.Parallel()

	txm := sip.NewTransactionManager(nil)
	ctx := t.Context()

	// Close the manager
	if err := txm.Close(ctx); err != nil {
		t.Fatalf("txm.Close() error = %v, want nil", err)
	}

	// Attempt to create new client transaction should fail
	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubTransport(sip.TransportProto("UDP"), 5060)
	laddr := tp.LocalAddr()
	req := newOutInviteReq(t, sip.TransportProto("UDP"), "", laddr, raddr)

	_, err := txm.NewClientTransaction(ctx, req, tp, nil)
	if !errors.Is(err, sip.ErrTransactionManagerClosed) {
		t.Fatalf("txm.NewClientTransaction() error = %v, want %v", err, sip.ErrTransactionManagerClosed)
	}
}

func TestTransactionManager_Close_RejectsNewServerTransaction(t *testing.T) {
	t.Parallel()

	txm := sip.NewTransactionManager(nil)
	ctx := t.Context()

	// Close the manager
	if err := txm.Close(ctx); err != nil {
		t.Fatalf("txm.Close() error = %v, want nil", err)
	}

	// Attempt to create new server transaction should fail
	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubTransport(sip.TransportProto("UDP"), 5060)
	laddr := tp.LocalAddr()
	req := newInInviteReq(t, sip.TransportProto("UDP"), "", laddr, raddr)

	_, err := txm.NewServerTransaction(ctx, req, tp, nil)
	if !errors.Is(err, sip.ErrTransactionManagerClosed) {
		t.Fatalf("txm.NewServerTransaction() error = %v, want %v", err, sip.ErrTransactionManagerClosed)
	}
}

func TestTransactionManager_Close_TerminatesActiveTransactions(t *testing.T) {
	t.Parallel()

	txm := sip.NewTransactionManager(nil)
	ctx := t.Context()
	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubTransport(sip.TransportProto("UDP"), 5060)
	laddr := tp.LocalAddr()

	// Create a client transaction
	clnReq := newOutInviteReq(t, sip.TransportProto("UDP"), "", laddr, raddr)
	clnTx, err := txm.NewClientTransaction(ctx, clnReq, tp, nil)
	if err != nil {
		t.Fatalf("txm.NewClientTransaction() error = %v, want nil", err)
	}

	// Create a server transaction (use different branch to avoid conflict)
	srvReq := newInInviteReq(t, sip.TransportProto("UDP"), sip.MagicCookie+".srv-branch", laddr, raddr)
	srvTx, err := txm.NewServerTransaction(ctx, srvReq, tp, nil)
	if err != nil {
		t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
	}

	// Close the manager - should terminate all transactions
	if err := txm.Close(ctx); err != nil {
		t.Fatalf("txm.Close() error = %v, want nil", err)
	}

	// Verify transactions are terminated
	waitForTransactState(t, clnTx, sip.TransactionStateTerminated, 100*time.Millisecond)
	waitForTransactState(t, srvTx, sip.TransactionStateTerminated, 100*time.Millisecond)
}

func TestTransactionManager_Close_WithContextTimeout(t *testing.T) {
	t.Parallel()

	txm := sip.NewTransactionManager(nil)
	// Close with timeout context
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := txm.Close(ctx); err != nil {
		t.Fatalf("txm.Close() error = %v, want nil", err)
	}
}

func TestTransactionManager_InboundRequest_RFC3261RetransmitMatching(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	txm := sip.NewTransactionManager(nil)

	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubTransport(sip.TransportProto("UDP"), 5060)
	laddr := tp.LocalAddr()

	// Create initial INVITE request with RFC3261 branch
	branch := sip.MagicCookie + ".test-branch"
	req := newInInviteReq(t, sip.TransportProto("UDP"), branch, laddr, raddr)

	// Create server transaction
	tx, err := txm.NewServerTransaction(ctx, req, tp, nil)
	if err != nil {
		t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
	}

	// Send initial request to transaction
	if err := tx.RecvRequest(ctx, req); err != nil {
		t.Fatalf("tx.RecvRequest() error = %v, want nil", err)
	}

	// Create retransmission - same request, different envelope
	retransmit := newInInviteReq(t, sip.TransportProto("UDP"), branch, laddr, raddr)

	nextCalled := false
	receiver := sip.ChainInboundRequest(
		[]sip.InboundRequestInterceptor{txm.InboundRequestInterceptor()},
		sip.RequestReceiverFunc(func(context.Context, *sip.InboundRequestEnvelope) error {
			nextCalled = true
			return nil
		}),
	)

	// Send retransmission - should be matched to existing transaction
	if err := receiver.RecvRequest(ctx, retransmit); err != nil {
		t.Fatalf("receiver.RecvRequest() error = %v, want nil", err)
	}

	// Verify transaction received the retransmission (not passed to next)
	if nextCalled {
		t.Fatalf("expected retransmission to be handled by transaction, not passed to next")
	}

	// Verify transaction is still alive and in correct state
	if got := tx.State(); got != sip.TransactionStateProceeding && got != sip.TransactionStateTrying {
		t.Fatalf("expected transaction to be in Trying or Proceeding state, got %v", got)
	}
}

func TestTransactionManager_InboundRequest_RFC2345RetransmitMatching(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	txm := sip.NewTransactionManager(nil)

	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubTransport(sip.TransportProto("UDP"), 5060)
	laddr := tp.LocalAddr()

	// Create initial INVITE request with RFC2543-style (no magic cookie) branch
	branch := "rfc2543.branch"
	req := newInInviteReq(t, sip.TransportProto("UDP"), branch, laddr, raddr)

	// Create server transaction
	tx, err := txm.NewServerTransaction(ctx, req, tp, nil)
	if err != nil {
		t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
	}

	// Send initial request to transaction
	if err := tx.RecvRequest(ctx, req); err != nil {
		t.Fatalf("tx.RecvRequest() error = %v, want nil", err)
	}

	// Create retransmission with same RFC2543 characteristics
	retransmit := newInInviteReq(t, sip.TransportProto("UDP"), branch, laddr, raddr)

	nextCalled := false
	receiver := sip.ChainInboundRequest(
		[]sip.InboundRequestInterceptor{txm.InboundRequestInterceptor()},
		sip.RequestReceiverFunc(func(context.Context, *sip.InboundRequestEnvelope) error {
			nextCalled = true
			return nil
		}),
	)

	// Send retransmission - should be matched to existing transaction
	if err := receiver.RecvRequest(ctx, retransmit); err != nil {
		t.Fatalf("receiver.RecvRequest() error = %v, want nil", err)
	}

	// Verify transaction received the retransmission (not passed to next)
	if nextCalled {
		t.Fatalf("expected retransmission to be handled by transaction, not passed to next")
	}

	// Verify transaction is still alive
	if got := tx.State(); got != sip.TransactionStateProceeding && got != sip.TransactionStateTrying {
		t.Fatalf("expected transaction to be in Trying or Proceeding state, got %v", got)
	}
}

func TestTransactionManager_InboundRequest_ACKMatching_2xxResponse(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	txm := sip.NewTransactionManager(nil)

	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubTransport(sip.TransportProto("UDP"), 5060)
	laddr := tp.LocalAddr()

	// Create initial INVITE request
	branch := sip.MagicCookie + ".invite-branch"
	inviteReq := newInInviteReq(t, sip.TransportProto("UDP"), branch, laddr, raddr)

	// Create server transaction for INVITE
	tx, err := txm.NewServerTransaction(ctx, inviteReq, tp, nil)
	if err != nil {
		t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
	}

	// Send initial INVITE to transaction
	if err := tx.RecvRequest(ctx, inviteReq); err != nil {
		t.Fatalf("tx.RecvRequest() error = %v, want nil", err)
	}

	// Create ACK request for 2xx response
	// ACK for 2xx should NOT match the INVITE transaction (ACK for 2xx is new transaction)
	ackReq := newInInviteReq(t, sip.TransportProto("UDP"), branch+".ack", laddr, raddr)
	ackReq.Message().Method = sip.RequestMethodAck
	if cseq, ok := ackReq.Message().Headers.CSeq(); ok {
		ackReq.Message().Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: sip.RequestMethodAck})
	}

	nextCalled := false
	receiver := sip.ChainInboundRequest(
		[]sip.InboundRequestInterceptor{txm.InboundRequestInterceptor()},
		sip.RequestReceiverFunc(func(context.Context, *sip.InboundRequestEnvelope) error {
			nextCalled = true
			return nil
		}),
	)

	// Send ACK - should NOT match the INVITE transaction (ACK for 2xx is new transaction)
	if err := receiver.RecvRequest(ctx, ackReq); err != nil {
		t.Fatalf("receiver.RecvRequest() error = %v, want nil", err)
	}

	// ACK for 2xx should be passed to next handler as new transaction
	if !nextCalled {
		t.Fatalf("expected ACK for 2xx to be passed to next handler as new transaction")
	}
}

func TestTransactionManager_InboundRequest_ACKMatching_3xxResponse(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	txm := sip.NewTransactionManager(nil)

	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubTransport(sip.TransportProto("UDP"), 5060)
	laddr := tp.LocalAddr()

	// Create initial INVITE request
	branch := sip.MagicCookie + ".invite-branch"
	inviteReq := newInInviteReq(t, sip.TransportProto("UDP"), branch, laddr, raddr)

	// Create server transaction for INVITE
	tx, err := txm.NewServerTransaction(ctx, inviteReq, tp, nil)
	if err != nil {
		t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
	}

	// Send initial INVITE to transaction
	if err := tx.RecvRequest(ctx, inviteReq); err != nil {
		t.Fatalf("tx.RecvRequest() error = %v, want nil", err)
	}

	// Create ACK request for 3xx response
	// ACK for 3xx+ should match the original INVITE transaction
	ackReq := newInInviteReq(t, sip.TransportProto("UDP"), branch, laddr, raddr)
	ackReq.Message().Method = sip.RequestMethodAck
	if cseq, ok := ackReq.Message().Headers.CSeq(); ok {
		ackReq.Message().Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: sip.RequestMethodAck})
	}

	nextCalled := false
	receiver := sip.ChainInboundRequest(
		[]sip.InboundRequestInterceptor{txm.InboundRequestInterceptor()},
		sip.RequestReceiverFunc(func(context.Context, *sip.InboundRequestEnvelope) error {
			nextCalled = true
			return nil
		}),
	)

	// Send ACK - should match the INVITE transaction based on branch matching
	if err := receiver.RecvRequest(ctx, ackReq); err != nil {
		t.Fatalf("receiver.RecvRequest() error = %v, want nil", err)
	}

	// ACK for 3xx+ should be handled by the transaction, not passed to next
	if nextCalled {
		t.Fatalf("expected ACK for 3xx to be handled by transaction, not passed to next")
	}

	// Verify transaction is still alive
	if got := tx.State(); got != sip.TransactionStateProceeding && got != sip.TransactionStateTrying {
		t.Fatalf("expected transaction to be in Trying or Proceeding state, got %v", got)
	}
}

func TestTransactionManager_InboundRequest_PassesToNext(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	txm := sip.NewTransactionManager(nil)

	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubTransport(sip.TransportProto("UDP"), 5060)
	laddr := tp.LocalAddr()

	// Create a new request that doesn't match any existing transaction
	branch := sip.MagicCookie + ".new-request"
	newReq := newInInviteReq(t, sip.TransportProto("UDP"), branch, laddr, raddr)

	nextCalled := false
	var receivedReq *sip.InboundRequestEnvelope
	next := func(_ context.Context, req *sip.InboundRequestEnvelope) error {
		nextCalled = true
		receivedReq = req
		return nil
	}

	receiver := sip.ChainInboundRequest(
		[]sip.InboundRequestInterceptor{txm.InboundRequestInterceptor()},
		sip.RequestReceiverFunc(next),
	)

	// Send new request - should be passed to next handler
	if err := receiver.RecvRequest(ctx, newReq); err != nil {
		t.Fatalf("receiver.RecvRequest() error = %v, want nil", err)
	}

	// Verify request was passed to next handler
	if !nextCalled {
		t.Fatalf("expected new request to be passed to next handler")
	}

	if receivedReq == nil {
		t.Fatalf("expected request to be received by next handler")
	}

	// Verify the received request matches the sent request
	if receivedReq.Method() != newReq.Method() {
		t.Fatalf("expected method %v, got %v", newReq.Method(), receivedReq.Method())
	}

	// Verify branch parameter matches
	receivedVia, ok1 := util.SeqFirst(receivedReq.Headers().Via())
	sentVia, ok2 := util.SeqFirst(newReq.Headers().Via())
	if !ok1 || !ok2 {
		t.Fatalf("missing Via header")
	}
	receivedBranch, _ := receivedVia.Branch()
	sentBranch, _ := sentVia.Branch()
	if receivedBranch != sentBranch {
		t.Fatalf("expected branch %v, got %v", sentBranch, receivedBranch)
	}
}

func TestTransactionManager_InboundRequest_WhenClosed(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	txm := sip.NewTransactionManager(nil)

	// Close the transaction manager
	if err := txm.Close(ctx); err != nil {
		t.Fatalf("txm.Close() error = %v, want nil", err)
	}

	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubTransport(sip.TransportProto("UDP"), 5060)
	laddr := tp.LocalAddr()

	// Create a new request
	branch := sip.MagicCookie + ".new-request-closed"
	newReq := newInInviteReq(t, sip.TransportProto("UDP"), branch, laddr, raddr)

	receiver := sip.ChainInboundRequest(
		[]sip.InboundRequestInterceptor{txm.InboundRequestInterceptor()},
		sip.RequestReceiverFunc(func(context.Context, *sip.InboundRequestEnvelope) error {
			t.Fatalf("next handler should not be called when manager is closed")
			return nil
		}),
	)

	// Send new request - should be rejected with ServiceUnavailable
	if err := receiver.RecvRequest(ctx, newReq); err == nil {
		t.Fatalf("expected error when manager is closed, got nil")
	} else if !strings.Contains(err.Error(), "transaction manager closed") {
		t.Fatalf("expected transaction manager closed error, got: %v", err)
	}
}

func TestTransactionManager_InboundResponse_DeliversToTransaction(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := sip.NewMemoryClientTransactionStore()
	txm := sip.NewTransactionManager(&sip.TransactionManagerOptions{ClientTransactionStore: store})

	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubTransport(sip.TransportProto("UDP"), 5063)
	laddr := tp.LocalAddr()
	oreq := newOutInviteReq(t, sip.TransportProto("UDP"), "", laddr, raddr)
	res := newInRes(t, oreq, sip.ResponseStatusRinging)

	key, err := sip.MakeClientTransactionKey(res)
	if err != nil {
		t.Fatalf("sip.MakeClientTransactionKey(res) error = %v, want nil", err)
	}

	tx := &stubClientTransaction{key: key}
	if err := store.Store(ctx, tx); err != nil {
		t.Fatalf("store.Store() error = %v, want nil", err)
	}

	nextCalled := false
	receiver := sip.ChainInboundResponse(
		[]sip.InboundResponseInterceptor{txm.InboundResponseInterceptor()},
		sip.ResponseReceiverFunc(func(context.Context, *sip.InboundResponseEnvelope) error {
			nextCalled = true
			return nil
		}),
	)
	if receiver == nil {
		t.Fatal("expected inbound response receiver")
	}
	if err := receiver.RecvResponse(ctx, res); err != nil {
		t.Fatalf("receiver.RecvResponse() error = %v, want nil", err)
	}

	if !tx.recvCalled.Load() {
		t.Fatalf("expected transaction RecvResponse to be called")
	}
	if nextCalled {
		t.Fatalf("expected next handler not to be called")
	}
}

func TestTransactionManager_InboundResponse_PassesToNext(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	txm := sip.NewTransactionManager(nil)

	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	tp := newStubTransport(sip.TransportProto("UDP"), 5064)
	laddr := tp.LocalAddr()
	oreq := newOutInviteReq(t, sip.TransportProto("UDP"), "", laddr, raddr)
	res := newInRes(t, oreq, sip.ResponseStatusRinging)

	nextCalled := false
	next := func(_ context.Context, _ *sip.InboundResponseEnvelope) error {
		nextCalled = true
		return nil
	}

	receiver := sip.ChainInboundResponse(
		[]sip.InboundResponseInterceptor{txm.InboundResponseInterceptor()},
		sip.ResponseReceiverFunc(next),
	)
	if receiver == nil {
		t.Fatal("expected inbound response receiver")
	}
	if err := receiver.RecvResponse(ctx, res); err != nil {
		t.Fatalf("receiver.RecvResponse() error = %v, want nil", err)
	}

	if !nextCalled {
		t.Fatalf("expected next handler to be called")
	}
}
