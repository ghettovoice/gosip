package sip_test

import (
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/uri"
)

func TestTmp(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:5060")
	if err != nil {
		t.Fatalf("failed to listen: %+v", err)
	}
	defer conn.Close()

	rmtConn, err := net.ListenPacket("udp", "127.0.0.1:6060")
	if err != nil {
		t.Fatalf("failed to setup remote conn: %+v", err)
	}
	defer rmtConn.Close()
	go func() {
		buf := make([]byte, sip.MaxMsgSize)
		for {
			n, addr, err := rmtConn.ReadFrom(buf)
			if err != nil {
				return
			}

			t.Logf("packet on remote from %s:\n%s", addr, buf[:n])

			time.Sleep(time.Second)

			_, err = rmtConn.WriteTo([]byte(
				"OPTIONS sip:local@127.0.0.1:5060 SIP/2.0\r\n"+
					"Via: SIP/2.0/UDP 127.0.0.1:6060;branch=z9hG4bK-123456\r\n"+
					"From: <sip:local@127.0.0.1:6060>;tag=123456\r\n"+
					"To: <sip:remote@127.0.0.1:5060>\r\n"+
					"Call-ID: call-123456\r\n"+
					"CSeq: 1 OPTIONS\r\n"+
					"Max-Forwards: 70\r\n"+
					"\r\n",
			), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060})
			if err != nil {
				t.Errorf("failed to write to remote conn: %+v", err)
			}

			t.Log("remote sent packet")

			return //nolint:staticcheck
		}
	}()

	tp, err := sip.NewUnreliableTransport("UDP", conn, &sip.UnreliableTransportOptions{
		Logger: log.Console(),
	})
	if err != nil {
		t.Fatalf("failed to setup transport: %+v", err)
	}

	elm, err := sip.NewElement("GoSIP", &sip.ElementOptions{
		Logger: log.Console(),
	})
	if err != nil {
		t.Fatalf("failed to setup element: %+v", err)
	}

	tp.UseInterceptor(elm)

	go func() {
		if err := tp.Serve(t.Context()); err != nil && !errors.Is(err, sip.ErrTransportClosed) {
			t.Errorf("failed to serve transport: %+v", err)
		}
	}()
	defer tp.Close(t.Context())

	time.Sleep(5 * time.Second)

	req, err := sip.NewRequest(
		sip.RequestMethodOptions,
		&uri.SIP{
			User: uri.User("remote"),
			Addr: uri.HostPort("127.0.0.1", 6060),
		},
		&uri.SIP{
			User: uri.User("local"),
			Addr: uri.HostPort("127.0.0.1", 5060),
		},
		&uri.SIP{
			User: uri.User("remote"),
			Addr: uri.HostPort("127.0.0.1", 6060),
		},
		nil,
	)
	if err != nil {
		t.Fatalf("failed to build request: %+v", err)
	}
	outReq, err := sip.NewOutboundRequestEnvelope(req)
	if err != nil {
		t.Fatalf("failed to build outbound request envelope: %+v", err)
	}
	outReq.SetRemoteAddr(netip.MustParseAddrPort("127.0.0.1:6060"))

	if err := tp.SendRequest(t.Context(), outReq, nil); err != nil {
		t.Fatalf("failed to send outbound request: %+v", err)
	}

	time.Sleep(5 * time.Second)
}
