package sip_test

import (
	"context"
	"iter"
	"net"
	"net/netip"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/internal/testutil/dnstest"
	"github.com/ghettovoice/gosip/internal/testutil/siptest"
	"github.com/ghettovoice/gosip/pkg/dns"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

type elementLookupFunc func(context.Context, *sip.URI, ...sip.LookupMessageAddrsOptions) iter.Seq[sip.ResolvedAddr]

func (f elementLookupFunc) LookupRequestAddrs(ctx context.Context, uri *sip.URI, opts ...sip.LookupMessageAddrsOptions) iter.Seq[sip.ResolvedAddr] {
	return f(ctx, uri, opts...)
}

func TestElement_LookupRequestAddrs(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"unconsumed", "break", "closed", "cancelled", "blocked", "cancel between yields", "close between yields"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				calls, yields := 0, 0
				entered, resume := make(chan struct{}), make(chan struct{})
				elm := &sip.Element{}
				defer elm.Close()
				elm.ServerLocator = elementLookupFunc(func(ctx context.Context, _ *sip.URI, _ ...sip.LookupMessageAddrsOptions) iter.Seq[sip.ResolvedAddr] {
					calls++
					owner, ok := sip.ElementFromContext(ctx)
					if !ok || owner != elm {
						t.Error("locator context does not carry element")
					}
					return func(yield func(sip.ResolvedAddr) bool) {
						defer close(entered)
						if name == "blocked" {
							<-resume
						}
						for range 2 {
							if !yield(sip.ResolvedAddr{Transport: "UDP", Addr: netip.MustParseAddrPort("192.0.2.1:5060")}) {
								return
							}
						}
					}
				})
				seq := elm.LookupRequestAddrs(ctx, &sip.URI{})
				if calls != 0 {
					t.Fatal("locator invoked before iteration")
				}
				switch name {
				case "unconsumed":
					return
				case "closed":
					if err := elm.Close(); err != nil {
						t.Fatal(err)
					}
				case "cancelled":
					cancel()
				}
				consume := func() {
					for range seq {
						yields++
						switch name {
						case "break":
							return
						case "cancel between yields":
							cancel()
						case "close between yields":
							if err := elm.Close(); err != nil {
								t.Error(err)
							}
						}
					}
				}
				if name == "blocked" {
					done := make(chan struct{})
					go func() { consume(); close(done) }()
					synctest.Wait()
					if err := elm.Close(); err != nil {
						t.Fatal(err)
					}
					close(resume)
					<-done
				} else {
					consume()
				}
				wantCalls, wantYields := 1, 1
				if name == "closed" || name == "cancelled" {
					wantCalls, wantYields = 0, 0
				}
				if name == "blocked" {
					wantYields = 0
				}
				if calls != wantCalls || yields != wantYields {
					t.Fatalf("calls/yields = %d/%d, want %d/%d", calls, yields, wantCalls, wantYields)
				}
				if calls != 0 {
					<-entered
				}
			})
		})
	}
}

func TestRemoteElementLocator_LookupRequestAddrs(t *testing.T) {
	t.Parallel()

	udpMeta := sip.UDPMetadata()
	tcpMeta := sip.TCPMetadata()
	tlsMeta := sip.TLSMetadata()
	multiPrvd := siptest.MetadataProvider{
		Metas: []sip.TransportMetadata{udpMeta, tcpMeta, tlsMeta},
	}

	t.Run("IP address directly in URI", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{DNSResolver: dns.DefaultResolver(), MetadataProvider: multiPrvd}

		var got []netip.AddrPort
		reqURI := &sip.URI{
			Addr: sip.MakeHostPortAddr("192.168.1.1", 5060),
		}
		for addr := range lctr.LookupRequestAddrs(t.Context(), reqURI) {
			got = append(got, addr.Addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("192.168.1.1:5060")}
		opts := []cmp.Option{
			cmpopts.SortSlices(func(a, b netip.AddrPort) bool { return a.String() < b.String() }),
			cmpopts.IgnoreUnexported(netip.AddrPort{}),
		}
		if diff := cmp.Diff(want, got, opts...); diff != "" {
			t.Errorf("lctr.LookupRequestAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("port specified with host - resolves IP", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver: dnstest.StubResolver{
				LookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
					if host == "example.com" {
						return []net.IP{net.ParseIP("192.168.1.1")}, nil
					}
					return nil, nil
				},
			},
			MetadataProvider: multiPrvd,
		}

		var got []netip.AddrPort
		reqURI := &sip.URI{
			Addr: sip.MakeHostPortAddr("example.com", 5070),
		}
		for addr := range lctr.LookupRequestAddrs(t.Context(), reqURI) {
			got = append(got, addr.Addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("192.168.1.1:5070")}
		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("lctr.LookupRequestAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("transport specified - uses SRV", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver: dnstest.StubResolver{
				LookupSRVFunc: func(_ context.Context, service, proto, host string) ([]*dns.SRV, error) {
					if service == "sip" && proto == "udp" && host == "example.com" {
						return []*dns.SRV{
							{Target: "sip1.example.com.", Port: 5060},
							{Target: "sip2.example.com.", Port: 5060},
						}, nil
					}
					return nil, nil
				},
				LookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
					switch host {
					case "sip1.example.com.", "sip2.example.com.":
						return []net.IP{net.ParseIP("10.0.0.1")}, nil
					}
					return nil, nil
				},
			},
			MetadataProvider: multiPrvd,
		}

		var got []netip.AddrPort
		reqURI := &sip.URI{
			Addr:   sip.MakeHostAddr("example.com"),
			Params: make(sip.Values).Set("transport", "UDP"),
		}
		for addr := range lctr.LookupRequestAddrs(t.Context(), reqURI) {
			got = append(got, addr.Addr)
		}

		want := []netip.AddrPort{
			netip.MustParseAddrPort("10.0.0.1:5060"),
			netip.MustParseAddrPort("10.0.0.1:5060"),
		}
		opts := []cmp.Option{
			cmpopts.SortSlices(func(a, b netip.AddrPort) bool { return a.String() < b.String() }),
			cmpopts.IgnoreUnexported(netip.AddrPort{}),
		}
		if diff := cmp.Diff(want, got, opts...); diff != "" {
			t.Errorf("lctr.LookupRequestAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("transport specified - falls back to A without SRV", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver: dnstest.StubResolver{
				LookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
					if host == "example.com" {
						return []net.IP{net.ParseIP("192.168.1.1")}, nil
					}
					return nil, nil
				},
			},
			MetadataProvider: multiPrvd,
		}

		var got []sip.ResolvedAddr
		reqURI := &sip.URI{
			Addr:   sip.MakeHostAddr("example.com"),
			Params: make(sip.Values).Set("transport", "UDP"),
		}
		for addr := range lctr.LookupRequestAddrs(t.Context(), reqURI) {
			got = append(got, addr)
		}

		if len(got) != 1 {
			t.Fatalf("lctr.LookupRequestAddrs() produced %d addresses, want 1", len(got))
		}
		gotAddr := netip.AddrPortFrom(got[0].Addr.Addr().Unmap(), got[0].Addr.Port())
		if gotAddr != netip.MustParseAddrPort("192.168.1.1:5060") {
			t.Errorf("lctr.LookupRequestAddrs() address = %v, want 192.168.1.1:5060", got[0].Addr)
		}
		if got[0].FromDNS {
			t.Errorf("LookupRequestAddrs() FromDNS = true, want false for A/AAAA fallback")
		}
	})

	t.Run("NAPTR lookup", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver: dnstest.StubResolver{
				LookupNAPTRFunc: func(_ context.Context, host string) ([]*dns.NAPTR, error) {
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
				LookupSRVFunc: func(_ context.Context, service, proto, host string) ([]*dns.SRV, error) {
					switch {
					case service == "sip" && proto == "udp" && host == "_sip._udp.example.com.":
						return []*dns.SRV{
							{Target: "sip.example.com.", Port: 5060},
						}, nil
					case service == "sip" && proto == "udp" && host == "example.com":
						return []*dns.SRV{
							{Target: "fallback.example.com.", Port: 5060},
						}, nil
					}
					return nil, nil
				},
				LookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
					switch host {
					case "sip.example.com.":
						return []net.IP{net.ParseIP("10.0.0.1")}, nil
					case "fallback.example.com.", "example.com":
						return []net.IP{net.ParseIP("192.168.1.1")}, nil
					}
					return nil, nil
				},
			},
			MetadataProvider: multiPrvd,
		}

		var got []netip.AddrPort
		reqURI := &sip.URI{
			Addr: sip.MakeHostAddr("example.com"),
		}
		for addr := range lctr.LookupRequestAddrs(t.Context(), reqURI) {
			got = append(got, addr.Addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("10.0.0.1:5060")}
		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("lctr.LookupRequestAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("fallback to default transport", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver: dnstest.StubResolver{
				LookupSRVFunc: func(_ context.Context, _, _, _ string) ([]*dns.SRV, error) {
					return nil, nil
				},
				LookupNAPTRFunc: func(_ context.Context, _ string) ([]*dns.NAPTR, error) {
					return nil, nil
				},
				LookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
					if host == "example.com" {
						return []net.IP{net.ParseIP("192.168.1.1")}, nil
					}
					return nil, nil
				},
			},
			MetadataProvider: multiPrvd,
		}

		var got []netip.AddrPort
		reqURI := &sip.URI{
			Addr: sip.MakeHostAddr("example.com"),
		}
		for addr := range lctr.LookupRequestAddrs(t.Context(), reqURI) {
			got = append(got, addr.Addr)
		}

		// Falls back to UDP (unsecured) with default port 5060
		want := []netip.AddrPort{netip.MustParseAddrPort("192.168.1.1:5060")}
		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("lctr.LookupRequestAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("SRV lookup stops before A fallback", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver: dnstest.StubResolver{
				LookupSRVFunc: func(_ context.Context, service, proto, host string) ([]*dns.SRV, error) {
					if service == "sip" && proto == "udp" && host == "example.com" {
						return []*dns.SRV{
							{Target: "sip.example.com.", Port: 5060},
						}, nil
					}
					return nil, nil
				},
				LookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
					switch host {
					case "sip.example.com.":
						return []net.IP{net.ParseIP("10.0.0.1")}, nil
					case "example.com":
						return []net.IP{net.ParseIP("192.168.1.1")}, nil
					}
					return nil, nil
				},
			},
			MetadataProvider: multiPrvd,
		}

		var got []sip.ResolvedAddr
		reqURI := &sip.URI{Addr: sip.MakeHostAddr("example.com")}
		for addr := range lctr.LookupRequestAddrs(t.Context(), reqURI) {
			got = append(got, addr)
		}

		if len(got) != 1 {
			t.Fatalf("lctr.LookupRequestAddrs() produced %d addresses, want 1", len(got))
		}
		gotAddr := netip.AddrPortFrom(got[0].Addr.Addr().Unmap(), got[0].Addr.Port())
		if gotAddr != netip.MustParseAddrPort("10.0.0.1:5060") {
			t.Errorf("lctr.LookupRequestAddrs() address = %v, want 10.0.0.1:5060", got[0].Addr)
		}
		if !got[0].FromDNS {
			t.Errorf("lctr.LookupRequestAddrs() FromDNS = false, want true for SRV result")
		}
	})

	t.Run("nil URI returns empty", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver:      dns.DefaultResolver(),
			MetadataProvider: multiPrvd,
		}

		var got []netip.AddrPort
		for addr := range lctr.LookupRequestAddrs(t.Context(), nil) {
			got = append(got, addr.Addr)
		}
		if len(got) != 0 {
			t.Errorf("lctr.LookupRequestAddrs() = %v, want empty", got)
		}
	})
}

func TestRemoteElementLocator_LookupResponseAddrs(t *testing.T) {
	t.Parallel()

	udpMeta := sip.UDPMetadata()
	multiPrvd := siptest.MetadataProvider{Metas: []sip.TransportMetadata{udpMeta}}

	t.Run("maddr parameter", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver: dnstest.StubResolver{
				LookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
					if host == "maddr.example.com" {
						return []net.IP{net.ParseIP("10.0.0.1")}, nil
					}
					return nil, nil
				},
			},
			MetadataProvider: multiPrvd,
		}

		var got []netip.AddrPort
		via := header.ViaHop{
			Proto:     sip.ProtoVer20(),
			Transport: sip.UDPMetadata().Proto,
			Addr:      sip.MakeHostPortAddr("sentby.example.com", 5060),
			Params:    make(sip.Values).Set("maddr", "maddr.example.com"),
		}
		for addr := range lctr.LookupResponseAddrs(t.Context(), via) {
			got = append(got, addr.Addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("10.0.0.1:5060")}
		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("lctr.LookupResponseAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("received parameter", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver:      dns.DefaultResolver(),
			MetadataProvider: multiPrvd,
		}

		var got []netip.AddrPort
		via := header.ViaHop{
			Proto:     sip.ProtoVer20(),
			Transport: sip.UDPMetadata().Proto,
			Addr:      sip.MakeHostPortAddr("sentby.example.com", 5060),
			Params:    make(sip.Values).Set("received", "203.0.113.1"),
		}
		for addr := range lctr.LookupResponseAddrs(t.Context(), via) {
			got = append(got, addr.Addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("203.0.113.1:5060")}
		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("lctr.LookupResponseAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("received with rport", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver:      dns.DefaultResolver(),
			MetadataProvider: multiPrvd,
		}

		var got []netip.AddrPort
		via := header.ViaHop{
			Proto:     sip.ProtoVer20(),
			Transport: sip.UDPMetadata().Proto,
			Addr:      sip.MakeHostPortAddr("sentby.example.com", 5060),
			Params:    make(sip.Values).Set("received", "203.0.113.1").Set("rport", "12345"),
		}
		for addr := range lctr.LookupResponseAddrs(t.Context(), via) {
			got = append(got, addr.Addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("203.0.113.1:12345")}
		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("lctr.LookupResponseAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("IP in sent-by", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver:      dns.DefaultResolver(),
			MetadataProvider: multiPrvd,
		}

		var got []netip.AddrPort
		via := header.ViaHop{
			Proto:     sip.ProtoVer20(),
			Transport: sip.UDPMetadata().Proto,
			Addr:      sip.MakeHostPortAddr("192.168.1.1", 5070),
		}
		for addr := range lctr.LookupResponseAddrs(t.Context(), via) {
			got = append(got, addr.Addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("192.168.1.1:5070")}
		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("lctr.LookupResponseAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("port specified with host - resolves IP", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver: dnstest.StubResolver{
				LookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
					if host == "example.com" {
						return []net.IP{net.ParseIP("192.168.1.1")}, nil
					}
					return nil, nil
				},
			},
			MetadataProvider: multiPrvd,
		}

		var got []netip.AddrPort
		via := header.ViaHop{
			Proto:     sip.ProtoVer20(),
			Transport: sip.UDPMetadata().Proto,
			Addr:      sip.MakeHostPortAddr("example.com", 5070),
		}
		for addr := range lctr.LookupResponseAddrs(t.Context(), via) {
			got = append(got, addr.Addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("192.168.1.1:5070")}
		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("lctr.LookupResponseAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("SRV fallback", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver: dnstest.StubResolver{
				LookupSRVFunc: func(_ context.Context, service, proto, host string) ([]*dns.SRV, error) {
					if service == "sip" && proto == "udp" && host == "example.com" {
						return []*dns.SRV{
							{Target: "sip.example.com.", Port: 5060},
						}, nil
					}
					return nil, nil
				},
				LookupIPFunc: func(_ context.Context, _, host string) ([]net.IP, error) {
					if host == "sip.example.com." {
						return []net.IP{net.ParseIP("10.0.0.1")}, nil
					}
					return nil, nil
				},
			},
			MetadataProvider: multiPrvd,
		}

		var got []netip.AddrPort
		via := header.ViaHop{
			Proto:     sip.ProtoVer20(),
			Transport: sip.UDPMetadata().Proto,
			Addr:      sip.MakeHostAddr("example.com"),
		}
		for addr := range lctr.LookupResponseAddrs(t.Context(), via) {
			got = append(got, addr.Addr)
		}

		want := []netip.AddrPort{netip.MustParseAddrPort("10.0.0.1:5060")}
		opts := cmpopts.IgnoreUnexported(netip.AddrPort{})
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Errorf("lctr.LookupResponseAddrs() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("invalid Via returns empty", func(t *testing.T) {
		t.Parallel()

		lctr := &sip.RemoteElementLocator{
			DNSResolver:      dns.DefaultResolver(),
			MetadataProvider: multiPrvd,
		}

		var got []netip.AddrPort
		for addr := range lctr.LookupResponseAddrs(t.Context(), header.ViaHop{}) {
			got = append(got, addr.Addr)
		}
		if len(got) != 0 {
			t.Errorf("lctr.LookupResponseAddrs() = %v, want empty", got)
		}
	})
}
