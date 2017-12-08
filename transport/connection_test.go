package transport

import (
	"fmt"
	"net"
	"os"
	"testing"

	"github.com/franela/goblin"
	"github.com/ghettovoice/gosip/log"
)

var (
	localAddr  = fmt.Sprintf("%v:%v", DefaultHost, DefaultTcpPort)
	remoteAddr = fmt.Sprintf("%v:%v", DefaultHost, DefaultTcpPort+1)
)

func TestMain(m *testing.M) {
	log.SetLevel(log.DebugLevel)
	os.Exit(m.Run())
}

// Test constructing of the new Connection
func TestConnectionConstruct(t *testing.T) {
	g := goblin.Goblin(t)

	g.Describe("New Connection", func() {
		g.It("From UDP", func() {
			cUdpConn, sUdpConn := createPacketClientServer(g, "udp", localAddr)
			defer func() {
				cUdpConn.Close()
				sUdpConn.Close()
			}()
			conn := NewConnection(sUdpConn)

			g.Assert(conn.Network()).Equal("UDP")
			g.Assert(conn.Streamed()).IsFalse()
			g.Assert(conn.LocalAddr().String()).Equal(sUdpConn.LocalAddr().String())

			if err := conn.Close(); err != nil {
				g.Fail(err)
			}
		})

		g.It("From TCP", func() {
			cTcpConn, sTcpConn := createStreamClientServer(g, "tcp", localAddr)
			defer func() {
				cTcpConn.Close()
				sTcpConn.Close()
			}()
			conn := NewConnection(sTcpConn)

			g.Assert(conn.Network()).Equal("TCP")
			g.Assert(conn.Streamed()).IsTrue()
			g.Assert(conn.LocalAddr().String()).Equal(sTcpConn.LocalAddr().String())
			g.Assert(conn.RemoteAddr().String()).Equal(sTcpConn.RemoteAddr().String())

			if err := conn.Close(); err != nil {
				g.Fail(err)
			}
		})
	})
}

func TestConnectionReadWrite(t *testing.T) {
	g := goblin.Goblin(t)

	g.Describe("Read from Connection", func() {
		g.It("From UDP", func() {
			cUdpConn, sUdpConn := createPacketClientServer(g, "udp", localAddr)
			defer func() {
				cUdpConn.Close()
				sUdpConn.Close()
			}()

			data := "Hello world!"
			sConn := NewConnection(sUdpConn)

			go func() {
				_, err := cUdpConn.(*net.UDPConn).Write([]byte(data))
				if err != nil {
					g.Fail(err)
				}
			}()

			buf := make([]byte, 0)
			num, err := sConn.Read(buf)
			if err != nil {
				g.Fail(err)
			}

			g.Assert(sConn.RemoteAddr().String()).Equal(cUdpConn.LocalAddr().String())
			g.Assert(string(buf[:num])).Equal(data)
		})
	})
}

func createStreamClientServer(g *goblin.G, network string, addr string) (net.Conn, net.Conn) {
	ln, err := net.Listen(network, addr)
	if err != nil {
		g.Fail(err)
	}

	ch := make(chan net.Conn)
	go func() {
		defer ln.Close()
		if server, err := ln.Accept(); err == nil {
			ch <- server
		} else {
			g.Fail(err)
		}
	}()

	client, err := net.Dial(network, ln.Addr().String())
	if err != nil {
		g.Fail(err)
	}

	return client, <-ch
}

func createPacketClientServer(g *goblin.G, network string, addr string) (net.Conn, net.Conn) {
	server, err := net.ListenPacket(network, addr)
	if err != nil {
		g.Fail(err)
	}

	client, err := net.Dial(network, server.LocalAddr().String())
	if err != nil {
		g.Fail(err)
	}

	return client, server.(net.Conn)
}
