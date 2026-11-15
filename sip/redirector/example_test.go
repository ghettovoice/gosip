package redirector_test

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/redirector"
	"github.com/ghettovoice/gosip/sip/transport"
)

// ExampleNew runs a redirector end to end over loopback UDP: an element
// with a single transport and listener answers a raw INVITE with the
// route's resolved contact.
//
// The element wiring — track one UDP transport, listen on a
// kernel-assigned port, register the component — is the same shape a
// production application uses; only the raw [net.Dial] peer is specific
// to the example.
func ExampleNew() {
	elm := &sip.Element{}
	defer func() { _ = elm.Close() }()

	tp, err := transport.NewConnLessTransport(
		sip.UDPMetadata(),
		transport.TransportOptions{PublicAddr: sip.MakeHostAddr("127.0.0.1")},
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if err := elm.TrackTransport(tp); err != nil {
		fmt.Println("error:", err)
		return
	}

	lis, err := elm.Listen(context.Background(), "UDP", "127.0.0.1:0")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		// Serve blocks until the listener is closed below; it reports
		// that shutdown as an error, which is expected here.
		_ = lis.Serve(context.Background())
	}()
	defer func() {
		_ = lis.Close()
		<-serveDone
	}()

	rdr, err := redirector.New([]redirector.Route{
		{
			Name: "static-location",
			// A nil matcher accepts every request at this route's
			// position, so the route resolves one fixed contact for
			// whatever it claims.
			ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
				return header.Contact{
					{
						URI: &sip.URI{
							User: sip.MakeUserInfo("alice"),
							Addr: sip.MakeHostPortAddr("192.0.2.10", 5060),
						},
					},
				}, nil
			},
		},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	unbind := elm.UseComponent(rdr)
	defer func() {
		_ = rdr.Close()
		unbind()
	}()

	peer, err := net.Dial("udp", lis.LocalAddr().String())
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer func() { _ = peer.Close() }()

	// The rport Via parameter asks the element to answer on the packet's
	// own source address (RFC 3581), so the response reaches this socket
	// without the Via sent-by being a routable address.
	invite, err := sip.NewRequest(
		sip.RequestMethodInvite,
		&sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("example.com")},
		&sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("example.net")},
		&sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("example.com")},
		sip.RequestOptions{RPort: true},
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if _, err := peer.Write([]byte(invite.Render())); err != nil {
		fmt.Println("error:", err)
		return
	}

	// The deadline bounds the read: an unanswered request surfaces as a
	// timeout instead of hanging the example. It is generous rather than
	// tight so that a response delayed by scheduling under -race and
	// parallel package binaries still arrives inside the deadline.
	if err := peer.SetReadDeadline(time.Now().Add(4 * time.Second)); err != nil {
		fmt.Println("error:", err)
		return
	}
	buf := make([]byte, 64*1024)
	num, err := peer.Read(buf)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	msg, err := sip.DefaultParser().ParsePacket(buf[:num])
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	res, ok := msg.(*sip.Response)
	if !ok {
		fmt.Println("error: unexpected message", msg)
		return
	}
	resEnv := sip.NewResponseEnvelope(res)
	fmt.Println(int(resEnv.Status()))

	// Output:
	// 302
}
