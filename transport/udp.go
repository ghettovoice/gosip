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
	udp.init("UDP", false, false, udp.onStop)
	return udp
}

func (udp *udpProtocol) Listen(target *Target) error {
	// fill empty target props with default values
	target = FillTargetHostAndPort(udp.Network(), target)
	return udp.serve(target, 1)
}

// serves connection with recreation of broken connections
func (udp *udpProtocol) serve(target *Target, try uint8) error {
	network := strings.ToLower(udp.Network())
	addr := target.Addr()
	// resolve local UDP endpoint
	laddr, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return &Error{
			Txt: fmt.Sprintf(
				"%s failed to resolve local address %s: %s",
				udp,
				addr,
				err,
			),
			Protocol: udp.String(),
			LAddr:    addr,
		}
	}

	udpConn, err := net.ListenUDP(network, laddr)
	if err != nil {
		return &Error{
			Txt: fmt.Sprintf(
				"%s failed to listen address %s: %s",
				udp,
				laddr,
				err,
			),
			Protocol: udp.String(),
			LAddr:    laddr.String(),
		}
	}
	// register new connection
	conn := NewConnection(udpConn)
	conn.SetLog(udp.Log())
	udp.connections.Add(laddr, conn)
	// start connection serving
	go func() {
		select {
		case <-udp.stop:
			// stop called, exit immediately
			return
		case err := <-udp.serveConnection(conn):
			// if the connection falls try to recreate 3 times then pass up error
			// todo check on Timeout error?
			if err != nil {
				try++
				if try > 3 {
					select {
					case udp.errs <- err: // send error
					case <-udp.stop: // or just exit if protocol was stopped
					}
				} else {
					// recreate connection
					conn.Close()
					udp.connections.Drop(laddr)
					udp.serve(target, try)
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
		return &Error{
			Txt: fmt.Sprintf(
				"failed to send message '%s' %p: invalid remote host resolved %s",
				msg.Short(),
				msg,
				target.Host,
			),
			Protocol: udp.String(),
			RAddr:    target.Addr(),
			LAddr:    laddr.String(),
		}
	}

	addr := target.Addr()
	udp.Log().Infof("sending message '%s' to %s", msg.Short(), addr)
	udp.Log().Debugf("sending message '%s' to %s:\r\n%s", msg.Short(), addr, msg)

	network := strings.ToLower(udp.String())
	// resolve remote address
	raddr, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return &Error{
			Txt: fmt.Sprintf(
				"%s failed to resolve remote address %s: %s",
				udp,
				addr,
				err,
			),
			Protocol: udp.String(),
			LAddr:    laddr.String(),
			RAddr:    raddr.String(),
		}
	}

	baseConn, err := net.DialUDP(network, laddr, raddr)
	if err != nil {
		return &Error{
			Txt: fmt.Sprintf(
				"%s failed to create connection to address %s: %s",
				udp,
				addr,
				err,
			),
			Protocol: udp.String(),
			LAddr:    laddr.String(),
			RAddr:    raddr.String(),
		}
	}

	conn := NewConnection(baseConn)
	defer conn.Close()
	conn.SetLog(udp.Log())

	data := []byte(msg.String())
	_, err = conn.Write(data)

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
