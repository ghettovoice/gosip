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
			opts: TransportOptions{PublicAddr: sip.AddrFromHostPort("example.com", 5060)},
			want: sip.AddrFromHostPort("example.com", 5060),
		},
		{
			name: "valid ip with port is returned as is",
			opts: TransportOptions{PublicAddr: sip.AddrFromIPPort(netip.MustParseAddr("192.168.1.1").AsSlice(), 5060)},
			want: sip.AddrFromIPPort(netip.MustParseAddr("192.168.1.1").AsSlice(), 5060),
		},
		{
			name: "host only is returned as is",
			opts: TransportOptions{PublicAddr: sip.AddrFromHost("example.com")},
			want: sip.AddrFromHost("example.com"),
		},
		{
			name: "ip only is returned as is",
			opts: TransportOptions{PublicAddr: sip.AddrFromIP(netip.MustParseAddr("192.168.1.1").AsSlice())},
			want: sip.AddrFromIP(netip.MustParseAddr("192.168.1.1").AsSlice()),
		},
		{
			name: "host with zero port keeps placeholder",
			opts: TransportOptions{PublicAddr: sip.AddrFromHostPort("example.com", 0)},
			want: sip.AddrFromHostPort("example.com", 0),
		},
		{
			name: "ip with zero port keeps placeholder",
			opts: TransportOptions{PublicAddr: sip.AddrFromIPPort(netip.MustParseAddr("192.168.1.1").AsSlice(), 0)},
			want: sip.AddrFromIPPort(netip.MustParseAddr("192.168.1.1").AsSlice(), 0),
		},
		{
			name: "zero address returns host IP",
			opts: TransportOptions{PublicAddr: sip.Addr{}},
			want: sip.AddrFromIP(hostIP),
		},
		{
			name: "unspecified ip with port returns host IP with that port",
			opts: TransportOptions{PublicAddr: sip.AddrFromIPPort(netip.IPv4Unspecified().AsSlice(), 0)},
			want: sip.AddrFromIPPort(hostIP, 0),
		},
		{
			name: "unspecified ip without port returns host IP",
			opts: TransportOptions{PublicAddr: sip.AddrFromHost("0.0.0.0")},
			want: sip.AddrFromIP(hostIP),
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
			opts:              TransportOptions{PublicAddr: sip.AddrFromHostPort("example.com", 5060)},
			shouldUseDirectly: true,
		},
		{
			name:              "valid ip with port should be used directly",
			opts:              TransportOptions{PublicAddr: sip.AddrFromIPPort(netip.MustParseAddr("192.168.1.1").AsSlice(), 5060)},
			shouldUseDirectly: true,
		},
		{
			name:              "ip without port should be used directly",
			opts:              TransportOptions{PublicAddr: sip.AddrFromIP(netip.MustParseAddr("123.123.123.123").AsSlice())},
			shouldUseDirectly: true,
		},
		{
			name:              "zero port should trigger finalization",
			opts:              TransportOptions{PublicAddr: sip.AddrFromHostPort("example.com", 0)},
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
				t.Errorf("TransportOptions.pubAddr() validation failed: expected direct use=%v, but needs finalization=%v",
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
