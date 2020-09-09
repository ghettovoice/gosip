package transport_test

import (
	"fmt"
	"net"
	"sync"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/testutils"
	"github.com/ghettovoice/gosip/transport"
)

var _ = Describe("TransportLayer", func() {
	var (
		tpl    transport.Layer
		wg     *sync.WaitGroup
		client net.Conn
	)
	ip := "127.0.0.1"
	localAddr1 := "127.0.0.1:5060"
	clientPort := sip.Port(9001)
	clientHost := "127.0.0.1"
	clientAddr := clientHost + ":" + fmt.Sprintf("%v", clientPort)
	logger := testutils.NewLogrusLogger()

	BeforeEach(func() {
		wg = new(sync.WaitGroup)
		tpl = transport.NewLayer(net.ParseIP(ip), net.DefaultResolver, nil, logger)
	})
	AfterEach(func(done Done) {
		wg.Wait()
		tpl.Cancel()
		<-tpl.Done()
		close(done)
	}, 3)

	Context("just initialized", func() {
	})

	Context(fmt.Sprintf("listens UDP and TCP %s", localAddr1), func() {
		BeforeEach(func() {
			Expect(tpl.Listen("udp", localAddr1, nil))
			Expect(tpl.Listen("tcp", localAddr1, nil))
		})

		Context("when remote UDP client sends INVITE request", func() {
			var server net.PacketConn
			var err error
			network := "udp"
			msg := "INVITE sip:bob@far-far-away.com SIP/2.0\r\n" +
				"Via: SIP/2.0/UDP pc33.far-far-away.com:" + fmt.Sprintf("%v", clientPort) + ";branch=z9hG4bK776asdhds\r\n" +
				"To: \"Bob\" <sip:bob@far-far-away.com>\r\n" +
				"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774\r\n" +
				"Content-Length: 12\r\n" +
				"\r\n" +
				"Hello world!"
			expectedMsg := "INVITE sip:bob@far-far-away.com SIP/2.0\r\n" +
				"Via: SIP/2.0/UDP pc33.far-far-away.com:" + fmt.Sprintf("%v", clientPort) + ";branch=z9hG4bK776asdhds;received=%s\r\n" +
				"To: \"Bob\" <sip:bob@far-far-away.com>\r\n" +
				"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774\r\n" +
				"Content-Length: 12\r\n" +
				"\r\n" +
				"Hello world!"

			BeforeEach(func() {
				client = testutils.CreateClient(network, localAddr1, clientAddr)
				server, err = net.ListenPacket(network, clientAddr)
				Expect(err).ToNot(HaveOccurred())
				wg.Add(1)
				go func() {
					defer wg.Done()
					testutils.WriteToConn(client, []byte(msg))
				}()
			})
			AfterEach(func() {
				client.Close()
				server.Close()
			})

			It("should process request (add 'received' param) and emit on Message chan", func(done Done) {
				testutils.AssertMessageArrived(tpl.Messages(), fmt.Sprintf(expectedMsg, clientHost), clientAddr,
					"far-far-away.com:5060")
				close(done)
			}, 3)

			Context("after request received", func() {
				var incomingRequest sip.Message
				var response sip.Message

				BeforeEach(func() {
					incomingRequest = testutils.AssertMessageArrived(
						tpl.Messages(),
						fmt.Sprintf(expectedMsg, clientHost),
						clientAddr,
						"far-far-away.com:5060",
					)
					response = sip.NewResponseFromRequest(
						"",
						incomingRequest.(sip.Request),
						200,
						"OK",
						"",
					)
				})

				It("should send response to client without error", func(done Done) {
					twg := new(sync.WaitGroup)
					twg.Add(1)
					go func() {
						defer twg.Done()
						By(fmt.Sprintf("remote server waits response on %s", server.LocalAddr()))
						buf := make([]byte, 65535)
						for {
							num, err := server.(net.Conn).Read(buf)
							Expect(err).ToNot(HaveOccurred())
							Expect(num).To(Equal(len(response.String())))
							data := append([]byte{}, buf[:num]...)
							Expect(string(data)).To(Equal(response.String()))
							return
						}
					}()
					time.Sleep(time.Second)
					By(fmt.Sprintf("tpl sends response to %s", response.Destination()))
					Expect(tpl.Send(response)).ToNot(HaveOccurred())

					twg.Wait()
					close(done)
				}, 3)
			})
		})

		Context("when remote TCP client sends INVITE request", func() {
			network := "tcp"
			msg := "INVITE sip:bob@far-far-away.com SIP/2.0\r\n" +
				"Via: SIP/2.0/TCP pc33.far-far-away.com:" + fmt.Sprintf("%v", clientPort) + ";branch=z9hG4bK776asdhds\r\n" +
				"To: \"Bob\" <sip:bob@far-far-away.com>\r\n" +
				"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774\r\n" +
				"Content-Length: 12\r\n" +
				"\r\n" +
				"Hello world!"
			expectedMsg := "INVITE sip:bob@far-far-away.com SIP/2.0\r\n" +
				"Via: SIP/2.0/TCP pc33.far-far-away.com:" + fmt.Sprintf("%v", clientPort) + ";branch=z9hG4bK776asdhds;received=%s\r\n" +
				"To: \"Bob\" <sip:bob@far-far-away.com>\r\n" +
				"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774\r\n" +
				"Content-Length: 12\r\n" +
				"\r\n" +
				"Hello world!"

			BeforeEach(func() {
				client = testutils.CreateClient(network, localAddr1, "")
				wg.Add(1)
				go func() {
					defer wg.Done()
					time.Sleep(100 * time.Millisecond)
					testutils.WriteToConn(client, []byte(msg))
				}()
			})
			AfterEach(func() {
				client.Close()
			})

			It("should process request (add 'received' param) and emit on Message chan", func(done Done) {
				testutils.AssertMessageArrived(tpl.Messages(), fmt.Sprintf(expectedMsg, clientHost), client.LocalAddr().String(),
					"far-far-away.com:5060")
				close(done)
			}, 3)

			Context("after request received", func() {
				var incomingRequest sip.Message
				var response sip.Message

				BeforeEach(func() {
					incomingRequest = testutils.AssertMessageArrived(tpl.Messages(), fmt.Sprintf(expectedMsg, clientHost),
						client.LocalAddr().String(), "far-far-away.com:5060")
					response = sip.NewResponseFromRequest(
						"",
						incomingRequest.(sip.Request),
						200,
						"OK",
						"",
					)
				})

				It("should send response to client without error", func(done Done) {
					twg := new(sync.WaitGroup)
					twg.Add(1)
					go func() {
						defer twg.Done()
						By(fmt.Sprintf("remote client waits response on %s", client.LocalAddr()))

						buf := make([]byte, 65535)
						for {
							num, err := client.Read(buf)
							Expect(err).ToNot(HaveOccurred())
							Expect(num).To(Equal(len(response.String())))
							data := append([]byte{}, buf[:num]...)
							Expect(string(data)).To(Equal(response.String()))
							return
						}
					}()
					time.Sleep(time.Second)
					By(fmt.Sprintf("tpl sends response to %s", response.Destination()))
					Expect(tpl.Send(response)).ToNot(HaveOccurred())

					twg.Wait()
					close(done)
				}, 3)
			})
		})

		Context("when cancels", func() {
			BeforeEach(func() {
				time.Sleep(time.Millisecond)
				tpl.Cancel()
			})
			It("should resolve Done chan", func(done Done) {
				<-tpl.Done()
				close(done)
			}, 3)
		})
	})
})
