package sip_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/dns"
	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

const asyncEventTimeout = 5 * time.Second

type spyPacketListenConfig struct {
	called  int
	network string
	addr    string
	conn    net.PacketConn
	err     error
}

func newViaHop(tb testing.TB, transport sip.TransportProto, addr header.Addr) header.ViaHop {
	tb.Helper()

	return header.ViaHop{
		Proto:     sip.ProtoVer20(),
		Transport: transport,
		Addr:      addr,
		Params:    make(header.Values).Set("branch", sip.GenerateBranch(0)),
	}
}

func (s *spyPacketListenConfig) ListenPacket(ctx context.Context, nt, addr string) (net.PacketConn, error) {
	s.called++
	s.network = nt
	s.addr = addr
	return s.conn, s.err
}

type spyConnListenConfig struct {
	called  int
	network string
	addr    string
	lis     net.Listener
	err     error
}

func (s *spyConnListenConfig) Listen(ctx context.Context, nt, addr string) (net.Listener, error) {
	s.called++
	s.network = nt
	s.addr = addr
	return s.lis, s.err
}

type signalListener struct {
	net.Listener
	acceptCalled chan struct{}
}

func (l *signalListener) Accept() (net.Conn, error) {
	select {
	case <-l.acceptCalled:
	default:
		close(l.acceptCalled)
	}

	return l.Listener.Accept()
}

type tempError struct{}

func (tempError) Error() string   { return "temporary accept error" }
func (tempError) Temporary() bool { return true }

type tempErrListener struct {
	net.Listener
	errOnce sync.Once
}

func (l *tempErrListener) Accept() (net.Conn, error) {
	var err error
	l.errOnce.Do(func() { err = tempError{} })

	if err != nil {
		return nil, err
	}

	return l.Listener.Accept()
}

type stubDNSResolver struct {
	lookupIP    func(ctx context.Context, network, host string) ([]net.IP, error)
	lookupSRV   func(ctx context.Context, service, proto, host string) ([]*dns.SRV, error)
	lookupNAPTR func(ctx context.Context, host string) ([]*dns.NAPTR, error)
}

func (s stubDNSResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	if s.lookupIP == nil {
		return nil, errors.New("lookup ip not configured")
	}
	return s.lookupIP(ctx, network, host)
}

func (s stubDNSResolver) LookupSRV(ctx context.Context, service, proto, host string) ([]*dns.SRV, error) {
	if s.lookupSRV == nil {
		return nil, errors.New("lookup srv not configured")
	}
	return s.lookupSRV(ctx, service, proto, host)
}

func (s stubDNSResolver) LookupNAPTR(ctx context.Context, host string) ([]*dns.NAPTR, error) {
	if s.lookupNAPTR == nil {
		return nil, errors.New("lookup naptr not configured")
	}
	return s.lookupNAPTR(ctx, host)
}

func newMinReq(tb testing.TB) *sip.Request {
	tb.Helper()

	ruri := &uri.SIP{User: uri.User("alice"), Addr: uri.AddrFromHost("example.com")}
	furi := &uri.SIP{User: uri.User("bob"), Addr: uri.AddrFromHost("example.com")}
	turi := &uri.SIP{User: uri.User("alice"), Addr: uri.AddrFromHost("example.com")}

	headers := make(sip.Headers).
		Set(&header.From{URI: furi, Params: make(header.Values).Set("tag", "from-tag")}).
		Set(&header.To{URI: turi}).
		Set(header.CallID("call-id")).
		Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInvite}).
		Set(header.MaxForwards(70))

	return &sip.Request{
		Method:  sip.RequestMethodInvite,
		URI:     ruri,
		Proto:   sip.ProtoVer20(),
		Headers: headers,
	}
}

func newMinResp(tb testing.TB, viaTp sip.TransportProto, viaAddr header.Addr) *sip.Response {
	tb.Helper()

	furi := &uri.SIP{User: uri.User("bob"), Addr: uri.AddrFromHost("example.com")}
	turi := &uri.SIP{User: uri.User("alice"), Addr: uri.AddrFromHost("example.com")}

	headers := make(sip.Headers).
		Set(header.Via{{
			Proto:     sip.ProtoVer20(),
			Transport: viaTp,
			Addr:      viaAddr,
			Params:    make(header.Values).Set("branch", sip.GenerateBranch(0)),
		}}).
		Set(&header.From{URI: furi, Params: make(header.Values).Set("tag", "from-tag")}).
		Set(&header.To{URI: turi}).
		Set(header.CallID("call-id")).
		Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInvite})

	return &sip.Response{
		Status:  sip.ResponseStatusOK,
		Reason:  "OK",
		Proto:   sip.ProtoVer20(),
		Headers: headers,
	}
}

func readUDPMsg(tb testing.TB, conn net.PacketConn) sip.Message {
	tb.Helper()

	buf := make([]byte, 65535)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck

	n, _, err := conn.ReadFrom(buf)
	if err != nil {
		tb.Fatalf("conn.ReadFrom() error = %v, want nil", err)
	}

	msg, err := sip.DefaultParser().ParsePacket(buf[:n])
	if err != nil {
		tb.Fatalf("sip.DefaultParser().ParsePacket() error = %v, want nil", err)
	}

	return msg
}

func readTCPMsg(tb testing.TB, conn net.Conn) sip.Message {
	tb.Helper()

	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 2048)

	conn.SetReadDeadline(time.Now().Add(asyncEventTimeout)) //nolint:errcheck

	for {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}

		if err != nil {
			if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
				break
			}

			if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
				break
			}

			tb.Fatalf("conn.Read() error = %v, want nil", err)
		}
	}

	if len(buf) == 0 {
		tb.Fatalf("conn.Read() read 0 bytes, want response data")
	}

	msg, err := sip.DefaultParser().ParsePacket(buf)
	if err != nil {
		tb.Fatalf("sip.DefaultParser().ParsePacket() error = %v, want nil", err)
	}

	return msg
}

func waitFor(tb testing.TB, fn func() bool) {
	tb.Helper()

	const timeout = 5 * time.Second

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if fn() {
			return
		}

		select {
		case <-deadline.C:
			tb.Fatalf("condition not met within %v", timeout)
		case <-ticker.C:
		}
	}
}

// Connless transport tests.
func TestConnlessTransport_Lifecycle(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	got := tp.Metadata()
	if got.Reliable() || got.Streamed() {
		t.Fatalf("tp.Metadata().Flags = %+v, want unreliable packet-oriented", got.Flags)
	}

	if err := tp.Close(); err != nil {
		t.Fatalf("tp.Close() error = %v, want nil", err)
	}

	if err := tp.Close(); err != nil {
		t.Fatalf("tp.Close() second call error = %v, want nil", err)
	}

	_, err = tp.AcquireConn(t.Context(), netip.AddrPort{}, nil)
	if !errors.Is(err, sip.ErrTransportClosed) {
		t.Fatalf("tp.AcquireConn() error = %v, want %v", err, sip.ErrTransportClosed)
	}
}

func TestConnlessTransport_ListenAndServe_UsesConfig(t *testing.T) {
	t.Parallel()

	lisErr := errors.New("listen failed")
	spy := &spyPacketListenConfig{err: lisErr}

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), &sip.ConnlessTransportOptions{PacketListenConfig: spy})
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	gotErr := tp.ListenAndServe(t.Context(), "127.0.0.1:0")
	if diff := cmp.Diff(lisErr, gotErr, cmpopts.EquateErrors()); diff != "" {
		t.Fatalf("tp.ListenAndServe() error = %v, want %v\ndiff (-want +got):\n%s", gotErr, lisErr, diff)
	}

	if spy.called != 1 {
		t.Fatalf("tp.ListenAndServe() listen calls = %d, want 1", spy.called)
	}
}

func TestConnlessTransport_ServeListener_Duplicate(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}

	t.Cleanup(func() { conn.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- tp.ServeListener(ctx, conn) }()

	laddr := netip.MustParseAddrPort(conn.LocalAddr().String())
	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), laddr, &sip.AcquireConnOptions{LocalAddr: laddr})
		return err == nil
	})

	err = tp.ServeListener(t.Context(), conn)
	if !errors.Is(err, sip.ErrListenerTracked) {
		t.Fatalf("tp.ServeListener() error = %v, want %v", err, sip.ErrListenerTracked)
	}

	cancel()
	conn.Close()
	tp.Close()

	if err := <-done; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("tp.ServeListener() error = %v, want context canceled or closed", err)
	}
}

func TestConnlessTransport_SendAndReceive(t *testing.T) {
	t.Parallel()

	srv, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}

	t.Cleanup(func() { srv.Close() })

	srvAddr := netip.MustParseAddrPort(srv.LocalAddr().String())
	meta := sip.UDPTransportMetadata()
	meta.DefaultPort = srvAddr.Port()

	tp, err := sip.NewConnlessTransport(meta, nil)
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	req := newMinReq(t)

	outReq, err := sip.NewOutboundRequestEnvelope(req)
	if err != nil {
		t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
	}

	outReq.SetRemoteAddr(netip.AddrPortFrom(srvAddr.Addr(), 0))

	if err := tp.SendRequest(t.Context(), outReq, nil); err != nil {
		t.Fatalf("tp.SendRequest() error = %v, want nil", err)
	}

	if got := outReq.RemoteAddr().Port(); got != srvAddr.Port() {
		t.Fatalf("tp.SendRequest() remote port = %d, want %d", got, srvAddr.Port())
	}

	msg := readUDPMsg(t, srv)

	parsedReq, ok := msg.(*sip.Request)
	if !ok {
		t.Fatalf("parsed message type = %T, want *sip.Request", msg)
	}

	via, ok := parsedReq.Headers.FirstViaHop()
	if !ok {
		t.Fatalf("parsed request Via header missing")
	}

	if got, want := via.Transport, sip.TransportProto("UDP"); !got.Equal(want) {
		t.Fatalf("Via.Transport = %v, want %v", got, want)
	}

	if branch, ok := via.Branch(); !ok || branch == "" {
		t.Fatalf("Via.Branch = %q (ok=%v), want non-empty", branch, ok)
	}

	resp := newMinResp(t, "UDP", header.AddrFromHostPort(srvAddr.Addr().String(), srvAddr.Port()))

	outRes, err := sip.NewOutboundResponseEnvelope(resp)
	if err != nil {
		t.Fatalf("sip.NewOutboundResponseEnvelope() error = %v, want nil", err)
	}

	outRes.SetRemoteAddr(srvAddr)

	if err := tp.SendResponse(t.Context(), outRes, nil); err != nil {
		t.Fatalf("tp.SendResponse() error = %v, want nil", err)
	}

	resMsg := readUDPMsg(t, srv)

	parsedRes, ok := resMsg.(*sip.Response)
	if !ok {
		t.Fatalf("parsed response type = %T, want *sip.Response", resMsg)
	}

	toHdr, ok := parsedRes.Headers.To()
	if !ok {
		t.Fatalf("parsed response To header missing")
	}

	if tag, ok := toHdr.Tag(); !ok || tag == "" {
		t.Fatalf("To.Tag = %q (ok=%v), want non-empty", tag, ok)
	}
}

func TestConnlessTransport_InterceptInboundMessages(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), &sip.ConnlessTransportOptions{
		TransportOptions: sip.TransportOptions{SentBy: sip.AddrFromHost("127.0.0.1")},
	})
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}

	t.Cleanup(func() { conn.Close() })

	reqCh := make(chan *sip.InboundRequestEnvelope, 1)
	resCh := make(chan *sip.InboundResponseEnvelope, 1)

	tp.UseInboundRequestInterceptor(
		sip.InboundRequestInterceptorFunc(func(ctx context.Context, req *sip.InboundRequestEnvelope, next sip.RequestReceiver) error {
			reqCh <- req
			return nil
		}),
	)
	tp.UseInboundResponseInterceptor(
		sip.InboundResponseInterceptorFunc(func(ctx context.Context, res *sip.InboundResponseEnvelope, next sip.ResponseReceiver) error {
			resCh <- res
			return nil
		}),
	)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- tp.ServeListener(ctx, conn) }()

	laddr := netip.MustParseAddrPort(conn.LocalAddr().String())
	req := newMinReq(t)
	req.Headers.Set(header.Via{{
		Proto:     sip.ProtoVer20(),
		Transport: "UDP",
		Addr:      header.AddrFromHostPort(laddr.Addr().String(), laddr.Port()),
		Params:    make(header.Values).Set("branch", sip.GenerateBranch(0)),
	}})

	_, err = conn.WriteTo([]byte(req.Render(nil)), net.UDPAddrFromAddrPort(laddr))
	if err != nil {
		t.Fatalf("conn.WriteTo() error = %v, want nil", err)
	}

	select {
	case <-reqCh:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("inbound request not received")
	}

	res := newMinResp(t, "UDP", header.AddrFromHost(laddr.Addr().String()))

	_, err = conn.WriteTo([]byte(res.Render(nil)), net.UDPAddrFromAddrPort(laddr))
	if err != nil {
		t.Fatalf("conn.WriteTo() error = %v, want nil", err)
	}

	select {
	case <-resCh:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("inbound response not received")
	}

	cancel()
	conn.Close()
	tp.Close()
	<-done
}

func TestConnlessTransport_RecvRequest_SetsReceivedAndRPort(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}

	t.Cleanup(func() { peer.Close() })

	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	reqCh := make(chan *sip.InboundRequestEnvelope, 1)
	tp.UseInboundRequestInterceptor(
		sip.InboundRequestInterceptorFunc(func(ctx context.Context, req *sip.InboundRequestEnvelope, next sip.RequestReceiver) error {
			reqCh <- req
			return nil
		}),
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), lisAddr, &sip.AcquireConnOptions{LocalAddr: lisAddr})
		return err == nil
	})

	req := newMinReq(t)
	viaParams := make(header.Values).
		Set("branch", sip.GenerateBranch(0)).
		Set("rport", "")
	req.Headers.Set(header.ContentLength(0))
	req.Headers.Set(header.Via{header.ViaHop{
		Proto:     sip.ProtoVer20(),
		Transport: "UDP",
		Addr:      header.AddrFromHost("example.com"),
		Params:    viaParams,
	}})

	if _, err := peer.WriteTo([]byte(req.Render(nil)), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo() error = %v, want nil", err)
	}

	var gotReq *sip.InboundRequestEnvelope
	select {
	case gotReq = <-reqCh:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("inbound request not received")
	}

	if gotReq == nil || gotReq.Message() == nil {
		t.Fatalf("inbound request = nil, want non-nil")
	}

	via, ok := gotReq.Headers().FirstViaHop()
	if !ok {
		t.Fatalf("inbound request Via header missing")
	}

	received, ok := via.Received()
	if !ok {
		t.Fatalf("Via.received missing")
	}

	if got, want := received, peerAddr.Addr(); got != want {
		t.Fatalf("Via.received = %v, want %v", got, want)
	}

	rport, ok := via.RPort()
	if !ok {
		t.Fatalf("Via.rport missing")
	}

	if got, want := rport, peerAddr.Port(); got != want {
		t.Fatalf("Via.rport = %v, want %v", got, want)
	}
}

func TestConnlessTransport_ReceiveResponse_MatchSentBy(t *testing.T) {
	t.Parallel()

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), &sip.ConnlessTransportOptions{
		TransportOptions: sip.TransportOptions{SentBy: sip.AddrFromHostPort("127.0.0.1", lisAddr.Port())},
	})
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}

	t.Cleanup(func() { peer.Close() })

	resCh := make(chan *sip.InboundResponseEnvelope, 1)
	tp.UseInboundResponseInterceptor(
		sip.InboundResponseInterceptorFunc(
			func(
				ctx context.Context,
				res *sip.InboundResponseEnvelope, next sip.ResponseReceiver,
			) error {
				resCh <- res
				return next.RecvResponse(ctx, res)
			},
		),
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), lisAddr, &sip.AcquireConnOptions{LocalAddr: lisAddr})
		return err == nil
	})

	matchAddr := header.AddrFromHostPort(lisAddr.Addr().String(), lisAddr.Port())
	res := newMinResp(t, "UDP", matchAddr)
	res.Headers.Set(header.ContentLength(0))

	if _, err := peer.WriteTo([]byte(res.Render(nil)), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(match) error = %v, want nil", err)
	}

	select {
	case <-resCh:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("inbound response not received")
	}

	res = newMinResp(t, "UDP", header.AddrFromHostPort("192.0.2.1", lisAddr.Port()))
	res.Headers.Set(header.ContentLength(0))

	if _, err := peer.WriteTo([]byte(res.Render(nil)), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(mismatch) error = %v, want nil", err)
	}

	select {
	case <-resCh:
		t.Fatalf("unexpected inbound response for mismatched sent-by")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestConnlessTransport_RecvResponse_PanicContinues(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), &sip.ConnlessTransportOptions{
		TransportOptions: sip.TransportOptions{SentBy: sip.AddrFromHost("127.0.0.1")},
	})
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}

	t.Cleanup(func() { peer.Close() })

	var panicOnce sync.Once

	resCh := make(chan *sip.InboundResponseEnvelope, 1)
	tp.UseInboundResponseInterceptor(
		sip.InboundResponseInterceptorFunc(func(ctx context.Context, res *sip.InboundResponseEnvelope, next sip.ResponseReceiver) error {
			panicked := false
			panicOnce.Do(func() { panicked = true })

			if panicked {
				panic(errors.New("boom"))
			}

			resCh <- res

			return next.RecvResponse(ctx, res)
		}),
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), lisAddr, &sip.AcquireConnOptions{LocalAddr: lisAddr})
		return err == nil
	})

	matchAddr := header.AddrFromHost(lisAddr.Addr().String())
	res := newMinResp(t, "UDP", matchAddr)
	res.Headers.Set(header.ContentLength(0))

	if _, err := peer.WriteTo([]byte(res.Render(nil)), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(first) error = %v, want nil", err)
	}

	res = newMinResp(t, "UDP", matchAddr)
	res.Headers.Set(header.ContentLength(0))

	if _, err := peer.WriteTo([]byte(res.Render(nil)), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(second) error = %v, want nil", err)
	}

	select {
	case <-resCh:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("inbound response not received after panic")
	}
}

func TestConnlessTransport_RecvRequest_PanicRespondsAndContinues(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}

	t.Cleanup(func() { peer.Close() })

	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	resCh := make(chan *sip.OutboundResponseEnvelope, 1)
	unbind := tp.UseOutboundResponseInterceptor(
		sip.OutboundResponseInterceptorFunc(func(ctx context.Context, res *sip.OutboundResponseEnvelope, opts *sip.SendResponseOptions, next sip.ResponseSender) error {
			resCh <- res
			return next.SendResponse(ctx, res, opts)
		}),
	)
	t.Cleanup(unbind)

	var panicOnce sync.Once

	reqCh := make(chan *sip.InboundRequestEnvelope, 1)

	tp.UseInboundRequestInterceptor(
		sip.InboundRequestInterceptorFunc(func(ctx context.Context, req *sip.InboundRequestEnvelope, next sip.RequestReceiver) error {
			panicked := false
			panicOnce.Do(func() { panicked = true })

			if panicked {
				panic(errors.New("boom"))
			}

			reqCh <- req

			return nil
		}),
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), lisAddr, &sip.AcquireConnOptions{LocalAddr: lisAddr})
		return err == nil
	})

	firstReq := newMinReq(t)
	firstReq.Headers.Set(header.Via{newViaHop(t, "UDP", header.AddrFromHostPort(peerAddr.Addr().String(), peerAddr.Port()))})
	firstReq.Headers.Set(header.ContentLength(0))

	if _, err := peer.WriteTo([]byte(firstReq.Render(nil)), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(first) error = %v, want nil", err)
	}

	select {
	case outRes := <-resCh:
		if outRes == nil || outRes.Message() == nil {
			t.Fatalf("outbound response = nil, want non-nil")
		}

		if got, want := outRes.Message().Status, sip.ResponseStatusServerInternalError; got != want {
			t.Fatalf("response status = %v, want %v", got, want)
		}

		if got, want := outRes.RemoteAddr(), peerAddr; got != want {
			t.Fatalf("response remote addr = %v, want %v", got, want)
		}
	case <-time.After(asyncEventTimeout):
		t.Fatalf("response not sent after panic")
	}

	secondReq := newMinReq(t)
	secondReq.Headers.Set(header.Via{newViaHop(t, "UDP", header.AddrFromHostPort(peerAddr.Addr().String(), peerAddr.Port()))})
	secondReq.Headers.Set(header.ContentLength(0))

	if _, err := peer.WriteTo([]byte(secondReq.Render(nil)), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(second) error = %v, want nil", err)
	}

	select {
	case <-reqCh:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("inbound request not received after panic")
	}
}

func TestConnlessTransport_RecvRequest_ParseErrorRespondsBadRequest(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}

	t.Cleanup(func() { peer.Close() })

	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	resCh := make(chan *sip.OutboundResponseEnvelope, 1)
	unbind := tp.UseOutboundResponseInterceptor(
		sip.OutboundResponseInterceptorFunc(func(ctx context.Context, res *sip.OutboundResponseEnvelope, opts *sip.SendResponseOptions, next sip.ResponseSender) error {
			resCh <- res
			return nil
		}),
	)
	t.Cleanup(unbind)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), lisAddr, &sip.AcquireConnOptions{LocalAddr: lisAddr})
		return err == nil
	})

	invalidReq := "INVITE sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP " + peerAddr.String() + ";branch=z9hG4bK-1\r\n" +
		"From: <sip:bob@example.com>;tag=1\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: 1@example.com\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Max-Forwards: 70\r\n" +
		"Content-Length: 0\r\n" +
		"BrokenHeader\r\n\r\n"

	if _, err := peer.WriteTo([]byte(invalidReq), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(invalid) error = %v, want nil", err)
	}

	select {
	case res := <-resCh:
		if res == nil || res.Message() == nil {
			t.Fatalf("outbound response = nil, want non-nil")
		}

		if got, want := res.Message().Status, sip.ResponseStatusBadRequest; got != want {
			t.Fatalf("response status = %v, want %v", got, want)
		}
	case <-time.After(asyncEventTimeout):
		t.Fatalf("response not sent")
	}
}

func TestConnlessTransport_RecvRequest_ParseErrorRespondsRequestEntityTooLarge(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}

	t.Cleanup(func() { peer.Close() })

	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	resCh := make(chan *sip.OutboundResponseEnvelope, 1)
	unbind := tp.UseOutboundResponseInterceptor(
		sip.OutboundResponseInterceptorFunc(func(ctx context.Context, res *sip.OutboundResponseEnvelope, opts *sip.SendResponseOptions, next sip.ResponseSender) error {
			resCh <- res
			return nil
		}),
	)
	t.Cleanup(unbind)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), lisAddr, &sip.AcquireConnOptions{LocalAddr: lisAddr})
		return err == nil
	})

	request := newMinReq(t)
	request.Headers.Set(header.Via{newViaHop(t, "UDP", header.AddrFromHostPort(peerAddr.Addr().String(), peerAddr.Port()))})
	request.Headers.Set(header.ContentLength(sip.MaxMessageSize + 1))

	if _, err := peer.WriteTo([]byte(request.Render(nil)), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo() error = %v, want nil", err)
	}

	select {
	case res := <-resCh:
		if res == nil || res.Message() == nil {
			t.Fatalf("outbound response = nil, want non-nil")
		}

		if got, want := res.Message().Status, sip.ResponseStatusRequestEntityTooLarge; got != want {
			t.Fatalf("response status = %v, want %v", got, want)
		}
	case <-time.After(asyncEventTimeout):
		t.Fatalf("response not sent")
	}
}

func TestConnlessTransport_RecvResponse_ParseErrorDiscarded(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}

	t.Cleanup(func() { peer.Close() })

	resCh := make(chan *sip.InboundResponseEnvelope, 1)
	tp.UseInboundResponseInterceptor(
		sip.InboundResponseInterceptorFunc(func(ctx context.Context, res *sip.InboundResponseEnvelope, next sip.ResponseReceiver) error {
			resCh <- res
			return nil
		}),
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	invalidResponse := "SIP/2.0 200 OK\r\n" +
		"Via: SIP/2.0/UDP " + lisAddr.String() + ";branch=z9hG4bK-1\r\n" +
		"From: <sip:bob@example.com>;tag=1\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: 1@example.com\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"BrokenHeader\r\n\r\n"

	if _, err := peer.WriteTo([]byte(invalidResponse), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(invalid) error = %v, want nil", err)
	}

	select {
	case <-resCh:
		t.Fatalf("unexpected inbound response for parse error")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestConnlessTransport_SendRequest_SentBy(t *testing.T) {
	t.Parallel()

	t.Run("from options", func(t *testing.T) {
		t.Parallel()

		srv, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.ListenPacket(server) error = %v, want nil", err)
		}

		t.Cleanup(func() { srv.Close() })

		srvAddr := netip.MustParseAddrPort(srv.LocalAddr().String())
		meta := sip.UDPTransportMetadata()
		meta.DefaultPort = srvAddr.Port()
		sentBy := sip.AddrFromHostPort("sentby.example.com", 5070)

		tp, err := sip.NewConnlessTransport(meta, &sip.ConnlessTransportOptions{
			TransportOptions: sip.TransportOptions{SentBy: sentBy},
		})
		if err != nil {
			t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
		}

		t.Cleanup(func() { tp.Close() })

		outReq, err := sip.NewOutboundRequestEnvelope(newMinReq(t))
		if err != nil {
			t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
		}

		outReq.SetRemoteAddr(srvAddr)

		if err := tp.SendRequest(t.Context(), outReq, nil); err != nil {
			t.Fatalf("tp.SendRequest() error = %v, want nil", err)
		}

		msg := readUDPMsg(t, srv)

		parsedReq, ok := msg.(*sip.Request)
		if !ok {
			t.Fatalf("parsed message type = %T, want *sip.Request", msg)
		}

		via, ok := parsedReq.Headers.FirstViaHop()
		if !ok {
			t.Fatalf("parsed request Via header missing")
		}

		wantAddr := header.AddrFromHostPort(sentBy.Host(), 5070)
		if !via.Addr.Equal(wantAddr) {
			t.Fatalf("Via.sent-by = %q, want %q", via.Addr, wantAddr)
		}
	})

	t.Run("from listener", func(t *testing.T) {
		t.Parallel()

		srv, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.ListenPacket(server) error = %v, want nil", err)
		}

		t.Cleanup(func() { srv.Close() })

		lis, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.ListenPacket(listener) error = %v, want nil", err)
		}

		t.Cleanup(func() { lis.Close() })

		lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())
		srvAddr := netip.MustParseAddrPort(srv.LocalAddr().String())
		meta := sip.UDPTransportMetadata()
		meta.DefaultPort = srvAddr.Port()

		tp, err := sip.NewConnlessTransport(meta, nil)
		if err != nil {
			t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
		}

		t.Cleanup(func() { tp.Close() })

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		done := make(chan error, 1)
		go func() { done <- tp.ServeListener(ctx, lis) }()

		outReq, err := sip.NewOutboundRequestEnvelope(newMinReq(t))
		if err != nil {
			t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
		}

		outReq.SetLocalAddr(lisAddr)
		outReq.SetRemoteAddr(srvAddr)

		if err := tp.SendRequest(t.Context(), outReq, nil); err != nil {
			t.Fatalf("tp.SendRequest() error = %v, want nil", err)
		}

		msg := readUDPMsg(t, srv)

		parsedReq, ok := msg.(*sip.Request)
		if !ok {
			t.Fatalf("parsed message type = %T, want *sip.Request", msg)
		}

		via, ok := parsedReq.Headers.FirstViaHop()
		if !ok {
			t.Fatalf("parsed request Via header missing")
		}

		if gotPort, ok := via.Addr.Port(); !ok || gotPort != lisAddr.Port() {
			t.Fatalf("Via.sent-by port = %d (ok=%v), want %d", gotPort, ok, lisAddr.Port())
		}

		cancel()
		lis.Close()
		<-done
	})

	t.Run("from connection", func(t *testing.T) {
		t.Parallel()

		srv, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.ListenPacket(server) error = %v, want nil", err)
		}

		t.Cleanup(func() { srv.Close() })

		srvAddr := netip.MustParseAddrPort(srv.LocalAddr().String())
		meta := sip.UDPTransportMetadata()
		meta.DefaultPort = srvAddr.Port()

		tp, err := sip.NewConnlessTransport(meta, nil)
		if err != nil {
			t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
		}

		t.Cleanup(func() { tp.Close() })

		outReq, err := sip.NewOutboundRequestEnvelope(newMinReq(t))
		if err != nil {
			t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
		}

		outReq.SetRemoteAddr(srvAddr)

		if err := tp.SendRequest(t.Context(), outReq, nil); err != nil {
			t.Fatalf("tp.SendRequest() error = %v, want nil", err)
		}

		msg := readUDPMsg(t, srv)

		parsedReq, ok := msg.(*sip.Request)
		if !ok {
			t.Fatalf("parsed message type = %T, want *sip.Request", msg)
		}

		via, ok := parsedReq.Headers.FirstViaHop()
		if !ok {
			t.Fatalf("parsed request Via header missing")
		}

		laddr := outReq.LocalAddr()
		if gotPort, ok := via.Addr.Port(); !ok || gotPort != laddr.Port() {
			t.Fatalf("Via.sent-by port = %d (ok=%v), want %d", gotPort, ok, laddr.Port())
		}
	})
}

func TestConnlessTransport_SendResponse_FallbackDNS(t *testing.T) {
	t.Parallel()

	srv, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}

	t.Cleanup(func() { srv.Close() })

	srvAddr := netip.MustParseAddrPort(srv.LocalAddr().String())
	meta := sip.UDPTransportMetadata()
	meta.DefaultPort = srvAddr.Port()

	dnsResolver := stubDNSResolver{
		lookupIP: func(ctx context.Context, network, host string) ([]net.IP, error) {
			if host != "example.com" {
				return nil, errors.New("unexpected host")
			}
			return []net.IP{srvAddr.Addr().AsSlice()}, nil
		},
		lookupSRV: func(ctx context.Context, service, proto, host string) ([]*dns.SRV, error) {
			return nil, errors.New("srv lookup not used")
		},
		lookupNAPTR: func(ctx context.Context, host string) ([]*dns.NAPTR, error) {
			return nil, errors.New("naptr lookup not used")
		},
	}

	tp, err := sip.NewConnlessTransport(meta, &sip.ConnlessTransportOptions{
		TransportOptions: sip.TransportOptions{
			RemoteClientLocator: &sip.RemoteElementLocator{DNSResolver: dnsResolver},
		},
	})
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	viaAddr := header.AddrFromHostPort("example.com", srvAddr.Port())
	resp := newMinResp(t, "UDP", viaAddr)

	outRes, err := sip.NewOutboundResponseEnvelope(resp)
	if err != nil {
		t.Fatalf("sip.NewOutboundResponseEnvelope() error = %v, want nil", err)
	}

	outRes.SetRemoteAddr(netip.AddrPort{})

	if err := tp.SendResponse(t.Context(), outRes, nil); err != nil {
		t.Fatalf("tp.SendResponse() error = %v, want nil", err)
	}

	msg := readUDPMsg(t, srv)
	if _, ok := msg.(*sip.Response); !ok {
		t.Fatalf("parsed message type = %T, want *sip.Response", msg)
	}
}

// ConnOriented transport tests.

func TestConnOrientedTransport_Lifecycle(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnOrientedTransport(sip.TCPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnOrientedTransport() error = %v, want nil", err)
	}

	if err := tp.Close(); err != nil {
		t.Fatalf("tp.Close() error = %v, want nil", err)
	}

	if err := tp.Close(); err != nil {
		t.Fatalf("tp.Close() second call error = %v, want nil", err)
	}

	_, err = tp.AcquireConn(t.Context(), netip.AddrPort{}, nil)
	if !errors.Is(err, sip.ErrTransportClosed) {
		t.Fatalf("tp.AcquireConn() error = %v, want %v", err, sip.ErrTransportClosed)
	}
}

func TestConnOrientedTransport_ListenAndServe_UsesConfig(t *testing.T) {
	t.Parallel()

	lisErr := errors.New("listen failed")
	spy := &spyConnListenConfig{err: lisErr}

	tp, err := sip.NewConnOrientedTransport(sip.TCPTransportMetadata(), &sip.ConnOrientedTransportOptions{
		ConnListenConfig: spy,
	})
	if err != nil {
		t.Fatalf("sip.NewConnOrientedTransport() error = %v, want nil", err)
	}

	gotErr := tp.ListenAndServe(t.Context(), "127.0.0.1:0")
	if diff := cmp.Diff(lisErr, gotErr, cmpopts.EquateErrors()); diff != "" {
		t.Fatalf("tp.ListenAndServe() error = %v, want %v\ndiff (-want +got):\n%s", gotErr, lisErr, diff)
	}

	if spy.called != 1 {
		t.Fatalf("tp.ListenAndServe() listen calls = %d, want 1", spy.called)
	}
}

func TestConnOrientedTransport_ServeListener_Duplicate(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnOrientedTransport(sip.TCPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { base.Close() })

	lis := &signalListener{Listener: base, acceptCalled: make(chan struct{})}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- tp.ServeListener(ctx, lis) }()

	select {
	case <-lis.acceptCalled:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("accept loop not started")
	}

	err = tp.ServeListener(t.Context(), lis)
	if !errors.Is(err, sip.ErrListenerTracked) {
		t.Fatalf("tp.ServeListener() error = %v, want %v", err, sip.ErrListenerTracked)
	}

	cancel()
	base.Close()
	tp.Close()
	<-done
}

func TestConnOrientedTransport_ServeListener_TemporaryError(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnOrientedTransport(sip.TCPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { base.Close() })

	lis := &tempErrListener{Listener: base}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- tp.ServeListener(ctx, lis) }()

	waitFor(t, func() bool {
		conn, err := net.Dial("tcp", base.Addr().String())
		if err != nil {
			return false
		}

		conn.Close()

		return true
	})

	cancel()
	base.Close()
	<-done
}

func TestConnOrientedTransport_ServeConn_Duplicate(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnOrientedTransport(sip.TCPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { base.Close() })

	connCh := make(chan net.Conn, 1)
	go func() {
		c, err := base.Accept()
		if err != nil {
			return
		}

		connCh <- c
	}()

	client, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}

	t.Cleanup(func() { client.Close() })

	srv := <-connCh
	t.Cleanup(func() { srv.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- tp.ServeConn(ctx, srv) }()

	laddr := netip.MustParseAddrPort(srv.LocalAddr().String())
	raddr := netip.MustParseAddrPort(srv.RemoteAddr().String())
	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), raddr, &sip.AcquireConnOptions{LocalAddr: laddr})
		return err == nil
	})

	err = tp.ServeConn(t.Context(), srv)
	if !errors.Is(err, sip.ErrConnTracked) {
		t.Fatalf("tp.ServeConn() error = %v, want %v", err, sip.ErrConnTracked)
	}

	cancel()
	srv.Close()
	tp.Close()
	<-done
}

func TestConnOrientedTransport_SendAndReceive(t *testing.T) {
	t.Parallel()

	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { base.Close() })

	tp, err := sip.NewConnOrientedTransport(sip.TCPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	srvCh := make(chan net.Conn, 1)
	go func() {
		c, err := base.Accept()
		if err != nil {
			return
		}

		srvCh <- c
	}()

	req := newMinReq(t)

	outReq, err := sip.NewOutboundRequestEnvelope(req)
	if err != nil {
		t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
	}

	srvAddr := netip.MustParseAddrPort(base.Addr().String())
	outReq.SetRemoteAddr(srvAddr)

	if err := tp.SendRequest(t.Context(), outReq, nil); err != nil {
		t.Fatalf("tp.SendRequest() error = %v, want nil", err)
	}

	srvConn := <-srvCh
	t.Cleanup(func() { srvConn.Close() })

	msg := readTCPMsg(t, srvConn)

	parsedReq, ok := msg.(*sip.Request)
	if !ok {
		t.Fatalf("parsed message type = %T, want *sip.Request", msg)
	}

	if _, ok := parsedReq.Headers.ContentLength(); !ok {
		t.Fatalf("Content-Length header missing")
	}

	resp := newMinResp(t, "TCP", header.AddrFromHostPort(srvAddr.Addr().String(), srvAddr.Port()))

	outRes, err := sip.NewOutboundResponseEnvelope(resp)
	if err != nil {
		t.Fatalf("sip.NewOutboundResponseEnvelope() error = %v, want nil", err)
	}

	outRes.SetRemoteAddr(srvAddr)

	if err := tp.SendResponse(t.Context(), outRes, nil); err != nil {
		t.Fatalf("tp.SendResponse() error = %v, want nil", err)
	}

	resMsg := readTCPMsg(t, srvConn)

	parsedRes, ok := resMsg.(*sip.Response)
	if !ok {
		t.Fatalf("parsed response type = %T, want *sip.Response", resMsg)
	}

	if _, ok := parsedRes.Headers.ContentLength(); !ok {
		t.Fatalf("Content-Length header missing")
	}

	toHdr, ok := parsedRes.Headers.To()
	if !ok {
		t.Fatalf("parsed response To header missing")
	}

	if tag, ok := toHdr.Tag(); !ok || tag == "" {
		t.Fatalf("To.Tag = %q (ok=%v), want non-empty", tag, ok)
	}
}

func TestConnOrientedTransport_InterceptInboundMessages(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnOrientedTransport(sip.TCPTransportMetadata(), &sip.ConnOrientedTransportOptions{
		TransportOptions: sip.TransportOptions{SentBy: sip.AddrFromHost("127.0.0.1")},
	})
	if err != nil {
		t.Fatalf("sip.NewConnOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { base.Close() })

	reqCh := make(chan *sip.InboundRequestEnvelope, 1)
	resCh := make(chan *sip.InboundResponseEnvelope, 1)

	tp.UseInboundRequestInterceptor(
		sip.InboundRequestInterceptorFunc(func(ctx context.Context, req *sip.InboundRequestEnvelope, next sip.RequestReceiver) error {
			reqCh <- req
			return nil
		}),
	)
	tp.UseInboundResponseInterceptor(
		sip.InboundResponseInterceptorFunc(func(ctx context.Context, res *sip.InboundResponseEnvelope, next sip.ResponseReceiver) error {
			resCh <- res
			return nil
		}),
	)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- tp.ServeListener(ctx, base) }()

	cln, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}

	t.Cleanup(func() { cln.Close() })

	waitFor(t, func() bool {
		req := newMinReq(t)
		req.Headers.Set(header.Via{{
			Proto:     sip.ProtoVer20(),
			Transport: "TCP",
			Addr:      header.AddrFromHostPort("127.0.0.1", 5060),
			Params:    make(header.Values).Set("branch", sip.GenerateBranch(0)),
		}})
		req.Headers.Set(header.ContentLength(0))
		_, err := cln.Write([]byte(req.Render(nil)))

		return err == nil
	})

	select {
	case <-reqCh:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("inbound request not received")
	}

	laddr := netip.MustParseAddrPort(base.Addr().String())
	res := newMinResp(t, "TCP", header.AddrFromHost(laddr.Addr().String()))
	res.Headers.Set(header.ContentLength(0))

	if _, err := cln.Write([]byte(res.Render(nil))); err != nil {
		t.Fatalf("client.Write() error = %v, want nil", err)
	}

	select {
	case <-resCh:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("inbound response not received")
	}

	cancel()
	base.Close()
	cln.Close()
	tp.Close()
	<-done
}

func TestConnOrientedTransport_RecvRequest_PanicResponds(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnOrientedTransport(sip.TCPTransportMetadata(), &sip.ConnOrientedTransportOptions{
		TransportOptions: sip.TransportOptions{SentBy: sip.AddrFromHostPort("127.0.0.1", 5060)},
	})
	if err != nil {
		t.Fatalf("sip.NewConnOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	var panicOnce sync.Once
	tp.UseInboundRequestInterceptor(
		sip.InboundRequestInterceptorFunc(func(ctx context.Context, req *sip.InboundRequestEnvelope, next sip.RequestReceiver) error {
			panicked := false
			panicOnce.Do(func() { panicked = true })

			if panicked {
				panic(errors.New("boom"))
			}

			return nil
		}),
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	cln, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}
	defer cln.Close()

	clnAddr := netip.MustParseAddrPort(cln.LocalAddr().String())
	lisAddr := netip.MustParseAddrPort(lis.Addr().String())

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), clnAddr, &sip.AcquireConnOptions{LocalAddr: lisAddr})
		return err == nil
	})

	req := newMinReq(t)
	req.Headers.Set(header.ContentLength(0))
	req.Headers.Set(header.Via{newViaHop(t, "TCP", header.AddrFromHostPort(clnAddr.Addr().String(), clnAddr.Port()))})

	if _, err := cln.Write([]byte(req.Render(nil))); err != nil {
		t.Fatalf("client.Write() error = %v, want nil", err)
	}

	msg := readTCPMsg(t, cln)

	res, ok := msg.(*sip.Response)
	if !ok {
		t.Fatalf("parsed message type = %T, want *sip.Response", msg)
	}

	if got, want := res.Status, sip.ResponseStatusServerInternalError; got != want {
		t.Fatalf("response status = %v, want %v", got, want)
	}
}

func TestConnOrientedTransport_RecvRequest_ParseErrorRespondsBadRequest(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnOrientedTransport(sip.TCPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	cln, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}
	defer cln.Close()

	clnAddr := netip.MustParseAddrPort(cln.LocalAddr().String())
	lisAddr := netip.MustParseAddrPort(lis.Addr().String())

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), clnAddr, &sip.AcquireConnOptions{LocalAddr: lisAddr})
		return err == nil
	})

	invalidRequest := "INVITE sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/TCP 127.0.0.1:5060;branch=z9hG4bK-1\r\n" +
		"From: <sip:bob@example.com>;tag=1\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: 1@example.com\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Max-Forwards: 70\r\n" +
		"Content-Length: 0\r\n" +
		"BrokenHeader\r\n\r\n"
	if _, err := cln.Write([]byte(invalidRequest)); err != nil {
		t.Fatalf("client.Write(invalid) error = %v, want nil", err)
	}

	msg := readTCPMsg(t, cln)

	res, ok := msg.(*sip.Response)
	if !ok {
		t.Fatalf("parsed message type = %T, want *sip.Response", msg)
	}

	if got, want := res.Status, sip.ResponseStatusBadRequest; got != want {
		t.Fatalf("response status = %v, want %v", got, want)
	}
}

func TestConnOrientedTransport_RecvRequest_ParseErrorRespondsMessageTooLarge(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnOrientedTransport(sip.TCPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	resCh := make(chan *sip.OutboundResponseEnvelope, 1)
	unbind := tp.UseOutboundResponseInterceptor(
		sip.OutboundResponseInterceptorFunc(func(ctx context.Context, res *sip.OutboundResponseEnvelope, opts *sip.SendResponseOptions, next sip.ResponseSender) error {
			resCh <- res
			return nil
		}),
	)
	t.Cleanup(unbind)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	cln, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}
	defer cln.Close()

	clnAddr := netip.MustParseAddrPort(cln.LocalAddr().String())
	lisAddr := netip.MustParseAddrPort(lis.Addr().String())

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), clnAddr, &sip.AcquireConnOptions{LocalAddr: lisAddr})
		return err == nil
	})

	req := newMinReq(t)
	req.Body = bytes.Repeat([]byte("a"), int(sip.MaxMessageSize))
	req.Headers.Set(header.ContentLength(len(req.Body)))
	req.Headers.Set(header.Via{newViaHop(t, "TCP", header.AddrFromHostPort(clnAddr.Addr().String(), clnAddr.Port()))})

	if _, err := cln.Write([]byte(req.Render(nil))); err != nil {
		t.Fatalf("client.Write() error = %v, want nil", err)
	}

	select {
	case res := <-resCh:
		if res == nil || res.Message() == nil {
			t.Fatalf("outbound response = nil, want non-nil")
		}

		if got, want := res.Message().Status, sip.ResponseStatusMessageTooLarge; got != want {
			t.Fatalf("response status = %v, want %v", got, want)
		}
	case <-time.After(asyncEventTimeout):
		t.Fatalf("response not sent")
	}
}

func TestConnOrientedTransport_RecvResponse_ParseErrorDiscarded(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnOrientedTransport(sip.TCPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	resCh := make(chan *sip.InboundResponseEnvelope, 1)
	tp.UseInboundResponseInterceptor(
		sip.InboundResponseInterceptorFunc(func(ctx context.Context, res *sip.InboundResponseEnvelope, next sip.ResponseReceiver) error {
			resCh <- res
			return next.RecvResponse(ctx, res)
		}),
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	cln, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}
	defer cln.Close()

	clnAddr := netip.MustParseAddrPort(cln.LocalAddr().String())
	lisAddr := netip.MustParseAddrPort(lis.Addr().String())

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), clnAddr, &sip.AcquireConnOptions{LocalAddr: lisAddr})
		return err == nil
	})

	invalidRes := "SIP/2.0 200 OK\r\n" +
		"Via: SIP/2.0/TCP " + lisAddr.String() + ";branch=z9hG4bK-1\r\n" +
		"From: <sip:bob@example.com>;tag=1\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: 1@example.com\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"BrokenHeader\r\n\r\n"
	if _, err := cln.Write([]byte(invalidRes)); err != nil {
		t.Fatalf("client.Write(invalid) error = %v, want nil", err)
	}

	select {
	case <-resCh:
		t.Fatalf("unexpected inbound response for parse error")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestConnOrientedTransport_ReceiveResponse_MatchSentBy(t *testing.T) {
	t.Parallel()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.Addr().String())

	tp, err := sip.NewConnOrientedTransport(sip.TCPTransportMetadata(), &sip.ConnOrientedTransportOptions{
		TransportOptions: sip.TransportOptions{SentBy: sip.AddrFromHostPort("127.0.0.1", lisAddr.Port())},
	})
	if err != nil {
		t.Fatalf("sip.NewConnOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	resCh := make(chan *sip.InboundResponseEnvelope, 1)
	tp.UseInboundResponseInterceptor(
		sip.InboundResponseInterceptorFunc(func(ctx context.Context, res *sip.InboundResponseEnvelope, next sip.ResponseReceiver) error {
			resCh <- res
			return next.RecvResponse(ctx, res)
		}),
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	cln, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}
	defer cln.Close()

	clnAddr := netip.MustParseAddrPort(cln.LocalAddr().String())
	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), clnAddr, &sip.AcquireConnOptions{LocalAddr: lisAddr})
		return err == nil
	})

	matchAddr := header.AddrFromHostPort(lisAddr.Addr().String(), lisAddr.Port())
	res := newMinResp(t, "TCP", matchAddr)
	res.Headers.Set(header.ContentLength(0))

	if _, err := cln.Write([]byte(res.Render(nil))); err != nil {
		t.Fatalf("client.Write(match) error = %v, want nil", err)
	}

	select {
	case <-resCh:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("inbound response not received")
	}

	res = newMinResp(t, "TCP", header.AddrFromHostPort("192.0.2.2", lisAddr.Port()))
	res.Headers.Set(header.ContentLength(0))

	if _, err := cln.Write([]byte(res.Render(nil))); err != nil {
		t.Fatalf("client.Write(mismatch) error = %v, want nil", err)
	}

	select {
	case <-resCh:
		t.Fatalf("unexpected inbound response for mismatched sent-by")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestConnOrientedTransport_SendRequest_SentBy(t *testing.T) {
	t.Parallel()

	t.Run("from options", func(t *testing.T) {
		t.Parallel()

		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen() error = %v, want nil", err)
		}

		t.Cleanup(func() { lis.Close() })

		lisAddr := netip.MustParseAddrPort(lis.Addr().String())
		sentBy := sip.AddrFromHostPort("sentby.example.com", 5071)

		tp, err := sip.NewConnOrientedTransport(sip.TCPTransportMetadata(), &sip.ConnOrientedTransportOptions{
			TransportOptions: sip.TransportOptions{SentBy: sentBy},
		})
		if err != nil {
			t.Fatalf("sip.NewConnOrientedTransport() error = %v, want nil", err)
		}

		t.Cleanup(func() { tp.Close() })

		serverCh := make(chan net.Conn, 1)
		go func() {
			conn, err := lis.Accept()
			if err != nil {
				return
			}

			serverCh <- conn
		}()

		outReq, err := sip.NewOutboundRequestEnvelope(newMinReq(t))
		if err != nil {
			t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
		}

		outReq.SetRemoteAddr(lisAddr)

		if err := tp.SendRequest(t.Context(), outReq, nil); err != nil {
			t.Fatalf("tp.SendRequest() error = %v, want nil", err)
		}

		srvConn := <-serverCh
		defer srvConn.Close()

		msg := readTCPMsg(t, srvConn)

		parsedReq, ok := msg.(*sip.Request)
		if !ok {
			t.Fatalf("parsed message type = %T, want *sip.Request", msg)
		}

		via, ok := parsedReq.Headers.FirstViaHop()
		if !ok {
			t.Fatalf("parsed request Via header missing")
		}

		wantAddr := header.AddrFromHostPort(sentBy.Host(), 5071)
		if !via.Addr.Equal(wantAddr) {
			t.Fatalf("Via.sent-by = %q, want %q", via.Addr, wantAddr)
		}
	})

	t.Run("from connection", func(t *testing.T) {
		t.Parallel()

		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen() error = %v, want nil", err)
		}

		t.Cleanup(func() { lis.Close() })

		lisAddr := netip.MustParseAddrPort(lis.Addr().String())

		tp, err := sip.NewConnOrientedTransport(sip.TCPTransportMetadata(), nil)
		if err != nil {
			t.Fatalf("sip.NewConnOrientedTransport() error = %v, want nil", err)
		}

		t.Cleanup(func() { tp.Close() })

		srvCh := make(chan net.Conn, 1)
		go func() {
			conn, err := lis.Accept()
			if err != nil {
				return
			}

			srvCh <- conn
		}()

		outReq, err := sip.NewOutboundRequestEnvelope(newMinReq(t))
		if err != nil {
			t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
		}

		outReq.SetRemoteAddr(lisAddr)

		if err := tp.SendRequest(t.Context(), outReq, nil); err != nil {
			t.Fatalf("tp.SendRequest() error = %v, want nil", err)
		}

		srvConn := <-srvCh
		defer srvConn.Close()

		msg := readTCPMsg(t, srvConn)

		parsedReq, ok := msg.(*sip.Request)
		if !ok {
			t.Fatalf("parsed message type = %T, want *sip.Request", msg)
		}

		via, ok := parsedReq.Headers.FirstViaHop()
		if !ok {
			t.Fatalf("parsed request Via header missing")
		}

		rmtAddr := netip.MustParseAddrPort(srvConn.RemoteAddr().String())
		if gotPort, ok := via.Addr.Port(); !ok || gotPort != rmtAddr.Port() {
			t.Fatalf("Via.sent-by port = %d (ok=%v), want %d", gotPort, ok, rmtAddr.Port())
		}
	})
}

func TestConnOrientedTransport_SendResponse_FallbackDNS(t *testing.T) {
	t.Parallel()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.Addr().String())
	meta := sip.TCPTransportMetadata()
	meta.DefaultPort = lisAddr.Port()

	callCnt := 0
	dialer := sip.ConnDialerFunc(func(ctx context.Context, network, addr string) (net.Conn, error) {
		callCnt++
		if callCnt == 1 {
			return nil, errors.New("dial failed")
		}

		return (&net.Dialer{}).DialContext(ctx, network, addr)
	})

	dnsResolver := stubDNSResolver{
		lookupIP: func(ctx context.Context, network, host string) ([]net.IP, error) {
			if host != "example.com" {
				return nil, errors.New("unexpected host")
			}
			return []net.IP{net.ParseIP("127.0.0.2"), net.ParseIP("127.0.0.1")}, nil
		},
		lookupSRV: func(ctx context.Context, service, proto, host string) ([]*dns.SRV, error) {
			return nil, errors.New("srv lookup not used")
		},
		lookupNAPTR: func(ctx context.Context, host string) ([]*dns.NAPTR, error) {
			return nil, errors.New("naptr lookup not used")
		},
	}

	tp, err := sip.NewConnOrientedTransport(meta, &sip.ConnOrientedTransportOptions{
		TransportOptions: sip.TransportOptions{
			RemoteClientLocator: &sip.RemoteElementLocator{DNSResolver: dnsResolver},
			ConnDialer:          dialer,
		},
	})
	if err != nil {
		t.Fatalf("sip.NewConnOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	srvCh := make(chan net.Conn, 1)
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}

		srvCh <- conn
	}()

	viaAddr := header.AddrFromHostPort("example.com", lisAddr.Port())
	resp := newMinResp(t, "TCP", viaAddr)
	resp.Headers.Set(header.ContentLength(0))

	outRes, err := sip.NewOutboundResponseEnvelope(resp)
	if err != nil {
		t.Fatalf("sip.NewOutboundResponseEnvelope() error = %v, want nil", err)
	}

	outRes.SetRemoteAddr(netip.AddrPort{})

	if err := tp.SendResponse(t.Context(), outRes, nil); err != nil {
		t.Fatalf("tp.SendResponse() error = %v, want nil", err)
	}

	srvConn := <-srvCh
	defer srvConn.Close()

	msg := readTCPMsg(t, srvConn)
	if _, ok := msg.(*sip.Response); !ok {
		t.Fatalf("parsed message type = %T, want *sip.Response", msg)
	}

	if callCnt < 2 {
		t.Fatalf("dial calls = %d, want >= 2", callCnt)
	}
}
