package transport

import (
	"fmt"
	"net"
	"strings"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
)

// TCP protocol implementation
type tcpProtocol struct {
	protocol
	listeners   ListenerPool
	connections ConnectionPool
	conns       chan Connection
}

func NewTcpProtocol(output chan<- *IncomingMessage, errs chan<- error, cancel <-chan struct{}) Protocol {
	tcp := new(tcpProtocol)
	tcp.network = "tcp"
	tcp.reliable = true
	tcp.streamed = true
	tcp.conns = make(chan Connection)
	tcp.logger = log.NewSafeLocalLogger()
	// TODO: add separate errs chan to listen errors from pool for reconnection?
	tcp.listeners = NewListenerPool(tcp.conns, errs, cancel)
	tcp.listeners.SetLog(tcp.Log())
	tcp.connections = NewConnectionPool(output, errs, cancel)
	tcp.connections.SetLog(tcp.Log())
	// pipe listener and connection pools
	go tcp.pipePools()

	return tcp
}

func (tcp *tcpProtocol) String() string {
	return fmt.Sprintf("Tcp%s", tcp.protocol.String())
}

func (tcp *tcpProtocol) SetLog(logger log.Logger) {
	tcp.protocol.SetLog(logger)
	tcp.listeners.SetLog(tcp.Log())
	tcp.connections.SetLog(tcp.Log())
}

func (tcp *tcpProtocol) Done() <-chan struct{} {
	return tcp.connections.Done()
}

// piping new connections to connection pool for serving
func (tcp *tcpProtocol) pipePools() {
	defer func() {
		tcp.Log().Infof("%s stops pipe pools", tcp)
		close(tcp.conns)
	}()
	tcp.Log().Infof("%s starts pipe pools", tcp)

	for {
		select {
		case <-tcp.listeners.Done():
			return
		case conn := <-tcp.conns:
			if err := tcp.connections.Put(ConnectionKey(conn.RemoteAddr().String()), conn, sockTTL); err != nil {
				// TODO should it be passed up to UA?
				tcp.Log().Errorf("%s failed to put new %s to %s: %s", tcp, conn, tcp.connections, err)
				continue
			}
		}
	}
}

func (tcp *tcpProtocol) Listen(target *Target) error {
	target = FillTargetHostAndPort(tcp.Network(), target)
	network := strings.ToLower(tcp.Network())
	// resolve local TCP endpoint
	laddr, err := tcp.resolveTarget(target)
	if err != nil {
		return err
	}
	// create listener
	listener, err := net.ListenTCP(network, laddr)
	if err != nil {
		return &ProtocolError{
			fmt.Errorf("failed to listen address %s: %s", laddr, err),
			fmt.Sprintf("create %s listener", tcp.Network()),
			tcp.String(),
		}
	}
	tcp.Log().Infof("%s begins listening on %s", tcp, target)
	// index listeners by local address
	// should live infinitely
	err = tcp.listeners.Put(ListenerKey(listener.Addr().String()), listener)

	return err // should be nil here
}

func (tcp *tcpProtocol) Send(target *Target, msg core.Message) error {
	target = FillTargetHostAndPort(tcp.Network(), target)

	tcp.Log().Infof("%s sends message '%s' to %s", tcp, msg.Short(), target.Addr())
	tcp.Log().Debugf("%s sends message '%s' to %s:\r\n%s", tcp, msg.Short(), target.Addr(), msg)

	// validate remote address
	if target.Host == "" {
		return &ProtocolError{
			fmt.Errorf("invalid remote host resolved %s", target.Host),
			"resolve destination address",
			tcp.String(),
		}
	}
	// resolve remote address
	raddr, err := tcp.resolveTarget(target)
	if err != nil {
		return err
	}
	// find or create connection
	conn, err := tcp.getOrCreateConnection(raddr)
	if err != nil {
		return err
	}
	// send message
	_, err = conn.Write([]byte(msg.String()))

	return err
}

func (tcp *tcpProtocol) resolveTarget(target *Target) (*net.TCPAddr, error) {
	addr := target.Addr()
	network := strings.ToLower(tcp.Network())
	// resolve remote address
	raddr, err := net.ResolveTCPAddr(network, addr)
	if err != nil {
		return nil, &ProtocolError{
			fmt.Errorf("failed to resolve address %s: %s", addr, err),
			fmt.Sprintf("resolve %s address", addr),
			tcp.String(),
		}
	}

	return raddr, nil
}

func (tcp *tcpProtocol) getOrCreateConnection(raddr *net.TCPAddr) (Connection, error) {
	network := strings.ToLower(tcp.Network())
	conn, err := tcp.connections.Get(ConnectionKey(raddr.String()))
	if err != nil {
		tcp.Log().Debugf("connection for address %s not found; create a new one", raddr)
		tcpConn, err := net.DialTCP(network, nil, raddr)
		if err != nil {
			return nil, &ProtocolError{
				fmt.Errorf("failed to create connection to remote address %s: %s", raddr, err),
				fmt.Sprintf("create %s connection", tcp.Network()),
				tcp.String(),
			}
		}

		conn = NewConnection(tcpConn)
		conn.SetLog(tcp.Log())
		tcp.connections.Put(ConnectionKey(conn.RemoteAddr().String()), conn, sockTTL)
	}

	return conn, nil
}
