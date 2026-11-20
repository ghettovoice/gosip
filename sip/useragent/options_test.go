package useragent_test

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/useragent"
	"github.com/ghettovoice/gosip/uri"
)

// collectAllow flattens every Allow header element of res into a single
// method list.
func collectAllow(res *sip.ResponseEnvelope) header.Allow {
	var allow header.Allow
	for _, h := range res.Headers().Get("Allow") {
		if a, ok := h.(header.Allow); ok {
			allow = append(allow, a...)
		}
	}
	return allow
}

func TestUserAgent_OptionsOperation(t *testing.T) {
	t.Parallel()

	t.Run("user agent to user agent", func(t *testing.T) {
		t.Parallel()

		contact, err := sip.ParseURI("sip:alice@pc.ua.test")
		if err != nil {
			t.Fatalf("sip.ParseURI() error = %v, want nil", err)
		}
		fxA := newMsgSender(t)
		wB := newWireElem(t)

		// The capture probe is registered before the user agent, so it
		// observes the OPTIONS request and still passes it to the
		// auto answer.
		reqCh := make(chan *sip.RequestEnvelope, 1)
		if unbind := wB.elm.UseComponent(sip.InboundRequestInterceptorFunc(
			func(ctx context.Context, next sip.RequestReceiver, req *sip.RequestEnvelope) error {
				reqCh <- req
				return next.RecvRequest(ctx, req)
			},
		)); unbind == nil {
			t.Fatal("wB.elm.UseComponent() unbind = nil, want non-nil")
		}

		bob, err := sip.ParseURI("sip:bob@ua.test")
		if err != nil {
			t.Fatalf("sip.ParseURI() error = %v, want nil", err)
		}
		uaB, err := useragent.New(useragent.Options{
			AOR:       bob,
			OnMessage: noopReqHandler,
		})
		if err != nil {
			t.Fatalf("useragent.New() error = %v, want nil", err)
		}
		if unbind := wB.elm.UseComponent(uaB); unbind == nil {
			t.Fatal("wB.elm.UseComponent() unbind = nil, want non-nil")
		}

		ctx := sip.ContextWithElement(t.Context(), fxA.elm)
		op, err := fxA.ua.Options(ctx, uriFor(wB.addr))
		if err != nil {
			t.Fatalf("uaA.Options() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("uaA.Options() op = nil, want non-nil")
		}

		var captured *sip.RequestEnvelope
		select {
		case captured = <-reqCh:
		case <-time.After(wireReadTimeout):
			t.Fatal("capture probe did not observe the OPTIONS request")
		}

		// The OPTIONS request carries no body, and it carries a Contact
		// only because the sender user agent configured one.
		if got := len(captured.Body()); got != 0 {
			t.Errorf("captured OPTIONS body length = %d, want 0", got)
		}
		contacts := slices.Collect(captured.Headers().Contacts())
		if len(contacts) != 1 {
			t.Fatalf("captured OPTIONS carried %d Contact headers, want 1", len(contacts))
		}
		if !contacts[0].URI.Equal(contact) {
			t.Errorf("Contact URI = %v, want %v", contacts[0].URI, contact)
		}

		res, err := op.Wait(ctx)
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusOK {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
		}

		// The auto answer advertises uaB's own methods: the MESSAGE
		// handler and the OPTIONS auto answer.
		wantAllow := header.Allow{sip.RequestMethodMessage, sip.RequestMethodOptions}
		if diff := cmp.Diff(wantAllow, collectAllow(res)); diff != "" {
			t.Fatalf("Allow header mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("request contact follows configuration", func(t *testing.T) {
		t.Parallel()

		contact, err := sip.ParseURI("sip:alice@pc.ua.test")
		if err != nil {
			t.Fatalf("sip.ParseURI() error = %v, want nil", err)
		}

		tests := []struct {
			name         string
			contact      *sip.URI
			wantContacts int
		}{
			{name: "configured contact is carried", contact: contact, wantContacts: 1},
			{name: "no configured contact carries none", wantContacts: 0},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				w := newWireElem(t)
				opts := useragent.Options{AOR: msgAOR(t), Contact: tt.contact}
				ua, err := useragent.New(opts)
				if err != nil {
					t.Fatalf("useragent.New() error = %v, want nil", err)
				}
				peer := newRawPeer(t)
				target := uriFor(msgPeerAddr(t, peer))

				ctx := sip.ContextWithElement(t.Context(), w.elm)
				op, err := ua.Options(ctx, target)
				if err != nil {
					t.Fatalf("ua.Options() error = %v, want nil", err)
				}
				if op == nil {
					t.Fatal("ua.Options() op = nil, want non-nil")
				}

				captured, src := readReq(t, peer)
				if mtd := captured.Method(); !mtd.Equal(sip.RequestMethodOptions) {
					t.Errorf("captured method = %v, want %v", mtd, sip.RequestMethodOptions)
				}
				if !captured.URI().Equal(target) {
					t.Errorf("captured URI = %v, want %v", captured.URI(), target)
				}
				if got := len(captured.Body()); got != 0 {
					t.Errorf("captured OPTIONS body length = %d, want 0", got)
				}
				if _, ok := captured.Headers().ContentType(); ok {
					t.Error("captured OPTIONS carried a Content-Type header, want none")
				}

				contacts := slices.Collect(captured.Headers().Contacts())
				if len(contacts) != tt.wantContacts {
					t.Fatalf("captured OPTIONS carried %d Contact headers, want %d",
						len(contacts), tt.wantContacts)
				}
				if tt.contact != nil && !contacts[0].URI.Equal(tt.contact) {
					t.Errorf("Contact URI = %v, want %v", contacts[0].URI, tt.contact)
				}

				replyPeerRes(t, peer, captured, src, sip.ResponseStatusOK)
				res, err := op.Wait(t.Context())
				if err != nil {
					t.Fatalf("op.Wait() error = %v, want nil", err)
				}
				if got := res.Status(); got != sip.ResponseStatusOK {
					t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
				}
			})
		}
	})

	t.Run("progress and completion callbacks", func(t *testing.T) {
		t.Parallel()

		fx := newMsgSender(t)
		peer := newRawPeer(t)
		progCh := make(chan sip.ResponseStatus, 4)
		var progressCalls atomic.Int32
		complete := make(chan struct{}, 1)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Options(ctx, uriFor(msgPeerAddr(t, peer)), useragent.OptionsOperationOptions{
			OnProgress: func(_ context.Context, res *sip.ResponseEnvelope) {
				progressCalls.Add(1)
				progCh <- res.Status()
				// The callback owns a cloned envelope: mutating it
				// must never reach the operation's outcome.
				res.WithMessage(func(msg *sip.Response) {
					msg.Status = sip.ResponseStatusBusyHere
				})
			},
			OnComplete: func(_ context.Context, _ useragent.Result) {
				complete <- struct{}{}
			},
		})
		if err != nil {
			t.Fatalf("ua.Options() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Options() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRes(t, peer, captured, src, sip.ResponseStatusSessionProgress)

		select {
		case sts := <-progCh:
			if sts != sip.ResponseStatusSessionProgress {
				t.Fatalf("progress status = %v, want %v", sts, sip.ResponseStatusSessionProgress)
			}
		case <-time.After(wireReadTimeout):
			t.Fatal("OnProgress not called for the provisional response")
		}

		// The provisional response is progress only: the operation
		// stays pending until the final response arrives.
		select {
		case <-op.Done():
			t.Fatal("op.Done() closed after a provisional response")
		case <-time.After(wireNoResponseWindow):
		}

		replyPeerRes(t, peer, captured, src, sip.ResponseStatusOK)
		res, err := op.Wait(t.Context())
		if err != nil || res.Status() != sip.ResponseStatusOK {
			t.Fatalf("op.Wait() = %v, %v, want 200", res, err)
		}

		// Outcome publication precedes the completion callback, so the
		// barrier channel — not Done or Wait — proves OnComplete ran.
		select {
		case <-complete:
		case <-time.After(2 * time.Second):
			t.Fatal("OnComplete never fired")
		}
		if got := progressCalls.Load(); got != 1 {
			t.Fatalf("OnProgress calls = %d, want 1", got)
		}
	})

	t.Run("bare final response emits no progress", func(t *testing.T) {
		t.Parallel()

		fx := newMsgSender(t)
		peer := newRawPeer(t)
		var progressCalls atomic.Int32

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Options(ctx, uriFor(msgPeerAddr(t, peer)), useragent.OptionsOperationOptions{
			OnProgress: func(_ context.Context, _ *sip.ResponseEnvelope) {
				progressCalls.Add(1)
			},
		})
		if err != nil {
			t.Fatalf("ua.Options() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Options() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRes(t, peer, captured, src, sip.ResponseStatusOK)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusOK {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
		}
		if got := progressCalls.Load(); got != 0 {
			t.Fatalf("OnProgress calls = %d, want 0", got)
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

				op, err := u.Options(ctx, tt.target)
				if op != nil {
					t.Fatalf("ua.Options() op = %v, want nil", op)
				}
				if !errors.Is(err, tt.want) {
					t.Fatalf("ua.Options() error = %v, want %v", err, tt.want)
				}
				if tt.class != nil && !tt.class(err) {
					t.Errorf("ua.Options() error = %v does not satisfy the expected class", err)
				}
			})
		}

		// No synchronous rejection may reach the wire: every failing
		// case returns before an operation worker is started.
		expectNoDatagram(t, peer)
	})

	t.Run("pre-canceled context", func(t *testing.T) {
		t.Parallel()

		fx := newMsgSender(t)
		peer := newRawPeer(t)
		ctx, cancel := context.WithCancel(sip.ContextWithElement(t.Context(), fx.elm))
		cancel()

		op, err := fx.ua.Options(ctx, uriFor(msgPeerAddr(t, peer)))
		if op != nil {
			t.Fatalf("ua.Options() op = %v, want nil", op)
		}
		if !errors.Is(err, useragent.ErrCanceled) || !errclass.IsCanceled(err) {
			t.Fatalf("ua.Options() error = %v, want canceled %v", err, useragent.ErrCanceled)
		}
		expectNoDatagram(t, peer)
	})
}
