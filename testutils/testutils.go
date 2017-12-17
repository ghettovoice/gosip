package testutils

import (
	"errors"
	"net"
	"strings"

	"github.com/ghettovoice/gosip/transp"
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
		la, err = net.ResolveUDPAddr(network, laddr)
		Expect(err).ToNot(HaveOccurred())
		ra, err = net.ResolveUDPAddr(network, raddr)
		Expect(err).ToNot(HaveOccurred())

		client, err := net.DialUDP(network, nil, ra.(*net.UDPAddr))
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())

		return client
	case "tcp":
		la, err = net.ResolveTCPAddr(network, laddr)
		Expect(err).ToNot(HaveOccurred())
		ra, err = net.ResolveTCPAddr(network, raddr)
		Expect(err).ToNot(HaveOccurred())

		client, err := net.DialTCP(network, nil, ra.(*net.TCPAddr))
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())

		return client
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

type MockListener struct {
	addr        net.Addr
	connections chan net.Conn
	closed      chan bool
}

func NewMockListener(addr net.Addr) *MockListener {
	return &MockListener{
		addr:        addr,
		connections: make(chan net.Conn),
		closed:      make(chan bool),
	}
}

func (ls *MockListener) Accept() (net.Conn, error) {
	select {
	case <-ls.closed:
		return nil, &net.OpError{"accept", ls.addr.Network(), ls.addr, nil,
			errors.New("listener closed")}
	case conn := <-ls.connections:
		return conn, nil
	}
}

func (ls *MockListener) Close() error {
	defer func() { recover() }()
	close(ls.closed)
	return nil
}

func (ls *MockListener) Addr() net.Addr {
	return ls.addr
}

func (ls *MockListener) Dial(network string, addr net.Addr) (net.Conn, error) {
	select {
	case <-ls.closed:
		return nil, &net.OpError{"dial", addr.Network(), ls.addr, addr,
			errors.New("listener closed")}
	default:
	}

	server, client := net.Pipe()
	ls.connections <- &MockConn{server, addr, server.RemoteAddr()}

	return &MockConn{client, client.LocalAddr(), addr}, nil
}

type MockAddr struct {
	Net  string
	Addr string
}

func (addr *MockAddr) Network() string {
	return addr.Net
}

func (addr *MockAddr) String() string {
	return addr.Addr
}

type MockConn struct {
	net.Conn
	LAddr net.Addr
	RAddr net.Addr
}

func (conn *MockConn) LocalAddr() net.Addr {
	return conn.LAddr
}

func (conn *MockConn) RemoteAddr() net.Addr {
	return conn.RAddr
}

func AssertIncomingMessageArrived(
	fromCh <-chan *transp.IncomingMessage,
	expectedMessage string,
	expectedLocalAddr string,
	expectedRemoteAddr string,
) *transp.IncomingMessage {
	incomingMsg := <-fromCh
	Expect(incomingMsg).ToNot(BeNil())
	Expect(incomingMsg.Msg).ToNot(BeNil())
	Expect(strings.Trim(incomingMsg.Msg.String(), " \r\n")).To(Equal(strings.Trim(expectedMessage, " \r\n")))
	Expect(incomingMsg.LAddr).To(Equal(expectedLocalAddr))
	Expect(incomingMsg.RAddr).To(Equal(expectedRemoteAddr))
	return incomingMsg
}

func AssertIncomingErrorArrived(
	fromCh <-chan error,
	expected string,
) {
	err := <-fromCh
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring(expected))
}
