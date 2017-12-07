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
	connections *connectionPool
}

func NewUdpProtocol() Protocol {
	udp := &udpProtocol{
		connections: NewConnectionPool(),
	}
	udp.init("udp", false, false)
	return udp
}

func (udp *udpProtocol) Listen(target *Target) error {
	// fill empty target props with default values
	target = FillTargetHostAndPort(udp.Network(), target)
	network := strings.ToLower(udp.Network())
	// resolve local UDP endpoint
	laddr, err := udp.resolveTarget(target)
	if err != nil {
		return err
	}
	// create UDP connection
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
	udp.connections.Add(ConnKey(conn.LocalAddr()), conn, -1)
	// start connection serving
	errs := make(chan error)
	udp.serveConnection(conn, udp.output, errs)
	udp.wg.Add(1)
	go func() {
		defer func() {
			udp.wg.Done()
			close(errs)
		}()
		for udp.errs != nil {
			select {
			case <-udp.stop:
				return
			case err, ok := <-errs:
				if !ok {
					return
				}
				if _, ok := err.(net.Error); ok {
					udp.Log().Errorf("%s received connection error: %s; connection %s will be closed", udp, err, conn)
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
	target = FillTargetHostAndPort(udp.Network(), target)

	udp.Log().Infof("sending message '%s' to %s", msg.Short(), target.Addr())
	udp.Log().Debugf("sending message '%s' to %s:\r\n%s", msg.Short(), target.Addr(), msg)

	// validate remote address
	if target.Host == "" || target.Host == DefaultHost {
		return &ProtocolError{
			fmt.Errorf("invalid remote host resolved %s", target.Host),
			"resolve destination address",
			udp,
		}
	}

	// resolve remote address
	raddr, err := udp.resolveTarget(target)
	if err != nil {
		return err
	}

	network := strings.ToLower(udp.Network())
	laddr := &net.UDPAddr{
		IP:   net.IP(DefaultHost),
		Port: int(DefaultUdpPort),
		Zone: "",
	}
	udpConn, err := net.DialUDP(network, laddr, raddr)
	if err != nil {
		return &ProtocolError{
			fmt.Errorf("failed to create connection to remote address %s: %s", raddr, err),
			fmt.Sprintf("create %s connection", udp.Network()),
			udp,
		}
	}

	conn := NewConnection(udpConn)
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

func (udp *udpProtocol) resolveTarget(target *Target) (*net.UDPAddr, error) {
	addr := target.Addr()
	network := strings.ToLower(udp.String())
	// resolve remote address
	raddr, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return nil, &ProtocolError{
			fmt.Errorf("failed to resolve address %s: %s", addr, err),
			fmt.Sprintf("resolve %s address", addr),
			udp,
		}
	}

	return raddr, nil
}
