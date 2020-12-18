package transport

import (
	"context"
	ntls "crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
	"golang.org/x/time/rate"
	"nhooyr.io/websocket"
)

var (
	subprotocol = "sip"
)

type wsListener struct {
	listener net.Listener
	log      log.Logger
	server   *http.Server
}

func NewTlsListener(listener net.Listener, address string, options *Options) *wsListener {
	l := &wsListener{listener: listener}
	l.server = &http.Server{
		Handler:      l,
		ReadTimeout:  time.Second * 10,
		WriteTimeout: time.Second * 10,
	}

	tcpl, err := net.Listen("tcp", address)
	if err != nil {
		l.log.Error(err)
	}
	l.log.Infof("listening on http://%v", tcpl.Addr())

	errc := make(chan error, 1)
	go func() {
		errc <- l.server.Serve(tcpl)
	}()
	return l
}

func (l *wsListener) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{subprotocol},
	})
	if err != nil {
		l.log.Errorf("%v", err)
		return
	}
	defer c.Close(websocket.StatusInternalError, "the sky is falling")

	if c.Subprotocol() != subprotocol {
		c.Close(websocket.StatusPolicyViolation, "client must speak the echo subprotocol")
		return
	}

	li := rate.NewLimiter(rate.Every(time.Millisecond*100), 10)
	for {
		err = echo(r.Context(), c, li)
		if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
			return
		}
		if err != nil {
			l.log.Errorf("failed to echo with %v: %v", r.RemoteAddr, err)
			return
		}
	}
}

// echo reads from the WebSocket connection and then writes
// the received message back to it.
// The entire function has 10s to complete.
func echo(ctx context.Context, c *websocket.Conn, l *rate.Limiter) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	err := l.Wait(ctx)
	if err != nil {
		return err
	}

	typ, r, err := c.Reader(ctx)
	if err != nil {
		return err
	}

	w, err := c.Writer(ctx, typ)
	if err != nil {
		return err
	}

	_, err = io.Copy(w, r)
	if err != nil {
		return fmt.Errorf("failed to io.Copy: %w", err)
	}

	err = w.Close()
	return err
}

func (l *wsListener) Accept() (net.Conn, error) {

	return l.listener.Accept()
}

func (l *wsListener) Close() error {
	return l.listener.Close()
}

func (l *wsListener) Addr() net.Addr {
	return &WsAddr{Addr: l.listener.Addr()}
}

type WsAddr struct {
	Addr net.Addr
}

func (a *WsAddr) Network() string {
	return "tls"
}

func (a *WsAddr) String() string {
	return a.Addr.String()
}

type wssProtocol struct {
	protocol
	listeners   ListenerPool
	connections ConnectionPool
	conns       chan Connection
}

func NewWssProtocol(output chan<- sip.Message, errs chan<- error, cancel <-chan struct{}, msgMapper sip.MessageMapper, logger log.Logger) Protocol {
	wss := new(wssProtocol)
	wss.network = "wss"
	wss.reliable = true
	wss.streamed = true
	wss.conns = make(chan Connection)

	wss.log = logger.
		WithPrefix("transport.Protocol").
		WithFields(log.Fields{
			"protocol_ptr": fmt.Sprintf("%p", wss),
		})

	//TODO: add separate errs chan to listen errors from pool for reconnection?
	wss.listeners = NewListenerPool(wss.conns, errs, cancel, wss.Log())
	wss.connections = NewConnectionPool(output, errs, cancel, msgMapper, wss.Log())

	//pipe listener and connection pools
	go wss.pipePools()

	return wss
}

func (wss *wssProtocol) String() string {
	return fmt.Sprintf("Wss%s", wss.protocol.String())
}

func (wss *wssProtocol) Done() <-chan struct{} {
	return wss.connections.Done()
}

//piping new connections to connection pool for serving
func (wss *wssProtocol) pipePools() {
	defer func() {
		wss.Log().Debugf("stop %s managing", wss)
		wss.dispose()
	}()
	wss.Log().Debugf("start %s managing", wss)

	for {
		select {
		case <-wss.listeners.Done():
			return
		case conn, ok := <-wss.conns:
			if !ok {
				return
			}
			logger := log.AddFieldsFrom(wss.Log(), conn)
			if err := wss.connections.Put(conn, sockTTL); err != nil {
				// TODO should it be passed up to UA?
				logger.Errorf("put new TLS connection failed: %s", err)
				continue
			}
		}
	}
}

func (wss *wssProtocol) dispose() {
	wss.Log().Debugf("dispose %s", wss)
	close(wss.conns)
}

func (wss *wssProtocol) Listen(target *Target) error {
	target = FillTargetHostAndPort(wss.Network(), target)
	//network := strings.ToLower(wss.Network())
	//esolve local TCP endpoint
	laddr, err := wss.resolveTarget(target)
	if err != nil {
		return err
	}

	if target.Options == nil {
		return fmt.Errorf("Require valid Options parameters to start TLS")
	}

	cert, err := ntls.LoadX509KeyPair(target.Options.CertFile, target.Options.KeyFile)
	if err != nil {
		wss.Log().Fatal(err)
	}
	//create listener
	listener, err := ntls.Listen("tcp", laddr.String(), &ntls.Config{
		Certificates: []ntls.Certificate{cert},
	})
	if err != nil {
		return &ProtocolError{
			fmt.Errorf("failed to listen address %s: %s", laddr, err),
			fmt.Sprintf("create %s listener", wss.Network()),
			"wss",
		}
	}
	//index listeners by local address
	key := ListenerKey(fmt.Sprintf("wss:0.0.0.0:%d", laddr.Port))
	wss.listeners.Put(key, listener)

	return err //should be nil here
}

func (wss *wssProtocol) Send(target *Target, msg sip.Message) error {
	target = FillTargetHostAndPort(wss.Network(), target)

	wss.Log().Infof("sending message '%s' to %s", msg.Short(), target.Addr())
	wss.Log().Debugf("sending message '%s' to %s:\r\n%s", msg.Short(), target.Addr(), msg)

	//validate remote address
	if target.Host == "" || target.Host == DefaultHost {
		return &ProtocolError{
			fmt.Errorf("invalid remote host resolved %s", target.Host),
			"resolve destination address",
			fmt.Sprintf("%p", wss),
		}
	}
	//resolve remote address
	raddr, err := wss.resolveTarget(target)
	if err != nil {
		return err
	}
	//find or create connection
	conn, err := wss.getOrCreateConnection(raddr)
	if err != nil {
		return err
	}
	//send message
	_, err = conn.Write([]byte(msg.String()))

	return err
}

func (wss *wssProtocol) resolveTarget(target *Target) (*net.TCPAddr, error) {
	addr := target.Addr()
	network := "tcp" //strings.ToLower(wss.Network())
	//resolve remote address
	raddr, err := net.ResolveTCPAddr(network, addr)
	if err != nil {
		return nil, &ProtocolError{
			err,
			fmt.Sprintf("resolve target address %s %s", wss.Network(), addr),
			fmt.Sprintf("%p", wss),
		}
	}

	return raddr, nil
}

func (wss *wssProtocol) getOrCreateConnection(raddr *net.TCPAddr) (Connection, error) {
	network := strings.ToLower(wss.Network())
	/*laddr := &net.TCPAddr{
		IP:   net.IP(DefaultHost),
		Port: int(DefaultUdpPort),
		Zone: "",
	}*/
	key := ConnectionKey("wss:" + raddr.String())
	conn, err := wss.connections.Get(key)
	if err != nil {
		wss.Log().Debugf("connection for address %s not found; create a new one", raddr)
		/*
			roots := x509.NewCertPool()
			ok := roots.AppendCertsFromPEM([]byte(rootPEM))
			if !ok {
				wss.Log().Panic("failed to parse root certificate")
			}
		*/
		tlsConn, err := ntls.Dial(network /*laddr,*/, raddr.String(), &ntls.Config{
			VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
				return nil
			},
		})
		if err != nil {
			return nil, &ProtocolError{
				err,
				fmt.Sprintf("connect to %s %s address", wss.Network(), raddr),
				fmt.Sprintf("%p", wss),
			}
		}

		conn = NewConnection(tlsConn, key, wss.Log())
		if err := wss.connections.Put(conn, sockTTL); err != nil {
			return conn, err
		}
	}

	return conn, nil
}
