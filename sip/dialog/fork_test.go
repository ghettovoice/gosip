package dialog_test

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/testutil/siptest"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/dialog"
	"github.com/ghettovoice/gosip/sip/header"
)

// captureClientTransp is a minimal [sip.ClientTransport] that records
// the requests the client transaction hands it without sending anything.
type captureClientTransp struct {
	mu   sync.Mutex
	reqs []*sip.RequestEnvelope
}

func (tp *captureClientTransp) Metadata() sip.TransportMetadata { return sip.UDPMetadata() }

func (tp *captureClientTransp) SendRequest(_ context.Context, req *sip.RequestEnvelope, _ ...sip.SendRequestOptions) error {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.reqs = append(tp.reqs, req)
	return nil
}

func (tp *captureClientTransp) last(tb testing.TB) *sip.RequestEnvelope {
	tb.Helper()
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if len(tp.reqs) == 0 {
		tb.Fatal("capture transport recorded no request")
	}
	return tp.reqs[len(tp.reqs)-1]
}

// udpListener opens a raw UDP socket on a kernel-assigned loopback port.
func udpListener(tb testing.TB) *net.UDPConn {
	tb.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		tb.Fatalf("net.ListenUDP() error = %v, want nil", err)
	}
	tb.Cleanup(func() { conn.Close() })
	return conn
}

// lisRouteURI is the loose Route hop pointing at the listener.
func lisRouteURI(tb testing.TB, conn *net.UDPConn) *sip.URI {
	tb.Helper()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		tb.Fatalf("listener addr type = %T, want *net.UDPAddr", conn.LocalAddr())
	}
	return &sip.URI{
		Addr:   sip.MakeHostPortAddr("127.0.0.1", uint16(addr.Port)),
		Params: make(header.Values).Set("lr", ""),
	}
}

// readWireReq reads one datagram and parses it as a SIP request.
func readWireReq(tb testing.TB, conn *net.UDPConn) *sip.Request {
	tb.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		tb.Fatalf("conn.SetReadDeadline() error = %v, want nil", err)
	}
	buf := make([]byte, 1<<16)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		tb.Fatalf("conn.ReadFromUDP() error = %v, want a datagram", err)
	}
	msg, err := sip.ParseMessage(buf[:n])
	if err != nil {
		tb.Fatalf("sip.ParseMessage() error = %v, want nil", err)
	}
	req, ok := msg.(*sip.Request)
	if !ok {
		tb.Fatalf("parsed message type = %T, want *sip.Request", msg)
	}
	return req
}

// forkRes builds a tagged 2xx to the captured INVITE whose Contact
// is the fork's user at callee.example.test and whose Record-Route is a
// loose hop at the fork's listener.
func forkRes(tb testing.TB, invite *sip.Request, tag, user string, hop *sip.URI) *sip.Response {
	tb.Helper()
	res, err := invite.NewResponse(sip.ResponseStatusOK, sip.ResponseOptions{
		LocalTag: tag,
		Headers: sip.Headers{
			"Contact": {
				header.Contact{{URI: &sip.URI{
					User: sip.MakeUserInfo(user),
					Addr: sip.MakeHostAddr("callee.example.test"),
				}}},
			},
			"Record-Route": {
				header.RecordRoute{{URI: hop}},
			},
		},
	})
	if err != nil {
		tb.Fatalf("invite.NewResponse() error = %v, want nil", err)
	}
	return res
}

func TestDialog_ForkedInviteACKReplay(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	elm, _ := siptest.NewLoopbackUDPElement(t, sip.UDPMetadata())
	lisB := udpListener(t)
	lisC := udpListener(t)

	// Record every client transaction the element registers; the
	// stateless ACKs must not add any.
	var (
		txMu      sync.Mutex
		txMethods []sip.RequestMethod
	)
	unbindTxHndlr := elm.UseComponent(sip.ClientTransactionHandlerFunc(
		func(_ context.Context, tx sip.ClientTransaction) {
			txMu.Lock()
			txMethods = append(txMethods, tx.Request().Method())
			txMu.Unlock()
		},
	))

	invite := credentialedInvite(t)
	invEnv := sip.NewRequestEnvelope(invite)
	invEnv.SetTransport(sip.UDPMetadata())
	invEnv.SetRemoteAddr(netip.MustParseAddrPort("127.0.0.1:5060"))

	tp := &captureClientTransp{}
	tx, err := elm.NewClientTransaction(ctx, invEnv, tp)
	if err != nil {
		t.Fatalf("elm.NewClientTransaction() error = %v, want nil", err)
	}
	resQue := make(chan *sip.Response, 4)
	unbindResHndlr := tx.BindResponseHandler(sip.InboundResponseHandlerFunc(
		func(_ context.Context, res *sip.ResponseEnvelope) {
			resQue <- res.Message()
		},
	))
	t.Cleanup(func() {
		unbindResHndlr()
		unbindTxHndlr()
		// The test context is already canceled in cleanup, so detach and
		// bound the explicit termination.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if err := tx.Terminate(cctx, context.Canceled); err != nil {
			t.Errorf("tx.Terminate() error = %v, want nil", err)
		}
	})
	if err := tx.Start(ctx); err != nil {
		t.Fatalf("tx.Start() error = %v, want nil", err)
	}
	captured := tp.last(t).Message()

	nextRes := func() *sip.Response {
		t.Helper()
		select {
		case res := <-resQue:
			return res
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for delivered response")
			return nil
		}
	}

	resB := forkRes(t, captured, "fork-b", "b", lisRouteURI(t, lisB))
	resC := forkRes(t, captured, "fork-c", "c", lisRouteURI(t, lisC))
	resBReplay := resB.Clone().(*sip.Response)

	type leg struct {
		d   *dialog.Dialog
		ack *sip.RequestEnvelope
	}
	diagsByID := map[dialog.ID]*leg{}
	sendACK := func(l *leg) {
		t.Helper()
		clone, ok := l.ack.Clone().(*sip.RequestEnvelope)
		if !ok {
			t.Fatal("ACK clone is not a *sip.RequestEnvelope")
		}
		if err := elm.SendRequest(ctx, clone); err != nil {
			t.Fatalf("elm.SendRequest(ACK) error = %v, want nil", err)
		}
	}
	deliver := func(res *sip.Response) *leg {
		t.Helper()
		if err := tx.RecvResponse(ctx, sip.NewResponseEnvelope(res)); err != nil {
			t.Fatalf("tx.RecvResponse() error = %v, want nil", err)
		}
		res = nextRes()

		id, err := dialog.IDFromResponse(res)
		if err != nil {
			t.Fatalf("dialog.IDFromResponse() error = %v, want nil", err)
		}
		if l, ok := diagsByID[id]; ok {
			// A repeated 2xx for a known leg resends the retained ACK
			// without touching the dialog.
			sendACK(l)
			return l
		}
		d, err := dialog.NewUAC(captured, res)
		if err != nil {
			t.Fatalf("dialog.NewUAC() error = %v, want nil", err)
		}
		ack, err := d.NewACK(captured, res, dialog.RequestOptions{
			Body:        []byte{0, 1, 255},
			ContentType: &header.ContentType{Type: "application", Subtype: "octet-stream"},
		})
		if err != nil {
			t.Fatalf("d.NewACK() error = %v, want nil", err)
		}
		l := &leg{d: d, ack: ack}
		diagsByID[id] = l
		sendACK(l)
		return l
	}

	// Two forks answer; each delivers through the same built-in client
	// transaction and produces its own dialog, ACK, and route.
	legB := deliver(resB)
	legC := deliver(resC)
	viewC := legC.d.View()

	// Terminate the b leg, then replay its 2xx: the consumer still
	// answers the repeated response with the retained ACK.
	if changed := legB.d.Terminate(); !changed {
		t.Fatal("legB.Terminate() = false, want true")
	}
	viewBTerminated := legB.d.View()
	deliver(resBReplay)

	// The wire order is b, c, replayed b at each fork's listener port.
	var ackToTags []string
	wires := []*sip.Request{
		readWireReq(t, lisB),
		readWireReq(t, lisC),
		readWireReq(t, lisB),
	}
	for i, wire := range wires {
		if wire.Method != sip.RequestMethodAck {
			t.Fatalf("wire[%d] method = %v, want %v", i, wire.Method, sip.RequestMethodAck)
		}
		if cseq, ok := wire.Headers.CSeq(); !ok || cseq.SeqNum != testInviteSeq || cseq.Method != sip.RequestMethodAck {
			t.Fatalf("wire[%d] CSeq = %v, want %d ACK", i, cseq, testInviteSeq)
		}
		to, _ := wire.Headers.To()
		if tag, _ := to.Tag(); tag == "" {
			t.Fatalf("wire[%d] missing To tag", i)
		} else {
			ackToTags = append(ackToTags, tag)
		}
		if got, want := wire.Body, []byte{0, 1, 255}; !cmp.Equal(got, want) {
			t.Errorf("wire[%d] body = %v, want %v", i, got, want)
		}
	}
	for i, wantUser := range []string{"b", "c", "b"} {
		u, ok := wires[i].URI.(*sip.URI)
		if !ok || u == nil || u.Addr.Host() != "callee.example.test" || u.User.Username() != wantUser {
			t.Errorf("wire[%d] Request-URI = %v, want sip:%s@callee.example.test (fork Contact)",
				i, wires[i].URI, wantUser)
		}
	}
	if diff := cmp.Diff([]string{"fork-b", "fork-c", "fork-b"}, ackToTags); diff != "" {
		t.Errorf("ACK To tags mismatch (-want +got):\n%s", diff)
	}

	// The replayed ACK is the retained envelope's clone: identical branch
	// and dialog headers.
	bVia, _ := wires[0].Headers.FirstVia()
	replayVia, _ := wires[2].Headers.FirstVia()
	bBranch, _ := bVia.Branch()
	replayBranch, _ := replayVia.Branch()
	if replayBranch != bBranch {
		t.Errorf("replayed ACK branch = %q, want retained branch %q", replayBranch, bBranch)
	}
	for _, name := range []string{"Authorization", "Proxy-Authorization"} {
		want := hdrVals(t, captured.Headers, name)
		if len(want) == 0 {
			t.Fatalf("INVITE fixture has no %s header", name)
		}
		for _, i := range []int{0, 2} {
			if !slices.Equal(hdrVals(t, wires[i].Headers, name), want) {
				t.Errorf("wire[%d] %s values differ from sent INVITE", i, name)
			}
		}
	}

	if got := len(diagsByID); got != 2 {
		t.Errorf("dialog count = %d, want 2", got)
	}
	if got := viewBTerminated.State; got != dialog.StateTerminated {
		t.Errorf("fork-b dialog state = %v, want %v before replay", got, dialog.StateTerminated)
	}
	assertSameView(t, viewBTerminated, legB.d.View())
	assertSameView(t, viewC, legC.d.View())

	txMu.Lock()
	gotTxMethods := slices.Clone(txMethods)
	txMu.Unlock()
	if diff := cmp.Diff([]sip.RequestMethod{sip.RequestMethodInvite}, gotTxMethods); diff != "" {
		t.Errorf("registered client transactions mismatch (-want +got):\n%s", diff)
	}
}
