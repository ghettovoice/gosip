package sip

import (
	"net"
)

type TransportUDP struct {
	transportBase
}

func NewTransportUDP(opts TransportOptions) *TransportUDP {
	tp := new(TransportUDP)
	tp.proto = TransportProtoUDP
	tp.TransportOptions = opts
	return tp
}

func (tp *TransportUDP) Proto() string { return tp.proto }

func (tp *TransportUDP) IsReliable() bool { return false }

func (tp *TransportUDP) IsStreamed() bool { return false }

func (tp *TransportUDP) IsSecured() bool { return false }

func (tp *TransportUDP) ListenAndServe(addr string, onMsg func(Message), _ ...any) error {
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	return tp.Serve(conn.(*net.UDPConn), onMsg)
}

func (tp *TransportUDP) Serve(c *net.UDPConn, onMsg func(Message)) error {
	return tp.servePacket(c, onMsg)
}

func (tp *TransportUDP) Close() error { return tp.close() }
