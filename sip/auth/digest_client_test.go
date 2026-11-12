package auth_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/auth"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func newRetryReq(t *testing.T) *sip.Request {
	t.Helper()

	return &sip.Request{
		Method: sip.RequestMethod("REGISTER"),
		URI: &uri.SIP{
			User: uri.MakeUserInfo("alice"),
			Addr: uri.MakeHostAddr("example.com"),
		},
		Headers: make(sip.Headers).
			Set(header.Via{header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "UDP",
				Addr:      uri.MakeHostAddr("client.example.com"),
			}}).
			Set(&header.CSeq{SeqNum: 42, Method: sip.RequestMethod("REGISTER")}).
			Set(header.Route{header.RouteHop{URI: &uri.SIP{Addr: uri.MakeHostAddr("proxy.example.com")}}}),
		Body: []byte("payload"),
	}
}

func chalRes(status sip.ResponseStatus, hdrs sip.Headers) *sip.ResponseEnvelope {
	return sip.NewResponseEnvelope(&sip.Response{
		Status:  status,
		Reason:  status.Reason(),
		Headers: hdrs,
	})
}

func digestCredsOf(t *testing.T, msg *sip.Request, proxy bool) *header.DigestCredentials {
	t.Helper()

	var name sip.HeaderName = "Authorization"
	if proxy {
		name = "Proxy-Authorization"
	}
	for _, h := range msg.Headers.Get(name) {
		var creds *header.DigestCredentials
		switch hdr := h.(type) {
		case *header.Authorization:
			if hdr != nil {
				creds, _ = hdr.AuthCredentials.(*header.DigestCredentials)
			}
		case *header.ProxyAuthorization:
			if hdr != nil {
				creds, _ = hdr.AuthCredentials.(*header.DigestCredentials)
			}
		}
		if creds != nil {
			return creds
		}
	}
	return nil
}

func TestDigestClient_AuthenticateRequest(t *testing.T) {
	t.Parallel()

	provider := func(_ context.Context, req *sip.Request, ch *header.DigestChallenge, proxy bool) (string, string, error) {
		if proxy {
			return "proxy-user", "proxy-pass", nil
		}
		return "uas-user", "uas-pass", nil
	}
	newClient := func(t *testing.T) *auth.DigestClient {
		t.Helper()

		client, err := auth.NewDigestClient(auth.DigestClientOptions{
			Realm:       "example.com",
			Credentials: provider,
		})
		if err != nil {
			t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
		}
		return client
	}

	for _, status := range []sip.ResponseStatus{
		sip.ResponseStatusUnauthorized,
		sip.ResponseStatusProxyAuthenticationRequired,
	} {
		t.Run(fmt.Sprintf("aggregated %d applies both legs", status), func(t *testing.T) {
			t.Parallel()

			req := sip.NewRequestEnvelope(newRetryReq(t))
			beforeReq := req.Message()
			res := chalRes(
				status,
				make(sip.Headers).
					Set(&header.WWWAuthenticate{
						AuthChallenge: &header.DigestChallenge{
							Realm:     "example.com",
							Nonce:     "www-md5",
							Algorithm: "MD5",
						},
					}).
					Append(&header.WWWAuthenticate{
						AuthChallenge: &header.DigestChallenge{
							Realm:     "example.com",
							Nonce:     "www-sha",
							Algorithm: "SHA-256",
							Opaque:    "opaque-1",
						},
					}).
					Append(&header.WWWAuthenticate{AuthChallenge: &header.BearerChallenge{Realm: "example.com"}}).
					Append(&header.ProxyAuthenticate{
						AuthChallenge: &header.DigestChallenge{
							Realm:     "other.com",
							Nonce:     "foreign",
							Algorithm: "SHA-256",
						},
					}).
					Append(&header.ProxyAuthenticate{
						AuthChallenge: &header.DigestChallenge{
							Realm:     "example.com",
							Nonce:     "proxy-sha",
							Algorithm: "SHA-256",
						},
					}),
			)
			beforeRes := res.Message()

			// MD5 explicitly enabled so stronger-first selection is really
			// exercised: the wire offers MD5 first, SHA-256 must still win.
			client, err := auth.NewDigestClient(auth.DigestClientOptions{
				Realm:       "example.com",
				Credentials: provider,
				Algorithms:  []string{"MD5", "SHA-256"},
			})
			if err != nil {
				t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
			}
			if err := client.AuthenticateRequest(t.Context(), req, res); err != nil {
				t.Fatalf("client.AuthenticateRequest() error = %v, want nil", err)
			}

			msg := req.Message()
			www := digestCredsOf(t, msg, false)
			proxy := digestCredsOf(t, msg, true)
			if www == nil || proxy == nil {
				t.Fatal("expected both credential families applied")
			}
			if www.Username != "uas-user" || proxy.Username != "proxy-user" {
				t.Fatalf("leg usernames = %q/%q, want uas-user/proxy-user: auth legs conflated",
					www.Username,
					proxy.Username,
				)
			}
			if www.Algorithm != "SHA-256" || www.Opaque != "opaque-1" {
				t.Fatalf("www algorithm/opaque = %q/%q, want SHA-256/opaque-1: selection or echo broken",
					www.Algorithm,
					www.Opaque,
				)
			}
			if www.Nonce != "www-sha" || proxy.Nonce != "proxy-sha" {
				t.Fatalf("nonces = %q/%q, want www-sha/proxy-sha", www.Nonce, proxy.Nonce)
			}
			if www.Realm != "example.com" || proxy.Realm != "example.com" {
				t.Fatalf("realms = %q/%q, want example.com/example.com", www.Realm, proxy.Realm)
			}
			if www.NonceCount != 1 || proxy.NonceCount != 1 || www.CNonce == "" || proxy.CNonce == "" {
				t.Fatalf("nc/cnonce = %d,%q / %d,%q, want count 1 and nonempty cnonces",
					www.NonceCount, www.CNonce, proxy.NonceCount, proxy.CNonce)
			}
			if www.QOP != "auth" || proxy.QOP != "auth" {
				t.Fatalf("qop = %q/%q, want auth/auth", www.QOP, proxy.QOP)
			}
			if www.URIText() != "sip:alice@example.com" {
				t.Fatalf("digest URI = %q, want sip:alice@example.com", www.URIText())
			}
			if !www.IsValid() || !proxy.IsValid() {
				t.Fatal("applied credentials are not structurally valid")
			}

			// With the applied auth families removed, the whole request must
			// equal its pre-call snapshot (method, URI, Via, CSeq, routes,
			// body), and the response must be untouched.
			msg.Headers = msg.Headers.Delete("Authorization", "Proxy-Authorization")
			if diff := cmp.Diff(beforeReq, msg); diff != "" {
				t.Fatalf("non-auth request fields changed (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(beforeRes, res.Message()); diff != "" {
				t.Fatalf("response changed (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("407 proxy leg only", func(t *testing.T) {
		t.Parallel()

		req := sip.NewRequestEnvelope(newRetryReq(t))
		res := chalRes(
			sip.ResponseStatusProxyAuthenticationRequired,
			make(sip.Headers).Set(&header.ProxyAuthenticate{
				AuthChallenge: &header.DigestChallenge{
					Realm:     "example.com",
					Nonce:     "proxy-sha",
					Algorithm: "SHA-256",
				},
			}),
		)

		if err := newClient(t).AuthenticateRequest(t.Context(), req, res); err != nil {
			t.Fatalf("client.AuthenticateRequest() error = %v, want nil", err)
		}

		msg := req.Message()
		if www := digestCredsOf(t, msg, false); www != nil {
			t.Fatalf("unexpected WWW credentials %+v on proxy-only challenge", www)
		}
		proxy := digestCredsOf(t, msg, true)
		if proxy == nil || proxy.Username != "proxy-user" {
			t.Fatalf("proxy credentials = %+v, want proxy-user applied", proxy)
		}
	})

	t.Run("second provider failure leaves request unchanged", func(t *testing.T) {
		t.Parallel()

		const failErr errors.Error = "provider unavailable"
		req := sip.NewRequestEnvelope(newRetryReq(t))
		before := req.Message()
		res := chalRes(
			sip.ResponseStatusUnauthorized,
			make(sip.Headers).
				Set(&header.WWWAuthenticate{
					AuthChallenge: &header.DigestChallenge{
						Realm:     "example.com",
						Nonce:     "www-sha",
						Algorithm: "SHA-256",
					},
				}).
				Append(&header.ProxyAuthenticate{
					AuthChallenge: &header.DigestChallenge{
						Realm:     "example.com",
						Nonce:     "proxy-sha",
						Algorithm: "SHA-256",
					},
				}),
		)
		client, err := auth.NewDigestClient(auth.DigestClientOptions{
			Realm: "example.com",
			Credentials: func(_ context.Context, _ *sip.Request, _ *header.DigestChallenge, proxy bool) (string, string, error) {
				if proxy {
					return "", "", failErr
				}
				return "uas-user", "uas-pass", nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
		}

		err = client.AuthenticateRequest(t.Context(), req, res)
		if err == nil {
			t.Fatal("client.AuthenticateRequest() error = nil, want provider failure")
		}
		if !errors.Is(err, failErr) {
			t.Fatalf("error = %v, want it to identify provider failure %v", err, failErr)
		}
		if diff := cmp.Diff(before, req.Message()); diff != "" {
			t.Fatalf("request changed after failed preparation (-want +got):\n%s", diff)
		}
	})

	t.Run("no compatible challenge is unsupported", func(t *testing.T) {
		t.Parallel()

		providerCalls := 0
		client, err := auth.NewDigestClient(auth.DigestClientOptions{
			Realm: "example.com",
			Credentials: func(context.Context, *sip.Request, *header.DigestChallenge, bool) (string, string, error) {
				providerCalls++
				return "uas-user", "uas-pass", nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
		}

		res := chalRes(
			sip.ResponseStatusUnauthorized,
			make(sip.Headers).
				Set(&header.WWWAuthenticate{
					AuthChallenge: &header.DigestChallenge{
						Realm:     "other.com",
						Nonce:     "foreign",
						Algorithm: "SHA-256",
					},
				}).
				Append(&header.WWWAuthenticate{
					AuthChallenge: &header.BearerChallenge{Realm: "example.com"},
				}),
		)

		err = client.AuthenticateRequest(t.Context(), sip.NewRequestEnvelope(newRetryReq(t)), res)
		if err == nil {
			t.Fatal("client.AuthenticateRequest() error = nil, want unsupported challenge")
		}
		if !errors.Is(err, sip.ErrUnsupportedChallenge) {
			t.Fatalf("error = %v, want it to identify %v", err, sip.ErrUnsupportedChallenge)
		}
		if providerCalls != 0 {
			t.Fatalf("provider called %d times, want 0 for incompatible challenges", providerCalls)
		}
	})

	t.Run("malformed challenges never reach the provider", func(t *testing.T) {
		t.Parallel()

		providerCalls := 0
		client, err := auth.NewDigestClient(auth.DigestClientOptions{
			Realm: "example.com",
			Credentials: func(context.Context, *sip.Request, *header.DigestChallenge, bool) (string, string, error) {
				providerCalls++
				return "uas-user", "uas-pass", nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
		}

		req := sip.NewRequestEnvelope(newRetryReq(t))
		before := req.Message()
		res := chalRes(
			sip.ResponseStatusUnauthorized,
			make(sip.Headers).
				Set(&header.WWWAuthenticate{
					AuthChallenge: &header.DigestChallenge{
						Realm:     "example.com",
						Nonce:     "bad-qop",
						Algorithm: "SHA-256",
						QOP:       []string{"auth", "bad token"},
					},
				}).
				Append(&header.WWWAuthenticate{
					AuthChallenge: &header.DigestChallenge{
						Realm:     "example.com",
						Nonce:     "bad-domain",
						Algorithm: "SHA-256",
						Domain:    []uri.URI{&uri.SIP{}},
					},
				}).
				Append(&header.WWWAuthenticate{
					AuthChallenge: &header.DigestChallenge{
						Realm:     "example.com",
						Nonce:     "bad-params",
						Algorithm: "SHA-256",
						Params:    header.Values{"not a token": {"v"}},
					},
				}),
		)

		err = client.AuthenticateRequest(t.Context(), req, res)
		if err == nil {
			t.Fatal("client.AuthenticateRequest() error = nil, want unsupported challenge")
		}
		if !errors.Is(err, sip.ErrUnsupportedChallenge) {
			t.Fatalf("error = %v, want it to identify %v", err, sip.ErrUnsupportedChallenge)
		}
		if providerCalls != 0 {
			t.Fatalf("provider called %d times, want 0 for malformed challenges", providerCalls)
		}
		if diff := cmp.Diff(before, req.Message()); diff != "" {
			t.Fatalf("request changed for malformed challenges (-want +got):\n%s", diff)
		}
	})

	t.Run("valid challenge wins over malformed", func(t *testing.T) {
		t.Parallel()

		providerCalls := 0
		client, err := auth.NewDigestClient(auth.DigestClientOptions{
			Realm: "example.com",
			Credentials: func(context.Context, *sip.Request, *header.DigestChallenge, bool) (string, string, error) {
				providerCalls++
				return "uas-user", "uas-pass", nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
		}

		req := sip.NewRequestEnvelope(newRetryReq(t))
		res := chalRes(
			sip.ResponseStatusUnauthorized,
			make(sip.Headers).
				Set(&header.WWWAuthenticate{
					AuthChallenge: &header.DigestChallenge{
						Realm:     "example.com",
						Nonce:     "bad-qop",
						Algorithm: "SHA-256",
						QOP:       []string{"auth", "bad token"},
					},
				}).
				Append(&header.WWWAuthenticate{
					AuthChallenge: &header.DigestChallenge{
						Realm:     "example.com",
						Nonce:     "good-nonce",
						Algorithm: "SHA-256",
					},
				}),
		)

		if err := client.AuthenticateRequest(t.Context(), req, res); err != nil {
			t.Fatalf("client.AuthenticateRequest() error = %v, want nil", err)
		}
		if providerCalls != 1 {
			t.Fatalf("provider called %d times, want 1 for the valid challenge only", providerCalls)
		}
		if creds := digestCredsOf(t, req.Message(), false); creds == nil || creds.Nonce != "good-nonce" {
			t.Fatalf("credentials = %+v, want nonce good-nonce", creds)
		}
	})

	t.Run("typed nil headers and credentials are safe", func(t *testing.T) {
		t.Parallel()

		reqMsg := newRetryReq(t)
		name := sip.HeaderName("Authorization").Canonic()
		reqMsg.Headers[name] = append(reqMsg.Headers[name],
			(*header.Authorization)(nil),
			&header.Authorization{AuthCredentials: (*header.DigestCredentials)(nil)},
			&header.Authorization{AuthCredentials: &header.BearerCredentials{Token: "keep"}},
		)
		req := sip.NewRequestEnvelope(reqMsg)
		res := chalRes(
			sip.ResponseStatusUnauthorized,
			make(sip.Headers).Set(&header.WWWAuthenticate{
				AuthChallenge: &header.DigestChallenge{
					Realm:     "example.com",
					Nonce:     "www-sha",
					Algorithm: "SHA-256",
				},
			}),
		)

		if err := newClient(t).AuthenticateRequest(t.Context(), req, res); err != nil {
			t.Fatalf("client.AuthenticateRequest() error = %v, want nil", err)
		}

		msg := req.Message()
		hdrValues := msg.Headers.Get(name)
		if len(hdrValues) != 4 {
			t.Fatalf("Authorization entries = %d, want 3 preserved + 1 applied", len(hdrValues))
		}
		var bearerKept bool
		for _, h := range hdrValues {
			if authHdr, ok := h.(*header.Authorization); ok && authHdr != nil {
				if bearer, ok := authHdr.AuthCredentials.(*header.BearerCredentials); ok && bearer.Token == "keep" {
					bearerKept = true
				}
			}
		}
		if !bearerKept {
			t.Fatal("foreign Bearer credentials were not preserved")
		}
		creds := digestCredsOf(t, msg, false)
		if creds == nil || creds.Realm != "example.com" || creds.Username != "uas-user" {
			t.Fatalf("credentials = %+v, want applied example.com digest", creds)
		}
	})

	t.Run("empty username leaves request unchanged", func(t *testing.T) {
		t.Parallel()

		client, err := auth.NewDigestClient(auth.DigestClientOptions{
			Realm: "example.com",
			Credentials: func(context.Context, *sip.Request, *header.DigestChallenge, bool) (string, string, error) {
				return "", "pass", nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
		}
		req := sip.NewRequestEnvelope(newRetryReq(t))
		before := req.Message()
		res := chalRes(
			sip.ResponseStatusUnauthorized,
			make(sip.Headers).Set(&header.WWWAuthenticate{
				AuthChallenge: &header.DigestChallenge{
					Realm:     "example.com",
					Nonce:     "www-sha",
					Algorithm: "SHA-256",
				},
			}),
		)

		if err := client.AuthenticateRequest(t.Context(), req, res); err == nil {
			t.Fatal("client.AuthenticateRequest() error = nil, want empty username rejection")
		}
		if diff := cmp.Diff(before, req.Message()); diff != "" {
			t.Fatalf("request changed after invalid username (-want +got):\n%s", diff)
		}
	})

	t.Run("existing credentials for realm replaced, others preserved", func(t *testing.T) {
		t.Parallel()

		reqMsg := newRetryReq(t)
		reqMsg.Headers = reqMsg.Headers.
			Set(&header.Authorization{
				AuthCredentials: &header.DigestCredentials{
					Username: "stale",
					Realm:    "example.com",
					Nonce:    "old",
					URI:      &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
					Response: strings.Repeat("0", 32),
				},
			}).
			Append(&header.Authorization{
				AuthCredentials: &header.DigestCredentials{
					Username: "other",
					Realm:    "other.com",
					Nonce:    "other-nonce",
					URI:      &uri.SIP{Addr: uri.MakeHostAddr("other.com")},
					Response: strings.Repeat("1", 32),
				},
			}).
			Append(&header.Authorization{AuthCredentials: &header.BearerCredentials{Token: "token-1"}})
		req := sip.NewRequestEnvelope(reqMsg)
		res := chalRes(
			sip.ResponseStatusUnauthorized,
			make(sip.Headers).Set(&header.WWWAuthenticate{
				AuthChallenge: &header.DigestChallenge{
					Realm:     "example.com",
					Nonce:     "www-sha",
					Algorithm: "SHA-256",
				},
			}),
		)

		if err := newClient(t).AuthenticateRequest(t.Context(), req, res); err != nil {
			t.Fatalf("client.AuthenticateRequest() error = %v, want nil", err)
		}

		var realms, users []string
		var schemes []string
		for creds := range req.Message().Headers.AuthorizationCredentials() {
			if dc, ok := creds.(*header.DigestCredentials); ok {
				realms = append(realms, dc.Realm)
				users = append(users, dc.Username)
			}
			schemes = append(schemes, shared.Render(creds)[:6])
		}
		if len(realms) != 2 || realms[0] != "example.com" || realms[1] != "other.com" {
			t.Fatalf("digest realms after apply = %v, want [example.com other.com]", realms)
		}
		if users[0] != "uas-user" || users[1] != "other" {
			t.Fatalf("digest users = %v, want realm creds replaced, foreign preserved", users)
		}
		if diff := cmp.Diff([]string{"Digest", "Digest", "Bearer"}, schemes); diff != "" {
			t.Fatalf("schemes changed (-want +got):\n%s", diff)
		}
	})
}

func TestDigestClient_AuthenticateRequest_InputAndCancellation(t *testing.T) {
	t.Parallel()

	newClient := func(t *testing.T) *auth.DigestClient {
		t.Helper()

		client, err := auth.NewDigestClient(auth.DigestClientOptions{
			Realm: "example.com",
			Credentials: func(context.Context, *sip.Request, *header.DigestChallenge, bool) (string, string, error) {
				return "uas-user", "uas-pass", nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
		}
		return client
	}
	wwwChal := func() sip.Headers {
		return make(sip.Headers).Set(&header.WWWAuthenticate{
			AuthChallenge: &header.DigestChallenge{
				Realm:     "example.com",
				Nonce:     "www-sha",
				Algorithm: "SHA-256",
			},
		})
	}

	t.Run("nil inputs are ordinary errors", func(t *testing.T) {
		t.Parallel()

		client := newClient(t)
		res := chalRes(sip.ResponseStatusUnauthorized, wwwChal())

		for _, tc := range []struct {
			name string
			req  *sip.RequestEnvelope
			res  *sip.ResponseEnvelope
		}{
			{"nil request", nil, res},
			{"nil response", sip.NewRequestEnvelope(newRetryReq(t)), nil},
			{"empty request envelope", &sip.RequestEnvelope{}, res},
			{"empty response envelope", sip.NewRequestEnvelope(newRetryReq(t)), &sip.ResponseEnvelope{}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				if err := client.AuthenticateRequest(t.Context(), tc.req, tc.res); err == nil {
					t.Fatal("client.AuthenticateRequest() error = nil, want non-nil")
				}
			})
		}
	})

	t.Run("nil contained message", func(t *testing.T) {
		t.Parallel()

		client := newClient(t)
		res := chalRes(sip.ResponseStatusUnauthorized, wwwChal())
		req := &sip.RequestEnvelope{MessageEnvelope: &sip.MessageEnvelope[*sip.Request]{}}
		if err := client.AuthenticateRequest(t.Context(), req, res); err == nil {
			t.Fatal("client.AuthenticateRequest() error = nil, want non-nil")
		}
	})

	t.Run("invalid method and uri", func(t *testing.T) {
		t.Parallel()

		client := newClient(t)
		res := chalRes(sip.ResponseStatusUnauthorized, wwwChal())

		for _, tt := range []struct {
			name string
			req  *sip.Request
		}{
			{"missing method", &sip.Request{URI: &uri.SIP{Addr: uri.MakeHostAddr("example.com")}}},
			{"invalid method", &sip.Request{Method: "B@D", URI: &uri.SIP{Addr: uri.MakeHostAddr("example.com")}}},
			{"missing uri", &sip.Request{Method: "REGISTER"}},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				err := client.AuthenticateRequest(t.Context(), sip.NewRequestEnvelope(tt.req), res)
				if err == nil {
					t.Fatal("client.AuthenticateRequest() error = nil, want non-nil")
				}
			})
		}
	})

	t.Run("ack and cancel are not authenticated", func(t *testing.T) {
		t.Parallel()

		client := newClient(t)
		res := chalRes(sip.ResponseStatusUnauthorized, wwwChal())

		for _, method := range []sip.RequestMethod{sip.RequestMethodAck, sip.RequestMethodCancel} {
			t.Run(string(method), func(t *testing.T) {
				t.Parallel()

				req := newRetryReq(t)
				req.Method = method
				err := client.AuthenticateRequest(t.Context(), sip.NewRequestEnvelope(req), res)
				if err == nil {
					t.Fatal("client.AuthenticateRequest() error = nil, want method rejection")
				}
				if !errors.Is(err, sip.ErrRequestMethodNotAllowed) {
					t.Fatalf("error = %v, want it to identify %v", err, sip.ErrRequestMethodNotAllowed)
				}
			})
		}
	})

	t.Run("non-challenge response", func(t *testing.T) {
		t.Parallel()

		client := newClient(t)
		res := chalRes(sip.ResponseStatusOK, nil)
		if err := client.AuthenticateRequest(t.Context(), sip.NewRequestEnvelope(newRetryReq(t)), res); err == nil {
			t.Fatal("client.AuthenticateRequest() error = nil, want non-nil")
		}
	})

	t.Run("zero-value client", func(t *testing.T) {
		t.Parallel()

		var client auth.DigestClient
		res := chalRes(sip.ResponseStatusUnauthorized, wwwChal())
		if err := client.AuthenticateRequest(t.Context(), sip.NewRequestEnvelope(newRetryReq(t)), res); err == nil {
			t.Fatal("client.AuthenticateRequest() error = nil, want non-nil")
		}
		var nilClient *auth.DigestClient
		if err := nilClient.AuthenticateRequest(t.Context(), sip.NewRequestEnvelope(newRetryReq(t)), res); err == nil {
			t.Fatal("nilClient.AuthenticateRequest() error = nil, want non-nil")
		}
	})

	t.Run("transaction headers are not required", func(t *testing.T) {
		t.Parallel()

		client := newClient(t)
		// A bare request: no Via/CSeq/To/From at all.
		req := sip.NewRequestEnvelope(&sip.Request{
			Method: "REGISTER",
			URI:    &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("example.com")},
		})
		res := chalRes(sip.ResponseStatusUnauthorized, wwwChal())

		if err := client.AuthenticateRequest(t.Context(), req, res); err != nil {
			t.Fatalf("client.AuthenticateRequest() error = %v, want nil", err)
		}
		if digestCredsOf(t, req.Message(), false) == nil {
			t.Fatal("credentials not applied to headerless request")
		}
	})

	t.Run("nil header map allocated at commit", func(t *testing.T) {
		t.Parallel()

		client := newClient(t)
		req := sip.NewRequestEnvelope(&sip.Request{
			Method:  "REGISTER",
			URI:     &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("example.com")},
			Headers: nil,
		})
		res := chalRes(sip.ResponseStatusUnauthorized, wwwChal())

		if err := client.AuthenticateRequest(t.Context(), req, res); err != nil {
			t.Fatalf("client.AuthenticateRequest() error = %v, want nil", err)
		}
		if req.Message().Headers == nil {
			t.Fatal("header map not allocated on successful commit")
		}
		if digestCredsOf(t, req.Message(), false) == nil {
			t.Fatal("credentials not applied")
		}
	})

	t.Run("canceled context before call", func(t *testing.T) {
		t.Parallel()

		client := newClient(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		res := chalRes(sip.ResponseStatusUnauthorized, wwwChal())
		if err := client.AuthenticateRequest(ctx, sip.NewRequestEnvelope(newRetryReq(t)), res); err == nil {
			t.Fatal("client.AuthenticateRequest() error = nil, want cancellation")
		} else if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want it to identify %v", err, context.Canceled)
		}
	})

	t.Run("cancellation before commit leaves request unchanged", func(t *testing.T) {
		t.Parallel()

		started := make(chan struct{})
		release := make(chan struct{})
		client, err := auth.NewDigestClient(auth.DigestClientOptions{
			Realm: "example.com",
			Credentials: func(context.Context, *sip.Request, *header.DigestChallenge, bool) (string, string, error) {
				select {
				case <-started:
				default:
					close(started)
				}
				<-release
				return "uas-user", "uas-pass", nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
		}

		req := sip.NewRequestEnvelope(newRetryReq(t))
		before := req.Message()
		res := chalRes(sip.ResponseStatusUnauthorized, wwwChal())

		ctx, cancel := context.WithCancel(t.Context())
		errCh := make(chan error, 1)
		go func() {
			errCh <- client.AuthenticateRequest(ctx, req, res)
		}()

		<-started
		cancel()
		close(release)

		err = <-errCh
		if err == nil {
			t.Fatal("client.AuthenticateRequest() error = nil, want cancellation")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want it to identify %v", err, context.Canceled)
		}
		if diff := cmp.Diff(before, req.Message()); diff != "" {
			t.Fatalf("canceled call changed request (-want +got):\n%s", diff)
		}
	})

	t.Run("cancel while waiting for commit lock writes nothing", func(t *testing.T) {
		// The RWMutex envelope lock is not durably-blocking under
		// testing/synctest on this toolchain, so the parked commit cannot
		// be observed with synctest.Wait. Coordination here proves the
		// contract — a cancellation delivered while the commit waits for
		// the lock must leave the request untouched; the in-lock check
		// itself is pinned down by TestWithRequestCommit.
		t.Parallel()

		providerEntered := make(chan struct{})
		releaseProvider := make(chan struct{})
		client, err := auth.NewDigestClient(auth.DigestClientOptions{
			Realm: "example.com",
			Credentials: func(context.Context, *sip.Request, *header.DigestChallenge, bool) (string, string, error) {
				select {
				case <-providerEntered:
				default:
					close(providerEntered)
				}
				<-releaseProvider
				return "uas-user", "uas-pass", nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
		}

		req := sip.NewRequestEnvelope(newRetryReq(t))
		before := req.Message()
		res := chalRes(sip.ResponseStatusUnauthorized, wwwChal())

		ctx, cancel := context.WithCancel(t.Context())
		errCh := make(chan error, 1)
		go func() {
			errCh <- client.AuthenticateRequest(ctx, req, res)
		}()
		<-providerEntered

		// Hold the request lock so the caller parks inside the commit
		// section once the provider is released.
		lockHeld := make(chan struct{})
		releaseLock := make(chan struct{})
		go func() {
			req.WithMessage(func(*sip.Request) {
				close(lockHeld)
				<-releaseLock
			})
		}()
		<-lockHeld

		close(releaseProvider)
		cancel()
		close(releaseLock)

		err = <-errCh
		if err == nil {
			t.Fatal("client.AuthenticateRequest() error = nil, want cancellation")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want it to identify %v", err, context.Canceled)
		}
		if diff := cmp.Diff(before, req.Message()); diff != "" {
			t.Fatalf("canceled commit changed request (-want +got):\n%s", diff)
		}
	})
}

func TestDigestClient_AuthenticateRequest_CallbackIsolation(t *testing.T) {
	t.Parallel()

	t.Run("provider mutations do not affect applied credentials", func(t *testing.T) {
		t.Parallel()

		client, err := auth.NewDigestClient(auth.DigestClientOptions{
			Realm: "example.com",
			Credentials: func(_ context.Context, req *sip.Request, ch *header.DigestChallenge, _ bool) (string, string, error) {
				// Mutating the detached inputs must not change the selected
				// challenge or the request used for hashing and commit.
				ch.Nonce = "forged-nonce"
				ch.Opaque = "forged-opaque"
				ch.Realm = "forged-realm"
				req.URI = &uri.SIP{Addr: uri.MakeHostAddr("attacker.example.com")}
				req.Body = []byte("forged")
				req.Headers = req.Headers.Set(&header.Authorization{
					AuthCredentials: &header.DigestCredentials{
						Username: "forged",
						Realm:    "example.com",
						Nonce:    "forged",
						URI:      &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
						Response: strings.Repeat("f", 64),
					},
				})
				return "uas-user", "uas-pass", nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
		}

		req := sip.NewRequestEnvelope(newRetryReq(t))
		res := chalRes(
			sip.ResponseStatusUnauthorized,
			make(sip.Headers).Set(&header.WWWAuthenticate{AuthChallenge: &header.DigestChallenge{
				Realm: "example.com", Nonce: "www-sha", Algorithm: "SHA-256", Opaque: "opaque-1",
			}}),
		)

		if err := client.AuthenticateRequest(t.Context(), req, res); err != nil {
			t.Fatalf("client.AuthenticateRequest() error = %v, want nil", err)
		}

		msg := req.Message()
		creds := digestCredsOf(t, msg, false)
		if creds == nil {
			t.Fatal("credentials not applied")
		}
		if creds.Nonce != "www-sha" || creds.Opaque != "opaque-1" || creds.Realm != "example.com" {
			t.Fatalf(
				"credentials nonce/opaque/realm = %q/%q/%q, want www-sha/opaque-1/example.com: provider mutation leaked",
				creds.Nonce, creds.Opaque, creds.Realm,
			)
		}
		if creds.URIText() != "sip:alice@example.com" {
			t.Fatalf("digest URI = %q, want sip:alice@example.com", creds.URIText())
		}
		if string(msg.Body) != "payload" {
			t.Fatalf("body = %q, want payload", msg.Body)
		}
	})

	t.Run("provider can read envelopes and reenter the client", func(t *testing.T) {
		t.Parallel()

		var client *auth.DigestClient
		var origReq *sip.RequestEnvelope
		var origRes *sip.ResponseEnvelope
		reentrantReq := sip.NewRequestEnvelope(newRetryReq(t))
		var sawOriginalHeader bool
		client, err := auth.NewDigestClient(auth.DigestClientOptions{
			Realm: "example.com",
			Credentials: func(ctx context.Context, req *sip.Request, ch *header.DigestChallenge, proxy bool) (string, string, error) {
				if proxy {
					// Reentrant call through the same client: original
					// envelopes are still readable and unmutated, and no
					// session lock is held while we run.
					if err := client.AuthenticateRequest(ctx, reentrantReq, chalRes(
						sip.ResponseStatusUnauthorized,
						make(sip.Headers).Set(&header.WWWAuthenticate{
							AuthChallenge: &header.DigestChallenge{
								Realm:     "example.com",
								Nonce:     "reentrant-nonce",
								Algorithm: "SHA-256",
							},
						}),
					)); err != nil {
						return "", "", err
					}
					return "proxy-user", "proxy-pass", nil
				}
				// Reading the original envelopes from inside the provider
				// proves their locks are not held during callbacks.
				if origMsg := origReq.Message(); origMsg == nil || string(origMsg.Body) != "payload" {
					return "", "", errors.ErrorWrap("original request not readable in provider")
				}
				if origResMsg := origRes.Message(); origResMsg == nil || origResMsg.Status != sip.ResponseStatusUnauthorized {
					return "", "", errors.ErrorWrap("original response not readable in provider")
				}
				for creds := range req.Headers.AuthorizationCredentials() {
					_ = creds
					sawOriginalHeader = true
				}
				return "uas-user", "uas-pass", nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
		}

		reqMsg := newRetryReq(t)
		reqMsg.Headers = reqMsg.Headers.Set(&header.Authorization{AuthCredentials: &header.DigestCredentials{
			Username: "prior", Realm: "example.com", Nonce: "prior-nonce",
			URI: &uri.SIP{Addr: uri.MakeHostAddr("example.com")}, Response: strings.Repeat("0", 32),
		}})
		origReq = sip.NewRequestEnvelope(reqMsg)
		origRes = chalRes(
			sip.ResponseStatusUnauthorized,
			make(sip.Headers).
				Set(&header.WWWAuthenticate{
					AuthChallenge: &header.DigestChallenge{
						Realm:     "example.com",
						Nonce:     "www-sha",
						Algorithm: "SHA-256",
					},
				}).
				Append(&header.ProxyAuthenticate{
					AuthChallenge: &header.DigestChallenge{
						Realm:     "example.com",
						Nonce:     "proxy-sha",
						Algorithm: "SHA-256",
					},
				}),
		)

		if err := client.AuthenticateRequest(t.Context(), origReq, origRes); err != nil {
			t.Fatalf("client.AuthenticateRequest() error = %v, want nil", err)
		}
		if !sawOriginalHeader {
			t.Fatal("provider did not observe the original request credentials")
		}
		if creds := digestCredsOf(t, reentrantReq.Message(), false); creds == nil || creds.Nonce != "reentrant-nonce" {
			t.Fatalf("reentrant credentials = %+v, want nonce reentrant-nonce", creds)
		}
	})
}
