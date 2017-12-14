package transport

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/syntax"
	"github.com/ghettovoice/gosip/timing"
)

type ConnectionKey string

func (key ConnectionKey) String() string {
	return string(key)
}

// ConnectionPool used for active connection management.
type ConnectionPool interface {
	log.LocalLogger
	core.Awaiting
	String() string
	Put(key ConnectionKey, connection Connection, ttl time.Duration) error
	Get(key ConnectionKey) (Connection, error)
	All() []Connection
	Drop(key ConnectionKey) error
	DropAll() error
	Length() int
}

// ConnectionHandler serves associated connection, i.e. parses
// incoming data, manages expiry time & etc.
type ConnectionHandler interface {
	log.LocalLogger
	core.Cancellable
	core.Awaiting
	String() string
	Key() ConnectionKey
	Connection() Connection
	// Expiry returns connection expiry time.
	Expiry() time.Time
	Expired() bool
	// Update updates connection expiry time.
	// TODO put later to allow runtime update
	//Update(conn Connection, ttl time.Duration)
	// Manage runs connection serving.
	Serve(done func())
}

type connectionRequest struct {
	keys        []ConnectionKey
	connections []Connection
	ttls        []time.Duration
	response    chan *connectionResponse
}
type connectionResponse struct {
	connections []Connection
	errs        []error
}

type connectionPool struct {
	logger  log.LocalLogger
	hwg     *sync.WaitGroup
	store   map[ConnectionKey]ConnectionHandler
	keys    []ConnectionKey
	output  chan<- *IncomingMessage
	errs    chan<- error
	cancel  <-chan struct{}
	done    chan struct{}
	hmess   chan *IncomingMessage
	herrs   chan error
	gets    chan *connectionRequest
	updates chan *connectionRequest
	drops   chan *connectionRequest
	mu      *sync.RWMutex
}

func NewConnectionPool(output chan<- *IncomingMessage, errs chan<- error, cancel <-chan struct{}) ConnectionPool {
	pool := &connectionPool{
		logger:  log.NewSafeLocalLogger(),
		hwg:     new(sync.WaitGroup),
		store:   make(map[ConnectionKey]ConnectionHandler),
		keys:    make([]ConnectionKey, 0),
		output:  output,
		errs:    errs,
		cancel:  cancel,
		done:    make(chan struct{}),
		hmess:   make(chan *IncomingMessage),
		herrs:   make(chan error),
		gets:    make(chan *connectionRequest),
		updates: make(chan *connectionRequest),
		drops:   make(chan *connectionRequest),
		mu:      new(sync.RWMutex),
	}

	wg := new(sync.WaitGroup)
	wg.Add(2)
	go pool.serveStore(wg)
	go pool.serveHandlers(wg)

	return pool
}

func (pool *connectionPool) String() string {
	var name string
	if pool == nil {
		name = "<nil>"
	} else {
		name = fmt.Sprintf("%p", pool)
	}

	return fmt.Sprintf("connection pool %s", name)
}

func (pool *connectionPool) Log() log.Logger {
	return pool.logger.Log()
}

func (pool *connectionPool) SetLog(logger log.Logger) {
	pool.logger.SetLog(logger.WithField("conn-pool", pool.String()))
}

func (pool *connectionPool) Done() <-chan struct{} {
	return pool.done
}

// Put adds new connection to pool or updates TTL of existing connection
// TTL - 0 - unlimited; 1 - ... - time to live in pool
func (pool *connectionPool) Put(key ConnectionKey, connection Connection, ttl time.Duration) error {
	select {
	case <-pool.cancel:
		return &PoolError{fmt.Errorf("%s canceled", pool), "put connection", pool.String()}
	default:
	}
	if key == "" {
		return &PoolError{fmt.Errorf("invalid key provided"), "put connection", pool.String()}
	}

	response := make(chan *connectionResponse)
	req := &connectionRequest{[]ConnectionKey{key}, []Connection{connection},
		[]time.Duration{ttl}, response}

	pool.Log().Debugf("send put request %#v", req)
	pool.updates <- req
	res := <-response

	if len(res.errs) > 0 {
		return res.errs[0]
	}

	return nil
}

func (pool *connectionPool) Get(key ConnectionKey) (Connection, error) {
	select {
	case <-pool.cancel:
		return nil, &PoolError{fmt.Errorf("%s canceled", pool), "get connection", pool.String()}
	default:
	}

	response := make(chan *connectionResponse)
	req := &connectionRequest{[]ConnectionKey{key}, nil, nil, response}

	pool.Log().Debugf("send get request %#v", req)
	pool.gets <- req
	res := <-response

	return res.connections[0], res.errs[0]
}

func (pool *connectionPool) Drop(key ConnectionKey) error {
	select {
	case <-pool.cancel:
		return &PoolError{fmt.Errorf("%s canceled", pool), "drop connection", pool.String()}
	default:
	}

	response := make(chan *connectionResponse)
	req := &connectionRequest{[]ConnectionKey{key}, nil, nil, response}

	pool.Log().Debugf("send drop request %#v", req)
	pool.drops <- req
	res := <-response

	return res.errs[0]
}

func (pool *connectionPool) DropAll() error {
	select {
	case <-pool.cancel:
		return &PoolError{fmt.Errorf("%s canceled", pool), "drop all connections", pool.String()}
	default:
	}

	response := make(chan *connectionResponse)
	req := &connectionRequest{pool.allKeys(), nil, nil, response}

	pool.Log().Debugf("send drop request %#v", req)
	pool.drops <- req
	<-response

	return nil
}

func (pool *connectionPool) All() []Connection {
	select {
	case <-pool.cancel:
		return []Connection{}
	default:
	}

	response := make(chan *connectionResponse)
	req := &connectionRequest{pool.allKeys(), nil, nil, response}

	pool.Log().Debugf("send get request %#v", req)
	pool.gets <- req
	res := <-response

	return res.connections
}

func (pool *connectionPool) Length() int {
	return len(pool.allKeys())
}

func (pool *connectionPool) serveStore(wg *sync.WaitGroup) {
	defer func() {
		defer wg.Done()
		pool.Log().Infof("%s stop serving store", pool)
		pool.dispose()
	}()
	pool.Log().Infof("%s start serving store", pool)

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

func (pool *connectionPool) dispose() {
	// clean pool
	for _, key := range pool.allKeys() {
		pool.drop(key, false)
	}
	pool.hwg.Wait()
	// stop serveHandlers goroutine
	close(pool.hmess)
	close(pool.herrs)
	// close store channels
	close(pool.gets)
	close(pool.updates)
	close(pool.drops)
}

func (pool *connectionPool) serveHandlers(wg *sync.WaitGroup) {
	defer func() {
		defer wg.Done()
		pool.Log().Infof("%s stop serving handlers", pool)
		close(pool.done)
	}()
	pool.Log().Infof("%s start serving handlers", pool)

	for {
		select {
		case incomingMsg, ok := <-pool.hmess:
			// cancel signal, serveStore exists
			if !ok {
				return
			}
			if incomingMsg == nil {
				continue
			}

			pool.Log().Debugf("%s received %s %p", pool, incomingMsg.Msg.Short(), incomingMsg.Msg)
			pool.output <- incomingMsg
		case err, ok := <-pool.herrs:
			// cancel signal, serveStore exists
			if !ok {
				return
			}
			if err == nil {
				continue
			}
			// on ConnectionHandleError we should drop handler in some cases
			// all other possible errors passed up
			if lerr, ok := err.(*ConnectionHandlerError); ok {
				if handler, gerr := pool.get(lerr.Key); gerr == nil {
					// handler expired, drop it from pool and continue without emitting error
					if lerr.Expired() {
						if handler.Expired() {
							// connection expired
							pool.Log().Warnf("%s notified that %s expired, drop it", pool, handler)
							pool.Drop(handler.Key())
						} else {
							// Due to a race condition, the socket has been updated since this expiry happened.
							// Ignore the expiry since we already have a new socket for this address.
							pool.Log().Warnf("ignore spurious expiry of %s in %s", handler, pool)
							continue
						}
					} else if lerr.Network() {
						// connection broken or closed
						pool.Log().Warnf("%s received network error: %s; drop %s", pool, lerr, handler)
						pool.Drop(handler.Key())
					} else {
						// syntax errors, malformed message errors and other
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
			pool.errs <- err
		}
	}
}

func (pool *connectionPool) allKeys() []ConnectionKey {
	pool.mu.RLock()
	defer pool.mu.RUnlock()
	return append([]ConnectionKey{}, pool.keys...)
}

func (pool *connectionPool) put(key ConnectionKey, conn Connection, ttl time.Duration) error {
	if _, err := pool.get(key); err == nil {
		return &PoolError{fmt.Errorf("%s already has key %s", pool, key),
			"put connection", pool.String()}
		//pool.Log().Debugf("update %s in %s", handler, pool)
		//handler.Update(ttl)
		//return nil
	}
	// wrap to handler
	handler := NewConnectionHandler(key, conn, ttl, pool.hmess, pool.herrs, pool.cancel)
	pool.Log().Debugf("put %s to %s", handler, pool)
	// lock store
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

func (pool *connectionPool) drop(key ConnectionKey, cancel bool) error {
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

func (pool *connectionPool) get(key ConnectionKey) (ConnectionHandler, error) {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	if handler, ok := pool.store[key]; ok {
		return handler, nil
	}

	return nil, &PoolError{fmt.Errorf("key %s not found in %s", key, pool),
		"get connection", pool.String()}
}

func (pool *connectionPool) getConnection(key ConnectionKey) (Connection, error) {
	var conn Connection
	handler, err := pool.get(key)
	if err == nil {
		conn = handler.Connection()
	}
	return conn, err
}

func (pool *connectionPool) handlePut(req *connectionRequest) {
	defer close(req.response)
	pool.Log().Debugf("handle put request %#v", req)

	res := &connectionResponse{nil, []error{}}
	for i, key := range req.keys {
		res.errs = append(res.errs, pool.put(key, req.connections[i], req.ttls[i]))
	}

	pool.Log().Debugf("send put response %#v", res)
	req.response <- res
}

func (pool *connectionPool) handleGet(req *connectionRequest) {
	defer close(req.response)
	pool.Log().Debugf("handle get request %#v", req)

	res := &connectionResponse{[]Connection{}, []error{}}
	for _, key := range req.keys {
		conn, err := pool.getConnection(key)
		res.connections = append(res.connections, conn)
		res.errs = append(res.errs, err)
	}

	pool.Log().Debugf("send get response %#v", res)
	req.response <- res
}

func (pool *connectionPool) handleDrop(req *connectionRequest) {
	defer close(req.response)
	pool.Log().Debugf("handle drop request %#v", req)

	res := &connectionResponse{nil, []error{}}
	for _, key := range req.keys {
		res.errs = append(res.errs, pool.drop(key, true))
	}

	pool.Log().Debugf("send drop response %#v", res)
	req.response <- res
}

// connectionHandler actually serves associated connection
type connectionHandler struct {
	logger     log.LocalLogger
	key        ConnectionKey
	connection Connection
	timer      timing.Timer
	expiry     time.Time
	output     chan<- *IncomingMessage
	errs       chan<- error
	cancel     <-chan struct{}
	canceled   chan struct{}
	done       chan struct{}
}

func NewConnectionHandler(
	key ConnectionKey,
	conn Connection,
	ttl time.Duration,
	output chan<- *IncomingMessage,
	errs chan<- error,
	cancel <-chan struct{},
) ConnectionHandler {
	handler := &connectionHandler{
		logger:     log.NewSafeLocalLogger(),
		key:        key,
		connection: conn,
		output:     output,
		errs:       errs,
		cancel:     cancel,
		canceled:   make(chan struct{}),
		done:       make(chan struct{}),
	}
	handler.SetLog(conn.Log())
	//handler.Update(ttl)
	if ttl > 0 {
		handler.expiry = time.Now().Add(ttl)
		handler.timer = timing.NewTimer(ttl)
	} else {
		handler.expiry = time.Time{}
		handler.timer = timing.NewTimer(0)
		handler.timer.Stop()
	}
	return handler
}

func (handler *connectionHandler) String() string {
	var name, addition string
	if handler == nil {
		name = "<nil>"
	} else {
		name = fmt.Sprintf("%p", handler)
		parts := make([]string, 0)
		if handler.Key() != "" {
			parts = append(parts, fmt.Sprintf("key %s", handler.Key()))
		}
		if handler.Connection() != nil {
			parts = append(parts, fmt.Sprintf("%s", handler.Connection()))
		}
		if len(parts) > 0 {
			addition = " (" + strings.Join(parts, ", ") + ")"
		}
	}

	return fmt.Sprintf("connection handler %s%s", name, addition)
}

func (handler *connectionHandler) Log() log.Logger {
	return handler.logger.Log()
}

func (handler *connectionHandler) SetLog(logger log.Logger) {
	handler.logger.SetLog(logger.WithFields(map[string]interface{}{
		"conn-handler": handler.String(),
	}))
}

func (handler *connectionHandler) Key() ConnectionKey {
	return handler.key
}

func (handler *connectionHandler) Connection() Connection {
	return handler.connection
}

func (handler *connectionHandler) Expiry() time.Time {
	return handler.expiry
}

func (handler *connectionHandler) Expired() bool {
	return !handler.Expiry().IsZero() && handler.Expiry().Before(time.Now())
}

// resets the timeout timer.
//func (handler *connectionHandler) Update(ttl time.Duration) {
//	if ttl > 0 {
//		expiryTime := timing.Now().Put(ttl)
//		handler.Log().Debugf("set %s expiry time to %s", handler, expiryTime)
//		handler.expiry = expiryTime
//
//		if handler.timer == nil {
//			handler.timer = timing.NewTimer(ttl)
//		} else {
//			handler.timer.Reset(ttl)
//		}
//	} else {
//		handler.Log().Debugf("set %s unlimited expiry time")
//		handler.expiry = time.Time{}
//
//		if handler.timer == nil {
//			handler.timer = timing.NewTimer(0)
//		}
//		handler.timer.Stop()
//	}
//}

// connection serving loop.
// Waits for the connection to expire, and notifies the pool when it does.
func (handler *connectionHandler) Serve(done func()) {
	defer func() {
		defer done()
		handler.Log().Infof("%s stop serving", handler)
		close(handler.done)
	}()
	handler.Log().Infof("%s begin serving", handler)

	messages := make(chan core.Message)
	errs := make(chan error)
	parser := syntax.NewParser(messages, errs, handler.Connection().Streamed())
	parser.SetLog(handler.Log())
	// watch for cancel
	go func() {
		select {
		case <-handler.cancel:
			handler.Log().Warnf("%s received cancel signal", handler)
			handler.Cancel()
		case <-handler.canceled:
		}
	}()
	// start connection serving
	wg := new(sync.WaitGroup)
	wg.Add(2)
	go handler.readConnection(wg, parser, messages, errs)
	go handler.pipeOutputs(wg, parser, messages, errs)

	wg.Wait()
}

func (handler *connectionHandler) readConnection(
	wg *sync.WaitGroup,
	parser syntax.Parser,
	messages chan<- core.Message,
	errs chan<- error,
) {
	defer func() {
		defer wg.Done()
		handler.Log().Debugf("%s stop reading connection", handler)
		handler.Connection().Close()
		parser.Stop()
		// wait for parser dispose
		close(messages)
		close(errs)
	}()
	handler.Log().Debugf("%s begin reading connection", handler)

	buf := make([]byte, bufferSize)
	for {
		// wait for data
		num, err := handler.Connection().Read(buf)
		if err != nil {
			// if we get timeout error just go further and try read on the next iteration
			if err, ok := err.(net.Error); ok {
				if err.Timeout() || err.Temporary() {
					handler.Log().Debugf("%s timeout or temporary unavailable, sleep by %d seconds",
						handler.Connection(), netErrRetryTime)
					time.Sleep(netErrRetryTime)
					continue
				}
			}
			// broken or closed connection
			// pass up error and exit
			errs <- err
			return
		}

		pkt := append([]byte{}, buf[:num]...)
		if _, err := parser.Write(pkt); err != nil {
			errs <- err
		}
	}
}

func (handler *connectionHandler) pipeOutputs(
	wg *sync.WaitGroup,
	parser syntax.Parser,
	messages <-chan core.Message,
	errs <-chan error,
) {
	defer func() {
		defer wg.Done()
		handler.Log().Debugf("%s stop piping outputs", handler)
	}()
	handler.Log().Debugf("%s begin piping outputs", handler)

	for {
		select {
		case <-handler.timer.C():
			// cancel signal
			select {
			case <-handler.canceled:
				return
			default:
			}

			if handler.Expiry().IsZero() {
				// handler expiryTime is zero only when TTL = 0 (unlimited handler)
				// so we must not get here with zero expiryTime
				handler.Log().Fatalf("%s fires expiry timer with ZERO expiryTime,", handler)
			}
			handler.Log().Debugf("%s received expiry timer signal: %s inactive for too long", handler,
				handler.Connection())
			// pass up to the pool
			// pool will make decision to drop out connection or update ttl.
			handler.errs <- &ConnectionHandlerError{
				ExpireError(fmt.Sprintf("%s expired", handler.Connection())),
				handler.Key(),
				handler.String(),
			}
		case msg, ok := <-messages:
			// cancel signal
			select {
			case <-handler.canceled:
				return
			default:
			}
			// connection was closed, exit
			if !ok {
				return
			}

			if msg != nil {
				incomingMsg := &IncomingMessage{
					msg,
					handler.Connection().LocalAddr(),
					handler.Connection().RemoteAddr(),
				}

				handler.Log().Debugf("%s received message %s %p; pass it up", handler, msg.Short(), msg)
				handler.output <- incomingMsg
			}
		case err, ok := <-errs:
			// cancel signal
			select {
			case <-handler.canceled:
				return
			default:
			}
			// connection was closed, exit
			if !ok {
				return
			}

			if err != nil {
				handler.Log().Debugf("%s received error %s; pass it up", handler, err)

				if _, ok := err.(syntax.Error); ok {
					// parser/syntax errors, broken or malformed message
					handler.Log().Warnf("%s reset %s due to parser error: %s", handler, parser, err)
					parser.Reset()
					continue
				}

				if _, ok = err.(*ConnectionHandlerError); !ok {
					err = &ConnectionHandlerError{err, handler.Key(), handler.String()}
				}
				handler.errs <- err
			}
		}
	}
}

// Cancel simply calls runtime provided cancel function.
func (handler *connectionHandler) Cancel() {
	select {
	case <-handler.canceled:
		return
	default:
	}
	handler.Log().Debugf("cancel %s", handler)
	close(handler.canceled)
	handler.Connection().Close()
}

func (handler *connectionHandler) Done() <-chan struct{} {
	return handler.done
}
