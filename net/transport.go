// net package implements SIP transport layer.
package net

import (
	"fmt"
	"time"

	"sync"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/lex"
	"github.com/ghettovoice/gosip/log"
)

const (
	bufferSize         uint16 = 65535
	listenersQueueSize uint16 = 1000
	socketExpiry              = time.Hour
)

// Common transport layer error.
type Error struct {
	msg string
}

func NewError(msg string) *Error {
	return &Error{msg}
}

func (err *Error) Error() string {
	return err.msg
}

func (err *Error) String() string {
	return err.Error()
}

// Transport layer is responsible for the actual transmission of messages - RFC 3261 - 18.
type Transport interface {
	log.WithLogger
	core.MessageTunnel
	// Registers new transport protocol.
	Register(protocol Protocol) error
	// Listen starts listening on `addr` for each registered protocol.
	Listen(addr string) error
	// Send sends message on suitable protocol.
	Send(addr string, msg core.Message) error
	// Stop listening for incoming messages.
	Stop()
}

// Transport layer implementation.
type transport struct {
	protocolsStore
	log    log.Logger
	output chan core.Message
	errs   chan error
	lock   *sync.RWMutex
}

func NewTransport(output chan core.Message, errs chan error, protocols []Protocol, logger log.Logger) *transport {
	tp := new(transport)
	tp.protocols = make(map[string]Protocol)
	tp.SetLog(logger)
	tp.SetOutput(output)
	tp.SetErrors(errs)

	for _, protocol := range protocols {
		tp.Register(protocol)
	}

	return tp
}

func (tp *transport) Log() log.Logger {
	return tp.log
}

func (tp *transport) SetLog(logger log.Logger) {
	tp.log = logger
}

func (tp *transport) SetOutput(output chan core.Message) {
	tp.output = output
}

func (tp *transport) Output() <-chan core.Message {
	return tp.output
}

func (tp *transport) SetErrors(errs chan error) {
	tp.errs = errs
}

func (tp *transport) Errors() <-chan error {
	return tp.errs
}

func (tp *transport) Register(protocol Protocol) error {
	if _, ok := tp.getProtocol(protocol.Name()); ok {
		return NewError(fmt.Sprintf("protocol '%s' already registered", protocol.Name()))
	}

	output := make(chan []byte)
	errs := make(chan error)
	protocol.SetOutput(output)
	protocol.SetErrors(errs)
	tp.addProtocol(protocol.Name(), protocol)

	return nil
}

func (tp *transport) Listen(addr string) error {
	tp.Log().Debugf("starting listening on each registered protocol...")

	for _, protocol := range tp.Protocols() {
		go func() {
			err := protocol.Listen(addr)
			if err != nil {
				tp.errs <- NewError(err.Error())
			}
		}()
		// forward outputs
		go func() {
			for {
				select {
				case err := <-protocol.Errors():
					tp.errs <- NewError(err.Error())
				case data := <-protocol.Output():
					if msg, err := lex.ParseMessage(data, tp.Log()); err == nil {
						tp.output <- msg
					} else {
						err := NewError(fmt.Sprintf("failed to parse message: %s", err))
						protocol.Log().Warn(err)
						tp.errs <- err
					}
				}
			}
		}()
	}
}

func (tp *transport) Stop() {
	for _, protocol := range tp.protocols {
		protocol.Stop()
	}
}

// helper struct for thread-safe protocols storing
type protocolsStore struct {
	protocolsLock sync.RWMutex
	protocols     map[string]Protocol
}

func (prs *protocolsStore) addProtocol(key string, protocol Protocol) {
	prs.protocolsLock.Lock()
	prs.protocols[key] = protocol
	prs.protocolsLock.Unlock()
}

func (prs *protocolsStore) getProtocol(key string) (Protocol, bool) {
	prs.protocolsLock.RLock()
	defer prs.protocolsLock.RUnlock()
	protocol, ok := prs.protocols[key]
	return protocol, ok
}

func (prs *protocolsStore) Protocols() []Protocol {
	all := make([]Protocol, 0)

	for key := range prs.protocols {
		if protocol, ok := prs.getProtocol(key); ok {
			all = append(all, protocol)
		}
	}

	return all
}
