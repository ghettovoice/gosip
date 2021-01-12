package gosip_test

import (
	"net"
	"sync"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/testutils"
	"github.com/ghettovoice/gosip/transport"
)

var _ = Describe("GoSIP Server", func() {
	var (
		srv          gosip.Server
		client1      net.Conn
		inviteBranch string
		invite       sip.Message
	)

	srvConf := gosip.ServerConfig{}
	clientAddr := "127.0.0.1:9001"
	localTarget := transport.NewTarget("127.0.0.1", 5060)
	logger := testutils.NewLogrusLogger()

	BeforeEach(func() {
		client1 = testutils.CreateClient("udp", localTarget.Addr(), clientAddr)

		inviteBranch = sip.GenerateBranch()
		invite = testutils.Request([]string{
			"INVITE sip:bob@example.com SIP/2.0",
			"Via: SIP/2.0/UDP " + clientAddr + ";branch=" + inviteBranch,
			"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774",
			"To: \"Bob\" <sip:bob@far-far-away.com>",
			"CSeq: 1 INVITE",
			"Content-Length: 0",
			"",
			"",
		})
	}, 3)

	JustBeforeEach(func() {
		srv = gosip.NewServer(srvConf, nil, nil, logger)
		Expect(srv.Listen("udp", "0.0.0.0:5060", nil)).To(Succeed())
		//Expect(srv.Listen("tls", "0.0.0.0:5061", &transport.TLSConfig{Cert: "certs/cert.pem", Key: "certs/key.pem"})).To(Succeed())
	})

	AfterEach(func() {
		if client1 != nil {
			Expect(client1.Close()).To(BeNil())
		}
		srv.Shutdown()
	}, 3)

	It("should call INVITE handler on incoming INVITE request", func(done Done) {
		wg := new(sync.WaitGroup)

		wg.Add(1)
		Expect(srv.OnRequest(sip.INVITE, func(req sip.Request, tx sip.ServerTransaction) {
			defer wg.Done()
			Expect(req.Method()).To(Equal(sip.INVITE))
			Expect(tx.Origin().Method()).To(Equal(sip.INVITE))
		})).To(BeNil())

		wg.Add(1)
		go func() {
			defer wg.Done()
			testutils.WriteToConn(client1, []byte(invite.String()))
		}()

		wg.Wait()
		close(done)
	}, 3)
})
