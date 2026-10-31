package dns

import (
	"cmp"
	"context"
	"math/rand/v2"
	"net"
	"reflect"
	"slices"
	"time"

	"github.com/jellydator/ttlcache/v3"

	"github.com/ghettovoice/gosip/internal/errors"
)

// Lookup is the interface implemented by DNS resolution backends.
// It is satisfied by [Resolver] and [CachedResolver] and is structurally
// assignable to the sip.DNSResolver interface, so a Lookup value can be
// injected anywhere SIP DNS resolution is expected.
type Lookup interface {
	// LookupIP looks up IP addresses for the given network and host.
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)
	// LookupSRV looks up SRV records for the given service, protocol, and host.
	LookupSRV(ctx context.Context, service, proto, host string) ([]*SRV, error)
	// LookupNAPTR looks up NAPTR records for the given host.
	LookupNAPTR(ctx context.Context, host string) ([]*NAPTR, error)
}

var (
	_ Lookup = (*Resolver)(nil)
	_ Lookup = (*CachedResolver)(nil)
)

const (
	errNilLookupBackend errors.Error = "nil lookup backend"
	errInvalidCacheTTL  errors.Error = "invalid cache TTL"
	errInvalidCacheCap  errors.Error = "invalid cache capacity"
)

// CacheOptions configures the result cache of a [CachedResolver].
type CacheOptions struct {
	// TTL is the fixed application-level lifetime of a cached result,
	// measured from the moment the backend returns it.
	// TTL == 0 disables caching entirely; TTL < 0 is invalid.
	TTL time.Duration
	// Capacity bounds the total number of cached lookups shared by the IP,
	// SRV, and NAPTR operations. It must be > 0 when TTL > 0 and is ignored
	// when TTL == 0. Least recently used entries are evicted first.
	Capacity uint64
}

// CachedResolver decorates a [Lookup] backend with an opt-in, bounded,
// fixed-TTL result cache shared across the IP, SRV, and NAPTR operations.
//
// CachedResolver is not DNS-TTL-aware: a cached result lives for the
// configured TTL regardless of the real DNS record TTL, and caching never
// changes the backend's own behavior. Only successful non-empty results are
// cached: errors, empty results, and canceled lookups are never stored, and
// calls with a pre-canceled context are rejected before any backend call.
//
// Use [NewCachedResolver] to create an instance; the zero value is not usable.
type CachedResolver struct {
	next  Lookup
	ttl   time.Duration
	cache *ttlcache.Cache[cacheKey, cacheValue] // nil when caching is disabled
}

// NewCachedResolver returns a [CachedResolver] that caches the results of next
// according to opts.
// next must be a non-nil [Lookup] backend: a nil interface or a typed-nil
// backend is an argument error. See [CacheOptions] for the validation rules.
func NewCachedResolver(next Lookup, opts CacheOptions) (*CachedResolver, error) {
	if isNilLookup(next) {
		return nil, errors.Wrap(errNilLookupBackend)
	}
	if opts.TTL < 0 {
		return nil, errors.PrefixWrap(errInvalidCacheTTL, "negative")
	}
	if opts.TTL == 0 {
		return &CachedResolver{next: next}, nil
	}
	if opts.Capacity == 0 {
		return nil, errors.PrefixWrap(errInvalidCacheCap, "zero with enabled TTL")
	}

	return &CachedResolver{
		next: next,
		ttl:  opts.TTL,
		cache: ttlcache.New(
			ttlcache.WithCapacity[cacheKey, cacheValue](opts.Capacity),
			ttlcache.WithDisableTouchOnHit[cacheKey, cacheValue](),
		),
	}, nil
}

// LookupIP looks up IP addresses for the given network and host.
// With caching enabled, a cached result is returned as long as it has not
// expired; cache reads never extend the entry lifetime.
func (r *CachedResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.Wrap(err)
	}
	if r.next == nil {
		return nil, errors.Wrap(errNilLookupBackend)
	}
	if r.cache == nil {
		return errors.Wrap2(r.next.LookupIP(ctx, network, host))
	}

	key := cacheKey{op: cacheOpIP, network: network, host: host}
	if item := r.cache.Get(key); item != nil {
		return cloneIPs(item.Value().ips), nil
	}

	ips, err := r.next.LookupIP(ctx, network, host)
	if err != nil {
		return ips, errors.Wrap(err)
	}

	if len(ips) > 0 && ctx.Err() == nil {
		r.cache.Set(key, cacheValue{ips: cloneIPs(ips)}, r.ttl)
	}
	return cloneIPs(ips), nil
}

// LookupSRV looks up SRV records for the given service, protocol, and host.
// With caching enabled, the unordered record set is cached and every returned
// result is a fresh copy ordered per RFC 2782: priorities ascending with
// weighted selection without replacement inside each priority.
func (r *CachedResolver) LookupSRV(ctx context.Context, service, proto, host string) ([]*SRV, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.Wrap(err)
	}
	if r.next == nil {
		return nil, errors.Wrap(errNilLookupBackend)
	}
	if r.cache == nil {
		return errors.Wrap2(r.next.LookupSRV(ctx, service, proto, host))
	}

	key := cacheKey{op: cacheOpSRV, service: service, proto: proto, host: host}
	if item := r.cache.Get(key); item != nil {
		return weightedSRVOrder(cloneSRVs(item.Value().srvs), rand.Int64N), nil
	}

	srvs, err := r.next.LookupSRV(ctx, service, proto, host)
	if err != nil {
		return srvs, errors.Wrap(err)
	}

	if len(srvs) > 0 && ctx.Err() == nil {
		r.cache.Set(key, cacheValue{srvs: cloneSRVs(srvs)}, r.ttl)
	}
	return weightedSRVOrder(cloneSRVs(srvs), rand.Int64N), nil
}

// LookupNAPTR looks up NAPTR records for the given host.
// With caching enabled, a cached result is returned in the order produced by
// the backend as long as it has not expired; cache reads never extend the
// entry lifetime.
func (r *CachedResolver) LookupNAPTR(ctx context.Context, host string) ([]*NAPTR, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.Wrap(err)
	}
	if r.next == nil {
		return nil, errors.Wrap(errNilLookupBackend)
	}
	if r.cache == nil {
		return errors.Wrap2(r.next.LookupNAPTR(ctx, host))
	}

	key := cacheKey{op: cacheOpNAPTR, host: host}
	if item := r.cache.Get(key); item != nil {
		return cloneNAPTRs(item.Value().naptrs), nil
	}

	naptrs, err := r.next.LookupNAPTR(ctx, host)
	if err != nil {
		return naptrs, errors.Wrap(err)
	}

	if len(naptrs) > 0 && ctx.Err() == nil {
		r.cache.Set(key, cacheValue{naptrs: cloneNAPTRs(naptrs)}, r.ttl)
	}
	return cloneNAPTRs(naptrs), nil
}

// isNilLookup reports whether l is nil, including a non-nil interface that
// holds a typed-nil backend.
func isNilLookup(l Lookup) bool {
	if l == nil {
		return true
	}
	v := reflect.ValueOf(l)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

// cacheOp identifies the lookup operation a cacheKey belongs to.
type cacheOp uint8

const (
	cacheOpIP cacheOp = iota
	cacheOpSRV
	cacheOpNAPTR
)

// cacheKey identifies a single lookup by its operation and raw arguments.
// Arguments are stored verbatim: equivalent spellings (e.g. a host with and
// without a trailing dot) are intentionally distinct cache entries so the
// backend's own query semantics are preserved.
type cacheKey struct {
	op      cacheOp
	network string
	service string
	proto   string
	host    string
}

// cacheValue stores one lookup result. Only the field matching cacheKey.op
// is populated.
type cacheValue struct {
	ips    []net.IP
	srvs   []*SRV
	naptrs []*NAPTR
}

func cloneIPs(ips []net.IP) []net.IP {
	if ips == nil {
		return nil
	}
	out := make([]net.IP, len(ips))
	for i, ip := range ips {
		out[i] = slices.Clone(ip)
	}
	return out
}

func cloneSRVs(recs []*SRV) []*SRV {
	if recs == nil {
		return nil
	}
	out := make([]*SRV, len(recs))
	for i, rec := range recs {
		if rec == nil {
			continue
		}
		cp := *rec
		out[i] = &cp
	}
	return out
}

func cloneNAPTRs(recs []*NAPTR) []*NAPTR {
	if recs == nil {
		return nil
	}
	out := make([]*NAPTR, len(recs))
	for i, rec := range recs {
		if rec == nil {
			continue
		}
		cp := *rec
		out[i] = &cp
	}
	return out
}

// weightedSRVOrder returns a fresh slice with the records ordered per
// RFC 2782: priorities ascending, and within each priority a weighted
// selection without replacement driven by randInt, which must return a
// uniform draw from [0, n). The input slice is not modified. Nil records
// cannot be dereferenced and are kept at the end of the result.
func weightedSRVOrder(recs []*SRV, randInt func(int64) int64) []*SRV {
	if recs == nil {
		return nil
	}

	work := make([]*SRV, 0, len(recs))
	nils := 0
	for _, rec := range recs {
		if rec == nil {
			nils++
			continue
		}
		work = append(work, rec)
	}

	slices.SortStableFunc(work, func(a, b *SRV) int {
		return cmp.Compare(a.Priority, b.Priority)
	})

	out := make([]*SRV, 0, len(recs))
	for i := 0; i < len(work); {
		j := i + 1
		for j < len(work) && work[j].Priority == work[i].Priority {
			j++
		}
		out = append(out, selectSRVGroup(work[i:j], randInt)...)
		i = j
	}

	return append(out, make([]*SRV, nils)...)
}

// selectSRVGroup performs the RFC 2782 weighted selection without
// replacement inside a single priority group.
func selectSRVGroup(group []*SRV, randInt func(int64) int64) []*SRV {
	cands := slices.Clone(group)

	// Randomize the initial order so that all-zero-weight groups and equal
	// weights do not freeze to the backend's ordering, then give zero-weight
	// records the RFC's small selection chance by placing them before records
	// with non-zero weight.
	for i := len(cands) - 1; i > 0; i-- {
		j := randInt(int64(i + 1))
		cands[i], cands[int(j)] = cands[int(j)], cands[i]
	}
	slices.SortStableFunc(cands, func(a, b *SRV) int {
		switch {
		case (a.Weight == 0) == (b.Weight == 0):
			return 0
		case a.Weight == 0:
			return -1
		default:
			return 1
		}
	})

	out := make([]*SRV, 0, len(cands))
	for len(cands) > 0 {
		var sum int64
		for _, cand := range cands {
			sum += int64(cand.Weight)
		}

		// Uniform draw from [0, sum] inclusive: a draw of 0 selects the first
		// zero-weight candidate, which is the selection chance RFC 2782
		// reserves for weight 0 records.
		draw := randInt(sum + 1)

		var cum int64
		pick := len(cands) - 1
		for i, cand := range cands {
			cum += int64(cand.Weight)
			if cum >= draw {
				pick = i
				break
			}
		}

		out = append(out, cands[pick])
		cands = slices.Delete(cands, pick, pick+1)
	}

	return out
}
