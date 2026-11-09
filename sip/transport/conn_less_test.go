package transport_test

import (
	"context"
	"math"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/internal/testutil/dnstest"
	"github.com/ghettovoice/gosip/internal/testutil/siptest"
	"github.com/ghettovoice/gosip/pkg/dns"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/transport"
)

func TestConnLessTransport_Lifecycle(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
	}

	if got := tp.Metadata(); got.Reliable() || got.Streamed() {
		t.Fatalf("tp.Metadata().Flags = %+v, want unreliable packet-oriented", got.Flags)
	}

	if err := tp.Close(); err != nil {
		t.Fatalf("tp.Close() error = %v, want nil", err)
	}

	if err := tp.Close(); err != nil {
		t.Fatalf("tp.Close() second call error = %v, want nil", err)
	}

	_, err = tp.AcquireConn(t.Context(), netip.AddrPort{})
	if !errors.Is(err, transport.ErrTransportClosed) {
		t.Fatalf("tp.AcquireConn() error = %v, want %v", err, transport.ErrTransportClosed)
	}
}

func TestConnLessTransport_Listen_UsesConfig(t *testing.T) {
	t.Parallel()

	lisErr := errors.New("listen failed")
	spy := &spyLisConfig{listenPktErr: lisErr}
	tp, err := transport.NewConnLessTransport(sip.UDPMetadata(), transport.TransportOptions{ListenConfig: spy})
	if err != nil {
		t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
	}

	_, gotErr := tp.Listen(t.Context(), "127.0.0.1:0")
	if diff := cmp.Diff(lisErr, gotErr, cmpopts.EquateErrors()); diff != "" {
		t.Fatalf("tp.Listen() error = %v, want %v\ndiff (-want +got):\n%s", gotErr, lisErr, diff)
	}

	if spy.listenPktCalls != 1 || spy.listenCalls != 0 {
		t.Fatalf("listen calls = %d, listen packet calls = %d, want 0 and 1", spy.listenCalls, spy.listenPktCalls)
	}
	if spy.network != "udp" {
		t.Fatalf("listen network = %q, want %q", spy.network, "udp")
	}
	if spy.addr != "127.0.0.1:0" {
		t.Fatalf("listen address = %q, want %q", spy.addr, "127.0.0.1:0")
	}
}

func TestConnLessTransport_ServeListener_Duplicate(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
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
		_, err := tp.AcquireConn(t.Context(), laddr, transport.AcquireConnOptions{LocalAddr: laddr})
		return err == nil
	})

	err = tp.ServeListener(t.Context(), conn)
	if !errors.Is(err, transport.ErrListenerTracked) {
		t.Fatalf("tp.ServeListener() error = %v, want %v", err, transport.ErrListenerTracked)
	}

	cancel()
	conn.Close()
	tp.Close()

	err = testutil.WaitResult(t, done, "tp.ServeListener()")
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("tp.ServeListener() error = %v, want context canceled or closed", err)
	}
}

func TestConnLessTransport_SendAndReceive(t *testing.T) {
	t.Parallel()

	srv, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	t.Cleanup(func() { srv.Close() })

	srvAddr := netip.MustParseAddrPort(srv.LocalAddr().String())
	meta := sip.UDPMetadata()
	meta.DefaultPort = srvAddr.Port()
	tp, err := transport.NewConnLessTransport(meta)
	if err != nil {
		t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

	req := newMinReq(t)
	outReq := sip.NewRequestEnvelope(req).
		SetRemoteAddr(netip.AddrPortFrom(srvAddr.Addr(), 0))

	if err := tp.SendRequest(t.Context(), outReq); err != nil {
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

	via, ok := parsedReq.Headers.FirstVia()
	if !ok {
		t.Fatalf("parsed request Via header missing")
	}

	if got, want := via.Transport, meta.Proto; !got.Equal(want) {
		t.Fatalf("via.Transport = %v, want %v", got, want)
	}

	if branch, ok := via.Branch(); !ok || branch == "" {
		t.Fatalf("via.Branch = %q (ok=%v), want non-empty", branch, ok)
	}

	res := newMinRes(t, meta.Proto, sip.MakeHostPortAddr(srvAddr.Addr().String(), srvAddr.Port()))
	outRes := sip.NewResponseEnvelope(res).
		SetRemoteAddr(srvAddr)

	if err := tp.SendResponse(t.Context(), outRes); err != nil {
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
		t.Fatalf("to.Tag = %q (ok=%v), want non-empty", tag, ok)
	}
}

func TestConnLessTransport_InterceptInboundMessages(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnLessTransport(sip.UDPMetadata(), transport.TransportOptions{
		PublicAddr: sip.MakeHostAddr("127.0.0.1"),
	})
	if err != nil {
		t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	t.Cleanup(func() { conn.Close() })

	reqCh := make(chan *sip.RequestEnvelope, 1)
	tp.UseInboundRequestInterceptor(
		sip.InboundRequestInterceptorFunc(
			func(ctx context.Context, next sip.RequestReceiver, req *sip.RequestEnvelope) error {
				reqCh <- req
				return nil
			},
		),
	)

	resCh := make(chan *sip.ResponseEnvelope, 1)
	tp.UseInboundResponseInterceptor(
		sip.InboundResponseInterceptorFunc(
			func(ctx context.Context, next sip.ResponseReceiver, res *sip.ResponseEnvelope) error {
				resCh <- res
				return nil
			},
		),
	)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- tp.ServeListener(ctx, conn) }()

	laddr := netip.MustParseAddrPort(conn.LocalAddr().String())
	req := newMinReq(t)
	req.Headers.Set(header.Via{{
		Proto:     sip.ProtoVer20(),
		Transport: tp.Metadata().Proto,
		Addr:      sip.MakeHostPortAddr(laddr.Addr().String(), laddr.Port()),
		Params:    make(sip.Values).Set("branch", sip.GenerateBranch(0)),
	}})

	_, err = conn.WriteTo([]byte(req.Render()), net.UDPAddrFromAddrPort(laddr))
	if err != nil {
		t.Fatalf("conn.WriteTo() error = %v, want nil", err)
	}

	select {
	case <-reqCh:
	case <-time.After(asyncEvtTimeout):
		t.Fatalf("inbound request not received")
	}

	res := newMinRes(t, tp.Metadata().Proto, sip.MakeHostAddr(laddr.Addr().String()))
	_, err = conn.WriteTo([]byte(res.Render()), net.UDPAddrFromAddrPort(laddr))
	if err != nil {
		t.Fatalf("conn.WriteTo() error = %v, want nil", err)
	}

	select {
	case <-resCh:
	case <-time.After(asyncEvtTimeout):
		t.Fatalf("inbound response not received")
	}

	cancel()
	conn.Close()
	tp.Close()

	err = testutil.WaitResult(t, done, "tp.ServeListener()")
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("tp.ServeListener() error = %v, want context canceled or closed", err)
	}
}

func TestConnLessTransport_RecvRequest_SetsReceivedAndRPort(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
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

	reqCh := make(chan *sip.RequestEnvelope, 1)
	tp.UseInboundRequestInterceptor(
		sip.InboundRequestInterceptorFunc(
			func(ctx context.Context, next sip.RequestReceiver, req *sip.RequestEnvelope) error {
				reqCh <- req
				return nil
			},
		),
	)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	go func() { _ = tp.ServeListener(ctx, lis) }()

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), lisAddr, transport.AcquireConnOptions{
			LocalAddr: lisAddr,
		})
		return err == nil
	})

	req := newMinReq(t)
	viaParams := make(sip.Values).
		Set("branch", sip.GenerateBranch(0)).
		Set("rport", "")
	req.Headers.Set(header.ContentLength(0))
	req.Headers.Set(header.Via{header.ViaHop{
		Proto:     sip.ProtoVer20(),
		Transport: tp.Metadata().Proto,
		Addr:      sip.MakeHostAddr("example.com"),
		Params:    viaParams,
	}})

	if _, err := peer.WriteTo([]byte(req.Render()), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo() error = %v, want nil", err)
	}

	var gotReq *sip.RequestEnvelope
	select {
	case gotReq = <-reqCh:
	case <-time.After(asyncEvtTimeout):
		t.Fatalf("inbound request not received")
	}

	if gotReq == nil || gotReq.Message() == nil {
		t.Fatalf("inbound request = nil, want non-nil")
	}

	via, ok := gotReq.Headers().FirstVia()
	if !ok {
		t.Fatalf("inbound request Via header missing")
	}

	received, ok := via.Received()
	if !ok {
		t.Fatalf("via.received missing")
	}

	if got, want := received, peerAddr.Addr(); got != want {
		t.Fatalf("via.received = %v, want %v", got, want)
	}

	rport, ok := via.RPort()
	if !ok {
		t.Fatalf("via.rport missing")
	}

	if got, want := rport, peerAddr.Port(); got != want {
		t.Fatalf("via.rport = %v, want %v", got, want)
	}
}

func TestConnLessTransport_RecvResponse_MatchSentBy(t *testing.T) {
	t.Parallel()

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())
	tp, err := transport.NewConnLessTransport(sip.UDPMetadata(), transport.TransportOptions{
		PublicAddr: sip.MakeHostPortAddr("127.0.0.1", lisAddr.Port()),
	})
	if err != nil {
		t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}
	t.Cleanup(func() { peer.Close() })

	resCh := make(chan *sip.ResponseEnvelope, 1)
	tp.UseInboundResponseInterceptor(
		sip.InboundResponseInterceptorFunc(
			func(ctx context.Context, next sip.ResponseReceiver, res *sip.ResponseEnvelope) error {
				resCh <- res
				return next.RecvResponse(ctx, res)
			},
		),
	)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	go func() { _ = tp.ServeListener(ctx, lis) }()

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), lisAddr, transport.AcquireConnOptions{LocalAddr: lisAddr})
		return err == nil
	})

	matchAddr := sip.MakeHostPortAddr(lisAddr.Addr().String(), lisAddr.Port())
	res := newMinRes(t, tp.Metadata().Proto, matchAddr)
	res.Headers.Set(header.ContentLength(0))

	if _, err := peer.WriteTo([]byte(res.Render()), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(match) error = %v, want nil", err)
	}

	select {
	case <-resCh:
	case <-time.After(asyncEvtTimeout):
		t.Fatalf("inbound response not received")
	}

	res = newMinRes(t, tp.Metadata().Proto, sip.MakeHostPortAddr("192.0.2.1", lisAddr.Port()))
	res.Headers.Set(header.ContentLength(0))

	if _, err := peer.WriteTo([]byte(res.Render()), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(mismatch) error = %v, want nil", err)
	}

	select {
	case <-resCh:
		t.Fatalf("unexpected inbound response for mismatched sent-by")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestConnLessTransport_RecvResponse_PanicContinues(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnLessTransport(sip.UDPMetadata(), transport.TransportOptions{
		PublicAddr: sip.MakeHostAddr("127.0.0.1"),
	})
	if err != nil {
		t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	t.Cleanup(func() { lis.Close() })

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}
	t.Cleanup(func() { peer.Close() })

	var panicOnce sync.Once

	resCh := make(chan *sip.ResponseEnvelope, 1)
	tp.UseInboundResponseInterceptor(
		sip.InboundResponseInterceptorFunc(
			func(ctx context.Context, next sip.ResponseReceiver, res *sip.ResponseEnvelope) error {
				panicked := false
				panicOnce.Do(func() { panicked = true })

				if panicked {
					panic(errors.New("boom"))
				}

				resCh <- res

				return next.RecvResponse(ctx, res)
			},
		),
	)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	go func() { _ = tp.ServeListener(ctx, lis) }()

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), lisAddr, transport.AcquireConnOptions{
			LocalAddr: lisAddr,
		})
		return err == nil
	})

	matchAddr := sip.MakeHostAddr(lisAddr.Addr().String())
	res := newMinRes(t, "UDP", matchAddr)
	res.Headers.Set(header.ContentLength(0))

	if _, err := peer.WriteTo([]byte(res.Render()), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(first) error = %v, want nil", err)
	}

	res = newMinRes(t, tp.Metadata().Proto, matchAddr)
	res.Headers.Set(header.ContentLength(0))

	if _, err := peer.WriteTo([]byte(res.Render()), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(second) error = %v, want nil", err)
	}

	select {
	case <-resCh:
	case <-time.After(asyncEvtTimeout):
		t.Fatalf("inbound response not received after panic")
	}
}

func TestConnLessTransport_RecvRequest_PanicRespondsAndContinues(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	t.Cleanup(func() { lis.Close() })

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}
	t.Cleanup(func() { peer.Close() })

	resCh := make(chan *sip.ResponseEnvelope, 1)
	unbind := tp.UseOutboundResponseInterceptor(
		sip.OutboundResponseInterceptorFunc(
			func(
				ctx context.Context,
				next sip.ResponseSender,
				res *sip.ResponseEnvelope,
				opts ...sip.SendResponseOptions,
			) error {
				resCh <- res
				return next.SendResponse(ctx, res, opts...)
			},
		),
	)
	t.Cleanup(unbind)

	var panicOnce sync.Once
	reqCh := make(chan *sip.RequestEnvelope, 1)
	tp.UseInboundRequestInterceptor(
		sip.InboundRequestInterceptorFunc(
			func(ctx context.Context, next sip.RequestReceiver, req *sip.RequestEnvelope) error {
				panicked := false
				panicOnce.Do(func() { panicked = true })

				if panicked {
					panic(errors.New("boom"))
				}

				reqCh <- req

				return nil
			},
		),
	)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	go func() { _ = tp.ServeListener(ctx, lis) }()

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())
	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), lisAddr, transport.AcquireConnOptions{
			LocalAddr: lisAddr,
		})
		return err == nil
	})

	firstReq := newMinReq(t)
	firstReq.Headers.Set(header.Via{
		newViaHop(t, tp.Metadata().Proto, sip.MakeHostPortAddr(peerAddr.Addr().String(), peerAddr.Port())),
	})
	firstReq.Headers.Set(header.ContentLength(0))

	if _, err := peer.WriteTo([]byte(firstReq.Render()), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
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
	case <-time.After(asyncEvtTimeout):
		t.Fatalf("response not sent after panic")
	}

	secondReq := newMinReq(t)
	secondReq.Headers.Set(header.Via{
		newViaHop(t, tp.Metadata().Proto, sip.MakeHostPortAddr(peerAddr.Addr().String(), peerAddr.Port())),
	})
	secondReq.Headers.Set(header.ContentLength(0))

	if _, err := peer.WriteTo([]byte(secondReq.Render()), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(second) error = %v, want nil", err)
	}

	select {
	case <-reqCh:
	case <-time.After(asyncEvtTimeout):
		t.Fatalf("inbound request not received after panic")
	}
}

func TestConnLessTransport_RecvRequest_ParseErrorRespondsBadRequest(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	t.Cleanup(func() { lis.Close() })

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}
	t.Cleanup(func() { peer.Close() })

	resCh := make(chan *sip.ResponseEnvelope, 1)
	unbind := tp.UseOutboundResponseInterceptor(
		sip.OutboundResponseInterceptorFunc(
			func(
				ctx context.Context,
				next sip.ResponseSender,
				res *sip.ResponseEnvelope,
				opts ...sip.SendResponseOptions,
			) error {
				resCh <- res
				return nil
			},
		),
	)
	t.Cleanup(unbind)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	go func() { _ = tp.ServeListener(ctx, lis) }()

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())
	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), lisAddr, transport.AcquireConnOptions{
			LocalAddr: lisAddr,
		})
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
	case <-time.After(asyncEvtTimeout):
		t.Fatalf("response not sent")
	}
}

func TestConnLessTransport_RecvRequest_ParseErrorRespondsRequestEntityTooLarge(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	t.Cleanup(func() { lis.Close() })

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}
	t.Cleanup(func() { peer.Close() })

	resCh := make(chan *sip.ResponseEnvelope, 1)
	unbind := tp.UseOutboundResponseInterceptor(
		sip.OutboundResponseInterceptorFunc(
			func(
				ctx context.Context,
				next sip.ResponseSender,
				res *sip.ResponseEnvelope,
				opts ...sip.SendResponseOptions,
			) error {
				resCh <- res
				return nil
			},
		),
	)
	t.Cleanup(unbind)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	go func() { _ = tp.ServeListener(ctx, lis) }()

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())
	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), lisAddr, transport.AcquireConnOptions{
			LocalAddr: lisAddr,
		})
		return err == nil
	})

	request := newMinReq(t)
	request.Headers.Set(header.Via{
		newViaHop(t, tp.Metadata().Proto, sip.MakeHostPortAddr(peerAddr.Addr().String(), peerAddr.Port())),
	})
	request.Headers.Set(header.ContentLength(math.MaxUint16 + 1))

	if _, err := peer.WriteTo([]byte(request.Render()), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
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
	case <-time.After(asyncEvtTimeout):
		t.Fatalf("response not sent")
	}
}

func TestConnLessTransport_RecvResponse_ParseErrorDiscarded(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	t.Cleanup(func() { lis.Close() })

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}
	t.Cleanup(func() { peer.Close() })

	resCh := make(chan *sip.ResponseEnvelope, 1)
	tp.UseInboundResponseInterceptor(
		sip.InboundResponseInterceptorFunc(
			func(
				ctx context.Context,
				next sip.ResponseReceiver,
				res *sip.ResponseEnvelope,
			) error {
				resCh <- res
				return nil
			},
		),
	)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	go func() { _ = tp.ServeListener(ctx, lis) }()

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())
	invalidRes := "SIP/2.0 200 OK\r\n" +
		"Via: SIP/2.0/UDP " + lisAddr.String() + ";branch=z9hG4bK-1\r\n" +
		"From: <sip:bob@example.com>;tag=1\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: 1@example.com\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"BrokenHeader\r\n\r\n"

	if _, err := peer.WriteTo([]byte(invalidRes), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(invalid) error = %v, want nil", err)
	}

	select {
	case <-resCh:
		t.Fatalf("unexpected inbound response for parse error")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestConnLessTransport_SendRequest_SentBy(t *testing.T) {
	t.Parallel()

	t.Run("from options", func(t *testing.T) {
		t.Parallel()

		srv, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.ListenPacket(server) error = %v, want nil", err)
		}
		t.Cleanup(func() { srv.Close() })

		srvAddr := netip.MustParseAddrPort(srv.LocalAddr().String())
		meta := sip.UDPMetadata()
		meta.DefaultPort = srvAddr.Port()
		sentBy := sip.MakeHostPortAddr("sentby.example.com", 5070)

		tp, err := transport.NewConnLessTransport(meta, transport.TransportOptions{PublicAddr: sentBy})
		if err != nil {
			t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
		}
		t.Cleanup(func() { tp.Close() })

		outReq := sip.NewRequestEnvelope(newMinReq(t)).
			SetRemoteAddr(srvAddr)

		if err := tp.SendRequest(t.Context(), outReq); err != nil {
			t.Fatalf("tp.SendRequest() error = %v, want nil", err)
		}

		msg := readUDPMsg(t, srv)
		parsedReq, ok := msg.(*sip.Request)
		if !ok {
			t.Fatalf("parsed message type = %T, want *sip.Request", msg)
		}

		via, ok := parsedReq.Headers.FirstVia()
		if !ok {
			t.Fatalf("parsed request Via header missing")
		}

		wantAddr := sip.MakeHostPortAddr(sentBy.Host(), 5070)
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

		srvAddr := netip.MustParseAddrPort(srv.LocalAddr().String())
		meta := sip.UDPMetadata()
		meta.DefaultPort = srvAddr.Port()
		tp, err := transport.NewConnLessTransport(meta, transport.TransportOptions{
			PublicAddr: sip.MakeHostPortAddr("", 0),
		})
		if err != nil {
			t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
		}
		t.Cleanup(func() { tp.Close() })

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		done := make(chan error, 1)
		go func() { done <- tp.ServeListener(ctx, lis) }()

		lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

		waitFor(t, func() bool {
			_, err := tp.AcquireConn(t.Context(), lisAddr, transport.AcquireConnOptions{LocalAddr: lisAddr})
			return err == nil
		})

		outReq := sip.NewRequestEnvelope(newMinReq(t)).
			SetLocalAddr(lisAddr).
			SetRemoteAddr(srvAddr)

		if err := tp.SendRequest(t.Context(), outReq); err != nil {
			t.Fatalf("tp.SendRequest() error = %v, want nil", err)
		}

		msg := readUDPMsg(t, srv)
		parsedReq, ok := msg.(*sip.Request)
		if !ok {
			t.Fatalf("parsed message type = %T, want *sip.Request", msg)
		}

		via, ok := parsedReq.Headers.FirstVia()
		if !ok {
			t.Fatalf("parsed request Via header missing")
		}

		if gotPort, ok := via.Addr.Port(); !ok || gotPort != lisAddr.Port() {
			t.Fatalf("Via.sent-by port = %d (ok=%v), want %d", gotPort, ok, lisAddr.Port())
		}

		cancel()
		lis.Close()

		err = testutil.WaitResult(t, done, "tp.ServeListener()")
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("tp.ServeListener() error = %v, want context canceled or closed", err)
		}
	})

	t.Run("from connection", func(t *testing.T) {
		t.Parallel()

		srv, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.ListenPacket(server) error = %v, want nil", err)
		}
		t.Cleanup(func() { srv.Close() })

		srvAddr := netip.MustParseAddrPort(srv.LocalAddr().String())
		meta := sip.UDPMetadata()
		meta.DefaultPort = srvAddr.Port()
		tp, err := transport.NewConnLessTransport(meta, transport.TransportOptions{
			PublicAddr: sip.MakeHostPortAddr("", 0),
		})
		if err != nil {
			t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
		}
		t.Cleanup(func() { tp.Close() })

		outReq := sip.NewRequestEnvelope(newMinReq(t)).
			SetRemoteAddr(srvAddr)

		if err := tp.SendRequest(t.Context(), outReq); err != nil {
			t.Fatalf("tp.SendRequest() error = %v, want nil", err)
		}

		msg := readUDPMsg(t, srv)
		parsedReq, ok := msg.(*sip.Request)
		if !ok {
			t.Fatalf("parsed message type = %T, want *sip.Request", msg)
		}

		via, ok := parsedReq.Headers.FirstVia()
		if !ok {
			t.Fatalf("parsed request Via header missing")
		}

		laddr := outReq.LocalAddr()
		if gotPort, ok := via.Addr.Port(); !ok || gotPort != laddr.Port() {
			t.Fatalf("Via.sent-by port = %d (ok=%v), want %d", gotPort, ok, laddr.Port())
		}
	})
}

func TestConnLessTransport_SendResponse_FallbackDNS(t *testing.T) {
	t.Parallel()

	srv, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	t.Cleanup(func() { srv.Close() })

	srvAddr := netip.MustParseAddrPort(srv.LocalAddr().String())
	meta := sip.UDPMetadata()
	meta.DefaultPort = srvAddr.Port()
	dnsResolver := dnstest.StubResolver{
		LookupIPFunc: func(ctx context.Context, network, host string) ([]net.IP, error) {
			if host != "example.com" {
				return nil, errors.New("unexpected host")
			}
			return []net.IP{srvAddr.Addr().AsSlice()}, nil
		},
		LookupSRVFunc: func(ctx context.Context, service, proto, host string) ([]*dns.SRV, error) {
			return nil, errors.New("srv lookup not used")
		},
		LookupNAPTRFunc: func(ctx context.Context, host string) ([]*dns.NAPTR, error) {
			return nil, errors.New("naptr lookup not used")
		},
	}
	tp, err := transport.NewConnLessTransport(meta, transport.TransportOptions{
		ClientLocator: &sip.RemoteElementLocator{
			DNSResolver:      dnsResolver,
			MetadataProvider: siptest.MetadataProvider{Metas: []sip.TransportMetadata{meta}},
		},
	})
	if err != nil {
		t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

	viaAddr := sip.MakeHostPortAddr("example.com", srvAddr.Port())
	res := newMinRes(t, tp.Metadata().Proto, viaAddr)
	outRes := sip.NewResponseEnvelope(res).
		SetRemoteAddr(netip.AddrPort{})

	if err := tp.SendResponse(t.Context(), outRes); err != nil {
		t.Fatalf("tp.SendResponse() error = %v, want nil", err)
	}

	msg := readUDPMsg(t, srv)
	if _, ok := msg.(*sip.Response); !ok {
		t.Fatalf("parsed message type = %T, want *sip.Response", msg)
	}
}

type blockingPktConn struct {
	addr      *net.UDPAddr
	started   chan struct{}
	startOnce sync.Once
	closed    chan struct{}
	closeOnce sync.Once
}

func newBlockingPktConn() *blockingPktConn {
	return &blockingPktConn{
		addr:    &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5060},
		started: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (c *blockingPktConn) ReadFrom([]byte) (int, net.Addr, error) {
	c.startOnce.Do(func() { close(c.started) })
	<-c.closed
	return 0, nil, net.ErrClosed
}

func (*blockingPktConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	return len(p), nil
}

func (c *blockingPktConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *blockingPktConn) LocalAddr() net.Addr            { return c.addr }
func (*blockingPktConn) SetDeadline(time.Time) error      { return nil }
func (*blockingPktConn) SetReadDeadline(time.Time) error  { return nil }
func (*blockingPktConn) SetWriteDeadline(time.Time) error { return nil }

type blockingLisConfig struct {
	conn net.PacketConn
}

func (*blockingLisConfig) Listen(context.Context, string, string) (net.Listener, error) {
	return nil, errors.New("stream listener not supported in test")
}

func (c *blockingLisConfig) ListenPacket(context.Context, string, string) (net.PacketConn, error) {
	return c.conn, nil
}

func TestConnLessListener_Serve_RejectsConcurrentCall(t *testing.T) {
	t.Parallel()

	packetConn := newBlockingPktConn()
	tp, err := transport.NewConnLessTransport(sip.UDPMetadata(), transport.ConnLessTransportOptions{
		ListenConfig: &blockingLisConfig{conn: packetConn},
	})
	if err != nil {
		t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = tp.Close() })

	lis, err := tp.Listen(t.Context(), "127.0.0.1:5060")
	if err != nil {
		t.Fatalf("tp.Listen() error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- lis.Serve(ctx) }()

	select {
	case <-packetConn.started:
	case <-time.After(time.Second):
		t.Fatal("ls.Serve() did not start reading")
	}

	if err := lis.Serve(ctx); !errors.Is(err, transport.ErrListenerServing) {
		t.Fatalf("ls.Serve() second call error = %v, want %v", err, transport.ErrListenerServing)
	}

	cancel()

	if err := testutil.WaitResult(t, done, "ls.Serve()"); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("ls.Serve() error = %v, want context canceled", err)
	}
}
