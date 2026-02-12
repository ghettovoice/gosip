package sip_test

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/sip"
)

// sendReqCall captures a request send call for testing.
type sendReqCall struct {
	req  *sip.OutboundRequestEnvelope
	opts *sip.SendRequestOptions
}

// sendResCall captures a response send call for testing.
type sendResCall struct {
	res  *sip.OutboundResponseEnvelope
	opts *sip.SendResponseOptions
}

// stubTransport is a unified test stub implementing sip.Transport.
// It can be used as ClientTransport, ServerTransport, or full Transport.
type stubTransport struct {
	proto   sip.TransportProto
	laddr   netip.AddrPort
	network string
	rel     bool // reliable transport flag

	mu         sync.Mutex
	serveErr   error
	closed     bool
	inReqInts  []sip.InboundRequestInterceptor
	inResInts  []sip.InboundResponseInterceptor
	outReqInts []sip.OutboundRequestInterceptor
	outResInts []sip.OutboundResponseInterceptor
	serveCh    chan struct{}

	// Request tracking
	sentReqs    []sendReqCall
	sendReqCh   chan sendReqCall
	sendReqHook func(call sendReqCall, index int) error

	// Response tracking
	sentRess    []sendResCall
	sendResCh   chan sendResCall
	sendResHook func(call sendResCall, index int) error
}

func newStubTransport(proto sip.TransportProto, port uint16) *stubTransport {
	laddr := netip.MustParseAddrPort(fmt.Sprintf("127.0.0.1:%d", port))
	return &stubTransport{
		proto:     proto,
		laddr:     laddr,
		network:   strings.ToLower(string(proto)),
		serveCh:   make(chan struct{}),
		sendReqCh: make(chan sendReqCall, 16),
		sendResCh: make(chan sendResCall, 16),
	}
}

func newStubTransportExt(
	proto sip.TransportProto,
	netw string,
	laddr netip.AddrPort,
	rel bool,
) *stubTransport {
	return &stubTransport{
		proto:     proto,
		laddr:     laddr,
		network:   netw,
		rel:       rel,
		serveCh:   make(chan struct{}),
		sendReqCh: make(chan sendReqCall, 16),
		sendResCh: make(chan sendResCall, 16),
	}
}

func (st *stubTransport) Serve(ctx context.Context) error {
	st.mu.Lock()
	ch := st.serveCh
	st.mu.Unlock()

	<-ch
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.serveErr != nil {
		return errtrace.Wrap(st.serveErr)
	}
	if st.closed {
		return errtrace.Wrap(sip.ErrTransportClosed)
	}
	return nil
}

func (st *stubTransport) Close(ctx context.Context) error {
	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		return errtrace.Wrap(sip.ErrTransportClosed)
	}
	st.closed = true
	st.mu.Unlock()
	close(st.serveCh)
	return nil
}

func (st *stubTransport) UseInboundRequestInterceptor(interceptor sip.InboundRequestInterceptor) (unbind func()) {
	st.mu.Lock()
	idx := len(st.inReqInts)
	st.inReqInts = append(st.inReqInts, interceptor)
	st.mu.Unlock()
	return func() {
		st.mu.Lock()
		if idx < len(st.inReqInts) {
			st.inReqInts[idx] = nil
		}
		st.mu.Unlock()
	}
}

func (st *stubTransport) UseInboundResponseInterceptor(interceptor sip.InboundResponseInterceptor) (unbind func()) {
	st.mu.Lock()
	idx := len(st.inResInts)
	st.inResInts = append(st.inResInts, interceptor)
	st.mu.Unlock()
	return func() {
		st.mu.Lock()
		if idx < len(st.inResInts) {
			st.inResInts[idx] = nil
		}
		st.mu.Unlock()
	}
}

func (st *stubTransport) UseOutboundRequestInterceptor(interceptor sip.OutboundRequestInterceptor) (unbind func()) {
	st.mu.Lock()
	idx := len(st.outReqInts)
	st.outReqInts = append(st.outReqInts, interceptor)
	st.mu.Unlock()
	return func() {
		st.mu.Lock()
		if idx < len(st.outReqInts) {
			st.outReqInts[idx] = nil
		}
		st.mu.Unlock()
	}
}

func (st *stubTransport) UseOutboundResponseInterceptor(interceptor sip.OutboundResponseInterceptor) (unbind func()) {
	st.mu.Lock()
	idx := len(st.outResInts)
	st.outResInts = append(st.outResInts, interceptor)
	st.mu.Unlock()
	return func() {
		st.mu.Lock()
		if idx < len(st.outResInts) {
			st.outResInts[idx] = nil
		}
		st.mu.Unlock()
	}
}

func (st *stubTransport) UseInterceptor(interceptor sip.MessageInterceptor) (unbind func()) {
	if interceptor == nil {
		return func() {}
	}

	var unbinds []func()
	if inbound := interceptor.InboundRequestInterceptor(); inbound != nil {
		unbinds = append(unbinds, st.UseInboundRequestInterceptor(inbound))
	}
	if inbound := interceptor.InboundResponseInterceptor(); inbound != nil {
		unbinds = append(unbinds, st.UseInboundResponseInterceptor(inbound))
	}
	if outbound := interceptor.OutboundRequestInterceptor(); outbound != nil {
		unbinds = append(unbinds, st.UseOutboundRequestInterceptor(outbound))
	}
	if outbound := interceptor.OutboundResponseInterceptor(); outbound != nil {
		unbinds = append(unbinds, st.UseOutboundResponseInterceptor(outbound))
	}
	return func() {
		for _, fn := range unbinds {
			fn()
		}
	}
}

func (st *stubTransport) LocalAddr() netip.AddrPort { return st.laddr }

func (st *stubTransport) Proto() sip.TransportProto { return st.proto }

func (st *stubTransport) Network() string { return st.network }

func (st *stubTransport) Reliable() bool { return st.rel }

func (*stubTransport) Secured() bool { return false }

func (*stubTransport) Streamed() bool { return false }

func (st *stubTransport) DefaultPort() uint16 { return st.laddr.Port() }

func (st *stubTransport) SendRequest(_ context.Context, req *sip.OutboundRequestEnvelope, opts *sip.SendRequestOptions) error {
	call := sendReqCall{req: req}
	if opts != nil {
		copied := *opts
		call.opts = &copied
	}

	st.mu.Lock()
	st.sentReqs = append(st.sentReqs, call)
	idx := len(st.sentReqs) - 1
	hook := st.sendReqHook
	st.mu.Unlock()

	if hook != nil {
		if err := hook(call, idx); err != nil {
			return errtrace.Wrap(err)
		}
	}

	st.sendReqCh <- call
	return nil
}

// func (st *stubTransport) setSendReqHook(fn func(sendReqCall, int) error) {
// 	st.mu.Lock()
// 	st.sendReqHook = fn
// 	st.mu.Unlock()
// }

func (st *stubTransport) SendResponse(_ context.Context, res *sip.OutboundResponseEnvelope, opts *sip.SendResponseOptions) error {
	call := sendResCall{res: res}
	if opts != nil {
		copied := *opts
		call.opts = &copied
	}

	st.mu.Lock()
	st.sentRess = append(st.sentRess, call)
	idx := len(st.sentRess) - 1
	hook := st.sendResHook
	st.mu.Unlock()

	if hook != nil {
		if err := hook(call, idx); err != nil {
			return errtrace.Wrap(err)
		}
	}

	st.sendResCh <- call
	return nil
}

func (st *stubTransport) setSendResHook(fn func(sendResCall, int) error) {
	st.mu.Lock()
	st.sendResHook = fn
	st.mu.Unlock()
}

func (st *stubTransport) requestCount() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.sentReqs)
}

func (st *stubTransport) responseCount() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.sentRess)
}

// func (st *stubTransport) sendReqChan() <-chan sendReqCall {
// 	return st.sendReqCh
// }

func (st *stubTransport) sendResChan() <-chan sendResCall {
	return st.sendResCh
}

// func (st *stubTransport) triggerRequest(ctx context.Context, req *sip.InboundRequestEnvelope) error {
// 	st.mu.Lock()
// 	interceptors := append([]sip.InboundRequestInterceptor(nil), st.inReqInts...)
// 	st.mu.Unlock()

// 	final := sip.RequestReceiverFunc(func(context.Context, *sip.InboundRequestEnvelope) error {
// 		return nil
// 	})
// 	receiver := sip.ChainInboundRequest(interceptors, final)
// 	if receiver == nil {
// 		return nil
// 	}
// 	return errtrace.Wrap(receiver.RecvRequest(sip.ContextWithTransport(ctx, st), req))
// }

// func (st *stubTransport) triggerResponse(ctx context.Context, res *sip.InboundResponseEnvelope) error {
// 	st.mu.Lock()
// 	interceptors := append([]sip.InboundResponseInterceptor(nil), st.inResInts...)
// 	st.mu.Unlock()

// 	final := sip.ResponseReceiverFunc(func(context.Context, *sip.InboundResponseEnvelope) error {
// 		return nil
// 	})
// 	receiver := sip.ChainInboundResponse(interceptors, final)
// 	if receiver == nil {
// 		return nil
// 	}
// 	return errtrace.Wrap(receiver.RecvResponse(sip.ContextWithTransport(ctx, st), res))
// }

// waitSendReq waits for a request to be sent and returns it.
func (st *stubTransport) waitSendReq(tb testing.TB, timeout time.Duration) sendReqCall {
	tb.Helper()
	select {
	case call := <-st.sendReqCh:
		return call
	case <-time.After(timeout):
		tb.Fatalf("expected request send within %v", timeout)
		return sendReqCall{}
	}
}

// waitSendRes waits for a response to be sent and returns it.
func (st *stubTransport) waitSendRes(tb testing.TB, timeout time.Duration) sendResCall {
	tb.Helper()
	select {
	case call := <-st.sendResCh:
		return call
	case <-time.After(timeout):
		tb.Fatalf("expected response send within %v", timeout)
		return sendResCall{}
	}
}

// ensureNoSendReq asserts no request is sent within timeout.
func (st *stubTransport) ensureNoSendReq(tb testing.TB, timeout time.Duration) {
	tb.Helper()
	select {
	case call := <-st.sendReqCh:
		tb.Fatalf("unexpected send: method %v", call.req.Method())
	case <-time.After(timeout):
	}
}

// ensureNoSendRes asserts no response is sent within timeout.
func (st *stubTransport) ensureNoSendRes(tb testing.TB, timeout time.Duration) {
	tb.Helper()
	select {
	case call := <-st.sendResCh:
		tb.Fatalf("unexpected send: status %v", call.res.Status())
	case <-time.After(timeout):
	}
}

// drainSendReqs drains all pending request sends from the channel.
func (st *stubTransport) drainSendReqs() {
	for {
		select {
		case <-st.sendReqCh:
		default:
			return
		}
	}
}

// drainSendRess drains all pending response sends from the channel.
func (st *stubTransport) drainSendRess() {
	for {
		select {
		case <-st.sendResCh:
		default:
			return
		}
	}
}
