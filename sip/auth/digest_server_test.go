package auth_test

import (
	"context"
	"crypto/md5" //nolint:gosec // Legacy MD5 is explicitly enabled for SIP Digest compatibility.
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/auth"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

type testAccount struct {
	ID string
}

func newAuthReq(t *testing.T) *sip.RequestEnvelope {
	t.Helper()

	return sip.NewRequestEnvelope(&sip.Request{
		Method: "REGISTER",
		URI:    &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("example.com")},
		Headers: make(sip.Headers).
			Set(header.Via{header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "UDP",
				Addr:      uri.MakeHostAddr("client.example.com"),
			}}).
			Set(&header.CSeq{SeqNum: 42, Method: "REGISTER"}),
	})
}

func newTestDigestServer(t *testing.T, proxy bool, algorithms []string) *auth.DigestServer[testAccount] {
	t.Helper()

	srv, err := auth.NewDigestServer(auth.DigestServerOptions[testAccount]{
		ServerOptions: testAuthServerOptsP(proxy),
		Algorithms:    algorithms,
		LookupHA1: func(context.Context, string, string, string) (string, testAccount, error) {
			return "", testAccount{}, sip.ErrInvalidCredentials
		},
	})
	if err != nil {
		t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
	}
	return srv
}

func chalsOf(t *testing.T, hdrs sip.Headers, proxy bool) []*header.DigestChallenge {
	t.Helper()

	var name sip.HeaderName = "WWW-Authenticate"
	if proxy {
		name = "Proxy-Authenticate"
	}
	var chals []*header.DigestChallenge
	for _, hdr := range hdrs.Get(name) {
		var cln *header.DigestChallenge
		switch h := hdr.(type) {
		case *header.WWWAuthenticate:
			if h != nil {
				cln, _ = h.AuthChallenge.(*header.DigestChallenge)
			}
		case *header.ProxyAuthenticate:
			if h != nil {
				cln, _ = h.AuthChallenge.(*header.DigestChallenge)
			}
		}
		if cln != nil {
			chals = append(chals, cln)
		}
	}
	return chals
}

func TestDigestServer_AuthenticateRequest_Challenges(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		proxy  bool
		status sip.ResponseStatus
	}{
		{"uas leg", false, sip.ResponseStatusUnauthorized},
		{"proxy leg", true, sip.ResponseStatusProxyAuthenticationRequired},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newTestDigestServer(t, tt.proxy, []string{"MD5", "SHA-256"})
			principal, err := srv.AuthenticateRequest(t.Context(), newAuthReq(t))
			if principal != (testAccount{}) {
				t.Fatalf("principal = %+v, want zero on challenge", principal)
			}
			if err == nil {
				t.Fatal("srv.AuthenticateRequest() error = nil, want challenge")
			}
			if !errors.Is(err, sip.ErrAuthenticationRequired) {
				t.Fatalf("error = %v, want it to identify %v", err, sip.ErrAuthenticationRequired)
			}
			var rejection *sip.RequestRejectedError
			if !errors.As(err, &rejection) {
				t.Fatalf("error = %T, want *sip.RequestRejectedError", err)
			}
			if rejection.ResponseStatus() != tt.status {
				t.Fatalf("status = %d, want %d", rejection.ResponseStatus(), tt.status)
			}
			if rejection.LogLevel() != slog.LevelDebug {
				t.Fatalf("level = %v, want Debug", rejection.LogLevel())
			}

			hdrs := rejection.RespondOptions().ResponseOptions.Headers
			chals := chalsOf(t, hdrs, tt.proxy)
			if len(chals) != 2 {
				t.Fatalf("challenges = %d, want one per enabled algorithm", len(chals))
			}
			if chals[0].Algorithm != "MD5" || chals[1].Algorithm != "SHA-256" {
				t.Fatalf("algorithms = %q/%q, want configured order MD5/SHA-256",
					chals[0].Algorithm, chals[1].Algorithm)
			}
			if chals[0].Nonce == "" || chals[0].Nonce == chals[1].Nonce {
				t.Fatalf("nonces = %q/%q, want distinct issued values", chals[0].Nonce, chals[1].Nonce)
			}
			for i, cln := range chals {
				if cln.Realm != "example.com" {
					t.Fatalf("challenge %d realm = %q, want example.com", i, cln.Realm)
				}
				if len(cln.QOP) != 1 || cln.QOP[0] != "auth" {
					t.Fatalf("challenge %d qop = %v, want [auth]", i, cln.QOP)
				}
				if cln.Stale {
					t.Fatalf("challenge %d stale = true, want false on fresh challenge", i)
				}
				if cln.Opaque != "" {
					t.Fatalf("challenge %d opaque = %q, want none issued", i, cln.Opaque)
				}
			}
			// The opposite family carries nothing.
			var oppositeName sip.HeaderName = "WWW-Authenticate"
			if !tt.proxy {
				oppositeName = "Proxy-Authenticate"
			}
			if got := chalsOf(t, hdrs, !tt.proxy); len(got) != 0 {
				t.Fatalf("opposite-family challenges = %v, want none under %s", got, oppositeName)
			}
		})
	}

	t.Run("zero principal on failure is exact zero", func(t *testing.T) {
		t.Parallel()

		srv, err := auth.NewDigestServer(auth.DigestServerOptions[*testAccount]{
			ServerOptions: testAuthServerOpts(),
			LookupHA1: func(context.Context, string, string, string) (string, *testAccount, error) {
				return "", nil, sip.ErrInvalidCredentials
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
		}
		principal, err := srv.AuthenticateRequest(t.Context(), newAuthReq(t))
		if principal != nil {
			t.Fatalf("principal = %+v, want nil", principal)
		}
		if err == nil {
			t.Fatal("srv.AuthenticateRequest() error = nil, want challenge")
		}
	})

	t.Run("zero-value and nil servers fail ordinarily", func(t *testing.T) {
		t.Parallel()

		var zero auth.DigestServer[testAccount]
		if _, err := zero.AuthenticateRequest(t.Context(), newAuthReq(t)); err == nil {
			t.Fatal("zero.AuthenticateRequest() error = nil, want non-nil")
		}
		var nilSrv *auth.DigestServer[testAccount]
		if _, err := nilSrv.AuthenticateRequest(t.Context(), newAuthReq(t)); err == nil {
			t.Fatal("nilSrv.AuthenticateRequest() error = nil, want non-nil")
		}
	})

	t.Run("ack and cancel are rejected without challenge", func(t *testing.T) {
		t.Parallel()

		srv := newTestDigestServer(t, false, nil)
		for _, method := range []sip.RequestMethod{sip.RequestMethodAck, sip.RequestMethodCancel} {
			req := newAuthReq(t)
			req.WithMessage(func(msg *sip.Request) { msg.Method = method })
			_, err := srv.AuthenticateRequest(t.Context(), req)
			if err == nil {
				t.Fatalf("srv.AuthenticateRequest(%s) error = nil, want method rejection", method)
			}
			if !errors.Is(err, sip.ErrRequestMethodNotAllowed) {
				t.Fatalf("error = %v, want it to identify %v", err, sip.ErrRequestMethodNotAllowed)
			}
			var rejection *sip.RequestRejectedError
			if errors.As(err, &rejection) {
				t.Fatalf("srv.AuthenticateRequest(%s) produced a challenge, want ordinary error", method)
			}
		}
	})

	t.Run("invalid inputs are ordinary errors", func(t *testing.T) {
		t.Parallel()

		srv := newTestDigestServer(t, false, nil)
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

				if _, err := srv.AuthenticateRequest(t.Context(), tt.req); err == nil {
					t.Fatal("srv.AuthenticateRequest() error = nil, want non-nil")
				}
			})
		}
	})

	t.Run("challenge carries no account material", func(t *testing.T) {
		t.Parallel()

		srv := newTestDigestServer(t, false, nil)
		_, err := srv.AuthenticateRequest(t.Context(), newAuthReq(t))
		if err == nil {
			t.Fatal("srv.AuthenticateRequest() error = nil, want challenge")
		}
		for _, secret := range []string{"alice", "password", "HA1"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("challenge error %q contains %q", err.Error(), secret)
			}
		}
	})

	t.Run("request unchanged after challenge", func(t *testing.T) {
		t.Parallel()

		req := newAuthReq(t)
		before := req.Message()
		srv := newTestDigestServer(t, false, nil)
		if _, err := srv.AuthenticateRequest(t.Context(), req); err == nil {
			t.Fatal("srv.AuthenticateRequest() error = nil, want challenge")
		}
		if diff := cmp.Diff(before, req.Message()); diff != "" {
			t.Fatalf("request changed by challenge (-want +got):\n%s", diff)
		}
	})
}

// refHexDigest is the independent test-side RFC 7616 calculation. It must
// stay standard-library only: production helpers must not be reused here
// or the vectors stop being independent.
func refHexDigest(algorithm string, parts ...string) string {
	var h hash.Hash
	switch strings.ToUpper(algorithm) {
	case "MD5":
		h = md5.New() //nolint:gosec // Legacy MD5 is explicitly enabled for SIP Digest compatibility.
	case "SHA-256":
		h = sha256.New()
	default:
		panic("unsupported ref algorithm")
	}
	for _, p := range parts {
		_, _ = io.WriteString(h, p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func refHA1(username, realm, password, algorithm string) string {
	return refHexDigest(algorithm, username, ":", realm, ":", password)
}

func refRes(ha1, method, uriText, nonce, cnonce string, count uint32, algorithm string) string {
	return refHexDigest(
		algorithm, ha1, ":", nonce, ":", fmt.Sprintf("%08x", count), ":", cnonce, ":auth:",
		refHexDigest(algorithm, method, ":", uriText),
	)
}

// chalNonce authenticates a credential-less request and extracts the
// issued nonce for the given algorithm.
func chalNonce[P any](t *testing.T, srv *auth.DigestServer[P], req *sip.RequestEnvelope, proxy bool, algorithm string) string {
	t.Helper()

	_, err := srv.AuthenticateRequest(t.Context(), req)
	var rejection *sip.RequestRejectedError
	if !errors.As(err, &rejection) {
		t.Fatalf("error = %v, want *sip.RequestRejectedError challenge", err)
	}
	for _, cln := range chalsOf(t, rejection.RespondOptions().ResponseOptions.Headers, proxy) {
		if cln.Algorithm == algorithm {
			return cln.Nonce
		}
	}
	t.Fatalf("no %s challenge issued", algorithm)
	return ""
}

// proofCreds builds a valid-by-construction credential for the
// request using the independent reference calculation.
func proofCreds( //nolint:revive // test helper mirrors the wire credential fields
	t *testing.T, req *sip.Request, credsURI uri.URI, username, password,
	nonce, cnonce, algorithm string, count uint,
) *header.DigestCredentials {
	t.Helper()

	creds := &header.DigestCredentials{
		Username:   username,
		Realm:      testRealm,
		Nonce:      nonce,
		URI:        credsURI,
		Algorithm:  algorithm,
		CNonce:     cnonce,
		QOP:        "auth",
		NonceCount: count,
	}
	ha1 := refHA1(username, testRealm, password, algorithm)
	creds.Response = refRes(ha1, string(req.Method), creds.URIText(), nonce, cnonce, uint32(count), algorithm)
	return creds
}

func reqWithDigest(t *testing.T, creds *header.DigestCredentials, proxy bool) *sip.RequestEnvelope {
	t.Helper()

	req := newAuthReq(t)
	req.WithMessage(func(msg *sip.Request) {
		if proxy {
			msg.Headers = msg.Headers.Append(&header.ProxyAuthorization{AuthCredentials: creds})
		} else {
			msg.Headers = msg.Headers.Append(&header.Authorization{AuthCredentials: creds})
		}
	})
	return req
}

const (
	testUser     = "alice"
	testPassword = "wonderland"
	testRealm    = "example.com"
)

// newVerifyingServer returns a server whose lookup answers HA1 for
// testUser only, using the same independent reference HA1.
func newVerifyingServer(t *testing.T, opts func(*auth.DigestServerOptions[testAccount])) *auth.DigestServer[testAccount] {
	t.Helper()

	options := auth.DigestServerOptions[testAccount]{
		ServerOptions: testAuthServerOpts(),
		LookupHA1: func(_ context.Context, username, realm, algorithm string) (string, testAccount, error) {
			if realm != testRealm {
				return "", testAccount{}, errors.Error("unexpected realm")
			}
			if username != testUser {
				return "", testAccount{}, sip.ErrInvalidCredentials
			}
			return refHA1(username, realm, testPassword, algorithm), testAccount{ID: "account-1"}, nil
		},
	}
	if opts != nil {
		opts(&options)
	}
	srv, err := auth.NewDigestServer(options)
	if err != nil {
		t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
	}
	return srv
}

func TestDigestServer_AuthenticateRequest(t *testing.T) {
	t.Parallel()

	reqMsg := func() *sip.Request {
		return &sip.Request{
			Method: "REGISTER",
			URI:    &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("example.com")},
		}
	}
	requestURI := func() *uri.SIP {
		return &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("example.com")}
	}

	t.Run("valid proof returns principal", func(t *testing.T) {
		t.Parallel()

		srv := newVerifyingServer(t, nil)
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		req := reqWithDigest(t,
			proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1),
			false,
		)

		principal, err := srv.AuthenticateRequest(t.Context(), req)
		if err != nil {
			t.Fatalf("srv.AuthenticateRequest() error = %v, want principal", err)
		}
		if principal.ID != "account-1" {
			t.Fatalf("principal = %+v, want account-1", principal)
		}
	})

	t.Run("pointer principal", func(t *testing.T) {
		t.Parallel()

		srv, err := auth.NewDigestServer(auth.DigestServerOptions[*testAccount]{
			ServerOptions: testAuthServerOpts(),
			LookupHA1: func(_ context.Context, username, realm, algorithm string) (string, *testAccount, error) {
				return refHA1(username, realm, testPassword, algorithm), &testAccount{ID: "account-1"}, nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
		}
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		req := reqWithDigest(t,
			proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1),
			false,
		)
		principal, err := srv.AuthenticateRequest(t.Context(), req)
		if err != nil || principal == nil || principal.ID != "account-1" {
			t.Fatalf("principal = %+v, err = %v, want account-1", principal, err)
		}
	})

	t.Run("explicit md5 and omitted wire algorithm", func(t *testing.T) {
		t.Parallel()

		srv := newVerifyingServer(t, func(o *auth.DigestServerOptions[testAccount]) {
			o.Algorithms = []string{"MD5"}
		})
		nonce := chalNonce(t, srv, newAuthReq(t), false, "MD5")
		creds := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "MD5", 1)
		creds.Algorithm = "" // omitted wire algorithm means MD5
		req := reqWithDigest(t, creds, false)
		if _, err := srv.AuthenticateRequest(t.Context(), req); err != nil {
			t.Fatalf("srv.AuthenticateRequest() error = %v, want principal", err)
		}
	})

	t.Run("md5 rejected when not enabled", func(t *testing.T) {
		t.Parallel()

		srv := newVerifyingServer(t, nil)
		creds := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, "invented", "cn-1", "MD5", 1)
		req := reqWithDigest(t, creds, false)
		if _, err := srv.AuthenticateRequest(t.Context(), req); err == nil {
			t.Fatal("srv.AuthenticateRequest() error = nil, want challenge")
		}
	})

	t.Run("challenge causes", func(t *testing.T) {
		t.Parallel()

		srv := newVerifyingServer(t, nil)
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		validCreds := func() *header.DigestCredentials {
			return proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1)
		}

		t.Run("bad proof is a fresh challenge", func(t *testing.T) {
			t.Parallel()

			creds := validCreds()
			creds.Response = strings.Repeat("0", 64)
			_, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, creds, false))
			if !errors.Is(err, sip.ErrAuthenticationRequired) {
				t.Fatalf("error = %v, want it to identify %v", err, sip.ErrAuthenticationRequired)
			}
			var rejection *sip.RequestRejectedError
			if !errors.As(err, &rejection) || rejection.ResponseStatus() != sip.ResponseStatusUnauthorized {
				t.Fatalf("error = %v, want 401 rejection", err)
			}
			for _, cln := range chalsOf(t, rejection.RespondOptions().ResponseOptions.Headers, false) {
				if cln.Stale {
					t.Fatal("bad proof produced stale challenge")
				}
			}
		})

		t.Run("absent user keeps negative cause", func(t *testing.T) {
			t.Parallel()

			creds := proofCreds(t, reqMsg(), requestURI(), "mallory", testPassword, nonce, "cn-2", "SHA-256", 1)
			_, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, creds, false))
			if !errors.Is(err, sip.ErrAuthenticationRequired) || !errors.Is(err, sip.ErrInvalidCredentials) {
				t.Fatalf("error = %v, want joined auth-required + invalid-credentials", err)
			}
		})

		t.Run("wrong realm is ignored", func(t *testing.T) {
			t.Parallel()

			creds := validCreds()
			creds.Realm = "other.com"
			_, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, creds, false))
			if !errors.Is(err, sip.ErrAuthenticationRequired) || errors.Is(err, sip.ErrInvalidCredentials) {
				t.Fatalf("error = %v, want plain auth-required", err)
			}
		})

		t.Run("case-sensitive realm", func(t *testing.T) {
			t.Parallel()

			creds := validCreds()
			creds.Realm = "Example.com"
			_, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, creds, false))
			if !errors.Is(err, sip.ErrAuthenticationRequired) {
				t.Fatalf("error = %v, want auth-required", err)
			}
		})

		for _, tt := range []struct {
			name   string
			mutate func(*header.DigestCredentials)
		}{
			{"wrong qop", func(c *header.DigestCredentials) { c.QOP = "auth-int" }},
			{"missing qop", func(c *header.DigestCredentials) { c.QOP = "" }},
			{"missing cnonce", func(c *header.DigestCredentials) { c.CNonce = "" }},
			{"zero count", func(c *header.DigestCredentials) { c.NonceCount = 0 }},
			{"invented nonce", func(c *header.DigestCredentials) { c.Nonce = "invented" }},
			{"invalid params", func(c *header.DigestCredentials) { c.Params = header.Values{"bad key": {"x"}} }},
			{"invalid uri", func(c *header.DigestCredentials) { c.URI = &uri.SIP{} }},
			{"nil uri", func(c *header.DigestCredentials) { c.URI = nil }},
			{"uppercase response", func(c *header.DigestCredentials) { c.Response = strings.ToUpper(c.Response) }},
			{"short response", func(c *header.DigestCredentials) { c.Response = "abcd" }},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				creds := validCreds()
				tt.mutate(creds)
				_, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, creds, false))
				if !errors.Is(err, sip.ErrAuthenticationRequired) {
					t.Fatalf("error = %v, want auth-required", err)
				}
			})
		}

		t.Run("count overflow", func(t *testing.T) {
			if strconv.IntSize < 64 {
				t.Skip("count > 0xffffffff is not representable")
			}
			t.Parallel()

			creds := validCreds()
			overflow := uint(math.MaxUint32)
			overflow++
			creds.NonceCount = overflow
			_, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, creds, false))
			if !errors.Is(err, sip.ErrAuthenticationRequired) {
				t.Fatalf("error = %v, want auth-required", err)
			}
		})
	})

	t.Run("invalid uri never reaches callbacks", func(t *testing.T) {
		t.Parallel()

		var validatorCalls, lookupCalls atomic.Int32
		srv := newVerifyingServer(t, func(o *auth.DigestServerOptions[testAccount]) {
			o.ValidateURI = func(context.Context, *sip.Request, uri.URI) error {
				validatorCalls.Add(1)
				return nil
			}
			o.LookupHA1 = func(_ context.Context, username, realm, algorithm string) (string, testAccount, error) {
				lookupCalls.Add(1)
				return refHA1(username, realm, testPassword, algorithm), testAccount{ID: "account-1"}, nil
			}
		})
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")

		for _, badURI := range []uri.URI{&uri.SIP{}, nil} {
			creds := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1)
			creds.URI = badURI
			principal, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, creds, false))
			if principal != (testAccount{}) {
				t.Fatalf("invalid URI %v released principal %+v", badURI, principal)
			}
			if !errors.Is(err, sip.ErrAuthenticationRequired) {
				t.Fatalf("invalid URI %v error = %v, want auth-required", badURI, err)
			}
		}
		if got := validatorCalls.Load(); got != 0 {
			t.Fatalf("ValidateURI called %d times on invalid URI, want 0", got)
		}
		if got := lookupCalls.Load(); got != 0 {
			t.Fatalf("LookupHA1 called %d times on invalid URI, want 0", got)
		}
	})

	t.Run("validator cancellation stops before lookup", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		var lookupCalls atomic.Int32
		srv := newVerifyingServer(t, func(o *auth.DigestServerOptions[testAccount]) {
			o.ValidateURI = func(context.Context, *sip.Request, uri.URI) error {
				cancel()
				return nil
			}
			o.LookupHA1 = func(_ context.Context, username, realm, algorithm string) (string, testAccount, error) {
				lookupCalls.Add(1)
				return refHA1(username, realm, testPassword, algorithm), testAccount{ID: "account-1"}, nil
			}
		})
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		creds := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1)
		principal, err := srv.AuthenticateRequest(ctx, reqWithDigest(t, creds, false))
		if principal != (testAccount{}) {
			t.Fatalf("canceled validator released principal %+v", principal)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		var rejection *sip.RequestRejectedError
		if errors.As(err, &rejection) {
			t.Fatal("canceled validator produced a challenge")
		}
		if got := lookupCalls.Load(); got != 0 {
			t.Fatalf("LookupHA1 called %d times after validator cancellation, want 0", got)
		}
	})

	t.Run("operational failures are ordinary errors", func(t *testing.T) {
		t.Parallel()

		const outage errors.Error = "backend unreachable"
		srv, err := auth.NewDigestServer(auth.DigestServerOptions[testAccount]{
			ServerOptions: testAuthServerOpts(),
			LookupHA1: func(context.Context, string, string, string) (string, testAccount, error) {
				return "", testAccount{}, outage
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
		}
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		creds := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1)
		_, err = srv.AuthenticateRequest(t.Context(), reqWithDigest(t, creds, false))
		if err == nil {
			t.Fatal("srv.AuthenticateRequest() error = nil, want outage")
		}
		if !errors.Is(err, outage) {
			t.Fatalf("error = %v, want it to identify %v", err, outage)
		}
		if errors.Is(err, sip.ErrAuthenticationRequired) {
			t.Fatal("operational outage disguised as challenge")
		}
		var rejection *sip.RequestRejectedError
		if errors.As(err, &rejection) {
			t.Fatal("operational outage produced a challenge response")
		}
	})

	t.Run("malformed backend ha1 is operational", func(t *testing.T) {
		t.Parallel()

		srv, err := auth.NewDigestServer(auth.DigestServerOptions[testAccount]{
			ServerOptions: testAuthServerOpts(),
			LookupHA1: func(context.Context, string, string, string) (string, testAccount, error) {
				return "ZZZZ-not-hex", testAccount{ID: "account-1"}, nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
		}
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		creds := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1)
		_, err = srv.AuthenticateRequest(t.Context(), reqWithDigest(t, creds, false))
		if err == nil {
			t.Fatal("srv.AuthenticateRequest() error = nil, want operational failure")
		}
		if errors.Is(err, sip.ErrAuthenticationRequired) {
			t.Fatal("malformed HA1 disguised as challenge")
		}
		var rejection *sip.RequestRejectedError
		if errors.As(err, &rejection) {
			t.Fatal("malformed HA1 produced a challenge response")
		}
	})

	t.Run("multiple candidates", func(t *testing.T) {
		t.Parallel()

		srv := newVerifyingServer(t, nil)
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		bad := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1)
		bad.Response = strings.Repeat("0", 64)
		good := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-2", "SHA-256", 2)
		req := newAuthReq(t)
		req.WithMessage(func(msg *sip.Request) {
			msg.Headers = msg.Headers.
				Append(&header.Authorization{AuthCredentials: bad}).
				Append(&header.Authorization{AuthCredentials: good})
		})
		principal, err := srv.AuthenticateRequest(t.Context(), req)
		if err != nil || principal.ID != "account-1" {
			t.Fatalf("principal = %+v, err = %v, want account-1", principal, err)
		}
	})

	t.Run("operational error stops candidate processing", func(t *testing.T) {
		t.Parallel()

		const outage errors.Error = "db down"
		lookedUp := make(map[string]int)
		srv, err := auth.NewDigestServer(auth.DigestServerOptions[testAccount]{
			ServerOptions: testAuthServerOpts(),
			LookupHA1: func(_ context.Context, username, _, algorithm string) (string, testAccount, error) {
				lookedUp[username]++
				if username == "down" {
					return "", testAccount{}, outage
				}
				return refHA1(username, testRealm, testPassword, algorithm), testAccount{ID: username}, nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
		}
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		downCreds := proofCreds(t, reqMsg(), requestURI(), "down", "x", nonce, "cn-1", "SHA-256", 1)
		goodCreds := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-2", "SHA-256", 2)
		req := newAuthReq(t)
		req.WithMessage(func(msg *sip.Request) {
			msg.Headers = msg.Headers.
				Append(&header.Authorization{AuthCredentials: downCreds}).
				Append(&header.Authorization{AuthCredentials: goodCreds})
		})
		if _, err := srv.AuthenticateRequest(t.Context(), req); !errors.Is(err, outage) {
			t.Fatalf("error = %v, want it to identify %v", err, outage)
		}
		if lookedUp[testUser] != 0 {
			t.Fatalf("lookup called %d times after operational stop", lookedUp[testUser])
		}
	})

	t.Run("forged high count never consumes replay state", func(t *testing.T) {
		t.Parallel()

		srv := newVerifyingServer(t, nil)
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")

		forged := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 0xffffffff)
		forged.Response = strings.Repeat("f", 64) // forged proof, forged count
		if _, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, forged, false)); err == nil {
			t.Fatal("forged proof authenticated")
		}

		// The legitimate count-1 proof still succeeds: the forged attempt
		// must not have advanced the nonce's accepted count.
		legit := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-2", "SHA-256", 1)
		principal, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, legit, false))
		if err != nil || principal.ID != "account-1" {
			t.Fatalf("legitimate count-1 proof rejected: %v", err)
		}
	})

	t.Run("reordered lower count is challenged", func(t *testing.T) {
		t.Parallel()

		srv := newVerifyingServer(t, nil)
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")

		high := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 7)
		if _, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, high, false)); err != nil {
			t.Fatalf("valid count-7 proof rejected: %v", err)
		}
		low := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-2", "SHA-256", 1)
		if _, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, low, false)); !errors.Is(err, sip.ErrAuthenticationRequired) {
			t.Fatalf("error = %v, want auth-required for reordered count", err)
		}
	})

	t.Run("cnonce change does not reset the count", func(t *testing.T) {
		t.Parallel()

		srv := newVerifyingServer(t, nil)
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")

		first := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1)
		if _, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, first, false)); err != nil {
			t.Fatalf("first proof rejected: %v", err)
		}
		second := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-2", "SHA-256", 2)
		if _, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, second, false)); err != nil {
			t.Fatalf("count-2 proof with new cnonce rejected: %v", err)
		}
		replay := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-3", "SHA-256", 1)
		if _, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, replay, false)); !errors.Is(err, sip.ErrAuthenticationRequired) {
			t.Fatalf("error = %v, want auth-required for regressed count", err)
		}
	})

	t.Run("username binding rejects swapped account", func(t *testing.T) {
		t.Parallel()

		srv := newVerifyingServer(t, func(o *auth.DigestServerOptions[testAccount]) {
			o.LookupHA1 = func(_ context.Context, username, realm, algorithm string) (string, testAccount, error) {
				return refHA1(username, realm, testPassword, algorithm), testAccount{ID: username}, nil
			}
		})
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")

		first := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1)
		if _, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, first, false)); err != nil {
			t.Fatalf("first proof rejected: %v", err)
		}
		// A different valid account must not reuse the bound nonce.
		second := proofCreds(t, reqMsg(), requestURI(), "bob", testPassword, nonce, "cn-2", "SHA-256", 2)
		if _, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, second, false)); !errors.Is(err, sip.ErrAuthenticationRequired) {
			t.Fatalf("error = %v, want auth-required for swapped username", err)
		}
	})

	t.Run("noncanonical signed uri and equivalent target", func(t *testing.T) {
		t.Parallel()

		srv := newVerifyingServer(t, nil)
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")

		// Parse credentials carrying a noncanonical signed URI spelling;
		// the hash must cover exactly that text.
		const signedURI = "sip:alice@example.com;z=9;a=1?b=2&a=1"
		rendered := fmt.Sprintf(
			`Authorization: Digest username="alice", realm="example.com", nonce="%s", uri="%s", `+
				`response="%s", algorithm=SHA-256, cnonce="cn-1", qop=auth, nc=00000001`,
			nonce,
			signedURI,
			refRes(refHA1(testUser, testRealm, testPassword, "SHA-256"), "REGISTER", signedURI, nonce, "cn-1", 1, "SHA-256"),
		)
		hdr, err := header.Parse(rendered)
		if err != nil {
			t.Fatalf("header.Parse() error = %v, want nil", err)
		}
		creds, ok := hdr.(*header.Authorization)
		if !ok {
			t.Fatalf("header.Parse() = %T, want *header.Authorization", hdr)
		}
		req := newAuthReq(t)
		req.WithMessage(func(msg *sip.Request) { msg.Headers = msg.Headers.Append(hdr) })
		// The request URI is the parsed typed URI — Equal holds
		// semantically while the signed text stays noncanonical.
		dc, _ := creds.AuthCredentials.(*header.DigestCredentials)
		req.WithMessage(func(msg *sip.Request) { msg.URI = dc.URI })

		principal, err := srv.AuthenticateRequest(t.Context(), req)
		if err != nil || principal.ID != "account-1" {
			t.Fatalf("principal = %+v, err = %v, want account-1 over noncanonical signed URI", principal, err)
		}
	})

	t.Run("equivalent differently spelled target", func(t *testing.T) {
		t.Parallel()

		srv := newVerifyingServer(t, nil)
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")

		// Host case differs: Equal is case-insensitive on host, while the
		// hash covers the credential's own URIText spelling.
		credsURI := &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("EXAMPLE.com")}
		creds := proofCreds(t, reqMsg(), credsURI, testUser, testPassword, nonce, "cn-1", "SHA-256", 1)
		req := reqWithDigest(t, creds, false)
		if _, err := srv.AuthenticateRequest(t.Context(), req); err != nil {
			t.Fatalf("equivalent target rejected: %v", err)
		}
	})

	t.Run("foreign target rejected by default", func(t *testing.T) {
		t.Parallel()

		srv := newVerifyingServer(t, nil)
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		foreign := &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("evil.example.com")}
		creds := proofCreds(t, reqMsg(), foreign, testUser, testPassword, nonce, "cn-1", "SHA-256", 1)
		_, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, creds, false))
		if !errors.Is(err, sip.ErrAuthenticationRequired) {
			t.Fatalf("error = %v, want auth-required for foreign target", err)
		}
	})

	t.Run("explicit uri validator controls forwarding", func(t *testing.T) {
		t.Parallel()

		trusted := &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("trusted.example.com")}
		srv := newVerifyingServer(t, func(o *auth.DigestServerOptions[testAccount]) {
			o.ValidateURI = func(_ context.Context, _ *sip.Request, target uri.URI) error {
				if target != nil && target.Equal(trusted) {
					return nil
				}
				return sip.ErrInvalidCredentials
			}
		})
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")

		accepted := proofCreds(t, reqMsg(), trusted, testUser, testPassword, nonce, "cn-1", "SHA-256", 1)
		if _, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, accepted, false)); err != nil {
			t.Fatalf("validator-accepted target rejected: %v", err)
		}
		denied := proofCreds(t,
			reqMsg(),
			&uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("untrusted.example.com")},
			testUser,
			testPassword,
			nonce,
			"cn-2",
			"SHA-256",
			2,
		)
		if _, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, denied, false)); !errors.Is(err, sip.ErrAuthenticationRequired) {
			t.Fatalf("error = %v, want auth-required for validator-denied target", err)
		}
	})

	t.Run("validator operational error is ordinary", func(t *testing.T) {
		t.Parallel()

		const outage errors.Error = "target policy offline"
		srv := newVerifyingServer(t, func(o *auth.DigestServerOptions[testAccount]) {
			o.ValidateURI = func(context.Context, *sip.Request, uri.URI) error { return outage }
		})
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		creds := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1)
		if _, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, creds, false)); !errors.Is(err, outage) {
			t.Fatalf("error = %v, want it to identify %v", err, outage)
		}
	})

	t.Run("validator mutating detached inputs cannot forge", func(t *testing.T) {
		t.Parallel()

		srv := newVerifyingServer(t, func(o *auth.DigestServerOptions[testAccount]) {
			o.ValidateURI = func(_ context.Context, req *sip.Request, target uri.URI) error {
				// Mutation of detached clones must not alter the selected
				// credential or the original request.
				req.Method = "INVITE"
				if s, ok := target.(*uri.SIP); ok {
					s.Addr = uri.MakeHostAddr("forged.example.com")
				}
				return nil
			}
		})
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		req := reqWithDigest(t,
			proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1),
			false,
		)
		before := req.Message()
		if _, err := srv.AuthenticateRequest(t.Context(), req); err != nil {
			t.Fatalf("srv.AuthenticateRequest() error = %v, want principal", err)
		}
		if diff := cmp.Diff(before, req.Message()); diff != "" {
			t.Fatalf("validator mutation leaked into original request (-want +got):\n%s", diff)
		}
	})

	t.Run("failed authentication leaves request unchanged", func(t *testing.T) {
		t.Parallel()

		srv := newVerifyingServer(t, nil)
		req := reqWithDigest(t,
			proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, "invented", "cn-1", "SHA-256", 1),
			false,
		)
		before := req.Message()
		if _, err := srv.AuthenticateRequest(t.Context(), req); err == nil {
			t.Fatal("srv.AuthenticateRequest() error = nil, want challenge")
		}
		if diff := cmp.Diff(before, req.Message()); diff != "" {
			t.Fatalf("request changed by failed auth (-want +got):\n%s", diff)
		}
	})

	t.Run("changed method invalidates the proof", func(t *testing.T) {
		t.Parallel()

		srv := newVerifyingServer(t, nil)
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		// The proof signs INVITE while the request is REGISTER.
		creds := proofCreds(t,
			&sip.Request{Method: "INVITE", URI: requestURI()},
			requestURI(),
			testUser,
			testPassword,
			nonce,
			"cn-1",
			"SHA-256",
			1,
		)
		_, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, creds, false))
		if !errors.Is(err, sip.ErrAuthenticationRequired) {
			t.Fatalf("error = %v, want auth-required", err)
		}
	})

	t.Run("wrong-issued-algorithm never reaches lookup", func(t *testing.T) {
		t.Parallel()

		var lookupCalls atomic.Int32
		srv := newVerifyingServer(t, func(o *auth.DigestServerOptions[testAccount]) {
			o.Algorithms = []string{"MD5", "SHA-256"}
			o.LookupHA1 = func(_ context.Context, username, realm, algorithm string) (string, testAccount, error) {
				lookupCalls.Add(1)
				return refHA1(username, realm, testPassword, algorithm), testAccount{ID: "account-1"}, nil
			}
		})
		md5Nonce := chalNonce(t, srv, newAuthReq(t), false, "MD5")
		// A correctly-signed SHA-256 proof over the nonce issued for MD5.
		creds := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, md5Nonce, "cn-1", "SHA-256", 1)
		_, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, creds, false))
		if !errors.Is(err, sip.ErrAuthenticationRequired) {
			t.Fatalf("error = %v, want auth-required", err)
		}
		if got := lookupCalls.Load(); got != 0 {
			t.Fatalf("LookupHA1 called %d times for a nonce of another algorithm, want 0", got)
		}
	})

	t.Run("username case is passed to lookup verbatim", func(t *testing.T) {
		t.Parallel()

		var gotUser atomic.Value
		srv := newVerifyingServer(t, func(o *auth.DigestServerOptions[testAccount]) {
			o.LookupHA1 = func(_ context.Context, username, realm, algorithm string) (string, testAccount, error) {
				gotUser.Store(username)
				if username != testUser {
					return "", testAccount{}, sip.ErrInvalidCredentials
				}
				return refHA1(username, realm, testPassword, algorithm), testAccount{ID: "account-1"}, nil
			}
		})
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		creds := proofCreds(t, reqMsg(), requestURI(), "ALICE", testPassword, nonce, "cn-1", "SHA-256", 1)
		_, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, creds, false))
		if !errors.Is(err, sip.ErrInvalidCredentials) {
			t.Fatalf("error = %v, want it to identify %v", err, sip.ErrInvalidCredentials)
		}
		if got := gotUser.Load(); got != "ALICE" {
			t.Fatalf("lookup username = %v, want verbatim ALICE", got)
		}
	})

	t.Run("opposite credential family is ignored", func(t *testing.T) {
		t.Parallel()

		for _, tt := range []struct {
			name      string
			proxy     bool
			credsLeg  bool
			wantError bool
		}{
			{"uas ignores proxy credentials", false, true, true},
			{"proxy ignores uas credentials", true, false, true},
			{"proxy accepts proxy credentials", true, true, false},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				srv := newVerifyingServer(t, func(o *auth.DigestServerOptions[testAccount]) {
					o.Proxy = tt.proxy
				})
				nonce := chalNonce(t, srv, newAuthReq(t), tt.proxy, "SHA-256")
				creds := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1)
				principal, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, creds, tt.credsLeg))
				if tt.wantError {
					if principal != (testAccount{}) || !errors.Is(err, sip.ErrAuthenticationRequired) {
						t.Fatalf("principal = %+v, err = %v, want zero + auth-required", principal, err)
					}
					return
				}
				if err != nil || principal.ID != "account-1" {
					t.Fatalf("principal = %+v, err = %v, want account-1", principal, err)
				}
			})
		}
	})

	t.Run("pointer principal is nil on bad proof and outage", func(t *testing.T) {
		t.Parallel()

		const outage errors.Error = "backend unreachable"
		srv, err := auth.NewDigestServer(auth.DigestServerOptions[*testAccount]{
			ServerOptions: testAuthServerOpts(),
			LookupHA1: func(_ context.Context, username, realm, algorithm string) (string, *testAccount, error) {
				if username == "down" {
					return "", nil, outage
				}
				return refHA1(username, realm, testPassword, algorithm), &testAccount{ID: "account-1"}, nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
		}
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")

		bad := proofCreds(t, reqMsg(), requestURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1)
		bad.Response = strings.Repeat("0", 64)
		principal, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, bad, false))
		if principal != nil || !errors.Is(err, sip.ErrAuthenticationRequired) {
			t.Fatalf("principal = %+v, err = %v, want nil + auth-required", principal, err)
		}

		down := proofCreds(t, reqMsg(), requestURI(), "down", "x", nonce, "cn-2", "SHA-256", 1)
		principal, err = srv.AuthenticateRequest(t.Context(), reqWithDigest(t, down, false))
		if principal != nil || !errors.Is(err, outage) {
			t.Fatalf("principal = %+v, err = %v, want nil + outage", principal, err)
		}
	})

	t.Run("interface conformance", func(t *testing.T) {
		t.Parallel()

		var _ sip.ServerAuthenticator[testAccount] = newVerifyingServer(t, nil)
		ptrSrv, err := auth.NewDigestServer(auth.DigestServerOptions[*testAccount]{
			ServerOptions: testAuthServerOpts(),
			LookupHA1: func(context.Context, string, string, string) (string, *testAccount, error) {
				return "", nil, sip.ErrInvalidCredentials
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
		}
		var _ sip.ServerAuthenticator[*testAccount] = ptrSrv
	})
}

// TestDigestExchange runs a real server challenge through a real client for
// each leg, including the explicitly enabled legacy MD5 scenario.
func TestDigestExchange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		proxy      bool
		algorithms []string
		wantAlgo   string
	}{
		{"uas default sha-256", false, nil, "SHA-256"},
		{"proxy default sha-256", true, nil, "SHA-256"},
		{"uas explicit md5", false, []string{"MD5"}, "MD5"},
		{"proxy explicit md5", true, []string{"MD5"}, "MD5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, err := auth.NewDigestServer(auth.DigestServerOptions[testAccount]{
				ServerOptions: testAuthServerOptsP(tt.proxy),
				Algorithms:    tt.algorithms,
				LookupHA1: func(_ context.Context, username, realm, algorithm string) (string, testAccount, error) {
					if username != "alice" {
						return "", testAccount{}, sip.ErrInvalidCredentials
					}
					ha1, err := auth.DigestHA1(username, realm, "exchange-password", algorithm)
					if err != nil {
						return "", testAccount{}, err
					}
					return ha1, testAccount{ID: username}, nil
				},
			})
			if err != nil {
				t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
			}
			cln, err := auth.NewDigestClient(auth.DigestClientOptions{
				Realm:      "example.com",
				Algorithms: tt.algorithms,
				Credentials: func(context.Context, *sip.Request, *header.DigestChallenge, bool) (string, string, error) {
					return "alice", "exchange-password", nil
				},
			})
			if err != nil {
				t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
			}

			req := newAuthReq(t)
			var rejected *sip.RequestRejectedError
			if _, err := srv.AuthenticateRequest(t.Context(), req); !errors.As(err, &rejected) {
				t.Fatalf("srv.AuthenticateRequest() error = %v, want *sip.RequestRejectedError", err)
			}
			res := chalRes(rejected.ResponseStatus(), rejected.RespondOptions().ResponseOptions.Headers)
			if err := cln.AuthenticateRequest(t.Context(), req, res); err != nil {
				t.Fatalf("cln.AuthenticateRequest() error = %v, want nil", err)
			}

			creds := digestCredsOf(t, req.Message(), tt.proxy)
			if creds.Algorithm != tt.wantAlgo {
				t.Fatalf("credential algorithm = %q, want %q", creds.Algorithm, tt.wantAlgo)
			}
			if creds.QOP != "auth" {
				t.Fatalf("credential qop = %q, want %q", creds.QOP, "auth")
			}
			p, err := srv.AuthenticateRequest(t.Context(), req)
			if err != nil {
				t.Fatalf("srv.AuthenticateRequest() error = %v, want nil", err)
			}
			if want := (testAccount{ID: "alice"}); p != want {
				t.Fatalf("principal = %+v, want %+v", p, want)
			}
		})
	}
}
