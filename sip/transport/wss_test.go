package transport_test

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/url"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
	. "github.com/onsi/gomega/gstruct"

	"github.com/ghettovoice/gosip/internal/log"
	"github.com/ghettovoice/gosip/internal/testutils"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/transport"
	"github.com/ghettovoice/gosip/sip/transport/internal/ws"
)

var _ = Describe("Transport", Label("sip", "transport"), func() {
	Describe("WSS", func() {
		var (
			tp  *transport.WSS
			itp sip.Transport
		)

		loadCert := func() tls.Certificate {
			cert, err := tls.LoadX509KeyPair(
				filepath.Join(testutils.ProjectRoot, "/examples/certs/server.pem"),
				filepath.Join(testutils.ProjectRoot, "/examples/certs/server.key"),
			)
			Expect(err).ToNot(HaveOccurred())
			return cert
		}

		BeforeEach(OncePerOrdered, func() {
			var logger *slog.Logger
			_, rptCfg := GinkgoConfiguration()
			if rptCfg.Verbose {
				logger = log.Def
			} else if rptCfg.VeryVerbose {
				logger = log.Dev
			}

			tp = transport.NewWSS(&transport.Options{
				Log:         logger,
				ConnIdleTTL: time.Second,
				//nolint:gosec
				TLSConfigSrv: &tls.Config{
					Certificates: []tls.Certificate{loadCert()},
				},
				//nolint:gosec
				TLSConfigCln: &tls.Config{
					InsecureSkipVerify: true,
				},
				WSConfig: &transport.WSConfig{
					UpgradeTimeout: time.Second,
				},
			})
			Expect(tp.Proto()).To(BeEquivalentTo("WSS"), "transport protocol is WSS")
			Expect(tp.Reliable()).To(BeTrue(), "transport is reliable")
			Expect(tp.Streamed()).To(BeTrue(), "transport is streamed")
			Expect(tp.Secured()).To(BeTrue(), "transport is secured")

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
			return ws.NewListener(
				//nolint:gosec
				tls.NewListener(ls, &tls.Config{
					Certificates: []tls.Certificate{loadCert()},
				}),
				&ws.Config{UpgradeTimeout: time.Second},
			), nil
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
				Upgrade(
					//nolint:gosec
					tls.Client(c, &tls.Config{
						InsecureSkipVerify: true,
					}),
					&url.URL{Scheme: "wss", Host: addr},
				)
		}

		specRelConnMng(&itp, 24000, 24500, listen, dial)

		specRelSendReq(&itp, 24600, listen)

		specRelSendRes(&itp, 24100, dial)

		specRelRecvReq(&itp, 24200, dial)

		specRelRecvRes(&itp, 24300, dial)
	})
})
