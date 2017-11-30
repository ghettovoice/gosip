package transport

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/syntax"
	"github.com/sirupsen/logrus"
)

const (
	netErrRetryTime = 5 * time.Second
)

// Protocol implements network specific transport features.
type Protocol interface {
	log.WithLogger
	Network() string
	IsReliable() bool
	IsStream() bool
	// Channel were incoming messages arrives
	Output() <-chan *IncomingMessage
	// Channel for protocol errors
	Errors() <-chan error
	Listen(target *Target) error
	Send(target *Target, msg core.Message) error
	Stop()
	String() string
}

type protocol struct {
	log      log.Logger
	network  string
	reliable bool
	stream   bool
	output   chan *IncomingMessage
	errs     chan error
	stop     chan bool
	wg       *sync.WaitGroup
}

func (pr *protocol) init(
	network string,
	reliable bool,
	stream bool,
) {
	pr.network = network
	pr.reliable = reliable
	pr.stream = stream
	pr.output = make(chan *IncomingMessage)
	pr.errs = make(chan error)
	pr.stop = make(chan bool)
	pr.wg = new(sync.WaitGroup)
	pr.SetLog(log.StandardLogger())
}

func (pr *protocol) SetLog(logger log.Logger) {
	pr.log = logger.WithFields(logrus.Fields{
		"protocol": pr.String(),
	})
}

func (pr *protocol) String() string {
	var name, network string
	if pr == nil {
		name = "<nil>"
		network = ""
	} else {
		name = fmt.Sprintf("%p", pr)
		network = pr.Network() + " "
	}

	return fmt.Sprintf("%sprotocol %p", network, name)
}

func (pr *protocol) Log() log.Logger {
	return pr.log
}

func (pr *protocol) Network() string {
	return strings.ToUpper(pr.network)
}

func (pr *protocol) IsReliable() bool {
	return pr.reliable
}

func (pr *protocol) IsStream() bool {
	return pr.stream
}

func (pr *protocol) Output() <-chan *IncomingMessage {
	return pr.output
}

func (pr *protocol) Errors() <-chan error {
	return pr.errs
}

func (pr *protocol) Stop() {
	pr.Log().Infof("stop %s", pr)
	// unlock and exit all goroutines
	close(pr.stop)
	// wait while goroutines completes
	pr.Log().Debug("wait until serving goroutines completes")
	pr.wg.Wait()
	// close outputs
	close(pr.output)
	close(pr.errs)
	pr.output = nil
	pr.errs = nil
}

// executes connection serving in separate goroutines
// call to this method is non-blocking
func (pr *protocol) serveConnection(
	conn Connection,
	incomingMessages chan<- *IncomingMessage,
	incomingErrs chan<- error,
) {
	pr.Log().Infof("begin serving %s on address %s", conn, conn.LocalAddr())
	// create parser for connection
	wg := new(sync.WaitGroup)
	// internal channels
	messages := make(chan core.Message)
	errs := make(chan error)
	parser := syntax.NewParser(messages, errs, conn.IsStream())
	parser.SetLog(conn.Log())

	pr.wg.Add(1)
	wg.Add(2)
	go pr.readConnection(conn, parser, errs, wg)
	go pr.pipeConnection(conn, parser, messages, errs, incomingMessages, incomingErrs, wg)

	go func() {
		defer func() {
			pr.wg.Done()
			pr.Log().Infof("stop serving %s on address %s", conn, conn.LocalAddr())
		}()
		wg.Wait()
		close(messages)
		close(errs)
	}()
}

// reads data from connection
// blocks until new data arrived
func (pr *protocol) readConnection(conn Connection, parser syntax.Parser, errs chan<- error, wg *sync.WaitGroup) {
	defer func() {
		wg.Done()
		pr.Log().Debugf("stop reading from %s on address %s with %s", conn, conn.LocalAddr(), parser)
	}()
	pr.Log().Debugf("begin reading from %s on address %s with %s", conn, conn.LocalAddr(), parser)

	buf := make([]byte, bufferSize)
	for {
		select {
		case <-pr.stop: // protocol stop was called
			return
		default:
			num, err := conn.Read(buf)
			if err != nil {
				// if we get timeout error just go further and try read on the next iteration
				if err, ok := err.(net.Error); ok {
					if err.Timeout() || err.Temporary() {
						pr.Log().Debugf("%s timeout or temporary unavailable, sleep by %d seconds", conn, netErrRetryTime)
						time.Sleep(netErrRetryTime)
						continue
					}
				}
				// broken or closed connection, stop reading and piping
				// so passing up error
				select {
				case <-pr.stop: // protocol stop called
				case errs <- err:
				}
				return
			}

			pkt := append([]byte{}, buf[:num]...)
			if _, err := parser.Write(pkt); err != nil {
				select {
				case <-pr.stop: // protocol stop called
					return
				case errs <- err:
				}
			}
		}
	}
}

// pipes parsed messages and errors to protocol outputs
func (pr *protocol) pipeConnection(
	conn Connection,
	parser syntax.Parser,
	messages <-chan core.Message,
	errs <-chan error,
	incomingMessages chan<- *IncomingMessage,
	incomingErrs chan<- error,
	wg *sync.WaitGroup,
) {
	defer func() {
		wg.Done()
		pr.Log().Debugf("stop piping outputs from %s on address %s with %s", conn, conn.LocalAddr(), parser)
	}()
	pr.Log().Debugf("start piping outputs from %s on address %s with %s", conn, conn.LocalAddr(), parser)

	for {
		select {
		case <-pr.stop: // protocol stop called
			return
		case msg, ok := <-messages:
			if !ok {
				// connection was closed, exit
				return
			}
			if msg != nil {
				pr.Log().Infof("%s received message '%s' from %s and %s, passing it up", pr, msg.Short(), conn, parser)

				incomingMsg := &IncomingMessage{msg, conn.LocalAddr(), conn.RemoteAddr()}
				select {
				case <-pr.stop: // protocol stop called
					return
				case incomingMessages <- incomingMsg:
					pr.Log().Debugf("%s passed up message '%s' %p", pr, msg.Short(), msg)
				}
			}
		case err, ok := <-errs:
			if !ok {
				// connection was closed, exit
				return
			}
			if err != nil {
				// on parser errors just reset parser and pass the error up
				//
				// all other unhandled errors (connection fall & etc) lead to halt piping
				// so drop connection, pass the error up and exit
				fatal := true

				if err, ok := err.(syntax.Error); ok {
					pr.Log().Warnf("reset %s for %s due to parser error: %s", parser, conn, err)
					parser.Reset()
					fatal = false
				}

				select {
				case <-pr.stop: // protocol stop called
					return
				case incomingErrs <- err:
					pr.Log().Debugf("%s passed up unhandled error %s", pr, err)
					if fatal {
						// connection error, exit
						return
					}
				}
			}
		}
	}
}
