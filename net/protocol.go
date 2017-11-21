package net

import (
	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
)

type Protocol interface {
	log.WithLogger
	core.DataTunnel
	Name() string
	IsReliable() bool
	Listen(addr string) error
	Send(addr string, data []byte) error
	Stop()
}

type protocol struct {
	connectionsPool
	log    log.Logger
	stop   chan bool
	output chan []byte
	errs   chan error
}

func (pr *protocol) SetLog(logger log.Logger) {
	pr.log = logger
}

func (pr *protocol) SetOutput(output chan []byte) {
	pr.output = output
}

func (pr *protocol) Output() <-chan []byte {
	return pr.output
}

func (pr *protocol) SetErrors(errs chan error) {
	pr.errs = errs
}

func (pr *protocol) Errors() <-chan error {
	return pr.errs
}

func (pr *protocol) Stop() {
	pr.stop <- true
	for _, conn := range pr.Connections() {
		conn.Close()
		pr.DropConnection(conn.RemoteAddr())
	}
}
