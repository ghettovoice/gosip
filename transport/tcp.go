package transport

import (
	"fmt"
	"net"
	"strings"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
)

// TCP protocol implementation
type tcpProtocol struct {
	protocol
	listeners   ListenerPool
	connections ConnectionPool
	conns       chan Connection
}

func NewTcpProtocol(
	output chan<- sip.Message,
	errs chan<- error,
	cancel <-chan struct{},
	msgMapper sip.MessageMapper,
	logger log.Logger,
) Protocol {
	tcp := new(tcpProtocol)
	tcp.network = "tcp"
	tcp.reliable = true
	tcp.streamed = true
	tcp.conns = make(chan Connection)
	tcp.log = logger.
		WithPrefix("transport.Protocol").
		WithFields(log.Fields{
			"protocol_ptr": fmt.Sprintf("%p", tcp),
		})
	// TODO: add separate errs chan to listen errors from pool for reconnection?
	tcp.listeners = NewListenerPool(tcp.conns, errs, cancel, tcp.Log())
	tcp.connections = NewConnectionPool(output, errs, cancel, msgMapper, tcp.Log())
	// pipe listener and connection pools
	go tcp.pipePools()

	return tcp
}

func (tcp *tcpProtocol) Done() <-chan struct{} {
	return tcp.connections.Done()
}

// piping new connections to connection pool for serving
func (tcp *tcpProtocol) pipePools() {
	defer close(tcp.conns)

	tcp.Log().Debug("start pipe pools")
	defer tcp.Log().Debug("stop pipe pools")

	for {
		select {
		case <-tcp.listeners.Done():
			return
		case conn := <-tcp.conns:
			logger := tcp.Log().WithFields(conn.Log().Fields())

			if err := tcp.connections.Put(conn, sockTTL); err != nil {
				// TODO should it be passed up to UA?
				logger.Errorf("put new TCP connection failed: %s", err)

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
			err,
			fmt.Sprintf("listen on %s %s address", tcp.Network(), laddr),
			fmt.Sprintf("%p", tcp),
		}
	}

	tcp.Log().Infof("begin listening on %s %s", tcp.Network(), laddr)

	// index listeners by local address
	// should live infinitely
	key := ListenerKey(fmt.Sprintf("tcp:0.0.0.0:%d", laddr.Port))
	err = tcp.listeners.Put(key, listener)

	return err // should be nil here
}

func (tcp *tcpProtocol) Send(target *Target, msg sip.Message) error {
	target = FillTargetHostAndPort(tcp.Network(), target)

	// validate remote address
	if target.Host == "" {
		return &ProtocolError{
			fmt.Errorf("empty remote target host"),
			fmt.Sprintf("send SIP message to %s %s", tcp.Network(), target.Addr()),
			fmt.Sprintf("%p", tcp),
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

	logger := tcp.Log().
		WithFields(conn.Log().Fields()).
		WithFields(msg.Fields())

	logger.Infof("writing SIP message to %s %s", tcp.Network(), raddr)

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
			err,
			fmt.Sprintf("resolve target address %s %s", tcp.Network(), addr),
			fmt.Sprintf("%p", tcp),
		}
	}

	return raddr, nil
}

func (tcp *tcpProtocol) getOrCreateConnection(raddr *net.TCPAddr) (Connection, error) {
	network := strings.ToLower(tcp.Network())

	key := ConnectionKey("tcp:" + raddr.String())
	conn, err := tcp.connections.Get(key)
	if err != nil {
		tcp.Log().Debugf("connection for remote address %s %s not found, create a new one", tcp.Network(), raddr)

		tcpConn, err := net.DialTCP(network, nil, raddr)
		if err != nil {
			return nil, &ProtocolError{
				err,
				fmt.Sprintf("connect to %s %s address", tcp.Network(), raddr),
				fmt.Sprintf("%p", tcp),
			}
		}

		conn = NewConnection(tcpConn, key, tcp.Log())

		if err := tcp.connections.Put(conn, sockTTL); err != nil {
			return conn, err
		}
	}

	return conn, nil
}
