package transp

import (
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/ghettovoice/gosip/core"
)

const (
	MTU uint = 1500

	DefaultHost     = "127.0.0.1"
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
	LAddr string
	// Remote address from which message arrived
	RAddr   string
	Network string
}

func (msg *IncomingMessage) String() string {
	if msg == nil {
		return "IncomingMessage <nil>"
	}
	s := "IncomingMessage " + msg.Msg.Short()
	parts := make([]string, 0)
	if msg.Network != "" {
		parts = append(parts, "net "+msg.Network)
	}
	if msg.LAddr != "" {
		parts = append(parts, "laddr "+msg.LAddr)
	}
	if msg.RAddr != "" {
		parts = append(parts, "raddr "+msg.RAddr)
	}
	if len(parts) > 0 {
		s += " (" + strings.Join(parts, ", ") + ")"
	}

	return s
}

// Target endpoint
type Target struct {
	Host string
	Port *core.Port
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
	}

	return fmt.Sprintf("%v:%v", host, port)
}

func (trg *Target) String() string {
	if trg == nil {
		return "Target <nil>"
	}
	return fmt.Sprintf("Target %s", trg.Addr())
}

func NewTarget(host string, port int) *Target {
	cport := core.Port(port)
	return &Target{host, &cport}
}

func NewTargetFromAddr(addr string) (*Target, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	iport, err := strconv.Atoi(port)
	if err != nil {
		return nil, err
	}
	return NewTarget(host, iport), nil
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
func FillTargetHostAndPort(network string, target *Target) *Target {
	if strings.TrimSpace(target.Host) == "" {
		target.Host = DefaultHost
	}
	if target.Port == nil {
		p := DefaultPort(network)
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

func isNetwork(err error) bool {
	_, ok := err.(net.Error)
	return ok || err == io.EOF || err == io.ErrClosedPipe
}
func isTimeout(err error) bool {
	e, ok := err.(net.Error)
	return ok && e.Timeout()
}
func isTemporary(err error) bool {
	e, ok := err.(net.Error)
	return ok && e.Temporary()
}
func isCanceled(err error) bool {
	e, ok := err.(core.CancelError)
	return ok && e.Canceled()
}
func isExpired(err error) bool {
	e, ok := err.(core.ExpireError)
	return ok && e.Expired()
}

// Connection level error.
type ConnectionError struct {
	Err    error
	Op     string
	Net    string
	Source string
	Dest   string
	Conn   string
}

func (err *ConnectionError) Network() bool   { return isNetwork(err.Err) }
func (err *ConnectionError) Timeout() bool   { return isTimeout(err.Err) }
func (err *ConnectionError) Temporary() bool { return isTemporary(err.Err) }
func (err *ConnectionError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "ConnectionError"
	if err.Conn != "" {
		s += " [" + err.Conn + "]"
	}
	s += " " + err.Op
	if err.Source != "" {
		s += " " + err.Source
	}
	if err.Dest != "" {
		if err.Source != "" {
			s += "->"
		} else {
			s += " "
		}
		s += err.Dest
	}

	s += ": " + err.Err.Error()

	return s
}

type ExpireError string

func (err ExpireError) Network() bool   { return false }
func (err ExpireError) Timeout() bool   { return true }
func (err ExpireError) Temporary() bool { return false }
func (err ExpireError) Canceled() bool  { return false }
func (err ExpireError) Expired() bool   { return true }
func (err ExpireError) Error() string   { return "ExpireError: " + string(err) }

// Net Protocol level error
type ProtocolError struct {
	Err      error
	Op       string
	Protocol string
}

func (err *ProtocolError) Network() bool   { return isNetwork(err.Err) }
func (err *ProtocolError) Timeout() bool   { return isTimeout(err.Err) }
func (err *ProtocolError) Temporary() bool { return isTemporary(err.Err) }
func (err *ProtocolError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "ProtocolError"
	if err.Protocol != "" {
		s += " " + err.Protocol
	}
	s += " " + err.Op + ": " + err.Err.Error()

	return s
}

type ConnectionHandlerError struct {
	Err     error
	Key     ConnectionKey
	Handler string
	Net     string
	LAddr   string
	RAddr   string
}

func (err *ConnectionHandlerError) Network() bool   { return isNetwork(err.Err) }
func (err *ConnectionHandlerError) Timeout() bool   { return isTimeout(err.Err) }
func (err *ConnectionHandlerError) Temporary() bool { return isTemporary(err.Err) }
func (err *ConnectionHandlerError) Canceled() bool  { return isCanceled(err.Err) }
func (err *ConnectionHandlerError) Expired() bool   { return isExpired(err.Err) }
func (err *ConnectionHandlerError) EOF() bool {
	if err.Err == io.EOF {
		return true
	}
	ok, _ := regexp.MatchString("(?i)eof", err.Err.Error())
	return ok
}
func (err *ConnectionHandlerError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "ConnectionHandlerError"
	if err.Handler != "" {
		s += " [" + err.Handler + "]"
	}
	parts := make([]string, 0)
	if err.Net != "" {
		parts = append(parts, "net "+err.Net)
	}
	if err.LAddr != "" {
		parts = append(parts, "laddr "+err.LAddr)
	}
	if err.RAddr != "" {
		parts = append(parts, "raddr "+err.RAddr)
	}
	if len(parts) > 0 {
		s += " (" + strings.Join(parts, ", ") + ")"
	}
	s += ": " + err.Err.Error()

	return s
}

type ListenerHandlerError struct {
	Err     error
	Key     ListenerKey
	Handler string
	Net     string
	Addr    string
}

func (err *ListenerHandlerError) Network() bool   { return isNetwork(err.Err) }
func (err *ListenerHandlerError) Timeout() bool   { return isTimeout(err.Err) }
func (err *ListenerHandlerError) Temporary() bool { return isTemporary(err.Err) }
func (err *ListenerHandlerError) Canceled() bool  { return isCanceled(err.Err) }
func (err *ListenerHandlerError) Expired() bool   { return isExpired(err.Err) }
func (err *ListenerHandlerError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "ListenerHandlerError"
	if err.Handler != "" {
		s += " [" + err.Handler + "]"
	}
	parts := make([]string, 0)
	if err.Net != "" {
		parts = append(parts, "net "+err.Net)
	}
	if err.Addr != "" {
		parts = append(parts, "laddr "+err.Addr)
	}
	if len(parts) > 0 {
		s += " (" + strings.Join(parts, ", ") + ")"
	}
	s += ": " + err.Err.Error()

	return s
}

type PoolError struct {
	Err  error
	Op   string
	Pool string
}

func (err *PoolError) Network() bool   { return isNetwork(err.Err) }
func (err *PoolError) Timeout() bool   { return isTimeout(err.Err) }
func (err *PoolError) Temporary() bool { return isTemporary(err.Err) }
func (err *PoolError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "PoolError " + err.Op
	if err.Pool != "" {
		s += " (" + err.Pool + ")"
	}
	s += ": " + err.Err.Error()

	return s
}

type UnsupportedProtocolError string

func (err UnsupportedProtocolError) Network() bool   { return false }
func (err UnsupportedProtocolError) Timeout() bool   { return false }
func (err UnsupportedProtocolError) Temporary() bool { return false }
func (err UnsupportedProtocolError) Error() string {
	return "UnsupportedProtocolError: " + string(err)
}
