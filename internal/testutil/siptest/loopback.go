package siptest

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil/dnstest"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/transport"
)

// NewLoopbackUDPElement creates an element serving a tracked
// connectionless transport on a kernel-assigned loopback socket. meta
// selects the transport metadata (for example sip.UDPMetadata(), or a
// secured-marked variant for sips-target tests without a real TLS
// stack). The listener serves detached on the test context; cleanup
// closes the listener, joins the serve goroutine, and shuts the element
// down under a bounded detached context.
func NewLoopbackUDPElement(tb testing.TB, meta sip.TransportMetadata) (*sip.Element, netip.AddrPort) {
	tb.Helper()

	const host = "127.0.0.1"
	const cleanupTimeout = 10 * time.Second

	elm := &sip.Element{
		DNSResolver: dnstest.StubResolver{
			LookupIPFunc: func(context.Context, string, string) ([]net.IP, error) {
				return []net.IP{net.ParseIP(host)}, nil
			},
		},
	}
	tb.Cleanup(func() {
		// The test context is already canceled in cleanup, so detach it
		// while still bounding the shutdown.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(tb.Context()), cleanupTimeout)
		defer cancel()
		if err := elm.Shutdown(ctx); err != nil {
			tb.Errorf("elm.Shutdown() error = %v, want nil", err)
		}
	})

	tp, err := transport.NewConnLessTransport(meta, transport.TransportOptions{
		PublicAddr: sip.MakeHostAddr(host),
	})
	if err != nil {
		tb.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
	}
	if err := elm.TrackTransport(tp); err != nil {
		tb.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}
	lis, err := elm.Listen(tb.Context(), "UDP", host+":0")
	if err != nil {
		tb.Fatalf("elm.Listen() error = %v, want nil", err)
	}

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- lis.Serve(context.WithoutCancel(tb.Context()))
	}()
	tb.Cleanup(func() {
		if err := lis.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			tb.Errorf("lis.Close() error = %v, want nil", err)
		}
		select {
		case err := <-serveDone:
			if err != nil &&
				!errors.Is(err, net.ErrClosed) &&
				!errors.Is(err, context.Canceled) &&
				!errors.Is(err, transport.ErrTransportClosed) &&
				!errors.Is(err, transport.ErrNetworkClosed) {
				tb.Errorf("lis.Serve() error = %v, want transport closed", err)
			}
		case <-time.After(cleanupTimeout):
			tb.Error("lis.Serve() did not stop within the cleanup timeout")
		}
	})

	return elm, lis.LocalAddr()
}
