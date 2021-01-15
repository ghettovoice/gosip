package transport

import (
	"crypto/tls"
	"errors"
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
	log.Loggable

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
	log.Loggable

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
	hwg   sync.WaitGroup
	mu    sync.RWMutex
	store map[ListenerKey]ListenerHandler
	keys  []ListenerKey

	output chan<- Connection
	errs   chan<- error
	cancel <-chan struct{}

	done    chan struct{}
	hconns  chan Connection
	herrs   chan error
	gets    chan *listenerRequest
	updates chan *listenerRequest
	drops   chan *listenerRequest

	log log.Logger
}

func NewListenerPool(
	output chan<- Connection,
	errs chan<- error,
	cancel <-chan struct{},
	logger log.Logger,
) ListenerPool {
	pool := &listenerPool{
		store: make(map[ListenerKey]ListenerHandler),
		keys:  make([]ListenerKey, 0),

		output: output,
		errs:   errs,
		cancel: cancel,

		done:    make(chan struct{}),
		hconns:  make(chan Connection),
		herrs:   make(chan error),
		gets:    make(chan *listenerRequest),
		updates: make(chan *listenerRequest),
		drops:   make(chan *listenerRequest),
	}
	pool.log = logger.
		WithPrefix("transport.ListenerPool").
		WithFields(log.Fields{
			"listener_pool_ptr": fmt.Sprintf("%p", pool),
		})

	go pool.serveStore()
	go pool.serveHandlers()

	return pool
}

func (pool *listenerPool) String() string {
	if pool == nil {
		return "<nil>"
	}

	return fmt.Sprintf("transport.ListenerPool<%s>", pool.Log().Fields())
}

func (pool *listenerPool) Log() log.Logger {
	return pool.log
}

// Done returns channel that resolves when pool gracefully completes it work.
func (pool *listenerPool) Done() <-chan struct{} {
	return pool.done
}

func (pool *listenerPool) Put(key ListenerKey, listener net.Listener) error {
	select {
	case <-pool.cancel:
		return &PoolError{
			fmt.Errorf("listener pool closed"),
			"put listener",
			pool.String(),
		}
	default:
	}
	if key == "" {
		return &PoolError{
			fmt.Errorf("empty listener key"),
			"put listener",
			pool.String(),
		}
	}

	response := make(chan *listenerResponse, 1)
	req := &listenerRequest{
		[]ListenerKey{key},
		[]net.Listener{listener},
		response,
	}

	logger := pool.Log().WithFields(log.Fields{
		"listener_key": key,
		"listener_ptr": fmt.Sprintf("%p", listener),
	})
	logger.Trace("sending put listener request")

	go func() {
		select {
		case <-pool.cancel:
		case pool.updates <- req:
			logger.Trace("put listener request sent")
		}
	}()

	select {
	case <-pool.cancel:
		return &PoolError{
			fmt.Errorf("listener pool closed"),
			"put listener",
			pool.String(),
		}
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
		return nil, &PoolError{
			fmt.Errorf("listener pool closed"),
			"get listener",
			pool.String(),
		}
	default:
	}

	response := make(chan *listenerResponse, 1)
	req := &listenerRequest{
		[]ListenerKey{key},
		nil,
		response,
	}

	logger := pool.Log().WithFields(log.Fields{
		"listener_key": key,
	})

	logger.Trace("sending get listener request")

	go func() {
		select {
		case <-pool.cancel:
		case pool.gets <- req:
			logger.Trace("get listener request sent")
		}
	}()

	select {
	case <-pool.cancel:
		return nil, &PoolError{
			fmt.Errorf("listener pool closed"),
			"get listener",
			pool.String(),
		}
	case res := <-response:
		return res.listeners[0], res.errs[0]
	}
}

func (pool *listenerPool) Drop(key ListenerKey) error {
	select {
	case <-pool.cancel:
		return &PoolError{
			fmt.Errorf("listener pool closed"),
			"drop listener",
			pool.String(),
		}
	default:
	}

	response := make(chan *listenerResponse, 1)
	req := &listenerRequest{
		[]ListenerKey{key},
		nil,
		response,
	}

	logger := pool.Log().WithFields(log.Fields{
		"listener_key": key,
	})

	logger.Trace("sending drop listener request")

	go func() {
		select {
		case <-pool.cancel:
		case pool.drops <- req:
			logger.Trace("drop listener request sent")
		}
	}()

	select {
	case <-pool.cancel:
		return &PoolError{
			fmt.Errorf("listener pool closed"),
			"drop listener",
			pool.String(),
		}
	case res := <-response:
		return res.errs[0]
	}
}

func (pool *listenerPool) DropAll() error {
	select {
	case <-pool.cancel:
		return &PoolError{
			fmt.Errorf("listener pool closed"),
			"drop all listeners",
			pool.String(),
		}
	default:
	}

	response := make(chan *listenerResponse, 1)
	keys := pool.allKeys()
	req := &listenerRequest{
		keys,
		nil,
		response,
	}

	logger := pool.Log().WithFields(log.Fields{
		"listener_key": fmt.Sprintf("%v", keys),
	})

	logger.Trace("sending drop all listeners request")

	go func() {
		select {
		case <-pool.cancel:
		case pool.drops <- req:
			logger.Trace("drop all listeners request sent")
		}
	}()

	select {
	case <-pool.cancel:
		return &PoolError{
			fmt.Errorf("listener pool closed"),
			"drop all listeners",
			pool.String(),
		}
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

	response := make(chan *listenerResponse, 1)
	keys := pool.allKeys()
	req := &listenerRequest{
		keys,
		nil,
		response,
	}

	logger := pool.Log().WithFields(log.Fields{
		"listener_keys": fmt.Sprintf("%v", keys),
	})

	logger.Trace("sending get all listeners request")

	go func() {
		select {
		case <-pool.cancel:
		case pool.gets <- req:
			logger.Trace("get all listeners request sent")
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
	defer pool.dispose()

	pool.Log().Debug("start serve listener store")
	defer pool.Log().Debug("stop serve listener store")

	for {
		select {
		case <-pool.cancel:
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
		if err := pool.drop(key, false); err != nil {
			pool.Log().WithFields(log.Fields{
				"listener_key": key,
			}).Error(err)
		}
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
	defer close(pool.done)

	pool.Log().Debug("start serve listener handlers")
	defer pool.Log().Debug("stop serve listener handlers")

	for {
		logger := pool.Log()

		select {
		case conn, ok := <-pool.hconns:
			if !ok {
				return
			}
			if conn == nil {
				continue
			}

			logger = log.AddFieldsFrom(logger, conn)
			logger.Trace("passing up connection")

			select {
			case <-pool.cancel:
				return
			case pool.output <- conn:
				logger.Trace("connection passed up")
			}
		case err, ok := <-pool.herrs:
			if !ok {
				return
			}
			if err == nil {
				continue
			}

			var lerr *ListenerHandlerError
			if errors.As(err, &lerr) {
				if handler, gerr := pool.get(lerr.Key); gerr == nil {
					logger = logger.WithFields(handler.Log().Fields())

					if lerr.Network() {
						// listener broken or closed, should be dropped
						logger.Debugf("listener network error: %s; drop it and go further", lerr)

						if err := pool.Drop(handler.Key()); err != nil {
							logger.Error(err)
						}
					} else {
						// other
						logger.Tracef("listener error: %s; pass the error up", lerr)
					}
				} else {
					// ignore, handler already dropped out
					logger.Tracef("ignore error from already dropped out listener %s: %s", lerr.Key, lerr)

					continue
				}
			} else {
				// all other possible errors
				logger.Tracef("ignore non listener error: %s", err)

				continue
			}

			select {
			case <-pool.cancel:
				return
			case pool.errs <- err:
				logger.Trace("error passed up")
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
		return &PoolError{
			fmt.Errorf("key %s already exists in the pool", key),
			"put listener",
			pool.String(),
		}
	}

	// wrap to handler
	handler := NewListenerHandler(key, listener, pool.hconns, pool.herrs, pool.cancel, pool.Log())

	pool.Log().WithFields(handler.Log().Fields()).Trace("put listener to the pool")

	pool.mu.Lock()

	// update store
	pool.store[handler.Key()] = handler
	pool.keys = append(pool.keys, handler.Key())

	pool.mu.Unlock()

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

	pool.Log().WithFields(handler.Log().Fields()).Trace("drop listener from the pool")

	pool.mu.Lock()

	// modify store
	delete(pool.store, key)
	for i, k := range pool.keys {
		if k == key {
			pool.keys = append(pool.keys[:i], pool.keys[i+1:]...)
			break
		}
	}

	pool.mu.Unlock()

	return nil
}

func (pool *listenerPool) get(key ListenerKey) (ListenerHandler, error) {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	if handler, ok := pool.store[key]; ok {
		return handler, nil
	}

	return nil, &PoolError{
		fmt.Errorf("listenr %s not found in the pool", key),
		"get listener",
		pool.String(),
	}
}

func (pool *listenerPool) handlePut(req *listenerRequest) {
	defer close(req.response)

	logger := pool.Log().WithFields(log.Fields{
		"listener_keys": fmt.Sprintf("%v", req.keys),
	})

	res := &listenerResponse{nil, []error{}}
	for i, key := range req.keys {
		res.errs = append(res.errs, pool.put(key, req.listeners[i]))
	}

	logger.Trace("sending put listener response")

	select {
	case <-pool.cancel:
	case req.response <- res:
		logger.Trace("put listener response sent")
	}
}

func (pool *listenerPool) handleGet(req *listenerRequest) {
	defer close(req.response)

	logger := pool.Log().WithFields(log.Fields{
		"listener_keys": fmt.Sprintf("%v", req.keys),
	})

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

	logger.Trace("sending get listener response")

	select {
	case <-pool.cancel:
	case req.response <- res:
		logger.Trace("get listener response sent")
	}
}

func (pool *listenerPool) handleDrop(req *listenerRequest) {
	defer close(req.response)

	logger := pool.Log().WithFields(log.Fields{
		"listener_keys": fmt.Sprintf("%v", req.keys),
	})

	res := &listenerResponse{nil, []error{}}
	for _, key := range req.keys {
		res.errs = append(res.errs, pool.drop(key, true))
	}

	logger.Trace("sending drop listener response")

	select {
	case <-pool.cancel:
	case req.response <- res:
		logger.Trace("drop listener response sent")
	}
}

type listenerHandler struct {
	key      ListenerKey
	listener net.Listener

	output chan<- Connection
	errs   chan<- error
	cancel <-chan struct{}

	canceled   chan struct{}
	done       chan struct{}
	cancelOnce sync.Once

	log log.Logger
}

func NewListenerHandler(
	key ListenerKey,
	listener net.Listener,
	output chan<- Connection,
	errs chan<- error,
	cancel <-chan struct{},
	logger log.Logger,
) ListenerHandler {
	handler := &listenerHandler{
		key:      key,
		listener: listener,

		output: output,
		errs:   errs,
		cancel: cancel,

		canceled: make(chan struct{}),
		done:     make(chan struct{}),
	}

	handler.log = logger.
		WithPrefix("transport.ListenerHandler").
		WithFields(log.Fields{
			"listener_handler_ptr": fmt.Sprintf("%p", handler),
			"listener_ptr":         fmt.Sprintf("%p", listener),
			"listener_key":         key,
		})

	return handler
}

func (handler *listenerHandler) String() string {
	if handler == nil {
		return "<nil>"
	}

	return fmt.Sprintf("transport.ListenerHandler<%s>", handler.Log().Fields())
}

func (handler *listenerHandler) Log() log.Logger {
	return handler.log
}

func (handler *listenerHandler) Key() ListenerKey {
	return handler.key
}

func (handler *listenerHandler) Listener() net.Listener {
	return handler.listener
}

func (handler *listenerHandler) Serve(done func()) {
	defer func() {
		close(handler.done)
		done()
	}()

	handler.Log().Debug("begin serve listener")
	defer handler.Log().Debugf("stop serve listener")

	conns := make(chan Connection)
	errs := make(chan error)

	// watch for cancel signal
	go func() {
		<-handler.cancel

		handler.Cancel()
	}()

	wg := sync.WaitGroup{}
	wg.Add(2)
	go handler.acceptConnections(&wg, conns, errs)
	go handler.pipeOutputs(&wg, conns, errs)

	wg.Wait()
}

func (handler *listenerHandler) acceptConnections(wg *sync.WaitGroup, conns chan<- Connection, errs chan<- error) {
	defer func() {
		handler.cancelOnce.Do(func() {
			if err := handler.Listener().Close(); err != nil {
				handler.Log().Errorf("close listener failed: %s", err)
			}
		})

		close(conns)
		close(errs)

		wg.Done()
	}()

	handler.Log().Debug("begin accept connections")
	defer handler.Log().Debug("stop accept connections")

	for {
		// wait for the new connection
		baseConn, err := handler.Listener().Accept()
		if err != nil {
			// if we get timeout error just go further and try accept on the next iteration
			var netErr net.Error
			if errors.As(err, &netErr) {
				if netErr.Timeout() || netErr.Temporary() {
					handler.Log().Warnf("listener timeout or temporary unavailable, sleep by %s", netErrRetryTime)

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
		var network string
		switch bc := baseConn.(type) {
		case *tls.Conn:
			network = "tls"
		case *wsConn:
			if _, ok := bc.Conn.(*tls.Conn); ok {
				network = "wss"
			} else {
				network = "ws"
			}
		default:
			network = strings.ToLower(baseConn.RemoteAddr().Network())
		}
		key := ConnectionKey(network + ":" + baseConn.RemoteAddr().String())
		conn := NewConnection(baseConn, key, network, handler.Log())

		select {
		case <-handler.canceled:
			return
		case conns <- conn:
		}
	}
}

func (handler *listenerHandler) pipeOutputs(wg *sync.WaitGroup, conns <-chan Connection, errs <-chan error) {
	defer wg.Done()

	handler.Log().Debug("begin pipe outputs")
	defer handler.Log().Debug("stop pipe outputs")

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

				logger := log.AddFieldsFrom(handler.Log(), conn)
				logger.Trace("passing up connection...")

				select {
				case <-handler.canceled:
					return
				case handler.output <- conn:
					logger.Trace("connection passed up")
				}
			}
		case err, ok := <-errs:
			// chan closed
			if !ok {
				return
			}
			if err != nil {
				var lerr *ListenerHandlerError
				if !errors.As(err, &lerr) {
					err = &ListenerHandlerError{
						err,
						handler.Key(),
						fmt.Sprintf("%p", handler),
						listenerNetwork(handler.Listener()),
						handler.Listener().Addr().String(),
					}
				}

				handler.Log().Trace("passing up listener error...")

				select {
				case <-handler.canceled:
					return
				case handler.errs <- err:
					handler.Log().Trace("listener error passed up")
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

	handler.cancelOnce.Do(func() {
		close(handler.canceled)

		if err := handler.Listener().Close(); err != nil {
			handler.Log().Errorf("close listener failed: %s")
		}

		handler.Log().Debug("listener handler canceled")
	})
}

// Done returns channel that resolves when handler gracefully completes it work.
func (handler *listenerHandler) Done() <-chan struct{} {
	return handler.done
}

func listenerNetwork(ls net.Listener) string {
	if val, ok := ls.(interface{ Network() string }); ok {
		return val.Network()
	}

	switch ls.(type) {
	case *net.TCPListener:
		return "tcp"
	case *net.UnixListener:
		return "unix"
	default:
		return ""
	}
}
