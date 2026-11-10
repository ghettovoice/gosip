package sip_test

import (
	"context"
	"slices"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
)

// prxStubTransp is a full-duplex transport stub: it records outbound request
// sends through the embedded client transport, runs the outbound response
// interceptor chain like a real transport, and records response sends.
type prxStubTransp struct {
	sip.StdMsgInterceptChain

	*stubClnTransp
	md sip.TransportMetadata

	sendResCalls chan sendResCall
	sendResHook  func(res *sip.ResponseEnvelope, idx int) error
	sendResCnt   int
}

func newPrxStubTransp() *prxStubTransp {
	return &prxStubTransp{
		stubClnTransp: newStubClnTransp(false),
		md:            sip.UDPMetadata(),
		sendResCalls:  make(chan sendResCall, 64),
	}
}

func (tp *prxStubTransp) Metadata() sip.TransportMetadata { return tp.md }

// SendResponse mirrors the real transport send path: the outbound response
// interceptors bound to the transport run before the wire send, so a
// proxy-initiated send sees its one-shot token.
func (tp *prxStubTransp) SendResponse(
	ctx context.Context,
	res *sip.ResponseEnvelope,
	opts ...sip.SendResponseOptions,
) error {
	sender := sip.InterceptOutboundResponse(
		slices.Collect(tp.OutboundResponseInterceptors.All()),
		sip.ResponseSenderFunc(tp.sendResponse),
	)
	return errors.Wrap(sender.SendResponse(ctx, res, opts...))
}

// sendResponse is the wire endpoint of the stub: it applies the
// transport-side To-tag default the same way a real transport does, then
// records the send. A hooked failure is reported to the caller without
// recording.
func (tp *prxStubTransp) sendResponse(
	ctx context.Context,
	res *sip.ResponseEnvelope,
	_ ...sip.SendResponseOptions,
) error {
	idx := tp.sendResCnt
	tp.sendResCnt++
	if tp.sendResHook != nil {
		if err := tp.sendResHook(res, idx); err != nil {
			return errors.Wrap(err)
		}
	}

	res.SetTransport(tp.md)
	var tag string
	res.WithMessage(func(msg *sip.Response) {
		tag = sip.GenerateStableToTag(msg, []byte("192.0.2.1:5060"))
	})
	res.EnsureToTag(tag)
	tp.sendResCalls <- sendResCall{ctx: ctx, res: res}
	return nil
}

func (*prxStubTransp) Respond(context.Context, *sip.RequestEnvelope, sip.ResponseStatus, ...sip.RespondOptions) error {
	return nil
}

func (*prxStubTransp) Close() error { return nil }
func (*prxStubTransp) Listen(context.Context, string) (sip.TransportListener, error) {
	return nil, nil
}

func (*prxStubTransp) MatchSentBy(sip.Addr) bool { return true }
