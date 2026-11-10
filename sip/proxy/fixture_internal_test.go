package proxy

import (
	"net/netip"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// newInternalInviteReq builds the fixed inbound INVITE used by white-box
// proxy tests: one UDP Via hop carrying branch, delivered from
// 192.168.1.100:5060 to the wildcard listen address.
func newInternalInviteReq(branch string) *sip.RequestEnvelope {
	viaAddr := netip.MustParseAddrPort("192.168.1.100:5060")
	req := &sip.Request{
		Proto:  sip.ProtoVer20(),
		Method: sip.RequestMethodInvite,
		URI:    &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.voip.com")},
		Headers: make(sip.Headers).
			Set(header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "UDP",
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
			Set(sip.DefaultMaxForwards),
	}
	return sip.NewRequestEnvelope(req).
		SetTransport(sip.UDPMetadata()).
		SetLocalAddr(netip.MustParseAddrPort("0.0.0.0:5060")).
		SetRemoteAddr(netip.MustParseAddrPort("192.168.1.100:5060"))
}

// srvReqCount returns the number of requests indexed under elm's scope.
func srvReqCount(prx *Proxy, elm *sip.Element) int {
	es := prx.getElemState(elm, false)
	if es == nil {
		return 0
	}
	es.reqsMu.Lock()
	defer es.reqsMu.Unlock()
	return len(es.srvRqs)
}
