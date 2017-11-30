package transport

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

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

	conn.baseConn.SetReadDeadline(time.Now().Add(30 * time.Second))

	switch baseConn := conn.baseConn.(type) {
	case net.PacketConn: // UDP & ...
		num, raddr, err = baseConn.ReadFrom(buf)
		conn.raddr = raddr
	default: // net.Conn - TCP, TLS & ...
		num, err = conn.baseConn.Read(buf)
	}

	if err != nil {
		return num, &ConnectionError{
			err,
			"read",
			conn.RemoteAddr(),
			conn.LocalAddr(),
			conn,
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

	conn.baseConn.SetWriteDeadline(time.Now().Add(30 * time.Second))

	switch baseConn := conn.baseConn.(type) {
	case net.PacketConn: // UDP & ...
		num, err = baseConn.WriteTo(buf, conn.RemoteAddr())
	default: // net.Conn - TCP, TLS & ...
		num, err = conn.baseConn.Write(buf)
	}

	if err != nil {
		return num, &ConnectionError{
			err,
			"write",
			conn.LocalAddr(),
			conn.RemoteAddr(),
			conn,
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
		return &ConnectionError{
			err,
			"close",
			nil,
			nil,
			conn,
		}
	}

	conn.Log().Debugf(
		"%s closed",
		conn,
	)

	return nil
}

// Thread-safe connections store.
type connectionsStore struct {
	lock  *sync.RWMutex
	index map[net.Addr]Connection
}

func NewConnectionsStore() *connectionsStore {
	return &connectionsStore{
		lock:  new(sync.RWMutex),
		index: make(map[net.Addr]Connection),
	}
}

func (store *connectionsStore) Add(key net.Addr, conn Connection) {
	store.lock.Lock()
	store.index[key] = conn
	store.lock.Unlock()
}

func (store *connectionsStore) Get(key net.Addr) (Connection, bool) {
	store.lock.RLock()
	defer store.lock.RUnlock()
	connection, ok := store.index[key]
	return connection, ok
}

func (store *connectionsStore) Drop(key net.Addr) {
	store.lock.Lock()
	delete(store.index, key)
	store.lock.Unlock()
}

func (store *connectionsStore) All() []Connection {
	all := make([]Connection, 0)
	for key := range store.index {
		if conn, ok := store.Get(key); ok {
			all = append(all, conn)
		}
	}

	return all
}

// Thread-safe listeners store.
type listenersStore struct {
	lock  *sync.RWMutex
	index map[net.Addr]net.Listener
}

func NewListenersStore() *listenersStore {
	return &listenersStore{
		lock:  new(sync.RWMutex),
		index: make(map[net.Addr]net.Listener),
	}
}

func (store *listenersStore) Add(key net.Addr, listener net.Listener) {
	store.lock.Lock()
	store.index[key] = listener
	store.lock.Unlock()
}

func (store *listenersStore) Get(key net.Addr) (net.Listener, bool) {
	store.lock.RLock()
	defer store.lock.RUnlock()
	listener, ok := store.index[key]
	return listener, ok
}

func (store *listenersStore) Drop(key net.Addr) {
	store.lock.Lock()
	delete(store.index, key)
	store.lock.Unlock()
}

func (store *listenersStore) All() []net.Listener {
	all := make([]net.Listener, 0)
	for key := range store.index {
		if listener, ok := store.Get(key); ok {
			all = append(all, listener)
		}
	}

	return all
}
