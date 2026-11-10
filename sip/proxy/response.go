package proxy

import (
	"context"
	"log/slog"
	"sync"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/sip"
)

// sendCtxKey is the private context key carrying the one-shot upstream
// response-send correlation token.
type sendCtxKey struct{}

// send is transient send bookkeeping for one proxy-initiated upstream
// response send. It correlates the send submitted through
// [sip.ServerTransaction.SendResponse] with the actual wire result observed by
// [Proxy.InterceptOutboundResponse]: the first intercepted send under the
// token records the real result once and enqueues it for the owning request
// coordinator. Automatic transaction retransmissions inheriting the token
// pass through without settling it again, and unrelated traffic never
// carries a token. A token is never a serialized callback and never a
// stateless response context.
type send struct {
	rq       *Request
	res      *sip.ResponseEnvelope
	targetID string
	attID    string
	once     sync.Once
}

// ctxWithSend returns a context carrying the send token.
func ctxWithSend(ctx context.Context, sendToken *send) context.Context {
	return context.WithValue(ctx, sendCtxKey{}, sendToken)
}

// sendFromCtx returns the send token carried by ctx.
func sendFromCtx(ctx context.Context) (*send, bool) {
	sendToken, ok := ctx.Value(sendCtxKey{}).(*send)
	return sendToken, ok && sendToken != nil
}

// settle records the first actual wire result and enqueues it for the owning
// request coordinator. The enqueue is the only coordinator touch: it never
// blocks, so no observer work is dispatched inside a transaction action.
// Later sends under the same token are ignored for bookkeeping.
func (s *send) settle(ctx context.Context, err error) {
	s.once.Do(func() {
		s.rq.enqueue(ctx, workEvent{
			kind:     evtSrvSend,
			res:      s.res,
			err:      err,
			targetID: s.targetID,
			attID:    s.attID,
		})
	})
}

// processRes applies RFC 3261 Section 16.7 response processing, as
// amended by RFC 6026, to one detached downstream response owned by the
// request coordinator.
//
// Cheap suppressions run before any copy work: a downstream 100 Trying is
// never forwarded, a non-INVITE provisional never crosses upstream in either
// server transaction behavior mode, and a non-INVITE 408 is suppressed even
// when committed as the request's final response.
//
// For every surviving response an owned copy is cloned, exactly one top Via
// is removed, and the copy is marked for To-tag preservation so neither the
// element nor the transport manufactures a dialog tag for it; the
// coordinator's cached response is never mutated. A response with no
// remaining Via after the strip belongs to a locally generated operation
// (for example a CANCEL) and is consumed instead of forwarded.
//
// Eligible responses — every INVITE 101-199 provisional, every INVITE
// success, and the request's single committed non-success final — are sent
// through the original upstream server transaction. Coordinator state is
// retained after the first success: later provisionals and additional 2xx
// responses from still-live sibling branches keep forwarding until the
// upstream transaction is gone.
func (rq *Request) processRes(
	ctx context.Context,
	key sip.ClientTransactionKey,
	res *sip.ResponseEnvelope,
) error {
	if res == nil {
		return nil
	}

	sts := res.Status()
	invite := rq.req.Method().Equal(sip.RequestMethodInvite)
	switch {
	case sts.IsProvisional() && (sts.Equal(sip.ResponseStatusTrying) || !invite):
		// A 100 Trying is never forwarded, and a non-INVITE provisional is
		// suppressed in either server transaction behavior mode. The
		// remaining INVITE 101-199 responses stay forwardable.
		return nil
	case !invite && sts.Equal(sip.ResponseStatusRequestTimeout):
		// A downstream non-INVITE 408 never crosses upstream, independently
		// of the server transaction behavior option.
		return nil
	}

	out := cloneResEnvelope(res)
	out.MarkForwarded()
	out.SetPreserveToTag(true)
	var hasVia bool
	out.WithMessage(func(r *sip.Response) {
		r.Headers.PopFirstVia()
		_, hasVia = r.Headers.FirstVia()
	})
	if !hasVia {
		return nil
	}

	if !rq.markResForwarding(res) {
		return nil
	}

	targetID, attID := rq.attemptCorrelation(key)
	return errors.Wrap(rq.sendUpstream(ctx, out, targetID, attID))
}

// markResForwarding commits the forwarding decision for one downstream
// response under the coordinator guard and records the send attempt so a
// response is never forwarded twice through the same pointer identity.
// Provisional reachability is already narrowed by the caller: only INVITE
// 101-199 responses arrive here unfiltered. Retransmitted successes of an
// already settled attempt do reach this point — the event attribution path
// lets an eligible INVITE 2xx through even after logical settlement — so
// this guard still decides forwarding on every arrival.
//
// A response still racing an internal abandonment is a stale discard: the
// request already committed its terminal decision, so nothing may cross
// upstream anymore.
func (rq *Request) markResForwarding(res *sip.ResponseEnvelope) bool {
	sts := res.Status()

	rq.mu.Lock()
	defer rq.mu.Unlock()

	if rq.outcome == OutcomeAbandoned {
		return false
	}

	invite := rq.req.Method().Equal(sip.RequestMethodInvite)
	eligible := invite && (sts.IsProvisional() || sts.IsSuccessful())
	if !eligible && rq.outcome == OutcomeResponse && rq.final == res {
		eligible = true
	}
	if !eligible {
		return false
	}

	if rq.sentRes == nil {
		rq.sentRes = make(map[*sip.ResponseEnvelope]struct{})
	}
	if _, sent := rq.sentRes[res]; sent {
		return false
	}
	rq.sentRes[res] = struct{}{}
	return true
}

// errNoFinalResCandidate reports a response context that carries no
// final response the selection may commit: every candidate is missing or
// still provisional.
const errNoFinalResCandidate errors.Error = "no final response candidate"

// selectFinalRes applies the RFC 3261 Section 16.7 response-context
// selection to the settled candidate responses in arrival order.
//
// The caller runs it only after every eligible attempt settled: a settled
// attempt contributes the final responses it received, while declined
// branches and attempts that ended without a response contribute nothing —
// a logical target's final result is already known long before its
// transaction cleanup timers D/K run down, so the decision never waits on
// them.
//
// The precedence is: a settled success (2xx) outranks every other class —
// it may already have been forwarded while its branch was live, so
// committing anything else would contradict the wire; otherwise any 6xx
// wins over the remaining classes, the lowest status class wins, inside
// 4xx the informative statuses (401, 407, 415, 420, 484) are preferred,
// and equal ranks keep the earliest arrival.
// A selected 401/407 collects every WWW-Authenticate and Proxy-Authenticate
// header of the other candidates onto an owned clone, verbatim — stored
// candidates are never rewritten. A 3xx selection keeps its Contacts and
// the proxy never recurses on them. When the only received response is a
// 503 the proxy cannot claim general unavailability on behalf of the
// upstream client, so the selection maps it to a generated 500; no explicit
// policy proving general unavailability exists yet, so the mapping is
// unconditional.
func selectFinalRes(candidates []*sip.ResponseEnvelope) (*sip.ResponseEnvelope, error) {
	finals := make([]*sip.ResponseEnvelope, 0, len(candidates))
	for _, res := range candidates {
		if res != nil && res.Status().IsFinal() {
			finals = append(finals, res)
		}
	}
	if len(finals) == 0 {
		return nil, errors.Wrap(errNoFinalResCandidate)
	}
	if len(finals) == 1 && finals[0].Status().Equal(sip.ResponseStatusServiceUnavailable) {
		return cloneResEnvelope(finals[0]).WithMessage(func(msg *sip.Response) {
			msg.Status = sip.ResponseStatusServerInternalError
			msg.Reason = sip.ResponseStatusServerInternalError.Reason()
		}), nil
	}

	best := finals[0]
	for _, cand := range finals[1:] {
		if resOutranks(cand.Status(), best.Status()) {
			best = cand
		}
	}

	// The winner is returned by pointer identity whenever the selection
	// needs no rewrite, so the send dedup still recognizes a response that
	// was already forwarded while its branch was live.
	if sts := best.Status(); sts.Equal(sip.ResponseStatusUnauthorized) ||
		sts.Equal(sip.ResponseStatusProxyAuthenticationRequired) {
		return mergeAuthChallenges(best, finals), nil
	}
	return best, nil
}

// resOutranks reports whether a candidate status outranks the
// current best under the Section 16.7 precedence: a success outranks even a
// global failure, because a settled 2xx may already have crossed upstream
// while its branch was live. The check is strict: equal ranks lose, which
// keeps the earliest arrival on ties.
func resOutranks(cand, best sip.ResponseStatus) bool {
	cc, bc := cand.Class(), best.Class()
	switch {
	case bc == shared.ResponseClassSuccessful && cc != shared.ResponseClassSuccessful:
		return false
	case cc == shared.ResponseClassSuccessful && bc != shared.ResponseClassSuccessful:
		return true
	case bc == shared.ResponseClassGlobalFailure && cc != shared.ResponseClassGlobalFailure:
		return false
	case cc == shared.ResponseClassGlobalFailure && bc != shared.ResponseClassGlobalFailure:
		return true
	case cc != bc:
		return cc < bc
	case cc == shared.ResponseClassRequestFailure:
		return preferredFailure(cand) && !preferredFailure(best)
	default:
		return false
	}
}

// preferredFailure reports whether the status is one of the 4xx codes a
// proxy prefers, per RFC 3261 Section 16.7.5: each carries information that
// lets the user agent client retry the request with different content.
func preferredFailure(sts sip.ResponseStatus) bool {
	switch sts {
	case sip.ResponseStatusUnauthorized,
		sip.ResponseStatusProxyAuthenticationRequired,
		sip.ResponseStatusUnsupportedMediaType,
		sip.ResponseStatusBadExtension,
		sip.ResponseStatusAddressIncomplete:
		return true
	default:
		return false
	}
}

// mergeAuthChallenges returns an owned clone of the selected
// authentication response with every WWW-Authenticate and Proxy-Authenticate
// header collected across the other candidates appended verbatim, per
// Section 16.7.5. Candidate order and per-response order are preserved; the
// stored candidates are never mutated.
func mergeAuthChallenges(
	sel *sip.ResponseEnvelope,
	candidates []*sip.ResponseEnvelope,
) *sip.ResponseEnvelope {
	var extra []sip.Header
	for _, res := range candidates {
		if res == nil || res == sel {
			continue
		}
		res.WithMessage(func(msg *sip.Response) {
			extra = append(extra, msg.Headers.Get("WWW-Authenticate")...)
			extra = append(extra, msg.Headers.Get("Proxy-Authenticate")...)
		})
	}

	out := cloneResEnvelope(sel)
	out.WithMessage(func(msg *sip.Response) {
		for _, hdr := range extra {
			if hdr != nil {
				msg.Headers.Append(hdr.Clone())
			}
		}
	})
	return out
}

// sendUpstream submits the owned response copy to the original upstream
// server transaction under a one-shot send token.
//
// The final-send guards run before submission and hold independently of the
// server transaction behavior option: a non-INVITE 408 never crosses
// upstream, and a late or stray response is discarded when the held
// transaction is absent or unusable. The send may only go through the held
// original upstream transaction — never through a fabricated replacement
// transaction and never through a stateless fallback.
//
// The transaction-level return only proves submission: the actual wire
// result arrives through the token's evtSrvSend event, so a nil return
// alone never commits a forwarded final. When the transport send never ran,
// the transaction-level rejection settles the token directly so the failure
// is reported through the same single path as a wire error. A missing or
// unusable upstream transaction discards the response — no replacement
// transaction is ever fabricated.
func (rq *Request) sendUpstream(
	ctx context.Context,
	res *sip.ResponseEnvelope,
	targetID, attID string,
) error {
	if res == nil {
		return nil
	}
	if !rq.req.Method().Equal(sip.RequestMethodInvite) &&
		res.Status().Equal(sip.ResponseStatusRequestTimeout) {
		// A non-INVITE 408 never crosses upstream, independently of the
		// server transaction behavior option.
		return nil
	}

	rq.mu.Lock()
	tx := rq.srvTx
	gone := rq.srvGone
	md := rq.stageMetaLocked(StageUpstream, targetID, attID)
	rq.mu.Unlock()

	if tx == nil || gone || tx.State() == sip.TransactionStateTerminated {
		return nil
	}

	// The send boundary and the token-settled wire result it later enqueues
	// run under the request's own element, never an element a transient
	// effect context happened to carry.
	ctx = sip.ContextWithElement(ctx, rq.elm)

	// The forwarded copy is answered through the original upstream
	// transaction, so it must carry the upstream addressing of the held
	// request — never the downstream source the inbound response envelope
	// brought along. A locally generated response already carries the same
	// addressing through [sip.RequestEnvelope.NewResponse], so re-applying it
	// stays idempotent.
	res.SetLocalAddr(rq.req.LocalAddr()).
		SetRemoteAddr(rq.req.RemoteAddr())

	// The owned upstream copy carries its stage record before the send:
	// the request identity plus the actual known source. A local or
	// aggregated response keeps the attempt fields empty — the whole-value
	// stamp also clears the response record the forwarded clone inherited
	// rather than letting it read as a single-attempt source.
	stampRes(res, md)

	sendToken := &send{rq: rq, res: res, targetID: targetID, attID: attID}
	if err := tx.SendResponse(ctxWithSend(ctx, sendToken), res); err != nil {
		sendToken.settle(ctx, err)
		return errors.Wrap(err)
	}
	return nil
}

// rejectUpstream answers an abandoned request through the original upstream
// server transaction and then retires the transaction. The abandonment maps
// through the same rules a pipeline exit applies: a carried
// [sip.RequestRejectedError] keeps its suggested response, any other failure is
// answered with a generated 500, and a method that may never be answered
// consumes the rejection silently. A transaction already gone by the time
// the effect runs discards the response — no replacement transaction is
// ever fabricated — while terminating the transaction keeps releasing the
// request index entry through the usual lifecycle event.
func (rq *Request) rejectUpstream(
	ctx context.Context,
	req *sip.RequestEnvelope,
	err error,
	tx sip.ServerTransaction,
) {
	if _, ok := errors.AsType[*sip.RequestRejectedError](err); !ok {
		err = errors.Wrap(sip.NewRequestRejectedError(
			err,
			slog.LevelError,
			sip.ResponseStatusServerInternalError,
		))
	}
	if adj := adjustReqRejection(req, err); !errors.Is(adj, errReqConsumed) {
		if rej, ok := errors.AsType[*sip.RequestRejectedError](adj); ok {
			sts := rej.ResponseStatus()
			if !sts.IsValid() {
				sts = sip.ResponseStatusServerInternalError
			}
			res, resErr := req.NewResponse(sts, rej.RespondOptions().ResponseOptions)
			if resErr != nil {
				rq.prx.logger(ctx).DebugContext(
					ctx, "proxy abandonment response generation failed",
					slog.Any("proxy", rq.stageMeta(StageUpstream, "", "")),
					slog.Any("error", resErr),
				)
			} else if err := rq.sendUpstream(ctx, res, "", ""); err != nil {
				rq.prx.logger(ctx).DebugContext(
					ctx, "proxy abandonment response forwarding failed",
					slog.Any("proxy", rq.stageMeta(StageUpstream, "", "")),
					slog.Any("error", err),
				)
			}
		}
	}
	if err := tx.Terminate(ctx, nil); err != nil {
		rq.prx.logger(ctx).DebugContext(
			ctx, "proxy upstream transaction retirement failed",
			slog.Any("proxy", rq.stageMeta(StageUpstream, "", "")),
			slog.Any("error", err),
		)
	}
}
