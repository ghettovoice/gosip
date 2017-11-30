package transport

import (
	"fmt"
	"net"
	"strings"

	"github.com/ghettovoice/gosip/core"
)

// UDP protocol implementation
type udpProtocol struct {
	protocol
	// listening connections
	connections *connectionsStore
}

func NewUdpProtocol() Protocol {
	udp := &udpProtocol{
		connections: NewConnectionsStore(),
	}
	udp.init("UDP", false, false)
	return udp
}

func (udp *udpProtocol) Listen(target *Target) error {
	// fill empty target props with default values
	target = FillTargetHostAndPort(udp.Network(), target)
	network := strings.ToLower(udp.Network())
	addr := target.Addr()
	// resolve local UDP endpoint
	laddr, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return &ProtocolError{
			fmt.Errorf("failed to resolve local address %s: %s", addr, err),
			fmt.Sprintf("resolve %s address", udp.Network()),
			udp,
		}
	}

	udpConn, err := net.ListenUDP(network, laddr)
	if err != nil {
		return &ProtocolError{
			fmt.Errorf("failed to listen address %s: %s", laddr, err),
			fmt.Sprintf("create %s listener", udp.Network()),
			udp,
		}
	}
	// register new connection
	conn := NewConnection(udpConn)
	conn.SetLog(udp.Log())
	// store by local address
	udp.connections.Add(conn.LocalAddr(), conn)
	// start connection serving
	errs := make(chan error)
	udp.serveConnection(conn, udp.output, udp.errs)
	udp.wg.Add(1)
	go func() {
		defer func() {
			udp.wg.Done()
			close(errs)
		}()
		for {
			select {
			case <-udp.stop:
				return
			case err, ok := <-errs:
				if !ok {
					return
				}
				if _, ok := err.(net.Error); ok {
					udp.Log().Warnf("%s received connection error: %s; connection %s will be closed", udp, err, conn)
					conn.Close()
					udp.connections.Drop(conn.LocalAddr())
				}
				select {
				case <-udp.stop:
					return
				case udp.errs <- err:
				}
			}
		}
	}()

	return err // should be nil here
}

func (udp *udpProtocol) Send(target *Target, msg core.Message) error {
	laddr := &net.UDPAddr{
		IP:   net.IP(DefaultHost),
		Port: int(DefaultUdpPort),
		Zone: "",
	}
	target = FillTargetHostAndPort(udp.Network(), target)
	// validate remote address
	if target.Host == "" || target.Host == DefaultHost {
		return &ProtocolError{
			fmt.Errorf("invalid remote host resolved %s for message '%s'", target.Host, msg.Short()),
			"resolve destination address",
			udp,
		}
	}

	addr := target.Addr()
	udp.Log().Infof("sending message '%s' to %s", msg.Short(), addr)
	udp.Log().Debugf("sending message '%s' to %s:\r\n%s", msg.Short(), addr, msg)

	network := strings.ToLower(udp.String())
	// resolve remote address
	raddr, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return &ProtocolError{
			fmt.Errorf("failed to resolve remote address %s: %s", addr, err),
			fmt.Sprintf("resolve %s address", addr),
			udp,
		}
	}

	baseConn, err := net.DialUDP(network, laddr, raddr)
	if err != nil {
		return &ProtocolError{
			fmt.Errorf("failed to connection to remote address %s: %s", raddr, err),
			fmt.Sprintf("create %s connection", udp.Network()),
			udp,
		}
	}

	conn := NewConnection(baseConn)
	defer conn.Close()
	conn.SetLog(udp.Log())

	data := []byte(msg.String())
	_, err = conn.Write(data)

	return err // should be nil
}

func (udp *udpProtocol) Stop() {
	udp.protocol.Stop()

	udp.Log().Debugf("disposing all active connections")
	for _, conn := range udp.connections.All() {
		conn.Close()
		udp.connections.Drop(conn.LocalAddr())
	}
}
