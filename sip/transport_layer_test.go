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

func TestTransportLayer_ServeClose(t *testing.T) {
	t.Parallel()

	layer := &sip.TransportLayer{}
	tcpTp := newStubTransport("TCP", 5060)
	tlsTp := newStubTransport("TLS", 5061)
	udpTp := newStubTransport("UDP", 5060)

	if err := layer.TrackTransport(tcpTp, true); err != nil {
		t.Fatalf("layer.TrackTransport(TCP) error = %v, want nil", err)
	}
	if err := layer.TrackTransport(tlsTp, true); err != nil {
		t.Fatalf("layer.TrackTransport(TLS) error = %v, want nil", err)
	}
	if err := layer.TrackTransport(udpTp, false); err != nil {
		t.Fatalf("layer.TrackTransport(UDP) error = %v, want nil", err)
	}

	var started sync.WaitGroup
	started.Add(1)
	go func() {
		started.Done()
		err := layer.Serve()
		if !errors.Is(err, sip.ErrTransportClosed) {
			t.Errorf("layer.Serve() error = %v, want ErrTransportClosed", err)
		}
	}()
	started.Wait()

	time.Sleep(100 * time.Millisecond)

	if err := layer.Close(); err != nil {
		t.Fatalf("layer.Close() error = %v, want nil", err)
	}
}

// func TestTransportLayer_ContextPropagation(t *testing.T) {
// 	t.Parallel()

// 	layer := &sip.TransportLayer{}
// 	st := newStubTransport("UDP", 5060)

// 	reqCtx := make(chan context.Context, 1)
// 	resCtx := make(chan context.Context, 1)

// 	layer.OnRequest(func(ctx context.Context, _ *sip.InboundRequest) { reqCtx <- ctx })
// 	layer.OnResponse(func(ctx context.Context, _ *sip.InboundResponse) { resCtx <- ctx })

// 	if err := layer.TrackTransport(st, true); err != nil {
// 		t.Fatalf("layer.TrackTransport(UDP) error = %v, want nil", err)
// 	}

// 	ctx, cancel := context.WithCancel(t.Context())
// 	defer cancel()

// 	st.triggerRequest(ctx, &sip.InboundRequest{})
// 	st.triggerResponse(ctx, &sip.InboundResponse{})

// 	select {
// 	case c := <-reqCtx:
// 		if got, ok := sip.TransportLayerFromContext(c); !ok || got != layer {
// 			t.Fatalf("request context missing transport layer")
// 		}
// 	case <-time.After(time.Second):
// 		t.Fatal("timeout waiting for request context")
// 	}

// 	select {
// 	case c := <-resCtx:
// 		if got, ok := sip.TransportLayerFromContext(c); !ok || got != layer {
// 			t.Fatalf("response context missing transport layer")
// 		}
// 	case <-time.After(time.Second):
// 		t.Fatal("timeout waiting for response context")
// 	}
// }

func TestTransportLayer_SendRequest(t *testing.T) {
	t.Parallel()
	layer := &sip.TransportLayer{}
	defTp := newStubTransport("UDP", 5060)
	viaTp := newStubTransport("TCP", 5060)

	if err := layer.TrackTransport(defTp, true); err != nil {
		t.Fatalf("layer.TrackTransport(UDP) error = %v, want nil", err)
	}
	if err := layer.TrackTransport(viaTp, false); err != nil {
		t.Fatalf("layer.TrackTransport(TCP) error = %v, want nil", err)
	}

	req := &sip.Request{
		Headers: make(sip.Headers).Set(header.Via{{Transport: viaTp.Proto()}}),
	}
	outReq := sip.NewOutboundRequest(req)
	outReq.SetLocalAddr(viaTp.LocalAddr())

	if err := layer.SendRequest(t.Context(), outReq, nil); err != nil {
		t.Fatalf("layer.SendRequest(ctx, req, nil) error = %v, want nil", err)
	}

	if viaTp.requestCount() != 1 {
		t.Fatalf("expected use of transport referenced by %s:%s", viaTp.Proto(), viaTp.LocalAddr())
	}

	if defTp.requestCount() != 0 {
		t.Fatalf("unexpected use of default transport")
	}
}

func TestTransportLayer_SendRequestDefault(t *testing.T) {
	t.Parallel()

	layer := &sip.TransportLayer{}
	defTp := newStubTransport("UDP", 5060)
	if err := layer.TrackTransport(defTp, true); err != nil {
		t.Fatalf("layer.TrackTransport(UDP) error = %v, want nil", err)
	}

	req := &sip.Request{}
	outReq := sip.NewOutboundRequest(req)
	outReq.SetLocalAddr(netip.MustParseAddrPort("127.0.0.1:5070"))

	if got := defTp.requestCount(); got != 0 {
		t.Fatalf("unexpected sends on transport: got %d, want 0", got)
	}

	if err := layer.SendRequest(t.Context(), outReq, nil); err != nil {
		t.Fatalf("layer.SendRequest(ctx, req, nil) error = %v, want nil", err)
	}

	if defTp.requestCount() != 1 {
		t.Fatalf("expected use of default transport")
	}
}

func TestTransportLayer_SendResponse(t *testing.T) {
	t.Parallel()

	layer := &sip.TransportLayer{}
	defTp := newStubTransport("UDP", 5060)
	viaTp := newStubTransport("TCP", 5060)

	if err := layer.TrackTransport(defTp, true); err != nil {
		t.Fatalf("layer.TrackTransport(UDP) error = %v, want nil", err)
	}
	if err := layer.TrackTransport(viaTp, false); err != nil {
		t.Fatalf("layer.TrackTransport(TCP) error = %v, want nil", err)
	}

	res := &sip.Response{
		Headers: make(sip.Headers).Set(header.Via{{Transport: viaTp.Proto()}}),
	}
	outRes := sip.NewOutboundResponse(res)
	outRes.SetLocalAddr(viaTp.LocalAddr())

	if err := layer.SendResponse(t.Context(), outRes, nil); err != nil {
		t.Fatalf("layer.SendResponse(ctx, res, nil) error = %v, want nil", err)
	}

	if viaTp.responseCount() != 1 {
		t.Fatalf("expected use of transport referenced by %s:%s", viaTp.Proto(), viaTp.LocalAddr())
	}

	if defTp.responseCount() != 0 {
		t.Fatalf("unexpected use of default transport")
	}
}

func TestTransportLayer_SendResponseDefault(t *testing.T) {
	t.Parallel()
	layer := &sip.TransportLayer{}
	defTp := newStubTransport("UDP", 5060)
	if err := layer.TrackTransport(defTp, true); err != nil {
		t.Fatalf("layer.TrackTransport(UDP) error = %v, want nil", err)
	}

	res := &sip.Response{}
	outRes := sip.NewOutboundResponse(res)
	outRes.SetLocalAddr(netip.MustParseAddrPort("127.0.0.1:5070"))

	if got := defTp.responseCount(); got != 0 {
		t.Fatalf("unexpected sends on transport: got %d, want 0", got)
	}

	if err := layer.SendResponse(t.Context(), outRes, nil); err != nil {
		t.Fatalf("layer.SendResponse(ctx, res, nil) error = %v, want nil", err)
	}

	if defTp.responseCount() != 1 {
		t.Fatalf("expected use of default transport")
	}
}
