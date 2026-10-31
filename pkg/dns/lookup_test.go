package dns_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	mdns "codeberg.org/miekg/dns"
	"github.com/google/go-cmp/cmp"

	gdns "github.com/ghettovoice/gosip/pkg/dns"
)

func newTestResolver(t *testing.T, nameserver string) *gdns.Resolver {
	t.Helper()

	return &gdns.Resolver{NameServer: nameserver}
}

func startTestDNSServer(t *testing.T, handler ...mdns.Handler) string {
	t.Helper()

	pktConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket(%q, %q) error = %v, want nil", "udp", "127.0.0.1:0", err)
	}

	hdlr := newTestDNSHandler()
	if len(handler) > 0 {
		hdlr = handler[0]
	}

	started := make(chan struct{})
	done := make(chan error, 1)
	dnsSrv := &mdns.Server{
		PacketConn:        pktConn,
		Handler:           hdlr,
		NotifyStartedFunc: func(context.Context) { close(started) },
	}
	go func() { done <- dnsSrv.ListenAndServe() }()

	select {
	case <-started:
	case err := <-done:
		_ = pktConn.Close()

		t.Fatalf("dnsSrv.ListenAndServe() error = %v, want nil", err)
	case <-time.After(time.Second):
		_ = pktConn.Close()

		t.Fatal("DNS server start timeout")
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		dnsSrv.Shutdown(ctx)
		_ = pktConn.Close()

		select {
		case err := <-done:
			if err != nil {
				t.Errorf("dnsSrv.ListenAndServe() error = %v, want nil", err)
			}
		case <-time.After(time.Second):
			t.Error("DNS server shutdown timeout")
		}
	})

	return pktConn.LocalAddr().String()
}

func newTestDNSHandler() mdns.Handler {
	mux := mdns.NewServeMux()
	mux.HandleFunc("example.test.", func(_ context.Context, w mdns.ResponseWriter, req *mdns.Msg) {
		resp := &mdns.Msg{
			ID:                 req.ID,
			Response:           true,
			Authoritative:      true,
			RecursionDesired:   req.RecursionDesired,
			RecursionAvailable: true,
			Question:           req.Question,
		}
		resp.Authoritative = true

		for _, q := range req.Question {
			qName := q.Header().Name

			switch {
			case isQuestionType[*mdns.A](q) && qName == "host.example.test.":
				resp.Answer = append(resp.Answer, &mdns.A{
					Hdr:  mdns.Header{Name: qName, Class: mdns.ClassINET, TTL: 300},
					Addr: netip.MustParseAddr("127.0.0.1"),
				})
			case isQuestionType[*mdns.AAAA](q) && qName == "host.example.test.":
				resp.Answer = append(resp.Answer, &mdns.AAAA{
					Hdr:  mdns.Header{Name: qName, Class: mdns.ClassINET, TTL: 300},
					Addr: netip.MustParseAddr("2001:db8::1"),
				})
			case isQuestionType[*mdns.SRV](q) && qName == "_sip._udp.example.test.":
				resp.Answer = append(resp.Answer, &mdns.SRV{
					Hdr:      mdns.Header{Name: qName, Class: mdns.ClassINET, TTL: 300},
					Priority: 10,
					Weight:   20,
					Port:     5060,
					Target:   "sip.example.test.",
				})
			case isQuestionType[*mdns.NAPTR](q) && qName == "example.test.":
				resp.Answer = append(
					resp.Answer,
					&mdns.NAPTR{
						Hdr:         mdns.Header{Name: qName, Class: mdns.ClassINET, TTL: 300},
						Order:       10,
						Preference:  50,
						Flags:       "S",
						Service:     "SIP+D2U",
						Replacement: "_sip._udp.example.test.",
					},
					&mdns.NAPTR{
						Hdr:         mdns.Header{Name: qName, Class: mdns.ClassINET, TTL: 300},
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

		_, _ = resp.WriteTo(w)
	})

	return mux
}

func isQuestionType[T mdns.RR](q mdns.RR) bool {
	_, ok := q.(T)
	return ok
}

func TestResolver_LookupIP(t *testing.T) {
	t.Parallel()

	rslvr := newTestResolver(t, startTestDNSServer(t))
	got, err := rslvr.LookupIP(t.Context(), "ip", "host.example.test")
	if err != nil {
		t.Fatalf("rslvr.LookupIP(ctx, %q, %q) error = %v, want nil", "ip", "host.example.test", err)
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
		t.Fatalf("rslvr.LookupIP() missing IPv4 record in %v", got)
	}

	if !hasIPv6 {
		t.Fatalf("rslvr.LookupIP() missing IPv6 record in %v", got)
	}
}

func TestResolver_LookupSRV(t *testing.T) {
	t.Parallel()

	rslvr := newTestResolver(t, startTestDNSServer(t))
	got, err := rslvr.LookupSRV(t.Context(), "sip", "udp", "example.test")
	if err != nil {
		t.Fatalf("rslvr.LookupSRV(ctx, %q, %q, %q) error = %v, want nil", "sip", "udp", "example.test", err)
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
		t.Errorf("rslvr.LookupSRV(ctx, %q, %q, %q) mismatch (-want +got):\n%s", "sip", "udp", "example.test", diff)
	}
}

func TestResolver_LookupNAPTR(t *testing.T) {
	t.Parallel()

	rslvr := newTestResolver(t, startTestDNSServer(t))

	t.Run("sort records by order and preference", func(t *testing.T) {
		t.Parallel()

		got, err := rslvr.LookupNAPTR(t.Context(), "example.test")
		if err != nil {
			t.Fatalf("rslvr.LookupNAPTR(ctx, %q) error = %v, want nil", "example.test", err)
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
			t.Errorf("rslvr.LookupNAPTR(ctx, %q) mismatch (-want +got):\n%s", "example.test", diff)
		}
	})

	t.Run("return not found error for nxdomain", func(t *testing.T) {
		t.Parallel()

		_, err := rslvr.LookupNAPTR(t.Context(), "missing.example.test")
		if err == nil {
			t.Fatalf("rslvr.LookupNAPTR(ctx, %q) error = nil, want error", "missing.example.test")
		}

		var dnsErr *net.DNSError
		if !errors.As(err, &dnsErr) {
			t.Fatalf("rslvr.LookupNAPTR(ctx, %q) error type = %T, want *net.DNSError", "missing.example.test", err)
		}

		if !dnsErr.IsNotFound {
			t.Errorf("dnsErr.IsNotFound = %v, want true", dnsErr.IsNotFound)
		}

		if dnsErr.Name != "missing.example.test" {
			t.Errorf("dnsErr.Name = %q, want %q", dnsErr.Name, "missing.example.test")
		}
	})
}

func TestResolver_LookupNAPTR_Cancel(t *testing.T) {
	t.Parallel()

	// The server receives queries but never responds.
	queries := make(chan struct{}, 1)
	mux := mdns.NewServeMux()
	mux.HandleFunc("example.test.", func(_ context.Context, _ mdns.ResponseWriter, _ *mdns.Msg) {
		select {
		case queries <- struct{}{}:
		default:
		}
	})
	rslvr := newTestResolver(t, startTestDNSServer(t, mux))

	t.Run("cancel after query received", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go func() {
			select {
			case <-queries:
				cancel()
			case <-t.Context().Done():
			}
		}()

		done := make(chan error, 1)
		go func() {
			_, err := rslvr.LookupNAPTR(ctx, "example.test")
			done <- err
		}()

		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("rslvr.LookupNAPTR(ctx, %q) error = %v, want %v", "example.test", err, context.Canceled)
			}
		case <-time.After(5 * time.Second):
			t.Error("rslvr.LookupNAPTR(ctx) did not return after context cancellation")
		}
	})

	t.Run("expired context returns immediately", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		defer cancel()

		_, err := rslvr.LookupNAPTR(ctx, "example.test")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("rslvr.LookupNAPTR(ctx, %q) error = %v, want %v", "example.test", err, context.DeadlineExceeded)
		}
	})
}

func TestResolver_ConcurrentInit(t *testing.T) {
	t.Parallel()

	r := &gdns.Resolver{NameServer: startTestDNSServer(t)}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	errs := make(chan error, 30)
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(3)
		go func() {
			defer wg.Done()

			_, err := r.LookupIP(ctx, "ip4", "host.example.test")
			errs <- err
		}()
		go func() {
			defer wg.Done()

			_, err := r.LookupSRV(ctx, "sip", "udp", "example.test")
			errs <- err
		}()
		go func() {
			defer wg.Done()

			_, err := r.LookupNAPTR(ctx, "example.test")
			errs <- err
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent lookup error = %v, want nil", err)
		}
	}
}
