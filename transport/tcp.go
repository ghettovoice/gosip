package transport

import (
	"context"
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

func NewTcpProtocol(ctx context.Context, output chan<- *IncomingMessage, errs chan<- error) Protocol {
	tcp := new(tcpProtocol)
	tcp.network = "tcp"
	tcp.reliable = true
	tcp.streamed = true
	tcp.conns = make(chan Connection)
	tcp.listeners = NewListenerPool(ctx, tcp.conns, errs)
	tcp.connections = NewConnectionPool(ctx, output, errs)
	tcp.SetLog(log.StandardLogger())
	// start up pools
	go tcp.listeners.Manage()
	go tcp.connections.Manage()
	go tcp.manage(ctx)

	return tcp
}

func (tcp *tcpProtocol) SetLog(logger log.Logger) {
	tcp.protocol.SetLog(logger)
	tcp.listeners.SetLog(tcp.Log())
	tcp.connections.SetLog(tcp.Log())
}

// piping new connections to connection pool for serving
func (tcp *tcpProtocol) manage(ctx context.Context) {
	defer func() {
		tcp.Log().Debugf("stop %s managing", tcp)
		tcp.dispose()
	}()
	tcp.Log().Debugf("start %s managing", tcp)

	for {
		select {
		case <-ctx.Done():
			return
		case conn := <-tcp.conns:
			if err := tcp.connections.Add(conn.RemoteAddr(), conn, socketTtl); err != nil {
				// TODO should it be passed up to UA?
				tcp.Log().Errorf("%s failed to add new %s to %s: %s", tcp, conn, tcp.connections, err)
				continue
			}
		}
	}
}

func (tcp *tcpProtocol) dispose() {
	tcp.Log().Debugf("dispose %s", tcp)
	close(tcp.conns)
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
			tcp,
		}
	}
	// index listeners by local address
	tcp.listeners.Add(listener.Addr(), listener)

	return err // should be nil here
}

func (tcp *tcpProtocol) Send(target *Target, msg core.Message) error {
	target = FillTargetHostAndPort(tcp.Network(), target)

	tcp.Log().Infof("sending message '%s' to %s", msg.Short(), target.Addr())
	tcp.Log().Debugf("sending message '%s' to %s:\r\n%s", msg.Short(), target.Addr(), msg)

	// validate remote address
	if target.Host == "" || target.Host == DefaultHost {
		return &ProtocolError{
			fmt.Errorf("invalid remote host resolved %s", target.Host),
			"resolve destination address",
			tcp,
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
	network := strings.ToLower(tcp.String())
	// resolve remote address
	raddr, err := net.ResolveTCPAddr(network, addr)
	if err != nil {
		return nil, &ProtocolError{
			fmt.Errorf("failed to resolve address %s: %s", addr, err),
			fmt.Sprintf("resolve %s address", addr),
			tcp,
		}
	}

	return raddr, nil
}

func (tcp *tcpProtocol) getOrCreateConnection(raddr *net.TCPAddr) (Connection, error) {
	network := strings.ToLower(tcp.String())
	laddr := &net.TCPAddr{
		IP:   net.IP(DefaultHost),
		Port: int(DefaultUdpPort),
		Zone: "",
	}

	conn, ok := tcp.connections.Get(raddr)
	if !ok {
		tcp.Log().Debugf("connection for address %s not found; create a new one", raddr)
		tcpConn, err := net.DialTCP(network, laddr, raddr)
		if err != nil {
			return nil, &ProtocolError{
				fmt.Errorf("failed to create connection to remote address %s: %s", raddr, err),
				fmt.Sprintf("create %s connection", tcp.Network()),
				tcp,
			}
		}

		conn = NewConnection(tcpConn)
		conn.SetLog(tcp.Log())
		tcp.connections.Add(conn.RemoteAddr(), conn, socketTtl)
	}

	return conn, nil
}
