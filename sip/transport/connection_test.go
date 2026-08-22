package transport_test

import (
	"bytes"
	"context"
	"io"
	"math"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/transport"
)

type rmtAddrOnly struct {
	addr net.Addr
}

func (r rmtAddrOnly) RemoteAddr() net.Addr { return r.addr }

type msgResult struct {
	msg sip.Message
	err error
}

type stubConn struct {
	laddr net.Addr
	raddr net.Addr
}

func (*stubConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (*stubConn) Write(p []byte) (int, error)      { return len(p), nil }
func (*stubConn) Close() error                     { return nil }
func (c *stubConn) LocalAddr() net.Addr            { return c.laddr }
func (c *stubConn) RemoteAddr() net.Addr           { return c.raddr }
func (*stubConn) SetDeadline(time.Time) error      { return nil }
func (*stubConn) SetReadDeadline(time.Time) error  { return nil }
func (*stubConn) SetWriteDeadline(time.Time) error { return nil }

type stubPacketConn struct {
	laddr net.Addr
}

func (*stubPacketConn) ReadFrom([]byte) (int, net.Addr, error) { return 0, nil, io.EOF }
func (*stubPacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	return len(p), nil
}
func (*stubPacketConn) Close() error                     { return nil }
func (c *stubPacketConn) LocalAddr() net.Addr            { return c.laddr }
func (*stubPacketConn) SetDeadline(time.Time) error      { return nil }
func (*stubPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (*stubPacketConn) SetWriteDeadline(time.Time) error { return nil }

type scriptedReadResult struct {
	data []byte
	err  error
}

type scriptedReadConn struct {
	laddr net.Addr
	raddr net.Addr
	seq   []scriptedReadResult
	idx   int
}

func (c *scriptedReadConn) Read(b []byte) (int, error) {
	if c.idx >= len(c.seq) {
		return 0, io.EOF
	}

	step := c.seq[c.idx]
	c.idx++
	n := copy(b, step.data)

	return n, step.err
}

func (*scriptedReadConn) Write(p []byte) (int, error)      { return len(p), nil }
func (*scriptedReadConn) Close() error                     { return nil }
func (c *scriptedReadConn) LocalAddr() net.Addr            { return c.laddr }
func (c *scriptedReadConn) RemoteAddr() net.Addr           { return c.raddr }
func (*scriptedReadConn) SetDeadline(time.Time) error      { return nil }
func (*scriptedReadConn) SetReadDeadline(time.Time) error  { return nil }
func (*scriptedReadConn) SetWriteDeadline(time.Time) error { return nil }

type scriptedPacketReadResult struct {
	data  []byte
	raddr net.Addr
	err   error
}

type scriptedPacketConn struct {
	laddr net.Addr
	seq   []scriptedPacketReadResult
	idx   int
}

func (c *scriptedPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	if c.idx >= len(c.seq) {
		return 0, nil, io.EOF
	}

	step := c.seq[c.idx]
	c.idx++
	n := copy(b, step.data)

	return n, step.raddr, step.err
}

func (*scriptedPacketConn) WriteTo(p []byte, _ net.Addr) (int, error) { return len(p), nil }
func (*scriptedPacketConn) Close() error                              { return nil }
func (c *scriptedPacketConn) LocalAddr() net.Addr                     { return c.laddr }
func (*scriptedPacketConn) SetDeadline(time.Time) error               { return nil }
func (*scriptedPacketConn) SetReadDeadline(time.Time) error           { return nil }
func (*scriptedPacketConn) SetWriteDeadline(time.Time) error          { return nil }

type temporaryReadError struct{}

func (temporaryReadError) Error() string   { return "temporary read error" }
func (temporaryReadError) Temporary() bool { return true }

func waitMsgResult(tb testing.TB, results <-chan msgResult) msgResult {
	tb.Helper()

	select {
	case res := <-results:
		return res
	case <-time.After(2 * time.Second):
		tb.Fatal("timed out waiting for message result")
		return msgResult{}
	}
}

func assertInReqEnvelopeMeta(tb testing.TB, msg sip.Message, wantMeta sip.TransportMetadata, wantLocAddr, wantRmtAddr netip.AddrPort) {
	tb.Helper()

	req, ok := msg.(*sip.RequestEnvelope)
	if !ok {
		tb.Fatalf("conn.Messages() first message type = %T, want *sip.RequestEnvelope", msg)
	}

	if got := req.Transport(); got != wantMeta {
		tb.Fatalf("inbound req Transport() = %v, want %v", got, wantMeta)
	}

	if got := req.LocalAddr(); got != wantLocAddr {
		tb.Fatalf("inbound req LocalAddr() = %v, want %v", got, wantLocAddr)
	}

	if got := req.RemoteAddr(); got != wantRmtAddr {
		tb.Fatalf("inbound req RemoteAddr() = %v, want %v", got, wantRmtAddr)
	}
}

func newTestTCPConn(tb testing.TB) sip.TransportConnection {
	tb.Helper()

	conn, _ := newTestTCPConnWithPeer(tb)
	return conn
}

func newTestTCPConnWithPeer(tb testing.TB) (sip.TransportConnection, net.Conn) {
	tb.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("net.Listen() error = %v, want nil", err)
	}

	tb.Cleanup(func() { lis.Close() })

	acceptedCh := make(chan net.Conn, 1)
	acceptErrCh := make(chan error, 1)

	go func() {
		accepted, acceptErr := lis.Accept()
		if acceptErr != nil {
			acceptErrCh <- acceptErr
			return
		}

		acceptedCh <- accepted
	}()

	peer, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		tb.Fatalf("net.Dial() error = %v, want nil", err)
	}

	tb.Cleanup(func() { peer.Close() })

	var base net.Conn
	select {
	case base = <-acceptedCh:
	case err = <-acceptErrCh:
		tb.Fatalf("listener.Accept() error = %v, want nil", err)
	case <-time.After(2 * time.Second):
		tb.Fatal("timed out waiting for listener.Accept()")
	}

	conn, err := transport.NewConnection(tb.Context(), base, sip.TCPMetadata())
	if err != nil {
		tb.Fatalf("transport.NewConnection(tcp) error = %v, want nil", err)
	}

	tb.Cleanup(func() { conn.Close(tb.Context()) })

	return conn, peer
}

func newTestPacketConn(tb testing.TB) sip.TransportConnection {
	tb.Helper()

	conn, _ := newTestPacketConnWithPeer(tb)
	return conn
}

func newTestPacketConnWithPeer(tb testing.TB) (sip.TransportConnection, net.PacketConn) {
	tb.Helper()

	base, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("net.ListenPacket(base) error = %v, want nil", err)
	}

	tb.Cleanup(func() { base.Close() })

	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("net.ListenPacket(peer) error = %v, want nil", err)
	}

	tb.Cleanup(func() { peer.Close() })

	tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata())
	if err != nil {
		tb.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
	}

	conn, err := transport.NewConnectionLessListener(tb.Context(), base, tp)
	if err != nil {
		tb.Fatalf("transport.NewConnectionLessListener() error = %v, want nil", err)
	}

	tb.Cleanup(func() { conn.Close(tb.Context()) })

	return conn, peer
}

func newConnReq(tb testing.TB, conn sip.TransportConnection) *sip.Request {
	tb.Helper()

	req := newMinReq(tb)
	laddr := conn.LocalAddr()
	req.Headers.Set(header.Via{{
		Proto:     sip.ProtoVer20(),
		Transport: conn.Metadata().Proto,
		Addr:      sip.AddrFromHostPort(laddr.Addr().String(), laddr.Port()),
		Params:    make(sip.Values).Set("branch", sip.GenerateBranch(0)),
	}})

	return req
}

const rawReqMsg = "OPTIONS sip:alice@example.com SIP/2.0\r\n" +
	"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK-1\r\n" +
	"From: <sip:bob@example.com>;tag=1\r\n" +
	"To: <sip:alice@example.com>\r\n" +
	"Call-ID: 1@example.com\r\n" +
	"CSeq: 1 OPTIONS\r\n" +
	"Max-Forwards: 70\r\n" +
	"Content-Length: 0\r\n\r\n"

func TestNewConnection(t *testing.T) {
	t.Parallel()

	laddr := netip.AddrPortFrom(netip.AddrFrom4([...]byte{111, 111, 111, 111}), 5060)
	raddr := netip.AddrPortFrom(netip.AddrFrom4([...]byte{222, 222, 222, 222}), 5060)

	tests := []struct {
		name        string
		baseConn    func(tb testing.TB) any
		meta        sip.TransportMetadata
		wantMeta    sip.TransportMetadata
		wantLocAddr netip.AddrPort
		wantRmtAddr netip.AddrPort
		wantErr     bool
	}{
		{
			name: "connected tcp",
			baseConn: func(tb testing.TB) any {
				tb.Helper()

				return &stubConn{
					laddr: net.TCPAddrFromAddrPort(laddr),
					raddr: net.TCPAddrFromAddrPort(raddr),
				}
			},
			meta: sip.TransportMetadata{
				Proto:        "tcp",
				Network:      "tcp",
				DefaultPort:  5060,
				Flags:        sip.TransportFlagReliable | sip.TransportFlagStreamed,
				NAPTRService: "SIP+D2T",
				Priority:     100,
			},
			wantMeta:    sip.TCPMetadata(),
			wantLocAddr: laddr,
			wantRmtAddr: raddr,
		},
		{
			name: "packet udp",
			baseConn: func(tb testing.TB) any {
				tb.Helper()

				return &stubPacketConn{laddr: net.UDPAddrFromAddrPort(laddr)}
			},
			meta: sip.TransportMetadata{
				Proto:        "udp",
				Network:      "udp",
				DefaultPort:  5060,
				NAPTRService: "SIP+D2U",
				MTU:          sip.UDPMetadata().MTU,
			},
			wantMeta:    sip.UDPMetadata(),
			wantLocAddr: laddr,
			wantRmtAddr: netip.AddrPort{},
		},
		{
			name: "connected socket but not net.Conn",
			baseConn: func(tb testing.TB) any {
				tb.Helper()
				return rmtAddrOnly{addr: &net.TCPAddr{IP: raddr.Addr().AsSlice(), Port: int(raddr.Port())}}
			},
			meta: sip.TransportMetadata{
				Proto:       "tcp",
				Network:     "tcp",
				DefaultPort: 5060,
			},
			wantErr: true,
		},
		{
			name: "unconnected net.Conn",
			baseConn: func(tb testing.TB) any {
				tb.Helper()

				return &stubConn{laddr: net.TCPAddrFromAddrPort(laddr)}
			},
			meta: sip.TransportMetadata{
				Proto:       "tcp",
				Network:     "tcp",
				DefaultPort: 5060,
			},
			wantErr: true,
		},
		{
			name: "unsupported base type",
			baseConn: func(tb testing.TB) any {
				tb.Helper()
				return struct{}{}
			},
			meta: sip.TransportMetadata{
				Proto:       "udp",
				Network:     "udp",
				DefaultPort: 5060,
			},
			wantErr: true,
		},
		{
			name: "invalid metadata for tcp",
			baseConn: func(tb testing.TB) any {
				tb.Helper()

				return &stubConn{raddr: net.TCPAddrFromAddrPort(raddr)}
			},
			meta:    sip.TransportMetadata{},
			wantErr: true,
		},
		{
			name: "invalid metadata for udp",
			baseConn: func(tb testing.TB) any {
				tb.Helper()

				return &stubPacketConn{laddr: net.UDPAddrFromAddrPort(laddr)}
			},
			meta:    sip.TransportMetadata{},
			wantErr: true,
		},
		{
			name: "metadata network mismatch tcp",
			baseConn: func(tb testing.TB) any {
				tb.Helper()

				return &stubConn{
					laddr: net.TCPAddrFromAddrPort(laddr),
					raddr: net.TCPAddrFromAddrPort(raddr),
				}
			},
			meta: sip.TransportMetadata{
				Proto:       "tcp",
				Network:     "udp",
				DefaultPort: 5060,
			},
			wantErr: true,
		},
		{
			name: "metadata network mismatch udp",
			baseConn: func(tb testing.TB) any {
				tb.Helper()

				return &stubPacketConn{laddr: net.UDPAddrFromAddrPort(laddr)}
			},
			meta: sip.TransportMetadata{
				Proto:       "udp",
				Network:     "tcp",
				DefaultPort: 5060,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base := tt.baseConn(t)

			var (
				conn sip.TransportConnection
				err  error
			)
			switch base := base.(type) {
			case net.Conn:
				conn, err = transport.NewConnection(t.Context(), base, tt.meta)
			case net.PacketConn:
				tp, tpErr := transport.NewConnectionLessTransport(tt.meta)
				if tpErr == nil {
					conn, err = transport.NewConnectionLessListener(t.Context(), base, tp)
				} else {
					err = tpErr
				}
			default:
				err = errors.Errorf("unsupported base type %T", base)
			}
			if tt.wantErr {
				if err == nil {
					t.Fatalf("connection constructor error = nil, want non-nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("connection constructor error = %v, want nil", err)
			}

			if conn == nil {
				t.Fatalf("transport.NewConnection() conn = nil, want non-nil")
			}

			t.Cleanup(func() { conn.Close(t.Context()) })

			if diff := cmp.Diff(tt.wantMeta, conn.Metadata()); diff != "" {
				t.Fatalf("transport.NewConnection() metadata mismatch (-want +got):\n%s", diff)
			}

			if got := conn.LocalAddr(); tt.wantLocAddr.IsValid() && got != tt.wantLocAddr {
				t.Fatalf("conn.LocalAddr() = %v, want %v", got, tt.wantLocAddr)
			}

			if got := conn.RemoteAddr(); tt.wantRmtAddr.IsValid() && got != tt.wantRmtAddr {
				t.Fatalf("conn.RemoteAddr() = %v, want %v", got, tt.wantRmtAddr)
			}
		})
	}
}

func TestConnection_Close_Idempotent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		new  func(tb testing.TB) sip.TransportConnection
	}{
		{name: "connected tcp", new: newTestTCPConn},
		{name: "packet udp", new: newTestPacketConn},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn := tt.new(t)
			if err := conn.Close(t.Context()); err != nil {
				t.Fatalf("conn.Close(t.Context()) first call error = %v, want nil", err)
			}

			if err := conn.Close(t.Context()); err != nil {
				t.Fatalf("conn.Close(t.Context()) second call error = %v, want nil", err)
			}
		})
	}
}

func TestConnection_AfterClose_WriteMessage_ReturnsNetErrClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "connected tcp",
			test: func(t *testing.T) {
				t.Helper()

				conn, _ := newTestTCPConnWithPeer(t)
				raddr := conn.RemoteAddr()

				if err := conn.Close(t.Context()); err != nil {
					t.Fatalf("conn.Close(t.Context()) error = %v, want nil", err)
				}

				err := conn.WriteMessage(t.Context(), newConnReq(t, conn), raddr)
				if !errors.Is(err, net.ErrClosed) {
					t.Fatalf("conn.WriteMessage() error = %v, want %v", err, net.ErrClosed)
				}
			},
		},
		{
			name: "packet udp",
			test: func(t *testing.T) {
				t.Helper()

				conn, peer := newTestPacketConnWithPeer(t)
				raddr := netip.MustParseAddrPort(peer.LocalAddr().String())

				if err := conn.Close(t.Context()); err != nil {
					t.Fatalf("conn.Close(t.Context()) error = %v, want nil", err)
				}

				err := conn.WriteMessage(t.Context(), newConnReq(t, conn), raddr)
				if !errors.Is(err, net.ErrClosed) {
					t.Fatalf("conn.WriteMessage() error = %v, want %v", err, net.ErrClosed)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.test(t)
		})
	}
}

func TestConnection_WriteMessage(t *testing.T) {
	t.Parallel()

	t.Run("invalid message returns error", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			test func(t *testing.T)
		}{
			{
				name: "connected tcp",
				test: func(t *testing.T) {
					t.Helper()

					conn, _ := newTestTCPConnWithPeer(t)

					err := conn.WriteMessage(t.Context(), &sip.Request{}, conn.RemoteAddr())
					if err == nil {
						t.Fatal("conn.WriteMessage() error = nil, want non-nil")
					}
				},
			},
			{
				name: "packet udp",
				test: func(t *testing.T) {
					t.Helper()

					conn, peer := newTestPacketConnWithPeer(t)
					raddr := netip.MustParseAddrPort(peer.LocalAddr().String())

					err := conn.WriteMessage(t.Context(), &sip.Request{}, raddr)
					if err == nil {
						t.Fatal("conn.WriteMessage() error = nil, want non-nil")
					}
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				tt.test(t)
			})
		}
	})

	t.Run("connected tcp address mismatch returns error", func(t *testing.T) {
		t.Parallel()

		conn, _ := newTestTCPConnWithPeer(t)
		raddr := conn.RemoteAddr()
		badAddr := netip.AddrPortFrom(raddr.Addr(), raddr.Port()+1)

		err := conn.WriteMessage(t.Context(), newConnReq(t, conn), badAddr)
		if err == nil {
			t.Fatal("conn.WriteMessage() error = nil, want non-nil")
		}
	})

	// t.Run("packet udp oversize message returns ErrMessageTooLarge", func(t *testing.T) {
	// 	t.Parallel()

	// 	conn, peer := newTestPacketConnWithPeer(t)
	// 	raddr := netip.MustParseAddrPort(peer.LocalAddr().String())
	// 	msg := newConnReq(t, conn)
	// 	msg.Body = bytes.Repeat([]byte("a"), 4000)

	// 	err := conn.WriteMessage(t.Context(), msg, raddr, nil)
	// 	if !errors.Is(err, sip.ErrMessageTooLarge) {
	// 		t.Fatalf("conn.WriteMessage() error = %v, want wraps %v", err, sip.ErrMessageTooLarge)
	// 	}

	// 	if !errors.Is(err, sip.ErrInvalidArgument) {
	// 		t.Fatalf("conn.WriteMessage() error = %v, want wraps %v", err, sip.ErrInvalidArgument)
	// 	}
	// })

	t.Run("happy path connected tcp sends message", func(t *testing.T) {
		t.Parallel()

		conn, peer := newTestTCPConnWithPeer(t)
		msg := newConnReq(t, conn)

		if err := conn.WriteMessage(t.Context(), msg, conn.RemoteAddr()); err != nil {
			t.Fatalf("conn.WriteMessage() error = %v, want nil", err)
		}

		received := readTCPMsg(t, peer)
		if _, ok := received.(*sip.Request); !ok {
			t.Fatalf("received message type = %T, want *sip.Request", received)
		}
	})

	t.Run("happy path packet udp sends message", func(t *testing.T) {
		t.Parallel()

		conn, peer := newTestPacketConnWithPeer(t)
		raddr := netip.MustParseAddrPort(peer.LocalAddr().String())
		msg := newConnReq(t, conn)

		if err := conn.WriteMessage(t.Context(), msg, raddr); err != nil {
			t.Fatalf("conn.WriteMessage() error = %v, want nil", err)
		}

		received := readUDPMsg(t, peer)
		if _, ok := received.(*sip.Request); !ok {
			t.Fatalf("received message type = %T, want *sip.Request", received)
		}
	})
}

func TestConnection_Messages(t *testing.T) {
	t.Parallel()

	t.Run("connected tcp receives inbound request envelope", func(t *testing.T) {
		t.Parallel()

		conn, peer := newTestTCPConnWithPeer(t)

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		results := make(chan msgResult, 1)
		go func() {
			for msg, err := range conn.Messages(ctx) {
				if msg != nil || err != nil {
					results <- msgResult{msg: msg, err: err}
					return
				}
			}

			results <- msgResult{}
		}()

		if _, err := peer.Write([]byte(rawReqMsg)); err != nil {
			t.Fatalf("peer.Write() error = %v, want nil", err)
		}

		res := waitMsgResult(t, results)
		if res.err != nil {
			t.Fatalf("conn.Messages() first error = %v, want nil", res.err)
		}

		assertInReqEnvelopeMeta(t, res.msg, conn.Metadata(), conn.LocalAddr(), conn.RemoteAddr())
	})

	t.Run("connected tcp oversize stream returns ErrMessageTooLarge", func(t *testing.T) {
		t.Parallel()

		conn, peer := newTestTCPConnWithPeer(t)

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		results := make(chan msgResult, 1)
		go func() {
			for msg, err := range conn.Messages(ctx) {
				if msg != nil || err != nil {
					results <- msgResult{msg: msg, err: err}
					return
				}
			}

			results <- msgResult{}
		}()

		overflow := bytes.Repeat([]byte("a"), int(math.MaxUint16)+1)
		if _, err := peer.Write(overflow); err != nil {
			t.Fatalf("peer.Write(overflow) error = %v, want nil", err)
		}

		res := waitMsgResult(t, results)
		if res.msg != nil {
			t.Fatalf("conn.Messages() first message = %T, want nil", res.msg)
		}

		if !errors.Is(res.err, sip.ErrMessageTooLarge) {
			t.Fatalf("conn.Messages() first error = %v, want wraps %v", res.err, sip.ErrMessageTooLarge)
		}
	})

	t.Run("connected tcp oversize in-message stream yields parse message with ErrMessageTooLarge", func(t *testing.T) {
		t.Parallel()

		conn, peer := newTestTCPConnWithPeer(t)

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		results := make(chan msgResult, 1)
		go func() {
			for msg, err := range conn.Messages(ctx) {
				if msg != nil || err != nil {
					results <- msgResult{msg: msg, err: err}
					return
				}
			}

			results <- msgResult{}
		}()

		msg := newConnReq(t, conn)
		msg.Body = bytes.Repeat([]byte("a"), int(math.MaxUint16))
		msg.Headers.Set(header.ContentLength(len(msg.Body)))

		if _, err := peer.Write([]byte(msg.Render())); err != nil {
			t.Fatalf("peer.Write(oversize-in-message) error = %v, want nil", err)
		}

		res := waitMsgResult(t, results)
		if res.msg != nil {
			t.Fatalf("conn.Messages() first message = %T, want nil", res.msg)
		}

		if !errors.Is(res.err, sip.ErrMessageTooLarge) {
			t.Fatalf("conn.Messages() first error = %v, want wraps %v", res.err, sip.ErrMessageTooLarge)
		}

		perr, ok := errors.AsType[*sip.ParseError](res.err)
		if !ok {
			t.Fatalf("conn.Messages() first error type = %T, want *sip.ParseError", res.err)
		}

		if perr.Msg == nil {
			t.Fatal("parse error message = nil, want non-nil")
		}

		assertInReqEnvelopeMeta(t, perr.Msg, conn.Metadata(), conn.LocalAddr(), conn.RemoteAddr())
	})

	t.Run("packet udp recovers after temporary read error", func(t *testing.T) {
		t.Parallel()

		laddr := netip.MustParseAddrPort("127.0.0.1:15061")
		raddr := netip.MustParseAddrPort("127.0.0.1:25061")
		base := &scriptedPacketConn{
			laddr: net.UDPAddrFromAddrPort(laddr),
			seq: []scriptedPacketReadResult{
				{err: temporaryReadError{}},
				{data: []byte(rawReqMsg), raddr: net.UDPAddrFromAddrPort(raddr)},
			},
		}

		tp, err := transport.NewConnectionLessTransport(sip.UDPMetadata())
		if err != nil {
			t.Fatalf("transport.NewConnectionLessTransport() error = %v, want nil", err)
		}

		conn, err := transport.NewConnectionLessListener(t.Context(), base, tp)
		if err != nil {
			t.Fatalf("transport.NewConnectionLessListener() error = %v, want nil", err)
		}

		t.Cleanup(func() { conn.Close(t.Context()) })

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		results := make(chan msgResult, 1)
		go func() {
			for msg, msgErr := range conn.Messages(ctx) {
				if msg != nil || msgErr != nil {
					results <- msgResult{msg: msg, err: msgErr}
					return
				}
			}

			results <- msgResult{}
		}()

		res := waitMsgResult(t, results)
		if res.err != nil {
			t.Fatalf("conn.Messages() first error = %v, want nil", res.err)
		}

		assertInReqEnvelopeMeta(t, res.msg, conn.Metadata(), conn.LocalAddr(), raddr)
	})

	t.Run("connected tcp recovers after temporary read error", func(t *testing.T) {
		t.Parallel()

		laddr := netip.MustParseAddrPort("127.0.0.1:15060")
		raddr := netip.MustParseAddrPort("127.0.0.1:25060")
		base := &scriptedReadConn{
			laddr: net.TCPAddrFromAddrPort(laddr),
			raddr: net.TCPAddrFromAddrPort(raddr),
			seq: []scriptedReadResult{
				{err: temporaryReadError{}},
				{data: []byte(rawReqMsg)},
			},
		}

		conn, err := transport.NewConnection(t.Context(), base, sip.TCPMetadata())
		if err != nil {
			t.Fatalf("transport.NewConnection() error = %v, want nil", err)
		}

		t.Cleanup(func() { conn.Close(t.Context()) })

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		results := make(chan msgResult, 1)
		go func() {
			for msg, msgErr := range conn.Messages(ctx) {
				if msg != nil || msgErr != nil {
					results <- msgResult{msg: msg, err: msgErr}
					return
				}
			}

			results <- msgResult{}
		}()

		res := waitMsgResult(t, results)
		if res.err != nil {
			t.Fatalf("conn.Messages() first error = %v, want nil", res.err)
		}

		assertInReqEnvelopeMeta(t, res.msg, conn.Metadata(), conn.LocalAddr(), raddr)
	})

	t.Run("packet udp parse error is skipped and valid message is yielded", func(t *testing.T) {
		t.Parallel()

		conn, peer := newTestPacketConnWithPeer(t)
		dst := net.UDPAddrFromAddrPort(conn.LocalAddr())
		raddr := netip.MustParseAddrPort(peer.LocalAddr().String())

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		results := make(chan msgResult, 1)
		go func() {
			for msg, err := range conn.Messages(ctx) {
				if msg != nil || err != nil {
					results <- msgResult{msg: msg, err: err}
					return
				}
			}

			results <- msgResult{}
		}()

		if _, err := peer.WriteTo([]byte("not a sip packet"), dst); err != nil {
			t.Fatalf("peer.WriteTo(invalid) error = %v, want nil", err)
		}

		if _, err := peer.WriteTo([]byte(rawReqMsg), dst); err != nil {
			t.Fatalf("peer.WriteTo(valid) error = %v, want nil", err)
		}

		res := waitMsgResult(t, results)
		if res.err != nil {
			t.Fatalf("conn.Messages() first error = %v, want nil", res.err)
		}

		assertInReqEnvelopeMeta(t, res.msg, conn.Metadata(), conn.LocalAddr(), raddr)
	})

	t.Run("packet udp CRLF keep-alive does not break message flow", func(t *testing.T) {
		t.Parallel()

		conn, peer := newTestPacketConnWithPeer(t)
		dst := net.UDPAddrFromAddrPort(conn.LocalAddr())
		raddr := netip.MustParseAddrPort(peer.LocalAddr().String())

		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()

		results := make(chan msgResult, 1)
		go func() {
			for msg, err := range conn.Messages(ctx) {
				if msg != nil || err != nil {
					results <- msgResult{msg: msg, err: err}
					return
				}
			}

			results <- msgResult{}
		}()

		if _, err := peer.WriteTo([]byte("\r\n\r\n"), dst); err != nil {
			t.Fatalf("peer.WriteTo(CRLFCRLF) error = %v, want nil", err)
		}

		buf := make([]byte, 16)

		peer.SetReadDeadline(time.Now().Add(time.Second))

		n, _, err := peer.ReadFrom(buf)
		if err != nil {
			t.Fatalf("peer.ReadFrom() keep-alive response error = %v, want nil", err)
		}

		if got := string(buf[:n]); got != "\r\n" {
			t.Fatalf("keep-alive response = %q, want %q", got, "\r\n")
		}

		if _, err := peer.WriteTo([]byte(rawReqMsg), dst); err != nil {
			t.Fatalf("peer.WriteTo(valid) error = %v, want nil", err)
		}

		res := waitMsgResult(t, results)
		if res.err != nil {
			t.Fatalf("conn.Messages() first error = %v, want nil", res.err)
		}

		assertInReqEnvelopeMeta(t, res.msg, conn.Metadata(), conn.LocalAddr(), raddr)
	})
}

func TestConnection_Messages_StopsOnContextCancel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		new  func(tb testing.TB) sip.TransportConnection
	}{
		{name: "connected tcp", new: newTestTCPConn},
		{name: "packet udp", new: newTestPacketConn},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn := tt.new(t)
			ctx, cancel := context.WithCancel(t.Context())
			errCh := make(chan error, 1)

			go func() {
				var gotErr error
				for _, err := range conn.Messages(ctx) {
					if err != nil {
						gotErr = err
						break
					}
				}

				errCh <- gotErr
			}()

			cancel()

			select {
			case err := <-errCh:
				if err == nil {
					t.Fatalf("conn.Messages() terminal error = nil, want non-nil")
				}

				if !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
					t.Fatalf("conn.Messages() terminal error = %v, want wraps %v or %v",
						err,
						context.Canceled,
						net.ErrClosed,
					)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("conn.Messages() did not stop after context cancellation")
			}
		})
	}
}

func TestConnection_MetadataAndAddr_Stable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		new        func(tb testing.TB) sip.TransportConnection
		wantLocal  bool
		wantRemote bool
	}{
		{name: "connected tcp", new: newTestTCPConn, wantLocal: true, wantRemote: true},
		{name: "packet udp", new: newTestPacketConn, wantLocal: true, wantRemote: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn := tt.new(t)
			metaBefore := conn.Metadata()
			laddrBefore := conn.LocalAddr()
			raddrBefore := conn.RemoteAddr()

			if tt.wantLocal != laddrBefore.IsValid() {
				t.Fatalf("conn.LocalAddr().IsValid() = %v, want %v", laddrBefore.IsValid(), tt.wantLocal)
			}

			if tt.wantRemote != raddrBefore.IsValid() {
				t.Fatalf("conn.RemoteAddr().IsValid() = %v, want %v", raddrBefore.IsValid(), tt.wantRemote)
			}

			if err := conn.Close(t.Context()); err != nil {
				t.Fatalf("conn.Close(t.Context()) error = %v, want nil", err)
			}

			if diff := cmp.Diff(metaBefore, conn.Metadata()); diff != "" {
				t.Fatalf("conn.Metadata() mismatch after close (-want +got):\n%s", diff)
			}

			if got := conn.LocalAddr(); got != laddrBefore {
				t.Fatalf("conn.LocalAddr() after close = %v, want %v", got, laddrBefore)
			}

			if got := conn.RemoteAddr(); got != raddrBefore {
				t.Fatalf("conn.RemoteAddr() after close = %v, want %v", got, raddrBefore)
			}
		})
	}
}
