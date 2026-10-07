package netutil_test

import (
	"io"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/netutil"
)

type countingConn struct {
	mockConn
	closes   atomic.Int32
	readErr  error
	writeErr error
	writeIn  chan struct{}
	writeOut chan struct{}
}

func (c *countingConn) Close() error {
	c.closes.Add(1)
	return nil
}

func (c *countingConn) Read(b []byte) (int, error) {
	if c.readErr != nil {
		return 1, c.readErr
	}
	return c.mockConn.Read(b)
}

func (c *countingConn) Write(b []byte) (int, error) {
	if c.writeIn != nil {
		close(c.writeIn)
		c.writeIn = nil
		<-c.writeOut
	}
	if c.writeErr != nil {
		return 1, c.writeErr
	}
	return c.mockConn.Write(b)
}

type countingPacketConn struct {
	mockPacketConn
	closes   atomic.Int32
	readErr  error
	writeErr error
	writeIn  chan struct{}
	writeOut chan struct{}
}

func (c *countingPacketConn) Close() error {
	c.closes.Add(1)
	return nil
}

func (c *countingPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	if c.readErr != nil {
		return 1, nil, c.readErr
	}
	return c.mockPacketConn.ReadFrom(p)
}

func (c *countingPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if c.writeIn != nil {
		close(c.writeIn)
		c.writeIn = nil
		<-c.writeOut
	}
	if c.writeErr != nil {
		return 1, c.writeErr
	}
	return c.mockPacketConn.WriteTo(p, addr)
}

type autoCloseFixture struct {
	closes      func() int32
	read        func() error
	write       func() error
	close       func() error
	setReadErr  func(error)
	setWriteErr func(error)
	gateWrite   func() (started, release chan struct{})
	rewrap      func(ttl time.Duration) bool
}

var autoCloseKinds = []struct {
	name  string
	setup func(ttl time.Duration) *autoCloseFixture
}{
	{name: "conn", setup: func(ttl time.Duration) *autoCloseFixture {
		base := &countingConn{}
		w := netutil.NewAutoCloseConn(base, ttl)
		return &autoCloseFixture{
			closes:      base.closes.Load,
			read:        func() error { _, err := w.Read(make([]byte, 1)); return err },
			write:       func() error { _, err := w.Write([]byte{1}); return err },
			close:       w.Close,
			setReadErr:  func(err error) { base.readErr = err },
			setWriteErr: func(err error) { base.writeErr = err },
			gateWrite: func() (chan struct{}, chan struct{}) {
				base.writeIn, base.writeOut = make(chan struct{}), make(chan struct{})
				return base.writeIn, base.writeOut
			},
			rewrap: func(newTTL time.Duration) bool {
				return netutil.NewAutoCloseConn(w, newTTL) == w
			},
		}
	}},
	{name: "packet_conn", setup: func(ttl time.Duration) *autoCloseFixture {
		base := &countingPacketConn{}
		w := netutil.NewAutoClosePacketConn(base, ttl)
		return &autoCloseFixture{
			closes: base.closes.Load,
			read: func() error {
				_, _, err := w.ReadFrom(make([]byte, 1))
				return err
			},
			write: func() error {
				_, err := w.WriteTo([]byte{1}, base.LocalAddr())
				return err
			},
			close:       w.Close,
			setReadErr:  func(err error) { base.readErr = err },
			setWriteErr: func(err error) { base.writeErr = err },
			gateWrite: func() (chan struct{}, chan struct{}) {
				base.writeIn, base.writeOut = make(chan struct{}), make(chan struct{})
				return base.writeIn, base.writeOut
			},
			rewrap: func(newTTL time.Duration) bool {
				return netutil.NewAutoClosePacketConn(w, newTTL) == w
			},
		}
	}},
}

func TestAutoClose_IdleTimeoutCloses(t *testing.T) {
	t.Parallel()

	for _, kind := range autoCloseKinds {
		t.Run(kind.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				fx := kind.setup(time.Second)

				time.Sleep(1100 * time.Millisecond)
				if got := fx.closes(); got != 1 {
					t.Fatalf("underlying closes = %d after idle timeout, want 1", got)
				}
			})
		})
	}
}

func TestAutoClose_SuccessResetsTimer(t *testing.T) {
	t.Parallel()

	for _, kind := range autoCloseKinds {
		for _, op := range []string{"read", "write"} {
			t.Run(kind.name+"/"+op, func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					fx := kind.setup(time.Second)
					defer func() { _ = fx.close() }()

					run := fx.read
					if op == "write" {
						run = fx.write
					}

					time.Sleep(599 * time.Millisecond)
					if got := fx.closes(); got != 0 {
						t.Fatalf("underlying closes = %d before deadline, want 0", got)
					}

					if err := run(); err != nil {
						t.Fatalf("%s() error = %v, want nil", op, err)
					}

					time.Sleep(500 * time.Millisecond)
					if got := fx.closes(); got != 0 {
						t.Fatalf("underlying closes = %d at original deadline, want 0", got)
					}

					time.Sleep(600 * time.Millisecond)
					if got := fx.closes(); got != 1 {
						t.Fatalf("underlying closes = %d after new full timeout, want 1", got)
					}
				})
			})
		}
	}
}

func TestAutoClose_ErrorDoesNotReset(t *testing.T) {
	t.Parallel()

	for _, kind := range autoCloseKinds {
		for _, op := range []string{"read", "write"} {
			t.Run(kind.name+"/"+op, func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					fx := kind.setup(time.Second)
					defer func() { _ = fx.close() }()

					sentinel := io.ErrUnexpectedEOF
					if op == "read" {
						fx.setReadErr(sentinel)
					} else {
						fx.setWriteErr(sentinel)
					}
					run := fx.read
					if op == "write" {
						run = fx.write
					}

					time.Sleep(600 * time.Millisecond)
					if err := run(); !errors.Is(err, sentinel) {
						t.Fatalf("%s() error = %v, want %v", op, err, sentinel)
					}

					time.Sleep(500 * time.Millisecond)
					if got := fx.closes(); got != 1 {
						t.Fatalf("underlying closes = %d at original deadline, want 1", got)
					}
				})
			})
		}
	}
}

func TestAutoClose_CloseIsPermanent(t *testing.T) {
	t.Parallel()

	for _, kind := range autoCloseKinds {
		t.Run(kind.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				fx := kind.setup(time.Second)

				time.Sleep(600 * time.Millisecond)
				if err := fx.close(); err != nil {
					t.Fatalf("close() error = %v, want nil", err)
				}
				if got := fx.closes(); got != 1 {
					t.Fatalf("underlying closes = %d after manual close, want 1", got)
				}

				if err := fx.read(); err != nil {
					t.Fatalf("late read() error = %v, want nil", err)
				}
				if err := fx.write(); err != nil {
					t.Fatalf("late write() error = %v, want nil", err)
				}

				time.Sleep(2 * time.Second)
				if got := fx.closes(); got != 1 {
					t.Fatalf("underlying closes = %d after late successful IO, want 1", got)
				}
			})
		})
	}
}

func TestAutoClose_Disabled(t *testing.T) {
	t.Parallel()

	for _, kind := range autoCloseKinds {
		for _, ttl := range []time.Duration{0, -time.Second} {
			t.Run(kind.name+"/"+ttl.String(), func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					fx := kind.setup(ttl)

					if err := fx.read(); err != nil {
						t.Fatalf("read() error = %v, want nil", err)
					}
					if err := fx.write(); err != nil {
						t.Fatalf("write() error = %v, want nil", err)
					}

					time.Sleep(3 * time.Second)
					if got := fx.closes(); got != 0 {
						t.Fatalf("underlying closes = %d with disabled timeout, want 0", got)
					}
					if err := fx.close(); err != nil {
						t.Fatalf("close() error = %v, want nil", err)
					}
				})
			})
		}
	}
}

func TestAutoClose_RewrapKeepsTimeout(t *testing.T) {
	t.Parallel()

	for _, kind := range autoCloseKinds {
		t.Run(kind.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				fx := kind.setup(time.Second)
				defer func() { _ = fx.close() }()

				time.Sleep(600 * time.Millisecond)
				if !fx.rewrap(10 * time.Second) {
					t.Fatal("re-wrap returned a different wrapper, want same instance")
				}

				time.Sleep(500 * time.Millisecond)
				if got := fx.closes(); got != 1 {
					t.Fatalf("underlying closes = %d at original deadline, want 1", got)
				}
			})
		})
	}
}

func TestAutoClose_ConcurrentWriteAndClose(t *testing.T) {
	t.Parallel()

	for _, kind := range autoCloseKinds {
		t.Run(kind.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				fx := kind.setup(time.Second)

				started, release := fx.gateWrite()
				done := make(chan error, 1)
				go func() { done <- fx.write() }()
				<-started

				if err := fx.close(); err != nil {
					t.Fatalf("close() error = %v, want nil", err)
				}
				if got := fx.closes(); got != 1 {
					t.Fatalf("underlying closes = %d after close, want 1", got)
				}

				close(release)
				if err := <-done; err != nil {
					t.Fatalf("late write() error = %v, want nil", err)
				}

				time.Sleep(2 * time.Second)
				if got := fx.closes(); got != 1 {
					t.Fatalf("underlying closes = %d after late write completion, want 1", got)
				}
			})
		})
	}
}
