package main

import (
	"fmt"
	"net"
	"time"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/transport"
	"github.com/ghettovoice/gosip/util"
)

func main() {
	log.SetLevel(log.DebugLevel)

	output := make(chan *transport.IncomingMessage)
	errs := make(chan error)
	cancel := make(chan struct{})
	client, server := net.Pipe()
	conn := transport.NewConnection(server)
	addr := "127.0.0.1:5060"
	key := transport.ConnectionKey(addr)
	handler := transport.NewConnectionHandler(key, conn, 0, output, errs, cancel)

	inviteMsg := "INVITE sip:bob@far-far-away.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP pc33.far-far-away.com;branch=z9hG4bK776asdhds\r\n" +
		"To: \"Bob\" <sip:bob@far-far-away.com>\r\n" +
		"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774\r\n" +
		"Content-Length: 12\r\n" +
		"\r\n" +
		"Hello world!\r\n"
	invalidContentLengthMsg := "INVITE sip:bob@biloxi.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP pc33.atlanta.com;branch=z9hG4bK776asdhds\r\n" +
		"To: \"Bob\" <sip:bob@biloxi.com>\r\n" +
		"From: \"Alice\" <sip:alice@atlanta.com>;tag=1928301774\r\n" +
		"\r\n" +
		"Message with wrong content length\r\n"

	go handler.Serve(util.Noop)
	go func() {
		client.Write([]byte(inviteMsg))
		time.Sleep(time.Millisecond)
		client.Write([]byte(invalidContentLengthMsg))
	}()

	for i := 0; i < 2; i++ {
		select {
		case msg := <-output:
			fmt.Println()
			fmt.Println(msg)
			fmt.Println()
		case err := <-errs:
			fmt.Println()
			fmt.Println(err)
			fmt.Println()
		}
	}
}
