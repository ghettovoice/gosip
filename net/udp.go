package net

import (
	"fmt"
	"github.com/ghettovoice/gosip/log"
	"net"
)

// UDP protocol implementation
type udpProtocol struct {
	protocol
}

func NewUdpProtocol() Protocol {
	udp := new(udpProtocol)
	udp.connectionsMap = make(map[string]Connection)
	return udp
}

func (udp *udpProtocol) Log() log.Logger {
	return udp.log.WithField("protocol", udp.Name())
}

func (udp *udpProtocol) Name() string {
	return "UDP"
}

func (udp *udpProtocol) IsReliable() bool {
	return false
}

func (udp *udpProtocol) Listen(addr string) error {
	udp.Log().Infof("initializing new %s listener on address %s", udp.Name(), addr)

	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		udp.Log().Errorf("failed to listen %s protocol on address %s: %s", udp.Name(), addr, err.Error())
		return err
	}
	go udp.listen(conn)

	return err // should be nil here
}

func (udp *udpProtocol) listen(baseConn net.PacketConn) {
	udp.Log().Infof("begin listening for %s on address %s", udp.Name(), baseConn.LocalAddr())

	buf := make([]byte, bufferSize)
	for {
		select {
		case <-udp.done:
			udp.Log().Infof("done listening for %s on address %s", udp.Name(), baseConn.LocalAddr())
			udp.onDone()
			return
		default:
			num, remoteAddr, err := baseConn.ReadFrom(buf)
			if err != nil {
				udp.errs <- NewError(fmt.Sprintf(
					"failed to read from %s socket on address %s: %s",
					udp.Name(),
					baseConn.LocalAddr(),
					err,
				))
				continue
			}

			conn := NewPacketConnection(
				baseConn,
				baseConn.LocalAddr().String(),
				remoteAddr.String(),
				buf[:num],
				udp.Log(),
			)
			// TODO read from connection
			udp.output <- conn
		}
	}
}

func (udp *udpProtocol) Send(addr string, data []byte) error {

}
