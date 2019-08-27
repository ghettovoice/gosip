package transport

import (
	"fmt"
	"net"
	"strings"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
)

// UDP protocol implementation
type udpProtocol struct {
	protocol
	connections ConnectionPool
}

func NewUdpProtocol(output chan<- sip.Message, errs chan<- error, cancel <-chan struct{}) Protocol {
	udp := new(udpProtocol)
	udp.network = "udp"
	udp.reliable = false
	udp.streamed = false
	udp.logger = log.NewSafeLocalLogger()
	// TODO: add separate errs chan to listen errors from pool for reconnection?
	udp.connections = NewConnectionPool(output, errs, cancel)
	udp.connections.SetLog(udp.Log())
	return udp
}

func (udp *udpProtocol) String() string {
	return fmt.Sprintf("Udp%s", udp.protocol.String())
}

func (udp *udpProtocol) SetLog(logger log.Logger) {
	udp.protocol.SetLog(logger)
	udp.connections.SetLog(udp.Log())
}

func (udp *udpProtocol) Done() <-chan struct{} {
	return udp.connections.Done()
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
			fmt.Errorf("failed to listen address '%s': %s", laddr, err),
			fmt.Sprintf("create '%s' listener", udp.Network()),
			udp.String(),
		}
	}
	udp.Log().Infof("%s begins listening on '%s'", udp, target.Addr())
	// register new connection
	conn := NewConnection(udpConn)
	conn.SetLog(udp.Log())
	// index by local address, TTL=0 - unlimited expiry time
	key := ConnectionKey(fmt.Sprintf("0.0.0.0:%d", laddr.Port))
	err = udp.connections.Put(key, conn, 0)

	return err // should be nil here
}

func (udp *udpProtocol) Send(target *Target, msg sip.Message) error {
	target = FillTargetHostAndPort(udp.Network(), target)

	udp.Log().Infof("%s sends message:\n%s", udp, msg)

	// validate remote address
	if target.Host == "" {
		return &ProtocolError{
			fmt.Errorf("invalid remote host resolved '%s'", target.Host),
			"resolve destination address",
			udp.String(),
		}
	}

	// resolve remote address
	raddr, err := udp.resolveTarget(target)
	if err != nil {
		return &ProtocolError{
			fmt.Errorf("invalid remote target: %s", err),
			"resolve destination net address",
			udp.String(),
		}
	}

	// send through already opened by connection
	// to always use same local port
	_, port, err := net.SplitHostPort(msg.Source())
	conn, err := udp.connections.Get(ConnectionKey("0.0.0.0:" + port))
	if err != nil {
		// todo change this bloody patch
		if len(udp.connections.All()) == 0 {
			return &ProtocolError{
				fmt.Errorf("connection for send not found: %s", err),
				"resolve connection",
				udp.String(),
			}
		}

		conn = udp.connections.All()[0]
	}

	data := []byte(msg.String())
	_, err = conn.WriteTo(data, raddr)

	return err // should be nil
}

func (udp *udpProtocol) resolveTarget(target *Target) (*net.UDPAddr, error) {
	addr := target.Addr()
	network := strings.ToLower(udp.Network())
	// resolve remote address
	raddr, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return nil, &ProtocolError{
			fmt.Errorf("failed to resolve address '%s': %s", addr, err),
			fmt.Sprintf("resolve '%s' address", addr),
			udp.String(),
		}
	}

	return raddr, nil
}
