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
	Close() error
	IsStream() bool
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

func (conn *connection) Log() log.Logger {
	// remote addr for net.PacketConn resolved in runtime
	return conn.log.WithField("raddr", conn.RemoteAddr().String())
}

func (conn *connection) SetLog(logger log.Logger) {
	conn.log = logger.WithFields(logrus.Fields{
		"laddr":    conn.LocalAddr().String(),
		"net":      strings.ToUpper(conn.LocalAddr().Network()),
		"conn-ptr": fmt.Sprintf("%p", conn),
	})
}

func (conn *connection) IsStream() bool {
	return conn.stream
}

func (conn *connection) Read(buf []byte) (num int, err error) {
	switch baseConn := conn.baseConn.(type) {
	case net.PacketConn: // UDP & ...
		num, raddr, err := baseConn.ReadFrom(buf)
		conn.raddr = raddr
		return num, err
	default: // net.Conn - TCP, TLS & ...
		return conn.baseConn.Read(buf)
	}
}

func (conn *connection) Write(buf []byte) (num int, err error) {
	switch baseConn := conn.baseConn.(type) {
	case net.PacketConn: // UDP & ...
		return baseConn.WriteTo(buf, conn.RemoteAddr())
	default: // net.Conn - TCP, TLS & ...
		return conn.baseConn.Write(buf)
	}
}

func (conn *connection) LocalAddr() net.Addr {
	return conn.laddr
}

func (conn *connection) RemoteAddr() net.Addr {
	return conn.raddr
}

func (conn *connection) Close() error {
	return conn.baseConn.Close()
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
