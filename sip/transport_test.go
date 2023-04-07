package sip_test

import (
	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("SIP", Label("sip", "transport"), func() {
	It("test", func() {
		// h := &sip.ContentType{
		// 	Type: "text", Subtype: "plain", Params: make(sip.Values).Set("charset", "utf-8"),
		// }
		// fmt.Println(h)
		//
		// tp := &sip.Transport{
		// 	Host: "a.example.com",
		// 	Log:  log.NewDefaultLogger("test", log.LevelDebug, os.Stdout),
		// }
		//
		// udpConn, err := net.ListenPacket("udp", "127.0.0.1:5060")
		// Expect(err).ToNot(HaveOccurred())
		//
		// var wg sync.WaitGroup
		// wg.Add(2)
		// go func() {
		// 	defer wg.Done()
		// 	err := tp.ServePacketConn(sip.TransportProtoUDP, udpConn, func(msg sip.Message) {
		// 		fmt.Println("got message")
		// 		fmt.Print(msg.RenderMessage())
		// 		fmt.Println(msg.MessageMetadata())
		// 		fmt.Println()
		// 	})
		// 	fmt.Println("serve error", errors.Is(err, net.ErrClosed), err)
		// }()
		// go func() {
		// 	defer wg.Done()
		// 	time.Sleep(time.Second)
		//
		// 	conn, err := net.Dial("udp", "127.0.0.1:5060")
		// 	Expect(err).ToNot(HaveOccurred())
		//
		// 	_, err = conn.Write([]byte("INVITE sip:bob@b.example.com SIP/2.0\r\n" +
		// 		"Via: SIP/2.0/UDP b.example.com;branch=qwerty;rport\r\n" +
		// 		"From: <sip:bob@b.example.com>;tag=abc\r\n" +
		// 		"To: sip:alice@a.example.com\r\n" +
		// 		"CSeq: 1 INVITE\r\n" +
		// 		"Call-ID: QwertY\r\n" +
		// 		"Max-Forwards: 70\r\n" +
		// 		"\r\n"))
		//
		// 	time.Sleep(time.Second)
		//
		// 	_, err = conn.Write([]byte(
		// 		"SIP/2.0 200 OK\r\n" +
		// 			"Via: SIP/2.0/UDP a.example.com;branch=qwerty\r\n" +
		// 			"From: <sip:alice@a.example.com>;tag=abc\r\n" +
		// 			"To: <sip:bob@b.example.com>;tag=def\r\n" +
		// 			"CSeq: 1 INVITE\r\n" +
		// 			"Call-ID: zxc\r\n" +
		// 			"Max-Forwards: 70\r\n" +
		// 			"Content-Length: 0\r\n" +
		// 			"\r\n",
		// 	))
		// 	Expect(err).ToNot(HaveOccurred())
		// 	conn.Close()
		//
		// 	time.Sleep(time.Second)
		//
		// 	udpConn.Close()
		// }()
		// wg.Wait()
	})
})
