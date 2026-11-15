package sip_test

import (
	"context"
	stderrs "errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

var errSenderFailed = stderrs.New("sender failed")

type ctxMarkerKey struct{}

func newIdentityReqEnvelope() *sip.RequestEnvelope {
	return sip.NewRequestEnvelope(&sip.Request{
		Method: sip.RequestMethodInvite,
		URI: &sip.URI{
			User: sip.MakeUserInfo("bob"),
			Addr: sip.MakeHostAddr("b.example.com"),
		},
		Proto:   sip.ProtoVer20(),
		Headers: make(sip.Headers),
	})
}

func newIdentityResEnvelope() *sip.ResponseEnvelope {
	return sip.NewResponseEnvelope(&sip.Response{
		Status:  sip.ResponseStatusOK,
		Proto:   sip.ProtoVer20(),
		Headers: make(sip.Headers),
	})
}

func TestSoftwareIdentity_InterceptOutboundRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		identity  sip.SoftwareIdentity
		hdr       sip.Header
		forwarded bool
		want      []sip.Header
	}{
		{name: "unset field adds nothing", want: nil},
		{
			name:     "configured user agent appended",
			identity: sip.SoftwareIdentity{UserAgent: "gosip-test/1.0"},
			want:     []sip.Header{header.UserAgent("gosip-test/1.0")},
		},
		{
			name:     "existing typed header untouched",
			identity: sip.SoftwareIdentity{UserAgent: "gosip-test/1.0"},
			hdr:      header.UserAgent("other/2.0"),
			want:     []sip.Header{header.UserAgent("other/2.0")},
		},
		{
			name:     "existing generic header untouched",
			identity: sip.SoftwareIdentity{UserAgent: "gosip-test/1.0"},
			hdr:      &header.Any{Name: "User-Agent", Value: "other/2.0"},
			want:     []sip.Header{&header.Any{Name: "User-Agent", Value: "other/2.0"}},
		},
		{
			name:     "existing case-varied generic header untouched",
			identity: sip.SoftwareIdentity{UserAgent: "gosip-test/1.0"},
			hdr:      &header.Any{Name: "user-agent", Value: "other/2.0"},
			want:     []sip.Header{&header.Any{Name: "user-agent", Value: "other/2.0"}},
		},
		{
			name:     "existing empty header untouched",
			identity: sip.SoftwareIdentity{UserAgent: "gosip-test/1.0"},
			hdr:      header.UserAgent(""),
			want:     []sip.Header{header.UserAgent("")},
		},
		{
			name:      "forwarded request untouched",
			identity:  sip.SoftwareIdentity{UserAgent: "gosip-test/1.0"},
			forwarded: true,
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newIdentityReqEnvelope()
			if tt.hdr != nil {
				env.WithMessage(func(r *sip.Request) { r.Headers.Append(tt.hdr) })
			}
			if tt.forwarded {
				env.MarkForwarded()
			}

			var (
				nextCalled bool
				nextOpts   []sip.SendRequestOptions
			)
			next := sip.RequestSenderFunc(func(ctx context.Context, _ *sip.RequestEnvelope, opts ...sip.SendRequestOptions) error {
				nextCalled = true
				if got := ctx.Value(ctxMarkerKey{}); got != "marker" {
					t.Errorf("next ctx marker = %v, want %q", got, "marker")
				}
				nextOpts = opts
				return nil
			})

			ctx := context.WithValue(t.Context(), ctxMarkerKey{}, "marker")
			opts := []sip.SendRequestOptions{{LookupOptions: sip.LookupMessageAddrsOptions{StableDNSRecordsOrder: true}}}
			err := tt.identity.InterceptOutboundRequest(ctx, next, env, opts...)
			if err != nil {
				t.Fatalf("InterceptOutboundRequest() error = %v, want nil", err)
			}
			if !nextCalled {
				t.Fatal("InterceptOutboundRequest() did not call next, want call")
			}
			if diff := cmp.Diff(opts, nextOpts); diff != "" {
				t.Errorf("next options mismatch (-want +got):\n%s", diff)
			}

			var got []sip.Header
			env.WithMessage(func(r *sip.Request) { got = r.Headers.Get("User-Agent") })
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(header.Any{})); diff != "" {
				t.Errorf("User-Agent headers mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSoftwareIdentity_InterceptOutboundRequest_SenderError(t *testing.T) {
	t.Parallel()

	env := newIdentityReqEnvelope()
	id := sip.SoftwareIdentity{UserAgent: "gosip-test/1.0"}
	next := sip.RequestSenderFunc(func(context.Context, *sip.RequestEnvelope, ...sip.SendRequestOptions) error {
		return errSenderFailed
	})

	err := id.InterceptOutboundRequest(t.Context(), next, env)
	if !stderrs.Is(err, errSenderFailed) {
		t.Fatalf("InterceptOutboundRequest() error = %v, want sender error", err)
	}
}

func TestSoftwareIdentity_InterceptOutboundResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		identity  sip.SoftwareIdentity
		hdr       sip.Header
		forwarded bool
		want      []sip.Header
	}{
		{name: "unset field adds nothing", want: nil},
		{
			name:     "configured server appended",
			identity: sip.SoftwareIdentity{Server: "gosip-test/1.0"},
			want:     []sip.Header{header.Server("gosip-test/1.0")},
		},
		{
			name:     "existing typed header untouched",
			identity: sip.SoftwareIdentity{Server: "gosip-test/1.0"},
			hdr:      header.Server("other/2.0"),
			want:     []sip.Header{header.Server("other/2.0")},
		},
		{
			name:     "existing generic header untouched",
			identity: sip.SoftwareIdentity{Server: "gosip-test/1.0"},
			hdr:      &header.Any{Name: "Server", Value: "other/2.0"},
			want:     []sip.Header{&header.Any{Name: "Server", Value: "other/2.0"}},
		},
		{
			name:     "existing case-varied generic header untouched",
			identity: sip.SoftwareIdentity{Server: "gosip-test/1.0"},
			hdr:      &header.Any{Name: "server", Value: "other/2.0"},
			want:     []sip.Header{&header.Any{Name: "server", Value: "other/2.0"}},
		},
		{
			name:     "existing empty header untouched",
			identity: sip.SoftwareIdentity{Server: "gosip-test/1.0"},
			hdr:      header.Server(""),
			want:     []sip.Header{header.Server("")},
		},
		{
			name:      "forwarded response untouched",
			identity:  sip.SoftwareIdentity{Server: "gosip-test/1.0"},
			forwarded: true,
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newIdentityResEnvelope()
			if tt.hdr != nil {
				env.WithMessage(func(r *sip.Response) { r.Headers.Append(tt.hdr) })
			}
			if tt.forwarded {
				env.MarkForwarded()
			}

			var (
				nextCalled bool
				nextOpts   []sip.SendResponseOptions
			)
			next := sip.ResponseSenderFunc(func(ctx context.Context, _ *sip.ResponseEnvelope, opts ...sip.SendResponseOptions) error {
				nextCalled = true
				if got := ctx.Value(ctxMarkerKey{}); got != "marker" {
					t.Errorf("next ctx marker = %v, want %q", got, "marker")
				}
				nextOpts = opts
				return nil
			})

			ctx := context.WithValue(t.Context(), ctxMarkerKey{}, "marker")
			opts := []sip.SendResponseOptions{{LookupOptions: sip.LookupMessageAddrsOptions{StableDNSRecordsOrder: true}}}
			err := tt.identity.InterceptOutboundResponse(ctx, next, env, opts...)
			if err != nil {
				t.Fatalf("InterceptOutboundResponse() error = %v, want nil", err)
			}
			if !nextCalled {
				t.Fatal("InterceptOutboundResponse() did not call next, want call")
			}
			if diff := cmp.Diff(opts, nextOpts); diff != "" {
				t.Errorf("next options mismatch (-want +got):\n%s", diff)
			}

			var got []sip.Header
			env.WithMessage(func(r *sip.Response) { got = r.Headers.Get("Server") })
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(header.Any{})); diff != "" {
				t.Errorf("Server headers mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSoftwareIdentity_InterceptOutboundResponse_SenderError(t *testing.T) {
	t.Parallel()

	env := newIdentityResEnvelope()
	id := sip.SoftwareIdentity{Server: "gosip-test/1.0"}
	next := sip.ResponseSenderFunc(func(context.Context, *sip.ResponseEnvelope, ...sip.SendResponseOptions) error {
		return errSenderFailed
	})

	err := id.InterceptOutboundResponse(t.Context(), next, env)
	if !stderrs.Is(err, errSenderFailed) {
		t.Fatalf("InterceptOutboundResponse() error = %v, want sender error", err)
	}
}

func ExampleSoftwareIdentity() {
	elm := &sip.Element{}
	_ = elm.UseComponent(sip.SoftwareIdentity{
		UserAgent: "example-agent/1.0",
		Server:    "example-server/1.0",
	})
	// Output:
}
