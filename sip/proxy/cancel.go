package proxy

import (
	"context"
	"log/slog"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
)

// handleCancel dispatches one inbound CANCEL claimed by a route.
// RFC 3261 Sections 9.2 and 16.10.
//
// The CANCEL is matched against the live response contexts — the upstream
// server-transaction index — before any location resolution: a matching
// CANCEL belongs to the related request and never enters the ordinary
// forwarding pipeline. It receives its own non-INVITE server transaction
// created through the shared factory seam, so the route's
// ServerTransactionOptions pass through unchanged; the CANCEL transaction
// alone is answered 200 — a response is never sent on the original
// context — and the related coordinator records cancellation intent,
// closes scheduling, and cancels every eligible branch through the
// existing deferred builder.
//
// An unmatched CANCEL has no response context: per Section 16.10 it takes
// the deterministic stateless forwarding path like any other request. It
// is never answered 481, never authentication-challenged, and never gains
// a fabricated transaction or response context.
func (prx *Proxy) handleCancel(
	ctx context.Context,
	elm *sip.Element,
	route *Route,
	req *sip.RequestEnvelope,
) error {
	key, err := cancelMatchKey(req)
	if err != nil {
		return errors.Wrap(err)
	}
	if rq, ok := prx.loadSrvReq(elm, key); ok {
		return errors.Wrap(prx.admitMatchedCancel(ctx, elm, route, req, rq))
	}
	return errors.Wrap(prx.forwardUnmatchedCancel(ctx, elm, route, req))
}

// cancelMatchKey returns the server transaction key of the request the
// CANCEL relates to: the whole transaction identity is shared with only
// the CSeq method differing.
func cancelMatchKey(req *sip.RequestEnvelope) (sip.ServerTransactionKey, error) {
	key, err := sip.MakeServerTransactionKey(req)
	if err != nil {
		return sip.ServerTransactionKey{}, errors.Wrap(err)
	}
	key.Method = string(sip.RequestMethodInvite)
	return key, nil
}

// admitMatchedCancel owns one CANCEL that matched a live response context.
// It creates the CANCEL's own upstream server transaction through the same
// seam as the related request's — the route's sip.ServerTransactionOptions,
// including sip.NonInviteServerTransactionBehavior, pass through unchanged — answers the CANCEL
// with a 200, and records the cancellation intent on the related request
// coordinator. The intent is enqueued before the acknowledgement so an
// accepted CANCEL cannot leave a still-scheduling coordinator behind.
//
// A retransmitted CANCEL maps to the already admitted transaction and is
// consumed by transaction-local retransmission rules: no second
// cancellation pass ever runs.
func (prx *Proxy) admitMatchedCancel(
	ctx context.Context,
	elm *sip.Element,
	route *Route,
	req *sip.RequestEnvelope,
	rq *Request,
) error {
	tp := prx.srvTpOf(elm, req)
	if tp == nil {
		return errors.Wrap(sip.NewNoTransportError())
	}

	// Every effect of the matched admission — the transaction start, the
	// acknowledgement send, and the cancellation intent — runs under the
	// element that claimed the CANCEL.
	ctx = sip.ContextWithElement(ctx, elm)

	// A matched CANCEL is its own lightweight child operation: its claim
	// record keeps the claim-minted identity and gains only the live
	// parent link. The child identity travels with the cancellation
	// intent so the downstream CANCELs this admission spawns share the
	// attribution; the parent's own envelopes are never rewritten.
	linkRelatedReq(route, req, rq.requestID())

	// The CANCEL transaction is deliberately created without an ownership
	// token: it belongs to no request coordinator, so the admission never
	// attaches it to the related request.
	cncTx, err := prx.newSrvTx(ctx, elm, route, req, tp)
	if errors.Is(err, sip.ErrTransactionDuplicate) {
		if key, keyErr := sip.MakeServerTransactionKey(req); keyErr == nil {
			if existTx, ok := elm.LoadServerTransaction(key); ok {
				if err := existTx.RecvRequest(sip.ContextWithTransaction(ctx, existTx), req); err != nil {
					return errors.Wrap(err)
				}
				return errors.Wrap(errReqConsumed)
			}
		}
		return errors.Wrap(err)
	}
	if err != nil {
		return errors.Wrap(err)
	}

	ctx = sip.ContextWithTransaction(ctx, cncTx)
	if err := cncTx.Start(ctx); err != nil {
		return errors.Wrap(err)
	}

	rq.enqueue(ctx, workEvent{kind: evtCancel, relID: claimID(req)})

	res, err := req.NewResponse(sip.ResponseStatusOK)
	if err != nil {
		return errors.Wrap(err)
	}
	if err := cncTx.SendResponse(ctx, res); err != nil {
		// The cancellation was already accepted; a failed acknowledgement
		// is left to the transaction's own retransmission handling instead
		// of producing a second response for the CANCEL. The CANCEL's own
		// claim record — its child identity and live parent link —
		// correlates the line.
		md, _ := MetadataFrom(req.Metadata())
		prx.logger(ctx).DebugContext(
			ctx, "proxy cancel acknowledgement send failed",
			slog.Any("proxy", md),
			slog.Any("error", err),
		)
	}
	return errors.Wrap(errReqConsumed)
}

// forwardUnmatchedCancel applies the deterministic stateless forwarding of
// RFC 3261 Section 16.10 to a CANCEL with no live response context. It is
// processed like an ordinary request except that it can never be promoted
// to stateful forwarding and can never gain a transaction or response
// context: an undecidable routing is a routing failure, not a new context.
// It is never authentication-challenged either: a suggested 401/407
// rejection is consumed by the method response rules at the pipeline exit.
func (prx *Proxy) forwardUnmatchedCancel(
	ctx context.Context,
	elm *sip.Element,
	route *Route,
	req *sip.RequestEnvelope,
) error {
	return errors.Wrap(prx.forwardWithoutResCtx(ctx, elm, route, req))
}

// forwardAck dispatches one inbound ACK claimed by a route.
// RFC 3261 Sections 16.6, 17.1.1.3 and 17.2.1.
//
// An ACK never gains a response context of its own. A non-2xx ACK is
// hop-by-hop: it shares the server transaction identity of the INVITE it
// acknowledges — the same top Via branch and CSeq number — so it is
// dispatched to the matching INVITE server transaction, whose
// retransmission handling absorbs it: the transaction leaves Completed
// for Confirmed and stops resending the final response. A delivery
// failure there — for example an early ACK while the transaction still
// proceeds — is absorbed the same way: a hop-by-hop message is never
// re-routed and never answered.
//
// An ACK without a matching INVITE server transaction is end-to-end: it
// acknowledged a successful response and is routed through the same
// prepared-target path as any other request — authorization, routing
// preprocess, target resolution, single-destination stateless send —
// without a transaction and without a provisional generation. A routing
// failure is reported as an error; the method response rules at the
// pipeline exit guarantee it never becomes a SIP response.
func (prx *Proxy) forwardAck(
	ctx context.Context,
	elm *sip.Element,
	route *Route,
	req *sip.RequestEnvelope,
) error {
	key, err := sip.MakeServerTransactionKey(req)
	if err != nil {
		return errors.Wrap(err)
	}
	// The transaction manager index maps an ACK key onto the related
	// INVITE transaction, so a lookup finds a non-2xx ACK's hop-by-hop
	// owner and leaves an end-to-end ACK unmatched.
	if tx, ok := elm.LoadServerTransaction(key); ok {
		if err := tx.RecvRequest(sip.ContextWithTransaction(ctx, tx), req); err != nil {
			// The hop-by-hop ACK is consumed on its own claim record;
			// no related link is recorded for a dispatched ACK.
			md, _ := MetadataFrom(req.Metadata())
			prx.logger(ctx).DebugContext(
				ctx, "proxy ack dispatch to the invite transaction failed",
				slog.Any("proxy", md),
				slog.Any("error", err),
			)
		}
		return errors.Wrap(errReqConsumed)
	}

	// A forwarded related request still records the live parent link when
	// the association is really known: the server-request index shares
	// the whole transaction identity with the related INVITE but the CSeq
	// method — the same rule the transaction lookup above applies. An
	// end-to-end ACK is a transaction of its own, so no live relationship
	// is known and none is fabricated.
	key.Method = string(sip.RequestMethodInvite)
	if rq, ok := prx.loadSrvReq(elm, key); ok {
		linkRelatedReq(route, req, rq.requestID())
	}

	return errors.Wrap(prx.forwardWithoutResCtx(ctx, elm, route, req))
}

// forwardWithoutResCtx forwards a request that can never gain a
// transaction or response context — an unmatched CANCEL or an end-to-end
// ACK — through the shared prepared-target path: authorization, routing
// preprocess, target resolution, and the single-destination deterministic
// stateless send. A routing failure is a reported error, never a
// fabricated response context.
func (prx *Proxy) forwardWithoutResCtx(
	ctx context.Context,
	elm *sip.Element,
	route *Route,
	req *sip.RequestEnvelope,
) error {
	if err := prx.authorizeReq(ctx, route, req); err != nil {
		return errors.Wrap(err)
	}

	wReq, err := prx.preprocessRouting(ctx, elm, route, req)
	if err != nil {
		return errors.Wrap(err)
	}

	plan, err := prx.resolveTargets(ctx, route, wReq)
	if err != nil {
		return errors.Wrap(err)
	}

	return errors.Wrap(prx.forwardStatelessReq(ctx, elm, route, req, wReq, plan))
}
