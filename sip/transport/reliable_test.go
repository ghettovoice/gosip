package transport_test

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/transport"
)

func specRelConnMng(
	tpPtr *sip.Transport,
	locPort, rmtPort uint16,
	listen func(context.Context, string) (net.Listener, error),
	dial func(context.Context, string) (net.Conn, error),
) {
	Describe("connections management", func() {
		When("no listeners are running", func() {
			var (
				rmtLs     net.Listener
				rmtConnCh chan net.Conn
			)

			BeforeEach(OncePerOrdered, func(ctx SpecContext) {
				rmtPort += uint16(GinkgoParallelProcess())

				var err error
				rmtLs, err = listen(ctx, fmt.Sprintf("0.0.0.0:%d", rmtPort))
				Expect(err).ToNot(HaveOccurred(), "remote listener initialized")

				started := make(chan struct{})
				rmtConnCh = make(chan net.Conn, 1)
				go func() {
					defer GinkgoRecover()

					close(started)

					c, err := rmtLs.Accept()
					if err != nil {
						return
					}
					rmtConnCh <- c
				}()
				Eventually(ctx, started).Within(time.Second).Should(BeClosed())
			})

			AfterEach(OncePerOrdered, func(ctx SpecContext) {
				select {
				case <-time.After(time.Second):
				case c := <-rmtConnCh:
					c.Close()
				}

				rmtLs.Close()
				time.Sleep(100 * time.Millisecond)
			})

			specBaseConnMngNoLs(tpPtr, &rmtPort)

			It("should dial a new connection to default port if no port provided", Serial, func(ctx SpecContext) {
				tp := *tpPtr

				ls, err := listen(ctx, fmt.Sprintf("0.0.0.0:%d", transport.DefaultPort(tp.Proto())))
				Expect(err).ToNot(HaveOccurred())
				Expect(ls).ToNot(BeNil())
				defer ls.Close()

				started := make(chan struct{})
				connCh := make(chan net.Conn, 1)
				go func() {
					defer GinkgoRecover()

					close(started)

					c, err := ls.Accept() //nolint:govet
					Expect(err).ToNot(HaveOccurred())
					connCh <- c
				}()
				Eventually(ctx, started).Within(time.Second).Should(BeClosed())

				w, err := tp.GetOrDial(ctx, netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), 0))
				Expect(err).ToNot(HaveOccurred())
				Expect(w).ToNot(BeNil())
				Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
					"Listeners":   BeEquivalentTo(0),
					"Connections": BeEquivalentTo(1),
				}), "connection added")

				var conn net.Conn
				Eventually(ctx, connCh).Within(time.Second).Should(Receive(&conn))
				conn.Close()
			})
		})

		When("a listener is running", func() {
			BeforeEach(OncePerOrdered, func(ctx SpecContext) {
				tp := *tpPtr
				locPort += uint16(GinkgoParallelProcess())

				go func() {
					defer GinkgoRecover()

					Expect(tp.ListenAndServe(context.Background(), netip.AddrPortFrom(netip.IPv4Unspecified(), locPort))).
						To(MatchError(transport.ErrTransportClosed))
				}()
				Eventually(ctx, func(g Gomega) {
					g.Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
						"Listeners":   BeEquivalentTo(1),
						"Connections": BeEquivalentTo(0),
					}))
				}).Within(time.Second).Should(Succeed(), "local listener started")
			})

			specBaseConnMngLs(tpPtr, &locPort)

			It("should accept a new connection", func(ctx SpecContext) {
				tp := *tpPtr

				c, err := dial(ctx, fmt.Sprintf("127.0.0.1:%d", locPort))
				Expect(err).ToNot(HaveOccurred(), "remote connection established")
				defer c.Close()

				Eventually(ctx, func(g Gomega) {
					g.Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
						"Listeners":   BeEquivalentTo(1),
						"Connections": BeEquivalentTo(1),
					}))
				}).Within(time.Second).Should(Succeed(), "inbound connection accepted")
			})

			It("should set idle TTL for the connection", func(ctx SpecContext) {
				tp := *tpPtr

				c, err := dial(ctx, fmt.Sprintf("127.0.0.1:%d", locPort))
				Expect(err).ToNot(HaveOccurred(), "remote connection established")
				defer c.Close()

				Eventually(ctx, func(g Gomega) {
					g.Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
						"Listeners":   BeEquivalentTo(1),
						"Connections": BeEquivalentTo(1),
					}))
				}).Within(time.Second).Should(Succeed(), "inbound connection accepted")

				time.Sleep(time.Second)

				Eventually(ctx, func(g Gomega) {
					g.Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
						"Listeners":   BeEquivalentTo(1),
						"Connections": BeEquivalentTo(0),
					}))
				}).Within(time.Second).Should(Succeed(), "idle connection closed")
			})
		})
	})
}

func specRelSendReq(tpPtr *sip.Transport, rmtPort uint16, listen func(context.Context, string) (net.Listener, error)) {
	Describe("sending requests", func() {
		var (
			wrt     sip.RequestWriter
			rmtLs   net.Listener
			rmtConn net.Conn
		)

		BeforeEach(OncePerOrdered, func(ctx SpecContext) {
			tp := *tpPtr
			rmtPort += uint16(GinkgoParallelProcess())

			var err error
			rmtLs, err = listen(ctx, fmt.Sprintf("0.0.0.0:%d", rmtPort))
			Expect(err).ToNot(HaveOccurred(), "remote listener initialized")

			rmtConnDone := make(chan struct{})
			go func() {
				defer GinkgoRecover()

				var err error //nolint:govet
				rmtConn, err = rmtLs.Accept()
				Expect(err).ToNot(HaveOccurred(), "remote connection accepted")
				Expect(rmtConn).ToNot(BeNil(), "remote connection accepted")

				close(rmtConnDone)
			}()

			time.Sleep(10 * time.Millisecond)

			wrt, err = tp.GetOrDial(ctx, netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), rmtPort))
			Expect(err).ToNot(HaveOccurred(), "connection established")
			Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"Listeners":   BeEquivalentTo(0),
				"Connections": BeEquivalentTo(1),
			}), "connection established")

			Eventually(rmtConnDone).Within(time.Second).Should(BeClosed(), "remote got connection")
		})

		AfterEach(OncePerOrdered, func() {
			rmtConn.Close()
			rmtLs.Close()
			time.Sleep(100 * time.Millisecond)
		})

		specBaseSendReq(tpPtr, &wrt, &rmtPort,
			func() ([]byte, error) {
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

			reqs chan struct {
				req  *sip.Request
				rspd sip.ResponseWriter
			}
			rspd sip.ResponseWriter
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
					To(MatchError(net.ErrClosed))
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
			reqs = make(chan struct {
				req  *sip.Request
				rspd sip.ResponseWriter
			}, 1)
			tp.OnInboundRequest(func(ctx context.Context, r *sip.Request, rd sip.ResponseWriter) error {
				reqs <- struct {
					req  *sip.Request
					rspd sip.ResponseWriter
				}{r, rd}
				return nil
			})
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
			var ir struct {
				req  *sip.Request
				rspd sip.ResponseWriter
			}
			Eventually(ctx, reqs).Within(time.Second).Should(Receive(&ir), "message received")
			rspd = ir.rspd
		})

		AfterEach(OncePerOrdered, func(ctx SpecContext) {
			rmtConn.Close()
			cncLsCtx()
			Eventually(ctx, lsDone).Within(time.Second).Should(BeClosed(), "listener stopped")
			time.Sleep(100 * time.Millisecond)
		})

		specBaseSendRes(tpPtr, &rspd,
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
			lsCtx    context.Context
			cncLsCtx context.CancelFunc
			lsDone   chan struct{}

			rmtConn net.Conn
			rmtPort uint16
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
					To(MatchError(net.ErrClosed))
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

			time.Sleep(10 * time.Millisecond)
		})

		AfterEach(OncePerOrdered, func(ctx SpecContext) {
			rmtConn.Close()
			cncLsCtx()
			Eventually(ctx, lsDone).Within(time.Second).Should(BeClosed(), "listener stopped")
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
					To(MatchError(net.ErrClosed))

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
