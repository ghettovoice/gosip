package sip

import (
	"net/netip"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/types"
)

func TestTransportOptions_sentBy(t *testing.T) {
	t.Parallel()

	assertZeroPort := func(t *testing.T, addr types.Addr) {
		t.Helper()

		if port, ok := addr.Port(); !ok || port != 0 {
			t.Fatalf("TransportOptions.sentBy() port = %d (has=%v), want 0", port, ok)
		}
	}

	assertPreservedPort := func(t *testing.T, addr types.Addr, want uint16) {
		t.Helper()

		if port, ok := addr.Port(); !ok || port != want {
			t.Fatalf("TransportOptions.sentBy() port = %d (has=%v), want %d", port, ok, want)
		}
	}

	tests := []struct {
		name    string
		opts    *TransportOptions
		want    types.Addr
		wantSet bool
		assert  func(t *testing.T, addr types.Addr)
	}{
		{
			name:    "valid host with port is returned as is",
			opts:    &TransportOptions{SentBy: types.AddrFromHostPort("example.com", 5060)},
			want:    types.AddrFromHostPort("example.com", 5060),
			wantSet: true,
		},
		{
			name:    "valid ip with port is returned as is",
			opts:    &TransportOptions{SentBy: types.AddrFromIPPort(netip.MustParseAddr("192.168.1.1").AsSlice(), 5060)},
			want:    types.AddrFromIPPort(netip.MustParseAddr("192.168.1.1").AsSlice(), 5060),
			wantSet: true,
		},
		{
			name:    "host only is returned as is",
			opts:    &TransportOptions{SentBy: types.AddrFromHost("example.com")},
			want:    types.AddrFromHost("example.com"),
			wantSet: true,
		},
		{
			name:    "ip only is returned as is",
			opts:    &TransportOptions{SentBy: types.AddrFromIP(netip.MustParseAddr("192.168.1.1").AsSlice())},
			want:    types.AddrFromIP(netip.MustParseAddr("192.168.1.1").AsSlice()),
			wantSet: true,
		},
		{
			name:   "nil options fallback to zero port",
			opts:   nil,
			assert: assertZeroPort,
		},
		{
			name:   "empty address fallback to zero port",
			opts:   &TransportOptions{SentBy: types.Addr{}},
			assert: assertZeroPort,
		},
		{
			name:   "host with zero port keeps placeholder",
			opts:   &TransportOptions{SentBy: types.AddrFromHostPort("example.com", 0)},
			assert: assertZeroPort,
		},
		{
			name:   "ip with zero port keeps placeholder",
			opts:   &TransportOptions{SentBy: types.AddrFromIPPort(netip.MustParseAddr("192.168.1.1").AsSlice(), 0)},
			assert: assertZeroPort,
		},
		{
			name: "unspecified ip with port reuses provided port",
			opts: &TransportOptions{SentBy: types.AddrFromIPPort(netip.IPv4Unspecified().AsSlice(), 5060)},
			assert: func(t *testing.T, addr types.Addr) {
				t.Helper()
				assertPreservedPort(t, addr, 5060)

				if ip := addr.IP(); ip != nil && ip.IsUnspecified() {
					t.Fatalf("TransportOptions.sentBy() IP is still unspecified: %v", ip)
				}
			},
		},
		{
			name:   "unspecified ip without port keeps zero port",
			opts:   &TransportOptions{SentBy: types.AddrFromIP(netip.IPv4Unspecified().AsSlice())},
			assert: assertZeroPort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got types.Addr
			if tt.opts == nil {
				var nilOpts *TransportOptions

				got = nilOpts.sentBy()
			} else {
				got = tt.opts.sentBy()
			}

			if tt.wantSet {
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("TransportOptions.sentBy() = %+v, want %+v\ndiff (-want +got):\n%s", got, tt.want, diff)
				}
			}

			if tt.assert != nil {
				tt.assert(t, got)
			}
		})
	}
}

func TestTransportOptions_sentBy_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		opts              *TransportOptions
		shouldUseDirectly bool
	}{
		{
			name:              "valid complete address should be used directly",
			opts:              &TransportOptions{SentBy: types.AddrFromHostPort("example.com", 5060)},
			shouldUseDirectly: true,
		},
		{
			name:              "valid ip with port should be used directly",
			opts:              &TransportOptions{SentBy: types.AddrFromIPPort(netip.MustParseAddr("192.168.1.1").AsSlice(), 5060)},
			shouldUseDirectly: true,
		},
		{
			name:              "zero port should trigger finalization",
			opts:              &TransportOptions{SentBy: types.AddrFromHostPort("example.com", 0)},
			shouldUseDirectly: false,
		},
		{
			name:              "unspecified ip should trigger finalization",
			opts:              &TransportOptions{SentBy: types.AddrFromIP(netip.IPv4Unspecified().AsSlice())},
			shouldUseDirectly: false,
		},
		{
			name:              "empty address should trigger finalization",
			opts:              &TransportOptions{SentBy: types.Addr{}},
			shouldUseDirectly: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := tt.opts.sentBy()

			// Check if the result would be used directly or need finalization
			needsFinalization := !result.IsValid() ||
				(result.IP() != nil && result.IP().IsUnspecified()) ||
				(func() bool { p, ok := result.Port(); return ok && p == 0 }())

			if tt.shouldUseDirectly == needsFinalization {
				t.Errorf("TransportOptions.sentBy() validation failed: expected direct use=%v, but needs finalization=%v",
					tt.shouldUseDirectly,
					needsFinalization,
				)
			}
		})
	}
}
