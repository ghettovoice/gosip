package sip_test

import (
	"context"
	"maps"
	"net/netip"
	"slices"
	"sync"
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
	typ           sip.TransactionType
	started       bool
	handlers      []sip.TransactionStateHandler
	startHandlers []sip.TransactionStartHandler
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

func (tx *stubTransaction) Started() bool { return tx.started }

func (tx *stubTransaction) BindStartHandler(fn sip.TransactionStartHandler) (unbind func()) {
	tx.startHandlers = append(tx.startHandlers, fn)
	return func() {}
}

func (tx *stubTransaction) start(ctx context.Context) {
	tx.started = true
	for _, fn := range tx.startHandlers {
		fn.HandleTransactionStart(ctx)
	}
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

func (*stubClientTransaction) State() sip.TransactionState { return sip.TransactionStateCalling }
func (tx *stubClientTransaction) Start(ctx context.Context) error {
	tx.start(ctx)
	return nil
}
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

func TestTransactionManager_NewCancelClientTransaction(t *testing.T) {
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

		if _, err := txm.NewCancelClientTransaction(ctx, invTx); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("txm.NewCancelClientTransaction(dormant) error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}

		startClientTransaction(t, invTx)
		if call := tp.waitSendReq(t); call.req.Method() != sip.RequestMethodInvite {
			t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
		}

		if err := invTx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("invTx.RecvResponse(180) error = %v, want nil", err)
		}
		waitForTransactState(t, invTx, sip.TransactionStateProceeding, 100*time.Millisecond)

		tp.drainSendReqs()

		cancelTx, err := txm.NewCancelClientTransaction(ctx, invTx)
		if err != nil {
			t.Fatalf("txm.NewCancelClientTransaction() error = %v, want nil", err)
		}

		select {
		case call := <-tp.sendReqCalls:
			t.Fatalf("unexpected send before CANCEL start with method %v", call.req.Method())
		default:
		}

		if err := cancelTx.Start(ctx); err != nil {
			t.Fatalf("cancelTx.Start() error = %v, want nil", err)
		}
		if call := tp.waitSendReq(t); call.req.Method() != sip.RequestMethodCancel {
			t.Fatalf("cancel send method = %q, want %q", call.req.Method(), sip.RequestMethodCancel)
		}

		if _, err := txm.NewCancelClientTransaction(ctx, invTx); !errors.Is(err, sip.ErrTransactionDuplicate) {
			t.Fatalf("second txm.NewCancelClientTransaction() error = %v, want %v", err, sip.ErrTransactionDuplicate)
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

		if _, err := txm.NewCancelClientTransaction(ctx, invTx); !errors.Is(err, sip.ErrTransactionDuplicate) {
			t.Fatalf("third txm.NewCancelClientTransaction() error = %v, want %v", err, sip.ErrTransactionDuplicate)
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
		startClientTransaction(t, clnTx)

		// Create a server transaction (use different branch to avoid conflict)
		srvTp := newStubServerTransport(false)
		srvReq := newInInviteReq(t, "UDP", sip.MagicCookie+".srv-branch", laddr, raddr)

		srvTx, err := txm.NewServerTransaction(ctx, srvReq, srvTp)
		if err != nil {
			t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, srvTx)

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
		txm := &sip.TransactionManager{}
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
		startServerTransaction(t, tx)

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
		txm := &sip.TransactionManager{}
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
		startServerTransaction(t, tx)

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
		// Use an isolated store to avoid cross-test conflicts with the shared global store.
		txm := &sip.TransactionManager{}
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
		startServerTransaction(t, tx)

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
		txm := &sip.TransactionManager{}
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
		startServerTransaction(t, tx)

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

	sentinel := errors.New("next receiver error")

	nextCalls := 0

	var receivedReq *sip.RequestEnvelope

	receiver := sip.InterceptInboundRequest(
		[]sip.InboundRequestInterceptor{txm},
		sip.RequestReceiverFunc(func(_ context.Context, req *sip.RequestEnvelope) error {
			nextCalls++
			receivedReq = req
			return sentinel
		}),
	)

	// Send new request - should be passed to next handler even when manager is closed
	err := receiver.RecvRequest(ctx, newReq)
	if !errors.Is(err, sentinel) {
		t.Fatalf("receiver.RecvRequest() error = %v, want %v", err, sentinel)
	}

	if nextCalls != 1 {
		t.Fatalf("next handler calls = %d, want 1", nextCalls)
	}
	if receivedReq != newReq {
		t.Fatalf("next handler request = %p, want same request %p", receivedReq, newReq)
	}
}

func TestTransactionManager_InboundResponse_WhenClosed(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	txm := &sip.TransactionManager{}

	if err := txm.Close(ctx); err != nil {
		t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
	}

	raddr := netip.MustParseAddrPort("192.168.1.100:5060")
	laddr := netip.MustParseAddrPort("0.0.0.0:5060")

	oreq := newOutInviteReq(t, "UDP", "", laddr, raddr)
	res := newInRes(t, oreq, sip.ResponseStatusRinging)

	sentinel := errors.New("next receiver error")

	nextCalls := 0

	var receivedRes *sip.ResponseEnvelope

	receiver := sip.InterceptInboundResponse(
		[]sip.InboundResponseInterceptor{txm},
		sip.ResponseReceiverFunc(func(_ context.Context, res *sip.ResponseEnvelope) error {
			nextCalls++
			receivedRes = res
			return sentinel
		}),
	)

	err := receiver.RecvResponse(ctx, res)
	if !errors.Is(err, sentinel) {
		t.Fatalf("receiver.RecvResponse() error = %v, want %v", err, sentinel)
	}

	if nextCalls != 1 {
		t.Fatalf("next handler calls = %d, want 1", nextCalls)
	}
	if receivedRes != res {
		t.Fatalf("next handler response = %p, want same response %p", receivedRes, res)
	}
}

func TestTransactionManager_InboundResponse_DeliversToTransaction(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		oreq := newOutInviteReq(t, "UDP", "", laddr, raddr)
		res := newInRes(t, oreq, sip.ResponseStatusRinging)

		key, err := sip.MakeClientTransactionKey(res)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey(res) error = %v, want nil", err)
		}

		tx := &stubClientTransaction{key: key}
		txm := &sip.TransactionManager{
			ClientTransactionFactory: sip.ClientTransactionFactoryFunc(
				func(
					*sip.RequestEnvelope,
					sip.ClientTransport,
					...sip.ClientTransactionOptions,
				) (sip.ClientTransaction, error) {
					return tx, nil
				},
			),
		}

		got, err := txm.NewClientTransaction(ctx, oreq, newStubClientTransport(false))
		if err != nil {
			t.Fatalf("txm.NewClientTransaction() error = %v, want nil", err)
		}
		if got != tx {
			t.Fatalf("txm.NewClientTransaction() = %p, want stub transaction %p", got, tx)
		}
		if stored, ok := txm.LoadClientTransaction(key); !ok || stored != tx {
			t.Fatalf("txm.LoadClientTransaction() = %p, %v, want stub transaction %p, true", stored, ok, tx)
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

func TestTransactionManager_NewClientTransaction_Duplicate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		defer func() {
			if err := txm.Close(ctx); err != nil {
				t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
			}
		}()

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubClientTransport(false)

		var hdlrCalls atomic.Int32
		txm.BindClientTransactionHandler(sip.ClientTransactionHandlerFunc(
			func(context.Context, sip.ClientTransaction) { hdlrCalls.Add(1) },
		))

		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".dup-client", laddr, raddr)

		tx, err := txm.NewClientTransaction(ctx, req, tp)
		if err != nil {
			t.Fatalf("txm.NewClientTransaction() error = %v, want nil", err)
		}

		tp.ensureNoSendReq(t)
		if _, err := tx.(*sip.InviteClientTransaction).Snapshot(); //nolint:forcetypeassert
		!errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("tx.Snapshot() before Start error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}

		if got := hdlrCalls.Load(); got != 1 {
			t.Fatalf("client transaction handler calls = %d, want 1", got)
		}

		startClientTransaction(t, tx)
		if call := tp.waitSendReq(t); call.req.Method() != sip.RequestMethodInvite {
			t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
		}

		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.RecvResponse(180) error = %v, want nil", err)
		}
		waitForTransactState(t, tx, sip.TransactionStateProceeding, 100*time.Millisecond)

		dup, err := txm.NewClientTransaction(ctx, req, tp)
		if !errors.Is(err, sip.ErrTransactionDuplicate) {
			t.Fatalf("second txm.NewClientTransaction() error = %v, want %v", err, sip.ErrTransactionDuplicate)
		}
		if dup != nil {
			t.Fatalf("second txm.NewClientTransaction() = %p, want nil", dup)
		}
		if got := hdlrCalls.Load(); got != 1 {
			t.Fatalf("client transaction handler calls after duplicate = %d, want 1", got)
		}
		tp.ensureNoSendReq(t)
	})
}

func TestTransactionManager_NewServerTransaction_Duplicate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		defer func() {
			if err := txm.Close(ctx); err != nil {
				t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
			}
		}()

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubServerTransport(false)

		var hdlrCalls atomic.Int32
		txm.BindServerTransactionHandler(sip.ServerTransactionHandlerFunc(
			func(context.Context, sip.ServerTransaction) { hdlrCalls.Add(1) },
		))

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".dup-server", laddr, raddr)

		tx, err := txm.NewServerTransaction(ctx, req, tp)
		if err != nil {
			t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
		}
		if got := hdlrCalls.Load(); got != 1 {
			t.Fatalf("server transaction handler calls = %d, want 1", got)
		}
		tp.ensureNoSendRes(t)
		if _, err := tx.(*sip.InviteServerTransaction).Snapshot(); //nolint:forcetypeassert
		!errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("tx.Snapshot() before Start error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}

		dup, err := txm.NewServerTransaction(ctx, req, tp)
		if !errors.Is(err, sip.ErrTransactionDuplicate) {
			t.Fatalf("second txm.NewServerTransaction() error = %v, want %v", err, sip.ErrTransactionDuplicate)
		}
		if dup != nil {
			t.Fatalf("second txm.NewServerTransaction() = %p, want nil", dup)
		}
		if got := hdlrCalls.Load(); got != 1 {
			t.Fatalf("server transaction handler calls after duplicate = %d, want 1", got)
		}

		if got := tx.State(); got != sip.TransactionStateProceeding {
			t.Fatalf("server transaction state = %v, want %v", got, sip.TransactionStateProceeding)
		}
		if stored, ok := txm.LoadServerTransaction(tx.Key()); !ok || stored != tx {
			t.Fatalf("txm.LoadServerTransaction() = %p, %v, want %p, true", stored, ok, tx)
		}
	})
}

func TestTransactionManager_ClientTransactionHandler_ClosesManager(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}

		var hdlrCalls atomic.Int32

		var closeErr error

		txm.BindClientTransactionHandler(sip.ClientTransactionHandlerFunc(
			func(ctx context.Context, _ sip.ClientTransaction) {
				hdlrCalls.Add(1)
				closeErr = txm.Close(ctx)
			},
		))

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".reentrant-close-client", laddr, raddr)

		_, err := txm.NewClientTransaction(ctx, req, tp)
		if !errors.Is(err, sip.ErrTransactionManagerClosed) {
			t.Fatalf("txm.NewClientTransaction() error = %v, want %v", err, sip.ErrTransactionManagerClosed)
		}

		if got := hdlrCalls.Load(); got != 1 {
			t.Fatalf("client transaction handler calls = %d, want 1", got)
		}
		if closeErr != nil {
			t.Fatalf("txm.Close() inside handler error = %v, want nil", closeErr)
		}

		for tx := range txm.AllClientTransactions() {
			t.Fatalf("txm.AllClientTransactions() yielded %v, want empty", tx)
		}
		for tx := range txm.AllServerTransactions() {
			t.Fatalf("txm.AllServerTransactions() yielded %v, want empty", tx)
		}
	})
}

func TestTransactionManager_ServerTransactionHandler_ClosesManager(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}

		var hdlrCalls atomic.Int32

		var closeErr error

		txm.BindServerTransactionHandler(sip.ServerTransactionHandlerFunc(
			func(ctx context.Context, _ sip.ServerTransaction) {
				hdlrCalls.Add(1)
				closeErr = txm.Close(ctx)
			},
		))

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubServerTransport(false)
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".reentrant-close-server", laddr, raddr)

		_, err := txm.NewServerTransaction(ctx, req, tp)
		if !errors.Is(err, sip.ErrTransactionManagerClosed) {
			t.Fatalf("txm.NewServerTransaction() error = %v, want %v", err, sip.ErrTransactionManagerClosed)
		}

		if got := hdlrCalls.Load(); got != 1 {
			t.Fatalf("server transaction handler calls = %d, want 1", got)
		}
		if closeErr != nil {
			t.Fatalf("txm.Close() inside handler error = %v, want nil", closeErr)
		}

		for tx := range txm.AllClientTransactions() {
			t.Fatalf("txm.AllClientTransactions() yielded %v, want empty", tx)
		}
		for tx := range txm.AllServerTransactions() {
			t.Fatalf("txm.AllServerTransactions() yielded %v, want empty", tx)
		}
	})
}

func TestTransactionManager_InboundRequest_AckMatching(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		branch     string
		status     sip.ResponseStatus
		tagRequest bool
		wantFound  bool
		wantNext   int
		wantState  sip.TransactionState
	}{
		{
			name:      "rfc3261 non-2xx ack matches transaction",
			branch:    sip.MagicCookie + ".ack-486",
			status:    sip.ResponseStatusBusyHere,
			wantFound: true,
			wantState: sip.TransactionStateConfirmed,
		},
		{
			name:      "rfc3261 2xx ack is a new transaction",
			branch:    sip.MagicCookie + ".ack-200",
			status:    sip.ResponseStatusOK,
			wantNext:  1,
			wantState: sip.TransactionStateAccepted,
		},
		{
			name:      "rfc2543 non-2xx ack matches transaction",
			branch:    "legacy.ack-486",
			status:    sip.ResponseStatusBusyHere,
			wantFound: true,
			wantState: sip.TransactionStateConfirmed,
		},
		{
			name:      "rfc2543 2xx ack matches transaction",
			branch:    "legacy.ack-200",
			status:    sip.ResponseStatusOK,
			wantFound: true,
			wantState: sip.TransactionStateAccepted,
		},
		{
			name:       "rfc2543 tagged invite non-2xx ack matches transaction",
			branch:     "legacy.tag-ack-486",
			status:     sip.ResponseStatusBusyHere,
			tagRequest: true,
			wantFound:  true,
			wantState:  sip.TransactionStateConfirmed,
		},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				txm := &sip.TransactionManager{}
				defer func() {
					if err := txm.Close(ctx); err != nil {
						t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
					}
				}()

				raddr := netip.MustParseAddrPort("192.168.1.100:5060")
				laddr := netip.MustParseAddrPort("0.0.0.0:5060")
				tp := newStubServerTransport(false)
				req := newInInviteReq(t, "UDP", c.branch, laddr, raddr)

				if c.tagRequest {
					req.WithMessage(func(r *sip.Request) {
						to, _ := r.Headers.To()
						to.Params = make(sip.Values).Set("tag", "req-to-tag")
					})
				}

				tx, err := txm.NewServerTransaction(ctx, req, tp)
				if err != nil {
					t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
				}
				startServerTransaction(t, tx)

				if err := tx.RecvRequest(ctx, req); err != nil {
					t.Fatalf("tx.RecvRequest() error = %v, want nil", err)
				}

				if err := tx.Respond(ctx, c.status); err != nil {
					t.Fatalf("tx.Respond() error = %v, want nil", err)
				}
				_ = tp.waitSendRes(t)

				lastRes := tx.LastResponse()
				if lastRes == nil {
					t.Fatal("tx.LastResponse() = nil, want response")
				}

				var resTag string
				lastRes.WithMessage(func(r *sip.Response) {
					to, _ := r.Headers.To()
					resTag, _ = to.Tag()
				})
				if resTag == "" {
					t.Fatal("response To tag is empty, want non-empty")
				}
				if c.tagRequest && resTag != "req-to-tag" {
					t.Fatalf("response To tag = %q, want %q", resTag, "req-to-tag")
				}

				ack := newInAckReq(t, req, lastRes)

				key, err := sip.MakeServerTransactionKey(ack)
				if err != nil {
					t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
				}
				if key.Method != string(sip.RequestMethodAck) {
					t.Fatalf("ack key.Method = %q, want %q", key.Method, sip.RequestMethodAck)
				}

				got, found := txm.LoadServerTransaction(key)
				if found != c.wantFound {
					t.Fatalf("txm.LoadServerTransaction() found = %v, want %v", found, c.wantFound)
				}
				if c.wantFound && got != tx {
					t.Fatalf("txm.LoadServerTransaction() = %p, want %p", got, tx)
				}
				if key.Method != string(sip.RequestMethodAck) {
					t.Fatalf("key.Method = %q after lookup, want unchanged %q", key.Method, sip.RequestMethodAck)
				}

				if matched := tx.MatchMessage(ack); matched != c.wantFound {
					t.Fatalf("tx.MatchMessage(ack) = %v, want %v", matched, c.wantFound)
				}

				nextCalls := 0
				receiver := sip.InterceptInboundRequest(
					[]sip.InboundRequestInterceptor{txm},
					sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
						nextCalls++
						return nil
					}),
				)
				if err := receiver.RecvRequest(ctx, ack); err != nil {
					t.Fatalf("receiver.RecvRequest() error = %v, want nil", err)
				}
				if nextCalls != c.wantNext {
					t.Fatalf("next handler calls = %d, want %d", nextCalls, c.wantNext)
				}

				waitForTransactState(t, tx, c.wantState, 100*time.Millisecond)
			})
		})
	}
}

func TestTransactionManager_InboundRequest_LegacyAckLookupMisses(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		defer func() {
			if err := txm.Close(ctx); err != nil {
				t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
			}
		}()

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubServerTransport(false)
		req := newInInviteReq(t, "UDP", "legacy.ack-mismatch", laddr, raddr)

		tx, err := txm.NewServerTransaction(ctx, req, tp)
		if err != nil {
			t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, tx)
		if err := tx.RecvRequest(ctx, req); err != nil {
			t.Fatalf("tx.RecvRequest() error = %v, want nil", err)
		}
		if err := tx.Respond(ctx, sip.ResponseStatusBusyHere); err != nil {
			t.Fatalf("tx.Respond() error = %v, want nil", err)
		}
		_ = tp.waitSendRes(t)

		ack := newInAckReq(t, req, tx.LastResponse())
		ack.WithMessage(func(r *sip.Request) {
			to, _ := r.Headers.To()
			to.Params = make(sip.Values).Set("tag", "wrong-tag")
		})

		key, err := sip.MakeServerTransactionKey(ack)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		if tx.MatchMessage(ack) {
			t.Fatal("tx.MatchMessage(ack with wrong To tag) = true, want false")
		}

		nextCalls := 0
		receiver := sip.InterceptInboundRequest(
			[]sip.InboundRequestInterceptor{txm},
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
				nextCalls++
				return nil
			}),
		)
		if err := receiver.RecvRequest(ctx, ack); err != nil {
			t.Fatalf("receiver.RecvRequest() error = %v, want nil", err)
		}
		if nextCalls != 1 {
			t.Fatalf("next handler calls = %d, want 1", nextCalls)
		}
		if got := tx.State(); got != sip.TransactionStateCompleted {
			t.Fatalf("tx.State() = %v, want %v", got, sip.TransactionStateCompleted)
		}
		if key.Method != string(sip.RequestMethodAck) {
			t.Fatalf("key.Method = %q after lookup, want unchanged %q", key.Method, sip.RequestMethodAck)
		}

		key.Method = string(sip.RequestMethodInvite)
		if _, found := txm.LoadServerTransaction(key); found {
			t.Fatal("txm.LoadServerTransaction(invite key) found = true, want false")
		}
		key.Method = string(sip.RequestMethodCancel)
		if _, found := txm.LoadServerTransaction(key); found {
			t.Fatal("txm.LoadServerTransaction(cancel key) found = true, want false")
		}

		niTp := newStubServerTransport(false)
		niReq := newInNonInviteReq(t, "UDP", sip.MagicCookie+".ni-ack", laddr, raddr)
		niTx, err := txm.NewServerTransaction(ctx, niReq, niTp)
		if err != nil {
			t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, niTx)
		if err := niTx.RecvRequest(ctx, niReq); err != nil {
			t.Fatalf("niTx.RecvRequest() error = %v, want nil", err)
		}
		if err := niTx.Respond(ctx, sip.ResponseStatusBusyHere); err != nil {
			t.Fatalf("niTx.Respond() error = %v, want nil", err)
		}
		_ = niTp.waitSendRes(t)

		niAck := newInAckReq(t, niReq, niTx.LastResponse())
		niKey, err := sip.MakeServerTransactionKey(niAck)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		if _, found := txm.LoadServerTransaction(niKey); found {
			t.Fatal("txm.LoadServerTransaction(ack key) found non-invite transaction, want false")
		}
		if niTx.MatchMessage(niAck) {
			t.Fatal("niTx.MatchMessage(ack) = true, want false")
		}
	})
}

func TestTransactionManager_LookupMergedRequest(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		branch string
	}{
		{name: "rfc3261", branch: sip.MagicCookie + ".merged-a"},
		{name: "rfc2543", branch: "legacy.merged-a"},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				txm := &sip.TransactionManager{}
				defer func() {
					if err := txm.Close(ctx); err != nil {
						t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
					}
				}()

				raddr := netip.MustParseAddrPort("192.168.1.100:5060")
				laddr := netip.MustParseAddrPort("0.0.0.0:5060")
				tp := newStubServerTransport(false)

				reqA := newInInviteReq(t, "UDP", c.branch, laddr, raddr)
				txA, err := txm.NewServerTransaction(ctx, reqA, tp)
				if err != nil {
					t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
				}
				startServerTransaction(t, txA)

				reqB := newInInviteReq(t, "UDP", c.branch+"-b", laddr, raddr)

				key, found := txm.LookupMergedRequest(reqB)
				if !found || !key.Equal(txA.Key()) {
					t.Fatalf("txm.LookupMergedRequest(reqB) = %+v, %v, want %+v, true", key, found, txA.Key())
				}

				key, found = txm.LookupMergedRequest(reqB.Message())
				if !found || !key.Equal(txA.Key()) {
					t.Fatalf("txm.LookupMergedRequest(raw reqB) = %+v, %v, want %+v, true", key, found, txA.Key())
				}

				key, found = txm.LookupMergedRequest(reqA)
				if !found || !key.Equal(txA.Key()) {
					t.Fatalf("txm.LookupMergedRequest(reqA) = %+v, %v, want %+v, true", key, found, txA.Key())
				}

				variants := []struct {
					name   string
					mutate func(*sip.Request)
				}{
					{
						name: "different call id",
						mutate: func(r *sip.Request) {
							r.Headers.Set(header.CallID("other-call@bob.voip.com"))
						},
					},
					{
						name: "different from tag",
						mutate: func(r *sip.Request) {
							from, _ := r.Headers.From()
							from.Params = make(sip.Values).Set("tag", "other-tag")
						},
					},
					{
						name: "different cseq number",
						mutate: func(r *sip.Request) {
							r.Headers.Set(&header.CSeq{SeqNum: 99, Method: sip.RequestMethodInvite})
						},
					},
					{
						name: "different method cancel",
						mutate: func(r *sip.Request) {
							r.Method = sip.RequestMethodCancel
							r.Headers.Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodCancel})
						},
					},
					{
						name: "ack method never aliases invite",
						mutate: func(r *sip.Request) {
							r.Method = sip.RequestMethodAck
							r.Headers.Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodAck})
						},
					},
					{
						name: "call id case change",
						mutate: func(r *sip.Request) {
							r.Headers.Set(header.CallID("CALL-1234@BOB.VOIP.COM"))
						},
					},
					{
						name: "from tag case change",
						mutate: func(r *sip.Request) {
							from, _ := r.Headers.From()
							from.Params = make(sip.Values).Set("tag", "FROM-1234")
						},
					},
				}

				for _, v := range variants {
					req := reqB.Clone().(*sip.RequestEnvelope) //nolint:forcetypeassert
					req.WithMessage(v.mutate)
					if key, found := txm.LookupMergedRequest(req); found {
						t.Fatalf("txm.LookupMergedRequest(%s) = %+v, true, want not found", v.name, key)
					}
				}

				if key, found := txm.LookupMergedRequest(&sip.Request{}); found {
					t.Fatalf("txm.LookupMergedRequest(invalid) = %+v, true, want not found", key)
				}
			})
		})
	}
}

func TestTransactionManager_LookupMergedRequest_IndependentTransactions(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		defer func() {
			if err := txm.Close(ctx); err != nil {
				t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
			}
		}()

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")

		reqA := newInInviteReq(t, "UDP", sip.MagicCookie+".indep-a", laddr, raddr)
		txA, err := txm.NewServerTransaction(ctx, reqA, newStubServerTransport(false))
		if err != nil {
			t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, txA)

		reqB := newInInviteReq(t, "UDP", sip.MagicCookie+".indep-b", laddr, raddr)
		reqB.WithMessage(func(r *sip.Request) {
			r.Headers.Set(header.CallID("indep-call-b@bob.voip.com"))
		})
		txB, err := txm.NewServerTransaction(ctx, reqB, newStubServerTransport(false))
		if err != nil {
			t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, txB)

		if key, found := txm.LookupMergedRequest(reqA); !found || !key.Equal(txA.Key()) {
			t.Fatalf("txm.LookupMergedRequest(reqA) = %+v, %v, want %+v, true", key, found, txA.Key())
		}
		if key, found := txm.LookupMergedRequest(reqB); !found || !key.Equal(txB.Key()) {
			t.Fatalf("txm.LookupMergedRequest(reqB) = %+v, %v, want %+v, true", key, found, txB.Key())
		}

		if err := txA.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("txA.Terminate() error = %v, want nil", err)
		}
		waitForTransactState(t, txA, sip.TransactionStateTerminated, 100*time.Millisecond)

		if key, found := txm.LookupMergedRequest(reqA); found {
			t.Fatalf("txm.LookupMergedRequest(reqA) = %+v, true, want not found", key)
		}
		if key, found := txm.LookupMergedRequest(reqB); !found || !key.Equal(txB.Key()) {
			t.Fatalf("txm.LookupMergedRequest(reqB) = %+v, %v, want %+v, true", key, found, txB.Key())
		}

		if err := txB.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("txB.Terminate() error = %v, want nil", err)
		}
		waitForTransactState(t, txB, sip.TransactionStateTerminated, 100*time.Millisecond)

		if key, found := txm.LookupMergedRequest(reqB); found {
			t.Fatalf("txm.LookupMergedRequest(reqB) = %+v, true, want not found", key)
		}
	})
}

func TestTransactionManager_LookupMergedRequest_Start(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		defer func() {
			if err := txm.Close(ctx); err != nil {
				t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
			}
		}()

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")

		reqA := newInInviteReq(t, "UDP", sip.MagicCookie+".life-a", laddr, raddr)
		txA, err := txm.NewServerTransaction(ctx, reqA, newStubServerTransport(false))
		if err != nil {
			t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, txA)

		reqB := newInInviteReq(t, "UDP", sip.MagicCookie+".life-b", laddr, raddr)
		txB, err := txm.NewServerTransaction(ctx, reqB, newStubServerTransport(false))
		if err != nil {
			t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, txB)

		if key, found := txm.LookupMergedRequest(reqA); !found || !key.Equal(txB.Key()) {
			t.Fatalf("txm.LookupMergedRequest(reqA) = %+v, %v, want latest %+v, true", key, found, txB.Key())
		}

		if err := txA.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("txA.Terminate() error = %v, want nil", err)
		}
		waitForTransactState(t, txA, sip.TransactionStateTerminated, 100*time.Millisecond)

		if key, found := txm.LookupMergedRequest(reqB); !found || !key.Equal(txB.Key()) {
			t.Fatalf("txm.LookupMergedRequest(reqB) = %+v, %v, want %+v, true", key, found, txB.Key())
		}

		if err := txB.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("txB.Terminate() error = %v, want nil", err)
		}
		waitForTransactState(t, txB, sip.TransactionStateTerminated, 100*time.Millisecond)

		if key, found := txm.LookupMergedRequest(reqB); found {
			t.Fatalf("txm.LookupMergedRequest(reqB) = %+v, true, want not found", key)
		}

		reqC := newInInviteReq(t, "UDP", sip.MagicCookie+".life-c", laddr, raddr)
		origQ := newInInviteReq(t, "UDP", sip.MagicCookie+".life-c", laddr, raddr)

		txC, err := txm.NewServerTransaction(ctx, reqC, newStubServerTransport(false))
		if err != nil {
			t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, txC)

		reqC.WithMessage(func(r *sip.Request) {
			r.Headers.Set(header.CallID("mutated-call@bob.voip.com"))
		})

		if key, found := txm.LookupMergedRequest(origQ); !found || !key.Equal(txC.Key()) {
			t.Fatalf("txm.LookupMergedRequest(origQ) = %+v, %v, want %+v, true", key, found, txC.Key())
		}
		if key, found := txm.LookupMergedRequest(reqC); found {
			t.Fatalf("txm.LookupMergedRequest(mutated reqC) = %+v, true, want not found", key)
		}

		if err := txC.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("txC.Terminate() error = %v, want nil", err)
		}
		waitForTransactState(t, txC, sip.TransactionStateTerminated, 100*time.Millisecond)

		if key, found := txm.LookupMergedRequest(origQ); found {
			t.Fatalf("txm.LookupMergedRequest(origQ) = %+v, true, want not found", key)
		}
	})
}

func TestTransactionManager_RegisterClientTransaction(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		defer func() {
			if err := txm.Close(ctx); err != nil {
				t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
			}
		}()

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".reg-client", laddr, raddr)

		if err := txm.RegisterClientTransaction(ctx, nil); err == nil {
			t.Fatal("txm.RegisterClientTransaction(ctx, nil) error = nil, want error")
		}
		var nilTx *sip.InviteClientTransaction
		if err := txm.RegisterClientTransaction(ctx, nilTx); err == nil {
			t.Fatal("txm.RegisterClientTransaction(ctx, typed nil) error = nil, want error")
		}

		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		if err := txm.RegisterClientTransaction(ctx, tx); err != nil {
			t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want nil", err)
		}
		tp.ensureNoSendReq(t)

		stored, ok := txm.LoadClientTransaction(tx.Key())
		if !ok || stored != tx {
			t.Fatalf("txm.LoadClientTransaction() = %p, %v, want %p, true", stored, ok, tx)
		}
		res := newInRes(t, req, sip.ResponseStatusRinging)
		errCh := make(chan error, 1)
		go func() {
			errCh <- txm.InterceptInboundResponse(ctx, sip.ResponseReceiverFunc(
				func(context.Context, *sip.ResponseEnvelope) error {
					return errors.Wrap(sip.NewMessageNotMatched())
				},
			), res)
		}()
		time.Sleep(100 * time.Millisecond)
		select {
		case err := <-errCh:
			t.Fatalf("InterceptInboundResponse() = %v before Start, want blocked", err)
		default:
		}

		if err := txm.RegisterClientTransaction(ctx, tx); !errors.Is(err, sip.ErrTransactionDuplicate) {
			t.Fatalf("second txm.RegisterClientTransaction(ctx) error = %v, want %v", err, sip.ErrTransactionDuplicate)
		}
		dupTx, err := sip.NewInviteClientTransaction(req.Clone().(*sip.RequestEnvelope), tp) //nolint:forcetypeassert
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		if err := txm.RegisterClientTransaction(ctx, dupTx); !errors.Is(err, sip.ErrTransactionDuplicate) {
			t.Fatalf("txm.RegisterClientTransaction(ctx, dup) error = %v, want %v", err, sip.ErrTransactionDuplicate)
		}
		if err := dupTx.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("dupTx.Terminate() error = %v, want nil", err)
		}

		startClientTransaction(t, tx)
		if call := tp.waitSendReq(t); call.req.Method() != sip.RequestMethodInvite {
			t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
		}
		if err := <-errCh; err != nil {
			t.Fatalf("InterceptInboundResponse() error = %v, want nil", err)
		}
		waitForTransactState(t, tx, sip.TransactionStateProceeding, 100*time.Millisecond)

		actTx, err := sip.NewInviteClientTransaction(
			newOutInviteReq(t, "UDP", sip.MagicCookie+".reg-active", laddr, raddr), tp,
		)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, actTx)
		if err := txm.RegisterClientTransaction(ctx, actTx); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("txm.RegisterClientTransaction(ctx, active) error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}
		if err := actTx.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("actTx.Terminate() error = %v, want nil", err)
		}
		tp.drainSendReqs()

		termTx, err := sip.NewInviteClientTransaction(
			newOutInviteReq(t, "UDP", sip.MagicCookie+".reg-term", laddr, raddr), tp,
		)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		if err := termTx.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("termTx.Terminate() error = %v, want nil", err)
		}
		if err := txm.RegisterClientTransaction(ctx, termTx); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("txm.RegisterClientTransaction(ctx, terminated) error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}

		if err := tx.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}
		waitForTransactState(t, tx, sip.TransactionStateTerminated, 100*time.Millisecond)
		time.Sleep(10 * time.Millisecond)
		if _, ok := txm.LoadClientTransaction(tx.Key()); ok {
			t.Fatal("txm.LoadClientTransaction() found terminated transaction, want removed")
		}
	})
}

func TestTransactionManager_RegisterServerTransaction(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		defer func() {
			if err := txm.Close(ctx); err != nil {
				t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
			}
		}()

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubServerTransport(false)
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".reg-server", laddr, raddr)

		if err := txm.RegisterServerTransaction(ctx, nil); err == nil {
			t.Fatal("txm.RegisterServerTransaction(ctx, nil) error = nil, want error")
		}
		var nilTx *sip.InviteServerTransaction
		if err := txm.RegisterServerTransaction(ctx, nilTx); err == nil {
			t.Fatal("txm.RegisterServerTransaction(ctx, typed nil) error = nil, want error")
		}

		tx, err := sip.NewInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		if err := txm.RegisterServerTransaction(ctx, tx); err != nil {
			t.Fatalf("txm.RegisterServerTransaction(ctx) error = %v, want nil", err)
		}
		tp.ensureNoSendRes(t)

		stored, ok := txm.LoadServerTransaction(tx.Key())
		if !ok || stored != tx {
			t.Fatalf("txm.LoadServerTransaction() = %p, %v, want %p, true", stored, ok, tx)
		}

		if mergedKey, ok := txm.LookupMergedRequest(req); !ok || !mergedKey.Equal(tx.Key()) {
			t.Fatalf("txm.LookupMergedRequest() = %v, %v, want %v, true", mergedKey, ok, tx.Key())
		}

		if err := txm.RegisterServerTransaction(ctx, tx); !errors.Is(err, sip.ErrTransactionDuplicate) {
			t.Fatalf("second txm.RegisterServerTransaction(ctx) error = %v, want %v", err, sip.ErrTransactionDuplicate)
		}

		if err := tx.Terminate(ctx, errors.New("test cleanup")); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}
		waitForTransactState(t, tx, sip.TransactionStateTerminated, 100*time.Millisecond)
		time.Sleep(10 * time.Millisecond)
		if _, ok := txm.LoadServerTransaction(tx.Key()); ok {
			t.Fatal("txm.LoadServerTransaction() found terminated transaction, want removed")
		}
		if _, ok := txm.LookupMergedRequest(req); ok {
			t.Fatal("txm.LookupMergedRequest() found terminated transaction, want removed")
		}
	})
}

func TestTransactionManager_RegisterTransaction_WhenClosed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		if err := txm.Close(ctx); err != nil {
			t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
		}

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")

		clnTx, err := sip.NewInviteClientTransaction(
			newOutInviteReq(t, "UDP", sip.MagicCookie+".reg-closed-cln", laddr, raddr),
			newStubClientTransport(false),
		)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		if err := txm.RegisterClientTransaction(ctx, clnTx); !errors.Is(err, sip.ErrTransactionManagerClosed) {
			t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want %v", err, sip.ErrTransactionManagerClosed)
		}

		srvTx, err := sip.NewInviteServerTransaction(
			newInInviteReq(t, "UDP", sip.MagicCookie+".reg-closed-srv", laddr, raddr),
			newStubServerTransport(false),
		)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		if err := txm.RegisterServerTransaction(ctx, srvTx); !errors.Is(err, sip.ErrTransactionManagerClosed) {
			t.Fatalf("txm.RegisterServerTransaction(ctx) error = %v, want %v", err, sip.ErrTransactionManagerClosed)
		}
	})
}

func TestTransactionManager_RegisterCustomTransaction(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		defer func() {
			if err := txm.Close(ctx); err != nil {
				t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
			}
		}()

		tx := &stubClientTransaction{
			key: sip.ClientTransactionKey{Branch: sip.MagicCookie + ".custom", Method: "INVITE"},
		}
		if err := txm.RegisterClientTransaction(ctx, tx); err != nil {
			t.Fatalf("txm.RegisterClientTransaction(ctx, custom) error = %v, want nil", err)
		}
		stored, ok := txm.LoadClientTransaction(tx.Key())
		if !ok || stored != tx {
			t.Fatalf("txm.LoadClientTransaction() = %p, %v, want %p, true", stored, ok, tx)
		}
	})
}

func TestTransactionManager_Close_TerminatesDormantTransactions(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")

		clnTx, err := txm.NewClientTransaction(
			ctx, newOutInviteReq(t, "UDP", sip.MagicCookie+".dormant-close-cln", laddr, raddr),
			newStubClientTransport(false),
		)
		if err != nil {
			t.Fatalf("txm.NewClientTransaction() error = %v, want nil", err)
		}
		srvTx, err := txm.NewServerTransaction(
			ctx, newInInviteReq(t, "UDP", sip.MagicCookie+".dormant-close-srv", laddr, raddr),
			newStubServerTransport(false),
		)
		if err != nil {
			t.Fatalf("txm.NewServerTransaction() error = %v, want nil", err)
		}

		res := newInRes(t, clnTx.Request(), sip.ResponseStatusRinging)
		errCh := make(chan error, 1)
		go func() { errCh <- clnTx.RecvResponse(ctx, res) }()
		time.Sleep(100 * time.Millisecond)

		if err := txm.Close(ctx); err != nil {
			t.Fatalf("txm.Close(ctx) error = %v, want nil", err)
		}

		if err := <-errCh; !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("clnTx.RecvResponse() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}
		waitForTransactState(t, clnTx, sip.TransactionStateTerminated, 100*time.Millisecond)
		waitForTransactState(t, srvTx, sip.TransactionStateTerminated, 100*time.Millisecond)

		for tx := range txm.AllClientTransactions() {
			t.Fatalf("txm.AllClientTransactions() yielded %v, want empty", tx)
		}
		for tx := range txm.AllServerTransactions() {
			t.Fatalf("txm.AllServerTransactions() yielded %v, want empty", tx)
		}
	})
}

func TestTransactionManager_ConcurrentStartClose(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubClientTransport(false)

		tx, err := txm.NewClientTransaction(
			ctx, newOutInviteReq(t, "UDP", sip.MagicCookie+".concurrent-close", laddr, raddr), tp,
		)
		if err != nil {
			t.Fatalf("txm.NewClientTransaction() error = %v, want nil", err)
		}

		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() { _ = tx.Start(ctx) })
			wg.Go(func() { _ = txm.Close(ctx) })
			wg.Go(func() { _ = tx.Terminate(ctx, errors.New("test cleanup")) })
		}
		wg.Wait()

		waitForTransactState(t, tx, sip.TransactionStateTerminated, 100*time.Millisecond)
		for range txm.AllClientTransactions() {
			t.Fatal("txm.AllClientTransactions() yielded a transaction, want empty")
		}
	})
}

type closeOnBindClientTransaction struct {
	stubClientTransaction
	ctx context.Context
	txm *sip.TransactionManager
}

func (tx *closeOnBindClientTransaction) BindStateHandler(fn sip.TransactionStateHandler) func() {
	_ = tx.txm.Close(tx.ctx)
	return tx.stubClientTransaction.BindStateHandler(fn)
}

type replayTermClientTransaction struct {
	stubClientTransaction
	ctx          context.Context
	term         atomic.Bool
	stateUnbinds atomic.Int32
	startUnbinds atomic.Int32
}

func (tx *replayTermClientTransaction) State() sip.TransactionState {
	if tx.term.Load() {
		return sip.TransactionStateTerminated
	}
	return sip.TransactionStateCalling
}

func (tx *replayTermClientTransaction) BindStateHandler(fn sip.TransactionStateHandler) func() {
	unbind := tx.stubClientTransaction.BindStateHandler(fn)
	tx.term.Store(true)
	fn.HandleTransactionState(tx.ctx, sip.TransactionStateCalling, sip.TransactionStateTerminated)
	return func() {
		tx.stateUnbinds.Add(1)
		unbind()
	}
}

func (tx *replayTermClientTransaction) BindStartHandler(fn sip.TransactionStartHandler) func() {
	unbind := tx.stubClientTransaction.BindStartHandler(fn)
	return func() {
		tx.startUnbinds.Add(1)
		unbind()
	}
}

type stubServerTransaction struct {
	stubTransaction
	key sip.ServerTransactionKey
	req *sip.RequestEnvelope
}

func (tx *stubServerTransaction) Type() sip.TransactionType {
	if tx.typ.IsValid() {
		return tx.typ
	}
	return sip.TransactionTypeServerInvite
}

func (*stubServerTransaction) State() sip.TransactionState { return sip.TransactionStateProceeding }
func (tx *stubServerTransaction) Start(ctx context.Context) error {
	tx.start(ctx)
	return nil
}
func (tx *stubServerTransaction) Key() sip.ServerTransactionKey    { return tx.key }
func (tx *stubServerTransaction) Request() *sip.RequestEnvelope    { return tx.req }
func (*stubServerTransaction) LastResponse() *sip.ResponseEnvelope { return nil }
func (*stubServerTransaction) Transport() sip.ServerTransport      { return nil }
func (*stubServerTransaction) LastError() error                    { return nil }

func (*stubServerTransaction) RecvRequest(context.Context, *sip.RequestEnvelope) error {
	return nil
}

func (*stubServerTransaction) SendResponse(context.Context, *sip.ResponseEnvelope, ...sip.SendResponseOptions) error {
	return nil
}

func (*stubServerTransaction) Respond(context.Context, sip.ResponseStatus, ...sip.RespondOptions) error {
	return nil
}

type replayTermServerTransaction struct {
	stubServerTransaction
	ctx  context.Context
	term atomic.Bool
}

func (tx *replayTermServerTransaction) State() sip.TransactionState {
	if tx.term.Load() {
		return sip.TransactionStateTerminated
	}
	return sip.TransactionStateProceeding
}

func (tx *replayTermServerTransaction) BindStateHandler(fn sip.TransactionStateHandler) func() {
	unbind := tx.stubServerTransaction.BindStateHandler(fn)
	tx.term.Store(true)
	fn.HandleTransactionState(tx.ctx, sip.TransactionStateProceeding, sip.TransactionStateTerminated)
	return unbind
}

func TestTransactionManager_RegisterSameBuiltinTwice(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txmA := &sip.TransactionManager{}
		txmB := &sip.TransactionManager{}
		defer func() { _ = txmB.Close(ctx) }()

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".reg-twice", laddr, raddr)

		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		if err := txmA.RegisterClientTransaction(ctx, tx); err != nil {
			t.Fatalf("txmA.RegisterClientTransaction(ctx) error = %v, want nil", err)
		}

		if err := txmA.RegisterClientTransaction(ctx, tx); !errors.Is(err, sip.ErrTransactionDuplicate) {
			t.Fatalf("repeated txmA.RegisterClientTransaction(ctx) error = %v, want %v", err, sip.ErrTransactionDuplicate)
		}

		if err := txmB.RegisterClientTransaction(ctx, tx); err != nil {
			t.Fatalf("txmB.RegisterClientTransaction(ctx) error = %v, want nil", err)
		}
		if stored, ok := txmB.LoadClientTransaction(tx.Key()); !ok || stored != tx {
			t.Fatalf("txmB.LoadClientTransaction() = %p, %v, want %p, true", stored, ok, tx)
		}

		if err := txmA.Close(ctx); err != nil {
			t.Fatalf("txmA.Close() error = %v, want nil", err)
		}
		waitForTransactState(t, tx, sip.TransactionStateTerminated, 100*time.Millisecond)
		if _, ok := txmB.LoadClientTransaction(tx.Key()); ok {
			t.Fatal("txmB.LoadClientTransaction() found terminated shared transaction, want removed")
		}
	})
}

func TestTransactionManager_RegisterRejectedDuplicateRetained(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txmA := &sip.TransactionManager{}
		txmB := &sip.TransactionManager{}
		defer func() { _ = txmA.Close(ctx) }()
		defer func() { _ = txmB.Close(ctx) }()

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".reg-retain", laddr, raddr)

		tx1, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		if err := txmA.RegisterClientTransaction(ctx, tx1); err != nil {
			t.Fatalf("txmA.RegisterClientTransaction(ctx, tx1) error = %v, want nil", err)
		}

		dupReq := req.Clone().(*sip.RequestEnvelope) //nolint:forcetypeassert
		tx2, err := sip.NewInviteClientTransaction(dupReq, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		if err := txmA.RegisterClientTransaction(ctx, tx2); !errors.Is(err, sip.ErrTransactionDuplicate) {
			t.Fatalf("txmA.RegisterClientTransaction(ctx, tx2) error = %v, want %v", err, sip.ErrTransactionDuplicate)
		}

		if err := txmB.RegisterClientTransaction(ctx, tx2); err != nil {
			t.Fatalf("txmB.RegisterClientTransaction(ctx, tx2) error = %v, want nil", err)
		}
		stored, ok := txmB.LoadClientTransaction(tx2.Key())
		if !ok || stored != tx2 {
			t.Fatalf("txmB.LoadClientTransaction() = %p, %v, want %p, true", stored, ok, tx2)
		}
	})
}

func TestTransactionManager_RegisterBindReentrantClose(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	txm := &sip.TransactionManager{}

	tx := &closeOnBindClientTransaction{ctx: ctx, txm: txm}
	tx.typ = sip.TransactionTypeClientInvite
	tx.key = sip.ClientTransactionKey{Branch: sip.MagicCookie + ".bind-close", Method: "INVITE"}

	if err := txm.RegisterClientTransaction(ctx, tx); !errors.Is(err, sip.ErrTransactionManagerClosed) {
		t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want %v", err, sip.ErrTransactionManagerClosed)
	}
	if _, ok := txm.LoadClientTransaction(tx.key); ok {
		t.Fatal("txm.LoadClientTransaction() found transaction after close, want removed")
	}
}

func TestTransactionManager_RegisterPendingStateReplay(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	txm := &sip.TransactionManager{}
	defer func() { _ = txm.Close(ctx) }()

	tx := &replayTermClientTransaction{ctx: ctx}
	tx.typ = sip.TransactionTypeClientInvite
	tx.key = sip.ClientTransactionKey{Branch: sip.MagicCookie + ".replay-term", Method: "INVITE"}

	if err := txm.RegisterClientTransaction(ctx, tx); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
		t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want %v",
			err, sip.ErrTransactionActionNotAllowed)
	}
	if _, ok := txm.LoadClientTransaction(tx.key); ok {
		t.Fatal("txm.LoadClientTransaction() found terminated transaction, want removed")
	}
	if tx.stateUnbinds.Load() != 1 || tx.startUnbinds.Load() != 1 {
		t.Fatalf("unbinds after rejection = %d state, %d start, want 1, 1",
			tx.stateUnbinds.Load(), tx.startUnbinds.Load())
	}
}

type closeOnStartBindClientTransaction struct {
	stubClientTransaction
	ctx context.Context
	txm *sip.TransactionManager
}

func (tx *closeOnStartBindClientTransaction) BindStartHandler(
	fn sip.TransactionStartHandler,
) func() {
	_ = tx.txm.Close(tx.ctx)
	return tx.stubClientTransaction.BindStartHandler(fn)
}

func TestTransactionManager_RegisterStartBindReentrantClose(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	txm := &sip.TransactionManager{}

	tx := &closeOnStartBindClientTransaction{ctx: ctx, txm: txm}
	tx.typ = sip.TransactionTypeClientInvite
	tx.key = sip.ClientTransactionKey{Branch: sip.MagicCookie + ".lc-bind-close", Method: "INVITE"}

	if err := txm.RegisterClientTransaction(ctx, tx); !errors.Is(err, sip.ErrTransactionManagerClosed) {
		t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want %v", err, sip.ErrTransactionManagerClosed)
	}
	if _, ok := txm.LoadClientTransaction(tx.key); ok {
		t.Fatal("txm.LoadClientTransaction() found transaction after close, want removed")
	}
}

func TestTransactionManager_CloseOneManagerKeepsOther(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txmA := &sip.TransactionManager{}
		txmB := &sip.TransactionManager{}
		defer func() { _ = txmB.Close(ctx) }()

		laddr := netip.MustParseAddrPort("11.11.11.11:5070")
		raddr := netip.MustParseAddrPort("55.55.55.55:5060")

		shared, err := sip.NewInviteServerTransaction(
			newInInviteReq(t, "UDP", sip.MagicCookie+".close-shared", laddr, raddr),
			newStubServerTransport(false),
		)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		if err := txmA.RegisterServerTransaction(ctx, shared); err != nil {
			t.Fatalf("txmA.RegisterServerTransaction(ctx) error = %v, want nil", err)
		}
		if err := txmB.RegisterServerTransaction(ctx, shared); err != nil {
			t.Fatalf("txmB.RegisterServerTransaction(ctx) error = %v, want nil", err)
		}

		other, err := sip.NewInviteServerTransaction(
			newInInviteReq(t, "UDP", sip.MagicCookie+".close-other", laddr, raddr),
			newStubServerTransport(false),
		)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		if err := txmB.RegisterServerTransaction(ctx, other); err != nil {
			t.Fatalf("txmB.RegisterServerTransaction(ctx, other) error = %v, want nil", err)
		}

		if err := txmA.Close(ctx); err != nil {
			t.Fatalf("txmA.Close() error = %v, want nil", err)
		}
		if _, ok := txmA.LoadServerTransaction(shared.Key()); ok {
			t.Fatal("txmA still holds shared tx after Close, want removed")
		}
		if got := shared.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("shared.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}
		if _, ok := txmB.LoadServerTransaction(shared.Key()); ok {
			t.Fatal("txmB still holds terminated shared tx, want removed")
		}
		if _, ok := txmB.LoadServerTransaction(other.Key()); !ok {
			t.Fatal("txmB lost unrelated transaction after txmA close, want kept")
		}
	})
}

func TestTransactionManager_NewServerTransactionFactoryReturnsExisting(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubServerTransport(false)
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".factory-dup", laddr, raddr)

		existing, err := sip.NewInviteServerTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}

		txm := &sip.TransactionManager{
			ServerTransactionFactory: sip.ServerTransactionFactoryFunc(
				func(*sip.RequestEnvelope, sip.ServerTransport, ...sip.ServerTransactionOptions) (sip.ServerTransaction, error) {
					return existing, nil
				},
			),
		}
		defer func() { _ = txm.Close(ctx) }()

		if err := txm.RegisterServerTransaction(ctx, existing); err != nil {
			t.Fatalf("txm.RegisterServerTransaction(ctx) error = %v, want nil", err)
		}

		tx, err := txm.NewServerTransaction(ctx, req, tp)
		if !errors.Is(err, sip.ErrTransactionDuplicate) {
			t.Fatalf("txm.NewServerTransaction() error = %v, want %v", err, sip.ErrTransactionDuplicate)
		}
		if tx != nil {
			t.Fatalf("txm.NewServerTransaction() tx = %p, want nil", tx)
		}
		if existing.State() == sip.TransactionStateTerminated {
			t.Fatal("existing transaction terminated by rejected factory result, want alive")
		}
	})
}

func TestTransactionManager_NewCancelClientTransaction_NilInvite(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	txm := &sip.TransactionManager{}
	defer func() { _ = txm.Close(ctx) }()

	if _, err := txm.NewCancelClientTransaction(ctx, nil); err == nil {
		t.Fatal("txm.NewCancelClientTransaction(nil) error = nil, want error")
	}

	var nilTx *sip.InviteClientTransaction
	if _, err := txm.NewCancelClientTransaction(ctx, nilTx); err == nil {
		t.Fatal("txm.NewCancelClientTransaction(typed nil) error = nil, want error")
	}
}

func TestTransactionManager_WatchdogArmsOnActivation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{StaleTransactionTimeout: time.Second}
		defer func() { _ = txm.Close(ctx) }()

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".watchdog", laddr, raddr)

		tx, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, tx)
		tp.waitSendReq(t)
		if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("tx.RecvResponse(180) error = %v, want nil", err)
		}
		waitForTransactState(t, tx, sip.TransactionStateProceeding, 100*time.Millisecond)

		snap := mustClientSnapshot(t, tx)
		restoredTP := newStubClientTransport(false)
		restored, err := sip.RestoreInviteClientTransaction(snap, restoredTP)
		if err != nil {
			t.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
		}
		if err := txm.RegisterClientTransaction(ctx, restored); err != nil {
			t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want nil", err)
		}

		time.Sleep(2 * time.Second)
		if got := restored.State(); got != sip.TransactionStateProceeding {
			t.Fatalf("dormant restored.State() = %q, want %q", got, sip.TransactionStateProceeding)
		}

		startClientTransaction(t, restored)
		waitForTransactState(t, restored, sip.TransactionStateTerminated, 2*time.Second)
	})
}

func TestTransactionManager_NewCancelClientTransaction_WaitCalling(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		defer func() { _ = txm.Close(ctx) }()

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".cancel-wait", laddr, raddr)

		invTx, err := txm.NewClientTransaction(ctx, req, tp)
		if err != nil {
			t.Fatalf("txm.NewClientTransaction() error = %v, want nil", err)
		}
		startClientTransaction(t, invTx)
		tp.waitSendReq(t)

		ctxC, cancel := context.WithCancel(ctx)
		errCh := make(chan error, 1)
		go func() {
			_, err := txm.NewCancelClientTransaction(ctxC, invTx)
			errCh <- err
		}()

		time.Sleep(100 * time.Millisecond)
		cancel()
		if err := <-errCh; !errors.Is(err, context.Canceled) {
			t.Fatalf("txm.NewCancelClientTransaction() error = %v, want %v", err, context.Canceled)
		}

		errCh = make(chan error, 1)
		go func() {
			_, err := txm.NewCancelClientTransaction(ctx, invTx)
			errCh <- err
		}()

		time.Sleep(100 * time.Millisecond)
		if err := invTx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusBusyHere)); err != nil {
			t.Fatalf("invTx.RecvResponse(486) error = %v, want nil", err)
		}
		if err := <-errCh; !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("txm.NewCancelClientTransaction() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
		}
	})
}

type replayProceedingClientTransaction struct {
	stubClientTransaction
	ctx       context.Context
	proceed   atomic.Bool
	term      atomic.Bool
	termCause error
}

func (tx *replayProceedingClientTransaction) State() sip.TransactionState {
	if tx.term.Load() {
		return sip.TransactionStateTerminated
	}
	if tx.proceed.Load() {
		return sip.TransactionStateProceeding
	}
	return sip.TransactionStateCalling
}

func (tx *replayProceedingClientTransaction) Terminate(_ context.Context, cause error) error {
	tx.term.Store(true)
	tx.termCause = cause
	return nil
}

func (tx *replayProceedingClientTransaction) BindStateHandler(fn sip.TransactionStateHandler) func() {
	unbind := tx.stubClientTransaction.BindStateHandler(fn)
	tx.proceed.Store(true)
	fn.HandleTransactionState(tx.ctx, sip.TransactionStateCalling, sip.TransactionStateProceeding)
	return unbind
}

func TestTransactionManager_RegisterRejectedClosesWatcher(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{StaleTransactionTimeout: time.Second}
		defer func() { _ = txm.Close(ctx) }()

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".reject-watch", laddr, raddr)

		tx, err := txm.NewClientTransaction(ctx, req, tp)
		if err != nil {
			t.Fatalf("txm.NewClientTransaction() error = %v, want nil", err)
		}

		custom := &replayProceedingClientTransaction{ctx: ctx}
		custom.key = sip.ClientTransactionKey{Branch: tx.Key().Branch, Method: tx.Key().Method}

		if err := txm.RegisterClientTransaction(ctx, custom); !errors.Is(err, sip.ErrTransactionDuplicate) {
			t.Fatalf("txm.RegisterClientTransaction(ctx, custom) error = %v, want %v",
				err, sip.ErrTransactionDuplicate)
		}

		time.Sleep(10 * time.Second)
		if custom.term.Load() {
			t.Fatalf("rejected transaction terminated with %v, want alive", custom.termCause)
		}
	})
}

func TestTransactionManager_NewClientTransactionFactoryReturnsActiveExisting(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubClientTransport(false)
		req := newOutInviteReq(t, "UDP", sip.MagicCookie+".factory-active", laddr, raddr)

		existing, err := sip.NewInviteClientTransaction(req, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		txm := &sip.TransactionManager{
			ClientTransactionFactory: sip.ClientTransactionFactoryFunc(
				func(*sip.RequestEnvelope, sip.ClientTransport, ...sip.ClientTransactionOptions) (sip.ClientTransaction, error) {
					return existing, nil
				},
			),
		}
		defer func() { _ = txm.Close(ctx) }()

		if err := txm.RegisterClientTransaction(ctx, existing); err != nil {
			t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want nil", err)
		}
		startClientTransaction(t, existing)
		tp.waitSendReq(t)

		tx, err := txm.NewClientTransaction(ctx, req, tp)
		if !errors.Is(err, sip.ErrTransactionDuplicate) {
			t.Fatalf("txm.NewClientTransaction() error = %v, want %v", err, sip.ErrTransactionDuplicate)
		}
		if tx != nil {
			t.Fatalf("txm.NewClientTransaction() tx = %p, want nil", tx)
		}
		if existing.State() == sip.TransactionStateTerminated {
			t.Fatal("existing transaction terminated by rejected factory result, want alive")
		}
		if stored, ok := txm.LoadClientTransaction(existing.Key()); !ok || stored != existing {
			t.Fatalf("txm.LoadClientTransaction() = %p, %v, want %p, true", stored, ok, existing)
		}
	})
}

func TestTransactionManager_RegisterRejectedPreservesMergedKey(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		defer func() { _ = txm.Close(ctx) }()

		raddr := netip.MustParseAddrPort("192.168.1.100:5060")
		laddr := netip.MustParseAddrPort("0.0.0.0:5060")
		tp := newStubServerTransport(false)
		reqA := newInInviteReq(t, "UDP", sip.MagicCookie+".merged-a", laddr, raddr)

		txA, err := sip.NewInviteServerTransaction(reqA, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		if err := txm.RegisterServerTransaction(ctx, txA); err != nil {
			t.Fatalf("txm.RegisterServerTransaction(ctx, txA) error = %v, want nil", err)
		}

		cloneReq := func(branch string) *sip.RequestEnvelope {
			req := reqA.Clone().(*sip.RequestEnvelope) //nolint:forcetypeassert
			req.WithMessage(func(msg *sip.Request) {
				via, _ := msg.Headers.FirstVia()
				via.Params.Set("branch", branch)
			})
			return req
		}

		assertMerged := func(want sip.ServerTransactionKey) {
			t.Helper()
			key, ok := txm.LookupMergedRequest(reqA.Message())
			if !ok || !key.Equal(want) {
				t.Fatalf("txm.LookupMergedRequest(reqA) = %v, %v, want %v, true", key, ok, want)
			}
		}

		reqB := cloneReq(sip.MagicCookie + ".merged-b")
		txB, err := sip.NewInviteServerTransaction(reqB, tp)
		if err != nil {
			t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
		}
		startServerTransaction(t, txB)

		if err := txm.RegisterServerTransaction(ctx, txB); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("txm.RegisterServerTransaction(ctx, started txB) error = %v, want %v",
				err, sip.ErrTransactionActionNotAllowed)
		}
		assertMerged(txA.Key())
		if _, ok := txm.LoadServerTransaction(txB.Key()); ok {
			t.Fatal("txm.LoadServerTransaction(txB) found rejected transaction, want absent")
		}

		custom := &replayTermServerTransaction{ctx: ctx}
		custom.typ = sip.TransactionTypeServerInvite
		custom.key = sip.ServerTransactionKey{
			Branch: sip.MagicCookie + ".merged-c", SentBy: laddr.String(), Method: "INVITE",
		}
		custom.req = cloneReq(sip.MagicCookie + ".merged-c")

		if err := txm.RegisterServerTransaction(ctx, custom); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("txm.RegisterServerTransaction(ctx, terminating custom) error = %v, want %v",
				err, sip.ErrTransactionActionNotAllowed)
		}
		assertMerged(txA.Key())
		if _, ok := txm.LoadServerTransaction(custom.key); ok {
			t.Fatal("txm.LoadServerTransaction(custom) found rejected transaction, want absent")
		}

		if err := txB.Terminate(ctx, errors.ErrorWrap("done")); err != nil {
			t.Fatalf("txB.Terminate() error = %v, want nil", err)
		}
	})
}

type customTransactionCore struct {
	typ sip.TransactionType

	mu      sync.Mutex
	state   sip.TransactionState
	started bool
	hseq    int
	states  map[int]sip.TransactionStateHandler
	starts  map[int]sip.TransactionStartHandler

	termErr      error
	termCalls    atomic.Int32
	failTerm     atomic.Bool
	stateUnbinds atomic.Int32
	startUnbinds atomic.Int32
}

func (tx *customTransactionCore) Type() sip.TransactionType { return tx.typ }

func (tx *customTransactionCore) State() sip.TransactionState {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.state
}

func (tx *customTransactionCore) Started() bool {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.started
}

func (*customTransactionCore) MatchMessage(sip.Message) bool { return true }
func (*customTransactionCore) LastError() error              { return nil }

func (tx *customTransactionCore) BindStateHandler(fn sip.TransactionStateHandler) func() {
	tx.mu.Lock()
	tx.hseq++
	id := tx.hseq
	if tx.states == nil {
		tx.states = map[int]sip.TransactionStateHandler{}
	}
	tx.states[id] = fn
	tx.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			tx.mu.Lock()
			delete(tx.states, id)
			tx.mu.Unlock()
			tx.stateUnbinds.Add(1)
		})
	}
}

func (tx *customTransactionCore) BindStartHandler(fn sip.TransactionStartHandler) func() {
	tx.mu.Lock()
	tx.hseq++
	id := tx.hseq
	if tx.starts == nil {
		tx.starts = map[int]sip.TransactionStartHandler{}
	}
	tx.starts[id] = fn
	tx.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			tx.mu.Lock()
			delete(tx.starts, id)
			tx.mu.Unlock()
			tx.startUnbinds.Add(1)
		})
	}
}

func (tx *customTransactionCore) liveHandlers() (states, starts int) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return len(tx.states), len(tx.starts)
}

func (*customTransactionCore) BindErrorHandler(sip.ErrorHandler) func() {
	return func() {}
}

func (tx *customTransactionCore) fireState(ctx context.Context, from, to sip.TransactionState) {
	tx.mu.Lock()
	tx.state = to
	hdlrs := slices.Collect(maps.Values(tx.states))
	tx.mu.Unlock()
	for _, fn := range hdlrs {
		fn.HandleTransactionState(ctx, from, to)
	}
}

func (tx *customTransactionCore) start(ctx context.Context) error {
	tx.mu.Lock()
	if tx.started {
		tx.mu.Unlock()
		return errors.Wrap(sip.NewTransactionActionNotAllowedError())
	}
	tx.started = true
	hdlrs := slices.Collect(maps.Values(tx.starts))
	tx.mu.Unlock()
	for _, fn := range hdlrs {
		fn.HandleTransactionStart(ctx)
	}
	return nil
}

func (tx *customTransactionCore) lastTermErr() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.termErr
}

func (tx *customTransactionCore) terminate(ctx context.Context, err error) error {
	if tx.failTerm.Load() {
		return errors.ErrorWrap("terminate failed")
	}
	tx.termCalls.Add(1)
	if err != nil {
		tx.mu.Lock()
		tx.termErr = err
		tx.mu.Unlock()
	}
	if tx.State() != sip.TransactionStateTerminated {
		tx.fireState(ctx, tx.State(), sip.TransactionStateTerminated)
	}
	return nil
}

type customClientTransaction struct {
	customTransactionCore
	key sip.ClientTransactionKey
	req *sip.RequestEnvelope
	tp  sip.ClientTransport
}

func (tx *customClientTransaction) Start(ctx context.Context) error { return tx.start(ctx) }
func (tx *customClientTransaction) Terminate(ctx context.Context, err error) error {
	return tx.terminate(ctx, err)
}
func (tx *customClientTransaction) Key() sip.ClientTransactionKey    { return tx.key }
func (tx *customClientTransaction) Request() *sip.RequestEnvelope    { return tx.req }
func (*customClientTransaction) LastResponse() *sip.ResponseEnvelope { return nil }
func (tx *customClientTransaction) Transport() sip.ClientTransport   { return tx.tp }
func (*customClientTransaction) RecvResponse(context.Context, *sip.ResponseEnvelope) error {
	return nil
}

func (*customClientTransaction) BindResponseHandler(sip.InboundResponseHandler) func() {
	return func() {}
}

type customServerTransaction struct {
	customTransactionCore
	key sip.ServerTransactionKey
	req *sip.RequestEnvelope
	tp  sip.ServerTransport
}

func (tx *customServerTransaction) Start(ctx context.Context) error { return tx.start(ctx) }
func (tx *customServerTransaction) Terminate(ctx context.Context, err error) error {
	return tx.terminate(ctx, err)
}
func (tx *customServerTransaction) Key() sip.ServerTransactionKey    { return tx.key }
func (tx *customServerTransaction) Request() *sip.RequestEnvelope    { return tx.req }
func (*customServerTransaction) LastResponse() *sip.ResponseEnvelope { return nil }
func (tx *customServerTransaction) Transport() sip.ServerTransport   { return tx.tp }
func (*customServerTransaction) RecvRequest(context.Context, *sip.RequestEnvelope) error {
	return nil
}

func (*customServerTransaction) SendResponse(context.Context, *sip.ResponseEnvelope, ...sip.SendResponseOptions) error {
	return nil
}

func (*customServerTransaction) Respond(context.Context, sip.ResponseStatus, ...sip.RespondOptions) error {
	return nil
}

func newCustomClientTx(t *testing.T, branch string, state sip.TransactionState) *customClientTransaction {
	t.Helper()
	laddr := netip.MustParseAddrPort("11.11.11.11:5070")
	raddr := netip.MustParseAddrPort("55.55.55.55:5060")
	req := newOutInviteReq(t, "UDP", branch, laddr, raddr)
	key, err := sip.MakeClientTransactionKey(req)
	if err != nil {
		t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
	}
	tx := &customClientTransaction{key: key, req: req, tp: newStubClientTransport(false)}
	tx.typ = sip.TransactionTypeClientInvite
	tx.state = state
	return tx
}

func newCustomServerTx(t *testing.T, branch string, state sip.TransactionState) *customServerTransaction {
	t.Helper()
	laddr := netip.MustParseAddrPort("11.11.11.11:5070")
	raddr := netip.MustParseAddrPort("55.55.55.55:5060")
	req := newInInviteReq(t, "UDP", branch, laddr, raddr)
	key, err := sip.MakeServerTransactionKey(req)
	if err != nil {
		t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
	}
	tx := &customServerTransaction{key: key, req: req, tp: newStubServerTransport(false)}
	tx.typ = sip.TransactionTypeServerInvite
	tx.state = state
	return tx
}

func TestTransactionManager_CustomClientTransactionWatchdog(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{StaleTransactionTimeout: 100 * time.Millisecond}
		defer func() { _ = txm.Close(ctx) }()

		tx := newCustomClientTx(t, sip.MagicCookie+".custom-wd", sip.TransactionStateProceeding)
		if err := txm.RegisterClientTransaction(ctx, tx); err != nil {
			t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want nil", err)
		}

		time.Sleep(200 * time.Millisecond)
		if got := tx.termCalls.Load(); got != 0 {
			t.Fatalf("custom tx terminated %d times while dormant, want 0", got)
		}

		if err := tx.Start(ctx); err != nil {
			t.Fatalf("custom.Start() error = %v, want nil", err)
		}
		time.Sleep(200 * time.Millisecond)
		if got := tx.termCalls.Load(); got != 1 {
			t.Fatalf("custom tx terminated %d times, want 1 (stale watchdog)", got)
		}
		if !errors.Is(tx.lastTermErr(), sip.ErrTransactionTimedOut) {
			t.Fatalf("custom tx terminate error = %v, want %v", tx.lastTermErr(), sip.ErrTransactionTimedOut)
		}
		if _, ok := txm.LoadClientTransaction(tx.Key()); ok {
			t.Fatal("terminated custom tx still registered, want absent")
		}
		if tx.stateUnbinds.Load() != 1 || tx.startUnbinds.Load() != 1 {
			t.Fatalf("unbinds = %d state, %d start, want 1, 1",
				tx.stateUnbinds.Load(), tx.startUnbinds.Load())
		}
		if states, lcs := tx.liveHandlers(); states != 0 || lcs != 0 {
			t.Fatalf("live handlers after drop = %d state, %d start, want 0, 0", states, lcs)
		}
	})
}

func TestTransactionManager_CustomServerTransactionWatchdog(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{StaleTransactionTimeout: 100 * time.Millisecond}
		defer func() { _ = txm.Close(ctx) }()

		tx := newCustomServerTx(t, sip.MagicCookie+".custom-srv-wd", sip.TransactionStateProceeding)
		if err := txm.RegisterServerTransaction(ctx, tx); err != nil {
			t.Fatalf("txm.RegisterServerTransaction(ctx) error = %v, want nil", err)
		}

		time.Sleep(200 * time.Millisecond)
		if got := tx.termCalls.Load(); got != 0 {
			t.Fatalf("custom server tx terminated %d times while dormant, want 0", got)
		}

		if err := tx.Start(ctx); err != nil {
			t.Fatalf("custom.Start() error = %v, want nil", err)
		}
		time.Sleep(200 * time.Millisecond)
		if got := tx.termCalls.Load(); got != 1 {
			t.Fatalf("custom server tx terminated %d times, want 1 (stale watchdog)", got)
		}
		if !errors.Is(tx.lastTermErr(), sip.ErrTransactionTimedOut) {
			t.Fatalf("custom server tx terminate error = %v, want %v", tx.lastTermErr(), sip.ErrTransactionTimedOut)
		}
		if _, ok := txm.LoadServerTransaction(tx.Key()); ok {
			t.Fatal("terminated custom server tx still registered, want absent")
		}
	})
}

func TestTransactionManager_CustomServerTransactionCleanup(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		defer func() { _ = txm.Close(ctx) }()

		tx := newCustomServerTx(t, sip.MagicCookie+".custom-srv", sip.TransactionStateProceeding)
		if err := txm.RegisterServerTransaction(ctx, tx); err != nil {
			t.Fatalf("txm.RegisterServerTransaction(ctx) error = %v, want nil", err)
		}
		if _, ok := txm.LoadServerTransaction(tx.Key()); !ok {
			t.Fatal("custom server tx not found, want registered")
		}
		if key, ok := txm.LookupMergedRequest(tx.Request()); !ok || key != tx.Key() {
			t.Fatalf("LookupMergedRequest() = %q, %v, want %q, true", key, ok, tx.Key())
		}

		if err := tx.Terminate(ctx, errors.ErrorWrap("done")); err != nil {
			t.Fatalf("custom.Terminate() error = %v, want nil", err)
		}
		if _, ok := txm.LoadServerTransaction(tx.Key()); ok {
			t.Fatal("terminated custom server tx still registered, want absent")
		}
		if key, ok := txm.LookupMergedRequest(tx.Request()); ok {
			t.Fatalf("LookupMergedRequest() = %q, true after termination, want not found", key)
		}
		if tx.stateUnbinds.Load() != 1 || tx.startUnbinds.Load() != 1 {
			t.Fatalf("unbinds = %d state, %d start, want 1, 1",
				tx.stateUnbinds.Load(), tx.startUnbinds.Load())
		}
		if states, lcs := tx.liveHandlers(); states != 0 || lcs != 0 {
			t.Fatalf("live handlers after drop = %d state, %d start, want 0, 0", states, lcs)
		}
	})
}

func TestTransactionManager_CustomTransactionAdmissionRejected(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		started bool
		state   sip.TransactionState
	}{
		{"started", true, sip.TransactionStateProceeding},
		{"terminated-state", false, sip.TransactionStateTerminated},
	}
	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			txm := &sip.TransactionManager{}
			defer func() { _ = txm.Close(ctx) }()

			wantUnbinds := int32(1)
			if c.state == sip.TransactionStateTerminated {
				wantUnbinds = 0
			}

			cln := newCustomClientTx(t, sip.MagicCookie+".rej-cln-"+c.name, c.state)
			cln.started = c.started
			if err := txm.RegisterClientTransaction(ctx, cln); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
				t.Fatalf("RegisterClientTransaction(%s) error = %v, want %v",
					c.name, err, sip.ErrTransactionActionNotAllowed)
			}
			if _, ok := txm.LoadClientTransaction(cln.Key()); ok {
				t.Fatal("rejected custom client tx registered, want absent")
			}
			if cln.stateUnbinds.Load() != wantUnbinds || cln.startUnbinds.Load() != wantUnbinds {
				t.Fatalf("unbinds after rejection = %d state, %d start, want %d",
					cln.stateUnbinds.Load(), cln.startUnbinds.Load(), wantUnbinds)
			}

			srv := newCustomServerTx(t, sip.MagicCookie+".rej-srv-"+c.name, c.state)
			srv.started = c.started
			if err := txm.RegisterServerTransaction(ctx, srv); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
				t.Fatalf("RegisterServerTransaction(%s) error = %v, want %v",
					c.name, err, sip.ErrTransactionActionNotAllowed)
			}
			if _, ok := txm.LoadServerTransaction(srv.Key()); ok {
				t.Fatal("rejected custom server tx registered, want absent")
			}
			if key, ok := txm.LookupMergedRequest(srv.Request()); ok {
				t.Fatalf("LookupMergedRequest() = %q, true after rejection, want not found", key)
			}
			if srv.stateUnbinds.Load() != wantUnbinds || srv.startUnbinds.Load() != wantUnbinds {
				t.Fatalf("unbinds after rejection = %d state, %d start, want %d",
					srv.stateUnbinds.Load(), srv.startUnbinds.Load(), wantUnbinds)
			}
		})
	}
}

func TestTransactionManager_CustomTransactionTwoManagers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txmA := &sip.TransactionManager{StaleTransactionTimeout: time.Hour}
		defer func() { _ = txmA.Close(ctx) }()
		txmB := &sip.TransactionManager{StaleTransactionTimeout: 100 * time.Millisecond}
		defer func() { _ = txmB.Close(ctx) }()

		tx := newCustomClientTx(t, sip.MagicCookie+".custom-2m", sip.TransactionStateProceeding)
		if err := txmA.RegisterClientTransaction(ctx, tx); err != nil {
			t.Fatalf("txmA.RegisterClientTransaction(ctx) error = %v, want nil", err)
		}
		if err := txmB.RegisterClientTransaction(ctx, tx); err != nil {
			t.Fatalf("txmB.RegisterClientTransaction(ctx) error = %v, want nil", err)
		}
		if err := txmA.RegisterClientTransaction(ctx, tx); !errors.Is(err, sip.ErrTransactionDuplicate) {
			t.Fatalf("repeat txmA.RegisterClientTransaction(ctx) error = %v, want %v",
				err, sip.ErrTransactionDuplicate)
		}

		if err := tx.Start(ctx); err != nil {
			t.Fatalf("custom.Start() error = %v, want nil", err)
		}

		time.Sleep(200 * time.Millisecond)
		if got := tx.termCalls.Load(); got != 1 {
			t.Fatalf("custom tx terminated %d times, want 1 from short-timeout manager", got)
		}
		if !errors.Is(tx.lastTermErr(), sip.ErrTransactionTimedOut) {
			t.Fatalf("custom tx terminate error = %v, want %v", tx.lastTermErr(), sip.ErrTransactionTimedOut)
		}
		if _, ok := txmB.LoadClientTransaction(tx.Key()); ok {
			t.Fatal("terminated tx still in txmB, want removed")
		}
		if _, ok := txmA.LoadClientTransaction(tx.Key()); ok {
			t.Fatal("terminated tx still in txmA, want removed")
		}
	})
}

func TestTransactionManager_SharedBuiltinTransactionManagers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txmA := &sip.TransactionManager{StaleTransactionTimeout: time.Hour}
		defer func() { _ = txmA.Close(ctx) }()
		txmB := &sip.TransactionManager{StaleTransactionTimeout: 100 * time.Millisecond}
		defer func() { _ = txmB.Close(ctx) }()

		laddr := netip.MustParseAddrPort("11.11.11.11:5070")
		raddr := netip.MustParseAddrPort("55.55.55.55:5060")
		tp := newStubClientTransport(false)
		tx, err := sip.NewInviteClientTransaction(
			newOutInviteReq(t, "UDP", sip.MagicCookie+".shared-2m", laddr, raddr), tp,
		)
		if err != nil {
			t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
		}

		if err := txmA.RegisterClientTransaction(ctx, tx); err != nil {
			t.Fatalf("txmA.RegisterClientTransaction(ctx) error = %v, want nil", err)
		}
		if err := txmB.RegisterClientTransaction(ctx, tx); err != nil {
			t.Fatalf("txmB.RegisterClientTransaction(ctx) error = %v, want nil", err)
		}

		if err := tx.Start(ctx); err != nil {
			t.Fatalf("tx.Start() error = %v, want nil", err)
		}
		tp.waitSendReq(t)

		res := newInRes(t, tx.Request(), sip.ResponseStatusSessionProgress)
		if err := tx.RecvResponse(ctx, res); err != nil {
			t.Fatalf("tx.RecvResponse(183) error = %v, want nil", err)
		}
		if got := tx.State(); got != sip.TransactionStateProceeding {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateProceeding)
		}

		time.Sleep(200 * time.Millisecond)
		if got := tx.State(); got != sip.TransactionStateTerminated {
			t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
		}
		if _, ok := txmB.LoadClientTransaction(tx.Key()); ok {
			t.Fatal("terminated tx still in txmB, want removed")
		}
		if _, ok := txmA.LoadClientTransaction(tx.Key()); ok {
			t.Fatal("terminated tx still in txmA, want removed")
		}
	})
}

func TestTransactionManager_NewCancelClientTransactionCustom(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		defer func() { _ = txm.Close(ctx) }()

		invTx := newCustomClientTx(t, sip.MagicCookie+".custom-cancel", sip.TransactionStateProceeding)
		if _, err := txm.NewCancelClientTransaction(ctx, invTx); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("NewCancelClientTransaction(dormant) error = %v, want %v",
				err, sip.ErrTransactionActionNotAllowed)
		}

		if err := txm.RegisterClientTransaction(ctx, invTx); err != nil {
			t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want nil", err)
		}
		if err := invTx.Start(ctx); err != nil {
			t.Fatalf("invTx.Start() error = %v, want nil", err)
		}

		cnc, err := txm.NewCancelClientTransaction(ctx, invTx)
		if err != nil {
			t.Fatalf("NewCancelClientTransaction(active) error = %v, want nil", err)
		}
		if cnc.Started() {
			t.Fatal("cancel.Started() = true, want false")
		}
		invTx.tp.(*stubClientTransport).ensureNoSendReq(t)
	})
}

type nonCmpClientTransaction struct {
	meta  map[string]any
	inner *customClientTransaction
}

func (tx nonCmpClientTransaction) Type() sip.TransactionType       { return tx.inner.Type() }
func (tx nonCmpClientTransaction) State() sip.TransactionState     { return tx.inner.State() }
func (tx nonCmpClientTransaction) MatchMessage(m sip.Message) bool { return tx.inner.MatchMessage(m) }
func (tx nonCmpClientTransaction) LastError() error                { return tx.inner.LastError() }
func (tx nonCmpClientTransaction) Started() bool                   { return tx.inner.Started() }

func (tx nonCmpClientTransaction) BindStateHandler(fn sip.TransactionStateHandler) func() {
	return tx.inner.BindStateHandler(fn)
}

func (tx nonCmpClientTransaction) BindStartHandler(fn sip.TransactionStartHandler) func() {
	return tx.inner.BindStartHandler(fn)
}

func (tx nonCmpClientTransaction) BindErrorHandler(fn sip.ErrorHandler) func() {
	return tx.inner.BindErrorHandler(fn)
}

func (tx nonCmpClientTransaction) BindResponseHandler(fn sip.InboundResponseHandler) func() {
	return tx.inner.BindResponseHandler(fn)
}
func (tx nonCmpClientTransaction) Start(ctx context.Context) error { return tx.inner.Start(ctx) }
func (tx nonCmpClientTransaction) Terminate(ctx context.Context, err error) error {
	return tx.inner.Terminate(ctx, err)
}
func (tx nonCmpClientTransaction) Key() sip.ClientTransactionKey { return tx.inner.Key() }
func (tx nonCmpClientTransaction) Request() *sip.RequestEnvelope { return tx.inner.Request() }
func (tx nonCmpClientTransaction) LastResponse() *sip.ResponseEnvelope {
	return tx.inner.LastResponse()
}
func (tx nonCmpClientTransaction) Transport() sip.ClientTransport { return tx.inner.Transport() }
func (tx nonCmpClientTransaction) RecvResponse(ctx context.Context, res *sip.ResponseEnvelope) error {
	return tx.inner.RecvResponse(ctx, res)
}

func TestTransactionManager_RegisterNonComparableTransaction(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		defer func() { _ = txm.Close(ctx) }()

		tx := nonCmpClientTransaction{
			meta:  map[string]any{"tag": "noncmp"},
			inner: newCustomClientTx(t, sip.MagicCookie+".noncmp", sip.TransactionStateProceeding),
		}
		if err := txm.RegisterClientTransaction(ctx, tx); err != nil {
			t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want nil", err)
		}
		if got, ok := txm.LoadClientTransaction(tx.Key()); !ok {
			t.Fatal("non-comparable tx not found, want registered")
		} else {
			_ = got.Terminate(ctx, errors.ErrorWrap("done"))
		}
		if _, ok := txm.LoadClientTransaction(tx.Key()); ok {
			t.Fatal("terminated non-comparable tx still registered, want absent")
		}
	})
}

type captureHandlersClientTransaction struct {
	*customClientTransaction
	bound []sip.TransactionStateHandler
}

func (tx *captureHandlersClientTransaction) BindStateHandler(fn sip.TransactionStateHandler) func() {
	tx.bound = append(tx.bound, fn)
	return tx.customClientTransaction.BindStateHandler(fn)
}

func TestTransactionManager_DelayedRecordCallbackKeepsReplacement(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}
		defer func() { _ = txm.Close(ctx) }()

		first := &captureHandlersClientTransaction{
			customClientTransaction: newCustomClientTx(t, sip.MagicCookie+".delayed", sip.TransactionStateProceeding),
		}
		if err := txm.RegisterClientTransaction(ctx, first); err != nil {
			t.Fatalf("txm.RegisterClientTransaction(ctx, first) error = %v, want nil", err)
		}
		if len(first.bound) == 0 {
			t.Fatal("no state handler captured, want manager handler")
		}
		staleHdlr := first.bound[0]

		if err := first.Terminate(ctx, errors.ErrorWrap("done")); err != nil {
			t.Fatalf("first.Terminate() error = %v, want nil", err)
		}

		second := newCustomClientTx(t, sip.MagicCookie+".delayed", sip.TransactionStateProceeding)
		if err := txm.RegisterClientTransaction(ctx, second); err != nil {
			t.Fatalf("txm.RegisterClientTransaction(ctx, second) error = %v, want nil", err)
		}

		staleHdlr.HandleTransactionState(ctx, sip.TransactionStateProceeding, sip.TransactionStateTerminated)
		if got, ok := txm.LoadClientTransaction(second.Key()); !ok || got == first {
			t.Fatal("delayed old-record callback removed replacement, want kept")
		}
	})
}

func TestTransactionManager_CloseReleasesRegistrationOnTerminateError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}

		tx := &failTerminateClientTransaction{
			customClientTransaction: newCustomClientTx(t, sip.MagicCookie+".failterm", sip.TransactionStateProceeding),
		}
		if err := txm.RegisterClientTransaction(ctx, tx); err != nil {
			t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want nil", err)
		}

		if err := txm.Close(ctx); err == nil {
			t.Fatal("txm.Close() error = nil, want non-nil from failing Terminate")
		}
		if _, ok := txm.LoadClientTransaction(tx.Key()); ok {
			t.Fatal("tx still registered after Close with failed Terminate, want removed")
		}
		if tx.stateUnbinds.Load() != 1 || tx.startUnbinds.Load() != 1 {
			t.Fatalf("unbinds after close = %d state, %d start, want 1, 1",
				tx.stateUnbinds.Load(), tx.startUnbinds.Load())
		}
		if states, lcs := tx.liveHandlers(); states != 0 || lcs != 0 {
			t.Fatalf("live handlers after close = %d state, %d start, want 0, 0", states, lcs)
		}
	})
}

type failTerminateClientTransaction struct {
	*customClientTransaction
}

func (tx *failTerminateClientTransaction) Terminate(context.Context, error) error {
	tx.termCalls.Add(1)
	return errors.ErrorWrap("terminate failed")
}

func TestTransactionManager_CloseCleansMutatingServerTransaction(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{}

		laddr := netip.MustParseAddrPort("11.11.11.11:5070")

		tx := newCustomServerTx(t, sip.MagicCookie+".mutating", sip.TransactionStateProceeding)
		origKey := tx.key
		origReq := tx.req.Clone().(*sip.RequestEnvelope) //nolint:forcetypeassert

		if err := txm.RegisterServerTransaction(ctx, tx); err != nil {
			t.Fatalf("txm.RegisterServerTransaction(ctx) error = %v, want nil", err)
		}

		tx.req.WithMessage(func(r *sip.Request) {
			r.Headers.Set(header.CallID("mutated-call@bob.voip.com"))
		})
		tx.key = sip.ServerTransactionKey{
			Branch: sip.MagicCookie + ".mutated", SentBy: laddr.String(), Method: "INVITE",
		}
		tx.failTerm.Store(true)

		if key, ok := txm.LookupMergedRequest(origReq); !ok || !key.Equal(origKey) {
			t.Fatalf("LookupMergedRequest(origReq) = %q, %v, want %q, true", key, ok, origKey)
		}
		if _, ok := txm.LoadServerTransaction(origKey); !ok {
			t.Fatal("LoadServerTransaction(origKey) not found, want registered")
		}

		err := txm.Close(ctx)
		if err == nil {
			t.Fatal("txm.Close() error = nil, want non-nil from failing Terminate")
		}

		if n := slices.Collect(txm.AllServerTransactions()); len(n) != 0 {
			t.Fatalf("AllServerTransactions() = %d entries after Close, want 0", len(n))
		}
		if key, ok := txm.LookupMergedRequest(origReq); ok {
			t.Fatalf("LookupMergedRequest(origReq) = %q, true after Close, want not found", key)
		}
		if _, ok := txm.LoadServerTransaction(origKey); ok {
			t.Fatal("LoadServerTransaction(origKey) found tx after Close, want absent")
		}
		if tx.stateUnbinds.Load() != 1 || tx.startUnbinds.Load() != 1 {
			t.Fatalf("unbinds after close = %d state, %d start, want 1, 1",
				tx.stateUnbinds.Load(), tx.startUnbinds.Load())
		}
		if states, lcs := tx.liveHandlers(); states != 0 || lcs != 0 {
			t.Fatalf("live handlers after close = %d state, %d start, want 0, 0", states, lcs)
		}
	})
}

type blockedStartClientTransaction struct {
	*customClientTransaction
	entered chan struct{}
	gate    chan struct{}
	once    sync.Once
}

func (tx *blockedStartClientTransaction) Started() bool {
	captured := tx.customClientTransaction.Started()
	tx.once.Do(func() {
		close(tx.entered)
		<-tx.gate
	})
	return captured
}

func TestTransactionManager_RegisterStartDuringCandidateRead(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	txm := &sip.TransactionManager{}
	defer func() { _ = txm.Close(ctx) }()

	tx := &blockedStartClientTransaction{
		customClientTransaction: newCustomClientTx(
			t, sip.MagicCookie+".blocked-lc", sip.TransactionStateProceeding,
		),
		entered: make(chan struct{}),
		gate:    make(chan struct{}),
	}

	errCh := make(chan error, 1)
	go func() { errCh <- txm.RegisterClientTransaction(ctx, tx) }()

	<-tx.entered
	if err := tx.Start(ctx); err != nil {
		t.Fatalf("custom.Start() error = %v, want nil", err)
	}
	close(tx.gate)

	if err := <-errCh; !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
		t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want %v",
			err, sip.ErrTransactionActionNotAllowed)
	}
	if _, ok := txm.LoadClientTransaction(tx.Key()); ok {
		t.Fatal("registering started tx succeeded, want absent")
	}
}

type firingStartClientTransaction struct {
	*customClientTransaction
	fired atomic.Bool
}

func (tx *firingStartClientTransaction) Started() bool {
	if tx.fired.CompareAndSwap(false, true) {
		_ = tx.start(context.Background())
	}
	return tx.customClientTransaction.Started()
}

func TestTransactionManager_RegisterStartGetterFiresHandler(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	txm := &sip.TransactionManager{}
	defer func() { _ = txm.Close(ctx) }()

	tx := &firingStartClientTransaction{
		customClientTransaction: newCustomClientTx(
			t, sip.MagicCookie+".firing-lc", sip.TransactionStateProceeding,
		),
	}

	done := make(chan error, 1)
	go func() { done <- txm.RegisterClientTransaction(ctx, tx) }()

	select {
	case err := <-done:
		if !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
			t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want %v",
				err, sip.ErrTransactionActionNotAllowed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("txm.RegisterClientTransaction(ctx) deadlocked on reentrant Start getter")
	}
}

func newCustomNonInviteServerTx(t *testing.T, branch string, state sip.TransactionState) *customServerTransaction {
	t.Helper()
	laddr := netip.MustParseAddrPort("11.11.11.11:5070")
	raddr := netip.MustParseAddrPort("55.55.55.55:5060")
	req := newInNonInviteReq(t, "UDP", branch, laddr, raddr)
	key, err := sip.MakeServerTransactionKey(req)
	if err != nil {
		t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
	}
	tx := &customServerTransaction{key: key, req: req, tp: newStubServerTransport(false)}
	tx.typ = sip.TransactionTypeServerNonInvite
	tx.state = state
	return tx
}

func TestTransactionManager_StartTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{
			TransactionStartTimeout: time.Second,
			StaleTransactionTimeout: -1,
		}
		defer func() { _ = txm.Close(ctx) }()

		tx := newCustomClientTx(t, sip.MagicCookie+".start-timeout", sip.TransactionStateTrying)
		tx.typ = sip.TransactionTypeClientNonInvite
		if err := txm.RegisterClientTransaction(ctx, tx); err != nil {
			t.Fatalf("txm.RegisterClientTransaction(ctx, tx) error = %v, want nil", err)
		}

		time.Sleep(2 * time.Second)
		if got := tx.termCalls.Load(); got != 1 {
			t.Fatalf("tx terminate calls = %d, want 1", got)
		}
		if !errors.Is(tx.lastTermErr(), sip.ErrTransactionTimedOut) {
			t.Fatalf("tx termination error = %v, want %v", tx.lastTermErr(), sip.ErrTransactionTimedOut)
		}
		if _, ok := txm.LoadClientTransaction(tx.Key()); ok {
			t.Fatal("timed out unstarted transaction still registered")
		}
	})
}

func TestTransactionManager_StartCancelsStartTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{
			TransactionStartTimeout: time.Second,
			StaleTransactionTimeout: -1,
		}
		defer func() { _ = txm.Close(ctx) }()

		tx := newCustomClientTx(t, sip.MagicCookie+".start-timeout-cancel", sip.TransactionStateCalling)
		if err := txm.RegisterClientTransaction(ctx, tx); err != nil {
			t.Fatalf("txm.RegisterClientTransaction(ctx, tx) error = %v, want nil", err)
		}
		if err := tx.Start(ctx); err != nil {
			t.Fatalf("tx.Start(ctx) error = %v, want nil", err)
		}

		time.Sleep(2 * time.Second)
		if got := tx.termCalls.Load(); got != 0 {
			t.Fatalf("tx terminate calls = %d, want 0", got)
		}
		if _, ok := txm.LoadClientTransaction(tx.Key()); !ok {
			t.Fatal("started transaction removed after start timeout")
		}
	})
}

func TestTransactionManager_StaleDeadlineNotPostponed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, ctx context.Context, txm *sip.TransactionManager) *customTransactionCore
	}{
		{name: "client_invite", setup: func(t *testing.T, ctx context.Context, txm *sip.TransactionManager) *customTransactionCore {
			t.Helper()
			tx := newCustomClientTx(t, sip.MagicCookie+".wd-nodelay-cln", sip.TransactionStateProceeding)
			if err := txm.RegisterClientTransaction(ctx, tx); err != nil {
				t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want nil", err)
			}
			return &tx.customTransactionCore
		}},
		{name: "server_invite", setup: func(t *testing.T, ctx context.Context, txm *sip.TransactionManager) *customTransactionCore {
			t.Helper()
			tx := newCustomServerTx(t, sip.MagicCookie+".wd-nodelay-srv", sip.TransactionStateProceeding)
			if err := txm.RegisterServerTransaction(ctx, tx); err != nil {
				t.Fatalf("txm.RegisterServerTransaction(ctx) error = %v, want nil", err)
			}
			return &tx.customTransactionCore
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				txm := &sip.TransactionManager{StaleTransactionTimeout: time.Second}
				defer func() { _ = txm.Close(ctx) }()

				tx := tt.setup(t, ctx, txm)

				time.Sleep(2 * time.Second)
				if got := tx.termCalls.Load(); got != 0 {
					t.Fatalf("tx terminated %d times while dormant, want 0", got)
				}

				if err := tx.start(ctx); err != nil {
					t.Fatalf("tx.start() error = %v, want nil", err)
				}

				time.Sleep(600 * time.Millisecond)
				tx.fireState(ctx, sip.TransactionStateProceeding, sip.TransactionStateProceeding)

				time.Sleep(450 * time.Millisecond)
				if got := tx.termCalls.Load(); got != 1 {
					t.Fatalf("tx terminated %d times at original deadline, want 1", got)
				}
			})
		})
	}
}

func TestTransactionManager_ServerNonInviteKeepsStaleDeadline(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{StaleTransactionTimeout: time.Second}
		defer func() { _ = txm.Close(ctx) }()

		tx := newCustomNonInviteServerTx(t, sip.MagicCookie+".wd-noninv-srv", sip.TransactionStateTrying)
		if err := txm.RegisterServerTransaction(ctx, tx); err != nil {
			t.Fatalf("txm.RegisterServerTransaction(ctx) error = %v, want nil", err)
		}
		if err := tx.Start(ctx); err != nil {
			t.Fatalf("tx.Start() error = %v, want nil", err)
		}

		time.Sleep(600 * time.Millisecond)
		tx.fireState(ctx, sip.TransactionStateTrying, sip.TransactionStateProceeding)

		time.Sleep(450 * time.Millisecond)
		if got := tx.termCalls.Load(); got != 1 {
			t.Fatalf("tx terminated %d times at original deadline, want 1", got)
		}
		if !errors.Is(tx.lastTermErr(), sip.ErrTransactionTimedOut) {
			t.Fatalf("tx terminate error = %v, want %v", tx.lastTermErr(), sip.ErrTransactionTimedOut)
		}
	})
}

func TestTransactionManager_LeavingStaleStateCancelsWatchdog(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{StaleTransactionTimeout: time.Second}
		defer func() { _ = txm.Close(ctx) }()

		tx := newCustomClientTx(t, sip.MagicCookie+".wd-leave", sip.TransactionStateProceeding)
		if err := txm.RegisterClientTransaction(ctx, tx); err != nil {
			t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want nil", err)
		}
		if err := tx.Start(ctx); err != nil {
			t.Fatalf("tx.Start() error = %v, want nil", err)
		}

		time.Sleep(600 * time.Millisecond)
		tx.fireState(ctx, sip.TransactionStateProceeding, sip.TransactionStateCompleted)

		time.Sleep(2 * time.Second)
		if got := tx.termCalls.Load(); got != 0 {
			t.Fatalf("tx terminated %d times after leaving stale state, want 0", got)
		}
	})
}

func TestTransactionManager_NegativeStaleTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{StaleTransactionTimeout: -time.Second}
		defer func() { _ = txm.Close(ctx) }()

		tx := newCustomClientTx(t, sip.MagicCookie+".wd-neg", sip.TransactionStateProceeding)
		if err := txm.RegisterClientTransaction(ctx, tx); err != nil {
			t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want nil", err)
		}
		if err := tx.Start(ctx); err != nil {
			t.Fatalf("tx.Start() error = %v, want nil", err)
		}

		time.Sleep(10 * time.Second)
		if got := tx.termCalls.Load(); got != 0 {
			t.Fatalf("tx terminated %d times with disabled stale timeout, want 0", got)
		}
	})
}

func TestTransactionManager_CloseCancelsArmedStaleWatchdog(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		txm := &sip.TransactionManager{StaleTransactionTimeout: time.Second}

		tx := &failTerminateClientTransaction{
			customClientTransaction: newCustomClientTx(
				t, sip.MagicCookie+".wd-failterm", sip.TransactionStateProceeding,
			),
		}
		if err := txm.RegisterClientTransaction(ctx, tx); err != nil {
			t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want nil", err)
		}
		if err := tx.Start(ctx); err != nil {
			t.Fatalf("tx.Start() error = %v, want nil", err)
		}

		time.Sleep(600 * time.Millisecond)
		if err := txm.Close(ctx); err == nil {
			t.Fatal("txm.Close() error = nil, want non-nil from failing Terminate")
		}
		if got := tx.termCalls.Load(); got != 1 {
			t.Fatalf("tx.Terminate() calls = %d after close, want 1", got)
		}

		time.Sleep(2 * time.Second)
		if got := tx.termCalls.Load(); got != 1 {
			t.Fatalf("tx terminated %d times after close, want 1 (no stale watchdog fire)", got)
		}
		if !tx.Started() {
			t.Fatal("tx.Started() = false after failed terminate, want true")
		}
		if states, lcs := tx.liveHandlers(); states != 0 || lcs != 0 {
			t.Fatalf("live handlers after close = %d state, %d start, want 0, 0", states, lcs)
		}
	})
}
