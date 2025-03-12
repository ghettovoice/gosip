package transport_test

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip"
)

func specRelSendReq(tpPtr *sip.Transport, rmtPort uint16, listen func(context.Context, string) (net.Listener, error)) {
	Describe("sending requests", func() {
		var (
			rmtLs      net.Listener
			rmtConnVal atomic.Value // net.Conn
			rmtConnCh  chan struct{}
		)

		BeforeEach(OncePerOrdered, func(ctx SpecContext) {
			rmtPort += uint16(GinkgoParallelProcess())

			var err error
			rmtLs, err = listen(ctx, fmt.Sprintf("0.0.0.0:%d", rmtPort))
			Expect(err).ToNot(HaveOccurred(), "remote listener initialized")

			rmtConnCh = make(chan struct{})
			go func() {
				defer GinkgoRecover()
				defer close(rmtConnCh)

				c, err := rmtLs.Accept()
				if err != nil {
					return
				}
				rmtConnVal.Store(c)
			}()

			time.Sleep(100 * time.Millisecond)
		})

		AfterEach(OncePerOrdered, func(ctx SpecContext) {
			rmtLs.Close()
			Eventually(ctx, rmtConnCh).WithTimeout(time.Second).Should(BeClosed(), "remote listener closed")
			//nolint:forcetypeassert
			if rmtConn := rmtConnVal.Load().(net.Conn); rmtConn != nil {
				rmtConn.Close()
			}
			time.Sleep(100 * time.Millisecond)
		})

		specBaseSendReq(tpPtr, &rmtPort,
			func() ([]byte, error) {
				<-rmtConnCh
				rmtConn := rmtConnVal.Load().(net.Conn) //nolint:forcetypeassert
				buf := make([]byte, sip.MaxMsgSize)
				n, err := rmtConn.Read(buf)
				return buf[:n], err
			},
		)
	})
}

func specRelSendRes(tpPtr *sip.Transport, locPort uint16, dial func(context.Context, string) (net.Conn, error)) {
	Describe("sending responses", func() {
		var (
			lsCtx    context.Context
			cncLsCtx context.CancelFunc
			lsDone   chan struct{}

			rmtConn net.Conn
			rmtPort uint16

			reqs chan *sip.Request
			req  *sip.Request
		)

		BeforeEach(OncePerOrdered, func(ctx SpecContext) {
			tp := *tpPtr
			locPort += uint16(GinkgoParallelProcess())

			// setup our listener
			lsCtx, cncLsCtx = context.WithCancel(context.Background()) //nolint:fatcontext
			lsDone = make(chan struct{})
			go func() {
				defer GinkgoRecover()
				defer close(lsDone)

				Expect(tp.ListenAndServe(lsCtx, netip.AddrPortFrom(netip.IPv4Unspecified(), locPort))).
					To(MatchError(context.Canceled))
			}()
			Eventually(ctx, func(g Gomega) {
				g.Expect(tp.Stats().Listeners).To(BeEquivalentTo(1))
			}).Within(time.Second).Should(Succeed(), "listener started")

			time.Sleep(10 * time.Millisecond)

			// setup connection on remote side
			var err error
			rmtConn, err = dial(ctx, fmt.Sprintf("127.0.0.1:%d", locPort))
			Expect(err).NotTo(HaveOccurred(), "remote connection established")
			_, port, err := net.SplitHostPort(rmtConn.LocalAddr().String())
			Expect(err).ToNot(HaveOccurred())
			iport, err := strconv.ParseUint(port, 10, 16)
			Expect(err).ToNot(HaveOccurred())
			rmtPort = uint16(iport)

			time.Sleep(10 * time.Millisecond)

			// remote sends the request
			reqs = make(chan *sip.Request, 1)
			tp.OnInboundRequest(sip.RequestHandlerFunc(func(ctx context.Context, req *sip.Request) error {
				reqs <- req
				return nil
			}))
			_, err = rmtConn.Write([]byte(fmt.Sprintf(
				"INVITE sip:alice@127.0.0.1:%d SIP/2.0\r\n"+
					"Via: SIP/2.0/%s example.com:%d;branch=%s.qwerty;rport\r\n"+
					"From: \"Bob\" <sip:bob@example.com>;tag=abc\r\n"+
					"To: \"Alice\" <sip:alice@localhost>\r\n"+
					"Call-ID: 123-abc-xyz@example.com\r\n"+
					"CSeq: 1 INVITE\r\n"+
					"Max-Forwards: 70\r\n"+
					"Contact: <sip:bob@127.0.0.1:%[3]d>\r\n"+
					"Timestamp: %.3[5]f\r\n"+
					"Content-Length: 5\r\n"+
					"\r\n"+
					"hello world!",
				locPort,
				tp.Proto(),
				rmtPort,
				sip.MagicCookie,
				float64(time.Now().UTC().UnixNano())/float64(time.Second),
			)))
			Expect(err).ToNot(HaveOccurred(), "remote sent the request")

			// we receive the request
			Eventually(ctx, reqs).Within(time.Second).Should(Receive(&req), "message received")
		})

		AfterEach(OncePerOrdered, func(ctx SpecContext) {
			rmtConn.Close()
			cncLsCtx()
			Eventually(ctx, lsDone).Within(time.Second).Should(BeClosed(), "listener stopped")
			time.Sleep(100 * time.Millisecond)
		})

		specBaseSendRes(tpPtr, &req,
			func() ([]byte, error) {
				buf := make([]byte, sip.MaxMsgSize)
				n, err := rmtConn.Read(buf)
				return buf[:n], err
			},
		)
	})
}

func specRelRecvReq(tpPtr *sip.Transport, locPort uint16, dial func(context.Context, string) (net.Conn, error)) {
	Describe("receiving requests", func() {
		var (
			rmtConn net.Conn
			rmtPort uint16
		)

		BeforeEach(OncePerOrdered, func(ctx SpecContext) {
			tp := *tpPtr
			locPort += uint16(GinkgoParallelProcess())

			// setup our listener
			go func() {
				defer GinkgoRecover()
				// this goroutine should be closed by tp.Shutdown in caller's topmost AfterEach
				Expect(tp.ListenAndServe(context.Background(), netip.AddrPortFrom(netip.IPv4Unspecified(), locPort))).
					To(MatchError(sip.ErrTransportClosed))
			}()
			Eventually(ctx, func(g Gomega) {
				g.Expect(tp.Stats().Listeners).To(BeEquivalentTo(1))
			}).Within(time.Second).Should(Succeed(), "listener started")

			time.Sleep(10 * time.Millisecond)

			// setup connection on remote side
			var err error
			rmtConn, err = dial(ctx, fmt.Sprintf("127.0.0.1:%d", locPort))
			Expect(err).NotTo(HaveOccurred())
			_, port, err := net.SplitHostPort(rmtConn.LocalAddr().String())
			Expect(err).ToNot(HaveOccurred())
			iport, err := strconv.ParseUint(port, 10, 16)
			Expect(err).ToNot(HaveOccurred())
			rmtPort = uint16(iport)

			time.Sleep(100 * time.Millisecond)
		})

		AfterEach(OncePerOrdered, func(ctx SpecContext) {
			rmtConn.Close()
			time.Sleep(100 * time.Millisecond)
		})

		specBaseRecvReq(tpPtr, &locPort, &rmtPort,
			func(buf []byte) error {
				_, err := rmtConn.Write(buf)
				return err
			},
			func() ([]byte, error) {
				buf := make([]byte, sip.MaxMsgSize)
				n, err := rmtConn.Read(buf)
				return buf[:n], err
			},
		)
	})
}

func specRelRecvRes(tpPtr *sip.Transport, locPort uint16, dial func(context.Context, string) (net.Conn, error)) {
	Describe("receiving responses", func() {
		var (
			lsCtx    context.Context
			cncLsCtx context.CancelFunc
			lsDone   chan struct{}

			rmtConn net.Conn
		)

		BeforeEach(OncePerOrdered, func(ctx SpecContext) {
			tp := *tpPtr
			locPort += uint16(GinkgoParallelProcess())

			// setup our listener
			lsCtx, cncLsCtx = context.WithCancel(context.Background()) //nolint:fatcontext
			lsDone = make(chan struct{})
			go func() {
				defer GinkgoRecover()

				Expect(tp.ListenAndServe(lsCtx, netip.AddrPortFrom(netip.IPv4Unspecified(), locPort))).
					To(MatchError(context.Canceled))

				close(lsDone)
			}()
			Eventually(ctx, func(g Gomega) {
				g.Expect(tp.Stats().Listeners).To(BeEquivalentTo(1))
			}).Within(time.Second).Should(Succeed(), "local listener started")

			time.Sleep(10 * time.Millisecond)

			// setup connection on remote side
			var err error
			rmtConn, err = dial(ctx, fmt.Sprintf("127.0.0.1:%d", locPort))
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(10 * time.Millisecond)
		})

		AfterEach(OncePerOrdered, func(ctx SpecContext) {
			rmtConn.Close()
			cncLsCtx()
			Eventually(ctx, lsDone).Should(BeClosed(), "local listener stopped")
			time.Sleep(100 * time.Millisecond)
		})

		specBaseRecvRes(tpPtr, &locPort,
			func(b []byte) error {
				_, err := rmtConn.Write(b)
				return err
			},
		)
	})
}
