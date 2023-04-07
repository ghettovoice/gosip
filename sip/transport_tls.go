package sip

import (
	"crypto/tls"
	"net"
)

type TransportTLS struct {
	transportBase
}

func NewTransportTLS(opts TransportOptions) *TransportTLS {
	tp := new(TransportTLS)
	tp.proto = TransportProtoTLS
	tp.TransportOptions = opts
	return tp
}

func (tp *TransportTLS) Proto() string { return tp.proto }

func (tp *TransportTLS) IsReliable() bool { return true }

func (tp *TransportTLS) IsStreamed() bool { return true }

func (tp *TransportTLS) IsSecured() bool { return true }

func (tp *TransportTLS) ListenAndServe(addr string, onMsg func(Message), opts ...any) error {
	var cfg *tls.Config
	for _, opt := range opts {
		if o, ok := opt.(*tls.Config); ok {
			cfg = o
		}
	}

	ls, err := tls.Listen("tcp", addr, cfg)
	if err != nil {
		return err
	}
	return tp.Serve(ls.(*net.TCPListener), onMsg)
}

func (tp *TransportTLS) Serve(ls *net.TCPListener, onMsg func(Message)) error {
	return tp.serveStream(ls, onMsg)
}

func (tp *TransportTLS) Close() error { return tp.close() }
