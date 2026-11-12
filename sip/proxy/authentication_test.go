package proxy_test

import (
	"context"
	"log/slog"
	"net/netip"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/auth"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/proxy"
	"github.com/ghettovoice/gosip/uri"
)

// authPrincipal is the application-owned principal returned by the
// authenticator callbacks in this file.
type authPrincipal struct {
	ID      string
	Allowed bool
}

// authRoute builds a target-resolving route whose authorization hook is the
// supplied authenticator closure.
func authRoute(authorize func(context.Context, *sip.RequestEnvelope) error) *proxy.Route {
	return &proxy.Route{
		Name:             "auth",
		ForwardMode:      proxy.ForwardModeStateful,
		AuthorizeRequest: authorize,
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
			return statelessPlan(statelessLitTarget("A", "10.0.0.9")), nil
		},
	}
}

// authInbound composes the real transport-bound inbound chain so that typed
// rejections are answered through the owned server transaction instead of
// being fabricated in assertions.
func authInbound(tb testing.TB, tp *stubTransp) sip.RequestReceiver {
	tb.Helper()
	return sip.InterceptInboundRequest(
		slices.Collect(tp.InboundRequestInterceptors.All()),
		sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
			return errors.ErrorWrap("request escaped to the silent receiver")
		}),
	)
}

// drainAuthChallenge drains one upstream response and asserts it is a proxy
// challenge without forwarding or user-agent headers.
func drainAuthChallenge(tb testing.TB, tp *stubTransp) *sip.ResponseEnvelope {
	tb.Helper()

	ress := drainRess(tp)
	if len(ress) != 1 {
		tb.Fatalf("upstream responses = %d, want 1", len(ress))
	}
	res := ress[0]
	if res.Status() != sip.ResponseStatusProxyAuthenticationRequired {
		tb.Fatalf("response status = %v, want %v", res.Status(), sip.ResponseStatusProxyAuthenticationRequired)
	}
	if got := res.Message().Headers.Get("WWW-Authenticate"); len(got) != 0 {
		tb.Fatalf("WWW-Authenticate headers = %d, want 0 (proxy leg only)", len(got))
	}
	if got := len(tp.sendReqCalls); got != 0 {
		tb.Fatalf("forwarded requests = %d, want 0", got)
	}
	return res
}

func digestProxyChallenge(tb testing.TB, res *sip.ResponseEnvelope) *header.DigestChallenge {
	tb.Helper()

	for _, h := range res.Message().Headers.Get("Proxy-Authenticate") {
		if pa, ok := h.(*header.ProxyAuthenticate); ok && pa != nil {
			if dc, ok := pa.AuthChallenge.(*header.DigestChallenge); ok {
				return dc
			}
		}
	}
	tb.Fatalf("no Proxy-Authenticate digest challenge on %v response", res.Status())
	return nil
}

func bearerProxyChallenge(tb testing.TB, res *sip.ResponseEnvelope) *header.BearerChallenge {
	tb.Helper()

	for _, h := range res.Message().Headers.Get("Proxy-Authenticate") {
		if pa, ok := h.(*header.ProxyAuthenticate); ok && pa != nil {
			if bc, ok := pa.AuthChallenge.(*header.BearerChallenge); ok {
				return bc
			}
		}
	}
	tb.Fatalf("no Proxy-Authenticate bearer challenge on %v response", res.Status())
	return nil
}

func appendBearerCredentials(tb testing.TB, req *sip.RequestEnvelope, token string) {
	tb.Helper()
	req.WithMessage(func(msg *sip.Request) {
		msg.Headers.Append(&header.ProxyAuthorization{
			AuthCredentials: &header.BearerCredentials{Token: token},
		})
	})
}

func TestAuthenticationHookChallenge(t *testing.T) {
	t.Parallel()

	served := []string{"alice.voip.com"}
	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	t.Run("digest", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			var lookups atomic.Int32
			srv, err := auth.NewDigestServer(auth.DigestServerOptions[authPrincipal]{
				ServerOptions: auth.ServerOptions{Realm: "alice.voip.com", Proxy: true}, //nolint:modernize // embedded field must stay keyed alongside other fields
				LookupHA1: func(_ context.Context, username, realm, algorithm string) (string, authPrincipal, error) {
					lookups.Add(1)
					ha1, err := auth.DigestHA1(username, realm, "auth-password", algorithm)
					if err != nil {
						return "", authPrincipal{}, err
					}
					return ha1, authPrincipal{ID: username, Allowed: true}, nil
				},
			})
			if err != nil {
				t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
			}
			_, _, tp := newConfiguredTestProxy(t, authRoute(func(ctx context.Context, req *sip.RequestEnvelope) error {
				_, err := srv.AuthenticateRequest(ctx, req)
				return err
			}), proxy.Options{ServedDomains: served})

			// Missing credentials: challenged without touching the backend.
			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".auth-digest-missing", laddr, raddr)
			if err := authInbound(t, tp).RecvRequest(ctx, req); err != nil {
				t.Fatalf("authInbound().RecvRequest() error = %v, want nil", err)
			}
			synctest.Wait()
			res407 := drainAuthChallenge(t, tp)
			ch := digestProxyChallenge(t, res407)
			if ch.Realm != "alice.voip.com" || ch.Nonce == "" || ch.Algorithm == "" {
				t.Fatalf("challenge = %+v, want realm, issued nonce, and algorithm", ch)
			}
			if got := lookups.Load(); got != 0 {
				t.Fatalf("credential lookups = %d, want 0", got)
			}

			// An invalid proof signed against the actually issued nonce still
			// reaches the backend once, then is challenged again.
			cln, err := auth.NewDigestClient(auth.DigestClientOptions{
				Realm: "alice.voip.com",
				Credentials: func(context.Context, *sip.Request, *header.DigestChallenge, bool) (string, string, error) {
					return "alice", "wrong-password", nil
				},
			})
			if err != nil {
				t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
			}
			retry := newInNonInviteReq(t, "UDP", sip.MagicCookie+".auth-digest-invalid", laddr, raddr)
			if err := cln.AuthenticateRequest(ctx, retry, res407); err != nil {
				t.Fatalf("cln.AuthenticateRequest() error = %v, want nil", err)
			}
			if err := authInbound(t, tp).RecvRequest(ctx, retry); err != nil {
				t.Fatalf("authInbound().RecvRequest() error = %v, want nil", err)
			}
			synctest.Wait()
			digestProxyChallenge(t, drainAuthChallenge(t, tp))
			if got := lookups.Load(); got != 1 {
				t.Fatalf("credential lookups = %d, want 1", got)
			}
		})
	})

	t.Run("bearer", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			as, err := uri.Parse("https://as.example/token")
			if err != nil {
				t.Fatalf("uri.Parse() error = %v, want nil", err)
			}
			var validations atomic.Int32
			srv, err := auth.NewBearerServer(auth.BearerServerOptions[authPrincipal]{
				ServerOptions: auth.ServerOptions{Realm: "alice.voip.com", Proxy: true}, //nolint:modernize // embedded field must stay keyed alongside other fields
				AuthzServer:   as,
				ValidateToken: func(_ context.Context, _ *sip.Request, token string) (authPrincipal, error) {
					validations.Add(1)
					if token == "valid-token" {
						return authPrincipal{ID: "alice", Allowed: true}, nil
					}
					return authPrincipal{}, sip.ErrInvalidCredentials
				},
			})
			if err != nil {
				t.Fatalf("auth.NewBearerServer() error = %v, want nil", err)
			}
			_, _, tp := newConfiguredTestProxy(t, authRoute(func(ctx context.Context, req *sip.RequestEnvelope) error {
				_, err := srv.AuthenticateRequest(ctx, req)
				return err
			}), proxy.Options{ServedDomains: served})

			// Missing token: challenged without an error parameter or a
			// backend call.
			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".auth-bearer-missing", laddr, raddr)
			if err := authInbound(t, tp).RecvRequest(ctx, req); err != nil {
				t.Fatalf("authInbound().RecvRequest() error = %v, want nil", err)
			}
			synctest.Wait()
			ch := bearerProxyChallenge(t, drainAuthChallenge(t, tp))
			if ch.Realm != "alice.voip.com" || ch.Error != "" {
				t.Fatalf("challenge = %+v, want realm and no error parameter", ch)
			}
			if got := validations.Load(); got != 0 {
				t.Fatalf("token validations = %d, want 0", got)
			}

			// A syntactically valid but unacceptable token is challenged with
			// error="invalid_token" after one backend call.
			retry := newInNonInviteReq(t, "UDP", sip.MagicCookie+".auth-bearer-invalid", laddr, raddr)
			appendBearerCredentials(t, retry, "invalid-token")
			if err := authInbound(t, tp).RecvRequest(ctx, retry); err != nil {
				t.Fatalf("authInbound().RecvRequest() error = %v, want nil", err)
			}
			synctest.Wait()
			ch = bearerProxyChallenge(t, drainAuthChallenge(t, tp))
			if ch.Error != "invalid_token" {
				t.Fatalf("challenge error = %q, want %q", ch.Error, "invalid_token")
			}
			if got := validations.Load(); got != 1 {
				t.Fatalf("token validations = %d, want 1", got)
			}
		})
	})
}

func TestAuthenticationHookBackendFailure(t *testing.T) {
	t.Parallel()

	served := []string{"alice.voip.com"}
	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	t.Run("digest", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			outage := errors.New("credential backend outage")

			srv, err := auth.NewDigestServer(auth.DigestServerOptions[authPrincipal]{
				ServerOptions: auth.ServerOptions{Realm: "alice.voip.com", Proxy: true}, //nolint:modernize // embedded field must stay keyed alongside other fields
				LookupHA1: func(context.Context, string, string, string) (string, authPrincipal, error) {
					return "", authPrincipal{}, outage
				},
			})
			if err != nil {
				t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
			}
			_, _, tp := newConfiguredTestProxy(t, authRoute(func(ctx context.Context, req *sip.RequestEnvelope) error {
				_, err := srv.AuthenticateRequest(ctx, req)
				return err
			}), proxy.Options{ServedDomains: served})

			// The unsigned request is challenged; the signed retry hits the
			// outage and must surface it as an ordinary failure.
			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".auth-digest-outage", laddr, raddr)
			if err := authInbound(t, tp).RecvRequest(ctx, req); err != nil {
				t.Fatalf("authInbound().RecvRequest() error = %v, want nil", err)
			}
			synctest.Wait()
			res407 := drainAuthChallenge(t, tp)

			cln, err := auth.NewDigestClient(auth.DigestClientOptions{
				Realm: "alice.voip.com",
				Credentials: func(context.Context, *sip.Request, *header.DigestChallenge, bool) (string, string, error) {
					return "alice", "auth-password", nil
				},
			})
			if err != nil {
				t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
			}
			retry := newInNonInviteReq(t, "UDP", sip.MagicCookie+".auth-digest-outage2", laddr, raddr)
			if err := cln.AuthenticateRequest(ctx, retry, res407); err != nil {
				t.Fatalf("cln.AuthenticateRequest() error = %v, want nil", err)
			}
			err = authInbound(t, tp).RecvRequest(ctx, retry)
			if !errors.Is(err, outage) {
				t.Fatalf("authInbound().RecvRequest() error = %v, want backend outage", err)
			}
			var rej *sip.RequestRejectedError
			if errors.As(err, &rej) {
				t.Fatalf("backend outage became a typed rejection: %v", err)
			}
			synctest.Wait()
			if got := len(drainRess(tp)); got != 0 {
				t.Fatalf("upstream responses = %d, want 0 (no fabricated 407)", got)
			}
			if got := len(drainReqs(tp)); got != 0 {
				t.Fatalf("forwarded requests = %d, want 0", got)
			}
		})
	})

	t.Run("bearer", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			outage := errors.New("token backend outage")

			as, err := uri.Parse("https://as.example/token")
			if err != nil {
				t.Fatalf("uri.Parse() error = %v, want nil", err)
			}
			srv, err := auth.NewBearerServer(auth.BearerServerOptions[authPrincipal]{
				ServerOptions: auth.ServerOptions{Realm: "alice.voip.com", Proxy: true}, //nolint:modernize // embedded field must stay keyed alongside other fields
				AuthzServer:   as,
				ValidateToken: func(context.Context, *sip.Request, string) (authPrincipal, error) {
					return authPrincipal{}, outage
				},
			})
			if err != nil {
				t.Fatalf("auth.NewBearerServer() error = %v, want nil", err)
			}
			_, _, tp := newConfiguredTestProxy(t, authRoute(func(ctx context.Context, req *sip.RequestEnvelope) error {
				_, err := srv.AuthenticateRequest(ctx, req)
				return err
			}), proxy.Options{ServedDomains: served})

			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".auth-bearer-outage", laddr, raddr)
			appendBearerCredentials(t, req, "valid-token")
			err = authInbound(t, tp).RecvRequest(ctx, req)
			if !errors.Is(err, outage) {
				t.Fatalf("authInbound().RecvRequest() error = %v, want backend outage", err)
			}
			var rej *sip.RequestRejectedError
			if errors.As(err, &rej) {
				t.Fatalf("backend outage became a typed rejection: %v", err)
			}
			synctest.Wait()
			if got := len(drainRess(tp)); got != 0 {
				t.Fatalf("upstream responses = %d, want 0 (no fabricated 407)", got)
			}
			if got := len(drainReqs(tp)); got != 0 {
				t.Fatalf("forwarded requests = %d, want 0", got)
			}
		})
	})
}

// TestAuthenticationHookStatefulRetransmission proves that a transaction
// retransmission of an authenticated request is consumed by the established
// server transaction rather than re-authenticated.
func TestAuthenticationHookStatefulRetransmission(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		served := []string{"alice.voip.com"}
		laddr := netip.MustParseAddrPort("192.168.1.100:5060")
		raddr := netip.MustParseAddrPort("10.0.0.5:5060")

		var lookups atomic.Int32
		srv, err := auth.NewDigestServer(auth.DigestServerOptions[authPrincipal]{
			ServerOptions: auth.ServerOptions{Realm: "alice.voip.com", Proxy: true}, //nolint:modernize // embedded field must stay keyed alongside other fields
			LookupHA1: func(_ context.Context, username, realm, algorithm string) (string, authPrincipal, error) {
				lookups.Add(1)
				ha1, err := auth.DigestHA1(username, realm, "auth-password", algorithm)
				if err != nil {
					return "", authPrincipal{}, err
				}
				return ha1, authPrincipal{ID: username, Allowed: true}, nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
		}
		route := authRoute(func(ctx context.Context, req *sip.RequestEnvelope) error {
			p, err := srv.AuthenticateRequest(ctx, req)
			if err != nil {
				return err
			}
			// The application consumes the typed principal explicitly.
			if p.ID != "alice" || !p.Allowed {
				return sip.NewRequestRejectedError(
					errors.New("principal denied"),
					slog.LevelInfo,
					sip.ResponseStatusForbidden,
				)
			}
			return nil
		})
		elm, _, tp := newConfiguredTestProxy(t, route, proxy.Options{ServedDomains: served})

		// The first unsigned INVITE is challenged through the real inbound
		// chain.
		invite := newInInviteReq(t, "UDP", sip.MagicCookie+".auth-rtx-chal", laddr, raddr)
		if err := authInbound(t, tp).RecvRequest(ctx, invite); err != nil {
			t.Fatalf("authInbound().RecvRequest() error = %v, want nil", err)
		}
		synctest.Wait()
		res407 := drainAuthChallenge(t, tp)

		// A real client signs the retry against the issued nonce.
		cln, err := auth.NewDigestClient(auth.DigestClientOptions{
			Realm: "alice.voip.com",
			Credentials: func(context.Context, *sip.Request, *header.DigestChallenge, bool) (string, string, error) {
				return "alice", "auth-password", nil
			},
		})
		if err != nil {
			t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
		}
		signed := newInInviteReq(t, "UDP", sip.MagicCookie+".auth-rtx", laddr, raddr)
		if err := cln.AuthenticateRequest(ctx, signed, res407); err != nil {
			t.Fatalf("cln.AuthenticateRequest() error = %v, want nil", err)
		}

		lookups.Store(0)
		if err := authInbound(t, tp).RecvRequest(ctx, signed); err != nil {
			t.Fatalf("authInbound().RecvRequest() error = %v, want nil", err)
		}
		synctest.Wait()
		sends := drainReqs(tp)
		if len(sends) != 1 {
			t.Fatalf("outbound requests = %d, want 1 forwarded INVITE", len(sends))
		}
		if got := lookups.Load(); got != 1 {
			t.Fatalf("credential lookups = %d, want 1", got)
		}

		// The downstream leg answers 180, which is relayed upstream through
		// the server transaction.
		clnKey, err := sip.MakeClientTransactionKey(sends[0])
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx, ok := elm.LoadClientTransaction(clnKey)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}
		if err := clnTx.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("clnTx.RecvResponse(180) error = %v, want nil", err)
		}
		synctest.Wait()
		if got := resStatuses(t, drainRess(tp), sip.RequestMethodInvite); !slices.Equal(got, []int{180}) {
			t.Fatalf("upstream responses = %v, want [180]", got)
		}

		// A same-branch/CSeq retransmission is consumed by the existing
		// server transaction: it replays the last response without a second
		// authentication pass or another forwarded INVITE.
		repeat, ok := signed.Clone().(*sip.RequestEnvelope)
		if !ok {
			t.Fatal("request envelope clone has unexpected type")
		}
		if err := authInbound(t, tp).RecvRequest(ctx, repeat); err != nil {
			t.Fatalf("authInbound().RecvRequest(retransmission) error = %v, want nil", err)
		}
		synctest.Wait()
		if got := resStatuses(t, drainRess(tp), sip.RequestMethodInvite); !slices.Equal(got, []int{180}) {
			t.Fatalf("retransmission responses = %v, want [180] replay", got)
		}
		if got := lookups.Load(); got != 1 {
			t.Fatalf("credential lookups = %d, want 1 (no re-authentication)", got)
		}
		if got := len(drainReqs(tp)); got != 0 {
			t.Fatalf("extra outbound requests = %d, want 0", got)
		}
	})
}

// TestAuthenticationHookMethodsAndPolicy keeps CANCEL/ACK processing and the
// application's own authorization decision independent of authentication.
func TestAuthenticationHookMethodsAndPolicy(t *testing.T) {
	t.Parallel()

	served := []string{"alice.voip.com"}
	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	silentNext := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
		return nil
	})

	newBearerFixture := func(tb testing.TB, validations *atomic.Int32, validate func(context.Context, *sip.Request, string) (authPrincipal, error)) (*sip.Element, *proxy.Proxy, *stubTransp) {
		tb.Helper()

		as, err := uri.Parse("https://as.example/token")
		if err != nil {
			tb.Fatalf("uri.Parse() error = %v, want nil", err)
		}
		srv, err := auth.NewBearerServer(auth.BearerServerOptions[authPrincipal]{
			ServerOptions: auth.ServerOptions{Realm: "alice.voip.com", Proxy: true}, //nolint:modernize // embedded field must stay keyed alongside other fields
			AuthzServer:   as,
			ValidateToken: func(ctx context.Context, req *sip.Request, token string) (authPrincipal, error) {
				validations.Add(1)
				if validate != nil {
					return validate(ctx, req, token)
				}
				switch token {
				case "valid-token":
					return authPrincipal{ID: "alice", Allowed: true}, nil
				case "denied-token":
					return authPrincipal{ID: "bob", Allowed: false}, nil
				}
				return authPrincipal{}, sip.ErrInvalidCredentials
			},
		})
		if err != nil {
			tb.Fatalf("auth.NewBearerServer() error = %v, want nil", err)
		}
		route := authRoute(func(ctx context.Context, req *sip.RequestEnvelope) error {
			// The mechanism authenticates only credential-bearing methods;
			// ACK and CANCEL follow the route's normal method rules.
			if req.Method().Equal(sip.RequestMethodAck) || req.Method().Equal(sip.RequestMethodCancel) {
				return nil
			}
			p, err := srv.AuthenticateRequest(ctx, req)
			if err != nil {
				return err
			}
			// Application authorization is independent of authentication.
			if !p.Allowed {
				return sip.NewRequestRejectedError(
					errors.New("principal denied"),
					slog.LevelInfo,
					sip.ResponseStatusForbidden,
				)
			}
			return nil
		})
		return newConfiguredTestProxy(tb, route, proxy.Options{ServedDomains: served})
	}

	t.Run("matched cancel and acks are never authenticated", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			var validations atomic.Int32
			elm, prx, tp := newBearerFixture(t, &validations, nil)

			invite := newInInviteReq(t, "UDP", sip.MagicCookie+".auth-cancel", laddr, raddr)
			appendBearerCredentials(t, invite, "valid-token")
			admitInviteAndSettle(t, prx, elm, tp, silentNext, invite, sip.ResponseStatusRinging)
			drainReqs(tp)
			if got := validations.Load(); got != 1 {
				t.Fatalf("token validations = %d, want 1 for the admitted INVITE", got)
			}

			// A matched CANCEL is acknowledged on its own transaction without
			// reaching the authorization hook again.
			cancel, err := sip.NewCancelRequestEnvelope(invite)
			if err != nil {
				t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
			}
			if err := authInbound(t, tp).RecvRequest(ctx, cancel); err != nil {
				t.Fatalf("authInbound().RecvRequest(CANCEL) error = %v, want nil", err)
			}
			synctest.Wait()
			if got := resStatuses(t, drainRess(tp), sip.RequestMethodCancel); !slices.Equal(got, []int{200}) {
				t.Fatalf("cancel responses = %v, want [200]", got)
			}
			if got := validations.Load(); got != 1 {
				t.Fatalf("token validations = %d, want 1 (CANCEL never authorized)", got)
			}
			drainReqs(tp)

			// An unmatched CANCEL follows the existing stateless method
			// rules without authentication.
			unmatched, err := sip.NewCancelRequestEnvelope(
				newInInviteReq(t, "UDP", sip.MagicCookie+".auth-cancel-unmatched", laddr, raddr),
			)
			if err != nil {
				t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
			}
			if err := authInbound(t, tp).RecvRequest(ctx, unmatched); err != nil {
				t.Fatalf("authInbound().RecvRequest(unmatched CANCEL) error = %v, want nil", err)
			}
			synctest.Wait()
			for _, res := range drainRess(tp) {
				if sts := res.Status(); sts == sip.ResponseStatusUnauthorized ||
					sts == sip.ResponseStatusProxyAuthenticationRequired {
					t.Fatalf("unmatched CANCEL produced auth challenge %v", sts)
				}
			}
			if got := validations.Load(); got != 1 {
				t.Fatalf("token validations = %d, want 1 (unmatched CANCEL never authorized)", got)
			}
			drainReqs(tp)

			// A non-2xx ACK is absorbed by the INVITE transaction before the
			// proxy or the hook sees it. It needs its own INVITE that
			// completed with a final failure response.
			rejected := newInInviteReq(t, "UDP", sip.MagicCookie+".auth-ack-nack", laddr, raddr)
			appendBearerCredentials(t, rejected, "valid-token")
			admitInviteAndSettle(t, prx, elm, tp, silentNext, rejected, sip.ResponseStatusNotFound)
			if got := resStatuses(t, drainRess(tp), sip.RequestMethodInvite); !slices.Equal(got, []int{404}) {
				t.Fatalf("rejected INVITE responses = %v, want [404]", got)
			}
			drainReqs(tp)
			ack := newInAckReq(t, rejected, newInRes(t, rejected, sip.ResponseStatusNotFound))
			if err := authInbound(t, tp).RecvRequest(ctx, ack); err != nil {
				t.Fatalf("authInbound().RecvRequest(ACK) error = %v, want nil", err)
			}
			synctest.Wait()
			if got := len(drainRess(tp)); got != 0 {
				t.Fatalf("non-2xx ACK responses = %d, want 0", got)
			}
			if got := countAckTxs(elm); got != 0 {
				t.Fatalf("ACK transactions = %d, want 0", got)
			}
			if got := validations.Load(); got != 2 {
				t.Fatalf("token validations = %d, want 2 (ACK never authorized)", got)
			}

			// A 2xx ACK routes downstream without a transaction or another
			// authorization pass.
			established := newInInviteReq(t, "UDP", sip.MagicCookie+".auth-ack-2xx", laddr, raddr)
			appendBearerCredentials(t, established, "valid-token")
			admitInviteAndSettle(t, prx, elm, tp, silentNext, established, sip.ResponseStatusOK)
			drainRess(tp)
			drainReqs(tp)
			ack2 := newInAckReq(t, established, newInRes(t, established, sip.ResponseStatusOK))
			if err := authInbound(t, tp).RecvRequest(ctx, ack2); err != nil {
				t.Fatalf("authInbound().RecvRequest(2xx ACK) error = %v, want nil", err)
			}
			synctest.Wait()
			if got := len(drainRess(tp)); got != 0 {
				t.Fatalf("2xx ACK responses = %d, want 0", got)
			}
			ackSends := drainReqs(tp)
			if len(ackSends) != 1 || !ackSends[0].Method().Equal(sip.RequestMethodAck) {
				t.Fatalf("outbound requests after 2xx ACK = %d, want 1 routed ACK", len(ackSends))
			}
			if got := countAckTxs(elm); got != 0 {
				t.Fatalf("ACK transactions = %d, want 0", got)
			}
			if got := validations.Load(); got != 3 {
				t.Fatalf("token validations = %d, want 3 (2xx ACK never authorized)", got)
			}
		})
	})

	t.Run("validator method error on an authenticated method propagates", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			// An operational callback error that happens to carry
			// ErrRequestMethodNotAllowed must not bypass authentication for
			// methods the mechanism does authenticate.
			var validations atomic.Int32
			_, _, tp := newBearerFixture(t, &validations, func(context.Context, *sip.Request, string) (authPrincipal, error) {
				return authPrincipal{}, sip.NewRequestMethodNotAllowedError("validator outage")
			})

			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".auth-mtd-err", laddr, raddr)
			appendBearerCredentials(t, req, "valid-token")
			err := authInbound(t, tp).RecvRequest(ctx, req)
			if !errors.Is(err, sip.ErrRequestMethodNotAllowed) {
				t.Fatalf("authInbound().RecvRequest() error = %v, want %v", err, sip.ErrRequestMethodNotAllowed)
			}
			if got := validations.Load(); got != 1 {
				t.Fatalf("token validations = %d, want 1", got)
			}
			synctest.Wait()
			if got := len(drainRess(tp)); got != 0 {
				t.Fatalf("upstream responses = %d, want 0 (no fabricated challenge)", got)
			}
			if got := len(drainReqs(tp)); got != 0 {
				t.Fatalf("forwarded requests = %d, want 0", got)
			}
		})
	})

	t.Run("authenticated principal can still be denied", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			var validations atomic.Int32
			_, _, tp := newBearerFixture(t, &validations, nil)

			req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".auth-denied", laddr, raddr)
			appendBearerCredentials(t, req, "denied-token")
			if err := authInbound(t, tp).RecvRequest(ctx, req); err != nil {
				t.Fatalf("authInbound().RecvRequest() error = %v, want nil", err)
			}
			synctest.Wait()
			ress := drainRess(tp)
			if len(ress) != 1 {
				t.Fatalf("upstream responses = %d, want 1", len(ress))
			}
			if sts := ress[0].Status(); sts != sip.ResponseStatusForbidden {
				t.Fatalf("response status = %v, want %v (403 is not a challenge)", sts, sip.ResponseStatusForbidden)
			}
			if got := validations.Load(); got != 1 {
				t.Fatalf("token validations = %d, want 1 (authentication ran)", got)
			}
			if got := len(drainReqs(tp)); got != 0 {
				t.Fatalf("forwarded requests = %d, want 0", got)
			}
		})
	})
}
