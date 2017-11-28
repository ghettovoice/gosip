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
	connections *connectionsPool
}

func NewUdpProtocol() Protocol {
	udp := &udpProtocol{
		connections: NewConnectionsPool(),
	}
	udp.init("UDP", false, false, udp.onStop)
	return udp
}

func (udp *udpProtocol) Listen(addr string) error {
	return udp.serve(addr, 1)
}

// serves connection with recreation of broken connections
func (udp *udpProtocol) serve(addr string, try uint8) error {
	network := strings.ToLower(udp.Name())
	addr = fillLocalAddr(udp.Name(), addr)
	laddr, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return NewError(fmt.Sprintf(
			"%s protocol %p failed to resolve local address %s: %s",
			udp.Name(),
			udp,
			addr,
			err,
		))
	}

	conn, ok := udp.connections.Get(laddr)
	if ok {
		// connection already serving
		return nil
	}

	udpConn, err := net.ListenUDP(network, laddr)
	if err != nil {
		return NewError(fmt.Sprintf(
			"%s protocol %p failed to listen address %s: %s",
			udp.Name(),
			udp,
			addr,
			err,
		))
	}
	// register new connection
	conn = NewConnection(udpConn)
	conn.SetLog(udp.Log())
	udp.connections.Add(laddr, conn)
	// start connection serving
	go func() {
		// run serving connection
		// if the connection falls try to recreate 3 times and pass up error
		if err := <-udp.serveConnection(conn); err != nil {
			try++
			if try > 3 {
				err = NewError(fmt.Sprintf(
					"%s connection %p was closed: %s",
					udp.Name(),
					conn,
					err,
				))
				select {
				case udp.errs <- err: // send error
				case <-udp.stop: // or just exit if protocol was stopped
				}
			} else {
				udp.serve(addr, try)
			}
		}
	}()

	return err // should be nil here
}

func (udp *udpProtocol) Send(addr string, msg core.Message) error {
	udp.Log().Infof("sending message '%s' to %s", msg.Short(), addr)
	udp.Log().Debugf("sending message '%s' to %s:\r\n%s", msg.Short(), addr, msg)

	network := strings.ToLower(udp.Name())
	// resolve remote address
	raddr, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return NewError(fmt.Sprintf(
			"%s protocol %p failed to resolve remote address %s: %s",
			udp.Name(),
			udp,
			addr,
			err,
		))
	}

	baseConn, err := net.DialUDP(network, nil, raddr)
	if err != nil {
		return NewError(fmt.Sprintf(
			"%s protocol %p failed to create connection to address %s: %s",
			udp.Name(),
			udp,
			addr,
			err,
		))
	}

	conn := NewConnection(baseConn)
	defer conn.Close()
	conn.SetLog(udp.Log())
	udp.connections.Add(raddr, conn)

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

func (udp *udpProtocol) onStop() error {
	udp.Log().Debugf("disposing all active connections")
	for _, conn := range udp.connections.All() {
		conn.Close()
		udp.connections.Drop(conn.RemoteAddr())
	}

	return nil
}
