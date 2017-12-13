package transport

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/log"
)

var (
	bufferSize   uint16 = 65535 - 20 - 8 // IPv4 max size - IPv4 Header size - UDP Header size
	readTimeout         = 30 * time.Second
	writeTimeout        = 30 * time.Second
)

// Wrapper around net.Conn.
type Connection interface {
	net.Conn
	log.LocalLogger
	Network() string
	Streamed() bool
	String() string
}

// Connection implementation.
type connection struct {
	logger   log.LocalLogger
	baseConn net.Conn
	laddr    net.Addr
	raddr    net.Addr
	streamed bool
	mu       *sync.RWMutex
}

func NewConnection(
	baseConn net.Conn,
) Connection {
	var stream bool
	switch baseConn.(type) {
	case net.PacketConn:
		stream = false
	default:
		stream = true
	}

	conn := &connection{
		logger:   log.NewSafeLocalLogger(),
		baseConn: baseConn,
		laddr:    baseConn.LocalAddr(),
		raddr:    baseConn.RemoteAddr(),
		streamed: stream,
		mu:       new(sync.RWMutex),
	}
	return conn
}

func (conn *connection) String() string {
	var name, network string
	if conn == nil {
		name = "<nil>"
		network = ""
	} else {
		name = fmt.Sprintf("%p", conn)
		network = conn.Network() + " "
	}

	return fmt.Sprintf(
		"%sconnection %s (laddr %v, raddr %v)",
		network,
		name,
		conn.LocalAddr(),
		conn.RemoteAddr(),
	)
}

func (conn *connection) Log() log.Logger {
	// remote addr for net.PacketConn resolved in runtime
	return conn.logger.Log().WithFields(map[string]interface{}{
		"conn":  conn.String(),
		"raddr": fmt.Sprintf("%v", conn.RemoteAddr()),
	})
}

func (conn *connection) SetLog(logger log.Logger) {
	conn.logger.SetLog(logger.WithFields(map[string]interface{}{
		"laddr": fmt.Sprintf("%v", conn.LocalAddr()),
		"net":   strings.ToUpper(conn.LocalAddr().Network()),
	}))
}

func (conn *connection) Streamed() bool {
	return conn.streamed
}

func (conn *connection) Network() string {
	return strings.ToUpper(conn.baseConn.LocalAddr().Network())
}

func (conn *connection) Read(buf []byte) (int, error) {
	var (
		num   int
		err   error
		raddr net.Addr
	)

	if err := conn.baseConn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		conn.Log().Warnf("%s failed to set read deadline: %s", conn, err)
	}

	switch baseConn := conn.baseConn.(type) {
	case net.PacketConn: // UDP & ...
		num, raddr, err = baseConn.ReadFrom(buf)
		conn.mu.Lock()
		conn.raddr = raddr
		conn.mu.Unlock()
	default: // net.Conn - TCP, TLS & ...
		num, err = conn.baseConn.Read(buf)
	}

	if err != nil {
		return num, &ConnectionError{
			err,
			"read",
			fmt.Sprintf("%v", conn.RemoteAddr()),
			fmt.Sprintf("%v", conn.LocalAddr()),
			conn.String(),
		}
	}

	conn.Log().Debugf(
		"received %d bytes from %s",
		num,
		conn,
	)

	return num, err
}

func (conn *connection) Write(buf []byte) (int, error) {
	var (
		num int
		err error
	)

	if err := conn.baseConn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		conn.Log().Warnf("%s failed to set write deadline: %s", conn, err)
	}

	num, err = conn.baseConn.Write(buf)
	if err != nil {
		return num, &ConnectionError{
			err,
			"write",
			fmt.Sprintf("%v", conn.RemoteAddr()),
			fmt.Sprintf("%v", conn.LocalAddr()),
			conn.String(),
		}
	}

	conn.Log().Debugf(
		"written %d bytes to %s",
		num,
		conn,
	)

	return num, err
}

func (conn *connection) LocalAddr() net.Addr {
	return conn.laddr
}

func (conn *connection) RemoteAddr() net.Addr {
	// we should protect raddr field with mutex,
	// because there is may be DATA RACE with Read method that usually executes
	// in another goroutine
	conn.mu.RLock()
	defer conn.mu.RUnlock()
	return conn.raddr
}

func (conn *connection) Close() error {
	err := conn.baseConn.Close()
	if err != nil {
		return &ConnectionError{
			err,
			"close",
			"",
			"",
			conn.String(),
		}
	}

	conn.Log().Debugf(
		"%s closed",
		conn,
	)

	return nil
}

func (conn *connection) SetDeadline(t time.Time) error {
	return conn.baseConn.SetDeadline(t)
}

func (conn *connection) SetReadDeadline(t time.Time) error {
	return conn.baseConn.SetReadDeadline(t)
}

func (conn *connection) SetWriteDeadline(t time.Time) error {
	return conn.baseConn.SetWriteDeadline(t)
}
