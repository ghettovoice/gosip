package gosip_test

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/parser"
	"github.com/ghettovoice/gosip/testutils"
	"github.com/ghettovoice/gosip/transport"
)

var _ = Describe("GoSIP Server", func() {
	var (
		srv          gosip.Server
		client1      net.Conn
		inviteBranch string
		inviteReq    sip.Request
	)

	srvConf := gosip.ServerConfig{}
	clientAddr := "127.0.0.1:9001"
	localTarget := transport.NewTarget("127.0.0.1", 5060)
	wsLocalTarget := transport.NewTarget("127.0.0.1", 8080)
	logger := testutils.NewLogrusLogger()

	JustBeforeEach(func() {
		srv = gosip.NewServer(srvConf, nil, nil, logger)
		Expect(srv.Listen("udp", localTarget.Addr())).To(Succeed())
		Expect(srv.Listen("tcp", localTarget.Addr())).To(Succeed())
		Expect(srv.Listen("ws", wsLocalTarget.Addr())).To(Succeed())
	})

	AfterEach(func() {
		srv.Shutdown()
	}, 3)

	It("should call INVITE handler on incoming INVITE request via UDP transport", func(done Done) {
		client1 = testutils.CreateClient("udp", localTarget.Addr(), clientAddr)
		defer func() {
			Expect(client1.Close()).To(BeNil())
		}()
		inviteBranch = sip.GenerateBranch()
		inviteReq = testutils.Request([]string{
			"INVITE sip:bob@example.com SIP/2.0",
			"Via: SIP/2.0/UDP " + clientAddr + ";branch=" + inviteBranch,
			"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774",
			"To: \"Bob\" <sip:bob@far-far-away.com>",
			"CSeq: 1 INVITE",
			"Content-Length: 0",
			"",
			"",
		})

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
			testutils.WriteToConn(client1, []byte(inviteReq.String()))
		}()

		wg.Wait()
		close(done)
	}, 3)

	It("should call INVITE handler on incoming INVITE request via TCP transport", func(done Done) {
		defer close(done)

		client1 = testutils.CreateClient("tcp", localTarget.Addr(), clientAddr)
		defer func() {
			Expect(client1.Close()).To(BeNil())
		}()
		inviteReq = testutils.Request([]string{
			"INVITE sip:bob@example.com SIP/2.0",
			"Via: SIP/2.0/TCP " + clientAddr + ";branch=" + sip.GenerateBranch(),
			"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774",
			"To: \"Bob\" <sip:bob@far-far-away.com>",
			"CSeq: 1 INVITE",
			"Content-Length: 0",
			"",
			"",
		})

		wg := new(sync.WaitGroup)
		wg.Add(1)
		Expect(srv.OnRequest(sip.INVITE, func(req sip.Request, tx sip.ServerTransaction) {
			defer wg.Done()

			Expect(req.Method()).To(Equal(sip.INVITE))
			Expect(tx.Origin().Method()).To(Equal(sip.INVITE))
			_, err := srv.RespondOnRequest(req, 200, "OK", "", nil)
			Expect(err).ShouldNot(HaveOccurred())
		}))

		wg.Add(1)
		go func() {
			defer wg.Done()

			_, err := client1.Write([]byte(inviteReq.String()))
			Expect(err).ShouldNot(HaveOccurred())

			buf := make([]byte, 1000)
			n, err := client1.Read(buf)
			Expect(err).ShouldNot(HaveOccurred())
			msg, err := parser.ParseMessage(buf[:n], logger)
			Expect(err).ShouldNot(HaveOccurred())
			res, ok := msg.(sip.Response)
			Expect(ok).Should(BeTrue())
			Expect(int(res.StatusCode())).Should(Equal(200))
		}()

		wg.Wait()
	}, 3)

	It("should call INVITE handler on incoming INVITE request via WS transport", func(done Done) {
		defer close(done)

		var err error
		client1, _, _, err = ws.Dial(context.Background(), "ws://"+wsLocalTarget.Addr())
		Expect(err).ShouldNot(HaveOccurred())
		defer func() {
			Expect(client1.Close()).To(BeNil())
		}()
		inviteReq = testutils.Request([]string{
			"INVITE sip:bob@example.com SIP/2.0",
			"Via: SIP/2.0/WS " + clientAddr + ";branch=" + sip.GenerateBranch(),
			"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774",
			"To: \"Bob\" <sip:bob@far-far-away.com>",
			"CSeq: 1 INVITE",
			"Content-Length: 0",
			"",
			"",
		})

		wg := new(sync.WaitGroup)
		wg.Add(1)
		Expect(srv.OnRequest(sip.INVITE, func(req sip.Request, tx sip.ServerTransaction) {
			defer wg.Done()

			Expect(req.Method()).To(Equal(sip.INVITE))
			Expect(tx.Origin().Method()).To(Equal(sip.INVITE))
			_, err := srv.RespondOnRequest(req, 200, "OK", "", nil)
			Expect(err).ShouldNot(HaveOccurred())
		}))

		wg.Add(1)
		go func() {
			defer wg.Done()

			Expect(wsutil.WriteClientText(client1, []byte(inviteReq.String()))).Should(Succeed())
			buf, err := wsutil.ReadServerText(client1)
			Expect(err).ShouldNot(HaveOccurred())
			msg, err := parser.ParseMessage(buf, logger)
			Expect(err).ShouldNot(HaveOccurred())
			res, ok := msg.(sip.Response)
			Expect(ok).Should(BeTrue())
			Expect(int(res.StatusCode())).Should(Equal(200))
		}()

		wg.Wait()
	}, 3)

	It("should send INVITE request through TX layer with UDP transport", func(done Done) {
		defer close(done)

		inviteReq = testutils.Request([]string{
			"INVITE sip:bob@example.com SIP/2.0",
			"Route: <sip:" + clientAddr + ";lr>",
			"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774",
			"To: \"Bob\" <sip:bob@far-far-away.com>",
			"CSeq: 1 INVITE",
			"",
			"Hello world!",
		})

		wg := new(sync.WaitGroup)
		wg.Add(1)
		go func() {
			defer wg.Done()

			conn, err := net.ListenPacket("udp", clientAddr)
			Expect(err).ShouldNot(HaveOccurred())
			defer conn.Close()

			buf := make([]byte, transport.MTU)
			i := 0
			for {
				num, raddr, err := conn.ReadFrom(buf)
				if err != nil {
					return
				}

				msg, err := parser.ParseMessage(buf[:num], logger)
				Expect(err).ShouldNot(HaveOccurred())
				viaHop, ok := msg.ViaHop()
				Expect(ok).Should(BeTrue())
				if viaHop.Params == nil {
					viaHop.Params = sip.NewParams()
				}
				viaHop.Params.Add("received", sip.String{Str: raddr.(*net.UDPAddr).IP.String()})
				req, ok := msg.(sip.Request)
				Expect(ok).Should(BeTrue())
				Expect(req.Method()).Should(Equal(sip.INVITE))
				Expect(req.Recipient().String()).Should(Equal("sip:bob@example.com"))

				// sleep and wait for retransmission
				if i == 0 {
					time.Sleep(300 * time.Millisecond)
					i++
					continue
				}

				res := sip.NewResponseFromRequest("", req, 100, "Trying", "")
				raddr, err = net.ResolveUDPAddr("udp", res.Destination())
				Expect(err).ShouldNot(HaveOccurred())
				num, err = conn.WriteTo([]byte(res.String()), raddr)
				Expect(err).ShouldNot(HaveOccurred())

				time.Sleep(100 * time.Millisecond)

				res = sip.NewResponseFromRequest("", req, 200, "Ok", "")
				num, err = conn.WriteTo([]byte(res.String()), raddr)
				Expect(err).ShouldNot(HaveOccurred())

				return
			}
		}()

		i := int32(0)
		res, err := srv.RequestWithContext(context.Background(), inviteReq,
			gosip.WithResponseHandler(func(res sip.Response, request sip.Request) {
				switch atomic.LoadInt32(&i) {
				case 0:
					Expect(int(res.StatusCode())).Should(Equal(100))
				case 1:
					Expect(int(res.StatusCode())).Should(Equal(200))
					ack := sip.NewAckRequest("", request, res, "", nil)
					Expect(ack).ShouldNot(BeNil())
				}
				atomic.AddInt32(&i, 1)
			}),
		)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(int(res.StatusCode())).Should(Equal(200))

		wg.Wait()
	}, 3)

	It("should send INVITE request through TX layer with TCP transport", func(done Done) {
		defer close(done)

		inviteReq = testutils.Request([]string{
			"INVITE sip:bob@example.com SIP/2.0",
			"Route: <sip:" + clientAddr + ";transport=tcp;lr>",
			"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774",
			"To: \"Bob\" <sip:bob@far-far-away.com>",
			"CSeq: 1 INVITE",
			"",
			"Hello world!",
		})

		wg := new(sync.WaitGroup)
		wg.Add(1)
		go func() {
			defer wg.Done()

			ls, err := net.Listen("tcp", clientAddr)
			Expect(err).ShouldNot(HaveOccurred())
			defer ls.Close()

			conn, err := ls.Accept()
			Expect(err).ShouldNot(HaveOccurred())
			defer conn.Close()

			buf := make([]byte, transport.MTU)
			for {
				num, err := conn.Read(buf)
				if err != nil {
					return
				}

				msg, err := parser.ParseMessage(buf[:num], logger)
				Expect(err).ShouldNot(HaveOccurred())
				viaHop, ok := msg.ViaHop()
				Expect(ok).Should(BeTrue())
				if viaHop.Params == nil {
					viaHop.Params = sip.NewParams()
				}
				viaHop.Params.Add("received", sip.String{Str: conn.RemoteAddr().(*net.TCPAddr).IP.String()})
				req, ok := msg.(sip.Request)
				Expect(ok).Should(BeTrue())
				Expect(req.Method()).Should(Equal(sip.INVITE))
				Expect(req.Recipient().String()).Should(Equal("sip:bob@example.com"))

				time.Sleep(100 * time.Millisecond)

				res := sip.NewResponseFromRequest("", req, 100, "Trying", "")
				res.SetBody("", true)
				num, err = conn.Write([]byte(res.String()))
				Expect(err).ShouldNot(HaveOccurred())

				time.Sleep(100 * time.Millisecond)

				res = sip.NewResponseFromRequest("", req, 200, "Ok", "")
				res.SetBody("", true)
				num, err = conn.Write([]byte(res.String()))
				Expect(err).ShouldNot(HaveOccurred())

				return
			}
		}()

		i := int32(0)
		res, err := srv.RequestWithContext(context.Background(), inviteReq,
			gosip.WithResponseHandler(func(res sip.Response, request sip.Request) {
				switch atomic.LoadInt32(&i) {
				case 0:
					Expect(int(res.StatusCode())).Should(Equal(100))
				case 1:
					Expect(int(res.StatusCode())).Should(Equal(200))
					ack := sip.NewAckRequest("", request, res, "", nil)
					Expect(ack).ShouldNot(BeNil())
				}
				atomic.AddInt32(&i, 1)
			}),
		)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(int(res.StatusCode())).Should(Equal(200))

		wg.Wait()
	}, 3)
})
