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

		rslvr := &Resolver{NameServer: "127.0.0.1:53"}
		if err := rslvr.initIfNeeded(); err != nil {
			t.Fatalf("r.initIfNeeded() error = %v, want nil", err)
		}

		if rslvr.netDlr == nil {
			t.Fatal("r.dialer = nil, want non-nil default dialer")
		}
		if rslvr.netDlr.Timeout != 5*time.Second {
			t.Errorf("r.dialer.Timeout = %v, want %v", rslvr.netDlr.Timeout, 5*time.Second)
		}
		if rslvr.netDlr.KeepAlive != 3*time.Second {
			t.Errorf("r.dialer.KeepAlive = %v, want %v", rslvr.netDlr.KeepAlive, 3*time.Second)
		}
		if rslvr.Dialer != nil {
			t.Errorf("r.Dialer = %v, want unchanged nil", rslvr.Dialer)
		}
		if rslvr.nameSrv != "127.0.0.1:53" || rslvr.netRslvr == nil {
			t.Errorf("r.nameSrv = %q, r.netResolver = %v, want resolved values", rslvr.nameSrv, rslvr.netRslvr)
		}
	})

	t.Run("custom dialer preserved", func(t *testing.T) {
		t.Parallel()

		custom := &net.Dialer{
			Timeout:   time.Second,
			KeepAlive: -1,
			LocalAddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 55333},
		}
		rslvr := &Resolver{NameServer: "127.0.0.1:53", Dialer: custom}
		if err := rslvr.initIfNeeded(); err != nil {
			t.Fatalf("r.initIfNeeded() error = %v, want nil", err)
		}

		if rslvr.netDlr != custom {
			t.Fatalf("r.dialer = %p, want custom dialer %p", rslvr.netDlr, custom)
		}
		if rslvr.Dialer != custom {
			t.Errorf("r.Dialer = %p, want unchanged %p", rslvr.Dialer, custom)
		}
		if rslvr.netDlr.Timeout != time.Second || rslvr.netDlr.KeepAlive != -1 {
			t.Errorf("r.dialer = %+v, want custom settings preserved", rslvr.netDlr)
		}
	})
}
