package sip_test

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestElement(t *testing.T) {
	tp, err := sip.NewConnlessTransport(
		sip.UDPTransportMetadata(),
		&sip.ConnlessTransportOptions{
			TransportOptions: sip.TransportOptions{
				Logger: log.Console(),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	rmtConn, err := net.ListenPacket("udp", "127.0.0.1:5070")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		log.Console().Info("starting listener...")

		err := tp.ListenAndServe(t.Context(), "127.0.0.1:5060")

		log.Console().Info("listener stopped", "error", err)
	})
	wg.Go(func() {
		log.Console().Info("starting remote conn reader...")

		buf := make([]byte, sip.MaxMessageSize)
		for {
			n, addr, err := rmtConn.ReadFrom(buf)
			if err != nil {
				log.Console().Info("remote conn reader stopped", "error", err)
				return
			}

			log.Console().Info("received message", "addr", addr, "message", string(buf[:n]))
		}
	})

	elm, err := sip.NewElement(
		"QwertY",
		tp,
		&sip.ElementOptions{
			TransactionManagerOptions: &sip.TransactionManagerOptions{},
			Logger:                    log.Console(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	elm.TransportManager().UseInterceptor(sip.StdMessageInterceptor{
		InboundRequest: sip.InboundRequestInterceptorFunc(func(ctx context.Context, req *sip.InboundRequestEnvelope, next sip.RequestReceiver) error {
			log.Console().Info("intercepted request")
			return nil
		}),
	})

	time.Sleep(time.Second)

	if _, err := rmtConn.WriteTo(
		[]byte(
			"INVITE sip:bob@b.example.com SIP/2.0\r\n"+
				"Via: SIP/2.0/UDP a.example.com;branch=qwerty,\r\n"+
				"\tSIP/2.0/UDP b.example.com;branch=asdf\r\n"+
				"Via: SIP/2.0/UDP c.example.com;branch=zxcvb\r\n"+
				"From: <sip:alice@a.example.com>;tag=abc\r\n"+
				"To: sip:bob@b.example.com\r\n"+
				"CSeq: 1 INVITE\r\n"+
				"Call-ID: zxc\r\n"+
				"Max-Forwards: 70\r\n"+
				"Contact: <sip:alice@a.example.com:5060>;transport=tcp\r\n"+
				"X-Generic-Header: 123\r\n"+
				"Content-Type: text/plain\r\n"+
				"Content-Length: 14\r\n"+
				"P-Custom-Header: 123 abc\r\n"+
				"\r\n"+
				"Hello world!\r\n",
		),
		&net.UDPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: 5060,
		},
	); err != nil {
		t.Fatal(err)
	}

	time.Sleep(time.Second)

	req, err := sip.NewOutboundRequestEnvelope(
		&sip.Request{
			Proto:  sip.ProtoVer20(),
			Method: sip.RequestMethodInfo,
			URI: &uri.SIP{
				User: uri.User("test"),
				Addr: uri.AddrFromHostPort("127.0.0.1", 5070),
			},
			Headers: make(sip.Headers).
				Set(
					&header.From{
						URI: &uri.SIP{
							User: uri.User("alice"),
							Addr: uri.AddrFromHost("a.example.com"),
						},
						Params: make(header.Values).Append("tag", "abc"),
					},
					&header.To{
						URI: &uri.SIP{
							User: uri.User("bob"),
							Addr: uri.AddrFromHost("b.example.com"),
						},
					},
				).
				Set(
					&header.CSeq{SeqNum: 1, Method: "INVITE"},
					header.CallID("zxc"),
					header.MaxForwards(70),
				),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	req.SetRemoteAddr(netip.MustParseAddrPort("127.0.0.1:5070"))

	if err := elm.SendRequest(t.Context(), req, &sip.SendRequestOptions{RenderCompact: true}); err != nil {
		t.Fatalf("%+v", err)
	}

	time.Sleep(time.Second)

	rmtConn.Close()
	elm.Close()
}
