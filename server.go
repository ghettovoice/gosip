package gosip

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/transaction"
	"github.com/ghettovoice/gosip/transport"
)

const (
	defaultHostAddr = "localhost"
)

// RequestHandler is a callback that will be called on the incoming request
// of the certain method
type RequestHandler func(req sip.Request, tx transaction.Tx)

// ResponseHandler is a callback that will be called on each response
type ResponseHandler func(res sip.Response, tx transaction.Tx)

// ServerConfig describes available options
type ServerConfig struct {
	HostAddr string
}

var defaultConfig = &ServerConfig{
	HostAddr: defaultHostAddr,
}

// Server is a SIP server
type Server struct {
	cancelFunc       context.CancelFunc
	tp               transport.Layer
	tx               transaction.Layer
	inShutdown       int32
	hwg              *sync.WaitGroup
	hmu              *sync.RWMutex
	requestHandlers  map[sip.RequestMethod][]RequestHandler
	responseHandlers []ResponseHandler
}

// NewServer creates new instance of SIP server.
func NewServer(config *ServerConfig) *Server {
	var hostAddr string

	if config == nil {
		config = defaultConfig
	}

	if config.HostAddr != "" {
		hostAddr = config.HostAddr
	} else {
		hostAddr = defaultHostAddr
	}

	tp := transport.NewLayer(hostAddr)
	tx := transaction.NewLayer(tp)
	srv := &Server{
		tp:               tp,
		tx:               tx,
		hwg:              new(sync.WaitGroup),
		hmu:              new(sync.RWMutex),
		requestHandlers:  make(map[sip.RequestMethod][]RequestHandler),
		responseHandlers: make([]ResponseHandler, 1),
	}

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	srv.cancelFunc = cancel

	go srv.serve(ctx)

	return srv
}

// ListenAndServe starts serving listeners on the provided address
func (srv *Server) Listen(network string, listenAddr string) error {
	if err := srv.tp.Listen(network, listenAddr); err != nil {
		// return immediately
		return err
	}

	return nil
}

func (srv *Server) serve(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-srv.tx.Messages():
			if msg != nil { // if chan is closed or early exit
				srv.hwg.Add(1)
				go srv.handleMessage(msg)
			}
		case err := <-srv.tx.Errors():
			if err != nil {
				log.Errorf("GoSIP server received transaction error: %s", err)
			}
		case err := <-srv.tp.Errors():
			if err != nil {
				log.Error("GoSIP server received transport error: %s", err.Error())
			}
		}
	}
}

func (srv *Server) handleMessage(txMsg transaction.TxMessage) {
	defer srv.hwg.Done()

	log.Infof("GoSIP server handles incoming message %s", txMsg.Short())
	log.Debugf(txMsg.String())

	switch msg := txMsg.Origin().(type) {
	case sip.Response:
		srv.hmu.RLock()
		handlers := srv.responseHandlers
		srv.hmu.RUnlock()

		for _, handler := range handlers {
			handler(msg, txMsg.Tx())
		}
		// if not handlers, jus ignore
		return
	case sip.Request:
		srv.hmu.RLock()
		handlers, ok := srv.requestHandlers[msg.Method()]
		srv.hmu.RUnlock()

		if ok {
			for _, handler := range handlers {
				handler(msg, txMsg.Tx())
			}
		} else {
			log.Warnf("GoSIP server not found handler registered for the request %s", msg.Short())
			log.Debug(msg.String())

			res := sip.NewResponseFromRequest(msg, 501, "Method Not Supported", "")
			if err := srv.Send(res); err != nil {
				log.Errorf("GoSIP server failed to respond on the unsupported request: %s", err)
			}
		}

		return
	default:
		log.Errorf("GoSIP server received unsupported SIP message type %s", msg.Short())

		return
	}
}

// Send SIP message
func (srv *Server) Send(msg sip.Message) error {
	if srv.shuttingDown() {
		return fmt.Errorf("can not send through shutting down server")
	}

	_, err := srv.tx.Send(msg)

	return err
}

func (srv *Server) shuttingDown() bool {
	return atomic.LoadInt32(&srv.inShutdown) != 0
}

// Shutdown gracefully shutdowns SIP server
func (srv *Server) Shutdown() {
	atomic.AddInt32(&srv.inShutdown, 1)
	defer atomic.AddInt32(&srv.inShutdown, -1)

	srv.cancelFunc()
	// stop transaction layer
	srv.tx.Cancel()
	<-srv.tx.Done()
	// stop transport layer
	srv.tp.Cancel()
	<-srv.tp.Done()
	// wait for handlers
	srv.hwg.Wait()
}

// OnRequest registers new request callback
func (srv *Server) OnRequest(method sip.RequestMethod, handler RequestHandler) error {
	srv.hmu.Lock()
	defer srv.hmu.Unlock()

	handlers, ok := srv.requestHandlers[method]

	if !ok {
		handlers = make([]RequestHandler, 0)
	}

	for _, h := range handlers {
		if &h == &handler {
			return fmt.Errorf("handler already binded to %s method", method)
		}
	}

	srv.requestHandlers[method] = append(srv.requestHandlers[method], handler)

	return nil
}

// OnResponse registers new response callback
func (srv *Server) OnResponse(handler ResponseHandler) error {
	srv.hmu.Lock()
	defer srv.hmu.Unlock()

	for _, h := range srv.responseHandlers {
		if &h == &handler {
			return fmt.Errorf("handler already binded to response")
		}
	}

	srv.responseHandlers = append(srv.responseHandlers, handler)

	return nil
}
