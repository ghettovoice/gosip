package sip_test

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// prxEvtRecorder captures every emitted [sip.ProxyEvent] for assertions.
type prxEvtRecorder struct {
	mu     sync.Mutex
	events []sip.ProxyEvent
}

func (r *prxEvtRecorder) record(_ context.Context, ev sip.ProxyEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *prxEvtRecorder) get() []sip.ProxyEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

func (r *prxEvtRecorder) counts() map[sip.ProxyEventKind]int {
	counts := make(map[sip.ProxyEventKind]int)
	for _, ev := range r.get() {
		counts[ev.Kind]++
	}
	return counts
}

func (r *prxEvtRecorder) kindSeqs(kind sip.ProxyEventKind) []sip.ProxyEvent {
	var out []sip.ProxyEvent
	for _, ev := range r.get() {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

// obsLitPlan builds one flat fork plan of literal-IP targets.
func obsLitPlan(ids ...string) sip.ProxyTargetPlan {
	hosts := map[string]string{
		"A": "10.0.0.9", "B": "10.0.0.10", "C": "10.0.0.11",
	}
	targets := make([]sip.ProxyTarget, 0, len(ids))
	for _, id := range ids {
		targets = append(targets, sip.ProxyTarget{
			ID:  id,
			URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr(hosts[id], 5060)},
		})
	}
	return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{targets}}
}

func obsRoute(name string, plan sip.ProxyTargetPlan) *sip.ProxyRoute {
	return &sip.ProxyRoute{
		Name:        name,
		ForwardMode: sip.ForwardModeStateful,
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
			return plan, nil
		},
	}
}

func TestProxy_promotion_observations(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		f := newPrxPromotionFixture()
		elm, prx, tp := newConfiguredTestProxy(t, f.route, f.opts, &sip.Element{
			ServerLocator: statelessFixedLocator{addr: netip.MustParseAddrPort("198.51.100.77:5060")},
		})
		rec := &prxEvtRecorder{}
		stats := &sip.StatsRecorder{}
		prx.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))
		prx.BindEventHandler(stats)
		before := stats.Report().Proxy
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".promotion-observations",
			netip.MustParseAddrPort("192.168.1.100:5060"), netip.MustParseAddrPort("10.0.0.5:5060"))
		fwdIntercept(t, prx, req)
		synctest.Wait()
		sends := drainPrxReqs(tp)
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
		upstream := drainPrxRess(tp)
		counts := rec.counts()
		wantCounts := map[sip.ProxyEventKind]int{
			sip.ProxyEventRequestHandled:         1,
			sip.ProxyEventForwardAttemptFinished: 1,
			sip.ProxyEventRequestForwarded:       1,
			sip.ProxyEventRequestCompleted:       1,
			sip.ProxyEventResponseForwarded:      len(upstream),
		}
		if diff := cmp.Diff(wantCounts, counts); diff != "" {
			t.Fatalf("promotion observations (-want +got): %s", diff)
		}
		events := rec.get()
		if events[0].Kind != sip.ProxyEventRequestHandled || events[0].RequestID == "" {
			t.Fatalf("first observation = %+v, want handled with request ID", events[0])
		}
		for i, ev := range events {
			if ev.Mode != sip.ForwardModeStateful || ev.RequestID != events[0].RequestID ||
				ev.Sequence != uint64(i+1) || ev.Error != nil {
				t.Fatalf("promotion event %d = %+v, want one stateful stream with unique monotonic sequences and no failed attempt", i, ev)
			}
		}
		if len(upstream) == 0 || counts[sip.ProxyEventResponseForwarded] != len(upstream) {
			t.Fatal("response observations do not match actual upstream sends")
		}
		wantStats := before
		wantStats.RequestsHandled++
		wantStats.ForwardAttemptsSucceeded++
		wantStats.RequestsForwarded++
		wantStats.RequestsCompleted++
		wantStats.ResponsesForwarded += uint64(len(upstream))
		if diff := cmp.Diff(wantStats, stats.Report().Proxy); diff != "" {
			t.Fatalf("promotion StatsRecorder delta (-want +got): %s", diff)
		}
	})
}

func TestProxy_ObservationBoundaries(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	opts := sip.ProxyOptions{ServedDomains: []string{"alice.voip.com"}}
	noopNext := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil })

	t.Run("handled once at winning admission, absorbed duplicate emits nothing", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &prxEvtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-dedup", obsLitPlan("A")), opts)
			prx.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-dedup", laddr, raddr)
			fwdIntercept(t, prx, req)
			synctest.Wait()
			if got := len(drainPrxReqs(tp)); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}

			dup := req.Clone().(*sip.RequestEnvelope)
			if err := prx.InterceptInboundRequest(ctx, noopNext, dup); err != nil {
				t.Fatalf("duplicate prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			counts := rec.counts()
			if counts[sip.ProxyEventRequestHandled] != 1 {
				t.Fatalf("handled events=%d, want 1", counts[sip.ProxyEventRequestHandled])
			}
			if counts[sip.ProxyEventForwardAttemptFinished] != 1 ||
				counts[sip.ProxyEventRequestForwarded] != 1 {
				t.Fatalf("counts=%v, want one attempt and one forward", counts)
			}
			if got := len(rec.get()); got != 3 {
				// handled + finished attempt + forwarded, nothing more.
				t.Fatalf("total events=%d, want 3", got)
			}
			handled := rec.kindSeqs(sip.ProxyEventRequestHandled)[0]
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
			rec := &prxEvtRecorder{}
			route := obsRoute("obs-reject", obsLitPlan("A"))
			route.AuthorizeRequest = func(context.Context, *sip.RequestEnvelope) error {
				return sip.NewRequestRejectedError(
					errors.ErrorWrap("policy denied"), 0, sip.ResponseStatusForbidden,
				)
			}
			_, prx, tp := newConfiguredTestProxy(t, route, opts)
			prx.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-reject", laddr, raddr)
			err := prx.InterceptInboundRequest(ctx, noopNext, req)
			if err == nil {
				t.Fatal("prx.InterceptInboundRequest() error = nil, want rejection")
			}
			synctest.Wait()

			counts := rec.counts()
			if counts[sip.ProxyEventRequestHandled] != 1 ||
				counts[sip.ProxyEventRequestCompleted] != 1 {
				t.Fatalf("rejection events=%v, want handled=1 completed=1", counts)
			}
			compl := rec.kindSeqs(sip.ProxyEventRequestCompleted)[0]
			if compl.Outcome != sip.ProxyOutcomeRejected || compl.StatusCode != http.StatusForbidden {
				t.Fatalf("completion=%+v, want rejected 403", compl)
			}
			if counts[sip.ProxyEventResponseForwarded] != 0 {
				t.Fatalf("response events before send=%d, want 0", counts[sip.ProxyEventResponseForwarded])
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
			rf := rec.kindSeqs(sip.ProxyEventResponseForwarded)
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
			rec := &prxEvtRecorder{}
			_, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-fail", obsLitPlan("A")), opts)
			prx.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))
			injErr := errors.ErrorWrap("udp write failed")
			tp.sendReqHook = func(sendReqCall, int) error { return injErr }

			fwdIntercept(t, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".obs-fail", laddr, raddr))
			synctest.Wait()

			counts := rec.counts()
			if counts[sip.ProxyEventForwardAttemptFinished] != 1 {
				t.Fatalf("attempt events=%d, want 1", counts[sip.ProxyEventForwardAttemptFinished])
			}
			att := rec.kindSeqs(sip.ProxyEventForwardAttemptFinished)[0]
			if att.Error == nil || att.TargetID != "A" || att.AttemptID == "" {
				t.Fatalf("attempt event=%+v, want failed attempt of A", att)
			}
			if counts[sip.ProxyEventRequestForwarded] != 0 {
				t.Fatalf("forwarded events=%d, want 0", counts[sip.ProxyEventRequestForwarded])
			}
			compl := rec.kindSeqs(sip.ProxyEventRequestCompleted)
			if len(compl) != 1 || compl[0].Outcome != sip.ProxyOutcomeSendFailure {
				t.Fatalf("completion=%+v, want one send_failure", compl)
			}
		})
	})

	t.Run("three successful forks: handled=1 attempts=3 forwarded=1", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &prxEvtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-fork", obsLitPlan("A", "B", "C")), opts)
			prx.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))

			fwdIntercept(t, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".obs-fork", laddr, raddr))
			synctest.Wait()

			counts := rec.counts()
			if counts[sip.ProxyEventRequestForwarded] != 1 ||
				counts[sip.ProxyEventForwardAttemptFinished] != 3 {
				t.Fatal("fork observation counts are wrong")
			}
			if counts[sip.ProxyEventRequestHandled] != 1 {
				t.Fatalf("handled events=%d, want 1", counts[sip.ProxyEventRequestHandled])
			}
			for _, ev := range rec.kindSeqs(sip.ProxyEventForwardAttemptFinished) {
				if ev.Error != nil || ev.TargetID == "" || ev.AttemptID == "" {
					t.Fatalf("attempt event=%+v, want successful correlated attempt", ev)
				}
			}

			sends := drainPrxReqs(tp)
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
			if got := len(rec.kindSeqs(sip.ProxyEventResponseForwarded)); got != 2 {
				t.Fatalf("response events=%d, want 2", got)
			}
			if err := txB.RecvResponse(ctx, newInRes(t, sends[1], sip.ResponseStatusOK)); err != nil {
				t.Fatalf("txB.RecvResponse(retransmitted 200) error = %v, want nil", err)
			}
			synctest.Wait()
			if got := len(rec.kindSeqs(sip.ProxyEventResponseForwarded)); got != 3 {
				t.Fatalf("response events after settled retransmission=%d, want 3", got)
			}

			txC := fwdClnTx(t, elm, sends[2])
			if err := txC.RecvResponse(ctx, newInRes(t, sends[2], sip.ResponseStatusOK)); err != nil {
				t.Fatalf("txC.RecvResponse(200) error = %v, want nil", err)
			}
			synctest.Wait()
			compl := rec.kindSeqs(sip.ProxyEventRequestCompleted)
			if len(compl) != 1 || compl[0].Outcome != sip.ProxyOutcomeResponse ||
				compl[0].StatusCode != http.StatusOK {
				t.Fatalf("completion=%+v, want one response 200", compl)
			}
			if got := len(rec.kindSeqs(sip.ProxyEventResponseForwarded)); got != 4 {
				t.Fatalf("response events=%d, want 4", got)
			}
		})
	})

	t.Run("dns retry failure plus success are distinct attempts", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &prxEvtRecorder{}
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
					[]sip.ProxyTarget{attemptURITarget("A", "10.0.0.9")},
				)), opts, loc)
			prx.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))
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

			admitAttemptReq(ctx, t, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".obs-retry", laddr, raddr))
			synctest.Wait()

			atts := rec.kindSeqs(sip.ProxyEventForwardAttemptFinished)
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
			if got := len(rec.kindSeqs(sip.ProxyEventRequestForwarded)); got != 1 {
				t.Fatalf("forwarded events=%d, want 1", got)
			}
			_ = elm
		})
	})

	t.Run("size switch settles the generation once", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &prxEvtRecorder{}
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
					[]sip.ProxyTarget{attemptURITarget("A", "10.0.0.9")},
				)), opts, loc)
			prx.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))

			tcp := newPrxStubTransp()
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

			admitAttemptReq(ctx, t, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".obs-switch", laddr, raddr))
			synctest.Wait()

			atts := rec.kindSeqs(sip.ProxyEventForwardAttemptFinished)
			if len(atts) != 1 {
				t.Fatalf("attempt events=%d, want 1 (size switch is one logical attempt)", len(atts))
			}
			if atts[0].Error != nil {
				t.Fatalf("attempt error=%v, want nil (switched generation succeeded)", atts[0].Error)
			}
			if got := len(rec.kindSeqs(sip.ProxyEventRequestForwarded)); got != 1 {
				t.Fatalf("forwarded events=%d, want 1", got)
			}
		})
	})

	t.Run("timeout and cancellation race report the actual outcome", func(t *testing.T) {
		t.Parallel()

		t.Run("timer C expiry commits timeout", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				rec := &prxEvtRecorder{}
				elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-tmo", obsLitPlan("A")),
					sip.ProxyOptions{ServedDomains: []string{"alice.voip.com"}, TimerC: 4 * time.Minute})
				prx.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))

				fwdIntercept(t, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".obs-tmo", laddr, raddr))
				synctest.Wait()
				if got := len(drainPrxReqs(tp)); got != 1 {
					t.Fatalf("outbound sends=%d, want 1", got)
				}

				time.Sleep(4*time.Minute + time.Second)
				synctest.Wait()

				compl := rec.kindSeqs(sip.ProxyEventRequestCompleted)
				if len(compl) != 1 || compl[0].Outcome != sip.ProxyOutcomeTimeout {
					t.Fatalf("completion=%+v, want timeout", compl)
				}
				if compl[0].StatusCode != http.StatusRequestTimeout {
					t.Fatalf("completion status=%d, want 408 (generated)", compl[0].StatusCode)
				}
				// The generated 408 crossed upstream: its send is observed.
				if got := len(rec.kindSeqs(sip.ProxyEventResponseForwarded)); got != 1 {
					t.Fatalf("response events=%d, want 1 (generated 408)", got)
				}
				_ = elm
			})
		})

		t.Run("accepted cancellation does not beat a winning 2xx", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				rec := &prxEvtRecorder{}
				elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-race", obsLitPlan("A")), opts)
				prx.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))

				req := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-race", laddr, raddr)
				fwdIntercept(t, prx, req)
				synctest.Wait()
				sends := drainPrxReqs(tp)
				txA := fwdClnTx(t, elm, sends[0])

				cancel, err := sip.NewCancelRequestEnvelope(req)
				if err != nil {
					t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
				}
				fwdIntercept(t, prx, cancel)
				synctest.Wait()

				if got := len(rec.kindSeqs(sip.ProxyEventRequestCompleted)); got != 0 {
					t.Fatalf("completions after bare cancellation=%d, want 0", got)
				}

				// A racing success still commits the response outcome.
				if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusOK)); err != nil {
					t.Fatalf("txA.RecvResponse(200) error = %v, want nil", err)
				}
				synctest.Wait()
				compl := rec.kindSeqs(sip.ProxyEventRequestCompleted)
				if len(compl) != 1 || compl[0].Outcome != sip.ProxyOutcomeResponse {
					t.Fatalf("completion=%+v, want response (not cancelled)", compl)
				}
			})
		})
	})

	t.Run("transaction retransmissions emit no attempt observations", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &prxEvtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-retr", obsLitPlan("A")), opts)
			prx.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-retr", laddr, raddr)
			fwdIntercept(t, prx, req)
			synctest.Wait()
			sends := drainPrxReqs(tp)
			if got := len(sends); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}
			txA := fwdClnTx(t, elm, sends[0])
			if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("txA.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()
			drainPrxRess(tp)

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
			drainPrxRess(tp)

			if diff := cmp.Diff(base, rec.counts()); diff != "" {
				t.Fatalf("event counts after retransmission (-want +got): %s", diff)
			}
		})
	})

	t.Run("two stateless traversals are two operations", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			rec := &prxEvtRecorder{}
			route := obsRoute("obs-sl", obsLitPlan("A"))
			route.ForwardMode = sip.ForwardModeStateless
			_, prx, tp := newConfiguredTestProxy(t, route, sip.ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
				StatelessKey:  []byte("obs-sl-key"),
			})
			prx.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-sl", laddr, raddr)
			fwdIntercept(t, prx, req)
			fwdIntercept(t, prx, req.Clone().(*sip.RequestEnvelope))
			synctest.Wait()
			if got := len(drainPrxReqs(tp)); got != 2 {
				t.Fatalf("stateless sends=%d, want 2", got)
			}

			counts := rec.counts()
			if counts[sip.ProxyEventRequestHandled] != 2 ||
				counts[sip.ProxyEventRequestForwarded] != 2 ||
				counts[sip.ProxyEventForwardAttemptFinished] != 2 ||
				counts[sip.ProxyEventRequestCompleted] != 2 {
				t.Fatalf("stateless counts=%v, want 2 of each operation event", counts)
			}
			handled := rec.kindSeqs(sip.ProxyEventRequestHandled)
			if handled[0].RequestID == "" || handled[0].RequestID == handled[1].RequestID {
				t.Fatalf("stateless request IDs=%q,%q, want distinct per operation",
					handled[0].RequestID, handled[1].RequestID)
			}
			compl := rec.kindSeqs(sip.ProxyEventRequestCompleted)
			for _, ev := range compl {
				if ev.Outcome != sip.ProxyOutcomeSent {
					t.Fatalf("stateless completion=%+v, want sent", ev)
				}
			}
		})
	})

	t.Run("stateless pre-send failure emits a failed attempt", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			rec := &prxEvtRecorder{}
			route := &sip.ProxyRoute{
				Name:        "obs-slfail",
				ForwardMode: sip.ForwardModeStateless,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
					return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{{
						{
							ID:  "A",
							URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr("10.0.0.9", 5060)},
							// A delivery reference without a configured
							// resolver fails the destination binding.
							Delivery: sip.ProxyDeliveryRef{Kind: "gw", Key: "x"},
						},
					}}}, nil
				},
			}
			_, prx, _ := newConfiguredTestProxy(t, route, sip.ProxyOptions{
				ServedDomains: []string{"alice.voip.com"},
				StatelessKey:  []byte("obs-sl-key"),
			})
			prx.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))

			// An unmatched CANCEL is never promoted: its binding failure
			// lands in the per-target errors as one failed attempt.
			unseen := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-slfail", laddr, raddr)
			cancel, err := sip.NewCancelRequestEnvelope(unseen)
			if err != nil {
				t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
			}
			if err := prx.InterceptInboundRequest(t.Context(), noopNext, cancel); err == nil {
				t.Fatal("prx.InterceptInboundRequest() error = nil, want rejection")
			}
			synctest.Wait()

			counts := rec.counts()
			if counts[sip.ProxyEventRequestHandled] != 1 {
				t.Fatalf("handled events=%d, want 1", counts[sip.ProxyEventRequestHandled])
			}
			atts := rec.kindSeqs(sip.ProxyEventForwardAttemptFinished)
			if len(atts) != 1 {
				t.Fatalf("attempt events=%d, want 1", len(atts))
			}
			if atts[0].Error == nil || atts[0].TargetID != "A" || atts[0].AttemptID != "A" {
				t.Fatalf("attempt event=%+v, want one failed attempt of A", atts[0])
			}
			if counts[sip.ProxyEventRequestForwarded] != 0 {
				t.Fatalf("forwarded events=%d, want 0", counts[sip.ProxyEventRequestForwarded])
			}
			compl := rec.kindSeqs(sip.ProxyEventRequestCompleted)
			if len(compl) != 1 || compl[0].Outcome != sip.ProxyOutcomeRejected ||
				compl[0].StatusCode != 480 {
				t.Fatalf("completion=%+v, want rejected 480", compl)
			}
			// Ordering: Handled, the failed attempt, the completion.
			handled := rec.kindSeqs(sip.ProxyEventRequestHandled)[0]
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
			rec := &prxEvtRecorder{}
			_, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-slres", obsLitPlan("A")), opts)
			prx.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))

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
			if err := prx.InterceptInboundResponse(ctx, next, res); err != nil {
				t.Fatalf("prx.InterceptInboundResponse() error = %v, want nil", err)
			}
			synctest.Wait()

			rf := rec.kindSeqs(sip.ProxyEventResponseForwarded)
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
			rec := &prxEvtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("obs-ack", obsLitPlan("A")), opts)
			prx.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))

			// An end-to-end ACK takes the deterministic stateless path as
			// one operation and allocates no transaction or request record.
			inv := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-ack", laddr, raddr)
			res := newInRes(t, inv, sip.ResponseStatusOK)
			ack := newInAckReq(t, inv, res)
			fwdIntercept(t, prx, ack)
			synctest.Wait()
			if got := len(drainPrxReqs(tp)); got != 1 {
				t.Fatalf("ack sends=%d, want 1", got)
			}

			// An unmatched CANCEL forwards statelessly the same way.
			unseen := newInInviteReq(t, "UDP", sip.MagicCookie+".obs-uncancel", laddr, raddr)
			cancel, err := sip.NewCancelRequestEnvelope(unseen)
			if err != nil {
				t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
			}
			fwdIntercept(t, prx, cancel)
			synctest.Wait()
			if got := len(drainPrxReqs(tp)); got != 1 {
				t.Fatalf("cancel sends=%d, want 1", got)
			}

			if got := countAllTxs(elm); got != 0 {
				t.Fatalf("transactions=%d, want 0 (no allocated contexts)", got)
			}
			srvKey, keyErr := sip.MakeServerTransactionKey(unseen)
			if keyErr != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", keyErr)
			}
			if _, ok := prx.LookupRequest(srvKey); ok {
				t.Fatal("unmatched CANCEL left a request record")
			}
			counts := rec.counts()
			if counts[sip.ProxyEventRequestHandled] != 2 ||
				counts[sip.ProxyEventRequestForwarded] != 2 {
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
	opts := sip.ProxyOptions{ServedDomains: []string{"alice.voip.com"}}

	t.Run("completed request replays no historical observations", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := obsRoute("rest-obs-done", obsLitPlan("A"))
			elm1, prx1, tp1 := newConfiguredTestProxy(t, route, opts)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".rest-obs-done", laddr, raddr)
			fwdIntercept(t, prx1, req)
			synctest.Wait()

			sends := drainPrxReqs(tp1)
			if got := len(sends); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}
			txA := fwdClnTx(t, elm1, sends[0])
			if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusNotFound)); err != nil {
				t.Fatalf("txA.RecvResponse(404) error = %v, want nil", err)
			}
			synctest.Wait()
			drainPrxRess(tp1)

			srvKey, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			rq1, ok := prx1.LookupRequest(srvKey)
			if !ok {
				t.Fatal("prx1.LookupRequest() found no owned request")
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
				t.Fatalf("json.Unmarshal(ProxyRequestSnapshot) error = %v, want nil", err)
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
				t.Fatal("upstream transaction does not implement ServerTransactionSnapshotter")
			}
			srvSnap := mustSrvTxSnapshot(t, srvSnapper)

			_, prx2, tp2 := newConfiguredTestProxy(t, route, opts)
			rec := &prxEvtRecorder{}
			prx2.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))

			srvTx2, err := sip.RestoreServerTransaction(srvSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
			}
			rq2, err := prx2.RestoreRequest(ctx, snap, sip.ProxyRestoreBindings{
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
			drainPrxReqs(tp2)
			drainPrxRess(tp2)

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
			fwdIntercept(t, prx1, req)
			synctest.Wait()

			targetHosts := map[string]string{"10.0.0.9": "A", "10.0.0.10": "B", "10.0.0.11": "C"}
			targetOf := func(req *sip.RequestEnvelope) string {
				if u, ok := req.URI().(*sip.URI); ok {
					return targetHosts[u.Addr.Host()]
				}
				return ""
			}
			sends := make(map[string]*sip.RequestEnvelope)
			for _, out := range drainPrxReqs(tp1) {
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
			rq1, ok := prx1.LookupRequest(srvKey)
			if !ok {
				t.Fatal("prx1.LookupRequest() found no owned request")
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
					t.Fatalf("client transaction of target %s does not implement ClientTransactionSnapshotter", id)
				}
				clnSnaps[key] = mustClnTxSnapshot(t, snapper)
			}
			srvTx1, ok := elm1.LoadServerTransaction(srvKey)
			if !ok {
				t.Fatal("upstream transaction not registered")
			}
			srvSnapper, ok := srvTx1.(sip.ServerTransactionSnapshotter)
			if !ok {
				t.Fatal("upstream transaction does not implement ServerTransactionSnapshotter")
			}
			srvSnap := mustSrvTxSnapshot(t, srvSnapper)

			_, prx2, tp2 := newConfiguredTestProxy(t, route, opts)
			rec := &prxEvtRecorder{}
			prx2.BindEventHandler(sip.ProxyEventHandlerFunc(rec.record))

			srvTx2, err := sip.RestoreServerTransaction(srvSnap, tp2)
			if err != nil {
				t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
			}
			bindings := sip.ProxyRestoreBindings{
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
			rq2, err := prx2.RestoreRequest(ctx, snap, bindings)
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
			if got := len(drainPrxReqs(tp2)); got == 0 {
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
			kindsAfterActivation := make([]sip.ProxyEventKind, 0, len(rec.get()))
			for _, ev := range rec.get() {
				kindsAfterActivation = append(kindsAfterActivation, ev.Kind)
			}
			want := []sip.ProxyEventKind{sip.ProxyEventResponseForwarded}
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
			if evs[1].Kind != sip.ProxyEventResponseForwarded ||
				evs[1].Sequence <= evs[0].Sequence ||
				evs[1].RequestID != snap.RequestID {
				t.Fatalf("retransmitted response event=%+v, want a correlated ResponseForwarded past %d",
					evs[1],
					evs[0].Sequence,
				)
			}
			counts := rec.counts()
			if counts[sip.ProxyEventRequestHandled] != 0 ||
				counts[sip.ProxyEventRequestForwarded] != 0 ||
				counts[sip.ProxyEventForwardAttemptFinished] != 0 ||
				counts[sip.ProxyEventRequestCompleted] != 0 {
				t.Fatalf("replayed counts=%v, want only ResponseForwarded events", counts)
			}

			// Settling the last live sibling commits a genuinely new
			// decision: its completion emits normally instead of replaying
			// anything historical.
			if err := clnC.RecvResponse(ctx, newInRes(t, clnC.Request(), sip.ResponseStatusBusyHere)); err != nil {
				t.Fatalf("clnC.RecvResponse(486) error = %v, want nil", err)
			}
			synctest.Wait()
			compl := rec.kindSeqs(sip.ProxyEventRequestCompleted)
			if len(compl) != 1 || compl[0].Outcome != sip.ProxyOutcomeResponse ||
				compl[0].RequestID != snap.RequestID || compl[0].Sequence <= snap.Sequence {
				t.Fatalf("completion=%+v, want one new correlated response completion", compl)
			}
			drainPrxRess(tp2)
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
