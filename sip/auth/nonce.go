package auth

import (
	"container/list"
	"context"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
)

// nonceOutcome classifies a replay commitment.
type nonceOutcome int

const (
	nonceAccepted nonceOutcome = iota
	nonceExpired
	nonceInvalid
)

// nonceRecord is an immutable snapshot of one issued nonce's state.
type nonceRecord struct {
	realm   string
	algo    string
	user    string
	expires time.Time
	count   uint32
}

// nonceStore is the server's bounded in-memory nonce state. Records are
// created only by issue — a client-supplied nonce never installs one.
// Expired records stay distinguishable until capacity eviction removes
// them; the oldest issued record is evicted first. There is no cleanup
// worker, lifecycle, or exported surface.
type nonceStore struct {
	mu   sync.Mutex
	cap  int
	recs map[string]*nonceStoreRecord
	ord  list.List
}

type nonceStoreRecord struct {
	nonceRecord
	elem *list.Element
}

func newNonceStore(capacity int) *nonceStore {
	if capacity <= 0 {
		capacity = 1
	}
	return &nonceStore{cap: capacity, recs: make(map[string]*nonceStoreRecord)}
}

// issue returns one fresh nonce per algorithm, in algorithm order, bound
// to realm and expiring after ttl. The complete set is installed under a
// single lock hold so issuing it cannot evict members of the same set.
// Each nonce carries at least 128 bits of crypto/rand entropy.
func (s *nonceStore) issue(ctx context.Context, realm string, algos []string, ttl time.Duration) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.Wrap(err)
	}
	nonces := make([]string, len(algos))
	for i := range nonces {
		nonce, err := newDigestCNonce()
		if err != nil {
			return nil, errors.Wrap(err)
		}
		nonces[i] = nonce
	}
	expires := time.Now().Add(ttl)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, errors.Wrap(err)
	}
	for i, nonce := range nonces {
		for len(s.recs) >= s.cap {
			oldest := s.ord.Back()
			if key, ok := oldest.Value.(string); ok {
				delete(s.recs, key)
			}
			s.ord.Remove(oldest)
		}
		rec := &nonceStoreRecord{}
		rec.realm = realm
		rec.algo = algos[i] //nolint:gosec // nonces is allocated with len(algorithms) above
		rec.expires = expires
		rec.elem = s.ord.PushFront(nonce)
		s.recs[nonce] = rec
	}
	return nonces, nil
}

// snapshot returns an immutable copy of an issued record. Reading a nonce
// never binds a username, advances a count, or installs a record.
func (s *nonceStore) snapshot(nonce string) (nonceRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.recs[nonce]
	if !ok {
		return nonceRecord{}, false
	}
	return rec.nonceRecord, true
}

// commit atomically rechecks a verified proof's nonce state and binds the
// outcome. Membership, realm/algorithm binding, username binding, expiry,
// and strictly increasing count are all revalidated under the lock; only
// an accepted outcome mutates the record. count must be greater than every
// previously accepted count for this nonce — reuse and regression are
// invalid regardless of proof validity.
func (s *nonceStore) commit(ctx context.Context, nonce, realm, algo, user string, count uint32) (nonceOutcome, error) {
	if err := ctx.Err(); err != nil {
		return nonceInvalid, errors.Wrap(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nonceInvalid, errors.Wrap(err)
	}
	rec, ok := s.recs[nonce]
	if !ok {
		return nonceInvalid, nil
	}
	if rec.realm != realm || rec.algo != algo {
		return nonceInvalid, nil
	}
	if rec.user != "" && rec.user != user {
		return nonceInvalid, nil
	}
	if !time.Now().Before(rec.expires) {
		return nonceExpired, nil
	}
	if count == 0 || count <= rec.count {
		return nonceInvalid, nil
	}
	rec.user = user
	rec.count = count
	return nonceAccepted, nil
}
