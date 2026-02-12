package sip_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/sip"
)

func TestStatsRecorder_ReportTransportStats(t *testing.T) {
	t.Parallel()

	stats := &sip.StatsRecorder{}
	tp := newStubTransport("UDP", 5070)
	ctx := sip.ContextWithTransport(t.Context(), tp)
	reqReceiver := sip.RequestReceiverFunc(func(_ context.Context, _ *sip.InboundRequestEnvelope) error {
		return nil
	})
	resReceiver := sip.ResponseReceiverFunc(func(_ context.Context, _ *sip.InboundResponseEnvelope) error {
		return nil
	})
	reqSender := sip.RequestSenderFunc(func(_ context.Context, _ *sip.OutboundRequestEnvelope, _ *sip.SendRequestOptions) error {
		return nil
	})
	resSender := sip.ResponseSenderFunc(func(_ context.Context, _ *sip.OutboundResponseEnvelope, _ *sip.SendResponseOptions) error {
		return nil
	})

	raddr := netip.MustParseAddrPort("192.168.1.10:5060")
	inReq := newInInviteReq(t, tp.Proto(), "", tp.LocalAddr(), raddr)
	if err := stats.InboundRequestInterceptor().InterceptInboundRequest(ctx, inReq, reqReceiver); err != nil {
		t.Fatalf("InboundRequestInterceptor.InterceptInboundRequest() error = %v", err)
	}

	outReq := newOutInviteReq(t, tp.Proto(), "", tp.LocalAddr(), raddr)
	if err := stats.OutboundRequestInterceptor().InterceptOutboundRequest(ctx, outReq, nil, reqSender); err != nil {
		t.Fatalf("OutboundRequestInterceptor.InterceptOutboundRequest() error = %v", err)
	}

	hdrs := outReq.Headers().Get("Timestamp")
	if len(hdrs) == 0 {
		t.Fatalf("Timestamp header not set")
	}
	if ts, ok := hdrs[0].(*header.Timestamp); !ok || ts.RequestTime.IsZero() {
		t.Fatalf("Timestamp header invalid: %#v", hdrs[0])
	}

	inRes := newInRes(t, outReq, sip.ResponseStatusRinging)
	if err := stats.InboundResponseInterceptor().InterceptInboundResponse(ctx, inRes, resReceiver); err != nil {
		t.Fatalf("InboundResponseInterceptor.InterceptInboundResponse() error = %v", err)
	}

	outRes, err := inReq.NewResponse(sip.ResponseStatusOK, nil)
	if err != nil {
		t.Fatalf("inReq.NewResponse() error = %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := stats.OutboundResponseInterceptor().InterceptOutboundResponse(ctx, outRes, nil, resSender); err != nil {
		t.Fatalf("OutboundResponseInterceptor.InterceptOutboundResponse() error = %v", err)
	}

	resHdrs := outRes.Headers().Get("Timestamp")
	if len(resHdrs) == 0 {
		t.Fatalf("Timestamp header missing in response")
	}
	resTS, ok := resHdrs[0].(*header.Timestamp)
	if !ok {
		t.Fatalf("Timestamp header invalid: %#v", resHdrs[0])
	}
	if resTS.ResponseDelay <= 0 {
		t.Fatalf("Timestamp.ResponseDelay = %v, want > 0", resTS.ResponseDelay)
	}

	report := stats.Report()
	if report.Time.IsZero() {
		t.Fatalf("report.Time is zero")
	}

	got, ok := findTransportStats(report, tp.Proto(), tp.LocalAddr())
	if !ok {
		t.Fatalf("transport stats not found")
	}

	if got.RequestsReceived != 1 {
		t.Fatalf("report.RequestsReceived = %d, want 1", got.RequestsReceived)
	}
	if got.RequestsSent != 1 {
		t.Fatalf("report.RequestsSent = %d, want 1", got.RequestsSent)
	}
	if got.ResponsesReceived != 1 {
		t.Fatalf("report.ResponsesReceived = %d, want 1", got.ResponsesReceived)
	}
	if got.ResponsesSent != 1 {
		t.Fatalf("report.ResponsesSent = %d, want 1", got.ResponsesSent)
	}
	if got.NumRTT != 1 {
		t.Fatalf("report.NumRTT = %d, want 1", got.NumRTT)
	}
	if got.AvgRTT < 0 {
		t.Fatalf("report.AvgRTT = %v, want >= 0", got.AvgRTT)
	}
}

func TestStatsRecorder_ReportTransactionStats(t *testing.T) {
	t.Parallel()

	stats := &sip.StatsRecorder{}
	hdlrs := &stubTransactionInitHandlers{}

	unbind := stats.BindTransactionInitHandlers(hdlrs)
	t.Cleanup(unbind)

	clientTx := &stubClientTransaction{stubTransaction: stubTransaction{typ: sip.TransactionTypeClientInvite}}
	serverTx := &stubServerTransaction{stubTransaction: stubTransaction{typ: sip.TransactionTypeServerNonInvite}}

	hdlrs.fireClient(t.Context(), clientTx)
	hdlrs.fireServer(t.Context(), serverTx)

	report := stats.Report()
	if report.Transactions.InviteClientTransactions != 1 {
		t.Fatalf("report.InviteClientTransactions = %d, want 1", report.Transactions.InviteClientTransactions)
	}
	if report.Transactions.NonInviteServerTransactions != 1 {
		t.Fatalf("report.NonInviteServerTransactions = %d, want 1", report.Transactions.NonInviteServerTransactions)
	}
	if report.Transactions.InviteClientTransactionsTotal != 1 {
		t.Fatalf("report.InviteClientTransactionsTotal = %d, want 1", report.Transactions.InviteClientTransactionsTotal)
	}
	if report.Transactions.NonInviteServerTransactionsTotal != 1 {
		t.Fatalf("report.NonInviteServerTransactionsTotal = %d, want 1", report.Transactions.NonInviteServerTransactionsTotal)
	}

	clientTx.fireState(sip.TransactionStateTerminated)
	serverTx.fireState(sip.TransactionStateTerminated)

	finalReport := stats.Report()
	if finalReport.Transactions.InviteClientTransactions != 0 {
		t.Fatalf("report.InviteClientTransactions = %d, want 0", finalReport.Transactions.InviteClientTransactions)
	}
	if finalReport.Transactions.NonInviteServerTransactions != 0 {
		t.Fatalf("report.NonInviteServerTransactions = %d, want 0", finalReport.Transactions.NonInviteServerTransactions)
	}
	// Total counters should remain unchanged after termination
	if finalReport.Transactions.InviteClientTransactionsTotal != 1 {
		t.Fatalf("report.InviteClientTransactionsTotal = %d, want 1", finalReport.Transactions.InviteClientTransactionsTotal)
	}
	if finalReport.Transactions.NonInviteServerTransactionsTotal != 1 {
		t.Fatalf("report.NonInviteServerTransactionsTotal = %d, want 1", finalReport.Transactions.NonInviteServerTransactionsTotal)
	}
}

func findTransportStats(report sip.StatsReport, proto sip.TransportProto, laddr netip.AddrPort) (sip.TransportStats, bool) {
	for _, stats := range report.Transports {
		if stats.Proto == proto && stats.LocalAddr == laddr.String() {
			return stats, true
		}
	}
	return sip.TransportStats{}, false
}

type stubTransactionInitHandlers struct {
	clientHandlers []sip.ClientTransactionHandler
	serverHandlers []sip.ServerTransactionHandler
}

func (hdlrs *stubTransactionInitHandlers) OnNewClientTransaction(fn sip.ClientTransactionHandler) (unbind func()) {
	hdlrs.clientHandlers = append(hdlrs.clientHandlers, fn)
	return func() {}
}

func (hdlrs *stubTransactionInitHandlers) OnNewServerTransaction(fn sip.ServerTransactionHandler) (unbind func()) {
	hdlrs.serverHandlers = append(hdlrs.serverHandlers, fn)
	return func() {}
}

func (hdlrs *stubTransactionInitHandlers) fireClient(ctx context.Context, tx sip.ClientTransaction) {
	for _, handler := range hdlrs.clientHandlers {
		handler(ctx, tx)
	}
}

func (hdlrs *stubTransactionInitHandlers) fireServer(ctx context.Context, tx sip.ServerTransaction) {
	for _, handler := range hdlrs.serverHandlers {
		handler(ctx, tx)
	}
}
