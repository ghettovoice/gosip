package transport_test

import (
	"fmt"
	"net"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/testutils"
	"github.com/ghettovoice/gosip/timing"
	"github.com/ghettovoice/gosip/transport"
	"github.com/ghettovoice/gosip/util"
)

var _ = Describe("ConnectionHandler", func() {
	var (
		output         chan sip.Message
		errs           chan error
		cancel         chan struct{}
		client, server net.Conn
		conn           transport.Connection
		handler        transport.ConnectionHandler
	)
	addr := &testutils.MockAddr{Net: "tcp", Addr: localAddr1}
	key := transport.ConnectionKey(addr.String())
	inviteMsg := "INVITE sip:bob@far-far-away.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP pc33.far-far-away.com;branch=z9hG4bK776asdhds\r\n" +
		"To: \"Bob\" <sip:bob@far-far-away.com>\r\n" +
		"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774\r\n" +
		"Content-Length: 12\r\n" +
		"\r\n" +
		"Hello world!"
	malformedMsg1 := "BYE sip:bob@biloxi.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP pc33.atlanta.com;branch=z9hG4bK776asdhds\r\n" +
		"From: \"Bob\" <sip:bob@biloxi.com>\r\n" +
		"To: \"Alice\" <sip:alice@atlanta.com>;tag=1928301774\r\n" +
		"\r\n" +
		"Message without content length\r\n"
	malformedMsg2 := "BULLSHIT sip:bob@bullshit.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP pc33.bullshit.com;branch=z9hG4bK776asdhds\r\n" +
		"To: <sip:hell@bullshit.com>\r\n" +
		"From: <sip:thepit@atlanta.com>;tag=1928301774\r\n" +
		"\r\n"
	bullshit := "This is bullshit!\r\n"

	logger := testutils.NewLogrusLogger()

	timing.MockMode = true

	Context("just initialized", func() {
		var ttl time.Duration

		BeforeEach(func() {
			output = make(chan sip.Message)
			errs = make(chan error)
			cancel = make(chan struct{})
			c1, c2 := net.Pipe()
			client = &testutils.MockConn{Conn: c1, LAddr: c1.LocalAddr(), RAddr: addr}
			server = &testutils.MockConn{Conn: c2, LAddr: addr, RAddr: c2.RemoteAddr()}
			conn = transport.NewConnection(server, logger)
		})
		AfterEach(func() {
			defer func() { recover() }()
			client.Close()
			server.Close()
			close(output)
			close(errs)
			close(cancel)
		})
		JustBeforeEach(func() {
			handler = transport.NewConnectionHandler(key, conn, ttl, output, errs, cancel, logger)
		})

		HasCorrectKeyAndConn := func() {
			It("should have ConnectionKey", func() {
				Expect(handler.Key()).To(Equal(key))
			})
			It("should have transport.Connection", func() {
				Expect(handler.Connection()).To(Equal(conn))
			})
		}

		Context("with TTL = 0", func() {
			BeforeEach(func() {
				ttl = 0
			})
			HasCorrectKeyAndConn()
			It("should have ZERO expiry time", func() {
				Expect(handler.Expiry()).To(BeZero())
			})
			It("should never expire", func() {
				Consistently(func() bool {
					return handler.Expired()
				}).Should(BeFalse())
			})
		})
		Context("with TTL > 0", func() {
			var expectedExpire time.Time
			BeforeEach(func() {
				ttl = 100 * time.Millisecond
				expectedExpire = time.Now().Add(ttl)
			})
			HasCorrectKeyAndConn()
			It("should set expiry time to Now() + 0.1 * time.Second", func() {
				Expect(handler.Expiry()).To(BeTemporally("~", expectedExpire))
			})
			It("should not be expired before TTL", func() {
				Expect(handler.Expired()).To(BeFalse())
				Eventually(func() bool {
					return handler.Expired()
				}).Should(BeTrue())
			})
		})
	})

	Context("serving connection", func() {
		var ttl time.Duration = 0

		BeforeEach(func() {
			output = make(chan sip.Message)
			errs = make(chan error)
			cancel = make(chan struct{})
			c1, c2 := net.Pipe()
			client = &testutils.MockConn{Conn: c1, LAddr: c1.LocalAddr(), RAddr: addr}
			server = &testutils.MockConn{Conn: c2, LAddr: addr, RAddr: c2.RemoteAddr()}
			conn = transport.NewConnection(server, logger)
		})
		AfterEach(func() {
			defer func() { recover() }()
			handler.Cancel()
			client.Close()
			server.Close()
			close(output)
			close(errs)
			close(cancel)
		})
		JustBeforeEach(func() {
			handler = transport.NewConnectionHandler(key, conn, ttl, output, errs, cancel, logger)
			go handler.Serve(util.Noop)
		})

		Context("when new data arrives", func() {
			JustBeforeEach(func() {
				go func() {
					testutils.WriteToConn(client, []byte(inviteMsg))
					time.Sleep(time.Millisecond)
					testutils.WriteToConn(client, []byte(bullshit))
					time.Sleep(time.Millisecond)
					testutils.WriteToConn(client, []byte(malformedMsg1))
					time.Sleep(time.Millisecond)
					testutils.WriteToConn(client, []byte(malformedMsg2))
					time.Sleep(time.Millisecond)
					testutils.WriteToConn(client, []byte(inviteMsg))
				}()
			})

			It("should read, parse and pipe to output", func(done Done) {
				By("first message arrives on output")
				testutils.AssertMessageArrived(output, inviteMsg, "pipe", "far-far-away.com:5060")
				By("bullshit arrives and ignored")
				time.Sleep(time.Millisecond)
				By("malformed message 1 arrives on errs")
				testutils.AssertIncomingErrorArrived(errs, "missing required 'Content-Length' header")
				By("malformed message 2 arrives on errs")
				testutils.AssertIncomingErrorArrived(errs, "missing required 'Content-Length' header")
				By("second message arrives on output")
				testutils.AssertMessageArrived(output, inviteMsg, "pipe", "far-far-away.com:5060")
				// for i := 0; i < 10; i++ {
				//	select {
				//	case msg := <-output:
				//		fmt.Printf("-------------------------------\n%s\n-------------------------------------\n", msg)
				//	case err := <-errs:
				//		fmt.Printf("-------------------------------\n%s\n-------------------------------------\n", err)
				//	}
				// }
				close(done)
			})
		})

		Context("with TTL = 0", func() {
			BeforeEach(func() {
				ttl = 0
			})
			It("should never expire", func() {
				timing.Elapse(time.Duration(time.Unix(1<<63-1, 0).Nanosecond()))
				select {
				case <-handler.Done():
					Fail("should run forever")
				case err := <-errs:
					Fail(err.Error())
				case <-time.After(time.Second):
				}
			})
		})

		Context("with TTL = 0.1 * time.Millisecond", func() {
			BeforeEach(func() {
				ttl = 100 * time.Millisecond
			})
			It("should fire expire error after 0.1 * time.Second", func() {
				timing.Elapse(ttl + time.Nanosecond)
				select {
				case <-handler.Done():
					Fail("should never complete")
				case err := <-errs:
					Expect(err.Error()).To(ContainSubstring("connection expired"))
				case <-time.After(100 * time.Millisecond):
					Fail("timed out")
				}
			})
		})

		Context("when gets cancel signal", func() {
			BeforeEach(func() {
				ttl = 0
			})
			Context("by Cancel() call", func() {
				JustBeforeEach(func() {
					handler.Cancel()
				})
				It("should resolve Done chan", func(done Done) {
					<-handler.Done()
					close(done)
				}, 3)
			})
			Context("by global cancel signal", func() {
				JustBeforeEach(func() {
					close(cancel)
				})
				It("should resolve Done chan", func(done Done) {
					<-handler.Done()
					close(done)
				}, 3)
			})
			Context("by connection Close() or socket error", func() {
				JustBeforeEach(func() {
					conn.Close()
				})
				It("should send error and resolve Done chan", func(done Done) {
					testutils.AssertIncomingErrorArrived(errs, "io: read/write on closed pipe")
					<-handler.Done()
					close(done)
				}, 3)
			})
		})
	})
})

var _ = Describe("ConnectionPool", func() {
	var (
		output chan sip.Message
		errs   chan error
		cancel chan struct{}
		pool   transport.ConnectionPool
	)
	addr1 := &testutils.MockAddr{Net: "tcp", Addr: localAddr1}
	addr2 := &testutils.MockAddr{Net: "tcp", Addr: localAddr2}
	addr3 := &testutils.MockAddr{Net: "tcp", Addr: localAddr3}
	key1 := transport.ConnectionKey(addr1.String())
	key2 := transport.ConnectionKey(addr2.String())
	key3 := transport.ConnectionKey(addr3.String())
	msg1 := "INVITE sip:bob@far-far-away.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP pc33.far-far-away.com;branch=z9hG4bK776asdhds\r\n" +
		"To: \"Bob\" <sip:bob@far-far-away.com>\r\n" +
		"From: \"Alice\" <sip:alice@wonderland.com>;tag=1928301774\r\n" +
		"Content-Length: 12\r\n" +
		"\r\n" +
		"Hello world!"
	msg2 := "BYE sip:bob@far-far-away.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP pc33.far-far-away.com;branch=z9hG4bK776asdhds\r\n" +
		"To: \"Alice\" <sip:bob@far-far-away.com>\r\n" +
		"From: \"Bob\" <sip:alice@wonderland.com>;tag=1928301774\r\n" +
		"Content-Length: 4\r\n" +
		"\r\n" +
		"Bye!"
	msg3 := "SIP/2.0 200 OK\r\n" +
		"CSeq: 2 INVITE\r\n" +
		"Call-ID: cheesecake1729\r\n" +
		"Max-Forwards: 65\r\n" +
		"\r\n"

	logger := testutils.NewLogrusLogger()

	timing.MockMode = true

	AssertIsEmpty := func() {
		Expect(pool.Length()).To(Equal(0))
		Expect(pool.All()).To(BeEmpty())
	}
	ShouldBeEmpty := func() {
		It("should be empty", func() {
			AssertIsEmpty()
		})
	}

	Context("just initialized", func() {
		BeforeEach(func() {
			output = make(chan sip.Message)
			errs = make(chan error)
			cancel = make(chan struct{})
			pool = transport.NewConnectionPool(output, errs, cancel, logger)
		})

		ShouldBeEmpty()
	})

	Context("that canceled", func() {
		var (
			err      error
			expected string
			server   net.Conn
		)

		BeforeEach(func() {
			output = make(chan sip.Message)
			errs = make(chan error)
			cancel = make(chan struct{})
			pool = transport.NewConnectionPool(output, errs, cancel, logger)
			expected = "connection pool closed"

			_, c2 := net.Pipe()
			server = &testutils.MockConn{Conn: c2, LAddr: addr1, RAddr: c2.RemoteAddr()}

			close(cancel)
			time.Sleep(time.Millisecond)
		})

		It("should decline Put", func() {
			err = pool.Put(key1, transport.NewConnection(server, logger), 0)
			Expect(err.Error()).To(ContainSubstring(expected))
			Expect(pool.Length()).To(Equal(0))
		})
		It("should decline Get", func() {
			ls, err := pool.Get(key1)
			Expect(ls).To(BeNil())
			Expect(err.Error()).To(ContainSubstring(expected))
			Expect(pool.Length()).To(Equal(0))
		})
		It("should decline Drop", func() {
			err = pool.Drop(key1)
			Expect(err.Error()).To(ContainSubstring(expected))
			Expect(pool.Length()).To(Equal(0))
		})
		It("should decline DropAll", func() {
			err = pool.DropAll()
			Expect(err.Error()).To(ContainSubstring(expected))
			Expect(pool.Length()).To(Equal(0))
		})
		It("should return empty from All", func() {
			Expect(pool.All()).To(BeEmpty())
		})
	})

	Context("that working", func() {
		var (
			err                                                         error
			client1, server1, client2, server2, client3, server3, conn4 transport.Connection
		)

		createConn := func(addr net.Addr) (transport.Connection, transport.Connection) {
			c1, c2 := net.Pipe()
			client := transport.NewConnection(&testutils.MockConn{Conn: c1, LAddr: c1.LocalAddr(), RAddr: addr}, logger)
			server := transport.NewConnection(&testutils.MockConn{Conn: c2, LAddr: addr, RAddr: c2.RemoteAddr()}, logger)
			return client, server
		}

		BeforeEach(func() {
			output = make(chan sip.Message)
			errs = make(chan error)
			cancel = make(chan struct{})
			pool = transport.NewConnectionPool(output, errs, cancel, logger)

			client1, server1 = createConn(addr1)
			client2, server2 = createConn(addr2)
			client3, server3 = createConn(addr3)
		})
		AfterEach(func() {
			defer func() { recover() }()
			close(cancel)
			<-pool.Done()
		})

		Context("put connection with empty key = ''", func() {
			BeforeEach(func() {
				err = pool.Put("", server1, 0)
			})
			It("should return Invalid Key error", func() {
				Expect(err.Error()).To(ContainSubstring("empty connection key"))
			})
			Context("the pool", func() {
				ShouldBeEmpty()
			})
		})

		Context("get connection by non existent key1", func() {
			BeforeEach(func() {
				conn4, err = pool.Get(key1)
			})

			It("should return Not Found error", func() {
				Expect(conn4).To(BeNil())
				Expect(err.Error()).To(ContainSubstring("not found"))
			})
		})

		Context("drop connection by non existent key1", func() {
			BeforeEach(func() {
				err = pool.Drop(key1)
			})

			It("should return Not Found error", func() {
				Expect(err.Error()).To(ContainSubstring("not found"))
			})
		})

		Context("put connection server1 with key1", func() {
			BeforeEach(func() {
				err = pool.Put(key1, server1, 0)
			})

			It("should run without error", func() {
				Expect(err).ToNot(HaveOccurred())
			})

			Context("the pool", func() {
				It("should has Length = 1", func() {
					Expect(pool.Length()).To(Equal(1))
				})

				It("should has store with one connection server1", func() {
					Expect(pool.All()).To(ConsistOf(server1))
				})

				It("should find connection server1 by key1", func() {
					Expect(pool.Get(key1)).To(Equal(server1))
				})
			})
		})

		Context("has connection server1 with key1", func() {
			BeforeEach(func() {
				Expect(pool.Put(key1, server1, 0)).ToNot(HaveOccurred())
			})

			Context("put another connection server3 with the same key1", func() {
				BeforeEach(func() {
					err = pool.Put(key1, server3, 0)
				})
				It("should return Duplicate error", func() {
					Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("key %s already exists in the pool", key1)))
				})
				Context("the pool", func() {
					It("should has Length = 1", func() {
						Expect(pool.Length()).To(Equal(1))
					})
					It("should has store with one connection server1", func() {
						Expect(pool.All()).To(ConsistOf(server1))
					})
				})
			})

			Context("put another connection server2 with key2", func() {
				BeforeEach(func() {
					err = pool.Put(key2, server2, 0)
				})

				It("should run without error", func() {
					Expect(err).ToNot(HaveOccurred())
				})

				Context("the pool", func() {
					It("should has Length = 2", func() {
						Expect(pool.Length()).To(Equal(2))
					})
					It("should has store with 2 connections: server1, server2", func() {
						// this line sometimes raises DATA RACE warning
						// Expect(pool.All()).To(ConsistOf(server1, server2))
						// compare by element
						all := pool.All()
						Expect(all[0]).To(Equal(server1))
						Expect(all[1]).To(Equal(server2))
					})
					It("should find connection server2 by key2", func() {
						Expect(pool.Get(key2)).To(Equal(server2))
					})
				})
			})

			Context("drop connection conn1 by key1", func() {
				BeforeEach(func() {
					err = pool.Drop(key1)
				})

				It("should run without error", func() {
					Expect(err).ToNot(HaveOccurred())
				})

				Context("the pool", func() {
					ShouldBeEmpty()

					Context("on get by key1", func() {
						BeforeEach(func() {
							_, err = pool.Get(key1)
						})
						It("should return Not Found error", func() {
							Expect(err.Error()).To(ContainSubstring("not found"))
						})
					})
				})
			})

			Context("received error from connection server1", func() {
				BeforeEach(func(done Done) {
					server1.Close()
					err = <-errs
					close(done)
				}, 3)

				It("should send io error", func() {
					Expect(err.Error()).To(ContainSubstring("io: read/write on closed pipe"))
					if err, ok := err.(transport.Error); ok {
						Expect(err.Network()).To(BeTrue())
					} else {
						Fail("error from failed connection must be of transport.Error type")
					}
				})
				It("should send error of transport.Error type and network indicator", func() {
					err, ok := err.(transport.Error)
					Expect(ok).To(BeTrue())
					Expect(err.Network()).To(BeTrue())
				})

				ShouldBeEmpty()

				Context("on get by key1", func() {
					BeforeEach(func() {
						conn4, err = pool.Get(key1)
					})
					It("should return Not Found error", func() {
						Expect(err.Error()).To(ContainSubstring("not found"))
					})
				})
			})
		})

		Context("has connection server1 with key1 and TTL > 0", func() {
			var ttl time.Duration

			BeforeEach(func() {
				ttl = time.Millisecond
				Expect(pool.Put(key1, server1, ttl)).ToNot(HaveOccurred())
				time.Sleep(time.Millisecond)
			})

			Context("after connection server1 expiry time", func() {
				BeforeEach(func() {
					timing.Elapse(ttl + time.Nanosecond)
					time.Sleep(time.Millisecond)
				}, 3)
				ShouldBeEmpty()
			})
		})
		// TODO refactor later, extract base helpers and assertions
		Context("has multiple connections: key1=>server1 (TTL=0), key2=>server2 (TTL=time.Millisecond), "+
			"key3=>server3 (TTL=100*time.Millisecond)", func() {
			var ttl2, ttl3 time.Duration
			BeforeEach(func() {
				ttl2 = 30 * time.Millisecond
				ttl3 = 100 * time.Millisecond
				Expect(pool.Put(key1, server1, 0)).ToNot(HaveOccurred())
				Expect(pool.Put(key2, server2, ttl2)).ToNot(HaveOccurred())
				Expect(pool.Put(key3, server3, ttl3)).ToNot(HaveOccurred())
			})

			Context("when new data arrives from clients", func() {
				BeforeEach(func() {
					go func() {
						time.Sleep(50 * time.Millisecond)
						testutils.WriteToConn(client1, []byte(msg1))
					}()
					go func() {
						time.Sleep(10 * time.Millisecond)
						testutils.WriteToConn(client2, []byte(msg2))
						time.Sleep(20 * time.Millisecond)
						timing.Elapse(ttl2 + time.Nanosecond)
					}()
					go func() {
						time.Sleep(20 * time.Millisecond)
						testutils.WriteToConn(client3, []byte(msg3))
						time.Sleep(20 * time.Millisecond)
						server3.Close()
					}()
				})
				It("should pipe handler outputs to self outputs", func(done Done) {
					By(fmt.Sprintf("message msg2 arrives %s -> %s", server2.RemoteAddr(), server2.LocalAddr()))
					testutils.AssertMessageArrived(output, msg2, "pipe", "far-far-away.com:5060")
					By(fmt.Sprintf("malformed message msg3 arrives %s -> %s", server3.RemoteAddr(), server3.LocalAddr()))
					testutils.AssertIncomingErrorArrived(errs, "missing required 'Content-Length' header")
					By("server2 expired error arrives and ignored")
					time.Sleep(time.Millisecond)
					By("server3 falls with error")
					testutils.AssertIncomingErrorArrived(errs, "io: read/write on closed pipe")
					By(fmt.Sprintf("message msg1 arrives %s -> %s", server1.RemoteAddr(), server1.LocalAddr()))
					testutils.AssertMessageArrived(output, msg1, "pipe", "far-far-away.com:5060")
					close(done)
				}, 3)
			})

			Context("got cancel signal", func() {
				BeforeEach(func() {
					close(cancel)
				})
				It("should gracefully stop", func(done Done) {
					<-pool.Done()
					close(done)
				}, 3)
			})
		})
	})
})
