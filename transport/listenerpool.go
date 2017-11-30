package transport

import (
	"net"
	"sync"
)

type listenerKey net.Addr

// Thread-safe listeners pool.
type listenerPool struct {
	lock  *sync.RWMutex
	store map[listenerKey]net.Listener
}

func NewListenerPool() *listenerPool {
	return &listenerPool{
		lock:  new(sync.RWMutex),
		store: make(map[listenerKey]net.Listener),
	}
}

func (pool *listenerPool) Add(key listenerKey, listener net.Listener) {
	pool.lock.Lock()
	pool.store[key] = listener
	pool.lock.Unlock()
}

func (pool *listenerPool) Get(key listenerKey) (net.Listener, bool) {
	pool.lock.RLock()
	defer pool.lock.RUnlock()
	listener, ok := pool.store[key]
	return listener, ok
}

func (pool *listenerPool) Drop(key listenerKey) {
	pool.lock.Lock()
	delete(pool.store, key)
	pool.lock.Unlock()
}

func (pool *listenerPool) All() []net.Listener {
	all := make([]net.Listener, 0)
	for key := range pool.store {
		if listener, ok := pool.Get(key); ok {
			all = append(all, listener)
		}
	}

	return all
}
