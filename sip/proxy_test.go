package sip_test

import (
	"context"
	"iter"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip"
)

type proxyStubTransport struct {
	sip.StdMsgInterceptChain
	*stubClientTransport

	md sip.TransportMetadata

	sendResCalls chan sendResCall
}

func newProxyStubTransport() *proxyStubTransport {
	return &proxyStubTransport{
		stubClientTransport: newStubClientTransport(false),
		md:                  sip.UDPMetadata(),
		sendResCalls:        make(chan sendResCall, 64),
	}
}

func (tp *proxyStubTransport) Metadata() sip.TransportMetadata { return tp.md }

func (tp *proxyStubTransport) SendResponse(
	ctx context.Context,
	res *sip.ResponseEnvelope,
	opts ...sip.SendResponseOptions,
) error {
	tp.sendResCalls <- sendResCall{ctx: ctx, res: res}
	return nil
}

func (*proxyStubTransport) Respond(context.Context, *sip.RequestEnvelope, sip.ResponseStatus, ...sip.RespondOptions) error {
	return nil
}

func (*proxyStubTransport) Close() error { return nil }

func (*proxyStubTransport) Listen(context.Context, string) (sip.TransportListener, error) {
	return nil, nil
}

func (*proxyStubTransport) MatchSentBy(sip.Addr) bool { return true }

func (tp *proxyStubTransport) countSendRes() int {
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
) (*sip.Element, *sip.Proxy, *proxyStubTransport) {
	tb.Helper()

	tp := newProxyStubTransport()

	elm := &sip.Element{}
	if len(elems) > 0 && elems[len(elems)-1] != nil {
		elm = elems[len(elems)-1]
	}
	tb.Cleanup(func() { elm.Close() })

	if err := elm.TrackTransport(tp); err != nil {
		tb.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	if route.MatchRequest == nil {
		route.MatchRequest = func(context.Context, *sip.ProxyRoute, *sip.RequestEnvelope) bool {
			return true
		}
	}
	if route.ServerTransactionOptions == nil {
		route.ServerTransactionOptions = func(context.Context, *sip.ProxyRoute, *sip.RequestEnvelope) sip.ServerTransactionOptions {
			return sip.ServerTransactionOptions{}
		}
	}

	prx, err := sip.NewProxy(elm, []sip.ProxyRoute{*route})
	if err != nil {
		tb.Fatalf("sip.NewProxy() error = %v, want nil", err)
	}

	return elm, prx, tp
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
		AuthorizeRequest: func(context.Context, *sip.ProxyRoute, *sip.RequestEnvelope) error {
			authorized.Add(1)
			return nil
		},
		ResolveRequestTargets: func(context.Context, *sip.ProxyRoute, *sip.RequestEnvelope) (iter.Seq[[]*sip.URI], error) {
			resolved.Add(1)
			return func(func([]*sip.URI) bool) {}, nil
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
		t.Fatalf("AuthorizeRequest called %d times, want 0", got)
	}
	if got := resolved.Load(); got != 0 {
		t.Fatalf("ResolveRequestTargets called %d times, want 0", got)
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
		AuthorizeRequest: func(ctx context.Context, _ *sip.ProxyRoute, _ *sip.RequestEnvelope) error {
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
