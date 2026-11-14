package stats_test

import (
	"context"
	"net/netip"
	"slices"
	"sync"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil/siptest"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/proxy"
)

type sendReqCall struct {
	ctx  context.Context
	req  *sip.RequestEnvelope
	opts sip.SendRequestOptions
}

// stubClnTransp is a client transport stub recording outbound request sends.
type stubClnTransp struct {
	reliable bool

	sendReqCalls chan sendReqCall

	mu          sync.Mutex
	sendReqHook func(sendReqCall, int) error
	sendReqCnt  int
}

func newStubClnTransp(reliable bool) *stubClnTransp {
	return &stubClnTransp{
		reliable:     reliable,
		sendReqCalls: make(chan sendReqCall, 64),
	}
}

func (tp *stubClnTransp) Metadata() sip.TransportMetadata {
	if tp == nil {
		return sip.TransportMetadata{}
	}

	var flags sip.TransportFlags

	flags.SetReliable(tp.reliable)

	return sip.TransportMetadata{Flags: flags}
}

func (tp *stubClnTransp) SendRequest(
	ctx context.Context,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	var reqCopy *sip.RequestEnvelope
	if req != nil {
		reqCopy = req.Clone().(*sip.RequestEnvelope)
	}

	call := sendReqCall{
		ctx:  ctx,
		req:  reqCopy,
		opts: util.LastSliceElem(opts, sip.SendRequestOptions{}),
	}

	tp.mu.Lock()
	idx := tp.sendReqCnt
	tp.sendReqCnt++
	hook := tp.sendReqHook
	tp.mu.Unlock()

	if hook != nil {
		if err := hook(call, idx); err != nil {
			return errors.Wrap(err)
		}
	}

	tp.sendReqCalls <- call

	return nil
}

type sendResCall struct {
	ctx context.Context
	res *sip.ResponseEnvelope
}

// stubTransp is a full-duplex transport stub: it records outbound request
// sends through the embedded client transport, runs the outbound response
// interceptor chain like a real transport, and records response sends.
type stubTransp struct {
	sip.StdMsgInterceptChain

	*stubClnTransp
	md sip.TransportMetadata

	sendResCalls chan sendResCall
	sendResHook  func(res *sip.ResponseEnvelope, idx int) error
	sendResCnt   int
}

func newStubTransp() *stubTransp {
	return &stubTransp{
		stubClnTransp: newStubClnTransp(false),
		md:            sip.UDPMetadata(),
		sendResCalls:  make(chan sendResCall, 64),
	}
}

func (tp *stubTransp) Metadata() sip.TransportMetadata { return tp.md }

func (tp *stubTransp) SendResponse(
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

func (tp *stubTransp) sendResponse(
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

func (*stubTransp) Respond(context.Context, *sip.RequestEnvelope, sip.ResponseStatus, ...sip.RespondOptions) error {
	return nil
}

func (*stubTransp) Close() error { return nil }
func (*stubTransp) Listen(context.Context, string) (sip.TransportListener, error) {
	return nil, nil
}

func (*stubTransp) MatchSentBy(sip.Addr) bool { return true }

func newConfiguredTestProxy(
	tb testing.TB,
	route *proxy.Route,
	opts proxy.Options,
	elems ...*sip.Element,
) (*sip.Element, *proxy.Proxy, *stubTransp) {
	tb.Helper()

	tp := newStubTransp()
	elm := &sip.Element{}
	if len(elems) > 0 && elems[len(elems)-1] != nil {
		elm = elems[len(elems)-1]
	}
	tb.Cleanup(func() { elm.Close() })

	if err := elm.TrackTransport(tp); err != nil {
		tb.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	prx, err := proxy.New([]proxy.Route{*route}, opts)
	if err != nil {
		tb.Fatalf("proxy.New() error = %v, want nil", err)
	}
	unbind := elm.UseComponent(prx)
	tb.Cleanup(func() {
		_ = prx.Close()
		unbind()
	})

	return elm, prx, tp
}

func drainReqs(tp *stubTransp) []*sip.RequestEnvelope {
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

func drainRess(tp *stubTransp) []*sip.ResponseEnvelope {
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

// obsLitPlan builds one flat fork plan of literal-IP targets.
func obsLitPlan(ids ...string) proxy.TargetPlan {
	hosts := map[string]string{
		"A": "10.0.0.9", "B": "10.0.0.10", "C": "10.0.0.11",
	}
	targets := make([]proxy.Target, 0, len(ids))
	for _, id := range ids {
		targets = append(targets, proxy.Target{
			ID:  id,
			URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr(hosts[id], 5060)},
		})
	}
	return proxy.TargetPlan{Groups: [][]proxy.Target{targets}}
}

func obsRoute(name string, plan proxy.TargetPlan) *proxy.Route {
	return &proxy.Route{
		Name:        name,
		ForwardMode: proxy.ForwardModeStateful,
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
			return plan, nil
		},
	}
}

// fwdIntercept admits one inbound request through the proxy interceptor.
func fwdIntercept(tb testing.TB, elm *sip.Element, prx *proxy.Proxy, req *sip.RequestEnvelope) {
	tb.Helper()

	next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil })
	if err := prx.InterceptInboundRequest(sip.ContextWithElement(tb.Context(), elm), next, req); err != nil {
		tb.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
	}
}

func newInInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.RequestEnvelope {
	tb.Helper()

	return siptest.NewInboundInvite(tb, tp, branch, locAddr, rmtAddr)
}

func newOutInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.RequestEnvelope {
	tb.Helper()

	return siptest.NewInboundInvite(tb, tp, branch, locAddr, locAddr).
		SetRemoteAddr(rmtAddr)
}

func newInRes(tb testing.TB, req *sip.RequestEnvelope, sts sip.ResponseStatus) *sip.ResponseEnvelope {
	tb.Helper()

	return siptest.NewInboundResponse(tb, req, sts)
}

// newInNonInviteReq builds an inbound non-INVITE (INFO) request envelope on
// the inbound INVITE fixture: only the method and the CSeq method differ.
func newInNonInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.RequestEnvelope {
	tb.Helper()

	return markReqNonInvite(newInInviteReq(tb, tp, branch, locAddr, rmtAddr))
}

// newOutNonInviteReq is the outbound counterpart of [newInNonInviteReq].
func newOutNonInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.RequestEnvelope {
	tb.Helper()

	return markReqNonInvite(newOutInviteReq(tb, tp, branch, locAddr, rmtAddr))
}

func markReqNonInvite(req *sip.RequestEnvelope) *sip.RequestEnvelope {
	req.WithMessage(func(msg *sip.Request) {
		msg.Method = sip.RequestMethodInfo
		if cseq, ok := msg.Headers.CSeq(); ok {
			msg.Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: sip.RequestMethodInfo})
		}
	})
	return req
}

// fwdClnTx loads the registered downstream client transaction of a
// captured outbound request.
func fwdClnTx(tb testing.TB, elm *sip.Element, outReq *sip.RequestEnvelope) sip.ClientTransaction {
	tb.Helper()

	key, err := sip.MakeClientTransactionKey(outReq)
	if err != nil {
		tb.Fatalf("sip.MakeClientTransactionKey(outReq) error = %v, want nil", err)
	}
	tx, ok := elm.LoadClientTransaction(key)
	if !ok {
		tb.Fatal("forwarding client transaction not registered")
	}
	return tx
}

type stubTxDefaults struct{}

func (stubTxDefaults) MatchMessage(sip.Message) bool            { return true }
func (stubTxDefaults) LastError() error                         { return nil }
func (stubTxDefaults) BindErrorHandler(sip.ErrorHandler) func() { return func() {} }
func (stubTxDefaults) Terminate(context.Context, error) error   { return nil }

type stubClnTxDefaults struct{}

func (stubClnTxDefaults) Request() *sip.RequestEnvelope { return nil }
func (stubClnTxDefaults) LastResponse() *sip.ResponseEnvelope {
	return nil
}

func (stubClnTxDefaults) Transport() sip.ClientTransport { return nil }
func (stubClnTxDefaults) RecvResponse(context.Context, *sip.ResponseEnvelope) error {
	return nil
}

func (stubClnTxDefaults) BindResponseHandler(sip.InboundResponseHandler) func() {
	return func() {}
}

type stubSrvTxDefaults struct{}

func (stubSrvTxDefaults) Request() *sip.RequestEnvelope { return nil }
func (stubSrvTxDefaults) LastResponse() *sip.ResponseEnvelope {
	return nil
}

func (stubSrvTxDefaults) Transport() sip.ServerTransport { return nil }
func (stubSrvTxDefaults) RecvRequest(context.Context, *sip.RequestEnvelope) error {
	return nil
}

func (stubSrvTxDefaults) SendResponse(
	context.Context,
	*sip.ResponseEnvelope,
	...sip.SendResponseOptions,
) error {
	return nil
}

func (stubSrvTxDefaults) Respond(context.Context, sip.ResponseStatus, ...sip.RespondOptions) error {
	return nil
}
