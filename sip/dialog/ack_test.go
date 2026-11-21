package dialog_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/dialog"
	"github.com/ghettovoice/gosip/sip/header"
)

// credentialedInvite returns the fixture INVITE carrying two
// Authorization and two Proxy-Authorization fields.
func credentialedInvite(tb testing.TB) *sip.Request {
	tb.Helper()

	invite, _ := newInvite(tb)
	digestURI := &sip.URI{Addr: sip.MakeHostAddr("example.test")}
	invite.Headers.Append(
		&header.Authorization{
			AuthCredentials: &header.DigestCredentials{
				Username: "alice",
				Realm:    "example.test",
				Nonce:    "n1",
				URI:      digestURI,
				Response: "0123456789abcdef0123456789abcdef",
			},
		},
		&header.Authorization{AuthCredentials: &header.BearerCredentials{Token: "bearer-1"}},
		&header.ProxyAuthorization{AuthCredentials: &header.BearerCredentials{Token: "proxy-1"}},
		&header.ProxyAuthorization{
			AuthCredentials: &header.DigestCredentials{
				Username: "alice",
				Realm:    "proxy.test",
				Nonce:    "n2",
				URI:      digestURI,
				Response: "fedcba9876543210fedcba9876543210",
			},
		},
	)
	return invite
}

func hdrVals(tb testing.TB, hdrs sip.Headers, name string) []string {
	tb.Helper()

	hs := hdrs.Get(sip.HeaderName(name))
	vals := make([]string, 0, len(hs))
	for _, h := range hs {
		vals = append(vals, h.RenderValue())
	}
	return vals
}

func TestDialog_NewACK(t *testing.T) {
	t.Parallel()

	invite := credentialedInvite(t)
	res200B := newInviteRes(t, invite, sip.ResponseStatusOK, "fork-b")
	d, err := dialog.NewUAC(invite, res200B)
	if err != nil {
		t.Fatalf("dialog.NewUAC() error = %v, want nil", err)
	}
	// Advance the local counter: the ACK must not consume a number.
	if _, err := d.NewRequest(sip.RequestMethodBye); err != nil {
		t.Fatalf("d.NewRequest(BYE) error = %v, want nil", err)
	}

	body := []byte{0, 1, 255}
	ctype := &header.ContentType{Type: "application", Subtype: "octet-stream"}
	ack, err := d.NewACK(invite, res200B, dialog.RequestOptions{Body: body, ContentType: ctype})
	if err != nil {
		t.Fatalf("d.NewACK() error = %v, want nil", err)
	}

	if got := ack.Method(); got != sip.RequestMethodAck {
		t.Fatalf("ack.Method() = %v, want %v", got, sip.RequestMethodAck)
	}
	// The ACK reuses the initial INVITE's sequence number, not the
	// dialog's next local number.
	if cseq, ok := ack.Headers().CSeq(); !ok || cseq.SeqNum != 42 || cseq.Method != sip.RequestMethodAck {
		t.Fatalf("ack.Headers().CSeq() = %v, want 42 ACK", cseq)
	}
	hdrs := ack.Headers()
	if cid, _ := hdrs.CallID(); string(cid) != testCallID {
		t.Errorf("Call-ID = %q, want %q", cid, testCallID)
	}
	if from, _ := hdrs.From(); from != nil {
		if tag, _ := from.Tag(); tag != "alice" {
			t.Errorf("From tag = %q, want %q", tag, "alice")
		}
	}
	if to, _ := hdrs.To(); to != nil {
		if tag, _ := to.Tag(); tag != "fork-b" {
			t.Errorf("To tag = %q, want %q", tag, "fork-b")
		}
	}
	via, _ := hdrs.FirstVia()
	branch, _ := via.Branch()
	invVia, _ := invite.Headers.FirstVia()
	invBranch, _ := invVia.Branch()
	if !sip.IsRFC3261Branch(branch) || branch == invBranch {
		t.Errorf("ack Via branch = %q, want fresh RFC 3261 branch != %q", branch, invBranch)
	}
	// Credentials copied byte-for-byte from the actual INVITE.
	for _, name := range []string{"Authorization", "Proxy-Authorization"} {
		got, want := hdrVals(t, hdrs, name), hdrVals(t, invite.Headers, name)
		if len(got) != len(want) {
			t.Fatalf("ack %s count = %d, want %d", name, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("ack %s[%d] = %q, want %q", name, i, got[i], want[i])
			}
		}
	}
	if got := ack.Message().Body; !bytes.Equal(got, body) {
		t.Errorf("ack body = %v, want %v", got, body)
	}
	if ct, ok := hdrs.ContentType(); !ok || ct.Type != "application" || ct.Subtype != "octet-stream" {
		t.Errorf("ack Content-Type = %+v, want application/octet-stream", ct)
	}
	if ack.Forwarded() || ack.RemoteAddr().IsValid() || ack.Transport().IsValid() {
		t.Error("ack envelope carries inherited transport metadata")
	}
	// Counters unchanged: the ACK allocates nothing.
	if got := d.View().LocalCSeq; got != 43 {
		t.Errorf("d.View().LocalCSeq = %d, want 43 (ACK consumes nothing)", got)
	}

	// Mutating the INVITE's credentials and the caller options after
	// construction leaves the prepared ACK untouched.
	creds := invite.Headers.Get("Authorization")[0].(*header.Authorization)
	creds.AuthCredentials.(*header.DigestCredentials).Username = "mallory"
	body[0] = 99
	ctype.Type = "tampered"
	if got := hdrVals(t, ack.Headers(), "Authorization")[0]; !strings.Contains(got, `"alice"`) || strings.Contains(got, "mallory") {
		t.Errorf("ack Authorization[0] = %q, want the original credential value", got)
	}
	if got := ack.Message().Body; !bytes.Equal(got, []byte{0, 1, 255}) {
		t.Errorf("ack body = %v, want unchanged %v", got, []byte{0, 1, 255})
	}
	if ct, _ := ack.Headers().ContentType(); ct.Type != "application" {
		t.Errorf("ack Content-Type = %+v, want application", ct)
	}

	// Strict first hop resolves through Element exactly once, as for
	// ordinary requests.
	invite2, _ := newInvite(t)
	res2 := newInviteRes(t, invite2, sip.ResponseStatusOK, "fork-b")
	res2.Headers.Set(header.Contact{{URI: calleeContactURI()}})
	res2.Headers.Set(header.RecordRoute{
		{URI: looseRouteURI()},
		{URI: strictRouteURI()},
	})
	sd, err := dialog.NewUAC(invite2, res2)
	if err != nil {
		t.Fatalf("dialog.NewUAC(strict) error = %v, want nil", err)
	}
	sack, err := sd.NewACK(invite2, res2)
	if err != nil {
		t.Fatalf("sd.NewACK() error = %v, want nil", err)
	}
	elm := &sip.Element{}
	t.Cleanup(func() { elm.Close() })
	for range 2 {
		trgt, err := elm.ResolveRequestTarget(t.Context(), sack)
		if err != nil {
			t.Fatalf("elm.ResolveRequestTarget() error = %v, want nil", err)
		}
		if got := trgt.Addr.Host(); got != "192.0.2.10" {
			t.Errorf("ack next hop = %q, want strict hop %q", got, "192.0.2.10")
		}
	}
	outURI, _ := sack.URI().(*sip.URI)
	if got := outURI.Addr.Host(); got != "192.0.2.10" {
		t.Fatalf("ack wire Request-URI = %q, want %q", got, "192.0.2.10")
	}
	assertRoutes(t, routeHops(sack.Headers()), []wantRoute{
		{host: "192.0.2.20", port: 5060, flags: []string{"lr"}},
		{host: "192.0.2.30", port: 5060},
	})

	// Rejections.
	t.Run("rejections", func(t *testing.T) {
		t.Parallel()

		okInvite, _ := newInvite(t)
		okRes := newInviteRes(t, okInvite, sip.ResponseStatusOK, "fork-b")
		base, err := dialog.NewUAC(okInvite, okRes)
		if err != nil {
			t.Fatalf("dialog.NewUAC() error = %v, want nil", err)
		}
		early, err := dialog.NewUAC(okInvite, newInviteRes(t, okInvite, sip.ResponseStatusRinging, "fork-b"))
		if err != nil {
			t.Fatalf("dialog.NewUAC(180) error = %v, want nil", err)
		}
		uas, err := dialog.NewUAS(okInvite, okRes)
		if err != nil {
			t.Fatalf("dialog.NewUAS() error = %v, want nil", err)
		}
		term, err := dialog.NewUAC(okInvite, okRes)
		if err != nil {
			t.Fatalf("dialog.NewUAC() error = %v, want nil", err)
		}
		term.Terminate()

		cases := []struct {
			name   string
			d      *dialog.Dialog
			invite *sip.Request
			res    *sip.Response
			want   error
		}{
			{"nil INVITE", base, nil, okRes, dialog.ErrInvalidMessage},
			{"nil response", base, okInvite, nil, dialog.ErrInvalidMessage},
			{
				"non-2xx",
				base,
				okInvite,
				newInviteRes(t, okInvite, sip.ResponseStatusBusyHere, "fork-b"), dialog.ErrNotEstablishing,
			},
			{
				"provisional",
				base,
				okInvite,
				newInviteRes(t, okInvite, sip.ResponseStatusRinging, "fork-b"), dialog.ErrNotEstablishing,
			},
			{"wrong INVITE branch", base, invHdrs(newBranch)(t), okRes, dialog.ErrMismatch},
			{"wrong INVITE From tag", base, invHdrs(setFromTag("alice-x"))(t), okRes, dialog.ErrMismatch},
			{"wrong INVITE CSeq", base, invHdrs(setCSeqNum(testInviteSeq + 1))(t), okRes, dialog.ErrMismatch},
			{
				"mismatched 2xx",
				base,
				okInvite,
				resCase(sip.ResponseStatusOK, setToTag("fork-c"))(t, okInvite), dialog.ErrMismatch,
			},
			{
				"early dialog",
				early,
				okInvite,
				newInviteRes(t, okInvite, sip.ResponseStatusRinging, "fork-b"), dialog.ErrNotEstablishing,
			},
			{"early dialog 2xx", early, okInvite, okRes, dialog.ErrInvalidState},
			{"UAS dialog", uas, okInvite, okRes, dialog.ErrInvalidState},
			{"terminated dialog", term, okInvite, okRes, dialog.ErrTerminated},
		}
		for _, tt := range cases {
			if _, err := tt.d.NewACK(tt.invite, tt.res); !errors.Is(err, tt.want) {
				t.Errorf("%s: tt.d.NewACK() error = %v, want %v", tt.name, err, tt.want)
			}
		}
	})
}

func TestDialog_MatchACK(t *testing.T) {
	t.Parallel()

	invite, _ := newInvite(t)
	res200B := newInviteRes(t, invite, sip.ResponseStatusOK, "fork-b")
	uac, err := dialog.NewUAC(invite, res200B)
	if err != nil {
		t.Fatalf("dialog.NewUAC() error = %v, want nil", err)
	}
	uas, err := dialog.NewUAS(invite, res200B)
	if err != nil {
		t.Fatalf("dialog.NewUAS() error = %v, want nil", err)
	}
	ack, err := uac.NewACK(invite, res200B)
	if err != nil {
		t.Fatalf("uac.NewACK() error = %v, want nil", err)
	}

	// The UAS remote baseline advances past the INVITE number; the ACK
	// still matches the original INVITE CSeq.
	if err := uas.ValidateRequest(newDiagReq(t, uas.ID(), sip.RequestMethodBye, 50)); err != nil {
		t.Fatalf("uas.ValidateRequest() error = %v, want nil", err)
	}
	if !uas.MatchACK(ack.Message()) {
		t.Fatal("uas.MatchACK() = false, want true after remote CSeq advanced")
	}

	uasID := uas.ID()
	badCSeqMethod := newDiagReq(t, uasID, sip.RequestMethodAck, testInviteSeq)
	badCSeqMethod.Headers.Set(&header.CSeq{SeqNum: testInviteSeq, Method: sip.RequestMethodBye})
	cases := []struct {
		name string
		d    *dialog.Dialog
		req  *sip.Request
	}{
		{"nil request", uas, nil},
		{
			"wrong Call-ID",
			uas,
			newDiagReq(t,
				dialog.ID{
					CallID:    "other@example.test",
					LocalTag:  uasID.LocalTag,
					RemoteTag: uasID.RemoteTag,
				},
				sip.RequestMethodAck,
				testInviteSeq,
			),
		},
		{"swapped tags", uas, newDiagReq(t, uac.ID(), sip.RequestMethodAck, testInviteSeq)},
		{"wrong CSeq number", uas, newDiagReq(t, uasID, sip.RequestMethodAck, testInviteSeq+1)},
		{"non-ACK method", uas, newDiagReq(t, uasID, sip.RequestMethodBye, testInviteSeq)},
		{"lowercase ack", uas, newDiagReq(t, uasID, "ack", testInviteSeq)},
		{"mismatched CSeq method", uas, badCSeqMethod},
		{"UAC dialog", uac, ack.Message()},
	}
	for _, tt := range cases {
		if tt.d.MatchACK(tt.req) {
			t.Errorf("%s: MatchACK() = true, want false", tt.name)
		}
	}

	// Early and terminated UAS dialogs do not recognize an ACK either.
	early, err := dialog.NewUAS(invite, newInviteRes(t, invite, sip.ResponseStatusRinging, "fork-b"))
	if err != nil {
		t.Fatalf("dialog.NewUAS(180) error = %v, want nil", err)
	}
	if early.MatchACK(ack.Message()) {
		t.Error("early uas.MatchACK() = true, want false")
	}
	uas.Terminate()
	if uas.MatchACK(ack.Message()) {
		t.Error("terminated uas.MatchACK() = true, want false")
	}

	// Recognition never touches sequence state.
	if got := uas.View().RemoteCSeq; got != 50 {
		t.Errorf("uas.View().RemoteCSeq = %d, want 50 (MatchACK mutates nothing)", got)
	}
}
