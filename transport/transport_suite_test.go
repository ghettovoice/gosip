package transport_test

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

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
			lvl = log.InfoLevel
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
