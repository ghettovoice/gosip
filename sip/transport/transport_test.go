package transport_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

const asyncEventTimeout = 5 * time.Second

type connectionDialerFunc func(context.Context, string, string) (net.Conn, error)

func (f connectionDialerFunc) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return f(ctx, network, addr)
}

type spyPacketListenConfig struct {
	called  int
	network string
	addr    string
	conn    net.PacketConn
	err     error
}

func newViaHop(tb testing.TB, proto sip.TransportProto, addr sip.Addr) header.ViaHop {
	tb.Helper()

	return header.ViaHop{
		Proto:     sip.ProtoVer20(),
		Transport: proto,
		Addr:      addr,
		Params:    make(sip.Values).Set("branch", sip.GenerateBranch(0)),
	}
}

func (s *spyPacketListenConfig) ListenPacket(ctx context.Context, nt, addr string) (net.PacketConn, error) {
	s.called++
	s.network = nt
	s.addr = addr
	return s.conn, s.err
}

func (*spyPacketListenConfig) Listen(context.Context, string, string) (net.Listener, error) {
	return nil, errors.New("stream listener not supported in packet test")
}

type spyConnListenConfig struct {
	called  int
	network string
	addr    string
	lis     net.Listener
	err     error
}

func (s *spyConnListenConfig) Listen(ctx context.Context, nt, addr string) (net.Listener, error) {
	s.called++
	s.network = nt
	s.addr = addr
	return s.lis, s.err
}

func (*spyConnListenConfig) ListenPacket(context.Context, string, string) (net.PacketConn, error) {
	return nil, errors.New("packet listener not supported in stream test")
}

type signalListener struct {
	net.Listener
	acceptCalled chan struct{}
}

func (l *signalListener) Accept() (net.Conn, error) {
	select {
	case <-l.acceptCalled:
	default:
		close(l.acceptCalled)
	}

	return l.Listener.Accept()
}

type tempError struct{}

func (tempError) Error() string   { return "temporary accept error" }
func (tempError) Temporary() bool { return true }

type tempErrListener struct {
	net.Listener
	errOnce     sync.Once
	acceptOnce  sync.Once
	errInjected chan struct{}
	accepted    chan struct{}
}

func (l *tempErrListener) Accept() (net.Conn, error) {
	var err error
	l.errOnce.Do(func() {
		close(l.errInjected)
		err = tempError{}
	})

	if err != nil {
		return nil, err
	}

	conn, err := l.Listener.Accept()
	if err == nil {
		l.acceptOnce.Do(func() { close(l.accepted) })
	}
	return conn, err
}

func newMinReq(tb testing.TB) *sip.Request {
	tb.Helper()

	ruri := &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("example.com")}
	furi := &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("example.com")}
	turi := &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("example.com")}

	headers := make(sip.Headers).
		Set(header.Via{{Proto: sip.ProtoVer20(), Params: make(sip.Values).Set("branch", sip.GenerateBranch(16))}}).
		Set(&header.From{URI: furi, Params: make(sip.Values).Set("tag", "from-tag")}).
		Set(&header.To{URI: turi}).
		Set(header.CallID("call-id")).
		Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInvite}).
		Set(sip.DefaultMaxForwards)

	return &sip.Request{
		Method:  sip.RequestMethodInvite,
		URI:     ruri,
		Proto:   sip.ProtoVer20(),
		Headers: headers,
	}
}

func newMinResp(tb testing.TB, viaTp sip.TransportProto, viaAddr sip.Addr) *sip.Response {
	tb.Helper()

	furi := &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("example.com")}
	turi := &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("example.com")}

	headers := make(sip.Headers).
		Set(header.Via{{
			Proto:     sip.ProtoVer20(),
			Transport: viaTp,
			Addr:      viaAddr,
			Params:    make(sip.Values).Set("branch", sip.GenerateBranch(0)),
		}}).
		Set(&header.From{URI: furi, Params: make(sip.Values).Set("tag", "from-tag")}).
		Set(&header.To{URI: turi, Params: make(sip.Values).Set("tag", "to-tag")}).
		Set(header.CallID("call-id")).
		Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInvite})

	return &sip.Response{
		Status:  sip.ResponseStatusOK,
		Reason:  "OK",
		Proto:   sip.ProtoVer20(),
		Headers: headers,
	}
}

func readUDPMsg(tb testing.TB, conn net.PacketConn) sip.Message {
	tb.Helper()

	buf := make([]byte, 65535)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	n, _, err := conn.ReadFrom(buf)
	if err != nil {
		tb.Fatalf("conn.ReadFrom() error = %v, want nil", err)
	}

	msg, err := sip.DefaultParser().ParsePacket(buf[:n])
	if err != nil {
		tb.Fatalf("sip.DefaultParser().ParsePacket() error = %v, want nil", err)
	}

	return msg
}

func waitFor(tb testing.TB, fn func() bool) {
	tb.Helper()

	const timeout = 5 * time.Second

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if fn() {
			return
		}

		select {
		case <-deadline.C:
			tb.Fatalf("condition not met within %v", timeout)
		case <-ticker.C:
		}
	}
}

type acceptResult struct {
	conn net.Conn
	err  error
}

// startTestAccept starts an Accept call and owns cleanup of the listener and
// accepted socket.
func startTestAccept(tb testing.TB, lis net.Listener) func() net.Conn {
	tb.Helper()

	var result acceptResult
	done := make(chan struct{})
	go func() {
		result.conn, result.err = lis.Accept()
		close(done)
	}()

	tb.Cleanup(func() {
		_ = lis.Close()
		testutil.WaitClosed(tb, done, "listener.Accept()")
		if result.conn != nil {
			_ = result.conn.Close()
		}
	})

	return func() net.Conn {
		tb.Helper()
		testutil.WaitClosed(tb, done, "listener.Accept()")
		if result.err != nil {
			tb.Fatalf("listener.Accept() error = %v, want nil", result.err)
		}
		if result.conn == nil {
			tb.Fatal("listener.Accept() connection = nil, want non-nil")
		}
		return result.conn
	}
}

// Connless transport tests.
