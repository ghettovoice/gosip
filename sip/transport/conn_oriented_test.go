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

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/internal/testutil/dnstest"
	"github.com/ghettovoice/gosip/internal/testutil/siptest"
	"github.com/ghettovoice/gosip/pkg/dns"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/transport"
)

func TestConnOrientedTransport_Lifecycle(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnOrientedTransport(sip.TCPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
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

func TestConnOrientedTransport_Listen_UsesConfig(t *testing.T) {
	t.Parallel()

	lisErr := errors.New("listen failed")
	spy := &spyLisConfig{listenErr: lisErr}

	tp, err := transport.NewConnOrientedTransport(sip.TCPMetadata(), transport.TransportOptions{
		ListenConfig: spy,
	})
	if err != nil {
		t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
	}

	_, gotErr := tp.Listen(t.Context(), "127.0.0.1:0")
	if diff := cmp.Diff(lisErr, gotErr, cmpopts.EquateErrors()); diff != "" {
		t.Fatalf("tp.Listen() error = %v, want %v\ndiff (-want +got):\n%s", gotErr, lisErr, diff)
	}

	if spy.listenCalls != 1 || spy.listenPktCalls != 0 {
		t.Fatalf("listen calls = %d, listen packet calls = %d, want 1 and 0", spy.listenCalls, spy.listenPktCalls)
	}
	if spy.network != "tcp" {
		t.Fatalf("listen network = %q, want %q", spy.network, "tcp")
	}
	if spy.addr != "127.0.0.1:0" {
		t.Fatalf("listen address = %q, want %q", spy.addr, "127.0.0.1:0")
	}
}

func TestConnOrientedTransport_ServeListener_Duplicate(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnOrientedTransport(sip.TCPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
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
	case <-time.After(asyncEvtTimeout):
		t.Fatalf("accept loop not started")
	}

	err = tp.ServeListener(t.Context(), lis)
	if !errors.Is(err, transport.ErrListenerTracked) {
		t.Fatalf("tp.ServeListener() error = %v, want %v", err, transport.ErrListenerTracked)
	}

	cancel()
	base.Close()
	tp.Close()

	err = testutil.WaitResult(t, done, "tp.ServeListener()")
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("tp.ServeListener() error = %v, want context canceled or closed", err)
	}
}

func TestConnOrientedTransport_ServeListener_TemporaryError(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnOrientedTransport(sip.TCPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}
	t.Cleanup(func() { base.Close() })

	lis := &tempErrListener{
		Listener:    base,
		errInjected: make(chan struct{}),
		accepted:    make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- tp.ServeListener(ctx, lis) }()

	testutil.WaitClosed(t, lis.errInjected, "temporary Accept error")

	client, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	testutil.WaitClosed(t, lis.accepted, "successful delegated Accept")

	_ = client.Close()
	cancel()
	base.Close()

	err = testutil.WaitResult(t, done, "tp.ServeListener()")
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("tp.ServeListener() error = %v, want context canceled or closed", err)
	}
}

func TestConnOrientedTransport_ServeConn_Duplicate(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnOrientedTransport(sip.TCPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	waitAccept := startTestAccept(t, base)

	client, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}
	t.Cleanup(func() { client.Close() })

	srv := waitAccept()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- tp.ServeConn(ctx, srv) }()

	laddr := netip.MustParseAddrPort(srv.LocalAddr().String())
	raddr := netip.MustParseAddrPort(srv.RemoteAddr().String())

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), raddr, transport.AcquireConnOptions{
			LocalAddr: laddr,
		})
		return err == nil
	})

	err = tp.ServeConn(t.Context(), srv)
	if !errors.Is(err, transport.ErrConnTracked) {
		t.Fatalf("tp.ServeConn() error = %v, want %v", err, transport.ErrConnTracked)
	}

	cancel()
	srv.Close()
	tp.Close()

	err = testutil.WaitResult(t, done, "tp.ServeConn()")
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("tp.ServeConn() error = %v, want context canceled or closed", err)
	}
}

func TestConnOrientedTransport_SendAndReceive(t *testing.T) {
	t.Parallel()

	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}
	waitAccept := startTestAccept(t, base)

	tp, err := transport.NewConnOrientedTransport(sip.TCPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

	srvAddr := netip.MustParseAddrPort(base.Addr().String())
	req := newMinReq(t)
	outReq := sip.NewRequestEnvelope(req).SetRemoteAddr(srvAddr)

	if err := tp.SendRequest(t.Context(), outReq); err != nil {
		t.Fatalf("tp.SendRequest() error = %v, want nil", err)
	}

	srvConn := waitAccept()
	rdr := siptest.NewStreamReader(t, srvConn)

	msg := rdr.Read(t)
	parsedReq, ok := msg.(*sip.Request)
	if !ok {
		t.Fatalf("parsed message type = %T, want *sip.Request", msg)
	}

	if _, ok := parsedReq.Headers.ContentLength(); !ok {
		t.Fatalf("Content-Length header missing")
	}

	res := newMinRes(t, tp.Metadata().Proto, sip.MakeHostPortAddr(srvAddr.Addr().String(), srvAddr.Port()))
	outRes := sip.NewResponseEnvelope(res).SetRemoteAddr(srvAddr)

	if err := tp.SendResponse(t.Context(), outRes); err != nil {
		t.Fatalf("tp.SendResponse() error = %v, want nil", err)
	}

	resMsg := rdr.Read(t)
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
		t.Fatalf("to.Tag = %q (ok=%v), want non-empty", tag, ok)
	}
}

func TestConnOrientedTransport_InterceptInboundMessages(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnOrientedTransport(sip.TCPMetadata(), transport.TransportOptions{
		PublicAddr: sip.MakeHostAddr("127.0.0.1"),
	})
	if err != nil {
		t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}
	t.Cleanup(func() { base.Close() })

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
			Transport: tp.Metadata().Proto,
			Addr:      sip.MakeHostPortAddr("127.0.0.1", 5060),
			Params:    make(sip.Values).Set("branch", sip.GenerateBranch(0)),
		}})
		req.Headers.Set(header.ContentLength(0))
		_, err := cln.Write([]byte(req.Render()))
		return err == nil
	})

	select {
	case <-reqCh:
	case <-time.After(asyncEvtTimeout):
		t.Fatalf("inbound request not received")
	}

	laddr := netip.MustParseAddrPort(base.Addr().String())
	res := newMinRes(t, tp.Metadata().Proto, sip.MakeHostAddr(laddr.Addr().String()))
	res.Headers.Set(header.ContentLength(0))

	if _, err := cln.Write([]byte(res.Render())); err != nil {
		t.Fatalf("cln.Write() error = %v, want nil", err)
	}

	select {
	case <-resCh:
	case <-time.After(asyncEvtTimeout):
		t.Fatalf("inbound response not received")
	}

	cancel()
	base.Close()
	cln.Close()
	tp.Close()

	err = testutil.WaitResult(t, done, "tp.ServeListener()")
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("tp.ServeListener() error = %v, want context canceled or closed", err)
	}
}

func TestConnOrientedTransport_RecvRequest_PanicResponds(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnOrientedTransport(sip.TCPMetadata(), transport.TransportOptions{
		PublicAddr: sip.MakeHostPortAddr("127.0.0.1", 5060),
	})
	if err != nil {
		t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

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
	t.Cleanup(cancel)

	go func() { _ = tp.ServeListener(ctx, lis) }()

	cln, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = cln.Close() })

	clnAddr := netip.MustParseAddrPort(cln.LocalAddr().String())
	lisAddr := netip.MustParseAddrPort(lis.Addr().String())

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), clnAddr, transport.AcquireConnOptions{LocalAddr: lisAddr})
		return err == nil
	})

	req := newMinReq(t)
	req.Headers.Set(header.ContentLength(0))
	req.Headers.Set(header.Via{
		newViaHop(t, tp.Metadata().Proto, sip.MakeHostPortAddr(clnAddr.Addr().String(), clnAddr.Port())),
	})

	if _, err := cln.Write([]byte(req.Render())); err != nil {
		t.Fatalf("cln.Write() error = %v, want nil", err)
	}

	msg := siptest.NewStreamReader(t, cln).Read(t)
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

	tp, err := transport.NewConnOrientedTransport(sip.TCPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}
	t.Cleanup(func() { lis.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	go func() { _ = tp.ServeListener(ctx, lis) }()

	cln, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = cln.Close() })

	clnAddr := netip.MustParseAddrPort(cln.LocalAddr().String())
	lisAddr := netip.MustParseAddrPort(lis.Addr().String())

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), clnAddr, transport.AcquireConnOptions{LocalAddr: lisAddr})
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
		t.Fatalf("cln.Write(invalid) error = %v, want nil", err)
	}

	msg := siptest.NewStreamReader(t, cln).Read(t)
	res, ok := msg.(*sip.Response)
	if !ok {
		t.Fatalf("parsed message type = %T, want *sip.Response", msg)
	}

	if got, want := res.Status, sip.ResponseStatusBadRequest; got != want {
		t.Fatalf("response status = %v, want %v", got, want)
	}
}

func TestConnOrientedTransport_RecvRequest_ParseErrorRespondsRequestEntityTooLarge(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnOrientedTransport(sip.TCPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

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
	t.Cleanup(cancel)

	go func() { _ = tp.ServeListener(ctx, lis) }()

	cln, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = cln.Close() })

	clnAddr := netip.MustParseAddrPort(cln.LocalAddr().String())
	lisAddr := netip.MustParseAddrPort(lis.Addr().String())

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), clnAddr, transport.AcquireConnOptions{LocalAddr: lisAddr})
		return err == nil
	})

	req := newMinReq(t)
	req.Body = bytes.Repeat([]byte("a"), int(math.MaxUint16))
	req.Headers.Set(header.ContentLength(len(req.Body)))
	req.Headers.Set(header.Via{
		newViaHop(t, tp.Metadata().Proto, sip.MakeHostPortAddr(clnAddr.Addr().String(), clnAddr.Port())),
	})

	if _, err := cln.Write([]byte(req.Render())); err != nil {
		t.Fatalf("cln.Write() error = %v, want nil", err)
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

func TestConnOrientedTransport_RecvResponse_ParseErrorDiscarded(t *testing.T) {
	t.Parallel()

	tp, err := transport.NewConnOrientedTransport(sip.TCPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

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
	t.Cleanup(cancel)

	go func() { _ = tp.ServeListener(ctx, lis) }()

	cln, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = cln.Close() })

	clnAddr := netip.MustParseAddrPort(cln.LocalAddr().String())
	lisAddr := netip.MustParseAddrPort(lis.Addr().String())

	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), clnAddr, transport.AcquireConnOptions{LocalAddr: lisAddr})
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
		t.Fatalf("cln.Write(invalid) error = %v, want nil", err)
	}

	select {
	case <-resCh:
		t.Fatalf("unexpected inbound response for parse error")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestConnOrientedTransport_RecvResponse_MatchSentBy(t *testing.T) {
	t.Parallel()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}
	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.Addr().String())

	tp, err := transport.NewConnOrientedTransport(sip.TCPMetadata(), transport.TransportOptions{
		PublicAddr: sip.MakeHostPortAddr("127.0.0.1", lisAddr.Port()),
	})
	if err != nil {
		t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

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

	cln, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = cln.Close() })

	clnAddr := netip.MustParseAddrPort(cln.LocalAddr().String())
	waitFor(t, func() bool {
		_, err := tp.AcquireConn(t.Context(), clnAddr, transport.AcquireConnOptions{
			LocalAddr: lisAddr,
		})
		return err == nil
	})

	matchAddr := sip.MakeHostPortAddr(lisAddr.Addr().String(), lisAddr.Port())
	res := newMinRes(t, tp.Metadata().Proto, matchAddr)
	res.Headers.Set(header.ContentLength(0))

	if _, err := cln.Write([]byte(res.Render())); err != nil {
		t.Fatalf("cln.Write(match) error = %v, want nil", err)
	}

	select {
	case <-resCh:
	case <-time.After(asyncEvtTimeout):
		t.Fatalf("inbound response not received")
	}

	res = newMinRes(t, tp.Metadata().Proto, sip.MakeHostPortAddr("192.0.2.2", lisAddr.Port()))
	res.Headers.Set(header.ContentLength(0))

	if _, err := cln.Write([]byte(res.Render())); err != nil {
		t.Fatalf("cln.Write(mismatch) error = %v, want nil", err)
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
		waitAccept := startTestAccept(t, lis)

		lisAddr := netip.MustParseAddrPort(lis.Addr().String())
		sentBy := sip.MakeHostPortAddr("sentby.example.com", 5071)

		tp, err := transport.NewConnOrientedTransport(sip.TCPMetadata(), transport.TransportOptions{
			PublicAddr: sentBy,
		})
		if err != nil {
			t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
		}
		t.Cleanup(func() { tp.Close() })

		outReq := sip.NewRequestEnvelope(newMinReq(t)).SetRemoteAddr(lisAddr)

		if err := tp.SendRequest(t.Context(), outReq); err != nil {
			t.Fatalf("tp.SendRequest() error = %v, want nil", err)
		}

		srvConn := waitAccept()
		msg := siptest.NewStreamReader(t, srvConn).Read(t)
		parsedReq, ok := msg.(*sip.Request)
		if !ok {
			t.Fatalf("parsed message type = %T, want *sip.Request", msg)
		}

		via, ok := parsedReq.Headers.FirstVia()
		if !ok {
			t.Fatalf("parsed request Via header missing")
		}

		wantAddr := sip.MakeHostPortAddr(sentBy.Host(), 5071)
		if !via.Addr.Equal(wantAddr) {
			t.Fatalf("via.sent-by = %q, want %q", via.Addr, wantAddr)
		}
	})

	t.Run("from connection", func(t *testing.T) {
		t.Parallel()

		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen() error = %v, want nil", err)
		}

		waitAccept := startTestAccept(t, lis)
		lisAddr := netip.MustParseAddrPort(lis.Addr().String())

		tp, err := transport.NewConnOrientedTransport(sip.TCPMetadata(), transport.TransportOptions{
			PublicAddr: sip.MakeHostPortAddr("", 0),
		})
		if err != nil {
			t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
		}
		t.Cleanup(func() { tp.Close() })

		outReq := sip.NewRequestEnvelope(newMinReq(t)).SetRemoteAddr(lisAddr)

		if err := tp.SendRequest(t.Context(), outReq); err != nil {
			t.Fatalf("tp.SendRequest() error = %v, want nil", err)
		}

		srvConn := waitAccept()
		msg := siptest.NewStreamReader(t, srvConn).Read(t)
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
			t.Fatalf("via.sent-by port = %d (ok=%v), want %d", gotPort, ok, rmtAddr.Port())
		}
	})
}

func TestConnOrientedTransport_SendResponse_FallbackDNS(t *testing.T) {
	t.Parallel()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v, want nil", err)
	}

	waitAccept := startTestAccept(t, lis)
	lisAddr := netip.MustParseAddrPort(lis.Addr().String())
	meta := sip.TCPMetadata()
	meta.DefaultPort = lisAddr.Port()

	callCnt := 0
	dialer := connDialerFunc(func(ctx context.Context, network, addr string) (net.Conn, error) {
		callCnt++
		if callCnt == 1 {
			return nil, errors.New("dial failed")
		}

		return (&net.Dialer{}).DialContext(ctx, network, addr)
	})

	dnsResolver := dnstest.StubResolver{
		LookupIPFunc: func(ctx context.Context, network, host string) ([]net.IP, error) {
			if host != "example.com" {
				return nil, errors.New("unexpected host")
			}
			return []net.IP{net.ParseIP("127.0.0.2"), net.ParseIP("127.0.0.1")}, nil
		},
		LookupSRVFunc: func(ctx context.Context, service, proto, host string) ([]*dns.SRV, error) {
			return nil, errors.New("srv lookup not used")
		},
		LookupNAPTRFunc: func(ctx context.Context, host string) ([]*dns.NAPTR, error) {
			return nil, errors.New("naptr lookup not used")
		},
	}

	tp, err := transport.NewConnOrientedTransport(meta, transport.TransportOptions{
		ClientLocator: &sip.RemoteElementLocator{
			DNSResolver:      dnsResolver,
			MetadataProvider: siptest.MetadataProvider{Metas: []sip.TransportMetadata{meta}},
		},
		ConnDialer: dialer,
	})
	if err != nil {
		t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { tp.Close() })

	viaAddr := sip.MakeHostPortAddr("example.com", lisAddr.Port())
	res := newMinRes(t, tp.Metadata().Proto, viaAddr)
	res.Headers.Set(header.ContentLength(0))

	outRes := sip.NewResponseEnvelope(res).SetRemoteAddr(netip.AddrPort{})

	if err := tp.SendResponse(t.Context(), outRes); err != nil {
		t.Fatalf("tp.SendResponse() error = %v, want nil", err)
	}

	srvConn := waitAccept()

	msg := siptest.NewStreamReader(t, srvConn).Read(t)
	if _, ok := msg.(*sip.Response); !ok {
		t.Fatalf("parsed message type = %T, want *sip.Response", msg)
	}

	if callCnt < 2 {
		t.Fatalf("dial calls = %d, want >= 2", callCnt)
	}
}
