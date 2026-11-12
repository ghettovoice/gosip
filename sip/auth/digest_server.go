package auth

import (
	"context"
	"math"
	"slices"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

const (
	// defaultDigestNonceTTL bounds how long an issued server nonce can
	// produce a valid proof.
	defaultDigestNonceTTL = 5 * time.Minute
	// defaultDigestServerNonceCapacity bounds issued server nonce records;
	// the oldest issued record is evicted first.
	defaultDigestServerNonceCapacity = 4096
)

// DigestServerOptions configures a Digest server authenticator for one
// protection space.
type DigestServerOptions[P any] struct {
	ServerOptions
	// Algorithms is an allowlist in challenge preference order: nil means
	// SHA-256 only, an explicitly empty list is an error, and MD5 must be
	// listed explicitly for legacy interop. The server advertises one
	// challenge per enabled algorithm in this order.
	Algorithms []string
	// NonceTTL bounds proof validity for an issued nonce; zero selects
	// five minutes, negative is an error.
	NonceTTL time.Duration
	// MaxNonces bounds issued nonce records; zero selects 4096, negative
	// is an error, and it must hold at least one complete algorithm
	// challenge set.
	MaxNonces int
	// LookupHA1 returns the lowercase algorithm-sized hexadecimal HA1 —
	// computed with DigestHA1 or stored precomputed — and the principal
	// for an account. Return sip.ErrInvalidCredentials for a missing or
	// unacceptable account; any other error is an ordinary operational
	// failure, never a challenge. The callback must not log or otherwise
	// expose the HA1, the account password, or the presented proof.
	LookupHA1 func(ctx context.Context, username, realm, algorithm string) (ha1 string, principal P, err error)
	// ValidateURI, when supplied, replaces the default target check —
	// the parsed credential URI's Equal against the request URI — and
	// must accept only the direct or legitimately forwarded targets the
	// application is willing to authenticate. It receives detached
	// clones. Return sip.ErrInvalidCredentials to reject the target; any
	// other error is an operational failure.
	ValidateURI func(ctx context.Context, req *sip.Request, target uri.URI) error
}

// DigestServer implements sip.ServerAuthenticator for RFC 7616 Digest
// over SIP. It validates Authorization/Proxy-Authorization credentials on
// the caller's request and returns the lookup's principal directly — the
// zero value of P on every failure. It never sends a response, mutates
// the request, authorizes the resulting action, or treats request fields
// such as From as identity.
//
// Issued nonces live in bounded in-memory state (default five-minute TTL,
// 4096 oldest-evicted records) that is local to the instance: it does not
// span restarts or replicas and provides no distributed replay protection,
// so credentials presented after a restart or on another replica are
// re-challenged. Accepted nonce counts must be positive and strictly
// increasing per nonce, which makes an already-consumed proof a replay;
// retransmitted requests are handled by the SIP layer's own retransmission
// rules, not by re-invoking authentication.
type DigestServer[P any] struct {
	realm       string
	proxy       bool
	algos       []string
	nonceTTL    time.Duration
	maxNonces   int
	lookupHA1   func(context.Context, string, string, string) (string, P, error)
	validateURI func(context.Context, *sip.Request, uri.URI) error
	store       *nonceStore
}

// NewDigestServer validates the configuration and returns a ready server.
// It performs no callback, sends nothing, and starts no goroutine.
func NewDigestServer[P any](opts DigestServerOptions[P]) (*DigestServer[P], error) {
	if opts.Realm == "" {
		return nil, errors.ErrorWrap("missing digest realm")
	}
	if opts.LookupHA1 == nil {
		return nil, errors.ErrorWrap("missing HA1 lookup")
	}
	if opts.NonceTTL < 0 {
		return nil, errors.ErrorWrap("negative nonce ttl")
	}
	if opts.MaxNonces < 0 {
		return nil, errors.ErrorWrap("negative nonce capacity")
	}
	algos, err := digestAlgoPolicy(opts.Algorithms)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	maxNonces := opts.MaxNonces
	if maxNonces == 0 {
		maxNonces = defaultDigestServerNonceCapacity
	}
	if maxNonces < len(algos) {
		return nil, errors.ErrorWrap("nonce capacity cannot hold the algorithm challenge set")
	}
	nonceTTL := opts.NonceTTL
	if nonceTTL == 0 {
		nonceTTL = defaultDigestNonceTTL
	}
	return &DigestServer[P]{
		realm:       opts.Realm,
		proxy:       opts.Proxy,
		algos:       algos,
		nonceTTL:    nonceTTL,
		maxNonces:   maxNonces,
		lookupHA1:   opts.LookupHA1,
		validateURI: opts.ValidateURI,
		store:       newNonceStore(maxNonces),
	}, nil
}

// AuthenticateRequest verifies the request's Digest credentials for the
// configured realm and leg. On success it returns the lookup's principal,
// which the application owns and may store or wrap as it sees fit; on every
// failure it returns the zero P plus either an ordinary error or a
// *sip.RequestRejectedError challenge — the caller decides whether and how
// to answer.
//
// Missing, malformed, replayed, and wrong-realm credentials produce a
// fresh challenge; a known-but-expired nonce with an otherwise valid proof
// produces a stale=true challenge. LookupHA1's sip.ErrInvalidCredentials
// additionally marks the challenge cause as an explicit negative result,
// while any other callback error is an ordinary operational failure, never
// a challenge. Nothing here logs or exposes usernames, passwords, HA1
// values, or proofs, and proof verification gives no whole-request
// constant-time guarantee for the account lookup itself.
func (s *DigestServer[P]) AuthenticateRequest(ctx context.Context, req *sip.RequestEnvelope) (P, error) {
	var zero P
	if s == nil || s.store == nil {
		return zero, errors.ErrorWrap("uninitialized digest server")
	}
	reqMsg, err := serverMsg(ctx, req)
	if err != nil {
		return zero, errors.Wrap(err)
	}

	var stale, negative bool
	seq := reqMsg.Headers.AuthorizationCredentials
	if s.proxy {
		seq = reqMsg.Headers.ProxyAuthorizationCredentials
	}
	for creds := range seq() {
		dc, ok := creds.(*header.DigestCredentials)
		if !ok || dc == nil || dc.Realm != s.realm {
			continue
		}
		principal, outcome, err := s.verify(ctx, reqMsg, dc)
		if err != nil {
			return zero, errors.Wrap(err)
		}
		switch outcome {
		case nonceAccepted:
			if err := ctx.Err(); err != nil {
				return zero, errors.Wrap(err)
			}
			return principal, nil
		case nonceInvalid:
			// fresh challenge without a negative-credential cause
		case nonceExpired:
			stale = true
		case outcomeInvalidCreds:
			negative = true
		}
	}

	return zero, errors.Wrap(s.chal(ctx, stale, negative))
}

// outcomeInvalidCreds marks a candidate rejected by an explicit
// ErrInvalidCredentials callback answer, so the challenge cause keeps the
// negative discoverable.
const outcomeInvalidCreds nonceOutcome = -1

// verify runs one matching-realm candidate through the verification
// pipeline. It never mutates the request or the nonce store except
// through commit after a constant-time verified proof.
func (s *DigestServer[P]) verify(ctx context.Context, req *sip.Request, dc *header.DigestCredentials) (P, nonceOutcome, error) {
	var zero P

	if !dc.IsValid() {
		return zero, nonceInvalid, nil
	}
	algo, _ := normalizeDigestAlgo(dc.Algorithm)
	if algo == "" || !slices.Contains(s.algos, algo) {
		return zero, nonceInvalid, nil
	}
	if dc.Username == "" || dc.Nonce == "" || dc.CNonce == "" || dc.QOP != digestQOPAuth {
		return zero, nonceInvalid, nil
	}
	if dc.NonceCount == 0 || dc.NonceCount > math.MaxUint32 {
		return zero, nonceInvalid, nil
	}
	rec, ok := s.store.snapshot(dc.Nonce)
	if !ok || rec.realm != s.realm || rec.algo != algo {
		return zero, nonceInvalid, nil
	}
	if err := ctx.Err(); err != nil {
		return zero, nonceInvalid, errors.Wrap(err)
	}

	if s.validateURI != nil {
		vreq, _ := req.Clone().(*sip.Request)
		vuri := shared.Clone[uri.URI](dc.URI)
		err := s.validateURI(ctx, vreq, vuri)
		switch {
		case err == nil:
		case errors.Is(err, sip.ErrInvalidCredentials):
			return zero, outcomeInvalidCreds, nil
		default:
			return zero, nonceInvalid, errors.Wrap(err)
		}
	} else if !shared.IsValid(dc.URI) || !dc.URI.Equal(req.URI) {
		return zero, nonceInvalid, nil
	}
	if err := ctx.Err(); err != nil {
		return zero, nonceInvalid, errors.Wrap(err)
	}

	ha1, principal, err := s.lookupHA1(ctx, dc.Username, s.realm, algo)
	switch {
	case err == nil:
	case errors.Is(err, sip.ErrInvalidCredentials):
		return zero, outcomeInvalidCreds, nil
	default:
		return zero, nonceInvalid, errors.Wrap(err)
	}
	if err := ctx.Err(); err != nil {
		return zero, nonceInvalid, errors.Wrap(err)
	}

	want, err := digestRes(
		ha1, string(req.Method), dc.URIText(), dc.Nonce, dc.CNonce, uint32(dc.NonceCount), algo,
	)
	if err != nil {
		// A malformed backend HA1 is an operational failure, not a bad
		// password challenge.
		return zero, nonceInvalid, errors.Wrap(err)
	}
	if !matchDigestRes(dc.Response, want, algo) {
		return zero, nonceInvalid, nil
	}

	outcome, err := s.store.commit(ctx, dc.Nonce, s.realm, algo, dc.Username, uint32(dc.NonceCount))
	if err != nil {
		return zero, nonceInvalid, errors.Wrap(err)
	}
	return principal, outcome, nil
}

// chal issues a fresh nonce per enabled algorithm and builds the
// typed rejection. A verified proof against a retained expired nonce is
// the only stale=true path; missing, malformed, foreign, or fabricated
// credentials all receive the same fresh chal shape.
func (s *DigestServer[P]) chal(ctx context.Context, stale, negative bool) error {
	nonces, err := s.store.issue(ctx, s.realm, s.algos, s.nonceTTL)
	if err != nil {
		return errors.Wrap(err)
	}

	hdr := func(i int) sip.Header {
		cln := &header.DigestChallenge{
			Realm:     s.realm,
			Nonce:     nonces[i],
			Algorithm: s.algos[i],
			QOP:       []string{digestQOPAuth},
			Stale:     stale,
		}
		if s.proxy {
			return &header.ProxyAuthenticate{AuthChallenge: cln}
		}
		return &header.WWWAuthenticate{AuthChallenge: cln}
	}
	hdrs := make(sip.Headers)
	for i := range nonces {
		if i == 0 {
			hdrs = hdrs.Set(hdr(i))
		} else {
			hdrs = hdrs.Append(hdr(i))
		}
	}

	cause := error(sip.ErrAuthenticationRequired)
	if negative {
		cause = errors.Join(cause, sip.ErrInvalidCredentials)
	}
	return authRejection(s.proxy, cause, hdrs)
}
