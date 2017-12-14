package transport_test

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/timing"
	"github.com/ghettovoice/gosip/transport"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("ConnectionHandler", func() {
	var (
		output         chan *transport.IncomingMessage
		errs           chan error
		cancel         chan struct{}
		client, server net.Conn
		conn           transport.Connection
		handler        transport.ConnectionHandler
	)
	addr := &mockAddr{"tcp", localAddr1}
	key := transport.ConnectionKey(addr.String())
	noParams := core.NewParams()
	callId := core.CallId("call-1234567890")
	tag := core.String{"qwerty"}
	body := "Hello world!"
	inviteMsg := core.NewRequest(
		"INVITE",
		&core.SipUri{
			User:      core.String{"bob"},
			Host:      "far-far-away.com",
			UriParams: noParams,
			Headers:   noParams,
		},
		"SIP/2.0",
		[]core.Header{
			&core.FromHeader{
				DisplayName: core.String{"bob"},
				Address: &core.SipUri{
					User:      core.String{"bob"},
					Host:      "far-far-away.com",
					UriParams: noParams,
					Headers:   noParams,
				},
				Params: noParams,
			},
			&core.ToHeader{
				DisplayName: core.String{"alice"},
				Address: &core.SipUri{
					User:      core.String{"alice"},
					Host:      "wonderland.com",
					UriParams: noParams,
					Headers:   noParams,
				},
				Params: core.NewParams().Add("tag", tag),
			},
			&callId,
		},
		body,
	)
	//bullshit := "This is bullshit!\r\n"

	timing.MockMode = true

	Context("just initialized", func() {
		var ttl time.Duration

		BeforeEach(func() {
			output = make(chan *transport.IncomingMessage)
			errs = make(chan error)
			cancel = make(chan struct{})
			c1, c2 := net.Pipe()
			client = &mockConn{c1, c1.LocalAddr(), addr}
			server = &mockConn{c2, addr, c2.RemoteAddr()}
			conn = transport.NewConnection(server)
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
			handler = transport.NewConnectionHandler(key, conn, ttl, output, errs, cancel)
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
		var wg *sync.WaitGroup
		var ttl time.Duration

		BeforeEach(func() {
			output = make(chan *transport.IncomingMessage)
			errs = make(chan error)
			cancel = make(chan struct{})
			c1, c2 := net.Pipe()
			client = &mockConn{c1, c1.LocalAddr(), addr}
			server = &mockConn{c2, addr, c2.RemoteAddr()}
			conn = transport.NewConnection(server)
			wg = new(sync.WaitGroup)
		})
		AfterEach(func() {
			defer func() { recover() }()
			handler.Cancel()
			wg.Wait()
			client.Close()
			server.Close()
			close(output)
			close(errs)
			close(cancel)
		})
		JustBeforeEach(func() {
			handler = transport.NewConnectionHandler(key, conn, ttl, output, errs, cancel)
			wg.Add(1)
			go handler.Serve(wg.Done)
		})

		Context("when new data arrives", func() {
			BeforeEach(func() {
				wg.Add(1)
				go func() {
					defer wg.Done()
					Expect(client.Write([]byte(inviteMsg.String()))).To(Equal(len(inviteMsg.String())))
					//time.Sleep(time.Millisecond)
					//Expect(client.Write([]byte(bullshit))).To(Equal(len(bullshit)))
					time.Sleep(time.Millisecond)
					Expect(client.Write([]byte(inviteMsg.String()))).To(Equal(len(inviteMsg.String())))
				}()
				time.Sleep(time.Millisecond)
			})

			It("should read, parse and pipe to output", func() {
				By("first message arrives")
				assertIncomingMessageArrived(output, inviteMsg, conn.LocalAddr().String(), conn.RemoteAddr().String())
				By("second message arrives")
				assertIncomingMessageArrived(output, inviteMsg, conn.LocalAddr().String(), conn.RemoteAddr().String())
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
					Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("%s expired", handler.Connection())))
				case <-time.After(100 * time.Millisecond):
					Fail("timed out")
				}
			})
		})

		Context("when gets cancel", func() {
			BeforeEach(func() {
				ttl = 0
			})
			Context("by Cancel() call", func() {
				JustBeforeEach(func() {
					handler.Cancel()
				})
				It("should resolve Done chan", func() {
					select {
					case <-handler.Done():
					case <-time.After(100 * time.Millisecond):
						Fail("timed out")
					}
				})
			})
			Context("by global cancel signal", func() {
				BeforeEach(func() {
					close(cancel)
				})
				It("should resolve Done chan", func() {
					select {
					case <-handler.Done():
					case <-time.After(100 * time.Millisecond):
						Fail("timed out")
					}
				})
			})
			Context("by connection Close() or socket error", func() {
				BeforeEach(func() {
					conn.Close()
				})
				It("should send error and resolve Done chan", func() {
					select {
					case err := <-errs:
						Expect(err).To(HaveOccurred())
						Expect(err.Error()).To(ContainSubstring("read/write on closed pipe"))
					case <-time.After(100 * time.Millisecond):
						Fail("timed out")
					}

					select {
					case <-handler.Done():
					case <-time.After(100 * time.Millisecond):
						Fail("timed out")
					}
				})
			})
		})
	})
})

var _ = Describe("ConnectionPool", func() {
	var (
		output chan *transport.IncomingMessage
		errs   chan error
		cancel chan struct{}
		pool   transport.ConnectionPool
	)
	addr1 := &mockAddr{"tcp", localAddr1}
	addr2 := &mockAddr{"tcp", localAddr2}
	addr3 := &mockAddr{"tcp", localAddr3}
	key1 := transport.ConnectionKey(addr1.String())
	key2 := transport.ConnectionKey(addr2.String())
	key3 := transport.ConnectionKey(addr3.String())
	noParams := core.NewParams()
	callId1 := core.CallId("call-1")
	callId2 := core.CallId("call-2")
	callId3 := core.CallId("call-3")
	msg1 := core.NewRequest(
		core.INVITE,
		&core.SipUri{
			User:      core.String{"bob"},
			Host:      "far-far-away.com",
			UriParams: noParams,
			Headers:   noParams,
		},
		"SIP/2.0",
		[]core.Header{
			&core.FromHeader{
				DisplayName: core.String{"bob"},
				Address: &core.SipUri{
					User:      core.String{"bob"},
					Host:      "far-far-away.com",
					UriParams: noParams,
					Headers:   noParams,
				},
				Params: noParams,
			},
			&core.ToHeader{
				DisplayName: core.String{"alice"},
				Address: &core.SipUri{
					User:      core.String{"alice"},
					Host:      "wonderland.com",
					UriParams: noParams,
					Headers:   noParams,
				},
				Params: core.NewParams().Add("tag", core.String{"msg-1"}),
			},
			&callId1,
		},
		"Hello world!",
	)
	msg2 := core.NewRequest(
		core.BYE,
		&core.SipUri{
			User:      core.String{"bob"},
			Host:      "far-far-away.com",
			UriParams: noParams,
			Headers:   noParams,
		},
		"SIP/2.0",
		[]core.Header{
			&core.FromHeader{
				DisplayName: core.String{"bob"},
				Address: &core.SipUri{
					User:      core.String{"bob"},
					Host:      "far-far-away.com",
					UriParams: noParams,
					Headers:   noParams,
				},
				Params: noParams,
			},
			&core.ToHeader{
				DisplayName: core.String{"alice"},
				Address: &core.SipUri{
					User:      core.String{"alice"},
					Host:      "wonderland.com",
					UriParams: noParams,
					Headers:   noParams,
				},
				Params: core.NewParams().Add("tag", core.String{"msg-2"}),
			},
			&callId2,
		},
		"Bye!",
	)
	msg3 := core.NewRequest(
		core.NOTIFY,
		&core.SipUri{
			User:      core.String{"bob"},
			Host:      "far-far-away.com",
			UriParams: noParams,
			Headers:   noParams,
		},
		"SIP/2.0",
		[]core.Header{
			&core.FromHeader{
				DisplayName: core.String{"bob"},
				Address: &core.SipUri{
					User:      core.String{"bob"},
					Host:      "far-far-away.com",
					UriParams: noParams,
					Headers:   noParams,
				},
				Params: noParams,
			},
			&core.ToHeader{
				DisplayName: core.String{"alice"},
				Address: &core.SipUri{
					User:      core.String{"alice"},
					Host:      "wonderland.com",
					UriParams: noParams,
					Headers:   noParams,
				},
				Params: core.NewParams().Add("tag", core.String{"msg-3"}),
			},
			&callId3,
		},
		"What's up, dude?",
	)

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
			output = make(chan *transport.IncomingMessage)
			errs = make(chan error)
			cancel = make(chan struct{})
			pool = transport.NewConnectionPool(output, errs, cancel)
		})

		ShouldBeEmpty()
	})

	Context("that canceled", func() {
		var (
			err            error
			expected       string
			client, server net.Conn
		)

		BeforeEach(func() {
			output = make(chan *transport.IncomingMessage)
			errs = make(chan error)
			cancel = make(chan struct{})
			pool = transport.NewConnectionPool(output, errs, cancel)
			expected = fmt.Sprintf("%s canceled", pool)

			c1, c2 := net.Pipe()
			client = &mockConn{c1, c1.LocalAddr(), addr1}
			server = &mockConn{c2, addr1, c2.RemoteAddr()}

			close(cancel)
			time.Sleep(time.Millisecond)
		})

		It("should decline Put", func() {
			err = pool.Put(key1, transport.NewConnection(server), 0)
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
			//wg *sync.WaitGroup
		)

		createConn := func(addr net.Addr) (transport.Connection, transport.Connection) {
			c1, c2 := net.Pipe()
			client := transport.NewConnection(&mockConn{c1, c1.LocalAddr(), addr})
			server := transport.NewConnection(&mockConn{c2, addr, c2.RemoteAddr()})
			return client, server
		}

		BeforeEach(func() {
			output = make(chan *transport.IncomingMessage)
			errs = make(chan error)
			cancel = make(chan struct{})
			pool = transport.NewConnectionPool(output, errs, cancel)

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
				Expect(err.Error()).To(ContainSubstring("invalid key provided"))
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
					Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("%s already has key %s", pool, key1)))
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
						//Expect(pool.All()).To(ConsistOf(server1, server2))
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
				BeforeEach(func() {
					server1.Close()
					select {
					case err = <-errs:
					case <-time.After(100 * time.Millisecond):
						Fail("timed out")
					}
				})

				It("should send error to errs chan", func() {
					Expect(err.Error()).To(ContainSubstring("read/write on closed pipe"))
					if err, ok := err.(transport.Error); ok {
						Expect(err.Network()).To(BeTrue())
					} else {
						Fail("error from failed connection must be of transport.Error type")
					}
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
					select {
					case err = <-errs:
					case <-time.After(100 * time.Millisecond):
						Fail("timed out")
					}
				})
				It("should send Expire error", func() {
					Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("%s expired", server1)))
				})
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
				time.Sleep(time.Millisecond)
			})

			writeTo := func(conn transport.Connection, msg core.Message) {
				data := []byte(msg.String())
				Expect(conn.Write(data)).To(Equal(len(data)))
			}
			readMsg := func(expected core.Message, client transport.Connection, server transport.Connection) {
				select {
				case incomingMsg := <-output:
					Expect(incomingMsg).ToNot(BeNil())
					Expect(incomingMsg.Msg).ToNot(BeNil())
					Expect(incomingMsg.Msg.String()).To(Equal(expected.String()))
					Expect(incomingMsg.LAddr.String()).To(Equal(server.LocalAddr().String()))
					Expect(incomingMsg.RAddr.String()).To(Equal(client.LocalAddr().String()))
				case <-time.After(100 * time.Millisecond):
					Fail("timed out")
				}
			}
			readErr := func(expected string) {
				select {
				case err := <-errs:
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring(expected))
				case <-time.After(100 * time.Millisecond):
					Fail("timed out")
				}
			}

			It("should serve connections", func() {
				wg := new(sync.WaitGroup)
				wg.Add(3)
				go func() {
					defer wg.Done()
					time.Sleep(50 * time.Millisecond)
					writeTo(client1, msg1)
				}()
				go func() {
					defer wg.Done()
					time.Sleep(10 * time.Millisecond)
					writeTo(client2, msg2)
					time.Sleep(20 * time.Millisecond)
					timing.Elapse(ttl2 + time.Nanosecond)
				}()
				go func() {
					defer wg.Done()
					time.Sleep(20 * time.Millisecond)
					writeTo(client3, msg3)
					time.Sleep(20 * time.Millisecond)
					server3.Close()
				}()
				By("server2 receives msg2")
				readMsg(msg2, client2, server2)
				By("server3 receives msg3")
				readMsg(msg3, client3, server3)
				By("server2 expires")
				readErr(fmt.Sprintf("%s expired", server2))
				By("server3 falls")
				readErr("read/write on closed pipe")
				By("server1 receives msg1")
				readMsg(msg1, client1, server1)

				wg.Wait()
			})

			Context("got cancel signal", func() {
				BeforeEach(func() {
					time.Sleep(time.Millisecond)
					close(cancel)
				})
				It("should gracefully stop", func() {
					select {
					case <-pool.Done():
						AssertIsEmpty()
					case <-time.After(100 * time.Millisecond):
						Fail("timed out")
					}
				})
			})
		})
	})
})

func assertIncomingMessageArrived(
	fromCh <-chan *transport.IncomingMessage,
	expectedMessage core.Message,
	expectedLocalAddr string,
	expectedRemoteAddr string,
) {
	select {
	case incomingMsg := <-fromCh:
		Expect(incomingMsg).ToNot(BeNil())
		Expect(incomingMsg.Msg).ToNot(BeNil())
		Expect(incomingMsg.Msg.String()).To(Equal(expectedMessage.String()))
		Expect(incomingMsg.LAddr.String()).To(Equal(expectedLocalAddr))
		Expect(incomingMsg.RAddr.String()).To(Equal(expectedRemoteAddr))
	case <-time.After(100 * time.Millisecond):
		Fail("timed out")
	}
}
