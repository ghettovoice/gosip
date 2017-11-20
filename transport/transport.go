package transport

import "github.com/ghettovoice/gosip/message"

type Transport interface {
	Listen(addr string) error
	Send(addr string, msg message.SipMessage) error
	Stop()
	IsStreamed() bool
	IsReliable() bool
}
