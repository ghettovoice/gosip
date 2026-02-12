package sip_test

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/uri"
)

func newInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	viaAddr netip.AddrPort,
) *sip.Request {
	tb.Helper()

	if branch == "" {
		branch = sip.MagicCookie + ".stub-branch"
	}
	req := &sip.Request{
		Proto:  sip.ProtoVer20(),
		Method: sip.RequestMethodInvite,
		URI: &uri.SIP{
			User: uri.User("alice"),
			Addr: uri.Host("alice.voip.com"),
		},
		Headers: make(sip.Headers).
			Set(header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: tp,
					Addr:      header.HostPort(viaAddr.Addr().String(), viaAddr.Port()),
					Params:    make(header.Values).Set("branch", branch),
				},
			}).
			Set(&header.From{
				URI:    &uri.SIP{User: uri.User("bob"), Addr: uri.Host("bob.voip.com")},
				Params: make(header.Values).Set("tag", "from-1234"),
			}).
			Set(&header.To{
				URI: &uri.SIP{User: uri.User("alice"), Addr: uri.Host("alice.voip.com")},
			}).
			Set(header.CallID("call-1234@bob.voip.com")).
			Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInvite}).
			Set(header.MaxForwards(70)).
			Set(&header.Timestamp{RequestTime: time.Now().Add(-time.Second)}),
	}
	return req
}

func newInInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.InboundRequestEnvelope {
	tb.Helper()

	req, err := sip.NewInboundRequestEnvelope(newInviteReq(tb, tp, branch, rmtAddr), tp, locAddr, rmtAddr)
	if err != nil {
		tb.Fatalf("sip.NewInboundRequestEnvelope() error = %v, want nil", err)
	}
	return req
}

func newOutInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.OutboundRequestEnvelope {
	tb.Helper()

	req, err := sip.NewOutboundRequestEnvelope(newInviteReq(tb, tp, branch, locAddr))
	if err != nil {
		tb.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
	}
	req.SetTransport(tp)
	req.SetLocalAddr(locAddr)
	req.SetRemoteAddr(rmtAddr)
	return req
}

func newAckReq(tb testing.TB, invite *sip.Request, res *sip.Response) *sip.Request {
	tb.Helper()

	ack := invite.Clone().(*sip.Request) //nolint:forcetypeassert
	ack.Method = sip.RequestMethodAck
	if via, ok := ack.Headers.FirstVia(); ok && res.Status.IsSuccessful() {
		if branch, _ := via.Branch(); sip.IsRFC3261Branch(branch) {
			via.Params.Set("branch", branch+".ack")
		}
	}
	if cseq, ok := ack.Headers.CSeq(); ok {
		ack.Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: sip.RequestMethodAck})
	}
	if to, ok := res.Headers.To(); ok {
		ack.Headers.Set(to.Clone())
	}
	return ack
}

func newInAckReq(tb testing.TB, invite *sip.InboundRequestEnvelope, res *sip.OutboundResponseEnvelope) *sip.InboundRequestEnvelope {
	tb.Helper()

	req, err := sip.NewInboundRequestEnvelope(
		newAckReq(tb, invite.Message(), res.Message()),
		invite.Transport(),
		invite.RemoteAddr(),
		invite.LocalAddr(),
	)
	if err != nil {
		tb.Fatalf("sip.NewInboundRequestEnvelope() error = %v, want nil", err)
	}
	return req
}

func newNonInviteReq(
	tb testing.TB,
	proto sip.TransportProto,
	branch string,
	rmtAddr netip.AddrPort,
) *sip.Request {
	tb.Helper()

	req := newInviteReq(tb, proto, branch, rmtAddr)
	req.Method = sip.RequestMethodInfo
	if cseq, ok := req.Headers.CSeq(); ok {
		req.Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: sip.RequestMethodInfo})
	}
	return req
}

func newInNonInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.InboundRequestEnvelope {
	tb.Helper()

	req, err := sip.NewInboundRequestEnvelope(newNonInviteReq(tb, tp, branch, rmtAddr), tp, locAddr, rmtAddr)
	if err != nil {
		tb.Fatalf("sip.NewInboundRequestEnvelope() error = %v, want nil", err)
	}
	return req
}

func newOutNonInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.OutboundRequestEnvelope {
	tb.Helper()

	req, err := sip.NewOutboundRequestEnvelope(newNonInviteReq(tb, tp, branch, locAddr))
	if err != nil {
		tb.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
	}
	req.SetTransport(tp)
	req.SetLocalAddr(locAddr)
	req.SetRemoteAddr(rmtAddr)
	return req
}

func newInRes(tb testing.TB, req *sip.OutboundRequestEnvelope, sts sip.ResponseStatus) *sip.InboundResponseEnvelope {
	tb.Helper()

	msg, err := req.Message().NewResponse(sts, nil)
	if err != nil {
		tb.Fatalf("failed to create response: %v", err)
	}

	res, err := sip.NewInboundResponseEnvelope(msg, req.Transport(), req.LocalAddr(), req.RemoteAddr())
	if err != nil {
		tb.Fatalf("sip.NewInboundResponseEnvelope() error = %v, want nil", err)
	}
	return res
}

//nolint:unparam
func waitForTransactState(tb testing.TB, tx sip.Transaction, want sip.TransactionState, timeout time.Duration) {
	tb.Helper()

	getState := func() sip.TransactionState {
		return tx.(interface{ State() sip.TransactionState }).State() //nolint:forcetypeassert
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if getState() == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	tb.Fatalf("transaction state did not reach %q, got %q", want, getState())
}

type stubTransaction struct {
	typ      sip.TransactionType
	handlers []sip.TransactionStateHandler
}

func (tx *stubTransaction) Type() sip.TransactionType {
	return tx.typ
}

func (*stubTransaction) State() sip.TransactionState {
	return ""
}

func (*stubTransaction) MatchMessage(msg sip.Message) bool {
	// For stub transactions, we'll accept any message for testing purposes
	return true
}

func (tx *stubTransaction) OnStateChanged(fn sip.TransactionStateHandler) (cancel func()) {
	tx.handlers = append(tx.handlers, fn)
	return func() {}
}

func (*stubTransaction) OnError(fn sip.ErrorHandler) (cancel func()) {
	_ = fn
	return func() {}
}

func (*stubTransaction) Terminate(_ context.Context) error {
	return nil
}

func (tx *stubTransaction) fireState(to sip.TransactionState) {
	for _, handler := range tx.handlers {
		if handler != nil {
			handler(context.Background(), "", to)
		}
	}
}

type stubClientTransaction struct {
	stubTransaction
	key        sip.ClientTransactionKey
	recvCalled atomic.Bool
	recvRes    *sip.InboundResponseEnvelope
}

func (tx *stubClientTransaction) Type() sip.TransactionType {
	if tx.typ != "" {
		return tx.typ
	}
	return sip.TransactionTypeClientInvite
}

func (*stubClientTransaction) State() sip.TransactionState {
	return sip.TransactionStateCalling
}

func (tx *stubClientTransaction) Key() sip.ClientTransactionKey {
	return tx.key
}

func (*stubClientTransaction) Request() *sip.OutboundRequestEnvelope {
	return nil
}

func (*stubClientTransaction) LastResponse() *sip.InboundResponseEnvelope {
	return nil
}

func (*stubClientTransaction) Transport() sip.ClientTransport {
	return nil
}

func (tx *stubClientTransaction) RecvResponse(_ context.Context, res *sip.InboundResponseEnvelope) error {
	tx.recvRes = res
	tx.recvCalled.Store(true)
	return nil
}

func (*stubClientTransaction) OnResponse(_ sip.InboundResponseHandler) (cancel func()) {
	return func() {}
}

type stubServerTransaction struct {
	stubTransaction
	key        sip.ServerTransactionKey
	recvCalled atomic.Bool
	recvReq    *sip.InboundRequestEnvelope
}

func (tx *stubServerTransaction) Type() sip.TransactionType {
	if tx.typ != "" {
		return tx.typ
	}
	return sip.TransactionTypeServerInvite
}

func (*stubServerTransaction) State() sip.TransactionState {
	return sip.TransactionStateTrying
}

func (tx *stubServerTransaction) Key() sip.ServerTransactionKey {
	return tx.key
}

func (*stubServerTransaction) Request() *sip.InboundRequestEnvelope {
	return nil
}

func (*stubServerTransaction) LastResponse() *sip.OutboundResponseEnvelope {
	return nil
}

func (*stubServerTransaction) Transport() sip.ServerTransport {
	return nil
}

func (tx *stubServerTransaction) RecvRequest(_ context.Context, req *sip.InboundRequestEnvelope) error {
	tx.recvReq = req
	tx.recvCalled.Store(true)
	return nil
}

func (*stubServerTransaction) SendResponse(
	_ context.Context,
	_ *sip.OutboundResponseEnvelope,
	_ *sip.SendResponseOptions,
) error {
	return nil
}
