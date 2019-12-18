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

func NewUdpProtocol(
	output chan<- sip.Message,
	errs chan<- error,
	cancel <-chan struct{},
	msgMapper sip.MessageMapper,
	logger log.Logger,
) Protocol {
	udp := new(udpProtocol)
	udp.network = "udp"
	udp.reliable = false
	udp.streamed = false
	udp.log = logger.
		WithPrefix("transport.Protocol").
		WithFields(log.Fields{
			"protocol_ptr":     fmt.Sprintf("%p", udp),
			"protocol_network": udp.Network(),
		})
	// TODO: add separate errs chan to listen errors from pool for reconnection?
	udp.connections = NewConnectionPool(output, errs, cancel, msgMapper, udp.Log())

	return udp
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
			err,
			fmt.Sprintf("listen on %s %s address", udp.Network(), laddr),
			fmt.Sprintf("%p", udp),
		}
	}

	udp.Log().Infof("begin listening on %s", laddr)

	// register new connection
	conn := NewConnection(udpConn, udp.Log())
	// index by local address, TTL=0 - unlimited expiry time
	key := ConnectionKey(fmt.Sprintf("0.0.0.0:%d", laddr.Port))
	err = udp.connections.Put(key, conn, 0)

	return err // should be nil here
}

func (udp *udpProtocol) Send(target *Target, msg sip.Message) error {
	target = FillTargetHostAndPort(udp.Network(), target)

	// validate remote address
	if target.Host == "" {
		return &ProtocolError{
			fmt.Errorf("empty remote target host"),
			fmt.Sprintf("send SIP message to %s", target.Addr()),
			fmt.Sprintf("%p", udp),
		}
	}

	// resolve remote address
	raddr, err := udp.resolveTarget(target)
	if err != nil {
		return err
	}

	// send through already opened by connection
	// to always use same local port
	_, port, err := net.SplitHostPort(msg.Source())
	conn, err := udp.connections.Get(ConnectionKey("0.0.0.0:" + port))
	if err != nil {
		// todo change this bloody patch
		if len(udp.connections.All()) == 0 {
			return &ProtocolError{
				fmt.Errorf("connection not found: %w", err),
				fmt.Sprintf("send SIP message to %s", raddr),
				fmt.Sprintf("%p", udp),
			}
		}

		conn = udp.connections.All()[0]
	}

	logger := udp.Log().
		WithFields(conn.Log().Fields()).
		WithFields(msg.Fields())

	logger.Infof("writing SIP message to %s", raddr)

	_, err = conn.WriteTo([]byte(msg.String()), raddr)

	return err // should be nil
}

func (udp *udpProtocol) resolveTarget(target *Target) (*net.UDPAddr, error) {
	addr := target.Addr()
	network := strings.ToLower(udp.Network())
	// resolve remote address
	raddr, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return nil, &ProtocolError{
			err,
			fmt.Sprintf("resolve target address %s", addr),
			fmt.Sprintf("%p", udp),
		}
	}

	return raddr, nil
}
