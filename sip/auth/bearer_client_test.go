package auth_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/auth"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

const testTrustedAS = "https://as.example/authorize"

func testBearerASURI(t *testing.T) uri.URI {
	t.Helper()

	u, err := uri.Parse(testTrustedAS)
	if err != nil {
		t.Fatalf("uri.Parse(%q) error = %v, want nil", testTrustedAS, err)
	}
	return u
}

func newBearerClient(t *testing.T, token func(context.Context, *sip.Request, *header.BearerChallenge, bool) (string, error)) *auth.BearerClient {
	t.Helper()

	cln, err := auth.NewBearerClient(auth.BearerClientOptions{
		Realm:       testRealm,
		AuthzServer: testBearerASURI(t),
		Token:       token,
	})
	if err != nil {
		t.Fatalf("auth.NewBearerClient() error = %v, want nil", err)
	}
	return cln
}

func bearerTokenOf(hdrs sip.Headers, name sip.HeaderName) []string {
	var tokens []string
	for _, hdr := range hdrs.Get(name) {
		var creds header.AuthCredentials
		switch h := hdr.(type) {
		case *header.Authorization:
			if h != nil {
				creds = h.AuthCredentials
			}
		case *header.ProxyAuthorization:
			if h != nil {
				creds = h.AuthCredentials
			}
		}
		if bc, ok := creds.(*header.BearerCredentials); ok && bc != nil {
			tokens = append(tokens, bc.Token)
		}
	}
	return tokens
}

func TestBearerClient_AuthenticateRequest_TrustedChallenges(t *testing.T) {
	t.Parallel()

	trusted := func() *header.BearerChallenge {
		return &header.BearerChallenge{Realm: testRealm, AuthzServer: testBearerASURI(t), Scope: "s1"}
	}

	for _, tt := range []struct {
		name   string
		status sip.ResponseStatus
	}{
		{"401", sip.ResponseStatusUnauthorized},
		{"407", sip.ResponseStatusProxyAuthenticationRequired},
	} {
		t.Run(tt.name+" applies both legs", func(t *testing.T) {
			t.Parallel()

			var calls []bool
			var gotScope, gotError []string
			cln := newBearerClient(t, func(_ context.Context, _ *sip.Request, cln *header.BearerChallenge, proxy bool) (string, error) {
				calls = append(calls, proxy)
				gotScope = append(gotScope, cln.Scope)
				gotError = append(gotError, cln.Error)
				if proxy {
					return "proxy-token", nil
				}
				return "uas-token", nil
			})
			wwwCln := trusted()
			wwwCln.Error = "invalid_token"
			proxyCln := trusted()
			proxyCln.Scope = "s2"
			res := chalRes(tt.status, make(sip.Headers).
				Set(&header.WWWAuthenticate{AuthChallenge: wwwCln}).
				Append(&header.ProxyAuthenticate{AuthChallenge: proxyCln}))
			req := sip.NewRequestEnvelope(newRetryReq(t))
			beforeRes := res.Message()

			if err := cln.AuthenticateRequest(t.Context(), req, res); err != nil {
				t.Fatalf("cln.AuthenticateRequest() error = %v, want nil", err)
			}
			msg := req.Message()
			if got := bearerTokenOf(msg.Headers, "Authorization"); len(got) != 1 || got[0] != "uas-token" {
				t.Fatalf("Authorization tokens = %v, want [uas-token]", got)
			}
			if got := bearerTokenOf(msg.Headers, "Proxy-Authorization"); len(got) != 1 || got[0] != "proxy-token" {
				t.Fatalf("Proxy-Authorization tokens = %v, want [proxy-token]", got)
			}
			if len(calls) != 2 || calls[0] != false || calls[1] != true {
				t.Fatalf("provider legs = %v, want [false true]", calls)
			}
			if gotScope[0] != "s1" || gotScope[1] != "s2" {
				t.Fatalf("forwarded scopes = %v, want [s1 s2]", gotScope)
			}
			if gotError[0] != "invalid_token" {
				t.Fatalf("forwarded error = %q, want invalid_token", gotError[0])
			}
			if diff := cmp.Diff(beforeRes, res.Message()); diff != "" {
				t.Fatalf("response changed by authentication (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("untrusted challenges never reach the provider", func(t *testing.T) {
		t.Parallel()

		for _, tt := range []struct {
			name      string
			realm     string
			as        string // empty keeps a missing AS
			malformed bool
		}{
			{"other realm", "other.com", testTrustedAS, false},
			{"case-only realm", "EXAMPLE.COM", testTrustedAS, false},
			{"other AS", testRealm, "https://other.example/authorize", false},
			{"missing AS", testRealm, "", false},
			{"http AS", testRealm, "http://as.example/authorize", false},
			{"hostless AS", testRealm, "https:opaque", false},
			{"userinfo AS", testRealm, "https://user@as.example/authorize", false},
			{"fragment AS", testRealm, "https://as.example/authorize#frag", false},
			{"default port AS", testRealm, "https://as.example:443/authorize", false},
			{"trailing slash AS", testRealm, testTrustedAS + "/", false},
			{"malformed params", testRealm, testTrustedAS, true},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				var calls int
				cln := newBearerClient(t, func(context.Context, *sip.Request, *header.BearerChallenge, bool) (string, error) {
					calls++
					return "token", nil
				})
				chal := &header.BearerChallenge{Realm: tt.realm}
				if tt.as != "" {
					as, err := uri.Parse(tt.as)
					if err != nil {
						t.Fatalf("uri.Parse(%q) error = %v, want nil", tt.as, err)
					}
					chal.AuthzServer = as
				}
				if tt.malformed {
					chal.Params = header.Values{"bad key": {"v"}}
				}
				res := chalRes(sip.ResponseStatusUnauthorized, make(sip.Headers).Set(&header.WWWAuthenticate{AuthChallenge: chal}))
				req := sip.NewRequestEnvelope(newRetryReq(t))
				err := cln.AuthenticateRequest(t.Context(), req, res)
				if !errors.Is(err, sip.ErrUnsupportedChallenge) {
					t.Fatalf("cln.AuthenticateRequest() error = %v, want it to identify %v", err, sip.ErrUnsupportedChallenge)
				}
				if calls != 0 {
					t.Fatalf("untrusted challenge reached provider %d times", calls)
				}
			})
		}
	})
}

func TestBearerClient_AuthenticateRequest_AtomicFailure(t *testing.T) {
	t.Parallel()

	trusted := func() *header.BearerChallenge {
		return &header.BearerChallenge{Realm: testRealm, AuthzServer: testBearerASURI(t)}
	}
	bothLegs := func(t *testing.T) *sip.ResponseEnvelope {
		t.Helper()
		return chalRes(
			sip.ResponseStatusUnauthorized,
			make(sip.Headers).
				Set(&header.WWWAuthenticate{AuthChallenge: trusted()}).
				Append(&header.ProxyAuthenticate{AuthChallenge: trusted()}),
		)
	}

	t.Run("second provider failure leaves request unchanged", func(t *testing.T) {
		t.Parallel()

		const outage errors.Error = "token service down"
		cln := newBearerClient(t, func(_ context.Context, _ *sip.Request, _ *header.BearerChallenge, proxy bool) (string, error) {
			if proxy {
				return "", outage
			}
			return "uas-token", nil
		})
		req := sip.NewRequestEnvelope(newRetryReq(t))
		res := bothLegs(t)
		before, beforeRes := req.Message(), res.Message()
		err := cln.AuthenticateRequest(t.Context(), req, res)
		if !errors.Is(err, outage) {
			t.Fatalf("cln.AuthenticateRequest() error = %v, want it to identify %v", err, outage)
		}
		if diff := cmp.Diff(before, req.Message()); diff != "" {
			t.Fatalf("request changed after provider failure (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(beforeRes, res.Message()); diff != "" {
			t.Fatalf("response changed after provider failure (-want +got):\n%s", diff)
		}
	})

	for _, tt := range []struct {
		name  string
		token string
	}{
		{"empty token", ""},
		{"space in token", "with space"},
		{"crlf token", "tok\r\nen"},
		{"non-ascii token", "tökén"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cln := newBearerClient(t, func(context.Context, *sip.Request, *header.BearerChallenge, bool) (string, error) {
				return tt.token, nil
			})
			req := sip.NewRequestEnvelope(newRetryReq(t))
			before := req.Message()
			err := cln.AuthenticateRequest(t.Context(), req, bothLegs(t))
			if err == nil {
				t.Fatalf("cln.AuthenticateRequest() accepted token %q, want error", tt.token)
			}
			if errors.Is(err, sip.ErrUnsupportedChallenge) {
				t.Fatalf("error = %v, want ordinary invalid-token error", err)
			}
			if diff := cmp.Diff(before, req.Message()); diff != "" {
				t.Fatalf("request changed after invalid token (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("provider mutations cannot alter originals", func(t *testing.T) {
		t.Parallel()

		req := sip.NewRequestEnvelope(newRetryReq(t))
		res := bothLegs(t)
		beforeReq, beforeRes := req.Message(), res.Message()
		cln := newBearerClient(t, func(_ context.Context, r *sip.Request, cln *header.BearerChallenge, proxy bool) (string, error) {
			r.Method = "INVITE"
			r.Headers = nil
			cln.Realm = "other.com"
			if anyURI, ok := cln.AuthzServer.(*uri.Any); ok {
				anyURI.Host = "forged.example"
			}
			cln.Scope = "forged"
			if proxy {
				return "proxy-token", nil
			}
			return "uas-token", nil
		})
		if err := cln.AuthenticateRequest(t.Context(), req, res); err != nil {
			t.Fatalf("cln.AuthenticateRequest() error = %v, want nil", err)
		}
		msg := req.Message()
		if got := bearerTokenOf(msg.Headers, "Authorization"); len(got) != 1 || got[0] != "uas-token" {
			t.Fatalf("Authorization tokens = %v, want [uas-token]", got)
		}
		if diff := cmp.Diff(stripAuthFamilies(beforeReq), stripAuthFamilies(msg)); diff != "" {
			t.Fatalf("request changed outside auth families (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(beforeRes, res.Message()); diff != "" {
			t.Fatalf("response changed by provider clones (-want +got):\n%s", diff)
		}
	})
}

func TestBearerClient_AuthenticateRequest_PreservesCredentials(t *testing.T) {
	t.Parallel()

	trusted := func() *header.BearerChallenge {
		return &header.BearerChallenge{Realm: testRealm, AuthzServer: testBearerASURI(t)}
	}
	response := func(t *testing.T) *sip.ResponseEnvelope {
		t.Helper()
		return chalRes(sip.ResponseStatusUnauthorized, make(sip.Headers).
			Set(&header.WWWAuthenticate{AuthChallenge: trusted()}).
			Append(&header.ProxyAuthenticate{AuthChallenge: trusted()}))
	}
	prepopulated := func(t *testing.T) *sip.RequestEnvelope {
		t.Helper()

		req := newRetryReq(t)
		req.Headers = req.Headers.
			Append(&header.ProxyAuthorization{AuthCredentials: &header.BearerCredentials{Token: "other-proxy-token"}}).
			Append(&header.Authorization{AuthCredentials: &header.DigestCredentials{Realm: "other.com", Username: "bob"}}).
			Append(&header.Authorization{AuthCredentials: &header.AnyCredentials{Scheme: "Custom"}})
		return sip.NewRequestEnvelope(req)
	}

	t.Run("existing values and ordering preserved", func(t *testing.T) {
		t.Parallel()

		cln := newBearerClient(t, func(_ context.Context, _ *sip.Request, _ *header.BearerChallenge, proxy bool) (string, error) {
			if proxy {
				return "our-proxy-token", nil
			}
			return "our-token", nil
		})
		req := prepopulated(t)
		before := req.Message()
		if err := cln.AuthenticateRequest(t.Context(), req, response(t)); err != nil {
			t.Fatalf("cln.AuthenticateRequest() error = %v, want nil", err)
		}
		msg := req.Message()

		if diff := cmp.Diff([]string{"our-token"}, bearerTokenOf(msg.Headers, "Authorization")); diff != "" {
			t.Fatalf("Authorization tokens (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"other-proxy-token", "our-proxy-token"}, bearerTokenOf(msg.Headers, "Proxy-Authorization")); diff != "" {
			t.Fatalf("Proxy-Authorization tokens (-want +got):\n%s", diff)
		}
		auths := msg.Headers.Get("Authorization")
		if len(auths) != 3 {
			t.Fatalf("Authorization headers = %d, want digest + custom + appended bearer", len(auths))
		}
		first, ok := auths[0].(*header.Authorization)
		if !ok {
			t.Fatal("existing Digest credential was reordered or replaced")
		}
		if _, ok := first.AuthCredentials.(*header.DigestCredentials); !ok {
			t.Fatal("existing Digest credential was replaced")
		}
		second, ok := auths[1].(*header.Authorization)
		if !ok {
			t.Fatal("existing Custom credential was reordered or replaced")
		}
		if anyCreds, ok := second.AuthCredentials.(*header.AnyCredentials); !ok || anyCreds.Scheme != "Custom" {
			t.Fatal("existing Custom credential was replaced")
		}
		if diff := cmp.Diff(stripAuthFamilies(before), stripAuthFamilies(msg)); diff != "" {
			t.Fatalf("non-auth request fields changed (-want +got):\n%s", diff)
		}
	})

	t.Run("typed-nil existing values are safe", func(t *testing.T) {
		t.Parallel()

		cln := newBearerClient(t, func(_ context.Context, _ *sip.Request, _ *header.BearerChallenge, proxy bool) (string, error) {
			if proxy {
				return "our-proxy-token", nil
			}
			return "our-token", nil
		})
		req := newRetryReq(t)
		var nilAuth *header.Authorization
		var nilProxy *header.ProxyAuthorization
		req.Headers = req.Headers.
			Append(nilAuth).
			Append(&header.Authorization{AuthCredentials: (*header.BearerCredentials)(nil)}).
			Append(nilProxy).
			Append(&header.ProxyAuthorization{AuthCredentials: (*header.BearerCredentials)(nil)})
		env := sip.NewRequestEnvelope(req)
		if err := cln.AuthenticateRequest(t.Context(), env, response(t)); err != nil {
			t.Fatalf("cln.AuthenticateRequest() error = %v, want nil", err)
		}
		msg := env.Message()
		if got := bearerTokenOf(msg.Headers, "Authorization"); len(got) != 1 || got[0] != "our-token" {
			t.Fatalf("Authorization tokens = %v, want [our-token]", got)
		}
		if got := bearerTokenOf(msg.Headers, "Proxy-Authorization"); len(got) != 1 || got[0] != "our-proxy-token" {
			t.Fatalf("Proxy-Authorization tokens = %v, want [our-proxy-token]", got)
		}
		if len(msg.Headers.Get("Authorization")) != 3 || len(msg.Headers.Get("Proxy-Authorization")) != 3 {
			t.Fatal("typed-nil entries were dropped during apply")
		}
	})

	t.Run("same token application is idempotent", func(t *testing.T) {
		t.Parallel()

		cln := newBearerClient(t, func(_ context.Context, _ *sip.Request, _ *header.BearerChallenge, proxy bool) (string, error) {
			if proxy {
				return "our-proxy-token", nil
			}
			return "our-token", nil
		})
		req := prepopulated(t)
		if err := cln.AuthenticateRequest(t.Context(), req, response(t)); err != nil {
			t.Fatalf("first cln.AuthenticateRequest() error = %v", err)
		}
		afterFirst := req.Message()
		if err := cln.AuthenticateRequest(t.Context(), req, response(t)); err != nil {
			t.Fatalf("second cln.AuthenticateRequest() error = %v", err)
		}
		afterSecond := req.Message()
		if diff := cmp.Diff(afterFirst.Headers, afterSecond.Headers); diff != "" {
			t.Fatalf("identical token application grew headers (-want +got):\n%s", diff)
		}
	})

	t.Run("changed token is appended alongside", func(t *testing.T) {
		t.Parallel()

		token := "token-1"
		cln := newBearerClient(t, func(_ context.Context, _ *sip.Request, _ *header.BearerChallenge, proxy bool) (string, error) {
			return token, nil
		})
		req := prepopulated(t)
		if err := cln.AuthenticateRequest(t.Context(), req, response(t)); err != nil {
			t.Fatalf("first cln.AuthenticateRequest() error = %v", err)
		}
		token = "token-2"
		if err := cln.AuthenticateRequest(t.Context(), req, response(t)); err != nil {
			t.Fatalf("second cln.AuthenticateRequest() error = %v", err)
		}
		got := bearerTokenOf(req.Message().Headers, "Authorization")
		if diff := cmp.Diff([]string{"token-1", "token-2"}, got); diff != "" {
			t.Fatalf("Authorization tokens (-want +got):\n%s", diff)
		}
	})
}

// stripAuthFamilies returns a clone without the two credential header
// families so everything else — method, URI, Via, CSeq, routing, body,
// unrelated headers — can be compared whole.
func stripAuthFamilies(msg *sip.Request) *sip.Request {
	cln, _ := msg.Clone().(*sip.Request)
	delete(cln.Headers, sip.HeaderName("Authorization"))
	delete(cln.Headers, sip.HeaderName("Proxy-Authorization"))
	return cln
}
