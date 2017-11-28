package transport

import (
	"fmt"
	"net"
	"sync"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/lex"
	"github.com/ghettovoice/gosip/log"
	"github.com/sirupsen/logrus"
)

// Incoming message with meta info: remote addr, local addr & etc.
type IncomingMessage struct {
	// SIP message
	Msg core.Message
	// Local address to which message arrived
	LAddr net.Addr
	// Remote address from which message arrived
	RAddr net.Addr
}

// Protocol implements network specific transport features.
type Protocol interface {
	log.WithLogger
	Name() string
	IsReliable() bool
	IsStream() bool
	SetOutput(output chan *IncomingMessage)
	SetErrors(errs chan error)
	Listen(addr string) error
	Send(addr string, msg core.Message) error
	Stop()
}

type protocol struct {
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

func (pr *protocol) init(
	output chan *IncomingMessage,
	errs chan error,
	name string,
	reliable bool,
	stream bool,
	onStop func() error,
) {
	pr.name = name
	pr.reliable = reliable
	pr.stream = stream
	pr.onStop = onStop
	pr.output = output
	pr.errs = errs
	pr.stop = make(chan bool)
	pr.wg = new(sync.WaitGroup)
	pr.SetLog(log.StandardLogger())
}

func (pr *protocol) SetLog(logger log.Logger) {
	pr.log = logger.WithFields(logrus.Fields{
		"protocol":     pr.Name(),
		"protocol-ptr": fmt.Sprintf("%p", pr),
	})
}

func (pr *protocol) Log() log.Logger {
	return pr.log
}

func (pr *protocol) Name() string {
	return pr.name
}

func (pr *protocol) IsReliable() bool {
	return pr.reliable
}

func (pr *protocol) IsStream() bool {
	return pr.stream
}

func (pr *protocol) SetOutput(output chan *IncomingMessage) {
	pr.output = output
}

//func (pr *protocol) Output() <-chan *IncomingMessage {
//	return pr.output
//}

func (pr *protocol) SetErrors(errs chan error) {
	pr.errs = errs
}

//func (pr *protocol) Errors() <-chan error {
//	return pr.errs
//}

func (pr *protocol) Stop() {
	pr.Log().Infof("stop %s protocol", pr.Name())
	close(pr.stop) // unlock and exit all goroutines
	pr.wg.Wait()   // wait while goroutines completes
	// execute protocol specific disposing
	if err := pr.onStop(); err != nil {
		pr.Log().Error(err)
	}
}

func (pr *protocol) serveConnection(conn Connection) {
	pr.Log().Infof("begin serving connection %p on address %s", conn, conn.LocalAddr())

	pr.wg.Add(1)
	// create parser for connection
	connWg := new(sync.WaitGroup)
	messages := make(chan core.Message)
	errs := make(chan error)
	parser := lex.NewParser(messages, errs, conn.IsStream())
	parser.SetLog(conn.Log())

	// start connection listener goroutine
	connWg.Add(1)
	go func() {
		defer connWg.Done()
		pr.Log().Debugf("start serving goroutine for connection %p", conn)

		buf := make([]byte, bufferSize)
		for {
			select {
			case <-pr.stop: // stop called
				pr.Log().Infof("stop serving connection %p on address %s", conn, conn.LocalAddr())
				return
			default:
				num, err := conn.Read(buf)
				if err != nil {
					if conn.IsStream() {
						pr.Log().Warnf(
							"connection %p failed to read data from %s to %s over %s protocol %p: %s; "+
								"connection will be re-created",
							conn,
							conn.RemoteAddr(),
							conn.LocalAddr(),
							pr.Name(),
							pr,
							err,
						)
						continue
					} else {
						pr.Log().Errorf(
							"connection %p failed to read data from %s to %s over %s protocol %p: %s",
							conn,
							conn.RemoteAddr(),
							conn.LocalAddr(),
							pr.Name(),
							pr,
							err,
						)
						return
					}
				}

				pr.Log().Debugf(
					"connection %p received %d bytes from %s to %s",
					conn,
					num,
					conn.RemoteAddr(),
					conn.LocalAddr(),
				)

				pkt := append([]byte{}, buf[:num]...)
				parser.Write(pkt)
			}
		}
	}()

	// start piping goroutine
	connWg.Add(1)
	go func() {
		defer connWg.Done()
		pr.Log().Debugf("start piping goroutine for connection %p and parser %p", conn, parser)

		for {
			select {
			case <-pr.stop: // stop called
				pr.Log().Infof(
					"stop piping parser %p outputs for connection %p on address %s",
					parser,
					conn,
					conn.LocalAddr(),
				)
				return
			case msg := <-messages:
				go func() {
					pr.Log().Infof(
						"connection %p from %s to %s received message '%s'",
						conn,
						conn.RemoteAddr(),
						conn.LocalAddr(),
						msg.Short(),
					)

					pr.output <- &IncomingMessage{msg, conn.LocalAddr(), conn.RemoteAddr()}
				}()
			case err := <-errs:
				go func() {
					pr.Log().Warnf(
						"connection %p from %s to %s failed to parse SIP message: %s; restarting parser",
						conn,
						conn.RemoteAddr(),
						conn.LocalAddr(),
						err,
					)
					parser := lex.NewParser(messages, errs, conn.IsStream())
					parser.SetLog(conn.Log())
				}()
			}
		}
	}()

	// wait for connection goroutines completes
	go func() {
		defer pr.wg.Done()
		connWg.Wait()
		close(messages)
		close(errs)
	}()
}
