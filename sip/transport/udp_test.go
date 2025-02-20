package transport_test

import (
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
	. "github.com/onsi/gomega/gstruct"

	"github.com/ghettovoice/gosip/internal/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/transport"
)

var _ = Describe("Transport", Label("sip", "transport", "unreliable"), func() {
	Describe("UDP", func() {
		var (
			tp  *transport.UDP
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

			tp = transport.NewUDP(&transport.Options{
				Log:         logger,
				ConnIdleTTL: time.Second,
			})
			Expect(tp.Proto()).To(BeEquivalentTo("UDP"), "transport protocol is UDP")
			Expect(tp.Reliable()).To(BeFalse(), "transport is unreliable")
			Expect(tp.Streamed()).To(BeFalse(), "transport is not streamed")
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

		specUnrelConnMng(&itp, 20000, 20500)

		specUnrelSendReq(&itp, 20600)

		specUnrelSendRes(&itp, 20100)

		specUnrelRecvReq(&itp, 20200)

		specUnrelRecvRes(&itp, 20300)
	})
})
