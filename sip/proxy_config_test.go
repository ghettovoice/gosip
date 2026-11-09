package sip_test

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/sip"
)

func TestProxy_Configuration(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	t.Run("route leaves default options untouched", func(t *testing.T) {
		t.Parallel()

		route := sip.ProxyRoute{Name: "r"}
		if diff := cmp.Diff(sip.ServerTransactionOptions{}, route.ServerTransactionOptions); diff != "" {
			t.Fatalf("example changes defaults (-want +got): %s", diff)
		}
	})

	t.Run("nil element rejected", func(t *testing.T) {
		t.Parallel()

		if _, err := sip.NewProxy(nil, nil); err == nil {
			t.Fatal("sip.NewProxy(nil, nil) error = nil, want error")
		}
	})

	t.Run("zero forward mode is stateful", func(t *testing.T) {
		t.Parallel()

		route := &sip.ProxyRoute{
			Name: "default-mode",
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
				return sip.ProxyTargetPlan{}, nil
			},
		}
		elm, prx, _ := newConfiguredTestProxy(t, route, sip.ProxyOptions{
			ServedDomains: []string{"alice.voip.com"},
		})

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".cfg-zero-mode", laddr, raddr)
		// The route resolves no targets, so the pipeline rejects the request;
		// the stateful default must already have created a server transaction.
		if err := prx.InterceptInboundRequest(
			t.Context(),
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil }),
			req,
		); err == nil {
			t.Fatal("prx.InterceptInboundRequest() error = nil, want no-targets rejection")
		}

		key, err := sip.MakeServerTransactionKey(req)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		if _, ok := elm.LoadServerTransaction(key); !ok {
			t.Fatal("no server transaction for zero ForwardMode, want stateful handling")
		}
	})

	t.Run("nil match request accepts every request", func(t *testing.T) {
		t.Parallel()

		var nexted atomic.Int32
		route := &sip.ProxyRoute{
			Name: "nil-match",
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
				return sip.ProxyTargetPlan{}, nil
			},
		}
		_, prx, _ := newConfiguredTestProxy(t, route, sip.ProxyOptions{
			ServedDomains: []string{"alice.voip.com"},
		})

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".cfg-nil-match", laddr, raddr)
		_ = prx.InterceptInboundRequest(
			t.Context(),
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
				nexted.Add(1)
				return nil
			}),
			req,
		)

		if got := nexted.Load(); got != 0 {
			t.Fatalf("next receiver called %d times, want 0 (nil MatchRequest must match)", got)
		}
	})

	t.Run("invalid configuration rejected", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			routes []sip.ProxyRoute
			opts   sip.ProxyOptions
		}{
			{
				name:   "unknown forward mode",
				routes: []sip.ProxyRoute{{Name: "r", ForwardMode: sip.ForwardMode(255)}},
			},
			{
				name: "duplicate route names",
				routes: []sip.ProxyRoute{
					{Name: "r"},
					{Name: "r", ForwardMode: sip.ForwardModeStateless},
				},
			},
			{
				name: "unsupported non-invite behavior",
				routes: []sip.ProxyRoute{{
					Name: "r",
					ServerTransactionOptions: sip.ServerTransactionOptions{
						NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehavior(255),
					},
				}},
			},
			{
				name:   "negative timer c",
				routes: []sip.ProxyRoute{{Name: "r"}},
				opts:   sip.ProxyOptions{TimerC: -time.Second},
			},
			{
				name:   "timer c below minimum",
				routes: []sip.ProxyRoute{{Name: "r"}},
				opts:   sip.ProxyOptions{TimerC: 3 * time.Minute},
			},
			{
				name:   "stateless route without key",
				routes: []sip.ProxyRoute{{Name: "r", ForwardMode: sip.ForwardModeStateless}},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				elm := &sip.Element{}
				t.Cleanup(func() { elm.Close() })

				if _, err := sip.NewProxy(elm, tt.routes, tt.opts); err == nil {
					t.Fatalf("sip.NewProxy() error = nil, want error")
				}
			})
		}
	})

	t.Run("valid configuration accepted", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			routes []sip.ProxyRoute
			opts   sip.ProxyOptions
		}{
			{
				name:   "stateless forward mode",
				routes: []sip.ProxyRoute{{Name: "r", ForwardMode: sip.ForwardModeStateless}},
				opts:   sip.ProxyOptions{StatelessKey: []byte("k")},
			},
			{
				name: "rfc4320 non-invite behavior",
				routes: []sip.ProxyRoute{{
					Name: "r",
					ServerTransactionOptions: sip.ServerTransactionOptions{
						NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320,
					},
				}},
			},
			{
				name:   "default timer c",
				routes: []sip.ProxyRoute{{Name: "r"}},
				opts:   sip.ProxyOptions{},
			},
			{
				name:   "timer c above minimum",
				routes: []sip.ProxyRoute{{Name: "r"}},
				opts:   sip.ProxyOptions{TimerC: 4 * time.Minute},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				elm := &sip.Element{}
				t.Cleanup(func() { elm.Close() })

				prx, err := sip.NewProxy(elm, tt.routes, tt.opts)
				if err != nil {
					t.Fatalf("sip.NewProxy() error = %v, want nil", err)
				}
				if prx == nil {
					t.Fatal("sip.NewProxy() returned nil proxy")
				}
			})
		}
	})

	t.Run("configuration is owned", func(t *testing.T) {
		t.Parallel()

		var resolved atomic.Int32
		routes := []sip.ProxyRoute{{
			Name: "r",
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
				resolved.Add(1)
				return sip.ProxyTargetPlan{}, nil
			},
		}}
		opts := sip.ProxyOptions{ServedDomains: []string{"alice.voip.com"}}

		tp := newPrxStubTransp()
		elm := &sip.Element{}
		t.Cleanup(func() { elm.Close() })
		if err := elm.TrackTransport(tp); err != nil {
			t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
		}

		prx, err := sip.NewProxy(elm, routes, opts)
		if err != nil {
			t.Fatalf("sip.NewProxy() error = %v, want nil", err)
		}
		if prx == nil {
			t.Fatal("sip.NewProxy() returned nil proxy")
		}

		// Mutating the caller's slices must not change the owned configuration.
		routes[0].Name = "mutated"
		routes[0].MatchRequest = func(context.Context, *sip.RequestEnvelope) bool { return false }
		opts.ServedDomains[0] = "mutated.example.com"

		var nexted atomic.Int32
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".cfg-owned", laddr, raddr)
		_ = prx.InterceptInboundRequest(
			t.Context(),
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
				nexted.Add(1)
				return nil
			}),
			req,
		)

		if got := nexted.Load(); got != 0 {
			t.Fatalf("next receiver called %d times, want 0 (owned route must still match)", got)
		}
		if v, ok := req.Metadata().Get("sip.proxy_route"); !ok || v != "r" {
			t.Fatalf("route metadata = (%v, %t), want (%q, true)", v, ok, "r")
		}
		if got := resolved.Load(); got == 0 {
			t.Fatal("route.ResolveRequestTargets not called, owned served domain must remain responsible")
		}
	})

	t.Run("failed construction installs no middleware", func(t *testing.T) {
		t.Parallel()

		tp := newPrxStubTransp()
		elm := &sip.Element{}
		t.Cleanup(func() { elm.Close() })
		if err := elm.TrackTransport(tp); err != nil {
			t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
		}

		before := tp.InboundRequestInterceptors.Len()

		_, err := sip.NewProxy(elm, []sip.ProxyRoute{{Name: "bad", ForwardMode: sip.ForwardMode(255)}})
		if err == nil {
			t.Fatal("sip.NewProxy() error = nil, want error")
		}

		if got := tp.InboundRequestInterceptors.Len(); got != before {
			t.Fatalf("inbound request interceptors = %d, want unchanged %d", got, before)
		}

		if _, err := sip.NewProxy(elm, []sip.ProxyRoute{{Name: "good"}}); err != nil {
			t.Fatalf("sip.NewProxy() error = %v, want nil", err)
		}
		if got, want := tp.InboundRequestInterceptors.Len(), before+1; got != want {
			t.Fatalf("inbound request interceptors = %d, want %d", got, want)
		}
	})
}
