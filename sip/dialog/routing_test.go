package dialog_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/dialog"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestDialog_RouteSet(t *testing.T) {
	t.Parallel()

	invite := routedInvite(t)
	res180 := newInviteRes(t, invite, sip.ResponseStatusRinging, "fork-b")
	setRecRoute(res180.Headers)
	res200 := newInviteRes(t, invite, sip.ResponseStatusOK, "fork-b")
	setRecRoute(res200.Headers)

	wantWire := []wantRoute{
		{host: "edge.example.test", params: map[string]string{"x": "one"}, flags: []string{"lr"}},
		{host: "core.example.test", secured: true, params: map[string]string{"transport": "tls", "x": "two"}},
		{host: "hub.example.test", flags: []string{"lr"}},
	}
	wantReversed := []wantRoute{
		{host: "hub.example.test", flags: []string{"lr"}},
		{host: "core.example.test", secured: true, params: map[string]string{"transport": "tls", "x": "two"}},
		{host: "edge.example.test", params: map[string]string{"x": "one"}, flags: []string{"lr"}},
	}

	// The caller's early dialog reverses the response Record-Route and
	// takes the remote target from the response Contact, not the request
	// URI.
	uac, err := dialog.NewUAC(invite, res180)
	if err != nil {
		t.Fatalf("dialog.NewUAC(180) error = %v, want nil", err)
	}
	uacView := uac.View()
	if uacView.RemoteTarget == nil {
		t.Fatal("uac.View().RemoteTarget = nil, want response Contact URI")
	}
	if uacView.RemoteTarget.Addr.Host() != "bob.example.test" {
		t.Fatalf("uac.View().RemoteTarget host = %q, want %q (peer Contact, not request URI)",
			uacView.RemoteTarget.Addr.Host(), "bob.example.test")
	}
	assertRoutes(t, uacView.RouteSet, wantReversed)
	if got, _ := uacView.RouteSet[2].Params.First("hop"); got != "a" {
		t.Fatalf("uac.View().RouteSet[2] param hop = %q, want %q", got, "a")
	}

	// The callee's dialog keeps the request Record-Route in wire order and
	// takes the remote target from the request Contact.
	uas, err := dialog.NewUAS(invite, res200)
	if err != nil {
		t.Fatalf("dialog.NewUAS() error = %v, want nil", err)
	}
	uasView := uas.View()
	if uasView.RemoteTarget == nil || uasView.RemoteTarget.Addr.Host() != "alice.example.test" {
		t.Fatalf("uas.View().RemoteTarget = %+v, want request Contact host %q",
			uasView.RemoteTarget, "alice.example.test")
	}
	assertRoutes(t, uasView.RouteSet, wantWire)

	// Mutating the establishing messages cannot leak into stored routing
	// state: target and hops were cloned at construction.
	for c := range res180.Headers.Contacts() {
		if u, ok := c.URI.(*sip.URI); ok {
			u.Addr = sip.MakeHostAddr("evil.example.test")
		}
	}
	if got := uac.View().RemoteTarget.Addr.Host(); got != "bob.example.test" {
		t.Fatalf("uac.View().RemoteTarget host after contact mutation = %q, want %q", got, "bob.example.test")
	}
	hop, _ := res180.Headers.FirstRecordRoute()
	if hopURI, ok := hop.URI.(*sip.URI); ok {
		hopURI.Addr = sip.MakeHostAddr("evil.example.test")
		hopURI.Params.Set("x", "tampered")
	}
	assertRoutes(t, uac.View().RouteSet, wantReversed)

	// Mutating a returned view cannot leak back into the dialog.
	out := uac.View()
	out.RemoteTarget.Addr = sip.MakeHostAddr("evil.example.test")
	out.RouteSet[0] = header.RouteHop{URI: &sip.URI{Addr: sip.MakeHostAddr("evil.example.test")}}
	if hopURI, ok := out.RouteSet[1].URI.(*sip.URI); ok {
		hopURI.Params.Set("x", "tampered")
	}
	out.RouteSet[2].Params.Set("hop", "tampered")
	fresh := uac.View()
	assertRoutes(t, fresh.RouteSet, wantReversed)
	if got := fresh.RemoteTarget.Addr.Host(); got != "bob.example.test" {
		t.Fatalf("uac.View().RemoteTarget host after view mutation = %q, want %q", got, "bob.example.test")
	}
	if got, _ := fresh.RouteSet[2].Params.First("hop"); got != "a" {
		t.Fatalf("uac.View().RouteSet[2] param hop after view mutation = %q, want %q", got, "a")
	}

	// Invalid Record-Route input is rejected without committing state.
	badRoutes := []struct {
		name string
		mut  hdrMut
	}{
		{"empty Record-Route field", setHdr(header.RecordRoute{})},
		{"wrong-typed Record-Route", setHdr(&header.Any{Name: "Record-Route", Value: "sip:x.example.test"})},
		{"non-SIP route URI", setHdr(header.RecordRoute{{URI: &uri.Tel{Number: "+123456"}}})},
		{"nil route URI", setHdr(header.RecordRoute{{}})},
		{"empty SIP route URI", setHdr(header.RecordRoute{{URI: &sip.URI{}}})},
	}
	for _, tt := range badRoutes {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := routedInvite(t)
			tt.mut(req.Headers)
			res := newInviteRes(t, req, sip.ResponseStatusOK, "fork-b")
			if _, err := dialog.NewUAS(req, res); !errors.Is(err, dialog.ErrInvalidMessage) {
				t.Errorf("dialog.NewUAS() error = %v, want %v", err, dialog.ErrInvalidMessage)
			}

			good := routedInvite(t)
			res = newInviteRes(t, good, sip.ResponseStatusOK, "fork-b")
			tt.mut(res.Headers)
			if _, err := dialog.NewUAC(good, res); !errors.Is(err, dialog.ErrInvalidMessage) {
				t.Errorf("dialog.NewUAC() error = %v, want %v", err, dialog.ErrInvalidMessage)
			}
		})
	}
}

func TestDialog_Confirm_RouteSet(t *testing.T) {
	t.Parallel()

	invite := routedInvite(t)

	// The early response uses a different Contact than the final 2xx and
	// carries the wire-order route set, which a UAC dialog holds reversed.
	res180 := newInviteRes(t, invite, sip.ResponseStatusRinging, "fork-b")
	res180.Headers.Set(header.Contact{
		{URI: &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("bob-early.example.test")}},
	})
	setRecRoute(res180.Headers)

	early, err := dialog.NewUAC(invite, res180)
	if err != nil {
		t.Fatalf("dialog.NewUAC(180) error = %v, want nil", err)
	}
	// Advance both counters through public actions; confirmation must
	// preserve them.
	if _, err := early.NewRequest(sip.RequestMethodBye); err != nil {
		t.Fatalf("early.NewRequest(BYE) error = %v, want nil", err)
	}
	if err := early.ValidateRequest(newDiagReq(t, early.ID(), sip.RequestMethodBye, 50)); err != nil {
		t.Fatalf("early.ValidateRequest() error = %v, want nil", err)
	}
	before := early.View()
	if want := uint(testInviteSeq + 1); before.LocalCSeq != want {
		t.Fatalf("before.View().LocalCSeq = %d, want %d", before.LocalCSeq, want)
	}
	if want := uint(50); before.RemoteCSeq != want {
		t.Fatalf("before.View().RemoteCSeq = %d, want %d", before.RemoteCSeq, want)
	}
	if before.RemoteTarget == nil || before.RemoteTarget.Addr.Host() != "bob-early.example.test" {
		t.Fatalf("early.View().RemoteTarget = %+v, want host %q", before.RemoteTarget, "bob-early.example.test")
	}
	assertRoutes(t, before.RouteSet, []wantRoute{
		{host: "hub.example.test", flags: []string{"lr"}},
		{host: "core.example.test", secured: true, params: map[string]string{"transport": "tls", "x": "two"}},
		{host: "edge.example.test", params: map[string]string{"x": "one"}, flags: []string{"lr"}},
	})

	// A matching 2xx with malformed Record-Route commits nothing.
	res200Bad := newInviteRes(t, invite, sip.ResponseStatusOK, "fork-b")
	res200Bad.Headers.Set(header.RecordRoute{{URI: &uri.Tel{Number: "+123456"}}})
	if changed, err := early.Confirm(res200Bad); changed || !errors.Is(err, dialog.ErrInvalidMessage) {
		t.Fatalf("early.Confirm(bad routes) = (%v, %v), want (false, %v)", changed, err, dialog.ErrInvalidMessage)
	}
	assertSameView(t, before, early.View())

	// A matching 2xx with a different Contact and a different route set
	// replaces only the routes: the early target and both CSeq baselines
	// survive per RFC 3261 13.2.2.4.
	res200 := newInviteRes(t, invite, sip.ResponseStatusOK, "fork-b")
	res200.Headers.Set(header.RecordRoute{
		{URI: &sip.URI{Addr: sip.MakeHostAddr("final.example.test"), Params: make(header.Values).Set("lr", "")}},
	})
	if changed, err := early.Confirm(res200); err != nil || !changed {
		t.Fatalf("early.Confirm(2xx) = (%v, %v), want (true, nil)", changed, err)
	}
	confirmed := early.View()
	assertRoutes(t, confirmed.RouteSet, []wantRoute{
		{host: "final.example.test", flags: []string{"lr"}},
	})
	if confirmed.RemoteTarget == nil || confirmed.RemoteTarget.Addr.Host() != "bob-early.example.test" {
		t.Fatalf("confirmed.View().RemoteTarget = %+v, want early target host %q",
			confirmed.RemoteTarget, "bob-early.example.test")
	}
	if confirmed.LocalCSeq != before.LocalCSeq || confirmed.RemoteCSeq != before.RemoteCSeq {
		t.Fatalf("confirmed.View() CSeq = (%d, %d), want (%d, %d)",
			confirmed.LocalCSeq, confirmed.RemoteCSeq, before.LocalCSeq, before.RemoteCSeq)
	}

	// A replayed 200 leaves the installed routes untouched.
	if changed, err := early.Confirm(res200); err != nil || changed {
		t.Fatalf("early.Confirm(replayed 2xx) = (%v, %v), want (false, nil)", changed, err)
	}
	assertSameView(t, confirmed, early.View())

	// A final response with no Record-Route empties the route set while
	// preserving everything else.
	early2, err := dialog.NewUAC(invite, res180)
	if err != nil {
		t.Fatalf("dialog.NewUAC(180) error = %v, want nil", err)
	}
	before2 := early2.View()
	if changed, err := early2.Confirm(newInviteRes(t, invite, sip.ResponseStatusOK, "fork-b")); err != nil || !changed {
		t.Fatalf("early2.Confirm(2xx no routes) = (%v, %v), want (true, nil)", changed, err)
	}
	want := before2
	want.State, want.RouteSet = dialog.StateConfirmed, nil
	got := early2.View()
	if len(got.RouteSet) == 0 {
		got.RouteSet = nil // nil and non-nil empty are equivalent here
	}
	if diff := cmp.Diff(want, got, viewCmpOpts); diff != "" {
		t.Errorf("early2.View() mismatch (-want +got):\n%s", diff)
	}

	// The callee keeps its request-derived routes when its own response
	// confirms the dialog, regardless of the response's Record-Route.
	uasEarly, err := dialog.NewUAS(invite, res180)
	if err != nil {
		t.Fatalf("dialog.NewUAS(180) error = %v, want nil", err)
	}
	uasBefore := uasEarly.View()
	if changed, err := uasEarly.Confirm(res200Bad); changed || !errors.Is(err, dialog.ErrInvalidMessage) {
		t.Fatalf("uasEarly.Confirm(bad routes) = (%v, %v), want (false, %v)", changed, err, dialog.ErrInvalidMessage)
	}
	assertSameView(t, uasBefore, uasEarly.View())
	res200Other := newInviteRes(t, invite, sip.ResponseStatusOK, "fork-b")
	res200Other.Headers.Set(header.RecordRoute{
		{URI: &sip.URI{Addr: sip.MakeHostAddr("unused.example.test"), Params: make(header.Values).Set("lr", "")}},
	})
	if changed, err := uasEarly.Confirm(res200Other); err != nil || !changed {
		t.Fatalf("uasEarly.Confirm(2xx) = (%v, %v), want (true, nil)", changed, err)
	}
	uasView := uasEarly.View()
	if uasView.State != dialog.StateConfirmed {
		t.Fatalf("uasEarly.View().State = %v, want %v", uasView.State, dialog.StateConfirmed)
	}
	assertRoutes(t, uasView.RouteSet, []wantRoute{
		{host: "edge.example.test", params: map[string]string{"x": "one"}, flags: []string{"lr"}},
		{host: "core.example.test", secured: true, params: map[string]string{"transport": "tls", "x": "two"}},
		{host: "hub.example.test", flags: []string{"lr"}},
	})
}
