package proxy_test

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/proxy"
)

func TestProxy_Configuration(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	t.Run("route leaves default options untouched", func(t *testing.T) {
		t.Parallel()

		route := proxy.Route{Name: "r"}
		if diff := cmp.Diff(sip.ServerTransactionOptions{}, route.ServerTransactionOptions); diff != "" {
			t.Fatalf("example changes defaults (-want +got): %s", diff)
		}
	})

	t.Run("zero forward mode is stateful", func(t *testing.T) {
		t.Parallel()

		route := &proxy.Route{
			Name: "default-mode",
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{}, nil
			},
		}
		elm, prx, _ := newConfiguredTestProxy(t, route, proxy.Options{
			ServedDomains: []string{"alice.voip.com"},
		})

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".cfg-zero-mode", laddr, raddr)
		// The route resolves no targets, so the pipeline rejects the request;
		// the stateful default must already have created a server transaction.
		if err := prx.InterceptInboundRequest(
			sip.ContextWithElement(t.Context(), elm),
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
		route := &proxy.Route{
			Name: "nil-match",
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{}, nil
			},
		}
		elm, prx, _ := newConfiguredTestProxy(t, route, proxy.Options{
			ServedDomains: []string{"alice.voip.com"},
		})

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".cfg-nil-match", laddr, raddr)
		_ = prx.InterceptInboundRequest(
			sip.ContextWithElement(t.Context(), elm),
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
			routes []proxy.Route
			opts   proxy.Options
		}{
			{
				name:   "unknown forward mode",
				routes: []proxy.Route{{Name: "r", ForwardMode: proxy.ForwardMode(255)}},
			},
			{
				name: "duplicate route names",
				routes: []proxy.Route{
					{Name: "r"},
					{Name: "r", ForwardMode: proxy.ForwardModeStateless},
				},
			},
			{
				name: "unsupported non-invite behavior",
				routes: []proxy.Route{{
					Name: "r",
					ServerTransactionOptions: sip.ServerTransactionOptions{
						NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehavior(255),
					},
				}},
			},
			{
				name:   "negative timer c",
				routes: []proxy.Route{{Name: "r"}},
				opts:   proxy.Options{TimerC: -time.Second},
			},
			{
				name:   "timer c below minimum",
				routes: []proxy.Route{{Name: "r"}},
				opts:   proxy.Options{TimerC: 3 * time.Minute},
			},
			{
				name:   "stateless route without key",
				routes: []proxy.Route{{Name: "r", ForwardMode: proxy.ForwardModeStateless}},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				elm := &sip.Element{}
				t.Cleanup(func() { elm.Close() })

				if _, err := proxy.New(tt.routes, tt.opts); err == nil {
					t.Fatalf("proxy.New() error = nil, want error")
				}
			})
		}
	})

	t.Run("valid configuration accepted", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			routes []proxy.Route
			opts   proxy.Options
		}{
			{
				name:   "stateless forward mode",
				routes: []proxy.Route{{Name: "r", ForwardMode: proxy.ForwardModeStateless}},
				opts:   proxy.Options{StatelessKey: []byte("k")},
			},
			{
				name: "rfc4320 non-invite behavior",
				routes: []proxy.Route{{
					Name: "r",
					ServerTransactionOptions: sip.ServerTransactionOptions{
						NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320,
					},
				}},
			},
			{
				name:   "default timer c",
				routes: []proxy.Route{{Name: "r"}},
				opts:   proxy.Options{},
			},
			{
				name:   "timer c above minimum",
				routes: []proxy.Route{{Name: "r"}},
				opts:   proxy.Options{TimerC: 4 * time.Minute},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				elm := &sip.Element{}
				t.Cleanup(func() { elm.Close() })

				prx, err := proxy.New(tt.routes, tt.opts)
				if err != nil {
					t.Fatalf("proxy.New() error = %v, want nil", err)
				}
				if prx == nil {
					t.Fatal("proxy.New() returned nil proxy")
				}
			})
		}
	})

	t.Run("configuration is owned", func(t *testing.T) {
		t.Parallel()

		var resolved atomic.Int32
		routes := []proxy.Route{{
			Name: "r",
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				resolved.Add(1)
				return proxy.TargetPlan{}, nil
			},
		}}
		opts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}

		tp := newStubTransp()
		elm := &sip.Element{}
		t.Cleanup(func() { elm.Close() })
		if err := elm.TrackTransport(tp); err != nil {
			t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
		}

		prx, err := proxy.New(routes, opts)
		if err != nil {
			t.Fatalf("proxy.New() error = %v, want nil", err)
		}
		if prx == nil {
			t.Fatal("proxy.New() returned nil proxy")
		}

		// Mutating the caller's slices must not change the owned configuration.
		routes[0].Name = "mutated"
		routes[0].MatchRequest = func(context.Context, *sip.RequestEnvelope) bool { return false }
		opts.ServedDomains[0] = "mutated.example.com"

		var nexted atomic.Int32
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".cfg-owned", laddr, raddr)
		_ = prx.InterceptInboundRequest(
			sip.ContextWithElement(t.Context(), elm),
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
				nexted.Add(1)
				return nil
			}),
			req,
		)

		if got := nexted.Load(); got != 0 {
			t.Fatalf("next receiver called %d times, want 0 (owned route must still match)", got)
		}
		if md, ok := proxy.MetadataFrom(req.Metadata()); !ok || md.RouteName != "r" {
			t.Fatalf("route claim stamp = (%+v, %t), want route name %q", md, ok, "r")
		}
		if got := resolved.Load(); got == 0 {
			t.Fatal("route.ResolveRequestTargets not called, owned served domain must remain responsible")
		}
	})

	t.Run("failed construction installs no component", func(t *testing.T) {
		t.Parallel()

		tp := newStubTransp()
		elm := &sip.Element{}
		t.Cleanup(func() { elm.Close() })
		if err := elm.TrackTransport(tp); err != nil {
			t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
		}

		before := tp.InboundRequestInterceptors.Len()

		_, err := proxy.New([]proxy.Route{{Name: "bad", ForwardMode: proxy.ForwardMode(255)}})
		if err == nil {
			t.Fatal("proxy.New() error = nil, want error")
		}

		if got := tp.InboundRequestInterceptors.Len(); got != before {
			t.Fatalf("inbound request interceptors = %d, want unchanged %d", got, before)
		}

		// Construction never installs components; only the caller's
		// explicit UseComponent registration attaches the proxy.
		prx, err := proxy.New([]proxy.Route{{Name: "good"}})
		if err != nil {
			t.Fatalf("proxy.New() error = %v, want nil", err)
		}
		if got := tp.InboundRequestInterceptors.Len(); got != before {
			t.Fatalf("inbound request interceptors after New = %d, want unchanged %d", got, before)
		}
		elm.UseComponent(prx)
		if got, want := tp.InboundRequestInterceptors.Len(), before+1; got != want {
			t.Fatalf("inbound request interceptors = %d, want %d", got, want)
		}
	})
}

func TestDefaultTargetSelector(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		groups [][]proxy.Target
		want   []string
	}{
		{name: "no groups"},
		{name: "empty groups", groups: [][]proxy.Target{}},
		{name: "nil first group", groups: [][]proxy.Target{nil, {{ID: "later"}}}},
		{name: "empty first group", groups: [][]proxy.Target{{}, {{ID: "later"}}}},
		{
			name:   "first group only in original order",
			groups: [][]proxy.Target{{{ID: "b"}, {ID: "a"}}, {{ID: "later"}}},
			want:   []string{"b", "a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := proxy.DefaultTargetSelector(
				t.Context(),
				proxy.Selection{RemainingGroups: tt.groups},
			)
			if err != nil {
				t.Fatalf("proxy.DefaultTargetSelector() error = %v, want nil", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("selected IDs (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("result is independent of input", func(t *testing.T) {
		t.Parallel()

		groups := [][]proxy.Target{{{ID: "b"}, {ID: "a"}}, {{ID: "later"}}}
		wantGroups := [][]proxy.Target{{{ID: "b"}, {ID: "a"}}, {{ID: "later"}}}

		got, err := proxy.DefaultTargetSelector(
			t.Context(),
			proxy.Selection{RemainingGroups: groups},
		)
		if err != nil {
			t.Fatalf("proxy.DefaultTargetSelector() error = %v, want nil", err)
		}
		got[0] = "mutated"

		again, err := proxy.DefaultTargetSelector(
			t.Context(),
			proxy.Selection{RemainingGroups: groups},
		)
		if err != nil {
			t.Fatalf("proxy.DefaultTargetSelector() error = %v, want nil", err)
		}
		if diff := cmp.Diff([]string{"b", "a"}, again); diff != "" {
			t.Fatalf("re-selected IDs (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(wantGroups, groups); diff != "" {
			t.Fatalf("input groups mutated (-want +got):\n%s", diff)
		}
	})
}
