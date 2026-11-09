package sip

import (
	"context"
	"iter"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip/header"
)

// prxInternalTransp is a minimal client/server transport stub for
// white-box coordinator tests.
type prxInternalTransp struct {
	StdMsgInterceptChain

	sendReqCalls chan *RequestEnvelope
	sendResCalls chan *ResponseEnvelope
	sendReqHook  func(context.Context, *RequestEnvelope) error
}

func newPrxInternalTransp() *prxInternalTransp {
	return &prxInternalTransp{
		sendReqCalls: make(chan *RequestEnvelope, 8),
		sendResCalls: make(chan *ResponseEnvelope, 8),
	}
}

func (*prxInternalTransp) Metadata() TransportMetadata { return UDPMetadata() }
func (tp *prxInternalTransp) SendRequest(
	ctx context.Context,
	req *RequestEnvelope,
	_ ...SendRequestOptions,
) error {
	if tp.sendReqHook != nil {
		if err := tp.sendReqHook(ctx, req); err != nil {
			return errors.Wrap(err)
		}
	}
	tp.sendReqCalls <- req
	return nil
}

func (tp *prxInternalTransp) SendResponse(
	_ context.Context,
	res *ResponseEnvelope,
	_ ...SendResponseOptions,
) error {
	tp.sendResCalls <- res
	return nil
}

func (*prxInternalTransp) Respond(
	context.Context,
	*RequestEnvelope,
	ResponseStatus,
	...RespondOptions,
) error {
	return nil
}

func (*prxInternalTransp) Close() error { return nil }
func (*prxInternalTransp) Listen(context.Context, string) (TransportListener, error) {
	return nil, nil
}

func (*prxInternalTransp) MatchSentBy(Addr) bool { return true }
func newInternalTestProxy(
	tb testing.TB,
	route ProxyRoute,
	opts ProxyOptions,
	elems ...*Element,
) (*Element, *Proxy, *prxInternalTransp) {
	tb.Helper()

	tp := newPrxInternalTransp()

	elm := &Element{}
	if len(elems) > 0 {
		elm = elems[len(elems)-1]
	}
	tb.Cleanup(func() { elm.Close() })
	if err := elm.TrackTransport(tp); err != nil {
		tb.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	prx, err := NewProxy(elm, []ProxyRoute{route}, opts)
	if err != nil {
		tb.Fatalf("NewProxy() error = %v, want nil", err)
	}

	return elm, prx, tp
}

func newInternalLitPlan() ProxyTargetPlan {
	return ProxyTargetPlan{Groups: [][]ProxyTarget{
		{
			{
				ID: "A",
				URI: &URI{
					User: MakeUserInfo("alice"),
					Addr: MakeHostPortAddr("10.0.0.9", 5060),
				},
			},
		},
	}}
}

func newInternalInRes(tb testing.TB, req *RequestEnvelope, sts ResponseStatus) *ResponseEnvelope {
	tb.Helper()

	msg, err := req.Message().NewResponse(sts)
	if err != nil {
		tb.Fatalf("req.NewResponse() error = %v, want nil", err)
	}
	return NewResponseEnvelope(msg).
		SetTransport(req.Transport()).
		SetLocalAddr(req.LocalAddr()).
		SetRemoteAddr(req.RemoteAddr())
}

func admitInternalReq(
	tb testing.TB,
	prx *Proxy,
	req *RequestEnvelope,
) *ProxyRequest {
	tb.Helper()

	err := prx.InterceptInboundRequest(
		tb.Context(),
		RequestReceiverFunc(func(context.Context, *RequestEnvelope) error { return nil }),
		req,
	)
	if err != nil {
		tb.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
	}
	key, err := MakeServerTransactionKey(req)
	if err != nil {
		tb.Fatalf("MakeServerTransactionKey() error = %v, want nil", err)
	}
	rq, ok := prx.loadSrvReq(key)
	if !ok {
		tb.Fatal("admitted request not associated with the server transaction")
	}
	return rq
}

func TestProxy_promotion_admission(t *testing.T) {
	t.Parallel()

	for _, race := range []bool{false, true} {
		name := "immediate response and admitted duplicate"
		if race {
			name = "racing prepared operations share one upstream owner"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				var authorized, preprocessed, resolved, insideRecv atomic.Int32
				arrived := make(chan struct{}, 2)
				release := make(chan struct{})
				plan := newInternalLitPlan()
				plan.Groups[0][0].URI.Addr = MakeHostAddr("promotion.example.org")
				route := ProxyRoute{
					Name:        "promotion-admission",
					ForwardMode: ForwardModeStateless,
					AuthorizeRequest: func(context.Context, *RequestEnvelope) error {
						authorized.Add(1)
						return nil
					},
					PreprocessRoute: func(context.Context, *RequestEnvelope) error {
						preprocessed.Add(1)
						return nil
					},
					ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
						n := resolved.Add(1)
						if race && n <= 2 {
							arrived <- struct{}{}
							<-release
						}
						return plan, nil
					},
				}
				elm, prx, tp := newInternalTestProxy(t, route, ProxyOptions{
					StatelessKey:  []byte("promotion-admission-key"),
					ServedDomains: []string{"alice.voip.com"},
				}, &Element{ServerLocator: prxInternalFixedLocator{}})
				req := newInternalInviteReq(MagicCookie + ".promotion-admission")
				srvKey, err := MakeServerTransactionKey(req)
				if err != nil {
					t.Fatalf("MakeServerTransactionKey() error = %v, want nil", err)
				}
				tp.sendReqHook = func(sendCtx context.Context, out *RequestEnvelope) error {
					owner, ok := prxTxOwnerFromCtx(sendCtx)
					if !ok || owner.srv || owner.rq == nil {
						return errors.ErrorWrap("client send has no promotion owner")
					}
					rq := owner.rq
					// Probe both guards in the synchronous callback, not after
					// the send returns: effects must never hold these locks.
					if !rq.mu.TryLock() {
						return errors.ErrorWrap("send ran under coordinator lock")
					}
					rq.mu.Unlock()
					if !prx.reqsMu.TryLock() {
						return errors.ErrorWrap("send ran under request index lock")
					}
					prx.reqsMu.Unlock()
					if got, ok := prx.LookupRequest(srvKey); !ok || got != rq ||
						rq.srvTx.Key() != srvKey || !rq.srvTx.Request().URI().Equal(req.URI()) {
						return errors.ErrorWrap("promotion lost original upstream association")
					}
					key, err := MakeClientTransactionKey(out)
					if err != nil {
						return errors.Wrap(err)
					}
					tx, ok := elm.LoadClientTransaction(key)
					if !ok {
						return errors.ErrorWrap("client transaction missing during Start")
					}
					if err := tx.RecvResponse(sendCtx, newInternalInRes(t, out, ResponseStatusRinging)); err != nil {
						return errors.Wrap(err)
					}
					insideRecv.Add(1)
					return nil
				}
				var rq *ProxyRequest
				wantHooks := int32(1)
				if race {
					wantHooks = 2 // Independent stateless operations already ran policy.
					inputs := []*RequestEnvelope{cloneReqEnvelope(req), cloneReqEnvelope(req)}
					errs := make([]error, len(inputs))
					var wg sync.WaitGroup
					for i, in := range inputs {
						wg.Go(func() {
							errs[i] = prx.InterceptInboundRequest(ctx,
								RequestReceiverFunc(func(context.Context, *RequestEnvelope) error { return nil }), in)
						})
					}
					<-arrived
					<-arrived
					close(release)
					wg.Wait()
					for _, err := range errs {
						if err != nil {
							t.Fatalf("racing admission error = %v, want nil", err)
						}
					}
					rq, _ = prx.LookupRequest(srvKey)
				} else {
					rq = admitInternalReq(t, prx, req)
				}
				synctest.Wait()
				if rq == nil || insideRecv.Load() != 1 {
					t.Fatalf("owned request = %v, synchronous responses = %d, want owner and 1", rq, insideRecv.Load())
				}
				rq.mu.Lock()
				gotRinging := rq.targets["A"].lastRes != nil &&
					rq.targets["A"].lastRes.Status() == ResponseStatusRinging
				pending := len(rq.pendingAdms)
				rq.mu.Unlock()
				if !gotRinging || pending != 0 {
					t.Fatalf("response attributed = %t, pending admissions = %d, want true, 0", gotRinging, pending)
				}
				// Drive a retransmission through the existing transaction-manager
				// receive path; it must not start another stateless operation.
				recv := InterceptInboundRequest(slices.Collect(tp.InboundRequestInterceptors.All()),
					RequestReceiverFunc(func(context.Context, *RequestEnvelope) error {
						return errors.ErrorWrap("duplicate escaped transaction manager")
					}))
				if err := recv.RecvRequest(ctx, cloneReqEnvelope(req)); err != nil {
					t.Fatalf("duplicate receive error = %v, want nil", err)
				}
				synctest.Wait()
				if len(prx.srvRqs) != 1 || len(rq.attempts) != 1 ||
					len(slices.Collect(elm.AllServerTransactions())) != 1 ||
					len(slices.Collect(elm.AllClientTransactions())) != 1 || len(tp.sendReqCalls) != 1 {
					t.Fatal("duplicate admission leaked ownership indexes, transactions, or branches")
				}
				if got := []int32{authorized.Load(), preprocessed.Load(), resolved.Load()}; !slices.Equal(got, []int32{wantHooks, wantHooks, wantHooks}) {
					t.Fatalf("route hook calls = %v, want [%d %d %d]", got, wantHooks, wantHooks, wantHooks)
				}
				if err := prx.Shutdown(ctx); err != nil {
					t.Fatalf("prx.Shutdown() error = %v, want nil", err)
				}
				synctest.Wait()
				if len(prx.srvRqs) != 0 || len(rq.unbinds) != 0 || len(rq.pendingAdms) != 0 ||
					len(slices.Collect(elm.AllServerTransactions())) != 0 ||
					len(slices.Collect(elm.AllClientTransactions())) != 0 {
					t.Fatal("promotion admission left subscriptions, indexes, or transactions after retirement")
				}
			})
		})
	}
}

type prxInternalFixedLocator struct{}

func (prxInternalFixedLocator) LookupRequestAddrs(
	context.Context, *URI, ...LookupMessageAddrsOptions,
) iter.Seq[ResolvedAddr] {
	return func(yield func(ResolvedAddr) bool) {
		yield(ResolvedAddr{Transport: "UDP", Addr: netip.MustParseAddrPort("10.0.0.9:5060")})
	}
}

func TestProxy_prepared_routing_ownership(t *testing.T) {
	t.Parallel()

	for _, part := range []string{"original", "working", "plan"} {
		t.Run(part, func(t *testing.T) {
			t.Parallel()

			source := newInternalInviteReq(MagicCookie + ".prepared-ownership")
			hop := header.RouteHop{URI: &URI{
				Addr: MakeHostAddr("hop.example.org"), Params: make(Values).Set("lr", ""),
			}}
			source.WithMessage(func(r *Request) { r.Headers.AppendRoute(hop) })
			source.Metadata().Set("owner", "source")
			before := source.Message().Render()
			sourceBefore := cloneReqEnvelope(source)
			plan := ProxyTargetPlan{Groups: [][]ProxyTarget{
				{{
					ID: "A", URI: source.URI().(*URI), Routes: []header.RouteHop{hop},
					Attributes: map[string]string{"role": "primary"},
				}},
				{{ID: "B", URI: source.URI().(*URI), Attributes: map[string]string{"role": "backup"}}},
			}}
			wantPlan, err := clonePrxTargetPlan(plan)
			if err != nil {
				t.Fatalf("clonePrxTargetPlan() error = %v, want nil", err)
			}
			// Even identical source pointers must produce independent original,
			// working, and plan URI/Route copies.
			prepared, err := newPrxPreparedRouting(source, source, plan)
			if err != nil {
				t.Fatalf("newPrxPreparedRouting() error = %v, want nil", err)
			}
			if prepared.input.original == prepared.input.working || prepared.input.original == source ||
				prepared.input.working == source {
				t.Fatal("prepared request envelopes alias source or sibling")
			}
			if part == "plan" {
				target := &prepared.plan.Groups[0][0]
				target.URI.Addr = MakeHostAddr("changed-target")
				target.Routes[0].URI.(*URI).Addr = MakeHostAddr("changed-target-route")
				target.Attributes["role"] = "changed"
				prepared.plan.Groups[1] = nil
			} else {
				mutated, sibling := prepared.input.original, prepared.input.working
				if part == "working" {
					mutated, sibling = sibling, mutated
				}
				mutated.WithMessage(func(r *Request) {
					r.URI.(*URI).Addr = MakeHostAddr("changed-request")
					route, _ := r.Headers.FirstRoute()
					route.URI.(*URI).Addr = MakeHostAddr("changed-request-route")
					r.Headers.Append(&header.Any{Name: "X-Clone", Value: part})
				})
				mutated.Metadata().Set("owner", part)
				if !sibling.Equal(sourceBefore) {
					t.Fatal("request clone mutation changed its sibling")
				}
				if diff := cmp.Diff(wantPlan, prepared.plan); diff != "" {
					t.Fatalf("plan changed through request clone (-want +got): %s", diff)
				}
			}
			if part == "plan" && (prepared.input.original.Message().Render() != before ||
				prepared.input.working.Message().Render() != before) {
				t.Fatal("plan mutation changed a prepared request")
			}
			if !source.Equal(sourceBefore) {
				t.Fatal("prepared result mutation changed source request or metadata")
			}
			if diff := cmp.Diff(wantPlan, plan); diff != "" {
				t.Fatalf("source plan changed (-want +got): %s", diff)
			}
		})
	}

	t.Run("invalid inputs return errors without admission effects", func(t *testing.T) {
		t.Parallel()

		elm, prx, tp := newInternalTestProxy(t, ProxyRoute{Name: "prepared-invalid"}, ProxyOptions{})
		req := newInternalInviteReq(MagicCookie + ".prepared-invalid")
		plan := newInternalLitPlan()
		duplicate := ProxyTargetPlan{Groups: [][]ProxyTarget{plan.Groups[0], plan.Groups[0]}}
		invalid := cloneReqEnvelope(req)
		invalid.WithMessage(func(r *Request) { r.URI = nil })
		for _, tc := range []struct {
			name              string
			original, working *RequestEnvelope
			plan              ProxyTargetPlan
		}{
			{"duplicate target IDs", req, req, duplicate},
			{"missing original", nil, req, plan},
			{"missing working", req, nil, plan},
			{"empty working envelope", req, &RequestEnvelope{}, plan},
			{"invalid original", invalid, req, plan},
			{"invalid working", req, invalid, plan},
			{"empty plan", req, req, ProxyTargetPlan{}},
		} {
			if got, err := newPrxPreparedRouting(tc.original, tc.working, tc.plan); err == nil ||
				got.input.original != nil || got.input.working != nil || got.plan.hasTargets() {
				t.Fatalf("%s: newPrxPreparedRouting() = %+v, %v, want zero result and error", tc.name, got, err)
			}
		}
		if len(prx.srvRqs) != 0 || len(slices.Collect(elm.AllServerTransactions())) != 0 ||
			len(slices.Collect(elm.AllClientTransactions())) != 0 || len(tp.sendReqCalls)+len(tp.sendResCalls) != 0 {
			t.Fatal("invalid prepared result caused admission or transport effects")
		}
	})
}

func TestProxyStateful_ClaimRequiresOwnershipToken(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		route := ProxyRoute{
			Name:        "test",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
				return newInternalLitPlan(), nil
			},
		}
		elm, prx, tp := newInternalTestProxy(t, route, ProxyOptions{
			ServedDomains: []string{"alice.voip.com"},
		})

		// A transaction carrying only an application-controlled route name is
		// never claimed.
		req := newInternalInviteReq(MagicCookie + ".foreign-meta")
		req.Metadata().Set(prxRouteMetaKey, route.Name)
		frnTx, err := elm.NewServerTransaction(ctx, req, tp)
		if err != nil {
			t.Fatalf("elm.NewServerTransaction() error = %v, want nil", err)
		}
		if _, ok := prx.loadSrvReq(frnTx.Key()); ok {
			t.Fatal("server transaction claimed through request metadata")
		}
		synctest.Wait()

		// A token minted for another proxy does not claim for this one.
		other, err := NewProxy(elm, []ProxyRoute{{Name: "other"}})
		if err != nil {
			t.Fatalf("sip.NewProxy() error = %v, want nil", err)
		}
		rq := newPrxReq(ctx, other, &route, req)
		ownerCtx := context.WithValue(ctx, prxTxOwnerCtxKey{}, &prxTxOwner{rq: rq, srv: true})
		prx.HandleServerTransaction(ownerCtx, frnTx)
		if _, ok := prx.loadSrvReq(frnTx.Key()); ok {
			t.Fatal("server transaction claimed by foreign proxy token")
		}
		_ = frnTx.Terminate(ctx, nil)

		// Proper stateful admission claims the upstream transaction.
		req2 := newInternalInviteReq(MagicCookie + ".owned-admit")
		rq2 := admitInternalReq(t, prx, req2)
		if rq2.srvTx == nil {
			t.Fatal("admitted request has no upstream server transaction")
		}
		key2, err := MakeServerTransactionKey(req2)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		if got, ok := prx.loadSrvReq(key2); !ok || got != rq2 {
			t.Fatalf("prx.loadSrvReq() = (%v, %t), want the admitted request", got, ok)
		}
	})
}

func TestProxyRequest_EnqueueDrain(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		route := ProxyRoute{Name: "test", ForwardMode: ForwardModeStateful}
		_, prx, _ := newInternalTestProxy(t, route, ProxyOptions{})

		rq := newPrxReq(ctx, prx, &route, newInternalInviteReq(MagicCookie+".drain"))
		// An idle queue transitions to the asynchronous drain; the enqueueing
		// call never blocks on decision work.
		rq.enqueue(ctx, prxWorkEvent{kind: prxEvtSrvErr, err: errors.ErrorWrap("first")})
		rq.enqueue(ctx, prxWorkEvent{kind: prxEvtSrvErr, err: errors.ErrorWrap("second")})
		synctest.Wait()

		if rq.srvErr == nil {
			t.Fatal("drain did not process queued events")
		}
		rq.mu.Lock()
		draining := rq.draining
		queued := len(rq.queue)
		rq.mu.Unlock()
		if draining {
			t.Fatal("drain owner still active on an idle queue")
		}
		if queued != 0 {
			t.Fatalf("pending events=%d, want 0", queued)
		}

		// A later enqueue on an idle queue schedules a new drain owner.
		rq.enqueue(ctx, prxWorkEvent{kind: prxEvtSrvState, to: TransactionStateTerminated})
		synctest.Wait()
		if !rq.srvGone {
			t.Fatal("idle-to-nonempty transition did not schedule a drain")
		}
	})
}

func TestProxyRequest_ResponseAttribution(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()

		var resolved atomic.Int32
		route := ProxyRoute{
			Name:        "test",
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
				resolved.Add(1)
				return newInternalLitPlan(), nil
			},
		}
		elm, prx, tp := newInternalTestProxy(t, route, ProxyOptions{
			ServedDomains: []string{"alice.voip.com"},
		})

		req := newInternalInviteReq(MagicCookie + ".attribution")
		rq := admitInternalReq(t, prx, req)
		synctest.Wait()

		outReq := testutil.WaitResult(t, tp.sendReqCalls, "outbound request send")
		key, err := MakeClientTransactionKey(outReq)
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		clnTx, ok := elm.LoadClientTransaction(key)
		if !ok {
			t.Fatal("forwarding client transaction not registered")
		}

		rq.mu.Lock()
		att := rq.attempts[key]
		st := rq.targets["A"]
		rq.mu.Unlock()
		if att == nil {
			t.Fatal("client transaction not associated with an attempt")
		}
		if att.targetID != "A" {
			t.Fatalf("attempt target=%q, want %q", att.targetID, "A")
		}
		if st == nil || !st.txKey.Equal(key) {
			t.Fatalf("attempt txKey=%v, want %v", st.txKey, key)
		}
		if st.timeC.IsZero() {
			t.Fatal("INVITE attempt has no Timer C deadline")
		}

		// An event from a stale admission generation is never attributed to
		// the live attempt.
		stale := newInternalInRes(t, outReq, ResponseStatusBadRequest)
		rq.enqueue(ctx, prxWorkEvent{
			kind: prxEvtClnRes,
			key:  key,
			gen:  att.gen + 100,
			res:  stale,
		})
		synctest.Wait()
		rq.mu.Lock()
		outcomes := len(rq.outcomes)
		done := rq.targets["A"].done
		rq.mu.Unlock()
		if outcomes != 0 || done {
			t.Fatal("stale-generation response attributed to the live attempt")
		}

		// The live generation settles the attempt.
		if err := clnTx.RecvResponse(ctx, newInternalInRes(t, outReq, ResponseStatusOK)); err != nil {
			t.Fatalf("clnTx.RecvResponse() error = %v, want nil", err)
		}
		synctest.Wait()

		rq.mu.Lock()
		defer rq.mu.Unlock()
		if !rq.targets["A"].done {
			t.Fatal("final response did not settle the attempt")
		}
		if len(rq.outcomes) != 1 {
			t.Fatalf("recorded outcomes=%d, want 1", len(rq.outcomes))
		}
		oc := rq.outcomes[0]
		if oc.TargetID != "A" || !oc.AttemptKey.Equal(key) || !oc.Completed {
			t.Fatalf("outcome=%+v, want completed outcome for target A key %v", oc, key)
		}
		if oc.Response == nil || oc.Response.Status() != ResponseStatusOK {
			t.Fatalf("outcome response=%v, want 200 OK", oc.Response)
		}
		if !rq.done || rq.outcome != ProxyOutcomeResponse {
			t.Fatalf("request outcome=(done=%t, %v), want (true, response)", rq.done, rq.outcome)
		}
	})
}

func TestProxyRequest_SchedulerContract(t *testing.T) {
	t.Parallel()

	forkHosts := map[string]string{
		"A": "10.0.0.9",
		"B": "10.0.0.10",
		"C": "10.0.0.11",
	}
	forkPlan := func(groups ...[]string) ProxyTargetPlan {
		var plan ProxyTargetPlan
		for _, group := range groups {
			targets := make([]ProxyTarget, 0, len(group))
			for _, id := range group {
				targets = append(targets, ProxyTarget{
					ID: id,
					URI: &URI{
						User: MakeUserInfo("alice"),
						Addr: MakeHostPortAddr(forkHosts[id], 5060),
					},
				})
			}
			plan.Groups = append(plan.Groups, targets)
		}
		return plan
	}
	admitFork := func(
		tb testing.TB,
		name string,
		plan ProxyTargetPlan,
		sel func(context.Context, ProxySelection) ([]string, error),
	) (*Element, *prxInternalTransp, *ProxyRequest) {
		tb.Helper()

		route := ProxyRoute{
			Name:        name,
			ForwardMode: ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *RequestEnvelope) (ProxyTargetPlan, error) {
				return plan, nil
			},
			SelectTargets: sel,
		}
		elm, prx, tp := newInternalTestProxy(tb, route, ProxyOptions{
			ServedDomains: []string{"alice.voip.com"},
		})
		rq := admitInternalReq(tb, prx, newInternalInviteReq(MagicCookie+"."+name))
		return elm, tp, rq
	}
	// settleTarget injects a downstream response into the client transaction
	// of the named target's current attempt.
	settleTarget := func(
		tb testing.TB,
		ctx context.Context,
		elm *Element,
		rq *ProxyRequest,
		id string,
		sts ResponseStatus,
	) {
		tb.Helper()

		rq.mu.Lock()
		st := rq.targets[id]
		rq.mu.Unlock()
		if st == nil || !st.txKey.IsValid() {
			tb.Fatalf("target %q has no attempt transaction", id)
		}
		tx, ok := elm.LoadClientTransaction(st.txKey)
		if !ok {
			tb.Fatalf("client transaction for target %q not registered", id)
		}
		if err := tx.RecvResponse(ctx, newInternalInRes(tb, tx.Request(), sts)); err != nil {
			tb.Fatalf("clnTx.RecvResponse(%s) error = %v, want nil", sts, err)
		}
	}
	targetState := func(rq *ProxyRequest, id string) (started, done bool) {
		rq.mu.Lock()
		defer rq.mu.Unlock()
		st := rq.targets[id]
		return st != nil && st.started, st != nil && st.done
	}
	// inviteSendCount drains the stub transport's recorded sends and counts
	// only INVITE branch starts, dropping the ACK copies an INVITE client
	// transaction emits on a non-2xx final response.
	inviteSendCount := func(tp *prxInternalTransp) int {
		cnt := 0
		for {
			select {
			case req := <-tp.sendReqCalls:
				if req.Method().Equal(RequestMethodInvite) {
					cnt++
				}
			default:
				return cnt
			}
		}
	}

	t.Run("selector contract violations fail the request", func(t *testing.T) {
		t.Parallel()

		for _, tt := range []struct {
			name   string
			first  []string
			settle string
			second []string
			want   error
		}{
			{
				name:  "unknown ID",
				first: []string{"ghost"},
				want:  errUnknownPrxTargetID,
			},
			{
				name:  "duplicate ID",
				first: []string{"A", "A"},
				want:  errDuplicatePrxTargetID,
			},
			{
				name:   "active ID",
				first:  []string{"A", "B"},
				settle: "A",
				second: []string{"B"},
				want:   errActivePrxTargetID,
			},
			{
				name:   "finished ID",
				first:  []string{"A"},
				settle: "A",
				second: []string{"A"},
				want:   errFinishedPrxTargetID,
			},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				synctest.Test(t, func(t *testing.T) {
					var calls atomic.Int32
					elm, _, rq := admitFork(t, "fork-violation", forkPlan([]string{"A", "B"}),
						func(context.Context, ProxySelection) ([]string, error) {
							if calls.Add(1) == 1 {
								return tt.first, nil
							}
							return tt.second, nil
						})
					synctest.Wait()

					if tt.settle != "" {
						settleTarget(t, t.Context(), elm, rq, tt.settle, ResponseStatusNotFound)
						synctest.Wait()
					}

					rq.mu.Lock()
					defer rq.mu.Unlock()
					if !rq.done {
						t.Fatal("invalid selection did not fail the request")
					}
					if rq.outcome != ProxyOutcomeAbandoned {
						t.Fatalf("request outcome=%v, want %v", rq.outcome, ProxyOutcomeAbandoned)
					}
					if !errors.Is(rq.lastErr, tt.want) {
						t.Fatalf("request lastErr=%v, want %v", rq.lastErr, tt.want)
					}
				})
			})
		}
	})

	t.Run("empty selection declines unstarted targets", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			var calls atomic.Int32
			elm, _, rq := admitFork(t, "fork-decline", forkPlan([]string{"A"}, []string{"B"}),
				func(context.Context, ProxySelection) ([]string, error) {
					if calls.Add(1) == 1 {
						return []string{"A"}, nil
					}
					return nil, nil
				})
			synctest.Wait()

			settleTarget(t, t.Context(), elm, rq, "A", ResponseStatusNotFound)
			synctest.Wait()

			started, done := targetState(rq, "B")
			if started || !done {
				t.Fatalf("declined target B=(started=%t, done=%t), want (false, true)", started, done)
			}
			rq.mu.Lock()
			defer rq.mu.Unlock()
			if !rq.done {
				t.Fatal("request not done after selection declined the last target")
			}
			if got := calls.Load(); got != 2 {
				t.Fatalf("selection hook calls=%d, want 2", got)
			}
		})
	})

	t.Run("received 6xx closes scheduling", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			elm, tp, rq := admitFork(t, "fork-6xx", forkPlan([]string{"A", "B"}, []string{"C"}), nil)
			synctest.Wait()
			if got := inviteSendCount(tp); got != 2 {
				t.Fatalf("initial outbound sends=%d, want 2", got)
			}

			// A 6xx ends forking: unstarted targets settle as declined and no
			// new branch may start, while the active sibling keeps running.
			settleTarget(t, t.Context(), elm, rq, "A", ResponseStatusBusyEverywhere)
			synctest.Wait()

			started, done := targetState(rq, "C")
			if started || !done {
				t.Fatalf("target C=(started=%t, done=%t) after 6xx, want (false, true)", started, done)
			}
			rq.mu.Lock()
			closed := rq.schedClosed
			rqDone := rq.done
			rq.mu.Unlock()
			if !closed {
				t.Fatal("received 6xx did not close target selection")
			}
			if rqDone {
				t.Fatal("request completed while a sibling branch stayed active")
			}
			if got := inviteSendCount(tp); got != 0 {
				t.Fatalf("new outbound sends after 6xx=%d, want 0 (no new branch)", got)
			}

			settleTarget(t, t.Context(), elm, rq, "B", ResponseStatusBusyHere)
			synctest.Wait()
			rq.mu.Lock()
			defer rq.mu.Unlock()
			if !rq.done || rq.outcome != ProxyOutcomeResponse {
				t.Fatalf("request outcome=(done=%t, %v), want (true, response)", rq.done, rq.outcome)
			}
		})
	})

	t.Run("success closes scheduling", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			elm, tp, rq := admitFork(t, "fork-2xx", forkPlan([]string{"A"}, []string{"B"}), nil)
			synctest.Wait()
			if got := inviteSendCount(tp); got != 1 {
				t.Fatalf("initial outbound sends=%d, want 1", got)
			}

			settleTarget(t, t.Context(), elm, rq, "A", ResponseStatusOK)
			synctest.Wait()

			started, done := targetState(rq, "B")
			if started || !done {
				t.Fatalf("target B=(started=%t, done=%t) after success, want (false, true)", started, done)
			}
			if got := inviteSendCount(tp); got != 0 {
				t.Fatalf("new outbound sends after success=%d, want 0 (no new branch)", got)
			}
			rq.mu.Lock()
			defer rq.mu.Unlock()
			if !rq.schedClosed {
				t.Fatal("received 2xx did not close target selection")
			}
			if !rq.done || rq.outcome != ProxyOutcomeResponse {
				t.Fatalf("request outcome=(done=%t, %v), want (true, response)", rq.done, rq.outcome)
			}
		})
	})

	t.Run("accepted cancellation closes scheduling", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			var calls atomic.Int32
			elm, tp, rq := admitFork(t, "fork-cancel", forkPlan([]string{"A"}, []string{"B"}),
				func(context.Context, ProxySelection) ([]string, error) {
					calls.Add(1)
					return []string{"A"}, nil
				})
			synctest.Wait()
			if got := inviteSendCount(tp); got != 1 {
				t.Fatalf("initial outbound sends=%d, want 1", got)
			}

			// Once cancellation is accepted no selection may run and no new
			// branch may start, even when active targets settle.
			rq.mu.Lock()
			rq.cancelIntent = true
			rq.mu.Unlock()
			settleTarget(t, t.Context(), elm, rq, "A", ResponseStatusNotFound)
			synctest.Wait()

			if got := calls.Load(); got != 1 {
				t.Fatalf("selection hook calls after cancellation=%d, want 1", got)
			}
			if got := inviteSendCount(tp); got != 0 {
				t.Fatalf("new outbound sends after cancellation=%d, want 0 (no new branch)", got)
			}
			if started, _ := targetState(rq, "B"); started {
				t.Fatal("target B started after cancellation")
			}
		})
	})
}
