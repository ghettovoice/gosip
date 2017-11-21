package net

import (
	"net"
	"sync"
)

type Connection interface {
	Read(buf []byte) (num int, err error)
	Write(buf []byte) (num int, err error)
	LocalAddr() string
	RemoteAddr() string
	Close() error
}

type streamConnection struct {
	baseConn net.Conn
}

func (conn *streamConnection) Read(buf []byte) (num int, err error) {

}

func (conn *streamConnection) Write(buf []byte) (num int, err error) {

}

type packetConnection struct {
	baseConn   net.PacketConn
	localAddr  string
	remoteAddr string
}

func (conn *packetConnection) Read(buf []byte) (num int, err error) {

}

func (conn *packetConnection) Write(buf []byte) (num int, err error) {

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
	connections     map[string]Connection
}

func (pool *connectionsPool) AddConnection(key string, conn Connection) {
	pool.connectionsLock.Lock()
	pool.connections[key] = conn
	pool.connectionsLock.Unlock()
}

func (pool *connectionsPool) GetConnection(key string) (Connection, bool) {
	pool.connectionsLock.RLock()
	defer pool.connectionsLock.RUnlock()
	connection, ok := pool.connections[key]
	return connection, ok
}

func (pool *connectionsPool) DropConnection(key string) {
	pool.connectionsLock.Lock()
	delete(pool.connections, key)
	pool.connectionsLock.Unlock()
}

func (pool *connectionsPool) Connections() []Connection {
	all := make([]Connection, 0)
	for key := range pool.connections {
		if conn, ok := pool.GetConnection(key); ok {
			all = append(all, conn)
		}
	}

	return all
}
