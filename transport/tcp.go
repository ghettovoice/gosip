package transport

import (
	"fmt"
	"net"
	"strings"
)

// TCP protocol implementation
type tcpProtocol struct {
	protocol
	connections *connectionsStore
	listeners   *listenersStore
}

func NewTcpProtocol() Protocol {
	tcp := &tcpProtocol{
		connections: NewConnectionsStore(),
		listeners:   NewListenersStore(),
	}
	tcp.init("TCP", true, true, tcp.onStop)
	return tcp
}

func (tcp *tcpProtocol) Listen(target *Target) error {
	target = FillTargetHostAndPort(tcp.Network(), target)
	network := strings.ToLower(tcp.Network())
	addr := target.Addr()
	// resolve local TCP endpoint
	laddr, err := net.ResolveTCPAddr(network, addr)
	if err != nil {
		return &ProtocolError{
			fmt.Errorf("failed to resolve local address %s: %s", addr, err),
			fmt.Sprintf("resolve %s address", tcp.Network()),
			tcp,
		}
	}

	listener, err := net.ListenTCP(network, laddr)
	if err != nil {
		return &ProtocolError{
			fmt.Errorf("failed to listen address %s: %s", laddr, err),
			fmt.Sprintf("create %s listener", tcp.Network()),
			tcp,
		}
	}

	tcp.listeners.Add(laddr, listener)
	// start listener serving
	go tcp.serveListener(listener)

	return err // should be nil here
}

func (tcp *tcpProtocol) serveListener(listener *net.TCPListener) {
	defer func() {
		tcp.Log().Infof("stop serving listener %p on address %s", listener, listener.Addr())
	}()
	tcp.Log().Infof("begin serving listener %p on address %s", listener, listener.Addr())

	for {
		select {
		case <-tcp.stop: // protocol stop was called
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
