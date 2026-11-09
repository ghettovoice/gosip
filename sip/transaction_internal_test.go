package sip

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip/header"
)

type stubClnTransp struct{}

func (stubClnTransp) Metadata() TransportMetadata { return UDPMetadata() }
func (stubClnTransp) SendRequest(context.Context, *RequestEnvelope, ...SendRequestOptions) error {
	return nil
}

func newInternalInviteReq(branch string) *RequestEnvelope {
	viaAddr := netip.MustParseAddrPort("192.168.1.100:5060")
	req := &Request{
		Proto:  ProtoVer20(),
		Method: RequestMethodInvite,
		URI:    &URI{User: MakeUserInfo("alice"), Addr: MakeHostAddr("alice.voip.com")},
		Headers: make(Headers).
			Set(header.Via{
				{
					Proto:     ProtoVer20(),
					Transport: "UDP",
					Addr:      MakeHostPortAddr(viaAddr.Addr().String(), viaAddr.Port()),
					Params:    make(Values).Set("branch", branch),
				},
			}).
			Set(&header.From{
				URI:    &URI{User: MakeUserInfo("bob"), Addr: MakeHostAddr("bob.voip.com")},
				Params: make(Values).Set("tag", "from-1234"),
			}).
			Set(&header.To{
				URI: &URI{User: MakeUserInfo("alice"), Addr: MakeHostAddr("alice.voip.com")},
			}).
			Set(header.CallID("call-1234@bob.voip.com")).
			Set(&header.CSeq{SeqNum: 1, Method: RequestMethodInvite}).
			Set(DefaultMaxForwards),
	}
	return NewRequestEnvelope(req).
		SetTransport(UDPMetadata()).
		SetLocalAddr(netip.MustParseAddrPort("0.0.0.0:5060")).
		SetRemoteAddr(netip.MustParseAddrPort("192.168.1.100:5060"))
}

func TestTransaction_State_SetterClosesTerminalMarker(t *testing.T) {
	t.Parallel()

	tx, err := NewInviteClientTransaction(
		newInternalInviteReq(MagicCookie+".term-marker"), stubClnTransp{},
	)
	if err != nil {
		t.Fatalf("NewInviteClientTransaction() error = %v, want nil", err)
	}

	if err := tx.fsm.FireCtx(t.Context(), txEvtTerminate, errors.ErrorWrap("test")); err != nil {
		t.Fatalf("tx.fsm.FireCtx(terminate) error = %v, want nil", err)
	}

	select {
	case <-tx.terminated:
	case <-time.After(5 * time.Second):
		t.Fatal("termCh not closed after terminated state store")
	}
	if err := tx.Start(t.Context()); !errors.Is(err, ErrTransactionActionNotAllowed) {
		t.Fatalf("tx.Start() error = %v, want %v", err, ErrTransactionActionNotAllowed)
	}
}
