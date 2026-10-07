package transport

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/netutil"
	"github.com/ghettovoice/gosip/sip"
)

func TestTransportOptions_pubAddr(t *testing.T) {
	t.Parallel()

	hostIP, err := netutil.HostIP()
	if err != nil {
		t.Fatalf("failed to get host IP: %v", err)
	}

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
		{
			name: "zero address returns host IP",
			opts: TransportOptions{PublicAddr: sip.Addr{}},
			want: sip.MakeIPAddr(hostIP),
		},
		{
			name: "unspecified ip with port returns host IP with that port",
			opts: TransportOptions{PublicAddr: sip.MakeIPPortAddr(netip.IPv4Unspecified().AsSlice(), 0)},
			want: sip.MakeIPPortAddr(hostIP, 0),
		},
		{
			name: "unspecified ip without port returns host IP",
			opts: TransportOptions{PublicAddr: sip.MakeHostAddr("0.0.0.0")},
			want: sip.MakeIPAddr(hostIP),
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
		{
			name:              "zero address should be used as is",
			opts:              TransportOptions{PublicAddr: sip.Addr{}},
			shouldUseDirectly: true,
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
func (*testTrackedListener) Close(context.Context) error     { return nil }
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
