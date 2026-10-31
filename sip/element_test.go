package sip_test

import (
	"bytes"
	"context"
	"iter"
	"log/slog"
	"net"
	"net/netip"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/internal/testutil/dnstest"
	"github.com/ghettovoice/gosip/pkg/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/transport"
	"github.com/ghettovoice/gosip/uri"
)

const asyncEventTimeout = 5 * time.Second

func newElementTestRequest(tb testing.TB, laddr netip.AddrPort) *sip.Request {
	tb.Helper()

	req, err := sip.NewRequest(
		sip.RequestMethodInvite,
		&sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("example.com")},
		&sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("example.com")},
		&sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("example.com")},
		sip.RequestOptions{Transport: "UDP", Branch: sip.GenerateBranch(0), LocalTag: "from-tag", CallID: "call-id"},
	)
	if err != nil {
		tb.Fatalf("sip.NewRequest() error = %v, want nil", err)
	}

	req.Headers.Set(header.Via{{
		Proto:     sip.ProtoVer20(),
		Transport: "UDP",
		Addr:      sip.MakeHostPortAddr(laddr.Addr().String(), laddr.Port()),
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
	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{PublicAddr: sip.MakeHostPortAddr("127.0.0.1", lisAddr.Port())})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	elm := &sip.Element{}
	t.Cleanup(func() { elm.Close() })

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

	elm.UseMiddleware(sip.StdMessageInterceptor{
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
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
			t.Errorf("tp.ServeListener() error = %v, want context canceled or closed", err)
		}
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

	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{PublicAddr: sip.MakeHostPortAddr("127.0.0.1", lisAddr.Port())})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	elm := &sip.Element{}
	t.Cleanup(func() { elm.Close() })

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
				ruri := &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostPortAddr("127.0.0.1", peerAddr.Port())}
				furi := &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("example.com")}
				turi := &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("example.com")}
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
					Addr:      sip.MakeHostPortAddr("127.0.0.1", lisAddr.Port()),
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
				ruri := &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostPortAddr("127.0.0.1", peerAddr.Port())}
				furi := &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("example.com")}
				turi := &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("example.com")}
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
					Addr:      sip.MakeHostPortAddr("127.0.0.1", lisAddr.Port()),
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

	elm := &sip.Element{}
	defer elm.Close()

	// Create outbound request with URI that cannot be resolved to a transport.
	ruri := &sip.URI{
		User: sip.MakeUserInfo("bob"),
		Addr: sip.MakeHostPortAddr("127.0.0.1", 1),
	}
	furi := &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("example.com")}
	turi := &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("example.com")}

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
		Addr:      sip.MakeHostPortAddr("127.0.0.1", 5060),
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

	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{PublicAddr: sip.MakeHostPortAddr("127.0.0.1", lisAddr.Port())})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	elm := &sip.Element{}
	t.Cleanup(func() { elm.Close() })

	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- tp.ServeListener(ctx, lis) }()
	t.Cleanup(func() {
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
			t.Errorf("tp.ServeListener() error = %v, want context canceled or closed", err)
		}
	})

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
				ruri := &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostPortAddr("127.0.0.1", peerAddr.Port())}
				furi := &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("example.com")}
				turi := &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("example.com")}
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
					Addr:      sip.MakeHostPortAddr("127.0.0.1", lisAddr.Port()),
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
	reqURI *sip.URI,
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

	elm := &sip.Element{ServerLocator: &mockRemoteServerLocator{addrs: addrs}}
	defer elm.Close()

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

	elm := &sip.Element{ServerLocator: &mockRemoteServerLocator{}}
	defer elm.Close()

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

	elm := &sip.Element{ServerLocator: &mockRemoteServerLocator{addrs: []sip.ResolvedAddr{udpAddr}}}
	t.Cleanup(func() { elm.Close() })

	// The element needs a UDP transport so that the primary attempt can resolve the transport.
	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{PublicAddr: sip.MakeHostPortAddr("127.0.0.1", 5060)})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	// Build a large request body to trigger ErrMessageTooLarge on UDP.
	req, err := sip.NewRequest(
		sip.RequestMethodInvite,
		&sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("example.com")},
		&sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("example.com")},
		&sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("example.com")},
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
		Addr:      sip.MakeHostPortAddr("127.0.0.1", 5060),
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
	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{PublicAddr: sip.MakeHostPortAddr("127.0.0.1", lisAddr.Port())})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	elm := &sip.Element{
		ServerLocator: &mockRemoteServerLocator{
			addrs: []sip.ResolvedAddr{{
				Transport: sip.UDPMetadata().Proto,
				Addr:      peerAddr,
				FromDNS:   false,
			}},
		},
	}
	t.Cleanup(func() { elm.Close() })

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
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
			t.Errorf("tp.ServeListener() error = %v, want context canceled or closed", err)
		}
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
	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{PublicAddr: sip.MakeHostPortAddr("127.0.0.1", lisAddr.Port())})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	elm := &sip.Element{
		ServerLocator: &mockRemoteServerLocator{
			addrs: []sip.ResolvedAddr{{
				Transport: sip.UDPMetadata().Proto,
				Addr:      peerAddr,
				FromDNS:   false,
			}},
		},
	}
	t.Cleanup(func() { elm.Close() })

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
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
			t.Errorf("tp.ServeListener() error = %v, want context canceled or closed", err)
		}
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

func TestElement_SendResponseStateful_DormantExisting(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	tp := newProxyStubTransport()
	elm := &sip.Element{}
	t.Cleanup(func() { elm.Close() })

	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	req := newInInviteReq(t, "UDP", sip.MagicCookie+".dormant-send-res", laddr, raddr)

	// NewServerTransaction registers the transaction without starting it,
	// leaving a dormant transaction that owns the key.
	tx, err := elm.NewServerTransaction(ctx, req, tp)
	if err != nil {
		t.Fatalf("elm.NewServerTransaction() error = %v, want nil", err)
	}

	res := newInRes(t, req, sip.ResponseStatusRinging)
	if _, err := elm.SendResponseStateful(ctx, req, res); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
		t.Fatalf("elm.SendResponseStateful() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
	}

	if tx.State() == sip.TransactionStateTerminated {
		t.Fatal("dormant transaction terminated by elm.SendResponseStateful, want unchanged")
	}
	stored, ok := elm.LoadServerTransaction(tx.Key())
	if !ok || stored != tx {
		t.Fatalf("elm.LoadServerTransaction() = %p, %v, want %p, true", stored, ok, tx)
	}
}

type elementSendResponseContextTransaction struct {
	*customServerTransaction
	sendCtxs chan context.Context
	termCtxs chan context.Context
	sendErr  error
}

func (tx *elementSendResponseContextTransaction) SendResponse(
	ctx context.Context,
	_ *sip.ResponseEnvelope,
	_ ...sip.SendResponseOptions,
) error {
	tx.sendCtxs <- ctx
	return errors.Wrap(tx.sendErr)
}

func (tx *elementSendResponseContextTransaction) Terminate(ctx context.Context, cause error) error {
	tx.termCtxs <- ctx
	return errors.Wrap(tx.customServerTransaction.Terminate(ctx, errors.Wrap(cause)))
}

func TestElement_SendResponseStateful_Context(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		branch        string
		sendErr       error
		wantTerminate bool
	}{
		{name: "success", branch: "success"},
		{name: "send failure", branch: "failure", sendErr: errors.ErrorWrap("send failed"), wantTerminate: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			elm := &sip.Element{}
			t.Cleanup(func() { _ = elm.Close() })

			transport := newProxyStubTransport()
			customTx := newCustomServerTx(
				t,
				sip.MagicCookie+".stateful-context-"+tt.branch,
				sip.TransactionStateProceeding,
			)
			customTx.tp = transport
			tx := &elementSendResponseContextTransaction{
				customServerTransaction: customTx,
				sendCtxs:                make(chan context.Context, 1),
				termCtxs:                make(chan context.Context, 2),
				sendErr:                 tt.sendErr,
			}
			if err := elm.RegisterServerTransaction(t.Context(), tx); err != nil {
				t.Fatalf("elm.RegisterServerTransaction() error = %v, want nil", err)
			}

			parentTransport := newProxyStubTransport()
			outerTx := &stubClientTransaction{}
			otherElement := &sip.Element{}
			parentCtx := sip.ContextWithElement(context.WithValue(t.Context(), contextTestKey{}, "caller"), otherElement)
			parentCtx = sip.ContextWithTransport(parentCtx, parentTransport)
			parentCtx = sip.ContextWithTransaction(parentCtx, outerTx)
			res := newInRes(t, customTx.req, sip.ResponseStatusRinging)

			gotTx, err := elm.SendResponseStateful(parentCtx, customTx.req, res)
			if tt.sendErr == nil {
				if err != nil {
					t.Fatalf("elm.SendResponseStateful() error = %v, want nil", err)
				}
				if gotTx != tx {
					t.Fatalf("elm.SendResponseStateful() transaction = %v, want %v", gotTx, tx)
				}
			} else if !errors.Is(err, tt.sendErr) {
				t.Fatalf("elm.SendResponseStateful() error = %v, want %v", err, tt.sendErr)
			}

			sendCtx := testutil.WaitResult(t, tx.sendCtxs, "stateful response context")
			assertPropagationContext(t, sendCtx, tx, transport, "caller")
			assertElementOwner(t, sendCtx, elm)
			if tt.wantTerminate {
				termCtx := testutil.WaitResult(t, tx.termCtxs, "stateful termination context")
				assertPropagationContext(t, termCtx, tx, transport, "caller")
				assertElementOwner(t, termCtx, elm)
			}
			assertElementOwner(t, parentCtx, otherElement)
			if got, ok := sip.TransactionFromContext(parentCtx); !ok || got != outerTx {
				t.Fatalf("parent transaction = (%v, %t), want outer transaction %v", got, ok, outerTx)
			}
			if got, ok := sip.TransportFromContext(parentCtx); !ok || got != parentTransport {
				t.Fatalf("parent transport = (%v, %t), want %v", got, ok, parentTransport)
			}
			if got := parentCtx.Value(contextTestKey{}); got != "caller" {
				t.Fatalf("parent context value = %v, want caller", got)
			}
		})
	}
}

type countingClientTransport struct {
	sip.ClientTransport
	sends atomic.Int32
}

func (tp *countingClientTransport) SendRequest(
	ctx context.Context,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	tp.sends.Add(1)
	return tp.ClientTransport.SendRequest(ctx, req, opts...)
}

func TestElement_SendRequestStateful_TerminateOnActive(t *testing.T) {
	t.Parallel()

	lisAddr := netip.MustParseAddrPort("127.0.0.1:0")
	peerAddr := netip.MustParseAddrPort("127.0.0.1:9")

	var counting *atomic.Int32
	elm := &sip.Element{
		ClientTransactionFactory: sip.ClientTransactionFactoryFunc(
			func(req *sip.RequestEnvelope, tp sip.ClientTransport, opts ...sip.ClientTransactionOptions) (sip.ClientTransaction, error) {
				ctp := &countingClientTransport{ClientTransport: tp}
				tx, err := sip.NewClientTransaction(req, ctp, opts...)
				if err != nil {
					return nil, err
				}
				tx.BindStartHandler(sip.TransactionStartHandlerFunc(
					func(hdlrCtx context.Context) {
						_ = tx.Terminate(hdlrCtx, errors.ErrorWrap("terminated on start"))
					},
				))
				counting = &ctp.sends
				return tx, nil
			},
		),
	}
	t.Cleanup(func() { elm.Close() })

	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata(), transport.TransportOptions{PublicAddr: sip.MakeHostPortAddr("127.0.0.1", lisAddr.Port())})
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	req := newElementTestRequest(t, lisAddr)
	env := sip.NewRequestEnvelope(req).
		SetTransport(sip.UDPMetadata()).
		SetRemoteAddr(peerAddr)

	tx, err := elm.SendRequestStateful(t.Context(), env)
	if err == nil {
		t.Fatalf("elm.SendRequestStateful() error = nil, tx = %v, want error", tx)
	}
	if !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
		t.Fatalf("elm.SendRequestStateful() error = %v, want wrap of %v", err, sip.ErrTransactionActionNotAllowed)
	}
	if counting == nil {
		t.Fatal("client transaction factory was not called")
	}
	if got := counting.Load(); got != 0 {
		t.Fatalf("SendRequest called %d times after terminate-on-active, want 0", got)
	}
}

// stubURIConverter is a test double for URIConverter that counts calls.
type stubURIConverter struct {
	uri   *sip.URI
	err   error
	calls atomic.Int32
}

func (c *stubURIConverter) ConvertURI(_ context.Context, _ sip.AnyURI) (*sip.URI, error) {
	c.calls.Add(1)
	return c.uri, c.err
}

func TestElement_ZeroValueLazyInit(t *testing.T) {
	t.Parallel()

	elm := &sip.Element{}
	t.Cleanup(func() { elm.Close() })

	id := elm.ID()
	if id == uuid.Nil() {
		t.Fatal("elm.ID() = nil UUID, want generated non-zero ID")
	}
	if got := elm.ID(); got != id {
		t.Fatalf("elm.ID() = %v, want stable ID %v", got, id)
	}
	if other := (&sip.Element{}).ID(); other == id {
		t.Fatalf("another element ID() = %v, want distinct from %v", other, id)
	}

	if got := elm.Logger(); got == nil {
		t.Fatal("elm.Logger() = nil, want non-nil")
	}
	if logger, logger2 := elm.Logger(), elm.Logger(); logger != logger2 {
		t.Fatal("elm.Logger() returned different pointers, want same logger")
	}

	if diff := cmp.Diff(sip.ElementCapabilities{}, elm.Capabilities()); diff != "" {
		t.Errorf("elm.Capabilities() mismatch (-want +got):\n%s", diff)
	}

	if got := slices.Collect(elm.AllTransports()); len(got) != 0 {
		t.Errorf("elm.AllTransports() = %v, want no transports", got)
	}
	if _, ok := elm.TransportByProto(sip.UDPMetadata().Proto); ok {
		t.Error("elm.TransportByProto(UDP) = true, want no transports")
	}
	if got := slices.Collect(elm.AllTransportMetadata()); len(got) != 0 {
		t.Errorf("elm.AllTransportMetadata() = %v, want no transport metadata", got)
	}
	if _, ok := elm.TransportMetadataByProto(sip.UDPMetadata().Proto); ok {
		t.Error("elm.TransportMetadataByProto(UDP) = true, want no transport metadata")
	}
	if got := slices.Collect(elm.AllClientTransactions()); len(got) != 0 {
		t.Errorf("elm.AllClientTransactions() = %v, want no transactions", got)
	}
	if got := slices.Collect(elm.AllServerTransactions()); len(got) != 0 {
		t.Errorf("elm.AllServerTransactions() = %v, want no transactions", got)
	}
	if _, ok := elm.LoadClientTransaction(sip.ClientTransactionKey{}); ok {
		t.Error("elm.LoadClientTransaction(zero key) = true, want no transaction")
	}
	if _, ok := elm.LoadServerTransaction(sip.ServerTransactionKey{}); ok {
		t.Error("elm.LoadServerTransaction(zero key) = true, want no transaction")
	}

	// Resolved defaults must not be written back to the public configuration.
	if elm.InstanceID != uuid.Nil() {
		t.Errorf("elm.InstanceID = %v after init, want unchanged nil UUID", elm.InstanceID)
	}
	if elm.ServerLocator != nil {
		t.Errorf("elm.ServerLocator = %v after init, want unchanged nil", elm.ServerLocator)
	}
	if elm.DNSResolver != nil {
		t.Errorf("elm.DNSResolver = %v after init, want unchanged nil", elm.DNSResolver)
	}
	if elm.URIConverter != nil {
		t.Errorf("elm.URIConverter = %v after init, want unchanged nil", elm.URIConverter)
	}
	if elm.BaseLogger != nil {
		t.Errorf("elm.BaseLogger = %v after init, want unchanged nil", elm.BaseLogger)
	}
}

func TestElement_ZeroValueClose(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	req := newInInviteReq(t, "UDP", "", laddr, raddr)
	res := newInRes(t, req, sip.ResponseStatusRinging)
	tp := newProxyStubTransport()

	var elm sip.Element

	// Repeated Close on a standalone zero-value element must succeed and must not reopen it.
	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}
	if err := elm.Close(); err != nil {
		t.Fatalf("second elm.Close() error = %v, want nil", err)
	}

	// Initialized state stays valid after close.
	if elm.ID() == uuid.Nil() {
		t.Fatal("elm.ID() = nil UUID after Close, want generated ID")
	}

	tests := []struct {
		name string
		call func() error
	}{
		{"TrackTransport", func() error { return elm.TrackTransport(tp) }},
		{"UntrackTransport", func() error { return elm.UntrackTransport(tp) }},
		{"ListenManaged", func() error { return elm.ListenManaged(ctx, "UDP", "127.0.0.1:0") }},
		{"Listen", func() error { _, err := elm.Listen(ctx, "UDP", "127.0.0.1:0"); return err }},
		{"SendRequest", func() error { return elm.SendRequest(ctx, req) }},
		{"SendRequestStateless", func() error { return elm.SendRequestStateless(ctx, req) }},
		{"SendResponse", func() error { return elm.SendResponse(ctx, res) }},
		{"Respond", func() error { return elm.Respond(ctx, req, sip.ResponseStatusOK) }},
		{"NewClientTransaction", func() error { _, err := elm.NewClientTransaction(ctx, req, tp); return err }},
		{"NewServerTransaction", func() error { _, err := elm.NewServerTransaction(ctx, req, tp); return err }},
		{"SendRequestStateful", func() error { _, err := elm.SendRequestStateful(ctx, req); return err }},
		{"SendResponseStateful", func() error { _, err := elm.SendResponseStateful(ctx, req, res); return err }},
		{"RespondStateful", func() error { _, err := elm.RespondStateful(ctx, req, sip.ResponseStatusOK); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.call(); !errors.Is(err, sip.ErrElementClosed) {
				t.Errorf("elm.%s() error = %v, want %v", tt.name, err, sip.ErrElementClosed)
			}
		})
	}
}

func TestElement_ExplicitConfig(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	id := sip.NextElementID()

	var logBuf bytes.Buffer
	baseLogger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var clnFactoryCalls, srvFactoryCalls atomic.Int32
	elm := &sip.Element{
		InstanceID: id,
		BaseLogger: baseLogger,
		ClientTransactionFactory: sip.ClientTransactionFactoryFunc(
			func(req *sip.RequestEnvelope, tp sip.ClientTransport, opts ...sip.ClientTransactionOptions) (sip.ClientTransaction, error) {
				clnFactoryCalls.Add(1)
				return sip.NewClientTransaction(req, tp, opts...)
			},
		),
		ServerTransactionFactory: sip.ServerTransactionFactoryFunc(
			func(req *sip.RequestEnvelope, tp sip.ServerTransport, opts ...sip.ServerTransactionOptions) (sip.ServerTransaction, error) {
				srvFactoryCalls.Add(1)
				return sip.NewServerTransaction(req, tp, opts...)
			},
		),
		StaleTransactionTimeout: 123 * time.Second,
	}
	t.Cleanup(func() { elm.Close() })

	if got := elm.ID(); got != id {
		t.Fatalf("elm.ID() = %v, want configured ID %v", got, id)
	}

	// The element logger is derived from the configured base logger and
	// carries the element attributes.
	elm.Logger().Info("probe")
	if got := logBuf.String(); !strings.Contains(got, `"id":"`+id.String()+`"`) {
		t.Fatalf("element log output = %q, want element attribute with id %q", got, id)
	}

	if got := log.LoggerFromValues(ctx, elm); got != elm.Logger() {
		t.Fatal("log.LoggerFromValues(ctx, elm) != elm.Logger(), want element logger")
	}

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	tp := newProxyStubTransport()
	req := newInInviteReq(t, "UDP", "", laddr, raddr)

	if _, err := elm.NewClientTransaction(ctx, req, tp); err != nil {
		t.Fatalf("elm.NewClientTransaction() error = %v, want nil", err)
	}
	if got := clnFactoryCalls.Load(); got != 1 {
		t.Fatalf("client transaction factory calls = %d, want 1", got)
	}

	if _, err := elm.NewServerTransaction(ctx, req, tp); err != nil {
		t.Fatalf("elm.NewServerTransaction() error = %v, want nil", err)
	}
	if got := srvFactoryCalls.Load(); got != 1 {
		t.Fatalf("server transaction factory calls = %d, want 1", got)
	}
}

func TestElement_LazyInitDefaultLocatorUsesDNSResolver(t *testing.T) {
	t.Parallel()

	var ipLookups atomic.Int32
	resolver := dnstest.StubResolver{
		LookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
			ipLookups.Add(1)
			if host == "example.com" {
				return []net.IP{net.ParseIP("192.0.2.1").To4()}, nil
			}
			return nil, nil
		},
	}

	elm := &sip.Element{DNSResolver: resolver}
	t.Cleanup(func() { elm.Close() })

	tp := newProxyStubTransport()
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	req := newElementTestRequest(t, netip.MustParseAddrPort("127.0.0.1:5060"))
	attempts, err := elm.ProduceRequestAttempts(t.Context(), sip.NewRequestEnvelope(req))
	if err != nil {
		t.Fatalf("elm.ProduceRequestAttempts() error = %v, want nil", err)
	}

	var got []sip.ResolvedAddr
	for attempt := range attempts {
		got = append(got, attempt.Addr)
	}

	want := []sip.ResolvedAddr{{
		Transport: sip.UDPMetadata().Proto,
		Addr:      netip.MustParseAddrPort("192.0.2.1:5060"),
	}}
	if !slices.Equal(got, want) {
		t.Fatalf("resolved attempts = %v, want %v", got, want)
	}
	if got := ipLookups.Load(); got == 0 {
		t.Fatal("DNS resolver LookupIP was not called, want lookup through configured resolver")
	}
}

func TestElement_LazyInitServerLocatorPrecedence(t *testing.T) {
	t.Parallel()

	var resolverCalls atomic.Int32
	resolver := dnstest.StubResolver{
		LookupIPFunc: func(context.Context, string, string) ([]net.IP, error) {
			resolverCalls.Add(1)
			return []net.IP{net.ParseIP("192.0.2.1")}, nil
		},
	}
	lctr := &mockRemoteServerLocator{addrs: []sip.ResolvedAddr{{
		Transport: sip.UDPMetadata().Proto,
		Addr:      netip.MustParseAddrPort("198.51.100.1:5060"),
	}}}

	elm := &sip.Element{ServerLocator: lctr, DNSResolver: resolver}
	t.Cleanup(func() { elm.Close() })

	req := newElementTestRequest(t, netip.MustParseAddrPort("127.0.0.1:5060"))
	attempts, err := elm.ProduceRequestAttempts(t.Context(), sip.NewRequestEnvelope(req))
	if err != nil {
		t.Fatalf("elm.ProduceRequestAttempts() error = %v, want nil", err)
	}

	var got []sip.ResolvedAddr
	for attempt := range attempts {
		got = append(got, attempt.Addr)
	}

	if !slices.Equal(got, lctr.addrs) {
		t.Fatalf("resolved attempts = %v, want %v", got, lctr.addrs)
	}
	if got := resolverCalls.Load(); got != 0 {
		t.Fatalf("DNS resolver called %d times, want 0 when ServerLocator is configured", got)
	}
}

func TestElement_LazyInitURIConverter(t *testing.T) {
	t.Parallel()

	target := &sip.URI{Addr: sip.MakeHostPortAddr("converted.example.com", 5090)}
	conv := &stubURIConverter{uri: target}

	elm := &sip.Element{URIConverter: conv}
	t.Cleanup(func() { elm.Close() })

	req, err := sip.NewRequest(
		sip.RequestMethodInvite,
		&uri.Tel{Number: "+15550100"},
		&sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("example.com")},
		&sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("example.com")},
		sip.RequestOptions{Transport: "UDP", Branch: sip.GenerateBranch(0), LocalTag: "from-tag", CallID: "call-id"},
	)
	if err != nil {
		t.Fatalf("sip.NewRequest() error = %v, want nil", err)
	}

	got, err := elm.ResolveRequestTarget(t.Context(), sip.NewRequestEnvelope(req))
	if err != nil {
		t.Fatalf("elm.ResolveRequestTarget() error = %v, want nil", err)
	}
	if got != target {
		t.Fatalf("elm.ResolveRequestTarget() = %v, want converted URI %v", got, target)
	}
	if got := conv.calls.Load(); got != 1 {
		t.Fatalf("URI converter calls = %d, want 1", got)
	}
}

func TestElement_BaseCapabilitiesSnapshot(t *testing.T) {
	t.Parallel()

	methods := []sip.RequestMethod{sip.RequestMethodInvite}
	options := []header.OptionTag{"100rel"}
	mimeTypes := []header.MIMERange{{
		Type: "application", Subtype: "sdp",
		Params: make(sip.Values).Set("q", "1"),
	}}

	elm := &sip.Element{
		BaseCapabilities: sip.ElementCapabilities{
			Methods:   methods,
			Options:   options,
			MIMETypes: mimeTypes,
		},
	}
	t.Cleanup(func() { elm.Close() })

	want := sip.ElementCapabilities{
		Methods: []sip.RequestMethod{sip.RequestMethodInvite},
		Options: []header.OptionTag{"100rel"},
		MIMETypes: []header.MIMERange{{
			Type: "application", Subtype: "sdp",
			Params: make(sip.Values).Set("q", "1"),
		}},
	}
	if diff := cmp.Diff(want, elm.Capabilities()); diff != "" {
		t.Fatalf("elm.Capabilities() mismatch (-want +got):\n%s", diff)
	}

	// Mutating the input slices and nested MIME params after the first use
	// must not change the runtime capabilities snapshot.
	methods[0] = sip.RequestMethodBye
	options[0] = "timer"
	mimeTypes[0].Params.Set("q", "0.1")
	mimeTypes[0].Subtype = "x-custom"

	if diff := cmp.Diff(want, elm.Capabilities()); diff != "" {
		t.Fatalf("elm.Capabilities() after input mutation mismatch (-want +got):\n%s", diff)
	}

	unbind := elm.UseMiddleware(elemCapabilsMiddleware{
		methods: []sip.RequestMethod{sip.RequestMethodCancel},
	})

	augmented := want
	augmented.Methods = append(slices.Clone(want.Methods), sip.RequestMethodCancel)
	if diff := cmp.Diff(augmented, elm.Capabilities()); diff != "" {
		t.Fatalf("elm.Capabilities() with middleware mismatch (-want +got):\n%s", diff)
	}

	unbind()
	if diff := cmp.Diff(want, elm.Capabilities()); diff != "" {
		t.Fatalf("elm.Capabilities() after unbind mismatch (-want +got):\n%s", diff)
	}
}

func TestElement_InterceptorsBoundBeforeUse(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	elm := &sip.Element{}
	t.Cleanup(func() { elm.Close() })

	tp := newProxyStubTransport()
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	// The built-in transaction manager and element interceptors must be bound
	// exactly once when the transport is tracked.
	for name, got := range map[string]int{
		"inbound request":   tp.InboundRequestInterceptors.Len(),
		"inbound response":  tp.InboundResponseInterceptors.Len(),
		"outbound request":  tp.OutboundRequestInterceptors.Len(),
		"outbound response": tp.OutboundResponseInterceptors.Len(),
	} {
		if got != 2 {
			t.Errorf("transport %s interceptors bound = %d, want 2 (txm + element)", name, got)
		}
	}

	// Repeated element queries must not duplicate the interceptor bindings.
	_, _ = elm.TransportMetadataByProto(sip.UDPMetadata().Proto)
	_ = slices.Collect(elm.AllClientTransactions())
	_ = slices.Collect(elm.AllServerTransactions())
	if got := tp.OutboundRequestInterceptors.Len(); got != 2 {
		t.Fatalf("outbound request interceptors bound = %d after repeated queries, want 2", got)
	}

	// An outbound request passed through the bound chain is completed by the
	// element interceptor: Via, From tag and Content-Length are ensured.
	req := sip.NewRequestEnvelope(newElementTestRequest(t, laddr)).
		SetTransport(sip.UDPMetadata()).
		SetRemoteAddr(raddr)
	req.WithMessage(func(r *sip.Request) {
		r.Headers.Delete("Via", "Content-Length")
		r.Body = []byte("v=0\r\no=- 0 0 IN IP4 192.168.1.100\r\ns=-\r\n")
		if from, ok := r.Headers.From(); ok {
			from.Params.Delete("tag")
		}
	})

	var delivered atomic.Int32
	sender := sip.InterceptOutboundRequest(
		slices.Collect(tp.OutboundRequestInterceptors.All()),
		sip.RequestSenderFunc(func(context.Context, *sip.RequestEnvelope, ...sip.SendRequestOptions) error {
			delivered.Add(1)
			return nil
		}),
	)
	if err := sender.SendRequest(ctx, req); err != nil {
		t.Fatalf("outbound request pipeline error = %v, want nil", err)
	}
	if got := delivered.Load(); got != 1 {
		t.Fatalf("outbound request deliveries = %d, want 1", got)
	}
	if via, ok := req.Headers().FirstVia(); !ok || via == nil {
		t.Fatal("outbound request Via header missing, want ensured by element interceptor")
	} else if branch, ok := via.Branch(); !ok || !sip.IsRFC3261Branch(branch) {
		t.Fatalf("outbound request Via branch = %q, want RFC 3261 branch", branch)
	}
	if from, ok := req.Headers().From(); !ok || from == nil {
		t.Fatal("outbound request From header missing")
	} else if tag, ok := from.Tag(); !ok || tag == "" {
		t.Fatal("outbound request From tag missing, want ensured by element interceptor")
	}
	if _, ok := req.Headers().ContentLength(); !ok {
		t.Fatal("outbound request Content-Length header missing, want ensured by element interceptor")
	}

	// An unmatched inbound request passes the transaction manager and element
	// interceptors and is delivered once to the final receiver.
	var received atomic.Int32
	receiver := sip.InterceptInboundRequest(
		slices.Collect(tp.InboundRequestInterceptors.All()),
		sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
			received.Add(1)
			return nil
		}),
	)
	inReq := newInInviteReq(t, "UDP", "", laddr, raddr)
	if err := receiver.RecvRequest(ctx, inReq); err != nil {
		t.Fatalf("inbound request pipeline error = %v, want nil", err)
	}
	if got := received.Load(); got != 1 {
		t.Fatalf("inbound request deliveries = %d, want 1", got)
	}
}

func TestElement_InterceptOutboundResponseFirstUse(t *testing.T) {
	t.Parallel()

	id := sip.NextElementID()
	elm := &sip.Element{InstanceID: id}
	t.Cleanup(func() { elm.Close() })

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	req := newElementTestRequest(t, laddr)

	res, err := req.NewResponse(sip.ResponseStatusOK)
	if err != nil {
		t.Fatalf("req.NewResponse() error = %v, want nil", err)
	}
	// Drop the tag generated by NewResponse so the element interceptor has to
	// produce it.
	if to, ok := res.Headers.To(); ok && to != nil {
		to.Params.Delete("tag")
	}

	// The stable To tag is derived from the configured element ID.
	want := sip.GenerateStableToTag(res, id[:])

	var sent atomic.Int32
	sender := sip.ResponseSenderFunc(func(context.Context, *sip.ResponseEnvelope, ...sip.SendResponseOptions) error {
		sent.Add(1)
		return nil
	})
	if err := elm.InterceptOutboundResponse(t.Context(), sender, sip.NewResponseEnvelope(res)); err != nil {
		t.Fatalf("elm.InterceptOutboundResponse() error = %v, want nil", err)
	}
	if got := sent.Load(); got != 1 {
		t.Fatalf("outbound response deliveries = %d, want 1", got)
	}

	to, ok := res.Headers.To()
	if !ok || to == nil {
		t.Fatal("response To header missing")
	}
	tag, ok := to.Tag()
	if !ok || tag == "" {
		t.Fatal("response To tag missing, want generated by element interceptor")
	}
	if tag != want {
		t.Fatalf("response To tag = %q, want %q (stable tag from element ID)", tag, want)
	}
}

func TestElement_LogValue(t *testing.T) {
	t.Parallel()

	var nilElm *sip.Element
	if got := nilElm.LogValue(); got.Kind() != slog.KindAny || got.Any() != nil {
		t.Fatalf("nil element LogValue() = %v, want zero slog.Value", got)
	}

	id := sip.NextElementID()
	elm := &sip.Element{InstanceID: id}
	t.Cleanup(func() { elm.Close() })

	// LogValue as the first use must initialize the element without deadlock
	// and produce the element attributes.
	val := elm.LogValue()
	if val.Kind() != slog.KindGroup {
		t.Fatalf("elm.LogValue().Kind() = %v, want %v", val.Kind(), slog.KindGroup)
	}

	var gotID any
	var gotPtr bool
	for _, attr := range val.Group() {
		switch attr.Key {
		case "id":
			gotID = attr.Value.Any()
		case "ptr":
			gotPtr = true
		}
	}
	if !gotPtr {
		t.Fatal("elm.LogValue() missing \"ptr\" attribute")
	}
	if gotID != id {
		t.Fatalf("elm.LogValue() id attribute = %v, want %v", gotID, id)
	}

	// Resolving the element through a real JSON handler must produce the same
	// attributes without recursive initialization.
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.With(slog.Any("element", elm)).Info("probe")
	if got := buf.String(); !strings.Contains(got, `"element":{"ptr":`) || !strings.Contains(got, `"id":"`+id.String()+`"`) {
		t.Fatalf("log output = %q, want element attribute with id %q", got, id)
	}
}

func TestElement_ConcurrentFirstUse(t *testing.T) {
	t.Parallel()

	elm := &sip.Element{}
	t.Cleanup(func() { elm.Close() })

	const n = 8
	var (
		ids     [n]sip.ElementID
		loggers [n]*slog.Logger
		tps     [n]int
		metas   [n]int
		clnTxs  [n]int
		srvTxs  [n]int
	)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			ids[i] = elm.ID()
			loggers[i] = elm.Logger()
			elm.LogValue()
			elm.Capabilities()
			tps[i] = len(slices.Collect(elm.AllTransports()))
			metas[i] = len(slices.Collect(elm.AllTransportMetadata()))
			clnTxs[i] = len(slices.Collect(elm.AllClientTransactions()))
			srvTxs[i] = len(slices.Collect(elm.AllServerTransactions()))
		})
	}
	close(start)
	wg.Wait()

	for i := 1; i < n; i++ {
		if ids[i] != ids[0] {
			t.Errorf("goroutine %d ID() = %v, want %v", i, ids[i], ids[0])
		}
		if loggers[i] != loggers[0] {
			t.Errorf("goroutine %d Logger() = %p, want %p", i, loggers[i], loggers[0])
		}
		if tps[i] != 0 {
			t.Errorf("goroutine %d AllTransports() count = %d, want 0", i, tps[i])
		}
		if metas[i] != 0 {
			t.Errorf("goroutine %d AllTransportMetadata() count = %d, want 0", i, metas[i])
		}
		if clnTxs[i] != 0 {
			t.Errorf("goroutine %d AllClientTransactions() count = %d, want 0", i, clnTxs[i])
		}
		if srvTxs[i] != 0 {
			t.Errorf("goroutine %d AllServerTransactions() count = %d, want 0", i, srvTxs[i])
		}
	}
	if ids[0] == uuid.Nil() {
		t.Fatal("concurrent ID() = nil UUID, want generated ID")
	}
}

func TestElement_ConcurrentCloseFirstUse(t *testing.T) {
	t.Parallel()

	elm := &sip.Element{}

	const n = 8
	var (
		ids       [n]sip.ElementID
		closeErrs [n]error
	)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			if i%2 == 0 {
				ids[i] = elm.ID()
			} else {
				closeErrs[i] = elm.Close()
			}
		})
	}
	close(start)
	wg.Wait()

	wantID := elm.ID()
	for i := range n {
		if i%2 == 0 {
			if ids[i] != wantID {
				t.Errorf("goroutine %d ID() = %v, want %v", i, ids[i], wantID)
			}
		} else if closeErrs[i] != nil {
			t.Errorf("goroutine %d Close() error = %v, want nil", i, closeErrs[i])
		}
	}

	if wantID == uuid.Nil() {
		t.Fatal("ID() = nil UUID after concurrent init/close, want generated ID")
	}
	if err := elm.TrackTransport(newProxyStubTransport()); !errors.Is(err, sip.ErrElementClosed) {
		t.Fatalf("elm.TrackTransport() after close error = %v, want %v", err, sip.ErrElementClosed)
	}
}

// lcGate is a two-phase deterministic rendezvous for lifecycle tests:
// enter is signaled when a gated function starts, open releases it.
type lcGate struct {
	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once
	openOnce  sync.Once
}

func newLcGate() *lcGate {
	return &lcGate{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (g *lcGate) enter() { g.enterOnce.Do(func() { close(g.entered) }) }

func (g *lcGate) waitEnter(tb testing.TB) {
	tb.Helper()
	testutil.WaitClosed(tb, g.entered, "gated function")
}

func (g *lcGate) open() { g.openOnce.Do(func() { close(g.release) }) }

// lcEventLog records ordered lifecycle events for deterministic assertions.
type lcEventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *lcEventLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.events = append(l.events, event)
}

func (l *lcEventLog) index(event string) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Index(l.events, event)
}

// lcGatedCloser blocks its first Close call until released.
type lcGatedCloser struct {
	gate     *lcGate
	closes   atomic.Int32
	returned atomic.Bool
}

func (mw *lcGatedCloser) Close() error {
	mw.closes.Add(1)
	mw.gate.enter()
	<-mw.gate.release
	mw.returned.Store(true)
	return nil
}

// lcCapsCloser provides capabilities, runs an optional hook per Capabilities
// call and counts Close calls.
type lcCapsCloser struct {
	methods  []sip.RequestMethod
	capCalls atomic.Int32
	capFn    func(call int32)
	closes   atomic.Int32
}

func (mw *lcCapsCloser) Capabilities() sip.ElementCapabilities {
	call := mw.capCalls.Add(1)
	if mw.capFn != nil {
		mw.capFn(call)
	}
	return sip.ElementCapabilities{Methods: mw.methods}
}

func (mw *lcCapsCloser) Close() error {
	mw.closes.Add(1)
	return nil
}

// lcTxHandlerMW records client/server transaction notifications, whether they
// arrived after the middleware was closed, and the middleware Close itself.
type lcTxHandlerMW struct {
	closed  atomic.Bool
	closes  atomic.Int32
	closeFn func()

	clnCalls        atomic.Int32
	srvCalls        atomic.Int32
	callsAfterClose atomic.Int32
	clnFn           func(ctx context.Context, tx sip.ClientTransaction)
	srvFn           func(ctx context.Context, tx sip.ServerTransaction)
}

func (mw *lcTxHandlerMW) HandleClientTransaction(ctx context.Context, tx sip.ClientTransaction) {
	if mw.closed.Load() {
		mw.callsAfterClose.Add(1)
	}
	mw.clnCalls.Add(1)
	if mw.clnFn != nil {
		mw.clnFn(ctx, tx)
	}
}

func (mw *lcTxHandlerMW) HandleServerTransaction(ctx context.Context, tx sip.ServerTransaction) {
	if mw.closed.Load() {
		mw.callsAfterClose.Add(1)
	}
	mw.srvCalls.Add(1)
	if mw.srvFn != nil {
		mw.srvFn(ctx, tx)
	}
}

func (mw *lcTxHandlerMW) Close() error {
	mw.closed.Store(true)
	mw.closes.Add(1)
	if mw.closeFn != nil {
		mw.closeFn()
	}
	return nil
}

// lcOutReqObserver counts outbound request interceptions, CANCEL interceptions
// delivered while the middleware was open, and any delivered after it closed.
type lcOutReqObserver struct {
	closed           atomic.Bool
	closes           atomic.Int32
	calls            atomic.Int32
	cancelCalls      atomic.Int32
	afterClose       atomic.Int32
	cancelAfterClose atomic.Int32
}

func (mw *lcOutReqObserver) InterceptOutboundRequest(
	ctx context.Context,
	next sip.RequestSender,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	mw.calls.Add(1)
	isCancel := req.Method().Equal(sip.RequestMethodCancel)
	if mw.closed.Load() {
		mw.afterClose.Add(1)
		if isCancel {
			mw.cancelAfterClose.Add(1)
		}
	} else if isCancel {
		mw.cancelCalls.Add(1)
	}
	return errors.Wrap(next.SendRequest(ctx, req, opts...))
}

func (mw *lcOutReqObserver) Close() error {
	mw.closed.Store(true)
	mw.closes.Add(1)
	return nil
}

// lcGatedOutReqInterceptor blocks its first outbound request interception until
// released, exposing the callback drain window during Close.
type lcGatedOutReqInterceptor struct {
	gate    *lcGate
	doneFn  func()
	closeFn func()

	closed atomic.Bool
	closes atomic.Int32
	calls  atomic.Int32
}

func (mw *lcGatedOutReqInterceptor) InterceptOutboundRequest(
	ctx context.Context,
	next sip.RequestSender,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	mw.calls.Add(1)
	mw.gate.enter()
	<-mw.gate.release
	if mw.doneFn != nil {
		mw.doneFn()
	}
	return errors.Wrap(next.SendRequest(ctx, req, opts...))
}

func (mw *lcGatedOutReqInterceptor) Close() error {
	mw.closed.Store(true)
	mw.closes.Add(1)
	if mw.closeFn != nil {
		mw.closeFn()
	}
	return nil
}

// lcGatedLocator blocks inside request-address iteration until released.
type lcGatedLocator struct {
	gate *lcGate
	addr sip.ResolvedAddr
}

func (l *lcGatedLocator) LookupRequestAddrs(
	context.Context,
	*sip.URI,
	...sip.LookupMessageAddrsOptions,
) iter.Seq[sip.ResolvedAddr] {
	return func(yield func(sip.ResolvedAddr) bool) {
		l.gate.enter()
		<-l.gate.release
		yield(l.addr)
	}
}

// lcRecordingTransport is a transport stub that runs its outbound request
// interceptor chain like real transports and records the requests it sends.
type lcRecordingTransport struct {
	*proxyStubTransport
	sendReqCalls chan *sip.RequestEnvelope
}

func newLcRecordingTransport() *lcRecordingTransport {
	return &lcRecordingTransport{
		proxyStubTransport: newProxyStubTransport(),
		sendReqCalls:       make(chan *sip.RequestEnvelope, 8),
	}
}

func (tp *lcRecordingTransport) SendRequest(
	ctx context.Context,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	sender := sip.InterceptOutboundRequest(
		slices.Collect(tp.OutboundRequestInterceptors.All()),
		sip.RequestSenderFunc(func(context.Context, *sip.RequestEnvelope, ...sip.SendRequestOptions) error {
			tp.sendReqCalls <- req
			return nil
		}),
	)
	return errors.Wrap(sender.SendRequest(ctx, req, opts...))
}

// waitElementClosing spins until the element rejects a call, proving that
// Close has sealed new-work admission. The closing state is monotonic, so the
// wait converges deterministically.
func waitElementClosing(tb testing.TB, elm *sip.Element) {
	tb.Helper()

	tmr := time.NewTimer(asyncEventTimeout)
	defer tmr.Stop()

	for {
		// While running the nil transport is admitted and rejected by the
		// manager for its nil argument; once closing begins the element
		// rejects it with ErrElementClosed before reaching the manager.
		if errors.Is(elm.TrackTransport(nil), sip.ErrElementClosed) {
			return
		}
		select {
		case <-tmr.C:
			tb.Fatal("timed out waiting for the element to start closing")
		default:
			runtime.Gosched()
		}
	}
}

func assertElementMethods(tb testing.TB, elm *sip.Element, want ...sip.RequestMethod) {
	tb.Helper()

	if diff := cmp.Diff(want, elm.Capabilities().Methods); diff != "" {
		tb.Fatalf("elm.Capabilities().Methods mismatch (-want +got):\n%s", diff)
	}
}

func TestElement_UseMiddleware_RejectedOnClose(t *testing.T) {
	t.Parallel()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	gate := newLcGate()
	t.Cleanup(gate.open)

	elm.UseMiddleware(&lcGatedCloser{gate: gate})

	closeDone := make(chan error, 1)
	go func() { closeDone <- elm.Close() }()

	// Close reached the middleware shutdown stage and is blocked inside the
	// gated closer, so new-work admission is already sealed.
	gate.waitEnter(t)

	late := &lcCapsCloser{methods: []sip.RequestMethod{sip.RequestMethodBye}}
	unbindLate := elm.UseMiddleware(late)
	if unbindLate == nil {
		t.Fatal("elm.UseMiddleware() during close returned nil unbind")
	}
	if elm.Capabilities().SupportsMethod(sip.RequestMethodBye) {
		t.Error("elm.Capabilities() supports BYE after a rejected registration, want unchanged")
	}
	if got := late.closes.Load(); got != 0 {
		t.Errorf("late.closes = %d during close, want 0 (rejected middleware stays caller-owned)", got)
	}

	gate.open()
	if err := testutil.WaitResult(t, closeDone, "elm.Close()"); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}
	if got := late.closes.Load(); got != 0 {
		t.Errorf("late.closes = %d after close, want 0", got)
	}

	// Registration stays rejected after Close completed.
	late2 := &lcCapsCloser{methods: []sip.RequestMethod{sip.RequestMethodBye}}
	unbindLate2 := elm.UseMiddleware(late2)
	if unbindLate2 == nil {
		t.Fatal("elm.UseMiddleware() after close returned nil unbind")
	}
	if elm.Capabilities().SupportsMethod(sip.RequestMethodBye) {
		t.Error("elm.Capabilities() supports BYE after a post-close registration, want unchanged")
	}
	if got := late2.closes.Load(); got != 0 {
		t.Errorf("late2.closes = %d, want 0", got)
	}

	// Returned unbinds remain callable and idempotent.
	unbindLate()
	unbindLate()
	unbindLate2()
	unbindLate2()
}

func TestElement_Close_CallbacksBeforeMiddlewareClose(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	tp := newProxyStubTransport()
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	events := &lcEventLog{}
	mw := &lcTxHandlerMW{
		closeFn: func() { events.add("mw:closed") },
	}
	mw.clnFn = func(_ context.Context, tx sip.ClientTransaction) {
		tx.BindStateHandler(sip.TransactionStateHandlerFunc(
			func(_ context.Context, _, to sip.TransactionState) {
				if to != sip.TransactionStateTerminated {
					return
				}
				if mw.closed.Load() {
					events.add("cb:terminated-after-close")
					return
				}
				events.add("cb:terminated")
			},
		))
	}
	elm.UseMiddleware(mw)

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	req := newInInviteReq(t, "UDP", "", laddr, raddr)

	tx, err := elm.NewClientTransaction(ctx, req, tp)
	if err != nil {
		t.Fatalf("elm.NewClientTransaction() error = %v, want nil", err)
	}
	startClientTransaction(t, tx)

	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}

	if got := mw.closes.Load(); got != 1 {
		t.Errorf("mw.closes = %d, want 1", got)
	}
	if got := mw.callsAfterClose.Load(); got != 0 {
		t.Errorf("transaction notifications after middleware close = %d, want 0", got)
	}

	cbIdx := events.index("cb:terminated")
	if cbIdx < 0 {
		t.Fatal("missing termination callback while middleware was alive")
	}
	if closedIdx := events.index("mw:closed"); closedIdx <= cbIdx {
		t.Errorf("middleware closed at event %d before termination callback at %d", closedIdx, cbIdx)
	}
	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Errorf("tx.State() = %v, want %v", got, sip.TransactionStateTerminated)
	}
}

func TestElement_NewTransaction_AdmitsBeforeClose(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	t.Run("ClientFactory", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()

		gate := newLcGate()
		events := &lcEventLog{}

		elm := &sip.Element{
			ClientTransactionFactory: sip.ClientTransactionFactoryFunc(
				func(
					req *sip.RequestEnvelope,
					tp sip.ClientTransport,
					opts ...sip.ClientTransactionOptions,
				) (sip.ClientTransaction, error) {
					gate.enter()
					<-gate.release
					events.add("op:done")
					return sip.NewClientTransaction(req, tp, opts...)
				},
			),
		}
		t.Cleanup(func() { _ = elm.Close() })
		t.Cleanup(gate.open)

		mw := &lcTxHandlerMW{closeFn: func() { events.add("mw:closed") }}
		elm.UseMiddleware(mw)

		type txResult struct {
			tx  sip.ClientTransaction
			err error
		}
		txDone := make(chan txResult, 1)
		req := newInInviteReq(t, "UDP", "", laddr, raddr)
		go func() {
			tx, err := elm.NewClientTransaction(ctx, req, newProxyStubTransport())
			txDone <- txResult{tx: tx, err: err}
		}()
		gate.waitEnter(t)

		closeDone := make(chan error, 1)
		go func() { closeDone <- elm.Close() }()
		waitElementClosing(t, elm)

		// New work must reject while the admitted factory call is in flight.
		if _, err := elm.NewClientTransaction(ctx, req, newProxyStubTransport()); !errors.Is(err, sip.ErrElementClosed) {
			t.Fatalf("elm.NewClientTransaction() during close error = %v, want %v", err, sip.ErrElementClosed)
		}
		if got := mw.closes.Load(); got != 0 {
			t.Fatalf("middleware closed %d times while an admitted operation is in flight, want 0", got)
		}

		gate.open()

		res := testutil.WaitResult(t, txDone, "elm.NewClientTransaction()/NewServerTransaction")
		if res.err == nil && res.tx == nil {
			t.Fatal("elm.NewClientTransaction() = nil, nil during close")
		}

		if err := <-closeDone; err != nil {
			t.Fatalf("elm.Close() error = %v, want nil", err)
		}

		if got := mw.closes.Load(); got != 1 {
			t.Errorf("mw.closes = %d, want 1", got)
		}
		if got := mw.callsAfterClose.Load(); got != 0 {
			t.Errorf("handler notifications after middleware close = %d, want 0", got)
		}
		if opIdx, closedIdx := events.index("op:done"), events.index("mw:closed"); opIdx < 0 || closedIdx <= opIdx {
			t.Errorf("event order op:done=%d mw:closed=%d, want admitted operation to finish first", opIdx, closedIdx)
		}
		if res.tx != nil && res.tx.State() != sip.TransactionStateTerminated {
			t.Errorf("tx.State() = %v after close, want %v", res.tx.State(), sip.TransactionStateTerminated)
		}
		if got := slices.Collect(elm.AllClientTransactions()); len(got) != 0 {
			t.Errorf("client transaction index has %d entries after close, want 0", len(got))
		}
	})

	t.Run("ServerFactory", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()

		gate := newLcGate()
		events := &lcEventLog{}

		elm := &sip.Element{
			ServerTransactionFactory: sip.ServerTransactionFactoryFunc(
				func(
					req *sip.RequestEnvelope,
					tp sip.ServerTransport,
					opts ...sip.ServerTransactionOptions,
				) (sip.ServerTransaction, error) {
					gate.enter()
					<-gate.release
					events.add("op:done")
					return sip.NewServerTransaction(req, tp, opts...)
				},
			),
		}
		t.Cleanup(func() { _ = elm.Close() })
		t.Cleanup(gate.open)

		mw := &lcTxHandlerMW{closeFn: func() { events.add("mw:closed") }}
		elm.UseMiddleware(mw)

		type txResult struct {
			tx  sip.ServerTransaction
			err error
		}
		txDone := make(chan txResult, 1)
		req := newInInviteReq(t, "UDP", "", laddr, raddr)
		go func() {
			tx, err := elm.NewServerTransaction(ctx, req, newProxyStubTransport())
			txDone <- txResult{tx: tx, err: err}
		}()
		gate.waitEnter(t)

		closeDone := make(chan error, 1)
		go func() { closeDone <- elm.Close() }()
		waitElementClosing(t, elm)

		if _, err := elm.NewServerTransaction(ctx, req, newProxyStubTransport()); !errors.Is(err, sip.ErrElementClosed) {
			t.Fatalf("elm.NewServerTransaction() during close error = %v, want %v", err, sip.ErrElementClosed)
		}
		if got := mw.closes.Load(); got != 0 {
			t.Fatalf("middleware closed %d times while an admitted operation is in flight, want 0", got)
		}

		gate.open()

		res := testutil.WaitResult(t, txDone, "elm.NewClientTransaction()/NewServerTransaction")
		if res.err == nil && res.tx == nil {
			t.Fatal("elm.NewServerTransaction() = nil, nil during close")
		}

		if err := <-closeDone; err != nil {
			t.Fatalf("elm.Close() error = %v, want nil", err)
		}

		if got := mw.closes.Load(); got != 1 {
			t.Errorf("mw.closes = %d, want 1", got)
		}
		if got := mw.callsAfterClose.Load(); got != 0 {
			t.Errorf("handler notifications after middleware close = %d, want 0", got)
		}
		if opIdx, closedIdx := events.index("op:done"), events.index("mw:closed"); opIdx < 0 || closedIdx <= opIdx {
			t.Errorf("event order op:done=%d mw:closed=%d, want admitted operation to finish first", opIdx, closedIdx)
		}
		if res.tx != nil && res.tx.State() != sip.TransactionStateTerminated {
			t.Errorf("tx.State() = %v after close, want %v", res.tx.State(), sip.TransactionStateTerminated)
		}
		if got := slices.Collect(elm.AllServerTransactions()); len(got) != 0 {
			t.Errorf("server transaction index has %d entries after close, want 0", len(got))
		}
	})
}

func TestElement_MiddlewareCallback_DrainedAndSealed(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })
	if err := elm.TrackTransport(newProxyStubTransport()); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	gate := newLcGate()
	t.Cleanup(gate.open)
	events := &lcEventLog{}

	mwA := &lcTxHandlerMW{closeFn: func() { events.add("mwA:closed") }}
	mwA.clnFn = func(context.Context, sip.ClientTransaction) {
		gate.enter()
		<-gate.release
		events.add("cbA:done")
	}
	mwB := &lcTxHandlerMW{}

	elm.UseMiddleware(mwA)
	elm.UseMiddleware(mwB)

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	req := newInInviteReq(t, "UDP", "", laddr, raddr)

	// Initiate through the element facade: the transaction is created and
	// registered in one admitted operation, and the notification iterates the
	// captured handler snapshot [A, B] and blocks inside A.
	txDone := make(chan error, 1)
	go func() {
		_, err := elm.NewClientTransaction(ctx, req, newProxyStubTransport())
		txDone <- err
	}()
	gate.waitEnter(t)

	closeDone := make(chan error, 1)
	go func() { closeDone <- elm.Close() }()
	waitElementClosing(t, elm)

	// Close is underway but must not close middleware while A's notification
	// is in flight.
	if _, err := elm.NewClientTransaction(ctx, req, newProxyStubTransport()); !errors.Is(err, sip.ErrElementClosed) {
		t.Fatalf("elm.NewClientTransaction() during close error = %v, want %v", err, sip.ErrElementClosed)
	}
	if got := mwA.closes.Load(); got != 0 {
		t.Fatalf("mwA.closes = %d while its notification is in flight, want 0", got)
	}

	gate.open()

	if err := testutil.WaitResult(t, txDone, "elm.NewClientTransaction()"); err != nil && !errors.Is(err, sip.ErrTransactionManagerClosed) {
		t.Fatalf("elm.NewClientTransaction() error = %v, want nil or %v", err, sip.ErrTransactionManagerClosed)
	}
	if err := testutil.WaitResult(t, closeDone, "elm.Close()"); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}

	if got := mwA.clnCalls.Load(); got != 1 {
		t.Errorf("mwA handler calls = %d, want 1", got)
	}
	// B's notification was captured in the same snapshot and may still be
	// delivered while callback admission is open during closing; the invariant
	// is that no notification reaches middleware after its own Close.
	for _, mw := range []*lcTxHandlerMW{mwA, mwB} {
		if got := mw.closes.Load(); got != 1 {
			t.Errorf("middleware closes = %d, want 1", got)
		}
		if got := mw.callsAfterClose.Load(); got != 0 {
			t.Errorf("notifications after middleware close = %d, want 0", got)
		}
	}
	if cbIdx, closedIdx := events.index("cbA:done"), events.index("mwA:closed"); cbIdx < 0 || closedIdx <= cbIdx {
		t.Errorf("event order cbA:done=%d mwA:closed=%d, want callback drained before close", cbIdx, closedIdx)
	}
}

func TestElement_Capabilities_ConcurrentRecompute(t *testing.T) {
	t.Parallel()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	gateA := newLcGate()
	t.Cleanup(gateA.open)
	mwA := &lcCapsCloser{methods: []sip.RequestMethod{sip.RequestMethodInvite}}
	mwA.capFn = func(call int32) {
		if call == 1 {
			gateA.enter()
			<-gateA.release
		}
	}

	// The first registration recomputes over the [A] snapshot and blocks
	// inside A's first Capabilities call.
	useADone := make(chan struct{})
	go func() {
		defer close(useADone)
		elm.UseMiddleware(mwA)
	}()
	gateA.waitEnter(t)

	gateB := newLcGate()
	t.Cleanup(gateB.open)
	mwB := &lcCapsCloser{methods: []sip.RequestMethod{sip.RequestMethodBye}}
	mwB.capFn = func(call int32) {
		// call 1 runs in B's own registration recompute, call 2 in the retry
		// recompute of A's registration; call 3 runs inside C's recomputation.
		if call == 3 {
			gateB.enter()
			<-gateB.release
		}
	}

	// The second recomputation snapshots [A,B]; A's second call does not block.
	unbindB := elm.UseMiddleware(mwB)
	assertElementMethods(t, elm, sip.RequestMethodInvite, sip.RequestMethodBye)

	gateA.open()
	testutil.WaitResult(t, useADone, "UseMiddleware(A)")

	// The stale [A] result must not overwrite the [A,B] capabilities.
	assertElementMethods(t, elm, sip.RequestMethodInvite, sip.RequestMethodBye)

	// A removal committed while a recompute is in flight must not be
	// resurrected by that recompute.
	mwC := &lcCapsCloser{methods: []sip.RequestMethod{sip.RequestMethodCancel}}
	useCDone := make(chan struct{})
	go func() {
		defer close(useCDone)
		elm.UseMiddleware(mwC)
	}()
	// B's third Capabilities call blocks inside C's [A,B,C] recomputation.
	gateB.waitEnter(t)

	unbindB()
	assertElementMethods(t, elm, sip.RequestMethodInvite, sip.RequestMethodCancel)

	gateB.open()
	testutil.WaitResult(t, useCDone, "UseMiddleware(C)")
	assertElementMethods(t, elm, sip.RequestMethodInvite, sip.RequestMethodCancel)

	// Repeated and concurrent unbinds stay idempotent.
	unbindB()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(unbindB)
	}
	wg.Wait()
	assertElementMethods(t, elm, sip.RequestMethodInvite, sip.RequestMethodCancel)
}

func TestElement_Capabilities_ReentrantProvider(t *testing.T) {
	t.Parallel()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	// A provider may reenter capabilities reads and registration from inside
	// its own Capabilities call: no element lock is held across provider calls.
	leaf := &lcCapsCloser{methods: []sip.RequestMethod{sip.RequestMethodOptions}}

	var regOnce atomic.Bool
	self := &lcCapsCloser{methods: []sip.RequestMethod{sip.RequestMethodInvite}}
	self.capFn = func(call int32) {
		if call == 1 && regOnce.CompareAndSwap(false, true) {
			_ = elm.Capabilities()
			elm.UseMiddleware(leaf)
		}
	}
	elm.UseMiddleware(self)
	assertElementMethods(t, elm, sip.RequestMethodInvite, sip.RequestMethodOptions)

	// A provider may also reenter an unbind: the removal is committed, the
	// in-flight recompute is invalidated and retries without the removed entry.
	unbindU := elm.UseMiddleware(&lcCapsCloser{methods: []sip.RequestMethod{sip.RequestMethodInfo}})

	var unbindOnce atomic.Bool
	trigger := &lcCapsCloser{methods: []sip.RequestMethod{sip.RequestMethodAck}}
	trigger.capFn = func(call int32) {
		if call == 1 && unbindOnce.CompareAndSwap(false, true) {
			unbindU()
		}
	}
	elm.UseMiddleware(trigger)
	assertElementMethods(
		t, elm,
		sip.RequestMethodInvite,
		sip.RequestMethodOptions,
		sip.RequestMethodAck,
	)
}

func TestElement_PublicOperations_RejectedDuringClose(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	gate := newLcGate()
	t.Cleanup(gate.open)

	closer := &lcGatedCloser{gate: gate}
	elm.UseMiddleware(closer)

	tp := newProxyStubTransport()
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	req := newInInviteReq(t, "UDP", "", laddr, raddr)
	res := newInRes(t, req, sip.ResponseStatusOK)

	// Produce one attempt while the element is running; its Do* methods are
	// exercised after closing begins.
	attempts, err := elm.ProduceRequestAttempts(ctx, req)
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

	closeDone := make(chan error, 1)
	go func() { closeDone <- elm.Close() }()
	gate.waitEnter(t)
	// Registered after gate.waitEnter so it runs before the earlier
	// gate.open cleanup: releases the gated closer and verifies Close
	// succeeds once all parallel subtests have finished.
	t.Cleanup(func() {
		gate.open()
		if err := testutil.WaitResult(t, closeDone, "elm.Close()"); err != nil {
			t.Errorf("elm.Close() error = %v, want nil", err)
		}
	})

	tests := []struct {
		name string
		call func() error
	}{
		{"TrackTransport", func() error { return elm.TrackTransport(newProxyStubTransport()) }},
		{"UntrackTransport", func() error { return elm.UntrackTransport(tp) }},
		{"ListenManaged", func() error { return elm.ListenManaged(ctx, "UDP", "127.0.0.1:0") }},
		{"Listen", func() error { _, err := elm.Listen(ctx, "UDP", "127.0.0.1:0"); return err }},
		{"SendRequest", func() error { return elm.SendRequest(ctx, req) }},
		{"SendRequestStateless", func() error { return elm.SendRequestStateless(ctx, req) }},
		{"SendResponse", func() error { return elm.SendResponse(ctx, res) }},
		{"Respond", func() error { return elm.Respond(ctx, req, sip.ResponseStatusOK) }},
		{"NewClientTransaction", func() error { _, err := elm.NewClientTransaction(ctx, req, tp); return err }},
		{"NewServerTransaction", func() error { _, err := elm.NewServerTransaction(ctx, req, tp); return err }},
		{"SendRequestStateful", func() error { _, err := elm.SendRequestStateful(ctx, req); return err }},
		{"SendResponseStateful", func() error { _, err := elm.SendResponseStateful(ctx, req, res); return err }},
		{"RespondStateful", func() error { _, err := elm.RespondStateful(ctx, req, sip.ResponseStatusOK); return err }},
		{"RequestAttempt.DoStateless", func() error { return attempt.DoStateless(ctx) }},
		{"RequestAttempt.DoStateful", func() error { _, err := attempt.DoStateful(ctx); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Parallel subtests run while Close is still blocked inside the
			// gated closer, so every call observes the Closing state rather
			// than a completed close.
			if closer.returned.Load() {
				t.Error("gated middleware Close returned before subtests ran")
			}
			if err := tt.call(); !errors.Is(err, sip.ErrElementClosed) {
				t.Errorf("elm.%s() error = %v, want %v", tt.name, err, sip.ErrElementClosed)
			}
		})
	}
}

func TestElement_Close_CancelProceedingInviteThroughMiddleware(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	tp := newLcRecordingTransport()
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	observer := &lcOutReqObserver{}
	elm.UseMiddleware(observer)

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	req := newInInviteReq(t, "UDP", "", laddr, raddr)

	tx, err := elm.NewClientTransaction(ctx, req, tp)
	if err != nil {
		t.Fatalf("elm.NewClientTransaction() error = %v, want nil", err)
	}
	startClientTransaction(t, tx)

	// Drive the INVITE transaction to proceeding so shutdown sends a CANCEL.
	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
		t.Fatalf("tx.RecvResponse() error = %v, want nil", err)
	}
	if got := tx.State(); got != sip.TransactionStateProceeding {
		t.Fatalf("tx.State() = %v, want %v", got, sip.TransactionStateProceeding)
	}

	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}

	// The internal CANCEL bypassed the sealed public admission but still went
	// through live middleware to the transport.
	var methods []sip.RequestMethod
	for done := false; !done; {
		select {
		case sent := <-tp.sendReqCalls:
			methods = append(methods, sent.Method())
		default:
			done = true
		}
	}
	if !slices.ContainsFunc(methods, func(m sip.RequestMethod) bool { return m.Equal(sip.RequestMethodCancel) }) {
		t.Errorf("transport requests = %v, want a CANCEL among them", methods)
	}

	if got := observer.cancelCalls.Load(); got == 0 {
		t.Error("CANCEL interceptions while middleware open = 0, want at least 1")
	}
	if got := observer.cancelAfterClose.Load(); got != 0 {
		t.Errorf("CANCEL interceptions after middleware close = %d, want 0", got)
	}
	if got := observer.afterClose.Load(); got != 0 {
		t.Errorf("interceptions after middleware close = %d, want 0", got)
	}
	if got := observer.closes.Load(); got != 1 {
		t.Errorf("observer.closes = %d, want 1", got)
	}
	if got := tx.State(); got != sip.TransactionStateTerminated {
		t.Errorf("tx.State() = %v, want %v", got, sip.TransactionStateTerminated)
	}
}

func TestElement_MiddlewareInterceptor_DrainedBeforeClose(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	gate := newLcGate()
	t.Cleanup(gate.open)
	events := &lcEventLog{}

	mw := &lcGatedOutReqInterceptor{
		gate:    gate,
		doneFn:  func() { events.add("intcptr:done") },
		closeFn: func() { events.add("mw:closed") },
	}
	elm.UseMiddleware(mw)

	tp := newLcRecordingTransport()
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	req := newInInviteReq(t, "UDP", "", laddr, raddr)

	// Send through the transport directly: the interceptor binding wired by
	// the element runs inside the transport's own outbound chain.
	sendDone := make(chan error, 1)
	go func() { sendDone <- tp.SendRequest(ctx, req) }()
	gate.waitEnter(t)

	closeDone := make(chan error, 1)
	go func() { closeDone <- elm.Close() }()
	waitElementClosing(t, elm)

	// Middleware must not be closed while its interceptor callback is in
	// flight.
	if got := mw.closes.Load(); got != 0 {
		t.Fatalf("mw.closes = %d while its interceptor is in flight, want 0", got)
	}

	gate.open()

	if err := testutil.WaitResult(t, sendDone, "tp.SendRequest()"); err != nil {
		t.Fatalf("tp.SendRequest() error = %v, want nil", err)
	}
	if err := testutil.WaitResult(t, closeDone, "elm.Close()"); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}

	if got := mw.calls.Load(); got != 1 {
		t.Errorf("interceptor calls = %d, want 1", got)
	}
	if got := mw.closes.Load(); got != 1 {
		t.Errorf("mw.closes = %d, want 1", got)
	}
	if doneIdx, closedIdx := events.index("intcptr:done"), events.index("mw:closed"); doneIdx < 0 || closedIdx <= doneIdx {
		t.Errorf("event order intcptr:done=%d mw:closed=%d, want interceptor drained first", doneIdx, closedIdx)
	}
	if got := len(tp.sendReqCalls); got != 1 {
		t.Errorf("transport sent requests = %d, want 1", got)
	}
}

func TestElement_UseMiddleware_UnbindReentry(t *testing.T) {
	t.Parallel()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	var unbindB func()
	var once atomic.Bool
	mwA := &lcCapsCloser{methods: []sip.RequestMethod{sip.RequestMethodInvite}}
	mwA.capFn = func(int32) {
		// Reenter the same unbind while it is recomputing capabilities; the
		// committed removal must make the nested call a prompt no-op.
		if unbindB != nil && once.CompareAndSwap(false, true) {
			unbindB()
		}
	}

	elm.UseMiddleware(mwA)
	unbindB = elm.UseMiddleware(&lcCapsCloser{methods: []sip.RequestMethod{sip.RequestMethodBye}})
	assertElementMethods(t, elm, sip.RequestMethodInvite, sip.RequestMethodBye)

	unbindB()
	assertElementMethods(t, elm, sip.RequestMethodInvite)
	if !once.Load() {
		t.Error("unbind was never reentered from the provider callback")
	}

	unbindB()
	assertElementMethods(t, elm, sip.RequestMethodInvite)
}

func TestElement_UseMiddleware_UnbindCompletesBeforeMiddlewareClose(t *testing.T) {
	t.Parallel()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	gate := newLcGate()
	t.Cleanup(gate.open)

	var arm atomic.Bool
	mwA := &lcCapsCloser{methods: []sip.RequestMethod{sip.RequestMethodInvite}}
	mwA.capFn = func(int32) {
		if arm.Load() {
			gate.enter()
			<-gate.release
		}
	}

	elm.UseMiddleware(mwA)
	unbindB := elm.UseMiddleware(&lcCapsCloser{methods: []sip.RequestMethod{sip.RequestMethodBye}})
	assertElementMethods(t, elm, sip.RequestMethodInvite, sip.RequestMethodBye)

	// An admitted unbind recomputes capabilities through live providers; Close
	// must wait for it before shutting down the remaining middleware.
	arm.Store(true)
	unbindDone := make(chan struct{})
	go func() {
		defer close(unbindDone)
		unbindB()
	}()
	gate.waitEnter(t)

	closeDone := make(chan error, 1)
	go func() { closeDone <- elm.Close() }()
	waitElementClosing(t, elm)

	if got := mwA.closes.Load(); got != 0 {
		t.Fatalf("mwA.closes = %d while an admitted unbind is in flight, want 0", got)
	}

	gate.open()
	testutil.WaitResult(t, unbindDone, "unbind")

	if err := testutil.WaitResult(t, closeDone, "elm.Close()"); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}
	if got := mwA.closes.Load(); got != 1 {
		t.Errorf("mwA.closes = %d, want 1", got)
	}
	assertElementMethods(t, elm, sip.RequestMethodInvite)
}

func TestElement_SendRequest_LookupDoesNotBlockClose(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	gate := newLcGate()
	t.Cleanup(gate.open)

	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	elm := &sip.Element{
		ServerLocator: &lcGatedLocator{
			gate: gate,
			addr: sip.ResolvedAddr{Transport: "UDP", Addr: raddr},
		},
	}
	t.Cleanup(func() { _ = elm.Close() })

	tp := newLcRecordingTransport()
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}
	observer := &lcOutReqObserver{}
	elm.UseMiddleware(observer)

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	req := newInInviteReq(t, "UDP", "", laddr, raddr)

	// The send blocks inside the locator iteration before yielding its first
	// candidate; no operation lease is held across the lookup.
	sendDone := make(chan error, 1)
	go func() { sendDone <- elm.SendRequest(ctx, req) }()
	gate.waitEnter(t)

	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v while a send was blocked in lookup, want nil", err)
	}

	// Once the lookup resumes, the attempt send is rejected by admission and
	// never reaches closed middleware or the transport.
	gate.open()
	if err := testutil.WaitResult(t, sendDone, "elm.SendRequest()"); !errors.Is(err, sip.ErrElementClosed) {
		t.Fatalf("elm.SendRequest() error = %v, want %v", err, sip.ErrElementClosed)
	}
	if got := observer.calls.Load(); got != 0 {
		t.Errorf("outbound interceptions = %d after close, want 0", got)
	}
	if got := len(tp.sendReqCalls); got != 0 {
		t.Errorf("transport sent requests = %d, want 0", got)
	}
	if got := observer.closes.Load(); got != 1 {
		t.Errorf("observer.closes = %d, want 1", got)
	}
}

// lcShutdownMW implements the optional middleware shutdown hook and Close,
// recording both into a shared event log.
type lcShutdownMW struct {
	name       string
	events     *lcEventLog
	shutdowns  atomic.Int32
	closes     atomic.Int32
	shutdownFn func(ctx context.Context) error
}

func (mw *lcShutdownMW) Shutdown(ctx context.Context) error {
	mw.shutdowns.Add(1)
	mw.events.add(mw.name + ":shutdown")
	if mw.shutdownFn != nil {
		return errors.Wrap(mw.shutdownFn(ctx))
	}
	return nil
}

func (mw *lcShutdownMW) Close() error {
	mw.closes.Add(1)
	mw.events.add(mw.name + ":closed")
	return nil
}

// lcEventCloser records its Close into the shared event log.
type lcEventCloser struct {
	name   string
	events *lcEventLog
	closes atomic.Int32
}

func (mw *lcEventCloser) Close() error {
	mw.closes.Add(1)
	mw.events.add(mw.name + ":closed")
	return nil
}

// lcCloseErrTransport embeds the stub transport but fails its Close.
type lcCloseErrTransport struct {
	*proxyStubTransport
	err error
}

func (tp *lcCloseErrTransport) Close() error { return errors.Wrap(tp.err) }

// lcShutdownCtxKey is a context value key for the shutdown hook probe.
type lcShutdownCtxKey struct{}

func TestElement_Shutdown_ZeroValue(t *testing.T) {
	t.Parallel()

	elm := &sip.Element{}

	if err := elm.Shutdown(t.Context()); err != nil {
		t.Fatalf("elm.Shutdown() error = %v, want nil", err)
	}
	if err := elm.Shutdown(t.Context()); err != nil {
		t.Fatalf("repeated elm.Shutdown() error = %v, want nil", err)
	}
	if err := elm.TrackTransport(nil); !errors.Is(err, sip.ErrElementClosed) {
		t.Fatalf("elm.TrackTransport() after shutdown error = %v, want %v", err, sip.ErrElementClosed)
	}
}

func TestElement_Shutdown_MiddlewareBeforeClose(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	tp := newLcRecordingTransport()
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	events := &lcEventLog{}
	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	req := newInInviteReq(t, "UDP", "", laddr, raddr)

	// The first hook still uses the running element: it registers and starts
	// a client transaction that the final Close must terminate.
	var hookTx sip.ClientTransaction
	mwA := &lcShutdownMW{name: "a", events: events}
	mwA.shutdownFn = func(ctx context.Context) error {
		tx, err := elm.NewClientTransaction(ctx, req, tp)
		if err != nil {
			return errors.Wrap(err)
		}
		if err := tx.Start(ctx); err != nil {
			return errors.Wrap(err)
		}
		hookTx = tx
		return nil
	}
	mwB := &lcShutdownMW{name: "b", events: events}
	plain := &lcEventCloser{name: "plain", events: events}

	elm.UseMiddleware(mwA)
	elm.UseMiddleware(plain)
	elm.UseMiddleware(mwB)

	if err := elm.Shutdown(ctx); err != nil {
		t.Fatalf("elm.Shutdown() error = %v, want nil", err)
	}

	// Hooks ran once, in registration order, before any middleware closed.
	sdA, sdB := events.index("a:shutdown"), events.index("b:shutdown")
	if sdA < 0 || sdB <= sdA {
		t.Fatalf("shutdown event order a=%d b=%d, want a before b", sdA, sdB)
	}
	firstClose := min(events.index("a:closed"), events.index("plain:closed"), events.index("b:closed"))
	if firstClose < 0 || sdB >= firstClose {
		t.Fatalf("last shutdown index = %d, first close index = %d, want all hooks before any close", sdB, firstClose)
	}
	for name, mw := range map[string]*lcShutdownMW{"a": mwA, "b": mwB} {
		if got := mw.shutdowns.Load(); got != 1 {
			t.Errorf("mw %s shutdown calls = %d, want 1", name, got)
		}
		if got := mw.closes.Load(); got != 1 {
			t.Errorf("mw %s closes = %d, want 1", name, got)
		}
	}
	if got := plain.closes.Load(); got != 1 {
		t.Errorf("plain.closes = %d, want 1", got)
	}

	if hookTx == nil {
		t.Fatal("shutdown hook created no client transaction")
	}
	if got := hookTx.State(); got != sip.TransactionStateTerminated {
		t.Errorf("hook transaction state = %v, want %v", got, sip.TransactionStateTerminated)
	}
	if got := len(slices.Collect(elm.AllClientTransactions())); got != 0 {
		t.Errorf("client transaction index size = %d, want 0", got)
	}
	if got := len(slices.Collect(elm.AllServerTransactions())); got != 0 {
		t.Errorf("server transaction index size = %d, want 0", got)
	}
}

func TestElement_Shutdown_CollectsErrors(t *testing.T) {
	t.Parallel()

	const (
		errShutdownA errors.Error = "shutdown A error"
		errShutdownB errors.Error = "shutdown B error"
		errTransport errors.Error = "transport close error"
	)

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	events := &lcEventLog{}
	mwA := &lcShutdownMW{
		name:       "a",
		events:     events,
		shutdownFn: func(context.Context) error { return errors.Wrap(errShutdownA) },
	}
	mwB := &lcShutdownMW{
		name:       "b",
		events:     events,
		shutdownFn: func(context.Context) error { return errors.Wrap(errShutdownB) },
	}
	elm.UseMiddleware(mwA)
	elm.UseMiddleware(mwB)

	tp := &lcCloseErrTransport{proxyStubTransport: newProxyStubTransport(), err: errTransport}
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	// A hook error must not skip the later hook or the final Close, so all
	// three failures surface in the joined result.
	err := elm.Shutdown(t.Context())
	for _, want := range []error{errShutdownA, errShutdownB, errTransport} {
		if !errors.Is(err, want) {
			t.Errorf("elm.Shutdown() error = %v, want chain containing %v", err, want)
		}
	}
	if got := mwB.shutdowns.Load(); got != 1 {
		t.Errorf("mwB.shutdowns = %d after mwA error, want 1", got)
	}
	for name, mw := range map[string]*lcShutdownMW{"a": mwA, "b": mwB} {
		if got := mw.closes.Load(); got != 1 {
			t.Errorf("mw %s closes = %d, want 1", name, got)
		}
	}
}

func TestElement_Shutdown_Context(t *testing.T) {
	t.Parallel()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), lcShutdownCtxKey{}, "probe"))
	cancel()

	// Hooks inherit caller values and cancellation along with the element;
	// even a canceled context does not skip the later hook or final Close.
	var sawValue atomic.Bool
	mwA := &lcShutdownMW{
		name:   "a",
		events: &lcEventLog{},
		shutdownFn: func(hookCtx context.Context) error {
			assertElementOwner(t, hookCtx, elm)
			if hookCtx.Done() != ctx.Done() {
				t.Error("hook context does not inherit caller cancellation")
			}
			sawValue.Store(hookCtx.Value(lcShutdownCtxKey{}) == "probe")
			return errors.Wrap(hookCtx.Err())
		},
	}
	mwB := &lcShutdownMW{name: "b", events: &lcEventLog{}}
	elm.UseMiddleware(mwA)
	elm.UseMiddleware(mwB)

	if err := elm.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("elm.Shutdown() error = %v, want %v", err, context.Canceled)
	}
	if !sawValue.Load() {
		t.Error("hook did not observe the caller context value")
	}
	if got := mwB.shutdowns.Load(); got != 1 {
		t.Errorf("mwB.shutdowns = %d after canceled context, want 1", got)
	}
	if got := mwA.closes.Load() + mwB.closes.Load(); got != 2 {
		t.Errorf("middleware closes = %d, want 2", got)
	}
}

func TestElement_Shutdown_ConcurrentCalls(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	gate := newLcGate()
	t.Cleanup(gate.open)

	const errShutdown errors.Error = "shutdown error"

	mw := &lcShutdownMW{
		name:   "a",
		events: &lcEventLog{},
		shutdownFn: func(context.Context) error {
			gate.enter()
			<-gate.release
			return errors.Wrap(errShutdown)
		},
	}
	elm.UseMiddleware(mw)

	// Concurrent callers share the single shutdown: one runs the gated hook
	// while the rest wait for the cached result.
	const callers = 4
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() { results <- elm.Shutdown(ctx) })
	}
	gate.waitEnter(t)
	gate.open()
	wg.Wait()
	close(results)

	for err := range results {
		if !errors.Is(err, errShutdown) {
			t.Errorf("concurrent elm.Shutdown() error = %v, want %v", err, errShutdown)
		}
	}
	if got := mw.shutdowns.Load(); got != 1 {
		t.Errorf("hook calls = %d, want 1", got)
	}
	if got := mw.closes.Load(); got != 1 {
		t.Errorf("mw.closes = %d, want 1", got)
	}
	if err := elm.Shutdown(ctx); !errors.Is(err, errShutdown) {
		t.Errorf("repeated elm.Shutdown() error = %v, want %v", err, errShutdown)
	}
}

func TestElement_Shutdown_AfterClose(t *testing.T) {
	t.Parallel()

	elm := &sip.Element{}

	mw := &lcShutdownMW{name: "a", events: &lcEventLog{}}
	elm.UseMiddleware(mw)

	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}
	if err := elm.Shutdown(t.Context()); err != nil {
		t.Fatalf("elm.Shutdown() after close error = %v, want nil", err)
	}
	if got := mw.shutdowns.Load(); got != 0 {
		t.Errorf("hook calls after close = %d, want 0", got)
	}
	if got := mw.closes.Load(); got != 1 {
		t.Errorf("mw.closes = %d, want 1", got)
	}
}

func TestElement_Shutdown_ConcurrentClose(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	gate := newLcGate()
	// Registered after the Close cleanup so a failure still releases the
	// blocked hook before the cleanup Close runs.
	t.Cleanup(gate.open)

	mw := &lcShutdownMW{
		name:   "a",
		events: &lcEventLog{},
		shutdownFn: func(context.Context) error {
			gate.enter()
			<-gate.release
			return nil
		},
	}
	elm.UseMiddleware(mw)

	sdDone := make(chan error, 1)
	go func() { sdDone <- elm.Shutdown(ctx) }()
	gate.waitEnter(t)

	closeDone := make(chan error, 1)
	go func() { closeDone <- elm.Close() }()
	waitElementClosing(t, elm)

	// The direct Close starts transport/transaction shutdown without waiting
	// for the hooks, but still waits for the admitted hook operation before
	// closing middleware.
	if got := mw.closes.Load(); got != 0 {
		t.Fatalf("mw.closes = %d while its shutdown hook is in flight, want 0", got)
	}

	gate.open()

	if err := testutil.WaitResult(t, sdDone, "elm.Shutdown()"); err != nil {
		t.Fatalf("elm.Shutdown() error = %v, want nil", err)
	}
	if err := testutil.WaitResult(t, closeDone, "elm.Close()"); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}
	if got := mw.shutdowns.Load(); got != 1 {
		t.Errorf("hook calls = %d, want 1", got)
	}
	if got := mw.closes.Load(); got != 1 {
		t.Errorf("mw.closes = %d, want 1", got)
	}
}

var errStubRun = errors.New("stub runtime failure")

// startStubTx is a concurrency-safe transaction core for managed-start tests:
// Start optionally blocks on a gate, records the received context, counts
// invocations, and returns a configured error.
type startStubTx struct {
	stubTransactionDefaults

	typ   sip.TransactionType
	state sip.TransactionState

	started    atomic.Bool
	startCalls atomic.Int32
	startCtxs  chan context.Context
	startDone  chan error
	gate       *lcGate
	startErr   error
	// waitDone and resume switch Start into cancellation-watch mode: it
	// blocks on ctx.Done, closes waitDone once cancellation is observed,
	// and stays blocked on resume until the test lets it return.
	waitDone chan struct{}
	resume   chan struct{}
	// onReturn, when set, runs as Start returns and lets a test author
	// ordering events exactly at the method return boundary.
	onReturn func()
}

func newStartStubTx(typ sip.TransactionType, state sip.TransactionState) *startStubTx {
	return &startStubTx{
		typ:       typ,
		state:     state,
		startCtxs: make(chan context.Context, 4),
		startDone: make(chan error, 4),
	}
}

func (tx *startStubTx) Type() sip.TransactionType   { return tx.typ }
func (tx *startStubTx) State() sip.TransactionState { return tx.state }
func (tx *startStubTx) Started() bool               { return tx.started.Load() }

func (*startStubTx) BindStartHandler(sip.TransactionStartHandler) (unbind func()) {
	return func() {}
}

func (*startStubTx) BindStateHandler(sip.TransactionStateHandler) (unbind func()) {
	return func() {}
}

func (tx *startStubTx) Start(ctx context.Context) error {
	defer func() {
		if tx.onReturn != nil {
			tx.onReturn()
		}
	}()

	// Count invocations at entry so a wrong duplicate start is observable
	// even though the CAS still rejects it.
	tx.startCalls.Add(1)
	if !tx.started.CompareAndSwap(false, true) {
		return errors.Wrap(sip.NewTransactionActionNotAllowedError())
	}

	tx.startCtxs <- ctx

	var err error
	switch {
	case tx.waitDone != nil:
		<-ctx.Done()
		close(tx.waitDone)
		err = ctx.Err()
		<-tx.resume
	case tx.gate != nil:
		tx.gate.enter()
		select {
		case <-tx.gate.release:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if err == nil {
		err = tx.startErr
	}
	tx.startDone <- err
	return errors.Wrap(err)
}

// startStubClientTx adapts startStubTx to [sip.ClientTransaction].
type startStubClientTx struct {
	*startStubTx
	stubClientTransactionDefaults

	key sip.ClientTransactionKey
}

func newStartStubClientTx(branch string) *startStubClientTx {
	return &startStubClientTx{
		startStubTx: newStartStubTx(sip.TransactionTypeClientInvite, sip.TransactionStateCalling),
		key:         sip.ClientTransactionKey{Branch: branch, Method: string(sip.RequestMethodInvite)},
	}
}

func (tx *startStubClientTx) Key() sip.ClientTransactionKey { return tx.key }

// startStubServerTx adapts startStubTx to [sip.ServerTransaction].
type startStubServerTx struct {
	*startStubTx
	stubServerTransactionDefaults

	key sip.ServerTransactionKey
	req *sip.RequestEnvelope
}

func newStartStubServerTx(tb testing.TB, branch string) *startStubServerTx {
	tb.Helper()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	return &startStubServerTx{
		startStubTx: newStartStubTx(sip.TransactionTypeServerInvite, sip.TransactionStateProceeding),
		key: sip.ServerTransactionKey{
			Branch: branch,
			SentBy: laddr.String(),
			Method: string(sip.RequestMethodInvite),
		},
		req: newInInviteReq(tb, "UDP", branch, laddr, raddr),
	}
}

func (tx *startStubServerTx) Key() sip.ServerTransactionKey { return tx.key }
func (tx *startStubServerTx) Request() *sip.RequestEnvelope { return tx.req }

// startStubListener is a listener stub whose Serve optionally blocks on a
// gate and returns a configured error.
type startStubListener struct {
	md    sip.TransportMetadata
	laddr netip.AddrPort

	serveCalls atomic.Int32
	serveCtxs  chan context.Context
	serveDone  chan error
	gate       *lcGate
	serveErr   error
	// waitDone and resume switch Serve into cancellation-watch mode, see
	// startStubTx for the semantics.
	waitDone chan struct{}
	resume   chan struct{}
	// onReturn, when set, runs as Serve returns and lets a test author
	// ordering events exactly at the method return boundary.
	onReturn func()

	closed   atomic.Bool
	closeErr error
}

func newStartStubListener() *startStubListener {
	return &startStubListener{
		md:        sip.UDPMetadata(),
		laddr:     netip.MustParseAddrPort("127.0.0.1:5060"),
		serveCtxs: make(chan context.Context, 4),
		serveDone: make(chan error, 4),
	}
}

func (lis *startStubListener) Metadata() sip.TransportMetadata { return lis.md }
func (lis *startStubListener) LocalAddr() netip.AddrPort       { return lis.laddr }

func (lis *startStubListener) Serve(ctx context.Context) error {
	defer func() {
		if lis.onReturn != nil {
			lis.onReturn()
		}
	}()

	lis.serveCalls.Add(1)
	lis.serveCtxs <- ctx

	var err error
	switch {
	case lis.waitDone != nil:
		<-ctx.Done()
		close(lis.waitDone)
		err = ctx.Err()
		<-lis.resume
	case lis.gate != nil:
		lis.gate.enter()
		select {
		case <-lis.gate.release:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if err == nil {
		err = lis.serveErr
	}
	lis.serveDone <- err
	return errors.Wrap(err)
}

func (lis *startStubListener) Close() error {
	lis.closed.Store(true)
	return errors.Wrap(lis.closeErr)
}

// startStubTransport is a transport stub that delegates Listen to a hook so
// tests can block inside binds and capture the created listeners.
type startStubTransport struct {
	*proxyStubTransport

	listenCalls atomic.Int32
	listenFn    func(ctx context.Context, addr string) (sip.TransportListener, error)
}

func newStartStubTransport() *startStubTransport {
	return &startStubTransport{proxyStubTransport: newProxyStubTransport()}
}

func (tp *startStubTransport) Listen(ctx context.Context, addr string) (sip.TransportListener, error) {
	tp.listenCalls.Add(1)
	return tp.listenFn(ctx, addr)
}

// lcStartCloser records middleware Close calls and an optional hook for
// ordering assertions.
type lcStartCloser struct {
	closes  atomic.Int32
	closeFn func()
}

func (mw *lcStartCloser) Close() error {
	mw.closes.Add(1)
	if mw.closeFn != nil {
		mw.closeFn()
	}
	return nil
}

func TestElement_Start_StartsDormantTransactions(t *testing.T) {
	t.Parallel()

	type ctxKey struct{}
	ctx := context.WithValue(t.Context(), ctxKey{}, "start-ctx")

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	cln := newStartStubClientTx(sip.MagicCookie + ".cln-dormant")
	srv := newStartStubServerTx(t, sip.MagicCookie+".srv-dormant")
	preStarted := newStartStubClientTx(sip.MagicCookie + ".cln-prestarted")

	if err := elm.RegisterClientTransaction(ctx, cln); err != nil {
		t.Fatalf("elm.RegisterClientTransaction() error = %v, want nil", err)
	}
	if err := elm.RegisterServerTransaction(ctx, srv); err != nil {
		t.Fatalf("elm.RegisterServerTransaction() error = %v, want nil", err)
	}
	if err := elm.RegisterClientTransaction(ctx, preStarted); err != nil {
		t.Fatalf("elm.RegisterClientTransaction(pre) error = %v, want nil", err)
	}

	// A transaction started manually before element Start must be skipped.
	if err := preStarted.Start(ctx); err != nil {
		t.Fatalf("preStarted.Start() error = %v, want nil", err)
	}
	manualCtx := testutil.WaitResult(t, preStarted.startCtxs, "manual tx Start")
	if got, ok := sip.ElementFromContext(manualCtx); ok || got != nil {
		t.Fatalf("manual tx Start element = (%v, %t), want (nil, false)", got, ok)
	}

	if err := elm.Start(ctx); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}

	for name, tx := range map[string]*startStubTx{"client": cln.startStubTx, "server": srv.startStubTx} {
		startCtx := testutil.WaitResult(t, tx.startCtxs, name+" tx Start")
		assertElementOwner(t, startCtx, elm)
		if got := startCtx.Value(ctxKey{}); got != "start-ctx" {
			t.Errorf("%s tx Start ctx value = %v, want derived from Start ctx", name, got)
		}
		if err := testutil.WaitResult(t, tx.startDone, name+" tx Start result"); err != nil {
			t.Errorf("%s tx Start() error = %v, want nil", name, err)
		}
	}

	// Drain all managed goroutines, then assert the exact Start invocation
	// counts: dormant transactions exactly once, the pre-started one only
	// through its manual call (managed start skipped it entirely).
	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}
	for name, tx := range map[string]*startStubTx{"client": cln.startStubTx, "server": srv.startStubTx} {
		if got := tx.startCalls.Load(); got != 1 {
			t.Errorf("%s tx start calls = %d, want 1", name, got)
		}
	}
	if got := preStarted.startCalls.Load(); got != 1 {
		t.Errorf("pre-started tx start calls = %d, want only the manual 1", got)
	}
}

func TestElement_Start_ServesRetainedListeners(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	gate := newLcGate()
	t.Cleanup(gate.open)

	lisBlocked := newStartStubListener()
	lisBlocked.gate = gate
	lisFailed := newStartStubListener()
	lisFailed.serveErr = errStubRun

	liss := []*startStubListener{lisBlocked, lisFailed}
	var lisIdx atomic.Int32
	tp := newStartStubTransport()
	tp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
		return liss[lisIdx.Add(1)-1], nil
	}
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	if err := elm.ListenManaged(ctx, "UDP", "127.0.0.1:0"); err != nil {
		t.Fatalf("elm.ListenManaged() error = %v, want nil", err)
	}
	if err := elm.ListenManaged(ctx, "UDP", "127.0.0.1:0"); err != nil {
		t.Fatalf("elm.ListenManaged() error = %v, want nil", err)
	}

	// Binding prepares listeners without serving them.
	for i, lis := range liss {
		if got := lis.serveCalls.Load(); got != 0 {
			t.Fatalf("listener %d serve calls after bind = %d, want 0", i, got)
		}
	}

	errCh := make(chan error, 4)
	elm.BindErrorHandler(sip.ErrorHandlerFunc(func(_ context.Context, err error) {
		errCh <- err
	}))

	startDone := make(chan error, 1)
	go func() { startDone <- elm.Start(ctx) }()

	// Serve is running in the background while Start itself already returned.
	gate.waitEnter(t)
	if err := testutil.WaitResult(t, startDone, "elm.Start()"); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}

	// The failed listener reports a ListenerServeError while the blocked one
	// keeps serving undisturbed.
	err := testutil.WaitResult(t, errCh, "listener serve error")
	var lisErr *sip.ListenerServeError
	if !errors.As(err, &lisErr) {
		t.Fatalf("handler error = %v, want *sip.ListenerServeError", err)
	}
	if got := lisErr.LocalAddr(); got != lisFailed.laddr {
		t.Errorf("ListenerServeError.LocalAddr() = %v, want %v", got, lisFailed.laddr)
	}
	if diff := cmp.Diff(lisFailed.md, lisErr.Metadata()); diff != "" {
		t.Errorf("ListenerServeError.Metadata() mismatch (-want +got):\n%s", diff)
	}
	if !errors.Is(err, errStubRun) {
		t.Errorf("handler error = %v, want cause %v", err, errStubRun)
	}
	if got := lisBlocked.serveCalls.Load(); got != 1 {
		t.Errorf("blocked listener serve calls = %d, want 1 (still serving)", got)
	}
	if lisBlocked.closed.Load() {
		t.Error("blocked listener closed after sibling failure, want still serving")
	}

	// Releasing the gate ends serving quietly: nil results are not reported.
	gate.open()
	if err := testutil.WaitResult(t, lisBlocked.serveDone, "blocked Serve()"); err != nil {
		t.Errorf("blocked Serve() error = %v, want nil", err)
	}
}

func TestElement_BindErrorHandler(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	gate := newLcGate()
	t.Cleanup(gate.open)

	lis := newStartStubListener()
	lis.serveErr = errStubRun
	lis2 := newStartStubListener()
	lis2.gate = gate
	lis2.serveErr = errStubRun

	liss := []*startStubListener{lis, lis2}
	var lisIdx atomic.Int32
	tp := newStartStubTransport()
	tp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
		return liss[lisIdx.Add(1)-1], nil
	}
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}
	if err := elm.ListenManaged(ctx, "UDP", "127.0.0.1:5060"); err != nil {
		t.Fatalf("elm.ListenManaged() error = %v, want nil", err)
	}
	if err := elm.ListenManaged(ctx, "UDP", "127.0.0.1:5061"); err != nil {
		t.Fatalf("elm.ListenManaged() error = %v, want nil", err)
	}

	failingTx := newStartStubClientTx(sip.MagicCookie + ".cln-failing")
	failingTx.startErr = errStubRun
	if err := elm.RegisterClientTransaction(ctx, failingTx); err != nil {
		t.Fatalf("elm.RegisterClientTransaction() error = %v, want nil", err)
	}

	errCh1 := make(chan error, 4)
	errCh2 := make(chan error, 4)
	elm.BindErrorHandler(sip.ErrorHandlerFunc(func(_ context.Context, err error) { errCh1 <- err }))
	unbind2 := elm.BindErrorHandler(sip.ErrorHandlerFunc(func(_ context.Context, err error) { errCh2 <- err }))
	if unbind2 == nil {
		t.Fatal("elm.BindErrorHandler() returned nil unbind")
	}

	if err := elm.Start(ctx); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}

	// Startup reports the failing listener as a *ListenerServeError and the
	// failing transaction start as a wrapped error with the original cause.
	var gotLis, gotTx bool
	for range 2 {
		err := testutil.WaitResult(t, errCh1, "element error notification")
		var lisErr *sip.ListenerServeError
		if errors.As(err, &lisErr) {
			gotLis = true
			if got := lisErr.LocalAddr(); got != lis.laddr {
				t.Errorf("ListenerServeError.LocalAddr() = %v, want %v", got, lis.laddr)
			}
			if diff := cmp.Diff(lis.md, lisErr.Metadata()); diff != "" {
				t.Errorf("ListenerServeError.Metadata() mismatch (-want +got):\n%s", diff)
			}
			if !errors.Is(err, sip.ErrClassTransport) || !errors.Is(err, sip.ErrClassElement) {
				t.Errorf("serve error = %v, want transport and element classes", err)
			}
			if !strings.Contains(err.Error(), "UDP") || !strings.Contains(err.Error(), lis.laddr.String()) {
				t.Errorf("serve error text = %q, want proto and local addr", err.Error())
			}
		} else {
			gotTx = true
		}
		if !errors.Is(err, errStubRun) {
			t.Errorf("handler error = %v, want cause %v", err, errStubRun)
		}
	}
	if !gotLis {
		t.Error("no *ListenerServeError delivered, want listener serve failure reported")
	}
	if !gotTx {
		t.Error("no transaction start error delivered, want tx start failure reported")
	}

	// The second handler saw the same startup notifications.
	for range 2 {
		_ = testutil.WaitResult(t, errCh2, "second handler error notification")
	}

	// Unbind is idempotent and stops future notifications for that handler.
	unbind2()
	unbind2()

	// The gated listener fails late: still delivered to the bound handler
	// only.
	gate.open()
	if err := testutil.WaitResult(t, errCh1, "late listener serve error"); !errors.Is(err, errStubRun) {
		t.Fatalf("late serve error = %v, want cause %v", err, errStubRun)
	}
	select {
	case err := <-errCh2:
		t.Errorf("unbound handler received notification = %v", err)
	default:
	}

	// Errors of listeners served manually outside the element are not
	// reported to element handlers.
	directLis := newStartStubListener()
	directLis.serveErr = errStubRun
	tp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
		return directLis, nil
	}
	unmanaged, err := tp.Listen(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tp.Listen() error = %v, want nil", err)
	}
	if err := unmanaged.Serve(ctx); !errors.Is(err, errStubRun) {
		t.Fatalf("unmanaged Serve() error = %v, want %v", err, errStubRun)
	}
	select {
	case err := <-errCh1:
		t.Errorf("element handler notified of unmanaged serve error = %v", err)
	default:
	}
}

func TestElement_Start_ConcurrentStartsSingleWinner(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	const starters = 8
	var wg sync.WaitGroup
	errs := make(chan error, starters)
	for range starters {
		wg.Go(func() { errs <- elm.Start(ctx) })
	}
	wg.Wait()
	close(errs)

	var nilCnt, conflictCnt int
	for err := range errs {
		switch {
		case err == nil:
			nilCnt++
		case errors.Is(err, sip.ErrElementStarted):
			conflictCnt++
		default:
			t.Errorf("concurrent elm.Start() error = %v, want nil or %v", err, sip.ErrElementStarted)
		}
	}
	if nilCnt != 1 || conflictCnt != starters-1 {
		t.Errorf("concurrent elm.Start() results: nil = %d, conflict = %d, want 1 and %d", nilCnt, conflictCnt, starters-1)
	}

	if err := elm.Start(ctx); !errors.Is(err, sip.ErrElementStarted) {
		t.Errorf("repeated elm.Start() error = %v, want %v", err, sip.ErrElementStarted)
	}
}

func TestElement_ListenManaged_RejectedAfterStart(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	tp := newStartStubTransport()
	tp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
		return newStartStubListener(), nil
	}
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	if err := elm.Start(ctx); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}

	if err := elm.ListenManaged(ctx, "UDP", "127.0.0.1:0"); !errors.Is(err, sip.ErrElementStarted) {
		t.Fatalf("elm.ListenManaged() after Start error = %v, want %v", err, sip.ErrElementStarted)
	}
	if got := tp.listenCalls.Load(); got != 0 {
		t.Errorf("transport Listen calls after Start = %d, want 0 (no bind attempted)", got)
	}
}

func TestElement_Start_PreCanceledContextDoesNotConsume(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	tx := newStartStubClientTx(sip.MagicCookie + ".cln-canceled")
	if err := elm.RegisterClientTransaction(ctx, tx); err != nil {
		t.Fatalf("elm.RegisterClientTransaction() error = %v, want nil", err)
	}

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := elm.Start(canceledCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("elm.Start(canceled) error = %v, want %v", err, context.Canceled)
	}

	// The one-shot start was not consumed: a healthy call still starts work.
	if err := elm.Start(ctx); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}
	_ = testutil.WaitResult(t, tx.startCtxs, "client tx Start")
}

func TestElement_Start_AfterCloseRejected(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}

	if err := elm.Start(ctx); !errors.Is(err, sip.ErrElementClosed) {
		t.Errorf("elm.Start() after close error = %v, want %v", err, sip.ErrElementClosed)
	}
	if unbind := elm.BindErrorHandler(sip.ErrorHandlerFunc(func(context.Context, error) {})); unbind == nil {
		t.Error("elm.BindErrorHandler() after close returned nil unbind, want no-op")
	}
}

func TestElement_Start_StartupCancellationDoesNotStopRuntime(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	// The startup context carries a value and a deadline and is canceled
	// explicitly below: it only gates Start admission, never the runtime.
	type ctxKey struct{}
	startupCtx, cancel := context.WithTimeout(
		context.WithValue(ctx, ctxKey{}, "startup-ctx"), time.Hour,
	)
	t.Cleanup(cancel)

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	gate := newLcGate()
	t.Cleanup(gate.open)

	lis := newStartStubListener()
	lis.gate = gate
	lis.serveErr = errStubRun
	tp := newStartStubTransport()
	tp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
		return lis, nil
	}
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}
	if err := elm.ListenManaged(ctx, "UDP", "127.0.0.1:0"); err != nil {
		t.Fatalf("elm.ListenManaged() error = %v, want nil", err)
	}

	tx := newStartStubClientTx(sip.MagicCookie + ".cln-detached")
	tx.gate = gate
	tx.startErr = errStubRun
	if err := elm.RegisterClientTransaction(ctx, tx); err != nil {
		t.Fatalf("elm.RegisterClientTransaction() error = %v, want nil", err)
	}

	errCh := make(chan error, 4)
	elm.BindErrorHandler(sip.ErrorHandlerFunc(func(_ context.Context, err error) {
		errCh <- err
	}))

	if err := elm.Start(startupCtx); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}

	// Both managed units run under their own detached runtime context.
	txRunCtx := testutil.WaitResult(t, tx.startCtxs, "client tx Start")
	lisRunCtx := testutil.WaitResult(t, lis.serveCtxs, "listener Serve")

	cancel()
	if err := startupCtx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("startup ctx error = %v, want %v", err, context.Canceled)
	}

	for name, runCtx := range map[string]context.Context{"tx": txRunCtx, "listener": lisRunCtx} {
		if err := runCtx.Err(); err != nil {
			t.Errorf("%s runtime ctx error = %v, want nil (detached from startup ctx)", name, err)
		}
		if dl, ok := runCtx.Deadline(); ok {
			t.Errorf("%s runtime ctx deadline = %v, want none", name, dl)
		}
		if got := runCtx.Value(ctxKey{}); got != "startup-ctx" {
			t.Errorf("%s runtime ctx value = %v, want inherited startup ctx value", name, got)
		}
		select {
		case <-runCtx.Done():
			t.Errorf("%s runtime ctx done, want still open", name)
		default:
		}
	}

	// The workers are still blocked: canceling the startup ctx did not end them.
	select {
	case err := <-tx.startDone:
		t.Fatalf("tx Start() returned %v after startup ctx cancel, want still blocked", err)
	case err := <-lis.serveDone:
		t.Fatalf("Serve() returned %v after startup ctx cancel, want still blocked", err)
	default:
	}

	// Failures after the startup ctx was canceled are still delivered.
	gate.open()

	var gotLis, gotTx bool
	for range 2 {
		err := testutil.WaitResult(t, errCh, "element error notification")
		var lisErr *sip.ListenerServeError
		if errors.As(err, &lisErr) {
			gotLis = true
			if got := lisErr.LocalAddr(); got != lis.laddr {
				t.Errorf("ListenerServeError.LocalAddr() = %v, want %v", got, lis.laddr)
			}
			if diff := cmp.Diff(lis.md, lisErr.Metadata()); diff != "" {
				t.Errorf("ListenerServeError.Metadata() mismatch (-want +got):\n%s", diff)
			}
		} else {
			gotTx = true
		}
		if !errors.Is(err, errStubRun) {
			t.Errorf("handler error = %v, want cause %v", err, errStubRun)
		}
	}
	if !gotLis {
		t.Error("no *ListenerServeError delivered after startup ctx cancel, want listener failure reported")
	}
	if !gotTx {
		t.Error("no transaction start error delivered after startup ctx cancel, want tx failure reported")
	}

	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}
}

func TestElement_Start_CloseCancelsAndDrainsManagedWork(t *testing.T) {
	t.Parallel()

	// The parent context stays alive for the whole test: only the managed
	// cancellation done by Close itself may unblock the workers.
	ctx := t.Context()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	events := &lcEventLog{}
	mw := &lcStartCloser{closeFn: func() { events.add("mw:closed") }}
	elm.UseMiddleware(mw)

	lis := newStartStubListener()
	lis.waitDone = make(chan struct{})
	lis.resume = make(chan struct{})
	lis.onReturn = func() { events.add("lis:done") }
	tp := newStartStubTransport()
	tp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
		return lis, nil
	}
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}
	if err := elm.ListenManaged(ctx, "UDP", "127.0.0.1:0"); err != nil {
		t.Fatalf("elm.ListenManaged() error = %v, want nil", err)
	}

	tx := newStartStubClientTx(sip.MagicCookie + ".cln-managed")
	tx.waitDone = make(chan struct{})
	tx.resume = make(chan struct{})
	tx.onReturn = func() { events.add("tx:done") }
	if err := elm.RegisterClientTransaction(ctx, tx); err != nil {
		t.Fatalf("elm.RegisterClientTransaction() error = %v, want nil", err)
	}

	// Release the post-cancellation barriers on every test path.
	release := sync.OnceFunc(func() {
		close(tx.resume)
		close(lis.resume)
	})
	t.Cleanup(release)

	var handlerCalls atomic.Int32
	elm.BindErrorHandler(sip.ErrorHandlerFunc(func(context.Context, error) {
		handlerCalls.Add(1)
	}))

	if err := elm.Start(ctx); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}

	// Both managed units are running and blocked on their managed context.
	_ = testutil.WaitResult(t, tx.startCtxs, "client tx Start")
	_ = testutil.WaitResult(t, lis.serveCtxs, "listener Serve")

	closeDone := make(chan error, 1)
	go func() { closeDone <- elm.Close() }()

	// Close must cancel the managed context itself while the parent is live.
	testutil.WaitClosed(t, tx.waitDone, "tx Start ctx.Done")
	testutil.WaitClosed(t, lis.waitDone, "Serve ctx.Done")
	if err := ctx.Err(); err != nil {
		t.Fatalf("parent ctx done = %v, want still alive", err)
	}

	// Close stays blocked until both background methods actually return:
	// middleware shutdown cannot begin while they are held on resume.
	if got := mw.closes.Load(); got != 0 {
		t.Fatalf("mw.closes = %d while managed work is held, want 0", got)
	}
	select {
	case err := <-closeDone:
		t.Fatalf("elm.Close() returned %v before managed work completed", err)
	default:
	}

	release()

	if err := testutil.WaitResult(t, tx.startDone, "tx Start return"); !errors.Is(err, context.Canceled) {
		t.Errorf("tx Start() after Close error = %v, want %v", err, context.Canceled)
	}
	if err := testutil.WaitResult(t, lis.serveDone, "Serve return"); !errors.Is(err, context.Canceled) {
		t.Errorf("Serve() after Close error = %v, want %v", err, context.Canceled)
	}

	if err := testutil.WaitResult(t, closeDone, "elm.Close()"); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}
	if got := mw.closes.Load(); got != 1 {
		t.Errorf("mw.closes = %d, want 1", got)
	}
	if closedIdx, txIdx, lisIdx := events.index("mw:closed"), events.index("tx:done"), events.index("lis:done"); closedIdx <= txIdx || closedIdx <= lisIdx {
		t.Errorf("event order tx:done=%d lis:done=%d mw:closed=%d, want middleware closed last", txIdx, lisIdx, closedIdx)
	}
	if got := handlerCalls.Load(); got != 0 {
		t.Errorf("error handler calls = %d, want 0 for managed Close cancellation", got)
	}
}

func TestElement_Close_DrainsErrorCallbacksBeforeMiddlewareClose(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	events := &lcEventLog{}
	mw := &lcStartCloser{closeFn: func() { events.add("mw:closed") }}
	elm.UseMiddleware(mw)

	lis := newStartStubListener()
	lis.serveErr = errStubRun
	tp := newStartStubTransport()
	tp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
		return lis, nil
	}
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}
	if err := elm.ListenManaged(ctx, "UDP", "127.0.0.1:0"); err != nil {
		t.Fatalf("elm.ListenManaged() error = %v, want nil", err)
	}

	handlerGate := newLcGate()
	t.Cleanup(handlerGate.open)
	elm.BindErrorHandler(sip.ErrorHandlerFunc(func(context.Context, error) {
		handlerGate.enter()
		<-handlerGate.release
		events.add("handler:done")
	}))

	if err := elm.Start(ctx); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}
	handlerGate.waitEnter(t)

	closeDone := make(chan error, 1)
	go func() { closeDone <- elm.Close() }()
	waitElementClosing(t, elm)

	// The blocked error callback holds a callback lease, so Close cannot
	// reach middleware shutdown until it returns.
	if got := mw.closes.Load(); got != 0 {
		t.Fatalf("mw.closes = %d while error callback is in flight, want 0", got)
	}

	handlerGate.open()
	if err := testutil.WaitResult(t, closeDone, "elm.Close()"); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}

	if got := mw.closes.Load(); got != 1 {
		t.Errorf("mw.closes = %d, want 1", got)
	}
	if doneIdx, closedIdx := events.index("handler:done"), events.index("mw:closed"); doneIdx < 0 || closedIdx <= doneIdx {
		t.Errorf("event order handler:done=%d mw:closed=%d, want handler drained first", doneIdx, closedIdx)
	}
}

func TestElement_ListenManaged_LateBoundListenerClosedOnStartRace(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	bindGate := newLcGate()
	t.Cleanup(bindGate.open)

	var bound atomic.Pointer[startStubListener]
	tp := newStartStubTransport()
	tp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
		bindGate.enter()
		<-bindGate.release
		lis := newStartStubListener()
		bound.Store(lis)
		return lis, nil
	}
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	bindDone := make(chan error, 1)
	go func() { bindDone <- elm.ListenManaged(ctx, "UDP", "127.0.0.1:0") }()
	bindGate.waitEnter(t)

	// Start consumes the one-shot while the bind is still blocked; the
	// listener snapshot does not include the in-flight bind.
	if err := elm.Start(ctx); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}

	bindGate.open()
	if err := testutil.WaitResult(t, bindDone, "elm.ListenManaged()"); !errors.Is(err, sip.ErrElementStarted) {
		t.Fatalf("racing elm.ListenManaged() error = %v, want %v", err, sip.ErrElementStarted)
	}

	lis := bound.Load()
	if lis == nil {
		t.Fatal("bind did not produce a listener")
	}
	if !lis.closed.Load() {
		t.Error("late-bound listener not closed after losing the Start race")
	}
	if got := lis.serveCalls.Load(); got != 0 {
		t.Errorf("late-bound listener serve calls = %d, want 0", got)
	}
}

func TestElement_ListenManaged_LateBoundListenerClosedOnCloseRace(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	bindGate := newLcGate()
	t.Cleanup(bindGate.open)

	var bound atomic.Pointer[startStubListener]
	tp := newStartStubTransport()
	tp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
		bindGate.enter()
		<-bindGate.release
		lis := newStartStubListener()
		bound.Store(lis)
		return lis, nil
	}
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	bindDone := make(chan error, 1)
	go func() { bindDone <- elm.ListenManaged(ctx, "UDP", "127.0.0.1:0") }()
	bindGate.waitEnter(t)

	closeDone := make(chan error, 1)
	go func() { closeDone <- elm.Close() }()
	waitElementClosing(t, elm)

	bindGate.open()
	if err := testutil.WaitResult(t, bindDone, "elm.ListenManaged()"); !errors.Is(err, sip.ErrElementClosed) {
		t.Fatalf("racing elm.ListenManaged() error = %v, want %v", err, sip.ErrElementClosed)
	}
	if err := testutil.WaitResult(t, closeDone, "elm.Close()"); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}

	lis := bound.Load()
	if lis == nil {
		t.Fatal("bind did not produce a listener")
	}
	if !lis.closed.Load() {
		t.Error("late-bound listener not closed after losing the Close race")
	}
	if got := lis.serveCalls.Load(); got != 0 {
		t.Errorf("late-bound listener serve calls = %d, want 0", got)
	}
}

func TestElement_Start_LateRegistrationsNotStarted(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	tp := newStartStubTransport()
	tp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
		return newStartStubListener(), nil
	}
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	if err := elm.Start(ctx); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}

	// Transactions registered after Start are not auto-started.
	tx := newStartStubClientTx(sip.MagicCookie + ".cln-late")
	if err := elm.RegisterClientTransaction(ctx, tx); err != nil {
		t.Fatalf("elm.RegisterClientTransaction() after Start error = %v, want nil", err)
	}
	if got := tx.startCalls.Load(); got != 0 {
		t.Fatalf("late-registered tx start calls = %d, want 0", got)
	}

	// The granular manual path is unaffected: explicit tx.Start and
	// Listen + lis.Serve still work.
	if err := tx.Start(ctx); err != nil {
		t.Fatalf("manual tx.Start() error = %v, want nil", err)
	}

	lis, err := elm.Listen(ctx, "UDP", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("elm.Listen() error = %v, want nil", err)
	}
	stubLis, ok := lis.(*startStubListener)
	if !ok {
		t.Fatalf("elm.Listen() listener type = %T, want *startStubListener", lis)
	}
	if err := lis.Serve(ctx); err != nil {
		t.Fatalf("manual lis.Serve() error = %v, want nil", err)
	}
	if got := stubLis.serveCalls.Load(); got != 1 {
		t.Errorf("manually served listener serve calls = %d, want 1", got)
	}
}

func TestListenerServeError_ValueSemantics(t *testing.T) {
	t.Parallel()

	addr := netip.MustParseAddrPort("127.0.0.1:5060")
	md := sip.UDPMetadata()
	cause := errStubRun

	err := sip.NewListenerServeError(addr, md, cause)

	var lisErr *sip.ListenerServeError
	if !errors.As(err, &lisErr) {
		t.Fatalf("NewListenerServeError() = %v, want *sip.ListenerServeError", err)
	}
	if !errors.Is(err, cause) {
		t.Errorf("NewListenerServeError() unwrap = %v, want cause %v", err, cause)
	}
	if !errors.Is(err, sip.ErrClassTransport) || !errors.Is(err, sip.ErrClassElement) {
		t.Errorf("NewListenerServeError() = %v, want transport and element classes", err)
	}

	// The constructor copies its arguments: later mutations of the source
	// variables do not change the stored values or the rendered text.
	addr = netip.MustParseAddrPort("10.0.0.1:5061")
	md.Proto = "TCP"
	if lisErr.LocalAddr() == addr || lisErr.Metadata() == md {
		t.Error("lisErr follows mutated source variables, want independent copies")
	}

	if got := lisErr.LocalAddr(); got != netip.MustParseAddrPort("127.0.0.1:5060") {
		t.Errorf("lisErr.LocalAddr() = %v, want %v", got, netip.MustParseAddrPort("127.0.0.1:5060"))
	}
	if diff := cmp.Diff(sip.UDPMetadata(), lisErr.Metadata()); diff != "" {
		t.Errorf("lisErr.Metadata() mismatch (-want +got):\n%s", diff)
	}
	if got := lisErr.Error(); !strings.Contains(got, "UDP:127.0.0.1:5060") {
		t.Errorf("lisErr.Error() = %q, want original proto and addr", got)
	}

	// Mutating a returned metadata copy does not affect the stored one.
	gotMd := lisErr.Metadata()
	gotMd.Proto = "WS"
	if lisErr.Metadata() == gotMd {
		t.Error("lisErr.Metadata() shares state with the mutated returned copy")
	}
	if got := lisErr.Metadata(); got.Proto != "UDP" {
		t.Errorf("lisErr.Metadata().Proto = %v after mutating the returned copy, want UDP", got.Proto)
	}

	// A nil error value stays usable and renders a stable placeholder.
	var nilErr *sip.ListenerServeError
	if got := nilErr.LocalAddr(); got != (netip.AddrPort{}) {
		t.Errorf("nil LocalAddr() = %v, want zero AddrPort", got)
	}
	if diff := cmp.Diff(sip.TransportMetadata{}, nilErr.Metadata()); diff != "" {
		t.Errorf("nil Metadata() mismatch (-want +got):\n%s", diff)
	}
	if got := nilErr.Unwrap(); got != nil {
		t.Errorf("nil Unwrap() = %v, want nil", got)
	}
	if got := nilErr.Error(); got != "<nil>" {
		t.Errorf("nil Error() = %q, want %q", got, "<nil>")
	}
}

// elemLateBindTransport gates Listen of a real UDP transport after the
// underlying bind, so a test can interleave lifecycle calls mid-bind.
type elemLateBindTransport struct {
	*transport.ConnectionLessTransport
	bound   chan sip.TransportListener
	release <-chan struct{}
}

func (tp *elemLateBindTransport) Listen(ctx context.Context, addr string) (sip.TransportListener, error) {
	lis, err := tp.ConnectionLessTransport.Listen(ctx, addr)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	tp.bound <- lis
	<-tp.release
	return lis, nil
}

// A bind rolled back by a lost Start race must also drop the listener's
// registry record, not only close it.
func TestElement_ListenManaged_LateBoundListenerDiscarded(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	udp, err := transport.NewConnectionLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	tp := &elemLateBindTransport{
		ConnectionLessTransport: udp,
		bound:                   make(chan sip.TransportListener, 1),
		release:                 release,
	}

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })
	t.Cleanup(unblock)

	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	bindDone := make(chan error, 1)
	go func() { bindDone <- elm.ListenManaged(ctx, "UDP", "127.0.0.1:0") }()

	lis := testutil.WaitResult(t, tp.bound, "underlying UDP bind")
	if err := elm.Start(ctx); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}
	unblock()

	if err := testutil.WaitResult(t, bindDone, "late ListenManaged result"); !errors.Is(err, sip.ErrElementStarted) {
		t.Fatalf("elm.ListenManaged() error = %v, want %v", err, sip.ErrElementStarted)
	}

	// The rolled-back listener was closed and dropped from the registry, so
	// an exact local-address lookup must not resolve it anymore.
	conn, err := udp.AcquireConnection(
		ctx,
		netip.MustParseAddrPort("127.0.0.1:50999"),
		transport.AcquireConnectionOptions{LocalAddr: lis.LocalAddr()},
	)
	if !errors.Is(err, transport.ErrConnectionNotFound) {
		t.Fatalf("udp.AcquireConnection() = (%v, %v), want %v", conn, err, transport.ErrConnectionNotFound)
	}
}

// elemLisObserveTransport serves a gated listener via Listen and records
// the error state of a watched context at the CANCEL send boundary.
type elemLisObserveTransport struct {
	*lcRecordingTransport
	lis     *startStubListener
	watched context.Context
	ctxErrs chan error
}

func (tp *elemLisObserveTransport) Listen(context.Context, string) (sip.TransportListener, error) {
	return tp.lis, nil
}

func (tp *elemLisObserveTransport) SendRequest(
	ctx context.Context,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	if req.Method().Equal(sip.RequestMethodCancel) && tp.watched != nil {
		tp.ctxErrs <- errors.Wrap(tp.watched.Err())
	}
	return errors.Wrap(tp.lcRecordingTransport.SendRequest(ctx, req, opts...))
}

// The best-effort CANCEL must be sent while the detached runtime context is
// still live; its cancellation happens afterwards inside Close.
func TestElement_Close_CancelsRuntimeAfterProceedingInviteCancel(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	stubLis := newStartStubListener()
	stubLis.gate = newLcGate()
	t.Cleanup(stubLis.gate.open)

	tp := &elemLisObserveTransport{
		lcRecordingTransport: newLcRecordingTransport(),
		lis:                  stubLis,
		ctxErrs:              make(chan error, 2),
	}

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}
	if err := elm.ListenManaged(ctx, "UDP", "127.0.0.1:5060"); err != nil {
		t.Fatalf("elm.ListenManaged() error = %v, want nil", err)
	}

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	req := newInInviteReq(t, "UDP", "", laddr, raddr)

	tx, err := elm.NewClientTransaction(ctx, req, tp)
	if err != nil {
		t.Fatalf("elm.NewClientTransaction() error = %v, want nil", err)
	}
	startClientTransaction(t, tx)

	// Drive the INVITE transaction to proceeding so shutdown sends a CANCEL.
	if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusRinging)); err != nil {
		t.Fatalf("tx.RecvResponse() error = %v, want nil", err)
	}
	if got := tx.State(); got != sip.TransactionStateProceeding {
		t.Fatalf("tx.State() = %v, want %v", got, sip.TransactionStateProceeding)
	}

	// The managed start skips the already-started transaction and only
	// launches the gated listener serve.
	if err := elm.Start(ctx); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}
	runCtx := testutil.WaitResult(t, stubLis.serveCtxs, "listener serve context")
	stubLis.gate.waitEnter(t)
	tp.watched = runCtx

	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}

	// The CANCEL was sent while the detached runtime context was still live;
	// its cancellation happened afterwards inside Close.
	if got := testutil.WaitResult(t, tp.ctxErrs, "runtime context state at CANCEL send"); got != nil {
		t.Errorf("runCtx.Err() at CANCEL send = %v, want nil", got)
	}
	if got := runCtx.Err(); !errors.Is(got, context.Canceled) {
		t.Errorf("runCtx.Err() after Close = %v, want %v", got, context.Canceled)
	}
	if err := testutil.WaitResult(t, stubLis.serveDone, "listener serve result"); !errors.Is(err, context.Canceled) {
		t.Errorf("lis.Serve() error = %v, want %v", err, context.Canceled)
	}
}

// Untracking while ready prunes prepared listeners from the launch set;
// they are neither served nor closed and stay caller-owned.
func TestElement_UntrackTransport_ReadyPrunesPreparedListeners(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	stubLis := newStartStubListener()
	stubTp := newStartStubTransport()
	stubTp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
		return stubLis, nil
	}

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })
	if err := elm.TrackTransport(stubTp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}
	if err := elm.ListenManaged(ctx, "UDP", "127.0.0.1:5060"); err != nil {
		t.Fatalf("elm.ListenManaged() error = %v, want nil", err)
	}

	if err := elm.UntrackTransport(stubTp); err != nil {
		t.Fatalf("elm.UntrackTransport() error = %v, want nil", err)
	}

	if err := elm.Start(ctx); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}
	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}

	// The pruned listener was never served or closed by the element; the
	// caller still owns it and can serve it directly in granular mode.
	if got := stubLis.serveCalls.Load(); got != 0 {
		t.Errorf("pruned listener serve calls = %d, want 0", got)
	}
	if stubLis.closed.Load() {
		t.Error("pruned listener closed by the element, want caller-owned")
	}
	if err := stubLis.Serve(ctx); err != nil {
		t.Errorf("manual lis.Serve() error = %v, want nil", err)
	}
	if got := stubLis.serveCalls.Load(); got != 1 {
		t.Errorf("manual lis.Serve() calls = %d, want 1", got)
	}
}

// Untracking is only possible while the element is ready.
func TestElement_UntrackTransport_RejectedAfterStartAndClose(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	tp := newProxyStubTransport()
	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}
	if err := elm.Start(ctx); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}

	if err := elm.UntrackTransport(tp); !errors.Is(err, sip.ErrElementStarted) {
		t.Fatalf("elm.UntrackTransport() error = %v, want %v", err, sip.ErrElementStarted)
	}
	if got, ok := elm.TransportByProto("UDP"); !ok || got != tp {
		t.Fatalf("elm.TransportByProto() = (%v, %v) after rejected untrack, want the tracked transport", got, ok)
	}

	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}
	if err := elm.UntrackTransport(tp); !errors.Is(err, sip.ErrElementClosed) {
		t.Fatalf("elm.UntrackTransport() after close error = %v, want %v", err, sip.ErrElementClosed)
	}
}

// A bind still in flight when its protocol is untracked discards the fresh
// listener and fails with ErrNoTransport, even if the protocol is tracked
// again before the bind returns.
func TestElement_UntrackTransport_InFlightBindDiscarded(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	bound := make(chan *startStubListener, 1)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })

	stubTp := newStartStubTransport()
	stubTp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
		lis := newStartStubListener()
		bound <- lis
		<-release
		return lis, nil
	}

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })
	// The gate must open before the element closes, or Close waits forever
	// for the blocked bind on an early failure.
	t.Cleanup(unblock)
	if err := elm.TrackTransport(stubTp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	bindDone := make(chan error, 1)
	go func() { bindDone <- elm.ListenManaged(ctx, "UDP", "127.0.0.1:0") }()
	lis := testutil.WaitResult(t, bound, "in-flight bind")

	// Untrack while the bind is blocked, then immediately track a
	// replacement transport on the same protocol: the pending bind still
	// loses because its untrack generation changed.
	if err := elm.UntrackTransport(stubTp); err != nil {
		t.Fatalf("elm.UntrackTransport() error = %v, want nil", err)
	}
	stubLis2 := newStartStubListener()
	newTp := newStartStubTransport()
	newTp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
		return stubLis2, nil
	}
	if err := elm.TrackTransport(newTp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}
	unblock()

	if err := testutil.WaitResult(t, bindDone, "late ListenManaged result"); !errors.Is(err, sip.ErrNoTransport) {
		t.Fatalf("elm.ListenManaged() error = %v, want %v", err, sip.ErrNoTransport)
	}
	if !lis.closed.Load() {
		t.Error("late-bound listener was not closed after the bind rollback")
	}
	if got := lis.serveCalls.Load(); got != 0 {
		t.Errorf("discarded listener serve calls = %d, want 0", got)
	}

	// A listener prepared on the replacement transport is retained and
	// served by managed startup.
	if err := elm.ListenManaged(ctx, "UDP", "127.0.0.1:5060"); err != nil {
		t.Fatalf("elm.ListenManaged() on replacement error = %v, want nil", err)
	}
	if err := elm.Start(ctx); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}
	if err := testutil.WaitResult(t, stubLis2.serveDone, "replacement listener serve"); err != nil {
		t.Fatalf("replacement lis.Serve() error = %v, want nil", err)
	}
	if got := stubLis2.serveCalls.Load(); got != 1 {
		t.Errorf("replacement listener serve calls = %d, want 1", got)
	}
}

// elemGatedMetadataTransport gates Metadata calls once armed: the second
// counted call blocks until the gate opens, which is the lookup the
// transport manager makes inside its own lock during UntrackTransport.
type elemGatedMetadataTransport struct {
	*startStubTransport
	gate  *lcGate
	armed atomic.Bool
	calls atomic.Int32
}

func (tp *elemGatedMetadataTransport) Metadata() sip.TransportMetadata {
	if tp.armed.Load() && tp.calls.Add(1) == 2 {
		tp.gate.enter()
		<-tp.gate.release
	}
	return tp.md
}

// A bind must be rejected while a detachment of the same protocol is still
// inside the manager call, before it ever reaches the transport.
func TestElement_UntrackTransport_RejectsListenDuringDetachment(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	tp := &elemGatedMetadataTransport{
		startStubTransport: newStartStubTransport(),
		gate:               newLcGate(),
	}
	tp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
		return newStartStubListener(), nil
	}

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })
	// The gate must open before the element closes, or Close waits forever
	// on the manager lock the blocked untrack holds.
	t.Cleanup(tp.gate.open)

	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}
	// Count only calls made during UntrackTransport: the element's own proto
	// lookup is first, the manager's lookup under its lock is gated second.
	tp.armed.Store(true)

	untrackDone := make(chan error, 1)
	go func() { untrackDone <- elm.UntrackTransport(tp) }()
	tp.gate.waitEnter(t)

	bindDone := make(chan error, 1)
	go func() { bindDone <- elm.ListenManaged(ctx, "UDP", "127.0.0.1:0") }()
	if err := testutil.WaitResult(t, bindDone, "ListenManaged during detachment"); !errors.Is(err, sip.ErrNoTransport) {
		t.Fatalf("elm.ListenManaged() error = %v, want %v", err, sip.ErrNoTransport)
	}
	if got := tp.listenCalls.Load(); got != 0 {
		t.Errorf("transport.Listen calls during detachment = %d, want 0", got)
	}

	tp.gate.open()
	if err := testutil.WaitResult(t, untrackDone, "elm.UntrackTransport()"); err != nil {
		t.Fatalf("elm.UntrackTransport() error = %v, want nil", err)
	}

	// After the detachment finished, a replacement on the same protocol
	// binds and retains listeners normally.
	stubLis := newStartStubListener()
	newTp := newStartStubTransport()
	newTp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
		return stubLis, nil
	}
	if err := elm.TrackTransport(newTp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}
	if err := elm.ListenManaged(ctx, "UDP", "127.0.0.1:5060"); err != nil {
		t.Fatalf("elm.ListenManaged() on replacement error = %v, want nil", err)
	}
}

// A listener bound through Listen is caller-served: Start never serves
// it and a canceled bind context does not leak into the manual Serve.
func TestElement_Listen_NotManagedByStart(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	stubLis := newStartStubListener()
	stubLis.serveErr = errStubRun

	bindCtxs := make(chan context.Context, 1)
	bindAddrs := make(chan string, 1)
	stubTp := newStartStubTransport()
	stubTp.listenFn = func(c context.Context, addr string) (sip.TransportListener, error) {
		bindCtxs <- c
		bindAddrs <- addr
		return stubLis, nil
	}

	var hndlrCalls atomic.Int32
	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })
	elm.BindErrorHandler(sip.ErrorHandlerFunc(func(context.Context, error) {
		hndlrCalls.Add(1)
	}))
	if err := elm.TrackTransport(stubTp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	bindPar, cancelBind := context.WithCancel(context.Background())
	lis, err := elm.Listen(bindPar, "UDP", "127.0.0.1:5060")
	if err != nil {
		t.Fatalf("elm.Listen() error = %v, want nil", err)
	}
	cancelBind()

	if lis != stubLis {
		t.Fatalf("elm.Listen() = %p, want the transport's listener %p", lis, stubLis)
	}
	if got := testutil.WaitResult(t, bindCtxs, "transport listen context"); got != nil {
		assertElementOwner(t, got, elm)
		if got.Done() != bindPar.Done() || !errors.Is(got.Err(), bindPar.Err()) {
			t.Errorf("transport Listen ctx cancellation = (%v, %v), want caller cancellation", got.Done(), got.Err())
		}
	} else {
		t.Error("transport Listen ctx = nil, want derived bind context")
	}
	if got := testutil.WaitResult(t, bindAddrs, "transport listen addr"); got != "127.0.0.1:5060" {
		t.Errorf("transport Listen addr = %q, want %q", got, "127.0.0.1:5060")
	}
	if got := stubLis.serveCalls.Load(); got != 0 {
		t.Fatalf("listener serve calls after bind = %d, want 0", got)
	}

	if err := elm.Start(ctx); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}
	if got := stubLis.serveCalls.Load(); got != 0 {
		t.Fatalf("listener serve calls after Start = %d, want 0 (granular listener)", got)
	}

	// The canceled bind context does not reach the manual Serve, which gets
	// exactly the caller-supplied context and reports errors to the caller
	// only, never to the element handlers.
	serveCtx := t.Context()
	if err := lis.Serve(serveCtx); !errors.Is(err, errStubRun) {
		t.Fatalf("manual lis.Serve() error = %v, want %v", err, errStubRun)
	}
	if got := testutil.WaitResult(t, stubLis.serveCtxs, "listener serve context"); got != serveCtx {
		t.Errorf("lis.Serve() ctx = %p, want the caller's %p", got, serveCtx)
	}

	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}
	if got := stubLis.serveCalls.Load(); got != 1 {
		t.Errorf("listener serve calls after Close = %d, want exactly 1", got)
	}
	if got := hndlrCalls.Load(); got != 0 {
		t.Errorf("element error handler calls = %d, want 0", got)
	}
}

// Bind failures are returned synchronously and never reach the bound error
// handlers.
func TestElement_Listen_BindErrors(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	tests := []struct {
		name  string
		track bool
		err   error
		want  error
	}{
		{name: "missing protocol", want: sip.ErrNoTransport},
		{name: "transport listen failure", track: true, err: errStubRun, want: errStubRun},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var hndlrCalls atomic.Int32
			elm := &sip.Element{}
			t.Cleanup(func() { _ = elm.Close() })
			elm.BindErrorHandler(sip.ErrorHandlerFunc(func(context.Context, error) {
				hndlrCalls.Add(1)
			}))

			if tt.track {
				stubTp := newStartStubTransport()
				stubTp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
					return nil, errors.Wrap(tt.err)
				}
				if err := elm.TrackTransport(stubTp); err != nil {
					t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
				}
			}

			lis, err := elm.Listen(ctx, "UDP", "127.0.0.1:0")
			if !errors.Is(err, tt.want) {
				t.Fatalf("elm.Listen() error = %v, want %v", err, tt.want)
			}
			if lis != nil {
				t.Errorf("elm.Listen() listener = %v, want nil", lis)
			}
			if got := hndlrCalls.Load(); got != 0 {
				t.Errorf("element error handler calls = %d, want 0", got)
			}
		})
	}
}

// Managed Start is optional: a fully granular application drives each
// transaction and listener itself in its own order.
func TestElement_GranularStartup(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	stubLis := newStartStubListener()
	stubTp := newStartStubTransport()
	stubTp.listenFn = func(context.Context, string) (sip.TransportListener, error) {
		return stubLis, nil
	}

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })
	if err := elm.TrackTransport(stubTp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	cln := newStartStubClientTx(sip.MagicCookie + ".cln-granular")
	srv := newStartStubServerTx(t, sip.MagicCookie+".srv-granular")
	if err := elm.RegisterClientTransaction(ctx, cln); err != nil {
		t.Fatalf("elm.RegisterClientTransaction() error = %v, want nil", err)
	}
	if err := elm.RegisterServerTransaction(ctx, srv); err != nil {
		t.Fatalf("elm.RegisterServerTransaction() error = %v, want nil", err)
	}
	lis, err := elm.Listen(ctx, "UDP", "127.0.0.1:5060")
	if err != nil {
		t.Fatalf("elm.Listen() error = %v, want nil", err)
	}

	// Nothing was auto-started while preparing.
	if got := cln.startCalls.Load(); got != 0 {
		t.Errorf("client tx start calls = %d, want 0", got)
	}
	if got := srv.startCalls.Load(); got != 0 {
		t.Errorf("server tx start calls = %d, want 0", got)
	}
	if got := stubLis.serveCalls.Load(); got != 0 {
		t.Errorf("listener serve calls = %d, want 0", got)
	}

	// The caller drives every unit in its chosen order; Element.Start is
	// never called at all.
	events := &lcEventLog{}
	cln.onReturn = func() { events.add("cln:done") }
	srv.onReturn = func() { events.add("srv:done") }
	stubLis.onReturn = func() { events.add("lis:done") }

	if err := cln.Start(ctx); err != nil {
		t.Fatalf("manual cln.Start() error = %v, want nil", err)
	}
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("manual srv.Start() error = %v, want nil", err)
	}
	if err := lis.Serve(ctx); err != nil {
		t.Fatalf("manual lis.Serve() error = %v, want nil", err)
	}

	clnIdx, srvIdx, lisIdx := events.index("cln:done"), events.index("srv:done"), events.index("lis:done")
	if clnIdx != 0 || srvIdx != 1 || lisIdx != 2 {
		t.Fatalf("startup event order = %v, want cln:done < srv:done < lis:done", []int{clnIdx, srvIdx, lisIdx})
	}
	if got := cln.startCalls.Load(); got != 1 {
		t.Errorf("client tx start calls = %d, want 1", got)
	}
	if got := srv.startCalls.Load(); got != 1 {
		t.Errorf("server tx start calls = %d, want 1", got)
	}
	if got := stubLis.serveCalls.Load(); got != 1 {
		t.Errorf("listener serve calls = %d, want 1", got)
	}
	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}
}

// A granular listener stays transport-owned: Element.Close closes the bound
// resource through the tracked transport even though it never served.
func TestElement_Listen_TransportOwned(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	udp, err := transport.NewConnectionLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}
	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })
	if err := elm.TrackTransport(udp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	lis, err := elm.Listen(ctx, "UDP", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("elm.Listen() error = %v, want nil", err)
	}
	laddr := lis.LocalAddr()

	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}

	// Rebinding the exact local address proves the transport closed the
	// unserved listener's socket.
	pc, err := net.ListenPacket("udp", laddr.String())
	if err != nil {
		t.Fatalf("net.ListenPacket() on %v error = %v, want nil (socket must be closed)", laddr, err)
	}
	if err := pc.Close(); err != nil {
		t.Fatalf("rebound socket Close() error = %v, want nil", err)
	}
}

type elementOwnerMiddleware struct {
	sip.NoopMessageInterceptor

	inReqCtx  context.Context
	inResCtx  context.Context
	outReqCtx context.Context
	outResCtx context.Context
	clientCtx context.Context
	serverCtx context.Context
	closeCtx  context.Context
}

func (mw *elementOwnerMiddleware) InterceptInboundRequest(
	ctx context.Context,
	next sip.RequestReceiver,
	req *sip.RequestEnvelope,
) error {
	mw.inReqCtx = ctx
	return errors.Wrap(next.RecvRequest(ctx, req))
}

func (mw *elementOwnerMiddleware) InterceptInboundResponse(
	ctx context.Context,
	next sip.ResponseReceiver,
	res *sip.ResponseEnvelope,
) error {
	mw.inResCtx = ctx
	return errors.Wrap(next.RecvResponse(ctx, res))
}

func (mw *elementOwnerMiddleware) InterceptOutboundRequest(
	ctx context.Context,
	next sip.RequestSender,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	mw.outReqCtx = ctx
	return errors.Wrap(next.SendRequest(ctx, req, opts...))
}

func (mw *elementOwnerMiddleware) InterceptOutboundResponse(
	ctx context.Context,
	next sip.ResponseSender,
	res *sip.ResponseEnvelope,
	opts ...sip.SendResponseOptions,
) error {
	mw.outResCtx = ctx
	return errors.Wrap(next.SendResponse(ctx, res, opts...))
}

func (mw *elementOwnerMiddleware) HandleClientTransaction(ctx context.Context, _ sip.ClientTransaction) {
	mw.clientCtx = ctx
}

func (mw *elementOwnerMiddleware) HandleServerTransaction(ctx context.Context, _ sip.ServerTransaction) {
	mw.serverCtx = ctx
}

func (mw *elementOwnerMiddleware) Shutdown(ctx context.Context) error {
	mw.closeCtx = ctx
	return nil
}

func assertElementOwner(t *testing.T, ctx context.Context, want *sip.Element) {
	t.Helper()
	if ctx == nil {
		t.Fatal("callback context = nil, want element context")
	}
	got, ok := sip.ElementFromContext(ctx)
	if !ok || got != want {
		t.Fatalf("sip.ElementFromContext(ctx) = (%p, %t), want %p", got, ok, want)
	}
}

func TestElementMiddleware_ContextOwnerIsScopedPerElement(t *testing.T) {
	t.Parallel()

	tp := newProxyStubTransport()
	first, second, unrelated := &sip.Element{}, &sip.Element{}, &sip.Element{}
	t.Cleanup(func() { _ = first.Close() })
	t.Cleanup(func() { _ = second.Close() })
	for _, elm := range []*sip.Element{first, second} {
		if err := elm.TrackTransport(tp); err != nil {
			t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
		}
	}

	mwFirst, mwSecond := &elementOwnerMiddleware{}, &elementOwnerMiddleware{}
	first.UseMiddleware(mwFirst)
	second.UseMiddleware(mwSecond)

	localAddr := netip.MustParseAddrPort("11.11.11.11:5070")
	remoteAddr := netip.MustParseAddrPort("55.55.55.55:5060")
	req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".element-ctx", localAddr, remoteAddr)
	res := newInRes(t, req, sip.ResponseStatusOK)
	ctx := sip.ContextWithElement(context.WithValue(t.Context(), contextTestKey{}, "caller"), unrelated)

	receiver := sip.InterceptInboundRequest(
		slices.Collect(tp.InboundRequestInterceptors.All()),
		sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil }),
	)
	if err := receiver.RecvRequest(ctx, req); err != nil {
		t.Fatalf("receiver.RecvRequest() error = %v, want nil", err)
	}
	responseReceiver := sip.InterceptInboundResponse(
		slices.Collect(tp.InboundResponseInterceptors.All()),
		sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error { return nil }),
	)
	if err := responseReceiver.RecvResponse(ctx, res); err != nil {
		t.Fatalf("responseReceiver.RecvResponse() error = %v, want nil", err)
	}

	outReq := newOutInviteReq(t, "UDP", sip.MagicCookie+".element-out", localAddr, remoteAddr)
	requestSender := sip.InterceptOutboundRequest(
		slices.Collect(tp.OutboundRequestInterceptors.All()),
		sip.RequestSenderFunc(func(context.Context, *sip.RequestEnvelope, ...sip.SendRequestOptions) error { return nil }),
	)
	if err := requestSender.SendRequest(ctx, outReq); err != nil {
		t.Fatalf("requestSender.SendRequest() error = %v, want nil", err)
	}
	responseSender := sip.InterceptOutboundResponse(
		slices.Collect(tp.OutboundResponseInterceptors.All()),
		sip.ResponseSenderFunc(func(context.Context, *sip.ResponseEnvelope, ...sip.SendResponseOptions) error { return nil }),
	)
	if err := responseSender.SendResponse(ctx, res); err != nil {
		t.Fatalf("responseSender.SendResponse() error = %v, want nil", err)
	}

	for _, tt := range []struct {
		name string
		mw   *elementOwnerMiddleware
		elm  *sip.Element
	}{
		{name: "first", mw: mwFirst, elm: first},
		{name: "second", mw: mwSecond, elm: second},
	} {
		assertElementOwner(t, tt.mw.inReqCtx, tt.elm)
		assertElementOwner(t, tt.mw.inResCtx, tt.elm)
		assertElementOwner(t, tt.mw.outReqCtx, tt.elm)
		assertElementOwner(t, tt.mw.outResCtx, tt.elm)
		if got := tt.mw.inReqCtx.Value(contextTestKey{}); got != "caller" {
			t.Errorf("%s inbound callback context value = %v, want caller", tt.name, got)
		}
	}
	assertElementOwner(t, ctx, unrelated)

	if err := first.SendResponse(ctx, res); err != nil {
		t.Fatalf("first.SendResponse() error = %v, want nil", err)
	}
	assertElementOwner(
		t,
		testutil.WaitResult(t, tp.sendResCalls, "Element response send context").ctx,
		first,
	)

	serverTx := newCustomServerTx(t, sip.MagicCookie+".element-server", sip.TransactionStateProceeding)
	if err := first.RegisterServerTransaction(ctx, serverTx); err != nil {
		t.Fatalf("first.RegisterServerTransaction() error = %v, want nil", err)
	}
	assertElementOwner(t, mwFirst.serverCtx, first)
	if got, ok := sip.ServerTransactionFromContext(mwFirst.serverCtx); !ok || got != serverTx {
		t.Fatalf("server callback transaction = (%v, %t), want %v", got, ok, serverTx)
	}
	clientTx := newStartStubClientTx(sip.MagicCookie + ".element-client")
	if err := second.RegisterClientTransaction(ctx, clientTx); err != nil {
		t.Fatalf("second.RegisterClientTransaction() error = %v, want nil", err)
	}
	assertElementOwner(t, mwSecond.clientCtx, second)
	if got, ok := sip.ClientTransactionFromContext(mwSecond.clientCtx); !ok || got != clientTx {
		t.Fatalf("client callback transaction = (%v, %t), want %v", got, ok, clientTx)
	}

	if err := first.Shutdown(ctx); err != nil {
		t.Fatalf("first.Shutdown() error = %v, want nil", err)
	}
	assertElementOwner(t, mwFirst.closeCtx, first)
}

func TestElementRequestAttempt_ContextOwner(t *testing.T) {
	t.Parallel()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })
	tp := newProxyStubTransport()
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}
	req := newOutInviteReq(
		t, "UDP", sip.MagicCookie+".attempt-owner",
		netip.MustParseAddrPort("11.11.11.11:5070"),
		netip.MustParseAddrPort("55.55.55.55:5060"),
	)
	attempts, err := elm.ProduceRequestAttempts(t.Context(), req)
	if err != nil {
		t.Fatalf("elm.ProduceRequestAttempts() error = %v, want nil", err)
	}
	for attempt := range attempts {
		if err := attempt.DoStateless(t.Context()); err != nil {
			t.Fatalf("attempt.DoStateless() error = %v, want nil", err)
		}
		assertElementOwner(t, tp.waitSendReq(t).ctx, elm)

		tx, err := attempt.DoStateful(t.Context())
		if err != nil {
			t.Fatalf("attempt.DoStateful() error = %v, want nil", err)
		}
		assertElementOwner(t, tp.waitSendReq(t).ctx, elm)
		if err := tx.Terminate(t.Context(), nil); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}
		return
	}
	t.Fatal("elm.ProduceRequestAttempts() produced no attempts")
}

func TestElementStart_ErrorHandlerContextTransaction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		new  func(*testing.T, string) sip.Transaction
	}{
		{
			name: "client",
			new: func(_ *testing.T, branch string) sip.Transaction {
				tx := newStartStubClientTx(branch)
				tx.startErr = errStubRun
				return tx
			},
		},
		{
			name: "server",
			new: func(t *testing.T, branch string) sip.Transaction {
				t.Helper()
				tx := newStartStubServerTx(t, branch)
				tx.startErr = errStubRun
				return tx
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			elm := &sip.Element{}
			unrelatedElm := &sip.Element{}
			t.Cleanup(func() { _ = elm.Close() })
			t.Cleanup(func() { _ = unrelatedElm.Close() })

			failedTx := tt.new(t, sip.MagicCookie+".element-start-error-"+tt.name)
			outerTx := newStartStubClientTx(sip.MagicCookie + ".element-start-outer-" + tt.name)
			var startCtxs <-chan context.Context
			switch tx := failedTx.(type) {
			case *startStubClientTx:
				startCtxs = tx.startCtxs
				if err := elm.RegisterClientTransaction(t.Context(), tx); err != nil {
					t.Fatalf("elm.RegisterClientTransaction() error = %v, want nil", err)
				}
			case *startStubServerTx:
				startCtxs = tx.startCtxs
				if err := elm.RegisterServerTransaction(t.Context(), tx); err != nil {
					t.Fatalf("elm.RegisterServerTransaction() error = %v, want nil", err)
				}
			}
			if startCtxs == nil {
				t.Fatal("transaction Start context channel = nil, want initialized")
			}

			type errorNotification struct {
				ctx context.Context
				err error
			}
			notifications := make(chan errorNotification, 1)
			elm.BindErrorHandler(sip.ErrorHandlerFunc(func(ctx context.Context, err error) {
				notifications <- errorNotification{ctx: ctx, err: errors.Wrap(err)}
			}))

			parentCtx := context.WithValue(t.Context(), contextTestKey{}, "caller")
			parentCtx = sip.ContextWithElement(parentCtx, unrelatedElm)
			parentCtx = sip.ContextWithTransaction(parentCtx, outerTx)
			if err := elm.Start(parentCtx); err != nil {
				t.Fatalf("elm.Start() error = %v, want nil", err)
			}

			startCtx := testutil.WaitResult(t, startCtxs, "transaction Start context")
			notification := testutil.WaitResult(t, notifications, "Element error notification")

			if !errors.Is(notification.err, errStubRun) {
				t.Errorf("Element error handler error = %v, want cause %v", notification.err, errStubRun)
			}
			assertManagedStartContext(t, startCtx, failedTx, tt.name, elm)
			assertManagedStartContext(t, notification.ctx, failedTx, tt.name, elm)

			if got, ok := sip.TransactionFromContext(parentCtx); !ok || got != outerTx {
				t.Errorf("parent transaction = (%v, %t), want %v", got, ok, outerTx)
			}
			assertElementOwner(t, parentCtx, unrelatedElm)
			if got := parentCtx.Value(contextTestKey{}); got != "caller" {
				t.Errorf("parent context value = %v, want caller", got)
			}
		})
	}
}

func assertManagedStartContext(
	t *testing.T,
	ctx context.Context,
	tx sip.Transaction,
	typ string,
	elm *sip.Element,
) {
	t.Helper()

	if got, ok := sip.TransactionFromContext(ctx); !ok || got != tx {
		t.Errorf("sip.TransactionFromContext(ctx) = (%v, %t), want %v", got, ok, tx)
	}

	switch typ {
	case "client":
		if got, ok := sip.ClientTransactionFromContext(ctx); !ok || got != tx {
			t.Errorf("sip.ClientTransactionFromContext(ctx) = (%v, %t), want %v", got, ok, tx)
		}
	case "server":
		if got, ok := sip.ServerTransactionFromContext(ctx); !ok || got != tx {
			t.Errorf("sip.ServerTransactionFromContext(ctx) = (%v, %t), want %v", got, ok, tx)
		}
	}

	assertElementOwner(t, ctx, elm)
	if got := ctx.Value(contextTestKey{}); got != "caller" {
		t.Errorf("context value = %v, want caller", got)
	}
}
