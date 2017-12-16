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
	"github.com/ghettovoice/gosip/util"
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

	go pool.serveStore()
	go pool.serveHandlers()

	return pool
}

func (pool *connectionPool) String() string {
	return fmt.Sprintf("ConnectionPool %p", pool)
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

func (pool *connectionPool) serveStore() {
	defer func() {
		pool.Log().Infof("%s stops serve store routine", pool)
		pool.dispose()
	}()
	pool.Log().Infof("%s begins serve store routine", pool)

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

func (pool *connectionPool) serveHandlers() {
	defer func() {
		pool.Log().Infof("%s stops serve handlers routine", pool)
		close(pool.done)
	}()
	pool.Log().Infof("%s begins serve handlers routine", pool)

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
	pool.Log().Debugf("put %s to %s with TTL = %s", handler, pool, ttl)
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
	addrs      util.ElasticChan
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
	if handler == nil {
		return "ConnectionHandler <nil>"
	}

	var info string
	parts := make([]string, 0)
	if handler.Key() != "" {
		parts = append(parts, fmt.Sprintf("key %s", handler.Key()))
	}
	if handler.Connection() != nil {
		parts = append(parts, fmt.Sprintf("%s", handler.Connection()))
	}
	if len(parts) > 0 {
		info = " (" + strings.Join(parts, ", ") + ")"
	}

	return fmt.Sprintf("ConnectionHandler %p%s", handler, info)
}

func (handler *connectionHandler) Log() log.Logger {
	return handler.logger.Log().WithFields(map[string]interface{}{
		"conn-handler": handler.String(),
		"conn":         handler.Connection().String(),
		"raddr":        fmt.Sprintf("%v", handler.Connection().RemoteAddr()),
	})
}

func (handler *connectionHandler) SetLog(logger log.Logger) {
	handler.logger.SetLog(logger)
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
		handler.Log().Infof("%s stops serve connection routine", handler)
		close(handler.done)
	}()

	handler.Log().Infof("%s begins serve connection routine", handler)
	// watch for cancel
	go func() {
		select {
		case <-handler.cancel:
			handler.Log().Warnf("%s received cancel signal", handler)
			handler.Cancel()
		case <-handler.canceled:
		}
	}()
	// start connection serving goroutines
	msgs, errs := handler.readConnection()
	handler.pipeOutputs(msgs, errs)
}

func (handler *connectionHandler) readConnection() (<-chan core.Message, <-chan error) {
	msgs := make(chan core.Message)
	errs := make(chan error)
	streamed := handler.Connection().Streamed()
	parser := syntax.NewParser(msgs, errs, streamed)
	parser.SetLog(handler.Log())
	if !streamed {
		handler.addrs.Init()
		handler.addrs.SetLog(handler.Log())
		handler.addrs.Run()
	}

	go func() {
		defer func() {
			handler.Log().Debugf("%s stops read connection routine", handler)
			parser.Stop()
			if !streamed {
				handler.addrs.Stop()
			}
			close(msgs)
			close(errs)
		}()
		handler.Log().Debugf("%s begins read connection routine", handler)

		buf := make([]byte, bufferSize)
		for {
			// wait for data
			num, err := handler.Connection().Read(buf)
			select {
			case <-handler.canceled:
				return
			default:
			}
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
				// so send error and exit
				errs <- err
				return
			}
			// parse received data
			parser.SetLog(handler.Log())
			if _, err := parser.Write(append([]byte{}, buf[:num]...)); err == nil {
				if !streamed {
					handler.addrs.In <- fmt.Sprintf("%v", handler.Connection().RemoteAddr())
				}
			} else {
				errs <- err
			}
		}
	}()

	return msgs, errs
}

func (handler *connectionHandler) pipeOutputs(msgs <-chan core.Message, errs <-chan error) {
	streamed := handler.Connection().Streamed()
	getRemoteAddr := func() string {
		if streamed {
			return fmt.Sprintf("%v", handler.Connection().RemoteAddr())
		} else {
			// use non-blocking read because remote address already should be here
			// or error occurred in read connection goroutine
			// TODO: fix this, sometimes it returns nil
			select {
			case v := <-handler.addrs.Out:
				return v.(string)
			default:
				return "<nil>"
			}
		}
	}
	isSyntaxError := func(err error) bool {
		if serr, ok := err.(syntax.Error); ok && serr.Syntax() {
			return true
		}
		if merr, ok := err.(core.MessageError); ok && merr.Broken() {
			return true
		}
		return false
	}

	defer func() {
		handler.Log().Debugf("%s stops pipe outputs routine", handler)
	}()
	handler.Log().Debugf("%s begins pipe outputs routine", handler)

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
				handler.Connection().Network(),
				fmt.Sprintf("%v", handler.Connection().LocalAddr()),
				fmt.Sprintf("%v", handler.Connection().RemoteAddr()),
			}
		case msg, ok := <-msgs:
			// cancel signal
			select {
			case <-handler.canceled:
				return
			default:
			}
			if !ok {
				return
			}
			handler.Log().Infof("%s received message %s; pass it up", handler, msg.Short())
			handler.output <- &IncomingMessage{
				msg,
				handler.Connection().LocalAddr().String(),
				getRemoteAddr(),
				handler.Connection().Network(),
			}
		case err, ok := <-errs:
			// cancel signal
			select {
			case <-handler.canceled:
				return
			default:
			}
			if !ok {
				return
			}

			if isSyntaxError(err) {
				// ignore broken message, syntax errors
				// such error can arrives only from parser goroutine
				// so we need to read remote address for broken message
				if !streamed {
					<-handler.addrs.Out
				}
				handler.Log().Warnf("%s ignores error %s", handler, err)
				continue
			}

			handler.Log().Debugf("%s received error %s; pass it up", handler, err)
			err = &ConnectionHandlerError{
				err,
				handler.Key(),
				handler.String(),
				handler.Connection().Network(),
				fmt.Sprintf("%v", handler.Connection().LocalAddr()),
				getRemoteAddr(),
			}
			handler.errs <- err
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
