package auth_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/auth"
	"github.com/ghettovoice/gosip/sip/header"
)

func newBearerServer(t *testing.T, opts func(*auth.BearerServerOptions[testAccount])) *auth.BearerServer[testAccount] {
	t.Helper()

	options := auth.BearerServerOptions[testAccount]{
		ServerOptions: testAuthServerOpts(),
		AuthzServer:   testBearerASURI(t),
		Scope:         "write",
		ValidateToken: func(_ context.Context, _ *sip.Request, token string) (testAccount, error) {
			if token == "good-token" {
				return testAccount{ID: "account-1"}, nil
			}
			return testAccount{}, sip.ErrInvalidCredentials
		},
	}
	if opts != nil {
		opts(&options)
	}
	srv, err := auth.NewBearerServer(options)
	if err != nil {
		t.Fatalf("auth.NewBearerServer() error = %v, want nil", err)
	}
	return srv
}

func reqWithBearer(t *testing.T, tokens []string, proxy bool) *sip.RequestEnvelope {
	t.Helper()

	req := newAuthReq(t)
	req.WithMessage(func(msg *sip.Request) {
		for _, token := range tokens {
			if proxy {
				msg.Headers = msg.Headers.Append(&header.ProxyAuthorization{
					AuthCredentials: &header.BearerCredentials{Token: token},
				})
			} else {
				msg.Headers = msg.Headers.Append(&header.Authorization{
					AuthCredentials: &header.BearerCredentials{Token: token},
				})
			}
		}
	})
	return req
}

func bearerChalOf(t *testing.T, err error) *header.BearerChallenge {
	t.Helper()

	var rejection *sip.RequestRejectedError
	if !errors.As(err, &rejection) {
		t.Fatalf("error = %v, want *sip.RequestRejectedError", err)
	}
	var challenges []*header.BearerChallenge
	for _, hdrs := range []sip.HeaderName{"WWW-Authenticate", "Proxy-Authenticate"} {
		for _, hdr := range rejection.RespondOptions().ResponseOptions.Headers.Get(hdrs) {
			var ac header.AuthChallenge
			switch h := hdr.(type) {
			case *header.WWWAuthenticate:
				if h != nil {
					ac = h.AuthChallenge
				}
			case *header.ProxyAuthenticate:
				if h != nil {
					ac = h.AuthChallenge
				}
			}
			if bc, ok := ac.(*header.BearerChallenge); ok && bc != nil {
				challenges = append(challenges, bc)
			}
		}
	}
	if len(challenges) != 1 {
		t.Fatalf("bearer challenges = %d, want exactly one", len(challenges))
	}
	return challenges[0]
}

func testAuthServerOpts() auth.ServerOptions { return auth.ServerOptions{Realm: testRealm} }

func testAuthServerOptsP(proxy bool) auth.ServerOptions {
	opts := testAuthServerOpts()
	opts.Proxy = proxy
	return opts
}

func TestBearerServer_AuthenticateRequest(t *testing.T) {
	t.Parallel()

	t.Run("valid opaque token returns principal", func(t *testing.T) {
		t.Parallel()

		srv := newBearerServer(t, nil)
		principal, err := srv.AuthenticateRequest(t.Context(), reqWithBearer(t, []string{"good-token"}, false))
		if err != nil || principal.ID != "account-1" {
			t.Fatalf("principal = %+v, err = %v, want account-1", principal, err)
		}
	})

	t.Run("pointer principal", func(t *testing.T) {
		t.Parallel()

		srv, err := auth.NewBearerServer(auth.BearerServerOptions[*testAccount]{
			ServerOptions: testAuthServerOpts(),
			AuthzServer:   testBearerASURI(t),
			ValidateToken: func(_ context.Context, _ *sip.Request, token string) (*testAccount, error) {
				return &testAccount{ID: "account-1"}, nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewBearerServer() error = %v, want nil", err)
		}
		principal, err := srv.AuthenticateRequest(t.Context(), reqWithBearer(t, []string{"opaque-reference"}, false))
		if err != nil || principal == nil || principal.ID != "account-1" {
			t.Fatalf("principal = %+v, err = %v, want account-1", principal, err)
		}
	})

	t.Run("missing token challenges without error", func(t *testing.T) {
		t.Parallel()

		for _, tt := range []struct {
			name   string
			proxy  bool
			status sip.ResponseStatus
		}{
			{"uas", false, sip.ResponseStatusUnauthorized},
			{"proxy", true, sip.ResponseStatusProxyAuthenticationRequired},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				srv := newBearerServer(t, func(o *auth.BearerServerOptions[testAccount]) { o.Proxy = tt.proxy })
				principal, err := srv.AuthenticateRequest(t.Context(), newAuthReq(t))
				if principal != (testAccount{}) {
					t.Fatalf("principal = %+v, want zero on challenge", principal)
				}
				if !errors.Is(err, sip.ErrAuthenticationRequired) {
					t.Fatalf("error = %v, want it to identify %v", err, sip.ErrAuthenticationRequired)
				}
				var rejection *sip.RequestRejectedError
				if !errors.As(err, &rejection) || rejection.ResponseStatus() != tt.status {
					t.Fatalf("error = %v, want %d rejection", err, tt.status)
				}
				cln := bearerChalOf(t, err)
				if cln.Realm != testRealm {
					t.Fatalf("challenge realm = %q, want %q", cln.Realm, testRealm)
				}
				if shared.Render(cln.AuthzServer) != testTrustedAS {
					t.Fatalf("challenge AS = %q, want %q", shared.Render(cln.AuthzServer), testTrustedAS)
				}
				if cln.Scope != "write" {
					t.Fatalf("challenge scope = %q, want write", cln.Scope)
				}
				if cln.Error != "" {
					t.Fatalf("missing-token challenge error = %q, want none", cln.Error)
				}
			})
		}
	})

	t.Run("rejected token challenges with invalid_token", func(t *testing.T) {
		t.Parallel()

		srv := newBearerServer(t, nil)
		principal, err := srv.AuthenticateRequest(t.Context(), reqWithBearer(t, []string{"expired"}, false))
		if principal != (testAccount{}) {
			t.Fatalf("principal = %+v, want zero", principal)
		}
		if !errors.Is(err, sip.ErrAuthenticationRequired) || !errors.Is(err, sip.ErrInvalidCredentials) {
			t.Fatalf("error = %v, want joined auth-required + invalid-credentials", err)
		}
		if cln := bearerChalOf(t, err); cln.Error != "invalid_token" {
			t.Fatalf("challenge error = %q, want invalid_token", cln.Error)
		}
	})

	t.Run("malformed tokens never reach the validator", func(t *testing.T) {
		t.Parallel()

		var calls int
		srv := newBearerServer(t, func(o *auth.BearerServerOptions[testAccount]) {
			o.ValidateToken = func(_ context.Context, _ *sip.Request, token string) (testAccount, error) {
				calls++
				return testAccount{}, sip.ErrInvalidCredentials
			}
		})
		for _, token := range []string{"", "has space", "bad\r\ncrlf", "nön-ascii"} {
			_, err := srv.AuthenticateRequest(t.Context(), reqWithBearer(t, []string{token}, false))
			if !errors.Is(err, sip.ErrAuthenticationRequired) {
				t.Fatalf("token %q error = %v, want auth-required", token, err)
			}
			if cln := bearerChalOf(t, err); cln.Error != "invalid_token" {
				t.Fatalf("token %q challenge error = %q, want invalid_token", token, cln.Error)
			}
		}
		if calls != 0 {
			t.Fatalf("validator called %d times on malformed tokens, want 0", calls)
		}
	})

	t.Run("other schemes are ignored", func(t *testing.T) {
		t.Parallel()

		srv := newBearerServer(t, nil)
		req := newAuthReq(t)
		req.WithMessage(func(msg *sip.Request) {
			msg.Headers = msg.Headers.
				Append(&header.Authorization{AuthCredentials: &header.DigestCredentials{Realm: testRealm}}).
				Append(&header.Authorization{AuthCredentials: &header.AnyCredentials{Scheme: "Custom"}})
		})
		_, err := srv.AuthenticateRequest(t.Context(), req)
		if cln := bearerChalOf(t, err); cln.Error != "" {
			t.Fatalf("challenge error = %q, want none for absent bearer credentials", cln.Error)
		}
	})

	t.Run("invalid then valid candidate succeeds", func(t *testing.T) {
		t.Parallel()

		var seen []string
		srv := newBearerServer(t, func(o *auth.BearerServerOptions[testAccount]) {
			o.ValidateToken = func(_ context.Context, _ *sip.Request, token string) (testAccount, error) {
				seen = append(seen, token)
				if token == "good-token" {
					return testAccount{ID: "account-1"}, nil
				}
				return testAccount{}, sip.ErrInvalidCredentials
			}
		})
		principal, err := srv.AuthenticateRequest(t.Context(), reqWithBearer(t, []string{"expired", "good-token"}, false))
		if err != nil || principal.ID != "account-1" {
			t.Fatalf("principal = %+v, err = %v, want account-1", principal, err)
		}
		if diff := cmp.Diff([]string{"expired", "good-token"}, seen); diff != "" {
			t.Fatalf("validator calls (-want +got):\n%s", diff)
		}
	})

	t.Run("operational error stops before later candidates", func(t *testing.T) {
		t.Parallel()

		const outage errors.Error = "validator down"
		var calls int
		srv := newBearerServer(t, func(o *auth.BearerServerOptions[testAccount]) {
			o.ValidateToken = func(_ context.Context, _ *sip.Request, token string) (testAccount, error) {
				calls++
				if token == "down" {
					return testAccount{}, outage
				}
				return testAccount{ID: "account-1"}, nil
			}
		})
		principal, err := srv.AuthenticateRequest(t.Context(), reqWithBearer(t, []string{"down", "good-token"}, false))
		if principal != (testAccount{}) {
			t.Fatalf("principal = %+v, want zero", principal)
		}
		if !errors.Is(err, outage) || errors.Is(err, sip.ErrAuthenticationRequired) {
			t.Fatalf("error = %v, want it to identify %v only", err, outage)
		}
		var rejection *sip.RequestRejectedError
		if errors.As(err, &rejection) {
			t.Fatal("operational outage produced a challenge")
		}
		if calls != 1 {
			t.Fatalf("validator called %d times after operational stop, want 1", calls)
		}
	})

	t.Run("validator sees a fresh detached request per candidate", func(t *testing.T) {
		t.Parallel()

		var methods []sip.RequestMethod
		srv := newBearerServer(t, func(o *auth.BearerServerOptions[testAccount]) {
			o.ValidateToken = func(_ context.Context, req *sip.Request, token string) (testAccount, error) {
				methods = append(methods, req.Method)
				if token == "first" {
					req.Method = "INVITE" // mutation must not reach the next candidate or the original
				}
				if token == "good-token" {
					return testAccount{ID: "account-1"}, nil
				}
				return testAccount{}, sip.ErrInvalidCredentials
			}
		})
		req := reqWithBearer(t, []string{"first", "good-token"}, false)
		before := req.Message()
		principal, err := srv.AuthenticateRequest(t.Context(), req)
		if err != nil || principal.ID != "account-1" {
			t.Fatalf("principal = %+v, err = %v, want account-1", principal, err)
		}
		if diff := cmp.Diff([]sip.RequestMethod{"REGISTER", "REGISTER"}, methods); diff != "" {
			t.Fatalf("validator request methods (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(before, req.Message()); diff != "" {
			t.Fatalf("original request changed by validator mutation (-want +got):\n%s", diff)
		}
	})

	t.Run("zero and nil servers fail ordinarily", func(t *testing.T) {
		t.Parallel()

		var zero auth.BearerServer[testAccount]
		if _, err := zero.AuthenticateRequest(t.Context(), newAuthReq(t)); err == nil {
			t.Fatal("zero.AuthenticateRequest() error = nil, want non-nil")
		}
		var nilSrv *auth.BearerServer[testAccount]
		if _, err := nilSrv.AuthenticateRequest(t.Context(), newAuthReq(t)); err == nil {
			t.Fatal("nilSrv.AuthenticateRequest() error = nil, want non-nil")
		}
	})

	t.Run("ack and cancel are ordinary method errors", func(t *testing.T) {
		t.Parallel()

		var calls int
		srv := newBearerServer(t, func(o *auth.BearerServerOptions[testAccount]) {
			o.ValidateToken = func(_ context.Context, _ *sip.Request, token string) (testAccount, error) {
				calls++
				return testAccount{}, nil
			}
		})
		for _, method := range []sip.RequestMethod{sip.RequestMethodAck, sip.RequestMethodCancel} {
			req := reqWithBearer(t, []string{"good-token"}, false)
			req.WithMessage(func(msg *sip.Request) { msg.Method = method })
			_, err := srv.AuthenticateRequest(t.Context(), req)
			if !errors.Is(err, sip.ErrRequestMethodNotAllowed) {
				t.Fatalf("srv.AuthenticateRequest(%s) error = %v, want %v", method, err, sip.ErrRequestMethodNotAllowed)
			}
		}
		if calls != 0 {
			t.Fatalf("validator called %d times for ACK/CANCEL, want 0", calls)
		}
	})

	t.Run("opposite family is ignored", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			name     string
			proxy    bool
			credsLeg bool
			wantOK   bool
		}{
			{"uas ignores proxy tokens", false, true, false},
			{"proxy ignores uas tokens", true, false, false},
			{"proxy accepts proxy tokens", true, true, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				srv := newBearerServer(t, func(o *auth.BearerServerOptions[testAccount]) { o.Proxy = tc.proxy })
				principal, err := srv.AuthenticateRequest(t.Context(), reqWithBearer(t, []string{"good-token"}, tc.credsLeg))
				if tc.wantOK {
					if err != nil || principal.ID != "account-1" {
						t.Fatalf("principal = %+v, err = %v, want account-1", principal, err)
					}
					return
				}
				if principal != (testAccount{}) || !errors.Is(err, sip.ErrAuthenticationRequired) {
					t.Fatalf("principal = %+v, err = %v, want zero + auth-required", principal, err)
				}
			})
		}
	})

	t.Run("challenge carries no token material", func(t *testing.T) {
		t.Parallel()

		srv := newBearerServer(t, nil)
		_, err := srv.AuthenticateRequest(t.Context(), reqWithBearer(t, []string{"secret-token"}, false))
		if err == nil {
			t.Fatal("srv.AuthenticateRequest() error = nil, want challenge")
		}
		if strings.Contains(err.Error(), "secret-token") {
			t.Fatalf("challenge error %q contains the presented token", err.Error())
		}
	})

	t.Run("interface conformance", func(t *testing.T) {
		t.Parallel()

		var _ sip.ServerAuthenticator[testAccount] = newBearerServer(t, nil)
	})
}

func TestBearerServer_AuthenticateRequest_ValidatorCancellation(t *testing.T) {
	t.Parallel()

	t.Run("validator cancels and rejects", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		var calls int
		srv := newBearerServer(t, func(o *auth.BearerServerOptions[testAccount]) {
			o.ValidateToken = func(_ context.Context, _ *sip.Request, token string) (testAccount, error) {
				calls++
				cancel()
				return testAccount{ID: "account-1"}, errors.Join(sip.ErrInvalidCredentials)
			}
		})
		principal, err := srv.AuthenticateRequest(ctx, reqWithBearer(t, []string{"bad"}, false))
		if principal != (testAccount{}) {
			t.Fatalf("canceled validator released principal %+v", principal)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		var rejection *sip.RequestRejectedError
		if errors.As(err, &rejection) {
			t.Fatal("validator-canceled request produced a challenge")
		}
		if calls != 1 {
			t.Fatalf("validator called %d times after cancellation, want 1", calls)
		}
	})

	t.Run("canceled context with malformed token is ordinary", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		srv := newBearerServer(t, nil)
		principal, err := srv.AuthenticateRequest(ctx, reqWithBearer(t, []string{"has space"}, false))
		if principal != (testAccount{}) {
			t.Fatalf("canceled request released principal %+v", principal)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		var rejection *sip.RequestRejectedError
		if errors.As(err, &rejection) {
			t.Fatal("canceled request produced a challenge")
		}
	})
}

func TestBearerServer_AuthenticateRequest_ZeroPointerPrincipalOnError(t *testing.T) {
	t.Parallel()

	const outage errors.Error = "validator down"
	srv, err := auth.NewBearerServer(auth.BearerServerOptions[*testAccount]{
		ServerOptions: testAuthServerOpts(),
		AuthzServer:   testBearerASURI(t),
		ValidateToken: func(_ context.Context, _ *sip.Request, token string) (*testAccount, error) {
			if token == "down" {
				return &testAccount{ID: "leaked"}, outage
			}
			return &testAccount{ID: "leaked"}, sip.ErrInvalidCredentials
		},
	})
	if err != nil {
		t.Fatalf("auth.NewBearerServer() error = %v, want nil", err)
	}

	principal, err := srv.AuthenticateRequest(t.Context(), reqWithBearer(t, []string{"expired"}, false))
	if principal != nil {
		t.Fatalf("explicit invalid released principal %+v", principal)
	}
	if !errors.Is(err, sip.ErrAuthenticationRequired) {
		t.Fatalf("error = %v, want auth-required", err)
	}

	principal, err = srv.AuthenticateRequest(t.Context(), reqWithBearer(t, []string{"down"}, false))
	if principal != nil {
		t.Fatalf("operational failure released principal %+v", principal)
	}
	if !errors.Is(err, outage) {
		t.Fatalf("error = %v, want it to identify %v", err, outage)
	}
}
