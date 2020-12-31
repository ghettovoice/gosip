package transport

import (
	ntls "crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strings"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
)

type tlsProtocol struct {
	protocol
	listeners   ListenerPool
	connections ConnectionPool
	conns       chan Connection
}

func NewTlsProtocol(output chan<- sip.Message, errs chan<- error, cancel <-chan struct{}, msgMapper sip.MessageMapper, logger log.Logger) Protocol {
	tls := new(tlsProtocol)
	tls.network = "tls"
	tls.reliable = true
	tls.streamed = true
	tls.conns = make(chan Connection)

	tls.log = logger.
		WithPrefix("transport.Protocol").
		WithFields(log.Fields{
			"protocol_ptr": fmt.Sprintf("%p", tls),
		})

	//TODO: add separate errs chan to listen errors from pool for reconnection?
	tls.listeners = NewListenerPool(tls.conns, errs, cancel, tls.Log())
	tls.connections = NewConnectionPool(output, errs, cancel, msgMapper, tls.Log())

	//pipe listener and connection pools
	go tls.pipePools()

	return tls
}

func (tls *tlsProtocol) String() string {
	return fmt.Sprintf("Tls%s", tls.protocol.String())
}

func (tls *tlsProtocol) Done() <-chan struct{} {
	return tls.connections.Done()
}

//piping new connections to connection pool for serving
func (tls *tlsProtocol) pipePools() {
	defer func() {
		tls.Log().Debugf("stop %s managing", tls)
		tls.dispose()
	}()
	tls.Log().Debugf("start %s managing", tls)

	for {
		select {
		case <-tls.listeners.Done():
			return
		case conn, ok := <-tls.conns:
			if !ok {
				return
			}
			logger := log.AddFieldsFrom(tls.Log(), conn)
			if err := tls.connections.Put(conn, sockTTL); err != nil {
				// TODO should it be passed up to UA?
				logger.Errorf("put new TLS connection failed: %s", err)
				continue
			}
		}
	}
}

func (tls *tlsProtocol) dispose() {
	tls.Log().Debugf("dispose %s", tls)
	close(tls.conns)
}

func (tls *tlsProtocol) Listen(target *Target) error {
	target = FillTargetHostAndPort(tls.Network(), target)
	//network := strings.ToLower(tls.Network())
	//esolve local TCP endpoint
	laddr, err := tls.resolveTarget(target)
	if err != nil {
		return err
	}

	if target.TLSConfig == nil {
		return fmt.Errorf("Require valid Options parameters to start TLS")
	}

	cert, err := ntls.LoadX509KeyPair(target.TLSConfig.Cert, target.TLSConfig.Key)
	if err != nil {
		tls.Log().Fatal(err)
	}
	//create listener
	listener, err := ntls.Listen("tcp", laddr.String(), &ntls.Config{
		Certificates: []ntls.Certificate{cert},
	})
	if err != nil {
		return &ProtocolError{
			fmt.Errorf("failed to listen address %s: %s", laddr, err),
			fmt.Sprintf("create %s listener", tls.Network()),
			"tls",
		}
	}
	//index listeners by local address
	key := ListenerKey(fmt.Sprintf("tls:0.0.0.0:%d", laddr.Port))

	tls.listeners.Put(key, listener)

	return err //should be nil here
}

func (tls *tlsProtocol) Send(target *Target, msg sip.Message) error {
	target = FillTargetHostAndPort(tls.Network(), target)

	tls.Log().Infof("sending message '%s' to %s", msg.Short(), target.Addr())
	tls.Log().Debugf("sending message '%s' to %s:\r\n%s", msg.Short(), target.Addr(), msg)

	//validate remote address
	if target.Host == "" || target.Host == DefaultHost {
		return &ProtocolError{
			fmt.Errorf("invalid remote host resolved %s", target.Host),
			"resolve destination address",
			fmt.Sprintf("%p", tls),
		}
	}
	//resolve remote address
	raddr, err := tls.resolveTarget(target)
	if err != nil {
		return err
	}
	//find or create connection
	conn, err := tls.getOrCreateConnection(raddr)
	if err != nil {
		return err
	}
	//send message
	_, err = conn.Write([]byte(msg.String()))

	return err
}

func (tls *tlsProtocol) resolveTarget(target *Target) (*net.TCPAddr, error) {
	addr := target.Addr()
	network := "tcp" //strings.ToLower(tls.Network())
	//resolve remote address
	raddr, err := net.ResolveTCPAddr(network, addr)
	if err != nil {
		return nil, &ProtocolError{
			err,
			fmt.Sprintf("resolve target address %s %s", tls.Network(), addr),
			fmt.Sprintf("%p", tls),
		}
	}

	return raddr, nil
}

func (tls *tlsProtocol) getOrCreateConnection(raddr *net.TCPAddr) (Connection, error) {
	network := strings.ToLower(tls.Network())
	/*laddr := &net.TCPAddr{
		IP:   net.IP(DefaultHost),
		Port: int(DefaultUdpPort),
		Zone: "",
	}*/
	key := ConnectionKey("tls:" + raddr.String())
	conn, err := tls.connections.Get(key)
	if err != nil {
		tls.Log().Debugf("connection for address %s not found; create a new one", raddr)
		/*
			roots := x509.NewCertPool()
			ok := roots.AppendCertsFromPEM([]byte(rootPEM))
			if !ok {
				tls.Log().Panic("failed to parse root certificate")
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
				fmt.Sprintf("connect to %s %s address", tls.Network(), raddr),
				fmt.Sprintf("%p", tls),
			}
		}

		conn = NewConnection(tlsConn, key, tls.Log())
		if err := tls.connections.Put(conn, sockTTL); err != nil {
			return conn, err
		}
	}

	return conn, nil
}
