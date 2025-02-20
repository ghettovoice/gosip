package transport_test

import (
	"context"
	"log/slog"
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
	. "github.com/onsi/gomega/gstruct"

	"github.com/ghettovoice/gosip/internal/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/transport"
)

var _ = Describe("Transport", Label("sip", "transport"), func() {
	Describe("TCP", func() {
		var (
			tp  *transport.TCP
			itp sip.Transport
		)

		BeforeEach(OncePerOrdered, func() {
			var logger *slog.Logger
			_, rptCfg := GinkgoConfiguration()
			if rptCfg.Verbose {
				logger = log.Def
			} else if rptCfg.VeryVerbose {
				logger = log.Dev
			}

			tp = transport.NewTCP(&transport.Options{
				Log:         logger,
				ConnIdleTTL: time.Second,
			})
			Expect(tp.Proto()).To(BeEquivalentTo("TCP"), "transport protocol is TCP")
			Expect(tp.Reliable()).To(BeTrue(), "transport is reliable")
			Expect(tp.Streamed()).To(BeTrue(), "transport is streamed")
			Expect(tp.Secured()).To(BeFalse(), "transport is not secured")

			itp = tp
		})

		AfterEach(OncePerOrdered, func(ctx SpecContext) {
			done := make(chan struct{})
			go func() {
				defer GinkgoRecover()

				Expect(tp.Shutdown()).To(Succeed())
				close(done)
			}()
			Eventually(ctx, done).Within(time.Second).Should(BeClosed(), "shutdown finished")
			Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"Listeners":   BeEquivalentTo(0),
				"Connections": BeEquivalentTo(0),
			}))

			time.Sleep(100 * time.Millisecond)

			Eventually(Goroutines).Within(time.Second).ShouldNot(HaveLeaked(), "no leaked goroutines")
		})

		listen := func(ctx context.Context, addr string) (net.Listener, error) {
			var lc net.ListenConfig
			return lc.Listen(ctx, "tcp", addr)
		}
		dial := func(ctx context.Context, addr string) (net.Conn, error) {
			var dc net.Dialer
			return dc.DialContext(ctx, "tcp", addr)
		}

		specRelConnMng(&itp, 21000, 21500, listen, dial)

		specRelSendReq(&itp, 21600, listen)

		specRelSendRes(&itp, 21100, dial)

		specRelRecvReq(&itp, 21200, dial)

		specRelRecvRes(&itp, 21300, dial)
	})
})
