package net

import (
	"net"
	"sync"

	"bytes"
	"github.com/ghettovoice/gosip/log"
)

type Connection interface {
	log.WithLogger
	Read(buf []byte) (num int, err error)
	Write(buf []byte) (num int, err error)
	LocalAddr() string
	RemoteAddr() string
	Close() error
}

// packetConnection wraps net.Conn
type streamConnection struct {
	baseConn net.Conn
}

func (conn *streamConnection) Read(buf []byte) (num int, err error) {

}

func (conn *streamConnection) Write(buf []byte) (num int, err error) {

}

// packetConnection wraps net.PacketConn
type packetConnection struct {
	log        log.Logger
	baseConn   net.PacketConn
	localAddr  string
	remoteAddr string
	buffer     *bytes.Buffer
}

func NewPacketConnection(
	baseConn net.PacketConn,
	localAddr string,
	remoteAddr string,
	buffer []byte,
	logger log.Logger,
) Connection {
	conn := &packetConnection{
		baseConn:   baseConn,
		localAddr:  localAddr,
		remoteAddr: remoteAddr,
		buffer:     bytes.NewBuffer(buffer),
	}
	conn.SetLog(logger)
	return conn
}

func (conn *packetConnection) Log() log.Logger {
	return conn.log
}

func (conn *packetConnection) SetLog(logger log.Logger) {
	conn.log = logger
}

func (conn *packetConnection) Read(buf []byte) (num int, err error) {
	return conn.buffer.Read(buf)
}

func (conn *packetConnection) Write(buf []byte) (num int, err error) {
	return conn.buffer.Write(buf)
}

func (conn *packetConnection) LocalAddr() string {
	return conn.localAddr
}

func (conn *packetConnection) RemoteAddr() string {
	return conn.remoteAddr
}

func (conn *packetConnection) Close() error {
	return conn.baseConn.Close()
}

// Helper struct to store opened connection
type connectionsPool struct {
	connectionsLock sync.RWMutex
	connectionsMap  map[string]Connection
}

func (pool *connectionsPool) addConnection(key string, conn Connection) {
	pool.connectionsLock.Lock()
	pool.connectionsMap[key] = conn
	pool.connectionsLock.Unlock()
}

func (pool *connectionsPool) getConnection(key string) (Connection, bool) {
	pool.connectionsLock.RLock()
	defer pool.connectionsLock.RUnlock()
	connection, ok := pool.connectionsMap[key]
	return connection, ok
}

func (pool *connectionsPool) dropConnection(key string) {
	pool.connectionsLock.Lock()
	delete(pool.connectionsMap, key)
	pool.connectionsLock.Unlock()
}

func (pool *connectionsPool) connections() []Connection {
	all := make([]Connection, 0)
	for key := range pool.connectionsMap {
		if conn, ok := pool.getConnection(key); ok {
			all = append(all, conn)
		}
	}

	return all
}
