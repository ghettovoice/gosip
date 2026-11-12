package auth_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/auth"
	"github.com/ghettovoice/gosip/uri"
)

func TestDigestServer_AuthenticateRequest_ConcurrencyAndExpiry(t *testing.T) {
	t.Parallel()

	reqURI := func() *uri.SIP {
		return &uri.SIP{User: uri.MakeUserInfo("alice"), Addr: uri.MakeHostAddr("example.com")}
	}
	reqMsg := func() *sip.Request {
		return &sip.Request{Method: "REGISTER", URI: reqURI()}
	}

	t.Run("duplicate valid proofs commit once", func(t *testing.T) {
		t.Parallel()

		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		srv, err := auth.NewDigestServer(auth.DigestServerOptions[testAccount]{
			ServerOptions: testAuthServerOpts(),
			LookupHA1: func(_ context.Context, username, realm, algorithm string) (string, testAccount, error) {
				entered <- struct{}{}
				<-release // hold both callers so commits truly race
				return refHA1(username, realm, testPassword, algorithm), testAccount{ID: "account-1"}, nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
		}
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		req := func() *sip.RequestEnvelope {
			return reqWithDigest(t,
				proofCreds(t, reqMsg(), reqURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1),
				false,
			)
		}

		type result struct {
			principal testAccount
			err       error
		}
		results := make(chan result, 2)
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				p, err := srv.AuthenticateRequest(t.Context(), req())
				results <- result{p, err}
			})
		}
		// Both lookups must actually be inside the callback before the
		// release, otherwise the commits could serialize at lookup time.
		<-entered
		<-entered
		close(release)
		wg.Wait()
		close(results)

		var successes, failures int
		for r := range results {
			switch r.err {
			case nil:
				successes++
				if r.principal.ID != "account-1" {
					t.Fatalf("winning principal = %+v, want account-1", r.principal)
				}
			default:
				failures++
				if r.principal != (testAccount{}) {
					t.Fatalf("losing race released principal %+v, want zero", r.principal)
				}
				if !errors.Is(r.err, sip.ErrAuthenticationRequired) {
					t.Fatalf("losing race error = %v, want auth-required", r.err)
				}
			}
		}
		if successes != 1 || failures != 1 {
			t.Fatalf("duplicate proofs: %d successes, %d failures, want exactly one each", successes, failures)
		}
	})

	t.Run("reentrant reads inside lookup", func(t *testing.T) {
		t.Parallel()

		var authReq *sip.RequestEnvelope
		var reenterErr, envErr atomic.Value
		var srv *auth.DigestServer[testAccount]
		srv, err := auth.NewDigestServer(auth.DigestServerOptions[testAccount]{
			ServerOptions: testAuthServerOpts(),
			LookupHA1: func(_ context.Context, username, realm, algorithm string) (string, testAccount, error) {
				// Reading the envelope under authentication inside the
				// callback must not deadlock: no envelope lock may be
				// held here.
				if msg := authReq.Message(); msg == nil {
					envErr.Store(errors.Error("envelope read returned nil"))
				}
				// Reentrant server use must not deadlock either: no nonce
				// lock may be held here.
				if _, err := srv.AuthenticateRequest(t.Context(), newAuthReq(t)); err == nil {
					reenterErr.Store(errors.Error("reentrant call succeeded"))
				}
				return refHA1(username, realm, testPassword, algorithm), testAccount{ID: "account-1"}, nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
		}
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		authReq = reqWithDigest(t, proofCreds(
			t, reqMsg(), reqURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1,
		), false)
		principal, err := srv.AuthenticateRequest(t.Context(), authReq)
		if err != nil || principal.ID != "account-1" {
			t.Fatalf("principal = %+v, err = %v, want account-1", principal, err)
		}
		if v := envErr.Load(); v != nil {
			t.Fatalf("envelope read inside lookup: %v", v)
		}
		if v := reenterErr.Load(); v == nil {
			// The reentrant call correctly challenged the credential-less
			// request; anything else is a defect.
		} else {
			t.Fatalf("reentrant call inside lookup: %v", v)
		}
	})

	t.Run("cancellation during lookup releases no identity", func(t *testing.T) {
		t.Parallel()

		entered := make(chan struct{})
		release := make(chan struct{})
		var blocking atomic.Bool
		srv, err := auth.NewDigestServer(auth.DigestServerOptions[testAccount]{
			ServerOptions: testAuthServerOpts(),
			LookupHA1: func(_ context.Context, username, realm, algorithm string) (string, testAccount, error) {
				if blocking.Swap(false) {
					close(entered)
					<-release
				}
				return refHA1(username, realm, testPassword, algorithm), testAccount{ID: "account-1"}, nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
		}
		blocking.Store(true)
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		req := reqWithDigest(t,
			proofCreds(t, reqMsg(), reqURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1),
			false,
		)

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			p, err := srv.AuthenticateRequest(ctx, req)
			if p != (testAccount{}) {
				done <- errors.Error("canceled request released a principal")
				return
			}
			done <- err
		}()
		<-entered // the callback is actually inside the lookup now
		cancel()
		close(release)
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}

		// Cancellation must not have consumed replay state: the very same
		// proof and count still authenticate under a fresh context.
		principal, err := srv.AuthenticateRequest(t.Context(),
			reqWithDigest(t,
				proofCreds(t, reqMsg(), reqURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1),
				false,
			),
		)
		if err != nil || principal.ID != "account-1" {
			t.Fatalf("retry after cancellation: principal = %+v, err = %v, want account-1", principal, err)
		}
	})

	t.Run("expiry while lookup is blocked produces stale", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			release := make(chan struct{})
			lookupStarted := make(chan struct{})
			srv, err := auth.NewDigestServer(auth.DigestServerOptions[testAccount]{
				ServerOptions: testAuthServerOpts(),
				NonceTTL:      time.Minute,
				LookupHA1: func(_ context.Context, username, realm, algorithm string) (string, testAccount, error) {
					close(lookupStarted)
					<-release
					return refHA1(username, realm, testPassword, algorithm), testAccount{ID: "account-1"}, nil
				},
			})
			if err != nil {
				t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
			}
			nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
			req := reqWithDigest(t,
				proofCreds(t, reqMsg(), reqURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1),
				false,
			)

			done := make(chan error, 1)
			go func() {
				var p testAccount
				p, err := srv.AuthenticateRequest(t.Context(), req)
				if p != (testAccount{}) {
					done <- errors.Error("expired nonce released a principal")
					return
				}
				done <- err
			}()
			<-lookupStarted
			// The fake clock advances only while every bubble goroutine
			// is durably blocked; the callback's channel receive is, so
			// this Sleep jumps past the TTL deterministically.
			time.Sleep(2 * time.Minute)
			close(release)

			err = <-done
			if !errors.Is(err, sip.ErrAuthenticationRequired) {
				t.Fatalf("error = %v, want auth-required", err)
			}
			var rejection *sip.RequestRejectedError
			if !errors.As(err, &rejection) {
				t.Fatalf("error = %T, want *sip.RequestRejectedError", err)
			}
			var sawStale bool
			for _, cln := range chalsOf(t, rejection.RespondOptions().ResponseOptions.Headers, false) {
				if cln.Stale {
					sawStale = true
				}
			}
			if !sawStale {
				t.Fatal("expired nonce with valid proof did not produce a stale challenge")
			}
		})
	})

	t.Run("expired nonce with bad proof gets a fresh challenge", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			srv := newVerifyingServer(t, func(o *auth.DigestServerOptions[testAccount]) {
				o.NonceTTL = time.Minute
			})
			nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
			creds := proofCreds(t, reqMsg(), reqURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1)
			creds.Response = strings.Repeat("0", 64)

			time.Sleep(2 * time.Minute)

			_, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, creds, false))
			if !errors.Is(err, sip.ErrAuthenticationRequired) {
				t.Fatalf("error = %v, want auth-required", err)
			}
			var rejection *sip.RequestRejectedError
			if !errors.As(err, &rejection) {
				t.Fatalf("error = %T, want *sip.RequestRejectedError", err)
			}
			for _, cln := range chalsOf(t, rejection.RespondOptions().ResponseOptions.Headers, false) {
				if cln.Stale {
					t.Fatal("bad proof against expired nonce produced a stale challenge")
				}
			}
		})
	})

	t.Run("eviction while lookup is blocked produces fresh challenge", func(t *testing.T) {
		t.Parallel()

		release := make(chan struct{})
		lookupStarted := make(chan struct{})
		srv, err := auth.NewDigestServer(auth.DigestServerOptions[testAccount]{
			ServerOptions: testAuthServerOpts(),
			MaxNonces:     2,
			LookupHA1: func(_ context.Context, username, realm, algorithm string) (string, testAccount, error) {
				close(lookupStarted)
				<-release
				return refHA1(username, realm, testPassword, algorithm), testAccount{ID: "account-1"}, nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
		}
		nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
		req := reqWithDigest(t,
			proofCreds(t, reqMsg(), reqURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1),
			false,
		)

		done := make(chan error, 1)
		go func() {
			var p testAccount
			p, err := srv.AuthenticateRequest(t.Context(), req)
			if p != (testAccount{}) {
				done <- errors.Error("evicted nonce released a principal")
				return
			}
			done <- err
		}()
		<-lookupStarted
		// Evict the presented nonce: capacity 2 with one algorithm means
		// two further issued challenges push the first record out.
		for range 2 {
			_, _ = srv.AuthenticateRequest(t.Context(), newAuthReq(t))
		}
		close(release)

		err = <-done
		if !errors.Is(err, sip.ErrAuthenticationRequired) {
			t.Fatalf("error = %v, want auth-required", err)
		}
		var rejection *sip.RequestRejectedError
		if !errors.As(err, &rejection) {
			t.Fatalf("error = %T, want *sip.RequestRejectedError", err)
		}
		for _, cln := range chalsOf(t, rejection.RespondOptions().ResponseOptions.Headers, false) {
			if cln.Stale {
				t.Fatal("evicted nonce produced a stale challenge")
			}
		}
	})

	t.Run("known expired nonce stays distinguishable until evicted", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			srv := newVerifyingServer(t, func(o *auth.DigestServerOptions[testAccount]) {
				o.NonceTTL = time.Minute
				o.MaxNonces = 3
			})
			nonce := chalNonce(t, srv, newAuthReq(t), false, "SHA-256")
			time.Sleep(2 * time.Minute)

			// Still recorded but expired: a valid proof yields stale.
			valid := proofCreds(t, reqMsg(), reqURI(), testUser, testPassword, nonce, "cn-1", "SHA-256", 1)
			_, err := srv.AuthenticateRequest(t.Context(), reqWithDigest(t, valid, false))
			var rejection *sip.RequestRejectedError
			if !errors.As(err, &rejection) {
				t.Fatalf("error = %v, want *sip.RequestRejectedError", err)
			}
			var sawStale bool
			for _, cln := range chalsOf(t, rejection.RespondOptions().ResponseOptions.Headers, false) {
				sawStale = sawStale || cln.Stale
			}
			if !sawStale {
				t.Fatal("expired but recorded nonce produced a non-stale challenge")
			}

			// Evict the record; the same proof now yields a fresh challenge.
			for range 3 {
				_, _ = srv.AuthenticateRequest(t.Context(), newAuthReq(t))
			}
			valid2 := proofCreds(
				t, reqMsg(), reqURI(), testUser, testPassword, nonce, "cn-2", "SHA-256", 1,
			)
			_, err = srv.AuthenticateRequest(t.Context(), reqWithDigest(t, valid2, false))
			if !errors.As(err, &rejection) {
				t.Fatalf("error = %v, want *sip.RequestRejectedError", err)
			}
			for _, cln := range chalsOf(t, rejection.RespondOptions().ResponseOptions.Headers, false) {
				if cln.Stale {
					t.Fatal("evicted expired nonce produced a stale challenge")
				}
			}
		})
	})
}
