package auth_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/auth"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestBearerInputAndCancellation(t *testing.T) {
	t.Parallel()

	trustedRes := func(t *testing.T) *sip.ResponseEnvelope {
		t.Helper()
		return chalRes(
			sip.ResponseStatusUnauthorized,
			make(sip.Headers).
				Set(&header.WWWAuthenticate{
					AuthChallenge: &header.BearerChallenge{
						Realm:       testRealm,
						AuthzServer: testBearerASURI(t),
					},
				}),
		)
	}
	provider := func(context.Context, *sip.Request, *header.BearerChallenge, bool) (string, error) {
		return "token", nil
	}

	t.Run("client invalid inputs are ordinary errors", func(t *testing.T) {
		t.Parallel()

		cln := newBearerClient(t, provider)
		res := trustedRes(t)
		for _, tt := range []struct {
			name string
			req  *sip.RequestEnvelope
		}{
			{"nil envelope", nil},
			{"empty envelope", &sip.RequestEnvelope{}},
			{"missing method", sip.NewRequestEnvelope(&sip.Request{URI: &uri.SIP{Addr: uri.MakeHostAddr("example.com")}})},
			{"missing uri", sip.NewRequestEnvelope(&sip.Request{Method: "REGISTER"})},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				if err := cln.AuthenticateRequest(t.Context(), tt.req, res); err == nil {
					t.Fatal("cln.AuthenticateRequest() error = nil, want non-nil")
				}
			})
		}

		for _, method := range []sip.RequestMethod{sip.RequestMethodAck, sip.RequestMethodCancel} {
			req := sip.NewRequestEnvelope(newRetryReq(t))
			req.WithMessage(func(msg *sip.Request) { msg.Method = method })
			if err := cln.AuthenticateRequest(t.Context(), req, res); !errors.Is(err, sip.ErrRequestMethodNotAllowed) {
				t.Fatalf("cln.AuthenticateRequest(%s) error = %v, want %v", method, err, sip.ErrRequestMethodNotAllowed)
			}
		}

		t.Run("non-challenge response", func(t *testing.T) {
			t.Parallel()

			ok := chalRes(sip.ResponseStatusOK, nil)
			if err := cln.AuthenticateRequest(t.Context(), sip.NewRequestEnvelope(newRetryReq(t)), ok); err == nil {
				t.Fatal("cln.AuthenticateRequest() on 200 error = nil, want non-nil")
			}
		})

		t.Run("zero and nil clients", func(t *testing.T) {
			t.Parallel()

			var zero auth.BearerClient
			if err := zero.AuthenticateRequest(t.Context(), sip.NewRequestEnvelope(newRetryReq(t)), res); err == nil {
				t.Fatal("zero.AuthenticateRequest() error = nil, want non-nil")
			}
			var nilCln *auth.BearerClient
			if err := nilCln.AuthenticateRequest(t.Context(), sip.NewRequestEnvelope(newRetryReq(t)), res); err == nil {
				t.Fatal("nilCln.AuthenticateRequest() error = nil, want non-nil")
			}
		})

		t.Run("no transaction headers or header map needed", func(t *testing.T) {
			t.Parallel()

			req := sip.NewRequestEnvelope(&sip.Request{
				Method: "REGISTER",
				URI:    &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("example.com")},
			})
			if err := cln.AuthenticateRequest(t.Context(), req, res); err != nil {
				t.Fatalf("cln.AuthenticateRequest() error = %v, want nil", err)
			}
			if got := bearerTokenOf(req.Message().Headers, "Authorization"); len(got) != 1 || got[0] != "token" {
				t.Fatalf("Authorization tokens = %v, want [token]", got)
			}
		})
	})

	t.Run("client cancellation during provider commits nothing", func(t *testing.T) {
		t.Parallel()

		entered := make(chan struct{})
		release := make(chan struct{})
		var blocking atomic.Bool
		blocking.Store(true)
		cln := newBearerClient(t, func(context.Context, *sip.Request, *header.BearerChallenge, bool) (string, error) {
			if blocking.Swap(false) {
				close(entered)
				<-release
			}
			return "token", nil
		})
		req := sip.NewRequestEnvelope(newRetryReq(t))
		before := req.Message()

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- cln.AuthenticateRequest(ctx, req, trustedRes(t)) }()
		<-entered
		cancel()
		close(release)
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cln.AuthenticateRequest() error = %v, want context.Canceled", err)
		}
		if diff := cmp.Diff(before, req.Message()); diff != "" {
			t.Fatalf("request changed after cancellation (-want +got):\n%s", diff)
		}
	})

	t.Run("server cancellation during validator releases no principal", func(t *testing.T) {
		t.Parallel()

		entered := make(chan struct{})
		release := make(chan struct{})
		var blocking atomic.Bool
		blocking.Store(true)
		srv := newBearerServer(t, func(o *auth.BearerServerOptions[testAccount]) {
			o.ValidateToken = func(_ context.Context, _ *sip.Request, token string) (testAccount, error) {
				if blocking.Swap(false) {
					close(entered)
					<-release
				}
				return testAccount{ID: "account-1"}, nil
			}
		})
		req := reqWithBearer(t, []string{"good-token"}, false)

		ctx, cancel := context.WithCancel(t.Context())
		type outcome struct {
			p   testAccount
			err error
		}
		done := make(chan outcome, 1)
		go func() {
			p, err := srv.AuthenticateRequest(ctx, req)
			done <- outcome{p, err}
		}()
		<-entered
		cancel()
		close(release)
		got := <-done
		if got.p != (testAccount{}) {
			t.Fatalf("canceled request released principal %+v", got.p)
		}
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", got.err)
		}

		// The same token still validates under a fresh context.
		principal, err := srv.AuthenticateRequest(t.Context(), reqWithBearer(t, []string{"good-token"}, false))
		if err != nil || principal.ID != "account-1" {
			t.Fatalf("retry after cancellation: principal = %+v, err = %v, want account-1", principal, err)
		}
	})

	t.Run("server invalid inputs are ordinary errors", func(t *testing.T) {
		t.Parallel()

		srv := newBearerServer(t, nil)
		for _, tc := range []struct {
			name string
			req  *sip.RequestEnvelope
		}{
			{"nil envelope", nil},
			{"empty envelope", &sip.RequestEnvelope{}},
			{"missing method", sip.NewRequestEnvelope(&sip.Request{URI: &uri.SIP{Addr: uri.MakeHostAddr("example.com")}})},
			{"missing uri", sip.NewRequestEnvelope(&sip.Request{Method: "REGISTER"})},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				if _, err := srv.AuthenticateRequest(t.Context(), tc.req); err == nil {
					t.Fatal("srv.AuthenticateRequest() error = nil, want non-nil")
				}
			})
		}
	})
}

func TestBearerReentrantCallbacks(t *testing.T) {
	t.Parallel()

	t.Run("client provider", func(t *testing.T) {
		t.Parallel()

		var reqEnv *sip.RequestEnvelope
		var resEnv *sip.ResponseEnvelope
		var reenterErr, envErr atomic.Value
		var cln *auth.BearerClient
		cln = newBearerClient(t, func(_ context.Context, _ *sip.Request, _ *header.BearerChallenge, proxy bool) (string, error) {
			if msg := reqEnv.Message(); msg == nil {
				envErr.Store(errors.Error("request envelope read returned nil"))
			}
			if msg := resEnv.Message(); msg == nil {
				envErr.Store(errors.Error("response envelope read returned nil"))
			}
			err := cln.AuthenticateRequest(t.Context(),
				sip.NewRequestEnvelope(newRetryReq(t)),
				chalRes(
					sip.ResponseStatusUnauthorized,
					make(sip.Headers).Set(&header.WWWAuthenticate{
						AuthChallenge: &header.BearerChallenge{
							Realm:       "other.com",
							AuthzServer: testBearerASURI(t),
						},
					}),
				),
			)
			if err == nil {
				reenterErr.Store(errors.Error("reentrant call succeeded"))
			} else if !errors.Is(err, sip.ErrUnsupportedChallenge) {
				reenterErr.Store(err)
			}
			if proxy {
				return "proxy-token", nil
			}
			return "uas-token", nil
		})
		resEnv = chalRes(
			sip.ResponseStatusUnauthorized,
			make(sip.Headers).
				Set(&header.WWWAuthenticate{
					AuthChallenge: &header.BearerChallenge{
						Realm:       testRealm,
						AuthzServer: testBearerASURI(t),
					},
				}).
				Append(&header.ProxyAuthenticate{
					AuthChallenge: &header.BearerChallenge{
						Realm:       testRealm,
						AuthzServer: testBearerASURI(t),
					},
				}),
		)
		reqEnv = sip.NewRequestEnvelope(newRetryReq(t))
		if err := cln.AuthenticateRequest(t.Context(), reqEnv, resEnv); err != nil {
			t.Fatalf("cln.AuthenticateRequest() error = %v, want nil", err)
		}
		if v := envErr.Load(); v != nil {
			t.Fatalf("envelope read inside provider: %v", v)
		}
		if v := reenterErr.Load(); v != nil {
			t.Fatalf("reentrant call inside provider: %v", v)
		}
	})

	t.Run("server validator", func(t *testing.T) {
		t.Parallel()

		var reqEnv *sip.RequestEnvelope
		var reenterErr, envErr atomic.Value
		var srv *auth.BearerServer[testAccount]
		srv = newBearerServer(t, func(o *auth.BearerServerOptions[testAccount]) {
			o.ValidateToken = func(_ context.Context, _ *sip.Request, token string) (testAccount, error) {
				if msg := reqEnv.Message(); msg == nil {
					envErr.Store(errors.Error("envelope read returned nil"))
				}
				_, err := srv.AuthenticateRequest(t.Context(), newAuthReq(t))
				if err == nil {
					reenterErr.Store(errors.Error("reentrant call succeeded"))
				} else if !errors.Is(err, sip.ErrAuthenticationRequired) {
					reenterErr.Store(err)
				}
				return testAccount{ID: "account-1"}, nil
			}
		})
		reqEnv = reqWithBearer(t, []string{"good-token"}, false)
		principal, err := srv.AuthenticateRequest(t.Context(), reqEnv)
		if err != nil || principal.ID != "account-1" {
			t.Fatalf("principal = %+v, err = %v, want account-1", principal, err)
		}
		if v := envErr.Load(); v != nil {
			t.Fatalf("envelope read inside validator: %v", v)
		}
		if v := reenterErr.Load(); v != nil {
			t.Fatalf("reentrant call inside validator: %v", v)
		}
	})
}
