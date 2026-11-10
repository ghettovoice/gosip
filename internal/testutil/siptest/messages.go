package siptest

import (
	"net/netip"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// NewInboundInvite builds an inbound INVITE request envelope addressed from
// a remote UAC to a local UAS. The request carries a single Via hop for
// remote, a generated default branch when branch is empty, and a Timestamp
// header one second in the past so response-delay measurements are
// deterministic.
func NewInboundInvite(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	local, remote netip.AddrPort,
) *sip.RequestEnvelope {
	tb.Helper()

	req := sip.NewRequestEnvelope(newInviteReq(tb, tp, branch, remote)).
		SetTransport(transportMeta(tp)).
		SetLocalAddr(local).
		SetRemoteAddr(remote)

	return req
}

// NewInboundResponse builds an inbound response envelope for req with the
// given status, sharing the request's transport and addresses.
func NewInboundResponse(
	tb testing.TB,
	req *sip.RequestEnvelope,
	status sip.ResponseStatus,
) *sip.ResponseEnvelope {
	tb.Helper()

	msg, err := req.Message().NewResponse(status)
	if err != nil {
		tb.Fatalf("failed to create response: %v", err)
	}

	res := sip.NewResponseEnvelope(msg).
		SetTransport(req.Transport()).
		SetRemoteAddr(req.RemoteAddr()).
		SetLocalAddr(req.LocalAddr())

	return res
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
			User: sip.MakeUserInfo("alice"),
			Addr: sip.MakeHostAddr("alice.voip.com"),
		},
		Headers: make(sip.Headers).
			Set(header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: tp,
					Addr:      sip.MakeHostPortAddr(viaAddr.Addr().String(), viaAddr.Port()),
					Params:    make(sip.Values).Set("branch", branch),
				},
			}).
			Set(&header.From{
				URI:    &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("bob.voip.com")},
				Params: make(sip.Values).Set("tag", "from-1234"),
			}).
			Set(&header.To{
				URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.voip.com")},
			}).
			Set(header.CallID("call-1234@bob.voip.com")).
			Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInvite}).
			Set(sip.DefaultMaxForwards).
			Set(&header.Timestamp{RequestTime: time.Now().Add(-time.Second)}),
	}

	return req
}

func transportMeta(tp sip.TransportProto) sip.TransportMetadata {
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
