package sip_test

import (
	"context"
	"net/netip"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip"
)

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
	res.WithMessage(func(msg *sip.Response) {
		sip.EnsureResponseToTag(msg, sip.GenerateStableToTag(msg, []byte("192.0.2.1:5060")))
	})
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

// prxSentByTransp narrows the permissive stub to explicit advertised addresses.
// The caller selects the transport by protocol before matching its sent-by.
type prxSentByTransp struct {
	*prxStubTransp
	sentBy []sip.Addr
}

func (tp *prxSentByTransp) MatchSentBy(addr sip.Addr) bool {
	return slices.ContainsFunc(tp.sentBy, func(owned sip.Addr) bool {
		return owned.Equal(addr)
	})
}

func (tp *prxStubTransp) countSendRes() int {
	cnt := 0
	for {
		select {
		case <-tp.sendResCalls:
			cnt++
		default:
			return cnt
		}
	}
}

func newTestProxy(
	tb testing.TB,
	route *sip.ProxyRoute,
	elems ...*sip.Element,
) (*sip.Element, *sip.Proxy, *prxStubTransp) {
	tb.Helper()
	return newConfiguredTestProxy(tb, route, sip.ProxyOptions{}, elems...)
}

func newConfiguredTestProxy(
	tb testing.TB,
	route *sip.ProxyRoute,
	opts sip.ProxyOptions,
	elems ...*sip.Element,
) (*sip.Element, *sip.Proxy, *prxStubTransp) {
	tb.Helper()

	tp := newPrxStubTransp()
	elm := &sip.Element{}
	if len(elems) > 0 && elems[len(elems)-1] != nil {
		elm = elems[len(elems)-1]
	}
	tb.Cleanup(func() { elm.Close() })

	if err := elm.TrackTransport(tp); err != nil {
		tb.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	prx, err := sip.NewProxy(elm, []sip.ProxyRoute{*route}, opts)
	if err != nil {
		tb.Fatalf("sip.NewProxy() error = %v, want nil", err)
	}

	return elm, prx, tp
}

func drainPrxReqs(tp *prxStubTransp) []*sip.RequestEnvelope {
	var reqs []*sip.RequestEnvelope
	for {
		select {
		case call := <-tp.sendReqCalls:
			reqs = append(reqs, call.req)
		default:
			return reqs
		}
	}
}

func drainPrxRess(tp *prxStubTransp) []*sip.ResponseEnvelope {
	var ress []*sip.ResponseEnvelope
	for {
		select {
		case call := <-tp.sendResCalls:
			ress = append(ress, call.res)
		default:
			return ress
		}
	}
}

func TestProxy_DuplicateRequestConsumedByExistingTransaction(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	var authorized, resolved, nexted atomic.Int32
	route := &sip.ProxyRoute{
		Name:        "test",
		ForwardMode: sip.ForwardModeStateful,
		AuthorizeRequest: func(context.Context, *sip.RequestEnvelope) error {
			authorized.Add(1)
			return nil
		},
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
			resolved.Add(1)
			return sip.ProxyTargetPlan{}, nil
		},
	}

	elm, prx, tp := newTestProxy(t, route)
	req := newInInviteReq(t, "UDP", sip.MagicCookie+".proxy-dup", laddr, raddr)
	srvTx, err := elm.NewServerTransaction(ctx, req, tp)
	if err != nil {
		t.Fatalf("elm.NewServerTransaction() error = %v, want nil", err)
	}
	if err := srvTx.Start(ctx); err != nil {
		t.Fatalf("srvTx.Start() error = %v, want nil", err)
	}

	res := newInRes(t, req, sip.ResponseStatusRinging)
	if err := srvTx.SendResponse(ctx, res); err != nil {
		t.Fatalf("srvTx.SendResponse() error = %v, want nil", err)
	}
	if got := tp.countSendRes(); got != 1 {
		t.Fatalf("send response calls = %d, want 1", got)
	}

	next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
		nexted.Add(1)
		return nil
	})

	if err := prx.InterceptInboundRequest(ctx, next, req); err != nil {
		t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
	}

	if got := nexted.Load(); got != 0 {
		t.Fatalf("next called %d times, want 0", got)
	}
	if got := authorized.Load(); got != 0 {
		t.Fatalf("route.AuthorizeRequest() called %d times, want 0", got)
	}
	if got := resolved.Load(); got != 0 {
		t.Fatalf("route.ResolveRequestTargets() called %d times, want 0", got)
	}

	if got := tp.countSendRes(); got != 1 {
		t.Fatalf("retransmission re-sent %d responses, want 1", got)
	}
}

func TestProxy_DuplicateRequestNoExistingTransaction(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	var nexted atomic.Int32
	route := &sip.ProxyRoute{
		Name:        "test",
		ForwardMode: sip.ForwardModeStateful,
	}

	_, prx, _ := newTestProxy(t, route, &sip.Element{
		ServerTransactionFactory: sip.ServerTransactionFactoryFunc(
			func(*sip.RequestEnvelope, sip.ServerTransport, ...sip.ServerTransactionOptions) (sip.ServerTransaction, error) {
				return nil, sip.NewTransactionDuplicateError()
			},
		),
	})

	req := newInInviteReq(t, "UDP", sip.MagicCookie+".proxy-gone", laddr, raddr)
	next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
		nexted.Add(1)
		return nil
	})

	err := prx.InterceptInboundRequest(ctx, next, req)
	if err == nil {
		t.Fatal("prx.InterceptInboundRequest() error = nil, want error")
	}
	if got := nexted.Load(); got != 0 {
		t.Fatalf("next called %d times, want 0", got)
	}
}

func TestProxy_StatefulAuthorizationContext(t *testing.T) {
	t.Parallel()

	localAddr := netip.MustParseAddrPort("192.168.1.100:5060")
	remoteAddr := netip.MustParseAddrPort("10.0.0.5:5060")
	authCtxs := make(chan context.Context, 1)
	route := &sip.ProxyRoute{
		Name:        "context",
		ForwardMode: sip.ForwardModeStateful,
		AuthorizeRequest: func(ctx context.Context, _ *sip.RequestEnvelope) error {
			authCtxs <- ctx
			return errors.Wrap(sip.NewMessageNotMatched())
		},
	}
	elm, prx, tp := newTestProxy(t, route)
	req := newInInviteReq(t, "UDP", sip.MagicCookie+".proxy-context", localAddr, remoteAddr)

	err := prx.InterceptInboundRequest(
		t.Context(),
		sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil }),
		req,
	)
	if err == nil {
		t.Fatal("prx.InterceptInboundRequest() error = nil, want authorization error")
	}

	ctx := testutil.WaitResult(t, authCtxs, "proxy authorization context")
	tx, ok := sip.ServerTransactionFromContext(ctx)
	if !ok {
		t.Fatal("sip.ServerTransactionFromContext(authorization context) reports no transaction")
	}
	key, err := sip.MakeServerTransactionKey(req)
	if err != nil {
		t.Fatalf("sip.MakeServerTransactionKey(req) error = %v, want nil", err)
	}
	registered, ok := elm.LoadServerTransaction(key)
	if !ok || registered != tx {
		t.Fatalf("registered transaction = (%v, %t), want authorization transaction %v", registered, ok, tx)
	}
	if got, ok := sip.TransportFromContext(ctx); !ok || got != tp {
		t.Fatalf("sip.TransportFromContext(authorization context) = (%v, %t), want %v", got, ok, tp)
	}
}
