package proxy

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// TestProxy_InterceptOutboundRequest pins the guard boundary of the
// outbound correlation rewrite: only a send carrying this proxy's ownership
// token whose canonical transaction key resolves to the token's live attempt
// gets the authoritative forward stamp. Every other send delegates with its
// own metadata, options, and sender result untouched.
func TestProxy_InterceptOutboundRequest(t *testing.T) {
	t.Parallel()

	const branch = sip.MagicCookie + ".outbound-guard"

	elm := &sip.Element{}
	otherElm := &sip.Element{}
	route := &Route{Name: "rest", ForwardMode: ForwardModeStateful}
	prx, err := New([]Route{*route})
	if err != nil {
		t.Fatalf("proxy.New() error = %v, want nil", err)
	}
	other, err := New([]Route{*route})
	if err != nil {
		t.Fatalf("proxy.New() error = %v, want nil", err)
	}

	// A pre-existing foreign stamp plus an unrelated application value must
	// survive every guard miss byte-for-byte; the owned hit replaces exactly
	// the proxy stamp.
	foreignMD := Metadata{
		RequestID: "foreign-rq",
		TargetID:  "Z",
		AttemptID: "Z#9",
		RouteName: "other",
		Mode:      ForwardModeStateless,
		Stage:     StageForward,
	}
	seed := func(req *sip.RequestEnvelope) {
		SetMetadata(req.Metadata(), foreignMD)
		req.Metadata().Set("app.keep", "sentinel")
	}

	withMethod := func(req *sip.RequestEnvelope, method sip.RequestMethod) *sip.RequestEnvelope {
		req.WithMessage(func(msg *sip.Request) {
			msg.Method = method
			if cseq, ok := msg.Headers.CSeq(); ok {
				msg.Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: method})
			}
		})
		return req
	}

	req := newInternalInviteReq(branch)
	key, err := sip.MakeClientTransactionKey(req)
	if err != nil {
		t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
	}
	ackReq := withMethod(newInternalInviteReq(branch), sip.RequestMethodAck)
	cncReq := withMethod(newInternalInviteReq(branch), sip.RequestMethodCancel)
	otherKeyReq := newInternalInviteReq(branch + ".other")
	for _, src := range []*sip.RequestEnvelope{req, ackReq, cncReq, otherKeyReq} {
		seed(src)
	}

	newRq := func(owner *Proxy, closed bool) *Request {
		return &Request{
			prx:   owner,
			elm:   elm,
			route: route,
			id:    "rq1",
			attempts: map[sip.ClientTransactionKey]*attempt{
				key: {targetID: "B", attID: "B#1", gen: 3},
			},
			closed: closed,
		}
	}
	ownerCtx := func(ctx context.Context, rq *Request, targetID string, gen uint64) context.Context {
		return ctxWithTxOwner(ctx, &txOwner{rq: rq, targetID: targetID, gen: gen})
	}

	base := sip.ContextWithElement(t.Context(), elm)
	rq := newRq(prx, false)
	srvOwnerCtx := ctxWithTxOwner(base, &txOwner{rq: rq, srv: true})

	tests := []struct {
		name      string
		ctx       context.Context
		req       *sip.RequestEnvelope
		wantStamp bool
	}{
		{"owned attempt", ownerCtx(base, rq, "B", 3), req, true},
		{"absent owner token", base, req, false},
		{"server owner token", srvOwnerCtx, req, false},
		{"foreign proxy", ownerCtx(base, newRq(other, false), "B", 3), req, false},
		{"foreign element", ownerCtx(sip.ContextWithElement(t.Context(), otherElm), rq, "B", 3), req, false},
		{"target mismatch", ownerCtx(base, rq, "C", 3), req, false},
		{"stale generation", ownerCtx(base, rq, "B", 4), req, false},
		{"different transaction key", ownerCtx(base, rq, "B", 3), otherKeyReq, false},
		{"ack same branch", ownerCtx(base, rq, "B", 3), ackReq, false},
		{"cancel same branch", ownerCtx(base, rq, "B", 3), cncReq, false},
		{"closed owner", ownerCtx(base, newRq(prx, true), "B", 3), req, false},
	}
	want := Metadata{
		RequestID: "rq1",
		TargetID:  "B",
		AttemptID: "B#1",
		RouteName: "rest",
		Mode:      ForwardModeStateful,
		Stage:     StageForward,
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotMD Metadata
			var gotOK bool
			var gotApp any
			nexted := 0
			sender := sip.RequestSenderFunc(func(_ context.Context, out *sip.RequestEnvelope, _ ...sip.SendRequestOptions) error {
				nexted++
				gotMD, gotOK = MetadataFrom(out.Metadata())
				gotApp, _ = out.Metadata().Get("app.keep")
				return nil
			})
			sendReq := tt.req.Clone().(*sip.RequestEnvelope)
			if err := prx.InterceptOutboundRequest(tt.ctx, sender, sendReq); err != nil {
				t.Fatalf("prx.InterceptOutboundRequest() error = %v, want nil", err)
			}
			if nexted != 1 {
				t.Fatalf("next calls = %d, want 1", nexted)
			}
			if gotApp != "sentinel" {
				t.Errorf("application metadata = %v, want unchanged %q", gotApp, "sentinel")
			}
			if !gotOK {
				t.Fatal("send carries no proxy metadata, want the seeded foreign stamp preserved")
			}
			if !tt.wantStamp {
				if diff := cmp.Diff(foreignMD, gotMD); diff != "" {
					t.Errorf("delegated send stamp diff (-want foreign +got):\n%s", diff)
				}
				return
			}
			if diff := cmp.Diff(want, gotMD); diff != "" {
				t.Errorf("stamped metadata diff (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("preserves sender options and error", func(t *testing.T) {
		t.Parallel()

		sentinel := errors.ErrorWrap("sender failed")
		opts := []sip.SendRequestOptions{
			{RenderOptions: sip.RenderOptions{Compact: false}},
			{RenderOptions: sip.RenderOptions{Compact: true}},
		}
		var gotOpts []sip.SendRequestOptions
		sender := sip.RequestSenderFunc(func(_ context.Context, _ *sip.RequestEnvelope, o ...sip.SendRequestOptions) error {
			gotOpts = o
			return sentinel
		})
		sendReq := req.Clone().(*sip.RequestEnvelope)
		err := prx.InterceptOutboundRequest(base, sender, sendReq, opts...)
		if !errors.Is(err, sentinel) {
			t.Errorf("prx.InterceptOutboundRequest() error = %v, want errors.Is %v", err, sentinel)
		}
		if diff := cmp.Diff(opts, gotOpts); diff != "" {
			t.Errorf("sender options diff (-want +got):\n%s", diff)
		}
	})
}
