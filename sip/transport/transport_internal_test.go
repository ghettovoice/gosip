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

func TestBuiltInTransport_ContextPropagation(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		meta sip.TransportMetadata
		new  func(sip.TransportMetadata, ...TransportOptions) (sip.Transport, func(context.Context, *sip.RequestEnvelope) error, func(context.Context, *sip.ResponseEnvelope) error, error)
	}{
		{
			name: "udp",
			meta: sip.UDPMetadata(),
			new: func(meta sip.TransportMetadata, opts ...TransportOptions) (
				sip.Transport,
				func(context.Context, *sip.RequestEnvelope) error,
				func(context.Context, *sip.ResponseEnvelope) error,
				error,
			) {
				tp, err := NewConnectionLessTransport(meta, opts...)
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
			name: "tcp",
			meta: sip.TCPMetadata(),
			new: func(meta sip.TransportMetadata, opts ...TransportOptions) (
				sip.Transport,
				func(context.Context, *sip.RequestEnvelope) error,
				func(context.Context, *sip.ResponseEnvelope) error,
				error,
			) {
				tp, err := NewConnectionOrientedTransport(meta, opts...)
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

			localAddr := netip.MustParseAddrPort("127.0.0.1:5070")
			remoteAddr := netip.MustParseAddrPort("127.0.0.1:5080")
			req := newContextPropagationRequest(t, tt.meta, pubAddr, localAddr, remoteAddr)
			tx, err := sip.NewInviteClientTransaction(req, tp)
			if err != nil {
				t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
			}

			parentCtx := sip.ContextWithTransaction(t.Context(), tx)
			outboundRequestCtxs := make(chan context.Context, 1)
			outboundResponseCtxs := make(chan context.Context, 1)
			inboundRequestCtxs := make(chan context.Context, 1)
			inboundResponseCtxs := make(chan context.Context, 1)

			tp.UseOutboundRequestInterceptor(sip.OutboundRequestInterceptorFunc(
				func(ctx context.Context, _ sip.RequestSender, _ *sip.RequestEnvelope, _ ...sip.SendRequestOptions) error {
					outboundRequestCtxs <- ctx
					return nil
				},
			))
			tp.UseOutboundResponseInterceptor(sip.OutboundResponseInterceptorFunc(
				func(ctx context.Context, _ sip.ResponseSender, _ *sip.ResponseEnvelope, _ ...sip.SendResponseOptions) error {
					outboundResponseCtxs <- ctx
					return nil
				},
			))
			tp.UseInboundRequestInterceptor(sip.InboundRequestInterceptorFunc(
				func(ctx context.Context, _ sip.RequestReceiver, _ *sip.RequestEnvelope) error {
					inboundRequestCtxs <- ctx
					return nil
				},
			))
			tp.UseInboundResponseInterceptor(sip.InboundResponseInterceptorFunc(
				func(ctx context.Context, _ sip.ResponseReceiver, _ *sip.ResponseEnvelope) error {
					inboundResponseCtxs <- ctx
					return nil
				},
			))

			if err := tp.SendRequest(parentCtx, req); err != nil {
				t.Fatalf("tp.SendRequest() error = %v, want nil", err)
			}
			assertTransportInterceptorContext(
				t,
				testutil.WaitResult(t, outboundRequestCtxs, "outbound request interceptor context"),
				tx,
				tp,
				true,
			)

			res, err := req.NewResponse(sip.ResponseStatusOK)
			if err != nil {
				t.Fatalf("req.NewResponse(200) error = %v, want nil", err)
			}
			if err := tp.SendResponse(parentCtx, res); err != nil {
				t.Fatalf("tp.SendResponse() error = %v, want nil", err)
			}
			assertTransportInterceptorContext(
				t,
				testutil.WaitResult(t, outboundResponseCtxs, "outbound response interceptor context"),
				tx,
				tp,
				true,
			)

			if err := recvReq(parentCtx, req); err != nil {
				t.Fatalf("recvReqSafe() error = %v, want nil", err)
			}
			assertTransportInterceptorContext(
				t,
				testutil.WaitResult(t, inboundRequestCtxs, "inbound request interceptor context"),
				tx,
				tp,
				false,
			)

			if err := recvRes(parentCtx, res); err != nil {
				t.Fatalf("recvResSafe() error = %v, want nil", err)
			}
			assertTransportInterceptorContext(
				t,
				testutil.WaitResult(t, inboundResponseCtxs, "inbound response interceptor context"),
				tx,
				tp,
				false,
			)
		})
	}
}

func newContextPropagationRequest(
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

func assertTransportInterceptorContext(
	t *testing.T,
	ctx context.Context,
	tx sip.Transaction,
	tp sip.Transport,
	wantTransaction bool,
) {
	t.Helper()

	if got, ok := sip.TransportFromContext(ctx); !ok || got != tp {
		t.Fatalf("sip.TransportFromContext(ctx) = (%v, %t), want %v", got, ok, tp)
	}
	gotTx, ok := sip.TransactionFromContext(ctx)
	if wantTransaction {
		if !ok || gotTx != tx {
			t.Fatalf("sip.TransactionFromContext(ctx) = (%v, %t), want %v", gotTx, ok, tx)
		}
	} else if ok || gotTx != nil {
		t.Fatalf("sip.TransactionFromContext(inbound ctx) = (%v, %t), want (nil, false)", gotTx, ok)
	}
}

type contextElementMiddleware struct {
	elm      *sip.Element
	observed chan contextElementObservation
}

type contextElementObservation struct {
	ctx context.Context
	tx  sip.ServerTransaction
}

func (mw *contextElementMiddleware) InterceptInboundRequest(
	ctx context.Context,
	_ sip.RequestReceiver,
	req *sip.RequestEnvelope,
) error {
	tp, ok := sip.TransportFromContext(ctx)
	if !ok {
		return errors.ErrorWrap("inbound transport missing from context")
	}
	tx, err := mw.elm.NewServerTransaction(ctx, req, tp)
	if err != nil {
		return errors.Wrap(err)
	}
	return errors.Wrap(tx.Start(ctx))
}

func (mw *contextElementMiddleware) HandleServerTransaction(ctx context.Context, tx sip.ServerTransaction) {
	mw.observed <- contextElementObservation{ctx: ctx, tx: tx}
}

func TestElementMiddleware_TransactionContextFromTransport(t *testing.T) {
	t.Parallel()

	meta := sip.UDPMetadata()
	pubAddr := sip.MakeHostPortAddr("127.0.0.1", meta.DefaultPort)
	tp, err := NewConnectionLessTransport(meta, TransportOptions{PublicAddr: pubAddr})
	if err != nil {
		t.Fatalf("NewConnectionLessTransport() error = %v, want nil", err)
	}

	elm := &sip.Element{}
	if err := elm.TrackTransport(tp); err != nil {
		t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = elm.Close() })

	mw := &contextElementMiddleware{
		elm:      elm,
		observed: make(chan contextElementObservation, 1),
	}
	elm.UseMiddleware(mw)

	req := newContextPropagationRequest(
		t,
		meta,
		pubAddr,
		netip.MustParseAddrPort("127.0.0.1:5070"),
		netip.MustParseAddrPort("127.0.0.1:5080"),
	)
	if err := tp.recvReqSafe(t.Context(), req, nil); err != nil {
		t.Fatalf("tp.recvReqSafe() error = %v, want nil", err)
	}

	observed := testutil.WaitResult(t, mw.observed, "Element middleware transaction context")
	if got, ok := sip.ServerTransactionFromContext(observed.ctx); !ok || got != observed.tx {
		t.Fatalf("sip.ServerTransactionFromContext(middleware ctx) = (%v, %t), want %v", got, ok, observed.tx)
	}
	if got, ok := sip.TransportFromContext(observed.ctx); !ok || got != tp {
		t.Fatalf("sip.TransportFromContext(middleware ctx) = (%v, %t), want %v", got, ok, tp)
	}
	if got, ok := elm.LoadServerTransaction(observed.tx.Key()); !ok || got != observed.tx {
		t.Fatalf("elm.LoadServerTransaction() = (%v, %t), want middleware transaction %v", got, ok, observed.tx)
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
			name: "valid host with port is returned as is",
			opts: TransportOptions{PublicAddr: sip.MakeHostPortAddr("example.com", 5060)},
			want: sip.MakeHostPortAddr("example.com", 5060),
		},
		{
			name: "valid ip with port is returned as is",
			opts: TransportOptions{PublicAddr: sip.MakeIPPortAddr(netip.MustParseAddr("192.168.1.1").AsSlice(), 5060)},
			want: sip.MakeIPPortAddr(netip.MustParseAddr("192.168.1.1").AsSlice(), 5060),
		},
		{
			name: "host only is returned as is",
			opts: TransportOptions{PublicAddr: sip.MakeHostAddr("example.com")},
			want: sip.MakeHostAddr("example.com"),
		},
		{
			name: "ip only is returned as is",
			opts: TransportOptions{PublicAddr: sip.MakeIPAddr(netip.MustParseAddr("192.168.1.1").AsSlice())},
			want: sip.MakeIPAddr(netip.MustParseAddr("192.168.1.1").AsSlice()),
		},
		{
			name: "host with zero port keeps placeholder",
			opts: TransportOptions{PublicAddr: sip.MakeHostPortAddr("example.com", 0)},
			want: sip.MakeHostPortAddr("example.com", 0),
		},
		{
			name: "ip with zero port keeps placeholder",
			opts: TransportOptions{PublicAddr: sip.MakeIPPortAddr(netip.MustParseAddr("192.168.1.1").AsSlice(), 0)},
			want: sip.MakeIPPortAddr(netip.MustParseAddr("192.168.1.1").AsSlice(), 0),
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
			name: "zero address returns host IP",
			opts: TransportOptions{PublicAddr: sip.Addr{}},
			want: sip.MakeIPAddr(hostIPv4),
		},
		{
			name: "unspecified ip with port returns host IP with that port",
			opts: TransportOptions{PublicAddr: sip.MakeIPPortAddr(netip.IPv4Unspecified().AsSlice(), 0)},
			want: sip.MakeIPPortAddr(hostIPv4, 0),
		},
		{
			name: "unspecified ip without port returns host IP",
			opts: TransportOptions{PublicAddr: sip.MakeHostAddr("0.0.0.0")},
			want: sip.MakeIPAddr(hostIPv4),
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

func TestTransportOptions_pubAddr_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		opts              TransportOptions
		shouldUseDirectly bool
	}{
		{
			name:              "valid complete address should be used directly",
			opts:              TransportOptions{PublicAddr: sip.MakeHostPortAddr("example.com", 5060)},
			shouldUseDirectly: true,
		},
		{
			name:              "valid ip with port should be used directly",
			opts:              TransportOptions{PublicAddr: sip.MakeIPPortAddr(netip.MustParseAddr("192.168.1.1").AsSlice(), 5060)},
			shouldUseDirectly: true,
		},
		{
			name:              "ip without port should be used directly",
			opts:              TransportOptions{PublicAddr: sip.MakeIPAddr(netip.MustParseAddr("123.123.123.123").AsSlice())},
			shouldUseDirectly: true,
		},
		{
			name:              "zero port should trigger finalization",
			opts:              TransportOptions{PublicAddr: sip.MakeHostPortAddr("example.com", 0)},
			shouldUseDirectly: false,
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
					"TransportOptions.pubAddr() validation failed: expected direct use=%v, but needs finalization=%v",
					tt.shouldUseDirectly, needsFinalization,
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
	oldListener := &trackedListener{
		transpListener: &testTrackedListener{addr: addr},
		borrowed:       true,
	}
	newListener := &trackedListener{
		transpListener: &testTrackedListener{addr: addr},
		borrowed:       true,
	}

	var tb transpBase[net.PacketConn]
	tb.lisMap.Store(addr, newListener)

	tb.untrackListener(context.Background(), oldListener)

	got, ok := tb.lisMap.Load(addr)
	if !ok {
		t.Fatal("tb.lisMap.Load() found = false, want true")
	}
	if got != newListener {
		t.Fatalf("tb.lisMap.Load() = %p, want %p", got, newListener)
	}
}

func TestTranspBase_UntrackConn_PreservesReplacement(t *testing.T) {
	t.Parallel()

	l := netip.MustParseAddrPort("127.0.0.1:5060")
	r := netip.MustParseAddrPort("127.0.0.2:5060")
	old := &trackedConn{Connection: &Connection{}, borrowed: true}
	old.laddr, old.raddr = l, r
	replacement := &trackedConn{Connection: &Connection{}, borrowed: true}
	replacement.laddr, replacement.raddr = l, r

	var tb transpBase[net.Listener]
	tb.log = slog.Default()

	bucket := &connBucket{}
	bucket.Store(l, replacement)
	tb.connMap.Store(r, bucket)

	tb.untrackConn(context.Background(), old)

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
		conn := &trackedConn{Connection: &Connection{}, borrowed: true}
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
			name: "removes tracked connection and empty remote bucket",
			setup: func() (*transpBase[net.Listener], *connBucket) {
				var tb transpBase[net.Listener]
				bucket := &connBucket{}
				bucket.Store(l, newConn(l, r))
				tb.connMap.Store(r, bucket)
				return &tb, bucket
			},
			conn:          newConn(l, r),
			wantSibling:   false,
			wantBucketReg: false,
		},
		{
			name: "removes tracked connection and keeps siblings",
			setup: func() (*transpBase[net.Listener], *connBucket) {
				var tb transpBase[net.Listener]
				bucket := &connBucket{}
				bucket.Store(l, newConn(l, r))
				bucket.Store(l2, newConn(l2, r))
				tb.connMap.Store(r, bucket)
				return &tb, bucket
			},
			conn:          newConn(l, r),
			wantSibling:   true,
			wantBucketReg: true,
		},
		{
			name: "missing connection leaves siblings untouched",
			setup: func() (*transpBase[net.Listener], *connBucket) {
				var tb transpBase[net.Listener]
				bucket := &connBucket{}
				bucket.Store(l2, newConn(l2, r))
				tb.connMap.Store(r, bucket)
				return &tb, bucket
			},
			conn:          newConn(l, r),
			wantSibling:   true,
			wantBucketReg: true,
		},
		{
			name: "missing remote bucket leaves map untouched",
			setup: func() (*transpBase[net.Listener], *connBucket) {
				var tb transpBase[net.Listener]
				return &tb, &connBucket{}
			},
			conn:          newConn(l, r),
			wantSibling:   false,
			wantBucketReg: false,
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

			tb.untrackConn(context.Background(), target)

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
		{
			name: "UDP",
		},
		{
			name: "TCP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			switch tt.name {
			case "UDP":
				tp, err := NewConnectionLessTransport(sip.UDPMetadata())
				if err != nil {
					t.Fatalf("NewConnectionLessTransport() error = %v, want nil", err)
				}
				tt.listen = tp.Listen
				tt.load = tp.lisMap.Load
				tt.close = tp.Close
			case "TCP":
				tp, err := NewConnectionOrientedTransport(sip.TCPMetadata())
				if err != nil {
					t.Fatalf("NewConnectionOrientedTransport() error = %v, want nil", err)
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
				t.Fatalf("Listen() error = %v, want nil", err)
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
				t.Fatalf("rebind Listen() error = %v, want nil", err)
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

// A stale closed record must not resolve an exact local-address lookup; it
// is dropped and a live sibling still works as fallback.
func TestConnectionLess_AcquireConnection_SkipsClosedListener(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	tp, err := NewConnectionLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("NewConnectionLessTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = tp.Close() })

	lis, err := tp.Listen(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v, want nil", err)
	}
	stale := lis.(*trackedListener) //nolint:forcetypeassert
	laddr := lis.LocalAddr()

	// Close the underlying listener behind the wrapper's back, leaving a
	// stale closed record in the registry like Serve's cancellation path
	// could.
	if err := stale.transpListener.Close(); err != nil {
		t.Fatalf("underlying lis.Close() error = %v, want nil", err)
	}
	if _, ok := tp.lisMap.Load(laddr); !ok {
		t.Fatal("stale record unexpectedly removed, test premise broken")
	}

	raddr := netip.MustParseAddrPort("127.0.0.1:50999")
	if conn, err := tp.AcquireConnection(
		ctx,
		raddr,
		AcquireConnectionOptions{LocalAddr: laddr},
	); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("AcquireConnection() = (%v, %v), want %v", conn, err, ErrConnectionNotFound)
	}
	if _, ok := tp.lisMap.Load(laddr); ok {
		t.Error("stale closed record still tracked after lookup, want dropped")
	}

	// A live sibling on the same address family is picked up by the generic
	// fallback instead.
	lis2, err := tp.Listen(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sibling Listen() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = lis2.Close() })
	conn, err := tp.AcquireConnection(
		ctx,
		raddr,
		AcquireConnectionOptions{LocalAddr: laddr},
	)
	if err != nil {
		t.Fatalf("AcquireConnection() with live sibling error = %v, want nil", err)
	}
	if got := conn.LocalAddr(); got != lis2.LocalAddr() {
		t.Errorf("acquired conn LocalAddr = %v, want sibling %v", got, lis2.LocalAddr())
	}
}

type (
	transportOperationContextKey struct{}
	transportServeContextKey     struct{}
)

type dialContextRecorder struct {
	dialer          net.Dialer
	contexts        chan context.Context
	calls           atomic.Int32
	successfulCalls atomic.Int32
}

func newDialContextRecorder() *dialContextRecorder {
	return &dialContextRecorder{contexts: make(chan context.Context, 2)}
}

func (d *dialContextRecorder) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	d.calls.Add(1)
	d.contexts <- ctx
	conn, err := d.dialer.DialContext(ctx, network, address)
	if err == nil {
		d.successfulCalls.Add(1)
	}
	return errors.Wrap2(conn, err)
}

type fixedConnectionDialer struct {
	conn            net.Conn
	err             error
	calls           atomic.Int32
	successfulCalls atomic.Int32
}

func (d *fixedConnectionDialer) Dial(context.Context, string, string) (net.Conn, error) {
	d.calls.Add(1)
	if d.err != nil {
		return nil, errors.Wrap(d.err)
	}
	d.successfulCalls.Add(1)
	return d.conn, nil
}

type contextDialableTransport interface {
	sip.Transport
	AcquireConnection(
		ctx context.Context,
		raddr netip.AddrPort,
		opts ...AcquireConnectionOptions,
	) (sip.TransportConnection, error)
}

func newContextDialableTransport(
	meta sip.TransportMetadata,
	dialer ConnectionDialer,
) (contextDialableTransport, error) {
	opts := TransportOptions{
		ConnectionDialer:      dialer,
		ConnectionReadTimeout: -1,
		ConnectionIdleTimeout: -1,
	}
	if meta.Streamed() {
		return errors.Wrap2(NewConnectionOrientedTransport(meta, opts))
	}
	return errors.Wrap2(NewConnectionLessTransport(meta, opts))
}

func TestTransport_DialSeparatesOperationAndInboundContexts(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name               string
		meta               sip.TransportMetadata
		customServeContext bool
	}{
		{name: "tcp default", meta: sip.TCPMetadata()},
		{name: "tcp custom", meta: sip.TCPMetadata(), customServeContext: true},
		{name: "udp default", meta: sip.UDPMetadata()},
		{name: "udp custom", meta: sip.UDPMetadata(), customServeContext: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			meta := tt.meta

			var (
				tcpListener *net.TCPListener
				udpPeer     net.PacketConn
				err         error
			)
			if meta.Streamed() {
				tcpListener, err = net.ListenTCP(meta.Network, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
				if err != nil {
					t.Fatalf("net.Listen() error = %v, want nil", err)
				}
				t.Cleanup(func() { _ = tcpListener.Close() })
			} else {
				udpPeer, err = net.ListenPacket(meta.Network, "127.0.0.1:0")
				if err != nil {
					t.Fatalf("net.ListenPacket() error = %v, want nil", err)
				}
				t.Cleanup(func() { _ = udpPeer.Close() })
			}

			remoteAddr := netip.MustParseAddrPort(func() string {
				if tcpListener != nil {
					return tcpListener.Addr().String()
				}
				return udpPeer.LocalAddr().String()
			}())
			dialer := newDialContextRecorder()
			tp, err := newContextDialableTransport(meta, dialer)
			if err != nil {
				t.Fatalf("newContextDialableTransport() error = %v, want nil", err)
			}
			t.Cleanup(func() { _ = tp.Close() })

			serveCtx := context.Background()
			var serveCancel context.CancelFunc
			var serveFactoryCalls atomic.Int32
			var successfulCallsAtFactory atomic.Int32
			var serveContextFactory func() context.Context
			if tt.customServeContext {
				serveCtx, serveCancel = context.WithDeadline(
					context.WithValue(context.Background(), transportServeContextKey{}, "serve"),
					time.Now().Add(time.Minute),
				)
				t.Cleanup(serveCancel)
				serveContextFactory = func() context.Context {
					serveFactoryCalls.Add(1)
					successfulCallsAtFactory.Store(dialer.successfulCalls.Load())
					return serveCtx
				}
			}
			serveContextState := servingContextState{
				value: serveCtx.Value(transportServeContextKey{}),
				done:  serveCtx.Done(),
			}
			serveContextState.deadline, serveContextState.hasDeadline = serveCtx.Deadline()

			inboundCtxs := make(chan context.Context, 2)
			tp.UseInboundRequestInterceptor(sip.InboundRequestInterceptorFunc(
				func(ctx context.Context, _ sip.RequestReceiver, _ *sip.RequestEnvelope) error {
					inboundCtxs <- ctx
					return nil
				},
			))

			req := newContextPropagationRequest(
				t,
				meta,
				sip.MakeHostPortAddr("127.0.0.1", remoteAddr.Port()),
				netip.MustParseAddrPort("127.0.0.1:5070"),
				remoteAddr,
			)
			req.WithMessage(func(msg *sip.Request) {
				msg.Headers.Set(header.ContentLength(0))
			})
			tx, err := sip.NewInviteClientTransaction(req, tp)
			if err != nil {
				t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
			}

			unrelatedElm := &sip.Element{}
			t.Cleanup(func() { _ = unrelatedElm.Close() })
			baseCtx, deadlineCancel := context.WithDeadline(
				context.WithValue(t.Context(), transportOperationContextKey{}, "dial"),
				time.Now().Add(10*time.Second),
			)
			t.Cleanup(deadlineCancel)
			operationCtx := sip.ContextWithTransaction(sip.ContextWithElement(baseCtx, unrelatedElm), tx)
			dialCtx, operationCancel := context.WithCancel(operationCtx)

			conn, err := tp.AcquireConnection(
				dialCtx,
				remoteAddr,
				AcquireConnectionOptions{
					Dial:         true,
					ServeContext: serveContextFactory,
				},
			)
			if err != nil {
				t.Fatalf("tp.AcquireConnection() error = %v, want nil", err)
			}
			actualDialCtx := testutil.WaitResult(t, dialer.contexts, "transport dial context")
			operationDeadline, _ := dialCtx.Deadline()
			assertOperationContext(t, actualDialCtx, tx, tp, unrelatedElm, operationDeadline)
			wantFactoryCalls := int32(0)
			if tt.customServeContext {
				wantFactoryCalls = 1
				if got := successfulCallsAtFactory.Load(); got != 1 {
					t.Errorf("successful dial calls when ServeContext ran = %d, want 1", got)
				}
			}
			if got := serveFactoryCalls.Load(); got != wantFactoryCalls {
				t.Errorf("ServeContext factory calls after dial = %d, want %d", got, wantFactoryCalls)
			}

			var writeInbound func([]byte) error
			if meta.Streamed() {
				if err := tcpListener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					t.Fatalf("tcpListener.SetDeadline() error = %v, want nil", errors.Wrap(err))
				}
				peer, acceptErr := tcpListener.AcceptTCP()
				if acceptErr != nil {
					t.Fatalf("tcpListener.AcceptTCP() error = %v, want nil", errors.Wrap(acceptErr))
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

			operationCancel()
			if !errors.Is(actualDialCtx.Err(), context.Canceled) {
				t.Fatalf("dial context error = %v, want %v after cancel", actualDialCtx.Err(), context.Canceled)
			}

			for range 2 {
				if err := writeInbound([]byte(req.Message().Render())); err != nil {
					t.Fatalf("writeInbound() error = %v, want nil", err)
				}
				assertFreshInboundContext(
					testutil.WaitResult(t, inboundCtxs, "inbound request interceptor context"),
					t,
					tp,
					serveContextState,
				)
			}

			var reuseFactoryCalls atomic.Int32
			reused, err := tp.AcquireConnection(
				context.Background(),
				remoteAddr,
				AcquireConnectionOptions{
					Dial: true,
					ServeContext: func() context.Context {
						reuseFactoryCalls.Add(1)
						return nil
					},
				},
			)
			if err != nil {
				t.Fatalf("tp.AcquireConnection(reuse) error = %v, want nil", err)
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
			assertFreshInboundContext(
				testutil.WaitResult(t, inboundCtxs, "reused connection inbound context"),
				t,
				tp,
				serveContextState,
			)

			if tt.customServeContext {
				servingConn, ok := conn.(*Connection)
				if !ok {
					t.Fatalf("dialed connection type = %T, want *Connection", conn)
				}
				serveCancel()
				testutil.WaitClosed(t, servingConn.closed, "custom serve context cancellation")
			}
			if err := tp.Close(); err != nil {
				t.Fatalf("tp.Close() error = %v, want nil", err)
			}
			if err := conn.WriteMessage(context.Background(), req.Message(), remoteAddr); err == nil {
				t.Fatal("conn.WriteMessage() after transport close error = nil, want error")
			}
		})
	}
}

func assertOperationContext(
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
	if got := ctx.Value(transportOperationContextKey{}); got != "dial" {
		t.Errorf("dial context value = %v, want dial", got)
	}
	if got, ok := ctx.Deadline(); !ok || !got.Equal(wantDeadline) {
		t.Errorf("dial context deadline = (%v, %t), want %v", got, ok, wantDeadline)
	}
}

type servingContextState struct {
	value       any
	deadline    time.Time
	hasDeadline bool
	done        <-chan struct{}
}

func assertFreshInboundContext(ctx context.Context, t *testing.T, tp sip.Transport, serveCtx servingContextState) {
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
	if got := ctx.Value(transportOperationContextKey{}); got != nil {
		t.Errorf("inbound context operation value = %v, want nil", got)
	}
	if got := ctx.Value(transportServeContextKey{}); got != serveCtx.value {
		t.Errorf("inbound context serving value = %v, want %v", got, serveCtx.value)
	}
	gotDeadline, gotDeadlineSet := ctx.Deadline()
	if gotDeadlineSet != serveCtx.hasDeadline ||
		serveCtx.hasDeadline && !gotDeadline.Equal(serveCtx.deadline) {
		t.Errorf("inbound context deadline = (%v, %t), want (%v, %t)",
			gotDeadline, gotDeadlineSet, serveCtx.deadline, serveCtx.hasDeadline)
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

func TestTransport_AcquireConnection_ServeContextErrors(t *testing.T) {
	t.Parallel()

	const dialFailure errors.Error = "fixture dial failure"
	remoteAddr := netip.MustParseAddrPort("127.0.0.1:5099")
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
					wantErr: ErrConnectionNotFound,
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

					var seamConn *idleSeamConn
					if tt.withConn {
						seamConn = &idleSeamConn{
							laddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5070},
							raddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5099},
						}
					}
					dialer := &fixedConnectionDialer{conn: seamConn, err: tt.dialErr}
					tp, err := newContextDialableTransport(meta, dialer)
					if err != nil {
						t.Fatalf("newContextDialableTransport() error = %v, want nil", errors.Wrap(err))
					}
					t.Cleanup(func() { _ = tp.Close() })

					var serveFactoryCalls atomic.Int32
					var successfulCallsAtFactory atomic.Int32
					conn, err := tp.AcquireConnection(
						t.Context(),
						remoteAddr,
						AcquireConnectionOptions{
							Dial: tt.dial,
							ServeContext: func() context.Context {
								serveFactoryCalls.Add(1)
								successfulCallsAtFactory.Store(dialer.successfulCalls.Load())
								return nil
							},
						},
					)

					switch {
					case tt.wantErr != nil:
						if !errors.Is(err, tt.wantErr) {
							t.Fatalf("tp.AcquireConnection() error = %v, want %v", errors.Wrap(err), tt.wantErr)
						}
					case tt.wantAnyErr:
						if err == nil {
							t.Fatal("tp.AcquireConnection() error = nil, want nil serve context error")
						}
					default:
						if err != nil {
							t.Fatalf("tp.AcquireConnection() error = %v, want nil", errors.Wrap(err))
						}
					}
					if conn != nil {
						t.Fatalf("tp.AcquireConnection() connection = %v, want nil", conn)
					}
					if got := dialer.calls.Load(); got != tt.wantDialCalls {
						t.Errorf("Dial calls = %d, want %d", got, tt.wantDialCalls)
					}
					if got := serveFactoryCalls.Load(); got != tt.wantFactory {
						t.Errorf("ServeContext factory calls = %d, want %d", got, tt.wantFactory)
					}
					if tt.withConn {
						if got := successfulCallsAtFactory.Load(); got != 1 {
							t.Errorf("successful dial calls when ServeContext ran = %d, want 1", got)
						}
						if got := seamConn.closes.Load(); got != tt.wantCloseCalls {
							t.Errorf("dialed socket Close calls = %d, want %d", got, tt.wantCloseCalls)
						}
					}

					if err := tp.Close(); err != nil {
						t.Fatalf("tp.Close() error = %v, want nil", errors.Wrap(err))
					}
					if tt.withConn && seamConn.closes.Load() != tt.wantCloseCalls {
						t.Errorf("dialed socket Close calls after transport close = %d, want %d",
							seamConn.closes.Load(), tt.wantCloseCalls)
					}
				})
			}
		})
	}
}

func TestConnectionLessTransport_AcquireConnection_ServeContextListenerFallback(t *testing.T) {
	t.Parallel()

	tp, err := NewConnectionLessTransport(sip.UDPMetadata())
	if err != nil {
		t.Fatalf("NewConnectionLessTransport() error = %v, want nil", errors.Wrap(err))
	}
	t.Cleanup(func() { _ = tp.Close() })

	listener, err := tp.Listen(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tp.Listen() error = %v, want nil", errors.Wrap(err))
	}

	var factoryCalls atomic.Int32
	conn, err := tp.AcquireConnection(
		t.Context(),
		netip.MustParseAddrPort("127.0.0.1:5098"),
		AcquireConnectionOptions{
			LocalAddr: listener.LocalAddr(),
			Dial:      true,
			ServeContext: func() context.Context {
				factoryCalls.Add(1)
				return nil
			},
		},
	)
	if err != nil {
		t.Fatalf("tp.AcquireConnection() error = %v, want nil", errors.Wrap(err))
	}
	if conn.LocalAddr() != listener.LocalAddr() {
		t.Fatalf("tp.AcquireConnection().LocalAddr() = %v, want listener address %v",
			conn.LocalAddr(), listener.LocalAddr())
	}
	if got := factoryCalls.Load(); got != 0 {
		t.Errorf("ServeContext factory calls for listener fallback = %d, want 0", got)
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

	tp, err := NewConnectionOrientedTransport(sip.TCPMetadata(), TransportOptions{
		ConnectionReadTimeout: -1,
		ConnectionIdleTimeout: -1,
	})
	if err != nil {
		t.Fatalf("NewConnectionOrientedTransport() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = tp.Close() })

	peer, server := newTestTCPConnPair(t)
	ctx := newAfterFuncStopContext()
	readConn := &readSignalingConn{Conn: server, readStarted: make(chan struct{})}
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

func TestTranspBase_ServeConn_CancelClosesOnlyOwnedConnections(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		borrowed bool
	}{
		{name: "owned"},
		{name: "borrowed", borrowed: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tp, err := NewConnectionOrientedTransport(sip.TCPMetadata(), TransportOptions{
				ConnectionReadTimeout: 75 * time.Millisecond,
				ConnectionIdleTimeout: -1,
			})
			if err != nil {
				t.Fatalf("NewConnectionOrientedTransport() error = %v, want nil", err)
			}
			t.Cleanup(func() { _ = tp.Close() })

			peer, server := newTestTCPConnPair(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			readConn := &readSignalingConn{Conn: server, readStarted: make(chan struct{})}
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

func newTestTCPConnPair(tb testing.TB) (peer, server net.Conn) {
	tb.Helper()

	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		tb.Fatalf("net.ListenTCP() error = %v, want nil", errors.Wrap(err))
	}
	tb.Cleanup(func() { _ = listener.Close() })

	if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		tb.Fatalf("listener.SetDeadline() error = %v, want nil", errors.Wrap(err))
	}
	peer, err = net.DialTimeout("tcp", listener.Addr().String(), 5*time.Second)
	if err != nil {
		tb.Fatalf("net.DialTimeout() error = %v, want nil", errors.Wrap(err))
	}
	tb.Cleanup(func() { _ = peer.Close() })

	server, err = listener.AcceptTCP()
	if err != nil {
		tb.Fatalf("listener.AcceptTCP() error = %v, want nil", errors.Wrap(err))
	}
	tb.Cleanup(func() { _ = server.Close() })

	return peer, server
}
