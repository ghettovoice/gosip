// net package implements SIP transport layer.
package gosip

import (
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
	log.LocalLogger
	core.Awaiting
	// Listen starts listening on `addr` for each registered protocol.
	Listen(network string, target *transport.Target) error
	// Send sends message on suitable protocol.
	Send(network string, target *transport.Target, msg core.Message) error
	String() string
}

var protocolFactory transport.ProtocolFactory = func(
	network string,
	output chan<- *transport.IncomingMessage,
	errs chan<- error,
	cancel <-chan struct{},
) (transport.Protocol, error) {
	switch network {
	case "udp":
		return transport.NewUdpProtocol(output, errs, cancel), nil
	case "tcp":
		return transport.NewTcpProtocol(output, errs, cancel), nil
	default:
		return nil, UnsupportedProtocolError(fmt.Sprintf("protocol %s is not supported", network))
	}
}

// SetProtocolFactory replaces default protocol factory
func SetProtocolFactory(factory transport.ProtocolFactory) {
	protocolFactory = factory
}

// ProtocolFactory returns default protocol factory
func ProtocolFactory() transport.ProtocolFactory {
	return protocolFactory
}

// Transport layer implementation.
type stdTransport struct {
	logger    log.LocalLogger
	hostAddr  string
	protocols *protocolStore
	output    chan<- core.Message
	errs      chan<- error
	cancel    <-chan struct{}
	done      chan struct{}
	wg        *sync.WaitGroup
	msgs      chan *transport.IncomingMessage
}

// NewTransport creates transport layer.
// 	- hostAddr - current server host address (IP or FQDN)
func NewTransport(hostAddr string, output chan<- core.Message, errs chan<- error, cancel <-chan struct{}) *stdTransport {
	tp := &stdTransport{
		logger:    log.NewSafeLocalLogger(),
		hostAddr:  hostAddr,
		output:    output,
		errs:      errs,
		wg:        new(sync.WaitGroup),
		protocols: NewProtocolStore(),
		done:      make(chan struct{}),
		msgs:      make(chan *transport.IncomingMessage),
	}
	go tp.serveProtocols()
	return tp
}

func (tp *stdTransport) String() string {
	var addr string
	if tp == nil {
		addr = "<nil>"
	} else {
		addr = fmt.Sprintf("%p", tp)
	}

	return fmt.Sprintf("Transport %s", addr)
}

func (tp *stdTransport) Log() log.Logger {
	return tp.logger.Log()
}

func (tp *stdTransport) SetLog(logger log.Logger) {
	tp.logger.SetLog(logger.WithField("transport-ptr", fmt.Sprintf("%p", tp)))
	for _, protocol := range tp.protocols.All() {
		protocol.SetLog(tp.Log())
	}
}

func (tp *stdTransport) Done() <-chan struct{} {
	return tp.done
}

func (tp *stdTransport) Listen(network string, target *transport.Target) error {
	// todo try with separate goroutine/outputs for each protocol
	protocol, ok := tp.protocols.Get(protocolKey(network))
	if !ok {
		protocol, err := protocolFactory(network, tp.msgs, tp.errs, tp.cancel)
		if err != nil {
			return err
		}
		tp.protocols.Put(protocolKey(protocol.Network()), protocol)
	}

	target = transport.FillTargetHostAndPort(network, target)
	return protocol.Listen(target)
}

func (tp *stdTransport) Send(network string, target *transport.Target, msg core.Message) error {
	nets := make([]string, 0)

	viaHop, ok := msg.ViaHop()
	if !ok {
		return &core.MalformedMessageError{
			Err: fmt.Errorf("missing 'Via' header"),
			Msg: msg.String(),
		}
	}

	switch msg := msg.(type) {
	// RFC 3261 - 18.1.1.
	case core.Request:
		msgLen := len(msg.String())
		// rewrite sent-by host
		viaHop.Host = tp.hostAddr
		// todo check for reliable/non-reliable
		if strings.ToLower(viaHop.Transport) == "udp" && msgLen > int(transport.MTU)-200 {
			nets = append(nets, transport.DefaultProtocol, viaHop.Transport)
		} else {
			nets = append(nets, viaHop.Transport)
		}

		var err error
		for _, nt := range nets {
			protocol, ok := tp.protocols.Get(protocolKey(nt))
			if !ok {
				err = UnsupportedProtocolError(fmt.Sprintf("protocol %s is not supported", network))
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
			return UnsupportedProtocolError(fmt.Sprintf("protocol %s is not supported", network))
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
			Err: fmt.Errorf("unsupported message %s", msg.Short()),
			Msg: msg.String(),
		}
	}
}

func (tp *stdTransport) serveProtocols() {
	defer func() {
		tp.Log().Infof("%s stops serves protocols", tp)
		tp.dispose()
		close(tp.done)
	}()
	tp.Log().Infof("%s begins serve protocols", tp)

	for {
		select {
		case <-tp.cancel:
			tp.Log().Warnf("%s received cancel signal", tp)
			return
		case incomingMsg := <-tp.msgs:
			tp.onIncomingMessage(incomingMsg)
		}
	}
}

func (tp *stdTransport) dispose() {
	// wait for protocols
	protocols := tp.protocols.All()
	wg := new(sync.WaitGroup)
	wg.Add(len(protocols))
	for _, protocol := range protocols {
		tp.protocols.Drop(protocolKey(protocol.Network()))
		go func(wg *sync.WaitGroup, protocol transport.Protocol) {
			defer wg.Done()
			<-protocol.Done()
		}(wg, protocol)
	}
	wg.Wait()
	close(tp.msgs)
}

// handles incoming message from protocol
// should be called inside goroutine for non-blocking forwarding
func (tp *stdTransport) onIncomingMessage(incomingMsg *transport.IncomingMessage) {
	tp.Log().Debugf("%s received %s", tp, incomingMsg)

	msg := incomingMsg.Msg
	switch incomingMsg.Msg.(type) {
	case core.Response:
		// incoming Response
		// RFC 3261 - 18.1.2. - Receiving Responses.
		viaHop, ok := msg.ViaHop()
		if !ok {
			tp.Log().Warnf(
				"%s discards malformed response %s %s -> %s over %s: empty or malformed 'Via' header",
				tp,
				msg.Short(),
				incomingMsg.RAddr,
				incomingMsg.LAddr,
				incomingMsg.Network,
			)
			return
		}

		if viaHop.Host != tp.hostAddr {
			tp.Log().Warnf(
				"%s discards unexpected response %s %s -> %s over %s: 'sent-by' in the first 'Via' header "+
					" equals to %s, but expected %s",
				tp,
				msg.Short(),
				incomingMsg.RAddr,
				incomingMsg.LAddr,
				incomingMsg.Network,
				viaHop.Host,
				tp.hostAddr,
			)
			return
		}
	case core.Request:
		// incoming Request
		// RFC 3261 - 18.2.1. - Receiving Request.
		viaHop, ok := msg.ViaHop()
		if !ok {
			// pass up errors on malformed requests, UA may response on it with 4xx code
			err := &core.MalformedMessageError{
				Err: fmt.Errorf("empty or malformed 'Via' header %s", viaHop),
				Msg: msg.String(),
			}
			tp.errs <- err
			return
		}

		rhost, _, err := net.SplitHostPort(incomingMsg.RAddr)
		if err != nil {
			err = &net.OpError{
				Err: fmt.Errorf("invalid remote address %s of the incoming request %s",
					incomingMsg.RAddr, msg.Short()),
				Op:  "extract remote host",
				Net: incomingMsg.Network,
			}
			tp.errs <- err
			return
		}
		if viaHop.Host != rhost {
			tp.Log().Debugf(
				"host %s from the first 'Via' header differs from the actual source address %s of the message %s: "+
					"'received' parameter will be added",
				viaHop.Host,
				rhost,
				msg.Short(),
			)
			viaHop.Params.Add("received", core.String{rhost})
		}
	default:
		// unsupported message received, log and discard
		tp.Log().Warnf(
			"%s received unsupported message %s %s -> %s over %s",
			tp,
			msg.Short(),
			incomingMsg.RAddr,
			incomingMsg.LAddr,
			incomingMsg.Network,
		)
		return
	}

	tp.Log().Debugf("%s passing up %s", tp, msg.Short())
	// pass up message
	tp.output <- msg
}

type protocolKey string

// Thread-safe protocols pool.
type protocolStore struct {
	mu        *sync.RWMutex
	protocols map[protocolKey]transport.Protocol
}

func NewProtocolStore() *protocolStore {
	return &protocolStore{
		mu:        new(sync.RWMutex),
		protocols: make(map[protocolKey]transport.Protocol),
	}
}

func (store *protocolStore) Put(key protocolKey, protocol transport.Protocol) {
	store.mu.Lock()
	store.protocols[key] = protocol
	store.mu.Unlock()
}

func (store *protocolStore) Get(key protocolKey) (transport.Protocol, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	protocol, ok := store.protocols[key]
	return protocol, ok
}

func (store *protocolStore) Drop(key protocolKey) bool {
	if _, ok := store.Get(key); !ok {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.protocols, key)
	return true
}

func (store *protocolStore) All() []transport.Protocol {
	all := make([]transport.Protocol, 0)
	for key := range store.protocols {
		if protocol, ok := store.Get(key); ok {
			all = append(all, protocol)
		}
	}

	return all
}
