package transport_test

import (
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/testutils"
	"github.com/ghettovoice/gosip/transport"
)

var _ = Describe("Connection", func() {
	logger := testutils.NewLogrusLogger()

	Describe("construct", func() {
		Context("from net.UDPConn", func() {
			It("should set connection params", func() {
				cUdpConn, sUdpConn := testutils.CreatePacketClientServer("udp", localAddr1)
				defer func() {
					cUdpConn.Close()
					sUdpConn.Close()
				}()
				conn := transport.NewConnection(sUdpConn, "dummy", "udp", logger)

				Expect(conn.Network()).To(Equal("UDP"))
				Expect(conn.Streamed()).To(BeFalse(), "UDP should be non-streamed")
				Expect(conn.LocalAddr().String()).To(Equal(sUdpConn.LocalAddr().String()))

				if err := conn.Close(); err != nil {
					Fail(err.Error())
				}
			})
		})

		Context("from net.TCPConn", func() {
			It("should set connection params", func() {
				cTcpConn, sTcpConn := testutils.CreateStreamClientServer("tcp", localAddr1)
				defer func() {
					cTcpConn.Close()
					sTcpConn.Close()
				}()
				conn := transport.NewConnection(sTcpConn, "dummy", "tcp", logger)

				Expect(conn.Network()).To(Equal("TCP"))
				Expect(conn.Streamed()).To(BeTrue())
				Expect(conn.LocalAddr().String()).To(Equal(sTcpConn.LocalAddr().String()))
				Expect(conn.RemoteAddr().String()).To(Equal(sTcpConn.RemoteAddr().String()))

				if err := conn.Close(); err != nil {
					Fail(err.Error())
				}
			})
		})
	})

	Describe("read and write", func() {
		data := "Hello world!"

		Context("with net.UDPConn", func() {
			It("should read and write data", func() {
				cUdpConn, sUdpConn := testutils.CreatePacketClientServer("udp", localAddr1)
				defer func() {
					cUdpConn.Close()
					sUdpConn.Close()
				}()

				sConn := transport.NewConnection(sUdpConn, "dummy", "udp", logger)
				cConn := transport.NewConnection(cUdpConn, "dummy", "udp", logger)

				wg := new(sync.WaitGroup)
				wg.Add(1)
				go func() {
					defer wg.Done()

					buf := make([]byte, 65535)
					num, raddr, err := sConn.ReadFrom(buf)
					Expect(err).ToNot(HaveOccurred())
					logger.Debugf("%s <- %s: read %d bytes", sConn.LocalAddr(), raddr, num)

					Expect(fmt.Sprintf("%v", raddr)).To(Equal(fmt.Sprintf("%v", cConn.LocalAddr())))
					Expect(string(buf[:num])).To(Equal(data))
				}()

				num, err := cConn.Write([]byte(data))
				Expect(err).ToNot(HaveOccurred())
				Expect(num).To(Equal(len(data)))
				logger.Debugf("%s -> %s: written %d bytes", cConn.LocalAddr(), sConn.LocalAddr(), num)

				wg.Wait()
			})
		})
		// TODO: add TCP test
	})
})
