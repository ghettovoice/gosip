package redirector_test

import (
	"context"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/redirector"
)

// testContacts is the fixed contact set returned by the stub resolvers.
var testContacts = header.Contact{{
	URI: &sip.URI{
		User: sip.MakeUserInfo("uas"),
		Addr: sip.MakeHostAddr("uas.test"),
	},
}}

// newStubRedirector builds a redirector claiming every request through a
// single route whose resolver counts its calls through resolveCalls.
func newStubRedirector(tb testing.TB, resolveCalls *atomic.Int32) *redirector.Redirector {
	tb.Helper()

	rdr, err := redirector.New([]redirector.Route{{
		Name: "stub",
		ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
			resolveCalls.Add(1)
			return testContacts, nil
		},
	}})
	if err != nil {
		tb.Fatalf("redirector.New() error = %v, want nil", err)
	}
	tb.Cleanup(func() { rdr.Close() })
	return rdr
}

// countingNext returns a pass-through request receiver counting its calls
// through nextCalls.
func countingNext(nextCalls *atomic.Int32) sip.RequestReceiver {
	return sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
		nextCalls.Add(1)
		return nil
	})
}

// newTestReq builds a minimal typed inbound request envelope for dispatch
// tests: only the method and the loopback addressing are meaningful.
func newTestReq(method sip.RequestMethod) *sip.RequestEnvelope {
	return sip.NewRequestEnvelope(&sip.Request{
		Proto:  sip.ProtoVer20(),
		Method: method,
		URI: &sip.URI{
			User: sip.MakeUserInfo("alice"),
			Addr: sip.MakeHostAddr("voip.test"),
		},
	}).
		SetTransport(sip.UDPMetadata()).
		SetRemoteAddr(netip.MustParseAddrPort("127.0.0.1:5060")).
		SetLocalAddr(netip.MustParseAddrPort("127.0.0.1:5061"))
}

func TestRedirector_Claiming(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		method      sip.RequestMethod
		matched     bool
		closed      bool
		wantNext    int32
		wantResolve int32
		wantErr     error
	}{
		{
			name:     "closed passes through",
			method:   sip.RequestMethodInvite,
			matched:  true,
			closed:   true,
			wantNext: 1,
		},
		{
			name:     "no route matches",
			method:   sip.RequestMethodInvite,
			wantNext: 1,
		},
		{
			name:     "matched ACK passes through",
			method:   sip.RequestMethodAck,
			matched:  true,
			wantNext: 1,
		},
		{
			name:    "matched CANCEL missing element",
			method:  sip.RequestMethodCancel,
			matched: true,
			wantErr: redirector.ErrMissingElement,
		},
		{
			name:        "matched custom method missing element",
			method:      sip.RequestMethodMessage,
			matched:     true,
			wantResolve: 1,
			wantErr:     redirector.ErrMissingElement,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				nextCalls    atomic.Int32
				resolveCalls atomic.Int32
			)
			next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
				nextCalls.Add(1)
				return nil
			})

			route := redirector.Route{
				Name: "test",
				ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
					resolveCalls.Add(1)
					return testContacts, nil
				},
			}
			if !tt.matched {
				route.MatchRequest = func(context.Context, *sip.RequestEnvelope) bool { return false }
			}

			rdr, err := redirector.New([]redirector.Route{route})
			if err != nil {
				t.Fatalf("redirector.New() error = %v, want nil", err)
			}
			t.Cleanup(func() { rdr.Close() })
			if tt.closed {
				if err := rdr.Close(); err != nil {
					t.Fatalf("rdr.Close() error = %v, want nil", err)
				}
			}

			err = rdr.InterceptInboundRequest(t.Context(), next, newTestReq(tt.method))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("rdr.InterceptInboundRequest() error = %v, want %v", err, tt.wantErr)
				}
				if !errors.Is(err, sip.ErrClassElement) {
					t.Fatalf("rdr.InterceptInboundRequest() error = %v, want classified as %v", err, sip.ErrClassElement)
				}
			} else if err != nil {
				t.Fatalf("rdr.InterceptInboundRequest() error = %v, want nil", err)
			}
			if got := nextCalls.Load(); got != tt.wantNext {
				t.Errorf("next calls = %d, want %d", got, tt.wantNext)
			}
			if got := resolveCalls.Load(); got != tt.wantResolve {
				t.Errorf("resolver calls = %d, want %d", got, tt.wantResolve)
			}
		})
	}

	t.Run("configuration copied", func(t *testing.T) {
		t.Parallel()

		var (
			nextCalls    atomic.Int32
			resolveCalls atomic.Int32
		)
		next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
			nextCalls.Add(1)
			return nil
		})

		routes := make([]redirector.Route, 1, 2)
		routes[0] = redirector.Route{
			Name: "first",
			MatchRequest: func(context.Context, *sip.RequestEnvelope) bool {
				return true
			},
			ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
				resolveCalls.Add(1)
				return testContacts, nil
			},
		}
		rdr, err := redirector.New(routes)
		if err != nil {
			t.Fatalf("redirector.New() error = %v, want nil", err)
		}
		t.Cleanup(func() { rdr.Close() })

		// Mutate the source slice after construction: replace the original
		// matcher and append another route. A copied configuration keeps the
		// original match, so the request is still claimed by the first route.
		routes[0].MatchRequest = func(context.Context, *sip.RequestEnvelope) bool { return false }
		routes = append(routes, redirector.Route{
			Name:         "appended",
			MatchRequest: func(context.Context, *sip.RequestEnvelope) bool { return false },
			ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
				resolveCalls.Add(1)
				return testContacts, nil
			},
		})
		if len(routes) != 2 {
			t.Fatalf("source routes len = %d, want 2", len(routes))
		}

		err = rdr.InterceptInboundRequest(t.Context(), next, newTestReq(sip.RequestMethodMessage))
		if !errors.Is(err, redirector.ErrMissingElement) {
			t.Fatalf("rdr.InterceptInboundRequest() error = %v, want %v", err, redirector.ErrMissingElement)
		}
		if !errors.Is(err, sip.ErrClassElement) {
			t.Fatalf("rdr.InterceptInboundRequest() error = %v, want classified as %v", err, sip.ErrClassElement)
		}
		if got := resolveCalls.Load(); got != 1 {
			t.Fatalf("resolver calls = %d, want 1", got)
		}
		if got := nextCalls.Load(); got != 0 {
			t.Fatalf("next calls = %d, want 0", got)
		}
	})
}

func TestRedirector_Close(t *testing.T) {
	t.Parallel()

	t.Run("sealed claim", func(t *testing.T) {
		t.Parallel()

		var (
			nextCalls    atomic.Int32
			resolveCalls atomic.Int32
		)
		next := countingNext(&nextCalls)
		rdr := newStubRedirector(t, &resolveCalls)

		// A matched request before Close is claimed: the resolver runs and
		// next is skipped.
		err := rdr.InterceptInboundRequest(t.Context(), next, newTestReq(sip.RequestMethodInvite))
		if !errors.Is(err, redirector.ErrMissingElement) {
			t.Fatalf("rdr.InterceptInboundRequest() error = %v, want %v", err, redirector.ErrMissingElement)
		}
		if got := resolveCalls.Load(); got != 1 {
			t.Fatalf("resolver calls = %d, want 1", got)
		}
		if got := nextCalls.Load(); got != 0 {
			t.Fatalf("next calls = %d, want 0", got)
		}

		if err := rdr.Close(); err != nil {
			t.Fatalf("rdr.Close() error = %v, want nil", err)
		}

		// A matched request after Close is not claimed: it passes through
		// to next and never reaches the resolver.
		err = rdr.InterceptInboundRequest(t.Context(), next, newTestReq(sip.RequestMethodInvite))
		if err != nil {
			t.Fatalf("rdr.InterceptInboundRequest() error = %v, want nil", err)
		}
		if got := nextCalls.Load(); got != 1 {
			t.Errorf("next calls = %d, want 1", got)
		}
		if got := resolveCalls.Load(); got != 1 {
			t.Errorf("resolver calls = %d, want 1", got)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		t.Parallel()

		var resolveCalls atomic.Int32
		rdr := newStubRedirector(t, &resolveCalls)

		if err := rdr.Close(); err != nil {
			t.Fatalf("rdr.Close() error = %v, want nil", err)
		}
		if err := rdr.Close(); err != nil {
			t.Fatalf("second rdr.Close() error = %v, want nil", err)
		}
	})

	t.Run("concurrent close", func(t *testing.T) {
		t.Parallel()

		var (
			nextCalls    atomic.Int32
			resolveCalls atomic.Int32
			claimed      atomic.Int32
			passed       atomic.Int32
			unexpected   atomic.Int32
			closeErrs    atomic.Int32
		)
		next := countingNext(&nextCalls)
		rdr := newStubRedirector(t, &resolveCalls)

		ctx := t.Context()
		const closers, dispatchers = 4, 32
		var wg sync.WaitGroup
		for range closers {
			wg.Go(func() {
				if err := rdr.Close(); err != nil {
					closeErrs.Add(1)
				}
			})
		}
		for range dispatchers {
			wg.Go(func() {
				// Every dispatch either claims the request — the resolver
				// runs and the stub dispatch fails on the element missing
				// from the context — or passes it through to next once
				// the close seal lands.
				err := rdr.InterceptInboundRequest(ctx, next, newTestReq(sip.RequestMethodInvite))
				switch {
				case err == nil:
					passed.Add(1)
				case errors.Is(err, redirector.ErrMissingElement):
					claimed.Add(1)
				default:
					unexpected.Add(1)
				}
			})
		}
		wg.Wait()

		if got := closeErrs.Load(); got != 0 {
			t.Errorf("rdr.Close() errors = %d, want 0", got)
		}
		if got := unexpected.Load(); got != 0 {
			t.Errorf("unexpected dispatch errors = %d, want 0", got)
		}
		if got := claimed.Load() + passed.Load(); got != dispatchers {
			t.Fatalf("dispatch outcomes = %d, want %d", got, dispatchers)
		}
		if got := nextCalls.Load(); got != passed.Load() {
			t.Errorf("next calls = %d, want %d, one per pass-through", got, passed.Load())
		}
		if got := resolveCalls.Load(); got != claimed.Load() {
			t.Errorf("resolver calls = %d, want %d, one per claim", got, claimed.Load())
		}
	})
}
