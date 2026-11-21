package dialog_test

import (
	"slices"
	"sync"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/dialog"
	"github.com/ghettovoice/gosip/sip/header"
)

// reqCSeqNum returns the envelope's CSeq number for assertions.
func reqCSeqNum(tb testing.TB, env *sip.RequestEnvelope) (*header.CSeq, uint) {
	tb.Helper()

	cseq, ok := env.Headers().CSeq()
	if !ok {
		tb.Fatal("request has no CSeq header")
	}
	return cseq, cseq.SeqNum
}

func TestDialog_NewRequest_LocalCSeq(t *testing.T) {
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

	// The UAC's local counter seeds from the sent INVITE; the remote side
	// is unset until the first inbound request.
	if got, want := uac.View().LocalCSeq, uint(testInviteSeq); got != want {
		t.Fatalf("uac.View().LocalCSeq = %d, want %d", got, want)
	}
	if got := uac.View().RemoteCSeq; got != 0 {
		t.Fatalf("uac.View().RemoteCSeq = %d, want 0 (unset)", got)
	}
	out, err := uac.NewRequest(sip.RequestMethodBye)
	if err != nil {
		t.Fatalf("uac.NewRequest(BYE) error = %v, want nil", err)
	}
	cseq, seq := reqCSeqNum(t, out)
	if seq != testInviteSeq+1 || cseq.Method != sip.RequestMethodBye {
		t.Errorf("uac first request CSeq = %d %v, want 43 BYE", seq, cseq.Method)
	}

	// The UAS holds the INVITE number as its remote baseline but numbers
	// its own requests from 1, independently.
	if got := uas.View().LocalCSeq; got != 0 {
		t.Fatalf("uas.View().LocalCSeq = %d, want 0 (unset)", got)
	}
	if got, want := uas.View().RemoteCSeq, uint(testInviteSeq); got != want {
		t.Fatalf("uas.View().RemoteCSeq = %d, want %d", got, want)
	}
	for want := uint(1); want <= 2; want++ {
		out, err := uas.NewRequest(sip.RequestMethodBye)
		if err != nil {
			t.Fatalf("uas.NewRequest(BYE) error = %v, want nil", err)
		}
		if cseq, seq := reqCSeqNum(t, out); seq != want || cseq.Method != sip.RequestMethodBye {
			t.Errorf("uas request CSeq = %d %v, want %d BYE", seq, cseq.Method, want)
		}
	}

	// Failed and refused builds consume no number.
	if _, err := uac.NewRequest(sip.RequestMethodBye, dialog.RequestOptions{Body: []byte("x")}); !errors.Is(err, dialog.ErrInvalidMessage) {
		t.Errorf("uac.NewRequest(body w/o Content-Type) error = %v, want %v", err, dialog.ErrInvalidMessage)
	}
	if _, err := uac.NewRequest(sip.RequestMethodAck); !errors.Is(err, dialog.ErrUnsupportedMethod) {
		t.Errorf("uac.NewRequest(ACK) error = %v, want %v", err, dialog.ErrUnsupportedMethod)
	}
	if got := uac.View().LocalCSeq; got != testInviteSeq+1 {
		t.Errorf("uac.View().LocalCSeq = %d, want 43 (refused builds consume nothing)", got)
	}
	uac.Terminate()
	if _, err := uac.NewRequest(sip.RequestMethodBye); !errors.Is(err, dialog.ErrTerminated) {
		t.Errorf("uac.NewRequest() on terminated dialog error = %v, want %v", err, dialog.ErrTerminated)
	}
	if got := uac.View().LocalCSeq; got != testInviteSeq+1 {
		t.Errorf("uac.View().LocalCSeq after termination = %d, want 43", got)
	}
}

func TestDialog_NewRequest_ConcurrentCSeq(t *testing.T) {
	t.Parallel()

	invite, _ := newInvite(t)
	d, err := dialog.NewUAC(invite, newInviteRes(t, invite, sip.ResponseStatusOK, "fork-b"))
	if err != nil {
		t.Fatalf("dialog.NewUAC() error = %v, want nil", err)
	}

	const n = 16
	methods := []sip.RequestMethod{
		sip.RequestMethodBye,
		sip.RequestMethodInfo,
		sip.RequestMethodOptions,
		sip.RequestMethodMessage,
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		envs []*sip.RequestEnvelope
	)
	for i := range n {
		wg.Go(func() {
			env, err := d.NewRequest(methods[i%len(methods)])
			if err != nil {
				t.Errorf("d.NewRequest(%v) error = %v, want nil", methods[i%len(methods)], err)
				return
			}
			mu.Lock()
			envs = append(envs, env)
			mu.Unlock()
		})
	}
	wg.Wait()

	if len(envs) != n {
		t.Fatalf("built %d requests, want %d", len(envs), n)
	}
	nums := make([]uint, 0, len(envs))
	for _, env := range envs {
		cseq, seq := reqCSeqNum(t, env)
		if cseq.Method != env.Method() {
			t.Errorf("CSeq method = %v, want request method %v", cseq.Method, env.Method())
		}
		nums = append(nums, seq)
	}
	slices.Sort(nums)
	for i, seq := range nums {
		if want := testInviteSeq + 1 + uint(i); seq != want {
			t.Fatalf("sorted CSeq numbers = %v, want consecutive 43..58", nums)
		}
	}
}

func TestDialog_ValidateRequest(t *testing.T) {
	t.Parallel()

	invite, _ := newInvite(t)
	res200B := newInviteRes(t, invite, sip.ResponseStatusOK, "fork-b")
	uas, err := dialog.NewUAS(invite, res200B)
	if err != nil {
		t.Fatalf("dialog.NewUAS() error = %v, want nil", err)
	}

	// The remote baseline seeds from the INVITE; a greater number
	// advances it, an equal retransmission changes nothing, and a lower
	// number is stale and leaves state untouched.
	stale49 := newDiagReq(t, uas.ID(), sip.RequestMethodBye, 49)
	bye50 := newDiagReq(t, uas.ID(), sip.RequestMethodBye, 50)
	if err := uas.ValidateRequest(bye50); err != nil {
		t.Fatalf("uas.ValidateRequest(seq 50) error = %v, want nil", err)
	}
	if got, want := uas.View().RemoteCSeq, uint(50); got != want {
		t.Fatalf("uas.View().RemoteCSeq = %d, want %d", got, want)
	}
	if err := uas.ValidateRequest(bye50); err != nil {
		t.Fatalf("uas.ValidateRequest(equal 50) error = %v, want nil", err)
	}
	if err := uas.ValidateRequest(stale49); !errors.Is(err, dialog.ErrCSeqOutOfOrder) {
		t.Errorf("uas.ValidateRequest(stale 49) error = %v, want %v", err, dialog.ErrCSeqOutOfOrder)
	}
	if got, want := uas.View().RemoteCSeq, uint(50); got != want {
		t.Errorf("uas.View().RemoteCSeq = %d, want %d", got, want)
	}

	// On a UAC dialog the remote baseline starts unset.
	uac, err := dialog.NewUAC(invite, res200B)
	if err != nil {
		t.Fatalf("dialog.NewUAC() error = %v, want nil", err)
	}
	// A CSeq method that does not exactly match the request line is
	// malformed, not a sequence event.
	badMethod := newDiagReq(t, uac.ID(), sip.RequestMethodBye, 900)
	badMethod.Headers.Set(&header.CSeq{SeqNum: 900, Method: sip.RequestMethodInfo})
	if err := uac.ValidateRequest(badMethod); !errors.Is(err, dialog.ErrInvalidMessage) {
		t.Errorf("uac.ValidateRequest(mismatched CSeq method) error = %v, want %v", err, dialog.ErrInvalidMessage)
	}
	// A well-formed request from another fork is not ours.
	if err := uac.ValidateRequest(newDiagReq(t, dialog.ID{CallID: testCallID, LocalTag: "alice", RemoteTag: "fork-x"}, sip.RequestMethodBye, 900)); !errors.Is(err, dialog.ErrMismatch) {
		t.Errorf("uac.ValidateRequest(foreign tag) error = %v, want %v", err, dialog.ErrMismatch)
	}
	// The first valid matched number initializes the baseline.
	if err := uac.ValidateRequest(newDiagReq(t, uac.ID(), sip.RequestMethodBye, 5)); err != nil {
		t.Fatalf("uac.ValidateRequest(seq 5) error = %v, want nil", err)
	}
	if got, want := uac.View().RemoteCSeq, uint(5); got != want {
		t.Fatalf("uac.View().RemoteCSeq = %d, want %d", got, want)
	}

	malformed := map[string]*sip.Request{
		"nil request": nil,
		"duplicate CSeq": func() *sip.Request {
			req := newDiagReq(t, uac.ID(), sip.RequestMethodBye, 6)
			req.Headers.Append(&header.CSeq{SeqNum: 6, Method: sip.RequestMethodBye})
			return req
		}(),
		"missing CSeq": func() *sip.Request {
			req := newDiagReq(t, uac.ID(), sip.RequestMethodBye, 6)
			req.Headers.Delete("CSeq")
			return req
		}(),
		"zero CSeq": func() *sip.Request {
			req := newDiagReq(t, uac.ID(), sip.RequestMethodBye, 6)
			req.Headers.Set(&header.CSeq{SeqNum: 0, Method: sip.RequestMethodBye})
			return req
		}(),
		"untyped CSeq": func() *sip.Request {
			req := newDiagReq(t, uac.ID(), sip.RequestMethodBye, 6)
			req.Headers[sip.CanonicHeaderName("CSeq")] = []sip.Header{header.MaxForwards(70)}
			return req
		}(),
	}
	for name, req := range malformed {
		if err := uac.ValidateRequest(req); !errors.Is(err, dialog.ErrInvalidMessage) {
			t.Errorf("uac.ValidateRequest(%s) error = %v, want %v", name, err, dialog.ErrInvalidMessage)
		}
	}

	// ACK and CANCEL carry their own sequence semantics and never enter
	// remote CSeq accounting.
	for _, mtd := range []sip.RequestMethod{sip.RequestMethodAck, "ack", sip.RequestMethodCancel, "Cancel"} {
		req := newDiagReq(t, uac.ID(), mtd, 6)
		if err := uac.ValidateRequest(req); !errors.Is(err, dialog.ErrUnsupportedMethod) {
			t.Errorf("uac.ValidateRequest(%v) error = %v, want %v", mtd, err, dialog.ErrUnsupportedMethod)
		}
	}
	if got := uac.View().RemoteCSeq; got != 5 {
		t.Errorf("uac.View().RemoteCSeq = %d, want 5 (ACK/CANCEL and malformed requests consume nothing)", got)
	}

	// A valid matched request after termination is refused.
	uac.Terminate()
	if err := uac.ValidateRequest(newDiagReq(t, uac.ID(), sip.RequestMethodBye, 6)); !errors.Is(err, dialog.ErrTerminated) {
		t.Errorf("uac.ValidateRequest() on terminated dialog error = %v, want %v", err, dialog.ErrTerminated)
	}
}
