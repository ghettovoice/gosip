package sip_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
)

type sendReqCall struct {
	ctx  context.Context
	req  *sip.OutboundRequestEnvelope
	opts *sip.SendRequestOptions
}

type stubClientTransport struct {
	reliable bool

	sendReqCalls chan sendReqCall

	mu          sync.Mutex
	sendReqHook func(sendReqCall, int) error
	sendReqCnt  int
}

func newStubClientTransport(reliable bool) *stubClientTransport {
	return &stubClientTransport{
		reliable:     reliable,
		sendReqCalls: make(chan sendReqCall, 64),
	}
}

func (tp *stubClientTransport) Reliable() bool {
	if tp == nil {
		return false
	}
	return tp.reliable
}

func (tp *stubClientTransport) SendRequest(
	ctx context.Context,
	req *sip.OutboundRequestEnvelope,
	opts *sip.SendRequestOptions,
) error {
	var reqCopy *sip.OutboundRequestEnvelope
	if req != nil {
		reqCopy = req.Clone().(*sip.OutboundRequestEnvelope) //nolint:forcetypeassert
	}

	call := sendReqCall{
		ctx:  ctx,
		req:  reqCopy,
		opts: cloneSendRequestOptions(opts),
	}

	tp.mu.Lock()
	idx := tp.sendReqCnt
	tp.sendReqCnt++
	hook := tp.sendReqHook
	tp.mu.Unlock()

	if hook != nil {
		if err := hook(call, idx); err != nil {
			return err
		}
	}

	tp.sendReqCalls <- call

	return nil
}

// func (tp *stubClientTransport) setSendReqHook(hook func(sendReqCall, int) error) {
// 	tp.mu.Lock()
// 	tp.sendReqHook = hook
// 	tp.mu.Unlock()
// }

func (tp *stubClientTransport) waitSendReq(tb testing.TB, timeout time.Duration) sendReqCall {
	tb.Helper()

	tmr := time.NewTimer(timeout)
	defer tmr.Stop()

	select {
	case call := <-tp.sendReqCalls:
		return call
	case <-tmr.C:
		tb.Fatalf("timed out waiting for request send call")
		return sendReqCall{}
	}
}

func (tp *stubClientTransport) ensureNoSendReq(tb testing.TB, timeout time.Duration) {
	tb.Helper()

	tmr := time.NewTimer(timeout)
	defer tmr.Stop()

	select {
	case call := <-tp.sendReqCalls:
		var mtd sip.RequestMethod
		if call.req != nil {
			mtd = call.req.Method()
		}

		tb.Fatalf("unexpected request send call with method %v", mtd)
	case <-tmr.C:
	}
}

// func (tp *stubClientTransport) sendReqChan() <-chan sendReqCall {
// 	return tp.sendReqCalls
// }

func (tp *stubClientTransport) drainSendReqs() {
	for {
		select {
		case <-tp.sendReqCalls:
		default:
			return
		}
	}
}

func cloneSendRequestOptions(opts *sip.SendRequestOptions) *sip.SendRequestOptions {
	if opts == nil {
		return nil
	}

	copyOpts := *opts

	return &copyOpts
}

func assertResponseStatus(tb testing.TB, resCh <-chan *sip.InboundResponseEnvelope, want sip.ResponseStatus) {
	tb.Helper()

	select {
	case res := <-resCh:
		if res.Status() != want {
			tb.Fatalf("res.Status = %v, want %v", res.Status(), want)
		}
	case <-time.After(100 * time.Millisecond):
		tb.Fatalf("response wait timeout, want %v", want)
	}
}

func TestInviteClientTransaction_Accepted(t *testing.T) {
	t.Parallel()

	// Use a slightly bigger T1 so timer A does not fire before we inject responses on slower/-race runs.
	t1 := 20 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	// Use an unreliable transport to keep timer A enabled and avoid retransmit races in tests.
	tp := newStubClientTransport(false)

	req := newOutInviteReq(t, "UDP", sip.MagicCookie+".client-accepted", local, remote)

	tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
	}

	call := tp.waitSendReq(t, 100*time.Millisecond)
	if call.req.Method() != sip.RequestMethodInvite {
		t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
	}

	if call.req.RemoteAddr() != remote {
		t.Fatalf("initial send remote addr = %v, want %v", call.req.RemoteAddr(), remote)
	}

	if got, want := tx.State(), sip.TransactionStateCalling; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	ctx := t.Context()

	resCh := make(chan *sip.InboundResponseEnvelope, 3)
	tx.OnResponse(func(_ context.Context, res *sip.InboundResponseEnvelope) {
		resCh <- res
	})

	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
	}

	if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	assertResponseStatus(t, resCh, sip.ResponseStatusRinging)
	tp.drainSendReqs()

	ok := newInRes(t, req, sip.ResponseStatusOK)
	if err := tx.RecvResponse(ctx, ok); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 200) error = %v, want nil", err)
	}

	if err := tx.RecvResponse(ctx, ok); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 200) error = %v, want nil", err)
	}

	if got, want := tx.State(), sip.TransactionStateAccepted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	assertResponseStatus(t, resCh, sip.ResponseStatusOK)
	tp.drainSendReqs()

	// additional 2xx keeps transaction accepted and delivers to TU
	secondOK := ok.Clone().(*sip.InboundResponseEnvelope) //nolint:forcetypeassert
	if err := tx.RecvResponse(ctx, secondOK); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 200 repeat) error = %v, want nil", err)
	}

	assertResponseStatus(t, resCh, sip.ResponseStatusOK)

	waitForTransactState(t, tx, sip.TransactionStateTerminated, timings.TimeM()+100*time.Millisecond)

	tp.ensureNoSendReq(t, 100*time.Millisecond)
}

func TestInviteClientTransaction_Rejected(t *testing.T) {
	t.Parallel()

	t1 := 20 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	tp := newStubClientTransport(false)

	req := newOutInviteReq(t, "UDP", sip.MagicCookie+".client-rejected", local, remote)

	tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
	}

	call := tp.waitSendReq(t, 100*time.Millisecond)
	if call.req.Method() != sip.RequestMethodInvite {
		t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
	}

	ctx := t.Context()

	resCh := make(chan *sip.InboundResponseEnvelope, 2)
	tx.OnResponse(func(_ context.Context, res *sip.InboundResponseEnvelope) {
		resCh <- res
	})

	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
	}

	assertResponseStatus(t, resCh, sip.ResponseStatusRinging)
	tp.drainSendReqs()

	decline := newInRes(t, req, sip.ResponseStatusDecline)
	if err := tx.RecvResponse(ctx, decline); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 603) error = %v, want nil", err)
	}

	assertResponseStatus(t, resCh, sip.ResponseStatusDecline)

	if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	ackCall := tp.waitSendReq(t, 100*time.Millisecond)
	if ackCall.req.Method() != sip.RequestMethodAck {
		t.Fatalf("sent %v, want ACK", ackCall.req.Method())
	}

	// Retransmitted final response should trigger another ACK send.
	secondDecline := decline.Clone().(*sip.InboundResponseEnvelope) //nolint:forcetypeassert
	if err := tx.RecvResponse(ctx, secondDecline); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 603 retransmit) error = %v, want nil", err)
	}

	retransAck := tp.waitSendReq(t, 100*time.Millisecond)
	if retransAck.req.Method() != sip.RequestMethodAck {
		t.Fatalf("sent %v, want ACK retransmit", retransAck.req.Method())
	}

	waitForTransactState(t, tx, sip.TransactionStateTerminated, timings.TimeD()+200*time.Millisecond)
	tp.ensureNoSendReq(t, 100*time.Millisecond)
}

func TestInviteClientTransaction_Timeout(t *testing.T) {
	t.Parallel()

	t1 := 5 * time.Millisecond
	timings := sip.NewTimings(t1, 4*t1, 6*t1, 32*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	tp := newStubClientTransport(false)

	req := newOutInviteReq(t, "UDP", sip.MagicCookie+".client-timeout", local, remote)

	tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
	}

	call := tp.waitSendReq(t, 100*time.Millisecond)
	if call.req.Method() != sip.RequestMethodInvite {
		t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInvite)
	}

	resCh := make(chan *sip.InboundResponseEnvelope, 1)
	tx.OnResponse(func(_ context.Context, res *sip.InboundResponseEnvelope) {
		resCh <- res
	})

	timeout := timings.TimeB() + 200*time.Millisecond
	waitForTransactState(t, tx, sip.TransactionStateTerminated, timeout)

	select {
	case res := <-resCh:
		t.Fatalf("unexpected response %v", res.Status())
	default:
	}

	if res := tx.LastResponse(); res != nil {
		t.Fatalf("tx.LastResponse() = %v, want nil", res.Status())
	}

	tp.drainSendReqs()
	tp.ensureNoSendReq(t, 50*time.Millisecond)
}

func TestInviteClientTransaction_RoundTripSnapshot(t *testing.T) {
	t.Parallel()

	t1 := 20 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, 2*t1)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	origTP := newStubClientTransport(true)
	req := newOutInviteReq(t, "TCP", sip.MagicCookie+".client-snapshot", local, remote)

	tx, err := sip.NewInviteClientTransaction(t.Context(), req, origTP, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
	}

	initial := origTP.waitSendReq(t, 100*time.Millisecond)
	if initial.req.Method() != sip.RequestMethodInvite {
		t.Fatalf("initial send method = %q, want %q", initial.req.Method(), sip.RequestMethodInvite)
	}

	origTP.drainSendReqs()

	ctx := t.Context()

	decline := newInRes(t, req, sip.ResponseStatusDecline)
	if err := tx.RecvResponse(ctx, decline); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 603) error = %v, want nil", err)
	}

	ackCall := origTP.waitSendReq(t, 100*time.Millisecond)
	if ackCall.req.Method() != sip.RequestMethodAck {
		t.Fatalf("sent %v, want ACK", ackCall.req.Method())
	}

	snap := tx.Snapshot()
	if snap == nil {
		t.Fatal("tx.Snapshot() = nil, want snapshot")
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("json.Marshal(snapshot) error = %v, want nil", err)
	}

	var snapCopy sip.ClientTransactionSnapshot
	if err := json.Unmarshal(data, &snapCopy); err != nil {
		t.Fatalf("json.Unmarshal(snapshot) error = %v, want nil", err)
	}

	restoredTP := newStubClientTransport(true)

	restored, err := sip.RestoreInviteClientTransaction(t.Context(), &snapCopy, restoredTP, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.RestoreInviteClientTransaction(snap, tp, opts) error = %v, want nil", err)
	}

	if got, want := restored.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("restored.State() = %q, want %q", got, want)
	}

	if got, want := restored.Key(), tx.Key(); !got.Equal(want) {
		t.Fatalf("restored.Key() = %v, want %v", got, want)
	}

	if res := restored.LastResponse(); res.Status() != sip.ResponseStatusDecline {
		t.Fatalf("restored.LastResponse().Status() = %v, want %v", res.Status(), sip.ResponseStatusDecline)
	}

	retransmit := decline.Clone().(*sip.InboundResponseEnvelope) //nolint:forcetypeassert
	if err := restored.RecvResponse(ctx, retransmit); err != nil {
		t.Fatalf("restored.RecvResponse(ctx, 603) error = %v, want nil", err)
	}

	ack := restoredTP.waitSendReq(t, 100*time.Millisecond)
	if ack.req.Method() != sip.RequestMethodAck {
		t.Fatalf("sent %v, want ACK retransmit", ack.req.Method())
	}

	waitForTransactState(t, restored, sip.TransactionStateTerminated, timings.TimeD()+200*time.Millisecond)
}

func TestInviteClientTransaction_Terminate_FromCalling(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	tp := newStubClientTransport(false)
	req := newOutInviteReq(t, "UDP", sip.MagicCookie+".terminate-calling", local, remote)

	tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
	}

	tp.waitSendReq(t, 100*time.Millisecond)

	if got := tx.State(); got != sip.TransactionStateCalling {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateCalling)
	}

	stateCh := make(chan sip.TransactionState, 1)
	tx.OnStateChanged(func(_ context.Context, _, to sip.TransactionState) {
		if to == sip.TransactionStateTerminated {
			stateCh <- to
		}
	})

	ctx := t.Context()
	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v", err)
	}

	select {
	case <-stateCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnStateChanged callback wait timeout")
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSendReq(t, 2*t1)
}

func TestInviteClientTransaction_Terminate_FromProceeding(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	tp := newStubClientTransport(false)
	req := newOutInviteReq(t, "UDP", sip.MagicCookie+".terminate-proceeding", local, remote)

	tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
	}

	tp.waitSendReq(t, 100*time.Millisecond)
	ctx := t.Context()

	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
		t.Fatalf("tx.RecvResponse(180) error = %v, want nil", err)
	}

	tp.drainSendReqs()

	if got := tx.State(); got != sip.TransactionStateProceeding {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateProceeding)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSendReq(t, 2*t1)
}

func TestInviteClientTransaction_Terminate_FromAccepted(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	tp := newStubClientTransport(false)
	req := newOutInviteReq(t, "UDP", sip.MagicCookie+".terminate-accepted", local, remote)

	tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteClientTransaction(INVITE, tp, opts) error = %v, want nil", err)
	}

	tp.waitSendReq(t, 100*time.Millisecond)
	ctx := t.Context()

	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
		t.Fatalf("tx.RecvResponse(200) error = %v, want nil", err)
	}

	tp.drainSendReqs()

	if got := tx.State(); got != sip.TransactionStateAccepted {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateAccepted)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSendReq(t, 2*t1)
}

func TestInviteClientTransaction_Terminate_FromCompleted(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	tp := newStubClientTransport(false)
	req := newOutInviteReq(t, "UDP", sip.MagicCookie+".terminate-completed", local, remote)

	tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
	}

	tp.waitSendReq(t, 100*time.Millisecond)
	ctx := t.Context()

	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusDecline)); err != nil {
		t.Fatalf("tx.RecvResponse(603) error = %v, want nil", err)
	}

	tp.waitSendReq(t, 100*time.Millisecond)
	tp.drainSendReqs()

	if got := tx.State(); got != sip.TransactionStateCompleted {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateCompleted)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSendReq(t, 2*t1)
}

func TestInviteClientTransaction_Terminate_Idempotent(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("11.11.11.11:5070")

	tp := newStubClientTransport(false)
	req := newOutInviteReq(t, "UDP", sip.MagicCookie+".terminate-idempotent", local, remote)

	tx, err := sip.NewInviteClientTransaction(t.Context(), req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
	}

	tp.waitSendReq(t, 100*time.Millisecond)
	ctx := t.Context()

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}
}

func TestNonInviteClientTransaction_LifecycleUnreliable(t *testing.T) {
	t.Parallel()

	t1 := 20 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, 2*t1)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("22.22.22.22:5070")

	tp := newStubClientTransport(false)
	req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".client-noninvite", local, remote)

	tx, err := sip.NewNonInviteClientTransaction(t.Context(), req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
	}

	call := tp.waitSendReq(t, 100*time.Millisecond)
	if call.req.Method() != sip.RequestMethodInfo {
		t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInfo)
	}

	if call.req.RemoteAddr() != remote {
		t.Fatalf("initial send remote addr = %v, want %v", call.req.RemoteAddr(), remote)
	}

	if got, want := tx.State(), sip.TransactionStateTrying; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	// Timer E should retransmit the request while waiting for a response on unreliable transports.
	retrans := tp.waitSendReq(t, timings.TimeE()+50*time.Millisecond)
	if retrans.req.Method() != sip.RequestMethodInfo {
		t.Fatalf("retransmit method = %q, want %q", retrans.req.Method(), sip.RequestMethodInfo)
	}

	resCh := make(chan *sip.InboundResponseEnvelope, 2)
	tx.OnResponse(func(_ context.Context, res *sip.InboundResponseEnvelope) {
		resCh <- res
	})

	ctx := t.Context()
	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 180) error = %v, want nil", err)
	}

	if got, want := tx.State(), sip.TransactionStateProceeding; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	assertResponseStatus(t, resCh, sip.ResponseStatusRinging)

	tp.drainSendReqs()

	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 200) error = %v, want nil", err)
	}

	if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	assertResponseStatus(t, resCh, sip.ResponseStatusOK)

	if res := tx.LastResponse(); res.Status() != sip.ResponseStatusOK {
		t.Fatalf("tx.LastResponse().Status() = %v, want %v", res.Status(), sip.ResponseStatusOK)
	}

	tp.drainSendReqs()
	tp.ensureNoSendReq(t, timings.TimeE()+20*time.Millisecond)

	waitForTransactState(t, tx, sip.TransactionStateTerminated, timings.TimeK()+200*time.Millisecond)
	tp.ensureNoSendReq(t, 2*timings.TimeE())
}

func TestNonInviteClientTransaction_RoundTripSnapshot(t *testing.T) {
	t.Parallel()

	t1 := 10 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, 2*t1)

	remote := netip.MustParseAddrPort("66.66.66.66:5080")
	local := netip.MustParseAddrPort("22.22.22.22:5071")

	origTP := newStubClientTransport(true)
	req := newOutNonInviteReq(t, "TCP", sip.MagicCookie+".client-noninvite-snapshot", local, remote)

	tx, err := sip.NewNonInviteClientTransaction(t.Context(), req, origTP, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteClientTransaction(req, tp, opts) error = %v, want nil", err)
	}

	call := origTP.waitSendReq(t, 100*time.Millisecond)
	if call.req.Method() != sip.RequestMethodInfo {
		t.Fatalf("initial send method = %q, want %q", call.req.Method(), sip.RequestMethodInfo)
	}

	ctx := t.Context()
	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
		t.Fatalf("tx.RecvResponse(ctx, 200) error = %v, want nil", err)
	}

	if got, want := tx.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("tx.State() = %q, want %q", got, want)
	}

	snap := tx.Snapshot()
	if snap == nil {
		t.Fatal("tx.Snapshot() = nil, want snapshot")
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("json.Marshal(snapshot) error = %v, want nil", err)
	}

	var snapCopy sip.ClientTransactionSnapshot
	if err := json.Unmarshal(data, &snapCopy); err != nil {
		t.Fatalf("json.Unmarshal(snapshot) error = %v, want nil", err)
	}

	restoredTP := newStubClientTransport(true)

	restored, err := sip.RestoreNonInviteClientTransaction(t.Context(), &snapCopy, restoredTP, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.RestoreNonInviteClientTransaction(snap, tp, opts) error = %v, want nil", err)
	}

	if got, want := restored.State(), sip.TransactionStateCompleted; got != want {
		t.Fatalf("restored.State() = %q, want %q", got, want)
	}

	if got, want := restored.Key(), tx.Key(); !got.Equal(want) {
		t.Fatalf("restored.Key() = %v, want %v", got, want)
	}

	if res := restored.LastResponse(); res.Status() != sip.ResponseStatusOK {
		t.Fatalf("restored.LastResponse().Status() = %v, want %v", res.Status(), sip.ResponseStatusOK)
	}

	waitForTransactState(t, restored, sip.TransactionStateTerminated, timings.TimeK()+200*time.Millisecond)
	restoredTP.ensureNoSendReq(t, 100*time.Millisecond)
}

func TestNonInviteClientTransaction_Terminate_FromTrying(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("22.22.22.22:5070")

	tp := newStubClientTransport(false)
	req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-trying", local, remote)

	tx, err := sip.NewNonInviteClientTransaction(t.Context(), req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
	}

	tp.waitSendReq(t, 100*time.Millisecond)

	if got := tx.State(); got != sip.TransactionStateTrying {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTrying)
	}

	stateCh := make(chan sip.TransactionState, 1)
	tx.OnStateChanged(func(_ context.Context, _, to sip.TransactionState) {
		if to == sip.TransactionStateTerminated {
			stateCh <- to
		}
	})

	ctx := t.Context()
	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	select {
	case <-stateCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnStateChanged callback wait timeout")
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSendReq(t, 2*t1)
}

func TestNonInviteClientTransaction_Terminate_FromProceeding(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("22.22.22.22:5070")

	tp := newStubClientTransport(false)
	req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-proceeding", local, remote)

	tx, err := sip.NewNonInviteClientTransaction(t.Context(), req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
	}

	tp.waitSendReq(t, 100*time.Millisecond)
	ctx := t.Context()

	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
		t.Fatalf("tx.RecvResponse(180) error = %v, want nil", err)
	}

	tp.drainSendReqs()

	if got := tx.State(); got != sip.TransactionStateProceeding {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateProceeding)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSendReq(t, 2*t1)
}

func TestNonInviteClientTransaction_Terminate_FromCompleted(t *testing.T) {
	t.Parallel()

	t1 := 50 * time.Millisecond
	timings := sip.NewTimings(t1, 8*t1, 10*t1, 64*t1, time.Minute)

	remote := netip.MustParseAddrPort("55.55.55.55:5060")
	local := netip.MustParseAddrPort("22.22.22.22:5070")

	tp := newStubClientTransport(false)
	req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".terminate-completed", local, remote)

	tx, err := sip.NewNonInviteClientTransaction(t.Context(), req, tp, &sip.ClientTransactionOptions{Timings: timings})
	if err != nil {
		t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
	}

	tp.waitSendReq(t, 100*time.Millisecond)
	ctx := t.Context()

	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
		t.Fatalf("tx.RecvResponse(200) error = %v, want nil", err)
	}

	tp.drainSendReqs()

	if got := tx.State(); got != sip.TransactionStateCompleted {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateCompleted)
	}

	if err := tx.Terminate(ctx); err != nil {
		t.Fatalf("tx.Terminate() error = %v, want nil", err)
	}

	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Fatalf("tx.State() = %q, want %q", got, sip.TransactionStateTerminated)
	}

	tp.ensureNoSendReq(t, 2*t1)
}
