package gosip

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/transaction"
	"github.com/ghettovoice/gosip/transport"
	"github.com/ghettovoice/gosip/util"
)

// RequestHandler is a callback that will be called on the incoming request
// of the certain method
// tx argument can be nil for 2xx ACK request
type RequestHandler func(req sip.Request, tx sip.ServerTransaction)

// ServerConfig describes available options
type ServerConfig struct {
	// Public IP address or domain name, if empty auto resolved IP will be used.
	Host string
	// Dns is an address of the public DNS server to use in SRV lookup.
	Dns        string
	Extensions []string
}

// Server is a SIP server
type Server struct {
	tp              transport.Layer
	tx              transaction.Layer
	host            string
	ip              net.IP
	inShutdown      int32
	hwg             *sync.WaitGroup
	hmu             *sync.RWMutex
	requestHandlers map[sip.RequestMethod][]RequestHandler
	extensions      []string
}

// NewServer creates new instance of SIP server.
func NewServer(config *ServerConfig) *Server {
	if config == nil {
		config = &ServerConfig{}
	}

	var host string
	var ip net.IP
	if config.Host != "" {
		host = config.Host
		if addr, err := net.ResolveIPAddr("ip", host); err == nil {
			ip = addr.IP
		} else {
			log.Fatalf("failed to resolve host ip: %s", err)
		}
	} else {
		if v, err := util.ResolveSelfIP(); err == nil {
			ip = v
			host = v.String()
		} else {
			log.Fatalf("failed to resolve host ip: %s", err)
		}
	}

	var dnsResolver *net.Resolver
	if config.Dns != "" {
		dnsResolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{}
				return d.DialContext(ctx, "udp", config.Dns)
			},
		}
	} else {
		dnsResolver = net.DefaultResolver
	}

	var extensions []string
	if config.Extensions != nil {
		extensions = config.Extensions
	}

	tp := transport.NewLayer(ip, dnsResolver)
	tx := transaction.NewLayer(tp)
	srv := &Server{
		tp:              tp,
		tx:              tx,
		host:            host,
		ip:              ip,
		hwg:             new(sync.WaitGroup),
		hmu:             new(sync.RWMutex),
		requestHandlers: make(map[sip.RequestMethod][]RequestHandler),
		extensions:      extensions,
	}
	// setup default handlers
	_ = srv.OnRequest(sip.ACK, func(req sip.Request, tx sip.ServerTransaction) {
		log.Infof("GoSIP server received ACK request: %s", req.Short())
	})
	_ = srv.OnRequest(sip.CANCEL, func(req sip.Request, tx sip.ServerTransaction) {
		response := sip.NewResponseFromRequest(tx.Origin(), 481, "Transaction Does Not Exist", "")
		if _, err := srv.Respond(response); err != nil {
			log.Errorf("failed to send response: %s", err)
		}
	})

	go srv.serve()

	return srv
}

// ListenAndServe starts serving listeners on the provided address
func (srv *Server) Listen(network string, listenAddr string) error {
	return srv.tp.Listen(network, listenAddr)
}

func (srv *Server) serve() {
	defer srv.Shutdown()

	for {
		select {
		case tx, ok := <-srv.tx.Requests():
			if !ok {
				return
			}
			if tx != nil { // if chan is closed or early exit
				srv.hwg.Add(1)
				go srv.handleRequest(tx.Origin(), tx)
			}
		case ack, ok := <-srv.tx.Acks():
			if !ok {
				return
			}
			if ack != nil {
				srv.hwg.Add(1)
				go srv.handleRequest(ack, nil)
			}
		case res, ok := <-srv.tx.Responses():
			if !ok {
				return
			}
			if res != nil {
				log.Warnf("GoSIP server received not matched response: %s", res.Short())
				log.Debugf("message:\n%s", res.String())
			}
		case err, ok := <-srv.tx.Errors():
			if !ok {
				return
			}
			if err != nil {
				log.Errorf("GoSIP server received transaction error: %s", err)
			}
		case err, ok := <-srv.tp.Errors():
			if !ok {
				return
			}
			if err != nil {
				log.Error("GoSIP server received transport error: %s", err)
			}
		}
	}
}

func (srv *Server) handleRequest(req sip.Request, tx sip.ServerTransaction) {
	defer srv.hwg.Done()

	log.Infof("GoSIP server handles incoming message %s", req.Short())
	log.Debugf("message:\n%s", req)

	var handlers []RequestHandler
	srv.hmu.RLock()
	if value, ok := srv.requestHandlers[req.Method()]; ok {
		handlers = value[:]
	}
	srv.hmu.RUnlock()

	if len(handlers) > 0 {
		for _, handler := range handlers {
			go handler(req, tx)
		}
	} else {
		log.Warnf("GoSIP server not found handler registered for the request %s", req.Short())

		res := sip.NewResponseFromRequest(req, 405, "Method Not Allowed", "")
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
				onComplete(nil, &Error{
					Message: fmt.Sprintf("request '%s' canceled", request.Short()),
					Code:    RequestCanceled,
				})
				return
			case err, ok := <-tx.Errors():
				if !ok {
					// todo
					return
				}

				if err == nil {
					err = errors.New("unknown transaction error")
				}

				onComplete(nil, fmt.Errorf("transaction '%s' error: %s", tx, err))
				return
			case response, ok := <-tx.Responses():
				if !ok {
					// todo
					return
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
	if viaHop, ok := req.ViaHop(); ok {
		if viaHop.Params == nil {
			viaHop.Params = sip.NewParams()
		}
		if !viaHop.Params.Has("branch") {
			viaHop.Params.Add("branch", sip.String{Str: sip.GenerateBranch()})
		}
	} else {
		viaHop = &sip.ViaHop{
			ProtocolName:    "SIP",
			ProtocolVersion: "2.0",
			Params: sip.NewParams().
				Add("branch", sip.String{Str: sip.GenerateBranch()}),
		}

		req.PrependHeaderAfter(sip.ViaHeader{
			viaHop,
		}, "Route")
	}

	srv.appendAutoHeaders(req)

	return req
}

func (srv *Server) Respond(res sip.Response) (sip.ServerTransaction, error) {
	if srv.shuttingDown() {
		return nil, fmt.Errorf("can not send through stopped server")
	}

	return srv.tx.Respond(srv.prepareResponse(res))
}

func (srv *Server) RespondOnRequest(
	request sip.Request,
	status sip.StatusCode,
	reason, body string,
	headers []sip.Header,
) (sip.ServerTransaction, error) {
	response := sip.NewResponseFromRequest(request, status, reason, body)
	for _, header := range headers {
		response.AppendHeader(header)
	}

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

	switch m := msg.(type) {
	case sip.Request:
		msg = srv.prepareRequest(m)
	case sip.Response:
		msg = srv.prepareResponse(m)
	}

	return srv.tp.Send(msg)
}

func (srv *Server) prepareResponse(res sip.Response) sip.Response {
	srv.appendAutoHeaders(res)

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

func (srv *Server) appendAutoHeaders(msg sip.Message) {
	autoAppendMethods := map[sip.RequestMethod]bool{
		sip.INVITE:   true,
		sip.REGISTER: true,
		sip.OPTIONS:  true,
		sip.REFER:    true,
		sip.NOTIFY:   true,
	}

	var msgMethod sip.RequestMethod
	switch m := msg.(type) {
	case sip.Request:
		msgMethod = m.Method()
	case sip.Response:
		if cseq, ok := m.CSeq(); ok && !m.IsProvisional() {
			msgMethod = cseq.MethodName
		}
	}
	if len(msgMethod) > 0 {
		if _, ok := autoAppendMethods[msgMethod]; ok {
			hdrs := msg.GetHeaders("Allow")
			if len(hdrs) == 0 {
				allow := make(sip.AllowHeader, 0)
				for _, method := range srv.getAllowedMethods() {
					allow = append(allow, method)
				}

				msg.AppendHeader(allow)
			}

			hdrs = msg.GetHeaders("Supported")
			if len(hdrs) == 0 {
				msg.AppendHeader(&sip.SupportedHeader{
					Options: srv.extensions,
				})
			}
		}
	}

	if hdrs := msg.GetHeaders("User-Agent"); len(hdrs) == 0 {
		userAgent := sip.UserAgentHeader("GoSIP")
		msg.AppendHeader(&userAgent)
	}
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
