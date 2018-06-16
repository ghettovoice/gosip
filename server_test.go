package gosip_test

import (
	"net"
	"sync"

	. "github.com/ghettovoice/gosip"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/testutils"
	"github.com/ghettovoice/gosip/transport"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Server", func() {
	var (
		srv     *Server
		client1 net.Conn
	)

	port1 := 9050
	clientAddr1 := "127.0.0.1:9001"
	localTarget1 := transport.NewTarget(transport.DefaultHost, port1)
	msg1 := "INVITE sip:bob@far-far-away.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP pc33.far-far-away.com:9001;branch=z9hG4bK776asdhds\r\n" +
		"To: \"Bob\" <sip:bob@far-far-away.com>\r\n" +
		"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774\r\n" +
		"Content-Length: 12\r\n" +
		"\r\n" +
		"Hello world!"

	BeforeEach(func() {
		srv = NewServer(ServerConfig{})
		client1 = testutils.CreateClient("udp", localTarget1.Addr(), clientAddr1)
	}, 3)

	AfterEach(func() {
		if client1 != nil {
			client1.Close()
		}
		srv.Shutdown()
	}, 3)

	It("should call INVITE handler on incoming INVITE request", func(done Done) {
		wg := new(sync.WaitGroup)
		called := false

		Expect(srv.Listen("127.0.0.1:5060")).To(Succeed())

		srv.OnRequest(sip.INVITE, func(req sip.Request) {
			Expect(req.Method).To(Equal(sip.INVITE))
			called = true
		})

		wg.Add(1)
		go func() {
			defer wg.Done()
			testutils.WriteToConn(client1, []byte(msg1))
		}()

		wg.Wait()
		close(done)
	}, 3)
})
