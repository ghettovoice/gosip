package net

import (
	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
)

type Protocol interface {
	log.WithLogger
	Name() string
	IsReliable() bool
	Listen(addr string) error
	Send(addr string, data []byte) error
	SetOutput(output chan []byte)
	SetErrors(errs chan error)
	SetDone(done chan bool)
}

type protocol struct {
	connectionsPool
	log    log.Logger
	stop   chan bool
	output chan Connection
	errs   chan error
	done   chan bool
}

func (pr *protocol) SetLog(logger log.Logger) {
	pr.log = logger
}

func (pr *protocol) SetOutput(output chan []byte) {
	pr.output = output
}

func (pr *protocol) SetErrors(errs chan error) {
	pr.errs = errs
}

func (pr *protocol) SetDone(done chan bool) {
	pr.done = done
}

func (pr *protocol) onDone() {
	for _, conn := range pr.connections() {
		conn.Close()
		pr.dropConnection(conn.RemoteAddr())
	}
}
