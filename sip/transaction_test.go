package sip_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
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
			Addr: uri.AddrFromHost("alice.voip.com"),
		},
		Headers: make(sip.Headers).
			Set(header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: tp,
					Addr:      header.AddrFromHostPort(viaAddr.Addr().String(), viaAddr.Port()),
					Params:    make(header.Values).Set("branch", branch),
				},
			}).
			Set(&header.From{
				URI:    &uri.SIP{User: uri.User("bob"), Addr: uri.AddrFromHost("bob.voip.com")},
				Params: make(header.Values).Set("tag", "from-1234"),
			}).
			Set(&header.To{
				URI: &uri.SIP{User: uri.User("alice"), Addr: uri.AddrFromHost("alice.voip.com")},
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

	req, err := sip.NewInboundRequestEnvelope(
		newInviteReq(tb, tp, branch, rmtAddr),
		tp,
		locAddr,
		rmtAddr,
	)
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

	req, err := sip.NewInboundRequestEnvelope(
		newNonInviteReq(tb, tp, branch, rmtAddr),
		tp,
		locAddr,
		rmtAddr,
	)
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

	res, err := sip.NewInboundResponseEnvelope(msg, req.Transport(), req.RemoteAddr(), req.LocalAddr())
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
