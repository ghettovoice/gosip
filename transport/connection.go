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
	readTimeout         = time.Minute
	writeTimeout        = time.Minute
)

// Wrapper around net.Conn.
type Connection interface {
	net.Conn
	log.Loggable

	Network() string
	Streamed() bool
	String() string
	ReadFrom(buf []byte) (num int, raddr net.Addr, err error)
	WriteTo(buf []byte, raddr net.Addr) (num int, err error)
}

// Connection implementation.
type connection struct {
	baseConn net.Conn
	laddr    net.Addr
	raddr    net.Addr
	streamed bool
	mu       sync.RWMutex

	log log.Logger
}

func NewConnection(baseConn net.Conn, logger log.Logger) Connection {
	var stream bool
	switch baseConn.(type) {
	case net.PacketConn:
		stream = false
	default:
		stream = true
	}

	conn := &connection{
		baseConn: baseConn,
		laddr:    baseConn.LocalAddr(),
		raddr:    baseConn.RemoteAddr(),
		streamed: stream,
	}
	conn.log = logger.
		WithPrefix("transport.Connection").
		WithFields(log.Fields{
			"connection_ptr":        fmt.Sprintf("%p", conn),
			"connection_network":    strings.ToUpper(conn.LocalAddr().Network()),
			"connection_local_addr": fmt.Sprintf("%v", conn.LocalAddr()),
		})

	return conn
}

func (conn *connection) String() string {
	if conn == nil {
		return "<nil>"
	}

	return fmt.Sprintf("transport.Connection<%s>", conn.Log().Fields())
}

func (conn *connection) Log() log.Logger {
	return conn.log.WithFields(log.Fields{
		"connection_remote_addr": fmt.Sprintf("%v", conn.RemoteAddr()),
	})
}

func (conn *connection) Streamed() bool {
	return conn.streamed
}

func (conn *connection) Network() string {
	return strings.ToUpper(conn.baseConn.LocalAddr().Network())
}

func (conn *connection) Read(buf []byte) (int, error) {
	var (
		num int
		err error
	)

	if err := conn.baseConn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		conn.Log().Warnf("set connection read deadline failed: %s", err)
	}

	num, err = conn.baseConn.Read(buf)

	if err != nil {
		return num, &ConnectionError{
			err,
			"read",
			conn.Network(),
			fmt.Sprintf("%v", conn.RemoteAddr()),
			fmt.Sprintf("%v", conn.LocalAddr()),
			conn.String(),
		}
	}

	conn.Log().Tracef("read %d bytes:\n%s", num, buf[:num])

	return num, err
}

func (conn *connection) ReadFrom(buf []byte) (num int, raddr net.Addr, err error) {
	if err := conn.baseConn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		conn.Log().Warnf("set connection read deadline failed: %s", err)
	}

	num, raddr, err = conn.baseConn.(net.PacketConn).ReadFrom(buf)
	if err != nil {
		return num, raddr, &ConnectionError{
			err,
			"read",
			conn.Network(),
			fmt.Sprintf("%v", raddr),
			fmt.Sprintf("%v", conn.LocalAddr()),
			conn.String(),
		}
	}

	conn.Log().WithFields(log.Fields{
		"connection_remote_addr": fmt.Sprintf("%v", raddr),
	}).Tracef("read %d bytes:\n%s", num, buf[:num])

	return num, raddr, err
}

func (conn *connection) Write(buf []byte) (int, error) {
	var (
		num int
		err error
	)

	if err := conn.baseConn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		conn.Log().Warnf("set connection write deadline: %s", err)
	}

	num, err = conn.baseConn.Write(buf)
	if err != nil {
		return num, &ConnectionError{
			err,
			"write",
			conn.Network(),
			fmt.Sprintf("%v", conn.LocalAddr()),
			fmt.Sprintf("%v", conn.RemoteAddr()),
			conn.String(),
		}
	}

	conn.Log().Tracef("write %d bytes:\n%s", num, buf[:num])

	return num, err
}

func (conn *connection) WriteTo(buf []byte, raddr net.Addr) (num int, err error) {
	if err := conn.baseConn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		conn.Log().Warnf("set connection write deadline: %s", err)
	}

	num, err = conn.baseConn.(net.PacketConn).WriteTo(buf, raddr)
	if err != nil {
		return num, &ConnectionError{
			err,
			"write",
			conn.Network(),
			fmt.Sprintf("%v", conn.LocalAddr()),
			fmt.Sprintf("%v", raddr),
			conn.String(),
		}
	}

	conn.Log().WithFields(log.Fields{
		"connection_remote_addr": fmt.Sprintf("%v", raddr),
	}).Tracef("write %d bytes:\n%s", num, buf[:num])

	return num, err
}

func (conn *connection) LocalAddr() net.Addr {
	return conn.baseConn.LocalAddr()
}

func (conn *connection) RemoteAddr() net.Addr {
	return conn.baseConn.RemoteAddr()
}

func (conn *connection) Close() error {
	err := conn.baseConn.Close()
	if err != nil {
		return &ConnectionError{
			err,
			"close",
			conn.Network(),
			"",
			"",
			conn.String(),
		}
	}

	conn.Log().Trace("connection closed")

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
