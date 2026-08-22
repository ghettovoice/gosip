package sip_test

import (
	"context"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

type stubTransaction struct {
	typ      sip.TransactionType
	handlers []sip.TransactionStateHandler
}

func (tx *stubTransaction) Type() sip.TransactionType { return tx.typ }
func (*stubTransaction) State() sip.TransactionState  { return 0 }

func (*stubTransaction) MatchMessage(msg sip.Message) bool {
	// For stub transactions, we'll accept any message for testing purposes
	return true
}

func (tx *stubTransaction) BindStateHandler(fn sip.TransactionStateHandler) (cancel func()) {
	tx.handlers = append(tx.handlers, fn)
	return func() {}
}

func (*stubTransaction) BindErrorHandler(fn sip.ErrorHandler) (cancel func()) {
	_ = fn
	return func() {}
}

func (*stubTransaction) Terminate(_ context.Context, _ error) error { return nil }

type stubClientTransaction struct {
	stubTransaction
	key        sip.ClientTransactionKey
	recvCalled atomic.Bool
	recvRes    *sip.ResponseEnvelope
}

func (tx *stubClientTransaction) Type() sip.TransactionType {
	if tx.typ.IsValid() {
		return tx.typ
	}
	return sip.TransactionTypeClientInvite
}

func (*stubClientTransaction) State() sip.TransactionState         { return sip.TransactionStateCalling }
func (*stubClientTransaction) Start(context.Context) error         { return nil }
func (tx *stubClientTransaction) Key() sip.ClientTransactionKey    { return tx.key }
func (*stubClientTransaction) Request() *sip.RequestEnvelope       { return nil }
func (*stubClientTransaction) LastResponse() *sip.ResponseEnvelope { return nil }
func (*stubClientTransaction) Transport() sip.ClientTransport      { return nil }
func (*stubClientTransaction) LastError() error                    { return nil }

func (tx *stubClientTransaction) RecvResponse(_ context.Context, res *sip.ResponseEnvelope) error {
	tx.recvRes = res
	tx.recvCalled.Store(true)
	return nil
}

func (*stubClientTransaction) BindResponseHandler(_ sip.InboundResponseHandler) (cancel func()) {
	return func() {}
}

func TestTransactionManager_CancelClientTransaction_Idempotent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		remote := netip.MustParseAddrPort("192.168.1.100:5060")
		local := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".cancel-idempotent", local, remote)

		invTx, err := txm.NewClientTransaction(ctx, req, tp)
		if err != nil {
			t.Fatalf("txm.NewClientTransaction() error = %v, want nil", err)
		}
		if call := tp.waitSendReq(t); call.req.Method() != sip.RequestMethodInvite {
			t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
		}

		if err := invTx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("invTx.RecvResponse(180) error = %v, want nil", err)
		}
		waitForTransactState(t, invTx, sip.TransactionStateProceeding, 100*time.Millisecond)

		cancelTx, err := txm.CancelClientTransaction(ctx, invTx)
		if err != nil {
			t.Fatalf("txm.CancelClientTransaction() error = %v, want nil", err)
		}
		if call := tp.waitSendReq(t); call.req.Method() != sip.RequestMethodCancel {
			t.Fatalf("cancel send method = %q, want %q", call.req.Method(), sip.RequestMethodCancel)
		}

		again, err := txm.CancelClientTransaction(ctx, invTx)
		if err != nil {
			t.Fatalf("second txm.CancelClientTransaction() error = %v, want nil", err)
		}
		if again != cancelTx {
			t.Fatalf("second CANCEL transaction = %p, want existing transaction %p", again, cancelTx)
		}

		if err := cancelTx.RecvResponse(ctx, newInRes(t, cancelTx.Request(), sip.ResponseStatusOK)); err != nil {
			t.Fatalf("cancelTx.RecvResponse(200) error = %v, want nil", err)
		}

		if err := invTx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusBusyHere)); err != nil {
			t.Fatalf("invTx.RecvResponse(486) error = %v, want nil", err)
		}
		if call := tp.waitSendReq(t); call.req.Method() != sip.RequestMethodAck {
			t.Fatalf("INVITE final response ACK method = %q, want %q", call.req.Method(), sip.RequestMethodAck)
		}

		afterFinal, err := txm.CancelClientTransaction(ctx, invTx)
		if err != nil {
			t.Fatalf("third txm.CancelClientTransaction() error = %v, want nil", err)
		}
		if afterFinal != cancelTx {
			t.Fatalf("CANCEL transaction after INVITE final = %p, want existing transaction %p", afterFinal, cancelTx)
		}
		tp.ensureNoSendReq(t)

		if err := invTx.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("invTx.Terminate() error = %v, want nil", err)
		}
		if err := cancelTx.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("cancelTx.Terminate() error = %v, want nil", err)
		}
	})
}

func TestTransactionManager_Close_Idempotent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		txm := &sip.TransactionManager{}

		// First close should succeed
		if err := txm.Close(t.Context()); err != nil {
			t.Fatalf("first txm.Close(ctx) error = %v, want nil", err)
		}

		// Second close should also succeed (idempotent)
		if err := txm.Close(t.Context()); err != nil {
			t.Fatalf("second txm.Close(ctx) error = %v, want nil", err)
		}
	})
}

func TestTransactionManager_Close_RejectsNewClientTransaction(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		txm := &sip.TransactionManager{}
		ctx := t.Context()

		// Close the manager
		if err := txm.Close(ctx); err != nil {
			t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
		}

		// Attempt to create new client transaction should fail
		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", "", laddr, raddr)

		_, err := txm.NewClientTransaction(ctx, req, tp)
		if !errors.Is(err, sip.ErrTransactionManagerClosed) {
			t.Fatalf("txm.NewClientTransaction() error = %v, want %v", err, sip.ErrTransactionManagerClosed)
		}
	})
}

func TestTransactionManager_Close_RejectsNewServerTransaction(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		txm := &sip.TransactionManager{}
		ctx := t.Context()

		// Close the manager
		if err := txm.Close(ctx); err != nil {
			t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
		}

		// Attempt to create new server transaction should fail
		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubServerTransport(false)
		req := newInInviteReq(t, "UDP", "", laddr, raddr)

		_, err := txm.NewServerTransaction(ctx, req, tp)
		if !errors.Is(err, sip.ErrTransactionManagerClosed) {
			t.Fatalf("txm.NewServerTransaction() error = %v, want %v", err, sip.ErrTransactionManagerClosed)
		}
	})
}

func TestTransactionManager_Close_TerminatesActiveTransactions(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		clnTp := newStubClientTransport(false)

		// Create a client transaction
		clnReq := newOutInviteReq(t, "UDP", "", laddr, raddr)

		clnTx, err := txm.NewClientTransaction(ctx, clnReq, clnTp)
		if err != nil {
			t.Fatalf("txm.NewClientTransaction() error = %v, want nil", err)
		}

		// Create a server transaction (use different branch to avoid conflict)
		srvTp := newStubServerTransport(false)
		srvReq := newInInviteReq(t, "UDP", sip.MagicCookie+".srv-branch", laddr, raddr)

		srvTx, err := txm.NewServerTransaction(ctx, srvReq, srvTp)
		if err != nil {
			t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
		}

		// Close the manager - should terminate all transactions
		if err := txm.Close(ctx); err != nil {
			t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
		}

		// Verify transactions are terminated
		waitForTransactState(t, clnTx, sip.TransactionStateTerminated, 100*time.Millisecond)
		waitForTransactState(t, srvTx, sip.TransactionStateTerminated, 100*time.Millisecond)
	})
}

func TestTransactionManager_InboundRequest_RFC3261RetransmitMatching(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{
			ServerTransactionStore: sip.NewMemoryServerTransactionStore(),
		}
		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubServerTransport(false)

		// Create initial INVITE request with RFC3261 branch
		branch := sip.MagicCookie + ".test-branch"
		req := newInInviteReq(t, "UDP", branch, laddr, raddr)

		// Create server transaction
		tx, err := txm.NewServerTransaction(ctx, req, tp)
		if err != nil {
			t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
		}

		// Send initial request to transaction
		if err := tx.RecvRequest(ctx, req); err != nil {
			t.Fatalf("tx.RecvRequest() error = %v, want nil", err)
		}

		// Create retransmission - same request, different envelope
		retransmit := newInInviteReq(t, "UDP", branch, laddr, raddr)

		nextCalled := false
		receiver := sip.InterceptInboundRequest(
			[]sip.InboundRequestInterceptor{txm},
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
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
	})
}

func TestTransactionManager_InboundRequest_RFC2345RetransmitMatching(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{
			ServerTransactionStore: sip.NewMemoryServerTransactionStore(),
		}
		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubServerTransport(false)

		// Create initial INVITE request with RFC2543-style (no magic cookie) branch
		branch := "rfc2543.branch"
		req := newInInviteReq(t, "UDP", branch, laddr, raddr)

		// Create server transaction
		tx, err := txm.NewServerTransaction(ctx, req, tp)
		if err != nil {
			t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
		}

		// Send initial request to transaction
		if err := tx.RecvRequest(ctx, req); err != nil {
			t.Fatalf("tx.RecvRequest() error = %v, want nil", err)
		}

		// Create retransmission with same RFC2543 characteristics
		retransmit := newInInviteReq(t, "UDP", branch, laddr, raddr)

		nextCalled := false
		receiver := sip.InterceptInboundRequest(
			[]sip.InboundRequestInterceptor{txm},
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
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
	})
}

func TestTransactionManager_InboundRequest_ACKMatching_2xxResponse(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{
			// Use an isolated store to avoid cross-test conflicts with the shared global store.
			ServerTransactionStore: sip.NewMemoryServerTransactionStore(),
		}
		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubServerTransport(false)

		// Create initial INVITE request
		branch := sip.MagicCookie + ".invite-branch"
		inviteReq := newInInviteReq(t, "UDP", branch, laddr, raddr)

		// Create server transaction for INVITE
		tx, err := txm.NewServerTransaction(ctx, inviteReq, tp)
		if err != nil {
			t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
		}

		// Send initial INVITE to transaction
		if err := tx.RecvRequest(ctx, inviteReq); err != nil {
			t.Fatalf("tx.RecvRequest() error = %v, want nil", err)
		}

		// Create ACK request for 2xx response
		// ACK for 2xx should NOT match the INVITE transaction (ACK for 2xx is new transaction)
		ackReq := newInInviteReq(t, "UDP", branch+".ack", laddr, raddr)

		ackReq.Message().Method = sip.RequestMethodAck
		if cseq, ok := ackReq.Message().Headers.CSeq(); ok {
			ackReq.Message().Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: sip.RequestMethodAck})
		}

		nextCalled := false
		receiver := sip.InterceptInboundRequest(
			[]sip.InboundRequestInterceptor{txm},
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
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
	})
}

func TestTransactionManager_InboundRequest_ACKMatching_3xxResponse(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{
			ServerTransactionStore: sip.NewMemoryServerTransactionStore(),
		}
		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubServerTransport(false)

		// Create initial INVITE request
		branch := sip.MagicCookie + ".invite-branch"
		inviteReq := newInInviteReq(t, "UDP", branch, laddr, raddr)

		// Create server transaction for INVITE
		tx, err := txm.NewServerTransaction(ctx, inviteReq, tp)
		if err != nil {
			t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
		}

		// Send initial INVITE to transaction
		if err := tx.RecvRequest(ctx, inviteReq); err != nil {
			t.Fatalf("tx.RecvRequest() error = %v, want nil", err)
		}

		// Create ACK request for 3xx response
		// ACK for 3xx+ should match the original INVITE transaction
		ackReq := newInInviteReq(t, "UDP", branch, laddr, raddr)

		ackReq.Message().Method = sip.RequestMethodAck
		if cseq, ok := ackReq.Message().Headers.CSeq(); ok {
			ackReq.Message().Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: sip.RequestMethodAck})
		}

		nextCalled := false
		receiver := sip.InterceptInboundRequest(
			[]sip.InboundRequestInterceptor{txm},
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
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
	})
}

func TestTransactionManager_InboundRequest_PassesToNext(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")

		// Create a new request that doesn't match any existing transaction
		branch := sip.MagicCookie + ".new-request"
		newReq := newInInviteReq(t, "UDP", branch, laddr, raddr)

		nextCalled := false

		var receivedReq *sip.RequestEnvelope

		next := func(_ context.Context, req *sip.RequestEnvelope) error {
			nextCalled = true
			receivedReq = req
			return nil
		}

		receiver := sip.InterceptInboundRequest(
			[]sip.InboundRequestInterceptor{txm},
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
		receivedVia, ok1 := util.SeqFirst(receivedReq.Headers().Vias())

		sentVia, ok2 := util.SeqFirst(newReq.Headers().Vias())
		if !ok1 || !ok2 {
			t.Fatalf("missing Via header")
		}

		receivedBranch, _ := receivedVia.Branch()

		sentBranch, _ := sentVia.Branch()
		if receivedBranch != sentBranch {
			t.Fatalf("expected branch %v, got %v", sentBranch, receivedBranch)
		}
	})
}

func TestTransactionManager_InboundRequest_WhenClosed(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	txm := &sip.TransactionManager{}

	// Close the transaction manager
	if err := txm.Close(ctx); err != nil {
		t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
	}

	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	laddr := netip.MustParseAddrPort("0.0.0.0:5060")

	// Create a new request
	branch := sip.MagicCookie + ".new-request-closed"
	newReq := newInInviteReq(t, "UDP", branch, laddr, raddr)

	receiver := sip.InterceptInboundRequest(
		[]sip.InboundRequestInterceptor{txm},
		sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
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
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		store := sip.NewMemoryClientTransactionStore()
		txm := &sip.TransactionManager{ClientTransactionStore: store}
		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		oreq := newOutInviteReq(t, "UDP", "", laddr, raddr)
		res := newInRes(t, oreq, sip.ResponseStatusRinging)

		key, err := sip.ClientTransactionKeyFromMessage(res)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey(res) error = %v, want nil", err)
		}

		tx := &stubClientTransaction{key: key}
		if err := store.Store(ctx, tx); err != nil {
			t.Fatalf("store.Store() error = %v, want nil", err)
		}

		nextCalled := false

		receiver := sip.InterceptInboundResponse(
			[]sip.InboundResponseInterceptor{txm},
			sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error {
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
	})
}

func TestTransactionManager_InboundResponse_PassesToNext(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		oreq := newOutInviteReq(t, "UDP", "", laddr, raddr)
		res := newInRes(t, oreq, sip.ResponseStatusRinging)

		nextCalled := false
		next := func(_ context.Context, _ *sip.ResponseEnvelope) error {
			nextCalled = true
			return nil
		}

		receiver := sip.InterceptInboundResponse(
			[]sip.InboundResponseInterceptor{txm},
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
	})
}
