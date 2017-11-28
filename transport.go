// net package implements SIP transport layer.
package gosip

import (
	"fmt"
	"net"
	"sync"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/transport"
)

// Transport layer is responsible for the actual transmission of messages - RFC 3261 - 18.
type Transport interface {
	log.WithLogger
	Output() <-chan core.Message
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
		stop:      make(chan bool),
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
	for _, protocol := range tp.protocols.All() {
		protocol.SetLog(tp.Log())
	}
}

func (tp *stdTransport) Output() <-chan core.Message {
	return tp.output
}

func (tp *stdTransport) Errors() <-chan error {
	return tp.errs
}

func (tp *stdTransport) Register(protocol transport.Protocol) error {
	if _, ok := tp.protocols.Get(protocol.Name()); ok {
		return transport.NewError(fmt.Sprintf(
			"%s protocol %p already registered",
			protocol.Name(),
			protocol,
		))
	}

	tp.Log().Debugf("registering %s protocol %p", protocol.Name(), protocol)
	protocol.SetLog(tp.Log())
	tp.protocols.Add(protocol.Name(), protocol)

	return nil
}

func (tp *stdTransport) Listen(addr string) error {
	tp.Log().Infof("begin listening all registered protocols")

	for _, protocol := range tp.protocols.All() {
		// start protocol listening
		if err := protocol.Listen(addr); err != nil {
			// return error right away to be more explicitly
			return err
		}
		// start protocol output forwarding goroutine
		tp.wg.Add(1)
		go tp.handleProtocol(protocol)
	}

	return nil
}

func (tp *stdTransport) Send(addr string, msg core.Message) error {
	// TODO implement
	return nil
}

func (tp *stdTransport) Stop() {
	tp.Log().Infof("stop transport")
	close(tp.stop)
	tp.wg.Wait()

	tp.Log().Debugf("disposing all registered protocols")
	for _, protocol := range tp.protocols.All() {
		protocol.Stop()
	}

	tp.Log().Debugf("disposing output channels")
	close(tp.output)
	close(tp.errs)
}

func (tp *stdTransport) handleProtocol(protocol transport.Protocol) {
	defer func() {
		tp.wg.Done()
		tp.Log().Debugf("stop forwarding of %s protocol %p outputs", protocol.Name(), protocol)
	}()
	tp.Log().Debugf("begin forwarding of %s protocol %p outputs", protocol.Name(), protocol)

	for {
		select {
		case <-tp.stop: // transport stop was called
			return
			// forward incoming message
		case incomingMsg := <-protocol.Output():
			tp.onProtocolMessage(incomingMsg, protocol)
			// forward errors
		case err := <-protocol.Errors():
			tp.onProtocolError(err, protocol)
		}
	}
}

// handles incoming message from protocol
// should be called inside goroutine for non-blocking forwarding
func (tp *stdTransport) onProtocolMessage(incomingMsg *transport.IncomingMessage, protocol transport.Protocol) {
	tp.Log().Debugf(
		"forwarding message '%s' %p from %s protocol %p",
		incomingMsg.Msg.Short(),
		incomingMsg.Msg,
		protocol.Name(),
		protocol,
	)

	msg := incomingMsg.Msg
	switch incomingMsg.Msg.(type) {
	// incoming Response
	case core.Response:
		// RFC 3261 - 18.1.2. - Receiving Responses.
		viaHop, ok := msg.ViaHop()
		if !ok {
			tp.Log().Warnf(
				"discarding message '%s' %p from %s to %s over %s protocol %p: empty or malformed 'Via' header",
				msg.Short(),
				msg,
				incomingMsg.RAddr,
				incomingMsg.LAddr,
				protocol.Name(),
				protocol,
			)
			return
		}

		if viaHop.Host != tp.hostname {
			tp.Log().Warnf(
				"discarding message '%s' %p from %s to %s over %s protocol %p: 'sent-by' in the first 'Via' header "+
					" equals to %s, but expected %s",
				msg.Short(),
				msg,
				incomingMsg.RAddr,
				incomingMsg.LAddr,
				protocol.Name(),
				protocol,
				viaHop.Host,
				tp.hostname,
			)
			return
		}
		// incoming Request
	case core.Request:
		// RFC 3261 - 18.2.1. - Receiving Request.
		viaHop, ok := msg.ViaHop()
		if !ok {
			tp.Log().Warnf(
				"discarding message '%s' %p from %s to %s over %s protocol %p: empty or malformed 'Via' header",
				msg.Short(),
				msg,
				incomingMsg.RAddr,
				incomingMsg.LAddr,
				protocol.Name(),
				protocol,
			)
			return
		}

		rhost, _, err := net.SplitHostPort(incomingMsg.RAddr.String())
		if err != nil {
			tp.Log().Errorf(
				"failed to extract host from remote address %s of the incoming request '%s' %p",
				incomingMsg.RAddr.String(),
				msg.Short(),
				msg,
			)
			return
		}
		if viaHop.Host != rhost {
			tp.Log().Debugf(
				"host %s from the first 'Via' header differs from the actual source address %s of the message '%s' %p: "+
					"'received' parameter will be added",
				viaHop.Host,
				rhost,
				msg.Short(),
				msg,
			)
			viaHop.Params.Add("received", core.String{rhost})
		}
	}

	// pass up message
	select {
	case tp.output <- msg:
	case <-tp.stop:
		return
	}
}

// handles protocol errors
// should be called inside goroutine for non-blocking forwarding
func (tp *stdTransport) onProtocolError(err error, protocol transport.Protocol) {
	tp.Log().Debugf(
		"forwarding error '%s' from %s protocol %p",
		err,
		protocol.Name(),
		protocol,
	)

	if err, ok := err.(*transport.Error); !ok {
		err = transport.NewError(err.Error())
	}

	// pass up error
	select {
	case tp.errs <- err:
	case <-tp.stop:
		return
	}
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
