// net package implements SIP transport layer.
package gosip

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/transport"
)

type UnsupportedProtocolError string

func (err UnsupportedProtocolError) Network() bool   { return false }
func (err UnsupportedProtocolError) Timeout() bool   { return false }
func (err UnsupportedProtocolError) Temporary() bool { return false }
func (err UnsupportedProtocolError) Error() string {
	return "UnsupportedProtocolError: " + string(err)
}

type AlreadyRegisteredProtocolError string

func (err AlreadyRegisteredProtocolError) Network() bool   { return false }
func (err AlreadyRegisteredProtocolError) Timeout() bool   { return false }
func (err AlreadyRegisteredProtocolError) Temporary() bool { return false }
func (err AlreadyRegisteredProtocolError) Error() string {
	return "AlreadyRegisteredProtocolError: " + string(err)
}

// Transport layer is responsible for the actual transmission of messages - RFC 3261 - 18.
type Transport interface {
	log.WithLogger
	Output() <-chan core.Message
	Errors() <-chan error
	Register(protocol transport.Protocol) error
	// Listen starts listening on `addr` for each registered protocol.
	Listen(target *transport.Target) error
	// Send sends message on suitable protocol.
	Send(target *transport.Target, msg core.Message) error
	// Stops all protocols
	Stop()
	String() string
}

var hostAddrKey = "hostAddr"

func WithHostAddr(parentCtx context.Context, hostAddr string) context.Context {
	return context.WithValue(parentCtx, hostAddrKey, hostAddr)
}

func GetHostAddr(ctx context.Context) (string, bool) {
	hostAddr, ok := ctx.Value(hostAddrKey).(string)
	return hostAddr, ok
}

// Transport layer implementation.
type stdTransport struct {
	protocols *protocolPool
	log       log.Logger
	ctx       context.Context
	output    chan<- core.Message
	errs      chan<- error
	wg        *sync.WaitGroup
}

// NewTransport creates transport layer.
// 	- hostaddr - current server host address (IP or FQDN)
func NewTransport(
	ctx context.Context,
	output chan<- core.Message,
	errs chan<- error,
) *stdTransport {
	tp := &stdTransport{
		ctx:       ctx,
		output:    output,
		errs:      errs,
		wg:        new(sync.WaitGroup),
		protocols: NewProtocolPool(),
	}
	tp.SetLog(log.StandardLogger())
	// todo tmp, fix later
	incomingMessage := make(chan *transport.IncomingMessage)
	errs := make(chan error)
	// predefined protocols
	tp.Register(transport.NewTcpProtocol(ctx, incomingMessage, errs))
	tp.Register(transport.NewUdpProtocol(ctx, incomingMessage, errs))
	// TODO implement TLS

	return tp
}

func (tp *stdTransport) Register(protocol transport.Protocol) error {
	if _, ok := tp.protocols.Get(protocolKey(protocol.Network())); ok {
		return AlreadyRegisteredProtocolError(fmt.Sprintf("%s already registered", protocol))
	}

	protocol.SetLog(tp.Log())
	tp.protocols.Add(protocolKey(protocol.Network()), protocol)

	return nil
}

func (tp *stdTransport) String() string {
	var addr string
	if tp == nil {
		addr = "<nil>"
	} else {
		addr = fmt.Sprintf("%p", tp)
	}

	return fmt.Sprintf("transport layer %s", addr)
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
	protocol, ok := tp.protocols.Get(protocolKey(target.Protocol))
	if !ok {
		return UnsupportedProtocolError(fmt.Sprintf(
			"protocol %s is not registered in %s",
			target.Protocol,
			tp,
		))
	}

	target = transport.FillTargetHostAndPort(target.Protocol, target)
	tp.Log().Infof("begin listening on %s", target)

	if err := protocol.Listen(target); err != nil {
		// return error right away to be more explicitly
		return err
	}

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
			Err: fmt.Errorf("missing 'Via' header"),
			Msg: msg,
		}
	}

	switch msg := msg.(type) {
	// RFC 3261 - 18.1.1.
	case core.Request:
		msgLen := len(msg.String())
		// rewrite sent-by host
		viaHop.Host = tp.hostaddr

		if strings.ToLower(viaHop.Transport) == "udp" && msgLen > int(transport.MTU)-200 {
			nets = append(nets, transport.DefaultProtocol, viaHop.Transport)
		} else {
			nets = append(nets, viaHop.Transport)
		}

		var err error
		for _, nt := range nets {
			protocol, ok := tp.protocols.Get(protocolKey(nt))
			if !ok {
				err = UnsupportedProtocolError(fmt.Sprintf(
					"protocol %s is not registered in %s",
					target.Protocol,
					tp,
				))
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
		protocol, ok := tp.protocols.Get(protocolKey(viaHop.Transport))
		if !ok {
			return UnsupportedProtocolError(fmt.Sprintf(
				"protocol %s is not registered in %s",
				target.Protocol,
				tp,
			))
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
		return &core.UnsupportedMessageError{
			Err: fmt.Errorf(
				"failed to send unsupported message '%s' %p",
				msg.Short(),
				msg,
			),
			Msg: msg,
		}
	}
}

func (tp *stdTransport) Stop() {
	tp.Log().Infof("stop transport")
	close(tp.stop)
	tp.wg.Wait()

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

	//for {
	//	select {
	//	case <-tp.stop: // transport stop was called
	//		return
	//		// forward incoming message
	//	case incomingMsg := <-protocol.Output():
	//		tp.onProtocolMessage(incomingMsg, protocol)
	//		// forward errors
	//	case err := <-protocol.Errors():
	//		tp.onProtocolError(err, protocol)
	//	}
	//}
}

// handles incoming message from protocol
// should be called inside goroutine for non-blocking forwarding
func (tp *stdTransport) onProtocolMessage(incomingMsg *transport.IncomingMessage, protocol transport.Protocol) {
	tp.Log().Debugf(
		"%s received message '%s' %p from %s",
		tp,
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
				Err: fmt.Errorf(
					"empty or malformed 'Via' header %s; protocol: %s",
					viaHop,
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
			err = &transport.ProtocolError{
				Err: fmt.Errorf(
					"failed to extract remote host from source address %s of the incoming request '%s' %p",
					incomingMsg.RAddr.String(),
					msg.Short(),
					msg,
				),
				Op:       "extract remote host",
				Protocol: protocol,
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
		// unsupported message received, log and discard
		tp.Log().Warnf(
			"received unsupported message '%s' %p from %s to %s over %s",
			msg.Short(),
			msg,
			incomingMsg.RAddr,
			incomingMsg.LAddr,
			protocol,
		)
		return
	}

	tp.Log().Debugf(
		"%s passing up message '%s' %p",
		tp,
		msg.Short(),
		msg,
	)
	// pass up message
	select {
	case tp.output <- msg:
	case <-tp.stop:
	}
}

// handles protocol errors
// should be called inside goroutine for non-blocking forwarding
func (tp *stdTransport) onProtocolError(err error, protocol transport.Protocol) {
	tp.Log().Debugf(
		"%s received error '%s' from %s, passing it up",
		tp,
		err,
		protocol,
	)

	// pass up error
	select {
	case tp.errs <- err:
	case <-tp.stop:
	}
}

type protocolKey string

// Thread-safe protocols pool.
type protocolPool struct {
	lock  *sync.RWMutex
	store map[protocolKey]transport.Protocol
}

func NewProtocolPool() *protocolPool {
	return &protocolPool{
		lock:  new(sync.RWMutex),
		store: make(map[protocolKey]transport.Protocol),
	}
}

func (pool *protocolPool) Add(key protocolKey, protocol transport.Protocol) {
	pool.lock.Lock()
	pool.store[key] = protocol
	pool.lock.Unlock()
}

func (pool *protocolPool) Get(key protocolKey) (transport.Protocol, bool) {
	pool.lock.RLock()
	defer pool.lock.RUnlock()
	protocol, ok := pool.store[key]
	return protocol, ok
}

func (pool *protocolPool) All() []transport.Protocol {
	all := make([]transport.Protocol, 0)
	for key := range pool.store {
		if protocol, ok := pool.Get(key); ok {
			all = append(all, protocol)
		}
	}

	return all
}
