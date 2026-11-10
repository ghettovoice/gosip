package proxy_test

import (
	"context"
	"net/netip"
	"sync"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil/siptest"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

type sendReqCall struct {
	ctx  context.Context
	req  *sip.RequestEnvelope
	opts sip.SendRequestOptions
}

// stubClnTransp is a client transport stub recording outbound request sends.
type stubClnTransp struct {
	reliable bool

	sendReqCalls chan sendReqCall

	mu          sync.Mutex
	sendReqHook func(sendReqCall, int) error
	sendReqCnt  int
}

func newStubClnTransp(reliable bool) *stubClnTransp {
	return &stubClnTransp{
		reliable:     reliable,
		sendReqCalls: make(chan sendReqCall, 64),
	}
}

func (tp *stubClnTransp) Metadata() sip.TransportMetadata {
	if tp == nil {
		return sip.TransportMetadata{}
	}

	var flags sip.TransportFlags

	flags.SetReliable(tp.reliable)

	return sip.TransportMetadata{Flags: flags}
}

func (tp *stubClnTransp) SendRequest(
	ctx context.Context,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	var reqCopy *sip.RequestEnvelope
	if req != nil {
		reqCopy = req.Clone().(*sip.RequestEnvelope)
	}

	call := sendReqCall{
		ctx:  ctx,
		req:  reqCopy,
		opts: util.LastSliceElem(opts, sip.SendRequestOptions{}),
	}

	tp.mu.Lock()
	idx := tp.sendReqCnt
	tp.sendReqCnt++
	hook := tp.sendReqHook
	tp.mu.Unlock()

	if hook != nil {
		if err := hook(call, idx); err != nil {
			return errors.Wrap(err)
		}
	}

	tp.sendReqCalls <- call

	return nil
}

type sendResCall struct {
	ctx context.Context
	res *sip.ResponseEnvelope
}

func newInInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.RequestEnvelope {
	tb.Helper()

	return siptest.NewInboundInvite(tb, tp, branch, locAddr, rmtAddr)
}

func newOutInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.RequestEnvelope {
	tb.Helper()

	return siptest.NewInboundInvite(tb, tp, branch, locAddr, locAddr).
		SetRemoteAddr(rmtAddr)
}

func newInRes(tb testing.TB, req *sip.RequestEnvelope, sts sip.ResponseStatus) *sip.ResponseEnvelope {
	tb.Helper()

	return siptest.NewInboundResponse(tb, req, sts)
}

//nolint:unparam // signature mirrors the sibling INVITE fixture
func newInNonInviteReq(
	tb testing.TB,
	tp sip.TransportProto,
	branch string,
	locAddr, rmtAddr netip.AddrPort,
) *sip.RequestEnvelope {
	tb.Helper()

	return markReqNonInvite(newInInviteReq(tb, tp, branch, locAddr, rmtAddr))
}

// markReqNonInvite turns an INVITE fixture request into an INFO request by
// rewriting the method and the CSeq method field.
func markReqNonInvite(req *sip.RequestEnvelope) *sip.RequestEnvelope {
	req.WithMessage(func(msg *sip.Request) {
		msg.Method = sip.RequestMethodInfo
		if cseq, ok := msg.Headers.CSeq(); ok {
			msg.Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: sip.RequestMethodInfo})
		}
	})
	return req
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
