package transport

import (
	"fmt"
	"net"
	"os"
	"sync"
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

	g.Describe("construct new Connection", func() {
		g.It("from UDP", func() {
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

		g.It("from TCP", func() {
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

	g.Describe("read/write from Connection", func() {
		data := "Hello world!"

		g.It("UDP", func() {
			cUdpConn, sUdpConn := createPacketClientServer(g, "udp", localAddr)
			defer func() {
				cUdpConn.Close()
				sUdpConn.Close()
			}()

			sConn := NewConnection(sUdpConn)
			cConn := NewConnection(cUdpConn)

			wg := new(sync.WaitGroup)
			wg.Add(1)
			go func() {
				defer wg.Done()

				buf := make([]byte, bufferSize)
				num, err := sConn.Read(buf)
				if err != nil {
					g.Fail(err)
				}
				log.Debugf("%s <- %s: read %d bytes", sConn.LocalAddr(), sConn.RemoteAddr(), num)

				g.Assert(fmt.Sprintf("%v", sConn.RemoteAddr())).Equal(fmt.Sprintf("%v", cConn.LocalAddr()))
				g.Assert(string(buf[:num])).Equal(data)
			}()

			num, err := cConn.Write([]byte(data))
			if err != nil {
				g.Fail(err)
			}
			log.Debugf("%s -> %s: written %d bytes", cConn.LocalAddr(), cConn.RemoteAddr(), num)

			wg.Wait()
		})

		g.It("from TCP", func() {
			cTcpConn, sTcpConn := createStreamClientServer(g, "tcp", localAddr)
			defer func() {
				cTcpConn.Close()
				sTcpConn.Close()
			}()

			sConn := NewConnection(sTcpConn)
			cConn := NewConnection(cTcpConn)

			wg := new(sync.WaitGroup)
			wg.Add(1)
			go func() {
				defer wg.Done()

				buf := make([]byte, bufferSize)
				num, err := sConn.Read(buf)
				if err != nil {
					g.Fail(err)
				}
				log.Debugf("%s <- %s: read %d bytes", sConn.LocalAddr(), sConn.RemoteAddr(), num)

				g.Assert(fmt.Sprintf("%v", sConn.RemoteAddr())).Equal(fmt.Sprintf("%v", cConn.LocalAddr()))
				g.Assert(string(buf[:num])).Equal(data)
			}()

			num, err := cConn.Write([]byte(data))
			if err != nil {
				g.Fail(err)
			}
			log.Debugf("%s -> %s: written %d bytes", cConn.LocalAddr(), cConn.RemoteAddr(), num)

			wg.Wait()
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
