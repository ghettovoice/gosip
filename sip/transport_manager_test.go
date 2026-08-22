package sip_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
)

type blockingManagerTransport struct {
	sip.StdMsgInterceptChain
	meta           sip.TransportMetadata
	closeStarted   chan struct{}
	releaseClose   chan struct{}
	closeStartOnce sync.Once
}

func newBlockingManagerTransport(meta sip.TransportMetadata) *blockingManagerTransport {
	return &blockingManagerTransport{
		meta:         meta,
		closeStarted: make(chan struct{}),
		releaseClose: make(chan struct{}),
	}
}

func (tp *blockingManagerTransport) Metadata() sip.TransportMetadata { return tp.meta }

func (*blockingManagerTransport) SendRequest(context.Context, *sip.RequestEnvelope, ...sip.SendRequestOptions) error {
	return nil
}

func (*blockingManagerTransport) SendResponse(context.Context, *sip.ResponseEnvelope, ...sip.SendResponseOptions) error {
	return nil
}

func (*blockingManagerTransport) Respond(context.Context, *sip.RequestEnvelope, sip.ResponseStatus, ...sip.RespondOptions) error {
	return nil
}

func (*blockingManagerTransport) Listen(context.Context, string) (sip.TransportListener, error) {
	return nil, errors.New("listen not supported in test")
}

func (*blockingManagerTransport) MatchSentBy(sip.Addr) bool { return false }

func (tp *blockingManagerTransport) Close(context.Context) error {
	tp.closeStartOnce.Do(func() { close(tp.closeStarted) })
	<-tp.releaseClose
	return nil
}

func TestTransportManager_CloseRejectsConcurrentTrack(t *testing.T) {
	t.Parallel()

	var manager sip.TransportManager
	first := newBlockingManagerTransport(sip.UDPMetadata())
	if err := manager.TrackTransport(first); err != nil {
		t.Fatalf("manager.TrackTransport(first) error = %v, want nil", err)
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- manager.Close(context.Background()) }()

	select {
	case <-first.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("manager.Close() did not start closing the tracked transport")
	}

	second := newBlockingManagerTransport(sip.TCPMetadata())
	if err := manager.TrackTransport(second); !errors.Is(err, sip.ErrTransportManagerClosed) {
		t.Fatalf("manager.TrackTransport(second) error = %v, want %v", err, sip.ErrTransportManagerClosed)
	}

	close(first.releaseClose)
	if err := <-closeDone; err != nil {
		t.Fatalf("manager.Close() error = %v, want nil", err)
	}
}

var _ sip.Transport = (*blockingManagerTransport)(nil)

type spyTransport struct {
	mu sync.Mutex

	meta       sip.TransportMetadata
	closeErr   error
	listenErr  error
	sendReqErr error
	sendResErr error
	respondErr error

	closeCalls   int
	listenCalls  int
	sendReqCalls int
	sendResCalls int
	respondCalls int

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
	respondCalls int

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

func (s *spyTransport) SendRequest(context.Context, *sip.RequestEnvelope, ...sip.SendRequestOptions) error {
	s.mu.Lock()
	s.sendReqCalls++
	err := s.sendReqErr
	s.mu.Unlock()

	return err
}

func (s *spyTransport) SendResponse(context.Context, *sip.ResponseEnvelope, ...sip.SendResponseOptions) error {
	s.mu.Lock()
	s.sendResCalls++
	err := s.sendResErr
	s.mu.Unlock()

	return err
}

func (s *spyTransport) Respond(context.Context, *sip.RequestEnvelope, sip.ResponseStatus, ...sip.RespondOptions) error {
	s.mu.Lock()
	s.respondCalls++
	err := s.respondErr
	s.mu.Unlock()

	return err
}

func (s *spyTransport) Listen(context.Context, string) (sip.TransportListener, error) {
	s.mu.Lock()
	s.listenCalls++
	err := s.listenErr
	s.mu.Unlock()

	return nil, err
}

func (*spyTransport) MatchSentBy(sip.Addr) bool { return false }

func (s *spyTransport) Close(context.Context) error {
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

func (s *spyTransport) UseMessageInterceptor(interceptor sip.MessageInterceptor) func() {
	if interceptor == nil {
		return func() {}
	}

	unbinds := []func(){
		s.UseInboundRequestInterceptor(interceptor),
		s.UseInboundResponseInterceptor(interceptor),
		s.UseOutboundRequestInterceptor(interceptor),
		s.UseOutboundResponseInterceptor(interceptor),
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
		respondCalls: s.respondCalls,

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

func (m testMessageInterceptor) InterceptInboundRequest(
	ctx context.Context,
	next sip.RequestReceiver,
	req *sip.RequestEnvelope,
) error {
	return m.inReq.InterceptInboundRequest(ctx, next, req)
}

func (m testMessageInterceptor) InterceptInboundResponse(
	ctx context.Context,
	next sip.ResponseReceiver,
	res *sip.ResponseEnvelope,
) error {
	return m.inRes.InterceptInboundResponse(ctx, next, res)
}

func (m testMessageInterceptor) InterceptOutboundRequest(
	ctx context.Context,
	next sip.RequestSender,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	return m.outReq.InterceptOutboundRequest(ctx, next, req, opts...)
}

func (m testMessageInterceptor) InterceptOutboundResponse(
	ctx context.Context,
	next sip.ResponseSender,
	res *sip.ResponseEnvelope,
	opts ...sip.SendResponseOptions,
) error {
	return m.outRes.InterceptOutboundResponse(ctx, next, res, opts...)
}

func newTestRequest(tb testing.TB, tp sip.TransportProto) *sip.Request {
	tb.Helper()

	ruri := &sip.URI{User: sip.UserWithName("alice"), Addr: sip.AddrFromHost("example.com")}
	furi := &sip.URI{User: sip.UserWithName("bob"), Addr: sip.AddrFromHost("example.com")}
	turi := &sip.URI{User: sip.UserWithName("alice"), Addr: sip.AddrFromHost("example.com")}

	req, err := sip.NewRequest(sip.RequestMethodInvite, ruri, furi, turi, sip.RequestOptions{Transport: tp})
	if err != nil {
		tb.Fatalf("sip.NewRequest() error = %v, want nil", err)
	}

	return req
}

func newTestRequestEnvelope(tb testing.TB, tp sip.TransportProto) *sip.RequestEnvelope {
	tb.Helper()

	return sip.NewRequestEnvelope(newTestRequest(tb, tp)).
		SetTransport(testTransportMetadata(tp)).
		SetLocalAddr(netip.MustParseAddrPort("127.0.0.1:5060")).
		SetRemoteAddr(netip.MustParseAddrPort("127.0.0.1:5070"))
}

func newTestResponseEnvelope(tb testing.TB, tp sip.TransportProto) *sip.ResponseEnvelope {
	tb.Helper()

	req := newTestRequest(tb, tp)

	res, err := req.NewResponse(sip.ResponseStatusOK)
	if err != nil {
		tb.Fatalf("req.NewResponse() error = %v, want nil", err)
	}

	return sip.NewResponseEnvelope(res).
		SetTransport(testTransportMetadata(tp)).
		SetLocalAddr(netip.MustParseAddrPort("127.0.0.1:5060")).
		SetRemoteAddr(netip.MustParseAddrPort("127.0.0.1:5070"))
}

func TestTransportManager_TrackGetUntrack(t *testing.T) {
	t.Parallel()

	var mgr sip.TransportManager

	udp := newSpyTransport(sip.UDPMetadata())
	tcp := newSpyTransport(sip.TCPMetadata())

	if err := mgr.TrackTransport(nil); err == nil {
		t.Fatal("mgr.TrackTransport(nil) error = nil, want non-nil")
	}

	if err := mgr.TrackTransport(udp); err != nil {
		t.Fatalf("mgr.TrackTransport(udp) error = %v, want nil", err)
	}

	if err := mgr.TrackTransport(tcp); err != nil {
		t.Fatalf("mgr.TrackTransport(tcp) error = %v, want nil", err)
	}

	got, ok := mgr.TransportByProto("udp")
	if !ok {
		t.Fatalf("mgr.GetTransport(\"udp\") not found")
	}

	if got != udp {
		t.Fatalf("mgr.GetTransport(\"udp\") = %v, want udp", got)
	}

	if _, ok := mgr.TransportByProto("tls"); ok {
		t.Fatalf("mgr.GetTransport(\"tls\") should not be found")
	}

	tpsCnt := 0
	for range mgr.AllTransports() {
		tpsCnt++
	}

	if got, want := tpsCnt, 2; got != want {
		t.Fatalf("len(mgr.AllTransports()) = %v, want %v", got, want)
	}

	if err := mgr.UntrackTransport(nil); err == nil {
		t.Fatal("mgr.UntrackTransport(nil) error = nil, want non-nil")
	}

	if err := mgr.UntrackTransport(udp); err != nil {
		t.Fatalf("mgr.UntrackTransport(udp) error = %v, want nil", err)
	}

	if _, ok := mgr.TransportByProto("UDP"); ok {
		t.Fatalf("mgr.GetTransport(\"UDP\") should not be found")
	}

	if err := mgr.SendRequest(t.Context(), newTestRequestEnvelope(t, "TLS")); !errors.Is(err, sip.ErrNoTransport) {
		t.Fatalf("mgr.SendRequest(unknown proto) error = %v, want wraps %v", err, sip.ErrNoTransport)
	}

	if err := mgr.UntrackTransport(udp); err != nil {
		t.Fatalf("mgr.UntrackTransport(udp second time) error = %v, want nil", err)
	}
}

func TestTransportManager_InterceptorLifecycle(t *testing.T) {
	t.Parallel()

	var mgr sip.TransportManager

	tp := newSpyTransport(sip.UDPMetadata())
	if err := mgr.TrackTransport(tp); err != nil {
		t.Fatalf("mgr.TrackTransport(tp) error = %v, want nil", err)
	}

	inReqUnbind := mgr.UseInboundRequestInterceptor(
		sip.InboundRequestInterceptorFunc(
			func(ctx context.Context, next sip.RequestReceiver, req *sip.RequestEnvelope) error {
				return next.RecvRequest(ctx, req)
			},
		),
	)
	inResUnbind := mgr.UseInboundResponseInterceptor(
		sip.InboundResponseInterceptorFunc(
			func(ctx context.Context, next sip.ResponseReceiver, res *sip.ResponseEnvelope) error {
				return next.RecvResponse(ctx, res)
			},
		),
	)
	outReqUnbind := mgr.UseOutboundRequestInterceptor(
		sip.OutboundRequestInterceptorFunc(
			func(
				ctx context.Context,
				next sip.RequestSender,
				req *sip.RequestEnvelope,
				opts ...sip.SendRequestOptions,
			) error {
				return next.SendRequest(ctx, req, opts...)
			},
		),
	)
	outResUnbind := mgr.UseOutboundResponseInterceptor(
		sip.OutboundResponseInterceptorFunc(
			func(
				ctx context.Context,
				next sip.ResponseSender,
				res *sip.ResponseEnvelope,
				opts ...sip.SendResponseOptions,
			) error {
				return next.SendResponse(ctx, res, opts...)
			},
		),
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
		sip.OutboundRequestInterceptorFunc(
			func(
				ctx context.Context,
				next sip.RequestSender,
				req *sip.RequestEnvelope,
				opts ...sip.SendRequestOptions,
			) error {
				return next.SendRequest(ctx, req, opts...)
			},
		),
	)

	tp2 := newSpyTransport(sip.TCPMetadata())
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

func TestTransportManager_UseMessageInterceptor_BindsAll(t *testing.T) {
	t.Parallel()

	var mgr sip.TransportManager

	tp := newSpyTransport(sip.UDPMetadata())
	if err := mgr.TrackTransport(tp); err != nil {
		t.Fatalf("mgr.TrackTransport(tp) error = %v, want nil", err)
	}

	unbind := mgr.UseMessageInterceptor(testMessageInterceptor{
		inReq: sip.InboundRequestInterceptorFunc(
			func(ctx context.Context, next sip.RequestReceiver, req *sip.RequestEnvelope) error {
				return next.RecvRequest(ctx, req)
			},
		),
		outRes: sip.OutboundResponseInterceptorFunc(
			func(
				ctx context.Context,
				next sip.ResponseSender,
				res *sip.ResponseEnvelope,
				opts ...sip.SendResponseOptions,
			) error {
				return next.SendResponse(ctx, res, opts...)
			},
		),
	})

	c := tp.counts()
	if got, want := c.bindInReqCalls+c.bindInResCalls+c.bindOutReqCalls+c.bindOutResCalls, 4; got != want {
		t.Fatalf("total bind calls = %v, want %v", got, want)
	}

	unbind()

	c = tp.counts()
	if got, want := c.unbindInReqCalls+c.unbindInResCalls+c.unbindOutReqCalls+c.unbindOutResCalls, 4; got != want {
		t.Fatalf("total unbind calls = %v, want %v", got, want)
	}
}

func TestTransportManager_Delegation(t *testing.T) {
	t.Parallel()

	t.Run("send request by proto and default", func(t *testing.T) {
		t.Parallel()

		var mgr sip.TransportManager

		udp := newSpyTransport(sip.UDPMetadata())
		tcp := newSpyTransport(sip.TCPMetadata())

		if err := mgr.TrackTransport(udp); err != nil {
			t.Fatalf("mgr.TrackTransport(udp) error = %v, want nil", err)
		}

		if err := mgr.TrackTransport(tcp); err != nil {
			t.Fatalf("mgr.TrackTransport(tcp) error = %v, want nil", err)
		}

		if err := mgr.SendRequest(t.Context(), newTestRequestEnvelope(t, "TCP")); err != nil {
			t.Fatalf("mgr.SendRequest(tcp) error = %v, want nil", err)
		}

		if err := mgr.SendRequest(t.Context(), newTestRequestEnvelope(t, "TLS")); !errors.Is(err, sip.ErrNoTransport) {
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

		udp := newSpyTransport(sip.UDPMetadata())
		tcp := newSpyTransport(sip.TCPMetadata())

		if err := mgr.TrackTransport(udp); err != nil {
			t.Fatalf("mgr.TrackTransport(udp) error = %v, want nil", err)
		}

		if err := mgr.TrackTransport(tcp); err != nil {
			t.Fatalf("mgr.TrackTransport(tcp) error = %v, want nil", err)
		}

		if err := mgr.SendResponse(t.Context(), newTestResponseEnvelope(t, "TCP")); err != nil {
			t.Fatalf("mgr.SendResponse(tcp) error = %v, want nil", err)
		}

		if err := mgr.SendResponse(t.Context(), newTestResponseEnvelope(t, "TLS")); !errors.Is(err, sip.ErrNoTransport) {
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

		udp := newSpyTransport(sip.UDPMetadata())
		tcp := newSpyTransport(sip.TCPMetadata())

		if err := mgr.TrackTransport(udp); err != nil {
			t.Fatalf("mgr.TrackTransport(udp) error = %v, want nil", err)
		}

		if err := mgr.TrackTransport(tcp); err != nil {
			t.Fatalf("mgr.TrackTransport(tcp) error = %v, want nil", err)
		}

		if _, err := mgr.Listen(t.Context(), "TCP", "127.0.0.1:5080"); err != nil {
			t.Fatalf("mgr.Listen(tcp) error = %v, want nil", err)
		}

		if _, err := mgr.Listen(t.Context(), "TLS", "127.0.0.1:5081"); !errors.Is(err, sip.ErrNoTransport) {
			t.Fatalf("mgr.Listen(unknown proto) error = %v, want wraps %v", err, sip.ErrNoTransport)
		}

		if got, want := tcp.counts().listenCalls, 1; got != want {
			t.Fatalf("tcp.Listen calls = %v, want %v", got, want)
		}

		if got, want := udp.counts().listenCalls, 0; got != want {
			t.Fatalf("udp.Listen calls = %v, want %v", got, want)
		}
	})

	t.Run("returns ErrNoTransport without tracked and default transport", func(t *testing.T) {
		t.Parallel()

		var mgr sip.TransportManager

		if err := mgr.SendRequest(t.Context(), newTestRequestEnvelope(t, "UDP")); !errors.Is(err, sip.ErrNoTransport) {
			t.Fatalf("mgr.SendRequest() error = %v, want wraps %v", err, sip.ErrNoTransport)
		}

		if err := mgr.SendResponse(t.Context(), newTestResponseEnvelope(t, "UDP")); !errors.Is(err, sip.ErrNoTransport) {
			t.Fatalf("mgr.SendResponse() error = %v, want wraps %v", err, sip.ErrNoTransport)
		}

		if _, err := mgr.Listen(t.Context(), "UDP", "127.0.0.1:5090"); !errors.Is(err, sip.ErrNoTransport) {
			t.Fatalf("mgr.Listen() error = %v, want wraps %v", err, sip.ErrNoTransport)
		}
	})
}

func TestTransportManager_Respond_DelegatesToTransport(t *testing.T) {
	t.Parallel()

	var mgr sip.TransportManager

	tp := newSpyTransport(sip.UDPMetadata())
	if err := mgr.TrackTransport(tp); err != nil {
		t.Fatalf("mgr.TrackTransport(tp) error = %v, want nil", err)
	}

	if err := mgr.Respond(t.Context(), newTestRequestEnvelope(t, "UDP"), sip.ResponseStatusOK); err != nil {
		t.Fatalf("mgr.Respond() error = %v, want nil", err)
	}

	if got, want := tp.counts().respondCalls, 1; got != want {
		t.Fatalf("tp.Respond calls = %v, want %v", got, want)
	}
}

func TestTransportManager_CloseAndClosedGuards(t *testing.T) {
	t.Parallel()

	closeErr := errors.New("close boom")

	var mgr sip.TransportManager

	udp := newSpyTransport(sip.UDPMetadata())
	tcp := newSpyTransport(sip.TCPMetadata())
	tcp.closeErr = closeErr

	if err := mgr.TrackTransport(udp); err != nil {
		t.Fatalf("mgr.TrackTransport(udp) error = %v, want nil", err)
	}

	if err := mgr.TrackTransport(tcp); err != nil {
		t.Fatalf("mgr.TrackTransport(tcp) error = %v, want nil", err)
	}

	var err error
	if err = mgr.Close(t.Context()); err == nil {
		t.Fatal("mgr.Close(t.Context()) error = nil, want non-nil")
	}

	if !strings.Contains(err.Error(), closeErr.Error()) {
		t.Fatalf("mgr.Close(t.Context()) error = %v, want contain %q", err, closeErr)
	}

	if got, want := udp.counts().closeCalls, 1; got != want {
		t.Fatalf("udp.Close calls = %v, want %v", got, want)
	}

	if got, want := tcp.counts().closeCalls, 1; got != want {
		t.Fatalf("tcp.Close calls = %v, want %v", got, want)
	}

	if !strings.Contains(err.Error(), closeErr.Error()) {
		t.Fatalf("mgr.Close(t.Context()) error = %v, want contain %q", err, closeErr)
	}

	if got, want := udp.counts().closeCalls, 1; got != want {
		t.Fatalf("udp.Close calls after second close = %v, want %v", got, want)
	}

	if got, want := tcp.counts().closeCalls, 1; got != want {
		t.Fatalf("tcp.Close calls after second close = %v, want %v", got, want)
	}

	if err := mgr.TrackTransport(newSpyTransport(sip.TLSMetadata())); !errors.Is(err, sip.ErrTransportManagerClosed) {
		t.Fatalf("mgr.TrackTransport() when closed error = %v, want wraps %v", err, sip.ErrTransportManagerClosed)
	}

	if err := mgr.UntrackTransport(udp); !errors.Is(err, sip.ErrTransportManagerClosed) {
		t.Fatalf("mgr.UntrackTransport() when closed error = %v, want wraps %v", err, sip.ErrTransportManagerClosed)
	}

	if err := mgr.SendRequest(t.Context(), newTestRequestEnvelope(t, "UDP")); !errors.Is(err, sip.ErrTransportManagerClosed) {
		t.Fatalf("mgr.SendRequest() when closed error = %v, want wraps %v", err, sip.ErrTransportManagerClosed)
	}

	if err := mgr.SendResponse(t.Context(), newTestResponseEnvelope(t, "UDP")); !errors.Is(err, sip.ErrTransportManagerClosed) {
		t.Fatalf("mgr.SendResponse() when closed error = %v, want wraps %v", err, sip.ErrTransportManagerClosed)
	}

	if err := mgr.Respond(t.Context(), newTestRequestEnvelope(t, "UDP"), sip.ResponseStatusOK); !errors.Is(err, sip.ErrTransportManagerClosed) {
		t.Fatalf("mgr.Respond() when closed error = %v, want wraps %v", err, sip.ErrTransportManagerClosed)
	}

	if _, err := mgr.Listen(t.Context(), "UDP", "127.0.0.1:5090"); !errors.Is(err, sip.ErrTransportManagerClosed) {
		t.Fatalf("mgr.Listen() when closed error = %v, want wraps %v", err, sip.ErrTransportManagerClosed)
	}
}
