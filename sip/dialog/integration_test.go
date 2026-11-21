package dialog_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/testutil/siptest"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/dialog"
	"github.com/ghettovoice/gosip/sip/header"
)

// TestDialog_LoopbackExchange drives a complete INVITE/2xx/ACK/BYE
// exchange between two loopback elements through the public dialog and
// element APIs.
func TestDialog_LoopbackExchange(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)

	cln, clnAddr := siptest.NewLoopbackUDPElement(t, sip.UDPMetadata())
	srv, srvAddr := siptest.NewLoopbackUDPElement(t, sip.UDPMetadata())

	// Registration observers prove which transactions exist; nothing may
	// create one for ACK.
	var (
		txMu      sync.Mutex
		clnClnTxs []sip.RequestMethod
		clnSrvTxs []sip.RequestMethod
		srvClnTxs []sip.RequestMethod
		srvSrvTxs []sip.RequestMethod
	)
	recTx := func(dst *[]sip.RequestMethod) sip.ClientTransactionHandlerFunc {
		return func(_ context.Context, tx sip.ClientTransaction) {
			txMu.Lock()
			*dst = append(*dst, tx.Request().Method())
			txMu.Unlock()
		}
	}
	recSrvTx := func(dst *[]sip.RequestMethod) sip.ServerTransactionHandlerFunc {
		return func(_ context.Context, tx sip.ServerTransaction) {
			txMu.Lock()
			*dst = append(*dst, tx.Request().Method())
			txMu.Unlock()
		}
	}
	cln.UseComponent(recTx(&clnClnTxs))
	cln.UseComponent(recSrvTx(&clnSrvTxs))
	srv.UseComponent(recTx(&srvClnTxs))
	srv.UseComponent(recSrvTx(&srvSrvTxs))

	// The server captures in-dialog traffic through an inbound request
	// interceptor registered before any traffic. INVITE/ACK/BYE are
	// claimed; anything else is forwarded untouched.
	inReqs := make(chan *sip.RequestEnvelope, 8)
	var overflow atomic.Int32
	srv.UseComponent(sip.InboundRequestInterceptorFunc(
		func(ctx context.Context, next sip.RequestReceiver, req *sip.RequestEnvelope) error {
			if mtd := req.Method(); mtd == sip.RequestMethodInvite ||
				mtd == sip.RequestMethodAck || mtd == sip.RequestMethodBye {
				select {
				case inReqs <- req:
				default:
					overflow.Add(1)
				}
				return nil
			}
			return next.RecvRequest(ctx, req)
		},
	))

	// A dedicated counter observes every outbound response carrying an
	// ACK CSeq — answering a stateless ACK is forbidden — while finals
	// are recorded separately to prove the two expected answers;
	// provisionals are filtered out.
	outFinals := make(chan *sip.Response, 8)
	var ackRess atomic.Int32
	srv.UseComponent(sip.OutboundResponseInterceptorFunc(
		func(ctx context.Context, next sip.ResponseSender, res *sip.ResponseEnvelope, opts ...sip.SendResponseOptions) error {
			if cseq, ok := res.Headers().CSeq(); ok && cseq.Method.Equal(sip.RequestMethodAck) {
				ackRess.Add(1)
			}
			if res.Status().IsFinal() {
				select {
				case outFinals <- res.Message():
				default:
					overflow.Add(1)
				}
			}
			return next.SendResponse(ctx, res, opts...)
		},
	))

	nextInbound := func() *sip.RequestEnvelope {
		t.Helper()
		select {
		case req := <-inReqs:
			return req
		case <-ctx.Done():
			t.Fatalf("timed out waiting for inbound request: %v", ctx.Err())
			return nil
		}
	}
	nextFinal := func() *sip.Response {
		t.Helper()
		select {
		case res := <-outFinals:
			return res
		case <-ctx.Done():
			t.Fatalf("timed out waiting for outbound final response: %v", ctx.Err())
			return nil
		}
	}

	// The INVITE targets the server's Contact address; the client's
	// Contact is its own listener address.
	aliceURI := &sip.URI{
		User: sip.MakeUserInfo("alice"),
		Addr: sip.MakeHostPortAddr("127.0.0.1", clnAddr.Port()),
	}
	bobURI := &sip.URI{
		User: sip.MakeUserInfo("bob"),
		Addr: sip.MakeHostPortAddr("127.0.0.1", srvAddr.Port()),
	}
	invite, err := sip.NewRequest(
		sip.RequestMethodInvite, bobURI, aliceURI, bobURI,
		sip.RequestOptions{CallID: testCallID, LocalTag: "alice", SeqNum: testInviteSeq, RPort: true},
	)
	if err != nil {
		t.Fatalf("sip.NewRequest(INVITE) error = %v, want nil", err)
	}
	invite.Headers.Set(header.Contact{{URI: aliceURI}})

	// Raised T1 suppresses client retransmission timers for the exchange
	// duration; the test drives every hop explicitly.
	timing := sip.TimingConfig{T1: 30 * time.Second}
	type txResult struct {
		tx  sip.ClientTransaction
		err error
	}
	invResult := make(chan txResult, 1)
	go func() {
		tx, err := cln.SendRequestStateful(ctx, sip.NewRequestEnvelope(invite),
			sip.SendRequestStatefulOptions{Timing: timing})
		invResult <- txResult{tx, err}
	}()

	// Server: claim the INVITE, create the UAS dialog before the response
	// is sent, then answer statefully.
	invEnv := nextInbound()
	invMsg := invEnv.Message()
	wantUAC := dialog.ID{CallID: testCallID, LocalTag: "alice", RemoteTag: "bob"}
	wantUAS := dialog.ID{CallID: testCallID, LocalTag: "bob", RemoteTag: "alice"}
	res200, err := invEnv.NewResponse(sip.ResponseStatusOK, sip.ResponseOptions{
		LocalTag: "bob",
		Headers:  sip.Headers{"Contact": {header.Contact{{URI: bobURI}}}},
	})
	if err != nil {
		t.Fatalf("invEnv.NewResponse(200) error = %v, want nil", err)
	}
	uas, err := dialog.NewUAS(invMsg, res200.Message())
	if err != nil {
		t.Fatalf("dialog.NewUAS() error = %v, want nil", err)
	}
	if _, err := srv.SendResponseStateful(ctx, invEnv, res200,
		sip.SendResponseStatefulOptions{}); err != nil {
		t.Fatalf("server.SendResponseStateful(200) error = %v, want nil", err)
	}
	if got := nextFinal().Status; got != sip.ResponseStatusOK {
		t.Fatalf("server outbound INVITE final = %v, want 200", got)
	}

	// Client: the stateful send returned the transaction that owns the
	// exchange; the actual wire INVITE and the single final come from it.
	invRes := <-invResult
	if invRes.err != nil {
		t.Fatalf("client.SendRequestStateful(INVITE) error = %v, want nil", invRes.err)
	}
	sentInvite := invRes.tx.Request().Message()
	finalRes := invRes.tx.LastResponse().Message()
	uac, err := dialog.NewUAC(sentInvite, finalRes)
	if err != nil {
		t.Fatalf("dialog.NewUAC() error = %v, want nil", err)
	}
	if got := uac.ID(); got != wantUAC {
		t.Fatalf("uac.ID() = %+v, want %+v", got, wantUAC)
	}
	if got := uas.ID(); got != wantUAS {
		t.Fatalf("uas.ID() = %+v, want %+v", got, wantUAS)
	}

	// The stateless ACK rides the dialog route set straight to the
	// callee Contact; the server recognizes it without sequence changes.
	ack, err := uac.NewACK(sentInvite, finalRes)
	if err != nil {
		t.Fatalf("uac.NewACK() error = %v, want nil", err)
	}
	if err := cln.SendRequest(ctx, ack); err != nil {
		t.Fatalf("client.SendRequest(ACK) error = %v, want nil", err)
	}
	ackMsg := nextInbound().Message()
	if cseq, ok := ackMsg.Headers.CSeq(); !ok || cseq.SeqNum != testInviteSeq || cseq.Method != sip.RequestMethodAck {
		t.Fatalf("inbound ACK CSeq = %v, want %d ACK", cseq, testInviteSeq)
	}
	if !uas.MatchACK(ackMsg) {
		t.Fatal("uas.MatchACK() = false, want true")
	}
	if got, want := uas.View().RemoteCSeq, uint(testInviteSeq); got != want {
		t.Fatalf("uas.View().RemoteCSeq = %d, want %d (ACK consumed nothing)", got, want)
	}

	// Ordinary BYE over a second stateful transaction. The loopback
	// transports advertise a host-only public address, so the BYE asks
	// for rport; the server then routes its final back to the client's
	// real source port, same as for the INVITE.
	bye, err := uac.NewRequest(sip.RequestMethodBye)
	if err != nil {
		t.Fatalf("uac.NewRequest(BYE) error = %v, want nil", err)
	}
	bye.WithMessage(func(r *sip.Request) {
		if via, ok := r.Headers.FirstVia(); ok && via != nil {
			if via.Params == nil {
				via.Params = make(sip.Values)
			}
			via.Params.Set("rport", "")
		}
	})
	byeResult := make(chan txResult, 1)
	go func() {
		tx, err := cln.SendRequestStateful(ctx, bye, sip.SendRequestStatefulOptions{Timing: timing})
		byeResult <- txResult{tx, err}
	}()

	byeEnv := nextInbound()
	byeMsg := byeEnv.Message()
	if cseq, ok := byeMsg.Headers.CSeq(); !ok || cseq.SeqNum != 43 || cseq.Method != sip.RequestMethodBye {
		t.Fatalf("inbound BYE CSeq = %v, want 43 BYE", cseq)
	}
	if id, err := dialog.IDFromRequest(byeMsg); err != nil {
		t.Fatalf("dialog.IDFromRequest(BYE) error = %v, want nil", err)
	} else if id != wantUAS {
		t.Fatalf("dialog.IDFromRequest(BYE) = %+v, want %+v", id, wantUAS)
	}
	if err := uas.ValidateRequest(byeMsg); err != nil {
		t.Fatalf("uas.ValidateRequest(BYE) error = %v, want nil", err)
	}
	bye200, err := byeEnv.NewResponse(sip.ResponseStatusOK)
	if err != nil {
		t.Fatalf("byeEnv.NewResponse(200) error = %v, want nil", err)
	}
	if _, err := srv.SendResponseStateful(ctx, byeEnv, bye200,
		sip.SendResponseStatefulOptions{}); err != nil {
		t.Fatalf("server.SendResponseStateful(BYE 200) error = %v, want nil", err)
	}
	byeFinal := nextFinal()
	if got := byeFinal.Status; got != sip.ResponseStatusOK {
		t.Fatalf("server outbound BYE final = %v, want 200", got)
	}
	if cseq, ok := byeFinal.Headers.CSeq(); !ok || cseq.SeqNum != 43 || cseq.Method != sip.RequestMethodBye {
		t.Fatalf("BYE final CSeq = %v, want 43 BYE", cseq)
	}

	byeRes := <-byeResult
	if byeRes.err != nil {
		t.Fatalf("client.SendRequestStateful(BYE) error = %v, want nil", byeRes.err)
	}
	if got := byeRes.tx.LastResponse().Status(); got != sip.ResponseStatusOK {
		t.Errorf("BYE final status = %v, want 200", got)
	}
	if cseq, ok := byeRes.tx.LastResponse().Headers().CSeq(); !ok || cseq.SeqNum != 43 || cseq.Method != sip.RequestMethodBye {
		t.Errorf("BYE final wire CSeq = %v, want 43 BYE", cseq)
	}
	if got, want := uas.View().RemoteCSeq, uint(43); got != want {
		t.Errorf("uas.View().RemoteCSeq = %d, want %d", got, want)
	}

	// No outbound response ever carried an ACK CSeq: the two finals
	// above were observed in order through the interceptor channel, so
	// any other final would already be queued.
	select {
	case extra := <-outFinals:
		t.Fatalf("unexpected extra outbound final with CSeq %v", extra.Headers)
	default:
	}
	if got := ackRess.Load(); got != 0 {
		t.Errorf("outbound responses with ACK CSeq = %d, want 0", got)
	}

	// Explicit consumer teardown is idempotent on both ends.
	for name, d := range map[string]*dialog.Dialog{"uac": uac, "uas": uas} {
		if changed := d.Terminate(); !changed {
			t.Errorf("%s.Terminate() = false, want true", name)
		}
		if changed := d.Terminate(); changed {
			t.Errorf("%s.Terminate() again = true, want false (idempotent)", name)
		}
		if got := d.View().State; got != dialog.StateTerminated {
			t.Errorf("%s.View().State = %v, want %v", name, got, dialog.StateTerminated)
		}
	}
	if got := overflow.Load(); got != 0 {
		t.Errorf("interceptor queue overflow count = %d, want 0", got)
	}

	txMu.Lock()
	gotTxs := map[string][]sip.RequestMethod{
		"client client transactions": clnClnTxs,
		"client server transactions": clnSrvTxs,
		"server client transactions": srvClnTxs,
		"server server transactions": srvSrvTxs,
	}
	txMu.Unlock()
	for name, got := range gotTxs {
		var want []sip.RequestMethod
		switch name {
		case "client client transactions", "server server transactions":
			want = []sip.RequestMethod{sip.RequestMethodInvite, sip.RequestMethodBye}
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("%s mismatch (-want +got):\n%s", name, diff)
		}
	}
}
