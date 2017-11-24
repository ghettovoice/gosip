package transport

import (
	"fmt"
	"net"
	"sync"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
	"github.com/sirupsen/logrus"
)

type Protocol interface {
	log.WithLogger
	Name() string
	IsReliable() bool
	IsStream() bool
	SetOutput(output chan *IncomingMessage)
	Output() <-chan *IncomingMessage
	SetErrors(errs chan error)
	Errors() <-chan error
	Listen(addr string) error
	Send(addr string, msg core.Message) error
	Stop()
}

type stdProtocol struct {
	log      log.Logger
	name     string
	reliable bool
	stream   bool
	output   chan *IncomingMessage
	errs     chan error
	stop     chan bool
	wg       *sync.WaitGroup
	onStop   func() error
}

func (pr *stdProtocol) init(name string, reliable bool, stream bool, onStop func() error) {
	pr.name = name
	pr.reliable = reliable
	pr.stream = stream
	pr.onStop = onStop
	pr.output = make(chan *IncomingMessage)
	pr.errs = make(chan error)
	pr.stop = make(chan bool, 1)
	pr.wg = new(sync.WaitGroup)
	pr.SetLog(log.StandardLogger())
}

func (pr *stdProtocol) SetLog(logger log.Logger) {
	pr.log = logger.WithFields(logrus.Fields{
		"stdProtocol":     pr.Name(),
		"stdProtocol-ptr": fmt.Sprintf("%p", pr),
	})
}

func (pr *stdProtocol) Log() log.Logger {
	return pr.log
}

func (pr *stdProtocol) Name() string {
	return pr.name
}

func (pr *stdProtocol) IsReliable() bool {
	return pr.reliable
}

func (pr *stdProtocol) IsStream() bool {
	return pr.stream
}

func (pr *stdProtocol) SetOutput(output chan *IncomingMessage) {
	if pr.output != nil {
		close(pr.output)
	}
	pr.output = output
}

func (pr *stdProtocol) Output() <-chan *IncomingMessage {
	return pr.output
}

func (pr *stdProtocol) SetErrors(errs chan error) {
	if pr.errs != nil {
		close(pr.errs)
	}
	pr.errs = errs
}

func (pr *stdProtocol) Errors() <-chan error {
	return pr.errs
}

func (pr *stdProtocol) Stop() {
	pr.Log().Infof("stop %s stdProtocol", pr.Name())
	pr.stop <- true
	pr.wg.Wait()

	pr.Log().Debugf("disposing %s output channels", pr.Name())
	close(pr.output)
	close(pr.errs)

	if err := pr.onStop(); err != nil {
		pr.Log().Error(err)
	}
}

// Incoming message with meta info: remote addr, local addr & etc.
type IncomingMessage struct {
	// SIP message
	Msg core.Message
	// Local address to which message arrived
	LAddr net.Addr
	// Remote address from which message arrived
	RAddr net.Addr
}
