package useragent_test

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/useragent"
)

// stubAuthenticator is a recording [sip.ClientAuthenticator] stub: calls
// counts every invocation and fn, when set, supplies the call behavior.
type stubAuthenticator struct {
	calls atomic.Int32
	fn    func(context.Context, *sip.RequestEnvelope, *sip.ResponseEnvelope) error
}

func (s *stubAuthenticator) AuthenticateRequest(
	ctx context.Context,
	req *sip.RequestEnvelope,
	res *sip.ResponseEnvelope,
) error {
	s.calls.Add(1)
	if s.fn == nil {
		return nil
	}
	return s.fn(ctx, req, res)
}

// errStubAuth is the sentinel a failing stubAuthenticator returns.
var errStubAuth = errors.Error("stub authenticator failure")

// noRetransmitTiming stretches Timer E far beyond the test window, so the
// scripted raw peer observes exactly one datagram per logical send: an
// RFC 3261 non-INVITE transaction would otherwise retransmit the request
// while a scripted reply is still in flight.
var noRetransmitTiming = sip.TimingConfig{T1: time.Minute}

// newAuthSender builds a wire element and a sender user agent configured
// with auth. It mirrors newMsgSender, and the configured contact proves a
// MESSAGE retry clone never gains one.
func newAuthSender(tb testing.TB, auth sip.ClientAuthenticator) *msgSenderFixture {
	tb.Helper()

	contact, err := sip.ParseURI("sip:alice@pc.ua.test")
	if err != nil {
		tb.Fatalf("sip.ParseURI() error = %v, want nil", err)
	}
	aor := msgAOR(tb)
	ua, err := useragent.New(useragent.Options{
		AOR:           aor,
		Contact:       contact,
		Authenticator: auth,
	})
	if err != nil {
		tb.Fatalf("useragent.New() error = %v, want nil", err)
	}
	return &msgSenderFixture{wireElement: newWireElem(tb), ua: ua, aor: aor}
}

// countAuthHeaders counts the Authorization and Proxy-Authorization
// headers a captured request carries.
func countAuthHeaders(req *sip.RequestEnvelope) int {
	hdrs := req.Headers()
	return len(hdrs.Get("Authorization")) + len(hdrs.Get("Proxy-Authorization"))
}

// wwwChallenge and proxyChallenge build the two challenge header families a
// peer sends in a 401/407 response.
func wwwChallenge(nonce string) *header.WWWAuthenticate {
	return &header.WWWAuthenticate{
		AuthChallenge: &header.DigestChallenge{Realm: "ua.test", Nonce: nonce},
	}
}

func proxyChallenge(nonce string) *header.ProxyAuthenticate {
	return &header.ProxyAuthenticate{
		AuthChallenge: &header.DigestChallenge{Realm: "proxy.test", Nonce: nonce},
	}
}

// replyPeerChallenge answers a captured raw-peer request with sts and the
// given authentication challenge headers.
func replyPeerChallenge(
	tb testing.TB,
	peer *net.UDPConn,
	captured *sip.RequestEnvelope,
	src netip.AddrPort,
	sts sip.ResponseStatus,
	chals ...sip.Header,
) {
	tb.Helper()

	hdrs := make(sip.Headers)
	for _, h := range chals {
		hdrs.Append(h)
	}
	res, err := captured.NewResponse(sts, sip.ResponseOptions{Headers: hdrs})
	if err != nil {
		tb.Fatalf("captured.NewResponse(%v) error = %v, want nil", sts, err)
	}
	sendPeerRes(tb, peer, src, res)
}

// firstDigestNonce extracts the nonce of the first Digest challenge found
// in either challenge family of a received response.
func firstDigestNonce(res *sip.ResponseEnvelope) string {
	for chal := range res.Headers().AuthenticateChallenges() {
		if dc, ok := chal.(*header.DigestChallenge); ok {
			return dc.Nonce
		}
	}
	for chal := range res.Headers().ProxyAuthenticateChallenges() {
		if dc, ok := chal.(*header.DigestChallenge); ok {
			return dc.Nonce
		}
	}
	return ""
}

// challengeNonce is the fatal-on-missing variant of firstDigestNonce for
// assertions on the test goroutine.
func challengeNonce(tb testing.TB, res *sip.ResponseEnvelope) string {
	tb.Helper()

	if nonce := firstDigestNonce(res); nonce != "" {
		return nonce
	}
	tb.Fatalf("response carries no digest challenge:\n%v", res)
	return ""
}

// stampAuthCredentials applies a Bearer credential header to req in the
// family matching the received challenge status: Authorization answers a
// 401 challenge and Proxy-Authorization a 407 challenge.
func stampAuthCredentials(req *sip.RequestEnvelope, res *sip.ResponseEnvelope) {
	req.WithMessage(func(msg *sip.Request) {
		creds := &header.BearerCredentials{Token: "stub-token"}
		if res.Status().Equal(sip.ResponseStatusProxyAuthenticationRequired) {
			msg.Headers.Append(&header.ProxyAuthorization{AuthCredentials: creds})
			return
		}
		msg.Headers.Append(&header.Authorization{AuthCredentials: creds})
	})
}

func TestUserAgent_AuthenticationRetry(t *testing.T) {
	t.Parallel()

	t.Run("challenge then success", func(t *testing.T) {
		t.Parallel()

		stub := &stubAuthenticator{fn: func(
			_ context.Context,
			req *sip.RequestEnvelope,
			res *sip.ResponseEnvelope,
		) error {
			stampAuthCredentials(req, res)
			return nil
		}}
		fx := newAuthSender(t, stub)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, uriFor(msgPeerAddr(t, peer)), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		// The captured initial send is the pristine snapshot: the
		// authenticator may mutate only the retry clone, never the
		// transaction's request.
		initial, src := readReq(t, peer)
		if got := countAuthHeaders(initial); got != 0 {
			t.Fatalf("initial request carried %d credential headers, want 0", got)
		}
		initHdrs := initial.Headers()
		initVia, ok := initHdrs.FirstVia()
		if !ok || initVia == nil {
			t.Fatal("initial request has no Via header")
		}
		initBranch, _ := initVia.Branch()
		initCallID, _ := initHdrs.CallID()
		initFrom, _ := initHdrs.From()
		initTag, _ := initFrom.Tag()
		initTo, _ := initHdrs.To()

		replyPeerChallenge(t, peer, initial, src, sip.ResponseStatusUnauthorized, wwwChallenge("n1"))

		retry, src2 := readReq(t, peer)
		retryHdrs := retry.Headers()

		retryCSeq, ok := retryHdrs.CSeq()
		if !ok || retryCSeq == nil {
			t.Fatal("retry request has no CSeq header")
		}
		if retryCSeq.SeqNum != 2 {
			t.Fatalf("retry CSeq = %d, want 2", retryCSeq.SeqNum)
		}
		if !retryCSeq.Method.Equal(sip.RequestMethodMessage) {
			t.Errorf("retry CSeq method = %v, want %v", retryCSeq.Method, sip.RequestMethodMessage)
		}

		if callID, _ := retryHdrs.CallID(); callID != initCallID {
			t.Errorf("retry Call-ID = %v, want %v", callID, initCallID)
		}
		from, _ := retryHdrs.From()
		if tag, _ := from.Tag(); tag != initTag {
			t.Errorf("retry From tag = %q, want %q", tag, initTag)
		}
		to, _ := retryHdrs.To()
		if !to.Equal(initTo) {
			t.Errorf("retry To = %v, want %v", to, initTo)
		}

		via, _ := retryHdrs.FirstVia()
		branch, _ := via.Branch()
		if branch == initBranch || !sip.IsRFC3261Branch(branch) {
			t.Errorf("retry Via branch = %q, want a fresh RFC 3261 branch (initial %q)", branch, initBranch)
		}

		if got := len(retryHdrs.Get("Authorization")); got != 1 {
			t.Errorf("retry carried %d Authorization headers, want 1", got)
		}
		if got := len(retryHdrs.Get("Proxy-Authorization")); got != 0 {
			t.Errorf("retry carried %d Proxy-Authorization headers, want 0", got)
		}
		if n := len(slices.Collect(retryHdrs.Contacts())); n != 0 {
			t.Errorf("retry carried %d Contact headers, want 0", n)
		}

		replyPeerRes(t, peer, retry, src2, sip.ResponseStatusOK)

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
	})

	t.Run("server then proxy challenge", func(t *testing.T) {
		t.Parallel()

		var gotChallenges []sip.ResponseStatus
		stub := &stubAuthenticator{fn: func(
			_ context.Context,
			req *sip.RequestEnvelope,
			res *sip.ResponseEnvelope,
		) error {
			gotChallenges = append(gotChallenges, res.Status())
			stampAuthCredentials(req, res)
			return nil
		}}
		fx := newAuthSender(t, stub)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, uriFor(msgPeerAddr(t, peer)), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		initial, src := readReq(t, peer)
		replyPeerChallenge(t, peer, initial, src, sip.ResponseStatusUnauthorized, wwwChallenge("n1"))

		// The first retry answers the 401 challenge and carries only the
		// Authorization family.
		retry1, src1 := readReq(t, peer)
		retry1Hdrs := retry1.Headers()
		if got := len(retry1Hdrs.Get("Authorization")); got != 1 {
			t.Errorf("first retry carried %d Authorization headers, want 1", got)
		}
		if got := len(retry1Hdrs.Get("Proxy-Authorization")); got != 0 {
			t.Errorf("first retry carried %d Proxy-Authorization headers, want 0", got)
		}
		replyPeerChallenge(t, peer, retry1, src1, sip.ResponseStatusProxyAuthenticationRequired,
			proxyChallenge("p1"))

		// The second retry answers the 407 challenge; the same-target
		// retry clone keeps the first leg's credentials.
		retry2, src2 := readReq(t, peer)
		retry2Hdrs := retry2.Headers()
		if got := len(retry2Hdrs.Get("Authorization")); got != 1 {
			t.Errorf("second retry carried %d Authorization headers, want 1", got)
		}
		if got := len(retry2Hdrs.Get("Proxy-Authorization")); got != 1 {
			t.Errorf("second retry carried %d Proxy-Authorization headers, want 1", got)
		}
		if cseq, _ := retry2Hdrs.CSeq(); cseq.SeqNum != 3 {
			t.Errorf("second retry CSeq = %d, want 3", cseq.SeqNum)
		}
		replyPeerRes(t, peer, retry2, src2, sip.ResponseStatusOK)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusOK {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusOK)
		}
		if got := stub.calls.Load(); got != 2 {
			t.Fatalf("authenticator calls = %d, want 2", got)
		}
		// Each leg hands the provider its own challenge.
		wantChallenges := []sip.ResponseStatus{
			sip.ResponseStatusUnauthorized,
			sip.ResponseStatusProxyAuthenticationRequired,
		}
		if !slices.Equal(gotChallenges, wantChallenges) {
			t.Errorf("provider challenge statuses = %v, want %v", gotChallenges, wantChallenges)
		}
	})

	t.Run("attempt limit keeps the last challenge", func(t *testing.T) {
		t.Parallel()

		stub := &stubAuthenticator{fn: func(
			_ context.Context,
			req *sip.RequestEnvelope,
			res *sip.ResponseEnvelope,
		) error {
			stampAuthCredentials(req, res)
			return nil
		}}
		fx := newAuthSender(t, stub)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, uriFor(msgPeerAddr(t, peer)), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		// The default bound of 3 authentication attempts means the
		// initial send plus three retries: four wire sends and three
		// provider calls, with a distinct nonce per leg.
		const sends = 4
		for i := range sends {
			captured, src := readReq(t, peer)
			replyPeerChallenge(t, peer, captured, src, sip.ResponseStatusUnauthorized,
				wwwChallenge(fmt.Sprintf("n%d", i+1)))
		}
		expectNoDatagram(t, peer)

		// The outcome carries the last challenge response together with
		// the classified limit error.
		res, err := op.Wait(t.Context())
		if !errors.Is(err, useragent.ErrAuthenticationLimited) {
			t.Fatalf("op.Wait() error = %v, want %v", err, useragent.ErrAuthenticationLimited)
		}
		if res == nil || res.Status() != sip.ResponseStatusUnauthorized {
			t.Fatalf("op.Wait() response = %v, want last 401 challenge alongside the error", res)
		}
		if got := challengeNonce(t, res); got != "n4" {
			t.Errorf("final challenge nonce = %q, want %q", got, "n4")
		}
		if got := stub.calls.Load(); got != 3 {
			t.Fatalf("authenticator calls = %d, want 3", got)
		}
	})

	t.Run("no authenticator resolves the challenge", func(t *testing.T) {
		t.Parallel()

		fx := newMsgSender(t)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, uriFor(msgPeerAddr(t, peer)), useragent.MessageOperationOptions{
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
		expectNoDatagram(t, peer)

		res, err := op.Wait(t.Context())
		if err != nil {
			t.Fatalf("op.Wait() error = %v, want nil", err)
		}
		if got := res.Status(); got != sip.ResponseStatusUnauthorized {
			t.Fatalf("op.Wait() status = %v, want %v", got, sip.ResponseStatusUnauthorized)
		}
	})

	t.Run("provider error fails without a send", func(t *testing.T) {
		t.Parallel()

		stub := &stubAuthenticator{fn: func(
			context.Context,
			*sip.RequestEnvelope,
			*sip.ResponseEnvelope,
		) error {
			return errStubAuth
		}}
		fx := newAuthSender(t, stub)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, uriFor(msgPeerAddr(t, peer)), useragent.MessageOperationOptions{
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

		// A provider failure is terminal: no retry packet is queued.
		expectNoDatagram(t, peer)

		res, err := op.Wait(t.Context())
		if res != nil {
			t.Errorf("op.Wait() response = %v, want nil", res)
		}
		if !errors.Is(err, errStubAuth) {
			t.Fatalf("op.Wait() error = %v, want wrapped %v", err, errStubAuth)
		}
		if got := stub.calls.Load(); got != 1 {
			t.Fatalf("authenticator calls = %d, want 1", got)
		}
	})

	t.Run("cancel during the provider blocks the retry", func(t *testing.T) {
		t.Parallel()

		entered := make(chan struct{})
		stub := &stubAuthenticator{fn: func(
			ctx context.Context,
			_ *sip.RequestEnvelope,
			_ *sip.ResponseEnvelope,
		) error {
			close(entered)
			<-ctx.Done()
			return nil
		}}
		fx := newAuthSender(t, stub)
		peer := newRawPeer(t)

		ctx, cancel := context.WithCancel(sip.ContextWithElement(t.Context(), fx.elm))
		op, err := fx.ua.Message(ctx, uriFor(msgPeerAddr(t, peer)), useragent.MessageOperationOptions{
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

		// The cancellation lands while the provider runs; the operation
		// context is re-checked after it returns, so no retry is queued.
		waitChan(t, entered, "authenticator entry")
		cancel()

		res, err := op.Wait(t.Context())
		if res != nil {
			t.Errorf("op.Wait() response = %v, want nil", res)
		}
		if !errors.Is(err, useragent.ErrCanceled) || !errclass.IsCanceled(err) {
			t.Fatalf("op.Wait() error = %v, want canceled %v", err, useragent.ErrCanceled)
		}
		expectNoDatagram(t, peer)
	})

	t.Run("provider sees a detached challenge clone", func(t *testing.T) {
		t.Parallel()

		var gotNonces []string
		stub := &stubAuthenticator{fn: func(
			_ context.Context,
			req *sip.RequestEnvelope,
			res *sip.ResponseEnvelope,
		) error {
			gotNonces = append(gotNonces, firstDigestNonce(res))
			// The challenge handed to the provider is a detached clone:
			// corrupting it must not reach the stored outcome.
			res.WithMessage(func(msg *sip.Response) {
				msg.Status = sip.ResponseStatusOK
				msg.Headers.Delete("WWW-Authenticate")
			})
			stampAuthCredentials(req, res)
			return nil
		}}
		fx := newAuthSender(t, stub)
		peer := newRawPeer(t)

		ctx := sip.ContextWithElement(t.Context(), fx.elm)
		op, err := fx.ua.Message(ctx, uriFor(msgPeerAddr(t, peer)), useragent.MessageOperationOptions{
			Timing: noRetransmitTiming,
			Body:   []byte("ping"),
		})
		if err != nil {
			t.Fatalf("ua.Message() error = %v, want nil", err)
		}
		if op == nil {
			t.Fatal("ua.Message() op = nil, want non-nil")
		}

		const sends = 4
		for i := range sends {
			captured, src := readReq(t, peer)
			replyPeerChallenge(t, peer, captured, src, sip.ResponseStatusUnauthorized,
				wwwChallenge(fmt.Sprintf("n%d", i+1)))
		}
		expectNoDatagram(t, peer)

		res, err := op.Wait(t.Context())
		if !errors.Is(err, useragent.ErrAuthenticationLimited) {
			t.Fatalf("op.Wait() error = %v, want %v", err, useragent.ErrAuthenticationLimited)
		}
		if res == nil || res.Status() != sip.ResponseStatusUnauthorized {
			t.Fatalf("op.Wait() response = %v, want last 401 challenge alongside the error", res)
		}
		if got := challengeNonce(t, res); got != "n4" {
			t.Errorf("final challenge nonce = %q, want %q", got, "n4")
		}

		// Each provider call saw its own leg's challenge — never a
		// replayed earlier one — and the mutations stayed inside the
		// clones.
		wantNonces := []string{"n1", "n2", "n3"}
		if !slices.Equal(gotNonces, wantNonces) {
			t.Errorf("provider challenge nonces = %v, want %v", gotNonces, wantNonces)
		}
	})
}
