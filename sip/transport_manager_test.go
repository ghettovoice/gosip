package sip_test

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/uri"
)

type spyTransport struct {
	mu sync.Mutex

	meta       sip.TransportMetadata
	closeErr   error
	listenErr  error
	sendReqErr error
	sendResErr error

	closeCalls   int
	listenCalls  int
	sendReqCalls int
	sendResCalls int

	bindInReqCalls    int
	unbindInReqCalls  int
	bindInResCalls    int
	unbindInResCalls  int
	bindOutReqCalls   int
	unbindOutReqCalls int
	bindOutResCalls   int
	unbindOutResCalls int
}

type spyTransportCounts struct {
	closeCalls   int
	listenCalls  int
	sendReqCalls int
	sendResCalls int

	bindInReqCalls    int
	unbindInReqCalls  int
	bindInResCalls    int
	unbindInResCalls  int
	bindOutReqCalls   int
	unbindOutReqCalls int
	bindOutResCalls   int
	unbindOutResCalls int
}

func newSpyTransport(meta sip.TransportMetadata) *spyTransport {
	return &spyTransport{meta: meta}
}

func (s *spyTransport) Metadata() sip.TransportMetadata {
	return s.meta
}

func (s *spyTransport) SendRequest(context.Context, *sip.OutboundRequestEnvelope, *sip.SendRequestOptions) error {
	s.mu.Lock()
	s.sendReqCalls++
	err := s.sendReqErr
	s.mu.Unlock()

	return err
}

func (s *spyTransport) SendResponse(context.Context, *sip.OutboundResponseEnvelope, *sip.SendResponseOptions) error {
	s.mu.Lock()
	s.sendResCalls++
	err := s.sendResErr
	s.mu.Unlock()

	return err
}

func (s *spyTransport) ListenAndServe(context.Context, string) error {
	s.mu.Lock()
	s.listenCalls++
	err := s.listenErr
	s.mu.Unlock()

	return err
}

func (s *spyTransport) Close() error {
	s.mu.Lock()
	s.closeCalls++
	err := s.closeErr
	s.mu.Unlock()

	return err
}

func (s *spyTransport) UseInboundRequestInterceptor(sip.InboundRequestInterceptor) func() {
	s.mu.Lock()
	s.bindInReqCalls++
	s.mu.Unlock()

	var once sync.Once

	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.unbindInReqCalls++
			s.mu.Unlock()
		})
	}
}

func (s *spyTransport) UseInboundResponseInterceptor(sip.InboundResponseInterceptor) func() {
	s.mu.Lock()
	s.bindInResCalls++
	s.mu.Unlock()

	var once sync.Once

	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.unbindInResCalls++
			s.mu.Unlock()
		})
	}
}

func (s *spyTransport) UseOutboundRequestInterceptor(sip.OutboundRequestInterceptor) func() {
	s.mu.Lock()
	s.bindOutReqCalls++
	s.mu.Unlock()

	var once sync.Once

	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.unbindOutReqCalls++
			s.mu.Unlock()
		})
	}
}

func (s *spyTransport) UseOutboundResponseInterceptor(sip.OutboundResponseInterceptor) func() {
	s.mu.Lock()
	s.bindOutResCalls++
	s.mu.Unlock()

	var once sync.Once

	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.unbindOutResCalls++
			s.mu.Unlock()
		})
	}
}

func (s *spyTransport) UseInterceptor(interceptor sip.MessageInterceptor) func() {
	if interceptor == nil {
		return func() {}
	}

	var unbinds []func()
	if inReq := interceptor.InboundRequestInterceptor(); inReq != nil {
		unbinds = append(unbinds, s.UseInboundRequestInterceptor(inReq))
	}

	if inRes := interceptor.InboundResponseInterceptor(); inRes != nil {
		unbinds = append(unbinds, s.UseInboundResponseInterceptor(inRes))
	}

	if outReq := interceptor.OutboundRequestInterceptor(); outReq != nil {
		unbinds = append(unbinds, s.UseOutboundRequestInterceptor(outReq))
	}

	if outRes := interceptor.OutboundResponseInterceptor(); outRes != nil {
		unbinds = append(unbinds, s.UseOutboundResponseInterceptor(outRes))
	}

	return func() {
		for _, fn := range unbinds {
			if fn != nil {
				fn()
			}
		}
	}
}

func (s *spyTransport) counts() spyTransportCounts {
	s.mu.Lock()
	defer s.mu.Unlock()

	return spyTransportCounts{
		closeCalls:   s.closeCalls,
		listenCalls:  s.listenCalls,
		sendReqCalls: s.sendReqCalls,
		sendResCalls: s.sendResCalls,

		bindInReqCalls:    s.bindInReqCalls,
		unbindInReqCalls:  s.unbindInReqCalls,
		bindInResCalls:    s.bindInResCalls,
		unbindInResCalls:  s.unbindInResCalls,
		bindOutReqCalls:   s.bindOutReqCalls,
		unbindOutReqCalls: s.unbindOutReqCalls,
		bindOutResCalls:   s.bindOutResCalls,
		unbindOutResCalls: s.unbindOutResCalls,
	}
}

type testMessageInterceptor struct {
	inReq  sip.InboundRequestInterceptor
	inRes  sip.InboundResponseInterceptor
	outReq sip.OutboundRequestInterceptor
	outRes sip.OutboundResponseInterceptor
}

func (m testMessageInterceptor) InboundRequestInterceptor() sip.InboundRequestInterceptor {
	return m.inReq
}

func (m testMessageInterceptor) InboundResponseInterceptor() sip.InboundResponseInterceptor {
	return m.inRes
}

func (m testMessageInterceptor) OutboundRequestInterceptor() sip.OutboundRequestInterceptor {
	return m.outReq
}

func (m testMessageInterceptor) OutboundResponseInterceptor() sip.OutboundResponseInterceptor {
	return m.outRes
}

func newTestRequest(tb testing.TB, tp sip.TransportProto) *sip.Request {
	tb.Helper()

	ruri := &uri.SIP{User: uri.User("alice"), Addr: uri.AddrFromHost("example.com")}
	furi := &uri.SIP{User: uri.User("bob"), Addr: uri.AddrFromHost("example.com")}
	turi := &uri.SIP{User: uri.User("alice"), Addr: uri.AddrFromHost("example.com")}

	req, err := sip.NewRequest(sip.RequestMethodInvite, ruri, furi, turi, &sip.RequestOptions{Transport: tp})
	if err != nil {
		tb.Fatalf("sip.NewRequest() error = %v, want nil", err)
	}

	return req
}

func newTestOutboundRequestEnvelope(tb testing.TB, tp sip.TransportProto) *sip.OutboundRequestEnvelope {
	tb.Helper()

	env, err := sip.NewOutboundRequestEnvelope(newTestRequest(tb, tp))
	if err != nil {
		tb.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
	}

	env.SetTransport(tp)
	env.SetLocalAddr(netip.MustParseAddrPort("127.0.0.1:5060"))
	env.SetRemoteAddr(netip.MustParseAddrPort("127.0.0.1:5070"))

	return env
}

func newTestOutboundResponseEnvelope(tb testing.TB, tp sip.TransportProto) *sip.OutboundResponseEnvelope {
	tb.Helper()

	req := newTestRequest(tb, tp)

	res, err := req.NewResponse(sip.ResponseStatusOK, nil)
	if err != nil {
		tb.Fatalf("req.NewResponse() error = %v, want nil", err)
	}

	env, err := sip.NewOutboundResponseEnvelope(res)
	if err != nil {
		tb.Fatalf("sip.NewOutboundResponseEnvelope() error = %v, want nil", err)
	}

	env.SetTransport(tp)
	env.SetLocalAddr(netip.MustParseAddrPort("127.0.0.1:5060"))
	env.SetRemoteAddr(netip.MustParseAddrPort("127.0.0.1:5070"))

	return env
}

func newTestInboundRequestEnvelope(tb testing.TB, tp sip.TransportProto) *sip.InboundRequestEnvelope {
	tb.Helper()

	env, err := sip.NewInboundRequestEnvelope(
		newTestRequest(tb, tp),
		tp,
		netip.MustParseAddrPort("127.0.0.1:5060"),
		netip.MustParseAddrPort("127.0.0.1:5070"),
	)
	if err != nil {
		tb.Fatalf("sip.NewInboundRequestEnvelope() error = %v, want nil", err)
	}

	return env
}

func TestTransportManager_TrackGetUntrack(t *testing.T) {
	t.Parallel()

	var mgr sip.TransportManager

	udp := newSpyTransport(sip.UDPTransportMetadata())
	tcp := newSpyTransport(sip.TCPTransportMetadata())

	if err := mgr.TrackTransport(nil); !errors.Is(err, sip.ErrInvalidArgument) {
		t.Fatalf("mgr.TrackTransport(nil) error = %v, want wraps %v", err, sip.ErrInvalidArgument)
	}

	if err := mgr.TrackTransport(udp); err != nil {
		t.Fatalf("mgr.TrackTransport(udp) error = %v, want nil", err)
	}

	if err := mgr.TrackTransport(tcp); err != nil {
		t.Fatalf("mgr.TrackTransport(tcp) error = %v, want nil", err)
	}

	if got := mgr.GetTransport("udp"); got != udp {
		t.Fatalf("mgr.GetTransport(\"udp\") = %v, want udp", got)
	}

	if got := mgr.GetTransport("tls"); got != nil {
		t.Fatalf("mgr.GetTransport(\"tls\") = %v, want nil", got)
	}

	tpsCnt := 0
	for range mgr.AllTransports() {
		tpsCnt++
	}

	if got, want := tpsCnt, 2; got != want {
		t.Fatalf("len(mgr.AllTransports()) = %v, want %v", got, want)
	}

	if err := mgr.UntrackTransport(nil); !errors.Is(err, sip.ErrInvalidArgument) {
		t.Fatalf("mgr.UntrackTransport(nil) error = %v, want wraps %v", err, sip.ErrInvalidArgument)
	}

	if err := mgr.UntrackTransport(udp); err != nil {
		t.Fatalf("mgr.UntrackTransport(udp) error = %v, want nil", err)
	}

	if got := mgr.GetTransport("UDP"); got != nil {
		t.Fatalf("mgr.GetTransport(\"UDP\") after untrack = %v, want nil", got)
	}

	if err := mgr.SendRequest(t.Context(), newTestOutboundRequestEnvelope(t, "TLS"), nil); !errors.Is(err, sip.ErrNoTransport) {
		t.Fatalf("mgr.SendRequest(unknown proto) error = %v, want wraps %v", err, sip.ErrNoTransport)
	}

	if err := mgr.UntrackTransport(udp); err != nil {
		t.Fatalf("mgr.UntrackTransport(udp second time) error = %v, want nil", err)
	}
}

func TestTransportManager_InterceptorLifecycle(t *testing.T) {
	t.Parallel()

	var mgr sip.TransportManager

	tp := newSpyTransport(sip.UDPTransportMetadata())
	if err := mgr.TrackTransport(tp); err != nil {
		t.Fatalf("mgr.TrackTransport(tp) error = %v, want nil", err)
	}

	inReqUnbind := mgr.UseInboundRequestInterceptor(
		sip.InboundRequestInterceptorFunc(func(ctx context.Context, req *sip.InboundRequestEnvelope, next sip.RequestReceiver) error {
			return next.RecvRequest(ctx, req)
		}),
	)
	inResUnbind := mgr.UseInboundResponseInterceptor(
		sip.InboundResponseInterceptorFunc(func(ctx context.Context, res *sip.InboundResponseEnvelope, next sip.ResponseReceiver) error {
			return next.RecvResponse(ctx, res)
		}),
	)
	outReqUnbind := mgr.UseOutboundRequestInterceptor(
		sip.OutboundRequestInterceptorFunc(func(ctx context.Context, req *sip.OutboundRequestEnvelope, opts *sip.SendRequestOptions, next sip.RequestSender) error {
			return next.SendRequest(ctx, req, opts)
		}),
	)
	outResUnbind := mgr.UseOutboundResponseInterceptor(
		sip.OutboundResponseInterceptorFunc(func(ctx context.Context, res *sip.OutboundResponseEnvelope, opts *sip.SendResponseOptions, next sip.ResponseSender) error {
			return next.SendResponse(ctx, res, opts)
		}),
	)

	c := tp.counts()
	if got, want := c.bindInReqCalls, 1; got != want {
		t.Fatalf("bind inbound request calls = %v, want %v", got, want)
	}

	if got, want := c.bindInResCalls, 1; got != want {
		t.Fatalf("bind inbound response calls = %v, want %v", got, want)
	}

	if got, want := c.bindOutReqCalls, 1; got != want {
		t.Fatalf("bind outbound request calls = %v, want %v", got, want)
	}

	if got, want := c.bindOutResCalls, 1; got != want {
		t.Fatalf("bind outbound response calls = %v, want %v", got, want)
	}

	inReqUnbind()
	inResUnbind()
	outReqUnbind()
	outResUnbind()

	c = tp.counts()
	if got, want := c.unbindInReqCalls, 1; got != want {
		t.Fatalf("unbind inbound request calls = %v, want %v", got, want)
	}

	if got, want := c.unbindInResCalls, 1; got != want {
		t.Fatalf("unbind inbound response calls = %v, want %v", got, want)
	}

	if got, want := c.unbindOutReqCalls, 1; got != want {
		t.Fatalf("unbind outbound request calls = %v, want %v", got, want)
	}

	if got, want := c.unbindOutResCalls, 1; got != want {
		t.Fatalf("unbind outbound response calls = %v, want %v", got, want)
	}

	var mgr2 sip.TransportManager

	preTrackUnbind := mgr2.UseOutboundRequestInterceptor(
		sip.OutboundRequestInterceptorFunc(func(ctx context.Context, req *sip.OutboundRequestEnvelope, opts *sip.SendRequestOptions, next sip.RequestSender) error {
			return next.SendRequest(ctx, req, opts)
		}),
	)

	tp2 := newSpyTransport(sip.TCPTransportMetadata())
	if err := mgr2.TrackTransport(tp2); err != nil {
		t.Fatalf("mgr2.TrackTransport(tp2) error = %v, want nil", err)
	}

	if got, want := tp2.counts().bindOutReqCalls, 1; got != want {
		t.Fatalf("pre-track bind outbound request calls = %v, want %v", got, want)
	}

	preTrackUnbind()

	if got, want := tp2.counts().unbindOutReqCalls, 1; got != want {
		t.Fatalf("pre-track unbind outbound request calls = %v, want %v", got, want)
	}
}

func TestTransportManager_UseInterceptor_BindsOnlyNonNil(t *testing.T) {
	t.Parallel()

	var mgr sip.TransportManager

	tp := newSpyTransport(sip.UDPTransportMetadata())
	if err := mgr.TrackTransport(tp); err != nil {
		t.Fatalf("mgr.TrackTransport(tp) error = %v, want nil", err)
	}

	unbind := mgr.UseInterceptor(testMessageInterceptor{
		inReq: sip.InboundRequestInterceptorFunc(func(ctx context.Context, req *sip.InboundRequestEnvelope, next sip.RequestReceiver) error {
			return next.RecvRequest(ctx, req)
		}),
		outRes: sip.OutboundResponseInterceptorFunc(
			func(ctx context.Context, res *sip.OutboundResponseEnvelope, opts *sip.SendResponseOptions, next sip.ResponseSender) error {
				return next.SendResponse(ctx, res, opts)
			},
		),
	})

	c := tp.counts()
	if got, want := c.bindInReqCalls, 1; got != want {
		t.Fatalf("bind inbound request calls = %v, want %v", got, want)
	}

	if got, want := c.bindOutResCalls, 1; got != want {
		t.Fatalf("bind outbound response calls = %v, want %v", got, want)
	}

	if got := c.bindInResCalls + c.bindOutReqCalls; got != 0 {
		t.Fatalf("unexpected binds for nil interceptors = %v, want 0", got)
	}

	unbind()

	c = tp.counts()
	if got, want := c.unbindInReqCalls, 1; got != want {
		t.Fatalf("unbind inbound request calls = %v, want %v", got, want)
	}

	if got, want := c.unbindOutResCalls, 1; got != want {
		t.Fatalf("unbind outbound response calls = %v, want %v", got, want)
	}
}

func TestTransportManager_Delegation(t *testing.T) {
	t.Parallel()

	t.Run("send request by proto and default", func(t *testing.T) {
		t.Parallel()

		var mgr sip.TransportManager

		udp := newSpyTransport(sip.UDPTransportMetadata())
		tcp := newSpyTransport(sip.TCPTransportMetadata())

		if err := mgr.TrackTransport(udp); err != nil {
			t.Fatalf("mgr.TrackTransport(udp) error = %v, want nil", err)
		}

		if err := mgr.TrackTransport(tcp); err != nil {
			t.Fatalf("mgr.TrackTransport(tcp) error = %v, want nil", err)
		}

		if err := mgr.SendRequest(t.Context(), newTestOutboundRequestEnvelope(t, "TCP"), nil); err != nil {
			t.Fatalf("mgr.SendRequest(tcp) error = %v, want nil", err)
		}

		if err := mgr.SendRequest(t.Context(), newTestOutboundRequestEnvelope(t, "TLS"), nil); !errors.Is(err, sip.ErrNoTransport) {
			t.Fatalf("mgr.SendRequest(unknown proto) error = %v, want wraps %v", err, sip.ErrNoTransport)
		}

		if got, want := tcp.counts().sendReqCalls, 1; got != want {
			t.Fatalf("tcp.SendRequest calls = %v, want %v", got, want)
		}

		if got, want := udp.counts().sendReqCalls, 0; got != want {
			t.Fatalf("udp.SendRequest calls = %v, want %v", got, want)
		}
	})

	t.Run("send response by proto and default", func(t *testing.T) {
		t.Parallel()

		var mgr sip.TransportManager

		udp := newSpyTransport(sip.UDPTransportMetadata())
		tcp := newSpyTransport(sip.TCPTransportMetadata())

		if err := mgr.TrackTransport(udp); err != nil {
			t.Fatalf("mgr.TrackTransport(udp) error = %v, want nil", err)
		}

		if err := mgr.TrackTransport(tcp); err != nil {
			t.Fatalf("mgr.TrackTransport(tcp) error = %v, want nil", err)
		}

		if err := mgr.SendResponse(t.Context(), newTestOutboundResponseEnvelope(t, "TCP"), nil); err != nil {
			t.Fatalf("mgr.SendResponse(tcp) error = %v, want nil", err)
		}

		if err := mgr.SendResponse(t.Context(), newTestOutboundResponseEnvelope(t, "TLS"), nil); !errors.Is(err, sip.ErrNoTransport) {
			t.Fatalf("mgr.SendResponse(unknown proto) error = %v, want wraps %v", err, sip.ErrNoTransport)
		}

		if got, want := tcp.counts().sendResCalls, 1; got != want {
			t.Fatalf("tcp.SendResponse calls = %v, want %v", got, want)
		}

		if got, want := udp.counts().sendResCalls, 0; got != want {
			t.Fatalf("udp.SendResponse calls = %v, want %v", got, want)
		}
	})

	t.Run("listen and serve by proto and default", func(t *testing.T) {
		t.Parallel()

		var mgr sip.TransportManager

		udp := newSpyTransport(sip.UDPTransportMetadata())
		tcp := newSpyTransport(sip.TCPTransportMetadata())

		if err := mgr.TrackTransport(udp); err != nil {
			t.Fatalf("mgr.TrackTransport(udp) error = %v, want nil", err)
		}

		if err := mgr.TrackTransport(tcp); err != nil {
			t.Fatalf("mgr.TrackTransport(tcp) error = %v, want nil", err)
		}

		if err := mgr.ListenAndServe(t.Context(), "TCP", "127.0.0.1:5080"); err != nil {
			t.Fatalf("mgr.ListenAndServe(tcp) error = %v, want nil", err)
		}

		if err := mgr.ListenAndServe(t.Context(), "TLS", "127.0.0.1:5081"); !errors.Is(err, sip.ErrNoTransport) {
			t.Fatalf("mgr.ListenAndServe(unknown proto) error = %v, want wraps %v", err, sip.ErrNoTransport)
		}

		if got, want := tcp.counts().listenCalls, 1; got != want {
			t.Fatalf("tcp.ListenAndServe calls = %v, want %v", got, want)
		}

		if got, want := udp.counts().listenCalls, 0; got != want {
			t.Fatalf("udp.ListenAndServe calls = %v, want %v", got, want)
		}
	})

	t.Run("returns ErrNoTransport without tracked and default transport", func(t *testing.T) {
		t.Parallel()

		var mgr sip.TransportManager

		if err := mgr.SendRequest(t.Context(), newTestOutboundRequestEnvelope(t, "UDP"), nil); !errors.Is(err, sip.ErrNoTransport) {
			t.Fatalf("mgr.SendRequest() error = %v, want wraps %v", err, sip.ErrNoTransport)
		}

		if err := mgr.SendResponse(t.Context(), newTestOutboundResponseEnvelope(t, "UDP"), nil); !errors.Is(err, sip.ErrNoTransport) {
			t.Fatalf("mgr.SendResponse() error = %v, want wraps %v", err, sip.ErrNoTransport)
		}

		if err := mgr.ListenAndServe(t.Context(), "UDP", "127.0.0.1:5090"); !errors.Is(err, sip.ErrNoTransport) {
			t.Fatalf("mgr.ListenAndServe() error = %v, want wraps %v", err, sip.ErrNoTransport)
		}
	})
}

func TestTransportManager_Respond_DelegatesToTransport(t *testing.T) {
	t.Parallel()

	var mgr sip.TransportManager

	tp := newSpyTransport(sip.UDPTransportMetadata())
	if err := mgr.TrackTransport(tp); err != nil {
		t.Fatalf("mgr.TrackTransport(tp) error = %v, want nil", err)
	}

	if err := mgr.Respond(t.Context(), newTestInboundRequestEnvelope(t, "UDP"), sip.ResponseStatusOK, nil); err != nil {
		t.Fatalf("mgr.Respond() error = %v, want nil", err)
	}

	if got, want := tp.counts().sendResCalls, 1; got != want {
		t.Fatalf("tp.SendResponse calls = %v, want %v", got, want)
	}
}

func TestTransportManager_CloseAndClosedGuards(t *testing.T) {
	t.Parallel()

	closeErr := errors.New("close boom")

	var mgr sip.TransportManager

	udp := newSpyTransport(sip.UDPTransportMetadata())
	tcp := newSpyTransport(sip.TCPTransportMetadata())
	tcp.closeErr = closeErr

	if err := mgr.TrackTransport(udp); err != nil {
		t.Fatalf("mgr.TrackTransport(udp) error = %v, want nil", err)
	}

	if err := mgr.TrackTransport(tcp); err != nil {
		t.Fatalf("mgr.TrackTransport(tcp) error = %v, want nil", err)
	}

	var err error
	if err = mgr.Close(); err == nil {
		t.Fatal("mgr.Close() error = nil, want non-nil")
	}

	if !strings.Contains(err.Error(), closeErr.Error()) {
		t.Fatalf("mgr.Close() error = %v, want contain %q", err, closeErr)
	}

	if got, want := udp.counts().closeCalls, 1; got != want {
		t.Fatalf("udp.Close calls = %v, want %v", got, want)
	}

	if got, want := tcp.counts().closeCalls, 1; got != want {
		t.Fatalf("tcp.Close calls = %v, want %v", got, want)
	}

	if !strings.Contains(err.Error(), closeErr.Error()) {
		t.Fatalf("mgr.Close() error = %v, want contain %q", err, closeErr)
	}

	if got, want := udp.counts().closeCalls, 1; got != want {
		t.Fatalf("udp.Close calls after second close = %v, want %v", got, want)
	}

	if got, want := tcp.counts().closeCalls, 1; got != want {
		t.Fatalf("tcp.Close calls after second close = %v, want %v", got, want)
	}

	if err := mgr.TrackTransport(newSpyTransport(sip.TLSTransportMetadata())); !errors.Is(err, sip.ErrTransportManagerClosed) {
		t.Fatalf("mgr.TrackTransport() when closed error = %v, want wraps %v", err, sip.ErrTransportManagerClosed)
	}

	if err := mgr.UntrackTransport(udp); !errors.Is(err, sip.ErrTransportManagerClosed) {
		t.Fatalf("mgr.UntrackTransport() when closed error = %v, want wraps %v", err, sip.ErrTransportManagerClosed)
	}

	if err := mgr.SendRequest(t.Context(), newTestOutboundRequestEnvelope(t, "UDP"), nil); !errors.Is(err, sip.ErrTransportManagerClosed) {
		t.Fatalf("mgr.SendRequest() when closed error = %v, want wraps %v", err, sip.ErrTransportManagerClosed)
	}

	if err := mgr.SendResponse(t.Context(), newTestOutboundResponseEnvelope(t, "UDP"), nil); !errors.Is(err, sip.ErrTransportManagerClosed) {
		t.Fatalf("mgr.SendResponse() when closed error = %v, want wraps %v", err, sip.ErrTransportManagerClosed)
	}

	if err := mgr.Respond(t.Context(), newTestInboundRequestEnvelope(t, "UDP"), sip.ResponseStatusOK, nil); !errors.Is(err, sip.ErrTransportManagerClosed) {
		t.Fatalf("mgr.Respond() when closed error = %v, want wraps %v", err, sip.ErrTransportManagerClosed)
	}

	if err := mgr.ListenAndServe(t.Context(), "UDP", "127.0.0.1:5090"); !errors.Is(err, sip.ErrTransportManagerClosed) {
		t.Fatalf("mgr.ListenAndServe() when closed error = %v, want wraps %v", err, sip.ErrTransportManagerClosed)
	}
}
