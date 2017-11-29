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
	// Listen starts listening on `addr` for each registered protocol.
	Listen(target *transport.Target) error
	// Send sends message on suitable protocol.
	Send(target *transport.Target, msg core.Message) error
	// Stops all protocols
	Stop()
}

// Transport layer implementation.
type stdTransport struct {
	protocols *protocolsPool
	log       log.Logger
	hostaddr  string
	output    chan core.Message
	errs      chan error
	stop      chan bool
	wg        *sync.WaitGroup
}

// NewTransport creates transport layer.
// 	- hostaddr - current server host address (IP or FQDN)
func NewTransport(
	hostaddr string,
) *stdTransport {
	tp := &stdTransport{
		hostaddr:  hostaddr,
		output:    make(chan core.Message),
		errs:      make(chan error),
		stop:      make(chan bool),
		wg:        new(sync.WaitGroup),
		protocols: NewProtocolsPool(),
	}
	tp.SetLog(log.StandardLogger())

	// protocols registering
	udp := transport.NewUdpProtocol()
	tp.protocols.Add(udp.Network(), udp)

	tcp := transport.NewTcpProtocol()
	tp.protocols.Add(tcp.Network(), tcp)
	// TODO implement TLS

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

func (tp *stdTransport) Listen(target *transport.Target) error {
	protocol, ok := tp.protocols.Get(target.Protocol)
	if !ok {
		return &transport.Error{
			Txt:      fmt.Sprintf("unknown transport protocol %s", target.Protocol),
			Protocol: fmt.Sprintf("%s protocol", target.Protocol),
			LAddr:    target.Addr(),
		}
	}

	target = transport.FillTargetHostAndPort(target.Protocol, target)
	tp.Log().Infof("begin listening on %s", target)

	if err := protocol.Listen(target); err != nil {
		// return error right away to be more explicitly
		return err
	}

	protocol.SetLog(tp.Log())
	tp.protocols.Add(protocol.Network(), protocol)
	// start protocol output forwarding goroutine
	tp.wg.Add(1)
	go tp.serveProtocol(protocol, tp.wg)

	return nil
}

func (tp *stdTransport) Send(target *transport.Target, msg core.Message) error {
	nets := make([]string, 0)

	viaHop, ok := msg.ViaHop()
	if !ok {
		return &core.MalformedMessageError{
			Txt: "missing 'Via' header",
			Msg: msg,
		}
	}

	switch msg := msg.(type) {
	// RFC 3261 - 18.1.1.
	case core.Request:
		msgLen := len(msg.String())
		// rewrite sent-by host
		viaHop.Host = tp.hostaddr

		if viaHop.Transport == "UDP" && msgLen > int(transport.MTU)-200 {
			nets = append(nets, "TCP", viaHop.Transport)
		} else {
			nets = append(nets, viaHop.Transport)
		}

		var err error
		for _, nt := range nets {
			protocol, ok := tp.protocols.Get(nt)
			if !ok {
				err = &transport.Error{
					Txt:      fmt.Sprintf("unknown transport protocol %s", target.Protocol),
					Protocol: fmt.Sprintf("%s protocol", nt),
					RAddr:    target.Addr(),
				}
				continue
			}
			// rewrite sent-by transport
			viaHop.Transport = nt
			// rewrite sent-by port
			defPort := transport.DefaultPort(nt)
			if viaHop.Port == nil {
				viaHop.Port = &defPort
			}
			err = protocol.Send(target, msg)
			if err != nil {
				break
			}
		}

		return err
	// RFC 3261 - 18.2.2.
	case core.Response:
		// resolve protocol from Via
		protocol, ok := tp.protocols.Get(viaHop.Transport)
		if !ok {
			return &transport.Error{
				Txt:      fmt.Sprintf("unknown transport protocol %s", target.Protocol),
				Protocol: fmt.Sprintf("%s protocol", viaHop.Transport),
				RAddr:    target.Addr(),
			}
		}
		// override target with values from Response headers
		// resolve host, port from Via
		target = new(transport.Target)
		if received, ok := viaHop.Params.Get("received"); ok {
			target.Host = received.String()
		} else {
			target.Host = viaHop.Host
		}

		return protocol.Send(target, msg)
	default:
		return &transport.Error{
			Txt: fmt.Sprintf(
				"failed to send unknown message '%s' %p",
				msg.Short(),
				msg,
			),
		}
	}
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

func (tp *stdTransport) serveProtocol(protocol transport.Protocol, wg *sync.WaitGroup) {
	defer func() {
		wg.Done()
		tp.Log().Infof("stop forwarding of %s protocol %p outputs", protocol.Network(), protocol)
	}()
	tp.Log().Infof("begin forwarding of %s protocol %p outputs", protocol.Network(), protocol)

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
	tp.Log().Infof(
		"received message '%s' %p from %s",
		incomingMsg.Msg.Short(),
		incomingMsg.Msg,
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
				"discarding malformed response '%s' %p from %s to %s over %s: empty or malformed 'Via' header",
				msg.Short(),
				msg,
				incomingMsg.RAddr,
				incomingMsg.LAddr,
				protocol,
			)
			return
		}

		if viaHop.Host != tp.hostaddr {
			tp.Log().Warnf(
				"discarding unexpected response '%s' %p from %s to %s over %s: 'sent-by' in the first 'Via' header "+
					" equals to %s, but expected %s",
				msg.Short(),
				msg,
				incomingMsg.RAddr,
				incomingMsg.LAddr,
				protocol,
				viaHop.Host,
				tp.hostaddr,
			)
			return
		}
		// incoming Request
	case core.Request:
		// RFC 3261 - 18.2.1. - Receiving Request.
		viaHop, ok := msg.ViaHop()
		if !ok {
			// pass up errors on malformed requests, UA may response on it with 4xx code
			err := &core.MalformedMessageError{
				Txt: fmt.Sprintf(
					"malformed request '%s' %p from %s to %s over %s: empty or malformed 'Via' header",
					msg.Short(),
					msg,
					incomingMsg.RAddr,
					incomingMsg.LAddr,
					protocol,
				),
				Msg: msg,
			}

			select {
			case tp.errs <- err:
			case <-tp.stop:
			}
			return
		}

		rhost, _, err := net.SplitHostPort(incomingMsg.RAddr.String())
		if err != nil {
			err = &transport.Error{
				Txt: fmt.Sprintf(
					"failed to extract host from remote address %s of the incoming request '%s' %p",
					incomingMsg.RAddr.String(),
					msg.Short(),
					msg,
				),
				Protocol: protocol.String(),
				LAddr:    incomingMsg.LAddr.String(),
				RAddr:    incomingMsg.RAddr.String(),
			}
			select {
			case tp.errs <- err:
			case <-tp.stop:
			}
			return
		}
		if viaHop.Host != rhost {
			tp.Log().Infof(
				"host %s from the first 'Via' header differs from the actual source address %s of the message '%s' %p: "+
					"'received' parameter will be added",
				viaHop.Host,
				rhost,
				msg.Short(),
				msg,
			)
			viaHop.Params.Add("received", core.String{rhost})
		}
	default:
		// unknown message received, log and discard
		tp.Log().Warnf(
			"received unknown message '%s' %p from %s to %s over %s",
			msg.Short(),
			msg,
			incomingMsg.RAddr,
			incomingMsg.LAddr,
			protocol,
		)
		return
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
	tp.Log().Warnf(
		"received error '%s' from %s",
		err,
		protocol,
	)

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
