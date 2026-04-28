package dns_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	mdns "github.com/miekg/dns"

	gdns "github.com/ghettovoice/gosip/dns"
	"github.com/ghettovoice/gosip/internal/errors"
)

func TestResolver_LookupIP(t *testing.T) {
	t.Parallel()

	rslvr := newTestResolver(startTestDNSServer(t))

	got, err := rslvr.LookupIP(t.Context(), "ip", "host.example.test")
	if err != nil {
		t.Fatalf("resolver.LookupIP(ctx, %q, %q) error = %v, want nil", "ip", "host.example.test", err)
	}

	var (
		hasIPv4 bool
		hasIPv6 bool
	)
	for _, ip := range got {
		if ip4 := ip.To4(); ip4 != nil {
			hasIPv4 = true

			if len(ip) != net.IPv4len {
				t.Errorf("len(ipv4) = %d, want %d", len(ip), net.IPv4len)
			}

			if !ip4.Equal(net.IPv4(127, 0, 0, 1)) {
				t.Errorf("ipv4 = %v, want %v", ip4, net.IPv4(127, 0, 0, 1))
			}

			continue
		}

		if ip.Equal(net.ParseIP("2001:db8::1")) {
			hasIPv6 = true
		}
	}

	if !hasIPv4 {
		t.Fatalf("LookupIP() missing IPv4 record in %v", got)
	}

	if !hasIPv6 {
		t.Fatalf("LookupIP() missing IPv6 record in %v", got)
	}
}

func TestResolver_LookupSRV(t *testing.T) {
	t.Parallel()

	rslvr := newTestResolver(startTestDNSServer(t))

	got, err := rslvr.LookupSRV(t.Context(), "sip", "udp", "example.test")
	if err != nil {
		t.Fatalf("resolver.LookupSRV(ctx, %q, %q, %q) error = %v, want nil", "sip", "udp", "example.test", err)
	}

	want := []*gdns.SRV{
		{
			Target:   "sip.example.test.",
			Port:     5060,
			Priority: 10,
			Weight:   20,
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("resolver.LookupSRV(ctx, %q, %q, %q) mismatch (-want +got):\n%s", "sip", "udp", "example.test", diff)
	}
}

func TestResolver_LookupNAPTR(t *testing.T) {
	t.Parallel()

	rslvr := newTestResolver(startTestDNSServer(t))

	t.Run("sort records by order and preference", func(t *testing.T) {
		t.Parallel()

		got, err := rslvr.LookupNAPTR(t.Context(), "example.test")
		if err != nil {
			t.Fatalf("resolver.LookupNAPTR(ctx, %q) error = %v, want nil", "example.test", err)
		}

		want := []*gdns.NAPTR{
			{
				Order:       10,
				Preference:  30,
				Flags:       "S",
				Service:     "SIP+D2T",
				Regexp:      "",
				Replacement: "_sip._tcp.example.test.",
			},
			{
				Order:       10,
				Preference:  50,
				Flags:       "S",
				Service:     "SIP+D2U",
				Regexp:      "",
				Replacement: "_sip._udp.example.test.",
			},
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("resolver.LookupNAPTR(ctx, %q) mismatch (-want +got):\n%s", "example.test", diff)
		}
	})

	t.Run("return not found error for nxdomain", func(t *testing.T) {
		t.Parallel()

		_, err := rslvr.LookupNAPTR(t.Context(), "missing.example.test")
		if err == nil {
			t.Fatalf("resolver.LookupNAPTR(ctx, %q) error = nil, want error", "missing.example.test")
		}

		var dnsErr *net.DNSError
		if !errors.As(err, &dnsErr) {
			t.Fatalf("resolver.LookupNAPTR(ctx, %q) error type = %T, want *net.DNSError", "missing.example.test", err)
		}

		if !dnsErr.IsNotFound {
			t.Errorf("dnsErr.IsNotFound = %v, want true", dnsErr.IsNotFound)
		}

		if dnsErr.Name != "missing.example.test" {
			t.Errorf("dnsErr.Name = %q, want %q", dnsErr.Name, "missing.example.test")
		}
	})
}

func newTestResolver(nameserver string) *gdns.Resolver {
	return &gdns.Resolver{
		Resolver: net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, nameserver)
			},
		},
		NameServer: nameserver,
		Timeout:    time.Second,
	}
}

func startTestDNSServer(t *testing.T) string {
	t.Helper()

	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(%q, %q) error = %v, want nil", "udp", "127.0.0.1:0", err)
	}

	server := &mdns.Server{PacketConn: packetConn, Handler: newTestDNSHandler()}
	go func() {
		_ = server.ActivateAndServe()
	}()

	t.Cleanup(func() {
		_ = server.Shutdown()
		_ = packetConn.Close()
	})

	return packetConn.LocalAddr().String()
}

func newTestDNSHandler() mdns.Handler {
	mux := mdns.NewServeMux()
	mux.HandleFunc(".", func(w mdns.ResponseWriter, req *mdns.Msg) {
		resp := new(mdns.Msg)
		resp.SetReply(req)
		resp.Authoritative = true

		for _, q := range req.Question {
			switch {
			case q.Qtype == mdns.TypeA && q.Name == "host.example.test.":
				resp.Answer = append(resp.Answer, &mdns.A{
					Hdr: mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 300},
					A:   net.IPv4(127, 0, 0, 1),
				})
			case q.Qtype == mdns.TypeAAAA && q.Name == "host.example.test.":
				resp.Answer = append(resp.Answer, &mdns.AAAA{
					Hdr:  mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeAAAA, Class: mdns.ClassINET, Ttl: 300},
					AAAA: net.ParseIP("2001:db8::1"),
				})
			case q.Qtype == mdns.TypeSRV && q.Name == "_sip._udp.example.test.":
				resp.Answer = append(resp.Answer, &mdns.SRV{
					Hdr:      mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeSRV, Class: mdns.ClassINET, Ttl: 300},
					Priority: 10,
					Weight:   20,
					Port:     5060,
					Target:   "sip.example.test.",
				})
			case q.Qtype == mdns.TypeNAPTR && q.Name == "example.test.":
				resp.Answer = append(resp.Answer,
					&mdns.NAPTR{
						Hdr:         mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeNAPTR, Class: mdns.ClassINET, Ttl: 300},
						Order:       10,
						Preference:  50,
						Flags:       "S",
						Service:     "SIP+D2U",
						Replacement: "_sip._udp.example.test.",
					},
					&mdns.NAPTR{
						Hdr:         mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeNAPTR, Class: mdns.ClassINET, Ttl: 300},
						Order:       10,
						Preference:  30,
						Flags:       "S",
						Service:     "SIP+D2T",
						Replacement: "_sip._tcp.example.test.",
					},
				)
			default:
				resp.Rcode = mdns.RcodeNameError
			}
		}

		_ = w.WriteMsg(resp)
	})

	return mux
}
