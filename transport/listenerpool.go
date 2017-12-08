package transport

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
)

type ListenerKey net.Addr

type ListenerPool interface {
	log.WithLogger
	String() string
	Add(key ListenerKey, listener net.Listener) error
	Get(key ListenerKey) (net.Listener, bool)
	Drop(key ListenerKey) bool
	All() []net.Listener
	Manage()
}

type ListenerHandler interface {
	log.WithLogger
	core.Cancellable
	String() string
	Key() ListenerKey
	Listener() net.Listener
	Serve()
}

// Thread-safe listeners pool.
type listenerPool struct {
	ctx         context.Context
	log         log.Logger
	lock        *sync.RWMutex
	wg          *sync.WaitGroup
	store       map[ListenerKey]ListenerHandler
	handlerErrs chan error
	output      chan<- Connection
	errs        chan<- error
}

func NewListenerPool(ctx context.Context, output chan<- Connection, errs chan<- error) ListenerPool {
	pool := &listenerPool{
		ctx:         ctx,
		lock:        new(sync.RWMutex),
		wg:          new(sync.WaitGroup),
		store:       make(map[ListenerKey]ListenerHandler),
		handlerErrs: make(chan error),
		output:      output,
		errs:        errs,
	}
	pool.SetLog(log.StandardLogger())
	return pool
}

func (pool *listenerPool) String() string {
	var name string
	if pool == nil {
		name = "<nil>"
	} else {
		name = fmt.Sprintf("%p", pool)
	}

	return fmt.Sprintf("listener pool %s", name)
}

func (pool *listenerPool) Log() log.Logger {
	return pool.log
}

func (pool *listenerPool) SetLog(logger log.Logger) {
	pool.log = logger.WithField("listener-pool", pool.String())
}

func (pool *listenerPool) Add(key ListenerKey, listener net.Listener) error {
	if pool.ctx.Err() != nil {
		pool.Log().Warnf("%s was stopped: %s", pool, pool.ctx.Err())
		return pool.ctx.Err()
	}

	if _, ok := pool.get(key); ok {
		return fmt.Errorf("%s already has lsitener with key %s", pool, key)
	}

	ctx, cancel := context.WithCancel(pool.ctx)
	handler := NewListenerHandler(ctx, key, listener, pool.output, pool.handlerErrs, cancel)
	handler.SetLog(pool.Log())
	pool.Log().Debugf("add %s to %s", handler, pool)
	pool.add(key, handler)

	pool.wg.Add(1)
	go func() {
		defer pool.wg.Done()
		handler.Serve()
	}()

	return nil
}

func (pool *listenerPool) Get(key ListenerKey) (net.Listener, bool) {
	if handler, ok := pool.get(key); ok {
		return handler.Listener(), true
	} else {
		return nil, false
	}
}

func (pool *listenerPool) Drop(key ListenerKey) bool {
	if listener, ok := pool.get(key); ok {
		pool.Log().Debugf("drop %s from %p", listener, pool)
		pool.drop(key)
		return true
	}

	return false
}

func (pool *listenerPool) All() []net.Listener {
	all := make([]net.Listener, 0)
	for _, handler := range pool.all() {
		all = append(all, handler.Listener())
	}

	return all
}

func (pool *listenerPool) Manage() {
	defer func() {
		pool.Log().Infof("%s stop managing", pool)
		pool.dispose()
	}()
	pool.Log().Infof("%s start managing", pool)

	for {
		select {
		case <-pool.ctx.Done():
			return
		case err := <-pool.handlerErrs:
			if err == nil {
				continue
			}

			var handler ListenerHandler
			shouldDrop := false
			// catch non-recoverable errors (like Network errors) and drop out handler from pool
			if err, ok := err.(Error); ok && err.Network() && !err.Temporary() && !err.Timeout() {
				shouldDrop = true
			}
			if err, ok := err.(net.Error); ok && !err.Temporary() && !err.Timeout() {
				shouldDrop = true
			}

			if err, ok := err.(*ListenerHandlerError); ok {
				handler = err.Handler
			}

			if shouldDrop && handler != nil {
				pool.Drop(handler.Key())
			}

			// pass up
			select {
			case <-pool.ctx.Done():
				return
			case pool.errs <- err:
			}
		}
	}
}

func (pool *listenerPool) dispose() {
	pool.Log().Debugf("dispose %s", pool)
	for _, handler := range pool.all() {
		pool.Drop(handler.Key())
	}
	pool.wg.Wait()
}

func (pool *listenerPool) add(key ListenerKey, handler ListenerHandler) {
	pool.lock.Lock()
	pool.store[key] = handler
	pool.lock.Unlock()
}

func (pool *listenerPool) get(key ListenerKey) (ListenerHandler, bool) {
	pool.lock.RLock()
	defer pool.lock.RUnlock()
	handler, ok := pool.store[key]
	return handler, ok
}

func (pool *listenerPool) drop(key ListenerKey) {
	pool.lock.Lock()
	delete(pool.store, key)
	pool.lock.Unlock()
}

func (pool *listenerPool) all() []ListenerHandler {
	all := make([]ListenerHandler, 0)
	for key := range pool.store {
		if handler, ok := pool.get(key); ok {
			all = append(all, handler)
		}
	}

	return all
}

type listenerHandler struct {
	log      log.Logger
	ctx      context.Context
	key      ListenerKey
	listener net.Listener
	output   chan<- Connection
	errs     chan<- error
	cancel   func()
}

func NewListenerHandler(
	ctx context.Context,
	key ListenerKey,
	listener net.Listener,
	output chan<- Connection,
	errs chan<- error,
	cancel func(),
) ListenerHandler {
	handler := &listenerHandler{
		ctx:      ctx,
		key:      key,
		listener: listener,
		output:   output,
		errs:     errs,
		cancel:   cancel,
	}
	handler.SetLog(log.StandardLogger())
	return handler
}

func (handler *listenerHandler) String() string {
	var name, key, listener, addition string
	if handler == nil {
		name = "<nil>"
		key = ""
		listener = ""
	} else {
		name = fmt.Sprintf("%p", handler)
		if handler.Key() != nil {
			key = fmt.Sprintf("%s", handler.Key())
		}
		if handler.Listener() != nil {
			listener = fmt.Sprintf("%s", handler.Listener())
		}
		if key != "" || listener != "" {
			addition = "("
			if key != "" {
				addition += key
			}
			if listener != "" {
				addition += listener
			}
			addition += ")"
		}
	}

	return fmt.Sprintf("listener handler %s%s", name, addition)
}

func (handler *listenerHandler) Log() log.Logger {
	return handler.log
}

func (handler *listenerHandler) SetLog(logger log.Logger) {
	handler.log = logger.WithFields(map[string]interface{}{
		"listener-handler": handler.String(),
		"listener-key":     fmt.Sprintf("%s", handler.Key()),
		"listener":         fmt.Sprintf("%s", handler.Listener()),
	})
}

func (handler *listenerHandler) Key() ListenerKey {
	return handler.key
}

func (handler *listenerHandler) Listener() net.Listener {
	return handler.listener
}

func (handler *listenerHandler) Serve() {
	defer func() {
		handler.Log().Infof("stop serving %s for key %s and %s", handler, handler.Key(), handler.Listener())
		handler.dispose()
	}()
	handler.Log().Infof("begin serving %s for key %s and %s", handler, handler.Key(), handler.Listener())

	for {
		select {
		case <-handler.ctx.Done():
			return
		default:
			handler.Listener()
			baseConn, err := handler.Listener().Accept()
			if err != nil {
				// if we get timeout error just go further and try read on the next iteration
				if err, ok := err.(net.Error); ok {
					if err.Timeout() || err.Temporary() {
						handler.Log().Debugf("%s timeout or temporary unavailable, sleep by %d seconds",
							handler.Listener(), netErrRetryTime)
						time.Sleep(netErrRetryTime)
						continue
					}
				}
				err = &ListenerHandlerError{err, handler}
				handler.Log().Debugf("pass up unhandled error %s from %s", err, handler)
				// broken or closed connection, stop reading and piping
				// and pass up error (net.Error)
				select {
				case <-handler.ctx.Done():
				case handler.errs <- err:
				}
				return
			}

			conn := NewConnection(baseConn)
			handler.Log().Infof("%s accepted new %s from %s to %s, passing it up", handler, conn,
				conn.RemoteAddr(), conn.LocalAddr())

			select {
			case <-handler.ctx.Done():
				return
			case handler.output <- conn:
			}
		}
	}
}

func (handler *listenerHandler) Cancel() {
	handler.cancel()
}

func (handler *listenerHandler) dispose() {
	handler.Log().Debugf("dispose %s for key %s and close %s", handler, handler.Key(), handler.Listener())
	handler.Listener().Close()
}
