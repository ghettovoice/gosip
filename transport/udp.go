package transport

import (
	"fmt"
	"net"
	"strings"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/lex"
)

// UDP stdProtocol implementation
type udpProtocol struct {
	stdProtocol
	// outgoing connections
	connections *connectionsPool
	// incoming listeners
	listeners []*net.UDPConn
}

func NewUdpProtocol() Protocol {
	udp := &udpProtocol{
		connections: NewConnectionsPool(),
		listeners:   make([]*net.UDPConn, 0),
	}
	udp.init("UDP", false, false, udp.onStop)
	return udp
}

func (udp *udpProtocol) Listen(addr string) error {
	network := strings.ToLower(udp.Name())
	addr = fillAddr(udp.Name(), addr)
	laddr, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return NewError(fmt.Sprintf(
			"failed to resolve %s address %s: %s",
			udp.Name(),
			addr,
			err,
		))
	}

	udp.Log().Debugf("receive resolved address %s", laddr)
	conn, err := net.ListenUDP(network, laddr)
	if err != nil {
		return NewError(fmt.Sprintf(
			"failed to listen %s on address %s: %s",
			udp.Name(),
			addr,
			err,
		))
	}

	udp.listeners = append(udp.listeners, conn)
	udp.wg.Add(1)
	go func() {
		defer udp.wg.Done()
		udp.listenConn(conn)
	}()

	return err // should be nil here
}

func (udp *udpProtocol) listenConn(baseConn *net.UDPConn) {
	udp.Log().Infof("begin listening %s connections on address %s", udp.Name(), baseConn.LocalAddr())

	buf := make([]byte, bufferSize)
	for {
		select {
		case <-udp.stop:
			udp.Log().Infof("stop listening %s connections on address %s", udp.Name(), baseConn.LocalAddr())
			return
		default:
			// read and parse new UDP packet
			conn := NewConnection(baseConn, udp.IsStream())
			conn.SetLog(udp.Log())
			num, err := conn.Read(buf)
			if err != nil {
				udp.errs <- NewError(fmt.Sprintf(
					"connection %p failed to read data from %s to %s over %s: %s",
					conn,
					conn.RemoteAddr(),
					conn.LocalAddr(),
					udp.Name(),
					err,
				))
				continue
			}
			udp.Log().Debugf(
				"connection %p received %d bytes from %s to %s over %s",
				conn,
				num,
				conn.RemoteAddr(),
				conn.LocalAddr(),
				udp.Name(),
			)
			pkt := append([]byte{}, buf[:num]...)
			go func() {
				if msg, err := lex.ParseMessage(pkt, conn.Log()); err == nil {
					udp.output <- &IncomingMessage{msg, conn.LocalAddr(), conn.RemoteAddr()}
				} else {
					udp.errs <- NewError(fmt.Sprintf(
						"connection %p failed to parse SIP message from %s to %s over %s: %s",
						conn,
						conn.RemoteAddr(),
						conn.LocalAddr(),
						udp.Name(),
						err,
					))
				}
			}()
		}
	}
}

func (udp *udpProtocol) Send(addr string, msg core.Message) error {
	udp.Log().Infof("sending message %s to %s over %s", msg.Short(), addr, udp.Name())
	udp.Log().Debugf("sending message to %s over %s:\r\n%s", addr, udp.Name(), msg)

	conn, err := udp.getOrCreateConnection(addr)
	if err != nil {
		return err
	}

	data := []byte(msg.String())
	num, err := conn.Write(data)
	if num != len(data) {
		return NewError(fmt.Sprintf(
			"failed to send message %s to %s over %s: written bytes num %d, but expected %d",
			msg.Short(),
			addr,
			udp.Name(),
			num,
			len(data),
		))
	}

	return err // should be nil
}

func (udp *udpProtocol) getOrCreateConnection(addr string) (Connection, error) {
	network := strings.ToLower(udp.Name())
	raddr, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return nil, NewError(fmt.Sprintf(
			"failed to resolve %s address %s: %s",
			udp.Name(),
			addr,
			err,
		))
	}

	if conn, ok := udp.connections.Get(raddr); ok {
		return conn, nil
	}

	laddr, _ := net.ResolveUDPAddr(network, fmt.Sprintf("%v:%v", DefaultHost, DefaultUdpPort))
	baseConn, err := net.DialUDP(network, laddr, raddr)
	if err != nil {
		return nil, NewError(fmt.Sprintf(
			"failed to create %s connection to address %s: %s",
			udp.Name(),
			addr,
			err,
		))
	}

	conn := NewConnection(baseConn, udp.IsStream())
	conn.SetLog(udp.Log())
	udp.connections.Add(raddr, conn)

	return conn, nil
}

func (udp *udpProtocol) onStop() error {
	udp.Log().Debugf("closing all active %s connections", udp.Name())
	for _, conn := range udp.connections.All() {
		conn.Close()
		udp.connections.Drop(conn.RemoteAddr())
	}
	udp.Log().Debugf("closing all active %s listeners", udp.Name())
	for _, conn := range udp.listeners {
		conn.Close()
	}
	udp.listeners = make([]*net.UDPConn, 0)

	return nil
}
