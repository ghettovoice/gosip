package useragent_test

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil/siptest"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/useragent"
)

// peerURI builds a SIP URI for user pointing at the raw peer socket, so a
// scripted 3xx contact lands back on the same datagram peer.
func peerURI(tb testing.TB, peer *net.UDPConn, user string) *sip.URI {
	tb.Helper()

	addr := msgPeerAddr(tb, peer)
	return &sip.URI{
		User: sip.MakeUserInfo(user),
		Addr: sip.MakeHostPortAddr(addr.Addr().String(), addr.Port()),
	}
}

// securedPeerURI is peerURI with the SIPS scheme.
func securedPeerURI(tb testing.TB, peer *net.UDPConn, user string) *sip.URI {
	tb.Helper()

	u := peerURI(tb, peer, user)
	u.Secured = true
	return u
}

// reqURIUser returns the captured request URI's user part: the raw peer is
// scripted on the request URI, and the user selects the scripted answer.
func reqURIUser(tb testing.TB, req *sip.RequestEnvelope) string {
	tb.Helper()

	u, ok := req.URI().(*sip.URI)
	if !ok {
		tb.Fatalf("request URI = %T, want *sip.URI", req.URI())
	}
	name := u.User.Username()
	if name == "" {
		tb.Fatalf("request URI %v has no user part", u)
	}
	return name
}

// gateUsers projects gated targets to their user parts for order comparison.
func gateUsers(calls []*sip.URI) []string {
	users := make([]string, len(calls))
	for i, u := range calls {
		users[i] = u.User.Username()
	}
	return users
}

// approveAll builds an AllowTarget that records each gated target's clone and
// approves it.
func approveAll(calls *[]*sip.URI) func(context.Context, *sip.RequestEnvelope, *sip.URI) (bool, error) {
	return func(_ context.Context, _ *sip.RequestEnvelope, target *sip.URI) (bool, error) {
		*calls = append(*calls, target.Clone().(*sip.URI))
		return true, nil
	}
}

// replyPeerRedirect answers a captured raw-peer request with a redirect status
// carrying the given Contact header values.
func replyPeerRedirect(
	tb testing.TB,
	peer *net.UDPConn,
	captured *sip.RequestEnvelope,
	src netip.AddrPort,
	sts sip.ResponseStatus,
	contacts ...sip.Header,
) {
	tb.Helper()

	hdrs := make(sip.Headers)
	for _, c := range contacts {
		hdrs.Append(c)
	}
	res, err := captured.NewResponse(sts, sip.ResponseOptions{Headers: hdrs})
	if err != nil {
		tb.Fatalf("captured.NewResponse(%v) error = %v, want nil", sts, err)
	}
	sendPeerRes(tb, peer, src, res)
}

// newRedirectUA builds a sender user agent configured with the given
// redirect policy and authenticator. It mirrors the sender setup of
// newAuthSender.
func newRedirectUA(
	tb testing.TB,
	policy *useragent.RedirectPolicy,
	authn sip.ClientAuthenticator,
) *useragent.UserAgent {
	tb.Helper()

	contact, err := sip.ParseURI("sip:alice@pc.ua.test")
	if err != nil {
		tb.Fatalf("sip.ParseURI() error = %v, want nil", err)
	}
	ua, err := useragent.New(useragent.Options{
		AOR:            msgAOR(tb),
		Contact:        contact,
		Authenticator:  authn,
		RedirectPolicy: policy,
	})
	if err != nil {
		tb.Fatalf("useragent.New() error = %v, want nil", err)
	}
	return ua
}

// newRedirectSender builds a wire element and a redirect-enabled sender
// user agent on it.
func newRedirectSender(
	tb testing.TB,
	policy *useragent.RedirectPolicy,
	authn sip.ClientAuthenticator,
) *msgSenderFixture {
	tb.Helper()

	return &msgSenderFixture{
		wireElement: newWireElem(tb),
		ua:          newRedirectUA(tb, policy, authn),
		aor:         msgAOR(tb),
	}
}

// newSecureWireElement creates an element serving a loopback UDP socket whose
// transport metadata is marked secured, so sips: operation targets resolve
// over the same datagram peer without a real TLS stack.
func newSecureWireElement(tb testing.TB) *wireElement {
	tb.Helper()

	meta := sip.UDPMetadata()
	meta.Flags.SetSecured(true)

	elm, addr := siptest.NewLoopbackUDPElement(tb, meta)
	return &wireElement{elm: elm, addr: addr}
}

func TestUserAgent_RedirectPolicy(t *testing.T) {
	t.Parallel()

	t.Run("redirect statuses follow an approved contact", func(t *testing.T) {
		t.Parallel()

		for _, sts := range []sip.ResponseStatus{
			sip.ResponseStatusMultipleChoices,
			sip.ResponseStatusMovedPermanently,
			sip.ResponseStatusMovedTemporarily,
		} {
			t.Run(strconv.Itoa(int(sts)), func(t *testing.T) {
				t.Parallel()

				var gateCalls []*sip.URI
				fx := newRedirectSender(t, &useragent.RedirectPolicy{
					Timeout:     10 * time.Second,
					AllowTarget: approveAll(&gateCalls),
				}, nil)
				peer := newRawPeer(t)

				target := peerURI(t, peer, "bob")
				// The contact carries junk a request URI must not keep:
				// the method parameter and URI headers are cleaned away
				// before the target is followed.
				contact := peerURI(t, peer, "carol")
				contact.Params = make(sip.Values).Set("method", "INVITE")
				contact.Headers = make(sip.Values).Set("X-Route", "yes")
				wantTarget := sip.CleanRequestURI(contact)

				ctx := sip.ContextWithElement(t.Context(), fx.elm)
				op, err := fx.ua.Message(ctx, target, useragent.MessageOperationOptions{
					Timing: noRetransmitTiming,
					Body:   []byte("ping"),
				})
				if err != nil {
					t.Fatalf("ua.Message() error = %v, want nil", err)
				}
				if op == nil {
					t.Fatal("ua.Message() op = nil, want non-nil")
				}

				captured, src := readReq(t, peer)
				replyPeerRedirect(t, peer, captured, src, sts, header.Contact{{URI: contact}})

				// The retargeted retry reaches the contact address while
				// the To identity stays on the original target: a 301
				// never retargets the AoR.
				captured2, src2 := readReq(t, peer)
				gotURI, ok := captured2.URI().(*sip.URI)
				if !ok {
					t.Fatalf("redirected request URI = %T, want *sip.URI", captured2.URI())
				}
				if !gotURI.Equal(wantTarget) {
					t.Errorf("redirected request URI = %v, want cleaned %v", gotURI, wantTarget)
				}
				to, ok := captured2.Headers().To()
				if !ok || to == nil {
					t.Fatal("redirected request has no To header")
				}
				if !to.URI.Equal(target) {
					t.Errorf("redirected To = %v, want original target %v", to.URI, target)
				}
				replyPeerRes(t, peer, captured2, src2, sip.ResponseStatusOK)

				res, err := op.Wait(t.Context())
				if err != nil {
					t.Fatalf("op.Wait() error = %v, want nil", err)
				}
				if got := res.Status(); got != sip.ResponseStatusOK {
					t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
				}
				if len(gateCalls) != 1 || !gateCalls[0].Equal(wantTarget) {
					t.Fatalf("gate targets = %v, want one call for %v", gateCalls, wantTarget)
				}
			})
		}
	})

	t.Run("no policy resolves the redirect", func(t *testing.T) {
		t.Parallel()

		fx := newMsgSender(t)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peer, "carol")}})
		expectNoDatagram(t, peer)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusMovedTemporarily {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusMovedTemporarily)
		}
	})

	t.Run("unsupported redirect statuses resolve as final", func(t *testing.T) {
		t.Parallel()

		for _, sts := range []sip.ResponseStatus{
			sip.ResponseStatusUseProxy,
			sip.ResponseStatusAlternativeService,
		} {
			t.Run(strconv.Itoa(int(sts)), func(t *testing.T) {
				t.Parallel()

				var gateCalls []*sip.URI
				fx := newRedirectSender(t, &useragent.RedirectPolicy{
					Timeout:     10 * time.Second,
					AllowTarget: approveAll(&gateCalls),
				}, nil)
				peer := newRawPeer(t)

				ctx := sip.ContextWithElement(t.Context(), fx.elm)
				op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
					Timing: noRetransmitTiming,
					Body:   []byte("ping"),
				})
				if err != nil {
					t.Fatalf("ua.Message() error = %v, want nil", err)
				}
				if op == nil {
					t.Fatal("ua.Message() op = nil, want non-nil")
				}

				captured, src := readReq(t, peer)
				replyPeerRedirect(t, peer, captured, src, sts,
					header.Contact{{URI: peerURI(t, peer, "carol")}})
				expectNoDatagram(t, peer)

				res, err := op.Wait(t.Context())
				if err != nil {
					t.Fatalf("op.Wait() error = %v, want nil", err)
				}
				if got := res.Status(); got != sts {
					t.Fatalf("op.Wait() status = %v, want %v", got, sts)
				}
				if len(gateCalls) != 0 {
					t.Errorf("gate calls = %v, want none for an unsupported status", gateCalls)
				}
			})
		}
	})

	t.Run("unsupported redirect mid-chain resolves itself", func(t *testing.T) {
		t.Parallel()

		for _, sts := range []sip.ResponseStatus{
			sip.ResponseStatusUseProxy,
			sip.ResponseStatusAlternativeService,
		} {
			t.Run(strconv.Itoa(int(sts)), func(t *testing.T) {
				t.Parallel()

				var gateCalls []*sip.URI
				fx := newRedirectSender(t, &useragent.RedirectPolicy{
					Timeout:     10 * time.Second,
					AllowTarget: approveAll(&gateCalls),
				}, nil)
				peer := newRawPeer(t)

				ctx := sip.ContextWithElement(t.Context(), fx.elm)
				op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
					Timing: noRetransmitTiming,
					Body:   []byte("ping"),
				})
				if err != nil {
					t.Fatalf("ua.Message() error = %v, want nil", err)
				}
				if op == nil {
					t.Fatal("ua.Message() op = nil, want non-nil")
				}

				captured, src := readReq(t, peer)
				replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
					header.Contact{{URI: peerURI(t, peer, "carol")}})

				// Carol answers an unsupported 3xx: the chain ends on her
				// response itself, not on the earlier recorded redirect —
				// the 305/380 status carries the terminal meaning a manual
				// follow-up needs.
				captured, src = readReq(t, peer)
				if got := reqURIUser(t, captured); got != "carol" {
					t.Fatalf("request URI user = %q, want carol", got)
				}
				replyPeerRes(t, peer, captured, src, sts)
				expectNoDatagram(t, peer)

				res, err := op.Wait(t.Context())
				if err != nil {
					t.Fatalf("op.Wait() error = %v, want nil", err)
				}
				if got := res.Status(); got != sts {
					t.Fatalf("op.Wait() status = %v, want %v", got, sts)
				}
				if got, want := gateUsers(gateCalls), []string{"carol"}; !slices.Equal(got, want) {
					t.Errorf("gate targets = %v, want %v", got, want)
				}
			})
		}
	})

	t.Run("contacts are filtered and ordered by q", func(t *testing.T) {
		t.Parallel()

		var gateCalls []*sip.URI
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			Timeout:     10 * time.Second,
			AllowTarget: approveAll(&gateCalls),
		}, nil)
		peer := newRawPeer(t)

		tel, err := sip.ParseAnyURI("tel:+1234")
		if err != nil {
			t.Fatalf("sip.ParseAnyURI() error = %v, want nil", err)
		}
		expired := header.ContactAddr{URI: peerURI(t, peer, "expired")}
		expired.Params = make(header.Values).Set("expires", "0")
		badQRange := header.ContactAddr{URI: peerURI(t, peer, "badqrange")}
		badQRange.Params = make(header.Values).Set("q", "2")
		badQSyntax := header.ContactAddr{URI: peerURI(t, peer, "badqsyntax")}
		badQSyntax.Params = make(header.Values).Set("q", "abc")
		lo := header.ContactAddr{URI: peerURI(t, peer, "lo")}
		lo.Params = make(header.Values).Set("q", "0.3")
		hi1 := header.ContactAddr{URI: peerURI(t, peer, "hi1")}
		hi1.Params = make(header.Values).Set("q", "0.9")
		hi2 := header.ContactAddr{URI: peerURI(t, peer, "hi2")}
		hi2.Params = make(header.Values).Set("q", "0.9")
		top := header.ContactAddr{URI: peerURI(t, peer, "top")}

		// An invalid contact URI cannot arrive over the wire at all: the
		// whole response fails message validation before it reaches the
		// redirect engine. The closest deliverable case is a malformed
		// Contact line that parses as a generic header and is skipped.
		contact := header.Contact{lo, {URI: tel}, expired, badQRange, badQSyntax, hi1, hi2, top}
		garbage := &header.Any{Name: "Contact", Value: "garbage"}

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily, contact, garbage)

		// Survivors pop in descending q with a stable order between
		// equals: top (default q=1), hi1, hi2 (q=0.9, header order), lo
		// (q=0.3). Ordinary final failures fall back to the next pending
		// sibling of the same redirect.
		for _, script := range []struct {
			user string
			sts  sip.ResponseStatus
		}{
			{"top", sip.ResponseStatusBusyHere},
			{"hi1", sip.ResponseStatusBusyHere},
			{"hi2", sip.ResponseStatusBusyHere},
			{"lo", sip.ResponseStatusOK},
		} {
			captured, src := readReq(t, peer)
			if got := reqURIUser(t, captured); got != script.user {
				t.Fatalf("request URI user = %q, want %q", got, script.user)
			}
			replyPeerRes(t, peer, captured, src, script.sts)
		}

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusOK {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
		}

		want := []string{"top", "hi1", "hi2", "lo"}
		if got := gateUsers(gateCalls); !slices.Equal(got, want) {
			t.Errorf("gate target order = %v, want %v", got, want)
		}
	})

	t.Run("wildcard contact yields no candidates", func(t *testing.T) {
		t.Parallel()

		var gateCalls []*sip.URI
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			Timeout:     10 * time.Second,
			AllowTarget: approveAll(&gateCalls),
		}, nil)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily, header.Contact{})
		expectNoDatagram(t, peer)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusMovedTemporarily {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusMovedTemporarily)
		}
		if len(gateCalls) != 0 {
			t.Errorf("gate calls = %v, want none for a wildcard contact", gateCalls)
		}
	})

	t.Run("depth cap resolves the last redirect", func(t *testing.T) {
		t.Parallel()

		var gateCalls []*sip.URI
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			MaxDepth:    1,
			Timeout:     10 * time.Second,
			AllowTarget: approveAll(&gateCalls),
		}, nil)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peer, "carol")}})

		// Carol sits at depth 1 and is followed; her own redirect would
		// open depth 2 beyond the cap, so the operation resolves her
		// redirect instead of sending again.
		captured, src = readReq(t, peer)
		if got := reqURIUser(t, captured); got != "carol" {
			t.Fatalf("request URI user = %q, want carol", got)
		}
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peer, "dave")}})
		expectNoDatagram(t, peer)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusMovedTemporarily {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusMovedTemporarily)
		}
		contacts := slices.Collect(res.Headers().Contacts())
		if len(contacts) != 1 || !contacts[0].URI.Equal(peerURI(t, peer, "dave")) {
			t.Errorf("final redirect contacts = %v, want dave's contact", contacts)
		}
		if got, want := gateUsers(gateCalls), []string{"carol"}; !slices.Equal(got, want) {
			t.Errorf("gate targets = %v, want %v", got, want)
		}
	})

	t.Run("shared budget bounds hops", func(t *testing.T) {
		t.Parallel()

		var gateCalls []*sip.URI
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			MaxAttempts: 2,
			Timeout:     10 * time.Second,
			AllowTarget: approveAll(&gateCalls),
		}, nil)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peer, "carol")}})

		// The second send exhausts the shared budget, so carol's own
		// redirect is not followed: the operation resolves the most
		// recent 3xx.
		captured, src = readReq(t, peer)
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peer, "dave")}})
		expectNoDatagram(t, peer)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusMovedTemporarily {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusMovedTemporarily)
		}
		if got, want := gateUsers(gateCalls), []string{"carol"}; !slices.Equal(got, want) {
			t.Errorf("gate targets = %v, want %v", got, want)
		}
	})

	t.Run("shared budget stops a sibling fallback", func(t *testing.T) {
		t.Parallel()

		var gateCalls []*sip.URI
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			MaxAttempts: 2,
			Timeout:     10 * time.Second,
			AllowTarget: approveAll(&gateCalls),
		}, nil)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{
				{URI: peerURI(t, peer, "carol")},
				{URI: peerURI(t, peer, "dave")},
			})

		// Carol's send exhausts the shared budget, so when she answers
		// an ordinary final failure the pending sibling hop is stopped:
		// the operation resolves the most recent 3xx instead of the 486,
		// and the denied sibling never reaches the gate.
		captured, src = readReq(t, peer)
		if got := reqURIUser(t, captured); got != "carol" {
			t.Fatalf("request URI user = %q, want carol", got)
		}
		replyPeerRes(t, peer, captured, src, sip.ResponseStatusBusyHere)
		expectNoDatagram(t, peer)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusMovedTemporarily {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusMovedTemporarily)
		}
		if got, want := gateUsers(gateCalls), []string{"carol"}; !slices.Equal(got, want) {
			t.Errorf("gate targets = %v, want %v", got, want)
		}
	})

	t.Run("shared budget gates auth retries", func(t *testing.T) {
		t.Parallel()

		stub := &stubAuthenticator{fn: func(
			_ context.Context,
			req *sip.RequestEnvelope,
			res *sip.ResponseEnvelope,
		) error {
			stampAuthCredentials(req, res)
			return nil
		}}
		var gateCalls []*sip.URI
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			MaxAttempts: 2,
			Timeout:     10 * time.Second,
			AllowTarget: approveAll(&gateCalls),
		}, stub)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peer, "carol")}})

		// Carol's challenge cannot start a retry: the shared budget is
		// already spent, so the most recent 3xx resolves the operation
		// and the provider is never invoked.
		captured, src = readReq(t, peer)
		replyPeerChallenge(t, peer, captured, src, sip.ResponseStatusUnauthorized, wwwChallenge("n1"))
		expectNoDatagram(t, peer)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusMovedTemporarily {
			t.Fatalf("op.Wait() status = %v, want %v (the last redirect)", got, sip.ResponseStatusMovedTemporarily)
		}
		if got := stub.calls.Load(); got != 0 {
			t.Fatalf("authenticator calls = %d, want 0", got)
		}
	})

	t.Run("auth retries burn the same budget", func(t *testing.T) {
		t.Parallel()

		stub := &stubAuthenticator{fn: func(
			_ context.Context,
			req *sip.RequestEnvelope,
			res *sip.ResponseEnvelope,
		) error {
			stampAuthCredentials(req, res)
			return nil
		}}
		var gateCalls []*sip.URI
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			MaxAttempts: 3,
			Timeout:     10 * time.Second,
			AllowTarget: approveAll(&gateCalls),
		}, stub)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerChallenge(t, peer, captured, src, sip.ResponseStatusUnauthorized, wwwChallenge("n1"))

		// The credentialed retry counts against the same budget as
		// redirect hops: send 2 is the retry, send 3 the hop.
		captured, src = readReq(t, peer)
		if got := countAuthHeaders(captured); got != 1 {
			t.Errorf("auth retry carried %d credential headers, want 1", got)
		}
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peer, "carol")}})

		captured, src = readReq(t, peer)
		if got := reqURIUser(t, captured); got != "carol" {
			t.Fatalf("redirected request URI user = %q, want carol", got)
		}
		if got := countAuthHeaders(captured); got != 0 {
			t.Errorf("retargeted request carried %d credential headers, want 0", got)
		}
		replyPeerRes(t, peer, captured, src, sip.ResponseStatusOK)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusOK {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
		}
		if got := stub.calls.Load(); got != 1 {
			t.Fatalf("authenticator calls = %d, want 1", got)
		}
		if got, want := gateUsers(gateCalls), []string{"carol"}; !slices.Equal(got, want) {
			t.Errorf("gate targets = %v, want %v", got, want)
		}
	})

	t.Run("revisited target fails the chain", func(t *testing.T) {
		t.Parallel()

		var gateCalls []*sip.URI
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			Timeout:     10 * time.Second,
			AllowTarget: approveAll(&gateCalls),
		}, nil)
		peer := newRawPeer(t)
		target := peerURI(t, peer, "bob")

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, target, useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peer, "carol")}})

		// Carol redirects back to the already visited initial target:
		// the loop ends the operation with the classified error.
		captured, src = readReq(t, peer)
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peer, "bob")}})
		expectNoDatagram(t, peer)

		res, err := op.Wait(t.Context())
		if !errors.Is(err, useragent.ErrRedirectLoop) {
			t.Fatalf("op.Wait() err = %v, want %v", err, useragent.ErrRedirectLoop)
		}
		if res != nil {
			t.Errorf("op.Wait() response = %v, want nil", res)
		}
	})

	t.Run("denied contacts resolve the last redirect", func(t *testing.T) {
		t.Parallel()

		var gateCalls []*sip.URI
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			Timeout: 10 * time.Second,
			AllowTarget: func(_ context.Context, _ *sip.RequestEnvelope, target *sip.URI) (bool, error) {
				gateCalls = append(gateCalls, target.Clone().(*sip.URI))
				return false, nil
			},
		}, nil)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peer, "carol")}, {URI: peerURI(t, peer, "dave")}})
		expectNoDatagram(t, peer)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusMovedTemporarily {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusMovedTemporarily)
		}
		if got, want := gateUsers(gateCalls), []string{"carol", "dave"}; !slices.Equal(got, want) {
			t.Errorf("gate targets = %v, want %v", got, want)
		}
	})

	t.Run("sips to sip downgrade is rejected", func(t *testing.T) {
		t.Parallel()

		var gateCalls []*sip.URI
		fx := &msgSenderFixture{
			wireElement: newSecureWireElement(t),
			ua: newRedirectUA(t, &useragent.RedirectPolicy{
				Timeout:     10 * time.Second,
				AllowTarget: approveAll(&gateCalls),
			}, nil),
		}
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, securedPeerURI(t, peer, "init"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{
				{URI: peerURI(t, peer, "down")},
				{URI: securedPeerURI(t, peer, "up")},
			})

		// The insecure contact never reaches the gate or the wire; only
		// the secured one is approved and followed.
		captured, src = readReq(t, peer)
		gotURI, ok := captured.URI().(*sip.URI)
		if !ok {
			t.Fatalf("redirected request URI = %T, want *sip.URI", captured.URI())
		}
		if got := gotURI.User.Username(); got != "up" {
			t.Fatalf("redirected request URI user = %q, want up", got)
		}
		if !gotURI.Secured {
			t.Error("redirected request URI is not secured, want sips")
		}
		replyPeerRes(t, peer, captured, src, sip.ResponseStatusOK)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusOK {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
		}
		if len(gateCalls) != 1 || !gateCalls[0].Secured {
			t.Fatalf("gate targets = %v, want only the secured contact", gateCalls)
		}
	})

	t.Run("gate error fails without a send", func(t *testing.T) {
		t.Parallel()

		errGate := errors.Error("gate refusal")
		var gateCalls []*sip.URI
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			Timeout: 10 * time.Second,
			AllowTarget: func(_ context.Context, _ *sip.RequestEnvelope, target *sip.URI) (bool, error) {
				gateCalls = append(gateCalls, target.Clone().(*sip.URI))
				return false, errGate
			},
		}, nil)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peer, "carol")}})
		expectNoDatagram(t, peer)

		res, err := op.Wait(t.Context())
		if !errors.Is(err, errGate) {
			t.Fatalf("op.Wait() error = %v, want wrapped %v", err, errGate)
		}
		if res != nil {
			t.Errorf("op.Wait() response = %v, want nil", res)
		}
		if got := len(gateCalls); got != 1 {
			t.Fatalf("gate calls = %d, want 1", got)
		}
	})

	t.Run("policy timeout bounds the whole chain", func(t *testing.T) {
		t.Parallel()

		var deadlines []time.Time
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			Timeout: 30 * time.Second,
			AllowTarget: func(ctx context.Context, _ *sip.RequestEnvelope, target *sip.URI) (bool, error) {
				dl, ok := ctx.Deadline()
				if !ok {
					t.Error("gate context has no deadline, want the policy deadline")
				}
				deadlines = append(deadlines, dl)
				return true, nil
			},
		}, nil)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		// Two hops run under the same entry-time deadline: a per-hop
		// timeout would give the second gate call a later deadline.
		for _, user := range []string{"carol", "dave"} {
			captured, src := readReq(t, peer)
			replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
				header.Contact{{URI: peerURI(t, peer, user)}})
		}
		captured, src := readReq(t, peer)
		replyPeerRes(t, peer, captured, src, sip.ResponseStatusOK)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusOK {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
		}
		if len(deadlines) != 2 {
			t.Fatalf("gate calls = %d, want 2", len(deadlines))
		}
		if !deadlines[0].Equal(deadlines[1]) {
			t.Errorf("gate deadlines = %v and %v, want the same chain deadline", deadlines[0], deadlines[1])
		}
	})

	t.Run("parallel operations keep independent chains", func(t *testing.T) {
		t.Parallel()

		var gateCalls []*sip.URI
		var gateMu sync.Mutex
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			Timeout: 10 * time.Second,
			AllowTarget: func(_ context.Context, _ *sip.RequestEnvelope, target *sip.URI) (bool, error) {
				gateMu.Lock()
				gateCalls = append(gateCalls, target.Clone().(*sip.URI))
				gateMu.Unlock()
				return true, nil
			},
		}, nil)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op1, err := fx.ua.Message(ctx, peerURI(t, peer, "i1"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("one"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		op2, err := fx.ua.Message(ctx, peerURI(t, peer, "i2"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("two"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op1 == nil || op2 == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		// Both operations redirect to the same contact: a shared visited
		// set would report a loop for the second operation, while
		// independent chains each complete.
		sharedContact := peerURI(t, peer, "common")
		for range 4 {
			captured, src := readReq(t, peer)
			switch reqURIUser(t, captured) {
			case "i1", "i2":
				replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
					header.Contact{{URI: sharedContact}})
			case "common":
				replyPeerRes(t, peer, captured, src, sip.ResponseStatusOK)
			default:
				t.Fatalf("unexpected request URI %v", captured.URI())
			}
		}

		for i, op := range []*useragent.MessageOperation{op1, op2} {
			res, err := op.Wait(t.Context())
			if err != nil {
				t.Fatalf("op%d.Wait() error = %v, want nil", i+1, err)
			}
			if got := res.Status(); got != sip.ResponseStatusOK {
				t.Fatalf("op%d.Wait() status = %v, want %v", i+1, got, sip.ResponseStatusOK)
			}
		}
		gateMu.Lock()
		defer gateMu.Unlock()
		if got, want := gateUsers(gateCalls), []string{"common", "common"}; !slices.Equal(got, want) {
			t.Errorf("gate targets = %v, want %v", got, want)
		}
	})

	t.Run("final failure falls back to a sibling contact", func(t *testing.T) {
		t.Parallel()

		var gateCalls []*sip.URI
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			Timeout:     10 * time.Second,
			AllowTarget: approveAll(&gateCalls),
		}, nil)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peer, "carol")}, {URI: peerURI(t, peer, "dave")}})

		for _, script := range []struct {
			user string
			sts  sip.ResponseStatus
		}{
			{"carol", sip.ResponseStatusBusyHere},
			{"dave", sip.ResponseStatusOK},
		} {
			captured, src = readReq(t, peer)
			if got := reqURIUser(t, captured); got != script.user {
				t.Fatalf("request URI user = %q, want %q", got, script.user)
			}
			replyPeerRes(t, peer, captured, src, script.sts)
		}

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusOK {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
		}
		if got, want := gateUsers(gateCalls), []string{"carol", "dave"}; !slices.Equal(got, want) {
			t.Errorf("gate targets = %v, want %v", got, want)
		}
	})

	t.Run("global failure ends the chain", func(t *testing.T) {
		t.Parallel()

		var gateCalls []*sip.URI
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			Timeout:     10 * time.Second,
			AllowTarget: approveAll(&gateCalls),
		}, nil)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peer, "carol")}, {URI: peerURI(t, peer, "dave")}})

		// A 6xx global failure ends the operation without trying the
		// pending sibling.
		captured, src = readReq(t, peer)
		if got := reqURIUser(t, captured); got != "carol" {
			t.Fatalf("request URI user = %q, want carol", got)
		}
		replyPeerRes(t, peer, captured, src, sip.ResponseStatusDecline)
		expectNoDatagram(t, peer)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusDecline {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusDecline)
		}
		if got, want := gateUsers(gateCalls), []string{"carol"}; !slices.Equal(got, want) {
			t.Errorf("gate targets = %v, want %v", got, want)
		}
	})

	t.Run("target gate receives detached clones", func(t *testing.T) {
		t.Parallel()

		var gateTarget *sip.URI
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			Timeout: 10 * time.Second,
			AllowTarget: func(_ context.Context, req *sip.RequestEnvelope, target *sip.URI) (bool, error) {
				gateTarget = target.Clone().(*sip.URI)
				// Mutating the detached inputs must not alter the
				// candidate that is actually sent.
				target.User = sip.MakeUserInfo("mallory")
				req.WithMessage(func(msg *sip.Request) {
					msg.URI = &sip.URI{
						User: sip.MakeUserInfo("mallory"),
						Addr: sip.MakeHostAddr("mallory.test"),
					}
				})
				return true, nil
			},
		}, nil)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		contact := peerURI(t, peer, "carol")
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: contact}})

		captured, src = readReq(t, peer)
		if !captured.URI().Equal(contact) {
			t.Fatalf("redirected request URI = %v, want %v", captured.URI(), contact)
		}
		replyPeerRes(t, peer, captured, src, sip.ResponseStatusOK)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusOK {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
		}
		if gateTarget == nil || !gateTarget.Equal(contact) {
			t.Errorf("gate target = %v, want %v", gateTarget, contact)
		}
	})

	t.Run("target approved before credentials", func(t *testing.T) {
		t.Parallel()

		// AllowTarget and AuthenticateRequest both run synchronously on
		// the operation worker, so the event order needs no locking.
		var events []string
		stub := &stubAuthenticator{fn: func(
			_ context.Context,
			req *sip.RequestEnvelope,
			res *sip.ResponseEnvelope,
		) error {
			events = append(events, "auth")
			stampAuthCredentials(req, res)
			return nil
		}}
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			Timeout: 10 * time.Second,
			AllowTarget: func(_ context.Context, _ *sip.RequestEnvelope, target *sip.URI) (bool, error) {
				events = append(events, "gate:"+target.User.Username())
				return true, nil
			},
		}, stub)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peer, "bob"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		captured, src := readReq(t, peer)
		replyPeerRedirect(t, peer, captured, src, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peer, "carol")}})

		// The new target's first send carries no credentials; its own
		// challenge is answered only after the gate already approved it.
		captured, src = readReq(t, peer)
		if got := reqURIUser(t, captured); got != "carol" {
			t.Fatalf("redirected request URI user = %q, want carol", got)
		}
		if got := countAuthHeaders(captured); got != 0 {
			t.Errorf("retargeted request carried %d credential headers, want 0", got)
		}
		replyPeerChallenge(t, peer, captured, src, sip.ResponseStatusUnauthorized, wwwChallenge("n1"))

		captured, src = readReq(t, peer)
		if got := reqURIUser(t, captured); got != "carol" {
			t.Fatalf("retry request URI user = %q, want carol", got)
		}
		if got := countAuthHeaders(captured); got != 1 {
			t.Errorf("credentialed retry carried %d credential headers, want 1", got)
		}
		replyPeerRes(t, peer, captured, src, sip.ResponseStatusOK)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusOK {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
		}

		want := []string{"gate:carol", "auth"}
		if !slices.Equal(events, want) {
			t.Errorf("call order = %v, want %v", events, want)
		}
	})
}

func TestUserAgent_RedirectCredentialIsolation(t *testing.T) {
	t.Parallel()

	t.Run("first send to a new target carries no foreign credentials", func(t *testing.T) {
		t.Parallel()

		var (
			traceMu sync.Mutex
			trace   []string
			nonces  []string
		)
		// The gate, the wire, and the authenticator run on different
		// goroutines: validateB is recorded on the operation worker inside
		// the gate, sendB on the test goroutine when B's first packet
		// arrives, and authB on the worker when the authenticator answers
		// B's own challenge.
		record := func(ev string) {
			traceMu.Lock()
			trace = append(trace, ev)
			traceMu.Unlock()
		}

		stub := &stubAuthenticator{fn: func(
			_ context.Context,
			req *sip.RequestEnvelope,
			res *sip.ResponseEnvelope,
		) error {
			nonce := firstDigestNonce(res)
			traceMu.Lock()
			nonces = append(nonces, nonce)
			traceMu.Unlock()
			if nonce == "nB" {
				record("authB")
			}
			stampAuthCredentials(req, res)
			// A second credential family rides along on every
			// authenticated leg, so the retargeted clone must drop
			// Authorization and Proxy-Authorization alike — not just
			// the challenged family.
			req.WithMessage(func(msg *sip.Request) {
				msg.Headers.Append(&header.ProxyAuthorization{
					AuthCredentials: &header.BearerCredentials{Token: "stub-proxy-token"},
				})
			})
			return nil
		}}
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			Timeout: 10 * time.Second,
			AllowTarget: func(_ context.Context, _ *sip.RequestEnvelope, target *sip.URI) (bool, error) {
				record("validate" + target.User.Username())
				return true, nil
			},
		}, stub)
		peerA := newRawPeer(t)
		peerB := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peerA, "A"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		// A challenges the anonymous initial request; the authenticator
		// stamps both credential families on the same-target retry.
		capturedA, srcA := readReq(t, peerA)
		if got := countAuthHeaders(capturedA); got != 0 {
			t.Fatalf("initial request carried %d credential headers, want 0", got)
		}
		replyPeerChallenge(t, peerA, capturedA, srcA, sip.ResponseStatusUnauthorized,
			wwwChallenge("nA"), proxyChallenge("pA"))

		capturedA, srcA = readReq(t, peerA)
		if got := countAuthHeaders(capturedA); got != 2 {
			t.Fatalf("authenticated retry carried %d credential headers, want 2", got)
		}
		replyPeerRedirect(t, peerA, capturedA, srcA, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peerB, "B")}})

		// B's first packet may leave only after the gate approves the new
		// target, and it is always credential-free: A's credentials never
		// cross targets.
		capturedB, srcB := readReq(t, peerB)
		record("sendB")
		if got := reqURIUser(t, capturedB); got != "B" {
			t.Fatalf("request URI user = %q, want B", got)
		}
		replyPeerChallenge(t, peerB, capturedB, srcB, sip.ResponseStatusUnauthorized, wwwChallenge("nB"))

		// Only B's own challenge re-arms the authenticator; the retry
		// clone stays on B and carries the fresh credentials.
		captured, src := readReq(t, peerB)
		if got := reqURIUser(t, captured); got != "B" {
			t.Fatalf("retry request URI user = %q, want B", got)
		}
		if got := countAuthHeaders(captured); got != 2 {
			t.Errorf("credentialed retry to B carried %d credential headers, want 2", got)
		}
		replyPeerRes(t, peerB, captured, src, sip.ResponseStatusOK)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusOK {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
		}

		traceMu.Lock()
		defer traceMu.Unlock()
		if diff := cmp.Diff([]string{"validateB", "sendB", "authB"}, trace); diff != "" {
			t.Fatalf("redirect trust ordering mismatch (-want +got):\n%s", diff)
		}
		if creds := countAuthHeaders(capturedB); creds != 0 {
			t.Fatalf("first request to B carried %d credential headers, want 0", creds)
		}
		if diff := cmp.Diff([]string{"nA", "nB"}, nonces); diff != "" {
			t.Fatalf("authenticator challenges mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("denied target stays silent", func(t *testing.T) {
		t.Parallel()

		var (
			gateMu sync.Mutex
			gated  []*sip.URI
		)
		stub := &stubAuthenticator{fn: func(
			_ context.Context,
			req *sip.RequestEnvelope,
			res *sip.ResponseEnvelope,
		) error {
			stampAuthCredentials(req, res)
			return nil
		}}
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			Timeout: 10 * time.Second,
			AllowTarget: func(_ context.Context, _ *sip.RequestEnvelope, target *sip.URI) (bool, error) {
				gateMu.Lock()
				gated = append(gated, target.Clone().(*sip.URI))
				gateMu.Unlock()
				return false, nil
			},
		}, stub)
		peerA := newRawPeer(t)
		peerB := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peerA, "A"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		capturedA, srcA := readReq(t, peerA)
		replyPeerChallenge(t, peerA, capturedA, srcA, sip.ResponseStatusUnauthorized, wwwChallenge("nA"))

		// A's credentialed retry redirects to B, but the gate denies the
		// new target: B sees no packet and the authenticator is never
		// re-armed for it.
		capturedA, srcA = readReq(t, peerA)
		replyPeerRedirect(t, peerA, capturedA, srcA, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peerB, "B")}})
		expectNoDatagram(t, peerB)
		expectNoDatagram(t, peerA)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusMovedTemporarily {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusMovedTemporarily)
		}
		if got := stub.calls.Load(); got != 1 {
			t.Fatalf("authenticator calls = %d, want 1", got)
		}
		gateMu.Lock()
		defer gateMu.Unlock()
		if got, want := gateUsers(gated), []string{"B"}; !slices.Equal(got, want) {
			t.Errorf("gate targets = %v, want %v", got, want)
		}
	})

	t.Run("canceled gate context stays silent", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		var gateErr error
		stub := &stubAuthenticator{fn: func(
			_ context.Context,
			req *sip.RequestEnvelope,
			res *sip.ResponseEnvelope,
		) error {
			stampAuthCredentials(req, res)
			return nil
		}}
		fx := newRedirectSender(t, &useragent.RedirectPolicy{
			Timeout: 10 * time.Second,
			AllowTarget: func(gctx context.Context, _ *sip.RequestEnvelope, _ *sip.URI) (bool, error) {
				// Even an approving answer under a canceled operation
				// context must produce no IO: the retargeted send is
				// gated by the same context the gate observed.
				cancel()
				gateErr = gctx.Err()
				return true, nil
			},
		}, stub)
		peerA := newRawPeer(t)
		peerB := newRawPeer(t)

		ctx = sip.ContextWithElement(ctx, fx.elm)
		op, err := fx.ua.Message(ctx, peerURI(t, peerA, "A"), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		capturedA, srcA := readReq(t, peerA)
		replyPeerChallenge(t, peerA, capturedA, srcA, sip.ResponseStatusUnauthorized, wwwChallenge("nA"))

		// A's credentialed retry redirects to B; the gate approves while
		// canceling the operation, so neither peer may see another
		// datagram and B's challenge never reaches the authenticator.
		capturedA, srcA = readReq(t, peerA)
		replyPeerRedirect(t, peerA, capturedA, srcA, sip.ResponseStatusMovedTemporarily,
			header.Contact{{URI: peerURI(t, peerB, "B")}})
		expectNoDatagram(t, peerB)
		expectNoDatagram(t, peerA)

		res, err := op.Wait(t.Context())
		if res != nil {
			t.Errorf("op.Wait() response = %v, want nil", res)
		}
		if !errors.Is(err, useragent.ErrCanceled) || !errclass.IsCanceled(err) {
			t.Fatalf("op.Wait() error = %v, want canceled %v", err, useragent.ErrCanceled)
		}
		if !errors.Is(gateErr, context.Canceled) {
			t.Errorf("gate context error = %v, want %v", gateErr, context.Canceled)
		}
		if got := stub.calls.Load(); got != 1 {
			t.Fatalf("authenticator calls = %d, want 1", got)
		}
	})
}
