package transport

import (
	"fmt"
	"net"
	"strings"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/lex"
)

// UDP protocol implementation
type udpProtocol struct {
	protocol
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
			"%s protocol %p failed to resolve address %s: %s",
			udp.Name(),
			udp,
			addr,
			err,
		))
	}

	conn, err := net.ListenUDP(network, laddr)
	if err != nil {
		return NewError(fmt.Sprintf(
			"%s protocol %p failed to listen address %s: %s",
			udp.Name(),
			udp,
			addr,
			err,
		))
	}

	udp.listeners = append(udp.listeners, conn)
	udp.wg.Add(1)
	go func() {
		defer udp.wg.Done()
		udp.serve(conn)
	}()

	return err // should be nil here
}

func (udp *udpProtocol) serve(baseConn *net.UDPConn) {
	udp.Log().Infof("begin serving connection on address %s", baseConn.LocalAddr())

	buf := make([]byte, bufferSize)
	for {
		select {
		case <-udp.stop:
			udp.Log().Infof("stop serving connection on address %s", baseConn.LocalAddr())
			return
		default:
			// read and parse new UDP packet
			conn := NewConnection(baseConn, udp.IsStream())
			conn.SetLog(udp.Log())
			num, err := conn.Read(buf)
			if err != nil {
				udp.errs <- NewError(fmt.Sprintf(
					"connection %p failed to read data from %s to %s over %s protocol %p: %s",
					conn,
					conn.RemoteAddr(),
					conn.LocalAddr(),
					udp.Name(),
					udp,
					err,
				))
				continue
			}

			udp.Log().Infof(
				"connection %p received %d bytes from %s to %s",
				conn,
				num,
				conn.RemoteAddr(),
				conn.LocalAddr(),
			)

			pkt := append([]byte{}, buf[:num]...)
			go func() {
				if msg, err := lex.ParseMessage(pkt, conn.Log()); err == nil {
					udp.output <- &IncomingMessage{msg, conn.LocalAddr(), conn.RemoteAddr()}
				} else {
					udp.errs <- NewError(fmt.Sprintf(
						"connection %p failed to parse SIP message from %s to %s over %s protocol %p: %s",
						conn,
						conn.RemoteAddr(),
						conn.LocalAddr(),
						udp.Name(),
						udp,
						err,
					))
				}
			}()
		}
	}
}

func (udp *udpProtocol) Send(addr string, msg core.Message) error {
	udp.Log().Infof("sending message '%s' to %s", msg.Short(), addr)
	udp.Log().Debugf("sending message '%s' to %s:\r\n%s", msg.Short(), addr, msg)

	conn, err := udp.getOrCreateConnection(addr)
	if err != nil {
		return err
	}

	data := []byte(msg.String())
	num, err := conn.Write(data)
	if num != len(data) {
		return NewError(fmt.Sprintf(
			"connection %p failed to send message '%s' to %s over %s protocol %p: "+
				"written bytes num %d, but expected %d",
			conn,
			msg.Short(),
			addr,
			udp.Name(),
			udp,
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
			"%s protocol %p failed to resolve address %s: %s",
			udp.Name(),
			udp,
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
			"%s protocol %p failed to create connection to address %s: %s",
			udp.Name(),
			udp,
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
	udp.Log().Debugf("disposing all active connections")
	for _, conn := range udp.connections.All() {
		conn.Close()
		udp.connections.Drop(conn.RemoteAddr())
	}
	udp.Log().Debugf("disposing all active listeners")
	for _, conn := range udp.listeners {
		conn.Close()
	}
	udp.listeners = make([]*net.UDPConn, 0)

	return nil
}
