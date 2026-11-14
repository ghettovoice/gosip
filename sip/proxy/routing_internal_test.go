package proxy

import (
	"context"
	"net/netip"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func newValidationProxy(tb testing.TB) (*sip.Element, *Proxy) {
	tb.Helper()

	elm := &sip.Element{
		BaseCapabilities: sip.ElementCapabilities{
			Options:   []header.OptionTag{"timer"},
			MIMETypes: []header.MIMERange{{Type: "application", Subtype: "sdp"}},
		},
	}
	tb.Cleanup(func() { _ = elm.Close() })

	return elm, &Proxy{
		selfAddrs:   identityAddrs([]*sip.URI{{Addr: sip.MakeHostAddr("pxy.example.net")}}),
		domainAddrs: domainAddrs([]string{"alice.example.com"}),
	}
}

func newValidationReq(method sip.RequestMethod, ruri sip.AnyURI, hdrs ...sip.Header) *sip.RequestEnvelope {
	msg := &sip.Request{
		Method: method,
		URI:    ruri,
		Proto:  sip.ProtoVer20(),
		Headers: make(sip.Headers).
			Set(header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: sip.UDPMetadata().Proto,
					Addr:      sip.MakeHostPortAddr("10.0.0.5", 5060),
					Params:    make(sip.Values).Set("branch", sip.MagicCookie+".validate-1"),
				},
			}).
			Set(&header.From{
				URI:    &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("bob.example.net")},
				Params: make(sip.Values).Set("tag", "from-1"),
			}).
			Set(&header.To{
				URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.example.com")},
			}).
			Set(header.CallID("call-validate@bob.example.net")).
			Set(&header.CSeq{SeqNum: 1, Method: method}),
	}
	for _, hdr := range hdrs {
		msg.Headers.Append(hdr)
	}

	return sip.NewRequestEnvelope(msg)
}

func TestProxy_ValidateRequest(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	elm, prx := newValidationProxy(t)
	route := &Route{Name: "test"}

	validationURI := &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.example.com")}

	tests := []struct {
		name            string
		method          sip.RequestMethod
		uri             sip.AnyURI
		hdrs            []sip.Header
		wantStatus      sip.ResponseStatus
		wantCause       error
		wantConsumed    bool
		wantUnsupported header.Unsupported
		wantAnswerHdrs  []sip.HeaderName
	}{
		{
			name:   "valid request accepted",
			method: sip.RequestMethodInvite,
			uri:    validationURI,
		},
		{
			name:   "unknown method accepted",
			method: sip.RequestMethod("FOO"),
			uri:    validationURI,
			hdrs:   []sip.Header{&header.Any{Name: "X-Ext-Thing", Value: "yes"}},
		},
		{
			name:       "malformed routing syntax rejected",
			method:     sip.RequestMethodInvite,
			uri:        validationURI,
			hdrs:       []sip.Header{header.Route{{}}}, // hop without a URI
			wantStatus: sip.ResponseStatusBadRequest,
			wantCause:  sip.ErrInvalidMessage,
		},
		{
			name:       "unsupported URI scheme rejected",
			method:     sip.RequestMethodInvite,
			uri:        &uri.Tel{Number: "+12025550100"},
			wantStatus: sip.ResponseStatusUnsupportedURIScheme,
			wantCause:  errUnsupURIScheme,
		},
		{
			name:       "exhausted max-forwards rejected",
			method:     sip.RequestMethodInvite,
			uri:        validationURI,
			hdrs:       []sip.Header{header.MaxForwards(0)},
			wantStatus: sip.ResponseStatusTooManyHops,
			wantCause:  errTooManyHops,
		},
		{
			name:   "unsupported proxy-require rejected",
			method: sip.RequestMethodInvite,
			uri:    validationURI,
			hdrs: []sip.Header{header.ProxyRequire{
				"timer",     // supported by the element
				"sec-agree", // not supported
			}},
			wantStatus:      sip.ResponseStatusBadExtension,
			wantCause:       errUnsupOption,
			wantUnsupported: header.Unsupported{"sec-agree"},
		},
		{
			name:   "all unsupported proxy-require tags listed",
			method: sip.RequestMethodInvite,
			uri:    validationURI,
			hdrs: []sip.Header{header.ProxyRequire{
				"ext-one",
				"ext-two",
			}},
			wantStatus:      sip.ResponseStatusBadExtension,
			wantCause:       errUnsupOption,
			wantUnsupported: header.Unsupported{"ext-one", "ext-two"},
		},
		{
			name:         "malformed ACK silently consumed",
			method:       sip.RequestMethodAck,
			uri:          validationURI,
			hdrs:         []sip.Header{header.Route{{}}},
			wantConsumed: true,
			wantCause:    sip.ErrInvalidMessage,
		},
		{
			name:         "exhausted max-forwards ACK silently consumed",
			method:       sip.RequestMethodAck,
			uri:          validationURI,
			hdrs:         []sip.Header{header.MaxForwards(0)},
			wantConsumed: true,
			wantCause:    errTooManyHops,
		},
		{
			name:   "valid ACK accepted",
			method: sip.RequestMethodAck,
			uri:    validationURI,
		},
		{
			name:       "exhausted max-forwards CANCEL rejected not challenged",
			method:     sip.RequestMethodCancel,
			uri:        validationURI,
			hdrs:       []sip.Header{header.MaxForwards(0)},
			wantStatus: sip.ResponseStatusTooManyHops,
			wantCause:  errTooManyHops,
		},
		{
			name:   "valid CANCEL accepted",
			method: sip.RequestMethodCancel,
			uri:    validationURI,
		},
		{
			name:           "exhausted max-forwards OPTIONS answered locally",
			method:         sip.RequestMethodOptions,
			uri:            validationURI,
			hdrs:           []sip.Header{header.MaxForwards(0)},
			wantStatus:     sip.ResponseStatusOK,
			wantCause:      errTooManyHops,
			wantAnswerHdrs: []sip.HeaderName{"Supported", "Accept"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := newValidationReq(tt.method, tt.uri, tt.hdrs...)
			err := prx.validateReq(ctx, elm, route, req)
			if tt.wantStatus == 0 && !tt.wantConsumed {
				if err != nil {
					t.Fatalf("prx.validateReq() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("prx.validateReq() error = nil, want failure (status %d)", tt.wantStatus)
			}
			if tt.wantCause != nil && !errors.Is(err, tt.wantCause) {
				t.Fatalf("prx.validateReq() error = %v, want cause %v", err, tt.wantCause)
			}
			if tt.wantConsumed {
				if !errors.Is(err, errReqConsumed) {
					t.Fatalf("prx.validateReq() error = %v, want %v", err, errReqConsumed)
				}
				var rej *sip.RequestRejectedError
				if errors.As(err, &rej) {
					t.Fatalf("prx.validateReq() error = %v carries a SIP response, want none", err)
				}
				return
			}

			rej, ok := errors.AsType[*sip.RequestRejectedError](err)
			if !ok {
				t.Fatalf("prx.validateReq() error = %v, want *sip.RequestRejectedError", err)
			}
			if got, want := rej.ResponseStatus(), tt.wantStatus; got != want {
				t.Fatalf("status = %v, want %v", got, want)
			}

			if tt.wantUnsupported == nil && len(tt.wantAnswerHdrs) == 0 {
				return
			}

			res, resErr := req.NewResponse(rej.ResponseStatus(), rej.RespondOptions().ResponseOptions)
			if resErr != nil {
				t.Fatalf("req.NewResponse() error = %v, want nil", resErr)
			}

			if tt.wantUnsupported != nil {
				wantUnsupported := []sip.Header{tt.wantUnsupported}
				var gotUnsupported []sip.Header
				res.WithMessage(func(msg *sip.Response) {
					gotUnsupported = msg.Headers.Clone().Get("Unsupported")
				})
				if diff := cmp.Diff(wantUnsupported, gotUnsupported); diff != "" {
					t.Fatalf("Unsupported (-want +got):\n%s", diff)
				}
			}

			for _, name := range tt.wantAnswerHdrs {
				var has bool
				res.WithMessage(func(msg *sip.Response) {
					has = msg.Headers.Has(name)
				})
				if !has {
					t.Fatalf("local answer misses %q header", name)
				}
			}
		})
	}

	t.Run("served domain is not self identity", func(t *testing.T) {
		t.Parallel()

		served := sip.MakeHostAddr("alice.example.com")
		identity := sip.MakeHostAddr("pxy.example.net")

		if prx.IsSelfAddr(served, 0) {
			t.Error("prx.IsSelfAddr(served domain) = true, want false")
		}
		if !prx.IsServedAddr(served) {
			t.Error("prx.IsServedAddr(served domain) = false, want true")
		}
		if !prx.IsResponsibleAddr(served) {
			t.Error("prx.IsResponsibleAddr(served domain) = false, want true")
		}

		if !prx.IsSelfAddr(identity, 0) {
			t.Error("prx.IsSelfAddr(local identity) = false, want true")
		}
		if prx.IsServedAddr(identity) {
			t.Error("prx.IsServedAddr(local identity) = true, want false")
		}
		if !prx.IsResponsibleAddr(identity) {
			t.Error("prx.IsResponsibleAddr(local identity) = false, want true")
		}
	})
}

// reqRouteHops returns an owned snapshot of the envelope's Route header field
// values in wire order.
func reqRouteHops(env *sip.RequestEnvelope) []header.RouteHop {
	var hops []header.RouteHop
	env.WithMessage(func(r *sip.Request) {
		for hop := range r.Headers.Routes() {
			hops = append(hops, hop.Clone())
		}
	})
	return hops
}

func TestProxy_PreprocessRouting(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	elm, prx := newValidationProxy(t)
	servedURI := func() *sip.URI {
		return &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.example.com")}
	}
	selfHop := func() header.RouteHop {
		return header.RouteHop{
			URI: &sip.URI{Addr: sip.MakeHostAddr("pxy.example.net"), Params: make(sip.Values).Set("lr", "")},
		}
	}
	edgeHop := func() header.RouteHop {
		return header.RouteHop{
			URI: &sip.URI{Addr: sip.MakeHostAddr("edge.example.com"), Params: make(sip.Values).Set("lr", "")},
		}
	}

	t.Run("rewrite", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			uri        *sip.URI
			hdrs       []sip.Header
			transport  sip.TransportMetadata
			localAddr  netip.AddrPort
			wantURI    *sip.URI
			wantRoutes []header.RouteHop
		}{
			{
				name: "strict router restores last route URI",
				uri:  &sip.URI{Addr: sip.MakeHostAddr("pxy.example.net")},
				hdrs: []sip.Header{header.Route{
					edgeHop(),
					{URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.example.com")}},
				}},
				wantURI:    servedURI(),
				wantRoutes: []header.RouteHop{edgeHop()},
			},
			{
				name: "local maddr stripped when received as indicated",
				uri: &sip.URI{
					User:   sip.MakeUserInfo("alice"),
					Addr:   sip.MakeHostPortAddr("alice.example.com", 5090),
					Params: make(sip.Values).Set("maddr", "alice.example.com"),
				},
				transport: sip.UDPMetadata(),
				localAddr: netip.MustParseAddrPort("192.0.2.7:5090"),
				wantURI:   servedURI(),
			},
			{
				name: "foreign maddr preserved",
				uri: &sip.URI{
					User:   sip.MakeUserInfo("alice"),
					Addr:   sip.MakeHostAddr("alice.example.com"),
					Params: make(sip.Values).Set("maddr", "bob.example.net"),
				},
				transport: sip.UDPMetadata(),
				localAddr: netip.MustParseAddrPort("192.0.2.7:5060"),
				wantURI: &sip.URI{
					User:   sip.MakeUserInfo("alice"),
					Addr:   sip.MakeHostAddr("alice.example.com"),
					Params: make(sip.Values).Set("maddr", "bob.example.net"),
				},
			},
			{
				name: "maddr port mismatch preserved",
				uri: &sip.URI{
					User:   sip.MakeUserInfo("alice"),
					Addr:   sip.MakeHostPortAddr("alice.example.com", 5090),
					Params: make(sip.Values).Set("maddr", "alice.example.com"),
				},
				transport: sip.UDPMetadata(),
				localAddr: netip.MustParseAddrPort("192.0.2.7:5060"),
				wantURI: &sip.URI{
					User:   sip.MakeUserInfo("alice"),
					Addr:   sip.MakeHostPortAddr("alice.example.com", 5090),
					Params: make(sip.Values).Set("maddr", "alice.example.com"),
				},
			},
			{
				name:       "local topmost route removed",
				uri:        servedURI(),
				hdrs:       []sip.Header{header.Route{selfHop(), edgeHop()}},
				wantURI:    servedURI(),
				wantRoutes: []header.RouteHop{edgeHop()},
			},
			{
				name:    "only local route removed",
				uri:     servedURI(),
				hdrs:    []sip.Header{header.Route{selfHop()}},
				wantURI: servedURI(),
			},
			{
				name: "non-local routes preserved",
				uri:  servedURI(),
				hdrs: []sip.Header{header.Route{
					edgeHop(),
					{URI: &sip.URI{Addr: sip.MakeHostAddr("core.example.com"), Params: make(sip.Values).Set("lr", "")}},
				}},
				wantURI: servedURI(),
				wantRoutes: []header.RouteHop{
					edgeHop(),
					{URI: &sip.URI{Addr: sip.MakeHostAddr("core.example.com"), Params: make(sip.Values).Set("lr", "")}},
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				req := newValidationReq(sip.RequestMethodInvite, tt.uri, tt.hdrs...).
					SetTransport(tt.transport).
					SetLocalAddr(tt.localAddr)
				inURI := req.URI()
				inRoutes := reqRouteHops(req)

				out, err := prx.preprocessRouting(ctx, elm, &Route{Name: "test"}, req)
				if err != nil {
					t.Fatalf("prx.preprocessRouting() error = %v, want nil", err)
				}
				if out == req {
					t.Fatal("prx.preprocessRouting() returned the inbound envelope, want an owned copy")
				}

				gotRoutes := reqRouteHops(out)
				if diff := cmp.Diff(tt.wantRoutes, gotRoutes); diff != "" {
					t.Fatalf("routes (-want +got): %s", diff)
				}
				if diff := cmp.Diff(tt.wantURI, out.URI()); diff != "" {
					t.Fatalf("request URI (-want +got): %s", diff)
				}

				if diff := cmp.Diff(inURI, req.URI()); diff != "" {
					t.Fatalf("inbound request URI mutated (-before +after): %s", diff)
				}
				if diff := cmp.Diff(inRoutes, reqRouteHops(req)); diff != "" {
					t.Fatalf("inbound routes mutated (-before +after): %s", diff)
				}
			})
		}
	})

	t.Run("hook observes local route before removal", func(t *testing.T) {
		t.Parallel()

		var seenURI sip.AnyURI
		var seenRoutes []header.RouteHop
		route := &Route{
			Name: "hook",
			PreprocessRoute: func(_ context.Context, req *sip.RequestEnvelope) error {
				seenURI = req.URI()
				seenRoutes = reqRouteHops(req)
				return nil
			},
		}
		selfHopExt := header.RouteHop{
			URI:    &sip.URI{Addr: sip.MakeHostAddr("pxy.example.net"), Params: make(sip.Values).Set("lr", "")},
			Params: make(sip.Values).Set("x-trace", "abc123"),
		}
		req := newValidationReq(sip.RequestMethodInvite, servedURI(), header.Route{selfHopExt, edgeHop()})

		out, err := prx.preprocessRouting(ctx, elm, route, req)
		if err != nil {
			t.Fatalf("prx.preprocessRouting() error = %v, want nil", err)
		}

		if got, want := len(seenRoutes), 2; got != want {
			t.Fatalf("hook observed %d Route values, want %d", got, want)
		}
		if !seenRoutes[0].Equal(selfHopExt) {
			t.Fatalf("hook observed first Route = %v, want local hop %v", seenRoutes[0], selfHopExt)
		}
		if got := seenRoutes[0].Params; !got.Has("x-trace") {
			t.Fatalf("hook observed first Route params = %v, want x-trace extension", got)
		}
		if diff := cmp.Diff(servedURI(), seenURI); diff != "" {
			t.Fatalf("hook observed request URI (-want +got): %s", diff)
		}

		if diff := cmp.Diff([]header.RouteHop{edgeHop()}, reqRouteHops(out)); diff != "" {
			t.Fatalf("routes (-want +got): %s", diff)
		}
		if diff := cmp.Diff(servedURI(), out.URI()); diff != "" {
			t.Fatalf("request URI (-want +got): %s", diff)
		}
	})

	t.Run("hook rewrite revalidated", func(t *testing.T) {
		t.Parallel()

		route := &Route{
			Name: "hook",
			PreprocessRoute: func(_ context.Context, req *sip.RequestEnvelope) error {
				req.WithMessage(func(r *sip.Request) {
					r.URI = &uri.Tel{Number: "+12025550100"}
				})
				return nil
			},
		}
		req := newValidationReq(sip.RequestMethodInvite, servedURI())

		_, err := prx.preprocessRouting(ctx, elm, route, req)
		if err == nil {
			t.Fatal("prx.preprocessRouting() error = nil, want rejection")
		}
		if !errors.Is(err, errUnsupURIScheme) {
			t.Fatalf("prx.preprocessRouting() error = %v, want %v", err, errUnsupURIScheme)
		}

		rej, ok := errors.AsType[*sip.RequestRejectedError](err)
		if !ok {
			t.Fatalf("prx.preprocessRouting() error = %v, want *sip.RequestRejectedError", err)
		}
		if got, want := rej.ResponseStatus(), sip.ResponseStatusUnsupportedURIScheme; got != want {
			t.Fatalf("rejection status = %v, want %v", got, want)
		}
	})

	t.Run("hook error propagates", func(t *testing.T) {
		t.Parallel()

		sentErr := errors.ErrorWrap("route policy failed")
		route := &Route{
			Name:            "hook",
			PreprocessRoute: func(context.Context, *sip.RequestEnvelope) error { return sentErr },
		}
		req := newValidationReq(sip.RequestMethodInvite, servedURI())

		_, err := prx.preprocessRouting(ctx, elm, route, req)
		if !errors.Is(err, sentErr) {
			t.Fatalf("prx.preprocessRouting() error = %v, want %v", err, sentErr)
		}

		var rej *sip.RequestRejectedError
		if errors.As(err, &rej) {
			t.Fatalf("prx.preprocessRouting() error = %v, hook error became a SIP rejection", err)
		}
	})

	t.Run("empty location plan rejects no-contact", func(t *testing.T) {
		t.Parallel()

		route := &Route{
			Name: "loc",
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
				return TargetPlan{}, nil
			},
		}
		req := newValidationReq(sip.RequestMethodInvite, servedURI())

		out, err := prx.preprocessRouting(ctx, elm, route, req)
		if err != nil {
			t.Fatalf("prx.preprocessRouting() error = %v, want nil", err)
		}

		_, err = prx.resolveTargets(ctx, route, out)
		if err == nil {
			t.Fatal("prx.resolveTargets() error = nil, want no-contact rejection")
		}
		if !errors.Is(err, errNoFwdTargets) {
			t.Fatalf("prx.resolveTargets() error = %v, want %v", err, errNoFwdTargets)
		}

		rej, ok := errors.AsType[*sip.RequestRejectedError](err)
		if !ok {
			t.Fatalf("prx.resolveTargets() error = %v, want *sip.RequestRejectedError", err)
		}
		if got, want := rej.ResponseStatus(), sip.ResponseStatusTemporarilyUnavailable; got != want {
			t.Fatalf("rejection status = %v, want %v", got, want)
		}
	})

	t.Run("predetermined foreign target bypasses lookup", func(t *testing.T) {
		t.Parallel()

		var resolved bool
		route := &Route{
			Name: "loc",
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (TargetPlan, error) {
				resolved = true
				return TargetPlan{}, nil
			},
		}
		req := newValidationReq(
			sip.RequestMethodInvite,
			&sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("bob.example.net")},
		)

		out, err := prx.preprocessRouting(ctx, elm, route, req)
		if err != nil {
			t.Fatalf("prx.preprocessRouting() error = %v, want nil", err)
		}

		plan, err := prx.resolveTargets(ctx, route, out)
		if err != nil {
			t.Fatalf("prx.resolveTargets() error = %v, want nil", err)
		}
		if resolved {
			t.Fatal("route.ResolveRequestTargets called for a predetermined non-local target")
		}
		if got, want := len(plan.Groups), 1; got != want {
			t.Fatalf("len(plan.Groups) = %d, want %d", got, want)
		}
		if got, want := len(plan.Groups[0]), 1; got != want {
			t.Fatalf("len(plan.Groups[0]) = %d, want %d", got, want)
		}
		if got, want := plan.Groups[0][0].URI, out.URI(); !got.Equal(want) {
			t.Fatalf("target URI = %v, want Request-URI %v", got, want)
		}
	})
}
