package proxy_test

import (
	"context"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/proxy"
)

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

// preparationTransp exercises transport finalization in the bounded lock regression.
type preparationTransp struct{ *stubTransp }

func (tp *preparationTransp) SendRequest(ctx context.Context, req *sip.RequestEnvelope, opts ...sip.SendRequestOptions) error {
	// Preparation snapshots provenance before acquiring the message lock.
	req.PrepareVia(tp.md.Proto, sip.MakeHostPortAddr("192.0.2.1", 5060))
	req.EnsureFromTag()
	return errors.Wrap(tp.stubClnTransp.SendRequest(ctx, req, opts...))
}

// SendRequest mirrors the real transport send path: the outbound request
// interceptors bound to the transport run before the wire send, so a
// transaction retransmission sees its ownership token claim.
func (tp *stubTransp) SendRequest(
	ctx context.Context,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	sender := sip.InterceptOutboundRequest(
		slices.Collect(tp.OutboundRequestInterceptors.All()),
		sip.RequestSenderFunc(tp.stubClnTransp.SendRequest),
	)
	return errors.Wrap(sender.SendRequest(ctx, req, opts...))
}

// SendResponse mirrors the real transport send path: the outbound response
// interceptors bound to the transport run before the wire send, so a
// proxy-initiated send sees its one-shot token.
func (tp *stubTransp) SendResponse(
	ctx context.Context,
	res *sip.ResponseEnvelope,
	opts ...sip.SendResponseOptions,
) error {
	sender := sip.InterceptOutboundResponse(
		slices.Collect(tp.OutboundResponseInterceptors.All()),
		sip.ResponseSenderFunc(tp.sendRes),
	)
	return errors.Wrap(sender.SendResponse(ctx, res, opts...))
}

// sendRes is the wire endpoint of the stub: it applies the
// transport-side To-tag default the same way a real transport does, then
// records the send. A hooked failure is reported to the caller without
// recording.
func (tp *stubTransp) sendRes(
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

// sentByTransp narrows the permissive stub to explicit advertised addresses.
// The caller selects the transport by protocol before matching its sent-by.
type sentByTransp struct {
	*stubTransp
	sentBy []sip.Addr
}

func (tp *sentByTransp) MatchSentBy(addr sip.Addr) bool {
	return slices.ContainsFunc(tp.sentBy, func(owned sip.Addr) bool {
		return owned.Equal(addr)
	})
}

func (tp *stubTransp) countSendRes() int {
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
	route *proxy.Route,
	elems ...*sip.Element,
) (*sip.Element, *proxy.Proxy, *stubTransp) {
	tb.Helper()
	return newConfiguredTestProxy(tb, route, proxy.Options{}, elems...)
}

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
		// Retire the proxy's owned work before detaching the registration.
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

func TestProxy_DuplicateRequestConsumedByExistingTransaction(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	var authorized, resolved, nexted atomic.Int32
	route := &proxy.Route{
		Name:        "test",
		ForwardMode: proxy.ForwardModeStateful,
		AuthorizeRequest: func(context.Context, *sip.RequestEnvelope) error {
			authorized.Add(1)
			return nil
		},
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
			resolved.Add(1)
			return proxy.TargetPlan{}, nil
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

	if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), next, req); err != nil {
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
	route := &proxy.Route{
		Name:        "test",
		ForwardMode: proxy.ForwardModeStateful,
	}

	elm, prx, _ := newTestProxy(t, route, &sip.Element{
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

	err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), next, req)
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
	route := &proxy.Route{
		Name:        "context",
		ForwardMode: proxy.ForwardModeStateful,
		AuthorizeRequest: func(ctx context.Context, _ *sip.RequestEnvelope) error {
			authCtxs <- ctx
			return errors.Wrap(sip.NewMessageNotMatched())
		},
	}
	elm, prx, tp := newTestProxy(t, route)
	req := newInInviteReq(t, "UDP", sip.MagicCookie+".proxy-context", localAddr, remoteAddr)

	err := prx.InterceptInboundRequest(
		sip.ContextWithElement(t.Context(), elm),
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

func TestProxy_NewIsPure(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	t.Run("construction installs no binding", func(t *testing.T) {
		t.Parallel()

		var srvTxCalls, clnTxCalls atomic.Int32
		elm := &sip.Element{
			ServerTransactionFactory: sip.ServerTransactionFactoryFunc(
				func(*sip.RequestEnvelope, sip.ServerTransport, ...sip.ServerTransactionOptions) (sip.ServerTransaction, error) {
					srvTxCalls.Add(1)
					return nil, errors.ErrorWrap("spy server transaction factory")
				},
			),
			ClientTransactionFactory: sip.ClientTransactionFactoryFunc(
				func(*sip.RequestEnvelope, sip.ClientTransport, ...sip.ClientTransactionOptions) (sip.ClientTransaction, error) {
					clnTxCalls.Add(1)
					return nil, errors.ErrorWrap("spy client transaction factory")
				},
			),
		}
		t.Cleanup(func() { elm.Close() })
		tp := newStubTransp()
		if err := elm.TrackTransport(tp); err != nil {
			t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
		}
		chainLens := func() [4]int {
			return [4]int{
				tp.InboundRequestInterceptors.Len(),
				tp.InboundResponseInterceptors.Len(),
				tp.OutboundRequestInterceptors.Len(),
				tp.OutboundResponseInterceptors.Len(),
			}
		}
		before := chainLens()

		prx, err := proxy.New([]proxy.Route{{Name: "r"}})
		if err != nil {
			t.Fatalf("proxy.New() error = %v, want nil", err)
		}
		if prx == nil {
			t.Fatal("proxy.New() returned nil proxy")
		}

		// A valid construction installs no component, transaction, or
		// transport binding: every spy double records zero calls and the
		// element-bound interceptor chains stay untouched.
		if got := srvTxCalls.Load() + clnTxCalls.Load(); got != 0 {
			t.Fatalf("transaction factory calls=%d, want 0", got)
		}
		if got := chainLens(); got != before {
			t.Fatalf("interceptor chains=%v, want unchanged %v", got, before)
		}

		// Without an explicit registration the proxy does not intercept:
		// a matching request delivered through the element chain passes
		// through to the next receiver unclaimed.
		var nexted atomic.Int32
		next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
			nexted.Add(1)
			return nil
		})
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".pure-unbound", laddr, raddr)
		inRecv := sip.InterceptInboundRequest(
			slices.Collect(tp.InboundRequestInterceptors.All()),
			next,
		)
		if err := inRecv.RecvRequest(t.Context(), req); err != nil {
			t.Fatalf("unbound recv.RecvRequest() error = %v, want nil", err)
		}
		if got := nexted.Load(); got != 1 {
			t.Fatalf("unbound next calls=%d, want 1", got)
		}
		if got := srvTxCalls.Load(); got != 0 {
			t.Fatalf("unbound server transaction factory calls=%d, want 0", got)
		}
		if md, ok := proxy.MetadataFrom(req.Metadata()); ok {
			t.Fatalf("unbound delivery claimed the request route, stamp = %+v", md)
		}

		// The explicit UseComponent registration is what binds the proxy
		// to the element's processing chain.
		unbind := elm.UseComponent(prx)
		t.Cleanup(unbind)
		inRecv = sip.InterceptInboundRequest(
			slices.Collect(tp.InboundRequestInterceptors.All()),
			next,
		)
		req2 := newInInviteReq(t, "UDP", sip.MagicCookie+".pure-bound", laddr, raddr)
		_ = inRecv.RecvRequest(t.Context(), req2)
		if got := nexted.Load(); got != 1 {
			t.Fatalf("bound next calls=%d, want 1 (request claimed)", got)
		}
		if md, ok := proxy.MetadataFrom(req2.Metadata()); !ok || md.RouteName != "r" {
			t.Fatalf("route claim stamp = (%+v, %t), want route name %q", md, ok, "r")
		}
		if got := srvTxCalls.Load(); got != 1 {
			t.Fatalf("bound server transaction factory calls=%d, want 1", got)
		}
	})

	t.Run("invalid configuration fails with zero attachment", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			routes []proxy.Route
			opts   proxy.Options
		}{
			{
				name:   "duplicate route names",
				routes: []proxy.Route{{Name: "r"}, {Name: "r"}},
			},
			{
				name:   "unsupported forward mode",
				routes: []proxy.Route{{Name: "r", ForwardMode: proxy.ForwardMode(2)}},
			},
			{
				name:   "stateless route without key",
				routes: []proxy.Route{{Name: "r", ForwardMode: proxy.ForwardModeStateless}},
			},
			{
				name:   "invalid timer c",
				routes: []proxy.Route{{Name: "r"}},
				opts:   proxy.Options{TimerC: 3 * time.Minute},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				elm := &sip.Element{}
				t.Cleanup(func() { elm.Close() })
				tp := newStubTransp()
				if err := elm.TrackTransport(tp); err != nil {
					t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
				}
				before := tp.InboundRequestInterceptors.Len() +
					tp.InboundResponseInterceptors.Len() +
					tp.OutboundRequestInterceptors.Len() +
					tp.OutboundResponseInterceptors.Len()

				if _, err := proxy.New(tt.routes, tt.opts); err == nil {
					t.Fatal("proxy.New() error = nil, want error")
				}
				got := tp.InboundRequestInterceptors.Len() +
					tp.InboundResponseInterceptors.Len() +
					tp.OutboundRequestInterceptors.Len() +
					tp.OutboundResponseInterceptors.Len()
				if got != before {
					t.Fatalf("interceptor bindings=%d, want unchanged %d", got, before)
				}
			})
		}
	})

	t.Run("caller configuration is owned", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			var resolved atomic.Int32
			identity := &sip.URI{
				User: sip.MakeUserInfo("proxy"),
				Addr: sip.MakeHostPortAddr("192.0.2.1", 5060),
			}
			routes := []proxy.Route{{
				Name:        "owned",
				RecordRoute: true,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					resolved.Add(1)
					return proxy.TargetPlan{Groups: [][]proxy.Target{
						{{
							ID:  "A",
							URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr("10.0.0.9", 5060)},
						}},
					}}, nil
				},
			}}
			opts := proxy.Options{
				LocalIdentities: []*sip.URI{identity},
				ServedDomains:   []string{"alice.voip.com"},
			}

			elm := &sip.Element{}
			t.Cleanup(func() { elm.Close() })
			tp := newStubTransp()
			if err := elm.TrackTransport(tp); err != nil {
				t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
			}
			prx, err := proxy.New(routes, opts)
			if err != nil {
				t.Fatalf("proxy.New() error = %v, want nil", err)
			}
			unbind := elm.UseComponent(prx)
			t.Cleanup(unbind)

			// Mutating every caller-owned input must not alter the
			// configuration the constructor already copied.
			routes[0].Name = "mutated"
			routes[0].RecordRoute = false
			routes[0].MatchRequest = func(context.Context, *sip.RequestEnvelope) bool { return false }
			opts.ServedDomains[0] = "mutated.example.com"
			identity.Addr = sip.MakeHostPortAddr("mutated.invalid", 5060)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".pure-owned", laddr, raddr)
			err = prx.InterceptInboundRequest(
				sip.ContextWithElement(t.Context(), elm),
				sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil }),
				req,
			)
			if err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			if md, ok := proxy.MetadataFrom(req.Metadata()); !ok || md.RouteName != "owned" {
				t.Fatalf("route claim stamp = (%+v, %t), want route name %q", md, ok, "owned")
			}
			if got := resolved.Load(); got != 1 {
				t.Fatalf("route.ResolveRequestTargets() calls=%d, want 1 (served domain still responsible)", got)
			}
			sends := drainReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("outbound sends=%d, want 1", len(sends))
			}
			var rrURI *sip.URI
			sends[0].WithMessage(func(r *sip.Request) {
				if hop, ok := r.Headers.FirstRecordRoute(); ok {
					if u, ok := hop.URI.(*sip.URI); ok {
						rrURI = u
					}
				}
			})
			if rrURI == nil {
				t.Fatal("forwarded request has no Record-Route URI")
			}
			if got := rrURI.Addr.Host(); got != "192.0.2.1" {
				t.Fatalf("Record-Route host=%q, want %q (owned local identity)", got, "192.0.2.1")
			}
		})
	})

	t.Run("stateless key is owned", func(t *testing.T) {
		t.Parallel()

		key := []byte("stable-stateless-key")
		routes := []proxy.Route{{
			Name:        "sl-owned",
			ForwardMode: proxy.ForwardModeStateless,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{
					{{
						ID:  "A",
						URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostPortAddr("10.0.0.9", 5060)},
					}},
				}}, nil
			},
		}}

		elm := &sip.Element{}
		t.Cleanup(func() { elm.Close() })
		tp := newStubTransp()
		if err := elm.TrackTransport(tp); err != nil {
			t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
		}
		prx, err := proxy.New(routes, proxy.Options{StatelessKey: key, ServedDomains: []string{"alice.voip.com"}})
		if err != nil {
			t.Fatalf("proxy.New() error = %v, want nil", err)
		}
		unbind := elm.UseComponent(prx)
		t.Cleanup(unbind)

		branchOf := func(req *sip.RequestEnvelope) string {
			t.Helper()
			var branch string
			req.WithMessage(func(r *sip.Request) {
				if via, ok := r.Headers.FirstVia(); ok {
					branch, _ = via.Branch()
				}
			})
			return branch
		}
		send := func(branchTag string) string {
			t.Helper()
			req := newInInviteReq(t, "UDP", branchTag, laddr, raddr)
			if err := prx.InterceptInboundRequest(
				sip.ContextWithElement(t.Context(), elm),
				sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil }),
				req,
			); err != nil {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			sends := drainReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("outbound sends=%d, want 1", len(sends))
			}
			return branchOf(sends[0])
		}

		first := send(sip.MagicCookie + ".pure-key")
		// Mutating the caller's key bytes must not alter the copy the
		// constructor already owns.
		key[0] = 'X'
		if second := send(sip.MagicCookie + ".pure-key"); second != first {
			t.Fatalf("stateless branch changed after caller mutation: %q -> %q", first, second)
		}
	})
}

func TestProxy_MissingElement(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	t.Run("claimed request fails before state or traffic", func(t *testing.T) {
		t.Parallel()

		var nexted atomic.Int32
		route := &proxy.Route{
			Name:        "claimed",
			ForwardMode: proxy.ForwardModeStateful,
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{}, nil
			},
		}
		elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
			ServedDomains: []string{"alice.voip.com"},
		})

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".missing-claim", laddr, raddr)
		err := prx.InterceptInboundRequest(
			t.Context(),
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
				nexted.Add(1)
				return nil
			}),
			req,
		)
		if err == nil {
			t.Fatal("prx.InterceptInboundRequest() error = nil, want ErrMissingElement")
		}
		if !errors.Is(err, proxy.ErrMissingElement) {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want %v", err, proxy.ErrMissingElement)
		}
		if !proxy.IsError(err) {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want a %q-classified error", err, proxy.ErrClass)
		}

		// The claim failed before any proxy state or traffic: no claim
		// stamp, no index entry, no sends, and no next call.
		if md, ok := proxy.MetadataFrom(req.Metadata()); ok {
			t.Fatalf("claim stamp installed before the element check: %+v", md)
		}
		key, keyErr := sip.MakeServerTransactionKey(req)
		if keyErr != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", keyErr)
		}
		if _, ok := prx.LookupRequest(elm, key); ok {
			t.Fatal("request index entry installed before the element check")
		}
		if _, ok := elm.LoadServerTransaction(key); ok {
			t.Fatal("server transaction installed before the element check")
		}
		if got := len(drainReqs(tp)) + tp.countSendRes(); got != 0 {
			t.Fatalf("sends before the element check=%d, want 0", got)
		}
		if got := nexted.Load(); got != 0 {
			t.Fatalf("next calls=%d, want 0", got)
		}
	})

	t.Run("stateless claim fails the same way", func(t *testing.T) {
		t.Parallel()

		route := &proxy.Route{
			Name:        "claimed-sl",
			ForwardMode: proxy.ForwardModeStateless,
		}
		_, prx, _ := newConfiguredTestProxy(t, route, proxy.Options{
			StatelessKey: []byte("k"),
		})

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".missing-sl", laddr, raddr)
		err := prx.InterceptInboundRequest(
			t.Context(),
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error { return nil }),
			req,
		)
		if !errors.Is(err, proxy.ErrMissingElement) {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want %v", err, proxy.ErrMissingElement)
		}
	})

	t.Run("owned response fails the same way", func(t *testing.T) {
		t.Parallel()

		route := &proxy.Route{Name: "claimed-res"}
		_, prx, _ := newConfiguredTestProxy(t, route, proxy.Options{})

		res := newInRes(t, newInInviteReq(t, "UDP", sip.MagicCookie+".missing-res", laddr, raddr), sip.ResponseStatusOK)
		res.WithMessage(func(r *sip.Response) {
			via, ok := r.Headers.FirstVia()
			if !ok {
				t.Fatal("response fixture has no Via")
			}
			via.Params.Set("branch", sip.MagicCookie+".proxy-stateless."+
				"0123456789abcdef0123456789abcdef.tok")
		})

		err := prx.InterceptInboundResponse(
			t.Context(),
			sip.ResponseReceiverFunc(func(context.Context, *sip.ResponseEnvelope) error { return nil }),
			res,
		)
		if !errors.Is(err, proxy.ErrMissingElement) {
			t.Fatalf("prx.InterceptInboundResponse() error = %v, want %v", err, proxy.ErrMissingElement)
		}
	})

	t.Run("unmatched request passes through without an element", func(t *testing.T) {
		t.Parallel()

		var nexted atomic.Int32
		route := &proxy.Route{
			Name: "unmatched",
			MatchRequest: func(context.Context, *sip.RequestEnvelope) bool {
				return false
			},
		}
		_, prx, _ := newConfiguredTestProxy(t, route, proxy.Options{})

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".missing-pass", laddr, raddr)
		if err := prx.InterceptInboundRequest(
			t.Context(),
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
				nexted.Add(1)
				return nil
			}),
			req,
		); err != nil {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}
		if got := nexted.Load(); got != 1 {
			t.Fatalf("next calls=%d, want 1", got)
		}
	})

	t.Run("closed proxy passes through without an element", func(t *testing.T) {
		t.Parallel()

		var nexted atomic.Int32
		route := &proxy.Route{Name: "closed"}
		_, prx, _ := newConfiguredTestProxy(t, route, proxy.Options{})
		if err := prx.Close(); err != nil {
			t.Fatalf("prx.Close() error = %v, want nil", err)
		}

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".missing-closed", laddr, raddr)
		if err := prx.InterceptInboundRequest(
			t.Context(),
			sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
				nexted.Add(1)
				return nil
			}),
			req,
		); err != nil {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}
		if got := nexted.Load(); got != 1 {
			t.Fatalf("next calls=%d, want 1", got)
		}
	})

	t.Run("restore without an element fails classified", func(t *testing.T) {
		t.Parallel()

		route := &proxy.Route{Name: "restore"}
		_, prx, _ := newConfiguredTestProxy(t, route, proxy.Options{})

		if _, err := prx.RestoreRequest(t.Context(), nil, proxy.RestoreBindings{}); !errors.Is(err, proxy.ErrMissingElement) {
			t.Fatalf("prx.RestoreRequest() error = %v, want %v", err, proxy.ErrMissingElement)
		}
	})
}

// sharedSide is one element side of a shared-proxy isolation fixture:
// its own element and its own transport stub.
type sharedSide struct {
	elm *sip.Element
	tp  *stubTransp
}

// newSharedTestProxy builds one proxy component bound to count fresh
// elements carrying deliberately equal identity and address configuration:
// every element shares one InstanceID and tracks an identical UDP stub
// transport. Element identity is the pointer alone, so equal configuration
// must never collapse the proxy's per-element namespaces.
func newSharedTestProxy(
	tb testing.TB,
	route *proxy.Route,
	opts proxy.Options,
	count int,
) (*proxy.Proxy, []sharedSide) {
	tb.Helper()

	prx, err := proxy.New([]proxy.Route{*route}, opts)
	if err != nil {
		tb.Fatalf("proxy.New() error = %v, want nil", err)
	}
	tb.Cleanup(func() { _ = prx.Close() })

	sharedID := sip.NextElementID()
	sides := make([]sharedSide, 0, count)
	for range count {
		elm := &sip.Element{InstanceID: sharedID}
		tp := newStubTransp()
		tb.Cleanup(func() { elm.Close() })
		if err := elm.TrackTransport(tp); err != nil {
			tb.Fatalf("elm.TrackTransport() error = %v, want nil", err)
		}
		tb.Cleanup(elm.UseComponent(prx))
		sides = append(sides, sharedSide{elm: elm, tp: tp})
	}
	return prx, sides
}

var silentNext = sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
	return nil
})

// interceptShared feeds req through the proxy pipeline of one side's
// element and fails on a returned error.
func interceptShared(
	ctx context.Context,
	tb testing.TB,
	side sharedSide,
	prx *proxy.Proxy,
	req *sip.RequestEnvelope,
) {
	tb.Helper()

	if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, side.elm), silentNext, req); err != nil {
		tb.Fatalf("prx.InterceptInboundRequest(%v) error = %v, want nil", req.Method(), err)
	}
}

// branchClnTx returns the downstream client transaction of one sent branch
// copy on the given element.
func branchClnTx(tb testing.TB, elm *sip.Element, outReq *sip.RequestEnvelope) sip.ClientTransaction {
	tb.Helper()

	key, err := sip.MakeClientTransactionKey(outReq)
	if err != nil {
		tb.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
	}
	tx, ok := elm.LoadClientTransaction(key)
	if !ok {
		tb.Fatal("forwarding client transaction not registered")
	}
	return tx
}

// observedEvent records one proxy event together with the processing
// element carried by its dispatch context.
type observedEvent struct {
	elm *sip.Element
	ev  proxy.Event
}

func TestProxy_ElementIsolation(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	opts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}

	t.Run("equal keys two elements", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := &proxy.Route{
				Name:        "iso-keys",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return statelessPlan(statelessLitTarget("A", "10.0.0.9")), nil
				},
			}
			prx, sides := newSharedTestProxy(t, route, opts, 2)
			a, b := sides[0], sides[1]

			// Identical inbound bytes on both elements produce equal
			// upstream transaction keys; each keeps its own coordinator and
			// its own downstream send.
			req := newInInviteReq(t, "UDP", sip.MagicCookie+".iso-keys", laddr, raddr)
			interceptShared(ctx, t, a, prx, req)
			interceptShared(ctx, t, b, prx, req)
			synctest.Wait()

			key, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			rqA, okA := prx.LookupRequest(a.elm, key)
			rqB, okB := prx.LookupRequest(b.elm, key)
			if !okA || !okB {
				t.Fatalf("prx.LookupRequest() = (%v, %t), (%v, %t), want an owned request on each element",
					rqA, okA, rqB, okB)
			}
			if rqA == rqB {
				t.Fatal("equal upstream keys on different elements aliased into one coordinator")
			}
			if got := len(drainReqs(a.tp)); got != 1 {
				t.Fatalf("element A outbound sends=%d, want 1", got)
			}
			if got := len(drainReqs(b.tp)); got != 1 {
				t.Fatalf("element B outbound sends=%d, want 1", got)
			}
		})
	})

	t.Run("equal rejected requests", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := &proxy.Route{
				Name:        "iso-rejected",
				ForwardMode: proxy.ForwardModeStateful,
				AuthorizeRequest: func(context.Context, *sip.RequestEnvelope) error {
					return errors.Wrap(sip.NewRequestRejectedError(
						errors.ErrorWrap("policy denied"),
						slog.LevelDebug,
						sip.ResponseStatusForbidden,
					))
				},
			}
			prx, sides := newSharedTestProxy(t, route, opts, 2)
			a, b := sides[0], sides[1]

			var (
				evMu     sync.Mutex
				observed []observedEvent
			)
			prx.BindEventHandler(proxy.EventHandlerFunc(func(evtCtx context.Context, ev proxy.Event) {
				elm, _ := sip.ElementFromContext(evtCtx)
				evMu.Lock()
				observed = append(observed, observedEvent{elm: elm, ev: ev})
				evMu.Unlock()
			}))
			find := func(elm *sip.Element, kind proxy.EventKind, id string) (proxy.Event, bool) {
				evMu.Lock()
				defer evMu.Unlock()
				for _, o := range observed {
					if o.elm == elm && o.ev.Kind == kind && (id == "" || o.ev.RequestID == id) {
						return o.ev, true
					}
				}
				return proxy.Event{}, false
			}
			rejectedID := func(elm *sip.Element) string {
				t.Helper()
				ev, ok := find(elm, proxy.EventKindRequestCompleted, "")
				if !ok || ev.Outcome != proxy.OutcomeRejected {
					t.Fatalf("rejected completion on element %p = (%+v, %t), want an OutcomeRejected event", elm, ev, ok)
				}
				return ev.RequestID
			}

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".iso-rejected", laddr, raddr)
			var rejErr error
			for _, s := range []sharedSide{a, b} {
				if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, s.elm), silentNext, req); err == nil {
					t.Fatal("prx.InterceptInboundRequest() error = nil, want the authorization rejection")
				} else {
					rejErr = err
				}
			}
			var rej *sip.RequestRejectedError
			if !errors.As(rejErr, &rej) {
				t.Fatalf("prx.InterceptInboundRequest() error = %v, want *sip.RequestRejectedError", rejErr)
			}

			idA, idB := rejectedID(a.elm), rejectedID(b.elm)

			// Interleaved answering sends: each element's wire result closes
			// the operation recorded under its own namespace.
			res, err := req.NewResponse(sip.ResponseStatusForbidden)
			if err != nil {
				t.Fatalf("req.NewResponse() error = %v, want nil", err)
			}
			if err := b.tp.SendResponse(sip.ContextWithElement(ctx, b.elm), res); err != nil {
				t.Fatalf("tp.SendResponse() error = %v, want nil", err)
			}
			if ev, ok := find(b.elm, proxy.EventKindResponseForwarded, idB); !ok {
				t.Fatalf("element B answering send observed = (%+v, %t), want a forwarded response of its own rejection", ev, ok)
			}
			if err := a.tp.SendResponse(sip.ContextWithElement(ctx, a.elm), res); err != nil {
				t.Fatalf("tp.SendResponse() error = %v, want nil", err)
			}
			if ev, ok := find(a.elm, proxy.EventKindResponseForwarded, idA); !ok {
				t.Fatalf("element A answering send observed = (%+v, %t), want a forwarded response of its own rejection", ev, ok)
			}
		})
	})

	t.Run("independent cancel", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := &proxy.Route{
				Name:        "iso-cancel",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return statelessPlan(statelessLitTarget("A", "10.0.0.9")), nil
				},
			}
			prx, sides := newSharedTestProxy(t, route, opts, 2)
			a, b := sides[0], sides[1]

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".iso-cancel", laddr, raddr)
			interceptShared(ctx, t, a, prx, req)
			interceptShared(ctx, t, b, prx, req)
			synctest.Wait()

			sendsA, sendsB := drainReqs(a.tp), drainReqs(b.tp)
			if len(sendsA) != 1 || len(sendsB) != 1 {
				t.Fatalf("outbound sends = %d, %d, want one per element", len(sendsA), len(sendsB))
			}
			txA, txB := branchClnTx(t, a.elm, sendsA[0]), branchClnTx(t, b.elm, sendsB[0])

			cancel, err := sip.NewCancelRequestEnvelope(req)
			if err != nil {
				t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
			}
			interceptShared(ctx, t, a, prx, cancel)
			synctest.Wait()

			// The matching CANCEL is acknowledged on element A only.
			if diff := cmp.Diff([]int{200}, resStatuses(t, drainRess(a.tp), sip.RequestMethodCancel)); diff != "" {
				t.Fatalf("element A cancel responses (-want +got): %s", diff)
			}
			if got := len(drainRess(b.tp)); got != 0 {
				t.Fatalf("element B responses after foreign CANCEL=%d, want 0", got)
			}

			// The first provisional on A's branch releases the deferred
			// downstream CANCEL on element A only; element B's coordinator
			// and branches are untouched.
			if err := txA.RecvResponse(ctx, newInRes(t, sendsA[0], sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("txA.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()
			if call := waitCancelSend(t, a.tp); !call.req.Method().Equal(sip.RequestMethodCancel) {
				t.Fatalf("element A downstream cancel method = %v, want CANCEL", call.req.Method())
			}
			if got := countReqMethod(drainReqs(b.tp), sip.RequestMethodCancel); got != 0 {
				t.Fatalf("element B downstream CANCEL sends=%d, want 0 (foreign cancel never associates)", got)
			}

			// Element B still forwards ordinarily: its provisional crosses
			// upstream and schedules no cancellation work.
			if err := txB.RecvResponse(ctx, newInRes(t, sendsB[0], sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("txB.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()
			if diff := cmp.Diff([]int{180}, resStatuses(t, drainRess(b.tp), sip.RequestMethodInvite)); diff != "" {
				t.Fatalf("element B upstream responses (-want +got): %s", diff)
			}
			if got := countReqMethod(drainReqs(b.tp), sip.RequestMethodCancel); got != 0 {
				t.Fatalf("element B downstream CANCEL sends=%d, want 0", got)
			}
		})
	})

	t.Run("independent ack", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := &proxy.Route{
				Name:        "iso-ack",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return statelessPlan(statelessLitTarget("A", "10.0.0.9")), nil
				},
			}
			prx, sides := newSharedTestProxy(t, route, opts, 2)
			a, b := sides[0], sides[1]

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".iso-ack", laddr, raddr)
			interceptShared(ctx, t, a, prx, req)
			interceptShared(ctx, t, b, prx, req)
			synctest.Wait()
			drainReqs(a.tp)
			drainReqs(b.tp)
			drainRess(a.tp)
			drainRess(b.tp)

			key, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			srvTxB, ok := b.elm.LoadServerTransaction(key)
			if !ok {
				t.Fatal("element B upstream transaction not registered")
			}
			stateB := srvTxB.State()

			// A non-2xx ACK shares the INVITE transaction identity: on
			// element A it is dispatched to and absorbed by the matching
			// INVITE server transaction.
			ack := newInAckReq(t, req, newInRes(t, req, sip.ResponseStatusNotFound))
			interceptShared(ctx, t, a, prx, ack)
			synctest.Wait()

			if got := srvTxB.State(); got != stateB {
				t.Fatalf("element B upstream transaction state = %v, want unchanged %v", got, stateB)
			}
			if got := len(drainReqs(b.tp)) + len(drainRess(b.tp)); got != 0 {
				t.Fatalf("element B traffic after foreign ACK=%d, want 0", got)
			}
			if _, ok := prx.LookupRequest(b.elm, key); !ok {
				t.Fatal("element B request association disturbed by a foreign ACK")
			}
		})
	})

	t.Run("same element duplicate", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			route := &proxy.Route{
				Name:        "iso-dup",
				ForwardMode: proxy.ForwardModeStateful,
				ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
					return statelessPlan(statelessLitTarget("A", "10.0.0.9")), nil
				},
			}
			prx, sides := newSharedTestProxy(t, route, opts, 1)
			a := sides[0]

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".iso-dup", laddr, raddr)
			interceptShared(ctx, t, a, prx, req)
			interceptShared(ctx, t, a, prx, req)
			synctest.Wait()

			key, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			rq, ok := prx.LookupRequest(a.elm, key)
			if !ok || rq == nil {
				t.Fatal("prx.LookupRequest() found no owned request")
			}
			if got := len(slices.Collect(a.elm.AllServerTransactions())); got != 1 {
				t.Fatalf("upstream transactions=%d, want 1 (retransmission shares the owner)", got)
			}
			if got := len(drainReqs(a.tp)); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}
		})
	})
}

func TestProxy_ElementFallbackKeys(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")

	// sendCancel forwards one unmatched CANCEL through the stateless path of
	// the side's element and returns the keyed token of the emitted
	// proxy-namespace top Via branch.
	sendCancel := func(tb testing.TB, ctx context.Context, side sharedSide, prx *proxy.Proxy, req *sip.RequestEnvelope) string {
		tb.Helper()

		interceptShared(ctx, tb, side, prx, req)
		call := testutil.WaitResult(tb, side.tp.sendReqCalls, "stateless CANCEL send")
		branch := statelessTopBranch(tb, call.req)
		return branch[strings.LastIndex(branch, ".")+1:]
	}

	cancelReq := func(tb testing.TB, branch string) *sip.RequestEnvelope {
		tb.Helper()
		req := newInInviteReq(tb, "UDP", branch, laddr, raddr)
		cancel, err := sip.NewCancelRequestEnvelope(req)
		if err != nil {
			tb.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
		}
		return cancel
	}

	route := &proxy.Route{
		Name:        "sl-fallback",
		ForwardMode: proxy.ForwardModeStateful,
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
			return statelessPlan(statelessLitTarget("A", "10.0.0.9")), nil
		},
	}

	t.Run("same element stable", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			prx, sides := newSharedTestProxy(t, route, proxy.Options{ServedDomains: []string{"alice.voip.com"}}, 1)
			a := sides[0]

			cancel := cancelReq(t, sip.MagicCookie+".slfb-same")
			first := sendCancel(t, ctx, a, prx, cancel)
			second := sendCancel(t, ctx, a, prx, cancel)
			if first != second {
				t.Fatalf("same-element fallback token changed: %q -> %q, want stable", first, second)
			}
		})
	})

	t.Run("different element distinct", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			prx, sides := newSharedTestProxy(t, route, proxy.Options{ServedDomains: []string{"alice.voip.com"}}, 2)
			a, b := sides[0], sides[1]

			cancel := cancelReq(t, sip.MagicCookie+".slfb-distinct")
			tokA := sendCancel(t, ctx, a, prx, cancel)
			tokB := sendCancel(t, ctx, b, prx, cancel)
			if tokA == tokB {
				t.Fatalf("element fallback keys collided at token %q", tokA)
			}
		})
	})

	t.Run("explicit stateless key preserved", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			prx, sides := newSharedTestProxy(t, route, proxy.Options{
				StatelessKey:  []byte("caller-stateless-key"),
				ServedDomains: []string{"alice.voip.com"},
			}, 2)
			a, b := sides[0], sides[1]

			cancel := cancelReq(t, sip.MagicCookie+".slfb-explicit")
			tokA := sendCancel(t, ctx, a, prx, cancel)
			tokB := sendCancel(t, ctx, b, prx, cancel)
			if tokA != tokB {
				t.Fatalf("explicit StatelessKey tokens differ across elements: %q vs %q, want caller-controlled key", tokA, tokB)
			}
		})
	})

	t.Run("unseen element lookup allocates nothing", func(t *testing.T) {
		t.Parallel()

		prx, sides := newSharedTestProxy(t, route, proxy.Options{}, 1)
		a := sides[0]

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".slfb-unseen", laddr, raddr)
		key, err := sip.MakeServerTransactionKey(req)
		if err != nil {
			t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		unseen := &sip.Element{}
		t.Cleanup(func() { unseen.Close() })
		if got, ok := prx.LookupRequest(unseen, key); ok || got != nil {
			t.Fatalf("prx.LookupRequest(unseen) = (%v, %t), want (nil, false)", got, ok)
		}
		if got, ok := prx.LookupRequest(nil, key); ok || got != nil {
			t.Fatalf("prx.LookupRequest(nil) = (%v, %t), want (nil, false)", got, ok)
		}
		if got, ok := prx.LookupRequest(a.elm, key); ok || got != nil {
			t.Fatalf("prx.LookupRequest(bound, unseen key) = (%v, %t), want (nil, false)", got, ok)
		}
	})
}

// TestProxy_LookupRequestElement pins the element-qualified lookup
// contract: a nil element, an element that never processed a request, and
// an absent key all report no association — without allocating a
// processing scope — and equal upstream keys on different elements resolve
// only their own namespace.
func TestProxy_LookupRequestElement(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	opts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}
	route := &proxy.Route{
		Name:        "lookup-elm",
		ForwardMode: proxy.ForwardModeStateful,
		ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
			return statelessPlan(statelessLitTarget("A", "10.0.0.9")), nil
		},
	}

	srvKeyOf := func(tb testing.TB, req *sip.RequestEnvelope) sip.ServerTransactionKey {
		tb.Helper()
		key, err := sip.MakeServerTransactionKey(req)
		if err != nil {
			tb.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		return key
	}

	t.Run("nil element reports no association", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			prx, sides := newSharedTestProxy(t, route, opts, 1)
			a := sides[0]

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".lke-nil", laddr, raddr)
			interceptShared(ctx, t, a, prx, req)
			synctest.Wait()

			if rq, ok := prx.LookupRequest(nil, srvKeyOf(t, req)); rq != nil || ok {
				t.Errorf("prx.LookupRequest(nil, key) = (%v, %v), want (nil, false)", rq, ok)
			}
		})
	})

	t.Run("live element absent key", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			prx, sides := newSharedTestProxy(t, route, opts, 1)
			a := sides[0]

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".lke-live", laddr, raddr)
			interceptShared(ctx, t, a, prx, req)
			synctest.Wait()

			absent := newInInviteReq(t, "UDP", sip.MagicCookie+".lke-absent", laddr, raddr)
			if rq, ok := prx.LookupRequest(a.elm, srvKeyOf(t, absent)); rq != nil || ok {
				t.Errorf("prx.LookupRequest(elmA, absentKey) = (%v, %v), want (nil, false)", rq, ok)
			}
		})
	})

	t.Run("unprocessed element reports no association", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			prx, sides := newSharedTestProxy(t, route, opts, 2)
			a, b := sides[0], sides[1]

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".lke-unseen", laddr, raddr)
			interceptShared(ctx, t, a, prx, req)
			synctest.Wait()

			key := srvKeyOf(t, req)
			// Element B never processed the request: the foreign key
			// reports no association and allocates no processing scope.
			if rq, ok := prx.LookupRequest(b.elm, key); rq != nil || ok {
				t.Errorf("prx.LookupRequest(elmB, keyA) = (%v, %v), want (nil, false)", rq, ok)
			}
			if rq, ok := prx.LookupRequest(a.elm, key); rq == nil || !ok {
				t.Error("prx.LookupRequest(elmA, keyA) not found, want own request")
			}
		})
	})

	t.Run("equal keys resolve only their own element", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			prx, sides := newSharedTestProxy(t, route, opts, 2)
			a, b := sides[0], sides[1]

			// Identical inbound bytes produce equal upstream keys on both
			// elements; each lookup resolves only its own namespace.
			req := newInInviteReq(t, "UDP", sip.MagicCookie+".lke-equal", laddr, raddr)
			interceptShared(ctx, t, a, prx, req)
			interceptShared(ctx, t, b, prx, req)
			synctest.Wait()

			key := srvKeyOf(t, req)
			rqA, okA := prx.LookupRequest(a.elm, key)
			rqB, okB := prx.LookupRequest(b.elm, key)
			if rqA == nil || !okA {
				t.Fatal("prx.LookupRequest(elmA, key) not found, want own request")
			}
			if rqB == nil || !okB {
				t.Fatal("prx.LookupRequest(elmB, key) not found, want own request")
			}
			if rqA == rqB {
				t.Fatal("equal keys on different elements aliased into one request")
			}
		})
	})
}

// TestProxy_MetadataIdentity pins the single operation identity minted at
// the claim: one request ID is stamped onto the inbound envelope at
// admission, is carried by every owned clone — the working request, the
// promoted copies, and the outbound branches — and is shared by the
// observation events and the request snapshot.
func TestProxy_MetadataIdentity(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	opts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}

	// claimOf returns the stamp recorded on env, failing when no valid
	// proxy metadata exists.
	claimOf := func(tb testing.TB, env *sip.RequestEnvelope) proxy.Metadata {
		tb.Helper()
		md, ok := proxy.MetadataFrom(env.Metadata())
		if !ok {
			tb.Fatalf("proxy.MetadataFrom() = (%+v, %v), want a valid claim stamp", md, ok)
		}
		return md
	}
	// claimSnapshot returns the snapshot of the request owning req's
	// upstream transaction key.
	claimSnapshot := func(tb testing.TB, elm *sip.Element, prx *proxy.Proxy, req *sip.RequestEnvelope) *proxy.RequestSnapshot {
		tb.Helper()
		key, err := sip.MakeServerTransactionKey(req)
		if err != nil {
			tb.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
		}
		rq, ok := prx.LookupRequest(elm, key)
		if !ok {
			tb.Fatal("claimed request not indexed by its upstream key")
		}
		snap, err := rq.Snapshot()
		if err != nil {
			tb.Fatalf("rq.Snapshot() error = %v, want nil", err)
		}
		return snap
	}

	t.Run("stateful", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			rec := &evtRecorder{}
			var (
				workMD   proxy.Metadata
				workSeen bool
			)
			route := obsRoute("id-stateful", obsLitPlan("A"))
			resolve := route.ResolveRequestTargets
			route.ResolveRequestTargets = func(ctx context.Context, wReq *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				workMD, workSeen = proxy.MetadataFrom(wReq.Metadata())
				return resolve(ctx, wReq)
			}
			elm, prx, tp := newConfiguredTestProxy(t, route, opts)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".id-stateful", laddr, raddr)
			fwdIntercept(t, elm, prx, req)
			synctest.Wait()

			want := claimOf(t, req)
			if want.RequestID == "" || want.Stage != proxy.StageClaim ||
				want.RouteName != "id-stateful" || want.Mode != proxy.ForwardModeStateful {
				t.Fatalf("inbound claim stamp = %+v, want nonempty request ID at stage claim for route id-stateful in stateful mode", want)
			}
			if !workSeen || workMD.RequestID != want.RequestID ||
				workMD.Stage != proxy.StageClaim || workMD.Mode != proxy.ForwardModeStateful {
				t.Fatalf("working claim stamp = (%+v, %v), want the inbound's claim record %+v", workMD, workSeen, want)
			}

			sends := drainReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("outbound sends=%d, want 1", len(sends))
			}
			if got := claimOf(t, sends[0]); got.RequestID != want.RequestID {
				t.Fatalf("outbound stamp = %+v, want request ID %q", got, want.RequestID)
			}

			evs := rec.get()
			if len(evs) == 0 {
				t.Fatal("no observation events recorded")
			}
			for i, ev := range evs {
				if ev.RequestID != want.RequestID {
					t.Fatalf("event %d RequestID = %q, want %q", i, ev.RequestID, want.RequestID)
				}
			}

			snap := claimSnapshot(t, elm, prx, req)
			if snap.RequestID != want.RequestID {
				t.Fatalf("snapshot request ID = %q, want %q", snap.RequestID, want.RequestID)
			}
			if got := claimOf(t, snap.OriginalRequest); got.RequestID != want.RequestID {
				t.Fatalf("snapshot original stamp = %+v, want request ID %q", got, want.RequestID)
			}
			if got := claimOf(t, snap.WorkingRequest); got.RequestID != want.RequestID {
				t.Fatalf("snapshot working stamp = %+v, want request ID %q", got, want.RequestID)
			}
		})
	})

	t.Run("stateless", func(t *testing.T) {
		t.Parallel()

		rec := &evtRecorder{}
		route := statelessPlanRoute(statelessPlan(statelessLitTarget("A", "10.0.0.9")))
		route.Name = "id-stateless"
		elm, prx, tp := newConfiguredTestProxy(t, route, proxy.Options{
			StatelessKey:  []byte("id-stateless-key"),
			ServedDomains: []string{"alice.voip.com"},
		})
		prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

		req := newInInviteReq(t, "UDP", sip.MagicCookie+".id-stateless", laddr, raddr)
		if err := prx.InterceptInboundRequest(
			sip.ContextWithElement(t.Context(), elm), silentNext, req,
		); err != nil {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}

		want := claimOf(t, req)
		if want.RequestID == "" || want.Stage != proxy.StageClaim ||
			want.RouteName != "id-stateless" || want.Mode != proxy.ForwardModeStateless {
			t.Fatalf("inbound claim stamp = %+v, want nonempty request ID at stage claim for route id-stateless in stateless mode", want)
		}
		sends := drainReqs(tp)
		if len(sends) != 1 {
			t.Fatalf("outbound sends=%d, want 1", len(sends))
		}
		if got := claimOf(t, sends[0]); got.RequestID != want.RequestID ||
			got.Mode != proxy.ForwardModeStateless {
			t.Fatalf("outbound stamp = %+v, want request ID %q in stateless mode", got, want.RequestID)
		}
		evs := rec.get()
		if len(evs) == 0 {
			t.Fatal("no observation events recorded")
		}
		for i, ev := range evs {
			if ev.RequestID != want.RequestID {
				t.Fatalf("event %d RequestID = %q, want %q", i, ev.RequestID, want.RequestID)
			}
		}
	})

	t.Run("rejected before claim", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			rec := &evtRecorder{}
			var (
				hookMD   proxy.Metadata
				hookSeen bool
			)
			route := obsRoute("id-reject", obsLitPlan("A"))
			route.AuthorizeRequest = func(_ context.Context, req *sip.RequestEnvelope) error {
				hookMD, hookSeen = proxy.MetadataFrom(req.Metadata())
				return sip.NewRequestRejectedError(
					errors.ErrorWrap("policy denied"), 0, sip.ResponseStatusForbidden,
				)
			}
			elm, prx, _ := newConfiguredTestProxy(t, route, opts)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".id-reject", laddr, raddr)
			err := prx.InterceptInboundRequest(sip.ContextWithElement(t.Context(), elm), silentNext, req)
			if err == nil {
				t.Fatal("prx.InterceptInboundRequest() error = nil, want the authorization rejection")
			}
			synctest.Wait()

			if !hookSeen || hookMD.RequestID == "" || hookMD.Stage != proxy.StageClaim {
				t.Fatalf("claim stamp at authorization = (%+v, %v), want a claim-stage stamp with nonempty request ID", hookMD, hookSeen)
			}
			want := claimOf(t, req)
			if want.RequestID != hookMD.RequestID {
				t.Fatalf("inbound claim ID = %q, want the ID seen at authorization %q", want.RequestID, hookMD.RequestID)
			}
			evs := rec.get()
			if len(evs) == 0 {
				t.Fatal("no observation events recorded")
			}
			for i, ev := range evs {
				if ev.RequestID != want.RequestID {
					t.Fatalf("event %d RequestID = %q, want %q", i, ev.RequestID, want.RequestID)
				}
			}
		})
	})

	t.Run("promotion", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			f := newPromotionFixture()
			rec := &evtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, f.route, f.opts, &sip.Element{
				ServerLocator: statelessFixedLocator{addr: netip.MustParseAddrPort("198.51.100.77:5060")},
			})
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".id-promotion", laddr, raddr)
			fwdIntercept(t, elm, prx, req)
			synctest.Wait()

			want := claimOf(t, req)
			if want.RequestID == "" || want.Stage != proxy.StageClaim ||
				want.RouteName != "promotion" || want.Mode != proxy.ForwardModeStateful {
				t.Fatalf("inbound claim stamp = %+v, want nonempty request ID at stage claim for route promotion in stateful effective mode", want)
			}
			if f.route.ForwardMode != proxy.ForwardModeStateless {
				t.Fatalf("configured route mode = %v, want unchanged %v", f.route.ForwardMode, proxy.ForwardModeStateless)
			}

			sends := drainReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("outbound sends=%d, want 1", len(sends))
			}
			if got := claimOf(t, sends[0]); got.RequestID != want.RequestID ||
				got.Mode != proxy.ForwardModeStateful {
				t.Fatalf("promoted outbound stamp = %+v, want request ID %q in stateful effective mode", got, want.RequestID)
			}
			if got := rec.counts()[proxy.EventKindRequestHandled]; got != 1 {
				t.Fatalf("handled observations=%d, want 1 (no replayed handled)", got)
			}
			for i, ev := range rec.get() {
				if ev.RequestID != want.RequestID || ev.Mode != proxy.ForwardModeStateful {
					t.Fatalf("event %d = %+v, want request ID %q in stateful mode", i, ev, want.RequestID)
				}
			}

			snap := claimSnapshot(t, elm, prx, req)
			if snap.RequestID != want.RequestID {
				t.Fatalf("snapshot request ID = %q, want %q", snap.RequestID, want.RequestID)
			}
			for _, env := range []*sip.RequestEnvelope{snap.OriginalRequest, snap.WorkingRequest} {
				if got := claimOf(t, env); got.RequestID != want.RequestID ||
					got.Mode != proxy.ForwardModeStateful {
					t.Fatalf("promoted owned copy stamp = %+v, want request ID %q in stateful effective mode", got, want.RequestID)
				}
			}
		})
	})

	t.Run("duplicate", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &evtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("id-dup", obsLitPlan("A")), opts)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".id-dup", laddr, raddr)
			fwdIntercept(t, elm, prx, req)
			synctest.Wait()
			want := claimOf(t, req)
			before := len(rec.get())
			drainReqs(tp)

			// A retransmission stripped of the inherited stamp still joins
			// the winning owner's operation identity.
			dup := req.Clone().(*sip.RequestEnvelope)
			dup.Metadata().Delete(proxy.MetadataKey)
			if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, dup); err != nil {
				t.Fatalf("duplicate prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			if got := claimOf(t, dup); got.RequestID != want.RequestID || got.Stage != proxy.StageClaim {
				t.Fatalf("retransmitted claim stamp = %+v, want the owner's request ID %q at stage claim", got, want.RequestID)
			}
			if got := len(rec.get()); got != before {
				t.Fatalf("events after the retransmission=%d, want unchanged %d", got, before)
			}
			if got := len(drainReqs(tp)); got != 0 {
				t.Fatalf("outbound sends after the retransmission=%d, want 0", got)
			}
		})
	})

	t.Run("promoted duplicate", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			f := newPromotionFixture()
			// The retransmission must promote too: return the same
			// multi-target plan on every resolution so it reaches the
			// duplicate admission of the winning owner.
			f.route.ResolveRequestTargets = func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return f.plan, nil
			}
			rec := &evtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, f.route, f.opts, &sip.Element{
				ServerLocator: statelessFixedLocator{addr: netip.MustParseAddrPort("198.51.100.77:5060")},
			})
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".id-promoted-dup", laddr, raddr)
			fwdIntercept(t, elm, prx, req)
			synctest.Wait()
			want := claimOf(t, req)
			before := len(rec.get())
			if got := len(drainReqs(tp)); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}

			// A retransmission stripped of the inherited stamp re-traverses
			// the stateless path and is promoted into the already admitted
			// owner: the app-facing envelope joins the winning identity
			// rather than keeping a dead freshly minted ID.
			dup := req.Clone().(*sip.RequestEnvelope)
			dup.Metadata().Delete(proxy.MetadataKey)
			if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), silentNext, dup); err != nil {
				t.Fatalf("promoted duplicate prx.InterceptInboundRequest() error = %v, want nil", err)
			}
			synctest.Wait()

			if got := claimOf(t, dup); got.RequestID != want.RequestID || got.Stage != proxy.StageClaim ||
				got.Mode != proxy.ForwardModeStateful {
				t.Fatalf("promoted retransmission claim stamp = %+v, want the owner's request ID %q at stage claim in stateful effective mode", got, want.RequestID)
			}
			if got := len(rec.get()); got != before {
				t.Fatalf("events after the retransmission=%d, want unchanged %d", got, before)
			}
		})
	})

	t.Run("unmatched", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		route := &proxy.Route{
			Name: "id-unmatched",
			MatchRequest: func(context.Context, *sip.RequestEnvelope) bool {
				return false
			},
		}
		elm, prx, _ := newConfiguredTestProxy(t, route, opts)

		var nexted atomic.Int32
		next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
			nexted.Add(1)
			return nil
		})
		req := newInInviteReq(t, "UDP", sip.MagicCookie+".id-unmatched", laddr, raddr)
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), next, req); err != nil {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}
		if got := nexted.Load(); got != 1 {
			t.Fatalf("next calls=%d, want 1", got)
		}
		if md, ok := proxy.MetadataFrom(req.Metadata()); ok {
			t.Fatalf("unmatched metadata = (%+v, %v), want no stamp", md, ok)
		}

		// A stamp a previous proxy left behind is left alone as well.
		foreign := proxy.Metadata{
			RequestID: "foreign-id",
			RouteName: "foreign",
			Mode:      proxy.ForwardModeStateless,
			Stage:     proxy.StageResponse,
		}
		proxy.SetMetadata(req.Metadata(), foreign)
		if err := prx.InterceptInboundRequest(sip.ContextWithElement(ctx, elm), next, req); err != nil {
			t.Fatalf("prx.InterceptInboundRequest() error = %v, want nil", err)
		}
		if md, ok := proxy.MetadataFrom(req.Metadata()); !ok || md != foreign {
			t.Fatalf("unmatched foreign stamp = (%+v, %v), want untouched %+v", md, ok, foreign)
		}
	})

	t.Run("re-traversal", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("id-retraverse", obsLitPlan("A")), opts)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".id-retraverse", laddr, raddr)
			proxy.SetMetadata(req.Metadata(), proxy.Metadata{
				RequestID:        "foreign-id",
				RelatedRequestID: "rel-1",
				TargetID:         "foreign-target",
				AttemptID:        "foreign-attempt",
				RouteName:        "foreign",
				Mode:             proxy.ForwardModeStateless,
				Stage:            proxy.StageResponse,
			})
			fwdIntercept(t, elm, prx, req)
			synctest.Wait()

			got := claimOf(t, req)
			if got.RequestID == "" || got.RequestID == "foreign-id" {
				t.Fatalf("re-traversed claim ID = %q, want a fresh nonempty identity", got.RequestID)
			}
			if got.Stage != proxy.StageClaim || got.RouteName != "id-retraverse" ||
				got.Mode != proxy.ForwardModeStateful {
				t.Fatalf("re-traversed claim stamp = %+v, want a claim record of route id-retraverse in stateful mode", got)
			}
			if got.TargetID != "" || got.AttemptID != "" || got.RelatedRequestID != "" {
				t.Fatalf("re-traversed claim stamp = %+v, want the foreign record replaced, not merged", got)
			}
			if got := len(drainReqs(tp)); got != 1 {
				t.Fatalf("outbound sends=%d, want 1", got)
			}
		})
	})
}

// TestProxy_MetadataRelatedMethods pins the child-operation contract of
// related requests: an envelope the proxy actually processes gets its own
// lightweight request identity and records the parent link only when a
// live relationship is really known, while a downstream CANCEL the proxy
// builds carries the child attribution plus the cancelled attempt's
// target/attempt. An ACK the core absorbs before the proxy ever sees it
// keeps no stamp at all, and the parent's envelopes, working copies, and
// snapshots are never rewritten by a child.
func TestProxy_MetadataRelatedMethods(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("10.0.0.5:5060")
	opts := proxy.Options{ServedDomains: []string{"alice.voip.com"}}

	// mustStamp returns the proxy metadata recorded on env, failing when
	// no valid value exists.
	mustStamp := func(tb testing.TB, env *sip.RequestEnvelope) proxy.Metadata {
		tb.Helper()
		md, ok := proxy.MetadataFrom(env.Metadata())
		if !ok {
			tb.Fatalf("proxy.MetadataFrom() = (%+v, %v), want a valid proxy stamp", md, ok)
		}
		return md
	}
	// targetOf maps an outbound branch copy back to its plan target via
	// the Request-URI host; unknown hosts return an empty ID.
	targetOf := func(req *sip.RequestEnvelope) string {
		uri, ok := req.URI().(*sip.URI)
		if !ok {
			return ""
		}
		for id, host := range map[string]string{"A": "10.0.0.9", "B": "10.0.0.10"} {
			if uri.Addr.Host() == host {
				return id
			}
		}
		return ""
	}

	t.Run("matched cancel", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("rel-cancel", obsLitPlan("A")), opts)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".rel-cancel", laddr, raddr)
			fwdIntercept(t, elm, prx, req)
			synctest.Wait()

			parent := mustStamp(t, req)
			sends := drainReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("outbound sends=%d, want 1", len(sends))
			}
			fwd := mustStamp(t, sends[0])

			// A CANCEL that matches a live coordinator is its own
			// lightweight child operation: its claim record keeps the
			// claim-minted identity and gains only the live parent link —
			// never a target or attempt.
			cancel, err := sip.NewCancelRequestEnvelope(req)
			if err != nil {
				t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
			}
			fwdIntercept(t, elm, prx, cancel)
			synctest.Wait()

			child, ok := proxy.MetadataFrom(cancel.Metadata())
			if !ok {
				t.Fatal("matched CANCEL envelope missing sip.proxy metadata")
			}
			if child.RequestID == parent.RequestID {
				t.Errorf("child reused parent RequestID %q, want distinct lightweight ID", child.RequestID)
			}
			if child.RelatedRequestID != parent.RequestID {
				t.Errorf("child RelatedRequestID = %q, want parent %q", child.RelatedRequestID, parent.RequestID)
			}
			if child.Stage != proxy.StageClaim || child.RouteName != "rel-cancel" ||
				child.Mode != proxy.ForwardModeStateful {
				t.Errorf("child claim stamp = %+v, want a claim record of route rel-cancel in stateful mode", child)
			}
			if child.TargetID != "" || child.AttemptID != "" {
				t.Errorf("child claim stamp = %+v, want empty target/attempt", child)
			}

			// The parent's envelopes and downstream copies are never
			// rewritten by the child.
			if diff := cmp.Diff(parent, mustStamp(t, req)); diff != "" {
				t.Errorf("parent claim stamp changed (-before +after):\n%s", diff)
			}
			if got := mustStamp(t, sends[0]); got != fwd {
				t.Errorf("forward copy stamp = %+v, want unchanged %+v", got, fwd)
			}

			// The branch saw no provisional yet, so the downstream CANCEL
			// is deferred to the first one; it then carries the child
			// attribution plus the cancelled attempt's target/attempt.
			txA := fwdClnTx(t, elm, sends[0])
			if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("txA.RecvResponse(180) error = %v, want nil", err)
			}
			call := waitCancelSend(t, tp)
			synctest.Wait()

			dwn := mustStamp(t, call.req)
			if dwn.RequestID != child.RequestID {
				t.Errorf("downstream CANCEL RequestID = %q, want the child operation %q", dwn.RequestID, child.RequestID)
			}
			if dwn.RelatedRequestID != parent.RequestID {
				t.Errorf("downstream CANCEL RelatedRequestID = %q, want parent %q", dwn.RelatedRequestID, parent.RequestID)
			}
			if dwn.Stage != proxy.StageForward || dwn.TargetID != fwd.TargetID || dwn.AttemptID != fwd.AttemptID {
				t.Errorf("downstream CANCEL stamp = %+v, want forward-stage attribution of attempt %q/%q",
					dwn, fwd.TargetID, fwd.AttemptID)
			}
			if dwn.RouteName != "rel-cancel" || dwn.Mode != proxy.ForwardModeStateful {
				t.Errorf("downstream CANCEL stamp = %+v, want route rel-cancel in stateful mode", dwn)
			}
			if !call.req.Forwarded() || call.req.ForwardedViaBranch() != sends[0].ForwardedViaBranch() {
				t.Error("downstream CANCEL lost the INVITE branch's forwarding provenance")
			}
		})
	})

	t.Run("unmatched cancel", func(t *testing.T) {
		t.Parallel()

		elm, prx, tp := newConfiguredTestProxy(t, obsRoute("rel-unmatched", obsLitPlan("A")), opts)

		// A CANCEL for an INVITE the proxy never admitted knows no live
		// parent: the claim record reports no relationship and the
		// Section 16.10 stateless forwarding is unchanged.
		unseen := newInInviteReq(t, "UDP", sip.MagicCookie+".rel-unseen", laddr, raddr)
		cancel, err := sip.NewCancelRequestEnvelope(unseen)
		if err != nil {
			t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
		}
		fwdIntercept(t, elm, prx, cancel)

		child := mustStamp(t, cancel)
		if child.RequestID == "" {
			t.Errorf("unmatched CANCEL RequestID = %q, want a nonempty lightweight ID", child.RequestID)
		}
		if child.RelatedRequestID != "" {
			t.Errorf("unmatched CANCEL RelatedRequestID = %q, want empty (no live relationship)", child.RelatedRequestID)
		}
		if child.Stage != proxy.StageClaim || child.TargetID != "" || child.AttemptID != "" {
			t.Errorf("unmatched CANCEL claim stamp = %+v, want claim stage with empty target/attempt", child)
		}
		sends := drainReqs(tp)
		if len(sends) != 1 || !sends[0].Method().Equal(sip.RequestMethodCancel) {
			t.Fatalf("stateless sends=%d, want 1 forwarded CANCEL", len(sends))
		}
		if got := len(drainRess(tp)); got != 0 {
			t.Fatalf("responses=%d, want 0 (never a 481)", got)
		}
		if got := countAllTxs(elm); got != 0 {
			t.Fatalf("transactions=%d, want 0 (no response context)", got)
		}
	})

	t.Run("non-2xx ack absorbed by core", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &evtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("rel-ack-abs", obsLitPlan("A")), opts)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			invite := newInInviteReq(t, "UDP", sip.MagicCookie+".rel-ack-abs", laddr, raddr)
			srvTx := admitInviteAndSettle(t, prx, elm, tp, silentNext, invite, sip.ResponseStatusNotFound)
			if got := srvTx.State(); got != sip.TransactionStateCompleted {
				t.Fatalf("invite transaction state = %v, want %v", got, sip.TransactionStateCompleted)
			}
			drainReqs(tp)
			drainRess(tp)
			eventsBefore := len(rec.get())

			// The real inbound chain reaches the transaction manager
			// first: a non-2xx ACK is absorbed by the matching INVITE
			// transaction before the proxy ever sees it, so it produces no
			// proxy coordinator, event, or stamp.
			ack := newInAckReq(t, invite, newInRes(t, invite, sip.ResponseStatusNotFound))
			recv := sip.InterceptInboundRequest(
				slices.Collect(tp.InboundRequestInterceptors.All()),
				silentNext,
			)
			if err := recv.RecvRequest(ctx, ack); err != nil {
				t.Fatalf("inbound chain RecvRequest(ACK) error = %v, want nil", err)
			}
			if got := srvTx.State(); got != sip.TransactionStateConfirmed {
				t.Fatalf("invite transaction state = %v, want %v", got, sip.TransactionStateConfirmed)
			}
			if md, ok := proxy.MetadataFrom(ack.Metadata()); ok {
				t.Errorf("absorbed ACK metadata = %+v, want no proxy stamp (no traversal)", md)
			}
			if got := len(rec.get()); got != eventsBefore {
				t.Errorf("events after absorb=%d, want unchanged %d", got, eventsBefore)
			}
			if got := countAckTxs(elm); got != 0 {
				t.Errorf("ACK transactions=%d, want 0", got)
			}
			if len(tp.sendResCalls) != 0 {
				t.Error("ACK received a SIP response")
			}
			if got := len(tp.sendReqCalls); got != 0 {
				t.Errorf("hop-by-hop ACK forwarded downstream, sends=%d, want 0", got)
			}
		})
	})

	t.Run("2xx ack forwarded", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("rel-ack-2xx", obsLitPlan("A")), opts)

			invite := newInInviteReq(t, "UDP", sip.MagicCookie+".rel-ack-2xx", laddr, raddr)
			admitInviteAndSettle(t, prx, elm, tp, silentNext, invite, sip.ResponseStatusOK)
			parent := mustStamp(t, invite)
			drainReqs(tp)
			drainRess(tp)

			// The end-to-end ACK is forwarded through the same
			// prepared-target path. It gets its own lightweight identity;
			// its own transaction identity means no live parent
			// relationship is really known, so no link is fabricated.
			ack := newInAckReq(t, invite, newInRes(t, invite, sip.ResponseStatusOK))
			fwdIntercept(t, elm, prx, ack)

			sent := testutil.WaitResult(t, tp.sendReqCalls, "forwarded end-to-end ACK").req
			if !sent.Method().Equal(sip.RequestMethodAck) {
				t.Fatalf("sent method = %v, want ACK", sent.Method())
			}
			child := mustStamp(t, ack)
			if child.RequestID == "" || child.RequestID == parent.RequestID {
				t.Errorf("ack RequestID = %q, want a nonempty lightweight ID distinct from parent %q",
					child.RequestID, parent.RequestID)
			}
			if child.Stage != proxy.StageClaim || child.TargetID != "" || child.AttemptID != "" {
				t.Errorf("ack claim stamp = %+v, want claim stage with empty target/attempt", child)
			}
			if child.RelatedRequestID != "" {
				t.Errorf("ack RelatedRequestID = %q, want empty (an end-to-end ACK knows no live parent)",
					child.RelatedRequestID)
			}
		})
	})

	t.Run("downstream cancel", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("rel-dwn", obsLitPlan("A", "B")), opts)

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".rel-dwn", laddr, raddr)
			fwdIntercept(t, elm, prx, req)
			synctest.Wait()

			parent := mustStamp(t, req)
			sends := drainReqs(tp)
			if len(sends) != 2 {
				t.Fatalf("outbound sends=%d, want 2", len(sends))
			}
			byTarget := make(map[string]*sip.RequestEnvelope, len(sends))
			for _, out := range sends {
				byTarget[targetOf(out)] = out
			}
			if byTarget["A"] == nil || byTarget["B"] == nil {
				t.Fatalf("fork copies = %v, want one copy per target A/B", byTarget)
			}
			fwdB := mustStamp(t, byTarget["B"])
			txA := fwdClnTx(t, elm, byTarget["A"])
			txB := fwdClnTx(t, elm, byTarget["B"])

			// Both branches proceed; a global failure on A initiates
			// cancellation of the still-pending B.
			if err := txA.RecvResponse(ctx, newInRes(t, byTarget["A"], sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("txA.RecvResponse(180) error = %v, want nil", err)
			}
			if err := txB.RecvResponse(ctx, newInRes(t, byTarget["B"], sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("txB.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()
			if err := txA.RecvResponse(ctx, newInRes(t, byTarget["A"], sip.ResponseStatusDecline)); err != nil {
				t.Fatalf("txA.RecvResponse(603) error = %v, want nil", err)
			}
			call := waitCancelSend(t, tp)
			synctest.Wait()

			// The built CANCEL keeps the branch's forwarding provenance
			// and carries proxy attribution — its own lightweight identity
			// linked to the parent request and the cancelled attempt —
			// recorded before the send.
			dwn := mustStamp(t, call.req)
			if dwn.RequestID == "" || dwn.RequestID == parent.RequestID {
				t.Errorf("downstream CANCEL RequestID = %q, want a nonempty lightweight ID distinct from parent %q",
					dwn.RequestID, parent.RequestID)
			}
			if dwn.RelatedRequestID != parent.RequestID {
				t.Errorf("downstream CANCEL RelatedRequestID = %q, want parent %q", dwn.RelatedRequestID, parent.RequestID)
			}
			if dwn.Stage != proxy.StageForward || dwn.TargetID != "B" || dwn.AttemptID != fwdB.AttemptID {
				t.Errorf("downstream CANCEL stamp = %+v, want forward-stage attribution of attempt %q/%q",
					dwn, "B", fwdB.AttemptID)
			}
			if dwn.RouteName != "rel-dwn" || dwn.Mode != proxy.ForwardModeStateful {
				t.Errorf("downstream CANCEL stamp = %+v, want route rel-dwn in stateful mode", dwn)
			}
			if !call.req.Forwarded() || call.req.ForwardedViaBranch() != byTarget["B"].ForwardedViaBranch() {
				t.Error("downstream CANCEL lost the branch's forwarding provenance")
			}
		})
	})

	t.Run("no side effects", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()
			rec := &evtRecorder{}
			elm, prx, tp := newConfiguredTestProxy(t, obsRoute("rel-noside", obsLitPlan("A")), opts)
			prx.BindEventHandler(proxy.EventHandlerFunc(rec.record))

			req := newInInviteReq(t, "UDP", sip.MagicCookie+".rel-noside", laddr, raddr)
			fwdIntercept(t, elm, prx, req)
			synctest.Wait()

			parent := mustStamp(t, req)
			sends := drainReqs(tp)
			if len(sends) != 1 {
				t.Fatalf("outbound sends=%d, want 1", len(sends))
			}
			txA := fwdClnTx(t, elm, sends[0])
			if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusRinging)); err != nil {
				t.Fatalf("txA.RecvResponse(180) error = %v, want nil", err)
			}
			synctest.Wait()

			cancel, err := sip.NewCancelRequestEnvelope(req)
			if err != nil {
				t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
			}
			fwdIntercept(t, elm, prx, cancel)
			waitCancelSend(t, tp)
			synctest.Wait()

			if err := txA.RecvResponse(ctx, newInRes(t, sends[0], sip.ResponseStatusRequestTerminated)); err != nil {
				t.Fatalf("txA.RecvResponse(487) error = %v, want nil", err)
			}
			synctest.Wait()

			// The related methods add no observation events of their own:
			// every recorded event still belongs to the parent operation.
			evs := rec.get()
			if len(evs) == 0 {
				t.Fatal("no observation events recorded")
			}
			for i, ev := range evs {
				if ev.RequestID != parent.RequestID {
					t.Fatalf("event %d RequestID = %q, want the parent operation %q (no child events)",
						i, ev.RequestID, parent.RequestID)
				}
			}
			// Response rules are unchanged: the CANCEL is answered 200 on
			// its own transaction, the INVITE sees its ordinary finals.
			ress := drainRess(tp)
			if diff := cmp.Diff([]int{200}, resStatuses(t, ress, sip.RequestMethodCancel)); diff != "" {
				t.Errorf("cancel responses (-want +got): %s", diff)
			}
			if diff := cmp.Diff([]int{180, 487}, resStatuses(t, ress, sip.RequestMethodInvite)); diff != "" {
				t.Errorf("invite responses (-want +got): %s", diff)
			}
		})
	})
}
