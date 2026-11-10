package proxy_test

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/proxy"
)

func TestProxy_ResponseForwarding(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	resHosts := map[string]string{"A": "10.0.0.9", "B": "10.0.0.10", "C": "10.0.0.11"}
	resPlan := func(ids ...string) proxy.TargetPlan {
		targets := make([]proxy.Target, 0, len(ids))
		for _, id := range ids {
			targets = append(targets, proxy.Target{
				ID: id,
				URI: &sip.URI{
					User: sip.MakeUserInfo("alice"),
					Addr: sip.MakeHostPortAddr(resHosts[id], 5060),
				},
			})
		}
		return proxy.TargetPlan{Groups: [][]proxy.Target{targets}}
	}
	// targetID maps an outbound branch copy back to its plan target via the
	// target URI host; unknown hosts return an empty ID.
	targetID := func(req *sip.RequestEnvelope) string {
		uri, ok := req.URI().(*sip.URI)
		if !ok {
			return ""
		}
		for id, host := range resHosts {
			if uri.Addr.Host() == host {
				return id
			}
		}
		return ""
	}
	resOpts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}

	t.Run("INVITE branches forward every eligible response", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			origBody := []byte("v=0\r\no=- 1 1 IN IP4 10.0.0.9\r\ns=-\r\n")

			route := &proxy.Route{
				Name:        "res-fork",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return resPlan("A", "B", "C"), nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, resOpts)

			inReq := newInInviteReq(t, "UDP", sip.MagicCookie+".res-fork", laddr, raddr)
			var (
				inVia  *header.ViaHop
				inVias int
			)
			inReq.WithMessage(func(msg *sip.Request) {
				inVia, _ = msg.Headers.FirstVia()
				for range msg.Headers.Vias() {
					inVias++
				}
			})
			fwdIntercept(t, elm, prx, inReq)
			synctest.Wait()

			sends := drainReqs(tp)
			if len(sends) != 3 {
				t.Fatalf("outbound request sends=%d, want 3", len(sends))
			}
			txOf := make(map[string]sip.ClientTransaction, len(sends))
			for _, out := range sends {
				txOf[targetID(out)] = fwdClnTx(t, elm, out)
			}
			txA, txB, txC := txOf["A"], txOf["B"], txOf["C"]

			// A detached response event is only enqueued by the transaction
			// callback: the forwarding decision never runs inside the
			// callback itself. The probe records how many upstream sends were
			// already observed at each callback, so a response's own send
			// must never appear in its own count.
			var probe []int
			for _, tx := range []sip.ClientTransaction{txA, txB, txC} {
				tx.BindResponseHandler(sip.InboundResponseHandlerFunc(
					func(context.Context, *sip.ResponseEnvelope) {
						probe = append(probe, len(tp.sendResCalls))
					},
				))
			}
			feed := func(
				tx sip.ClientTransaction,
				sts sip.ResponseStatus,
				mutate func(*sip.Response),
			) *sip.ResponseEnvelope {
				t.Helper()
				res := newInRes(t, tx.Request(), sts)
				if mutate != nil {
					res.WithMessage(mutate)
				}
				if err := tx.RecvResponse(ctx, res); err != nil {
					t.Fatalf("clnTx.RecvResponse(%s) error = %v, want nil", sts, err)
				}
				synctest.Wait()
				return res
			}
			toTag := func(res *sip.ResponseEnvelope) string {
				var tag string
				res.WithMessage(func(msg *sip.Response) {
					if to, ok := msg.Headers.To(); ok {
						tag, _ = to.Tag()
					}
				})
				return tag
			}

			// A downstream 100 Trying is suppressed.
			feed(txA, sip.ResponseStatusTrying, nil)
			// An eligible INVITE 101-199 provisional forwards. The cached
			// downstream message the coordinator detached is never mutated by
			// the response processing.
			cachedRes := newInRes(t, txA.Request(), sip.ResponseStatusRinging)
			var cachedBefore string
			cachedRes.WithMessage(func(msg *sip.Response) { cachedBefore = msg.Render() })
			if err := txA.RecvResponse(ctx, cachedRes); err != nil {
				t.Fatalf("clnTx.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()
			var cachedAfter string
			cachedRes.WithMessage(func(msg *sip.Response) { cachedAfter = msg.Render() })
			if cachedAfter != cachedBefore {
				t.Fatal("rq.processRes mutated cached response")
			}

			// Additional and repeated INVITE successes from different
			// branches all forward with body and dialog tags intact.
			resA := feed(txA, sip.ResponseStatusOK, func(msg *sip.Response) { msg.Body = origBody })
			tagA := toTag(resA)
			resB := feed(txB, sip.ResponseStatusOK, func(msg *sip.Response) { msg.Body = origBody })
			tagB := toTag(resB)
			// A retransmitted success of an already settled attempt still
			// forwards while the downstream client and upstream server
			// transactions remain usable.
			feed(txB, sip.ResponseStatusOK, nil)
			// A response carrying only the proxy's own Via — like a response
			// to a locally generated CANCEL — has no upstream Via left after
			// the strip: it belongs to a local operation and is consumed.
			feed(txC, sip.ResponseStatusOK, func(msg *sip.Response) {
				if via, ok := msg.Headers.FirstVia(); ok {
					msg.Headers.Set(header.Via{*via})
				}
			})

			upstream := drainRess(tp)
			upstreamStatuses := make([]int, 0, len(upstream))
			for _, res := range upstream {
				upstreamStatuses = append(upstreamStatuses, int(res.Status()))
			}
			if diff := cmp.Diff([]int{180, 200, 200, 200}, upstreamStatuses); diff != "" {
				t.Fatalf("forwarded statuses (-want +got): %s", diff)
			}
			if diff := cmp.Diff([]int{0, 0, 1, 2, 3, 4}, probe); diff != "" {
				t.Fatalf("upstream sends observed inside response callbacks (-want +got): %s", diff)
			}

			// Exactly one Via is removed from the forwarded clone: the first
			// forwarded response keeps the downstream hop as its top Via and
			// the proxy's own hop is gone.
			var (
				fwdVia  *header.ViaHop
				fwdVias int
			)
			upstream[0].WithMessage(func(msg *sip.Response) {
				fwdVia, _ = msg.Headers.FirstVia()
				for range msg.Headers.Vias() {
					fwdVias++
				}
			})
			if fwdVias != inVias {
				t.Fatalf("forwarded response Via count=%d, want %d (one proxy hop removed)", fwdVias, inVias)
			}
			if !fwdVia.Equal(inVia) {
				t.Fatal("forwarded response top Via is not the downstream hop")
			}

			for i, wantTag := range []string{tagA, tagB} {
				res := upstream[1+i]
				var (
					gotTag string
					body   []byte
				)
				res.WithMessage(func(msg *sip.Response) {
					body = msg.Body
					if to, ok := msg.Headers.To(); ok {
						gotTag, _ = to.Tag()
					}
				})
				if gotTag != wantTag {
					t.Fatalf("forwarded 200 #%d To tag=%q, want %q", i+1, gotTag, wantTag)
				}
				if diff := cmp.Diff(origBody, body); diff != "" {
					t.Fatalf("forwarded 200 #%d body (-want +got): %s", i+1, diff)
				}
			}
		})
	})

	t.Run("committed final forwards once and keeps the cached copy intact", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			var (
				cachedRes    *sip.ResponseEnvelope
				cachedBefore string
			)
			route := &proxy.Route{
				Name:        "res-final",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return resPlan("A", "B"), nil
				},
				// The first settled non-success final is retained in the
				// response context while a sibling branch stays active: the
				// selection snapshot exposes the coordinator's stored copy.
				SelectTargets: func(_ context.Context, sel proxy.Selection) ([]string, error) {
					for i := range sel.CompletedOutcomes {
						if oc := &sel.CompletedOutcomes[i]; cachedRes == nil && oc.Response != nil {
							cachedRes = oc.Response
							cachedRes.WithMessage(func(msg *sip.Response) { cachedBefore = msg.Render() })
						}
					}
					if len(sel.RemainingGroups) == 0 {
						return nil, nil
					}
					ids := make([]string, 0, len(sel.RemainingGroups[0]))
					for _, trgt := range sel.RemainingGroups[0] {
						ids = append(ids, trgt.ID)
					}
					return ids, nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, resOpts)

			inReq := newInInviteReq(t, "UDP", sip.MagicCookie+".res-final", laddr, raddr)
			fwdIntercept(t, elm, prx, inReq)
			synctest.Wait()

			sends := drainReqs(tp)
			if len(sends) != 2 {
				t.Fatalf("outbound request sends=%d, want 2", len(sends))
			}
			txOf := make(map[string]sip.ClientTransaction, len(sends))
			for _, out := range sends {
				txOf[targetID(out)] = fwdClnTx(t, elm, out)
			}

			// A non-success final is not immediately forwardable while a
			// sibling branch stays active.
			if err := txOf["A"].RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusNotFound)); err != nil {
				t.Fatalf("clnTx.RecvResponse(404) error = %v, want nil", err)
			}
			synctest.Wait()
			if got := len(drainRess(tp)); got != 0 {
				t.Fatalf("upstream sends with an active sibling=%d, want 0", got)
			}
			if cachedRes == nil {
				t.Fatal("settled final response not retained in the response context")
			}

			// Once every branch settled, the single committed final is
			// forwarded through the upstream transaction exactly once.
			if err := txOf["B"].RecvResponse(ctx, newInRes(t, sends[1], sip.ResponseStatusBusyHere)); err != nil {
				t.Fatalf("clnTx.RecvResponse(486) error = %v, want nil", err)
			}
			synctest.Wait()

			upstream := drainRess(tp)
			if diff := cmp.Diff([]int{404}, fwdStatuses(upstream)); diff != "" {
				t.Fatalf("forwarded statuses (-want +got): %s", diff)
			}
			var cachedAfter string
			cachedRes.WithMessage(func(msg *sip.Response) { cachedAfter = msg.Render() })
			if cachedAfter != cachedBefore {
				t.Fatal("rq.processRes mutated cached response")
			}
		})
	})

	t.Run("selection ranks finals by precedence not arrival", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := &proxy.Route{
				Name:        "res-select",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return resPlan("A", "B"), nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, resOpts)

			fwdIntercept(t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".res-select", laddr, raddr))
			synctest.Wait()

			sends := drainReqs(tp)
			if len(sends) != 2 {
				t.Fatalf("outbound request sends=%d, want 2", len(sends))
			}
			txOf := make(map[string]sip.ClientTransaction, len(sends))
			for _, out := range sends {
				txOf[targetID(out)] = fwdClnTx(t, elm, out)
			}

			// A's earlier 486 loses to B's informative 401: the preferred
			// request-failure status outranks plain arrival order.
			if err := txOf["A"].RecvResponse(
				ctx, newInRes(t, txOf["A"].Request(), sip.ResponseStatusBusyHere),
			); err != nil {
				t.Fatalf("clnTx.RecvResponse(486) error = %v, want nil", err)
			}
			synctest.Wait()
			if err := txOf["B"].RecvResponse(
				ctx, newInRes(t, txOf["B"].Request(), sip.ResponseStatusUnauthorized),
			); err != nil {
				t.Fatalf("clnTx.RecvResponse(401) error = %v, want nil", err)
			}
			synctest.Wait()

			upstream := drainRess(tp)
			if diff := cmp.Diff([]int{401}, fwdStatuses(upstream)); diff != "" {
				t.Fatalf("forwarded statuses (-want +got): %s", diff)
			}
		})
	})

	t.Run("received 6xx outranks an earlier lower-class final", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := &proxy.Route{
				Name:        "res-6xx",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return resPlan("A", "B"), nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, resOpts)

			fwdIntercept(t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".res-6xx", laddr, raddr))
			synctest.Wait()

			sends := drainReqs(tp)
			if len(sends) != 2 {
				t.Fatalf("outbound request sends=%d, want 2", len(sends))
			}
			txOf := make(map[string]sip.ClientTransaction, len(sends))
			for _, out := range sends {
				txOf[targetID(out)] = fwdClnTx(t, elm, out)
			}

			// A's earlier 404 loses to B's later 6xx: any global failure wins
			// over every lower class.
			if err := txOf["A"].RecvResponse(
				ctx, newInRes(t, txOf["A"].Request(), sip.ResponseStatusNotFound),
			); err != nil {
				t.Fatalf("clnTx.RecvResponse(404) error = %v, want nil", err)
			}
			synctest.Wait()
			if err := txOf["B"].RecvResponse(
				ctx, newInRes(t, txOf["B"].Request(), sip.ResponseStatusBusyEverywhere),
			); err != nil {
				t.Fatalf("clnTx.RecvResponse(600) error = %v, want nil", err)
			}
			synctest.Wait()

			upstream := drainRess(tp)
			if diff := cmp.Diff([]int{600}, fwdStatuses(upstream)); diff != "" {
				t.Fatalf("forwarded statuses (-want +got): %s", diff)
			}
		})
	})

	t.Run("racing success commits over an earlier 6xx", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			var sendErrs atomic.Int32
			route := &proxy.Route{
				Name:        "res-6xx-race",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return resPlan("A", "B"), nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, resOpts)
			elm.BindErrorHandler(sip.ErrorHandlerFunc(func(context.Context, error) {
				sendErrs.Add(1)
			}))

			fwdIntercept(t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".res-6xx-race", laddr, raddr))
			synctest.Wait()

			sends := drainReqs(tp)
			if len(sends) != 2 {
				t.Fatalf("outbound request sends=%d, want 2", len(sends))
			}
			txOf := make(map[string]sip.ClientTransaction, len(sends))
			for _, out := range sends {
				txOf[targetID(out)] = fwdClnTx(t, elm, out)
			}

			// A's 6xx ends forking but B's racing 2xx still wins: the
			// committed final is the success — already forwarded while its
			// branch was live — so no extra send is attempted and nothing
			// errors.
			if err := txOf["A"].RecvResponse(
				ctx, newInRes(t, txOf["A"].Request(), sip.ResponseStatusBusyEverywhere),
			); err != nil {
				t.Fatalf("clnTx.RecvResponse(600) error = %v, want nil", err)
			}
			synctest.Wait()
			if err := txOf["B"].RecvResponse(
				ctx, newInRes(t, txOf["B"].Request(), sip.ResponseStatusOK),
			); err != nil {
				t.Fatalf("clnTx.RecvResponse(200) error = %v, want nil", err)
			}
			synctest.Wait()

			upstream := drainRess(tp)
			if diff := cmp.Diff([]int{200}, fwdStatuses(upstream)); diff != "" {
				t.Fatalf("forwarded statuses (-want +got): %s", diff)
			}
			if got := sendErrs.Load(); got != 0 {
				t.Fatalf("upstream send errors=%d, want 0 (a rejected committed send is a selection bug)", got)
			}
		})
	})

	t.Run("lone downstream 503 maps to a generated 500", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := &proxy.Route{
				Name:        "res-lone-503",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return resPlan("A"), nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, resOpts)

			fwdIntercept(t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".res-lone-503", laddr, raddr))
			synctest.Wait()

			sends := drainReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("outbound request sends=%d, want 1", len(sends))
			}
			tx := fwdClnTx(t, elm, sends[0])

			// The only response the request context ever collects is a 503:
			// the proxy cannot claim general unavailability for the upstream
			// client, so the committed final is a generated 500.
			if err := tx.RecvResponse(
				ctx, newInRes(t, sends[0], sip.ResponseStatusServiceUnavailable),
			); err != nil {
				t.Fatalf("clnTx.RecvResponse(503) error = %v, want nil", err)
			}
			synctest.Wait()

			upstream := drainRess(tp)
			if diff := cmp.Diff([]int{500}, fwdStatuses(upstream)); diff != "" {
				t.Fatalf("forwarded statuses (-want +got): %s", diff)
			}
			var toTag string
			upstream[0].WithMessage(func(msg *sip.Response) {
				if to, ok := msg.Headers.To(); ok {
					toTag, _ = to.Tag()
				}
			})
			if toTag == "" {
				t.Fatal("generated 500 lost the downstream dialog tag")
			}
		})
	})

	t.Run("declined branch does not disturb the lone 503", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			var calls atomic.Int32
			route := &proxy.Route{
				Name:        "res-decline",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return resPlan("A", "B"), nil
				},
				// B is never selected: once A settles the hook declines the
				// remaining target, which settles without any outcome.
				SelectTargets: func(context.Context, proxy.Selection) ([]string, error) {
					if calls.Add(1) == 1 {
						return []string{"A"}, nil
					}
					return nil, nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, resOpts)

			fwdIntercept(t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".res-decline", laddr, raddr))
			synctest.Wait()

			sends := drainReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("outbound request sends=%d, want 1", len(sends))
			}
			tx := fwdClnTx(t, elm, sends[0])

			if err := tx.RecvResponse(
				ctx, newInRes(t, sends[0], sip.ResponseStatusServiceUnavailable),
			); err != nil {
				t.Fatalf("clnTx.RecvResponse(503) error = %v, want nil", err)
			}
			synctest.Wait()

			upstream := drainRess(tp)
			if diff := cmp.Diff([]int{500}, fwdStatuses(upstream)); diff != "" {
				t.Fatalf("forwarded statuses (-want +got): %s", diff)
			}
			var invites int
			for _, out := range drainReqs(tp) {
				if out.Method().Equal(sip.RequestMethodInvite) {
					invites++
				}
			}
			if invites != 0 {
				t.Fatalf("new INVITE branches after completion=%d, want 0 (declined target never started)", invites)
			}
		})
	})

	t.Run("selected 401 forwards aggregated challenges", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			wwwA := sip.Header(&header.WWWAuthenticate{
				AuthChallenge: &header.DigestChallenge{Realm: "a.example", Nonce: "n-a"},
			})
			prxAuth := sip.Header(&header.ProxyAuthenticate{
				AuthChallenge: &header.DigestChallenge{Realm: "p.example", Nonce: "n-p"},
			})
			route := &proxy.Route{
				Name:        "res-aggreg",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return resPlan("A", "B"), nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, resOpts)

			fwdIntercept(t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".res-aggreg", laddr, raddr))
			synctest.Wait()

			sends := drainReqs(tp)
			if len(sends) != 2 {
				t.Fatalf("outbound request sends=%d, want 2", len(sends))
			}
			txOf := make(map[string]sip.ClientTransaction, len(sends))
			for _, out := range sends {
				txOf[targetID(out)] = fwdClnTx(t, elm, out)
			}

			resA := newInRes(t, txOf["A"].Request(), sip.ResponseStatusUnauthorized)
			resA.WithMessage(func(msg *sip.Response) { msg.Headers.Append(wwwA) })
			if err := txOf["A"].RecvResponse(ctx, resA); err != nil {
				t.Fatalf("clnTx.RecvResponse(401) error = %v, want nil", err)
			}
			synctest.Wait()
			resB := newInRes(t, txOf["B"].Request(), sip.ResponseStatusProxyAuthenticationRequired)
			resB.WithMessage(func(msg *sip.Response) { msg.Headers.Append(prxAuth) })
			if err := txOf["B"].RecvResponse(ctx, resB); err != nil {
				t.Fatalf("clnTx.RecvResponse(407) error = %v, want nil", err)
			}
			synctest.Wait()

			// The forwarded 401 carries its own challenge plus every
			// Proxy-Authenticate collected across the response context.
			upstream := drainRess(tp)
			if diff := cmp.Diff([]int{401}, fwdStatuses(upstream)); diff != "" {
				t.Fatalf("forwarded statuses (-want +got): %s", diff)
			}
			var gotChallenges []sip.Header
			upstream[0].WithMessage(func(msg *sip.Response) {
				gotChallenges = append(gotChallenges, msg.Headers.Get("WWW-Authenticate")...)
				gotChallenges = append(gotChallenges, msg.Headers.Get("Proxy-Authenticate")...)
			})
			if diff := cmp.Diff([]sip.Header{wwwA, prxAuth}, gotChallenges); diff != "" {
				t.Fatalf("forwarded challenges (-want +got): %s", diff)
			}
		})
	})

	t.Run("redirection final preserves its contacts without recursion", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			contact := sip.Header(header.Contact{{
				URI: &sip.URI{
					User: sip.MakeUserInfo("alice"),
					Addr: sip.MakeHostAddr("alt.example"),
				},
			}})
			route := &proxy.Route{
				Name:        "res-3xx",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return resPlan("A", "B"), nil
				},
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, resOpts)

			fwdIntercept(t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".res-3xx", laddr, raddr))
			synctest.Wait()

			sends := drainReqs(tp)
			if len(sends) != 2 {
				t.Fatalf("outbound request sends=%d, want 2", len(sends))
			}
			txOf := make(map[string]sip.ClientTransaction, len(sends))
			for _, out := range sends {
				txOf[targetID(out)] = fwdClnTx(t, elm, out)
			}

			resA := newInRes(t, txOf["A"].Request(), sip.ResponseStatusMovedTemporarily)
			resA.WithMessage(func(msg *sip.Response) { msg.Headers.Append(contact) })
			if err := txOf["A"].RecvResponse(ctx, resA); err != nil {
				t.Fatalf("clnTx.RecvResponse(302) error = %v, want nil", err)
			}
			synctest.Wait()
			if err := txOf["B"].RecvResponse(
				ctx, newInRes(t, txOf["B"].Request(), sip.ResponseStatusBusyHere),
			); err != nil {
				t.Fatalf("clnTx.RecvResponse(486) error = %v, want nil", err)
			}
			synctest.Wait()

			upstream := drainRess(tp)
			if diff := cmp.Diff([]int{302}, fwdStatuses(upstream)); diff != "" {
				t.Fatalf("forwarded statuses (-want +got): %s", diff)
			}
			var gotContacts []sip.Header
			upstream[0].WithMessage(func(msg *sip.Response) {
				gotContacts = append(gotContacts, msg.Headers.Get("Contact")...)
			})
			if diff := cmp.Diff([]sip.Header{contact}, gotContacts); diff != "" {
				t.Fatalf("forwarded contacts (-want +got): %s", diff)
			}
		})
	})

	for _, tt := range []struct {
		name     string
		behavior sip.NonInviteServerTransactionBehavior
	}{
		{"legacy", sip.NonInviteServerTransactionBehaviorLegacy},
		{"rfc4320", sip.NonInviteServerTransactionBehaviorRFC4320},
	} {
		t.Run("non-INVITE provisional suppressed in "+tt.name+" mode", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				route := &proxy.Route{
					Name:        "res-ni-" + tt.name,
					ForwardMode: proxy.ForwardModeStateful,
					ServerTransactionOptions: sip.ServerTransactionOptions{
						NonInviteServerTransactionBehavior: tt.behavior,
					},
					ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
						return resPlan("A"), nil
					},
				}
				elm, prx, tp := newConfiguredTestProxy(t, route, resOpts)

				var sendErrs atomic.Int32
				elm.BindErrorHandler(sip.ErrorHandlerFunc(func(context.Context, error) {
					sendErrs.Add(1)
				}))

				fwdIntercept(t, elm, prx, newInNonInviteReq(t, "UDP", sip.MagicCookie+".ni-"+tt.name, laddr, raddr))
				synctest.Wait()

				sends := drainReqs(tp)
				if len(sends) != 1 {
					t.Fatalf("outbound request sends=%d, want 1", len(sends))
				}
				tx := fwdClnTx(t, elm, sends[0])

				// A downstream non-INVITE provisional is suppressed in either
				// behavior mode, before any upstream send is attempted.
				if err := tx.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusSessionProgress)); err != nil {
					t.Fatalf("clnTx.RecvResponse(183) error = %v, want nil", err)
				}
				synctest.Wait()
				if got := len(drainRess(tp)); got != 0 {
					t.Fatalf("upstream sends after non-INVITE 183=%d, want 0", got)
				}

				// A downstream non-INVITE 408 never crosses upstream, not
				// even as the request's committed final response.
				if err := tx.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusRequestTimeout)); err != nil {
					t.Fatalf("clnTx.RecvResponse(408) error = %v, want nil", err)
				}
				synctest.Wait()
				if got := len(drainRess(tp)); got != 0 {
					t.Fatalf("upstream sends after non-INVITE 408=%d, want 0", got)
				}
				if got := sendErrs.Load(); got != 0 {
					t.Fatalf("upstream send attempts reported errors=%d, want 0 (suppressed before send)", got)
				}
			})
		})
	}
}

func TestProxy_ResponseForwarding_SuccessAfterLogicalCompletion(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	opts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}
	// targetID maps an outbound branch copy back to its plan target via the
	// obsLitPlan target URI host.
	targetID := func(req *sip.RequestEnvelope) string {
		uri, ok := req.URI().(*sip.URI)
		if !ok {
			return ""
		}
		switch uri.Addr.Host() {
		case "10.0.0.9":
			return "A"
		case "10.0.0.10":
			return "B"
		default:
			return ""
		}
	}
	feed := func(
		tb testing.TB,
		ctx context.Context,
		tx sip.ClientTransaction,
		res *sip.ResponseEnvelope,
	) {
		tb.Helper()
		if err := tx.RecvResponse(ctx, res); err != nil {
			tb.Fatalf("clnTx.RecvResponse(%s) error = %v, want nil", res.Status(), err)
		}
		synctest.Wait()
	}

	t.Run("retransmitted success on a settled branch", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &evtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("res-retr-2xx", obsLitPlan("A")), opts)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			inReq := newInInviteReq(t, "UDP", sip.MagicCookie+".res-retr-2xx", laddr, raddr)
			fwdIntercept(t, elm, prx, inReq)
			synctest.Wait()

			sends := drainReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("outbound request sends=%d, want 1", len(sends))
			}
			tx := fwdClnTx(t, elm, sends[0])

			res := newInRes(t, sends[0], sip.ResponseStatusOK)
			feed(t, ctx, tx, res)
			if diff := cmp.Diff([]int{200}, fwdStatuses(drainRess(tp))); diff != "" {
				t.Fatalf("forwarded statuses (-want +got): %s", diff)
			}

			// The settled client transaction lives on in Accepted: an exact
			// retransmission of its success stays forwardable and crosses
			// upstream again through the retained server transaction.
			feed(t, ctx, tx, res.Clone().(*sip.ResponseEnvelope))
			if diff := cmp.Diff([]int{200}, fwdStatuses(drainRess(tp))); diff != "" {
				t.Fatalf("forwarded statuses after retransmission (-want +got): %s", diff)
			}

			if got := tx.State(); got == sip.TransactionStateTerminated {
				t.Fatalf("client transaction state=%v, want a live transaction", got)
			}
			srvKey, err := sip.MakeServerTransactionKey(inReq)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			srvTx, ok := elm.LoadServerTransaction(srvKey)
			if !ok {
				t.Fatal("upstream server transaction not registered")
			}
			if got := srvTx.State(); got == sip.TransactionStateTerminated {
				t.Fatalf("upstream transaction state=%v, want a live transaction", got)
			}

			counts := rec.counts()
			if counts[proxy.EventKindRequestForwarded] != 1 ||
				counts[proxy.EventKindResponseForwarded] != 2 ||
				counts[proxy.EventKindRequestCompleted] != 1 {
				t.Fatalf("event counts=%v, want one forward, two forwarded responses, one completion", counts)
			}
		})
	})

	t.Run("success from a live branch after completion", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &evtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("res-live-2xx", obsLitPlan("A", "B")), opts)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			fwdIntercept(t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".res-live-2xx", laddr, raddr))
			synctest.Wait()

			sends := drainReqs(tp)
			if len(sends) != 2 {
				t.Fatalf("outbound request sends=%d, want 2", len(sends))
			}
			txOf := make(map[string]sip.ClientTransaction, len(sends))
			for _, out := range sends {
				txOf[targetID(out)] = fwdClnTx(t, elm, out)
			}

			// A's success commits the request direction while B's branch is
			// still live; B's later success on its retained transaction
			// crosses upstream as an additional eligible 2xx.
			feed(t, ctx, txOf["A"], newInRes(t, txOf["A"].Request(), sip.ResponseStatusOK))
			feed(t, ctx, txOf["B"], newInRes(t, txOf["B"].Request(), sip.ResponseStatusOK))

			if diff := cmp.Diff([]int{200, 200}, fwdStatuses(drainRess(tp))); diff != "" {
				t.Fatalf("forwarded statuses (-want +got): %s", diff)
			}
			counts := rec.counts()
			if counts[proxy.EventKindRequestCompleted] != 1 {
				t.Fatalf("completion events=%d, want 1", counts[proxy.EventKindRequestCompleted])
			}
		})
	})
}

func TestProxy_NonInviteSuppression(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	niHosts := map[string]string{"A": "10.0.0.9", "B": "10.0.0.10"}
	niPlan := func(ids ...string) proxy.TargetPlan {
		targets := make([]proxy.Target, 0, len(ids))
		for _, id := range ids {
			targets = append(targets, proxy.Target{
				ID: id,
				URI: &sip.URI{
					User: sip.MakeUserInfo("alice"),
					Addr: sip.MakeHostPortAddr(niHosts[id], 5060),
				},
			})
		}
		return proxy.TargetPlan{Groups: [][]proxy.Target{targets}}
	}
	// niTargetID maps an outbound branch copy back to its plan target via
	// the target URI host; unknown hosts return an empty ID.
	niTargetID := func(req *sip.RequestEnvelope) string {
		uri, ok := req.URI().(*sip.URI)
		if !ok {
			return ""
		}
		for id, host := range niHosts {
			if uri.Addr.Host() == host {
				return id
			}
		}
		return ""
	}
	niOpts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}
	// countingTestProxy counts upstream server transaction factory calls:
	// the factory seam runs exactly once at admission, and a discarded or
	// suppressed response never fabricates a replacement transaction.
	countingTestProxy := func(
		tb testing.TB,
		route *proxy.Route,
		calls *atomic.Int32,
	) (*sip.Element, *proxy.Proxy, *stubTransp) {
		tb.Helper()
		return newConfiguredTestProxy(tb, route, niOpts, &sip.Element{
			ServerTransactionFactory: sip.ServerTransactionFactoryFunc(
				func(
					req *sip.RequestEnvelope,
					tp sip.ServerTransport,
					opts ...sip.ServerTransactionOptions,
				) (sip.ServerTransaction, error) {
					calls.Add(1)
					return sip.NewServerTransaction(req, tp, opts...)
				},
			),
		})
	}
	niRoute := func(name string, behavior sip.NonInviteServerTransactionBehavior, ids ...string) *proxy.Route {
		return &proxy.Route{
			Name:                     name,
			ForwardMode:              proxy.ForwardModeStateful,
			ServerTransactionOptions: sip.ServerTransactionOptions{NonInviteServerTransactionBehavior: behavior},
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return niPlan(ids...), nil
			},
		}
	}
	// upstreamSrvKey returns the transaction key under which the admitted
	// upstream server transaction is registered for req.
	upstreamSrvKey := func(tb testing.TB, req *sip.RequestEnvelope) sip.ServerTransactionKey {
		tb.Helper()
		key, err := sip.MakeServerTransactionKey(req)
		if err != nil {
			tb.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		return key
	}
	// upstreamStatuses returns the wire-observed response statuses in send
	// order.
	upstreamStatuses := func(tp *stubTransp) []int {
		return fwdStatuses(drainRess(tp))
	}

	for _, tt := range []struct {
		name     string
		behavior sip.NonInviteServerTransactionBehavior
	}{
		{"legacy", sip.NonInviteServerTransactionBehaviorLegacy},
		{"rfc4320", sip.NonInviteServerTransactionBehaviorRFC4320},
	} {
		t.Run("downstream 408 and all-timeout retire silently in "+tt.name+" mode", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				var factoryCalls atomic.Int32
				route := niRoute("ni-silent-"+tt.name, tt.behavior, "A", "B")
				elm, prx, tp := countingTestProxy(t, route, &factoryCalls)

				inReq := newInNonInviteReq(t, "UDP", sip.MagicCookie+".ni-silent-"+tt.name, laddr, raddr)
				fwdIntercept(t, elm, prx, inReq)
				synctest.Wait()

				sends := drainReqs(tp)
				if len(sends) != 2 {
					t.Fatalf("outbound request sends=%d, want 2", len(sends))
				}
				txOf := make(map[string]sip.ClientTransaction, len(sends))
				for _, out := range sends {
					txOf[niTargetID(out)] = fwdClnTx(t, elm, out)
				}

				// The local timeout outcome stays observable through the
				// attempt transaction's own error report.
				var timeouts atomic.Int32
				txOf["B"].BindErrorHandler(sip.ErrorHandlerFunc(func(_ context.Context, err error) {
					if errors.Is(err, sip.ErrTransactionTimedOut) {
						timeouts.Add(1)
					}
				}))

				// A downstream non-INVITE 408 records the branch outcome but
				// can never cross upstream, in either behavior mode.
				if err := txOf["A"].RecvResponse(
					ctx, newInRes(t, txOf["A"].Request(), sip.ResponseStatusRequestTimeout),
				); err != nil {
					t.Fatalf("clnTx.RecvResponse(408) error = %v, want nil", err)
				}
				synctest.Wait()

				// The remaining silent branch exhausts its finite Timer F;
				// the request completes with the local timeout outcome and
				// retires the owned upstream transaction silently.
				time.Sleep(sip.TimingConfig{}.TimeF() + time.Second)
				synctest.Wait()

				for _, c := range upstreamStatuses(tp) {
					if c == 408 {
						t.Fatal("non-INVITE 408 reached upstream")
					}
				}
				if got := timeouts.Load(); got != 1 {
					t.Fatalf("local timeout reports=%d, want 1", got)
				}
				if _, ok := elm.LoadServerTransaction(upstreamSrvKey(t, inReq)); ok {
					t.Fatal("upstream server transaction still registered: owned state did not retire silently")
				}
				if got := factoryCalls.Load(); got != 1 {
					t.Fatalf("server creations=%d, want 1", got)
				}
			})
		})

		t.Run("late final after upstream termination is discarded in "+tt.name+" mode", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				var factoryCalls atomic.Int32
				route := niRoute("ni-late-"+tt.name, tt.behavior, "A", "B")
				elm, prx, tp := countingTestProxy(t, route, &factoryCalls)

				inReq := newInNonInviteReq(t, "UDP", sip.MagicCookie+".ni-late-"+tt.name, laddr, raddr)
				fwdIntercept(t, elm, prx, inReq)
				synctest.Wait()

				sends := drainReqs(tp)
				if len(sends) != 2 {
					t.Fatalf("outbound request sends=%d, want 2", len(sends))
				}
				txOf := make(map[string]sip.ClientTransaction, len(sends))
				for _, out := range sends {
					txOf[niTargetID(out)] = fwdClnTx(t, elm, out)
				}

				// A settles with a forwardable final while B stays active:
				// the non-INVITE committed final still waits for selection.
				if err := txOf["A"].RecvResponse(
					ctx, newInRes(t, txOf["A"].Request(), sip.ResponseStatusUnauthorized),
				); err != nil {
					t.Fatalf("clnTx.RecvResponse(401) error = %v, want nil", err)
				}
				synctest.Wait()

				// The held upstream transaction terminates: a late committed
				// final is discarded — never through a fabricated
				// replacement transaction and never through a stateless
				// fallback.
				srvTx, ok := elm.LoadServerTransaction(upstreamSrvKey(t, inReq))
				if !ok {
					t.Fatal("admitted server transaction not registered")
				}
				if err := srvTx.Terminate(ctx, errors.ErrorWrap("upstream gone")); err != nil {
					t.Fatalf("srvTx.Terminate() error = %v, want nil", err)
				}
				synctest.Wait()
				if err := txOf["B"].RecvResponse(
					ctx, newInRes(t, txOf["B"].Request(), sip.ResponseStatusNotFound),
				); err != nil {
					t.Fatalf("clnTx.RecvResponse(404) error = %v, want nil", err)
				}
				synctest.Wait()

				if got := len(drainRess(tp)); got != 0 {
					t.Fatalf("upstream sends after upstream termination=%d, want 0", got)
				}
				if got := factoryCalls.Load(); got != 1 {
					t.Fatalf("server creations=%d, want 1", got)
				}
			})
		})

		t.Run("fast 401 and a silent branch honor finite timer F in "+tt.name+" mode", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				var factoryCalls atomic.Int32
				route := niRoute("ni-timerf-"+tt.name, tt.behavior, "A", "B")
				elm, prx, tp := countingTestProxy(t, route, &factoryCalls)

				inReq := newInNonInviteReq(t, "UDP", sip.MagicCookie+".ni-timerf-"+tt.name, laddr, raddr)
				fwdIntercept(t, elm, prx, inReq)
				synctest.Wait()

				sends := drainReqs(tp)
				if len(sends) != 2 {
					t.Fatalf("outbound request sends=%d, want 2", len(sends))
				}
				txOf := make(map[string]sip.ClientTransaction, len(sends))
				for _, out := range sends {
					txOf[niTargetID(out)] = fwdClnTx(t, elm, out)
				}

				snapper, ok := txOf["B"].(sip.ClientTransactionSnapshotter)
				if !ok {
					t.Fatal("client transaction does not support snapshots")
				}
				snapBefore, err := snapper.Snapshot()
				if err != nil {
					t.Fatalf("tx.Snapshot() error = %v, want nil", err)
				}
				if snapBefore == nil || snapBefore.TimerF == nil {
					t.Fatalf("tx.Snapshot() = %+v, want a running Timer F", snapBefore)
				}
				deadline := snapBefore.TimerF.StartTime.Add(snapBefore.TimerF.Duration)

				// A's fast 401 settles only its own branch; the silent
				// sibling's lifetime is never reset by sibling progress.
				if err := txOf["A"].RecvResponse(
					ctx, newInRes(t, txOf["A"].Request(), sip.ResponseStatusUnauthorized),
				); err != nil {
					t.Fatalf("clnTx.RecvResponse(401) error = %v, want nil", err)
				}
				synctest.Wait()

				snapAfter, err := snapper.Snapshot()
				if err != nil {
					t.Fatalf("tx.Snapshot() error = %v, want nil", err)
				}
				if snapAfter == nil || snapAfter.TimerF == nil {
					t.Fatalf("tx.Snapshot() after sibling 401 = %+v, want a running Timer F", snapAfter)
				}
				if got := snapAfter.TimerF.StartTime.Add(snapAfter.TimerF.Duration); !got.Equal(deadline) {
					t.Fatalf("Timer F deadline after sibling 401 = %v, want unchanged %v", got, deadline)
				}

				// At its original deadline the silent branch times out and
				// the committed 401 finally crosses upstream.
				time.Sleep(time.Until(deadline) + 200*time.Millisecond)
				synctest.Wait()

				if got := txOf["B"].State(); got != sip.TransactionStateTerminated {
					t.Fatalf("silent branch state=%v, want %v", got, sip.TransactionStateTerminated)
				}
				var (
					got408 bool
					got401 bool
				)
				for _, c := range upstreamStatuses(tp) {
					if c == 408 {
						got408 = true
					}
					if c == 401 {
						got401 = true
					}
				}
				if got408 {
					t.Fatal("non-INVITE 408 reached upstream")
				}
				if !got401 {
					t.Fatal("committed non-INVITE 401 never reached upstream")
				}
				if got := factoryCalls.Load(); got != 1 {
					t.Fatalf("server creations=%d, want 1", got)
				}
			})
		})
	}

	t.Run("INVITE exhaustion answers 408 through the original upstream", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var factoryCalls atomic.Int32
			route := niRoute("inv-exhaust", sip.NonInviteServerTransactionBehaviorLegacy, "A")
			elm, prx, tp := countingTestProxy(t, route, &factoryCalls)

			fwdIntercept(t, elm, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".inv-exhaust", laddr, raddr))
			synctest.Wait()

			sends := drainReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("outbound request sends=%d, want 1", len(sends))
			}

			// No downstream response ever arrives: the single attempt times
			// out at Timer B and the proxy answers the INVITE itself through
			// the existing upstream transaction — the transaction's own
			// automatic 100 Trying may appear first.
			time.Sleep(sip.TimingConfig{}.TimeB() + time.Second)
			synctest.Wait()

			sts := upstreamStatuses(tp)
			if len(sts) == 0 || sts[len(sts)-1] != 408 {
				t.Fatalf("upstream statuses=%v, want last 408", sts)
			}
			if got := factoryCalls.Load(); got != 1 {
				t.Fatalf("server creations=%d, want 1", got)
			}
		})
	})
}

func TestProxy_AcceptedLifetime(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	accHosts := map[string]string{"A": "10.0.0.9", "B": "10.0.0.10", "C": "10.0.0.11"}
	accPlan := func(ids ...string) proxy.TargetPlan {
		targets := make([]proxy.Target, 0, len(ids))
		for _, id := range ids {
			targets = append(targets, proxy.Target{
				ID: id,
				URI: &sip.URI{
					User: sip.MakeUserInfo("alice"),
					Addr: sip.MakeHostPortAddr(accHosts[id], 5060),
				},
			})
		}
		return proxy.TargetPlan{Groups: [][]proxy.Target{targets}}
	}
	// targetID maps an outbound branch copy back to its plan target via the
	// target URI host; unknown hosts return an empty ID.
	targetID := func(req *sip.RequestEnvelope) string {
		uri, ok := req.URI().(*sip.URI)
		if !ok {
			return ""
		}
		for id, host := range accHosts {
			if uri.Addr.Host() == host {
				return id
			}
		}
		return ""
	}
	accRoute := func(name string, ids ...string) *proxy.Route {
		return &proxy.Route{
			Name:        name,
			ForwardMode: proxy.ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return accPlan(ids...), nil
			},
		}
	}
	accOpts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}
	// inviteSends drops the ACK copies an INVITE client transaction emits on
	// a non-2xx final response and any branch-cancellation CANCEL, keeping
	// only the INVITE branch starts.
	inviteSends := func(reqs []*sip.RequestEnvelope) []*sip.RequestEnvelope {
		var out []*sip.RequestEnvelope
		for _, req := range reqs {
			if req.Method().Equal(sip.RequestMethodInvite) {
				out = append(out, req)
			}
		}
		return out
	}
	// clnTxsOf loads the registered downstream client transactions of the
	// captured outbound INVITE copies keyed by their plan target ID.
	clnTxsOf := func(
		tb testing.TB,
		elm *sip.Element,
		sends []*sip.RequestEnvelope,
	) map[string]sip.ClientTransaction {
		tb.Helper()
		txOf := make(map[string]sip.ClientTransaction, len(sends))
		for _, out := range inviteSends(sends) {
			txOf[targetID(out)] = fwdClnTx(tb, elm, out)
		}
		return txOf
	}
	// srvTxOf returns the admitted upstream server transaction of inReq.
	srvTxOf := func(tb testing.TB, elm *sip.Element, inReq *sip.RequestEnvelope) sip.ServerTransaction {
		tb.Helper()
		key, err := sip.MakeServerTransactionKey(inReq)
		if err != nil {
			tb.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		tx, ok := elm.LoadServerTransaction(key)
		if !ok {
			tb.Fatal("admitted server transaction not registered")
		}
		return tx
	}
	feed := func(
		tb testing.TB,
		ctx context.Context,
		tx sip.ClientTransaction,
		sts sip.ResponseStatus,
	) {
		tb.Helper()
		if err := tx.RecvResponse(ctx, newInRes(tb, tx.Request(), sts)); err != nil {
			tb.Fatalf("clnTx.RecvResponse(%s) error = %v, want nil", sts, err)
		}
		synctest.Wait()
	}

	t.Run("received 6xx cancels pending branches yet a racing 2xx still wins", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			elm, prx, tp := newConfiguredTestProxy(t, accRoute("acc-6xx-race", "A", "B", "C"), accOpts)

			inReq := newInInviteReq(t, "UDP", sip.MagicCookie+".acc-6xx-race", laddr, raddr)
			fwdIntercept(t, elm, prx, inReq)
			synctest.Wait()

			sends := drainReqs(tp)
			if got := len(inviteSends(sends)); got != 3 {
				t.Fatalf("outbound INVITE sends=%d, want 3", got)
			}
			txOf := clnTxsOf(t, elm, sends)

			// A provisional moves B's branch to Proceeding where a CANCEL
			// may legally be sent; C stays in Calling, where none may.
			feed(t, ctx, txOf["B"], sip.ResponseStatusRinging)
			if diff := cmp.Diff([]int{180}, fwdStatuses(drainRess(tp))); diff != "" {
				t.Fatalf("forwarded provisionals (-want +got): %s", diff)
			}

			// A's 6xx ends forking and initiates cancellation of the
			// pending branches, but the response context is kept for the
			// committed decision — the 6xx is not forwarded ahead of a
			// possible racing 2xx.
			feed(t, ctx, txOf["A"], sip.ResponseStatusBusyEverywhere)

			var cancels []*sip.RequestEnvelope
			for _, req := range drainReqs(tp) {
				if req.Method().Equal(sip.RequestMethodCancel) {
					cancels = append(cancels, req)
				}
			}
			if got := len(cancels); got != 1 {
				t.Fatalf("downstream CANCEL sends=%d, want 1", got)
			}
			if got := targetID(cancels[0]); got != "B" {
				t.Fatalf("CANCEL target=%q, want %q", got, "B")
			}
			if got := len(drainRess(tp)); got != 0 {
				t.Fatalf("upstream sends after 6xx=%d, want 0 (a pending branch may still return 2xx)", got)
			}

			// B's racing success — emitted by the UAS before it consumed
			// the CANCEL — still wins and crosses upstream through the
			// RFC 6026 Accepted-state transaction.
			feed(t, ctx, txOf["B"], sip.ResponseStatusOK)
			srvTx := srvTxOf(t, elm, inReq)
			if got := srvTx.State(); got != sip.TransactionStateAccepted {
				t.Fatalf("upstream transaction state=%v, want %v", got, sip.TransactionStateAccepted)
			}

			// C was cancelled while still in Calling, so its racing 200 is
			// the additional eligible 2xx: it still forwards while the
			// Accepted transaction lives.
			feed(t, ctx, txOf["C"], sip.ResponseStatusOK)

			upstream := drainRess(tp)
			if diff := cmp.Diff([]int{200, 200}, fwdStatuses(upstream)); diff != "" {
				t.Fatalf("forwarded (-want +got): %s", diff)
			}
			if got := srvTx.State(); got != sip.TransactionStateAccepted {
				t.Fatalf("upstream transaction state after second 2xx=%v, want %v",
					got, sip.TransactionStateAccepted)
			}

			// The Accepted state is retained only while Timer L allows:
			// once it runs down the transaction retires and nothing may
			// reach the wire through it anymore.
			time.Sleep(sip.TimingConfig{}.TimeL() + time.Second)
			synctest.Wait()
			if got := srvTx.State(); got != sip.TransactionStateTerminated {
				t.Fatalf("upstream transaction state after Timer L=%v, want %v",
					got, sip.TransactionStateTerminated)
			}
		})
	})

	t.Run("failed upstream write reports the error yet leaves the sibling untouched", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			var sendErrs atomic.Int32
			elm, prx, tp := newConfiguredTestProxy(t, accRoute("acc-write-err", "A", "B"), accOpts)
			elm.BindErrorHandler(sip.ErrorHandlerFunc(func(context.Context, error) {
				sendErrs.Add(1)
			}))
			tp.sendResHook = func(_ *sip.ResponseEnvelope, idx int) error {
				if idx == 0 {
					return errors.ErrorWrap("upstream write failed")
				}
				return nil
			}

			inReq := newInInviteReq(t, "UDP", sip.MagicCookie+".acc-write-err", laddr, raddr)
			fwdIntercept(t, elm, prx, inReq)
			synctest.Wait()

			sends := drainReqs(tp)
			if got := len(inviteSends(sends)); got != 2 {
				t.Fatalf("outbound INVITE sends=%d, want 2", got)
			}
			txOf := clnTxsOf(t, elm, sends)

			var activeBranch atomic.Bool
			activeBranch.Store(true)
			txOf["B"].BindStateHandler(sip.TransactionStateHandlerFunc(
				func(_ context.Context, _, to sip.TransactionState) {
					if to == sip.TransactionStateTerminated {
						activeBranch.Store(false)
					}
				},
			))

			// A's provisional write fails on the wire: the failure is
			// reported once and never cancels the unrelated sibling.
			feed(t, ctx, txOf["A"], sip.ResponseStatusRinging)
			if got := sendErrs.Load(); got != 1 {
				t.Fatalf("reported upstream send errors=%d, want 1", got)
			}
			if got := len(drainRess(tp)); got != 0 {
				t.Fatalf("recorded upstream sends after failed write=%d, want 0", got)
			}
			if !activeBranch.Load() {
				t.Fatal("response write failure cancelled unrelated branch")
			}

			// The untouched sibling keeps processing: its provisional and
			// success still forward through the live transaction.
			feed(t, ctx, txOf["B"], sip.ResponseStatusRinging)
			feed(t, ctx, txOf["B"], sip.ResponseStatusOK)
			feed(t, ctx, txOf["A"], sip.ResponseStatusBusyHere)

			upstream := drainRess(tp)
			if diff := cmp.Diff([]int{180, 200}, fwdStatuses(upstream)); diff != "" {
				t.Fatalf("forwarded (-want +got): %s", diff)
			}
			if !activeBranch.Load() {
				t.Fatal("response write failure cancelled unrelated branch")
			}
		})
	})

	t.Run("stray final after upstream termination is silently discarded", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			var sendErrs atomic.Int32
			elm, prx, tp := newConfiguredTestProxy(t, accRoute("acc-late", "A", "B"), accOpts)
			elm.BindErrorHandler(sip.ErrorHandlerFunc(func(context.Context, error) {
				sendErrs.Add(1)
			}))

			inReq := newInInviteReq(t, "UDP", sip.MagicCookie+".acc-late", laddr, raddr)
			fwdIntercept(t, elm, prx, inReq)
			synctest.Wait()

			sends := drainReqs(tp)
			if got := len(inviteSends(sends)); got != 2 {
				t.Fatalf("outbound INVITE sends=%d, want 2", got)
			}
			txOf := clnTxsOf(t, elm, sends)

			// A settles with a non-success final while B stays pending: the
			// committed decision still waits on the sibling.
			feed(t, ctx, txOf["A"], sip.ResponseStatusBusyHere)

			// The upstream transaction goes away before B answers.
			if err := srvTxOf(t, elm, inReq).Terminate(
				ctx, errors.ErrorWrap("upstream gone"),
			); err != nil {
				t.Fatalf("srvTx.Terminate() error = %v, want nil", err)
			}
			synctest.Wait()

			// B's stray final — even a success — is silently discarded per
			// RFC 4320/6026: no new send, no fabricated replacement
			// transaction, no error report.
			feed(t, ctx, txOf["B"], sip.ResponseStatusOK)

			if got := len(drainRess(tp)); got != 0 {
				t.Fatalf("upstream sends after upstream termination=%d, want 0", got)
			}
			if got := sendErrs.Load(); got != 0 {
				t.Fatalf("reported send errors=%d, want 0 (stray final silently discarded)", got)
			}
		})
	})
}
