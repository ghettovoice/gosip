package useragent_test

import (
	"context"
	"io"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/useragent"
)

var (
	_ sip.InboundRequestInterceptor = (*useragent.UserAgent)(nil)
	_ io.Closer                     = (*useragent.UserAgent)(nil)
)

// capsStub is a component stub advertising a fixed method set.
type capsStub struct {
	methods []sip.RequestMethod
}

func (c *capsStub) Capabilities() sip.ElementCapabilities {
	return sip.ElementCapabilities{Methods: c.methods}
}

// noopReqHandler is the shared inbound handler stub: it is never
// invoked by the wiring tests.
func noopReqHandler(context.Context, *useragent.IncomingMessage) error { return nil }

// testAOR parses the shared address of record used across the tests.
func testAOR(tb testing.TB) *sip.URI {
	tb.Helper()

	aor, err := sip.ParseURI("sip:alice@example.com")
	if err != nil {
		tb.Fatalf("sip.ParseURI() error = %v, want nil", err)
	}
	return aor
}

func TestUserAgent_Capabilities(t *testing.T) {
	t.Parallel()

	aor := testAOR(t)

	tests := []struct {
		name string
		opts useragent.Options
		want []sip.RequestMethod
	}{
		{
			name: "default options",
			opts: useragent.Options{AOR: aor},
			want: []sip.RequestMethod{sip.RequestMethodOptions},
		},
		{
			name: "message handler",
			opts: useragent.Options{AOR: aor, OnMessage: noopReqHandler},
			want: []sip.RequestMethod{sip.RequestMethodMessage, sip.RequestMethodOptions},
		},
		{
			name: "message handler without options auto answer",
			opts: useragent.Options{
				AOR:                      aor,
				OnMessage:                noopReqHandler,
				DisableOptionsAutoAnswer: true,
			},
			want: []sip.RequestMethod{sip.RequestMethodMessage},
		},
		{
			name: "options auto answer disabled",
			opts: useragent.Options{AOR: aor, DisableOptionsAutoAnswer: true},
			want: nil,
		},
		{
			name: "options handler without auto answer",
			opts: useragent.Options{
				AOR:                      aor,
				OnOptions:                noopReqHandler,
				DisableOptionsAutoAnswer: true,
			},
			want: []sip.RequestMethod{sip.RequestMethodOptions},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ua, err := useragent.New(tt.opts)
			if err != nil {
				t.Fatalf("useragent.New() error = %v, want nil", err)
			}
			caps := ua.Capabilities()
			if diff := cmp.Diff(tt.want, caps.Methods); diff != "" {
				t.Fatalf("ua.Capabilities().Methods mismatch (-want +got):\n%s", diff)
			}
		})
	}

	// Each call builds a fresh method list: mutating a returned
	// advertisement never leaks into the next call.
	ua, err := useragent.New(useragent.Options{AOR: aor, OnMessage: noopReqHandler})
	if err != nil {
		t.Fatalf("useragent.New() error = %v, want nil", err)
	}
	caps := ua.Capabilities()
	caps.Methods[0] = sip.RequestMethodInvite
	if diff := cmp.Diff(
		[]sip.RequestMethod{sip.RequestMethodMessage, sip.RequestMethodOptions},
		ua.Capabilities().Methods,
	); diff != "" {
		t.Fatalf("ua.Capabilities().Methods after mutation mismatch (-want +got):\n%s", diff)
	}

	// An instance that never passed New advertises nothing, even though
	// the options auto answer is not disabled.
	var zero useragent.UserAgent
	if got := zero.Capabilities().Methods; len(got) != 0 {
		t.Fatalf("zero.Capabilities().Methods = %v, want empty", got)
	}
}

func TestUserAgent_Capabilities_ElementAggregation(t *testing.T) {
	t.Parallel()

	ua, err := useragent.New(useragent.Options{
		AOR:       testAOR(t),
		OnMessage: noopReqHandler,
	})
	if err != nil {
		t.Fatalf("useragent.New() error = %v, want nil", err)
	}

	elm := &sip.Element{}
	t.Cleanup(func() { elm.Close() })
	elm.UseComponent(ua)
	elm.UseComponent(&capsStub{methods: []sip.RequestMethod{sip.RequestMethodInvite}})

	// The element aggregates the advertised methods of every component.
	caps := elm.Capabilities()
	for _, method := range []sip.RequestMethod{
		sip.RequestMethodMessage,
		sip.RequestMethodOptions,
		sip.RequestMethodInvite,
	} {
		if !caps.SupportsMethod(method) {
			t.Errorf("elm.Capabilities() does not support %s, want supported", method)
		}
	}

	// The user agent still reports only its own advertisement.
	if diff := cmp.Diff(
		[]sip.RequestMethod{sip.RequestMethodMessage, sip.RequestMethodOptions},
		ua.Capabilities().Methods,
	); diff != "" {
		t.Fatalf("ua.Capabilities().Methods mismatch (-want +got):\n%s", diff)
	}
}

func TestUserAgent_Lifecycle(t *testing.T) {
	t.Parallel()

	ua, err := useragent.New(useragent.Options{AOR: testAOR(t)})
	if err != nil {
		t.Fatalf("useragent.New() error = %v, want nil", err)
	}

	elm := &sip.Element{}
	t.Cleanup(func() { elm.Close() })
	if unbind := elm.UseComponent(ua); unbind == nil {
		t.Fatal("elm.UseComponent() = nil unbind, want non-nil")
	}

	if err := ua.Shutdown(t.Context()); err != nil {
		t.Fatalf("ua.Shutdown() error = %v, want nil", err)
	}
	if err := ua.Close(); err != nil {
		t.Fatalf("ua.Close() error = %v, want nil", err)
	}
	if err := ua.Close(); err != nil {
		t.Fatalf("second ua.Close() error = %v, want nil", err)
	}

	// The borrowed element is never closed by the user agent: it still
	// admits components and resolves capabilities.
	if unbind := elm.UseComponent(&capsStub{
		methods: []sip.RequestMethod{sip.RequestMethodInvite},
	}); unbind == nil {
		t.Fatal("elm.UseComponent() after ua.Close() = nil unbind, want non-nil")
	}
	if caps := elm.Capabilities(); !caps.SupportsMethod(sip.RequestMethodInvite) {
		t.Error("elm.Capabilities() does not support INVITE after ua.Close(), want supported")
	}
}
