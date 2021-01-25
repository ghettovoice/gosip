package transport

import (
	"fmt"
	"net"

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
	p := new(udpProtocol)
	p.network = "udp"
	p.reliable = false
	p.streamed = false
	p.log = logger.
		WithPrefix("transport.Protocol").
		WithFields(log.Fields{
			"protocol_ptr": fmt.Sprintf("%p", p),
		})
	// TODO: add separate errs chan to listen errors from pool for reconnection?
	p.connections = NewConnectionPool(output, errs, cancel, msgMapper, p.Log())

	return p
}

func (p *udpProtocol) Done() <-chan struct{} {
	return p.connections.Done()
}

func (p *udpProtocol) Listen(target *Target, options ...ListenOption) error {
	// fill empty target props with default values
	target = FillTargetHostAndPort(p.Network(), target)
	// resolve local UDP endpoint
	laddr, err := net.ResolveUDPAddr(p.network, target.Addr())
	if err != nil {
		return &ProtocolError{
			err,
			fmt.Sprintf("resolve target address %s %s", p.Network(), target.Addr()),
			fmt.Sprintf("%p", p),
		}
	}
	// create UDP connection
	udpConn, err := net.ListenUDP(p.network, laddr)
	if err != nil {
		return &ProtocolError{
			err,
			fmt.Sprintf("listen on %s %s address", p.Network(), laddr),
			fmt.Sprintf("%p", p),
		}
	}

	p.Log().Debugf("begin listening on %s %s", p.Network(), laddr)

	// register new connection
	// index by local address, TTL=0 - unlimited expiry time
	key := ConnectionKey(fmt.Sprintf("%s:0.0.0.0:%d", p.network, laddr.Port))
	conn := NewConnection(udpConn, key, p.network, p.Log())
	err = p.connections.Put(conn, 0)
	if err != nil {
		err = &ProtocolError{
			Err:      err,
			Op:       fmt.Sprintf("put %s connection to the pool", conn.Key()),
			ProtoPtr: fmt.Sprintf("%p", p),
		}
	}

	return err // should be nil here
}

func (p *udpProtocol) Send(target *Target, msg sip.Message) error {
	target = FillTargetHostAndPort(p.Network(), target)

	// validate remote address
	if target.Host == "" {
		return &ProtocolError{
			fmt.Errorf("empty remote target host"),
			fmt.Sprintf("send SIP message to %s %s", p.Network(), target.Addr()),
			fmt.Sprintf("%p", p),
		}
	}

	// resolve remote address
	raddr, err := net.ResolveUDPAddr(p.network, target.Addr())
	if err != nil {
		return &ProtocolError{
			err,
			fmt.Sprintf("resolve target address %s %s", p.Network(), target.Addr()),
			fmt.Sprintf("%p", p),
		}
	}

	baseConn, err := net.DialUDP(p.network, nil, raddr)
	if err != nil {
		return &ProtocolError{
			Err:      err,
			Op:       fmt.Sprintf("dial to %s", raddr),
			ProtoPtr: fmt.Sprintf("%p", p),
		}
	}

	key := ConnectionKey(fmt.Sprintf("%s:%s", p.network, baseConn.LocalAddr()))
	conn := NewConnection(baseConn, key, p.network, p.Log())

	logger := log.AddFieldsFrom(p.Log(), conn, msg)
	logger.Tracef("writing SIP message to %s %s", p.Network(), raddr)

	_, err = conn.Write([]byte(msg.String()))
	if err != nil {
		err = &ProtocolError{
			Err:      err,
			Op:       fmt.Sprintf("write SIP message to the %s connection", conn.Key()),
			ProtoPtr: fmt.Sprintf("%p", p),
		}
	}

	return err // should be nil
}
