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

	DefaultHost     = "0.0.0.0"
	DefaultProtocol = "TCP"

	DefaultUdpPort core.Port = 5060
	DefaultTcpPort core.Port = 5060
	DefaultTlsPort core.Port = 5061
)

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

// Transport error
type Error interface {
	net.Error
	// Network indicates network level errors
	Network() bool
}

type ConnectionError struct {
	Err    error
	Op     string
	Source net.Addr
	Dest   net.Addr
	Conn   Connection
}

func (err *ConnectionError) Network() bool { return true }
func (err *ConnectionError) Timeout() bool {
	if nerr, ok := err.Err.(net.Error); ok {
		return nerr.Timeout()
	}
	terr, ok := err.Err.(Error)
	return ok && terr.Timeout()
}
func (err *ConnectionError) Temporary() bool {
	if nerr, ok := err.Err.(net.Error); ok {
		return nerr.Temporary()
	}
	terr, ok := err.Err.(Error)
	return ok && terr.Temporary()
}
func (err *ConnectionError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "ConnectionError"
	if err.Conn != nil {
		s += " " + err.Conn.String()
	}
	s += err.Op
	if err.Source != nil {
		s += " " + err.Source.String()
	}
	if err.Dest != nil {
		if err.Source != nil {
			s += "->"
		} else {
			s += " "
		}
		s += err.Dest.String()
	}

	s += ": " + err.Err.Error()

	return s
}

type ProtocolError struct {
	Err      error
	Op       string
	Protocol Protocol
}

func (err *ProtocolError) Network() bool { return true }
func (err *ProtocolError) Timeout() bool {
	if nerr, ok := err.Err.(net.Error); ok {
		return nerr.Timeout()
	}
	terr, ok := err.Err.(Error)
	return ok && terr.Timeout()
}
func (err *ProtocolError) Temporary() bool {
	if nerr, ok := err.Err.(net.Error); ok {
		return nerr.Temporary()
	}
	terr, ok := err.Err.(Error)
	return ok && terr.Temporary()
}
func (err *ProtocolError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "ProtocolError"
	if err.Protocol != nil {
		s += " " + err.Protocol.String()
	}
	s += " " + err.Op + ": " + err.Err.Error()

	return s
}

type UnsupportedProtocolError string

func (err UnsupportedProtocolError) Network() bool   { return false }
func (err UnsupportedProtocolError) Timeout() bool   { return false }
func (err UnsupportedProtocolError) Temporary() bool { return false }
func (err UnsupportedProtocolError) Error() string {
	return "UnsupportedProtocolError: " + string(err)
}
