package redirector_test

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/internal/testutil/siptest"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/redirector"
)

// wireReadTimeout bounds one wire read. It is generous rather than tight
// so that a response delayed by scheduling under -race and parallel
// package binaries still arrives inside the deadline.
const wireReadTimeout = 4 * time.Second

// wireRedirector is one loopback redirector side of the wire harness: an
// element with a single real UDP transport listening on a kernel-assigned
// loopback port.
type wireRedirector struct {
	elm  *sip.Element
	rdr  *redirector.Redirector
	addr netip.AddrPort
}

// newWireRedirector creates the loopback redirector side of the wire
// harness: a real UDP transport bound to a kernel-assigned loopback port,
// served by a detached goroutine, with the redirector bound to the element.
// It is modeled on newWireProxy in sip/proxy/integration_test.go.
func newWireRedirector(tb testing.TB, routes []redirector.Route) *wireRedirector {
	tb.Helper()

	elm, addr := siptest.NewLoopbackUDPElement(tb, sip.UDPMetadata())

	rdr, err := redirector.New(routes)
	if err != nil {
		tb.Fatalf("redirector.New() error = %v, want nil", err)
	}
	elm.UseComponent(rdr)

	return &wireRedirector{elm: elm, rdr: rdr, addr: addr}
}

// sendReq renders req and writes it onto the wire toward addr.
func sendReq(tb testing.TB, pc net.PacketConn, addr netip.AddrPort, req *sip.RequestEnvelope) {
	tb.Helper()

	wire := []byte(shared.Render(req.Message()))
	if _, err := pc.WriteTo(wire, net.UDPAddrFromAddrPort(addr)); err != nil {
		tb.Fatalf("pc.WriteTo() error = %v, want nil", err)
	}
}

// readRes reads the next wire datagram within wireReadTimeout and parses
// it as a SIP response envelope.
func readRes(tb testing.TB, pc net.PacketConn) *sip.ResponseEnvelope {
	tb.Helper()

	res, _ := readResFrom(tb, pc)
	return res
}

// readResFrom reads the next wire datagram within wireReadTimeout, parses
// it as a SIP response envelope, and reports the address it arrived from.
func readResFrom(tb testing.TB, pc net.PacketConn) (*sip.ResponseEnvelope, netip.AddrPort) {
	tb.Helper()

	buf := make([]byte, 64*1024)
	if err := pc.SetReadDeadline(time.Now().Add(wireReadTimeout)); err != nil {
		tb.Fatalf("pc.SetReadDeadline() error = %v, want nil", err)
	}
	n, src, err := pc.ReadFrom(buf)
	if err != nil {
		tb.Fatalf("pc.ReadFrom() error = %v, want one wire response", err)
	}
	srcAddr, ok := src.(*net.UDPAddr)
	if !ok {
		tb.Fatalf("wire response source = %T, want *net.UDPAddr", src)
	}
	msg, err := sip.DefaultParser().ParsePacket(buf[:n])
	if err != nil {
		tb.Fatalf("sip.DefaultParser().ParsePacket() error = %v, want nil", err)
	}
	res, ok := msg.(*sip.Response)
	if !ok {
		tb.Fatalf("wire message = %T, want *sip.Response", msg)
	}
	return sip.NewResponseEnvelope(res), srcAddr.AddrPort()
}

// readResFor reads wire datagrams until one parses as the SIP response for
// req — correlated by the top Via branch and CSeq the response echoes
// back — and returns it. Datagrams not answering req, such as
// transaction-layer retransmissions of an earlier exchange, are
// discarded; the CSeq halves matter when req shares the Via of the
// request it relates to, as a CANCEL keeps the INVITE's top Via — branch
// included — per RFC 3261 Section 9.1. Each read keeps the
// wireReadTimeout deadline of [readRes].
func readResFor(tb testing.TB, pc net.PacketConn, req *sip.RequestEnvelope) *sip.ResponseEnvelope {
	tb.Helper()

	reqVia, ok := req.Headers().FirstVia()
	if !ok {
		tb.Fatal("request headers missing Via")
	}
	reqBranch, _ := reqVia.Branch()
	reqCSeq, ok := req.Headers().CSeq()
	if !ok {
		tb.Fatal("request headers missing CSeq")
	}
	for {
		res := readRes(tb, pc)
		resVia, ok := res.Headers().FirstVia()
		if !ok {
			tb.Fatal("response headers missing Via")
		}
		resCSeq, ok := res.Headers().CSeq()
		if !ok {
			tb.Fatal("response headers missing CSeq")
		}
		if resBranch, _ := resVia.Branch(); resBranch == reqBranch && resCSeq.Equal(*reqCSeq) {
			return res
		}
	}
}

func TestRedirector_Claim_InvitesOnWire(t *testing.T) {
	t.Parallel()

	var resolveCalls atomic.Int32
	harness := newWireRedirector(t, []redirector.Route{{
		Name: "all",
		ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
			resolveCalls.Add(1)
			return testContacts, nil
		},
	}})

	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v, want nil", err)
	}
	t.Cleanup(func() { peer.Close() })
	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	req := siptest.NewInboundInvite(t, "UDP", "", harness.addr, peerAddr)
	sendReq(t, peer, harness.addr, req)

	res := readRes(t, peer)
	if got := res.Status(); got != sip.ResponseStatusMovedTemporarily {
		t.Fatalf("response status = %v, want %v", got, sip.ResponseStatusMovedTemporarily)
	}
	if got := resolveCalls.Load(); got != 1 {
		t.Fatalf("resolver calls = %d, want 1", got)
	}
}

// resContacts collects the Contact addresses carried by res in wire order.
func resContacts(res *sip.ResponseEnvelope) header.Contact {
	var contacts header.Contact
	for addr := range res.Headers().Contacts() {
		contacts = append(contacts, *addr)
	}
	return contacts
}

// paramContacts is a fixed contact set with header parameters, used to
// verify that contact values and their resolved order survive the wire.
var paramContacts = header.Contact{
	{
		URI: &sip.URI{
			User: sip.MakeUserInfo("uas1"),
			Addr: sip.MakeHostAddr("uas1.test"),
		},
		Params: sip.Values{"q": {"0.9"}, "expires": {"3600"}},
	},
	{
		URI: &sip.URI{
			User: sip.MakeUserInfo("uas2"),
			Addr: sip.MakeHostAddr("uas2.test"),
		},
		Params: sip.Values{"q": {"0.1"}},
	},
}

func TestRedirector_RedirectResponse(t *testing.T) {
	t.Parallel()

	// exchange describes one request/response round on the wire: user is
	// the Request-URI username the resolver may key on, wantContacts nil
	// expects a response without a Contact header.
	type exchange struct {
		user         string
		wantStatus   sip.ResponseStatus
		wantContacts header.Contact
	}

	resolveParamContacts := func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
		return paramContacts, nil
	}
	resolveTestContacts := func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
		return testContacts, nil
	}

	tests := []struct {
		name      string
		route     redirector.Route
		exchanges []exchange
	}{
		{
			name: "contacts with parameters",
			route: redirector.Route{
				Name:            "paramed",
				ResolveContacts: resolveParamContacts,
			},
			exchanges: []exchange{{
				wantStatus:   sip.ResponseStatusMovedTemporarily,
				wantContacts: paramContacts,
			}},
		},
		{
			name: "default status",
			route: redirector.Route{
				Name:            "defaulted",
				ResolveContacts: resolveTestContacts,
			},
			exchanges: []exchange{{
				wantStatus:   sip.ResponseStatusMovedTemporarily,
				wantContacts: testContacts,
			}},
		},
		{
			name: "constant status",
			route: redirector.Route{
				Name:            "permanent",
				ResolveContacts: resolveTestContacts,
				Status:          sip.ResponseStatusMovedPermanently,
			},
			exchanges: []exchange{{
				wantStatus:   sip.ResponseStatusMovedPermanently,
				wantContacts: testContacts,
			}},
		},
		{
			name: "per-request selection",
			route: redirector.Route{
				Name: "selective",
				// Resolve one contact for the default user and the
				// parameterized pair for carol; the selector turns a
				// multi-contact resolution into 300 Multiple Choices.
				ResolveContacts: func(_ context.Context, req *sip.RequestEnvelope) (header.Contact, error) {
					if u, ok := req.URI().(*sip.URI); ok && u.User.Username() == "carol" {
						return paramContacts, nil
					}
					return testContacts, nil
				},
				SelectStatus: func(_ context.Context, _ *sip.RequestEnvelope, contacts header.Contact) sip.ResponseStatus {
					if len(contacts) > 1 {
						return sip.ResponseStatusMultipleChoices
					}
					return sip.ResponseStatusMovedTemporarily
				},
			},
			exchanges: []exchange{
				{
					user:         "alice",
					wantStatus:   sip.ResponseStatusMovedTemporarily,
					wantContacts: testContacts,
				},
				{
					user:         "carol",
					wantStatus:   sip.ResponseStatusMultipleChoices,
					wantContacts: paramContacts,
				},
			},
		},
		{
			name: "unsupported selected status",
			route: redirector.Route{
				Name:            "misconfigured",
				ResolveContacts: resolveTestContacts,
				SelectStatus: func(context.Context, *sip.RequestEnvelope, header.Contact) sip.ResponseStatus {
					return sip.ResponseStatusTemporarilyUnavailable
				},
			},
			exchanges: []exchange{{
				wantStatus: sip.ResponseStatusServerInternalError,
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			harness := newWireRedirector(t, []redirector.Route{tt.route})

			peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
			if err != nil {
				t.Fatalf("net.ListenUDP() error = %v, want nil", err)
			}
			t.Cleanup(func() { peer.Close() })
			peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

			for i, ex := range tt.exchanges {
				// Each exchange needs its own server transaction: a
				// distinct Via branch keeps a repeated request from being
				// absorbed as a retransmission.
				req := siptest.NewInboundInvite(
					t,
					"UDP",
					fmt.Sprintf("%s.redirect-%d", sip.MagicCookie, i),
					harness.addr,
					peerAddr,
				)
				if ex.user != "" {
					req.WithMessage(func(m *sip.Request) {
						if u, ok := m.URI.(*sip.URI); ok {
							u.User = sip.MakeUserInfo(ex.user)
						}
					})
				}
				sendReq(t, peer, harness.addr, req)

				res := readResFor(t, peer, req)
				if got := res.Status(); got != ex.wantStatus {
					t.Fatalf("exchange %d response status = %v, want %v", i, got, ex.wantStatus)
				}

				if ex.wantContacts == nil {
					if res.Headers().Has("Contact") {
						t.Errorf("exchange %d response headers contain Contact, want none", i)
					}
					continue
				}
				if diff := cmp.Diff(ex.wantContacts, resContacts(res)); diff != "" {
					t.Errorf("exchange %d response contacts mismatch (-want +got):\n%s", i, diff)
				}
			}
		})
	}
}

// TestRedirector_AuthorizationSuccess verifies that a route's non-nil
// authorizer runs before contact resolution and that a nil result lets
// the exchange continue to the ordinary redirect response.
func TestRedirector_AuthorizationSuccess(t *testing.T) {
	t.Parallel()

	var (
		authorizeCalls          atomic.Int32
		resolveCalls            atomic.Int32
		authorizedBeforeResolve atomic.Bool
	)
	harness := newWireRedirector(t, []redirector.Route{{
		Name: "authorized",
		AuthorizeRequest: func(context.Context, *sip.RequestEnvelope) error {
			authorizeCalls.Add(1)
			return nil
		},
		ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
			authorizedBeforeResolve.Store(authorizeCalls.Load() == 1)
			resolveCalls.Add(1)
			return testContacts, nil
		},
	}})

	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v, want nil", err)
	}
	t.Cleanup(func() { peer.Close() })
	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	req := siptest.NewInboundInvite(t, "UDP", "", harness.addr, peerAddr)
	sendReq(t, peer, harness.addr, req)

	res := readRes(t, peer)
	if got := res.Status(); got != sip.ResponseStatusMovedTemporarily {
		t.Fatalf("response status = %v, want %v", got, sip.ResponseStatusMovedTemporarily)
	}
	if diff := cmp.Diff(testContacts, resContacts(res)); diff != "" {
		t.Errorf("response contacts mismatch (-want +got):\n%s", diff)
	}
	if got := authorizeCalls.Load(); got != 1 {
		t.Errorf("authorizer calls = %d, want 1", got)
	}
	if got := resolveCalls.Load(); got != 1 {
		t.Errorf("resolver calls = %d, want 1", got)
	}
	if !authorizedBeforeResolve.Load() {
		t.Error("resolver ran before authorization, want authorization first")
	}
}

func TestRedirector_PolicyFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		route       func(resolveCalls *atomic.Int32) redirector.Route
		wantStatus  sip.ResponseStatus
		wantResolve int32
	}{
		{
			name: "authorize failure",
			route: func(resolveCalls *atomic.Int32) redirector.Route {
				return redirector.Route{
					Name: "denied",
					AuthorizeRequest: func(context.Context, *sip.RequestEnvelope) error {
						return errors.Errorf("policy denied")
					},
					ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
						resolveCalls.Add(1)
						return testContacts, nil
					},
				}
			},
			wantStatus: sip.ResponseStatusForbidden,
		},
		{
			name: "resolver error",
			route: func(resolveCalls *atomic.Int32) redirector.Route {
				return redirector.Route{
					Name: "failing",
					ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
						resolveCalls.Add(1)
						return nil, errors.Errorf("directory unavailable")
					},
				}
			},
			wantStatus:  sip.ResponseStatusServerInternalError,
			wantResolve: 1,
		},
		{
			name: "empty contacts",
			route: func(resolveCalls *atomic.Int32) redirector.Route {
				return redirector.Route{
					Name: "empty",
					ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
						resolveCalls.Add(1)
						return header.Contact{}, nil
					},
				}
			},
			wantStatus:  sip.ResponseStatusNotFound,
			wantResolve: 1,
		},
		{
			name: "nil contacts",
			route: func(resolveCalls *atomic.Int32) redirector.Route {
				return redirector.Route{
					Name: "missing",
					ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
						resolveCalls.Add(1)
						return nil, nil
					},
				}
			},
			wantStatus:  sip.ResponseStatusNotFound,
			wantResolve: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var resolveCalls atomic.Int32
			harness := newWireRedirector(t, []redirector.Route{tt.route(&resolveCalls)})

			peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
			if err != nil {
				t.Fatalf("net.ListenUDP() error = %v, want nil", err)
			}
			t.Cleanup(func() { peer.Close() })
			peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

			req := siptest.NewInboundInvite(t, "UDP", "", harness.addr, peerAddr)
			sendReq(t, peer, harness.addr, req)

			res := readRes(t, peer)
			if got := res.Status(); got != tt.wantStatus {
				t.Fatalf("response status = %v, want %v", got, tt.wantStatus)
			}
			if res.Headers().Has("Contact") {
				t.Errorf("response headers contain Contact, want none")
			}
			if got := resolveCalls.Load(); got != tt.wantResolve {
				t.Errorf("resolver calls = %d, want %d", got, tt.wantResolve)
			}
		})
	}
}

// TestRedirector_StatefulResponse pins the element-provided stateful
// response machinery through the redirector's response path: an INVITE
// asking for RFC 3581 symmetric routing is answered on the peer's own
// socket, and the response carries the reflected received/rport Via
// values, a generated To tag, and the request's Call-ID/CSeq identity.
func TestRedirector_StatefulResponse(t *testing.T) {
	t.Parallel()

	harness := newWireRedirector(t, []redirector.Route{{
		Name: "all",
		ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
			return testContacts, nil
		},
	}})

	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("net.ListenUDP() error = %v, want nil", err)
	}
	t.Cleanup(func() { peer.Close() })
	peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

	req := siptest.NewInboundInvite(t, "UDP", "", harness.addr, peerAddr)
	// The Via asks for rport reflection (RFC 3581) and advertises a
	// sent-by that is neither the packet source address nor port, so the
	// receiver fills received/rport per RFC 3261 Section 18.2.1 — and a
	// response routed by sent-by alone would never reach the peer socket.
	req.WithMessage(func(m *sip.Request) {
		via, ok := m.Headers.FirstVia()
		if !ok {
			t.Error("request headers missing Via")
			return
		}
		via.Addr = sip.MakeHostPortAddr("192.0.2.10", 5060)
		via.Params.Set("rport", "")
	})
	reqHdrs := req.Headers()
	reqCallID, _ := reqHdrs.CallID()
	reqCSeq, _ := reqHdrs.CSeq()
	sendReq(t, peer, harness.addr, req)

	// The datagram arriving on the peer socket is itself the
	// symmetric-routing assertion: sent-by alone points at 192.0.2.10.
	res := readRes(t, peer)

	if got := res.Status(); got != sip.ResponseStatusMovedTemporarily {
		t.Fatalf("response status = %v, want %v", got, sip.ResponseStatusMovedTemporarily)
	}

	resHdrs := res.Headers()

	to, ok := resHdrs.To()
	if !ok {
		t.Fatal("response headers missing To")
	}
	if tag, ok := to.Tag(); !ok || tag == "" {
		t.Errorf("response To tag = %q, %t, want a non-empty tag", tag, ok)
	}

	via, ok := resHdrs.FirstVia()
	if !ok {
		t.Fatal("response headers missing Via")
	}
	if got, ok := via.Received(); !ok || got != netip.MustParseAddr("127.0.0.1") {
		t.Errorf("response Via received = %v, %t, want 127.0.0.1", got, ok)
	}
	if got, ok := via.RPort(); !ok || got != peerAddr.Port() {
		t.Errorf("response Via rport = %d, %t, want %d", got, ok, peerAddr.Port())
	}

	if got, ok := resHdrs.CallID(); !ok || got != reqCallID {
		t.Errorf("response Call-ID = %q, %t, want %q", got, ok, reqCallID)
	}
	if got, ok := resHdrs.CSeq(); !ok || !got.Equal(reqCSeq) {
		t.Errorf("response CSeq = %v, %t, want %v", got, ok, reqCSeq)
	}

	if got := shared.Render(res.Message()); !strings.HasPrefix(got, "SIP/2.0 302") {
		t.Errorf("rendered response = %q, want prefix %q", got, "SIP/2.0 302")
	}
}

// newAckFrom clones invite into the hop-by-hop ACK acknowledging res, a
// non-2xx final response: the request and CSeq methods become ACK while
// the Request-URI, Via, From, Call-ID, and CSeq number are kept, and To
// is replaced by the response's tag-carrying To per RFC 3261 Section
// 17.1.1.3. Sending it confirms the INVITE server transaction so it stops
// retransmitting the final response into later reads.
func newAckFrom(tb testing.TB, invite *sip.RequestEnvelope, res *sip.ResponseEnvelope) *sip.RequestEnvelope {
	tb.Helper()

	ack, ok := invite.Clone().(*sip.RequestEnvelope)
	if !ok {
		tb.Fatalf("invite.Clone() = %T, want *sip.RequestEnvelope", invite.Clone())
	}
	to, ok := res.Headers().To()
	if !ok {
		tb.Fatal("response headers missing To")
	}
	ack.WithMessage(func(m *sip.Request) {
		m.Method = sip.RequestMethodAck
		m.Headers.Set(to)

		cseq, ok := m.Headers.CSeq()
		if !ok {
			tb.Error("request headers missing CSeq")
			return
		}
		cseq.Method = sip.RequestMethodAck
	})
	return ack
}

// newCancelFrom clones invite into its RFC 3261 Section 9.1 CANCEL: the
// request and CSeq methods become CANCEL, while the Request-URI, To,
// From, Call-ID, CSeq number, and the single Via — branch included — are
// kept, since Section 9.1 requires the CANCEL to carry the top Via of
// the request it cancels.
func newCancelFrom(tb testing.TB, invite *sip.RequestEnvelope) *sip.RequestEnvelope {
	tb.Helper()

	cnc, ok := invite.Clone().(*sip.RequestEnvelope)
	if !ok {
		tb.Fatalf("invite.Clone() = %T, want *sip.RequestEnvelope", invite.Clone())
	}
	cnc.WithMessage(func(m *sip.Request) {
		m.Method = sip.RequestMethodCancel

		cseq, ok := m.Headers.CSeq()
		if !ok {
			tb.Error("request headers missing CSeq")
			return
		}
		cseq.Method = sip.RequestMethodCancel
	})
	return cnc
}

func TestRedirector_Cancel(t *testing.T) {
	t.Parallel()

	// Each case runs one INVITE/CANCEL exchange on the wire harness: the
	// INVITE is always claimed and answered 302, and the CANCEL built from
	// it by newCancelFrom expects wantStatus. retransmit resends the
	// identical CANCEL datagram, which the CANCEL's own server transaction
	// must absorb and answer from its cached response without new route
	// work — observable through the matcher call count.
	tests := []struct {
		name        string
		matchCancel bool
		retransmit  bool
		wantStatus  sip.ResponseStatus
		wantMatch   int32
		wantResolve int32
	}{
		{
			name:        "matched cancel",
			matchCancel: true,
			wantStatus:  sip.ResponseStatusCallTransactionDoesNotExist,
			wantMatch:   2,
			wantResolve: 1,
		},
		{
			name:        "cancel retransmission",
			matchCancel: true,
			retransmit:  true,
			wantStatus:  sip.ResponseStatusCallTransactionDoesNotExist,
			wantMatch:   2,
			wantResolve: 1,
		},
		{
			name: "unmatched cancel passes through",
			// A CANCEL no route matches falls to the end of the receiver
			// chain, answered by the transport-level 503 rather than a
			// redirector response.
			wantStatus:  sip.ResponseStatusServiceUnavailable,
			wantMatch:   2,
			wantResolve: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				matchCalls   atomic.Int32
				resolveCalls atomic.Int32
			)
			harness := newWireRedirector(t, []redirector.Route{{
				Name: "cancel",
				MatchRequest: func(_ context.Context, req *sip.RequestEnvelope) bool {
					matchCalls.Add(1)
					return tt.matchCancel || !req.Method().Equal(sip.RequestMethodCancel)
				},
				ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
					resolveCalls.Add(1)
					return testContacts, nil
				},
			}})

			peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
			if err != nil {
				t.Fatalf("net.ListenUDP() error = %v, want nil", err)
			}
			t.Cleanup(func() { peer.Close() })
			peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

			invite := siptest.NewInboundInvite(t, "UDP", "", harness.addr, peerAddr)
			sendReq(t, peer, harness.addr, invite)
			inviteRes := readRes(t, peer)
			if got := inviteRes.Status(); got != sip.ResponseStatusMovedTemporarily {
				t.Fatalf("invite response status = %v, want %v", got, sip.ResponseStatusMovedTemporarily)
			}
			// Acknowledge the non-2xx final response: the INVITE server
			// transaction confirms and stops its response retransmissions,
			// leaving every later datagram to the CANCEL exchange.
			sendReq(t, peer, harness.addr, newAckFrom(t, invite, inviteRes))

			cnc := newCancelFrom(t, invite)
			sendReq(t, peer, harness.addr, cnc)
			res := readResFor(t, peer, cnc)
			if got := res.Status(); got != tt.wantStatus {
				t.Fatalf("cancel response status = %v, want %v", got, tt.wantStatus)
			}

			if tt.retransmit {
				// The identical CANCEL datagram maps to the server
				// transaction the first CANCEL created: the transaction
				// layer re-answers from its cached response and the
				// redirector never sees the retransmission.
				sendReq(t, peer, harness.addr, cnc)
				res = readResFor(t, peer, cnc)
				if got := res.Status(); got != tt.wantStatus {
					t.Fatalf("retransmitted cancel response status = %v, want %v", got, tt.wantStatus)
				}
			}

			if got := matchCalls.Load(); got != tt.wantMatch {
				t.Errorf("matcher calls = %d, want %d", got, tt.wantMatch)
			}
			if got := resolveCalls.Load(); got != tt.wantResolve {
				t.Errorf("resolver calls = %d, want %d", got, tt.wantResolve)
			}
		})
	}
}

// wireSilenceWindow is the observation window for "nothing new was sent"
// assertions on loopback. It stays below the first INVITE server
// transaction response retransmit (timer G fires at sip.T1 = 500ms) so at
// most the resend already in flight when the confirming ACK went out can
// appear inside it.
const wireSilenceWindow = 250 * time.Millisecond

// expectSilentOrResend reads pc for the whole window and reports whether
// the wire stayed silent apart from resends of res — the cached final
// response an INVITE server transaction retransmits on timer G until it
// confirms the ACK, which may already be in flight when the ACK is sent
// (RFC 3261 Section 17.2.1). A request, or a response belonging to any
// other transaction — such as one answering the ACK itself, which would
// echo its CSeq method — means real output escaped onto the wire.
func expectSilentOrResend(
	tb testing.TB,
	pc net.PacketConn,
	res *sip.ResponseEnvelope,
	window time.Duration,
) bool {
	tb.Helper()

	resVia, ok := res.Headers().FirstVia()
	if !ok {
		tb.Fatal("response headers missing Via")
	}
	resBranch, _ := resVia.Branch()
	resCSeq, ok := res.Headers().CSeq()
	if !ok {
		tb.Fatal("response headers missing CSeq")
	}

	buf := make([]byte, 64*1024)
	deadline := time.Now().Add(window)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return true
		}
		if err := pc.SetReadDeadline(time.Now().Add(left)); err != nil {
			tb.Fatalf("pc.SetReadDeadline() error = %v, want nil", err)
		}
		n, _, err := pc.ReadFrom(buf)
		if resetErr := pc.SetReadDeadline(time.Time{}); resetErr != nil {
			tb.Fatalf("pc.SetReadDeadline(zero) error = %v, want nil", resetErr)
		}
		if err != nil {
			if nerr, ok := errors.AsType[net.Error](err); ok && nerr.Timeout() {
				return true
			}
			tb.Fatalf("pc.ReadFrom() error = %v, want one wire message", err)
		}
		msg, perr := sip.DefaultParser().ParsePacket(buf[:n])
		if perr != nil {
			tb.Fatalf("sip.DefaultParser().ParsePacket() error = %v, want nil", perr)
		}
		got, ok := msg.(*sip.Response)
		if !ok {
			tb.Logf("unexpected wire message during silence window: %v", msg)
			return false
		}
		gotEnv := sip.NewResponseEnvelope(got)
		gotVia, ok := gotEnv.Headers().FirstVia()
		if !ok {
			tb.Logf("unexpected wire message during silence window: %v", msg)
			return false
		}
		gotBranch, _ := gotVia.Branch()
		gotCSeq, ok := gotEnv.Headers().CSeq()
		if !ok || gotBranch != resBranch || !gotCSeq.Equal(resCSeq) {
			tb.Logf("unexpected wire message during silence window: %v", msg)
			return false
		}
		// The resend of res is legitimate traffic that predates the ACK
		// confirmation; keep watching until the window ends.
	}
}

// TestRedirector_Retransmission pins the transaction layer's absorption of
// INVITE server transaction traffic through the redirector path: a resent
// request is re-answered from the transaction's cached response, and the
// ACK confirming the non-2xx final is absorbed without any wire output.
// In both cases the redirector's resolver sees the request exactly once.
func TestRedirector_Retransmission(t *testing.T) {
	t.Parallel()

	// Each case opens with the same INVITE exchange: the request is
	// claimed and answered 302 through a server transaction. followUp
	// then drives that transaction's absorbed wire traffic — the
	// identical request datagram or the confirming ACK — whose handling
	// must never reach the resolver.
	tests := []struct {
		name     string
		followUp func(
			tb testing.TB,
			pc net.PacketConn,
			addr netip.AddrPort,
			invite *sip.RequestEnvelope,
			res *sip.ResponseEnvelope,
		)
	}{
		{
			name: "request retransmission",
			followUp: func(
				tb testing.TB,
				pc net.PacketConn,
				addr netip.AddrPort,
				invite *sip.RequestEnvelope,
				res *sip.ResponseEnvelope,
			) {
				tb.Helper()

				// The identical INVITE datagram maps to the server
				// transaction the first request created: the transaction
				// layer re-answers from its cached response and the
				// redirector never sees the retransmission.
				sendReq(tb, pc, addr, invite)
				resent := readResFor(tb, pc, invite)
				if got := resent.Status(); got != sip.ResponseStatusMovedTemporarily {
					tb.Fatalf("retransmitted invite response status = %v, want %v",
						got, sip.ResponseStatusMovedTemporarily)
				}

				// The cached resend belongs to the first transaction:
				// its To tag is the one res generated, while a fresh
				// response would carry a new one.
				resTo, ok := res.Headers().To()
				if !ok {
					tb.Fatal("response headers missing To")
				}
				resentTo, ok := resent.Headers().To()
				if !ok {
					tb.Fatal("retransmitted response headers missing To")
				}
				resTag, _ := resTo.Tag()
				if gotTag, _ := resentTo.Tag(); gotTag != resTag {
					tb.Errorf("retransmitted response To tag = %q, want the first response's %q",
						gotTag, resTag)
				}
			},
		},
		{
			name: "ack absorbed silently",
			followUp: func(
				tb testing.TB,
				pc net.PacketConn,
				addr netip.AddrPort,
				invite *sip.RequestEnvelope,
				res *sip.ResponseEnvelope,
			) {
				tb.Helper()

				// The ACK for the non-2xx final is absorbed by the INVITE
				// server transaction it confirms (RFC 3261 Section
				// 17.2.1): it never reaches the redirector and no
				// datagram may be emitted for it. Only the response
				// resend possibly in flight ahead of the ACK is
				// legitimate wire traffic inside the window.
				sendReq(tb, pc, addr, newAckFrom(tb, invite, res))
				if !expectSilentOrResend(tb, pc, res, wireSilenceWindow) {
					tb.Error("peer observed new traffic for the confirming ACK, want silence")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var resolveCalls atomic.Int32
			harness := newWireRedirector(t, []redirector.Route{{
				Name: "all",
				ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
					resolveCalls.Add(1)
					return testContacts, nil
				},
			}})

			peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
			if err != nil {
				t.Fatalf("net.ListenUDP() error = %v, want nil", err)
			}
			t.Cleanup(func() { peer.Close() })
			peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

			invite := siptest.NewInboundInvite(t, "UDP", "", harness.addr, peerAddr)
			sendReq(t, peer, harness.addr, invite)

			res := readRes(t, peer)
			if got := res.Status(); got != sip.ResponseStatusMovedTemporarily {
				t.Fatalf("invite response status = %v, want %v",
					got, sip.ResponseStatusMovedTemporarily)
			}

			tt.followUp(t, peer, harness.addr, invite, res)

			if got := resolveCalls.Load(); got != 1 {
				t.Errorf("resolver calls = %d, want 1", got)
			}
		})
	}
}

// TestRedirector_MultipleElements pins cross-element isolation for a
// stateless component: one Redirector instance registered on two wire
// harnesses — each with its own element, UDP listener, and peer — answers
// a matching INVITE sent to each listener through the element that
// received it. Each peer reads its own 3xx emitted from its own element's
// listener, the shared resolver runs once per request, and each
// response's top Via sent-by names the receiving element's listener
// address.
func TestRedirector_MultipleElements(t *testing.T) {
	t.Parallel()

	var resolveCalls atomic.Int32
	sharedRdr, err := redirector.New([]redirector.Route{{
		Name: "all",
		ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
			resolveCalls.Add(1)
			return testContacts, nil
		},
	}})
	if err != nil {
		t.Fatalf("redirector.New() error = %v, want nil", err)
	}

	// Each harness builds and registers its own redirector from the given
	// routes; nil leaves it an inert pass-through so the shared instance,
	// registered afterward on each harness element, claims every matching
	// request.
	harnesses := []*wireRedirector{
		newWireRedirector(t, nil),
		newWireRedirector(t, nil),
	}
	for _, harness := range harnesses {
		harness.elm.UseComponent(sharedRdr)
	}

	for i, harness := range harnesses {
		peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
		if err != nil {
			t.Fatalf("net.ListenUDP() error = %v, want nil", err)
		}
		t.Cleanup(func() { peer.Close() })
		peerAddr := netip.MustParseAddrPort(peer.LocalAddr().String())

		req := siptest.NewInboundInvite(t,
			"UDP",
			fmt.Sprintf("%s.multi-%d", sip.MagicCookie, i),
			harness.addr,
			peerAddr,
		)
		// The response echoes the request's top Via verbatim, so seeding
		// its sent-by with the receiving element's listener address makes
		// the wire response name the element that answered it. The
		// RFC 3581 rport request keeps response routing pointed back at
		// the peer socket instead of that sent-by.
		req.WithMessage(func(m *sip.Request) {
			via, ok := m.Headers.FirstVia()
			if !ok {
				t.Error("request headers missing Via")
				return
			}
			via.Addr = sip.MakeHostPortAddr("127.0.0.1", harness.addr.Port())
			via.Params.Set("rport", "")
		})
		sendReq(t, peer, harness.addr, req)

		res, src := readResFrom(t, peer)
		if got := res.Status(); got != sip.ResponseStatusMovedTemporarily {
			t.Fatalf("element %d response status = %v, want %v",
				i, got, sip.ResponseStatusMovedTemporarily)
		}
		// The datagram arrived from the listener that received the
		// request: hidden per-element state would emit it through the
		// other element's socket.
		if got := src; got != harness.addr {
			t.Errorf("element %d response source = %v, want %v", i, got, harness.addr)
		}

		via, ok := res.Headers().FirstVia()
		if !ok {
			t.Fatalf("element %d response headers missing Via", i)
		}
		// This asserts only the response shape — a response echoes the
		// request's Vias regardless of which element answered; the
		// datagram-source check above carries the element-isolation
		// weight.
		wantSentBy := sip.MakeHostPortAddr("127.0.0.1", harness.addr.Port())
		if got := via.Addr; !got.Equal(wantSentBy) {
			t.Errorf("element %d response top Via sent-by = %v, want %v", i, got, wantSentBy)
		}
	}

	if got, want := resolveCalls.Load(), int32(len(harnesses)); got != want {
		t.Errorf("resolver calls = %d, want %d", got, want)
	}
}
