package transport

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/log"
)

type ListenerKey string

func (key ListenerKey) String() string {
	return string(key)
}

type ListenerPool interface {
	log.LocalLogger
	Done() <-chan struct{}
	String() string
	Put(key ListenerKey, listener net.Listener) error
	Get(key ListenerKey) (net.Listener, error)
	All() []net.Listener
	Drop(key ListenerKey) error
	DropAll() error
	Length() int
}

type ListenerHandler interface {
	log.LocalLogger
	Cancel()
	Done() <-chan struct{}
	String() string
	Key() ListenerKey
	Listener() net.Listener
	Serve(done func())
	// TODO implement later, runtime replace of the net.Listener in handler
	// Update(ls net.Listener)
}

type listenerRequest struct {
	keys      []ListenerKey
	listeners []net.Listener
	response  chan *listenerResponse
}
type listenerResponse struct {
	listeners []net.Listener
	errs      []error
}

type listenerPool struct {
	logger  log.LocalLogger
	hwg     *sync.WaitGroup
	store   map[ListenerKey]ListenerHandler
	keys    []ListenerKey
	output  chan<- Connection
	errs    chan<- error
	cancel  <-chan struct{}
	done    chan struct{}
	hconns  chan Connection
	herrs   chan error
	gets    chan *listenerRequest
	updates chan *listenerRequest
	drops   chan *listenerRequest
	mu      *sync.RWMutex
}

func NewListenerPool(output chan<- Connection, errs chan<- error, cancel <-chan struct{}) ListenerPool {
	pool := &listenerPool{
		logger:  log.NewSafeLocalLogger(),
		hwg:     new(sync.WaitGroup),
		store:   make(map[ListenerKey]ListenerHandler),
		keys:    make([]ListenerKey, 0),
		output:  output,
		errs:    errs,
		cancel:  cancel,
		done:    make(chan struct{}),
		hconns:  make(chan Connection),
		herrs:   make(chan error),
		gets:    make(chan *listenerRequest),
		updates: make(chan *listenerRequest),
		drops:   make(chan *listenerRequest),
		mu:      new(sync.RWMutex),
	}

	go pool.serveStore()
	go pool.serveHandlers()

	return pool
}

func (pool *listenerPool) String() string {
	return fmt.Sprintf("ListenerPool %p", pool)
}

func (pool *listenerPool) Log() log.Logger {
	return pool.logger.Log()
}

func (pool *listenerPool) SetLog(logger log.Logger) {
	pool.logger.SetLog(logger.WithField("listener-pool", pool.String()))
}

// Done returns channel that resolves when pool gracefully completes it work.
func (pool *listenerPool) Done() <-chan struct{} {
	return pool.done
}

func (pool *listenerPool) Put(key ListenerKey, listener net.Listener) error {
	select {
	case <-pool.cancel:
		return &PoolError{fmt.Errorf("%s canceled", pool), "put listener", pool.String()}
	default:
	}
	if key == "" {
		return &PoolError{fmt.Errorf("invalid key provided"), "put listener", pool.String()}
	}

	response := make(chan *listenerResponse)
	req := &listenerRequest{[]ListenerKey{key}, []net.Listener{listener}, response}

	pool.Log().Debugf("send put request %#v", req)

	go func() {
		select {
		case <-pool.cancel:
		case pool.updates <- req:
		}
	}()
	select {
	case <-pool.cancel:
		return &PoolError{fmt.Errorf("%s canceled", pool), "put listener", pool.String()}
	case res := <-response:
		if len(res.errs) > 0 {
			return res.errs[0]
		}
	}

	return nil
}

func (pool *listenerPool) Get(key ListenerKey) (net.Listener, error) {
	select {
	case <-pool.cancel:
		return nil, &PoolError{fmt.Errorf("%s canceled", pool), "get listener", pool.String()}
	default:
	}

	response := make(chan *listenerResponse)
	req := &listenerRequest{[]ListenerKey{key}, nil, response}

	pool.Log().Debugf("send get request %#v", req)

	go func() {
		select {
		case <-pool.cancel:
		case pool.gets <- req:
		}
	}()
	select {
	case <-pool.cancel:
		return nil, &PoolError{fmt.Errorf("%s canceled", pool), "get listener", pool.String()}
	case res := <-response:
		return res.listeners[0], res.errs[0]
	}
}

func (pool *listenerPool) Drop(key ListenerKey) error {
	select {
	case <-pool.cancel:
		return &PoolError{fmt.Errorf("%s canceled", pool), "drop listener", pool.String()}
	default:
	}

	response := make(chan *listenerResponse)
	req := &listenerRequest{[]ListenerKey{key}, nil, response}

	pool.Log().Debugf("send drop request %#v", req)

	go func() {
		select {
		case <-pool.cancel:
		case pool.drops <- req:
		}
	}()
	select {
	case <-pool.cancel:
		return &PoolError{fmt.Errorf("%s canceled", pool), "drop listener", pool.String()}
	case res := <-response:
		return res.errs[0]
	}
}

func (pool *listenerPool) DropAll() error {
	select {
	case <-pool.cancel:
		return &PoolError{fmt.Errorf("%s canceled", pool), "drop all listeners", pool.String()}
	default:
	}

	response := make(chan *listenerResponse)
	req := &listenerRequest{pool.allKeys(), nil, response}

	pool.Log().Debugf("send drop request %#v", req)

	go func() {
		select {
		case <-pool.cancel:
		case pool.drops <- req:
		}
	}()
	select {
	case <-pool.cancel:
		return &PoolError{fmt.Errorf("%s canceled", pool), "drop all listeners", pool.String()}
	case <-response:
		return nil
	}
}

func (pool *listenerPool) All() []net.Listener {
	select {
	case <-pool.cancel:
		return []net.Listener{}
	default:
	}

	response := make(chan *listenerResponse)
	req := &listenerRequest{pool.allKeys(), nil, response}

	pool.Log().Debugf("send get request %#v", req)

	go func() {
		select {
		case <-pool.cancel:
		case pool.gets <- req:
		}
	}()
	select {
	case <-pool.cancel:
		return []net.Listener{}
	case res := <-response:
		return res.listeners
	}
}

func (pool *listenerPool) Length() int {
	return len(pool.allKeys())
}

func (pool *listenerPool) serveStore() {
	defer func() {
		pool.Log().Infof("%s stops serve store routine", pool)
		pool.dispose()
	}()
	pool.Log().Infof("%s starts serve store routine", pool)

	for {
		select {
		case <-pool.cancel:
			pool.Log().Warnf("%s received cancel signal", pool)
			return
		case req := <-pool.updates:
			pool.handlePut(req)
		case req := <-pool.gets:
			pool.handleGet(req)
		case req := <-pool.drops:
			pool.handleDrop(req)
		}
	}
}

func (pool *listenerPool) dispose() {
	// wait for handlers
	for _, key := range pool.allKeys() {
		pool.drop(key, false)
	}
	pool.hwg.Wait()
	// stop serveHandlers goroutine
	close(pool.hconns)
	close(pool.herrs)
	// close store channels
	close(pool.gets)
	close(pool.updates)
	close(pool.drops)
}

func (pool *listenerPool) serveHandlers() {
	defer func() {
		pool.Log().Infof("%s stops serve handlers routine", pool)
		close(pool.done)
	}()
	pool.Log().Infof("%s starts serve handlers routine", pool)

	for {
		select {
		case conn, ok := <-pool.hconns:
			if !ok {
				return
			}
			if conn == nil {
				continue
			}

			pool.Log().Debugf("%s received %s", pool, conn)
			select {
			case <-pool.cancel:
				return
			case pool.output <- conn:
			}
		case err, ok := <-pool.herrs:
			if !ok {
				return
			}
			if err == nil {
				continue
			}

			if lerr, ok := err.(*ListenerHandlerError); ok {
				if handler, gerr := pool.get(lerr.Key); gerr == nil {
					if lerr.Network() {
						// listener broken or closed, should be dropped
						pool.Log().Warnf("%s received network error: %s; drop %s", pool, lerr, handler)
						pool.Drop(lerr.Key)
					} else {
						// other
						pool.Log().Debugf("%s received error: %s", pool, lerr)
					}
				} else {
					// ignore, handler already dropped out
					pool.Log().Debugf("ignore error from already dropped out handler %s: %s", lerr.Key, lerr)
					continue
				}
			} else {
				// all other possible errors
				pool.Log().Debugf("%s received error: %s", pool, err)
			}
			select {
			case <-pool.cancel:
				return
			case pool.errs <- err:
			}
		}
	}
}

func (pool *listenerPool) allKeys() []ListenerKey {
	pool.mu.RLock()
	defer pool.mu.RUnlock()
	return append([]ListenerKey{}, pool.keys...)
}

func (pool *listenerPool) put(key ListenerKey, listener net.Listener) error {
	if _, err := pool.get(key); err == nil {
		return &PoolError{fmt.Errorf("%s already has key %s", pool, key),
			"put listener", pool.String()}
	}

	// wrap to handler
	handler := NewListenerHandler(key, listener, pool.hconns, pool.herrs, pool.cancel)
	handler.SetLog(pool.Log())
	pool.Log().Debugf("put %s to %s", handler, pool)
	// lock store
	pool.mu.Lock()
	defer pool.mu.Unlock()
	// update store
	pool.store[handler.Key()] = handler
	pool.keys = append(pool.keys, handler.Key())
	// start serving
	pool.hwg.Add(1)
	go handler.Serve(pool.hwg.Done)

	return nil
}

func (pool *listenerPool) drop(key ListenerKey, cancel bool) error {
	// check existence in pool
	handler, err := pool.get(key)
	if err != nil {
		return err
	}

	if cancel {
		handler.Cancel()
	}
	pool.Log().Debugf("drop %s from %s", handler, pool)
	// lock store
	pool.mu.Lock()
	defer pool.mu.Unlock()
	// modify store
	delete(pool.store, key)
	for i, k := range pool.keys {
		if k == key {
			pool.keys = append(pool.keys[:i], pool.keys[i+1:]...)
			break
		}
	}

	return nil
}

func (pool *listenerPool) get(key ListenerKey) (ListenerHandler, error) {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	if handler, ok := pool.store[key]; ok {
		return handler, nil
	}

	return nil, &PoolError{fmt.Errorf("key %s not found in %s", key, pool),
		"get listener", pool.String()}
}

func (pool *listenerPool) handlePut(req *listenerRequest) {
	defer close(req.response)
	pool.Log().Debugf("handle put request %#v", req)

	res := &listenerResponse{nil, []error{}}
	for i, key := range req.keys {
		res.errs = append(res.errs, pool.put(key, req.listeners[i]))
	}

	pool.Log().Debugf("send put response %#v", res)
	select {
	case <-pool.cancel:
	case req.response <- res:
	}
}

func (pool *listenerPool) handleGet(req *listenerRequest) {
	defer close(req.response)
	pool.Log().Debugf("handle get request %#v", req)

	res := &listenerResponse{[]net.Listener{}, []error{}}
	for _, key := range req.keys {
		var ls net.Listener
		handler, err := pool.get(key)
		if err == nil {
			ls = handler.Listener()
		}
		res.listeners = append(res.listeners, ls)
		res.errs = append(res.errs, err)
	}

	pool.Log().Debugf("send get response %#v", res)
	select {
	case <-pool.cancel:
	case req.response <- res:
	}
}

func (pool *listenerPool) handleDrop(req *listenerRequest) {
	defer close(req.response)
	pool.Log().Debugf("handle drop request %#v", req)

	res := &listenerResponse{nil, []error{}}
	for _, key := range req.keys {
		res.errs = append(res.errs, pool.drop(key, true))
	}

	pool.Log().Debugf("send drop response %#v", res)
	select {
	case <-pool.cancel:
	case req.response <- res:
	}
}

type listenerHandler struct {
	logger   log.LocalLogger
	key      ListenerKey
	listener net.Listener
	output   chan<- Connection
	errs     chan<- error
	cancel   <-chan struct{}
	canceled chan struct{}
	done     chan struct{}
}

func NewListenerHandler(
	key ListenerKey,
	listener net.Listener,
	output chan<- Connection,
	errs chan<- error,
	cancel <-chan struct{},
) ListenerHandler {
	handler := &listenerHandler{
		logger:   log.NewSafeLocalLogger(),
		key:      key,
		listener: listener,
		output:   output,
		errs:     errs,
		cancel:   cancel,
		canceled: make(chan struct{}),
		done:     make(chan struct{}),
	}
	return handler
}

func (handler *listenerHandler) String() string {
	if handler == nil {
		return "ListenerHandler <nil>"
	}
	var info string
	parts := make([]string, 0)
	if handler.Key() != "" {
		parts = append(parts, fmt.Sprintf("key %s", handler.Key()))
	}
	if handler.Listener() != nil {
		parts = append(parts, fmt.Sprintf("%s listener %p on %s", listenerNetwork(handler.Listener()),
			handler.Listener(), handler.Listener().Addr()))
	}
	if len(parts) > 0 {
		info = " (" + strings.Join(parts, ", ") + ")"
	}

	return fmt.Sprintf("ListenerHandler %p%s", handler, info)
}

func (handler *listenerHandler) Log() log.Logger {
	return handler.logger.Log()
}

func (handler *listenerHandler) SetLog(logger log.Logger) {
	handler.logger.SetLog(logger.WithFields(map[string]interface{}{
		"listener-handler": handler.String(),
	}))
}

func (handler *listenerHandler) Key() ListenerKey {
	return handler.key
}

func (handler *listenerHandler) Listener() net.Listener {
	return handler.listener
}

func (handler *listenerHandler) Serve(done func()) {
	defer done()
	defer func() {
		handler.Log().Infof("%s stops serve listener routine", handler)
		close(handler.done)
	}()
	handler.Log().Infof("%s begins serve listener routine", handler)

	conns := make(chan Connection)
	errs := make(chan error)
	// watch for cancel signal
	go func() {
		<-handler.cancel
		handler.Log().Warnf("%s received cancel signal", handler)
		handler.Cancel()
	}()

	wg := new(sync.WaitGroup)
	wg.Add(2)
	go handler.acceptConnections(wg, conns, errs)
	go handler.pipeOutputs(wg, conns, errs)

	wg.Wait()
}

func (handler *listenerHandler) acceptConnections(wg *sync.WaitGroup, conns chan<- Connection, errs chan<- error) {
	defer func() {
		defer wg.Done()
		handler.Log().Debugf("%s stops accept connections routine", handler)
		handler.Listener().Close()
		close(conns)
		close(errs)
	}()
	handler.Log().Debugf("%s begins accept connections routine", handler)

	for {
		// wait for the new connection
		baseConn, err := handler.Listener().Accept()
		if err != nil {
			// if we get timeout error just go further and try accept on the next iteration
			if err, ok := err.(net.Error); ok {
				if err.Timeout() || err.Temporary() {
					handler.Log().Debugf("listener %p timeout or temporary unavailable, sleep by %d seconds",
						handler.Listener(), netErrRetryTime/time.Second)
					time.Sleep(netErrRetryTime)
					continue
				}
			}
			// broken or closed listener
			// pass up error and exit
			select {
			case <-handler.canceled:
			case errs <- err:
			}
			return
		}

		conn := NewConnection(baseConn)
		conn.SetLog(handler.Log())
		select {
		case <-handler.canceled:
			return
		case conns <- conn:
		}
	}
}

func (handler *listenerHandler) pipeOutputs(wg *sync.WaitGroup, conns <-chan Connection, errs <-chan error) {
	defer func() {
		defer wg.Done()
		handler.Log().Debugf("%s stops pipe outputs routine", handler)
	}()
	handler.Log().Debugf("%s begins pipe outputs routine", handler)

	for {
		select {
		case <-handler.canceled:
			return
		case conn, ok := <-conns:
			// chan closed
			if !ok {
				return
			}
			if conn != nil {
				handler.Log().Infof("%s accepted new %s; pass it up", handler, conn)
				select {
				case <-handler.canceled:
					return
				case handler.output <- conn:
				}
			}
		case err, ok := <-errs:
			// chan closed
			if !ok {
				return
			}
			if err != nil {
				handler.Log().Debugf("%s received error %s; pass it up", handler, err)

				if _, ok := err.(*ListenerHandlerError); !ok {
					err = &ListenerHandlerError{err, handler.Key(), handler.String(),
						listenerNetwork(handler.Listener()), handler.Listener().Addr().String()}
				}
				select {
				case <-handler.canceled:
					return
				case handler.errs <- err:
				}
			}
		}
	}
}

// Cancel stops serving.
// blocked until Serve completes
func (handler *listenerHandler) Cancel() {
	select {
	case <-handler.canceled:
		return
	default:
	}
	defer func() { recover() }()
	handler.Log().Debugf("cancel %s", handler)
	close(handler.canceled)
	handler.Listener().Close()
}

// Done returns channel that resolves when handler gracefully completes it work.
func (handler *listenerHandler) Done() <-chan struct{} {
	return handler.done
}

func listenerNetwork(ls net.Listener) string {
	switch ls.(type) {
	case *net.TCPListener:
		return "tcp"
	case *net.UnixListener:
		return "unix"
	default:
		return ""
	}
}
