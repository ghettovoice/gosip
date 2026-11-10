package proxy_test

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/proxy"
	"github.com/ghettovoice/gosip/sip/stats"
)

// evtRecorder captures every emitted [proxy.Event] for assertions.
type evtRecorder struct {
	mu     sync.Mutex
	events []proxy.Event
}

func (r *evtRecorder) record(_ context.Context, ev proxy.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *evtRecorder) get() []proxy.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

func (r *evtRecorder) counts() map[proxy.EventKind]int {
	counts := make(map[proxy.EventKind]int)
	for _, ev := range r.get() {
		counts[ev.Kind]++
	}
	return counts
}

func (r *evtRecorder) kindSeqs(kind proxy.EventKind) []proxy.Event {
	var out []proxy.Event
	for _, ev := range r.get() {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

// obsLitPlan builds one flat fork plan of literal-IP targets.
func obsLitPlan(ids ...string) proxy.TargetPlan {
	hosts := map[string]string{
		"A": "10.0.0.9", "B": "10.0.0.10", "C": "10.0.0.11",
	}
	targets := make([]proxy.Target, 0, len(ids))
	for _, id := range ids {
		targets = append(targets, proxy.Target{
			ID:  id,
			URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr(hosts[id], 5060)},
		})
	}
	return proxy.TargetPlan{Groups: [][]proxy.Target{targets}}
}

func obsRoute(name string, plan proxy.TargetPlan) *proxy.Route {
	return &proxy.Route{
		Name:        name,
		ForwardMode: proxy.ForwardModeStateful,
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
			return plan, nil
		},
	}
}

func TestProxy_promotion_observations(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		f := newPromotionFixture()
		elm, prx, tp := newConfiguredTestProxy(t, f.route, f.opts, &sip.Element{
			ServerLocator: statelessFixedLocator{addr: netip.MustParseAddrPort("198.51.100.77:5060")},
		})
		rec := &evtRecorder{}
		rcdr := &stats.Recorder{}
		prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))
		prx.BindEventHandler(rcdr)
		before := rcdr.Report().Proxy
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".promotion-observations",
			netip.MustParseAddrPort("192.168.1.100:5060"), netip.MustParseAddrPort("10.0.0.5:5060"))
		fwdIntercept(t, elm, prx, req)
		synctest.Wait()
		sends := drainReqs(tp)
		if len(sends) != 1 {
			t.Fatalf("initial sends = %d, want 1", len(sends))
		}
		key, err := sip.MakeClientTransactionKey(sends[0])
		if err != nil {
			t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
		}
		tx, ok := elm.LoadClientTransaction(key)
		if !ok {
			t.Fatal("promoted transaction missing")
		}
		if err := tx.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusOK)); err != nil {
			t.Fatalf("tx.RecvResponse() error = %v, want nil", err)
		}
		synctest.Wait()
		upstream := drainRess(tp)
		counts := rec.counts()
		wantCounts := map[proxy.EventKind]int{
			proxy.EventKindRequestHandled:         1,
			proxy.EventKindForwardAttemptFinished: 1,
			proxy.EventKindRequestForwarded:       1,
			proxy.EventKindRequestCompleted:       1,
			proxy.EventKindResponseForwarded:      len(upstream),
		}
		if diff := cmp.Diff(wantCounts, counts); diff != "" {
			t.Fatalf("promotion observations (-want +got): %s", diff)
		}
		events := rec.get()
		if events[0].Kind != proxy.EventKindRequestHandled || events[0].RequestID == "" {
			t.Fatalf("first observation = %+v, want handled with request ID", events[0])
		}
		for i, ev := range events {
			if ev.Mode != proxy.ForwardModeStateful || ev.RequestID != events[0].RequestID ||
				ev.Sequence != uint64(i+1) || ev.Error != nil {
				t.Fatalf("promotion event %d = %+v, want one stateful stream with unique monotonic sequences and no failed attempt", i, ev)
			}
		}
		if len(upstream) == 0 || counts[proxy.EventKindResponseForwarded] != len(upstream) {
			t.Fatal("response observations do not match actual upstream sends")
		}
		wantStats := before
		wantStats.RequestsHandled++
		wantStats.ForwardAttemptsSucceeded++
		wantStats.RequestsForwarded++
		wantStats.RequestsCompleted++
		wantStats.ResponsesForwarded += uint64(len(upstream))
		if diff := cmp.Diff(wantStats, rcdr.Report().Proxy); diff != "" {
			t.Fatalf("promotion Recorder delta (-want +got): %s", diff)
		}
	})
}

func TestProxy_ObservationBoundaries(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	opts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}
	noopNext := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil })

	t.Run("handled once at winning admission, absorbed duplicate emits nothing", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &evtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-dedup", obsLitPlan("A")), opts)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-dedup", laddr, raddr)
			fwdIntercept(t, elm, prx, req)
			synctest.Wait()
			if got := len(drainReqs(tp)); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}

			dup := req.Clone().(*sip.RequestEnvelope)
			if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), noopNext, dup); err != nil {
				t.Fatalf("duplicate prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			counts := rec.counts()
			if counts[proxy.EventKindRequestHandled] != 1 {
				t.Fatalf("handled events=%d, want 1", counts[proxy.EventKindRequestHandled])
			}
			if counts[proxy.EventKindForwardAttemptFinished] != 1 ||
				counts[proxy.EventKindRequestForwarded] != 1 {
				t.Fatalf("counts=%v, want one attempt and one forward", counts)
			}
			if got := len(rec.get()); got != 3 {
				// handled + finished attempt + forwarded, nothing more.
				t.Fatalf("total events=%d, want 3", got)
			}
			handled := rec.kindSeqs(proxy.EventKindRequestHandled)[0]
			if handled.Sequence != 1 || handled.RequestID == "" ||
				handled.RouteName != "obs-dedup" || handled.Method != sip.RequestMethodInvite {
				t.Fatalf("handled event=%+v, want seq=1 routed INVITE", handled)
			}
			_ = elm
		})
	})

	t.Run("early matched rejection emits handled, rejected completion and the send", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &evtRecorder{}
			route := obsRoute("obs-reject", obsLitPlan("A"))
			route.AuthorizeRequest = func(context.Context, *sip.RequestEnvelope) error {
				return sip.NewRequestRejectedError(
					errors.ErrorWrap("policy denied"), 0, sip.ResponseStatusForbidden,
				)
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, opts)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-reject", laddr, raddr)
			err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), noopNext, req)
			if err == nil {
				t.Fatal("prx.InterceptInboundRequest() error = nil, want rejection")
			}
			synctest.Wait()

			counts := rec.counts()
			if counts[proxy.EventKindRequestHandled] != 1 ||
				counts[proxy.EventKindRequestCompleted] != 1 {
				t.Fatalf("rejection events=%v, want handled=1 completed=1", counts)
			}
			compl := rec.kindSeqs(proxy.EventKindRequestCompleted)[0]
			if compl.Outcome != proxy.OutcomeRejected || compl.StatusCode != http.StatusForbidden {
				t.Fatalf("completion=%+v, want rejected 403", compl)
			}
			if counts[proxy.EventKindResponseForwarded] != 0 {
				t.Fatalf("response events before send=%d, want 0", counts[proxy.EventKindResponseForwarded])
			}

			// The transport answers the rejection through the outbound
			// response path: the send is observed once it returns nil.
			res, resErr := req.NewResponse(sip.ResponseStatusForbidden)
			if resErr != nil {
				t.Fatalf("req.NewResponse() error = %v, want nil", resErr)
			}
			if err := tp.SendResponse(ctx, res); err != nil {
				t.Fatalf("tp.SendResponse() error = %v, want nil", err)
			}
			synctest.Wait()
			rf := rec.kindSeqs(proxy.EventKindResponseForwarded)
			if len(rf) != 1 {
				t.Fatalf("response-forwarded events=%d, want 1", len(rf))
			}
			if rf[0].StatusCode != http.StatusForbidden || rf[0].Method != sip.RequestMethodInvite ||
				rf[0].RequestID != compl.RequestID {
				t.Fatalf("response event=%+v, want correlated 403 INVITE", rf[0])
			}
		})
	})

	t.Run("failed initial send is one failed attempt, no request forward", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			rec := &evtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-fail", obsLitPlan("A")), opts)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))
			injErr := errors.ErrorWrap("udp write failed")
			tp.sendReqHook = func(sendReqCall, int) error { return injErr }

			fwdIntercept(t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".obs-fail", laddr, raddr))
			synctest.Wait()

			counts := rec.counts()
			if counts[proxy.EventKindForwardAttemptFinished] != 1 {
				t.Fatalf("attempt events=%d, want 1", counts[proxy.EventKindForwardAttemptFinished])
			}
			att := rec.kindSeqs(proxy.EventKindForwardAttemptFinished)[0]
			if att.Error == nil || att.TargetID != "A" || att.AttemptID == "" {
				t.Fatalf("attempt event=%+v, want failed attempt of A", att)
			}
			if counts[proxy.EventKindRequestForwarded] != 0 {
				t.Fatalf("forwarded events=%d, want 0", counts[proxy.EventKindRequestForwarded])
			}
			compl := rec.kindSeqs(proxy.EventKindRequestCompleted)
			if len(compl) != 1 || compl[0].Outcome != proxy.OutcomeSendFailure {
				t.Fatalf("completion=%+v, want one send_failure", compl)
			}
		})
	})

	t.Run("three successful forks: handled=1 attempts=3 forwarded=1", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &evtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-fork", obsLitPlan("A", "B", "C")), opts)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			fwdIntercept(t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".obs-fork", laddr, raddr))
			synctest.Wait()

			counts := rec.counts()
			if counts[proxy.EventKindRequestForwarded] != 1 ||
				counts[proxy.EventKindForwardAttemptFinished] != 3 {
				t.Fatal("fork observation counts are wrong")
			}
			if counts[proxy.EventKindRequestHandled] != 1 {
				t.Fatalf("handled events=%d, want 1", counts[proxy.EventKindRequestHandled])
			}
			for _, ev := range rec.kindSeqs(proxy.EventKindForwardAttemptFinished) {
				if ev.Error != nil || ev.TargetID == "" || ev.AttemptID == "" {
					t.Fatalf("attempt event=%+v, want successful correlated attempt", ev)
				}
			}

			sends := drainReqs(tp)
			if got := len(sends); got != 3 {
				t.Fatalf("outbound sends=%d, want 3", got)
			}

			// Sequences are 1-based committed dispatch order per request.
			evs := rec.get()
			var last uint64
			for i, ev := range evs {
				if ev.RequestID == "" {
					t.Fatalf("event %d has no request correlation: %+v", i, ev)
				}
				if ev.Sequence <= last {
					t.Fatalf("event %d sequence=%d, want > %d", i, ev.Sequence, last)
				}
				last = ev.Sequence
			}

			// Two sibling 2xx forward: each emits its own response event but
			// only one completion. A settled-branch retransmission still
			// forwards and emits its own response event.
			txA := fwdClnTx(t, elm, sends[0])
			txB := fwdClnTx(t, elm, sends[1])
			if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusOK)); err != nil {
				t.Fatalf("txA.RecvResponse(200) error = %v, want nil", err)
			}
			synctest.Wait()
			if err := txB.RecvResponse(ctx, newInRes(t, sends[1], sip.ResponseStatusOK)); err != nil {
				t.Fatalf("txB.RecvResponse(200) error = %v, want nil", err)
			}
			synctest.Wait()
			if got := len(rec.kindSeqs(proxy.EventKindResponseForwarded)); got != 2 {
				t.Fatalf("response events=%d, want 2", got)
			}
			if err := txB.RecvResponse(ctx, newInRes(t, sends[1], sip.ResponseStatusOK)); err != nil {
				t.Fatalf("txB.RecvResponse(retransmitted 200) error = %v, want nil", err)
			}
			synctest.Wait()
			if got := len(rec.kindSeqs(proxy.EventKindResponseForwarded)); got != 3 {
				t.Fatalf("response events after settled retransmission=%d, want 3", got)
			}

			txC := fwdClnTx(t, elm, sends[2])
			if err := txC.RecvResponse(ctx, newInRes(t, sends[2], sip.ResponseStatusOK)); err != nil {
				t.Fatalf("txC.RecvResponse(200) error = %v, want nil", err)
			}
			synctest.Wait()
			compl := rec.kindSeqs(proxy.EventKindRequestCompleted)
			if len(compl) != 1 || compl[0].Outcome != proxy.OutcomeResponse ||
				compl[0].StatusCode != http.StatusOK {
				t.Fatalf("completion=%+v, want one response 200", compl)
			}
			if got := len(rec.kindSeqs(proxy.EventKindResponseForwarded)); got != 4 {
				t.Fatalf("response events=%d, want 4", got)
			}
		})
	})

	t.Run("dns retry failure plus success are distinct attempts", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &evtRecorder{}
			log := &attemptActionLog{}
			var lookups atomic.Int32
			loc := &attemptLocator{
				log:     log,
				lookups: &lookups,
				candidates: map[string][]attemptCandidate{
					"10.0.0.9": {
						udpAttemptCandidate("first", "10.0.0.9:5060"),
						udpAttemptCandidate("second", "10.0.0.10:5060"),
					},
				},
			}
			names := map[string]string{"10.0.0.9": "first", "10.0.0.10": "second"}
			elm, prx, tp := newAttemptTestProxy(t,
				attemptRoute("obs-retry", attemptPlan(
					[]proxy.Target{attemptURITarget("A", "10.0.0.9")},
				)), opts, loc)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))
			injErr := errors.ErrorWrap("first candidate write failed")
			var failed atomic.Bool
			failed.Store(true)
			tp.sendReqHook = func(call sendReqCall, _ int) error {
				if !call.req.Method().Equal(sip.RequestMethodInvite) {
					return nil
				}
				log.add("send:" + attemptSendNamer(call, names) + "@UDP")
				if failed.CompareAndSwap(true, false) {
					return injErr
				}
				return nil
			}

			admitAttemptReq(ctx, t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".obs-retry", laddr, raddr))
			synctest.Wait()

			atts := rec.kindSeqs(proxy.EventKindForwardAttemptFinished)
			if len(atts) != 2 {
				t.Fatalf("attempt events=%d, want 2", len(atts))
			}
			if atts[0].Error == nil || !errors.Is(atts[0].Error, injErr) {
				t.Fatalf("first attempt error=%v, want %v", atts[0].Error, injErr)
			}
			if atts[1].Error != nil {
				t.Fatalf("second attempt error=%v, want nil", atts[1].Error)
			}
			if atts[0].AttemptID == "" || atts[0].AttemptID == atts[1].AttemptID {
				t.Fatalf("attempt IDs=%q,%q, want distinct", atts[0].AttemptID, atts[1].AttemptID)
			}
			if got := len(rec.kindSeqs(proxy.EventKindRequestForwarded)); got != 1 {
				t.Fatalf("forwarded events=%d, want 1", got)
			}
			_ = elm
		})
	})

	t.Run("size switch settles the generation once", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &evtRecorder{}
			log := &attemptActionLog{}
			var lookups atomic.Int32
			loc := &attemptLocator{
				log:     log,
				lookups: &lookups,
				candidates: map[string][]attemptCandidate{
					"10.0.0.9": {udpAttemptCandidate("a-first", "10.0.0.9:5060")},
				},
			}
			names := map[string]string{"10.0.0.9": "a-first"}
			elm, prx, tp := newAttemptTestProxy(t,
				attemptRoute("obs-switch", attemptPlan(
					[]proxy.Target{attemptURITarget("A", "10.0.0.9")},
				)), opts, loc)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			tcp := newStubTransp()
			tcp.md = sip.TCPMetadata()
			if err := elm.TrackTransport(tcp); err != nil {
				t.Fatalf("elm.TrackTransport(tcp) error = %v, want nil", err)
			}
			tcp.sendReqHook = func(call sendReqCall, _ int) error {
				if call.req.Method().Equal(sip.RequestMethodInvite) {
					log.add("send:" + attemptSendNamer(call, names) + "@TCP")
				}
				return nil
			}
			var tooLarge atomic.Bool
			tooLarge.Store(true)
			tp.sendReqHook = func(call sendReqCall, _ int) error {
				if !call.req.Method().Equal(sip.RequestMethodInvite) {
					return nil
				}
				log.add("send:" + attemptSendNamer(call, names) + "@UDP")
				if tooLarge.CompareAndSwap(true, false) {
					return sip.ErrMessageTooLarge
				}
				return nil
			}

			admitAttemptReq(ctx, t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".obs-switch", laddr, raddr))
			synctest.Wait()

			atts := rec.kindSeqs(proxy.EventKindForwardAttemptFinished)
			if len(atts) != 1 {
				t.Fatalf("attempt events=%d, want 1 (size switch is one logical attempt)", len(atts))
			}
			if atts[0].Error != nil {
				t.Fatalf("attempt error=%v, want nil (switched generation succeeded)", atts[0].Error)
			}
			if got := len(rec.kindSeqs(proxy.EventKindRequestForwarded)); got != 1 {
				t.Fatalf("forwarded events=%d, want 1", got)
			}
		})
	})

	t.Run("timeout and cancellation race report the actual outcome", func(t *testing.T) {
		t.Parallel()

		t.Run("timer C expiry commits timeout", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				rec := &evtRecorder{}
				elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-tmo", obsLitPlan("A")),
					proxy.Options{ServedDomains: []string{"alice.voip.com"}, TimerC: 4 * time.Minute})
				prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

				fwdIntercept(t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".obs-tmo", laddr, raddr))
				synctest.Wait()
				if got := len(drainReqs(tp)); got != 1 {
					t.Fatalf("outbound sends=%d, want 1", got)
				}

				time.Sleep(4*time.Minute + time.Second)
				synctest.Wait()

				compl := rec.kindSeqs(proxy.EventKindRequestCompleted)
				if len(compl) != 1 || compl[0].Outcome != proxy.OutcomeTimeout {
					t.Fatalf("completion=%+v, want timeout", compl)
				}
				if compl[0].StatusCode != http.StatusRequestTimeout {
					t.Fatalf("completion status=%d, want 408 (generated)", compl[0].StatusCode)
				}
				// The generated 408 crossed upstream: its send is observed.
				if got := len(rec.kindSeqs(proxy.EventKindResponseForwarded)); got != 1 {
					t.Fatalf("response events=%d, want 1 (generated 408)", got)
				}
				_ = elm
			})
		})

		t.Run("accepted cancellation does not beat a winning 2xx", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				rec := &evtRecorder{}
				elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-race", obsLitPlan("A")), opts)
				prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

				req := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-race", laddr, raddr)
				fwdIntercept(t, elm, prx, req)
				synctest.Wait()
				sends := drainReqs(tp)
				txA := fwdClnTx(t, elm, sends[0])

				cancel, err := sip.NewCancelRequestEnvelope(req)
				if err != nil {
					t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
				}
				fwdIntercept(t, elm, prx, cancel)
				synctest.Wait()

				if got := len(rec.kindSeqs(proxy.EventKindRequestCompleted)); got != 0 {
					t.Fatalf("completions after bare cancellation=%d, want 0", got)
				}

				// A racing success still commits the response outcome.
				if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusOK)); err != nil {
					t.Fatalf("txA.RecvResponse(200) error = %v, want nil", err)
				}
				synctest.Wait()
				compl := rec.kindSeqs(proxy.EventKindRequestCompleted)
				if len(compl) != 1 || compl[0].Outcome != proxy.OutcomeResponse {
					t.Fatalf("completion=%+v, want response (not cancelled)", compl)
				}
			})
		})
	})

	t.Run("transaction retransmissions emit no attempt observations", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &evtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-retr", obsLitPlan("A")), opts)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-retr", laddr, raddr)
			fwdIntercept(t, elm, prx, req)
			synctest.Wait()
			sends := drainReqs(tp)
			if got := len(sends); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}
			txA := fwdClnTx(t, elm, sends[0])
			if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("txA.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()
			drainRess(tp)

			base := rec.counts()

			// A request retransmission absorbed by the upstream transaction
			// resends the cached response through transaction machinery:
			// neither attempts nor response events move.
			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			srvTx, ok := elm.LoadServerTransaction(srvKey)
			if !ok {
				t.Fatal("upstream transaction not registered")
			}
			if err := srvTx.RecvRequest(ctx, req.Clone().(*sip.RequestEnvelope)); err != nil {
				t.Fatalf("srvTx.RecvRequest() error = %v, want nil", err)
			}
			synctest.Wait()
			drainRess(tp)

			if diff := cmp.Diff(base, rec.counts()); diff != "" {
				t.Fatalf("event counts after retransmission (-want +got): %s", diff)
			}
		})
	})

	t.Run("two stateless traversals are two operations", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			rec := &evtRecorder{}
			route := obsRoute("obs-sl", obsLitPlan("A"))
			route.ForwardMode = proxy.ForwardModeStateless
			elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
				StatelessKey:  []byte("obs-sl-key"),
			})
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-sl", laddr, raddr)
			fwdIntercept(t, elm, prx, req)
			fwdIntercept(t, elm, prx, req.Clone().(*sip.RequestEnvelope))
			synctest.Wait()
			if got := len(drainReqs(tp)); got != 2 {
				t.Fatalf("stateless sends=%d, want 2", got)
			}

			counts := rec.counts()
			if counts[proxy.EventKindRequestHandled] != 2 ||
				counts[proxy.EventKindRequestForwarded] != 2 ||
				counts[proxy.EventKindForwardAttemptFinished] != 2 ||
				counts[proxy.EventKindRequestCompleted] != 2 {
				t.Fatalf("stateless counts=%v, want 2 of each operation event", counts)
			}
			handled := rec.kindSeqs(proxy.EventKindRequestHandled)
			if handled[0].RequestID == "" || handled[0].RequestID == handled[1].RequestID {
				t.Fatalf("stateless request IDs=%q,%q, want distinct per operation",
					handled[0].RequestID, handled[1].RequestID)
			}
			compl := rec.kindSeqs(proxy.EventKindRequestCompleted)
			for _, ev := range compl {
				if ev.Outcome != proxy.OutcomeSent {
					t.Fatalf("stateless completion=%+v, want sent", ev)
				}
			}
		})
	})

	t.Run("stateless pre-send failure emits a failed attempt", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			rec := &evtRecorder{}
			route := &proxy.Route{
				Name:        "obs-slfail",
				ForwardMode: proxy.ForwardModeStateless,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return proxy.TargetPlan{Groups: [][]proxy.Target{{
						{
							ID:  "A",
							URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr("10.0.0.9", 5060)},
							// A delivery reference without a configured
							// resolver fails the destination binding.
							Delivery: proxy.DeliveryRef{Kind: "gw", Key: "x"},
						},
					}}}, nil
				},
			}
			elm, prx, _ := newConfiguredTestProxy(t, route, proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
				StatelessKey:  []byte("obs-sl-key"),
			})
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			// An unmatched CANCEL is never promoted: its binding failure
			// lands in the per-target errors as one failed attempt.
			unseen := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-slfail", laddr, raddr)
			cancel, err := sip.NewCancelRequestEnvelope(unseen)
			if err != nil {
				t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
			}
			if err := prx.InterceptInboundRequest(sip.ContextWithElement(t.Context(), elm), noopNext, cancel); err == nil {
				t.Fatal("prx.InterceptInboundRequest() error = nil, want rejection")
			}
			synctest.Wait()

			counts := rec.counts()
			if counts[proxy.EventKindRequestHandled] != 1 {
				t.Fatalf("handled events=%d, want 1", counts[proxy.EventKindRequestHandled])
			}
			atts := rec.kindSeqs(proxy.EventKindForwardAttemptFinished)
			if len(atts) != 1 {
				t.Fatalf("attempt events=%d, want 1", len(atts))
			}
			if atts[0].Error == nil || atts[0].TargetID != "A" || atts[0].AttemptID != "A" {
				t.Fatalf("attempt event=%+v, want one failed attempt of A", atts[0])
			}
			if counts[proxy.EventKindRequestForwarded] != 0 {
				t.Fatalf("forwarded events=%d, want 0", counts[proxy.EventKindRequestForwarded])
			}
			compl := rec.kindSeqs(proxy.EventKindRequestCompleted)
			if len(compl) != 1 || compl[0].Outcome != proxy.OutcomeRejected ||
				compl[0].StatusCode != 480 {
				t.Fatalf("completion=%+v, want rejected 480", compl)
			}
			// Ordering: Handled, the failed attempt, the completion.
			handled := rec.kindSeqs(proxy.EventKindRequestHandled)[0]
			if atts[0].Sequence <= handled.Sequence || compl[0].Sequence <= atts[0].Sequence {
				t.Fatalf("sequences=%d,%d,%d, want handled < attempt < completion",
					handled.Sequence,
					atts[0].Sequence,
					compl[0].Sequence,
				)
			}
		})
	})

	t.Run("standalone stateless response fabricates no association", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &evtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-slres", obsLitPlan("A")), opts)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			// An inbound response owned by this proxy's stateless path:
			// top Via carries the stateless mark and names this transport,
			// the next Via routes back to the prior hop.
			out := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-slres", laddr, raddr)
			out.WithMessage(func(r *sip.Request) {
				r.Headers.PrependVia(header.ViaHop{
					Proto:     sip.ProtoVer20(),
					Transport: tp.Metadata().Proto,
					Addr:      sip.MakeHostPortAddr("192.0.2.1", 5060),
					Params: make(sip.Values).Set(
						"branch",
						sip.MagicCookie+".proxy-stateless.0123456789abcdef0123456789abcdef.token",
					),
				})
			})
			res := newInRes(t, out, sip.ResponseStatusOK)
			next := sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error {
				return errors.ErrorWrap("next must not be called")
			})
			if err := prx.InterceptInboundResponse(sip.ContextWithElement(ctx, elm), next, res); err != nil {
				t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
			}
			synctest.Wait()

			rf := rec.kindSeqs(proxy.EventKindResponseForwarded)
			if len(rf) != 1 {
				t.Fatalf("response events=%d, want 1", len(rf))
			}
			if rf[0].RequestID != "" || rf[0].RouteName != "" || rf[0].Sequence != 0 {
				t.Fatalf("standalone response event=%+v, want no request/route association", rf[0])
			}
			if rf[0].StatusCode != http.StatusOK || rf[0].Method != sip.RequestMethodInvite {
				t.Fatalf("standalone response event=%+v, want 200 INVITE", rf[0])
			}
		})
	})

	t.Run("ack and unmatched cancel allocate nothing extra", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			rec := &evtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-ack", obsLitPlan("A")), opts)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			// An end-to-end ACK takes the deterministic stateless path as
			// one operation and allocates no transaction or request record.
			inv := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-ack", laddr, raddr)
			res := newInRes(t, inv, sip.ResponseStatusOK)
			ack := newInAckReq(t, inv, res)
			fwdIntercept(t, elm, prx, ack)
			synctest.Wait()
			if got := len(drainReqs(tp)); got != 1 {
				t.Fatalf("ack sends=%d, want 1", got)
			}

			// An unmatched CANCEL forwards statelessly the same way.
			unseen := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-uncancel", laddr, raddr)
			cancel, err := sip.NewCancelRequestEnvelope(unseen)
			if err != nil {
				t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
			}
			fwdIntercept(t, elm, prx, cancel)
			synctest.Wait()
			if got := len(drainReqs(tp)); got != 1 {
				t.Fatalf("cancel sends=%d, want 1", got)
			}

			if got := countAllTxs(elm); got != 0 {
				t.Fatalf("transactions=%d, want 0 (no allocated contexts)", got)
			}
			srvKey, keyErr := sip.MakeServerTransactionKey(unseen)
			if keyErr != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", keyErr)
			}
			if _, ok := prx.LookupRequest(elm, srvKey); ok {
				t.Fatal("unmatched CANCEL left a request record")
			}
			counts := rec.counts()
			if counts[proxy.EventKindRequestHandled] != 2 ||
				counts[proxy.EventKindRequestForwarded] != 2 {
				t.Fatalf("op counts=%v, want 2 handled + 2 forwarded", counts)
			}
		})
	})
}

// TestProxy_RestoreObservations pins the observation contract of a restored
// request: activation replays no historical events, the recorded request
// correlation and dispatch sequence continue past the stored checkpoint,
// only genuinely new effects emit, and transaction-owned retransmissions
// stay silent.
func TestProxy_RestoreObservations(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	opts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}

	t.Run("completed request replays no historical observations", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := obsRoute("rest-obs-done", obsLitPlan("A"))
			elm1, prx1, tp1 := newConfiguredTestProxy(t, route, opts)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".rest-obs-done", laddr, raddr)
			fwdIntercept(t, elm1, prx1, req)
			synctest.Wait()

			sends := drainReqs(tp1)
			if got := len(sends); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}
			txA := fwdClnTx(t, elm1, sends[0])
			if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusNotFound)); err != nil {
				t.Fatalf("txA.RecvResponse(404) error = %v, want nil", err)
			}
			synctest.Wait()
			drainRess(tp1)

			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			rq1, ok := prx1.LookupRequest(elm1, srvKey)
			if !ok {
				t.Fatal("prx1.LookupRequest(elm1, ) found no owned request")
			}
			snap, err := rq1.Snapshot()
			if err != nil {
				t.Fatalf("rq1.Snapshot() error = %v, want nil", err)
			}
			if snap.RequestID == "" || snap.Sequence == 0 {
				t.Fatalf("snapshot correlation=(%q, %d), want a minted request ID and a nonzero sequence",
					snap.RequestID,
					snap.Sequence,
				)
			}
			if !snap.Completed || !snap.CompletionCommitted ||
				!snap.FirstForwardCommitted || !snap.FinalForwarded {
				t.Fatalf("snapshot flags=(done=%t, completion=%t, forward=%t, final=%t), want all committed",
					snap.Completed,
					snap.CompletionCommitted,
					snap.FirstForwardCommitted,
					snap.FinalForwarded,
				)
			}
			attA := attemptOfTarget(snap, "A")
			if attA == nil {
				t.Fatal("snapshot has no attempt record for target A")
			}
			if !attA.InitialSendSettled || !attA.InitialSendSucceeded ||
				attA.InitialSendErrorClass != "" {
				t.Fatalf(
					"attempt A initial send=(settled=%t, succeeded=%t, class=%q), "+
						"want a settled success without a class",
					attA.InitialSendSettled,
					attA.InitialSendSucceeded,
					attA.InitialSendErrorClass,
				)
			}

			// The serialized checkpoint carries detached scalars only: no
			// handlers, counters, contexts, or arbitrary error objects may
			// appear as extra fields.
			raw, err := json.Marshal(snap)
			if err != nil {
				t.Fatalf("json.Marshal(snap) error = %v, want nil", err)
			}
			var rqKeys map[string]json.RawMessage
			if err := json.Unmarshal(raw, &rqKeys); err != nil {
				t.Fatalf("json.Unmarshal(proxy.RequestSnapshot) error = %v, want nil", err)
			}
			wantKeys := []string{
				"version", "revision", "time", "route_name", "mode", "upstream_key",
				"original_request", "working_request", "target_plan", "target_states",
				"attempts", "candidates", "final_forwarded", "completed", "outcome",
				"final_status", "cancelled", "request_id", "sequence",
				"first_forward_committed", "completion_committed",
			}
			slices.Sort(wantKeys)
			if diff := cmp.Diff(wantKeys, slices.Sorted(maps.Keys(rqKeys))); diff != "" {
				t.Fatalf("snapshot fields (-want +got): %s", diff)
			}
			var attKeys []map[string]json.RawMessage
			if err := json.Unmarshal(rqKeys["attempts"], &attKeys); err != nil {
				t.Fatalf("json.Unmarshal(attempts) error = %v, want nil", err)
			}
			if got := len(attKeys); got != 1 {
				t.Fatalf("serialized attempts=%d, want 1", got)
			}
			wantAttKeys := []string{
				"key", "target_id", "request", "addr", "state", "response",
				"completed", "cancelled", "deferred_cancel",
				"initial_send_settled", "initial_send_succeeded",
			}
			slices.Sort(wantAttKeys)
			if diff := cmp.Diff(wantAttKeys, slices.Sorted(maps.Keys(attKeys[0]))); diff != "" {
				t.Fatalf("attempt fields (-want +got): %s", diff)
			}

			srvTx1, ok := elm1.LoadServerTransaction(srvKey)
			if !ok {
				t.Fatal("upstream transaction not registered")
			}
			srvSnapper, ok := srvTx1.(sip.ServerTransactionSnapshotter)
			if !ok {
				t.Fatal("upstream transaction does not implement sip.ServerTransactionSnapshotter")
			}
			srvSnap := mustSrvTxSnapshot(t, srvSnapper)

			elm2, prx2, tp2 := newConfiguredTestProxy(t, route, opts)
			rec := &evtRecorder{}
			prx2.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			srvTx2, err := sip.RestoreServerTransaction(srvSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
			}
			rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, proxy.RestoreBindings{
				Element:           elm2,
				ServerTransaction: srvTx2,
			})
			if err != nil {
				t.Fatalf("prx2.RestoreRequest() error = %v, want nil", err)
			}
			if err := rq2.Start(ctx); err != nil {
				t.Fatalf("rq2.Start() error = %v, want nil", err)
			}

			// Even the restored transaction-owned resend of the stored 404
			// produces no observation: advance past its resend timer.
			time.Sleep(time.Second)
			synctest.Wait()
			drainReqs(tp2)
			drainRess(tp2)

			if got := len(rec.get()); got != 0 {
				t.Fatalf("post-activation events=%d, want 0 (no historical replay)", got)
			}
		})
	})

	t.Run("live attempts keep correlation, tx retransmissions stay silent", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := obsRoute("rest-obs", obsLitPlan("A", "B", "C"))
			elm1, prx1, tp1 := newConfiguredTestProxy(t, route, opts)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".rest-obs", laddr, raddr)
			fwdIntercept(t, elm1, prx1, req)
			synctest.Wait()

			targetHosts := map[string]string{"10.0.0.9": "A", "10.0.0.10": "B", "10.0.0.11": "C"}
			targetOf := func(req *sip.RequestEnvelope) string {
				if u, ok := req.URI().(*sip.URI); ok {
					return targetHosts[u.Addr.Host()]
				}
				return ""
			}
			sends := make(map[string]*sip.RequestEnvelope)
			for _, out := range drainReqs(tp1) {
				if id := targetOf(out); id != "" {
					sends[id] = out
				}
			}
			if len(sends) != 3 {
				t.Fatalf("outbound branch sends=%d, want targets A, B and C", len(sends))
			}

			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			rq1, ok := prx1.LookupRequest(elm1, srvKey)
			if !ok {
				t.Fatal("prx1.LookupRequest(elm1, ) found no owned request")
			}
			snap, err := rq1.Snapshot()
			if err != nil {
				t.Fatalf("rq1.Snapshot() error = %v, want nil", err)
			}
			if snap.RequestID == "" || snap.Sequence == 0 {
				t.Fatalf("snapshot correlation=(%q, %d), want a minted request ID and a nonzero sequence",
					snap.RequestID,
					snap.Sequence,
				)
			}
			if !snap.FirstForwardCommitted || snap.CompletionCommitted || snap.Completed {
				t.Fatalf(
					"snapshot flags=(forward=%t, completion=%t, done=%t), "+
						"want a committed forward and an open request",
					snap.FirstForwardCommitted,
					snap.CompletionCommitted,
					snap.Completed,
				)
			}
			for _, id := range []string{"A", "B", "C"} {
				att := attemptOfTarget(snap, id)
				if att == nil || !att.InitialSendSettled || !att.InitialSendSucceeded ||
					att.InitialSendErrorClass != "" {
					t.Fatalf("attempt %s initial send=%+v, want a settled success", id, att)
				}
			}

			// Every live attempt needs its dormant transaction binding.
			clnSnaps := make(map[sip.ClientTransactionKey]*sip.ClientTransactionSnapshot, len(sends))
			for id, out := range sends {
				key, err := sip.MakeClientTransactionKey(out)
				if err != nil {
					t.Fatalf("sip.MakeClientTransactionKey(%s) error = %v, want nil", id, err)
				}
				tx, ok := elm1.LoadClientTransaction(key)
				if !ok {
					t.Fatalf("client transaction of target %s not registered", id)
				}
				snapper, ok := tx.(sip.ClientTransactionSnapshotter)
				if !ok {
					t.Fatalf("client transaction of target %s does not implement sip.ClientTransactionSnapshotter", id)
				}
				clnSnaps[key] = mustClnTxSnapshot(t, snapper)
			}
			srvTx1, ok := elm1.LoadServerTransaction(srvKey)
			if !ok {
				t.Fatal("upstream transaction not registered")
			}
			srvSnapper, ok := srvTx1.(sip.ServerTransactionSnapshotter)
			if !ok {
				t.Fatal("upstream transaction does not implement sip.ServerTransactionSnapshotter")
			}
			srvSnap := mustSrvTxSnapshot(t, srvSnapper)

			elm2, prx2, tp2 := newConfiguredTestProxy(t, route, opts)
			rec := &evtRecorder{}
			prx2.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			srvTx2, err := sip.RestoreServerTransaction(srvSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
			}
			bindings := proxy.RestoreBindings{
				Element:            elm2,
				ServerTransaction:  srvTx2,
				ClientTransactions: make(map[sip.ClientTransactionKey]sip.ClientTransaction, len(clnSnaps)),
			}
			for key, clnSnap := range clnSnaps {
				clnTx, err := sip.RestoreClientTransaction(clnSnap, tp2)
				if err != nil {
					t.Fatalf("sip.RestoreClientTransaction(%v) error = %v, want nil", key, err)
				}
				bindings.ClientTransactions[key] = clnTx
			}
			rq2, err := prx2.RestoreRequest(sip.ContextWithElement(ctx, elm2), snap, bindings)
			if err != nil {
				t.Fatalf("prx2.RestoreRequest() error = %v, want nil", err)
			}
			clnA := bindings.ClientTransactions[mustClnTxKey(t, sends["A"])]
			clnB := bindings.ClientTransactions[mustClnTxKey(t, sends["B"])]
			clnC := bindings.ClientTransactions[mustClnTxKey(t, sends["C"])]
			if err := rq2.Start(ctx); err != nil {
				t.Fatalf("rq2.Start() error = %v, want nil", err)
			}
			synctest.Wait()

			if got := len(rec.get()); got != 0 {
				t.Fatalf("activation events=%d, want 0 (nothing replayed)", got)
			}

			// The restored transaction-owned retransmission timers fire on
			// the wire but produce no attempt observations.
			time.Sleep(time.Second)
			synctest.Wait()
			if got := len(drainReqs(tp2)); got == 0 {
				t.Fatal("restored transactions never retransmitted the INVITE")
			}
			if got := len(rec.get()); got != 0 {
				t.Fatalf("retransmission events=%d, want 0", got)
			}

			// A newly eligible INVITE 2xx forwards and is observed with the
			// preserved correlation and a sequence continuing past the
			// stored checkpoint value — no historical kind replays.
			if err := clnB.RecvResponse(ctx, newInRes(t, clnB.Request(), sip.ResponseStatusOK)); err != nil {
				t.Fatalf("clnB.RecvResponse(200) error = %v, want nil", err)
			}
			synctest.Wait()
			kindsAfterActivation := make([]proxy.EventKind, 0, len(rec.get()))
			for _, ev := range rec.get() {
				kindsAfterActivation = append(kindsAfterActivation, ev.Kind)
			}
			want := []proxy.EventKind{proxy.EventKindResponseForwarded}
			if diff := cmp.Diff(want, kindsAfterActivation); diff != "" {
				t.Fatalf("post-activation kinds (-want +got): %s", diff)
			}
			ev := rec.get()[0]
			if ev.RequestID != snap.RequestID {
				t.Fatalf("response event request ID=%q, want the restored %q", ev.RequestID, snap.RequestID)
			}
			if ev.Sequence <= snap.Sequence {
				t.Fatalf("response event sequence=%d, want > %d", ev.Sequence, snap.Sequence)
			}
			if ev.StatusCode != http.StatusOK || ev.TargetID != "B" {
				t.Fatalf("response event=%+v, want the 200 of target B", ev)
			}

			// A retransmitted eligible 2xx arriving on the still-live
			// sibling is observed the same way: ResponseForwarded, and
			// still nothing historical.
			if err := clnA.RecvResponse(ctx, newInRes(t, clnA.Request(), sip.ResponseStatusOK)); err != nil {
				t.Fatalf("clnA.RecvResponse(200) error = %v, want nil", err)
			}
			synctest.Wait()
			evs := rec.get()
			if got := len(evs); got != 2 {
				t.Fatalf("post-activation events=%d, want 2", got)
			}
			if evs[1].Kind != proxy.EventKindResponseForwarded ||
				evs[1].Sequence <= evs[0].Sequence ||
				evs[1].RequestID != snap.RequestID {
				t.Fatalf("retransmitted response event=%+v, want a correlated ResponseForwarded past %d",
					evs[1],
					evs[0].Sequence,
				)
			}
			counts := rec.counts()
			if counts[proxy.EventKindRequestHandled] != 0 ||
				counts[proxy.EventKindRequestForwarded] != 0 ||
				counts[proxy.EventKindForwardAttemptFinished] != 0 ||
				counts[proxy.EventKindRequestCompleted] != 0 {
				t.Fatalf("replayed counts=%v, want only ResponseForwarded events", counts)
			}

			// Settling the last live sibling commits a genuinely new
			// decision: its completion emits normally instead of replaying
			// anything historical.
			if err := clnC.RecvResponse(ctx, newInRes(t, clnC.Request(), sip.ResponseStatusBusyHere)); err != nil {
				t.Fatalf("clnC.RecvResponse(486) error = %v, want nil", err)
			}
			synctest.Wait()
			compl := rec.kindSeqs(proxy.EventKindRequestCompleted)
			if len(compl) != 1 || compl[0].Outcome != proxy.OutcomeResponse ||
				compl[0].RequestID != snap.RequestID || compl[0].Sequence <= snap.Sequence {
				t.Fatalf("completion=%+v, want one new correlated response completion", compl)
			}
			drainRess(tp2)
		})
	})
}

// mustClnTxKey returns the client transaction key of one captured outbound request.
func mustClnTxKey(tb testing.TB, req *sip.RequestEnvelope) sip.ClientTransactionKey {
	tb.Helper()

	key, err := sip.MakeClientTransactionKey(req)
	if err != nil {
		tb.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
	}
	return key
}

// obsCtxKey is the marker context key used to verify that unrelated
// caller values survive the dispatch-context element stamping.
type obsCtxKey struct{}

// obsCtx is the dispatch context observed by one handler invocation.
type obsCtx struct {
	elm    *sip.Element
	hasElm bool
	marker any
}

// ctxSpy records the dispatch context of every event and snapshot
// handler invocation.
type ctxSpy struct {
	mu    sync.Mutex
	evts  []obsCtx
	snaps []obsCtx
}

func (s *ctxSpy) record(ctx context.Context) obsCtx {
	elm, ok := sip.ElementFromContext(ctx)
	return obsCtx{elm: elm, hasElm: ok, marker: ctx.Value(obsCtxKey{})}
}

func (s *ctxSpy) HandleEvent(ctx context.Context, _ proxy.Event) {
	rec := s.record(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evts = append(s.evts, rec)
}

func (s *ctxSpy) HandleRequestSnapshot(ctx context.Context, _ *proxy.RequestSnapshot) {
	rec := s.record(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snaps = append(s.snaps, rec)
}

func (s *ctxSpy) get() (evts, snaps []obsCtx) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.evts), slices.Clone(s.snaps)
}

// txCtxSpy records the dispatch context of transaction start and state
// callbacks — the transaction effects a dispatch source drives.
type txCtxSpy struct {
	mu   sync.Mutex
	ctxs []obsCtx
}

func (s *txCtxSpy) record(ctx context.Context) {
	rec := obsCtx{marker: ctx.Value(obsCtxKey{})}
	rec.elm, rec.hasElm = sip.ElementFromContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctxs = append(s.ctxs, rec)
}

func (s *txCtxSpy) get() []obsCtx {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.ctxs)
}

// checkObsCtxs asserts every recorded dispatch context carried owner as its
// processing element and preserved the unrelated caller value.
func checkObsCtxs(tb testing.TB, what string, ctxs []obsCtx, owner *sip.Element, marker any) {
	tb.Helper()
	for i, c := range ctxs {
		if !c.hasElm || c.elm != owner {
			tb.Errorf("%s context %d element = (%p, %t), want owner %p present", what, i, c.elm, c.hasElm, owner)
		}
		if c.marker != marker {
			tb.Errorf("%s context %d marker = %v, want %v preserved", what, i, c.marker, marker)
		}
	}
}

// TestProxy_ObserverElementContext pins the dispatch-context contract of
// [Proxy.BindEventHandler] and [Proxy.BindSnapshotHandler]: every handler
// invocation and every asynchronous effect sees the request's own immutable
// element through [sip.ElementFromContext] — stamped immediately before
// dispatch even when the triggering transient context is empty or carries a
// conflicting element — while neither payload type gains an element field.
func TestProxy_ObserverElementContext(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	opts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}
	noopNext := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil })

	// foreignElm returns a bare element used to stamp deliberately
	// conflicting transient contexts.
	foreignElm := func(tb testing.TB) *sip.Element {
		tb.Helper()
		elm := &sip.Element{}
		tb.Cleanup(func() { elm.Close() })
		return elm
	}
	mark := func(ctx context.Context, v string) context.Context {
		return context.WithValue(ctx, obsCtxKey{}, v)
	}

	t.Run("admission", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-elm-adm", obsLitPlan("A")), opts)
			spy := &ctxSpy{}
			prx.BindEventHandler(spy)
			prx.BindSnapshotHandler(spy)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-elm-adm", laddr, raddr)
			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}

			// A reentrant observer inspecting public state and unbinding
			// itself mid-dispatch must not deadlock: dispatch holds no
			// coordinator, transaction, or registry lock.
			var (
				unbindSelf func()
				reent      atomic.Bool
			)
			self := proxy.EventHandlerFunc(func(context.Context, proxy.Event) {
				if reent.Swap(true) {
					return
				}
				if _, ok := prx.LookupRequest(elm, srvKey); !ok {
					t.Error("reentrant LookupRequest found no admitted request")
				}
				unbindSelf()
				unbindSelf() // idempotent
			})
			unbindSelf = prx.BindEventHandler(self)

			inCtx := mark(sip.ContextWithElement(ctx, elm), "in")
			if err := prx.InterceptInboundRequest(inCtx, noopNext, req); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()
			if got := len(drainReqs(tp)); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}

			evts, snaps := spy.get()
			if len(evts) == 0 {
				t.Fatal("admission emitted no events")
			}
			if len(snaps) == 0 {
				t.Fatal("admission published no snapshot revision")
			}
			checkObsCtxs(t, "event", evts, elm, "in")
			checkObsCtxs(t, "snapshot", snaps, elm, "in")
			if !reent.Load() {
				t.Error("reentrant observer never dispatched")
			}
		})
	})

	t.Run("early rejection answering send", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := obsRoute("obs-elm-rej", obsLitPlan("A"))
			route.AuthorizeRequest = func(context.Context, *sip.RequestEnvelope) error {
				return sip.NewRequestRejectedError(
					errors.ErrorWrap("policy denied"), 0, sip.ResponseStatusForbidden,
				)
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, opts)
			spy := &ctxSpy{}
			prx.BindEventHandler(spy)
			prx.BindSnapshotHandler(spy)
			elmB := foreignElm(t)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-elm-rej", laddr, raddr)
			inCtx := mark(sip.ContextWithElement(ctx, elm), "in")
			if err := prx.InterceptInboundRequest(inCtx, noopNext, req); err == nil {
				t.Fatal("prx.InterceptInboundRequest() error = nil, want rejection")
			}
			synctest.Wait()

			// The transport answers the rejection through the outbound
			// response path under a deliberately conflicting caller
			// context: the element's own send chain rebinds the owning
			// element, and the pending record closes the operation's
			// stream under it.
			res, resErr := req.NewResponse(sip.ResponseStatusForbidden)
			if resErr != nil {
				t.Fatalf("req.NewResponse() error = %v, want nil", resErr)
			}
			sendCtx := mark(sip.ContextWithElement(ctx, elmB), "send")
			if err := tp.SendResponse(sendCtx, res); err != nil {
				t.Fatalf("tp.SendResponse() error = %v, want nil", err)
			}
			synctest.Wait()

			evts, _ := spy.get()
			if got := len(evts); got < 3 {
				t.Fatalf("rejection events=%d, want at least 3 (handled, completed, response)", got)
			}
			checkObsCtxs(t, "event", evts[:len(evts)-1], elm, "in")
			checkObsCtxs(t, "answering send event", evts[len(evts)-1:], elm, "send")
		})
	})

	t.Run("stateless response", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			elm, prx, _ := newConfiguredTestProxy(t, obsRoute("obs-elm-slres", obsLitPlan("A")), opts)
			spy := &ctxSpy{}
			prx.BindEventHandler(spy)
			prx.BindSnapshotHandler(spy)

			// An inbound response owned by this proxy's stateless path:
			// top Via carries the stateless mark and names this transport,
			// the next Via routes back to the prior hop.
			out := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-elm-slres", laddr, raddr)
			out.WithMessage(func(r *sip.Request) {
				r.Headers.PrependVia(header.ViaHop{
					Proto:     sip.ProtoVer20(),
					Transport: "UDP",
					Addr:      sip.MakeHostPortAddr("192.0.2.1", 5060),
					Params: make(sip.Values).Set(
						"branch",
						sip.MagicCookie+".proxy-stateless.0123456789abcdef0123456789abcdef.token",
					),
				})
			})
			res := newInRes(t, out, sip.ResponseStatusOK)
			next := sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error {
				return errors.ErrorWrap("next must not be called")
			})

			inCtx := mark(sip.ContextWithElement(ctx, elm), "in")
			if err := prx.InterceptInboundResponse(inCtx, next, res); err != nil {
				t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
			}
			synctest.Wait()

			evts, snaps := spy.get()
			if len(evts) != 1 {
				t.Fatalf("response events=%d, want 1", len(evts))
			}
			checkObsCtxs(t, "event", evts, elm, "in")
			if len(snaps) != 0 {
				t.Fatalf("standalone response published %d snapshots, want 0", len(snaps))
			}
		})
	})

	t.Run("queue drain", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-elm-drain", obsLitPlan("A")), opts)
			spy := &ctxSpy{}
			prx.BindEventHandler(spy)
			prx.BindSnapshotHandler(spy)
			elmB := foreignElm(t)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-elm-drain", laddr, raddr)
			inCtx := mark(sip.ContextWithElement(ctx, elm), "in")
			if err := prx.InterceptInboundRequest(inCtx, noopNext, req); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()
			sends := drainReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("outbound sends=%d, want 1", len(sends))
			}
			clnTx := fwdClnTx(t, elm, sends[0])

			// A downstream response arriving under a conflicting transient
			// callback context still drains under the request's own stable
			// element and admission-context values.
			conflict := mark(sip.ContextWithElement(ctx, elmB), "conflict")
			if err := clnTx.RecvResponse(conflict, newInRes(t, sends[0], sip.ResponseStatusNotFound)); err != nil {
				t.Fatalf("clnTx.RecvResponse(404) error = %v, want nil", err)
			}
			synctest.Wait()

			evts, snaps := spy.get()
			if len(evts) == 0 || len(snaps) == 0 {
				t.Fatalf("drain dispatch=(%d events, %d snapshots), want both nonzero", len(evts), len(snaps))
			}
			checkObsCtxs(t, "event", evts, elm, "in")
			checkObsCtxs(t, "snapshot", snaps, elm, "in")
		})
	})

	t.Run("timer c", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			tmrOpts := proxy.Options{
				ServedDomains: []string{"alice.voip.com"},
				TimerC:        4 * time.Minute,
			}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-elm-tmrc", obsLitPlan("A")), tmrOpts)
			spy := &ctxSpy{}
			prx.BindEventHandler(spy)
			prx.BindSnapshotHandler(spy)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-elm-tmrc", laddr, raddr)
			inCtx := mark(sip.ContextWithElement(ctx, elm), "in")
			if err := prx.InterceptInboundRequest(inCtx, noopNext, req); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()
			if got := len(drainReqs(tp)); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}

			// The armed deadline fires from the runtime timer: its expiry
			// path dispatches under the request's own element.
			time.Sleep(4*time.Minute + time.Second)
			synctest.Wait()

			evts, snaps := spy.get()
			compl := 0
			for _, ev := range evts {
				if ev.elm == elm {
					compl++
				}
			}
			if compl == 0 {
				t.Fatal("timer expiry produced no events")
			}
			checkObsCtxs(t, "event", evts, elm, "in")
			checkObsCtxs(t, "snapshot", snaps, elm, "in")
		})
	})

	t.Run("cleanup", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-elm-shut", obsLitPlan("A")), opts)
			spy := &ctxSpy{}
			prx.BindEventHandler(spy)
			prx.BindSnapshotHandler(spy)
			elmB := foreignElm(t)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-elm-shut", laddr, raddr)
			inCtx := mark(sip.ContextWithElement(ctx, elm), "in")
			if err := prx.InterceptInboundRequest(inCtx, noopNext, req); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()
			sends := drainReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("outbound sends=%d, want 1", len(sends))
			}

			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			srvTx, ok := elm.LoadServerTransaction(srvKey)
			if !ok {
				t.Fatal("upstream transaction not registered")
			}
			clnTx := fwdClnTx(t, elm, sends[0])

			// Lifecycle retirement terminates the owned transactions: the
			// callbacks they still fire see the request's own element even
			// when the lifecycle context carries a conflicting one.
			srvSpy := &txCtxSpy{}
			srvTx.BindStateHandler(sip.TransactionStateHandlerFunc(
				func(hctx context.Context, _, _ sip.TransactionState) { srvSpy.record(hctx) },
			))
			clnSpy := &txCtxSpy{}
			clnTx.BindStateHandler(sip.TransactionStateHandlerFunc(
				func(hctx context.Context, _, _ sip.TransactionState) { clnSpy.record(hctx) },
			))

			evtsBefore, snapsBefore := spy.get()
			shutCtx := mark(sip.ContextWithElement(ctx, elmB), "shutdown")
			if err := prx.Shutdown(shutCtx); err != nil {
				t.Fatalf("prx.Shutdown() error = %v, want nil", err)
			}
			synctest.Wait()

			if got := srvSpy.get(); len(got) == 0 {
				t.Fatal("upstream transaction retirement fired no state callback")
			}
			if got := clnSpy.get(); len(got) == 0 {
				t.Fatal("branch transaction retirement fired no state callback")
			}
			checkObsCtxs(t, "server transaction state", srvSpy.get(), elm, "shutdown")
			checkObsCtxs(t, "client transaction state", clnSpy.get(), elm, "shutdown")

			// Observer subscriptions detach before coordinators retire:
			// cleanup dispatches no further event or snapshot records.
			evts, snaps := spy.get()
			if len(evts) != len(evtsBefore) || len(snaps) != len(snapsBefore) {
				t.Fatalf("observer calls during shutdown=(%d events, %d snapshots), want unchanged (%d, %d)",
					len(evts), len(snaps), len(evtsBefore), len(snapsBefore))
			}
		})
	})

	t.Run("restored activation", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := obsRoute("obs-elm-rest", obsLitPlan("A"))
			elm1, prx1, tp1 := newConfiguredTestProxy(t, route, opts)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-elm-rest", laddr, raddr)
			fwdIntercept(t, elm1, prx1, req)
			synctest.Wait()
			sends := drainReqs(tp1)
			if len(sends) != 1 {
				t.Fatalf("outbound sends=%d, want 1", len(sends))
			}
			clnTx1 := fwdClnTx(t, elm1, sends[0])
			clnSnapper, ok := clnTx1.(sip.ClientTransactionSnapshotter)
			if !ok {
				t.Fatal("client transaction does not implement sip.ClientTransactionSnapshotter")
			}
			clnSnap := mustClnTxSnapshot(t, clnSnapper)

			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			rq1, ok := prx1.LookupRequest(elm1, srvKey)
			if !ok {
				t.Fatal("prx1.LookupRequest(elm1, ) found no owned request")
			}
			snap, err := rq1.Snapshot()
			if err != nil {
				t.Fatalf("rq1.Snapshot() error = %v, want nil", err)
			}
			srvTx1, ok := elm1.LoadServerTransaction(srvKey)
			if !ok {
				t.Fatal("upstream transaction not registered")
			}
			srvSnapper, ok := srvTx1.(sip.ServerTransactionSnapshotter)
			if !ok {
				t.Fatal("upstream transaction does not implement sip.ServerTransactionSnapshotter")
			}
			srvSnap := mustSrvTxSnapshot(t, srvSnapper)

			// The restored request binds explicitly to elm2; activation
			// under a conflicting caller context must still dispatch and
			// start its transactions under the bound element.
			elm2, prx2, tp2 := newConfiguredTestProxy(t, route, opts)
			spy := &ctxSpy{}
			prx2.BindEventHandler(spy)
			prx2.BindSnapshotHandler(spy)
			elmB := foreignElm(t)

			srvTx2, err := sip.RestoreServerTransaction(srvSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
			}
			clnTx2, err := sip.RestoreClientTransaction(clnSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
			}
			restCtx := mark(sip.ContextWithElement(ctx, elm2), "restore")
			rq2, err := prx2.RestoreRequest(restCtx, snap, proxy.RestoreBindings{
				Element:           elm2,
				ServerTransaction: srvTx2,
				ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{
					mustClnTxKey(t, sends[0]): clnTx2,
				},
			})
			if err != nil {
				t.Fatalf("prx2.RestoreRequest() error = %v, want nil", err)
			}

			// The transaction effects of activation are the restored
			// transaction starts: their callbacks observe the bound
			// element, never the caller's conflicting one.
			startSpy := &txCtxSpy{}
			srvTx2.BindStartHandler(sip.TransactionStartHandlerFunc(startSpy.record))
			srvTx2.BindStateHandler(sip.TransactionStateHandlerFunc(
				func(hctx context.Context, _, _ sip.TransactionState) { startSpy.record(hctx) },
			))
			clnTx2.BindStartHandler(sip.TransactionStartHandlerFunc(startSpy.record))
			clnTx2.BindStateHandler(sip.TransactionStateHandlerFunc(
				func(hctx context.Context, _, _ sip.TransactionState) { startSpy.record(hctx) },
			))

			startCtx := mark(sip.ContextWithElement(ctx, elmB), "start")
			if err := rq2.Start(startCtx); err != nil {
				t.Fatalf("rq2.Start() error = %v, want nil", err)
			}
			synctest.Wait()

			starts := startSpy.get()
			if len(starts) == 0 {
				t.Fatal("activation fired no transaction start/state callback")
			}
			checkObsCtxs(t, "transaction effect", starts, elm2, "start")

			// Activation drain dispatches under the restore context's
			// element and preserved values.
			evts, snaps := spy.get()
			checkObsCtxs(t, "event", evts, elm2, "restore")
			checkObsCtxs(t, "snapshot", snaps, elm2, "restore")
		})
	})

	t.Run("payloads carry no element field", func(t *testing.T) {
		t.Parallel()

		// JSON encoding: neither payload serializes an element key.
		for _, tt := range []struct {
			name string
			v    any
		}{
			{"proxy.Event", proxy.Event{
				Kind: proxy.EventKindRequestHandled, RequestID: "rq-1",
				RouteName: "route-a", Mode: proxy.ForwardModeStateful,
				Method: sip.RequestMethodInvite,
			}},
			{"proxy.RequestSnapshot", proxy.RequestSnapshot{Version: 1, RouteName: "route-a"}},
		} {
			raw, err := json.Marshal(tt.v)
			if err != nil {
				t.Fatalf("json.Marshal(%s) error = %v, want nil", tt.name, err)
			}
			var keys map[string]json.RawMessage
			if err := json.Unmarshal(raw, &keys); err != nil {
				t.Fatalf("json.Unmarshal(%s) error = %v, want nil", tt.name, err)
			}
			for k := range keys {
				if strings.Contains(strings.ToLower(k), "element") {
					t.Errorf("%s serialized element key %q", tt.name, k)
				}
			}
		}

		// Reflection: no payload type embeds the element, not even behind
		// a json:"-" tag.
		elmType := reflect.TypeFor[*sip.Element]()
		for _, typ := range []reflect.Type{
			reflect.TypeFor[proxy.Event](),
			reflect.TypeFor[proxy.RequestSnapshot](),
			reflect.TypeFor[proxy.TargetSnapshot](),
			reflect.TypeFor[proxy.AttemptSnapshot](),
		} {
			for f := range typ.Fields() {
				if f.Type == elmType {
					t.Errorf("%s.%s is an element field", typ.Name(), f.Name)
				}
			}
		}
	})
}
