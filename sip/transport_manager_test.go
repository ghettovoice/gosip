package sip_test

import (
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/sip"
)

func TestTransportManager_ServeClose(t *testing.T) {
	t.Parallel()

	mng := &sip.TransportManager{}
	tcpTp := newStubTransport("TCP", 5060)
	tlsTp := newStubTransport("TLS", 5061)
	udpTp := newStubTransport("UDP", 5060)

	if err := mng.TrackTransport(tcpTp, true); err != nil {
		t.Fatalf("manager.TrackTransport(TCP) error = %v, want nil", err)
	}
	if err := mng.TrackTransport(tlsTp, true); err != nil {
		t.Fatalf("manager.TrackTransport(TLS) error = %v, want nil", err)
	}
	if err := mng.TrackTransport(udpTp, false); err != nil {
		t.Fatalf("manager.TrackTransport(UDP) error = %v, want nil", err)
	}

	var started sync.WaitGroup
	started.Add(1)
	go func() {
		started.Done()
		err := mng.Serve(t.Context())
		if !errors.Is(err, sip.ErrTransportClosed) {
			t.Errorf("manager.Serve(ctx) error = %v, want ErrTransportClosed", err)
		}
	}()
	started.Wait()

	time.Sleep(100 * time.Millisecond)

	if err := mng.Close(t.Context()); err != nil {
		t.Fatalf("manager.Close(ctx) error = %v, want nil", err)
	}
}

func TestTransportManager_SendRequest(t *testing.T) {
	t.Parallel()

	mng := &sip.TransportManager{}
	defTp := newStubTransport("UDP", 5060)
	viaTp := newStubTransport("TCP", 5060)

	if err := mng.TrackTransport(defTp, true); err != nil {
		t.Fatalf("manager.TrackTransport(UDP) error = %v, want nil", err)
	}
	if err := mng.TrackTransport(viaTp, false); err != nil {
		t.Fatalf("manager.TrackTransport(TCP) error = %v, want nil", err)
	}

	req := &sip.Request{
		Headers: make(sip.Headers).Set(header.Via{}),
	}
	outReq, err := sip.NewOutboundRequestEnvelope(req)
	if err != nil {
		t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
	}
	outReq.SetTransport(viaTp.Proto())
	outReq.SetLocalAddr(viaTp.LocalAddr())

	if err := mng.SendRequest(t.Context(), outReq, nil); err != nil {
		t.Fatalf("manager.SendRequest(ctx, req, nil) error = %v, want nil", err)
	}

	if viaTp.requestCount() != 1 {
		t.Fatalf("expected use of transport referenced by %s:%s", viaTp.Proto(), viaTp.LocalAddr())
	}

	if defTp.requestCount() != 0 {
		t.Fatalf("unexpected use of default transport")
	}
}

func TestTransportManager_SendRequest_Default(t *testing.T) {
	t.Parallel()

	mng := &sip.TransportManager{}
	defTp := newStubTransport("UDP", 5060)
	if err := mng.TrackTransport(defTp, true); err != nil {
		t.Fatalf("manager.TrackTransport(UDP) error = %v, want nil", err)
	}

	req := &sip.Request{}
	outReq, err := sip.NewOutboundRequestEnvelope(req)
	if err != nil {
		t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
	}
	outReq.SetLocalAddr(netip.MustParseAddrPort("127.0.0.1:5070"))

	if got := defTp.requestCount(); got != 0 {
		t.Fatalf("unexpected sends on transport: got %d, want 0", got)
	}

	if err := mng.SendRequest(t.Context(), outReq, nil); err != nil {
		t.Fatalf("manager.SendRequest(ctx, req, nil) error = %v, want nil", err)
	}

	if defTp.requestCount() != 1 {
		t.Fatalf("expected use of default transport")
	}
}

func TestTransportManager_SendResponse(t *testing.T) {
	t.Parallel()

	mng := &sip.TransportManager{}
	defTp := newStubTransport("UDP", 5060)
	viaTp := newStubTransport("TCP", 5060)

	if err := mng.TrackTransport(defTp, true); err != nil {
		t.Fatalf("manager.TrackTransport(UDP) error = %v, want nil", err)
	}
	if err := mng.TrackTransport(viaTp, false); err != nil {
		t.Fatalf("manager.TrackTransport(TCP) error = %v, want nil", err)
	}

	res := &sip.Response{
		Headers: make(sip.Headers).Set(header.Via{{Transport: viaTp.Proto()}}),
	}
	outRes, err := sip.NewOutboundResponseEnvelope(res)
	if err != nil {
		t.Fatalf("sip.NewOutboundResponseEnvelope() error = %v, want nil", err)
	}
	outRes.SetTransport(viaTp.Proto())
	outRes.SetLocalAddr(viaTp.LocalAddr())

	if err := mng.SendResponse(t.Context(), outRes, nil); err != nil {
		t.Fatalf("manager.SendResponse(ctx, res, nil) error = %v, want nil", err)
	}

	if viaTp.responseCount() != 1 {
		t.Fatalf("expected use of transport referenced by %s:%s", viaTp.Proto(), viaTp.LocalAddr())
	}

	if defTp.responseCount() != 0 {
		t.Fatalf("unexpected use of default transport")
	}
}

func TestTransportManager_SendResponse_Default(t *testing.T) {
	t.Parallel()

	mng := &sip.TransportManager{}
	defTp := newStubTransport("UDP", 5060)
	if err := mng.TrackTransport(defTp, true); err != nil {
		t.Fatalf("manager.TrackTransport(UDP) error = %v, want nil", err)
	}

	res := &sip.Response{}
	outRes, err := sip.NewOutboundResponseEnvelope(res)
	if err != nil {
		t.Fatalf("sip.NewOutboundResponseEnvelope() error = %v, want nil", err)
	}
	outRes.SetLocalAddr(netip.MustParseAddrPort("127.0.0.1:5070"))

	if got := defTp.responseCount(); got != 0 {
		t.Fatalf("unexpected sends on transport: got %d, want 0", got)
	}

	if err := mng.SendResponse(t.Context(), outRes, nil); err != nil {
		t.Fatalf("manager.SendResponse(ctx, res, nil) error = %v, want nil", err)
	}

	if defTp.responseCount() != 1 {
		t.Fatalf("expected use of default transport")
	}
}
