package transport_test

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/testutils"
	"github.com/ghettovoice/gosip/transport"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("ListenerHandler", func() {
	var (
		output  chan transport.Connection
		errs    chan error
		cancel  chan struct{}
		ls      *testutils.MockListener
		handler transport.ListenerHandler
		wg      *sync.WaitGroup
	)
	addr := &testutils.MockAddr{"tcp", localAddr1}
	key := transport.ListenerKey(addr.String())
	str := "Hello world!"

	Context("just initialized", func() {
		BeforeEach(func() {
			output = make(chan transport.Connection)
			errs = make(chan error)
			cancel = make(chan struct{})
			ls = testutils.NewMockListener(addr)
			handler = transport.NewListenerHandler(key, ls, output, errs, cancel)
		})

		It("has ListenerKey", func() {
			Expect(handler.Key()).To(Equal(key))
		})
		It("has net.Listener", func() {
			Expect(handler.Listener()).To(Equal(ls))
		})
	})

	Context("serving listener", func() {
		BeforeEach(func() {
			output = make(chan transport.Connection)
			errs = make(chan error)
			cancel = make(chan struct{})
			ls = testutils.NewMockListener(addr)
			handler = transport.NewListenerHandler(key, ls, output, errs, cancel)

			wg = new(sync.WaitGroup)
			wg.Add(1)
			go handler.Serve(wg.Done)
			time.Sleep(time.Millisecond)
		})
		AfterEach(func() {
			handler.Cancel()
			wg.Wait()
		})

		Context("when new connection arrives", func() {
			BeforeEach(func() {
				wg.Add(1)
				go func() {
					defer wg.Done()
					client, err := ls.Dial("tcp", addr)
					Expect(err).ToNot(HaveOccurred())
					Expect(client.Write([]byte(str))).To(Equal(len(str)))
				}()
			})

			It("should accept connection and send to output", func() {
				conn := <-output
				Expect(conn).ToNot(BeNil())

				buf := make([]byte, 65535)
				num, err := conn.Read(buf)
				Expect(err).ToNot(HaveOccurred())
				Expect(string(buf[:num])).To(Equal(str))
			})
		})

		Context("when canceled", func() {
			Context("with Cancel()", func() {
				BeforeEach(func() {
					handler.Cancel()
				})
				It("should resolve Done chan", func() {
					select {
					case <-handler.Done():
					case <-time.After(time.Second):
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
			Context("by listener Close() or socket error", func() {
				BeforeEach(func() {
					ls.Close()
				})
				It("should send error and resolve Done chan", func() {
					select {
					case err := <-errs:
						Expect(err).To(HaveOccurred())
						Expect(err.Error()).To(ContainSubstring("listener closed"))
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

var _ = Describe("ListenerPool", func() {
	var (
		output chan transport.Connection
		errs   chan error
		cancel chan struct{}
		pool   transport.ListenerPool
	)
	str1 := "Hello world!"
	str2 := "Bye!"
	str3 := "What's up, dude?"
	addr1 := &testutils.MockAddr{"tcp", localAddr1}
	addr2 := &testutils.MockAddr{"tcp", localAddr2}
	addr3 := &testutils.MockAddr{"tcp", localAddr3}
	key1 := transport.ListenerKey(addr1.String())
	key2 := transport.ListenerKey(addr2.String())
	key3 := transport.ListenerKey(addr3.String())

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
			output = make(chan transport.Connection)
			errs = make(chan error)
			cancel = make(chan struct{})
			pool = transport.NewListenerPool(output, errs, cancel)
		})

		ShouldBeEmpty()
	})

	Context("that canceled", func() {
		var (
			err      error
			expected string
		)

		BeforeEach(func() {
			output = make(chan transport.Connection)
			errs = make(chan error)
			cancel = make(chan struct{})
			pool = transport.NewListenerPool(output, errs, cancel)
			expected = fmt.Sprintf("%s canceled", pool)

			close(cancel)
			time.Sleep(time.Millisecond)
		})

		It("should decline Put", func() {
			err = pool.Put(key1, testutils.NewMockListener(addr1))
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
			err                error
			ls1, ls2, ls3, ls4 net.Listener
			wg                 *sync.WaitGroup
		)

		BeforeEach(func() {
			output = make(chan transport.Connection)
			errs = make(chan error)
			cancel = make(chan struct{})
			pool = transport.NewListenerPool(output, errs, cancel)

			ls1 = testutils.NewMockListener(addr1)
			ls2 = testutils.NewMockListener(addr2)
			ls3 = testutils.NewMockListener(addr3)
		})
		AfterEach(func() {
			defer func() { recover() }()
			close(cancel)
			<-pool.Done()
		})

		Context("put listener with empty key = ''", func() {
			BeforeEach(func() {
				err = pool.Put("", ls1)
			})
			It("should return Invalid Key error", func() {
				Expect(err.Error()).To(ContainSubstring("invalid key provided"))
			})
			Context("the pool", func() {
				ShouldBeEmpty()
			})
		})

		Context("get listener by non existent key1", func() {
			BeforeEach(func() {
				ls4, err = pool.Get(key1)
			})

			It("should return Not Found error", func() {
				Expect(ls4).To(BeNil())
				Expect(err.Error()).To(ContainSubstring("not found"))
			})
		})

		Context("drop listener by non existent key1", func() {
			BeforeEach(func() {
				err = pool.Drop(key1)
			})

			It("should return Not Found error", func() {
				Expect(err.Error()).To(ContainSubstring("not found"))
			})
		})

		Context("put listener with key1", func() {
			BeforeEach(func() {
				err = pool.Put(key1, ls1)
			})

			It("should run without error", func() {
				Expect(err).ToNot(HaveOccurred())
			})

			Context("the pool", func() {
				It("should has Length = 1", func() {
					Expect(pool.Length()).To(Equal(1))
				})

				It("should has store with one listener ls1", func() {
					Expect(pool.All()).To(ConsistOf(ls1))
				})

				It("should find listener ls1 by key1", func() {
					ls4, err = pool.Get(key1)
					Expect(ls4).To(Equal(ls1))
					Expect(err).ToNot(HaveOccurred())
				})
			})
		})

		Context("has listener ls1 with key1", func() {
			BeforeEach(func() {
				Expect(pool.Put(key1, ls1)).ToNot(HaveOccurred())
			})

			Context("put another listener with the same key1", func() {
				BeforeEach(func() {
					err = pool.Put(key1, testutils.NewMockListener(addr3))
				})
				It("should return Duplicate error", func() {
					Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("%s already has key %s", pool, key1)))
				})
				Context("the pool", func() {
					It("should has Length = 1", func() {
						Expect(pool.Length()).To(Equal(1))
					})
					It("should has store with one listener ls1", func() {
						Expect(pool.All()).To(ConsistOf(ls1))
					})
				})
			})

			Context("put another listener ls2 with key2", func() {
				BeforeEach(func() {
					err = pool.Put(key2, ls2)
				})

				It("should run without error", func() {
					Expect(err).ToNot(HaveOccurred())
				})

				Context("the pool", func() {
					It("should has Length = 2", func() {
						Expect(pool.Length()).To(Equal(2))
					})
					It("should has store with 2 listeners: ls1, ls2", func() {
						Expect(pool.All()).To(ConsistOf(ls1, ls2))
					})
					It("should find listener ls2 by key2", func() {
						ls4, err = pool.Get(key2)
						Expect(ls4).To(Equal(ls2))
						Expect(err).ToNot(HaveOccurred())
					})
				})
			})

			Context("drop listener ls1 by key1", func() {
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
							ls4, err = pool.Get(key1)
						})
						It("should return Not Found error", func() {
							Expect(err.Error()).To(ContainSubstring("not found"))
						})
					})
				})
			})

			Context("received error from listener ls1", func() {
				BeforeEach(func() {
					ls1.Close()
					select {
					case err = <-errs:
					case <-time.After(100 * time.Millisecond):
						Fail("timed out")
					}
				})

				It("should send error to errs chan", func() {
					Expect(err.Error()).To(ContainSubstring("listener closed"))
					if err, ok := err.(transport.Error); ok {
						Expect(err.Network()).To(BeTrue())
					} else {
						Fail("error from failed listener must be of transport.Error type")
					}
				})

				ShouldBeEmpty()

				Context("on get by key1", func() {
					BeforeEach(func() {
						ls4, err = pool.Get(key1)
					})
					It("should return Not Found error", func() {
						Expect(err.Error()).To(ContainSubstring("not found"))
					})
				})
			})
		})

		Context("has multiple listeners: key1=>ls1, key2=>ls2, key3=>ls3", func() {
			BeforeEach(func() {
				Expect(pool.Put(key1, ls1)).ToNot(HaveOccurred())
				Expect(pool.Put(key2, ls2)).ToNot(HaveOccurred())
				Expect(pool.Put(key3, ls3)).ToNot(HaveOccurred())
				Expect(pool.Length()).To(Equal(3))
			})

			sendTo := func(ls *testutils.MockListener, addr net.Addr, str string) {
				client, err := ls.Dial("tcp", addr)
				Expect(err).ToNot(HaveOccurred())
				defer client.Close()

				Expect(client.Write([]byte(str))).To(Equal(len(str)))
			}
			readConn := func(ls *testutils.MockListener, expected string) {
				buf := make([]byte, 65535)
				select {
				case conn := <-output:
					Expect(conn).ToNot(BeNil())
					Expect(conn.LocalAddr().String()).To(Equal(ls.Addr().String()))

					num, err := conn.Read(buf)
					Expect(err).ToNot(HaveOccurred())
					Expect(string(buf[:num])).To(Equal(expected))
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

			It("should pipe accepted connections", func() {
				wg = new(sync.WaitGroup)
				wg.Add(3)
				go func() {
					defer wg.Done()
					sendTo(ls1.(*testutils.MockListener), ls1.Addr(), str1)
					time.Sleep(50 * time.Millisecond)
					sendTo(ls1.(*testutils.MockListener), ls1.Addr(), str2)
				}()
				go func() {
					defer wg.Done()
					time.Sleep(10 * time.Millisecond)
					sendTo(ls2.(*testutils.MockListener), ls2.Addr(), str3)
					time.Sleep(10 * time.Millisecond)
					ls2.Close()
				}()
				go func() {
					defer wg.Done()
					time.Sleep(60 * time.Millisecond)
					sendTo(ls3.(*testutils.MockListener), ls3.Addr(), str3)
				}()
				By("ls1 accepts connection from addr1")
				readConn(ls1.(*testutils.MockListener), str1)
				By("ls2 accepts connection from addr2")
				readConn(ls2.(*testutils.MockListener), str3)
				By("ls2 falls")
				readErr("listener closed")
				By("ls1 accepts connection")
				readConn(ls1.(*testutils.MockListener), str2)
				By("ls3 accepts connection")
				readConn(ls3.(*testutils.MockListener), str3)

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
