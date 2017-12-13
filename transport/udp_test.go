package transport_test

import (
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

	port := transport.DefaultUdpPort
	localTarget := &transport.Target{"UDP", transport.DefaultHost, &port}

	BeforeEach(func() {
		output = make(chan *transport.IncomingMessage)
		errs = make(chan error)
		cancel = make(chan struct{})
		protocol = transport.NewUdpProtocol(output, errs, cancel)
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

	Context("listening on "+localTarget.String(), func() {
		BeforeEach(func() {
			Expect(protocol.Listen(localTarget)).To(Succeed())
		})
		It("should receive message", func() {

		})
	})
})
