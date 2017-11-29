package transport

import (
	"fmt"
	"net"
	"strings"

	"github.com/ghettovoice/gosip/core"
)

const (
	bufferSize uint16 = 65535 - 20 - 8 // IPv4 max size - IPv4 Header size - UDP Header size

	MTU uint = 1500

	DefaultHost               = "0.0.0.0"
	DefaultProtocol           = "TCP"
	DefaultUdpPort  core.Port = 5060
	DefaultTcpPort  core.Port = 5060
	DefaultTlsPort  core.Port = 5061
)

// Transport error
type Error struct {
	Txt        string
	Protocol   string
	Connection string
	// Local address to which message arrived
	LAddr string
	// Remote address from which message arrived
	RAddr string
}

func (err *Error) Error() string {
	return fmt.Sprintf(
		"transport error: %s, protocol: %s, connection: %s, laddr: %s, raddr: %s",
		err.Txt,
		err.Protocol,
		err.Connection,
		err.LAddr,
		err.RAddr,
	)
}

// Incoming message with meta info: remote addr, local addr & etc.
type IncomingMessage struct {
	// SIP message
	Msg core.Message
	// Local address to which message arrived
	LAddr net.Addr
	// Remote address from which message arrived
	RAddr net.Addr
}

// Target endpoint
type Target struct {
	Host     string
	Port     *core.Port
	Protocol string
}

func (trg *Target) Addr() string {
	var (
		host string
		port core.Port
	)

	if strings.TrimSpace(trg.Host) != "" {
		host = trg.Host
	} else {
		host = DefaultHost
	}

	if trg.Port != nil {
		port = *trg.Port
	} else {
		port = DefaultPort(trg.Protocol)
	}

	return fmt.Sprintf("%v:%v", host, port)
}

func (trg *Target) String() string {
	var prc string
	if strings.TrimSpace(trg.Protocol) != "" {
		prc = trg.Protocol
	} else {
		prc = DefaultProtocol
	}

	return fmt.Sprintf("%s %s", prc, trg.Addr())
}

// DefaultPort returns protocol default port by network.
func DefaultPort(protocol string) core.Port {
	switch strings.ToLower(protocol) {
	case "tls":
		return DefaultTlsPort
	case "tcp":
		return DefaultTcpPort
	case "udp":
		return DefaultUdpPort
	default:
		return DefaultTcpPort
	}
}

// Fills endpoint target with default values.
func FillTargetHostAndPort(protocol string, target *Target) *Target {
	target.Protocol = protocol

	if strings.TrimSpace(target.Host) == "" {
		target.Host = DefaultHost
	}
	if target.Port == nil {
		p := DefaultPort(target.Protocol)
		target.Port = &p
	}

	return target
}
