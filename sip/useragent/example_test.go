package useragent_test

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil/dnstest"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/transport"
	"github.com/ghettovoice/gosip/sip/useragent"
)

// newExampleElement creates an element serving a tracked connectionless
// UDP transport on a loopback socket; the stub resolver answers every
// lookup with loopback, so targets need no real DNS. The listener binds
// on a kernel-assigned port and serves detached on ctx until its Close.
//
// The returned cleanup tears the element down in reverse order: it
// closes the listener, joins the Serve goroutine, then shuts the element
// down under a fresh bounded context — the caller's ctx may already be
// done by then. A construction failure cleans up whatever was already
// set up before reporting the error.
//
//nolint:revive // the element wiring needs four returns: element, listen addr, cleanup, error
func newExampleElement(ctx context.Context) (*sip.Element, netip.AddrPort, func() error, error) {
	elm := &sip.Element{
		DNSResolver: &dnstest.StubResolver{
			LookupIPFunc: func(context.Context, string, string) ([]net.IP, error) {
				return []net.IP{net.ParseIP("127.0.0.1")}, nil
			},
		},
	}
	tp, err := transport.NewConnLessTransport(
		sip.UDPMetadata(),
		transport.TransportOptions{PublicAddr: sip.MakeHostAddr("127.0.0.1")},
	)
	if err != nil {
		return nil, netip.AddrPort{}, nil, errors.Wrap(err)
	}
	if err := elm.TrackTransport(tp); err != nil {
		return nil, netip.AddrPort{}, nil, errors.JoinWrap(err, tp.Close())
	}
	lis, err := elm.Listen(ctx, "UDP", "127.0.0.1:0")
	if err != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		shutErr := elm.Shutdown(shutCtx)
		cancel()
		return nil, netip.AddrPort{}, nil, errors.JoinWrap(err, shutErr)
	}

	serveDone := make(chan error, 1)
	go func() { serveDone <- lis.Serve(ctx) }()

	cleanup := func() error {
		var errs []error
		if err := lis.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, errors.Wrap(err))
		}
		select {
		case err := <-serveDone:
			// Closing the listener ends Serve; that shutdown error is
			// expected and filtered with the transport closed sentinels.
			if err != nil &&
				!errors.Is(err, net.ErrClosed) &&
				!errors.Is(err, context.Canceled) &&
				!errors.Is(err, transport.ErrTransportClosed) &&
				!errors.Is(err, transport.ErrNetworkClosed) {
				errs = append(errs, errors.Wrap(err))
			}
		case <-time.After(10 * time.Second):
			errs = append(errs, errors.ErrorWrap("listener serve did not stop within 10s"))
		}
		// The passed-in context may be done already, so the shutdown
		// runs under a fresh bounded one.
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := elm.Shutdown(shutCtx); err != nil {
			errs = append(errs, errors.Wrap(err))
		}
		return errors.JoinWrap(errs...)
	}

	return elm, lis.LocalAddr(), cleanup, nil
}

// ExampleUserAgent_Message runs a MESSAGE operation end to end over
// loopback UDP: two real elements each bound to a user agent, where the
// peer's OnMessage handler answers the ping with 200 OK.
//
// The element wiring — track one UDP transport, listen on a
// kernel-assigned port, serve detached, register the component — is the
// same shape a production application uses. Examples carry no test
// context, so the operation context below is an explicit bounded
// context.Background derivative; the repository test guide documents
// that exception.
func ExampleUserAgent_Message() {
	elmAlice, _, cleanupAlice, err := newExampleElement(context.Background())
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer func() {
		if err := cleanupAlice(); err != nil {
			fmt.Println("error:", err)
		}
	}()
	elmBob, addrBob, cleanupBob, err := newExampleElement(context.Background())
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer func() {
		if err := cleanupBob(); err != nil {
			fmt.Println("error:", err)
		}
	}()

	bob, err := sip.ParseURI("sip:bob@example.net")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	uaBob, err := useragent.New(useragent.Options{
		AOR: bob,
		OnMessage: func(ctx context.Context, in *useragent.IncomingMessage) error {
			return in.Respond(ctx, sip.ResponseStatusOK)
		},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	// UseComponent returns the registration unbind, not an error: hold
	// it and detach after sealing the component.
	unbindBob := elmBob.UseComponent(uaBob)
	defer func() {
		if err := uaBob.Close(); err != nil {
			fmt.Println("error:", err)
		}
		unbindBob()
	}()

	alice, err := sip.ParseURI("sip:alice@example.com")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	uaAlice, err := useragent.New(useragent.Options{AOR: alice})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	unbindAlice := elmAlice.UseComponent(uaAlice)
	defer func() {
		if err := uaAlice.Close(); err != nil {
			fmt.Println("error:", err)
		}
		unbindAlice()
	}()

	// The operation context carries the sending element and bounds the
	// whole exchange.
	opCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()

	target := &sip.URI{
		User: sip.MakeUserInfo("bob"),
		Addr: sip.MakeHostPortAddr(addrBob.Addr().String(), addrBob.Port()),
	}
	op, err := uaAlice.Message(
		sip.ContextWithElement(opCtx, elmAlice),
		target,
		useragent.MessageOperationOptions{Body: []byte("ping")},
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	res, err := op.Wait(opCtx)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(int(res.Status()))

	// Output:
	// 200
}
