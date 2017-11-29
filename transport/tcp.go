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
		return &Error{
			Txt: fmt.Sprintf(
				"%s failed to resolve address %s: %s",
				tcp,
				addr,
				err,
			),
			Protocol: tcp.String(),
			LAddr:    addr,
		}
	}

	listener, err := net.ListenTCP(network, laddr)
	if err != nil {
		return &Error{
			Txt: fmt.Sprintf(
				"%s failed to listen on address %s: %s",
				tcp,
				addr,
				err,
			),
			Protocol: tcp.String(),
			LAddr:    laddr.String(),
		}
	}

	tcp.listeners.Add(laddr, listener)
	// start listener serving
	go tcp.serveListener(listener)

	return err // should be nil here
}

func (tcp *tcpProtocol) serveListener(listener *net.TCPListener) <-chan error {
	tcp.Log().Infof("begin serving listener %p on address %s", listener, listener.Addr())

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
