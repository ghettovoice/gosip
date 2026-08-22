package sip_test

import (
	"context"
	"net/netip"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
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

	got, ok := findTransportStats(report, transportProto, localAddr)
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
	clientInvite := newStatsClientTransactionStub(sip.TransactionTypeClientInvite)
	clientNonInvite := newStatsClientTransactionStub(sip.TransactionTypeClientNonInvite)
	serverInvite := newStatsServerTransactionStub(sip.TransactionTypeServerInvite)
	serverNonInvite := newStatsServerTransactionStub(sip.TransactionTypeServerNonInvite)

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

	for _, tx := range []statsTransactionLifecycle{clientInvite, clientNonInvite, serverInvite, serverNonInvite} {
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

func findTransportStats(
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

type statsTransactionLifecycle interface {
	fireState(ctx context.Context, to sip.TransactionState)
}

type statsTransactionStub struct {
	typ      sip.TransactionType
	handlers []sip.TransactionStateHandler
}

func (tx *statsTransactionStub) Type() sip.TransactionType {
	return tx.typ
}

func (*statsTransactionStub) State() sip.TransactionState {
	return sip.TransactionStateTrying
}

func (*statsTransactionStub) MatchMessage(sip.Message) bool {
	return true
}

func (*statsTransactionStub) LastError() error {
	return nil
}

func (tx *statsTransactionStub) BindStateHandler(handler sip.TransactionStateHandler) (unbind func()) {
	tx.handlers = append(tx.handlers, handler)
	return func() {}
}

func (*statsTransactionStub) BindErrorHandler(sip.ErrorHandler) (unbind func()) {
	return func() {}
}

func (*statsTransactionStub) Terminate(context.Context, error) error {
	return nil
}

func (tx *statsTransactionStub) fireState(ctx context.Context, to sip.TransactionState) {
	for _, handler := range tx.handlers {
		if handler != nil {
			handler.HandleTransactionState(ctx, 0, to)
		}
	}
}

type statsClientTransactionStub struct {
	*statsTransactionStub
}

func newStatsClientTransactionStub(typ sip.TransactionType) *statsClientTransactionStub {
	return &statsClientTransactionStub{
		statsTransactionStub: &statsTransactionStub{typ: typ},
	}
}

func (*statsClientTransactionStub) RecvResponse(context.Context, *sip.ResponseEnvelope) error {
	return nil
}

func (*statsClientTransactionStub) Start(context.Context) error {
	return nil
}

func (*statsClientTransactionStub) Key() sip.ClientTransactionKey {
	return sip.ClientTransactionKey{}
}

func (*statsClientTransactionStub) Request() *sip.RequestEnvelope {
	return nil
}

func (*statsClientTransactionStub) LastResponse() *sip.ResponseEnvelope {
	return nil
}

func (*statsClientTransactionStub) Transport() sip.ClientTransport {
	return nil
}

func (*statsClientTransactionStub) BindResponseHandler(sip.InboundResponseHandler) (unbind func()) {
	return func() {}
}

type statsServerTransactionStub struct {
	*statsTransactionStub
}

func newStatsServerTransactionStub(typ sip.TransactionType) *statsServerTransactionStub {
	return &statsServerTransactionStub{
		statsTransactionStub: &statsTransactionStub{typ: typ},
	}
}

func (*statsServerTransactionStub) RecvRequest(context.Context, *sip.RequestEnvelope) error {
	return nil
}

func (*statsServerTransactionStub) SendResponse(
	context.Context,
	*sip.ResponseEnvelope,
	...sip.SendResponseOptions,
) error {
	return nil
}

func (*statsServerTransactionStub) Start(context.Context) error {
	return nil
}

func (*statsServerTransactionStub) Key() sip.ServerTransactionKey {
	return sip.ServerTransactionKey{}
}

func (*statsServerTransactionStub) Request() *sip.RequestEnvelope {
	return nil
}

func (*statsServerTransactionStub) LastResponse() *sip.ResponseEnvelope {
	return nil
}

func (*statsServerTransactionStub) Transport() sip.ServerTransport {
	return nil
}

func (*statsServerTransactionStub) Respond(
	context.Context,
	sip.ResponseStatus,
	...sip.RespondOptions,
) error {
	return nil
}

var (
	_ sip.ClientTransaction = (*statsClientTransactionStub)(nil)
	_ sip.ServerTransaction = (*statsServerTransactionStub)(nil)
)
