package transport

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/netutil"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

func TestTransport_ContextPropagation(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		meta sip.TransportMetadata
		new  func(sip.TransportMetadata, ...TransportOptions) (
			sip.Transport,
			func(context.Context, *sip.RequestEnvelope) error,
			func(context.Context, *sip.ResponseEnvelope) error,
			error,
		)
	}{
		{
			"udp",
			sip.UDPMetadata(),
			func(meta sip.TransportMetadata, opts ...TransportOptions) (
				sip.Transport,
				func(context.Context, *sip.RequestEnvelope) error,
				func(context.Context, *sip.ResponseEnvelope) error,
				error,
			) {
				tp, err := NewConnLessTransport(meta, opts...)
				if err != nil {
					return nil, nil, nil, errors.Wrap(err)
				}
				return tp,
					func(ctx context.Context, req *sip.RequestEnvelope) error {
						return errors.Wrap(tp.recvReqSafe(ctx, req, nil))
					},
					func(ctx context.Context, res *sip.ResponseEnvelope) error {
						return errors.Wrap(tp.recvResSafe(ctx, res, nil))
					},
					nil
			},
		},
		{
			"tcp",
			sip.TCPMetadata(),
			func(meta sip.TransportMetadata, opts ...TransportOptions) (
				sip.Transport,
				func(context.Context, *sip.RequestEnvelope) error,
				func(context.Context, *sip.ResponseEnvelope) error,
				error,
			) {
				tp, err := NewConnOrientedTransport(meta, opts...)
				if err != nil {
					return nil, nil, nil, errors.Wrap(err)
				}
				return tp,
					func(ctx context.Context, req *sip.RequestEnvelope) error {
						return errors.Wrap(tp.recvReqSafe(ctx, req, nil))
					},
					func(ctx context.Context, res *sip.ResponseEnvelope) error {
						return errors.Wrap(tp.recvResSafe(ctx, res, nil))
					},
					nil
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pubAddr := sip.MakeHostPortAddr("127.0.0.1", tt.meta.DefaultPort)
			tp, recvReq, recvRes, err := tt.new(tt.meta, TransportOptions{PublicAddr: pubAddr})
			if err != nil {
				t.Fatalf("new transport() error = %v, want nil", err)
			}
			t.Cleanup(func() { _ = tp.Close() })

			locAddr := netip.MustParseAddrPort("127.0.0.1:5070")
			rmtAddr := netip.MustParseAddrPort("127.0.0.1:5080")
			req := newCtxPropagationReq(t, tt.meta, pubAddr, locAddr, rmtAddr)
			tx, err := sip.NewInviteClientTransaction(req, tp)
			if err != nil {
				t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
			}

			outReqCtxs := make(chan context.Context, 1)
			tp.UseOutboundRequestInterceptor(sip.OutboundRequestInterceptorFunc(
				func(ctx context.Context, _ sip.RequestSender, _ *sip.RequestEnvelope, _ ...sip.SendRequestOptions) error {
					outReqCtxs <- ctx
					return nil
				},
			))

			outResCtxs := make(chan context.Context, 1)
			tp.UseOutboundResponseInterceptor(sip.OutboundResponseInterceptorFunc(
				func(ctx context.Context, _ sip.ResponseSender, _ *sip.ResponseEnvelope, _ ...sip.SendResponseOptions) error {
					outResCtxs <- ctx
					return nil
				},
			))

			inReqCtxs := make(chan context.Context, 1)
			tp.UseInboundRequestInterceptor(sip.InboundRequestInterceptorFunc(
				func(ctx context.Context, _ sip.RequestReceiver, _ *sip.RequestEnvelope) error {
					inReqCtxs <- ctx
					return nil
				},
			))

			inResCtxs := make(chan context.Context, 1)
			tp.UseInboundResponseInterceptor(sip.InboundResponseInterceptorFunc(
				func(ctx context.Context, _ sip.ResponseReceiver, _ *sip.ResponseEnvelope) error {
					inResCtxs <- ctx
					return nil
				},
			))

			parentCtx := sip.ContextWithTransaction(t.Context(), tx)

			if err := tp.SendRequest(parentCtx, req); err != nil {
				t.Fatalf("tp.SendRequest() error = %v, want nil", err)
			}

			assertTpInceptCtx(t,
				testutil.WaitResult(t, outReqCtxs, "outbound request interceptor context"),
				tx, tp, true,
			)

			res, err := req.NewResponse(sip.ResponseStatusOK)
			if err != nil {
				t.Fatalf("req.NewResponse(200) error = %v, want nil", err)
			}
			if err := tp.SendResponse(parentCtx, res); err != nil {
				t.Fatalf("tp.SendResponse() error = %v, want nil", err)
			}

			assertTpInceptCtx(t,
				testutil.WaitResult(t, outResCtxs, "outbound response interceptor context"),
				tx, tp, true,
			)

			if err := recvReq(parentCtx, req); err != nil {
				t.Fatalf("recvReqSafe() error = %v, want nil", err)
			}

			assertTpInceptCtx(t,
				testutil.WaitResult(t, inReqCtxs, "inbound request interceptor context"),
				tx, tp, false,
			)

			if err := recvRes(parentCtx, res); err != nil {
				t.Fatalf("recvResSafe() error = %v, want nil", err)
			}

			assertTpInceptCtx(t,
				testutil.WaitResult(t, inResCtxs, "inbound response interceptor context"),
				tx, tp, false,
			)
		})
	}
}

func newCtxPropagationReq(
	tb testing.TB,
	meta sip.TransportMetadata,
	viaAddr sip.Addr,
	localAddr,
	remoteAddr netip.AddrPort,
) *sip.RequestEnvelope {
	tb.Helper()

	msg := &sip.Request{
		Proto:  sip.ProtoVer20(),
		Method: sip.RequestMethodInvite,
		URI:    &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("example.com")},
		Headers: make(sip.Headers).
			Set(header.Via{{
				Proto:     sip.ProtoVer20(),
				Transport: meta.Proto,
				Addr:      viaAddr,
				Params:    make(sip.Values).Set("branch", sip.MagicCookie+".transport-context"),
			}}).
			Set(&header.From{
				URI:    &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("example.com")},
				Params: make(sip.Values).Set("tag", "from-tag"),
			}).
			Set(&header.To{URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("example.com")}}).
			Set(header.CallID("transport-context")).
			Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInvite}),
	}

	return sip.NewRequestEnvelope(msg).
		SetTransport(meta).
		SetLocalAddr(localAddr).
		SetRemoteAddr(remoteAddr)
}

func assertTpInceptCtx(
	t *testing.T,
	ctx context.Context,
	tx sip.Transaction,
	tp sip.Transport,
	wantTx bool,
) {
	t.Helper()

	if got, ok := sip.TransportFromContext(ctx); !ok || got != tp {
		t.Fatalf("sip.TransportFromContext(ctx) = (%v, %t), want %v", got, ok, tp)
	}
	gotTx, ok := sip.TransactionFromContext(ctx)
	if wantTx {
		if !ok || gotTx != tx {
			t.Fatalf("sip.TransactionFromContext(ctx) = (%v, %t), want %v", gotTx, ok, tx)
		}
	} else if ok || gotTx != nil {
		t.Fatalf("sip.TransactionFromContext(inbound ctx) = (%v, %t), want (nil, false)", gotTx, ok)
	}
}

func TestTransportOptions_pubAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts TransportOptions
		want sip.Addr
	}{
		{
			"valid host with port is returned as is",
			TransportOptions{PublicAddr: sip.MakeHostPortAddr("example.com", 5060)},
			sip.MakeHostPortAddr("example.com", 5060),
		},
		{
			"valid ip with port is returned as is",
			TransportOptions{PublicAddr: sip.MakeIPPortAddr(netip.MustParseAddr("192.168.1.1").AsSlice(), 5060)},
			sip.MakeIPPortAddr(netip.MustParseAddr("192.168.1.1").AsSlice(), 5060),
		},
		{
			"host only is returned as is",
			TransportOptions{PublicAddr: sip.MakeHostAddr("example.com")},
			sip.MakeHostAddr("example.com"),
		},
		{
			"ip only is returned as is",
			TransportOptions{PublicAddr: sip.MakeIPAddr(netip.MustParseAddr("192.168.1.1").AsSlice())},
			sip.MakeIPAddr(netip.MustParseAddr("192.168.1.1").AsSlice()),
		},
		{
			"host with zero port keeps placeholder",
			TransportOptions{PublicAddr: sip.MakeHostPortAddr("example.com", 0)},
			sip.MakeHostPortAddr("example.com", 0),
		},
		{
			"ip with zero port keeps placeholder",
			TransportOptions{PublicAddr: sip.MakeIPPortAddr(netip.MustParseAddr("192.168.1.1").AsSlice(), 0)},
			sip.MakeIPPortAddr(netip.MustParseAddr("192.168.1.1").AsSlice(), 0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.opts.pubAddr()
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("TransportOptions.pubAddr() = %+v, want %+v\ndiff (-want +got):\n%s", got, tt.want, diff)
			}
		})
	}
}

func TestTransportOptions_pubAddr_DynamicHost(t *testing.T) {
	t.Parallel()

	hostIP, err := netutil.HostIP()
	if err != nil {
		t.Skipf("no usable host IPv4 address: %v", err)
	}

	hostIPv4 := hostIP.To4()
	if hostIPv4 == nil {
		t.Skipf("host IP %v has no usable IPv4 address", hostIP)
	}

	tests := []struct {
		name string
		opts TransportOptions
		want sip.Addr
	}{
		{
			"zero address returns host IP",
			TransportOptions{PublicAddr: sip.Addr{}},
			sip.MakeIPAddr(hostIPv4),
		},
		{
			"unspecified ip with port returns host IP with that port",
			TransportOptions{PublicAddr: sip.MakeIPPortAddr(netip.IPv4Unspecified().AsSlice(), 0)},
			sip.MakeIPPortAddr(hostIPv4, 0),
		},
		{
			"unspecified ip without port returns host IP",
			TransportOptions{PublicAddr: sip.MakeHostAddr("0.0.0.0")},
			sip.MakeIPAddr(hostIPv4),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.opts.pubAddr()
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("transport.TransportOptions.pubAddr() = %+v, want %+v\ndiff (-want +got):\n%s", got, tt.want, diff)
			}
		})
	}
}

func TestTransportOptions_pubAddr_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		opts              TransportOptions
		shouldUseDirectly bool
	}{
		{
			"valid complete address should be used directly",
			TransportOptions{PublicAddr: sip.MakeHostPortAddr("example.com", 5060)},
			true,
		},
		{
			"valid ip with port should be used directly",
			TransportOptions{PublicAddr: sip.MakeIPPortAddr(netip.MustParseAddr("192.168.1.1").AsSlice(), 5060)},
			true,
		},
		{
			"ip without port should be used directly",
			TransportOptions{PublicAddr: sip.MakeIPAddr(netip.MustParseAddr("123.123.123.123").AsSlice())},
			true,
		},
		{
			"zero port should trigger finalization",
			TransportOptions{PublicAddr: sip.MakeHostPortAddr("example.com", 0)},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := tt.opts.pubAddr()

			// Check if the result would be used directly or need finalization
			needsFinalization := !result.IsValid() ||
				(result.IP() != nil && result.IP().IsUnspecified()) ||
				func() bool { p, ok := result.Port(); return ok && p == 0 }()

			if tt.shouldUseDirectly == needsFinalization {
				t.Errorf(
					"transport.TransportOptions.pubAddr() validation failed: "+
						"expected direct use=%v, but needs finalization=%v",
					tt.shouldUseDirectly,
					needsFinalization,
				)
			}
		})
	}
}

type testTrackedListener struct {
	addr netip.AddrPort
}

func (*testTrackedListener) Metadata() sip.TransportMetadata { return sip.UDPMetadata() }
func (l *testTrackedListener) LocalAddr() netip.AddrPort     { return l.addr }
func (*testTrackedListener) Serve(context.Context) error     { return nil }
func (*testTrackedListener) Close() error                    { return nil }
func (l *testTrackedListener) String() string                { return l.addr.String() }
func (*testTrackedListener) LogValue() slog.Value            { return slog.StringValue("test listener") }
func (*testTrackedListener) isClosed() bool                  { return false }

func TestTranspBase_UntrackListener_PreservesReplacement(t *testing.T) {
	t.Parallel()

	addr := netip.MustParseAddrPort("127.0.0.1:5060")
	oldLis := &trackedListener{
		transpListener: &testTrackedListener{addr: addr},
		borrowed:       true,
	}
	newLis := &trackedListener{
		transpListener: &testTrackedListener{addr: addr},
		borrowed:       true,
	}

	var tb transpBase[net.PacketConn]
	tb.lisMap.Store(addr, newLis)

	tb.untrackLis(t.Context(), oldLis)

	got, ok := tb.lisMap.Load(addr)
	if !ok {
		t.Fatal("tb.lisMap.Load() found = false, want true")
	}
	if got != newLis {
		t.Fatalf("tb.lisMap.Load() = %p, want %p", got, newLis)
	}
}

func TestTranspBase_UntrackConn_PreservesReplacement(t *testing.T) {
	t.Parallel()

	l := netip.MustParseAddrPort("127.0.0.1:5060")
	r := netip.MustParseAddrPort("127.0.0.2:5060")
	old := &trackedConn{Conn: &Conn{}, borrowed: true}
	old.laddr, old.raddr = l, r
	replacement := &trackedConn{Conn: &Conn{}, borrowed: true}
	replacement.laddr, replacement.raddr = l, r

	var tb transpBase[net.Listener]
	tb.log = slog.Default()

	bucket := &connBucket{}
	bucket.Store(l, replacement)
	tb.connMap.Store(r, bucket)

	tb.untrackConn(t.Context(), old)

	got, ok := bucket.Load(l)
	if !ok || got != replacement {
		t.Fatalf("bucket.Load() = %p, %v, want replacement %p, true", got, ok, replacement)
	}
	if b, ok := tb.connMap.Load(r); !ok || b != bucket {
		t.Fatalf("tb.connMap.Load() = %p, %v, want bucket %p, true", b, ok, bucket)
	}
}

func TestTranspBase_UntrackConn(t *testing.T) {
	t.Parallel()

	l := netip.MustParseAddrPort("127.0.0.1:5060")
	l2 := netip.MustParseAddrPort("127.0.0.1:5061")
	r := netip.MustParseAddrPort("127.0.0.2:5060")

	newConn := func(l, r netip.AddrPort) *trackedConn {
		conn := &trackedConn{Conn: &Conn{}, borrowed: true}
		conn.laddr, conn.raddr = l, r
		return conn
	}

	tests := []struct {
		name          string
		setup         func() (*transpBase[net.Listener], *connBucket)
		conn          *trackedConn
		wantSibling   bool
		wantBucketReg bool
	}{
		{
			"removes tracked connection and empty remote bucket",
			func() (*transpBase[net.Listener], *connBucket) {
				var tb transpBase[net.Listener]
				bucket := &connBucket{}
				bucket.Store(l, newConn(l, r))
				tb.connMap.Store(r, bucket)
				return &tb, bucket
			},
			newConn(l, r),
			false,
			false,
		},
		{
			"removes tracked connection and keeps siblings",
			func() (*transpBase[net.Listener], *connBucket) {
				var tb transpBase[net.Listener]
				bucket := &connBucket{}
				bucket.Store(l, newConn(l, r))
				bucket.Store(l2, newConn(l2, r))
				tb.connMap.Store(r, bucket)
				return &tb, bucket
			},
			newConn(l, r),
			true,
			true,
		},
		{
			"missing connection leaves siblings untouched",
			func() (*transpBase[net.Listener], *connBucket) {
				var tb transpBase[net.Listener]
				bucket := &connBucket{}
				bucket.Store(l2, newConn(l2, r))
				tb.connMap.Store(r, bucket)
				return &tb, bucket
			},
			newConn(l, r),
			true,
			true,
		},
		{
			"missing remote bucket leaves map untouched",
			func() (*transpBase[net.Listener], *connBucket) {
				var tb transpBase[net.Listener]
				return &tb, &connBucket{}
			},
			newConn(l, r),
			false,
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tb, bucket := tt.setup()
			tb.log = slog.Default()

			sibling, _ := bucket.Load(l2)

			target := tt.conn
			if stored, ok := bucket.Load(tt.conn.laddr); ok {
				target = stored
			}

			tb.untrackConn(t.Context(), target)

			if _, ok := bucket.Load(target.laddr); ok {
				t.Fatalf("bucket.Load(%v) found removed connection", target.laddr)
			}

			gotSibling, ok := bucket.Load(l2)
			if ok != tt.wantSibling {
				t.Fatalf("bucket.Load(%v) found = %v, want %v", l2, ok, tt.wantSibling)
			}
			if ok && sibling != nil && gotSibling != sibling {
				t.Fatalf("bucket.Load(%v) = %p, want sibling %p", l2, gotSibling, sibling)
			}

			if _, ok := tb.connMap.Load(r); ok != tt.wantBucketReg {
				t.Fatalf("tb.connMap.Load(%v) found = %v, want %v", r, ok, tt.wantBucketReg)
			}
		})
	}
}

// Closing a listener returned by Transport.Listen drops its registry
// record; a repeated Close of the discarded wrapper must not remove a
// replacement bound to the same local address.
func TestTrackedListener_Close_UntracksRecord(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	tests := []struct {
		name   string
		listen func(ctx context.Context, addr string) (sip.TransportListener, error)
		load   func(laddr netip.AddrPort) (*trackedListener, bool)
		close  func() error
	}{
		{name: "UDP"},
		{name: "TCP"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			switch tt.name {
			case "UDP":
				tp, err := NewConnLessTransport(sip.UDPMetadata())
				if err != nil {
					t.Fatalf("transport.NewConnLessTransport() error = %v, want nil", err)
				}
				tt.listen = tp.Listen
				tt.load = tp.lisMap.Load
				tt.close = tp.Close
			case "TCP":
				tp, err := NewConnOrientedTransport(sip.TCPMetadata())
				if err != nil {
					t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
				}
				tt.listen = tp.Listen
				tt.load = tp.lisMap.Load
				tt.close = tp.Close
			default:
				t.Fatalf("unknown transport %q", tt.name)
			}
			t.Cleanup(func() { _ = tt.close() })

			lis, err := tt.listen(ctx, "127.0.0.1:0")
			if err != nil {
				t.Fatalf("tp.Listen() error = %v, want nil", err)
			}

			laddr := lis.LocalAddr()
			if _, ok := tt.load(laddr); !ok {
				t.Fatal("listener not tracked after Listen")
			}
			if err := lis.Close(); err != nil {
				t.Fatalf("lis.Close() error = %v, want nil", err)
			}
			if _, ok := tt.load(laddr); ok {
				t.Fatal("closed listener still tracked, want removed from the registry")
			}

			// A replacement bound to the same address must survive a repeated
			// Close of the discarded wrapper.
			lis2, err := tt.listen(ctx, laddr.String())
			if err != nil {
				t.Fatalf("rebind tp.Listen() error = %v, want nil", err)
			}
			t.Cleanup(func() { _ = lis2.Close() })

			got, ok := tt.load(laddr)
			if !ok || got != lis2 {
				t.Fatalf("rebound listener record = (%p, %v), want %p, true", got, ok, lis2)
			}
			if err := lis.Close(); err != nil {
				t.Fatalf("repeated lis.Close() error = %v, want nil", err)
			}

			got, ok = tt.load(laddr)
			if !ok || got != lis2 {
				t.Fatalf("record after repeated close = (%p, %v), want replacement %p, true", got, ok, lis2)
			}
			if trLis2, ok2 := lis2.(*trackedListener); !ok2 || trLis2.isClosed() {
				t.Fatal("replacement listener closed by the discarded wrapper, want still open")
			}
		})
	}
}

type (
	tpOpCtxKey  struct{}
	tpSrvCtxKey struct{}
)

type dialCtxRecorder struct {
	dialer       net.Dialer
	contexts     chan context.Context
	calls        atomic.Int32
	successCalls atomic.Int32
}

func newDialCtxRecorder() *dialCtxRecorder {
	return &dialCtxRecorder{contexts: make(chan context.Context, 2)}
}

func (d *dialCtxRecorder) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	d.calls.Add(1)
	d.contexts <- ctx
	conn, err := d.dialer.DialContext(ctx, network, address)
	if err == nil {
		d.successCalls.Add(1)
	}
	return errors.Wrap2(conn, err)
}

type fixedConnDialer struct {
	conn         net.Conn
	err          error
	calls        atomic.Int32
	successCalls atomic.Int32
}

func (d *fixedConnDialer) Dial(context.Context, string, string) (net.Conn, error) {
	d.calls.Add(1)
	if d.err != nil {
		return nil, errors.Wrap(d.err)
	}
	d.successCalls.Add(1)
	return d.conn, nil
}

type ctxDialableTransp interface {
	sip.Transport
	AcquireConn(ctx context.Context, raddr netip.AddrPort, opts ...AcquireConnOptions) (sip.TransportConn, error)
}

func newCtxDialableTransp(meta sip.TransportMetadata, dialer ConnDialer) (ctxDialableTransp, error) {
	opts := TransportOptions{
		ConnDialer:      dialer,
		ConnReadTimeout: -1,
		ConnIdleTimeout: -1,
	}
	if meta.Streamed() {
		return errors.Wrap2(NewConnOrientedTransport(meta, opts))
	}
	return errors.Wrap2(NewConnLessTransport(meta, opts))
}

func TestTransport_Dial_SeparatesOperationAndInboundContexts(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name         string
		meta         sip.TransportMetadata
		customSrvCtx bool
	}{
		{"tcp default", sip.TCPMetadata(), false},
		{"tcp custom", sip.TCPMetadata(), true},
		{"udp default", sip.UDPMetadata(), false},
		{"udp custom", sip.UDPMetadata(), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			meta := tt.meta

			var (
				tcpLis  *net.TCPListener
				udpPeer net.PacketConn
				err     error
			)
			if meta.Streamed() {
				tcpLis, err = net.ListenTCP(meta.Network, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
				if err != nil {
					t.Fatalf("net.Listen() error = %v, want nil", err)
				}
				t.Cleanup(func() { _ = tcpLis.Close() })
			} else {
				udpPeer, err = net.ListenPacket(meta.Network, "127.0.0.1:0")
				if err != nil {
					t.Fatalf("net.ListenPacket() error = %v, want nil", err)
				}
				t.Cleanup(func() { _ = udpPeer.Close() })
			}

			rmtAddr := netip.MustParseAddrPort(func() string {
				if tcpLis != nil {
					return tcpLis.Addr().String()
				}
				return udpPeer.LocalAddr().String()
			}())
			dialer := newDialCtxRecorder()
			tp, err := newCtxDialableTransp(meta, dialer)
			if err != nil {
				t.Fatalf("newCtxDialableTransp() error = %v, want nil", err)
			}
			t.Cleanup(func() { _ = tp.Close() })

			// The default serving context is independent of the test and acquisition contexts.
			srvCtx := context.Background() //nolint:usetesting // Must not inherit the test context.
			var srvCancel context.CancelFunc
			var srvFactoryCalls atomic.Int32
			var successCallsAtFactory atomic.Int32
			var srvCtxFactory func() context.Context
			if tt.customSrvCtx {
				srvCtx, srvCancel = context.WithDeadline(
					context.WithValue(t.Context(), tpSrvCtxKey{}, "serve"),
					time.Now().Add(time.Minute),
				)
				t.Cleanup(srvCancel)
				srvCtxFactory = func() context.Context {
					srvFactoryCalls.Add(1)
					successCallsAtFactory.Store(dialer.successCalls.Load())
					return srvCtx
				}
			}
			srvCtxState := servingCtxState{
				value: srvCtx.Value(tpSrvCtxKey{}),
				done:  srvCtx.Done(),
			}
			srvCtxState.deadline, srvCtxState.hasDeadline = srvCtx.Deadline()

			inCtxs := make(chan context.Context, 2)
			tp.UseInboundRequestInterceptor(sip.InboundRequestInterceptorFunc(
				func(ctx context.Context, _ sip.RequestReceiver, _ *sip.RequestEnvelope) error {
					inCtxs <- ctx
					return nil
				},
			))

			req := newCtxPropagationReq(
				t,
				meta,
				sip.MakeHostPortAddr("127.0.0.1", rmtAddr.Port()),
				netip.MustParseAddrPort("127.0.0.1:5070"),
				rmtAddr,
			)
			req.WithMessage(func(msg *sip.Request) {
				msg.Headers.Set(header.ContentLength(0))
			})
			tx, err := sip.NewInviteClientTransaction(req, tp)
			if err != nil {
				t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
			}

			unrelatedElem := &sip.Element{}
			t.Cleanup(func() { _ = unrelatedElem.Close() })

			baseCtx, deadlineCancel := context.WithDeadline(
				context.WithValue(t.Context(), tpOpCtxKey{}, "dial"),
				time.Now().Add(10*time.Second),
			)
			t.Cleanup(deadlineCancel)

			opCtx := sip.ContextWithTransaction(sip.ContextWithElement(baseCtx, unrelatedElem), tx)
			dialCtx, opCancel := context.WithCancel(opCtx)

			conn, err := tp.AcquireConn(
				dialCtx,
				rmtAddr,
				AcquireConnOptions{
					Dial:         true,
					ServeContext: srvCtxFactory,
				},
			)
			if err != nil {
				t.Fatalf("tp.AcquireConn() error = %v, want nil", err)
			}

			actualDialCtx := testutil.WaitResult(t, dialer.contexts, "transport dial context")
			opDeadline, _ := dialCtx.Deadline()
			assertOpCtx(t, actualDialCtx, tx, tp, unrelatedElem, opDeadline)
			wantFactoryCalls := int32(0)
			if tt.customSrvCtx {
				wantFactoryCalls = 1
				if got := successCallsAtFactory.Load(); got != 1 {
					t.Errorf("successful dial calls when ServeContext ran = %d, want 1", got)
				}
			}
			if got := srvFactoryCalls.Load(); got != wantFactoryCalls {
				t.Errorf("opts.ServeContext() factory calls after dial = %d, want %d", got, wantFactoryCalls)
			}

			var writeInbound func([]byte) error
			if meta.Streamed() {
				if err := tcpLis.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					t.Fatalf("tcpLis.SetDeadline() error = %v, want nil", errors.Wrap(err))
				}

				peer, acceptErr := tcpLis.AcceptTCP()
				if acceptErr != nil {
					t.Fatalf("tcpLis.AcceptTCP() error = %v, want nil", errors.Wrap(acceptErr))
				}
				t.Cleanup(func() { _ = peer.Close() })

				writeInbound = func(data []byte) error {
					if err := peer.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
						return errors.Wrap(err)
					}
					_, writeErr := peer.Write(data)
					return errors.Wrap(writeErr)
				}
			} else {
				peerAddr := net.UDPAddrFromAddrPort(conn.LocalAddr())
				writeInbound = func(data []byte) error {
					_, writeErr := udpPeer.WriteTo(data, peerAddr)
					return errors.Wrap(writeErr)
				}
			}

			opCancel()
			if !errors.Is(actualDialCtx.Err(), context.Canceled) {
				t.Fatalf("dial context error = %v, want %v after cancel", actualDialCtx.Err(), context.Canceled)
			}

			for range 2 {
				if err := writeInbound([]byte(req.Message().Render())); err != nil {
					t.Fatalf("writeInbound() error = %v, want nil", err)
				}
				assertFreshInCtx(t,
					testutil.WaitResult(t, inCtxs, "inbound request interceptor context"),
					tp, srvCtxState,
				)
			}

			var reuseFactoryCalls atomic.Int32
			reused, err := tp.AcquireConn(
				t.Context(),
				rmtAddr,
				AcquireConnOptions{
					Dial: true,
					ServeContext: func() context.Context {
						reuseFactoryCalls.Add(1)
						return nil
					},
				},
			)
			if err != nil {
				t.Fatalf("tp.AcquireConn(reuse) error = %v, want nil", err)
			}
			if reused != conn {
				t.Errorf("reused connection = %v, want same instance %v", reused, conn)
			}
			if reused.LocalAddr() != conn.LocalAddr() {
				t.Errorf("reused connection local address = %v, want %v", reused.LocalAddr(), conn.LocalAddr())
			}
			if got := dialer.calls.Load(); got != 1 {
				t.Errorf("dial calls after reuse = %d, want 1", got)
			}
			if got := reuseFactoryCalls.Load(); got != 0 {
				t.Errorf("ServeContext reuse factory calls = %d, want 0", got)
			}
			if err := writeInbound([]byte(req.Message().Render())); err != nil {
				t.Fatalf("writeInbound(reused connection) error = %v, want nil", err)
			}
			assertFreshInCtx(t,
				testutil.WaitResult(t, inCtxs, "reused connection inbound context"),
				tp, srvCtxState,
			)

			if tt.customSrvCtx {
				servingConn, ok := conn.(*Conn)
				if !ok {
					t.Fatalf("dialed connection type = %T, want *Conn", conn)
				}
				srvCancel()
				testutil.WaitClosed(t, servingConn.closed, "custom serve context cancellation")
			}
			if err := tp.Close(); err != nil {
				t.Fatalf("tp.Close() error = %v, want nil", err)
			}
			if err := conn.WriteMessage(t.Context(), req.Message(), rmtAddr); err == nil {
				t.Fatal("conn.WriteMessage() after transport close error = nil, want error")
			}
		})
	}
}

func assertOpCtx(
	t *testing.T,
	ctx context.Context,
	tx sip.Transaction,
	tp sip.Transport,
	elm *sip.Element,
	wantDeadline time.Time,
) {
	t.Helper()

	if got, ok := sip.TransactionFromContext(ctx); !ok || got != tx {
		t.Errorf("sip.TransactionFromContext(dial ctx) = (%v, %t), want %v", got, ok, tx)
	}
	if got, ok := sip.TransportFromContext(ctx); !ok || got != tp {
		t.Errorf("sip.TransportFromContext(dial ctx) = (%v, %t), want %v", got, ok, tp)
	}
	if got, ok := sip.ElementFromContext(ctx); !ok || got != elm {
		t.Errorf("sip.ElementFromContext(dial ctx) = (%v, %t), want %v", got, ok, elm)
	}
	if got := ctx.Value(tpOpCtxKey{}); got != "dial" {
		t.Errorf("dial context value = %v, want dial", got)
	}
	if got, ok := ctx.Deadline(); !ok || !got.Equal(wantDeadline) {
		t.Errorf("dial context deadline = (%v, %t), want %v", got, ok, wantDeadline)
	}
}

type servingCtxState struct {
	value       any
	deadline    time.Time
	hasDeadline bool
	done        <-chan struct{}
}

func assertFreshInCtx(t *testing.T, ctx context.Context, tp sip.Transport, serveCtx servingCtxState) {
	t.Helper()

	if got, ok := sip.TransportFromContext(ctx); !ok || got != tp {
		t.Errorf("sip.TransportFromContext(inbound ctx) = (%v, %t), want %v", got, ok, tp)
	}
	if got, ok := sip.TransactionFromContext(ctx); ok || got != nil {
		t.Errorf("sip.TransactionFromContext(inbound ctx) = (%v, %t), want (nil, false)", got, ok)
	}
	if got, ok := sip.ElementFromContext(ctx); ok || got != nil {
		t.Errorf("sip.ElementFromContext(inbound ctx) = (%v, %t), want (nil, false)", got, ok)
	}
	if got := ctx.Value(tpOpCtxKey{}); got != nil {
		t.Errorf("inbound context operation value = %v, want nil", got)
	}
	if got := ctx.Value(tpSrvCtxKey{}); got != serveCtx.value {
		t.Errorf("inbound context serving value = %v, want %v", got, serveCtx.value)
	}
	gotDeadline, gotDeadlineSet := ctx.Deadline()
	if gotDeadlineSet != serveCtx.hasDeadline || serveCtx.hasDeadline && !gotDeadline.Equal(serveCtx.deadline) {
		t.Errorf("inbound context deadline = (%v, %t), want (%v, %t)",
			gotDeadline,
			gotDeadlineSet,
			serveCtx.deadline,
			serveCtx.hasDeadline,
		)
	}
	if err := ctx.Err(); err != nil {
		t.Errorf("inbound context error = %v, want nil", err)
	}
	if ctx.Done() != serveCtx.done {
		t.Error("inbound context Done channel differs from serving context")
	}
	if done := ctx.Done(); done != nil {
		select {
		case <-done:
			t.Error("inbound context is canceled")
		default:
		}
	}
}

func TestTransport_AcquireConn_ServeContextErrors(t *testing.T) {
	t.Parallel()

	const dialFailure errors.Error = "fixture dial failure"
	rmtAddr := netip.MustParseAddrPort("127.0.0.1:5099")
	for _, meta := range []sip.TransportMetadata{sip.TCPMetadata(), sip.UDPMetadata()} {
		t.Run(string(meta.Proto), func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name           string
				dial           bool
				dialErr        error
				withConn       bool
				wantErr        error
				wantAnyErr     bool
				wantDialCalls  int32
				wantFactory    int32
				wantCloseCalls int32
			}{
				{
					name:    "dial disabled",
					wantErr: ErrConnNotFound,
				},
				{
					name:          "dial failure",
					dial:          true,
					dialErr:       dialFailure,
					wantErr:       dialFailure,
					wantDialCalls: 1,
				},
				{
					name:           "nil serve context",
					dial:           true,
					withConn:       true,
					wantAnyErr:     true,
					wantDialCalls:  1,
					wantFactory:    1,
					wantCloseCalls: 1,
				},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()

					var idleConn *stubIdleConn
					if tt.withConn {
						idleConn = &stubIdleConn{
							laddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5070},
							raddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5099},
						}
					}
					dialer := &fixedConnDialer{conn: idleConn, err: tt.dialErr}
					tp, err := newCtxDialableTransp(meta, dialer)
					if err != nil {
						t.Fatalf("newCtxDialableTransp() error = %v, want nil", errors.Wrap(err))
					}
					t.Cleanup(func() { _ = tp.Close() })

					var serveFactoryCalls atomic.Int32
					var successfulCallsAtFactory atomic.Int32
					conn, err := tp.AcquireConn(
						t.Context(),
						rmtAddr,
						AcquireConnOptions{
							Dial: tt.dial,
							ServeContext: func() context.Context {
								serveFactoryCalls.Add(1)
								successfulCallsAtFactory.Store(dialer.successCalls.Load())
								return nil
							},
						},
					)

					switch {
					case tt.wantErr != nil:
						if !errors.Is(err, tt.wantErr) {
							t.Fatalf("tp.AcquireConn() error = %v, want %v", errors.Wrap(err), tt.wantErr)
						}
					case tt.wantAnyErr:
						if err == nil {
							t.Fatal("tp.AcquireConn() error = nil, want nil serve context error")
						}
					default:
						if err != nil {
							t.Fatalf("tp.AcquireConn() error = %v, want nil", errors.Wrap(err))
						}
					}
					if conn != nil {
						t.Fatalf("tp.AcquireConn() connection = %v, want nil", conn)
					}
					if got := dialer.calls.Load(); got != tt.wantDialCalls {
						t.Errorf("Dial calls = %d, want %d", got, tt.wantDialCalls)
					}
					if got := serveFactoryCalls.Load(); got != tt.wantFactory {
						t.Errorf("opts.ServeContext() factory calls = %d, want %d", got, tt.wantFactory)
					}
					if tt.withConn {
						if got := successfulCallsAtFactory.Load(); got != 1 {
							t.Errorf("successful dial calls when opts.ServeContext() ran = %d, want 1", got)
						}
						if got := idleConn.closes.Load(); got != tt.wantCloseCalls {
							t.Errorf("dialed socket Close calls = %d, want %d", got, tt.wantCloseCalls)
						}
					}

					if err := tp.Close(); err != nil {
						t.Fatalf("tp.Close() error = %v, want nil", errors.Wrap(err))
					}
					if tt.withConn && idleConn.closes.Load() != tt.wantCloseCalls {
						t.Errorf("dialed socket Close calls after transport close = %d, want %d",
							idleConn.closes.Load(), tt.wantCloseCalls)
					}
				})
			}
		})
	}
}

type readSignalingConn struct {
	net.Conn
	readStarted chan struct{}
	readOnce    sync.Once
}

func (conn *readSignalingConn) Read(p []byte) (int, error) {
	conn.readOnce.Do(func() { close(conn.readStarted) })
	return errors.Wrap2(conn.Conn.Read(p))
}

type afterFuncStopContext struct {
	done       chan struct{}
	registered chan struct{}
	stopped    chan struct{}
}

func newAfterFuncStopContext() *afterFuncStopContext {
	return &afterFuncStopContext{
		done:       make(chan struct{}),
		registered: make(chan struct{}, 1),
		stopped:    make(chan struct{}, 1),
	}
}

func (*afterFuncStopContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (ctx *afterFuncStopContext) Done() <-chan struct{}   { return ctx.done }
func (*afterFuncStopContext) Err() error                  { return nil }
func (*afterFuncStopContext) Value(any) any               { return nil }
func (ctx *afterFuncStopContext) AfterFunc(func()) func() bool {
	ctx.registered <- struct{}{}
	return func() bool {
		ctx.stopped <- struct{}{}
		return true
	}
}

func TestTranspBase_ServeConn_StopsCancellationRegistration(t *testing.T) {
	t.Parallel()

	tp, err := NewConnOrientedTransport(sip.TCPMetadata(), TransportOptions{
		ConnReadTimeout: -1,
		ConnIdleTimeout: -1,
	})
	if err != nil {
		t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = tp.Close() })

	peer, srv := newTestTCPConnPair(t)
	ctx := newAfterFuncStopContext()
	readConn := &readSignalingConn{Conn: srv, readStarted: make(chan struct{})}
	conn, done, err := tp.serveConn(ctx, readConn, true)
	if err != nil {
		t.Fatalf("tp.serveConn() error = %v, want nil", err)
	}
	if done == nil {
		t.Fatal("tp.serveConn() done = nil, want read completion channel")
	}
	testutil.WaitClosed(t, ctx.registered, "context.AfterFunc registration")
	testutil.WaitClosed(t, readConn.readStarted, "connection reader start")

	if err := peer.Close(); err != nil {
		t.Fatalf("peer.Close() error = %v, want nil", err)
	}
	readErr := testutil.WaitResult(t, done, "connection reader result after peer close")
	if readErr == nil {
		t.Error("connection reader error = nil after peer close, want read error")
	}
	testutil.WaitClosed(t, ctx.stopped, "context.AfterFunc stop")
	select {
	case _, ok := <-done:
		if ok {
			t.Fatal("connection completion channel remains open after cleanup")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for connection reader cleanup")
	}
	if tp.isClosing() {
		t.Fatal("transport is closing, want live transport")
	}
	if ctx.Err() != nil {
		t.Fatalf("serving context error = %v, want nil", ctx.Err())
	}
	if conn.isClosed() {
		t.Fatal("borrowed connection closed after read ended, want caller ownership")
	}
}

func TestTranspBase_ServeConn_CancelClosesOnlyOwnedConns(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		borrowed bool
	}{
		{"owned", false},
		{"borrowed", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tp, err := NewConnOrientedTransport(sip.TCPMetadata(), TransportOptions{
				ConnReadTimeout: 75 * time.Millisecond,
				ConnIdleTimeout: -1,
			})
			if err != nil {
				t.Fatalf("transport.NewConnOrientedTransport() error = %v, want nil", err)
			}
			t.Cleanup(func() { _ = tp.Close() })

			peer, srv := newTestTCPConnPair(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			readConn := &readSignalingConn{Conn: srv, readStarted: make(chan struct{})}
			conn, done, err := tp.serveConn(ctx, readConn, tt.borrowed)
			if err != nil {
				t.Fatalf("tp.serveConn() error = %v, want nil", err)
			}
			testutil.WaitClosed(t, readConn.readStarted, "connection reader start")

			cancel()
			readErr := testutil.WaitResult(t, done, "connection reader result after context cancellation")
			if tt.borrowed && !errors.Is(readErr, context.Canceled) {
				t.Errorf("borrowed connection read error = %v, want %v", readErr, context.Canceled)
			}
			select {
			case _, ok := <-done:
				if ok {
					t.Fatal("connection completion channel remains open after reader exit")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for connection reader cleanup")
			}
			if got := conn.isClosed(); got == tt.borrowed {
				t.Errorf("connection closed = %t, want %t", got, !tt.borrowed)
			}
			if _, found := tp.findConn(conn.raddr, conn.laddr, ""); found {
				t.Error("connection remains tracked after reader exit")
			}

			if err := tp.Close(); err != nil {
				t.Fatalf("tp.Close() error = %v, want nil", err)
			}
			if tt.borrowed && conn.isClosed() {
				t.Error("transport closed caller-owned connection after it was untracked")
			}
			_ = peer.Close()
		})
	}
}

func newTestTCPConnPair(tb testing.TB) (peer, srv net.Conn) {
	tb.Helper()

	lis, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		tb.Fatalf("net.ListenTCP() error = %v, want nil", errors.Wrap(err))
	}
	tb.Cleanup(func() { _ = lis.Close() })

	if err := lis.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		tb.Fatalf("lis.SetDeadline() error = %v, want nil", errors.Wrap(err))
	}
	peer, err = net.DialTimeout("tcp", lis.Addr().String(), 5*time.Second)
	if err != nil {
		tb.Fatalf("net.DialTimeout() error = %v, want nil", errors.Wrap(err))
	}
	tb.Cleanup(func() { _ = peer.Close() })

	srv, err = lis.AcceptTCP()
	if err != nil {
		tb.Fatalf("lis.AcceptTCP() error = %v, want nil", errors.Wrap(err))
	}
	tb.Cleanup(func() { _ = srv.Close() })

	return peer, srv
}
