package testutils

import (
	"net"
	"os"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	prefixed "github.com/x-cray/logrus-prefixed-formatter"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
)

func CreateStreamClientServer(network string, addr string) (net.Conn, net.Conn) {
	network = strings.ToLower(network)
	ln, err := net.Listen(network, addr)
	if err != nil {
		Fail(err.Error())
	}

	ch := make(chan net.Conn)
	go func() {
		defer ln.Close()
		if server, err := ln.Accept(); err == nil {
			ch <- server
		} else {
			Fail(err.Error())
		}
	}()

	client, err := net.Dial(network, ln.Addr().String())
	if err != nil {
		Fail(err.Error())
	}

	return client, <-ch
}

func CreatePacketClientServer(network string, addr string) (net.Conn, net.Conn) {
	network = strings.ToLower(network)
	server, err := net.ListenPacket(network, addr)
	if err != nil {
		Fail(err.Error())
	}

	client, err := net.Dial(network, server.LocalAddr().String())
	if err != nil {
		Fail(err.Error())
	}

	return client, server.(net.Conn)
}

func CreateClient(network string, raddr string, laddr string) net.Conn {
	var la, ra net.Addr
	var err error
	network = strings.ToLower(network)

	switch network {
	case "udp":
		if laddr != "" {
			la, err = net.ResolveUDPAddr(network, laddr)
			Expect(err).ToNot(HaveOccurred())
		}
		ra, err = net.ResolveUDPAddr(network, raddr)
		Expect(err).ToNot(HaveOccurred())
	case "tcp":
		if laddr != "" {
			la, err = net.ResolveTCPAddr(network, laddr)
			Expect(err).ToNot(HaveOccurred())
		}
		ra, err = net.ResolveTCPAddr(network, raddr)
		Expect(err).ToNot(HaveOccurred())
	default:
		Fail("unsupported network " + network)
	}

	client, err := net.Dial(network, raddr)
	Expect(err).ToNot(HaveOccurred())
	Expect(client).ToNot(BeNil())

	return &MockConn{client, la, ra}
}

func WriteToConn(conn net.Conn, data []byte) {
	num, err := conn.Write(data)
	Expect(err).ToNot(HaveOccurred())
	Expect(num).To(Equal(len(data)))
}

func AssertMessageArrived(
	fromCh <-chan sip.Message,
	expectedMessage string,
	expectedSource string,
	expectedDest string,
) sip.Message {
	msg := <-fromCh
	Expect(msg).ToNot(BeNil())
	Expect(strings.Trim(msg.String(), " \r\n")).To(Equal(strings.Trim(expectedMessage, " \r\n")))
	Expect(msg.Source()).To(Equal(expectedSource))
	Expect(msg.Destination()).To(Equal(expectedDest))

	return msg
}

func AssertIncomingErrorArrived(
	fromCh <-chan error,
	expected string,
) {
	err := <-fromCh
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring(expected))
}

func NewLogrusLogger() *log.LogrusLogger {
	logger := logrus.New()
	logger.Level = logrus.ErrorLevel
	logger.Formatter = &prefixed.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05.000",
		ForceColors:     true,
		ForceFormatting: true,
	}

	return log.NewLogrusLogger(logger, "main", nil)
}

func GetProjectRootPath(projectRootDir string) string {
	cwd, err := os.Getwd()
	cwdOrig := cwd
	if err != nil {
		panic(err)
	}
	for {
		if strings.HasSuffix(cwd, "/"+projectRootDir) {
			return cwd
		}
		lastSlashIndex := strings.LastIndex(cwd, "/")
		if lastSlashIndex == -1 {
			panic(cwdOrig + " did not contain /" + projectRootDir)
		}
		cwd = cwd[0:lastSlashIndex]
	}
}
