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
	defaultListenAddr = "127.0.0.1:5060"
	defaultHostAddr   = "127.0.0.1"
)

var (
	protocols = []string{"udp", "tcp"}
)

// RequestHandler is a callback that will be called on the incoming request
// of the certain method
type RequestHandler func(req sip.Request)
type requestHandlerCollection []RequestHandler

// ResponseHandler is a callback that will be called on each response
type ResponseHandler func(res sip.Response)
type responseHandlerCollection []ResponseHandler

// ServerConfig describes available options
type ServerConfig struct {
	HostAddr string
}

// Server is a SIP server
type Server struct {
	cancelFunc       context.CancelFunc
	tp               transport.Layer
	tx               transaction.Layer
	hwg              *sync.WaitGroup
	inShutdown       int32
	requestHandlers  map[sip.RequestMethod]requestHandlerCollection
	responseHandlers responseHandlerCollection
}

// NewServer creates new instance of SIP server.
func NewServer(config ServerConfig) *Server {
	var hostAddr string
	if config.HostAddr != "" {
		hostAddr = config.HostAddr
	} else {
		hostAddr = defaultHostAddr
	}

	tp := transport.NewLayer(hostAddr)
	tx := transaction.NewLayer(tp)
	srv := &Server{
		hwg:              new(sync.WaitGroup),
		tp:               tp,
		tx:               tx,
		requestHandlers:  make(map[sip.RequestMethod]requestHandlerCollection),
		responseHandlers: make(responseHandlerCollection, 1),
	}

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	srv.cancelFunc = cancel

	go srv.serve(ctx)

	return srv
}

// ListenAndServe starts serving listeners on the provided address
func (srv *Server) Listen(listenAddr string) error {
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}

	for _, protocol := range protocols {
		if err := srv.tp.Listen(protocol, listenAddr); err != nil {
			// return immediately
			return err
		}
	}

	return nil
}

func (srv *Server) serve(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-srv.tx.Messages():
			srv.hwg.Add(1)
			go srv.handleMessage(msg)
		case err := <-srv.tx.Errors():
			log.Error(err.Error())
		case err := <-srv.tp.Errors():
			log.Error(err.Error())
		}
	}
}

func (srv *Server) handleMessage(message sip.Message) {
	defer srv.hwg.Done()

	var msg sip.Message

	if txMsg, ok := message.(transaction.TxMessage); ok {
		msg = txMsg.Origin()
	} else {
		msg = message
	}

	switch m := msg.(type) {
	case sip.Response:
		for _, handler := range srv.responseHandlers {
			handler(m)
		}
	case sip.Request:
		if handlers, ok := srv.requestHandlers[m.Method()]; ok {
			for _, handler := range handlers {
				handler(m)
			}
		}
	default:
		log.Errorf("unsupported SIP message type %s", msg.Short())
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
	srv.cancelFunc()

	atomic.AddInt32(&srv.inShutdown, 1)
	defer atomic.AddInt32(&srv.inShutdown, -1)
	// canceling transport layer causes canceling
	// of all listeners, pool, transactions and etc
	srv.tp.Cancel()
	// wait transaction layer because it is the top layer
	// in stack
	<-srv.tx.Done()
	// wait for handlers
	srv.hwg.Wait()
}

// OnRequest registers new request callback
func (srv *Server) OnRequest(method sip.RequestMethod, handler RequestHandler) error {
	handlers, ok := srv.requestHandlers[method]

	if !ok {
		handlers = make(requestHandlerCollection, 0)
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
	for _, h := range srv.responseHandlers {
		if &h == &handler {
			return fmt.Errorf("handler already binded to response")
		}
	}

	srv.responseHandlers = append(srv.responseHandlers, handler)

	return nil
}
