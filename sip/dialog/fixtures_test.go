package dialog_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/dialog"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

const (
	testCallID    = "call-1@example.test"
	testInviteSeq = 42
)

// newInvite builds the fixture's initial INVITE from alice to bob with one
// caller Contact header. It returns the request and the From URI so tests
// can verify that a dialog owns a copy of the input identity.
func newInvite(tb testing.TB) (*sip.Request, *sip.URI) {
	tb.Helper()

	callerURI := &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.example.test")}
	calleeURI := &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("example.test")}
	req, err := sip.NewRequest(sip.RequestMethodInvite, calleeURI, callerURI, calleeURI, sip.RequestOptions{
		CallID:   testCallID,
		LocalTag: "alice",
		SeqNum:   testInviteSeq,
	})
	if err != nil {
		tb.Fatalf("sip.NewRequest() error = %v, want nil", err)
	}
	req.Headers.Set(header.Contact{
		{URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.example.test")}},
	})
	return req, callerURI
}

// newInviteRes builds a response to the fixture INVITE with the given
// status, callee (To) tag, and one callee Contact header.
func newInviteRes(tb testing.TB, invite *sip.Request, sts sip.ResponseStatus, tag string) *sip.Response {
	tb.Helper()

	res, err := invite.NewResponse(sts, sip.ResponseOptions{
		LocalTag: tag,
		Headers: sip.Headers{
			"Contact": {
				header.Contact{
					{URI: &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("bob.example.test")}},
				},
			},
		},
	})
	if err != nil {
		tb.Fatalf("invite.NewResponse(%v) error = %v, want nil", sts, err)
	}
	return res
}

// newInboundReq builds an in-dialog request from the callee to the
// caller as the caller endpoint would receive it: From carries the remote
// tag and To carries the local tag.
func newInboundReq(tb testing.TB, remoteTag string, method sip.RequestMethod, seq uint) *sip.Request {
	tb.Helper()

	calleeURI := &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("example.test")}
	callerURI := &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.example.test")}
	req, err := sip.NewRequest(method, callerURI, calleeURI, callerURI, sip.RequestOptions{
		CallID:    testCallID,
		LocalTag:  remoteTag,
		RemoteTag: "alice",
		SeqNum:    seq,
	})
	if err != nil {
		tb.Fatalf("sip.NewRequest() error = %v, want nil", err)
	}
	return req
}

// assertSameView asserts that two detached views carry the same state,
// including remote target and route set.
func assertSameView(tb testing.TB, want, got dialog.View) {
	tb.Helper()

	if len(got.RouteSet) == 0 && len(want.RouteSet) == 0 {
		want.RouteSet, got.RouteSet = nil, nil
	}
	if diff := cmp.Diff(want, got, viewCmpOpts); diff != "" {
		tb.Fatalf("dialog.View() mismatch (-want +got):\n%s", diff)
	}
}

// viewCmpOpts compares dialog views through the public equality of party
// addresses and URIs, so a before/after diff stays meaningful once the
// routing fields become populated.
var viewCmpOpts = cmp.Options{
	cmp.Comparer(func(a, b header.NameAddr) bool { return a.Equal(b) }),
	cmp.Comparer(func(a, b *sip.URI) bool {
		if a == nil || b == nil {
			return a == b
		}
		return a.Equal(b)
	}),
}

// hdrMut mutates a fixture message's headers in place.
type hdrMut func(sip.Headers)

// resCase returns a builder for a fork-b tagged response of the given
// status to the fixture INVITE, mutated before use.
func resCase(sts sip.ResponseStatus, mut hdrMut) func(testing.TB, *sip.Request) *sip.Response {
	return func(tb testing.TB, inv *sip.Request) *sip.Response {
		tb.Helper()
		res := newInviteRes(tb, inv, sts, "fork-b")
		if mut != nil {
			mut(res.Headers)
		}
		return res
	}
}

// invCase returns a builder for the fixture INVITE mutated before use.
func invCase(mut func(*sip.Request)) func(testing.TB) *sip.Request {
	return func(tb testing.TB) *sip.Request {
		tb.Helper()
		req, _ := newInvite(tb)
		if mut != nil {
			mut(req)
		}
		return req
	}
}

// invHdrs builds the fixture INVITE with its headers mutated.
func invHdrs(mut hdrMut) func(testing.TB) *sip.Request {
	return invCase(func(req *sip.Request) { mut(req.Headers) })
}

func delHdr(name sip.HeaderName) hdrMut {
	return func(hdrs sip.Headers) { hdrs.Delete(name) }
}

func dropToTag(hdrs sip.Headers) {
	to, _ := hdrs.To()
	to.Params.Delete("tag")
}

func setToTag(tag string) hdrMut {
	return func(hdrs sip.Headers) {
		to, _ := hdrs.To()
		if to.Params == nil {
			to.Params = make(header.Values)
		}
		to.Params.Set("tag", tag)
	}
}

func dupToTag(hdrs sip.Headers) {
	to, _ := hdrs.To()
	if to.Params == nil {
		to.Params = make(header.Values)
	}
	to.Params["tag"] = []string{"fork-b", "fork-c"}
}

func dropFromTag(hdrs sip.Headers) {
	from, _ := hdrs.From()
	from.Params.Delete("tag")
}

func setFromTag(tag string) hdrMut {
	return func(hdrs sip.Headers) {
		from, _ := hdrs.From()
		if from.Params == nil {
			from.Params = make(header.Values)
		}
		from.Params.Set("tag", tag)
	}
}

func dupFromTag(hdrs sip.Headers) {
	from, _ := hdrs.From()
	if from.Params == nil {
		from.Params = make(header.Values)
	}
	from.Params["tag"] = []string{"alice", "alice-2"}
}

func dupFromHdr(hdrs sip.Headers) {
	hdrs.Append(&header.From{
		URI:    &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.example.test")},
		Params: make(header.Values).Set("tag", "alice"),
	})
}

func setCallID(callID string) hdrMut {
	return func(hdrs sip.Headers) { hdrs.Set(header.CallID(callID)) }
}

func dupCallID(hdrs sip.Headers) { hdrs.Append(header.CallID("call-2@example.test")) }

func setAnyCallID(hdrs sip.Headers) {
	hdrs.Set(&header.Any{Name: "Call-ID", Value: "call-2@example.test"})
}

func setAnyCSeq(hdrs sip.Headers) {
	hdrs.Set(&header.Any{Name: "CSeq", Value: "42 INVITE"})
}

func setAnyVia(hdrs sip.Headers) {
	hdrs.Set(&header.Any{
		Name:  "Via",
		Value: "SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK-foreign",
	})
}

func setEmptyVia(hdrs sip.Headers) { hdrs.Set(header.Via{}) }

func setCSeqNum(n uint) hdrMut {
	return func(hdrs sip.Headers) {
		cseq, _ := hdrs.CSeq()
		cseq.SeqNum = n
	}
}

func setCSeqMethod(m sip.RequestMethod) hdrMut {
	return func(hdrs sip.Headers) {
		cseq, _ := hdrs.CSeq()
		cseq.Method = m
	}
}

func dupCSeq(hdrs sip.Headers) {
	hdrs.Append(&header.CSeq{SeqNum: testInviteSeq + 1, Method: sip.RequestMethodInvite})
}

func newBranch(hdrs sip.Headers) {
	via, _ := hdrs.FirstVia()
	via.Params.Set("branch", sip.GenerateBranch(0))
}

func setHdr(hdr sip.Header) hdrMut {
	return func(hdrs sip.Headers) { hdrs.Set(hdr) }
}

func setContact(c header.Contact) hdrMut {
	return func(hdrs sip.Headers) { hdrs.Set(c) }
}

func appendContact(c header.Contact) hdrMut {
	return func(hdrs sip.Headers) { hdrs.Append(c) }
}

func twoContacts(hdrs sip.Headers) {
	hdrs.Set(header.Contact{
		{URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.example.test")}},
		{URI: &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("bob.example.test")}},
	})
}

func splitContacts(hdrs sip.Headers) {
	hdrs.Append(header.Contact{
		{URI: &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("bob.example.test")}},
	})
}

// Unusable Contact header variants: a non-SIP URI, a typed-nil SIP URI,
// and a SIP URI that fails validation.
var (
	telContact      = header.Contact{{URI: &uri.Tel{Number: "+123456"}}}
	nilURIContact   = header.Contact{{URI: (*sip.URI)(nil)}}
	emptyURIContact = header.Contact{{URI: &sip.URI{}}}
)

// Route URI builders for the fixture Record-Route set: edge, core, and
// hub hops with their own schemes and URI parameters.
func edgeRouteURI() *sip.URI {
	return &sip.URI{
		Addr:   sip.MakeHostAddr("edge.example.test"),
		Params: make(header.Values).Set("lr", "").Set("x", "one"),
	}
}

func coreRouteURI() *sip.URI {
	return &sip.URI{
		Secured: true,
		Addr:    sip.MakeHostAddr("core.example.test"),
		Params:  make(header.Values).Set("transport", "tls").Set("x", "two"),
	}
}

func hubRouteURI() *sip.URI {
	return &sip.URI{
		Addr:   sip.MakeHostAddr("hub.example.test"),
		Params: make(header.Values).Set("lr", ""),
	}
}

// setRecRoute installs the fixture route set on a message: two
// Record-Route fields carrying edge+core and hub hops.
func setRecRoute(hdrs sip.Headers) {
	hdrs.Set(header.RecordRoute{
		{URI: edgeRouteURI(), Params: make(header.Values).Set("hop", "a")},
		{URI: coreRouteURI()},
	})
	hdrs.Append(header.RecordRoute{{URI: hubRouteURI()}})
}

// routedInvite builds the fixture INVITE with its Record-Route set in
// wire order edge, core, hub.
func routedInvite(tb testing.TB) *sip.Request {
	tb.Helper()
	req, _ := newInvite(tb)
	setRecRoute(req.Headers)
	return req
}

// wantRoute describes an expected route hop for assertion.
type wantRoute struct {
	host    string
	port    uint16 // 0 means the port is not asserted
	secured bool
	params  map[string]string
	flags   []string
}

// assertRoutes checks the route set's exact order and each hop's SIP/SIPS
// URI scheme, host, and retained URI parameters.
func assertRoutes(tb testing.TB, got []header.RouteHop, want []wantRoute) {
	tb.Helper()

	if len(got) != len(want) {
		tb.Fatalf("route set length = %d (%+v), want %d", len(got), got, len(want))
	}
	for i, w := range want {
		u, ok := got[i].URI.(*sip.URI)
		if !ok || u == nil {
			tb.Fatalf("RouteSet[%d].URI type = %T, want *sip.URI", i, got[i].URI)
		}
		if u.Secured != w.secured {
			tb.Errorf("RouteSet[%d] secured = %v, want %v", i, u.Secured, w.secured)
		}
		if host := u.Addr.Host(); host != w.host {
			tb.Errorf("RouteSet[%d] host = %q, want %q", i, host, w.host)
		}
		if w.port != 0 {
			if port, ok := u.Addr.Port(); !ok || port != w.port {
				tb.Errorf("RouteSet[%d] port = %v, want %d", i, port, w.port)
			}
		}
		for name, wantVal := range w.params {
			if val, _ := u.Params.First(name); val != wantVal {
				tb.Errorf("RouteSet[%d] param %q = %q, want %q", i, name, val, wantVal)
			}
		}
		for _, name := range w.flags {
			if !u.Params.Has(name) {
				tb.Errorf("RouteSet[%d] missing flag param %q", i, name)
			}
		}
	}
}

// looseRouteURI and strictRouteURI are the fixture Route/Record-Route hop
// URIs: a loose-routing hop and a strict-routing one.
func looseRouteURI() *sip.URI {
	return &sip.URI{
		Addr:   sip.MakeHostPortAddr("192.0.2.20", 5060),
		Params: make(header.Values).Set("lr", ""),
	}
}

func strictRouteURI() *sip.URI {
	return &sip.URI{Addr: sip.MakeHostPortAddr("192.0.2.10", 5060)}
}

// calleeContactURI is the fixture remote target carried by the peer
// Contact.
func calleeContactURI() *sip.URI {
	return &sip.URI{
		User: sip.MakeUserInfo("callee"),
		Addr: sip.MakeHostPortAddr("192.0.2.30", 5060),
	}
}

// diagWithRoutes builds a confirmed caller dialog whose remote target
// is the callee Contact and whose route set comes from the response's
// wire-order Record-Route. It returns the dialog and the INVITE it was
// built from.
func diagWithRoutes(tb testing.TB, rr header.RecordRoute) (*dialog.Dialog, *sip.Request) {
	tb.Helper()
	invite, _ := newInvite(tb)
	res := newInviteRes(tb, invite, sip.ResponseStatusOK, "fork-b")
	res.Headers.Set(header.Contact{{URI: calleeContactURI()}})
	if len(rr) > 0 {
		res.Headers.Set(rr)
	}
	d, err := dialog.NewUAC(invite, res)
	if err != nil {
		tb.Fatalf("dialog.NewUAC() error = %v, want nil", err)
	}
	return d, invite
}

// routeHops collects the Route header hops of a message in order.
func routeHops(hdrs sip.Headers) []header.RouteHop {
	var hops []header.RouteHop
	for hop := range hdrs.Routes() {
		hops = append(hops, *hop)
	}
	return hops
}

// newDiagReq builds an inbound in-dialog request for the endpoint
// holding the given identity: From carries the remote tag, To the local
// tag, and Call-ID the dialog's.
func newDiagReq(tb testing.TB, id dialog.ID, method sip.RequestMethod, seq uint) *sip.Request {
	tb.Helper()

	req, err := sip.NewRequest(
		method,
		&sip.URI{Addr: sip.MakeHostAddr("target.example.test")},
		&sip.URI{Addr: sip.MakeHostAddr("from.example.test")},
		&sip.URI{Addr: sip.MakeHostAddr("to.example.test")},
		sip.RequestOptions{
			CallID:    id.CallID,
			LocalTag:  id.RemoteTag,
			RemoteTag: id.LocalTag,
			SeqNum:    seq,
		},
	)
	if err != nil {
		tb.Fatalf("sip.NewRequest() error = %v, want nil", err)
	}
	return req
}
