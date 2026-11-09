package sip

import (
	"context"
	"log/slog"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
)

// errTmrCStale reports a Timer C firing that no longer names a live armed
// attempt: the target already settled or disarmed, or the deadline was
// re-armed past the firing.
const errTmrCStale errors.Error = "stale proxy timer C expiry"

// armTmrCLocked records the absolute Timer C deadline of one selected
// INVITE attempt generation and arms its runtime timer. The deadline bounds
// the whole attempt generation, so it survives a failover between candidate
// client transactions; the firing callback resolves the target's committed
// state at expiry time, and fires even while no attempt transaction is
// committed — an admission pending past its deadline must never lose it.
// The expiry itself is reported through the coordinator event queue — the
// callback never decides inline.
func (rq *ProxyRequest) armTmrCLocked(ctx context.Context, st *prxTargetState) {
	dur := rq.prx.opts.TimerC
	st.timeC = time.Now().Add(dur)
	if st.tmrC != nil {
		st.tmrC.Stop()
	}
	st.tmrC = time.AfterFunc(dur, func() {
		if err := rq.onTmrC(ctx, st); err != nil {
			rq.prx.logger(ctx).DebugContext(
				ctx, "proxy timer C expiry dropped",
				slog.Any("error", err),
			)
		}
	})
}

// armRestoredTmrCLocked re-arms the runtime Timer C of a restored target
// at its preserved absolute deadline. Unlike armTimerCLocked it never
// computes a fresh interval: an already expired deadline fires immediately
// so the expiry policy applies at activation, and a future deadline keeps
// bounding the attempt generation exactly as it did before the checkpoint.
func (rq *ProxyRequest) armRestoredTmrCLocked(ctx context.Context, st *prxTargetState) {
	deadline := st.timeC
	if st.tmrC != nil {
		st.tmrC.Stop()
	}
	st.tmrC = time.AfterFunc(time.Until(deadline), func() {
		if err := rq.onTmrC(ctx, st); err != nil {
			rq.prx.logger(ctx).DebugContext(
				ctx, "proxy timer C expiry dropped",
				slog.Any("error", err),
			)
		}
	})
}

// rearmTmrCLocked extends the armed Timer C of a live attempt to
// now + duration. It runs only for 101-199 provisional responses: a 100
// Trying never refreshes the deadline, and a disarmed timer is never
// resurrected.
func (rq *ProxyRequest) rearmTmrCLocked(st *prxTargetState) {
	if st.tmrC == nil {
		return
	}
	dur := rq.prx.opts.TimerC
	st.timeC = time.Now().Add(dur)
	st.tmrC.Reset(dur)
}

// disarmAllTmrsLocked settles every plan target and releases its armed
// Timer C deadline at the lifecycle end: a retired coordinator must never
// let a deadline fire. Transaction-owned timers — including a pending
// Trying — stop separately through the transaction terminations the
// shutdown runs.
func (rq *ProxyRequest) disarmAllTmrsLocked() {
	for _, st := range rq.targets {
		st.done = true
		disarmTmrCLocked(st)
	}
}

// disarmTmrCLocked stops the target's armed Timer C and releases the
// recorded deadline. A truly settled or abandoned attempt has no further
// use for it; a failover between candidate transactions keeps it armed, and
// so does an initiated cancellation — a downstream CANCEL never settles
// the branch by itself.
func disarmTmrCLocked(st *prxTargetState) {
	st.timeC = time.Time{}
	if st.tmrC != nil {
		st.tmrC.Stop()
		st.tmrC = nil
	}
}

// onTmrC reports the expiry of one target's armed Timer C to the
// coordinator. Like every other coordinator input it only translates the
// firing into a queued expiry event; the drain applies the policy. A firing
// that no longer matches the armed deadline is reported as
// [errTimerCStale] — the target already settled or disarmed, or a re-armed
// deadline moved past it. An expiry without a committed attempt
// transaction still queues: the deadline bounds the attempt generation,
// not any single transaction key.
func (rq *ProxyRequest) onTmrC(ctx context.Context, st *prxTargetState) error {
	rq.mu.Lock()
	var ev prxWorkEvent
	switch {
	case st.done,
		st.timeC.IsZero() || st.timeC.After(time.Now()):
	default:
		ev = prxWorkEvent{kind: prxEvtTmrC, targetID: st.target.ID, gen: st.gen, key: st.txKey}
	}
	rq.mu.Unlock()

	if ev.kind != prxEvtTmrC {
		return errors.PrefixWrap(errTmrCStale, "target %q", st.target.ID)
	}
	rq.enqueue(ctx, ev)
	return nil
}

// applyTmrCLocked applies a queued Timer C expiry to the attempt
// generation it names. Only an armed deadline that was actually reached
// expires the attempt: a re-armed or already released deadline drops the
// firing. The expiry acts on the currently committed attempt transaction of
// the generation, so a firing that lands mid-failover still bounds the
// attempt — and one landing before any transaction committed settles the
// pending generation as a timeout.
//
// The terminate-on-expiry policy follows RFC 3261 Section 16.6: an attempt
// that received a provisional response is cancelled downstream — its
// eventual final response, usually a 487, settles it through the ordinary
// response path — and keeps its deadline re-armed so a cancelled branch
// that never answers is still bounded. An expiry landing on an
// already-cancelled attempt never re-issues the downstream CANCEL: like an
// attempt without any provisional it settles as a timeout outcome, as if
// the branch transaction had received a 408.
func (rq *ProxyRequest) applyTmrCLocked(_ context.Context, ev prxWorkEvent) []prxEffect {
	st := rq.targets[ev.targetID]
	if rq.done || st == nil || st.done || st.gen != ev.gen ||
		st.timeC.IsZero() || st.timeC.After(time.Now()) {
		return nil
	}

	att := rq.attempts[st.txKey]
	if att != nil && !att.settled && att.sawRes && !att.cancelled {
		// Cancellation never settles the branch by itself: a racing final
		// response must still reach the response context, and the re-armed
		// deadline bounds the cancelled branch's answer.
		st.cancelling = true
		att.cancelled = true
		rq.rearmTmrCLocked(st)
		tx := att.tx
		return []prxEffect{func(ctx context.Context) {
			rq.cancelAttemptTx(ctx, tx)
		}}
	}

	disarmTmrCLocked(st)
	st.done = true
	err := NewTransactionTimedOutError()
	oc := ProxyBranchOutcome{
		TargetID:  ev.targetID,
		Error:     err,
		Completed: true,
	}
	var effects []prxEffect
	if att != nil && !att.settled {
		att.settled = true
		oc.AttemptKey = st.txKey
		oc.Cancelled = att.cancelled
		if tx := att.tx; tx != nil {
			effects = append(effects, func(ctx context.Context) {
				if err := tx.Terminate(ctx, nil); err != nil {
					rq.prx.logger(ctx).
						DebugContext(ctx, "proxy timer C branch transaction retirement failed", slog.Any("error", err))
				}
			})
		}
	}
	rq.outcomes = append(rq.outcomes, oc)
	rq.lastErr = err
	rq.queueLocked(prxWorkEvent{kind: prxEvtSchedule})
	effects = append(effects, detachAttemptRunnerLocked(st)...)
	return append(effects, rq.finishLocked()...)
}
