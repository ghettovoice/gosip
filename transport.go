// net package implements SIP transport layer.
package gosip

import (
	"fmt"
	"sync"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/transport"
)

// Transport layer is responsible for the actual transmission of messages - RFC 3261 - 18.
type Transport interface {
	log.WithLogger
	SetOutput(output chan core.Message)
	Output() <-chan core.Message
	SetErrors(errs chan error)
	Errors() <-chan error
	// Registers new stdTransport protocol.
	Register(protocol transport.Protocol) error
	// Listen starts listening on `addr` for each registered protocol.
	Listen(addr string) error
	// Send sends message on suitable protocol.
	Send(addr string, msg core.Message) error
	// Stops all protocols
	Stop()
}

// Transport layer implementation.
type stdTransport struct {
	protocols *protocolsPool
	log       log.Logger
	hostname  string
	output    chan core.Message
	errs      chan error
	stop      chan bool
	wg        *sync.WaitGroup
}

// NewTransport creates transport layer.
// 	- hostname - current server hostname (IP or FQDN)
// 	- protocols - initial slice of protocols for register in layer
func NewTransport(
	hostname string,
	protocols []transport.Protocol,
) *stdTransport {
	tp := &stdTransport{
		hostname:  hostname,
		output:    make(chan core.Message),
		errs:      make(chan error),
		stop:      make(chan bool, 1),
		wg:        new(sync.WaitGroup),
		protocols: NewProtocolsPool(),
	}
	tp.SetLog(log.StandardLogger())

	for _, protocol := range protocols {
		tp.Register(protocol)
	}

	return tp
}

func (tp *stdTransport) Log() log.Logger {
	return tp.log
}

func (tp *stdTransport) SetLog(logger log.Logger) {
	tp.log = logger.WithField("transport-ptr", fmt.Sprintf("%p", tp))
}

func (tp *stdTransport) SetOutput(output chan core.Message) {
	tp.output = output
}

func (tp *stdTransport) Output() <-chan core.Message {
	return tp.output
}

func (tp *stdTransport) SetErrors(errs chan error) {
	tp.errs = errs
}

func (tp *stdTransport) Errors() <-chan error {
	return tp.errs
}

func (tp *stdTransport) Register(protocol transport.Protocol) error {
	if _, ok := tp.protocols.Get(protocol.Name()); ok {
		return transport.NewError(fmt.Sprintf("protocol %s already registered", protocol.Name()))
	}

	output := make(chan *transport.IncomingMessage)
	errs := make(chan error)
	protocol.SetLog(tp.Log())
	protocol.SetOutput(output)
	protocol.SetErrors(errs)
	tp.protocols.Add(protocol.Name(), protocol)

	return nil
}

func (tp *stdTransport) Listen(addr string) error {
	tp.Log().Info("begin listening all registered protocols")

	for _, protocol := range tp.protocols.All() {
		// star protocol listening
		if err := protocol.Listen(addr); err != nil {
			// return error right away to be more explicitly
			return err
		}
		// start protocol output forwarding goroutine
		tp.wg.Add(1)
		go func() {
			defer tp.wg.Done()
			tp.handleProtocol(protocol)
		}()
	}

	return nil
}

func (tp *stdTransport) Send(addr string, msg core.Message) error {
	// TODO implement
	return nil
}

func (tp *stdTransport) Stop() {
	tp.Log().Info("stop transport layer")
	tp.stop <- true
	tp.wg.Wait()

	tp.Log().Debug("closing transport output channels")
	close(tp.output)
	close(tp.errs)

	tp.Log().Debug("stop all registered protocols")
	for _, protocol := range tp.protocols.All() {
		protocol.Stop()
	}
}

func (tp *stdTransport) handleProtocol(protocol transport.Protocol) {
	for {
		select {
		// handle stop signal
		case <-tp.stop:
			tp.Log().Debugf("stop %s protocol", protocol.Name())
			return
			// forward incoming message
		case incoming := <-protocol.Output():
			go tp.onProtocolIncoming(incoming, protocol)
			// forward errors
		case err := <-protocol.Errors():
			go tp.onProtocolError(err, protocol)
		}
	}
}

// handles incoming message from protocol
// should be called inside goroutine for non-blocking forwarding
func (tp *stdTransport) onProtocolIncoming(incoming *transport.IncomingMessage, protocol transport.Protocol) {
	switch msg := incoming.Msg.(type) {
	case core.Response:
		// RFC 3261 - 18.1.2. - Receiving Responses.
		viaHop, ok := msg.ViaHop()
		if !ok {
			tp.Log().Warnf(
				"discarding message %s from %s over %s: empty or malformed 'Via' header",
				msg.Short(),
				incoming.RAddr,
				protocol.Name(),
			)
			return
		}

		if viaHop.Host != tp.hostname {
			tp.Log().Warnf(
				"discarding message %s from %s over %s: 'sent-by' in the first 'Via' header "+
					" equals to %s, but expected %s",
				msg.Short(),
				incoming.RAddr,
				protocol.Name(),
				viaHop.Host,
				tp.hostname,
			)
			return
		}
		// pass up message
		tp.output <- msg
	case core.Request:
		// RFC 3261 - 18.2.1. - Receiving Request.
		viaHop, ok := msg.ViaHop()
		if !ok {
			tp.Log().Warnf(
				"discarding message %s from %s over %s: empty or malformed 'Via' header",
				msg.Short(),
				incoming.RAddr,
				protocol.Name(),
			)
			return
		}

		if viaHop.Host != tp.hostname {
			tp.Log().Warnf(
				"discarding message %s from %s over %s: 'sent-by' in the first 'Via' header "+
					" equals to %s, but expected %s",
				msg.Short(),
				incoming.RAddr,
				protocol.Name(),
				viaHop.Host,
				tp.hostname,
			)
			return
		}
		// pss up message
		tp.output <- msg
	}
}

// handles protocol errors
// should be called inside goroutine for non-blocking forwarding
func (tp *stdTransport) onProtocolError(err error, protocol transport.Protocol) {
	if err, ok := err.(*transport.Error); !ok {
		err = transport.NewError(err.Error())
	}
	tp.errs <- err
}

// Thread-safe protocols pool.
type protocolsPool struct {
	lock      *sync.RWMutex
	protocols map[string]transport.Protocol
}

func NewProtocolsPool() *protocolsPool {
	return &protocolsPool{
		lock:      new(sync.RWMutex),
		protocols: make(map[string]transport.Protocol),
	}
}

func (pool *protocolsPool) Add(key string, protocol transport.Protocol) {
	pool.lock.Lock()
	pool.protocols[key] = protocol
	pool.lock.Unlock()
}

func (pool *protocolsPool) Get(key string) (transport.Protocol, bool) {
	pool.lock.RLock()
	defer pool.lock.RUnlock()
	protocol, ok := pool.protocols[key]
	return protocol, ok
}

func (pool *protocolsPool) All() []transport.Protocol {
	all := make([]transport.Protocol, 0)
	for key := range pool.protocols {
		if protocol, ok := pool.Get(key); ok {
			all = append(all, protocol)
		}
	}

	return all
}
