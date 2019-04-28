package gosip

import (
	"context"
	"errors"
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
type RequestHandler func(tx sip.ServerTransaction)

// ServerConfig describes available options
type ServerConfig struct {
	HostAddr   string
	Extensions []string
}

var defaultConfig = &ServerConfig{
	HostAddr:   defaultHostAddr,
	Extensions: make([]string, 0),
}

// Server is a SIP server
type Server struct {
	tp              transport.Layer
	tx              transaction.Layer
	inShutdown      int32
	hwg             *sync.WaitGroup
	hmu             *sync.RWMutex
	requestHandlers map[sip.RequestMethod][]RequestHandler
	extensions      []string
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

	ctx := context.Background()
	tp := transport.NewLayer(hostAddr)
	tx := transaction.NewLayer(tp)
	srv := &Server{
		tp:              tp,
		tx:              tx,
		hwg:             new(sync.WaitGroup),
		hmu:             new(sync.RWMutex),
		requestHandlers: make(map[sip.RequestMethod][]RequestHandler),
		extensions:      config.Extensions,
	}
	// setup default handlers
	_ = srv.OnRequest(sip.ACK, func(tx sip.ServerTransaction) {
		log.Infof("GoSIP server received ACK request: %s", tx.Origin().Short())
	})
	_ = srv.OnRequest(sip.CANCEL, func(tx sip.ServerTransaction) {
		response := sip.NewResponseFromRequest(tx.Origin(), 481, "Transaction Does Not Exist", "")
		if _, err := srv.Respond(response); err != nil {
			log.Errorf("failed to send response: %s", err)
		}
	})

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
		case tx := <-srv.tx.Requests():
			if tx != nil { // if chan is closed or early exit
				srv.hwg.Add(1)
				go srv.handleRequest(ctx, tx)
			}
		case res := <-srv.tx.Responses():
			if res != nil {
				log.Warnf("GoSIP server received not matched response: %s", res.Short())
				log.Debugf("message:\n%s", res.String())
			}
		case err := <-srv.tx.Errors():
			if err != nil {
				log.Errorf("GoSIP server received transaction error: %s", err)
			}
		case err := <-srv.tp.Errors():
			if err != nil {
				log.Error("GoSIP server received transport error: %s", err)
			}
		}
	}
}

func (srv *Server) handleRequest(ctx context.Context, tx sip.ServerTransaction) {
	defer srv.hwg.Done()

	log.Infof("GoSIP server handles incoming message %s", tx.Origin().Short())
	log.Debugf("message:\n%s", tx)

	var handlers []RequestHandler
	srv.hmu.RLock()
	if value, ok := srv.requestHandlers[tx.Origin().Method()]; ok {
		handlers = value[:]
	}
	srv.hmu.RUnlock()

	if len(handlers) > 0 {
		for _, handler := range handlers {
			go handler(tx)
		}
	} else {
		log.Warnf("GoSIP server not found handler registered for the request %s", tx.Origin().Short())

		res := sip.NewResponseFromRequest(tx.Origin(), 405, "Method Not Allowed", "")
		if _, err := srv.Respond(res); err != nil {
			log.Errorf("GoSIP server failed to respond on the unsupported request: %s", err)
		}
	}
}

// Send SIP message
func (srv *Server) Request(req sip.Request) (sip.ClientTransaction, error) {
	if srv.shuttingDown() {
		return nil, fmt.Errorf("can not send through stopped server")
	}

	return srv.tx.Request(srv.prepareRequest(req))
}

func (srv *Server) RequestAsync(
	ctx context.Context,
	request sip.Request,
	onComplete func(response sip.Response, err error) bool,
) error {
	tx, err := srv.Request(request)
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				onComplete(nil, fmt.Errorf("request '%s' canceled", request.Short()))
				return
			case <-tx.Done():
				onComplete(nil, fmt.Errorf("transaction '%s' terminated", tx))
				return
			case err, ok := <-tx.Errors():
				if !ok {
					// todo
					return
				}

				if err == nil {
					err = errors.New("unknown transaction error")
				}

				onComplete(nil, fmt.Errorf("trasaction '%s' error: %s", tx, err))
				return
			case response, ok := <-tx.Responses():
				if !ok {
					// todo
					return
				}

				if response.IsProvisional() {
					continue
				}

				if onComplete(response, nil) {
					continue
				} else {
					return
				}
			}
		}
	}()

	return nil
}

func (srv *Server) prepareRequest(req sip.Request) sip.Request {
	autoAppendMethods := map[sip.RequestMethod]bool{
		sip.INVITE:   true,
		sip.REGISTER: true,
		sip.REFER:    true,
		sip.NOTIFY:   true,
	}
	if _, ok := autoAppendMethods[req.Method()]; ok {
		hdrs := req.GetHeaders("Allow")
		if len(hdrs) == 0 {
			allow := make(sip.AllowHeader, 0)
			for _, method := range srv.getAllowedMethods() {
				allow = append(allow, method)
			}
			req.AppendHeader(allow)
		}

		hdrs = req.GetHeaders("Supported")
		if len(hdrs) == 0 {
			req.AppendHeader(&sip.SupportedHeader{
				Options: srv.extensions,
			})
		}
	}

	hdrs := req.GetHeaders("User-Agent")
	if len(hdrs) == 0 {
		userAgent := sip.UserAgentHeader("GoSIP")
		req.AppendHeader(&userAgent)
	}

	return req
}

func (srv *Server) Respond(res sip.Response) (sip.ServerTransaction, error) {
	if srv.shuttingDown() {
		return nil, fmt.Errorf("can not send through stopped server")
	}

	return srv.tx.Respond(srv.prepareResponse(res))
}

func (srv *Server) RespondOnRequest(request sip.Request, status sip.StatusCode, reason, body string) (sip.ServerTransaction, error) {
	response := sip.NewResponseFromRequest(request, status, reason, body)
	tx, err := srv.Respond(response)
	if err != nil {
		return nil, fmt.Errorf("failed to respond on request '%s': %s", request.Short(), err)
	}

	return tx, nil
}

func (srv *Server) Send(msg sip.Message) error {
	if srv.shuttingDown() {
		return fmt.Errorf("can not send through stopped server")
	}

	return srv.tp.Send(msg)
}

func (srv *Server) prepareResponse(res sip.Response) sip.Response {
	autoAppendMethods := map[sip.RequestMethod]bool{
		sip.OPTIONS: true,
	}

	if cseq, ok := res.CSeq(); ok {
		if _, ok := autoAppendMethods[cseq.MethodName]; ok {
			hdrs := res.GetHeaders("Allow")
			if len(hdrs) == 0 {
				allow := make(sip.AllowHeader, 0)
				for _, method := range srv.getAllowedMethods() {
					allow = append(allow, method)
				}

				res.AppendHeader(allow)
			}

			hdrs = res.GetHeaders("Supported")
			if len(hdrs) == 0 {
				res.AppendHeader(&sip.SupportedHeader{
					Options: srv.extensions,
				})
			}
		}
	}

	return res
}

func (srv *Server) shuttingDown() bool {
	return atomic.LoadInt32(&srv.inShutdown) != 0
}

// Shutdown gracefully shutdowns SIP server
func (srv *Server) Shutdown() {
	if srv.shuttingDown() {
		return
	}

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
	var handlers []RequestHandler
	srv.hmu.RLock()
	if value, ok := srv.requestHandlers[method]; ok {
		handlers = value[:]
	} else {
		handlers = make([]RequestHandler, 0)
	}
	srv.hmu.RUnlock()

	for _, h := range handlers {
		if &h == &handler {
			return fmt.Errorf("handler already binded to %s method", method)
		}
	}

	srv.hmu.Lock()
	srv.requestHandlers[method] = append(srv.requestHandlers[method], handler)
	srv.hmu.Unlock()

	return nil
}

func (srv *Server) getAllowedMethods() []sip.RequestMethod {
	methods := []sip.RequestMethod{
		sip.INVITE,
		sip.ACK,
		sip.CANCEL,
	}
	added := map[sip.RequestMethod]bool{
		sip.INVITE: true,
		sip.ACK:    true,
		sip.CANCEL: true,
	}

	srv.hmu.RLock()
	for method := range srv.requestHandlers {
		if _, ok := added[method]; !ok {
			methods = append(methods, method)
		}
	}
	srv.hmu.RUnlock()

	return methods
}
