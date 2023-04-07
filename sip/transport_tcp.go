package sip

import (
	"net"
)

type TransportTCP struct {
	transportBase
}

func NewTransportTCP(opts TransportOptions) *TransportTCP {
	tp := new(TransportTCP)
	tp.proto = TransportProtoTCP
	tp.TransportOptions = opts
	return tp
}

func (tp *TransportTCP) Proto() string { return tp.proto }

func (tp *TransportTCP) IsReliable() bool { return true }

func (tp *TransportTCP) IsStreamed() bool { return true }

func (tp *TransportTCP) IsSecured() bool { return false }

func (tp *TransportTCP) ListenAndServe(addr string, onMsg func(Message), _ ...any) error {
	ls, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return tp.Serve(ls.(*net.TCPListener), onMsg)
}

func (tp *TransportTCP) Serve(ls *net.TCPListener, onMsg func(Message)) error {
	return tp.serveStream(ls, onMsg)
}

func (tp *TransportTCP) Close() error { return tp.close() }
