package sip

import (
	"net"
	"time"

	"github.com/ghettovoice/gosip/log"
)

var (
	MTU uint16 = 1500
)

const (
	defPortUDP uint16 = 5060
	defPortTCP uint16 = 5060
	defPortTLS uint16 = 5061
	// defPortWS  uint16 = 80
	// defPortWSS uint16 = 443
)

const (
	TransportProtoUDP = "UDP"
	TransportProtoTCP = "TCP"
	TransportProtoTLS = "TLS"
	// TransportProtoWS  TransportProto = "WS"
	// TransportProtoWSS TransportProto = "WSS"
)

// TransportOptions are used to configure SIP transport.
// The zero value is a valid configuration and provides default values for all fields.
type TransportOptions struct {
	// Parser is the parser that will be used to parse incoming SIP messages.
	// Defaults to [DefaultParser].
	Parser Parser
	// SentByHost is the host name that will be used in the Via's "sent-by" field.
	// Defaults to "localhost".
	SentByHost string
	// ConnTTL is the time-to-live for connections (except for UDP listen connections).
	// Defaults to [TimeC].
	// If the value is less than 0, the TTL will be ignored, i.e., the connection will not expire.
	ConnTTL time.Duration
	Log     log.Logger
}

func (tp *TransportOptions) GetParser() Parser {
	if tp.Parser == nil {
		return defParser
	}
	return tp.Parser
}

func (tp *TransportOptions) GetSentByHost() string {
	if tp.SentByHost == "" {
		return "localhost"
	}
	return tp.SentByHost
}

func (tp *TransportOptions) GetLog() log.Logger {
	if tp.Log == nil {
		return noopLogger
	}
	return tp.Log
}

func (tp *TransportOptions) GetConnTTL() time.Duration {
	if tp.ConnTTL >= 0 && tp.ConnTTL <= TimeC {
		return TimeC
	}
	return tp.ConnTTL
}

// Transport represents a SIP transport.
type Transport interface {
	Proto() string
	IsReliable() bool
	IsStreamed() bool
	IsSecured() bool
	ListenAndServe(addr string, onMsg func(Message), opts ...any) error
	Close() error
}

type inboundRequest struct {
	*Request
	conn    *connection
	rmtAddr net.Addr
}

func (req *inboundRequest) Respond(res *Response) error {
	// TODO RFC 3261 Section 18.2.2 + RFC 3581 Section 4 + RFC 3263 Section 5.
	//   resolve ip and port, dial new connection if needed (failover).
	return req.conn.writeMsg(res, req.rmtAddr)
}
