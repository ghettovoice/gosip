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
type RequestHandler func(req sip.Request)

// ServerConfig describes available options
type ServerConfig struct {
	HostAddr string
}

var defaultConfig = &ServerConfig{
	HostAddr: defaultHostAddr,
}

// Server is a SIP server
type Server struct {
	cancelFunc      context.CancelFunc
	tp              transport.Layer
	tx              transaction.Layer
	inShutdown      int32
	hwg             *sync.WaitGroup
	hmu             *sync.RWMutex
	requestHandlers map[sip.RequestMethod][]RequestHandler
}

// NewServer creates new instance of SIP server.
func NewServer(ctx context.Context, config *ServerConfig) *Server {
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
		tp:              tp,
		tx:              tx,
		hwg:             new(sync.WaitGroup),
		hmu:             new(sync.RWMutex),
		requestHandlers: make(map[sip.RequestMethod][]RequestHandler),
	}

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
	defer srv.Shutdown()

	for {
		select {
		case <-ctx.Done():
			return
		case req := <-srv.tx.Requests():
			if req != nil { // if chan is closed or early exit
				srv.hwg.Add(1)
				go srv.handleRequest(req)
			}
		case res := <-srv.tx.Responses():
			if res != nil {
				log.Warnf("GoSIP server received not matched response: %s", res.Short())
				log.Debug(res.String())
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

func (srv *Server) handleRequest(req sip.Request) {
	defer srv.hwg.Done()

	log.Infof("GoSIP server handles incoming message %s", req.Short())
	log.Debugf(req.String())

	srv.hmu.RLock()
	handlers, ok := srv.requestHandlers[req.Method()]
	srv.hmu.RUnlock()

	if ok {
		for _, handler := range handlers {
			handler(req)
		}
	} else if req.IsAck() {
		// nothing to do, just ignore it
	} else {
		log.Warnf("GoSIP server not found handler registered for the request %s", req.Short())
		log.Debug(req.String())

		res := sip.NewResponseFromRequest(req, 501, "Method Not Supported", "")
		if _, err := srv.Respond(res); err != nil {
			log.Errorf("GoSIP server failed to respond on the unsupported request: %s", err)
		}
	}

	return
}

// Send SIP message
func (srv *Server) Request(req sip.Request) (<-chan sip.Response, error) {
	if srv.shuttingDown() {
		return nil, fmt.Errorf("can not send through shutting down server")
	}

	return srv.tx.Request(req)
}

func (srv *Server) Respond(res sip.Response) (<-chan sip.Request, error) {
	if srv.shuttingDown() {
		return nil, fmt.Errorf("can not send through shutting down server")
	}

	return srv.tx.Respond(res)
}

func (srv *Server) shuttingDown() bool {
	return atomic.LoadInt32(&srv.inShutdown) != 0
}

// Shutdown gracefully shutdowns SIP server
func (srv *Server) Shutdown() {
	atomic.AddInt32(&srv.inShutdown, 1)
	defer atomic.AddInt32(&srv.inShutdown, -1)
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
