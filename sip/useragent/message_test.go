package useragent_test

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/useragent"
	"github.com/ghettovoice/gosip/uri"
)

// msgAOR parses the sender address of record used by the MESSAGE tests.
func msgAOR(tb testing.TB) *sip.URI {
	tb.Helper()

	aor, err := sip.ParseURI("sip:alice@ua.test")
	if err != nil {
		tb.Fatalf("sip.ParseURI() error = %v, want nil", err)
	}
	return aor
}

// msgPeerAddr returns the loopback address a raw peer listens on.
func msgPeerAddr(tb testing.TB, peer *net.UDPConn) netip.AddrPort {
	tb.Helper()

	udpAddr, ok := peer.LocalAddr().(*net.UDPAddr)
	if !ok {
		tb.Fatalf("peer.LocalAddr() = %T, want *net.UDPAddr", peer.LocalAddr())
	}
	return udpAddr.AddrPort()
}

// sendPeerRes renders res and writes it from the raw peer to dst, the
// request's datagram source captured by readReq.
func sendPeerRes(tb testing.TB, peer *net.UDPConn, dst netip.AddrPort, res *sip.ResponseEnvelope) {
	tb.Helper()

	if _, err := peer.WriteToUDPAddrPort([]byte(shared.Render(res.Message())), dst); err != nil {
		tb.Fatalf("useragent: send response: %v", err)
	}
}

// replyPeerRes answers a captured raw-peer request with the given status.
func replyPeerRes(
	tb testing.TB,
	peer *net.UDPConn,
	captured *sip.RequestEnvelope,
	src netip.AddrPort,
	sts sip.ResponseStatus,
) {
	tb.Helper()

	res, err := captured.NewResponse(sts)
	if err != nil {
		tb.Fatalf("captured.NewResponse(%v) error = %v, want nil", sts, err)
	}
	sendPeerRes(tb, peer, src, res)
}

// msgSenderFixture bundles a loopback element and an unregistered sender
// user agent.
type msgSenderFixture struct {
	*wireElement
	ua  *useragent.UserAgent
	aor *sip.URI
}

// newMsgSender builds a wire element and a sender user agent configured
// with an address of record and a contact. The configured contact proves
// that a MESSAGE request never carries one (RFC 3428), and the agent is
// deliberately left unregistered: outbound operations take their element
// binding from the operation context, not from component registration.
func newMsgSender(tb testing.TB) *msgSenderFixture {
	tb.Helper()

	contact, err := sip.ParseURI("sip:alice@pc.ua.test")
	if err != nil {
		tb.Fatalf("sip.ParseURI() error = %v, want nil", err)
	}
	aor := msgAOR(tb)
	ua, err := useragent.New(useragent.Options{AOR: aor, Contact: contact})
	if err != nil {
		tb.Fatalf("useragent.New() error = %v, want nil", err)
	}
	return &msgSenderFixture{wireElement: newWireElem(tb), ua: ua, aor: aor}
}

func TestUserAgent_Message(t *testing.T) {
	t.Parallel()

	t.Run("user agent to user agent", func(t *testing.T) {
		t.Parallel()

		fxA := newMsgSender(t)
		wB := newWireElem(t)
		bob, err := sip.ParseURI("sip:bob@ua.test")
		if err != nil {
			t.Fatalf("sip.ParseURI() error = %v, want nil", err)
		}
		uaB, err := useragent.New(useragent.Options{
			AOR: bob,
			OnMessage: func(ctx context.Context, in *useragent.IncomingMessage) error {
				return in.Respond(ctx, sip.ResponseStatusOK)
			},
		})
		if err != nil {
			t.Fatalf("useragent.New() error = %v, want nil", err)
		}
		if unbind := wB.elm.UseComponent(uaB); unbind == nil {
			t.Fatal("wB.elm.UseComponent() unbind = nil, want non-nil")
		}

		ctx := sip.ContextWithElement(t.Context(), fxA.elm)
		op, err := fxA.ua.Message(ctx, uriFor(wB.addr), useragent.MessageOperationOptions{
			Body: []byte("ping"),
		})
		if err != nil {
			t.Fatalf("uaA.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("uaA.Message() op = nil, want non-nil")
		}

		res, err := op.Wait(ctx)
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusOK {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
		}
	})

	t.Run("wire shape and ok response", func(t *testing.T) {
		t.Parallel()

		fx := newMsgSender(t)
		peer := newRawPeer(t)
		target := uriFor(msgPeerAddr(t, peer))
		body := []byte("ping")

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, target, useragent.MessageOperationOptions{Body: body})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		hdrs := captured.Headers()

		if mtd := captured.Method(); !mtd.Equal(sip.RequestMethodMessage) {
			t.Errorf("captured method = %v, want %v", mtd, sip.RequestMethodMessage)
		}
		if !captured.URI().Equal(target) {
			t.Errorf("captured URI = %v, want %v", captured.URI(), target)
		}

		from, ok := hdrs.From()
		if !ok || from == nil {
			t.Fatal("captured request has no From header")
		}
		if !from.URI.Equal(fx.aor) {
			t.Errorf("From URI = %v, want %v", from.URI, fx.aor)
		}
		if tag, ok := from.Tag(); !ok || tag == "" {
			t.Error("From header has no tag, want a generated tag")
		}

		to, ok := hdrs.To()
		if !ok || to == nil {
			t.Fatal("captured request has no To header")
		}
		if !to.URI.Equal(target) {
			t.Errorf("To URI = %v, want %v", to.URI, target)
		}
		if _, ok := to.Tag(); ok {
			t.Error("To header carries a tag, want none")
		}

		cseq, ok := hdrs.CSeq()
		if !ok || cseq == nil {
			t.Fatal("captured request has no CSeq header")
		}
		if cseq.SeqNum != 1 || !cseq.Method.Equal(sip.RequestMethodMessage) {
			t.Errorf("CSeq = %v, want SeqNum 1 MESSAGE", cseq)
		}

		if mf, ok := hdrs.MaxForwards(); !ok || mf != 70 {
			t.Errorf("Max-Forwards = %v, %v, want 70", mf, ok)
		}

		via, ok := hdrs.FirstVia()
		if !ok || via == nil {
			t.Fatal("captured request has no Via header")
		}
		if branch, ok := via.Branch(); !ok || !sip.IsRFC3261Branch(branch) {
			t.Errorf("Via branch = %q, %v, want a valid RFC 3261 branch", branch, ok)
		}
		if _, ok := via.Params.Last("rport"); !ok {
			t.Error("Via header has no rport parameter, want present")
		}

		if n := len(slices.Collect(hdrs.Contacts())); n != 0 {
			t.Errorf("captured MESSAGE carried %d Contact headers, want 0", n)
		}

		ct, ok := hdrs.ContentType()
		if !ok || ct == nil || ct.RenderValue() != "text/plain" {
			t.Errorf("Content-Type = %v, want %q", ct, "text/plain")
		}

		if !bytes.Equal(captured.Body(), body) {
			t.Errorf("captured body = %q, want %q", captured.Body(), body)
		}

		replyPeerRes(t, peer, captured, src, sip.ResponseStatusOK)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusOK {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
		}

		// A repeated Wait resolves the same published outcome.
		res2, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("second op.Wait() error = %v, want nil", err)
		}
		if !res2.Equal(res) {
			t.Errorf("second op.Wait() response = %v, want %v", res2, res)
		}
	})

	t.Run("final failure is a response", func(t *testing.T) {
		t.Parallel()

		fx := newMsgSender(t)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, uriFor(msgPeerAddr(t, peer)), useragent.MessageOperationOptions{
			Body: []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRes(t, peer, captured, src, sip.ResponseStatusNotFound)

		// An ordinary negative final resolves as a response, not an error.
		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusNotFound {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusNotFound)
		}
	})

	t.Run("copies caller inputs", func(t *testing.T) {
		t.Parallel()

		fx := newMsgSender(t)
		peer := newRawPeer(t)
		target := uriFor(msgPeerAddr(t, peer))
		wantURI := target.Clone()
		body := []byte("ping")

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, target, useragent.MessageOperationOptions{Body: body})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		// Validation and copying complete in the synchronous gate, so
		// mutating the caller's values afterwards must not change what
		// goes on the wire.
		body[0] = 'X'
		target.User = sip.MakeUserInfo("mallory")
		target.Addr = sip.MakeHostAddr("mallory.test")

		captured, src := readReq(t, peer)
		if !captured.URI().Equal(wantURI) {
			t.Errorf("captured URI = %v, want %v", captured.URI(), wantURI)
		}
		if !bytes.Equal(captured.Body(), []byte("ping")) {
			t.Errorf("captured body = %q, want %q", captured.Body(), "ping")
		}

		replyPeerRes(t, peer, captured, src, sip.ResponseStatusOK)
		if _, err := op.Wait(t.Context()); err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
	})

	t.Run("synchronous failures", func(t *testing.T) {
		t.Parallel()

		fx := newMsgSender(t)
		peer := newRawPeer(t)
		elmCtx := sip.ContextWithElement(t.Context(), fx.elm)
		target := uriFor(msgPeerAddr(t, peer))

		closedUA, err := useragent.New(useragent.Options{AOR: msgAOR(t)})
		if err != nil {
			t.Fatalf("useragent.New() error = %v, want nil", err)
		}
		if err := closedUA.Close(); err != nil {
			t.Fatalf("closedUA.Close() error = %v, want nil", err)
		}

		var nilSIPURI *uri.SIP
		tests := []struct {
			name   string
			ua     *useragent.UserAgent
			ctx    context.Context
			target sip.AnyURI
			opts   useragent.MessageOperationOptions
			want   error
			class  func(error) bool
		}{
			{
				name: "nil target",
				want: useragent.ErrInvalidRequest,
			},
			{
				name:   "typed nil target",
				target: nilSIPURI,
				want:   useragent.ErrInvalidRequest,
			},
			{
				name:   "invalid target",
				target: &uri.SIP{},
				want:   useragent.ErrInvalidRequest,
			},
			{
				name:   "malformed content type",
				target: target,
				opts:   useragent.MessageOperationOptions{ContentType: "not a type"},
				want:   useragent.ErrInvalidRequest,
			},
			{
				name:   "missing element",
				ctx:    t.Context(),
				target: target,
				want:   useragent.ErrMissingElement,
			},
			{
				name:   "zero user agent",
				ua:     &useragent.UserAgent{},
				target: target,
				want:   useragent.ErrClosed,
				class:  errclass.IsClosed,
			},
			{
				name:   "closed user agent",
				ua:     closedUA,
				target: target,
				want:   useragent.ErrClosed,
				class:  errclass.IsClosed,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				u := tt.ua
				if u == nil {
					u = fx.ua
				}
				ctx := tt.ctx
				if ctx == nil {
					ctx = elmCtx
				}

				op, err := u.Message(ctx, tt.target, tt.opts)
				if op != nil {
					t.Fatalf("ua.Message() op = %v, want nil", op)
				}
				if !errors.Is(err, tt.want) {
					t.Fatalf("ua.Message() error = %v, want %v", err, tt.want)
				}
				if tt.class != nil && !tt.class(err) {
					t.Errorf("ua.Message() error = %v does not satisfy the expected class", err)
				}
			})
		}

		// No synchronous rejection may reach the wire: every failing case
		// returns before an operation worker is started.
		expectNoDatagram(t, peer)
	})

	t.Run("element binding per context", func(t *testing.T) {
		t.Parallel()

		fx := newMsgSender(t)
		w2 := newWireElem(t)
		peer1 := newRawPeer(t)
		peer2 := newRawPeer(t)

		ctx1 := sip.ContextWithElement(t.Context(), fx.elm)
		op1, err := fx.ua.Message(ctx1, uriFor(msgPeerAddr(t, peer1)), useragent.MessageOperationOptions{
			Body: []byte("one"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op1 == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}
		captured1, src1 := readReq(t, peer1)
		if src1 != fx.addr {
			t.Fatalf("first request source = %v, want %v", src1, fx.addr)
		}
		replyPeerRes(t, peer1, captured1, src1, sip.ResponseStatusOK)

		ctx2 := sip.ContextWithElement(t.Context(), w2.elm)
		op2, err := fx.ua.Message(ctx2, uriFor(msgPeerAddr(t, peer2)), useragent.MessageOperationOptions{
			Body: []byte("two"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op2 == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}
		captured2, src2 := readReq(t, peer2)
		if src2 != w2.addr {
			t.Fatalf("second request source = %v, want %v", src2, w2.addr)
		}
		replyPeerRes(t, peer2, captured2, src2, sip.ResponseStatusOK)

		for i, op := range []*useragent.MessageOperation{op1, op2} {
			res, err := op.Wait(t.Context())
			if err != nil {
				t.Fatalf("op%d.Wait() error = %v, want nil", i+1, err)
			}
			if got := res.Status(); got != sip.ResponseStatusOK {
				t.Fatalf("op%d.Wait() status = %v, want %v", i+1, got, sip.ResponseStatusOK)
			}
		}
	})

	t.Run("pre-canceled context", func(t *testing.T) {
		t.Parallel()

		fx := newMsgSender(t)
		peer := newRawPeer(t)
		ctx, cancel := context.WithCancel(sip.ContextWithElement(t.Context(), fx.elm))
		cancel()

		op, err := fx.ua.Message(ctx, uriFor(msgPeerAddr(t, peer)), useragent.MessageOperationOptions{
			Body: []byte("ping"),
		})
		if op != nil {
			t.Fatalf("ua.Message() op = %v, want nil", op)
		}
		if !errors.Is(err, useragent.ErrCanceled) || !errclass.IsCanceled(err) {
			t.Fatalf("ua.Message() error = %v, want canceled %v", err, useragent.ErrCanceled)
		}
		expectNoDatagram(t, peer)
	})

	t.Run("service unavailable fails the operation", func(t *testing.T) {
		t.Parallel()

		fx := newMsgSender(t)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, uriFor(msgPeerAddr(t, peer)), useragent.MessageOperationOptions{
			Body: []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRes(t, peer, captured, src, sip.ResponseStatusServiceUnavailable)

		// A 503 is the element's DNS-failover outcome, never an ordinary
		// final response: with a single candidate the send error
		// propagates with its RequestAttemptError chain intact.
		res, err := op.Wait(t.Context())
		if err == nil {
			t.Fatalf("op.Wait() error = nil, response = %v, want an error", res)
		}
		if res != nil {
			t.Errorf("op.Wait() response = %v, want nil", res)
		}
		var raErr *sip.RequestAttemptError
		if !errors.As(err, &raErr) {
			t.Errorf("op.Wait() error = %v, want a *sip.RequestAttemptError in chain", err)
		}
	})

	t.Run("provisional progress reaches OnProgress", func(t *testing.T) {
		t.Parallel()

		fx := newMsgSender(t)
		peer := newRawPeer(t)
		progCh := make(chan sip.ResponseStatus, 4)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, uriFor(msgPeerAddr(t, peer)), useragent.MessageOperationOptions{
			Body: []byte("ping"),
			OnProgress: func(_ context.Context, res *sip.ResponseEnvelope) {
				progCh <- res.Status()
			},
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRes(t, peer, captured, src, sip.ResponseStatusRinging)

		select {
		case sts := <-progCh:
			if sts != sip.ResponseStatusRinging {
				t.Fatalf("progress status = %v, want %v", sts, sip.ResponseStatusRinging)
			}
		case <-time.After(wireReadTimeout):
			t.Fatal("OnProgress not called for the provisional response")
		}

		replyPeerRes(t, peer, captured, src, sip.ResponseStatusOK)
		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusOK {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
		}

		// The final response resolves the operation; it is never a
		// progress event.
		select {
		case sts := <-progCh:
			t.Fatalf("unexpected progress status %v after the final response", sts)
		case <-time.After(wireNoResponseWindow):
		}
	})
}
