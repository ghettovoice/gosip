package transport_test

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
	. "github.com/onsi/gomega/gstruct"

	"github.com/ghettovoice/gosip/internal/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/transport"
	"github.com/ghettovoice/gosip/sip/transport/internal/ws"
)

var _ = Describe("Transport", Label("sip", "transport"), func() {
	Describe("WS", func() {
		var (
			tp  *transport.WS
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

			tp = transport.NewWS(&transport.Options{
				Log:         logger,
				ConnIdleTTL: time.Second,
				WSConfig: &transport.WSConfig{
					UpgradeTimeout: time.Second,
				},
			})
			Expect(tp.Proto()).To(BeEquivalentTo("WS"), "transport protocol is WS")
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
			ls, err := lc.Listen(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			return ws.NewListener(ls, &ws.Config{UpgradeTimeout: time.Second}), nil
		}
		dial := func(ctx context.Context, addr string) (c net.Conn, err error) {
			var dlr net.Dialer
			c, err = dlr.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			defer func() {
				if err != nil {
					c.Close()
				}
			}()
			return ws.NewDialer(&ws.Config{UpgradeTimeout: time.Second}).
				Upgrade(c, &url.URL{Scheme: "ws", Host: addr})
		}

		specRelConnMng(&itp, 23000, 23500, listen, dial)

		specRelSendReq(&itp, 23600, listen)

		specRelSendRes(&itp, 23100, dial)

		specRelRecvReq(&itp, 23200, dial)

		specRelRecvRes(&itp, 23300, dial)
	})
})
