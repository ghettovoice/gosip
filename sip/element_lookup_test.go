package sip_test

import (
	"context"
	"iter"
	"net"
	"net/netip"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/dns"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

// mockDNSResolver is a test double for DNSResolver.
type mockDNSResolver struct {
	lookupIPFunc    func(ctx context.Context, network, host string) ([]net.IP, error)
	lookupSRVFunc   func(ctx context.Context, service, proto, host string) ([]*dns.SRV, error)
	lookupNAPTRFunc func(ctx context.Context, host string) ([]*dns.NAPTR, error)
}

func (m *mockDNSResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	if m.lookupIPFunc != nil {
		return m.lookupIPFunc(ctx, network, host)
	}
	return nil, nil
}

func (m *mockDNSResolver) LookupSRV(ctx context.Context, service, proto, host string) ([]*dns.SRV, error) {
	if m.lookupSRVFunc != nil {
		return m.lookupSRVFunc(ctx, service, proto, host)
	}
	return nil, nil
}

func (m *mockDNSResolver) LookupNAPTR(ctx context.Context, host string) ([]*dns.NAPTR, error) {
	if m.lookupNAPTRFunc != nil {
		return m.lookupNAPTRFunc(ctx, host)
	}
	return nil, nil
}

// multiTransportProvider is a test double providing multiple transports.
type multiTransportProvider struct {
	metas []sip.TransportMetadata
}

func (p *multiTransportProvider) MetadataByProto(proto sip.TransportProto) sip.TransportMetadata {
	for _, m := range p.metas {
		if m.Proto == proto {
			return m
		}
	}

	return sip.TransportMetadata{}
}

func (p *multiTransportProvider) MetadataByNAPTRService(service string) sip.TransportMetadata {
	for _, m := range p.metas {
		if m.NAPTRService == service {
			return m
		}
	}

	return sip.TransportMetadata{}
}

func (p *multiTransportProvider) AllMetadata() iter.Seq[sip.TransportMetadata] {
	return func(yield func(sip.TransportMetadata) bool) {
		for _, m := range p.metas {
			if !yield(m) {
				return
			}
		}
	}
}

func TestRemoteElementLocator_LookupRequestAddrs(t *testing.T) {
	t.Parallel()

	udpMeta := sip.UDPTransportMetadata()
	tcpMeta := sip.TCPTransportMetadata()
	tlsMeta := sip.TLSTransportMetadata()

	multiPrvd := &multiTransportProvider{
		metas: []sip.TransportMetadata{udpMeta, tcpMeta, tlsMeta},
	}

	t.Run("IP address directly in URI", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{}

		reqURI := &uri.SIP{
			Addr: uri.AddrFromHostPort("192.168.1.1", 5060),
		}

		var got []netip.AddrPort
		for _, addr := range lctr.LookupRequestAddrs(t.Context(), reqURI, multiPrvd) {
			got = append(got, addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("192.168.1.1:5060")}

		opts := []cmp.Option{
			cmpopts.SortSlices(func(a, b netip.AddrPort) bool { return a.String() < b.String() }),
			cmpopts.IgnoreUnexported(netip.AddrPort{}),
		}
		if diff := cmp.Diff(want, got, opts...); diff != "" {
			t.Errorf("LookupRequestAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("port specified with host - resolves IP", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver: &mockDNSResolver{
				lookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
					if host == "example.com" {
						return []net.IP{net.ParseIP("192.168.1.1")}, nil
					}
					return nil, nil
				},
			},
		}

		reqURI := &uri.SIP{
			Addr: uri.AddrFromHostPort("example.com", 5070),
		}

		var got []netip.AddrPort
		for _, addr := range lctr.LookupRequestAddrs(t.Context(), reqURI, multiPrvd) {
			got = append(got, addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("192.168.1.1:5070")}

		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("LookupRequestAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("transport specified - uses SRV then A", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver: &mockDNSResolver{
				lookupSRVFunc: func(_ context.Context, service, proto, host string) ([]*dns.SRV, error) {
					if service == "sip" && proto == "udp" && host == "example.com" {
						return []*dns.SRV{
							{Target: "sip1.example.com.", Port: 5060},
							{Target: "sip2.example.com.", Port: 5060},
						}, nil
					}

					return nil, nil
				},
				lookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
					switch host {
					case "sip1.example.com.", "sip2.example.com.":
						return []net.IP{net.ParseIP("10.0.0.1")}, nil
					case "example.com":
						return []net.IP{net.ParseIP("192.168.1.1")}, nil

					}

					return nil, nil
				},
			},
		}

		reqURI := &uri.SIP{
			Addr:   uri.AddrFromHost("example.com"),
			Params: make(uri.Values).Set("transport", "UDP"),
		}

		var got []netip.AddrPort
		for _, addr := range lctr.LookupRequestAddrs(t.Context(), reqURI, multiPrvd) {
			got = append(got, addr)
		}

		want := []netip.AddrPort{
			netip.MustParseAddrPort("10.0.0.1:5060"),
			netip.MustParseAddrPort("10.0.0.1:5060"),
			netip.MustParseAddrPort("192.168.1.1:5060"),
		}

		opts := []cmp.Option{
			cmpopts.SortSlices(func(a, b netip.AddrPort) bool { return a.String() < b.String() }),
			cmpopts.IgnoreUnexported(netip.AddrPort{}),
		}
		if diff := cmp.Diff(want, got, opts...); diff != "" {
			t.Errorf("LookupRequestAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("NAPTR lookup", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver: &mockDNSResolver{
				lookupNAPTRFunc: func(_ context.Context, host string) ([]*dns.NAPTR, error) {
					if host == "example.com" {
						return []*dns.NAPTR{
							{
								Order:       10,
								Preference:  100,
								Flags:       "S",
								Service:     "SIP+D2U",
								Replacement: "_sip._udp.example.com.",
							},
						}, nil
					}

					return nil, nil
				},
				lookupSRVFunc: func(_ context.Context, service, proto, host string) ([]*dns.SRV, error) {
					if service == "sip" && proto == "udp" && host == "_sip._udp.example.com." {
						return []*dns.SRV{
							{Target: "sip.example.com.", Port: 5060},
						}, nil
					}

					return nil, nil
				},
				lookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
					if host == "sip.example.com." {
						return []net.IP{net.ParseIP("10.0.0.1")}, nil
					}
					return nil, nil
				},
			},
		}

		reqURI := &uri.SIP{
			Addr: uri.AddrFromHost("example.com"),
		}

		var got []netip.AddrPort
		for _, addr := range lctr.LookupRequestAddrs(t.Context(), reqURI, multiPrvd) {
			got = append(got, addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("10.0.0.1:5060")}

		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("LookupRequestAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("fallback to default transport", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver: &mockDNSResolver{
				lookupSRVFunc: func(_ context.Context, _, _, _ string) ([]*dns.SRV, error) {
					return nil, nil
				},
				lookupNAPTRFunc: func(_ context.Context, _ string) ([]*dns.NAPTR, error) {
					return nil, nil
				},
				lookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
					if host == "example.com" {
						return []net.IP{net.ParseIP("192.168.1.1")}, nil
					}
					return nil, nil
				},
			},
		}

		reqURI := &uri.SIP{
			Addr: uri.AddrFromHost("example.com"),
		}

		var got []netip.AddrPort
		for _, addr := range lctr.LookupRequestAddrs(t.Context(), reqURI, multiPrvd) {
			got = append(got, addr)
		}

		// Falls back to UDP (unsecured) with default port 5060
		want := []netip.AddrPort{netip.MustParseAddrPort("192.168.1.1:5060")}

		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("LookupRequestAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("nil locator returns empty", func(t *testing.T) {
		t.Parallel()

		var lctr *sip.RemoteElementLocator

		reqURI := &uri.SIP{
			Addr: uri.AddrFromHostPort("192.168.1.1", 5060),
		}

		var got []netip.AddrPort
		for _, addr := range lctr.LookupRequestAddrs(t.Context(), reqURI, multiPrvd) {
			got = append(got, addr)
		}

		if len(got) != 0 {
			t.Errorf("LookupRequestAddrs() = %v, want empty", got)
		}
	})

	t.Run("nil URI returns empty", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{}

		var got []netip.AddrPort
		for _, addr := range lctr.LookupRequestAddrs(t.Context(), nil, multiPrvd) {
			got = append(got, addr)
		}

		if len(got) != 0 {
			t.Errorf("LookupRequestAddrs() = %v, want empty", got)
		}
	})
}

func TestRemoteElementLocator_LookupResponseAddrs(t *testing.T) {
	t.Parallel()

	udpMeta := sip.UDPTransportMetadata()
	multiPrvd := &multiTransportProvider{metas: []sip.TransportMetadata{udpMeta}}

	t.Run("maddr parameter", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver: &mockDNSResolver{
				lookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
					if host == "maddr.example.com" {
						return []net.IP{net.ParseIP("10.0.0.1")}, nil
					}
					return nil, nil
				},
			},
		}

		via := header.ViaHop{
			Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
			Transport: "UDP",
			Addr:      uri.AddrFromHostPort("sentby.example.com", 5060),
			Params:    make(header.Values).Set("maddr", "maddr.example.com"),
		}

		var got []netip.AddrPort
		for _, addr := range lctr.LookupResponseAddrs(t.Context(), via, multiPrvd) {
			got = append(got, addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("10.0.0.1:5060")}

		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("LookupResponseAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("received parameter", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{}

		via := header.ViaHop{
			Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
			Transport: "UDP",
			Addr:      uri.AddrFromHostPort("sentby.example.com", 5060),
			Params:    make(header.Values).Set("received", "203.0.113.1"),
		}

		var got []netip.AddrPort
		for _, addr := range lctr.LookupResponseAddrs(t.Context(), via, multiPrvd) {
			got = append(got, addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("203.0.113.1:5060")}

		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("LookupResponseAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("received with rport", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{}

		via := header.ViaHop{
			Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
			Transport: "UDP",
			Addr:      uri.AddrFromHostPort("sentby.example.com", 5060),
			Params:    make(header.Values).Set("received", "203.0.113.1").Set("rport", "12345"),
		}

		var got []netip.AddrPort
		for _, addr := range lctr.LookupResponseAddrs(t.Context(), via, multiPrvd) {
			got = append(got, addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("203.0.113.1:12345")}

		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("LookupResponseAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("IP in sent-by", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{}

		via := header.ViaHop{
			Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
			Transport: "UDP",
			Addr:      uri.AddrFromHostPort("192.168.1.1", 5070),
		}

		var got []netip.AddrPort
		for _, addr := range lctr.LookupResponseAddrs(t.Context(), via, multiPrvd) {
			got = append(got, addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("192.168.1.1:5070")}

		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("LookupResponseAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("port specified with host - resolves IP", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver: &mockDNSResolver{
				lookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
					if host == "example.com" {
						return []net.IP{net.ParseIP("192.168.1.1")}, nil
					}
					return nil, nil
				},
			},
		}

		via := header.ViaHop{
			Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
			Transport: "UDP",
			Addr:      uri.AddrFromHostPort("example.com", 5070),
		}

		var got []netip.AddrPort
		for _, addr := range lctr.LookupResponseAddrs(t.Context(), via, multiPrvd) {
			got = append(got, addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("192.168.1.1:5070")}

		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("LookupResponseAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("SRV fallback", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver: &mockDNSResolver{
				lookupSRVFunc: func(_ context.Context, service, proto, host string) ([]*dns.SRV, error) {
					if service == "sip" && proto == "udp" && host == "example.com" {
						return []*dns.SRV{
							{Target: "sip.example.com.", Port: 5060},
						}, nil
					}

					return nil, nil
				},
				lookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
					if host == "sip.example.com." {
						return []net.IP{net.ParseIP("10.0.0.1")}, nil
					}
					return nil, nil
				},
			},
		}

		via := header.ViaHop{
			Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
			Transport: "UDP",
			Addr:      uri.AddrFromHost("example.com"),
		}

		var got []netip.AddrPort
		for _, addr := range lctr.LookupResponseAddrs(t.Context(), via, multiPrvd) {
			got = append(got, addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("10.0.0.1:5060")}

		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("LookupResponseAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("nil locator returns empty", func(t *testing.T) {
		t.Parallel()

		var lctr *sip.RemoteElementLocator

		via := header.ViaHop{
			Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
			Transport: "UDP",
			Addr:      uri.AddrFromHostPort("192.168.1.1", 5060),
		}

		var got []netip.AddrPort
		for _, addr := range lctr.LookupResponseAddrs(t.Context(), via, multiPrvd) {
			got = append(got, addr)
		}

		if len(got) != 0 {
			t.Errorf("LookupResponseAddrs() = %v, want empty", got)
		}
	})

	t.Run("invalid via returns empty", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{}

		var got []netip.AddrPort
		for _, addr := range lctr.LookupResponseAddrs(t.Context(), header.ViaHop{}, multiPrvd) {
			got = append(got, addr)
		}

		if len(got) != 0 {
			t.Errorf("LookupResponseAddrs() = %v, want empty", got)
		}
	})
}
