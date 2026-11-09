package sip

import (
	"context"
	"iter"
	"sync"

	"github.com/ghettovoice/gosip/internal/errors"
)

// prxAdmission records one client-transaction admission created under a
// proxy ownership token before the outer send result is known.
//
// The admission binds its event subscriptions inside transaction admission —
// before Start — so callbacks can already enqueue events, but those events
// stay buffered on the admission until the runner learns the result of the
// outer send operation: a successful send commits the admission and replays
// the buffered events, while a failed send discards them. Correlation by the
// admission's own generation prevents events of a replaced or failed physical
// transaction — for example a discarded UDP-to-reliable size switch that
// shares the branch key — from being attributed to its successor.
type prxAdmission struct {
	targetID string
	runGen   uint64
	gen      uint64
	key      ClientTransactionKey
	tx       ClientTransaction
	events   []prxWorkEvent
}

// prxAttemptRunner owns the lazy address-attempt progression of one logical
// target. The candidate iterator is pulled only after a retryable initial
// failure or a retryable 503/transport/timeout outcome; it is never advanced
// while a sent transaction still awaits its outcome. Progression and errors
// remain owned by coordinator effects on the request drain goroutine, but
// retirement may be requested concurrently. The runner pointer itself is
// published to [prxTargetState] under the coordinator guard.
type prxAttemptRunner struct {
	targetID string
	gen      uint64
	nextFn   func() (*RequestAttempt, bool)
	stopFn   func()
	errs     []error

	mu      sync.Mutex
	pulling bool
	stopped bool
}

// next advances the single-owner iterator outside the runner guard. A
// concurrent retirement leaves cleanup to this pull and discards its result.
func (rn *prxAttemptRunner) next() (attempt *RequestAttempt, ok bool) {
	rn.mu.Lock()
	if rn.stopped {
		rn.mu.Unlock()
		return nil, false
	}
	rn.pulling = true
	rn.mu.Unlock()

	defer func() {
		rn.mu.Lock()
		rn.pulling = false
		stopped := rn.stopped
		rn.mu.Unlock()
		if stopped {
			rn.stopFn()
			attempt, ok = nil, false
		}
	}()

	return rn.nextFn()
}

// stop requests retirement without waiting for an active pull. Only an idle
// runner can stop immediately; otherwise the pull's completion owns cleanup.
func (rn *prxAttemptRunner) stop() {
	rn.mu.Lock()
	if rn.stopped {
		rn.mu.Unlock()
		return
	}
	rn.stopped = true
	idle := !rn.pulling
	rn.mu.Unlock()
	if idle {
		rn.stopFn()
	}
}

// startAttempt is the outside-guard effect that starts the address-attempt
// runner of one selected target. It prepares the outbound copy exactly once,
// then either binds the target's custom delivery or opens the lazy
// next-hop candidate iterator and hands control to the runner.
//
// A custom [ProxyDeliveryRef] is resolved once through
// [ProxyOptions.ResolveDelivery] and its transport and local/remote
// addresses are enforced through the element transaction factory; a failed
// or invalid binding settles the target without any implicit DNS or dial
// fallback.
func (rq *ProxyRequest) startAttempt(ctx context.Context, targetID string, gen uint64) {
	rq.mu.Lock()
	st := rq.targets[targetID]
	if st == nil || !st.started || st.done || st.gen != gen || rq.done || rq.cancelIntent {
		rq.mu.Unlock()
		return
	}
	var (
		prx    = rq.prx
		route  = rq.route
		input  = prxRequestInput{original: rq.req, working: rq.work}
		target = st.target
	)
	rq.mu.Unlock()

	outReq, err := prx.prepareTargetReq(ctx, route, input, target)
	if err != nil {
		err = errors.Wrap(err)
		rq.obsAttemptSettled(ctx, targetID, rq.nextAttemptID(targetID), err)
		rq.failAttempt(ctx, targetID, gen, err)
		return
	}

	if target.Delivery != (ProxyDeliveryRef{}) {
		rq.startBoundAttempt(ctx, targetID, gen, route, input, target, outReq)
		return
	}

	attempts, err := prx.elm.ProduceRequestAttempts(ctx, outReq)
	if err != nil {
		err = errors.Wrap(err)
		rq.obsAttemptSettled(ctx, targetID, rq.nextAttemptID(targetID), err)
		rq.failAttempt(ctx, targetID, gen, err)
		return
	}

	next, stop := iter.Pull(attempts)
	rn := &prxAttemptRunner{targetID: targetID, gen: gen, nextFn: next, stopFn: stop}

	rq.mu.Lock()
	if st = rq.targets[targetID]; st == nil || st.done || st.gen != gen || rq.done || rq.cancelIntent {
		rq.mu.Unlock()
		rn.stop()
		return
	}
	st.runner = rn
	rq.mu.Unlock()

	rq.advanceAttempt(ctx, targetID, gen)
}

// advanceAttempt pulls the next lazy next-hop candidate of the target's
// runner and sends it as a distinct client transaction. It runs on the
// request drain goroutine: after the initial start it is invoked again only
// when the current attempt settles with a retryable outcome.
//
// Each candidate gets its own finalized proxy Via with a fresh owned branch
// token, so ordinary retries use unique client branches and the lazy
// branch-regeneration inside [RequestAttempt] never strips the proxy
// namespace. An exhausted iterator or a terminal failure settles the target
// through the attempt-error event.
func (rq *ProxyRequest) advanceAttempt(ctx context.Context, targetID string, gen uint64) {
	rq.mu.Lock()
	st := rq.targets[targetID]
	if st == nil || !st.started || st.done || st.gen != gen || rq.done || rq.cancelIntent {
		rq.mu.Unlock()
		return
	}
	rn := st.runner
	if rn == nil {
		rq.mu.Unlock()
		return
	}
	var (
		prx   = rq.prx
		route = rq.route
		input = prxRequestInput{original: rq.req, working: rq.work}
	)
	rq.mu.Unlock()

	for {
		attempt, ok := rn.next()
		if !ok {
			rn.stop()
			rq.failAttempt(ctx, targetID, gen, attemptRunnerErrs(rn))
			return
		}
		if attempt == nil {
			continue
		}

		// The transport is known now: the proxy Via is finalized on this
		// candidate before the send, and the ownership token is installed so
		// admitted client transactions are claimed before they can emit an
		// event. Each candidate — including a failed pre-send one — is its
		// own observed attempt.
		attID := rq.nextAttemptID(targetID)
		if err := prx.finalizeTargetReq(ctx, route, input, attempt.Request, GenerateBranch(0)); err != nil {
			attErr := errors.Wrap(&RequestAttemptError{
				Cause:   err,
				Addr:    attempt.Addr,
				Request: cloneReqEnvelope(attempt.Request),
			})
			rq.recordAttemptAddr(targetID, gen, attempt.Addr)
			rn.errs = append(rn.errs, attErr)
			rq.obsAttemptSettled(ctx, targetID, attID, attErr)
			// Policy-scoped failures — a looped request, a SIPS downgrade,
			// an unusable record-route identity — abort the runner: every
			// remaining candidate would hit the same invariant.
			// Candidate-scoped failures (e.g. an unresolvable transport)
			// advance like ordinary per-attempt failures.
			if prxAttemptFinalizeFatal(err) {
				rn.stop()
				rq.failAttempt(ctx, targetID, gen, attemptRunnerErrs(rn))
				return
			}
			continue
		}

		owner := &prxTxOwner{rq: rq, targetID: targetID, gen: gen}
		tx, err := attempt.DoStateful(
			ctxWithPrxTxOwner(ctx, owner),
			route.ClientTransactionOptions,
		)
		rq.recordAttemptAddr(targetID, gen, attempt.Addr)
		if err != nil {
			rq.discardTxAdmissions(owner)
			attErr := errors.Wrap(&RequestAttemptError{
				Cause:       err,
				Addr:        attempt.Addr,
				Request:     cloneReqEnvelope(attempt.Request),
				Transaction: tx,
			})
			rn.errs = append(rn.errs, attErr)
			rq.obsAttemptSettled(ctx, targetID, attID, attErr)
			if prxAttemptSendRetryable(ctx, err) {
				continue
			}
			rn.stop()
			rq.failAttempt(ctx, targetID, gen, attErr)
			return
		}

		rq.commitTxAdmissions(ctx, owner, tx, attempt.Addr)
		if tx != nil {
			rq.noteAttemptID(tx.Key(), attID)
		}
		rq.obsAttemptSettled(ctx, targetID, attID, nil)
		return
	}
}

// startBoundAttempt runs the single bound-delivery send of a target with a
// nonzero [ProxyDeliveryRef]. The resolved transport and local/remote
// addresses are enforced through the element transaction factory; there is
// no implicit DNS or dial fallback and no retry, so outcome settlement
// follows the ordinary transaction events.
func (rq *ProxyRequest) startBoundAttempt(
	ctx context.Context,
	targetID string,
	gen uint64,
	route *ProxyRoute,
	input prxRequestInput,
	target ProxyTarget,
	outReq *RequestEnvelope,
) {
	prx := rq.prx
	attID := rq.nextAttemptID(targetID)

	bound, err := rq.bindAttemptDelivery(ctx, target.Delivery)
	if err != nil {
		attErr := errors.Wrap(&RequestAttemptError{Cause: err})
		rq.obsAttemptSettled(ctx, targetID, attID, attErr)
		rq.failAttempt(ctx, targetID, gen, attErr)
		return
	}
	if bound.Transport == nil {
		attErr := errors.Wrap(&RequestAttemptError{
			Cause: errors.ErrorfWrap("no bound transport for proxy target %q delivery", targetID),
		})
		rq.obsAttemptSettled(ctx, targetID, attID, attErr)
		rq.failAttempt(ctx, targetID, gen, attErr)
		return
	}
	if !bound.RemoteAddr.IsValid() {
		attErr := errors.Wrap(&RequestAttemptError{
			Cause: errors.ErrorfWrap("no bound remote address for proxy target %q delivery", targetID),
		})
		rq.obsAttemptSettled(ctx, targetID, attID, attErr)
		rq.failAttempt(ctx, targetID, gen, attErr)
		return
	}

	outReq.SetTransport(bound.Transport.Metadata()).
		SetLocalAddr(bound.LocalAddr).
		SetRemoteAddr(bound.RemoteAddr)

	if err := prx.finalizeTargetReq(ctx, route, input, outReq, GenerateBranch(0)); err != nil {
		err = errors.Wrap(err)
		rq.obsAttemptSettled(ctx, targetID, attID, err)
		rq.failAttempt(ctx, targetID, gen, err)
		return
	}

	owner := &prxTxOwner{rq: rq, targetID: targetID, gen: gen}
	ownerCtx := ctxWithPrxTxOwner(ctx, owner)
	attAddr := ResolvedAddr{
		Transport: bound.Transport.Metadata().Proto,
		Addr:      bound.RemoteAddr,
	}

	tx, err := prx.elm.NewClientTransaction(ownerCtx, outReq, bound.Transport, route.ClientTransactionOptions)
	if err != nil {
		rq.discardTxAdmissions(owner)
		attErr := errors.Wrap(&RequestAttemptError{Cause: err})
		rq.obsAttemptSettled(ctx, targetID, attID, attErr)
		rq.failAttempt(ctx, targetID, gen, attErr)
		return
	}
	if err := tx.Start(ContextWithTransaction(ownerCtx, tx)); err != nil {
		rq.discardTxAdmissions(owner)
		rq.recordAttemptAddr(targetID, gen, attAddr)
		attErr := errors.Wrap(&RequestAttemptError{
			Cause:       err,
			Addr:        attAddr,
			Request:     cloneReqEnvelope(outReq),
			Transaction: tx,
		})
		rq.obsAttemptSettled(ctx, targetID, attID, attErr)
		rq.failAttempt(ctx, targetID, gen, attErr)
		return
	}

	rq.recordAttemptAddr(targetID, gen, attAddr)
	rq.commitTxAdmissions(ctx, owner, tx, attAddr)
	rq.noteAttemptID(tx.Key(), attID)
	rq.obsAttemptSettled(ctx, targetID, attID, nil)
}

// failAttempt reports a runner-level failure of one attempt generation to
// the coordinator.
func (rq *ProxyRequest) failAttempt(ctx context.Context, targetID string, gen uint64, err error) {
	rq.enqueue(ctx, prxWorkEvent{
		kind:     prxEvtAttemptErr,
		targetID: targetID,
		gen:      gen,
		err:      err,
	})
}

// recordAttemptAddr appends one executed candidate address to the target's
// attempt history used by request snapshots.
func (rq *ProxyRequest) recordAttemptAddr(targetID string, gen uint64, addr ResolvedAddr) {
	rq.mu.Lock()
	defer rq.mu.Unlock()
	if st := rq.targets[targetID]; st != nil && st.gen == gen {
		st.attemptAddrs = append(st.attemptAddrs, addr)
	}
}

// commitTxAdmissions settles the pending client-transaction admissions
// created under one ownership token: the admission of the winning
// transaction — the last one admitted for its key — becomes the correlated
// attempt and replays its buffered events, while every other admission is
// discarded with its events so a replaced physical transaction can never
// terminate its successor.
func (rq *ProxyRequest) commitTxAdmissions(
	ctx context.Context,
	owner *prxTxOwner,
	tx ClientTransaction,
	addr ResolvedAddr,
) {
	rq.mu.Lock()
	var win *prxAdmission
	for _, adm := range owner.adms {
		delete(rq.pendingAdms, adm.gen)
		if tx != nil && adm.key.Equal(tx.Key()) {
			win = adm
		}
	}
	if win == nil && len(owner.adms) > 0 {
		win = owner.adms[len(owner.adms)-1]
	}
	var replay []prxWorkEvent
	var cancelTx ClientTransaction
	if win != nil {
		att := &prxAttempt{
			targetID: win.targetID,
			gen:      win.gen,
			tx:       tx,
			addr:     addr,
			// A committed admission implies the initial send's observation
			// already succeeded; a restore install overwrites the markers
			// with the checkpoint's stored values.
			sendSettled: true,
			sendOK:      true,
		}
		rq.attempts[win.key] = att
		if st := rq.targets[win.targetID]; st != nil && st.gen == win.runGen {
			st.txKey = win.key
			// A cancel intent that landed while this admission was still
			// pending must not be lost: emit the downstream CANCEL when
			// the committed transaction already admits one. A transaction
			// still in Calling keeps the intent unsettled so its first
			// provisional fires the deferred CANCEL instead; an unstarted
			// dormant restored transaction keeps it unsettled the same
			// way — it cannot legally carry a CANCEL yet — so the
			// activation resume emits it once the binding runs.
			if st.cancelling && tx != nil &&
				tx.Type() == TransactionTypeClientInvite &&
				tx.Started() &&
				tx.State() == TransactionStateProceeding {
				att.cancelled = true
				cancelTx = tx
			}
		}
		replay = win.events
		win.events = nil
	}
	rq.mu.Unlock()

	if cancelTx != nil {
		rq.cancelAttemptTx(ctx, cancelTx)
	}
	for _, ev := range replay {
		rq.enqueue(ctx, ev)
	}
}

// noteAttemptID records the observation correlation ID of the attempt
// committed under key, so later response sends attributed to the attempt
// carry it into their events.
func (rq *ProxyRequest) noteAttemptID(key ClientTransactionKey, attID string) {
	rq.mu.Lock()
	defer rq.mu.Unlock()
	if att := rq.attempts[key]; att != nil {
		att.attID = attID
	}
}

// discardTxAdmissions drops the pending admissions created under one
// ownership token together with their buffered events.
func (rq *ProxyRequest) discardTxAdmissions(owner *prxTxOwner) {
	rq.mu.Lock()
	for _, adm := range owner.adms {
		delete(rq.pendingAdms, adm.gen)
	}
	owner.adms = nil
	rq.mu.Unlock()
}

// attemptRunnerErrs joins the runner's accumulated attempt errors the same
// way the exhausted-iterator path reports them, or reports a bare
// no-address error when no candidate produced a failure.
func attemptRunnerErrs(rn *prxAttemptRunner) error {
	if err := errors.JoinPrefixWrap("send request errors:", rn.errs...); err != nil {
		return err
	}
	return errors.Wrap(NewNoAddressError())
}

// prxAttemptFinalizeFatal classifies a [Proxy.finalizeTargetReq]
// failure: policy-scoped invariants shared by every remaining candidate
// (a detected loop, a SIPS downgrade, an unusable record-route identity)
// abort the runner, while candidate-scoped failures (unresolvable
// transport, per-candidate validation) advance to the next candidate.
func prxAttemptFinalizeFatal(err error) bool {
	return errors.Is(err, errLoopDetected) ||
		errors.Is(err, errSecureDowngrade) ||
		errors.Is(err, errNoRecRouteIdentity) ||
		errors.Is(err, errAmbiguousIdentity)
}

// prxAttemptSendRetryable classifies an initial-send failure of a request
// attempt following the element request-attempt rules: a failure matching
// the send context's own error, element or manager shutdown, non-size
// message errors and explicit terminal errors stop the failover; anything
// else advances to the next candidate.
//
// Parity note: [Element.runReqAttempts] compares against ctx.Err() of the
// sending call, and the runner's drain context never cancels, so context
// errors surfacing from inside the transport machinery (for example dial
// deadlines) stay retryable exactly as they do for the element.
func prxAttemptSendRetryable(ctx context.Context, err error) bool {
	switch {
	case errors.Is(err, ctx.Err()),
		errors.Is(err, ErrElementClosed),
		errors.Is(err, ErrTransportManagerClosed),
		errors.Is(err, ErrTransactionManagerClosed):
		return false
	case IsMessageError(err) &&
		!errors.Is(err, ErrMessageBodyTooLarge) &&
		!errors.Is(err, ErrMessageTooLarge):
		return false
	}
	if attErr, ok := errors.AsType[*RequestAttemptError](err); ok && attErr.Terminal {
		return false
	}
	return true
}

// prxAttemptOutcomeRetryable classifies a settled transaction outcome
// without a final response following RFC 3263 direction-confirmation rules:
// a transport error always advances to the next candidate, while a
// transaction timeout advances only when no response was ever seen — after
// a provisional response the server proved reachable and the outcome is
// terminal.
func prxAttemptOutcomeRetryable(att *prxAttempt, err error) bool {
	switch {
	case IsTransportError(err):
		return true
	case errors.Is(err, ErrTransactionTimedOut):
		return !att.sawRes
	default:
		return false
	}
}
