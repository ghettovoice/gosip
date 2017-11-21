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
	udp.connections = make(map[string]Connection)
	udp.stop = make(chan bool, 1)
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
	udp.Log().Debugf("initializing new %s listener on address %s", udp.Name(), addr)
	baseConn, err := net.ListenPacket("udp", addr)
	if err != nil {
		udp.Log().Errorf("protocol %s listen on address %s failed: %", udp.Name(), addr, err.Error())
		return err
	}
	go udp.listen(baseConn)

	return err // should be nil here
}

func (udp *udpProtocol) listen(baseConn net.PacketConn) {
	udp.Log().Infof("begin listening for %s on address %s", udp.Name(), baseConn.LocalAddr())
	buf := make([]byte, bufferSize)
	for {
		select {
		case <-udp.stop:
			udp.Log().Infof("stopped listening for %s on address %s", udp.Name(), baseConn.LocalAddr())
			return
		default:
			num, remoteAddr, err := baseConn.ReadFrom(buf)
			if err != nil {
				err := fmt.Errorf("failed to read from %s socket on address %s: %s", udp.Name(), baseConn.LocalAddr(), err)
				udp.Log().Error(err)
				udp.errs <- err
				continue
			}
			// todo save connction to pool for re-using
			pkt := append([]byte(nil), buf[:num]...)
			udp.output <- pkt
		}
	}
}

func (udp *udpProtocol) Send(addr string, data []byte) error {

}
