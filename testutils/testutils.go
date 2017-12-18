package testutils

import (
	"net"
	"strings"

	"github.com/ghettovoice/gosip/core"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
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
	fromCh <-chan core.Message,
	expectedMessage string,
	expectedSource string,
	expectedDest string,
) core.Message {
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
