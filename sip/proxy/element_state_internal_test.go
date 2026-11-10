package proxy

import (
	"testing"

	"github.com/ghettovoice/gosip/sip"
)

// TestProxy_getElemState pins the allocation contract of the per-element
// processing scopes: a nil element or a lookup without create never
// allocates, a created scope is returned by later lookups and creations,
// and element identity is the pointer alone.
func TestProxy_getElemState(t *testing.T) {
	t.Parallel()

	newScopeProxy := func(tb testing.TB, key []byte) *Proxy {
		tb.Helper()
		prx, err := New([]Route{{Name: "scope"}}, Options{StatelessKey: key})
		if err != nil {
			tb.Fatalf("proxy.New() error = %v, want nil", err)
		}
		return prx
	}

	t.Run("nil element never allocates", func(t *testing.T) {
		t.Parallel()

		prx := newScopeProxy(t, nil)
		if got := prx.getElemState(nil, true); got != nil {
			t.Fatalf("prx.getElemState(nil, true) = %v, want nil", got)
		}
		if got := prx.getElemState(nil, false); got != nil {
			t.Fatalf("prx.getElemState(nil, false) = %v, want nil", got)
		}
		if got := len(prx.elms); got != 0 {
			t.Fatalf("len(prx.elms) = %d, want 0", got)
		}
	})

	t.Run("lookup without create allocates nothing", func(t *testing.T) {
		t.Parallel()

		prx := newScopeProxy(t, nil)
		elm := &sip.Element{}
		if got := prx.getElemState(elm, false); got != nil {
			t.Fatalf("prx.getElemState(elm, false) = %v, want nil for an unseen element", got)
		}
		if got := len(prx.elms); got != 0 {
			t.Fatalf("len(prx.elms) = %d, want 0 (no scope allocated by a lookup)", got)
		}
	})

	t.Run("create installs one scope per pointer", func(t *testing.T) {
		t.Parallel()

		prx := newScopeProxy(t, nil)
		elmA := &sip.Element{InstanceID: sip.NextElementID()}
		// Deliberately equal identity: scope identity is the pointer alone.
		elmB := &sip.Element{InstanceID: elmA.InstanceID}

		esA := prx.getElemState(elmA, true)
		if esA == nil {
			t.Fatal("prx.getElemState(elmA, true) = nil, want a scope")
		}
		if got := prx.getElemState(elmA, false); got != esA {
			t.Fatalf("prx.getElemState(elmA, false) = %v, want the created scope %v", got, esA)
		}
		if got := prx.getElemState(elmA, true); got != esA {
			t.Fatalf("prx.getElemState(elmA, true) = %v, want the same scope %v", got, esA)
		}

		esB := prx.getElemState(elmB, true)
		if esB == nil || esB == esA {
			t.Fatalf("prx.getElemState(elmB, true) = %v, want a distinct scope", esB)
		}
		if got := len(prx.elms); got != 2 {
			t.Fatalf("len(prx.elms) = %d, want 2", got)
		}
	})

	t.Run("fallback key is generated per scope", func(t *testing.T) {
		t.Parallel()

		prx := newScopeProxy(t, nil)
		elmA, elmB := &sip.Element{}, &sip.Element{}
		esA, esB := prx.getElemState(elmA, true), prx.getElemState(elmB, true)
		if len(esA.slFallbackKey) == 0 || len(esB.slFallbackKey) == 0 {
			t.Fatal("keyless proxy scope has no generated fallback key")
		}
		if string(esA.slFallbackKey) == string(esB.slFallbackKey) {
			t.Fatal("element fallback keys collided across scopes")
		}

		keyed := newScopeProxy(t, []byte("configured-key"))
		if got := keyed.getElemState(elmA, true).slFallbackKey; len(got) != 0 {
			t.Fatalf("configured-key scope fallback key = %q, want empty", got)
		}
	})
}

// TestProxy_LookupRequest_scope pins the lookup allocation contract at the
// internal scope-map level: a lookup never creates a scope, and the
// lifecycle end clears every scope without letting a later create
// resurrect one.
func TestProxy_LookupRequest_scope(t *testing.T) {
	t.Parallel()

	prx, err := New([]Route{{Name: "lookup-scope"}})
	if err != nil {
		t.Fatalf("proxy.New() error = %v, want nil", err)
	}

	elm := &sip.Element{}
	key := sip.ServerTransactionKey{Branch: "z9hG4bK.unseen"}

	if rq, ok := prx.LookupRequest(elm, key); rq != nil || ok {
		t.Fatalf("prx.LookupRequest(elm, key) = (%v, %v), want (nil, false)", rq, ok)
	}
	if got := len(prx.elms); got != 0 {
		t.Fatalf("len(prx.elms) = %d, want 0 (a lookup allocates no scope)", got)
	}

	if es := prx.getElemState(elm, true); es == nil {
		t.Fatal("prx.getElemState(elm, true) = nil, want a scope")
	}
	if rq, ok := prx.LookupRequest(elm, key); rq != nil || ok {
		t.Fatalf("prx.LookupRequest(elm, key) = (%v, %v), want (nil, false)", rq, ok)
	}
	if got := len(prx.elms); got != 1 {
		t.Fatalf("len(prx.elms) = %d, want 1 (a lookup keeps the map size)", got)
	}

	if err := prx.Close(); err != nil {
		t.Fatalf("prx.Close() error = %v, want nil", err)
	}
	if got := len(prx.elms); got != 0 {
		t.Fatalf("len(prx.elms) after close = %d, want 0", got)
	}
	if es := prx.getElemState(elm, true); es != nil {
		t.Fatalf("prx.getElemState(elm, true) after close = %v, want nil", es)
	}
	if got := len(prx.elms); got != 0 {
		t.Fatalf("len(prx.elms) after closed create = %d, want 0", got)
	}
}
