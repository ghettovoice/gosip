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

	"github.com/ghettovoice/gosip/dns"
	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/transport"
)

func TestConnectionLessTransport_Lifecycle(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	if got := tp.Metadata(); got.Reliable() || got.Streamed() {
		t.Fatalf("tp.Metadata().Flags = %+v, want unreliable packet-oriented", got.Flags)
	}

	if err := tp.Close(t.Context()); err != nil {
		t.Fatalf("tp.Close(t.Context()) error = %v, want nil", err)
	}

	if err := tp.Close(t.Context()); err != nil {
		t.Fatalf("tp.Close(t.Context()) second call error = %v, want nil", err)
	}

	_, err = tp.AcquireConnection(t.Context(), netip.AddrPort{})
	if !errors.Is(err, transport.ErrTransportClosed) {
		t.Fatalf("tp.AcquireConnection() error = %v, want %v", err, transport.ErrTransportClosed)
	}
}

func TestConnectionLessTransport_Listen_UsesConfig(t *testing.T) {
	t.Parallel()

	lisErr := errors.New("listen failed")
	spy := &spyPacketListenConfig{err: lisErr}
	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{ListenConfig: spy})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	_, gotErr := tp.Listen(t.Context(), "127.0.0.1:0")
	if diff := cmp.Diff(lisErr, gotErr, cmpopts.EquateErrors()); diff != "" {
		t.Fatalf("tp.Listen() error = %v, want %v\ndiff (-want +got):\n%s", gotErr, lisErr, diff)
	}

	if spy.called != 1 {
		t.Fatalf("tp.Listen() listen calls = %d, want 1", spy.called)
	}
}

func TestConnectionLessListener_Serve_Duplicate(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close(t.Context()) })

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
		_, err := tp.AcquireConnection(t.Context(), laddr, transport.AcquireConnectionOptions{LocalAddr: laddr})
		return err == nil
	})

	err = tp.ServeListener(t.Context(), conn)
	if !errors.Is(err, transport.ErrListenerTracked) {
		t.Fatalf("tp.ServeListener() error = %v, want %v", err, transport.ErrListenerTracked)
	}

	cancel()
	conn.Close()
	tp.Close(t.Context())

	if err := <-done; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("tp.ServeListener() error = %v, want context canceled or closed", err)
	}
}

func TestConnectionLessTransport_SendAndReceive(t *testing.T) {
	t.Parallel()

	srv, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	t.Cleanup(func() { srv.Close() })

	srvAddr := netip.MustParseAddrPort(srv.LocalAddr().String())
	meta := sip.UDPMetadata()
	meta.DefaultPort = srvAddr.Port()

	tp, err := transport.NewConnectionLessTransport(meta)
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close(t.Context()) })

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

	if got, want := via.Transport, sip.TransportProto("UDP"); !got.Equal(want) {
		t.Fatalf("Via.Transport = %v, want %v", got, want)
	}

	if branch, ok := via.Branch(); !ok || branch == "" {
		t.Fatalf("Via.Branch = %q (ok=%v), want non-empty", branch, ok)
	}

	resp := newMinResp(t, "UDP", sip.AddrFromHostPort(srvAddr.Addr().String(), srvAddr.Port()))

	outRes := sip.NewResponseEnvelope(resp).
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
		t.Fatalf("To.Tag = %q (ok=%v), want non-empty", tag, ok)
	}
}

func TestConnectionLessTransport_InterceptInboundMessages(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{PublicAddr: sip.AddrFromHost("127.0.0.1")})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}

	t.Cleanup(func() { conn.Close() })

	reqCh := make(chan *sip.RequestEnvelope, 1)
	resCh := make(chan *sip.ResponseEnvelope, 1)

	tp.UseInboundRequestInterceptor(
		sip.InboundRequestInterceptorFunc(
			func(ctx context.Context, next sip.RequestReceiver, req *sip.RequestEnvelope) error {
				reqCh <- req
				return nil
			},
		),
	)
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
		Transport: "UDP",
		Addr:      sip.AddrFromHostPort(laddr.Addr().String(), laddr.Port()),
		Params:    make(sip.Values).Set("branch", sip.GenerateBranch(0)),
	}})

	_, err = conn.WriteTo([]byte(req.Render()), net.UDPAddrFromAddrPort(laddr))
	if err != nil {
		t.Fatalf("conn.WriteTo() error = %v, want nil", err)
	}

	select {
	case <-reqCh:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("inbound request not received")
	}

	res := newMinResp(t, "UDP", sip.AddrFromHost(laddr.Addr().String()))

	_, err = conn.WriteTo([]byte(res.Render()), net.UDPAddrFromAddrPort(laddr))
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
	tp.Close(t.Context())
	<-done
}

func TestConnectionLessTransport_RecvRequest_SetsReceivedAndRPort(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

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
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	waitFor(t, func() bool {
		_, err := tp.AcquireConnection(t.Context(), lisAddr, transport.AcquireConnectionOptions{LocalAddr: lisAddr})
		return err == nil
	})

	req := newMinReq(t)
	viaParams := make(sip.Values).
		Set("branch", sip.GenerateBranch(0)).
		Set("rport", "")
	req.Headers.Set(header.ContentLength(0))
	req.Headers.Set(header.Via{header.ViaHop{
		Proto:     sip.ProtoVer20(),
		Transport: "UDP",
		Addr:      sip.AddrFromHost("example.com"),
		Params:    viaParams,
	}})

	if _, err := peer.WriteTo([]byte(req.Render()), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo() error = %v, want nil", err)
	}

	var gotReq *sip.RequestEnvelope
	select {
	case gotReq = <-reqCh:
	case <-time.After(asyncEventTimeout):
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

func TestConnectionLessTransport_ReceiveResponse_MatchSentBy(t *testing.T) {
	t.Parallel()

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{PublicAddr: sip.AddrFromHostPort("127.0.0.1", lisAddr.Port())})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

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
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	waitFor(t, func() bool {
		_, err := tp.AcquireConnection(t.Context(), lisAddr, transport.AcquireConnectionOptions{LocalAddr: lisAddr})
		return err == nil
	})

	matchAddr := sip.AddrFromHostPort(lisAddr.Addr().String(), lisAddr.Port())
	res := newMinResp(t, "UDP", matchAddr)
	res.Headers.Set(header.ContentLength(0))

	if _, err := peer.WriteTo([]byte(res.Render()), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(match) error = %v, want nil", err)
	}

	select {
	case <-resCh:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("inbound response not received")
	}

	res = newMinResp(t, "UDP", sip.AddrFromHostPort("192.0.2.1", lisAddr.Port()))
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

func TestConnectionLessTransport_RecvResponse_PanicContinues(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{PublicAddr: sip.AddrFromHost("127.0.0.1")})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

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
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	waitFor(t, func() bool {
		_, err := tp.AcquireConnection(t.Context(), lisAddr, transport.AcquireConnectionOptions{LocalAddr: lisAddr})
		return err == nil
	})

	matchAddr := sip.AddrFromHost(lisAddr.Addr().String())
	res := newMinResp(t, "UDP", matchAddr)
	res.Headers.Set(header.ContentLength(0))

	if _, err := peer.WriteTo([]byte(res.Render()), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(first) error = %v, want nil", err)
	}

	res = newMinResp(t, "UDP", matchAddr)
	res.Headers.Set(header.ContentLength(0))

	if _, err := peer.WriteTo([]byte(res.Render()), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(second) error = %v, want nil", err)
	}

	select {
	case <-resCh:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("inbound response not received after panic")
	}
}

func TestConnectionLessTransport_RecvRequest_PanicRespondsAndContinues(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

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
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	waitFor(t, func() bool {
		_, err := tp.AcquireConnection(t.Context(), lisAddr, transport.AcquireConnectionOptions{LocalAddr: lisAddr})
		return err == nil
	})

	firstReq := newMinReq(t)
	firstReq.Headers.Set(header.Via{newViaHop(t, "UDP", sip.AddrFromHostPort(peerAddr.Addr().String(), peerAddr.Port()))})
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
	case <-time.After(asyncEventTimeout):
		t.Fatalf("response not sent after panic")
	}

	secondReq := newMinReq(t)
	secondReq.Headers.Set(header.Via{newViaHop(t, "UDP", sip.AddrFromHostPort(peerAddr.Addr().String(), peerAddr.Port()))})
	secondReq.Headers.Set(header.ContentLength(0))

	if _, err := peer.WriteTo([]byte(secondReq.Render()), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(second) error = %v, want nil", err)
	}

	select {
	case <-reqCh:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("inbound request not received after panic")
	}
}

func TestConnectionLessTransport_RecvRequest_ParseErrorRespondsBadRequest(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

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
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	waitFor(t, func() bool {
		_, err := tp.AcquireConnection(t.Context(), lisAddr, transport.AcquireConnectionOptions{LocalAddr: lisAddr})
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

func TestConnectionLessTransport_RecvRequest_ParseErrorRespondsRequestEntityTooLarge(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

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
	defer cancel()

	go func() { _ = tp.ServeListener(ctx, lis) }()

	waitFor(t, func() bool {
		_, err := tp.AcquireConnection(t.Context(), lisAddr, transport.AcquireConnectionOptions{LocalAddr: lisAddr})
		return err == nil
	})

	request := newMinReq(t)
	request.Headers.Set(header.Via{newViaHop(t, "UDP", sip.AddrFromHostPort(peerAddr.Addr().String(), peerAddr.Port()))})
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
	case <-time.After(asyncEventTimeout):
		t.Fatalf("response not sent")
	}
}

func TestConnectionLessTransport_RecvResponse_ParseErrorDiscarded(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

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

func TestConnectionLessTransport_SendRequest_SentBy(t *testing.T) {
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
		sentBy := sip.AddrFromHostPort("sentby.example.com", 5070)

		tp, err := transport.NewConnectionLessTransport(meta, transport.TransportOptions{PublicAddr: sentBy})
		if err != nil {
			t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
		}

		t.Cleanup(func() { tp.Close(t.Context()) })

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

		wantAddr := sip.AddrFromHostPort(sentBy.Host(), 5070)
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
		meta := sip.UDPMetadata()
		meta.DefaultPort = srvAddr.Port()

		tp, err := transport.NewConnectionLessTransport(meta, transport.TransportOptions{PublicAddr: sip.AddrFromHostPort("", 0)})
		if err != nil {
			t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
		}

		t.Cleanup(func() { tp.Close(t.Context()) })

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		done := make(chan error, 1)
		go func() { done <- tp.ServeListener(ctx, lis) }()

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
		meta := sip.UDPMetadata()
		meta.DefaultPort = srvAddr.Port()

		tp, err := transport.NewConnectionLessTransport(meta, transport.TransportOptions{PublicAddr: sip.AddrFromHostPort("", 0)})
		if err != nil {
			t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
		}

		t.Cleanup(func() { tp.Close(t.Context()) })

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

func TestConnectionLessTransport_SendResponse_FallbackDNS(t *testing.T) {
	t.Parallel()

	srv, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}

	t.Cleanup(func() { srv.Close() })

	srvAddr := netip.MustParseAddrPort(srv.LocalAddr().String())
	meta := sip.UDPMetadata()
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

	tp, err := transport.NewConnectionLessTransport(meta, transport.TransportOptions{
		ClientLocator: &sip.RemoteElementLocator{
			DNSResolver:      dnsResolver,
			MetadataProvider: &multiTransportProvider{metas: []sip.TransportMetadata{meta}},
		},
	})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

	viaAddr := sip.AddrFromHostPort("example.com", srvAddr.Port())
	resp := newMinResp(t, "UDP", viaAddr)

	outRes := sip.NewResponseEnvelope(resp).
		SetRemoteAddr(netip.AddrPort{})

	if err := tp.SendResponse(t.Context(), outRes); err != nil {
		t.Fatalf("tp.SendResponse() error = %v, want nil", err)
	}

	msg := readUDPMsg(t, srv)
	if _, ok := msg.(*sip.Response); !ok {
		t.Fatalf("parsed message type = %T, want *sip.Response", msg)
	}
}

type blockingPacketConn struct {
	addr      *net.UDPAddr
	started   chan struct{}
	startOnce sync.Once
	closed    chan struct{}
	closeOnce sync.Once
}

func newBlockingPacketConn() *blockingPacketConn {
	return &blockingPacketConn{
		addr:    &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5060},
		started: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (c *blockingPacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	c.startOnce.Do(func() { close(c.started) })
	<-c.closed
	return 0, nil, net.ErrClosed
}

func (*blockingPacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	return len(p), nil
}

func (c *blockingPacketConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *blockingPacketConn) LocalAddr() net.Addr            { return c.addr }
func (*blockingPacketConn) SetDeadline(time.Time) error      { return nil }
func (*blockingPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (*blockingPacketConn) SetWriteDeadline(time.Time) error { return nil }

type blockingListenConfig struct {
	conn net.PacketConn
}

func (*blockingListenConfig) Listen(context.Context, string, string) (net.Listener, error) {
	return nil, errors.New("stream listener not supported in test")
}

func (c *blockingListenConfig) ListenPacket(context.Context, string, string) (net.PacketConn, error) {
	return c.conn, nil
}

func TestConnectionLessListener_ServeRejectsConcurrentCall(t *testing.T) {
	t.Parallel()

	packetConn := newBlockingPacketConn()
	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.ConnectionLessTransportOptions{
		ListenConfig: &blockingListenConfig{conn: packetConn},
	})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = tp.Close(context.Background()) })

	listener, err := tp.Listen(context.Background(), "127.0.0.1:5060")
	if err != nil {
		t.Fatalf("tp.Listen() error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- listener.Serve(ctx) }()

	select {
	case <-packetConn.started:
	case <-time.After(time.Second):
		t.Fatal("ls.Serve() did not start reading")
	}

	if err := listener.Serve(ctx); !errors.Is(err, transport.ErrListenerServing) {
		t.Fatalf("ls.Serve() second call error = %v, want %v", err, transport.ErrListenerServing)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ls.Serve() did not stop after context cancellation")
	}
}
