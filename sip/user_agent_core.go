package sip

import (
	"context"
)

type UserAgentCore struct {
	txl *TransactionLayer
	tpl *TransportLayer
}

func (ua *UserAgentCore) OnRequest() {}

func (ua *UserAgentCore) SendRequest() {}

// func (ua *UserAgentCore) Listen(ctx context.Context, tp TransportProto, laddr netip.AddrPort) error {
// 	_, err := ua.tpl.Listen(ctx, tp, laddr, nil)
// 	if err != nil {
// 		return errtrace.Wrap(err)
// 	}
// 	return nil
// }

func (ua *UserAgentCore) Run(ctx context.Context) error {
	go ua.tpl.Serve()
	return nil
}

func (ua *UserAgentCore) Stop(ctx context.Context) error {
	ua.tpl.Close()
	return nil
}
