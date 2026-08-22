package sip_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

func testTransportMetadata(tp sip.TransportProto) sip.TransportMetadata {
	switch tp.Canonic() {
	case "UDP":
		return sip.UDPMetadata()
	case "TCP":
		return sip.TCPMetadata()
	case "TLS":
		return sip.TLSMetadata()
	case "SCTP":
		return sip.SCTPMetadata()
	case "TLS-SCTP":
		return sip.TLSSCTPMetadata()
	case "WS":
		return sip.WSMetadata()
	case "WSS":
		return sip.WSSMetadata()
	default:
		return sip.TransportMetadata{Proto: tp}
	}
}

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
		URI: &sip.URI{
			User: sip.UserWithName("alice"),
			Addr: sip.AddrFromHost("alice.voip.com"),
		},
		Headers: make(sip.Headers).
			Set(header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: tp,
					Addr:      sip.AddrFromHostPort(viaAddr.Addr().String(), viaAddr.Port()),
					Params:    make(sip.Values).Set("branch", branch),
				},
			}).
			Set(&header.From{
				URI:    &sip.URI{User: sip.UserWithName("bob"), Addr: sip.AddrFromHost("bob.voip.com")},
				Params: make(sip.Values).Set("tag", "from-1234"),
			}).
			Set(&header.To{
				URI: &sip.URI{User: sip.UserWithName("alice"), Addr: sip.AddrFromHost("alice.voip.com")},
			}).
			Set(header.CallID("call-1234@bob.voip.com")).
			Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInvite}).
			Set(sip.DefaultMaxForwards).
			Set(&header.Timestamp{RequestTime: time.Now().Add(-time.Second)}),
	}

	return req
}

func newInInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.RequestEnvelope {
	tb.Helper()

	req := sip.NewRequestEnvelope(newInviteReq(tb, tp, branch, rmtAddr)).
		SetTransport(testTransportMetadata(tp)).
		SetLocalAddr(locAddr).
		SetRemoteAddr(rmtAddr)

	return req
}

func newOutInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.RequestEnvelope {
	tb.Helper()

	req := sip.NewRequestEnvelope(newInviteReq(tb, tp, branch, locAddr)).
		SetTransport(testTransportMetadata(tp)).
		SetLocalAddr(locAddr).
		SetRemoteAddr(rmtAddr)

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

func newInAckReq(
	tb testing.TB,
	invite *sip.RequestEnvelope,
	res *sip.ResponseEnvelope,
) *sip.RequestEnvelope {
	tb.Helper()

	req := sip.NewRequestEnvelope(newAckReq(tb, invite.Message(), res.Message())).
		SetTransport(invite.Transport()).
		SetRemoteAddr(invite.RemoteAddr()).
		SetLocalAddr(invite.LocalAddr())

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
) *sip.RequestEnvelope {
	tb.Helper()

	req := sip.NewRequestEnvelope(newNonInviteReq(tb, tp, branch, rmtAddr)).
		SetTransport(testTransportMetadata(tp)).
		SetLocalAddr(locAddr).
		SetRemoteAddr(rmtAddr)

	return req
}

func newOutNonInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.RequestEnvelope {
	tb.Helper()

	req := sip.NewRequestEnvelope(newNonInviteReq(tb, tp, branch, locAddr)).
		SetTransport(testTransportMetadata(tp)).
		SetLocalAddr(locAddr).
		SetRemoteAddr(rmtAddr)

	return req
}

func newInRes(tb testing.TB, req *sip.RequestEnvelope, sts sip.ResponseStatus) *sip.ResponseEnvelope {
	tb.Helper()

	msg, err := req.Message().NewResponse(sts)
	if err != nil {
		tb.Fatalf("failed to create response: %v", err)
	}

	res := sip.NewResponseEnvelope(msg).
		SetTransport(req.Transport()).
		SetRemoteAddr(req.RemoteAddr()).
		SetLocalAddr(req.LocalAddr())

	return res
}

func waitForTransactState(tb testing.TB, tx sip.Transaction, want sip.TransactionState, timeout time.Duration) {
	tb.Helper()

	// Allow the transaction goroutines to run and advance virtual time up to the timeout.
	time.Sleep(timeout)

	if got := tx.(interface{ State() sip.TransactionState }).State(); got != want { //nolint:forcetypeassert
		tb.Fatalf("transaction state did not reach %q, got %q", want, got)
	}
}
