package proxy_test

import (
	"testing"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/proxy"
)

func newAddrTestProxy(tb testing.TB, identity *sip.URI, domains ...string) *proxy.Proxy {
	tb.Helper()

	opts := proxy.Options{ServedDomains: domains}
	if identity != nil {
		opts.LocalIdentities = []*sip.URI{identity}
	}
	prx, err := proxy.New(nil, opts)
	if err != nil {
		tb.Fatalf("proxy.New() error = %v, want nil", err)
	}
	tb.Cleanup(func() { _ = prx.Close() })
	return prx
}

func TestProxy_IsSelfAddr(t *testing.T) {
	t.Parallel()

	self := sip.MakeHostAddr("pxy.example.net")
	self5060 := sip.MakeHostPortAddr("pxy.example.net", 5060)
	self5090 := sip.MakeHostPortAddr("pxy.example.net", 5090)
	served := sip.MakeHostAddr("example.com")
	served5090 := sip.MakeHostPortAddr("example.com", 5090)
	foreign := sip.MakeHostAddr("other.example.net")

	tests := []struct {
		name        string
		identity    sip.Addr
		query       sip.Addr
		defaultPort uint16
		want        bool
	}{
		{"missing configured port filled by default", self, self5060, 5060, true},
		{"missing query port filled by default", self5060, self, 5060, true},
		{"explicit non-default port overrides default", self5090, self, 5060, false},
		{"explicit query port overrides default", self, self5090, 5060, false},
		{"equal explicit ports match", self5090, self5090, 5060, true},
		{"zero default ignores ports", self5090, self5060, 0, true},
		{"served domain is not a local identity", self, served, 0, false},
		{"served domain port is not a local identity", self, served5090, 0, false},
		{"foreign address", self, foreign, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prx := newAddrTestProxy(t, &sip.URI{Addr: tt.identity}, "example.com")
			if got := prx.IsSelfAddr(tt.query, tt.defaultPort); got != tt.want {
				t.Errorf("prx.IsSelfAddr(%v, %d) = %v, want %v", tt.query, tt.defaultPort, got, tt.want)
			}
		})
	}
}

func TestProxy_IsServedAddr(t *testing.T) {
	t.Parallel()

	self := sip.MakeHostAddr("pxy.example.net")
	served := sip.MakeHostAddr("example.com")
	served5090 := sip.MakeHostPortAddr("example.com", 5090)
	foreign := sip.MakeHostAddr("other.example.net")

	tests := []struct {
		name  string
		query sip.Addr
		want  bool
	}{
		{"served domain", served, true},
		{"served domain explicit port", served5090, true},
		{"local identity is not a served domain", self, false},
		{"foreign address", foreign, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prx := newAddrTestProxy(t, &sip.URI{Addr: self}, "example.com")
			if got := prx.IsServedAddr(tt.query); got != tt.want {
				t.Errorf("prx.IsServedAddr(%v) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestProxy_IsResponsibleAddr(t *testing.T) {
	t.Parallel()

	self := sip.MakeHostAddr("pxy.example.net")
	self5090 := sip.MakeHostPortAddr("pxy.example.net", 5090)
	served := sip.MakeHostAddr("example.com")
	served5090 := sip.MakeHostPortAddr("example.com", 5090)
	foreign := sip.MakeHostAddr("other.example.net")

	tests := []struct {
		name  string
		query sip.Addr
		want  bool
	}{
		{"local identity", self, true},
		{"local identity explicit port", self5090, true},
		{"served domain", served, true},
		{"served domain explicit port", served5090, true},
		{"foreign address", foreign, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prx := newAddrTestProxy(t, &sip.URI{Addr: self}, "example.com")
			if got := prx.IsResponsibleAddr(tt.query); got != tt.want {
				t.Errorf("prx.IsResponsibleAddr(%v) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}

	t.Run("address configured in both lists", func(t *testing.T) {
		t.Parallel()

		prx := newAddrTestProxy(t, &sip.URI{Addr: self}, "example.com", "pxy.example.net")
		if !prx.IsSelfAddr(self, 0) {
			t.Error("prx.IsSelfAddr(self) = false, want true")
		}
		if !prx.IsServedAddr(self) {
			t.Error("prx.IsServedAddr(self) = false, want true")
		}
		if !prx.IsResponsibleAddr(self) {
			t.Error("prx.IsResponsibleAddr(self) = false, want true")
		}
	})

	t.Run("unconfigured proxy matches nothing", func(t *testing.T) {
		t.Parallel()

		prx := newAddrTestProxy(t, nil)
		for _, addr := range []sip.Addr{self, self5090, served, foreign} {
			if prx.IsSelfAddr(addr, 5060) {
				t.Errorf("prx.IsSelfAddr(%v) = true, want false", addr)
			}
			if prx.IsServedAddr(addr) {
				t.Errorf("prx.IsServedAddr(%v) = true, want false", addr)
			}
			if prx.IsResponsibleAddr(addr) {
				t.Errorf("prx.IsResponsibleAddr(%v) = true, want false", addr)
			}
		}
	})
}

func TestProxy_AddressPredicatesOwnConfiguration(t *testing.T) {
	t.Parallel()

	self := sip.MakeHostAddr("pxy.example.net")
	served := sip.MakeHostAddr("example.com")
	foreign := sip.MakeHostAddr("other.example.net")

	identity := &sip.URI{Addr: self}
	domains := []string{"example.com"}
	prx, err := proxy.New(nil, proxy.Options{
		LocalIdentities: []*sip.URI{identity},
		ServedDomains:   domains,
	})
	if err != nil {
		t.Fatalf("proxy.New() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = prx.Close() })

	// Mutating the caller's URI and slice must not change the copied
	// configuration the predicates consult.
	identity.Addr = foreign
	domains[0] = "other.example.net"

	if !prx.IsSelfAddr(self, 0) {
		t.Error("prx.IsSelfAddr(original identity) = false, want true")
	}
	if !prx.IsServedAddr(served) {
		t.Error("prx.IsServedAddr(original served domain) = false, want true")
	}
	if !prx.IsResponsibleAddr(self) {
		t.Error("prx.IsResponsibleAddr(original identity) = false, want true")
	}
	if !prx.IsResponsibleAddr(served) {
		t.Error("prx.IsResponsibleAddr(original served domain) = false, want true")
	}

	if prx.IsSelfAddr(foreign, 0) {
		t.Error("prx.IsSelfAddr(mutated identity) = true, want false")
	}
	if prx.IsServedAddr(foreign) {
		t.Error("prx.IsServedAddr(mutated domain) = true, want false")
	}
	if prx.IsResponsibleAddr(foreign) {
		t.Error("prx.IsResponsibleAddr(mutated address) = true, want false")
	}
}
