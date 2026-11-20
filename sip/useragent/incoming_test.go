package useragent_test

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/useragent"
)

// errNext is the marker error the recording receiver returns: a
// pass-through proves it reached the next interceptor by coming back as
// this error.
var errNext = errors.New("next interceptor reached")

// newClaimReq builds a fully headered inbound request envelope for the
// claim table: [sip.NewRequest] supplies the mandatory headers and the
// envelope carries loopback addressing metadata like a wire-delivered
// request.
func newClaimReq(tb testing.TB, method sip.RequestMethod) *sip.RequestEnvelope {
	tb.Helper()

	target := &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("voip.test")}
	caller := &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("voip.test")}
	req, err := sip.NewRequest(method, target, caller, target)
	if err != nil {
		tb.Fatalf("sip.NewRequest(%q) error = %v, want nil", method, err)
	}
	return sip.NewRequestEnvelope(req).
		SetTransport(sip.UDPMetadata()).
		SetRemoteAddr(netip.MustParseAddrPort("127.0.0.1:5060")).
		SetLocalAddr(netip.MustParseAddrPort("127.0.0.1:5061"))
}

func TestUserAgent_Claiming(t *testing.T) {
	t.Parallel()

	aor := testAOR(t)

	tests := []struct {
		name            string
		method          sip.RequestMethod
		onMessage       bool
		onOptions       bool
		disableAutoOpts bool
		closed          bool
		zero            bool
		wantErr         error
	}{
		// Pass-through cases: an unclaimed request reaches the next
		// interceptor with the identical envelope and context.
		{
			name:    "message without handler passes through",
			method:  sip.RequestMethodMessage,
			wantErr: errNext,
		},
		{
			name:            "options without auto answer passes through",
			method:          sip.RequestMethodOptions,
			disableAutoOpts: true,
			wantErr:         errNext,
		},
		{
			name:      "ack passes through",
			method:    sip.RequestMethodAck,
			onMessage: true,
			wantErr:   errNext,
		},
		{
			name:      "cancel passes through",
			method:    sip.RequestMethodCancel,
			onMessage: true,
			wantErr:   errNext,
		},
		{
			name:      "unknown method passes through",
			method:    sip.RequestMethod("FOO"),
			onMessage: true,
			onOptions: true,
			wantErr:   errNext,
		},
		{
			name:      "closed passes through",
			method:    sip.RequestMethodMessage,
			onMessage: true,
			closed:    true,
			wantErr:   errNext,
		},
		{
			name:    "zero value passes through",
			method:  sip.RequestMethodMessage,
			zero:    true,
			wantErr: errNext,
		},
		// Claim-selection cases: a claimed request with no element in its
		// context fails with ErrMissingElement before reaching next or the
		// handler. Dispatch to a claimed element lands with the inbound
		// dispatch, so no case supplies one here.
		{
			name:      "message claimed",
			method:    sip.RequestMethodMessage,
			onMessage: true,
			wantErr:   useragent.ErrMissingElement,
		},
		{
			name:      "options claimed",
			method:    sip.RequestMethodOptions,
			onOptions: true,
			wantErr:   useragent.ErrMissingElement,
		},
		{
			name:    "options auto answer claimed",
			method:  sip.RequestMethodOptions,
			wantErr: useragent.ErrMissingElement,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var handlerCalls atomic.Int32
			handler := useragent.RequestHandler(func(context.Context, *useragent.IncomingMessage) error {
				handlerCalls.Add(1)
				return nil
			})

			var ua *useragent.UserAgent
			if tt.zero {
				ua = new(useragent.UserAgent)
			} else {
				opts := useragent.Options{
					AOR:                      aor,
					DisableOptionsAutoAnswer: tt.disableAutoOpts,
				}
				if tt.onMessage {
					opts.OnMessage = handler
				}
				if tt.onOptions {
					opts.OnOptions = handler
				}

				var err error
				ua, err = useragent.New(opts)
				if err != nil {
					t.Fatalf("useragent.New() error = %v, want nil", err)
				}
				if tt.closed {
					if err := ua.Close(); err != nil {
						t.Fatalf("ua.Close() error = %v, want nil", err)
					}
				}
			}

			ctx := t.Context()
			req := newClaimReq(t, tt.method)

			var (
				nextCalls atomic.Int32
				gotReq    *sip.RequestEnvelope
				sameCtx   atomic.Bool
			)
			next := sip.RequestReceiverFunc(func(nextCtx context.Context, nextReq *sip.RequestEnvelope) error {
				nextCalls.Add(1)
				gotReq = nextReq
				sameCtx.Store(nextCtx == ctx)
				return errNext
			})

			err := ua.InterceptInboundRequest(ctx, next, req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ua.InterceptInboundRequest() error = %v, want %v", err, tt.wantErr)
			}
			if errors.Is(err, errNext) && (nextCalls.Load() != 1 || gotReq != req || !sameCtx.Load()) {
				t.Fatal("pass-through replaced the request envelope or context")
			}
			if errors.Is(err, useragent.ErrMissingElement) && (nextCalls.Load() != 0 || handlerCalls.Load() != 0) {
				t.Fatal("missing-element claim reached next or the handler")
			}
		})
	}
}

// wireFixture bundles an element serving real loopback UDP, a registered
// user agent, and a raw UDP peer acting as the remote endpoint.
type wireFixture struct {
	*wireElement
	ua   *useragent.UserAgent
	peer *net.UDPConn
}

func newWireFixture(tb testing.TB, opts useragent.Options) *wireFixture {
	tb.Helper()

	w := newWireElem(tb)
	opts.AOR = testAOR(tb)
	ua, err := useragent.New(opts)
	if err != nil {
		tb.Fatalf("useragent.New() error = %v, want nil", err)
	}
	if unbind := w.elm.UseComponent(ua); unbind == nil {
		tb.Fatal("elm.UseComponent() unbind = nil, want non-nil")
	}
	return &wireFixture{wireElement: w, ua: ua, peer: newRawPeer(tb)}
}

// newMessageReq builds the MESSAGE request envelope the raw peer sends to
// the element addressed by ruri.
func newMessageReq(tb testing.TB, ruri *sip.URI) *sip.RequestEnvelope {
	tb.Helper()
	return newWireReq(tb, sip.RequestMethodMessage, ruri)
}

// newOptionsReq builds the OPTIONS request envelope the raw peer sends to
// the element addressed by ruri.
func newOptionsReq(tb testing.TB, ruri *sip.URI) *sip.RequestEnvelope {
	tb.Helper()
	return newWireReq(tb, sip.RequestMethodOptions, ruri)
}

// newWireReq builds a fully headered request envelope for method that the
// raw peer sends to the element addressed by ruri.
func newWireReq(tb testing.TB, method sip.RequestMethod, ruri *sip.URI) *sip.RequestEnvelope {
	tb.Helper()

	caller := &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("voip.test")}
	callee := &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("voip.test")}
	// RFC 3581: rport lets the transport's response routing learn the
	// peer's real source port; without it the response resolves to the
	// Via sent-by address and never reaches the raw peer.
	req, err := sip.NewRequest(
		method,
		ruri,
		caller,
		callee,
		sip.RequestOptions{RPort: true},
	)
	if err != nil {
		tb.Fatalf("sip.NewRequest(%q) error = %v, want nil", method, err)
	}
	return sip.NewRequestEnvelope(req)
}

// waitResCh receives one responder outcome within a bounded wait.
func waitResCh(tb testing.TB, ch <-chan error, name string) error {
	tb.Helper()

	select {
	case err := <-ch:
		return err
	case <-time.After(wireReadTimeout):
		tb.Fatalf("useragent: %s not received within %s", name, wireReadTimeout)
		return nil
	}
}

func TestIncomingMessage_Respond(t *testing.T) {
	t.Parallel()

	t.Run("final response and retransmission replay", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32
		resCh := make(chan error, 1)
		done := make(chan struct{})
		fx := newWireFixture(t, useragent.Options{
			OnMessage: func(ctx context.Context, in *useragent.IncomingMessage) error {
				calls.Add(1)
				defer close(done)

				if tx, ok := sip.ServerTransactionFromContext(ctx); !ok || tx == nil {
					t.Error("handler context has no server transaction")
				}
				if err := in.Respond(ctx, sip.ResponseStatusOK); err != nil {
					return err
				}
				// A second final must be rejected by the responder gate
				// before anything is sent.
				resCh <- in.Respond(ctx, sip.ResponseStatusNotFound)
				return nil
			},
		})

		req := newMessageReq(t, uriFor(fx.addr))
		sendReq(t, fx.peer, fx.addr, req)

		res := readRes(t, fx.peer)
		if got := res.Message().Status; got != sip.ResponseStatusOK {
			t.Fatalf("response status = %v, want %v", got, sip.ResponseStatusOK)
		}
		waitChan(t, done, "handler done")
		if err := waitResCh(t, resCh, "second Respond() result"); !errors.Is(err, useragent.ErrResponseFinal) {
			t.Fatalf("in.Respond() error = %v, want %v", err, useragent.ErrResponseFinal)
		}

		// An identical retransmitted datagram must not redispatch the
		// handler: the transaction itself replays the final response.
		sendReq(t, fx.peer, fx.addr, req)
		res = readRes(t, fx.peer)
		if got := res.Message().Status; got != sip.ResponseStatusOK {
			t.Fatalf("retransmission response status = %v, want %v", got, sip.ResponseStatusOK)
		}
		if got := calls.Load(); got != 1 {
			t.Errorf("handler calls = %d, want 1", got)
		}
		expectNoDatagram(t, fx.peer)
	})

	t.Run("typed rejection answers once", func(t *testing.T) {
		t.Parallel()

		cause := errors.New("auth required")
		hdrs := sip.Headers{}.Set(&header.Any{Name: "X-Test", Value: "rejected"})
		fx := newWireFixture(t, useragent.Options{
			OnMessage: func(context.Context, *useragent.IncomingMessage) error {
				return sip.NewRequestRejectedError(
					cause,
					slog.LevelWarn,
					sip.ResponseStatusUnauthorized,
					sip.RespondOptions{ResponseOptions: sip.ResponseOptions{Headers: hdrs}},
				)
			},
		})

		sendReq(t, fx.peer, fx.addr, newMessageReq(t, uriFor(fx.addr)))

		res := readRes(t, fx.peer)
		if got := res.Message().Status; got != sip.ResponseStatusUnauthorized {
			t.Fatalf("response status = %v, want %v", got, sip.ResponseStatusUnauthorized)
		}
		if got := res.Message().Headers.Get("X-Test"); len(got) != 1 || got[0].RenderValue() != "rejected" {
			t.Errorf("X-Test headers = %v, want exactly one with value %q", got, "rejected")
		}
		// The dispatcher consumed the rejection, so no other layer answers.
		expectNoDatagram(t, fx.peer)
	})

	t.Run("handler error sends 500 once", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32
		marker := errors.New("handler failure")
		fx := newWireFixture(t, useragent.Options{
			OnMessage: func(context.Context, *useragent.IncomingMessage) error {
				calls.Add(1)
				return marker
			},
		})

		req := newMessageReq(t, uriFor(fx.addr))
		sendReq(t, fx.peer, fx.addr, req)

		// The dispatcher reports the failure as a rejection, and the
		// transaction manager's deferred responder answers it once
		// through the registered server transaction: no second,
		// stateless answer follows on the wire.
		res := readRes(t, fx.peer)
		if got := res.Message().Status; got != sip.ResponseStatusServerInternalError {
			t.Fatalf("response status = %v, want %v", got, sip.ResponseStatusServerInternalError)
		}

		// The answer is transactional: an identical retransmission
		// replays the same 500 without reaching the handler again.
		sendReq(t, fx.peer, fx.addr, req)
		res = readRes(t, fx.peer)
		if got := res.Message().Status; got != sip.ResponseStatusServerInternalError {
			t.Fatalf("retransmission response status = %v, want %v", got, sip.ResponseStatusServerInternalError)
		}
		if got := calls.Load(); got != 1 {
			t.Errorf("handler calls = %d, want 1", got)
		}
		expectNoDatagram(t, fx.peer)
	})

	t.Run("request returns isolated clone", func(t *testing.T) {
		t.Parallel()

		fx := newWireFixture(t, useragent.Options{
			OnMessage: func(ctx context.Context, in *useragent.IncomingMessage) error {
				clone := in.Request().WithMessage(func(r *sip.Request) {
					r.Headers.Set(header.CallID("mutated@voip.test"))
				})
				if cid, _ := clone.Message().Headers.CallID(); cid != "mutated@voip.test" {
					t.Errorf("mutated clone Call-ID = %q, want %q", cid, "mutated@voip.test")
				}
				return in.Respond(ctx, sip.ResponseStatusOK)
			},
		})

		req := newMessageReq(t, uriFor(fx.addr))
		wantCID, _ := req.Message().Headers.CallID()
		sendReq(t, fx.peer, fx.addr, req)

		res := readRes(t, fx.peer)
		if got := res.Message().Status; got != sip.ResponseStatusOK {
			t.Fatalf("response status = %v, want %v", got, sip.ResponseStatusOK)
		}
		// Mutating the returned clone must not leak into the stored
		// request: the response still echoes the original Call-ID.
		if cid, _ := res.Message().Headers.CallID(); cid != wantCID {
			t.Errorf("response Call-ID = %q, want %q", cid, wantCID)
		}
	})

	t.Run("retained responder", func(t *testing.T) {
		t.Parallel()

		inCh := make(chan *useragent.IncomingMessage, 1)
		fx := newWireFixture(t, useragent.Options{
			OnMessage: func(_ context.Context, in *useragent.IncomingMessage) error {
				inCh <- in
				return nil
			},
		})

		sendReq(t, fx.peer, fx.addr, newMessageReq(t, uriFor(fx.addr)))

		var in *useragent.IncomingMessage
		select {
		case in = <-inCh:
		case <-time.After(wireReadTimeout):
			t.Fatal("handler did not receive the inbound message")
		}

		// A retained handle still reaches the peer through its transaction.
		if err := in.Respond(t.Context(), sip.ResponseStatusOK); err != nil {
			t.Fatalf("in.Respond() error = %v, want nil", err)
		}
		res := readRes(t, fx.peer)
		if got := res.Message().Status; got != sip.ResponseStatusOK {
			t.Fatalf("response status = %v, want %v", got, sip.ResponseStatusOK)
		}

		if err := fx.ua.Close(); err != nil {
			t.Fatalf("ua.Close() error = %v, want nil", err)
		}
		if err := in.Respond(t.Context(), sip.ResponseStatusNotFound); !errors.Is(err, useragent.ErrClosed) {
			t.Errorf("in.Respond() after ua.Close() error = %v, want %v", err, useragent.ErrClosed)
		}
	})

	t.Run("provisional then final", func(t *testing.T) {
		t.Parallel()

		resCh := make(chan error, 1)
		done := make(chan struct{})
		fx := newWireFixture(t, useragent.Options{
			OnMessage: func(ctx context.Context, in *useragent.IncomingMessage) error {
				defer close(done)

				if err := in.Respond(ctx, sip.ResponseStatusTrying); err != nil {
					return err
				}
				if err := in.Respond(ctx, sip.ResponseStatusOK); err != nil {
					return err
				}
				// Only the 200 commits the final gate.
				resCh <- in.Respond(ctx, sip.ResponseStatusBusyHere)
				return nil
			},
		})

		sendReq(t, fx.peer, fx.addr, newMessageReq(t, uriFor(fx.addr)))

		res := readRes(t, fx.peer)
		if got := res.Message().Status; got != sip.ResponseStatusTrying {
			t.Fatalf("response status = %v, want %v", got, sip.ResponseStatusTrying)
		}
		res = readRes(t, fx.peer)
		if got := res.Message().Status; got != sip.ResponseStatusOK {
			t.Fatalf("response status = %v, want %v", got, sip.ResponseStatusOK)
		}
		waitChan(t, done, "handler done")
		if err := waitResCh(t, resCh, "third Respond() result"); !errors.Is(err, useragent.ErrResponseFinal) {
			t.Fatalf("in.Respond() error = %v, want %v", err, useragent.ErrResponseFinal)
		}
		expectNoDatagram(t, fx.peer)
	})

	t.Run("concurrent finals send once", func(t *testing.T) {
		t.Parallel()

		inCh := make(chan *useragent.IncomingMessage, 1)
		fx := newWireFixture(t, useragent.Options{
			OnMessage: func(_ context.Context, in *useragent.IncomingMessage) error {
				inCh <- in
				return nil
			},
		})

		sendReq(t, fx.peer, fx.addr, newMessageReq(t, uriFor(fx.addr)))

		var in *useragent.IncomingMessage
		select {
		case in = <-inCh:
		case <-time.After(wireReadTimeout):
			t.Fatal("handler did not receive the inbound message")
		}

		resCh := make(chan error, 2)
		go func() { resCh <- in.Respond(t.Context(), sip.ResponseStatusOK) }()
		go func() { resCh <- in.Respond(t.Context(), sip.ResponseStatusBusyHere) }()

		var sent, rejected int
		for range 2 {
			err := waitResCh(t, resCh, "Respond() result")
			switch {
			case err == nil:
				sent++
			case errors.Is(err, useragent.ErrResponseFinal):
				rejected++
			default:
				t.Fatalf("in.Respond() error = %v, want nil or %v", err, useragent.ErrResponseFinal)
			}
		}
		if sent != 1 || rejected != 1 {
			t.Errorf("concurrent Respond() results = (%d sent, %d rejected), want (1, 1)", sent, rejected)
		}

		res := readRes(t, fx.peer)
		if got := res.Message().Status; got != sip.ResponseStatusOK && got != sip.ResponseStatusBusyHere {
			t.Fatalf("response status = %v, want %v or %v", got, sip.ResponseStatusOK, sip.ResponseStatusBusyHere)
		}
		expectNoDatagram(t, fx.peer)
	})

	t.Run("message 2xx with body or contact rejected", func(t *testing.T) {
		t.Parallel()

		resCh := make(chan error, 3)
		fx := newWireFixture(t, useragent.Options{
			OnMessage: func(ctx context.Context, in *useragent.IncomingMessage) error {
				resCh <- in.Respond(
					ctx,
					sip.ResponseStatusOK,
					sip.ResponseOptions{Body: []byte("payload")},
				)
				resCh <- in.Respond(
					ctx,
					sip.ResponseStatusOK,
					sip.ResponseOptions{Headers: sip.Headers{}.Set(header.Contact{{URI: testAOR(t)}})},
				)
				resCh <- in.Respond(ctx, sip.ResponseStatusOK)
				return nil
			},
		})

		sendReq(t, fx.peer, fx.addr, newMessageReq(t, uriFor(fx.addr)))

		for range 2 {
			if err := waitResCh(t, resCh, "Respond() result"); !errors.Is(err, useragent.ErrInvalidRequest) {
				t.Fatalf("in.Respond() error = %v, want %v", err, useragent.ErrInvalidRequest)
			}
		}
		if err := waitResCh(t, resCh, "Respond() result"); err != nil {
			t.Fatalf("in.Respond() error = %v, want nil", err)
		}

		// The invalid attempts sent nothing before the valid 200 arrived.
		res := readRes(t, fx.peer)
		if got := res.Message().Status; got != sip.ResponseStatusOK {
			t.Fatalf("response status = %v, want %v", got, sip.ResponseStatusOK)
		}
		if got := len(res.Message().Body); got != 0 {
			t.Errorf("response body length = %d, want 0", got)
		}
		expectNoDatagram(t, fx.peer)
	})
}

func TestIncomingMessage_ShutdownCancelsHandlerContext(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	done := make(chan struct{})
	inCh := make(chan *useragent.IncomingMessage, 1)
	fx := newWireFixture(t, useragent.Options{
		OnMessage: func(ctx context.Context, in *useragent.IncomingMessage) error {
			close(entered)
			inCh <- in
			defer close(done)
			// Blocks until the inbound context is canceled by shutdown;
			// the context here is the inbound scope, not a stale caller
			// transaction context.
			<-ctx.Done()
			return nil
		},
	})

	sendReq(t, fx.peer, fx.addr, newMessageReq(t, uriFor(fx.addr)))
	waitChan(t, entered, "handler enter")

	ctx, cancel := context.WithTimeout(t.Context(), wireReadTimeout)
	t.Cleanup(cancel)
	if err := fx.ua.Shutdown(ctx); err != nil {
		t.Fatalf("ua.Shutdown() error = %v, want nil", err)
	}
	// Shutdown returned: the handler was drained by its canceled inbound
	// context, without waiting for any remote decision.
	waitChan(t, done, "handler done")

	in := <-inCh
	if err := in.Respond(t.Context(), sip.ResponseStatusOK); !errors.Is(err, useragent.ErrClosed) {
		t.Errorf("in.Respond() after ua.Shutdown() error = %v, want %v", err, useragent.ErrClosed)
	}
	expectNoDatagram(t, fx.peer)
}

// advCapsStub is a component stub advertising a fixed capability set,
// including the option tags the method-only capsStub cannot express.
type advCapsStub struct {
	caps sip.ElementCapabilities
}

func (s *advCapsStub) Capabilities() sip.ElementCapabilities { return s.caps }

func TestUserAgent_Options(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		onMessage       bool
		onOptionsStatus sip.ResponseStatus
		disableAutoOpts bool
		probe           bool
		components      []sip.ElementComponent
		secondElement   bool
		wantStatus      sip.ResponseStatus
		wantAllow       header.Allow
		wantProbeCalls  int32
	}{
		{
			name:       "auto answer",
			wantStatus: sip.ResponseStatusOK,
			wantAllow:  header.Allow{sip.RequestMethodOptions},
		},
		{
			name:       "auto answer advertises message handler",
			onMessage:  true,
			wantStatus: sip.ResponseStatusOK,
			wantAllow:  header.Allow{sip.RequestMethodMessage, sip.RequestMethodOptions},
		},
		{
			name:            "options handler controls response",
			onOptionsStatus: sip.ResponseStatusBusyHere,
			wantStatus:      sip.ResponseStatusBusyHere,
		},
		{
			name:            "options handler rejects not found",
			onOptionsStatus: sip.ResponseStatusNotFound,
			wantStatus:      sip.ResponseStatusNotFound,
		},
		{
			name:            "disabled auto answer passes through",
			disableAutoOpts: true,
			probe:           true,
			wantStatus:      sip.ResponseStatusServiceUnavailable,
			wantProbeCalls:  1,
		},
		{
			name: "element aggregate capabilities are not answered",
			components: []sip.ElementComponent{
				&advCapsStub{caps: sip.ElementCapabilities{
					Methods: []sip.RequestMethod{sip.RequestMethodInvite},
					Options: []header.OptionTag{"x-other"},
				}},
			},
			wantStatus: sip.ResponseStatusOK,
			wantAllow:  header.Allow{sip.RequestMethodOptions},
		},
		{
			name:          "second element answers identically",
			secondElement: true,
			wantStatus:    sip.ResponseStatusOK,
			wantAllow:     header.Allow{sip.RequestMethodOptions},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := useragent.Options{
				DisableOptionsAutoAnswer: tt.disableAutoOpts,
			}
			if tt.onMessage {
				opts.OnMessage = noopReqHandler
			}
			if tt.onOptionsStatus.IsValid() {
				opts.OnOptions = func(ctx context.Context, in *useragent.IncomingMessage) error {
					return in.Respond(ctx, tt.onOptionsStatus)
				}
			}

			var probeCalls atomic.Int32
			fx := newWireFixture(t, opts)
			for _, comp := range tt.components {
				if unbind := fx.elm.UseComponent(comp); unbind == nil {
					t.Fatal("elm.UseComponent() unbind = nil, want non-nil")
				}
			}
			if tt.probe {
				// The probe sits behind the user agent in the inbound
				// chain: an unclaimed request must reach it instead of
				// being auto-answered.
				if unbind := fx.elm.UseComponent(sip.InboundRequestInterceptorFunc(
					func(ctx context.Context, next sip.RequestReceiver, req *sip.RequestEnvelope) error {
						probeCalls.Add(1)
						return next.RecvRequest(ctx, req)
					},
				)); unbind == nil {
					t.Fatal("elm.UseComponent() unbind = nil, want non-nil")
				}
			}

			w := fx.wireElement
			peer := fx.peer
			if tt.secondElement {
				// One user agent bound to several elements answers with
				// identical capabilities on each of them.
				w = newWireElem(t)
				if unbind := w.elm.UseComponent(fx.ua); unbind == nil {
					t.Fatal("elm.UseComponent() unbind = nil, want non-nil")
				}
				peer = newRawPeer(t)
			}

			sendReq(t, peer, w.addr, newOptionsReq(t, uriFor(w.addr)))

			res := readRes(t, peer)
			if got := res.Status(); got != tt.wantStatus {
				t.Fatalf("response status = %v, want %v", got, tt.wantStatus)
			}

			var allow header.Allow
			for _, h := range res.Headers().Get("Allow") {
				if a, ok := h.(header.Allow); ok {
					allow = append(allow, a...)
				}
			}
			if diff := cmp.Diff(tt.wantAllow, allow); diff != "" {
				t.Fatalf("Allow header mismatch (-want +got):\n%s", diff)
			}

			var supported header.Supported
			for _, h := range res.Headers().Get("Supported") {
				if s, ok := h.(header.Supported); ok {
					supported = append(supported, s...)
				}
			}
			if diff := cmp.Diff(header.Supported(nil), supported); diff != "" {
				t.Fatalf("Supported header mismatch (-want +got):\n%s", diff)
			}
			// Only non-empty capability headers render: the user agent
			// advertises methods alone, so the auto answer must not emit
			// bare Supported or Accept header lines.
			if got := res.Headers().Get("Supported"); len(got) != 0 {
				t.Fatalf("Supported headers = %v, want none", got)
			}
			if got := res.Headers().Get("Accept"); len(got) != 0 {
				t.Fatalf("Accept headers = %v, want none", got)
			}

			if got := probeCalls.Load(); got != tt.wantProbeCalls {
				t.Errorf("probe calls = %d, want %d", got, tt.wantProbeCalls)
			}
			expectNoDatagram(t, peer)
		})
	}
}
