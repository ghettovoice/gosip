package dns_test

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	gdns "github.com/ghettovoice/gosip/pkg/dns"
	"github.com/ghettovoice/gosip/sip"
)

type lookupIPCall struct {
	network string
	host    string
}

type lookupSRVCall struct {
	service string
	proto   string
	host    string
}

type lookupNAPTRCall struct {
	host string
}

// stubLookup is a Lookup test double with per-method callbacks and call records.
type stubLookup struct {
	lookupIPFunc    func(ctx context.Context, network, host string) ([]net.IP, error)
	lookupSRVFunc   func(ctx context.Context, service, proto, host string) ([]*gdns.SRV, error)
	lookupNAPTRFunc func(ctx context.Context, host string) ([]*gdns.NAPTR, error)

	mu         sync.Mutex
	ipCalls    []lookupIPCall
	srvCalls   []lookupSRVCall
	naptrCalls []lookupNAPTRCall
}

var (
	_ gdns.Lookup     = (*stubLookup)(nil)
	_ gdns.Lookup     = (*gdns.Resolver)(nil)
	_ gdns.Lookup     = (*gdns.CachedResolver)(nil)
	_ sip.DNSResolver = (*gdns.CachedResolver)(nil)
)

func (s *stubLookup) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	s.mu.Lock()
	s.ipCalls = append(s.ipCalls, lookupIPCall{network: network, host: host})
	s.mu.Unlock()

	if s.lookupIPFunc != nil {
		return s.lookupIPFunc(ctx, network, host)
	}
	return nil, nil
}

func (s *stubLookup) LookupSRV(ctx context.Context, service, proto, host string) ([]*gdns.SRV, error) {
	s.mu.Lock()
	s.srvCalls = append(s.srvCalls, lookupSRVCall{service: service, proto: proto, host: host})
	s.mu.Unlock()

	if s.lookupSRVFunc != nil {
		return s.lookupSRVFunc(ctx, service, proto, host)
	}
	return nil, nil
}

func (s *stubLookup) LookupNAPTR(ctx context.Context, host string) ([]*gdns.NAPTR, error) {
	s.mu.Lock()
	s.naptrCalls = append(s.naptrCalls, lookupNAPTRCall{host: host})
	s.mu.Unlock()

	if s.lookupNAPTRFunc != nil {
		return s.lookupNAPTRFunc(ctx, host)
	}
	return nil, nil
}

func (s *stubLookup) ipCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ipCalls)
}

func (s *stubLookup) srvCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.srvCalls)
}

func (s *stubLookup) naptrCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.naptrCalls)
}

func (s *stubLookup) lastIPCall() lookupIPCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ipCalls[len(s.ipCalls)-1]
}

func (s *stubLookup) lastSRVCall() lookupSRVCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.srvCalls[len(s.srvCalls)-1]
}

func (s *stubLookup) lastNAPTRCall() lookupNAPTRCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.naptrCalls[len(s.naptrCalls)-1]
}

func newTestStubLookup() *stubLookup {
	return &stubLookup{
		lookupIPFunc: func(_ context.Context, _, _ string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1")}, nil
		},
		lookupSRVFunc: func(_ context.Context, _, _, _ string) ([]*gdns.SRV, error) {
			return []*gdns.SRV{
				{Target: "sip.example.com.", Port: 5060, Priority: 10, Weight: 100},
				{Target: "sip2.example.com.", Port: 5060, Priority: 20, Weight: 50},
			}, nil
		},
		lookupNAPTRFunc: func(_ context.Context, _ string) ([]*gdns.NAPTR, error) {
			return []*gdns.NAPTR{
				{
					Order:       10,
					Preference:  20,
					Flags:       "s",
					Service:     "SIP+D2U",
					Replacement: "_sip._udp.example.com.",
				},
			}, nil
		},
	}
}

func mustCachedResolver(t *testing.T, backend gdns.Lookup, opts gdns.CacheOptions) *gdns.CachedResolver {
	t.Helper()

	rslvr, err := gdns.NewCachedResolver(backend, opts)
	if err != nil {
		t.Fatalf("gdns.NewCachedResolver() error = %v, want nil", err)
	}
	return rslvr
}

// lookupOp describes one Lookup operation for table-driven tests: how to
// install a generic result callback on the stub backend, how to invoke the
// resolver method, and how many times the backend was called.
type lookupOp struct {
	name   string
	sample any // a non-empty backend result for this operation
	apply  func(b *stubLookup, fn func(ctx context.Context) (any, error))
	call   func(r *gdns.CachedResolver, ctx context.Context) (any, error)
	calls  func(b *stubLookup) int
}

var lookupOps = []lookupOp{
	{
		name:   "LookupIP",
		sample: []net.IP{net.ParseIP("192.0.2.1")},
		apply: func(b *stubLookup, fn func(context.Context) (any, error)) {
			b.lookupIPFunc = func(ctx context.Context, _, _ string) ([]net.IP, error) {
				v, err := fn(ctx)
				ips, _ := v.([]net.IP)
				return ips, err
			}
		},
		call: func(r *gdns.CachedResolver, ctx context.Context) (any, error) {
			return r.LookupIP(ctx, "ip4", "host.example.com")
		},
		calls: func(b *stubLookup) int { return b.ipCallCount() },
	},
	{
		name:   "LookupSRV",
		sample: []*gdns.SRV{{Target: "sip.example.com.", Port: 5060, Priority: 10, Weight: 100}},
		apply: func(b *stubLookup, fn func(context.Context) (any, error)) {
			b.lookupSRVFunc = func(ctx context.Context, _, _, _ string) ([]*gdns.SRV, error) {
				v, err := fn(ctx)
				recs, _ := v.([]*gdns.SRV)
				return recs, err
			}
		},
		call: func(r *gdns.CachedResolver, ctx context.Context) (any, error) {
			return r.LookupSRV(ctx, "sip", "udp", "example.com")
		},
		calls: func(b *stubLookup) int { return b.srvCallCount() },
	},
	{
		name: "LookupNAPTR",
		sample: []*gdns.NAPTR{
			{Order: 10, Preference: 20, Flags: "s", Service: "SIP+D2U", Replacement: "_sip._udp.example.com."},
		},
		apply: func(b *stubLookup, fn func(context.Context) (any, error)) {
			b.lookupNAPTRFunc = func(ctx context.Context, _ string) ([]*gdns.NAPTR, error) {
				v, err := fn(ctx)
				recs, _ := v.([]*gdns.NAPTR)
				return recs, err
			}
		},
		call: func(r *gdns.CachedResolver, ctx context.Context) (any, error) {
			return r.LookupNAPTR(ctx, "example.com")
		},
		calls: func(b *stubLookup) int { return b.naptrCallCount() },
	},
}

// isNilResult reports whether v is an untyped nil or a typed-nil slice.
func isNilResult(v any) bool {
	rv := reflect.ValueOf(v)
	return !rv.IsValid() || rv.IsNil()
}

// resultLen returns the length of a slice-typed result.
func resultLen(v any) int {
	return reflect.ValueOf(v).Len()
}

func TestNewCachedResolver_InvalidArgs(t *testing.T) {
	t.Parallel()

	validOpts := gdns.CacheOptions{TTL: time.Minute, Capacity: 1}

	t.Run("nil interface backend", func(t *testing.T) {
		t.Parallel()

		if _, err := gdns.NewCachedResolver(nil, validOpts); err == nil {
			t.Error("gdns.NewCachedResolver(nil, opts) error = nil, want error")
		}
	})

	t.Run("typed nil resolver backend", func(t *testing.T) {
		t.Parallel()

		var backend *gdns.Resolver
		if _, err := gdns.NewCachedResolver(backend, validOpts); err == nil {
			t.Error("gdns.NewCachedResolver((*gdns.Resolver)(nil), opts) error = nil, want error")
		}
	})

	t.Run("typed nil stub backend", func(t *testing.T) {
		t.Parallel()

		var backend *stubLookup
		if _, err := gdns.NewCachedResolver(backend, validOpts); err == nil {
			t.Error("gdns.NewCachedResolver((*stubLookup)(nil), opts) error = nil, want error")
		}
	})

	t.Run("negative TTL", func(t *testing.T) {
		t.Parallel()

		opts := gdns.CacheOptions{TTL: -time.Second, Capacity: 1}
		if _, err := gdns.NewCachedResolver(newTestStubLookup(), opts); err == nil {
			t.Error("gdns.NewCachedResolver(backend, {TTL: -1s, Capacity: 1}) error = nil, want error")
		}
	})

	t.Run("enabled TTL with zero capacity", func(t *testing.T) {
		t.Parallel()

		opts := gdns.CacheOptions{TTL: time.Minute, Capacity: 0}
		if _, err := gdns.NewCachedResolver(newTestStubLookup(), opts); err == nil {
			t.Error("gdns.NewCachedResolver(backend, {TTL: 1m, Capacity: 0}) error = nil, want error")
		}
	})
}

func TestCachedResolver_DisabledDelegates(t *testing.T) {
	t.Parallel()

	for _, capacity := range []uint64{0, 8} {
		t.Run(fmt.Sprintf("capacity %d", capacity), func(t *testing.T) {
			t.Parallel()

			backend := newTestStubLookup()
			rslvr := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: 0, Capacity: capacity})

			for range 2 {
				ips, err := rslvr.LookupIP(t.Context(), "ip4", "host.example.com")
				if err != nil {
					t.Fatalf("rslvr.LookupIP() error = %v, want nil", err)
				}
				if len(ips) != 2 {
					t.Errorf("len(ips) = %d, want %d", len(ips), 2)
				}

				srvs, err := rslvr.LookupSRV(t.Context(), "sip", "udp", "example.com")
				if err != nil {
					t.Fatalf("rslvr.LookupSRV() error = %v, want nil", err)
				}
				// Disabled caching is a true pass-through: the backend order
				// is preserved as is.
				if diff := cmp.Diff([]string{"sip.example.com.", "sip2.example.com."}, srvTargets(srvs)); diff != "" {
					t.Errorf("rslvr.LookupSRV() targets mismatch (-want +got):\n%s", diff)
				}

				naptrs, err := rslvr.LookupNAPTR(t.Context(), "example.com")
				if err != nil {
					t.Fatalf("rslvr.LookupNAPTR() error = %v, want nil", err)
				}
				if len(naptrs) != 1 {
					t.Errorf("len(naptrs) = %d, want %d", len(naptrs), 1)
				}
			}

			if n := backend.ipCallCount(); n != 2 {
				t.Errorf("backend ip calls = %d, want %d", n, 2)
			}
			if n := backend.srvCallCount(); n != 2 {
				t.Errorf("backend srv calls = %d, want %d", n, 2)
			}
			if n := backend.naptrCallCount(); n != 2 {
				t.Errorf("backend naptr calls = %d, want %d", n, 2)
			}
		})
	}
}

func TestCachedResolver_CacheHit(t *testing.T) {
	t.Parallel()

	backend := newTestStubLookup()
	rslvr := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: time.Minute, Capacity: 32})
	ctx := t.Context()

	for i := range 2 {
		ips, err := rslvr.LookupIP(ctx, "ip4", "host.example.com")
		if err != nil {
			t.Fatalf("call %d: rslvr.LookupIP() error = %v, want nil", i, err)
		}
		if len(ips) != 2 {
			t.Errorf("call %d: len(ips) = %d, want %d", i, len(ips), 2)
		}
	}
	if n := backend.ipCallCount(); n != 1 {
		t.Errorf("backend ip calls = %d, want %d", n, 1)
	}
	if got, want := backend.lastIPCall(), (lookupIPCall{network: "ip4", host: "host.example.com"}); got != want {
		t.Errorf("last ip call args = %+v, want %+v", got, want)
	}

	for i := range 2 {
		srvs, err := rslvr.LookupSRV(ctx, "sip", "udp", "example.com")
		if err != nil {
			t.Fatalf("call %d: rslvr.LookupSRV() error = %v, want nil", i, err)
		}
		if len(srvs) != 2 {
			t.Errorf("call %d: len(srvs) = %d, want %d", i, len(srvs), 2)
		}
	}
	if n := backend.srvCallCount(); n != 1 {
		t.Errorf("backend srv calls = %d, want %d", n, 1)
	}
	if got, want := backend.lastSRVCall(), (lookupSRVCall{service: "sip", proto: "udp", host: "example.com"}); got != want {
		t.Errorf("last srv call args = %+v, want %+v", got, want)
	}

	for i := range 2 {
		naptrs, err := rslvr.LookupNAPTR(ctx, "example.com")
		if err != nil {
			t.Fatalf("call %d: rslvr.LookupNAPTR() error = %v, want nil", i, err)
		}
		if len(naptrs) != 1 {
			t.Errorf("call %d: len(naptrs) = %d, want %d", i, len(naptrs), 1)
		}
	}
	if n := backend.naptrCallCount(); n != 1 {
		t.Errorf("backend naptr calls = %d, want %d", n, 1)
	}
	if got, want := backend.lastNAPTRCall(), (lookupNAPTRCall{host: "example.com"}); got != want {
		t.Errorf("last naptr call args = %+v, want %+v", got, want)
	}
}

func TestCachedResolver_KeyIsolation(t *testing.T) {
	t.Parallel()

	backend := newTestStubLookup()
	rslvr := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: time.Minute, Capacity: 32})
	ctx := t.Context()

	// Warm the base keys.
	if _, err := rslvr.LookupIP(ctx, "ip4", "host.example.com"); err != nil {
		t.Fatalf("rslvr.LookupIP() error = %v, want nil", err)
	}
	if _, err := rslvr.LookupSRV(ctx, "sip", "udp", "example.com"); err != nil {
		t.Fatalf("rslvr.LookupSRV() error = %v, want nil", err)
	}
	if _, err := rslvr.LookupNAPTR(ctx, "example.com"); err != nil {
		t.Fatalf("rslvr.LookupNAPTR() error = %v, want nil", err)
	}

	// Every argument distinction is a distinct entry: network, service, proto
	// and the raw host spelling (including a trailing dot).
	for i, call := range []func() error{
		func() error { _, err := rslvr.LookupIP(ctx, "ip6", "host.example.com"); return err },
		func() error { _, err := rslvr.LookupIP(ctx, "ip4", "host.example.com."); return err },
		func() error { _, err := rslvr.LookupSRV(ctx, "sips", "udp", "example.com"); return err },
		func() error { _, err := rslvr.LookupSRV(ctx, "sip", "tcp", "example.com"); return err },
		func() error { _, err := rslvr.LookupSRV(ctx, "sip", "udp", "other.example.com"); return err },
		func() error { _, err := rslvr.LookupNAPTR(ctx, "other.example.com"); return err },
	} {
		if err := call(); err != nil {
			t.Fatalf("call %d error = %v, want nil", i, err)
		}
	}

	if n := backend.ipCallCount(); n != 3 {
		t.Errorf("backend ip calls = %d, want %d", n, 3)
	}
	if n := backend.srvCallCount(); n != 4 {
		t.Errorf("backend srv calls = %d, want %d", n, 4)
	}
	if n := backend.naptrCallCount(); n != 2 {
		t.Errorf("backend naptr calls = %d, want %d", n, 2)
	}

	// A NAPTR entry never satisfies an IP or SRV key for the same host and
	// vice versa.
	if _, err := rslvr.LookupIP(ctx, "ip4", "example.com"); err != nil {
		t.Fatalf("rslvr.LookupIP() error = %v, want nil", err)
	}
	if n := backend.ipCallCount(); n != 4 {
		t.Errorf("backend ip calls = %d, want %d", n, 4)
	}

	// Separate resolvers never share entries even over the same backend.
	other := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: time.Minute, Capacity: 32})
	if _, err := other.LookupIP(ctx, "ip4", "host.example.com"); err != nil {
		t.Fatalf("other.LookupIP() error = %v, want nil", err)
	}
	if n := backend.ipCallCount(); n != 5 {
		t.Errorf("backend ip calls = %d, want %d", n, 5)
	}
}

func TestCachedResolver_SharedCapacityLRU(t *testing.T) {
	t.Parallel()

	backend := newTestStubLookup()
	rslvr := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: time.Minute, Capacity: 2})
	ctx := t.Context()

	if _, err := rslvr.LookupIP(ctx, "ip4", "a.example.com"); err != nil {
		t.Fatalf("rslvr.LookupIP() error = %v, want nil", err)
	}
	if _, err := rslvr.LookupNAPTR(ctx, "b.example.com"); err != nil {
		t.Fatalf("rslvr.LookupNAPTR() error = %v, want nil", err)
	}
	// Hit on the IP entry makes it most recently used.
	if _, err := rslvr.LookupIP(ctx, "ip4", "a.example.com"); err != nil {
		t.Fatalf("rslvr.LookupIP() error = %v, want nil", err)
	}
	// Inserting a third entry of another operation evicts the NAPTR entry.
	if _, err := rslvr.LookupSRV(ctx, "sip", "udp", "c.example.com"); err != nil {
		t.Fatalf("rslvr.LookupSRV() error = %v, want nil", err)
	}

	if _, err := rslvr.LookupIP(ctx, "ip4", "a.example.com"); err != nil {
		t.Fatalf("rslvr.LookupIP() error = %v, want nil", err)
	}
	if n := backend.ipCallCount(); n != 1 {
		t.Errorf("backend ip calls = %d, want %d (IP entry must survive eviction)", n, 1)
	}

	if _, err := rslvr.LookupNAPTR(ctx, "b.example.com"); err != nil {
		t.Fatalf("rslvr.LookupNAPTR() error = %v, want nil", err)
	}
	if n := backend.naptrCallCount(); n != 2 {
		t.Errorf("backend naptr calls = %d, want %d (NAPTR entry must be evicted)", n, 2)
	}
}

func TestCachedResolver_TTLExpiry(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		const ttl = time.Minute

		backend := newTestStubLookup()
		rslvr := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: ttl, Capacity: 16})
		ctx := t.Context()

		tests := []struct {
			name   string
			lookup func() error
			calls  func() int
		}{
			{
				name: "LookupIP",
				lookup: func() error {
					_, err := rslvr.LookupIP(ctx, "ip4", "host.example.com")
					return err
				},
				calls: backend.ipCallCount,
			},
			{
				name: "LookupSRV",
				lookup: func() error {
					_, err := rslvr.LookupSRV(ctx, "sip", "udp", "example.com")
					return err
				},
				calls: backend.srvCallCount,
			},
			{
				name: "LookupNAPTR",
				lookup: func() error {
					_, err := rslvr.LookupNAPTR(ctx, "example.com")
					return err
				},
				calls: backend.naptrCallCount,
			},
		}

		for _, tt := range tests {
			if err := tt.lookup(); err != nil {
				t.Fatalf("%s: first lookup error = %v, want nil", tt.name, err)
			}
			if err := tt.lookup(); err != nil {
				t.Fatalf("%s: second lookup error = %v, want nil", tt.name, err)
			}
			if n := tt.calls(); n != 1 {
				t.Fatalf("%s: backend calls = %d, want %d", tt.name, n, 1)
			}

			// A hit shortly before expiry is still served and must not extend
			// the fixed entry lifetime.
			time.Sleep(ttl - time.Second)
			if err := tt.lookup(); err != nil {
				t.Fatalf("%s: lookup before expiry error = %v, want nil", tt.name, err)
			}
			if n := tt.calls(); n != 1 {
				t.Fatalf("%s: backend calls = %d, want %d", tt.name, n, 1)
			}

			// Past the original expiry the backend is queried again: the hit
			// above did not slide the expiration.
			time.Sleep(2 * time.Second)
			if err := tt.lookup(); err != nil {
				t.Fatalf("%s: lookup after expiry error = %v, want nil", tt.name, err)
			}
			if n := tt.calls(); n != 2 {
				t.Fatalf("%s: backend calls = %d, want %d", tt.name, n, 2)
			}
		}
	})
}

func TestCachedResolver_DeepCopyIsolation(t *testing.T) {
	t.Parallel()

	t.Run("LookupIP", func(t *testing.T) {
		t.Parallel()

		backendIPs := []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1")}
		want := []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1")}

		backend := newTestStubLookup()
		backend.lookupIPFunc = func(_ context.Context, _, _ string) ([]net.IP, error) {
			return backendIPs, nil
		}
		rslvr := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: time.Minute, Capacity: 8})
		ctx := t.Context()

		first, err := rslvr.LookupIP(ctx, "ip4", "host.example.com")
		if err != nil {
			t.Fatalf("rslvr.LookupIP() error = %v, want nil", err)
		}

		// Mutating both the backend-owned slice and the returned result must
		// not corrupt the cache.
		backendIPs[0][0] = 255
		first[1][0] = 255

		got, err := rslvr.LookupIP(ctx, "ip4", "host.example.com")
		if err != nil {
			t.Fatalf("rslvr.LookupIP() error = %v, want nil", err)
		}
		if n := backend.ipCallCount(); n != 1 {
			t.Errorf("backend ip calls = %d, want %d", n, 1)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("cached result mismatch (-want +got):\n%s", diff)
		}

		// Mutating a result served from the cache must not corrupt it either.
		got[0][0] = 254
		again, err := rslvr.LookupIP(ctx, "ip4", "host.example.com")
		if err != nil {
			t.Fatalf("rslvr.LookupIP() error = %v, want nil", err)
		}
		if diff := cmp.Diff(want, again); diff != "" {
			t.Errorf("cached result after hit mutation mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("LookupSRV", func(t *testing.T) {
		t.Parallel()

		backendSRVs := []*gdns.SRV{
			{Target: "sip.example.com.", Port: 5060, Priority: 10, Weight: 100},
			{Target: "sip2.example.com.", Port: 5061, Priority: 20, Weight: 50},
		}

		backend := newTestStubLookup()
		backend.lookupSRVFunc = func(_ context.Context, _, _, _ string) ([]*gdns.SRV, error) {
			return backendSRVs, nil
		}
		rslvr := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: time.Minute, Capacity: 8})
		ctx := t.Context()

		first, err := rslvr.LookupSRV(ctx, "sip", "udp", "example.com")
		if err != nil {
			t.Fatalf("rslvr.LookupSRV() error = %v, want nil", err)
		}

		// SIP code sorts returned SRV records in place; reordering and
		// mutating the returned slice must not corrupt the cache.
		slices.Reverse(first)
		first[0].Target = "pwned.example.com."
		first[0].Port = 1
		backendSRVs[0].Port = 2

		want := []*gdns.SRV{
			{Target: "sip.example.com.", Port: 5060, Priority: 10, Weight: 100},
			{Target: "sip2.example.com.", Port: 5061, Priority: 20, Weight: 50},
		}
		got, err := rslvr.LookupSRV(ctx, "sip", "udp", "example.com")
		if err != nil {
			t.Fatalf("rslvr.LookupSRV() error = %v, want nil", err)
		}
		if n := backend.srvCallCount(); n != 1 {
			t.Errorf("backend srv calls = %d, want %d", n, 1)
		}
		if diff := cmp.Diff(want, sortedSRVs(got)); diff != "" {
			t.Errorf("cached result mismatch (-want +got):\n%s", diff)
		}

		// A cache hit result may also be mutated and reordered by the caller.
		slices.Reverse(got)
		got[0].Target = "mutated.example.com."
		got[0].Port = 1
		again, err := rslvr.LookupSRV(ctx, "sip", "udp", "example.com")
		if err != nil {
			t.Fatalf("rslvr.LookupSRV() error = %v, want nil", err)
		}
		if diff := cmp.Diff(want, sortedSRVs(again)); diff != "" {
			t.Errorf("cached result after hit mutation mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("LookupNAPTR", func(t *testing.T) {
		t.Parallel()

		backendNAPTRs := []*gdns.NAPTR{
			{Order: 10, Preference: 20, Flags: "s", Service: "SIP+D2U", Replacement: "_sip._udp.example.com."},
		}
		want := []*gdns.NAPTR{
			{Order: 10, Preference: 20, Flags: "s", Service: "SIP+D2U", Replacement: "_sip._udp.example.com."},
		}

		backend := newTestStubLookup()
		backend.lookupNAPTRFunc = func(_ context.Context, _ string) ([]*gdns.NAPTR, error) {
			return backendNAPTRs, nil
		}
		rslvr := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: time.Minute, Capacity: 8})
		ctx := t.Context()

		first, err := rslvr.LookupNAPTR(ctx, "example.com")
		if err != nil {
			t.Fatalf("rslvr.LookupNAPTR() error = %v, want nil", err)
		}

		first[0].Replacement = "pwned.example.com."
		first[0].Order = 99
		backendNAPTRs[0].Flags = "u"

		got, err := rslvr.LookupNAPTR(ctx, "example.com")
		if err != nil {
			t.Fatalf("rslvr.LookupNAPTR() error = %v, want nil", err)
		}
		if n := backend.naptrCallCount(); n != 1 {
			t.Errorf("backend naptr calls = %d, want %d", n, 1)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("cached result mismatch (-want +got):\n%s", diff)
		}

		// A cache hit result may also be mutated by the caller.
		got[0].Replacement = "mutated.example.com."
		got[0].Order = 99
		again, err := rslvr.LookupNAPTR(ctx, "example.com")
		if err != nil {
			t.Fatalf("rslvr.LookupNAPTR() error = %v, want nil", err)
		}
		if diff := cmp.Diff(want, again); diff != "" {
			t.Errorf("cached result after hit mutation mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("nil entries and slices", func(t *testing.T) {
		t.Parallel()

		backend := newTestStubLookup()
		backend.lookupIPFunc = func(_ context.Context, _, _ string) ([]net.IP, error) {
			return []net.IP{nil, net.ParseIP("192.0.2.1")}, nil
		}
		backend.lookupSRVFunc = func(_ context.Context, _, _, _ string) ([]*gdns.SRV, error) {
			return []*gdns.SRV{nil, {Target: "sip.example.com.", Port: 5060, Priority: 10, Weight: 1}}, nil
		}
		backend.lookupNAPTRFunc = func(_ context.Context, _ string) ([]*gdns.NAPTR, error) {
			return []*gdns.NAPTR{nil, {Order: 10, Preference: 20}}, nil
		}
		rslvr := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: time.Minute, Capacity: 8})
		ctx := t.Context()

		for i := range 2 {
			ips, err := rslvr.LookupIP(ctx, "ip4", "host.example.com")
			if err != nil {
				t.Fatalf("call %d: rslvr.LookupIP() error = %v, want nil", i, err)
			}
			if len(ips) != 2 || ips[0] != nil || !ips[1].Equal(net.ParseIP("192.0.2.1")) {
				t.Errorf("call %d: ips = %v, want [<nil>, 192.0.2.1]", i, ips)
			}

			srvs, err := rslvr.LookupSRV(ctx, "sip", "udp", "example.com")
			if err != nil {
				t.Fatalf("call %d: rslvr.LookupSRV() error = %v, want nil", i, err)
			}
			// Nil SRV entries cannot be ordered by priority and stay at the end.
			if len(srvs) != 2 || srvs[0] == nil || srvs[1] != nil {
				t.Errorf("call %d: srvs = %v, want [<rec>, <nil>]", i, srvs)
			}

			naptrs, err := rslvr.LookupNAPTR(ctx, "example.com")
			if err != nil {
				t.Fatalf("call %d: rslvr.LookupNAPTR() error = %v, want nil", i, err)
			}
			if len(naptrs) != 2 || naptrs[0] != nil || naptrs[1] == nil {
				t.Errorf("call %d: naptrs = %v, want [<nil>, <rec>]", i, naptrs)
			}
		}
	})
}

func TestCachedResolver_Concurrent(t *testing.T) {
	t.Parallel()

	backend := newTestStubLookup()
	rslvr := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: time.Minute, Capacity: 8})
	ctx := t.Context()

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for range 16 {
		wg.Add(3)

		go func() {
			defer wg.Done()

			ips, err := rslvr.LookupIP(ctx, "ip4", "host.example.com")
			if err != nil {
				errs <- errors.Wrap(err)
				return
			}
			want := []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1")}
			if diff := cmp.Diff(want, ips); diff != "" {
				errs <- errors.ErrorfWrap("rslvr.LookupIP() mismatch (-want +got):\n%s", diff)
				return
			}
			// Every caller owns its returned copy and may mutate it freely.
			ips[0][0] = 255
		}()

		go func() {
			defer wg.Done()

			srvs, err := rslvr.LookupSRV(ctx, "sip", "udp", "example.com")
			if err != nil {
				errs <- errors.Wrap(err)
				return
			}
			wantPorts := map[string]uint16{"sip.example.com.": 5060, "sip2.example.com.": 5060}
			if len(srvs) != len(wantPorts) {
				errs <- errors.ErrorfWrap("rslvr.LookupSRV() len = %d, want %d", len(srvs), len(wantPorts))
				return
			}
			for _, rec := range srvs {
				if rec == nil {
					errs <- errors.ErrorWrap("rslvr.LookupSRV() returned a nil record")
					return
				}
				if wantPorts[rec.Target] != rec.Port {
					errs <- errors.ErrorfWrap("rslvr.LookupSRV() record %+v not in expected set", rec)
					return
				}
				rec.Target = "mutated.example.com."
			}
			slices.Reverse(srvs)
		}()

		go func() {
			defer wg.Done()

			naptrs, err := rslvr.LookupNAPTR(ctx, "example.com")
			if err != nil {
				errs <- errors.Wrap(err)
				return
			}
			want := []*gdns.NAPTR{
				{Order: 10, Preference: 20, Flags: "s", Service: "SIP+D2U", Replacement: "_sip._udp.example.com."},
			}
			if diff := cmp.Diff(want, naptrs); diff != "" {
				errs <- errors.ErrorfWrap("rslvr.LookupNAPTR() mismatch (-want +got):\n%s", diff)
				return
			}
			naptrs[0].Replacement = "mutated.example.com."
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent lookup error = %v, want nil", err)
	}
}

func TestCachedResolver_ErrorsNotCached(t *testing.T) {
	t.Parallel()

	errBackend := errors.New("backend failure")

	for _, op := range lookupOps {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()

			t.Run("backend error", func(t *testing.T) {
				t.Parallel()

				backend := newTestStubLookup()
				op.apply(backend, func(context.Context) (any, error) { return nil, errBackend })
				rslvr := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: time.Minute, Capacity: 8})

				for i := range 2 {
					got, err := op.call(rslvr, t.Context())
					if !errors.Is(err, errBackend) {
						t.Errorf("call %d: %s error = %v, want %v", i, op.name, err, errBackend)
					}
					if !isNilResult(got) {
						t.Errorf("call %d: %s result = %v, want nil", i, op.name, got)
					}
				}
				if n := op.calls(backend); n != 2 {
					t.Errorf("backend %s calls = %d, want %d (errors must not be cached)", op.name, n, 2)
				}
			})

			t.Run("partial result with error", func(t *testing.T) {
				t.Parallel()

				backend := newTestStubLookup()
				op.apply(backend, func(context.Context) (any, error) { return op.sample, errBackend })
				rslvr := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: time.Minute, Capacity: 8})

				for i := range 2 {
					got, err := op.call(rslvr, t.Context())
					if !errors.Is(err, errBackend) {
						t.Errorf("call %d: %s error = %v, want %v", i, op.name, err, errBackend)
					}
					if diff := cmp.Diff(op.sample, got); diff != "" {
						t.Errorf("call %d: %s partial result mismatch (-want +got):\n%s", i, op.name, diff)
					}
				}
				if n := op.calls(backend); n != 2 {
					t.Errorf("backend %s calls = %d, want %d (partial error results must not be cached)", op.name, n, 2)
				}
			})

			t.Run("empty success", func(t *testing.T) {
				t.Parallel()

				backend := newTestStubLookup()
				// A non-nil empty slice of the operation's result type.
				empty := reflect.MakeSlice(reflect.TypeOf(op.sample), 0, 0).Interface()
				op.apply(backend, func(context.Context) (any, error) { return empty, nil })
				rslvr := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: time.Minute, Capacity: 8})

				for i := range 2 {
					got, err := op.call(rslvr, t.Context())
					if err != nil {
						t.Errorf("call %d: %s error = %v, want nil", i, op.name, err)
					}
					// A non-nil empty backend result keeps its shape.
					if isNilResult(got) || resultLen(got) != 0 {
						t.Errorf("call %d: %s result = %v, want non-nil empty", i, op.name, got)
					}
				}
				if n := op.calls(backend); n != 2 {
					t.Errorf("backend %s calls = %d, want %d (empty results must not be cached)", op.name, n, 2)
				}
			})

			t.Run("nil success", func(t *testing.T) {
				t.Parallel()

				backend := newTestStubLookup()
				op.apply(backend, func(context.Context) (any, error) { return nil, nil })
				rslvr := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: time.Minute, Capacity: 8})

				for i := range 2 {
					got, err := op.call(rslvr, t.Context())
					if err != nil {
						t.Errorf("call %d: %s error = %v, want nil", i, op.name, err)
					}
					if !isNilResult(got) {
						t.Errorf("call %d: %s result = %v, want nil", i, op.name, got)
					}
				}
				if n := op.calls(backend); n != 2 {
					t.Errorf("backend %s calls = %d, want %d (nil results must not be cached)", op.name, n, 2)
				}
			})
		})
	}
}

func TestCachedResolver_RejectsDoneContext(t *testing.T) {
	t.Parallel()

	ctxKinds := []struct {
		name string
		mk   func(t *testing.T) context.Context
		want error
	}{
		{
			name: "canceled",
			mk: func(t *testing.T) context.Context {
				t.Helper()

				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			want: context.Canceled,
		},
		{
			name: "deadline",
			mk: func(t *testing.T) context.Context {
				t.Helper()

				ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
				t.Cleanup(cancel)
				return ctx
			},
			want: context.DeadlineExceeded,
		},
	}

	for _, op := range lookupOps {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()

			for _, ck := range ctxKinds {
				for _, warm := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/warm=%v", ck.name, warm), func(t *testing.T) {
						t.Parallel()

						backend := newTestStubLookup()
						op.apply(backend, func(context.Context) (any, error) { return op.sample, nil })
						rslvr := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: time.Minute, Capacity: 8})

						wantCalls := 0
						if warm {
							if _, err := op.call(rslvr, t.Context()); err != nil {
								t.Fatalf("%s warm-up error = %v, want nil", op.name, err)
							}
							wantCalls = 1
						}

						// A done context is rejected before the cache lookup
						// and the backend, even on a warm cache.
						got, err := op.call(rslvr, ck.mk(t))
						if !errors.Is(err, ck.want) {
							t.Errorf("%s error = %v, want %v", op.name, err, ck.want)
						}
						if !isNilResult(got) {
							t.Errorf("%s result = %v, want nil", op.name, got)
						}
						if n := op.calls(backend); n != wantCalls {
							t.Errorf("backend %s calls = %d, want %d", op.name, n, wantCalls)
						}

						if warm {
							// The rejection does not disturb the cached entry.
							if _, err := op.call(rslvr, t.Context()); err != nil {
								t.Fatalf("%s post-reject error = %v, want nil", op.name, err)
							}
							if n := op.calls(backend); n != wantCalls {
								t.Errorf("backend %s calls = %d, want %d (warm entry must still hit)", op.name, n, wantCalls)
							}
						}
					})
				}
			}
		})
	}
}

func TestCachedResolver_CanceledDuringBackendNotCached(t *testing.T) {
	t.Parallel()

	for _, op := range lookupOps {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()

			backend := newTestStubLookup()
			rslvr := mustCachedResolver(t, backend, gdns.CacheOptions{TTL: time.Minute, Capacity: 8})

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			op.apply(backend, func(context.Context) (any, error) {
				cancel()
				return op.sample, nil
			})

			// The backend succeeded, so the caller gets the result, but the
			// canceled operation is never stored.
			got, err := op.call(rslvr, ctx)
			if err != nil {
				t.Fatalf("%s error = %v, want nil (backend succeeded)", op.name, err)
			}
			if diff := cmp.Diff(op.sample, got); diff != "" {
				t.Errorf("%s result mismatch (-want +got):\n%s", op.name, diff)
			}

			if _, err := op.call(rslvr, t.Context()); err != nil {
				t.Fatalf("%s second call error = %v, want nil", op.name, err)
			}
			if n := op.calls(backend); n != 2 {
				t.Errorf("backend %s calls = %d, want %d (canceled result must not be cached)", op.name, n, 2)
			}
		})
	}
}

func srvTargets(recs []*gdns.SRV) []string {
	targets := make([]string, 0, len(recs))
	for _, rec := range recs {
		if rec == nil {
			continue
		}
		targets = append(targets, rec.Target)
	}
	return targets
}

// sortedSRVs returns a copy of recs sorted by target so results can be
// compared independently of the RFC 2782 weighted ordering.
func sortedSRVs(recs []*gdns.SRV) []*gdns.SRV {
	out := slices.Clone(recs)
	slices.SortFunc(out, func(a, b *gdns.SRV) int {
		return strings.Compare(a.Target, b.Target)
	})
	return out
}

func ExampleNewCachedResolver() {
	backend := &stubLookup{
		lookupIPFunc: func(_ context.Context, _, _ string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("192.0.2.1")}, nil
		},
	}

	// Wrap any Lookup implementation to opt in to fixed-TTL caching.
	rslvr, err := gdns.NewCachedResolver(backend, gdns.CacheOptions{
		TTL:      5 * time.Second,
		Capacity: 128,
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	// The result is fetched from the backend once, then served from the cache.
	for range 2 {
		ips, err := rslvr.LookupIP(context.Background(), "ip", "example.com")
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Println(ips)
	}

	// Output:
	// [192.0.2.1]
	// [192.0.2.1]
}
