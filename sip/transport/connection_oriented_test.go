package transport_test

import (
	"bytes"
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

func TestConnectionOrientedTransport_Lifecycle(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionOrientedTransport(sip.TCPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionOrientedTransport() error = %v, want nil", err)
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

func TestConnectionOrientedTransport_Listen_UsesConfig(t *testing.T) {
	t.Parallel()

	lisErr := errors.New("listen failed")
	spy := &spyConnListenConfig{err: lisErr}

	tp, err := transport.NewConnectionOrientedTransport(sip.TCPMetadata(), transport.TransportOptions{
		ListenConfig: spy,
	})
	if err != nil {
		t.Fatalf("transport.NewConnectionOrientedTransport() error = %v, want nil", err)
	}

	_, gotErr := tp.Listen(t.Context(), "127.0.0.1:0")
	if diff := cmp.Diff(lisErr, gotErr, cmpopts.EquateErrors()); diff != "" {
		t.Fatalf("tp.Listen() error = %v, want %v\ndiff (-want +got):\n%s", gotErr, lisErr, diff)
	}

	if spy.called != 1 {
		t.Fatalf("tp.Listen() listen calls = %d, want 1", spy.called)
	}
}

func TestConnectionOrientedListener_Serve_Duplicate(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionOrientedTransport(sip.TCPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

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
	if !errors.Is(err, transport.ErrListenerTracked) {
		t.Fatalf("tp.ServeListener() error = %v, want %v", err, transport.ErrListenerTracked)
	}

	cancel()
	base.Close()
	tp.Close(t.Context())
	<-done
}

func TestConnectionOrientedListener_Serve_TemporaryError(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionOrientedTransport(sip.TCPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

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

func TestConnectionOrientedTransport_ServeConn_Duplicate(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionOrientedTransport(sip.TCPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

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
		_, err := tp.AcquireConnection(t.Context(), raddr, transport.AcquireConnectionOptions{LocalAddr: laddr})
		return err == nil
	})

	err = tp.ServeConn(t.Context(), srv)
	if !errors.Is(err, transport.ErrConnectionTracked) {
		t.Fatalf("tp.ServeConn() error = %v, want %v", err, transport.ErrConnectionTracked)
	}

	cancel()
	srv.Close()
	tp.Close(t.Context())
	<-done
}

func TestConnectionOrientedTransport_SendAndReceive(t *testing.T) {
	t.Parallel()

	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { base.Close() })

	tp, err := transport.NewConnectionOrientedTransport(sip.TCPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

	srvCh := make(chan net.Conn, 1)
	go func() {
		c, err := base.Accept()
		if err != nil {
			return
		}

		srvCh <- c
	}()

	req := newMinReq(t)
	srvAddr := netip.MustParseAddrPort(base.Addr().String())

	outReq := sip.NewRequestEnvelope(req).
		SetRemoteAddr(srvAddr)

	if err := tp.SendRequest(t.Context(), outReq); err != nil {
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

	resp := newMinResp(t, "TCP", sip.AddrFromHostPort(srvAddr.Addr().String(), srvAddr.Port()))

	outRes := sip.NewResponseEnvelope(resp).
		SetRemoteAddr(srvAddr)

	if err := tp.SendResponse(t.Context(), outRes); err != nil {
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

func TestConnectionOrientedTransport_InterceptInboundMessages(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionOrientedTransport(sip.TCPMetadata(), transport.TransportOptions{PublicAddr: sip.AddrFromHost("127.0.0.1")})
	if err != nil {
		t.Fatalf("transport.NewConnectionOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { base.Close() })

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
			Addr:      sip.AddrFromHostPort("127.0.0.1", 5060),
			Params:    make(sip.Values).Set("branch", sip.GenerateBranch(0)),
		}})
		req.Headers.Set(header.ContentLength(0))
		_, err := cln.Write([]byte(req.Render()))

		return err == nil
	})

	select {
	case <-reqCh:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("inbound request not received")
	}

	laddr := netip.MustParseAddrPort(base.Addr().String())
	res := newMinResp(t, "TCP", sip.AddrFromHost(laddr.Addr().String()))
	res.Headers.Set(header.ContentLength(0))

	if _, err := cln.Write([]byte(res.Render())); err != nil {
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
	tp.Close(t.Context())
	<-done
}

func TestConnectionOrientedTransport_RecvRequest_PanicResponds(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionOrientedTransport(sip.TCPMetadata(), transport.TransportOptions{PublicAddr: sip.AddrFromHostPort("127.0.0.1", 5060)})
	if err != nil {
		t.Fatalf("transport.NewConnectionOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	var panicOnce sync.Once
	tp.UseInboundRequestInterceptor(
		sip.InboundRequestInterceptorFunc(
			func(ctx context.Context, next sip.RequestReceiver, req *sip.RequestEnvelope) error {
				panicked := false
				panicOnce.Do(func() { panicked = true })

				if panicked {
					panic(errors.New("boom"))
				}

				return nil
			},
		),
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
		_, err := tp.AcquireConnection(t.Context(), clnAddr, transport.AcquireConnectionOptions{LocalAddr: lisAddr})
		return err == nil
	})

	req := newMinReq(t)
	req.Headers.Set(header.ContentLength(0))
	req.Headers.Set(header.Via{newViaHop(t, "TCP", sip.AddrFromHostPort(clnAddr.Addr().String(), clnAddr.Port()))})

	if _, err := cln.Write([]byte(req.Render())); err != nil {
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

func TestConnectionOrientedTransport_RecvRequest_ParseErrorRespondsBadRequest(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionOrientedTransport(sip.TCPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

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
		_, err := tp.AcquireConnection(t.Context(), clnAddr, transport.AcquireConnectionOptions{LocalAddr: lisAddr})
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

func TestConnectionOrientedTransport_RecvRequest_ParseErrorRespondsMessageTooLarge(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionOrientedTransport(sip.TCPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

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

	cln, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}
	defer cln.Close()

	clnAddr := netip.MustParseAddrPort(cln.LocalAddr().String())
	lisAddr := netip.MustParseAddrPort(lis.Addr().String())

	waitFor(t, func() bool {
		_, err := tp.AcquireConnection(t.Context(), clnAddr, transport.AcquireConnectionOptions{LocalAddr: lisAddr})
		return err == nil
	})

	req := newMinReq(t)
	req.Body = bytes.Repeat([]byte("a"), int(math.MaxUint16))
	req.Headers.Set(header.ContentLength(len(req.Body)))
	req.Headers.Set(header.Via{newViaHop(t, "TCP", sip.AddrFromHostPort(clnAddr.Addr().String(), clnAddr.Port()))})

	if _, err := cln.Write([]byte(req.Render())); err != nil {
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

func TestConnectionOrientedTransport_RecvResponse_ParseErrorDiscarded(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnectionOrientedTransport(sip.TCPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

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

	cln, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}
	defer cln.Close()

	clnAddr := netip.MustParseAddrPort(cln.LocalAddr().String())
	lisAddr := netip.MustParseAddrPort(lis.Addr().String())

	waitFor(t, func() bool {
		_, err := tp.AcquireConnection(t.Context(), clnAddr, transport.AcquireConnectionOptions{LocalAddr: lisAddr})
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

func TestConnectionOrientedTransport_ReceiveResponse_MatchSentBy(t *testing.T) {
	t.Parallel()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.Addr().String())

	tp, err := transport.NewConnectionOrientedTransport(sip.TCPMetadata(), transport.TransportOptions{PublicAddr: sip.AddrFromHostPort("127.0.0.1", lisAddr.Port())})
	if err != nil {
		t.Fatalf("transport.NewConnectionOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

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

	cln, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}
	defer cln.Close()

	clnAddr := netip.MustParseAddrPort(cln.LocalAddr().String())
	waitFor(t, func() bool {
		_, err := tp.AcquireConnection(t.Context(), clnAddr, transport.AcquireConnectionOptions{LocalAddr: lisAddr})
		return err == nil
	})

	matchAddr := sip.AddrFromHostPort(lisAddr.Addr().String(), lisAddr.Port())
	res := newMinResp(t, "TCP", matchAddr)
	res.Headers.Set(header.ContentLength(0))

	if _, err := cln.Write([]byte(res.Render())); err != nil {
		t.Fatalf("client.Write(match) error = %v, want nil", err)
	}

	select {
	case <-resCh:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("inbound response not received")
	}

	res = newMinResp(t, "TCP", sip.AddrFromHostPort("192.0.2.2", lisAddr.Port()))
	res.Headers.Set(header.ContentLength(0))

	if _, err := cln.Write([]byte(res.Render())); err != nil {
		t.Fatalf("client.Write(mismatch) error = %v, want nil", err)
	}

	select {
	case <-resCh:
		t.Fatalf("unexpected inbound response for mismatched sent-by")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestConnectionOrientedTransport_SendRequest_SentBy(t *testing.T) {
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

		tp, err := transport.NewConnectionOrientedTransport(sip.TCPMetadata(), transport.TransportOptions{PublicAddr: sentBy})
		if err != nil {
			t.Fatalf("transport.NewConnectionOrientedTransport() error = %v, want nil", err)
		}

		t.Cleanup(func() { tp.Close(t.Context()) })

		serverCh := make(chan net.Conn, 1)
		go func() {
			conn, err := lis.Accept()
			if err != nil {
				return
			}

			serverCh <- conn
		}()

		outReq := sip.NewRequestEnvelope(newMinReq(t)).
			SetRemoteAddr(lisAddr)

		if err := tp.SendRequest(t.Context(), outReq); err != nil {
			t.Fatalf("tp.SendRequest() error = %v, want nil", err)
		}

		srvConn := <-serverCh
		defer srvConn.Close()

		msg := readTCPMsg(t, srvConn)

		parsedReq, ok := msg.(*sip.Request)
		if !ok {
			t.Fatalf("parsed message type = %T, want *sip.Request", msg)
		}

		via, ok := parsedReq.Headers.FirstVia()
		if !ok {
			t.Fatalf("parsed request Via header missing")
		}

		wantAddr := sip.AddrFromHostPort(sentBy.Host(), 5071)
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

		tp, err := transport.NewConnectionOrientedTransport(sip.TCPMetadata(), transport.TransportOptions{PublicAddr: sip.AddrFromHostPort("", 0)})
		if err != nil {
			t.Fatalf("transport.NewConnectionOrientedTransport() error = %v, want nil", err)
		}

		t.Cleanup(func() { tp.Close(t.Context()) })

		srvCh := make(chan net.Conn, 1)
		go func() {
			conn, err := lis.Accept()
			if err != nil {
				return
			}

			srvCh <- conn
		}()

		outReq := sip.NewRequestEnvelope(newMinReq(t)).
			SetRemoteAddr(lisAddr)

		if err := tp.SendRequest(t.Context(), outReq); err != nil {
			t.Fatalf("tp.SendRequest() error = %v, want nil", err)
		}

		srvConn := <-srvCh
		defer srvConn.Close()

		msg := readTCPMsg(t, srvConn)

		parsedReq, ok := msg.(*sip.Request)
		if !ok {
			t.Fatalf("parsed message type = %T, want *sip.Request", msg)
		}

		via, ok := parsedReq.Headers.FirstVia()
		if !ok {
			t.Fatalf("parsed request Via header missing")
		}

		rmtAddr := netip.MustParseAddrPort(srvConn.RemoteAddr().String())
		if gotPort, ok := via.Addr.Port(); !ok || gotPort != rmtAddr.Port() {
			t.Fatalf("Via.sent-by port = %d (ok=%v), want %d", gotPort, ok, rmtAddr.Port())
		}
	})
}

func TestConnectionOrientedTransport_SendResponse_FallbackDNS(t *testing.T) {
	t.Parallel()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.Addr().String())
	meta := sip.TCPMetadata()
	meta.DefaultPort = lisAddr.Port()

	callCnt := 0
	dialer := connectionDialerFunc(func(ctx context.Context, network, addr string) (net.Conn, error) {
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

	tp, err := transport.NewConnectionOrientedTransport(meta, transport.TransportOptions{
		ClientLocator: &sip.RemoteElementLocator{
			DNSResolver:      dnsResolver,
			MetadataProvider: &multiTransportProvider{metas: []sip.TransportMetadata{meta}},
		},
		ConnectionDialer: dialer,
	})
	if err != nil {
		t.Fatalf("transport.NewConnectionOrientedTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close(t.Context()) })

	srvCh := make(chan net.Conn, 1)
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}

		srvCh <- conn
	}()

	viaAddr := sip.AddrFromHostPort("example.com", lisAddr.Port())
	resp := newMinResp(t, "TCP", viaAddr)
	resp.Headers.Set(header.ContentLength(0))

	outRes := sip.NewResponseEnvelope(resp).
		SetRemoteAddr(netip.AddrPort{})

	if err := tp.SendResponse(t.Context(), outRes); err != nil {
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
