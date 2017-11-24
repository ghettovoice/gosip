package transport

import (
	"fmt"
	"net"
	"strings"

	"github.com/ghettovoice/gosip/core"
)

const (
	bufferSize     uint16    = 65535 - 20 - 8 // IPv4 max size - IPv4 Header size - UDP Header size
	DefaultHost              = "localhost"
	DefaultUdpPort core.Port = 5060
	DefaultTcpPort core.Port = 5060
	DefaultTlsPort core.Port = 5061
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

// DefaultPort returns protocol default port by name.
func DefaultPort(protocol string) core.Port {
	switch strings.ToLower(protocol) {
	case "tls":
		return DefaultTlsPort
	case "tcp":
		return DefaultTcpPort
	case "udp":
		fallthrough
	default:
		return DefaultUdpPort
	}
}

// fills omitted host/port parts with default values according to protocol defaults.
func fillAddr(protocol string, addr string) string {
	var host, port string
	// The port starts after the last colon.
	if i := strings.LastIndexByte(addr, ':'); i < 0 {
		host = addr
		port = fmt.Sprintf("%d", DefaultPort(protocol))
	} else {
		port = addr[i+1:]
	}
	if host == "" {
		host = DefaultHost
	}
	return net.JoinHostPort(host, port)
}
