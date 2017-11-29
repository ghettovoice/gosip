package transport

import (
	"fmt"
	"sync"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/lex"
	"github.com/ghettovoice/gosip/log"
	"github.com/sirupsen/logrus"
	"net"
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
	onStop   func() error
}

func (pr *protocol) init(
	network string,
	reliable bool,
	stream bool,
	onStop func() error,
) {
	pr.network = network
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
	return pr.network
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
	pr.Log().Infof("begin serving %s on address %s", conn, conn.LocalAddr())
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
			pr.Log().Debugf("stop reading %s on address %s", conn, conn.LocalAddr())
			// close parser output channels here due to nothing to parse without connection
			close(parserOutput)
			close(parserErrs)
		}()
		pr.Log().Debugf("start reading goroutine for %s", conn)

		buf := make([]byte, bufferSize)
		for {
			select {
			case <-pr.stop: // protocol stop was called
				return
			default:
				num, err := conn.Read(buf)
				if err != nil {
					// if we get timeout error just go further and try read on the next iteration
					if err, ok := err.(net.Error); ok && err.Timeout() {
						continue
					}
					// broken connection, stop reading and piping
					// return error to the caller for handling
					outErrs <- err
					return
				}

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
			pr.Log().Debugf(
				"stop piping %s outputs for %s on address %s",
				parser,
				conn,
				conn.LocalAddr(),
			)
		}()
		pr.Log().Debugf("start piping goroutine for %s and %s", conn, parser)

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
						"%s received message '%s' from %s and %s, passing it up",
						pr,
						msg.Short(),
						conn,
						parser,
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
						"%s received parse error from %s and %s: %s",
						pr,
						conn,
						parser,
						err,
					)

					select {
					case pr.errs <- err:
						parser := lex.NewParser(parserOutput, parserErrs, conn.IsStream())
						parser.SetLog(conn.Log())
					case <-pr.stop: // protocol stop called
						return
					}
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
