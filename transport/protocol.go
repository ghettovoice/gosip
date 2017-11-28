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
	// Channel were incoming messages arrives
	Output() <-chan *IncomingMessage
	// Channel for protocol errors
	Errors() <-chan error
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
	name string,
	reliable bool,
	stream bool,
	onStop func() error,
) {
	pr.name = name
	pr.reliable = reliable
	pr.stream = stream
	pr.onStop = onStop
	pr.output = make(chan *IncomingMessage)
	pr.errs = make(chan error)
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

func (pr *protocol) Output() <-chan *IncomingMessage {
	return pr.output
}

func (pr *protocol) Errors() <-chan error {
	return pr.errs
}

func (pr *protocol) Stop() {
	pr.Log().Infof("stop %s protocol", pr.Name())
	// unlock and exit all goroutines
	close(pr.stop)
	// wait while goroutines completes
	pr.wg.Wait()
	// close outputs
	close(pr.output)
	close(pr.errs)
	// execute protocol specific disposing
	if err := pr.onStop(); err != nil {
		pr.Log().Error(err)
	}
}

// serves connection with related parser
func (pr *protocol) serveConnection(conn Connection) <-chan error {
	pr.Log().Infof("begin serving connection %p on address %s", conn, conn.LocalAddr())
	// TODO split into two methods: readConnection and pipeConnection
	pr.wg.Add(1)
	outErrs := make(chan error, 1)
	// create parser for connection
	connWg := new(sync.WaitGroup)
	parserOutput := make(chan core.Message)
	parserErrs := make(chan error)
	parser := lex.NewParser(parserOutput, parserErrs, conn.IsStream())
	parser.SetLog(conn.Log())

	// start reading goroutine
	go func() {
		defer func() {
			connWg.Done()
			pr.Log().Infof("stop reading connection %p on address %s", conn, conn.LocalAddr())
			// close parser output channels here due to nothing to parse without connection
			close(parserOutput)
			close(parserErrs)
		}()
		pr.Log().Debugf("start reading goroutine for connection %p", conn)

		buf := make([]byte, bufferSize)
		for {
			select {
			case <-pr.stop: // protocol stop was called
				return
			default:
				num, err := conn.Read(buf)
				if err != nil {
					// broken connection, stop serving and piping
					outErrs <- NewError(fmt.Sprintf(
						"connection %p failed to read data from %s to %s over %s protocol %p: %s",
						conn,
						conn.RemoteAddr(),
						conn.LocalAddr(),
						pr.Name(),
						pr,
						err,
					))
					return
				}

				pr.Log().Debugf(
					"connection %p received %d bytes from %s to %s",
					conn,
					num,
					conn.RemoteAddr(),
					conn.LocalAddr(),
				)

				pkt := append([]byte{}, buf[:num]...)
				if _, err := parser.Write(pkt); err != nil {
					select {
					case parserErrs <- err:
					case <-pr.stop: // protocol stop called
						return
					}
				}
			}
		}
	}()

	// start piping parser outputs goroutine
	connWg.Add(1)
	go func() {
		defer func() {
			connWg.Done()
			pr.Log().Infof(
				"stop piping parser %p outputs for connection %p on address %s",
				parser,
				conn,
				conn.LocalAddr(),
			)
		}()
		pr.Log().Debugf("start piping goroutine for connection %p and parser %p", conn, parser)

		for {
			select {
			case <-pr.stop: // protocol stop called
				return
			case msg, ok := <-parserOutput:
				if !ok {
					// connection was closed, then exit
					return
				}
				if msg != nil {
					pr.Log().Infof(
						"connection %p from %s to %s received message '%s'",
						conn,
						conn.RemoteAddr(),
						conn.LocalAddr(),
						msg.Short(),
					)

					incomingMsg := &IncomingMessage{msg, conn.LocalAddr(), conn.RemoteAddr()}
					select {
					case pr.output <- incomingMsg:
					case <-pr.stop: // protocol stop called
						return
					}

				}
			case err, ok := <-parserErrs:
				if !ok {
					// connection was closed, then exit
					return
				}
				if err != nil {
					pr.Log().Warnf(
						"connection %p from %s to %s failed to parse SIP message: %s; recreating parser",
						conn,
						conn.RemoteAddr(),
						conn.LocalAddr(),
						err,
					)
					parser := lex.NewParser(parserOutput, parserErrs, conn.IsStream())
					parser.SetLog(conn.Log())
				}
			}
		}
	}()

	// wait for connection goroutines completes
	go func() {
		defer pr.wg.Done()
		connWg.Wait()
		close(outErrs)
	}()

	return outErrs
}
