package dialog_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/dialog"
	"github.com/ghettovoice/gosip/sip/header"
)

func TestDialog_Lifecycle(t *testing.T) {
	t.Parallel()

	invite, callerURI := newInvite(t)
	res180B := newInviteRes(t, invite, sip.ResponseStatusRinging, "fork-b")
	res200B := newInviteRes(t, invite, sip.ResponseStatusOK, "fork-b")
	res200C := newInviteRes(t, invite, sip.ResponseStatusOK, "fork-c")

	wantUAC := dialog.ID{CallID: testCallID, LocalTag: "alice", RemoteTag: "fork-b"}
	wantUAS := dialog.ID{CallID: testCallID, LocalTag: "fork-b", RemoteTag: "alice"}
	wantSeq := uint(testInviteSeq)

	// A direct tagged 2xx establishes a confirmed UAC dialog.
	uac, err := dialog.NewUAC(invite, res200B)
	if err != nil {
		t.Fatalf("dialog.NewUAC() error = %v, want nil", err)
	}
	if got := uac.ID(); got != wantUAC {
		t.Fatalf("uac.ID() = %+v, want %+v", got, wantUAC)
	}
	uacView := uac.View()
	if uacView.State != dialog.StateConfirmed {
		t.Fatalf("uac.View().State = %v, want %v", uacView.State, dialog.StateConfirmed)
	}
	if uacView.Role != dialog.RoleUAC {
		t.Fatalf("uac.View().Role = %v, want %v", uacView.Role, dialog.RoleUAC)
	}
	if uacView.LocalCSeq != wantSeq {
		t.Fatalf("uac.View().LocalCSeq = %d, want %d", uacView.LocalCSeq, wantSeq)
	}
	if uacView.RemoteCSeq != 0 {
		t.Fatalf("uac.View().RemoteCSeq = %d, want 0 (unset)", uacView.RemoteCSeq)
	}
	if tag, _ := uacView.Local.Tag(); tag != "alice" {
		t.Fatalf("uac.View().Local tag = %q, want %q", tag, "alice")
	}
	if tag, _ := uacView.Remote.Tag(); tag != "fork-b" {
		t.Fatalf("uac.View().Remote tag = %q, want %q", tag, "fork-b")
	}

	// The same exchange establishes the reversed UAS identity, confirmed.
	uas, err := dialog.NewUAS(invite, res200B)
	if err != nil {
		t.Fatalf("dialog.NewUAS() error = %v, want nil", err)
	}
	if got := uas.ID(); got != wantUAS {
		t.Fatalf("uas.ID() = %+v, want %+v", got, wantUAS)
	}
	uasView := uas.View()
	if uasView.State != dialog.StateConfirmed || uasView.Role != dialog.RoleUAS {
		t.Fatalf("uas.View() = %+v, want state %v and role %v", uasView, dialog.StateConfirmed, dialog.RoleUAS)
	}
	if uasView.RemoteCSeq != wantSeq {
		t.Fatalf("uas.View().RemoteCSeq = %d, want %d", uasView.RemoteCSeq, wantSeq)
	}
	if uasView.LocalCSeq != 0 {
		t.Fatalf("uas.View().LocalCSeq = %d, want 0 (unset)", uasView.LocalCSeq)
	}
	if tag, _ := uasView.Local.Tag(); tag != "fork-b" {
		t.Fatalf("uas.View().Local tag = %q, want %q", tag, "fork-b")
	}

	// A tagged provisional on the same leg establishes an early dialog.
	early, err := dialog.NewUAC(invite, res180B)
	if err != nil {
		t.Fatalf("dialog.NewUAC(180) error = %v, want nil", err)
	}
	if got := early.View().State; got != dialog.StateEarly {
		t.Fatalf("early.View().State = %v, want %v", got, dialog.StateEarly)
	}
	if got := early.ID(); got != wantUAC {
		t.Fatalf("early.ID() = %+v, want %+v", got, wantUAC)
	}

	// The matching 2xx confirms the early dialog exactly once; a repeated
	// 2xx and a late same-leg provisional are no-ops.
	if changed, err := early.Confirm(res200B); err != nil || !changed {
		t.Fatalf("early.Confirm(2xx) = %v, %v, want true, nil", changed, err)
	}
	confirmedView := early.View()
	if confirmedView.State != dialog.StateConfirmed {
		t.Fatalf("early.View().State after Confirm = %v, want %v", confirmedView.State, dialog.StateConfirmed)
	}
	if changed, err := early.Confirm(res200B); err != nil || changed {
		t.Fatalf("early.Confirm(duplicate 2xx) = %v, %v, want false, nil", changed, err)
	}
	if changed, err := early.Confirm(res180B); err != nil || changed {
		t.Fatalf("early.Confirm(late provisional) = %v, %v, want false, nil", changed, err)
	}
	assertSameView(t, confirmedView, early.View())

	// A well-formed response from another fork does not match this dialog
	// and commits nothing.
	if _, err := early.Confirm(res200C); !errors.Is(err, dialog.ErrMismatch) {
		t.Fatalf("early.Confirm(fork-c 2xx) error = %v, want dialog.ErrMismatch", err)
	}
	assertSameView(t, confirmedView, early.View())

	// The other fork is an independent confirmed dialog.
	forkC, err := dialog.NewUAC(invite, res200C)
	if err != nil {
		t.Fatalf("dialog.NewUAC(fork-c 2xx) error = %v, want nil", err)
	}
	wantForkC := dialog.ID{CallID: testCallID, LocalTag: "alice", RemoteTag: "fork-c"}
	if got := forkC.ID(); got != wantForkC {
		t.Fatalf("forkC.ID() = %+v, want %+v", got, wantForkC)
	}
	if got := forkC.View().State; got != dialog.StateConfirmed {
		t.Fatalf("forkC.View().State = %v, want %v", got, dialog.StateConfirmed)
	}

	// Termination is idempotent, sends nothing, and is local to the object.
	if changed := early.Terminate(); !changed {
		t.Fatal("early.Terminate() = false, want true")
	}
	if changed := early.Terminate(); changed {
		t.Fatal("early.Terminate() = true, want false")
	}
	if got := early.View().State; got != dialog.StateTerminated {
		t.Fatalf("early.View().State = %v, want %v", got, dialog.StateTerminated)
	}
	if got := early.ID(); got != wantUAC {
		t.Fatalf("early.ID() after Terminate = %+v, want %+v", got, wantUAC)
	}
	if _, err := early.Confirm(res200B); !errors.Is(err, dialog.ErrTerminated) {
		t.Fatalf("early.Confirm(after terminate) error = %v, want dialog.ErrTerminated", err)
	}
	if got := forkC.View().State; got != dialog.StateConfirmed {
		t.Fatalf("forkC.View().State = %v, want %v (sibling unaffected)", got, dialog.StateConfirmed)
	}
	if changed := forkC.Terminate(); !changed {
		t.Fatal("forkC.Terminate() = false, want true (sibling unaffected)")
	}

	// In-dialog requests and the establishing response map back to the
	// role-relative dialog identity.
	bye := newInboundReq(t, "fork-b", sip.RequestMethodBye, 7)
	if got, err := dialog.IDFromRequest(bye); err != nil {
		t.Fatalf("dialog.IDFromRequest() error = %v, want nil", err)
	} else if got != wantUAC {
		t.Fatalf("dialog.IDFromRequest() = %+v, want %+v", got, wantUAC)
	}
	if got, err := dialog.IDFromResponse(res200B); err != nil {
		t.Fatalf("dialog.IDFromResponse() error = %v, want nil", err)
	} else if got != wantUAC {
		t.Fatalf("dialog.IDFromResponse() = %+v, want %+v", got, wantUAC)
	}

	// Tags compare byte for byte: a case-only change is another dialog.
	cased, err := dialog.IDFromRequest(newInboundReq(t, "Fork-B", sip.RequestMethodBye, 7))
	if err != nil {
		t.Fatalf("dialog.IDFromRequest(case changed) error = %v, want nil", err)
	}
	if cased == wantUAC {
		t.Fatalf("dialog.IDFromRequest(case changed) = %+v, want an ID different from %+v", cased, wantUAC)
	}

	// Mutating the input identity URI after construction cannot leak into
	// the dialog: parties were snapshotted at creation time.
	before := uac.View()
	callerURI.User = sip.MakeUserInfo("mallory")
	after := uac.View()
	if got := after.Local.URI; got == callerURI {
		t.Fatal("uac.View().Local.URI aliases the mutated input URI")
	}
	if !after.Local.Equal(before.Local) {
		t.Fatalf("uac.View().Local after input URI mutation = %+v, want %+v", after.Local, before.Local)
	}

	// Mutating a returned view cannot leak back into the dialog either.
	out := uac.View()
	out.Local.Params.Set("tag", "tampered")
	remoteURI, ok := out.Remote.URI.(*sip.URI)
	if !ok {
		t.Fatalf("uac.View().Remote.URI type = %T, want *sip.URI", out.Remote.URI)
	}
	remoteURI.Addr = sip.MakeHostAddr("evil.example.test")
	fresh := uac.View()
	if !fresh.Local.Equal(before.Local) || !fresh.Remote.Equal(before.Remote) {
		t.Fatalf("uac.View() after mutating a returned view = %+v, want %+v", fresh, before)
	}
	if tag, _ := fresh.Local.Tag(); tag != "alice" {
		t.Fatalf("uac.View().Local tag after mutation = %q, want %q", tag, "alice")
	}
}

func TestDialog_EstablishmentValidation(t *testing.T) {
	t.Parallel()

	invite, _ := newInvite(t)
	res200B := newInviteRes(t, invite, sip.ResponseStatusOK, "fork-b")

	// An early dialog is the fixed endpoint for rejected confirmations:
	// every rejected Confirm must leave its whole view untouched.
	early, err := dialog.NewUAC(invite, newInviteRes(t, invite, sip.ResponseStatusRinging, "fork-b"))
	if err != nil {
		t.Fatalf("dialog.NewUAC(180) error = %v, want nil", err)
	}

	t.Run("request", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			build func(testing.TB) *sip.Request
			want  error
		}{
			{"nil request", func(testing.TB) *sip.Request { return nil }, dialog.ErrInvalidMessage},
			{
				"non-INVITE method",
				invCase(func(req *sip.Request) {
					req.Method = sip.RequestMethodBye
					setCSeqMethod(sip.RequestMethodBye)(req.Headers)
				}),
				dialog.ErrInvalidMessage,
			},
			{
				"lowercase method",
				invCase(func(req *sip.Request) {
					req.Method = "invite"
					setCSeqMethod("invite")(req.Headers)
				}),
				dialog.ErrInvalidMessage,
			},
			{"tagged initial To", invHdrs(setToTag("peer")), dialog.ErrInvalidMessage},
			{"missing Call-ID", invHdrs(delHdr("Call-ID")), dialog.ErrInvalidMessage},
			{"duplicate Call-ID", invHdrs(dupCallID), dialog.ErrInvalidMessage},
			{"whitespace Call-ID", invHdrs(setCallID(" ")), dialog.ErrInvalidMessage},
			{"missing From", invHdrs(delHdr("From")), dialog.ErrInvalidMessage},
			{"duplicate From", invHdrs(dupFromHdr), dialog.ErrInvalidMessage},
			{"missing From tag", invHdrs(dropFromTag), dialog.ErrInvalidMessage},
			{"empty From tag", invHdrs(setFromTag("")), dialog.ErrInvalidMessage},
			{"non-token From tag", invHdrs(setFromTag("bad tag")), dialog.ErrInvalidMessage},
			{"duplicate From tag", invHdrs(dupFromTag), dialog.ErrInvalidMessage},
			{"missing CSeq", invHdrs(delHdr("CSeq")), dialog.ErrInvalidMessage},
			{"duplicate CSeq", invHdrs(dupCSeq), dialog.ErrInvalidMessage},
			{"zero CSeq", invHdrs(setCSeqNum(0)), dialog.ErrInvalidMessage},
			{"overflow CSeq", invHdrs(setCSeqNum(1 << 31)), dialog.ErrInvalidMessage},
			{"non-INVITE CSeq", invHdrs(setCSeqMethod(sip.RequestMethodBye)), dialog.ErrInvalidMessage},
			{"wrong-typed CSeq", invHdrs(setAnyCSeq), dialog.ErrInvalidMessage},
			{"wrong-typed Via", invHdrs(setAnyVia), dialog.ErrInvalidMessage},
			{"empty Via", invHdrs(setEmptyVia), dialog.ErrInvalidMessage},
			{"missing Contact", invHdrs(delHdr("Contact")), dialog.ErrInvalidMessage},
			{"wildcard Contact", invHdrs(setContact(header.Contact{})), dialog.ErrInvalidMessage},
			{"wildcard plus valid Contact", invHdrs(appendContact(header.Contact{})), dialog.ErrInvalidMessage},
			{"non-SIP Contact", invHdrs(setContact(telContact)), dialog.ErrInvalidMessage},
			{"nil SIP URI Contact", invHdrs(setContact(nilURIContact)), dialog.ErrInvalidMessage},
			{"empty SIP URI Contact", invHdrs(setContact(emptyURIContact)), dialog.ErrInvalidMessage},
			{"two Contacts grouped", invHdrs(twoContacts), dialog.ErrInvalidMessage},
			{"two Contacts split", invHdrs(splitContacts), dialog.ErrInvalidMessage},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				req := tt.build(t)
				if _, err := dialog.NewUAC(req, res200B); !errors.Is(err, tt.want) {
					t.Errorf("dialog.NewUAC() error = %v, want %v", err, tt.want)
				}
				if _, err := dialog.NewUAS(req, res200B); !errors.Is(err, tt.want) {
					t.Errorf("dialog.NewUAS() error = %v, want %v", err, tt.want)
				}
			})
		}
	})

	t.Run("response", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			build func(testing.TB, *sip.Request) *sip.Response
			want  error
		}{
			{"nil response", func(testing.TB, *sip.Request) *sip.Response { return nil }, dialog.ErrInvalidMessage},
			{"100 Trying", resCase(sip.ResponseStatusTrying, nil), dialog.ErrNotEstablishing},
			{"untagged provisional", resCase(183, dropToTag), dialog.ErrNotEstablishing},
			{"non-2xx final", resCase(486, nil), dialog.ErrNotEstablishing},
			{"missing To tag on 2xx", resCase(sip.ResponseStatusOK, dropToTag), dialog.ErrInvalidMessage},
			{"empty To tag", resCase(sip.ResponseStatusOK, setToTag("")), dialog.ErrInvalidMessage},
			{"non-token To tag", resCase(sip.ResponseStatusOK, setToTag("bad tag")), dialog.ErrInvalidMessage},
			{"duplicate To tag", resCase(sip.ResponseStatusOK, dupToTag), dialog.ErrInvalidMessage},
			{"missing From", resCase(sip.ResponseStatusOK, delHdr("From")), dialog.ErrInvalidMessage},
			{"duplicate From", resCase(sip.ResponseStatusOK, dupFromHdr), dialog.ErrInvalidMessage},
			{"missing Call-ID", resCase(sip.ResponseStatusOK, delHdr("Call-ID")), dialog.ErrInvalidMessage},
			{"duplicate Call-ID", resCase(sip.ResponseStatusOK, dupCallID), dialog.ErrInvalidMessage},
			{"whitespace Call-ID", resCase(sip.ResponseStatusOK, setCallID("call 1@x")), dialog.ErrInvalidMessage},
			{"wrong-typed Call-ID", resCase(sip.ResponseStatusOK, setAnyCallID), dialog.ErrInvalidMessage},
			{"missing CSeq", resCase(sip.ResponseStatusOK, delHdr("CSeq")), dialog.ErrInvalidMessage},
			{"duplicate CSeq", resCase(sip.ResponseStatusOK, dupCSeq), dialog.ErrInvalidMessage},
			{"zero CSeq", resCase(sip.ResponseStatusOK, setCSeqNum(0)), dialog.ErrInvalidMessage},
			{"overflow CSeq", resCase(sip.ResponseStatusOK, setCSeqNum(1<<31)), dialog.ErrInvalidMessage},
			{"non-INVITE CSeq", resCase(sip.ResponseStatusOK, setCSeqMethod(sip.RequestMethodBye)), dialog.ErrInvalidMessage},
			{"wrong-typed CSeq", resCase(sip.ResponseStatusOK, setAnyCSeq), dialog.ErrInvalidMessage},
			{"wrong-typed Via", resCase(sip.ResponseStatusOK, setAnyVia), dialog.ErrInvalidMessage},
			{"empty Via", resCase(sip.ResponseStatusOK, setEmptyVia), dialog.ErrInvalidMessage},
			{"missing Contact", resCase(sip.ResponseStatusOK, delHdr("Contact")), dialog.ErrInvalidMessage},
			{"wildcard Contact", resCase(sip.ResponseStatusOK, setContact(header.Contact{})), dialog.ErrInvalidMessage},
			{"wildcard plus valid Contact", resCase(sip.ResponseStatusOK, appendContact(header.Contact{})), dialog.ErrInvalidMessage},
			{"non-SIP Contact", resCase(sip.ResponseStatusOK, setContact(telContact)), dialog.ErrInvalidMessage},
			{"nil SIP URI Contact", resCase(sip.ResponseStatusOK, setContact(nilURIContact)), dialog.ErrInvalidMessage},
			{"empty SIP URI Contact", resCase(sip.ResponseStatusOK, setContact(emptyURIContact)), dialog.ErrInvalidMessage},
			{"two Contacts grouped", resCase(sip.ResponseStatusOK, twoContacts), dialog.ErrInvalidMessage},
			{"two Contacts split", resCase(sip.ResponseStatusOK, splitContacts), dialog.ErrInvalidMessage},
			{"changed Call-ID", resCase(sip.ResponseStatusOK, setCallID("call-2@example.test")), dialog.ErrMismatch},
			{"changed From tag", resCase(sip.ResponseStatusOK, setFromTag("mallory")), dialog.ErrMismatch},
			{"changed CSeq number", resCase(sip.ResponseStatusOK, setCSeqNum(testInviteSeq+1)), dialog.ErrMismatch},
			{"changed Via branch", resCase(sip.ResponseStatusOK, newBranch), dialog.ErrMismatch},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				res := tt.build(t, invite)
				if _, err := dialog.NewUAC(invite, res); !errors.Is(err, tt.want) {
					t.Errorf("dialog.NewUAC() error = %v, want %v", err, tt.want)
				}
				if _, err := dialog.NewUAS(invite, res); !errors.Is(err, tt.want) {
					t.Errorf("dialog.NewUAS() error = %v, want %v", err, tt.want)
				}

				before := early.View()
				if changed, err := early.Confirm(res); changed || !errors.Is(err, tt.want) {
					t.Fatalf("early.Confirm() = (%v, %v), want (false, %v)", changed, err, tt.want)
				}
				if diff := cmp.Diff(before, early.View(), viewCmpOpts); diff != "" {
					t.Errorf("early.View() mismatch (-want +got):\n%s", diff)
				}
			})
		}
	})
}
