package auth

import (
	"container/list"
	"context"
	"crypto/rand"
	"encoding/hex"
	"math"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

const (
	// defDigestNonceCap bounds the client's in-memory nonce
	// sessions; the oldest evicted session restarts with a fresh cnonce.
	defDigestNonceCap = 1024
	digestCNonceBytes = 16
)

// DigestClientOptions configures a Digest client authenticator.
type DigestClientOptions struct {
	// Realm selects the protection space this instance answers; the client
	// ignores challenges for any other realm (case-sensitive).
	Realm string
	// Algorithms is an allowlist, not a preference order: nil means
	// SHA-256 only, an explicitly empty list is an error, and MD5 must be
	// listed explicitly for legacy interop. Selection prefers SHA-256 over
	// MD5 independently of slice or challenge order; challenge wire order
	// only breaks same-algorithm ties.
	Algorithms []string
	// MaxNonces bounds per-(leg,nonce) credential sessions; zero means
	// 1024, negative is an error.
	MaxNonces int
	// Credentials returns the username and password for the challenge.
	// It receives detached clones of the request and challenge; mutating
	// them has no effect on the applied credentials. proxy is false for
	// the WWW-Authenticate/Authorization leg and true for the
	// Proxy-Authenticate/Proxy-Authorization leg, so the same realm on
	// different legs may select different accounts.
	Credentials func(
		ctx context.Context,
		req *sip.Request,
		challenge *header.DigestChallenge,
		proxy bool,
	) (username, password string, err error)
}

// DigestClient implements sip.ClientAuthenticator for RFC 7616 Digest over
// SIP. It only applies credential headers to the caller-owned retry request:
// sending, retries, correlation, and CSeq/Via ownership stay with the
// caller. Nonce-count state is finite, instance-local, and does not span
// restarts or replicas.
type DigestClient struct {
	realm     string
	algos     []string
	maxNonces int
	creds     func(context.Context, *sip.Request, *header.DigestChallenge, bool) (string, string, error)

	mu   sync.Mutex
	sess map[digestNonceSessKey]*digestNonceSess
	lru  list.List
}

// NewDigestClient validates the configuration and returns a ready client.
func NewDigestClient(opts DigestClientOptions) (*DigestClient, error) {
	if opts.Realm == "" {
		return nil, errors.ErrorWrap("missing digest realm")
	}
	if opts.Credentials == nil {
		return nil, errors.ErrorWrap("missing credentials provider")
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
		maxNonces = defDigestNonceCap
	}
	return &DigestClient{
		realm:     opts.Realm,
		algos:     algos,
		maxNonces: maxNonces,
		creds:     opts.Credentials,
		sess:      make(map[digestNonceSessKey]*digestNonceSess),
	}, nil
}

// digestAlgoPolicy resolves the configured allowlist: nil means
// SHA-256 only; an explicitly empty, duplicate, or unsupported list is a
// configuration error.
func digestAlgoPolicy(configured []string) ([]string, error) {
	if configured == nil {
		return []string{digestAlgoSHA256}, nil
	}
	if len(configured) == 0 {
		return nil, errors.ErrorWrap("empty digest algorithm list")
	}
	algos := make([]string, 0, len(configured))
	seen := make(map[string]struct{}, len(configured))
	for _, name := range configured {
		if name == "" {
			return nil, errors.ErrorWrap("empty digest algorithm name")
		}
		algo, err := normalizeDigestAlgo(name)
		if err != nil {
			return nil, errors.Wrap(err)
		}
		if _, dup := seen[algo]; dup {
			return nil, errors.ErrorfWrap("duplicate digest algorithm %q", name)
		}
		seen[algo] = struct{}{}
		algos = append(algos, algo)
	}
	return algos, nil
}

// selectDigestChal picks the strongest compatible challenge for the
// configured realm: SHA-256 over MD5 independently of allowlist or wire
// order, wire order only breaking same-algorithm ties. Challenges for other
// realms, malformed challenges, unsupported/disabled algorithms, and qop
// lists without auth are skipped.
func selectDigestChal(
	chals []*header.DigestChallenge,
	realm string,
	algos []string,
) *header.DigestChallenge {
	allowed := make(map[string]struct{}, len(algos))
	for _, name := range algos {
		allowed[name] = struct{}{}
	}

	var selected *header.DigestChallenge
	var strength int
	for _, chal := range chals {
		if chal == nil || !chal.IsValid() || chal.Realm != realm {
			continue
		}
		algo, err := normalizeDigestAlgo(chal.Algorithm)
		if err != nil {
			continue
		}
		if _, ok := allowed[algo]; !ok {
			continue
		}
		if len(chal.QOP) > 0 && !slices.Contains(chal.QOP, digestQOPAuth) {
			continue
		}
		if s := digestAlgoStrength(algo); selected == nil || s > strength {
			selected, strength = chal, s
		}
	}
	return selected
}

func digestAlgoStrength(algo string) int {
	if algo == digestAlgoSHA256 {
		return 2
	}
	return 1
}

// digestNonceSessKey scopes a credential session to one authentication
// leg and server nonce within the client's fixed realm.
type digestNonceSessKey struct {
	proxy bool
	nonce string
}

type digestNonceSess struct {
	cnonce string
	count  atomic.Uint32
	elem   *list.Element
}

// nextNonceCount returns the session cnonce and the next nonce count for
// the leg/nonce pair, creating and bounding sessions under LRU policy.
// Counts start at 1, increment atomically, and never wrap: exhaustion is
// an ordinary error. Reserved counts may leave gaps after aborted
// exchanges and are never rolled back.
func (c *DigestClient) nextNonceCount(proxy bool, nonce string) (string, uint32, error) {
	key := digestNonceSessKey{proxy: proxy, nonce: nonce}

	c.mu.Lock()
	if c.sess == nil {
		c.sess = make(map[digestNonceSessKey]*digestNonceSess)
	}
	sess, ok := c.sess[key]
	if ok {
		c.lru.MoveToFront(sess.elem)
	} else {
		maxNonces := c.maxNonces
		if maxNonces <= 0 {
			maxNonces = defDigestNonceCap
		}
		for len(c.sess) >= maxNonces {
			oldest := c.lru.Back()
			if oldKey, ok := oldest.Value.(digestNonceSessKey); ok {
				delete(c.sess, oldKey)
			}
			c.lru.Remove(oldest)
		}
		cnonce, err := newDigestCNonce()
		if err != nil {
			c.mu.Unlock()
			return "", 0, errors.Wrap(err)
		}
		sess = &digestNonceSess{cnonce: cnonce}
		sess.elem = c.lru.PushFront(key)
		c.sess[key] = sess
	}
	c.mu.Unlock()

	for {
		cur := sess.count.Load()
		if cur == math.MaxUint32 {
			return "", 0, errors.ErrorWrap("nonce count exhausted")
		}
		if sess.count.CompareAndSwap(cur, cur+1) {
			return sess.cnonce, cur + 1, nil
		}
	}
}

// AuthenticateRequest answers the Digest challenges of a 401/407 response
// by applying credentials to the caller-owned request. Both challenge
// families are processed independently; providers are consulted with
// detached request/challenge clones, and the request is mutated only after
// every selected leg is prepared — any provider error, invalid result, or
// cancellation leaves it unchanged. ACK and CANCEL return
// sip.ErrRequestMethodNotAllowed. It sends nothing and owns no retry
// policy.
func (c *DigestClient) AuthenticateRequest(ctx context.Context, req *sip.RequestEnvelope, res *sip.ResponseEnvelope) error {
	if c == nil {
		return errors.ErrorWrap("nil digest client")
	}
	reqMsg, resMsg, err := clientMsgs(ctx, req, res)
	if err != nil {
		return errors.Wrap(err)
	}
	if c.creds == nil {
		return errors.ErrorWrap("missing credentials provider")
	}
	uriText, err := renderURI(reqMsg.URI)
	if err != nil {
		return errors.Wrap(err)
	}

	var prepared []preparedLeg
	for _, proxy := range []bool{false, true} {
		selected := selectDigestChal(digestChals(resMsg.Headers, proxy), c.realm, c.algos)
		if selected == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return errors.Wrap(err)
		}
		credentials, err := c.prepareLeg(ctx, reqMsg, uriText, selected, proxy)
		if err != nil {
			return errors.Wrap(err)
		}
		prepared = append(prepared, preparedLeg{proxy: proxy, creds: credentials})
	}
	if len(prepared) == 0 {
		return errors.Wrap(sip.ErrUnsupportedChallenge)
	}
	if err := ctx.Err(); err != nil {
		return errors.Wrap(err)
	}

	return withReqCommit(ctx, req, func(msg *sip.Request) {
		for _, p := range prepared {
			applyDigestCreds(&msg.Headers, p.proxy, c.realm, p.creds)
		}
	})
}

type preparedLeg struct {
	proxy bool
	creds *header.DigestCredentials
}

// prepareLeg collects credentials from the provider for one challenge leg and
// renders the response proof. The provider gets detached clones; the selected
// challenge and request snapshot stay immutable for calculation and commit.
func (c *DigestClient) prepareLeg(
	ctx context.Context, reqMsg *sip.Request, uriText string,
	selected *header.DigestChallenge, proxy bool,
) (*header.DigestCredentials, error) {
	provReq, ok := reqMsg.Clone().(*sip.Request)
	if !ok {
		return nil, errors.ErrorWrap("unexpected request snapshot type")
	}
	provChal, ok := selected.Clone().(*header.DigestChallenge)
	if !ok {
		return nil, errors.ErrorWrap("unexpected challenge snapshot type")
	}
	usr, pwd, err := c.creds(ctx, provReq, provChal, proxy)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Wrap(err)
	}
	if usr == "" {
		return nil, errors.ErrorWrap("empty credentials username")
	}

	cnonce, count, err := c.nextNonceCount(proxy, selected.Nonce)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	ha1, err := DigestHA1(usr, selected.Realm, pwd, selected.Algorithm)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	resp, err := digestRes(
		ha1, string(reqMsg.Method), uriText, selected.Nonce, cnonce, count, selected.Algorithm,
	)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	algo, _ := normalizeDigestAlgo(selected.Algorithm)
	return &header.DigestCredentials{
		Username:   usr,
		Realm:      selected.Realm,
		Nonce:      selected.Nonce,
		URI:        reqMsg.URI.Clone(),
		Response:   resp,
		Algorithm:  algo,
		CNonce:     cnonce,
		Opaque:     selected.Opaque,
		QOP:        digestQOPAuth,
		NonceCount: uint(count),
	}, nil
}

func newDigestCNonce() (string, error) {
	buf := make([]byte, digestCNonceBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", errors.Wrap(err)
	}
	return hex.EncodeToString(buf), nil
}
