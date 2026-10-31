package dnstest_test

import (
	"context"
	"net"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil/dnstest"
	"github.com/ghettovoice/gosip/pkg/dns"
)

func TestStubResolver_NilHooks(t *testing.T) {
	t.Parallel()

	resolver := dnstest.StubResolver{}
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "LookupIP",
			run: func(t *testing.T) {
				t.Helper()
				if got, err := resolver.LookupIP(t.Context(), "ip4", "example.com"); got != nil || err != nil {
					t.Errorf("dnstest.StubResolver.LookupIP() = (%v, %v), want (nil, nil)", got, err)
				}
			},
		},
		{
			name: "LookupSRV",
			run: func(t *testing.T) {
				t.Helper()
				if got, err := resolver.LookupSRV(t.Context(), "sip", "udp", "example.com"); got != nil || err != nil {
					t.Errorf("dnstest.StubResolver.LookupSRV() = (%v, %v), want (nil, nil)", got, err)
				}
			},
		},
		{
			name: "LookupNAPTR",
			run: func(t *testing.T) {
				t.Helper()
				if got, err := resolver.LookupNAPTR(t.Context(), "example.com"); got != nil || err != nil {
					t.Errorf("dnstest.StubResolver.LookupNAPTR() = (%v, %v), want (nil, nil)", got, err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.run(t)
		})
	}
}

func TestStubResolver_ForwardsLookups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "LookupIP forwards arguments, result, and error",
			run: func(t *testing.T) {
				t.Helper()
				type contextKey struct{}
				ctx := context.WithValue(t.Context(), contextKey{}, "forwarded")
				wantErr := errors.ErrorWrap("lookup failure")
				wantIPs := []net.IP{net.ParseIP("192.0.2.1")}
				called := false
				resolver := dnstest.StubResolver{
					LookupIPFunc: func(got context.Context, network, host string) ([]net.IP, error) {
						called = true
						if got != ctx {
							t.Errorf("dnstest.StubResolver.LookupIPFunc() context = %v, want forwarded context", got)
						}
						if network != "ip4" || host != "ip.example.com" {
							t.Errorf("dnstest.StubResolver.LookupIPFunc() arguments = (%q, %q), want (%q, %q)",
								network, host, "ip4", "ip.example.com")
						}
						return wantIPs, wantErr
					},
				}

				gotIPs, gotErr := resolver.LookupIP(ctx, "ip4", "ip.example.com")
				if !called {
					t.Error("dnstest.StubResolver.LookupIPFunc was not called")
				}
				if diff := cmp.Diff(wantIPs, gotIPs); diff != "" {
					t.Errorf("dnstest.StubResolver.LookupIP() mismatch (-want +got):\n%s", diff)
				}
				if !errors.Is(gotErr, wantErr) {
					t.Errorf("dnstest.StubResolver.LookupIP() error = %v, want wrapped %v", gotErr, wantErr)
				}
			},
		},
		{
			name: "LookupSRV forwards arguments, result, and error",
			run: func(t *testing.T) {
				t.Helper()
				type contextKey struct{}
				ctx := context.WithValue(t.Context(), contextKey{}, "forwarded")
				wantErr := errors.ErrorWrap("lookup failure")
				wantSRV := []*dns.SRV{{Target: "sip.example.com.", Port: 5060}}
				called := false
				resolver := dnstest.StubResolver{
					LookupSRVFunc: func(
						got context.Context,
						service, proto, host string,
					) ([]*dns.SRV, error) {
						called = true
						if got != ctx {
							t.Errorf("dnstest.StubResolver.LookupSRVFunc() context = %v, want forwarded context", got)
						}
						if service != "sip" || proto != "udp" || host != "srv.example.com" {
							t.Errorf("dnstest.StubResolver.LookupSRVFunc() arguments = (%q, %q, %q), want (%q, %q, %q)",
								service, proto, host, "sip", "udp", "srv.example.com")
						}
						return wantSRV, wantErr
					},
				}

				gotSRV, gotErr := resolver.LookupSRV(ctx, "sip", "udp", "srv.example.com")
				if !called {
					t.Error("dnstest.StubResolver.LookupSRVFunc was not called")
				}
				if diff := cmp.Diff(wantSRV, gotSRV); diff != "" {
					t.Errorf("dnstest.StubResolver.LookupSRV() mismatch (-want +got):\n%s", diff)
				}
				if !errors.Is(gotErr, wantErr) {
					t.Errorf("dnstest.StubResolver.LookupSRV() error = %v, want wrapped %v", gotErr, wantErr)
				}
			},
		},
		{
			name: "LookupNAPTR forwards arguments, result, and error",
			run: func(t *testing.T) {
				t.Helper()
				type contextKey struct{}
				ctx := context.WithValue(t.Context(), contextKey{}, "forwarded")
				wantErr := errors.ErrorWrap("lookup failure")
				wantNAPTR := []*dns.NAPTR{{Service: "SIP+D2U", Replacement: "example.com."}}
				called := false
				resolver := dnstest.StubResolver{
					LookupNAPTRFunc: func(got context.Context, host string) ([]*dns.NAPTR, error) {
						called = true
						if got != ctx {
							t.Errorf("dnstest.StubResolver.LookupNAPTRFunc() context = %v, want forwarded context", got)
						}
						if host != "naptr.example.com" {
							t.Errorf("dnstest.StubResolver.LookupNAPTRFunc() host = %q, want %q", host, "naptr.example.com")
						}
						return wantNAPTR, wantErr
					},
				}

				gotNAPTR, gotErr := resolver.LookupNAPTR(ctx, "naptr.example.com")
				if !called {
					t.Error("dnstest.StubResolver.LookupNAPTRFunc was not called")
				}
				if diff := cmp.Diff(wantNAPTR, gotNAPTR); diff != "" {
					t.Errorf("dnstest.StubResolver.LookupNAPTR() mismatch (-want +got):\n%s", diff)
				}
				if !errors.Is(gotErr, wantErr) {
					t.Errorf("dnstest.StubResolver.LookupNAPTR() error = %v, want wrapped %v", gotErr, wantErr)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.run(t)
		})
	}
}
