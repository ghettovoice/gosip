package transport

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
)

// UDP protocol implementation
type udpProtocol struct {
	protocol
	connections ConnectionPool
}

func NewUdpProtocol(ctx context.Context, output chan<- *IncomingMessage, errs chan<- error) Protocol {
	udp := new(udpProtocol)
	udp.network = "udp"
	udp.reliable = false
	udp.streamed = false
	udp.connections = NewConnectionPool(ctx, output, errs)
	udp.SetLog(log.StandardLogger())
	// start up pool
	go udp.connections.Manage()

	return udp
}

func (udp *udpProtocol) SetLog(logger log.Logger) {
	udp.protocol.SetLog(logger)
	udp.connections.SetLog(udp.Log())
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
	// index by local address
	udp.connections.Add(conn.LocalAddr(), conn, 0)

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
