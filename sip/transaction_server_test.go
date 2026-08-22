package sip_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

type sendResCall struct {
	ctx  context.Context
	res  *sip.ResponseEnvelope
	opts sip.SendResponseOptions
}

type stubServerTransport struct {
	reliable bool

	sendResCalls chan sendResCall

	mu          sync.Mutex
	sendResHook func(sendResCall, int) error
	sendResCnt  int
}

func newStubServerTransport(reliable bool) *stubServerTransport {
	return &stubServerTransport{
		reliable:     reliable,
		sendResCalls: make(chan sendResCall, 64),
	}
}

func (tp *stubServerTransport) Metadata() sip.TransportMetadata {
	if tp == nil {
		return sip.TransportMetadata{}
	}

	var flags sip.TransportFlags

	flags.SetReliable(tp.reliable)

	return sip.TransportMetadata{Flags: flags}
}

func (tp *stubServerTransport) SendResponse(
	ctx context.Context,
	res *sip.ResponseEnvelope,
	opts ...sip.SendResponseOptions,
) error {
	call := sendResCall{
		ctx:  ctx,
		res:  res,
		opts: util.LastSliceElemOr(opts, sip.SendResponseOptions{}),
	}

	tp.mu.Lock()
	idx := tp.sendResCnt
	tp.sendResCnt++
	hook := tp.sendResHook
	tp.mu.Unlock()

	if hook != nil {
		if err := hook(call, idx); err != nil {
			return err
		}
	}

	tp.sendResCalls <- call

	return nil
}

func (tp *stubServerTransport) setSendResHook(hook func(sendResCall, int) error) {
	tp.mu.Lock()
	tp.sendResHook = hook
	tp.mu.Unlock()
}

func (tp *stubServerTransport) waitSendRes(tb testing.TB) sendResCall {
	tb.Helper()

	tmr := time.NewTimer(5 * time.Second)
	defer tmr.Stop()

	select {
	case call := <-tp.sendResCalls:
		return call
	case <-tmr.C:
		tb.Fatalf("timed out waiting for response send call")
		return sendResCall{}
	}
}

func (tp *stubServerTransport) ensureNoSendRes(tb testing.TB) {
	tb.Helper()

	tmr := time.NewTimer(1 * time.Second)
	defer tmr.Stop()

	select {
	case call := <-tp.sendResCalls:
		tb.Fatalf("unexpected response send call with status %v", call.res.Status())
	case <-tmr.C:
	}
}

func startServerTransaction(tb testing.TB, tx sip.ServerTransaction) {
	tb.Helper()

	if err := tx.Start(tb.Context()); err != nil {
		tb.Fatalf("tx.Start() error = %v, want nil", err)
	}
}

func (tp *stubServerTransport) sendResChan() <-chan sendResCall {
	return tp.sendResCalls
}

func (tp *stubServerTransport) drainSendRess() {
	for {
		select {
		case <-tp.sendResCalls:
		default:
			return
		}
	}
}
