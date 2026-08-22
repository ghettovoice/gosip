package sip_test

import (
	"bytes"
	"context"
	"iter"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/transport"
)

const asyncEventTimeout = 5 * time.Second

func newElementTestRequest(tb testing.TB, laddr netip.AddrPort) *sip.Request {
	tb.Helper()

	req, err := sip.NewRequest(
		sip.RequestMethodInvite,
		&sip.URI{User: sip.UserWithName("bob"), Addr: sip.AddrFromHost("example.com")},
		&sip.URI{User: sip.UserWithName("alice"), Addr: sip.AddrFromHost("example.com")},
		&sip.URI{User: sip.UserWithName("bob"), Addr: sip.AddrFromHost("example.com")},
		sip.RequestOptions{Transport: "UDP", Branch: sip.GenerateBranch(0), LocalTag: "from-tag", CallID: "call-id"},
	)
	if err != nil {
		tb.Fatalf("sip.NewRequest() error = %v, want nil", err)
	}

	req.Headers.Set(header.Via{{
		Proto:     sip.ProtoVer20(),
		Transport: "UDP",
		Addr:      sip.AddrFromHostPort(laddr.Addr().String(), laddr.Port()),
		Params:    make(sip.Values).Set("branch", sip.GenerateBranch(0)),
	}})
	req.Headers.Set(header.ContentLength(0))
	return req
}

func newElementTestResponse(tb testing.TB, laddr netip.AddrPort) *sip.Response {
	tb.Helper()

	req := newElementTestRequest(tb, laddr)

	res, err := req.NewResponse(sip.ResponseStatusOK, sip.ResponseOptions{LocalTag: "to-tag"})
	if err != nil {
		tb.Fatalf("req.NewResponse() error = %v, want nil", err)
	}

	res.Headers.Set(header.ContentLength(0))
	return res
}

func TestElement_ReceiveRequestResponse(t *testing.T) {
	t.Parallel()

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(listener) error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	// Create transport and add it to transport manager
	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{PublicAddr: sip.AddrFromHostPort("127.0.0.1", lisAddr.Port())})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	elm, err := sip.NewElement()
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	t.Cleanup(func() { elm.Close(t.Context()) })

	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}

	t.Cleanup(func() { peer.Close() })

	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())
	reqCh := make(chan *sip.RequestEnvelope, 1)
	resCh := make(chan *sip.ResponseEnvelope, 1)

	elm.TransportManager().UseMessageInterceptor(sip.StdMessageInterceptor{
		InboundRequestInterceptor: sip.InboundRequestInterceptorFunc(
			func(ctx context.Context, next sip.RequestReceiver, req *sip.RequestEnvelope) error {
				reqCh <- req
				return next.RecvRequest(ctx, req)
			},
		),
		InboundResponseInterceptor: sip.InboundResponseInterceptorFunc(
			func(ctx context.Context, next sip.ResponseReceiver, res *sip.ResponseEnvelope) error {
				resCh <- res
				return next.RecvResponse(ctx, res)
			},
		),
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- tp.ServeListener(ctx, lis) }()

	t.Cleanup(func() {
		cancel()
		lis.Close()
		<-done
	})

	if _, err := peer.WriteTo(
		[]byte(newElementTestRequest(t, lisAddr).Render()),
		net.UDPAddrFromAddrPort(lisAddr),
	); err != nil {
		t.Fatalf("peer.WriteTo(request) error = %v, want nil", err)
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

	if got := gotReq.Message().Method; got != sip.RequestMethodInvite {
		t.Fatalf("inbound request method = %v, want %v", got, sip.RequestMethodInvite)
	}

	if got := gotReq.Transport(); got != sip.UDPMetadata() {
		t.Fatalf("inbound request transport = %v, want UDP", got)
	}

	if got := gotReq.LocalAddr(); got != lisAddr {
		t.Fatalf("inbound request local addr = %v, want %v", got, lisAddr)
	}

	if got := gotReq.RemoteAddr(); got != peerAddr {
		t.Fatalf("inbound request remote addr = %v, want %v", got, peerAddr)
	}

	if _, err := peer.WriteTo(
		[]byte(newElementTestResponse(t, lisAddr).Render()),
		net.UDPAddrFromAddrPort(lisAddr),
	); err != nil {
		t.Fatalf("peer.WriteTo(response) error = %v, want nil", err)
	}

	var gotRes *sip.ResponseEnvelope
	select {
	case gotRes = <-resCh:
	case <-time.After(asyncEventTimeout):
		t.Fatalf("inbound response not received")
	}

	if gotRes == nil || gotRes.Message() == nil {
		t.Fatalf("inbound response = nil, want non-nil")
	}

	if got := gotRes.Message().Status; got != sip.ResponseStatusOK {
		t.Fatalf("inbound response status = %v, want %v", got, sip.ResponseStatusOK)
	}

	if got := gotRes.Transport(); got != sip.UDPMetadata() {
		t.Fatalf("inbound response transport = %v, want UDP", got)
	}

	if got := gotRes.LocalAddr(); got != lisAddr {
		t.Fatalf("inbound response local addr = %v, want %v", got, lisAddr)
	}

	if got := gotRes.RemoteAddr(); got != peerAddr {
		t.Fatalf("inbound response remote addr = %v, want %v", got, peerAddr)
	}
}

// TestElement_SendRequest groups stateless send scenarios that share a single
// transport/element.
func TestElement_SendRequest(t *testing.T) {
	t.Parallel()

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(listener) error = %v, want nil", err)
	}
	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{PublicAddr: sip.AddrFromHostPort("127.0.0.1", lisAddr.Port())})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	elm, err := sip.NewElement()
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	t.Cleanup(func() { elm.Close(t.Context()) })

	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	tests := []struct {
		name       string
		prepareReq func(t *testing.T, peerAddr netip.AddrPort) *sip.RequestEnvelope
		checkReq   func(t *testing.T, req *sip.Request)
	}{
		{
			name: "with transport and address",
			prepareReq: func(t *testing.T, peerAddr netip.AddrPort) *sip.RequestEnvelope {
				t.Helper()
				req := newElementTestRequest(t, lisAddr)
				return sip.NewRequestEnvelope(req).
					SetTransport(sip.UDPMetadata()).
					SetRemoteAddr(peerAddr)
			},
		},
		{
			name: "resolve target URI",
			prepareReq: func(t *testing.T, peerAddr netip.AddrPort) *sip.RequestEnvelope {
				t.Helper()
				ruri := &sip.URI{User: sip.UserWithName("bob"), Addr: sip.AddrFromHostPort("127.0.0.1", peerAddr.Port())}
				furi := &sip.URI{User: sip.UserWithName("alice"), Addr: sip.AddrFromHost("example.com")}
				turi := &sip.URI{User: sip.UserWithName("bob"), Addr: sip.AddrFromHost("example.com")}
				req, err := sip.NewRequest(
					sip.RequestMethodInvite,
					ruri,
					furi,
					turi,
					sip.RequestOptions{Transport: "UDP", Branch: sip.GenerateBranch(0), LocalTag: "from-tag", CallID: "call-id"},
				)
				if err != nil {
					t.Fatalf("sip.NewRequest() error = %v, want nil", err)
				}
				req.Headers.Set(header.Via{{
					Proto:     sip.ProtoVer20(),
					Transport: "UDP",
					Addr:      sip.AddrFromHostPort("127.0.0.1", lisAddr.Port()),
					Params:    make(sip.Values).Set("branch", sip.GenerateBranch(0)),
				}})
				req.Headers.Set(header.ContentLength(0))
				return sip.NewRequestEnvelope(req)
			},
		},
		{
			name: "no auto user-agent header",
			prepareReq: func(t *testing.T, peerAddr netip.AddrPort) *sip.RequestEnvelope {
				t.Helper()
				ruri := &sip.URI{User: sip.UserWithName("bob"), Addr: sip.AddrFromHostPort("127.0.0.1", peerAddr.Port())}
				furi := &sip.URI{User: sip.UserWithName("alice"), Addr: sip.AddrFromHost("example.com")}
				turi := &sip.URI{User: sip.UserWithName("bob"), Addr: sip.AddrFromHost("example.com")}
				req, err := sip.NewRequest(
					sip.RequestMethodInvite,
					ruri,
					furi,
					turi,
					sip.RequestOptions{Transport: "UDP", Branch: sip.GenerateBranch(0), LocalTag: "from-tag", CallID: "call-id"},
				)
				if err != nil {
					t.Fatalf("sip.NewRequest() error = %v, want nil", err)
				}
				req.Headers.Set(header.Via{{
					Proto:     sip.ProtoVer20(),
					Transport: "UDP",
					Addr:      sip.AddrFromHostPort("127.0.0.1", lisAddr.Port()),
					Params:    make(sip.Values).Set("branch", sip.GenerateBranch(0)),
				}})
				req.Headers.Set(header.ContentLength(0))
				return sip.NewRequestEnvelope(req)
			},
			checkReq: func(t *testing.T, req *sip.Request) {
				t.Helper()
				if ua := req.Headers.Get("User-Agent"); len(ua) != 0 {
					t.Errorf("User-Agent header added unexpectedly: %v", ua)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			peer, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
			}
			t.Cleanup(func() { peer.Close() })

			peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())
			outReq := tt.prepareReq(t, peerAddr)

			recvCh := make(chan []byte, 1)
			go func() {
				buf := make([]byte, 65535)
				peer.SetReadDeadline(time.Now().Add(2 * time.Second))

				n, _, err := peer.ReadFrom(buf)
				if err != nil {
					return
				}
				recvCh <- buf[:n]
			}()

			ctx, cancel := context.WithTimeout(t.Context(), asyncEventTimeout)
			defer cancel()

			err = elm.SendRequest(ctx, outReq)
			if err != nil {
				t.Fatalf("elm.SendRequest() error = %v, want nil", err)
			}

			select {
			case data := <-recvCh:
				msg, err := sip.DefaultParser().ParsePacket(data)
				if err != nil {
					t.Fatalf("sip.DefaultParser().ParsePacket() error = %v, want nil", err)
				}
				if msg == nil {
					t.Fatal("parsed message = nil, want non-nil")
				}
				reqMsg, ok := msg.(*sip.Request)
				if !ok {
					t.Fatalf("message type = %T, want *sip.Request", msg)
				}
				if reqMsg.Method != sip.RequestMethodInvite {
					t.Errorf("request method = %v, want %v", reqMsg.Method, sip.RequestMethodInvite)
				}
				if tt.checkReq != nil {
					tt.checkReq(t, reqMsg)
				}
			case <-time.After(asyncEventTimeout):
				t.Fatal("request not received by peer")
			}
		})
	}
}

func TestElement_SendRequest_NoDestAddressResolved(t *testing.T) {
	t.Parallel()

	elm, err := sip.NewElement()
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	defer elm.Close(t.Context())

	// Create outbound request with URI that cannot be resolved to a transport.
	ruri := &sip.URI{
		User: sip.UserWithName("bob"),
		Addr: sip.AddrFromHostPort("127.0.0.1", 1),
	}
	furi := &sip.URI{User: sip.UserWithName("alice"), Addr: sip.AddrFromHost("example.com")}
	turi := &sip.URI{User: sip.UserWithName("bob"), Addr: sip.AddrFromHost("example.com")}

	req, err := sip.NewRequest(
		sip.RequestMethodInvite,
		ruri,
		furi,
		turi,
		sip.RequestOptions{Transport: "UDP", Branch: sip.GenerateBranch(0), LocalTag: "from-tag", CallID: "call-id"},
	)
	if err != nil {
		t.Fatalf("sip.NewRequest() error = %v, want nil", err)
	}

	req.Headers.Set(header.Via{{
		Proto:     sip.ProtoVer20(),
		Transport: "UDP",
		Addr:      sip.AddrFromHostPort("127.0.0.1", 5060),
		Params:    make(sip.Values).Set("branch", sip.GenerateBranch(0)),
	}})
	req.Headers.Set(header.ContentLength(0))

	ctx, cancel := context.WithTimeout(t.Context(), asyncEventTimeout)
	defer cancel()

	err = elm.SendRequest(ctx, sip.NewRequestEnvelope(req))
	if err == nil {
		t.Fatal("elm.SendRequest() error = nil, want non-nil")
	}
}

// TestElement_SendRequestStateful groups stateful send scenarios that share a single
// transport/element.
func TestElement_SendRequestStateful(t *testing.T) {
	t.Parallel()

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(listener) error = %v, want nil", err)
	}
	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{PublicAddr: sip.AddrFromHostPort("127.0.0.1", lisAddr.Port())})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	elm, err := sip.NewElement()
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	t.Cleanup(func() { elm.Close(t.Context()) })

	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- tp.ServeListener(ctx, lis) }()
	t.Cleanup(func() { <-done })

	tests := []struct {
		name       string
		prepareReq func(t *testing.T, lisAddr, peerAddr netip.AddrPort) *sip.RequestEnvelope
	}{
		{
			name: "with transport and address",
			prepareReq: func(t *testing.T, lisAddr, peerAddr netip.AddrPort) *sip.RequestEnvelope {
				t.Helper()
				req := newElementTestRequest(t, lisAddr)
				return sip.NewRequestEnvelope(req).
					SetTransport(sip.UDPMetadata()).
					SetRemoteAddr(peerAddr)
			},
		},
		{
			name: "resolve target URI",
			prepareReq: func(t *testing.T, lisAddr, peerAddr netip.AddrPort) *sip.RequestEnvelope {
				t.Helper()
				ruri := &sip.URI{User: sip.UserWithName("bob"), Addr: sip.AddrFromHostPort("127.0.0.1", peerAddr.Port())}
				furi := &sip.URI{User: sip.UserWithName("alice"), Addr: sip.AddrFromHost("example.com")}
				turi := &sip.URI{User: sip.UserWithName("bob"), Addr: sip.AddrFromHost("example.com")}
				req, err := sip.NewRequest(
					sip.RequestMethodInvite,
					ruri,
					furi,
					turi,
					sip.RequestOptions{Transport: "UDP", Branch: sip.GenerateBranch(0), LocalTag: "from-tag", CallID: "call-id"},
				)
				if err != nil {
					t.Fatalf("sip.NewRequest() error = %v, want nil", err)
				}
				req.Headers.Set(header.Via{{
					Proto:     sip.ProtoVer20(),
					Transport: "UDP",
					Addr:      sip.AddrFromHostPort("127.0.0.1", lisAddr.Port()),
					Params:    make(sip.Values).Set("branch", sip.GenerateBranch(0)),
				}})
				req.Headers.Set(header.ContentLength(0))
				return sip.NewRequestEnvelope(req)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			peer, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
			}
			t.Cleanup(func() { peer.Close() })

			peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())
			outReq := tt.prepareReq(t, lisAddr, peerAddr)

			response, err := outReq.NewResponse(sip.ResponseStatusOK, sip.ResponseOptions{LocalTag: "to-tag"})
			if err != nil {
				t.Fatalf("outReq.NewResponse() error = %v, want nil", err)
			}

			recvCh := make(chan []byte, 1)
			go func() {
				buf := make([]byte, 65535)
				peer.SetReadDeadline(time.Now().Add(2 * time.Second))

				n, _, err := peer.ReadFrom(buf)
				if err != nil {
					return
				}
				if _, err := peer.WriteTo([]byte(response.Render()), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
					return
				}
				recvCh <- buf[:n]
			}()

			ctx, cancel := context.WithTimeout(t.Context(), asyncEventTimeout)
			defer cancel()

			_, err = elm.SendRequestStateful(ctx, outReq)
			if err != nil {
				t.Fatalf("elm.SendRequestStateful() error = %v, want nil", err)
			}

			select {
			case data := <-recvCh:
				msg, err := sip.DefaultParser().ParsePacket(data)
				if err != nil {
					t.Fatalf("sip.DefaultParser().ParsePacket() error = %v, want nil", err)
				}
				if msg == nil {
					t.Fatal("parsed message = nil, want non-nil")
				}
				reqMsg, ok := msg.(*sip.Request)
				if !ok {
					t.Fatalf("message type = %T, want *sip.Request", msg)
				}
				if reqMsg.Method != sip.RequestMethodInvite {
					t.Errorf("request method = %v, want %v", reqMsg.Method, sip.RequestMethodInvite)
				}
			case <-time.After(asyncEventTimeout):
				t.Fatal("request not received by peer")
			}
		})
	}
}

// mockRemoteServerLocator is a test double for RemoteServerLocator.
type mockRemoteServerLocator struct {
	addrs []sip.ResolvedAddr
}

func (m *mockRemoteServerLocator) LookupRequestAddrs(
	ctx context.Context,
	uri *sip.URI,
	opts ...sip.LookupMessageAddrsOptions,
) iter.Seq[sip.ResolvedAddr] {
	return func(yield func(sip.ResolvedAddr) bool) {
		for _, addr := range m.addrs {
			if !yield(addr) {
				return
			}
		}
	}
}

func TestElement_ProduceRequestAttempts(t *testing.T) {
	t.Parallel()

	addrs := []sip.ResolvedAddr{
		{Transport: sip.UDPMetadata().Proto, Addr: netip.MustParseAddrPort("192.168.1.1:5060"), FromDNS: false},
		{Transport: sip.UDPMetadata().Proto, Addr: netip.MustParseAddrPort("192.168.1.2:5060"), FromDNS: true},
	}

	elm, err := sip.NewElement(sip.ElementOptions{
		ServerLocator: &mockRemoteServerLocator{addrs: addrs},
	})
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	defer elm.Close(t.Context())

	req := newElementTestRequest(t, netip.MustParseAddrPort("127.0.0.1:0"))
	outReq := sip.NewRequestEnvelope(req)

	attempts, err := elm.ProduceRequestAttempts(t.Context(), outReq)
	if err != nil {
		t.Fatalf("elm.ProduceRequestAttempts() error = %v, want nil", err)
	}

	var got []*sip.RequestAttempt
	for attempt := range attempts {
		got = append(got, attempt)
	}

	if len(got) != len(addrs) {
		t.Fatalf("elm.ProduceRequestAttempts() produced %d attempts, want %d", len(got), len(addrs))
	}

	for i, attempt := range got {
		if attempt == nil {
			t.Fatalf("attempt[%d] = nil, want non-nil", i)
		}
		if got := attempt.Addr; got != addrs[i] {
			t.Errorf("attempt[%d].Addr() = %v, want %v", i, got, addrs[i])
		}
		if attempt.IsRFC2543Fallback() {
			t.Errorf("attempt[%d].IsRFC2543Fallback() = true, want false", i)
		}
		if attempt.Request == nil {
			t.Errorf("attempt[%d].Request() = nil, want non-nil", i)
		}
	}
}

func TestElement_ProduceRequestAttempts_Empty(t *testing.T) {
	t.Parallel()

	elm, err := sip.NewElement(sip.ElementOptions{
		ServerLocator: &mockRemoteServerLocator{},
	})
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	defer elm.Close(t.Context())

	req := newElementTestRequest(t, netip.MustParseAddrPort("127.0.0.1:0"))

	attempts, err := elm.ProduceRequestAttempts(t.Context(), sip.NewRequestEnvelope(req))
	if err != nil {
		t.Fatalf("elm.ProduceRequestAttempts() error = %v, want nil", err)
	}

	var got []*sip.RequestAttempt
	for attempt := range attempts {
		got = append(got, attempt)
	}

	if len(got) != 0 {
		t.Errorf("elm.ProduceRequestAttempts() produced %d attempts, want 0", len(got))
	}
}

func TestElement_ProduceRequestAttempts_Fallback(t *testing.T) {
	t.Parallel()

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}
	defer peer.Close()

	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())
	udpAddr := sip.ResolvedAddr{
		Transport: sip.UDPMetadata().Proto,
		Addr:      peerAddr,
		FromDNS:   true,
	}

	elm, err := sip.NewElement(sip.ElementOptions{
		ServerLocator: &mockRemoteServerLocator{addrs: []sip.ResolvedAddr{udpAddr}},
	})
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	t.Cleanup(func() { elm.Close(t.Context()) })

	// The element needs a UDP transport so that the primary attempt can resolve the transport.
	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{PublicAddr: sip.AddrFromHostPort("127.0.0.1", 5060)})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	// Build a large request body to trigger ErrMessageTooLarge on UDP.
	req, err := sip.NewRequest(
		sip.RequestMethodInvite,
		&sip.URI{User: sip.UserWithName("bob"), Addr: sip.AddrFromHost("example.com")},
		&sip.URI{User: sip.UserWithName("alice"), Addr: sip.AddrFromHost("example.com")},
		&sip.URI{User: sip.UserWithName("bob"), Addr: sip.AddrFromHost("example.com")},
		sip.RequestOptions{
			Transport: "UDP",
			Branch:    sip.GenerateBranch(0),
			LocalTag:  "from-tag",
			CallID:    "call-id",
			Body:      bytes.Repeat([]byte("x"), 2000),
		},
	)
	if err != nil {
		t.Fatalf("sip.NewRequest() error = %v, want nil", err)
	}
	req.Headers.Set(header.Via{{
		Proto:     sip.ProtoVer20(),
		Transport: "UDP",
		Addr:      sip.AddrFromHostPort("127.0.0.1", 5060),
		Params:    make(sip.Values).Set("branch", sip.GenerateBranch(0)),
	}})
	req.Headers.Set(header.ContentLength(len(req.Body)))

	attempts, err := elm.ProduceRequestAttempts(t.Context(), sip.NewRequestEnvelope(req))
	if err != nil {
		t.Fatalf("elm.ProduceRequestAttempts() error = %v, want nil", err)
	}

	var (
		primary         *sip.RequestAttempt
		fallbackAttempt *sip.RequestAttempt
	)
	for attempt := range attempts {
		if fallbackAttempt != nil {
			t.Fatal("elm.ProduceRequestAttempts() yielded more than one fallback attempt")
		}
		if primary == nil {
			// First (and only) primary attempt should fail because the message is too large.
			if err := attempt.DoStateless(t.Context()); !errors.Is(err, sip.ErrMessageTooLarge) {
				t.Fatalf("primary attempt.DoStateless() error = %v, want wraps %v", err, sip.ErrMessageTooLarge)
			}
			primary = attempt
			continue
		}
		if !attempt.IsRFC2543Fallback() {
			t.Fatalf("fallback attempt.IsRFC2543Fallback() = false, want true")
		}
		if got := attempt.Addr; got != udpAddr {
			t.Errorf("fallback attempt.Addr() = %v, want %v", got, udpAddr)
		}
		fallbackAttempt = attempt
	}

	if primary == nil {
		t.Fatal("elm.ProduceRequestAttempts() did not yield a primary attempt")
	}
	if fallbackAttempt == nil {
		t.Fatal("elm.ProduceRequestAttempts() did not yield a fallback attempt")
	}

	recvCh := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 65535)
		peer.SetReadDeadline(time.Now().Add(2 * time.Second))

		n, _, err := peer.ReadFrom(buf)
		if err != nil {
			return
		}
		recvCh <- buf[:n]
	}()

	if err := fallbackAttempt.DoStateless(t.Context()); err != nil {
		t.Fatalf("fallback attempt.DoStateless() error = %v, want nil", err)
	}

	select {
	case data := <-recvCh:
		msg, err := sip.DefaultParser().ParsePacket(data)
		if err != nil {
			t.Fatalf("sip.DefaultParser().ParsePacket() error = %v, want nil", err)
		}
		if msg == nil {
			t.Fatal("parsed message = nil, want non-nil")
		}
		reqMsg, ok := msg.(*sip.Request)
		if !ok {
			t.Fatalf("message type = %T, want *sip.Request", msg)
		}
		if reqMsg.Method != sip.RequestMethodInvite {
			t.Errorf("request method = %v, want %v", reqMsg.Method, sip.RequestMethodInvite)
		}
	case <-time.After(asyncEventTimeout):
		t.Fatal("fallback request not received by peer")
	}
}

func TestRequestAttempt_DoStateless(t *testing.T) {
	t.Parallel()

	// Create the transport's listener and a separate peer to receive packets.
	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(listener) error = %v, want nil", err)
	}
	defer lis.Close()

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}
	defer peer.Close()

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())
	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	// Create transport bound to the listener.
	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{PublicAddr: sip.AddrFromHostPort("127.0.0.1", lisAddr.Port())})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	elm, err := sip.NewElement(sip.ElementOptions{
		ServerLocator: &mockRemoteServerLocator{
			addrs: []sip.ResolvedAddr{{
				Transport: sip.UDPMetadata().Proto,
				Addr:      peerAddr,
				FromDNS:   false,
			}},
		},
	})
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	t.Cleanup(func() { elm.Close(t.Context()) })

	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- tp.ServeListener(ctx, lis) }()

	t.Cleanup(func() {
		cancel()
		lis.Close()
		<-done
	})

	req := newElementTestRequest(t, lisAddr)

	recvCh := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 65535)
		peer.SetReadDeadline(time.Now().Add(2 * time.Second))

		n, _, err := peer.ReadFrom(buf)
		if err != nil {
			return
		}
		recvCh <- buf[:n]
	}()

	attempts, err := elm.ProduceRequestAttempts(ctx, sip.NewRequestEnvelope(req))
	if err != nil {
		t.Fatalf("elm.ProduceRequestAttempts() error = %v, want nil", err)
	}

	var attempt *sip.RequestAttempt
	for a := range attempts {
		attempt = a
		break
	}
	if attempt == nil {
		t.Fatal("elm.ProduceRequestAttempts() produced no attempts")
	}

	if err := attempt.DoStateless(ctx); err != nil {
		t.Fatalf("attempt.DoStateless() error = %v, want nil", err)
	}

	select {
	case data := <-recvCh:
		msg, err := sip.DefaultParser().ParsePacket(data)
		if err != nil {
			t.Fatalf("sip.DefaultParser().ParsePacket() error = %v, want nil", err)
		}
		if msg == nil {
			t.Fatal("parsed message = nil, want non-nil")
		}
		if reqMsg, ok := msg.(*sip.Request); ok {
			if reqMsg.Method != sip.RequestMethodInvite {
				t.Errorf("request method = %v, want %v", reqMsg.Method, sip.RequestMethodInvite)
			}
		} else {
			t.Errorf("message type = %T, want *sip.Request", msg)
		}
	case <-time.After(asyncEventTimeout):
		t.Fatal("request not received by peer")
	}
}

func TestRequestAttempt_DoStateful(t *testing.T) {
	t.Parallel()

	// Create the transport's listener and a separate peer to receive packets.
	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(listener) error = %v, want nil", err)
	}
	defer lis.Close()

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}
	defer peer.Close()

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())
	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	// Create transport bound to the listener.
	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{PublicAddr: sip.AddrFromHostPort("127.0.0.1", lisAddr.Port())})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	elm, err := sip.NewElement(sip.ElementOptions{
		ServerLocator: &mockRemoteServerLocator{
			addrs: []sip.ResolvedAddr{{
				Transport: sip.UDPMetadata().Proto,
				Addr:      peerAddr,
				FromDNS:   false,
			}},
		},
	})
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	t.Cleanup(func() { elm.Close(t.Context()) })

	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- tp.ServeListener(ctx, lis) }()

	t.Cleanup(func() {
		cancel()
		lis.Close()
		<-done
	})

	req := newElementTestRequest(t, lisAddr)

	recvCh := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 65535)
		peer.SetReadDeadline(time.Now().Add(2 * time.Second))

		n, _, err := peer.ReadFrom(buf)
		if err != nil {
			return
		}
		recvCh <- buf[:n]
	}()

	attempts, err := elm.ProduceRequestAttempts(ctx, sip.NewRequestEnvelope(req))
	if err != nil {
		t.Fatalf("elm.ProduceRequestAttempts() error = %v, want nil", err)
	}

	var attempt *sip.RequestAttempt
	for a := range attempts {
		attempt = a
		break
	}
	if attempt == nil {
		t.Fatal("elm.ProduceRequestAttempts() produced no attempts")
	}

	_, err = attempt.DoStateful(ctx)
	if err != nil {
		t.Fatalf("attempt.DoStateful() error = %v, want nil", err)
	}

	select {
	case data := <-recvCh:
		msg, err := sip.DefaultParser().ParsePacket(data)
		if err != nil {
			t.Fatalf("sip.DefaultParser().ParsePacket() error = %v, want nil", err)
		}
		if msg == nil {
			t.Fatal("parsed message = nil, want non-nil")
		}
		if reqMsg, ok := msg.(*sip.Request); ok {
			if reqMsg.Method != sip.RequestMethodInvite {
				t.Errorf("request method = %v, want %v", reqMsg.Method, sip.RequestMethodInvite)
			}
		} else {
			t.Errorf("message type = %T, want *sip.Request", msg)
		}
	case <-time.After(asyncEventTimeout):
		t.Fatal("request not received by listener")
	}
}
