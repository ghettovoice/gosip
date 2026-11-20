package useragent_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/auth"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/useragent"
)

// digestReceiverFixture bundles a loopback element, the digest server
// authenticator guarding its inbound MESSAGE requests, and the call
// records the exchange asserts on.
type digestReceiverFixture struct {
	*wireElement
	srv *auth.DigestServer[string]

	// sendCount counts handler invocations, which equals wire sends:
	// each logical request registers its own server transaction, and the
	// transaction layer absorbs retransmissions before dispatch.
	sendCount   atomic.Int32
	lookupCalls atomic.Int32
	lookupUser  atomic.Value // string
	principal   atomic.Value // string
}

// onMsg is the receiving user agent's MESSAGE handler. It
// authenticates the inbound request with the digest server: a typed
// [sip.RequestRejectedError] challenge is answered through its carried
// status and respond options, an operational failure propagates, and a
// valid proof is answered 200 with the resolved principal recorded.
func (d *digestReceiverFixture) onMsg(ctx context.Context, in *useragent.IncomingMessage) error {
	d.sendCount.Add(1)

	principal, err := d.srv.AuthenticateRequest(ctx, in.Request())
	if err != nil {
		var rej *sip.RequestRejectedError
		if errors.As(err, &rej) {
			return in.Respond(ctx, rej.ResponseStatus(), rej.RespondOptions().ResponseOptions)
		}
		return errors.Wrap(err)
	}
	d.principal.Store(principal)
	return in.Respond(ctx, sip.ResponseStatusOK)
}

// newDigestReceiver builds a wire element and a user agent registered on
// it whose OnMessage handler authenticates every inbound MESSAGE through
// an RFC 7616 digest server for realm ua.test. The lookup counts calls,
// records the requested username, and resolves alice's principal for the
// SHA-256 HA1 of password "secret".
func newDigestReceiver(tb testing.TB) *digestReceiverFixture {
	tb.Helper()

	d := &digestReceiverFixture{wireElement: newWireElem(tb)}
	srv, err := auth.NewDigestServer(auth.DigestServerOptions[string]{
		Realm: "ua.test",
		LookupHA1: func(_ context.Context, username, _, _ string) (string, string, error) {
			d.lookupCalls.Add(1)
			d.lookupUser.Store(username)
			ha1, err := auth.DigestHA1("alice", "ua.test", "secret", "SHA-256")
			if err != nil {
				return "", "", errors.Wrap(err)
			}
			return ha1, "alice", nil
		},
	})
	if err != nil {
		tb.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
	}
	d.srv = srv

	bob, err := sip.ParseURI("sip:bob@ua.test")
	if err != nil {
		tb.Fatalf("sip.ParseURI() error = %v, want nil", err)
	}
	uaB, err := useragent.New(useragent.Options{
		AOR:       bob,
		OnMessage: d.onMsg,
	})
	if err != nil {
		tb.Fatalf("useragent.New() error = %v, want nil", err)
	}
	if unbind := d.elm.UseComponent(uaB); unbind == nil {
		tb.Fatal("elm.UseComponent() unbind = nil, want non-nil")
	}
	return d
}

// newDigestClient builds the client digest authenticator for realm
// ua.test whose credentials callback resolves account alice with the
// given password.
func newDigestClient(tb testing.TB, password string) *auth.DigestClient {
	tb.Helper()

	cli, err := auth.NewDigestClient(auth.DigestClientOptions{
		Realm: "ua.test",
		Credentials: func(
			context.Context,
			*sip.Request,
			*header.DigestChallenge,
			bool,
		) (string, string, error) {
			return "alice", password, nil
		},
	})
	if err != nil {
		tb.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
	}
	return cli
}

func TestUserAgent_DigestExchange(t *testing.T) {
	t.Parallel()

	t.Run("valid credentials complete the challenge", func(t *testing.T) {
		t.Parallel()

		rcv := newDigestReceiver(t)
		fx := newAuthSender(t, newDigestClient(t, "secret"))

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, uriFor(rcv.addr), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if res.Status() != sip.ResponseStatusOK {
			t.Fatalf("final status = %v, want %v", res.Status(), sip.ResponseStatusOK)
		}

		// The exchange is exactly two wire sends: the anonymous initial
		// MESSAGE answered with a 401 challenge, and the credentialed
		// retry answered 200.
		if sends := rcv.sendCount.Load(); sends != 2 {
			t.Fatalf("wire MESSAGE count = %d, want 2", sends)
		}
		if got := rcv.principal.Load(); got != "alice" {
			t.Fatalf("rcv.principal = %q, want alice", got)
		}
		if calls := rcv.lookupCalls.Load(); calls != 1 {
			t.Fatalf("LookupHA1 calls = %d, want 1", calls)
		}
		if got := rcv.lookupUser.Load(); got != "alice" {
			t.Errorf("LookupHA1 username = %q, want alice", got)
		}
	})

	t.Run("wrong password stays challenged to the bound", func(t *testing.T) {
		t.Parallel()

		rcv := newDigestReceiver(t)
		fx := newAuthSender(t, newDigestClient(t, "wrong-secret"))

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, uriFor(rcv.addr), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		// Every proof mismatches the stored HA1, so the server keeps
		// challenging and the retry chain terminates at its bound: the
		// outcome is the classified limit error with the last 401.
		res, err := op.Wait(t.Context())
		if !errors.Is(err, useragent.ErrAuthenticationLimited) {
			t.Fatalf("op.Wait() error = %v, want %v", err, useragent.ErrAuthenticationLimited)
		}
		if res == nil || res.Status() != sip.ResponseStatusUnauthorized {
			t.Fatalf("op.Wait() response = %v, want last 401 challenge alongside the error", res)
		}

		// The default bound of 3 authentication attempts means the
		// initial send plus three retries.
		if sends := rcv.sendCount.Load(); sends != 4 {
			t.Fatalf("wire MESSAGE count = %d, want %d", sends, 4)
		}
	})
}
