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

func specUnrelConnMng(tpPtr *sip.Transport, locPort, rmtPort uint16) {
	Describe("connections management", func() {
		When("no listeners is running", func() {
			specBaseConnMngNoLs(tpPtr, &rmtPort)

			It("should dial a new connection to default port if no port provided", func(ctx SpecContext) {
				tp := *tpPtr

				w, err := tp.GetOrDial(ctx, netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), 0))
				Expect(err).ToNot(HaveOccurred())
				Expect(w).ToNot(BeNil())
				Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
					"Listeners":   BeEquivalentTo(0),
					"Connections": BeEquivalentTo(1),
				}), "connection added")
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

			It("should re-use listener connection", func(ctx SpecContext) {
				tp := *tpPtr

				w, err := tp.GetOrDial(ctx, netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), rmtPort))
				Expect(err).ToNot(HaveOccurred())
				Expect(w).ToNot(BeNil())
				Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
					"Listeners":   BeEquivalentTo(1),
					"Connections": BeEquivalentTo(0),
				}), "listener connection re-used")
			})

			It("should ignore idle TTL for listen connections", func() {
				tp := *tpPtr

				time.Sleep(1100 * time.Millisecond)

				Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
					"Listeners":   BeEquivalentTo(1),
					"Connections": BeEquivalentTo(0),
				}), "idle listen connection not closed")
			})
		})
	})
}

func specUnrelSendReq(tpPtr *sip.Transport, rmtPort uint16) {
	Describe("sending requests", func() {
		var (
			wrt        sip.RequestWriter
			rmtPktConn net.PacketConn
		)

		BeforeEach(OncePerOrdered, func(ctx SpecContext) {
			tp := *tpPtr
			rmtPort += uint16(GinkgoParallelProcess())

			var (
				lsCfg net.ListenConfig
				err   error
			)
			rmtPktConn, err = lsCfg.ListenPacket(ctx, transport.Network(tp.Proto()), fmt.Sprintf("0.0.0.0:%d", rmtPort))
			Expect(err).ToNot(HaveOccurred(), "remote listener initialized")

			time.Sleep(10 * time.Millisecond)

			wrt, err = tp.GetOrDial(ctx, netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), rmtPort))
			Expect(err).ToNot(HaveOccurred(), "connection established")
			Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"Listeners":   BeEquivalentTo(0),
				"Connections": BeEquivalentTo(1),
			}), "connection established")
		})

		AfterEach(OncePerOrdered, func() {
			rmtPktConn.Close()
			time.Sleep(100 * time.Millisecond)
		})

		specBaseSendReq(tpPtr, &wrt, &rmtPort, func() ([]byte, error) {
			buf := make([]byte, sip.MaxMsgSize)
			n, _, err := rmtPktConn.ReadFrom(buf)
			return buf[:n], err
		})
	})
}

func specUnrelSendRes(tpPtr *sip.Transport, locPort uint16) {
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
			var (
				dlr net.Dialer
				err error
			)
			rmtConn, err = dlr.DialContext(ctx, transport.Network(tp.Proto()), fmt.Sprintf("127.0.0.1:%d", locPort))
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

func specUnrelRecvReq(tpPtr *sip.Transport, locPort uint16) {
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
			var (
				dlr net.Dialer
				err error
			)
			rmtConn, err = dlr.DialContext(ctx, transport.Network(tp.Proto()), fmt.Sprintf("127.0.0.1:%d", locPort))
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

func specUnrelRecvRes(tpPtr *sip.Transport, locPort uint16) {
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
			var (
				dlr net.Dialer
				err error
			)
			rmtConn, err = dlr.DialContext(ctx, transport.Network(tp.Proto()), fmt.Sprintf("127.0.0.1:%d", locPort))
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(10 * time.Millisecond)
		})

		AfterEach(OncePerOrdered, func(ctx SpecContext) {
			rmtConn.Close()
			cncLsCtx()
			Eventually(ctx, lsDone).Should(BeClosed(), "local listener stopped")
			time.Sleep(100 * time.Millisecond)
		})

		specBaseRecvRes(tpPtr, &locPort, func(b []byte) error {
			_, err := rmtConn.Write(b)
			return err
		})
	})
}
