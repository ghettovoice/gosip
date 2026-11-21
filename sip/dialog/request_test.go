package dialog_test

import (
	"bytes"
	"net/netip"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/dialog"
	"github.com/ghettovoice/gosip/sip/header"
)

func TestDialog_NewRequest_Routing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		resRoutes   header.RecordRoute // wire order on the establishing 2xx
		wantRoutes  []wantRoute        // logical Route order in the request
		wantNextHop wantRoute
		strict      bool
	}{
		{
			name:        "no routes",
			wantNextHop: wantRoute{host: "192.0.2.30", port: 5060},
		},
		{
			name:        "loose first hop",
			resRoutes:   header.RecordRoute{{URI: looseRouteURI()}},
			wantRoutes:  []wantRoute{{host: "192.0.2.20", port: 5060, flags: []string{"lr"}}},
			wantNextHop: wantRoute{host: "192.0.2.20", port: 5060},
		},
		{
			name: "strict first hop",
			// The UAC reverses the response Record-Route, so the wire
			// order [loose, strict] stores the dialog routes
			// [strict, loose].
			resRoutes: header.RecordRoute{
				{URI: looseRouteURI()},
				{URI: strictRouteURI()},
			},
			wantRoutes: []wantRoute{
				{host: "192.0.2.10", port: 5060},
				{host: "192.0.2.20", port: 5060, flags: []string{"lr"}},
			},
			wantNextHop: wantRoute{host: "192.0.2.10", port: 5060},
			strict:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d, invite := diagWithRoutes(t, tt.resRoutes)
			out, err := d.NewRequest(sip.RequestMethodBye)
			if err != nil {
				t.Fatalf("d.NewRequest(BYE) error = %v, want nil", err)
			}

			// Logical form before any resolution: Request-URI is the
			// remote Contact and Route carries the dialog routes in order.
			if got := out.Method(); got != sip.RequestMethodBye {
				t.Fatalf("out.Method() = %v, want %v", got, sip.RequestMethodBye)
			}
			reqURI, ok := out.URI().(*sip.URI)
			if !ok || reqURI == nil {
				t.Fatalf("out.URI() type = %T, want *sip.URI", out.URI())
			}
			if got := reqURI.Addr.Host(); got != "192.0.2.30" {
				t.Errorf("Request-URI host = %q, want Contact %q", got, "192.0.2.30")
			}
			if port, ok := reqURI.Addr.Port(); !ok || port != 5060 {
				t.Errorf("Request-URI port = %v, want %d", port, 5060)
			}
			hdrs := out.Headers()
			assertRoutes(t, routeHops(hdrs), tt.wantRoutes)
			if cid, _ := hdrs.CallID(); string(cid) != testCallID {
				t.Errorf("Call-ID = %q, want %q", cid, testCallID)
			}
			from, _ := hdrs.From()
			if tag, _ := from.Tag(); tag != "alice" {
				t.Errorf("From tag = %q, want %q", tag, "alice")
			}
			if u, ok := from.URI.(*sip.URI); !ok || u == nil || u.Addr.Host() != "alice.example.test" {
				t.Errorf("From URI = %v, want host %q", from.URI, "alice.example.test")
			}
			to, _ := hdrs.To()
			if tag, _ := to.Tag(); tag != "fork-b" {
				t.Errorf("To tag = %q, want %q", tag, "fork-b")
			}
			if u, ok := to.URI.(*sip.URI); !ok || u == nil || u.Addr.Host() != "example.test" {
				t.Errorf("To URI = %v, want host %q", to.URI, "example.test")
			}
			if cseq, ok := hdrs.CSeq(); !ok || cseq.SeqNum != 43 || cseq.Method != sip.RequestMethodBye {
				t.Fatalf("out.Headers().CSeq() = %v, want 43 BYE", cseq)
			}
			via, _ := hdrs.FirstVia()
			branch, _ := via.Branch()
			if !sip.IsRFC3261Branch(branch) {
				t.Errorf("Via branch = %q, want RFC 3261 branch", branch)
			}
			invVia, _ := invite.Headers.FirstVia()
			if invBranch, _ := invVia.Branch(); branch == invBranch {
				t.Errorf("Via branch = %q, want a fresh branch different from the INVITE", branch)
			}
			if n := len(hdrs.Get("Authorization")) + len(hdrs.Get("Proxy-Authorization")); n != 0 {
				t.Errorf("credential header count = %d, want 0", n)
			}
			if out.Forwarded() {
				t.Error("out.Forwarded() = true, want false")
			}
			if out.RemoteAddr().IsValid() {
				t.Errorf("out.RemoteAddr() = %v, want unset", out.RemoteAddr())
			}
			if out.Transport().IsValid() {
				t.Errorf("out.Transport() = %v, want unset", out.Transport())
			}

			// Resolution on a fresh Element: the next hop is the Contact
			// or the first Route, and strict routing rewrites once.
			elm := &sip.Element{}
			t.Cleanup(func() { elm.Close() })
			for range 2 {
				trgt, err := elm.ResolveRequestTarget(t.Context(), out)
				if err != nil {
					t.Fatalf("elm.ResolveRequestTarget() error = %v, want nil", err)
				}
				if got := trgt.Addr.Host(); got != tt.wantNextHop.host {
					t.Errorf("resolved next hop host = %q, want %q", got, tt.wantNextHop.host)
				}
				if tt.wantNextHop.port != 0 {
					if port, ok := trgt.Addr.Port(); !ok || port != tt.wantNextHop.port {
						t.Errorf("resolved next hop port = %v, want %d", port, tt.wantNextHop.port)
					}
				}
			}

			if !tt.strict {
				return
			}
			outURI, _ := out.URI().(*sip.URI)
			if got := outURI.Addr.Host(); got != "192.0.2.10" {
				t.Fatalf("wire Request-URI host after strict resolve = %q, want %q", got, "192.0.2.10")
			}
			assertRoutes(t, routeHops(out.Headers()), []wantRoute{
				{host: "192.0.2.20", port: 5060, flags: []string{"lr"}},
				{host: "192.0.2.30", port: 5060},
			})
		})
	}
}

func TestDialog_NewRequest_Ownership(t *testing.T) {
	t.Parallel()

	// The establishing INVITE arrives on an envelope carrying unrelated
	// transport state; none of it may leak into built requests.
	src, _ := newInvite(t)
	srcEnv := sip.NewRequestEnvelope(src)
	srcEnv.SetRemoteAddr(netip.MustParseAddrPort("192.0.2.99:5090"))
	srcEnv.SetTransport(sip.TCPMetadata())
	srcEnv.MarkForwarded()

	invite := srcEnv.Message()
	res := newInviteRes(t, invite, sip.ResponseStatusOK, "fork-b")
	res.Headers.Set(header.Contact{{URI: calleeContactURI()}})
	res.Headers.Set(header.RecordRoute{{URI: looseRouteURI()}})

	d, err := dialog.NewUAC(invite, res)
	if err != nil {
		t.Fatalf("dialog.NewUAC() error = %v, want nil", err)
	}

	body := []byte("v=0")
	ctype := &header.ContentType{Type: "application", Subtype: "sdp"}
	out, err := d.NewRequest(sip.RequestMethodBye, dialog.RequestOptions{Body: body, ContentType: ctype})
	if err != nil {
		t.Fatalf("d.NewRequest(BYE) error = %v, want nil", err)
	}

	if out.Forwarded() {
		t.Error("out.Forwarded() = true, want false")
	}
	if out.RemoteAddr().IsValid() {
		t.Errorf("out.RemoteAddr() = %v, want unset", out.RemoteAddr())
	}
	if out.Transport().IsValid() {
		t.Errorf("out.Transport() = %v, want unset", out.Transport())
	}

	// Caller inputs are cloned: mutating them leaves the request intact.
	body[0] = 'X'
	ctype.Type = "tampered"
	if got := out.Message().Body; !bytes.Equal(got, []byte("v=0")) {
		t.Errorf("out body = %q, want %q", got, "v=0")
	}
	if ct, ok := out.Headers().ContentType(); !ok || ct.Type != "application" {
		t.Errorf("Content-Type = %+v, want application", ct)
	}

	// Mutating the output's routes does not touch the stored set, and a
	// later build consumes the next CSeq number.
	out.WithMessage(func(r *sip.Request) {
		if hop, ok := r.Headers.FirstRoute(); ok {
			if u, ok := hop.URI.(*sip.URI); ok {
				u.Addr = sip.MakeHostAddr("evil.example.test")
			}
		}
	})
	later, err := d.NewRequest(sip.RequestMethodBye)
	if err != nil {
		t.Fatalf("d.NewRequest(BYE) error = %v, want nil", err)
	}
	assertRoutes(t, routeHops(later.Headers()), []wantRoute{
		{host: "192.0.2.20", port: 5060, flags: []string{"lr"}},
	})
	if cseq, ok := later.Headers().CSeq(); !ok || cseq.SeqNum != 44 {
		t.Errorf("later.Headers().CSeq() = %v, want 44 (each build consumes a number)", cseq)
	}
}

func TestDialog_NewRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method sip.RequestMethod
		opts   []dialog.RequestOptions
		want   error
	}{
		{"ACK", sip.RequestMethodAck, nil, dialog.ErrUnsupportedMethod},
		{"lowercase ack", "ack", nil, dialog.ErrUnsupportedMethod},
		{"CANCEL", sip.RequestMethodCancel, nil, dialog.ErrUnsupportedMethod},
		{"INVITE", sip.RequestMethodInvite, nil, dialog.ErrUnsupportedMethod},
		{"UPDATE", sip.RequestMethodUpdate, nil, dialog.ErrUnsupportedMethod},
		{"mixed-case update", "Update", nil, dialog.ErrUnsupportedMethod},
		{"empty method", "", nil, dialog.ErrInvalidMessage},
		{"invalid method token", "BY E", nil, dialog.ErrInvalidMessage},
		{
			"body without Content-Type", sip.RequestMethodBye,
			[]dialog.RequestOptions{{Body: []byte("v=0")}},
			dialog.ErrInvalidMessage,
		},
		{
			"invalid Content-Type", sip.RequestMethodBye,
			[]dialog.RequestOptions{{Body: []byte("v=0"), ContentType: &header.ContentType{}}},
			dialog.ErrInvalidMessage,
		},
		{
			"invalid Content-Type with empty body", sip.RequestMethodBye,
			[]dialog.RequestOptions{{ContentType: &header.ContentType{}}},
			dialog.ErrInvalidMessage,
		},
	}

	d, _ := diagWithRoutes(t, nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := d.NewRequest(tt.method, tt.opts...); !errors.Is(err, tt.want) {
				t.Errorf("d.NewRequest(%q) error = %v, want %v", tt.method, err, tt.want)
			}
		})
	}

	// The last whole options value wins: the empty trailing value drops
	// the body and Content-Type of the earlier one.
	out, err := d.NewRequest(sip.RequestMethodBye,
		dialog.RequestOptions{
			Body:        []byte("v=0"),
			ContentType: &header.ContentType{Type: "application", Subtype: "sdp"},
		},
		dialog.RequestOptions{},
	)
	if err != nil {
		t.Fatalf("d.NewRequest(BYE) error = %v, want nil", err)
	}
	if got := out.Message().Body; len(got) != 0 {
		t.Errorf("out body = %q, want empty (last options wins)", got)
	}
	if n := len(out.Headers().Get("Content-Type")); n != 0 {
		t.Errorf("Content-Type header count = %d, want 0", n)
	}

	// A terminated dialog refuses new requests.
	if changed := d.Terminate(); !changed {
		t.Fatal("d.Terminate() = false, want true")
	}
	if _, err := d.NewRequest(sip.RequestMethodBye); !errors.Is(err, dialog.ErrTerminated) {
		t.Fatalf("d.NewRequest() on terminated dialog error = %v, want %v", err, dialog.ErrTerminated)
	}
}
