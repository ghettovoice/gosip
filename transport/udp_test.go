package transport_test

import (
	"fmt"
	"net"
	"time"

	"github.com/ghettovoice/gosip/timing"
	"github.com/ghettovoice/gosip/transport"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("UdpProtocol", func() {
	var (
		output                    chan *transport.IncomingMessage
		errs                      chan error
		cancel                    chan struct{}
		protocol                  transport.Protocol
		client1, client2, client3 net.Conn
	)

	network := "udp"
	port1 := transport.DefaultUdpPort
	port2 := transport.DefaultUdpPort + 1
	localTarget1 := &transport.Target{network, transport.DefaultHost, &port1}
	localTarget2 := &transport.Target{network, transport.DefaultHost, &port2}
	clientAddr1 := "127.0.0.1:9001"
	clientAddr2 := "127.0.0.1:9002"
	clientAddr3 := "127.0.0.1:9003"
	msg1 := "INVITE sip:bob@far-far-away.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP pc33.far-far-away.com;branch=z9hG4bK776asdhds\r\n" +
		"To: \"Bob\" <sip:bob@far-far-away.com>\r\n" +
		"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774\r\n" +
		"Content-Length: 12\r\n" +
		"\r\n" +
		"Hello world!"
	msg2 := "BYE sip:bob@far-far-away.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP pc33.far-far-away.com;branch=z9hG4bK776asdhds\r\n" +
		"To: \"Alice\" <sip:bob@far-far-away.com>\r\n" +
		"From: \"Bob\" <sip:alice@wonderland.com>;tag=1928301774\r\n" +
		"Content-Length: 4\r\n" +
		"\r\n" +
		"Bye!"
	msg3 := "SIP/2.0 200 OK\r\n" +
		"CSeq: 2 INVITE\r\n" +
		"Call-Id: cheesecake1729\r\n" +
		"Max-Forwards: 65\r\n" +
		"\r\n"
	broken := "BROKEN from hell.com SIP/2.0\r\n" +
		"Via: HELL\r\n" +
		"\r\n" +
		"THIS MESSAGE FROM HELL!"
	bullshit := "This is bullshit!\r\n"

	timing.MockMode = true

	BeforeEach(func() {
		output = make(chan *transport.IncomingMessage)
		errs = make(chan error)
		cancel = make(chan struct{})
		protocol = transport.NewUdpProtocol(output, errs, cancel)
	})
	AfterEach(func() {
		defer func() { recover() }()
		close(cancel)
		close(output)
		close(errs)
		time.Sleep(100 * time.Millisecond)
	})

	Context("just initialized", func() {
		It("should has Network = UDP", func() {
			Expect(protocol.Network()).To(Equal("UDP"))
		})
		It("should not be reliable", func() {
			Expect(protocol.Reliable()).To(BeFalse())
		})
		It("should not be streamed", func() {
			Expect(protocol.Streamed()).To(BeFalse())
		})
	})

	Context(fmt.Sprintf("listening 2 target: %s, %s", localTarget1, localTarget2), func() {
		BeforeEach(func() {
			Expect(protocol.Listen(localTarget1)).To(Succeed())
			Expect(protocol.Listen(localTarget2)).To(Succeed())
			time.Sleep(time.Millisecond)
		})

		Context("3 clients connects and sends data", func() {
			BeforeEach(func() {
				client1 = createClient(network, localTarget1.Addr(), clientAddr1)
				client2 = createClient(network, localTarget2.Addr(), clientAddr2)
				client3 = createClient(network, localTarget1.Addr(), clientAddr3)

				go func() {
					defer client1.Close()
					time.Sleep(time.Millisecond)
					writeToConn(client1, []byte(msg1))
				}()
				go func() {
					defer client2.Close()
					time.Sleep(10 * time.Millisecond)
					writeToConn(client2, []byte(msg2))
					time.Sleep(20 * time.Millisecond)
					writeToConn(client2, []byte(bullshit))
					time.Sleep(50 * time.Millisecond)
					writeToConn(client2, []byte(msg2))
				}()
				go func() {
					defer client3.Close()
					time.Sleep(20 * time.Millisecond)
					writeToConn(client3, []byte(broken))
					time.Sleep(time.Millisecond)
					writeToConn(client3, []byte(msg3))
				}()
			})
			It("should pipe incoming messages and errors", func(done Done) {
				By(fmt.Sprintf("msg1 arrives on output from client1 %s -> %s", clientAddr1, localTarget1.Addr()))
				assertIncomingMessageArrived(output, msg1, localTarget1.Addr(), clientAddr1)
				By(fmt.Sprintf("msg2 arrives on output from client2 %s -> %s", clientAddr2, localTarget2.Addr()))
				assertIncomingMessageArrived(output, msg2, localTarget2.Addr(), clientAddr2)
				By(fmt.Sprintf("broken message arrives from client3 and ignored %s -> %s", clientAddr3, localTarget1.Addr()))
				time.Sleep(time.Millisecond)
				By(fmt.Sprintf("bullshit arrives from client2 and ignored %s -> %s", clientAddr2, localTarget2.Addr()))
				time.Sleep(time.Millisecond)
				By(fmt.Sprintf("msg3 arrives on output from client3 %s -> %s", clientAddr3, localTarget1.Addr()))
				assertIncomingMessageArrived(output, msg3, localTarget1.Addr(), clientAddr3)
				By(fmt.Sprintf("msg2 arrives on output from client2 %s -> %s", clientAddr2, localTarget2.Addr()))
				assertIncomingMessageArrived(output, msg2, localTarget2.Addr(), clientAddr2)

				//for i := 0; i < 4; i++ {
				//	select {
				//	case msg := <-output:
				//		fmt.Printf("\n-------------------------------\n%s\n-------------------------------------\n\n", msg)
				//	case err := <-errs:
				//		fmt.Printf("\n-------------------------------\n%s\n-------------------------------------\n\n", err)
				//	}
				//}
				close(done)
			}, 10)
		})

		Context("after cancel signal", func() {
			BeforeEach(func() {
				time.Sleep(time.Millisecond)
				close(cancel)
			})
			It("should resolve Done chan", func(done Done) {
				<-protocol.Done()
				close(done)
			})
		})
	})
})
