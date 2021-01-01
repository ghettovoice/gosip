package transport

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
)

//TLSConfig for TLS and WSS only
type TLSConfig struct {
	TLSDomain string
	Cert      string
	Key       string
	Pass      string
}

// TransportLayer layer is responsible for the actual transmission of messages - RFC 3261 - 18.
type Layer interface {
	Cancel()
	Done() <-chan struct{}
	Messages() <-chan sip.Message
	Errors() <-chan error
	// Listen starts listening on `addr` for each registered protocol.
	Listen(network string, addr string, options *TLSConfig) error
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
	msgMapper sip.MessageMapper,
	logger log.Logger,
) (Protocol, error) {
	switch strings.ToLower(network) {
	case "udp":
		return NewUdpProtocol(output, errs, cancel, msgMapper, logger), nil
	case "tcp":
		return NewTcpProtocol(output, errs, cancel, msgMapper, logger), nil
	case "tls":
		return NewTlsProtocol(output, errs, cancel, msgMapper, logger), nil
	case "wss":
		return NewWssProtocol(output, errs, cancel, msgMapper, logger), nil
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
	protocols   *protocolStore
	listenPorts map[string]*sip.Port
	ip          net.IP
	dnsResolver *net.Resolver
	msgMapper   sip.MessageMapper
	ua          string

	msgs     chan sip.Message
	errs     chan error
	pmsgs    chan sip.Message
	perrs    chan error
	canceled chan struct{}
	done     chan struct{}

	wg         sync.WaitGroup
	cancelOnce sync.Once

	log log.Logger
}

// NewLayer creates transport layer.
// - ip - host IP
// - dnsAddr - DNS server address, default is 127.0.0.1:53
func NewLayer(
	ip net.IP,
	dnsResolver *net.Resolver,
	msgMapper sip.MessageMapper,
	logger log.Logger,
) Layer {
	tpl := &layer{
		protocols:   newProtocolStore(),
		listenPorts: make(map[string]*sip.Port),
		ip:          ip,
		dnsResolver: dnsResolver,
		msgMapper:   msgMapper,
		ua:          "GoSIP",

		msgs:     make(chan sip.Message),
		errs:     make(chan error),
		pmsgs:    make(chan sip.Message),
		perrs:    make(chan error),
		canceled: make(chan struct{}),
		done:     make(chan struct{}),
	}

	tpl.log = logger.
		WithPrefix("transport.Layer").
		WithFields(map[string]interface{}{
			"transport_layer_ptr": fmt.Sprintf("%p", tpl),
		})

	go tpl.serveProtocols()

	return tpl
}

func (tpl *layer) SetUserAgent(ua string) {
	if ua != "" {
		tpl.ua = ua
	}
}

func (tpl *layer) String() string {
	if tpl == nil {
		return "<nil>"
	}

	return fmt.Sprintf("transport.Layer<%s>", tpl.Log().Fields())
}

func (tpl *layer) Log() log.Logger {
	return tpl.log
}

func (tpl *layer) Cancel() {
	select {
	case <-tpl.canceled:
		return
	default:
	}

	tpl.cancelOnce.Do(func() {
		close(tpl.canceled)

		tpl.Log().Debug("transport layer canceled")
	})
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

func (tpl *layer) Listen(network string, addr string, tlsConfig *TLSConfig) error {
	select {
	case <-tpl.canceled:
		return fmt.Errorf("transport layer is canceled")
	default:
	}

	// todo try with separate goroutine/outputs for each protocol
	protocol, ok := tpl.protocols.get(protocolKey(network))
	if !ok {
		var err error
		protocol, err = protocolFactory(
			network,
			tpl.pmsgs,
			tpl.perrs,
			tpl.canceled,
			tpl.msgMapper,
			tpl.Log(),
		)
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
	if _, ok := tpl.listenPorts[network]; !ok {
		tpl.listenPorts[network] = target.Port
	}

	if tlsConfig != nil {
		target.TLSConfig = tlsConfig
	}

	return protocol.Listen(target)
}

func (tpl *layer) Send(msg sip.Message) error {
	select {
	case <-tpl.canceled:
		return fmt.Errorf("transport layer is canceled")
	default:
	}

	viaHop, ok := msg.ViaHop()
	if !ok {
		return &sip.MalformedMessageError{
			Err: fmt.Errorf("missing required 'Via' header"),
			Msg: msg.String(),
		}
	}

	if hdrs := msg.GetHeaders("User-Agent"); len(hdrs) == 0 {
		userAgent := sip.UserAgentHeader(tpl.ua)
		msg.AppendHeader(&userAgent)
	}

	switch msg := msg.(type) {
	// RFC 3261 - 18.1.1.
	case sip.Request:
		nets := make([]string, 0)

		nets = append(nets, strings.ToUpper(viaHop.Transport))

		msgLen := len(msg.String())
		// todo check for reliable/non-reliable
		if msgLen > int(MTU)-200 {
			nets = append(nets, "TCP", "UDP")
		} else {
			nets = append(nets, "UDP", "TCP")
		}

		viaHop.Host = tpl.ip.String()
		if viaHop.Params == nil {
			viaHop.Params = sip.NewParams()
		}
		if !viaHop.Params.Has("rport") {
			viaHop.Params.Add("rport", nil)
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
			if port, ok := tpl.listenPorts[nt]; ok {
				viaHop.Port = port
			} else {
				defPort := DefaultPort(nt)
				viaHop.Port = &defPort
			}

			var target *Target
			target, err = NewTargetFromAddr(msg.Destination())
			if err != nil {
				continue
			}

			// dns srv lookup
			if net.ParseIP(target.Host) == nil {
				ctx := context.Background()
				proto := strings.ToLower(nt)
				if _, addrs, err := tpl.dnsResolver.LookupSRV(ctx, "sip", proto, target.Host); err == nil && len(addrs) > 0 {
					addr := addrs[0]
					addrStr := fmt.Sprintf("%s:%d", addr.Target[:len(addr.Target)-1], addr.Port)
					switch nt {
					case "UDP":
						if addr, err := net.ResolveUDPAddr("udp", addrStr); err == nil {
							port := sip.Port(addr.Port)
							target.Host = addr.IP.String()
							target.Port = &port
						}
					case "TLS":
						fallthrough
					case "WS":
						fallthrough
					case "WSS":
					case "TCP":
						if addr, err := net.ResolveTCPAddr("tcp", addrStr); err == nil {
							port := sip.Port(addr.Port)
							target.Host = addr.IP.String()
							target.Port = &port
						}
					}
				}
			}

			logger := log.AddFieldsFrom(tpl.Log(), protocol, msg)
			logger.Infof("sending SIP request:\n%s", msg)

			err = protocol.Send(target, msg)
			if err == nil {
				break
			} else {
				continue
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

		logger := log.AddFieldsFrom(tpl.Log(), protocol, msg)
		logger.Infof("sending SIP response:\n%s", msg)

		return protocol.Send(target, msg)
	default:
		return &sip.UnsupportedMessageError{
			Err: fmt.Errorf("unsupported message %s", msg.Short()),
			Msg: msg.String(),
		}
	}
}

func (tpl *layer) serveProtocols() {
	defer func() {
		tpl.dispose()
		close(tpl.done)
	}()

	tpl.Log().Debug("begin serve protocols")
	defer tpl.Log().Debug("stop serve protocols")

	for {
		select {
		case <-tpl.canceled:
			return
		case msg := <-tpl.pmsgs:
			tpl.handleMessage(msg)
		case err := <-tpl.perrs:
			tpl.handlerError(err)
		}
	}
}

func (tpl *layer) dispose() {
	tpl.Log().Debug("disposing...")
	// wait for protocols
	for _, protocol := range tpl.protocols.all() {
		tpl.protocols.drop(protocolKey(protocol.Network()))
		<-protocol.Done()
	}

	tpl.listenPorts = make(map[string]*sip.Port)

	close(tpl.pmsgs)
	close(tpl.perrs)
	close(tpl.msgs)
	close(tpl.errs)
}

// handles incoming message from protocol
// should be called inside goroutine for non-blocking forwarding
func (tpl *layer) handleMessage(msg sip.Message) {
	logger := tpl.Log().
		WithFields(msg.Fields())

	logger.Infof("received SIP message:\n%s", msg)
	logger.Trace("passing up SIP message...")

	// pass up message
	select {
	case <-tpl.canceled:
	case tpl.msgs <- msg:
		logger.Trace("SIP message passed up")
	}
}

func (tpl *layer) handlerError(err error) {
	// TODO: implement re-connection strategy for listeners
	if err, ok := err.(Error); ok {
		// currently log and ignore
		tpl.Log().Errorf("SIP transport error: %s", err)

		return
	}

	logger := tpl.Log().WithFields(log.Fields{
		"sip_error": err.Error(),
	})

	logger.Trace("passing up error...")

	select {
	case <-tpl.canceled:
	case tpl.errs <- err:
		logger.Trace("error passed up")
	}
}

type protocolKey string

// Thread-safe protocols pool.
type protocolStore struct {
	protocols map[protocolKey]Protocol
	mu        sync.RWMutex
}

func newProtocolStore() *protocolStore {
	return &protocolStore{
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
