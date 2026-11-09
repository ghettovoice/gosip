package sip_test

import (
	"context"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

func findTpStats(
	report sip.StatsReport,
	proto sip.TransportProto,
	localAddr netip.AddrPort,
) (sip.TransportStats, bool) {
	for _, transportStats := range report.Transports {
		if transportStats.Proto == proto && transportStats.Addr == localAddr.String() {
			return transportStats, true
		}
	}
	return sip.TransportStats{}, false
}

type statsTxState interface {
	fireState(ctx context.Context, to sip.TransactionState)
}

type stubStatsTransact struct {
	stubTxDefaults
	typ   sip.TransactionType
	hdlrs []sip.TransactionStateHandler
}

func (tx *stubStatsTransact) Type() sip.TransactionType {
	return tx.typ
}

func (*stubStatsTransact) State() sip.TransactionState {
	return sip.TransactionStateTrying
}

func (tx *stubStatsTransact) BindStateHandler(handler sip.TransactionStateHandler) (unbind func()) {
	tx.hdlrs = append(tx.hdlrs, handler)
	return func() {}
}

func (*stubStatsTransact) Started() bool { return false }
func (*stubStatsTransact) BindStartHandler(sip.TransactionStartHandler) (unbind func()) {
	return func() {}
}

func (tx *stubStatsTransact) fireState(ctx context.Context, to sip.TransactionState) {
	for _, handler := range tx.hdlrs {
		if handler != nil {
			handler.HandleTransactionState(ctx, 0, to)
		}
	}
}

type stubStatsClnTransact struct {
	*stubStatsTransact
	stubClnTxDefaults
}

func newStubStatsClnTransact(typ sip.TransactionType) *stubStatsClnTransact {
	return &stubStatsClnTransact{
		stubStatsTransact: &stubStatsTransact{typ: typ},
	}
}

func (*stubStatsClnTransact) Start(context.Context) error {
	return nil
}

func (*stubStatsClnTransact) Key() sip.ClientTransactionKey {
	return sip.ClientTransactionKey{}
}

type stubStatsSrvTransact struct {
	*stubStatsTransact
	stubSrvTxDefaults
}

func newStubStatsSrvTransact(typ sip.TransactionType) *stubStatsSrvTransact {
	return &stubStatsSrvTransact{
		stubStatsTransact: &stubStatsTransact{typ: typ},
	}
}

func (*stubStatsSrvTransact) Start(context.Context) error {
	return nil
}

func (*stubStatsSrvTransact) Key() sip.ServerTransactionKey {
	return sip.ServerTransactionKey{}
}

var (
	_ sip.ClientTransaction = (*stubStatsClnTransact)(nil)
	_ sip.ServerTransaction = (*stubStatsSrvTransact)(nil)
)

func TestStatsRecorder_ReportTransportStats(t *testing.T) {
	t.Parallel()

	const transportProto sip.TransportProto = "UDP"
	localAddr := netip.MustParseAddrPort("192.0.2.10:5070")
	remoteAddr := netip.MustParseAddrPort("192.0.2.20:5060")
	stats := &sip.StatsRecorder{}
	ctx := t.Context()

	reqReceiver := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
		return nil
	})
	resReceiver := sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error {
		return nil
	})
	reqSender := sip.RequestSenderFunc(func(context.Context, *sip.RequestEnvelope, ...sip.SendRequestOptions) error {
		return nil
	})
	resSender := sip.ResponseSenderFunc(func(context.Context, *sip.ResponseEnvelope, ...sip.SendResponseOptions) error {
		return nil
	})

	inReq := newInInviteReq(t, transportProto, "", localAddr, remoteAddr)
	if err := stats.InterceptInboundRequest(ctx, reqReceiver, inReq); err != nil {
		t.Fatalf("stats.InterceptInboundRequest() error = %v, want nil", err)
	}

	outReq := newOutInviteReq(t, transportProto, "", localAddr, remoteAddr)
	outReq.WithMessage(func(req *sip.Request) {
		req.Headers.Delete("Timestamp")
	})
	if err := stats.InterceptOutboundRequest(ctx, reqSender, outReq); err != nil {
		t.Fatalf("stats.InterceptOutboundRequest() error = %v, want nil", err)
	}

	hdrs := outReq.Headers().Get("Timestamp")
	if len(hdrs) == 0 {
		t.Fatal("outbound request Timestamp header is missing")
	}
	timestamp, ok := hdrs[0].(*header.Timestamp)
	if !ok || timestamp.RequestTime.IsZero() {
		t.Fatalf("outbound request Timestamp header = %#v, want a timestamp with request time", hdrs[0])
	}

	inRes := newInRes(t, outReq, sip.ResponseStatusRinging)
	if err := stats.InterceptInboundResponse(ctx, resReceiver, inRes); err != nil {
		t.Fatalf("stats.InterceptInboundResponse() error = %v, want nil", err)
	}

	outRes, err := inReq.NewResponse(sip.ResponseStatusOK)
	if err != nil {
		t.Fatalf("inReq.NewResponse() error = %v, want nil", err)
	}
	if err := stats.InterceptOutboundResponse(ctx, resSender, outRes); err != nil {
		t.Fatalf("stats.InterceptOutboundResponse() error = %v, want nil", err)
	}

	resHdrs := outRes.Headers().Get("Timestamp")
	if len(resHdrs) == 0 {
		t.Fatal("outbound response Timestamp header is missing")
	}
	resTimestamp, ok := resHdrs[0].(*header.Timestamp)
	if !ok {
		t.Fatalf("outbound response Timestamp header = %#v, want *header.Timestamp", resHdrs[0])
	}
	if resTimestamp.ResponseDelay <= 0 {
		t.Fatalf("outbound response Timestamp.ResponseDelay = %v, want > 0", resTimestamp.ResponseDelay)
	}

	report := stats.Report()
	if report.Time.IsZero() {
		t.Fatal("stats.Report().Time is zero, want non-zero")
	}

	got, ok := findTpStats(report, transportProto, localAddr)
	if !ok {
		t.Fatalf("stats.Report() transport stats for %s at %s not found", transportProto, localAddr)
	}

	want := sip.TransportStats{
		Proto:             transportProto,
		Addr:              localAddr.String(),
		RequestsReceived:  1,
		RequestsSent:      1,
		ResponsesReceived: 1,
		ResponsesSent:     1,
		NumRTT:            1,
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(sip.TransportStats{}, "AvgRTT")); diff != "" {
		t.Errorf("stats.Report() transport stats mismatch (-want +got):\n%s", diff)
	}
	if got.AvgRTT < 0 {
		t.Errorf("stats.Report() transport AvgRTT = %v, want >= 0", got.AvgRTT)
	}
}

func TestStatsRecorder_ReportTransactionStats(t *testing.T) {
	t.Parallel()

	stats := &sip.StatsRecorder{}
	ctx := t.Context()
	clientInvite := newStubStatsClnTransact(sip.TransactionTypeClientInvite)
	clientNonInvite := newStubStatsClnTransact(sip.TransactionTypeClientNonInvite)
	serverInvite := newStubStatsSrvTransact(sip.TransactionTypeServerInvite)
	serverNonInvite := newStubStatsSrvTransact(sip.TransactionTypeServerNonInvite)

	stats.HandleClientTransaction(ctx, clientInvite)
	stats.HandleClientTransaction(ctx, clientNonInvite)
	stats.HandleServerTransaction(ctx, serverInvite)
	stats.HandleServerTransaction(ctx, serverNonInvite)

	want := sip.TransactionStats{
		InviteClientTransactions:         1,
		NonInviteClientTransactions:      1,
		InviteServerTransactions:         1,
		NonInviteServerTransactions:      1,
		InviteClientTransactionsTotal:    1,
		NonInviteClientTransactionsTotal: 1,
		InviteServerTransactionsTotal:    1,
		NonInviteServerTransactionsTotal: 1,
	}
	got := stats.Report().Transactions
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("stats.Report() transaction stats mismatch (-want +got):\n%s", diff)
	}

	for _, tx := range []statsTxState{clientInvite, clientNonInvite, serverInvite, serverNonInvite} {
		tx.fireState(ctx, sip.TransactionStateTerminated)
	}

	want = sip.TransactionStats{
		InviteClientTransactionsTotal:    1,
		NonInviteClientTransactionsTotal: 1,
		InviteServerTransactionsTotal:    1,
		NonInviteServerTransactionsTotal: 1,
	}
	got = stats.Report().Transactions
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("stats.Report() transaction stats after termination mismatch (-want +got):\n%s", diff)
	}
}

func TestStatsRecorder_UseMiddleware_TransportStats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		mwBeforeTrack bool
	}{
		{"middleware_before_transport", true},
		{"middleware_after_transport", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const tpProto sip.TransportProto = "UDP"
			locAddr := netip.MustParseAddrPort("192.0.2.10:5070")
			rmtAddr := netip.MustParseAddrPort("192.0.2.20:5060")
			elm := &sip.Element{}
			stats := &sip.StatsRecorder{}
			tp := newPrxStubTransp()

			t.Cleanup(func() { _ = elm.Close() })

			var unbind func()

			if tt.mwBeforeTrack {
				unbind = elm.UseMiddleware(stats)
			}
			if err := elm.TrackTransport(tp); err != nil {
				t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
			}
			if !tt.mwBeforeTrack {
				unbind = elm.UseMiddleware(stats)
			}

			runTrafficRound := func(round string, expectTstamp bool) {
				t.Helper()

				var nextCalls struct {
					InboundRequest   int
					InboundResponse  int
					OutboundRequest  int
					OutboundResponse int
				}
				inReqReceiver := sip.InterceptInboundRequest(
					slices.Collect(tp.InboundRequestInterceptors.All()),
					sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
						nextCalls.InboundRequest++
						return nil
					}),
				)
				inResReceiver := sip.InterceptInboundResponse(
					slices.Collect(tp.InboundResponseInterceptors.All()),
					sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error {
						nextCalls.InboundResponse++
						return nil
					}),
				)
				outReqSender := sip.InterceptOutboundRequest(
					slices.Collect(tp.OutboundRequestInterceptors.All()),
					sip.RequestSenderFunc(func(context.Context, *sip.RequestEnvelope, ...sip.SendRequestOptions) error {
						nextCalls.OutboundRequest++
						return nil
					}),
				)
				outResSender := sip.InterceptOutboundResponse(
					slices.Collect(tp.OutboundResponseInterceptors.All()),
					sip.ResponseSenderFunc(func(context.Context, *sip.ResponseEnvelope, ...sip.SendResponseOptions) error {
						nextCalls.OutboundResponse++
						return nil
					}),
				)

				branch := sip.MagicCookie + ".stats-middleware-" + tt.name + "-" + round
				outReq := newOutInviteReq(t, tpProto, branch+"-out", locAddr, rmtAddr)
				outReq.WithMessage(func(req *sip.Request) {
					req.Headers.Delete("Timestamp")
				})
				if err := outReqSender.SendRequest(t.Context(), outReq); err != nil {
					t.Fatalf("outReqSender.SendRequest() error = %v, want nil", err)
				}

				tstampHdrs := outReq.Headers().Get("Timestamp")
				if expectTstamp {
					if len(tstampHdrs) == 0 {
						t.Fatal("outbound request Timestamp header is missing")
					}
					ts, ok := tstampHdrs[0].(*header.Timestamp)
					if !ok || ts.RequestTime.IsZero() {
						t.Fatalf("outbound request Timestamp header = %#v, want a timestamp with request time", tstampHdrs[0])
					}
				} else if len(tstampHdrs) != 0 {
					t.Fatalf("outbound request Timestamp headers = %#v, want none after middleware unbind", tstampHdrs)
				}

				inRes := newInRes(t, outReq, sip.ResponseStatusRinging)
				if err := inResReceiver.RecvResponse(t.Context(), inRes); err != nil {
					t.Fatalf("inResReceiver.RecvResponse() error = %v, want nil", err)
				}

				inReq := newInInviteReq(t, tpProto, branch+"-in", locAddr, rmtAddr)
				if err := inReqReceiver.RecvRequest(t.Context(), inReq); err != nil {
					t.Fatalf("inReqReceiver.RecvRequest() error = %v, want nil", err)
				}

				outRes, err := inReq.NewResponse(sip.ResponseStatusOK)
				if err != nil {
					t.Fatalf("inReq.NewResponse() error = %v, want nil", err)
				}
				if err := outResSender.SendResponse(t.Context(), outRes); err != nil {
					t.Fatalf("outResSender.SendResponse() error = %v, want nil", err)
				}

				wantNextCalls := struct {
					InboundRequest   int
					InboundResponse  int
					OutboundRequest  int
					OutboundResponse int
				}{1, 1, 1, 1}
				if diff := cmp.Diff(wantNextCalls, nextCalls); diff != "" {
					t.Errorf("interceptor pipeline next calls mismatch (-want +got):\n%s", diff)
				}
			}

			runTrafficRound("initial", true)
			wantTpStats := sip.TransportStats{
				Proto:             tpProto,
				Addr:              locAddr.String(),
				RequestsReceived:  1,
				RequestsSent:      1,
				ResponsesReceived: 1,
				ResponsesSent:     1,
				NumRTT:            1,
			}
			report := stats.Report()
			if len(report.Transports) != 1 {
				t.Fatalf("stats.Report() transport entries = %d, want 1", len(report.Transports))
			}
			if diff := cmp.Diff(
				wantTpStats,
				report.Transports[0],
				cmpopts.IgnoreFields(sip.TransportStats{}, "AvgRTT"),
			); diff != "" {
				t.Errorf("stats.Report() transport stats mismatch (-want +got):\n%s", diff)
			}

			unbind()
			unbind()

			runTrafficRound("after-unbind", false)

			report = stats.Report()
			if len(report.Transports) != 1 {
				t.Fatalf("stats.Report() transport entries after middleware unbind = %d, want 1", len(report.Transports))
			}
			if diff := cmp.Diff(
				wantTpStats,
				report.Transports[0],
				cmpopts.IgnoreFields(sip.TransportStats{}, "AvgRTT"),
			); diff != "" {
				t.Errorf("stats.Report() transport stats after middleware unbind mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStatsRecorder_UseMiddleware_TransactionStats(t *testing.T) {
	t.Parallel()

	const tpProto sip.TransportProto = "UDP"
	locAddr := netip.MustParseAddrPort("192.0.2.10:5070")
	rmtAddr := netip.MustParseAddrPort("192.0.2.20:5060")
	ctx := t.Context()
	elm := &sip.Element{}
	stats := &sip.StatsRecorder{}
	tp := newPrxStubTransp()

	t.Cleanup(func() { _ = elm.Close() })

	unbind := elm.UseMiddleware(stats)

	newTxs := func(suffix string) []sip.Transaction {
		t.Helper()

		clnInv, err := elm.NewClientTransaction(
			ctx,
			newOutInviteReq(t, tpProto, sip.MagicCookie+".stats-client-invite-"+suffix, locAddr, rmtAddr),
			tp,
		)
		if err != nil {
			t.Fatalf("elm.NewClientTransaction() invite error = %v, want nil", err)
		}

		clnNonInv, err := elm.NewClientTransaction(
			ctx,
			newOutNonInviteReq(t, tpProto, sip.MagicCookie+".stats-client-non-invite-"+suffix, locAddr, rmtAddr),
			tp,
		)
		if err != nil {
			t.Fatalf("elm.NewClientTransaction() non-invite error = %v, want nil", err)
		}

		srvInv, err := elm.NewServerTransaction(
			ctx,
			newInInviteReq(t, tpProto, sip.MagicCookie+".stats-server-invite-"+suffix, locAddr, rmtAddr),
			tp,
		)
		if err != nil {
			t.Fatalf("elm.NewServerTransaction() invite error = %v, want nil", err)
		}

		srvNonInv, err := elm.NewServerTransaction(
			ctx,
			newInNonInviteReq(t, tpProto, sip.MagicCookie+".stats-server-non-invite-"+suffix, locAddr, rmtAddr),
			tp,
		)
		if err != nil {
			t.Fatalf("elm.NewServerTransaction() non-invite error = %v, want nil", err)
		}

		return []sip.Transaction{clnInv, clnNonInv, srvInv, srvNonInv}
	}
	assertStats := func(stage string, want sip.TransactionStats) {
		t.Helper()

		got := stats.Report().Transactions
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("stats.Report() transaction stats %s mismatch (-want +got):\n%s", stage, diff)
		}
	}

	initTxs := newTxs("initial")
	assertStats("after creation", sip.TransactionStats{
		InviteClientTransactions:         1,
		NonInviteClientTransactions:      1,
		InviteServerTransactions:         1,
		NonInviteServerTransactions:      1,
		InviteClientTransactionsTotal:    1,
		NonInviteClientTransactionsTotal: 1,
		InviteServerTransactionsTotal:    1,
		NonInviteServerTransactionsTotal: 1,
	})

	unbind()
	unbind()

	for _, tx := range initTxs {
		if err := tx.Terminate(ctx, nil); err != nil {
			t.Fatalf("tx.Terminate() error = %v, want nil", err)
		}
	}

	wantTermStats := sip.TransactionStats{
		InviteClientTransactionsTotal:    1,
		NonInviteClientTransactionsTotal: 1,
		InviteServerTransactionsTotal:    1,
		NonInviteServerTransactionsTotal: 1,
	}
	assertStats("after termination", wantTermStats)

	newTxs("after-unbind")
	assertStats("after middleware unbind", wantTermStats)
}

func TestStatsRecorder_Proxy(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	opts := sip.ProxyOptions{ServedDomains: []string{"alice.voip.com"}}

	t.Run("three forks count one handled request, three attempts, one forward", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			stats := &sip.StatsRecorder{}
			tp := newPrxStubTransp()
			elm := &sip.Element{}
			t.Cleanup(func() { _ = elm.Close() })

			if err := elm.TrackTransport(tp); err != nil {
				t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
			}
			// The recorder serves the middleware role bound ahead of the
			// proxy, so the inbound request is counted before the route
			// claim consumes it.
			unbindMw := elm.UseMiddleware(stats)

			prx, err := sip.NewProxy(
				elm,
				[]sip.ProxyRoute{*obsRoute("stats-fork", obsLitPlan("A", "B", "C"))},
				opts,
			)
			if err != nil {
				t.Fatalf("sip.NewProxy() error = %v, want nil", err)
			}
			unbindEv := prx.BindEventHandler(stats)
			t.Cleanup(func() {
				unbindEv()
				unbindMw()
			})

			// Drive the wire-level inbound path: the middleware intercepts
			// the request first, then the proxy claims it.
			inRecv := sip.InterceptInboundRequest(
				slices.Collect(tp.InboundRequestInterceptors.All()),
				sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
					return nil
				}),
			)
			if err := inRecv.RecvRequest(
				ctx,
				newInInviteReq(t, "UDP", sip.MagicCookie+".stats-fork", laddr, raddr),
			); err != nil {
				t.Fatalf("inbound RecvRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			sends := drainPrxReqs(tp)
			if got := len(sends); got != 3 {
				t.Fatalf("outbound sends=%d, want 3", got)
			}

			report := stats.Report()
			want := sip.ProxyStats{
				RequestsHandled:          1,
				RequestsForwarded:        1,
				ForwardAttemptsSucceeded: 3,
			}
			if diff := cmp.Diff(want, report.Proxy); diff != "" {
				t.Fatalf("proxy stats (-want +got): %s", diff)
			}

			// The same recorder's middleware counters keep their own
			// meanings: one upstream server transaction and three
			// downstream client transactions, one inbound request on the
			// transport — no proxy event leaks into them.
			wantTxs := sip.TransactionStats{
				InviteClientTransactions:      3,
				InviteServerTransactions:      1,
				InviteClientTransactionsTotal: 3,
				InviteServerTransactionsTotal: 1,
			}
			if diff := cmp.Diff(wantTxs, report.Transactions); diff != "" {
				t.Fatalf("transaction stats (-want +got): %s", diff)
			}
			tpStats, ok := findTpStats(report, "UDP", laddr)
			if !ok {
				t.Fatalf("transport stats for UDP at %s not found", laddr)
			}
			if tpStats.RequestsReceived != 1 || tpStats.RequestsSent != 0 ||
				tpStats.ResponsesReceived != 0 || tpStats.ResponsesSent != 0 {
				t.Fatalf("transport stats = %+v, want only RequestsReceived=1", tpStats)
			}

			// A downstream 403 settles the request with the best final
			// response: it is not an attempt failure, and its upstream
			// forwarding counts one forwarded response plus one completion
			// without a rejection/timeout/cancel reason.
			for i, outReq := range sends {
				tx := fwdClnTx(t, elm, outReq)
				if err := tx.RecvResponse(ctx, newInRes(t, outReq, sip.ResponseStatusForbidden)); err != nil {
					t.Fatalf("tx.RecvResponse(403) sends[%d] error = %v, want nil", i, err)
				}
				synctest.Wait()
			}
			if got := len(drainPrxRess(tp)); got != 1 {
				t.Fatalf("upstream responses=%d, want 1 (best 403)", got)
			}

			report = stats.Report()
			want = sip.ProxyStats{
				RequestsHandled:          1,
				RequestsForwarded:        1,
				ForwardAttemptsSucceeded: 3,
				ResponsesForwarded:       1,
				RequestsCompleted:        1,
			}
			if diff := cmp.Diff(want, report.Proxy); diff != "" {
				t.Fatalf("proxy stats after 403 (-want +got): %s", diff)
			}
			// The forwarded 403 carries the upstream addressing of the held
			// request, so the middleware charges its send to the transport
			// entry alongside the counted inbound request.
			tpStats, ok = findTpStats(report, "UDP", laddr)
			if !ok || tpStats.RequestsReceived != 1 || tpStats.ResponsesSent != 1 {
				t.Fatalf("transport stats after 403 = %+v, want RequestsReceived=1 and ResponsesSent=1", tpStats)
			}
		})
	})

	t.Run("a failed initial send is one failed attempt without a forward", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			stats := &sip.StatsRecorder{}
			_, prx, tp := newConfiguredTestProxy(t, obsRoute("stats-fail", obsLitPlan("A")), opts)
			unbind := prx.BindEventHandler(stats)
			t.Cleanup(unbind)
			injErr := errors.ErrorWrap("udp write failed")
			tp.sendReqHook = func(sendReqCall, int) error { return injErr }

			fwdIntercept(t, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".stats-fail", laddr, raddr))
			synctest.Wait()

			want := sip.ProxyStats{
				RequestsHandled:       1,
				ForwardAttemptsFailed: 1,
				RequestsCompleted:     1,
			}
			if diff := cmp.Diff(want, stats.Report().Proxy); diff != "" {
				t.Fatalf("proxy stats (-want +got): %s", diff)
			}
		})
	})

	t.Run("completion outcomes increment only their own reason counter", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			outcome sip.ProxyRequestOutcome
			want    sip.ProxyStats
		}{
			{"response", sip.ProxyOutcomeResponse, sip.ProxyStats{RequestsCompleted: 1}},
			{"rejected", sip.ProxyOutcomeRejected, sip.ProxyStats{RequestsCompleted: 1, RequestsRejected: 1}},
			{"timeout", sip.ProxyOutcomeTimeout, sip.ProxyStats{RequestsCompleted: 1, RequestsTimedOut: 1}},
			{"cancelled", sip.ProxyOutcomeCancelled, sip.ProxyStats{RequestsCompleted: 1, RequestsCancelled: 1}},
			{"send failure", sip.ProxyOutcomeSendFailure, sip.ProxyStats{RequestsCompleted: 1}},
			{"abandoned", sip.ProxyOutcomeAbandoned, sip.ProxyStats{RequestsCompleted: 1}},
			{"sent", sip.ProxyOutcomeSent, sip.ProxyStats{RequestsCompleted: 1}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				stats := &sip.StatsRecorder{}
				stats.HandleProxyEvent(t.Context(), sip.ProxyEvent{
					Kind:    sip.ProxyEventRequestCompleted,
					Outcome: tt.outcome,
				})

				if diff := cmp.Diff(tt.want, stats.Report().Proxy); diff != "" {
					t.Fatalf("proxy stats (-want +got): %s", diff)
				}
			})
		}
	})

	t.Run("concurrent event updates and reports are race free", func(t *testing.T) {
		t.Parallel()

		stats := &sip.StatsRecorder{}
		ctx := t.Context()

		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				for range 250 {
					stats.HandleProxyEvent(ctx, sip.ProxyEvent{Kind: sip.ProxyEventForwardAttemptFinished})
					stats.HandleProxyEvent(ctx, sip.ProxyEvent{
						Kind:  sip.ProxyEventForwardAttemptFinished,
						Error: sip.ErrMessageTooLarge,
					})
					stats.HandleProxyEvent(ctx, sip.ProxyEvent{
						Kind:    sip.ProxyEventRequestCompleted,
						Outcome: sip.ProxyOutcomeTimeout,
					})
				}
			})
		}
		for range 4 {
			wg.Go(func() {
				for range 250 {
					_ = stats.Report()
				}
			})
		}
		wg.Wait()

		want := sip.ProxyStats{
			ForwardAttemptsSucceeded: 2000,
			ForwardAttemptsFailed:    2000,
			RequestsCompleted:        2000,
			RequestsTimedOut:         2000,
		}
		if diff := cmp.Diff(want, stats.Report().Proxy); diff != "" {
			t.Fatalf("proxy stats (-want +got): %s", diff)
		}
	})

	t.Run("unbind stops future updates", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			stats := &sip.StatsRecorder{}
			_, prx, tp := newConfiguredTestProxy(t, obsRoute("stats-unbind", obsLitPlan("A")), opts)
			unbind := prx.BindEventHandler(stats)

			fwdIntercept(t, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".stats-unbind-1", laddr, raddr))
			synctest.Wait()
			if got := len(drainPrxReqs(tp)); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}

			unbind()
			unbind()

			fwdIntercept(t, prx, newInInviteReq(t, "UDP", sip.MagicCookie+".stats-unbind-2", laddr, raddr))
			synctest.Wait()
			if got := len(drainPrxReqs(tp)); got != 1 {
				t.Fatalf("outbound sends after unbind=%d, want 1", got)
			}

			want := sip.ProxyStats{
				RequestsHandled:          1,
				RequestsForwarded:        1,
				ForwardAttemptsSucceeded: 1,
			}
			if diff := cmp.Diff(want, stats.Report().Proxy); diff != "" {
				t.Fatalf("proxy stats (-want +got): %s", diff)
			}
		})
	})
}
