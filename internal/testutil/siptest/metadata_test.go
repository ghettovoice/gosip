package siptest_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/testutil/siptest"
	"github.com/ghettovoice/gosip/sip"
)

func TestMetadataProvider_Lookups(t *testing.T) {
	t.Parallel()

	udpMeta := sip.UDPMetadata()
	tcpMeta := sip.TCPMetadata()
	provider := siptest.MetadataProvider{Metas: []sip.TransportMetadata{udpMeta, tcpMeta}}

	tests := []struct {
		name string
		got  func() (sip.TransportMetadata, bool)
		want sip.TransportMetadata
		ok   bool
	}{
		{
			name: "protocol match is case insensitive",
			got: func() (sip.TransportMetadata, bool) {
				return provider.TransportMetadataByProto(sip.TransportProto("uDp"))
			},
			want: udpMeta,
			ok:   true,
		},
		{
			name: "NAPTR service match is case insensitive",
			got:  func() (sip.TransportMetadata, bool) { return provider.TransportMetadataByNAPTRService("sip+d2t") },
			want: tcpMeta,
			ok:   true,
		},
		{
			name: "missing protocol",
			got:  func() (sip.TransportMetadata, bool) { return provider.TransportMetadataByProto("missing") },
		},
		{
			name: "missing NAPTR service",
			got:  func() (sip.TransportMetadata, bool) { return provider.TransportMetadataByNAPTRService("missing") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := tt.got()
			if ok != tt.ok {
				t.Errorf("siptest.MetadataProvider lookup found = %v, want %v", ok, tt.ok)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("siptest.MetadataProvider lookup mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMetadataProvider_AllTransportMetadata(t *testing.T) {
	t.Parallel()

	metas := []sip.TransportMetadata{sip.UDPMetadata(), sip.TCPMetadata(), sip.TLSMetadata()}
	provider := siptest.MetadataProvider{Metas: metas}

	var got []sip.TransportMetadata
	for meta := range provider.AllTransportMetadata() {
		got = append(got, meta)
	}
	if diff := cmp.Diff(metas, got); diff != "" {
		t.Errorf("siptest.MetadataProvider.AllTransportMetadata() mismatch (-want +got):\n%s", diff)
	}

	yielded := 0
	provider.AllTransportMetadata()(func(sip.TransportMetadata) bool {
		yielded++
		return false
	})
	if yielded != 1 {
		t.Errorf("siptest.MetadataProvider.AllTransportMetadata() yielded %d values after early stop, want 1", yielded)
	}
}
