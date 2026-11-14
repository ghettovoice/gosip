package proxy

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

func TestProxy_FinalResponseResolutionRecovery(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		statuses   []sip.ResponseStatus
		index      int
		wantStatus sip.ResponseStatus
		wantTag    string
		wantChoice string
	}{
		{"same status selects second original", []sip.ResponseStatus{486, 486}, 1, 486, "second", "B"},
		{"custom class preference", []sip.ResponseStatus{302, 486}, 1, 486, "second", "B"},
		{"lone 503 with non-response sibling", []sip.ResponseStatus{503}, 0, 500, "first", "A"},
		{"custom 407 combines challenges", []sip.ResponseStatus{401, 407}, 1, 407, "second", "B"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, policy := range []string{"error if called", "prefer A"} {
				t.Run(policy, func(t *testing.T) {
					t.Parallel()

					synctest.Test(t, func(t *testing.T) {
						ctx := t.Context()
						choiceValue := func(res *sip.ResponseEnvelope) string {
							t.Helper()
							var value string
							res.WithMessage(func(msg *sip.Response) {
								hdrs := msg.Headers.Get("X-Resolver-Choice")
								if len(hdrs) != 1 {
									t.Fatalf("X-Resolver-Choice headers = %d, want 1", len(hdrs))
								}
								value = hdrs[0].RenderValue()
							})
							return value
						}
						assertChallenges := func(res *sip.ResponseEnvelope, want []string) {
							t.Helper()
							got := resChallenges(res)
							if len(got) != len(want) {
								t.Fatalf("authentication challenges = %v, want exactly %v", got, want)
							}
							for i, literal := range want {
								expected, err := header.Parse(literal)
								if err != nil {
									t.Fatalf("header.Parse(%q) error = %v, want nil", literal, err)
								}
								if !got[i].Equal(expected) {
									t.Fatalf("authentication challenge %d = %v, want %s", i, got[i], literal)
								}
							}
						}
						var wantChallenges []string
						if tt.wantStatus == 407 {
							wantChallenges = []string{
								`WWW-Authenticate: Digest realm="A", nonce="first"`,
								`Proxy-Authenticate: Digest realm="B", nonce="second"`,
							}
						}
						var originalCalls atomic.Int32
						route := Route{
							Name:        "resolution-recovery",
							ForwardMode: ForwardModeStateful,
							ResolveFinalResponse: func(context.Context, FinalResponseSelection) (int, error) {
								originalCalls.Add(1)
								return tt.index, nil
							},
						}
						req := newInternalInviteReq(sip.MagicCookie + ".resolution-recovery")
						elm, tp, rq := admitFinalResolverFork(t, route, req)
						for i, id := range []string{"A", "B"} {
							tx := finalResolverTarget(t, elm, rq, id)
							if i == len(tt.statuses) {
								if err := tx.Terminate(ctx, nil); err != nil {
									t.Fatalf("tx.Terminate() error = %v, want nil", err)
								}
								synctest.Wait()
								continue
							}
							res := newInternalInRes(t, tx.Request(), tt.statuses[i])
							var challenge sip.Header
							if tt.statuses[i] == 401 {
								var err error
								challenge, err = header.Parse(`WWW-Authenticate: Digest realm="A", nonce="first"`)
								if err != nil {
									t.Fatal(err)
								}
							}
							if tt.statuses[i] == 407 {
								var err error
								challenge, err = header.Parse(`Proxy-Authenticate: Digest realm="B", nonce="second"`)
								if err != nil {
									t.Fatal(err)
								}
							}
							res.WithMessage(func(msg *sip.Response) {
								to, _ := msg.Headers.To()
								to.Params.Set("tag", []string{"first", "second"}[i])
								msg.Headers.Append(&header.Any{Name: "X-Resolver-Choice", Value: id})
								if challenge != nil {
									msg.Headers.Append(challenge)
								}
							})
							if err := tx.RecvResponse(ctx, res); err != nil {
								t.Fatalf("tx.RecvResponse(%v) error = %v, want nil", tt.statuses[i], err)
							}
							synctest.Wait()
						}
						upstream := drainInternalRess(tp)
						if len(upstream) != 1 {
							t.Fatalf("upstream sends = %d, want 1", len(upstream))
						}
						if got := resToTag(upstream[0]); got != tt.wantTag {
							t.Fatalf("original final To tag = %q, want %q", got, tt.wantTag)
						}
						if got := choiceValue(upstream[0]); got != tt.wantChoice {
							t.Fatalf("original final X-Resolver-Choice = %q, want %q", got, tt.wantChoice)
						}
						assertChallenges(upstream[0], wantChallenges)
						if got := upstream[0].Status(); got != tt.wantStatus {
							t.Fatalf("original final status = %v, want %v", got, tt.wantStatus)
						}
						if got := originalCalls.Load(); got != 1 {
							t.Fatalf("original resolver calls = %d, want 1", got)
						}

						snap, err := rq.Snapshot()
						if err != nil {
							t.Fatalf("rq.Snapshot() error = %v, want nil", err)
						}
						before := make([]string, len(snap.Candidates))
						for i, candidate := range snap.Candidates {
							before[i] = candidate.Message().Render()
						}
						srvSnap, err := rq.srvTx.(sip.ServerTransactionSnapshotter).Snapshot()
						if err != nil {
							t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
						}
						raw, err := json.Marshal(snap)
						if err != nil {
							t.Fatalf("json.Marshal(snap) error = %v, want nil", err)
						}
						var decoded RequestSnapshot
						if err := json.Unmarshal(raw, &decoded); err != nil {
							t.Fatalf("json.Unmarshal(snapshot) error = %v, want nil", err)
						}
						if len(decoded.Candidates) != len(tt.statuses) {
							t.Fatalf("decoded candidates = %d, want %d", len(decoded.Candidates), len(tt.statuses))
						}
						for i, candidate := range decoded.Candidates {
							if got, want := resToTag(candidate), []string{"first", "second"}[i]; got != want {
								t.Fatalf("decoded candidate %d To tag = %q, want %q", i, got, want)
							}
							if got, want := choiceValue(candidate), []string{"A", "B"}[i]; got != want {
								t.Fatalf("decoded candidate %d X-Resolver-Choice = %q, want %q", i, got, want)
							}
							if got, want := candidate.Status(), tt.statuses[i]; got != want {
								t.Fatalf("decoded candidate %d status = %v, want %v", i, got, want)
							}
							var want []string
							if tt.wantStatus == 407 {
								want = wantChallenges[i : i+1]
							}
							assertChallenges(candidate, want)
						}
						if decoded.FinalCandidateIndex == nil || *decoded.FinalCandidateIndex != tt.index {
							t.Fatalf("decoded selection = %v, want index %d", decoded.FinalCandidateIndex, tt.index)
						}

						var calls, completions atomic.Int32
						route.ResolveFinalResponse = func(context.Context, FinalResponseSelection) (int, error) {
							calls.Add(1)
							if policy == "error if called" {
								return -1, errors.ErrorWrap("resolver must not run during recovery")
							}
							return 0, nil
						}
						elm2, prx2, tp2 := newInternalTestProxy(t, route, Options{ServedDomains: []string{"alice.voip.com"}})
						defer prx2.BindEventHandler(EventHandlerFunc(func(_ context.Context, ev Event) {
							if ev.Kind == EventKindRequestCompleted {
								completions.Add(1)
							}
						}))()
						srvTx, err := sip.RestoreServerTransaction(srvSnap, tp2)
						if err != nil {
							t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
						}
						restored, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), &decoded, RestoreBindings{
							Element:           elm2,
							ServerTransaction: srvTx,
						})
						if err != nil {
							t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
						}
						synctest.Wait()
						if restored.final == nil {
							t.Fatal("restored final = nil, want saved response")
						}
						if got := resToTag(restored.final); got != tt.wantTag {
							t.Fatalf("restored final To tag = %q, want %q", got, tt.wantTag)
						}
						if got := choiceValue(restored.final); got != tt.wantChoice {
							t.Fatalf("restored final X-Resolver-Choice = %q, want %q", got, tt.wantChoice)
						}
						assertChallenges(restored.final, wantChallenges)
						if restored.finalResIndex != tt.index {
							t.Fatalf("restored final index = %d, want %d", restored.finalResIndex, tt.index)
						}
						if got := calls.Load(); got != 0 {
							t.Fatalf("resolver calls during restore = %d, want 0", got)
						}
						if !restored.done || restored.outcome != OutcomeResponse || restored.final.Status() != tt.wantStatus {
							t.Fatalf("restored decision = (done=%t, %v, %v), want completed response %v",
								restored.done, restored.outcome, restored.final.Status(), tt.wantStatus)
						}
						if !restored.dormant {
							t.Fatal("restored coordinator is active, want dormant")
						}
						for _, phase := range []string{"restore", "Start"} {
							if phase == "Start" {
								if err := restored.Start(ctx); err != nil {
									t.Fatalf("restored.Start() error = %v, want nil", err)
								}
								synctest.Wait()
							}
							if got := len(drainInternalRess(tp2)) + len(drainInternalReqs(tp2)); got != 0 {
								t.Fatalf("sends after %s = %d, want 0", phase, got)
							}
							if got := completions.Load(); got != 0 {
								t.Fatalf("completion observations after %s = %d, want 0", phase, got)
							}
							if got := calls.Load(); got != 0 {
								t.Fatalf("resolver calls after %s = %d, want 0", phase, got)
							}
							for _, candidates := range [][]*sip.ResponseEnvelope{rq.finals, decoded.Candidates, restored.finals} {
								after := make([]string, len(candidates))
								for i, candidate := range candidates {
									after[i] = candidate.Message().Render()
								}
								if diff := cmp.Diff(before, after); diff != "" {
									t.Fatalf("stored candidates after %s (-before +after):\n%s", phase, diff)
								}
							}
						}
					})
				})
			}
		})
	}
}

// bindCountingSrvTransact wraps a borrowed upstream transaction and records the
// balance of handler binds the proxy staged on it, so a rejected or
// rolled-back restore can be checked for leaked subscriptions.
type bindCountingSrvTransact struct {
	*sip.InviteServerTransaction
	binds   atomic.Int32
	unbinds atomic.Int32
}

func (tx *bindCountingSrvTransact) BindStateHandler(fn sip.TransactionStateHandler) func() {
	tx.binds.Add(1)
	inner := tx.InviteServerTransaction.BindStateHandler(fn)
	return func() { tx.unbinds.Add(1); inner() }
}

func (tx *bindCountingSrvTransact) BindErrorHandler(fn sip.ErrorHandler) func() {
	tx.binds.Add(1)
	inner := tx.InviteServerTransaction.BindErrorHandler(fn)
	return func() { tx.unbinds.Add(1); inner() }
}

// bindBalance reports staged binds lacking a matching unbind.
func (tx *bindCountingSrvTransact) bindBalance() int32 { return tx.binds.Load() - tx.unbinds.Load() }

// bindCountingClnTransact is the downstream counterpart of [bindCountingSrvTransact].
type bindCountingClnTransact struct {
	*sip.InviteClientTransaction
	binds   atomic.Int32
	unbinds atomic.Int32
}

func (tx *bindCountingClnTransact) BindStateHandler(fn sip.TransactionStateHandler) func() {
	tx.binds.Add(1)
	inner := tx.InviteClientTransaction.BindStateHandler(fn)
	return func() { tx.unbinds.Add(1); inner() }
}

func (tx *bindCountingClnTransact) BindErrorHandler(fn sip.ErrorHandler) func() {
	tx.binds.Add(1)
	inner := tx.InviteClientTransaction.BindErrorHandler(fn)
	return func() { tx.unbinds.Add(1); inner() }
}

func (tx *bindCountingClnTransact) BindResponseHandler(fn sip.InboundResponseHandler) func() {
	tx.binds.Add(1)
	inner := tx.InviteClientTransaction.BindResponseHandler(fn)
	return func() { tx.unbinds.Add(1); inner() }
}

// bindBalance reports staged binds lacking a matching unbind.
func (tx *bindCountingClnTransact) bindBalance() int32 { return tx.binds.Load() - tx.unbinds.Load() }

type bindCountingNISrvTransact struct {
	*sip.NonInviteServerTransaction
	binds   atomic.Int32
	unbinds atomic.Int32
}

func (tx *bindCountingNISrvTransact) BindStateHandler(fn sip.TransactionStateHandler) func() {
	tx.binds.Add(1)
	inner := tx.NonInviteServerTransaction.BindStateHandler(fn)
	return func() { tx.unbinds.Add(1); inner() }
}

func (tx *bindCountingNISrvTransact) BindErrorHandler(fn sip.ErrorHandler) func() {
	tx.binds.Add(1)
	inner := tx.NonInviteServerTransaction.BindErrorHandler(fn)
	return func() { tx.unbinds.Add(1); inner() }
}

type bindCountingNIClnTransact struct {
	*sip.NonInviteClientTransaction
	binds   atomic.Int32
	unbinds atomic.Int32
}

func (tx *bindCountingNIClnTransact) BindStateHandler(fn sip.TransactionStateHandler) func() {
	tx.binds.Add(1)
	inner := tx.NonInviteClientTransaction.BindStateHandler(fn)
	return func() { tx.unbinds.Add(1); inner() }
}

func (tx *bindCountingNIClnTransact) BindErrorHandler(fn sip.ErrorHandler) func() {
	tx.binds.Add(1)
	inner := tx.NonInviteClientTransaction.BindErrorHandler(fn)
	return func() { tx.unbinds.Add(1); inner() }
}

func (tx *bindCountingNIClnTransact) BindResponseHandler(fn sip.InboundResponseHandler) func() {
	tx.binds.Add(1)
	inner := tx.NonInviteClientTransaction.BindResponseHandler(fn)
	return func() { tx.unbinds.Add(1); inner() }
}

type finalRestoreSpec struct {
	method      sip.RequestMethod
	statuses    []sip.ResponseStatus
	index       int
	status      sip.ResponseStatus
	nilResolver bool
	unfinished  bool
	abandoned   bool
}

type finalRestoreFixture struct {
	initialSnap    *RequestSnapshot
	initialSrvSnap *sip.ServerTransactionSnapshot
	snap           *RequestSnapshot
	srvSnap        *sip.ServerTransactionSnapshot
	clnSnaps       map[sip.ClientTransactionKey]*sip.ClientTransactionSnapshot
}

func newFinalRestoreFixture(tb testing.TB, spec finalRestoreSpec) *finalRestoreFixture {
	tb.Helper()

	var calls atomic.Int32
	route := Route{
		Name:        "final-restore-validation",
		ForwardMode: ForwardModeStateful,
		ResolveFinalResponse: func(context.Context, FinalResponseSelection) (int, error) {
			calls.Add(1)
			if spec.abandoned {
				return -1, errors.ErrorWrap("capture resolver abandonment")
			}
			return spec.index, nil
		},
	}
	if spec.nilResolver {
		route.ResolveFinalResponse = nil
	}
	route.ServerTransactionOptions.NonInviteServerTransactionBehavior = sip.NonInviteServerTransactionBehaviorRFC4320
	req := newInternalInviteReq(sip.MagicCookie + ".final-restore-validation")
	req.WithMessage(func(msg *sip.Request) {
		msg.Method = spec.method
		cseq, _ := msg.Headers.CSeq()
		cseq.Method = spec.method
	})
	elm, _, rq := admitFinalResolverFork(tb, route, req)
	initialSnap, err := rq.Snapshot()
	if err != nil {
		tb.Fatalf("initial rq.Snapshot() error = %v, want nil", err)
	}
	initialSrvSnap, err := rq.srvTx.(sip.ServerTransactionSnapshotter).Snapshot()
	if err != nil {
		tb.Fatalf("initial srvTx.Snapshot() error = %v, want nil", err)
	}
	txs := []sip.ClientTransaction{
		finalResolverTarget(tb, elm, rq, "A"),
		finalResolverTarget(tb, elm, rq, "B"),
	}
	for i, tx := range txs {
		if i >= len(spec.statuses) {
			if !spec.unfinished {
				if err := tx.Terminate(tb.Context(), nil); err != nil {
					tb.Fatalf("tx.Terminate() error = %v, want nil", err)
				}
				synctest.Wait()
			}
			continue
		}
		res := newInternalInRes(tb, tx.Request(), spec.statuses[i])
		res.WithMessage(func(msg *sip.Response) {
			to, _ := msg.Headers.To()
			to.Params.Set("tag", []string{"first", "second"}[i])
		})
		if err := tx.RecvResponse(tb.Context(), res); err != nil {
			tb.Fatalf("tx.RecvResponse(%v) error = %v, want nil", spec.statuses[i], err)
		}
		synctest.Wait()
	}
	snap, err := rq.Snapshot()
	if err != nil {
		tb.Fatalf("rq.Snapshot() error = %v, want nil", err)
	}
	statuses := make([]sip.ResponseStatus, 0, len(snap.Candidates))
	for _, candidate := range snap.Candidates {
		statuses = append(statuses, candidate.Status())
	}
	if diff := cmp.Diff(spec.statuses, statuses); diff != "" {
		tb.Fatalf("captured candidates (-want +got):\n%s", diff)
	}
	if spec.unfinished {
		for _, ts := range snap.TargetStates {
			if !ts.Started || ts.Completed != (ts.TargetID == "A") {
				tb.Fatalf("unfinished fork target = %+v, want A settled and B pending", ts)
			}
		}
	}
	wantOutcome := OutcomeResponse
	if spec.abandoned {
		wantOutcome = OutcomeAbandoned
	}
	if snap.Completed != !spec.unfinished || snap.CompletionCommitted != !spec.unfinished ||
		snap.Outcome != wantOutcome || snap.FinalStatus != spec.status {
		tb.Fatalf("captured decision = (completed=%t, observed=%t, %v, %v), want (%t, %t, %v, %v)",
			snap.Completed, snap.CompletionCommitted, snap.Outcome, snap.FinalStatus,
			!spec.unfinished, !spec.unfinished, wantOutcome, spec.status)
	}
	if spec.unfinished || spec.abandoned {
		if snap.FinalCandidateIndex != nil {
			tb.Fatalf("captured selection = %v, want nil", snap.FinalCandidateIndex)
		}
	} else if snap.FinalCandidateIndex == nil || *snap.FinalCandidateIndex != spec.index {
		tb.Fatalf("captured selection = %v, want index %d", snap.FinalCandidateIndex, spec.index)
	}
	wantCalls := int32(1)
	if spec.nilResolver || spec.unfinished || spec.status.IsSuccessful() {
		wantCalls = 0
	}
	if got := calls.Load(); got != wantCalls {
		tb.Fatalf("capture resolver calls = %d, want %d", got, wantCalls)
	}
	if spec.method == sip.RequestMethodInfo &&
		snap.NonInviteServerTransactionBehavior != sip.NonInviteServerTransactionBehaviorRFC4320 {
		tb.Fatal("INFO capture lost the actual RFC4320 behavior")
	}
	srvSnap, err := rq.srvTx.(sip.ServerTransactionSnapshotter).Snapshot()
	if err != nil {
		tb.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
	}
	f := &finalRestoreFixture{
		initialSnap: initialSnap, initialSrvSnap: initialSrvSnap,
		snap: snap, srvSnap: srvSnap,
		clnSnaps: make(map[sip.ClientTransactionKey]*sip.ClientTransactionSnapshot),
	}
	for _, att := range snap.Attempts {
		for _, tx := range txs {
			if !tx.Key().Equal(att.Key) {
				continue
			}
			clnSnap, err := tx.(sip.ClientTransactionSnapshotter).Snapshot()
			if err != nil {
				tb.Fatalf("clnTx.Snapshot() error = %v, want nil", err)
			}
			f.clnSnaps[att.Key] = clnSnap
		}
	}
	if len(f.clnSnaps) != len(snap.Attempts) {
		tb.Fatalf("captured transactions = %d, want %d retained attempts", len(f.clnSnaps), len(snap.Attempts))
	}
	return f
}

type restoreBindingProbe struct {
	tx      sip.Transaction
	binds   *atomic.Int32
	unbinds *atomic.Int32
}

type restoreBindingState struct {
	State   sip.TransactionState
	Started bool
	Binds   int32
	Unbinds int32
	Balance int32
}

func (p restoreBindingProbe) state() restoreBindingState {
	binds, unbinds := p.binds.Load(), p.unbinds.Load()
	return restoreBindingState{p.tx.State(), p.tx.Started(), binds, unbinds, binds - unbinds}
}

func (f *finalRestoreFixture) dormantBindings(
	tb testing.TB,
	elm *sip.Element,
	tp *internalTransp,
) (RestoreBindings, []restoreBindingProbe) {
	tb.Helper()

	bindings := RestoreBindings{
		Element: elm, ClientTransactions: make(map[sip.ClientTransactionKey]sip.ClientTransaction),
	}
	var probes []restoreBindingProbe
	if f.srvSnap.Type == sip.TransactionTypeServerInvite {
		tx, err := sip.RestoreInviteServerTransaction(f.srvSnap, tp)
		if err != nil {
			tb.Fatalf("sip.RestoreInviteServerTransaction() error = %v, want nil", err)
		}
		srv := &bindCountingSrvTransact{InviteServerTransaction: tx}
		bindings.ServerTransaction = srv
		probes = append(probes, restoreBindingProbe{srv, &srv.binds, &srv.unbinds})
	} else {
		tx, err := sip.RestoreNonInviteServerTransaction(f.srvSnap, tp)
		if err != nil {
			tb.Fatalf("sip.RestoreNonInviteServerTransaction() error = %v, want nil", err)
		}
		srv := &bindCountingNISrvTransact{NonInviteServerTransaction: tx}
		bindings.ServerTransaction = srv
		probes = append(probes, restoreBindingProbe{srv, &srv.binds, &srv.unbinds})
	}
	for _, att := range f.snap.Attempts {
		snap := f.clnSnaps[att.Key]
		if snap.Type == sip.TransactionTypeClientInvite {
			tx, err := sip.RestoreInviteClientTransaction(snap, tp)
			if err != nil {
				tb.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
			}
			cln := &bindCountingClnTransact{InviteClientTransaction: tx}
			bindings.ClientTransactions[att.Key] = cln
			probes = append(probes, restoreBindingProbe{cln, &cln.binds, &cln.unbinds})
		} else {
			tx, err := sip.RestoreNonInviteClientTransaction(snap, tp)
			if err != nil {
				tb.Fatalf("sip.RestoreNonInviteClientTransaction() error = %v, want nil", err)
			}
			cln := &bindCountingNIClnTransact{NonInviteClientTransaction: tx}
			bindings.ClientTransactions[att.Key] = cln
			probes = append(probes, restoreBindingProbe{cln, &cln.binds, &cln.unbinds})
		}
	}
	return bindings, probes
}

type finalRestoreRejectionProbe struct {
	prx       *Proxy
	elm       *sip.Element
	tp        *internalTransp
	key       sip.ServerTransactionKey
	probes    []restoreBindingProbe
	calls     *atomic.Int32
	events    *atomic.Int32
	snapshots *atomic.Int32
}

func (p finalRestoreRejectionProbe) assertRejected(
	tb testing.TB,
	invalid *RequestSnapshot,
	bindings RestoreBindings,
	cause error,
	reason string,
) {
	tb.Helper()

	before := make([]restoreBindingState, len(p.probes))
	for i, probe := range p.probes {
		before[i] = probe.state()
		if before[i].Started {
			tb.Fatal("binding already started before invalid restore")
		}
	}
	reqs, ress := len(p.tp.sendReqCalls), len(p.tp.sendResCalls)
	beforeCalls, beforeEvents, beforeSnapshots := p.calls.Load(), p.events.Load(), p.snapshots.Load()
	lookup, visible := p.prx.LookupRequest(p.elm, p.key)
	scope := p.prx.getElemState(p.elm, false)
	if lookup != nil || visible || scope != nil {
		tb.Fatal("restore element already has a scope or upstream association")
	}
	restored, err := p.prx.RestoreRequest(tb.Context(), invalid, bindings)
	if restored != nil || !errors.Is(err, ErrInvalidSnapshot) {
		tb.Fatalf("RestoreRequest() = (%v, %v), want nil and ErrInvalidSnapshot", restored, err)
	}
	if !IsError(err) {
		tb.Fatalf("RestoreRequest() error = %v, want proxy classification", err)
	}
	if cause != nil && !errors.Is(err, cause) {
		tb.Fatalf("RestoreRequest() error = %v, want preserved %v", err, cause)
	}
	if !strings.Contains(err.Error(), reason) {
		tb.Fatalf("RestoreRequest() error = %v, want reason %q", err, reason)
	}
	synctest.Wait()
	for i, probe := range p.probes {
		if diff := cmp.Diff(before[i], probe.state()); diff != "" {
			tb.Fatalf("binding %d changed after rejection (-before +after):\n%s", i, diff)
		}
	}
	if got, ok := p.prx.LookupRequest(p.elm, p.key); got != lookup || ok != visible {
		tb.Fatal("rejection changed upstream lookup visibility")
	}
	if got := p.prx.getElemState(p.elm, false); got != scope {
		tb.Fatal("rejection allocated an element scope, indexes, or coordinator timers")
	}
	if len(p.tp.sendReqCalls) != reqs || len(p.tp.sendResCalls) != ress ||
		p.calls.Load() != beforeCalls || p.events.Load() != beforeEvents || p.snapshots.Load() != beforeSnapshots {
		tb.Fatalf("rejection effects = (requests=%d, responses=%d, policy=%d, events=%d, snapshots=%d), want (%d, %d, %d, %d, %d)",
			len(p.tp.sendReqCalls), len(p.tp.sendResCalls), p.calls.Load(), p.events.Load(), p.snapshots.Load(),
			reqs, ress, beforeCalls, beforeEvents, beforeSnapshots)
	}
}

func assertNoFinalCandidateIndex(tb testing.TB, snap *RequestSnapshot) {
	tb.Helper()

	if snap.FinalCandidateIndex != nil {
		tb.Fatalf("snapshot selection = %v, want nil", snap.FinalCandidateIndex)
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		tb.Fatalf("json.Marshal() error = %v, want nil", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		tb.Fatalf("json.Unmarshal() error = %v, want nil", err)
	}
	if _, ok := fields["final_candidate_index"]; ok {
		tb.Fatal("snapshot JSON contains final_candidate_index without a response decision")
	}
}

func TestRequestSnapshot_FinalCandidateIndexAbsent(t *testing.T) {
	t.Parallel()

	t.Run("unfinished and resolver abandoned", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			unfinished := newFinalRestoreFixture(t, finalRestoreSpec{
				method: sip.RequestMethodInvite, statuses: []sip.ResponseStatus{480}, unfinished: true,
			})
			assertNoFinalCandidateIndex(t, unfinished.snap)
			abandoned := newFinalRestoreFixture(t, finalRestoreSpec{
				method: sip.RequestMethodInvite, statuses: []sip.ResponseStatus{480, 486}, abandoned: true,
			})
			assertNoFinalCandidateIndex(t, abandoned.snap)
		})
	})
	t.Run("timeout", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			elm, tp, rq := admitFinalResolverFork(t, Route{Name: "index-absent-timeout"},
				newInternalInviteReq(sip.MagicCookie+".index-absent-timeout"))
			for _, id := range []string{"A", "B"} {
				tx := finalResolverTarget(t, elm, rq, id)
				if err := tx.Terminate(t.Context(), sip.NewTransactionTimedOutError()); err != nil {
					t.Fatalf("tx.Terminate() error = %v, want nil", err)
				}
				synctest.Wait()
			}
			snap, err := rq.Snapshot()
			if err != nil {
				t.Fatalf("rq.Snapshot() error = %v, want nil", err)
			}
			if !snap.Completed || snap.Outcome != OutcomeTimeout || len(snap.Candidates) != 0 {
				t.Fatalf("timeout checkpoint = %+v, want completed timeout without candidates", snap)
			}
			if diff := cmp.Diff([]int{408}, resStatusList(drainInternalRess(tp))); diff != "" {
				t.Fatalf("timeout response (-want +got):\n%s", diff)
			}
			assertNoFinalCandidateIndex(t, snap)
		})
	})
}

func TestProxy_RestoreLiveCancellationFinalCandidateIndex(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		route := Route{
			Name: "restore-live-cancel",
			ResolveFinalResponse: func(context.Context, FinalResponseSelection) (int, error) {
				calls.Add(1)
				return -1, errors.ErrorWrap("no-candidate cancellation must not call policy")
			},
		}
		req := newInternalInviteReq(sip.MagicCookie + ".restore-live-cancel")
		elm, tp, rq := admitFinalResolverFork(t, route, req)
		prx := rq.prx
		drainInternalReqs(tp)
		cancel, err := sip.NewCancelRequestEnvelope(req)
		if err != nil {
			t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
		}
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(t.Context(), elm),
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil }), cancel); err != nil {
			t.Fatalf("prx.InterceptInboundRequest(CANCEL) error = %v, want nil", err)
		}
		synctest.Wait()
		if diff := cmp.Diff([]int{200}, resStatusList(drainInternalRess(tp))); diff != "" {
			t.Fatalf("accepted CANCEL acknowledgement (-want +got):\n%s", diff)
		}
		if len(drainInternalReqs(tp)) != 0 {
			t.Fatal("Calling branches sent CANCEL before a provisional")
		}
		live, err := rq.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		if live.Completed || !live.Cancelled || len(live.Candidates) != 0 {
			t.Fatalf("live cancellation = %+v, want unfinished accepted cancellation without candidates", live)
		}
		assertNoFinalCandidateIndex(t, live)
		for _, st := range live.TargetStates {
			if !st.Started || st.Completed || !st.Cancelling || st.TimerCDeadline.IsZero() {
				t.Fatalf("live target = %+v, want active cancelling with Timer C", st)
			}
		}
		srvSnap, err := rq.srvTx.(sip.ServerTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
		}
		f := &finalRestoreFixture{
			snap: live, srvSnap: srvSnap,
			clnSnaps: make(map[sip.ClientTransactionKey]*sip.ClientTransactionSnapshot),
		}
		for _, att := range live.Attempts {
			if att.Completed || att.Cancelled || !att.DeferredCancel ||
				att.State != sip.TransactionStateCalling || att.TimerCDeadline.IsZero() {
				t.Fatalf("live attempt = %+v, want Calling with deferred CANCEL and Timer C", att)
			}
			tx := finalResolverTarget(t, elm, rq, att.TargetID)
			f.clnSnaps[att.Key], err = tx.(sip.ClientTransactionSnapshotter).Snapshot()
			if err != nil {
				t.Fatalf("clnTx.Snapshot() error = %v, want nil", err)
			}
		}
		elm2, prx2, tp2 := newInternalTestProxy(t, route, Options{ServedDomains: []string{"alice.voip.com"}})
		var completions atomic.Int32
		defer prx2.BindEventHandler(EventHandlerFunc(func(_ context.Context, ev Event) {
			if ev.Kind == EventKindRequestCompleted && ev.RequestID == live.RequestID {
				completions.Add(1)
			}
		}))()
		bindings, probes := f.dormantBindings(t, elm2, tp2)
		restored, err := prx2.RestoreRequest(t.Context(), live, bindings)
		if err != nil {
			t.Fatalf("prx2.RestoreRequest() error = %v, want nil", err)
		}
		synctest.Wait()
		for _, probe := range probes {
			if probe.tx.Started() {
				t.Fatal("dormant cancellation binding already started")
			}
		}
		if calls.Load() != 0 || completions.Load() != 0 ||
			len(drainInternalReqs(tp2))+len(drainInternalRess(tp2)) != 0 {
			t.Fatal("dormant cancellation replayed policy, observations, or traffic")
		}
		if err := restored.Start(t.Context()); err != nil {
			t.Fatalf("restored.Start() error = %v, want nil", err)
		}
		synctest.Wait()
		active, err := restored.Snapshot()
		if err != nil {
			t.Fatalf("restored.Snapshot() error = %v, want nil", err)
		}
		if diff := cmp.Diff(live.TargetStates, active.TargetStates,
			cmpopts.EquateComparable(sip.ResolvedAddr{}, netip.AddrPort{})); diff != "" {
			t.Fatalf("activated cancellation targets (-want +got):\n%s", diff)
		}
		assertNoFinalCandidateIndex(t, active)
		for _, att := range live.Attempts {
			deadline, armed := tmrCDeadline(t, restored, att.Key)
			if !armed || !deadline.Equal(att.TimerCDeadline) {
				t.Fatalf("activated Timer C = (%v, %t), want preserved (%v, true)", deadline, armed, att.TimerCDeadline)
			}
		}
		if len(drainInternalReqs(tp2))+len(drainInternalRess(tp2)) != 0 {
			t.Fatal("activation replayed cancellation while branches were Calling")
		}
		var txA sip.ClientTransaction
		for _, att := range live.Attempts {
			if att.TargetID == "A" {
				txA = bindings.ClientTransactions[att.Key]
			}
		}
		if txA == nil {
			t.Fatal("missing restored A transaction")
		}
		ctx := sip.ContextWithElement(t.Context(), elm2)
		for i := range 2 {
			if err := txA.RecvResponse(ctx, newInternalInRes(t, txA.Request(), 180)); err != nil {
				t.Fatalf("txA.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()
			sends := drainInternalReqs(tp2)
			wantSends := 1
			if i == 1 {
				wantSends = 0
			}
			if len(sends) != wantSends {
				t.Fatalf("provisional %d downstream sends = %d, want %d", i, len(sends), wantSends)
			}
			if i == 0 {
				if !sends[0].Method().Equal(sip.RequestMethodCancel) {
					t.Fatalf("downstream method = %v, want CANCEL", sends[0].Method())
				}
				key, err := sip.MakeClientTransactionKey(sends[0])
				if err != nil || key.Branch != txA.Key().Branch {
					t.Fatalf("downstream CANCEL key = (%v, %v), want A branch %q", key, err, txA.Key().Branch)
				}
			}
			drainInternalRess(tp2)
		}
		pending, err := restored.Snapshot()
		if err != nil {
			t.Fatalf("restored.Snapshot() error = %v, want nil", err)
		}
		if pending.Completed || completions.Load() != 0 {
			t.Fatal("deferred CANCEL settled the request before branch exhaustion")
		}
		assertNoFinalCandidateIndex(t, pending)
		for _, att := range pending.Attempts {
			deadline, armed := tmrCDeadline(t, restored, att.Key)
			if att.Completed || !armed || deadline.IsZero() || !deadline.Equal(att.TimerCDeadline) ||
				att.Cancelled != (att.TargetID == "A") || att.DeferredCancel != (att.TargetID == "B") {
				t.Fatalf("post-provisional attempt = %+v, Timer C (%v, %t), want live bounded cancellation", att, deadline, armed)
			}
		}
		for _, tx := range bindings.ClientTransactions {
			if err := tx.Terminate(ctx, sip.NewTransactionTimedOutError()); err != nil {
				t.Fatalf("tx.Terminate() error = %v, want nil", err)
			}
			synctest.Wait()
		}
		completed, err := restored.Snapshot()
		if err != nil {
			t.Fatalf("restored.Snapshot() error = %v, want nil", err)
		}
		if !completed.Completed || !completed.CompletionCommitted || !completed.Cancelled ||
			completed.Outcome != OutcomeCancelled || len(completed.Candidates) != 0 {
			t.Fatalf("completed cancellation = %+v, want no-candidate cancellation", completed)
		}
		assertNoFinalCandidateIndex(t, completed)
		if diff := cmp.Diff([]int{487}, resStatusList(drainInternalRess(tp2))); diff != "" {
			t.Fatalf("cancellation final (-want +got):\n%s", diff)
		}
		if calls.Load() != 0 || completions.Load() != 1 {
			t.Fatalf("policy/completion calls = (%d, %d), want (0, 1)", calls.Load(), completions.Load())
		}
	})
}

func TestProxy_FinalResponseResolutionResume(t *testing.T) {
	t.Parallel()

	for _, allSettled := range []bool{false, true} {
		name := "remaining work"
		if allSettled {
			name = "all settled pre-policy checkpoint"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				spec := finalRestoreSpec{
					method: sip.RequestMethodInvite, statuses: []sip.ResponseStatus{480}, unfinished: true,
				}
				if allSettled {
					spec.statuses, spec.unfinished, spec.index, spec.status = []sip.ResponseStatus{480, 486}, false, 1, 486
				}
				f := newFinalRestoreFixture(t, spec)
				if allSettled {
					// Assemble a pre-policy format checkpoint from real admission
					// and settled branch facts, not a captured pending callback.
					settled := f.snap.Clone()
					f.snap = f.initialSnap.Clone()
					f.snap.TargetStates, f.snap.Attempts, f.snap.Candidates =
						settled.TargetStates, settled.Attempts, settled.Candidates
					f.srvSnap = f.initialSrvSnap
					if f.srvSnap.State != sip.TransactionStateProceeding || f.snap.Completed ||
						f.snap.CompletionCommitted || f.snap.FinalForwarded || f.snap.FinalStatus != 0 ||
						f.snap.FinalCandidateIndex != nil {
						t.Fatal("pre-policy checkpoint retained a historical wire decision")
					}
				}
				wantRequest := f.snap.OriginalRequest.Render()
				var owner *sip.Element
				var calls, completions atomic.Int32
				route := Route{
					Name: f.snap.RouteName,
					ResolveFinalResponse: func(ctx context.Context, selection FinalResponseSelection) (int, error) {
						calls.Add(1)
						if got, ok := sip.ElementFromContext(ctx); !ok || got != owner {
							t.Errorf("resolver element = (%p, %t), want bound replacement %p", got, ok, owner)
						}
						if diff := cmp.Diff(wantRequest, selection.Request.Render()); diff != "" {
							t.Errorf("resolver original request (-want +got):\n%s", diff)
						}
						if diff := cmp.Diff([]int{480, 486}, resStatusList(selection.Candidates)); diff != "" {
							t.Errorf("resolver candidate order (-want +got):\n%s", diff)
						}
						if allSettled {
							return 0, nil
						}
						return 1, nil
					},
				}
				elm, prx, tp := newInternalTestProxy(t, route, Options{ServedDomains: []string{"alice.voip.com"}})
				owner = elm
				defer prx.BindEventHandler(EventHandlerFunc(func(_ context.Context, ev Event) {
					if ev.Kind == EventKindRequestCompleted && ev.RequestID == f.snap.RequestID {
						completions.Add(1)
					}
				}))()
				bindings, probes := f.dormantBindings(t, elm, tp)
				callerCtx := sip.ContextWithElement(t.Context(), &sip.Element{})
				restored, err := prx.RestoreRequest(callerCtx, f.snap, bindings)
				if err != nil {
					t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
				}
				synctest.Wait()
				if calls.Load() != 0 || completions.Load() != 0 ||
					len(drainInternalReqs(tp))+len(drainInternalRess(tp)) != 0 {
					t.Fatal("dormant restore executed policy, observations, or traffic")
				}
				for _, probe := range probes {
					if probe.tx.Started() {
						t.Fatal("dormant binding started before activation")
					}
				}
				if err := restored.Start(callerCtx); err != nil {
					t.Fatalf("restored.Start() error = %v, want nil", err)
				}
				synctest.Wait()
				wantIndex, wantStatus, wantTag := 0, sip.ResponseStatus(480), "first"
				if !allSettled {
					if calls.Load() != 0 || completions.Load() != 0 ||
						len(drainInternalReqs(tp))+len(drainInternalRess(tp)) != 0 {
						t.Fatal("activation resolved or replayed while B was pending")
					}
					var txB sip.ClientTransaction
					for _, att := range f.snap.Attempts {
						if att.TargetID == "B" {
							txB = bindings.ClientTransactions[att.Key]
						}
					}
					if txB == nil {
						t.Fatal("missing real restored B transaction")
					}
					res := newInternalInRes(t, txB.Request(), 486)
					res.WithMessage(func(msg *sip.Response) {
						to, _ := msg.Headers.To()
						to.Params.Set("tag", "second")
					})
					if err := txB.RecvResponse(callerCtx, res); err != nil {
						t.Fatalf("txB.RecvResponse(486) error = %v, want nil", err)
					}
					synctest.Wait()
					wantIndex, wantStatus, wantTag = 1, 486, "second"
				}
				if calls.Load() != 1 || completions.Load() != 1 {
					t.Fatalf("policy/completion calls = (%d, %d), want (1, 1)", calls.Load(), completions.Load())
				}
				sent := drainInternalRess(tp)
				if len(sent) != 1 || sent[0].Status() != wantStatus || resToTag(sent[0]) != wantTag {
					t.Fatalf("upstream finals = %v, want one %d with tag %q", resStatusList(sent), wantStatus, wantTag)
				}
				committed, err := restored.Snapshot()
				if err != nil {
					t.Fatalf("restored.Snapshot() error = %v, want nil", err)
				}
				if !committed.Completed || !committed.CompletionCommitted ||
					committed.Outcome != OutcomeResponse || committed.FinalStatus != wantStatus ||
					committed.FinalCandidateIndex == nil || *committed.FinalCandidateIndex != wantIndex {
					t.Fatalf("resumed decision = %+v, want committed %d/index %d", committed, wantStatus, wantIndex)
				}
				if got := resToTag(committed.Candidates[wantIndex]); got != wantTag {
					t.Fatalf("selected source tag = %q, want %q", got, wantTag)
				}
			})
		})
	}
}

func TestProxy_FinalResponseResolutionLateSuccessRecovery(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		entered, gate := make(chan struct{}), make(chan struct{})
		release := sync.OnceFunc(func() { close(gate) })
		defer release()
		signal := sync.OnceFunc(func() { close(entered) })
		var calls, completions atomic.Int32
		route := Route{
			Name: "late-success-recovery",
			ResolveFinalResponse: func(_ context.Context, selection FinalResponseSelection) (int, error) {
				calls.Add(1)
				if diff := cmp.Diff([]int{480, 486}, resStatusList(selection.Candidates)); diff != "" {
					t.Errorf("pending candidates (-want +got):\n%s", diff)
				}
				signal()
				<-gate
				return -1, errors.ErrorWrap("stale policy failure must not replace success")
			},
		}
		elm, tp, rq := admitFinalResolverFork(t, route,
			newInternalInviteReq(sip.MagicCookie+".late-success-recovery"))
		prx := rq.prx
		defer prx.BindEventHandler(EventHandlerFunc(func(_ context.Context, ev Event) {
			if ev.Kind == EventKindRequestCompleted {
				completions.Add(1)
			}
		}))()
		for i, id := range []string{"A", "B"} {
			tx := finalResolverTarget(t, elm, rq, id)
			if err := tx.RecvResponse(t.Context(), newInternalInRes(t, tx.Request(), []sip.ResponseStatus{480, 486}[i])); err != nil {
				t.Fatalf("tx.RecvResponse() error = %v, want nil", err)
			}
			synctest.Wait()
		}
		select {
		case <-entered:
		default:
			t.Fatal("resolver did not enter")
		}
		if calls.Load() != 1 || completions.Load() != 0 || len(drainInternalRess(tp)) != 0 {
			t.Fatal("pending policy already completed or forwarded")
		}
		drainInternalReqs(tp)
		tx := finalResolverTarget(t, elm, rq, "A")
		success := newInternalInRes(t, tx.Request(), 200)
		success.WithMessage(func(msg *sip.Response) {
			to, _ := msg.Headers.To()
			to.Params.Set("tag", "late")
		})
		wantWire := cloneResEnvelope(success)
		wantWire.WithMessage(func(msg *sip.Response) { msg.Headers.PopFirstVia() })
		rq.mu.Lock()
		gen := rq.attempts[tx.Key()].gen
		rq.mu.Unlock()
		// The real transaction consumed its negative final already; use the
		// retained-attempt attribution seam also exercised by the coordinator.
		rq.enqueue(t.Context(), workEvent{kind: evtClnRes, key: tx.Key(), gen: gen, res: success})
		synctest.Wait()
		release()
		synctest.Wait()
		sent := drainInternalRess(tp)
		if len(sent) != 1 || sent[0].Status() != 200 || resToTag(sent[0]) != "late" {
			t.Fatalf("upstream finals = %v, want only original late 200", resStatusList(sent))
		}
		if diff := cmp.Diff(wantWire.Render(), sent[0].Render()); diff != "" {
			t.Fatalf("late success content (-want +got):\n%s", diff)
		}
		if calls.Load() != 1 || completions.Load() != 1 {
			t.Fatalf("policy/completion calls = (%d, %d), want (1, 1)", calls.Load(), completions.Load())
		}
		snap, err := rq.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		if !snap.Completed || !snap.CompletionCommitted ||
			snap.Outcome != OutcomeResponse || snap.FinalStatus != 200 ||
			snap.FinalCandidateIndex == nil || *snap.FinalCandidateIndex != 2 {
			t.Fatalf("late success capture = %+v, want committed 200/index 2", snap)
		}
		if diff := cmp.Diff([]int{480, 486, 200}, resStatusList(snap.Candidates)); diff != "" {
			t.Fatalf("retained candidate order (-want +got):\n%s", diff)
		}
		srvSnap, err := rq.srvTx.(sip.ServerTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
		}
		raw, err := json.Marshal(snap)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v, want nil", err)
		}
		var decoded RequestSnapshot
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("json.Unmarshal() error = %v, want nil", err)
		}
		var restoredCalls, restoredCompletions atomic.Int32
		route.ResolveFinalResponse = func(context.Context, FinalResponseSelection) (int, error) {
			restoredCalls.Add(1)
			return 0, errors.ErrorWrap("committed success must not invoke changed policy")
		}
		elm2, prx2, tp2 := newInternalTestProxy(t, route, Options{ServedDomains: []string{"alice.voip.com"}})
		defer prx2.BindEventHandler(EventHandlerFunc(func(_ context.Context, ev Event) {
			if ev.Kind == EventKindRequestCompleted {
				restoredCompletions.Add(1)
			}
		}))()
		srv, err := sip.RestoreServerTransaction(srvSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		restored, err := prx2.RestoreRequest(t.Context(), &decoded, RestoreBindings{Element: elm2, ServerTransaction: srv})
		if err != nil {
			t.Fatalf("prx2.RestoreRequest() error = %v, want nil", err)
		}
		for _, phase := range []string{"dormant", "active"} {
			if phase == "active" {
				if err := restored.Start(t.Context()); err != nil {
					t.Fatalf("restored.Start() error = %v, want nil", err)
				}
			}
			synctest.Wait()
			if restoredCalls.Load() != 0 || restoredCompletions.Load() != 0 ||
				len(drainInternalReqs(tp2))+len(drainInternalRess(tp2)) != 0 {
				t.Fatalf("%s recovery replayed policy, observations, or traffic", phase)
			}
			got, err := restored.Snapshot()
			if err != nil {
				t.Fatalf("restored.Snapshot() error = %v, want nil", err)
			}
			if !got.Completed || got.Outcome != OutcomeResponse || got.FinalStatus != 200 ||
				got.FinalCandidateIndex == nil || *got.FinalCandidateIndex != 2 ||
				resToTag(got.Candidates[2]) != "late" {
				t.Fatalf("%s recovered decision = %+v, want late 200/index 2", phase, got)
			}
			if restored.final == nil || restored.final.Status() != 200 || resToTag(restored.final) != "late" {
				t.Fatalf("%s recovered final = %v, want late 200", phase, restored.final)
			}
			if diff := cmp.Diff(success.Render(), got.Candidates[2].Render()); diff != "" {
				t.Fatalf("%s recovered source content (-want +got):\n%s", phase, diff)
			}
		}
	})
}

func TestProxy_FinalResponseResolutionRestoreValidation(t *testing.T) {
	t.Parallel()

	setIndex := func(snap *RequestSnapshot, index int) { snap.FinalCandidateIndex = &index }
	setStatus := func(snap *RequestSnapshot, index int, status sip.ResponseStatus) {
		snap.Candidates[index].WithMessage(func(msg *sip.Response) { msg.Status = status })
	}
	const presenceReason = "final candidate index must be present exactly for a completed response outcome"
	for _, tt := range []struct {
		name      string
		base      string
		mutate    func(*RequestSnapshot)
		wantCause error
		reason    string
	}{
		{"missing index", "", func(s *RequestSnapshot) { s.FinalCandidateIndex = nil }, nil, presenceReason},
		{"negative index", "", func(s *RequestSnapshot) { setIndex(s, -1) }, errInvalidFinalResIndex, "-1 is not a final candidate"},
		{"index equals count", "", func(s *RequestSnapshot) { setIndex(s, 2) }, errInvalidFinalResIndex, "2 is not a final candidate"},
		{"selected nil", "", func(s *RequestSnapshot) { s.Candidates[1] = nil }, nil, "nil response context candidate 1"},
		{"selected provisional", "", func(s *RequestSnapshot) { setStatus(s, 1, 180) }, errInvalidFinalResIndex, "1 is not a final candidate"},
		{"selected INFO 408", "INFO", func(s *RequestSnapshot) { setStatus(s, 1, 408) }, errInvalidFinalResIndex, "1 selects a non-INVITE 408"},
		{"negative despite 200", "", func(s *RequestSnapshot) { setStatus(s, 0, 200) }, errInvalidFinalResIndex, "1 violates mandatory response precedence"},
		{"486 despite 600", "", func(s *RequestSnapshot) { setStatus(s, 0, 600) }, errInvalidFinalResIndex, "1 violates mandatory response precedence"},
		{"processed status mismatch", "", func(s *RequestSnapshot) { s.FinalStatus = 480 }, nil, "final status 480 Temporarily Unavailable does not match the response context"},
		{"index on unfinished", "unfinished", func(s *RequestSnapshot) { setIndex(s, 0) }, nil, presenceReason},
		{"index on abandoned", "abandoned", func(s *RequestSnapshot) { setIndex(s, 0) }, nil, presenceReason},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				spec := finalRestoreSpec{method: sip.RequestMethodInvite, statuses: []sip.ResponseStatus{480, 486}, index: 1, status: 486}
				switch tt.base {
				case "INFO":
					spec.method = sip.RequestMethodInfo
				case "unfinished":
					spec.statuses, spec.status, spec.unfinished = []sip.ResponseStatus{480}, 0, true
				case "abandoned":
					spec.status, spec.abandoned = 0, true
				}
				f := newFinalRestoreFixture(t, spec)
				var calls, events, snapshots atomic.Int32
				route := Route{
					Name: f.snap.RouteName, ForwardMode: ForwardModeStateful,
					ResolveFinalResponse: func(context.Context, FinalResponseSelection) (int, error) {
						calls.Add(1)
						return -1, errors.ErrorWrap("restore must not invoke resolver")
					},
				}
				elm, prx, tp := newInternalTestProxy(t, route, Options{ServedDomains: []string{"alice.voip.com"}})
				defer prx.BindEventHandler(EventHandlerFunc(func(context.Context, Event) { events.Add(1) }))()
				defer prx.BindSnapshotHandler(RequestSnapshotHandlerFunc(func(context.Context, *RequestSnapshot) { snapshots.Add(1) }))()
				bindings, probes := f.dormantBindings(t, elm, tp)
				invalid := f.snap.Clone()
				tt.mutate(invalid)

				beforeCalls, beforeEvents, beforeSnapshots := calls.Load(), events.Load(), snapshots.Load()
				probe := finalRestoreRejectionProbe{
					prx: prx, elm: elm, tp: tp, key: f.snap.UpstreamKey, probes: probes,
					calls: &calls, events: &events, snapshots: &snapshots,
				}
				probe.assertRejected(t, invalid, bindings, tt.wantCause, tt.reason)

				restored, err := prx.RestoreRequest(t.Context(), f.snap, bindings)
				if err != nil || restored == nil {
					t.Fatalf("RestoreRequest(valid, same bindings) = (%v, %v), want request and nil", restored, err)
				}
				synctest.Wait()
				assertFinalRestoreDormant(t, prx, elm, tp, restored, f.snap, bindings)
				if calls.Load() != beforeCalls || events.Load() != beforeEvents || snapshots.Load() != beforeSnapshots {
					t.Fatal("valid dormant reuse invoked policy or published observations")
				}
			})
		})
	}

	for _, tt := range []struct {
		name     string
		statuses []sip.ResponseStatus
		index    int
		status   sip.ResponseStatus
		tag      string
	}{
		{"486 over 302", []sip.ResponseStatus{302, 486}, 1, 486, "second"},
		{"404 over 401", []sip.ResponseStatus{401, 404}, 1, 404, "second"},
		{"second 6xx", []sip.ResponseStatus{604, 600}, 1, 600, "second"},
		{"core success over 6xx", []sip.ResponseStatus{600, 200}, 1, 200, "second"},
		{"lone 503 processed 500", []sip.ResponseStatus{503}, 0, 500, "first"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				f := newFinalRestoreFixture(t, finalRestoreSpec{
					method: sip.RequestMethodInvite, statuses: tt.statuses, index: tt.index, status: tt.status,
				})
				var calls, observations atomic.Int32
				route := Route{
					Name: f.snap.RouteName, ForwardMode: ForwardModeStateful,
					ResolveFinalResponse: func(context.Context, FinalResponseSelection) (int, error) {
						calls.Add(1)
						return -1, errors.ErrorWrap("poison restore resolver")
					},
				}
				elm, prx, tp := newInternalTestProxy(t, route, Options{ServedDomains: []string{"alice.voip.com"}})
				defer prx.BindEventHandler(EventHandlerFunc(func(context.Context, Event) { observations.Add(1) }))()
				defer prx.BindSnapshotHandler(RequestSnapshotHandlerFunc(func(context.Context, *RequestSnapshot) { observations.Add(1) }))()
				bindings, _ := f.dormantBindings(t, elm, tp)
				restored, err := prx.RestoreRequest(t.Context(), f.snap, bindings)
				if err != nil || restored == nil {
					t.Fatalf("RestoreRequest() = (%v, %v), want request and nil", restored, err)
				}
				synctest.Wait()
				assertFinalRestoreDormant(t, prx, elm, tp, restored, f.snap, bindings)
				if restored.finalResIndex != tt.index || restored.final == nil ||
					restored.final.Status() != tt.status || resToTag(restored.final) != tt.tag {
					t.Fatalf("restored final = (index=%d, %v), want index %d, status %v, tag %q",
						restored.finalResIndex, restored.final, tt.index, tt.status, tt.tag)
				}
				if calls.Load() != 0 || observations.Load() != 0 {
					t.Fatalf("dormant callbacks = (policy=%d, observations=%d), want (0, 0)", calls.Load(), observations.Load())
				}
			})
		})
	}
}

func TestProxy_FinalResponseSnapshotVersion(t *testing.T) {
	t.Parallel()

	const presenceReason = "final candidate index must be present exactly for a completed response outcome"
	for _, tt := range []struct {
		name        string
		nilResolver bool
		index       int
		tag         string
		mutate      func(*RequestSnapshot)
		reason      string
	}{
		{"nil hook missing index", true, 0, "first", func(s *RequestSnapshot) { s.FinalCandidateIndex = nil }, presenceReason},
		{"custom hook missing index", false, 1, "second", func(s *RequestSnapshot) { s.FinalCandidateIndex = nil }, presenceReason},
		{"version 0", false, 1, "second", func(s *RequestSnapshot) { s.Version = 0 }, "unsupported version 0"},
		{"version 2", false, 1, "second", func(s *RequestSnapshot) { s.Version = 2 }, "unsupported version 2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				f := newFinalRestoreFixture(t, finalRestoreSpec{
					method: sip.RequestMethodInvite, statuses: []sip.ResponseStatus{486, 486},
					index: tt.index, status: 486, nilResolver: tt.nilResolver,
				})
				if f.snap.Version != 1 {
					t.Fatalf("captured version = %d, want 1", f.snap.Version)
				}
				for i, candidate := range f.snap.Candidates {
					if got, want := resToTag(candidate), []string{"first", "second"}[i]; got != want {
						t.Fatalf("captured candidate %d To tag = %q, want %q", i, got, want)
					}
				}
				if got := resToTag(f.snap.Candidates[tt.index]); got != tt.tag {
					t.Fatalf("captured selected To tag = %q, want %q", got, tt.tag)
				}

				var calls, events, snapshots atomic.Int32
				route := Route{
					Name: f.snap.RouteName, ForwardMode: ForwardModeStateful,
					ResolveFinalResponse: func(context.Context, FinalResponseSelection) (int, error) {
						calls.Add(1)
						return -1, errors.ErrorWrap("restore must not infer a final response")
					},
				}
				elm, prx, tp := newInternalTestProxy(t, route, Options{ServedDomains: []string{"alice.voip.com"}})
				defer prx.BindEventHandler(EventHandlerFunc(func(context.Context, Event) { events.Add(1) }))()
				defer prx.BindSnapshotHandler(RequestSnapshotHandlerFunc(func(context.Context, *RequestSnapshot) { snapshots.Add(1) }))()
				bindings, probes := f.dormantBindings(t, elm, tp)
				invalid := f.snap.Clone()
				tt.mutate(invalid)

				probe := finalRestoreRejectionProbe{
					prx: prx, elm: elm, tp: tp, key: f.snap.UpstreamKey, probes: probes,
					calls: &calls, events: &events, snapshots: &snapshots,
				}
				probe.assertRejected(t, invalid, bindings, nil, tt.reason)

				restored, err := prx.RestoreRequest(t.Context(), f.snap, bindings)
				if err != nil || restored == nil {
					t.Fatalf("RestoreRequest(valid, same bindings) = (%v, %v), want request and nil", restored, err)
				}
				synctest.Wait()
				assertFinalRestoreDormant(t, prx, elm, tp, restored, f.snap, bindings)
				if restored.finalResIndex != tt.index || restored.final == nil ||
					restored.final.Status() != 486 || resToTag(restored.final) != tt.tag {
					t.Fatalf("restored final = (index=%d, %v), want index %d, status 486, tag %q",
						restored.finalResIndex, restored.final, tt.index, tt.tag)
				}
				if calls.Load() != 0 || events.Load() != 0 || snapshots.Load() != 0 {
					t.Fatalf("dormant callbacks = (policy=%d, events=%d, snapshots=%d), want (0, 0, 0)",
						calls.Load(), events.Load(), snapshots.Load())
				}
			})
		})
	}
}

func assertFinalRestoreDormant(
	tb testing.TB,
	prx *Proxy,
	elm *sip.Element,
	tp *internalTransp,
	rq *Request,
	snap *RequestSnapshot,
	bindings RestoreBindings,
) {
	tb.Helper()

	if !rq.dormant || rq.srvTx != bindings.ServerTransaction || rq.srvTx.Started() {
		tb.Fatal("restore did not retain the identical dormant server binding")
	}
	for key, tx := range bindings.ClientTransactions {
		if att := rq.attempts[key]; att == nil || att.tx != tx || tx.Started() {
			tb.Fatalf("restore did not retain identical dormant client binding %v", key)
		}
	}
	for id, st := range rq.targets {
		if st.tmrC != nil {
			tb.Fatalf("dormant target %q has an armed runtime Timer C", id)
		}
	}
	if got, ok := prx.LookupRequest(elm, snap.UpstreamKey); bindings.ServerTransaction.State() == sip.TransactionStateTerminated {
		if ok || got != nil {
			tb.Fatal("terminal upstream binding gained a lookup association")
		}
	} else if !ok || got != rq {
		tb.Fatal("valid restore is not indexed under its upstream key")
	}
	if got := len(tp.sendReqCalls) + len(tp.sendResCalls); got != 0 {
		tb.Fatalf("dormant restore sends = %d, want 0", got)
	}
}

func TestProxyRequestSnapshot_UnstableTransition(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		route := Route{
			Name:        "snap-unstable",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		elm, prx, _ := newInternalTestProxy(t, route, Options{
			ServedDomains: []string{"alice.voip.com"},
		})

		// A coordinator before the committed plan has no stable ownership
		// boundary to capture.
		rq := newReq(ctx, elm, prx, &route, newInternalInviteReq(sip.MagicCookie+".snap-unstable"), "snap-unstable")
		if _, err := rq.Snapshot(); !errors.Is(err, errSnapshotUnstable) {
			t.Fatalf("rq.Snapshot() error = %v, want %v", err, errSnapshotUnstable)
		}

		// Once admitted, capture works.
		rq2 := admitInternalReq(t, elm, prx, newInternalInviteReq(sip.MagicCookie+".snap-admitted"))
		synctest.Wait()
		if _, err := rq2.Snapshot(); err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}

		// A pending client-transaction admission is a partial branch
		// decision: capture and publication are refused while it is
		// unresolved.
		rq2.mu.Lock()
		rq2.pendingAdms[42] = &admission{targetID: "A", gen: 42}
		rq2.mu.Unlock()

		if _, err := rq2.Snapshot(); !errors.Is(err, errSnapshotUnstable) {
			t.Fatalf("rq.Snapshot() error = %v, want %v", err, errSnapshotUnstable)
		}

		var delivered atomic.Int32
		unbind := prx.BindSnapshotHandler(RequestSnapshotHandlerFunc(
			func(context.Context, *RequestSnapshot) { delivered.Add(1) },
		))
		rq2.enqueue(ctx, workEvent{kind: evtSrvErr, err: errors.ErrorWrap("test")})
		synctest.Wait()
		if got := delivered.Load(); got != 0 {
			t.Fatalf("snapshot revisions published during an unstable transition=%d, want 0", got)
		}
		unbind()

		rq2.mu.Lock()
		delete(rq2.pendingAdms, 42)
		rq2.mu.Unlock()

		if _, err := rq2.Snapshot(); err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
	})

	t.Run("pending final response policy", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			entered, gate := make(chan struct{}), make(chan struct{})
			release := sync.OnceFunc(func() { close(gate) })
			defer release()
			signal := sync.OnceFunc(func() { close(entered) })
			route := Route{
				Name: "snap-pending-policy",
				ResolveFinalResponse: func(context.Context, FinalResponseSelection) (int, error) {
					signal()
					<-gate
					return 1, nil
				},
			}
			elm, _, rq := admitFinalResolverFork(t, route,
				newInternalInviteReq(sip.MagicCookie+".snap-pending-policy"))
			prx := rq.prx
			initial, err := rq.Snapshot()
			if err != nil {
				t.Fatalf("rq.Snapshot() error = %v, want nil", err)
			}
			var mu sync.Mutex
			var published []*RequestSnapshot
			defer prx.BindSnapshotHandler(RequestSnapshotHandlerFunc(func(_ context.Context, snap *RequestSnapshot) {
				if snap.RequestID == initial.RequestID && snap.UpstreamKey.Equal(initial.UpstreamKey) {
					mu.Lock()
					published = append(published, snap)
					mu.Unlock()
				}
			}))()
			txA := finalResolverTarget(t, elm, rq, "A")
			if err := txA.RecvResponse(t.Context(), newInternalInRes(t, txA.Request(), 486)); err != nil {
				t.Fatalf("txA.RecvResponse(486) error = %v, want nil", err)
			}
			synctest.Wait()
			mu.Lock()
			beforeCount := len(published)
			beforeRevision := initial.Revision
			if beforeCount > 0 {
				beforeRevision = published[beforeCount-1].Revision
			}
			mu.Unlock()
			if beforeCount == 0 {
				t.Fatal("no stable publication before eligible policy entry")
			}
			txB := finalResolverTarget(t, elm, rq, "B")
			if err := txB.RecvResponse(t.Context(), newInternalInRes(t, txB.Request(), 486)); err != nil {
				t.Fatalf("txB.RecvResponse(486) error = %v, want nil", err)
			}
			synctest.Wait()
			select {
			case <-entered:
			default:
				t.Fatal("resolver did not enter")
			}
			if _, err := rq.Snapshot(); !errors.Is(err, errSnapshotUnstable) {
				t.Fatalf("pending rq.Snapshot() error = %v, want errSnapshotUnstable", err)
			}
			rq.enqueue(t.Context(), workEvent{kind: evtSchedule})
			synctest.Wait()
			mu.Lock()
			pendingCount := len(published)
			mu.Unlock()
			if pendingCount != beforeCount {
				t.Fatalf("pending publications = %d, want unchanged %d", pendingCount, beforeCount)
			}
			release()
			synctest.Wait()
			mu.Lock()
			defer mu.Unlock()
			if len(published) <= beforeCount {
				t.Fatal("no stable publication after policy release")
			}
			committed := published[len(published)-1]
			if committed.Revision <= beforeRevision || !committed.Completed || !committed.CompletionCommitted ||
				committed.Outcome != OutcomeResponse ||
				committed.FinalCandidateIndex == nil || *committed.FinalCandidateIndex != 1 {
				t.Fatalf("published decision = %+v, want later committed response at index 1", committed)
			}
		})
	})
}

func TestProxyRequestSnapshot_PublishedRevision(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		route := Route{
			Name:        "snap-pub",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		elm, prx, tp := newInternalTestProxy(t, route, Options{
			ServedDomains: []string{"alice.voip.com"},
		})

		var revs []uint64
		unbind := prx.BindSnapshotHandler(RequestSnapshotHandlerFunc(
			func(_ context.Context, snap *RequestSnapshot) {
				revs = append(revs, snap.Revision)
			},
		))
		defer unbind()

		rq := admitInternalReq(t, elm, prx, newInternalInviteReq(sip.MagicCookie+".snap-pub"))
		synctest.Wait()

		if len(revs) == 0 {
			t.Fatal("no snapshot revisions published")
		}
		for i := 1; i < len(revs); i++ {
			if revs[i] <= revs[i-1] {
				t.Fatalf("published revisions=%v, want strictly increasing", revs)
			}
		}

		outReq := testutil.WaitResult(t, tp.sendReqCalls, "outbound branch send")
		key, err := sip.MakeClientTransactionKey(outReq)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx, ok := elm.LoadClientTransaction(key)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}

		// A committed downstream response publishes a higher revision.
		if err := clnTx.RecvResponse(ctx, newInternalInRes(t, outReq, sip.ResponseStatusRinging)); err != nil {
			t.Fatalf("clnTx.RecvResponse() error = %v, want nil", err)
		}
		synctest.Wait()

		if got, want := revs[len(revs)-1], revs[0]; got <= want {
			t.Fatalf("last published revision=%d, want > %d", got, want)
		}

		snap, err := rq.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		if snap.Revision != revs[len(revs)-1] {
			t.Fatalf("direct snapshot revision=%d, want last published %d", snap.Revision, revs[len(revs)-1])
		}
	})
}

func TestProxyRequestSnapshot_FinalCandidateIndex(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		index      int
		clearFinal bool
		wantErr    bool
		wantCause  error
	}{
		{"valid zero index", 0, false, false, nil},
		{"missing committed index", -1, false, true, errInvalidFinalResIndex},
		{"out of range committed index", 1, false, true, errInvalidFinalResIndex},
		{"missing committed final", 0, true, true, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				route := Route{
					Name:        "snap-final-index",
					ForwardMode: ForwardModeStateful,
					ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
						return newInternalLitPlan(), nil
					},
				}
				elm, prx, tp := newInternalTestProxy(t, route, Options{ServedDomains: []string{"alice.voip.com"}})
				rq := admitInternalReq(t, elm, prx, newInternalInviteReq(sip.MagicCookie+".snap-final-index"))
				synctest.Wait()
				initial, err := rq.Snapshot()
				if err != nil {
					t.Fatalf("rq.Snapshot() error = %v, want nil", err)
				}
				if initial.FinalCandidateIndex != nil {
					t.Fatalf("unfinished selection = %v, want nil", initial.FinalCandidateIndex)
				}
				out := testutil.WaitResult(t, tp.sendReqCalls, "outbound request send")
				key, err := sip.MakeClientTransactionKey(out)
				if err != nil {
					t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
				}
				tx, ok := elm.LoadClientTransaction(key)
				if !ok {
					t.Fatal("forwarding client transaction not registered")
				}
				if err := tx.RecvResponse(ctx, newInternalInRes(t, out, 486)); err != nil {
					t.Fatalf("tx.RecvResponse(486) error = %v, want nil", err)
				}
				synctest.Wait()
				rq.mu.Lock()
				rq.finalResIndex = tt.index
				if tt.clearFinal {
					rq.final = nil
				}
				rq.mu.Unlock()
				snap, err := rq.Snapshot()
				if tt.wantErr {
					if !errors.Is(err, ErrInvalidSnapshot) || !errors.Is(err, ErrClass) {
						t.Fatalf("rq.Snapshot() error = %v, want classified invalid snapshot", err)
					}
					if tt.wantCause != nil && !errors.Is(err, tt.wantCause) {
						t.Fatalf("rq.Snapshot() error = %v, want cause %v", err, tt.wantCause)
					}
					return
				}
				if err != nil {
					t.Fatalf("rq.Snapshot() error = %v, want nil", err)
				}
				if snap.FinalCandidateIndex == nil || *snap.FinalCandidateIndex != 0 {
					t.Fatalf("captured selection = %v, want non-nil index 0", snap.FinalCandidateIndex)
				}
				*snap.FinalCandidateIndex = 1
				next, err := rq.Snapshot()
				if err != nil {
					t.Fatalf("rq.Snapshot() after mutation error = %v, want nil", err)
				}
				if next.FinalCandidateIndex == nil || *next.FinalCandidateIndex != 0 || rq.finalResIndex != 0 {
					t.Fatalf("selection after capture mutation = (%v, %d), want (index 0, 0)",
						next.FinalCandidateIndex, rq.finalResIndex)
				}
			})
		})
	}
}

func TestProxyRequestSnapshot_CloneIsIndependent(t *testing.T) {
	t.Parallel()

	index := 0
	snap := &RequestSnapshot{
		Version:   reqSnapshotVer,
		Revision:  7,
		RouteName: "r",
		TargetPlan: TargetPlan{Groups: [][]Target{
			{{
				ID:         "A",
				URI:        &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr("10.0.0.9", 5060)},
				Attributes: map[string]string{"k": "v"},
			}},
		}},
		TargetStates: []TargetSnapshot{
			{TargetID: "A", Started: true, AttemptedAddresses: []sip.ResolvedAddr{{Transport: "UDP"}}},
		},
		WorkingRequest:      newInternalInviteReq(sip.MagicCookie + ".snap-clone-work"),
		FinalCandidateIndex: &index,
	}
	clone := snap.Clone()
	if clone.FinalCandidateIndex == nil || *clone.FinalCandidateIndex != 0 {
		t.Fatalf("cloned selection = %v, want non-nil index 0", clone.FinalCandidateIndex)
	}
	*clone.FinalCandidateIndex = 1
	clone.TargetPlan.Groups[0][0].Attributes["k"] = "evil"
	clone.TargetStates[0].AttemptedAddresses = nil
	clone.WorkingRequest.WithMessage(func(r *sip.Request) {
		r.Headers.AppendRoute(header.RouteHop{URI: &sip.URI{Addr: sip.MakeHostAddr("evil.example")}})
	})
	if snap.TargetPlan.Groups[0][0].Attributes["k"] != "v" {
		t.Fatal("mutating the clone changed the original snapshot")
	}
	if *snap.FinalCandidateIndex != 0 {
		t.Fatal("mutating the clone changed the original selection index")
	}
	if len(snap.TargetStates[0].AttemptedAddresses) != 1 {
		t.Fatal("mutating the clone changed the original snapshot")
	}
	snap.WorkingRequest.WithMessage(func(r *sip.Request) {
		if _, ok := r.Headers.FirstRoute(); ok {
			t.Fatal("mutating the clone changed the original snapshot")
		}
	})
}

func TestProxy_RestoreRequest_DormantInstall(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		route := Route{
			Name:        "rest",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		opts := Options{ServedDomains: []string{"alice.voip.com"}}
		elm1, prx1, tp1 := newInternalTestProxy(t, route, opts)

		inReq := newInternalInviteReq(sip.MagicCookie + ".rest-install")
		rq1 := admitInternalReq(t, elm1, prx1, inReq)
		synctest.Wait()

		outReq := testutil.WaitResult(t, tp1.sendReqCalls, "outbound request send")
		clnKey, err := sip.MakeClientTransactionKey(outReq)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx1, ok := elm1.LoadClientTransaction(clnKey)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}
		srvKey, err := sip.MakeServerTransactionKey(inReq)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		snap, err := rq1.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		srvSnapper, ok := rq1.srvTx.(sip.ServerTransactionSnapshotter)
		if !ok {
			t.Fatal("upstream transaction does not implement sip.ServerTransactionSnapshotter")
		}
		srvSnap, err := srvSnapper.Snapshot()
		if err != nil {
			t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
		}
		clnSnapper, ok := clnTx1.(sip.ClientTransactionSnapshotter)
		if !ok {
			t.Fatal("client transaction does not implement sip.ClientTransactionSnapshotter")
		}
		clnSnap, err := clnSnapper.Snapshot()
		if err != nil {
			t.Fatalf("clnTx.Snapshot() error = %v, want nil", err)
		}

		elm2, prx2, tp2 := newInternalTestProxy(t, route, opts)
		srvTx, err := sip.RestoreServerTransaction(srvSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		clnTx, err := sip.RestoreClientTransaction(clnSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
		}

		rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, RestoreBindings{
			Element:            elm2,
			ServerTransaction:  srvTx,
			ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{clnKey: clnTx},
		})
		if err != nil {
			t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
		}

		rq2.mu.Lock()
		dormant, begun := rq2.dormant, rq2.begun
		att := rq2.attempts[clnKey]
		st := rq2.targets["A"]
		pending := len(rq2.pendingAdms)
		unbinds := len(rq2.unbinds)
		srvHeld := rq2.srvTx
		timerC := st.timeC
		deliveries := rq2.deliveries
		rq2.mu.Unlock()

		if !dormant || !begun {
			t.Fatalf("restored request=(dormant=%t, begun=%t), want (true, true)", dormant, begun)
		}
		if srvHeld != srvTx {
			t.Fatal("upstream binding not installed on the coordinator")
		}
		if att == nil || att.tx != clnTx {
			t.Fatal("client transaction binding not installed on the attempt")
		}
		if att.gen == 0 || st.gen == 0 {
			t.Fatal("restored attempt and target carry no runtime generations")
		}
		if !st.txKey.Equal(clnKey) {
			t.Fatalf("target txKey=%v, want %v", st.txKey, clnKey)
		}
		if timerC.IsZero() {
			t.Fatal("live INVITE attempt lost its Timer C deadline")
		}
		if pending != 0 {
			t.Fatalf("pending admissions=%d, want 0", pending)
		}
		if unbinds != 5 {
			t.Fatalf("staged unbinds=%d, want 5 (2 upstream + 3 downstream)", unbinds)
		}
		if deliveries != nil {
			t.Fatalf("restored deliveries=%v, want nil for ordinary targets", deliveries)
		}
		if got, ok := prx2.loadSrvReq(elm2, srvKey); !ok || got != rq2 {
			t.Fatal("restored request not indexed by its upstream key")
		}

		// The dormant callback queue cannot drain: a queued event waits
		// for activation without scheduling a drain owner.
		rq2.enqueue(ctx, workEvent{kind: evtSrvErr, err: errors.ErrorWrap("test")})
		synctest.Wait()
		rq2.mu.Lock()
		queued, draining := len(rq2.queue), rq2.draining
		srvErr := rq2.srvErr
		rq2.mu.Unlock()
		if queued != 1 || draining {
			t.Fatalf("dormant queue=(queued=%d, draining=%t), want (1, false)", queued, draining)
		}
		if srvErr != nil {
			t.Fatal("dormant queue drained a staged event")
		}
	})
}

func TestProxy_RestoreRequest_InvalidValuesLeaveBindingsUntouched(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*RequestSnapshot)
		wantErr error
	}{
		{
			"missing via",
			func(snap *RequestSnapshot) {
				snap.WorkingRequest.WithMessage(func(r *sip.Request) { r.Headers.Delete("Via") })
			},
			ErrInvalidSnapshot,
		},
		{
			"opaque original metadata",
			func(snap *RequestSnapshot) {
				snap.OriginalRequest.Metadata().Set("restore.opaque", make(chan int))
			},
			ErrSnapshotUnsupported,
		},
		{
			"opaque working metadata",
			func(snap *RequestSnapshot) {
				snap.WorkingRequest.Metadata().Set("restore.opaque", make(chan int))
			},
			ErrSnapshotUnsupported,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				route := Route{
					Name:        "restore-values",
					ForwardMode: ForwardModeStateful,
					ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
						return newInternalLitPlan(), nil
					},
				}
				opts := Options{ServedDomains: []string{"alice.voip.com"}}
				elm1, prx1, tp1 := newInternalTestProxy(t, route, opts)
				rq1 := admitInternalReq(t, elm1, prx1, newInternalInviteReq(sip.MagicCookie+".restore-values"))
				synctest.Wait()
				outReq := testutil.WaitResult(t, tp1.sendReqCalls, "outbound request send")
				clnKey, err := sip.MakeClientTransactionKey(outReq)
				if err != nil {
					t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
				}
				clnTx1, ok := elm1.LoadClientTransaction(clnKey)
				if !ok {
					t.Fatal("forwarding client transaction not registered")
				}
				snap, err := rq1.Snapshot()
				if err != nil {
					t.Fatalf("rq1.Snapshot() error = %v, want nil", err)
				}
				srvSnap, err := rq1.srvTx.(sip.ServerTransactionSnapshotter).Snapshot()
				if err != nil {
					t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
				}
				clnSnap, err := clnTx1.(sip.ClientTransactionSnapshotter).Snapshot()
				if err != nil {
					t.Fatalf("clnTx.Snapshot() error = %v, want nil", err)
				}

				elm, prx, tp := newInternalTestProxy(t, route, opts)
				controlReq := newInternalInviteReq(sip.MagicCookie + ".restore-values-control")
				control := admitInternalReq(t, elm, prx, controlReq)
				synctest.Wait()
				controlKey, err := sip.MakeServerTransactionKey(controlReq)
				if err != nil {
					t.Fatalf("sip.MakeServerTransactionKey(control) error = %v, want nil", err)
				}
				if got, ok := prx.loadSrvReq(elm, controlKey); !ok || got != control {
					t.Fatal("control request not indexed before rejection")
				}
				if got := len(drainInternalReqs(tp)); got != 1 {
					t.Fatalf("control request sends = %d, want 1", got)
				}
				drainInternalRess(tp)

				srvTx, err := sip.RestoreInviteServerTransaction(srvSnap, tp)
				if err != nil {
					t.Fatalf("sip.RestoreInviteServerTransaction() error = %v, want nil", err)
				}
				clnTx, err := sip.RestoreInviteClientTransaction(clnSnap, tp)
				if err != nil {
					t.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
				}
				srv := &bindCountingSrvTransact{InviteServerTransaction: srvTx}
				cln := &bindCountingClnTransact{InviteClientTransaction: clnTx}
				bindings := RestoreBindings{
					Element:            elm,
					ServerTransaction:  srv,
					ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{clnKey: cln},
				}
				var observations atomic.Int32
				unbindEvents := prx.BindEventHandler(EventHandlerFunc(
					func(context.Context, Event) { observations.Add(1) },
				))
				defer unbindEvents()
				unbindSnapshots := prx.BindSnapshotHandler(RequestSnapshotHandlerFunc(
					func(context.Context, *RequestSnapshot) { observations.Add(1) },
				))
				defer unbindSnapshots()

				valid := snap.Clone()
				tt.mutate(snap)
				rq, err := prx.RestoreRequest(sip.ContextWithElement(ctx, elm), snap, bindings)
				if rq != nil || !errors.Is(err, tt.wantErr) {
					t.Fatalf("prx.RestoreRequest() = (%v, %v), want (nil, %v)", rq, err, tt.wantErr)
				}
				synctest.Wait()
				if got := srv.bindBalance(); got != 0 {
					t.Fatalf("upstream staged binds after rejection = %d, want 0", got)
				}
				if got := cln.bindBalance(); got != 0 {
					t.Fatalf("downstream staged binds after rejection = %d, want 0", got)
				}
				if srv.Started() || cln.Started() {
					t.Fatal("rejected restore started a borrowed transaction")
				}
				if srv.State() == sip.TransactionStateTerminated ||
					cln.State() == sip.TransactionStateTerminated {
					t.Fatal("rejected restore terminated a borrowed transaction")
				}
				if got, ok := prx.loadSrvReq(elm, snap.UpstreamKey); ok || got != nil {
					t.Fatal("rejected restore installed a candidate upstream index entry")
				}
				if got, ok := prx.loadSrvReq(elm, controlKey); !ok || got != control {
					t.Fatal("rejected restore disturbed the unrelated control request")
				}
				if got := len(drainInternalReqs(tp)) + len(drainInternalRess(tp)); got != 0 {
					t.Fatalf("rejected restore sends = %d, want 0", got)
				}
				if got := observations.Load(); got != 0 {
					t.Fatalf("rejected restore observations = %d, want 0", got)
				}

				// Repair only the saved values and reuse the exact borrowed
				// transactions: failed validation must not consume ownership.
				snap.OriginalRequest = valid.OriginalRequest
				snap.WorkingRequest = valid.WorkingRequest
				rq, err = prx.RestoreRequest(sip.ContextWithElement(ctx, elm), snap, bindings)
				if rq == nil || err != nil {
					t.Fatalf("prx.RestoreRequest(repaired) = (%v, %v), want a dormant request and nil", rq, err)
				}
				synctest.Wait()
				rq.mu.Lock()
				dormant := rq.dormant
				boundSrv := rq.srvTx
				boundCln := rq.attempts[clnKey].tx
				rq.mu.Unlock()
				if !dormant || boundSrv != srv || boundCln != cln {
					t.Fatal("repaired restore did not adopt the same bindings dormant")
				}
				if got, ok := prx.loadSrvReq(elm, snap.UpstreamKey); !ok || got != rq {
					t.Fatal("repaired restore did not index its coordinator")
				}
				if srv.Started() || cln.Started() ||
					srv.State() == sip.TransactionStateTerminated ||
					cln.State() == sip.TransactionStateTerminated {
					t.Fatal("repaired dormant restore started or terminated a borrowed transaction")
				}
				if got, ok := prx.loadSrvReq(elm, controlKey); !ok || got != control {
					t.Fatal("repaired restore disturbed the unrelated control request")
				}
				if got := len(drainInternalReqs(tp)) + len(drainInternalRess(tp)); got != 0 {
					t.Fatalf("repaired dormant restore sends = %d, want 0", got)
				}
				if got := observations.Load(); got != 0 {
					t.Fatalf("repaired dormant restore observations = %d, want 0", got)
				}
			})
		})
	}
}

func TestProxy_RestoreRequest_RollbackOnlyStaged(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		route := Route{
			Name:        "rest-rb",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		opts := Options{ServedDomains: []string{"alice.voip.com"}}
		elm1, prx1, tp1 := newInternalTestProxy(t, route, opts)

		inReq := newInternalInviteReq(sip.MagicCookie + ".rest-rollback")
		rq1 := admitInternalReq(t, elm1, prx1, inReq)
		synctest.Wait()

		outReq := testutil.WaitResult(t, tp1.sendReqCalls, "outbound request send")
		clnKey, err := sip.MakeClientTransactionKey(outReq)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx1, ok := elm1.LoadClientTransaction(clnKey)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}
		srvKey, err := sip.MakeServerTransactionKey(inReq)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		snap, err := rq1.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		srvSnap, err := rq1.srvTx.(sip.ServerTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
		}
		clnSnap, err := clnTx1.(sip.ClientTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("clnTx.Snapshot() error = %v, want nil", err)
		}

		elm2, prx2, tp2 := newInternalTestProxy(t, route, opts)
		mkBindings := func() RestoreBindings {
			srvTx, err := sip.RestoreInviteServerTransaction(srvSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreInviteServerTransaction() error = %v, want nil", err)
			}
			clnTx, err := sip.RestoreInviteClientTransaction(clnSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
			}
			return RestoreBindings{
				Element:           elm2,
				ServerTransaction: &bindCountingSrvTransact{InviteServerTransaction: srvTx},
				ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{
					clnKey: &bindCountingClnTransact{InviteClientTransaction: clnTx},
				},
			}
		}

		rqFirst, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, mkBindings())
		if err != nil {
			t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
		}

		second := mkBindings()
		srv2 := second.ServerTransaction.(*bindCountingSrvTransact)
		cln2 := second.ClientTransactions[clnKey].(*bindCountingClnTransact)
		if _, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, second); err == nil {
			t.Fatal("duplicate upstream restore accepted")
		}

		if got, ok := prx2.loadSrvReq(elm2, srvKey); !ok || got != rqFirst {
			t.Fatal("failed install disturbed the existing index entry")
		}
		if got := srv2.bindBalance(); got != 0 {
			t.Fatalf("upstream staged binds=%d, want 0 (staged unbinds rolled back)", got)
		}
		if got := cln2.bindBalance(); got != 0 {
			t.Fatalf("downstream staged binds=%d, want 0 (staged unbinds rolled back)", got)
		}
		if srv2.Started() || cln2.Started() {
			t.Fatal("borrowed transaction started by a failed install")
		}
		if srv2.State() == sip.TransactionStateTerminated ||
			cln2.State() == sip.TransactionStateTerminated {
			t.Fatal("borrowed transaction terminated by a failed install")
		}
	})
}

func TestProxy_RestoreRequest_CompletedAttemptBindingIsolation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		route := Route{
			Name:        "rest-iso",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		opts := Options{ServedDomains: []string{"alice.voip.com"}}
		elm1, prx1, tp1 := newInternalTestProxy(t, route, opts)

		inReq := newInternalInviteReq(sip.MagicCookie + ".rest-iso")
		rq1 := admitInternalReq(t, elm1, prx1, inReq)
		synctest.Wait()
		testutil.WaitResult(t, tp1.sendReqCalls, "outbound request send")

		snap, err := rq1.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		srvSnap, err := rq1.srvTx.(sip.ServerTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
		}

		// Two real client transactions stand in for the failover history of
		// target A: the settled attempt committed a 503, the live sibling
		// is still Proceeding on its provisional response.
		out1 := newInternalInviteReq(sip.MagicCookie + ".rest-iso-a1")
		cln1, err := elm1.NewClientTransaction(ctx, out1, tp1)
		if err != nil {
			t.Fatalf("elm.NewClientTransaction() error = %v, want nil", err)
		}
		if err := cln1.Start(ctx); err != nil {
			t.Fatalf("cln1.Start() error = %v, want nil", err)
		}
		res503 := newInternalInRes(t, cln1.Request(), sip.ResponseStatusServiceUnavailable)
		if err := cln1.RecvResponse(ctx, res503); err != nil {
			t.Fatalf("cln1.RecvResponse(503) error = %v, want nil", err)
		}
		out2 := newInternalInviteReq(sip.MagicCookie + ".rest-iso-a2")
		cln2, err := elm1.NewClientTransaction(ctx, out2, tp1)
		if err != nil {
			t.Fatalf("elm.NewClientTransaction() error = %v, want nil", err)
		}
		if err := cln2.Start(ctx); err != nil {
			t.Fatalf("cln2.Start() error = %v, want nil", err)
		}
		res180 := newInternalInRes(t, cln2.Request(), sip.ResponseStatusRinging)
		if err := cln2.RecvResponse(ctx, res180); err != nil {
			t.Fatalf("cln2.RecvResponse(180) error = %v, want nil", err)
		}
		synctest.Wait()

		clnSnap1, err := cln1.(sip.ClientTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("cln1.Snapshot() error = %v, want nil", err)
		}
		clnSnap2, err := cln2.(sip.ClientTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("cln2.Snapshot() error = %v, want nil", err)
		}

		deadline := snap.Time.Add(time.Hour)
		snap.TargetStates = []TargetSnapshot{{
			TargetID:           "A",
			Started:            true,
			AttemptedAddresses: []sip.ResolvedAddr{{Transport: "UDP", Addr: netip.MustParseAddrPort("10.0.0.9:5060")}},
		}}
		snap.Attempts = []AttemptSnapshot{
			{
				Key:                  clnSnap1.Key,
				TargetID:             "A",
				Request:              clnSnap1.Request,
				Addr:                 sip.ResolvedAddr{Transport: "UDP", Addr: netip.MustParseAddrPort("10.0.0.9:5060")},
				State:                sip.TransactionStateCompleted,
				Response:             res503,
				Completed:            true,
				InitialSendSettled:   true,
				InitialSendSucceeded: true,
			},
			{
				Key:                  clnSnap2.Key,
				TargetID:             "A",
				Request:              clnSnap2.Request,
				Addr:                 sip.ResolvedAddr{Transport: "UDP", Addr: netip.MustParseAddrPort("10.0.0.10:5060")},
				State:                sip.TransactionStateProceeding,
				Response:             res180,
				TimerCDeadline:       deadline,
				InitialSendSettled:   true,
				InitialSendSucceeded: true,
			},
		}

		elm2, prx2, tp2 := newInternalTestProxy(t, route, opts)
		srvTx, err := sip.RestoreServerTransaction(srvSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		settledTx, err := sip.RestoreClientTransaction(clnSnap1, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction(settled) error = %v, want nil", err)
		}
		liveTx, err := sip.RestoreClientTransaction(clnSnap2, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction(live) error = %v, want nil", err)
		}

		rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, RestoreBindings{
			Element:           elm2,
			ServerTransaction: srvTx,
			ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{
				clnSnap1.Key: settledTx,
				clnSnap2.Key: liveTx,
			},
		})
		if err != nil {
			t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
		}

		rq2.mu.Lock()
		defer rq2.mu.Unlock()
		st := rq2.targets["A"]
		settledAtt := rq2.attempts[clnSnap1.Key]
		liveAtt := rq2.attempts[clnSnap2.Key]
		if settledAtt == nil || settledAtt.tx != settledTx || !settledAtt.settled {
			t.Fatal("settled attempt record not installed")
		}
		if liveAtt == nil || liveAtt.tx != liveTx || liveAtt.settled {
			t.Fatal("live attempt record not installed")
		}
		if !st.txKey.Equal(clnSnap2.Key) {
			t.Fatalf("live target txKey=%v, want live sibling %v (settled binding must not reclaim it)", st.txKey, clnSnap2.Key)
		}
		if !st.timeC.Equal(deadline) {
			t.Fatalf("live target timerC=%v, want %v (settled binding must not disarm it)", st.timeC, deadline)
		}
		if st.lastRes == nil || st.lastRes.Status() != sip.ResponseStatusRinging {
			t.Fatalf("live target lastRes=%v, want the sibling's 180", st.lastRes)
		}
		if len(rq2.outcomes) != 1 || !rq2.outcomes[0].AttemptKey.Equal(clnSnap1.Key) {
			t.Fatalf("restored outcomes=%+v, want the settled attempt's stored outcome", rq2.outcomes)
		}
	})
}

func TestProxy_RestoreRequest_PromotedMode(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		// A stateless route whose request was promoted to the stateful
		// pipeline: the snapshot records the owning route name with the
		// actual stateful processing mode.
		slRoute := Route{
			Name:        "sl-rest",
			ForwardMode: ForwardModeStateless,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		opts := Options{
			StatelessKey:  []byte("promoted-restore-key"),
			ServedDomains: []string{"alice.voip.com"},
		}
		elm1, prx1, tp1 := newInternalTestProxy(t, slRoute, opts)

		inReq := newInternalInviteReq(sip.MagicCookie + ".sl-rest")
		prepared, err := newPreparedRouting(inReq, inReq, newInternalLitPlan())
		if err != nil {
			t.Fatalf("newPreparedRouting() error = %v, want nil", err)
		}
		if err := prx1.promoteStatefulReq(ctx, elm1, &slRoute, prepared); err != nil {
			t.Fatalf("prx.promoteStatefulReq() error = %v, want nil", err)
		}
		synctest.Wait()

		outReq := testutil.WaitResult(t, tp1.sendReqCalls, "outbound request send")
		clnKey, err := sip.MakeClientTransactionKey(outReq)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx1, ok := elm1.LoadClientTransaction(clnKey)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}
		srvKey, err := sip.MakeServerTransactionKey(inReq)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		rq1, ok := prx1.loadSrvReq(elm1, srvKey)
		if !ok {
			t.Fatal("promoted request not indexed by its upstream key")
		}
		snap, err := rq1.Snapshot()
		if err != nil {
			t.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		if snap.RouteName != "sl-rest" || snap.Mode != ForwardModeStateful {
			t.Fatalf("promoted snapshot route=(%q, %v), want (%q, %v)",
				snap.RouteName,
				snap.Mode,
				"sl-rest",
				ForwardModeStateful,
			)
		}
		srvSnap, err := rq1.srvTx.(sip.ServerTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
		}
		clnSnap, err := clnTx1.(sip.ClientTransactionSnapshotter).Snapshot()
		if err != nil {
			t.Fatalf("clnTx.Snapshot() error = %v, want nil", err)
		}

		// The restore-side proxy still carries the stateless configuration:
		// the promoted snapshot must round-trip against it.
		elm2, prx2, tp2 := newInternalTestProxy(t, slRoute, opts)
		srvTx, err := sip.RestoreServerTransaction(srvSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
		}
		clnTx, err := sip.RestoreClientTransaction(clnSnap, tp2)
		if err != nil {
			t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
		}

		rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, RestoreBindings{
			Element:            elm2,
			ServerTransaction:  srvTx,
			ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{clnKey: clnTx},
		})
		if err != nil {
			t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
		}

		rq2.mu.Lock()
		mode := rq2.route.ForwardMode
		rq2.mu.Unlock()
		if mode != ForwardModeStateful {
			t.Fatalf("restored request route mode=%v, want %v", mode, ForwardModeStateful)
		}
		if got := prx2.routes[0].ForwardMode; got != ForwardModeStateless {
			t.Fatalf("stored route mode=%v, want %v (restore never mutates configuration)", got, ForwardModeStateless)
		}
		if got, ok := prx2.loadSrvReq(elm2, srvKey); !ok || got != rq2 {
			t.Fatal("restored request not indexed by its upstream key")
		}
	})
}

// restoreElmFixture is the captured in-flight INVITE checkpoint of the
// element-binding restore cases: the request snapshot plus the dormant
// transaction snapshots its runtime bindings are restored from.
type restoreElmFixture struct {
	snap    *RequestSnapshot
	srvSnap *sip.ServerTransactionSnapshot
	clnSnap *sip.ClientTransactionSnapshot
	srvKey  sip.ServerTransactionKey
	clnKey  sip.ClientTransactionKey
}

// newRestoreElmFixture admits an in-flight INVITE on a throwaway source
// proxy and captures the request and transaction checkpoints.
func newRestoreElmFixture(tb testing.TB, route Route, opts Options) *restoreElmFixture {
	tb.Helper()

	elm, prx, tp := newInternalTestProxy(tb, route, opts)
	inReq := newInternalInviteReq(sip.MagicCookie + ".rest-elm")
	rq := admitInternalReq(tb, elm, prx, inReq)
	synctest.Wait()

	outReq := testutil.WaitResult(tb, tp.sendReqCalls, "outbound request send")
	clnKey, err := sip.MakeClientTransactionKey(outReq)
	if err != nil {
		tb.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
	}
	clnTx, ok := elm.LoadClientTransaction(clnKey)
	if !ok {
		tb.Fatal("forwarding client transaction not registered")
	}
	srvKey, err := sip.MakeServerTransactionKey(inReq)
	if err != nil {
		tb.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
	}
	snap, err := rq.Snapshot()
	if err != nil {
		tb.Fatalf("rq.Snapshot() error = %v, want nil", err)
	}
	srvSnap, err := rq.srvTx.(sip.ServerTransactionSnapshotter).Snapshot()
	if err != nil {
		tb.Fatalf("srvTx.Snapshot() error = %v, want nil", err)
	}
	clnSnap, err := clnTx.(sip.ClientTransactionSnapshotter).Snapshot()
	if err != nil {
		tb.Fatalf("clnTx.Snapshot() error = %v, want nil", err)
	}
	return &restoreElmFixture{
		snap:    snap,
		srvSnap: srvSnap,
		clnSnap: clnSnap,
		srvKey:  srvKey,
		clnKey:  clnKey,
	}
}

// dormantBindings builds fresh dormant transaction bindings of the
// fixture against tp for elm, wrapped in subscription-counting spies.
func (f *restoreElmFixture) dormantBindings(
	tb testing.TB,
	elm *sip.Element,
	tp *internalTransp,
) (*bindCountingSrvTransact, *bindCountingClnTransact, RestoreBindings) {
	tb.Helper()

	srvTx, err := sip.RestoreInviteServerTransaction(f.srvSnap, tp)
	if err != nil {
		tb.Fatalf("sip.RestoreInviteServerTransaction() error = %v, want nil", err)
	}
	clnTx, err := sip.RestoreInviteClientTransaction(f.clnSnap, tp)
	if err != nil {
		tb.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
	}
	srv := &bindCountingSrvTransact{InviteServerTransaction: srvTx}
	cln := &bindCountingClnTransact{InviteClientTransaction: clnTx}
	return srv, cln, RestoreBindings{
		Element:            elm,
		ServerTransaction:  srv,
		ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{f.clnKey: cln},
	}
}

// trackRestoreElm attaches a second element with its own stub transport to
// prx so one proxy serves two processing namespaces.
func trackRestoreElm(tb testing.TB, prx *Proxy) (*sip.Element, *internalTransp) {
	tb.Helper()

	elm := &sip.Element{}
	tb.Cleanup(func() { elm.Close() })
	tp := newInternalTransp()
	if err := elm.TrackTransport(tp); err != nil {
		tb.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}
	elm.UseComponent(prx)
	return elm, tp
}

// ctxMarkKey is the unrelated caller-context value a restore must
// preserve while it overrides the caller's element.
type ctxMarkKey struct{}

// TestProxy_RestoreElementBinding pins the explicit element contract of
// [Proxy.RestoreRequest]: [RestoreBindings.Element] is required and
// authoritative — a restore never resolves its element from the caller's
// context and never infers ownership from a transaction key.
func TestProxy_RestoreElementBinding(t *testing.T) {
	t.Parallel()

	route := Route{
		Name:        "rest-elm",
		ForwardMode: ForwardModeStateful,
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
			return newInternalLitPlan(), nil
		},
	}
	opts := Options{ServedDomains: []string{"alice.voip.com"}}

	t.Run("nil element rejects before staging even with a ctx element", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreElmFixture(t, route, opts)
			elmA, prx, tpA := newInternalTestProxy(t, route, opts)
			elmB, _ := trackRestoreElm(t, prx)

			srv, cln, bindings := f.dormantBindings(t, nil, tpA)
			var observations atomic.Int32
			unbindEvents := prx.BindEventHandler(EventHandlerFunc(
				func(context.Context, Event) { observations.Add(1) },
			))
			defer unbindEvents()
			unbindSnaps := prx.BindSnapshotHandler(RequestSnapshotHandlerFunc(
				func(context.Context, *RequestSnapshot) { observations.Add(1) },
			))
			defer unbindSnaps()

			// The caller context carries a valid element the restore must
			// not adopt: only the explicit binding can name the namespace.
			rq, err := prx.RestoreRequest(sip.ContextWithElement(ctx, elmB), f.snap, bindings)
			if rq != nil || err == nil {
				t.Fatalf("prx.RestoreRequest() = (%v, %v), want (nil, %v)", rq, err, ErrMissingElement)
			}
			if !errors.Is(err, ErrMissingElement) {
				t.Fatalf("prx.RestoreRequest() error = %v, want %v", err, ErrMissingElement)
			}
			if !IsError(err) {
				t.Fatalf("prx.RestoreRequest() error = %v, want proxy classification", err)
			}
			synctest.Wait()

			// The rejection precedes every staged effect: no subscription
			// staged, no borrowed transaction claimed or moved, no element
			// scope or index entry allocated, and no send, timer, or
			// observation produced.
			if got := srv.bindBalance(); got != 0 {
				t.Fatalf("upstream staged binds=%d, want 0", got)
			}
			if got := cln.bindBalance(); got != 0 {
				t.Fatalf("downstream staged binds=%d, want 0", got)
			}
			if srv.Started() || cln.Started() {
				t.Fatal("rejected restore started a borrowed transaction")
			}
			if srv.State() == sip.TransactionStateTerminated ||
				cln.State() == sip.TransactionStateTerminated {
				t.Fatal("rejected restore terminated a borrowed transaction")
			}
			if es := prx.getElemState(elmA, false); es != nil {
				t.Fatal("rejected restore allocated the candidate element scope")
			}
			if es := prx.getElemState(elmB, false); es != nil {
				t.Fatal("rejected restore allocated the caller-context element scope")
			}
			if got := len(drainInternalReqs(tpA)) + len(drainInternalRess(tpA)); got != 0 {
				t.Fatalf("rejected restore sends=%d, want 0", got)
			}
			if got := observations.Load(); got != 0 {
				t.Fatalf("rejected restore observations=%d, want 0", got)
			}
		})
	})

	t.Run("explicit binding overrides the caller context element", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreElmFixture(t, route, opts)
			elmA, prx, tpA := newInternalTestProxy(t, route, opts)
			elmB, _ := trackRestoreElm(t, prx)

			_, _, bindings := f.dormantBindings(t, elmA, tpA)
			ctxB := context.WithValue(sip.ContextWithElement(ctx, elmB), ctxMarkKey{}, "caller")
			rq, err := prx.RestoreRequest(ctxB, f.snap, bindings)
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}

			if got, ok := prx.LookupRequest(elmA, f.srvKey); !ok || got != rq {
				t.Fatal("restored request not indexed under the bound element")
			}
			if got, ok := prx.LookupRequest(elmB, f.srvKey); ok || got != nil {
				t.Fatal("restored request resolved under the caller-context element")
			}
			if got, ok := sip.ElementFromContext(rq.ctx); !ok || got != elmA {
				t.Fatalf("restored request ctx element=(%v, %t), want the bound element", got, ok)
			}
			if got := rq.ctx.Value(ctxMarkKey{}); got != "caller" {
				t.Fatalf("restored request ctx marker=%v, want the preserved caller value", got)
			}
		})
	})

	t.Run("equal keys under two elements resolve independently", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreElmFixture(t, route, opts)
			elmA, prx, tpA := newInternalTestProxy(t, route, opts)
			elmB, tpB := trackRestoreElm(t, prx)

			_, _, bindingsA := f.dormantBindings(t, elmA, tpA)
			_, _, bindingsB := f.dormantBindings(t, elmB, tpB)
			rqA, err := prx.RestoreRequest(ctx, f.snap, bindingsA)
			if err != nil {
				t.Fatalf("prx.RestoreRequest(elmA) error = %v, want nil", err)
			}
			rqB, err := prx.RestoreRequest(ctx, f.snap, bindingsB)
			if err != nil {
				t.Fatalf("prx.RestoreRequest(elmB) error = %v, want nil", err)
			}
			if rqA == rqB {
				t.Fatal("equal upstream keys restored to one coordinator")
			}
			if got, ok := prx.LookupRequest(elmA, f.srvKey); !ok || got != rqA {
				t.Fatal("restore under element A not indexed by its upstream key")
			}
			if got, ok := prx.LookupRequest(elmB, f.srvKey); !ok || got != rqB {
				t.Fatal("restore under element B not indexed by its upstream key")
			}
		})
	})

	t.Run("equal key under the same element conflicts", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreElmFixture(t, route, opts)
			elmA, prx, tpA := newInternalTestProxy(t, route, opts)

			_, _, first := f.dormantBindings(t, elmA, tpA)
			rq1, err := prx.RestoreRequest(ctx, f.snap, first)
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}

			srv2, cln2, second := f.dormantBindings(t, elmA, tpA)
			rq2, err := prx.RestoreRequest(ctx, f.snap, second)
			if rq2 != nil || !errors.Is(err, ErrInvalidSnapshot) {
				t.Fatalf("duplicate restore = (%v, %v), want (nil, %v)", rq2, err, ErrInvalidSnapshot)
			}
			if got, ok := prx.LookupRequest(elmA, f.srvKey); !ok || got != rq1 {
				t.Fatal("conflicting restore disturbed the existing index entry")
			}
			if got := srv2.bindBalance(); got != 0 {
				t.Fatalf("upstream staged binds=%d, want 0", got)
			}
			if got := cln2.bindBalance(); got != 0 {
				t.Fatalf("downstream staged binds=%d, want 0", got)
			}
			if srv2.Started() || cln2.Started() {
				t.Fatal("conflicting restore started a borrowed transaction")
			}
			if got := len(drainInternalReqs(tpA)) + len(drainInternalRess(tpA)); got != 0 {
				t.Fatalf("conflicting restore sends=%d, want 0", got)
			}
		})
	})

	t.Run("a key owned on another element implies no ownership", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newRestoreElmFixture(t, route, opts)
			elmA, prx, tpA := newInternalTestProxy(t, route, opts)
			elmB, tpB := trackRestoreElm(t, prx)

			// A live request on element B already owns the upstream key the
			// snapshot carries: the restore bound to element A must neither
			// attach there nor disturb the live association.
			live := admitInternalReq(t, elmB, prx, newInternalInviteReq(sip.MagicCookie+".rest-elm"))
			synctest.Wait()
			drainInternalReqs(tpB)
			if got, ok := prx.LookupRequest(elmB, f.srvKey); !ok || got != live {
				t.Fatal("live request on element B not indexed by its upstream key")
			}

			_, _, bindings := f.dormantBindings(t, elmA, tpA)
			rq, err := prx.RestoreRequest(ctx, f.snap, bindings)
			if err != nil {
				t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
			}
			if rq == live {
				t.Fatal("restore attached to the live request of another element")
			}
			if got, ok := prx.LookupRequest(elmA, f.srvKey); !ok || got != rq {
				t.Fatal("restored request not indexed under the bound element")
			}
			if got, ok := prx.LookupRequest(elmB, f.srvKey); !ok || got != live {
				t.Fatal("restore disturbed the live association of the other element")
			}
		})
	})
}

func TestProxy_SnapshotErrorClass(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"canceled", errclass.Classify(errors.ErrorWrap("x"), errclass.ClassCanceled), "canceled"},
		{"timeout", sip.NewTransactionTimedOutError(), "timeout"},
		{"closed", sip.NewTransactionManagerClosedError(), "closed"},
		{"transport", sip.NewNoTransportError(), "transport"},
		{"message", sip.NewInvalidMessageError(), "message"},
		{"transaction", sip.NewTransactionNotFoundError(), "transaction"},
		{"element closed", sip.NewElementClosedError(), "closed"},
		{"element", errclass.Classify(errors.ErrorWrap("x"), sip.ErrClassElement), "element"},
		{"other", errors.ErrorWrap("misc"), "error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := attemptErrClass(tt.err); got != tt.want {
				t.Fatalf("attemptErrClass(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}
