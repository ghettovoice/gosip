package transport

import (
	"fmt"
	"net"
	"strings"
)

// TCP protocol implementation
type tcpProtocol struct {
	protocol
	connections *connectionsPool
	listeners   []*net.TCPListener
}

func NewTcpProtocol() Protocol {
	tcp := &tcpProtocol{
		connections: NewConnectionsPool(),
		listeners:   make([]*net.TCPListener, 0),
	}
	tcp.init("TCP", true, true, tcp.onStop)
	return tcp
}

func (tcp *tcpProtocol) Listen(addr string) error {
	network := strings.ToLower(tcp.Name())
	addr = fillLocalAddr(tcp.Name(), addr)
	laddr, err := net.ResolveTCPAddr(network, addr)
	if err != nil {
		return NewError(fmt.Sprintf(
			"%s protocol %p failed to resolve address %s: %s",
			tcp.Name(),
			tcp,
			addr,
			err,
		))
	}

	listener, err := net.ListenTCP(network, laddr)
	if err != nil {
		return NewError(fmt.Sprintf(
			"%s protocol %p failed to listen on address %s: %s",
			tcp.Name(),
			tcp,
			addr,
			err,
		))
	}

	tcp.listeners = append(tcp.listeners, listener)
	tcp.wg.Add(1)
	go func() {
		defer tcp.wg.Done()
		tcp.serve(listener)
	}()

	return err // should be nil here
}

func (tcp *tcpProtocol) serve(listener *net.TCPListener) {
	tcp.Log().Infof("begin serving listener on address %s", listener.Addr())

	for {
		select {
		case <-tcp.stop:
			tcp.Log().Infof("stop serving connections on address %s", listener.Addr())
			return
		default:
			baseConn, err := listener.Accept()
			if err != nil {
				tcp.Log().Errorf(
					"%s protocol %p failed to accept connection on address %s: %s",
					tcp.Name(),
					tcp,
					listener.Addr(),
					err,
				)
				continue
			}

			conn := NewConnection(baseConn, tcp.IsStream())
			conn.SetLog(tcp.Log())

			tcp.Log().Infof(
				"accepted connection %p from %s to %s",
				conn,
				conn.RemoteAddr(),
				conn.LocalAddr(),
			)
			// TODO serve connection
		}
	}
}
