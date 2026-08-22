package transport_test

import (
	"context"
	"io"
	"iter"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/dns"
	"github.com/ghettovoice/gosip/internal/errors"
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
	errOnce sync.Once
}

func (l *tempErrListener) Accept() (net.Conn, error) {
	var err error
	l.errOnce.Do(func() { err = tempError{} })

	if err != nil {
		return nil, err
	}

	return l.Listener.Accept()
}

type stubDNSResolver struct {
	lookupIP    func(ctx context.Context, network, host string) ([]net.IP, error)
	lookupSRV   func(ctx context.Context, service, proto, host string) ([]*dns.SRV, error)
	lookupNAPTR func(ctx context.Context, host string) ([]*dns.NAPTR, error)
}

func (s stubDNSResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	if s.lookupIP == nil {
		return nil, errors.New("lookup ip not configured")
	}
	return s.lookupIP(ctx, network, host)
}

func (s stubDNSResolver) LookupSRV(ctx context.Context, service, proto, host string) ([]*dns.SRV, error) {
	if s.lookupSRV == nil {
		return nil, errors.New("lookup srv not configured")
	}
	return s.lookupSRV(ctx, service, proto, host)
}

func (s stubDNSResolver) LookupNAPTR(ctx context.Context, host string) ([]*dns.NAPTR, error) {
	if s.lookupNAPTR == nil {
		return nil, errors.New("lookup naptr not configured")
	}
	return s.lookupNAPTR(ctx, host)
}

type multiTransportProvider struct {
	metas []sip.TransportMetadata
}

func (p *multiTransportProvider) TransportMetadataByProto(proto sip.TransportProto) (sip.TransportMetadata, bool) {
	for _, meta := range p.metas {
		if meta.Proto.Equal(proto) {
			return meta, true
		}
	}
	return sip.TransportMetadata{}, false
}

func (p *multiTransportProvider) TransportMetadataByNAPTRService(service string) (sip.TransportMetadata, bool) {
	for _, meta := range p.metas {
		if strings.EqualFold(meta.NAPTRService, service) {
			return meta, true
		}
	}
	return sip.TransportMetadata{}, false
}

func (p *multiTransportProvider) AllTransportMetadata() iter.Seq[sip.TransportMetadata] {
	return func(yield func(sip.TransportMetadata) bool) {
		for _, meta := range p.metas {
			if !yield(meta) {
				return
			}
		}
	}
}

func newMinReq(tb testing.TB) *sip.Request {
	tb.Helper()

	ruri := &sip.URI{User: sip.UserWithName("alice"), Addr: sip.AddrFromHost("example.com")}
	furi := &sip.URI{User: sip.UserWithName("bob"), Addr: sip.AddrFromHost("example.com")}
	turi := &sip.URI{User: sip.UserWithName("alice"), Addr: sip.AddrFromHost("example.com")}

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

	furi := &sip.URI{User: sip.UserWithName("bob"), Addr: sip.AddrFromHost("example.com")}
	turi := &sip.URI{User: sip.UserWithName("alice"), Addr: sip.AddrFromHost("example.com")}

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

// readTCPIdleTimeout is the max time to wait for the next chunk of data after
// the previous one. It is intentionally much smaller than asyncEventTimeout so
// that readTCPMsg returns quickly once the sender is done writing.
const readTCPIdleTimeout = 100 * time.Millisecond

func readTCPMsg(tb testing.TB, conn net.Conn) sip.Message {
	tb.Helper()

	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 2048)

	// Wait up to asyncEventTimeout for the very first byte.
	conn.SetReadDeadline(time.Now().Add(asyncEventTimeout))

	for {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			// Reset deadline: wait only a short idle interval for the next chunk.
			conn.SetReadDeadline(time.Now().Add(readTCPIdleTimeout))
		}

		if err != nil {
			if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
				break
			}

			if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
				break
			}

			tb.Fatalf("conn.Read() error = %v, want nil", err)
		}
	}

	if len(buf) == 0 {
		tb.Fatalf("conn.Read() read 0 bytes, want response data")
	}

	msg, err := sip.DefaultParser().ParsePacket(buf)
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

// Connless transport tests.
