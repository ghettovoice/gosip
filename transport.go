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

// TransportLayer layer is responsible for the actual transmission of messages - RFC 3261 - 18.
type TransportLayer interface {
	log.LocalLogger
	core.Cancellable
	core.Awaiting
	HostAddr() string
	Messages() <-chan *core.IncomingMessage
	Errors() <-chan error
	// Listen starts listening on `addr` for each registered protocol.
	Listen(network string, addr string) error
	// Send sends message on suitable protocol.
	Send(network string, addr string, msg core.Message) error
	String() string
}

var protocolFactory transport.ProtocolFactory = func(
	network string,
	output chan<- *core.IncomingMessage,
	errs chan<- error,
	cancel <-chan struct{},
) (transport.Protocol, error) {
	switch strings.ToLower(network) {
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

// TransportLayer implementation.
type transportLayer struct {
	logger    log.LocalLogger
	hostAddr  string
	protocols *protocolStore
	msgs      chan *core.IncomingMessage
	errs      chan error
	pmsgs     chan *core.IncomingMessage
	perrs     chan error
	cancel    chan struct{}
	done      chan struct{}
	wg        *sync.WaitGroup
}

// NewTransportLayer creates transport layer.
// 	- hostAddr - current server host address (IP or FQDN)
func NewTransportLayer(hostAddr string) TransportLayer {
	tpl := &transportLayer{
		logger:    log.NewSafeLocalLogger(),
		hostAddr:  hostAddr,
		wg:        new(sync.WaitGroup),
		protocols: NewProtocolStore(),
		msgs:      make(chan *core.IncomingMessage),
		errs:      make(chan error),
		pmsgs:     make(chan *core.IncomingMessage),
		perrs:     make(chan error),
		cancel:    make(chan struct{}),
		done:      make(chan struct{}),
	}
	go tpl.serveProtocols()
	return tpl
}

func (tpl *transportLayer) String() string {
	var addr string
	if tpl == nil {
		addr = "<nil>"
	} else {
		addr = fmt.Sprintf("%p", tpl)
	}

	return fmt.Sprintf("TransportLayer %s", addr)
}

func (tpl *transportLayer) Log() log.Logger {
	return tpl.logger.Log()
}

func (tpl *transportLayer) SetLog(logger log.Logger) {
	tpl.logger.SetLog(logger.WithField("tp-layer", fmt.Sprintf("%p", tpl)))
	for _, protocol := range tpl.protocols.All() {
		protocol.SetLog(tpl.Log())
	}
}

func (tpl *transportLayer) HostAddr() string {
	return tpl.hostAddr
}

func (tpl *transportLayer) Cancel() {
	select {
	case <-tpl.cancel:
	default:
		close(tpl.cancel)
	}
}

func (tpl *transportLayer) Done() <-chan struct{} {
	return tpl.done
}

func (tpl *transportLayer) Messages() <-chan *core.IncomingMessage {
	return tpl.msgs
}

func (tpl *transportLayer) Errors() <-chan error {
	return tpl.errs
}

func (tpl *transportLayer) Listen(network string, addr string) error {
	// todo try with separate goroutine/outputs for each protocol
	protocol, ok := tpl.protocols.Get(protocolKey(network))
	if !ok {
		var err error
		protocol, err = protocolFactory(network, tpl.pmsgs, tpl.perrs, tpl.cancel)
		if err != nil {
			return err
		}
		tpl.protocols.Put(protocolKey(protocol.Network()), protocol)
	}
	target, err := transport.NewTargetFromAddr(addr)
	if err != nil {
		return err
	}
	target = transport.FillTargetHostAndPort(network, target)
	return protocol.Listen(target)
}

func (tpl *transportLayer) Send(network string, addr string, msg core.Message) error {
	nets := make([]string, 0)
	target, err := transport.NewTargetFromAddr(addr)
	if err != nil {
		return err
	}

	viaHop, ok := msg.ViaHop()
	if !ok {
		return &core.MalformedMessageError{
			Err: fmt.Errorf("missing required 'Via' header"),
			Msg: msg.String(),
		}
	}

	switch msg := msg.(type) {
	// RFC 3261 - 18.1.1.
	case core.Request:
		msgLen := len(msg.String())
		// rewrite sent-by host
		viaHop.Host = tpl.HostAddr()
		// todo check for reliable/non-reliable
		if strings.ToLower(viaHop.Transport) == "udp" && msgLen > int(transport.MTU)-200 {
			nets = append(nets, transport.DefaultProtocol, viaHop.Transport)
		} else {
			nets = append(nets, viaHop.Transport)
		}

		var err error
		for _, nt := range nets {
			protocol, ok := tpl.protocols.Get(protocolKey(nt))
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
		protocol, ok := tpl.protocols.Get(protocolKey(viaHop.Transport))
		if !ok {
			return UnsupportedProtocolError(fmt.Sprintf("protocol %s is not supported", network))
		}
		// override target with values from Response headers
		// resolve host, port from Via
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

func (tpl *transportLayer) serveProtocols() {
	defer func() {
		tpl.Log().Infof("%s stops serves protocols", tpl)
		tpl.dispose()
		close(tpl.done)
	}()
	tpl.Log().Infof("%s begins serve protocols", tpl)

	for {
		select {
		case <-tpl.cancel:
			tpl.Log().Warnf("%s received cancel signal", tpl)
			return
		case msg := <-tpl.pmsgs:
			tpl.handleMessage(msg)
		case err := <-tpl.perrs:
			tpl.handlerError(err)
		}
	}
}

func (tpl *transportLayer) dispose() {
	tpl.Log().Debugf("%s disposing...")
	// wait for protocols
	protocols := tpl.protocols.All()
	wg := new(sync.WaitGroup)
	wg.Add(len(protocols))
	for _, protocol := range protocols {
		tpl.protocols.Drop(protocolKey(protocol.Network()))
		go func(wg *sync.WaitGroup, protocol transport.Protocol) {
			defer wg.Done()
			<-protocol.Done()
		}(wg, protocol)
	}
	wg.Wait()
	close(tpl.pmsgs)
	close(tpl.perrs)
	close(tpl.msgs)
	close(tpl.errs)
}

// handles incoming message from protocol
// should be called inside goroutine for non-blocking forwarding
func (tpl *transportLayer) handleMessage(incomingMsg *core.IncomingMessage) {
	tpl.Log().Debugf("%s received %s", tpl, incomingMsg)

	msg := incomingMsg.Msg
	switch incomingMsg.Msg.(type) {
	case core.Response:
		// incoming Response
		// RFC 3261 - 18.1.2. - Receiving Responses.
		viaHop, ok := msg.ViaHop()
		if !ok {
			tpl.Log().Warnf(
				"%s discards malformed response %s %s -> %s over %s: empty or malformed 'Via' header",
				tpl,
				msg.Short(),
				incomingMsg.RAddr,
				incomingMsg.LAddr,
				incomingMsg.Network,
			)
			return
		}

		if viaHop.Host != tpl.HostAddr() {
			tpl.Log().Warnf(
				"%s discards unexpected response %s %s -> %s over %s: 'sent-by' in the first 'Via' header "+
					" equals to %s, but expected %s",
				tpl,
				msg.Short(),
				incomingMsg.RAddr,
				incomingMsg.LAddr,
				incomingMsg.Network,
				viaHop.Host,
				tpl.HostAddr(),
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
				Err: fmt.Errorf("empty or malformed required 'Via' header %s", viaHop),
				Msg: msg.String(),
			}
			tpl.Log().Debugf("%s passes up %s", tpl, err)
			tpl.errs <- err
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
			tpl.Log().Debugf("%s passes up %s", tpl, err)
			tpl.errs <- err
			return
		}
		if viaHop.Host != rhost {
			tpl.Log().Debugf(
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
		tpl.Log().Warnf(
			"%s discards unsupported message %s %s -> %s over %s",
			tpl,
			msg.Short(),
			incomingMsg.RAddr,
			incomingMsg.LAddr,
			incomingMsg.Network,
		)
		return
	}

	tpl.Log().Debugf("%s passes up %s", tpl, incomingMsg)
	// pass up message
	tpl.msgs <- incomingMsg
}

func (tpl *transportLayer) handlerError(err error) {
	tpl.Log().Debugf("%s received %s", tpl, err)
	// TODO: implement re-connection strategy for listeners
	if err, ok := err.(transport.Error); ok {
		// currently log and ignore
		tpl.Log().Error(err)
		return
	}
	// core.Message errors
	tpl.Log().Debugf("%s passes up %s", tpl, err)
	tpl.errs <- err
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
