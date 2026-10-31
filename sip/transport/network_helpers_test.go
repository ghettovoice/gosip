package transport_test

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
)

type acceptedConnListener struct {
	conn net.Conn
}

func (l *acceptedConnListener) Accept() (net.Conn, error) { return l.conn, nil }
func (*acceptedConnListener) Close() error                { return nil }
func (*acceptedConnListener) Addr() net.Addr              { return &net.TCPAddr{} }

func TestStartTestAccept_CleanupWithoutWait(t *testing.T) {
	t.Parallel()

	var lis *net.TCPListener
	t.Cleanup(func() {
		if lis == nil {
			t.Error("unused accept waiter did not create a listener")
			return
		}
		if err := lis.SetDeadline(time.Now().Add(time.Second)); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("listener.SetDeadline() error = %v, want nil or %v", err, net.ErrClosed)
		}
		if _, err := lis.Accept(); !errors.Is(err, net.ErrClosed) {
			t.Errorf("listener.Accept() after helper cleanup error = %v, want %v", err, net.ErrClosed)
		}
	})

	t.Run("unused accept waiter", func(t *testing.T) {
		t.Parallel()

		var err error
		lis, err = net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatalf("net.Listen() error = %v, want nil", err)
		}
		_ = startTestAccept(t, lis)
	})
}

func TestStartTestAccept_CleanupClosesUnclaimedConnection(t *testing.T) {
	t.Parallel()

	peer, server := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	lis := &acceptedConnListener{conn: server}

	t.Cleanup(func() {
		if err := peer.SetWriteDeadline(time.Now().Add(time.Second)); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			t.Errorf("peer.SetWriteDeadline() error = %v, want nil or %v", err, io.ErrClosedPipe)
		}
		if _, err := peer.Write([]byte("probe")); !errors.Is(err, io.ErrClosedPipe) {
			t.Errorf("peer.Write() error = %v, want %v", err, io.ErrClosedPipe)
		}
	})

	t.Run("unclaimed accepted connection", func(t *testing.T) {
		t.Parallel()
		_ = startTestAccept(t, lis)
	})
}
