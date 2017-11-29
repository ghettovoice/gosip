package transport

import (
	"net"
	"sync"

	"fmt"

	"strings"

	"github.com/ghettovoice/gosip/log"
	"github.com/sirupsen/logrus"
)

// Wrapper around net.Conn.
type Connection interface {
	log.WithLogger
	Read(buf []byte) (num int, err error)
	Write(buf []byte) (num int, err error)
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Network() string
	Close() error
	IsStream() bool
	String() string
}

// Connection implementation.
type connection struct {
	log      log.Logger
	baseConn net.Conn
	laddr    net.Addr
	raddr    net.Addr
	stream   bool
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
		baseConn: baseConn,
		laddr:    baseConn.LocalAddr(),
		raddr:    baseConn.RemoteAddr(),
		stream:   stream,
	}
	conn.SetLog(log.StandardLogger())
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
		"%sconnection %s (laddr %s, raddr %s)",
		network,
		name,
		conn.LocalAddr(),
		conn.RemoteAddr(),
	)
}

func (conn *connection) Log() log.Logger {
	// remote addr for net.PacketConn resolved in runtime
	return conn.log.WithField("raddr", conn.RemoteAddr().String())
}

func (conn *connection) SetLog(logger log.Logger) {
	conn.log = logger.WithFields(logrus.Fields{
		"laddr":      conn.LocalAddr().String(),
		"network":    strings.ToUpper(conn.LocalAddr().Network()),
		"connection": conn.String(),
	})
}

func (conn *connection) IsStream() bool {
	return conn.stream
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

	switch baseConn := conn.baseConn.(type) {
	case net.PacketConn: // UDP & ...
		num, raddr, err = baseConn.ReadFrom(buf)
		conn.raddr = raddr
	default: // net.Conn - TCP, TLS & ...
		num, err = conn.baseConn.Read(buf)
	}

	if err != nil {
		return num, &Error{
			Txt: fmt.Sprintf(
				"failed to read data from %s: %s",
				conn,
				err,
			),
			Connection: conn.String(),
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

	switch baseConn := conn.baseConn.(type) {
	case net.PacketConn: // UDP & ...
		num, err = baseConn.WriteTo(buf, conn.RemoteAddr())
	default: // net.Conn - TCP, TLS & ...
		num, err = conn.baseConn.Write(buf)
	}

	if err != nil {
		return num, &Error{
			Txt: fmt.Sprintf(
				"failed to write data to %s: %s",
				conn,
				err,
			),
			Connection: conn.String(),
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
	return conn.raddr
}

func (conn *connection) Close() error {
	err := conn.baseConn.Close()
	if err != nil {
		return &Error{
			Txt: fmt.Sprintf(
				"%s failed to close: %s",
				conn,
				err,
			),
			Connection: conn.String(),
		}
	}

	conn.Log().Debugf(
		"%s closed",
		conn,
	)

	return err
}

// Pool of connections.
// todo connections management: expiry & ...
type connectionsPool struct {
	lock        *sync.RWMutex
	connections map[net.Addr]Connection
}

func NewConnectionsPool() *connectionsPool {
	return &connectionsPool{
		lock:        new(sync.RWMutex),
		connections: make(map[net.Addr]Connection),
	}
}

func (pool *connectionsPool) Add(key net.Addr, conn Connection) {
	pool.lock.Lock()
	pool.connections[key] = conn
	pool.lock.Unlock()
}

func (pool *connectionsPool) Get(key net.Addr) (Connection, bool) {
	pool.lock.RLock()
	defer pool.lock.RUnlock()
	connection, ok := pool.connections[key]
	return connection, ok
}

func (pool *connectionsPool) Drop(key net.Addr) {
	pool.lock.Lock()
	delete(pool.connections, key)
	pool.lock.Unlock()
}

func (pool *connectionsPool) All() []Connection {
	all := make([]Connection, 0)
	for key := range pool.connections {
		if conn, ok := pool.Get(key); ok {
			all = append(all, conn)
		}
	}

	return all
}
