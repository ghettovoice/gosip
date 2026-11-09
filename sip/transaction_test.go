package sip_test

import (
	"context"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/timeutil"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

func mustClnTxSnapshot(
	tb testing.TB,
	tx interface {
		Snapshot() (*sip.ClientTransactionSnapshot, error)
	},
) *sip.ClientTransactionSnapshot {
	tb.Helper()
	snap, err := tx.Snapshot()
	if err != nil {
		tb.Fatalf("tx.Snapshot() error = %v, want nil", err)
	}
	return snap
}

func mustSrvTxSnapshot(
	tb testing.TB,
	tx interface {
		Snapshot() (*sip.ServerTransactionSnapshot, error)
	},
) *sip.ServerTransactionSnapshot {
	tb.Helper()
	snap, err := tx.Snapshot()
	if err != nil {
		tb.Fatalf("tx.Snapshot() error = %v, want nil", err)
	}
	return snap
}

type clnTxSnapRecorder struct {
	mu    sync.Mutex
	snaps []*sip.ClientTransactionSnapshot
}

func (r *clnTxSnapRecorder) handler() sip.ClientTransactionSnapshotHandlerFunc {
	return func(_ context.Context, snap *sip.ClientTransactionSnapshot) {
		r.mu.Lock()
		r.snaps = append(r.snaps, snap)
		r.mu.Unlock()
	}
}

func (r *clnTxSnapRecorder) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.snaps)
}

func (r *clnTxSnapRecorder) at(idx int) *sip.ClientTransactionSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snaps[idx]
}

func (r *clnTxSnapRecorder) states() []sip.TransactionState {
	r.mu.Lock()
	defer r.mu.Unlock()
	states := make([]sip.TransactionState, len(r.snaps))
	for i, snap := range r.snaps {
		states[i] = snap.State
	}
	return states
}

type srvTxSnapRecorder struct {
	mu    sync.Mutex
	snaps []*sip.ServerTransactionSnapshot
}

func (r *srvTxSnapRecorder) handler() sip.ServerTransactionSnapshotHandlerFunc {
	return func(_ context.Context, snap *sip.ServerTransactionSnapshot) {
		r.mu.Lock()
		r.snaps = append(r.snaps, snap)
		r.mu.Unlock()
	}
}

func (r *srvTxSnapRecorder) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.snaps)
}

func (r *srvTxSnapRecorder) at(idx int) *sip.ServerTransactionSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snaps[idx]
}

func (r *srvTxSnapRecorder) states() []sip.TransactionState {
	r.mu.Lock()
	defer r.mu.Unlock()
	states := make([]sip.TransactionState, len(r.snaps))
	for i, snap := range r.snaps {
		states[i] = snap.State
	}
	return states
}

func txSnapshotErr(tx sip.Transaction) error {
	switch tx := tx.(type) {
	case interface {
		Snapshot() (*sip.ClientTransactionSnapshot, error)
	}:
		_, err := tx.Snapshot()
		return err
	case interface {
		Snapshot() (*sip.ServerTransactionSnapshot, error)
	}:
		_, err := tx.Snapshot()
		return err
	default:
		return errors.ErrorWrap("transaction does not support snapshots")
	}
}

func testTpMeta(tp sip.TransportProto) sip.TransportMetadata {
	switch tp.Canonic() {
	case "UDP":
		return sip.UDPMetadata()
	case "TCP":
		return sip.TCPMetadata()
	case "TLS":
		return sip.TLSMetadata()
	case "SCTP":
		return sip.SCTPMetadata()
	case "TLS-SCTP":
		return sip.TLSSCTPMetadata()
	case "WS":
		return sip.WSMetadata()
	case "WSS":
		return sip.WSSMetadata()
	default:
		return sip.TransportMetadata{Proto: tp}
	}
}

func newInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	viaAddr netip.AddrPort,
) *sip.Request {
	tb.Helper()

	if branch == "" {
		branch = sip.MagicCookie + ".stub-branch"
	}

	req := &sip.Request{
		Proto:  sip.ProtoVer20(),
		Method: sip.RequestMethodInvite,
		URI: &sip.URI{
			User: sip.MakeUserInfo("alice"),
			Addr: sip.MakeHostAddr("alice.voip.com"),
		},
		Headers: make(sip.Headers).
			Set(header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: tp,
					Addr:      sip.MakeHostPortAddr(viaAddr.Addr().String(), viaAddr.Port()),
					Params:    make(sip.Values).Set("branch", branch),
				},
			}).
			Set(&header.From{
				URI:    &sip.URI{User: sip.MakeUserInfo("bob"), Addr: sip.MakeHostAddr("bob.voip.com")},
				Params: make(sip.Values).Set("tag", "from-1234"),
			}).
			Set(&header.To{
				URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alice.voip.com")},
			}).
			Set(header.CallID("call-1234@bob.voip.com")).
			Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInvite}).
			Set(sip.DefaultMaxForwards).
			Set(&header.Timestamp{RequestTime: time.Now().Add(-time.Second)}),
	}

	return req
}

func newInInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.RequestEnvelope {
	tb.Helper()

	req := sip.NewRequestEnvelope(newInviteReq(tb, tp, branch, rmtAddr)).
		SetTransport(testTpMeta(tp)).
		SetLocalAddr(locAddr).
		SetRemoteAddr(rmtAddr)

	return req
}

func newOutInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.RequestEnvelope {
	tb.Helper()

	req := sip.NewRequestEnvelope(newInviteReq(tb, tp, branch, locAddr)).
		SetTransport(testTpMeta(tp)).
		SetLocalAddr(locAddr).
		SetRemoteAddr(rmtAddr)

	return req
}

func newAckReq(tb testing.TB, invite *sip.Request, res *sip.Response) *sip.Request {
	tb.Helper()

	ack := invite.Clone().(*sip.Request)

	ack.Method = sip.RequestMethodAck
	if via, ok := ack.Headers.FirstVia(); ok && res.Status.IsSuccessful() {
		if branch, _ := via.Branch(); sip.IsRFC3261Branch(branch) {
			via.Params.Set("branch", branch+".ack")
		}
	}

	if cseq, ok := ack.Headers.CSeq(); ok {
		ack.Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: sip.RequestMethodAck})
	}

	if to, ok := res.Headers.To(); ok {
		ack.Headers.Set(to.Clone())
	}

	return ack
}

func newInAckReq(
	tb testing.TB,
	invite *sip.RequestEnvelope,
	res *sip.ResponseEnvelope,
) *sip.RequestEnvelope {
	tb.Helper()

	req := sip.NewRequestEnvelope(newAckReq(tb, invite.Message(), res.Message())).
		SetTransport(invite.Transport()).
		SetRemoteAddr(invite.RemoteAddr()).
		SetLocalAddr(invite.LocalAddr())

	return req
}

func newNonInviteReq(
	tb testing.TB,
	proto sip.TransportProto,
	branch string,
	rmtAddr netip.AddrPort,
) *sip.Request {
	tb.Helper()

	req := newInviteReq(tb, proto, branch, rmtAddr)

	req.Method = sip.RequestMethodInfo
	if cseq, ok := req.Headers.CSeq(); ok {
		req.Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: sip.RequestMethodInfo})
	}

	return req
}

func newInNonInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.RequestEnvelope {
	tb.Helper()

	req := sip.NewRequestEnvelope(newNonInviteReq(tb, tp, branch, rmtAddr)).
		SetTransport(testTpMeta(tp)).
		SetLocalAddr(locAddr).
		SetRemoteAddr(rmtAddr)

	return req
}

func newOutNonInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.RequestEnvelope {
	tb.Helper()

	req := sip.NewRequestEnvelope(newNonInviteReq(tb, tp, branch, locAddr)).
		SetTransport(testTpMeta(tp)).
		SetLocalAddr(locAddr).
		SetRemoteAddr(rmtAddr)

	return req
}

func newInRes(tb testing.TB, req *sip.RequestEnvelope, sts sip.ResponseStatus) *sip.ResponseEnvelope {
	tb.Helper()

	msg, err := req.Message().NewResponse(sts)
	if err != nil {
		tb.Fatalf("failed to create response: %v", err)
	}

	res := sip.NewResponseEnvelope(msg).
		SetTransport(req.Transport()).
		SetRemoteAddr(req.RemoteAddr()).
		SetLocalAddr(req.LocalAddr())

	return res
}

func waitForTxState(tb testing.TB, tx sip.Transaction, want sip.TransactionState, timeout time.Duration) {
	tb.Helper()

	// Allow the transaction goroutines to run and advance virtual time up to the timeout.
	time.Sleep(timeout)

	if got := tx.(interface{ State() sip.TransactionState }).State(); got != want {
		tb.Fatalf("transaction state did not reach %q, got %q", want, got)
	}
}

type restoreEvtProbe struct {
	states atomic.Int32
	errs   atomic.Int32
	resps  atomic.Int32
}

func (p *restoreEvtProbe) bind(t *testing.T, tx sip.Transaction) {
	t.Helper()

	tx.BindStateHandler(sip.TransactionStateHandlerFunc(
		func(context.Context, sip.TransactionState, sip.TransactionState) { p.states.Add(1) },
	))
	tx.BindErrorHandler(sip.ErrorHandlerFunc(func(context.Context, error) { p.errs.Add(1) }))
	if cln, ok := tx.(sip.ClientTransaction); ok {
		cln.BindResponseHandler(sip.InboundResponseHandlerFunc(
			func(context.Context, *sip.ResponseEnvelope) { p.resps.Add(1) },
		))
	}
}

func (p *restoreEvtProbe) assertQuiet(t *testing.T, stage string) {
	t.Helper()

	if got := p.states.Load(); got != 0 {
		t.Fatalf("state handler calls %s = %d, want 0", stage, got)
	}
	if got := p.errs.Load(); got != 0 {
		t.Fatalf("error handler calls %s = %d, want 0", stage, got)
	}
	if got := p.resps.Load(); got != 0 {
		t.Fatalf("response handler calls %s = %d, want 0", stage, got)
	}
}

type startableTransact interface {
	sip.Transaction
	Start(ctx context.Context) error
}

type restoreContractCase struct {
	restored          startableTransact
	state             sip.TransactionState
	tmrSnap           *timeutil.TimerSnapshot
	register          func(txm *sip.TransactionManager) error
	loaded            func(txm *sip.TransactionManager) bool
	noSend            func(t *testing.T)
	match             func(t *testing.T)
	dormantToDeadline bool
}

func runRestoreContract(t *testing.T, c restoreContractCase) {
	t.Helper()

	ctx := t.Context()
	deadline := c.tmrSnap.StartTime.Add(c.tmrSnap.Duration)
	txm := &sip.TransactionManager{}
	defer func() { _ = txm.Close() }()

	probe := &restoreEvtProbe{}
	probe.bind(t, c.restored)

	var startCalls atomic.Int32
	c.restored.BindStartHandler(sip.TransactionStartHandlerFunc(
		func(context.Context) { startCalls.Add(1) },
	))
	if c.restored.Started() {
		t.Fatal("restored.Started() = true, want false")
	}
	if err := txSnapshotErr(c.restored); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
		t.Fatalf("restored snapshot before Start error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
	}
	if got := startCalls.Load(); got != 0 {
		t.Fatalf("start handler calls = %d before Start, want 0", got)
	}

	if got := c.restored.State(); got != c.state {
		t.Fatalf("restored.State() = %q, want %q", got, c.state)
	}
	c.noSend(t)

	if c.dormantToDeadline {
		time.Sleep(time.Until(deadline) + time.Second)
	} else {
		if d := min(time.Until(deadline)/2, 500*time.Millisecond); d > 0 {
			time.Sleep(d)
		}
	}
	probe.assertQuiet(t, "while dormant")
	if got := startCalls.Load(); got != 0 {
		t.Fatalf("start handler calls = %d while dormant, want 0", got)
	}
	c.noSend(t)
	if got := c.restored.State(); got != c.state {
		t.Fatalf("restored.State() after dormant advance = %q, want %q", got, c.state)
	}

	if err := c.register(txm); err != nil {
		t.Fatalf("register restored transaction error = %v, want nil", err)
	}
	if !c.loaded(txm) {
		t.Fatal("restored transaction not found in manager, want registered")
	}

	if err := c.restored.Start(ctx); err != nil {
		t.Fatalf("restored.Start() error = %v, want nil", err)
	}
	c.noSend(t)

	if got := startCalls.Load(); got != 1 {
		t.Fatalf("start handler calls = %d after Start, want 1", got)
	}
	if !c.restored.Started() {
		t.Fatal("restored.Started() = false after Start, want true")
	}

	if c.dormantToDeadline {
		waitForTxState(t, c.restored, sip.TransactionStateTerminated, time.Second)
	} else {
		probe.assertQuiet(t, "on start")
		if got := c.restored.State(); got != c.state {
			t.Fatalf("restored.State() after Start = %q, want %q", got, c.state)
		}

		c.match(t)

		if d := time.Until(deadline) - 10*time.Millisecond; d > 0 {
			time.Sleep(d)
		}
		if got := c.restored.State(); got != c.state {
			t.Fatalf("restored.State() before deadline = %q, want %q (timer restarted?)", got, c.state)
		}
		time.Sleep(20 * time.Millisecond)
		waitForTxState(t, c.restored, sip.TransactionStateTerminated, time.Second)
	}

	if c.loaded(txm) {
		t.Fatal("restored transaction still in manager after termination, want removed")
	}
}

func TestTransaction_Restore_StartContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(t *testing.T, ctx context.Context) restoreContractCase
	}{
		{
			"invite client (timer D)",
			func(t *testing.T, ctx context.Context) restoreContractCase {
				t.Helper()

				remote := netip.MustParseAddrPort("55.55.55.55:5060")
				local := netip.MustParseAddrPort("11.11.11.11:5070")
				tp := newStubClnTransp(false)
				req := newOutInviteReq(t, "UDP", sip.MagicCookie+".contract-cln-inv", local, remote)

				tx, err := sip.NewInviteClientTransaction(req, tp)
				if err != nil {
					t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
				}
				if err := tx.Start(ctx); err != nil {
					t.Fatalf("tx.Start() error = %v, want nil", err)
				}
				tp.waitSendReq(t)
				if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusBusyHere)); err != nil {
					t.Fatalf("tx.RecvResponse(486) error = %v, want nil", err)
				}
				tp.waitSendReq(t)
				waitForTxState(t, tx, sip.TransactionStateCompleted, 100*time.Millisecond)

				snap := mustClnTxSnapshot(t, tx)
				if snap.TimerD == nil {
					t.Fatal("snapshot TimerD = nil, want armed")
				}
				if err := tx.Terminate(ctx, errors.ErrorWrap("source terminated")); err != nil {
					t.Fatalf("tx.Terminate() error = %v, want nil", err)
				}

				rTP := newStubClnTransp(false)
				rtx, err := sip.RestoreInviteClientTransaction(snap, rTP)
				if err != nil {
					t.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
				}
				return restoreContractCase{
					state:    sip.TransactionStateCompleted,
					tmrSnap:  snap.TimerD,
					restored: rtx,
					register: func(txm *sip.TransactionManager) error {
						return txm.RegisterClientTransaction(ctx, rtx)
					},
					loaded: func(txm *sip.TransactionManager) bool {
						_, ok := txm.LoadClientTransaction(rtx.Key())
						return ok
					},
					noSend: func(t *testing.T) { t.Helper(); rTP.ensureNoSendReq(t) },
					match: func(t *testing.T) {
						t.Helper()
						if err := rtx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusBusyHere)); err != nil {
							t.Fatalf("restored.RecvResponse(486) error = %v, want nil", err)
						}
						if call := rTP.waitSendReq(t); call.req.Method() != sip.RequestMethodAck {
							t.Fatalf("resent request method = %q, want ACK", call.req.Method())
						}
					},
				}
			},
		},
		{
			"non-invite client (timer K)",
			func(t *testing.T, ctx context.Context) restoreContractCase {
				t.Helper()

				remote := netip.MustParseAddrPort("55.55.55.55:5060")
				local := netip.MustParseAddrPort("11.11.11.11:5070")
				tp := newStubClnTransp(false)
				req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".contract-cln-non", local, remote)

				tx, err := sip.NewNonInviteClientTransaction(req, tp)
				if err != nil {
					t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
				}
				if err := tx.Start(ctx); err != nil {
					t.Fatalf("tx.Start() error = %v, want nil", err)
				}
				tp.waitSendReq(t)
				if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
					t.Fatalf("tx.RecvResponse(200) error = %v, want nil", err)
				}
				waitForTxState(t, tx, sip.TransactionStateCompleted, 100*time.Millisecond)

				snap := mustClnTxSnapshot(t, tx)
				if snap.TimerK == nil {
					t.Fatal("snapshot TimerK = nil, want armed")
				}
				if err := tx.Terminate(ctx, errors.ErrorWrap("source terminated")); err != nil {
					t.Fatalf("tx.Terminate() error = %v, want nil", err)
				}

				rTP := newStubClnTransp(false)
				var rtx *sip.NonInviteClientTransaction
				restored, err := sip.RestoreNonInviteClientTransaction(snap, rTP)
				if err != nil {
					t.Fatalf("sip.RestoreNonInviteClientTransaction() error = %v, want nil", err)
				}
				rtx = restored
				return restoreContractCase{
					state:    sip.TransactionStateCompleted,
					tmrSnap:  snap.TimerK,
					restored: rtx,
					register: func(txm *sip.TransactionManager) error {
						return txm.RegisterClientTransaction(ctx, rtx)
					},
					loaded: func(txm *sip.TransactionManager) bool {
						_, ok := txm.LoadClientTransaction(rtx.Key())
						return ok
					},
					noSend: func(t *testing.T) { t.Helper(); rTP.ensureNoSendReq(t) },
					match: func(t *testing.T) {
						t.Helper()
						res := newInRes(t, req, sip.ResponseStatusOK)
						if !rtx.MatchMessage(res.Message()) {
							t.Fatal("restored.MatchMessage(200) = false, want true")
						}
					},
				}
			},
		},
		{
			"invite server (timer H)",
			func(t *testing.T, ctx context.Context) restoreContractCase {
				t.Helper()

				remote := netip.MustParseAddrPort("55.55.55.55:5060")
				local := netip.MustParseAddrPort("11.11.11.11:5070")
				tp := newStubSrvTransp(true)
				req := newInInviteReq(t, "TCP", sip.MagicCookie+".contract-srv-inv", local, remote)

				tx, err := sip.NewInviteServerTransaction(req, tp)
				if err != nil {
					t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
				}
				if err := tx.Start(ctx); err != nil {
					t.Fatalf("tx.Start() error = %v, want nil", err)
				}
				if err := tx.SendResponse(ctx, newInRes(t, req, sip.ResponseStatusBusyHere)); err != nil {
					t.Fatalf("tx.SendResponse(486) error = %v, want nil", err)
				}
				tp.waitSendRes(t)
				waitForTxState(t, tx, sip.TransactionStateCompleted, 100*time.Millisecond)

				snap := mustSrvTxSnapshot(t, tx)
				if snap.TimerH == nil {
					t.Fatal("snapshot TimerH = nil, want armed")
				}
				if err := tx.Terminate(ctx, errors.ErrorWrap("source terminated")); err != nil {
					t.Fatalf("tx.Terminate() error = %v, want nil", err)
				}

				rTP := newStubSrvTransp(true)
				rtx, err := sip.RestoreInviteServerTransaction(snap, rTP)
				if err != nil {
					t.Fatalf("sip.RestoreInviteServerTransaction() error = %v, want nil", err)
				}
				return restoreContractCase{
					state:    sip.TransactionStateCompleted,
					tmrSnap:  snap.TimerH,
					restored: rtx,
					register: func(txm *sip.TransactionManager) error {
						return txm.RegisterServerTransaction(ctx, rtx)
					},
					loaded: func(txm *sip.TransactionManager) bool {
						_, ok := txm.LoadServerTransaction(rtx.Key())
						return ok
					},
					noSend: func(t *testing.T) { t.Helper(); rTP.ensureNoSendRes(t) },
					match: func(t *testing.T) {
						t.Helper()
						if err := rtx.RecvRequest(ctx, req.Clone().(*sip.RequestEnvelope)); err != nil {
							t.Fatalf("restored.RecvRequest(INVITE) error = %v, want nil", err)
						}
						if call := rTP.waitSendRes(t); call.res.Status() != sip.ResponseStatusBusyHere {
							t.Fatalf("resent response status = %v, want %v",
								call.res.Status(), sip.ResponseStatusBusyHere)
						}
					},
				}
			},
		},
		{
			"non-invite server (timer J)",
			func(t *testing.T, ctx context.Context) restoreContractCase {
				t.Helper()

				remote := netip.MustParseAddrPort("55.55.55.55:5060")
				local := netip.MustParseAddrPort("11.11.11.11:5070")
				tp := newStubSrvTransp(false)
				req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".contract-srv-non", local, remote)

				tx, err := sip.NewNonInviteServerTransaction(req, tp)
				if err != nil {
					t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
				}
				if err := tx.Start(ctx); err != nil {
					t.Fatalf("tx.Start() error = %v, want nil", err)
				}
				if err := tx.SendResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
					t.Fatalf("tx.SendResponse(200) error = %v, want nil", err)
				}
				tp.waitSendRes(t)
				waitForTxState(t, tx, sip.TransactionStateCompleted, 100*time.Millisecond)

				snap := mustSrvTxSnapshot(t, tx)
				if snap.TimerJ == nil {
					t.Fatal("snapshot TimerJ = nil, want armed")
				}
				if err := tx.Terminate(ctx, errors.ErrorWrap("source terminated")); err != nil {
					t.Fatalf("tx.Terminate() error = %v, want nil", err)
				}

				rTP := newStubSrvTransp(false)
				rtx, err := sip.RestoreNonInviteServerTransaction(snap, rTP)
				if err != nil {
					t.Fatalf("sip.RestoreNonInviteServerTransaction() error = %v, want nil", err)
				}
				return restoreContractCase{
					state:    sip.TransactionStateCompleted,
					tmrSnap:  snap.TimerJ,
					restored: rtx,
					register: func(txm *sip.TransactionManager) error {
						return txm.RegisterServerTransaction(ctx, rtx)
					},
					loaded: func(txm *sip.TransactionManager) bool {
						_, ok := txm.LoadServerTransaction(rtx.Key())
						return ok
					},
					noSend:            func(t *testing.T) { t.Helper(); rTP.ensureNoSendRes(t) },
					match:             func(t *testing.T) { t.Helper() },
					dormantToDeadline: true,
				}
			},
		},
		{
			"invite client (expired timer D)",
			func(t *testing.T, ctx context.Context) restoreContractCase {
				t.Helper()

				remote := netip.MustParseAddrPort("55.55.55.55:5060")
				local := netip.MustParseAddrPort("11.11.11.11:5070")
				tp := newStubClnTransp(false)
				req := newOutInviteReq(t, "UDP", sip.MagicCookie+".contract-cln-inv-d", local, remote)

				tx, err := sip.NewInviteClientTransaction(req, tp)
				if err != nil {
					t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
				}
				if err := tx.Start(ctx); err != nil {
					t.Fatalf("tx.Start() error = %v, want nil", err)
				}
				tp.waitSendReq(t)
				if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusBusyHere)); err != nil {
					t.Fatalf("tx.RecvResponse(486) error = %v, want nil", err)
				}
				tp.waitSendReq(t)
				waitForTxState(t, tx, sip.TransactionStateCompleted, 100*time.Millisecond)

				snap := mustClnTxSnapshot(t, tx)
				if snap.TimerD == nil {
					t.Fatal("snapshot TimerD = nil, want armed")
				}
				if err := tx.Terminate(ctx, errors.ErrorWrap("source terminated")); err != nil {
					t.Fatalf("tx.Terminate() error = %v, want nil", err)
				}

				rTP := newStubClnTransp(false)
				rtx, err := sip.RestoreInviteClientTransaction(snap, rTP)
				if err != nil {
					t.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
				}
				return restoreContractCase{
					state:    sip.TransactionStateCompleted,
					tmrSnap:  snap.TimerD,
					restored: rtx,
					register: func(txm *sip.TransactionManager) error {
						return txm.RegisterClientTransaction(ctx, rtx)
					},
					loaded: func(txm *sip.TransactionManager) bool {
						_, ok := txm.LoadClientTransaction(rtx.Key())
						return ok
					},
					noSend:            func(t *testing.T) { t.Helper(); rTP.ensureNoSendReq(t) },
					match:             func(t *testing.T) { t.Helper() },
					dormantToDeadline: true,
				}
			},
		},
		{
			"invite client (expired timer M)",
			func(t *testing.T, ctx context.Context) restoreContractCase {
				t.Helper()

				remote := netip.MustParseAddrPort("55.55.55.55:5060")
				local := netip.MustParseAddrPort("11.11.11.11:5070")
				tp := newStubClnTransp(false)
				req := newOutInviteReq(t, "UDP", sip.MagicCookie+".contract-cln-inv-m", local, remote)

				tx, err := sip.NewInviteClientTransaction(req, tp)
				if err != nil {
					t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
				}
				if err := tx.Start(ctx); err != nil {
					t.Fatalf("tx.Start() error = %v, want nil", err)
				}
				tp.waitSendReq(t)
				if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
					t.Fatalf("tx.RecvResponse(200) error = %v, want nil", err)
				}
				waitForTxState(t, tx, sip.TransactionStateAccepted, 100*time.Millisecond)

				snap := mustClnTxSnapshot(t, tx)
				if snap.TimerM == nil {
					t.Fatal("snapshot TimerM = nil, want armed")
				}
				if err := tx.Terminate(ctx, errors.ErrorWrap("source terminated")); err != nil {
					t.Fatalf("tx.Terminate() error = %v, want nil", err)
				}

				rTP := newStubClnTransp(false)
				rtx, err := sip.RestoreInviteClientTransaction(snap, rTP)
				if err != nil {
					t.Fatalf("sip.RestoreInviteClientTransaction() error = %v, want nil", err)
				}
				return restoreContractCase{
					state:    sip.TransactionStateAccepted,
					tmrSnap:  snap.TimerM,
					restored: rtx,
					register: func(txm *sip.TransactionManager) error {
						return txm.RegisterClientTransaction(ctx, rtx)
					},
					loaded: func(txm *sip.TransactionManager) bool {
						_, ok := txm.LoadClientTransaction(rtx.Key())
						return ok
					},
					noSend:            func(t *testing.T) { t.Helper(); rTP.ensureNoSendReq(t) },
					match:             func(t *testing.T) { t.Helper() },
					dormantToDeadline: true,
				}
			},
		},
		{
			"non-invite client (expired timer K)",
			func(t *testing.T, ctx context.Context) restoreContractCase {
				t.Helper()

				remote := netip.MustParseAddrPort("55.55.55.55:5060")
				local := netip.MustParseAddrPort("11.11.11.11:5070")
				tp := newStubClnTransp(false)
				req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".contract-cln-non-k", local, remote)

				tx, err := sip.NewNonInviteClientTransaction(req, tp)
				if err != nil {
					t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
				}
				if err := tx.Start(ctx); err != nil {
					t.Fatalf("tx.Start() error = %v, want nil", err)
				}
				tp.waitSendReq(t)
				if err := tx.RecvResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
					t.Fatalf("tx.RecvResponse(200) error = %v, want nil", err)
				}
				waitForTxState(t, tx, sip.TransactionStateCompleted, 100*time.Millisecond)

				snap := mustClnTxSnapshot(t, tx)
				if snap.TimerK == nil {
					t.Fatal("snapshot TimerK = nil, want armed")
				}
				if err := tx.Terminate(ctx, errors.ErrorWrap("source terminated")); err != nil {
					t.Fatalf("tx.Terminate() error = %v, want nil", err)
				}

				rTP := newStubClnTransp(false)
				rtx, err := sip.RestoreNonInviteClientTransaction(snap, rTP)
				if err != nil {
					t.Fatalf("sip.RestoreNonInviteClientTransaction() error = %v, want nil", err)
				}
				return restoreContractCase{
					state:    sip.TransactionStateCompleted,
					tmrSnap:  snap.TimerK,
					restored: rtx,
					register: func(txm *sip.TransactionManager) error {
						return txm.RegisterClientTransaction(ctx, rtx)
					},
					loaded: func(txm *sip.TransactionManager) bool {
						_, ok := txm.LoadClientTransaction(rtx.Key())
						return ok
					},
					noSend:            func(t *testing.T) { t.Helper(); rTP.ensureNoSendReq(t) },
					match:             func(t *testing.T) { t.Helper() },
					dormantToDeadline: true,
				}
			},
		},
		{
			"invite server (expired timer I)",
			func(t *testing.T, ctx context.Context) restoreContractCase {
				t.Helper()

				remote := netip.MustParseAddrPort("55.55.55.55:5060")
				local := netip.MustParseAddrPort("11.11.11.11:5070")
				tp := newStubSrvTransp(false)
				req := newInInviteReq(t, "UDP", sip.MagicCookie+".contract-srv-inv-i", local, remote)

				tx, err := sip.NewInviteServerTransaction(req, tp)
				if err != nil {
					t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
				}
				if err := tx.Start(ctx); err != nil {
					t.Fatalf("tx.Start() error = %v, want nil", err)
				}
				res := newInRes(t, req, sip.ResponseStatusBusyHere)
				if err := tx.SendResponse(ctx, res); err != nil {
					t.Fatalf("tx.SendResponse(486) error = %v, want nil", err)
				}
				tp.waitSendRes(t)
				waitForTxState(t, tx, sip.TransactionStateCompleted, 100*time.Millisecond)
				if err := tx.RecvRequest(ctx, newInAckReq(t, req, res)); err != nil {
					t.Fatalf("tx.RecvRequest(ACK) error = %v, want nil", err)
				}
				waitForTxState(t, tx, sip.TransactionStateConfirmed, 100*time.Millisecond)

				snap := mustSrvTxSnapshot(t, tx)
				if snap.TimerI == nil {
					t.Fatal("snapshot TimerI = nil, want armed")
				}
				if err := tx.Terminate(ctx, errors.ErrorWrap("source terminated")); err != nil {
					t.Fatalf("tx.Terminate() error = %v, want nil", err)
				}

				rTP := newStubSrvTransp(false)
				rtx, err := sip.RestoreInviteServerTransaction(snap, rTP)
				if err != nil {
					t.Fatalf("sip.RestoreInviteServerTransaction() error = %v, want nil", err)
				}
				return restoreContractCase{
					state:    sip.TransactionStateConfirmed,
					tmrSnap:  snap.TimerI,
					restored: rtx,
					register: func(txm *sip.TransactionManager) error {
						return txm.RegisterServerTransaction(ctx, rtx)
					},
					loaded: func(txm *sip.TransactionManager) bool {
						_, ok := txm.LoadServerTransaction(rtx.Key())
						return ok
					},
					noSend:            func(t *testing.T) { t.Helper(); rTP.ensureNoSendRes(t) },
					match:             func(t *testing.T) { t.Helper() },
					dormantToDeadline: true,
				}
			},
		},
		{
			"invite server (expired timer L)",
			func(t *testing.T, ctx context.Context) restoreContractCase {
				t.Helper()

				remote := netip.MustParseAddrPort("55.55.55.55:5060")
				local := netip.MustParseAddrPort("11.11.11.11:5070")
				tp := newStubSrvTransp(false)
				req := newInInviteReq(t, "UDP", sip.MagicCookie+".contract-srv-inv-l", local, remote)

				tx, err := sip.NewInviteServerTransaction(req, tp)
				if err != nil {
					t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
				}
				if err := tx.Start(ctx); err != nil {
					t.Fatalf("tx.Start() error = %v, want nil", err)
				}
				if err := tx.SendResponse(ctx, newInRes(t, req, sip.ResponseStatusOK)); err != nil {
					t.Fatalf("tx.SendResponse(200) error = %v, want nil", err)
				}
				tp.waitSendRes(t)
				waitForTxState(t, tx, sip.TransactionStateAccepted, 100*time.Millisecond)

				snap := mustSrvTxSnapshot(t, tx)
				if snap.TimerL == nil {
					t.Fatal("snapshot TimerL = nil, want armed")
				}
				if err := tx.Terminate(ctx, errors.ErrorWrap("source terminated")); err != nil {
					t.Fatalf("tx.Terminate() error = %v, want nil", err)
				}

				rTP := newStubSrvTransp(false)
				rtx, err := sip.RestoreInviteServerTransaction(snap, rTP)
				if err != nil {
					t.Fatalf("sip.RestoreInviteServerTransaction() error = %v, want nil", err)
				}
				return restoreContractCase{
					state:    sip.TransactionStateAccepted,
					tmrSnap:  snap.TimerL,
					restored: rtx,
					register: func(txm *sip.TransactionManager) error {
						return txm.RegisterServerTransaction(ctx, rtx)
					},
					loaded: func(txm *sip.TransactionManager) bool {
						_, ok := txm.LoadServerTransaction(rtx.Key())
						return ok
					},
					noSend:            func(t *testing.T) { t.Helper(); rTP.ensureNoSendRes(t) },
					match:             func(t *testing.T) { t.Helper() },
					dormantToDeadline: true,
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				runRestoreContract(t, tt.prepare(t, t.Context()))
			})
		})
	}
}

type startProbe struct {
	calls atomic.Int32
}

func (p *startProbe) handler() sip.TransactionStartHandlerFunc {
	return func(context.Context) { p.calls.Add(1) }
}

func (p *startProbe) want(t *testing.T, want int32) {
	t.Helper()

	if got := p.calls.Load(); got != want {
		t.Fatalf("start handler calls = %d, want %d", got, want)
	}
}

func TestTransaction_Start_Events(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("11.11.11.11:5070")
	raddr := netip.MustParseAddrPort("55.55.55.55:5060")

	tests := []struct {
		name  string
		state sip.TransactionState
		new   func(t *testing.T) startableTransact
	}{
		{
			"invite client",
			sip.TransactionStateCalling,
			func(t *testing.T) startableTransact {
				t.Helper()

				tx, err := sip.NewInviteClientTransaction(
					newOutInviteReq(t, "UDP", sip.MagicCookie+".lc-cln-inv", laddr, raddr),
					newStubClnTransp(false),
				)
				if err != nil {
					t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
				}
				return tx
			},
		},
		{
			"non-invite client",
			sip.TransactionStateTrying,
			func(t *testing.T) startableTransact {
				t.Helper()

				tx, err := sip.NewNonInviteClientTransaction(
					newOutNonInviteReq(t, "UDP", sip.MagicCookie+".lc-cln-non", laddr, raddr),
					newStubClnTransp(false),
				)
				if err != nil {
					t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
				}
				return tx
			},
		},
		{
			"invite server",
			sip.TransactionStateProceeding,
			func(t *testing.T) startableTransact {
				t.Helper()

				tx, err := sip.NewInviteServerTransaction(
					newInInviteReq(t, "UDP", sip.MagicCookie+".lc-srv-inv", laddr, raddr),
					newStubSrvTransp(false),
				)
				if err != nil {
					t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
				}
				return tx
			},
		},
		{
			"non-invite server",
			sip.TransactionStateTrying,
			func(t *testing.T) startableTransact {
				t.Helper()

				tx, err := sip.NewNonInviteServerTransaction(
					newInNonInviteReq(t, "UDP", sip.MagicCookie+".lc-srv-non", laddr, raddr),
					newStubSrvTransp(false),
				)
				if err != nil {
					t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
				}
				return tx
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()

				tx := tt.new(t)
				if tx.Started() {
					t.Fatal("tx.Started() = true, want false")
				}
				if got := tx.State(); got != tt.state {
					t.Fatalf("tx.State() = %v, want %v", got, tt.state)
				}

				probe := &startProbe{}
				probe2 := &startProbe{}
				tx.BindStartHandler(probe.handler())
				tx.BindStartHandler(probe2.handler())

				if err := tx.Start(ctx); err != nil {
					t.Fatalf("tx.Start() error = %v, want nil", err)
				}
				probe.want(t, 1)
				probe2.want(t, 1)
				if !tx.Started() {
					t.Fatal("tx.Started() = false, want true")
				}

				if err := tx.Start(ctx); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
					t.Fatalf("repeated tx.Start() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
				}
				probe.want(t, 1)

				if err := tx.Terminate(ctx, errors.ErrorWrap("done")); err != nil {
					t.Fatalf("tx.Terminate() error = %v, want nil", err)
				}
				probe.want(t, 1)
				if got := tx.State(); got != sip.TransactionStateTerminated {
					t.Fatalf("tx.State() = %v, want %v", got, sip.TransactionStateTerminated)
				}
			})
		})

		t.Run(tt.name+" unbind", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				tx := tt.new(t)
				probe := &startProbe{}
				unbind := tx.BindStartHandler(probe.handler())
				unbind()

				if err := tx.Start(t.Context()); err != nil {
					t.Fatalf("tx.Start() error = %v, want nil", err)
				}
				probe.want(t, 0)

				late := &startProbe{}
				tx.BindStartHandler(late.handler())
				if err := tx.Terminate(t.Context(), errors.ErrorWrap("done")); err != nil {
					t.Fatalf("tx.Terminate() error = %v, want nil", err)
				}
				late.want(t, 0)
			})
		})

		t.Run(tt.name+" prepared terminate", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				tx := tt.new(t)
				probe := &startProbe{}
				tx.BindStartHandler(probe.handler())

				if err := tx.Terminate(t.Context(), errors.ErrorWrap("done")); err != nil {
					t.Fatalf("tx.Terminate() error = %v, want nil", err)
				}
				probe.want(t, 0)
				if err := txSnapshotErr(tx); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
					t.Fatalf("tx snapshot before Start error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
				}
				if err := tx.Start(t.Context()); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
					t.Fatalf("tx.Start() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
				}
			})
		})
	}
}

func TestTransaction_ContextPropagation(t *testing.T) {
	t.Parallel()

	localAddr := netip.MustParseAddrPort("11.11.11.11:5070")
	remoteAddr := netip.MustParseAddrPort("55.55.55.55:5060")
	tests := []struct {
		name string
		new  func(*testing.T, *prxStubTransp, string) sip.Transaction
	}{
		{
			"invite client",
			func(t *testing.T, tp *prxStubTransp, branch string) sip.Transaction {
				t.Helper()

				tx, err := sip.NewInviteClientTransaction(
					newOutInviteReq(t, "UDP", branch, localAddr, remoteAddr),
					tp,
				)
				if err != nil {
					t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
				}
				return tx
			},
		},
		{
			"non-invite client",
			func(t *testing.T, tp *prxStubTransp, branch string) sip.Transaction {
				t.Helper()

				tx, err := sip.NewNonInviteClientTransaction(
					newOutNonInviteReq(t, "UDP", branch, localAddr, remoteAddr),
					tp,
				)
				if err != nil {
					t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
				}
				return tx
			},
		},
		{
			"invite server",
			func(t *testing.T, tp *prxStubTransp, branch string) sip.Transaction {
				t.Helper()

				tx, err := sip.NewInviteServerTransaction(
					newInInviteReq(t, "UDP", branch, localAddr, remoteAddr),
					tp,
				)
				if err != nil {
					t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
				}
				return tx
			},
		},
		{
			"non-invite server",
			func(t *testing.T, tp *prxStubTransp, branch string) sip.Transaction {
				t.Helper()

				tx, err := sip.NewNonInviteServerTransaction(
					newInNonInviteReq(t, "UDP", branch, localAddr, remoteAddr),
					tp,
				)
				if err != nil {
					t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
				}
				return tx
			},
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tp := newPrxStubTransp()
			tx := tt.new(t, tp, sip.MagicCookie+".context-start-"+strconv.Itoa(i))
			registerTxCleanup(t, tx)
			startCtxs := make(chan context.Context, 1)
			stateCtxs := make(chan context.Context, 8)
			snapshotCtxs := make(chan context.Context, 8)
			responseCtxs := make(chan context.Context, 1)
			tx.BindStartHandler(sip.TransactionStartHandlerFunc(func(ctx context.Context) {
				startCtxs <- ctx
			}))
			tx.BindStateHandler(sip.TransactionStateHandlerFunc(func(ctx context.Context, _, _ sip.TransactionState) {
				stateCtxs <- ctx
			}))
			switch tx := tx.(type) {
			case sip.ClientTransaction:
				tx.BindResponseHandler(sip.InboundResponseHandlerFunc(func(ctx context.Context, _ *sip.ResponseEnvelope) {
					responseCtxs <- ctx
				}))
				if snapshotter, ok := tx.(sip.ClientTransactionSnapshotter); ok {
					snapshotter.BindSnapshotHandler(sip.ClientTransactionSnapshotHandlerFunc(func(
						ctx context.Context,
						_ *sip.ClientTransactionSnapshot,
					) {
						snapshotCtxs <- ctx
					}))
				}
			case sip.ServerTransaction:
				if snapshotter, ok := tx.(sip.ServerTransactionSnapshotter); ok {
					snapshotter.BindSnapshotHandler(sip.ServerTransactionSnapshotHandlerFunc(func(
						ctx context.Context,
						_ *sip.ServerTransactionSnapshot,
					) {
						snapshotCtxs <- ctx
					}))
				}
			}

			startCtx := context.WithValue(t.Context(), testCtxKey{}, "start")
			if err := tx.Start(startCtx); err != nil {
				t.Fatalf("tx.Start() error = %v, want nil", err)
			}
			assertPropagationCtx(t, testutil.WaitResult(t, startCtxs, "transaction start context"), tx, tp, "start")
			if clientTx, ok := tx.(sip.ClientTransaction); ok {
				assertPropagationCtx(t, tp.waitSendReq(t).ctx, tx, tp, "start")
				responseCtx := context.WithValue(t.Context(), testCtxKey{}, "response")
				if err := clientTx.RecvResponse(responseCtx, newInRes(t, clientTx.Request(), sip.ResponseStatusRinging)); err != nil {
					t.Fatalf("clientTx.RecvResponse(180) error = %v, want nil", err)
				}
				assertPropagationCtx(t,
					testutil.WaitResult(t, responseCtxs, "client response callback context"),
					tx, tp, "response",
				)
			}

			terminateCtx := context.WithValue(t.Context(), testCtxKey{}, "terminate")
			if err := tx.Terminate(terminateCtx, nil); err != nil {
				t.Fatalf("tx.Terminate() error = %v, want nil", err)
			}
			waitForPropagationCtx(t, stateCtxs, tx, tp, "terminate")
			waitForPropagationCtx(t, snapshotCtxs, tx, tp, "terminate")
		})
	}
}

func waitForPropagationCtx(
	t *testing.T,
	ctxs <-chan context.Context,
	tx sip.Transaction,
	tp sip.Transport,
	value string,
) {
	t.Helper()

	timer := time.NewTimer(time.Second)
	defer timer.Stop()

	for {
		select {
		case ctx, ok := <-ctxs:
			if !ok || ctx == nil {
				t.Fatalf("transaction callback context channel closed before value %q", value)
				return
			}
			if ctx.Value(testCtxKey{}) == value {
				assertPropagationCtx(t, ctx, tx, tp, value)
				return
			}
		case <-timer.C:
			t.Fatalf("no transaction callback observed context value %q", value)
		}
	}
}

func registerTxCleanup(tb testing.TB, tx sip.Transaction) {
	tb.Helper()

	tb.Cleanup(func() {
		if tx.State() == sip.TransactionStateTerminated {
			return
		}
		// The test context is already canceled when cleanup runs.
		if err := tx.Terminate(context.WithoutCancel(tb.Context()), nil); err != nil {
			tb.Errorf("tx.Terminate() cleanup error = %v, want nil", errors.Wrap(err))
		}
	})
}

type stubTxDefaults struct{}

func (stubTxDefaults) MatchMessage(sip.Message) bool            { return true }
func (stubTxDefaults) LastError() error                         { return nil }
func (stubTxDefaults) BindErrorHandler(sip.ErrorHandler) func() { return func() {} }
func (stubTxDefaults) Terminate(context.Context, error) error   { return nil }

type stubClnTxDefaults struct{}

func (stubClnTxDefaults) Request() *sip.RequestEnvelope { return nil }
func (stubClnTxDefaults) LastResponse() *sip.ResponseEnvelope {
	return nil
}

func (stubClnTxDefaults) Transport() sip.ClientTransport { return nil }
func (stubClnTxDefaults) RecvResponse(context.Context, *sip.ResponseEnvelope) error {
	return nil
}

func (stubClnTxDefaults) BindResponseHandler(sip.InboundResponseHandler) func() {
	return func() {}
}

type stubSrvTxDefaults struct{}

func (stubSrvTxDefaults) Request() *sip.RequestEnvelope { return nil }
func (stubSrvTxDefaults) LastResponse() *sip.ResponseEnvelope {
	return nil
}

func (stubSrvTxDefaults) Transport() sip.ServerTransport { return nil }
func (stubSrvTxDefaults) RecvRequest(context.Context, *sip.RequestEnvelope) error {
	return nil
}

func (stubSrvTxDefaults) SendResponse(
	context.Context,
	*sip.ResponseEnvelope,
	...sip.SendResponseOptions,
) error {
	return nil
}

func (stubSrvTxDefaults) Respond(context.Context, sip.ResponseStatus, ...sip.RespondOptions) error {
	return nil
}
