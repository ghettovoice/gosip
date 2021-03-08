package transport

import (
	"bytes"
	"errors"
	"fmt"
	"net"
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
	Done() <-chan struct{}
	String() string
	Put(connection Connection, ttl time.Duration) error
	Get(key ConnectionKey) (Connection, error)
	All() []Connection
	Drop(key ConnectionKey) error
	DropAll() error
	Length() int
}

// ConnectionHandler serves associated connection, i.e. parses
// incoming data, manages expiry time & etc.
type ConnectionHandler interface {
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

	response chan *connectionResponse
}
type connectionResponse struct {
	connections []Connection
	errs        []error
}

type connectionPool struct {
	store     map[ConnectionKey]ConnectionHandler
	keys      []ConnectionKey
	msgMapper sip.MessageMapper

	output chan<- sip.Message
	errs   chan<- error
	cancel <-chan struct{}

	done  chan struct{}
	hmess chan sip.Message
	herrs chan error

	gets    chan *connectionRequest
	updates chan *connectionRequest
	drops   chan *connectionRequest

	hwg sync.WaitGroup
	mu  sync.RWMutex

	log log.Logger
}

func NewConnectionPool(
	output chan<- sip.Message,
	errs chan<- error,
	cancel <-chan struct{},
	msgMapper sip.MessageMapper,
	logger log.Logger,
) ConnectionPool {
	pool := &connectionPool{
		store:     make(map[ConnectionKey]ConnectionHandler),
		keys:      make([]ConnectionKey, 0),
		msgMapper: msgMapper,

		output: output,
		errs:   errs,
		cancel: cancel,

		done:  make(chan struct{}),
		hmess: make(chan sip.Message),
		herrs: make(chan error),

		gets:    make(chan *connectionRequest),
		updates: make(chan *connectionRequest),
		drops:   make(chan *connectionRequest),
	}

	pool.log = logger.
		WithPrefix("transport.ConnectionPool").
		WithFields(log.Fields{
			"connection_pool_ptr": fmt.Sprintf("%p", pool),
		})

	go pool.serveStore()
	go pool.serveHandlers()

	return pool
}

func (pool *connectionPool) String() string {
	if pool == nil {
		return "<nil>"
	}

	return fmt.Sprintf("transport.ConnectionPool<%s>", pool.Log().Fields())
}

func (pool *connectionPool) Log() log.Logger {
	return pool.log
}

func (pool *connectionPool) Done() <-chan struct{} {
	return pool.done
}

// Put adds new connection to pool or updates TTL of existing connection
// TTL - 0 - unlimited; 1 - ... - time to live in pool
func (pool *connectionPool) Put(connection Connection, ttl time.Duration) error {
	select {
	case <-pool.cancel:
		return &PoolError{
			fmt.Errorf("connection pool closed"),
			"put connection",
			pool.String(),
		}
	default:
	}

	key := connection.Key()

	if key == "" {
		return &PoolError{
			fmt.Errorf("empty connection key"),
			"put connection",
			pool.String(),
		}
	}

	response := make(chan *connectionResponse, 1)
	req := &connectionRequest{
		[]ConnectionKey{key},
		[]Connection{connection},
		[]time.Duration{ttl},
		response,
	}

	logger := pool.Log().WithFields(log.Fields{
		"connection_key": key,
		"connection_ptr": fmt.Sprintf("%p", connection),
		"connection_ttl": ttl,
	})

	logger.Trace("sending put connection request")

	go func() {
		select {
		case <-pool.cancel:
		case pool.updates <- req:
			logger.Trace("put connection request sent")
		}
	}()

	select {
	case <-pool.cancel:
		return &PoolError{
			fmt.Errorf("connection pool closed"),
			"put connection",
			pool.String(),
		}
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
		return nil, &PoolError{
			fmt.Errorf("connection pool closed"),
			"get connection",
			pool.String(),
		}
	default:
	}

	response := make(chan *connectionResponse, 1)
	req := &connectionRequest{
		[]ConnectionKey{key},
		nil,
		nil,
		response,
	}

	logger := pool.Log().WithFields(log.Fields{
		"connection_key": key,
	})

	logger.Trace("sending get connection request")

	go func() {
		select {
		case <-pool.cancel:
		case pool.gets <- req:
			logger.Trace("get connection request sent")
		}
	}()

	select {
	case <-pool.cancel:
		return nil, &PoolError{
			fmt.Errorf("connection pool canceled"),
			"get connection",
			pool.String(),
		}
	case res := <-response:
		return res.connections[0], res.errs[0]
	}
}

func (pool *connectionPool) Drop(key ConnectionKey) error {
	select {
	case <-pool.cancel:
		return &PoolError{
			fmt.Errorf("connection pool closed"),
			"drop connection",
			pool.String(),
		}
	default:
	}

	response := make(chan *connectionResponse, 1)
	req := &connectionRequest{
		[]ConnectionKey{key},
		nil,
		nil,
		response,
	}

	logger := pool.Log().WithFields(log.Fields{
		"connection_key": key,
	})

	logger.Trace("sending drop connection request")

	go func() {
		select {
		case <-pool.cancel:
		case pool.drops <- req:
			logger.Trace("drop connection request sent")
		}
	}()

	select {
	case <-pool.cancel:
		return &PoolError{
			fmt.Errorf("connection pool canceled"),
			"drop connection",
			pool.String(),
		}
	case res := <-response:
		return res.errs[0]
	}
}

func (pool *connectionPool) DropAll() error {
	select {
	case <-pool.cancel:
		return &PoolError{
			fmt.Errorf("connection pool closed"),
			"drop all connections",
			pool.String(),
		}
	default:
	}

	response := make(chan *connectionResponse, 1)
	keys := pool.allKeys()
	req := &connectionRequest{
		keys,
		nil,
		nil,
		response,
	}

	logger := pool.Log().WithFields(log.Fields{
		"connection_keys": fmt.Sprintf("%v", keys),
	})

	logger.Trace("sending drop all connections request")

	go func() {
		select {
		case <-pool.cancel:
		case pool.drops <- req:
			logger.Trace("drop all connections request sent")
		}
	}()

	select {
	case <-pool.cancel:
		return &PoolError{
			fmt.Errorf("connection pool closed"),
			"drop all connections",
			pool.String(),
		}
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

	response := make(chan *connectionResponse, 1)
	keys := pool.allKeys()
	req := &connectionRequest{
		keys,
		nil,
		nil,
		response,
	}

	logger := pool.Log().WithFields(log.Fields{
		"connection_keys": fmt.Sprintf("%v", keys),
	})

	logger.Trace("sending get all connections request")

	go func() {
		select {
		case <-pool.cancel:
		case pool.gets <- req:
			logger.Trace("get all connections request sent")
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
	defer pool.dispose()

	pool.Log().Debug("begin serve connection store")
	defer pool.Log().Debug("stop serve connection store")

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

func (pool *connectionPool) dispose() {
	// clean pool
	for _, key := range pool.allKeys() {
		if err := pool.drop(key, true); err != nil {
			pool.Log().WithFields(log.Fields{
				"connection_key": key,
			}).Error(err)
		}
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
	defer close(pool.done)

	pool.Log().Debug("begin serve connection handlers")
	defer pool.Log().Debug("stop serve connection handlers")

	for {
		logger := pool.Log()

		select {
		case msg, ok := <-pool.hmess:
			// cancel signal, serveStore exists
			if !ok {
				return
			}
			if msg == nil {
				continue
			}

			logger = logger.WithFields(msg.Fields())
			logger.Trace("passing up SIP message")

			select {
			case <-pool.cancel:
				return
			case pool.output <- msg:
				logger.Trace("SIP message passed up")

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
			var herr *ConnectionHandlerError
			if !errors.As(err, &herr) {
				// all other possible errors
				logger.Tracef("ignore non connection error: %s", err)

				continue
			}

			handler, gerr := pool.get(herr.Key)
			if gerr != nil {
				// ignore, handler already dropped out
				logger.Tracef("ignore error from already dropped out connection %s: %s", herr.Key, gerr)

				continue
			}

			logger = logger.WithFields(log.Fields{
				"connection_handler": handler.String(),
			})

			if herr.Expired() {
				// handler expired, drop it from pool and continue without emitting error
				if handler.Expired() {
					// connection expired
					logger.Debug("connection expired, drop it and go further")

					if err := pool.Drop(handler.Key()); err != nil {
						logger.Error(err)
					}
				} else {
					// Due to a race condition, the socket has been updated since this expiry happened.
					// Ignore the expiry since we already have a new socket for this address.
					logger.Trace("ignore spurious connection expiry")
				}

				continue
			} else if herr.EOF() {
				select {
				case <-pool.cancel:
					return
				default:
				}

				// remote endpoint closed
				logger.Debugf("connection EOF: %s; drop it and go further", herr)

				if err := pool.Drop(handler.Key()); err != nil {
					logger.Error(err)
				}

				var connErr *ConnectionError
				if errors.As(herr.Err, &connErr) {
					pool.errs <- herr.Err
				}

				continue
			} else if herr.Network() {
				// connection broken or closed
				logger.Debugf("connection network error: %s; drop it and pass the error up", herr)

				if err := pool.Drop(handler.Key()); err != nil {
					logger.Error(err)
				}
			} else {
				// syntax errors, malformed message errors and other
				logger.Tracef("connection error: %s; pass the error up", herr)
			}
			// send initial error
			select {
			case <-pool.cancel:
				return
			case pool.errs <- herr.Err:
				logger.Trace("error passed up")

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
		return &PoolError{
			fmt.Errorf("key %s already exists in the pool", key),
			"put connection",
			pool.String(),
		}
	}

	// wrap to handler
	handler := NewConnectionHandler(
		conn,
		ttl,
		pool.hmess,
		pool.herrs,
		pool.cancel,
		pool.msgMapper,
		pool.Log(),
	)

	logger := log.AddFieldsFrom(pool.Log(), handler)
	logger.Tracef("put connection to the pool with TTL = %s", ttl)

	pool.mu.Lock()

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

	logger := log.AddFieldsFrom(pool.Log(), handler)
	logger.Trace("drop connection from the pool")

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

func (pool *connectionPool) get(key ConnectionKey) (ConnectionHandler, error) {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	if handler, ok := pool.store[key]; ok {
		return handler, nil
	}

	return nil, &PoolError{
		fmt.Errorf("connection %s not found in the pool", key),
		"get connection",
		pool.String(),
	}
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

	logger := pool.Log().WithFields(log.Fields{
		"connection_keys": fmt.Sprintf("%v", req.keys),
		"connection_ttls": fmt.Sprintf("%v", req.ttls),
	})

	res := &connectionResponse{nil, []error{}}
	for i, key := range req.keys {
		res.errs = append(res.errs, pool.put(key, req.connections[i], req.ttls[i]))
	}

	logger.Trace("sending put connection response")

	select {
	case <-pool.cancel:
	case req.response <- res:
		logger.Trace("put connection response sent")
	}
}

func (pool *connectionPool) handleGet(req *connectionRequest) {
	defer close(req.response)

	logger := pool.Log().WithFields(log.Fields{
		"connection_keys": fmt.Sprintf("%v", req.keys),
	})

	res := &connectionResponse{[]Connection{}, []error{}}
	for _, key := range req.keys {
		conn, err := pool.getConnection(key)
		res.connections = append(res.connections, conn)
		res.errs = append(res.errs, err)
	}

	logger.Trace("sending get connection response")

	select {
	case <-pool.cancel:
	case req.response <- res:
		logger.Trace("get connection response sent")
	}
}

func (pool *connectionPool) handleDrop(req *connectionRequest) {
	defer close(req.response)

	logger := pool.Log().WithFields(log.Fields{
		"connection_keys": fmt.Sprintf("%v", req.keys),
	})

	logger.Trace("handle drop connections request")

	res := &connectionResponse{nil, []error{}}
	for _, key := range req.keys {
		res.errs = append(res.errs, pool.drop(key, true))
	}

	logger.Debugf("sending drop connection response")

	select {
	case <-pool.cancel:
	case req.response <- res:
		logger.Debugf("drop connection response sent")
	}
}

// connectionHandler actually serves associated connection
type connectionHandler struct {
	connection Connection
	msgMapper  sip.MessageMapper

	timer  timing.Timer
	ttl    time.Duration
	expiry time.Time

	output     chan<- sip.Message
	errs       chan<- error
	cancel     <-chan struct{}
	cancelOnce sync.Once
	canceled   chan struct{}
	done       chan struct{}
	addrs      util.ElasticChan

	log log.Logger
}

func NewConnectionHandler(
	conn Connection,
	ttl time.Duration,
	output chan<- sip.Message,
	errs chan<- error,
	cancel <-chan struct{},
	msgMapper sip.MessageMapper,
	logger log.Logger,
) ConnectionHandler {
	handler := &connectionHandler{
		connection: conn,
		msgMapper:  msgMapper,

		output: output,
		errs:   errs,
		cancel: cancel,

		canceled: make(chan struct{}),
		done:     make(chan struct{}),

		ttl: ttl,
	}

	handler.log = logger.
		WithPrefix("transport.ConnectionHandler").
		WithFields(log.Fields{
			"connection_handler_ptr": fmt.Sprintf("%p", handler),
			"connection_ptr":         fmt.Sprintf("%p", conn),
			"connection_key":         conn.Key(),
			"connection_network":     conn.Network(),
		})

	// handler.Update(ttl)
	if ttl > 0 {
		handler.expiry = time.Now().Add(ttl)
		handler.timer = timing.NewTimer(ttl)
	} else {
		handler.expiry = time.Time{}
		handler.timer = timing.NewTimer(0)
		if !handler.timer.Stop() {
			<-handler.timer.C()
		}
	}

	if handler.msgMapper == nil {
		handler.msgMapper = func(msg sip.Message) sip.Message {
			return msg
		}
	}

	return handler
}

func (handler *connectionHandler) String() string {
	if handler == nil {
		return "<nil>"
	}

	return fmt.Sprintf("transport.ConnectionHandler<%s>", handler.Log().Fields())
}

func (handler *connectionHandler) Log() log.Logger {
	return handler.log
}

func (handler *connectionHandler) Key() ConnectionKey {
	return handler.connection.Key()
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
	defer func() {
		close(handler.done)
		done()
	}()

	handler.Log().Debug("begin serve connection")
	defer handler.Log().Debug("stop serve connection")

	// watch for cancel
	go func() {
		<-handler.cancel

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
	prs := parser.NewParser(msgs, errs, streamed, handler.Log())

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
			handler.cancelOnce.Do(func() {
				if err := handler.Connection().Close(); err != nil {
					handler.Log().Errorf("connection close failed: %s", err)
				}
			})

			prs.Stop()

			if !streamed {
				handler.addrs.Stop()
			}

			close(msgs)
			close(errs)
		}()

		handler.Log().Debug("begin read connection")
		defer handler.Log().Debug("stop read connection")

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
				var netErr net.Error
				if errors.As(err, &netErr) {
					if netErr.Timeout() || netErr.Temporary() {
						handler.Log().Warnf("connection timeout or temporary unavailable, sleep by %s", netErrRetryTime)

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

			data := buf[:num]

			// skip empty udp packets
			if len(bytes.Trim(data, "\x00")) == 0 {
				handler.Log().Tracef("skip empty data: %#v", data)

				continue
			}

			if !streamed {
				handler.addrs.In <- fmt.Sprintf("%v", raddr)
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
		var perr parser.Error
		if errors.As(err, &perr) && perr.Syntax() {
			return true
		}

		var merr sip.MessageError
		if errors.As(err, &merr) && merr.Broken() {
			return true
		}

		return false
	}

	handler.Log().Debug("begin pipe outputs")
	defer handler.Log().Debug("stop pipe outputs")

	for {
		select {
		case <-handler.canceled:
			return
		case <-handler.timer.C():
			raddr := getRemoteAddr()

			if handler.Expiry().IsZero() {
				// handler expiryTime is zero only when TTL = 0 (unlimited handler)
				// so we must not get here with zero expiryTime
				handler.Log().Panic("fires expiry timer with ZERO expiryTime")
			}

			// pass up to the pool
			// pool will make decision to drop out connection or update ttl.
			err := &ConnectionHandlerError{
				ExpireError("connection expired"),
				handler.Key(),
				fmt.Sprintf("%p", handler),
				handler.Connection().Network(),
				fmt.Sprintf("%v", handler.Connection().LocalAddr()),
				raddr,
			}

			handler.Log().Trace("passing up connection expiry error...")

			select {
			case <-handler.canceled:
				return
			case handler.errs <- err:
				handler.Log().Trace("connection expiry error passed up")
			}
		case msg, ok := <-msgs:
			if !ok {
				return
			}

			msg = handler.msgMapper(msg).WithFields(log.Fields{
				"connection_key": handler.Connection().Key(),
				"received_at":    time.Now(),
			})

			logger := handler.Log().WithFields(msg.Fields())

			// add Remote Address
			raddr := getRemoteAddr()
			rhost, rport, _ := net.SplitHostPort(raddr)

			msg.SetDestination(handler.Connection().LocalAddr().String())

			switch msg := msg.(type) {
			case sip.Request:
				// RFC 3261 - 18.2.1
				viaHop, ok := msg.ViaHop()
				if !ok {
					handler.Log().Warn("ignore message without 'Via' header")

					continue
				}

				if rhost != "" {
					viaHop.Params.Add("received", sip.String{Str: rhost})
				}

				// rfc3581
				if viaHop.Params.Has("rport") {
					viaHop.Params.Add("rport", sip.String{Str: rport})
				}

				if !streamed {
					if !viaHop.Params.Has("rport") {
						var port sip.Port
						if viaHop.Port != nil {
							port = *viaHop.Port
						} else {
							port = sip.DefaultPort(handler.Connection().Network())
						}
						raddr = fmt.Sprintf("%s:%d", rhost, port)
					}
				}
				msg.SetSource(raddr)
			case sip.Response:
				// Set Remote Address as response source
				msg.SetSource(raddr)
			}

			logger.Trace("passing up SIP message...")

			// pass up
			select {
			case <-handler.canceled:
				return
			case handler.output <- msg:
				logger.Trace("SIP message passed up")
			}

			if !handler.Expiry().IsZero() {
				handler.expiry = time.Now().Add(handler.ttl)
				handler.timer.Reset(handler.ttl)
			}
		case err, ok := <-errs:
			if !ok {
				return
			}

			raddr := getRemoteAddr()

			if isSyntaxError(err) {
				handler.Log().Warn("ignore error: %s", err)

				continue
			}

			err = &ConnectionHandlerError{
				err,
				handler.Key(),
				fmt.Sprintf("%p", handler),
				handler.Connection().Network(),
				fmt.Sprintf("%v", handler.Connection().LocalAddr()),
				raddr,
			}

			handler.Log().Trace("passing up error...")

			select {
			case <-handler.canceled:
				return
			case handler.errs <- err:
				handler.Log().Trace("error passed up")
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

	handler.cancelOnce.Do(func() {
		close(handler.canceled)

		if err := handler.Connection().Close(); err != nil {
			handler.Log().Errorf("connection close failed: %s", err)
		}

		handler.Log().Debug("connection handler canceled")
	})
}

func (handler *connectionHandler) Done() <-chan struct{} {
	return handler.done
}
