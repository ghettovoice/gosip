package transport_test

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/transport"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("UdpProtocol", func() {
	var (
		output   chan *transport.IncomingMessage
		errs     chan error
		cancel   chan struct{}
		protocol transport.Protocol
	)

	network := "udp"
	port1 := transport.DefaultUdpPort
	port2 := transport.DefaultUdpPort + 1
	localTarget1 := &transport.Target{network, transport.DefaultHost, &port1}
	localTarget2 := &transport.Target{network, transport.DefaultHost, &port2}
	noParams := core.NewParams()
	callId1 := core.CallId("call-1")
	callId2 := core.CallId("call-2")
	msg1 := core.NewRequest(
		core.INVITE,
		&core.SipUri{
			User:      core.String{"bob"},
			Host:      "far-far-away.com",
			UriParams: noParams,
			Headers:   noParams,
		},
		"SIP/2.0",
		[]core.Header{
			&core.FromHeader{
				DisplayName: core.String{"bob"},
				Address: &core.SipUri{
					User:      core.String{"bob"},
					Host:      "far-far-away.com",
					UriParams: noParams,
					Headers:   noParams,
				},
				Params: noParams,
			},
			&core.ToHeader{
				DisplayName: core.String{"alice"},
				Address: &core.SipUri{
					User:      core.String{"alice"},
					Host:      "wonderland.com",
					UriParams: noParams,
					Headers:   noParams,
				},
				Params: core.NewParams().Add("tag", core.String{"msg-1"}),
			},
			&callId1,
		},
		"Hello world!",
	)
	msg2 := core.NewRequest(
		core.BYE,
		&core.SipUri{
			User:      core.String{"bob"},
			Host:      "far-far-away.com",
			UriParams: noParams,
			Headers:   noParams,
		},
		"SIP/2.0",
		[]core.Header{
			&core.FromHeader{
				DisplayName: core.String{"bob"},
				Address: &core.SipUri{
					User:      core.String{"bob"},
					Host:      "far-far-away.com",
					UriParams: noParams,
					Headers:   noParams,
				},
				Params: noParams,
			},
			&core.ToHeader{
				DisplayName: core.String{"alice"},
				Address: &core.SipUri{
					User:      core.String{"alice"},
					Host:      "wonderland.com",
					UriParams: noParams,
					Headers:   noParams,
				},
				Params: core.NewParams().Add("tag", core.String{"msg-2"}),
			},
			&callId2,
		},
		"Bye!",
	)
	bullshit := "This is bullshit!\r\n"

	BeforeEach(func() {
		output = make(chan *transport.IncomingMessage)
		errs = make(chan error)
		cancel = make(chan struct{})
		protocol = transport.NewUdpProtocol(output, errs, cancel)
	})
	AfterEach(func() {
		close(cancel)
		<-protocol.Done()
		close(output)
		close(errs)
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

	createClient := func(addr string) net.Conn {
		client, err := net.Dial(strings.ToLower(network), addr)
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())
		return client
	}
	readMsg := func(expected core.Message) {
		select {
		case incomingMsg := <-output:
			Expect(incomingMsg).ToNot(BeNil())
			Expect(incomingMsg.Msg).ToNot(BeNil())
			Expect(incomingMsg.Msg.String()).To(Equal(expected.String()))
			//Expect(incomingMsg.LAddr.String()).To(Equal(conn.LocalAddr()))
			//Expect(incomingMsg.RAddr.String()).To(Equal(conn.RemoteAddr()))
		case <-time.After(100 * time.Millisecond):
			Fail("timed out")
		}
	}
	readErr := func(expected string) {
		select {
		case err := <-errs:
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(expected))
		case <-time.After(100 * time.Millisecond):
			Fail("timed out")
		}
	}

	PContext(fmt.Sprintf("listening 2 target: %s, %s", localTarget1, localTarget2), func() {
		var client1, client2, client3 net.Conn
		BeforeEach(func() {
			Expect(protocol.Listen(localTarget1)).To(Succeed())
			Expect(protocol.Listen(localTarget2)).To(Succeed())
		})

		It("should pipe incoming messages and errors", func() {
			wg := new(sync.WaitGroup)
			wg.Add(2)
			go func() {
				defer wg.Done()

				time.Sleep(10 * time.Millisecond)

				client1 = createClient(localTarget1.Addr())
				defer client1.Close()
				Expect(client1.Write([]byte(msg1.String()))).To(Equal(len(msg1.String())))
			}()
			go func() {
				defer wg.Done()

				time.Sleep(20 * time.Millisecond)

				client2 = createClient(localTarget2.Addr())
				defer client2.Close()
				Expect(client2.Write([]byte(bullshit))).To(Equal(len(bullshit)))

				time.Sleep(10 * time.Millisecond)

				client3 = createClient(localTarget2.Addr())
				defer client3.Close()
				Expect(client3.Write([]byte(msg2.String()))).To(Equal(len(msg2.String())))
			}()

			By(fmt.Sprintf("message %s arrives on %s", msg1.Short(), localTarget1.Addr()))
			readMsg(msg1)
			By(fmt.Sprintf("bullshit '%s' arrives on %s", bullshit, localTarget2.Addr()))
			readErr("failed to parse")
			By(fmt.Sprintf("message %s arrives on %s", msg2.Short(), localTarget2.Addr()))
			readMsg(msg2)

			wg.Wait()
		})
	})
})
