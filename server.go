package gosip

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/transaction"
	"github.com/ghettovoice/gosip/transport"
	"github.com/ghettovoice/gosip/util"

	"github.com/tevino/abool"
)

// RequestHandler is a callback that will be called on the incoming request
// of the certain method
// tx argument can be nil for 2xx ACK request
type RequestHandler func(req sip.Request, tx sip.ServerTransaction)

type Server interface {
	Shutdown()

	Listen(network, addr string) error
	Send(msg sip.Message) error

	Request(req sip.Request) (sip.ClientTransaction, error)
	RequestWithContext(
		ctx context.Context,
		request sip.Request,
		authorizer sip.Authorizer,
	) (sip.Response, error)
	OnRequest(method sip.RequestMethod, handler RequestHandler) error

	Respond(res sip.Response) (sip.ServerTransaction, error)
	RespondOnRequest(
		request sip.Request,
		status sip.StatusCode,
		reason, body string,
		headers []sip.Header,
	) (sip.ServerTransaction, error)
}

type TransportLayerFactory func(
	ip net.IP,
	dnsResolver *net.Resolver,
	msgMapper sip.MessageMapper,
	logger log.Logger,
) transport.Layer

type TransactionLayerFactory func(tpl transport.Layer, logger log.Logger) transaction.Layer

// ServerConfig describes available options
type ServerConfig struct {
	// Public IP address or domain name, if empty auto resolved IP will be used.
	Host string
	// Dns is an address of the public DNS server to use in SRV lookup.
	Dns        string
	Extensions []string
	MsgMapper  sip.MessageMapper
}

// Server is a SIP server
type server struct {
	running         abool.AtomicBool
	tp              transport.Layer
	tx              transaction.Layer
	host            string
	ip              net.IP
	hwg             *sync.WaitGroup
	hmu             *sync.RWMutex
	requestHandlers map[sip.RequestMethod]RequestHandler
	extensions      []string
	invites         map[transaction.TxKey]sip.Request
	invitesLock     *sync.RWMutex

	log log.Logger
}

// NewServer creates new instance of SIP server.
func NewServer(
	config ServerConfig,
	tpFactory TransportLayerFactory,
	txFactory TransactionLayerFactory,
	logger log.Logger,
) Server {
	if tpFactory == nil {
		tpFactory = transport.NewLayer
	}
	if txFactory == nil {
		txFactory = transaction.NewLayer
	}

	logger = logger.WithPrefix("gosip.Server")

	var host string
	var ip net.IP
	if config.Host != "" {
		host = config.Host
		if addr, err := net.ResolveIPAddr("ip", host); err == nil {
			ip = addr.IP
		} else {
			logger.Panicf("resolve host IP failed: %s", err)
		}
	} else {
		if v, err := util.ResolveSelfIP(); err == nil {
			ip = v
			host = v.String()
		} else {
			logger.Panicf("resolve host IP failed: %s", err)
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

	srv := &server{
		host:            host,
		ip:              ip,
		hwg:             new(sync.WaitGroup),
		hmu:             new(sync.RWMutex),
		requestHandlers: make(map[sip.RequestMethod]RequestHandler),
		extensions:      extensions,
		invites:         make(map[transaction.TxKey]sip.Request),
		invitesLock:     new(sync.RWMutex),
	}
	srv.log = logger.WithFields(log.Fields{
		"sip_server_ptr": fmt.Sprintf("%p", srv),
	})
	srv.tp = tpFactory(ip, dnsResolver, config.MsgMapper, srv.Log())
	srv.tx = txFactory(srv.tp, log.AddFieldsFrom(srv.Log(), srv.tp))

	srv.running.Set()
	go srv.serve()

	return srv
}

func (srv *server) Log() log.Logger {
	return srv.log
}

// ListenAndServe starts serving listeners on the provided address
func (srv *server) Listen(network string, listenAddr string) error {
	return srv.tp.Listen(network, listenAddr)
}

func (srv *server) serve() {
	defer srv.Shutdown()

	for {
		select {
		case tx, ok := <-srv.tx.Requests():
			if !ok {
				return
			}
			srv.hwg.Add(1)
			go srv.handleRequest(tx.Origin(), tx)
		case ack, ok := <-srv.tx.Acks():
			if !ok {
				return
			}
			srv.hwg.Add(1)
			go srv.handleRequest(ack, nil)
		case response, ok := <-srv.tx.Responses():
			if !ok {
				return
			}

			logger := srv.Log().WithFields(map[string]interface{}{
				"sip_response": response.Short(),
			})

			logger.Warn("received not matched response")

			if key, err := transaction.MakeClientTxKey(response); err == nil {
				srv.invitesLock.RLock()
				inviteRequest, ok := srv.invites[key]
				srv.invitesLock.RUnlock()
				if ok {
					go srv.ackInviteRequest(inviteRequest, response)
				}
			}
		case err, ok := <-srv.tx.Errors():
			if !ok {
				return
			}

			srv.Log().Errorf("received SIP transaction error: %s", err)
		case err, ok := <-srv.tp.Errors():
			if !ok {
				return
			}

			srv.Log().Errorf("received SIP transport error: %s", err)
		}
	}
}

func (srv *server) handleRequest(req sip.Request, tx sip.ServerTransaction) {
	defer srv.hwg.Done()

	logger := srv.Log().WithFields(req.Fields())
	logger.Info("routing incoming SIP request...")

	srv.hmu.RLock()
	handler, ok := srv.requestHandlers[req.Method()]
	srv.hmu.RUnlock()

	if !ok {
		logger.Warnf("SIP request handler not found")

		res := sip.NewResponseFromRequest("", req, 405, "Method Not Allowed", "")
		if _, err := srv.Respond(res); err != nil {
			logger.Errorf("respond '405 Method Not Allowed' failed: %s", err)
		}

		return
	}

	go handler(req, tx)
}

// Send SIP message
func (srv *server) Request(req sip.Request) (sip.ClientTransaction, error) {
	if !srv.running.IsSet() {
		return nil, fmt.Errorf("can not send through stopped server")
	}

	return srv.tx.Request(srv.prepareRequest(req))
}

func (srv *server) RequestWithContext(
	ctx context.Context,
	request sip.Request,
	authorizer sip.Authorizer,
) (sip.Response, error) {
	tx, err := srv.Request(sip.CopyRequest(request))
	if err != nil {
		return nil, err
	}

	responses := make(chan sip.Response)
	errs := make(chan error)
	go func() {
		var lastResponse sip.Response

		previousResponses := make([]sip.Response, 0)
		previousResponsesStatuses := make(map[sip.StatusCode]bool)

		for {
			select {
			case <-ctx.Done():
				if lastResponse != nil && lastResponse.IsProvisional() {
					srv.cancelRequest(request, lastResponse)
				}
				if lastResponse != nil {
					lastResponse.SetPrevious(previousResponses)
				}
				errs <- sip.NewRequestError(487, "Request Terminated", request, lastResponse)
				// pull out later possible transaction responses and errors
				go func() {
					for {
						select {
						case <-tx.Done():
							return
						case <-tx.Errors():
						case <-tx.Responses():
						}
					}
				}()
				return
			case err, ok := <-tx.Errors():
				if !ok {
					if lastResponse != nil {
						lastResponse.SetPrevious(previousResponses)
					}
					errs <- sip.NewRequestError(487, "Request Terminated", request, lastResponse)
					return
				}
				errs <- err
				return
			case response, ok := <-tx.Responses():
				if !ok {
					if lastResponse != nil {
						lastResponse.SetPrevious(previousResponses)
					}
					errs <- sip.NewRequestError(487, "Request Terminated", request, lastResponse)
					return
				}

				response = sip.CopyResponse(response)
				lastResponse = response

				if response.IsProvisional() {
					if _, ok := previousResponsesStatuses[response.StatusCode()]; !ok {
						previousResponses = append(previousResponses, response)
					}

					continue
				}

				// success
				if response.IsSuccess() {
					response.SetPrevious(previousResponses)

					if request.IsInvite() {
						srv.ackInviteRequest(request, response)
						srv.rememberInviteRequest(request)
						go func() {
							for response := range tx.Responses() {
								srv.ackInviteRequest(request, response)
							}
						}()
					}

					responses <- response

					return
				}

				// unauth request
				if (response.StatusCode() == 401 || response.StatusCode() == 407) && authorizer != nil {
					if err := authorizer.AuthorizeRequest(request, response); err != nil {
						errs <- err

						return
					}

					if response, err := srv.RequestWithContext(ctx, request, nil); err == nil {
						responses <- response
					} else {
						errs <- err
					}

					return
				}

				// failed request
				if lastResponse != nil {
					lastResponse.SetPrevious(previousResponses)
				}
				errs <- sip.NewRequestError(uint(response.StatusCode()), response.Reason(), request, lastResponse)

				return
			}
		}
	}()

	select {
	case err := <-errs:
		return nil, err
	case response := <-responses:
		return response, nil
	}
}

func (srv *server) rememberInviteRequest(request sip.Request) {
	if key, err := transaction.MakeClientTxKey(request); err == nil {
		srv.invitesLock.Lock()
		srv.invites[key] = request
		srv.invitesLock.Unlock()

		time.AfterFunc(time.Minute, func() {
			srv.invitesLock.Lock()
			delete(srv.invites, key)
			srv.invitesLock.Unlock()
		})
	} else {
		srv.Log().WithFields(map[string]interface{}{
			"sip_request": request.Short(),
		}).Errorf("remember of the request failed: %s", err)
	}
}

func (srv *server) ackInviteRequest(request sip.Request, response sip.Response) {
	ackRequest := sip.NewAckRequest("", request, response)
	if err := srv.Send(ackRequest); err != nil {
		srv.Log().WithFields(map[string]interface{}{
			"invite_request":  request.Short(),
			"invite_response": response.Short(),
			"ack_request":     ackRequest.Short(),
		}).Errorf("send ACK request failed: %s", err)
	}
}

func (srv *server) cancelRequest(request sip.Request, response sip.Response) {
	cancelRequest := sip.NewCancelRequest("", request)
	if err := srv.Send(cancelRequest); err != nil {
		srv.Log().WithFields(map[string]interface{}{
			"invite_request":  request.Short(),
			"invite_response": response.Short(),
			"cancel_request":  cancelRequest.Short(),
		}).Errorf("send CANCEL request failed: %s", err)
	}
}

func (srv *server) prepareRequest(req sip.Request) sip.Request {
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

func (srv *server) Respond(res sip.Response) (sip.ServerTransaction, error) {
	if !srv.running.IsSet() {
		return nil, fmt.Errorf("can not send through stopped server")
	}

	return srv.tx.Respond(srv.prepareResponse(res))
}

func (srv *server) RespondOnRequest(
	request sip.Request,
	status sip.StatusCode,
	reason, body string,
	headers []sip.Header,
) (sip.ServerTransaction, error) {
	response := sip.NewResponseFromRequest("", request, status, reason, body)
	for _, header := range headers {
		response.AppendHeader(header)
	}

	tx, err := srv.Respond(response)
	if err != nil {
		return nil, fmt.Errorf("respond '%d %s' failed: %w", response.StatusCode(), response.Reason(), err)
	}

	return tx, nil
}

func (srv *server) Send(msg sip.Message) error {
	if !srv.running.IsSet() {
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

func (srv *server) prepareResponse(res sip.Response) sip.Response {
	srv.appendAutoHeaders(res)

	return res
}

// Shutdown gracefully shutdowns SIP server
func (srv *server) Shutdown() {
	if !srv.running.IsSet() {
		return
	}
	srv.running.UnSet()
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
func (srv *server) OnRequest(method sip.RequestMethod, handler RequestHandler) error {
	srv.hmu.Lock()
	srv.requestHandlers[method] = handler
	srv.hmu.Unlock()

	return nil
}

func (srv *server) appendAutoHeaders(msg sip.Message) {
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

func (srv *server) getAllowedMethods() []sip.RequestMethod {
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
