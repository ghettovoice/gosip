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
	req  *sip.OutboundRequest
	opts *sip.SendRequestOptions
}

// sendResCall captures a response send call for testing.
type sendResCall struct {
	res  *sip.OutboundResponse
	opts *sip.SendResponseOptions
}

// stubTransport is a unified test stub implementing sip.Transport.
// It can be used as ClientTransport, ServerTransport, or full Transport.
type stubTransport struct {
	proto   sip.TransportProto
	laddr   netip.AddrPort
	network string
	rel     bool // reliable transport flag

	mu          sync.Mutex
	serveErr    error
	closed      bool
	reqHandlers []sip.RequestHandler
	resHandlers []sip.ResponseHandler
	serveCh     chan struct{}

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

func (st *stubTransport) Serve() error {
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

func (st *stubTransport) Close() error {
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

func (st *stubTransport) OnRequest(fn sip.RequestHandler) (cancel func()) {
	st.mu.Lock()
	idx := len(st.reqHandlers)
	st.reqHandlers = append(st.reqHandlers, fn)
	st.mu.Unlock()
	return func() {
		st.mu.Lock()
		if idx < len(st.reqHandlers) {
			st.reqHandlers[idx] = nil
		}
		st.mu.Unlock()
	}
}

func (st *stubTransport) OnResponse(fn sip.ResponseHandler) (cancel func()) {
	st.mu.Lock()
	idx := len(st.resHandlers)
	st.resHandlers = append(st.resHandlers, fn)
	st.mu.Unlock()
	return func() {
		st.mu.Lock()
		if idx < len(st.resHandlers) {
			st.resHandlers[idx] = nil
		}
		st.mu.Unlock()
	}
}

func (st *stubTransport) LocalAddr() netip.AddrPort { return st.laddr }

func (st *stubTransport) Proto() sip.TransportProto { return st.proto }

func (st *stubTransport) Network() string { return st.network }

func (st *stubTransport) Reliable() bool { return st.rel }

func (*stubTransport) Secured() bool { return false }

func (*stubTransport) Streamed() bool { return false }

func (st *stubTransport) DefaultPort() uint16 { return st.laddr.Port() }

func (st *stubTransport) SendRequest(_ context.Context, req *sip.OutboundRequest, opts *sip.SendRequestOptions) error {
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

func (st *stubTransport) SendResponse(_ context.Context, res *sip.OutboundResponse, opts *sip.SendResponseOptions) error {
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

func (st *stubTransport) triggerRequest(ctx context.Context, req *sip.InboundRequest) {
	st.mu.Lock()
	handlers := append([]sip.RequestHandler(nil), st.reqHandlers...)
	st.mu.Unlock()
	for _, fn := range handlers {
		if fn != nil {
			fn(ctx, req)
		}
	}
}

func (st *stubTransport) triggerResponse(ctx context.Context, res *sip.InboundResponse) {
	st.mu.Lock()
	handlers := append([]sip.ResponseHandler(nil), st.resHandlers...)
	st.mu.Unlock()
	for _, fn := range handlers {
		if fn != nil {
			fn(ctx, res)
		}
	}
}

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

// Aliases for backward compatibility with existing tests.

// waitSend is an alias for waitSendReq (for client transaction tests).
func (st *stubTransport) waitSend(tb testing.TB, timeout time.Duration) sendReqCall {
	tb.Helper()
	return st.waitSendReq(tb, timeout)
}

// ensureNoSend is an alias for ensureNoSendReq (for client transaction tests).
func (st *stubTransport) ensureNoSend(tb testing.TB, timeout time.Duration) {
	tb.Helper()
	st.ensureNoSendReq(tb, timeout)
}

// drainSends is an alias for drainSendReqs (for client transaction tests).
func (st *stubTransport) drainSends() {
	st.drainSendReqs()
}

// newStubClientTransport is an alias for newStubTransportExt (for client transaction tests).
func newStubClientTransport(
	proto sip.TransportProto,
	netw string,
	laddr netip.AddrPort,
	rel bool,
) *stubTransport {
	return newStubTransportExt(proto, netw, laddr, rel)
}

// stubServerTransportWrapper wraps stubTransport for server transaction tests,
// providing server-specific method signatures.
type stubServerTransportWrapper struct {
	*stubTransport
}

// newStubServerTransport creates a stub for server transaction tests.
func newStubServerTransport(
	proto sip.TransportProto,
	netw string,
	laddr netip.AddrPort,
	rel bool,
) *stubServerTransportWrapper {
	return &stubServerTransportWrapper{
		stubTransport: newStubTransportExt(proto, netw, laddr, rel),
	}
}

// waitSend waits for a response send (for server transaction tests).
func (w *stubServerTransportWrapper) waitSend(tb testing.TB, timeout time.Duration) sendResCall {
	tb.Helper()
	return w.waitSendRes(tb, timeout)
}

// ensureNoSend asserts no response is sent within timeout (for server transaction tests).
func (w *stubServerTransportWrapper) ensureNoSend(tb testing.TB, timeout time.Duration) {
	tb.Helper()
	w.ensureNoSendRes(tb, timeout)
}

// drainSends drains pending response sends (for server transaction tests).
func (w *stubServerTransportWrapper) drainSends() {
	w.drainSendRess()
}

// sendCh returns the response send channel (for server transaction tests).
func (w *stubServerTransportWrapper) sendCh() chan sendResCall {
	return w.sendResCh
}

// setSendHook sets the response send hook (for server transaction tests).
func (w *stubServerTransportWrapper) setSendHook(hook func(call sendResCall, index int) error) {
	w.mu.Lock()
	w.sendResHook = hook
	w.mu.Unlock()
}
