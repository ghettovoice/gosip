package dns

import (
	"net"
	"testing"
	"time"
)

func TestResolver_Init_ResolvedDialer(t *testing.T) {
	t.Parallel()

	t.Run("nil dialer resolves default", func(t *testing.T) {
		t.Parallel()

		r := &Resolver{NameServer: "127.0.0.1:53"}
		if err := r.initIfNeeded(); err != nil {
			t.Fatalf("r.initIfNeeded() error = %v, want nil", err)
		}

		if r.dialer == nil {
			t.Fatal("r.dialer = nil, want non-nil default dialer")
		}
		if r.dialer.Timeout != 5*time.Second {
			t.Errorf("r.dialer.Timeout = %v, want %v", r.dialer.Timeout, 5*time.Second)
		}
		if r.dialer.KeepAlive != 3*time.Second {
			t.Errorf("r.dialer.KeepAlive = %v, want %v", r.dialer.KeepAlive, 3*time.Second)
		}
		if r.Dialer != nil {
			t.Errorf("r.Dialer = %v, want unchanged nil", r.Dialer)
		}
		if r.nameSrv != "127.0.0.1:53" || r.netRslvr == nil {
			t.Errorf("r.nameSrv = %q, r.netRslvr = %v, want resolved values", r.nameSrv, r.netRslvr)
		}
	})

	t.Run("custom dialer preserved", func(t *testing.T) {
		t.Parallel()

		custom := &net.Dialer{
			Timeout:   time.Second,
			KeepAlive: -1,
			LocalAddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 55333},
		}
		r := &Resolver{NameServer: "127.0.0.1:53", Dialer: custom}
		if err := r.initIfNeeded(); err != nil {
			t.Fatalf("r.initIfNeeded() error = %v, want nil", err)
		}

		if r.dialer != custom {
			t.Fatalf("r.dialer = %p, want custom dialer %p", r.dialer, custom)
		}
		if r.Dialer != custom {
			t.Errorf("r.Dialer = %p, want unchanged %p", r.Dialer, custom)
		}
		if r.dialer.Timeout != time.Second || r.dialer.KeepAlive != -1 {
			t.Errorf("r.dialer = %+v, want custom settings preserved", r.dialer)
		}
	})
}
