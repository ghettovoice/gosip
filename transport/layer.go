package transport

import (
	"fmt"
	"strings"
	"sync"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
)

// TransportLayer layer is responsible for the actual transmission of messages - RFC 3261 - 18.
type Layer interface {
	log.LocalLogger
	Cancel()
	Done() <-chan struct{}
	HostAddr() string
	Messages() <-chan sip.Message
	Errors() <-chan error
	// Listen starts listening on `addr` for each registered protocol.
	Listen(network string, addr string) error
	// Send sends message on suitable protocol.
	Send(msg sip.Message) error
	String() string
	IsReliable(network string) bool
}

var protocolFactory ProtocolFactory = func(
	network string,
	output chan<- sip.Message,
	errs chan<- error,
	cancel <-chan struct{},
) (Protocol, error) {
	switch strings.ToLower(network) {
	case "udp":
		return NewUdpProtocol(output, errs, cancel), nil
	case "tcp":
		return NewTcpProtocol(output, errs, cancel), nil
	default:
		return nil, UnsupportedProtocolError(fmt.Sprintf("protocol %s is not supported", network))
	}
}

// SetProtocolFactory replaces default protocol factory
func SetProtocolFactory(factory ProtocolFactory) {
	protocolFactory = factory
}

// ProtocolFactory returns default protocol factory
func GetProtocolFactory() ProtocolFactory {
	return protocolFactory
}

// TransportLayer implementation.
type layer struct {
	logger    log.LocalLogger
	hostAddr  string
	protocols *protocolStore
	msgs      chan sip.Message
	errs      chan error
	pmsgs     chan sip.Message
	perrs     chan error
	canceled  chan struct{}
	done      chan struct{}
	wg        *sync.WaitGroup
}

// NewLayer creates transport layer.
// 	- hostAddr - current server host address (IP or FQDN)
func NewLayer(hostAddr string) Layer {
	tpl := &layer{
		logger:    log.NewSafeLocalLogger(),
		hostAddr:  hostAddr,
		wg:        new(sync.WaitGroup),
		protocols: newProtocolStore(),
		msgs:      make(chan sip.Message),
		errs:      make(chan error),
		pmsgs:     make(chan sip.Message),
		perrs:     make(chan error),
		canceled:  make(chan struct{}),
		done:      make(chan struct{}),
	}
	go tpl.serveProtocols()
	return tpl
}

func (tpl *layer) String() string {
	var addr string
	if tpl == nil {
		addr = "<nil>"
	} else {
		addr = fmt.Sprintf("%p", tpl)
	}

	return fmt.Sprintf("TransportLayer %s", addr)
}

func (tpl *layer) Log() log.Logger {
	return tpl.logger.Log()
}

func (tpl *layer) SetLog(logger log.Logger) {
	tpl.logger.SetLog(logger.WithFields(map[string]interface{}{
		"tp-layer": tpl.String(),
	}))
	for _, protocol := range tpl.protocols.all() {
		protocol.SetLog(tpl.Log())
	}
}

func (tpl *layer) HostAddr() string {
	return tpl.hostAddr
}

func (tpl *layer) Cancel() {
	select {
	case <-tpl.canceled:
	default:
		close(tpl.canceled)
	}
}

func (tpl *layer) Done() <-chan struct{} {
	return tpl.done
}

func (tpl *layer) Messages() <-chan sip.Message {
	return tpl.msgs
}

func (tpl *layer) Errors() <-chan error {
	return tpl.errs
}

func (tpl *layer) IsReliable(network string) bool {
	if protocol, ok := tpl.protocols.get(protocolKey(network)); ok && protocol.Reliable() {
		return true
	}
	return false
}

func (tpl *layer) Listen(network string, addr string) error {
	// todo try with separate goroutine/outputs for each protocol
	protocol, ok := tpl.protocols.get(protocolKey(network))
	if !ok {
		var err error
		protocol, err = protocolFactory(network, tpl.pmsgs, tpl.perrs, tpl.canceled)
		if err != nil {
			return err
		}
		tpl.protocols.put(protocolKey(protocol.Network()), protocol)
	}
	target, err := NewTargetFromAddr(addr)
	if err != nil {
		return err
	}
	target = FillTargetHostAndPort(network, target)
	return protocol.Listen(target)
}

func (tpl *layer) Send(msg sip.Message) error {
	nets := make([]string, 0)

	viaHop, ok := msg.ViaHop()
	if !ok {
		return &sip.MalformedMessageError{
			Err: fmt.Errorf("missing required 'Via' header"),
			Msg: msg.String(),
		}
	}

	switch msg := msg.(type) {
	// RFC 3261 - 18.1.1.
	case sip.Request:
		msgLen := len(msg.String())
		// rewrite sent-by host
		viaHop.Host = tpl.HostAddr()
		// todo check for reliable/non-reliable
		if msgLen > int(MTU)-200 {
			nets = append(nets, "TCP", "UDP")
		} else {
			nets = append(nets, "UDP")
		}

		var err error
		for _, nt := range nets {
			protocol, ok := tpl.protocols.get(protocolKey(nt))
			if !ok {
				err = UnsupportedProtocolError(fmt.Sprintf("protocol %s is not supported", nt))
				continue
			}
			// rewrite sent-by transport
			viaHop.Transport = nt
			// rewrite sent-by port
			defPort := DefaultPort(nt)
			if viaHop.Port == nil {
				viaHop.Port = &defPort
			}

			target, err := NewTargetFromAddr(msg.Destination())
			if err != nil {
				return err
			}

			err = protocol.Send(target, msg)
			if err == nil {
				break
			}
		}

		return err
		// RFC 3261 - 18.2.2.
	case sip.Response:
		// resolve protocol from Via
		protocol, ok := tpl.protocols.get(protocolKey(viaHop.Transport))
		if !ok {
			return UnsupportedProtocolError(fmt.Sprintf("protocol %s is not supported", viaHop.Transport))
		}

		target, err := NewTargetFromAddr(msg.Destination())
		if err != nil {
			return err
		}

		return protocol.Send(target, msg)
	default:
		return &sip.UnsupportedMessageError{
			fmt.Errorf("unsupported message %s", msg.Short()),
			msg.String(),
		}
	}
}

func (tpl *layer) serveProtocols() {
	defer func() {
		tpl.Log().Infof("%s stops serves protocols", tpl)
		tpl.dispose()
		close(tpl.done)
	}()
	tpl.Log().Infof("%s begins serve protocols", tpl)

	for {
		select {
		case <-tpl.canceled:
			tpl.Log().Warnf("%s received cancel signal", tpl)
			return
		case msg := <-tpl.pmsgs:
			tpl.handleMessage(msg)
		case err := <-tpl.perrs:
			tpl.handlerError(err)
		}
	}
}

func (tpl *layer) dispose() {
	tpl.Log().Debugf("%s disposing...", tpl)
	// wait for protocols
	protocols := tpl.protocols.all()
	wg := new(sync.WaitGroup)
	wg.Add(len(protocols))
	for _, protocol := range protocols {
		tpl.protocols.drop(protocolKey(protocol.Network()))
		go func(wg *sync.WaitGroup, protocol Protocol) {
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
func (tpl *layer) handleMessage(msg sip.Message) {
	tpl.Log().Debugf("%s received %s\r\n%s", tpl, msg.Short(), msg)

	switch msg.(type) {
	case sip.Response:
		// incoming Response
		// RFC 3261 - 18.1.2. - Receiving Responses.
		viaHop, ok := msg.ViaHop()
		if !ok {
			tpl.Log().Warnf("%s received response without Via header %s", tpl, msg.Short())

			return
		}

		if viaHop.Host != tpl.HostAddr() {
			tpl.Log().Warnf(
				"%s discards unexpected response %s %s -> %s over %s: 'sent-by' in the first 'Via' header "+
					" equals to %s, but expected %s",
				tpl,
				msg.Short(),
				msg.Source(),
				msg.Destination(),
				msg.Transport(),
				viaHop.Host,
				tpl.HostAddr(),
			)
			return
		}
	case sip.Request:
		// incoming Request
		// RFC 3261 - 18.2.1. - Receiving Request. already done in ConnectionHandler
	default:
		// unsupported message received, log and discard
		tpl.Log().Warnf(
			"%s discards unsupported message %s %s -> %s over %s",
			tpl,
			msg.Short(),
			msg.Source(),
			msg.Destination(),
			msg.Transport(),
		)
		return
	}

	tpl.Log().Debugf("%s passes up %s", tpl, msg.Short())
	// pass up message
	tpl.msgs <- msg
}

func (tpl *layer) handlerError(err error) {
	tpl.Log().Debugf("%s received %s", tpl, err)
	// TODO: implement re-connection strategy for listeners
	if err, ok := err.(Error); ok {
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
	protocols map[protocolKey]Protocol
}

func newProtocolStore() *protocolStore {
	return &protocolStore{
		mu:        new(sync.RWMutex),
		protocols: make(map[protocolKey]Protocol),
	}
}

func (store *protocolStore) put(key protocolKey, protocol Protocol) {
	store.mu.Lock()
	store.protocols[key] = protocol
	store.mu.Unlock()
}

func (store *protocolStore) get(key protocolKey) (Protocol, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	protocol, ok := store.protocols[key]
	return protocol, ok
}

func (store *protocolStore) drop(key protocolKey) bool {
	if _, ok := store.get(key); !ok {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.protocols, key)
	return true
}

func (store *protocolStore) all() []Protocol {
	all := make([]Protocol, 0)
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, protocol := range store.protocols {
		all = append(all, protocol)
	}

	return all
}
