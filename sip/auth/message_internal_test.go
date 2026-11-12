package auth

import (
	"context"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/uri"
)

func TestClientMessages(t *testing.T) {
	t.Parallel()

	newRequest := func() *sip.Request {
		return &sip.Request{
			Method: "REGISTER",
			URI:    &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("example.com")},
		}
	}
	newResponse := func(status sip.ResponseStatus) *sip.Response {
		return &sip.Response{Status: status, Reason: status.Reason()}
	}

	t.Run("detached snapshots", func(t *testing.T) {
		t.Parallel()

		req := sip.NewRequestEnvelope(newRequest())
		res := sip.NewResponseEnvelope(newResponse(sip.ResponseStatusUnauthorized))

		reqMsg, resMsg, err := clientMsgs(t.Context(), req, res)
		if err != nil {
			t.Fatalf("auth.clientMessages() error = %v, want nil", err)
		}
		if reqMsg == nil || resMsg == nil {
			t.Fatal("auth.clientMessages() returned nil snapshot")
		}

		// Snapshots are clones: mutating them must not affect envelopes.
		reqMsg.Method = "INVITE"
		resMsg.Status = sip.ResponseStatusOK
		if got := req.Message(); got.Method != "REGISTER" {
			t.Fatalf("envelope method = %q, want REGISTER", got.Method)
		}
		if got := res.Message(); got.Status != sip.ResponseStatusUnauthorized {
			t.Fatalf("envelope status = %d, want 401", got.Status)
		}
	})

	t.Run("transaction headers not required", func(t *testing.T) {
		t.Parallel()

		req := sip.NewRequestEnvelope(newRequest())
		res := sip.NewResponseEnvelope(newResponse(sip.ResponseStatusProxyAuthenticationRequired))
		if _, _, err := clientMsgs(t.Context(), req, res); err != nil {
			t.Fatalf("auth.clientMessages() error = %v, want nil for bare request", err)
		}
	})

	t.Run("invalid inputs", func(t *testing.T) {
		t.Parallel()

		res := sip.NewResponseEnvelope(newResponse(sip.ResponseStatusUnauthorized))
		for _, tc := range []struct {
			name string
			req  *sip.RequestEnvelope
			res  *sip.ResponseEnvelope
		}{
			{"nil request envelope", nil, res},
			{"nil response envelope", sip.NewRequestEnvelope(newRequest()), nil},
			{"empty request envelope", &sip.RequestEnvelope{}, res},
			{"empty response envelope", sip.NewRequestEnvelope(newRequest()), &sip.ResponseEnvelope{}},
			{"nil contained message", &sip.RequestEnvelope{MessageEnvelope: &sip.MessageEnvelope[*sip.Request]{}}, res},
			{
				"invalid method",
				sip.NewRequestEnvelope(&sip.Request{Method: "B@D", URI: &uri.SIP{Addr: uri.MakeHostAddr("example.com")}}),
				res,
			},
			{"missing uri", sip.NewRequestEnvelope(&sip.Request{Method: "REGISTER"}), res},
			{
				"non-challenge status",
				sip.NewRequestEnvelope(newRequest()),
				sip.NewResponseEnvelope(newResponse(sip.ResponseStatusOK)),
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				if _, _, err := clientMsgs(t.Context(), tc.req, tc.res); err == nil {
					t.Fatal("auth.clientMessages() error = nil, want non-nil")
				}
			})
		}
	})

	t.Run("ack and cancel rejected", func(t *testing.T) {
		t.Parallel()

		res := sip.NewResponseEnvelope(newResponse(sip.ResponseStatusUnauthorized))
		for _, method := range []sip.RequestMethod{sip.RequestMethodAck, sip.RequestMethodCancel} {
			req := newRequest()
			req.Method = method
			_, _, err := clientMsgs(t.Context(), sip.NewRequestEnvelope(req), res)
			if err == nil {
				t.Fatalf("auth.clientMessages(%s) error = nil, want method rejection", method)
			}
			if !errors.Is(err, sip.ErrRequestMethodNotAllowed) {
				t.Fatalf("error = %v, want it to identify %v", err, sip.ErrRequestMethodNotAllowed)
			}
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, _, err := clientMsgs(ctx,
			sip.NewRequestEnvelope(newRequest()),
			sip.NewResponseEnvelope(newResponse(sip.ResponseStatusUnauthorized)),
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("auth.clientMessages() error = %v, want %v", err, context.Canceled)
		}
	})
}

func TestWithRequestCommit(t *testing.T) {
	t.Parallel()

	newEnvelope := func() *sip.RequestEnvelope {
		return sip.NewRequestEnvelope(&sip.Request{
			Method: "REGISTER",
			URI:    &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("example.com")},
		})
	}

	t.Run("commits under the envelope lock", func(t *testing.T) {
		t.Parallel()

		req := newEnvelope()
		var ran bool
		err := withReqCommit(t.Context(), req, func(msg *sip.Request) {
			ran = true
			msg.Body = []byte("committed")
		})
		if err != nil {
			t.Fatalf("auth.withRequestCommit() error = %v, want nil", err)
		}
		if !ran {
			t.Fatal("commit function did not run")
		}
		if got := req.Message().Body; string(got) != "committed" {
			t.Fatalf("body = %q, want committed mutation visible in envelope", got)
		}
	})

	t.Run("precanceled context commits nothing", func(t *testing.T) {
		t.Parallel()

		req := newEnvelope()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		var ran bool
		err := withReqCommit(ctx, req, func(*sip.Request) { ran = true })
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("auth.withRequestCommit() error = %v, want %v", err, context.Canceled)
		}
		if ran {
			t.Fatal("commit function ran for canceled context")
		}
	})

	t.Run("cancel while waiting for the lock commits nothing", func(t *testing.T) {
		t.Parallel()

		req := newEnvelope()
		lockHeld := make(chan struct{})
		releaseLock := make(chan struct{})
		go func() {
			req.WithMessage(func(*sip.Request) {
				close(lockHeld)
				<-releaseLock
			})
		}()
		<-lockHeld

		ctx, cancel := context.WithCancel(t.Context())
		started := make(chan struct{})
		errCh := make(chan error, 1)
		var ran bool
		go func() {
			close(started)
			errCh <- withReqCommit(ctx, req, func(*sip.Request) { ran = true })
		}()
		<-started

		// The commit is parked behind the held lock; cancellation must be
		// observed inside the lock, before fn can run.
		cancel()
		close(releaseLock)

		err := <-errCh
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("auth.withRequestCommit() error = %v, want %v", err, context.Canceled)
		}
		if ran {
			t.Fatal("commit function ran after cancellation")
		}
	})
}

func TestServerMessage(t *testing.T) {
	t.Parallel()

	t.Run("detached snapshot", func(t *testing.T) {
		t.Parallel()

		req := sip.NewRequestEnvelope(&sip.Request{
			Method: "REGISTER",
			URI:    &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("example.com")},
		})
		msg, err := serverMsg(t.Context(), req)
		if err != nil {
			t.Fatalf("auth.serverMessage() error = %v, want nil", err)
		}
		msg.Method = "INVITE"
		if got := req.Message(); got.Method != "REGISTER" {
			t.Fatalf("envelope method = %q, want REGISTER", got.Method)
		}
	})

	t.Run("invalid inputs", func(t *testing.T) {
		t.Parallel()

		if _, err := serverMsg(t.Context(), nil); err == nil {
			t.Fatal("auth.serverMessage(nil) error = nil, want non-nil")
		}
		if _, err := serverMsg(t.Context(), &sip.RequestEnvelope{}); err == nil {
			t.Fatal("auth.serverMessage(empty) error = nil, want non-nil")
		}
	})

	t.Run("ack rejected", func(t *testing.T) {
		t.Parallel()

		req := sip.NewRequestEnvelope(&sip.Request{
			Method: sip.RequestMethodAck,
			URI:    &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
		})
		if _, err := serverMsg(t.Context(), req); !errors.Is(err, sip.ErrRequestMethodNotAllowed) {
			t.Fatalf("auth.serverMessage(ACK) error = %v, want %v", err, sip.ErrRequestMethodNotAllowed)
		}
	})
}
