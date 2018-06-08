package gosip

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/transaction"
	"github.com/ghettovoice/gosip/transport"
)

var Version = "0.0.0"

const (
	defaultListenAddr = "127.0.0.1:5060"
	defaultHostAddr   = "127.0.0.1"
)

var (
	protocols = []string{"udp", "tcp"}
)

// HandleFunc handles incoming SIP messages.
// Executes in goroutine for each incoming message.
type HandleFunc func(msg core.Message, srv *Server)

// Server is a SIP server
type Server struct {
	tp         transport.Layer
	tx         transaction.Layer
	hwg        *sync.WaitGroup
	inShutdown int32
}

// NewServer creates new instance of SIP server.
func NewServer(hostAddr string) *Server {
	if hostAddr == "" {
		hostAddr = defaultHostAddr
	}

	srv := new(Server)
	srv.hwg = new(sync.WaitGroup)
	srv.tp = transport.NewLayer(hostAddr)
	srv.tx = transaction.NewLayer(srv.tp)

	return srv
}

// Serve starts listening provided address
func (srv *Server) Serve(listenAddr string, handler HandleFunc) error {
	ctx := context.Background()

	for _, protocol := range protocols {
		if err := srv.tp.Listen(protocol, listenAddr); err != nil {
			// return immediately
			return err
		}
	}

	for {
		select {
		case <-ctx.Done():
			srv.Shutdown()
			break
		case msg := <-srv.tx.Messages():
			srv.hwg.Add(1)
			go func(msg core.Message, handler HandleFunc) {
				defer srv.hwg.Done()

				handler(msg, srv)
			}(msg, handler)
		case err := <-srv.tx.Errors():
			log.Error(err.Error())
		case err := <-srv.tp.Errors():
			log.Error(err.Error())
		}
	}

	return nil
}

// Send SIP message
func (srv *Server) Send(msg core.Message) error {
	if srv.shuttingDown() {
		return fmt.Errorf("can not send through shutting down server")
	}

	_, err := srv.tx.Send(msg)

	return err
}

func (srv *Server) shuttingDown() bool {
	return atomic.LoadInt32(&srv.inShutdown) != 0
}

func (srv *Server) Shutdown() {
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

type Responder struct {
	srv *Server
}

func (r *Responder) Send(msg core.Message) error {
	_, err := r.srv.tx.Send(msg)

	return err
}

// Serve starts SIP stack
func Serve(hostAddr, listenAddr string, handler HandleFunc) error {
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}

	srv := NewServer(hostAddr)

	return srv.Serve(listenAddr, handler)
}
