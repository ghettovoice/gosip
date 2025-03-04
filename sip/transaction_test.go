package sip_test

import (
	"net/netip"
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
	rmtAddr netip.AddrPort,
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
					Addr:      header.HostPort(rmtAddr.Addr().String(), rmtAddr.Port()),
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
			Set(header.MaxForwards(70)),
	}
	return req
}

func newInInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.InboundRequest {
	tb.Helper()
	return sip.NewInboundRequest(newInviteReq(tb, tp, branch, rmtAddr), locAddr, rmtAddr)
}

func newOutInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.OutboundRequest {
	tb.Helper()

	req := sip.NewOutboundRequest(newInviteReq(tb, tp, branch, rmtAddr))
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

func newInAckReq(tb testing.TB, invite *sip.InboundRequest, res *sip.OutboundResponse) *sip.InboundRequest {
	tb.Helper()

	return sip.NewInboundRequest(
		newAckReq(tb, invite.Message(), res.Message()),
		invite.RemoteAddr(),
		invite.LocalAddr(),
	)
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
) *sip.InboundRequest {
	tb.Helper()
	return sip.NewInboundRequest(newNonInviteReq(tb, tp, branch, rmtAddr), locAddr, rmtAddr)
}

func newOutNonInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.OutboundRequest {
	tb.Helper()

	req := sip.NewOutboundRequest(newNonInviteReq(tb, tp, branch, rmtAddr))
	req.SetLocalAddr(locAddr)
	req.SetRemoteAddr(rmtAddr)
	return req
}

func newInRes(tb testing.TB, req *sip.OutboundRequest, sts sip.ResponseStatus) *sip.InboundResponse {
	tb.Helper()

	msg, err := req.Message().NewResponse(sts, nil)
	if err != nil {
		tb.Fatalf("failed to create response: %v", err)
	}

	return sip.NewInboundResponse(msg, req.LocalAddr(), req.RemoteAddr())
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
