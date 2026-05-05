package sip_test

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

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

func TestNewElement(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	t.Cleanup(func() { tp.Close() })

	tests := []struct {
		name    string
		opts    *sip.ElementOptions
		elmName string
		wantErr bool
		errType error
		check   func(t *testing.T, elm *sip.Element)
	}{
		{
			name:    "success with defaults",
			opts:    nil,
			elmName: "TestElement",
			wantErr: false,
			check: func(t *testing.T, elm *sip.Element) {
				t.Helper()

				if elm.Name() != "TestElement" {
					t.Errorf("elm.Name() = %q, want %q", elm.Name(), "TestElement")
				}

				if elm.TransportManager() == nil {
					t.Error("elm.TransportManager() = nil, want non-nil")
				}
			},
		},
		{
			name:    "empty name error",
			opts:    nil,
			elmName: "",
			wantErr: true,
			errType: errors.ErrInvalidArgument,
		},
		{
			name: "with custom logger",
			opts: &sip.ElementOptions{
				Logger: log.Console(),
			},
			elmName: "TestElement",
			wantErr: false,
			check: func(t *testing.T, elm *sip.Element) {
				t.Helper()

				if elm.Logger() == nil {
					t.Error("elm.Logger() = nil, want non-nil")
				}
			},
		},
		{
			name: "with transaction options",
			opts: &sip.ElementOptions{
				TransactionOptions: &sip.TransactionManagerOptions{},
			},
			elmName: "TestElement",
			wantErr: false,
			check: func(t *testing.T, elm *sip.Element) {
				t.Helper()

				if elm.TransactionManager() == nil {
					t.Error("elm.TransactionManager() = nil, want non-nil")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			elm, err := sip.NewElement(tt.elmName, tp, tt.opts)

			if tt.wantErr {
				if err == nil {
					t.Fatal("sip.NewElement() error = nil, want non-nil")
				}

				if elm != nil {
					t.Errorf("sip.NewElement() = %v, want nil", elm)
				}

				if tt.errType != nil && !errors.Is(err, tt.errType) {
					t.Errorf("sip.NewElement() error = %v, want error containing %v", err, tt.errType)
				}

				return
			}

			if err != nil {
				t.Fatalf("sip.NewElement() error = %v, want nil", err)
			}

			t.Cleanup(func() { elm.Close() })

			if tt.check != nil {
				tt.check(t, elm)
			}
		})
	}
}

func TestElement_NilReceiver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fn   func(elm *sip.Element) any
		want any
	}{
		{
			name: "Name returns empty string",
			fn:   func(elm *sip.Element) any { return elm.Name() },
			want: "",
		},
		{
			name: "Logger returns nil",
			fn:   func(elm *sip.Element) any { return elm.Logger() },
			want: nil,
		},
		{
			name: "TransportManager returns nil",
			fn:   func(elm *sip.Element) any { return elm.TransportManager() },
			want: nil,
		},
		{
			name: "TransactionManager returns nil",
			fn:   func(elm *sip.Element) any { return elm.TransactionManager() },
			want: nil,
		},
		{
			name: "LogValue returns empty slog.Value",
			fn:   func(elm *sip.Element) any { return elm.LogValue() },
			want: slog.Value{},
		},
		{
			name: "Close returns nil error",
			fn:   func(elm *sip.Element) any { return elm.Close() },
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var elm *sip.Element

			got := tt.fn(elm)

			switch w := tt.want.(type) {
			case slog.Value:
				if diff := cmp.Diff(w, got, cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("nil Element method mismatch (-want +got):\n%s", diff)
				}
			case error:
				gotErr, ok := got.(error)
				if !ok {
					t.Errorf("nil Element method returned %v, want error", got)
				} else if !errors.Is(gotErr, w) {
					t.Errorf("nil Element method error = %v, want %v", gotErr, w)
				}
			default:
				if w == nil {
					// Handle typed nil comparison
					if got != nil && !reflect.ValueOf(got).IsNil() {
						t.Errorf("nil Element method = %v, want nil", got)
					}
				} else if got != w {
					t.Errorf("nil Element method = %v, want %v", got, w)
				}
			}
		})
	}
}

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

	elm.TransportManager().UseMessageInterceptor(sip.StdMessageInterceptor{
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

	if _, err := peer.WriteTo(
		[]byte(newElementTestRequest(t, lisAddr).Render(nil)),
		net.UDPAddrFromAddrPort(lisAddr),
	); err != nil {
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

	if _, err := peer.WriteTo(
		[]byte(newElementTestResponse(t, lisAddr).Render(nil)),
		net.UDPAddrFromAddrPort(lisAddr),
	); err != nil {
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

func TestElement_SendRequest_WithTransportAndAddr(t *testing.T) {
	t.Parallel()

	// Create listener to receive the request
	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	defer lis.Close()

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	// Create transport and element
	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), &sip.ConnlessTransportOptions{
		TransportOptions: sip.TransportOptions{SentBy: sip.AddrFromHostPort("127.0.0.1", lisAddr.Port())},
	})
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	elm, err := sip.NewElement("TestElement", tp, nil)
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	defer elm.Close()

	// Create outbound request with pre-set transport and address
	req := newElementTestRequest(t, lisAddr)

	outReq, err := sip.NewOutboundRequestEnvelope(req)
	if err != nil {
		t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
	}

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}
	defer peer.Close()

	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	outReq.SetTransport("UDP")
	outReq.SetRemoteAddr(peerAddr)

	// Send request
	ctx, cancel := context.WithTimeout(t.Context(), asyncEventTimeout)
	defer cancel()

	recvCh := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 65535)

		peer.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck

		n, _, err := peer.ReadFrom(buf)
		if err != nil {
			return
		}

		recvCh <- buf[:n]
	}()

	err = elm.SendRequest(ctx, outReq, nil)
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

func TestElement_SendRequest_ResolveTargetURI(t *testing.T) {
	t.Parallel()

	// Create listener to receive the request
	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	defer lis.Close()

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	// Create transport and element
	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), &sip.ConnlessTransportOptions{
		TransportOptions: sip.TransportOptions{SentBy: sip.AddrFromHostPort("127.0.0.1", lisAddr.Port())},
	})
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	elm, err := sip.NewElement("TestElement", tp, nil)
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	defer elm.Close()

	// Create outbound request with URI pointing to our listener
	// The URI has an IP address, so it should be resolved directly
	ruri := &uri.SIP{
		User: uri.User("bob"),
		Addr: uri.AddrFromHostPort("127.0.0.1", lisAddr.Port()),
	}
	furi := &uri.SIP{User: uri.User("alice"), Addr: uri.AddrFromHost("example.com")}
	turi := &uri.SIP{User: uri.User("bob"), Addr: uri.AddrFromHost("example.com")}

	req, err := sip.NewRequest(
		sip.RequestMethodInvite,
		ruri,
		furi,
		turi,
		&sip.RequestOptions{Transport: "UDP", Branch: sip.GenerateBranch(0), LocalTag: "from-tag", CallID: "call-id"},
	)
	if err != nil {
		t.Fatalf("sip.NewRequest() error = %v, want nil", err)
	}

	req.Headers.Set(header.Via{{
		Proto:     sip.ProtoVer20(),
		Transport: "UDP",
		Addr:      header.AddrFromHostPort("127.0.0.1", lisAddr.Port()),
		Params:    make(header.Values).Set("branch", sip.GenerateBranch(0)),
	}})
	req.Headers.Set(header.ContentLength(0))

	outReq, err := sip.NewOutboundRequestEnvelope(req)
	if err != nil {
		t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
	}

	// Send request
	ctx, cancel := context.WithTimeout(t.Context(), asyncEventTimeout)
	defer cancel()

	recvCh := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 65535)

		lis.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck

		n, _, err := lis.ReadFrom(buf)
		if err != nil {
			return
		}

		recvCh <- buf[:n]
	}()

	err = elm.SendRequest(ctx, outReq, nil)
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

func TestElement_SendRequest_NoDestAddressResolved(t *testing.T) {
	t.Parallel()

	// Create transport and element
	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	elm, err := sip.NewElement("TestElement", tp, nil)
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	defer elm.Close()

	// Create request with an invalid URI (non-resolvable domain without port)
	ruri := &uri.SIP{User: uri.User("bob"), Addr: uri.AddrFromHost("invalid.domain.that.does.not.exist.example")}
	furi := &uri.SIP{User: uri.User("alice"), Addr: uri.AddrFromHost("example.com")}
	turi := &uri.SIP{User: uri.User("bob"), Addr: uri.AddrFromHost("example.com")}

	req, err := sip.NewRequest(
		sip.RequestMethodInvite,
		ruri,
		furi,
		turi,
		&sip.RequestOptions{Transport: "UDP", Branch: sip.GenerateBranch(0), LocalTag: "from-tag", CallID: "call-id"},
	)
	if err != nil {
		t.Fatalf("sip.NewRequest() error = %v, want nil", err)
	}

	req.Headers.Set(header.Via{{
		Proto:     sip.ProtoVer20(),
		Transport: "UDP",
		Addr:      header.AddrFromHost("127.0.0.1"),
		Params:    make(header.Values).Set("branch", sip.GenerateBranch(0)),
	}})
	req.Headers.Set(header.ContentLength(0))

	outReq, err := sip.NewOutboundRequestEnvelope(req)
	if err != nil {
		t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
	}

	// Send request - should fail with ErrNoDestAddressResolved
	ctx, cancel := context.WithTimeout(t.Context(), asyncEventTimeout)
	defer cancel()

	err = elm.SendRequest(ctx, outReq, nil)
	if err == nil {
		t.Fatal("elm.SendRequest() error = nil, want non-nil")
	}

	if !errors.Is(err, sip.ErrNoDestAddressResolved) {
		t.Errorf("elm.SendRequest() error = %v, want ErrNoDestAddressResolved", err)
	}
}

func TestElement_SendRequest_UserAgentHeader(t *testing.T) {
	t.Parallel()

	// Create listener to receive the request
	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	defer lis.Close()

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	// Create transport and element with specific name
	const elementName = "TestUserAgentElement"

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), &sip.ConnlessTransportOptions{
		TransportOptions: sip.TransportOptions{SentBy: sip.AddrFromHostPort("127.0.0.1", lisAddr.Port())},
	})
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	elm, err := sip.NewElement(elementName, tp, nil)
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	defer elm.Close()

	// Create outbound request with pre-set transport and address
	req := newElementTestRequest(t, lisAddr)

	outReq, err := sip.NewOutboundRequestEnvelope(req)
	if err != nil {
		t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
	}

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}
	defer peer.Close()

	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	outReq.SetTransport("UDP")
	outReq.SetRemoteAddr(peerAddr)

	// Send request
	ctx, cancel := context.WithTimeout(t.Context(), asyncEventTimeout)
	defer cancel()

	recvCh := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 65535)

		peer.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck

		n, _, err := peer.ReadFrom(buf)
		if err != nil {
			return
		}

		recvCh <- buf[:n]
	}()

	err = elm.SendRequest(ctx, outReq, nil)
	if err != nil {
		t.Fatalf("elm.SendRequest() error = %v, want nil", err)
	}

	select {
	case data := <-recvCh:
		msg, err := sip.DefaultParser().ParsePacket(data)
		if err != nil {
			t.Fatalf("sip.DefaultParser().ParsePacket() error = %v, want nil", err)
		}

		if reqMsg, ok := msg.(*sip.Request); ok {
			// Check that User-Agent header was added
			hdrs := reqMsg.Headers.Get("User-Agent")
			if len(hdrs) == 0 {
				t.Error("User-Agent header not present, expected it to be set")
			} else {
				ua := hdrs[0].RenderValue()
				if ua != elementName {
					t.Errorf("User-Agent header = %q, want %q", ua, elementName)
				}
			}
		} else {
			t.Errorf("message type = %T, want *sip.Request", msg)
		}
	case <-time.After(asyncEventTimeout):
		t.Fatal("request not received by peer")
	}
}

func TestElement_SendResponse(t *testing.T) {
	t.Parallel()

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	defer lis.Close()

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), &sip.ConnlessTransportOptions{
		TransportOptions: sip.TransportOptions{SentBy: sip.AddrFromHostPort("127.0.0.1", lisAddr.Port())},
	})
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	elm, err := sip.NewElement("TestElement", tp, nil)
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	defer elm.Close()

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}
	defer peer.Close()

	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	res := newElementTestResponse(t, lisAddr)
	res.Headers.Set(header.Via{{
		Proto:     sip.ProtoVer20(),
		Transport: "UDP",
		Addr:      header.AddrFromHostPort(peerAddr.Addr().String(), peerAddr.Port()),
		Params:    make(header.Values).Set("branch", sip.GenerateBranch(0)),
	}})

	outRes, err := sip.NewOutboundResponseEnvelope(res)
	if err != nil {
		t.Fatalf("sip.NewOutboundResponseEnvelope() error = %v, want nil", err)
	}

	outRes.SetTransport("UDP")
	outRes.SetRemoteAddr(peerAddr)

	ctx, cancel := context.WithTimeout(t.Context(), asyncEventTimeout)
	defer cancel()

	recvCh := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 65535)

		peer.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck

		n, _, err := peer.ReadFrom(buf)
		if err != nil {
			return
		}

		recvCh <- buf[:n]
	}()

	err = elm.SendResponse(ctx, outRes, nil)
	if err != nil {
		t.Fatalf("elm.SendResponse() error = %v, want nil", err)
	}

	select {
	case data := <-recvCh:
		msg, err := sip.DefaultParser().ParsePacket(data)
		if err != nil {
			t.Fatalf("sip.DefaultParser().ParsePacket() error = %v, want nil", err)
		}

		resMsg, ok := msg.(*sip.Response)
		if !ok {
			t.Fatalf("message type = %T, want *sip.Response", msg)
		}

		if got, want := resMsg.Status, sip.ResponseStatusOK; got != want {
			t.Errorf("response status = %v, want %v", got, want)
		}
	case <-time.After(asyncEventTimeout):
		t.Fatal("response not received by peer")
	}
}

func TestElement_SendResponse_ServerHeader(t *testing.T) {
	t.Parallel()

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	defer lis.Close()

	lisAddr := netip.MustParseAddrPort(lis.LocalAddr().String())

	const elementName = "TestServerElement"

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), &sip.ConnlessTransportOptions{
		TransportOptions: sip.TransportOptions{SentBy: sip.AddrFromHostPort("127.0.0.1", lisAddr.Port())},
	})
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	elm, err := sip.NewElement(elementName, tp, nil)
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	defer elm.Close()

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}
	defer peer.Close()

	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	res := newElementTestResponse(t, lisAddr)
	res.Headers.Set(header.Via{{
		Proto:     sip.ProtoVer20(),
		Transport: "UDP",
		Addr:      header.AddrFromHostPort(peerAddr.Addr().String(), peerAddr.Port()),
		Params:    make(header.Values).Set("branch", sip.GenerateBranch(0)),
	}})

	outRes, err := sip.NewOutboundResponseEnvelope(res)
	if err != nil {
		t.Fatalf("sip.NewOutboundResponseEnvelope() error = %v, want nil", err)
	}

	outRes.SetTransport("UDP")
	outRes.SetRemoteAddr(peerAddr)

	ctx, cancel := context.WithTimeout(t.Context(), asyncEventTimeout)
	defer cancel()

	recvCh := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 65535)

		peer.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck

		n, _, err := peer.ReadFrom(buf)
		if err != nil {
			return
		}

		recvCh <- buf[:n]
	}()

	err = elm.SendResponse(ctx, outRes, nil)
	if err != nil {
		t.Fatalf("elm.SendResponse() error = %v, want nil", err)
	}

	select {
	case data := <-recvCh:
		msg, err := sip.DefaultParser().ParsePacket(data)
		if err != nil {
			t.Fatalf("sip.DefaultParser().ParsePacket() error = %v, want nil", err)
		}

		resMsg, ok := msg.(*sip.Response)
		if !ok {
			t.Fatalf("message type = %T, want *sip.Response", msg)
		}

		hdrs := resMsg.Headers.Get("Server")
		if len(hdrs) == 0 {
			t.Fatal("Server header not present, expected it to be set")
		}

		if got, want := hdrs[0].RenderValue(), elementName; got != want {
			t.Errorf("Server header = %q, want %q", got, want)
		}
	case <-time.After(asyncEventTimeout):
		t.Fatal("response not received by peer")
	}
}

func TestElement_SendRequestStateful_WithTransportAndAddr(t *testing.T) {
	t.Parallel()

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	defer lis.Close()

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
	defer elm.Close()

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}
	defer peer.Close()

	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	req := newElementTestRequest(t, lisAddr)

	outReq, err := sip.NewOutboundRequestEnvelope(req)
	if err != nil {
		t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
	}

	outReq.SetTransport("UDP")
	outReq.SetRemoteAddr(peerAddr)

	ctx, cancel := context.WithTimeout(t.Context(), asyncEventTimeout)
	defer cancel()

	recvCh := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 65535)

		peer.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck

		n, _, err := peer.ReadFrom(buf)
		if err != nil {
			return
		}

		recvCh <- buf[:n]
	}()

	tx, err := elm.SendRequestStateful(ctx, outReq, nil)
	if err != nil {
		t.Fatalf("elm.SendRequestStateful() error = %v, want nil", err)
	}

	if tx == nil {
		t.Fatal("elm.SendRequestStateful() tx = nil, want non-nil")
	}

	t.Cleanup(func() { tx.Terminate(t.Context()) }) //nolint:errcheck

	select {
	case data := <-recvCh:
		msg, err := sip.DefaultParser().ParsePacket(data)
		if err != nil {
			t.Fatalf("sip.DefaultParser().ParsePacket() error = %v, want nil", err)
		}

		reqMsg, ok := msg.(*sip.Request)
		if !ok {
			t.Fatalf("message type = %T, want *sip.Request", msg)
		}

		if got, want := reqMsg.Method, sip.RequestMethodInvite; got != want {
			t.Errorf("request method = %v, want %v", got, want)
		}
	case <-time.After(asyncEventTimeout):
		t.Fatal("request not received by peer")
	}
}

func TestElement_SendRequestStateful_StatelessElement(t *testing.T) {
	t.Parallel()

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	elm, err := sip.NewElement("TestElement", tp, nil)
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	defer elm.Close()

	req := newElementTestRequest(t, netip.MustParseAddrPort("127.0.0.1:5060"))

	outReq, err := sip.NewOutboundRequestEnvelope(req)
	if err != nil {
		t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), asyncEventTimeout)
	defer cancel()

	tx, err := elm.SendRequestStateful(ctx, outReq, nil)
	if err == nil {
		t.Fatal("elm.SendRequestStateful() error = nil, want non-nil")
	}

	if tx != nil {
		t.Fatalf("elm.SendRequestStateful() tx = %v, want nil", tx)
	}

	if !errors.Is(err, sip.ErrStatelessElement) {
		t.Errorf("elm.SendRequestStateful() error = %v, want ErrStatelessElement", err)
	}
}

func TestElement_SendResponseStateful(t *testing.T) {
	t.Parallel()

	lis, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v, want nil", err)
	}
	defer lis.Close()

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
	defer elm.Close()

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}
	defer peer.Close()

	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	req := newElementTestRequest(t, peerAddr)

	inReq, err := sip.NewInboundRequestEnvelope(req, "UDP", lisAddr, peerAddr)
	if err != nil {
		t.Fatalf("sip.NewInboundRequestEnvelope() error = %v, want nil", err)
	}

	res, err := inReq.NewResponse(sip.ResponseStatusOK, &sip.ResponseOptions{LocalTag: "to-tag"})
	if err != nil {
		t.Fatalf("inReq.NewResponse() error = %v, want nil", err)
	}

	res.AccessMessage(func(r *sip.Response) {
		if r != nil {
			r.Headers.Set(header.ContentLength(0))
		}
	})

	outRes := res

	outRes.SetTransport("UDP")
	outRes.SetRemoteAddr(peerAddr)

	ctx, cancel := context.WithTimeout(t.Context(), asyncEventTimeout)
	defer cancel()

	recvCh := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 65535)

		peer.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck

		n, _, err := peer.ReadFrom(buf)
		if err != nil {
			return
		}

		recvCh <- buf[:n]
	}()

	tx, err := elm.SendResponseStateful(ctx, inReq, outRes, nil)
	if err != nil {
		t.Fatalf("elm.SendResponseStateful() error = %v, want nil", err)
	}

	if tx == nil {
		t.Fatal("elm.SendResponseStateful() tx = nil, want non-nil")
	}

	t.Cleanup(func() { tx.Terminate(t.Context()) }) //nolint:errcheck

	select {
	case data := <-recvCh:
		msg, err := sip.DefaultParser().ParsePacket(data)
		if err != nil {
			t.Fatalf("sip.DefaultParser().ParsePacket() error = %v, want nil", err)
		}

		resMsg, ok := msg.(*sip.Response)
		if !ok {
			t.Fatalf("message type = %T, want *sip.Response", msg)
		}

		if got, want := resMsg.Status, sip.ResponseStatusOK; got != want {
			t.Errorf("response status = %v, want %v", got, want)
		}
	case <-time.After(asyncEventTimeout):
		t.Fatal("response not received by peer")
	}
}

func TestElement_SendResponseStateful_NoTransportResolved(t *testing.T) {
	t.Parallel()

	txOpts := &sip.TransactionManagerOptions{}

	tp, err := sip.NewConnlessTransport(sip.UDPTransportMetadata(), nil)
	if err != nil {
		t.Fatalf("sip.NewConnlessTransport() error = %v, want nil", err)
	}

	elm, err := sip.NewElement("TestElement", tp, &sip.ElementOptions{TransactionOptions: txOpts})
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	defer elm.Close()

	lisAddr := netip.MustParseAddrPort("127.0.0.1:5060")
	peerAddr := netip.MustParseAddrPort("127.0.0.1:5070")

	req := newElementTestRequest(t, peerAddr)

	inReq, err := sip.NewInboundRequestEnvelope(req, "UDP", lisAddr, peerAddr)
	if err != nil {
		t.Fatalf("sip.NewInboundRequestEnvelope() error = %v, want nil", err)
	}

	res, err := inReq.NewResponse(sip.ResponseStatusOK, &sip.ResponseOptions{LocalTag: "to-tag"})
	if err != nil {
		t.Fatalf("inReq.NewResponse() error = %v, want nil", err)
	}

	res.AccessMessage(func(r *sip.Response) {
		if r != nil {
			r.Headers.Set(header.Via{{
				Proto:     sip.ProtoVer20(),
				Transport: "TCP",
				Addr:      header.AddrFromHostPort(peerAddr.Addr().String(), peerAddr.Port()),
				Params:    make(header.Values).Set("branch", sip.GenerateBranch(0)),
			}})
		}
	})

	outRes := res

	ctx, cancel := context.WithTimeout(t.Context(), asyncEventTimeout)
	defer cancel()

	tx, err := elm.SendResponseStateful(ctx, inReq, outRes, nil)
	if err == nil {
		t.Fatal("elm.SendResponseStateful() error = nil, want non-nil")
	}

	if tx != nil {
		t.Fatalf("elm.SendResponseStateful() tx = %v, want nil", tx)
	}

	if !errors.Is(err, errors.ErrInvalidArgument) {
		t.Errorf("elm.SendResponseStateful() error = %v, want invalid argument error", err)
	}
}
