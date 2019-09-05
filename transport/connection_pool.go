package transport

import (
	"bytes"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/parser"
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
	Done() <-chan struct{}
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
	Cancel()
	Done() <-chan struct{}
	String() string
	Key() ConnectionKey
	Connection() Connection
	// Expiry returns connection expiry time.
	Expiry() time.Time
	Expired() bool
	// Update updates connection expiry time.
	// TODO put later to allow runtime update
	// Update(conn Connection, ttl time.Duration)
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
	output  chan<- sip.Message
	errs    chan<- error
	cancel  <-chan struct{}
	done    chan struct{}
	hmess   chan sip.Message
	herrs   chan error
	gets    chan *connectionRequest
	updates chan *connectionRequest
	drops   chan *connectionRequest
	mu      *sync.RWMutex
}

func NewConnectionPool(output chan<- sip.Message, errs chan<- error, cancel <-chan struct{}) ConnectionPool {
	pool := &connectionPool{
		logger:  log.NewSafeLocalLogger(),
		hwg:     new(sync.WaitGroup),
		store:   make(map[ConnectionKey]ConnectionHandler),
		keys:    make([]ConnectionKey, 0),
		output:  output,
		errs:    errs,
		cancel:  cancel,
		done:    make(chan struct{}),
		hmess:   make(chan sip.Message),
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

	go func() {
		select {
		case <-pool.cancel:
		case pool.updates <- req:
		}
	}()

	select {
	case <-pool.cancel:
		return &PoolError{fmt.Errorf("%s canceled", pool), "put connection", pool.String()}
	case res := <-response:
		if len(res.errs) > 0 {
			return res.errs[0]
		}
		return nil
	}
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

	go func() {
		select {
		case <-pool.cancel:
		case pool.gets <- req:
		}
	}()

	select {
	case <-pool.cancel:
		return nil, &PoolError{fmt.Errorf("%s canceled", pool), "get connection", pool.String()}
	case res := <-response:
		return res.connections[0], res.errs[0]
	}
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

	go func() {
		select {
		case <-pool.cancel:
		case pool.drops <- req:
		}
	}()

	select {
	case <-pool.cancel:
		return &PoolError{fmt.Errorf("%s canceled", pool), "drop connection", pool.String()}
	case res := <-response:
		return res.errs[0]
	}
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

	go func() {
		select {
		case <-pool.cancel:
		case pool.drops <- req:
		}
	}()

	select {
	case <-pool.cancel:
		return &PoolError{fmt.Errorf("%s canceled", pool), "drop all connections", pool.String()}
	case <-response:
		return nil
	}
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

	go func() {
		select {
		case <-pool.cancel:
		case pool.gets <- req:
		}
	}()

	select {
	case <-pool.cancel:
		return []Connection{}
	case res := <-response:
		return res.connections
	}
}

func (pool *connectionPool) Length() int {
	return len(pool.allKeys())
}

func (pool *connectionPool) serveStore() {
	defer func() {
		pool.Log().Debugf("%s stops serve store routine", pool)
		pool.dispose()
	}()
	pool.Log().Debugf("%s begins serve store routine", pool)

	for {
		select {
		case <-pool.cancel:
			pool.Log().Debugf("%s received cancel signal", pool)
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
		pool.drop(key, true)
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
		pool.Log().Debugf("%s stops serve handlers routine", pool)
		close(pool.done)
	}()
	pool.Log().Debugf("%s begins serve handlers routine", pool)

	for {
		select {
		case msg, ok := <-pool.hmess:
			// cancel signal, serveStore exists
			if !ok {
				return
			}
			if msg == nil {
				continue
			}

			pool.Log().Debugf("%s received '%s'", pool, msg.Short())
			select {
			case <-pool.cancel:
				return
			case pool.output <- msg:
				continue
			}
		case err, ok := <-pool.herrs:
			// cancel signal, serveStore exists
			if !ok {
				return
			}
			if err == nil {
				continue
			}
			// on ConnectionHandleError we should drop handler in some cases
			// all other possible errors ignored because in pool.herrs should be only ConnectionHandlerErrors
			// so ConnectionPool passes up only Network (when connection falls) and MalformedMessage errors
			herr, ok := err.(*ConnectionHandlerError)
			if !ok {
				// all other possible errors
				pool.Log().Debugf("%s ignores non-handler error: %s", pool, err)
				continue
			}
			handler, gerr := pool.get(herr.Key)
			if gerr != nil {
				// ignore, handler already dropped out
				pool.Log().Debugf("ignore error from already dropped out handler %s: %s", herr.Key, herr)
				continue
			}

			if herr.Expired() {
				// handler expired, drop it from pool and continue without emitting error
				if handler.Expired() {
					// connection expired
					pool.Log().Debugf("%s notified that %s expired, drop it and go further", pool, handler)
					pool.Drop(handler.Key())
				} else {
					// Due to a race condition, the socket has been updated since this expiry happened.
					// Ignore the expiry since we already have a new socket for this address.
					pool.Log().Debugf("ignore spurious expiry of %s in %s", handler, pool)
				}
				continue
			} else if herr.EOF() {
				// remote endpoint closed
				pool.Log().Debugf("%s received EOF error: %s; drop %s and go further", pool, herr, handler)
				pool.Drop(handler.Key())
				continue
			} else if herr.Network() {
				// connection broken or closed
				pool.Log().Debugf("%s received network error: %s; drop %s and pass up", pool, herr, handler)
				pool.Drop(handler.Key())
			} else {
				// syntax errors, malformed message errors and other
				pool.Log().Debugf("%s received error: %s", pool, herr)
			}
			// send initial error
			select {
			case <-pool.cancel:
				return
			case pool.errs <- herr.Err:
				continue
			}
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
		return &PoolError{fmt.Errorf("%s already has key '%s'", pool, key),
			"put connection", pool.String()}
		// pool.Log().Debugf("update %s in %s", handler, pool)
		// handler.Update(ttl)
		// return nil
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

	return nil, &PoolError{fmt.Errorf("key '%s' not found in %s", key, pool),
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

	select {
	case <-pool.cancel:
	case req.response <- res:
	}
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

	select {
	case <-pool.cancel:
	case req.response <- res:
	}
}

func (pool *connectionPool) handleDrop(req *connectionRequest) {
	defer close(req.response)
	pool.Log().Debugf("handle drop request %#v", req)

	res := &connectionResponse{nil, []error{}}
	for _, key := range req.keys {
		res.errs = append(res.errs, pool.drop(key, true))
	}

	pool.Log().Debugf("send drop response %#v", res)

	select {
	case <-pool.cancel:
	case req.response <- res:
	}
}

// connectionHandler actually serves associated connection
type connectionHandler struct {
	logger     log.LocalLogger
	key        ConnectionKey
	connection Connection
	timer      timing.Timer
	ttl        time.Duration
	expiry     time.Time
	output     chan<- sip.Message
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
	output chan<- sip.Message,
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
		ttl:        ttl,
	}
	handler.SetLog(conn.Log())
	// handler.Update(ttl)
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
// func (handler *connectionHandler) Update(ttl time.Duration) {
// 	if ttl > 0 {
// 		expiryTime := timing.Now().Put(ttl)
// 		handler.Log().Debugf("set %s expiry time to %s", handler, expiryTime)
// 		handler.expiry = expiryTime
//
// 		if handler.timer == nil {
// 			handler.timer = timing.NewTimer(ttl)
// 		} else {
// 			handler.timer.Reset(ttl)
// 		}
// 	} else {
// 		handler.Log().Debugf("set %s unlimited expiry time")
// 		handler.expiry = time.Time{}
//
// 		if handler.timer == nil {
// 			handler.timer = timing.NewTimer(0)
// 		}
// 		handler.timer.Stop()
// 	}
// }

// connection serving loop.
// Waits for the connection to expire, and notifies the pool when it does.
func (handler *connectionHandler) Serve(done func()) {
	defer done()
	defer func() {
		handler.Log().Debugf("%s stops serve connection routine", handler)
		close(handler.done)
	}()

	handler.Log().Debugf("%s begins serve connection routine", handler)
	// watch for cancel
	go func() {
		<-handler.cancel
		handler.Log().Debugf("%s received cancel signal", handler)
		handler.Cancel()
	}()
	// start connection serving goroutines
	msgs, errs := handler.readConnection()
	handler.pipeOutputs(msgs, errs)
}

func (handler *connectionHandler) readConnection() (<-chan sip.Message, <-chan error) {
	msgs := make(chan sip.Message)
	errs := make(chan error)
	streamed := handler.Connection().Streamed()
	prs := parser.NewParser(msgs, errs, streamed)
	prs.SetLog(handler.Log())

	var raddr net.Addr
	if streamed {
		raddr = handler.Connection().RemoteAddr()
	} else {
		handler.addrs.Init()
		handler.addrs.SetLog(handler.Log())
		handler.addrs.Run()
	}

	go func() {
		defer func() {
			handler.Log().Debugf("%s stops read connection routine", handler)
			prs.Stop()
			if !streamed {
				handler.addrs.Stop()
			}
			close(msgs)
			close(errs)
		}()
		handler.Log().Debugf("%s begins read connection routine", handler)

		buf := make([]byte, bufferSize)

		var (
			num int
			err error
		)

		for {
			// wait for data
			if streamed {
				num, err = handler.Connection().Read(buf)
			} else {
				num, raddr, err = handler.Connection().ReadFrom(buf)
			}

			if err != nil {
				// if we get timeout error just go further and try read on the next iteration
				if err, ok := err.(net.Error); ok {
					if err.Timeout() || err.Temporary() {
						handler.Log().Warnf("%s timeout or temporary unavailable, sleep by %d seconds",
							handler.Connection(), netErrRetryTime/time.Second)
						time.Sleep(netErrRetryTime)
						continue
					}
				}
				// broken or closed connection
				// so send error and exit
				select {
				case <-handler.canceled:
				case errs <- err:
				}
				return
			}

			if !streamed {
				handler.addrs.In <- fmt.Sprintf("%v", raddr)
			}

			data := buf[:num]

			// skip empty udp packets
			if len(bytes.Trim(data, "\x00")) == 0 {
				handler.Log().Debugf("%s skips empty data: %v", handler, data)
				continue
			}

			// parse received data
			if _, err := prs.Write(append([]byte{}, buf[:num]...)); err != nil {
				select {
				case <-handler.canceled:
					return
				case errs <- err:
				}
			}
		}
	}()

	return msgs, errs
}

func (handler *connectionHandler) pipeOutputs(msgs <-chan sip.Message, errs <-chan error) {
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
			case <-time.After(time.Second):
				return "<nil>"
			}
		}
	}
	isSyntaxError := func(err error) bool {
		if serr, ok := err.(parser.Error); ok && serr.Syntax() {
			return true
		}
		if merr, ok := err.(sip.MessageError); ok && merr.Broken() {
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
		case <-handler.canceled:
			return
		case <-handler.timer.C():
			if handler.Expiry().IsZero() {
				// handler expiryTime is zero only when TTL = 0 (unlimited handler)
				// so we must not get here with zero expiryTime
				handler.Log().Fatalf("%s fires expiry timer with ZERO expiryTime,", handler)
			}

			// pass up to the pool
			// pool will make decision to drop out connection or update ttl.
			err := &ConnectionHandlerError{
				ExpireError(fmt.Sprintf("%s expired", handler.Connection())),
				handler.Key(),
				handler.String(),
				handler.Connection().Network(),
				fmt.Sprintf("%v", handler.Connection().LocalAddr()),
				fmt.Sprintf("%v", handler.Connection().RemoteAddr()),
			}
			select {
			case <-handler.canceled:
				return
			case handler.errs <- err:
			}
		case msg, ok := <-msgs:
			if !ok {
				return
			}
			handler.Log().Debugf("%s received message '%s'; pass it up", handler, msg.Short())
			// add Remote Address
			raddr := getRemoteAddr()
			rhost, rport, _ := net.SplitHostPort(raddr)

			switch msg := msg.(type) {
			case sip.Request:
				// RFC 3261 - 18.2.1
				viaHop, ok := msg.ViaHop()
				if !ok {
					handler.Log().Warnf("%s ignores message without 'Via' header '%s'", handler, msg.Short())
					continue
				}

				if rhost != "" && viaHop.Host != rhost {
					viaHop.Params.Add("received", sip.String{rhost})
				}
				// rfc3581
				if viaHop.Params.Has("rport") {
					viaHop.Params.Add("rport", sip.String{rport})
				}

				if handler.Connection().Streamed() {
					msg.SetSource(raddr)
				}
			case sip.Response:
				// Set Remote Address as response source
				msg.SetSource(raddr)
			}
			// pass up
			select {
			case <-handler.canceled:
				return
			case handler.output <- msg:
			}
			if !handler.Expiry().IsZero() {
				handler.expiry = time.Now().Add(handler.ttl)
				handler.timer.Reset(handler.ttl)
			}
		case err, ok := <-errs:
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
				handler.Log().Warnf("%s ignores error: %s", handler, err)
				continue
			}
			var raddr string
			if _, ok := err.(net.Error); ok {
				raddr = fmt.Sprintf("%v", handler.Connection().RemoteAddr())
			} else {
				raddr = getRemoteAddr()
			}

			err = &ConnectionHandlerError{
				err,
				handler.Key(),
				handler.String(),
				handler.Connection().Network(),
				fmt.Sprintf("%v", handler.Connection().LocalAddr()),
				raddr,
			}
			select {
			case <-handler.canceled:
				return
			case handler.errs <- err:
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
	defer func() { recover() }()
	handler.Log().Debugf("cancel %s", handler)
	close(handler.canceled)
	handler.Connection().Close()
}

func (handler *connectionHandler) Done() <-chan struct{} {
	return handler.done
}
