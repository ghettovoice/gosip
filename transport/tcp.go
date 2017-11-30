package transport

import (
	"fmt"
	"net"
	"strings"

	"github.com/ghettovoice/gosip/core"
)

// TCP protocol implementation
type tcpProtocol struct {
	protocol
	connections *connectionPool
	listeners   *listenerPool
}

func NewTcpProtocol() Protocol {
	tcp := &tcpProtocol{
		connections: NewConnectionPool(),
		listeners:   NewListenerPool(),
	}
	tcp.init("tcp", true, true)
	return tcp
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
	// start listener serving
	go tcp.serveListener(listener)

	return err // should be nil here
}

func (tcp *tcpProtocol) serveListener(listener net.Listener) {
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
				err = &ProtocolError{
					fmt.Errorf(
						"%s failed to accept connection on address %s: %s",
						tcp,
						listener.Addr(),
						err,
					),
					"accept connection",
					tcp,
				}
				tcp.Log().Error(err)
				select {
				case <-tcp.stop:
				case tcp.errs <- err:
					tcp.closeListener(listener)
				}
				return
			}

			conn := NewConnection(baseConn)
			conn.SetLog(tcp.Log())
			// index connections by remote address
			tcp.connections.Add(conn.RemoteAddr(), conn)
			tcp.Log().Infof("%s accepted connection %p from %s to %s", tcp, conn, conn.RemoteAddr(), conn.LocalAddr())
			// start connection serving
			// TODO split into goroutines
			errs := make(chan error)
			tcp.serveConnection(conn, tcp.output, errs)
			tcp.wg.Add(1)
			go func() {
				defer func() {
					tcp.wg.Done()
					close(errs)
				}()
				for tcp.errs != nil {
					select {
					case <-tcp.stop:
						return
					case err, ok := <-errs:
						if !ok {
							return
						}
						if _, ok := err.(net.Error); ok {
							tcp.Log().Errorf("%s received connection error: %s; connection %s will be closed", tcp, err, conn)
							conn.Close()
							tcp.connections.Drop(conn.LocalAddr())
						}
						select {
						case <-tcp.stop:
							return
						case tcp.errs <- err:
						}
					}
				}
			}()
		}
	}
}

func (tcp *tcpProtocol) closeListener(listener net.Listener) {
	for _, conn := range tcp.connections.All() {
		if conn.LocalAddr().String() == listener.Addr().String() {
			conn.Close()
			tcp.connections.Drop(conn.RemoteAddr())
		}
	}
	listener.Close()
	tcp.listeners.Drop(listener.Addr())
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
	}

	return conn, nil
}

func (tcp *tcpProtocol) Stop() {
	tcp.protocol.Stop()

	tcp.Log().Debugf("disposing all active connections")
	for _, conn := range tcp.connections.All() {
		conn.Close()
		tcp.connections.Drop(conn.RemoteAddr())
	}
	tcp.Log().Debugf("disposing all active listeners")
	for _, listener := range tcp.listeners.All() {
		listener.Close()
		tcp.listeners.Drop(listener.Addr())
	}
}
