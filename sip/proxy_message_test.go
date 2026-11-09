package sip //nolint:testpackage // white-box preparation checks per the plan test boundary

import (
	"context"
	"encoding/json"
	"net/netip"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip/header"
)

// stubSrvTransp is a minimal [ServerTransport] used by the cached-response

// provenance subtest; it never touches the network.

type stubSrvTransp struct{}

func (stubSrvTransp) Metadata() TransportMetadata { return UDPMetadata() }
func (stubSrvTransp) SendResponse(context.Context, *ResponseEnvelope, ...SendResponseOptions) error {
	return nil
}

// prxOwnedViaTransp keeps white-box ownership fixtures address- and protocol-specific.
type prxOwnedViaTransp struct {
	*prxInternalTransp
	md     TransportMetadata
	sentBy Addr
}

func (tp *prxOwnedViaTransp) Metadata() TransportMetadata { return tp.md }
func (tp *prxOwnedViaTransp) MatchSentBy(addr Addr) bool  { return tp.sentBy.Equal(addr) }

func TestProxy_owned_via_loop_scan(t *testing.T) {
	t.Parallel()

	for _, mode := range []ForwardMode{ForwardModeStateful, ForwardModeStateless} {
		t.Run(mode.String(), func(t *testing.T) {
			t.Parallel()

			for _, tc := range []struct {
				name     string
				hops     []string
				wantLoop bool
				crossTP  bool
			}{
				{name: "foreign matching only", hops: []string{"foreign"}},
				{name: "owned matching", hops: []string{"owned"}, wantLoop: true},
				{name: "foreign matching before owned", hops: []string{"foreign", "owned"}, wantLoop: true},
				{name: "foreign changed before owned", hops: []string{"foreign changed", "owned"}, wantLoop: true},
				{name: "owned changed before matching", hops: []string{"changed", "owned"}, wantLoop: true},
				{name: "only owned changed routing", hops: []string{"changed", "changed"}},
				{name: "malformed branch", hops: []string{"malformed"}},
				{name: "unsupported namespace", hops: []string{"unsupported"}},
				{name: "missing branch", hops: []string{"no branch"}},
				{name: "invalid via", hops: []string{"invalid"}},
				{name: "missing transport", hops: []string{"missing transport"}},
				{name: "wrong protocol", hops: []string{"wrong protocol"}},
				{name: "foreign sent-by port", hops: []string{"foreign port"}},
				{name: "old transport differs from receiving", hops: []string{"owned"}, wantLoop: true, crossTP: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()

					a := netip.MustParseAddrPort("192.0.2.10:5060")
					b := netip.MustParseAddrPort("192.0.2.20:5060")
					target := ProxyTarget{ID: "same-target", URI: &URI{
						User: MakeUserInfo("alice"), Addr: MakeHostPortAddr("198.51.100.10", 5060),
					}}
					route := &ProxyRoute{Name: "ownership", ForwardMode: mode}
					newProxy := func(local netip.AddrPort, key string) (*Proxy, *prxOwnedViaTransp) {
						elm := &Element{}
						t.Cleanup(func() { _ = elm.Close() })
						tp := &prxOwnedViaTransp{
							prxInternalTransp: newPrxInternalTransp(),
							md:                UDPMetadata(),
							sentBy:            MakeHostPortAddr(local.Addr().String(), local.Port()),
						}
						if err := elm.TrackTransport(tp); err != nil {
							t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
						}
						prx, err := NewProxy(elm, []ProxyRoute{*route}, ProxyOptions{
							DetectLoops: true, StatelessKey: []byte(key),
							LocalIdentities: []*URI{{Addr: tp.sentBy}},
						})
						if err != nil {
							t.Fatalf("NewProxy() error = %v, want nil", err)
						}
						return prx, tp
					}
					prxA, tpA := newProxy(a, "independent-proxy-a-key")
					prxB, tpB := newProxy(b, "independent-proxy-b-key")
					prepare := func(prx *Proxy, in *RequestEnvelope, local netip.AddrPort) *RequestEnvelope {
						out, err := prx.prepareTargetReq(t.Context(), route, prxRequestInput{original: in, working: in}, target)
						if err != nil {
							t.Fatalf("prepareTargetReq() error = %v, want nil", err)
						}
						return out.SetTransport(UDPMetadata()).SetLocalAddr(local)
					}
					inA := newValidationReq(RequestMethodInfo, &URI{
						User: MakeUserInfo("alice"), Addr: MakeHostAddr("alice.voip.com"),
					}, header.MaxForwards(70))
					outA := prepare(prxA, inA, a)
					if err := prxA.finalizeTargetReq(t.Context(), route,
						prxRequestInput{original: inA, working: inA}, outA, "a-token"); err != nil {
						t.Fatalf("A finalizeTargetReq() error = %v, want nil", err)
					}
					viaA, _ := outA.Message().Headers.FirstVia()
					branch, _ := viaA.Branch()
					mark, fp, ok := parsePrxBranch(branch)
					if !ok || !tpA.MatchSentBy(viaA.Addr) || tpB.MatchSentBy(viaA.Addr) {
						t.Fatal("A fixture Via must be usable and owned only by A")
					}
					inB := NewRequestEnvelope(outA.Message().Clone().(*Request)).
						SetTransport(UDPMetadata()).SetLocalAddr(b).SetRemoteAddr(a)
					outB := prepare(prxB, inB, b)
					fingerprint := routeFingerprint
					wantMark := prxBranchStatefulMark
					if mode == ForwardModeStateless {
						fingerprint = routeFingerprintStateless
						wantMark = prxBranchStatelessMark
					}
					if got := fingerprint(inB, outB); got != fp || mark != wantMark {
						t.Fatalf("foreign fixture mark/fingerprint = %q/%q, want %q/%q", mark, fp, wantMark, got)
					}
					if got := fingerprint(inB, inB); got != fp {
						t.Fatalf("admission fixture fingerprint = %q, want A emitted %q", got, fp)
					}
					changed := cloneReqEnvelope(outB)
					changed.WithMessage(func(r *Request) {
						r.URI = &URI{Addr: MakeHostPortAddr("198.51.100.77", 5060)}
					})
					changedFP := fingerprint(inB, changed)
					if changedFP == fp {
						t.Fatal("changed routing fixture fingerprint equals matching fingerprint")
					}
					var hops header.Via
					for _, kind := range tc.hops {
						hop := viaA.Clone()
						hop.Addr = tpB.sentBy
						switch kind {
						case "foreign", "foreign changed":
							hop.Addr = tpA.sentBy
							if kind == "foreign changed" {
								hop.Params.Set("branch", makePrxBranch(mark, changedFP, "foreign"))
							}
						case "changed":
							hop.Params.Set("branch", makePrxBranch(mark, changedFP, "changed"))
						case "malformed":
							hop.Params.Set("branch", MagicCookie+"."+mark+".short.token")
						case "unsupported":
							hop.Params.Set("branch", makePrxBranch("other-proxy", fp, "unsupported"))
						case "no branch":
							hop.Params.Delete("branch")
						case "invalid":
							hop.Proto = ProtoInfo{}
						case "missing transport":
							hop.Transport = "TLS"
						case "wrong protocol":
							hop.Transport = "TCP"
						case "foreign port":
							hop.Addr = MakeHostPortAddr(b.Addr().String(), 5090)
						}
						hops = append(hops, hop)
					}
					// A tracked TCP transport does not own UDP's advertised address.
					tcp := &prxOwnedViaTransp{
						prxInternalTransp: newPrxInternalTransp(), md: TCPMetadata(),
						sentBy: MakeHostPortAddr("192.0.2.30", 5060),
					}
					if err := prxB.elm.TrackTransport(tcp); err != nil {
						t.Fatalf("elm.TrackTransport(TCP) error = %v, want nil", err)
					}
					inB.WithMessage(func(r *Request) { r.Headers.Set(hops) })
					ctx := t.Context()
					if tc.crossTP {
						inB.SetTransport(TCPMetadata())
						ctx = ContextWithTransport(ctx, tcp)
					}
					outB = prepare(prxB, inB, b)
					if got := prxB.detectLoop(inB, outB); got != tc.wantLoop {
						t.Errorf("detectLoop() = %v, want %v", got, tc.wantLoop)
					}
					if !tc.wantLoop {
						return
					}
					assertLoop := func(err error) {
						t.Helper()
						if !errors.Is(err, errLoopDetected) {
							t.Fatalf("proxy loop check error = %v, want %v", err, errLoopDetected)
						}
						rej, ok := errors.AsType[*RequestRejectedError](err)
						if !ok || rej.ResponseStatus() != ResponseStatusLoopDetected {
							t.Fatalf("proxy loop check error = %v, want rejection 482", err)
						}
					}
					assertLoop(prxB.validateReq(ctx, route, inB))
					assertLoop(prxB.finalizeTargetReq(ctx, route,
						prxRequestInput{original: inB, working: inB}, outB, "b-token"))
					for _, method := range []RequestMethod{RequestMethodAck, RequestMethodCancel} {
						related := cloneReqEnvelope(inB)
						related.WithMessage(func(r *Request) {
							r.Method = method
							cseq, _ := r.Headers.CSeq()
							cseq.Method = method
						})
						err := prxB.validateReq(ctx, route, related)
						if method == RequestMethodCancel {
							assertLoop(err)
						} else if !errors.Is(err, errLoopDetected) || !errors.Is(err, errPrxReqConsumed) {
							t.Fatalf("ACK loop check error = %v, want silent loop consumption", err)
						} else if _, ok := errors.AsType[*RequestRejectedError](err); ok {
							t.Fatalf("ACK loop check error = %v, want no response rejection", err)
						}
					}
				})
			}
		})
	}
}

func TestProxy_TransparentPreparation(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	prx := newValidationProxy(t)
	route := &ProxyRoute{Name: "test"}

	servedURI := func() *URI {
		return &URI{User: MakeUserInfo("alice"), Addr: MakeHostAddr("alice.example.com")}
	}
	targetURI := func() *URI {
		return &URI{User: MakeUserInfo("alice"), Addr: MakeHostPortAddr("198.51.100.10", 5060)}
	}

	reqMaxForwards := func(env *RequestEnvelope) (header.MaxForwards, bool) {
		var (
			mf header.MaxForwards
			ok bool
		)
		env.WithMessage(func(msg *Request) { mf, ok = msg.Headers.MaxForwards() })
		return mf, ok
	}

	t.Run("max-forwards decrements once per preparation", func(t *testing.T) {
		t.Parallel()

		req := newValidationReq(RequestMethodInvite, servedURI(), header.MaxForwards(1))
		origBody := []byte("v=0\r\no=- 1 1 IN IP4 10.0.0.5\r\ns=-\r\n")
		req.WithMessage(func(msg *Request) { msg.Body = origBody })

		trgt := ProxyTarget{ID: "t1", URI: targetURI()}

		out, err := prx.prepareTargetReq(ctx, route, prxRequestInput{original: req, working: req}, trgt)
		if err != nil {
			t.Fatalf("prx.prepareTargetReq() error = %v, want nil", err)
		}
		if out == req {
			t.Fatal("prx.prepareTargetReq() returned the inbound envelope, want an owned copy")
		}

		out.WithMessage(func(msg *Request) {
			gotMF, okMF := msg.Headers.MaxForwards()
			if !okMF || gotMF != 0 {
				t.Fatalf("max-forwards=%d (present=%v), want 0", gotMF, okMF)
			}
		})
		var gotBody []byte
		out.WithMessage(func(msg *Request) { gotBody = msg.Body })
		if diff := cmp.Diff(origBody, gotBody); diff != "" {
			t.Fatalf("body (-want +got): %s", diff)
		}

		// An address retry prepares a fresh copy from the same inbound
		// request: it must never decrement a second time.
		retry, err := prx.prepareTargetReq(ctx, route, prxRequestInput{original: req, working: req}, trgt)
		if err != nil {
			t.Fatalf("prx.prepareTargetReq() retry error = %v, want nil", err)
		}
		if gotMF, okMF := reqMaxForwards(retry); !okMF || gotMF != 0 {
			t.Fatalf("retry max-forwards=%d (present=%v), want 0", gotMF, okMF)
		}

		// The element send helper must retain the outgoing zero.
		sender := RequestSenderFunc(func(context.Context, *RequestEnvelope, ...SendRequestOptions) error {
			return nil
		})
		if err := prx.elm.InterceptOutboundRequest(ctx, sender, out); err != nil {
			t.Fatalf("elm.InterceptOutboundRequest() error = %v, want nil", err)
		}
		if gotMF, okMF := reqMaxForwards(out); !okMF || gotMF != 0 {
			t.Fatalf("max-forwards after element send helper = %d (present=%v), want 0", gotMF, okMF)
		}

		// The inbound request is untouched.
		if gotMF, okMF := reqMaxForwards(req); !okMF || gotMF != 1 {
			t.Fatalf("inbound max-forwards = %d (present=%v), want 1", gotMF, okMF)
		}
	})

	t.Run("absent max-forwards filled without decrement", func(t *testing.T) {
		t.Parallel()

		req := newValidationReq(RequestMethodInvite, servedURI())
		trgt := ProxyTarget{ID: "t1", URI: targetURI()}

		out, err := prx.prepareTargetReq(ctx, route, prxRequestInput{original: req, working: req}, trgt)
		if err != nil {
			t.Fatalf("prx.prepareTargetReq() error = %v, want nil", err)
		}
		if gotMF, okMF := reqMaxForwards(out); !okMF || gotMF != DefaultMaxForwards {
			t.Fatalf("max-forwards = %d (present=%v), want %d", gotMF, okMF, DefaultMaxForwards)
		}

		retry, err := prx.prepareTargetReq(ctx, route, prxRequestInput{original: req, working: req}, trgt)
		if err != nil {
			t.Fatalf("prx.prepareTargetReq() retry error = %v, want nil", err)
		}
		if gotMF, okMF := reqMaxForwards(retry); !okMF || gotMF != DefaultMaxForwards {
			t.Fatalf("retry max-forwards = %d (present=%v), want %d", gotMF, okMF, DefaultMaxForwards)
		}

		if _, okMF := reqMaxForwards(req); okMF {
			t.Fatal("inbound request gained a Max-Forwards header, want absent")
		}
	})

	t.Run("next hop route rewriting", func(t *testing.T) {
		t.Parallel()

		strictHop := header.RouteHop{URI: &URI{Addr: MakeHostAddr("strict.example.com")}}
		lrHop := header.RouteHop{
			URI: &URI{Addr: MakeHostAddr("lr.example.com"), Params: make(Values).Set("lr", "")},
		}

		tests := []struct {
			name       string
			routes     []header.RouteHop
			wantURI    *URI
			wantRoutes []header.RouteHop
		}{
			{
				name:    "strict first hop swaps into request URI",
				routes:  []header.RouteHop{strictHop, lrHop},
				wantURI: &URI{Addr: MakeHostAddr("strict.example.com")},
				wantRoutes: []header.RouteHop{
					lrHop,
					{URI: targetURI()},
				},
			},
			{
				name:       "loose first hop leaves request URI",
				routes:     []header.RouteHop{lrHop, strictHop},
				wantURI:    targetURI(),
				wantRoutes: []header.RouteHop{lrHop, strictHop},
			},
			{
				name:    "no route set leaves request URI",
				wantURI: targetURI(),
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				req := newValidationReq(RequestMethodInvite, servedURI())
				trgt := ProxyTarget{ID: "t1", URI: targetURI(), Routes: tt.routes}

				out, err := prx.prepareTargetReq(ctx, route, prxRequestInput{original: req, working: req}, trgt)
				if err != nil {
					t.Fatalf("prx.prepareTargetReq() error = %v, want nil", err)
				}

				if diff := cmp.Diff(tt.wantURI, out.URI()); diff != "" {
					t.Fatalf("request URI (-want +got): %s", diff)
				}
				if diff := cmp.Diff(tt.wantRoutes, reqRouteHops(out)); diff != "" {
					t.Fatalf("routes (-want +got): %s", diff)
				}
			})
		}
	})

	t.Run("fork copies are independent", func(t *testing.T) {
		t.Parallel()

		req := newValidationReq(RequestMethodInvite, servedURI(), header.MaxForwards(10))
		inURI := req.URI()
		inRoutes := reqRouteHops(req)

		lrHop := header.RouteHop{
			URI: &URI{Addr: MakeHostAddr("lr.example.com"), Params: make(Values).Set("lr", "")},
		}
		out1, err := prx.prepareTargetReq(ctx, route, prxRequestInput{original: req, working: req}, ProxyTarget{
			ID:     "t1",
			URI:    &URI{User: MakeUserInfo("one"), Addr: MakeHostAddr("198.51.100.1")},
			Routes: []header.RouteHop{lrHop},
		})
		if err != nil {
			t.Fatalf("prx.prepareTargetReq(t1) error = %v, want nil", err)
		}
		out2, err := prx.prepareTargetReq(ctx, route, prxRequestInput{original: req, working: req}, ProxyTarget{
			ID:  "t2",
			URI: &URI{User: MakeUserInfo("two"), Addr: MakeHostAddr("198.51.100.2")},
		})
		if err != nil {
			t.Fatalf("prx.prepareTargetReq(t2) error = %v, want nil", err)
		}
		if out1 == out2 || out1 == req || out2 == req {
			t.Fatal("prx.prepareTargetReq() aliased the inbound or sibling copy")
		}

		out1.WithMessage(func(msg *Request) {
			if ru, ok := msg.URI.(*URI); ok {
				ru.User = MakeUserInfo("mutated")
			}
			msg.Headers.Append(&header.Any{Name: "X-Test", Value: "1"})
			msg.Headers.Delete("Max-Forwards")
		})

		wantURI2 := &URI{User: MakeUserInfo("two"), Addr: MakeHostAddr("198.51.100.2")}
		if diff := cmp.Diff(wantURI2, out2.URI()); diff != "" {
			t.Fatalf("sibling request URI mutated (-want +got): %s", diff)
		}
		if gotMF, okMF := reqMaxForwards(out2); !okMF || gotMF != 9 {
			t.Fatalf("sibling max-forwards = %d (present=%v), want 9", gotMF, okMF)
		}

		if diff := cmp.Diff(inURI, req.URI()); diff != "" {
			t.Fatalf("inbound request URI mutated (-before +after): %s", diff)
		}
		if diff := cmp.Diff(inRoutes, reqRouteHops(req)); diff != "" {
			t.Fatalf("inbound routes mutated (-before +after): %s", diff)
		}
	})

	t.Run("forwarded response preserves To tag and body", func(t *testing.T) {
		t.Parallel()

		newFwdRes := func(sts ResponseStatus, toTag string, body []byte, preserve bool) *ResponseEnvelope {
			to := &header.To{
				URI: &URI{User: MakeUserInfo("alice"), Addr: MakeHostAddr("alice.example.com")},
			}
			if toTag != "" {
				to.Params = make(Values).Set("tag", toTag)
			}
			return NewResponseEnvelope(&Response{
				Status: sts,
				Reason: sts.Reason(),
				Proto:  protoVer20,
				Headers: make(Headers).
					Set(header.Via{{
						Proto:     protoVer20,
						Transport: udpMeta.Proto,
						Addr:      MakeHostPortAddr("10.0.0.5", 5060),
						Params:    make(Values).Set("branch", MagicCookie+".down-1"),
					}}).
					Set(&header.From{
						URI:    &URI{User: MakeUserInfo("bob"), Addr: MakeHostAddr("bob.example.net")},
						Params: make(Values).Set("tag", "from-1"),
					}).
					Set(to).
					Set(header.CallID("call-fwd@bob.example.net")).
					Set(&header.CSeq{SeqNum: 1, Method: RequestMethodInvite}),
				Body:          body,
				preserveToTag: preserve,
			})
		}

		resToTag := func(env *ResponseEnvelope) (string, bool) {
			var (
				tag string
				ok  bool
			)
			env.WithMessage(func(msg *Response) {
				if to, _ := msg.Headers.To(); to != nil {
					tag, ok = to.Tag()
				}
			})
			return tag, ok
		}
		resBody := func(env *ResponseEnvelope) []byte {
			var body []byte
			env.WithMessage(func(msg *Response) { body = msg.Body })
			return body
		}

		tests := []struct {
			name        string
			sts         ResponseStatus
			toTag       string
			preserve    bool
			wantTag     string
			wantTagless bool
		}{
			{
				name:     "forwarded provisional keeps tag",
				sts:      ResponseStatusRinging,
				toTag:    "down-tag-1",
				preserve: true,
				wantTag:  "down-tag-1",
			},
			{
				name:     "forwarded success keeps tag",
				sts:      ResponseStatusOK,
				toTag:    "down-tag-2",
				preserve: true,
				wantTag:  "down-tag-2",
			},
			{
				name:        "tagless forwarded stays tagless",
				sts:         ResponseStatusOK,
				preserve:    true,
				wantTagless: true,
			},
			{
				name: "endpoint response gets defaults",
				sts:  ResponseStatusOK,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				origBody := []byte("forwarded body")
				env := newFwdRes(tt.sts, tt.toTag, origBody, tt.preserve)

				sender := ResponseSenderFunc(
					func(context.Context, *ResponseEnvelope, ...SendResponseOptions) error {
						return nil
					},
				)
				if err := prx.elm.InterceptOutboundResponse(ctx, sender, env); err != nil {
					t.Fatalf("elm.InterceptOutboundResponse() error = %v, want nil", err)
				}

				// The transport SendResponse preparation path applies the same
				// EnsureResponseToTag call; the wire-path assertions are owned
				// by Plan 2's TestProxy_TransparentPreparationTransport.
				env.WithMessage(func(msg *Response) {
					EnsureResponseToTag(msg, GenerateStableToTag(msg, []byte("192.0.2.1:5060")))
				})

				tag, hasTag := resToTag(env)
				switch {
				case tt.wantTagless:
					if hasTag && tag != "" {
						t.Fatalf("forwarded response To tag = %q, want tagless", tag)
					}
				case tt.wantTag != "":
					if tag != tt.wantTag {
						t.Fatalf("forwarded response To tag = %q, want %q", tag, tt.wantTag)
					}
				default:
					if !hasTag || tag == "" {
						t.Fatal("endpoint response To tag missing, want generated")
					}
				}

				if diff := cmp.Diff(origBody, resBody(env)); diff != "" {
					t.Fatalf("body (-want +got): %s", diff)
				}
			})
		}
	})

	t.Run("inbound message not marked forwarded", func(t *testing.T) {
		t.Parallel()

		req := newValidationReq(RequestMethodInvite, servedURI())

		out, err := prx.prepareTargetReq(ctx, route, prxRequestInput{original: req, working: req}, ProxyTarget{ID: "t1", URI: targetURI()})
		if err != nil {
			t.Fatalf("prx.prepareTargetReq() error = %v, want nil", err)
		}

		out.WithMessage(func(msg *Request) {
			if !msg.forwarded {
				t.Fatal("prepared copy not marked forwarded")
			}
		})
		req.WithMessage(func(msg *Request) {
			if msg.forwarded {
				t.Fatal("inbound message marked forwarded")
			}
		})

		// A forwarded copy never gets an endpoint-invented From tag from the
		// element send helper; a local copy still does.
		req.WithMessage(func(msg *Request) {
			if from, ok := msg.Headers.From(); ok {
				from.Params.Delete("tag")
			}
		})
		out, err = prx.prepareTargetReq(ctx, route, prxRequestInput{original: req, working: req}, ProxyTarget{ID: "t1", URI: targetURI()})
		if err != nil {
			t.Fatalf("prx.prepareTargetReq() error = %v, want nil", err)
		}

		sender := RequestSenderFunc(func(context.Context, *RequestEnvelope, ...SendRequestOptions) error {
			return nil
		})
		if err := prx.elm.InterceptOutboundRequest(ctx, sender, out); err != nil {
			t.Fatalf("elm.InterceptOutboundRequest() error = %v, want nil", err)
		}
		out.WithMessage(func(msg *Request) {
			from, _ := msg.Headers.From()
			if tag, _ := from.Tag(); tag != "" {
				t.Fatalf("forwarded request From tag = %q, want tagless", tag)
			}
		})

		local := cloneReqEnvelope(req)
		if err := prx.elm.InterceptOutboundRequest(ctx, sender, local); err != nil {
			t.Fatalf("elm.InterceptOutboundRequest() error = %v, want nil", err)
		}
		local.WithMessage(func(msg *Request) {
			from, _ := msg.Headers.From()
			if tag, _ := from.Tag(); tag == "" {
				t.Fatal("local request From tag missing, want generated")
			}
		})

		// The marker is trusted in-memory provenance: JSON rebuilds reset it.
		data, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v, want nil", err)
		}
		var decoded RequestEnvelope
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("json.Unmarshal() error = %v, want nil", err)
		}
		decoded.WithMessage(func(msg *Request) {
			if msg.forwarded {
				t.Fatal("decoded request kept the forwarded marker")
			}
		})
	})

	t.Run("cached response provenance survives restore", func(t *testing.T) {
		t.Parallel()

		rmtAddr := netip.MustParseAddrPort("10.0.0.5:5060")

		// INVITE server transaction: snapshot capture reads the flag, the
		// restorer reapplies it to the owned cached response.
		invReq := newValidationReq(RequestMethodInvite, servedURI()).
			SetTransport(UDPMetadata()).
			SetRemoteAddr(rmtAddr)
		invTx, err := NewInviteServerTransaction(invReq, stubSrvTransp{})
		if err != nil {
			t.Fatalf("NewInviteServerTransaction() error = %v, want nil", err)
		}

		invRes, err := invReq.NewResponse(ResponseStatusRinging)
		if err != nil {
			t.Fatalf("invReq.NewResponse() error = %v, want nil", err)
		}
		invRes.WithMessage(func(msg *Response) { msg.preserveToTag = true })
		invTx.lastRes.Store(invRes)

		snap := invTx.takeSnapshot()
		if !snap.PreserveResponseToTag {
			t.Fatal("snapshot PreserveResponseToTag = false, want true")
		}

		restored, err := RestoreInviteServerTransaction(snap, stubSrvTransp{})
		if err != nil {
			t.Fatalf("RestoreInviteServerTransaction() error = %v, want nil", err)
		}
		restored.LastResponse().WithMessage(func(msg *Response) {
			if !msg.preserveToTag {
				t.Fatal("restored cached response lost preserveToTag")
			}
		})

		// Non-INVITE restorer: the checkpoint scalar is trusted state applied
		// to the owned cached response, never a message field.
		optReq := newValidationReq(RequestMethodOptions, servedURI()).
			SetTransport(UDPMetadata()).
			SetRemoteAddr(rmtAddr)
		key, err := MakeServerTransactionKey(optReq)
		if err != nil {
			t.Fatalf("MakeServerTransactionKey() error = %v, want nil", err)
		}
		optRes, err := optReq.NewResponse(ResponseStatusOK)
		if err != nil {
			t.Fatalf("optReq.NewResponse() error = %v, want nil", err)
		}
		optRes.WithMessage(func(msg *Response) { msg.preserveToTag = true })

		optSnap := &ServerTransactionSnapshot{
			Time:                  time.Now(),
			Type:                  TransactionTypeServerNonInvite,
			State:                 TransactionStateCompleted,
			Key:                   key,
			Request:               optReq,
			LastResponse:          optRes,
			PreserveResponseToTag: true,
		}
		restoredNI, err := RestoreNonInviteServerTransaction(optSnap, stubSrvTransp{})
		if err != nil {
			t.Fatalf("RestoreNonInviteServerTransaction() error = %v, want nil", err)
		}
		restoredNI.LastResponse().WithMessage(func(msg *Response) {
			if !msg.preserveToTag {
				t.Fatal("restored non-INVITE cached response lost preserveToTag")
			}
		})

		// A cached endpoint response keeps endpoint defaults: no flag means
		// the restorer does not invent forwarded-response provenance.
		optSnap.PreserveResponseToTag = false
		restoredEP, err := RestoreNonInviteServerTransaction(optSnap, stubSrvTransp{})
		if err != nil {
			t.Fatalf("RestoreNonInviteServerTransaction() error = %v, want nil", err)
		}
		restoredEP.LastResponse().WithMessage(func(msg *Response) {
			if msg.preserveToTag {
				t.Fatal("restored endpoint cached response gained preserveToTag")
			}
		})
	})
}

func TestProxy_sips_preparation(t *testing.T) {
	t.Parallel()

	preparations := []struct {
		name     string
		rewrite  bool
		restored bool
	}{
		{name: "sip contact rejected during preparation"},
		{name: "working sip uri cannot weaken original", rewrite: true},
		{name: "decoded working sip uri cannot weaken original", rewrite: true, restored: true},
	}
	for _, tt := range preparations {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prx := newValidationProxy(t)
			original := newValidationReq(RequestMethodInvite, &URI{
				User: MakeUserInfo("alice"), Addr: MakeHostAddr("alice.example.com"), Secured: true,
			}, header.MaxForwards(10))
			working := cloneReqEnvelope(original)
			if tt.rewrite {
				working.WithMessage(func(r *Request) {
					r.URI = &URI{User: MakeUserInfo("alice"), Addr: MakeHostAddr("alice.example.com")}
				})
			}
			if tt.restored {
				saved := struct {
					Original *RequestEnvelope `json:"original"`
					Working  *RequestEnvelope `json:"working"`
				}{Original: original, Working: working}
				data, err := json.Marshal(saved)
				if err != nil {
					t.Fatalf("json.Marshal() error = %v, want nil", err)
				}
				var decoded struct {
					Original *RequestEnvelope `json:"original"`
					Working  *RequestEnvelope `json:"working"`
				}
				if err := json.Unmarshal(data, &decoded); err != nil {
					t.Fatalf("json.Unmarshal() error = %v, want nil", err)
				}
				original, working = decoded.Original, decoded.Working
				if original.URI().Scheme() != "sips" || working.URI().Scheme() != "sip" {
					t.Fatalf("decoded URI schemes = %s/%s, want sips/sip", original.URI().Scheme(), working.URI().Scheme())
				}
			}
			input := prxRequestInput{original: original, working: working}
			sibling, err := prx.prepareTargetReq(t.Context(), nil, input, ProxyTarget{
				ID: "sibling", URI: &URI{Addr: MakeHostPortAddr("198.51.100.11", 5061), Secured: true},
			})
			if err != nil {
				t.Fatalf("secure sibling preparation error = %v, want nil", err)
			}
			siblingBefore := sibling.Message().Render()
			workingBefore := working.Message().Render()
			target := ProxyTarget{ID: "t1", URI: &URI{
				User: MakeUserInfo("alice"), Addr: MakeHostPortAddr("198.51.100.10", 5060),
			}}
			originalBefore := original.Message().Render()
			targetBefore := target.URI.String()
			var prepared *RequestEnvelope
			route := &ProxyRoute{
				Name: "sips",
				AdjustOutboundRequest: func(_ context.Context, _, out *RequestEnvelope, _ ProxyTarget) error {
					prepared = out
					return nil
				},
			}
			_, err = prx.prepareTargetReq(t.Context(), route, input, target)
			if !errors.Is(err, errSecureDowngrade) {
				t.Errorf("prx.prepareTargetReq() error = %v, want %v", err, errSecureDowngrade)
			}
			if prepared == nil {
				t.Fatal("adjustment did not receive a prepared request")
			}
			if prepared.Metadata().Has(prxFinalizedMetaKey) {
				t.Error("rejected preparation has a finalized marker")
			}
			prepared.WithMessage(func(r *Request) {
				var vias int
				for range r.Headers.Vias() {
					vias++
				}
				if vias != 1 {
					t.Errorf("rejected preparation Via count = %d, want 1 peer Via", vias)
				}
				if _, ok := r.Headers.FirstRecordRoute(); ok {
					t.Error("rejected preparation inserted Record-Route")
				}
			})
			if got := original.Message().Render(); got != originalBefore {
				t.Errorf("original request changed:\n%s", got)
			}
			if got := working.Message().Render(); got != workingBefore {
				t.Errorf("working request changed:\n%s", got)
			}
			if got := sibling.Message().Render(); got != siblingBefore {
				t.Errorf("sibling request changed:\n%s", got)
			}
			if got := target.URI.String(); got != targetBefore {
				t.Errorf("target URI = %s, want unchanged %s", got, targetBefore)
			}
		})
	}

	tests := []struct {
		name      string
		secureURI bool
		tp        TransportMetadata
	}{
		{name: "sip uri over tls", tp: TLSMetadata()},
		{name: "sip uri over udp", tp: UDPMetadata()},
		{name: "sips uri over udp", secureURI: true, tp: UDPMetadata()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prx := newValidationProxy(t)
			prx.opts.LocalIdentities = []*URI{
				{Addr: MakeHostAddr("proxy.example.org")},
				{Addr: MakeHostAddr("proxy.example.org"), Secured: true},
			}
			route := &ProxyRoute{Name: "sips", RecordRoute: true}
			original := newValidationReq(RequestMethodInvite, &URI{
				User: MakeUserInfo("alice"), Addr: MakeHostAddr("alice.example.com"), Secured: true,
			}).SetTransport(UDPMetadata())
			working := newValidationReq(RequestMethodInvite, &URI{
				User: MakeUserInfo("alice"), Addr: MakeHostAddr("alice.example.com"),
			}).SetTransport(TLSMetadata())
			out := newValidationReq(RequestMethodInvite, &URI{
				User: MakeUserInfo("alice"), Addr: MakeHostPortAddr("198.51.100.10", 5060), Secured: tt.secureURI,
			}).SetTransport(tt.tp)
			before := out.Message().Render()
			err := prx.finalizeTargetReq(t.Context(), route, prxRequestInput{original: original, working: working}, out, "sips-attempt")
			if !errors.Is(err, errSecureDowngrade) {
				t.Errorf("prx.finalizeTargetReq() error = %v, want %v", err, errSecureDowngrade)
			}
			if got := out.Message().Render(); got != before {
				t.Errorf("rejected finalization changed Via/Record-Route:\n%s", got)
			}
			if out.Metadata().Has(prxFinalizedMetaKey) {
				t.Error("rejected finalization inserted finalized marker")
			}
		})
	}

	t.Run("secure preparation retains working hook view", func(t *testing.T) {
		t.Parallel()

		prx := newValidationProxy(t)
		original := newValidationReq(RequestMethodInvite, &URI{
			Addr: MakeHostAddr("alice.example.com"), Secured: true,
		}, header.MaxForwards(10)).SetTransport(UDPMetadata())
		working := newValidationReq(RequestMethodInvite, &URI{
			Addr: MakeHostAddr("working.example.com"),
		}, header.MaxForwards(7)).SetTransport(TLSMetadata())
		working.WithMessage(func(r *Request) { r.Body = []byte("working body") })
		originalBefore := original.Message().Render()
		workingBefore := working.Message().Render()
		var adjustments, recordRoutes int
		route := &ProxyRoute{
			Name: "sips", RecordRoute: true,
			AdjustOutboundRequest: func(_ context.Context, in, _ *RequestEnvelope, _ ProxyTarget) error {
				adjustments++
				if in != working {
					t.Error("adjustment input is not the working request")
				}
				return nil
			},
		}
		prx.opts.RecordRouteURI = func(_ context.Context, in, _ *RequestEnvelope) (*URI, error) {
			recordRoutes++
			if in != working || in.URI().Scheme() != "sip" || !in.Transport().Secured() {
				t.Errorf("Record-Route input = %+v, want working SIP request over TLS", in)
			}
			return &URI{Addr: MakeHostAddr("proxy.example.org"), Secured: true}, nil
		}
		input := prxRequestInput{original: original, working: working}
		out, err := prx.prepareTargetReq(t.Context(), route, input, ProxyTarget{
			ID: "secure", URI: &URI{Addr: MakeHostPortAddr("198.51.100.10", 5061), Secured: true},
		})
		if err != nil {
			t.Fatalf("prx.prepareTargetReq() error = %v, want nil", err)
		}
		out.SetTransport(TLSMetadata())
		if err := prx.finalizeTargetReq(t.Context(), route, input, out, "working-view"); err != nil {
			t.Fatalf("prx.finalizeTargetReq() error = %v, want nil", err)
		}
		out.WithMessage(func(r *Request) {
			if got, ok := r.Headers.MaxForwards(); !ok || got != 6 {
				t.Errorf("forwarded Max-Forwards = %d (present %v), want 6", got, ok)
			}
			if got := string(r.Body); got != "working body" {
				t.Errorf("forwarded body = %q, want working body", got)
			}
			if hop, ok := r.Headers.FirstRecordRoute(); !ok || hop.URI.Scheme() != "sips" {
				t.Errorf("Record-Route = %+v (present %v), want SIPS", hop, ok)
			}
		})
		if adjustments != 1 || recordRoutes != 1 {
			t.Errorf("hook calls = %d/%d, want 1/1", adjustments, recordRoutes)
		}
		if original.Message().Render() != originalBefore || working.Message().Render() != workingBefore {
			t.Error("secure preparation mutated original or working input")
		}
	})
}

func TestProxy_IdentitySecurity(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	servedURI := func() *URI {
		return &URI{User: MakeUserInfo("alice"), Addr: MakeHostAddr("alice.example.com")}
	}
	targetURI := func() *URI {
		return &URI{User: MakeUserInfo("alice"), Addr: MakeHostPortAddr("198.51.100.10", 5060)}
	}
	sipsTargetURI := func() *URI {
		return &URI{User: MakeUserInfo("alice"), Addr: MakeHostPortAddr("198.51.100.60", 5061), Secured: true}
	}
	sipIdentity := func() *URI {
		return &URI{Addr: MakeHostAddr("pxy.example.net")}
	}
	sipsIdentity := func() *URI {
		return &URI{Addr: MakeHostPortAddr("pxy.example.net", 5061), Secured: true}
	}

	newProxy := func(tb testing.TB, opts ProxyOptions, ids ...*URI) *Proxy {
		tb.Helper()
		elm := &Element{
			BaseCapabilities: ElementCapabilities{
				Options: []header.OptionTag{"timer"},
			},
		}
		tb.Cleanup(func() { _ = elm.Close() })
		return &Proxy{
			elm:         elm,
			opts:        opts.clone(),
			selfAddrs:   prxIdentityAddrs(ids),
			domainAddrs: prxDomainAddrs([]string{"alice.example.com"}),
		}
	}

	prepare := func(tb testing.TB, prx *Proxy, in *RequestEnvelope, target ProxyTarget) *RequestEnvelope {
		tb.Helper()
		out, err := prx.prepareTargetReq(ctx, &ProxyRoute{Name: "test"}, prxRequestInput{original: in, working: in}, target)
		if err != nil {
			tb.Fatalf("prx.prepareTargetReq() error = %v, want nil", err)
		}
		return out
	}

	viaHops := func(env *RequestEnvelope) []header.ViaHop {
		var hops []header.ViaHop
		env.WithMessage(func(r *Request) {
			for hop := range r.Headers.Vias() {
				hops = append(hops, hop.Clone())
			}
		})
		return hops
	}

	rrCount := func(env *RequestEnvelope) int {
		var n int
		env.WithMessage(func(r *Request) {
			for range r.Headers.RecordRoutes() {
				n++
			}
		})
		return n
	}

	t.Run("record-route selection", func(t *testing.T) {
		t.Parallel()

		in := newValidationReq(RequestMethodInvite, servedURI())
		plainOut := func() *RequestEnvelope { return newValidationReq(RequestMethodInvite, targetURI()) }
		sipsOut := func() *RequestEnvelope { return newValidationReq(RequestMethodInvite, sipsTargetURI()) }

		t.Run("hook result cloned and used", func(t *testing.T) {
			t.Parallel()

			hookURI := &URI{User: MakeUserInfo("proxy"), Addr: MakeHostAddr("adv.example.net")}
			prx := newProxy(t, ProxyOptions{
				LocalIdentities: []*URI{sipIdentity()},
				RecordRouteURI: func(context.Context, *RequestEnvelope, *RequestEnvelope) (*URI, error) {
					return hookURI, nil
				},
			}, sipIdentity())

			got, err := prx.chooseRecRoute(ctx, in, plainOut())
			if err != nil {
				t.Fatalf("chooseRecordRoute() error = %v, want nil", err)
			}
			want := &URI{
				User:   MakeUserInfo("proxy"),
				Addr:   MakeHostAddr("adv.example.net"),
				Params: make(Values).Set("lr", ""),
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("record-route URI (-want +got): %s", diff)
			}
			if hookURI.Params.Has("lr") {
				t.Fatal("chooseRecordRoute() mutated the hook-owned URI")
			}
		})

		t.Run("hook failures rejected", func(t *testing.T) {
			t.Parallel()

			hookErr := errors.ErrorWrap("hook exploded")
			tests := []struct {
				name    string
				hook    func(context.Context, *RequestEnvelope, *RequestEnvelope) (*URI, error)
				out     *RequestEnvelope
				wantErr error
			}{
				{
					name: "nil URI",
					hook: func(context.Context, *RequestEnvelope, *RequestEnvelope) (*URI, error) {
						return nil, nil
					},
					out: plainOut(),
				},
				{
					name: "invalid URI",
					hook: func(context.Context, *RequestEnvelope, *RequestEnvelope) (*URI, error) {
						return &URI{}, nil
					},
					out: plainOut(),
				},
				{
					name: "unreachable URI",
					hook: func(context.Context, *RequestEnvelope, *RequestEnvelope) (*URI, error) {
						return &URI{Addr: MakeHostAddr("0.0.0.0")}, nil
					},
					out: plainOut(),
				},
				{
					name: "secure downgrade",
					hook: func(context.Context, *RequestEnvelope, *RequestEnvelope) (*URI, error) {
						return &URI{Addr: MakeHostAddr("adv.example.net")}, nil
					},
					out:     sipsOut(),
					wantErr: errSecureDowngrade,
				},
				{
					name: "hook error",
					hook: func(context.Context, *RequestEnvelope, *RequestEnvelope) (*URI, error) {
						return nil, hookErr
					},
					out:     plainOut(),
					wantErr: hookErr,
				},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()

					prx := newProxy(t, ProxyOptions{
						LocalIdentities: []*URI{sipsIdentity()},
						RecordRouteURI:  tt.hook,
					}, sipsIdentity())

					_, err := prx.chooseRecRoute(ctx, in, tt.out)
					if err == nil {
						t.Fatal("chooseRecordRoute() error = nil, want rejection")
					}
					if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
						t.Fatalf("chooseRecordRoute() error = %v, want %v", err, tt.wantErr)
					}
				})
			}
		})

		t.Run("configured identities", func(t *testing.T) {
			t.Parallel()

			t.Run("single compatible chosen", func(t *testing.T) {
				t.Parallel()

				id := sipIdentity()
				prx := newProxy(t, ProxyOptions{LocalIdentities: []*URI{id}}, id)

				got, err := prx.chooseRecRoute(ctx, in, plainOut())
				if err != nil {
					t.Fatalf("chooseRecordRoute() error = %v, want nil", err)
				}
				want := &URI{
					Addr:   MakeHostAddr("pxy.example.net"),
					Params: make(Values).Set("lr", ""),
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Fatalf("record-route URI (-want +got): %s", diff)
				}
				if id.Params.Has("lr") {
					t.Fatal("chooseRecordRoute() mutated the configured identity")
				}
			})

			t.Run("zero identities rejected", func(t *testing.T) {
				t.Parallel()

				// No LocalAddrs[0] fallback: an empty identity list is an
				// explicit failure even though the element exists.
				prx := newProxy(t, ProxyOptions{})

				if _, err := prx.chooseRecRoute(ctx, in, plainOut()); err == nil {
					t.Fatal("chooseRecordRoute() error = nil, want rejection")
				}
			})

			t.Run("multiple compatible rejected", func(t *testing.T) {
				t.Parallel()

				ids := []*URI{
					{Addr: MakeHostAddr("pxy-a.example.net")},
					{Addr: MakeHostAddr("pxy-b.example.net")},
				}
				prx := newProxy(t, ProxyOptions{LocalIdentities: ids}, ids...)

				if _, err := prx.chooseRecRoute(ctx, in, plainOut()); err == nil {
					t.Fatal("chooseRecordRoute() error = nil, want rejection")
				}
			})

			t.Run("incompatible filtered out", func(t *testing.T) {
				t.Parallel()

				ids := []*URI{sipIdentity(), sipsIdentity()}
				prx := newProxy(t, ProxyOptions{LocalIdentities: ids}, ids...)

				got, err := prx.chooseRecRoute(ctx, in, sipsOut())
				if err != nil {
					t.Fatalf("chooseRecordRoute() error = %v, want nil", err)
				}
				if got, want := got.Scheme(), "sips"; got != want {
					t.Fatalf("scheme=%q, want %q", got, want)
				}
				if !got.Params.Has("lr") {
					t.Fatal("record-route URI misses the lr parameter")
				}
			})

			t.Run("no compatible rejected", func(t *testing.T) {
				t.Parallel()

				ids := []*URI{sipIdentity()}
				prx := newProxy(t, ProxyOptions{LocalIdentities: ids}, ids...)

				if _, err := prx.chooseRecRoute(ctx, in, sipsOut()); err == nil {
					t.Fatal("chooseRecordRoute() error = nil, want rejection")
				}
			})

			t.Run("unusable identity filtered", func(t *testing.T) {
				t.Parallel()

				ids := []*URI{{Addr: MakeHostAddr("0.0.0.0")}, sipsIdentity()}
				prx := newProxy(t, ProxyOptions{LocalIdentities: ids}, ids...)

				got, err := prx.chooseRecRoute(ctx, in, sipsOut())
				if err != nil {
					t.Fatalf("chooseRecordRoute() error = %v, want nil", err)
				}
				if got, want := got.Scheme(), "sips"; got != want {
					t.Fatalf("scheme=%q, want %q", got, want)
				}
			})
		})
	})

	t.Run("finalize secure route", func(t *testing.T) {
		t.Parallel()

		prx := newProxy(t, ProxyOptions{LocalIdentities: []*URI{sipsIdentity()}}, sipsIdentity())
		route := &ProxyRoute{Name: "test", RecordRoute: true}

		in := newValidationReq(RequestMethodInvite, servedURI())
		out := prepare(t, prx, in, ProxyTarget{ID: "t1", URI: sipsTargetURI()}).
			SetTransport(TLSMetadata()).
			SetLocalAddr(netip.MustParseAddrPort("192.0.2.10:5061"))

		if err := prx.finalizeTargetReq(ctx, route, prxRequestInput{original: in, working: in}, out, "attempt-1"); err != nil {
			t.Fatalf("prx.finalizeTargetReq() error = %v, want nil", err)
		}

		var recordRoute *URI
		out.WithMessage(func(r *Request) {
			hop, ok := r.Headers.FirstRecordRoute()
			if !ok {
				t.Error("finalized request misses Record-Route")
				return
			}
			u, ok := hop.URI.(*URI)
			if !ok {
				t.Error("Record-Route URI is not a SIP URI")
				return
			}
			recordRoute = u
		})
		if recordRoute == nil {
			t.Fatal("finalized request misses a usable Record-Route")
		}
		if got, want := recordRoute.Scheme(), "sips"; got != want {
			t.Fatalf("scheme=%q, want %q", got, want)
		}
		if !recordRoute.Params.Has("lr") {
			t.Fatal("record-route URI misses the lr parameter")
		}
	})

	t.Run("sips downgrade rejected", func(t *testing.T) {
		t.Parallel()

		prx := newProxy(t, ProxyOptions{LocalIdentities: []*URI{sipsIdentity()}}, sipsIdentity())
		route := &ProxyRoute{Name: "test", RecordRoute: true}

		in := newValidationReq(RequestMethodInvite, servedURI())
		out := prepare(t, prx, in, ProxyTarget{ID: "t1", URI: sipsTargetURI()}).
			SetTransport(UDPMetadata())

		err := prx.finalizeTargetReq(ctx, route, prxRequestInput{original: in, working: in}, out, "attempt-1")
		if err == nil {
			t.Fatal("secure downgrade accepted")
		}
		if !errors.Is(err, errSecureDowngrade) {
			t.Fatalf("prx.finalizeTargetReq() error = %v, want %v", err, errSecureDowngrade)
		}
		for _, hop := range viaHops(out) {
			if b, _ := hop.Branch(); isPrxBranch(b) {
				t.Fatal("prx.finalizeTargetReq() inserted a proxy Via despite the failure")
			}
		}
	})

	t.Run("via markers and invariants", func(t *testing.T) {
		t.Parallel()

		prx := newProxy(t, ProxyOptions{LocalIdentities: []*URI{sipIdentity()}}, sipIdentity())

		t.Run("stateful marker", func(t *testing.T) {
			t.Parallel()

			route := &ProxyRoute{Name: "test"}
			in := newValidationReq(RequestMethodInvite, servedURI())
			out := prepare(t, prx, in, ProxyTarget{ID: "t1", URI: targetURI()}).
				SetTransport(UDPMetadata())

			if err := prx.finalizeTargetReq(ctx, route, prxRequestInput{original: in, working: in}, out, "attempt-1"); err != nil {
				t.Fatalf("prx.finalizeTargetReq() error = %v, want nil", err)
			}

			hops := viaHops(out)
			if got, want := len(hops), 2; got != want {
				t.Fatalf("via hops = %d, want %d", got, want)
			}
			branch, _ := hops[0].Branch()
			mark, fp, ok := parsePrxBranch(branch)
			if !ok {
				t.Fatalf("top via branch %q is not a proxy-namespace branch", branch)
			}
			if got, want := mark, prxBranchStatefulMark; got != want {
				t.Fatalf("branch mark = %q, want %q", got, want)
			}
			if got, want := len(fp), 32; got != want {
				t.Fatalf("branch fingerprint length = %d, want %d", got, want)
			}
			if !IsRFC3261Branch(branch) {
				t.Fatalf("branch %q is not an RFC 3261 branch", branch)
			}
			if got, want := hops[0].Transport, TransportProto("UDP"); got != want {
				t.Fatalf("top via transport = %q, want %q", got, want)
			}
			if got, want := hops[0].Addr.IsValid(), true; got != want {
				t.Fatalf("top via sent-by invalid, want valid placeholder")
			}
			if b, _ := hops[1].Branch(); b != MagicCookie+".validate-1" {
				t.Fatalf("forwarded peer via branch = %q, want preserved", b)
			}

			// The inbound request keeps only the peer's Via.
			for _, hop := range viaHops(in) {
				if b, _ := hop.Branch(); isPrxBranch(b) {
					t.Fatal("prx.finalizeTargetReq() mutated the inbound request")
				}
			}
		})

		t.Run("stateless marker", func(t *testing.T) {
			t.Parallel()

			route := &ProxyRoute{Name: "test", ForwardMode: ForwardModeStateless}
			in := newValidationReq(RequestMethodInvite, servedURI())
			out := prepare(t, prx, in, ProxyTarget{ID: "t1", URI: targetURI()}).
				SetTransport(UDPMetadata())

			if err := prx.finalizeTargetReq(ctx, route, prxRequestInput{original: in, working: in}, out, "attempt-1"); err != nil {
				t.Fatalf("prx.finalizeTargetReq() error = %v, want nil", err)
			}
			branch, _ := viaHops(out)[0].Branch()
			mark, _, ok := parsePrxBranch(branch)
			if !ok {
				t.Fatalf("top via branch %q is not a proxy-namespace branch", branch)
			}
			if got, want := mark, prxBranchStatelessMark; got != want {
				t.Fatalf("branch mark = %q, want %q", got, want)
			}
		})

		t.Run("duplicate proxy via rejected", func(t *testing.T) {
			t.Parallel()

			route := &ProxyRoute{Name: "test", RecordRoute: true}
			in := newValidationReq(RequestMethodInvite, servedURI())
			out := prepare(t, prx, in, ProxyTarget{ID: "t1", URI: targetURI()}).
				SetTransport(UDPMetadata())

			if err := prx.finalizeTargetReq(ctx, route, prxRequestInput{original: in, working: in}, out, "attempt-1"); err != nil {
				t.Fatalf("prx.finalizeTargetReq() error = %v, want nil", err)
			}
			wantRR := rrCount(out)
			if wantRR != 1 {
				t.Fatalf("record-route count = %d, want 1", wantRR)
			}

			if err := prx.finalizeTargetReq(ctx, route, prxRequestInput{original: in, working: in}, out, "attempt-1b"); err == nil {
				t.Fatal("second prx.finalizeTargetReq() error = nil, want rejection")
			}

			// A rejected call leaves the copy untouched: no duplicate
			// Record-Route and still exactly one proxy Via.
			if got := rrCount(out); got != wantRR {
				t.Fatalf("record-route count after rejection = %d, want %d", got, wantRR)
			}
			var own int
			for _, hop := range viaHops(out) {
				if b, _ := hop.Branch(); isPrxBranch(b) {
					own++
				}
			}
			if own != 1 {
				t.Fatalf("proxy via count = %d, want 1", own)
			}
		})

		t.Run("invariants rejected", func(t *testing.T) {
			t.Parallel()

			route := &ProxyRoute{Name: "test"}
			in := newValidationReq(RequestMethodInvite, servedURI())

			out := prepare(t, prx, in, ProxyTarget{ID: "t1", URI: targetURI()})
			if err := prx.finalizeTargetReq(ctx, route, prxRequestInput{original: in, working: in}, out, "attempt-1"); err == nil {
				t.Fatal("prx.finalizeTargetReq() without transport error = nil, want rejection")
			}

			out = prepare(t, prx, in, ProxyTarget{ID: "t1", URI: targetURI()}).
				SetTransport(UDPMetadata())
			if err := prx.finalizeTargetReq(ctx, route, prxRequestInput{original: in, working: in}, out, ""); err == nil {
				t.Fatal("prx.finalizeTargetReq() without branch error = nil, want rejection")
			}
		})
	})

	t.Run("forwarded top via protected", func(t *testing.T) {
		t.Parallel()

		prx := newProxy(t, ProxyOptions{LocalIdentities: []*URI{sipIdentity()}}, sipIdentity())
		route := &ProxyRoute{Name: "test"}

		in := newValidationReq(RequestMethodInvite, servedURI())
		out := prepare(t, prx, in, ProxyTarget{ID: "t1", URI: targetURI()}).
			SetTransport(UDPMetadata())

		// Before the proxy inserts its own hop, the forwarded peer's top Via
		// is never rewritten by endpoint conveniences.
		out.WithMessage(func(r *Request) {
			EnsureRequestVia(r, "TCP", MakeHostPortAddr("192.0.2.9", 5060))
		})
		top := viaHops(out)[0]
		if got, want := top.Transport, TransportProto("UDP"); got != want {
			t.Fatalf("forwarded peer via transport = %q, want %q", got, want)
		}
		if got, want := top.Addr.String(), "10.0.0.5:5060"; got != want {
			t.Fatalf("forwarded peer via sent-by = %q, want %q", got, want)
		}
		if b, _ := top.Branch(); b != MagicCookie+".validate-1" {
			t.Fatalf("forwarded peer via branch = %q, want %q", b, MagicCookie+".validate-1")
		}

		// After finalization the proxy-owned top hop may be filled with the
		// selected transport and sent-by while its branch stays untouched.
		if err := prx.finalizeTargetReq(ctx, route, prxRequestInput{original: in, working: in}, out, "attempt-1"); err != nil {
			t.Fatalf("prx.finalizeTargetReq() error = %v, want nil", err)
		}
		origBranch, _ := viaHops(out)[0].Branch()
		out.WithMessage(func(r *Request) {
			EnsureRequestVia(r, "TCP", MakeHostPortAddr("192.0.2.9", 5060))
		})
		top = viaHops(out)[0]
		if got, want := top.Transport, TransportProto("TCP"); got != want {
			t.Fatalf("proxy via transport = %q, want %q", got, want)
		}
		if got, want := top.Addr.String(), "192.0.2.9:5060"; got != want {
			t.Fatalf("proxy via sent-by = %q, want %q", got, want)
		}
		if b, _ := top.Branch(); b != origBranch {
			t.Fatalf("proxy via branch = %q, want preserved %q", b, origBranch)
		}
	})

	t.Run("loop detection", func(t *testing.T) {
		t.Parallel()

		prxLoop := newProxy(t, ProxyOptions{DetectLoops: true, LocalIdentities: []*URI{sipIdentity()}}, sipIdentity())
		prxPlain := newProxy(t, ProxyOptions{LocalIdentities: []*URI{sipIdentity()}}, sipIdentity())
		local := netip.MustParseAddrPort("192.0.2.20:5060")
		tp := &prxOwnedViaTransp{
			prxInternalTransp: newPrxInternalTransp(), md: UDPMetadata(),
			sentBy: MakeHostPortAddr(local.Addr().String(), local.Port()),
		}
		if err := prxLoop.elm.TrackTransport(tp); err != nil {
			t.Fatalf("elm.TrackTransport() error = %v, want nil", err)
		}
		route := &ProxyRoute{Name: "test"}
		target := ProxyTarget{ID: "t1", URI: targetURI()}

		in1 := newValidationReq(RequestMethodInvite, servedURI())
		out1 := prepare(t, prxLoop, in1, target).SetTransport(UDPMetadata()).SetLocalAddr(local)
		if err := prxLoop.finalizeTargetReq(ctx, route, prxRequestInput{original: in1, working: in1}, out1, "tok-1"); err != nil {
			t.Fatalf("prx.finalizeTargetReq() error = %v, want nil", err)
		}

		// The emitted copy returns unchanged: a true loop.
		in2 := cloneReqEnvelope(out1).SetTransport(UDPMetadata())

		err := prxLoop.validateReq(ctx, route, in2)
		if err == nil {
			t.Fatal("prx.validateReq() accepted a looped request, want 482 rejection")
		}
		if !errors.Is(err, errLoopDetected) {
			t.Fatalf("prx.validateReq() error = %v, want %v", err, errLoopDetected)
		}
		rej, ok := errors.AsType[*RequestRejectedError](err)
		if !ok {
			t.Fatalf("prx.validateReq() error = %v, want *RequestRejectedError", err)
		}
		if got, want := rej.ResponseStatus(), ResponseStatusLoopDetected; got != want {
			t.Fatalf("rejection status = %v, want %v", got, want)
		}

		// Loop detection is off without DetectLoops: the same request is a
		// spiral at worst and admission accepts it.
		if err := prxPlain.validateReq(ctx, route, in2); err != nil {
			t.Fatalf("prx.validateReq() error = %v, want nil with DetectLoops off", err)
		}

		// The copy-level check reports the loop as well and refuses the send.
		out2 := prepare(t, prxLoop, in2, target).SetTransport(UDPMetadata())
		if !prxLoop.detectLoop(in2, out2) {
			t.Fatal("detectLoop() = false, want true for an unchanged returning request")
		}
		if err := prxLoop.finalizeTargetReq(ctx, route, prxRequestInput{original: in2, working: in2}, out2, "tok-2"); !errors.Is(err, errLoopDetected) {
			t.Fatalf("prx.finalizeTargetReq() error = %v, want %v", err, errLoopDetected)
		}

		// A request returning with changed routing-relevant data is a
		// spiral, not a loop.
		in2s := cloneReqEnvelope(out1).SetTransport(UDPMetadata())
		in2s.WithMessage(func(r *Request) {
			r.URI = &URI{User: MakeUserInfo("bob"), Addr: MakeHostAddr("bob.example.org")}
		})
		if err := prxLoop.validateReq(ctx, route, in2s); err != nil {
			t.Fatalf("prx.validateReq() spiral error = %v, want nil", err)
		}

		spiralTarget := ProxyTarget{ID: "t1", URI: &URI{
			User: MakeUserInfo("bob"),
			Addr: MakeHostPortAddr("198.51.100.77", 5060),
		}}
		out2s := prepare(t, prxLoop, in2s, spiralTarget).SetTransport(UDPMetadata())
		if prxLoop.detectLoop(in2s, out2s) {
			t.Fatal("detectLoop() = true for a changed-route spiral, want false")
		}

		// A changed Proxy-Require is routing-relevant data too.
		in2p := cloneReqEnvelope(out1).SetTransport(UDPMetadata())
		in2p.WithMessage(func(r *Request) {
			r.Headers.Append(header.ProxyRequire{"timer"})
		})
		if err := prxLoop.validateReq(ctx, route, in2p); err != nil {
			t.Fatalf("prx.validateReq() spiral error = %v, want nil", err)
		}
		out2p := prepare(t, prxLoop, in2p, target).SetTransport(UDPMetadata())
		if prxLoop.detectLoop(in2p, out2p) {
			t.Fatal("detectLoop() = true for a Proxy-Require spiral, want false")
		}
	})
}
