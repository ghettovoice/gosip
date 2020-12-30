package transport

import (
	ntls "crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

var (
	subprotocol = "sip"
)

type WsConn struct {
	conn net.Conn
}

func (wc WsConn) Read(b []byte) (n int, err error) {
	msg, op, err := wsutil.ReadClientData(wc.conn)
	if err != nil {
		// handle error
		return n, err
	}
	if op == ws.OpClose {
		return n, io.EOF
	}
	copy(b, msg)
	return len(msg), err
}

func (wc WsConn) Write(b []byte) (n int, err error) {
	err = wsutil.WriteServerMessage(wc.conn, ws.OpText, b)
	if err != nil {
		// handle error
		return n, err
	}
	return len(b), nil
}

func (wc WsConn) LocalAddr() net.Addr {
	return wc.conn.LocalAddr()
}

// RemoteAddr returns the remote network address.
func (wc WsConn) RemoteAddr() net.Addr {
	return wc.conn.RemoteAddr()
}

func (wc WsConn) Close() error {
	return wc.conn.Close()
}

func (wc WsConn) SetDeadline(t time.Time) error {
	return wc.conn.SetDeadline(t)
}

func (wc WsConn) SetReadDeadline(t time.Time) error {
	return wc.conn.SetReadDeadline(t)
}

func (wc WsConn) SetWriteDeadline(t time.Time) error {
	return wc.conn.SetWriteDeadline(t)
}

type wsListener struct {
	listener net.Listener
	log      log.Logger
	u        ws.Upgrader
}

func NewWsListener(listener net.Listener, address string) *wsListener {
	l := &wsListener{listener: listener}
	/*
		e := wsflate.Extension{
			// We are using default parameters here since we use
			// wsflate.{Compress,Decompress}Frame helpers below in the code.
			// This assumes that we use standard compress/flate package as flate
			// implementation.
			Parameters: wsflate.DefaultParameters,
		}
	*/

	l.u = ws.Upgrader{
		//Negotiate: e.Negotiate,
	}

	return l
}

func (l *wsListener) Accept() (net.Conn, error) {
	conn, err := l.listener.Accept()
	if err != nil {
		l.log.Infof("Error on wsListener.Accept %v", err)
		return nil, err
	}
	_, err = l.u.Upgrade(conn)
	if err != nil {
		l.log.Infof("Error on wsListener.Accept.Upgrade %v", err)
		return nil, err
	}
	wc := WsConn{conn: conn}
	return wc, err
}

func (l *wsListener) Close() error {
	return l.listener.Close()
}

func (l *wsListener) Addr() net.Addr {
	return l.listener.Addr()
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

	var listener net.Listener
	if target.Options != nil {

		cert, err := ntls.LoadX509KeyPair(target.Options.CertFile, target.Options.KeyFile)
		if err != nil {
			wss.Log().Fatal(err)
		}
		//create tls listener
		listener, err = ntls.Listen("tcp", laddr.String(), &ntls.Config{
			Certificates: []ntls.Certificate{cert},
		})
		if err != nil {
			return &ProtocolError{
				fmt.Errorf("failed to listen address %s: %s", laddr, err),
				fmt.Sprintf("create %s listener", wss.Network()),
				"wss",
			}
		}
	} else {
		//create tcp listener
		listener, err = net.Listen("tcp", laddr.String())
		if err != nil {
			return &ProtocolError{
				fmt.Errorf("failed to listen address %s: %s", laddr, err),
				fmt.Sprintf("create %s listener", wss.Network()),
				"ws",
			}
		}
	}

	wsl := NewWsListener(listener, laddr.String())

	//index listeners by local address
	protocol := "ws"
	if target.Options != nil {
		protocol = "wss"
	}

	key := ListenerKey(fmt.Sprintf("%s:0.0.0.0:%d", protocol, laddr.Port))
	wss.listeners.Put(key, wsl)

	wss.Log().Infof("listening on %s://%v", protocol, laddr.String())

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
