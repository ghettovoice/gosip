package transp_test

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/testutils"
	"github.com/ghettovoice/gosip/transp"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("TransportLayer", func() {
	var (
		tpl    transp.Layer
		wg     *sync.WaitGroup
		client net.Conn
	)
	hostAddr := "192.168.0.1:5060"
	localAddr1 := "127.0.0.1:5060"
	clientPort := core.Port(9001)
	clientHost := "127.0.0.1"
	clientAddr := clientHost + ":" + fmt.Sprintf("%v", clientPort)

	BeforeEach(func() {
		wg = new(sync.WaitGroup)
		tpl = transp.NewLayer(hostAddr)
	})
	AfterEach(func(done Done) {
		wg.Wait()
		tpl.Cancel()
		<-tpl.Done()
		close(done)
	}, 3)

	Context("just initialized", func() {
		It("should has correct host address", func() {
			Expect(tpl.HostAddr()).To(Equal(hostAddr))
		})
	})

	Context(fmt.Sprintf("listens UDP and TCP %s", localAddr1), func() {
		BeforeEach(func() {
			Expect(tpl.Listen("udp", localAddr1))
			Expect(tpl.Listen("tcp", localAddr1))
		})

		Context("when remote UDP client sends INVITE request", func() {
			var server net.PacketConn
			var err error
			network := "udp"
			msg := "INVITE sip:bob@far-far-away.com SIP/2.0\r\n" +
				"Via: SIP/2.0/UDP pc33.far-far-away.com;branch=z9hG4bK776asdhds\r\n" +
				"To: \"Bob\" <sip:bob@far-far-away.com>\r\n" +
				"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774\r\n" +
				"Content-Length: 12\r\n" +
				"\r\n" +
				"Hello world!"
			expectedMsg := "INVITE sip:bob@far-far-away.com SIP/2.0\r\n" +
				"Via: SIP/2.0/UDP pc33.far-far-away.com;branch=z9hG4bK776asdhds;received=%s\r\n" +
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
				testutils.AssertIncomingMessageArrived(tpl.Messages(), fmt.Sprintf(expectedMsg, clientHost), localAddr1,
					client.LocalAddr().String())
				close(done)
			}, 3)

			Context("after request received", func() {
				var incomingRequest *transp.IncomingMessage
				var response core.Message

				BeforeEach(func() {
					incomingRequest = testutils.AssertIncomingMessageArrived(tpl.Messages(), fmt.Sprintf(expectedMsg, clientHost), localAddr1,
						client.LocalAddr().String())
					response = core.NewResponse(
						incomingRequest.Msg.SipVersion(),
						200,
						"OK",
						[]core.Header{},
						"",
					)
					core.CopyHeaders("Via", incomingRequest.Msg, response)
					core.CopyHeaders("From", incomingRequest.Msg, response)
					core.CopyHeaders("To", incomingRequest.Msg, response)
					core.CopyHeaders("Call-Id", incomingRequest.Msg, response)
					core.CopyHeaders("CSeq", incomingRequest.Msg, response)
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
					By(fmt.Sprintf("tpl sends response to %s", clientAddr))
					Expect(tpl.Send(clientAddr, response)).ToNot(HaveOccurred())

					twg.Wait()
					close(done)
				}, 3)
			})
		})

		Context("when remote TCP client sends INVITE request", func() {
			network := "tcp"
			msg := "INVITE sip:bob@far-far-away.com SIP/2.0\r\n" +
				"Via: SIP/2.0/TCP pc33.far-far-away.com;branch=z9hG4bK776asdhds\r\n" +
				"To: \"Bob\" <sip:bob@far-far-away.com>\r\n" +
				"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774\r\n" +
				"Content-Length: 12\r\n" +
				"\r\n" +
				"Hello world!"
			expectedMsg := "INVITE sip:bob@far-far-away.com SIP/2.0\r\n" +
				"Via: SIP/2.0/TCP pc33.far-far-away.com;branch=z9hG4bK776asdhds;received=%s\r\n" +
				"To: \"Bob\" <sip:bob@far-far-away.com>\r\n" +
				"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774\r\n" +
				"Content-Length: 12\r\n" +
				"\r\n" +
				"Hello world!"

			BeforeEach(func() {
				client = testutils.CreateClient(network, localAddr1, clientAddr)
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
				testutils.AssertIncomingMessageArrived(tpl.Messages(), fmt.Sprintf(expectedMsg, clientHost), localAddr1,
					client.LocalAddr().String())
				close(done)
			}, 3)

			Context("after request received", func() {
				var incomingRequest *transp.IncomingMessage
				var response core.Message

				BeforeEach(func() {
					incomingRequest = testutils.AssertIncomingMessageArrived(tpl.Messages(), fmt.Sprintf(expectedMsg, clientHost), localAddr1,
						client.LocalAddr().String())
					response = core.NewResponse(
						incomingRequest.Msg.SipVersion(),
						200,
						"OK",
						[]core.Header{},
						"",
					)
					core.CopyHeaders("Via", incomingRequest.Msg, response)
					core.CopyHeaders("From", incomingRequest.Msg, response)
					core.CopyHeaders("To", incomingRequest.Msg, response)
					core.CopyHeaders("Call-Id", incomingRequest.Msg, response)
					core.CopyHeaders("CSeq", incomingRequest.Msg, response)
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
					By(fmt.Sprintf("tpl sends response to %s", incomingRequest.RAddr))
					Expect(tpl.Send(incomingRequest.RAddr, response)).ToNot(HaveOccurred())

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
