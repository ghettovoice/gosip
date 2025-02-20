package transport_test

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
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
)

var _ = Describe("Transport", Label("sip", "transport"), func() {
	Describe("TLS", func() {
		var (
			tp  *transport.TLS
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

			tp = transport.NewTLS(&transport.Options{
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
			})
			Expect(tp.Proto()).To(BeEquivalentTo("TLS"), "transport protocol is TLS")
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
			//nolint:gosec
			return tls.NewListener(ls, &tls.Config{
				Certificates: []tls.Certificate{loadCert()},
			}), nil
		}
		dial := func(ctx context.Context, addr string) (net.Conn, error) {
			var dc net.Dialer
			c, err := dc.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			//nolint:gosec
			return tls.Client(c, &tls.Config{
				InsecureSkipVerify: true,
			}), nil
		}

		specRelConnMng(&itp, 22000, 22500, listen, dial)

		specRelSendReq(&itp, 22600, listen)

		specRelSendRes(&itp, 22100, dial)

		specRelRecvReq(&itp, 22200, dial)

		specRelRecvRes(&itp, 22300, dial)
	})
})
