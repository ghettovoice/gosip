package transp

import (
	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gossip/base"
	"time"
)

const (
	bufferSize         uint16 = 65535
	listenersQueueSize uint16 = 1000
	socketExpiry              = time.Hour
)

// Transport is responsible for the actual transmission of messages.
type Transport interface {
	// Listen starts new listener on provided address.
	Listen(addr string) error
	Send(addr string, msg core.Message) error
	// Stop listening for incoming messages.
	Stop()
	IsStreamed() bool
	IsReliable() bool
	// Messages returns channel with incoming SIP messages.
	Messages() <-chan core.Message
	// Errors returns channel with network/transport errors.
	Errors() <-chan error
}

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

// Common transport routines.
type transport struct {
	messages chan base.SipMessage
	errs     chan error
}
