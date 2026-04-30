package sip_test

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestElement_ReceiveRequestResponse(t *testing.T) {
	t.Parallel()

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(listener) error = %v, want nil", err)
	}

	t.Cleanup(func() { lis.Close() })

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), &sip.ConnlessTransportOptions{
		TransportOptions: sip.TransportOptions{SentBy: sip.AddrFromHostPort("127.0.0.1", lisAddr.Port())},
	})
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	elm, err := sip.NewElement("TestElement", tp, &sip.ElementOptions{TransactionOptions: &sip.TransactionManagerOptions{}})
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}

	t.Cleanup(func() { elm.Close() })

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}

	t.Cleanup(func() { peer.Close() })

	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())
	reqCh := make(chan *sip.InboundRequestEnvelope, 1)
	resCh := make(chan *sip.InboundResponseEnvelope, 1)

	elm.TransportManager().UseInterceptor(sip.StdMessageInterceptor{
		InboundRequest: sip.InboundRequestInterceptorFunc(
			func(ctx context.Context, req *sip.InboundRequestEnvelope, next sip.RequestReceiver) error {
				reqCh <- req
				return nil
			},
		),
		InboundResponse: sip.InboundResponseInterceptorFunc(
			func(ctx context.Context, res *sip.InboundResponseEnvelope, next sip.ResponseReceiver) error {
				resCh <- res
				return nil
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

	if _, err := peer.WriteTo([]byte(newElementTestRequest(t, lisAddr).Render(nil)), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(request) error = %v, want nil", err)
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

	if got := gotReq.Method(); got != sip.RequestMethodInvite {
		t.Fatalf("inbound request method = %v, want %v", got, sip.RequestMethodInvite)
	}

	if got := gotReq.Transport(); got != sip.TransportProto("UDP") {
		t.Fatalf("inbound request transport = %v, want UDP", got)
	}

	if got := gotReq.LocalAddr(); got != lisAddr {
		t.Fatalf("inbound request local addr = %v, want %v", got, lisAddr)
	}

	if got := gotReq.RemoteAddr(); got != peerAddr {
		t.Fatalf("inbound request remote addr = %v, want %v", got, peerAddr)
	}

	if _, err := peer.WriteTo([]byte(newElementTestResponse(t, lisAddr).Render(nil)), net.UDPAddrFromAddrPort(lisAddr)); err != nil {
		t.Fatalf("peer.WriteTo(response) error = %v, want nil", err)
	}

	var gotRes *sip.InboundResponseEnvelope
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

	if got := gotRes.Transport(); got != sip.TransportProto("UDP") {
		t.Fatalf("inbound response transport = %v, want UDP", got)
	}

	if got := gotRes.LocalAddr(); got != lisAddr {
		t.Fatalf("inbound response local addr = %v, want %v", got, lisAddr)
	}

	if got := gotRes.RemoteAddr(); got != peerAddr {
		t.Fatalf("inbound response remote addr = %v, want %v", got, peerAddr)
	}
}

func newElementTestRequest(tb testing.TB, laddr netip.AddrPort) *sip.Request {
	tb.Helper()

	req, err := sip.NewRequest(
		sip.RequestMethodInvite,
		&uri.SIP{User: uri.User("bob"), Addr: uri.AddrFromHost("example.com")},
		&uri.SIP{User: uri.User("alice"), Addr: uri.AddrFromHost("example.com")},
		&uri.SIP{User: uri.User("bob"), Addr: uri.AddrFromHost("example.com")},
		&sip.RequestOptions{Transport: "UDP", Branch: sip.GenerateBranch(0), LocalTag: "from-tag", CallID: "call-id"},
	)
	if err != nil {
		tb.Fatalf("sip.NewRequest() error = %v, want nil", err)
	}

	req.Headers.Set(header.Via{{
		Proto:     sip.ProtoVer20(),
		Transport: "UDP",
		Addr:      header.AddrFromHostPort(laddr.Addr().String(), laddr.Port()),
		Params:    make(header.Values).Set("branch", sip.GenerateBranch(0)),
	}})
	req.Headers.Set(header.ContentLength(0))

	return req
}

func newElementTestResponse(tb testing.TB, laddr netip.AddrPort) *sip.Response {
	tb.Helper()

	req := newElementTestRequest(tb, laddr)

	res, err := req.NewResponse(sip.ResponseStatusOK, &sip.ResponseOptions{LocalTag: "to-tag"})
	if err != nil {
		tb.Fatalf("req.NewResponse() error = %v, want nil", err)
	}

	res.Headers.Set(header.ContentLength(0))

	return res
}
