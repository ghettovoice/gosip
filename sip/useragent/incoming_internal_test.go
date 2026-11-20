package useragent

import (
	"context"
	"net/netip"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
)

// stubSendFailTransp is a sip.ServerTransport whose SendResponse fails
// with a scripted error until nil is scripted.
type stubSendFailTransp struct {
	err error
}

func (tp *stubSendFailTransp) Metadata() sip.TransportMetadata { return sip.UDPMetadata() }

func (tp *stubSendFailTransp) SendResponse(
	context.Context,
	*sip.ResponseEnvelope,
	...sip.SendResponseOptions,
) error {
	return errors.Wrap(tp.err)
}

// newInboundReq builds a minimal valid inbound request envelope for the
// internal dispatch and responder tests.
func newInboundReq(tb testing.TB) *sip.RequestEnvelope {
	tb.Helper()

	target, err := sip.ParseURI("sip:alice@example.com")
	if err != nil {
		tb.Fatalf("sip.ParseURI() error = %v, want nil", err)
	}
	caller, err := sip.ParseURI("sip:bob@example.com")
	if err != nil {
		tb.Fatalf("sip.ParseURI() error = %v, want nil", err)
	}
	req, err := sip.NewRequest(sip.RequestMethodMessage, target, caller, target)
	if err != nil {
		tb.Fatalf("sip.NewRequest() error = %v, want nil", err)
	}
	return sip.NewRequestEnvelope(req).
		SetTransport(sip.UDPMetadata()).
		SetRemoteAddr(netip.MustParseAddrPort("127.0.0.1:5060")).
		SetLocalAddr(netip.MustParseAddrPort("127.0.0.1:5061"))
}

func TestIncomingMessage_Respond_SendFailureKeepsGateOpen(t *testing.T) {
	t.Parallel()

	ua := newLifecycleTestUserAgent(t)
	elm := &sip.Element{}
	t.Cleanup(func() { elm.Close() })

	sendErr := errors.New("stub send failure")
	tp := &stubSendFailTransp{err: sendErr}

	req := newInboundReq(t)
	tx, err := elm.NewServerTransaction(t.Context(), req, tp, sip.ServerTransactionOptions{})
	if err != nil {
		t.Fatalf("elm.NewServerTransaction() error = %v, want nil", err)
	}
	if err := tx.Start(sip.ContextWithTransaction(t.Context(), tx)); err != nil {
		t.Fatalf("tx.Start() error = %v, want nil", err)
	}

	in := &IncomingMessage{ua: ua, elm: elm, req: req}
	in.ctx, in.cancel = context.WithCancel(t.Context())
	release, admitted := ua.trackInbound(in)
	if !admitted {
		t.Fatal("ua.trackInbound() admitted = false, want true")
	}
	t.Cleanup(ua.workers.Done)
	unbindState := tx.BindStateHandler(sip.TransactionStateHandlerFunc(
		func(_ context.Context, _, to sip.TransactionState) {
			if to == sip.TransactionStateTerminated {
				release()
			}
		},
	))
	unbindErr := tx.BindErrorHandler(sip.ErrorHandlerFunc(func(_ context.Context, err error) {
		in.sendErr.Store(&err)
	}))
	in.setUnbind(func() {
		unbindState()
		unbindErr()
	})

	// A failed send returns the transport error without committing the
	// final gate.
	if err := in.Respond(t.Context(), sip.ResponseStatusOK); !errors.Is(err, sendErr) {
		t.Fatalf("in.Respond() error = %v, want %v", err, sendErr)
	}
	if in.final {
		t.Error("in.final = true after failed send, want false")
	}

	// A subsequent Respond is still allowed: the transaction itself
	// already entered Completed on the first final event (RFC 6026 keeps
	// it alive to absorb retransmissions), so the retry is absorbed by
	// the FSM, reports success, and commits the responder's gate to
	// match the transaction's own state.
	if err := in.Respond(t.Context(), sip.ResponseStatusOK); err != nil {
		t.Fatalf("in.Respond() retry error = %v, want nil", err)
	}
	if !in.final {
		t.Error("in.final = false after absorbed retry, want true")
	}
	if err := in.Respond(t.Context(), sip.ResponseStatusOK); !errors.Is(err, ErrResponseFinal) {
		t.Errorf("in.Respond() after committed final error = %v, want %v", err, ErrResponseFinal)
	}
}

// TestUserAgent_SealReleasesRetainedInbound proves the seal releases a
// retained inbound handle — unbind observation, cancel the inbound scope,
// and unregister it — not just cancels its context.
func TestUserAgent_SealReleasesRetainedInbound(t *testing.T) {
	t.Parallel()

	stops := map[string]func(*UserAgent) func(context.Context) error{
		"Shutdown": func(ua *UserAgent) func(context.Context) error {
			return ua.Shutdown
		},
		"Close": func(ua *UserAgent) func(context.Context) error {
			return func(context.Context) error { return ua.Close() }
		},
	}
	for name, stopOf := range stops {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ua := newLifecycleTestUserAgent(t)
			in := &IncomingMessage{ua: ua}
			in.ctx, in.cancel = context.WithCancel(t.Context())
			_, admitted := ua.trackInbound(in)
			if !admitted {
				t.Fatal("ua.trackInbound() admitted = false, want true")
			}
			// A retained responder: the handler already returned, so its
			// worker slot is gone and only the registration survives.
			ua.workers.Done()
			unbound := 0
			ua.mu.Lock()
			in.unbind = func() { unbound++ }
			ua.mu.Unlock()

			stop := stopOf(ua)
			for i := range 2 {
				if err := stop(t.Context()); err != nil {
					t.Fatalf("stop call %d error = %v, want nil", i+1, err)
				}
				if !errors.Is(in.ctx.Err(), context.Canceled) {
					t.Fatalf("in.ctx.Err() after stop %d = %v, want context.Canceled", i+1, in.ctx.Err())
				}
				ua.mu.Lock()
				registered := len(ua.inbounds)
				ua.mu.Unlock()
				if registered != 0 {
					t.Fatalf("len(ua.inbounds) after stop %d = %d, want 0", i+1, registered)
				}
				if unbound != 1 {
					t.Fatalf("unbind calls after stop %d = %d, want 1", i+1, unbound)
				}
			}
		})
	}
}

// TestIncomingMessage_ReleasesLateUnbind proves a dispatch's unbind
// landing after the seal already released the handle still detaches the
// transaction observation instead of being dropped.
func TestIncomingMessage_ReleasesLateUnbind(t *testing.T) {
	t.Parallel()

	ua := newLifecycleTestUserAgent(t)
	in := &IncomingMessage{ua: ua}
	in.ctx, in.cancel = context.WithCancel(t.Context())
	_, admitted := ua.trackInbound(in)
	if !admitted {
		t.Fatal("ua.trackInbound() admitted = false, want true")
	}
	t.Cleanup(ua.workers.Done)

	// Seal while the handle is admitted but its unbind is not yet
	// published: release runs with no unbind bound.
	if err := ua.Close(); err != nil {
		t.Fatalf("ua.Close() error = %v, want nil", err)
	}
	unbound := 0
	in.setUnbind(func() { unbound++ })
	in.release()

	if unbound != 1 {
		t.Fatalf("unbind calls = %d, want 1", unbound)
	}
	ua.mu.Lock()
	remaining := in.unbind
	ua.mu.Unlock()
	if remaining != nil {
		t.Error("in.unbind != nil after release, want nil")
	}
	if !errors.Is(in.ctx.Err(), context.Canceled) {
		t.Fatalf("in.ctx.Err() = %v, want context.Canceled", in.ctx.Err())
	}
}
