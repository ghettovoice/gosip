package sip

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
)

// tmrCDeadline returns the coordinator's armed Timer C deadline for the
// attempt owning client transaction key, and whether a runtime timer is
// armed for it.
func tmrCDeadline(
	tb testing.TB,
	rq *ProxyRequest,
	key ClientTransactionKey,
) (deadline time.Time, armed bool) {
	tb.Helper()

	rq.mu.Lock()
	defer rq.mu.Unlock()

	att := rq.attempts[key]
	if att == nil {
		tb.Fatalf("no attempt for client transaction key %v", key)
	}
	st := rq.targets[att.targetID]
	if st == nil {
		tb.Fatalf("no target state for attempt target %q", att.targetID)
	}
	return st.timeC, st.tmrC != nil
}

// waitTmrCCancel waits for the downstream CANCEL one attempt emits on a
// Timer C expiry, skipping any unrelated sends.
func waitTmrCCancel(tb testing.TB, tp *prxInternalTransp) *RequestEnvelope {
	tb.Helper()

	for {
		req := testutil.WaitResult(tb, tp.sendReqCalls, "downstream CANCEL")
		if req.Method().Equal(RequestMethodCancel) {
			return req
		}
	}
}

// drainInternalRess collects the recorded upstream response sends in
// arrival order.
func drainInternalRess(tp *prxInternalTransp) []*ResponseEnvelope {
	var out []*ResponseEnvelope
	for {
		select {
		case res := <-tp.sendResCalls:
			out = append(out, res)
		default:
			return out
		}
	}
}

func resStatusList(ress []*ResponseEnvelope) []int {
	out := make([]int, 0, len(ress))
	for _, res := range ress {
		out = append(out, int(res.Status()))
	}
	return out
}

func TestProxy_TimerC(t *testing.T) {
	t.Parallel()

	served := []string{"alice.voip.com"}
	litRoute := func(name string) ProxyRoute {
		return ProxyRoute{
			Name:        name,
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
	}
	// commitAttempt admits the request and waits for the single outbound
	// branch send, returning the coordinator and the committed attempt key.
	commitAttempt := func(
		tb testing.TB,
		prx *Proxy,
		tp *prxInternalTransp,
		branch string,
	) (*ProxyRequest, ClientTransactionKey, *RequestEnvelope) {
		tb.Helper()

		rq := admitInternalReq(tb, prx, newInternalInviteReq(branch))
		synctest.Wait()

		outReq := testutil.WaitResult(tb, tp.sendReqCalls, "outbound branch send")
		key, err := MakeClientTransactionKey(outReq)
		if err != nil {
			tb.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		return rq, key, outReq
	}

	t.Run("option validation", func(t *testing.T) {
		t.Parallel()

		for _, tt := range []struct {
			name    string
			timerC  time.Duration
			wantErr bool
		}{
			{"negative", -time.Second, true},
			{"positive below minimum", time.Minute, true},
			{"at minimum", 3 * time.Minute, true},
			{"above minimum", 4 * time.Minute, false},
			{"zero selects default", 0, false},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				elm := &Element{}
				t.Cleanup(func() { elm.Close() })

				_, err := NewProxy(elm, []ProxyRoute{{Name: "r"}}, ProxyOptions{TimerC: tt.timerC})
				if tt.wantErr && err == nil {
					t.Fatalf("sip.NewProxy(TimerC=%s) error = nil, want error", tt.timerC)
				}
				if !tt.wantErr && err != nil {
					t.Fatalf("sip.NewProxy(TimerC=%s) error = %v, want nil", tt.timerC, err)
				}
			})
		}
	})

	t.Run("armed per attempt independent of T1", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			// A pathological T1 drives every transaction timer but must not
			// shorten the independent Timer C deadline.
			route := litRoute("timer-c-t1")
			route.ClientTransactionOptions = ClientTransactionOptions{
				Timing: TimingConfig{T1: time.Millisecond},
			}
			_, prx, tp := newInternalTestProxy(t, route, ProxyOptions{ServedDomains: served})
			rq, key, _ := commitAttempt(t, prx, tp, MagicCookie+".timer-c-t1")
			// The white-box test captures the coordinator's armed Timer C
			// deadline for the attempt key into `deadline`.
			now := time.Now()
			deadline, armed := tmrCDeadline(t, rq, key)
			if !armed {
				t.Fatal("INVITE attempt has no armed Timer C")
			}
			if got, want := deadline.Sub(now), 5*time.Minute; got != want {
				t.Fatalf("Timer C=%s, want %s", got, want)
			}
		})
	})

	t.Run("re-arms only on 101-199 and cancels on expiry", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			// The upstream transaction's automatic 100 Trying is pushed past
			// the test window so only proxy-forwarded responses are recorded.
			route := litRoute("timer-c-rearm")
			route.ServerTransactionOptions = ServerTransactionOptions{
				Timing: TimingConfig{Time100: time.Hour},
			}
			elm, prx, tp := newInternalTestProxy(t, route, ProxyOptions{ServedDomains: served})

			rq, key, outReq := commitAttempt(t, prx, tp, MagicCookie+".timer-c-rearm")
			clnTx, ok := elm.LoadClientTransaction(key)
			if !ok {
				t.Fatal("forwarding client transaction not registered")
			}

			now := time.Now()
			deadline0, armed := tmrCDeadline(t, rq, key)
			if !armed {
				t.Fatal("INVITE attempt has no armed Timer C")
			}
			if got, want := deadline0.Sub(now), 5*time.Minute; got != want {
				t.Fatalf("Timer C=%s, want %s", got, want)
			}

			// A 100 Trying is attributed but never refreshes the deadline.
			if err := clnTx.RecvResponse(ctx, newInternalInRes(t, outReq, ResponseStatusTrying)); err != nil {
				t.Fatalf("clnTx.RecvResponse(100) error = %v, want nil", err)
			}
			synctest.Wait()
			deadline, armed := tmrCDeadline(t, rq, key)
			if !armed || !deadline.Equal(deadline0) {
				t.Fatalf("Timer C after 100 Trying = (%v, armed=%t), want (%v, armed=true)", deadline, armed, deadline0)
			}

			// A 101-199 provisional re-arms to now + duration.
			time.Sleep(time.Minute)
			if err := clnTx.RecvResponse(ctx, newInternalInRes(t, outReq, ResponseStatusRinging)); err != nil {
				t.Fatalf("clnTx.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()

			now = time.Now()
			deadline, armed = tmrCDeadline(t, rq, key)
			if !armed {
				t.Fatal("Timer C disarmed on first provisional")
			}
			if got, want := deadline.Sub(now), 5*time.Minute; got != want {
				t.Fatalf("Timer C after 180 Ringing=%s, want %s", got, want)
			}
			if !deadline.After(deadline0) {
				t.Fatalf("Timer C after 180 Ringing = %v, want later than %v", deadline, deadline0)
			}

			// The expiry of an attempt that saw a provisional cancels the
			// branch downstream; the attempt itself stays unsettled until the
			// final response arrives, and keeps its re-armed deadline so a
			// cancelled branch that never answers stays bounded.
			time.Sleep(5*time.Minute + time.Second)
			synctest.Wait()

			waitTmrCCancel(t, tp)
			rq.mu.Lock()
			att := rq.attempts[key]
			st := rq.targets["A"]
			cancelled := att != nil && att.cancelled
			cancelling := st != nil && st.cancelling
			settled := att == nil || att.settled || st == nil || st.done
			rq.mu.Unlock()
			if !cancelled || !cancelling {
				t.Fatal("Timer C expiry did not cancel the provisional-bearing attempt")
			}
			if settled {
				t.Fatal("Timer C expiry settled the attempt instead of awaiting the cancelled final")
			}
			deadline, armed = tmrCDeadline(t, rq, key)
			if !armed || deadline.IsZero() {
				t.Fatal("cancelled attempt lost its armed Timer C deadline")
			}

			// The downstream 487 settles the attempt and is forwarded upstream.
			if err := clnTx.RecvResponse(ctx, newInternalInRes(t, outReq, ResponseStatusRequestTerminated)); err != nil {
				t.Fatalf("clnTx.RecvResponse(487) error = %v, want nil", err)
			}
			synctest.Wait()

			if diff := cmp.Diff([]int{180, 487}, resStatusList(drainInternalRess(tp))); diff != "" {
				t.Fatalf("upstream responses (-want +got): %s", diff)
			}
			deadline, armed = tmrCDeadline(t, rq, key)
			if armed || !deadline.IsZero() {
				t.Fatal("settled attempt kept its Timer C state")
			}
			rq.mu.Lock()
			defer rq.mu.Unlock()
			if !rq.done || rq.outcome != ProxyOutcomeResponse {
				t.Fatalf("request outcome=(done=%t, %v), want (true, response)", rq.done, rq.outcome)
			}
		})
	})

	t.Run("expiry on a cancelled attempt settles as timeout", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			// The transaction timers stay past the Timer C window so the
			// expiry policy decides the cancelled branch.
			route := litRoute("timer-c-cancelled")
			route.ClientTransactionOptions = ClientTransactionOptions{
				Timing: TimingConfig{T1: time.Hour},
			}
			route.ServerTransactionOptions = ServerTransactionOptions{
				Timing: TimingConfig{T1: time.Hour, Time100: time.Hour},
			}
			elm, prx, tp := newInternalTestProxy(t, route, ProxyOptions{ServedDomains: served})

			rq, key, outReq := commitAttempt(t, prx, tp, MagicCookie+".timer-c-cancelled")
			clnTx, ok := elm.LoadClientTransaction(key)
			if !ok {
				t.Fatal("forwarding client transaction not registered")
			}

			// A provisional moves the branch to Proceeding.
			if err := clnTx.RecvResponse(ctx, newInternalInRes(t, outReq, ResponseStatusRinging)); err != nil {
				t.Fatalf("clnTx.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()

			// An accepted upstream CANCEL initiates the downstream
			// cancellation; the cancelled branch keeps its armed Timer C.
			rq.enqueue(ctx, prxWorkEvent{kind: prxEvtCancel})
			synctest.Wait()
			if cancel := waitTmrCCancel(t, tp); cancel == nil {
				t.Fatal("cancelled attempt emitted no downstream CANCEL")
			}

			if deadline, armed := tmrCDeadline(t, rq, key); !armed || deadline.IsZero() {
				t.Fatal("cancelled attempt lost its armed Timer C deadline")
			}

			// The downstream never answers the cancelled INVITE: the next
			// expiry settles the attempt as a timeout instead of re-issuing
			// the CANCEL, and the request completes with the generated 487.
			// The initiated CANCEL transaction keeps retransmitting on its
			// own timer — drain its sends while the deadline window passes.
			for range 6 {
				time.Sleep(time.Minute)
				countReqMethodInternal(tp, RequestMethodCancel)
			}
			synctest.Wait()

			if diff := cmp.Diff([]int{180, 487}, resStatusList(drainInternalRess(tp))); diff != "" {
				t.Fatalf("upstream responses (-want +got): %s", diff)
			}

			rq.mu.Lock()
			defer rq.mu.Unlock()
			if !rq.done || rq.outcome != ProxyOutcomeCancelled {
				t.Fatalf("request outcome=(done=%t, %v), want (true, cancelled)", rq.done, rq.outcome)
			}
			if len(rq.outcomes) != 1 {
				t.Fatalf("recorded outcomes=%d, want 1", len(rq.outcomes))
			}
			oc := rq.outcomes[0]
			if oc.TargetID != "A" || !oc.AttemptKey.Equal(key) || !oc.Completed || !oc.Cancelled || !errors.Is(oc.Error, ErrTransactionTimedOut) {
				t.Fatalf("outcome=%+v, want completed cancelled timeout outcome for target A", oc)
			}
			st := rq.targets["A"]
			if st == nil || !st.done || !st.timeC.IsZero() || st.tmrC != nil {
				t.Fatal("settled attempt kept its Timer C state")
			}
		})
	})

	t.Run("expiry without a committed transaction settles as timeout", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			route := litRoute("timer-c-pending")
			route.ClientTransactionOptions = ClientTransactionOptions{
				Timing: TimingConfig{T1: time.Hour},
			}
			route.ServerTransactionOptions = ServerTransactionOptions{
				Timing: TimingConfig{T1: time.Hour, Time100: time.Hour},
			}
			_, prx, tp := newInternalTestProxy(t, route, ProxyOptions{ServedDomains: served})
			rq, key, _ := commitAttempt(t, prx, tp, MagicCookie+".timer-c-pending")

			// Roll the target back to a pending admission: no committed
			// attempt transaction and a deadline already reached. The
			// armed runtime timer still fires and the expiry settles the
			// attempt generation as a timeout — a pending admission must
			// never lose its deadline.
			rq.mu.Lock()
			st := rq.targets["A"]
			delete(rq.attempts, key)
			st.txKey = ClientTransactionKey{}
			st.timeC = time.Now()
			st.tmrC.Reset(0)
			rq.mu.Unlock()
			synctest.Wait()

			if diff := cmp.Diff([]int{408}, resStatusList(drainInternalRess(tp))); diff != "" {
				t.Fatalf("upstream responses (-want +got): %s", diff)
			}

			rq.mu.Lock()
			defer rq.mu.Unlock()
			if !rq.done || rq.outcome != ProxyOutcomeTimeout {
				t.Fatalf("request outcome=(done=%t, %v), want (true, timeout)", rq.done, rq.outcome)
			}
			if len(rq.outcomes) != 1 {
				t.Fatalf("recorded outcomes=%d, want 1", len(rq.outcomes))
			}
			oc := rq.outcomes[0]
			if oc.TargetID != "A" || oc.AttemptKey.IsValid() || !oc.Completed ||
				!errors.Is(oc.Error, ErrTransactionTimedOut) {
				t.Fatalf("outcome=%+v, want completed timeout outcome for target A", oc)
			}
			if st == nil || !st.done || !st.timeC.IsZero() || st.tmrC != nil {
				t.Fatal("settled attempt kept its Timer C state")
			}
		})
	})

	t.Run("expiry without provisional settles as timeout", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			// Push the client transaction's own timers past the Timer C window
			// so the expiry policy runs while the branch is still Calling.
			route := litRoute("timer-c-timeout")
			route.ClientTransactionOptions = ClientTransactionOptions{
				Timing: TimingConfig{T1: time.Hour},
			}
			route.ServerTransactionOptions = ServerTransactionOptions{
				Timing: TimingConfig{T1: time.Hour, Time100: time.Hour},
			}
			_, prx, tp := newInternalTestProxy(t, route, ProxyOptions{ServedDomains: served})

			rq, key, _ := commitAttempt(t, prx, tp, MagicCookie+".timer-c-timeout")
			if _, armed := tmrCDeadline(t, rq, key); !armed {
				t.Fatal("INVITE attempt has no armed Timer C")
			}

			time.Sleep(5*time.Minute + time.Second)
			synctest.Wait()

			// No provisional was seen: no CANCEL is emitted and the attempt
			// settles as a timeout outcome.
			if got := countReqMethodInternal(tp, RequestMethodCancel); got != 0 {
				t.Fatalf("downstream CANCEL sends=%d, want 0", got)
			}
			if diff := cmp.Diff([]int{408}, resStatusList(drainInternalRess(tp))); diff != "" {
				t.Fatalf("upstream responses (-want +got): %s", diff)
			}

			rq.mu.Lock()
			defer rq.mu.Unlock()
			if !rq.done || rq.outcome != ProxyOutcomeTimeout {
				t.Fatalf("request outcome=(done=%t, %v), want (true, timeout)", rq.done, rq.outcome)
			}
			if len(rq.outcomes) != 1 {
				t.Fatalf("recorded outcomes=%d, want 1", len(rq.outcomes))
			}
			oc := rq.outcomes[0]
			if oc.TargetID != "A" || !oc.AttemptKey.Equal(key) || !oc.Completed ||
				!errors.Is(oc.Error, ErrTransactionTimedOut) {
				t.Fatalf("outcome=%+v, want completed timeout outcome for target A", oc)
			}
			st := rq.targets["A"]
			if st == nil || !st.done || !st.timeC.IsZero() || st.tmrC != nil {
				t.Fatal("settled attempt kept its Timer C state")
			}
		})
	})
}

// countReqMethodInternal counts the recorded outbound sends carrying mtd.
func countReqMethodInternal(tp *prxInternalTransp, mtd RequestMethod) int {
	cnt := 0
	for {
		select {
		case req := <-tp.sendReqCalls:
			if req.Method().Equal(mtd) {
				cnt++
			}
		default:
			return cnt
		}
	}
}
