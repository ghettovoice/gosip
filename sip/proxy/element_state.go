package proxy

import (
	"sync"

	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

// elemState is the per-element processing namespace of a [Proxy]: the
// index of owned requests by their upstream server transaction key, the
// pending rejection-send records with their bounded eviction order, and the
// element-local stateless fallback key. One shared proxy attached to
// several elements keeps an independent request, rejection, and
// stateless-fallback namespace under each.
//
// Element identity is the *[sip.Element] pointer alone — never its
// InstanceID or advertised addresses. Per-scope synchronization keeps the
// same locking split the flat indexes had: index lookups and updates run
// under the scope's request guard, rejection records under its rejection
// guard, and no effect or callback runs under either.
type elemState struct {
	// reqsMu guards srvRqs, the index of requests this proxy owns under
	// the element, keyed by their upstream server transaction key.
	reqsMu sync.Mutex
	srvRqs map[sip.ServerTransactionKey]*Request

	// rejMu guards rejSends and rejOrd, the pending rejection-send records
	// and their bounded FIFO eviction order within this element's
	// namespace.
	rejMu    sync.Mutex
	rejSends map[sip.ServerTransactionKey]*rejectionSend
	rejOrd   []sip.ServerTransactionKey

	// slFallbackKey is the element-local random key reserved for the
	// stateless sends a stateful-only proxy must still perform without a
	// transaction under this element: the mandatory unmatched-CANCEL path
	// of RFC 3261 Section 16.10 and the end-to-end ACK. It is generated on
	// scope creation only when no [Options.StatelessKey] is configured and
	// never authorizes an explicitly stateless route.
	slFallbackKey []byte
}

// getElemState resolves the processing namespace of elm. Element
// identity is the pointer alone: a nil element returns nil, and a lookup
// with create=false for an element the proxy never processed under returns
// nil without allocating. A created scope lives until global shutdown, and
// a create that lost the lifecycle seal race returns nil instead of
// resurrecting a scope past the cleared namespace.
func (prx *Proxy) getElemState(elm *sip.Element, create bool) *elemState {
	if elm == nil {
		return nil
	}

	prx.reqsMu.Lock()
	defer prx.reqsMu.Unlock()
	if es, ok := prx.elms[elm]; ok {
		return es
	}
	if !create || prx.closed.Load() {
		return nil
	}

	es := &elemState{
		srvRqs: make(map[sip.ServerTransactionKey]*Request),
	}
	if len(prx.opts.StatelessKey) == 0 {
		es.slFallbackKey = []byte(util.RandString(32))
	}
	if prx.elms == nil {
		prx.elms = make(map[*sip.Element]*elemState)
	}
	prx.elms[elm] = es
	return es
}
