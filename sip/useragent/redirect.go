package useragent

import (
	"cmp"
	"context"
	"slices"
	"strconv"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
)

// redirectCandidate is one followable contact extracted from a redirect
// response. The depth is fixed at extraction: a pending sibling keeps the
// depth it would have had as the redirect's first choice, so a serial
// fallback never turns into a deeper chain.
type redirectCandidate struct {
	target *sip.URI
	depth  int
}

// redirectState is the per-operation redirect tracker, built at runOp
// entry when a policy is configured. visited seeds the initial target at
// depth 0 and loop detection compares request URIs, never rendered strings.
// pending holds approved-for-extraction contacts in pop order: a new
// redirect's contacts are prepended, so a chain is depth-first while a
// sibling contact stays available for a serial fallback after an ordinary
// final failure. sends counts every logical send — the initial request,
// each authentication retry, and each redirect hop — against the policy's
// shared MaxAttempts budget; element DNS failover inside one send never
// counts. lastRedirect is a detached clone of the most recent redirect
// response: the response an exhausted or denied chain resolves with.
// budgetHit records that the last nextTarget call dropped a candidate
// because the shared send budget ran out — the stopped hop resolves the
// most recent redirect rather than the current final response.
type redirectState struct {
	policy       RedirectPolicy
	visited      []sip.AnyURI
	pending      []redirectCandidate
	currentDepth int
	sends        int
	lastRedirect *sip.ResponseEnvelope
	budgetHit    bool
}

// newRedirectState creates the operation's redirect tracker and derives the
// operation context bounded by the policy timeout. The returned cancel must
// be deferred by the caller: the single deadline covers the initial send,
// every authentication retry, and every redirect hop — it is created once
// and never refreshed between hops.
func newRedirectState(
	ctx context.Context,
	policy *RedirectPolicy,
) (*redirectState, context.Context, context.CancelFunc) {
	opCtx, cancel := context.WithTimeout(ctx, policy.Timeout)
	return &redirectState{policy: *policy}, opCtx, cancel
}

// outcome resolves the response an exhausted or denied chain ends with: the
// most recent redirect when one was recorded, otherwise the current final
// response. It never produces a synthetic error.
func (rs *redirectState) outcome(res *sip.ResponseEnvelope) *sip.ResponseEnvelope {
	if rs.lastRedirect != nil {
		return rs.lastRedirect
	}
	return res
}

// nextTarget returns the next approved redirect target after res, or nil
// when the chain ends. A 300/301/302 response records itself as the last
// redirect and enqueues its surviving contacts; any other 3xx resolves the
// response directly; a non-3xx response only pops pending siblings — it
// never extracts contacts, advances their saved depths, or overwrites the
// last redirect.
func (rs *redirectState) nextTarget(
	ctx context.Context,
	req *sip.RequestEnvelope,
	res *sip.ResponseEnvelope,
) (*sip.URI, error) {
	sts := res.Status()
	rs.budgetHit = false
	switch {
	case isSupportedRedirect(sts):
		rs.lastRedirect = res.Clone().(*sip.ResponseEnvelope) //nolint:forcetypeassert // Clone preserves the concrete envelope type
		rs.collect(res)
	case sts.IsRedirection():
		return nil, nil //nolint:nilnil // a nil target is the "no candidate, resolve the response" signal
	}

	for len(rs.pending) > 0 {
		cand := rs.pending[0]
		rs.pending = rs.pending[1:]

		if slices.ContainsFunc(rs.visited, func(u sip.AnyURI) bool {
			return cand.target.Equal(u)
		}) {
			return nil, errors.Wrap(NewRedirectLoopError("target %q already visited", cand.target))
		}
		if cand.depth > rs.policy.MaxDepth {
			continue
		}
		if rs.sends >= rs.policy.MaxAttempts {
			// The shared send budget stopped this hop.
			rs.budgetHit = true
			continue
		}

		// The trust gate runs before any send or credential computation
		// for the target and receives detached clones: mutating them
		// inside the callback cannot alter the candidate or the sent
		// request.
		dreq := req.Clone().(*sip.RequestEnvelope) //nolint:forcetypeassert // Clone preserves the concrete envelope type
		dtrg := cand.target.Clone().(*sip.URI)     //nolint:forcetypeassert // Clone preserves the concrete type
		ok, err := rs.policy.AllowTarget(ctx, dreq, dtrg)
		if err != nil {
			return nil, errors.Wrap(err)
		}
		if !ok {
			continue
		}

		rs.currentDepth = cand.depth
		rs.visited = append(rs.visited, cand.target.Clone())
		return cand.target, nil
	}
	return nil, nil //nolint:nilnil // a nil target is the "no candidate, resolve the response" signal
}

// redirectRetry produces the retargeted retry for the next approved
// candidate after res, or resolves the chain. A chain-ending response
// resolves itself — including an unsupported 3xx, whose own status
// carries the terminal meaning a manual follow-up needs — unless the
// shared send budget stopped a pending sibling hop, which resolves the
// most recent recorded redirect. A retargeted retry is a fresh send:
// the request URI becomes the approved contact and the credential
// header families never cross targets.
func (rs *redirectState) redirectRetry(
	ctx context.Context,
	sent *sip.RequestEnvelope,
	res *sip.ResponseEnvelope,
) (*sip.RequestEnvelope, *sip.ResponseEnvelope, error) {
	target, err := rs.nextTarget(ctx, sent, res)
	if err != nil {
		return nil, nil, errors.Wrap(err)
	}
	if target == nil {
		// Only a sibling hop stopped by the shared send budget resolves
		// the most recent redirect; every other chain-ending response —
		// a supported redirect without approved candidates, an
		// unsupported 3xx, or an ordinary final failure — resolves
		// itself.
		if rs.budgetHit {
			return nil, rs.outcome(res), nil
		}
		return nil, res, nil
	}
	// A gate that approves after the operation was canceled must not
	// produce a packet: the context is re-checked before the retargeted
	// retry is built and queued.
	if err := ctx.Err(); err != nil {
		return nil, nil, errors.Wrap(err)
	}
	retry, err := makeRetryReq(sent, target)
	if err != nil {
		return nil, nil, errors.Wrap(err)
	}
	return retry, nil, nil
}

// collect extracts the followable contacts of a redirect response and
// prepends them to the pending queue as candidates for the next depth.
// Wildcard, non-SIP/SIPS, invalid, and expired contacts are dropped, and a
// secured operation never follows a downgrade to plain SIP. Survivors keep
// header order inside equal q values; malformed or out-of-range q discards
// the contact.
func (rs *redirectState) collect(res *sip.ResponseEnvelope) {
	var secured bool
	if u, ok := rs.visited[0].(*sip.URI); ok {
		secured = u.Secured
	}

	type scoredTarget struct {
		target *sip.URI
		q      float64
	}
	var surv []scoredTarget
	for addr := range res.Headers().Contacts() {
		if addr == nil {
			continue
		}
		u, ok := addr.URI.(*sip.URI)
		if !ok || u == nil || !u.IsValid() {
			continue
		}
		if exp, ok := addr.Expires(); ok && exp <= 0 {
			continue
		}
		if secured && !u.Secured {
			continue
		}
		q := 1.0
		if raw, ok := addr.Params.Last("q"); ok {
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil || v < 0 || v > 1 {
				continue
			}
			q = v
		}
		surv = append(surv, scoredTarget{target: sip.CleanRequestURI(u), q: q})
	}
	slices.SortStableFunc(surv, func(a, b scoredTarget) int {
		return cmp.Compare(b.q, a.q)
	})

	depth := rs.currentDepth + 1
	cands := make([]redirectCandidate, 0, len(surv)+len(rs.pending))
	for _, s := range surv {
		cands = append(cands, redirectCandidate{target: s.target, depth: depth})
	}
	rs.pending = append(cands, rs.pending...)
}

// isSupportedRedirect reports whether sts is a redirect status whose
// contacts the policy follows: 300, 301, and 302.
func isSupportedRedirect(sts sip.ResponseStatus) bool {
	return sts.Equal(sip.ResponseStatusMultipleChoices) ||
		sts.Equal(sip.ResponseStatusMovedPermanently) ||
		sts.Equal(sip.ResponseStatusMovedTemporarily)
}
