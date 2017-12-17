package transport_test

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/transport"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var (
	localAddr1 = fmt.Sprintf("%v:%v", transport.DefaultHost, transport.DefaultTcpPort)
	localAddr2 = fmt.Sprintf("%v:%v", transport.DefaultHost, transport.DefaultTcpPort+1)
	localAddr3 = fmt.Sprintf("%v:%v", transport.DefaultHost, transport.DefaultTcpPort+2)
)

func TestTransport(t *testing.T) {
	// setup logger
	lvl := log.ErrorLevel
	forceColor := true
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "--test.v") || strings.HasPrefix(arg, "--ginkgo.v") {
			lvl = log.DebugLevel
		} else if strings.HasPrefix(arg, "--ginkgo.noColor") {
			forceColor = false
		}
	}
	log.SetLevel(lvl)
	log.SetFormatter(log.NewFormatter(true, forceColor))

	// setup Ginkgo
	RegisterFailHandler(Fail)
	RegisterTestingT(t)
	RunSpecs(t, "Transport Suite")
}

//----------------------------------
// Helpers
//----------------------------------
func createStreamClientServer(network string, addr string) (net.Conn, net.Conn) {
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

func createPacketClientServer(network string, addr string) (net.Conn, net.Conn) {
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

func createClient(network string, raddr string, laddr string) net.Conn {
	network = strings.ToLower(network)

	var err error
	switch network {
	case "udp":
		var la *net.UDPAddr
		if laddr != "" {
			la, err = net.ResolveUDPAddr(network, laddr)
			Expect(err).ToNot(HaveOccurred())
		}
		ra, err := net.ResolveUDPAddr(network, raddr)
		Expect(err).ToNot(HaveOccurred())

		client, err := net.DialUDP(network, la, ra)
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())

		return client
	case "tcp":
		var la *net.TCPAddr
		if laddr != "" {
			la, err = net.ResolveTCPAddr(network, laddr)
			Expect(err).ToNot(HaveOccurred())
		}
		ra, err := net.ResolveTCPAddr(network, raddr)
		Expect(err).ToNot(HaveOccurred())

		client, err := net.DialTCP(network, la, ra)
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())

		return client
	default:
		Fail("unsupported network " + network)
	}

	client, err := net.Dial(network, raddr)
	Expect(err).ToNot(HaveOccurred())
	Expect(client).ToNot(BeNil())

	return client
}

func writeToConn(conn net.Conn, data []byte) {
	num, err := conn.Write(data)
	Expect(err).ToNot(HaveOccurred())
	Expect(num).To(Equal(len(data)))
}

type mockListener struct {
	addr        net.Addr
	connections chan net.Conn
	closed      chan bool
}

func NewMockListener(addr net.Addr) *mockListener {
	return &mockListener{
		addr:        addr,
		connections: make(chan net.Conn),
		closed:      make(chan bool),
	}
}

func (ls *mockListener) Accept() (net.Conn, error) {
	select {
	case <-ls.closed:
		return nil, &net.OpError{"accept", ls.addr.Network(), ls.addr, nil,
			errors.New("listener closed")}
	case conn := <-ls.connections:
		return conn, nil
	}
}

func (ls *mockListener) Close() error {
	defer func() { recover() }()
	close(ls.closed)
	return nil
}

func (ls *mockListener) Addr() net.Addr {
	return ls.addr
}

func (ls *mockListener) Dial(network string, addr net.Addr) (net.Conn, error) {
	select {
	case <-ls.closed:
		return nil, &net.OpError{"dial", addr.Network(), ls.addr, addr,
			errors.New("listener closed")}
	default:
	}

	server, client := net.Pipe()
	ls.connections <- &mockConn{server, addr, server.RemoteAddr()}

	return &mockConn{client, client.LocalAddr(), addr}, nil
}

type mockAddr struct {
	network string
	addr    string
}

func (addr *mockAddr) Network() string {
	return addr.network
}

func (addr *mockAddr) String() string {
	return addr.addr
}

type mockConn struct {
	net.Conn
	localAddr  net.Addr
	remoteAddr net.Addr
}

func (conn *mockConn) LocalAddr() net.Addr {
	return conn.localAddr
}

func (conn *mockConn) RemoteAddr() net.Addr {
	return conn.remoteAddr
}

func assertIncomingMessageArrived(
	fromCh <-chan *core.IncomingMessage,
	expectedMessage string,
	expectedLocalAddr string,
	expectedRemoteAddr string,
) {
	incomingMsg := <-fromCh
	Expect(incomingMsg).ToNot(BeNil())
	Expect(incomingMsg.Msg).ToNot(BeNil())
	Expect(strings.Trim(incomingMsg.Msg.String(), " \r\n")).To(Equal(strings.Trim(expectedMessage, " \r\n")))
	Expect(incomingMsg.LAddr).To(Equal(expectedLocalAddr))
	Expect(incomingMsg.RAddr).To(Equal(expectedRemoteAddr))
}

func assertIncomingErrorArrived(
	fromCh <-chan error,
	expected string,
) {
	err := <-fromCh
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring(expected))
}
